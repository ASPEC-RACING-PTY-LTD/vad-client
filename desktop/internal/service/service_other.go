//go:build !windows

package service

import (
	"os"
	"path/filepath"
	"time"
)

func programData() string { return filepath.Join(os.TempDir(), "vaultdock-programdata") }

// IsWindowsService always reports false off Windows.
func IsWindowsService() (bool, error) { return false, nil }

// IsElevated always reports false off Windows.
func IsElevated() bool { return false }

// RunWithOptions is unsupported off Windows; use RunInteractive.
func RunWithOptions(string, Handler, Options) error { return ErrUnsupported }

// Install is unsupported off Windows.
func Install(Config) error { return ErrUnsupported }

// Configure is unsupported off Windows.
func Configure(Config) error { return ErrUnsupported }

// SetRequiredPrivileges is unsupported off Windows.
func SetRequiredPrivileges(string, []string) error { return ErrUnsupported }

// Uninstall is unsupported off Windows.
func Uninstall(string, time.Duration) error { return ErrUnsupported }

// StartService is unsupported off Windows.
func StartService(string) error { return ErrUnsupported }

// StopService is unsupported off Windows.
func StopService(string, time.Duration) error { return ErrUnsupported }

// Query is unsupported off Windows.
func Query(string) (Status, error) { return Status{}, ErrUnsupported }
