package backup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/tokens"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/automation"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// Local issue classes (S4U spike §2). They are stored in backup.db as is;
// WireClass maps them for POST /backup/runs/{id}/issues.
const (
	IssueClassEFSNeedsLogon     = "efs_needs_logon"     // EFS file under an S4U token
	IssueClassProfileNeedsLogon = "profile_needs_logon" // no token for a logged-off profile (Entra ID, DC offline)
)

// WireClass maps a local issue class to an api-v1 §6 class (api-v1 §14
// Rev 1.1b); every other class is returned unchanged.
func WireClass(class string) string {
	switch class {
	case IssueClassEFSNeedsLogon:
		return wire.IssueClassAccessDenied
	case IssueClassProfileNeedsLogon:
		return wire.IssueClassOther
	}
	return class
}

// Report converts a pending issue for POST /backup/runs/{id}/issues. A
// local class is sent as its wire class with the local reason at the start
// of detail (api-v1 §14 Rev 1.1b).
func (p PendingIssue) Report() wire.IssueReport {
	class, detail := WireClass(p.Class), p.Detail
	if class != p.Class {
		detail = p.Class + ": " + detail
	}
	return wire.IssueReport{Path: p.Path, Class: class, Severity: p.Severity, Detail: detail,
		FirstSeen: p.FirstSeen, LastSeen: p.LastSeen, Count: p.Count, Retryable: p.Retryable}
}

// Windows file attributes the walker looks at (winnt.h).
const (
	AttrReadOnly           uint32 = 0x1
	AttrHidden             uint32 = 0x2
	AttrSystem             uint32 = 0x4
	AttrDirectory          uint32 = 0x10
	AttrArchive            uint32 = 0x20
	AttrReparsePoint       uint32 = 0x400
	AttrOffline            uint32 = 0x1000
	AttrEncrypted          uint32 = 0x4000
	AttrRecallOnOpen       uint32 = 0x40000
	AttrRecallOnDataAccess uint32 = 0x400000
)

// Reparse tags the walker treats as ordinary files (their data is local
// or recalled by attribute rules below). Every other tag, and every
// name-surrogate tag (symlink, junction, mount point), is never followed.
const (
	reparseTagNameSurrogate uint32 = 0x20000000
	reparseTagSymlink       uint32 = 0xA000000C
	reparseTagDedup         uint32 = 0x80000013
	reparseTagWOF           uint32 = 0x80000017
	reparseTagOneDrive      uint32 = 0x80000021
	reparseTagCloud         uint32 = 0x9000001A // CLOUD, CLOUD_1..F differ in bits 12-15
)

func allowedReparse(tag uint32) bool {
	if tag&reparseTagNameSurrogate != 0 {
		return false
	}
	return tag&0xFFFF0FFF == reparseTagCloud || tag == reparseTagDedup || tag == reparseTagWOF || tag == reparseTagOneDrive
}

// isPlaceholder reports a cloud-only or offline file: reading it would
// download (hydrate) it, so the walker never opens it.
func isPlaceholder(attrs uint32) bool {
	return attrs&(AttrRecallOnDataAccess|AttrRecallOnOpen|AttrOffline) != 0
}

// FileInfo is what the walker knows about a directory entry without
// opening it. Times are unix nanoseconds.
type FileInfo struct {
	Name       string
	Size       int64
	MTime      int64 // last write
	CTime      int64 // creation
	Attrs      uint32
	ReparseTag uint32 // valid when Attrs has AttrReparsePoint
	FileID     string // set by Probe (or from the cache for unchanged files)
}

// IsDir reports a directory.
func (fi FileInfo) IsDir() bool { return fi.Attrs&AttrDirectory != 0 }

// FS is the file-system access the walker needs. It is injectable for
// tests; OSFS is the real one. All calls run inside the profile's
// impersonation. None may follow reparse points or recall cloud data.
type FS interface {
	// Stat describes path itself (never a link target).
	Stat(path string) (FileInfo, error)
	// ReadDir lists dir (attributes from the directory listing only).
	ReadDir(dir string) ([]FileInfo, error)
	// Probe opens path for reading without following reparse points and
	// without recall, and returns its stable file id. It proves the file
	// is readable now (ACL, sharing, EFS).
	Probe(path string) (fileID string, err error)
}

