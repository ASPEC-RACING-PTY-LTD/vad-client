package agent

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/platform"
)

// App lock: the device key that signs every request to the server is sealed
// with a random lock key. The lock key is wrapped by a key derived from the
// user's 6-digit PIN (Argon2id) and, optionally, by a Windows Hello key the
// UI keeps in the user's Hello container (TPM-backed; only Hello can
// decrypt it). Every agent start (Windows start, restart, update) begins
// locked: nothing reaches the server until the user unlocks with the PIN or
// Windows Hello. The lock file itself is also DPAPI-protected.

// CodeLocked is the error code of calls refused while the app is locked.
const CodeLocked = "locked"

// ErrLocked is returned while the app lock is set and not unlocked.
var ErrLocked = &Error{Code: CodeLocked, Message: "VaultDock is locked. Unlock it with your PIN or Windows Hello."}

// PINLength is the length of the app lock PIN (digits only).
const PINLength = 6

// Failed PIN tries: after freeTries, each further failure doubles the wait,
// up to maxWait.
const (
	freeTries = 5
	maxWait   = 30 * time.Minute
)

// LockStatus describes the app lock to the UI.
type LockStatus struct {
	Enabled bool `json:"enabled"` // a PIN is set
	Locked  bool `json:"locked"`
	Hello   bool `json:"hello"` // Windows Hello can unlock
	// HelloKey and HelloBox let the UI unlock with Windows Hello: HelloBox
	// is the lock key encrypted to the Hello key named HelloKey.
	HelloKey   string `json:"hello_key,omitempty"`
	HelloBox   []byte `json:"hello_box,omitempty"`
	RetryAfter int64  `json:"retry_after,omitempty"` // unix ms before another PIN try is accepted
}

// SetupLockRequest sets or changes the PIN. CurrentPIN is needed to change
// an existing PIN.
type SetupLockRequest struct {
	PIN        string `json:"pin"`
	CurrentPIN string `json:"current_pin,omitempty"`
}

// UnlockRequest unlocks with the PIN or, from Windows Hello, the lock key.
type UnlockRequest struct {
	PIN    string `json:"pin,omitempty"`
	Secret []byte `json:"secret,omitempty"`
}

// HelloRequest stores (or, with an empty PublicKey, removes) the Windows
// Hello key: the modulus and exponent of its RSA public key.
type HelloRequest struct {
	KeyName  string `json:"key_name"`
	Modulus  []byte `json:"modulus,omitempty"`
	Exponent []byte `json:"exponent,omitempty"`
}

type lockRecord struct {
	Version  int    `json:"version"`
	Salt     []byte `json:"salt"`
	Time     uint32 `json:"time"`
	MemoryKB uint32 `json:"memory_kb"`
	Threads  uint8  `json:"threads"`
	PINBox   []byte `json:"pin_box"`           // lock key sealed with the PIN key
	CheckBox []byte `json:"check_box"`         // empty value sealed with the lock key
	KeyBox   []byte `json:"key_box,omitempty"` // device key sealed with the lock key
	HelloKey string `json:"hello_key,omitempty"`
	HelloBox []byte `json:"hello_box,omitempty"` // lock key, RSA PKCS#1 v1.5 to the Hello key
	Failures int    `json:"failures,omitempty"`
	WaitTill int64  `json:"wait_till,omitempty"`
}

// appLock holds the lock state; master is the lock key while unlocked.
type appLock struct {
	path   string
	mu     sync.Mutex
	rec    *lockRecord
	master []byte
}

func openAppLock(path string) (*appLock, error) {
	l := &appLock{path: path}
	enc, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	raw, err := platform.Unprotect(enc)
	if err != nil {
		return nil, fmt.Errorf("app lock file: %w", err)
	}
	var rec lockRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("app lock file: %w", err)
	}
	l.rec = &rec
	return l, nil
}

func (l *appLock) saveLocked() error {
	raw, err := json.Marshal(l.rec)
	if err != nil {
		return err
	}
	enc, err := platform.Protect(raw)
	if err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, enc, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}

func (l *appLock) enabled() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rec != nil
}

func (l *appLock) locked() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rec != nil && l.master == nil
}

func (l *appLock) status() LockStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rec == nil {
		return LockStatus{}
	}
	st := LockStatus{Enabled: true, Locked: l.master == nil, Hello: len(l.rec.HelloBox) > 0}
	if st.Hello {
		st.HelloKey, st.HelloBox = l.rec.HelloKey, l.rec.HelloBox
	}
	if l.rec.WaitTill > time.Now().UnixMilli() {
		st.RetryAfter = l.rec.WaitTill
	}
	return st
}

