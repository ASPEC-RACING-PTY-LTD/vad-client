# P1-28 DSK-MOD end-to-end install mode
Status: completed

## Goal
Take the P0-38 module manager end to end (MASTER_PLAN §7.7, api-v1 §6/§6A):
- a server sender over `*client.Client`;
- a report loop: at startup, after every change, daily, with backoff;
- `mode_change_request` task handling that needs local admin confirmation;
- an `ItemSource` registry plus a sync folder adapter;
- a one-call wiring helper for the agent `Host`.

## Changes (repo: path, created/modified/deleted, why)
All in VaultDock, under `desktop/internal/modules/`:

- `manager.go` (modified):
  - Sources moved into a mutex-guarded registry: `Options.Sources` seeds it and `RegisterSource` adds to it. `affected` now takes a sources snapshot, so suspend uses the same list it queried.
  - `ModulesState.PendingRequest` added.
  - New `Options` fields: `ReportInterval`, `RetryMin`, `RetryMax`, `ModeRequestTimeout`.
  - `changed()` now kicks the loop while `RunReporter` is running and still reports synchronously otherwise, so the P0-38 behaviour and tests are unchanged.
- `report.go` (created): `ErrNotConnected`, `ClientSender`, `Kick`, `RunReporter`.
- `moderequest.go` (created): `ModeRequest`, `ModeChangeResult`, `HandleTask`, `PendingModeRequest`, `ConfirmModeRequest` and their errors.
- `sources.go` (created):
  - `RegisterSource`;
  - `SyncConfigStore`;
  - `SyncFolderSource`, which adapts `config.SyncConfig` through a small interface that `*engine.Engine` already satisfies. The `config` and `engine` packages are not edited.
- `host.go` (created): `NewForHost`.
- `e2e_test.go` (created), covering:
  - the report loop: startup report, backoff after two failures, a change reported through the loop, the periodic report and the single-instance guard;
  - `Kick`;
  - `ClientSender` returning not-connected;
  - `NewForHost`;
  - mode request deny, approve, same-mode and a downgrade that suspends items;
  - mode request errors: wrong kind, bad mode, unknown payload field, timeout, superseded and ctx cancel;
  - `RegisterSource` suspend and resume through a fake source;
  - `SyncFolderSource` with a fake config store: a user-disabled folder is never re-enabled, and suspension survives a restart.

## Interfaces exposed
```go
var ErrNotConnected, ErrUnsupportedTask, ErrNoModeRequest, ErrModeRequestSuperseded, ErrModeRequestTimeout error

func ClientSender(cl func() *client.Client) Sender            // cl() nil → ErrNotConnected; else c.ReportModules (PUT /device/modules)
func (m *Manager) RunReporter(ctx context.Context)             // blocking; startup + on change/Kick + every ReportInterval (24h); backoff 30s→1h
func (m *Manager) Kick()                                       // non-blocking "report now" (e.g. after enrolment)

type ModeRequest struct { ID string; TaskID int64; CurrentMode, TargetMode sdkmod.Mode; RequestedBy string; ReceivedAt int64; Preview SwitchPreview } // json snake_case
type ModeChangeResult struct { Approved bool; FromMode, InstallMode sdkmod.Mode; Applied *SwitchPreview } // task result JSON: approved, from_mode, install_mode, applied?
func (m *Manager) HandleTask(ctx context.Context, t wire.DeviceTask) (result any, err error)
func (m *Manager) PendingModeRequest() *ModeRequest
func (m *Manager) ConfirmModeRequest(ctx context.Context, id string, approve bool) (SwitchPreview, error)
// ModulesState gains: PendingRequest *ModeRequest `json:"pending_request,omitempty"`

func (m *Manager) RegisterSource(src ItemSource)
const ItemKindSyncFolder = "sync_folder"
type SyncConfigStore interface { Configs() []config.SyncConfig; SaveConfig(config.SyncConfig) (config.SyncConfig, error) }
func NewSyncFolderSource(db *sql.DB, store SyncConfigStore, requires func(config.SyncConfig) []sdkmod.ID) *SyncFolderSource // requires nil = [sync]
func (s *SyncFolderSource) Suspended(ctx context.Context) ([]string, error)

func NewForHost(db *sql.DB, modeFile string, coreVersion string, cl func() *client.Client, publish func(any)) *Manager // nil only if state.db unreadable (logged)
```

