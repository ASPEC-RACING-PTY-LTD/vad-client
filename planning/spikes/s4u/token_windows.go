package s4u

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ErrNotLoggedOn    = errors.New("tokens: user has no interactive session")
	ErrS4UUnsupported = errors.New("tokens: S4U not supported for this account type")
	ErrNoPrivilege    = errors.New("tokens: required privilege not held (need LocalSystem + SeTcbPrivilege)")
	ErrSIDMismatch    = errors.New("tokens: token user does not match requested SID")
	ErrNotUser        = errors.New("tokens: SID is not a user account")
)

// Source says how a Token was obtained.
type Source int

const (
	SourceSession Source = iota + 1 // WTSQueryUserToken: full logon session, has credentials
	SourceS4U                       // LsaLogonUser S4U: identity only, no credentials
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

// Caps lists what a token can be relied on for.
type Caps struct {
	Files   bool // ACL-governed file reads/writes
	DPAPI   bool // CryptUnprotectData of user-scope blobs
	EFS     bool // transparent EFS decryption
	Network bool // outbound SMB/HTTP auth as the user (never used by VaultDock)
}

// Token is a user token normalised for VaultDock: UAC-style filtered
// (Administrators deny-only, privileges stripped) so access equals what the
// user's standard identity gets, plus an impersonation duplicate.
type Token struct {
	sid     *windows.SID
	source  Source
	session uint32
	primary windows.Token // restricted primary token
	imp     windows.Token // SecurityImpersonation duplicate of primary
}

func (t *Token) SID() *windows.SID { return t.sid }
func (t *Token) Source() Source    { return t.source }

// SessionID is valid only for SourceSession.
func (t *Token) SessionID() (uint32, bool) { return t.session, t.source == SourceSession }

func (t *Token) Caps() Caps {
	if t.source == SourceSession {
		return Caps{Files: true, DPAPI: true, EFS: true, Network: true}
	}
	return Caps{Files: true}
}

func (t *Token) Close() error {
	var errs []error
	if t.imp != 0 {
		errs = append(errs, t.imp.Close())
		t.imp = 0
	}
	if t.primary != 0 {
		errs = append(errs, t.primary.Close())
		t.primary = 0
	}
	return errors.Join(errs...)
}

// Do runs fn on a locked OS thread impersonating the user. fn must not start
// goroutines that expect the identity, and must not block for long. If
// RevertToSelf fails the thread stays locked so the runtime discards it
// instead of reusing a thread that still carries the user's identity.
func (t *Token) Do(fn func() error) (err error) {
	runtime.LockOSThread()
	if err := windows.SetThreadToken(nil, t.imp); err != nil {
		runtime.UnlockOSThread()
		return fmt.Errorf("SetThreadToken: %w", err)
	}
	defer func() {
		if rerr := windows.RevertToSelf(); rerr != nil {
			err = errors.Join(err, fmt.Errorf("RevertToSelf: %w", rerr))
			return // deliberately leave the thread locked
		}
		runtime.UnlockOSThread()
	}()
	return fn()
}

// PrimaryDup returns a caller-owned duplicate of the primary token for
// CreateProcessAsUser (needs SeAssignPrimaryTokenPrivilege + SeIncreaseQuotaPrivilege).
func (t *Token) PrimaryDup() (windows.Token, error) {
	var d windows.Token
	err := windows.DuplicateTokenEx(t.primary, windows.MAXIMUM_ALLOWED, nil,
		windows.SecurityImpersonation, windows.TokenPrimary, &d)
	return d, err
}

// File access rights generic mapping.
var fileMapping = struct{ Read, Write, Execute, All uint32 }{0x120089, 0x120116, 0x1200A0, 0x1F01FF}

// AccessCheck reports whether the user would be granted mask on an object
// with security descriptor sd (needs OWNER|GROUP|DACL). Used for VSS
// snapshot reads made as SYSTEM with SeBackupPrivilege (C7).
func (t *Token) AccessCheck(sd *windows.SECURITY_DESCRIPTOR, mask uint32) (bool, error) {
	var (
		privs   [64]byte // PRIVILEGE_SET buffer
		privLen = uint32(len(privs))
		granted uint32
		status  int32
	)
	r, _, e := procAccessCheck.Call(
		uintptr(unsafe.Pointer(sd)), uintptr(t.imp), uintptr(mask),
		uintptr(unsafe.Pointer(&fileMapping)),
		uintptr(unsafe.Pointer(&privs[0])), uintptr(unsafe.Pointer(&privLen)),
		uintptr(unsafe.Pointer(&granted)), uintptr(unsafe.Pointer(&status)))
	if r == 0 {
		return false, fmt.Errorf("AccessCheck: %w", e)
	}
	return status != 0, nil
}

// AccessCheckPath is AccessCheck on a path's current security descriptor.
func (t *Token) AccessCheckPath(path string, mask uint32) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	return t.AccessCheck(sd, mask)
}

