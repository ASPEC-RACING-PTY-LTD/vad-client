package backup

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/profiles"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// RunStatusSkipped is the local status of a run that backed nothing up
// because the profile's user must sign in first (ErrProfileNeedsLogon).
// api-v1 has no such run status: the server sees the run as cancelled,
// with a profile_needs_logon issue (reported as class other).
const RunStatusSkipped = "skipped"

// Runner defaults.
const (
	DefaultHeartbeatEvery  = 2 * time.Second // api-v1 §6: at most one heartbeat per 2 s
	DefaultIssueFlushEvery = 10 * time.Second
	DefaultResumeEvery     = 10 * time.Minute // ResumeLoop
)

var (
	// ErrRunBusy is returned when the job already has a run in progress.
	ErrRunBusy = errors.New("backup: the job is already running")
	// ErrJobNotRunnable is returned for a disabled or suspended job.
	ErrJobNotRunnable = errors.New("backup: the job is disabled or suspended")
	// ErrUnknownRun is returned by RetrySkipped for a run this device does
	// not know.
	ErrUnknownRun = errors.New("backup: unknown run")
)

// RunAPI is the server surface the runner uses; *client.Client satisfies
// it.
type RunAPI interface {
	StartRun(ctx context.Context, jobID int64, req wire.RunStartRequest) (int64, error)
	RunHeartbeat(ctx context.Context, runID int64, p wire.RunProgress) error
	FinishRun(ctx context.Context, runID int64, p wire.RunProgress) error
	ReportIssues(ctx context.Context, runID int64, issues []wire.IssueReport) error
	CommitSnapshot(ctx context.Context, repoID int64, req wire.SnapshotCommitRequest) (*wire.SnapshotCommitResponse, bool, error)
}

var _ RunAPI = (*client.Client)(nil)

// Hooks are the per-run callbacks an Engine passes to the pipeline.
type Hooks struct {
	Progress func(RunStats) // must not block
	OnIssue  func(Issue)
}

// Engine is one job's opened repository and pipeline. RepoEngines builds
// the real one; tests use fakes.
type Engine interface {
	// Run executes the pipeline (Pipeline.Run).
	Run(ctx context.Context, in Params, h Hooks) (*Result, error)
	// CommitRefs returns the index objects and every pack the saved
	// snapshot needs (api-v1 §5.2 index_oids, pack_oids).
	CommitRefs(ctx context.Context, root *snapshot.Root) (indexOIDs, packOIDs []string, err error)
	// KeyID names this device's key file (snapshot.Device.KeyID); "" when
	// none matches.
	KeyID(deviceID int64, signPub ed25519.PublicKey) string
	Close()
}

// EngineFactory opens the engine for a job's repository.
type EngineFactory func(ctx context.Context, job *Job) (Engine, error)

// ProfileInfo is the Windows profile a job reads.
type ProfileInfo struct {
	SID, Username, ProfilePath, ConsentID string
	// Allowed: consented (active_default or authorised) with a proof; the
	// agent never reads any other profile (api-v1 §6B rule 2).
	Allowed bool
}

// ProfileLookup resolves a job's profile; sid "" means the active user's
// profile (state active_default).
type ProfileLookup func(ctx context.Context, sid string) (ProfileInfo, error)

// StoreProfiles adapts the local profiles store (state.db).
func StoreProfiles(st *profiles.Store) ProfileLookup {
	return func(ctx context.Context, sid string) (ProfileInfo, error) {
		var rec *profiles.Record
		if sid != "" {
			r, err := st.Get(ctx, sid)
			if err != nil {
				return ProfileInfo{}, err
			}
			rec = r
		} else {
			recs, err := st.List(ctx)
			if err != nil {
				return ProfileInfo{}, err
			}
			for i := range recs {
				if recs[i].State == wire.ProfileStateActiveDefault {
					rec = &recs[i]
					break
				}
			}
			if rec == nil {
				return ProfileInfo{}, profiles.ErrNoActiveUser
			}
		}
		return ProfileInfo{SID: rec.SID, Username: rec.Username, ProfilePath: rec.ProfilePath,
			ConsentID: rec.ConsentID, Allowed: rec.Allowed()}, nil
	}
}

// Identity is this device as recorded in snapshots.
type Identity struct {
	DeviceID    int64
	DeviceName  string
	Key         ed25519.PrivateKey // VD1 key; signs snapshot roots
	Hostname    string
	InstallMode string // "simple" | "advanced"; "" = simple
}

