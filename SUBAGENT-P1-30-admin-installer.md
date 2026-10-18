# P1-30 (REL-C) Admin NSIS installer
Status: completed (not compiled: `makensis` is not installed on this host; first real compile is the CI `nsis` job)

## Goal
Turn spike P0-S5 into the real Wails NSIS installer:
- per-machine, admin-only Program Files install with an install-dir guard;
- `%ProgramData%\VaultDock` folders and ACLs;
- mode page with (i) pages, loading `modes/*.en.txt`, `/MODE=`, and keeping the existing mode on upgrade;
- `config\mode.json`;
- `VaultDockAgent` service through `vaultdock-agent install`;
- HKLM Run entry for the tray UI;
- uninstall that keeps data unless a box is ticked;
- C5 per-user detection and migration page;
- `vaultdock-agent.exe` built and shipped.

## Changes (repo: path, created/modified/deleted, why)
All files are in the client repo `VaultDock`.

**`desktop/build/windows/installer/project.nsi`** (modified, rewritten)
- **Guards:** `REQUEST_EXECUTION_LEVEL admin` and `WAILS_INSTALL_SCOPE machine`. Building with `-installscope user` stops with `!error`.
- **Install dir:** `InstallDir $PROGRAMFILES64\VaultDock`.
- **Pages:** Welcome → Licence (repo `LICENSE`, PolyForm NC) → Migration (C5) → Mode → (i) Info → Directory (skipped on upgrade) → Install → Finish. The uninstaller has Confirm → Components → Instfiles.
- **`.onInit`:**
  - Wails arch check, then `/RELAUNCH`.
  - Reads the token SID.
  - Per-user detection, which runs before `$INSTDIR` is replaced.
  - Upgrade detection from the HKLM uninstall key and reuse of `InstallLocation`.
  - The install-dir rule is enforced here too, because silent installs never reach `.onVerifyInstDir`. A bad `/D=` resets to the default.
  - Data-dir trust check, then mode init.
