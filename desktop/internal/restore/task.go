package restore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/tokens"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// Server is the server side of a device restore (api-v1 rev 1.1c);
// *client.Client implements it.
type Server interface {
	DeviceRestore(ctx context.Context, id int64) (*wire.DeviceRestore, error)
	ReportRestore(ctx context.Context, id int64, p wire.RestoreProgress) (*wire.RestoreProgressReply, error)
}

// Repo is an opened repository that can load and verify snapshots
// (*repo.Repo implements it).
type Repo interface {
	Source
	LoadSnapshot(ctx context.Context, oid string) (*snapshot.Root, []byte, error)
	VerifySnapshot(root *snapshot.Root, stored []byte) error
	Close()
}

// Target is a consented target profile, resolved for writing.
type Target struct {
	Profile guard.Profile
	Tokens  snapshot.Tokens
	Token   tokens.Token // nil: write as the process
	Close   func()       // releases Token; may be nil
}

// ErrNotRunnable is returned for a restore that is not queued or running.
var ErrNotRunnable = errors.New("restore: not waiting to run on this device")

// Jobs runs server-requested restores on this device: one goroutine per
// restore, reporting progress and items until a terminal status.
type Jobs struct {
	Server   Server
	OpenRepo func(ctx context.Context, repoID int64) (Repo, error)
	// Target resolves and authorises the target profile (consented only).
	Target func(ctx context.Context, sid string) (Target, error)
	Guard  func(op guard.Op, path string, p guard.Profile) error
	Log    *slog.Logger
	// ReportEvery is the progress report interval (default 2s);
	// ReportItems flushes earlier once that many items are buffered
	// (default 500, at most wire.RestoreProgressItemsMax).
	ReportEvery time.Duration
	ReportItems int
	// OnFinish, when set, receives each restore's final status.
	OnFinish func(id int64, status string, p Progress)

	mu      sync.Mutex
	running map[int64]context.CancelFunc
	wg      sync.WaitGroup
}

func (j *Jobs) log() *slog.Logger {
	if j.Log != nil {
		return j.Log
	}
	return slog.Default()
}

// Running reports whether restore id is running here.
func (j *Jobs) Running(id int64) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	_, ok := j.running[id]
	return ok
}

// Cancel stops restore id; it reports cancelled.
func (j *Jobs) Cancel(id int64) bool {
	j.mu.Lock()
	cancel, ok := j.running[id]
	j.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// Wait waits for every running restore to end.
func (j *Jobs) Wait() { j.wg.Wait() }

// Start fetches restore id and runs it in the background under parent. It
// returns once the restore was accepted (nil) or refused; a refusal after
// the fetch is also reported to the server as failed.
func (j *Jobs) Start(parent context.Context, id int64) error {
	j.mu.Lock()
	if j.running == nil {
		j.running = map[int64]context.CancelFunc{}
	}
	if _, dup := j.running[id]; dup {
		j.mu.Unlock()
		return nil // a re-delivered task for a restore already running
	}
	ctx, cancel := context.WithCancel(parent)
	j.running[id] = cancel
	j.mu.Unlock()
	done := func() {
		j.mu.Lock()
		delete(j.running, id)
		j.mu.Unlock()
		cancel()
	}

	dr, err := j.Server.DeviceRestore(ctx, id)
	if err != nil {
		done()
		return err
	}
	if s := dr.Restore.Status; s != wire.RestoreStatusQueued && s != wire.RestoreStatusRunning {
		done()
		return fmt.Errorf("%w (status %s)", ErrNotRunnable, s)
	}
	refuse := func(msg string, err error) error {
		j.final(ctx, id, Progress{}, wire.RestoreStatusFailed, msg+": "+err.Error())
		done()
		return err
	}
	tgt, err := j.Target(ctx, dr.TargetProfileSID)
	if err != nil {
		return refuse("the target Windows profile cannot be restored into", err)
	}
	r, err := j.OpenRepo(ctx, dr.RepoID)
	if err != nil {
		if tgt.Close != nil {
			tgt.Close()
		}
		return refuse("the repository cannot be opened on this device", err)
	}
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		defer done()
		defer r.Close()
		if tgt.Close != nil {
			defer tgt.Close()
		}
		j.run(ctx, parent, cancel, dr, r, tgt)
	}()
	return nil
}

