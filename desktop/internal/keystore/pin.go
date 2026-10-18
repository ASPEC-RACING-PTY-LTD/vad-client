package keystore

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/vdcrypt"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

var (
	// ErrNotPinned is returned when no server identity is pinned yet.
	ErrNotPinned = client.ErrNoSIKPin
	// ErrBadIdentity is returned for a malformed server identity or a SEK
	// whose signature does not verify under the SIK.
	ErrBadIdentity = errors.New("keystore: server identity is malformed or its escrow key signature does not verify")
	// ErrPinFile is returned when sik_pin.json cannot be parsed.
	ErrPinFile = errors.New("keystore: pinned server identity file is invalid")
)

// MismatchError reports that the server identity differs from the pinned
// one. It unwraps to a 409 sik_mismatch *client.APIError, so
// client.IsCode(err, wire.CodeSIKMismatch) holds.
type MismatchError struct {
	Pinned string // pinned fingerprint
	Got    string // fingerprint derived from the server's sik_pub
	api    *client.APIError
}

func newMismatch(pinned, got string) *MismatchError {
	return &MismatchError{Pinned: pinned, Got: got, api: &client.APIError{
		Status: 409, Code: wire.CodeSIKMismatch,
		Message: "the server's identity key does not match the one pinned at enrolment; backup is stopped until it is re-pinned with an administrator's confirmation",
	}}
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("keystore: server identity mismatch (pinned %s, server %s)", e.Pinned, e.Got)
}

// Unwrap returns the sik_mismatch API error.
func (e *MismatchError) Unwrap() error { return e.api }

// Fingerprint is the SIK fingerprint (api-v1 §14 rev 1.1b): uppercase hex
// of the first 16 bytes of SHA-256(sik_pub), in 8 groups of 4 joined by "-".
func Fingerprint(sikPub ed25519.PublicKey) string {
	sum := sha256.Sum256(sikPub)
	h := strings.ToUpper(hex.EncodeToString(sum[:16]))
	groups := make([]string, 0, 8)
	for i := 0; i < len(h); i += 4 {
		groups = append(groups, h[i:i+4])
	}
	return strings.Join(groups, "-")
}

// Identity is a parsed, checked server identity.
type Identity struct {
	SIKPub      ed25519.PublicKey
	Fingerprint string // derived from SIKPub
	SEKPub      vdcrypt.Key
	SEKVersion  uint32
	SEKSig      []byte
}

// ParseIdentity decodes id, checks the key sizes, the SEK signature and that
// the advertised fingerprint matches the one derived from sik_pub.
func ParseIdentity(id *wire.ServerIdentity) (*Identity, error) {
	if id == nil {
		return nil, ErrBadIdentity
	}
	sik, err := base64.StdEncoding.DecodeString(id.SIKPub)
	if err != nil || len(sik) != ed25519.PublicKeySize {
		return nil, ErrBadIdentity
	}
	sekRaw, err := base64.StdEncoding.DecodeString(id.SEKPub)
	if err != nil {
		return nil, ErrBadIdentity
	}
	sek, err := vdcrypt.KeyFromBytes(sekRaw)
	if err != nil {
		return nil, ErrBadIdentity
	}
	sig, err := base64.StdEncoding.DecodeString(id.SEKSig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, ErrBadIdentity
	}
	fp := Fingerprint(sik)
	if err := client.CheckSIKPin(fp, id); err != nil {
		return nil, newMismatch(fp, id.SIKFingerprint)
	}
	if !vdcrypt.VerifySEK(sik, sek, id.SEKVersion, sig) {
		return nil, ErrBadIdentity
	}
	return &Identity{SIKPub: sik, Fingerprint: fp, SEKPub: sek, SEKVersion: id.SEKVersion, SEKSig: sig}, nil
}

// pinRecord is sik_pin.json.
type pinRecord struct {
	Fingerprint string `json:"fingerprint"`
	SIKPub      []byte `json:"sik_pub"`
	PinnedAt    int64  `json:"pinned_at"` // Unix milliseconds
}

