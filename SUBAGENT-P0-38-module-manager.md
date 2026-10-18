# P0-38 DSK-MOD: module manager skeleton
Status: completed

## Goal
Create `desktop/internal/modules` with a `Manager` (MASTER_PLAN §7.7, api-v1 §6A) that:
- persists the install mode and per-module state in `state.db`;
- reads and writes `config/mode.json`;
- toggles the built-ins `sync`, `automation` and `profiles`;
- switches Simple↔Advanced with a preview and never deletes anything;
- keeps the components `vss`, `discovery` and `reinstall` as wanted behind a `Downloader` seam (no-op stub);
- exposes UI gating (`Enabled`, `Snapshot`, change callback);
- builds and sends the `PUT /device/modules` body through an injected sender.

## Changes (repo: path, created/modified/deleted, why)
- VaultDock: `desktop/internal/modules/manager.go` (created). Manager, local states, persistence, mode file, switching, built-in toggles, gating and report.
- VaultDock: `desktop/internal/modules/download.go` (created). `Downloader` interface, `NoopDownloader` and `ErrDownloadUnavailable`.
- VaultDock: `desktop/internal/modules/manager_test.go` (created). Tests:
  - mode persistence: first run from the mode file, default simple, bad file ignored, DB authoritative afterwards;
  - switching up and down with the preview, including persistence across a reopen and resume on the way back up;
  - the never-delete invariant (items and module rows survive repeated switches) and error cases;
  - built-in toggle with suspend and resume;
  - the exact `PUT /device/modules` JSON with a fake downloader (installed and failed components) and the mapping of local states to wire states;
  - the no-op downloader.

## Interfaces exposed
```go
package modules // desktop/internal/modules; sdkmod = sdk/modules

type State string // enabled, disabled, suspended, wanted, installing, failed (suspended/wanted are local-only)
const EventKind = "modules"
var ErrUnknownModule, ErrNotBuiltin, ErrInvalidMode, ErrModeRequired, ErrDownloadUnavailable error

type ModuleInfo struct { ID sdkmod.ID; Kind sdkmod.Kind; Version string; State State; Error string; UpdatedAt int64 } // json snake_case
type ModulesState struct { InstallMode sdkmod.Mode; CoreVersion string; ChangedAt, ReportedAt int64; Modules []ModuleInfo } // contract table order
type AffectedItem struct { Kind, ID, Name string; Requires []sdkmod.ID; Reason string }
type ItemSource interface {
    Affected(ctx context.Context, disabling []sdkmod.ID) ([]AffectedItem, error)
    Suspend(ctx context.Context, items []AffectedItem) error // set suspended_by_mode; never delete
    Resume(ctx context.Context, enabled []sdkmod.ID) error   // clear suspended_by_mode where all requires are enabled
}
type SwitchPreview struct { From, To sdkmod.Mode; Enable, Want, Suspend []sdkmod.ID; Affected []AffectedItem; NoDeletes bool }
type Sender func(ctx context.Context, body wire.ModuleReport) error
type Downloader interface { Install(ctx context.Context, id sdkmod.ID) (version string, err error) }
type NoopDownloader struct{} // always ErrDownloadUnavailable → component stays "wanted"
type ModeFile struct { Mode sdkmod.Mode `json:"mode"` } // config/mode.json: {"mode":"simple|advanced"}

type Options struct {
    DB *sql.DB; ModeFile, CoreVersion string; Downloader Downloader; Sources []ItemSource
    Send Sender; OnChange func(ModulesState); Log *slog.Logger; Now func() time.Time
}
func New(ctx context.Context, opts Options) (*Manager, error)
func (m *Manager) Mode() sdkmod.Mode
func (m *Manager) Enabled(id sdkmod.ID) bool
func (m *Manager) Snapshot() ModulesState
func (m *Manager) PreviewSwitch(ctx context.Context, target sdkmod.Mode) (SwitchPreview, error)
func (m *Manager) SetMode(ctx context.Context, target sdkmod.Mode) (SwitchPreview, error)
func (m *Manager) SetBuiltin(ctx context.Context, id sdkmod.ID, enabled bool) error
func (m *Manager) ReportBody() wire.ModuleReport
func (m *Manager) Report(ctx context.Context) error // Send + install_mode.reported_at
func ReadModeFile(path string) (ModeFile, error)
func WriteModeFile(path string, mode sdkmod.Mode) error // atomic (tmp + rename)
```

