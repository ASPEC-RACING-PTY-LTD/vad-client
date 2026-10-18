# Spike P0-S2: winget in the user session vs SYSTEM; consent flow

Status: design complete. The prototype builds, vets and tests on the dev host. **Nothing was installed or uninstalled, and nothing ran elevated.** Session launch and UAC behaviour still need the elevated-VM test plan in section 6.

Feeds: DSK-R (`desktop/cmd/vd-reinstall/**`, `desktop/internal/reinstall/**`), MASTER_PLAN §6 process table, §7.6 IPC, §7.7 `reinstall` component.

## 1. Observed on the dev host (read-only)

| Check | Result |
|---|---|
| `winget --version` | `v1.11.510` |
| App Installer package | `Microsoft.DesktopAppInstaller_8wekyb3d8bbwe` v1.26.509.0, under `C:\Program Files\WindowsApps\...` (directory not listable by a standard user) |
| `winget` resolves to | `%LOCALAPPDATA%\Microsoft\WindowsApps\winget.exe`, a **per-user app execution alias** |
| Logs / settings | `%LOCALAPPDATA%\Packages\Microsoft.DesktopAppInstaller_8wekyb3d8bbwe\LocalState\DiagOutputDir` and `...\LocalState\settings.json`, all per user |
| `winget list --count 1 --accept-source-agreements --disable-interactivity` | works un-elevated; output is a human table (truncated at the count) |
| choco / scoop | neither installed (`where choco scoop` finds nothing) |

Conclusion: on a normal Windows 11 machine winget is a per-user MSIX app. Its path, settings, logs and source state all live in the user's profile.

## 2. Findings: winget under LocalSystem

1. **App Installer availability.**
   - The alias exists only for users who have the package registered. SYSTEM has no `WindowsApps` alias on `PATH`.
   - Calling it as SYSTEM means resolving the versioned folder `C:\Program Files\WindowsApps\Microsoft.DesktopAppInstaller_<ver>_x64__8wekyb3d8bbwe\winget.exe` yourself. That path changes with every App Installer update, and its VCLibs/UI.Xaml framework dependencies must resolve.
   - This is widely reported as fragile, and Microsoft only partially supports it.
   - App Installer can be **missing entirely** on Windows Server, LTSC/IoT, Sandbox, and right after OOBE before the Store registers it. In those cases the fix (`Add-AppxPackage -RegisterByFamilyName -MainPackage Microsoft.DesktopAppInstaller_8wekyb3d8bbwe`) has to run in the user's session.
2. **Per-user packages.**
   - Under SYSTEM, `--scope user` installs (and portable packages, and anything whose manifest defaults to user scope) land in `C:\Windows\System32\config\systemprofile`, not in the user's profile. That is wrong for a restore.
   - Per-user MSIX and Store packages cannot be provisioned for another user from SYSTEM through winget.
3. **msstore source.**
   - The msstore source needs a user context: Store licensing is tied to the user and to the MSA/Entra sign-in.
   - It is not usable as SYSTEM. Even in the user session, paid or licensed Store apps need the user signed in to the Store, and free ones may still need age or region checks.
4. **Agreements.**
   - Source agreements (msstore needs a geographic-region data agreement) and package agreements (EULAs in manifests) are accepted **per user context**.
   - Under SYSTEM nothing is shown to anyone, so `--accept-*` there would amount to accepting on the user's behalf without them seeing it.
   - The flags must only be passed after the user has seen the agreements in VaultDock's consent screen (section 4).
5. **Settings and policy.** Group Policy (`EnableAppInstaller`, `EnableMSStoreSource`, allowed sources) can disable winget or a source. winget then returns policy errors, which must surface as `blocked` rather than `failed`.

**Decision: never run winget as LocalSystem.** This confirms MASTER_PLAN §6, where `vd-reinstall.exe` runs in the user's session under the user's token.

## 3. Recommended design

### 3.1 Process model

```
UI (user, session N) ──pipe──► Agent (LocalSystem, SeTcb)
   consent batch                 │ verify caller SID, component hash, batch hash
                                 │ WTSQueryUserToken(N) → check SID == caller, not elevated
                                 ▼ CreateProcessAsUser(winsta0\default, user env, profile cwd)
                          vd-reinstall.exe (user, session N, filtered token)
                                 │ pipe back to agent: fetch plan by batch id, stream job events
                                 ├─► winget install ... (plain child process, user PATH/alias)
                                 ├─► scoop install ...   (user scope, if detected)
                                 └─► [machine set] ShellExecuteEx "runas" vd-reinstall --machine-batch
                                        └─► winget/choco installs (one UAC prompt for the set)
```

