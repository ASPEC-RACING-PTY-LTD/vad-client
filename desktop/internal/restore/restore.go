// Package restore is the Windows client's restore engine (MASTER_PLAN §7.9,
// D16, api-v1 §7): it writes the selected files of a snapshot to their
// targets as the target user, through system-file protection, and reports
// one item per file.
//
// Rules:
//   - every target is checked by guard (OpRestoreWrite) first; a refusal is
//     an item with status blocked and is never retried or overridden;
//   - files are created under the target user's token (owner and ACLs come
//     from the folder; ACLs and SIDs are never restored), into a temporary
//     name with O_EXCL, filled and verified (keyed whole-file hash) through
//     the open handle, then renamed into place;
//   - nothing is ever deleted: a file replaced by "overwrite" is kept
//     beside the target as "<name> (replaced …)" (P2-D2; the shell's
//     Recycle Bin call can silently delete permanently, so it is not used);
//   - links and reparse points in the snapshot are not recreated.
package restore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/tokens"
	sdkrestore "github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/restore"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/vdcrypt"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// Source is an opened repository (*repo.Repo implements it).
type Source interface {
	sdkrestore.TreeReader
	sdkrestore.BlobReader
	Keys() (vdcrypt.RepoKeys, error)
}

// Request is one restore.
type Request struct {
	Roots     []snapshot.RootRef // from the snapshot root
	Selection []wire.RestoreSelection
	Overrides []wire.RestorePathMapping
	// Tokens are the target profile's token values ({PROFILE}, {DOCUMENTS},
	// …); overrides win over them.
	Tokens snapshot.Tokens
	// Policy is a conflict policy (wire.RestoreConflict*); "" means rename.
	Policy string
	// Profile is the target profile for guard (SID required).
	Profile guard.Profile
}

// Options are the engine's collaborators.
type Options struct {
	// Guard checks every target (default guard.Check).
	Guard func(op guard.Op, path string, p guard.Profile) error
	// Token is the target user's token; nil writes with the process's own
	// identity (the per-user app, the standalone tool, tests).
	Token tokens.Token
	// Recycle, when set, may move a replaced file away (tests); it must
	// never delete. nil or an error keeps the file beside the target.
	Recycle func(path string) error
	// Now is the clock (default time.Now).
	Now func() time.Time
	// OnItem receives every finished item (done, skipped, failed, blocked).
	OnItem func(wire.RestoreItem)
	// OnProgress receives counters at most every ProgressEvery.
	OnProgress    func(Progress)
	ProgressEvery time.Duration
}

// Progress counts a running restore.
type Progress struct {
	FilesTotal, FilesDone int64
	BytesTotal, BytesDone int64
	Skipped, Failed       int64
	Blocked               int64
}

// Status returns the terminal wire status for a finished restore.
func (p Progress) Status() string {
	switch {
	case p.Failed == 0 && p.Blocked == 0:
		return wire.RestoreStatusCompleted
	case p.FilesDone == 0 && p.Skipped == 0:
		return wire.RestoreStatusFailed
	}
	return wire.RestoreStatusPartial
}

// ErrNoProfile is returned when the request names no target profile.
var ErrNoProfile = errors.New("restore: no target profile")

type runner struct {
	src    Source
	req    Request
	o      Options
	mapper *sdkrestore.Mapper
	kfh    vdcrypt.Key
	p      Progress
	last   time.Time
}

