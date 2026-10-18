//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrDenied means the user's token does not pass AccessCheck; the caller
// skips the file and raises an issue.
var ErrDenied = errors.New("access denied for user token")

// ErrReparse means the object is a reparse point; snapshot reads never follow them.
var ErrReparse = errors.New("reparse point refused")

var (
	modadvapi32               = windows.NewLazySystemDLL("advapi32.dll")
	procAccessCheck           = modadvapi32.NewProc("AccessCheck")
	procMapGenericMask        = modadvapi32.NewProc("MapGenericMask")
	procAdjustTokenPrivileges = modadvapi32.NewProc("AdjustTokenPrivileges")
)

const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1FF

type genericMapping struct{ Read, Write, Execute, All uint32 }

var fileGenericMapping = genericMapping{
	Read:    windows.FILE_GENERIC_READ,
	Write:   windows.FILE_GENERIC_WRITE,
	Execute: windows.FILE_GENERIC_EXECUTE,
	All:     fileAllAccess,
}

const sdInfo = windows.OWNER_SECURITY_INFORMATION | windows.GROUP_SECURITY_INFORMATION |
	windows.DACL_SECURITY_INFORMATION | windows.LABEL_SECURITY_INFORMATION

// Gate decides whether a snapshot object may be read on behalf of a user.
type Gate struct {
	imp windows.Token // impersonation-level duplicate of the user's token
}

// NewGate duplicates user (a primary token from WTSQueryUserToken or S4U) to an
// identification-level impersonation token, which is what AccessCheck needs.
func NewGate(user windows.Token) (*Gate, error) {
	var imp windows.Token
	err := windows.DuplicateTokenEx(user, windows.TOKEN_QUERY|windows.TOKEN_IMPERSONATE|windows.TOKEN_DUPLICATE,
		nil, windows.SecurityIdentification, windows.TokenImpersonation, &imp)
	if err != nil {
		return nil, fmt.Errorf("DuplicateTokenEx: %w", err)
	}
	return &Gate{imp: imp}, nil
}

func (g *Gate) Close() { g.imp.Close() }

// accessCheck reports whether the token is granted all of desired (generic
// rights are mapped with the file mapping) by sd.
func accessCheck(sd *windows.SECURITY_DESCRIPTOR, tok windows.Token, desired uint32) (bool, error) {
	mapping := fileGenericMapping
	procMapGenericMask.Call(uintptr(unsafe.Pointer(&desired)), uintptr(unsafe.Pointer(&mapping)))
	var privSet [64]uint32 // PRIVILEGE_SET buffer, DWORD aligned
	privLen := uint32(unsafe.Sizeof(privSet))
	var granted, status uint32
	r, _, e := procAccessCheck.Call(
		uintptr(unsafe.Pointer(sd)), uintptr(tok), uintptr(desired),
		uintptr(unsafe.Pointer(&mapping)),
		uintptr(unsafe.Pointer(&privSet[0])), uintptr(unsafe.Pointer(&privLen)),
		uintptr(unsafe.Pointer(&granted)), uintptr(unsafe.Pointer(&status)))
	if r == 0 {
		return false, fmt.Errorf("AccessCheck: %w", e)
	}
	return status != 0 && granted&desired == desired, nil
}

// openMeta opens path for READ_CONTROL only, never following reparse points,
// and returns the handle plus its by-handle info. Backup semantics are needed
// to open directories and (with SeBackupPrivilege enabled) to get READ_CONTROL
// on objects SYSTEM is not granted.
func openMeta(path string) (windows.Handle, *windows.ByHandleFileInformation, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, nil, err
	}
	h, err := windows.CreateFile(p, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, nil, err
	}
	var fi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &fi); err != nil {
		windows.CloseHandle(h)
		return 0, nil, err
	}
	if fi.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		windows.CloseHandle(h)
		return 0, nil, ErrReparse
	}
	return h, &fi, nil
}

