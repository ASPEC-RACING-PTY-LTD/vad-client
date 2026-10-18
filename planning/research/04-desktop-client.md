# 04: Desktop Client, Deployment and CI (Research and Plan)

Scope: `desktop/` (Wails v2 Windows client), `install.sh`, `.github/workflows/*`, `scripts/`.
Input: `planning/REQUIREMENTS.md`. Everything here is research and planning only. No source file was changed.

Citations use `file:line` relative to `desktop/` unless they start with another top-level directory.
Confidence flags: **[V]** means verified in source. **[H]** means high-confidence knowledge of Windows or Go behaviour that was not tested here. **[M]** means medium confidence and needs a spike.

Correction to the brief: `desktop/app.go` has **603 lines**, not 657. `frontend/src/App.tsx` has 114 lines. The biggest frontend file is `frontend/src/screens/Configs.tsx` (503 lines).

---

## 1. Current state

### 1.1 Process model (the most important finding)

| Aspect | Current behaviour | Evidence |
|---|---|---|
| Elevation | **Never elevated.** The installer uses `REQUEST_EXECUTION_LEVEL "user"` and `WAILS_INSTALL_SCOPE "user"`. The manifest has no `requestedExecutionLevel`. | `build/windows/installer/project.nsi:5-9`, `build/windows/wails.exe.manifest` [V] |
| Install location | `$LOCALAPPDATA\Programs\VaultDock`. The uninstall key goes to HKCU. | `project.nsi:51`, `wails_tools.nsh` (HKCU branch of `wails.writeUninstaller`) [V] |
| Service | **None.** No `golang.org/x/sys/windows/svc` usage, no SCM code, no named pipes. | grep over `desktop/**/*.go` for svc, VSS, elevat and admin found nothing [V] |
| Start mode | GUI process started at user logon through `HKCU\...\Run` with `--minimized`. Runs only while the user is signed in. | `internal/platform/platform_windows.go:15-16,47-64` [V] |
| Data location | `%APPDATA%\VaultDock` (Roaming) holds `settings.json`, `state.db` and `device.key`. Logs go to `%LOCALAPPDATA%\VaultDock\logs`. | `internal/platform/platform.go:13-29`, `app.go:79`, `main.go:42-50` [V] |
| Secrets | DPAPI **user scope** (`CryptProtectData` with only `CRYPTPROTECT_UI_FORBIDDEN`, no `CRYPTPROTECT_LOCAL_MACHINE`) and fixed entropy `"VaultDock device key v1"`. | `platform_windows.go:18,27-45` [V] |
| Single instance | Wails `SingleInstanceLock` with `UniqueId "vaultdock-desktop-5c1f7e0a"`. | `main.go:90-96` [V] |
| HKLM access | Reads only (`MachineGuid`, `ProductName`), which works without elevation. | `platform_windows.go:76-107` [V] |

**What this means:** VSS, writing to HKLM, reading other users' profiles and hives, `SeBackupPrivilege` reads, and backups while nobody is logged on are **all impossible** in the current process model. Files that another process holds open without `FILE_SHARE_READ` also cannot be read. Go's `os.Open` asks for `FILE_SHARE_READ|FILE_SHARE_WRITE` (Go `syscall_windows.go:395`), and the call fails with a sharing violation when another process denies sharing [V, local GOROOT].

### 1.2 App lifecycle and Wails bindings

- **Startup** (`main.go:36-108`): set `engine.Version`. When generating bindings, start an empty `wails.Run` that touches no profile data (`bindings_on.go`, `main.go:38-41`). Then create the data dir, open the rotating JSON log (`logging.go:23-65`, 10 MiB × 3), open `state.Open(state.db)`, then `engine.New`, `NewApp` and `newToasts`. The tray runs on its own locked OS thread (`main.go:110-168`) with the items Open, Sync all, Pause, Web UI and Quit, and refreshes its tooltip every 5 s.
- **`App.startup`** (`app.go:99-123`): wire `OnRevoked`, `ApplySettings`, `LoadCachedAutomations` and `eng.Start`, then start the goroutines `restoreConnection` (`app.go:126-166`) and `runUpdates`. A ticker **emits a `snapshot` event every second** (`app.go:116-122`).
- **Shutdown**: `eng.Stop()` then `launchPendingUpdate()` (`app.go:168-171`, `app_update.go:132-150`).
- **Bound methods** (all on `*App`, `main.go:97`):
  - Connection: `GetBootstrap`, `ProbeServer`, `Connect`, `CancelEnrollment`, `Disconnect` (`app.go:188-397`)
  - Sync: `ListConfigs`, `SaveConfig`, `DeleteConfig`, `SyncNow`, `PreviewConfig`, `PickFolder`, `PickFiles`, `SuggestFolders` (`app.go:431-494`)
  - Transfers: `GetSnapshot`, `SetPaused`, `RetryFailed`, `ListQueue`, `ListHistory` (`app.go:498-515`)
  - Automations: `ListAutomations`, `RefreshAutomations` (`app.go:519-524`)
  - Settings: `GetSettings`, `SaveSettings` (`app.go:528-556`)
  - Helpers: `OpenWebInterface`, `OpenLogsFolder`, `OpenFolder`, `ShowWindow`, `Quit`, `OpenURL` (`app.go:560-603`)
  - Updates: `GetUpdateStatus`, `CheckForUpdates`, `InstallUpdate` (`app_update.go:155-186`)
- **Events to the frontend**: `snapshot`, `connected`, `revoked`, `enrollment`, `settings` and `update` (`app.go`, `app_update.go:38`, `main.go:138`).
- Generated TypeScript bindings are **committed** (`frontend/wailsjs/go/main/App.d.ts`, `App.js`, `models.ts`). The release build passes `-skipbindings` (`.github/workflows/release.yml:224`), so whoever adds bindings must regenerate and commit these files. That makes them a merge hotspot (see §6).

### 1.3 Enrolment and key storage

- `Connect` generates an Ed25519 key, calls `client.Enroll` (sends the password once) and then polls `EnrollStatus` (`app.go:255-327`).
- On approval, `saveKey` DPAPI-protects the key and writes `device.key` atomically with tmp+rename (`app.go:399-412`). Settings record server, pin, device ID, username and fingerprint (`app.go:352-355`).
- If the server or account changes, `st.ResetIndex()` deletes the whole file index and queue (`app.go:362-365`, `state.go:202-205`).
- `restoreConnection` treats a 401 (other than clock skew or replay) as revoked and any other error as offline (`app.go:147-160`).
- **Consequence for DR**: the device key is bound to the Windows user's DPAPI master key. After a Windows reinstall it **cannot be recovered**, so the device must re-enrol. That is acceptable for identity. It is **not** acceptable for backup encryption keys (see §3.4) [H].

### 1.4 Configuration model

- `config.SyncConfig` (`internal/config/config.go:16-56`) has these fields:
  - `Kind` folder or files, `LocalPath`, `Files` (max 5000), `Destination`, `Recursive`
  - include and exclude subfolders, extensions and glob patterns, min and max size, `ModifiedAfter`, `SkipHidden`
  - `Mode` auto or manual, `ScanIntervalMinutes` (1-1440), `StableSeconds` (1-3600)
  - `AutomationMode` all, selected or none
  - `OnConflict` replace, rename or skip. This is enforced **server-side** (`internal/api/uploads.go:123-128`). The comment says "replace" sends the previous version to trash (`config.go:51-53`, server `internal/store/commit.go:24`).
- Validation rejects whole-drive roots (`config.go:122-124,180-184`). Globs are validated through `pkg/automation.CompileGlob` (`config.go:161-165`).
- `config.Settings` (`config.go:196-214`) is stored in `settings.json`, not in SQLite (`app.go:81-97`). Defaults: auto-start, close-to-tray, auto concurrency (max 32), notifications and auto-update on (`config.go:216-218`).
- Sync configurations are stored as JSON blobs in the `configs` table of `state.db` (`state.go:25,85-110`).

### 1.5 Local state DB (`internal/state/state.go`, 447 lines, no tests)

**Schema** (`state.go:24-49`): `configs(id, data JSON)`, `files` (index keyed by `(config_id, rel_path)`, with size, mtime ns, sha256, remote_path, status and reason), `queue` (one row per file, `UNIQUE(config_id, rel_path)`, with event, size, mtime, remote_dir and name, tags JSON, attempts, `next_at`, status pending/active/failed, error and manual), `history` (bounded to the last ~5000 rows, `state.go:411`) and `kv`.

