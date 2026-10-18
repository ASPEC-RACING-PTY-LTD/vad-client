package backup

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/tokens"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/chunker"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/pack"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/vdcrypt"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// Progress phases (wire.RunProgress.Phase).
const (
	PhaseResume   = "resume"   // uploading packs spooled by an earlier run
	PhaseScan     = "scan"     // walking, chunking and packing
	PhaseTrees    = "trees"    // encoding tree blobs
	PhaseUpload   = "upload"   // draining the spool
	PhaseSnapshot = "snapshot" // writing the snapshot root
)

// Pipeline turns one job run into a snapshot: walk → chunk → dedupe →
// pack → spool → upload → index → trees → snapshot root (backup-format
// §7, §8, §11). It does not commit the snapshot to the server catalog or
// report runs and issues; the runner (P1-26) does that with the Result.
type Pipeline struct {
	Store  *Store
	Repo   *repo.Repo // opened with keystore.OpenRepo over the server backend
	RepoID int64      // server repo id (packs.repo_id; spool subdirectory)

	Tokens  tokens.Provider // nil = tokens.Default
	Walker  Walker          // FS, Guard, Log; Tokens and Cache are set per run
	Opener  Opener          // nil = OSOpener{}
	Folders FolderResolver  // used when Params.Spec.Tokens is nil; nil = OSFolders{}

	SpoolDir   string // e.g. <StateDir>\spool; packs go to <SpoolDir>\<RepoID>
	SpoolMax   int64  // 0 = DefaultSpoolMax
	IndexEvery int    // 0 = DefaultIndexEvery
	PackTarget int    // 0 = config pack_target (tests use small packs)

	// Progress receives heartbeats, at most every ProgressEvery (0 = 1s),
	// plus one at the end of each phase. It must not block.
	Progress      func(RunStats)
	ProgressEvery time.Duration
	// OnIssue receives each issue as it happens (they are also returned in
	// Result.Issues). It may be nil.
	OnIssue func(Issue)

	Log *slog.Logger
	Now func() time.Time
}

// Params describe one run.
type Params struct {
	JobID   int64
	RunUUID string   // client_run_uuid; the file cache seen_run marker
	Spec    WalkSpec // from SpecFor; Tokens nil = resolve with Folders; Epoch 0 = repo epoch
	// Root carries the descriptive snapshot fields: Parent, Job, Device,
	// Host, Profile, InstallMode ("" = simple) and Tags. The pipeline sets
	// Time, Tokens, Roots, Stats and IssuesSummary; SaveSnapshot sets Repo,
	// Epoch and Sig.
	Root snapshot.Root
	// DeviceKey is this device's VD1 Ed25519 key, which signs the root.
	DeviceKey ed25519.PrivateKey
}

// Result is what the runner commits (api-v1 §5.2) and reports.
type Result struct {
	SnapshotOID string         // "" when the run did not reach the snapshot
	Root        *snapshot.Root // the saved root (signed)
	Stats       RunStats
	Walk        WalkStats
	Issues      []Issue
	Packs       []string // packs uploaded and indexed during this run (including resumed ones)
	// ReferencedPacks lists, sorted, every pack the snapshot needs (api-v1
	// §5.2 pack_oids, including earlier runs' packs), collected from the
	// in-memory trees; nil when it could not be (CommitRefs then reads the
	// trees back).
	ReferencedPacks []string
	Resumed         int   // packs left by an earlier run that were uploaded first
	Pruned          int64 // file cache rows pruned (complete walks only)
}

// precompressedExt are file types stored raw (§7.2: known-compressed).
var precompressedExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".heic": true, ".avif": true,
	".mp3": true, ".aac": true, ".m4a": true, ".ogg": true, ".opus": true, ".flac": true,
	".mp4": true, ".m4v": true, ".mkv": true, ".mov": true, ".avi": true, ".webm": true, ".wmv": true,
	".zip": true, ".7z": true, ".rar": true, ".gz": true, ".tgz": true, ".bz2": true, ".xz": true, ".zst": true,
	".cab": true, ".msi": true, ".jar": true, ".apk": true,
	".docx": true, ".xlsx": true, ".pptx": true, ".odt": true, ".ods": true, ".odp": true, ".epub": true,
	".pdf": true,
}

