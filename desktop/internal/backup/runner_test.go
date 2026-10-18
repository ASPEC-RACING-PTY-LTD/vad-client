package backup

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// --- fakes ---

type fakeRunAPI struct {
	mu        sync.Mutex
	events    []string // "start", "beat", "issues:N", "commit", "finish:<status>"
	runs      map[string]int64
	triggers  map[int64]string
	nextRun   int64
	beats     []wire.RunProgress
	issues    map[int64][]wire.IssueReport
	commits   []wire.SnapshotCommitRequest
	finished  map[int64]wire.RunProgress
	commitErr error
	finishErr error
	startErr  error
}

func newFakeRunAPI() *fakeRunAPI {
	return &fakeRunAPI{runs: map[string]int64{}, triggers: map[int64]string{}, issues: map[int64][]wire.IssueReport{},
		finished: map[int64]wire.RunProgress{}, nextRun: 100}
}

func (f *fakeRunAPI) ev(s string) { f.events = append(f.events, s) }

func (f *fakeRunAPI) StartRun(_ context.Context, jobID int64, req wire.RunStartRequest) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return 0, f.startErr
	}
	f.ev("start")
	if id, ok := f.runs[req.ClientRunUUID]; ok {
		return id, nil
	}
	f.nextRun++
	f.runs[req.ClientRunUUID] = f.nextRun
	f.triggers[f.nextRun] = req.Trigger
	return f.nextRun, nil
}

func (f *fakeRunAPI) RunHeartbeat(_ context.Context, runID int64, p wire.RunProgress) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ev("beat")
	f.beats = append(f.beats, p)
	return nil
}

func (f *fakeRunAPI) FinishRun(_ context.Context, runID int64, p wire.RunProgress) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.finishErr != nil {
		return f.finishErr
	}
	f.ev("finish:" + p.Status)
	f.finished[runID] = p
	return nil
}

func (f *fakeRunAPI) ReportIssues(_ context.Context, runID int64, issues []wire.IssueReport) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(issues) > wire.IssueBatchMax {
		return fmt.Errorf("batch of %d", len(issues))
	}
	f.ev(fmt.Sprintf("issues:%d", len(issues)))
	f.issues[runID] = append(f.issues[runID], issues...)
	return nil
}

func (f *fakeRunAPI) CommitSnapshot(_ context.Context, repoID int64, req wire.SnapshotCommitRequest) (*wire.SnapshotCommitResponse, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commitErr != nil {
		return nil, false, f.commitErr
	}
	f.ev("commit")
	f.commits = append(f.commits, req)
	return &wire.SnapshotCommitResponse{SnapshotID: int64(500 + len(f.commits))}, true, nil
}

func (f *fakeRunAPI) snapshot() fakeRunAPI {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeRunAPI{events: append([]string(nil), f.events...), beats: append([]wire.RunProgress(nil), f.beats...),
		commits: append([]wire.SnapshotCommitRequest(nil), f.commits...), finished: copyMap(f.finished), issues: f.issues,
		triggers: copyMap(f.triggers)}
}

func copyMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

type fakeEngine struct {
	mu     sync.Mutex
	params []Params
	opened int
	closed int

	result  func(in Params) *Result
	err     error
	issues  []Issue
	block   chan struct{} // when set, Run waits for it (or ctx)
	entered chan struct{}
	beats   int
}

func (e *fakeEngine) factory(ctx context.Context, j *Job) (Engine, error) {
	e.mu.Lock()
	e.opened++
	e.mu.Unlock()
	return e, nil
}

func (e *fakeEngine) Run(ctx context.Context, in Params, h Hooks) (*Result, error) {
	e.mu.Lock()
	e.params = append(e.params, in)
	e.mu.Unlock()
	if e.entered != nil {
		e.entered <- struct{}{}
	}
	for i := 0; i < e.beats; i++ {
		h.Progress(RunStats{Status: wire.RunStatusScanning, Phase: PhaseScan, FilesDone: int64(i + 1)})
		time.Sleep(5 * time.Millisecond)
	}
	for _, is := range e.issues {
		h.OnIssue(is)
	}
	if e.block != nil {
		select {
		case <-e.block:
		case <-ctx.Done():
			return &Result{}, ctx.Err()
		}
	}
	if e.err != nil {
		return &Result{Issues: e.issues}, e.err
	}
	res := e.result(in)
	res.Issues = e.issues
	return res, nil
}