**Pragmas**: WAL, `busy_timeout(10000)`, `synchronous(NORMAL)`, `_txlock=immediate`, max 4 connections (`state.go:57-66`).

**No schema versioning or migrations.** Only `CREATE TABLE IF NOT EXISTS` is used (`state.go:67`), so adding a column needs a migration framework first [V].

**Queue semantics:**
- *Upsert*: a re-enqueue overwrites metadata, resets attempts when the row was failed, and keeps `active` rows active (`state.go:248-261`).
- *Claim*: one transaction selects `pending AND next_at<=now ORDER BY next_at,id`, skips excluded configs and marks the rows `active` (`state.go:264-299`). There is no priority and no per-config fairness.
- *Crash recovery*: `active` goes back to `pending` on open (`state.go:72`) and on stop (`ReleaseActive`, `state.go:347-349`).
- *Outcomes*: `Done` deletes the row. `Retry` increments attempts and sets `next_at`. `Defer` reschedules **without** counting an attempt. `Fail` marks the row failed. `RetryFailed` resets failed rows to pending (`state.go:308-344`).
- Per-config counts use the `files` and `queue` tables (`state.go:195-199`).

### 1.6 Sync engine pipeline (`internal/engine`)

The package documentation states the contract: **one-way local → server, never deletes or modifies local files, never deletes server copies** (`engine.go:1-5`). The diagram below is the entire pipeline.

```
Scan (WalkDir) ─┐                              ┌─ hash (sem=2) ─ CreateUpload ─ PutChunkHashed×N ─ Complete
Watch (notify) ─┴─ filter ─ consider(automations) ─ queue (SQLite) ─ dispatchLoop ─ process ┤
                                                                                │  (adaptive Controller, limiter)
                                                                                └─ handleErr → Retry/Defer/Fail
```

**Start** (`engine.go:127-158`): load configs, start watchers for enabled `auto` configs, start `dispatchLoop` and `timerLoop`, then run an initial scan of all auto configs.

**Scan** (`scan.go:39-157`):
- Walks with `filepath.WalkDir`, uses the filter's `WalkDir` pruning and skips symlinks and non-regular files (`scan.go:131`).
- Detects changes by **size + mtime(ns)** against the index (`scan.go:80`) and does not hash during the scan.
- Records missing files as `deleted` locally. Server copies remain (`scan.go:143-151`).
- **Unreadable folders and `Info()` errors are skipped silently** (`scan.go:112-116,134-137`). That violates requirement 4.

**Watch** (`watch.go`):
- `rjeczalik/notify` (ReadDirectoryChangesW on Windows), recursive using the `...` suffix (`watch.go:56-60`).
- For `files` configs it watches the parent folders.
- Per-path debounce of 1.5 s (`watch.go:35,86-103`). In an event storm (more than 20,000 pending paths) it drops tracking and runs a full rescan (`watch.go:104-111`).
- Rename detection: a deletion followed within 4-5 s by a create with the same size and mtime counts as a rename (`watch.go:176-191,202-227`).
- If watching fails, it falls back to periodic rescans and shows a message (`watch.go:62-74`).

**Consider** (`scan.go:174-224`):
- A 10-minute dedupe map by `(cfg, rel, size, mtime, ev)`.
- Evaluates `automation.EvaluateAll`, then side effects (folder creation, notifications, start other task with a loop guard, `automations.go:131-190`).
- A modified file keeps its previous remote path (`scan.go:194-204`).
- Enqueues with `next_at = mtime + StableSeconds` (`scan.go:210-215`).

**Process** (`transfer.go:243-352`):
- Re-stats the file. If it changed, it is re-considered.
- If the stability window has not passed, the item is deferred.
- `os.Open` failure leads to **`Defer` 30 s with "file is in use; waiting", with no attempt count, so a permanently locked file loops forever** (`transfer.go:279-283`). That violates requirement 4 [V].
- Full-file SHA-256 behind `hashSem` (capacity 2, `engine.go:111`), followed by a re-stat (`transfer.go:286-303`).
- `CreateUpload(path,size,sha256,mtime,tags,on_conflict)`. The server may answer `complete` immediately (whole-file dedup, `unchanged` or `skipped`).
- Otherwise it uploads missing chunks with up to 4 workers per file (`transfer.go:22,376-414`). Each chunk is **re-read and re-hashed** by `client.HashSection` (`transfer.go:431`), so a file is read at least 3 times: full hash, chunk hash and send.
- `CompleteUpload` handles the server codes `hash_mismatch`, `size_mismatch`, `not_found` and `upload_gone` (`transfer.go:319-335`).
- Resumability comes from the server's upload session (`Received[]`, `pkg/client/upload.go:24-54`). The engine itself does not persist the upload ID. Resume relies on the server matching the session by path, size and hash on a repeated `CreateUpload` (`Resumed` flag, `upload.go:56-62`) [M: server side not verified here].

**Adaptive concurrency** (`concurrency.go:10-208`): a TCP-like hill climber (slow start, +12 %, a 30 % cut on 429/502/503/504 or timeout, latency-inflation back-off, hold periods), bounded by user max, server `MaxConcurrentChunks` (`engine.go:187-191`) and 128. Dispatch admits `limit+2` files in flight (`transfer.go:78`). One `Controller` exists per `Engine`. It has tests (`concurrency_test.go`).

**Bandwidth**: `golang.org/x/time/rate` limiter wrapped around chunk bodies via `client.ChunkBody` (`engine.go:203-219,390-414`).

**Retries and offline:**
- `handleErr` (`transfer.go:167-216`) handles these cases:
  - Cancel: defer the item.
  - 401 revoked: stop the engine.
  - Network error: retry with backoff. After 3 consecutive failures, `offlineUntil` grows up to 5 min.
  - 507: retry in 15 min.
  - Retryable errors: exponential backoff from 5 s up to 30 min with 20 % jitter (`transfer.go:144-149`), up to 12 attempts (`transfer.go:23`).
  - Anything else: `Fail` plus history and report.
- While offline the queue simply persists. There is no network-state detection (polling only).

**Reporting** (`report.go`):
- Every 1 s: speeds. Every 2 s: controller tick. Every 10 s: flush transfer reports and notifications to the server, held in memory and capped at 1000 and 200, so they are **lost on restart** (`report.go:142-178`).
- Every 15 s: `ReportState`, which includes **local paths** of configs (`report.go:91-140`).
- Every 30 s: schedules and periodic rescans. Every 120 s: refresh automations.

**Automation runtime** (`automations.go`):
- Fetches device automations, validates them locally with `pkg/automation`, and caches them in `kv` for offline use (`automations.go:30-82`).
- Events: `file.created`, `file.modified`, `file.renamed`, `file.deleted`, `schedule.tick`, `sync.completed` and `sync.failed` (`pkg/automation/engine.go:18-24`).
- `schedule.interval` has a minimum of 5 min and does not fire on first sighting (`automations.go:228-264`).
- `sync.completed` and `sync.failed` fire when a config's queue drains (`automations.go:194-225`).

### 1.7 Filters (`internal/filter/filter.go`, tested)

Built-in ignores: `desktop.ini`, `thumbs.db`, `~$*`, `*.tmp`, `*.part` and similar (`filter.go:24`). `WalkDir` pruning handles recursion, hidden folders, exclude and include subfolders, and `/**` exclude patterns (`filter.go:64-97`). `Match` returns a human-readable reason (`filter.go:100-148`).

Gaps:
- No `.gitignore` semantics and no rule packs or presets.
- No `FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS` or `OFFLINE` check. `isHidden` checks only HIDDEN|SYSTEM (`engine/fileinfo_windows.go:11-16`). **Syncing a OneDrive Files-On-Demand folder would hydrate (download) every placeholder** [H].

### 1.8 Platform layer: Windows APIs actually used

