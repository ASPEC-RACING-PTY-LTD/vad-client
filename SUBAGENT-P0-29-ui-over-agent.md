# P0-29 (DSK-I) Desktop UI talks to the agent through agent.API
Status: completed

## Goal
- The Wails `App` keeps only UI-session concerns and forwards everything else to `agent.API`. Agent bus events are bridged to the existing Wails event names.
- `main.go` uses the IPC client when the VaultDockAgent service is reachable; otherwise it uses an in-process `agent.Host`. Exactly one engine runs, which fixes the dual-engine race that P0-28 warned about.
- The Wails bindings and frontend callers are updated for the new shapes.
- `ui.tsx` and `index.css` mirror the P0-21 web versions (B6 parity).

## Changes (repo: path, created/modified/deleted, why)
All changes are in the client repo `VaultDock`, under `desktop/`.

**Go**
- `app.go` (rewritten): core and lifecycle.
  - `UpdateRepo`, `App`, `NewApp(log, api)`, `startup` and `shutdown`.
  - **Event bridge:** `bridgeEvents` and `handleEvent`.
  - `currentSnapshot` (for the tray).
  - UI-session helpers: `ShowWindow`, `Quit`, `OpenURL`, `OpenFolder`, `OpenLogsFolder`, `openFolder`.
  - The engine, state store, settings file, device key, enrolment and `friendlyNetErr` logic is all removed from `main`; it lives in `internal/agent` (P0-26).
- `app_sync.go` (created):
  - forwards configs, `SyncNow`, `PreviewConfig`, transfers (`GetSnapshot`, `SetPaused`, `RetryFailed`, `ListQueue`, `ListHistory`) and automations to the agent;
  - keeps `PickFolder`, `PickFiles` and `SuggestFolders` local (UI session).
- `app_settings.go` (created):
  - `GetBootstrap`, `ProbeServer`, `Connect`, `CancelEnrollment`, `Disconnect`, `GetSettings` and `SaveSettings` forward to the agent;
  - `OpenWebInterface` stays local and reads `ServerURL` from the agent's settings.
