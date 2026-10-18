package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/backup"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/keystore"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/platform"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/profiles"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/scheduler"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/repo/backend/server"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// BackupOptions enables the backup stack in the Host (MASTER_PLAN §7.9,
// P1-26b): the keystore, backup.db, the runner, the scheduler and the
// device task loop. Options.Backup nil leaves backup off (the per-user
// desktop host and most tests).
type BackupOptions struct {
	// KeysDir holds box.key and sik_pin.json; "" = platform.KeysDir().
	KeysDir string
	// StateDir holds backup.db and (when set) the spool; "" =
	// platform.StateDir() with the spool in platform.SpoolDir().
	StateDir string
	// Protector seals the box key; nil = machine DPAPI.
	Protector keystore.Protector

	// Test hooks; nil = the real implementations.
	Server   func(c *client.Client) BackupServer               // default: c itself
	Backend  func(c *client.Client, repoID int64) repo.Backend // default: sdk/repo/backend/server
	Engines  backup.EngineFactory                              // default: RepoEngines over the pipeline
	Profiles backup.ProfileLookup                              // default: StoreProfiles(state.db)

	JobSyncEvery time.Duration // server job list refresh; 0 = 10 min
	RetryMin     time.Duration // identity check retry; 0 = 5 s
	RetryMax     time.Duration // 0 = 5 min
	TaskWait     time.Duration // device task long-poll; 0 = client.MaxTaskWait
}

// BackupServer is the server surface of the backup stack; *client.Client
// satisfies it.
type BackupServer interface {
	backup.RunAPI
	backup.TaskAPI
	ServerIdentity(ctx context.Context) (*wire.ServerIdentity, error)
	Repos(ctx context.Context, cursor string, limit int) (*wire.List[wire.Repo], error)
	CreateRepo(ctx context.Context, req wire.RepoCreateRequest) (*wire.Repo, error)
	Jobs(ctx context.Context, cursor string, limit int) (*wire.List[wire.Job], error)
	CreateJob(ctx context.Context, req wire.JobCreateRequest) (*wire.Job, error)
	UpdateJob(ctx context.Context, id, revision int64, req wire.JobUpdateRequest) (*wire.Job, error)
	RunIssues(ctx context.Context, runID int64, class, cursor string, limit int) (*wire.List[wire.Issue], error)
	SkipIssues(ctx context.Context, runID int64, req wire.IssueSkipRequest) (*wire.BulkResult, error)
	DeviceRestore(ctx context.Context, id int64) (*wire.DeviceRestore, error)
	ReportRestore(ctx context.Context, id int64, p wire.RestoreProgress) (*wire.RestoreProgressReply, error)
}

var _ BackupServer = (*client.Client)(nil)

// KindBackup carries KeyStatus (local fields only) whenever the server
// identity state or the repository state changes.
const KindBackup EventKind = "backup"

// Server identity (SIK pin) states, KeyStatus.PinState.
const (
	PinStateUnknown  = "unknown"  // not checked yet (not connected or server unreachable)
	PinStateUnpinned = "unpinned" // nothing pinned yet; backups wait for ConfirmServerIdentity
	PinStateVerified = "verified"
	PinStateMismatch = "mismatch" // backups stopped until ConfirmServerIdentity (re-pin)
)

// Repository states, KeyStatus.RepoState.
const (
	RepoStateUnknown     = "unknown"     // server not reachable
	RepoStateNone        = "none"        // first device: InitRepository is needed
	RepoStateInitialised = "initialised" // this device created the repository
	RepoStateRemote      = "remote"      // another device created it; keys arrive by key_share
)

// state.db kv keys of the first-run state.
const (
	kvRepoID            = "backup.repo_id"
	kvRepoUUID          = "backup.repo_uuid"
	kvEscrowMode        = "backup.escrow_mode"
	kvRecoveryConfirmed = "backup.recovery_confirmed_at"
)

