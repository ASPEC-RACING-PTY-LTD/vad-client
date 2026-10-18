# Spike P0-S5: admin NSIS installer with install modes

Status: prototype. **Not compiled**, because `makensis` is not installed on the build host (`where makensis` found nothing, and there is no `Program Files\NSIS`). Nothing has been run. P1-30 (REL-C) turns this into the real installer.

| File | Purpose |
|---|---|
| `vaultdock_modes.nsh` | Reusable pieces: init/command line, mode page, (i) info page, install-dir guard, ProgramData ACL, mode file, service registration, uninstall macro |
| `spike.nsi` | Standalone harness that includes the `.nsh` (MUI2, no Wails), so the pieces can be compiled and tried in a VM |
| `modes/simple.en.txt`, `modes/advanced.en.txt` | Plain-language (i) texts: functionality, scope, permissions, resources and differences. Draft wording for INT review |

## Design

### Pages

Welcome, (licence), **Mode**, **Info** (hidden unless requested), Directory, Install, Finish.

- **Mode page** (`VD_ModePageCreate` / `VD_ModePageLeave`, nsDialogs):
  - two radio buttons in one `WS_GROUP`, so arrow keys and Alt-mnemonics work: **Simple Folder Backups (recommended)** and **Full Access to All Tools (Advanced)**;
  - a one-line summary label under each;
  - a `${NSD_CreateLink}` "ⓘ What does this include?" next to each radio. The ⓘ is written as `${U+24D8}`, so the script can stay ASCII.
  - Simple is the default.
- **ⓘ "dialog"** (`VD_InfoPageCreate`):
  - nsDialogs cannot open a second top-level modal window without a third-party plugin, so the dialog is a wizard page that behaves like a modal.
  - Clicking ⓘ stores which text to show and sends `WM_NOTIFY_OUTER_NEXT`. The mode page's leave function records the current radio state, so the choice is preserved.
  - The info page then shows a read-only, scrollable multiline EDIT, hides **Next** and relabels **Back** as "< Back to the choice".
  - When the page closes, the buttons are restored.
  - When no ⓘ was clicked, the page `Abort`s, so it is skipped in both directions.
  - Alternatives considered:
    - `MessageBox`: strings are capped at `NSIS_MAX_STRLEN` (1024), so the texts don't fit.
    - Opening the `.txt` in Notepad: not modal, leaves temp files open, and doesn't work on Server Core or with locked-down shells.
- **Text loading**:
  - The `.txt` files are compiled into the installer (`File /oname=$PLUGINSDIR\...`), so there is no runtime dependency.
  - They are appended line by line with `EM_REPLACESEL`, so their length isn't limited by `NSIS_MAX_STRLEN`, and LF is normalised to CRLF.
  - The texts must be ASCII. For localisation, convert them at build time to UTF-16LE and switch to `FileReadUTF16LE`.
  - These files are the single source of truth: P1-30 moves them to `desktop/build/windows/installer/modes/`, and the UI embeds the same files (§7.7).

### Command line and upgrade behaviour (`VD_Init`)

- `/MODE=simple|advanced` sets the mode and skips the page (silent or fleet installs). Any other value exits with error level 2.
- **Upgrade**: an HKLM uninstall key plus an existing mode file means the page is skipped and the mode file is left untouched. The mode is changed only in Settings → Mode (R11), and the agent is the authority after first start.
- A fresh `/S` install without `/MODE` gets `simple`.
- `InstallLocation` from HKLM is reused on upgrade.
- `VD_VerifyInstDir` rejects anything under `%WINDIR%`, the user's profile (which blocks the per-user path `%LOCALAPPDATA%\Programs\VaultDock` that the old updater passes as `/D=`) and drive roots.

### `%ProgramData%\VaultDock` and the mode file

- **Path resolution**: `SetShellVarContext all` + `$APPDATA`, which comes from the shell known-folder API. It is **not** read from `%PROGRAMDATA%`: an elevated installer inherits the user's environment, and a user can override that variable in `HKCU\Environment`.
- **Squatting defence**: any user may create folders in ProgramData. If `VaultDock` already exists and is **not** from a previous admin install, or is a reparse point, it is renamed to `VaultDock.untrusted-<tick>` and a fresh folder is created.
  - This avoids `icacls /T` following planted hard links, which would change the ACLs of arbitrary files.
  - If the rename fails, setup aborts.
