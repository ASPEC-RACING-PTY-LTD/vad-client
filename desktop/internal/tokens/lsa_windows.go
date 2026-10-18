//go:build windows

package tokens

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Raw LSA / advapi32 entry points not wrapped by x/sys/windows.
var (
	modsecur32  = windows.NewLazySystemDLL("secur32.dll")
	modadvapi32 = windows.NewLazySystemDLL("advapi32.dll")

	procLsaRegisterLogonProcess        = modsecur32.NewProc("LsaRegisterLogonProcess")
	procLsaDeregisterLogonProcess      = modsecur32.NewProc("LsaDeregisterLogonProcess")
	procLsaLookupAuthenticationPackage = modsecur32.NewProc("LsaLookupAuthenticationPackage")
	procLsaLogonUser                   = modsecur32.NewProc("LsaLogonUser")
	procLsaFreeReturnBuffer            = modsecur32.NewProc("LsaFreeReturnBuffer")

	procAllocateLocallyUniqueId = modadvapi32.NewProc("AllocateLocallyUniqueId")
	procCreateRestrictedToken   = modadvapi32.NewProc("CreateRestrictedToken")
	procAccessCheck             = modadvapi32.NewProc("AccessCheck")
	procAdjustTokenPrivileges   = modadvapi32.NewProc("AdjustTokenPrivileges")
)

const (
	// logonTypeNetwork needs only "Access this computer from the network",
	// which Users have by default; Batch would need SeBatchLogonRight.
	logonTypeNetwork = 3

	// MSV1_0_S4U_LOGON.MessageType (MsV1_0S4ULogon) and
	// KERB_S4U_LOGON.MessageType (KerbS4ULogon) are both 12.
	s4uMessageType = 12

	maxS4UChars = 512

	logonProcessName = "VaultDockAgent"
)

// lsaString is LSA_STRING (ANSI).
type lsaString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        *byte
}

func newLSAString(s string) *lsaString {
	b := append([]byte(s), 0)
	return &lsaString{Length: uint16(len(s)), MaximumLength: uint16(len(b)), Buffer: &b[0]}
}

// s4uLogonInfo matches both MSV1_0_S4U_LOGON and KERB_S4U_LOGON.
type s4uLogonInfo struct {
	MessageType       uint32
	Flags             uint32
	UserPrincipalName windows.NTUnicodeString // ClientUpn for Kerberos
	DomainName        windows.NTUnicodeString // ClientRealm for Kerberos
}

// s4uBuffer keeps the strings in the same allocation directly after the
// header, as LsaLogonUser requires (it copies AuthenticationInformationLength
// bytes and rebases the embedded pointers).
type s4uBuffer struct {
	info s4uLogonInfo
	data [maxS4UChars]uint16
}

type tokenSource struct {
	SourceName       [8]byte
	SourceIdentifier windows.LUID
}

type quotaLimits struct {
	PagedPoolLimit        uintptr
	NonPagedPoolLimit     uintptr
	MinimumWorkingSetSize uintptr
	MaximumWorkingSetSize uintptr
	PagefileLimit         uintptr
	TimeLimit             int64
}

// lsaConn is a trusted LSA connection. LsaRegisterLogonProcess requires
// SeTcbPrivilege (enabled) in the caller, i.e. LocalSystem.
type lsaConn struct {
	h   windows.Handle
	sid string
}

func connectLSA(sid string) (*lsaConn, error) {
	var h windows.Handle
	var mode uint32
	r, _, _ := procLsaRegisterLogonProcess.Call(
		uintptr(unsafe.Pointer(newLSAString(logonProcessName))), uintptr(unsafe.Pointer(&h)), uintptr(unsafe.Pointer(&mode)))
	if r != 0 {
		return nil, ntStatusError("LsaRegisterLogonProcess", sid, uint32(r), 0)
	}
	return &lsaConn{h: h, sid: sid}, nil
}

func (c *lsaConn) Close() {
	procLsaDeregisterLogonProcess.Call(uintptr(c.h))
}

func (c *lsaConn) lookupPackage(name string) (uint32, error) {
	var id uint32
	r, _, _ := procLsaLookupAuthenticationPackage.Call(
		uintptr(c.h), uintptr(unsafe.Pointer(newLSAString(name))), uintptr(unsafe.Pointer(&id)))
	if r != 0 {
		return 0, ntStatusError("LsaLookupAuthenticationPackage", c.sid, uint32(r), 0)
	}
	return id, nil
}