// ValidPIN reports whether pin is exactly PINLength digits.
func ValidPIN(pin string) bool {
	if len(pin) != PINLength {
		return false
	}
	for _, c := range pin {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func pinKey(rec *lockRecord, pin string) []byte {
	return argon2.IDKey([]byte(pin), rec.Salt, rec.Time, rec.MemoryKB, rec.Threads, 32)
}

func seal(key, plain []byte, aad string) ([]byte, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plain, []byte(aad)), nil
}

func open(key, box []byte, aad string) ([]byte, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	if len(box) < g.NonceSize() {
		return nil, errors.New("sealed data too short")
	}
	return g.Open(nil, box[:g.NonceSize()], box[g.NonceSize():], []byte(aad))
}

const (
	aadPIN   = "vaultdock applock pin"
	aadKey   = "vaultdock applock device key"
	aadCheck = "vaultdock applock check"
	lockSize = 32
)

// setup sets the PIN. Without a lock yet it creates the lock key and seals
// current (the device key, nil when not connected); with one it needs the
// app unlocked and the current PIN.
func (l *appLock) setup(req SetupLockRequest, current ed25519.PrivateKey) error {
	if !ValidPIN(req.PIN) {
		return invalidf(fmt.Sprintf("the PIN must be %d digits", PINLength))
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	master := l.master
	if l.rec != nil {
		if master == nil {
			return ErrLocked
		}
		key := pinKey(l.rec, req.CurrentPIN)
		if _, err := open(key, l.rec.PINBox, aadPIN); err != nil {
			return invalidf("the current PIN is not correct")
		}
	} else {
		master = make([]byte, lockSize)
		if _, err := rand.Read(master); err != nil {
			return err
		}
	}
	rec := &lockRecord{Version: 1, Salt: make([]byte, 16), Time: 3, MemoryKB: 64 * 1024, Threads: 4}
	if _, err := rand.Read(rec.Salt); err != nil {
		return err
	}
	pinBox, err := seal(pinKey(rec, req.PIN), master, aadPIN)
	if err != nil {
		return err
	}
	rec.PINBox = pinBox
	if rec.CheckBox, err = seal(master, nil, aadCheck); err != nil {
		return err
	}
	if l.rec != nil {
		rec.KeyBox, rec.HelloKey, rec.HelloBox = l.rec.KeyBox, l.rec.HelloKey, l.rec.HelloBox
	} else if current != nil {
		if rec.KeyBox, err = seal(master, current, aadKey); err != nil {
			return err
		}
	}
	prev := l.rec
	l.rec = rec
	if err := l.saveLocked(); err != nil {
		l.rec = prev
		return err
	}
	l.master = master
	return nil
}

// unlock opens the lock with the PIN or the lock key from Windows Hello.
func (l *appLock) unlock(req UnlockRequest) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rec == nil || l.master != nil {
		return nil
	}
	now := time.Now()
	if len(req.Secret) > 0 {
		// The lock key from Windows Hello: valid when it opens the check box.
		if len(req.Secret) != lockSize || !l.checkMaster(req.Secret) {
			return invalidf("Windows Hello did not return this PC's unlock key")
		}
		l.master = append([]byte(nil), req.Secret...)
		l.rec.Failures, l.rec.WaitTill = 0, 0
		_ = l.saveLocked()
		return nil
	}
	if l.rec.WaitTill > now.UnixMilli() {
		wait := time.Until(time.UnixMilli(l.rec.WaitTill)).Round(time.Second)
		return &Error{Code: CodeInvalid, Message: fmt.Sprintf("Too many wrong PINs. Try again in %s.", wait)}
	}
	if !ValidPIN(req.PIN) {
		return invalidf(fmt.Sprintf("enter your %d-digit PIN", PINLength))
	}
	master, err := open(pinKey(l.rec, req.PIN), l.rec.PINBox, aadPIN)
	if err != nil {
		l.rec.Failures++
		if over := l.rec.Failures - freeTries; over >= 0 {
			wait := min(maxWait, time.Duration(1<<min(over, 20))*30*time.Second)
			l.rec.WaitTill = now.Add(wait).UnixMilli()
		}
		_ = l.saveLocked()
		left := freeTries - l.rec.Failures
		if left > 0 {
			return invalidf(fmt.Sprintf("wrong PIN (%d tries left before a wait)", left))
		}
		return invalidf("wrong PIN")
	}
	l.master = master
	l.rec.Failures, l.rec.WaitTill = 0, 0
	_ = l.saveLocked()
	return nil
}

// checkMaster reports whether m is the lock key: it opens the check box.
func (l *appLock) checkMaster(m []byte) bool {
	_, err := open(m, l.rec.CheckBox, aadCheck)
	return err == nil
}

// lock forgets the lock key.
func (l *appLock) lock() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.master != nil {
		clear(l.master)
		l.master = nil
	}
}

