package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/backup"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/ipc/caller"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/keystore"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/vdcrypt"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// xorProtector stands in for machine DPAPI.
type xorProtector struct{}

func (xorProtector) Protect(data, entropy []byte) ([]byte, error) {
	out := append([]byte("X"), entropy...)
	out = append(out, 0)
	for _, b := range data {
		out = append(out, b^0x33)
	}
	return out, nil
}

func (xorProtector) Unprotect(data, entropy []byte) ([]byte, error) {
	prefix := append(append([]byte("X"), entropy...), 0)
	if !bytes.HasPrefix(data, prefix) {
		return nil, errors.New("not protected")
	}
	out := make([]byte, 0, len(data)-len(prefix))
	for _, b := range data[len(prefix):] {
		out = append(out, b^0x33)
	}
	return out, nil
}

// testIdentity returns a valid server identity (SIK + signed SEK).
func testIdentity(t *testing.T) *wire.ServerIdentity {
	t.Helper()
	_, sik, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, sekPub, err := vdcrypt.GenerateX25519(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := sik.Public().(ed25519.PublicKey)
	return &wire.ServerIdentity{
		SIKPub:         base64.StdEncoding.EncodeToString(pub),
		SIKFingerprint: keystore.Fingerprint(pub),
		SEKPub:         base64.StdEncoding.EncodeToString(sekPub[:]),
		SEKVersion:     1,
		SEKSig:         base64.StdEncoding.EncodeToString(vdcrypt.SignSEK(sik, sekPub, 1)),
	}
}

type taskResult struct {
	ID     int64
	Status string
	Result json.RawMessage
}

// fakeBackupServer implements BackupServer in memory.
type fakeBackupServer struct {
	mu       sync.Mutex
	ident    *wire.ServerIdentity
	identN   int
	repos    []wire.Repo
	jobs     []wire.Job
	nextRun  int64
	starts   []wire.RunStartRequest
	finished map[int64]wire.RunProgress
	issues   map[int64][]wire.IssueReport
	tasks    []wire.DeviceTask
	results  []taskResult
	skips    []skipCall
}

type skipCall struct {
	runID int64
	req   wire.IssueSkipRequest
}

func newFakeBackupServer(t *testing.T) *fakeBackupServer {
	return &fakeBackupServer{ident: testIdentity(t), finished: map[int64]wire.RunProgress{}, issues: map[int64][]wire.IssueReport{}}
}

func (f *fakeBackupServer) identity() *wire.ServerIdentity {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ident
}

func (f *fakeBackupServer) setIdentity(id *wire.ServerIdentity) {
	f.mu.Lock()
	f.ident = id
	f.mu.Unlock()
}

func (f *fakeBackupServer) StartRun(_ context.Context, jobID int64, req wire.RunStartRequest) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextRun++
	f.starts = append(f.starts, req)
	return 100 + f.nextRun, nil
}

func (f *fakeBackupServer) RunHeartbeat(context.Context, int64, wire.RunProgress) error { return nil }

func (f *fakeBackupServer) FinishRun(_ context.Context, runID int64, p wire.RunProgress) error {
	f.mu.Lock()
	f.finished[runID] = p
	f.mu.Unlock()
	return nil
}

func (f *fakeBackupServer) ReportIssues(_ context.Context, runID int64, is []wire.IssueReport) error {
	f.mu.Lock()
	f.issues[runID] = append(f.issues[runID], is...)
	f.mu.Unlock()
	return nil
}

func (f *fakeBackupServer) CommitSnapshot(context.Context, int64, wire.SnapshotCommitRequest) (*wire.SnapshotCommitResponse, bool, error) {
	return nil, false, errors.New("fake: no commits")
}

func (f *fakeBackupServer) PollTasks(ctx context.Context, wait time.Duration) ([]wire.DeviceTask, error) {
	f.mu.Lock()
	ts := f.tasks
	f.tasks = nil
	f.mu.Unlock()
	if len(ts) > 0 {
		return ts, nil
	}
	t := time.NewTimer(10 * time.Millisecond)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.C:
		return nil, nil
	}
}

