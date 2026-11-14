//go:build windows

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unsafe"

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

// runElevated runs exe with args after the Windows administrator prompt
// and waits (up to a minute) for it to finish.
func runElevated(exe string, args string) error {
	var info struct {
		cbSize       uint32
		fMask        uint32
		hwnd         windows.Handle
		lpVerb       *uint16
		lpFile       *uint16
		lpParameters *uint16
		lpDirectory  *uint16
		nShow        int32
		hInstApp     windows.Handle
		lpIDList     uintptr
		lpClass      *uint16
		hkeyClass    windows.Handle
		dwHotKey     uint32
		hIcon        windows.Handle
		hProcess     windows.Handle
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	info.fMask = 0x00000040 // SEE_MASK_NOCLOSEPROCESS
	info.lpVerb, _ = windows.UTF16PtrFromString("runas")
	info.lpFile, _ = windows.UTF16PtrFromString(exe)
	info.lpParameters, _ = windows.UTF16PtrFromString(args)
	info.nShow = windows.SW_HIDE
	if r, _, err := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		if err == windows.ERROR_CANCELLED {
			return fmt.Errorf("administrator rights were not granted")
		}
		return err
	}
	if info.hProcess != 0 {
		_, _ = windows.WaitForSingleObject(info.hProcess, 60000)
		var code uint32
		_ = windows.GetExitCodeProcess(info.hProcess, &code)
		_ = windows.CloseHandle(info.hProcess)
		if code != 0 {
			return fmt.Errorf("the service did not start (exit code %d); see the VaultDock logs", code)
		}
	}
	return nil
}

var procShellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

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
