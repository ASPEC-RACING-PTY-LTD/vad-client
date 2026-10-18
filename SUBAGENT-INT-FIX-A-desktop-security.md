# INT-FIX-A Desktop security and upgrade fixes (INT audit items 1–3)
Status: completed

## Goal
1. Agent API calls that touch caller-supplied paths (`PreviewConfig`, `SyncNow`, and also `SaveConfig`) must not use SYSTEM's rights for a pipe client: check the paths through `guard` and access them as the caller.
2. In service mode the agent refuses `InstallUpdate` with a typed error, so the per-user installer never runs as SYSTEM.
3. An existing install that has sync folders and no `config\mode.json` defaults to Advanced and persists `mode.json` = `advanced`. Fresh installs stay Simple.

## Changes (repo: path, created/modified/deleted, why)
All changes are in the client repo `D:/VaultDock/VaultDock`.
- `desktop/internal/agent/pathaccess.go` (created):
  - `pathAccess` per call: the caller token, the guard profile and the guard function;
  - `Host.callerAccess(ctx)`: in-process and SYSTEM callers are trusted, with no token. A pipe user gets `opts.Tokens.ForProfile(callerSID)`, and the profile root is resolved through `opts.Profiles`. It **fails closed**: no token means `access_denied`, never SYSTEM;
  - `check(cfg)`: for every path (`LocalPath`, or each entry of `Files` for `kind=files`) it calls `guard(OpRead, path, Profile{SID, Root})`, then opens the path and lists one entry while impersonating the caller;
  - `do(fn)`: impersonates the caller;
  - `CodeAccessDenied`, `ErrAccessDenied` and `ProfileLister`.
- `desktop/internal/agent/host.go` (modified):
  - new `Options`: `Service`, `Tokens`, `Guard` and `Profiles`. The defaults are `tokens.Default`, `guard.Check` and `profiles.OSSystem{}`;
  - `SaveConfig` checks the caller's paths before saving;
  - `SyncNow(id)` refuses a folder the caller can't access. `SyncNow("")` skips such folders and logs a warning;
  - `PreviewConfig` checks the paths, then runs the whole `eng.Preview` walk under the caller's impersonation.
- `desktop/internal/agent/updates.go` (modified):
  - adds `CodeUpdatesManaged`, `ErrUpdatesManaged`, `runningAsSystem` (a var so tests can override it) and `Host.serviceMode()`;
  - `InstallUpdate` refuses in service mode, and also for any call that comes over the pipe;
  - `requestInstall`, used by the auto-update loop, also refuses;
  - `launchPendingUpdate` refuses as defence in depth.
- `desktop/internal/agent/system_windows.go` / `system_other.go` (created): `processIsSystem()`. On Windows the process token user must be S-1-5-18; if the token can't be read it assumes service mode (fails closed). On other platforms it returns false.
- `desktop/internal/agent/pathaccess_test.go` (created): table tests with fake Token, Provider, Guard and Profiles for:
  - trusted in-process and SYSTEM callers;
  - a pipe user;
  - guard refusal;
  - the user's token denying access;
  - token unavailable (fail closed);
  - the walk running impersonated;
  - `kind=files` checking every file;
  - `SaveConfig` and `SyncNow` (by id, all, guarded, permitted, SYSTEM, missing);
  - `InstallUpdate` in service mode, as LocalSystem, over the pipe, and in-process (unchanged).
- `desktop/internal/ipc/ipc_test.go` (modified):
  - the test host uses a pass-through token provider and an allow-all guard;
  - `InstallUpdate` over the pipe now expects `updates_managed`;
  - new `TestPathCallsFailClosedWithoutCallerToken` checks that the refusal survives the transport.
- `desktop/internal/modules/manager.go` (modified):
  - on first run, when there is no `install_mode` row and the mode file is missing or invalid, `hasLegacySyncFolders` (`SELECT EXISTS(SELECT 1 FROM configs)` on state.db) selects Advanced;
  - the existing `persist(ctx, fresh)` then writes `mode.json`;
  - a mode file the installer wrote still wins.
- `desktop/internal/modules/manager_test.go` (modified): `TestUpgradeWithSyncFoldersDefaultsToAdvanced` covers a fresh install (simple), an upgrade with folders (advanced, with the file written and the mode persisted across reopen), and the installer's choice winning.

## Interfaces exposed
```go
package agent
const CodeAccessDenied = "access_denied"
var ErrAccessDenied *Error
const CodeUpdatesManaged = "updates_managed"
var ErrUpdatesManaged *Error // "updates for the VaultDock service are installed by the administrator installer"
type ProfileLister interface{ LocalProfiles() ([]profiles.LocalProfile, error) }
// Options additions:
Service  bool
Tokens   tokens.Provider
Guard    func(op guard.Op, path string, p guard.Profile) error
Profiles ProfileLister
```
The authorisation policy (`authz.go`) is unchanged.

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `go build ./...`: ok.
- `go vet ./...`: ok.
- `GOOS=linux go build ./...`: ok. `GOOS=linux go vet` on agent, ipc and modules: ok.
- `gofmt -l internal/agent internal/ipc internal/modules app*.go`: clean.
- `go test -count=1 ./internal/... ./cmd/...`: all ok, with no pre-existing failures. This includes keystore, backup and scheduler.

## Contract issues
None. The new error codes are IPC-internal.

## Gaps / follow-ups
- **SyncNow and automatic scans:** the engine's background scan and upload still run as SYSTEM. The caller check gates only the folder root (guard plus a read/list as the caller), so a subfolder with tighter ACLs under a folder the user can read is still uploaded with SYSTEM's rights. Full fix: the engine scans and reads per profile under `tokens.ForProfile(owner)`, which needs a config owner SID (§7.8). That is engine work, outside this task.
- **No per-config owner:** configs are machine-global, so a pipe user can still trigger a scan of another user's folder if that user can read it, and can still delete or edit configs (ownership is pending §7.8).
- **The PreviewConfig walk** runs impersonated, including its `state.Index` read for saved configs. If a new DB connection opens under the user's token and is denied, the error is ignored by the engine. The only effect is that entries show `upload` instead of `synced`.
- **`OpRead` is used for all checks** (sync is one-way). Two-way sync downloads need `OpWrite` checks when they land.
- **Bad mode file:** a mode file that is present but invalid on an upgrade with sync folders also picks Advanced, the same as a missing file.

## Notes for dependent tasks / INT
- `cmd/vaultdock-agent/main.go` (not owned) should pass `Service: isSvc` in `agent.Options`. Service mode is already detected automatically when the process is LocalSystem, and any pipe call to `InstallUpdate` is refused regardless.
- The UI (P0-29) should hide or disable "Install update" when it gets `updates_managed`, and show `access_denied` messages verbatim. These are not "requires administrator".
- Host wiring of `modules.NewForHost` (P1-28) is still not done in `host.go`; the upgrade default works once it is wired.
- No new dependencies; `go.mod` was not touched.
