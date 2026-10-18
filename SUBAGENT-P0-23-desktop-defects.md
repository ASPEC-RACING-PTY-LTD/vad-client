# P0-23 (DSK-H) Desktop defect fixes B3, B4, scan issues (B12)
Status: completed

## Goal
- **B3:** persist the `schedule.interval` last fire time in state.db.
- **B4:** log invalid automations and record them as visible issues.
- **B12:** record unreadable, locked or inaccessible scan entries as de-duplicated issues, and keep scanning.

## Changes (repo: path, created/modified/deleted, why)
- VaultDock: `desktop/internal/engine/issues.go` (created).
  - Issue source and class constants.
  - `classifyIssue(err)`: `locked` for Windows errno 32/33, `permission_denied`, `not_found`, `io_error`.
  - `(*Engine).recordIssue`: an upsert into `issues` through the exported `state.Store.DB`. On conflict of (source, config_id, rel_path, class) it bumps `count`, `last_seen` and `message`, and reopens a `resolved` row.
  - `probeRead`: a package var so tests can replace it.
- VaultDock: `desktop/internal/engine/automations.go` (modified).
  - B4: new `activeAutomations(list)`, shared by `refreshAutomations` and `LoadCachedAutomations`. Each invalid automation gets an `slog` Warn with id, name, error count and the first error, and an issue `source=automation, rel_path=<automation id>, class=invalid_automation`. Disabled automations are still skipped quietly.
  - B3: `runSchedules` now uses the new `scheduleDue(key, now, every)`.
    - It is backed by `Store.ScheduleLast`/`SetScheduleLast` (key `"<automation id>/<node id>"`, unix ms) and serialised by a package-level `scheduleMu`.
    - On first sighting the interval starts and nothing fires (same as before).
    - A stored time in the future (the clock moved back) restarts the interval from now.
    - On a DB error the tick is skipped and logged, so it can't fire repeatedly.
- VaultDock: `desktop/internal/engine/scan.go` (modified). `Scan` has a per-scan `skipped(rel, err)` helper that logs a Warn and records an issue, once per path per scan. Across scans the DB row counts repeats. It is called for:
  - walk errors (unreadable folders and entries);
  - `d.Info()` failures;
  - a missing or unstattable file in a `files`-kind config.

  Files that vanish during a walk (`not_found` in folder configs) are ignored as normal churn. New or changed files are opened once (`probeRead`) before being queued:
  - **permission_denied / io_error:** recorded and not queued, but still counted as seen, so the file isn't treated as a local deletion.
  - **locked:** recorded and still queued; the transfer defers it.
- VaultDock: `desktop/internal/engine/automations_persist_test.go` (created). Covers the schedule surviving a restart (no re-fire, no skip), the clock moving back, and invalid automations recorded once per automation with count de-dup.
- VaultDock: `desktop/internal/engine/scan_issues_test.go` (created). Covers permission-denied and locked files (via the `probeRead` stub; locked only on Windows), a missing selected file, an unreadable folder (POSIX only, skipped on Windows) and `classifyIssue`. None of these tests need the server.

## Interfaces exposed
Package-internal only:
```go
func classifyIssue(err error) string
func (e *Engine) recordIssue(source, configID, rel, class, msg string)
func (e *Engine) activeAutomations(list []client.RemoteAutomation) []automation.Named
func (e *Engine) scheduleDue(key string, now time.Time, every time.Duration) bool
var probeRead func(abs string) error
```
Issue rows written:
- Source `scan`: `config_id` = config id; `rel_path` = slash path ("." for the root, or the absolute path for a files-kind entry outside the root); class is one of `locked`, `permission_denied`, `not_found`, `io_error`.
- Source `automation`: `config_id` = ""; `rel_path` = automation id; class = `invalid_automation`.

## Commands run and results
- `cd desktop && go build ./...`: ok. It failed at first in `internal/agent` (undefined `EventKind`/`Event`, another task mid-edit). Every other package built, and it passed on retry.
- `go vet ./internal/engine/`: ok
- `go test ./internal/engine/ ./internal/state/`: ok
- `cd desktop/e2e && go test ./...`: ok (about 18s)
- `gofmt -l internal/engine`: clean

## Contract issues
None. `invalid_automation` and the scan classes are local-only classes in state.db. Mapping them to the api-v1 §6 issue classes when reporting is P0-25's job.

## Gaps / follow-ups
- Issues are never auto-resolved when a path later scans fine, or when an automation is fixed or removed. That fits with reporting/UI (P0-25).
- The infinite locked-file deferral in the transfer and the in-memory reports (rest of B12) are P0-25.
- `Engine.scheduleLast` in `engine.go` is now unused, but I couldn't remove it because the file isn't mine.
- `invalid_automation` issues are re-counted on every automation refresh while the automation stays invalid.

## Notes for dependent tasks / INT
- INT or the `engine.go` owner can delete the unused `scheduleLast` field and its map initialiser in `New`.
- `state` has no issues accessor; the engine writes through `Store.DB`. If a shared `state.RecordIssue`/`ListIssues` is added later (P0-25), switch `recordIssue` to it.
- No new dependencies.