How `HandleTask` behaves:
- **Other task kinds:** a `ErrUnsupportedTask` error.
- **Already in the target mode:** returns `{approved:true}` at once.
- **Otherwise:**
  1. publishes a pending request, with a preview, through `OnChange`;
  2. blocks until `ConfirmModeRequest`, a newer request (the old one fails with `ErrModeRequestSuperseded`), `ModeRequestTimeout` (24h; `ErrModeRequestTimeout`) or ctx.
- **Outcomes:**
  - Deny: a `done` result with `approved:false`; nothing is reported.
  - Approve: runs `SetMode`, which suspends items, persists, notifies and reports, and the result carries the applied preview.
  - The pending request is held in memory only and is lost if the agent restarts.
- **Result reporting:** P1-26 maps `err != nil` to status `failed` (with the error text) and otherwise to `done` with `json.Marshal(result)`.

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `gofmt -l internal/modules`: clean.
- `go build ./...`: ok. An earlier run failed in the root package (`app_update.go`, another task's in-progress rewiring); the final run was clean.
- `go vet ./internal/modules/`: ok.
- `go test -count=3 ./internal/modules/`: ok.
- `go test -count=1 ./internal/modules/`: ok.
- `-race` is unavailable because cgo is disabled.

## Contract issues
- The `mode_change_request` task **result shape is not defined** in api-v1 §6. I chose `ModeChangeResult{approved, from_mode, install_mode, applied?}`, with a denial reported as `done` + `approved:false` rather than `failed`. INT may want to amend the contract.
- No error code exists for "the local admin did not answer". Timeout and supersede come back as a `failed` task with the error text.

## Gaps / follow-ups
- **The admin check is the caller's job.** `ConfirmModeRequest` trusts that IPC (P0-28/P0-29) has already verified a local Windows administrator (UAC). This package cannot verify it.
- **Sync folders have no `suspended_by_mode` state.** `config.SyncConfig` lacks the field, so the adapter sets `Enabled=false` and records the ids in the `kv` key `modules.suspended_sync_folders`. Resume re-enables only recorded folders, never one the user disabled. Once two-way sync (api §10) adds a real state, switch the adapter to it.
- **Which modules a folder needs:** by default every legacy folder needs `sync`. Pass a custom `requires` to also map automations to `automation`.
- **Upgrade risk:** an existing install with no `install_mode` row and no `mode.json` starts in Simple. That is inconsistent with its active sync folders, although nothing is suspended until a switch or toggle. The installer upgrade path (P0-33) should write `mode.json` = `advanced` when legacy sync configs exist.
- **Not durable:** the report loop and pending requests live in memory only. A durable outbox (P0-25) could replace the retry if desired.

## Notes for dependent tasks / INT
Wiring snippet for `Host` (P0-29/P1-29/INT). It assumes `agent.KindModules EventKind = "modules"` exists, `h.store` is the `*state.Store`, `h.eng` is the `*engine.Engine` and `h.client` is the enrolled client guarded by `h.mu`. Adapt the names to the actual fields.
```go
h.mods = modules.NewForHost(h.store.DB, filepath.Join(platform.ConfigDir(), "mode.json"), version,
	func() *client.Client { h.mu.Lock(); defer h.mu.Unlock(); return h.client },
	func(p any) { _ = h.bus.Publish(KindModules, p) })
if h.mods != nil {
	h.mods.RegisterSource(modules.NewSyncFolderSource(h.store.DB, h.eng, nil))
	go h.mods.RunReporter(h.ctx) // startup + change + daily report
}
// after successful enrolment: h.mods.Kick()
// P1-26 task loop: case wire.TaskKindModeChangeRequest: res, err := h.mods.HandleTask(ctx, task)
//   (run it in its own goroutine: it blocks until the admin answers)
// P1-24: h.mods.RegisterSource(<backup job source>) setting wire.JobStateSuspendedByMode
// API: GetMode → Snapshot; SetMode → PreviewSwitch + SetMode; ListModules → Snapshot().Modules;
//      SetModule → SetBuiltin; plus PendingModeRequest / ConfirmModeRequest (admin-verified IPC only)
```
- **Mode file path:** P0-28's `ConfigDir()` is the ProgramData `config` directory, so the mode file is `ConfigDir()\mode.json`.
- **Task loop (P1-26):** `HandleTask` can block for up to 24h. Do not run it inline in the poll loop.
- **No new dependencies.** The package now imports `sdk/client` and `desktop/internal/config`.