// s4uLogon performs an S4U logon and returns a primary token for
// user@domain. The token has no credentials.
func (c *lsaConn) s4uLogon(pkgName, user, domain string) (windows.Token, error) {
	pkg, err := c.lookupPackage(pkgName)
	if err != nil {
		return 0, err
	}
	u, err := windows.UTF16FromString(user)
	if err != nil {
		return 0, newErr("s4u", c.sid, ErrS4UUnavailable, err)
	}
	d, err := windows.UTF16FromString(domain)
	if err != nil {
		return 0, newErr("s4u", c.sid, ErrS4UUnavailable, err)
	}
	u, d = u[:len(u)-1], d[:len(d)-1] // drop NULs; UNICODE_STRING is counted
	if len(u) == 0 || len(u)+len(d) > maxS4UChars {
		return 0, newErr("s4u", c.sid, ErrS4UUnavailable, errors.New("bad account name length"))
	}

	b := new(s4uBuffer)
	b.info.MessageType = s4uMessageType
	copy(b.data[:], u)
	copy(b.data[len(u):], d)
	b.info.UserPrincipalName = windows.NTUnicodeString{Length: uint16(2 * len(u)), MaximumLength: uint16(2 * len(u)), Buffer: &b.data[0]}
	if len(d) > 0 {
		b.info.DomainName = windows.NTUnicodeString{Length: uint16(2 * len(d)), MaximumLength: uint16(2 * len(d)), Buffer: &b.data[len(u)]}
	}
	authLen := unsafe.Sizeof(b.info) + uintptr(2*(len(u)+len(d)))

	var src tokenSource
	copy(src.SourceName[:], "VaultDck")
	if r, _, e := procAllocateLocallyUniqueId.Call(uintptr(unsafe.Pointer(&src.SourceIdentifier))); r == 0 {
		return 0, newErr("AllocateLocallyUniqueId", c.sid, ErrS4UUnavailable, e)
	}

	var (
		profile    uintptr
		profileLen uint32
		logonID    windows.LUID
		tok        windows.Token
		quotas     quotaLimits
		sub        uint32
	)
	r, _, _ := procLsaLogonUser.Call(
		uintptr(c.h),
		uintptr(unsafe.Pointer(newLSAString(logonProcessName))),
		logonTypeNetwork,
		uintptr(pkg),
		uintptr(unsafe.Pointer(b)),
		authLen,
		0, // LocalGroups: none
		uintptr(unsafe.Pointer(&src)),
		uintptr(unsafe.Pointer(&profile)),
		uintptr(unsafe.Pointer(&profileLen)),
		uintptr(unsafe.Pointer(&logonID)),
		uintptr(unsafe.Pointer(&tok)),
		uintptr(unsafe.Pointer(&quotas)),
		uintptr(unsafe.Pointer(&sub)),
	)
	if profile != 0 {
		procLsaFreeReturnBuffer.Call(profile)
	}
	if r != 0 {
		return 0, ntStatusError("LsaLogonUser", c.sid, uint32(r), sub)
	}
	return tok, nil
}

// enablePrivilege enables a privilege that is already present in the
// process token (the SCM RequiredPrivileges allowlist keeps it present).
func enablePrivilege(name string) error {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		return newErr("OpenProcessToken", "", ErrInsufficientPrivilege, err)
	}
	defer tok.Close()
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr(name), &luid); err != nil {
		return newErr("LookupPrivilegeValue", "", ErrInsufficientPrivilege, err)
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
	// Call directly so the last error is captured on the same thread:
	// AdjustTokenPrivileges "succeeds" with ERROR_NOT_ALL_ASSIGNED when the
	// privilege is absent from the token.
	r, _, e := procAdjustTokenPrivileges.Call(uintptr(tok), 0, uintptr(unsafe.Pointer(&tp)), 0, 0, 0)
	if r == 0 {
		return newErr("AdjustTokenPrivileges", "", ErrInsufficientPrivilege, e)
	}
	if errors.Is(e, windows.ERROR_NOT_ALL_ASSIGNED) {
		return newErr("AdjustTokenPrivileges", "", ErrInsufficientPrivilege, e)
	}
	return nil
}
