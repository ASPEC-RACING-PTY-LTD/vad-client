//go:build !windows

package platform

import (
	"errors"
	"os"
)

// Non-Windows builds exist only so shared code compiles in CI; the desktop
// application targets Windows.

func protect(data []byte) ([]byte, error) { return nil, errors.New("secure storage requires Windows") }
func unprotect(data []byte) ([]byte, error) {
	return nil, errors.New("secure storage requires Windows")
}
func protectMachine(data, entropy []byte) ([]byte, error) {
	return nil, errors.New("secure storage requires Windows")
}
func unprotectMachine(data, entropy []byte) ([]byte, error) {
	return nil, errors.New("secure storage requires Windows")
}
func programDataDir() string {
	if d := os.Getenv("ProgramData"); d != "" {
		return d
	}
	return "/var/lib"
}
func setAutoStart(bool) error { return errors.New("autostart requires Windows") }
func autoStartEnabled() bool  { return false }
func machineID() string       { return "" }
func osDescription() string   { return "" }
