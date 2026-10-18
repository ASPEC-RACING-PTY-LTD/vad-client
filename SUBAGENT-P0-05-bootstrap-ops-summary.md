# P0-05 (SRV-0) main.go + testserver parity (B13) + summary/operations/plans
Status: completed. One assertion in P0-03's `devices_delete_test.go` is now outdated because `/summary` is no longer a stub; that file is not mine. The one-line fix is under Notes.

## Goal
1. B13: build the test server exactly as `vaultdock serve` does, from one shared bootstrap.
2. A store, a runner and `GET /operations/{id}` for long-running operations (api-v1 §1, §3).
3. A plan/execute helper with a hashed token, a digest, an expiry, single use and `409 plan_stale` (§1).
4. `GET /summary` (§3).

## Changes (repo vad-server)
- `internal/app/app.go` (**created**): the shared bootstrap. It does what `serve()` used to do inline:
  - create the state dir;
  - `store.Open(DBPath)`;
  - `ensureDefaultLocation` (moved here from main.go);
  - `LoadSettings`, GeoIP with fallback, `storage.New` + **`SetGlobalMinFree(settings.MinFreeBytes)`** + `Reload`;
  - location health logging (a `ListLocations` error is no longer ignored);
  - `FailInterruptedOperations`, `api.New`, `RecoverAfterRestart`.
  - `Run` runs update checks plus the maintenance loop: maintenance at start, every 10 min, orphans every 6th run, plus `ExpirePlans`.
  - `Close` runs `StopOperations` (20 s), then closes the store.
- `cmd/vaultdock/main.go` (modified):
  - `serve()` uses `app.New`, `a.Run` and `a.Close`. It now waits for the background goroutine before closing the store.
  - `dbPath` and `ensureDefaultLocation` were removed; the other commands use `app.DBPath`.
  - gofmt converted the file to LF.
- `internal/testserver/testserver.go` (modified): uses `app.New` and `a.Run`, so the maintenance loop runs as in production. Cleanup order: close the HTTP server, cancel, wait for `Run`, then `a.Close()`. Differences from before:
  - **The DB file is now `vaultdock.db`, not `db.sqlite`.**
  - `min_free` is now the real setting (1 GiB default), not 0.
  - The env sets `VAULTDOCK_UPDATE_CHECK=false` (no network) and `VAULTDOCK_GEOIP_DB=""`.
  - The public API is unchanged (`Start`, `Do`, `Content`, `Device`, `URL`, `Store`), so `testkit` is unchanged and works.
- `internal/store/operations.go` (**created**): the operation store.
- `internal/store/plans.go` (**created**): the plan/execute store.
- `internal/store/summary.go` (**created**): the summary counters.
- `internal/api/operations.go` (replaced the P0-03 stub): `GET /operations/{id}`, the runner and the API plan helpers.
- `internal/api/summary.go` (replaced the stub): `GET /summary`.
- `internal/api/server.go` (**minimal wiring edit**, two lines):
  - field `ops *opRunner` in `Server`;
  - `ops: newOpRunner(),` in `New`.
- Tests (created):
  - `internal/store/operations_test.go`: lifecycle, final-state immutability, list filters, interrupted-on-restart;
  - `internal/store/plans_test.go`: canonical digest, success, single use, wrong user/kind/token, changed state is stale, recompute error passthrough, invalidate, expiry, `ExpirePlans` pruning;
  - `internal/api/summary_test.go` (package `api_test`, frozen helpers): summary counts, scoping and session-only access; operations progress/result, visibility (owner / device-creator / admin for system ops), error masking, panic containment, `bad_id`/404, shutdown cancel and refusal.

## Interfaces exposed

### Bootstrap (`internal/app`)
```go
func DBPath(cfg *config.Config) string
type App struct { Cfg *config.Config; Store *store.Store; Blobs *storage.Manager; Settings *api.SettingsCache; Server *api.Server }
func New(ctx context.Context, cfg *config.Config) (*App, error)
func (a *App) Run(ctx context.Context)   // blocks until ctx ends
func (a *App) Close() error              // stop operations, close DB (after Run/HTTP stopped)
```
Any new construction step goes into `app.build`. Main and the testserver then both get it.

