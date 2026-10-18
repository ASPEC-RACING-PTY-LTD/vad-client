# P1-24c (DSK-C) Persist run snapshot id/oid
Status: completed

## Goal
Close the P1-26 gaps listed under "Parent oid", "Lost snapshot id" and "Unreported final status", plus the optional store and pipeline items:
- persist each run's committed snapshot id and root oid in backup.db;
- add a `RunByServerID` lookup;
- add a periodic `Resume` retry;
- add `Result.ReferencedPacks`, so `CommitRefs` no longer reads the trees back.

## Changes (repo: path, created/modified/deleted, why)
All changes are in VaultDock, `desktop/internal/backup/`. All of these files were modified; none were created or deleted.
- `store.go`:
  - Migration 6 `runs_snapshot_oid` adds `runs.snapshot_oid TEXT NOT NULL DEFAULT ''` and index `runs_run_id`.
  - Added the field `Run.SnapshotOID`, which `PutRun`, `Run`, `Runs` and `UnfinishedRuns` now read and write.
  - Added `SetRunSnapshot` and `RunByServerID`.
  - `FinishRun` now uses `snapshot_id = COALESCE(?, snapshot_id)`. Passing 0 keeps an id that is already recorded.
- `issues.go`: `runJob` uses `Store.RunByServerID`. It no longer queries `Store.DB` directly or imports `database/sql`.
- `runner.go`:
  - **Snapshot record.** Right after a successful commit, `backup()` calls `SetRunSnapshot(uuid, snapshotID, oid)`. This happens before `finalize`.
  - **Parent from backup.db.** The in-memory `parent` map, `parentOID` and `setParent` are removed. The new `lastSnapshot(ctx, job, self)` returns both `parent_snapshot_id` and `Root.Parent` from the newest earlier run that has a snapshot. Both are computed once, before the pipeline runs. If that run predates migration 6, its oid is empty and `Root.Parent` is nil.
  - **Resume.** `Resume` now acquires the job before handling each run. A job that has a live run is skipped and left for a later `Resume`, which makes periodic calls safe. Per-run handling moved into `resumeRun`:
    - A run with a terminal status is finalized with its recorded `SnapshotID`.
    - A non-terminal run with `SnapshotID > 0` committed, but the process stopped before its final status was saved. It is finalized as `committedStatus(stats)` (partial if `Errors > 0`, otherwise completed) and is not backed up again.
    - All other runs keep the earlier behaviour.
  - **ResumeLoop.** New `ResumeLoop(ctx, every)`, with `DefaultResumeEvery = 10m`. It is a ticker bound to `ctx` that calls `Resume` and logs errors at Warn.
- `pipeline.go`: new field `Result.ReferencedPacks`, set right after `SaveSnapshot`.
- `pipeline_tree.go`: new `treeBuilder.referencedPacks`. It looks up in the repo index every tree blob, every file and ADS chunk, and the inventory blob. It returns the packs sorted, or nil if any blob is not in the index.
- `report.go`:
  - `RepoEngine` keeps the latest `Run` result in the unexported field `last`.
  - `CommitRefs` uses `last.ReferencedPacks` when the root is the latest run's root (pointer equality). Otherwise it falls back to `snapshotPacks`, which reads the trees.
- Tests:
  - `store_test.go`: the schema version is now 6. Added `TestStoreRunSnapshot`, a table test for `RunByServerID` that also checks `FinishRun(0)` keeps the snapshot, and `TestStoreMigrateFromV5`, which upgrades a v5 database that already has rows.
  - `runner_test.go`: `TestRunnerCommitsSnapshot` now checks `SnapshotOID`, and that a new `Runner` (a restart) gets the parent id and oid from the store. `TestRunnerResume` checks that an unreported run keeps snapshot 501 and its oid. Added `TestRunnerResumeCommitted` (no re-backup; completed and partial) and `TestRunnerResumeLoop` (retries the final status and leaves a live run alone).
  - `report_test.go`: `TestRepoEngineCommitRefs` checks that `ReferencedPacks` equals what a tree read-back returns and what is committed, and that an older root still uses the read-back path.

## Interfaces exposed
The API changes are additions only, so existing callers still compile.
```go
// store.go
type Run struct { ...; SnapshotOID string /* new */; ... }
func (s *Store) SetRunSnapshot(ctx context.Context, uuid string, snapshotID int64, oid string) error // ErrNotFound
func (s *Store) RunByServerID(ctx context.Context, runID int64) (*Run, error)                      // newest; ErrNotFound
// FinishRun: snapshotID 0 now keeps an id recorded earlier (it used to clear it to NULL).

// runner.go
const DefaultResumeEvery = 10 * time.Minute
func (r *Runner) ResumeLoop(ctx context.Context, every time.Duration) // blocks until ctx ends; does not call Resume first

// pipeline.go
type Result struct { ...; ReferencedPacks []string /* new; nil = unknown */ }
```
backup.db schema: migration 6 adds `runs.snapshot_oid` and `CREATE INDEX runs_run_id ON runs(run_id)`.

## Commands run and results
All commands were run from `D:/VaultDock/VaultDock/desktop`.
- `gofmt -l internal/backup`: no output (clean).
- `go build ./...`: ok.
- `go vet ./internal/backup/...`: ok.
- `GOOS=linux go vet ./internal/backup/...`: ok.
- `go test -count=1 ./internal/backup/...`: ok.
- `go test -count=5 -run Runner ./internal/backup/`: ok.
- `-race` could not run because there is no gcc (cgo) on this host.

## Contract issues
None.

## Gaps / follow-ups
- **Approximated status.** Only one case uses `committedStatus`: a run that committed but stopped before `finalize` saved its terminal status. It decides partial from `stats.Errors > 0` in the last saved heartbeat, which approximates `partial()`. That function also looks at `Walk.Complete` and the severity of each issue, and neither survives a restart. The crash window is a single local write.
- **Old rows.** Runs recorded before migration 6 have no oid. The first run after the upgrade therefore has `Root.Parent = null`, while its `parent_snapshot_id` is still set.
- **Interrupted runs.** `Resume`, and so `ResumeLoop`, still re-runs non-terminal runs that are unfinished and not live. This is the earlier behaviour, but it now also happens periodically. In normal operation, such runs only exist after a shutdown or crash, or after a failed local `FinishRun` write.
- **Pipeline items not done.** `Params.NoPrune` and a path filter for narrowed Retry Skipped runs were not implemented, because they were not requested.

## Notes for dependent tasks / INT
- **Service wiring** (P1-26 notes, step 4): after `r.Resume(ctx)`, add `go r.ResumeLoop(ctx, 0)`. Its goroutine ends when `ctx` is cancelled. Call `r.Wait()` on shutdown as before.
- **UI / IPC:** `Run.SnapshotOID` is available, and it is already set while a run is still finalizing. Use `Store.RunByServerID` instead of querying `runs.run_id` directly.
- **Dependencies:** none added.
