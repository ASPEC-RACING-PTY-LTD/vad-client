# 06 - OSS Research: Windows Application Inventory, Settings & Recovery

Scope: Windows app discovery and inventory, package-manager reinstall, AppData/registry
backup, game saves, dev project discovery, VSS from Go, profile/username/SID migration,
ACLs, EFS/DPAPI, filesystem change monitoring (ReadDirectoryChangesW, USN journal), and
privileged service architecture. Backup formats, chunking, crypto, storage adapters and
sync are covered by other agents.

Research date: 2026-10-17. Read-only research, no external code was copied. Licences
were checked from each repository's `LICENSE` file (raw.githubusercontent.com) or the
GitHub repository API. "Activity" is the most recent commit or push timestamp, read from
the repository's commits Atom feed (`https://github.com/<repo>/commits/<branch>.atom`)
or `pushed_at` from `https://api.github.com/repos/<repo>` on the research date.

---

## 1. Summary table

| Project | Lang | Licence (SPDX) | MIT? | Activity (verified) | Relevance | Recommended use |
|---|---|---|---|---|---|---|
| [microsoft/winget-cli](https://github.com/microsoft/winget-cli) | C++ | MIT | Y | pushed 2026-10-14 | Reinstall engine, `export`/`import` JSON, COM API | **Runtime dependency** (call the OS-installed winget, don't vendor it) |
| [microsoft/winget-pkgs](https://github.com/microsoft/winget-pkgs) | YAML | MIT | Y | pushed 2026-10-17 | Package ID catalogue, ProductCode/PFN mapping | **Data source** (via winget itself) |
| [microsoft/winget-create](https://github.com/microsoft/winget-create) | C# | MIT | Y | commit 2026-09-09 | Manifest authoring | Reference only |
| [Devolutions/UniGetUI](https://github.com/Devolutions/UniGetUI) (formerly marticliment/WingetUI) | C# | MIT | Y | commit 2026-10-16 | Multi-manager UI, bundle export, ID matching UX | **Inspiration** (UX for multi-source bundles) |
| [chocolatey/choco](https://github.com/chocolatey/choco) | C# | Apache-2.0 | N | pushed 2026-10-14 | Secondary reinstall source, `choco export` | **Runtime dependency** (optional, if installed) |
| [ScoopInstaller/Scoop](https://github.com/ScoopInstaller/Scoop) | PowerShell | Unlicense OR MIT | Y (dual) | pushed 2026-10-17 | User-scope portable apps, `scoop export/import` | **Runtime dependency** (optional, if installed) |
| [ChrisTitusTech/winutil](https://github.com/ChrisTitusTech/winutil) | PowerShell | MIT | Y | commit 2026-10-07 | Curated app list with winget/choco IDs, tweak catalogue | Inspiration only |
| [mtkennerly/ludusavi](https://github.com/mtkennerly/ludusavi) | Rust | MIT | Y | commit 2026-09-11 | Game save backup engine, store detection | **Inspiration** (or optional external CLI) |
| [mtkennerly/ludusavi-manifest](https://github.com/mtkennerly/ludusavi-manifest) | YAML/Rust | MIT (repo) + data derived from PCGamingWiki | see notes | commit 2026-10-17 | Save-location database (tens of thousands of games) | **Data source, with licence caveat** (see section 2) |
| [PCGamingWiki](https://www.pcgamingwiki.com/) | wiki | CC BY-NC-SA 3.0 ([Wikipedia](https://en.wikipedia.org/wiki/PCGamingWiki)) | N | live | Upstream of ludusavi-manifest | Data source; non-commercial/share-alike constraints |
| [MikeMaximus/gbm](https://github.com/MikeMaximus/gbm) (Game Backup Monitor) | VB.NET | GPL-3.0 | N | commit 2026-06-30 | Process-triggered save backup | Inspiration only |
| GameSave Manager ([gamesave-manager.com](https://www.gamesave-manager.com/)) | .NET | Freeware, no source found | N | DB updated 2025 ([HowToGeek](https://www.howtogeek.com/107323/how-to-back-up-restore-over-1000-pc-games-with-gamesave-manager/)) | Commercial-style reference | Avoid (closed) |
| [JosefNemec/Playnite](https://github.com/JosefNemec/Playnite) | C# | MIT (LICENSE.md) | Y | commit 2026-10-15 | Library detection for Steam/Epic/GOG/Xbox | Inspiration (store detection) |
| [Heroic-Games-Launcher](https://github.com/Heroic-Games-Launcher/HeroicGamesLauncher) | TS | GPL-3.0 (COPYING) | N | commit 2026-10-15 | Epic/GOG manifests | Inspiration only |
| [derrod/legendary](https://github.com/derrod/legendary) | Python | GPL-3.0 | N | not verified (feed failed) | Epic install manifests | Inspiration only |
| [andygrunwald/vdf](https://github.com/andygrunwald/vdf) | Go | MIT | Y | commit 2026-10-16 | Parse Steam `libraryfolders.vdf` / `.acf` (text VDF) | **Dependency candidate** |
| [restic/restic](https://github.com/restic/restic) `internal/fs/vss_windows.go` | Go | BSD-2-Clause | N | commit 2026-10-17 | Proven VSS-from-Go via raw COM vtables + go-ole | Inspiration only (internal pkg, not importable) |
| [mxk/go-vss](https://github.com/mxk/go-vss) | Go | MPL-2.0 | N | commit 2026-03-04 | VSS via WMI `Win32_ShadowCopy` | Dependency candidate (file-level copyleft OK if unmodified) |
| [go-ole/go-ole](https://github.com/go-ole/go-ole) | Go | MIT | Y | commit 2026-10-12 | COM from Go (needed for own VSS/IVssBackupComponents binding) | **Dependency** |
| [golang/sys](https://github.com/golang/sys) (`x/sys/windows`, `/registry`, `/svc`) | Go | BSD-3-Clause | N | commit 2026-10-15 | Registry, services, tokens, SDDL, DeviceIoControl | **Dependency** (de-facto stdlib) |
| [microsoft/go-winio](https://github.com/microsoft/go-winio) | Go | MIT | Y | commit 2026-10-14 | Named pipes with SDDL, backup-stream (`BackupRead`) helpers, privileges | **Dependency** (IPC + backup streams) |
| [kardianos/service](https://github.com/kardianos/service) | Go | Zlib | N | commit 2026-08-29 | Service install/run wrapper | Optional; `x/sys/windows/svc` is enough |
| [fsnotify/fsnotify](https://github.com/fsnotify/fsnotify) | Go | BSD-3-Clause | N | commit 2026-05-11 | ReadDirectoryChangesW wrapper | Dependency (already the common choice) |
| [rjeczalik/notify](https://github.com/rjeczalik/notify) | Go | MIT | Y | commit 2026-06-01 | Recursive watches (RDCW on Windows) | Alternative to fsnotify; lower activity |
| [gentlemanautomaton/volmgmt](https://github.com/gentlemanautomaton/volmgmt) | Go | MIT | Y | commit 2026-08-09 | USN journal read/enum, volume handles | Dependency candidate / reference |
| [chaugan/swinv](https://github.com/chaugan/swinv) `internal/usn` | Go | Apache-2.0 | N | commit 2026-08-30 | MFT enumeration via `FSCTL_ENUM_USN_DATA` | Inspiration only (internal) |
| [Velocidex/velociraptor](https://github.com/Velocidex/velociraptor) | Go | AGPL-3.0 | N | commit 2026-10-17 | Raw NTFS, USN, VSS, registry artefacts | **Avoid as dependency**; read docs for technique only |
| [Velocidex/go-ntfs](https://github.com/Velocidex/go-ntfs) | Go | Apache-2.0 | N | commit 2026-10-09 | Raw NTFS/MFT/USN parsing (bypasses locks) | Reference; last-resort locked-file reader |
| [microsoft/wmi](https://github.com/microsoft/wmi) | Go | MIT | Y | commit 2026-06-03 | WMI from Go (Win32_ShadowCopy, Win32_Product avoid) | Optional |
| [yusufpapurcu/wmi](https://github.com/yusufpapurcu/wmi) | Go | MIT | Y | commit 2024-01-28 | WMI queries | Avoid (stale) |
| [shirou/gopsutil](https://github.com/shirou/gopsutil) | Go | BSD-3-Clause | N | commit 2026-10-17 | Process/disk info for "app running?" checks | Optional |
| [go-git/go-git](https://github.com/go-git/go-git) | Go | Apache-2.0 | N | commit 2026-10-16 | Read repo state (remotes, dirty, unpushed) without git.exe | Optional (prefer shelling to `git` if present) |
| [wailsapp/wails](https://github.com/wailsapp/wails) | Go | MIT | Y | commit 2026-10-17 | Existing client framework | Already used |
| Microsoft USMT ([docs](https://learn.microsoft.com/en-us/windows/deployment/usmt/usmt-what-does-usmt-migrate)) | native | Proprietary (part of Windows ADK) | N | docs updated 2025-06 | Gold-standard reference for profile migration semantics | Reference only (cannot redistribute) |
| ForensiT Transwiz / User Profile Wizard | native | Proprietary | N | n/a | Profile re-association to new SID | Reference only |
| Laplink PCmover | native | Commercial | N | n/a | App "transplant" (moves installed apps) | Reference only; we won't do binary app transplant |
| Windows Backup app ([support](https://support.microsoft.com/help/17127)) | OS | Proprietary | N | current | Store-app inventory + settings via MSA/OneDrive | Reference for UX tiers |

Notes:
- Chocolatey's GitHub API reports `NOASSERTION`. The LICENSE file is Apache License 2.0
  ([LICENSE](https://raw.githubusercontent.com/chocolatey/choco/develop/LICENSE)).
- Scoop's LICENSE starts `SPDX-License-Identifier: UNLICENSE or MIT`
  ([LICENSE](https://raw.githubusercontent.com/ScoopInstaller/Scoop/master/LICENSE)).
- UniGetUI's API record now resolves to `Devolutions/UniGetUI`. The MIT LICENSE says
  "Copyright (c) 2025 Martí Climent" ([LICENSE](https://raw.githubusercontent.com/Devolutions/UniGetUI/main/LICENSE)).
- `jeffallen/vss` returned 404 on the research date, so it is excluded.
- WebSearch found a forked `zoid42/volmgmt` (last commit 2021-08-06). Use the upstream
  `gentlemanautomaton/volmgmt` instead.

---

## 2. Licence grouping

### MIT candidates (safe to depend on, borrow ideas from or ship data from, with attribution)
- **winget-cli / winget-pkgs / winget-create**: MIT. VaultDock should **invoke** the
  OS-provided `winget.exe` (or its COM API) rather than bundle it. The export/import JSON
  schema is published in-repo (`schemas/JSON/packages`), so VaultDock can emit
  winget-compatible import files directly.
- **UniGetUI**: MIT. A useful reference for a multi-manager "bundle" (winget + choco +
  scoop + pip/npm), and for UX that shows the match source and confidence. Inspiration only:
  it is C#, so no code is reused.
- **Scoop**: dual Unlicense/MIT. Fine to invoke `scoop export`/`scoop import`.
- **winutil**: MIT. Its curated app list maps friendly names to winget and choco IDs. Treat
  it as an idea source for a VaultDock "known apps" table. If we ship derived data, keep
  the attribution.
- **ludusavi / ludusavi-manifest**: the code repositories are MIT. **Caveat:** the primary
  manifest data is compiled from PCGamingWiki
  ([README](https://raw.githubusercontent.com/mtkennerly/ludusavi-manifest/master/README.md)),
  whose content is reported as **CC BY-NC-SA 3.0**
  ([Wikipedia](https://en.wikipedia.org/wiki/PCGamingWiki), [Valve dev wiki](https://developer.valvesoftware.com/wiki/Template:Sysreq/doc)).
  If VaultDock is or may become commercial, **do not bundle** the manifest inside the
  binary. Instead, download it at runtime from the upstream URL as user-fetched data (as
  ludusavi itself does) and show attribution. Get a legal opinion before redistributing it.
  The YAML *schema* (`schema.yaml`) is a format, and implementing a reader for it is fine.
- **andygrunwald/vdf, go-ole, go-winio, microsoft/wmi, rjeczalik/notify, gentlemanautomaton/volmgmt,
  Playnite (inspiration), wails**: all MIT.

### Other licences, and what they mean
- **BSD-2/BSD-3 (restic, x/sys, fsnotify, gopsutil)**: permissive. Dependencies are fine
  with notice retention. The restic VSS code is in an `internal/` package, so it cannot be
  imported. Copying it would also mean carrying its notice, and the project rule forbids
  copying anyway. Use it only as an architecture reference: COM init, then
  `IVssBackupComponents`, then `SetContext(VSS_CTX_BACKUP)`, then `StartSnapshotSet`,
  `AddToSnapshotSet` and `PrepareForBackup`/`DoSnapshotSet`, then read through
  `\\?\GLOBALROOT\Device\HarddiskVolumeShadowCopyN\`. It imports `github.com/go-ole/go-ole`
  and `golang.org/x/sys/windows` (verified in
  [vss_windows.go](https://github.com/restic/restic/blob/master/internal/fs/vss_windows.go)).
- **Apache-2.0 (Chocolatey, go-ntfs, go-git, swinv)**: permissive with patent grant and
  NOTICE requirements. Fine as dependencies or invoked tools.
- **MPL-2.0 (mxk/go-vss)**: file-level copyleft. Using it **unmodified** as a Go module
  dependency is fine. Any modifications to its files must be published. Its v1 API is based
  on WMI `Win32_ShadowCopy` ([README](https://github.com/mxk/go-vss)). Activity is moderate
  (last commit 2026-03-04).
- **Zlib (kardianos/service)**: permissive. It is not needed because `x/sys/windows/svc`
  covers it.
- **GPL-3.0 (gbm, Heroic, legendary)** and **AGPL-3.0 (Velociraptor)**: **do not link or
  copy**. Reading their documentation or behaviour for ideas is fine. AGPL in particular
  would affect the network-served VaultDock server.
- **Proprietary (USMT, Transwiz, PCmover, Windows Backup, GameSave Manager)**: behavioural
  references only. USMT's documented *non-goals* are the most useful honest-limitations
  input (section 3.9).

---

## 3. Technical findings

### 3.1 Where installed applications are listed

| Source | Location / API | Scope | Notes |
|---|---|---|---|
| ARP (Add/Remove Programs), 64-bit | `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*` | machine | Main list. Values: `DisplayName`, `DisplayVersion`, `Publisher`, `InstallLocation`, `InstallDate`, `UninstallString`, `QuietUninstallString`, `EstimatedSize`, `WindowsInstaller`, `SystemComponent`, `ParentKeyName`, `ReleaseType`. |
| ARP, 32-bit on 64-bit OS | `HKLM\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*` | machine | Read with `registry.WOW64_32KEY` (x/sys/windows/registry) or the explicit path. Don't double-count the same app. |
| ARP, per-user | `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\*` | user | Per-user installers (VS Code User, Discord, Spotify, Zoom, many Electron/Squirrel apps). Other users' entries are only readable by loading their `NTUSER.DAT` (service + `RegLoadKey`, needs SeBackup/SeRestore). |
| MSI database | `MsiEnumProductsEx` / key name is the ProductCode GUID when `WindowsInstaller=1` | both | ProductCode and UpgradeCode are the strongest join keys to winget manifests. **Never use WMI `Win32_Product`**: it triggers MSI consistency checks/repairs. |
| MSIX/AppX (Store and sideloaded) | WinRT `Windows.Management.Deployment.PackageManager.FindPackagesForUser("")`, or PowerShell `Get-AppxPackage` | user (per-SID registration) | Identity is the **PackageFamilyName (PFN)**. Filter `IsFramework`, `IsResourcePackage`, `SignatureKind=System` (inbox apps). From Go, the simplest robust route is to shell out to PowerShell `Get-AppxPackage | ConvertTo-Json`, or use WinRT via go-ole (more work). |
| Store product IDs | winget `msstore` source (IDs like `9NBLGGH4NNS1`) | user | `winget list` shows store apps matched to the msstore source. |
| winget's own view | `winget list` / `winget export` | both | Correlates ARP, MSI and MSIX with source manifests. Unmatched entries show pseudo-IDs such as `ARP\Machine\X64\{GUID}` or `MSIX\<PFN>`, which are **not installable**. |
| Chocolatey | `choco list` (v2: local only), `C:\ProgramData\chocolatey\lib\<id>\` | machine | `choco export` writes a `packages.config`. |
| Scoop | `scoop list`, `scoop export` (JSON with buckets + apps), `~\scoop\apps\<app>` | user | Portable apps. `persist` dirs hold their data. |
| Portable / unmanaged | Start Menu `.lnk` targets, `%LOCALAPPDATA%\Programs`, `C:\Tools` | both | Heuristic only. Record as "data-only". |
| Game launchers | Steam/Epic/GOG/EA/Ubisoft/Xbox manifests (3.7) | user/machine | Reinstalled through the launcher, not winget. |
| Drivers | `pnputil /enum-drivers` | machine | Out of scope for reinstall. Inventory only (drivers come from Windows Update/OEM). |

Filtering rules (empirical, the same rules UniGetUI and winget use): drop
`SystemComponent=1`, entries with `ParentKeyName` (updates/patches), `ReleaseType` in
{Hotfix, Security Update, Update Rollup}, and entries with no `DisplayName`. Normalise
`DisplayName` by stripping version and arch suffixes, so it can be used for fuzzy matching.

### 3.2 Mapping an installed app to a winget / choco ID

Recommended order, from highest to lowest confidence:
1. **Ask winget.** Run `winget export -o x.json --include-versions --accept-source-agreements`
   and `winget list` (or use the COM API `Microsoft.Management.Deployment` with
   `FindPackagesAsync` plus a `PackageMatchFilter`, see the
   [COM API overview](https://www.mintlify.com/microsoft/winget-cli/com-api/overview)).
   winget matches on ARP ProductCode, UpgradeCode, PFN and normalised name/publisher. The
   [export doc](https://learn.microsoft.com/en-us/windows/package-manager/winget/export)
   says it warns when an app can't be matched to a source. Matching depends on manifest
   metadata plus ARP data.
2. **PFN to msstore** for MSIX apps.
3. **Chocolatey**: `choco export` for anything choco-managed. For non-choco apps, choco
   has no reliable reverse mapping. Use only a name search and require user confirmation.
4. **Scoop**: `scoop export`. Exact match.
5. **VaultDock fuzzy matcher**: name and publisher similarity against `winget search`
   results. **Always shown as "suggested", and needs user confirmation.**
6. **Unmatched**: record vendor `URLInfoAbout`/`HelpLink` from ARP and offer "open
   download page" at restore.

Store the match **with its evidence**: `{source:"winget", id:"Git.Git", matchedBy:"ProductCode", confidence:1.0}`.
That lets the restore UI explain itself and lets users correct bad matches. The
correction should persist across future backups.

### 3.3 Reinstall automation and silent-install limits
- `winget import` installs serially. It supports `--ignore-unavailable`,
  `--ignore-versions`, `--no-upgrade`, `--accept-package-agreements` and
  `--accept-source-agreements`
  ([import doc](https://learn.microsoft.com/en-us/windows/package-manager/winget/import)).
  VaultDock should **not** call `import` as one big step. Run `winget install --id X -e
  --silent --accept-*` **per app**, and track each app as its own job. This matches
  requirement 2 (independent jobs, retries, skip/skip-all).
- **Scope and elevation**: machine-scope installers trigger UAC. User-scope installs must
  run in the user's session. winget is an MSIX-delivered app ("App Installer"). Running
  `winget.exe` as LocalSystem from a service is not a supported, reliable path: it is
  widely reported as broken or limited because the package isn't registered for SYSTEM.
  **Design so the per-user agent drives winget.** Machine-scope installs can elevate
  through the helper service only if we accept that complexity. The simpler route is to
  let winget raise its own UAC prompt per installer. Batch elevation by running one
  elevated helper for the whole machine-scope set.
- **Silent switches are manifest data.** Some installers ignore them or show EULAs
  anyway. Some need reboots (exit codes 3010/1641). Apps that need licence activation or
  sign-in (Adobe CC, Office, JetBrains Toolbox) install but are not *usable* until the
  user signs in.
- **Versions**: `--include-versions` pins to the exact version, but old versions are often
  removed from winget-pkgs. The default should be "latest" with "pinned" as an option, and
  pinned falls back to latest with a warning.
- **Order**: install apps **before** restoring their data (USMT gives the same advice).
  Otherwise first-run installers may overwrite restored settings. Close or kill the app
  before restoring data.
- **Store apps**: `winget install --id <9N...> -s msstore` works for free apps. It needs
  an MSA sign-in in the Store for paid or licensed apps.
- **Chocolatey/Scoop**: only offer these if the manager is installed at restore, or
  bootstrap it with consent (choco needs admin, Scoop is user-scope).

### 3.4 Registry: what is safe to export and import

**Hive files** (for context): `HKCU` = `%USERPROFILE%\NTUSER.DAT`;
`HKCU\Software\Classes` = `%LOCALAPPDATA%\Microsoft\Windows\UsrClass.dat`;
`HKLM\SOFTWARE`/`SYSTEM`/`SAM`/`SECURITY` = `%WINDIR%\System32\config\*`. Store apps keep
per-package settings in `...\Packages\<PFN>\Settings\settings.dat`, which is also a hive.

| Area | Back up? | Restore? | Why |
|---|---|---|---|
| `HKCU\Software\<Vendor>\<App>` for a known app | Yes | **Yes, per-app opt-in**, after reinstall, app closed | Most app preferences live here. Rewrite embedded profile paths (3.5). |
| `HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\*` (MRU, Streams, taskbar, Bags) | Optional | **No** (or a curated subset) | Contains machine- and shell-version-specific binary blobs, PIDLs and GUIDs. |
| `HKCU\Environment` (user env vars) | Yes | Yes, with path rewrite and review | Usually safe and useful (PATH entries need review). |
| `HKCU\Control Panel\*` (mouse, keyboard, desktop) | Yes | Selective | Mostly portable. USMT migrates a similar set. |
| `HKCU\Software\Classes` (per-user file associations, COM) | Yes | **No** by default | Associations are protected by `UserChoice` hashes (Windows rejects forged ones). COM registrations point at binaries that may not exist. |
| `HKLM\SOFTWARE\<Vendor>\<App>` | Yes (inventory/reference) | **Rarely**, admin + per-app opt-in | Mixed with installer state. Usually rewritten by reinstall. Licence keys sometimes live here. |
| `HKLM\SOFTWARE\Microsoft\*`, `HKLM\SYSTEM`, `SAM`, `SECURITY` | No (except read-only inventory) | **Never** | Machine identity, drivers, services, accounts, LSA secrets. Restoring them corrupts a new install. |
| Whole `NTUSER.DAT` | Raw copy for forensic/last resort | **Never** load it as the new user's hive | Embeds old SID, paths, DPAPI references and shell state. |

Implementation notes:
- Read with `golang.org/x/sys/windows/registry` and store a **typed, structured** export
  (value name, type, data) as JSON/CBOR. **Do not** use `.reg` text as the primary format.
  A structured format lets VaultDock template `REG_SZ`/`REG_EXPAND_SZ`/`REG_MULTI_SZ`
  paths and diff values, and avoids `.reg` encoding pitfalls. A `.reg` file can still be
  produced as an export convenience.
- `RegSaveKeyEx` / `RegLoadKey` need SeBackupPrivilege / SeRestorePrivilege, so they run
  in the service. `RegLoadAppKey` can open a hive file privately without those privileges,
  which helps for *inspecting* a backed-up `settings.dat` or `NTUSER.DAT` at restore time.
- Track keys from the ludusavi manifest `registry:` entries (games) and a VaultDock app
  catalogue (non-games).

### 3.5 Profiles, usernames, SIDs and ACLs

**Why ACL/SID restore is dangerous**
- Local account SIDs are `S-1-5-21-<machine-specific>-<RID>`. A clean Windows install
  creates a **new machine SID**, so the same username gets a **different SID**. Microsoft
  accounts and Entra accounts also map to local profile SIDs.
- Restoring a DACL or owner that references old SIDs leaves orphaned "Account Unknown"
  ACEs. In the worst case it makes the owner a non-existent principal, or keeps a
  `PROTECTED_DACL` that blocks inheritance. The user can then be locked out of their own
  files, and fixing it needs admin `takeown`/`icacls` work.
- An explicit Deny or `SYSTEM`-only ACL copied from a source system folder can break apps.
- USMT migrates ACLs only for explicitly listed directories, and does not migrate shared
  folder permissions
  ([What USMT migrates](https://learn.microsoft.com/en-us/windows/deployment/usmt/usmt-what-does-usmt-migrate)).
  It remaps users with `/mu:old:new` and `/md:olddomain:newdomain`, and creates missing
  local accounts with `/lac` (disabled until `/lae`)
  ([LoadState syntax](https://learn.microsoft.com/en-us/windows/deployment/usmt/usmt-loadstate-syntax)).

**Recommended VaultDock policy (meets REQUIREMENTS §3)**
- **Back up** each file's security descriptor as SDDL metadata, for audit and for an
  optional advanced restore.
- **Restore by default with no explicit DACL.** Create files under the destination parent
  so they inherit. Set the owner to the *target* user's SID when the service writes on the
  user's behalf.
- Optional "preserve permissions" mode. It only translates **well-known SIDs** (`S-1-5-18`
  SYSTEM, `S-1-5-32-544` Administrators, `S-1-1-0` Everyone and so on), plus an explicit
  user-confirmed mapping `oldSID -> newSID`. Drop any ACE whose SID can't be resolved, and
  report it as a warning.
- Never restore owner, DACL or SACL on `%USERPROFILE%` itself or on `AppData` roots.

**Remapping profile paths, as USMT-like tools do**
- At backup, record the `ProfileList` entry
  (`HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\<SID>\ProfileImagePath`),
  the account name, the SID and **every Known Folder path** (`SHGetKnownFolderPath` for
  FOLDERID_Profile, RoamingAppData, LocalAppData, LocalAppDataLow, Documents, Desktop,
  Pictures, Music, Videos, Downloads, SavedGames, Public, ProgramData).
- Store every backed-up path as **`{token}/relative`**, for example
  `{FOLDERID_RoamingAppData}/Code/User/settings.json`. Don't store absolute `C:\Users\Ali\...`
  paths.
- At restore, resolve tokens against the **current** user. Detect a changed username,
  profile folder or drive. The profile folder name may differ from the account name
  (MSA-created profiles often get a truncated folder name), so always resolve through the
  API, never by string building.
- Optional **content rewrite** of absolute paths inside text configs (JSON/INI/XML) and
  registry strings. Only apply it to files flagged by the app catalogue, show it in dry-run,
  and record before/after values. Binary files are never rewritten.
- Manual mapping UI: old root to new root, overridable per job (REQUIREMENTS §3
  "granular manual destination mapping").

### 3.6 Known Folder redirection, OneDrive, and AppData tiers
- Redirection is recorded in
  `HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\User Shell Folders`. Always
  use `SHGetKnownFolderPath` rather than assuming `%USERPROFILE%\Documents`.
- **OneDrive Known Folder Move** redirects Desktop, Documents and Pictures into
  `%OneDrive%`. Two consequences:
  1. **Files On-Demand placeholders**: reading a cloud-only file **triggers a download**
     (hydration). Detect `FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS` / `RECALL_ON_OPEN` /
     `OFFLINE` and default to **skip, recorded as "cloud-only (owned by OneDrive)"**, with an
     opt-in to hydrate. The same applies to other Cloud Files API providers (Dropbox, Google
     Drive, iCloud).
  2. Avoid double-backup and conflict with OneDrive sync. At restore, if KFM is active on
     the new machine, restore into the redirected location, or warn that OneDrive will sync
     the restored files.
- **AppData tiers**:
  - `Roaming` (`FOLDERID_RoamingAppData`): designed to roam, so it is mostly settings.
    **Highest value, usually portable.**
  - `Local` (`FOLDERID_LocalAppData`): machine-specific and caches, **but** many critical
    profiles live here: Chrome/Edge `User Data`, Discord, Slack, `Programs` (per-user
    installs), `Packages\<PFN>` (Store app data). It needs per-app rules with heavy cache
    exclusions (`Cache`, `Code Cache`, `GPUCache`, `Temp`, `CrashDumps`, `*.log`,
    `Service Worker\CacheStorage`).
  - `LocalLow` (`FOLDERID_LocalAppDataLow`): low-integrity apps. Unity games commonly save
    here (`LocalLow\<Company>\<Product>`), so it is important for game saves.
  - `Packages\<PFN>\{LocalState,RoamingState,Settings}`: Store app data. It can be restored
    only into the same PFN after reinstall. USMT explicitly does **not** migrate Store-app
    settings, so expect partial success.

### 3.7 Locked files and VSS from Go
- **Locks**: Outlook PST/OST, browser SQLite databases (`Cookies`, `History`, `Login
  Data`), `NTUSER.DAT`, running VMs and databases hold exclusive share locks. Reading with
  `FILE_FLAG_BACKUP_SEMANTICS` plus SeBackupPrivilege **bypasses ACLs but not sharing
  violations**. A snapshot is the only consistent answer.
- **VSS requires** an elevated process with Administrators or Backup Operators rights. The
  process must also match the native bitness: a 32-bit process on 64-bit Windows can't
  drive VSS. **The unelevated Wails app can't create snapshots, so a helper service
  (or per-run elevation) is mandatory.**
- Options from Go:
  1. **Own binding of `IVssBackupComponents` via go-ole (MIT)**. This is what restic does
     (BSD-2, `internal/fs/vss_windows.go`, used by `--use-fs-snapshot`; options include
     timeout, exclude volumes/mount points and provider selection,
     [restic docs](https://github.com/restic/restic/blob/master/doc/040_backup.rst)). It
     gives full control (writers metadata, `VSS_CTX_BACKUP`, `BackupComplete`) and is the
     **recommended** route. Write it clean-room from the Microsoft VSS docs.
  2. **mxk/go-vss (MPL-2.0)**, which uses WMI `Win32_ShadowCopy.Create`. It is simpler but
     has no writer coordination (so not application-consistent for Exchange/SQL-type
     writers; acceptable for desktop files). It is a fine early-milestone dependency.
  3. Shell out to `vssadmin`: `create shadow` is **Server-only**, so this doesn't work on
     client Windows. Use `wmic`/PowerShell `Win32_ShadowCopy` instead (wmic is deprecated).
- Snapshot lifecycle: create one snapshot set per backup run for all needed volumes,
  expose the device path `\\?\GLOBALROOT\Device\HarddiskVolumeShadowCopyN\`, map original
  paths onto it, and **always delete** the snapshot on completion or crash. On service
  start, reap orphans tagged by VaultDock.
- Note that `HKLM\SYSTEM\CurrentControlSet\Control\BackupRestore\FilesNotToSnapshot`
  excludes some files from snapshots (Outlook OST by default, per the restic docs).
- Last resort without VSS: raw NTFS reading (go-ntfs, Apache-2.0, the Velociraptor
  technique). It bypasses locks but is crash-inconsistent. Not recommended except for
  forensics.

### 3.8 DPAPI-protected data and EFS

**DPAPI basics**: user master keys are in `%APPDATA%\Microsoft\Protect\<SID>\` and are
protected by a key derived from the user's logon credential (plus a domain backup key on
domain machines). Machine DPAPI keys are under `%WINDIR%\System32\Microsoft\Protect\`. A
blob decrypts only with the matching master key **and** the ability to unlock it. On a
clean install the user has a new SID and new master keys, so **copying blobs plus the
`Protect` folder is not a reliable migration**. It may work for a local account if the
same password is used and the SID-bound key folder is placed correctly, but this is
fragile and unsupported. Treat it as "cannot be restored".

| Data | Protection | Migratable to a new install? | VaultDock approach |
|---|---|---|---|
| Chrome/Edge/Brave saved passwords | `Local State` `os_crypt.encrypted_key` (user DPAPI) → AES-GCM `Login Data`. Chrome 127+ adds **App-Bound Encryption** (user DPAPI + SYSTEM DPAPI + exe-path binding via an elevation service) for cookies, with plans to extend it to passwords ([Google blog](https://security.googleblog.com/2024/07/improving-security-of-chrome-cookies-on.html), [BleepingComputer](https://www.bleepingcomputer.com/news/security/google-chrome-adds-app-bound-encryption-to-block-infostealer-malware/)) | **No** via file copy. Google states ABE binds keys to the machine, so profiles don't roam. | Back up the profile for bookmarks, extensions and history. **Tell the user** to use browser sync or the browser's own password CSV export/import. Never decrypt third-party browser secrets: it resembles infostealer behaviour and would be flagged by AV/EDR. |
| Browser cookies/sessions | DPAPI/ABE | No | User re-logs in. |
| Windows Credential Manager (`%APPDATA%\Microsoft\Credentials`, `Vault`) | user DPAPI | No (Windows Backup only syncs some via MSA) | Inventory target names only (`CredEnumerate` gives names, not secrets) so restore can produce a "re-enter credentials for X" checklist. |
| Wi-Fi profiles | machine DPAPI | Yes **through the supported API**: `netsh wlan export profile key=clear` (admin) → `netsh wlan add profile` | Optional, explicit opt-in. Treat it as a secret and encrypt client-side (already the plan). |
| Certificates with private keys (user `My` store) | DPAPI/CNG | Yes, if exportable: PFX export with password | Opt-in. Non-exportable keys (TPM, smartcard) can't be migrated. |
| EFS-encrypted files | per-file FEK wrapped by the user's EFS cert (private key DPAPI-protected) | Ciphertext yes. **Readable only if the EFS cert/key is exported** (`cipher /x` → PFX) and imported on the new install. | Detect `FILE_ATTRIBUTE_ENCRYPTED`. Back up raw with `ReadEncryptedFileRaw` (works without decrypting, preserves EFS). **Warn** that a key export is required, and offer a guided `cipher /x`. Without the key the data is unrecoverable. USMT lists "EFS files" as migrated, but that still relies on the certificate. |
| App-specific DPAPI blobs (Outlook account passwords, RDP saved creds, VPN clients, Git Credential Manager tokens) | user DPAPI | No | Re-authenticate checklist. |
| VaultDock's own device key (Ed25519 in DPAPI, per REQUIREMENTS) | user DPAPI | **No, by design** | After reinstall, **enrol a new device**. Restore must work from a recovery passphrase or key that does not depend on DPAPI (coordinate with the crypto agent). |
| BitLocker | volume | n/a (transparent while reading) | Remind the user to escrow the recovery key. Not a backup concern. |

"Where authorised and technically possible" (REQUIREMENTS §3): the only clean,
authorised routes are vendor export APIs (PFX, `netsh`, browser export/sync) run **in the
user's own session with explicit consent**. VaultDock should orchestrate these, not
decrypt DPAPI blobs itself.

### 3.9 Game saves and the ludusavi manifest model
- **The manifest model**
  ([README](https://raw.githubusercontent.com/mtkennerly/ludusavi-manifest/master/README.md)):
  YAML keyed by game title. Each game may have:
  - `files`: a map of path templates to `{tags: [save|config], when: [{os, store}]}`.
  - `registry`: keys, also tagged.
  - `installDir`.
  - `launch`.
  - store IDs (`steam.id`, `id.steamExtra`, and others).
  - `cloud` flags (for example `cloud: {steam: true}`, meaning the store already syncs it).
  Placeholders: `<root>`, `<game>`, `<base>`, `<home>`, `<storeUserId>`, `<storeGameId>`,
  `<osUserName>`, `<winAppData>`, `<winLocalAppData>`, `<winLocalAppDataLow>`,
  `<winDocuments>`, `<winPublic>`, `<winProgramData>`, `<winDir>`. Paths are globs, folders
  are recursive, and registry keys include subkeys. Secondary `.ludusavi.yaml` manifests
  can sit in a game folder. The placeholder model maps directly onto VaultDock's
  `{FOLDERID_*}` path tokens (3.5), so a Go reader for this schema is low-effort.
- **Detecting installed games** (needed to resolve `<root>`/`<base>`):
  - **Steam**: `HKCU\Software\Valve\Steam\SteamPath` → `steamapps\libraryfolders.vdf`
    (lists library roots) → `appmanifest_<appid>.acf` (`installdir`). Saves are often under
    `userdata\<steamID3>\<appid>\remote`, where `<storeUserId>` comes from `userdata\*`. The
    text VDF format is parseable with andygrunwald/vdf (MIT).
  - **Epic**: `C:\ProgramData\Epic\EpicGamesLauncher\Data\Manifests\*.item` (JSON with
    `InstallLocation`, `AppName`).
  - **GOG**: `HKLM\SOFTWARE\WOW6432Node\GOG.com\Games\<id>` (`path`, `gameName`).
  - **EA app / Ubisoft Connect**: registry install paths. Ubisoft saves are under the
    launcher's `savegames\<userId>\<gameId>`.
  - **Xbox/Game Pass (MSIX)**: saves are in `Packages\<PFN>\SystemAppData\wgs` containers,
    tied to the Xbox account and cloud-synced. Restore is **unreliable**, so prefer Xbox
    cloud sync.
- **Strategy**: skip or demote entries flagged `cloud.steam` etc. (still allow an optional
  backup as a safety net). Reinstall games through launcher URIs (`steam://install/<appid>`;
  Epic and GOG equivalents), never through winget. Treat game install folders as
  "reinstall", not "backup", because they are huge and re-downloadable.
- **Licence**: see section 2. Fetch the manifest at runtime, cache it, attribute it, and
  don't bundle it.

### 3.10 Development project discovery
- **Roots to scan**: user profile (excluding AppData), `C:\src`, `C:\dev`, `D:\*`, and
  WSL distros (`\\wsl$\<distro>\home` is reachable but slow; inventory only, and
  recommend in-distro backup).
- **Markers**: `.git` directory or `.git` *file* (worktrees/submodules), `go.mod`,
  `package.json`, `Cargo.toml`, `*.sln`/`*.csproj`, `pyproject.toml`/`requirements.txt`,
  `pom.xml`/`build.gradle*`, `.devcontainer/devcontainer.json`, `docker-compose.yml`. Stop
  descending once a project root is found, but still detect nested repositories.
- **Per-repo state** (via `git` CLI, or go-git under Apache-2.0): remotes, current branch,
  uncommitted changes, untracked files, **unpushed commits**, stashes, LFS usage.
  Classification:
  - *clean & pushed* → "re-clone" tier (store remote URL + branch only).
  - *dirty/unpushed* → full backup of the working tree, honouring `.gitignore`, **plus** a
    `git bundle` of unpushed refs.
- **Default excludes**: `node_modules`, `target`, `bin`/`obj`, `.venv`/`venv`,
  `__pycache__`, `dist`/`build`, `.gradle`, `.next`, `vendor` (configurable).
- **Global dev config**: `~\.gitconfig`, `~\.ssh` (**secrets**), `~\.npmrc`/`~\.aws`/
  `~\.kube`/`~\.docker\config.json` (may contain tokens). Classify these as "sensitive",
  require opt-in, and rely on client-side encryption.
- **VS Code**: settings in `%APPDATA%\Code\User\{settings.json,keybindings.json,snippets\}`.
  The extension list comes from `code --list-extensions` (reinstall via
  `code --install-extension`). Don't back up `~\.vscode\extensions` binaries. If the user
  has VS Code Settings Sync on (MS/GitHub account), mark it as "synced elsewhere". The same
  pattern applies to JetBrains (`%APPDATA%\JetBrains\<product><ver>`).

### 3.11 Change detection: USN journal vs ReadDirectoryChangesW

| | ReadDirectoryChangesW (fsnotify, rjeczalik/notify) | USN change journal (`FSCTL_QUERY/READ_USN_JOURNAL`, `FSCTL_ENUM_USN_DATA`) |
|---|---|---|
| Scope | Per watched directory tree (handle per root) | Whole volume |
| Privilege | Normal user | Volume handle needs admin. Windows 10+ also offers an unprivileged read IOCTL with restrictions. Plan for service-side use. |
| Persistence | Only while the process runs. Changes made while not running are lost. | Persistent across reboots and app downtime. Resume from the stored `NextUsn` if the `UsnJournalID` is unchanged and the USN hasn't been purged. |
| Overflow | Buffer overflow drops events (`ERROR_NOTIFY_ENUM_DIR`), which needs a rescan. Network shares are limited. | Bounded journal size: if it wraps past our cursor, do a full rescan. |
| Data | Paths (relative) | File Reference Numbers plus reason flags. Parent FRN resolution to paths is needed (`OpenFileById` or MFT enumeration). V1 records carry 128-bit IDs (ReFS). |
| Filesystems | Any local FS, partial on SMB | NTFS 3.0+ and ReFS only. Not FAT/exFAT, removable or network. ([MS docs](https://learn.microsoft.com/en-us/windows/desktop/fileio/change-journal-operations)) |
| Best for | Low-latency **sync** in the user agent | **Incremental backup** ("what changed since snapshot N") and fast initial enumeration |

Go options: fsnotify (BSD-3, active) for RDCW. For USN, either gentlemanautomaton/volmgmt
(MIT, commit 2026-08) or a small in-house `DeviceIoControl` wrapper over
`x/sys/windows`. The wrapper is small (~300 LOC) and avoids a thin-maintenance dependency.

**Recommendation**: hybrid.
- Sync (existing engine) keeps RDCW in the user process.
- Backup jobs in the service use the USN cursor per volume to produce candidate path sets,
  then verify each candidate by `(size, mtime, FRN)`.
- Fall back to a full scan when the journal ID changes, the journal wraps, the volume
  isn't NTFS/ReFS, or the service was off.
- Always run a periodic full reconcile (weekly) so silent misses are caught.

---

## 4. Recommendations for VaultDock

### 4.1 Process architecture: privileged helper plus per-user agent

```
 +-------------------------- user session (unelevated) ---------------------------+
 | VaultDock.exe (Wails v2)                                                        |
 |  - UI, existing sync engine (RDCW), upload queue, device auth (user DPAPI)      |
 |  - USER-CONTEXT collectors: HKCU, known folders, AppX list, winget/scoop        |
 |    list+install (user scope), browser/VS Code profiles, DPAPI-consent exports,  |
 |    OneDrive/placeholder detection                                               |
 +--------------------------------------+------------------------------------------+
                                        | named pipe \\.\pipe\VaultDock.<ver>
                                        | (go-winio, SDDL: SYSTEM + Administrators full;
                                        |  INTERACTIVE/Authenticated Users connect only)
 +--------------------------------------v------------------------------------------+
 | VaultDockSvc (Windows service, LocalSystem, x/sys/windows/svc)                  |
 |  - VSS snapshot lifecycle + orphan reaping                                      |
 |  - USN cursors per volume                                                       |
 |  - Reads via snapshot / SeBackupPrivilege; writes via SeRestorePrivilege        |
 |  - HKLM inventory (ARP, MSI, GOG/Epic machine manifests), other users' hives    |
 |  - Optional machine-scope elevated installs                                     |
 |  - NO network or cloud credentials: hands byte streams/chunks back to the agent |
 |    or writes to a local spool the agent uploads                                 |
 +----------------------------------------------------------------------------------+
```

Key rules:
1. **Authorise every pipe request.** Get the client token (`ImpersonateNamedPipeClient` /
   `GetNamedPipeClientProcessId` → `OpenProcessToken`). Resolve the caller SID, and only
   allow backup or restore of paths the caller owns or can read, unless the caller is an
   elevated admin. This prevents a confused deputy: SeBackupPrivilege would otherwise let
   any local user read other users' files through VaultDock.
2. **Verify the client binary** (path under Program Files plus an Authenticode signature
   check) as defence in depth. Never rely on it alone.
3. **Narrow, typed RPC** (JSON-RPC or protobuf frames). There is no "run arbitrary
   command" verb. Versioned protocol, and the service rejects an unknown major version.
4. **Restore writes as the target user.** Set the owner SID explicitly and inherit the
   DACL (3.5). Never leave SYSTEM-owned files in a profile.
5. **Keep secrets out of the service.** Encryption keys and cloud credentials stay in the
   user agent (user DPAPI). The service sees plaintext file bytes only transiently, which
   it inevitably does when reading.
6. **Install and update**: the MSI/installer registers the service, and the existing
   self-updater (`desktop/internal/updater`) needs a service-aware path: stop, swap, start,
   all through the service itself or an elevated updater.
7. **Degraded mode without the service** (portable, non-admin users): everything except
   VSS, USN, HKLM writes and other users' data. The UI must say what is missing (no locked
   files, slower change detection).

### 4.2 Inventory data model (proposal)

```
InventorySnapshot { id, deviceId, takenAt, osEdition, osBuild, arch, locale,
                    users: [UserProfile], apps: [AppRecord], games: [GameRecord],
                    projects: [ProjectRecord], secretsChecklist: [CredentialHint] }

UserProfile { sid, accountName, accountType(local|msa|aad|domain), profilePath,
              knownFolders: {FOLDERID_*: path}, oneDriveKfm: bool, oneDriveRoot }

AppRecord {
  key            // stable VaultDock id: hash(normalised name + publisher + scope)
  displayName, publisher, version, arch, scope(machine|user), installLocation
  evidence: [ {kind: arp|msi|msix|choco|scoop|portable, ref: uninstallKey|productCode|
              upgradeCode|pfn|chocoId|scoopApp} ]
  packageIds: [ {manager: winget|msstore|choco|scoop, id, source, matchedBy, confidence,
                 userConfirmed: bool} ]
  reinstallTier: T1..T6 (4.3)
  mode: full-data | data-only | reinstall-inventory      // REQUIREMENTS §1
  dataRules: [ {type: path|registry, template: "{FOLDERID_RoamingAppData}/Foo/**",
                tags: [config|save|cache|secret], excludes: [...], rewritePaths: bool,
                origin: catalog|ludusavi|user} ]
  restoreNotes: [ "requires sign-in", "licence key in HKLM", ... ]
}

GameRecord   { title, store(steam|epic|gog|xbox|ubisoft|ea|other), storeId, installDir,
               manifestRef(ludusavi title + manifest etag), cloudSynced: bool, dataRules }
ProjectRecord{ root(template), markers[], vcs{remotes[], branch, dirty, unpushed, stashes,
               lfs}, tier(reclone|full|bundle+full), excludes[] }
CredentialHint{ kind(wifi|credman|browser|cert|efs|ssh), name, recoverable: bool, action }
```

Each `AppRecord`/`GameRecord`/`ProjectRecord` is its own backup job and manifest, as
REQUIREMENTS §2 asks. The inventory itself is a small, separately versioned job. It is
uploaded first, so a recovery-readiness report is available even before data jobs finish.

### 4.3 Reinstall strategy tiers

| Tier | Mechanism | Automation | Example |
|---|---|---|---|
| T1 | Exact package ID (winget exact, msstore PFN/ID, scoop, choco) | Fully automatic, per-app job, silent | Git, 7-Zip, VS Code, PowerToys |
| T2 | Fuzzy or suggested ID | Needs one-click user confirmation (remembered) | Apps with renamed manifests |
| T3 | Launcher-managed | Opens launcher URI / queues in launcher | Steam/Epic/GOG games |
| T4 | Vendor download page from ARP `URLInfoAbout` | Guided manual | Niche or OEM utilities |
| T5 | Cached installer in the backup (opt-in, user's own copy) | Semi-automatic, may still prompt | Offline or legacy installers |
| T6 | Not reinstallable (drivers, licence-locked, enterprise-deployed) | Inventory and checklist only | GPU drivers, Office via org, Adobe CC |

Restore order for each app job: install → verify (re-scan ARP or `winget list --id`) →
close the app → restore data with path rewrite → post-restore hints (sign-in needed and
so on). Failures feed the Retry Skipped workflow (REQUIREMENTS §4). The dry-run preview
shows tier, ID, evidence and data size per app.

### 4.4 Honest limitations: what can never be restored reliably
1. **DPAPI-protected secrets** on a new install: browser saved passwords and cookies
   (worse with Chrome App-Bound Encryption), Credential Manager entries, app tokens, and
   VaultDock's own device key. Mitigation: vendor sync/export guidance and a re-auth
   checklist.
2. **EFS files without an exported EFS certificate and private key**. These can't be
   decrypted.
3. **Non-exportable keys** (TPM-bound, smartcard, Windows Hello, passkeys bound to the
   device).
4. **Original ACLs/SIDs** across reinstalls. By design we reset to inherited permissions.
5. **Installed application binaries** as a "transplant". We reinstall instead; PCmover-
   style binary migration is out of scope. Exact old versions may no longer be
   downloadable.
6. **Licence activation and DRM state** (Office, Adobe, games with machine-bound
   activation, anything tied to hardware IDs).
7. **Microsoft Store app settings**, which USMT also does not migrate. Xbox/Game Pass
   `wgs` saves are only reliable through Xbox cloud.
8. **Shell state**: Start layout, taskbar pins, file associations (UserChoice hash),
   Explorer view bags, notification settings. Best-effort only.
9. **Drivers, services and machine policy** (HKLM `SYSTEM`/`SOFTWARE\Microsoft`, Group
   Policy, Intune/MDM state).
10. **Cloud-only placeholders** (OneDrive Files On-Demand and similar). Skipped by default;
    their source of truth is the cloud provider.
11. **Application consistency of live databases** without VSS writers (WMI-only VSS route,
    or no service). Crash-consistent at best.
12. **Settings across major app versions**. USMT notes this is unsupported in general
    (except Office), so "restore older settings into a newer app" is best-effort.
13. **Domain/Entra profile identity changes** (AD → Entra and similar). USMT itself doesn't
    support these, so VaultDock offers only manual path mapping.
14. **Changes made while the service was stopped and the USN journal wrapped**. These are
    caught only by a full rescan.

### 4.5 What to borrow, by project
- **winget**: the per-app `install --id -e --silent` loop, export JSON for interop, and
  pseudo-ID awareness. The COM API is the later option once a Go/WinRT shim is worthwhile.
- **UniGetUI**: the multi-manager bundle concept and showing source and confidence
  per package.
- **ludusavi**: the manifest schema (placeholders, tags, `when`, `cloud`), store root
  detection, and its "don't bundle, fetch and attribute" data handling.
- **restic**: the VSS lifecycle shape and its option surface (timeout, exclude volumes,
  provider).
- **USMT**: user remap (`/mu`, `/md`), install-before-restore ordering, explicit ACL policy
  and its documented "doesn't migrate" list. These become the honest-limitations UX.
- **Windows Backup app**: tiered presentation of folders, apps, settings and credentials,
  and offering restore during first-run setup ([support](https://support.microsoft.com/help/17127)).

---

## Sources (licence and activity verification)
- LICENSE files: https://raw.githubusercontent.com/{microsoft/winget-cli/master, Devolutions/UniGetUI/main,
  ChrisTitusTech/winutil/main, mtkennerly/ludusavi/master, mtkennerly/ludusavi-manifest/master,
  chocolatey/choco/develop, ScoopInstaller/Scoop/master, MikeMaximus/gbm/master, andygrunwald/vdf/main,
  restic/restic/master, mxk/go-vss/main, go-ole/go-ole/master, golang/sys/master, microsoft/go-winio/main,
  kardianos/service/master, fsnotify/fsnotify/main, rjeczalik/notify/master, gentlemanautomaton/volmgmt/main,
  chaugan/swinv/main, Velocidex/velociraptor/master, Velocidex/go-ntfs/master, microsoft/wmi/master,
  yusufpapurcu/wmi/master, shirou/gopsutil/master, go-git/go-git/main, wailsapp/wails/master,
  microsoft/winget-create/main, microsoft/winget-cli-restsource/main, derrod/legendary/master}/LICENSE;
  JosefNemec/Playnite/master/LICENSE.md; Heroic-Games-Launcher/HeroicGamesLauncher/main/COPYING
- Activity: https://github.com/<repo>/commits/<branch>.atom for each repo above;
  https://api.github.com/repos/{microsoft/winget-cli, microsoft/winget-pkgs, mtkennerly/ludusavi,
  mtkennerly/ludusavi-manifest, restic/restic, chocolatey/choco, ScoopInstaller/Scoop,
  marticliment/UniGetUI, ChrisTitusTech/winutil, mxk/go-vss}
- winget export/import: https://learn.microsoft.com/en-us/windows/package-manager/winget/export ,
  https://learn.microsoft.com/en-us/windows/package-manager/winget/import
- winget COM API (community mirror of repo docs): https://www.mintlify.com/microsoft/winget-cli/com-api/overview
- USMT: https://learn.microsoft.com/en-us/windows/deployment/usmt/usmt-what-does-usmt-migrate ,
  https://learn.microsoft.com/en-us/windows/deployment/usmt/usmt-loadstate-syntax
- restic VSS: https://github.com/restic/restic/blob/master/internal/fs/vss_windows.go ,
  https://github.com/restic/restic/blob/master/doc/040_backup.rst
- go-vss: https://github.com/mxk/go-vss
- USN journal: https://learn.microsoft.com/en-us/windows/desktop/fileio/change-journal-operations ,
  https://pkg.go.dev/github.com/zoid42/volmgmt/usn , https://pkg.go.dev/github.com/chaugan/swinv/internal/usn
- Chrome App-Bound Encryption: https://security.googleblog.com/2024/07/improving-security-of-chrome-cookies-on.html ,
  https://www.bleepingcomputer.com/news/security/google-chrome-adds-app-bound-encryption-to-block-infostealer-malware/ ,
  https://cvemon.intruder.io/cves/CVE-2025-34091
- PCGamingWiki licence: https://en.wikipedia.org/wiki/PCGamingWiki ,
  https://developer.valvesoftware.com/wiki/Template:Sysreq/doc
- GameSave Manager: https://www.howtogeek.com/107323/how-to-back-up-restore-over-1000-pc-games-with-gamesave-manager/ ,
  https://www.gamesave-manager.com/
- Windows Backup: https://support.microsoft.com/help/17127 ,
  https://pureinfotech.com/cloud-backup-apps-settings-files-windows-11/

Items stated from platform knowledge rather than a fetched page in this session:
- registry value semantics
- the `ARP\...` pseudo-IDs
- the VSS bitness and privilege requirement
- the unprivileged USN IOCTL
- Files On-Demand attributes
- the DPAPI master-key layout
- `ReadEncryptedFileRaw`
- winget-as-SYSTEM limitations
- launcher manifest paths

Verify these against Microsoft Learn during design review.
