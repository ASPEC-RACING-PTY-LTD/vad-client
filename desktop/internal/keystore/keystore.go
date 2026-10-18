// Package keystore holds the device's repository key material (MASTER_PLAN
// §7.2, backup-format §4): the X25519 box key sealed with machine DPAPI, the
// pinned server identity (SIK), first-device repository init, opening a
// repository with the box key, and the key_share and rewrap device tasks
// (api-v1 §5A, §14 rev 1.1b).
package keystore

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/platform"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/vdcrypt"
)

// File names inside Options.Dir.
const (
	BoxKeyFile = "box.key"
	PinFile    = "sik_pin.json"
)

// boxEntropy is the DPAPI entropy of the box key file.
var boxEntropy = []byte("VaultDock box key v1")

var (
	ErrClosed    = errors.New("keystore: closed")
	ErrBoxKey    = errors.New("keystore: device box key file is invalid")
	ErrNoBackend = errors.New("keystore: no repository backend hook configured")
)

// Protector seals secrets at rest. The default is MachineDPAPI.
type Protector interface {
	Protect(data, entropy []byte) ([]byte, error)
	Unprotect(data, entropy []byte) ([]byte, error)
}

// MachineDPAPI is machine-scope DPAPI (platform.ProtectMachine). The file
// holding its output relies on the installer's ACL on the keys directory
// (SYSTEM and Administrators only).
type MachineDPAPI struct{}

// Protect implements Protector.
func (MachineDPAPI) Protect(data, entropy []byte) ([]byte, error) {
	return platform.ProtectMachine(data, entropy)
}

// Unprotect implements Protector.
func (MachineDPAPI) Unprotect(data, entropy []byte) ([]byte, error) {
	return platform.UnprotectMachine(data, entropy)
}

// Options configure a Keystore.
type Options struct {
	// Dir holds box.key and sik_pin.json; "" = platform.KeysDir().
	Dir string
	// Protector seals the box key; nil = MachineDPAPI.
	Protector Protector
	// Backend returns the repository backend for a server repo id; needed
	// by HandleTask.
	Backend func(ctx context.Context, repoID int64) (repo.Backend, error)
	// Revoked returns this device's view of revoked device ids; rewrap
	// excludes their key files and key_share refuses them. nil = none.
	Revoked func(ctx context.Context) (map[int64]bool, error)
	Rand    io.Reader        // nil = crypto/rand
	Now     func() time.Time // nil = time.Now
	Logger  *slog.Logger     // nil = slog.Default()
}

// Keystore is the device key store. Methods are safe for concurrent use.
type Keystore struct {
	opts Options

	mu      sync.Mutex
	boxPriv vdcrypt.Key
	boxPub  vdcrypt.Key
	closed  bool
	pin     *pinRecord
}

// Open loads the box key from Dir, creating and sealing a fresh one when
// none exists, and loads the SIK pin if present.
func Open(opts Options) (*Keystore, error) {
	if opts.Dir == "" {
		opts.Dir = platform.KeysDir()
	}
	if opts.Protector == nil {
		opts.Protector = MachineDPAPI{}
	}
	if opts.Rand == nil {
		opts.Rand = rand.Reader
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("keystore: %w", err)
	}
	ks := &Keystore{opts: opts}
	if err := ks.loadBoxKey(); err != nil {
		return nil, err
	}
	pin, err := loadPin(filepath.Join(opts.Dir, PinFile))
	if err != nil {
		ks.Close()
		return nil, err
	}
	ks.pin = pin
	return ks, nil
}

func (ks *Keystore) loadBoxKey() error {
	path := filepath.Join(ks.opts.Dir, BoxKeyFile)
	sealed, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ks.createBoxKey(path)
	}
	if err != nil {
		return fmt.Errorf("keystore: read box key: %w", err)
	}
	raw, err := ks.opts.Protector.Unprotect(sealed, boxEntropy)
	if err != nil {
		return fmt.Errorf("keystore: unseal box key: %w", err)
	}
	defer clear(raw)
	priv, err := vdcrypt.KeyFromBytes(raw)
	if err != nil {
		return ErrBoxKey
	}
	pub, err := vdcrypt.X25519Public(priv)
	if err != nil {
		priv.Wipe()
		return ErrBoxKey
	}
	ks.boxPriv, ks.boxPub = priv, pub
	return nil
}

func (ks *Keystore) createBoxKey(path string) error {
	priv, pub, err := vdcrypt.GenerateX25519(ks.opts.Rand)
	if err != nil {
		return fmt.Errorf("keystore: generate box key: %w", err)
	}
	sealed, err := ks.opts.Protector.Protect(priv[:], boxEntropy)
	if err != nil {
		priv.Wipe()
		return fmt.Errorf("keystore: seal box key: %w", err)
	}
	// Never replace a key another process created meanwhile.
	if _, err := os.Stat(path); err == nil {
		priv.Wipe()
		return ks.loadBoxKey()
	}
	if err := writeFileAtomic(path, sealed); err != nil {
		priv.Wipe()
		return err
	}
	ks.boxPriv, ks.boxPub = priv, pub
	ks.opts.Logger.Info("keystore: created device box key", "dir", ks.opts.Dir)
	return nil
}

// BoxPub returns the device's X25519 box public key.
func (ks *Keystore) BoxPub() vdcrypt.Key {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	return ks.boxPub
}

// BoxPubB64 returns BoxPub as std base64 (wire.EnrolRequest.BoxPub).
func (ks *Keystore) BoxPubB64() string {
	pub := ks.BoxPub()
	return base64.StdEncoding.EncodeToString(pub[:])
}

// boxPrivate returns a copy of the box private key; the caller wipes it.
func (ks *Keystore) boxPrivate() (vdcrypt.Key, error) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.closed {
		return vdcrypt.Key{}, ErrClosed
	}
	return ks.boxPriv, nil
}

// Close wipes the box private key from memory.
func (ks *Keystore) Close() {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ks.boxPriv.Wipe()
	ks.closed = true
}

// writeFileAtomic writes data to a temp file in the same directory with mode
// 0600, syncs it and renames it over path. Directory ACLs come from the
// installer (platform.EnsureMachineDirs).
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("keystore: %w", err)
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	// CreateTemp opens with mode 0600; on Windows the inherited ACL is what
	// protects the file.
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("keystore: write %s: %w", filepath.Base(path), err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("keystore: sync %s: %w", filepath.Base(path), err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("keystore: close %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("keystore: replace %s: %w", filepath.Base(path), err)
	}
	ok = true
	return nil
}
