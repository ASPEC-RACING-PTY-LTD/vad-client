# INT-FIX-D Agent host wiring (service flag + module manager)
Status: completed

## Goal
1. `cmd/vaultdock-agent` passes `Service: isSvc` to `agent.Options`.
2. Wire the P1-28 module manager into `agent.Host`, so that at host start:
   - the install mode is resolved, including the INT-FIX-A upgrade default (Advanced when legacy sync folders exist), and `mode.json` is persisted;
   - the `PUT /device/modules` report loop runs.
3. Add tests.

## Changes (repo: path, created/modified/deleted, why)
All in VaultDock, under `desktop/`:
- `internal/agent/host_modules.go` (created):
  - `KindModules` event kind;
  - `modeFilePath()`: `Options.ModeFile`, else `DataDir\config\mode.json`;
  - `startModules()`: calls `modules.NewForHost(st.DB, modeFile, engine.Version, currentClient, publish KindModules)`, then `RegisterSource(NewSyncFolderSource(st.DB, eng, nil))`, then runs `RunReporter(h.ctx)` through `goLoop`, so `Close` waits for it. If the manager can't be created, it logs a warning and `Start` still succeeds;
  - `Modules()` accessor, `currentClient()`, `kickModules()`.
- `internal/agent/host.go` (modified):
  - new `Options.ModeFile`;
  - new `Host.mods` field;
  - `Start` calls `startModules()` after the engine starts;
  - `kickModules()` runs after `restoreConnection` connects and after enrolment is approved, so the first report isn't held back by the not-connected backoff.
- `internal/agent/host_modules_test.go` (created), covering:
  - Start creates the manager: none before Start, Simple on a fresh install, `mode.json` written, Close doesn't hang;
  - an upgrade with an existing sync folder and no mode file starts in Advanced and writes `mode.json` = advanced;
  - `SetMode` publishes a `modules` event.
- `cmd/vaultdock-agent/main.go` (modified): new `agentOptions(cl, log, isSvc)` builds the host options, now including `Service: isSvc`.
- `cmd/vaultdock-agent/main_test.go` (modified): `TestAgentOptionsServiceFlag` checks that the service flag is set and survives `newAgentHandler`.

## Interfaces exposed
```go
package agent
const KindModules EventKind = "modules"   // payload modules.ModulesState
// Options addition:
ModeFile string // default DataDir\config\mode.json
func (h *Host) Modules() *modules.Manager // nil before Start / when unavailable
```

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `gofmt -l internal/agent cmd`: clean.
- `go build ./...`: ok.
- `GOOS=linux go build ./...`: ok.
- `go vet ./...`: ok.
- `go test -count=1 ./internal/agent/... ./internal/ipc/... ./internal/modules/... ./cmd/...`: all ok.

## Contract issues
None.

## Gaps / follow-ups
- **`mode_change_request` tasks are not routed yet.** `Host` has no device task loop yet (P1-26). When one is added, route the task to `h.Modules().HandleTask` in its own goroutine, because it blocks until the admin answers.
- **No API methods for modules yet.** The reserved names in `api.go` (`GetMode`, `SetMode`, `ListModules`, `SetModule`, plus `ConfirmModeRequest`) have no Host methods. `ListModules` is already in `authz.go`.
- **No backup job `ItemSource` registered.** The P1-24 source should be added with `h.Modules().RegisterSource(...)`.
- **Mode file location:** the service's DataDir is `service.ProgramDataDir()` = `%ProgramData%\VaultDock`, so the default path equals `platform.ConfigDir()\mode.json` unless `VAULTDOCK_DATA_DIR` is set.

## Notes for dependent tasks / INT
- `KindModules` is declared in `host_modules.go`, not `events.go`, which this task doesn't own. INT may move it there.
- The per-user desktop app's Host gets a manager too, with its mode file at `<user DataDir>\config\mode.json`.
- No new dependencies; `go.mod` was not touched.
