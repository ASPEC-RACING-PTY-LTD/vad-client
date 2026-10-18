//go:build windows

package updater

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// launch starts the installer through ShellExecute "runas": the machine-wide
// installer needs administrator rights, so a standard UI gets the UAC prompt
// (an already elevated caller gets none). args is passed verbatim (NSIS
// needs /D= unquoted and last).
func launch(path, args string) error {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	params, err := windows.UTF16PtrFromString(args)
	if err != nil {
		return err
	}
	if err := windows.ShellExecute(0, verb, file, params, nil, windows.SW_SHOWNORMAL); err != nil {
		if err == windows.ERROR_CANCELLED {
			return fmt.Errorf("the update needs administrator rights, which were not granted")
		}
		return err
	}
	return nil
}