// check runs AccessCheck for desired against the SD of the object at path.
// A missing object is reported as os.ErrNotExist.
func (g *Gate) check(path string, desired uint32) (*windows.ByHandleFileInformation, error) {
	h, fi, err := openMeta(path)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	defer windows.CloseHandle(h)
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, sdInfo)
	if err != nil {
		return nil, fmt.Errorf("GetSecurityInfo %s: %w", path, err)
	}
	ok, err := accessCheck(sd, g.imp, desired)
	if err != nil {
		return nil, err
	}
	if !ok {
		return fi, ErrDenied
	}
	return fi, nil
}

// CheckLive checks GENERIC_READ for the user on a live path (no snapshot involved).
func (g *Gate) CheckLive(livePath string) error {
	_, err := g.check(livePath, windows.GENERIC_READ)
	return err
}

// CheckDir checks that the user may list a directory before the engine
// enumerates the same directory inside the snapshot (names are data too).
func (g *Gate) CheckDir(liveDir, snapDir string) error {
	if _, err := g.check(liveDir, windows.FILE_LIST_DIRECTORY|windows.SYNCHRONIZE); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := g.check(snapDir, windows.FILE_LIST_DIRECTORY|windows.SYNCHRONIZE)
	return err
}

// OpenSnapshotFile returns a read handle on snapPath only if the user's token
// passes AccessCheck(GENERIC_READ) on BOTH the live file's SD (current
// permission) and the snapshot file's SD (permission at snapshot time). If the
// live file no longer exists, the snapshot SD alone decides. The data handle
// (FILE_FLAG_BACKUP_SEMANTICS) is opened only after both checks pass.
//
// Must be called inside withBackupPrivilege.
func (g *Gate) OpenSnapshotFile(livePath, snapPath string) (*os.File, error) {
	liveFI, err := g.check(livePath, windows.GENERIC_READ)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("live %s: %w", livePath, err)
	}
	snapFI, err := g.check(snapPath, windows.GENERIC_READ)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", snapPath, err)
	}
	if snapFI.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return nil, fmt.Errorf("snapshot %s: is a directory", snapPath)
	}
	_ = liveFI // file-ID comparison (liveFI vs snapFI index) is informational only, see README

	p, err := windows.UTF16PtrFromString(snapPath)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_SEQUENTIAL_SCAN, 0)
	if err != nil {
		return nil, fmt.Errorf("open snapshot data %s: %w", snapPath, err)
	}
	var fi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &fi); err != nil ||
		fi.FileIndexHigh != snapFI.FileIndexHigh || fi.FileIndexLow != snapFI.FileIndexLow {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("snapshot %s: object changed between check and open", snapPath)
	}
	return os.NewFile(uintptr(h), snapPath), nil
}

// withBackupPrivilege runs fn on a locked OS thread whose impersonation token
// (a copy of the process token) has SeBackupPrivilege enabled, so the
// privilege is never enabled process-wide. Handles opened in fn stay valid.
func withBackupPrivilege(fn func() error) error {
	runtime.LockOSThread()
	if err := windows.ImpersonateSelf(windows.SecurityImpersonation); err != nil {
		runtime.UnlockOSThread()
		return fmt.Errorf("ImpersonateSelf: %w", err)
	}
	reverted := false
	defer func() {
		if windows.RevertToSelf() == nil {
			reverted = true
		}
		// If RevertToSelf failed the thread keeps a privileged token: leave it
		// locked so the runtime discards it when the goroutine exits.
		if reverted {
			runtime.UnlockOSThread()
		}
	}()
	var tok windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, true, &tok); err != nil {
		return fmt.Errorf("OpenThreadToken: %w", err)
	}
	defer tok.Close()
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeBackupPrivilege"), &luid); err != nil {
		return err
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
	// Called directly so the ERROR_NOT_ALL_ASSIGNED "success" (privilege not
	// held) is visible; x/sys's wrapper drops it.
	r, _, e := procAdjustTokenPrivileges.Call(uintptr(tok), 0, uintptr(unsafe.Pointer(&tp)), 0, 0, 0)
	if r == 0 {
		return fmt.Errorf("AdjustTokenPrivileges: %w", e)
	}
	if errors.Is(e, windows.ERROR_NOT_ALL_ASSIGNED) {
		return errors.New("SeBackupPrivilege not held by process token")
	}
	return fn()
}