- **`.onVerifyInstDir`:** only a **subfolder of 64-bit Program Files**, with no `\.` / `\..` / `/`.
- **Install section:**
  1. stop the service and UIs;
  2. WebView2;
  3. `wails.files` plus `vaultdock-agent.exe` from `payload\<arch>\`;
  4. prepare the data dir, then mode file, migration and service registration;
  5. Start-menu shortcut, plus a desktop shortcut on fresh interactive installs;
  6. HKLM uninstall key plus `InstallLocation`, `NoModify` and `NoRepair`.
- **Finish / `/RELAUNCH`:** the UI is launched **de-elevated** with `explorer.exe "<exe>"`. This is skipped when the installer runs as SYSTEM.
- **Uninstall:**
  - service stop and delete through the agent, Run value removal and UI kill;
  - `RMDir /r $INSTDIR`, only when it is inside Program Files (this also removes `components\`);
  - an unticked component **"Also remove settings, logs and device registration (%ProgramData%\VaultDock)"**. It is off in silent uninstalls too, so data is kept by default.
- **Signing hooks:** the `!uninstfinalize` line is still commented, with the exact `sign.ps1` command (see Gaps).

**`desktop/build/windows/installer/common.nsh`** (created)
- Shared variables and defines: service name, Run key, HKLM marker `Software\VaultDock` `DataDir`, and well-known SIDs.
- Helpers: `VD_StrContains`, `VD_IsUnder` (whole-component prefix), `VD_ValidUserSid` (`S-1-5-21-…` / `S-1-12-1-…`, digits and dashes only), `VD_JsonEscape`.
- `VD_GetTokenSid`: System plugin calls `OpenProcessToken`, `GetTokenInformation(TokenUser)` and `ConvertSidToStringSidW`.
- `VD_DataDirInit`:
  - resolves ProgramData with the known-folder API (`SetShellVarContext all` + `$APPDATA`), not `%PROGRAMDATA%`;
  - the folder is trusted only when it is a real directory with no reparse point, **and** an admin install exists or the HKLM marker names it.
- `VD_LaunchUI`.

**`desktop/build/windows/installer/modes.nsh`** (created, from the spike)
- `VD_ModeInit`:
  - `/MODE=` must be `simple` or `advanced`; any other value exits with error level 2.
  - A trusted existing `config\mode.json` means the mode is kept, the page is skipped and `/MODE` is ignored (logged).
  - A fresh install with `/MODE` skips the page.
  - A fresh install without `/MODE` shows the page, defaulting to `simple`. A silent install gets `simple`.
- `VD_ReadModeFile`: accepts both the agent format `{"mode":…}` and the installer format.
- Mode page:
  - nsDialogs radios in a `WS_GROUP`;
  - a `${U+24D8}` link on each radio (the glyph can be overridden with `VD_INFO_GLYPH`);
  - an admin/permissions note.
- (i) info page:
  - a modal-like wizard page with a read-only scrolling EDIT, loaded line by line from `modes\simple.en.txt` / `advanced.en.txt`, which are compiled in;
  - Next is hidden and Back reads "< Back to the choice".
- `VD_WriteModeFile`: writes `config\mode.json` through a tmp file and rename; failure aborts.
- `VD_EffectiveMode`.

**`desktop/build/windows/installer/service.nsh`** (created, from the spike)
- `VD_StopForUpgrade`: `vaultdock-agent stop -timeout 60s` using the installed binary, falling back to `net stop`, then `taskkill` of the agent and all UIs.
- `VD_PrepareDataDir`:
  - An untrusted existing entry is renamed to `VaultDock.untrusted-<tick>`; abort if the rename fails.
  - The root is created **atomically with its final DACL**: `CreateDirectoryW` + SDDL `O:BAD:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)`. There is no window in which it inherits ProgramData's "Users may create" entry. If it already exists after the move, setup aborts.
  - The root ACL is re-asserted with `icacls` (no `/T` anywhere).
  - Subfolders `config`, `state`, `keys`, `logs`, `spool` are created. **`config` gets Users `(OI)(CI)RX`; `logs` and everything else get no Users access**, per INT.
  - Any `icacls` failure aborts (fail closed).
  - The HKLM marker is written.
- `VD_RegisterService`: `"…\vaultdock-agent.exe" install -mode <effective>`. If that fails, it falls back to `VD_RegisterServiceSc`, which uses the P0-27 privilege sets (Simple + **SeTcb**; Advanced + SeBackup + SeManageVolume; **no SeRestore**).
- HKLM Run `VaultDock` = `"<INSTDIR>\VaultDock.exe" --autostart --minimized`.
- `vaultdock-agent start` (non-fatal).
- `VD_UninstallService` macro: `vaultdock-agent uninstall -timeout 60s` → net/sc fallback, `taskkill`, and removal of the HKLM Run value.

**`desktop/build/windows/installer/migrate-peruser.nsh`** (created)
- C5 detection, first match wins:
  1. `/MIGRATE_FROM=` (+ `/MIGRATE_SID=`);
  2. `/D=` pointing inside the profiles root (old per-user updater);
  3. this user's HKCU uninstall key `DisplayIcon`, or `%LOCALAPPDATA%\Programs\VaultDock`.
- A candidate needs `VaultDock.exe` and must not be under Program Files or Windows.
- Migration page: shows the path and explains the move, what is kept, the later adoption, and that Cancel keeps the old version without further updates.
- `VD_MigrateApply`:
  - deletes the old HKCU Run value (`HKU\<sid>\…` when the SID was passed);
  - writes `config\migrate-<sid>.json`.

**`desktop/build/windows/installer/build-agent.go`** (created, `//go:build ignore`)
- Builds `./cmd/vaultdock-agent` into `build/windows/installer/payload/<arch>/vaultdock-agent.exe` with `-trimpath -ldflags "-s -w -X main.version=<v>"`.
- `<v>` = `$VAULTDOCK_VERSION`, or `wails.json` `info.productVersion`, validated.
- Finds the desktop module by walking up to its `go.mod`, because Wails runs hooks in `build/bin`.
- The output is outside `build/bin`, so `wails build -clean` (which cleans after the hooks) doesn't remove it. `*.exe` is already gitignored.

**`desktop/wails.json`** (modified)
- Added `"preBuildHooks": {"windows/*": "go run ../windows/installer/build-agent.go ${platform}"}`.
- I checked the Wails v2.16.0 source:
  - hooks run before compile and before the bin clean;
  - the working directory is `build/bin`;
  - the command is split with `shlex`;
  - `${platform}` becomes `windows/amd64`.

