# VaultDock Backup, Recovery & Storage: Master Plan

Status: **implementation authorised 2026-10-18** (owner green light; open questions Q1–Q18 accepted at their recommendations). Phase S done (local commits only). Plan revision **r3** (2026-10-18). Baseline: `main` @ `10af74f` (single repository `D:/VaultDock/VaultDock`, origin = Codehold `git.aspecracing.com.au/aspec-racing-pty-ltd/vaultdock`).

r3 incorporates the owner's binding decisions O1–O9, the deployment requirements DR1–DR8 and the install-mode requirement R11 (§2). Conflicts these create are listed explicitly in §4 and are **not** silently resolved.

## ⚑ Execution protocol (read first; binding for the master agent and every sub-agent)

**Locked contracts.**
- [sdk/contracts/api-v1.md](sdk/contracts/api-v1.md) is the HTTP API contract.
- [sdk/contracts/backup-format-v1.md](sdk/contracts/backup-format-v1.md) is the repository byte format.
- Both are **LOCKED at contract revision 1.1**. Sub-agents implement them exactly and never edit them.
- A contract problem is recorded under "Contract issues" in the task report while the rest of the task continues.
- Only the master agent (INT) amends a contract, by appending a versioned amendment.
- After the repository split (Phase S), the contracts live in `VaultDock/sdk/contracts/` (§7.1).

**Sub-agent completion reports.**
- When a sub-agent finishes (completed, partial, failed or stopped), it creates `SUBAGENT-<TASKID>-<short-title>.md` in `D:/VaultDock/VaultDock/` (the client repository root, also for server-repo tasks, so all reports are in one place).
- The report contains its full summary and changes:
  - task id and goal;
  - status;
  - every file created, modified or deleted (with repository) and why;
  - commands and tests run, with results;
  - contract issues;
  - gaps and follow-ups;
  - notes for dependent tasks.
- A task is not done until its report exists.

**Master review and commit.**
- When all sub-agents have finished completely, the master/management agent:
  1. reviews every `SUBAGENT-*.md`;
  2. audits the changes against the finished code in **both** repositories (diffs, builds, `go vet`, all Go and web tests, ownership-boundary check, licence check);
  3. makes any fixes, repairs or changes needed;
  4. commits locally in each affected repository with **`ch commit -m "<small summary of the full changes>"`** (`ch` = the Codehold CLI wrapper around git, verified at `C:\Users\Ali\go\bin\ch.exe`);
  5. **never pushes**;
  6. reports to the owner.
- Sub-agents never run `git`/`ch` commands that change state (commit, push, checkout/switch, stash, reset, rebase, merge, filter-repo).

**Parallelism.**
- Run as many sub-agents at once as possible, from the task graph (§18).
- A task starts **only after all of its dependencies have finished with a `completed` report**. Never launch a dependent task early.
- Independent tasks run in parallel.

**Small tasks only.**
- No sub-agent gets a feature that would take 10–20 minutes or longer; context growth burns usage.
- Every task is sized for roughly ≤10 minutes and runs in a **fresh** sub-agent.
- An agent whose task grows stops at a buildable point and reports `partial`; the master splits the remainder.

**STOP.**
- When the owner says **"STOP"**, the master immediately stops/kills every running sub-agent and launches nothing new.
- It reports which tasks were running, finished or partial, and waits.
- Nothing is committed during a STOP unless the owner says so.

**Hygiene.**
- Edit only owned files (§13/§18).
- Run tests for your own packages only.
- Dependency changes go only through the dependency owner. Only INT runs `go mod tidy`.
- Network access only where a task lists it (licence-text fetch, package install by the dependency owner).

**Exception to "commit only at the end": Phase S.** The repository split (§7.1) cannot be done without local commits: the history import into `vad-server` and the removal commit in `VaultDock`. INT performs Phase S itself (no sub-agents), locally only and never pushed, **only after a separate explicit owner approval** (Q1 in §16).

---

Sources:
- research reports [planning/research/01–07](planning/research/);
- requirements brief [planning/REQUIREMENTS.md](planning/REQUIREMENTS.md);
- the owner decisions of 2026-10-18 (§2).

The research reports describe the pre-split, pre-r3 design. Where they differ from r3, **r3 and the contracts win**.

---

## 1. Executive summary

VaultDock becomes **two projects**:
- **`vad-server`** (`D:/VaultDock/vad-server/`): the Go server, the embedded React web UI, the Docker deployment and `install.sh`.
- **`VaultDock`** (`D:/VaultDock/VaultDock/`): the Windows application and a small shared Go **SDK module**. The SDK holds the wire types, the API client, request signing, the automation schema/engine, the repository format, the restore planner, schedules and module manifests. No server implementation code lives in the client repository.

The Windows application is installed **only by an admin installer**. It runs a least-privilege Windows service (the agent) and a per-user tray/UI that talks to it over an ACL-restricted named pipe. At install time the user chooses **Simple Folder Backups** or **Full Access to All Tools (Advanced)**. Both modes are one modular application: a common core plus built-in modules toggled by mode and optional components downloaded from public GitHub releases. Users can switch modes later without reinstalling or losing anything.

**Backups:**
- per-job content-defined chunks, compressed and encrypted on the client, uploaded as packs as soon as each is sealed;
- the server catalogs them, and its catalog of names is itself encrypted at rest;
- key material is **shared** between the user's devices and the server through an escrow established at device registration, so the web UI can browse metadata and (by default) download files;
- a mandatory recovery key still allows restore with no server at all;
- key rotation is epoch-based.

**Sync** supports per-folder **one-way upload (default)** and **two-way** with conflict policies, version history, a change feed, mass-delete guards and strict system-file protection.

**Multi-user PCs:** the active user's profile is protected by default. Other profiles need that user's own consent. Detected credential and security changes blacklist a profile until a server **superuser** clears it and the user consents again.

**Other decisions:**
- AI uses any OpenAI-compatible endpoint.
- Code signing is optional and dormant until a certificate is supplied.
- The project is licensed under **PolyForm Noncommercial 1.0.0**.
- Third-party code is incorporated only when appropriately licensed (MIT preferred) and is credited in the README.
- Direct-to-cloud backup is out of scope. Cloud storage (S3/R2/B2) remains available as **server-side** destinations.

**Deployment:**
- Docker Compose without bundled proxy or cloudflared containers;
- `install.sh` that installs, upgrades repeatably and configures integrations, detecting an existing cloudflared install;
- environment variables are only ever added, never removed without approval;
- backups and rollback on every upgrade;
- Codehold is the internal git host and mirrors to public GitHub.

---

## 2. Owner requirements (binding)

