# P1-26b (DSK-C/INT) Backup stack in the agent host + agent API for first run
Status: completed

## Goal
- Wire the keystore, backup.db, pipeline, runner, scheduler and device task loop into `agent.Host`, using the P1-26 recipe and the service context.
  - `Resume` runs at start; `r.Wait()` runs on shutdown.
  - `ks.Verify` runs before backups and key tasks. A `sik_mismatch` stops backups.
- Enrolment sends `box_pub` and pins `server_identity` when approved.
- Add agent API methods for P1-29 (first run and Simple screens), expose them over IPC under the §7.6 policy, and test with fakes.

## Changes (repo: path, created/modified/deleted, why)
All in VaultDock `desktop/`.

**Enrolment** lives in `internal/agent/host.go`: `Connect`, `pollEnrollment` and `completeEnrollment`. There is no separate enrolment package.

- `internal/agent/host_backup.go` (created): the backup stack.
  - **Types and constants:** `BackupOptions`, the `BackupServer` interface, `KindBackup`, the `PinState*` and `RepoState*` constants, `backupState` and `backupStack`.
  - **Lifecycle:** `openBackup` (in `NewHost`), `closeBackup` (in `Close`, after `wg.Wait`), `startBackupHost` (in `Start`; registers `backup.NewJobSource` with the module manager), `startBackup(c)`, `stopBackup`, `runBackup` and `runLoops`.
  - **Identity checks:** `verifyIdentity`, `verifiedEngines` and `verifiedKeys`.
  - **Task handling:**
    - `stackRuns`: task-started runs use the stack context, not the task context.
    - `taskAPI` / `divertTask`: `mode_change_request` runs in its own goroutine and posts its own result. A re-delivery of a task that is still waiting is dropped.
    - `consentTask`: handles `profile_consent_request` with `profiles.Manager.ApplyConsentRequest`, then sends `Report`.
  - **Enrolment helpers:** `boxPubB64` and `pinEnrolledIdentity`.
- `internal/agent/host_backup_api.go` (created): the Host API methods, `backupErr` mapping, the per-caller job visibility filter and the readiness computation.
- `internal/agent/api_backup.go` (created): API types and error codes.
- `internal/agent/host.go` (modified, minimal):
  - adds `Options.Backup` and the `Host.bk` field;
  - `NewHost` calls `openBackup`;
  - `Start` calls `startBackupHost`;
  - `Close` calls `closeBackup`;
  - `onRevoked` and `Disconnect` call `stopBackup`;
  - `restoreConnection` calls `startBackup(c)`;
  - `Connect` sends `BoxPub: h.boxPubB64()`;
  - `completeEnrollment` gains an `ident *wire.ServerIdentity` parameter (from the approved poll), calls `pinEnrolledIdentity(ident)`, then calls `startBackup(c)`.
- `internal/agent/api.go` (modified): 10 methods are added to `API`, and the reserved list is updated.
- `internal/agent/inproc.go` (modified): the InProc methods.
- `internal/agent/authz.go` (modified):
  - `ListBackupRuns`: read;
  - `RetrySkipped`: user;
  - `InitRepository` and `ConfirmServerIdentity`: admin.
  - Already classified, unchanged: `KeyStatus`, `ListBackupJobs`, `ListIssues` and `Readiness` are read; `RunBackup` is user; `ConfirmRecoveryKey` is admin.
- `internal/ipc/methods.go` and `internal/ipc/client.go` (modified): wire handlers and client methods.
- `cmd/vaultdock-agent/main.go` (modified):
  - `agentOptions` sets `Backup: &agent.BackupOptions{}` in service mode only;
  - adds `import _ "time/tzdata"` (P1-27 note).
- Tests:
  - `internal/agent/host_backup_test.go` (created);
  - cases added to `internal/agent/authz_test.go`;
  - `TestBackupMethodsOverPipe` in `internal/ipc/ipc_test.go`;
  - `cmd/vaultdock-agent/main_test.go` checks the backup flag.