func isPrecompressed(path string) bool { return precompressedExt[strings.ToLower(filepath.Ext(path))] }

type run struct {
	p      *Pipeline
	in     Params
	ctx    context.Context
	cancel context.CancelCauseFunc
	log    *slog.Logger
	keys   vdcrypt.RepoKeys
	ek     vdcrypt.EpochKeys
	gear   *chunker.Gear
	ch     *chunker.Chunker
	tok    tokens.Token
	opener Opener
	sp     *spool
	up     *uploader
	dedup  *dedupIndex
	cache  *cacheQueue
	tree   *treeBuilder
	target int

	dw      *pack.Writer
	dataSeq int64

	mu       sync.Mutex
	stats    RunStats
	issues   []Issue
	lastProg time.Time
	files    int64
	bytes    int64
}

func (p *Pipeline) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Run executes one backup run. On error the Result is still returned with
// what was done (stats, issues, uploaded packs); packs already spooled stay
// pending in backup.db and are uploaded first by the next run.
func (p *Pipeline) Run(ctx context.Context, in Params) (*Result, error) {
	res := &Result{}
	if p.Store == nil || p.Repo == nil || p.SpoolDir == "" {
		return res, errors.New("backup: pipeline needs Store, Repo and SpoolDir")
	}
	if in.JobID <= 0 || in.RunUUID == "" {
		return res, errors.New("backup: run needs a job id and a run uuid")
	}
	if len(in.DeviceKey) != ed25519.PrivateKeySize {
		return res, errors.New("backup: run needs the device signing key")
	}
	keys, err := p.Repo.Keys()
	if err != nil {
		return res, err
	}
	ek, err := p.Repo.EpochKeys(p.Repo.Epoch())
	if err != nil {
		return res, err
	}
	r := &run{p: p, in: in, keys: keys, ek: ek, log: p.Log, opener: p.Opener,
		dedup: newDedupIndex(p.Repo), cache: &cacheQueue{st: p.Store}, tree: newTreeBuilder(in.Spec.Paths)}
	if r.log == nil {
		r.log = slog.Default()
	}
	if r.opener == nil {
		r.opener = OSOpener{}
	}
	r.target = p.PackTarget
	if r.target <= 0 {
		r.target = int(p.Repo.Config().PackTarget)
	}
	if in.Spec.Epoch == 0 {
		r.in.Spec.Epoch = int64(ek.Epoch)
	}
	if r.in.Spec.Tokens == nil {
		fr := p.Folders
		if fr == nil {
			fr = OSFolders{}
		}
		if r.in.Spec.Tokens, err = fr.Tokens(ctx, r.in.Spec.Profile); err != nil {
			return res, fmt.Errorf("backup: resolve known folders: %w", err)
		}
	}
	r.gear = chunker.NewGear(keys.Chunker)
	r.ch = chunker.New(nil, r.gear)

	rctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	r.ctx, r.cancel = rctx, cancel
	err = r.execute(res)
	res.Stats = r.snapshotStats()
	r.mu.Lock()
	res.Issues = append([]Issue(nil), r.issues...)
	r.mu.Unlock()
	if r.up != nil {
		res.Packs = r.up.uploadedPacks()
	}
	if err != nil && ctx.Err() == nil {
		// The uploader cancels the run on failure: report its error rather
		// than the bare cancellation the walker returned.
		if cause := context.Cause(rctx); cause != nil && !errors.Is(cause, context.Canceled) && !errors.Is(err, cause) {
			err = cause
		}
	}
	return res, err
}