Behaviour:
- **First run** (no `install_mode` row): the mode comes from `ModeFile`, defaulting to simple when the file is missing or invalid. Module defaults:
  - Simple: core enabled, everything else disabled.
  - Advanced: built-ins enabled, components wanted.
- **After first run** the DB is authoritative. `SetMode` rewrites `mode.json` so a reinstall preselects the current mode.
- **Rows:** module rows are only ever upserted, never deleted. Core is always enabled at `CoreVersion`.
- **Simple→Advanced:**
  1. built-ins → enabled;
  2. components → wanted, then `Downloader.Install` (the stub leaves them wanted; any other error → failed with the error text);
  3. `Resume` on the sources, the `OnChange` callback and a best-effort `Report`.
- **Advanced→Simple:**
  1. `Suspend` items from the sources, before the modules are turned off;
  2. modules → suspended (version kept, so component binaries are considered kept);
  3. persist, notify and report.
  `PreviewSwitch` returns the same plan plus `Affected` without side effects.
- **Built-ins:** `SetBuiltin(id, false)` suspends the items that depend on that module. Enabling a built-in requires Advanced.
- **Report:** sends the modules in contract order, with local states mapped to wire states:
  - enabled → enabled;
  - installing → installing;
  - failed → failed;
  - disabled, suspended and wanted → `disabled`.

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `gofmt -l internal/modules`: clean.
- `go build ./...`: ok.
- `go vet ./internal/modules/`: ok.
- `go test -count=1 ./internal/modules/`: ok.

## Contract issues
- api-v1 §6A report states are only `enabled|disabled|installing|failed`. There is no way to say "wanted, not installed yet" or "suspended by mode, binaries kept"; both are reported as `disabled`. If the server or web UI needs to distinguish them (for example, to show a "downloading soon" badge), that needs a contract amendment, which is INT's call. No change was made.

## Gaps / follow-ups
- **No `ItemSource` implementations exist yet.** Backup jobs (P1), sync folders and automations, and profiles must each implement `ItemSource` and set `suspended_by_mode` (`wire.JobStateSuspendedByMode` / `SyncStateSuspendedByMode`). Until then, switching suspends only modules.
- **Mode-switch steps not done here:**
  - the 30-day cleanup of kept component binaries;
  - the UAC/admin confirmation, which belongs to IPC/P0-28;
  - the confirmation step between preview and apply, which is the caller's (UI) responsibility.
- **Downloads are synchronous.** `Downloader.Install` runs inside `SetMode`. When the Phase 3 pipeline lands, it may want to set `installing` and run asynchronously; that would need a `setState` hook or a method such as `SetComponentState`.
- **No daily report scheduler.** The caller should call `Report` at enrolment and daily; failures are only logged. An outbox (P0-25) would be the durable path.

## Notes for dependent tasks / INT
- **Agent wiring** (P0-29 or INT; this task does not touch `agent`):
  - Add `KindModules EventKind = "modules"` (payload `modules.ModulesState`) to `agent/events.go`.
  - In `Host`, construct the manager with:
    ```go
    modules.New(ctx, modules.Options{
        DB:          store.DB,
        ModeFile:    <platform ProgramData config dir>/mode.json,
        CoreVersion: version,
        Send:        <P1-09 client PutDeviceModules>,
        OnChange:    func(s modules.ModulesState) { bus.Publish(KindModules, s) },
        Log:         log,
        Sources:     ...,
    })
    ```
  - Implement the reserved API methods:
    - `GetMode` → `Snapshot`;
    - `SetMode` → a preview call plus an apply call (consider splitting it into `PreviewMode` and `SetMode`);
    - `ListModules` → `Snapshot().Modules`;
    - `SetModule` → `SetBuiltin`.
- **Import direction:** `modules` must not import `agent`, because the agent will import modules; that is why events use the `OnChange` callback.
- **P0-28:** pass the ProgramData `config\mode.json` path. The manager creates the parent directory with 0700 if needed, but the installer normally creates it with the right ACL.
- **P1-09:** the sender signature is `func(ctx, wire.ModuleReport) error` → `PUT /device/modules`, expecting `204`.
- **Dependencies:** none added. The package imports the stdlib, `sdk/modules`, `sdk/wire`, and `desktop/internal/state` (tests only).
