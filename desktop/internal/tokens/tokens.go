// Package tokens obtains Windows access tokens for user profiles so the
// agent (running as SYSTEM) can read and write files as that user
// (MASTER_PLAN §7.8, spike P0-S3): the user's session token while they are
// logged on (WTSQueryUserToken, active or disconnected session), otherwise an
// S4U logon token for the SID (MSV1_0 for local and Microsoft-account-linked
// accounts, Kerberos via Negotiate for domain accounts).
//
// Every token is normalised: BUILTIN\Administrators is deny-only and all
// privileges are stripped, so access equals what the user's standard
// (UAC-filtered) identity gets. The real provider is Windows-only
// (tokens_windows.go); other platforms keep the Unsupported stub.
package tokens

import (
	"strings"
)

// Source says how a Token was obtained.
type Source int

const (
	// SourceSession is a WTSQueryUserToken token: a full logon session that
	// has the user's credentials (DPAPI, EFS work).
	SourceSession Source = iota + 1
	// SourceS4U is an LsaLogonUser S4U token: identity only, no credentials.
	SourceS4U
)

func (s Source) String() string {
	switch s {
	case SourceSession:
		return "session"
	case SourceS4U:
		return "s4u"
	}
	return "unknown"
}

// Caps lists what a token can be relied on for (spike P0-S3 §2).
type Caps struct {
	Files   bool // ACL-governed file reads and writes
	DPAPI   bool // CryptUnprotectData of user-scope blobs
	EFS     bool // transparent EFS decryption
	Network bool // outbound auth as the user (never used by VaultDock)
}

// CapsFor returns the capabilities of a token from source s.
func CapsFor(s Source) Caps {
	switch s {
	case SourceSession:
		return Caps{Files: true, DPAPI: true, EFS: true, Network: true}
	case SourceS4U:
		return Caps{Files: true}
	}
	return Caps{}
}

// Token is a filtered user token for one profile. Implementations must be
// safe to Close more than once. Get one token per job per profile: every S4U
// token is a new LSA logon session (Security event 4624), so never mint one
// per file.
type Token interface {
	// SID is the profile owner's SID string.
	SID() string
	// Source says whether this is a session or an S4U token.
	Source() Source
	// SessionID is the Windows session; ok is false for S4U tokens.
	SessionID() (id uint32, ok bool)
	// Caps is CapsFor(Source()).
	Caps() Caps
	// Impersonate runs fn on a locked OS thread impersonating the token
	// and reverts before returning. fn's error is returned unchanged. fn
	// must not start goroutines that expect the identity and must not do
	// network I/O; batch work (one directory listing, one file open and
	// bounded read) rather than one call per tiny operation.
	Impersonate(fn func() error) error
	// AccessCheckPath reports whether the user would be granted mask
	// (file access rights) on path's current security descriptor. Used to
	// gate VSS snapshot reads made as SYSTEM (C7).
	AccessCheckPath(path string, mask uint32) (bool, error)
	// Close releases the token handles.
	Close() error
}

// Provider issues tokens for profiles. Errors wrap the sentinels in
// errors.go (use errors.Is).
type Provider interface {
	// ForProfile prefers a session token, then falls back to S4U. The
	// caller must Close the token.
	ForProfile(sid string) (Token, error)
	// SessionOnly never falls back to S4U (consent verification, DPAPI,
	// EFS). It returns ErrNotLoggedOn when the user has no session.
	SessionOnly(sid string) (Token, error)
}

// Unsupported is the stub Provider: it validates the SID and then returns
// ErrUnsupported. It is the Default on non-Windows builds.
type Unsupported struct{}

// ForProfile implements Provider.
func (Unsupported) ForProfile(sid string) (Token, error) {
	if err := ValidateSID(sid); err != nil {
		return nil, err
	}
	return nil, ErrUnsupported
}

// SessionOnly implements Provider.
func (Unsupported) SessionOnly(sid string) (Token, error) { return Unsupported{}.ForProfile(sid) }

// Default is the Provider used by ForProfile and SessionOnly. On Windows it
// is the session/S4U provider (tokens_windows.go).
var Default Provider = Unsupported{}

// ForProfile returns a token for the profile identified by sid using the
// Default provider. The caller must Close it.
func ForProfile(sid string) (Token, error) { return Default.ForProfile(sid) }

// SessionOnly returns a session token for sid using the Default provider.
func SessionOnly(sid string) (Token, error) { return Default.SessionOnly(sid) }

// ValidateSID performs a syntactic check of a string SID ("S-1-…"); it
// does not look the account up.
func ValidateSID(sid string) error {
	parts := strings.Split(sid, "-")
	if len(parts) < 3 || len(parts) > 18 || parts[0] != "S" || parts[1] != "1" {
		return ErrInvalidSID
	}
	for i, p := range parts[1:] {
		max := 10
		if i == 1 { // identifier authority
			max = 14
		}
		if p == "" || len(p) > max {
			return ErrInvalidSID
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return ErrInvalidSID
			}
		}
	}
	return nil
}

// IsUserSID reports whether sid has the shape of a real user account that
// can own a profile: a local/domain account (S-1-5-21-…) or an Entra ID
// account (S-1-12-1-…). Well-known service SIDs (S-1-5-18/19/20) are not.
func IsUserSID(sid string) bool {
	if ValidateSID(sid) != nil {
		return false
	}
	return strings.HasPrefix(sid, "S-1-5-21-") || strings.HasPrefix(sid, "S-1-12-1-")
}
