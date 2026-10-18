//go:build windows

package tokens

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

func init() { Default = NewProvider() }

// WindowsProvider is the session/S4U Provider. The process must be
// LocalSystem with SeTcbPrivilege in its token (the SCM RequiredPrivileges
// allowlist, C6); otherwise every call fails with ErrInsufficientPrivilege
// before touching any user session or LSA.
type WindowsProvider struct {
	// isSystem reports whether the process runs as LocalSystem; replaced in
	// tests.
	isSystem func() (bool, error)
}

// NewProvider returns the Windows Provider.
func NewProvider() *WindowsProvider { return &WindowsProvider{isSystem: processIsSystem} }

// ForProfile implements Provider: session token if the user has an active
// or disconnected session, otherwise S4U.
func (p *WindowsProvider) ForProfile(sid string) (Token, error) { return p.get(sid, true) }

// SessionOnly implements Provider.
func (p *WindowsProvider) SessionOnly(sid string) (Token, error) { return p.get(sid, false) }

func (p *WindowsProvider) get(sidStr string, allowS4U bool) (Token, error) {
	if err := ValidateSID(sidStr); err != nil {
		return nil, err
	}
	sid, err := windows.StringToSid(sidStr)
	if err != nil {
		return nil, newErr("StringToSid", sidStr, ErrInvalidSID, err)
	}
	if err := p.requirePrivilege(); err != nil {
		return nil, err
	}
	raw, session, err := sessionTokenFor(sid, sidStr)
	src := SourceSession
	if errors.Is(err, ErrNotLoggedOn) && allowS4U {
		src = SourceS4U
		raw, err = s4uTokenFor(sid, sidStr)
	}
	if err != nil {
		return nil, err
	}
	defer raw.Close()
	return normalise(raw, sid, sidStr, src, session)
}

// requirePrivilege fails fast with ErrInsufficientPrivilege unless the
// process is LocalSystem and SeTcbPrivilege can be enabled.
func (p *WindowsProvider) requirePrivilege() error {
	sys, err := p.isSystem()
	if err != nil {
		return newErr("GetTokenUser", "", ErrInsufficientPrivilege, err)
	}
	if !sys {
		return newErr("", "", ErrInsufficientPrivilege, errors.New("process is not LocalSystem"))
	}
	return enablePrivilege("SeTcbPrivilege")
}

func processIsSystem() (bool, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false, err
	}
	return u.User.Sid.IsWellKnown(windows.WinLocalSystemSid), nil
}

// sessionTokenFor finds a session (active preferred, then disconnected)
// whose user is sid and returns its primary token.
func sessionTokenFor(sid *windows.SID, sidStr string) (windows.Token, uint32, error) {
	var infos *windows.WTS_SESSION_INFO
	var n uint32
	if err := windows.WTSEnumerateSessions(0 /* WTS_CURRENT_SERVER_HANDLE */, 0, 1, &infos, &n); err != nil {
		return 0, 0, newErr("WTSEnumerateSessions", sidStr, win32Sentinel(errnoOf(err), ErrS4UUnavailable), err)
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(infos)))

	var best windows.Token
	var bestID uint32
	bestActive := false
	var privErr error
	for _, s := range unsafe.Slice(infos, n) {
		if s.State != windows.WTSActive && s.State != windows.WTSDisconnected {
			continue
		}
		var t windows.Token
		if err := windows.WTSQueryUserToken(s.SessionID, &t); err != nil {
			if errors.Is(win32Sentinel(errnoOf(err), nil), ErrInsufficientPrivilege) {
				privErr = newErr("WTSQueryUserToken", sidStr, ErrInsufficientPrivilege, err)
			}
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
		if privErr != nil {
			return 0, 0, privErr
		}
		return 0, 0, newErr("", sidStr, ErrNotLoggedOn, nil)
	}
	return best, bestID, nil
}

// s4uTokenFor picks the authentication package from the account's domain
// and performs the S4U logon.
func s4uTokenFor(sid *windows.SID, sidStr string) (windows.Token, error) {
	acct, dom, typ, err := sid.LookupAccount("")
	if err != nil {
		return 0, newErr("LookupAccountSid", sidStr, win32Sentinel(errnoOf(err), ErrS4UUnavailable), err)
	}
	if typ != windows.SidTypeUser {
		return 0, newErr("LookupAccountSid", sidStr, ErrNotUser, nil)
	}
	host, err := windows.ComputerName()
	if err != nil {
		return 0, newErr("GetComputerName", sidStr, ErrS4UUnavailable, err)
	}
	pkg, err := PackageFor(dom, host)
	if err != nil {
		return 0, newErr("PackageFor", sidStr, err, nil)
	}
	c, err := connectLSA(sidStr)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	return c.s4uLogon(pkg, acct, dom)
}