- **ACL** (well-known SIDs, so it works on non-English Windows):
  - owner `*S-1-5-32-544`;
  - `/inheritance:r`;
  - SYSTEM `(OI)(CI)F`, Administrators `(OI)(CI)F`;
  - `logs\` additionally grants Users `RX`, as §7.6 specifies.
  - Any `icacls` failure **aborts** (fail closed).
- **Mode file** `install-mode.json`:
  ```json
  {"version":1,"mode":"simple","source":"installer","installer_version":"0.1.0"}
  ```
  - It is written to `.tmp` and then renamed.
  - It then gets an explicit, non-inherited ACL: SYSTEM:F and Administrators:F only. No one else can write it, or even read it; the UI asks the agent over IPC.
  - The agent validates `mode` with `modules.Mode.Valid()`, imports it on first start into `state.db` (`modules` table), applies `modules.ModulesForMode`, then treats `state.db` as the authority.
- **Path discrepancy for INT**: the task text says `%ProgramData%\VaultDock\install-mode.json`, while MASTER_PLAN §7.6 says `%ProgramData%\VaultDock\config\mode.json`. The spike uses the task's path behind `!define VD_MODE_FILE_NAME`, so P1-30 can change it in one place.

### Service (`VD_RegisterService`)

All service settings are applied with `nsExec::ExecToLog "$SYSDIR\sc.exe" ...`:

| Setting | Command / value |
|---|---|
| Create, or reconfigure on upgrade | `sc create`; if it returns 1073 (`ERROR_SERVICE_EXISTS`), `sc config` with the same arguments |
| Binary path | Quoted `"\"$INSTDIR\vaultdock-agent.exe\" service"`, so there is no unquoted-path issue. Program Files is admin-write only |
| Start type | `start= delayed-auto` |
| Account | `obj= LocalSystem` |
| Description | `sc description` |
| Recovery | `sc failure ... actions= restart/5000/restart/30000/restart/120000 reset= 86400`, plus `failureflag 1` (non-crash failure exits also trigger recovery) |
| Service SID | `sc sidtype ... unrestricted`, so `NT SERVICE\VaultDockAgent` can be used in ACLs later |
| Privileges | `sc privs` (RequiredPrivileges) per mode, see below. Failure aborts |
| Tray UI | HKLM Run `"VaultDock.exe" --tray` |
| Start | `sc start`. 1056 is OK. A failed start is **not** fatal: the files are installed and the UI reports the service state |

Privilege allowlists by mode:
- Simple: `SeChangeNotifyPrivilege/SeImpersonatePrivilege/SeCreateGlobalPrivilege`.
- Advanced: the Simple set plus `SeBackupPrivilege/SeRestorePrivilege/SeManageVolumePrivilege` (VSS requestor).

On a mode switch, the agent rewrites the list itself with `ChangeServiceConfig2W(SERVICE_CONFIG_REQUIRED_PRIVILEGES_INFO)` and restarts. Component processes are children of the agent, so they can never exceed this list.

**Open:**
- Scheduled backups while no UI is running need the user's token.
  - `ImpersonateNamedPipeClient` plus `DuplicateTokenEx` (from a running tray) needs only `SeImpersonatePrivilege`.
  - `WTSQueryUserToken` would need `SeTcbPrivilege`, which this spike deliberately does not grant.
  - The S4U spike (`planning/spikes/s4u/`) should settle this; then adjust `VD_PRIVS_*`.
- `SeManageVolumePrivilege` should be confirmed as necessary by the VSS spike, or dropped.

**Stopping for upgrade (`VD_StopForUpgrade`):**
- `net stop VaultDockAgent` waits for `SERVICE_STOPPED`. Its exit codes aren't localised, unlike `sc query` text.
- After that, `taskkill /F` runs on the agent and on `VaultDock.exe` in all sessions (the installer is elevated).
- P1-30 should replace the hard kill of the UIs with an IPC "exit for update" request, keeping `taskkill` as the fallback.

**Uninstall (`VD_UninstallService` macro, usable in `Section "uninstall"`):**
- `net stop`, `sc delete`, and removal of the HKLM Run value.
- The caller asks before deleting ProgramData, as §7.6 requires. User files and backups are never touched.

### Components are downloaded by the agent, not by NSIS

The installer ships and installs **core only**, then writes the mode. On first start the agent:
1. reads the mode;
2. fetches `components-<v>.json`;
3. verifies it with `sdk/modules` (`ParseManifest`/`VerifyEmbedded` → `Find` → `CheckCompatible` → `CheckDependencies` → `VerifyComponent`);
4. installs into `%ProgramFiles%\VaultDock\components\<id>\<version>`.

Progress is shown in the UI on its first run. Why not NSIS:

1. **One trust path.** Ed25519 manifest signatures, SHA-256 with a constant-time compare and Authenticode signer checks already live in Go (`sdk/modules`, `desktop/internal/modules`). NSIS has none of these natively and would need third-party plugins (NScurl/inetc, a hashing plugin). That means a second, weaker and untested implementation of the most security-sensitive step, plus extra licence review (O5).
2. **One code path for installs and mode switches.** Settings → Mode (Simple → Advanced) must download components without reinstalling anyway (R11). Using the same code at install time means that path is exercised on every Advanced install.
3. **Robustness.** Go `net/http` with the system proxy, retries, resume, timeouts and rollback to the previous version. A network failure leaves a working Simple-capable install and a retry button, instead of a half-failed installer.
4. **Independent updates.** Components update on their own cadence (manifest `min_core`). The installer would be stale for them the day after release.
5. **Smaller, signable installer.** The signed `setup.exe` stays core-only (D29), and offline/air-gapped needs are covered by the optional full installer (C16). That installer drops the zips into `%ProgramData%\VaultDock\components-cache\`, and the agent verifies and installs them exactly as if they had been downloaded.
6. **Least privilege in the right place.** Downloading as SYSTEM in a long-lived, supervised service with logging and `issues` reporting is auditable. An installer UI thread is not.

The cost is that right after installing in Advanced mode, the Advanced screens show "Installing components…" until the agent finishes. This is acceptable and matches §7.6 step 6.

## Integration with the Wails-generated installer

`wails build -nsis` regenerates `wails_tools.nsh` on every build ("DO NOT EDIT"). It keeps an existing `project.nsi`, so all changes go into `project.nsi`, and this `.nsh` is copied next to it. P1-30 will make these changes to `desktop/build/windows/installer/project.nsi` (owned by REL-C; not edited here):

1. **Install scope.** Change `!define REQUEST_EXECUTION_LEVEL "user"` → `"admin"` and `WAILS_INSTALL_SCOPE "user"` → `"machine"`. With any value other than `user`, `wails.writeUninstaller` writes HKLM, and `wails.setShellContext` switches to `SetShellVarContext all`, so shortcuts go to All Users.
2. **Install directory.** `InstallDir "$PROGRAMFILES64\${INFO_PRODUCTNAME}"`. Make sure `SetRegView 64` is in effect: NSIS is 32-bit, and `$PROGRAMFILES` would mean `Program Files (x86)`.
3. **Includes.** `!include "vaultdock_modes.nsh"` after `MUI.nsh`, and set `!define VD_MODES_DIR "modes"`.
4. **Pages.** Insert `Page custom VD_ModePageCreate VD_ModePageLeave` and `Page custom VD_InfoPageCreate` after the licence page and before `MUI_PAGE_DIRECTORY`. Also add `MUI_PAGE_LICENSE` with the PolyForm NC text.
5. **Callbacks.**
   - In `.onInit`, call `VD_Init` after `wails.checkArchitecture`.
   - Add `.onVerifyInstDir` → `Call VD_VerifyInstDir`.
   - Keep the existing `/RELAUNCH` handling, but `.onInstSuccess` must launch the UI **de-elevated**. `Exec` from an elevated installer would run the UI as admin; use an `explorer.exe "<exe>"` trick or the ShellExecute-from-shell approach, and decide this in P1-30.
6. **Install section.**
   - Replace `StopRunningApp` with `Call VD_StopForUpgrade`.
   - After `!insertmacro wails.files`, add `File` lines for `vaultdock-agent.exe`, which is built by `go build` in the release pipeline. Wails only packs the UI binary (`ARG_WAILS_AMD64_BINARY`).
   - Then call `VD_PrepareDataDir`, `VD_WriteModeFile` and `VD_RegisterService`, and then `wails.writeUninstaller`.
   - `VD_RegisterService` writes `InstallLocation`, which `VD_Init` reads on upgrade.
   - Drop the per-user desktop/Start-menu assumptions; `setShellContext` handles them.
7. **Uninstall section.**
   - Add `!insertmacro VD_UninstallService` first.
   - Replace the HKCU Run removal with HKLM (done by the macro).
   - The "remove settings" prompt deletes `%ProgramData%\VaultDock`, not `%APPDATA%`.
   - Never touch shadow copies other than those recorded by id; the agent does this via an `uninstall-cleanup` command before `sc delete`, so add `ExecWait '"$INSTDIR\vaultdock-agent.exe" uninstall-cleanup'` in P1-30.
8. **Signing.** The existing `!finalize`/`!uninstfinalize` hooks stay commented until a certificate exists (O4/D29).
9. **Build.** `wails.json` needs no change (`nsisType: single` is fine). The release script runs `wails build -nsis` after building the agent into `desktop/build/bin/`.

## Upgrading from the per-user install (C5)

Facts from the current `project.nsi`:
- the per-user install lives in `%LOCALAPPDATA%\Programs\VaultDock`;
- its uninstall key is `HKCU\...\Uninstall\VaultDockVaultDock`;
- it adds an HKCU Run value `VaultDock`;
- its data (config, the DPAPI user-scope device key and sync state) is in `%APPDATA%\VaultDock`;
- the updater runs `setup.exe /S /RELAUNCH /D=<dir>`;
- its uninstaller's data prompt uses `/SD IDNO`, so a **silent uninstall keeps user data**.

Proposed flow (UX to be confirmed by Q5):

1. **The last per-user release** shows "Move VaultDock to the new system-wide installer" and downloads the admin `setup.exe`. It launches the installer with ShellExecute `runas` and these parameters:
   ```
   /MIGRATE_FROM="%LOCALAPPDATA%\Programs\VaultDock" /MIGRATE_SID=<user SID>
   ```
   The admin installer must **not** rely on its own `HKCU`. With over-the-shoulder UAC (a standard user typing admin credentials), the elevated process's HKCU and `%LOCALAPPDATA%` belong to the *admin*, not the user. That is why the per-user side passes its path and SID explicitly.
2. **The admin installer** does the following:
   - `VD_VerifyInstDir` refuses `/D=` paths in the profile, so the updater's `/D=` cannot keep the old location.
   - Installs per-machine as normal. The mode page is shown, defaulting to Simple. With `/S`, the mode is `simple`, since the old app was functionally Simple.
   - Writes `%ProgramData%\VaultDock\config\migrate-<SID>.json` (`{"from":"...","sid":"..."}`, ACL SYSTEM/Admins).
   - **Does not** copy `%APPDATA%` data or touch the DPAPI key itself. User-scope DPAPI can only be decrypted in that user's logon session.
   - Ends by stopping the per-user `VaultDock.exe` (`taskkill /FI "USERNAME eq ..."`, or IPC).
3. **First launch of the new UI in that user's session.** The agent sees the pending migration for the caller SID and asks the UI to run `AdoptUserDevice`. The UI:
   - unprotects the device key with user-scope DPAPI and passes it over the authenticated pipe; the agent re-protects it machine-scope or with the agent's key store;
   - streams `config.json` and `state.db` from `%APPDATA%\VaultDock` to the agent, which imports them into `%ProgramData%\VaultDock\state`;
   - deletes the HKCU Run value;
   - runs the per-user `uninstall.exe /S` as the user. Its silent default keeps `%APPDATA%\VaultDock` as a rollback copy for N releases.
   
   After success, the agent deletes the marker.
4. **If the user declines**, the per-user version keeps working and gets no further updates (C5).
5. **Coexistence guard.** While a per-user install exists, the admin UI shows a banner. Both installs use the same uninstall key name in different hives, so Apps & Features shows two entries until the migration completes. That is acceptable, but `UNINST_KEY_NAME` could also be changed for the machine install.

## Test plan (Windows 10 22H2 and Windows 11 VMs, snapshots; never on a dev machine)

Compile:
- `makensis /V4 /DOUTDIR=%TEMP%\vd spike.nsi` with no warnings other than unused-function warnings. Then do the same with `/DVD_PAYLOAD_DIR=` and real binaries.
- Test a German or Arabic display-language VM too; SIDs are used instead of group names.

| # | Case | Expected |
|---|---|---|
| 1 | Run as a standard user | UAC prompt; declining it means no changes |
| 2 | Fresh interactive install, Simple | Default radio is Simple. `install-mode.json` = simple. `icacls` shows exactly SYSTEM:F and Administrators:F, not inherited. The ProgramData owner is Administrators, and `logs` has Users RX. `sc qc` shows the quoted binPath, DELAYED, LocalSystem. `sc qprivs` shows the Simple list, `sc qsidtype` shows UNRESTRICTED, `sc qfailure` shows 3 restarts. The service is running. HKLM Run is present |
| 3 | Fresh install, Advanced | The mode file is advanced, and `sc qprivs` includes SeBackup/SeRestore. The agent then downloads and verifies the components (logs, `PUT /device/modules`) |
| 4 | ⓘ Simple / ⓘ Advanced | The info page shows the full text with scrolling and no truncation, and Next is hidden. Back returns with the radio selection preserved; for example, select Advanced, open ⓘ Simple, come back, and Advanced is still selected. Back from the Directory page skips the info page. Keyboard only: Tab reaches the links, and Enter/Space opens them. Works at 100/150/200% DPI and in high contrast |
| 5 | `/S /MODE=advanced`, `/S` alone, `/S /MODE=bogus` | Advanced; simple; exit code 2 with no changes |
| 6 | Standard user writes the mode file | `echo x > ...install-mode.json` gives access denied, and so does reading it |
| 7 | Squatting: a standard user pre-creates `%ProgramData%\VaultDock` with a file hard-linked to a file they don't own, and separately as a junction | Setup moves the folder to `VaultDock.untrusted-*`, the target file's ACL is unchanged, and the new folder is clean |
| 8 | `PROGRAMDATA` overridden in HKCU\Environment | Data still goes to the real ProgramData |
| 9 | `/D=%LOCALAPPDATA%\Programs\VaultDock` or `/D=C:\Windows\x` or `/D=C:\` | Rejected (directory page Next disabled; silent install fails) |
| 10 | Upgrade over an admin install (mode advanced, service running, UI open in 2 sessions) | The service stops and UIs close, files are replaced, there is no mode page, the mode file is unchanged, `sc config` is used (no 1073 failure), the service runs again, and privileges are unchanged |
| 11 | Upgrade with `/MODE=simple` | The mode file is rewritten and the privileges become the Simple list. The agent's mode-switch logic suspends Advanced jobs (`suspended_by_mode`) and deletes nothing |
| 12 | Service fails to start (bad binary) | Install completes and the log shows the error. A failed `icacls` or `sc privs` aborts instead |
| 13 | Uninstall, answer No | The service is gone (`sc query` → 1060), the Run value and program files are removed, and `%ProgramData%\VaultDock` is kept. Answer Yes: it is removed. User files are untouched in both cases |
| 14 | C5: per-user 0.x installed and enrolled, then migration | Covers both same-user admin and over-the-shoulder admin. The migration marker is written. On first UI launch the device key is adopted, with no re-enrolment and the same device id on the server. The HKCU Run value is gone, the per-user folder is removed, `%APPDATA%\VaultDock` is kept, and backups continue |
| 15 | Offline Advanced install | Install succeeds and the UI shows the component download pending or retry. Simple features work |

## Not done in this spike

- Not compiled (no NSIS on the host). The likeliest compile issues to check first:
  - `${U+24D8}` support (NSIS ≥ 3.0); the fallback is a UTF-8-with-BOM script and a literal ⓘ;
  - the `ES_*` constants (fallback `!define`s are included);
  - quoting in the `VD_Icacls`/`VD_Sc` macro arguments.
- No licence page, de-elevated relaunch or `uninstall-cleanup` hook (P1-30).
- No real agent. `vaultdock-agent.exe service` and the mode-file import are interface proposals for DSK-SVC and DSK-MOD.
