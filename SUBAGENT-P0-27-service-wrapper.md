# P0-27 (DSK-A) Windows service wrapper, RequiredPrivileges, cmd/vaultdock-agent
Status: completed

## Goal
- Add a Windows service runner and SCM install/configure helpers in `desktop/internal/service`, using x/sys `svc`, `svc/mgr` and `svc/eventlog`.
- Add the `desktop/cmd/vaultdock-agent` binary. It runs `agent.Host` (P0-26) as the `VaultDockAgent` service, or in the console.
- Leave a hook for the P0-28 IPC server.

## Changes (repo: path, created/modified/deleted, why)
All files are in the client repo `VaultDock`, and all were created by this task.

- `desktop/internal/service/service.go`:
  - `Handler`, `ExitNotifier`, `Options`, `Run`;
  - the session event types;
  - the platform-independent run core (`runUntil`, `stopWithTimeout`).
- `desktop/internal/service/console.go`: `RunInteractive`, which stops on Ctrl+C, SIGTERM or an exit request.
- `desktop/internal/service/log.go`: `OpenLog(dir, level, extra...)`. It writes a JSON slog to `dir/agent.log`, rotating at 10 MiB and keeping 3 old files.
- `desktop/internal/service/privileges.go`:
  - privilege constants;
  - the `SimplePrivileges`, `AdvancedExtraPrivileges` and `ProcessLaunchPrivileges` sets;
  - `PrivilegesForMode` and `NormalizePrivileges`. These validate names against a closed allowlist and de-duplicate them.
- `desktop/internal/service/manage.go`: `Config`, the recovery policy, `Status`, `ProgramDataDir` and `DefaultLogDir`.
- `desktop/internal/service/service_windows.go`: the SCM runner.
  - Lifecycle: StartPending, then Running.
  - Accepted controls: Stop, Shutdown, **PreShutdown** and SessionChange.
  - Stopping:
    - it reports StopPending with an increasing checkpoint every 2 s;
    - `Handler.Stop` gets the timeout context;
    - after Stop, the ctx passed to Start is cancelled.
  - Exit codes are service-specific and trigger recovery: 1 = start failed, 2 = stop failed or timed out.
  - Start, stop and failure entries go to the Application event log.
  - `WTSSESSION_NOTIFICATION` is decoded into a `SessionEvent`.
  - `IsWindowsService`, `IsElevated`, and `programData()` (KnownFolder, not the environment).
- `desktop/internal/service/manage_windows.go`:
  - `Install`, which creates the service, or reconfigures it on `ERROR_SERVICE_EXISTS`:
    - own process, LocalSystem, auto start + **delayed**;
    - **SidType unrestricted**;
    - quoted binary path;
    - **RequiredPrivileges** via `ChangeServiceConfig2(SERVICE_CONFIG_REQUIRED_PRIVILEGES_INFO)`. x/sys has no struct for it, so a local MULTI_SZ struct is used;
    - recovery restart after 5 s / 30 s / 120 s, reset after 24 h, with the non-crash failure flag;
    - preshutdown timeout of 60 s;
    - an event log source.
  - `Configure`, `SetRequiredPrivileges`, `Uninstall` (stop, wait, delete, remove the event source), `StartService` and `StopService`.
  - `Query`, which opens SCM with `SC_MANAGER_CONNECT` and `SERVICE_QUERY_STATUS|QUERY_CONFIG`, so it **needs no elevation**. It also reads the RequiredPrivileges back.