- `CryptProtectData` and `CryptUnprotectData` (`platform_windows.go:27-45`)
- Registry: HKCU Run read and write, HKLM `MachineGuid` and `CurrentVersion` reads (`platform_windows.go:47-107`)
- `Win32FileAttributeData` for hidden and creation time (`engine/fileinfo_windows.go`)
- `ReadDirectoryChangesW` through rjeczalik/notify
- WinRT toast through `go-toast/wintoast` with AppUserModelID `VaultDock.Desktop`. The PowerShell fallback is deliberately disabled to avoid script injection (`notify.go:13-43`). Keep this principle: **never build PowerShell scripts from data**.
- `DETACHED_PROCESS` launch for the updater (`internal/updater/launch_windows.go`)
- WebView2 through Wails. The installer bootstraps WebView2 online if it is missing (`project.nsi:87`, `wails_tools.nsh` `wails.webview2runtime`).

Not used anywhere: VSS, `SeBackupPrivilege`, `BackupRead`, `SHGetKnownFolderPath`, power APIs, priority APIs, `NetworkCostManager`, service APIs, named pipes.

### 1.9 Updater (`internal/updater`, tested)

- Polls GitHub Releases for `ASPEC-RACING-PTY-LTD/vad-client` (`app.go:32`): the first check after 2 min, then every 6 h (`app_update.go:18-22`).
- Asset regex `^VaultDock-<ver>-amd64-setup\.exe$` (`updater.go:28`).
- Download only from trusted github.com URLs, verified against SHA-256 from the release (`SHA256SUMS`, produced at `release.yml:252-254`). The checksum is checked again before install (`updater.go:274-293`).
- Runs `setup.exe /S /RELAUNCH /D=<dir>` detached after the app has shut down (`updater.go:297-305`, `app_update.go:132-150`). The installer kills the running exe (`project.nsi:57-61`) and relaunches it with `--updated` (`project.nsi:78-82`).
- Auto-install waits for `syncIdle` (`app_update.go:87-112`).
- **There is no Authenticode check.** Integrity depends only on GitHub plus SHA256SUMS from the same release [V].

### 1.10 Frontend (`frontend/src`, React 19, Tailwind 4, Vite 8, no router, no React Query)

- `App.tsx` (114 lines): bootstrap gate. When not configured or revoked it shows `ConnectScreen`. Otherwise it shows a sidebar with 5 tabs: Overview, Sync folders, Transfers, Automations, Settings (`App.tsx:15,55-61`). Global events are wired at `App.tsx:31-46`.
- Screens:
  - `Connect.tsx` (238): probe, TLS pin, enrol, wait for approval.
  - `Overview.tsx` (122): stats tiles and config summary.
  - `Configs.tsx` (503): editor, preview modal, suggestions for Medal, Screenshots, Documents and Videos (`app.go:476-494`).
  - `Transfers.tsx` (127): tabs Active, Queued, Failed and History, with Retry failed.
  - `Automations.tsx` (69): read-only list.
  - `Settings.tsx` (184): autostart, tray, notifications, concurrency, bandwidth, updates and disconnect.
- `ui.tsx` (409) is a shared component kit (Modal, Tabs, Toggle, Menu, Toast provider).
- There is **no first-run wizard beyond enrolment**, no restore UI and no frontend tests.

### 1.11 Deployment and CI

- `install.sh` (721 lines) is the **Linux server** installer (Docker Compose, Caddy or tunnel, upgrade, cron auto-update). It is not relevant to the Windows client except for sharing the release tag flow.
- `ci.yml`: server tests on Ubuntu, web build, **desktop `go vet` and `go test` on windows-latest after building the frontend** (`ci.yml:48-74`), shellcheck of `install.sh`, and a Docker image build. The NSIS installer is **not** built in CI, only on release.
- `release.yml`: on a tag, build the multi-arch server image and the Windows installer through `wails build -nsis` (`release.yml:168-238`), then publish the GitHub release with `SHA256SUMS`. There is **no code signing**: the signtool lines are commented out (`project.nsi:45-47`). There is a single artifact, the installer.
- `scripts/`: `dev-server.ps1` runs a local server with throwaway data. `make_icons.py` generates icons.

---

## 2. Reuse map for requirements 1-5, 9 and 10

| Req | Reuse as-is | Reuse with refactor | Gap (new) |
|---|---|---|---|
| 1 Backup engine | `filepath.WalkDir` scan loop pattern (`scan.go:108-140`), `filter.Filter` reasons (`filter.go:100-148`), `isHidden` and `createdTime` | Generalise `filter` into a rule engine that takes presets (dev, games, AppData), not only `SyncConfig` | Discovery: apps, registry, saves, dev projects, inventory; per-app modes |
| 2 Independent jobs | SQLite queue pattern (claim, retry, defer, crash recovery `state.go:72,264-349`), adaptive `Controller`, rate limiter, chunked hash-verified resumable upload (`transfer.go:376-467`, `pkg/client/upload.go`) | Extract the transfer core from `engine` into `internal/transfer` so one controller can serve several queues | Per-job manifest, CDC chunker, zstd, packs, local dedup index, snapshot commit |
| 3 Recovery | Nothing on the desktop (no download path in `pkg/client`). Server `GET /files` and `/files/{id}/content` are already "session or device" (`internal/api/server.go:543-558`) | Add device-side list and download wrappers to `pkg/client` | Restore engine, path and SID remap, app reinstall, registry import |
| 4 Errors | `PreviewEntry` reasons (`scan.go:251-259`), `history` table, `RetryFailed` (`state.go:332-344`), Transfers Failed tab | Make `Defer` loops bounded, add error classes | Skip, skip-all, Retry Skipped, completion summaries, durable report outbox |
| 5 Sync | All of `engine`, `watch`, `filter`, `automations`, server `OnConflict`, `KeepReplaced` trash | Schedules (dispatch gate via `ClaimReady` exclude, `state.go:264`), presets | Remote-change conflict detection (base hash), version-history UI, optional two-way (major) |
| 9 Reliability and security | DPAPI wrapper, Ed25519 signed client, SHA-256 verify, WAL SQLite queue, offline `offlineUntil` | DPAPI machine scope for the service | Client-side encryption and recovery key, VSS, priority and battery governor, verification, server-independent restore tool |
| 10 UX | Tray, toasts, 1 s snapshot events, `ui.tsx` kit, enrolment flow, auto-update | `App.tsx` navigation, `Overview` | First-run wizard, readiness report, guided post-reinstall restore, notification preferences |

---

## 3. Required desktop changes

### 3.1 Process architecture: privileged agent service plus user UI

**Problem.** VSS, SeBackupPrivilege, HKLM registry, other profiles, backups without a logged-on user and installing apps for all users all need admin or SYSTEM rights. The current app is per-user and unelevated (§1.1).

**Proposal: one engine host, two deployment modes.**

```
┌───────────── Per-user session ─────────────┐        ┌──────── Session 0 ────────┐
│ VaultDock.exe (Wails UI, tray, toasts)     │  IPC   │ VaultDockAgent.exe        │
│  └ agent.Client (named pipe)  ─────────────┼───────►│  (Windows service,        │
│                                            │◄───────┤   LocalSystem)            │
│ [per-user mode: agent.Host in-process]     │ events │  agent.Host:              │
└────────────────────────────────────────────┘        │   sync engine, backup     │
                                                      │   engine, scheduler,      │
                                                      │   transfer core, VSS,     │
                                                      │   restore, updater        │
                                                      └───────────────────────────┘
```

- **`internal/agent.Host`** owns the state stores, the sync engine, the backup engine, the shared transfer core, the scheduler and the server client. It exposes a Go interface, `agent.API`, that covers today's App methods plus backup and restore methods.
- **Per-user mode** (current installer, no admin): the UI process creates `agent.Host` in-process. This is exactly today's behaviour and preserves all current functionality. VSS and machine-wide sources are unavailable, and the readiness report says so.
- **Service mode** (new per-machine installer, admin once): `VaultDockAgent.exe` runs `agent.Host` under the SCM (`golang.org/x/sys/windows/svc`, BSD-3). The UI becomes a thin client over a named pipe. One controller means one global concurrency and bandwidth budget for sync and backup, which avoids duplicate subsystems.
- **IPC**:
  - Named pipe `\\.\pipe\VaultDock.Agent.v1`. The SDDL grants SYSTEM and Administrators full access and grants Authenticated Users read and write on the pipe. `github.com/Microsoft/go-winio` provides SDDL pipes (MIT) [H]. The OSS agent should confirm the licence and maintenance.
  - Length-prefixed JSON-RPC with request/response plus a server-push event stream that replaces the Wails `snapshot` ticker.
  - Authorisation: the agent identifies the caller with `GetNamedPipeClientProcessId` and `ImpersonateNamedPipeClient` to get the token's user SID.
    - Read-only status is allowed for any interactive user.
    - Configuration changes and restores into a profile are allowed only for that profile's owner or for Administrators.
    - Machine-wide jobs (HKLM, other profiles) and destructive operations need the Administrators group. Each such operation also needs UI confirmation.
  - There is **no TCP listener**, so remote attack surface is unchanged.
