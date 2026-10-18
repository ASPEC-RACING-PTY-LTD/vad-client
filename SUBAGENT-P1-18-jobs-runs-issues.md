# P1-18 (SRV-3) Backup jobs, runs, issues, skip/retry, with module gating
Status: completed

## Goal
Implement the api-v1 §6 job, run and issue routes (rev 1.0 §6 shapes plus the rev 1.1 fields `profile_sid`, `required_modules` and `state`) in `vad-server`. Requirements:
- revision-based optimistic concurrency;
- runs that are idempotent by `client_run_uuid`;
- heartbeat and finish;
- issue dedup, list, skip and resolve;
- Retry Skipped;
- module and profile gating;
- own-device enforcement.

## Changes (repo vad-server)
| File | Change |
|---|---|
| `internal/store/backup_jobs.go` | Created. Covers job CRUD with revision checks, the module suspend/resume sweep, runs (idempotent start, heartbeat/finish, snapshot link, list) and issues (dedup report, list, skip, retryable ids, resolve, retry linking). |
| `internal/store/backup_jobs_test.go` | Created. |
| `internal/api/backup_jobs.go` | Stub replaced. Implements all 12 routes, validation, gating, views, and `applyJobModuleState` for modules.go. |
| `internal/api/backup_jobs_test.go` | Created (package `api_test`). |

No other files were touched. The new code reuses `sidPattern` from profiles.go and `listParams` from approvals.go. No dependencies were added.

## Interfaces exposed