// backupState is the Host's backup stack. ks and st live as long as the
// Host; a backupStack runs while the device is connected.
type backupState struct {
	o     BackupOptions
	spool string
	ks    *keystore.Keystore
	st    *backup.Store
	err   error // why backup is unavailable; nil when ks and st are open

	initMu      sync.Mutex // serialises InitRepository and ConfirmRecoveryKey
	pendingRepo *repo.Repo // opened by InitRepository until the key is confirmed

	mu       sync.Mutex
	stack    *backupStack
	pinState string
	serverFP string
}

// backupStack is one connected run of the backup services.
type backupStack struct {
	c      *client.Client
	srv    BackupServer
	ctx    context.Context // the service context of runs; cancelled by stopBackup
	cancel context.CancelFunc
	runner *backup.Runner
	kick   chan struct{} // re-verify the server identity now
	done   chan struct{} // closed when every goroutine of the stack has ended
	tasks  sync.WaitGroup

	mu         sync.Mutex
	loopCancel context.CancelFunc // stops scheduler + task loop; nil while stopped
	sched      *scheduler.Scheduler
	modeTasks  map[int64]bool
}

// openBackup opens the keystore and backup.db when Options.Backup is set.
// A failure leaves backup unavailable (KeyStatus/Readiness say why) but
// never fails the Host.
func (h *Host) openBackup() {
	o := h.opts.Backup
	if o == nil {
		return
	}
	bk := &backupState{o: *o, pinState: PinStateUnknown}
	h.bk = bk
	stateDir, spool := o.StateDir, filepath.Join(o.StateDir, "spool")
	if stateDir == "" {
		stateDir, spool = platform.StateDir(), platform.SpoolDir()
	}
	bk.spool = spool
	if bk.o.JobSyncEvery <= 0 {
		bk.o.JobSyncEvery = 10 * time.Minute
	}
	if bk.o.RetryMin <= 0 {
		bk.o.RetryMin = 5 * time.Second
	}
	if bk.o.RetryMax < bk.o.RetryMin {
		bk.o.RetryMax = max(5*time.Minute, bk.o.RetryMin)
	}
	ks, err := keystore.Open(keystore.Options{Dir: o.KeysDir, Protector: o.Protector, Backend: h.keyBackend, Logger: h.log})
	if err != nil {
		bk.err = fmt.Errorf("keystore: %w", err)
		h.log.Warn("backup unavailable: keystore", "err", err)
		return
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		ks.Close()
		bk.err = err
		h.log.Warn("backup unavailable: state dir", "err", err)
		return
	}
	st, err := backup.OpenDir(stateDir)
	if err != nil {
		ks.Close()
		bk.err = fmt.Errorf("backup.db: %w", err)
		h.log.Warn("backup unavailable: backup.db", "err", err)
		return
	}
	bk.ks, bk.st = ks, st
}

// closeBackup releases the keystore and backup.db after the stack stopped.
func (h *Host) closeBackup() {
	bk := h.bk
	if bk == nil || bk.err != nil {
		return
	}
	bk.initMu.Lock()
	if bk.pendingRepo != nil {
		bk.pendingRepo.Close()
		bk.pendingRepo = nil
	}
	bk.initMu.Unlock()
	bk.ks.Close()
	_ = bk.st.Close()
}

// startBackupHost registers backup jobs with the module manager so mode
// and module changes suspend and resume them (called by Start).
func (h *Host) startBackupHost() {
	if bk := h.bk; bk != nil && bk.err == nil {
		if m := h.Modules(); m != nil {
			m.RegisterSource(backup.NewJobSource(bk.st))
		}
	}
}

// BoxPubB64 returns the device box key for the enrolment request, or ""
// when backup is off.
func (h *Host) boxPubB64() string {
	if bk := h.bk; bk != nil && bk.err == nil {
		return bk.ks.BoxPubB64()
	}
	return ""
}

