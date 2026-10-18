// Package guard implements VaultDock's system-file protection (MASTER_PLAN
// §7.6, api-v1 §10.3). Every write, delete, rename, restore target and
// two-way sync root must pass Check before the agent touches the file
// system. There is no override: a block surfaces as a guard_blocked issue.
//
// The guard always applies Windows path semantics (drive letters, UNC,
// case-insensitive comparison), whatever GOOS it is built for. Windows
// builds additionally consult the file system: 8.3 short names are expanded
// (GetLongPathName), the final path is resolved (GetFinalPathNameByHandle,
// which also unmasks subst drives and links) and every existing path
// component is checked for name-surrogate reparse points (symlinks,
// junctions, volume mount points). Non-Windows builds perform only the
// lexical checks; they exist so shared code compiles and vets in CI.
package guard

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Op is the file-system operation being authorised.
type Op uint8

const (
	// OpRead reads a file or lists a directory (backup sources).
	OpRead Op = iota + 1
	// OpWrite creates or modifies a file or directory, including renames
	// and two-way sync downloads.
	OpWrite
	// OpDelete removes a file or directory (to the Recycle Bin).
	OpDelete
	// OpRestoreWrite writes a restored file. It is OpWrite plus the
	// requirement that the target profile is identified (Profile.SID).
	OpRestoreWrite
)

// String returns the lower-case operation name used in issues and logs.
func (o Op) String() string {
	switch o {
	case OpRead:
		return "read"
	case OpWrite:
		return "write"
	case OpDelete:
		return "delete"
	case OpRestoreWrite:
		return "restore-write"
	}
	return fmt.Sprintf("op(%d)", uint8(o))
}

func (o Op) valid() bool { return o >= OpRead && o <= OpRestoreWrite }

// mutates reports whether the operation changes the file system.
func (o Op) mutates() bool { return o == OpWrite || o == OpDelete || o == OpRestoreWrite }

// Profile is the Windows profile on whose behalf the operation runs. The
// caller (agent) is responsible for passing only profiles whose consent
// state is authorised (§7.8); the guard enforces path scope only.
type Profile struct {
	// SID of the profile owner, e.g. "S-1-5-21-…-1001". Required for
	// OpRestoreWrite.
	SID string
	// Root is the profile directory, e.g. C:\Users\alice.
	Root string
	// AuthorisedRoots are additional profile directories the operation may
	// touch, e.g. other consented profiles in Advanced mode.
	AuthorisedRoots []string
}

// Sentinel errors. Every error returned by Check is a *BlockedError that
// wraps exactly one of these, so callers can use errors.Is.
var (
	// ErrProtected: the path is in a protected system location.
	ErrProtected = errors.New("guard: protected system location")
	// ErrReparse: a path component is a symlink, junction or mount point.
	ErrReparse = errors.New("guard: path traverses a reparse point")
	// ErrOutsideProfile: the path belongs to another user's profile that
	// the operation is not authorised for.
	ErrOutsideProfile = errors.New("guard: path outside the authorised profile")
	// ErrInvalidPath: the path is relative, malformed, a device or
	// volume-GUID path, or names an alternate data stream.
	ErrInvalidPath = errors.New("guard: invalid path")
)

// BlockedError describes a refused operation.
type BlockedError struct {
	Op     Op
	Path   string // the path as given by the caller
	Reason string // short human-readable reason, safe to log
	Err    error  // one of the sentinel errors
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("%v: %s %q: %s", e.Err, e.Op, e.Path, e.Reason)
}

func (e *BlockedError) Unwrap() error { return e.Err }

// Env lists the machine locations the guard protects. Empty fields are
// ignored. SystemEnv returns the values for the running machine.
type Env struct {
	WinDir          string // %WINDIR%
	ProgramFiles    string // %ProgramFiles%
	ProgramFilesX86 string // %ProgramFiles(x86)%
	ProgramFilesX64 string // %ProgramW6432% (may equal ProgramFiles)
	ProgramData     string // %ProgramData%
	ProfilesDir     string // parent of user profiles, e.g. C:\Users
	SystemDrive     string // e.g. "C:"; informational (every volume root is protected)
}

