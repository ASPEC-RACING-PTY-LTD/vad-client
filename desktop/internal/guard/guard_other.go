//go:build !windows

package guard

import "os"

// Non-Windows builds exist only so shared code compiles and vets in CI;
// the agent targets Windows. They perform lexical checks only.

// SystemEnv returns protected locations from Windows-style environment
// variables when present; on a non-Windows host they are normally empty.
func SystemEnv() Env {
	return Env{
		WinDir:          os.Getenv("WINDIR"),
		ProgramFiles:    os.Getenv("ProgramFiles"),
		ProgramFilesX86: os.Getenv("ProgramFiles(x86)"),
		ProgramFilesX64: os.Getenv("ProgramW6432"),
		ProgramData:     os.Getenv("ProgramData"),
		SystemDrive:     os.Getenv("SystemDrive"),
	}
}

func resolveLong(c cpath) cpath               { return c }
func finalPath(c cpath) (cpath, bool)         { return c, false }
func reparseComponent(c cpath) (string, bool) { return "", false }