**Not modified:** `wails_tools.nsh` (generated), the `modes/*.txt` texts and the spike files.

## Interfaces exposed
- **Installer command line:**
  - `/S`;
  - `/MODE=simple|advanced`: fresh installs only, otherwise exit 2;
  - `/D=<Program Files subfolder>`;
  - `/RELAUNCH`;
  - `/MIGRATE_FROM=<dir>`, `/MIGRATE_SID=<S-1-5-21-…|S-1-12-1-…>`.
- **Mode file** `%ProgramData%\VaultDock\config\mode.json`: `{"version":1,"mode":"simple|advanced","source":"installer","installer_version":"<v>"}`.
  - It is a superset of `modules.ModeFile{Mode}`; the extra fields are ignored by `json.Unmarshal`.
  - Only SYSTEM and Administrators can write it; Users can read it.
- **Data folder:** root, `state`, `keys`, `logs` and `spool` are SYSTEM/Admins only. `config` adds Users RX. Trust marker: `HKLM\Software\VaultDock` `DataDir`.
- **C5 marker** `%ProgramData%\VaultDock\config\migrate-<SID>.json`: `{"version":1,"from":"<dir>","sid":"<SID>","sid_source":"arg|token","installer_version":"<v>"}`.
- **Service:** registered by `vaultdock-agent install -mode <m>` as `"<INSTDIR>\vaultdock-agent.exe" service`, with every P0-27 setting.
- **HKLM Run:** `VaultDock` = `"<INSTDIR>\VaultDock.exe" --autostart --minimized`.
- **Build:** `payload/<arch>/vaultdock-agent.exe`. Env `VAULTDOCK_VERSION` overrides the agent version.

## Commands run and results
- `where makensis`: not available (as stated in the task), so the scripts were **not compiled**.
- Static checks:
  - balanced `If/EndIf`, `Do/Loop`, `Function`, `!if/!endif`, `!macro` and `Section` in every file;
  - all script files are ASCII with LF line endings;
  - each macro and plugin call was reviewed by hand against the NSIS 3 syntax (LogicLib `S!=`/`${Silent}`, `nsDialogs` `NSD_AddStyle`/`NSD_CreateLink`/`NSD_OnClick`, System `p` type and struct allocation, `NSIS_PTR_SIZE`, the `HKU` root, `Section /o "un.…"`, `MUI_FINISHPAGE_RUN_FUNCTION`, `MUI_PAGE_CUSTOMFUNCTION_PRE`).
- `cd desktop && go build ./...`: OK.
- `go vet build/windows/installer/build-agent.go`: OK; `gofmt -l`: clean.
- `cd desktop/build/bin && go run ../windows/installer/build-agent.go windows/amd64`: built `payload/amd64/vaultdock-agent.exe` (version 0.1.0). `vaultdock-agent.exe status` returned `{"installed":false}`. The payload was deleted afterwards.
- Nothing was run elevated, no service was installed, and no installer was executed.

## Contract issues
None. The API and backup-format contracts don't cover the installer.

## Gaps / follow-ups
1. **Not compiled.** The CI `nsis` job (`wails build -nsis` plus "Check installer exists") is the first real compile. The most likely things to check first:
   - `${U+24D8}` (fallback: `/DVD_INFO_GLYPH=(i)`);
   - `!if ${NSIS_PTR_SIZE} > 4`;
   - the System struct `*(i 12, p r1, i 0)`;
   - the `GetOptions` quoting of `/MIGRATE_FROM="…"`.
2. **Deliberate deviation: the per-user uninstaller is not run by the elevated installer.**
   - It lives in a user-writable folder, so running it as admin would run user-replaceable code elevated, which is a privilege escalation for over-the-shoulder UAC.
   - The installer instead closes the old UI, removes its Run value and writes the marker. The **UI runs `<from>\uninstall.exe /S` as the user** after `AdoptUserDevice`; this was the spike's original step 3.
   - `/DVD_MIGRATE_RUN_PERUSER_UNINSTALLER` turns on the elevated run (same SID only, `ExecWait … _?=`) if INT prefers it.
