# P0-S1 VSS via WMI/COM + AccessCheck-gated reads with a user token
Status: completed (it compiles and the design is settled; VSS and privileged behaviour is untested on this machine and needs the elevated-VM plan in the README)

## Goal
Find a workable, licence-safe way for the LocalSystem service to create, expose and delete VSS shadows from Go, and to read shadow files only when the target user's token passes `AccessCheck(GENERIC_READ)`. Deliver a compilable prototype, a README and the C7 pass/fail criteria.

## Changes (repo: path, created/modified/deleted, why)
- VaultDock: `planning/spikes/vss/go.mod`, `go.sum`: created. Module `vdspike/vss`, go 1.26.0, requires go-ole v1.3.0 and x/sys v0.49.0.
- VaultDock: `planning/spikes/vss/wmi_windows.go`: created. Covers `Win32_ShadowCopy` Create (`ClientAccessible`) through `ExecMethod_`, the DeviceObject lookup and Delete, with GUID validation of IDs.
- VaultDock: `planning/spikes/vss/gate_windows.go`: created. Contains:
  - `Gate`: an identification-level duplicate of the user token, and `AccessCheck` (with `MapGenericMask`) on handle-read SDs (OWNER|GROUP|DACL|LABEL). It refuses reparse points.
  - `OpenSnapshotFile`: checks the live SD and the snapshot SD, opens the data handle with backup semantics only after both pass, then verifies the file index.
  - `CheckDir`.
  - `withBackupPrivilege`: SeBackup on a thread-only impersonation token.
- VaultDock: `planning/spikes/vss/main_windows.go`: created. CLI with `check`, `create`, `delete`, `read` and `demo`. The user token comes from `WTSQueryUserToken` when the process runs as SYSTEM, otherwise from the process token.
- VaultDock: `planning/spikes/vss/README.md`: created. Covers the recommendation, COM and WMI call sequences, privileges, the AccessCheck design, risks, the VM test plan T1–T12, the C7 pass/fail criteria and the fallback.

## Interfaces exposed
These are spike-only and not imported by anything. Candidate shapes for DSK-F (`desktop/internal/vss`, `privio`):
- `func NewGate(user windows.Token) (*Gate, error)`
- `func (g *Gate) OpenSnapshotFile(livePath, snapPath string) (*os.File, error)`; `CheckDir(liveDir, snapDir string) error`; `CheckLive(path string) error`
- `var ErrDenied, ErrReparse error`
- `func withBackupPrivilege(fn func() error) error`
- WMI: `CreateShadow(volume) (id, error)`, `DeviceObject(id) (string, error)`, `DeleteShadow(id) error`

## Commands run and results
- `GOWORK=off GOPROXY=off GOFLAGS=-mod=mod go get github.com/go-ole/go-ole@v1.3.0 golang.org/x/sys@v0.49.0`: resolved from the local module cache (no network). I removed the `// indirect` markers by hand; `go mod tidy` was not run.
- `GOWORK=off GOOS=windows go build .` and `go vet .`: OK. `gofmt` is clean.
- Non-admin `vss-spike check` probes on this machine:
  - `MASTER_PLAN.md` and `notepad.exe` were allowed.
  - `config\SAM`, `systemprofile` and `System Volume Information` were denied. Unelevated, the open for `READ_CONTROL` itself fails, and the gate treats that as deny (fail-closed).
- Not run: shadow create, delete, read or demo. They need admin rights.
  - One mistaken command batch included `vss-spike create C:\`. The permission classifier blocked that whole batch, so nothing in it ran.
  - I then reran only the read-only checks. No shadow was created on this machine.

## Contract issues
None. The spike doesn't touch the API or the backup format.

## Gaps / follow-ups
- The VM tests T1–T12 (README) must be run elevated to decide C7.
- The production COM `IVssBackupComponents` binding (with `VSS_CTX_BACKUP` + `VSS_BT_COPY`, auto-deleted on process exit) is designed but **not prototyped**. It is the recommended route for DSK-F; WMI is the fallback.
- The WMI orphan-reaping race (a crash between Create and persisting the ID) needs a before/after shadow listing.
- EFS files and cloud placeholders: detect and skip with an issue.
- T12 (reading the shadow under an impersonated user token, no SeBackup) may be a simpler primary design. Test it first.

## Notes for dependent tasks / INT
- C6 `RequiredPrivileges` must include SeBackupPrivilege and SeTcbPrivilege; SeRestore is not needed. Enable SeBackup only on a thread token around snapshot reads and VSS calls.
- Build the VSS component for the native architecture only (no 32-bit; `VSS_E_WOW64_UNSUPPORTED`).
- Read SDs by handle (`GetSecurityInfo`) with `FILE_FLAG_OPEN_REPARSE_POINT`, never with `GetNamedSecurityInfo`, which follows links.
- Include `LABEL_SECURITY_INFORMATION` so integrity labels count.
- `x/sys/windows` lacks `AccessCheck`/`MapGenericMask`; use a LazyProc on advapi32. Call `AdjustTokenPrivileges` directly to detect `ERROR_NOT_ALL_ASSIGNED`.
- New dependency: go-ole v1.3.0 (MIT; already an indirect dependency of `desktop`), in the spike module only. x/sys is BSD-3 and already used.
- The spike module isn't in `go.work`, so build it with `GOWORK=off`.
