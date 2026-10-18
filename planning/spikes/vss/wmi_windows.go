//go:build windows

package main

import (
	"errors"
	"fmt"
	"regexp"
	"runtime"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

// wmiConn is a connection to root\cimv2. All calls must happen on the OS
// thread that created it (COM apartment), so the constructor locks the
// thread and Close unlocks it.
type wmiConn struct {
	locator *ole.IDispatch
	svc     *ole.IDispatch
}

var shadowIDRe = regexp.MustCompile(`^\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}$`)

func connectWMI() (*wmiConn, error) {
	runtime.LockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		// S_FALSE (already initialised) is reported as an error by go-ole.
		var oe *ole.OleError
		if !errors.As(err, &oe) || oe.Code() != 1 {
			runtime.UnlockOSThread()
			return nil, fmt.Errorf("CoInitializeEx: %w", err)
		}
	}
	unk, err := oleutil.CreateObject("WbemScripting.SWbemLocator")
	if err != nil {
		ole.CoUninitialize()
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("create SWbemLocator: %w", err)
	}
	defer unk.Release()
	loc, err := unk.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		ole.CoUninitialize()
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("locator IDispatch: %w", err)
	}
	// Ask the scripting layer to enable SeBackupPrivilege on the calls it makes.
	if sec, err := oleutil.GetProperty(loc, "Security_"); err == nil {
		if privs, err := oleutil.GetProperty(sec.ToIDispatch(), "Privileges"); err == nil {
			_, _ = oleutil.CallMethod(privs.ToIDispatch(), "AddAsString", "SeBackupPrivilege", true)
			privs.Clear()
		}
		sec.Clear()
	}
	sv, err := oleutil.CallMethod(loc, "ConnectServer", ".", `root\cimv2`)
	if err != nil {
		loc.Release()
		ole.CoUninitialize()
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("ConnectServer: %w", err)
	}
	return &wmiConn{locator: loc, svc: sv.ToIDispatch()}, nil
}

func (w *wmiConn) Close() {
	w.svc.Release()
	w.locator.Release()
	ole.CoUninitialize()
	runtime.UnlockOSThread()
}

// CreateShadow creates a ClientAccessible (persistent) shadow of volume, e.g. `C:\`.
// It returns the shadow ID ("{GUID}"). The caller must record the ID durably
// before doing anything else so orphans can be reaped after a crash.
func (w *wmiConn) CreateShadow(volume string) (string, error) {
	cv, err := oleutil.CallMethod(w.svc, "Get", "Win32_ShadowCopy")
	if err != nil {
		return "", fmt.Errorf("get class: %w", err)
	}
	class := cv.ToIDispatch()
	defer class.Release()

	mv, err := oleutil.GetProperty(class, "Methods_")
	if err != nil {
		return "", err
	}
	methods := mv.ToIDispatch()
	defer methods.Release()
	mtd, err := oleutil.CallMethod(methods, "Item", "Create")
	if err != nil {
		return "", err
	}
	method := mtd.ToIDispatch()
	defer method.Release()
	ipd, err := oleutil.GetProperty(method, "InParameters")
	if err != nil {
		return "", err
	}
	inDef := ipd.ToIDispatch()
	defer inDef.Release()
	iv, err := oleutil.CallMethod(inDef, "SpawnInstance_")
	if err != nil {
		return "", err
	}
	in := iv.ToIDispatch()
	defer in.Release()
	if _, err := oleutil.PutProperty(in, "Volume", volume); err != nil {
		return "", err
	}
	// Client SKUs only support the ClientAccessible context through WMI.
	if _, err := oleutil.PutProperty(in, "Context", "ClientAccessible"); err != nil {
		return "", err
	}

	ov, err := oleutil.CallMethod(class, "ExecMethod_", "Create", in)
	if err != nil {
		return "", fmt.Errorf("Win32_ShadowCopy.Create: %w", err)
	}
	out := ov.ToIDispatch()
	defer out.Release()
	rv, err := oleutil.GetProperty(out, "ReturnValue")
	if err != nil {
		return "", err
	}
	if code := variantInt(rv); code != 0 {
		return "", fmt.Errorf("Win32_ShadowCopy.Create returned %d", code)
	}
	idv, err := oleutil.GetProperty(out, "ShadowID")
	if err != nil {
		return "", err
	}
	id := idv.ToString()
	if !shadowIDRe.MatchString(id) {
		return "", fmt.Errorf("unexpected shadow id %q", id)
	}
	return id, nil
}

func (w *wmiConn) shadow(id string) (*ole.IDispatch, error) {
	if !shadowIDRe.MatchString(id) {
		return nil, fmt.Errorf("invalid shadow id %q", id)
	}
	v, err := oleutil.CallMethod(w.svc, "Get", fmt.Sprintf(`Win32_ShadowCopy.ID="%s"`, id))
	if err != nil {
		return nil, fmt.Errorf("get shadow %s: %w", id, err)
	}
	return v.ToIDispatch(), nil
}

// DeviceObject returns `\\?\GLOBALROOT\Device\HarddiskVolumeShadowCopyN`.
func (w *wmiConn) DeviceObject(id string) (string, error) {
	obj, err := w.shadow(id)
	if err != nil {
		return "", err
	}
	defer obj.Release()
	v, err := oleutil.GetProperty(obj, "DeviceObject")
	if err != nil {
		return "", err
	}
	return v.ToString(), nil
}

// DeleteShadow deletes one shadow by ID. Only IDs VaultDock recorded itself
// may be passed here.
func (w *wmiConn) DeleteShadow(id string) error {
	obj, err := w.shadow(id)
	if err != nil {
		return err
	}
	defer obj.Release()
	if _, err := oleutil.CallMethod(obj, "Delete_"); err != nil {
		return fmt.Errorf("delete shadow %s: %w", id, err)
	}
	return nil
}

func variantInt(v *ole.VARIANT) int64 {
	switch x := v.Value().(type) {
	case int32:
		return int64(x)
	case uint32:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	default:
		return -1
	}
}
