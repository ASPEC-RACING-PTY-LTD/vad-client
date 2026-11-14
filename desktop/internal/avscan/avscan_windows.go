//go:build windows

package avscan

import (
	"fmt"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	amsi                = windows.NewLazySystemDLL("amsi.dll")
	procAmsiInitialize  = amsi.NewProc("AmsiInitialize")
	procAmsiUninit      = amsi.NewProc("AmsiUninitialize")
	procAmsiOpenSession = amsi.NewProc("AmsiOpenSession")
	procAmsiCloseSess   = amsi.NewProc("AmsiCloseSession")
	procAmsiScanBuffer  = amsi.NewProc("AmsiScanBuffer")
)

// AMSI_RESULT values: 0 clean, 1 not detected, 0x4000-0x4fff blocked by
// administrator, >= 32768 malware.
const (
	amsiBlockedStart = 0x4000
	amsiBlockedEnd   = 0x4fff
	amsiMalware      = 32768
)

func scanFile(path, name string) (Verdict, error) {
	if err := amsi.Load(); err != nil {
		return Unavailable, err
	}
	f, err := os.Open(path)
	if err != nil {
		return Unavailable, err
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, maxScan))
	if err != nil {
		return Unavailable, err
	}
	if len(buf) == 0 {
		return Clean, nil
	}
	app, _ := windows.UTF16PtrFromString("VaultDock")
	var ctx uintptr
	if r, _, _ := procAmsiInitialize.Call(uintptr(unsafe.Pointer(app)), uintptr(unsafe.Pointer(&ctx))); r != 0 {
		return Unavailable, fmt.Errorf("AmsiInitialize: 0x%x", r)
	}
	defer procAmsiUninit.Call(ctx)
	var session uintptr
	if r, _, _ := procAmsiOpenSession.Call(ctx, uintptr(unsafe.Pointer(&session))); r != 0 {
		return Unavailable, fmt.Errorf("AmsiOpenSession: 0x%x", r)
	}
	defer procAmsiCloseSess.Call(ctx, session)
	content, _ := windows.UTF16PtrFromString(name)
	var result uint32
	r, _, _ := procAmsiScanBuffer.Call(ctx, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)),
		uintptr(unsafe.Pointer(content)), session, uintptr(unsafe.Pointer(&result)))
	if r != 0 {
		return Unavailable, fmt.Errorf("AmsiScanBuffer: 0x%x", r)
	}
	switch {
	case result >= amsiMalware:
		return Malware, nil
	case result >= amsiBlockedStart && result <= amsiBlockedEnd:
		return Blocked, nil
	}
	return Clean, nil
}
