//go:build windows

package agent

import "golang.org/x/sys/windows"

// processIsSystem reports whether the current process token is LocalSystem.
// If the token cannot be read it assumes service mode (fail closed).
func processIsSystem() bool {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return true
	}
	return tu.User.Sid.String() == sidSystem
}
