//go:build windows

package platform

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const runValue = "VaultDock"

var dpapiEntropy = []byte("VaultDock device key v1")

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

// entropyBlob returns nil (no entropy) for empty input.
func entropyBlob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return nil
	}
	return blob(b)
}

func dpapiProtect(data, entropy []byte, flags uint32) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptProtectData(blob(data), windows.StringToUTF16Ptr("VaultDock"), entropyBlob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN|flags, &out); err != nil {
		return nil, fmt.Errorf("DPAPI protect: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func dpapiUnprotect(data, entropy []byte, flags uint32) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(blob(data), nil, entropyBlob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN|flags, &out); err != nil {
		return nil, fmt.Errorf("DPAPI unprotect: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func protect(data []byte) ([]byte, error)   { return dpapiProtect(data, dpapiEntropy, 0) }
func unprotect(data []byte) ([]byte, error) { return dpapiUnprotect(data, dpapiEntropy, 0) }

func protectMachine(data, entropy []byte) ([]byte, error) {
	return dpapiProtect(data, entropy, windows.CRYPTPROTECT_LOCAL_MACHINE)
}

func unprotectMachine(data, entropy []byte) ([]byte, error) {
	// The blob records its scope; the flag is accepted on unprotect too.
	return dpapiUnprotect(data, entropy, windows.CRYPTPROTECT_LOCAL_MACHINE)
}

func programDataDir() string {
	if d, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0); err == nil && d != "" {
		return d
	}
	if d := os.Getenv("ProgramData"); d != "" {
		return d
	}
	return `C:\ProgramData`
}

func setAutoStart(enabled bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !enabled {
		if err := k.DeleteValue(runValue); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(runValue, fmt.Sprintf(`"%s" --minimized`, exe))
}

func autoStartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValue)
	return err == nil
}

func machineID() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue("MachineGuid")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

func osDescription() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	product, _, _ := k.GetStringValue("ProductName")
	display, _, _ := k.GetStringValue("DisplayVersion")
	build, _, _ := k.GetStringValue("CurrentBuild")
	// ProductName still says "Windows 10" on Windows 11; builds >= 22000 are Windows 11.
	if strings.HasPrefix(product, "Windows 10") && build >= "22000" && len(build) >= 5 {
		product = "Windows 11" + strings.TrimPrefix(product, "Windows 10")
	}
	out := strings.TrimSpace(product + " " + display)
	if build != "" {
		out += " (build " + build + ")"
	}
	return out
}
