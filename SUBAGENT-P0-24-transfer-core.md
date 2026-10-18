# P0-24 (DSK-B) Extract the transfer core
Status: completed

## Goal
Move the reusable upload mechanics (resumable chunked sessions, adaptive concurrency, rate limiting, retry/backoff classification, progress) out of `desktop/internal/engine` into the new package `desktop/internal/transfer`, expose `transfer.Core`, and make `engine` delegate to it with no change in behaviour.

## Changes (repo: path, created/modified/deleted, why)
All in the client repo `VaultDock`:
- `desktop/internal/transfer/controller.go` (created): the adaptive `Controller`, moved verbatim from `engine/concurrency.go`. `absoluteMax` is exported as `AbsoluteMax`. New method `CapMax(n)` replaces engine code that wrote `ctrl.max` directly under `ctrl.mu`; same semantics, no notify.
- `desktop/internal/transfer/controller_test.go` (created): the four controller tests, moved verbatim.
- `desktop/internal/transfer/limiter.go` (created): `Limiter`, the bandwidth limiter. It was `Engine.limiter` + `limitedReader` + `wrapBody`. The logic is the same (64 KiB read cap, burst `max(256 KiB, rate)`, limit re-read on every Read), but it now has its own mutex instead of `Engine.mu`.
- `desktop/internal/transfer/classify.go` (created): `MaxAttempts`, `Backoff`, `IsNetErr`, `IsCongestion` (moved from `engine/transfer.go`), and the new `StageError`.
- `desktop/internal/transfer/core.go` (created): `Core`. It contains `Hash` (was `hashReader` + `Engine.hashSem`), `Upload` (the CreateUpload → chunks → CompleteUpload sequence from `Engine.process`) and `UploadChunks`/`sendChunk`/`progressReaderAt`, moved from `engine/transfer.go`. The per-file parallelism (4) and the per-chunk retries (5 attempts, 0.5s·2^n or Retry-After) are unchanged.
- `desktop/internal/transfer/core_test.go` (created): tests against an httptest fake server. They cover resume (the already-received chunk is skipped), a 503 chunk retry, progress accounting, the phase sequence, the already-complete shortcut, a `complete`-stage `StageError` with the code preserved, hashing plus cancellation, classification, backoff bounds and the limiter.
- `desktop/internal/engine/engine.go` (modified): new field `xfer *transfer.Core`. `ctrl` is kept as `xfer.Controller()` because `report.go` calls `e.ctrl.Tick`. Removed `hashSem`, `limiter`, `limitedReader` and `wrapBody`. `SetClient` → `xfer.Attach`; `ApplySettings` → `xfer.ApplySettings`. Dropped the `io` and `x/time/rate` imports.
- `desktop/internal/engine/transfer.go` (modified): `process` now calls `e.xfer.Hash` and `e.xfer.Upload`. It unwraps `*transfer.StageError` so `handleErr` gets the same stage strings ("start upload", "upload", "complete") and messages as before; the complete-stage `hash_mismatch`/`size_mismatch`/`not_found`/`upload_gone` deferrals are unchanged. Removed the moved helpers. The sync-specific `handleErr`, `dispatch` and status code stay in engine.
- `desktop/internal/engine/concurrency.go` (modified): thin compatibility aliases: `type Controller = transfer.Controller`, `NewController`, `absoluteMax`.
- `desktop/internal/engine/concurrency_test.go` (modified): now checks that the engine routes its settings to the shared core (the controller tests moved to `transfer`).
- Not touched: `engine/{scan,watch,automations,report}.go`. The exported engine API (`New`, `Start`, `Stop`, `SetClient`, `ApplySettings`, `SaveConfig`, `Scan`, `RefreshAutomations`, `Automations`, `Snapshot`, …) is unchanged.

