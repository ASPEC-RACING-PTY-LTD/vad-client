# P1-26 (DSK-C) Commit, runs, issues, Retry Skipped, device-task loop
Status: completed

## Goal
Add the run layer on top of the P1-25 pipeline in `desktop/internal/backup`:
- **Runner.** One run per job at a time. It registers the server run, runs the pipeline and commits the snapshot (api-v1 §5.2).
- **Reporting.** Progress heartbeats, batched issue upload and the final status.
- **Profile states.** A profile that needs logon gives a skipped run; an unconsented profile gives a failed run.
- **Resume.** Unfinished runs are resumed at start.
- **Retry Skipped.**
- **Device-task loop.** `backup_now`, `retry_skipped`, `key_share`, `rewrap`, pluggable extra kinds, and failure for unknown kinds.
- **Scheduler adapter** for P1-27.

## Changes (repo: path, created/modified/deleted, why)
All in VaultDock, `desktop/internal/backup/`, all created. No other file was touched; store, walker and pipeline files were not edited.
- `runner.go`:
  - `Runner`: `Start`, `Trigger`, `Resume`, `Running` and `Wait`.
  - Run execution, commit, final status and `finalize`.
  - The `RunAPI`, `Engine`, `EngineFactory`, `Hooks`, `ProfileLookup`/`ProfileInfo` and `Identity` interfaces, and `StoreProfiles`.
  - `retry`: a bounded retry for transient failures.
- `report.go`:
  - `reporter`: heartbeat and issue-flush goroutine.
  - `commitRequest`.
  - `snapshotPacks`: walks the trees to collect every pack the snapshot needs.
  - `RepoEngine` and `RepoEngines`: the real `Engine` over `Pipeline` and `repo.Repo`.
  - `deviceKeyID`.
- `issues.go`: `addIssue`, `flushIssues` (batched, then `MarkIssuesReported`), `errorIssue` (maps run-level errors to classes), `RetrySkipped`, `runJob`.
- `runner_scheduler.go`: `SchedulerJobs` (a `scheduler.JobSource`) and `Runner.SchedulerConfig()`.
- `tasks.go`: `TaskLoop`, plus the `TaskAPI`, `KeyTasks`, `BackupTasks` and `TaskHandler` interfaces.
- Tests: `runner_test.go`, `report_test.go`, `tasks_test.go`.

## Interfaces exposed
```go
const RunStatusSkipped = "skipped"   // local only; sent to the server as "cancelled"
const PhaseCommit = "commit"
const DefaultHeartbeatEvery = 2*time.Second; DefaultIssueFlushEvery = 10*time.Second
const DefaultTaskTimeout = 8*time.Minute; DefaultMinBackoff = time.Second; DefaultMaxBackoff = time.Minute
var ErrRunBusy, ErrJobNotRunnable, ErrUnknownRun error

type RunAPI interface { // *client.Client satisfies it
    StartRun(ctx, jobID int64, wire.RunStartRequest) (int64, error)
    RunHeartbeat(ctx, runID int64, wire.RunProgress) error
    FinishRun(ctx, runID int64, wire.RunProgress) error
    ReportIssues(ctx, runID int64, []wire.IssueReport) error
    CommitSnapshot(ctx, repoID int64, wire.SnapshotCommitRequest) (*wire.SnapshotCommitResponse, bool, error)
}
type Hooks struct{ Progress func(RunStats); OnIssue func(Issue) }
type Engine interface {
    Run(ctx, Params, Hooks) (*Result, error)
    CommitRefs(ctx, *snapshot.Root) (indexOIDs, packOIDs []string, err error)
    KeyID(deviceID int64, signPub ed25519.PublicKey) string
    Close()
}
type EngineFactory func(ctx context.Context, job *Job) (Engine, error)
func RepoEngines(base *Pipeline, open func(ctx context.Context, repoID int64) (*repo.Repo, error)) EngineFactory
type RepoEngine struct{ Pipeline *Pipeline } // implements Engine

type ProfileInfo struct{ SID, Username, ProfilePath, ConsentID string; Allowed bool }
type ProfileLookup func(ctx context.Context, sid string) (ProfileInfo, error) // sid "" = the active_default profile
func StoreProfiles(st *profiles.Store) ProfileLookup
type Identity struct{ DeviceID int64; DeviceName string; Key ed25519.PrivateKey; Hostname, InstallMode string }

type Runner struct {
    Store *Store; API RunAPI; Engines EngineFactory; Profiles ProfileLookup; Device Identity
    HeartbeatEvery, IssueFlushEvery time.Duration; Log *slog.Logger; Now func() time.Time
}
func (r *Runner) Start(ctx, jobID int64, trigger string) (runID int64, err error) // returns once the server run exists; the backup runs in the background
func (r *Runner) Trigger(ctx, jobID int64, trigger string) error                 // scheduler.TriggerFunc; returns at once
func (r *Runner) Resume(ctx) error
func (r *Runner) RetrySkipped(ctx, wire.TaskRetrySkippedPayload) (runID int64, err error)
func (r *Runner) Running(jobID int64) bool
func (r *Runner) Wait()
func (r *Runner) SchedulerConfig() scheduler.Config // Source, Trigger, Running set
type SchedulerJobs struct{ Store *Store }           // scheduler.JobSource
func NewRunUUID() (string, error)

type TaskAPI interface { PollTasks; TaskResult; FinishKeyShare; FinishRewrap } // *client.Client
type KeyTasks interface{ HandleTask(ctx, wire.DeviceTask) (any, error) }     // *keystore.Keystore
type BackupTasks interface{ Start(...); RetrySkipped(...) }                 // *Runner
type TaskHandler func(ctx context.Context, t wire.DeviceTask) (any, error)
type TaskLoop struct{ API TaskAPI; Backups BackupTasks; Keys KeyTasks; Handlers map[string]TaskHandler
    Wait, TaskTimeout, MinBackoff, MaxBackoff time.Duration; Log *slog.Logger }
func (l *TaskLoop) Run(ctx) error           // returns ctx.Err()
func (l *TaskLoop) Handle(ctx, wire.DeviceTask)
```