### Operation runner (`internal/api/operations.go`): use this from features
```go
type OpFunc func(ctx context.Context, p *OpProgress) (result any, err error)
type OpProgress struct{ ID int64 }                    // p.Set(done, total, bytes int64)
type OpStart struct{ Kind string; UserID, DeviceID *int64; Params any }
func (s *Server) StartOperation(o OpStart, fn OpFunc) (int64, error)   // system/scheduler use; UserID nil = system op
func (s *Server) startRequestOperation(r *http.Request, kind string, params any, fn OpFunc) (int64, error) // owner = caller user (+device)
func (s *Server) acceptOperation(w http.ResponseWriter, r *http.Request, kind string, params any, fn OpFunc) // writes 202 {"operation_id"}
func (s *Server) StopOperations(ctx context.Context)
```
- **Typical handler:** `s.acceptOperation(w, r, wire.OperationKindVerify, body, func(ctx, p) (any, error) { …; p.Set(i, n, b); return result, nil })`.
- **States:** queued, then running (at most 4 run concurrently), then one of:
  - succeeded: the result is marshalled into `result`, and nil gives `{}`;
  - failed: an `*apiError` (`errf`) message is shown to the user; any other error is logged and shown as "Internal server error"; panics are recovered;
  - cancelled: on server shutdown (ctx cancelled).
- At startup, leftover queued/running ops are marked failed with the message "interrupted by a server restart". In-process operations do not resume after a restart.
- **Visibility of `GET /operations/{id}`:**
  - a device sees only ops whose `device_id` is itself (and its user);
  - a session sees ops of its own user;
  - an admin session additionally sees system ops (`user_id` NULL);
  - everything else gets 404.
- There is no list route (none in the contract). The store has `ListOperations` for later admin views.

### Operation store (`internal/store/operations.go`)
```go
type Operation struct{ ID int64; UserID, DeviceID *int64; Kind, State string; Done, Total, Bytes int64; Params, Result, Error string; CreatedAt, UpdatedAt int64; FinishedAt *int64 }
var ErrOperationFinished error
func (s *Store) CreateOperation(ctx, *Operation) error
func (s *Store) Operation(ctx, id int64) (*Operation, error)
func (s *Store) StartOperation(ctx, id int64) error
func (s *Store) UpdateOperationProgress(ctx, id, done, total, bytes int64) error
func (s *Store) FinishOperation(ctx, id int64, state, resultJSON, errMsg string) error
type OperationFilter struct{ UserID *int64; SystemOnly bool; Kind, State string; BeforeID int64; Limit int }
func (s *Store) ListOperations(ctx, OperationFilter) ([]Operation, error)
func (s *Store) FailInterruptedOperations(ctx) (int64, error)
```

### Plan/execute
Store side:
```go
var ErrPlanStale error
const DefaultPlanTTL = 15 * time.Minute
type NewPlan struct{ UserID int64; Kind, TargetType string; TargetID *int64; Plan any; TTL time.Duration }
func PlanDigest(plan any) (canonical []byte, digest string, err error) // compact JSON, sorted keys, exact numbers; SHA-256 hex
func (s *Store) CreatePlan(ctx, NewPlan) (token string, *Plan, error)   // 32-byte crypto/rand token, base64url; only SHA-256 stored
func (s *Store) ExecutePlan(ctx, userID int64, kind, token string, current func(*Plan) (any, error)) (*Plan, error)
func (s *Store) InvalidatePlans(ctx, kind, targetType string, targetID int64) (int64, error)
func (s *Store) ExpirePlans(ctx, retain time.Duration) (int64, error)   // called by app maintenance, retain 7 days
```
API side (package `api`):
```go
func (s *Server) createPlan(r *http.Request, kind, targetType string, targetID *int64, plan any, ttl time.Duration) (*wire.PlanResponse, error)
func (s *Server) executePlan(r *http.Request, kind, token string, current func(*store.Plan) (any, error)) (*store.Plan, error) // ErrPlanStale → 409 plan_stale
func errPlanStale() *apiError
```
- **Usage:** in `…/plan`, build the plan value, then `resp, err := s.createPlan(r, "prune", "repo", &repoID, plan, 0)` and `writeJSON(w, 200, resp)`.
- In `…/execute`, decode `wire.PlanExecuteRequest`, then `sp, err := s.executePlan(r, "prune", req.PlanToken, func(sp *store.Plan) (any, error) { return recomputePlan(...) })`. Then start the operation or approval.
- `current` must rebuild the plan **with the same Go types/shape** used at plan time. It runs outside any transaction.
- **These all return `plan_stale`:**
  - an unknown, foreign-user or wrong-kind token;
  - an expired, used, invalidated or tampered plan;
  - a changed digest.
