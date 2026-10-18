# P0-S5 Installer modes spike (admin NSIS, mode page with ⓘ, service, agent-driven components)
Status: completed (prototype; not compiled because makensis is not installed)

## Goal
Prototype the admin NSIS pieces:
- a per-machine install that requires admin;
- a Simple/Advanced mode page with ⓘ info dialogs loaded from text files;
- `install-mode.json` under `%ProgramData%\VaultDock` with an ACL limited to SYSTEM and Administrators;
- agent service registration with `sc` (including `sc privs`);
- a documented decision that the agent downloads the components.

The README also covers Wails integration, the C5 per-user upgrade and a test plan.

## Changes (repo: path, created/modified/deleted, why)
- VaultDock: `planning/spikes/installer-modes/vaultdock_modes.nsh` (created). The reusable NSIS pieces:
  - `VD_Init`: `/MODE=`, upgrade detection, and resolving ProgramData from the known-folder API rather than the environment.
  - `VD_VerifyInstDir`: rejects `%WINDIR%`, the user profile and drive roots.
  - `VD_ModePageCreate`/`Leave`: nsDialogs radios plus `${NSD_CreateLink}` ⓘ links.
  - `VD_InfoPageCreate`/`VD_LoadInfoText`: a modal-like info page with a read-only scrolling EDIT, filled line by line so `NSIS_MAX_STRLEN` doesn't cap it.
  - `VD_StopForUpgrade`.
  - `VD_PrepareDataDir`: moves a squatted folder or reparse point aside; ACL by SID; fails closed on any `icacls` error.
  - `VD_WriteModeFile`: tmp file plus rename; explicit SYSTEM/Admins-only ACL.
  - `VD_RegisterService`: create or config, delayed-auto, LocalSystem, quoted binPath, failure actions, sidtype unrestricted, `sc privs` per mode, HKLM Run, start.
  - `VD_UninstallService` macro.
- VaultDock: `planning/spikes/installer-modes/spike.nsi` (created). A standalone MUI2 harness for compiling and testing in a VM. It needs `/DVD_PAYLOAD_DIR` for the binaries and the service step.
- VaultDock: `planning/spikes/installer-modes/modes/simple.en.txt`, `modes/advanced.en.txt` (created). Draft plain-language ⓘ texts covering functionality, scope, permissions, resources, differences and switching later.
- VaultDock: `planning/spikes/installer-modes/README.md` (created). Covers the design, why the agent downloads components, the Wails `project.nsi` integration steps, the C5 migration flow and a 15-case VM test plan.

The Wails installer files and `wails.json` were only read, not changed.

## Interfaces exposed
- Mode file `%ProgramData%\VaultDock\install-mode.json`, set by `!define VD_MODE_FILE_NAME`:
  ```json
  {"version":1,"mode":"simple|advanced","source":"installer","installer_version":"<v>"}
  ```
  - ACL: SYSTEM:F and Administrators:F, not inherited.
  - The agent imports it on first start into the `state.db` `modules` table. After that, `state.db` is authoritative.
- Installer command line:
  - `/MODE=simple|advanced`: any other value exits with code 2;
  - `/S`: without `/MODE`, gives simple on a fresh install and keeps the existing mode on an upgrade.
- Service `VaultDockAgent`:
  - binPath `"<INSTDIR>\vaultdock-agent.exe" service`;
  - delayed-auto start, LocalSystem;
  - SID type unrestricted.
- `RequiredPrivileges`:
  - Simple: `SeChangeNotifyPrivilege/SeImpersonatePrivilege/SeCreateGlobalPrivilege`;
  - Advanced: adds `SeBackupPrivilege/SeRestorePrivilege/SeManageVolumePrivilege`.
- HKLM Run value `VaultDock` = `"<INSTDIR>\VaultDock.exe" --tray`.
- HKLM uninstall key gets `InstallLocation`.
- Proposed (not built) C5 marker: `%ProgramData%\VaultDock\config\migrate-<SID>.json` `{"from","sid"}`, plus installer arguments `/MIGRATE_FROM= /MIGRATE_SID=`.
- Proposed agent subcommands: `service` and `uninstall-cleanup`.

## Commands run and results
- `where makensis`: not found. There is no `C:\Program Files\NSIS` or `C:\Program Files (x86)\NSIS`, so nothing was compiled and nothing was run. I didn't install NSIS, per hard rule 6.

## Contract issues
None.

## Gaps / follow-ups
- **Not compiled.** These need checking first:
  - `${U+24D8}` support;
  - `nsDialogs::CreateControl` style flags;
  - quoting of the `VD_Icacls`/`VD_Sc` macro arguments.
- **Mode-file path mismatch.** The task says `%ProgramData%\VaultDock\install-mode.json`; MASTER_PLAN §7.6 says `config\mode.json`. INT should pick one; it is a single `!define`.
- **Token for scheduled backups when no UI is running.** Pipe impersonation (only `SeImpersonatePrivilege`, as granted here) or `WTSQueryUserToken` (needs `SeTcbPrivilege`, deliberately not granted). Settle this with the S4U spike, then adjust `VD_PRIVS_*`.
- The VSS spike should confirm whether `SeManageVolumePrivilege` is needed.
- **Left for P1-30:**
  - the licence page;
  - a de-elevated UI relaunch from `.onInstSuccess`;
  - an `uninstall-cleanup` hook;
  - an IPC "exit for update" instead of `taskkill /F` on UIs.

## Notes for dependent tasks / INT
- **P1-30 (REL-C):**
  - Follow the README section "Integration with the Wails-generated installer". All edits go in `project.nsi`, since `wails_tools.nsh` is regenerated.
  - Set `REQUEST_EXECUTION_LEVEL "admin"`, `WAILS_INSTALL_SCOPE "machine"` and `InstallDir "$PROGRAMFILES64\..."`.
  - Ship `vaultdock-agent.exe` with an explicit `File` line, because Wails only packs the UI.
  - Move `modes/*.txt` to `desktop/build/windows/installer/modes/`.
- **DSK-SVC / DSK-MOD:**
  - Import and validate the mode file (`modules.Mode.Valid`), then drive the component download through `sdk/modules`. Components are never fetched by NSIS; the README gives six reasons.
  - On a mode switch, rewrite `RequiredPrivileges` with `ChangeServiceConfig2W` and restart.
- **C5 / Q5:** an elevated installer cannot trust its own HKCU or `%LOCALAPPDATA%` (over-the-shoulder UAC), so the last per-user release must pass the path and SID. Device-key adoption must happen in the user's session (`AdoptUserDevice`). The per-user uninstaller run with `/S` keeps `%APPDATA%\VaultDock` (`/SD IDNO`), which is useful as a rollback copy.
- **INT:** review the wording of the ⓘ texts. The Advanced text says "roughly tens of MB"; replace that with real sizes once the components exist.