- `app_update.go` (rewritten): `GetUpdateStatus`, `CheckForUpdates` and `InstallUpdate` are thin forwarders. The update loop now lives in `agent/updates.go`.
- `main.go` (modified):
  - `connectAgent(log, quit)` dials the pipe (2 s). When that fails it calls `newInProcAgent` and returns an error only if the in-process Host cannot start.
  - The `state.Open`/`engine.New` calls are gone.
  - `close` is wrapped in `sync.OnceFunc`. It runs from `OnShutdown` (so the in-process Host's `Close` launches a pending update installer) and again from the deferred call.
  - `RequestExit` (in-process) → `App.Quit` through an `atomic.Pointer[App]`.
  - `newInProcAgent` passes `UpdateDir = filepath.Dir(LogDir())/updates`, the same directory as the legacy App.
  - `StartHidden` and `HideWindowOnClose` now come from `app.GetSettings()`.
  - **Tray:**
    - it uses `currentSnapshot()` instead of `eng`;
    - Pause relies on the agent's `settings` event, not a manual `EventsEmit`;
    - in service mode the Quit tooltip says the service keeps syncing.
  - The second-instance callback uses `app.ShowWindow()`.

**Bindings (hand-edited)**
The `wails` CLI is not installed and there is no network access, so `wails generate module` could not run. These files were edited by hand to match the Go signatures exactly:
- `frontend/wailsjs/go/main/App.d.ts`:
  - `Connect(arg1:agent.ConnectRequest):Promise<agent.EnrollView>`;
  - `ProbeServer(arg1:agent.ProbeRequest):Promise<agent.ProbeResult>`;
  - `GetBootstrap():Promise<agent.Bootstrap>`;
  - `PreviewConfig(...):Promise<agent.PreviewResult>`;
  - the `main` model import is replaced by `agent`.
- `frontend/wailsjs/go/main/App.js`: `Connect` and `ProbeServer` now take one argument.
- `frontend/wailsjs/go/models.ts`:
  - The `main` namespace is removed.
  - A new `agent` namespace holds `ServerInfo`, `EnrollView`, `Bootstrap`, `ConnectRequest`, `PreviewResult`, `ProbeRequest` and `ProbeResult`.
  - `Bootstrap` gains `protocol_version: number` and `server_info?: ServerInfo`.
- INT should run `wails generate module` once on a machine with the CLI; the diff should be cosmetic (ordering and whitespace) at most.

**Frontend (`frontend/src`)**
- `screens/Connect.tsx`: `ProbeServer({ url, pin: withPin })` and `Connect({ url, login, password, pin })`; types `main.*` → `agent.*`.
- `App.tsx`, `screens/Overview.tsx`, `screens/Configs.tsx`, `screens/Settings.tsx`: type imports `main` → `agent`. `boot.server_info?.used`/`.quota` work unchanged with the typed `ServerInfo`.
- `ui.tsx` and `index.css`: copied verbatim from `vad-server/web/src/components/ui.tsx` and `web/src/index.css`. Both pairs were byte-identical at HEAD before P0-21, so no desktop-only edits were lost.
- **Form audit:** the only desktop `<form>`s are the two in `screens/Connect.tsx`. Every `Button` in them has an explicit `type` (`submit`/`button`). The other raw `<button>`s are outside forms.
- `package.json`: unchanged.

## Interfaces exposed (exact Go/TS signatures, routes, schema others depend on)
```go
func NewApp(log *slog.Logger, api agent.API) *App
// App fields set by main: toasts *toasts; localAutoStart bool; onSettings func(config.Settings)

// Bound methods (Wails), all forwarding to agent.API with App.bg (cancelled on shutdown):
GetBootstrap() (agent.Bootstrap, error)
ProbeServer(req agent.ProbeRequest) (agent.ProbeResult, error)
Connect(req agent.ConnectRequest) (agent.EnrollView, error)
CancelEnrollment() error
Disconnect() error
ListConfigs() ([]engine.ConfigView, error)
SaveConfig(c config.SyncConfig) (config.SyncConfig, error)
DeleteConfig(id string) error
SyncNow(id string) error
PreviewConfig(c config.SyncConfig) (agent.PreviewResult, error)
GetSnapshot() (engine.Snapshot, error)
SetPaused(p bool) error
RetryFailed(id int64) (int64, error)
ListQueue(status string) ([]state.Item, error)
ListHistory(status string) ([]state.HistoryEntry, error)
ListAutomations() ([]engine.AutomationView, error)
RefreshAutomations() ([]engine.AutomationView, error)
GetSettings() (config.Settings, error)
SaveSettings(s config.Settings) (config.Settings, error)
GetUpdateStatus() (updater.Status, error)
CheckForUpdates() (updater.Status, error)
InstallUpdate() error
// UI-session only:
PickFolder(title string) (string, error); PickFiles() ([]string, error); SuggestFolders() map[string]string
OpenFolder(p string); OpenLogsFolder(); OpenURL(u string); OpenWebInterface(path string); ShowWindow(); Quit()

func connectAgent(log *slog.Logger, quit func()) (*agentConn, error)
func newInProcAgent(ctx context.Context, log *slog.Logger, quit func()) (agent.API, func(), error)
```

**Event bridge (agent kind → Wails event)**

| Agent kind | Wails event |
|---|---|
| `snapshot` | `"snapshot"` with an `engine.Snapshot` payload; also cached for the tray |
| `connection{state}` | an event named after the state: `"connected"`, `"revoked"` or `"disconnected"`. The frontend listens to the first two. |
| `enrollment`, `update` | 1:1, payload verbatim |
| `settings` | `"settings"`; also updates the tray via `onSettings` |
| `notification` | a Windows toast (`toasts.Show`) |
| `shutdown` | logged only |
| `log` | not subscribed |

Payloads are forwarded as the event's raw JSON (`json.RawMessage`), so the frontend receives the same shapes as before.

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `cd frontend && npm run build` (`tsc -b && vite build`): ok, no type errors.
- `gofmt -l` on the owned Go files: clean. `e2e/engine_e2e_test.go` is listed by gofmt; it is not mine and was not touched.
- `go build ./...`: ok.
- `go vet . ./internal/...`: ok.
- `go vet -tags bindings .` (the binding-generation build): ok.
- `go test ./internal/...`: all ok.
- `cd e2e && go test ./...`: ok (18.0 s).
- Not run: the Wails app itself (no `wails` CLI, and it would be a long-running GUI). The real service-mode path was not run either; it needs an installed service.

## Contract issues
None. The agent API and IPC are internal, not part of `api-v1.md`.

## Gaps / follow-ups
- **Bindings were hand-edited** (no `wails` CLI). Regenerate them with `wails generate module` when convenient.
- **No Go unit test for `App`.** `app_test.go` is not an owned path. The bridge could be tested with `agent.NewInProc` over a Host and a fake emit hook.
- **Service mode and updates:** `InstallUpdate` forwards to the service. The service's updater would launch the per-user NSIS installer as SYSTEM, which is unsafe (see P0-27). The service defaults `AutoUpdates` off, but the manual Settings → Install button still reaches it. P1-30 or INT should make the service return `unavailable`/`not_implemented` until the admin installer exists, or the UI should hide the button in service mode.
- **Service mode and autostart:** the agent does not manage autostart there (its `AutoStart` hook is nil). The UI therefore applies `auto_start` to its own per-user Run key (`localAutoStart`) and overlays `AutoStartEnabled()` in `GetSettings`/`GetBootstrap`.
- **Service restart:** after the agent service restarts, the IPC client reconnects and re-subscribes, but the UI does not re-fetch Bootstrap until the next `connected`/`settings` event.
- **Service mode logs:** `OpenLogsFolder` opens the UI's own log directory. The agent's log is in `%ProgramData%\VaultDock\logs`.
- **The UI ↔ agent protocol version is not checked.** `Bootstrap.protocol_version` is available, but the IPC handshake already enforces the version.

## Notes for dependent tasks / INT
- **Dev mode:** `wails dev` with no service runs in-process. The Host uses the per-user `DataDir` with the same `settings.json`, `device.key` and `state.db` formats, so existing installs carry over.
- **Exactly one engine:**
  - With the service present, the UI no longer opens `state.db` or starts an engine.
  - In-process, the only engine is the Host's.
  - The P0-28 "dual engine" warning in `main.go` is gone.
- **Removed from package `main`:** `dataPath`, `saveKey`/`loadKey`, `friendlyNetErr`, the `Bootstrap`/`EnrollView`/`ProbeResult`/`PreviewResult` types, and the update loop. Any external reference should use `internal/agent`.
- No new dependencies. `package.json`, `go.mod` and `go.sum` are untouched by this task.