// Runner executes backup runs (MASTER_PLAN §7.9, api-v1 §5.2/§6): it
// registers the run, runs the pipeline, commits the snapshot, sends
// heartbeats and issues, and reports the final status. A job has at most
// one run at a time, and runs of jobs sharing a repository are serialised
// (they share the spool and the pending index).
type Runner struct {
	Store    *Store
	API      RunAPI
	Engines  EngineFactory
	Profiles ProfileLookup
	Device   Identity

	HeartbeatEvery  time.Duration // 0 = DefaultHeartbeatEvery
	IssueFlushEvery time.Duration // 0 = DefaultIssueFlushEvery
	Log             *slog.Logger
	Now             func() time.Time

	mu    sync.Mutex
	busy  map[int64]bool
	repos map[int64]*sync.Mutex
	wg    sync.WaitGroup
}

func (r *Runner) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Running reports whether job has a run in progress (scheduler.Config.Running).
func (r *Runner) Running(jobID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.busy[jobID]
}

// Wait blocks until every background run has returned.
func (r *Runner) Wait() { r.wg.Wait() }

func (r *Runner) acquire(jobID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.busy == nil {
		r.busy = map[int64]bool{}
	}
	if r.busy[jobID] {
		return false
	}
	r.busy[jobID] = true
	return true
}

func (r *Runner) release(jobID int64) {
	r.mu.Lock()
	delete(r.busy, jobID)
	r.mu.Unlock()
}

func (r *Runner) repoLock(repoID int64) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.repos == nil {
		r.repos = map[int64]*sync.Mutex{}
	}
	m := r.repos[repoID]
	if m == nil {
		m = &sync.Mutex{}
		r.repos[repoID] = m
	}
	return m
}

// NewRunUUID returns a random client_run_uuid (UUID v4 text).
func NewRunUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// begin checks the job and records a queued local run.
func (r *Runner) begin(ctx context.Context, jobID int64, trigger string) (*Job, *Run, error) {
	j, err := r.Store.Job(ctx, jobID)
	if err != nil {
		return nil, nil, err
	}
	if !j.Runnable() {
		return nil, nil, ErrJobNotRunnable
	}
	if !r.acquire(jobID) {
		return nil, nil, ErrRunBusy
	}
	uuid, err := NewRunUUID()
	if err == nil {
		lr := &Run{ClientRunUUID: uuid, JobID: jobID, Trigger: trigger, Status: wire.RunStatusQueued, StartedAt: r.now().UnixMilli()}
		if err = r.Store.PutRun(ctx, *lr); err == nil {
			return j, lr, nil
		}
	}
	r.release(jobID)
	return nil, nil, err
}

// register creates (or, on resume, replays) the server run. A failure
// other than cancellation finishes the local run as failed.
func (r *Runner) register(ctx context.Context, lr *Run) error {
	id, err := r.API.StartRun(ctx, lr.JobID, wire.RunStartRequest{ClientRunUUID: lr.ClientRunUUID, Trigger: lr.Trigger})
	if err == nil {
		lr.RunID = id
		err = r.Store.SetRunServerID(ctx, lr.ClientRunUUID, id)
	}
	if err != nil && ctx.Err() == nil {
		r.log().Warn("backup: run not registered", "job", lr.JobID, "err", err)
		if ferr := r.Store.FinishRun(context.WithoutCancel(ctx), lr.ClientRunUUID, wire.RunStatusFailed, 0, err.Error()); ferr != nil {
			r.log().Error("backup: finish local run", "run", lr.ClientRunUUID, "err", ferr)
		}
	}
	return err
}

// Start begins a run of jobID: it records the run locally and on the
// server, then backs up in the background and returns the server run id.
// ctx must outlive the run (the service context); cancelling it stops the
// run, which stays unfinished locally and is picked up by Resume.
func (r *Runner) Start(ctx context.Context, jobID int64, trigger string) (int64, error) {
	j, lr, err := r.begin(ctx, jobID, trigger)
	if err != nil {
		return 0, err
	}
	if err := r.register(ctx, lr); err != nil {
		r.release(jobID)
		return 0, err
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer r.release(jobID)
		r.execute(ctx, j, lr)
	}()
	return lr.RunID, nil
}