// setHello stores the Windows Hello key and the lock key encrypted to it
// (needs the app unlocked); an empty modulus removes it.
func (l *appLock) setHello(req HelloRequest) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rec == nil {
		return invalidf("set a PIN first")
	}
	if l.master == nil {
		return ErrLocked
	}
	if len(req.Modulus) == 0 {
		l.rec.HelloKey, l.rec.HelloBox = "", nil
		return l.saveLocked()
	}
	if strings.TrimSpace(req.KeyName) == "" || len(req.Modulus) < 256 || len(req.Exponent) == 0 || len(req.Exponent) > 8 {
		return invalidf("the Windows Hello key is not valid")
	}
	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(req.Modulus), E: int(new(big.Int).SetBytes(req.Exponent).Int64())}
	box, err := rsa.EncryptPKCS1v15(rand.Reader, pub, l.master)
	if err != nil {
		return err
	}
	l.rec.HelloKey, l.rec.HelloBox = req.KeyName, box
	return l.saveLocked()
}

// deviceKey opens the sealed device key.
func (l *appLock) deviceKey() (ed25519.PrivateKey, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.master == nil {
		return nil, ErrLocked
	}
	if len(l.rec.KeyBox) == 0 {
		return nil, os.ErrNotExist
	}
	raw, err := open(l.master, l.rec.KeyBox, aadKey)
	if err != nil {
		return nil, fmt.Errorf("device key: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, errors.New("device key is corrupt")
	}
	return ed25519.PrivateKey(raw), nil
}

func (l *appLock) setDeviceKey(k ed25519.PrivateKey) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.master == nil {
		return ErrLocked
	}
	var err error
	prev := l.rec.KeyBox
	if k == nil {
		l.rec.KeyBox = nil
	} else if l.rec.KeyBox, err = seal(l.master, k, aadKey); err != nil {
		l.rec.KeyBox = prev
		return err
	}
	if err := l.saveLocked(); err != nil {
		l.rec.KeyBox = prev
		return err
	}
	return nil
}

// lockedKeys is the KeyStore used by the Host: the sealed key while an app
// lock is set, the plain (DPAPI) store until then.
type lockedKeys struct {
	inner KeyStore
	lock  *appLock
}

func (k lockedKeys) Load() (ed25519.PrivateKey, error) {
	if k.lock.enabled() {
		return k.lock.deviceKey()
	}
	return k.inner.Load()
}

func (k lockedKeys) Save(key ed25519.PrivateKey) error {
	if k.lock.enabled() {
		return k.lock.setDeviceKey(key)
	}
	return k.inner.Save(key)
}

func (k lockedKeys) Delete() error {
	if k.lock.enabled() && !k.lock.locked() {
		if err := k.lock.setDeviceKey(nil); err != nil {
			return err
		}
	}
	return k.inner.Delete()
}

// --- Host API ---

// LockStatus reports the app lock.
func (h *Host) LockStatus(ctx context.Context) (LockStatus, error) {
	if err := h.live(); err != nil {
		return LockStatus{}, err
	}
	return h.lock.status(), nil
}

// SetupLock sets or changes the PIN. Setting it the first time moves the
// device key into the lock.
func (h *Host) SetupLock(ctx context.Context, req SetupLockRequest) error {
	if err := h.live(); err != nil {
		return err
	}
	var current ed25519.PrivateKey
	first := !h.lock.enabled()
	if first {
		if k, err := h.plainKeys.Load(); err == nil {
			current = k
		}
	}
	if err := h.lock.setup(req, current); err != nil {
		return err
	}
	if first && current != nil {
		if err := h.plainKeys.Delete(); err != nil {
			h.log.Warn("could not remove the unlocked device key copy", "err", err)
		}
	}
	h.log.Info("app lock PIN set", "first", first)
	_ = h.bus.Publish(KindConnection, ConnectionEvent{State: "unlocked"})
	return nil
}

// Unlock opens the app lock and reconnects to the server.
func (h *Host) Unlock(ctx context.Context, req UnlockRequest) error {
	if err := h.live(); err != nil {
		return err
	}
	if err := h.lock.unlock(req); err != nil {
		h.log.Warn("app unlock failed", "err", err)
		return err
	}
	h.log.Info("app unlocked")
	h.mu.Lock()
	connected := h.client != nil
	h.mu.Unlock()
	if !connected {
		h.goLoop(h.restoreConnection)
	}
	_ = h.bus.Publish(KindConnection, ConnectionEvent{State: "unlocked"})
	return nil
}

// Lock locks the app now: the server connection stops until unlocked.
func (h *Host) Lock(ctx context.Context) error {
	if err := h.live(); err != nil {
		return err
	}
	if !h.lock.enabled() {
		return invalidf("set a PIN first")
	}
	h.lockNow("locked from the app")
	return nil
}

func (h *Host) lockNow(why string) {
	h.lock.lock()
	h.mu.Lock()
	h.client, h.me = nil, nil
	h.mu.Unlock()
	h.stopBackup()
	h.eng.SetClient(nil, nil)
	h.log.Info("app locked", "why", why)
	h.setConnection("locked")
}

// SetHello stores or removes the Windows Hello unlock.
func (h *Host) SetHello(ctx context.Context, req HelloRequest) error {
	if err := h.live(); err != nil {
		return err
	}
	return h.lock.setHello(req)
}