func (r *run) execute(res *Result) error {
	p := r.p
	var err error
	if r.sp, err = newSpool(filepath.Join(p.SpoolDir, strconv.FormatInt(p.RepoID, 10)), p.SpoolMax); err != nil {
		return err
	}
	ctx := r.ctx
	r.up = &uploader{r: p.Repo, st: p.Store, sp: r.sp, every: p.IndexEvery, log: r.log, cancel: r.cancel,
		onIndexed: r.cache.advance, onProgress: func() { r.progress(false) }}
	r.up.start(ctx)
	upDone := false
	defer func() {
		if !upDone {
			r.up.finish()
		}
	}()

	// Resume: packs an earlier run spooled but did not get indexed.
	pending, err := p.Store.PendingPacks(ctx, p.RepoID)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, pk := range pending {
		keep[pk.OID] = true
	}
	if err := r.sp.sweep(keep); err != nil {
		return fmt.Errorf("backup: spool: %w", err)
	}
	r.setPhase(wire.RunStatusUploading, PhaseResume)
	for _, pk := range pending {
		data, err := r.sp.read(pk.OID)
		if err != nil {
			// The bytes are gone (spool dir cleared): the pack can never be
			// uploaded. Retire the row; its blobs are re-packed if needed.
			r.log.Warn("backup: spooled pack missing, dropped", "pack", pk.OID)
			if err := p.Store.DeletePack(ctx, pk.OID); err != nil {
				return err
			}
			continue
		}
		s := spooled{oid: pk.OID, class: pack.Class(pk.Class), size: int64(len(data))}
		pr, perr := pack.NewReader(bytes.NewReader(data), int64(len(data)), p.Repo.Keyring(), s.class)
		if perr != nil || vdcrypt.OID(data) != pk.OID {
			r.log.Warn("backup: spooled pack corrupt, dropped", "pack", pk.OID, "err", perr)
			r.sp.remove(pk.OID, 0)
			if err := p.Store.DeletePack(ctx, pk.OID); err != nil {
				return err
			}
			continue
		}
		// Its blobs count as queued, so this run does not pack them again.
		s.entries = pr.Entries
		if s.class == pack.ClassData {
			r.dataSeq++
			s.seq = r.dataSeq
		}
		for _, e := range pr.Entries {
			r.dedup.add(e.ID, s.seq)
		}
		r.sp.account(s.size)
		if err := r.up.enqueue(ctx, s); err != nil {
			return err
		}
		res.Resumed++
	}

	// Walk, chunk and pack.
	tok, terr := r.tokens().ForProfile(r.in.Spec.ProfileSID)
	if terr == nil {
		r.tok = tok
		defer tok.Close()
	}
	w := p.Walker
	w.Tokens = sharedProvider{tok: tok, err: terr}
	w.Cache = StoreCache(p.Store, r.in.JobID)
	w.EmitDirs = true
	if w.Log == nil {
		w.Log = r.log
	}
	r.setPhase(wire.RunStatusScanning, PhaseScan)
	ws, err := w.Walk(ctx, r.in.Spec, r.file, r.issue)
	res.Walk = ws
	if err != nil {
		return err
	}
	if r.dw != nil && r.dw.Len() > 0 {
		if err := r.seal(r.dw, r.dataSeq); err != nil {
			return err
		}
	}
	r.dw = nil

	// Trees, in meta packs.
	r.setPhase(wire.RunStatusScanning, PhaseTrees)
	refs, blobs, err := r.tree.encode(r.keys.ID)
	if err != nil {
		return err
	}
	if err := r.addMeta(blobs); err != nil {
		return err
	}

	// Drain the spool; the final flush writes the last index.
	r.setPhase(wire.RunStatusUploading, PhaseUpload)
	upDone = true
	if err := r.up.finish(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.cache.Err(); err != nil {
		return fmt.Errorf("backup: file cache: %w", err)
	}

	// Snapshot root (FlushIndex inside SaveSnapshot is a no-op by now).
	r.setPhase(wire.RunStatusCommitting, PhaseSnapshot)
	root := r.in.Root
	root.V = snapshot.Version
	root.Time = p.now().UnixMilli()
	root.Tokens = usedTokens(r.in.Spec.Tokens, refs)
	root.Roots = refs
	if root.InstallMode == "" {
		root.InstallMode = snapshot.InstallModeSimple
	}
	r.mu.Lock()
	root.Stats = snapshot.Stats{Files: r.files, Dirs: r.tree.dirs, Bytes: r.bytes}
	root.IssuesSummary = summarise(r.issues)
	r.mu.Unlock()
	oid, err := p.Repo.SaveSnapshot(ctx, &root, r.in.DeviceKey)
	if err != nil {
		return err
	}
	res.SnapshotOID, res.Root = oid, &root
	res.ReferencedPacks = r.tree.referencedPacks(p.Repo.Index(), blobs, root.Inventory)

	if ws.Complete {
		n, err := p.Store.PruneFileCache(context.WithoutCancel(ctx), r.in.JobID, r.in.RunUUID)
		if err != nil {
			return err
		}
		res.Pruned = n
	}
	r.progress(true)
	return nil
}

func (r *run) tokens() tokens.Provider {
	if r.p.Tokens != nil {
		return r.p.Tokens
	}
	return tokens.Default
}

func summarise(issues []Issue) snapshot.IssuesSummary {
	s := snapshot.IssuesSummary{ByClass: map[string]int64{}}
	for _, is := range issues {
		if is.Severity != wire.IssueSeveritySkipped {
			continue
		}
		s.Skipped++
		s.ByClass[WireClass(is.Class)]++
	}
	return s
}

// --- progress and issues ---

func (r *run) setPhase(status, phase string) {
	r.mu.Lock()
	r.stats.Status, r.stats.Phase = status, phase
	r.mu.Unlock()
	r.progress(true)
}

func (r *run) snapshotStats() RunStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.stats
	if r.up != nil {
		st.BytesUploaded = r.up.bytesUploaded.Load()
		st.QueuePacks = r.up.queued.Load()
	}
	return st
}

