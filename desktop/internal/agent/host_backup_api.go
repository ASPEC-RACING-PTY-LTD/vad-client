package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/backup"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/ipc/caller"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/keystore"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/vdcrypt"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// serverCallTimeout bounds the server calls made inside API methods.
const serverCallTimeout = 20 * time.Second

// backupReady returns the backup state, or an unavailable error when this
// host has no working backup stack.
func (h *Host) backupReady() (*backupState, error) {
	if err := h.live(); err != nil {
		return nil, err
	}
	bk := h.bk
	if bk == nil {
		return nil, &Error{Code: CodeUnavailable, Message: "backup is not available in this agent"}
	}
	if bk.err != nil {
		return nil, &Error{Code: CodeUnavailable, Message: "backup is unavailable: " + bk.err.Error()}
	}
	return bk, nil
}

// connectedStack returns the running stack or an unavailable error.
func (bk *backupState) connectedStack() (*backupStack, error) {
	s := bk.current()
	if s == nil {
		return nil, &Error{Code: CodeUnavailable, Message: "this device is not connected to a VaultDock server"}
	}
	return s, nil
}

// backupErr maps backup, keystore and server errors to *Error.
func backupErr(err error) error {
	if err == nil {
		return nil
	}
	var ae *Error
	if errors.As(err, &ae) {
		return err
	}
	var mm *keystore.MismatchError
	var api *client.APIError
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return AsError(err)
	case errors.Is(err, backup.ErrNotFound), errors.Is(err, backup.ErrUnknownRun):
		return &Error{Code: CodeNotFound, Message: err.Error()}
	case errors.Is(err, backup.ErrRunBusy):
		return &Error{Code: CodeConflict, Message: "this backup is already running"}
	case errors.Is(err, backup.ErrJobNotRunnable):
		return &Error{Code: CodeInvalid, Message: "this backup is disabled or suspended"}
	case errors.Is(err, keystore.ErrNotPinned), errors.As(err, &mm), client.IsCode(err, wire.CodeSIKMismatch):
		return identityError(err)
	case errors.As(err, &api):
		return &Error{Code: CodeUnavailable, Message: api.Message}
	}
	return &Error{Code: CodeInternal, Message: err.Error()}
}

// visibleJob reports whether ctx's caller may see and act on job j: trusted
// callers (in-process, SYSTEM) and administrators see every job; another
// pipe user sees only jobs of their own profile (§7.6; an empty profile
// SID is the active_default profile).
func (h *Host) visibleJob(ctx context.Context, j *backup.Job) bool {
	c, ok := caller.From(ctx)
	if !ok || IsAdmin(c) {
		return true
	}
	sid := j.ProfileSID
	if sid == "" {
		p, err := h.profileLookup()(ctx, "")
		if err != nil {
			return false
		}
		sid = p.SID
	}
	return sid != "" && strings.EqualFold(sid, c.SID)
}

// keyStatusLocal is KeyStatus without server calls.
func (h *Host) keyStatusLocal() KeyStatus {
	bk := h.bk
	ks := KeyStatus{PinState: PinStateUnknown, RepoState: RepoStateUnknown}
	if bk == nil {
		ks.Error = "backup is not available in this agent"
		return ks
	}
	if bk.err != nil {
		ks.Error = bk.err.Error()
		return ks
	}
	ks.Available = true
	ks.Connected = bk.current() != nil
	ks.PinState, ks.ServerFingerprint = bk.pin()
	ks.PinnedFingerprint = bk.ks.Pinned()
	if ks.PinState == PinStateUnknown && ks.PinnedFingerprint == "" && !ks.Connected {
		ks.PinState = PinStateUnpinned
	}
	if id, _ := strconv.ParseInt(h.st.Get(kvRepoID), 10, 64); id > 0 {
		ks.RepoState, ks.RepoID = RepoStateInitialised, id
		ks.RepoUUID, ks.EscrowMode = h.st.Get(kvRepoUUID), h.st.Get(kvEscrowMode)
		ks.RecoveryConfirmed = h.st.Get(kvRecoveryConfirmed) != ""
	}
	return ks
}

