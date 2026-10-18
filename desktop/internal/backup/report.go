package backup

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/pack"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/vdcrypt"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// --- progress heartbeats ---

// reporter forwards pipeline progress: it keeps the latest stats (the
// pipeline callback never blocks), stores and sends them at most every
// HeartbeatEvery, and uploads pending issues every IssueFlushEvery.
type reporter struct {
	r  *Runner
	lr *Run

	mu    sync.Mutex
	cur   RunStats
	dirty bool

	quit chan struct{}
	done chan struct{}
}

func newReporter(r *Runner, lr *Run) *reporter {
	return &reporter{r: r, lr: lr, cur: lr.Stats, quit: make(chan struct{}), done: make(chan struct{})}
}

func (p *reporter) progress(st RunStats) {
	p.mu.Lock()
	p.cur, p.dirty = st, true
	p.mu.Unlock()
}

func (p *reporter) last() RunStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cur
}

func (p *reporter) start(ctx context.Context) {
	hb := p.r.HeartbeatEvery
	if hb <= 0 {
		hb = DefaultHeartbeatEvery
	}
	fl := p.r.IssueFlushEvery
	if fl <= 0 {
		fl = DefaultIssueFlushEvery
	}
	go func() {
		defer close(p.done)
		beat := time.NewTicker(hb)
		defer beat.Stop()
		flush := time.NewTicker(fl)
		defer flush.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-p.quit:
				return
			case <-beat.C:
				p.beat(ctx)
			case <-flush.C:
				if err := p.r.flushIssues(ctx, p.lr.ClientRunUUID, p.lr.RunID); err != nil && ctx.Err() == nil {
					p.r.log().Warn("backup: issue upload failed; will retry", "run", p.lr.ClientRunUUID, "err", err)
				}
			}
		}
	}()
}

func (p *reporter) stop() {
	close(p.quit)
	<-p.done
}

// beat stores and sends the latest stats when they changed.
func (p *reporter) beat(ctx context.Context) {
	p.mu.Lock()
	st, dirty := p.cur, p.dirty
	p.dirty = false
	p.mu.Unlock()
	if !dirty {
		return
	}
	if err := p.r.Store.UpdateRunStats(ctx, p.lr.ClientRunUUID, st.Status, st); err != nil && ctx.Err() == nil {
		p.r.log().Warn("backup: save run progress", "run", p.lr.ClientRunUUID, "err", err)
	}
	if p.lr.RunID == 0 || st.Status == "" {
		return
	}
	if err := p.r.API.RunHeartbeat(ctx, p.lr.RunID, st); err != nil && ctx.Err() == nil {
		p.r.log().Debug("backup: heartbeat failed", "run", p.lr.RunID, "err", err)
	}
}

// --- snapshot commit ---

// commitRequest builds the api-v1 §5.2 commit body.
func commitRequest(j *Job, lr *Run, prof ProfileInfo, dev Identity, res *Result, idx, packs []string, parent *int64, took time.Duration) wire.SnapshotCommitRequest {
	root := res.Root
	tags := root.Tags
	if tags == nil {
		tags = []string{}
	}
	return wire.SnapshotCommitRequest{
		SnapshotOID: res.SnapshotOID, JobID: j.ID, RunID: lr.RunID, ParentSnapshotID: parent,
		Kind: j.Kind, Mode: j.Mode, Hostname: dev.Hostname, OSUser: prof.Username, UserSID: prof.SID,
		ProfilePath: prof.ProfilePath, ClientTime: root.Time, Tags: tags,
		Stats: wire.SnapshotStats{FileCount: root.Stats.Files, DirCount: root.Stats.Dirs, LogicalBytes: root.Stats.Bytes,
			NewBytes: res.Stats.BytesNew, NewObjects: int64(len(res.Packs)), DurationMS: took.Milliseconds()},
		IndexOIDs: idx, PackOIDs: packs, Epoch: int64(root.Epoch), ProfileConsentID: prof.ConsentID,
	}
}

// treeSource is what snapshotPacks reads: the index and tree blobs.
type treeSource interface {
	Lookup(id pack.BlobID) (repo.Location, bool)
	ReadTree(ctx context.Context, idHex string) (*snapshot.Tree, error)
}

type repoTrees struct{ r *repo.Repo }

func (t repoTrees) Lookup(id pack.BlobID) (repo.Location, bool) { return t.r.Index().Lookup(id) }
func (t repoTrees) ReadTree(ctx context.Context, idHex string) (*snapshot.Tree, error) {
	return t.r.ReadTree(ctx, idHex)
}

func blobID(idHex string) (pack.BlobID, error) {
	var id pack.BlobID
	b, err := hex.DecodeString(idHex)
	if err != nil || len(b) != pack.IDSize {
		return id, fmt.Errorf("backup: bad blob id %q", idHex)
	}
	copy(id[:], b)
	return id, nil
}