// Run restores req from src. It returns the counters; an error means the
// restore could not continue (cancelled, repository unreadable); per-file
// problems are items, not errors.
func Run(ctx context.Context, src Source, req Request, o Options) (Progress, error) {
	if req.Profile.SID == "" {
		return Progress{}, ErrNoProfile
	}
	if o.Guard == nil {
		o.Guard = guard.Check
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ProgressEvery <= 0 {
		o.ProgressEvery = time.Second
	}
	if req.Policy == "" {
		req.Policy = wire.RestoreConflictRename
	}
	if !sdkrestore.ValidPolicy(req.Policy) {
		return Progress{}, fmt.Errorf("restore: unknown conflict policy %q", req.Policy)
	}
	sel, err := sdkrestore.NewSelector(req.Selection)
	if err != nil {
		return Progress{}, err
	}
	mapper, err := sdkrestore.NewMapper(req.Tokens, req.Overrides)
	if err != nil {
		return Progress{}, err
	}
	keys, err := src.Keys()
	if err != nil {
		return Progress{}, err
	}
	r := &runner{src: src, req: req, o: o, mapper: mapper, kfh: keys.FileHash}
	sum, err := sdkrestore.Summarize(ctx, src, req.Roots, sel)
	if err != nil {
		return Progress{}, err
	}
	r.p.FilesTotal, r.p.BytesTotal = sum.Files, sum.Bytes
	r.progress(true)
	err = sdkrestore.Walk(ctx, src, req.Roots, sel, func(it sdkrestore.Item) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.item(ctx, it)
		r.progress(false)
		return nil
	})
	r.progress(true)
	return r.p, err
}

func (r *runner) progress(force bool) {
	if r.o.OnProgress == nil {
		return
	}
	now := r.o.Now()
	if !force && now.Sub(r.last) < r.o.ProgressEvery {
		return
	}
	r.last = now
	r.o.OnProgress(r.p)
}

func (r *runner) emit(it wire.RestoreItem) {
	switch it.Status {
	case wire.RestoreItemSkipped:
		r.p.Skipped++
	case wire.RestoreItemFailed:
		r.p.Failed++
	case wire.RestoreItemBlocked:
		r.p.Blocked++
	}
	if r.o.OnItem != nil {
		r.o.OnItem(it)
	}
}

// as runs fn as the target user.
func (r *runner) as(fn func() error) error {
	if r.o.Token == nil {
		return fn()
	}
	return r.o.Token.Impersonate(fn)
}

// guardCheck returns a blocked item for a refused target, or nil.
func (r *runner) guardCheck(it sdkrestore.Item, target string) *wire.RestoreItem {
	err := r.o.Guard(guard.OpRestoreWrite, target, r.req.Profile)
	if err == nil {
		return nil
	}
	msg := "protected location"
	var be *guard.BlockedError
	if errors.As(err, &be) && be.Reason != "" {
		msg = be.Reason
	}
	return &wire.RestoreItem{Path: it.Path, TargetPath: target, Action: wire.RestoreActionSkip, Status: wire.RestoreItemBlocked,
		Message: "blocked by system-file protection: " + msg, Size: it.Entry.S}
}

func (r *runner) item(ctx context.Context, it sdkrestore.Item) {
	target, err := r.mapper.Target(it.Path)
	if err != nil {
		if it.Entry.T == snapshot.TypeFile {
			r.emit(wire.RestoreItem{Path: it.Path, Action: wire.RestoreActionSkip, Status: wire.RestoreItemFailed, Message: "no target folder for this path", Size: it.Entry.S})
		}
		return
	}
	switch it.Entry.T {
	case snapshot.TypeDir:
		if b := r.guardCheck(it, target); b != nil {
			r.emit(*b)
			return
		}
		if err := r.as(func() error { return os.MkdirAll(target, 0o700) }); err != nil {
			r.emit(wire.RestoreItem{Path: it.Path, TargetPath: target, Action: wire.RestoreActionRestore, Status: wire.RestoreItemFailed, Message: "cannot create the folder: " + errText(err)})
		}
		return
	case snapshot.TypeFile:
	default:
		r.emit(wire.RestoreItem{Path: it.Path, TargetPath: target, Action: wire.RestoreActionSkip, Status: wire.RestoreItemSkipped, Message: "links and reparse points are not restored"})
		return
	}
	if b := r.guardCheck(it, target); b != nil {
		r.emit(*b)
		return
	}
	out := r.file(ctx, it, target)
	if out.Status == wire.RestoreItemDone {
		r.p.FilesDone++
		r.p.BytesDone += it.Entry.S
	}
	r.emit(out)
}