func (r *run) progress(force bool) {
	if r.p.Progress == nil {
		return
	}
	every := r.p.ProgressEvery
	if every <= 0 {
		every = time.Second
	}
	now := r.p.now()
	r.mu.Lock()
	if !force && now.Sub(r.lastProg) < every {
		r.mu.Unlock()
		return
	}
	r.lastProg = now
	r.mu.Unlock()
	r.p.Progress(r.snapshotStats())
}

func (r *run) issue(is Issue) {
	r.mu.Lock()
	r.issues = append(r.issues, is)
	if is.Severity == wire.IssueSeverityError {
		r.stats.Errors++
	} else {
		r.stats.Warnings++
	}
	r.mu.Unlock()
	if r.p.OnIssue != nil {
		r.p.OnIssue(is)
	}
}

// --- files ---

// file backs up one walker entry: a directory is recorded with its
// metadata; an unchanged file reuses its cached chunk list; a changed one
// is read, chunked and deduplicated.
func (r *run) file(e Entry) error {
	if e.IsDir() {
		return r.dir(e)
	}
	r.mu.Lock()
	r.stats.FilesTotal++
	r.stats.BytesTotal += e.Size
	r.mu.Unlock()

	var entry snapshot.Entry
	var ok bool
	var err error
	if e.Unchanged && e.Cached != nil {
		entry, ok, err = r.reuse(e)
		if err != nil {
			return err
		}
	}
	if !ok {
		if entry, ok, err = r.read(e); err != nil || !ok {
			return err
		}
	}
	if err := r.tree.add(e.TokenPath, entry); err != nil {
		if errors.Is(err, errBadName) {
			r.issue(Issue{Path: e.Path, Class: wire.IssueClassOther, Severity: wire.IssueSeveritySkipped, Detail: err.Error()})
			return nil
		}
		return err
	}
	r.mu.Lock()
	r.files++
	r.bytes += entry.S
	r.stats.FilesDone++
	r.stats.BytesDone += entry.S
	r.mu.Unlock()
	r.progress(false)
	return nil
}

