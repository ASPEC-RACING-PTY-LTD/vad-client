// Package main is a throwaway spike (P0-S2). It is NOT production code.
//
// winget.go holds the portable parts that vd-reinstall would run inside the
// user's session: building one `winget install` command per job and turning
// its exit code into a job outcome.
package main

import (
	"fmt"
	"regexp"
)

// Scope mirrors winget's --scope values. ScopeAny lets the manifest decide.
type Scope string

const (
	ScopeAny     Scope = ""
	ScopeUser    Scope = "user"
	ScopeMachine Scope = "machine"
)

// Item is one consented install job.
type Item struct {
	Source  string // "winget" or "msstore"
	ID      string // exact package identifier
	Version string // empty = latest
	Scope   Scope
	LogPath string // per-job winget log file
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]{0,127}$`)

// InstallArgs returns the argv (without "winget") for one job. Agreements are
// only accepted because the user consented to this exact item in the UI.
func InstallArgs(it Item) ([]string, error) {
	if it.Source != "winget" && it.Source != "msstore" {
		return nil, fmt.Errorf("unsupported source %q", it.Source)
	}
	if !idPattern.MatchString(it.ID) {
		return nil, fmt.Errorf("invalid package id %q", it.ID)
	}
	args := []string{
		"install", "--id", it.ID, "--exact", "--source", it.Source,
		"--silent", "--disable-interactivity",
		"--accept-package-agreements", "--accept-source-agreements",
	}
	if it.Version != "" {
		if !idPattern.MatchString(it.Version) {
			return nil, fmt.Errorf("invalid version %q", it.Version)
		}
		args = append(args, "--version", it.Version)
	}
	switch it.Scope {
	case ScopeAny:
	case ScopeUser, ScopeMachine:
		args = append(args, "--scope", string(it.Scope))
	default:
		return nil, fmt.Errorf("invalid scope %q", it.Scope)
	}
	if it.LogPath != "" {
		args = append(args, "--log", it.LogPath)
	}
	return args, nil
}

// Outcome is the job-level classification of a winget exit code.
type Outcome string

const (
	Installed      Outcome = "installed"
	AlreadyPresent Outcome = "already_installed"
	RebootPending  Outcome = "reboot_required"
	Retryable      Outcome = "retryable"
	UserCancelled  Outcome = "cancelled"
	NotFound       Outcome = "not_found"
	Blocked        Outcome = "blocked"
	Failed         Outcome = "failed"
)

// HRESULTs from winget-cli's documented return codes (APPINSTALLER_CLI_ERROR_*
// and APPINSTALLER_CLI_ERROR_INSTALL_*). Re-check the values against the
// winget version DSK-R targets before relying on them.
var outcomes = map[uint32]Outcome{
	0x00000000: Installed,
	0x8A150014: NotFound,       // NO_APPLICATIONS_FOUND
	0x8A15002B: AlreadyPresent, // UPDATE_NOT_APPLICABLE
	0x8A150061: AlreadyPresent, // PACKAGE_ALREADY_INSTALLED
	0x8A150101: Retryable,      // INSTALL_PACKAGE_IN_USE
	0x8A150102: Retryable,      // INSTALL_INSTALL_IN_PROGRESS (another MSI running)
	0x8A150103: Retryable,      // INSTALL_FILE_IN_USE
	0x8A150104: Failed,         // INSTALL_MISSING_DEPENDENCY
	0x8A150105: Failed,         // INSTALL_DISK_FULL
	0x8A150107: Retryable,      // INSTALL_NO_NETWORK
	0x8A150109: RebootPending,  // INSTALL_REBOOT_REQUIRED_TO_FINISH
	0x8A15010A: RebootPending,  // INSTALL_REBOOT_REQUIRED_FOR_INSTALL
	0x8A15010B: RebootPending,  // INSTALL_REBOOT_INITIATED
	0x8A15010C: UserCancelled,  // INSTALL_CANCELLED_BY_USER (incl. declined UAC)
	0x8A15010D: AlreadyPresent, // INSTALL_ALREADY_INSTALLED
	0x8A15010F: Blocked,        // INSTALL_BLOCKED_BY_POLICY
	0x8A150110: Blocked,        // INSTALL_SYSTEM_NOT_SUPPORTED
}

// Classify maps a process exit code to an Outcome. Raw installer reboot codes
// (3010, 1641) can leak through for some installer types.
func Classify(code uint32) Outcome {
	if o, ok := outcomes[code]; ok {
		return o
	}
	switch code {
	case 3010, 1641:
		return RebootPending
	case 1602, 0x800704C7: // ERROR_INSTALL_USEREXIT, HRESULT(ERROR_CANCELLED)
		return UserCancelled
	}
	return Failed
}