// pinEnrolledIdentity pins the server identity returned by the approved
// enrolment poll (TOFU, api-v1 §5A). A different existing pin is kept and
// reported as a mismatch for the administrator to confirm.
func (h *Host) pinEnrolledIdentity(id *wire.ServerIdentity) {
	bk := h.bk
	if bk == nil || bk.err != nil {
		return
	}
	if id == nil {
		h.log.Warn("enrolment approved without a server identity; backup waits for the identity to be confirmed")
		bk.setPin(h, PinStateUnpinned, "")
		return
	}
	ident, err := bk.ks.PinServerIdentity(id)
	var mm *keystore.MismatchError
	switch {
	case err == nil:
		bk.setPin(h, PinStateVerified, ident.Fingerprint)
	case errors.As(err, &mm):
		h.log.Warn("server identity differs from the pinned one", "pinned", mm.Pinned, "server", mm.Got)
		bk.setPin(h, PinStateMismatch, mm.Got)
	default:
		h.log.Warn("could not pin the server identity", "err", err)
		bk.setPin(h, PinStateUnpinned, id.SIKFingerprint)
	}
}

func (bk *backupState) server(c *client.Client) BackupServer {
	if bk.o.Server != nil {
		return bk.o.Server(c)
	}
	return c
}

func (bk *backupState) backend(c *client.Client, repoID int64, h *Host) repo.Backend {
	if bk.o.Backend != nil {
		return bk.o.Backend(c, repoID)
	}
	return server.New(c, repoID, server.Options{Logger: h.log})
}

// keyBackend is the keystore's Backend hook (key_share / rewrap tasks).
func (h *Host) keyBackend(ctx context.Context, repoID int64) (repo.Backend, error) {
	s := h.bk.current()
	if s == nil {
		return nil, errors.New("not connected to the server")
	}
	return h.bk.backend(s.c, repoID, h), nil
}

func (bk *backupState) current() *backupStack {
	if bk == nil {
		return nil
	}
	bk.mu.Lock()
	defer bk.mu.Unlock()
	return bk.stack
}

func (bk *backupState) pin() (string, string) {
	bk.mu.Lock()
	defer bk.mu.Unlock()
	return bk.pinState, bk.serverFP
}

// setPin records the identity state and publishes it when it changes.
func (bk *backupState) setPin(h *Host, state, fp string) {
	bk.mu.Lock()
	changed := bk.pinState != state || (fp != "" && bk.serverFP != fp)
	bk.pinState = state
	if fp != "" {
		bk.serverFP = fp
	}
	bk.mu.Unlock()
	if !changed {
		return
	}
	if state == PinStateMismatch {
		h.notify("VaultDock backup stopped",
			"The server's identity key does not match the one this device trusts. An administrator must confirm the new identity before backups resume.", "error")
	}
	h.publishBackup()
}

func (h *Host) publishBackup() {
	_ = h.bus.Publish(KindBackup, h.keyStatusLocal())
}

// goBackup runs f like goLoop and reports whether it started.
func (h *Host) goBackup(f func()) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.wg.Add(1)
	go func() { defer h.wg.Done(); f() }()
	return true
}

// startBackup (re)starts the backup stack for the connected client c.
func (h *Host) startBackup(c *client.Client) {
	bk := h.bk
	if bk == nil || bk.err != nil || c == nil || c.DeviceID == 0 || len(c.Key) != ed25519.PrivateKeySize {
		return
	}
	h.stopBackup()
	ctx, cancel := context.WithCancel(h.ctx)
	s := &backupStack{c: c, srv: bk.server(c), ctx: ctx, cancel: cancel, kick: make(chan struct{}, 1),
		done: make(chan struct{}), modeTasks: map[int64]bool{}}
	profs := h.profileLookup()
	host, _ := os.Hostname()
	mode := ""
	if m := h.Modules(); m != nil {
		mode = string(m.Mode())
	}
	s.runner = &backup.Runner{Store: bk.st, API: s.srv, Engines: h.verifiedEngines(s), Profiles: profs,
		Device: backup.Identity{DeviceID: c.DeviceID, DeviceName: h.opts.DeviceName, Key: c.Key, Hostname: host, InstallMode: mode},
		Log:    h.log}
	bk.mu.Lock()
	bk.stack = s
	bk.mu.Unlock()
	if !h.goBackup(func() { defer close(s.done); h.runBackup(s) }) {
		cancel()
		close(s.done)
	}
}

