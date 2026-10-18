package tokens

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors. Provider errors are *Error values that unwrap to one of
// these (and to the OS cause, if any).
var (
	// ErrUnsupported is returned on non-Windows builds.
	ErrUnsupported = errors.New("tokens: profile tokens not supported")
	// ErrInvalidSID is returned for an empty or malformed SID string.
	ErrInvalidSID = errors.New("tokens: invalid SID")
	// ErrInsufficientPrivilege: the process is not LocalSystem or lacks
	// SeTcbPrivilege (needed for WTSQueryUserToken and S4U).
	ErrInsufficientPrivilege = errors.New("tokens: insufficient privilege (need LocalSystem with SeTcbPrivilege)")
	// ErrNotLoggedOn: the user has no active or disconnected session
	// (SessionOnly).
	ErrNotLoggedOn = errors.New("tokens: user has no interactive session")
	// ErrNeedsLogon: the account type has no S4U path (Entra ID only), so
	// the profile can be used only while the user is signed in. Walker
	// issue: profile_needs_logon.
	ErrNeedsLogon = errors.New("tokens: account needs an interactive logon (no S4U path)")
	// ErrS4UUnavailable: an S4U logon was attempted and failed (DC
	// unreachable, account restricted or disabled, LSA error). Usually
	// transient; skip the profile and retry later.
	ErrS4UUnavailable = errors.New("tokens: S4U logon unavailable")
	// ErrAccountNotFound: the SID does not resolve to an account
	// (ERROR_NONE_MAPPED).
	ErrAccountNotFound = errors.New("tokens: account not found")
	// ErrNotUser: the SID is not a user account (group, alias, service).
	ErrNotUser = errors.New("tokens: SID is not a user account")
	// ErrSIDMismatch: the obtained token belongs to a different SID.
	ErrSIDMismatch = errors.New("tokens: token user does not match requested SID")
)

// Error is a provider failure: Err is one of the sentinels above, Cause the
// underlying OS error (may be nil). errors.Is matches either.
type Error struct {
	Op    string // e.g. "LsaLogonUser", "WTSQueryUserToken"
	SID   string
	Err   error
	Cause error
}

func (e *Error) Error() string {
	s := e.Err.Error()
	if e.Op != "" {
		s += " (" + e.Op
		if e.Cause != nil {
			s += ": " + e.Cause.Error()
		}
		s += ")"
	} else if e.Cause != nil {
		s += ": " + e.Cause.Error()
	}
	if e.SID != "" {
		s += " [" + e.SID + "]"
	}
	return s
}

func (e *Error) Unwrap() []error {
	if e.Cause == nil {
		return []error{e.Err}
	}
	return []error{e.Err, e.Cause}
}

func newErr(op, sid string, sentinel, cause error) error {
	return &Error{Op: op, SID: sid, Err: sentinel, Cause: cause}
}

// NTSTATUS values seen from LsaRegisterLogonProcess / LsaLogonUser.
const (
	statusAccessDenied          uint32 = 0xC0000022
	statusPortConnectionRefused uint32 = 0xC0000041 // LsaRegisterLogonProcess without SeTcb
	statusNoLogonServers        uint32 = 0xC000005E
	statusPrivilegeNotHeld      uint32 = 0xC0000061
	statusNoSuchUser            uint32 = 0xC0000064
	statusAccountRestriction    uint32 = 0xC000006E
	statusAccountDisabled       uint32 = 0xC0000072
	statusNoSuchPackage         uint32 = 0xC00000FE
	statusLogonTypeNotGranted   uint32 = 0xC000015B
	statusNoTrustSamAccount     uint32 = 0xC000018B
	statusAccountLockedOut      uint32 = 0xC0000234
	statusNotSupported          uint32 = 0xC00000BB
	statusInvalidParameter      uint32 = 0xC000000D
)

// Win32 error codes seen from WTS*, token and lookup calls.
const (
	errorAccessDenied      uint32 = 5
	errorNoToken           uint32 = 1008
	errorNotAllAssigned    uint32 = 1300
	errorPrivilegeNotHeld  uint32 = 1314
	errorNoneMapped        uint32 = 1332
	errorTrustedRelFailure uint32 = 1789
	errorNoLogonServers    uint32 = 1311
)

// ntStatusSentinel maps an LSA NTSTATUS to a sentinel (portable so the
// mapping is unit-tested everywhere).
func ntStatusSentinel(st uint32) error {
	switch st {
	case statusPortConnectionRefused, statusPrivilegeNotHeld, statusAccessDenied:
		return ErrInsufficientPrivilege
	case statusNoSuchUser:
		return ErrAccountNotFound
	case statusNoLogonServers, statusAccountRestriction, statusAccountDisabled, statusNoSuchPackage,
		statusLogonTypeNotGranted, statusNoTrustSamAccount, statusAccountLockedOut, statusNotSupported,
		statusInvalidParameter:
		return ErrS4UUnavailable
	}
	return ErrS4UUnavailable
}

// win32Sentinel maps a Win32 error code to a sentinel; fallback is returned
// for codes with no specific meaning.
func win32Sentinel(code uint32, fallback error) error {
	switch code {
	case errorPrivilegeNotHeld, errorNotAllAssigned, errorAccessDenied:
		return ErrInsufficientPrivilege
	case errorNoToken:
		return ErrNotLoggedOn
	case errorNoneMapped:
		return ErrAccountNotFound
	case errorTrustedRelFailure, errorNoLogonServers:
		return ErrS4UUnavailable
	}
	return fallback
}

// ntStatusError wraps a failed LSA call.
func ntStatusError(op, sid string, st, sub uint32) error {
	cause := fmt.Errorf("NTSTATUS 0x%08X", st)
	if sub != 0 {
		cause = fmt.Errorf("NTSTATUS 0x%08X, substatus 0x%08X", st, sub)
	}
	return newErr(op, sid, ntStatusSentinel(st), cause)
}

// PackageFor chooses the S4U authentication package for an account whose
// LookupAccountSid domain is domain on a computer named host: local SAM
// accounts (including Microsoft-account-linked ones, whose SAM domain is the
// computer name) use MSV1_0; Entra ID ("AzureAD") accounts have no S4U path
// (ErrNeedsLogon); anything else is an AD domain and uses Negotiate, which
// routes KERB_S4U_LOGON to Kerberos and needs a reachable DC.
func PackageFor(domain, host string) (string, error) {
	switch {
	case domain == "":
		return "", ErrS4UUnavailable
	case strings.EqualFold(domain, host):
		return PackageMSV, nil
	case strings.EqualFold(domain, "AzureAD"):
		return "", ErrNeedsLogon
	default:
		return PackageNegotiate, nil
	}
}

// S4U authentication package names.
const (
	PackageMSV       = "MICROSOFT_AUTHENTICATION_PACKAGE_V1_0"
	PackageNegotiate = "Negotiate"
)
