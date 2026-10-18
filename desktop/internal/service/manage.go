package service

import (
	"path/filepath"
	"time"
)

// Config describes the service registration written by Install/Configure.
// Fixed properties (not configurable): own-process service, LocalSystem,
// delayed auto-start, service SID type unrestricted, recovery = restart
// after 5 s / 30 s / 120 s with the failure count reset after a day, also
// for non-crash failures (a failure exit code from Run).
type Config struct {
	Name        string // default DefaultName
	DisplayName string // default DefaultDisplayName
	Description string // default DefaultDescription
	ExePath     string // absolute path of vaultdock-agent.exe (required)
	Args        []string
	// Privileges is the RequiredPrivileges allowlist; build it with
	// PrivilegesForMode. It is required (an empty list would leave the
	// full LocalSystem privilege set).
	Privileges []string
	// PreshutdownTimeout is how long SCM waits after PRESHUTDOWN (default 60 s).
	PreshutdownTimeout time.Duration
}

func (c Config) withDefaults() Config {
	if c.Name == "" {
		c.Name = DefaultName
	}
	if c.DisplayName == "" {
		c.DisplayName = DefaultDisplayName
	}
	if c.Description == "" {
		c.Description = DefaultDescription
	}
	if c.PreshutdownTimeout <= 0 {
		c.PreshutdownTimeout = 60 * time.Second
	}
	return c
}

// Recovery policy applied by Install/Configure.
var (
	RecoveryDelays      = []time.Duration{5 * time.Second, 30 * time.Second, 120 * time.Second}
	RecoveryResetPeriod = 24 * time.Hour
)

// Status is the SCM view of the service, readable without elevation.
type Status struct {
	Name       string   `json:"name"`
	Installed  bool     `json:"installed"`
	State      string   `json:"state,omitempty"` // stopped, start_pending, stop_pending, running, ...
	PID        uint32   `json:"pid,omitempty"`
	StartType  string   `json:"start_type,omitempty"` // auto, delayed_auto, manual, disabled
	Account    string   `json:"account,omitempty"`
	BinaryPath string   `json:"binary_path,omitempty"`
	SidType    string   `json:"sid_type,omitempty"`
	Privileges []string `json:"privileges,omitempty"`
	QueryError string   `json:"query_error,omitempty"` // partial result reason
}

// ProgramDataDir returns %ProgramData%\VaultDock (resolved from the known
// folder on Windows, not from the environment).
func ProgramDataDir() string { return filepath.Join(programData(), "VaultDock") }

// DefaultLogDir returns %ProgramData%\VaultDock\logs.
func DefaultLogDir() string { return filepath.Join(ProgramDataDir(), "logs") }