- **Agent-launched (recommended).** The agent is LocalSystem and already holds SeTcb (§6), so it can call `WTSQueryUserToken` for the caller's session. Prototype: `session_windows.go`.
  - The session id comes from the pipe client: `GetNamedPipeClientSessionId`, cross-checked with `ProcessIdToSessionId` of the client PID.
  - The token user must equal the consenting caller's SID.
  - The token must **not** be elevated. `WTSQueryUserToken` returns the UAC-filtered token for admins. If UAC is off, refuse and ask the user to run the batch from the UI, which uses the UI-launched path below.
  - **Never** use `GetLinkedToken` to get the elevated half and launch silently. That is a UAC bypass.
  - The binary launched is `%ProgramFiles%\VaultDock\components\vd-reinstall.exe`, which users cannot write. The agent verifies its SHA-256 against the component manifest (§7.7) immediately before launch.
  - Use `CREATE_UNICODE_ENVIRONMENT` with `CreateEnvironmentBlock(user token)`, so `%LOCALAPPDATA%\Microsoft\WindowsApps` (the winget alias) is on `PATH`. Desktop `winsta0\default` lets UAC and installer UIs appear.
  - Put the child in a job object so cancel or agent stop kills the tree. Don't use kill-on-close for elevated children; ask them to stop over the pipe.
- **UI-launched (fallback).** The UI is already in the session as the user, so it can simply `exec` `vd-reinstall.exe`. This is simpler and needs no SeTcb, but the agent can't attest which binary runs or that it ran unmodified.
  - Use it only when the agent path fails (UAC disabled, WTS API error, RDP-only edge cases).
  - Even then, the agent accepts job reports **only** for a batch id it issued, from a pipe client whose SID matches.
- **winget invocation.** Run the CLI as a child of `vd-reinstall`, **one `winget install` per app**, never `winget import`. This keeps independent jobs, retries, and skip/skip-all.
  - Parse **exit codes only**; never parse the localised table output.
  - Verify afterwards with `winget list --id <id> --exact` (exit 0 means present).
  - Later option: the COM API `Microsoft.Management.Deployment` gives structured progress, but it is out-of-proc COM tied to the same per-user package. CLI first.
- **winget missing.** `vd-reinstall` probes `winget --version`. If that fails, the batch's winget items become `manual`, and the UI offers "Get App Installer" (opens the Store page) or the re-register command for the user to run. VaultDock never downloads or bundles winget.

### 3.2 Scope and elevation

| Item class | How it runs | Prompts |
|---|---|---|
| `--scope user`, portable, scoop | in `vd-reinstall` (filtered token) | none |
| msstore | in `vd-reinstall` (filtered token) | Store sign-in if licensed |
| `--scope machine` / installer requires elevation (winget) | **default:** the un-elevated `vd-reinstall` runs winget, and winget/the installer raises UAC per installer | one UAC per elevating installer |
| same, "batch elevation" option | `vd-reinstall` relaunches itself once with `ShellExecuteEx(runas)` as `--machine-batch <id>`, and the elevated copy runs the whole machine set | **one** UAC for the set |
| choco | always in the elevated machine set (choco needs admin) | part of the single UAC |

- Use `--scope` exactly as backed up (`scope` from discovery evidence).
- If a scope was unknown at backup, omit `--scope` and let winget pick.
- If an install with an explicit scope fails with "no applicable installer", retry once without `--scope` and record a warning.
- Never run user-scope items inside the elevated set. An elevated token belongs to the same user, so this works, but it widens privilege for nothing.
- A standard (non-admin) user can still approve machine installs with an admin's credentials at UAC ("over-the-shoulder"). VaultDock treats that as normal UAC, never as a VaultDock permission.

### 3.3 Exit codes and outcomes

- `Classify` in `winget.go` maps winget HRESULTs (`0x8A15xxxx`) and raw installer codes to job outcomes:
  - `installed`;
  - `already_installed` (treated as success);
  - `reboot_required` (pauses the remaining batch, see 4.5);
  - `retryable` (in use, another install running, no network: up to 2 retries with backoff);
  - `cancelled` (includes declined UAC: offer retry/skip, never auto-retry);
  - `not_found` (offer an alternative source if the match evidence has one);
  - `blocked` (policy or unsupported OS);
  - `failed`.
- The table values must be re-verified against winget-cli's `returnCodes.md` for the targeted winget version. An unknown code defaults to `failed`.
- choco: 0 is success; 1641/3010 mean reboot; anything else is `failed`. Pass `--no-progress --limit-output -y`.
- scoop: 0 is success; anything else is `failed`. Verify with `scoop list <app>`.

### 3.4 Logging