## Interfaces exposed
### Agent API (P1-29 uses these exact signatures; the wire method name equals the Go name)
```go
KeyStatus(ctx) (KeyStatus, error)                                              // read
ConfirmServerIdentity(ctx, fingerprint string) (KeyStatus, error)              // admin
InitRepository(ctx, req InitRepositoryRequest) (InitRepositoryResult, error)   // admin; returns recovery text once
ConfirmRecoveryKey(ctx, text string) error                                     // admin
ListBackupJobs(ctx) ([]BackupJob, error)                                       // read (filtered per caller)
ListBackupRuns(ctx, jobID int64) ([]BackupRun, error)                          // read; newest first, max 50
ListIssues(ctx, q IssueQuery) ([]BackupIssue, error)                           // read; max 1000
RunBackup(ctx, jobID int64) (runID int64, error)                               // user ("RunJobNow")
RetrySkipped(ctx, jobID int64) (runID int64, error)                            // user
Readiness(ctx) (BackupReadiness, error)                                        // read
```

**Naming:** `RunBackup` is the reserved name that already existed in `api.go`/`authz.go`. It is the "RunJobNow" the task asked for.

**IPC params:**
- `fingerprint`, `text` and `jobID` are sent as bare JSON values.
- `InitRepositoryRequest` is `{"escrow_mode":"full|metadata"}`; empty means full.
- `IssueQuery` is `{"job_id"?, "run_uuid"?}`:
  - `run_uuid` wins;
  - otherwise `job_id` selects that job's latest run;
  - if neither is set, the result is the latest run of every visible job.

### Types (JSON names in `api_backup.go`)
- **`KeyStatus`:**
  - availability: `{available, error?, connected}`;
  - identity: `{pin_state, pinned_fingerprint, server_fingerprint}`;
  - repository: `{repo_state, repo_id?, repo_uuid?, escrow_mode?, recovery_confirmed}`.
- **`InitRepositoryResult`:** `{repo_id, repo_uuid, escrow_mode, recovery_key}`.
- **`BackupJob`:** the `wire.Job` fields, inlined, plus `{effective_state, suspended_by_mode, running, last_run}`.
- **`BackupRun`:** `{client_run_uuid, job_id, run_id, trigger, status, stats (wire.RunProgress), snapshot_id, error?, started_at, finished_at}`. The status may be the local value `skipped`.
- **`BackupIssue`:** `{id, job_id, run_uuid, path, class, severity, detail, retryable, count, first_seen, last_seen, reported}`.
- **`BackupReadiness`:** `{ready, keys (KeyStatus), jobs, runnable_jobs, running, last_run, last_success_at, open_issues, items:[{code, severity:"error|warning", message}]}`.
  - Item codes: `unavailable`, `not_connected`, `identity_unpinned`, `identity_mismatch`, `identity_unverified`, `repo_not_initialised`, `recovery_not_confirmed`, `no_jobs`, `last_run_failed`, `open_issues`.

**Constants:**
- `PinState*`: `unknown`, `unpinned`, `verified`, `mismatch`.
- `RepoState*`:
  - `unknown`;
  - `none`: first device, so call `InitRepository`;
  - `initialised`: this device created the repository;
  - `remote`: another device created it, and keys arrive by `key_share`.

**New error codes:**
- `sik_mismatch` (`CodeSIKMismatch`);
- `identity_unpinned`;
- `conflict`: a run already in progress, or a repository that already exists;
- existing codes are also used: `unavailable` (no stack, or not connected), `not_found`, `invalid` and `forbidden`.

**Event:** `KindBackup = "backup"`, whose payload is the local `KeyStatus` (no server calls). It is published when the pin or repository state changes. A pin mismatch also sends an error `KindNotification`.

### Host and options
```go
Options.Backup *BackupOptions // nil = off
type BackupOptions struct {
    KeysDir, StateDir string; Protector keystore.Protector
    Server func(*client.Client) BackupServer; Backend func(*client.Client, int64) repo.Backend
    Engines backup.EngineFactory; Profiles backup.ProfileLookup
    JobSyncEvery, RetryMin, RetryMax, TaskWait time.Duration
}
type BackupServer interface { backup.RunAPI; backup.TaskAPI; ServerIdentity; Repos; CreateRepo; Jobs } // *client.Client
```

### Behaviour
1. **`NewHost`.** `keystore.Open` uses `platform.KeysDir()` and machine DPAPI. The keystore's `Backend` hook uses the current client. Then `backup.OpenDir(platform.StateDir())` runs; the spool is `platform.SpoolDir()`. A failure makes backup unavailable (logged) and does not stop the host.
2. **On connect** (restored, or enrolment approved), `startBackup(c)` starts a stack under a child of the host context. The stack loops:
   1. `ServerIdentity` → `ks.Verify`. If it fails, wait with backoff (5 s → 5 min) or until a kick from `ConfirmServerIdentity`/`KeyStatus`.
   2. `SyncFromServer(Jobs)` mirrors the jobs.
   3. `runner.Resume(stackCtx)`.
   4. The following run until mismatch or stop:
      - `scheduler.Run`: `Trigger` uses the stack context, and `DeviceID` is the decimal device id;
      - `runner.ResumeLoop` (the new P1-24/26 helper);
      - `TaskLoop.Run`;
      - a job re-sync every 10 min, which calls `sched.Reload()` on changes.
