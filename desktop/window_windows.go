//go:build windows

package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procIsIconic                 = user32.NewProc("IsIconic")
)

// windowOnScreen reports whether one of this process's top-level windows is
// shown and not minimised: the app is open, even if another window is in
// front. Hidden to the tray or minimised to the taskbar counts as not open.
func windowOnScreen() bool {
	pid := uint32(windows.GetCurrentProcessId())
	open := false
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var owner uint32
		procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if owner != pid {
			return 1
		}
		if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		if min, _, _ := procIsIconic.Call(hwnd); min != 0 {
			return 1
		}
		open = true
		return 0
	})
	procEnumWindows.Call(cb, 0)
	return open
}
