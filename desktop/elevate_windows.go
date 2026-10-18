//go:build windows

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

const waitPIDArg = "--wait-pid="

func currentElevation() Elevation {
	tok := windows.GetCurrentProcessToken()
	e := Elevation{Elevated: tok.IsElevated()}
	if e.Elevated {
		e.Admin = true
		return e
	}
	// A UAC-filtered token has Administrators as deny-only; the linked
	// token says whether the user is an administrator at all.
	if linked, err := tok.GetLinkedToken(); err == nil {
		defer linked.Close()
		if ba, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid); err == nil {
			e.Admin, _ = linked.IsMember(ba)
		}
	}
	return e
}

func relaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	args, _ := windows.UTF16PtrFromString(waitPIDArg + strconv.Itoa(os.Getpid()))
	if err := windows.ShellExecute(0, verb, file, args, nil, windows.SW_SHOWNORMAL); err != nil {
		if err == windows.ERROR_CANCELLED {
			return fmt.Errorf("administrator rights were not granted")
		}
		return err
	}
	return nil
}

// waitForParent waits (up to 15 s) for the process named by --wait-pid= to
// exit.
func waitForParent(args []string) {
	for _, a := range args {
		if !strings.HasPrefix(a, waitPIDArg) {
			continue
		}
		pid, err := strconv.ParseUint(strings.TrimPrefix(a, waitPIDArg), 10, 32)
		if err != nil {
			return
		}
		h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
		if err != nil {
			return // already gone
		}
		_, _ = windows.WaitForSingleObject(h, 15000)
		_ = windows.CloseHandle(h)
		return
	}
}