func (f *fakeBackupServer) TaskResult(_ context.Context, id int64, status string, res json.RawMessage) error {
	f.mu.Lock()
	f.results = append(f.results, taskResult{id, status, res})
	f.mu.Unlock()
	return nil
}

func (f *fakeBackupServer) FinishKeyShare(context.Context, int64, client.KeyTaskResult) error {
	return nil
}
func (f *fakeBackupServer) FinishRewrap(context.Context, int64, client.KeyTaskResult) error {
	return nil
}

func (f *fakeBackupServer) ServerIdentity(context.Context) (*wire.ServerIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.identN++
	id := *f.ident
	return &id, nil
}

func (f *fakeBackupServer) Repos(context.Context, string, int) (*wire.List[wire.Repo], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &wire.List[wire.Repo]{Items: append([]wire.Repo(nil), f.repos...)}, nil
}

func (f *fakeBackupServer) CreateRepo(_ context.Context, req wire.RepoCreateRequest) (*wire.Repo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := wire.Repo{ID: 7, RepoUUID: req.RepoUUID, Name: req.Name, EscrowMode: req.EscrowMode, CreatedByDeviceID: 3}
	f.repos = append(f.repos, r)
	return &r, nil
}

func (f *fakeBackupServer) Jobs(context.Context, string, int) (*wire.List[wire.Job], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &wire.List[wire.Job]{Items: append([]wire.Job(nil), f.jobs...)}, nil
}

func (f *fakeBackupServer) CreateJob(_ context.Context, req wire.JobCreateRequest) (*wire.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := wire.Job{ID: int64(len(f.jobs) + 1), DeviceID: req.DeviceID, RepoID: req.RepoID, Name: req.Name, Kind: req.Kind,
		Mode: req.Mode, Spec: req.Spec, Schedule: req.Schedule, Priority: req.Priority, Enabled: req.Enabled,
		ProfileSID: req.ProfileSID, Revision: 1, State: wire.JobStateActive, CreatedAt: 1000}
	f.jobs = append(f.jobs, j)
	return &j, nil
}

func (f *fakeBackupServer) UpdateJob(_ context.Context, id, revision int64, req wire.JobUpdateRequest) (*wire.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.jobs {
		j := &f.jobs[i]
		if j.ID != id {
			continue
		}
		if j.Revision != revision {
			return nil, &client.APIError{Status: 409, Code: "conflict", Message: "revision mismatch"}
		}
		if req.Name != nil {
			j.Name = *req.Name
		}
		if req.Spec != nil {
			j.Spec = *req.Spec
		}
		if req.Enabled != nil {
			j.Enabled = *req.Enabled
		}
		if req.ProfileSID != nil {
			j.ProfileSID = *req.ProfileSID
		}
		j.Revision++
		out := *j
		return &out, nil
	}
	return nil, &client.APIError{Status: 404, Code: "job_not_found", Message: "no such job"}
}

// RunIssues numbers a run's reported issues runID*1000+i.
func (f *fakeBackupServer) RunIssues(_ context.Context, runID int64, _, _ string, _ int) (*wire.List[wire.Issue], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := &wire.List[wire.Issue]{}
	for i, r := range f.issues[runID] {
		out.Items = append(out.Items, wire.Issue{ID: runID*1000 + int64(i), RunID: runID, Path: r.Path, Class: r.Class})
	}
	return out, nil
}

func (f *fakeBackupServer) SkipIssues(_ context.Context, runID int64, req wire.IssueSkipRequest) (*wire.BulkResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skips = append(f.skips, skipCall{runID, req})
	return &wire.BulkResult{OKCount: len(req.IDs)}, nil
}

func (f *fakeBackupServer) addTask(t wire.DeviceTask) {
	f.mu.Lock()
	f.tasks = append(f.tasks, t)
	f.mu.Unlock()
}