3. **Install dir is restricted to Program Files subfolders.** A custom folder elsewhere (for example `D:\Apps`) can be renamed or replaced by its non-admin creator, which would let them swap the SYSTEM service binary. The directory page explains the restriction.
4. **`/MODE` on upgrade is ignored**, and the existing mode is kept. The agent's `state.db` is authoritative and the manager only reads `mode.json` on first run, so rewriting it would give privileges that don't match the stored mode. Mode changes go through Settings → Mode (R11).
5. **ACL difference from §7.6.** §7.6 says "Users read on `logs` only"; INT's task says logs no, config read. I followed INT: `config` is Users RX and `logs` has no Users access. If the UI needs logs, it should get them over IPC.
6. **Signing (P0-34 / INT).** `release.yml` signs `VaultDock.exe` and `setup.exe` only. It does not sign `vaultdock-agent.exe` or the embedded uninstaller, so `!uninstfinalize` stays commented. When enabling it:
   - sign `desktop/build/windows/installer/payload/amd64/vaultdock-agent.exe` with `sign.ps1` **before** the repackaging `makensis` call;
   - uncomment the `!uninstfinalize` line. `sign.ps1` exits 0 when there is no certificate, so it is safe in unsigned builds.
7. **Version (P0-34).** Set `VAULTDOCK_VERSION: ${{ steps.v.outputs.version }}` on the release "Build installer" step. Otherwise the agent gets the stripped `wails.json` version, which loses the prerelease suffix.
8. **Not done:**
   - toast AppUserModelID registration (§7.6), which needs a shortcut property and so a plugin or the UI;
   - `uninstall-cleanup` (VaultDock's own shadow copies by id): the agent subcommand doesn't exist yet (DSK-SVC / P0-S1 owner), so the uninstaller doesn't call it;
   - an IPC "exit for update" before `taskkill` of the UIs (P0-28 / P1-29).
9. **De-elevated launch** uses the `explorer.exe "<exe>"` hand-off, so no arguments are passed (no `--updated`). It depends on a running shell, so it does nothing on Server Core.

## Notes for dependent tasks / INT
- **P1-29 (UI, `AdoptUserDevice` hand-off):**
  1. On start, ask the agent for a pending `config\migrate-<caller SID>.json`. The agent matches the pipe caller's SID; the marker's `sid` must equal the caller's SID.
  2. Validate that `from` is inside the **caller's own** profile (`%LOCALAPPDATA%`). Never act on a path outside it.
  3. In the user's session, unprotect the user-scope DPAPI device key from `%APPDATA%\VaultDock` and send it, together with `config.json` and `state.db`, over the authenticated pipe. The agent re-protects it machine-scope (`platform.ProtectMachine`, `keys\`, SYSTEM/Admins ACL) and imports the state.
  4. Run `"<from>\uninstall.exe" /S` **as the user**. Its silent default keeps `%APPDATA%\VaultDock` as the rollback copy, and it also removes the HKCU uninstall key and shortcuts. Then delete the HKCU Run value `VaultDock` if it is still present.
  5. The agent deletes the marker. `config` is admin-write only, so the UI can't.
  6. With `sid_source: "token"` from an over-the-shoulder elevation, the SID may be the admin's. The UI of the real user then finds no marker and should offer the move itself when `%LOCALAPPDATA%\Programs\VaultDock\VaultDock.exe` exists.
  7. In a machine install the UI must **not** write the HKCU Run value (`platform.SetAutoStart`), because HKLM Run already starts it with `--autostart --minimized`. Otherwise two UIs start.
- **DSK-MOD (P0-38):** the reader is unchanged and compatible. The installer never rewrites `mode.json` on upgrade.
- **DSK-SVC (P0-27):** the installer relies on:
  - `install -mode` creating or reconfiguring the service;
  - `stop -timeout`;
  - `uninstall -timeout`;
  - `start`;
  - a non-zero exit on failure.
  
  Please add `uninstall-cleanup` and call it out to REL-C when it exists. The NSIS sc fallback mirrors `SimplePrivileges` / `AdvancedExtraPrivileges`; keep the two in sync.
- **INT:** please check the wording of the migration page (Q5 UX) and the directory-page note.

## VM test plan (Windows 10 22H2 and Windows 11 VMs with snapshots; never on a dev machine)
Build with `wails build -nsis`, or `makensis -DARG_WAILS_AMD64_BINARY=<VaultDock.exe> project.nsi` once `payload\amd64\vaultdock-agent.exe` exists. There should be no compile warnings beyond unused-function warnings. Also run case 2 on a German-language VM.

| # | Case | Expected |
|---|---|---|
| 1 | Standard user runs setup | UAC prompt. Declining it changes nothing |
| 2 | Fresh interactive Simple install | Licence page, then Mode with Simple preselected. `config\mode.json` contains simple. On the root, `icacls` shows SYSTEM F and Administrators F, protected, owner Administrators. `config` also has Users RX, and `logs`/`keys`/`state` have no Users entry. `sc qc` shows the quoted binPath with `service`, delayed start and LocalSystem. `sc qprivs` shows ChangeNotify, Impersonate, **Tcb** and CreateGlobal. `sc qsidtype` shows UNRESTRICTED, `sc qfailure` shows 3 restarts, and the service is running. HKLM Run is present. Finish launches the UI **non-elevated** (check Task Manager → Elevated = No) |
| 3 | Fresh Advanced install | `qprivs` adds SeBackup and SeManageVolume, **not** SeRestore. The agent starts the component download |
| 4 | (i) links | Full text with scrolling and Next hidden. Back keeps the radio choice. Back from Directory skips the info page. Keyboard: Tab, then Enter/Space. Test at 100%, 150% and 200% DPI and in high contrast. The ⓘ glyph renders correctly |
| 5 | `/S /MODE=advanced`; `/S`; `/S /MODE=bogus` | Advanced; Simple; exit 2 with no changes |
| 6 | `/D=C:\Windows\x`, `/D=C:\`, `/D=D:\Apps\VaultDock`, `/D=%LOCALAPPDATA%\Programs\VaultDock` | Interactive: Next disabled. Silent: installs to `%ProgramFiles%\VaultDock`; the last case also becomes a migration source |
| 7 | Squatting: a user pre-creates `%ProgramData%\VaultDock` with a hard link to a foreign file, then separately as a junction | Moved to `VaultDock.untrusted-*`. The target file's ACL is unchanged and the new folder is clean |
| 8 | `PROGRAMDATA` overridden in `HKCU\Environment` | The real ProgramData is used |
| 9 | Upgrade over Advanced (service running, UI open in 2 sessions) | Service stopped and UIs closed. No mode or directory page; `mode.json` unchanged. `install` reconfigures the service, privileges stay Advanced, and the service runs again |
| 10 | Upgrade with `/S /MODE=simple` over Advanced | Log says "/MODE ignored"; mode and privileges unchanged |
| 11 | Agent install fails (rename the agent to force `error`) | The sc fallback registers the service; the log shows the fallback |
| 12 | Uninstall with the box unticked; then reinstall | Service gone (`sc query` gives 1060), Run value and program files removed. ProgramData and the HKLM marker are kept. The reinstall trusts the folder (not moved aside) and keeps the existing mode with no mode page |
| 13 | Uninstall with the box ticked; also `uninstall.exe /S` | Ticked: `%ProgramData%\VaultDock` and `HKLM\Software\VaultDock` removed. Silent: data kept. User files are untouched in both cases |
| 14 | C5, same-user admin: per-user 0.x enrolled, then the admin setup | Migration page with the path. Old UI closed, HKCU Run value removed, `migrate-<SID>.json` written with `sid_source` token. The per-user folder still exists until the UI adopts (P1-29) |
| 15 | C5, over-the-shoulder: standard user, last per-user release passes `/MIGRATE_FROM` and `/MIGRATE_SID` | Marker named with the user's SID (`sid_source` arg). The user's `HKU\<SID>\…\Run` value is removed. Nothing in the user's folder is executed (check with Process Monitor) |
| 16 | Old per-user updater runs `setup.exe /S /RELAUNCH /D=<per-user dir>` | Installs into Program Files and migrates from that dir. The UI is relaunched de-elevated |
| 17 | Deployment as SYSTEM (Intune/PDQ) with `/S /RELAUNCH` | Installs; no UI is launched as SYSTEM, and HKLM Run starts it at sign-in |
| 18 | Offline Advanced install | Install succeeds. The UI shows component download pending or retry |
| 19 | Service fails to start (corrupt agent) | Install completes and the log shows the start error. An `icacls` failure (simulated by denying the admin on a pre-existing trusted folder) aborts |