// Guard checks paths against a fixed Env. It is safe for concurrent use.
type Guard struct {
	env       Env
	protected []string // canonical roots denied for mutation
	readDeny  []string // canonical roots denied for every op
	profiles  string   // canonical ProfilesDir, or ""
}

// rootNamesAlways are volume-root entries refused for every operation.
var rootNamesAlways = map[string]bool{
	"system volume information": true,
	"$recycle.bin":              true,
	"recycler":                  true,
	"pagefile.sys":              true,
	"hiberfil.sys":              true,
	"swapfile.sys":              true,
}

// rootNamesMutate are volume-root entries additionally refused for write,
// delete and restore-write (boot and recovery files).
var rootNamesMutate = map[string]bool{
	"boot":              true,
	"bootmgr":           true,
	"bootnxt":           true,
	"bootsect.bak":      true,
	"efi":               true,
	"recovery":          true,
	"$winreagent":       true,
	"$windows.~bt":      true,
	"$windows.~ws":      true,
	"config.msi":        true,
	"dumpstack.log":     true,
	"dumpstack.log.tmp": true,
}

// profileNamesProtected are entries of ProfilesDir that are not user
// profiles and must never be written or read through the guard.
var profileNamesProtected = map[string]bool{
	"default":      true,
	"default user": true,
	"all users":    true,
}

// New builds a Guard. Env paths must be absolute; they are canonicalised
// lexically (no file-system access).
func New(env Env) (*Guard, error) {
	g := &Guard{env: env}
	canon := func(name, p string) (string, error) {
		if p == "" {
			return "", nil
		}
		c, err := lexical(p)
		if err != nil {
			return "", fmt.Errorf("guard: env %s: %w", name, err)
		}
		return c.String(), nil
	}
	for _, f := range []struct{ name, val string }{
		{"WinDir", env.WinDir},
		{"ProgramFiles", env.ProgramFiles},
		{"ProgramFilesX86", env.ProgramFilesX86},
		{"ProgramFilesX64", env.ProgramFilesX64},
		{"ProgramData", env.ProgramData},
	} {
		c, err := canon(f.name, f.val)
		if err != nil {
			return nil, err
		}
		if c != "" {
			g.protected = append(g.protected, c)
		}
	}
	if pd, _ := canon("ProgramData", env.ProgramData); pd != "" {
		g.readDeny = append(g.readDeny, pd+`\Microsoft`, pd+`\VaultDock`)
	}
	var err error
	if g.profiles, err = canon("ProfilesDir", env.ProfilesDir); err != nil {
		return nil, err
	}
	return g, nil
}

var (
	defaultOnce  sync.Once
	defaultGuard *Guard
	defaultErr   error
)

// Default returns the Guard for the running machine (SystemEnv), built
// once.
func Default() (*Guard, error) {
	defaultOnce.Do(func() { defaultGuard, defaultErr = New(SystemEnv()) })
	return defaultGuard, defaultErr
}

// Check authorises op on path for profile using the machine's Guard. It
// returns nil when allowed and a *BlockedError otherwise. It fails closed.
func Check(op Op, path string, profile Profile) error {
	g, err := Default()
	if err != nil {
		return &BlockedError{Op: op, Path: path, Reason: "guard unavailable: " + err.Error(), Err: ErrProtected}
	}
	return g.Check(op, path, profile)
}

// Canonicalize returns the canonical form of an absolute path: cleaned,
// separators normalised, \\?\ prefixes removed, trailing dots and spaces
// stripped and, on Windows, 8.3 names expanded for the existing prefix.
// It rejects relative, device and ADS paths with ErrInvalidPath.
func Canonicalize(path string) (string, error) {
	c, err := lexical(path)
	if err != nil {
		return "", err
	}
	return resolveLong(c).String(), nil
}