// run executes one restore and reports it.
func (j *Jobs) run(ctx, parent context.Context, cancel context.CancelFunc, dr *wire.DeviceRestore, r Repo, tgt Target) {
	id := dr.Restore.ID
	root, stored, err := r.LoadSnapshot(ctx, dr.SnapshotOID)
	if err == nil {
		if verr := r.VerifySnapshot(root, stored); verr != nil && !isNoDeviceKey(verr) {
			err = fmt.Errorf("the snapshot signature does not verify: %w", verr)
		}
	}
	if err != nil {
		j.final(ctx, id, Progress{}, wire.RestoreStatusFailed, "cannot read the snapshot: "+err.Error())
		return
	}
	toks := snapshot.Tokens{}
	for k, v := range tgt.Tokens {
		toks[k] = v
	}
	for _, m := range dr.Mappings {
		if m.To != "" {
			toks[m.Token] = m.To
		}
	}

	every := j.ReportEvery
	if every <= 0 {
		every = 2 * time.Second
	}
	maxItems := j.ReportItems
	if maxItems <= 0 || maxItems > wire.RestoreProgressItemsMax {
		maxItems = 500
	}
	var mu sync.Mutex
	var buf []wire.RestoreItem
	var cur Progress
	var serverCancel bool
	send := func(force bool) {
		mu.Lock()
		if !force && len(buf) < maxItems {
			mu.Unlock()
			return
		}
		items := buf
		if len(items) > wire.RestoreProgressItemsMax {
			items = items[:wire.RestoreProgressItemsMax]
		}
		buf = buf[len(items):]
		p := cur
		mu.Unlock()
		rc, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer rcancel()
		reply, err := j.Server.ReportRestore(rc, id, progressBody(wire.RestoreStatusRunning, p, "", items))
		if err != nil {
			j.log().Warn("restore progress not reported", "restore_id", id, "err", err)
			mu.Lock()
			buf = append(items, buf...) // retry with the next report
			if len(buf) > 50*wire.RestoreProgressItemsMax {
				buf = buf[len(buf)-50*wire.RestoreProgressItemsMax:]
			}
			mu.Unlock()
			return
		}
		if reply.CancelRequested {
			mu.Lock()
			serverCancel = true
			mu.Unlock()
			cancel()
		}
	}
	send(true) // running

	stop := make(chan struct{})
	tick := time.NewTicker(every)
	var tw sync.WaitGroup
	tw.Add(1)
	go func() {
		defer tw.Done()
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				send(true)
			}
		}
	}()

	p, runErr := Run(ctx, r, Request{Roots: root.Roots, Selection: dr.Selection, Overrides: dr.Overrides, Tokens: toks,
		Policy: dr.ConflictPolicy, Profile: tgt.Profile}, Options{Guard: j.Guard, Token: tgt.Token,
		OnItem: func(it wire.RestoreItem) {
			mu.Lock()
			buf = append(buf, it)
			full := len(buf) >= maxItems
			mu.Unlock()
			if full {
				send(false)
			}
		},
		OnProgress: func(p Progress) { mu.Lock(); cur = p; mu.Unlock() }})
	close(stop)
	tw.Wait()
	mu.Lock()
	cur = p
	sc := serverCancel
	mu.Unlock()

	status, msg := p.Status(), ""
	switch {
	case runErr == nil:
	case sc || (errors.Is(runErr, context.Canceled) && parent.Err() == nil):
		status, msg = wire.RestoreStatusCancelled, "cancelled"
	case parent.Err() != nil:
		status, msg = wire.RestoreStatusFailed, "the VaultDock service stopped during the restore"
	default:
		status, msg = wire.RestoreStatusFailed, runErr.Error()
	}
	// Flush the remaining items, then the terminal report.
	for {
		mu.Lock()
		n := len(buf)
		mu.Unlock()
		if n <= wire.RestoreProgressItemsMax {
			break
		}
		send(true)
		mu.Lock()
		stuck := len(buf) >= n
		mu.Unlock()
		if stuck {
			break
		}
	}
	mu.Lock()
	items := buf
	buf = nil
	mu.Unlock()
	if len(items) > wire.RestoreProgressItemsMax {
		items = items[len(items)-wire.RestoreProgressItemsMax:]
	}
	j.finalItems(ctx, id, p, status, msg, items)
}

// isNoDeviceKey: a snapshot whose signer has no key file is a warning, not
// a failure (backup-format §8.1).
func isNoDeviceKey(err error) bool { return errors.Is(err, repo.ErrNoDeviceKey) }

func progressBody(status string, p Progress, msg string, items []wire.RestoreItem) wire.RestoreProgress {
	return wire.RestoreProgress{Status: status, FilesTotal: p.FilesTotal, FilesDone: p.FilesDone, BytesTotal: p.BytesTotal,
		BytesDone: p.BytesDone, Error: msg, Items: items}
}

func (j *Jobs) final(ctx context.Context, id int64, p Progress, status, msg string) {
	j.finalItems(ctx, id, p, status, msg, nil)
}

func (j *Jobs) finalItems(ctx context.Context, id int64, p Progress, status, msg string, items []wire.RestoreItem) {
	rc, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if _, err := j.Server.ReportRestore(rc, id, progressBody(status, p, msg, items)); err != nil {
		j.log().Warn("restore result not reported", "restore_id", id, "status", status, "err", err)
	}
	j.log().Info("restore finished", "restore_id", id, "status", status, "files", p.FilesDone, "failed", p.Failed, "blocked", p.Blocked)
	if j.OnFinish != nil {
		j.OnFinish(id, status, p)
	}
}