// file restores one file to target.
func (r *runner) file(ctx context.Context, it sdkrestore.Item, target string) wire.RestoreItem {
	e := it.Entry
	res := wire.RestoreItem{Path: it.Path, TargetPath: target, Size: e.S}
	fail := func(msg string, err error) wire.RestoreItem {
		res.Status, res.Message = wire.RestoreItemFailed, msg
		if err != nil {
			res.Message += ": " + errText(err)
		}
		return res
	}

	// Decide and open the temporary file as the user.
	var tmp *os.File
	var tmpPath string
	err := r.as(func() error {
		dir := filepath.Dir(target)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		ex := existing(target)
		res.Action = sdkrestore.Decide(r.req.Policy, e, ex)
		switch res.Action {
		case wire.RestoreActionSkip:
			return nil
		case wire.RestoreActionRename:
			p, err := sdkrestore.RenamedPath(target, func(p string) bool { return existing(p).Exists })
			if err != nil {
				return err
			}
			res.TargetPath = p
		}
		var err error
		tmp, tmpPath, err = createTemp(dir, filepath.Base(target))
		return err
	})
	if err != nil {
		return fail("cannot write here", err)
	}
	if res.Action == wire.RestoreActionSkip {
		res.Status = wire.RestoreItemSkipped
		if existing := res.Message; existing == "" {
			res.Message = "a file is already there (" + r.req.Policy + ")"
		}
		return res
	}

	// Fill it through the handle (no impersonation needed) and verify.
	_, cerr := sdkrestore.CopyFile(ctx, r.src, &r.kfh, e, tmp)
	if cerr == nil {
		cerr = tmp.Sync()
	}
	if err := tmp.Close(); cerr == nil {
		cerr = err
	}
	if cerr != nil {
		_ = r.as(func() error { return os.Remove(tmpPath) })
		if ctx.Err() != nil {
			return fail("cancelled", nil)
		}
		return fail("cannot restore the content", cerr)
	}

	// Move it into place as the user.
	var note string
	err = r.as(func() error {
		dst := res.TargetPath
		if res.Action == wire.RestoreActionOverwrite {
			if ex := existing(dst); ex.Exists && !ex.IsDir {
				if r.o.Recycle == nil || r.o.Recycle(dst) != nil {
					keep := sdkrestore.ReplacedName(dst, r.o.Now())
					if err := os.Rename(dst, keep); err != nil {
						return fmt.Errorf("cannot move the existing file aside: %w", err)
					}
					note = "previous version kept as " + filepath.Base(keep)
				} else {
					note = "previous version moved to the Recycle Bin"
				}
			}
		}
		if existing(dst).Exists {
			return fmt.Errorf("%s appeared while restoring", filepath.Base(dst))
		}
		if err := os.Rename(tmpPath, dst); err != nil {
			return err
		}
		mt := time.Unix(0, e.M)
		_ = os.Chtimes(dst, mt, mt)
		return nil
	})
	if err != nil {
		_ = r.as(func() error { return os.Remove(tmpPath) })
		return fail("cannot put the file in place", err)
	}
	res.Status, res.Message = wire.RestoreItemDone, note
	return res
}

// existing describes what is at p without following a final link.
func existing(p string) sdkrestore.Existing {
	fi, err := os.Lstat(p)
	if err != nil {
		return sdkrestore.Existing{}
	}
	return sdkrestore.Existing{Exists: true, IsDir: fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0, Size: fi.Size(), MTime: fi.ModTime().UnixNano()}
}

// createTemp creates a new hidden temporary file beside the target with
// O_EXCL, so an existing file or link at that name is never followed.
func createTemp(dir, base string) (*os.File, string, error) {
	for i := 0; i < 10; i++ {
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		p := filepath.Join(dir, "."+trimName(base)+".vdrestore-"+hex.EncodeToString(b)+".tmp")
		f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return f, p, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("cannot create a temporary file")
}

func trimName(n string) string {
	if len(n) > 60 {
		return n[:60]
	}
	return n
}

// errText shortens an error for an item message.
func errText(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	s := err.Error()
	if len(s) > 300 {
		s = s[:300]
	}
	return strings.TrimSpace(s)
}