| ID | Requirement (summary of the owner's wording) |
|---|---|
| O1 | Browsable metadata; backup data encrypted on client **and** server using securely managed shared key material established at device registration; defined key exchange, storage, rotation and recovery; plaintext master secrets never exposed |
| O2 | One-way and two-way sync; client→server one-way by default; conflict resolution; per-folder configuration |
| O3 | Admin installer only; necessary filesystem and service permissions; strict system-file protection; least privilege; safeguards against OS damage |
| O4 | Optional code signing, disabled until a valid certificate is supplied |
| O5 | Only appropriately licensed third-party code (prefer MIT); credit actually incorporated repositories and their specific contribution in the README; never credit research-only repositories; verify licences independently |
| O6 | Multi-user Windows: default to the active user's profile; other profiles require that user to log in, open VaultDock and authorise; admins may request selected/all profiles subject to that consent; secure password-change detection where technically possible; credential/security changes blacklist profiles until a server superuser clears and reapproves; never store passwords; never bypass Windows access controls |
| O7 | Licence: PolyForm Noncommercial 1.0.0 |
| O8 | AI: generic OpenAI-compatible endpoints (user-configurable URL, model, API key) |
| O9 | Remove direct-to-cloud backup; split into `D:/VaultDock/vad-server/` (server) and `D:/VaultDock/VaultDock/` (Windows app/client); only shared protocols/contracts/client-connection code in the client project; plan a safe repository/history/build/deployment migration |
| DR1 | Docker server deployment; HTTPS for clients via optional Cloudflare Tunnel or alternative reverse proxies |
| DR2 | No cloudflared or proxy containers in the default Compose deployment |
| DR3 | `install.sh`: initial install plus repeatable upgrades and optional integration setup; detect an existing cloudflared (systemd or Docker) before configuring |
| DR4 | Upgrades touch only the necessary images and config; preserve volumes, databases, data, secrets and `.env` values |
| DR5 | New env vars are added only when missing; removing obsolete vars requires explicit per-variable approval (upgrade script or admin UI) |
| DR6 | Integration changes at upgrade time without destructive reinstalls, with validation, backups and rollback |
| DR7 | Public GitHub distribution alongside Codehold, the internal git host that mirrors pushes to GitHub |
| DR8 | Preserve the sub-agent protocol; local `ch commit`, never push |
| R11 | Install-time choice of **Simple Folder Backups** or **Full Access to All Tools (Advanced)**, each with a clickable ⓘ explaining function, scope, permissions, resources and differences in plain language. The mode decides which components are downloaded (public GitHub releases), installed, enabled and shown. A modular system, not two apps. Switch Simple↔Advanced later in settings without reinstalling or losing backups/config. The server supports both seamlessly |

Original requirements 1–10 ([planning/REQUIREMENTS.md](planning/REQUIREMENTS.md)) still apply, except where O1–O9/R11 override them. Specifically: requirement 6's "user-managed credentials" now means server-side destination credentials (O9), and requirement 9's "client-side encryption" now means shared-key encryption (O1).

---

## 3. Binding design decisions

| # | Decision | Status vs r2 |
|---|---|---|
| D1 | Backup data in a separate repository, never in `nodes`/`blobs` | kept |
| D2 | Own repository format from small permissive libraries (zstd BSD-3, x/crypto BSD-3); the chunker is specified in the contract | kept |
| D3 | Shared code lives in the **SDK module `VaultDock/sdk`** (Go module `github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk`), used by `vad-server` and the Windows client. Server-only code (objstore, retention, catalog, keys custody, GC, scheduler) lives in `vad-server/internal` | **changed** (O9) |
| D4 | Storage backends are **server-side only**: `vad-server/internal/objstore` with `localfs` (incl. host-mounted SMB/NAS) and `s3` (AWS, R2, B2, MinIO). Devices always upload through the server | **changed** (O9) |
| D5 | Sync blob storage stays on `internal/storage` (not migrated to objstore) | kept |
| D6 | Pack upload = one signed, streamed PUT per pack (target 16 MiB, max 64 MiB) | kept |
| D7 | CDC `vd-fastcdc-1`: 512 KiB / 1 MiB / 8 MiB, keyed | kept |
| D8 | **Shared-key model**: `DK` + per-epoch `RMK_e`, wrapped by X25519 sealed boxes to device, server escrow (SEK), recovery and optional passphrase recipients; `escrow_mode` full (default) or metadata; a backward epoch chain; the server KEK keeps SIK/SEK sealed at rest; the SIK is pinned at enrolment | **changed** (O1) |
| D9 | Browsable metadata comes from the server decrypting tree/meta blobs with escrowed `K_meta`. The catalog is encrypted at rest with a per-user DEK under the server KEK; search uses an HMAC blind index (token/prefix only) | **changed** (O1) |
| D10 | GC = mark-and-sweep at pack level with a grace period and a two-phase delete. Compaction is server-side under full escrow, a device task under metadata escrow | updated |
| D11 | **Admin-only install**: agent = Windows service; the per-user UI is a thin client over a named pipe. No per-user install mode. An in-process host exists only for tests | **changed** (O3) |
| D12 | **Sync: per-folder `upload` (default) or `two_way`**, a server change feed, three-way merge state on the client, conflict policies, mass-delete guard, deletes to trash/Recycle Bin only | **changed** (O2) |
| D13 | Single approvals model; approvals only by a session user with CSRF | kept |
| D14 | Server scheduler `internal/scheduler`, desktop scheduler, shared `sdk/schedule` | kept |
| D15 | Standalone `vaultdock-restore.exe` in `VaultDock/desktop/cmd/vaultdock-restore`. Inputs: server URL, or a local/UNC path to a repository copy, plus the recovery key or passphrase | updated (O9) |
| D16 | Never restore ACLs/SIDs; all user-space writes happen **under the target user's token**; a system-file protection guard (§7.6) applies to every write | strengthened (O3, O6) |
| D17 | ReadDirectoryChangesW for sync; USN journal for backups in the service | kept |
| D18 | Game-save data fetched at runtime with attribution, not bundled | kept |
| D19 | AI: one generic OpenAI-compatible client written in-house (no SDK dependency); SSRF policy; proposals only; audit | **changed** (O8) |
| D20 | Web feature registry; server mount hooks with stub route files | kept |
| D21 | Locked contracts; wire types in `sdk/wire` | kept (path changed) |
| D22 | Device-runtime automation ops are delivered as `automation_op` device tasks | kept |
| D23 | Chunker defined by the contract, not by a library | kept |
| D24 | **Least privilege in the agent**: LocalSystem service with SCM-restricted `RequiredPrivileges`. All profile reads and writes happen under the profile owner's token (interactive or S4U), with an `AccessCheck` gate when reading VSS snapshots. No SeRestorePrivilege use; SeBackupPrivilege only for VSS operations (§7.6) | new (O3, O6) |
| D25 | **Profiles & consent**: per-SID consent recorded with a user-scope-DPAPI-bound proof; security-change signals blacklist a profile; only a superuser clear plus fresh local consent re-enables it (§7.8) | new (O6) |
| D26 | **Install modes & modules**: core + built-in modules (`sync`, `automation`, `profiles`) toggled by mode + downloadable components (`vss`, `discovery`, `reinstall`), described by a signed-or-checksummed component manifest in GitHub releases and installed by the agent. The server gates features on reported modules (§7.7) | new (R11) |
| D27 | **Repository split** with history preserved: `vad-server` is created via `git filter-repo` path filtering; `VaultDock` keeps its history and removes server paths in one commit. Module paths move to `github.com/ASPEC-RACING-PTY-LTD/…` (§7.1) | new (O9) |
| D28 | **Deployment**: Compose with only the `vaultdock` service; proxy and tunnel integrations live in `install.sh` and opt-in files under `deploy/integrations/`; an `env.schema.json` drives add-only `.env` merging; removals need explicit approval (§7.13) | new (DR1–DR6) |
| D29 | **Release integrity**: SHA-256 manifests always; optional Authenticode when a certificate exists. **Recommended:** an Ed25519 release-manifest signature with the public key embedded in the app (no certificate needed). Needs owner confirmation (Q4) | new (O4) |
| D30 | **Licensing**: PolyForm Noncommercial 1.0.0 for both repositories; a dependency allowlist enforced in CI; THIRD_PARTY_NOTICES plus README "Credits" listing only incorporated code (§7.15) | new (O5, O7) |

Removed from r2: D9's "managed-metadata plaintext listing", the per-user installer and the in-process production mode, Phase 4b (direct-to-destination), `pkg/objstore` in shared code, client-held destination credentials, and the Anthropic/OpenAI SDK dependencies.

---

## 4. Conflicts and tensions identified (not silently resolved)

| # | Between | Conflict | Resolution in r3 | Owner action |
|---|---|---|---|---|
| C1 | O1 "shared key material" vs requirement 9 / earlier E2E intent | If the server holds key material, a compromise of the server (KEK + DB + storage) exposes metadata, and under full escrow also file contents. This is not zero-knowledge | Default `escrow_mode=full` per O1. `metadata` mode offered per repository (server can browse, never read contents). Server KEK stored outside the DB; SIK pinning; audit of every server-side decryption | Confirm default `full` (Q2) |
| C2 | O1 "encrypt on client **and** server" | Encrypting already client-encrypted packs a second time on the server adds CPU/IO cost but no confidentiality (the server holds both keys), and breaks oid = SHA-256(stored bytes) verification | **Interpretation:** client-side AEAD for all repository objects; **server-side** encryption at rest for everything the server derives or holds (catalog names/paths, SIK/SEK/escrow material, destination credentials, AI keys). Packs are **not** double-encrypted. S3 server-side encryption is enabled where available | Confirm the interpretation (Q3) |
| C3 | O1 browsable metadata vs encrypted catalog | Searching encrypted names prevents arbitrary substring search in SQL | Blind index (HMAC tokens): whole-word and prefix search only. A full-text substring search would require decrypting the catalog in memory per query (slow) | Accept the search limitation (Q3) |
| C4 | O1 server-held keys vs requirement 9 "restore without the server" | Server-side escrow is useless if the server is gone | Mandatory recovery key (asymmetric, works across rotations); optional passphrase; standalone restore tool reads a repository copy from a disk/UNC path | — |
| C5 | O3 admin-only vs current users | Existing per-user installs (`%LOCALAPPDATA%\Programs`, HKCU Run, user-scope DPAPI device key) cannot be silently elevated by the per-user updater | The last per-user release ships a "Move to the new installer" prompt (UAC). The admin installer detects the per-user install, migrates config and state, re-protects the device key machine-scope through the user's UI session (`AdoptUserDevice`), then removes the per-user copy. If the user declines, the per-user version keeps working but receives no further updates | Confirm the migration UX (Q5) |
| C6 | O3 least privilege vs VSS / multi-profile | VSS requestors need Administrators/Backup Operators and SeBackupPrivilege. S4U impersonation needs SeTcbPrivilege (LocalSystem) | LocalSystem with an SCM `RequiredPrivileges` allowlist (SeBackup, SeTcb, SeImpersonate, SeChangeNotify, SeIncreaseQuota, SeAssignPrimaryToken); everything else is stripped. User data is accessed under user tokens. A narrow VSS component process. Spikes P0-S1 and P0-S3 validate this | — |
| C7 | O6 "never bypass Windows access controls" vs VSS and locked files | Reading a VSS snapshot as SYSTEM with SeBackupPrivilege bypasses ACLs | Every file read from a snapshot is preceded by `AccessCheck(user token, file security descriptor, FILE_READ_DATA)`; a denied file is skipped with an issue. Live files are opened under the user's token. **Limitation:** if the spike shows `AccessCheck` gating cannot be made reliable for some objects, VSS is restricted to the active user's own profile | Accept (Q6) |
| C8 | O6 password-change detection "where technically possible" | Windows offers no universal, unprivileged password-change notification. Microsoft accounts and Entra ID changes are not reliably observable locally | Signals: (1) local accounts: `NetUserGetInfo` password age reset; (2) password **reset**: the user-scope DPAPI consent proof fails to decrypt; (3) domain: LDAP `pwdLastSet` when the DC is reachable; (4) Security log 4723/4724 when auditing is enabled; (5) SID, account-disabled or profile-path changes. Microsoft and Entra account changes made elsewhere may go undetected until a DPAPI or sign-in signal appears (documented limitation) | Accept the limitation (Q7) |
| C9 | O6 "superuser" | The server today has `admin` only | New `users.is_superuser`; the first admin is a superuser; at least one must always remain | Confirm superuser ≠ every admin (Q8) |
| C10 | O6 whose account receives another profile's data | Consent alone does not decide which VaultDock user owns a second Windows profile's backups | Default: the consenting Windows user signs in with **their own** VaultDock account in the UI, and their data goes to their own repository. Alternative: consent to the device owner's account (shared household) | Choose (Q9) |
| C11 | O7 PolyForm Noncommercial vs "public GitHub distribution" / third-party licences | PolyForm NC is source-available, not OSI open source. Permissive dependencies (MIT/BSD/ISC/Apache-2.0) are compatible if their notices are kept. GPL/AGPL code can never be incorporated. Commercial users cannot use VaultDock without a separate licence | Licence both repos under PolyForm NC 1.0.0 with a `Required Notice: Copyright © ASPEC Racing Pty Ltd`; change marketing wording from "open source" to "source-available"; plan a contributor licence/assignment if outside contributions are accepted | Confirm the copyright holder and the contribution policy (Q10) |
| C12 | O5 "prefer MIT" vs best-available libraries | No MIT-licensed equivalents of comparable maturity exist for zstd (klauspost, BSD-3), the S3 client (minio-go, Apache-2.0), or x/crypto and x/sys (BSD-3) | Use them with notices (permissive, PolyForm-compatible), each justified in the licence audit. MIT is chosen wherever comparable (go-winio, go-ole, vdf, volmgmt, wails, notify). **go-vss (MPL-2.0) dropped** in favour of an in-house WMI/COM requestor | Accept (Q11) |
| C13 | O5 credits vs runtime tools and data | winget, choco, scoop and git are *invoked*, not incorporated. Ludusavi manifest data is *fetched at runtime*. GeoLite (optional, user-supplied) is not bundled | Not listed under README "Credits" (not incorporated). Listed in an in-app "Integrations and data sources" notice with attribution where a licence requires it (PCGamingWiki CC BY-NC-SA) | — |
| C14 | O4 unsigned builds vs an admin service | An unsigned SYSTEM service plus self-updating components invites SmartScreen/AV friction, and makes the GitHub account a single point of compromise for code running as SYSTEM | Signing stays optional (O4). The updater requires the **same** Authenticode publisher once a signed build is installed. Recommended D29: an Ed25519 manifest signature works today without a certificate | Approve the Ed25519 manifest signing (Q4) |
| C15 | R11 "only download necessary components" vs one transfer controller / small built-ins | Splitting the sync engine or automation runtime into separate processes would duplicate the transfer controller and add IPC overhead | Hybrid: heavy, privileged or third-party-dependent parts are **downloaded components** (`vss`, `discovery`, `reinstall`); small parts (`sync`, `automation`, `profiles`) are **built into core** and only enabled and shown in Advanced (~a few MB) | Confirm the hybrid (Q12) |
| C16 | R11 components from public GitHub vs offline/air-gapped installs | Mode setup needs internet access to GitHub | Optional "offline full installer" release asset containing all components | Confirm whether it is needed (Q13) |
| C17 | R11 Simple mode vs O3 admin-only | Simple mode still needs the admin installer and the service (scheduled backups while logged off, protected install) | Same admin installer; Simple enables fewer modules. The ⓘ text explains why admin rights are needed | — |
| C18 | R11 Simple mode without VSS | Locked files (e.g. Outlook PST) are skipped in Simple mode | Readiness shows the "locked files skipped" issue class with a one-click "Enable snapshot support" that installs only the `vss` component (staying in Simple mode) | Confirm VSS as an optional add-on in Simple (Q14) |
| C19 | DR5 admin-UI env removal vs a container that cannot edit host files | The server container (read-only root FS) cannot edit the host `.env` | The admin UI records approvals into the state volume (`deploy/env-removal-approvals.json`); `install.sh upgrade` applies them on the host | — |
| C20 | DR2 vs the existing `caddy` Compose profile | Current installs using `COMPOSE_PROFILES=caddy` rely on a bundled proxy container | The default Compose drops Caddy. `install.sh upgrade` detects the old profile and moves it to the opt-in `deploy/integrations/caddy/compose.caddy.yml` override **with the same volumes**, so no data or certificates are lost | — |
| C21 | O9 split vs existing server installs | Installed servers auto-update from `ASPEC-RACING-PTY-LTD/VaultDock` releases and download `install.sh` from that repo | A transitional release from the `VaultDock` repo ships an `install.sh` that switches its source to `vad-server`. The old repo keeps publishing a tiny `install.sh` **redirect stub** for 2 release cycles. That stub is deployment glue, not server implementation | Approve the stub (Q15) |
| C22 | O9 "no server source in client project" vs git history | Old commits in `VaultDock` contain server source | History is preserved (no rewrite), because rewriting breaks every clone and the Codehold→GitHub mirror. If the owner wants server code purged from client history before the public release, a one-time rewrite before first publication is possible | Choose (Q16) |
| C23 | O9 SDK in the client repo vs server builds | `vad-server` depends on a module hosted inside the client repo; Docker builds need it | The SDK is versioned with `sdk/vX.Y.Z` tags and fetched via the Go module proxy from the public GitHub mirror (or `GOPRIVATE` from Codehold). Local development uses an untracked `D:/VaultDock/go.work` | Confirm the module path host: GitHub vs Codehold (Q17) |
| C24 | DR3 cloudflared detection vs remotely managed tunnels | Tunnels created in the Cloudflare dashboard (token-based) cannot have ingress edited locally | `install.sh` detects systemd/Docker cloudflared. For a locally managed config it adds or validates the ingress rule (backup first, then `cloudflared tunnel ingress validate`, then reload, with rollback). For token tunnels it prints exact dashboard steps and verifies reachability afterwards | — |
| C25 | O2 two-way sync vs O3 OS-damage safeguards | Two-way sync writes and deletes local files | Guard (§7.6); deletes go to the Recycle Bin; mass-delete guard; two-way never allowed on system locations or drive roots; enabling two-way requires an approval and a preview | — |

---

## 5. Baseline and verified defects

Unchanged from r2. Summary:
- **Server:** chi + SQLite (one migration, 13 tables), per-user whole-file blobs, upload sessions, maintenance ticker only.
- **Web:** React 19 with hard-coded routes; UI kit duplicated with the desktop.
- **Desktop:** per-user unelevated Wails app, one-way sync, `state.db` without migrations.
- **CI:** builds and tests only; nothing is signed.
- **Compose:** bundles an optional Caddy container.
- **`install.sh`:** has `set_env` (update-or-append), backups and rollback.
- There is no `LICENSE` file.
- Root module `github.com/vaultdock/vaultdock`; desktop module `…/desktop` with `replace ../`.

| ID | Defect (✔ re-verified in code by INT) | Fix task |
|---|---|---|
| B1 ✔ | Devices can purge trash (`internal/api/server.go:563-564`) | P0-03 |
| B2 ✔ | Zip silently skips missing blobs (`internal/api/files.go:506-507`) | P0-09 |
| B3 ✔ | `schedule.interval` last-fire in memory (`desktop/internal/engine/automations.go:244-249`) | P0-23 |
| B4 ✔ | Invalid automations silently skipped (`automations.go:47-49`) | P0-23 |
| B5 ✔ | Logout keeps the query cache (`web/src/lib/auth.tsx:84-92`) | P0-13 |
| B6 ✔ | Web/desktop UI kit byte-identical copies | P0-21/P0-27 + parity script |
| B7–B9 | Upload/storage hardening (research 01 R3–R7) | P0-06, P0-07 |
| B10 | Versions = anonymous trash roots | Phase 5 |
| B11 | No scheduled DB backup | Phase 4 |
| B12 | Silent scan skips, infinite locked-file deferral, in-memory reports | P0-23, P0-25 |
| B13 | Testserver drift | P0-05 |
| B14 | Settings PUT overwrites the whole struct | P0-04 |
| B15 ✔ | `install.sh set_env` **overwrites** existing values (conflicts with DR4) | P0-32 |
| B16 ✔ | Default Compose bundles the Caddy service (conflicts with DR2) | P0-32 |

---

## 6. Target architecture

```
 D:/VaultDock/vad-server  (Go module github.com/ASPEC-RACING-PTY-LTD/vad-server)
 ┌──────────────────────────────── vaultdock container ───────────────────────────────┐
 │ internal/api (+mount hooks) · store (SQLite) · storage (sync blobs, unchanged)      │
 │ keys (KEK, SIK, SEK, escrow ops) · catalog (encrypted, blind index) · backup (GC,   │
 │ verify, replicate, compaction) · objstore (localfs/S3) · retention · scheduler ·    │
 │ secrets · ai (OpenAI-compatible) · web/ (React, embedded)                           │
 └──────────────▲──────────────────────────────────────────────────────────────────────┘
                │ HTTPS (direct TLS | reverse proxy | Cloudflare Tunnel — not bundled)
 D:/VaultDock/VaultDock
 ├ sdk/  (Go module …/VaultDock/sdk)  wire · client · protocol · automation · vdcrypt ·
 │        compress · chunker · pack · snapshot · repo · restore (planner) · schedule ·
 │        modules (manifest types) · release · version · pathutil · mimetypes · contracts/
 └ desktop/ (Go module …/VaultDock/desktop)
     VaultDockAgent.exe  (Windows service, LocalSystem w/ restricted privileges)
       agent host · transfer core · backup engine · sync engine (upload + two-way) ·
       scheduler · keystore · profiles/consent/guard · module manager · updater
       └ components (downloaded per mode): vd-vss.exe · vd-discovery.exe · vd-reinstall.exe
     VaultDock.exe (Wails UI + tray, per user, no admin)  ⇄ named pipe (SDDL) ⇄ agent
     vaultdock-restore.exe (standalone, no WebView2)
```

**Process and privilege model** (D11, D24):

| Process | Account | Rights used | Talks to |
|---|---|---|---|
| `VaultDockAgent` service | LocalSystem, SCM-restricted privileges | SeTcb (S4U tokens), SeImpersonate, SeBackup (VSS only), SeChangeNotify. No SeRestore, SeDebug or SeTakeOwnership | Server (HTTPS), UI (pipe), components (pipe) |
| `VaultDock.exe` UI | Interactive user | None elevated | Agent via pipe; the caller's SID decides what it may see and do |
| `vd-vss.exe` (component) | Started by the agent as SYSTEM, short-lived | VSS requestor only (create/delete **own** shadows) | Agent pipe |
| `vd-discovery.exe` (component) | Started by the agent **under the target user's token** | User rights only | Agent pipe |
| `vd-reinstall.exe` (component) | Run **in the user's session under the user's token** (winget needs a user context) | Installer-level elevation only via the package manager's own UAC flow, after explicit consent | Agent pipe |

**Sync vs DR separation:**
- Sync = `nodes`/blobs, file versions and the change feed.
- DR = repository packs and snapshots.
- Shared: transfer core, rules engine, scanner, guard, profile tokens, identity, UI kit.

---

## 7. Subsystem designs

### 7.1 Repository split and migration (O9, DR7, D27)

**Target layout:**

| Repository | Path | Contains | Module(s) |
|---|---|---|---|
| `vad-server` | `D:/VaultDock/vad-server/` | `cmd/vaultdock`, `internal/**` (incl. the new objstore, retention, keys, catalog, backup, scheduler, secrets, ai), `web/**`, `deploy/**` (integrations, `env.schema.json`), `Dockerfile`, `docker-compose.yml`, `install.sh`, `.env.example`, `tests/integration/**`, server docs, `scripts/dev-server.ps1`, `.github/workflows/{ci,release}.yml`, `LICENSE`, `THIRD_PARTY_NOTICES.md`, `README.md` | `github.com/ASPEC-RACING-PTY-LTD/vad-server` |
| `VaultDock` | `D:/VaultDock/VaultDock/` | `sdk/**` (shared code + `sdk/contracts/`), `desktop/**`, `planning/**`, `MASTER_PLAN.md`, client docs, `assets/brand`, `scripts/{make_icons.py,sign.ps1}`, `.github/workflows/{ci,release}.yml`, `LICENSE`, `THIRD_PARTY_NOTICES.md`, `README.md` | `…/VaultDock/sdk`, `…/VaultDock/desktop` |

**Path mapping** (old → new):

| Old path | New path |
|---|---|
| `pkg/{protocol,client,automation,pathutil,mimetypes,release,version}` | `VaultDock/sdk/…` |
| `internal/*`, `cmd/*`, `web/*`, `deploy/*`, `tests/integration/*`, `Dockerfile`, `docker-compose.yml`, `install.sh`, `.env.example` | `vad-server/…` |
| `desktop/*` | stays in `VaultDock/desktop/*` |
| `docs/{deployment,operations,security,automations}.md` | `vad-server/docs/` |
| `docs/{desktop,development}.md` | `VaultDock/docs/` |
| `docs/architecture.md` | Split between the two repos |

**Migration procedure (Phase S, INT only, sequential; each step verified before the next):**
1. **Safety:**
   - Record `git rev-parse HEAD` and confirm the tree is clean except planning files.
   - Create `git bundle create D:/VaultDock/backups/vaultdock-pre-split-<date>.bundle --all`.
   - Tag `pre-split-r3` locally.
   - Zip copy of the working tree.
2. **Create `vad-server`:**
   - `ch clone` the local repo to `D:/VaultDock/vad-server`.
   - Run `git filter-repo --path cmd/ --path internal/ --path web/ --path deploy/ --path tests/integration/ --path Dockerfile --path docker-compose.yml --path install.sh --path .env.example --path .dockerignore --path scripts/dev-server.ps1 --path docs/ --path .github/` (history preserved for those paths).
   - Remove the origin remote; the owner creates the Codehold repo later.
   - `git-filter-repo` is an external tool run by INT (MIT-licensed tool, not incorporated). Its use needs owner approval (Q1).
3. **Move shared code into the SDK** (in `VaultDock`):
   - `git mv pkg sdk/<pkgs>`, `go mod init github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk`.
   - Rewrite imports in `sdk/` and `desktop/`.
   - Desktop `replace` → `../sdk`.
4. **Remove server paths from `VaultDock`** (one commit). Move `sdk/contracts` → `sdk/contracts`.
5. **In `vad-server`:**
   - delete `desktop/` and `pkg/` remnants;
   - `go mod edit -module github.com/ASPEC-RACING-PTY-LTD/vad-server` and rewrite imports;
   - require the SDK (`replace` to `../VaultDock/sdk` only through the untracked `D:/VaultDock/go.work`);
   - update the Dockerfile so it builds with the SDK fetched from the module proxy, plus a `vendor`-based fallback (`go mod vendor` in CI only).
6. **Workspace:** create the untracked `D:/VaultDock/go.work` listing `./vad-server`, `./VaultDock/sdk` and `./VaultDock/desktop`.
7. **Verify:**
   - `go build/vet/test ./...` in all three modules;
   - web build;
   - desktop build;
   - Docker image build;
   - `install.sh` dry run;
   - the integration tests from `vad-server` against a local server with the desktop client library.
8. **Commit locally** with `ch commit` in both repos (no push). Report to the owner, including:
   - the Codehold repo to create (`vad-server`);
   - the GitHub mirror target;
   - the `sdk/v0.1.0` tag to create **after** the owner pushes.
9. **Release/URL transition** (lands in Phase 0 tasks):
   - `vad-server/install.sh` points at `ASPEC-RACING-PTY-LTD/vad-server` releases;
   - the old repo gets an `install.sh` redirect stub (C21);
   - the server admin "Updates" page checks `vad-server` releases;
   - the desktop updater keeps checking `VaultDock` releases.

**Codehold and GitHub (DR7):**
- Codehold is `origin` for both repositories and mirrors pushes to public GitHub (`ASPEC-RACING-PTY-LTD/VaultDock` and `ASPEC-RACING-PTY-LTD/vad-server`).
- CI and releases run on GitHub Actions (public runners) from the mirrored tags:
  - the server image goes to GHCR;
  - the Windows installer, components, the restore tool, `SHA256SUMS` and the component manifest go to GitHub Releases.
- Secrets (optional signing certificate, release-manifest signing key) are GitHub Actions secrets.
- Codehold-side CI is optional and out of scope until the owner specifies it.
- Nothing is pushed by agents.

### 7.2 Encryption and key management (O1, D8, D9)

Normative definition: [backup-format-v1.md §3–§5](sdk/contracts/backup-format-v1.md) and [api-v1.md §5A](sdk/contracts/api-v1.md).

| Aspect | Design |
|---|---|
| Key material | `DK` (dedup, chunker, keyed file hash, config MAC), never rotated in v1. `RMK_e` per epoch → `K_data_e`, `K_meta_e` |
| Recipients | Device X25519 (`box_priv` machine-scope DPAPI in `%ProgramData%\VaultDock\keys`, ACL SYSTEM/Admins); server escrow SEK; recovery (asymmetric, derived from the 32-byte recovery secret); optional passphrase (Argon2id-derived) |
| Exchange at registration | The device sends `box_pub` at enrolment. On approval it receives and **pins** the SIK fingerprint (shown with the verification code on both screens) and the SIK-signed SEK pub. First device: creates `DK`/`RMK_1`, the wraps and the recovery key. Later devices: the server re-wraps under full escrow (in memory only), or an existing device or the recovery key does it under metadata escrow |
| Storage | Client: DPAPI machine scope plus a file ACL. Server: KEK file outside the DB (Docker secret or `/state/keys/master.key` 0600, created by `install.sh`); SIK/SEK private keys sealed by the KEK; catalog per-user DEKs sealed by the KEK; destination credentials and AI keys under the existing `internal/secrets` envelope with the KEK |
| Rotation | Epoch rotation on device revocation (automatic), escrow-mode change, SEK rotation, manual request, or the yearly policy. Backward chain; no re-encryption of old data by default. SIK rotation is signed by the old SIK, and devices accept it automatically only with a valid chain. SEK rotation re-wraps the current epoch |
| Recovery | (a) Server alive: a new device is approved, then key share. (b) Server gone, repository copy available: `vaultdock-restore` plus the recovery key or passphrase. (c) Recovery key lost, server alive, full escrow: the server can re-wrap to a new device after normal device approval. (d) Server KEK lost: escrow is unusable, the recovery key still works. **The KEK backup is a mandatory step in `install.sh` and is shown in the admin readiness view** |
| Server-side decryption audit | Every unwrap operation is logged (`key_audit`: repo, purpose, actor, time) and visible to the repository owner |
| Limitations | Go memory cannot be reliably zeroised. A server compromise under full escrow exposes data (C1). A revoked device keeps the epochs it already held |

### 7.3 Repository format

See the locked [backup-format-v1.md](sdk/contracts/backup-format-v1.md) (rev 1.1).

### 7.4 Storage (server-side only)

- `vad-server/internal/objstore`: interface + `localfs` (incl. host-mounted SMB/NFS) + `s3` (minio-go, Apache-2.0) with profiles for AWS, R2, B2, MinIO and Garage.
- Destinations, replication, verification, object lock and analytics as in r2, minus everything client-held.
- Default: the "server" destination = `localfs` under each storage location's `backup/` root.

### 7.5 Server (vad-server)

As r2 (catalog, jobs, runs, issues, device tasks, approvals, readiness, summary, operations, plan/execute), plus:
- **`internal/keys`**: KEK/SIK/SEK custody, enrolment exchange, escrow re-wrap, rotation, `key_audit`.
- **`internal/catalog`**: ingestion that decrypts trees with `K_meta`; encrypted columns (per-user DEK); HMAC blind index.
- **Modules and profiles** (`device_modules`, `device_profiles`): feature gating; superuser clear.
- **Two-way sync**: `nodes.revision`, a `sync_changes` feed with a tombstone window, folder ops, conflicts.
- **Deploy env management**: `env.schema.json` compiled into the binary; `/admin/deploy/env`; the approvals file in the state volume.
- **Superuser** (`users.is_superuser`).
- **AI**: in-house OpenAI-compatible client with an SSRF guard (HTTPS only; private ranges only with an admin setting; no redirects; 30 s timeout; response size cap).

### 7.6 Windows client: admin install, service, least privilege, system-file protection (O3, D11, D24)

**Installer** (NSIS, per-machine, `RequestExecutionLevel admin`):
1. Welcome.
2. Licence page (PolyForm NC text).
3. **Mode page** (R11, §7.7).
4. Install directory (default `%ProgramFiles%\VaultDock`, not changeable to system folders).
5. Install, which:
   - copies the core files;
   - creates `%ProgramData%\VaultDock` with ACL (SYSTEM full, Administrators full, Users read on `logs` only);
   - registers the `VaultDockAgent` service (delayed auto-start, failure restart, `RequiredPrivileges` allowlist, service SID type unrestricted);
   - adds an HKLM Run entry for the UI tray;
   - registers the toast AppUserModelID;
   - writes the selected mode to `%ProgramData%\VaultDock\config\mode.json`;
   - starts the service.
6. Finish, which launches the UI. The UI completes connect, enrolment and component download with progress.

Upgrade-in-place keeps `%ProgramData%`. The per-user install is migrated per C5.

**Uninstaller:**
- stops and deletes the service;
- removes only VaultDock's own shadow copies (by recorded id), components and program files;
- asks before deleting `%ProgramData%\VaultDock` and **never** touches user data or backups.

**Guard** (`desktop/internal/guard`), applied to every write, delete, rename, restore target, two-way sync root and registry import:
1. Canonicalise the path (`GetFinalPathNameByHandle`, reject `\\?\GLOBALROOT` and device paths, 8.3 names normalised).
2. Deny-list:
   - `%WINDIR%`, `%ProgramFiles%`, `%ProgramFiles(x86)%`;
   - `%ProgramData%\Microsoft`, `System Volume Information`, `$Recycle.Bin`;
   - `pagefile.sys`, `hiberfil.sys`, `swapfile.sys`;
   - boot files and the system volume root;
   - other users' profiles without consent;
   - `%ProgramData%\VaultDock` itself (except by the agent's own store code).
3. Never follow reparse points (`FILE_FLAG_OPEN_REPARSE_POINT`); refuse to traverse junctions out of the authorised root.
4. Never write files with the System attribute; never change ACLs; the owner is set implicitly by creating files under the user token.
5. Deletes go to the Recycle Bin via `IFileOperation` in the user's context; never a permanent delete.
6. Registry: only per-app HKCU keys from the catalogue, written in the user's context; HKLM never, except catalogued per-app keys with an explicit administrator confirmation in Advanced mode; never `SYSTEM`, `SAM`, `SECURITY`, `BCD`, or `Software\Microsoft\Windows*`.
7. Every block becomes a `guard_blocked` issue. No API or UI override exists.

**IPC:**
- Named pipe `\\.\pipe\VaultDock.Agent.v1` (go-winio, MIT), SDDL allowing SYSTEM, Administrators and Interactive Users.
- Each call is authorised by the caller SID (impersonation) and the profile consent state.
- Destructive or machine-wide calls need the Administrators group plus a UI confirmation.
- No network listener.

### 7.7 Install modes and module system (R11, D26)

**Modes (shown in the installer and in Settings → Mode):**

| | Simple Folder Backups | Full Access to All Tools (Advanced) |
|---|---|---|
| What it does | Pick folders, automatic scheduled backups, browse backed-up files, restore files | Everything in Simple, plus one-way and two-way sync, automations, app/game/dev-project discovery, app reinstall, registry/settings backup, VSS snapshots for locked files, multi-profile protection, advanced management screens |
| Modules | `core` | `core` + `sync` + `automation` + `profiles` (built-in, enabled) + `vss`, `discovery`, `reinstall` (downloaded) |
| Permissions | Admin rights to install the background service. Backups read only **your** profile, as you | Same, plus snapshot support (system shadow copies), installing apps through winget/choco/scoop with your confirmation, and protecting other Windows profiles only when those users consent |
| Resources | ~core size; low CPU/IO in the background; disk for the local upload queue | Plus about the size of the components; more CPU during discovery and VSS snapshots |
| Downloads | Core only | Core + components (from public GitHub releases, verified) |

**ⓘ info dialogs:**
- The installer mode page has two radio cards, each with a clickable ⓘ button (NSIS `nsDialogs` link/button that opens a modal page).
- The same texts appear in the UI (Settings → Mode, and the first-run screen).
- The texts are plain-language and cover functionality, scope, permissions, resources and differences.
- Single source of truth: `desktop/build/windows/installer/modes/{simple,advanced}.<lang>.txt`, embedded into both the installer and the UI at build time (REL-C owns the files, INT reviews the wording).

**Module system:**
- **`sdk/modules`**: `Manifest{ManifestVersion, Release, Components:[{ID, Version, MinCore, Modes, URL, SHA256, Size, Depends, Entry}], Signature?}` plus the module ids from api-v1 §6A.
- **`desktop/internal/modules`** (the agent's module manager):
  - persists the mode and enabled modules (`modules` table in `state.db`);
  - enables or disables built-ins;
  - downloads components from the release URL in the manifest over HTTPS;
  - verifies SHA-256, plus the Ed25519 manifest signature if adopted (D29) and the Authenticode signer when the core is signed (D29);
  - installs into `%ProgramFiles%\VaultDock\components\<id>\<version>`, keeping the previous version for rollback;
  - starts and supervises component processes;
  - reports `PUT /device/modules`.
- **UI gating**: the desktop frontend uses a module registry like the web one. Screens declare `requires: ['discovery']` and are hidden when a module is disabled. Hidden screens' data stays intact.
- **Release pipeline**: each release publishes `VaultDock-<v>-setup.exe` (core), `vd-vss-<v>.zip`, `vd-discovery-<v>.zip`, `vd-reinstall-<v>.zip`, `vaultdock-restore-<v>.exe`, `components-<v>.json` and `SHA256SUMS` (plus the optional offline full installer, C16).

**Switching modes** (Settings → Mode; requires a Windows administrator confirmation through UAC on the UI's request; the agent performs it):
- **Simple → Advanced:**
  1. download and verify the components;
  2. enable the built-ins;
  3. report modules;
  4. show the new screens.
  Existing jobs and backups are untouched.
- **Advanced → Simple:**
  1. preview the affected items (two-way folders, app/registry jobs, automations, other profiles);
  2. ask for confirmation;
  3. disable the modules;
  4. set affected jobs and folders to `suspended_by_mode` (never deleted);
  5. two-way folders stop **without** deleting anything;
  6. optionally uninstall component binaries (default: keep them for 30 days for a quick switch back);
  7. report modules.
  Backups made in Advanced remain browsable and restorable for files; app reinstall needs Advanced again.
- **Server mode request:** `POST /devices/{id}/mode-request` creates a task that the local administrator must confirm.

**Server support:**
- The server stores `install_mode` and the reported modules and computes each job's `required_modules`.
- It refuses unsupported jobs and tasks with `409 module_unavailable` and filters automations by device modules and version.
- The web UI shows a mode badge per device and disables Advanced-only actions with "Requires Advanced mode on this device" plus a "Request mode change" button.

### 7.8 Multi-user profiles, consent, blacklist (O6, D25)

- **Default:** at first run the interactive user who completes setup gets `active_default` consent for their profile (recorded, with a proof).
- **Consent:**
  - The user signs in to Windows, opens VaultDock, and clicks "Allow VaultDock to protect my profile" (sees the scope and the account choice, C10).
  - The UI creates a consent blob `{sid, device_id, consent_id, time, scope}` protected with **user-scope DPAPI** in the user's session and sends it to the agent.
  - The agent stores it and reports `proof_digest` to the server.
- **Admin requests:** an admin (web, or local administrator in the UI) requests selected or all profiles. Each target user sees a consent prompt at their next VaultDock launch. Nothing is read before they consent.
- **Access:**
  - while the user is logged on, the agent uses their session token (`WTSQueryUserToken`);
  - otherwise it uses an **S4U** logon token for that SID (no password; SeTcbPrivilege).
  - S4U tokens cannot access the user's DPAPI or EFS data, so such files are skipped with an issue until the user is logged on.
- **Security-change detection** (best effort, C8). Checked at service start, every 6 h, at each user logon (session notification) and before each job:
  - local account `usri4_password_age` reset → `password_changed`;
  - consent blob fails to unprotect in the user's session → `password_reset` / `consent_unverifiable`;
  - SID lookup mismatch → `sid_changed`;
  - account disabled or removed;
  - profile path changed;
  - domain `pwdLastSet` (if reachable);
  - Security events 4723/4724 (if readable and auditing is enabled).
- **Blacklist:**
  1. the agent stops all work for the profile immediately;
  2. reports `POST /device/profiles/{sid}/security-event`;
  3. the server marks the profile `blacklisted`, suspends jobs and notifies superusers.
  The agent ignores the profile until the server state is `cleared_pending_consent` **and** the user consents locally again.
- **Never:** store passwords, prompt for other users' passwords, load unloaded hives, or bypass ACLs (C7).

### 7.9 Backup engine, sources, VSS, restore

As r2, except:
- the engine runs in the service, reading **through profile tokens**;
- `vss`, `discovery` and `reinstall` are components;
- restore writes as the target user through the guard;
- restore targets must be `authorised` profiles.

Simple mode backs up folders of the active profile (and other authorised profiles only if `profiles` is enabled, i.e. Advanced).

### 7.10 Sync: one-way and two-way (O2, D12)

- **Per-folder config:** `direction` `upload` (default) or `two_way`; conflict policy `keep_both` (default), `newer_wins`, `local_wins`, `remote_wins` or `ask`; delete policy; mass-delete threshold; ruleset; schedule. Folders can be authored in the client or the web. The server is the desired-state store and the device acknowledges revisions.
- **Client state:** a per-folder 3-way base `{path, file_id, base_revision, base_sha256, local_size, local_mtime}` in `state.db`.
- **Reconcile loop** (two-way):
  1. pull the change feed;
  2. scan local changes;
  3. classify each path (local-only change → upload with `base_revision`; remote-only change → download to temp, verify SHA-256, guard check, atomic replace under the user token, with the previous local copy going to the Recycle Bin; both changed → policy);
  4. deletes: remote delete → local Recycle Bin; local delete → server trash via folder ops;
  5. moves are detected by file id or hash.
- **Safeguards:**
  - echo suppression of the agent's own writes;
  - case-insensitive collision handling;
  - invalid names;
  - long paths;
  - placeholder skip;
  - the mass-delete guard pauses and asks;
  - two-way never on guarded paths;
  - enabling two-way requires an approval plus a first-reconcile preview.
- **One-way upload** keeps today's engine behaviour (B-fixes applied) and gains base-hash conflict detection and server version history.

### 7.11 Automations and AI (O8)

- Automations as r2 (registry refactor, v2, approval gates, new kinds). Kinds declare `required_modules`, and the server filters delivery.
- **AI:**
  - user-configured `base_url`, `model` and API key (encrypted at rest);
  - the server calls the OpenAI-compatible `/chat/completions` endpoint;
  - redaction (paths and names tokenised);
  - read-only and propose tools only;
  - proposals become approvals;
  - SSRF guard (C-series, api-v1 §11);
  - audit log.
  - No provider SDKs.

### 7.12 Web (vad-server/web)

As r2 (WEB-0 foundation, registry, feature folders), plus:
- device mode and module badges, and gating;
- profiles panel (consent states, admin requests, superuser clear with type-to-confirm);
- key status (epoch, recipients, recovery-wrapped flag, escrow mode, rotate);
- snapshot download and preview under full escrow;
- sync folder editor with direction, policies and two-way enable flow;
- conflicts;
- admin "Deployment → Environment" page (DR5);
- AI settings (URL, model, key).

### 7.13 Deployment: Compose, install.sh, environment, integrations (DR1–DR6, D28)

**Compose:**
- Only the `vaultdock` service, hardened as today.
- Proxies and tunnels are opt-in files under `deploy/integrations/` (`caddy/compose.caddy.yml`, `caddy/Caddyfile`, `nginx/vaultdock.conf.example`, `traefik/labels.example`, `cloudflared/README.md`).
- None is included by default.

**`env.schema.json`:** `[{name, default, required, secret, since, deprecated_since, replacement, description}]`. It is the single source for `.env.example`, `install.sh` merging and `/admin/deploy/env`.

**`install.sh` menu:**
1. Install
2. Upgrade
3. Integrations (configure/change)
4. Environment review
5. Automatic updates
6. Uninstall

**Upgrade algorithm:**
1. **Preflight:** Docker and Compose versions, disk space, current version, health.
2. **Backup:** DB (`VACUUM INTO` through the container CLI), `.env`, compose and override files, integration configs it manages, KEK file presence check (never copied off-host automatically). Stored in `backups/<timestamp>/`.
3. **Managed files:**
   - Only files listed in `deploy/managed-files.json` are updated.
   - A file whose checksum differs from the last shipped version (user-modified) is left in place, the new version is written as `<file>.new`, and the user is warned.
4. **`.env` merge:**
   - Add missing variables with defaults (and generated secrets where marked), appended under a dated comment block.
   - **Never change existing values.**
   - Obsolete variables are listed and removed only after interactive per-variable confirmation or an approval file from the admin UI (C19); otherwise they are kept.
5. **Images:** pull or build only when the target version or digest differs; `docker compose up -d` recreates only changed services. Volumes and bind mounts are never removed.
6. **Health check**, then on failure **automatic rollback** (previous image tag, restored files, restored DB copy if a migration ran).
7. **Report.**

**Integrations step** (install or upgrade; repeatable; non-destructive):
- **Cloudflare Tunnel:**
  - Detect `systemctl list-units 'cloudflared*'` / `/etc/cloudflared/config.yml` and `docker ps` containers from the `cloudflare/cloudflared` image.
  - If a locally managed config exists: back up, add or verify the ingress rule `hostname → http://127.0.0.1:<port>`, run `cloudflared tunnel ingress validate`, then reload, restoring the backup on failure.
  - Token or dashboard-managed tunnels: print exact steps and verify through an HTTPS probe.
  - If none exists: offer instructions to install cloudflared **outside** the VaultDock Compose project (never bundled).
- **Reverse proxy:** detect Caddy, Nginx or Traefik on the host and generate a config snippet from `deploy/integrations/*`. Validate (`caddy validate`, `nginx -t`) before any reload, but only when the user asks the script to apply it.
- **Built-in TLS:** self-signed or a provided certificate (unchanged).
- Bind to `127.0.0.1` when fronted by a tunnel or proxy.

**Existing installs:** the old Caddy profile is migrated (C20); the `set_env` overwrite is fixed (B15).

### 7.14 Code signing and release integrity (O4, D29)

- `scripts/sign.ps1` and release-workflow steps run **only if** the `WINDOWS_SIGNING_CERT` secret (or a cloud signing configuration) exists. Otherwise they are skipped with a notice.
- The updater and the module manager:
  - always verify SHA-256 from `SHA256SUMS` / the component manifest;
  - if the installed core is Authenticode-signed, require the same publisher on updates and components;
  - if D29's Ed25519 manifest signing is approved, verify the manifest signature with the embedded public key (the key is kept offline or in GitHub secrets).
- The NSIS installer and the uninstaller are signed when a certificate exists.

### 7.15 Licensing and third-party code (O5, O7, D30)

- **`LICENSE`** in both repositories: the verbatim PolyForm Noncommercial 1.0.0 text, fetched from polyformproject.org by task P0-31, plus `Required Notice: Copyright © ASPEC Racing Pty Ltd` (to be confirmed, Q10).
- **README "Licence" section:** noncommercial use permitted; commercial use requires a separate licence; contact.
- **Allowlist:** MIT (preferred), BSD-2/3, ISC, Apache-2.0, Zlib, CC0, Unlicense. MPL-2.0 needs owner approval per case. GPL/LGPL/AGPL/SSPL and custom/non-commercial code licences are **forbidden** for incorporated code.
- Enforced by `go-licenses` (Go) and `license-checker` (npm) in CI with an allowlist config. **Independent verification** = task P0-31 reads each dependency's LICENSE file at the pinned version (not just metadata) and records SPDX, URL, commit/version and the contribution.
- **`THIRD_PARTY_NOTICES.md`** per repository: the full licence texts and notices required by BSD/Apache.
- **README "Credits"**: only incorporated repositories, each with its specific contribution (e.g. "klauspost/compress: Zstandard compression of backup data"). Research-only projects (restic, kopia, borg, Syncthing, ludusavi, etc.) are **not** listed.
- **Current dependencies** to be verified by P0-31 include go-chi, modernc sqlite, the x/* packages, maxminddb-golang, go-humanize, go-isatty, go-strftime, Wails, rjeczalik/notify, `git.sr.ht/~jackmordaunt/go-toast` (licence to verify), React, React Router, TanStack Query, xyflow, lucide and Tailwind.

---

## 8. Database and state migrations

### 8.1 vad-server (`internal/store/migrations.go`, owner SRV-0; all landed in Phase 0)

| Mig | Tables / changes |
|---|---|
| 2 | `secrets`, `destinations` (server-held only), `approvals` (+ kinds `escrow_change`, `two_way_enable`), `operations`, `plans`, `devices.can_delete` (DEFAULT 1), `users.is_superuser` (first admin = 1) |
| 3 | `repositories` (`escrow_mode`, `current_epoch`), `repo_objects`, `snapshots` (`epoch`, `profile_sid`, `ingest_state`), `snapshot_packs`, `snapshot_files` (encrypted `path_ct`, `name_ct`, `blind_tokens`), `catalog_keys` (user_id, dek_wrapped_by_kek, created_at, rotated_at) |
| 4 | `backup_jobs` (`profile_sid`, `required_modules`, `state`, `revision`), `job_runs`, `run_issues`, `device_tasks`, `app_inventory`, `restores`, `restore_items`, `device_backup_state` |
| 5 | `retention_policies`, `prune_plans`, `repo_locks`, `schedules` |
| 6 | `file_versions`, `sync_folders` (`direction`, policies, `revision`, `acked_revision`, `state`, `profile_sid`), `sync_conflicts`, `sync_changes` (user_id, seq, node_id, op, path, old_path, revision, sha256, size, modified_at, device_id, created_at), `nodes.revision` (DEFAULT 1) |
| 7 | `replication_policies`, `replication_state`, `notification_prefs`, `ai_settings` (base_url, model, secret_id, scopes, enabled), `ai_audit`, `users.backup_quota_bytes`, `automations.schema_version`, `automations.runtime` |
| 8 | `server_keys` (kind `sik|sek`, version, public, private_sealed_by_kek, created_at, retired_at), `key_audit`, `device_keys` (device_id, box_pub, sik_pinned_fingerprint), `device_modules` (device_id, install_mode, core_version, modules JSON, updated_at), `device_profiles` (api-v1 §6B), `profile_requests` |

Rule: a column for every persisted field in api-v1. SRV-0 lists the mapping in its report. `migrate_test.go` covers v1 → v8.

### 8.2 Windows client

- `state.db`: moves to `%ProgramData%\VaultDock\state` (migrated from per-user Roaming by the installer, C5). Gains `schema_version` with migrations: `issues`, `outbox`, `schedule_last`, `modules`, `profiles` (consent records, consent blob ciphertext, last security check), and `sync_base` (3-way base per folder).
- `backup.db` (new): as r2, plus `epoch` on packs and runs.
- Key store: `%ProgramData%\VaultDock\keys\` (box key, pinned SIK).

---

## 9. API

The canonical, locked definition is [sdk/contracts/api-v1.md](sdk/contracts/api-v1.md) rev 1.1. Owners per section:

| api-v1 section | Owner |
|---|---|
| §1 conventions, flags, summary/operations | SRV-0 |
| §4 destinations | SRV-1 |
| §5A keys | SRV-K |
| §5 repos/objects/snapshots | SRV-2 |
| §5.4 catalog ingestion/search | SRV-C |
| §6 jobs/tasks/inventory | SRV-3 |
| §6A modules, §6B profiles, §11B superuser | SRV-M |
| §7 restore, §8 approvals | SRV-3 |
| §9 retention/GC/verify/replication | SRV-4, SRV-6 |
| §10 sync | SRV-5 |
| §11 readiness/notifications/AI | SRV-7 |
| §11A deploy env | SRV-0 (API) + DEP (`install.sh`) |

---

## 10. Security model (deltas from r2)

| Concern | Measure |
|---|---|
| Key confidentiality | Wraps only (sealed boxes); SIK pinning; KEK outside the DB; `key_audit`; escrow mode choice (C1) |
| Server at rest | Catalog DEK encryption; secrets envelope; S3 SSE where supported (C2) |
| Least privilege | Restricted LocalSystem privileges; user-token data access; component processes with minimal rights; no network listener (C6, C7) |
| OS damage | Guard (§7.6), Recycle Bin deletes, no ACL/SID changes, no HKLM by default, own-shadow-only deletion, uninstaller never deletes user data |
| Multi-user | Explicit per-user consent; DPAPI-bound proof; blacklist plus superuser clear plus re-consent; never passwords (C8–C10) |
| Supply chain | Licence allowlist CI; SHA-256 manifests; optional Authenticode; recommended Ed25519 manifest signing (C14) |
| AI | SSRF guard; redaction; proposals only; audit |
| Deployment | Add-only env; approvals for removals; backups and rollback; validation before reloading proxies/tunnels |

---

## 11. Testing strategy

As r2 (T-F1 format vectors, T-S1 migrations, T-S2 GC safety, T-S3 hardening, T-A1 automation, T-D1 desktop DBs, T-D2 admin Windows tests, T-W1 web, T-I1 integration, T-R1–T-R4 VM restore and server-independent restore). Additions:

| ID | Test | Owner |
|---|---|---|
| T-K1 | Key exchange: first device, second device under full escrow, second device under metadata escrow, rotation on revocation, recovery-key-only restore, SIK mismatch refusal | SRV-K, SDK-REPO, DSK-D |
| T-K2 | KEK loss: escrow unusable, recovery key works; `key_audit` entries for every server unwrap | SRV-K |
| T-M1 | Install modes: fresh Simple install (only core present), switch to Advanced (components verified + installed), switch back (jobs suspended, nothing deleted), server gating returns `module_unavailable` | DSK-MOD, SRV-M, REL-C |
| T-M2 | Tampered component zip / manifest → refused; rollback to the previous component version | DSK-MOD |
| T-P1 | Profiles: active default, admin request then consent by user B, password reset → blacklist, superuser clear, re-consent; S4U access denied to another user's private file → skipped with an issue | DSK-P, SRV-M |
| T-G1 | Guard: restore/sync attempts into `%WINDIR%`, Program Files, another profile, through a junction → all blocked and recorded | DSK-P |
| T-Y1 | Two-way sync: concurrent edits (each policy), remote delete → Recycle Bin, mass-delete guard, rename/move, case collision, offline beyond the tombstone window → reset | DSK-H, SRV-5 |
| T-E1 | `install.sh`: fresh install; upgrade with an untouched `.env` (values unchanged, missing added); obsolete var kept without approval and removed with approval; user-modified managed file → `.new`; failed health → rollback; Caddy-profile migration; cloudflared detection (systemd, docker, none) in containers or VMs | DEP |
| T-L1 | Licence CI fails on a disallowed licence; THIRD_PARTY_NOTICES complete | LIC, REL |
| T-S4 | Repository split: all modules build and test; Docker build with the SDK from proxy and vendor; integration tests pass across repos | INT (Phase S) |

---

## 12. Phases

| Phase | Goal | Workstreams | Exit criteria |
|---|---|---|---|
| **S: Repository split** | `vad-server` + `VaultDock` (SDK + desktop) with history; nothing else changes | INT only (needs approval Q1) | T-S4 green; existing behaviour unchanged; local commits only |
| **0: Foundations & hardening** | Hotspots removed, schema v8, wire types, defect fixes, licence/notice baseline, deploy env schema + `install.sh` add-only merge + Caddy de-bundling + repo URL transition, agent service host + IPC + module registry skeleton, spikes | SRV-0, SRV-8, SRV-5 (B2), SDK-DEPS, SDK-WIRE, SDK-AUTO, WEB-0, DSK-A, DSK-B, DSK-H, DSK-I (delegation), DSK-MOD (skeleton), REL-C, REL-S, LIC, DEP, SPIKE | B1–B16 fixed; migrations 2–8 tested; `sdk/wire` matches api-v1; agent runs as a service in a dev VM with the UI over the pipe; LICENSE files and licence CI in place |
| **1: Simple-mode backup MVP** | Folder backups of the active profile, shared-key encryption, escrow, catalog ingestion, admin installer with mode page (Simple functional; Advanced installs core + enables built-ins) | SDK-REPO, SDK-SCHED, SDK-CLIENT, SRV-ST, SRV-K, SRV-2, SRV-C, SRV-3, SRV-7, SRV-M, DSK-C, DSK-D, DSK-P (core), DSK-MOD, DSK-I, REL-C (installer), WEB-1, WEB-6, WEB-7, SRV-ITEST | T-I1, T-K1 (full escrow), T-M1 (Simple install) pass; format vectors frozen |
| **2: Restore (Simple complete)** | File restore, guard, standalone tool, web browse/download | SDK-RESTORE, DSK-G, DSK-P (guard), DSK-I, WEB-2, SRV-2, SRV-3, SRV-ITEST | T-R3, T-G1 pass; **Simple mode feature-complete** |
| **3: Advanced Windows depth** | Components `vss`/`discovery`/`reinstall` (download pipeline), multi-profile consent + blacklist + detection, app/registry restore, mode switching | DSK-F, DSK-E, DSK-R, DSK-MOD, DSK-P, DSK-G, DSK-I, SRV-M, WEB-1, WEB-7, REL-C | T-R1, T-R2, T-D2, T-M1 (switching), T-M2, T-P1 pass |
| **4: Server storage & lifecycle** | S3/R2/B2 destinations, retention/GC/compaction, replication, verify, analytics, key rotation + escrow-mode change, DB auto-backup, deployment integrations manager | SRV-ST (s3), SRV-1, SRV-4, SRV-6, SRV-K, SDK-KINDS, WEB-3, WEB-AUTO, DEP | T-F2, T-S2, T-K2, T-E1 pass |
| **5: Sync one-way + two-way** | Versions, change feed, conflicts, per-folder config, two-way engine | SRV-5, SDK-CLIENT (upload), DSK-H, DSK-I, WEB-4, WEB-5 | T-Y1 passes; existing sync tests green |
| **6: AI, notifications, release polish** | OpenAI-compatible AI, notifications, signing hooks finalised, README credits/notices final, offline installer (if Q13) | SRV-7, SDK-AI, WEB-6, DSK-I, REL-C, REL-S, LIC | AI SSRF and redaction tests; licence CI green; release dry run |

Concurrency at the widest point: S = 1 (INT), 0 ≈ 22, 1 ≈ 12, 2 ≈ 6, 3 ≈ 9, 4 ≈ 9, 5 ≈ 6, 6 ≈ 5. Task counts: Phase 0 = 38, Phase 1 = 36 (§18); later phases ≈ 16 / 26 / 22 / 18 / 12. **≈170 sub-agent tasks in total.**

---

## 13. Workstream ownership (exclusive files, post-split paths)

**Rules:**
1. One owner per path at a time.
2. New paths are created only by their owner.
3. Hand-offs are sequential (§13.6).
4. Dependency owners:
   - `VaultDock/sdk/go.mod` → **SDK-DEPS**
   - `VaultDock/desktop/go.mod` → **DSK-A**
   - `vad-server/go.mod` → **SRV-DEPS**
   - `vad-server/web/package.json` → **WEB-0**
   - `VaultDock/desktop/frontend/package.json` → **DSK-I**
   Only INT runs `go mod tidy`.
5. `vad-server/internal/api/api_test.go` and `vad-server/tests/integration/integration_test.go` are frozen.
6. **INT** owns:
   - `MASTER_PLAN.md`, `planning/**` (pre-split), `VaultDock/sdk/contracts/**` (post-split);
   - both `README.md` files, `VaultDock/docs/**`, `vad-server/docs/**`;
   - `D:/VaultDock/go.work`, Phase S, the review and the commits.
   SPIKE tasks write only under `VaultDock/planning/spikes/<name>/`.
7. Every task writes one `SUBAGENT-<TASKID>-<title>.md` in the `VaultDock` root.

### 13.1 SDK (`VaultDock/sdk`)

| WS | Phases | Modifies | Creates |
|---|---|---|---|
| SDK-DEPS | 0 | `sdk/go.mod`, `sdk/go.sum` | — |
| SDK-WIRE | 0 | — | `sdk/wire/**` |
| SDK-CLIENT | 1, 5 | `sdk/client/client.go`, `client_test.go`, `sdk/protocol/{protocol,protocol_test}.go`; `sdk/client/upload.go` (Phase 5 only) | `sdk/client/{capabilities,repo,jobs,tasks,keys,modules,profiles,sync,download,repo_test}.go` |
| SDK-AUTO | 0 | `sdk/automation/{schema,catalog,engine,validate,reference,automation_test}.go` | `sdk/automation/{registry,ops,migrate,approval,runtime}.go` + tests/golden |
| SDK-KINDS | 4 | — | `sdk/automation/kinds_*.go`, `templates_backup.go` |
| SDK-AI | 6 | — | `sdk/automation/aitools{,_test}.go` |
| SDK-SCHED | 1 | — | `sdk/schedule/**` |
| SDK-REPO | 1 | — | `sdk/{vdcrypt,compress,chunker,pack,snapshot,repo}/**` (incl. `sdk/repo/testdata/v1/**`, `sdk/repo/backend/server/**`) |
| SDK-RESTORE | 2 | — | `sdk/restore/**` |
| SDK-MOD | 0 | — | `sdk/modules/**` |
| REL-C (shared release code) | 0, 3 | `sdk/release/**`, `sdk/version/**` | `sdk/release/manifest_sig.go` (if D29 is approved) |

### 13.2 Server (`vad-server`)

| WS | Phases | Modifies | Creates |
|---|---|---|---|
| SRV-DEPS | 0, 1, 4 | `go.mod`, `go.sum` | — |
| SRV-0 | 0 | `internal/api/{server,settings,devices}.go`, `internal/store/{migrations,store}.go`, `internal/config/config.go`, `cmd/vaultdock/main.go`, `internal/testserver/testserver.go` | `internal/store/migrate_test.go`, stub route files (handed over), `internal/api/{summary,operations,deploy_env}.go`, `internal/store/{operations,summary,plans}.go`, `internal/deployenv/**` (schema embed + approvals file) |
| SRV-8 | 0 | `internal/api/uploads.go`, `internal/storage/storage.go`, `internal/api/admin.go`, `internal/store/{uploads,users}.go` | storage/auth/store unit tests |
| SRV-5 | 0 (B2), 5 | `internal/api/files.go`, `internal/store/{commit,nodes}.go`; `internal/api/uploads.go` + `internal/store/uploads.go` after hand-off | `internal/store/{versions,sync_folders,sync_changes,conflicts}.go`, `internal/api/{versions,sync_folders,sync_changes,conflicts}.go` + tests |
| SRV-ST | 1, 4 | — | `internal/objstore/**` |
| SRV-K | 1, 4 | `internal/api/devices.go` (enrolment fields, after hand-off from SRV-0) | `internal/keys/**`, `internal/store/{server_keys,device_keys,key_audit}.go`, `internal/api/keys{,_test}.go` |
| SRV-1 | 4 | — | `internal/secrets/**`, `internal/store/destinations.go`, `internal/api/destinations.go` + tests |
| SRV-2 | 1, 2 | — | `internal/backup/{repo,locks}.go`, `internal/store/{repos,snapshots}.go`, `internal/api/{backup_objects,snapshots,snapshot_download}.go` + tests |
| SRV-C | 1 | — | `internal/catalog/**` (ingest, DEK, blind index), `internal/store/catalog.go` + tests |
| SRV-3 | 1, 2 | — | `internal/store/{backup_jobs,device_tasks,approvals,inventory,restores}.go`, `internal/api/{backup_jobs,device_tasks,approvals,restore}.go` + tests |
| SRV-M | 1, 3 | — | `internal/store/{device_modules,device_profiles,superuser}.go`, `internal/api/{modules,profiles,superuser}.go` + tests |
| SRV-4 | 4 | — | `internal/scheduler/**`, `internal/retention/**`, `internal/backup/{gc,compact,dbbackup}.go`, `internal/store/{retention,prune,schedules}.go`, `internal/api/prune.go` + tests |
| SRV-6 | 4 | — | `internal/backup/{verify,replicate,analytics}.go`, `internal/store/replication.go`, `internal/api/{replication,analytics}.go` + tests |
| SRV-7 | 1, 6 | — | `internal/api/{readiness,notify_prefs,ai}.go`, `internal/store/{readiness,notify_prefs,ai_settings}.go`, `internal/ai/**` + tests |
| SRV-ITEST | 1+ | — | `tests/integration/{backup,keys,restore,sync2,modules}_test.go` |
| WEB-0 … WEB-7, WEB-AUTO | 0–6 | As r2 under `web/`. **WEB-7** (new) owns `web/src/pages/Devices.tsx` and `web/src/features/{devices,profiles,keys,deploy}/**` | |
| DEP | 0, 4 | `install.sh`, `docker-compose.yml`, `.env.example`, `deploy/Caddyfile` (moved) | `deploy/env.schema.json`, `deploy/managed-files.json`, `deploy/integrations/**`, `scripts/test-install/**` (container/VM harness) |
| REL-S | 0, 6 | `Dockerfile`, `.dockerignore`, `.github/workflows/{ci,release}.yml` | — |
| LIC | 0, 6 | — | `LICENSE`, `THIRD_PARTY_NOTICES.md`, `.licenses/allowlist.*`, `planning/licences/*` (drafts, in VaultDock) |

### 13.3 Client (`VaultDock/desktop`)

| WS | Phases | Modifies | Creates |
|---|---|---|---|
| DSK-A Agent service + IPC | 0, 3 | `desktop/main.go`, `desktop/go.mod`, `go.sum`, `desktop/internal/platform/**` | `desktop/cmd/vaultdock-agent/**`, `desktop/internal/{agent,ipc,service}/**` |
| DSK-B Transfer core | 0 | `desktop/internal/engine/{engine,transfer,concurrency,concurrency_test,report,engine_test}.go` | `desktop/internal/{transfer,outbox}/**` |
| DSK-H Sync + state | 0, 5 | `desktop/internal/state/state.go`, `desktop/internal/config/config.go`, `desktop/internal/filter/**`, `desktop/internal/engine/{scan,watch,automations,fileinfo_*}.go` | `desktop/internal/{dbutil,rules,sync2}/**`, `desktop/internal/state/{migrate,state_test}.go` |
| DSK-C Backup engine | 1 | — | `desktop/internal/{backup,scheduler}/**` |
| DSK-D Keystore | 1, 4 | — | `desktop/internal/keystore/**` |
| DSK-P Profiles, consent, tokens, guard | 1–3 | — | `desktop/internal/{profiles,guard,tokens}/**` |
| DSK-MOD Module manager | 0, 1, 3 | — | `desktop/internal/modules/**` |
| DSK-E Discovery component | 3 | — | `desktop/cmd/vd-discovery/**`, `desktop/internal/sources/**` |
| DSK-F VSS component + governor | 3 | — | `desktop/cmd/vd-vss/**`, `desktop/internal/{vss,privio,governor}/**` |
| DSK-R Reinstall component | 3 | — | `desktop/cmd/vd-reinstall/**`, `desktop/internal/reinstall/**` |
| DSK-G Restore engine + tool | 2, 3 | — | `desktop/internal/restore/**`, `desktop/cmd/vaultdock-restore/**` |
| DSK-I UI | 0–6 | `desktop/app.go`, `desktop/frontend/src/**` (existing files), `desktop/frontend/package.json`, `desktop/frontend/wailsjs/**` | `desktop/app_*.go`, new screens, `desktop/frontend/src/{modules,api}/**` |
| REL-C Installer, updater, signing, client CI | 0–6 | `desktop/build/windows/installer/**`, `desktop/wails.json`, `desktop/internal/updater/**`, `desktop/app_update.go`, `desktop/notify.go`, `desktop/logging.go`, `.github/workflows/{ci,release}.yml`, `scripts/make_icons.py` | `desktop/build/windows/installer/{modes/**,service.nsh,migrate-peruser.nsh}`, `scripts/sign.ps1`, `scripts/vm/**` |

### 13.4 Unowned (no change planned)

- **vad-server:** `internal/api/{auth_handlers,automations,misc,updates*,web}.go`, `internal/store/{devices,misc}.go`, `internal/auth/auth.go`, `internal/storage/disk_*.go`, `web/embed.go`.
- *`updates.go` changes its release repo string in Phase S (INT).*

### 13.5 Interface contracts agreed in Phase 0

- `agent.API` + IPC protocol v1 (DSK-A)
- `NewApp(log, api agent.API)` (DSK-I)
- `transfer.Core` (DSK-B)
- `modules.Manager` + `sdk/modules.Manifest` (DSK-MOD / SDK-MOD)
- `guard.Check(op, path, profile)` (DSK-P, stub in Phase 0 by DSK-P task P0-36)
- `tokens.ForProfile(sid)` (DSK-P)
- `sdk/repo.Backend` (SDK-REPO)
- stub routes (SRV-0)
- `FeatureModule` (WEB-0)
- `env.schema.json` format (DEP)

### 13.6 Hand-offs

| Files | From → To | When |
|---|---|---|
| `vad-server/internal/api/devices.go` | SRV-0 → SRV-K | After P0-03 |
| `vad-server/internal/api/uploads.go`, `internal/store/uploads.go` | SRV-8 → SRV-5 | Before Phase 5 |
| `VaultDock/sdk/client/upload.go` | frozen → SDK-CLIENT | Phase 5 |
| Stub route files | SRV-0 → feature owner | At creation |
| `web/src/pages/{Files,Sync}.tsx`, `components/files/**` | WEB-0 → WEB-5 / WEB-4 | After the splits |

---

## 14. Dependencies and licences

Verification of everything below is task P0-31 (C12, C13). The "Credit?" column says whether the item would appear in README Credits.

| Dependency | Licence | MIT? | Where | Credit? |
|---|---|---|---|---|
| golang.org/x/crypto, x/sys, x/time | BSD-3-Clause | N (no MIT equivalent) | SDK, server, client | Yes |
| klauspost/compress (zstd) | BSD-3-Clause | N (no comparable MIT) | SDK | Yes |
| minio/minio-go/v7 | Apache-2.0 | N (no comparable MIT) | Server (Phase 4) | Yes |
| microsoft/go-winio | MIT | Y | Client | Yes |
| go-ole/go-ole | MIT | Y | Client (`vd-vss`) | Yes |
| andygrunwald/vdf | MIT | Y | Client (`vd-discovery`) | Yes |
| gentlemanautomaton/volmgmt | MIT | Y | Client (USN) | Yes, if adopted |
| wailsapp/wails, rjeczalik/notify | MIT | Y | Client (existing) | Yes |
| go-chi/chi, modernc.org/sqlite, maxminddb-golang, etc. (existing) | MIT / BSD-3 / ISC (to verify) | mixed | Server | Yes |
| React, React Router, TanStack Query, xyflow, lucide, Tailwind (existing) | MIT / ISC (to verify) | mostly | Web, desktop UI | Yes |
| git.sr.ht/~jackmordaunt/go-toast | **to verify** | ? | Client (existing) | If allowed |
| ~~mxk/go-vss~~ | MPL-2.0 | — | dropped (C12) | — |
| ~~anthropic-sdk-go, openai-go~~ | — | — | dropped (D19) | — |
| Runtime tools (not incorporated): winget, choco, scoop, git, cloudflared | various | — | invoked or detected only | No (C13) |
| Data fetched at runtime: ludusavi manifest (PCGamingWiki CC BY-NC-SA 3.0) | — | — | Advanced `discovery` | No; in-app attribution |
| Research only (never credited): restic, kopia, borg, duplicacy, Syncthing, rclone, gocloud, USMT, Velociraptor, etc. | — | — | — | No |

---

## 15. Risks and honest limitations

All r2 risks remain (format ownership, index memory, SQLite single writer, AV heuristics, WebView2, Go COM vtables). Added or changed:

| # | Risk / limitation | Mitigation |
|---|---|---|
| R-K1 | Server compromise under full escrow exposes backups (C1) | Metadata escrow option; KEK outside the DB; audit; hardened container |
| R-K2 | Loss of both the KEK and the recovery key = data loss | KEK backup step in `install.sh`; recovery-key confirmation; readiness check |
| R-P1 | Password changes on Microsoft/Entra accounts may go undetected (C8) | DPAPI and sign-in signals; documented |
| R-P2 | S4U tokens cannot read DPAPI/EFS-protected user data while logged off | Skipped with an issue; retried at the next logon |
| R-M1 | Component download needs GitHub access; corporate proxies may block it | Proxy honouring (WinHTTP settings); optional offline installer (C16) |
| R-M2 | Mode downgrade confusion (suspended jobs) | Preview + clear UI state + readiness entries |
| R-Y1 | Two-way sync can propagate deletions or corruption | Recycle Bin, server versions and trash, mass-delete guard, approval to enable |
| R-S1 | Repository split errors (imports, CI, Docker) | Phase S by INT only, with backups, a bundle and full verification before any further work |
| R-D1 | `install.sh` complexity (detection across distros) | Container and VM test harness (T-E1); dry-run mode |
| R-L1 | PolyForm NC limits adoption and contributions | Owner decision; CLA plan (Q10) |
| R-U1 | Unsigned admin service and components (C14) | Optional signing; Ed25519 manifest signing recommended |

---

## 16. Open questions for the owner

| # | Question | Recommendation |
|---|---|---|
| Q1 | Approve Phase S (local commits in both repos, use of `git filter-repo`, creating `D:/VaultDock/vad-server`)? | Yes, as the first implementation step |
| Q2 | Default `escrow_mode` = `full` (server can read contents) vs `metadata`? (C1) | `full` per O1; offer `metadata` per repository |
| Q3 | Confirm the "encrypt on client and server" interpretation and the token/prefix-only metadata search (C2, C3) | Accept |
| Q4 | Approve Ed25519 release-manifest signing independent of Authenticode (C14) | Yes |
| Q5 | Per-user → admin install migration UX (C5) | Prompt + guided migration; per-user builds stop updating after N releases |
| Q6 | Accept the AccessCheck-gated VSS reads, or restrict VSS to the active user only (C7) | Accept; fall back if the spike fails |
| Q7 | Accept the limits of password-change detection (C8) | Accept |
| Q8 | Is "superuser" a distinct role from admin? (C9) | Distinct flag; the first admin is a superuser |
| Q9 | Other profiles' backups go to their own VaultDock account or the device owner's? (C10) | Their own account by default |
| Q10 | Copyright holder for the PolyForm Required Notice; outside contributions (CLA)? (C11) | "ASPEC Racing Pty Ltd"; CLA if contributions are accepted |
| Q11 | Accept BSD/Apache dependencies where no comparable MIT option exists (C12) | Accept |
| Q12 | Hybrid modules (built-in sync/automation/profiles vs downloaded vss/discovery/reinstall) (C15) | Accept |
| Q13 | Offline full installer asset needed? (C16) | Yes, as an optional asset |
| Q14 | VSS as an optional add-on in Simple mode? (C18) | Yes |
| Q15 | `install.sh` redirect stub in the old repo for 2 release cycles? (C21) | Yes |
| Q16 | Rewrite `VaultDock` history to purge server source before the first public push? (C22) | No, unless it was never published |
| Q17 | Go module host for the SDK: GitHub path (public proxy) vs Codehold path (GOPRIVATE)? (C23) | GitHub path |
| Q18 | Keep server-side S3/R2/B2 destinations in scope after removing direct-to-cloud? | Yes (requirement 6 intact on the server side) |

---

## 17. Requirements traceability

| Req | Design | Phases | Owners |
|---|---|---|---|
| 1 Backup engine & sources | 7.9 | 1, 3 | SDK-REPO, DSK-C, DSK-E |
| 2 Independent jobs, CDC, dedup | 7.3, 7.9 | 1 | SDK-REPO, DSK-C, SRV-2 |
| 3 Recovery | 7.9, 7.6 | 2, 3 | DSK-G, SDK-RESTORE, DSK-R |
| 4 Errors / skip / retry | 7.5, 7.9 | 0–2 | SRV-3, DSK-C |
| 5 Sync | 7.10 | 5 | SRV-5, DSK-H |
| 6 Storage integrations | 7.4 | 4 | SRV-ST, SRV-1, SRV-6 |
| 7 Web management | 7.12 | 1–5 | WEB-* |
| 8 Automations & AI | 7.11 | 0, 4, 6 | SDK-AUTO/KINDS/AI, SRV-7 |
| 9 Reliability & security | 7.2, 7.6, 10 | 1–4 | SRV-K, DSK-D, DSK-F |
| 10 UX | 7.7, 7.9 | 1–3, 6 | DSK-I, WEB-1 |
| O1 Encryption & keys | 7.2 | 1, 4 | SRV-K, SRV-C, SDK-REPO, DSK-D |
| O2 Sync modes | 7.10 | 5 | SRV-5, DSK-H, WEB-4 |
| O3 Admin install & protection | 7.6 | 0–3 | DSK-A, DSK-P, REL-C |
| O4 Signing | 7.14 | 0, 6 | REL-C |
| O5, O7 Licensing | 7.15 | 0, 6 | LIC, INT |
| O6 Multi-user | 7.8 | 1, 3 | DSK-P, SRV-M, WEB-7 |
| O8 AI | 7.11 | 6 | SRV-7 |
| O9 Split | 7.1 | S | INT |
| DR1–DR6 Deployment | 7.13 | 0, 4 | DEP, SRV-0 |
| DR7 Codehold/GitHub | 7.1 | S, 0 | INT, REL-C, REL-S |
| R11 Install modes | 7.7 | 0, 1, 3 | DSK-MOD, SDK-MOD, REL-C, SRV-M, DSK-I, WEB-7 |

---

## 18. Task graph (≤10-minute tasks; launch order)

The sub-agent brief template is unchanged from r2: task id, goal, owned files, required reading (Execution protocol, sections, contract sections, dependency reports), the do-nots, exact verify commands, and the report filename. Paths below are **post-split** (`S:` = `vad-server/`, `C:` = `VaultDock/`). Phase S must be complete first.

### 18.1 Phase S (INT only; requires Q1)

| ID | Step | Deps |
|---|---|---|
| S-01 | Safety: bundle, tag, zip, HEAD record | owner approval |
| S-02 | Create `vad-server` with `filter-repo` (history-preserving) | S-01 |
| S-03 | Move `pkg/*` → `C:sdk/*`, new SDK module, rewrite imports (SDK + desktop) | S-01 |
| S-04 | Remove server paths from `VaultDock`; move contracts into `C:sdk/contracts` | S-03 |
| S-05 | `vad-server`: drop desktop/pkg, module rename, imports, Dockerfile SDK fetch + vendor fallback, `updates.go` repo string | S-02, S-03 |
| S-06 | `D:/VaultDock/go.work`; full build/vet/test; web, desktop and Docker builds; integration tests | S-04, S-05 |
| S-07 | Local `ch commit` in both repos; report (Codehold repo creation, mirror, `sdk/v0.1.0` tag after the owner's push) | S-06 |

### 18.2 Phase 0: Foundations (38 tasks; ≈22 start immediately)

| ID | WS | Task | Files | Deps |
|---|---|---|---|---|
| P0-01 | SDK-DEPS | SDK deps: klauspost/compress, direct x/crypto | `C:sdk/go.{mod,sum}` | — |
| P0-02 | SRV-0 | Migrations 2–8 + v1→v8 test + api field mapping | `S:internal/store/{migrations.go,migrate_test.go}` | — |
| P0-03 | SRV-0 | Mount hooks, stubs, streamed routes, feature flags, `can_delete` (B1) | `S:internal/api/server.go`, `S:internal/api/devices.go`, stub files, `S:internal/api/devices_delete_test.go` | P0-02 |
| P0-04 | SRV-0 | Settings merge (B14); config (KEK file path, AI SSRF setting, tombstone days) | `S:internal/api/settings.go`, `S:internal/config/config.go`, `S:internal/api/settings_merge_test.go` | — |
| P0-05 | SRV-0 | `main.go` + testserver parity (B13) + summary/operations/plans | `S:cmd/vaultdock/main.go`, `S:internal/testserver/testserver.go`, `S:internal/store/{operations,summary,plans}.go`, `S:internal/api/{summary,operations}.go` + tests | P0-03, P0-04, P0-08 |
| P0-06 | SRV-8 | Upload hardening (B7, B8) | `S:internal/api/uploads.go`, `S:internal/store/uploads.go` + tests | — |
| P0-07 | SRV-8 | Storage hardening (B9) + unit tests | `S:internal/storage/storage.go`, `S:internal/api/admin.go`, `S:internal/store/users.go` + tests | — |
| P0-08 | SDK-WIRE | `sdk/wire` for **all** api-v1 rev 1.1 payloads + round-trip tests | `C:sdk/wire/**` | — |
| P0-09 | SRV-5 | Zip missing report (B2) | `S:internal/api/files.go`, `S:internal/api/zip_missing_test.go` | — |
| P0-10 | SDK-AUTO | Registry + KindSpec (+ `required_modules`) + golden v1 | `C:sdk/automation/{registry.go,catalog.go}`, testdata, `registry_test.go` | — |
| P0-11 | SDK-AUTO | Generic ops, merge, approval metadata | `C:sdk/automation/{engine,validate,ops,approval}.go`, `ops_test.go` | P0-10 |
| P0-12 | SDK-AUTO | Schema v2, runtime, migrate, `KindsSupportedBy(version, modules)`, fuzz, prose | `C:sdk/automation/{schema,reference,migrate,runtime,automation_test,migrate_test,fuzz_test}.go` | P0-11 |
| P0-13 | WEB-0 | Registry; `App`/`Layout` from registry; logout cache clear (B5) | `S:web/src/app/**`, `S:web/src/{App,main}.tsx`, `S:web/src/components/Layout.tsx`, `S:web/src/lib/auth.tsx` | — |
| P0-14 | WEB-0 | Typed API helpers, query hooks, format | `S:web/src/lib/{api,query,format}.ts` | — |
| P0-15 | WEB-0 | Split `Files.tsx` | `S:web/src/pages/Files.tsx`, `S:web/src/components/files/**` | — |
| P0-16 | WEB-0 | Split `Admin.tsx` + tabs registry + spinner fix | `S:web/src/pages/admin/{Admin.tsx,tabs/**,LocationBar.tsx}` | P0-13, P0-17 |
| P0-17 | WEB-0 | `TransfersTable`, `describe()`, dashboard slots | `S:web/src/pages/{Sync,Activity,Dashboard}.tsx`, `S:web/src/components/TransfersTable.tsx`, `S:web/src/lib/activity.ts` | P0-13 |
| P0-18 | WEB-0 | Test/lint tooling | `S:web/{package.json,package-lock.json,vite.config.ts,tsconfig.json,vitest.config.ts,eslint.config.js}`, `S:web/src/test/**`, `S:web/e2e/**` | — |
| P0-19 | WEB-0 | Components A (DataTable, FilterBar, ListInput, TagInput, SecretField, BulkResultToast) | `S:web/src/components/{data/**,ListInput,TagInput,SecretField,BulkResultToast}.tsx` | P0-14 |
| P0-20 | WEB-0 | Components B (Stepper, TreeView, PlanPreview, ScheduleEditor, JobProgress, charts) | `S:web/src/components/{Stepper,TreeView,PlanPreview,ScheduleEditor,JobProgress}.tsx`, `charts/**` | P0-14 |
| P0-21 | WEB-0 | `ui.tsx` accessibility; `index.css` | `S:web/src/components/ui.tsx`, `S:web/src/index.css` | — |
| P0-22 | DSK-H | `dbutil`; `state.db` migrations (issues, outbox, schedule_last, modules, profiles, sync_base); ProgramData location | `C:desktop/internal/dbutil/**`, `C:desktop/internal/state/{state,migrate,state_test}.go` | — |
| P0-23 | DSK-H | B3, B4, scan issues (B12) | `C:desktop/internal/engine/{automations,scan}.go` | P0-22 |
| P0-24 | DSK-B | Extract the transfer core | `C:desktop/internal/engine/{engine,transfer,concurrency,concurrency_test,engine_test}.go`, `C:desktop/internal/transfer/**` | — |
| P0-25 | DSK-B | Outbox + bounded locked deferral | `C:desktop/internal/engine/{report,transfer}.go`, `C:desktop/internal/outbox/**` | P0-22, P0-24 |
| P0-26 | DSK-A | `agent.API` + host + event bus (in-proc for tests) | `C:desktop/internal/agent/**` | P0-24 |
| P0-27 | DSK-A | Windows service wrapper (`x/sys/windows/svc`), `RequiredPrivileges`, `cmd/vaultdock-agent` | `C:desktop/internal/service/**`, `C:desktop/cmd/vaultdock-agent/**` | P0-26 |
| P0-28 | DSK-A | Named-pipe IPC (go-winio, SDDL, caller SID authz) + client; `main.go` UI connects via IPC; ProgramData paths; machine DPAPI | `C:desktop/internal/ipc/**`, `C:desktop/main.go`, `C:desktop/internal/platform/**`, `C:desktop/go.{mod,sum}` | P0-26 |
| P0-29 | DSK-I | `app.go` delegates over IPC; split `app_sync`/`app_settings`; bindings; mirror P0-21 UI changes | `C:desktop/{app.go,app_sync.go,app_settings.go}`, `C:desktop/frontend/wailsjs/**`, `C:desktop/frontend/src/{ui.tsx,index.css}` | P0-28, P0-21 |
| P0-30 | SDK-MOD | `sdk/modules`: module ids, Manifest types, verification interface | `C:sdk/modules/**` | — |
| P0-31 | LIC | Independent licence audit (both repos, pinned versions, LICENSE files read); PolyForm NC `LICENSE` ×2 (verbatim fetch); `THIRD_PARTY_NOTICES.md` ×2; allowlist configs; README credits/licence **draft** for INT | `C:LICENSE`, `S:LICENSE`, `C:THIRD_PARTY_NOTICES.md`, `S:THIRD_PARTY_NOTICES.md`, `C:.licenses/**`, `S:.licenses/**`, `C:planning/licences/**` | — |
| P0-32 | DEP | `env.schema.json`, `managed-files.json`; `install.sh` add-only env merge + obsolete-var approval (B15); Caddy de-bundled into `deploy/integrations/caddy` + migration (B16, C20) | `S:install.sh`, `S:docker-compose.yml`, `S:.env.example`, `S:deploy/**` | — |
| P0-33 | DEP | `install.sh`: repo URL → vad-server, image-only upgrades, managed-file `.new` handling, KEK generation + backup prompt, rollback retained; test harness | `S:install.sh`, `S:scripts/test-install/**` | P0-32 |
| P0-34 | REL-C | Client CI (go vet incl. linux stubs, tests, NSIS build, bindings diff, licence check); signing step **gated** on the secret | `C:.github/workflows/{ci,release}.yml`, `C:scripts/sign.ps1` | P0-31 |
| P0-35 | REL-S | Server CI + release (GHCR image, SDK via proxy/vendor, licence check) | `S:.github/workflows/{ci,release}.yml`, `S:Dockerfile`, `S:.dockerignore` | P0-31 |
| P0-36 | DSK-P | `guard` package (deny-list, canonicalisation, reparse refusal) + `tokens` interface stubs + tests | `C:desktop/internal/{guard,tokens}/**` | — |
| P0-37 | SRV-0 | Deploy env API (§11A) + `internal/deployenv` (embedded schema, approvals file) | `S:internal/deployenv/**`, `S:internal/api/deploy_env{,_test}.go` | P0-05, P0-32 |
| P0-38 | DSK-MOD | Module manager skeleton: persisted mode, built-in toggles, `PUT /device/modules` report (stub client call), UI gating API | `C:desktop/internal/modules/**` | P0-22, P0-26, P0-30 |
| P0-S1 | SPIKE | VSS via WMI/COM + `AccessCheck`-gated read with a user token | `C:planning/spikes/vss/**` | — |
| P0-S2 | SPIKE | winget in the user session vs SYSTEM; consent flow | `C:planning/spikes/winget/**` | — |
| P0-S3 | SPIKE | S4U token for a local and an MSA account; file access; DPAPI behaviour | `C:planning/spikes/s4u/**` | — |
| P0-S4 | SPIKE | Credential-change signals (password age, DPAPI failure, events, `pwdLastSet`) | `C:planning/spikes/credsignals/**` | — |
| P0-S5 | SPIKE | NSIS admin installer mode page with ⓘ dialogs + service registration + agent-driven component download | `C:planning/spikes/installer-modes/**` | — |
| P0-S6 | SPIKE | cloudflared detection matrix (systemd, docker, token tunnels) | `C:planning/spikes/cloudflared/**` | — |

(Spikes are not counted in the 38.)

### 18.3 Phase 1: Simple-mode backup MVP (36 tasks; ≈12 in parallel)

| ID | WS | Task | Files | Deps |
|---|---|---|---|---|
| P1-01 | SDK-REPO | `vdcrypt`: subkeys, envelope with epoch, sealed box, recipients, recovery text, chain; vectors 1, 6, 7, 8 | `C:sdk/vdcrypt/**`, `C:sdk/repo/testdata/v1/crypto*` | P0-01 |
| P1-02 | SDK-REPO | `compress` | `C:sdk/compress/**` | P0-01 |
| P1-03 | SDK-REPO | `chunker` `vd-fastcdc-1`; vector 2 | `C:sdk/chunker/**`, testdata | P1-01 |
| P1-04 | SDK-REPO | `pack` (data/meta classes); vectors 3–4 | `C:sdk/pack/**`, testdata | P1-01, P1-02 |
| P1-05 | SDK-REPO | `snapshot` (keyed file hash, JCS, signature); vector 5 | `C:sdk/snapshot/**`, testdata | P1-01, P1-02 |
| P1-06 | SRV-ST | `objstore` interface + `localfs` + conformance | `S:internal/objstore/**` | — |
| P1-07 | SDK-REPO | `repo`: init/open, config mac (vector 9), key files, chain, index, snapshots, `Backend` interface | `C:sdk/repo/*.go` | P1-03, P1-04, P1-05 |
| P1-08 | SDK-SCHED | `schedule` | `C:sdk/schedule/**` | — |
| P1-09 | SDK-CLIENT | Client methods: capabilities, keys, repos, objects, locks, snapshots, jobs, runs, issues, tasks, modules, profiles, backup-state | `C:sdk/client/**` (listed files) | P0-08 |
| P1-10 | SDK-REPO | `repo/backend/server` adapter | `C:sdk/repo/backend/server/**` | P1-07, P1-09 |
| P1-11 | SRV-DEPS | Server go.mod: SDK version bump | `S:go.{mod,sum}` | P1-07 |
| P1-12 | SRV-K | KEK/SIK/SEK custody, `key_audit`, `/keys/server` | `S:internal/keys/**`, `S:internal/store/{server_keys,key_audit}.go`, `S:internal/api/keys.go` + tests | P0-05, P1-01, P1-11 |
| P1-13 | SRV-K | Enrolment exchange (`box_pub`, `server_identity`), escrow re-wrap → `key_share` tasks | `S:internal/api/devices.go`, `S:internal/store/device_keys.go`, `S:internal/keys/rewrap.go` + tests | P1-12, P1-17 |
| P1-14 | SRV-2 | Repos + objects (localfs under `backup/`) | `S:internal/backup/repo.go`, `S:internal/store/repos.go`, `S:internal/api/backup_objects.go` + tests | P0-05, P1-06, P1-11 |
| P1-15 | SRV-2 | Locks + snapshot commit (consent check) + list/get | `S:internal/backup/locks.go`, `S:internal/store/snapshots.go`, `S:internal/api/snapshots.go` + tests | P1-14, P1-20 |
| P1-16 | SRV-C | Catalog ingestion (decrypt with escrowed `K_meta`), DEK, blind index, tree/search | `S:internal/catalog/**`, `S:internal/store/catalog.go` + tests | P1-15, P1-12 |
| P1-17 | SRV-3 | Device tasks (long-poll) + approvals | `S:internal/store/{device_tasks,approvals}.go`, `S:internal/api/{device_tasks,approvals}.go` + tests | P0-05 |
| P1-18 | SRV-3 | Jobs/runs/issues/skip/retry (module gating) | `S:internal/store/backup_jobs.go`, `S:internal/api/backup_jobs.go` + tests | P0-05, P1-19 |
| P1-19 | SRV-M | Modules endpoints + gating helper + catalog | `S:internal/store/device_modules.go`, `S:internal/api/modules.go` + tests | P0-05 |
| P1-20 | SRV-M | Profiles (`active_default`, security events → blacklist) + superuser + clear | `S:internal/store/{device_profiles,superuser}.go`, `S:internal/api/{profiles,superuser}.go` + tests | P0-05 |
| P1-21 | SRV-7 | Readiness + backup-state | `S:internal/store/readiness.go`, `S:internal/api/readiness.go` + tests | P1-15, P1-18 |
| P1-22 | DSK-D | Keystore: box key (machine DPAPI), SIK pin, first-device init (DK/RMK/recovery), `key_share` handling | `C:desktop/internal/keystore/**` | P1-07, P0-28 |
| P1-23 | DSK-P | Profiles core: active-user detection, consent record (DPAPI user scope via UI), session/S4U tokens, profile report | `C:desktop/internal/{profiles,tokens}/**` | P0-36, P0-28, P0-S3 |
| P1-24 | DSK-C | `backup.db`, job model, walker (profile token, guard) | `C:desktop/internal/backup/{store,job,walker}*.go` | P0-22, P0-24, P1-23 |
| P1-25 | DSK-C | Pipeline: chunk → index → pack → spool → upload | `C:desktop/internal/backup/{pipeline,index,spool,upload}*.go` | P1-24, P1-10, P1-22 |
| P1-26 | DSK-C | Commit, runs, issues, Retry Skipped, device-task loop | `C:desktop/internal/backup/{runner,report,tasks,issues}*.go` | P1-25, P1-17, P1-18 |
| P1-27 | DSK-C | Desktop scheduler | `C:desktop/internal/scheduler/**` | P1-24, P1-08 |
| P1-28 | DSK-MOD | Mode persistence end-to-end; module report to the server; built-in enable/disable | `C:desktop/internal/modules/**` | P0-38, P1-09 |
| P1-29 | DSK-I | First-run (connect, SIK fingerprint, recovery key confirm, consent); Simple screens (folder picker, schedule, backups, issues, readiness); module-gated nav | `C:desktop/app_{backup,wizard,mode}.go`, `C:desktop/frontend/src/**` (new screens + `App.tsx` + `modules/`), `C:desktop/frontend/wailsjs/**` | P1-26, P1-22, P1-23, P1-27, P1-28, P0-29 |
| P1-30 | REL-C | Admin NSIS installer: Program Files, service registration, ProgramData ACL, HKLM run, mode page with ⓘ dialogs (texts in `modes/`), mode file, per-user install detection + migration (C5) | `C:desktop/build/windows/installer/**`, `C:desktop/wails.json` | P0-27, P0-S5 |
| P1-31 | REL-C | Mode info texts (simple, advanced; plain language) for review by INT | `C:desktop/build/windows/installer/modes/**` | — |
| P1-32 | WEB-1 | Backups feature | `S:web/src/features/backups/**` | P1-18, P0-13, P0-14, P0-19, P0-20 |
| P1-33 | WEB-1 | Readiness + dashboard widget | `S:web/src/features/readiness/**` | P1-21, P0-17 |
| P1-34 | WEB-6 | Approvals inbox | `S:web/src/features/approvals/**` | P1-17, P0-13 |
| P1-35 | WEB-7 | Devices page: mode/modules badges, profiles panel (states, request, superuser clear), key status | `S:web/src/pages/Devices.tsx`, `S:web/src/features/{devices,profiles,keys}/**` | P1-19, P1-20, P1-12 |
| P1-36 | SRV-ITEST | E2E: enrol (SIK pin) → first-device keys → backup → incremental → second device key share → catalog browse | `S:tests/integration/{backup,keys}_test.go` | P1-26, P1-16, P1-13 |

### 18.4 Phases 2–6

The master decomposes each later phase at its start from §12/§13 with the same rules (≤10-minute tasks, exclusive files, explicit deps, one report each). Expected tasks: Phase 2 ≈ 16, Phase 3 ≈ 26, Phase 4 ≈ 22, Phase 5 ≈ 18, Phase 6 ≈ 12.

---

## Appendix A: Revision history

- **r1** (2026-10-17): consolidated seven research reports into the first plan.
- **r2** (2026-10-17): reconciled phases and ownership; locked contracts v1.0; added the execution protocol and the Phase 0–1 task graph.
- **r3** (2026-10-18), owner decisions O1–O9, DR1–DR8 and R11:
  - shared-key encryption with escrow (contract rev 1.1);
  - one-way plus two-way sync;
  - admin-only service install with least privilege and a guard;
  - optional signing;
  - licensing (PolyForm NC, allowlist, credits);
  - multi-user consent and blacklist;
  - OpenAI-compatible AI;
  - repository split (`vad-server` + `VaultDock` SDK/desktop) with a history-preserving migration;
  - Docker/`install.sh` deployment requirements;
  - Codehold/GitHub distribution;
  - Simple/Advanced install modes with a modular component system;
  - 25 explicit conflicts (§4) and 18 open questions (§16);
  - removed direct-to-cloud backup and the per-user install.
  No source files were changed in any revision.