// Errors an FS maps OS failures to (besides fs.ErrPermission and
// fs.ErrNotExist).
var (
	ErrLocked           = errors.New("backup: file is locked by another process")
	ErrEFS              = errors.New("backup: encrypted file cannot be decrypted with this token")
	ErrCloudPlaceholder = errors.New("backup: cloud file provider is not available")
	ErrPathTooLong      = errors.New("backup: path too long")
)

// ErrProfileNeedsLogon is returned by Walk when no token can be obtained
// for a logged-off profile; the job is skipped until the user signs in.
var ErrProfileNeedsLogon = errors.New("backup: profile needs the user to sign in")

// Entry is one file to back up or, with Walker.EmitDirs, one directory
// (FileInfo.IsDir; Unchanged and Cached are then unset).
type Entry struct {
	Path      string // concrete path
	TokenPath string // backup-format §6 path (root token + components)
	FileInfo
	// Unchanged is set when the file cache matches (same size, mtime,
	// ctime and key epoch); Cached then holds the entry to reuse.
	Unchanged bool
	Cached    *CachedFile
}

// CacheLookup returns the file cache entry for a concrete path.
type CacheLookup func(ctx context.Context, path string) (CachedFile, bool, error)

// StoreCache adapts Store.FileCache for one job.
func StoreCache(s *Store, jobID int64) CacheLookup {
	return func(ctx context.Context, path string) (CachedFile, bool, error) { return s.FileCache(ctx, jobID, path) }
}

// WalkSpec is what one walk covers.
type WalkSpec struct {
	ProfileSID string
	Paths      []string        // token paths (wire.JobSpec.Paths)
	Exclude    []string        // glob patterns (see excluded)
	Tokens     snapshot.Tokens // the profile's concrete token values
	Profile    guard.Profile   // for guard.Check; SID and Root of the profile
	Epoch      int64           // current key epoch; cache entries of other epochs are stale
}

// SpecFor builds a WalkSpec from a job; tok and prof come from the
// profile's known folders.
func SpecFor(j *Job, tok snapshot.Tokens, prof guard.Profile, epoch int64) WalkSpec {
	if prof.SID == "" {
		prof.SID = j.ProfileSID
	}
	return WalkSpec{ProfileSID: j.ProfileSID, Paths: j.Spec.Paths, Exclude: j.Spec.Exclude, Tokens: tok, Profile: prof, Epoch: epoch}
}

// WalkStats summarises a walk.
type WalkStats struct {
	Source    tokens.Source
	Dirs      int64
	Files     int64 // entries emitted
	Unchanged int64
	Bytes     int64 // size of emitted entries
	Skipped   int64 // issues reported
	Excluded  int64
	Reparse   int64 // reparse points not followed
	// Complete is false when a root or directory could not be listed, so
	// the file cache must not be pruned after this run.
	Complete bool
}

// Walker enumerates a job's files as the profile's user.
type Walker struct {
	Tokens tokens.Provider                                       // nil = tokens.Default
	Guard  func(op guard.Op, path string, p guard.Profile) error // nil = guard.Check
	FS     FS                                                    // nil = OSFS{}
	Cache  CacheLookup                                           // nil = every file is changed
	Log    *slog.Logger
	// EmitDirs also emits an entry for every directory that is listed
	// (directory roots included), with its mtime, ctime and attributes, so
	// empty directories and directory metadata can be recorded. Excluded,
	// guard-blocked, cloud-only, unlisted and reparse-point directories are
	// not emitted.
	EmitDirs bool
}

type walk struct {
	w      *Walker
	ctx    context.Context
	spec   WalkSpec
	tok    tokens.Token
	fs     FS
	guard  func(op guard.Op, path string, p guard.Profile) error
	emit   func(Entry) error
	report func(Issue)
	st     WalkStats
	excl   []string
}