func (f *fakeBackupServer) result(id int64) (taskResult, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.results {
		if r.ID == id {
			return r, true
		}
	}
	return taskResult{}, false
}

// fakeBackupEngine records one skipped file and fails the run, so runs,
// issues and readiness can be checked without a repository.
type fakeBackupEngine struct {
	mu     sync.Mutex
	opened int
}

func (e *fakeBackupEngine) factory(context.Context, *backup.Job) (backup.Engine, error) {
	e.mu.Lock()
	e.opened++
	e.mu.Unlock()
	return e, nil
}

func (e *fakeBackupEngine) Run(_ context.Context, _ backup.Params, h backup.Hooks) (*backup.Result, error) {
	h.OnIssue(backup.Issue{Path: `C:\Users\ali\Documents\locked.pst`, Class: wire.IssueClassLocked,
		Severity: wire.IssueSeveritySkipped, Detail: "in use", Retryable: true})
	return nil, errors.New("fake pipeline stopped")
}

func (e *fakeBackupEngine) CommitRefs(context.Context, *snapshot.Root) ([]string, []string, error) {
	return nil, nil, nil
}
func (e *fakeBackupEngine) KeyID(int64, ed25519.PublicKey) string { return "" }
func (e *fakeBackupEngine) Close()                                {}

const userSID = "S-1-5-21-1-2-3-1001"

type backupEnv struct {
	h   *Host
	api *InProc
	srv *fakeBackupServer
	c   *client.Client
	eng *fakeBackupEngine
}

func backupTestOptions(t *testing.T, dir string, srv *fakeBackupServer, eng *fakeBackupEngine) Options {
	mem := repo.NewMemBackend()
	o := testOptions(dir)
	o.Backup = &BackupOptions{
		KeysDir: filepath.Join(dir, "keys"), StateDir: filepath.Join(dir, "state"), Protector: xorProtector{},
		Server:  func(*client.Client) BackupServer { return srv },
		Backend: func(*client.Client, int64) repo.Backend { return mem },
		Engines: eng.factory,
		Profiles: func(context.Context, string) (backup.ProfileInfo, error) {
			return backup.ProfileInfo{SID: userSID, Username: "ali", ProfilePath: `C:\Users\ali`, ConsentID: "c1", Allowed: true}, nil
		},
		RetryMin: 5 * time.Millisecond, RetryMax: 20 * time.Millisecond, JobSyncEvery: 20 * time.Millisecond,
	}
	return o
}

