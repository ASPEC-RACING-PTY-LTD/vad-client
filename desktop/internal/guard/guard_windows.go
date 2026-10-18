//go:build windows

package guard

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

const volumeNameDOS = 0x0 // VOLUME_NAME_DOS | FILE_NAME_NORMALIZED

// reparseTagNameSurrogate marks reparse tags that redirect to another
// name (symlinks, junctions, mount points, WSL symlinks). Cloud-file and
// dedup tags are not name surrogates and are allowed.
const reparseTagNameSurrogate = 0x20000000

// SystemEnv returns the protected locations of the running machine from
// the known-folder API, falling back to environment variables.
func SystemEnv() Env {
	kf := func(id *windows.KNOWNFOLDERID, envVar string) string {
		if p, err := windows.KnownFolderPath(id, windows.KF_FLAG_DONT_VERIFY); err == nil && p != "" {
			return p
		}
		return os.Getenv(envVar)
	}
	e := Env{
		WinDir:          kf(windows.FOLDERID_Windows, "WINDIR"),
		ProgramFiles:    kf(windows.FOLDERID_ProgramFiles, "ProgramFiles"),
		ProgramFilesX86: kf(windows.FOLDERID_ProgramFilesX86, "ProgramFiles(x86)"),
		ProgramFilesX64: kf(windows.FOLDERID_ProgramFilesX64, "ProgramW6432"),
		ProgramData:     kf(windows.FOLDERID_ProgramData, "ProgramData"),
		ProfilesDir:     kf(windows.FOLDERID_UserProfiles, ""),
		SystemDrive:     os.Getenv("SystemDrive"),
	}
	if e.WinDir == "" {
		e.WinDir = `C:\Windows`
	}
	if e.SystemDrive == "" {
		e.SystemDrive = filepath.VolumeName(e.WinDir)
	}
	if e.ProfilesDir == "" {
		e.ProfilesDir = e.SystemDrive + `\Users`
	}
	return e
}

// extended renders c as a \\?\ path so long paths work in Win32 calls.
func extended(c cpath) string {
	s := c.String()
	if isUNC(c.vol) {
		return `\\?\UNC\` + s[2:]
	}
	return `\\?\` + s
}

func exists(c cpath) bool {
	p, err := windows.UTF16PtrFromString(extended(c))
	if err != nil {
		return false
	}
	_, err = windows.GetFileAttributes(p)
	return err == nil
}

// existingPrefix returns how many leading components of c exist.
func existingPrefix(c cpath) int {
	for n := len(c.comps); n > 0; n-- {
		if exists(c.prefix(n)) {
			return n
		}
	}
	return 0
}

// rebase replaces the first n components of c with the OS-reported path
// got, keeping the remaining components.
func rebase(c cpath, n int, got string) (cpath, bool) {
	r, err := lexical(got)
	if err != nil {
		return c, false
	}
	r.comps = append(r.comps, c.comps[n:]...)
	return r, true
}

// resolveLong expands 8.3 short names in the existing prefix of c.
func resolveLong(c cpath) cpath {
	n := existingPrefix(c)
	if n == 0 {
		return c
	}
	p, err := windows.UTF16PtrFromString(extended(c.prefix(n)))
	if err != nil {
		return c
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	got, err := windows.GetLongPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || got == 0 || int(got) > len(buf) {
		return c
	}
	r, ok := rebase(c, n, windows.UTF16ToString(buf[:got]))
	if !ok {
		return c
	}
	return r
}

// finalPath resolves links, junctions and subst drives in the existing
// prefix of c. ok is false when nothing exists or resolution fails.
func finalPath(c cpath) (cpath, bool) {
	n := existingPrefix(c)
	if n == 0 {
		return c, false
	}
	p, err := windows.UTF16PtrFromString(extended(c.prefix(n)))
	if err != nil {
		return c, false
	}
	h, err := windows.CreateFile(p, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return c, false
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	got, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), volumeNameDOS)
	if err != nil || got == 0 || int(got) > len(buf) {
		return c, false
	}
	return rebase(c, n, windows.UTF16ToString(buf[:got]))
}

// reparseComponent returns the first existing component of c (below the
// volume root) that is a name-surrogate reparse point. Errors while
// reading a reparse tag count as a reparse point (fail closed).
func reparseComponent(c cpath) (string, bool) {
	n := existingPrefix(c)
	for i := 1; i <= n; i++ {
		sub := c.prefix(i)
		p, err := windows.UTF16PtrFromString(extended(sub))
		if err != nil {
			return sub.String(), true
		}
		attrs, err := windows.GetFileAttributes(p)
		if err != nil {
			// Vanished between calls; stop walking.
			return "", false
		}
		if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
			continue
		}
		var fd windows.Win32finddata
		fh, err := windows.FindFirstFile(p, &fd)
		if err != nil {
			return sub.String(), true
		}
		windows.FindClose(fh)
		if fd.Reserved0&reparseTagNameSurrogate != 0 {
			return sub.String(), true
		}
	}
	return "", false
}
