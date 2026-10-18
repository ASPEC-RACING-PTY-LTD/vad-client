package platform

import (
	"os"
	"path/filepath"
)

// EnvDataDir overrides the machine-wide data root (development and tests).
// When set, MachineDataDir returns it verbatim instead of
// %ProgramData%\VaultDock.
const EnvDataDir = "VAULTDOCK_DATA_DIR"

// MachineDataDir returns the service-mode data root,
// %ProgramData%\VaultDock (or $VAULTDOCK_DATA_DIR). The per-user DataDir
// stays in use for the per-user install and the UI process.
func MachineDataDir() string {
	if d := os.Getenv(EnvDataDir); d != "" {
		return filepath.Clean(d)
	}
	return filepath.Join(programDataDir(), "VaultDock")
}

// StateDir returns <machine data>\state (state.db, settings.json).
func StateDir() string { return filepath.Join(MachineDataDir(), "state") }

// KeysDir returns <machine data>\keys (device key, wrapped repository keys).
func KeysDir() string { return filepath.Join(MachineDataDir(), "keys") }

// MachineLogDir returns <machine data>\logs.
func MachineLogDir() string { return filepath.Join(MachineDataDir(), "logs") }

// ConfigDir returns <machine data>\config (mode.json, modules).
func ConfigDir() string { return filepath.Join(MachineDataDir(), "config") }

// SpoolDir returns <machine data>\spool (temporary packs and uploads).
func SpoolDir() string { return filepath.Join(MachineDataDir(), "spool") }

// EnsureMachineDirs creates the machine data directories. ACLs are set by
// the installer (§7.6); directories created here inherit them.
func EnsureMachineDirs() error {
	for _, d := range []string{StateDir(), KeysDir(), MachineLogDir(), ConfigDir(), SpoolDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// ProtectMachine encrypts data with machine-scope DPAPI
// (CRYPTPROTECT_LOCAL_MACHINE): any process on this computer can decrypt
// it, so the file holding the result must be protected by an ACL
// (SYSTEM and Administrators only). entropy is optional additional secret
// input; nil means none, and the same value must be passed to
// UnprotectMachine.
func ProtectMachine(data, entropy []byte) ([]byte, error) { return protectMachine(data, entropy) }

// UnprotectMachine decrypts data produced by ProtectMachine.
func UnprotectMachine(data, entropy []byte) ([]byte, error) {
	return unprotectMachine(data, entropy)
}