// Check authorises op on path for profile. It returns nil when allowed and
// a *BlockedError (wrapping a sentinel error) otherwise.
func (g *Guard) Check(op Op, path string, profile Profile) error {
	block := func(err error, reason string) error {
		return &BlockedError{Op: op, Path: path, Reason: reason, Err: err}
	}
	if !op.valid() {
		return block(ErrProtected, "unknown operation")
	}
	if op == OpRestoreWrite && strings.TrimSpace(profile.SID) == "" {
		return block(ErrOutsideProfile, "restore requires an authorised target profile")
	}
	c, err := lexical(path)
	if err != nil {
		return block(ErrInvalidPath, strings.TrimPrefix(err.Error(), ErrInvalidPath.Error()+": "))
	}
	roots, err := profileRoots(profile)
	if err != nil {
		return block(ErrOutsideProfile, "invalid profile root")
	}
	// Check the lexical form first (no file-system access for paths that
	// are plainly protected), then the form with 8.3 names expanded.
	if reason, err := g.checkPath(op, c, roots); err != nil {
		return block(err, reason)
	}
	if long := resolveLong(c); !strings.EqualFold(long.String(), c.String()) {
		c = long
		if reason, err := g.checkPath(op, c, roots); err != nil {
			return block(err, reason)
		}
	}
	// Check the target after following links and subst drives, so a link
	// cannot smuggle a read or write into a protected location.
	if fp, ok := finalPath(c); ok && !strings.EqualFold(fp.String(), c.String()) {
		if reason, err := g.checkPath(op, fp, roots); err != nil {
			return block(err, reason+" (via link target)")
		}
	}
	if op.mutates() {
		if at, ok := reparseComponent(c); ok {
			return block(ErrReparse, "reparse point at "+at)
		}
	}
	return nil
}

func profileRoots(p Profile) ([]cpath, error) {
	var out []cpath
	for _, r := range append([]string{p.Root}, p.AuthorisedRoots...) {
		if strings.TrimSpace(r) == "" {
			continue
		}
		c, err := lexical(r)
		if err != nil {
			return nil, err
		}
		if len(c.comps) == 0 {
			return nil, ErrInvalidPath // a volume root is never a profile
		}
		// Keep both forms so short-name and long-name paths both match.
		out = append(out, c, resolveLong(c))
	}
	return out, nil
}

// checkPath applies the deny rules to a canonical path.
func (g *Guard) checkPath(op Op, c cpath, roots []cpath) (reason string, err error) {
	s := c.String()
	mut := op.mutates()

	if isUNC(c.vol) && strings.HasSuffix(c.vol, "$") {
		// C$, ADMIN$ etc. expose whole volumes and the Windows directory.
		return "administrative share", ErrProtected
	}
	if len(c.comps) == 0 {
		if mut {
			return "volume root", ErrProtected
		}
		return "", nil
	}
	first := strings.ToLower(c.comps[0])
	if rootNamesAlways[first] || (mut && rootNamesMutate[first]) {
		return "protected volume entry " + c.comps[0], ErrProtected
	}
	for _, r := range g.readDeny {
		if under(s, r) {
			return "protected location " + r, ErrProtected
		}
	}
	if mut {
		for _, r := range g.protected {
			if under(s, r) {
				return "protected location " + r, ErrProtected
			}
		}
	}

	if g.profiles != "" && under(s, g.profiles) {
		pc, _ := lexical(g.profiles)
		if len(c.comps) == len(pc.comps) {
			if mut {
				return "profiles directory", ErrProtected
			}
			return "", nil
		}
		name := strings.ToLower(c.comps[len(pc.comps)])
		switch {
		case name == "public":
			// Shared by all users; ordinary ACLs apply.
		case profileNamesProtected[name]:
			return "system profile " + c.comps[len(pc.comps)], ErrProtected
		default:
			ok := false
			for _, r := range roots {
				if under(s, r.String()) {
					ok = true
					break
				}
			}
			if !ok {
				return "another user's profile", ErrOutsideProfile
			}
		}
	}
	return "", nil
}

// under reports whether path p equals root or lies beneath it
// (case-insensitive). Both must be canonical.
func under(p, root string) bool {
	if strings.EqualFold(p, root) {
		return true
	}
	r := strings.TrimSuffix(root, `\`) + `\`
	return len(p) > len(r) && strings.EqualFold(p[:len(r)], r)
}
