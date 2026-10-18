# P1-21 Readiness + backup-state
Status: completed

## Goal
Implement api-v1 §11 readiness (`GET /readiness`) and `PUT /device/backup-state` (shapes per rev 1.0 §11, plus the rev 1.1 checks `profile_authorised`, `install_mode` and `modules_healthy`), replacing the P0-03 501 stubs.

## Changes (repo: path, created/modified/deleted, why)
- vad-server: `internal/store/readiness.go` (created): backup-state upsert/read (`device_backup_state`, migration 4, unchanged) and readiness fact queries per device and job.
- vad-server: `internal/api/readiness.go` (modified; it was the P0-03 stub): real handlers on the same auth groups, plus check evaluation and scoring.
- vad-server: `internal/api/readiness_test.go` (created): end-to-end test using the `api_test.go` env helpers.
- vad-server: `internal/api/readiness_internal_test.go` (created): table tests for schedule freshness and scoring.
- No migration. No other files touched. No new dependencies (new imports are `sdk/schedule` and `sdk/wire`, both already required).

## Interfaces exposed
Routes:
- `PUT /device/backup-state` (dev): body `wire.BackupState`, strict JSON (unknown field gives `400 bad_json`). Replies `204`. Each report replaces the previous one.
- `GET /readiness` (sess): replies `wire.ReadinessReport`.
  - No params: the caller's approved devices.
  - `?device=<id>`: that device, for its owner or an admin. Anyone else gets `404 not_found`. A bad id gives `422 validation` (`fields.device`).
  - `?scope=all`: every approved device of every user. Admin only; others get `403 forbidden`. `scope` other than `mine|all` gives 422.
  - `?device` takes precedence over `scope`.
  - Admin callers get one extra check per device, `server_key_custody`: `critical` with fix_link `/admin` when `s.Keys()` is nil (degraded custody, no KEK), otherwise `ok`.

Checks:

| Check | How it is evaluated |
|---|---|
| `recent_backup` | The worst result over enabled and `active` jobs, judged from the last success against the schedule (`sdk/schedule.Next`, in the job's timezone, UTC fallback). Missing at most one scheduled run is `ok`. Missing two or more is `warning`, or `critical` once the last success is more than 7 days old. Manual jobs (or schedules with no next run) are overdue after 30 days. A job with no success is `warning` for its first 24 h, then `critical`. A newer failed run makes an otherwise ok job `warning`. If the device has no runnable jobs the check is `critical`, with the message saying whether they are suspended, disabled or missing. |
| `open_issues` | `warning` if any run issue of the device's jobs is still open, else `ok`. |
| `vss_available` | From backup-state: `unknown` if never reported, `ok` if available. If unavailable: `critical` when an active job has `use_vss: required`, else `warning`. |
| `verified_recently` / `test_restore_recent` | Over runnable jobs that have at least one success. `unknown` if none was ever verified or test-restored. `warning` if any such job is unverified or older than 30 days (verify) or 90 days (test restore). Otherwise `ok`. |
| `recovery_key_confirmed` | `unknown` if never reported, `ok` if confirmed. If not confirmed: `critical` when server escrow is unavailable (`Keys()==nil`), else `warning`. |
| `service_installed` | `unknown`, `ok` or `warning`. |
| `profile_authorised` | `critical` if the device has a blacklisted profile or an enabled job in `suspended_by_profile`. `warning` if an enabled job's `profile_sid` is not `active_default`/`authorised` (or is unknown). Otherwise `ok`. |
| `install_mode` | Informational: `ok` with "Simple mode." or "Advanced mode.", or `unknown` if never reported. Never scored. |
| `modules_healthy` | `unknown` if never reported. `critical` if an active job's `required_modules` are not enabled. `warning` if a module is `failed` or a job is `suspended_by_mode`. Otherwise `ok`. |

Jobs and scoring:
- `jobs[]` lists every job of the device.
  - `last_success_at` is the max of the latest committed snapshot of the job and the latest `completed`/`partial` run.
  - `open_issues` counts issues with resolution open.
  - `vss_used` is true when the `vss` module is enabled, the device reports VSS available, and the job's `use_vss` is not `off`. It is an approximation; there is no per-run VSS record.
  - `verified_at` is the max of: a done `verify` task of the device whose payload `repo_id` is the job's repo, and `repo_objects.last_verified_at` where `verify_status='ok'`.
  - `test_restored_at` is the latest done `test_restore` task whose payload `snapshot_id` is a snapshot of the job.
- Score: ok = 100, warning = 50, critical = 0, averaged and rounded over all scored checks of all devices. `unknown` checks and `install_mode` are left out.
- Status is the worst scored check, or `unknown` with score 0 when nothing could be scored (for example, no devices).

Go (package `store`):
```go
func (s *Store) PutBackupState(ctx context.Context, deviceID int64, st wire.BackupState) error
func (s *Store) BackupState(ctx context.Context, deviceID int64) (*wire.BackupState, int64 /*updated_at*/, error) // ErrNotFound
type ReadinessJob struct{ Job BackupJob; LastSuccessAt int64; LastRunStatus string; LastRunAt, OpenIssues, VerifiedAt, TestRestoredAt int64 }
type ReadinessProfile struct{ SID, State string }
type ReadinessFacts struct{ Device *Device; State *wire.BackupState; StateAt int64; Modules *DeviceModules; Profiles []ReadinessProfile; Jobs []ReadinessJob }
func (s *Store) ReadinessDevices(ctx context.Context, userID int64) ([]Device, error) // approved; userID 0 = all
func (s *Store) ReadinessFacts(ctx context.Context, d *Device) (*ReadinessFacts, error)
```
Go (package `api`):
```go
func (s *Server) readinessStatus(ctx context.Context, user *store.User) (string, error) // overall status for GET /summary
const readinessCheckServerKeys = "server_key_custody"
```
There is no readiness feature flag in `wire` (§1.3), so `implementFeature` is not called.

## Commands run and results
- `go build ./...`: OK before P1-16's in-progress `internal/catalog` landed. It now fails only in `internal/catalog` (undefined `Service`, `ErrNoCatalogKey`, `errNoEscrow`), which is P1-16's work in progress and not this task.
- `go vet ./internal/api/ ./internal/store/`: OK.
- `go test ./internal/api/ -run "Readiness|JobFreshness"`: PASS.
- `go test $(go list ./internal/... | grep -v internal/catalog)`: all packages PASS (api, backup, deployenv, keys, objstore/localfs, storage, store).
- `gofmt -l` on the owned files: clean.

## Contract issues
- §11 has no server-level check list and no check id for server key custody. Admin views add `server_key_custody` to each device's checks; it is not in the `wire` constants. INT may want a `wire.ReadinessCheckServerKeyCustody` constant, or a top-level field.
- The contract has no "admin view" parameter. `?scope=all` (admin only) is an implementer choice.
- The `PUT /device/backup-state` status code is unspecified; `204` is used, which matches `sdk/client.ReportBackupState` (no reply body).
- `redundant_destination` and `not_portable_items` are not emitted yet. There is no data source: replication (§9) and the not-portable inventory are not implemented. The list is open, so omitting them is compatible.

## Gaps / follow-ups
- `GET /summary` still reports `readiness: "unknown"`, because `summary.go` (P0-05) is not owned by this task. See the INT notes.
- No `readiness.degraded` notification on status transitions. That belongs to the notifications work.
- `repo_objects.verify_status` has no defined values yet. The verify feature should write `'ok'` on success, or the query in `jobReadiness` needs to change.
- Thresholds (24 h, 7 d, 30 d, 90 d) are constants in `api/readiness.go`. They are not admin settings.

## Notes for dependent tasks / INT
- **summary.go (P0-05 / INT):** in `handleSummary`, replace `sum.Readiness = wire.ReadinessStatusUnknown` with `if st, err := s.readinessStatus(r.Context(), principal(r).User); err == nil { sum.Readiness = st } else { sum.Readiness = wire.ReadinessStatusUnknown }`. Then update `summary_test.go:35`: a user with no devices still gets `unknown`, so the existing assertion only holds when the user has no approved devices.
- **Web (P1-33):**
  - The report is `wire.ReadinessReport`.
  - Fix links point at `/backups`, `/backups/jobs/{id}`, `/backups?device={id}`, `/devices` and `/devices?device={id}`, plus `/admin`.
  - Unknown check ids (`server_key_custody`) should be rendered generically.
  - `install_mode` is informational.
- **Desktop (P1-29):** send `PUT /device/backup-state` at startup and whenever recovery-key confirmation, service install state or VSS availability changes. Each report replaces the previous one, so always send all three fields.
- **Verify / test-restore implementers:** readiness reads `device_tasks` rows of kind `verify` (payload `repo_id`) and `test_restore` (payload `snapshot_id`) with status `done` and `finished_at`.
