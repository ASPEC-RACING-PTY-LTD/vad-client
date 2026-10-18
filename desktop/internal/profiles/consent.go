package profiles

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/tokens"
)

// ScopeProfile is the only consent scope today: "Allow VaultDock to protect
// my profile" (§7.8). The resulting state (active_default or authorised) is
// decided by the agent's policy, not by the claim.
const ScopeProfile = "profile"

// MaxProofSize bounds a consent proof received from a client.
const MaxProofSize = 16 << 10

const (
	claimVersion   = 1
	maxClaimFuture = 10 * time.Minute // clock skew between UI and agent
	maxClaimAge    = 24 * time.Hour   // a new consent must be fresh
)

var (
	// ErrNeedSession: verification needs the user's session token. Under
	// an S4U token (or no session) the proof is simply not checked; this is
	// never a security signal.
	ErrNeedSession = errors.New("profiles: consent verification needs the user's session")
	// ErrDPAPIKeyUnavailable: the user's DPAPI master key could not be
	// decrypted (NTE_BAD_KEY_STATE). With a session token this is the
	// password_reset candidate signal (spike P0-S3 §3).
	ErrDPAPIKeyUnavailable = errors.New("profiles: DPAPI master key unavailable")
	// ErrConsentInvalid: tampered or foreign blob, wrong device, or claim
	// mismatch.
	ErrConsentInvalid = errors.New("profiles: consent proof invalid")
	// ErrConsentStale: the claim time is outside the accepted window.
	ErrConsentStale = errors.New("profiles: consent proof is stale")
)

// ConsentClaim is the plaintext sealed in the consent proof with
// user-scope DPAPI (§7.8). Time is unix ms.
type ConsentClaim struct {
	V         int    `json:"v"`
	SID       string `json:"sid"`
	DeviceID  string `json:"device_id"`
	ConsentID string `json:"consent_id"`
	Time      int64  `json:"time"`
	Scope     string `json:"scope"`
}

// consentEntropy binds the blob to this device.
func consentEntropy(deviceID string) []byte {
	return []byte("VaultDock.consent.v1\x00" + deviceID)
}

// ProofDigest is the hex SHA-256 of a consent blob (api-v1 §6B
// proof_digest).
func ProofDigest(blob []byte) string {
	d := sha256.Sum256(blob)
	return hex.EncodeToString(d[:])
}

// NewConsentID returns a random consent id (32 hex chars).
func NewConsentID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// CreateConsentProof runs in the user's UI process, in the user's own
// session (user-scope DPAPI needs it): it seals a new claim for sid on
// deviceID and returns the blob to send to the agent. sid must be the
// process's own user.
func CreateConsentProof(deviceID, sid string) ([]byte, error) {
	if deviceID == "" {
		return nil, errors.New("profiles: empty device id")
	}
	if !tokens.IsUserSID(sid) {
		return nil, ErrInvalidSID
	}
	self, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	if self != sid {
		return nil, fmt.Errorf("profiles: consent must be created by the profile's own user")
	}
	id, err := NewConsentID()
	if err != nil {
		return nil, err
	}
	pt, err := json.Marshal(ConsentClaim{V: claimVersion, SID: sid, DeviceID: deviceID, ConsentID: id,
		Time: time.Now().UnixMilli(), Scope: ScopeProfile})
	if err != nil {
		return nil, err
	}
	return protect(pt, consentEntropy(deviceID))
}

// VerifyConsentProof runs in the service. It unprotects blob while
// impersonating tok, which must be the user's *session* token (S4U tokens
// cannot use DPAPI: ErrNeedSession, never a signal), and checks that the
// claim is version 1, for sid (== tok.SID()) and deviceID, with scope
// ScopeProfile. Field comparisons are constant time. Freshness is not
// checked here (re-verification of an old proof is normal); see
// CheckFresh.
func VerifyConsentProof(tok tokens.Token, blob []byte, deviceID, sid string) (ConsentClaim, error) {
	var got ConsentClaim
	if len(blob) == 0 || len(blob) > MaxProofSize {
		return got, fmt.Errorf("%w: bad size", ErrConsentInvalid)
	}
	if tok == nil || tok.Source() != tokens.SourceSession {
		return got, ErrNeedSession
	}
	if !eq(tok.SID(), sid) {
		return got, fmt.Errorf("%w: token is for another user", ErrConsentInvalid)
	}
	var pt []byte
	if err := tok.Impersonate(func() error {
		var err error
		pt, err = unprotect(blob, consentEntropy(deviceID))
		return err
	}); err != nil {
		return got, err
	}
	if err := json.Unmarshal(pt, &got); err != nil {
		return ConsentClaim{}, fmt.Errorf("%w: %v", ErrConsentInvalid, err)
	}
	ok := got.V == claimVersion
	ok = eq(got.SID, sid) && ok
	ok = eq(got.DeviceID, deviceID) && ok
	ok = eq(got.Scope, ScopeProfile) && ok
	ok = got.ConsentID != "" && len(got.ConsentID) <= 128 && ok
	if !ok {
		return ConsentClaim{}, fmt.Errorf("%w: claim mismatch", ErrConsentInvalid)
	}
	return got, nil
}

// CheckFresh rejects a claim created more than maxClaimAge ago or more than
// maxClaimFuture ahead of now (used when a new consent is recorded).
func CheckFresh(c ConsentClaim, now time.Time) error {
	t := time.UnixMilli(c.Time)
	if t.After(now.Add(maxClaimFuture)) || t.Before(now.Add(-maxClaimAge)) {
		return ErrConsentStale
	}
	return nil
}

func eq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