// profileLookup resolves a job's profile: BackupOptions.Profiles, else the
// consent records in state.db.
func (h *Host) profileLookup() backup.ProfileLookup {
	if h.bk.o.Profiles != nil {
		return h.bk.o.Profiles
	}
	return backup.StoreProfiles(&profiles.Store{DB: h.st.DB})
}

// stopBackup stops the stack (disconnect, revocation) and waits for its
// runs and loops to end.
func (h *Host) stopBackup() {
	bk := h.bk
	if bk == nil {
		return
	}
	bk.mu.Lock()
	s := bk.stack
	bk.stack = nil
	bk.mu.Unlock()
	if s == nil {
		return
	}
	s.cancel()
	<-s.done
}

func (s *backupStack) setLoop(cancel context.CancelFunc, sched *scheduler.Scheduler) {
	s.mu.Lock()
	s.loopCancel, s.sched = cancel, sched
	s.mu.Unlock()
}

// stopLoops stops the scheduler and task loop (sik_mismatch); runs in
// progress finish on their own.
func (s *backupStack) stopLoops() {
	s.mu.Lock()
	cancel := s.loopCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *backupStack) reload() {
	s.mu.Lock()
	sc := s.sched
	s.mu.Unlock()
	if sc != nil {
		sc.Reload()
	}
}

func (s *backupStack) kickNow() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// runBackup verifies the server identity, then runs the scheduler, the
// task loop and the job refresh until the identity stops verifying or the
// stack is stopped. Runs use s.ctx (the service context).
func (h *Host) runBackup(s *backupStack) {
	bk := h.bk
	defer func() {
		s.runner.Wait()
		s.tasks.Wait()
	}()
	delay := bk.o.RetryMin
	for s.ctx.Err() == nil {
		if err := h.verifyIdentity(s.ctx, s); err != nil {
			if s.ctx.Err() != nil {
				return
			}
			h.log.Debug("backup: server identity not verified", "retry_in", delay, "err", err)
			t := time.NewTimer(delay)
			select {
			case <-s.ctx.Done():
				t.Stop()
				return
			case <-s.kick:
				t.Stop()
			case <-t.C:
			}
			delay = min(delay*2, bk.o.RetryMax)
			continue
		}
		delay = bk.o.RetryMin
		h.syncJobs(s)
		if err := s.runner.Resume(s.ctx); err != nil && s.ctx.Err() == nil {
			h.log.Warn("backup: resume failed", "err", err)
		}
		h.runLoops(s)
	}
}

// runLoops runs the scheduler, the task loop and the job refresh until
// the stack stops or stopLoops is called.
func (h *Host) runLoops(s *backupStack) {
	bk := h.bk
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	cfg := s.runner.SchedulerConfig()
	cfg.Trigger = func(_ context.Context, jobID int64, trigger string) error {
		return s.runner.Trigger(s.ctx, jobID, trigger)
	}
	cfg.DeviceID = strconv.FormatInt(s.c.DeviceID, 10)
	cfg.Log = h.log
	sched := scheduler.New(cfg)
	s.setLoop(cancel, sched)
	defer s.setLoop(nil, nil)

	rj := h.restoreJobs(s)
	loop := &backup.TaskLoop{
		API:      &taskAPI{BackupServer: s.srv, divert: func(t wire.DeviceTask) bool { return h.divertTask(ctx, s, t) }},
		Backups:  stackRuns{s},
		Keys:     verifiedKeys{h: h, s: s},
		Handlers: map[string]backup.TaskHandler{wire.TaskKindProfileConsentRequest: h.consentTask(s), wire.TaskKindRestore: h.restoreTask(s, rj)},
		Wait:     bk.o.TaskWait,
		Log:      h.log,
	}
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); _ = sched.Run(ctx) }()
	go func() { defer wg.Done(); s.runner.ResumeLoop(ctx, 0) }()
	go func() { defer wg.Done(); _ = loop.Run(ctx) }()
	go func() {
		defer wg.Done()
		t := time.NewTicker(bk.o.JobSyncEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.syncJobs(s)
			}
		}
	}()
	wg.Wait()
}