3. **Identity checks during runs.**
   - Every run's engine factory calls `verifyIdentity` before opening the repository, and so does every `key_share` and `rewrap` task.
   - On mismatch or unpinned, the scheduler and task loop are cancelled. The current run fails before touching the repository, and `RunBackup`/`RetrySkipped` return `sik_mismatch` or `identity_unpinned`.
   - `ConfirmServerIdentity(fp)` requires `fp` to equal the server's current fingerprint (via `client.CheckSIKPin`). It then calls `PinServerIdentity` (no pin yet) or `Repin`, and the stack resumes.
4. **Disconnect, revocation and `Close`** cancel the stack and wait for its goroutines, `runner.Wait()` and the mode-request goroutines. `Close` then closes the keystore and backup.db.
5. **Enrolment.**
   - The request carries `box_pub = ks.BoxPubB64()`; it is empty when backup is off, and the field is `omitempty`.
   - On approval, `ks.PinServerIdentity(status.ServerIdentity)` runs (TOFU). An existing different pin is kept and becomes state `mismatch`. A missing identity becomes `unpinned`.
6. **`InitRepository`.**
   - It requires a verified pin.
   - It refuses with `conflict` if:
     - this device already initialised (kv `backup.repo_id`);
     - another device created a repository;
     - `repo.ErrExists`.
   - It reuses a repository this device created earlier without initialising it. Otherwise it calls `CreateRepo` with a new UUIDv4 and the name "VaultDock backup" on the built-in destination (id 0).
   - It then calls `ks.InitRepo` over the server backend and returns `RecoveryKey.Text()` once, then calls `Wipe()`.
   - A pipe caller that is not an interactive user (for example SYSTEM over the pipe) gets `forbidden`.
   - The opened repository stays in memory until `ConfirmRecoveryKey`, which otherwise reopens it with `ks.OpenRepo`.
   - kv keys in state.db: `backup.repo_id`, `backup.repo_uuid`, `backup.escrow_mode`, `backup.recovery_confirmed_at`.