### Run semantics
1. **Start.**
   - The job must exist and be `Runnable()`; otherwise `ErrNotFound` or `ErrJobNotRunnable`. A job that is already running gives `ErrRunBusy`.
   - A local run is written with a random UUID and status `queued`.
   - `StartRun` is called; it is idempotent by `client_run_uuid`.
   - If registration fails, the local run is finished as `failed`. For example, a `409 module_unavailable` reaches the caller.
2. **Profile.** The profile comes from `job.profile_sid`, or from the `active_default` profile when that is empty. If it is not `Allowed`, the run reports a `profile_unauthorised` issue (severity error), finishes `failed`, and never opens the repo.
3. **Pipeline.**
   - Runs of jobs on the same repo are serialised: they share the spool, the pending index and the stray sweep.
   - The engine is opened per run, so a rewrap or rotation between runs is always seen.
   - `Params` come from `SpecFor` with `spec.ProfileSID` set to the resolved SID. `Root` carries:
     - `Parent`: the oid of the last snapshot this process committed for the job;
     - `Job`;
     - `Device{ID, Name, KeyID}`, where `KeyID` is the newest device key file whose `sign_pub` is our key;
     - `Host{Hostname, GOOS, User, SID}`;
     - `Profile{SID, Username, ConsentID}`;
     - `InstallMode`.
4. **Commit.** Uses `CommitRefs`:
   - `pack_oids` is every pack the trees reference: tree blobs, data chunks, ADS chunks, continuation trees and the inventory. Shared subtrees are read once. This includes packs uploaded by earlier runs, as rev 1.0 §5.2 requires for GC.
   - `index_oids` is `Repo.Index().Files()`.
   - `parent_snapshot_id` is the newest local run of the job with a snapshot id.
   - Other fields: `NewObjects = len(Result.Packs)` and `ClientTime = root.Time`.
   - Commit, finish and task results are retried up to 3 times on network errors, 429 and 5xx, honouring Retry-After.
5. **Status.**
   - `partial` when any issue's severity is not `warning`, or the walk was incomplete. Otherwise `completed`.
   - `ErrProfileNeedsLogon` gives local `skipped`, sent as wire `cancelled`.
   - Any other error gives `failed`, plus one run-level issue: `server`, `module_unavailable` or `profile_unauthorised` for API errors, `network` for `net.Error`, `other` otherwise.
6. **Reporting.**
   - Pipeline progress is kept non-blocking.
   - Every `HeartbeatEvery` (2 s), the changed stats are stored with `UpdateRunStats` and sent with `RunHeartbeat`.
   - Every `IssueFlushEvery`, issues are uploaded. `OnIssue` writes them with `AddIssue`; `PendingIssues` takes at most 500; then `.Report()` (the Rev 1.1b mapping), `ReportIssues` and `MarkIssuesReported`.