// syncJobs mirrors the server job list into backup.db.
func (h *Host) syncJobs(s *backupStack) {
	res, err := h.bk.st.SyncFromServer(s.ctx, s.srv.Jobs, s.c.DeviceID)
	if err != nil {
		if s.ctx.Err() == nil {
			h.log.Warn("backup: job sync failed", "err", err)
		}
		return
	}
	if len(res.Added)+len(res.Updated)+len(res.Removed) > 0 {
		h.log.Info("backup: jobs synced", "added", len(res.Added), "updated", len(res.Updated), "removed", len(res.Removed))
		s.reload()
	}
}

// verifyIdentity fetches the server identity and checks it against the pin
// (ks.Verify). Unpinned or mismatching identities stop the loops; the
// returned error then carries the stable code.
func (h *Host) verifyIdentity(ctx context.Context, s *backupStack) error {
	bk := h.bk
	id, err := s.srv.ServerIdentity(ctx)
	if err != nil {
		return err
	}
	_, err = bk.ks.Verify(id)
	var mm *keystore.MismatchError
	switch {
	case err == nil:
		bk.setPin(h, PinStateVerified, id.SIKFingerprint)
		return nil
	case errors.Is(err, keystore.ErrNotPinned):
		bk.setPin(h, PinStateUnpinned, id.SIKFingerprint)
	case errors.As(err, &mm), client.IsCode(err, wire.CodeSIKMismatch):
		bk.setPin(h, PinStateMismatch, id.SIKFingerprint)
	default:
		// A malformed identity is treated like a mismatch: nothing is sent.
		bk.setPin(h, PinStateMismatch, id.SIKFingerprint)
	}
	s.stopLoops()
	return identityError(err)
}

func identityError(err error) error {
	if errors.Is(err, keystore.ErrNotPinned) {
		return &Error{Code: CodeIdentityUnpinned, Message: "the server identity has not been confirmed on this device yet"}
	}
	return &Error{Code: CodeSIKMismatch, Message: "the server's identity key does not match the one this device trusts; backups are stopped until an administrator confirms it"}
}

// verifiedEngines checks the server identity before every run opens its
// repository (P1-22: Verify before backups).
func (h *Host) verifiedEngines(s *backupStack) backup.EngineFactory {
	bk := h.bk
	base := bk.o.Engines
	if base == nil {
		pipe := &backup.Pipeline{Store: bk.st, SpoolDir: bk.spool, Walker: backup.Walker{Log: h.log}, Log: h.log}
		base = backup.RepoEngines(pipe, func(ctx context.Context, id int64) (*repo.Repo, error) {
			return bk.ks.OpenRepo(ctx, bk.backend(s.c, id, h))
		})
	}
	return func(ctx context.Context, j *backup.Job) (backup.Engine, error) {
		if err := h.verifyIdentity(ctx, s); err != nil {
			return nil, err
		}
		return base(ctx, j)
	}
}

// stackRuns starts task-requested runs under the stack's service context,
// not the task's, so a run outlives the task handler.
type stackRuns struct{ s *backupStack }

func (r stackRuns) Start(_ context.Context, jobID int64, trigger string) (int64, error) {
	return r.s.runner.Start(r.s.ctx, jobID, trigger)
}

func (r stackRuns) RetrySkipped(_ context.Context, p wire.TaskRetrySkippedPayload) (int64, error) {
	return r.s.runner.RetrySkipped(r.s.ctx, p)
}

// verifiedKeys verifies the server identity before key_share and rewrap.
type verifiedKeys struct {
	h *Host
	s *backupStack
}

