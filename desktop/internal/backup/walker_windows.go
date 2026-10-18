//go:build windows

package backup

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/snapshot"
)

// OSFS is the real FS. Listings use FindFirstFile, so attributes and the
// reparse tag come from the directory entry without opening (or recalling)
// anything. Paths are passed to Windows in the `\\?\` form.
type OSFS struct{}

var _ FS = OSFS{}

// Stat implements FS. The entry is read from its parent's listing; volume
// roots (which have none) fall back to GetFileAttributesEx.
func (OSFS) Stat(path string) (FileInfo, error) {
	p := filepath.Clean(path)
	if filepath.Dir(p) == p {
		var d windows.Win32FileAttributeData
		name, err := windows.UTF16PtrFromString(snapshot.ExtendedPath(p))
		if err != nil {
			return FileInfo{}, &fs.PathError{Op: "stat", Path: path, Err: err}
		}
		if err := windows.GetFileAttributesEx(name, windows.GetFileExInfoStandard, (*byte)(unsafe.Pointer(&d))); err != nil {
			return FileInfo{}, mapWinErr("stat", path, err)
		}
		return FileInfo{Name: p, Attrs: d.FileAttributes, Size: int64(d.FileSizeHigh)<<32 | int64(d.FileSizeLow),
			MTime: d.LastWriteTime.Nanoseconds(), CTime: d.CreationTime.Nanoseconds()}, nil
	}
	var d windows.Win32finddata
	name, err := windows.UTF16PtrFromString(snapshot.ExtendedPath(p))
	if err != nil {
		return FileInfo{}, &fs.PathError{Op: "stat", Path: path, Err: err}
	}
	h, err := windows.FindFirstFile(name, &d)
	if err != nil {
		return FileInfo{}, mapWinErr("stat", path, err)
	}
	windows.FindClose(h)
	return findInfo(&d), nil
}

// ReadDir implements FS.
func (OSFS) ReadDir(dir string) ([]FileInfo, error) {
	pattern, err := windows.UTF16PtrFromString(snapshot.ExtendedPath(filepath.Join(filepath.Clean(dir), "*")))
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: dir, Err: err}
	}
	var d windows.Win32finddata
	h, err := windows.FindFirstFile(pattern, &d)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return nil, nil // an empty volume root has no "." entry
		}
		return nil, mapWinErr("readdir", dir, err)
	}
	defer windows.FindClose(h)
	var out []FileInfo
	for {
		fi := findInfo(&d)
		if fi.Name != "." && fi.Name != ".." {
			out = append(out, fi)
		}
		if err := windows.FindNextFile(h, &d); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return out, nil
			}
			return out, mapWinErr("readdir", dir, err)
		}
	}
}

// Probe implements FS: a read open that neither follows reparse points nor
// recalls cloud data, then the volume serial and file index as the id.
func (OSFS) Probe(path string) (string, error) {
	name, err := windows.UTF16PtrFromString(snapshot.ExtendedPath(filepath.Clean(path)))
	if err != nil {
		return "", &fs.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_NO_RECALL, 0)
	if err != nil {
		return "", mapWinErr("open", path, err)
	}
	defer windows.CloseHandle(h)
	var bi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &bi); err != nil {
		return "", mapWinErr("fileinfo", path, err)
	}
	return fmt.Sprintf("%08x-%08x%08x", bi.VolumeSerialNumber, bi.FileIndexHigh, bi.FileIndexLow), nil
}

func findInfo(d *windows.Win32finddata) FileInfo {
	fi := FileInfo{
		Name:  windows.UTF16ToString(d.FileName[:]),
		Size:  int64(d.FileSizeHigh)<<32 | int64(d.FileSizeLow),
		MTime: d.LastWriteTime.Nanoseconds(),
		CTime: d.CreationTime.Nanoseconds(),
		Attrs: d.FileAttributes,
	}
	if fi.Attrs&AttrReparsePoint != 0 {
		fi.ReparseTag = d.Reserved0
	}
	return fi
}

// mapWinErr wraps a Win32 error in an *fs.PathError, prefixed with the
// walker sentinel it maps to. Access denied and not found keep matching
// fs.ErrPermission and fs.ErrNotExist through syscall.Errno.
func mapWinErr(op, path string, err error) error {
	pe := &fs.PathError{Op: op, Path: path, Err: err}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return pe
	}
	switch errno {
	case windows.ERROR_SHARING_VIOLATION, windows.ERROR_LOCK_VIOLATION:
		return fmt.Errorf("%w: %w", ErrLocked, pe)
	case windows.ERROR_ENCRYPTION_FAILED:
		return fmt.Errorf("%w: %w", ErrEFS, pe)
	case windows.ERROR_CLOUD_FILE_PROVIDER_NOT_RUNNING:
		return fmt.Errorf("%w: %w", ErrCloudPlaceholder, pe)
	case windows.ERROR_FILENAME_EXCED_RANGE:
		return fmt.Errorf("%w: %w", ErrPathTooLong, pe)
	}
	return pe
}