func newBackupEnv(t *testing.T) *backupEnv {
	t.Helper()
	srv, eng := newFakeBackupServer(t), &fakeBackupEngine{}
	h, err := NewHost(backupTestOptions(t, t.TempDir(), srv, eng))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	base, _ := url.Parse("http://127.0.0.1:1")
	c := client.New(base, client.Options{})
	c.DeviceID, c.Key = 3, key
	return &backupEnv{h: h, api: NewInProc(h), srv: srv, c: c, eng: eng}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (env *backupEnv) pinState() string {
	st, _ := env.h.bk.pin()
	return st
}

func TestBackupUnavailableWithoutOptions(t *testing.T) {
	h := startHost(t, t.TempDir())
	api := NewInProc(h)
	ks, err := api.KeyStatus(context.Background())
	if err != nil || ks.Available {
		t.Fatalf("KeyStatus = %+v, %v", ks, err)
	}
	if _, err := api.RunBackup(context.Background(), 1); code(err) != CodeUnavailable {
		t.Fatalf("RunBackup err = %v", err)
	}
	rd, err := api.Readiness(context.Background())
	if err != nil || rd.Ready || len(rd.Items) != 1 || rd.Items[0].Code != ReadyUnavailable {
		t.Fatalf("Readiness = %+v, %v", rd, err)
	}
}

func TestBackupFirstRun(t *testing.T) {
	env := newBackupEnv(t)
	ctx := context.Background()

	ks, err := env.api.KeyStatus(ctx)
	if err != nil || !ks.Available || ks.Connected || ks.PinState != PinStateUnpinned {
		t.Fatalf("KeyStatus before connect = %+v, %v", ks, err)
	}
	env.h.startBackup(env.c)
	waitFor(t, "unpinned state", func() bool { return env.pinState() == PinStateUnpinned })
	ks, err = env.api.KeyStatus(ctx)
	if err != nil || !ks.Connected || ks.ServerFingerprint != env.srv.identity().SIKFingerprint || ks.RepoState != RepoStateNone {
		t.Fatalf("KeyStatus connected = %+v, %v", ks, err)
	}
	if _, err := env.api.InitRepository(ctx, InitRepositoryRequest{}); code(err) != CodeIdentityUnpinned {
		t.Fatalf("InitRepository unpinned err = %v", err)
	}
	if _, err := env.api.ConfirmServerIdentity(ctx, "AAAA-BBBB"); code(err) != CodeInvalid {
		t.Fatalf("ConfirmServerIdentity wrong fp err = %v", err)
	}
	// Separators and case are ignored, as in §14 rev 1.1b.
	fp := strings.ToLower(strings.ReplaceAll(env.srv.identity().SIKFingerprint, "-", ":"))
	ks, err = env.api.ConfirmServerIdentity(ctx, fp)
	if err != nil || ks.PinState != PinStateVerified || ks.PinnedFingerprint != env.srv.identity().SIKFingerprint {
		t.Fatalf("ConfirmServerIdentity = %+v, %v", ks, err)
	}

	if _, err := env.api.InitRepository(ctx, InitRepositoryRequest{EscrowMode: "bogus"}); code(err) != CodeInvalid {
		t.Fatalf("bad escrow mode err = %v", err)
	}
	// A non-interactive pipe caller never receives the recovery key.
	svc := caller.With(ctx, caller.Caller{SID: "S-1-5-18", System: true})
	if _, err := env.h.InitRepository(svc, InitRepositoryRequest{}); code(err) != CodeForbidden {
		t.Fatalf("InitRepository as SYSTEM err = %v", err)
	}
	res, err := env.api.InitRepository(ctx, InitRepositoryRequest{EscrowMode: repo.EscrowMetadata})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.RecoveryKey, "VDRK1-") || res.RepoID != 7 || res.EscrowMode != repo.EscrowMetadata {
		t.Fatalf("InitRepository = %+v", InitRepositoryResult{RepoID: res.RepoID, RepoUUID: res.RepoUUID, EscrowMode: res.EscrowMode})
	}
	if _, err := vdcrypt.ParseRepoID(res.RepoUUID); err != nil {
		t.Fatalf("repo uuid %q: %v", res.RepoUUID, err)
	}
	if _, err := env.api.InitRepository(ctx, InitRepositoryRequest{}); code(err) != CodeConflict {
		t.Fatalf("second InitRepository err = %v", err)
	}
	ks, _ = env.api.KeyStatus(ctx)
	if ks.RepoState != RepoStateInitialised || ks.RecoveryConfirmed || ks.RepoUUID != res.RepoUUID {
		t.Fatalf("KeyStatus after init = %+v", ks)
	}
	rd, _ := env.api.Readiness(ctx)
	if !hasItem(rd, ReadyRecoveryNotConfirmed) || !hasItem(rd, ReadyNoJobs) || rd.Ready {
		t.Fatalf("Readiness after init = %+v", rd)
	}

	if err := env.api.ConfirmRecoveryKey(ctx, "VDRK1-not-a-key"); code(err) != CodeInvalid {
		t.Fatalf("ConfirmRecoveryKey garbage err = %v", err)
	}
	if err := env.api.ConfirmRecoveryKey(ctx, res.RecoveryKey); err != nil {
		t.Fatal(err)
	}
	// After a restart the repository is reopened with the box key.
	if err := env.api.ConfirmRecoveryKey(ctx, " "+res.RecoveryKey+"\n"); err != nil {
		t.Fatalf("ConfirmRecoveryKey (reopened repo): %v", err)
	}
	if ks, _ = env.api.KeyStatus(ctx); !ks.RecoveryConfirmed {
		t.Fatalf("KeyStatus after confirm = %+v", ks)
	}
	if b, _ := json.Marshal(ks); bytes.Contains(b, []byte("VDRK1")) {
		t.Fatal("KeyStatus leaks the recovery key")
	}
}