// ForProfile returns a token for the user with SID sidStr. It prefers a live
// session token; otherwise it performs an S4U logon. Caller must be
// LocalSystem with SeTcbPrivilege. The caller must Close the token.
func ForProfile(sidStr string) (*Token, error) {
	sid, err := windows.StringToSid(sidStr)
	if err != nil {
		return nil, fmt.Errorf("tokens: bad SID: %w", err)
	}
	if err := enablePrivilege("SeTcbPrivilege"); err != nil {
		return nil, err
	}
	raw, session, err := sessionTokenFor(sid)
	src := SourceSession
	if errors.Is(err, ErrNotLoggedOn) {
		src = SourceS4U
		raw, err = s4uTokenFor(sid)
	}
	if err != nil {
		return nil, err
	}
	defer raw.Close()
	return normalise(raw, sid, src, session)
}

// sessionTokenFor finds a session (active preferred, then disconnected)
// whose user is sid and returns its primary token.
func sessionTokenFor(sid *windows.SID) (windows.Token, uint32, error) {
	var infos *windows.WTS_SESSION_INFO
	var n uint32
	if err := windows.WTSEnumerateSessions(0 /* WTS_CURRENT_SERVER_HANDLE */, 0, 1, &infos, &n); err != nil {
		return 0, 0, fmt.Errorf("WTSEnumerateSessions: %w", err)
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(infos)))

	var best windows.Token
	var bestID uint32
	bestActive := false
	for _, s := range unsafe.Slice(infos, n) {
		if s.State != windows.WTSActive && s.State != windows.WTSDisconnected {
			continue
		}
		var t windows.Token
		if err := windows.WTSQueryUserToken(s.SessionID, &t); err != nil {
			continue // ERROR_NO_TOKEN: nobody logged on in that session
		}
		u, err := t.GetTokenUser()
		if err != nil || !u.User.Sid.Equals(sid) {
			t.Close()
			continue
		}
		active := s.State == windows.WTSActive
		if best == 0 || (active && !bestActive) {
			if best != 0 {
				best.Close()
			}
			best, bestID, bestActive = t, s.SessionID, active
		} else {
			t.Close()
		}
	}
	if best == 0 {
		return 0, 0, ErrNotLoggedOn
	}
	return best, bestID, nil
}

// s4uTokenFor picks the authentication package from the account's domain.
func s4uTokenFor(sid *windows.SID) (windows.Token, error) {
	acct, dom, typ, err := sid.LookupAccount("")
	if err != nil {
		return 0, fmt.Errorf("LookupAccountSid: %w", err)
	}
	if typ != windows.SidTypeUser {
		return 0, ErrNotUser
	}
	pkg, err := packageFor(dom)
	if err != nil {
		return 0, err
	}
	c, err := connectLSA()
	if err != nil {
		return 0, err
	}
	defer c.Close()
	tok, _, err := c.s4uLogon(pkg, acct, dom, logonTypeNetwork)
	return tok, err
}

// packageFor: local SAM accounts (including Microsoft-account-linked ones,
// whose SAM domain is the computer name) use MSV1_0; AD accounts use
// Negotiate/Kerberos (needs a reachable DC); Entra ID ("AzureAD") accounts
// have no S4U path.
func packageFor(domain string) (string, error) {
	host, err := windows.ComputerName()
	if err != nil {
		return "", err
	}
	switch {
	case strings.EqualFold(domain, host):
		return packageMSV, nil
	case strings.EqualFold(domain, "AzureAD"):
		return "", ErrS4UUnsupported
	default:
		return packageNegotiate, nil
	}
}

// normalise verifies the token user, applies UAC-like filtering and makes
// the impersonation duplicate.
func normalise(raw windows.Token, sid *windows.SID, src Source, session uint32) (*Token, error) {
	u, err := raw.GetTokenUser()
	if err != nil {
		return nil, err
	}
	if !u.User.Sid.Equals(sid) {
		return nil, ErrSIDMismatch
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, err
	}
	const disableMaxPrivilege = 0x1
	deny := windows.SIDAndAttributes{Sid: admins}
	var restricted windows.Token
	r, _, e := procCreateRestrictedToken.Call(uintptr(raw), disableMaxPrivilege,
		1, uintptr(unsafe.Pointer(&deny)), 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted)))
	if r == 0 {
		return nil, fmt.Errorf("CreateRestrictedToken: %w", e)
	}
	sidCopy, err := sid.Copy()
	if err != nil {
		restricted.Close()
		return nil, err
	}
	t := &Token{sid: sidCopy, source: src, session: session, primary: restricted}
	if err := windows.DuplicateTokenEx(restricted, windows.TOKEN_QUERY|windows.TOKEN_IMPERSONATE|windows.TOKEN_DUPLICATE,
		nil, windows.SecurityImpersonation, windows.TokenImpersonation, &t.imp); err != nil {
		t.Close()
		return nil, fmt.Errorf("DuplicateTokenEx: %w", err)
	}
	return t, nil
}