// KeyStatus returns the server identity and repository state. When
// connected it refreshes the server fingerprint and checks whether another
// device already created the repository.
func (h *Host) KeyStatus(ctx context.Context) (KeyStatus, error) {
	if err := h.live(); err != nil {
		return KeyStatus{}, err
	}
	st := h.keyStatusLocal()
	bk := h.bk
	s := bk.current()
	if !st.Available || s == nil {
		return st, nil
	}
	ctx, cancel := context.WithTimeout(ctx, serverCallTimeout)
	defer cancel()
	if id, err := s.srv.ServerIdentity(ctx); err == nil {
		st.ServerFingerprint = id.SIKFingerprint
		if _, verr := bk.ks.Verify(id); verr == nil {
			if prev, _ := bk.pin(); prev != PinStateVerified {
				s.kickNow() // let a waiting stack start now
			}
			bk.setPin(h, PinStateVerified, id.SIKFingerprint)
		} else if errors.Is(verr, keystore.ErrNotPinned) {
			bk.setPin(h, PinStateUnpinned, id.SIKFingerprint)
		} else {
			bk.setPin(h, PinStateMismatch, id.SIKFingerprint)
			s.stopLoops()
		}
		st.PinState, _ = bk.pin()
	}
	if st.RepoState != RepoStateInitialised {
		if list, err := s.srv.Repos(ctx, "", 1); err == nil {
			st.RepoState = RepoStateNone
			if len(list.Items) > 0 {
				st.RepoState = RepoStateRemote
				st.RepoID, st.RepoUUID, st.EscrowMode = list.Items[0].ID, list.Items[0].RepoUUID, list.Items[0].EscrowMode
			}
		}
	}
	return st, nil
}