func TestBackupInitRepositorySecondDevice(t *testing.T) {
	env := newBackupEnv(t)
	ctx := context.Background()
	env.srv.repos = []wire.Repo{{ID: 9, RepoUUID: "6f1c2b9e-7a3d-4b8e-9c1a-2d3e4f5a6b7c", CreatedByDeviceID: 2, EscrowMode: "full"}}
	if _, err := env.h.bk.ks.PinServerIdentity(env.srv.identity()); err != nil {
		t.Fatal(err)
	}
	env.h.startBackup(env.c)
	waitFor(t, "verified", func() bool { return env.pinState() == PinStateVerified })
	if ks, _ := env.api.KeyStatus(ctx); ks.RepoState != RepoStateRemote || ks.RepoID != 9 {
		t.Fatalf("KeyStatus = %+v", ks)
	}
	if _, err := env.api.InitRepository(ctx, InitRepositoryRequest{}); code(err) != CodeConflict {
		t.Fatalf("InitRepository err = %v", err)
	}
}

func hasItem(rd BackupReadiness, c string) bool {
	for _, it := range rd.Items {
		if it.Code == c {
			return true
		}
	}
	return false
}

func testJob(id int64, sid string) wire.Job {
	return wire.Job{ID: id, DeviceID: 3, RepoID: 7, Name: "Docs", Kind: wire.JobKindFolder, Mode: wire.JobModeFull,
		Spec: wire.JobSpec{Paths: []string{"{DOCUMENTS}"}}, Schedule: wire.JobSchedule{Type: "manual"},
		Enabled: true, Revision: 1, State: wire.JobStateActive, ProfileSID: sid, CreatedAt: 1000}
}

// startVerified pins the server identity, adds jobs and starts the stack.
func (env *backupEnv) startVerified(t *testing.T, jobs ...wire.Job) {
	t.Helper()
	env.srv.mu.Lock()
	env.srv.jobs = jobs
	env.srv.mu.Unlock()
	if _, err := env.h.bk.ks.PinServerIdentity(env.srv.identity()); err != nil {
		t.Fatal(err)
	}
	env.h.startBackup(env.c)
	waitFor(t, "jobs synced", func() bool {
		js, _ := env.api.ListBackupJobs(context.Background())
		return len(js) == len(jobs)
	})
}

func (env *backupEnv) waitRunDone(t *testing.T, jobID int64, n int) []BackupRun {
	t.Helper()
	var runs []BackupRun
	waitFor(t, "run finished", func() bool {
		runs, _ = env.api.ListBackupRuns(context.Background(), jobID)
		return len(runs) >= n && runs[0].FinishedAt != 0
	})
	return runs
}