// Trigger starts a run without waiting for the server: it is the
// scheduler's TriggerFunc (scheduler.Config.Trigger) and returns at once
// with ErrRunBusy, ErrJobNotRunnable or ErrNotFound when it cannot start.
func (r *Runner) Trigger(ctx context.Context, jobID int64, trigger string) error {
	j, lr, err := r.begin(ctx, jobID, trigger)
	if err != nil {
		return err
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer r.release(jobID)
		if r.register(ctx, lr) != nil {
			return
		}
		r.execute(ctx, j, lr)
	}()
	return nil
}

// Resume handles unfinished runs that no live run of this process owns:
// runs that already reached a final status, or committed their snapshot,
// are reported to the server (keeping the recorded snapshot id); the
// others are run again under the same client_run_uuid (the server replays
// the run id and the pipeline uploads the spooled packs first). Runs of
// jobs that are gone or no longer runnable are finished as cancelled.
// Runs of a job that is running now are left for a later Resume, so it is
// safe to call periodically (ResumeLoop).
func (r *Runner) Resume(ctx context.Context) error {
	runs, err := r.Store.UnfinishedRuns(ctx)
	if err != nil {
		return err
	}
	for _, lr := range runs {
		lr := lr
		if !r.acquire(lr.JobID) {
			continue
		}
		started, err := r.resumeRun(ctx, &lr)
		if !started {
			r.release(lr.JobID)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// resumeRun handles one unfinished run whose job the caller acquired.
// started reports that a background run took the job over (it releases
// the job when done).
func (r *Runner) resumeRun(ctx context.Context, lr *Run) (started bool, err error) {
	switch {
	case terminalStatus(lr.Status):
		r.finalize(ctx, lr, outcome{status: lr.Status, stats: lr.Stats, snapshotID: lr.SnapshotID, err: lr.Error})
		return false, nil
	case lr.SnapshotID > 0:
		// Committed, but the process stopped before the final status was
		// saved: report it rather than back up again.
		r.finalize(ctx, lr, outcome{status: committedStatus(lr.Stats), stats: lr.Stats, snapshotID: lr.SnapshotID})
		return false, nil
	}
	j, err := r.Store.Job(ctx, lr.JobID)
	if err != nil || !j.Runnable() {
		msg := "the job was removed or suspended before the run finished"
		if err != nil && !errors.Is(err, ErrNotFound) {
			return false, err
		}
		if lr.RunID == 0 {
			return false, r.Store.FinishRun(ctx, lr.ClientRunUUID, wire.RunStatusCancelled, 0, msg)
		}
		r.finalize(ctx, lr, outcome{status: wire.RunStatusCancelled, stats: lr.Stats, err: msg})
		return false, nil
	}
	r.log().Info("backup: resuming run", "job", lr.JobID, "run", lr.ClientRunUUID)
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer r.release(lr.JobID)
		if r.register(ctx, lr) != nil {
			return
		}
		r.execute(ctx, j, lr)
	}()
	return true, nil
}

// committedStatus is the final status of a committed run whose outcome
// was not saved: partial when its last progress counted errors (skipped
// items), else completed.
func committedStatus(st RunStats) string {
	if st.Errors > 0 {
		return wire.RunStatusPartial
	}
	return wire.RunStatusCompleted
}

// ResumeLoop calls Resume every interval (0 = DefaultResumeEvery) until
// ctx ends, so final statuses the server did not accept are retried while
// the service runs. Call Resume once at start; ResumeLoop does not.
func (r *Runner) ResumeLoop(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = DefaultResumeEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.Resume(ctx); err != nil && ctx.Err() == nil {
				r.log().Warn("backup: resume runs", "err", err)
			}
		}
	}
}

// outcome is a run's final state.
type outcome struct {
	status     string // wire terminal status or RunStatusSkipped; "" = interrupted
	stats      RunStats
	snapshotID int64
	err        string
}

func terminalStatus(s string) bool {
	switch s {
	case wire.RunStatusCompleted, wire.RunStatusPartial, wire.RunStatusFailed, wire.RunStatusCancelled, RunStatusSkipped:
		return true
	}
	return false
}

// wireStatus maps a local final status to the api-v1 run status.
func wireStatus(s string) string {
	if s == RunStatusSkipped {
		return wire.RunStatusCancelled
	}
	return s
}

