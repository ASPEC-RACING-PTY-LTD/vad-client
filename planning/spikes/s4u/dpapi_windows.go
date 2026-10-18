package s4u

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	// ErrDPAPIKeyUnavailable: the user's master key could not be decrypted
	// (NTE_BAD_KEY_STATE). With a session token this is the password_reset
	// signal; with an S4U token it is expected and means nothing.
	ErrDPAPIKeyUnavailable = errors.New("consent: DPAPI master key unavailable")
	// ErrConsentInvalid: blob tampered, wrong entropy, or claim mismatch.
	ErrConsentInvalid = errors.New("consent: proof invalid")
	ErrNeedSession    = errors.New("consent: verification needs a session token")
)

// ConsentClaim is the plaintext sealed in the consent proof (§7.8).
type ConsentClaim struct {
	V         int       `json:"v"`
	SID       string    `json:"sid"`
	DeviceID  string    `json:"device_id"`
	ConsentID string    `json:"consent_id"`
	Time      time.Time `json:"time"`
	Scope     string    `json:"scope"`
}

func consentEntropy(deviceID string) []byte {
	return []byte("VaultDock.consent.v1\x00" + deviceID)
}

func blobOf(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func takeBlob(out *windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...)
}

// ProtectConsent runs in the UI process, in the user's own session (no
// impersonation): user-scope DPAPI, entropy bound to the device.
func ProtectConsent(c ConsentClaim) ([]byte, error) {
	pt, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var out windows.DataBlob
	if err := windows.CryptProtectData(blobOf(pt), windows.StringToUTF16Ptr("VaultDock consent"),
		blobOf(consentEntropy(c.DeviceID)), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("CryptProtectData: %w", err)
	}
	return takeBlob(&out), nil
}

// unprotectConsent decrypts in the current thread's security context.
func unprotectConsent(blob []byte, deviceID string) (ConsentClaim, error) {
	var c ConsentClaim
	var out windows.DataBlob
	err := windows.CryptUnprotectData(blobOf(blob), nil, blobOf(consentEntropy(deviceID)), 0, nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	if err != nil {
		if errors.Is(err, syscall.Errno(windows.NTE_BAD_KEY_STATE)) {
			return c, fmt.Errorf("%w: %v", ErrDPAPIKeyUnavailable, err)
		}
		return c, fmt.Errorf("%w: %v", ErrConsentInvalid, err)
	}
	pt := takeBlob(&out)
	if err := json.Unmarshal(pt, &c); err != nil {
		return c, fmt.Errorf("%w: %v", ErrConsentInvalid, err)
	}
	return c, nil
}

// VerifyConsent runs in the service. It impersonates the user's *session*
// token and unprotects the stored blob, then checks the claim. Returns the
// proof_digest (SHA-256 of the blob) reported to the server (api-v1 §6B).
func VerifyConsent(t *Token, blob []byte, want ConsentClaim) ([32]byte, error) {
	digest := sha256.Sum256(blob)
	if t.Source() != SourceSession {
		return digest, ErrNeedSession
	}
	var got ConsentClaim
	err := t.Do(func() error {
		var err error
		got, err = unprotectConsent(blob, want.DeviceID)
		return err
	})
	if err != nil {
		return digest, err
	}
	sidStr := t.SID().String()
	if got.V != 1 || got.SID != sidStr || !eq(got.SID, want.SID) || !eq(got.DeviceID, want.DeviceID) ||
		!eq(got.ConsentID, want.ConsentID) || got.Scope != want.Scope {
		return digest, ErrConsentInvalid
	}
	return digest, nil
}

// VerifyConsentSelf is the no-privilege variant used by the spike CLI: it
// unprotects in the calling process's own context.
func VerifyConsentSelf(blob []byte, want ConsentClaim) ([32]byte, error) {
	digest := sha256.Sum256(blob)
	got, err := unprotectConsent(blob, want.DeviceID)
	if err != nil {
		return digest, err
	}
	if !eq(got.SID, want.SID) || !eq(got.ConsentID, want.ConsentID) {
		return digest, ErrConsentInvalid
	}
	return digest, nil
}

func eq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