func TestBackupRunIssuesAndReadiness(t *testing.T) {
	env := newBackupEnv(t)
	ctx := context.Background()
	env.startVerified(t, testJob(1, ""), testJob(2, "S-1-5-21-9-9-9-500"))

	runID, err := env.api.RunBackup(ctx, 1)
	if err != nil || runID == 0 {
		t.Fatalf("RunBackup = %d, %v", runID, err)
	}
	runs := env.waitRunDone(t, 1, 1)
	if runs[0].Status != wire.RunStatusFailed || runs[0].RunID != runID || runs[0].Trigger != wire.RunTriggerManual {
		t.Fatalf("run = %+v", runs[0])
	}
	issues, err := env.api.ListIssues(ctx, IssueQuery{JobID: 1})
	if err != nil {
		t.Fatal(err)
	}
	var locked bool
	for _, is := range issues {
		locked = locked || (is.Class == wire.IssueClassLocked && is.Retryable && is.RunUUID == runs[0].ClientRunUUID)
	}
	if !locked {
		t.Fatalf("issues = %+v", issues)
	}
	if all, _ := env.api.ListIssues(ctx, IssueQuery{}); len(all) != len(issues) {
		t.Fatalf("ListIssues(all) = %d, want %d", len(all), len(issues))
	}
	if byRun, _ := env.api.ListIssues(ctx, IssueQuery{RunUUID: runs[0].ClientRunUUID}); len(byRun) != len(issues) {
		t.Fatalf("ListIssues(run) = %d", len(byRun))
	}

	retryID, err := env.api.RetrySkipped(ctx, 1)
	if err != nil || retryID == 0 || retryID == runID {
		t.Fatalf("RetrySkipped = %d, %v", retryID, err)
	}
	runs = env.waitRunDone(t, 1, 2)
	if runs[0].Trigger != wire.RunTriggerRetrySkipped {
		t.Fatalf("retry run = %+v", runs[0])
	}
	if _, err := env.api.RetrySkipped(ctx, 2); code(err) != CodeNotFound {
		t.Fatalf("RetrySkipped without runs err = %v", err)
	}
	if _, err := env.api.RunBackup(ctx, 99); code(err) != CodeNotFound {
		t.Fatalf("RunBackup unknown err = %v", err)
	}
	if _, err := env.api.ListBackupRuns(ctx, 0); code(err) != CodeInvalid {
		t.Fatalf("ListBackupRuns(0) err = %v", err)
	}

	rd, err := env.api.Readiness(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Jobs != 2 || rd.RunnableJobs != 2 || rd.LastRun == nil || rd.OpenIssues == 0 ||
		!hasItem(rd, ReadyLastRunFailed) || !hasItem(rd, ReadyOpenIssues) || !hasItem(rd, ReadyRepoNotInitialised) {
		t.Fatalf("Readiness = %+v", rd)
	}

	// A standard pipe user sees only the jobs of their own profile (job 1
	// follows the active_default profile = userSID).
	user := caller.With(ctx, caller.Caller{SID: userSID})
	jobs, err := env.h.ListBackupJobs(user)
	if err != nil || len(jobs) != 1 || jobs[0].ID != 1 || jobs[0].LastRun == nil {
		t.Fatalf("ListBackupJobs(user) = %+v, %v", jobs, err)
	}
	if _, err := env.h.RunBackup(user, 2); code(err) != CodeNotFound {
		t.Fatalf("RunBackup(other profile) err = %v", err)
	}
	if _, err := env.h.ListIssues(user, IssueQuery{JobID: 2}); code(err) != CodeNotFound {
		t.Fatalf("ListIssues(other profile) err = %v", err)
	}
	admin := caller.With(ctx, caller.Caller{SID: "S-1-5-21-1-2-3-500", Admin: true, Elevated: true})
	if jobs, _ := env.h.ListBackupJobs(admin); len(jobs) != 2 {
		t.Fatalf("ListBackupJobs(admin) = %d", len(jobs))
	}
}

func TestBackupIdentityMismatchStopsBackups(t *testing.T) {
	env := newBackupEnv(t)
	ctx := context.Background()
	env.startVerified(t, testJob(1, ""))
	waitFor(t, "verified", func() bool { return env.pinState() == PinStateVerified })

	sub, _ := env.api.Subscribe(ctx, KindNotification)
	defer sub.Close()
	other := testIdentity(t)
	env.srv.setIdentity(other)
	// The run's identity check fails: the run fails and backups stop.
	if _, err := env.api.RunBackup(ctx, 1); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "mismatch", func() bool { return env.pinState() == PinStateMismatch })
	runs := env.waitRunDone(t, 1, 1)
	if runs[0].Status != wire.RunStatusFailed {
		t.Fatalf("run = %+v", runs[0])
	}
	env.eng.mu.Lock()
	opened := env.eng.opened
	env.eng.mu.Unlock()
	if opened != 0 {
		t.Fatalf("engine opened %d times under a mismatching identity", opened)
	}
	select {
	case ev := <-sub.C():
		var n Notification
		_ = ev.Decode(&n)
		if n.Level != "error" {
			t.Fatalf("notification = %+v", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no mismatch notification")
	}
	if _, err := env.api.RunBackup(ctx, 1); code(err) != CodeSIKMismatch {
		t.Fatalf("RunBackup after mismatch err = %v", err)
	}
	// The task loop is stopped: a backup_now task is not taken.
	env.srv.addTask(wire.DeviceTask{ID: 50, Kind: wire.TaskKindBackupNow, Payload: json.RawMessage(`{"job_id":1}`)})
	time.Sleep(60 * time.Millisecond)
	if _, ok := env.srv.result(50); ok {
		t.Fatal("task handled while the identity mismatches")
	}
	rd, _ := env.api.Readiness(ctx)
	if !hasItem(rd, ReadyIdentityMismatch) || rd.Ready {
		t.Fatalf("Readiness = %+v", rd)
	}
	ks, err := env.api.KeyStatus(ctx)
	if err != nil || ks.ServerFingerprint != other.SIKFingerprint || ks.PinnedFingerprint == other.SIKFingerprint {
		t.Fatalf("KeyStatus = %+v, %v", ks, err)
	}

	// The administrator compares and re-pins: backups and tasks resume.
	if _, err := env.api.ConfirmServerIdentity(ctx, other.SIKFingerprint); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task handled after re-pin", func() bool { _, ok := env.srv.result(50); return ok })
	if r, _ := env.srv.result(50); r.Status != wire.TaskStatusDone {
		t.Fatalf("task result = %+v", r)
	}
	waitFor(t, "engine opened", func() bool { env.eng.mu.Lock(); defer env.eng.mu.Unlock(); return env.eng.opened > 0 })
}

func TestBackupTaskLoopAndModeRequest(t *testing.T) {
	env := newBackupEnv(t)
	ctx := context.Background()
	env.startVerified(t, testJob(1, ""))

	// A mode change request waits for the local administrator without
	// blocking the other tasks.
	env.srv.addTask(wire.DeviceTask{ID: 60, Kind: wire.TaskKindModeChangeRequest, Payload: json.RawMessage(`{"target_mode":"advanced","requested_by":"admin"}`)})
	m := env.h.Modules()
	if m == nil {
		t.Fatal("no module manager")
	}
	waitFor(t, "pending mode request", func() bool { return m.PendingModeRequest() != nil })
	env.srv.addTask(wire.DeviceTask{ID: 61, Kind: wire.TaskKindBackupNow, Payload: json.RawMessage(`{"job_id":1}`)})
	waitFor(t, "backup_now result", func() bool { _, ok := env.srv.result(61); return ok })
	r, _ := env.srv.result(61)
	var out struct {
		JobID int64 `json:"job_id"`
		RunID int64 `json:"run_id"`
	}
	if r.Status != wire.TaskStatusDone || json.Unmarshal(r.Result, &out) != nil || out.JobID != 1 || out.RunID == 0 {
		t.Fatalf("backup_now result = %+v", r)
	}
	if _, ok := env.srv.result(60); ok {
		t.Fatal("mode request answered before the administrator")
	}
	// The re-delivered (re-claimed) request is not handled twice.
	env.srv.addTask(wire.DeviceTask{ID: 60, Kind: wire.TaskKindModeChangeRequest, Payload: json.RawMessage(`{"target_mode":"advanced","requested_by":"admin"}`)})
	pr := m.PendingModeRequest()
	time.Sleep(40 * time.Millisecond)
	if p2 := m.PendingModeRequest(); p2 == nil || p2.ID != pr.ID {
		t.Fatal("re-delivered mode request superseded the waiting one")
	}
	if _, err := m.ConfirmModeRequest(ctx, pr.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "mode request result", func() bool { _, ok := env.srv.result(60); return ok })
	if r, _ := env.srv.result(60); r.Status != wire.TaskStatusDone || !strings.Contains(string(r.Result), `"approved":false`) {
		t.Fatalf("mode result = %+v %s", r, r.Result)
	}
	// Unknown kinds fail.
	env.srv.addTask(wire.DeviceTask{ID: 62, Kind: "verify", Payload: json.RawMessage(`{}`)})
	waitFor(t, "verify result", func() bool { _, ok := env.srv.result(62); return ok })
	if r, _ := env.srv.result(62); r.Status != wire.TaskStatusFailed {
		t.Fatalf("verify result = %+v", r)
	}
}

func TestBackupStopsOnDisconnectAndClose(t *testing.T) {
	env := newBackupEnv(t)
	env.startVerified(t, testJob(1, ""))
	if err := env.h.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if env.h.bk.current() != nil {
		t.Fatal("stack still running after Disconnect")
	}
	if _, err := env.api.RunBackup(context.Background(), 1); code(err) != CodeUnavailable {
		t.Fatalf("RunBackup after disconnect err = %v", err)
	}
	env.h.startBackup(env.c)
	done := make(chan struct{})
	go func() { _ = env.h.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung")
	}
}

// TestEnrolmentSendsBoxPubAndPinsIdentity drives Connect against a fake
// server: the enrol request carries the keystore's box_pub, and the
// identity of the approved poll is pinned.
func TestEnrolmentSendsBoxPubAndPinsIdentity(t *testing.T) {
	ident := testIdentity(t)
	var mu sync.Mutex
	var boxPub string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/device/enroll":
			var req client.EnrollRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			boxPub = req.BoxPub
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(client.EnrollResponse{RequestID: 1, PollToken: "p", UserCode: "ABCD-1234", ExpiresAt: time.Now().Add(time.Hour).Unix(), Username: "ali"})
		case r.URL.Path == "/api/v1/device/enroll/1":
			_ = json.NewEncoder(w).Encode(client.EnrollStatus{Status: "approved", DeviceID: 3, ServerIdentity: ident})
		case r.URL.Path == "/api/v1/device/me":
			_, _ = w.Write([]byte(`{"device":{"id":3,"name":"TEST-PC"},"user":{"id":1,"username":"ali"},"server":{"version":"1"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"nope"}}`))
		}
	}))
	defer ts.Close()

	srv, eng := newFakeBackupServer(t), &fakeBackupEngine{}
	srv.setIdentity(ident)
	h, err := NewHost(backupTestOptions(t, t.TempDir(), srv, eng))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	sub, _ := h.Subscribe(context.Background(), KindEnrollment)
	defer sub.Close()
	if _, err := h.Connect(context.Background(), ConnectRequest{URL: ts.URL, Login: "ali", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := boxPub
	mu.Unlock()
	if got == "" || got != h.bk.ks.BoxPubB64() {
		t.Fatalf("box_pub = %q, want %q", got, h.bk.ks.BoxPubB64())
	}
	deadline := time.After(10 * time.Second)
	for approved := false; !approved; {
		select {
		case ev := <-sub.C():
			var v EnrollView
			_ = ev.Decode(&v)
			if v.Status == "error" || v.Status == "expired" {
				t.Fatalf("enrolment ended %+v", v)
			}
			approved = v.Status == "approved"
		case <-deadline:
			t.Fatal("enrolment not approved")
		}
	}
	if h.bk.ks.Pinned() != ident.SIKFingerprint {
		t.Fatalf("pinned = %q, want %q", h.bk.ks.Pinned(), ident.SIKFingerprint)
	}
	waitFor(t, "stack verified", func() bool { st, _ := h.bk.pin(); return st == PinStateVerified && h.bk.current() != nil })
}

func (f *fakeBackupServer) DeviceRestore(context.Context, int64) (*wire.DeviceRestore, error) {
	return nil, errors.New("fake: no restores")
}

func (f *fakeBackupServer) ReportRestore(context.Context, int64, wire.RestoreProgress) (*wire.RestoreProgressReply, error) {
	return &wire.RestoreProgressReply{}, nil
}