// dir records a walked directory, so empty directories (and roots without
// files) are kept and directory entries carry their own m, c and a.
func (r *run) dir(e Entry) error {
	err := r.tree.addDir(e.TokenPath, snapshot.Entry{M: e.MTime, C: e.CTime, A: e.Attrs})
	if errors.Is(err, errBadName) {
		r.issue(Issue{Path: e.Path, Class: wire.IssueClassOther, Severity: wire.IssueSeveritySkipped, Detail: err.Error()})
		return nil
	}
	return err
}

func fileEntry(e Entry, size int64, h []byte, ids []string) snapshot.Entry {
	return snapshot.Entry{T: snapshot.TypeFile, S: size, M: e.MTime, C: e.CTime, A: e.Attrs,
		H: snapshot.HexPtr(hex.EncodeToString(h)), Ch: ids}
}

// reuse builds the entry from the file cache when every cached chunk is
// still stored; ok is false when the file must be re-read.
func (r *run) reuse(e Entry) (snapshot.Entry, bool, error) {
	c := e.Cached
	ids := c.ChunkIDs()
	if len(c.Chunks)%32 != 0 || len(c.H) != 32 {
		return snapshot.Entry{}, false, nil
	}
	if _, ok := r.dedup.hasAll(ids); !ok {
		return snapshot.Entry{}, false, nil
	}
	hx := make([]string, len(ids))
	for i, id := range ids {
		hx[i] = hex.EncodeToString(id[:])
	}
	if err := r.p.Store.MarkFileSeen(r.ctx, r.in.JobID, e.Path, r.in.RunUUID); err != nil {
		return snapshot.Entry{}, false, fmt.Errorf("backup: file cache: %w", err)
	}
	return fileEntry(e, c.Size, c.H, hx), true, nil
}

// read chunks a changed file. ok is false when the file was skipped (an
// issue is reported); err is fatal for the run.
func (r *run) read(e Entry) (snapshot.Entry, bool, error) {
	if r.tok == nil {
		return snapshot.Entry{}, false, errors.New("backup: no profile token")
	}
	rc, err := r.opener.Open(r.tok, e.Path)
	if err != nil {
		if r.skip(e.Path, err) {
			return snapshot.Entry{}, false, nil
		}
		return snapshot.Entry{}, false, err
	}
	defer rc.Close()
	pre := isPrecompressed(e.Path)
	fh := snapshot.NewFileHash(r.keys.FileHash)
	r.ch.Reset(rc)
	var size, need int64
	var ids []string
	var raw []byte
	for {
		if err := r.ctx.Err(); err != nil {
			return snapshot.Entry{}, false, err
		}
		c, err := r.ch.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if r.skip(e.Path, err) {
				return snapshot.Entry{}, false, nil
			}
			return snapshot.Entry{}, false, err
		}
		fh.Write(c.Data)
		id := pack.ComputeID(r.keys.ID, c.Data)
		seq, err := r.addData(id, c.Data, pre)
		if err != nil {
			return snapshot.Entry{}, false, err
		}
		need = max(need, seq)
		ids = append(ids, id.String())
		raw = append(raw, id[:]...)
		size += int64(c.Length)
	}
	h := fh.Sum(nil)
	if size == e.Size {
		// A file that changed while being read is not cached, so the next
		// run reads it again.
		if err := r.cache.add(r.ctx, need, CachedFile{JobID: r.in.JobID, Path: e.Path, Size: e.Size, MTime: e.MTime,
			CTime: e.CTime, Attrs: e.Attrs, FileID: e.FileID, H: h, Chunks: raw, Epoch: r.in.Spec.Epoch,
			SeenRun: r.in.RunUUID}); err != nil {
			return snapshot.Entry{}, false, fmt.Errorf("backup: file cache: %w", err)
		}
	}
	if ids == nil {
		ids = []string{}
	}
	return fileEntry(e, size, h, ids), true, nil
}

