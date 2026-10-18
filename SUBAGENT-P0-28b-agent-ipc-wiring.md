# P0-28b (DSK-A) Wire the IPC server and §7.6 authorisation policy into the agent service
Status: completed

## Goal
- Start the `ipc` named-pipe server from `cmd/vaultdock-agent` through the P0-27 `ServeIPC` hook, closing it before the host on stop.
- Add the §7.6 `Authorize` policy in `internal/agent`, with table tests, and plug it into the server.

## Changes (repo: path, created/modified/deleted, why)
All changes are in the client repo `VaultDock`.
- `desktop/internal/agent/authz.go` (created):
  - `CodeForbidden`, `ErrForbidden`;
  - the `Access` classes and a table that classifies every wire method, including the reserved ones;
  - `MethodAccess`, `IsSystem`, `IsAdmin`, `Authorize`.
- `desktop/internal/agent/authz_test.go` (created):
  - a 41-case table over fabricated `caller.Caller` values;
  - a reflection test that every `agent.API` method has an explicit class;
  - `Access.String`.
- `desktop/cmd/vaultdock-agent/ipc_wire.go` (created):
  - `init()` sets `ServeIPC = serveIPC`;
  - `serveIPC` runs `ipc.Listen(PipeName)`, then `ipc.NewServer(api, {Log, Authorize: authorizeLogged(log)})`, then `Serve`;
  - on ctx cancel it calls `srv.Close()` (which waits for all connection handlers) and only then returns.
- `desktop/cmd/vaultdock-agent/main.go`: **not modified**. The shutdown order already holds:
  1. `agentHandler.Stop` cancels the IPC ctx;
  2. it waits on `ipcDone`, which is closed only after `serveIPC` returns, which is after `srv.Close()`;
  3. it calls `host.Close()`.

## Interfaces exposed
```go
package agent
const CodeForbidden = "forbidden"
var ErrForbidden *Error // errors.Is matches any refusal (Error.Is compares Code)
type Access int
const (AccessDenied Access = iota; AccessRead; AccessUser; AccessAdmin) // String(): denied|read|user|admin
func MethodAccess(method string) Access
func IsSystem(c caller.Caller) bool // c.System && c.SID == "S-1-5-18"
func IsAdmin(c caller.Caller) bool  // IsSystem || (Admin && Elevated && real user SID)
func Authorize(ctx context.Context, c caller.Caller, method string) error // ipc.ServerOptions.Authorize
```
Refusals are `*agent.Error{Code: "forbidden", Message: "forbidden: <reason>"}`.

`package main`: `var pipeName = ipc.PipeName`, `serveIPC`, and `authorizeLogged`.

### Policy (MASTER_PLAN §7.6, conservative where the plan is vague)
1. **Unknown method** is denied for everyone, including SYSTEM (default deny).
2. **SYSTEM** (the token flag and SID S-1-5-18 together) may call everything.
3. **Identity allowlist:** every other caller must have a real user SID: `S-1-5-21-<decimal…>` (local or domain) or `S-1-12-1-<decimal…>` (Entra ID). Everything else is denied:
   - anonymous (S-1-5-7) and network (S-1-5-2);
   - null and world SIDs;
   - LocalService and NetworkService (S-1-5-19/20);
   - virtual and service accounts (S-1-5-80/90-…);
   - empty or malformed SIDs;
   - a `System` flag paired with a non-system SID.
4. **read** (any interactive user):
   - Bootstrap, Snapshot, ListConfigs, ListQueue, ListHistory, ListAutomations, GetSettings, UpdateStatus;
   - Subscribe, Unsubscribe;
   - reserved: ListBackupJobs, ListSnapshots, Readiness, ListIssues, KeyStatus, GetMode, ListModules, ListProfiles.
5. **user** (any interactive user; the item must be their own):
   - SaveConfig, DeleteConfig, SyncNow, PreviewConfig, RetryFailed, RefreshAutomations;
   - reserved: SaveBackupJob, DeleteBackupJob, RunBackup, CancelBackup, ResolveIssue, PlanRestore, StartRestore, CancelRestore, GiveConsent, RevokeConsent.