func (e *fakeEngine) CommitRefs(context.Context, *snapshot.Root) ([]string, []string, error) {
	return []string{strings.Repeat("1", 64)}, []string{strings.Repeat("2", 64), strings.Repeat("3", 64)}, nil
}

func (e *fakeEngine) KeyID(int64, ed25519.PublicKey) string { return "kid" }

func (e *fakeEngine) Close() {
	e.mu.Lock()
	e.closed++
	e.mu.Unlock()
}

func okResult(oid string) func(in Params) *Result {
	return func(in Params) *Result {
		root := in.Root
		root.Time, root.Epoch = 1234, 2
		root.Stats = snapshot.Stats{Files: 3, Dirs: 1, Bytes: 30}
		return &Result{SnapshotOID: oid, Root: &root, Walk: WalkStats{Complete: true},
			Stats: RunStats{Status: wire.RunStatusCommitting, FilesDone: 3, BytesNew: 20}, Packs: []string{"p"}}
	}
}

type runnerEnv struct {
	st   *Store
	api  *fakeRunAPI
	eng  *fakeEngine
	r    *Runner
	prof ProfileInfo
}

func newRunnerEnv(t *testing.T) *runnerEnv {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	_, key, _ := ed25519.GenerateKey(nil)
	env := &runnerEnv{st: st, api: newFakeRunAPI(), eng: &fakeEngine{result: okResult(strings.Repeat("a", 64))},
		prof: ProfileInfo{SID: "S-1-5-21-1-2-3-1001", Username: "ali", ProfilePath: `C:\Users\ali`, ConsentID: "c1", Allowed: true}}
	env.r = &Runner{Store: st, API: env.api, Engines: env.eng.factory, Device: Identity{DeviceID: 3, DeviceName: "pc", Key: key, Hostname: "PC"},
		HeartbeatEvery: 5 * time.Millisecond, IssueFlushEvery: 5 * time.Millisecond,
		Profiles: func(_ context.Context, sid string) (ProfileInfo, error) { return env.prof, nil }}
	env.putJob(t, 1, true)
	return env
}

func (env *runnerEnv) putJob(t *testing.T, id int64, enabled bool) {
	t.Helper()
	j := Job{Job: wire.Job{ID: id, DeviceID: 3, RepoID: 7, Name: "Docs", Kind: wire.JobKindFolder, Mode: wire.JobModeFull,
		Spec: wire.JobSpec{Paths: []string{"{DOCUMENTS}"}}, Enabled: enabled, Revision: 1, State: wire.JobStateActive, CreatedAt: 1000}}
	if err := env.st.PutJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}
}

func (env *runnerEnv) lastRun(t *testing.T, jobID int64) Run {
	t.Helper()
	runs, err := env.st.Runs(context.Background(), jobID, 1)
	if err != nil || len(runs) == 0 {
		t.Fatalf("runs: %v %v", runs, err)
	}
	return runs[0]
}

// --- tests ---