// skip reports a per-file read failure; it returns false for errors that
// end the run (cancellation).
func (r *run) skip(path string, err error) bool {
	if r.ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	is := Issue{Path: path, Severity: wire.IssueSeveritySkipped, Detail: err.Error()}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return true // deleted since the walk
	case errors.Is(err, ErrLocked):
		is.Class, is.Retryable = wire.IssueClassLocked, true
	case errors.Is(err, ErrEFS):
		is.Class, is.Retryable = IssueClassEFSNeedsLogon, true
	case errors.Is(err, fs.ErrPermission):
		is.Class = wire.IssueClassAccessDenied
	case errors.Is(err, ErrPathTooLong):
		is.Class = wire.IssueClassPathTooLong
	default:
		is.Class, is.Retryable = wire.IssueClassIO, true
	}
	r.issue(is)
	return true
}

// --- packing ---

func (r *run) newWriter(c pack.Class) (*pack.Writer, error) {
	cfg := r.p.Repo.Config()
	return pack.NewWriter(r.p.Repo.ID(), r.keys.ID, r.ek, c, pack.WriterOptions{Target: r.target, Max: int(cfg.PackMax)})
}

// addData stores one data chunk unless it is already stored or queued and
// returns the data-pack seq it waits for.
func (r *run) addData(id pack.BlobID, data []byte, pre bool) (int64, error) {
	if seq, ok := r.dedup.lookup(id); ok {
		return seq, nil
	}
	for attempt := 0; ; attempt++ {
		if r.dw == nil {
			w, err := r.newWriter(pack.ClassData)
			if err != nil {
				return 0, err
			}
			r.dw = w
			r.dataSeq++
		}
		_, err := r.dw.Add(pack.TypeData, data, pre)
		if errors.Is(err, pack.ErrPackFull) && attempt == 0 {
			if err := r.seal(r.dw, r.dataSeq); err != nil {
				return 0, err
			}
			r.dw = nil
			continue
		}
		if err != nil {
			return 0, err
		}
		break
	}
	seq := r.dataSeq
	r.dedup.add(id, seq)
	r.mu.Lock()
	r.stats.BytesNew += int64(len(data))
	r.mu.Unlock()
	if r.dw.Full() {
		if err := r.seal(r.dw, seq); err != nil {
			return 0, err
		}
		r.dw = nil
	}
	return seq, nil
}

// addMeta packs tree blobs not stored yet.
func (r *run) addMeta(blobs []snapshot.TreeBlob) error {
	var mw *pack.Writer
	for _, b := range blobs {
		id := pack.BlobID(b.ID)
		if _, ok := r.dedup.lookup(id); ok {
			continue
		}
		for attempt := 0; ; attempt++ {
			if mw == nil {
				w, err := r.newWriter(pack.ClassMeta)
				if err != nil {
					return err
				}
				mw = w
			}
			_, err := mw.Add(pack.TypeTree, b.Raw, false)
			if errors.Is(err, pack.ErrPackFull) && attempt == 0 {
				if err := r.seal(mw, 0); err != nil {
					return err
				}
				mw = nil
				continue
			}
			if err != nil {
				return err
			}
			break
		}
		r.dedup.add(id, 0)
		if mw.Full() {
			if err := r.seal(mw, 0); err != nil {
				return err
			}
			mw = nil
		}
	}
	if mw != nil && mw.Len() > 0 {
		return r.seal(mw, 0)
	}
	return nil
}

// seal finishes a pack, spools it, records it in backup.db and queues the
// upload.
func (r *run) seal(w *pack.Writer, seq int64) error {
	pk, err := w.Finish()
	if err != nil {
		return err
	}
	if err := r.sp.write(r.ctx, pk.OID, pk.Bytes); err != nil {
		return err
	}
	size := int64(len(pk.Bytes))
	if err := r.p.Store.AddPack(r.ctx, Pack{OID: pk.OID, RepoID: r.p.RepoID, Epoch: int64(pk.Epoch), Class: int(pk.Class),
		Size: size, RunUUID: r.in.RunUUID}); err != nil {
		r.sp.remove(pk.OID, size)
		return err
	}
	return r.up.enqueue(r.ctx, spooled{oid: pk.OID, class: pk.Class, size: size, seq: seq, entries: pk.Entries})
}
