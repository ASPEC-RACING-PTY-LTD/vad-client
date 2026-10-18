package service

import (
	"fmt"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/modules"
)

// Privilege names accepted in the SCM RequiredPrivileges allowlist.
const (
	SeChangeNotifyPrivilege       = "SeChangeNotifyPrivilege"
	SeImpersonatePrivilege        = "SeImpersonatePrivilege"
	SeTcbPrivilege                = "SeTcbPrivilege"
	SeCreateGlobalPrivilege       = "SeCreateGlobalPrivilege"
	SeBackupPrivilege             = "SeBackupPrivilege"
	SeRestorePrivilege            = "SeRestorePrivilege"
	SeManageVolumePrivilege       = "SeManageVolumePrivilege"
	SeAssignPrimaryTokenPrivilege = "SeAssignPrimaryTokenPrivilege"
	SeIncreaseQuotaPrivilege      = "SeIncreaseQuotaPrivilege"
)

// knownPrivileges is the closed set the builder accepts. Anything outside it
// (SeDebug, SeTakeOwnership, SeLoadDriver, ...) is rejected: the agent must
// never be able to hold it (MASTER_PLAN §6, C6).
var knownPrivileges = map[string]bool{
	SeChangeNotifyPrivilege:       true,
	SeImpersonatePrivilege:        true,
	SeTcbPrivilege:                true,
	SeCreateGlobalPrivilege:       true,
	SeBackupPrivilege:             true,
	SeRestorePrivilege:            true,
	SeManageVolumePrivilege:       true,
	SeAssignPrimaryTokenPrivilege: true,
	SeIncreaseQuotaPrivilege:      true,
}

// SimplePrivileges is the allowlist for Simple mode:
//   - SeChangeNotify: traverse checking;
//   - SeImpersonate: SetThreadToken / ImpersonateNamedPipeClient with user tokens;
//   - SeTcb: WTSQueryUserToken and S4U LsaLogonUser for impersonation-level
//     user tokens (spike P0-S3); without it S4U yields identification tokens;
//   - SeCreateGlobal: the Global\ named objects and the agent pipe namespace.
var SimplePrivileges = []string{
	SeChangeNotifyPrivilege,
	SeImpersonatePrivilege,
	SeTcbPrivilege,
	SeCreateGlobalPrivilege,
}

// AdvancedExtraPrivileges are added to SimplePrivileges in Advanced mode:
//   - SeBackup: the VSS requestor and reading the shadow device, enabled only
//     on a thread impersonation token (spike P0-S1);
//   - SeManageVolume: kept from the installer spike for the VSS component
//     until the VM test confirms it can be dropped.
//
// SeRestorePrivilege is deliberately NOT included although the task text and
// the installer spike list it: MASTER_PLAN §6 says "No SeRestore", and the VSS
// spike states it is not needed, because restores write under the user's
// token through the guard. It is still accepted as an explicit extra so INT
// can add it later without a code change.
var AdvancedExtraPrivileges = []string{
	SeBackupPrivilege,
	SeManageVolumePrivilege,
}

// ProcessLaunchPrivileges are needed only to start component helpers with
// CreateProcessAsUser under a user token (vd-discovery, vd-reinstall; spike
// P0-S3). They are not in either default set; pass them as extras once a
// component launcher lands.
var ProcessLaunchPrivileges = []string{
	SeAssignPrimaryTokenPrivilege,
	SeIncreaseQuotaPrivilege,
}

// PrivilegesForMode returns the RequiredPrivileges allowlist for an install
// mode plus any extras, de-duplicated in a stable order (mode set first).
// Unknown modes and unknown privilege names are errors.
func PrivilegesForMode(mode modules.Mode, extra ...string) ([]string, error) {
	var base []string
	switch mode {
	case modules.ModeSimple:
		base = SimplePrivileges
	case modules.ModeAdvanced:
		base = append(append([]string{}, SimplePrivileges...), AdvancedExtraPrivileges...)
	default:
		return nil, fmt.Errorf("unknown install mode %q", mode)
	}
	return NormalizePrivileges(append(append([]string{}, base...), extra...))
}

// NormalizePrivileges validates names against the known set and removes
// duplicates, keeping first occurrence order. SeChangeNotify is always
// included because SCM adds it anyway and its absence would surprise callers.
func NormalizePrivileges(privs []string) ([]string, error) {
	out := []string{SeChangeNotifyPrivilege}
	seen := map[string]bool{SeChangeNotifyPrivilege: true}
	for _, p := range privs {
		if !knownPrivileges[p] {
			return nil, fmt.Errorf("privilege %q is not allowed for the agent service", p)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}