- **Identity in service mode**: one VaultDock device per machine.
  - `device.key` moves to `%ProgramData%\VaultDock\` with an ACL of SYSTEM and Administrators only.
  - Protection uses DPAPI with `CRYPTPROTECT_LOCAL_MACHINE`. Without a restrictive ACL that is weak protection [H].
  - Migrating from per-user mode means re-enrolling, or importing the key after `Unprotect` in the user's session followed by `Protect(machine)` through the IPC call `AdoptUserDevice`.
- **Paths**: `platform.DataDir()` (`platform.go:13-20`) must become mode-aware. Service mode uses `%ProgramData%\VaultDock` and per-user mode keeps `%APPDATA%\VaultDock`. For a SYSTEM process `os.UserConfigDir()` resolves to `C:\Windows\System32\config\systemprofile\AppData\Roaming` [H], which is wrong.
- **Notifications**: a session-0 service cannot show toasts [H]. The UI process subscribes to agent events and calls the existing `toasts.Show` (`notify.go:31-43`). When no UI is running, events are persisted and shown at the next UI start, and they are also pushed through the existing `client.Notify` to the web (`report.go:166-177`).
- **Multi-user PCs**: for each loaded profile the service can enumerate `HKU\<SID>`. Plan v1 as "back up profiles of users who opted in through the UI". Never load unloaded `NTUSER.DAT` hives on a running system. That is risky and needs SeRestorePrivilege [M].

### 3.2 Keeping sync and DR separate while sharing machinery

| Layer | Sync (existing) | Backup / DR (new) | Shared |
|---|---|---|---|
| Definition | `config.SyncConfig` in `state.db.configs` | `backup.Job` in `backup.db.jobs` | rule engine (`filter` v2) |
| Local DB | `state.db` (files, queue, history) | **separate** `backup.db` (jobs, runs, manifests, chunk index, pack queue, issues) | migration helper package |
| Server namespace | user file tree (`nodes`) | backup repository: packs, snapshots, keys, not in the file tree | upload session protocol |
| Unit of upload | whole file at a remote path | encrypted, compressed **pack** of CDC chunks | `transfer.Uploader` (chunk PUT, verify, retry), `Controller`, limiter |
| Change detection | size+mtime then SHA-256 | size+mtime+fileID then CDC chunks against the local dedup index | scanner walker, VSS path translator |

The separate DB file isolates the DR state from `ResetIndex()` (`app.go:362-365`) and from config edits that wipe the index (`engine.go:324-328`). It also keeps the `state.go` hotspot small. The transfer core is extracted once (WS-B) and both engines submit `transfer.Task`s to a weighted scheduler, for example sync interactive > backup scheduled > verification. Sync stays real-time and DR snapshots stay immutable and point-in-time.

### 3.3 Backup job model, per-job manifest, queue and retry

- **`backup.Job`** fields: `ID`, `Name`, `Kind` (`folder|profile|app|game|devproject|registry|inventory`), `SourceRef` (path, app ID, Steam app ID or registry key set), `Mode` (`full|data-only|reinstall-inventory`, req 1), `Rules` (filter v2), `ScheduleID`, `Destinations` (repository IDs, req 6 hook), `Retention` (server-evaluated), `UseVSS` (`auto|required|off`), `Priority`, `Enabled`.
- **Run pipeline, per job and fully independent (req 2):**
  1. Snapshot: VSS set for the volumes the job touches, shared between jobs that start in the same window.
  2. Walk with rules.
  3. For each file:
     - Compare against the job's previous manifest (size, mtime, file ID). Unchanged files reuse the old chunk list without being read.
     - Changed files go through FastCDC (target 1 MiB, min 256 KiB, max 4 MiB) to get the chunk list. Each chunk ID is `HMAC-SHA256(repoKey, plaintext)`, and the local dedup index is consulted.
     - New chunks are zstd-compressed, then AEAD-encrypted, then appended to an open pack.
  4. A pack is sealed at about 32 MiB or after a 30 s timeout, and the sealed pack is enqueued for upload **immediately**. Uploading never waits for the run to finish (req 2).
  5. When the run ends: write the manifest (tree with chunk lists, metadata and path tokens), encrypt it, upload it as an object, then `CommitSnapshot` with the referenced pack IDs.
  6. The snapshot becomes visible only after all of its packs are confirmed. Partial runs stay resumable "checkpoints".
- **`backup.db` tables:**
  - `jobs`
  - `runs` (job_id, status, started, finished, counts, bytes_new, bytes_dedup)
  - `run_files` (only for files that changed in this run, so checkpoints can resume)
  - `chunks` (chunk_id PK, pack_id, offset, len, uploaded bool)
  - `packs` (pack_id, path in local spool, size, sha256, status pending/active/uploaded/failed, attempts, next_at)
  - `issues` (see §3.12)
  - `schema_version`
- **Local spool**: sealed packs are written to `%ProgramData%\VaultDock\spool` and deleted after the server confirms them. A spool size cap provides back-pressure that pauses chunking. Packs are already encrypted, so the spool holds no plaintext.
- **Resumable transfers**: reuse server upload sessions with 8 MiB chunks and `Received[]`. Persist the `upload_id` in `packs` so that resuming does not depend on server-side matching (a current gap, §1.6).
- **Parallelism**: N job workers, each with its own scan/chunk pipeline (CPU bound, about `GOMAXPROCS/2`, tunable), and a shared pack uploader through `transfer.Controller`.

### 3.4 Encryption and keys (req 9, server-independent restore)

- **Repository master key** `MK`: 32 random bytes per repository (per user account, or per destination when destinations are added). From it derive `K_enc` (AEAD: XChaCha20-Poly1305 or AES-256-GCM, from `golang.org/x/crypto`, already an indirect dependency at `go.mod:47`) and `K_id` (HMAC chunk IDs) through HKDF.
- **Wrappings, stored as small key files on the server and inside each destination repository:**
  1. **DPAPI wrap**, for unattended operation on this machine only: machine scope in service mode, user scope in per-user mode. It does **not survive a reinstall** [H].
  2. **Recovery key**: 256-bit random, shown once as 24 words or grouped base32, plus a printable PDF or QR code. Wrap key = HKDF(recovery key). This is mandatory in the first-run wizard, and the user must re-enter it once to confirm.
  3. **Optional passphrase**: Argon2id (the server already uses Argon2id for web passwords) wraps `MK`.
- The server stores only wrapped blobs and ciphertext. Restore needs (server or destination) + (recovery key or passphrase), so the management server is not required (req 9).
- Chunk IDs are keyed with `K_id`, so the server cannot run confirmation-of-file attacks. Dedup is therefore **per repository**, and cross-user dedup is impossible. This trade-off must be documented for the server agent: the server's SHA-256 whole-file dedup does not apply to backup packs.
- **EFS-encrypted files** read as SYSTEM through `ReadEncryptedFileRaw` stay EFS-encrypted. They can be restored only with the user's EFS certificate. v1 should read them in the user's context (plaintext, then our encryption) or flag them [M].
- **DPAPI-protected application secrets** (browser saved passwords, Wi-Fi keys, credential manager) are bound to the old user's master key and SID. After a clean reinstall they **cannot be decrypted** even when the files are restored [H]. Mark them "not portable" in the app catalogue and recommend vendor sync or export.

### 3.5 VSS

- **Approach A (v1)**: WMI `Win32_ShadowCopy.Create(Volume, "ClientAccessible")` through `go-ole` IDispatch. go-ole is already an indirect dependency (`go.mod:25`). Read through `\\?\GLOBALROOT\Device\HarddiskVolumeShadowCopyN\...` and delete with `Win32_ShadowCopy.Delete_` **by the ID we created only**. This works on client SKUs, where `vssadmin create shadow` is Server-only [H]. Writer participation and app-consistency guarantees through WMI are weaker than with a proper requestor [M].
- **Approach B (v2)**: a native requestor through `IVssBackupComponents` (`CreateVssBackupComponents`, `GatherWriterMetadata`, `StartSnapshotSet`, `AddToSnapshotSet`, `PrepareForBackup`, `DoSnapshotSet`, `BackupComplete`). This is a non-IDispatch COM vtable, so it needs `syscall` vtable calls in Go [M]. A spike is required. The fallback is a small C++ helper exe, but that adds a toolchain.
- **Policy**:
  - VSS only in service mode.
  - Snapshot per volume, shared by the jobs that start together, held at most N minutes, always cleaned up on startup (orphan sweep by a VaultDock tag kept in a `backup.db` table).
  - Without VSS: open with maximal sharing (`FILE_SHARE_READ|WRITE|DELETE` through `windows.CreateFile`) and with `FILE_FLAG_BACKUP_SEMANTICS` plus `SeBackupPrivilege` in service mode. That bypasses ACLs but not sharing locks. On a sharing violation, record an **issue** (§3.12) and do not defer forever.
- **Antivirus risk**: deleting shadow copies is a ransomware indicator. Delete only our own shadow IDs, sign the binaries (§4) and never call `vssadmin delete`.

### 3.6 Application discovery and inventory (req 1, 3)

Implement each source behind `sources.Provider{ Discover(ctx) ([]Item, error) }`. Every external tool is run with `exec.Command` and an argument array, with no shell and no PowerShell script assembled from data (following `notify.go:13-15`).

| Source | Method | Elevation |
|---|---|---|
| Win32 apps | `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall` (64-bit view and `WOW6432Node`) plus `HKU\<SID>\...\Uninstall` per loaded profile: DisplayName, Version, Publisher, InstallLocation, UninstallString, `SystemComponent` filter | read: none / other users: SYSTEM |
| winget | `winget export -o <tmp>.json --include-versions` in **user context** (winget under SYSTEM is unreliable [M]). In service mode, ask the UI process to run it and upload the result over IPC | user |
| Chocolatey | `%ProgramData%\chocolatey\lib\*\*.nuspec` (parse id and version), no execution needed | none |
| Scoop | `%USERPROFILE%\scoop\apps\*\current\manifest.json` plus buckets | user |
| MSIX/Store | `PackageManager.FindPackagesForUser` (WinRT) [M], or registry `HKCU\Software\Classes\Local Settings\Software\Microsoft\Windows\CurrentVersion\AppModel\Repository\Packages` (read-only) | user |
| Steam | `HKCU\Software\Valve\Steam\SteamPath`, then `steamapps\libraryfolders.vdf` and `appmanifest_*.acf` (VDF parser) | none |
| Epic | `%ProgramData%\Epic\EpicGamesLauncher\Data\Manifests\*.item` (JSON) | none |
| GOG | `HKLM\SOFTWARE\WOW6432Node\GOG.com\Games\*` | none |
| Game saves | Data-driven manifest mapping game to save paths (`%USERPROFILE%\Saved Games`, `Documents\My Games`, `AppData\LocalLow`, Steam `userdata\<id>\<appid>`, registry keys). The OSS agent should evaluate the Ludusavi manifest and its data licence before reuse | user/none |
| Dev projects | Find `.git` directories under chosen roots. Record remote URL, branch, HEAD and dirty state. Rule pack excludes `node_modules`, `target`, `bin`, `obj`, `.venv`, `dist`, `.gradle`. Optional `.gitignore` honouring. Mode `reinstall-inventory` means remote+commit only for clean repos with a pushed HEAD | user |
| App settings | Curated catalogue: app ID to AppData paths, registry keys and portability flags. Mode `data-only` uses the AppData set without reinstall info | user |

The inventory is uploaded as part of the snapshot. It is also uploaded **unencrypted** to the server as metadata (`PUT /device/inventory`) only if the user opts in, because the web UI needs to browse apps (req 7). That privacy trade-off is the user's choice.

### 3.7 Registry export

- **Per-app keys**: `HKCU\Software\<Vendor>\<App>` and selected HKLM keys. Export with `RegOpenKeyEx` plus a value walk into our own JSON (typed values) rather than `reg.exe`. This allows filtering and SID/path rewriting on restore.
- **Optional full-hive capture** in service mode: `RegSaveKeyEx` of `HKU\<SID>` (needs SeBackupPrivilege) is **archive only and never auto-restored**.
- On restore, rewrite embedded profile paths and SIDs (§3.8). HKLM writes need service mode and per-key confirmation.

### 3.8 Restore engine

- **Path tokens in manifests**: `{PROFILE}`, `{APPDATA}`, `{LOCALAPPDATA}`, `{DOCUMENTS}` (resolved through `SHGetKnownFolderPath`, because Documents may be redirected to OneDrive), `{PROGRAMDATA}`, `{PROGRAMFILES}` and volume IDs. Also store the source SID, username and machine name.
- **Mapping**: auto-map tokens to the target user's known folders. A granular manual mapping UI shows a source-tree to destination table with conflicts (exists/newer/older) and per-entry Skip, Overwrite or Rename.
- **Username change detection**: compare source and target profile paths and SIDs. Rewrite paths inside restored registry values and known text config files only where the catalogue says it is safe.
- **ACL avoidance (req 3)**: never apply stored security descriptors. Create files fresh so they inherit the destination ACL. Restore only timestamps and the readonly and hidden attributes. When restoring into another user's profile as SYSTEM, set the owner to the target user SID through `SetNamedSecurityInfo` with OWNER only. Do not restore files with the System attribute into system locations.
- **App reinstall**: plan per app: winget ID, choco ID, scoop app, Store link or "manual installer URL". Run unattended where possible with `winget install --id X -e --silent --accept-package-agreements`. That command accepts licences, so **the UI must get explicit user consent per run**. Then restore data, then settings and registry. Restoring settings before the app is installed is allowed only for apps marked `settings-before-install`.
- **Engine**:
  - Fetch the manifest, compute the needed packs and chunks, fetch packs with HTTP Range (server), decrypt, decompress, verify the chunk HMAC, and write to `target.vdtmp`.
  - Verify the whole-file hash from the manifest, then atomically rename.
  - Restore runs are resumable (`restore_runs` and `restore_items` in `backup.db`) and use the same issue model (§3.12).
- **Verification and recovery testing (req 9)**: a `verify` job samples or fully reads packs, checks the AEAD and the manifest graph, and can test-restore to a temp folder with a hash compare. The result feeds the readiness report.

### 3.9 Guided post-reinstall restore and first-run wizard (req 10)

- **First run** (extending `ConnectScreen`):
  1. Connect (existing).
  2. Detect existing backups for this account from other or previous devices. The device identifier hashes MachineGuid (`platform.go:33-41`), and a reinstall changes MachineGuid, so the user must choose, for example "This PC was reinstalled: restore from DESKTOP-ABC (last backup 2 days ago)?"
  3. **New-protection path**: recommended sources (profile known folders, discovered apps' settings, game saves, dev roots, inventory), then schedule, then recovery key (display, confirm, print), then optional "Install background service (requires administrator)", which launches the elevated installer or service install, then a summary.
  4. **Restore path**: pick snapshot, then user/profile mapping, then app plan, then data, then a report.
- **Readiness report**: per job, show last successful snapshot age, open issues, VSS used or not, verify age, recovery key confirmed, service installed, and the encrypted-or-not-portable items list. Expose it to the server for the web (req 7/10).

### 3.10 Standalone restore tool

- `desktop/cmd/vaultdock-restore` is a single static Go exe with **no WebView2 dependency**. A fresh Windows 10 install may lack the WebView2 runtime [H]. Use a CLI/TUI with optional native Win32 dialogs.
- Inputs: a server URL with a login (temporary device enrol or a session-scoped read token), **or** a direct destination path (local disk, SMB, later S3) plus the recovery key or passphrase.
- It shares `internal/backup/format`, `internal/backup/crypto` and `internal/restore` with the agent. It is published as a separate release asset (§4).

### 3.11 Scheduler, offline queue and resource limits

- **Scheduler** (`internal/scheduler`):
  - Schedule types: daily or weekly at a time, every N hours, at logon, on idle (`GetLastInputInfo` works only in a user session, so the UI process reports idle over IPC), and on AC power.
  - Missed-run catch-up after boot, with jitter.
  - Persisted in `backup.db.schedules` and run by the service, so it works without the UI.
  - Sync configs gain an optional schedule window (only between X and Y) enforced through the dispatch gate (`transfer.go:60-65` builds the `exclude` map).
- **Offline**: chunking and packing continue offline until the spool cap is reached, then they pause. Packs persist, so uploads resume after network return. Add `NotifyAddrChange`/`NotifyIpInterfaceChange` [M] to wake the dispatcher instead of the fixed `offlineUntil` back-off (`transfer.go:180-185`).
- **Resource governor** (`internal/governor`):
  - `SetPriorityClass(PROCESS_MODE_BACKGROUND_BEGIN)` lowers CPU, I/O and memory priority. It is process-wide and cannot be applied per goroutine, because Go migrates goroutines across threads [H]. Use it while only scheduled background work runs. Never use it while interactive restores run.
  - Configurable hash and chunk worker count, replacing the fixed `hashSem` of 2 (`engine.go:111`).
  - Battery: `GetSystemPowerStatus` (`ACLineStatus`, battery-saver flag), with policy pause-on-battery and resume-below-X%.
  - Metered networks: `INetworkCostManager` [M], v2.
  - The existing bandwidth limiter is shared, and a schedule-aware bandwidth limit is added.

### 3.12 Error handling: skip, skip-all, Retry Skipped (req 4)

- **`issues` table** (sync and backup, each in its own DB, same shape): `id, scope(job/config), run_id, path, class(locked|access_denied|not_found|path_too_long|io|network|server|crypto|cloud_placeholder), detail, first_seen, last_seen, count, resolution(open|skipped|skipped_all|retried|resolved)`.
- **Interactive run**: the first issue of a class raises a non-blocking prompt with Skip, Skip all of this class for this run, Retry now or Abort job. The run continues with other files while it waits.
- **Unattended run**: auto-skip and record. Nothing is discarded silently.
- **Completion summary per run**: counts by class, top paths, bytes new and dedup, duration, and an actionable hint per class (for example "Locked: install the background service to enable VSS").
- **Retry Skipped**: create a mini-run of the skipped items for one job or all jobs.
- **Fixes to existing silent paths**:
  - `scan.go:112-116,134-137`: record an issue instead of skipping silently.
  - `transfer.go:279-283`: count deferrals and convert to an issue after a threshold or N minutes.
  - `report.go:142-178`: in-memory reports are lost on restart, so move them to a durable outbox table.

### 3.13 Sync additions (req 5)

- **Dedicated sync folders and arbitrary watched folders**: already supported as `Kind=folder`. Add presets (rule packs) and a "VaultDock folder" quick-create in the profile.
- **Custom rulesets and exclusions**: filter v2 with named, reusable rulesets referenced by sync configs and backup jobs, `.gitignore` import, and cloud-placeholder skip (§1.7).
- **Schedules**: §3.11.
- **Conflict handling**: today `on_conflict` is decided only by "destination exists and differs". To detect *remote edits since our last upload*, send `base_sha256` (the last uploaded hash from `files.sha256`, `state.go:128-138`) in `UploadRequest`. The server returns `conflict` when the current hash is different. The client then applies the policy: keep both (rename with device and timestamp), local wins, or skip and raise an issue. This needs a server and pkg change (§7).
- **Version history**: the server currently moves replaced files to trash (`KeepReplaced`). Proper versions are a server feature. The desktop needs only a "versions" view through the web (`OpenWebInterface`) or bindings once the server exposes `GET /files/{id}/versions`.
- **Two-way sync is not proposed** for this initiative. The engine contract (`engine.go:1-5`) and the absence of any download or local-write path make it a separate project (download, local writes, deletion propagation, tombstones, 3-way merge). Requirement 5's "dedicated sync folders" should be confirmed with the owner (§5).

---

## 4. Deployment and CI changes

1. **Installer**:
   - Keep the per-user installer (`project.nsi`, unchanged behaviour) as "Basic".
   - Add a **per-machine installer** (`REQUEST_EXECUTION_LEVEL admin`, `InstallDir $PROGRAMFILES64\VaultDock`, HKLM uninstall key) that:
     - installs `VaultDockAgent.exe` and registers the service with the SCM (`sc.exe create` or the agent's own `--install-service` using `x/sys/windows/svc/mgr`), start type automatic (delayed), recovery actions restart.
     - creates `%ProgramData%\VaultDock` with a restrictive ACL.
     - registers the UI autostart in HKLM Run, or per user on first launch.
     - registers the toast AppUserModelID shortcut.
   - The uninstaller stops and deletes the service, removes orphan shadow copies the agent created (agent `--cleanup`), and asks before deleting data (as `project.nsi:114-119` does).
   - One installer with a mode page is possible through NSIS `MultiUser.nsh`, but the Wails-generated `wails_tools.nsh` fixes the scope at compile time (`wails_tools.nsh` `REQUEST_EXECUTION_LEVEL`). Two outputs are simpler [M].
2. **Updater**:
   - Per-machine installs cannot be updated silently from the unelevated UI. In service mode, **the agent runs the updater** as SYSTEM: download, SHA-256 check, **Authenticode signer check** (`WinVerifyTrust` plus a publisher pin, new), run the installer `/S`, restart the service, signal the UI to relaunch.
   - Extend `InstallerPattern` (`updater.go:28`) to choose the `-machine-setup.exe` or `-user-setup.exe` asset by install mode.
   - Keep `/RELAUNCH` semantics.
3. **Signing**: none today (`project.nsi:45-47`). Add Authenticode signing of `VaultDock.exe`, `VaultDockAgent.exe`, `vaultdock-restore.exe`, the installers and the uninstaller. Options are signtool with an EV/OV certificate in GitHub secrets, or a cloud signing service. Signing is a **prerequisite** for a service that reads all files and manages VSS (AV reputation, SmartScreen).
4. **CI matrix** (`ci.yml`):
   - desktop: `go test ./...` on windows-latest (exists).
   - Add `GOOS=linux go vet ./...` for the desktop module so the `_other.go` stubs keep compiling. The `platform_other.go` comment says CI relies on this, but nothing runs it.
   - Add an **admin-required test job** (`-tags=integration_admin`) for VSS, service install and uninstall, and SeBackupPrivilege. GitHub windows runners run as admin [H], but VSS availability on hosted runners is [M]. Fall back to a self-hosted Hyper-V runner.
   - Build the NSIS installers in CI (not only on release) to catch script breakage.
   - Add frontend type-check (exists through `tsc -b`) and Vitest for new screens.
   - Add a `wails generate module` diff check so committed bindings match Go.
5. **Release artifacts** (`release.yml:222-238`): build and collect `VaultDock-<v>-amd64-user-setup.exe`, `VaultDock-<v>-amd64-machine-setup.exe` and `vaultdock-restore-<v>-amd64.exe` (plain `go build` with `-ldflags -X main.version`), plus optional arm64 later. `SHA256SUMS` already covers `assets/*`. Pin the release-asset naming contract in a shared constant used by the updater and the web download page.
6. `install.sh` needs no changes, unless the server agent adds backup-repository storage settings to `.env`.

---

## 5. Limitations and risks

| # | Risk or limitation | Evidence or confidence | Mitigation |
|---|---|---|---|
| 1 | **Per-user, unelevated install**: no VSS, HKLM writes, other profiles or backups while logged off | `project.nsi:5-9` [V] | Service mode (§3.1). Per-user mode stays and shows reduced readiness |
| 2 | **Locked files** (Outlook PST, browser DBs, `NTUSER.DAT`) cannot be read without VSS. Today they loop forever | `transfer.go:279-283` [V], Go share mode [V] | VSS in service mode, bounded deferral, issue records |
| 3 | **One-way assumptions baked in**: no download path, no remote-change detection, deletions never propagate, the index is keyed per config | `engine.go:1-5`, `pkg/client` has no download [V] | Keep sync one-way. Add `base_sha256` conflicts. Two-way is out of scope |
| 4 | DPAPI-bound secrets (device key, browser passwords) are lost on reinstall | `platform_windows.go:27-45` [V], DPAPI semantics [H] | Recovery key or passphrase for MK. "Not portable" flags |
| 5 | The WebView2 runtime may be missing on a fresh Windows 10 | `wails_tools.nsh` bootstrapper [V], OS behaviour [H] | Restore tool without WebView2. The installer already bootstraps it, but that needs network |
| 6 | Go-only Windows API access: `IVssBackupComponents` and WinRT `PackageManager` need raw COM vtables | [M] | WMI VSS first. Registry fallback for MSIX. Spike before committing |
| 7 | winget under SYSTEM is unreliable, and package agreements need consent | [M] | Run winget in the user process over IPC. Explicit consent UI |
| 8 | OneDrive placeholders get hydrated by scanning or reading | `fileinfo_windows.go:11-16` [V], behaviour [H] | Skip the RECALL_ON_DATA_ACCESS and OFFLINE attributes by default and record an issue class |
| 9 | `state.db` lives in **Roaming** AppData. In domain roaming-profile environments a multi-hundred-MB DB roams | `platform.go:13-20` [V] | Move new DBs to Local or ProgramData. Migrate `state.db` to Local with a one-time move |
| 10 | No schema migrations in `state.go`. Columns cannot be added safely | `state.go:24-49,67` [V] | Add a `schema_version` plus ordered migrations (WS-H) |
| 11 | No tests for `internal/state`. The updater installs unsigned binaries | brief, `updater.go` [V] | Tests in WS-H. Authenticode check in WS-J |
| 12 | `ReportState` sends local paths to the server every 15 s | `report.go:91-140` [V] | Fine for self-hosted. Document it. Backup reports send only job names and hashes unless the user opts in |
| 13 | Reports and notifications in memory are lost on restart | `report.go:142-178` [V] | Durable outbox |
| 14 | Client-side encryption removes server-side cross-user dedup and server-side preview | design [H] | Documented trade-off. Previews only in the desktop or restore tool, or with opt-in unencrypted metadata |
| 15 | AV/EDR heuristics: a service that reads everything and deletes shadow copies | [H] | Signing, own-ID-only deletion, documented exclusions |
| 16 | Two controllers when sync runs in the UI and backup in the service would overcommit bandwidth | design [V] | Host both engines in the agent (§3.1) |
| 17 | Process-wide background priority also slows IPC responsiveness | [H] | Leave background mode during interactive operations |
| 18 | Multi-user PCs: ambiguous mapping of one device to one VaultDock account | design | v1 covers the device-owner account plus opt-in profiles. Flag for product decision |
| 19 | MachineGuid changes on reinstall, so "same device" cannot be detected automatically | `platform.go:33-41` [V] | User-chosen source device in the restore wizard |
| 20 | 3× file read per upload (full hash, chunk hash, send) | `transfer.go:294,431,441` [V] | The backup path hashes during chunking in one pass. Sync can keep its current behaviour |

---

## 6. Implementation workstreams (exclusive file ownership)

Rules:
- Each path is owned by exactly one workstream.
- New packages are wholly owned.
- `desktop/go.mod` and `go.sum` are owned by **WS-A**. Other streams list their dependencies in a short request and WS-A lands them up front in one PR, for example `go-winio`, `klauspost/compress/zstd`, a FastCDC implementation or own code, `x/crypto` direct, and `go-ole` direct.
- Generated `frontend/wailsjs/**` is owned by **WS-I** only.

**Order**: Phase 0: WS-A skeleton, WS-B extraction, WS-H migrations. Phase 1: WS-C, WS-D, WS-E, WS-F in parallel. Phase 2: WS-G, WS-I. WS-J runs alongside.

### WS-A: Agent host, IPC, service, platform modes
- Modify: `desktop/main.go` (wire `agent.Host` in-process or `agent.Client`; tray uses the API), `desktop/go.mod`, `desktop/go.sum`, `internal/platform/platform.go`, `platform_windows.go`, `platform_other.go` (mode-aware DataDir/StateDir, machine-scope DPAPI option, `IsService()`).
- New: `desktop/cmd/vaultdock-agent/main.go`, `internal/agent/{api.go,host.go,events.go,inproc.go}`, `internal/ipc/{protocol.go,server.go,client.go,pipe_windows.go,pipe_other.go,auth_windows.go}`, `internal/service/{service_windows.go,manage_windows.go,service_other.go}`.
- Provides `agent.API` (the interface all bindings call) and an event bus.

### WS-B: Transfer core extraction (shared by sync and backup)
- Modify: `internal/engine/engine.go` (remove `ctrl`, limiter and `wrapBody`, and inject `*transfer.Core`; owns all `engine.go` changes, including hooks requested by WS-H: `SetDispatchGate`), `internal/engine/transfer.go` (use `transfer.Uploader` and classification; bounded deferral fix), `internal/engine/concurrency.go` and `concurrency_test.go` (moved into `internal/transfer`, leaving thin aliases or deleting them), `internal/engine/report.go` (durable outbox, backup run reports), `internal/engine/engine_test.go`.
- New: `internal/transfer/{core.go,controller.go,uploader.go,classify.go,limiter.go,scheduler.go,*_test.go}`, `internal/outbox/{outbox.go,outbox_test.go}`.

### WS-H: Sync enhancements and state migrations
- Modify: `internal/state/state.go` (migrations, `issues`, conflict base hash use, schedule fields), `internal/config/config.go` (SyncConfig schedule, ruleset refs, conflict policy `keep_both|local_wins|skip`), `internal/filter/filter.go` and `filter_test.go` (filter v2: rulesets, `.gitignore`, placeholder skip), `internal/engine/scan.go` (issues instead of silent skips), `internal/engine/watch.go`, `internal/engine/automations.go`, `internal/engine/fileinfo_windows.go`, `fileinfo_other.go` (cloud attributes).
- New: `internal/state/migrate.go`, `internal/state/state_test.go`, `internal/rules/{ruleset.go,presets/*.json,gitignore.go,*_test.go}`, `internal/dbutil/migrate.go` (shared migration helper also used by WS-C).

### WS-C: Backup engine
- New: `internal/backup/{job.go,runner.go,walker.go,manifest.go,chunker.go,pack.go,spool.go,index.go,store.go,issues.go,report.go,*_test.go}`, `internal/scheduler/{scheduler.go,schedule.go,*_test.go}`.
- Consumes: `transfer.Core` (WS-B), `rules` (WS-H), `vss` (WS-F), `sources` (WS-E), `backup/crypto` (WS-D).

### WS-D: Keys and crypto format
- New: `internal/backup/crypto/{keys.go,wrap.go,recovery.go,passphrase.go,aead.go,*_test.go}`, `internal/backup/format/{spec.go,pack_format.go,manifest_format.go,*_test.go}` plus a written format spec `planning/specs/backup-format.md`, owned here and created during implementation, not now.

### WS-E: Windows sources and discovery
- New: `internal/sources/{provider.go,knownfolders_windows.go,apps_registry_windows.go,winget.go,choco.go,scoop.go,msix_windows.go,steam.go,vdf.go,epic.go,gog_windows.go,gamesaves.go,devprojects.go,registry_export_windows.go,*_other.go,*_test.go}`, `internal/sources/catalog/*.json` (app settings catalogue, game-save manifest adapter).

### WS-F: VSS, privileged I/O, governor
- New: `internal/vss/{vss.go,wmi_windows.go,requestor_windows.go(v2),vss_other.go,sweep.go}`, `internal/privio/{open_windows.go,backupread_windows.go,privilege_windows.go,open_other.go}`, `internal/governor/{governor.go,power_windows.go,priority_windows.go,governor_other.go}`.

### WS-G: Restore engine and standalone tool
- New: `internal/restore/{plan.go,pathmap.go,sid_windows.go,writer.go,conflicts.go,apps_reinstall.go,registry_import_windows.go,verify.go,*_test.go}`, `desktop/cmd/vaultdock-restore/{main.go,tui.go}`.
- Consumes the WS-K `pkg/client` additions (via the pkg agent) and the WS-D format.

### WS-I: UI bindings and frontend
- Modify: `desktop/app.go`, split into:
  - `app.go`: lifecycle, bootstrap, connection (current lines 1-427)
  - **new** `app_sync.go`: sync bindings moved from `app.go:429-526`
  - `app_settings.go`: `app.go:528-603`
  - **new** `app_backup.go`, `app_restore.go`, `app_wizard.go`
  - Each delegates to `agent.API`.
- Modify `frontend/src/App.tsx` (navigation: Backups, Restore, Issues, Readiness, plus a wizard gate), `frontend/src/screens/{Overview,Transfers,Settings,Connect,Configs}.tsx`, `frontend/src/ui.tsx`, `frontend/src/lib.ts`, `frontend/package.json` (Vitest), `frontend/wailsjs/**` (regenerated).
- New: `frontend/src/screens/{Backups.tsx,BackupJobEditor.tsx,Restore.tsx,RestoreMapping.tsx,Issues.tsx,Readiness.tsx,Wizard/*.tsx}`, `frontend/src/api/agent.ts` (typed wrappers around bindings).
- To reduce the `Configs.tsx` hotspot, split its editor into `screens/configs/{Editor.tsx,Preview.tsx,Rules.tsx}` under WS-I.

### WS-J: Deployment, updater, notifications, CI
- Modify: `build/windows/installer/project.nsi`, `wails.json`, `internal/updater/updater.go`, `updater_test.go`, `launch_windows.go`, `launch_other.go`, `desktop/app_update.go`, `desktop/notify.go`, `desktop/logging.go` (log dir by mode), `.github/workflows/ci.yml`, `.github/workflows/release.yml`.
- New: `build/windows/installer/machine.nsi`, `service.nsh`, `internal/updater/authenticode_windows.go`, `scripts/sign.ps1`, `scripts/vm/` (Packer and Hyper-V restore-test harness).
- `install.sh` and `scripts/dev-server.ps1`: no change expected, owned here if touched.

### Ownership conflict notes
- **`engine.go`** belongs to WS-B only. WS-H requests hooks such as `SetDispatchGate(func(cfgID string) bool)` and `Engine.Issues` through WS-B, landed in Phase 0.
- **`state.go`** belongs to WS-H only. Backup uses its own `backup.db`, so WS-C never touches it.
- **`app.go`** belongs to WS-I only. WS-A changes only `main.go`, and all agent wiring flows through `agent.API` injected into `NewApp`. **Signature change**: `NewApp(log, api agent.API)` is agreed in Phase 0.
- **`main.go`** belongs to WS-A only, including the tray.

---

## 7. Interfaces

### 7.1 Needed from the server and `pkg/*` (other agents)
1. **`pkg/client`, device-signed read API**: `ListFiles(path)`, `Resolve`, `Download(id, w, range)` against the existing `/files` and `/files/{id}/content`, which already allow device auth (`internal/api/server.go:543-558`).
2. **Backup repository API, device-signed:**
   - `POST /backup/repos` (create or get, store **wrapped key blobs** only)
   - `POST /backup/packs/has` (batch existence by pack ID)
   - Pack upload through the existing upload sessions with `namespace=backup` and `kind=pack`, kept out of `nodes`, plus a persisted `upload_id` resume (`GET /uploads/{id}` exists)
   - `GET /backup/packs/{id}` with HTTP Range
   - `POST /backup/snapshots` (commit: job, run, manifest object ID, referenced pack IDs, stats)
   - `GET /backup/snapshots?job=&device=`
   - `POST /backup/snapshots/{id}/protect` (lock against pruning)
   - `PUT /device/inventory` (opt-in plaintext metadata)
   - `PUT /device/backup-state` (job and readiness summary, extending `ReportState` at `report.go:122-133`)
3. **Server GC contract**: GC uses the server-side snapshot-to-pack reference lists and never needs plaintext. Prune happens server-side by retention policy, with protected snapshots exempt.
4. **Uploads**: `UploadRequest.BaseSHA256` and a `conflict` result for sync conflict detection. A file-versions API (`GET /files/{id}/versions`, restore version).
5. **`pkg/automation`**:
   - Events: `backup.completed`, `backup.failed`, `backup.issue`, `restore.completed`, `verify.failed`
   - Actions: `backup.start(job)`, `verify.start(job)`
   - Trigger: `schedule.cron`
   - Approval-gate metadata for destructive actions (req 8)
   - The desktop evaluates these exactly as `automations.go` does today.
6. **Release naming contract** in `pkg/release` (user, machine and restore assets) for the updater and the web download page.
7. **Storage destinations (req 6)**: for direct-to-destination restore, the format must be readable from plain object or file layouts (`packs/<id>`, `snapshots/<id>`, `keys/<id>`). The storage agent should agree that the server writes backup objects in this self-describing layout on each destination.

### 7.2 Provided by the desktop
- Backup format spec (packs, manifests, key-wrap files, versioned) from WS-D.
- Job and run report schema, issue classes, and readiness report schema for web dashboards (req 7, 10).
- Inventory schema (apps with source and IDs, games, dev repos).
- The restore tool as a release artifact. Agent IPC is local only and not exposed.

---

## 8. Testing plan

**Unit tests (Go, run on every OS where feasible):**
- `state` is currently untested: queue claim, retry, defer, fail, crash recovery, migrations up from the v0 schema.
- `transfer` controller (existing tests moved), classification table and the uploader against `internal/testserver`.
- `backup`:
  - CDC determinism and boundary-shift resilience (insert 1 byte, then ≥90 % chunk reuse)
  - zstd round-trip
  - pack sealing and spool cap back-pressure
  - manifest encode and decode golden files
  - dedup index correctness and crash mid-pack
- `crypto`: wrap and unwrap with DPAPI, recovery key and passphrase. Tamper detection. Known-answer vectors pinned in `format`.
- `rules` and `filter`: presets, `.gitignore` semantics, placeholder attributes.
- `restore/pathmap`: table tests mapping `C:\Users\alice` to `D:\Users\bob`, OneDrive-redirected Documents, SID rewrite in registry values.
- `scheduler` with a fake clock: missed runs, DST, battery gating through a fake governor.
- `sources`: parsers on fixture files (VDF, Epic `.item`, choco nuspec, scoop manifests, winget export JSON, registry snapshots through an interface fake).

**Integration (Windows CI, `internal/testserver` like `engine_test.go:46-63`):**
- Backup end-to-end: run, kill the agent mid-pack upload, restart, check that only missing chunks are sent and the snapshot commits.
- Offline: drop the server, fill the spool to its cap, restore the network, drain.
- Issues: lock a file with `CreateFile(share=0)`, then check the skip record, Retry Skipped after unlock and the summary counts.
- Sync conflict with `base_sha256` once the server supports it.
- IPC: the pipe ACL denies a non-admin's machine-wide operation, and a user may control only their own profile jobs.

**Admin-only (tagged, self-hosted or hosted runner):** VSS create, read and delete; orphan sweep; service install, start, stop and uninstall; SeBackupPrivilege read of an ACL-denied file; updater Authenticode rejection of an unsigned or wrong-publisher installer.

**Windows VM restore tests (Hyper-V and Packer, `scripts/vm/`, nightly and before release):**

| Scenario | Source VM | Target VM | Assertions |
|---|---|---|---|
| Same-user reinstall | Win11 24H2, user `alice`, apps through winget, choco and scoop, Steam fixture library, dev repos, Office-like locked PST | Fresh Win11 with user `alice` (new SID) | Files restored with matching hashes. Apps reinstalled from the plan. Registry keys present. `icacls` shows inherited ACLs only and **no foreign SIDs** |
| Username change | as above | Fresh Win10 22H2 with user `bob` and Documents redirected | Path remap. Registry path rewrite. No `C:\Users\alice` created |
| Server-independent | as above | Fresh VM with no network to the server | `vaultdock-restore.exe` restores from a local destination copy with the recovery key only. Fails cleanly with a wrong key |
| No WebView2 | Win10 without the WebView2 runtime | — | The restore tool runs. The installer bootstraps WebView2 |
| Per-user mode | Basic installer, no admin | — | Sync works as before. Readiness flags no VSS and locked files are recorded as issues |
| Upgrade | vPrev per-user plus existing `state.db` | vNext | Migrations apply. Configs, queue and device key are kept (as `project.nsi:54-56` promises) |

**Regression guard**: the existing `TestEngineEndToEnd` and `TestEngineRecoversInterruptedUpload` (`engine_test.go:65,154`), the updater tests and the filter tests must pass unchanged after the WS-B extraction. That is the acceptance gate for "preserve all current functionality".