// normalise verifies the token user, makes Administrators deny-only, strips
// all privileges and creates the impersonation duplicate.
func normalise(raw windows.Token, sid *windows.SID, sidStr string, src Source, session uint32) (*winToken, error) {
	u, err := raw.GetTokenUser()
	if err != nil {
		return nil, newErr("GetTokenUser", sidStr, ErrS4UUnavailable, err)
	}
	if !u.User.Sid.Equals(sid) {
		return nil, newErr("", sidStr, ErrSIDMismatch, nil)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, newErr("CreateWellKnownSid", sidStr, ErrS4UUnavailable, err)
	}
	const disableMaxPrivilege = 0x1 // keeps only SeChangeNotifyPrivilege
	deny := windows.SIDAndAttributes{Sid: admins}
	var restricted windows.Token
	r, _, e := procCreateRestrictedToken.Call(uintptr(raw), disableMaxPrivilege,
		1, uintptr(unsafe.Pointer(&deny)), 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted)))
	if r == 0 {
		return nil, newErr("CreateRestrictedToken", sidStr, win32Sentinel(errnoOf(e), ErrS4UUnavailable), e)
	}
	t := &winToken{sid: sidStr, source: src, session: session, primary: restricted}
	if err := windows.DuplicateTokenEx(restricted, windows.TOKEN_QUERY|windows.TOKEN_IMPERSONATE|windows.TOKEN_DUPLICATE,
		nil, windows.SecurityImpersonation, windows.TokenImpersonation, &t.imp); err != nil {
		t.Close()
		return nil, newErr("DuplicateTokenEx", sidStr, win32Sentinel(errnoOf(err), ErrS4UUnavailable), err)
	}
	return t, nil
}

func errnoOf(err error) uint32 {
	var e windows.Errno
	if errors.As(err, &e) {
		return uint32(e)
	}
	return 0
}

// winToken is the normalised user token.
type winToken struct {
	sid     string
	source  Source
	session uint32

	mu      sync.Mutex
	primary windows.Token // restricted primary token
	imp     windows.Token // SecurityImpersonation duplicate of primary
}

// PrimaryToken is implemented by Windows tokens: PrimaryDup returns a
// caller-owned duplicate of the restricted primary token for
// CreateProcessAsUser of a per-user helper (needs
// SeAssignPrimaryTokenPrivilege and SeIncreaseQuotaPrivilege).
type PrimaryToken interface {
	PrimaryDup() (windows.Token, error)
}

// SecurityChecker is implemented by Windows tokens: AccessCheck on an
// already-read security descriptor (OWNER|GROUP|DACL), e.g. from a VSS
// snapshot.
type SecurityChecker interface {
	AccessCheck(sd *windows.SECURITY_DESCRIPTOR, mask uint32) (bool, error)
}

func (t *winToken) SID() string               { return t.sid }
func (t *winToken) Source() Source            { return t.source }
func (t *winToken) SessionID() (uint32, bool) { return t.session, t.source == SourceSession }
func (t *winToken) Caps() Caps                { return CapsFor(t.source) }
func (t *winToken) String() string            { return fmt.Sprintf("token(%s,%s)", t.sid, t.source) }
func (t *winToken) handles() (p, i windows.Token) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.primary, t.imp
}

// Close implements Token; safe to call more than once.
func (t *winToken) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
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

var errClosed = errors.New("tokens: token closed")

// Impersonate implements Token. If RevertToSelf fails the thread stays
// locked so the runtime discards it instead of reusing a thread that still
// carries the user's identity.
func (t *winToken) Impersonate(fn func() error) (err error) {
	_, imp := t.handles()
	if imp == 0 {
		return errClosed
	}
	runtime.LockOSThread()
	if err := windows.SetThreadToken(nil, imp); err != nil {
		runtime.UnlockOSThread()
		return fmt.Errorf("tokens: SetThreadToken: %w", err)
	}
	defer func() {
		if rerr := windows.RevertToSelf(); rerr != nil {
			err = errors.Join(err, fmt.Errorf("tokens: RevertToSelf: %w", rerr))
			return // deliberately leave the thread locked
		}
		runtime.UnlockOSThread()
	}()
	return fn()
}

// PrimaryDup implements PrimaryToken.
func (t *winToken) PrimaryDup() (windows.Token, error) {
	p, _ := t.handles()
	if p == 0 {
		return 0, errClosed
	}
	var d windows.Token
	err := windows.DuplicateTokenEx(p, windows.MAXIMUM_ALLOWED, nil,
		windows.SecurityImpersonation, windows.TokenPrimary, &d)
	return d, err
}

// fileMapping is the file GENERIC_MAPPING.
var fileMapping = struct{ Read, Write, Execute, All uint32 }{0x120089, 0x120116, 0x1200A0, 0x1F01FF}

// AccessCheck implements SecurityChecker.
func (t *winToken) AccessCheck(sd *windows.SECURITY_DESCRIPTOR, mask uint32) (bool, error) {
	_, imp := t.handles()
	if imp == 0 {
		return false, errClosed
	}
	var (
		privs   [256]byte // PRIVILEGE_SET buffer
		privLen = uint32(len(privs))
		granted uint32
		status  int32
	)
	r, _, e := procAccessCheck.Call(
		uintptr(unsafe.Pointer(sd)), uintptr(imp), uintptr(mask),
		uintptr(unsafe.Pointer(&fileMapping)),
		uintptr(unsafe.Pointer(&privs[0])), uintptr(unsafe.Pointer(&privLen)),
		uintptr(unsafe.Pointer(&granted)), uintptr(unsafe.Pointer(&status)))
	if r == 0 {
		return false, fmt.Errorf("tokens: AccessCheck: %w", e)
	}
	return status != 0, nil
}

// AccessCheckPath implements Token.
func (t *winToken) AccessCheckPath(path string, mask uint32) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, fmt.Errorf("tokens: GetNamedSecurityInfo: %w", err)
	}
	return t.AccessCheck(sd, mask)
}