// ConfirmServerIdentity pins the server's current identity after the
// administrator compared fingerprint with the one shown out of band
// (first pin or re-pin after a mismatch, api-v1 §5A). fingerprint must
// equal the server's current SIK fingerprint; backups then resume.
func (h *Host) ConfirmServerIdentity(ctx context.Context, fingerprint string) (KeyStatus, error) {
	bk, err := h.backupReady()
	if err != nil {
		return KeyStatus{}, err
	}
	if fingerprint = strings.TrimSpace(fingerprint); fingerprint == "" || len(fingerprint) > 128 {
		return KeyStatus{}, invalidf("enter the server identity fingerprint")
	}
	s, err := bk.connectedStack()
	if err != nil {
		return KeyStatus{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, serverCallTimeout)
	defer cancel()
	id, err := s.srv.ServerIdentity(cctx)
	if err != nil {
		return KeyStatus{}, backupErr(err)
	}
	if client.CheckSIKPin(fingerprint, id) != nil {
		return KeyStatus{}, invalidf("the fingerprint does not match the server's current identity; compare it again")
	}
	if bk.ks.Pinned() == "" {
		_, err = bk.ks.PinServerIdentity(id)
	} else {
		_, err = bk.ks.Repin(id)
	}
	if err != nil {
		return KeyStatus{}, backupErr(err)
	}
	h.log.Info("server identity confirmed", "fingerprint", id.SIKFingerprint)
	bk.setPin(h, PinStateVerified, id.SIKFingerprint)
	s.kickNow()
	return h.KeyStatus(ctx)
}

// InitRepository creates and initialises the account's repository as the
// first device (backup-format §4.7) and returns the recovery key text
// once. It fails with CodeConflict when a repository already exists.
func (h *Host) InitRepository(ctx context.Context, req InitRepositoryRequest) (InitRepositoryResult, error) {
	bk, err := h.backupReady()
	if err != nil {
		return InitRepositoryResult{}, err
	}
	if c, ok := caller.From(ctx); ok && !interactiveUser(c.SID) {
		// The recovery key is only ever handed to a person at the UI.
		return InitRepositoryResult{}, forbidden("the recovery key can only be shown to an interactive user")
	}
	mode := req.EscrowMode
	if mode == "" {
		mode = repo.EscrowFull
	}
	if mode != repo.EscrowFull && mode != repo.EscrowMetadata {
		return InitRepositoryResult{}, invalidf("escrow mode must be full or metadata")
	}
	s, err := bk.connectedStack()
	if err != nil {
		return InitRepositoryResult{}, err
	}
	bk.initMu.Lock()
	defer bk.initMu.Unlock()
	if h.st.Get(kvRepoID) != "" {
		return InitRepositoryResult{}, &Error{Code: CodeConflict, Message: "the backup repository is already set up on this device"}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	id, err := s.srv.ServerIdentity(ctx)
	if err != nil {
		return InitRepositoryResult{}, backupErr(err)
	}
	if _, err := bk.ks.Verify(id); err != nil {
		return InitRepositoryResult{}, backupErr(err)
	}
	list, err := s.srv.Repos(ctx, "", 100)
	if err != nil {
		return InitRepositoryResult{}, backupErr(err)
	}
	var target *wire.Repo
	for i := range list.Items {
		r := &list.Items[i]
		if r.CreatedByDeviceID != s.c.DeviceID {
			return InitRepositoryResult{}, &Error{Code: CodeConflict,
				Message: "a backup repository already exists for this account; this device receives its keys from the server"}
		}
		target = r // an earlier attempt from this device created it
	}
	if target == nil {
		rid, err := newRepoID()
		if err != nil {
			return InitRepositoryResult{}, err
		}
		target, err = s.srv.CreateRepo(ctx, wire.RepoCreateRequest{RepoUUID: rid.String(), Name: "VaultDock backup", EscrowMode: mode})
		if err != nil {
			return InitRepositoryResult{}, backupErr(err)
		}
	}
	rid, err := vdcrypt.ParseRepoID(target.RepoUUID)
	if err != nil {
		return InitRepositoryResult{}, &Error{Code: CodeInternal, Message: "the server returned an invalid repository id"}
	}
	r, rk, err := bk.ks.InitRepo(ctx, keystore.InitParams{
		Backend: bk.backend(s.c, target.ID, h), RepoID: rid, EscrowMode: mode, DeviceID: s.c.DeviceID,
		SignPub: s.c.Key.Public().(ed25519.PublicKey), Label: h.opts.DeviceName, Server: id,
	})
	if errors.Is(err, repo.ErrExists) {
		return InitRepositoryResult{}, &Error{Code: CodeConflict, Message: "the backup repository was already initialised"}
	}
	if err != nil {
		return InitRepositoryResult{}, backupErr(err)
	}
	h.st.Set(kvRepoID, strconv.FormatInt(target.ID, 10))
	h.st.Set(kvRepoUUID, target.RepoUUID)
	h.st.Set(kvEscrowMode, mode)
	h.st.Set(kvRecoveryConfirmed, "")
	if bk.pendingRepo != nil {
		bk.pendingRepo.Close()
	}
	bk.pendingRepo = r
	text := rk.Text()
	rk.Wipe()
	h.log.Info("backup repository initialised", "repo", target.ID, "escrow_mode", mode)
	h.publishBackup()
	return InitRepositoryResult{RepoID: target.ID, RepoUUID: target.RepoUUID, EscrowMode: mode, RecoveryKey: text}, nil
}

// newRepoID returns a random lowercase UUIDv4 repository id.
func newRepoID() (vdcrypt.RepoID, error) {
	var id vdcrypt.RepoID
	if _, err := rand.Read(id[:]); err != nil {
		return id, err
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return id, nil
}

// ConfirmRecoveryKey checks that the user kept the recovery key shown by
// InitRepository (keystore.CheckRecoveryKey) and records the confirmation.
func (h *Host) ConfirmRecoveryKey(ctx context.Context, text string) error {
	bk, err := h.backupReady()
	if err != nil {
		return err
	}
	if text = strings.TrimSpace(text); text == "" || len(text) > 1024 {
		return invalidf("enter the recovery key")
	}
	repoID, _ := strconv.ParseInt(h.st.Get(kvRepoID), 10, 64)
	if repoID <= 0 {
		return invalidf("the backup repository has not been set up on this device")
	}
	bk.initMu.Lock()
	defer bk.initMu.Unlock()
	r := bk.pendingRepo
	if r == nil {
		s, err := bk.connectedStack()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(ctx, serverCallTimeout)
		defer cancel()
		if r, err = bk.ks.OpenRepo(ctx, bk.backend(s.c, repoID, h)); err != nil {
			return backupErr(err)
		}
		defer r.Close()
	}
	if err := keystore.CheckRecoveryKey(r, text); err != nil {
		if errors.Is(err, keystore.ErrRecoveryMismatch) {
			return invalidf("this recovery key does not belong to this backup repository")
		}
		return invalidf("this is not a valid VaultDock recovery key")
	}
	h.st.Set(kvRecoveryConfirmed, strconv.FormatInt(time.Now().UnixMilli(), 10))
	if bk.pendingRepo != nil {
		bk.pendingRepo.Close()
		bk.pendingRepo = nil
	}
	h.publishBackup()
	return nil
}

func runView(r backup.Run) BackupRun {
	return BackupRun{ClientRunUUID: r.ClientRunUUID, JobID: r.JobID, RunID: r.RunID, Trigger: r.Trigger, Status: r.Status,
		Stats: r.Stats, SnapshotID: r.SnapshotID, Error: r.Error, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt}
}

// visibleJobs lists the jobs ctx's caller may see.
func (h *Host) visibleJobs(ctx context.Context, bk *backupState) ([]backup.Job, error) {
	jobs, err := bk.st.Jobs(ctx)
	if err != nil {
		return nil, backupErr(err)
	}
	out := jobs[:0]
	for i := range jobs {
		if h.visibleJob(ctx, &jobs[i]) {
			out = append(out, jobs[i])
		}
	}
	return out, nil
}

// ListBackupJobs lists the mirrored backup jobs with their latest run.
func (h *Host) ListBackupJobs(ctx context.Context) ([]BackupJob, error) {
	bk, err := h.backupReady()
	if err != nil {
		return nil, err
	}
	jobs, err := h.visibleJobs(ctx, bk)
	if err != nil {
		return nil, err
	}
	s := bk.current()
	out := make([]BackupJob, 0, len(jobs))
	for i := range jobs {
		j := &jobs[i]
		v := BackupJob{Job: j.Job, EffectiveState: j.EffectiveState(), SuspendedByMode: j.SuspendedByMode}
		v.Running = s != nil && s.runner.Running(j.ID)
		if runs, err := bk.st.Runs(ctx, j.ID, 1); err == nil && len(runs) > 0 {
			rv := runView(runs[0])
			v.LastRun = &rv
		}
		out = append(out, v)
	}
	return out, nil
}

// visibleJobByID loads a job the caller may act on.
func (h *Host) visibleJobByID(ctx context.Context, bk *backupState, jobID int64) (*backup.Job, error) {
	if jobID <= 0 {
		return nil, invalidf("invalid job id")
	}
	j, err := bk.st.Job(ctx, jobID)
	if err != nil {
		return nil, backupErr(err)
	}
	if !h.visibleJob(ctx, j) {
		return nil, &Error{Code: CodeNotFound, Message: "no such backup job"}
	}
	return j, nil
}

// ListBackupRuns lists a job's latest local runs, newest first (at most 50).
func (h *Host) ListBackupRuns(ctx context.Context, jobID int64) ([]BackupRun, error) {
	bk, err := h.backupReady()
	if err != nil {
		return nil, err
	}
	if _, err := h.visibleJobByID(ctx, bk, jobID); err != nil {
		return nil, err
	}
	runs, err := bk.st.Runs(ctx, jobID, 50)
	if err != nil {
		return nil, backupErr(err)
	}
	out := make([]BackupRun, 0, len(runs))
	for _, r := range runs {
		out = append(out, runView(r))
	}
	return out, nil
}

// maxIssues caps one ListIssues reply.
const maxIssues = 1000

// ListIssues lists the recorded issues of a run (see IssueQuery).
func (h *Host) ListIssues(ctx context.Context, q IssueQuery) ([]BackupIssue, error) {
	bk, err := h.backupReady()
	if err != nil {
		return nil, err
	}
	var runs []string
	switch {
	case q.RunUUID != "":
		r, err := bk.st.Run(ctx, q.RunUUID)
		if err != nil {
			return nil, backupErr(err)
		}
		if _, err := h.visibleJobByID(ctx, bk, r.JobID); err != nil {
			return nil, err
		}
		runs = []string{r.ClientRunUUID}
	case q.JobID != 0:
		if _, err := h.visibleJobByID(ctx, bk, q.JobID); err != nil {
			return nil, err
		}
		runs, err = latestRuns(ctx, bk, []int64{q.JobID})
	default:
		jobs, jerr := h.visibleJobs(ctx, bk)
		if jerr != nil {
			return nil, jerr
		}
		ids := make([]int64, 0, len(jobs))
		for _, j := range jobs {
			ids = append(ids, j.ID)
		}
		runs, err = latestRuns(ctx, bk, ids)
	}
	if err != nil {
		return nil, backupErr(err)
	}
	out := []BackupIssue{}
	for _, uuid := range runs {
		is, err := runIssues(ctx, bk.st, uuid, maxIssues-len(out))
		if err != nil {
			return nil, backupErr(err)
		}
		out = append(out, is...)
		if len(out) >= maxIssues {
			break
		}
	}
	return out, nil
}

func latestRuns(ctx context.Context, bk *backupState, jobIDs []int64) ([]string, error) {
	var out []string
	for _, id := range jobIDs {
		runs, err := bk.st.Runs(ctx, id, 1)
		if err != nil {
			return nil, err
		}
		if len(runs) > 0 {
			out = append(out, runs[0].ClientRunUUID)
		}
	}
	return out, nil
}

// runIssues reads a run's issues from backup.db (read-only; the store has
// no list accessor yet, see the P1-26b report).
func runIssues(ctx context.Context, st *backup.Store, runUUID string, limit int) ([]BackupIssue, error) {
	rows, err := st.DB.QueryContext(ctx, `SELECT id, job_id, run_uuid, path, class, severity, detail, retryable, count,
		first_seen, last_seen, reported_at IS NOT NULL FROM issues WHERE run_uuid = ? ORDER BY id LIMIT ?`, runUUID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupIssue
	for rows.Next() {
		var is BackupIssue
		var retry int
		if err := rows.Scan(&is.ID, &is.JobID, &is.RunUUID, &is.Path, &is.Class, &is.Severity, &is.Detail, &retry, &is.Count,
			&is.FirstSeen, &is.LastSeen, &is.Reported); err != nil {
			return nil, err
		}
		is.Retryable = retry != 0
		out = append(out, is)
	}
	return out, rows.Err()
}

// runnableStack returns the stack when backups may run now.
func (h *Host) runnableStack(bk *backupState) (*backupStack, error) {
	s, err := bk.connectedStack()
	if err != nil {
		return nil, err
	}
	switch st, _ := bk.pin(); st {
	case PinStateMismatch:
		return nil, identityError(&keystore.MismatchError{})
	case PinStateUnpinned:
		return nil, identityError(keystore.ErrNotPinned)
	}
	return s, nil
}

// RunBackup starts a run of a job now (trigger manual) and returns the
// server run id; the backup continues in the background.
func (h *Host) RunBackup(ctx context.Context, jobID int64) (int64, error) {
	bk, err := h.backupReady()
	if err != nil {
		return 0, err
	}
	if _, err := h.visibleJobByID(ctx, bk, jobID); err != nil {
		return 0, err
	}
	s, err := h.runnableStack(bk)
	if err != nil {
		return 0, err
	}
	id, err := s.runner.Start(s.ctx, jobID, wire.RunTriggerManual)
	return id, backupErr(err)
}

// RetrySkipped starts a retry_skipped run of a job: a normal incremental
// run in which the files skipped by the latest registered run are read
// again. It returns the new server run id.
func (h *Host) RetrySkipped(ctx context.Context, jobID int64) (int64, error) {
	bk, err := h.backupReady()
	if err != nil {
		return 0, err
	}
	if _, err := h.visibleJobByID(ctx, bk, jobID); err != nil {
		return 0, err
	}
	s, err := h.runnableStack(bk)
	if err != nil {
		return 0, err
	}
	runs, err := bk.st.Runs(ctx, jobID, 20)
	if err != nil {
		return 0, backupErr(err)
	}
	for _, r := range runs {
		if r.RunID != 0 {
			id, err := s.runner.RetrySkipped(s.ctx, wire.TaskRetrySkippedPayload{RunID: r.RunID})
			return id, backupErr(err)
		}
	}
	return 0, &Error{Code: CodeNotFound, Message: "this backup has no earlier run to retry"}
}

// Readiness summarises whether this device is protected (first-run and
// Simple screens).
func (h *Host) Readiness(ctx context.Context) (BackupReadiness, error) {
	keys, err := h.KeyStatus(ctx)
	if err != nil {
		return BackupReadiness{}, err
	}
	rd := BackupReadiness{Keys: keys, Items: []ReadinessItem{}}
	add := func(code, sev, msg string) {
		rd.Items = append(rd.Items, ReadinessItem{Code: code, Severity: sev, Message: msg})
	}
	if !keys.Available {
		add(ReadyUnavailable, "error", "Backup is not available: "+keys.Error)
		return rd, nil
	}
	if !keys.Connected {
		add(ReadyNotConnected, "error", "This device is not connected to a VaultDock server.")
	}
	switch keys.PinState {
	case PinStateUnpinned:
		add(ReadyIdentityUnpinned, "error", "Confirm the server's identity to start backing up.")
	case PinStateMismatch:
		add(ReadyIdentityMismatch, "error", "The server's identity changed. Backups are stopped until an administrator confirms it.")
	case PinStateUnknown:
		if keys.Connected {
			add(ReadyIdentityUnverified, "warning", "The server's identity has not been checked yet.")
		}
	}
	switch keys.RepoState {
	case RepoStateNone:
		add(ReadyRepoNotInitialised, "error", "Set up the backup repository and save your recovery key.")
	case RepoStateInitialised:
		if !keys.RecoveryConfirmed {
			add(ReadyRecoveryNotConfirmed, "warning", "Confirm that you saved your recovery key.")
		}
	}
	bk := h.bk
	jobs, err := h.visibleJobs(ctx, bk)
	if err != nil {
		return BackupReadiness{}, err
	}
	s := bk.current()
	var ids []int64
	for i := range jobs {
		j := &jobs[i]
		rd.Jobs++
		ids = append(ids, j.ID)
		if j.Runnable() {
			rd.RunnableJobs++
		}
		if s != nil && s.runner.Running(j.ID) {
			rd.Running++
		}
		runs, err := bk.st.Runs(ctx, j.ID, 20)
		if err != nil {
			return BackupReadiness{}, backupErr(err)
		}
		for k, r := range runs {
			if k == 0 && (rd.LastRun == nil || r.StartedAt > rd.LastRun.StartedAt) {
				rv := runView(r)
				rd.LastRun = &rv
			}
			if (r.Status == wire.RunStatusCompleted || r.Status == wire.RunStatusPartial) && r.FinishedAt > rd.LastSuccessAt {
				rd.LastSuccessAt = r.FinishedAt
			}
		}
	}
	if rd.RunnableJobs == 0 {
		add(ReadyNoJobs, "error", "Choose the folders to back up.")
	}
	if rd.LastRun != nil && rd.LastRun.Status == wire.RunStatusFailed {
		add(ReadyLastRunFailed, "warning", "The last backup failed.")
	}
	runs, err := latestRuns(ctx, bk, ids)
	if err != nil {
		return BackupReadiness{}, backupErr(err)
	}
	for _, uuid := range runs {
		var n int
		if err := bk.st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues WHERE run_uuid = ?", uuid).Scan(&n); err != nil {
			return BackupReadiness{}, backupErr(err)
		}
		rd.OpenIssues += n
	}
	if rd.OpenIssues > 0 {
		add(ReadyOpenIssues, "warning", strconv.Itoa(rd.OpenIssues)+" items were skipped or need attention.")
	}
	rd.Ready = true
	for _, it := range rd.Items {
		if it.Severity == "error" {
			rd.Ready = false
		}
	}
	return rd, nil
}