func TestRunnerCommitsSnapshot(t *testing.T) {
	env := newRunnerEnv(t)
	ctx := context.Background()
	runID, err := env.r.Start(ctx, 1, wire.RunTriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	env.r.Wait()
	got := env.api.snapshot()
	if len(got.commits) != 1 {
		t.Fatalf("commits %d", len(got.commits))
	}
	c := got.commits[0]
	if c.RunID != runID || c.JobID != 1 || c.UserSID != env.prof.SID || c.ProfileConsentID != "c1" || c.OSUser != "ali" ||
		c.ProfilePath != env.prof.ProfilePath || c.Hostname != "PC" || c.Epoch != 2 || c.ClientTime != 1234 ||
		c.Kind != wire.JobKindFolder || c.Mode != wire.JobModeFull || c.ParentSnapshotID != nil || len(c.PackOIDs) != 2 ||
		len(c.IndexOIDs) != 1 || c.Stats.FileCount != 3 || c.Stats.NewBytes != 20 || c.Stats.NewObjects != 1 || c.Tags == nil {
		t.Fatalf("commit %+v", c)
	}
	if got.finished[runID].Status != wire.RunStatusCompleted {
		t.Fatalf("finish %+v", got.finished)
	}
	lr := env.lastRun(t, 1)
	if lr.Status != wire.RunStatusCompleted || lr.FinishedAt == 0 || lr.SnapshotID != 501 || lr.SnapshotOID != strings.Repeat("a", 64) ||
		lr.RunID != runID || lr.Trigger != wire.RunTriggerManual {
		t.Fatalf("local run %+v", lr)
	}
	in := env.eng.params[0]
	if in.Spec.ProfileSID != env.prof.SID || in.Spec.Profile.Root != env.prof.ProfilePath || in.Root.Device.KeyID != "kid" ||
		in.Root.Profile.ConsentID != "c1" || in.Root.Parent != nil || in.Root.Job.ID != 1 || len(in.DeviceKey) != ed25519.PrivateKeySize {
		t.Fatalf("params %+v", in)
	}
	if env.eng.closed != 1 {
		t.Fatal("engine not closed")
	}

	// The next run names its parent (oid and server id).
	env.eng.result = okResult(strings.Repeat("b", 64))
	if _, err := env.r.Start(ctx, 1, wire.RunTriggerSchedule); err != nil {
		t.Fatal(err)
	}
	env.r.Wait()
	got = env.api.snapshot()
	if p := got.commits[1].ParentSnapshotID; p == nil || *p != 501 {
		t.Fatalf("parent id %v", p)
	}
	if p := env.eng.params[1].Root.Parent; p == nil || *p != strings.Repeat("a", 64) {
		t.Fatalf("parent oid %v", p)
	}

	// After a restart, the parent comes from backup.db.
	r2 := &Runner{Store: env.st, API: env.api, Engines: env.eng.factory, Profiles: env.r.Profiles, Device: env.r.Device}
	if _, err := r2.Start(ctx, 1, wire.RunTriggerSchedule); err != nil {
		t.Fatal(err)
	}
	r2.Wait()
	got = env.api.snapshot()
	if p := got.commits[2].ParentSnapshotID; p == nil || *p != 502 {
		t.Fatalf("parent id after restart %v", p)
	}
	if p := env.eng.params[2].Root.Parent; p == nil || *p != strings.Repeat("b", 64) {
		t.Fatalf("parent oid after restart %v", p)
	}
}

func TestRunnerPartialReportsIssuesBeforeFinish(t *testing.T) {
	env := newRunnerEnv(t)
	for i := 0; i < wire.IssueBatchMax+3; i++ {
		env.eng.issues = append(env.eng.issues, Issue{Path: fmt.Sprintf(`C:\f%d`, i), Class: wire.IssueClassLocked, Severity: wire.IssueSeveritySkipped, Retryable: true})
	}
	env.eng.issues = append(env.eng.issues, Issue{Path: `C:\e`, Class: IssueClassEFSNeedsLogon, Severity: wire.IssueSeveritySkipped})
	runID, err := env.r.Start(context.Background(), 1, wire.RunTriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	env.r.Wait()
	got := env.api.snapshot()
	if got.finished[runID].Status != wire.RunStatusPartial {
		t.Fatalf("status %+v", got.finished[runID])
	}
	if n := len(got.issues[runID]); n != wire.IssueBatchMax+4 {
		t.Fatalf("issues reported %d", n)
	}
	var efs *wire.IssueReport
	for i := range got.issues[runID] {
		if got.issues[runID][i].Path == `C:\e` {
			efs = &got.issues[runID][i]
		}
	}
	if efs == nil || efs.Class != wire.IssueClassAccessDenied || !strings.HasPrefix(efs.Detail, IssueClassEFSNeedsLogon) {
		t.Fatalf("efs mapping %+v", efs)
	}
	last := got.events[len(got.events)-1]
	if last != "finish:partial" {
		t.Fatalf("events end with %q", last)
	}
	if pend, _ := env.st.PendingIssues(context.Background(), env.lastRun(t, 1).ClientRunUUID, 0); len(pend) != 0 {
		t.Fatalf("unreported issues %d", len(pend))
	}
}

func TestRunnerOutcomes(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(env *runnerEnv)
		wire       string
		local      string
		issueClass string
		commits    int
	}{
		{name: "needs logon", setup: func(env *runnerEnv) { env.eng.err = fmt.Errorf("walk: %w", ErrProfileNeedsLogon) },
			wire: wire.RunStatusCancelled, local: RunStatusSkipped},
		{name: "pipeline error", setup: func(env *runnerEnv) { env.eng.err = errors.New("disk on fire") },
			wire: wire.RunStatusFailed, local: wire.RunStatusFailed, issueClass: wire.IssueClassOther},
		{name: "commit pack_missing", setup: func(env *runnerEnv) {
			env.api.commitErr = &client.APIError{Status: 409, Code: wire.CodePackMissing}
		}, wire: wire.RunStatusFailed, local: wire.RunStatusFailed, issueClass: wire.IssueClassServer},
		{name: "commit blacklisted", setup: func(env *runnerEnv) {
			env.api.commitErr = &client.APIError{Status: 423, Code: wire.CodeProfileBlacklisted}
		}, wire: wire.RunStatusFailed, local: wire.RunStatusFailed, issueClass: wire.IssueClassProfileUnauthorised},
		{name: "profile not allowed", setup: func(env *runnerEnv) { env.prof.Allowed = false },
			wire: wire.RunStatusFailed, local: wire.RunStatusFailed, issueClass: wire.IssueClassProfileUnauthorised},
		{name: "incomplete walk", setup: func(env *runnerEnv) {
			ok := env.eng.result
			env.eng.result = func(in Params) *Result { r := ok(in); r.Walk.Complete = false; return r }
		}, wire: wire.RunStatusPartial, local: wire.RunStatusPartial, commits: 1},
		{name: "warnings only", setup: func(env *runnerEnv) {
			env.eng.issues = []Issue{{Path: "{DOCUMENTS}", Class: wire.IssueClassNotFound, Severity: wire.IssueSeverityWarning}}
		}, wire: wire.RunStatusCompleted, local: wire.RunStatusCompleted, issueClass: wire.IssueClassNotFound, commits: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newRunnerEnv(t)
			tc.setup(env)
			runID, err := env.r.Start(context.Background(), 1, wire.RunTriggerManual)
			if err != nil {
				t.Fatal(err)
			}
			env.r.Wait()
			got := env.api.snapshot()
			if got.finished[runID].Status != tc.wire {
				t.Fatalf("wire status %+v", got.finished[runID])
			}
			lr := env.lastRun(t, 1)
			if lr.Status != tc.local || lr.FinishedAt == 0 {
				t.Fatalf("local %+v", lr)
			}
			if len(got.commits) != tc.commits {
				t.Fatalf("commits %d", len(got.commits))
			}
			if tc.issueClass != "" {
				is := got.issues[runID]
				if len(is) != 1 || is[0].Class != tc.issueClass {
					t.Fatalf("issues %+v", is)
				}
			}
			if tc.name == "profile not allowed" && env.eng.opened != 0 {
				t.Fatal("engine opened for an unconsented profile")
			}
		})
	}
}