- Each job uses `winget ... --log %LOCALAPPDATA%\VaultDock\reinstall\<batch>\<seq>-<id>.log`. Keep winget's own default diag logs; add `--verbose-logs` only on a "retry with diagnostics" action.
- `vd-reinstall` streams structured events to the agent: `{batch, seq, source, id, scope, attempt, exit_code_hex, outcome, duration_ms}`. The agent logs them with `slog`, and the UI shows them. Log files stay in the user's profile and are attached to an issue only on request.
- Never log tokens, the environment block, or installer override strings. Package IDs and versions are fine.

### 3.5 choco and scoop (optional, detected only)

- **Detection** (in `vd-reinstall`, user session):
  - choco: `%ChocolateyInstall%\bin\choco.exe`, else `%ProgramData%\chocolatey\bin\choco.exe`;
  - scoop: `%SCOOP%\shims\scoop.cmd`, else `%USERPROFILE%\scoop\shims\scoop.cmd`;
  - then `--version` / `scoop --version`.
- **Never bundled and never bootstrapped by VaultDock** (C13). If the backup has choco/scoop items and the manager is missing, those items become `manual`, with the manager's install URL shown as text.
- choco items join the elevated machine set: `choco install <id> -y --no-progress --limit-output [--version x]`.
- scoop items run un-elevated (scoop refuses or warns when elevated).
  - A required bucket (`extras`, ...) is added with `scoop bucket add <name>` only if it is listed in the consent screen, because it is a persistent config change.
  - Use `scoop install <bucket>/<app>`.
- IDs are validated with the same strict pattern as winget IDs. Arguments are passed as argv, never through a shell string. choco/scoop shims are `.cmd`/`.ps1` files, so run them via `cmd.exe /d /c` with quoted, validated arguments only.

## 4. Consent flow (per batch)

1. **Plan.** The restore planner gives the UI the app list, each with match evidence `{source, id, matchedBy, confidence, scope, version}`.
   - Items with confidence below 0.9, `ARP\...` / `MSIX\...` pseudo-IDs, or a missing manager are unchecked or `manual` by default.
   - Before showing the screen, the UI asks `vd-reinstall --probe` (in session) for each item's agreements and publisher, using `winget show --id X --exact`. The first msstore use shows the msstore source agreement.
2. **Review screen.** Items are grouped as:
   - "No admin needed";
   - "Needs administrator (one Windows prompt)" or "(a prompt per app)", depending on the batch-elevation toggle;
   - "Microsoft Store (may ask you to sign in)";
   - "Manual".

   Each row shows the name, publisher, source and ID, pinned/latest, scope and agreement links. Plain-language text says that VaultDock will accept these licence terms on your behalf. Buttons: Install selected / Cancel.
3. **Consent record.**
   - The UI sends `ReinstallConsent{batch_id, sha256(canonical item list), sid, session_id, time}` over the pipe.
   - The agent checks that the caller SID equals the profile being restored, unless an admin is restoring their own machine-scope set (§7.6: machine-wide calls need Administrators plus UI confirmation).
   - The agent stores the record with a 24 h expiry. There is **no standing consent**: every batch needs a new one, and changing any item invalidates the hash.
4. **Run.**
   - The agent launches `vd-reinstall --batch <id>` (3.1).
   - `vd-reinstall` fetches the plan by id and re-checks the hash.
   - Jobs run serially (installers conflict on the MSI mutex). User-scope items run first, then the machine set.
   - Each item supports skip, skip-all and cancel from the UI. Cancel stops after the current installer, because winget installers can't be killed safely mid-install.
5. **Reboot.**
   - On `reboot_required` the remaining items are paused. The UI offers "Restart now" / "Later".
   - After the reboot, resuming needs a single click to re-confirm the **same** hash: the stored consent remains valid only if it has not expired. Data restore for an app waits until that app's install has succeeded, so apps are installed before their data is restored.
6. **Audit.** The batch summary (counts per outcome, no log contents) goes to the server as a restore issue/report, using the existing contract mechanisms only (no new API).

## 5. Prototype (`vdspike/winget`)

| File | What |
|---|---|
| `winget.go` | `InstallArgs(Item)`: strict ID/version validation; `--exact --source --silent --disable-interactivity --accept-*`; optional `--version`, `--scope`, `--log`. `Classify(uint32) Outcome`: exit-code table |
| `winget_test.go` | table tests (injection-shaped ID rejected, scope validation, code mapping) |
| `session_windows.go` | `LaunchInUserSession(sessionID, expectSID, exe, args)`: `WTSQueryUserToken` → SID check → refuse elevated → `CreateEnvironmentBlock` → `GetUserProfileDirectory` → `CreateProcessAsUser` (`winsta0\default`) |
| `main.go` | prints a sample winget argv only; **never** calls the session launch |

Dependencies: stdlib plus `golang.org/x/sys v0.49.0` (BSD-3-Clause, already used by the VSS spike and resolved from the local module cache). Build with `GOWORK=off` (the spike is not in `go.work`).