// execute runs one registered run to its end.
func (r *Runner) execute(ctx context.Context, j *Job, lr *Run) {
	rep := newReporter(r, lr)
	rep.start(ctx)
	o := r.backup(ctx, j, lr, rep)
	rep.stop()
	if o.status == "" {
		// Interrupted (service stopping): keep the run unfinished for Resume.
		st := rep.last()
		status := st.Status
		if status == "" {
			status = lr.Status
		}
		if err := r.Store.UpdateRunStats(context.WithoutCancel(ctx), lr.ClientRunUUID, status, st); err != nil {
			r.log().Warn("backup: save interrupted run", "run", lr.ClientRunUUID, "err", err)
		}
		return
	}
	r.finalize(ctx, lr, o)
}

// backup runs the pipeline and commits the snapshot.
func (r *Runner) backup(ctx context.Context, j *Job, lr *Run, rep *reporter) outcome {
	start := r.now()
	fail := func(err error) outcome {
		if ctx.Err() != nil {
			return outcome{}
		}
		r.addIssue(ctx, lr, errorIssue(err))
		st := rep.last()
		st.Errors++
		return outcome{status: wire.RunStatusFailed, stats: st, err: err.Error()}
	}

	prof, err := r.Profiles(ctx, j.ProfileSID)
	if err != nil {
		return fail(fmt.Errorf("backup: profile: %w", err))
	}
	if !prof.Allowed {
		r.addIssue(ctx, lr, Issue{Class: wire.IssueClassProfileUnauthorised, Severity: wire.IssueSeverityError,
			Detail: "the Windows profile has no active consent for backup"})
		st := rep.last()
		st.Errors++
		return outcome{status: wire.RunStatusFailed, stats: st, err: "profile not authorised"}
	}

	mu := r.repoLock(j.RepoID)
	mu.Lock()
	defer mu.Unlock()
	if ctx.Err() != nil {
		return outcome{}
	}
	eng, err := r.Engines(ctx, j)
	if err != nil {
		return fail(fmt.Errorf("backup: open repository: %w", err))
	}
	defer eng.Close()

	parentID, parentOID := r.lastSnapshot(ctx, j.ID, lr.ClientRunUUID)
	pub, _ := r.Device.Key.Public().(ed25519.PublicKey)
	spec := SpecFor(j, nil, guard.Profile{SID: prof.SID, Root: prof.ProfilePath}, 0)
	spec.ProfileSID = prof.SID
	in := Params{JobID: j.ID, RunUUID: lr.ClientRunUUID, Spec: spec, DeviceKey: r.Device.Key,
		Root: snapshot.Root{
			Parent:      parentOID,
			Job:         snapshot.Job{ID: j.ID, Name: j.Name, Kind: j.Kind, Mode: j.Mode},
			Device:      snapshot.Device{ID: r.Device.DeviceID, Name: r.Device.DeviceName, KeyID: eng.KeyID(r.Device.DeviceID, pub)},
			Host:        snapshot.Host{Hostname: r.Device.Hostname, OS: runtime.GOOS, User: prof.Username, SID: prof.SID},
			Profile:     snapshot.Profile{SID: prof.SID, Username: prof.Username, ConsentID: prof.ConsentID},
			InstallMode: r.Device.InstallMode,
		}}
	res, err := eng.Run(ctx, in, Hooks{Progress: rep.progress, OnIssue: func(is Issue) { r.addIssue(ctx, lr, is) }})
	if res != nil {
		rep.progress(res.Stats)
	}
	switch {
	case errors.Is(err, ErrProfileNeedsLogon):
		return outcome{status: RunStatusSkipped, stats: rep.last()}
	case err != nil:
		return fail(err)
	case res == nil || res.Root == nil || res.SnapshotOID == "":
		return fail(errors.New("backup: the pipeline returned no snapshot"))
	}

	// Commit (api-v1 §5.2).
	st := res.Stats
	st.Status, st.Phase = wire.RunStatusCommitting, PhaseCommit
	rep.progress(st)
	idx, packs, err := eng.CommitRefs(ctx, res.Root)
	if err != nil {
		return fail(fmt.Errorf("backup: snapshot references: %w", err))
	}
	req := commitRequest(j, lr, prof, r.Device, res, idx, packs, parentID, r.now().Sub(start))
	var resp *wire.SnapshotCommitResponse
	err = retry(ctx, func() error {
		var err error
		resp, _, err = r.API.CommitSnapshot(ctx, j.RepoID, req)
		return err
	})
	if err != nil {
		return fail(fmt.Errorf("backup: commit snapshot: %w", err))
	}
	// Record the snapshot before the final status: it is the next run's
	// parent, and Resume keeps it if the final status is not reported now.
	lr.SnapshotID, lr.SnapshotOID = resp.SnapshotID, res.SnapshotOID
	if err := r.Store.SetRunSnapshot(context.WithoutCancel(ctx), lr.ClientRunUUID, resp.SnapshotID, res.SnapshotOID); err != nil {
		r.log().Error("backup: record snapshot", "run", lr.ClientRunUUID, "snapshot", resp.SnapshotID, "err", err)
	}

	status := wire.RunStatusCompleted
	if partial(res) {
		status = wire.RunStatusPartial
	}
	return outcome{status: status, stats: res.Stats, snapshotID: resp.SnapshotID}
}