6. **admin** (`Admin && Elevated`, or SYSTEM):
   - ProbeServer, Connect, CancelEnrollment, Disconnect;
   - SaveSettings, SetPaused;
   - CheckForUpdates, InstallUpdate;
   - reserved: ConfirmRecoveryKey, SetMode, SetModule.

**Conservative choices to review:**
- **SetPaused is admin.** Today it pauses the whole machine's engine; make it user-level once pause is per-profile.
- **ProbeServer is admin.** It makes the SYSTEM service issue outbound requests to a URL the caller supplies (an SSRF-like risk), and it is part of the connect flow.
- **CheckForUpdates is admin** (it is in the updates category). UpdateStatus stays read.
- **SaveSettings is admin as a whole**, although it includes per-user UI preferences (StartMinimized, CloseToTray, Notifications); see the gaps.
- **ConfirmRecoveryKey is admin**, because the key is machine-wide.
- **ListProfiles and KeyStatus are read**; the Host must filter them per caller.
- **The §7.6 "UI confirmation"** for destructive calls is a UI concern. The policy enforces only the group and elevation requirement.

### Logging
- Every authorised call logs at debug: `ipc call` with sid, session and method.
- Every refusal logs at warn: `ipc call refused` with sid, session, pid, method, admin and elevated.
- Per connection, `ipc/server.go` already logs `ipc client connected` at debug with sid, session and pid. The default identifier is unexported, so the wiring file cannot add its own per-connection hook without editing ipc.
- Note: `runAgent` opens the log at Info, so debug lines are not emitted today.

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `gofmt -l internal/agent cmd/vaultdock-agent`: clean.
- `go build ./...`: ok. The first attempt hit a transient "could not import sync/atomic" error in root `main.go` (not my file, probably a parallel task or the cache); the retry was clean.
- `go vet ./internal/agent/ ./cmd/vaultdock-agent/`: ok. `GOOS=linux go vet` on the same packages: ok.
- `go test -count=1 ./internal/agent/ ./cmd/vaultdock-agent/ ./internal/ipc/...`: all ok.
- A temporary smoke test was run and then deleted. It was not committed and is not one of my owned files. It covered:
  - `serveIPC` on a random real pipe with a fake API;
  - `ipc.Dial`, then Bootstrap, which succeeded;
  - Disconnect from an unelevated user, which returned `forbidden` and matched `errors.Is(err, agent.ErrForbidden)`;
  - cancelling ctx, after which `serveIPC` returned nil promptly.

## Contract issues
None. IPC is internal.

## Gaps / follow-ups
- **No network or anonymous flag on `caller.Caller`.** The denial relies on:
  - the pipe (`FILE_PIPE_REJECT_REMOTE_CLIENTS` plus the `NU` deny ACE);
  - the SID allowlist above.

  A local user who reaches the pipe over a loopback network logon would still have an S-1-5-21 SID. Suggest that ipc add `Caller.Network` (the token has S-1-5-2) and `Caller.Interactive` (S-1-5-4) in `identifyConn`, and that `Authorize` then require `!Network`.
- **No ownership checks yet.** "Own profile" items are not enforced, because Host configs are machine-global until §7.8 profiles land. The Host must check `caller.From(ctx)` against the item's owner SID. In-process calls carry no caller and should be treated as trusted.
- **PreviewConfig and SyncNow run as SYSTEM** over paths the caller supplies. Until user-token access (C7) and the guard are applied in the Host, a standard user can enumerate folders they cannot read. This is a Host gap, not a policy gap.
- **Settings split:** split per-user UI preferences out of `config.Settings`, or add a user-level `SaveUserPrefs`, so that non-admins can change tray and notification options.
- **No automated test of the wiring** in `cmd/vaultdock-agent`: a test file there was not in my owned set. INT could add the smoke test described above as `ipc_wire_windows_test.go`, using `pipeName` override and a fake `agent.API`.

## Notes for dependent tasks / INT
- **When you add an `agent.API` method,** also add it to `methodAccess` in `authz.go`. `TestEveryAPIMethodClassified` fails otherwise, and unclassified methods are denied over IPC.
- **The UI (P0-29)** should treat the `forbidden` code as "requires administrator", for example by offering an elevated helper or disabling the control.
- **Logging:** consider a `-log-level` flag for `run` so the per-call debug logging can be enabled.
- No new dependencies; `go.mod` was not touched.