func (k verifiedKeys) HandleTask(ctx context.Context, t wire.DeviceTask) (any, error) {
	if err := k.h.verifyIdentity(ctx, k.s); err != nil {
		return client.KeyTaskResult{Error: err.Error()}, err
	}
	return k.h.bk.ks.HandleTask(ctx, t)
}

// taskAPI diverts tasks that must not block the task loop (a
// mode_change_request waits for a local administrator) before the loop
// sees them.
type taskAPI struct {
	BackupServer
	divert func(t wire.DeviceTask) bool
}

func (a *taskAPI) PollTasks(ctx context.Context, wait time.Duration) ([]wire.DeviceTask, error) {
	ts, err := a.BackupServer.PollTasks(ctx, wait)
	if err != nil {
		return nil, err
	}
	out := ts[:0]
	for _, t := range ts {
		if !a.divert(t) {
			out = append(out, t)
		}
	}
	return out, nil
}

// divertTask handles mode_change_request in its own goroutine (it blocks
// until the local administrator answers, P1-28) and posts the result.
// A re-delivery of a task still waiting is dropped.
func (h *Host) divertTask(ctx context.Context, s *backupStack, t wire.DeviceTask) bool {
	if t.Kind != wire.TaskKindModeChangeRequest {
		return false
	}
	m := h.Modules()
	if m == nil {
		return false // the loop fails it as unsupported
	}
	s.mu.Lock()
	if s.modeTasks[t.ID] {
		s.mu.Unlock()
		return true
	}
	s.modeTasks[t.ID] = true
	s.mu.Unlock()
	s.tasks.Add(1)
	go func() {
		defer s.tasks.Done()
		defer func() { s.mu.Lock(); delete(s.modeTasks, t.ID); s.mu.Unlock() }()
		res, err := m.HandleTask(ctx, t)
		if ctx.Err() != nil {
			return // the server requeues the task when its lease ends
		}
		if perr := postTaskResult(ctx, s.srv, t.ID, res, err); perr != nil {
			h.log.Warn("backup: mode change result not posted", "task", t.ID, "err", perr)
		}
	}()
	return true
}

// postTaskResult posts done with res, or failed with {"error"}.
func postTaskResult(ctx context.Context, api backup.TaskAPI, id int64, res any, herr error) error {
	status := wire.TaskStatusDone
	if herr != nil {
		status, res = wire.TaskStatusFailed, map[string]string{"error": herr.Error()}
	}
	var raw json.RawMessage
	if res != nil {
		b, err := json.Marshal(res)
		if err != nil {
			return err
		}
		raw = b
	}
	return api.TaskResult(ctx, id, status, raw)
}

// consentTask handles profile_consent_request (P1-23): it records the
// admin request locally and sends the profile report.
func (h *Host) consentTask(s *backupStack) backup.TaskHandler {
	return func(ctx context.Context, t wire.DeviceTask) (any, error) {
		p, err := wire.DecodeTaskPayload(t.Kind, t.Payload)
		if err != nil {
			return nil, err
		}
		req, ok := p.(*wire.TaskProfileConsentRequestPayload)
		if !ok {
			return nil, fmt.Errorf("unexpected payload %T", p)
		}
		m := h.profileManager(s.c)
		if err := m.ApplyConsentRequest(ctx, *req); err != nil {
			return nil, err
		}
		if _, err := m.Report(ctx); err != nil {
			h.log.Warn("profile report not sent", "err", err)
		}
		return map[string]bool{"recorded": true}, nil
	}
}

// profileManager is the profiles manager reporting through c.
func (h *Host) profileManager(c *client.Client) *profiles.Manager {
	return &profiles.Manager{Store: &profiles.Store{DB: h.st.DB}, Sys: profiles.OSSystem{}, Tokens: h.opts.Tokens,
		DeviceID: strconv.FormatInt(c.DeviceID, 10), SendReport: c.ReportProfiles, Log: h.log}
}