// PhaseCommit is the progress phase of the catalog commit.
const PhaseCommit = "commit"

// partial: committed with skipped items (rev 1.0 §6), or a walk that
// could not list everything.
func partial(res *Result) bool {
	if !res.Walk.Complete {
		return true
	}
	for _, is := range res.Issues {
		if is.Severity != wire.IssueSeverityWarning {
			return true
		}
	}
	return false
}

// lastSnapshot is the job's latest committed snapshot from earlier local
// runs: its server id (parent_snapshot_id) and root oid (Root.Parent; nil
// when that run predates runs.snapshot_oid).
func (r *Runner) lastSnapshot(ctx context.Context, jobID int64, self string) (*int64, *string) {
	runs, err := r.Store.Runs(ctx, jobID, 50)
	if err != nil {
		return nil, nil
	}
	for _, x := range runs {
		if x.ClientRunUUID != self && x.SnapshotID > 0 {
			id := x.SnapshotID
			if x.SnapshotOID == "" {
				return &id, nil
			}
			oid := x.SnapshotOID
			return &id, &oid
		}
	}
	return nil, nil
}

// finalize reports issues and the final status to the server, then
// finishes the local run. If the server cannot be reached, the local run
// keeps its final status but stays unfinished so Resume reports it later.
func (r *Runner) finalize(ctx context.Context, lr *Run, o outcome) {
	log := r.log().With("job", lr.JobID, "run", lr.ClientRunUUID)
	st := o.stats
	st.Status = wireStatus(o.status)
	if err := r.Store.UpdateRunStats(context.WithoutCancel(ctx), lr.ClientRunUUID, o.status, st); err != nil {
		log.Error("backup: save run status", "err", err)
	}
	if lr.RunID != 0 {
		err := r.flushIssues(ctx, lr.ClientRunUUID, lr.RunID)
		if err == nil {
			err = retry(ctx, func() error { return r.API.FinishRun(ctx, lr.RunID, st) })
			if client.IsCode(err, codeRunFinished) {
				err = nil
			}
		}
		if err != nil {
			log.Warn("backup: final status not reported; will retry on resume", "status", o.status, "err", err)
			return
		}
	}
	if err := r.Store.FinishRun(context.WithoutCancel(ctx), lr.ClientRunUUID, o.status, o.snapshotID, o.err); err != nil {
		log.Error("backup: finish local run", "err", err)
		return
	}
	log.Info("backup: run finished", "status", o.status, "snapshot", o.snapshotID)
}

// codeRunFinished is the server's 409 for a heartbeat after the run ended
// (P1-18 server-local code).
const codeRunFinished = "run_finished"

// retry calls fn up to three times while it fails with a transient error
// (network, 429 or 5xx), honouring Retry-After.
func retry(ctx context.Context, fn func() error) error {
	delay := time.Second
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = fn(); err == nil || !transient(err) || ctx.Err() != nil {
			return err
		}
		d := delay
		var ae *client.APIError
		if errors.As(err, &ae) && ae.RetryAfter > d {
			d = min(ae.RetryAfter, time.Minute)
		}
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return err
		case <-t.C:
		}
		delay *= 2
	}
	return err
}

func transient(err error) bool {
	var ae *client.APIError
	if errors.As(err, &ae) {
		return ae.Status == 429 || ae.Status >= 500
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