### Routes (auth groups unchanged from the reconciled P0-03 stub)
| Route | Auth | Behaviour |
|---|---|---|
| `GET /backup/jobs?device=&cursor=&limit=` | dev, sess | Returns `List[wire.Job]` of the caller's jobs in ascending id order; the cursor is the last id. A device always gets only its own device's jobs, and `?device=<other>` gives `403 forbidden`. |
| `POST /backup/jobs` | dev, sess | Body `wire.JobCreateRequest`; reply `201 wire.Job` with revision 1 and state `active`. For a device, `device_id` defaults to its own; any other value gives 403 `forbidden`. A session must name its own device, otherwise `422 validation` (`device_id`). An unapproved device gives `409 device_not_approved`. |
| `PATCH /backup/jobs/{id}` | dev, sess | Body `wire.JobUpdateRequest`, with optional `If-Match: <rev>` (quoted or `W/` forms accepted). A mismatch gives `409 conflict` with `details.current_revision`; a malformed header gives `400 bad_if_match`. Reply `200 wire.Job`. A foreign job gives 404. |
| `DELETE /backup/jobs/{id}` | sess | Reply `204`; runs and issues are deleted by cascade. |
| `POST /backup/jobs/{id}/runs` | dev (own device's job) | Body `wire.RunStartRequest`. A new run gets `201 {run_id}`; a replay of the same uuid gets `200 {run_id}` with no gating. An invalid uuid (1–64 chars `[A-Za-z0-9._:-]`) or an unknown trigger gives `422`. |
| `PATCH /backup/runs/{id}` | dev (own run) | Body `wire.RunProgress`, reply `204`. A terminal status finishes the run. Any heartbeat after that gives `409 run_finished`. |
| `POST /backup/runs/{id}/issues` | dev (own run) | Body `{"issues":[≤500]}`, reply `204`. Issues are deduplicated by (run, path, class). |
| `GET /backup/runs?job=&device=&status=&cursor=` | sess | Returns `List[wire.Run]`, newest first. |
| `GET /backup/runs/{id}` | sess | Returns `wire.Run`. |
| `GET /backup/runs/{id}/issues?class=&resolution=&cursor=` | sess | Returns `List[wire.Issue]` in id order. |
| `POST /backup/runs/{id}/issues/skip` | sess | Body `{"ids":[…]}` (resolution becomes `skipped`) or `{"class":…}` (open issues of that class become `skipped_all`); give exactly one, otherwise `422`. Reply `200 BulkResult`. Failure codes are `not_found` and `issue_resolved`. |
| `POST /backup/runs/{id}/retry-skipped` | sess | Reply `201 {task_id}`. It creates a `retry_skipped` device task with payload `{run_id}`, gated on the job's `required_modules`. If the run has no open, retryable, severity-`skipped` issue, the reply is `409 nothing_to_retry`. |

Validation errors use `422 validation` with `details.fields`, the same as modules.go and profiles.go. Every route that takes a resource returns 404 when the caller is not the owner, or for a device, not the owning device.

### Gating
- **`required_modules`** is computed by the server and never includes `core`:
  - `discovery` for kinds app, game, devproject, registry and inventory;
  - `vss` for `use_vss: "required"`;
  - `profiles` when `profile_sid` names a profile whose state is not `active_default`.
- **`gateJob`** runs `requireProfileAuthorised` first (`423 profile_blacklisted` or `403 profile_not_authorised`), then `requireModules` (`409 module_unavailable`).
- **The gate runs on:**
  - create (always);
  - a PATCH that leaves the job enabled when it was disabled, when its modules or `profile_sid` changed, or when it is not `active`. Passing the gate sets state `active`;
  - the start of a new run (not a replay);
  - retry-skipped.
- Disabling a job never needs the gate.
- Schedules are validated with `schedule.Schedule(js).Validate()`. An empty `type` defaults to `manual`, an empty `mode` to `full`, and an empty `use_vss` to `auto`.

### Retry Skipped flow
1. The session calls `retry-skipped`, which creates the device task `retry_skipped {run_id}`.
2. The device claims the task and starts a run with `trigger:"retry_skipped"`.
3. The server links issues to the new run (`retry_run_id`, resolution `retried`):
   - The source is the device's newest claimed or done `retry_skipped` task whose run belongs to the same job. Only the open, retryable, severity-`skipped` issues of that run are linked, limited to the task's `issue_ids` when it has them.
   - Without such a task, eligible issues from every earlier run of the job are linked.
4. When the retry run finishes:
   - `completed` or `partial`: each linked issue becomes `resolved`, unless the retry run reported the same path and class again.
   - `failed` or `cancelled`: the linked issues go back to `open`.

### Go (package `api`)
```go
func (s *Server) gateJob(ctx context.Context, j *store.BackupJob) error
func (s *Server) jobModules(ctx context.Context, j *store.BackupJob) ([]string, error)
func (s *Server) applyJobModuleState(ctx context.Context, deviceID int64) error // active <-> suspended_by_mode from the device's module report
```

### Store (package `store`)
```go
var ErrRevisionConflict, ErrRunFinished error
type BackupJob struct{ID, UserID, DeviceID, RepoID int64; Name, Kind, Mode string; Spec wire.JobSpec; Schedule wire.JobSchedule; RetentionPolicyID *int64; Priority int; Enabled bool; ProfileSID string; RequiredModules []string; State string; Revision, CreatedAt, UpdatedAt int64}
type JobFilter struct{UserID, DeviceID, AfterID int64; Limit int}
func (s *Store) CreateJob(ctx, *BackupJob) error
func (s *Store) Job(ctx, id) (*BackupJob, error)
func (s *Store) ListJobs(ctx, JobFilter) ([]BackupJob, error)
func (s *Store) UpdateJob(ctx, j *BackupJob, ifRevision int64) error        // ErrRevisionConflict / ErrNotFound
func (s *Store) DeleteJob(ctx, id) error
func (s *Store) ApplyJobModules(ctx, deviceID, enabled map[string]bool) (suspended, resumed []int64, err error)
func (s *Store) OwnedRef(ctx, table string, id, userID int64) (bool, error) // repositories | retention_policies
type JobRun struct{ID, JobID, UserID, DeviceID int64; ClientRunUUID, Trigger string; SnapshotID *int64; StartedAt, UpdatedAt int64; FinishedAt *int64; Skipped int64; Progress wire.RunProgress}
func RunFinished(status string) bool
func (s *Store) StartRun(ctx, *JobRun) (created bool, err error)            // idempotent; links retry issues
func (s *Store) RunByUUID(ctx, jobID int64, uuid string) (*JobRun, error)
func (s *Store) Run(ctx, id) (*JobRun, error)
func (s *Store) UpdateRun(ctx, id, wire.RunProgress) (*JobRun, error)        // ErrRunFinished
func (s *Store) SetRunSnapshot(ctx, runID, snapshotID int64) error           // for P1-15
type RunFilter struct{UserID, JobID, DeviceID int64; Status string; BeforeID int64; Limit int}
func (s *Store) ListRuns(ctx, RunFilter) ([]JobRun, error)
func (s *Store) ReportIssues(ctx, runID int64, []wire.IssueReport) error
type IssueFilter struct{RunID int64; Class, Resolution string; AfterID int64; Limit int}
func ValidIssueResolution(string) bool
func (s *Store) ListIssues(ctx, IssueFilter) ([]wire.Issue, error)
func (s *Store) SkipIssues(ctx, runID int64, ids []int64, class string) (wire.BulkResult, error)
func (s *Store) RetryableIssueIDs(ctx, runID int64, limit int) ([]int64, error)
func (s *Store) ResolveIssues(ctx, runID int64, ids []int64) (int, error)
```

Storage mapping (migration 4 is used as-is; no schema change):
- `job_runs.source` holds the trigger.
- `phase`, `queue_packs` and `eta_ms` are stored in `job_runs.summary` as JSON.
- `run_issues.message` stores `{"detail","first_seen","last_seen","count"}` as JSON.
- Resolution is derived as follows:
  - `resolved_at` set → `resolved`;
  - `skip` 1 → `skipped`;
  - `skip` 2 → `skipped_all`;
  - `retry_run_id` set → `retried`;
  - otherwise → `open`.

## Commands run and results
Run in `D:/VaultDock/vad-server`:
- `go build ./... && go vet ./internal/... && go test -count=1 ./internal/store/ ./internal/api/`: all OK (store 0.9 s, api 5.1 s; the whole api package passes).
- `gofmt -l` on the 4 owned files: clean.

What the tests cover:
- **store:**
  - CRUD and revision conflict (a stale revision changes nothing);
  - paging;
  - mode suspend and resume;
  - idempotent start, heartbeat and summary fields;
  - issue dedup (count, first and last seen, severity escalation);
  - skip by id and by class;
  - finish and the skipped count;
  - `ErrRunFinished`;
  - a retry run resolving one issue while another stays retried;
  - a failed retry putting its issues back to open.
- **api:**
  - own-device enforcement: create 403, list 403, foreign patch/start/heartbeat 404, foreign user 404/422;
  - schedule and kind validation;
  - If-Match conflict (409 `conflict`), and a session PATCH without If-Match;
  - 409 `module_unavailable`: an app job without discovery (`details.module`), `use_vss` required without vss, re-enabling after a downgrade;
  - 403 for an unconsented profile, then 423 for a blacklisted profile on create, enable and run start, with the job state becoming `suspended_by_profile`;
  - DELETE: device 403 `session_required`, foreign user 404, owner 204;
  - runs: idempotent replay returns the same id; bad trigger 422; heartbeat after finish 409 `run_finished`;
  - session views and filters, issue dedup over HTTP, skip as BulkResult;
  - Retry Skipped: device task delivered by a poll, the retry run links the issue, a second call gives `nothing_to_retry`, and the issue resolves on completion.

## Contract issues
1. The migration 4 schema lacks columns for issue `first_seen`, `last_seen`, `count` and `detail`, and for run `trigger`, `phase`, `queue_packs` and `eta_ms`. These are kept in existing columns as described under "Storage mapping". A migration (`run_issues.first_seen/last_seen/count/detail`, `job_runs.trigger/phase/...`) would be cleaner; INT decides.
2. Rev 1.0 doesn't say whether `If-Match` is mandatory. It is optional for both devices and sessions; without it the update applies to the revision the server just read, which is still race-safe through a conditional UPDATE. Mismatch → `409 conflict` with `details.current_revision`. `bad_if_match` (400) is a server-local code.
3. Unspecified status codes. I chose:
   - job POST: `201 Job`; PATCH: `200 Job`; DELETE: `204`;
   - new run: `201 {run_id}`; replay: `200 {run_id}`;
   - heartbeat and issues: `204`; skip: `200 BulkResult`; retry-skipped: `201 {task_id}`.

   Server-local error codes: `run_finished`, `nothing_to_retry`, `device_not_approved`, and `issue_resolved` (BulkFailed).
4. The rule that `profile_sid` adds the `profiles` module is my reading of §6A ("multi-profile protection"): only a profile other than the active user's (`active_default`) needs it. Rev 1.1 lists only `kind:"app"` and `use_vss` examples. I also gate game, devproject, registry and inventory kinds on discovery, per §6A ("apps, games, dev projects, registry export").
5. Rev 1.0 doesn't say how a `retry_skipped` run identifies which issues it retries; `RunStartRequest` has no run or task reference. The server infers it from the device's newest claimed or done `retry_skipped` task for the same job. See the flow above.
6. Server-side heartbeat rate limiting (≤ 1 per 2 s) is not enforced; it is treated as a client rule.
7. Session reads (runs, issues, jobs) are limited to the owner. Admin access to other users' jobs and runs isn't specified, so it isn't granted.

## Gaps / follow-ups
- **`backup.v1` is not advertised.** P1-15 calls `implementFeature(wire.FeatureBackup)` once repositories and snapshots work.
- **Mode downgrade suspension is not wired yet.** `applyJobModuleState` and `Store.ApplyJobModules` exist, but modules.go (P1-19) must call them after storing the report (see Notes).
- **Snapshot link:** `Store.SetRunSnapshot` exists, but nothing calls it yet (P1-15).
- **Manual resolve:** `ResolveIssues` exists, but no route uses it, because the contract defines none.
- **No server scheduler:** this task only validates schedules; devices schedule their own runs.

## Notes for dependent tasks / INT
- **modules.go (P1-19 / INT):** in `handlePutDeviceModules`, after `PutDeviceModules` succeeds, call `if err := s.applyJobModuleState(r.Context(), d.ID); err != nil { … }`. This implements "a device downgrading its mode moves affected jobs to `suspended_by_mode`; they are never deleted", and resumes them when the modules come back.
- **P1-15 (snapshot commit):**
  - Advertise `backup.v1` once repositories and snapshots work.
  - After commit, call `s.st.SetRunSnapshot(ctx, runID, snapshotID)` when the commit carries a run id or job.
  - Use `s.gateJob` or `requireProfileConsent` for profile checks.
- **Client (desktop):**
  - When the client receives a `retry_skipped {run_id, issue_ids?}` task, it must start a run of the same job with `trigger:"retry_skipped"` and then report the task result.
  - Devices cannot GET runs or issues (sess routes), so the client keeps its own local record of the paths it skipped.
  - `sdk/client.RetrySkipped` calls a sess-only route, so a device signature gets `403 session_required`, as the P1-09 report noted.
- **Summary (P0-05)** counts unfinished runs by `job_runs.status`; the stored values match the wire statuses.
- **profiles.go `setProfileJobs`** updates `backup_jobs.state` without bumping `revision`. A device holding an old revision can still PATCH after a suspension. That is harmless, because the gate re-checks the profile, but INT may want suspensions to bump the revision for consistency (`ApplyJobModules` does).