## Interfaces exposed (exact Go/TS signatures, routes, schema others depend on)
```go
package transfer // github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/transfer

const AbsoluteMax = 128
const MaxChunkParallelPerFile = 4
const MaxAttempts = 12 // queue-level attempts before an item is failed

type Settings struct {
	MaxParallel    int // automatic upper bound on parallel requests
	ManualParallel int // fixed parallel requests; 0 = adaptive
	BandwidthKBps  int // upload limit in KiB/s; 0 = unlimited
}

type Progress struct {
	Sent  *atomic.Int64      // optional; bytes sent incl. resumed bytes
	Phase func(string)       // optional; "uploading", "finalizing"
}

type Core struct{ /* unexported */ }
func New(maxParallel, hashParallel int) *Core
func (c *Core) Controller() *Controller
func (c *Core) Limit() int
func (c *Core) Inflight() int
func (c *Core) Tick(now time.Time)                       // call ~every 2s
func (c *Core) ApplySettings(s Settings)
func (c *Core) Attach(cl *client.Client, serverMaxChunks int) // sets cl.ChunkBody, caps concurrency
func (c *Core) WrapBody(r io.Reader) io.Reader
func (c *Core) Hash(ctx context.Context, r io.ReaderAt, size int64) (string, error)
func (c *Core) Upload(ctx context.Context, cl *client.Client, req client.UploadRequest, r io.ReaderAt, p Progress) (*client.UploadResult, error) // errors are *StageError
func (c *Core) UploadChunks(ctx context.Context, cl *client.Client, up *client.UploadSession, r io.ReaderAt, sent *atomic.Int64) error

type StageError struct{ Stage string; Err error } // Stage: "start upload" | "upload" | "complete"; implements Unwrap

type Controller struct{ /* unexported */ }
func NewController(maxLimit int) *Controller
func (c *Controller) SetBounds(maxLimit, manual int)
func (c *Controller) CapMax(n int)
func (c *Controller) Limit() int
func (c *Controller) Inflight() int
func (c *Controller) Throughput() float64
func (c *Controller) Acquire(ctx context.Context) error
func (c *Controller) Release(bytes int64, latency time.Duration, congestion bool)
func (c *Controller) Tick(now time.Time)

type Limiter struct{ /* zero value = unlimited */ }
func (l *Limiter) SetKBps(kbps int)
func (l *Limiter) Wrap(r io.Reader) io.Reader

func Backoff(attempts int) time.Duration
func IsNetErr(err error) bool
func IsCongestion(err error) bool
```

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `gofmt -l internal/`: clean.
- `go build ./...`: ok.
- `go vet ./internal/engine/ ./internal/transfer/`: ok.
- `go test ./internal/engine/ ./internal/transfer/`: both ok.
- `cd e2e && go test ./...`: ok (15.3s).

## Contract issues
None.

## Gaps / follow-ups
- The engine still creates its own `Core` in `engine.New` (`transfer.New(32, 2)`). To share one core between the sync and backup engines, a later task should add an injecting constructor or setter (for example `engine.NewWithCore` / `SetTransferCore`) in `engine.go`. No such hook was added, because no consumer exists yet.
- These items from research WS-B are out of scope for this task and not done: the weighted multi-queue scheduler (`transfer.Task` / `scheduler.go`), the durable outbox (`internal/outbox`), the "bounded deferral fix", and report.go changes.
- `Core.Upload` is tied to the sync upload-session API (`sdk/client`). The backup engine's pack uploads may need an extra method once `sdk/repo.Backend` exists.

## Notes for dependent tasks / INT
- `engine.Controller`, `engine.NewController` and `engine.absoluteMax` are now aliases of `transfer` types and values. They can be removed once nothing references them; today only `engine` itself uses them.
- `report.go` (another owner) still calls `e.ctrl.Tick(now)`. That works because `e.ctrl == e.xfer.Controller()`. The owner may switch it to `e.xfer.Tick(now)`.
- No new dependencies. `golang.org/x/time/rate` is now imported by `internal/transfer` instead of `internal/engine`, and `go.mod` is unchanged.