7. **Finalize.**
   - The local terminal status is written first (`UpdateRunStats`).
   - Then all pending issues are flushed. They go **before** `FinishRun`, because the server resolves retried issues at finish.
   - Then `FinishRun` is sent; a 409 `run_finished` is accepted. Then the local `FinishRun` with the snapshot id.
   - If the server cannot be reached, the local run keeps its terminal status but stays unfinished.
8. **Interrupted** (ctx cancelled): no final status is sent. The run stays unfinished locally with its last stats.
9. **`Resume`.**
   - Unfinished runs with a terminal status are finalized.
   - Runs of removed or non-runnable jobs are finished as `cancelled`.
   - Other runs are re-registered under the same UUID (the server replays the run id) and executed again. The pipeline uploads the spooled packs first.

### Device tasks
- **Polling.**
  - `PollTasks(wait = 25 s)` claims tasks under the server's 10-minute lease.
  - Poll errors back off exponentially from 1 s to 1 min, honouring Retry-After.
  - Tasks are handled in order. A per-task timeout of 8 min keeps each handler inside the lease.
- **`backup_now {job_id}`:**
  - calls `Runner.Start(ctx, job, "task")`;
  - is reported `done {"job_id","run_id"}` as soon as the server run exists, so a long backup never holds the lease;
  - when the job is already running: `done {"job_id","already_running":true}`.
- **`retry_skipped {run_id, issue_ids?}`:** calls `Runner.RetrySkipped`, then reports `done {"run_id"}`. An unknown run is `failed`.
- **`key_share` / `rewrap`:**
  - calls `Keys.HandleTask`;
  - its `client.KeyTaskResult` goes to `FinishKeyShare` / `FinishRewrap`;
  - if `err` is set and the result has no `Error`, the error text is copied into the result;
  - without a keystore, the task fails through the same call.
- **Other kinds:** handled by `Handlers[kind]` when one is registered. Otherwise the task is `failed {"error":"unsupported task kind \"x\""}`. Bad payloads (`wire.DecodeTaskPayload`) are `failed`.
- **Shutdown:** a cancelled ctx stops the loop, and no result is posted for an interrupted task; the server requeues it when the lease ends.

### Scheduler adapter
- `SchedulerJobs.RunnableJobs` returns the jobs where `Runnable()` holds.
- Each job's schedule is `schedule.Schedule(j.Schedule)`.
- `LastRun` is the start of the newest local run, or else the job's `created_at`.
- `Runner.SchedulerConfig()` fills `Source`, `Trigger` and `Running`.

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `go build ./...`: ok
- `go vet ./internal/backup/...`: ok
- `GOOS=linux go vet ./internal/backup/...`: ok
- `GOOS=linux go test -c ./internal/backup/`: compiles
- `go test -count=1 ./internal/backup/...`: ok, and all P1-24/P1-25 tests still pass. The new tests use fake `RunAPI`, `Engine`, `TaskAPI`, keys and backups, plus the real `Store`:
  - commit field mapping, then parent oid and parent id on the next run;
  - 503 issues uploaded in batches of ≤500 before `finish:partial`, with the EFS issue mapped to `access_denied`;
  - an outcome table: needs logon (cancelled/skipped), pipeline error, `pack_missing`, `profile_blacklisted`, unconsented profile (engine never opened), incomplete walk (partial), warnings only (completed);
  - busy, not runnable and unknown jobs;
  - a rejected start (`module_unavailable`);
  - heartbeats;
  - `Resume`: an interrupted run, an unreported final status, and a disabled job;
  - Retry Skipped, including an unknown run;
  - the scheduler source;
  - the UUID format;
  - error classes and truncation;
  - `snapshotPacks` (shared subtree read once, continuation, ADS, inventory, missing blob, bad id);
  - `deviceKeyID`;
  - the full task-loop dispatch, including a 503 poll backoff, a custom handler, busy, no keystore, and no result posted after shutdown.
  - `TestRepoEngineCommitRefs` runs the **real** pipeline through `RepoEngine` (using the P1-25 `pipeFixture`). It checks that the second run's `pack_oids` include the first run's packs and that every listed pack is stored.
- `gofmt -l internal/backup`: clean. `-race` was not run because cgo is unavailable.