// Walk enumerates spec as the profile's user: one token per walk
// (tokens.ForProfile), directory listings and file probes inside
// Impersonate, every root and file checked with guard.Check(OpRead).
// Reparse points are never followed, cloud placeholders never opened and
// EFS files skipped under S4U. emit receives entries in a stable
// depth-first pre-order: per root, a directory's entry (with EmitDirs)
// comes right after its listing succeeds, then its files sorted by name,
// then each subdirectory (sorted by name) recursively; roots go shortest
// path first. emit's error aborts the walk. report receives every skip (it may be nil).
// ctx cancels the walk with ctx.Err().
func (w *Walker) Walk(ctx context.Context, spec WalkSpec, emit func(Entry) error, report func(Issue)) (WalkStats, error) {
	prov := w.Tokens
	if prov == nil {
		prov = tokens.Default
	}
	k := &walk{w: w, ctx: ctx, spec: spec, fs: w.FS, guard: w.Guard, emit: emit, report: report}
	if k.fs == nil {
		k.fs = OSFS{}
	}
	if k.guard == nil {
		k.guard = guard.Check
	}
	if k.report == nil {
		k.report = func(Issue) {}
	}
	for _, p := range spec.Exclude {
		if p = strings.TrimSpace(strings.ReplaceAll(p, `\`, "/")); p != "" {
			k.excl = append(k.excl, p)
		}
	}
	tok, err := prov.ForProfile(spec.ProfileSID)
	if err != nil {
		if errors.Is(err, tokens.ErrNeedsLogon) || errors.Is(err, tokens.ErrNotLoggedOn) || errors.Is(err, tokens.ErrS4UUnavailable) {
			k.issue(Issue{Class: IssueClassProfileNeedsLogon, Severity: wire.IssueSeveritySkipped,
				Detail: "the profile's user must sign in before it can be backed up", Retryable: true})
			return k.st, fmt.Errorf("%w: %w", ErrProfileNeedsLogon, err)
		}
		return k.st, err
	}
	defer tok.Close()
	k.tok = tok
	k.st.Source = tok.Source()
	k.st.Complete = true

	roots, err := k.roots()
	if err != nil {
		return k.st, err
	}
	for _, r := range roots {
		if err := ctx.Err(); err != nil {
			return k.st, err
		}
		if err := k.walkRoot(r); err != nil {
			return k.st, err
		}
	}
	return k.st, ctx.Err()
}

type root struct{ tp, path string }

// roots resolves the token paths, drops duplicates and roots nested in
// another root (they are walked as part of the outer one).
func (k *walk) roots() ([]root, error) {
	var rs []root
	for _, tp := range k.spec.Paths {
		p, err := k.spec.Tokens.Resolve(tp)
		if err != nil {
			k.st.Complete = false
			k.issue(Issue{Path: tp, Class: wire.IssueClassNotFound, Severity: wire.IssueSeverityWarning,
				Detail: "the backup folder could not be resolved for this profile: " + err.Error()})
			continue
		}
		rs = append(rs, root{tp: strings.TrimSuffix(tp, "/"), path: filepath.Clean(p)})
	}
	slices.SortStableFunc(rs, func(a, b root) int { return len(a.path) - len(b.path) })
	var out []root
	for _, r := range rs {
		if !slices.ContainsFunc(out, func(o root) bool { return within(r.path, o.path) }) {
			out = append(out, r)
		}
	}
	return out, nil
}

// within reports whether p is base or below it (case-insensitive).
func within(p, base string) bool {
	if strings.EqualFold(p, base) {
		return true
	}
	b := strings.TrimRight(base, `\/`)
	return len(p) > len(b) && strings.EqualFold(p[:len(b)], b) && (p[len(b)] == '\\' || p[len(b)] == '/')
}

func (k *walk) issue(is Issue) {
	k.st.Skipped++
	k.report(is)
}

func (k *walk) log() *slog.Logger {
	if k.w.Log != nil {
		return k.w.Log
	}
	return slog.Default()
}

// errIssue turns an FS error into an issue; vanished files are ignored.
func (k *walk) errIssue(path string, err error, encrypted bool) {
	is := Issue{Path: path, Severity: wire.IssueSeveritySkipped, Detail: err.Error()}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return // deleted while walking
	case errors.Is(err, ErrLocked):
		is.Class, is.Retryable = wire.IssueClassLocked, true
	case errors.Is(err, ErrEFS), encrypted && errors.Is(err, fs.ErrPermission) && !k.tok.Caps().EFS:
		is.Class, is.Retryable = IssueClassEFSNeedsLogon, true
	case errors.Is(err, ErrCloudPlaceholder):
		is.Class = wire.IssueClassCloudPlaceholder
	case errors.Is(err, fs.ErrPermission):
		is.Class = wire.IssueClassAccessDenied
	case errors.Is(err, ErrPathTooLong):
		is.Class = wire.IssueClassPathTooLong
	default:
		is.Class, is.Retryable = wire.IssueClassIO, true
	}
	k.issue(is)
}

func (k *walk) guardOK(path string) bool {
	err := k.guard(guard.OpRead, path, k.spec.Profile)
	if err == nil {
		return true
	}
	detail := "blocked by system-file protection"
	var be *guard.BlockedError
	if errors.As(err, &be) {
		detail += ": " + be.Reason
	}
	k.issue(Issue{Path: path, Class: wire.IssueClassGuardBlocked, Severity: wire.IssueSeveritySkipped, Detail: detail})
	return false
}

// impersonate runs fn as the user; a failure to impersonate is fatal.
func (k *walk) impersonate(fn func()) error {
	if err := k.tok.Impersonate(func() error { fn(); return nil }); err != nil {
		return fmt.Errorf("backup: impersonate profile: %w", err)
	}
	return nil
}

func (k *walk) walkRoot(r root) error {
	if !k.guardOK(r.path) {
		k.st.Complete = false
		return nil
	}
	var fi FileInfo
	var serr error
	if err := k.impersonate(func() { fi, serr = k.fs.Stat(r.path) }); err != nil {
		return err
	}
	if serr != nil {
		k.st.Complete = false
		if errors.Is(serr, fs.ErrNotExist) {
			k.issue(Issue{Path: r.path, Class: wire.IssueClassNotFound, Severity: wire.IssueSeverityWarning, Detail: "the backup folder does not exist"})
			return nil
		}
		k.errIssue(r.path, serr, false)
		return nil
	}
	if fi.Attrs&AttrReparsePoint != 0 && !allowedReparse(fi.ReparseTag) {
		k.st.Reparse++
		k.st.Complete = false
		k.issue(Issue{Path: r.path, Class: wire.IssueClassOther, Severity: wire.IssueSeveritySkipped,
			Detail: "the backup folder is a link (reparse point) and is not followed"})
		return nil
	}
	if !fi.IsDir() {
		fi.Name = filepath.Base(r.path)
		return k.batch([]item{{path: r.path, tp: r.tp, rel: fi.Name, fi: fi}})
	}
	if fi.Name == "" {
		fi.Name = filepath.Base(r.path)
	}
	return k.walkDir(r.path, r.tp, "", fi)
}

type item struct {
	path, tp, rel string
	fi            FileInfo
}

// walkDir lists dir (described by fi), emits its entry with EmitDirs, then
// its contents.
func (k *walk) walkDir(dir, tp, rel string, fi FileInfo) error {
	if err := k.ctx.Err(); err != nil {
		return err
	}
	k.st.Dirs++
	var list []FileInfo
	var lerr error
	if err := k.impersonate(func() { list, lerr = k.fs.ReadDir(dir) }); err != nil {
		return err
	}
	if lerr != nil {
		if !errors.Is(lerr, fs.ErrNotExist) {
			k.st.Complete = false
		}
		k.errIssue(dir, lerr, false)
		return nil
	}
	if k.w.EmitDirs {
		if err := k.emit(Entry{Path: dir, TokenPath: tp, FileInfo: fi}); err != nil {
			return err
		}
	}
	slices.SortFunc(list, func(a, b FileInfo) int { return strings.Compare(a.Name, b.Name) })
	items := make([]item, 0, len(list))
	for _, fi := range list {
		if fi.Name == "" || fi.Name == "." || fi.Name == ".." {
			continue
		}
		crel := fi.Name
		if rel != "" {
			crel = rel + "/" + fi.Name
		}
		items = append(items, item{path: filepath.Join(dir, fi.Name), tp: tp + "/" + fi.Name, rel: crel, fi: fi})
	}
	return k.batch(items)
}

// batch classifies one directory's entries, probes changed files in one
// impersonation, emits files and then descends into subdirectories.
func (k *walk) batch(items []item) error {
	var files []*Entry
	var probe []*Entry
	var dirs []item
	for _, it := range items {
		if err := k.ctx.Err(); err != nil {
			return err
		}
		fi := it.fi
		if k.excluded(it.rel, it.tp, fi.Name, fi.IsDir()) {
			k.st.Excluded++
			continue
		}
		if fi.Attrs&AttrReparsePoint != 0 && !allowedReparse(fi.ReparseTag) {
			k.st.Reparse++
			k.log().Debug("backup: reparse point not followed", "tag", fmt.Sprintf("%#x", fi.ReparseTag))
			continue
		}
		if fi.IsDir() {
			if fi.Attrs&(AttrRecallOnOpen|AttrRecallOnDataAccess) != 0 {
				k.issue(Issue{Path: it.path, Class: wire.IssueClassCloudPlaceholder, Severity: wire.IssueSeveritySkipped,
					Detail: "cloud-only folder; its contents are not downloaded for backup"})
				continue
			}
			if k.guardOK(it.path) {
				dirs = append(dirs, it)
			}
			continue
		}
		if isPlaceholder(fi.Attrs) {
			k.issue(Issue{Path: it.path, Class: wire.IssueClassCloudPlaceholder, Severity: wire.IssueSeveritySkipped,
				Detail: "cloud-only file (online-only placeholder); it is not downloaded for backup"})
			continue
		}
		if fi.Attrs&AttrEncrypted != 0 && !k.tok.Caps().EFS {
			k.issue(Issue{Path: it.path, Class: IssueClassEFSNeedsLogon, Severity: wire.IssueSeveritySkipped,
				Detail: "EFS-encrypted file; it is backed up while the user is signed in", Retryable: true})
			continue
		}
		if !k.guardOK(it.path) {
			continue
		}
		e := &Entry{Path: it.path, TokenPath: it.tp, FileInfo: fi}
		if k.w.Cache != nil {
			c, ok, err := k.w.Cache(k.ctx, it.path)
			if err != nil {
				return fmt.Errorf("backup: file cache: %w", err)
			}
			if ok && len(c.H) > 0 && c.Epoch == k.spec.Epoch && c.Size == fi.Size && c.MTime == fi.MTime && c.CTime == fi.CTime {
				e.Unchanged, e.Cached = true, &c
				if e.FileID == "" {
					e.FileID = c.FileID
				}
			}
		}
		files = append(files, e)
		if !e.Unchanged {
			probe = append(probe, e)
		}
	}
	perr := make([]error, len(probe))
	if len(probe) > 0 {
		var cerr error
		if err := k.impersonate(func() {
			for i, e := range probe {
				if cerr = k.ctx.Err(); cerr != nil {
					return
				}
				var id string
				id, perr[i] = k.fs.Probe(e.Path)
				if perr[i] == nil && id != "" {
					e.FileID = id
				}
			}
		}); err != nil {
			return err
		}
		if cerr != nil {
			return cerr
		}
	}
	failed := map[*Entry]error{}
	for i, e := range probe {
		if perr[i] != nil {
			failed[e] = perr[i]
		}
	}
	for _, e := range files {
		if err, bad := failed[e]; bad {
			k.errIssue(e.Path, err, e.Attrs&AttrEncrypted != 0)
			continue
		}
		k.st.Files++
		k.st.Bytes += e.Size
		if e.Unchanged {
			k.st.Unchanged++
		}
		if err := k.emit(*e); err != nil {
			return err
		}
	}
	for _, d := range dirs {
		if err := k.walkDir(d.path, d.tp, d.rel, d.fi); err != nil {
			return err
		}
	}
	return nil
}

// excluded matches the job's exclude globs (sdk/automation syntax,
// case-insensitive). A pattern starting with "{" matches the token path;
// one containing "/" matches the path below the root; otherwise the name.
// A trailing "/" restricts a pattern to directories. Excluded directories
// are not descended into.
func (k *walk) excluded(rel, tp, name string, dir bool) bool {
	for _, p := range k.excl {
		if strings.HasSuffix(p, "/") {
			if !dir {
				continue
			}
			p = strings.TrimSuffix(p, "/")
		}
		target := name
		switch {
		case strings.HasPrefix(p, "{"):
			target = tp
		case strings.Contains(p, "/"):
			target = rel
		}
		if automation.MatchGlob(p, target, false) {
			return true
		}
	}
	return false
}