func TestRunnerBusyAndNotRunnable(t *testing.T) {
	env := newRunnerEnv(t)
	env.eng.block = make(chan struct{})
	env.eng.entered = make(chan struct{}, 1)
	ctx := context.Background()
	if err := env.r.Trigger(ctx, 1, wire.RunTriggerSchedule); err != nil {
		t.Fatal(err)
	}
	<-env.eng.entered
	if !env.r.Running(1) {
		t.Fatal("not running")
	}
	if _, err := env.r.Start(ctx, 1, wire.RunTriggerManual); !errors.Is(err, ErrRunBusy) {
		t.Fatalf("start while busy: %v", err)
	}
	if err := env.r.Trigger(ctx, 1, wire.RunTriggerSchedule); !errors.Is(err, ErrRunBusy) {
		t.Fatalf("trigger while busy: %v", err)
	}
	close(env.eng.block)
	env.r.Wait()
	if env.r.Running(1) {
		t.Fatal("still running")
	}
	if got := env.api.snapshot(); got.triggers[101] != wire.RunTriggerSchedule {
		t.Fatalf("trigger %v", got.triggers)
	}

	env.putJob(t, 2, false)
	if _, err := env.r.Start(ctx, 2, wire.RunTriggerManual); !errors.Is(err, ErrJobNotRunnable) {
		t.Fatalf("disabled: %v", err)
	}
	if _, err := env.r.Start(ctx, 9, wire.RunTriggerManual); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestRunnerStartRejected(t *testing.T) {
	env := newRunnerEnv(t)
	env.api.startErr = &client.APIError{Status: 409, Code: wire.CodeModuleUnavailable}
	if _, err := env.r.Start(context.Background(), 1, wire.RunTriggerManual); !client.IsCode(err, wire.CodeModuleUnavailable) {
		t.Fatalf("err %v", err)
	}
	lr := env.lastRun(t, 1)
	if lr.Status != wire.RunStatusFailed || lr.FinishedAt == 0 || env.r.Running(1) {
		t.Fatalf("local %+v", lr)
	}
}

func TestRunnerHeartbeats(t *testing.T) {
	env := newRunnerEnv(t)
	env.eng.beats = 10
	if _, err := env.r.Start(context.Background(), 1, wire.RunTriggerManual); err != nil {
		t.Fatal(err)
	}
	env.r.Wait()
	got := env.api.snapshot()
	if len(got.beats) == 0 || len(got.beats) > 10 {
		t.Fatalf("heartbeats %d", len(got.beats))
	}
	for _, b := range got.beats {
		if b.Status == "" || b.Phase == "" {
			t.Fatalf("heartbeat %+v", b)
		}
	}
}

func TestRunnerResume(t *testing.T) {
	env := newRunnerEnv(t)
	env.putJob(t, 2, true)
	env.putJob(t, 3, true)
	env.eng.block = make(chan struct{})
	env.eng.entered = make(chan struct{}, 2)
	ctx, cancel := context.WithCancel(context.Background())
	id1, err := env.r.Start(ctx, 1, wire.RunTriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	<-env.eng.entered
	cancel() // service stops mid-run
	env.r.Wait()
	if got := env.api.snapshot(); len(got.finished) != 0 {
		t.Fatalf("interrupted run finished: %+v", got.finished)
	}
	interrupted := env.lastRun(t, 1)
	if interrupted.FinishedAt != 0 {
		t.Fatalf("interrupted run %+v", interrupted)
	}

	// A run whose final status could not be reported.
	env.eng.block, env.eng.entered = nil, nil
	env.api.finishErr = &client.APIError{Status: 400, Code: "validation"}
	id2, err := env.r.Start(context.Background(), 2, wire.RunTriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	env.r.Wait()
	if lr := env.lastRun(t, 2); lr.FinishedAt != 0 || lr.Status != wire.RunStatusCompleted {
		t.Fatalf("unreported run %+v", lr)
	}
	env.api.mu.Lock()
	env.api.finishErr = nil
	env.api.mu.Unlock()

	// A run of a job that was then disabled.
	env.eng.block, env.eng.entered = make(chan struct{}), make(chan struct{}, 1)
	ctx3, cancel3 := context.WithCancel(context.Background())
	id3, err := env.r.Start(ctx3, 3, wire.RunTriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	<-env.eng.entered
	cancel3()
	env.r.Wait()
	env.putJob(t, 3, false)
	env.eng.block, env.eng.entered = nil, nil

	// A new process resumes.
	r2 := &Runner{Store: env.st, API: env.api, Engines: env.eng.factory, Profiles: env.r.Profiles, Device: env.r.Device,
		HeartbeatEvery: time.Millisecond, IssueFlushEvery: time.Millisecond}
	if err := r2.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	r2.Wait()
	got := env.api.snapshot()
	if got.finished[id1].Status != wire.RunStatusCompleted || got.finished[id2].Status != wire.RunStatusCompleted ||
		got.finished[id3].Status != wire.RunStatusCancelled {
		t.Fatalf("finished %+v", got.finished)
	}
	if lr := env.lastRun(t, 1); lr.ClientRunUUID != interrupted.ClientRunUUID || lr.FinishedAt == 0 || lr.RunID != id1 {
		t.Fatalf("resumed run %+v", lr)
	}
	// The unreported run keeps its snapshot.
	if lr := env.lastRun(t, 2); lr.SnapshotID != 501 || lr.SnapshotOID != strings.Repeat("a", 64) {
		t.Fatalf("unreported run lost its snapshot: %+v", lr)
	}
	for _, j := range []int64{1, 2, 3} {
		if lr := env.lastRun(t, j); lr.FinishedAt == 0 {
			t.Fatalf("job %d run unfinished: %+v", j, lr)
		}
	}
	if left, _ := env.st.UnfinishedRuns(context.Background()); len(left) != 0 {
		t.Fatalf("unfinished %+v", left)
	}
}

// TestRunnerResumeCommitted: a run that committed but stopped before its
// final status was saved is reported, not backed up again.
func TestRunnerResumeCommitted(t *testing.T) {
	env := newRunnerEnv(t)
	ctx := context.Background()
	tests := []struct {
		uuid  string
		stats RunStats
		want  string
	}{
		{"c1", RunStats{Status: wire.RunStatusCommitting, FilesDone: 3}, wire.RunStatusCompleted},
		{"c2", RunStats{Status: wire.RunStatusCommitting, Errors: 1}, wire.RunStatusPartial},
	}
	for i, tt := range tests {
		lr := Run{ClientRunUUID: tt.uuid, JobID: 1, RunID: int64(200 + i), Status: wire.RunStatusCommitting, Stats: tt.stats,
			StartedAt: int64(i + 1)}
		if err := env.st.PutRun(ctx, lr); err != nil {
			t.Fatal(err)
		}
		if err := env.st.SetRunSnapshot(ctx, tt.uuid, int64(700+i), "oid"); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.r.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	env.r.Wait()
	got := env.api.snapshot()
	if len(got.commits) != 0 || len(env.eng.params) != 0 {
		t.Fatalf("committed run backed up again: %d commits", len(got.commits))
	}
	for i, tt := range tests {
		if got.finished[int64(200+i)].Status != tt.want {
			t.Errorf("%s reported %+v, want %s", tt.uuid, got.finished[int64(200+i)], tt.want)
		}
		lr, err := env.st.Run(ctx, tt.uuid)
		if err != nil || lr.FinishedAt == 0 || lr.Status != tt.want || lr.SnapshotID != int64(700+i) {
			t.Errorf("%s local run %+v, %v", tt.uuid, lr, err)
		}
	}
}

// TestRunnerResumeLoop retries an unreported final status while the
// service runs, and leaves a job that is running now alone.
func TestRunnerResumeLoop(t *testing.T) {
	env := newRunnerEnv(t)
	env.putJob(t, 2, true)
	env.api.finishErr = &client.APIError{Status: 400, Code: "validation"}
	id, err := env.r.Start(context.Background(), 1, wire.RunTriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	env.r.Wait()

	// A live run of job 2 holds an unfinished row too.
	env.eng.block, env.eng.entered = make(chan struct{}), make(chan struct{}, 1)
	if _, err := env.r.Start(context.Background(), 2, wire.RunTriggerManual); err != nil {
		t.Fatal(err)
	}
	<-env.eng.entered
	env.api.mu.Lock()
	env.api.finishErr = nil
	env.api.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { env.r.ResumeLoop(ctx, 5*time.Millisecond); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if lr := env.lastRun(t, 1); lr.FinishedAt != 0 {
			if lr.SnapshotID != 501 || lr.Status != wire.RunStatusCompleted {
				t.Fatalf("run %+v", lr)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("final status not retried")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := env.api.snapshot(); got.finished[id].Status != wire.RunStatusCompleted {
		t.Fatalf("finished %+v", got.finished)
	}
	env.eng.mu.Lock()
	n := len(env.eng.params)
	env.eng.mu.Unlock()
	if n != 2 {
		t.Fatalf("live run resumed: %d pipeline runs", n)
	}
	cancel()
	<-done
	close(env.eng.block)
	env.r.Wait()
	if lr := env.lastRun(t, 2); lr.FinishedAt == 0 || lr.Status != wire.RunStatusCompleted || lr.SnapshotID != 502 {
		t.Fatalf("live run %+v", lr)
	}
}

func TestRunnerRetrySkipped(t *testing.T) {
	env := newRunnerEnv(t)
	ctx := context.Background()
	src, err := env.r.Start(ctx, 1, wire.RunTriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	env.r.Wait()
	id, err := env.r.RetrySkipped(ctx, wire.TaskRetrySkippedPayload{RunID: src, IssueIDs: []int64{4}})
	if err != nil {
		t.Fatal(err)
	}
	env.r.Wait()
	got := env.api.snapshot()
	if got.triggers[id] != wire.RunTriggerRetrySkipped || got.finished[id].Status != wire.RunStatusCompleted {
		t.Fatalf("retry run %v %+v", got.triggers, got.finished)
	}
	if _, err := env.r.RetrySkipped(ctx, wire.TaskRetrySkippedPayload{RunID: 999}); !errors.Is(err, ErrUnknownRun) {
		t.Fatalf("unknown run: %v", err)
	}
}

func TestSchedulerJobs(t *testing.T) {
	env := newRunnerEnv(t)
	env.putJob(t, 2, false)
	ctx := context.Background()
	jobs, err := SchedulerJobs{Store: env.st}.RunnableJobs(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].ID != 1 || !jobs[0].LastRun.Equal(time.UnixMilli(1000)) {
		t.Fatalf("jobs %+v %v", jobs, err)
	}
	if err := env.st.PutRun(ctx, Run{ClientRunUUID: "u", JobID: 1, Status: wire.RunStatusCompleted, StartedAt: 5000}); err != nil {
		t.Fatal(err)
	}
	jobs, _ = SchedulerJobs{Store: env.st}.RunnableJobs(ctx)
	if !jobs[0].LastRun.Equal(time.UnixMilli(5000)) {
		t.Fatalf("last run %v", jobs[0].LastRun)
	}
	cfg := env.r.SchedulerConfig()
	if cfg.Source == nil || cfg.Trigger == nil || cfg.Running == nil {
		t.Fatal("scheduler config incomplete")
	}
}

func TestNewRunUUID(t *testing.T) {
	a, err := NewRunUUID()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewRunUUID()
	if len(a) != 36 || a == b || a[14] != '4' {
		t.Fatalf("uuid %q %q", a, b)
	}
}

func TestErrorIssueAndTruncate(t *testing.T) {
	if is := errorIssue(&client.APIError{Status: 409, Code: wire.CodeModuleUnavailable}); is.Class != wire.IssueClassModuleUnavailable || is.Severity != wire.IssueSeverityError {
		t.Fatalf("%+v", is)
	}
	if s := truncate(strings.Repeat("é", 10), 5); s != "éé" {
		t.Fatalf("truncate %q", s)
	}
}