// snapshotPacks walks the snapshot's trees and returns, sorted, every
// pack holding one of its tree, data or meta blobs: rev 1.0 §5.2 requires
// pack_oids to list all of them, including packs uploaded by earlier runs,
// because the server's GC reachability relies on it. Shared subtrees are
// read once.
func snapshotPacks(ctx context.Context, src treeSource, root *snapshot.Root) ([]string, error) {
	packs := map[string]bool{}
	add := func(idHex string) error {
		id, err := blobID(idHex)
		if err != nil {
			return err
		}
		loc, ok := src.Lookup(id)
		if !ok {
			return fmt.Errorf("%w: %s", repo.ErrBlobNotFound, idHex)
		}
		packs[loc.Pack] = true
		return nil
	}
	seen := map[string]bool{}
	var queue []string
	push := func(idHex string) {
		if !seen[idHex] {
			seen[idHex] = true
			queue = append(queue, idHex)
		}
	}
	for _, rr := range root.Roots {
		push(rr.Tree)
	}
	if root.Inventory != nil {
		if err := add(*root.Inventory); err != nil {
			return nil, err
		}
	}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if err := add(id); err != nil {
			return nil, err
		}
		t, err := src.ReadTree(ctx, id)
		if err != nil {
			return nil, err
		}
		if t.Cont != nil {
			push(*t.Cont)
		}
		for _, e := range t.Entries {
			for _, c := range e.Ch {
				if err := add(c); err != nil {
					return nil, err
				}
			}
			if e.X != nil {
				for _, a := range e.X.ADS {
					for _, c := range a.Ch {
						if err := add(c); err != nil {
							return nil, err
						}
					}
				}
			}
			if e.Sub != nil {
				push(*e.Sub)
			}
		}
	}
	out := make([]string, 0, len(packs))
	for p := range packs {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// --- the real engine ---

// RepoEngine runs the pipeline on one opened repository.
type RepoEngine struct {
	Pipeline *Pipeline // Repo and RepoID set

	last *Result // the latest Run's result (its ReferencedPacks)
}

// RepoEngines returns an EngineFactory that opens the job's repository
// with open (keystore.OpenRepo over server.New(client, repoID, …)) and runs
// a copy of base (Store, SpoolDir, Walker, …) on it. The repository is
// opened per run so a key rotation between runs is always seen.
func RepoEngines(base *Pipeline, open func(ctx context.Context, repoID int64) (*repo.Repo, error)) EngineFactory {
	return func(ctx context.Context, j *Job) (Engine, error) {
		if j.RepoID <= 0 {
			return nil, fmt.Errorf("backup: job %d has no repository", j.ID)
		}
		rp, err := open(ctx, j.RepoID)
		if err != nil {
			return nil, err
		}
		p := *base
		p.Repo, p.RepoID = rp, j.RepoID
		return &RepoEngine{Pipeline: &p}, nil
	}
}

// Run runs the pipeline with the runner's hooks.
func (e *RepoEngine) Run(ctx context.Context, in Params, h Hooks) (*Result, error) {
	p := *e.Pipeline
	p.Progress, p.OnIssue = h.Progress, h.OnIssue
	res, err := p.Run(ctx, in)
	e.last = res
	return res, err
}

// CommitRefs returns every loaded index object and the packs the snapshot
// needs: the ones the latest Run collected for this root, else read back
// from the trees.
func (e *RepoEngine) CommitRefs(ctx context.Context, root *snapshot.Root) ([]string, []string, error) {
	rp := e.Pipeline.Repo
	var packs []string
	if e.last != nil && e.last.Root == root && e.last.ReferencedPacks != nil {
		packs = slices.Clone(e.last.ReferencedPacks)
	} else {
		var err error
		if packs, err = snapshotPacks(ctx, repoTrees{rp}, root); err != nil {
			return nil, nil, err
		}
	}
	return rp.Index().Files(), packs, nil
}

// KeyID returns the key_id of this device's newest key file whose sign_pub
// is the device key, so readers verify the root with that key.
func (e *RepoEngine) KeyID(deviceID int64, signPub ed25519.PublicKey) string {
	return deviceKeyID(e.Pipeline.Repo.KeyFiles(), deviceID, signPub)
}

func deviceKeyID(files map[string]*vdcrypt.KeyFile, deviceID int64, signPub ed25519.PublicKey) string {
	var best *vdcrypt.KeyFile
	for _, kf := range files {
		if kf.Kind != vdcrypt.KindDevice || kf.Device == nil || kf.Device.ID != deviceID || !bytes.Equal(kf.Device.SignPub, signPub) {
			continue
		}
		if best == nil || kf.Epoch > best.Epoch || (kf.Epoch == best.Epoch && kf.KeyID < best.KeyID) {
			best = kf
		}
	}
	if best == nil {
		return ""
	}
	return best.KeyID
}

// Close closes the repository (wipes its keys).
func (e *RepoEngine) Close() { e.Pipeline.Repo.Close() }
