# P0-25 (DSK-B) Outbox + bounded locked-file deferral (B12 remainder)
Status: completed (e2e blocked by an unrelated in-progress server edit, see below)

## Goal
Durable outbox for server reports in `state.db`; route transfer reports (and notifications) through it instead of the in-memory buffer; bound locked/unopenable-file deferral in `engine/transfer.go` and turn it into an issue + skip-until-changed.

## Changes (repo: path, created/modified/deleted, why)
All in client repo `VaultDock`:
- `desktop/internal/outbox/outbox.go` (created): `Outbox` over the `outbox` table (migration 3). Enqueue with an idempotency key (`dedupe_key`, ignored on duplicate, random 128-bit hex when empty). The queue is capped (default 10000): the oldest rows are dropped in the same transaction and `OnDrop` is called. `Flush` delivers in `next_at, id` order and deletes a row only after the sender succeeds (at-least-once). On a transient error it sets attempts+1, `next_at` = now + backoff (5s doubling, max 15m) and `last_error` (truncated to 500 chars), then stops the flush. A `Permanent(err)` drops the item and calls `OnDrop`. A cancelled send is not counted as an attempt. `Run(ctx)` is a background loop (Interval and `Kick`). Concurrent `Flush` calls are serialised with TryLock. `method`/`path` columns are stored as `''` (unused).
- `desktop/internal/outbox/outbox_test.go` (created): order + keys + dedupe, the backoff schedule and not-before, permanent drop, overflow oldest-drop, durability across reopen, cancel not counted, Run+Kick.
- `desktop/internal/engine/report.go` (modified):
  - `flushReports` now drains `e.reports`/`e.notes` into the outbox every 10s, even while offline. Transfers go in batches of 200, kind `transfer_report`; notifications in batches of 50, kind `notification`.
  - It then flushes if a client is set.
  - Anything that fails to enqueue is put back in memory, with the old caps.
  - New `DeliverReport` sender: calls `checkRevoked`. A 4xx other than 401/403/408/429, bad JSON or an unknown kind returns `outbox.Permanent`.
  - Drops record an issue: source `outbox`, rel_path = kind, class `report_dropped`.
  - `e.ctrl.Tick` was changed to `e.xfer.Tick`, as P0-24 suggested.
- `desktop/internal/engine/transfer.go` (modified): `os.Open` failure → `deferUnreadable`.
  - The count and first-failure time are persisted in `kv` under key `open_defer/<config>/<rel>` with value `"size mtime count firstMs"`. This survives restarts and resets when size/mtime change.
  - Below the bounds (`maxOpenDeferrals`=20 tries, `maxOpenDeferSpan`=30 min, retry every 30s), the item is deferred as before ("file is in use; waiting", or "cannot open the file; waiting" when the error is not a lock).
  - Once a bound is hit:
    - an issue is recorded (source `transfer`, class = `classifyIssue(err)`, i.e. `locked` for sharing/lock violations);
    - the queue item is removed;
    - the files entry is set to `skipped` with the current size/mtime, so scan (`scan.go:98`) and the watcher leave it alone until the file changes;
    - a `skipped` TransferReport is queued (the server accepts `skipped`).
  - A successful open clears the counter. `openForUpload` is a var so tests can inject failures. The P0-24 `xfer.Hash`/`xfer.Upload` delegation is unchanged.
- `desktop/internal/engine/deferral_test.go` (created): count bound, reset on file change, time bound, success clears the counter, reports survive restart (offline enqueue → new engine on the same DB delivers all 250 in order after a 503 + backoff), and a rejected (422) report is dropped with an issue.

## Interfaces exposed
```go
package outbox
type Sender func(ctx context.Context, kind string, payload []byte) error
type Options struct { MaxItems int; BaseDelay, MaxDelay, Interval time.Duration; Batch int; OnDrop func(reason, kind string, n int); Log *slog.Logger }
const DropOverflow = "overflow"; const DropRejected = "rejected"
func New(db *sql.DB, send Sender, opt Options) *Outbox
func (o *Outbox) Enqueue(kind, key string, payload []byte) error
func (o *Outbox) Flush(ctx context.Context) (int, error)
func (o *Outbox) Run(ctx context.Context)
func (o *Outbox) Kick()
func (o *Outbox) Len() (int, error)
func Permanent(err error) error
func IsPermanent(err error) bool
func KeyFromContext(ctx context.Context) string // idempotency key of the item being sent
func NewKey() string

package engine
func (e *Engine) SetOutbox(o *outbox.Outbox)   // optional; sender should be e.DeliverReport
func (e *Engine) DeliverReport(ctx context.Context, kind string, payload []byte) error
```
DB handle: `state.Store.DB` (already exported). Without `SetOutbox`, the engine lazily creates its outbox from `e.st.DB`, so **no wiring is required**.

## Commands run and results
From `desktop/`:
- `gofmt -l internal/engine internal/outbox`: clean
- `go build ./...`: ok
- `go vet ./internal/engine/ ./internal/outbox/`: ok
- `go test -count=1 ./internal/engine/ ./internal/outbox/`: both ok
- `cd e2e && go test ./...`: **build failed**, outside this task. The error is `vad-server/internal/api/server.go:624: s.deviceCanDelete undefined`. A parallel server task is mid-edit; this task touched no server file. INT should rerun e2e once that task lands.

## Contract issues
None. (`/device/transfers` has no idempotency key in api-v1, so duplicate delivery after a lost response is possible. The server only appends history rows, so that is harmless. `KeyFromContext` is ready for run reports idempotent by `client_run_uuid`.)

## Gaps / follow-ups
- The outbox is stored in a package-level `sync.Map` keyed by `*Engine` (because `engine.go` is not owned). INT may move it into an `Engine` field and start `ob.Run(e.ctx)` from `Start`. Today it is flushed from `timerLoop` every 10s through `flushReports`, and backoff is honoured by `next_at`.
- In-memory staging remains for ≤10s (`e.reports` is appended under `e.mu`, and DB writes were kept out of that lock). A crash in that window loses those reports.
- `open_defer/` kv rows are cleared on open success and on skip. Rows for files whose queue item ends another way (deleted, config removed) remain, but they are tiny and reset on size/mtime mismatch. A cleanup could go into a later housekeeping task.
- Run reports and issue upload (api-v1 §6) are not yet producers; they can use the outbox with their own kinds plus a `DeliverReport` case.
- The skipped file is not added to `history` (the UI history statuses are completed/failed). It shows via the issues table and the files entry `skipped`.

## Notes for dependent tasks / INT
- `transfer.go` relies on `recordIssue`, `classifyIssue` and `issueLocked` from `engine/issues.go` (created by P0-23). Keep those names, or adjust the call sites.
- New engine constant `issueSourceTransfer = "transfer"`; issue sources in use are now `scan`, `automation`, `transfer` and `outbox`. New issue class `report_dropped`.
- The test helpers in `deferral_test.go` are self-contained (`deferralEngine`, `setTestClient`) so they don't depend on P0-23's test helpers.
- No new dependencies.