7. **Visibility.**
   - In-process callers, SYSTEM and elevated admins see every job.
   - Other pipe users see only jobs whose `profile_sid` (or, when it is empty, the `active_default` profile's SID) equals their SID.
   - Hidden jobs answer `not_found`.

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `go build ./...`: ok.
- `GOOS=linux go build ./...`: ok.
- `go vet ./...`: ok. `GOOS=linux go vet ./internal/agent/... ./internal/ipc/...`: ok.
- `go test -count=1` and `-count=2` on `./internal/agent/... ./internal/ipc/... ./cmd/...`: all ok, and the backup tests also passed with `-count=3`. They use a fake `BackupServer`, `repo.MemBackend`, a fake protector, a fake engine and a fake profile lookup. They cover:
  - **No backup options:** the backup API reports unavailable.
  - **First run:** unpinned, then a wrong fingerprint refused, then a confirm with separators and case ignored, then verified. A bad escrow mode is refused, and SYSTEM is refused the recovery key. InitRepository returns `VDRK1-…` with a valid UUIDv4, and a second call gets `conflict`. Readiness then shows `recovery_not_confirmed` and `no_jobs`. A garbage recovery key is invalid; the correct key is confirmed, and confirmed again through the reopened repository. KeyStatus never contains `VDRK1`.
  - **Second device:** `repo_state=remote`, and InitRepository gets `conflict`.
  - **Runs and issues:**
    - a run, then its issues (by job, all and by run), then RetrySkipped (trigger `retry_skipped`);
    - unknown job and no-runs errors;
    - Readiness counts and items;
    - per-caller filtering (user versus admin).
  - **Identity mismatch:**
    - the run fails without opening the engine, the error notification is sent, and RunBackup gets `sik_mismatch`;
    - a `backup_now` task is not taken while mismatched;
    - Readiness and KeyStatus show both fingerprints;
    - after the re-pin, the task is handled and backups run.
  - **Task loop:**
    - a `mode_change_request` waits without blocking `backup_now` (result `{job_id, run_id}`);
    - a re-delivered request is not handled twice;
    - a deny posts `done {approved:false}`;
    - an unknown kind fails.
  - **Disconnect and Close:** Disconnect stops the stack, and Close does not hang.
  - **Enrolment:** against an httptest server, `box_pub` is sent and equals `ks.BoxPubB64()`, the approved identity is pinned and the stack verifies.
  - **Authz:** table cases for the new methods.
  - **IPC:** new methods over the pipe with `agent.Authorize` (unavailable, plus `forbidden` for the three admin calls from a standard user).
- `cd e2e && go vet ./...`: ok.
- `gofmt -l internal/agent internal/ipc cmd`: clean.
- `-race` was not run because cgo is unavailable.

## Contract issues
None new. As P1-26 reported, `POST /backup/runs/{id}/retry-skipped` is session-only, so the agent's `RetrySkipped(jobID)` starts a local `retry_skipped` run from the latest registered run, not through a server task. Whether the server links the skipped issues to a run started without a task is the server's P1-18 behaviour; INT should confirm it.

## Gaps / follow-ups
- **Service only.** Backup runs only in the service (`isSvc`). `vaultdock-agent run` in a console and the per-user desktop host report `available:false`.
- **Pin survives Disconnect.** After re-enrolling to a different server, the old pin makes the state `mismatch` until an admin calls `ConfirmServerIdentity`. This is deliberate, since pins are never overwritten silently.
- **Recovery text can be lost.** If the agent crashes or the IPC call drops after `repo.Init` but before the reply, the recovery text is lost. The repository then exists and a retry gets `conflict`. The only fix is the escrow path or a future "rotate recovery key" operation.
- **Re-claimed mode requests.** A `mode_change_request` held for more than the 10-minute lease is re-claimed by the server. Re-deliveries are dropped while one is pending, but the eventual result may be posted to a task the server re-queued (P1-28 design). A server-side lease extension would avoid this.
- **No progress events.** `KindProgress`/`KindIssue` are still not emitted for backup runs; the runner has no external progress hook. The UI should poll `ListBackupRuns`/`Readiness`, or P1-26 could add `Runner.OnProgress`.
- **Stale install mode.** `InstallMode` in `backup.Identity` is captured when the stack starts. A mode change takes effect at the next connect or restart.
- **Consent handler.** `profile_consent_request` uses `profiles.OSSystem{}` and sends the report directly (not through the outbox).
- **Not implemented.** The `restore` task and the reserved methods (`SaveBackupJob`, `DeleteBackupJob`, `CancelBackup`, `ListSnapshots`, `ResolveIssue`, restore, profiles, modules) are not implemented.

## Notes for dependent tasks / INT
- **P1-29 (UI):**
  - **Order:** connect, then `KeyStatus`. If `pin_state` is `unpinned` or `mismatch`, show `server_fingerprint` (and `pinned_fingerprint`), and on admin confirmation call `ConfirmServerIdentity(server_fingerprint)`. After enrolment the pin is normally already `verified`.
  - **Repository:** if `repo_state` is `none`, call `InitRepository({escrow_mode})`, show `recovery_key` once, never persist or log it, then call `ConfirmRecoveryKey(text)`.
  - **Simple screens:** `Readiness`, `ListBackupJobs`, `ListBackupRuns`, `ListIssues`, `RunBackup` and `RetrySkipped`.
  - **Events:** subscribe to `KindBackup`.
  - **Errors:** treat `forbidden` as "requires administrator"; `sik_mismatch` means show the identity screen.
- **Store owner (`internal/backup/store.go`), requested:**
  - `Issues(ctx, runUUID string, limit int) ([]PendingIssue-like, error)`, including reported rows, plus a count accessor. The agent currently reads `issues` with read-only SQL through `Store.DB` (`runIssues` and the count in `Readiness` in `host_backup_api.go`). Switch to the accessor once it lands.
  - `LatestRuns(ctx)`, one query instead of `Runs(job, 1)` per job.
- **Runner owner:**
  - `ResumeLoop` is used; the agent calls it under the loop context.
  - An optional `Runner.OnProgress(func(Run, RunStats))` hook would let the host emit `KindProgress`.
- **Installer:** the ACL on `%ProgramData%\VaultDock\keys` (SYSTEM and Administrators) is still required (P1-22).
- **Dependencies:** none added. `go.mod` was not touched.