- `desktop/internal/service/service_other.go`: non-Windows stubs that return `ErrUnsupported`. `RunInteractive` works everywhere.
- `desktop/internal/service/service_test.go`, `manage_windows_test.go`: tests (see Commands).
- `desktop/cmd/vaultdock-agent/main.go`:
  - Subcommands: `run`, `service` (an alias of run; the installer spike's SCM binary path is `"…\vaultdock-agent.exe" service`), `install`, `configure`, `uninstall`, `start`, `stop`, `status` and `help`.
  - Exit codes: usage error 2, not elevated 3, operation failure 1.
  - `install`, `configure`, `uninstall`, `start` and `stop` check `IsElevated` first and fail with a clear message when not elevated.
  - `run`:
    - detects SCM vs console with `svc.IsWindowsService`;
    - default data dir is `%ProgramData%\VaultDock`, and logs go to `<data>\logs\agent.log` (plus stderr in console mode);
    - builds `agent.NewHost(Options{Log, DataDir, UpdateRepo, AutoUpdates, RequestExit})`. `RequestExit` closes the `ExitNotifier` channel, which makes the runner do a normal stop;
    - `Stop` cancels the IPC server and then calls `Host.Close()`.
- `desktop/cmd/vaultdock-agent/main_test.go`: tests for argument parsing, the admin command set, the service config builder and realMain's usage exit codes.

## Interfaces exposed (exact Go/TS signatures, routes, schema others depend on)
```go
package service // github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/service

const DefaultName = "VaultDockAgent"; DefaultDisplayName = "VaultDock Agent"; DefaultDescription
const DefaultStopTimeout = 30 * time.Second
var ErrUnsupported, ErrNotInstalled error

type Handler interface {
	Start(ctx context.Context) error // ctx cancelled after Stop returns/times out
	Stop(ctx context.Context) error  // ctx carries StopTimeout
	SessionChange(ev SessionEvent)   // on the control loop; must not block
}
type ExitNotifier interface{ ExitRequested() <-chan struct{} } // optional
type Options struct {
	Log         *slog.Logger
	StopTimeout time.Duration
	EventLog    bool
}
func Run(name string, h Handler) error // = RunWithOptions(name, h, Options{EventLog: true})
func RunWithOptions(name string, h Handler, opts Options) error
func RunInteractive(ctx context.Context, h Handler, opts Options) error
func IsWindowsService() (bool, error)
func IsElevated() bool

type SessionEventType uint32 // ConsoleConnect..SessionTerminate (WTS_* values), String()
type SessionEvent struct{ Type SessionEventType; SessionID uint32 }

func OpenLog(dir string, level slog.Leveler, extra ...io.Writer) (*slog.Logger, io.Closer, error)
const LogFileName = "agent.log"
func ProgramDataDir() string // %ProgramData%\VaultDock
func DefaultLogDir() string  // %ProgramData%\VaultDock\logs

// Privileges
const SeChangeNotifyPrivilege, SeImpersonatePrivilege, SeTcbPrivilege, SeCreateGlobalPrivilege,
	SeBackupPrivilege, SeRestorePrivilege, SeManageVolumePrivilege,
	SeAssignPrimaryTokenPrivilege, SeIncreaseQuotaPrivilege string
var SimplePrivileges        // SeChangeNotify, SeImpersonate, SeTcb, SeCreateGlobal
var AdvancedExtraPrivileges // SeBackup, SeManageVolume
var ProcessLaunchPrivileges // SeAssignPrimaryToken, SeIncreaseQuota (not in any default set)
func PrivilegesForMode(mode modules.Mode, extra ...string) ([]string, error)
func NormalizePrivileges(privs []string) ([]string, error)

// SCM management
type Config struct {
	Name, DisplayName, Description, ExePath string
	Args               []string
	Privileges         []string
	PreshutdownTimeout time.Duration
}
var RecoveryDelays []time.Duration
var RecoveryResetPeriod time.Duration
type Status struct {
	Name       string
	Installed  bool
	State      string
	PID        uint32
	StartType  string
	Account    string
	BinaryPath string
	SidType    string
	Privileges []string
	QueryError string
} // JSON snake_case
func Install(c Config) error   // admin; creates or reconfigures; does not start
func Configure(c Config) error // admin; upgrade / mode switch (restart to apply privileges)
func SetRequiredPrivileges(name string, privs []string) error
func Uninstall(name string, timeout time.Duration) error
func StartService(name string) error
func StopService(name string, timeout time.Duration) error
func Query(name string) (Status, error) // no admin; Installed=false if absent
```

```go
// package main (desktop/cmd/vaultdock-agent): hooks for P0-28 / INT
type IPCServeFunc func(ctx context.Context, api agent.API, log *slog.Logger) error // serve until ctx cancelled
var ServeIPC IPCServeFunc                                    // nil → headless, warning logged
var OnSessionChange func(api agent.API, ev service.SessionEvent) // nil → log only
```

CLI:
```
vaultdock-agent run|service [-data-dir D] [-log-dir D] [-auto-updates]
vaultdock-agent install|configure [-mode simple|advanced] [-extra-privileges a,b] [-exe PATH]
vaultdock-agent uninstall|stop [-timeout 60s]
vaultdock-agent start
vaultdock-agent status   # JSON Status
```

## Privilege sets: reconciliation
- **Simple** = SeChangeNotify, SeImpersonate, **SeTcb**, SeCreateGlobal. These are the sets from the task.
  - The installer spike omits SeTcb, but the S4U spike (P0-S3) requires it. `WTSQueryUserToken` and `LsaRegisterLogonProcess` need it, and without it S4U returns identification-level tokens only.
  - The installer's `VD_PRIVS_SIMPLE` must therefore add `SeTcbPrivilege`.
- **Advanced** = Simple + SeBackup + SeManageVolume. **SeRestore is excluded** from the default set because:
  - MASTER_PLAN §6 says "No SeRestore";
  - the VSS spike says "SeRestore is not needed", because restores write under the user token through the guard.
  - `SeRestorePrivilege` is still accepted as an explicit extra (`-extra-privileges`), so INT can reverse this without a code change.
  - The installer spike's `VD_PRIVS_ADVANCED` (which includes SeRestore) should be aligned.
  - SeManageVolume is kept as the task specifies. The VSS spike does not list it, so the VM test should confirm whether it is needed or drop it.
- **ProcessLaunchPrivileges** (SeAssignPrimaryToken, SeIncreaseQuota) are in C6 and are needed for `CreateProcessAsUser` component helpers (S4U spike). They are in no default set; add them once a component launcher exists.
- Any privilege outside the known nine (for example SeDebug or SeTakeOwnership) is rejected. SeChangeNotify is always included.

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `gofmt -l internal/service cmd/vaultdock-agent`: clean.
- `go build ./...`: ok.
  - It was briefly broken during the run by parallel tasks' in-progress edits in `sdk/client` and `internal/platform`, which are not my files. It was retried until clean, and the final run passed.
- `go vet ./internal/service/ ./cmd/vaultdock-agent/`: ok.
- `go test -count=1 ./internal/service/ ./cmd/vaultdock-agent/`: ok. The tests cover:
  - the privilege builder table (modes, dedup, forbidden names, SeRestore only as an extra, no aliasing);
  - the run core: ctx cancel, exit request, start error (Stop not called), stop timeout, and the Start ctx being cancelled after Stop;
  - log rotation;
  - MULTI_SZ round trip, binary path quoting and config validation;
  - `Query` of a non-existent service, unelevated;
  - parseArgs (18 cases) and realMain exit codes.
- `GOOS=linux go vet ./internal/service/`: ok.
- `go build -o %TEMP%/vda.exe ./cmd/vaultdock-agent`: ok.
  - `vda.exe status` printed `{"name":"VaultDockAgent","installed":false}` with exit 0.
  - `vda.exe install` (unelevated) printed "requires administrator rights" with exit 3 and changed nothing.
  - The binary was deleted afterwards.
- No service was installed, and nothing was run elevated.

## Contract issues
None. The service wrapper is not covered by `api-v1.md` or `backup-format-v1.md`.

## Gaps / follow-ups
- **Untested here (needs an elevated VM):**
  - the real SCM run;
  - install, configure and uninstall;
  - `sc qprivs` output;
  - PreShutdown behaviour;
  - session notifications.
- **Updates:** `run` sets `AutoUpdates` to false by default, behind `-auto-updates`. The current updater launches the per-user NSIS installer, which is unsafe to run as SYSTEM until P1-30's admin installer exists, so the default deviates from P0-26's suggestion of `true`.
- **Key storage:** the Host's default `FileKeyStore` uses user-scope DPAPI, which for LocalSystem is SYSTEM's own key. P0-28/INT should plug in the machine-scope KeyStore as planned. A parallel task appears to be adding `platform` machine helpers.
- **Not implemented:**
  - the `uninstall-cleanup` subcommand mentioned in the installer spike (shadow-copy cleanup before `sc delete`);
  - reading `install-mode.json` / `mode.json` at run time.
- **Path discrepancy:** the installer spike registers the service as `VaultDockAgent` with the arg `service`. The NSIS `VD_RegisterService` and `vaultdock-agent install` produce the same registration. P1-30 may call `vaultdock-agent install -mode <m>` instead of the `sc` commands.

## Notes for dependent tasks / INT
- **P0-28 (IPC):** wire the pipe server by adding a file in `desktop/cmd/vaultdock-agent/`, for example `ipc.go` with `func init() { ServeIPC = ipc.Serve }`. The signature is `func(ctx context.Context, api agent.API, log *slog.Logger) error`. It must return when ctx is cancelled; Stop waits for it up to the stop timeout. `api` is the `*agent.Host`.
- **Profiles (§7.8):** set `OnSessionChange` the same way. It is called synchronously on the SCM control loop, so it must not block.
- **Mode switch (P0-38 / Settings → Mode):** call `service.PrivilegesForMode(mode)`, then `service.SetRequiredPrivileges(service.DefaultName, privs)`, then restart the service (`Host` `RequestExit`, and the recovery restart or SCM start). An exit through `RequestExit` stops cleanly with exit code 0, which does **not** trigger recovery, so a restart after a mode switch needs an explicit mechanism. Options:
  - a non-zero exit;
  - `sc start` from a helper.
- **Installer (P1-30):** add `SeTcbPrivilege` to `VD_PRIVS_SIMPLE`, and remove `SeRestorePrivilege` from `VD_PRIVS_ADVANCED` (see the reconciliation above). Add `sc failureflag 1` and the preshutdown settings, or call `vaultdock-agent install`.
- No new dependencies. `go.mod` was not touched; only `golang.org/x/sys` (already required) and `sdk/modules` are used.
