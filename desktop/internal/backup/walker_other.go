//go:build !windows

package backup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// OSFS is the FS for non-Windows builds (development and tests): Lstat and
// ReadDir, with links and special files reported as unfollowed reparse
// points and the file id taken from dev:ino.
type OSFS struct{}

var _ FS = OSFS{}

// Stat implements FS.
func (OSFS) Stat(path string) (FileInfo, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return FileInfo{}, mapOSErr(err)
	}
	return osInfo(st), nil
}

// ReadDir implements FS.
func (OSFS) ReadDir(dir string) ([]FileInfo, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, mapOSErr(err)
	}
	out := make([]FileInfo, 0, len(ents))
	for _, e := range ents {
		st, err := e.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // deleted while listing
			}
			return out, mapOSErr(err)
		}
		out = append(out, osInfo(st))
	}
	return out, nil
}

// Probe implements FS. Links are refused rather than followed.
func (OSFS) Probe(path string) (string, error) {
	lst, err := os.Lstat(path)
	if err != nil {
		return "", mapOSErr(err)
	}
	if !lst.Mode().IsRegular() {
		return "", &fs.PathError{Op: "open", Path: path, Err: fs.ErrInvalid}
	}
	f, err := os.Open(path)
	if err != nil {
		return "", mapOSErr(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", mapOSErr(err)
	}
	if !os.SameFile(lst, st) {
		return "", &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist} // replaced while probing
	}
	return fileID(st), nil
}

func fileID(st fs.FileInfo) string {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%x:%x", uint64(sys.Dev), uint64(sys.Ino))
	}
	return ""
}

func osInfo(st fs.FileInfo) FileInfo {
	fi := FileInfo{Name: filepath.Base(st.Name()), Size: st.Size(), MTime: st.ModTime().UnixNano()}
	m := st.Mode()
	switch {
	case m.IsDir():
		fi.Attrs |= AttrDirectory
		fi.Size = 0
	case m&fs.ModeSymlink != 0:
		fi.Attrs |= AttrReparsePoint
		fi.ReparseTag = reparseTagSymlink
	case !m.IsRegular():
		fi.Attrs |= AttrReparsePoint // device, pipe or socket: tag 0, never read
	default:
		fi.Attrs |= AttrArchive
	}
	if m.Perm()&0o222 == 0 {
		fi.Attrs |= AttrReadOnly
	}
	return fi
}

func mapOSErr(err error) error {
	if errors.Is(err, syscall.ENAMETOOLONG) {
		return fmt.Errorf("%w: %w", ErrPathTooLong, err)
	}
	return err
}