- The executed transition is a conditional UPDATE, so the token is single use even under concurrent executes.
- An error from `current` is returned unchanged and does not consume the plan.

### Summary
- `func (s *Store) Summary(ctx, userID int64) (*wire.Summary, error)`, scoped to the session user:
  - pending (unexpired) enrolments;
  - pending unexpired approvals;
  - distinct jobs with an unfinished run in queued/scanning/uploading/committing;
  - failed runs in the last 24 h;
  - unread notifications;
  - open sync conflicts;
  - blacklisted profiles on the user's devices.
- `GET /summary` (sess) returns these counters with `readiness: "unknown"` until SRV-7 supplies a status.

## Commands run and results
- `cd vad-server && go build ./... && go vet ./...`: OK. Both briefly failed on another task's in-flight `sdk/client` edits; I waited and retried.
- `go test -count=1 ./internal/...`:
  - store, storage and objstore: ok;
  - api: everything passes **except `TestStubRoutesAndAuthGroups`**, which expects `/summary` to answer 501 (see Notes);
  - `TestSummary` and `TestOperations` pass.
- `go test -count=1 -tags integration ./tests/...`: ok
- `cd VaultDock/desktop/e2e && go test -count=1 ./...`: ok (17.8 s; it runs via testkit on the new bootstrap)
- `gofmt -l` on all owned and created files: clean

## Contract issues
1. §3 gives no list route for operations, so none was added (store only).
2. The visibility of system operations (no owning user, e.g. scheduled GC) is not specified. I chose to make them visible to admin sessions only.
3. Summary scope (per user vs server-wide for admins) is not specified. I chose per user, including for admins.
4. Several things are not specified, so I chose:
   - plan TTL: default 15 min, overridable per call;
   - an unknown or foreign token is also `plan_stale` (no 404), to avoid leaking whether a token exists.

## Gaps / follow-ups
- There is no operation cancel API (none in the contract).
- Progress writes go to the DB on every `Set`. Features with very frequent updates should throttle their calls.
- Readiness in the summary stays `unknown` until SRV-7 provides an overall status.

## Notes for dependent tasks / INT
- **Required (INT or the P0-03 owner):** in `internal/api/devices_delete_test.go`, `TestStubRoutesAndAuthGroups` (line 149) still uses `/api/v1/summary` as its example of a session-only stub. Change it to another session-only stub that is still unimplemented, for example `GET /api/v1/approvals`, keeping the 501 `not_implemented` assertion.
- **server.go edit (constructor wiring only):** one field `ops *opRunner` and one initialiser line in `New`. Code that builds `api.Server` directly (such as the frozen `api_test.go`) still works; it just never calls `StopOperations`.
- **Shutdown order:** stop HTTP, cancel `Run`, then `App.Close()`. Code that builds the server directly should call `srv.StopOperations(ctx)` before closing the store.
- The testserver DB is now `<state>/vaultdock.db`. `Store` is still exposed.
- Line endings: the edited files (main.go and testserver.go) were rewritten with LF by gofmt.