## Contract issues
1. **There is no wire run status "skipped".** A run that skips because the profile needs logon is sent as `cancelled`. It carries the walker's `profile_needs_logon` issue (class `other`, per Rev 1.1b), and the local status is `skipped` (`RunStatusSkipped`), which the UI can show.
2. **The scope of a Retry Skipped run is not defined.** The device cannot read issues (session-only routes) or map server `issue_ids` to local rows. The retry is therefore a normal incremental run with `trigger:"retry_skipped"`:
   - Only the files skipped earlier (which have no file-cache entry) and changed files are read.
   - The server links and resolves the issues (P1-18 flow).

   It is deliberately not narrowed to the skipped paths. A narrowed walk would let the pipeline prune the job's whole file cache (it prunes on any complete walk), and it would produce a near-empty snapshot that becomes the next parent. Narrowing would need a pipeline option such as `Params.NoPrune` plus a path filter.
3. **Task results** for `backup_now` and `retry_skipped` are not defined by the contract. The shapes used are `{"job_id","run_id"}`, `{"job_id","already_running":true}` and `{"error"}`.
4. **`index_oids`** is sent as every index object the repo has loaded, which is a superset of what is needed. The contract says only "index_oids", and the in-memory index has no pack→index-file map. The list grows with the repo until compaction.

## Gaps / follow-ups
- **Parent oid.** `snapshot.Root.Parent` (an oid) survives only in memory. After a restart, the first run's root has `parent: null`. `parent_snapshot_id` survives because it comes from `runs.snapshot_id`. Adding a `snapshot_oid` column to `runs` (store owner) would fix this.
- **Lost snapshot id.** When the final status could not be reported, `Resume` finishes the run with snapshot id 0. The commit succeeded, but the local id is lost, so the next run's `parent_snapshot_id` skips it. The same column, or a `SetRunSnapshot` accessor, would fix this.
- **Unreported final status.** If `FinishRun` fails, the local run stays unfinished until the next `Resume`, which only runs at service start. INT may also want to call `Resume` periodically.
- **Commit cost.** `CommitRefs` re-reads every tree blob, which is one ranged GET per directory. It is correct, but costs one request per directory per run. A pipeline `Result.ReferencedPacks`, collected while building trees and from the cached chunk lists, would make it free.
- **Run concurrency.** The per-repo serialisation means jobs on the same repo queue behind each other. `Trigger` returns at once, so the scheduler still sees the job as running.
- **Task handling.** `backup_now` uses trigger `task`. Tasks are handled one at a time. Only the long-running work (backups) is asynchronous.

## Notes for dependent tasks / INT
**Service wiring (agent host).** Build the components in this order:
1. The pipeline base:
   ```go
   base := &backup.Pipeline{Store: st, SpoolDir: filepath.Join(platform.StateDir(), "spool"), Walker: backup.Walker{}}
   ```
2. The engine factory:
   ```go
   engines := backup.RepoEngines(base, func(ctx context.Context, id int64) (*repo.Repo, error) {
       return ks.OpenRepo(ctx, server.New(c, id, server.Options{Logger: log}))
   })
   ```
3. The runner:
   ```go
   r := &backup.Runner{Store: st, API: c, Engines: engines, Profiles: backup.StoreProfiles(profStore),
       Device: backup.Identity{DeviceID, DeviceName, Key: deviceKey, Hostname, InstallMode}, Log: log}
   ```
4. At start, call `r.Resume(ctx)`.
5. The scheduler: `cfg := r.SchedulerConfig()`, set `cfg.DeviceID`, `cfg.Loc` and `cfg.Log`, then `go scheduler.New(cfg).Run(ctx)`.
6. The task loop: `go (&backup.TaskLoop{API: c, Backups: r, Keys: ks, Handlers: {...}}).Run(ctx)`.
   - Register the `profile_consent_request`, `mode_change_request` and `restore` handlers there, from their own packages.
7. On shutdown: cancel `ctx`, then call `r.Wait()`.

`ctx` must be the service context, because background runs use it.

Before key operations and backups, the host should call `ks.Verify(serverIdentity)`, as the P1-22 notes say.

**Store owner (P1-24), optional:**
- `runs.snapshot_oid`, or `SetRunSnapshot(uuid, id, oid)`;
- `RunByServerID(runID)`. `issues.go` currently queries `runs.run_id` directly through `Store.DB`.

**Pipeline owner (P1-25), optional:**
- `Result.ReferencedPacks`, to avoid re-reading trees at commit;
- `Params.NoPrune` and a path filter, if narrowed Retry Skipped runs are wanted.

**UI (P1-29):**
- The local run status may be `skipped`.
- Issues for display are in `backup.db`'s `issues` table, keyed by `client_run_uuid`.

**Tests:** `TestRepoEngineCommitRefs` depends on `pipeFixture` in `pipeline_test.go`. If that fixture changes, update this test.

**Dependencies:** none added.
