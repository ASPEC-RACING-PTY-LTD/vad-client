package agent

import (
	"context"
	"strings"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/ipc/caller"
)

// CodeForbidden is the stable error code for calls the caller may not make.
const CodeForbidden = "forbidden"

// ErrForbidden matches (errors.Is) every authorisation refusal.
var ErrForbidden = &Error{Code: CodeForbidden, Message: "forbidden"}

// Access is the authorisation class of an API method (MASTER_PLAN §7.6).
type Access int

const (
	// AccessDenied: unknown methods are refused (default deny).
	AccessDenied Access = iota
	// AccessRead: status and read-only views; any interactive user.
	AccessRead
	// AccessUser: an interactive user managing their own profile's items.
	// Per-item ownership is enforced by the Host (caller.From(ctx)) once
	// profiles land (§7.8); this policy only admits the caller.
	AccessUser
	// AccessAdmin: machine-wide or destructive operations; an elevated
	// administrator or SYSTEM.
	AccessAdmin
)

func (a Access) String() string {
	switch a {
	case AccessRead:
		return "read"
	case AccessUser:
		return "user"
	case AccessAdmin:
		return "admin"
	}
	return "denied"
}

// methodAccess classifies every wire method (agent.API method names plus the
// transport's Subscribe/Unsubscribe). Reserved methods are classified now so
// that the policy is in place when they are implemented.
var methodAccess = map[string]Access{
	// Status and read-only views.
	"Bootstrap":       AccessRead,
	"Snapshot":        AccessRead,
	"ListConfigs":     AccessRead,
	"ListQueue":       AccessRead,
	"ListHistory":     AccessRead,
	"ListAutomations": AccessRead,
	"GetSettings":     AccessRead,
	"UpdateStatus":    AccessRead,
	"Subscribe":       AccessRead,
	"Unsubscribe":     AccessRead,
	"ListBackupJobs":  AccessRead,
	"ListBackupRuns":  AccessRead,
	"ListSnapshots":   AccessRead,
	"Readiness":       AccessRead,
	"ListIssues":      AccessRead,
	"KeyStatus":       AccessRead,
	"GetMode":         AccessRead,
	"ListModules":     AccessRead,
	"ListProfiles":    AccessRead,

	// The caller's own profile items.
	"SaveConfig":         AccessUser,
	"DeleteConfig":       AccessUser,
	"SyncNow":            AccessUser,
	"PreviewConfig":      AccessUser,
	"RetryFailed":        AccessUser,
	"ResetSyncData":      AccessUser,
	"RefreshAutomations": AccessUser,
	"SaveBackupJob":      AccessUser,
	"DeleteBackupJob":    AccessUser,
	"RunBackup":          AccessUser,
	"RetrySkipped":       AccessUser,
	"SkipIssues":         AccessUser,
	"CancelBackup":       AccessUser,
	"ResolveIssue":       AccessUser,
	"PlanRestore":        AccessUser,
	"StartRestore":       AccessUser,
	"CancelRestore":      AccessUser,
	"GiveConsent":        AccessUser,
	"RevokeConsent":      AccessUser,

	// Machine-wide: server connection and enrolment, machine settings,
	// engine pause, updates, keys, mode and modules. Owner decision
	// 2026-10-19: every feature works without Windows administrator rights;
	// only (re)installing needs elevation, and the installer asks for it
	// through UAC itself. File access still runs under each owner's token.
	"ProbeServer":        AccessUser,
	"Connect":            AccessUser,
	"CancelEnrollment":   AccessUser,
	"Disconnect":         AccessUser,
	"SaveSettings":       AccessUser,
	"SetPaused":          AccessUser,
	"CheckForUpdates":    AccessUser,
	"InstallUpdate":      AccessUser,
	"SetWindowVisible":   AccessUser,
	"ConfirmRecoveryKey": AccessUser,
	// First-run key setup: the repository and the trusted server identity
	// are machine-wide; InitRepository returns the recovery key once.
	"InitRepository":        AccessUser,
	"ConfirmServerIdentity": AccessUser,
	"SetMode":               AccessUser,
	"SetModule":             AccessUser,
}

// MethodAccess returns the class of a wire method; AccessDenied if unknown.
func MethodAccess(method string) Access { return methodAccess[method] }

// Well-known SIDs.
const (
	sidSystem = "S-1-5-18"
)

// interactiveUser reports whether sid is a real user account: a local or
// domain account (S-1-5-21-…) or an Entra ID account (S-1-12-1-…). This
// excludes anonymous (S-1-5-7), network (S-1-5-2), the null and world SIDs,
// LocalService/NetworkService (S-1-5-19/20), virtual and service accounts
// (S-1-5-80-…, S-1-5-90-…) and malformed or empty SIDs.
func interactiveUser(sid string) bool {
	for _, p := range []string{"S-1-5-21-", "S-1-12-1-"} {
		if rest, ok := strings.CutPrefix(sid, p); ok && validSubAuthorities(rest) {
			return true
		}
	}
	return false
}

// validSubAuthorities checks a non-empty dash-separated list of decimals.
func validSubAuthorities(s string) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, "-") {
		if part == "" || len(part) > 10 {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// IsSystem reports whether c is LocalSystem. Both the token flag and the SID
// must agree.
func IsSystem(c caller.Caller) bool { return c.System && c.SID == sidSystem }

// IsAdmin reports whether c may perform AccessAdmin operations: SYSTEM, or an
// elevated member of Administrators that is a real user account.
func IsAdmin(c caller.Caller) bool {
	return IsSystem(c) || (c.Admin && c.Elevated && interactiveUser(c.SID))
}

// Authorize is the §7.6 IPC policy, used as ipc.ServerOptions.Authorize.
//   - SYSTEM may call everything.
//   - Anonymous, network, service and malformed identities are refused.
//   - Any interactive user may call AccessRead and AccessUser methods.
//   - AccessAdmin methods need Admin && Elevated (none are currently
//     classified AccessAdmin; the class is kept for future use).
//   - Unknown methods are refused.
//
// Refusals are *Error with code CodeForbidden.
func Authorize(_ context.Context, c caller.Caller, method string) error {
	access := MethodAccess(method)
	if access == AccessDenied {
		return forbidden("unknown method " + method)
	}
	if IsSystem(c) {
		return nil
	}
	if !interactiveUser(c.SID) {
		return forbidden("caller is not an interactive user")
	}
	if access == AccessAdmin && !IsAdmin(c) {
		return forbidden(method + " requires an elevated administrator")
	}
	return nil
}

func forbidden(msg string) *Error {
	return &Error{Code: CodeForbidden, Message: "forbidden: " + msg}
}