## 6. Test plan (elevated Windows 11 VM, snapshot before each run)

Setup:
- a Windows 11 23H2+ VM with two local users: `admin1` (Administrators) and `std1` (standard);
- the agent built with a test hook that calls `LaunchInUserSession` for `vd-reinstall --probe`;
- small, safe packages: `7zip.7zip` (machine), `Microsoft.PowerToys` (per-user option), one portable package, one msstore free app (e.g. `9WZDNCRFJ3PS`, Remote Desktop);
- revert the snapshot after each case.

| # | Case | Expect |
|---|---|---|
| T1 | Run `winget --version` as SYSTEM (`psexec -s` equivalent: a test service) via PATH, then via the resolved WindowsApps path | PATH fails. The direct path may work. Record the result; it is evidence only |
| T2 | Agent → `LaunchInUserSession` for `admin1` (UAC on) | child runs with a filtered token, `winget --version` OK, `%LOCALAPPDATA%` is admin1's |
| T3 | Same with a SID mismatch (consent by `std1`, session of `admin1` via fast user switch) | refused, nothing launched |
| T4 | UAC disabled (`EnableLUA=0`, VM only) | elevated token refused; the UI-launched fallback is used |
| T5 | User-scope install of a portable package | no UAC, outcome `installed`, visible in `winget list` for that user only |
| T6 | Machine-scope install, default mode | one UAC per installer; declining gives `cancelled` (record the exact exit code) |
| T7 | Machine set with batch elevation (`runas` relaunch) | exactly one UAC; all machine items install |
| T8 | `std1` machine install with admin1 credentials at UAC | installs; the agent never sees credentials |
| T9 | msstore free app, signed-out Store | record the behaviour and exit code (`installed` or a sign-in error) |
| T10 | Already-installed package | `already_installed`; record the actual code (0x8A15002B vs 0x8A15010D vs 0x8A150061) |
| T11 | Network off mid-batch | `retryable`, then retried; the batch continues |
| T12 | Reboot-requiring installer (MSI that returns 3010) | `reboot_required`; batch paused; resume after reboot keeps the same hash |
| T13 | Policy `EnableAppInstaller=0` | `blocked` for every winget item; no crash |
| T14 | App Installer de-registered for the user | items become `manual`; the UI offers the Store link |
| T15 | choco installed / scoop installed vs absent | detection is correct; choco runs in the elevated set and scoop un-elevated; `scoop bucket add` only when consented |
| T16 | Tamper: replace `vd-reinstall.exe` hash in the components dir | the agent refuses to launch |
| T17 | Edit the plan after consent (change an ID) | hash mismatch; `vd-reinstall` aborts |
| T18 | Cancel during a long installer | the current installer finishes, the rest are `skipped`, no orphan processes (job object) |

Exit criteria: T2, T3, T4, T6, T7, T10, T12, T16 and T17 pass, and the observed exit codes are written back into `Classify`.

## 7. Risks

- **HRESULT table drift.** winget return codes change between versions. Mitigation: re-verify per release (T10/T6) and treat unknown codes as `failed`, with the raw hex shown.
- **Installers that ignore `--silent`.** Some show EULAs or UIs anyway or hang waiting for input. Mitigation: a per-job timeout (30 min default) that marks the job `failed` without killing the installer, plus a per-item "run interactively" retry (`--interactive`).
- **Per-installer UAC fatigue** in the default mode. Mitigation: the batch-elevation toggle (one prompt). The elevated `vd-reinstall` is a small, short-lived surface. It accepts only a batch id, and re-validates the plan with the agent.
- **UAC disabled / built-in Administrator** gives an already-elevated token. We refuse the agent launch path (T4), because there would be no prompt and so no OS-level consent; the UI fallback runs with whatever the user already has.
- **Store licensing.** Paid or licensed apps may need sign-in, and `--accept-source-agreements` for msstore implies a region data agreement. This is shown explicitly in consent.
- **App Installer absent** (Server/LTSC/fresh OOBE). There is no reinstall via winget; we degrade to `manual`. VaultDock never installs App Installer itself.
- **winget output is localised and table-shaped.** Never parse it; use exit codes plus `list --id --exact`. Move to the COM API later if structured progress is needed.
- **Activation and sign-in apps** (Office, Adobe CC, JetBrains) install but are unusable until the user signs in. The summary says so; it is not a failure.
- **WTS edge cases.** RDP sessions, disconnected sessions, and multiple sessions for one user: always use the pipe client's session id, never `WTSGetActiveConsoleSessionId`.
- **`--accept-package-agreements` is a legal acceptance on the user's behalf.** It is only passed for items the user saw in the consent screen (hash-bound). Owner/legal may want to review the consent wording.