func loadPin(path string) (*pinRecord, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("keystore: read pin: %w", err)
	}
	var p pinRecord
	if err := json.Unmarshal(b, &p); err != nil || len(p.SIKPub) != ed25519.PublicKeySize {
		return nil, ErrPinFile
	}
	// The stored fingerprint must be the one derived from the stored key.
	if client.CheckSIKPin(p.Fingerprint, &wire.ServerIdentity{SIKFingerprint: Fingerprint(p.SIKPub)}) != nil {
		return nil, ErrPinFile
	}
	return &p, nil
}

func (ks *Keystore) savePin(p *pinRecord) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(ks.opts.Dir, PinFile), b)
}

// Pinned returns the pinned fingerprint, or "" when nothing is pinned.
func (ks *Keystore) Pinned() string {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.pin == nil {
		return ""
	}
	return ks.pin.Fingerprint
}

// pinnedSIK returns the pinned SIK public key.
func (ks *Keystore) pinnedSIK() (ed25519.PublicKey, error) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.pin == nil {
		return nil, ErrNotPinned
	}
	return ed25519.PublicKey(append([]byte(nil), ks.pin.SIKPub...)), nil
}

// PinServerIdentity pins id on first use (enrolment, api-v1 §5A). When a pin
// exists it must match; a different identity is a *MismatchError and is
// never overwritten here (use Repin).
func (ks *Keystore) PinServerIdentity(id *wire.ServerIdentity) (*Identity, error) {
	ident, err := ParseIdentity(id)
	if err != nil {
		return nil, err
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.pin != nil {
		if err := matchPin(ks.pin, ident); err != nil {
			return nil, err
		}
		return ident, nil
	}
	return ident, ks.pinLocked(ident)
}

// Repin replaces the pin with id. The caller must have the user's and an
// administrator's confirmation (api-v1 §5A).
func (ks *Keystore) Repin(id *wire.ServerIdentity) (*Identity, error) {
	ident, err := ParseIdentity(id)
	if err != nil {
		return nil, err
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	old := ""
	if ks.pin != nil {
		old = ks.pin.Fingerprint
	}
	if err := ks.pinLocked(ident); err != nil {
		return nil, err
	}
	ks.opts.Logger.Warn("keystore: server identity re-pinned", "old", old, "new", ident.Fingerprint)
	return ident, nil
}

func (ks *Keystore) pinLocked(ident *Identity) error {
	p := &pinRecord{Fingerprint: ident.Fingerprint, SIKPub: append([]byte(nil), ident.SIKPub...),
		PinnedAt: ks.opts.Now().UnixMilli()}
	if err := ks.savePin(p); err != nil {
		return err
	}
	ks.pin = p
	ks.opts.Logger.Info("keystore: server identity pinned", "fingerprint", p.Fingerprint)
	return nil
}

// Verify checks id against the pin: the fingerprint string, the raw sik_pub
// (constant time) and the fingerprint re-derived from sik_pub. A difference
// is a *MismatchError; backup must stop until Repin.
func (ks *Keystore) Verify(id *wire.ServerIdentity) (*Identity, error) {
	ks.mu.Lock()
	p := ks.pin
	ks.mu.Unlock()
	if p == nil {
		return nil, ErrNotPinned
	}
	if id == nil {
		return nil, ErrBadIdentity
	}
	if err := client.CheckSIKPin(p.Fingerprint, id); err != nil {
		return nil, newMismatch(p.Fingerprint, id.SIKFingerprint)
	}
	ident, err := ParseIdentity(id)
	if err != nil {
		return nil, err
	}
	if err := matchPin(p, ident); err != nil {
		return nil, err
	}
	return ident, nil
}

func matchPin(p *pinRecord, ident *Identity) error {
	fpOK := client.CheckSIKPin(p.Fingerprint, &wire.ServerIdentity{SIKFingerprint: ident.Fingerprint}) == nil
	keyOK := subtle.ConstantTimeCompare(p.SIKPub, ident.SIKPub) == 1
	if !fpOK || !keyOK {
		return newMismatch(p.Fingerprint, ident.Fingerprint)
	}
	return nil
}
