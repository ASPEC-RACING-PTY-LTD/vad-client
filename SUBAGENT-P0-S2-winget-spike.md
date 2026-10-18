# P0-S2 winget in the user session vs SYSTEM; consent flow
Status: completed

## Goal
Design how the Advanced `reinstall` component installs apps:
- winget limits under LocalSystem;
- launching in the user's session;
- per-batch consent;
- exit codes, logging and scope;
- choco/scoop as optional, detect-only fallbacks.

Also: a small compilable prototype, a VM test plan and the risks.

## Changes (repo: path, created/modified/deleted, why)
All in the client repo, all created:
- `planning/spikes/winget/README.md`: findings, recommended design, consent flow, elevated-VM test plan (T1–T18), risks.
- `planning/spikes/winget/go.mod`, `go.sum`: module `vdspike/winget`, go 1.26.0, `golang.org/x/sys v0.49.0`.
- `planning/spikes/winget/winget.go`: `InstallArgs` (argv builder with strict ID validation) and `Classify` (exit code to outcome).
- `planning/spikes/winget/winget_test.go`: table tests.
- `planning/spikes/winget/session_windows.go`: `LaunchInUserSession`, the call sequence `WTSQueryUserToken` → SID check → refuse elevated token → `CreateEnvironmentBlock` → `CreateProcessAsUser` on `winsta0\default`.
- `planning/spikes/winget/main.go`: prints a sample argv; never launches anything.

## Interfaces exposed (exact Go/TS signatures, routes, schema others depend on)
These are spike-only; DSK-R may adapt them:
- `func InstallArgs(it Item) ([]string, error)`, where `Item{Source, ID, Version string; Scope Scope; LogPath string}`
- `func Classify(code uint32) Outcome`, where Outcome is one of installed, already_installed, reboot_required, retryable, cancelled, not_found, blocked, failed
- `func LaunchInUserSession(sessionID uint32, expectSID *windows.SID, exe string, args []string) (*windows.ProcessInformation, error)`
- Proposed pipe message: `ReinstallConsent{batch_id, sha256(canonical item list), sid, session_id, time}`, with a 24 h expiry and no standing consent.

## Commands run and results
- `winget --version`: v1.11.510. `winget --info`: App Installer 1.26.509.0, per-user alias at `%LOCALAPPDATA%\Microsoft\WindowsApps\winget.exe`.
- `winget list --count 1 --accept-source-agreements --disable-interactivity`: works un-elevated.
- `where choco scoop`: neither installed.
- In the spike dir, with `GOWORK=off GOPROXY=off`, `gofmt -l .`, `go build ./...`, `go vet ./...` and `go test ./...` all passed.
- `go run . -scope machine` printed the expected argv.
- Nothing was installed or uninstalled, and nothing ran elevated.

## Contract issues
None.

## Gaps / follow-ups
- Not run here, by design: the session launch, UAC behaviour, the actual winget exit codes, msstore behaviour and the SYSTEM-context behaviour. They are in README §6 (T1–T18) for an elevated VM.
- The HRESULT values in `Classify` come from winget-cli's documented return codes but are unverified on this host. Re-check them against `returnCodes.md` for the targeted winget version (T6/T10).
- The consent wording for `--accept-package-agreements` (accepting EULAs on the user's behalf) may need owner/legal review.

## Notes for dependent tasks / INT
- **DSK-R:**
  - Never run winget as LocalSystem.
  - The agent launches `vd-reinstall.exe` with `WTSQueryUserToken` in the **pipe client's** session (`GetNamedPipeClientSessionId`), after a SID match, a refusal if the token is elevated, and a component SHA-256 check. Never use `GetLinkedToken` (that would be a UAC bypass).
  - The UI-launched path is a fallback only.
  - Run one `winget install` per app (not `import`). Use exit codes only and never parse the localised output; verify with `winget list --id --exact`.
  - Machine-scope items get per-installer UAC by default. An optional batch-elevation mode does one `ShellExecuteEx runas` relaunch for the machine set.
  - choco/scoop are detected only, never bundled or bootstrapped. choco goes in the elevated set, scoop runs un-elevated, and `scoop bucket add` needs consent.
- **Agent/service (§7.6):** `GetNamedPipeClientSessionId` is not wrapped in `x/sys/windows`; DSK-R/agent needs a small lazy-DLL binding. The agent needs SeTcb in its `RequiredPrivileges`, which §6 already lists.
- The spike module isn't in `go.work`; build it with `GOWORK=off`. No new third-party dependencies (`x/sys` is BSD-3 and already in use).
