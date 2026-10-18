# 01: Server backend research (Go server)

Scope: `cmd/vaultdock`, `internal/{api,store,storage,auth,config,testserver}`, `Dockerfile`, `docker-compose.yml`.
Everything below comes from reading the code. Citations use `file:line` and are relative to the repo root.
Confidence tags: **[H]** verified in code, **[M]** inferred from code paths without running them, **[L]** plausible, needs a test to confirm.

---

## 1. Current state

### 1.1 Process, bootstrap and background work

- Subcommands: `serve | healthcheck | backup <file> | schema | reset-password | version` (`cmd/vaultdock/main.go:45-61`).
- Bootstrap order: config load, open the store (migrations run here), `ensureDefaultLocation`, load settings, GeoIP, `storage.New` and `Reload`, mount health warning, `api.New`, `RecoverAfterRestart` (`main.go:83-126`).
- Background loop: `RunMaintenance(ctx,false)` runs once at start and then every 10 min. Every 6th tick (hourly) it also runs the orphan scan (`main.go:136-150`). There is no general job scheduler. This ticker is the only server-side periodic execution.
- `RunUpdateChecks` runs in its own goroutine (`main.go:135`, `internal/api/updates.go:125`).
- The HTTP server has `ReadHeaderTimeout` 20 s, `IdleTimeout` 120 s and **no `WriteTimeout`/`ReadTimeout`**, so long streams work (`main.go:152-159`). Graceful shutdown takes 20 s (`main.go:182-187`).
- `vaultdock backup <dest>` makes a consistent DB copy with `VACUUM INTO` (`main.go:325-345`, `internal/store/store.go:103-109`). It only runs when an operator triggers it. **No scheduled DB backup exists.**
- `internal/testserver/testserver.go:41-76` duplicates the bootstrap by hand: no `ensureDefaultLocation`, no `RecoverAfterRestart`, `GlobalMinFree=0`. New services wired into `main.go` also have to be wired here, or desktop integration tests will skip them.

### 1.2 Middleware chain

Global: `logRequests` (panic recovery and access log) and `securityHeaders` (nosniff, DENY frame, COOP, HSTS on https, server-time header) (`internal/api/server.go:523`, `279-320`).
On `/api/v1`: `rateLimitAPI` and then `authenticate` (`server.go:529`).

- `rateLimitAPI`: one token bucket per client IP, 6000/min with burst 1200 (`server.go:62`, `322-331`). The limiter is `auth.Limiter` (`internal/auth/auth.go:140-179`). Separate limiters exist for login (20/min), enroll (10/min) and poll (60/min) (`server.go:60-63`).
- `authenticate` (`server.go:337-356`) works in this order:
  1. If `X-VD-Signature` or `X-VD-Device` is present, it calls `authDevice` (`server.go:391-454`). That verifies the Ed25519 signature over method+URI+timestamp+nonce+body-hash (`pkg/protocol/protocol.go:43,91`). It checks device status after the signature, applies the replay check via the in-memory `NonceCache` keyed by fingerprint+nonce (`auth.go:250-287`) and loads the user.
     - Non-streamed bodies are read fully, **capped at 2 MiB** (`maxJSONBody`, `server.go:161,441`), and compared with the signed hash.
     - Streamed bodies are recognised only by `isStreamedBody`: PUT under `/api/v1/uploads/*/chunks/*` (`server.go:387-389`). For those the signed hash is passed to the handler as `DeclaredBodySHA`.
  2. Otherwise it checks the session cookie `vd_session`. The DB lookup uses the SHA-256 of the token (`server.go:358-371`) and the session is touched at most once a minute (`server.go:373-384`).
- Guards:
  - `requireUser`: any principal, plus a CSRF header check for session non-GET requests (`server.go:456-472`).
  - `requireSession`: browser only (`server.go:474-482`).
  - `requireDevice` (`server.go:484-492`).
  - `requireAdmin`: session **and** role=admin, so admins cannot act from devices (`server.go:494-503`).

### 1.3 Routing table (`server.go:521-656`)

| Route | Auth |
|---|---|
| GET `/healthz`, `/readyz` | none (outside `/api/v1`) |
| GET `/info`, GET `/setup/status`, POST `/setup`, POST `/auth/login`, POST `/auth/register`, POST `/device/enroll`, GET `/device/enroll/{id}` (bearer poll token) | public (rate limited) |
| POST `/auth/logout`, GET `/auth/me` | user (session or device) |
| GET `/files`, `/files/resolve`, `/files/search`, `/files/tags`, `/files/zip`, `/files/{id}`, GET/HEAD `/files/{id}/content`; POST `/files/folder`, `/files/ensure-folder`, `/files/move`, `/files/delete`; PATCH/DELETE `/files/{id}`; PUT `/files/{id}/tags` | user (session **or device**) |
| GET `/trash`, POST `/trash/{id}/restore`, **DELETE `/trash/{id}`, DELETE `/trash`** | user (session **or device**) |
| POST/GET `/uploads`, GET `/uploads/{id}`, PUT `/uploads/{id}/chunks/{index}`, POST `/uploads/{id}/complete`, DELETE `/uploads/{id}` | user (session or device) |
| GET `/automations/reference`, POST `/automations/validate`, POST `/automations/dry-run` | user |
| GET `/device/me`, PUT `/device/state`, GET `/device/automations`, POST `/device/transfers`, POST `/device/notifications` | device |
| GET `/stats`, `/activity`, `/transfers`, `/sync/overview`, `/notifications`; POST `/notifications/read`; `/account/*` (profile, password, sessions); `/devices*` (list/approve/reject/revoke/rename/delete); `/automations` CRUD, templates, export | session |
| `/admin/*`: overview, users CRUD, revoke-sessions, reset-password, recalculate, storage CRUD, devices, revoke, transfers, uploads, cancel upload, settings GET/PUT, health, update, update/check, audit, maintenance/cleanup | session + admin |

Security-relevant observation: **a device key can permanently purge the trash and delete files** (`server.go:553,556,563-564`, handlers `internal/api/files.go:551-582`). A ransomware-compromised client could wipe server-side history, which conflicts with "protected recovery points" (req 9). **[H]**

### 1.4 Database schema (single migration, `internal/store/migrations.go:5-198`)

| Table | Columns (all timestamps are Unix ms) | Notes |
|---|---|---|
| `settings` (`:8`) | key PK, value | Platform settings are stored as one JSON blob under key `platform` (`internal/api/settings.go:68-98`) |
| `storage_locations` (`:13`) | id, name UQ, path UQ, reserve_bytes, enabled, is_default, created_at | Filesystem paths only; no backend kind or config column |
| `users` (`:23`) | id, username UQ NOCASE, email UQ NOCASE, display_name, password_hash, role CHECK(user,admin), disabled, quota_bytes, used_bytes, location_id FK, root_id, created_at, updated_at, last_login_at | `used_bytes` is **logical** (sum of node sizes, trash included) (`internal/store/users.go:224-234`) |
| `sessions` (`:40`) | id (SHA-256 of token) PK, user_id FK CASCADE, csrf, ip, user_agent, created_at, last_seen_at, expires_at | |
| `nodes` (`:52`) | id, user_id FK CASCADE, parent_id FK, name, is_dir, size, sha256, mime, created_at, modified_at, updated_at, deleted_at, trash_root, original_parent_id, origin_device_id | Unique live name per folder (NOCASE) (`:69`); indexes `:70-73` |
| `node_tags` (`:75`) | node_id FK CASCADE, tag NOCASE; PK(node_id, tag) | |
| `blobs` (`:82`) | user_id, sha256, size, location_id FK, refcount, created_at; **PK(user_id, sha256)** | Per-user dedup with refcounts |
| `devices` (`:92`) | id, user_id FK CASCADE, name, os, client_version, identifier, public_key, key_fingerprint, status CHECK(pending,approved,rejected,revoked,expired), user_code, poll_token_hash, request_ip, request_location, created_at, expires_at, decided_at, decided_by, last_seen_at, last_ip, sync_state JSON, sync_state_at | `sync_state` is an opaque client JSON blob ≤512 KiB (`internal/api/devices.go:270-289`) |
| `uploads` (`:117`) | id TEXT PK, user_id FK CASCADE, device_id, transfer_id, path, size, sha256, chunk_size, chunks_total, received (bitmap BLOB), bytes_received, location_id, modified_at, tags JSON, on_conflict, status (open/finalizing/done/failed), created_at, updated_at, expires_at | |
| `transfers` (`:140`) | id, user_id FK CASCADE, device_id, upload_id, path, size, bytes_done, status, error, source (web/device), started_at, finished_at | Transfer history |
| `automations` (`:157`) | id, user_id FK CASCADE, name, description, enabled, priority, is_template, valid, definition JSON, created_at, updated_at | |
| `activity` (`:172`) | id, user_id, actor_id, device_id, action, target, details, ip, audit, created_at | No FK; audit rows survive user deletion |
| `notifications` (`:187`) | id, user_id FK CASCADE, title, message, level, source, read_at, created_at | |

### 1.5 Migration mechanism

- `migrations` is a `[]string`, one DDL script per version. "Never edit a released migration; append a new one" (`migrations.go:3-5`).
- `migrate` reads `PRAGMA user_version` and applies each missing script in its own transaction, setting `user_version=i+1` (`store.go:73-100`). It refuses to start if the DB is newer than the binary (`store.go:78-80`). There are no down-migrations.
- Next free number: **2**. The schema version is exposed via `vaultdock schema` and admin health (`main.go:347-368`, `internal/api/admin.go:584`).

### 1.6 Connection and transaction patterns

- DSN: WAL, `busy_timeout(15000)`, `foreign_keys(1)`, `synchronous(NORMAL)`, **`_txlock=immediate`** (`store.go:35-41`); `MaxOpenConns(8)` (`store.go:46`).
- `Store.Tx(fn)` is the only transaction helper (`store.go:124-134`). With `_txlock=immediate`, every `Tx` takes the single SQLite write lock at BEGIN. Plain reads through `s.DB` (for example `ListTrash`, `ListChildren`) are unaffected. Every transactional path serialises: `CreateFolder`, `MoveRename`, `MarkChunk`, `CreateUpload`, `CommitFile`, `PurgeTrash`, `RecalculateUsage`.
- Store functions take a `Querier` interface (`store.go:146-150`) so helpers run inside or outside a transaction.
- Unique-violation detection is string matching (`store.go:141-143`).
- Long single transactions exist today: `PurgeTrash` deletes whole trees and adjusts refcounts in one transaction (`internal/store/nodes.go:557-602`), and `DeleteUserRecords` does the same (`users.go:190-221`).

### 1.7 Blob storage layout (`internal/storage/storage.go`)

- Layout: `<loc>/.vaultdock-location` (mount marker), `<loc>/blobs/<userID>/<aa>/<bb>/<sha256>`, `<loc>/tmp/<uploadID>.part` (`storage.go:3-10`, `152-173`).
- The marker check means a missing mount is never treated as empty storage (`storage.go:122-127`).
- Content addressing: whole-file SHA-256, validated as lowercase hex (`storage.go:38-45`). Paths are built only from numeric IDs and hashes.
- Dedup: **per user only**, enforced by both the blob path (`userID` directory) and `blobs` PK(user_id, sha256). There is no cross-user dedup. Cross-device dedup for the same user works for whole identical files only.
- Locks: 256 striped in-process mutexes keyed by fnv(sha+userID) (`storage.go:129-136`). They are process-local; a second server instance would need a distributed lock.
- `Place`: renames the verified temp file into place under the lock and runs `commit` (DB insert) under the same lock. If the commit fails and the blob is unreferenced, it deletes the new file (`storage.go:216-262`). If a blob row exists on another location, it copies there (`storage.go:238-245`).
- `LinkExisting`: dedup without upload; forces a re-upload if the row exists but the file is missing (`storage.go:266-285`).
- `Remove`: under the lock, deletes the file only if no row exists any more (`storage.go:288-305`).
- Space: free space minus pending upload bytes must stay above max(location reserve, `GlobalMinFree`) (`storage.go:187-211`). Free space comes from `statfs` or `GetDiskFreeSpaceEx` (`disk_unix.go`, `disk_windows.go`).
- Orphan cleanup `CleanOrphans(minAge=2h)`: removes `tmp/` files without an open upload and walks `blobs/`, removing files with no DB row (re-checked under the lock) (`storage.go:365-441`, triggered at `admin.go:672-678`).
- No checksum scrub or bit-rot verification of stored blobs exists. `IntegrityCheck` only checks SQLite (`store.go:112-121`).

### 1.8 Upload session, chunk and complete flow (`internal/api/uploads.go`)

1. **Create** (`:102-237`):
   - Validates sha256 (required for devices, `:115-118`), size ≤ 2^46, on_conflict, tags, `MaxFileBytes`, and the path (`uploadTarget` `:55-87`).
   - Dedup fast path: `LinkExisting` + `CommitFile` and an "already finished" transfer record (`:152-181`).
   - Resume: `FindResumableUpload` matches user, device, path, size and sha (`:183-187`, `internal/store/uploads.go:123-129`).
   - Quota: logical used + pending + size (`:189-201`). Then `CheckSpace` (`:207`). Then it creates `tmp/<id>.part` with `O_EXCL` and the uploads and transfers rows (`:219-234`).
2. **Chunk** PUT (`:282-362`):
   - Exact `Content-Length` is required (`:297-301`). The declared hash comes from the signed body hash for devices or the optional `X-Chunk-SHA256` for web (`:302-310`).
   - Back-pressure: `chunkSem` (capacity `MaxConcurrentChunkWrites`, default 64, `internal/config/config.go:136-140`), with a 3 s wait and then 503 + `Retry-After` (`:314-324`).
   - The chunk is written at its offset with `OffsetWriter`, hashed, `fsync`ed and then `MarkChunk` updates the bitmap in a transaction (`:337-360`, `store/uploads.go:133-161`).
3. **Complete** (`:364-404`):
   - CAS `open→finalizing` (`:374-382`). `finalize` checks size, re-hashes the whole file, compares with the declared hash and sniffs the MIME type. Then `Place` + `CommitFile` (`:415-473`).
   - Permanent errors set the upload to `failed`; transient ones revert to `open` (`:384-397`). `RecoverAfterRestart` resets `finalizing→open` (`admin.go:683-690`).
4. **Commit** `CommitFile` (`internal/store/commit.go:36-145`), one transaction:
   - Disabled-account check; `EnsureDirs` (mkdir -p).
   - Conflict handling: unchanged (same sha+size, touch mtime) / skip / rename `name (n).ext` / replace. Replace either trashes the old node (`KeepReplaced`) or purges it (`:61-107`).
   - Quota check (`:108-110`); blob upsert refcount+1 (`:112-114`); insert node; `used_bytes += size` (`:128`); `finishUpload` marks the upload and transfer (`:147-159`).
5. Defaults: chunk size 8 MiB (env 1-64 MiB, `config.go:141-145`), session TTL 48 h refreshed on every chunk (`config.go:146-150`, `uploads.go:357`).

### 1.9 Trash and versioning

- Soft delete: `trashTx` sets `deleted_at` on the subtree and marks the root `trash_root=1, original_parent_id` (`nodes.go:429-443`).
- Restore: returns to the original parent or the root, with "(restored n)" renaming (`nodes.go:495-533`).
- Purge: `purgeTree` decrements blob refcounts per (user, sha), decrements `used_bytes` and returns freed `BlobRef`s. The caller then calls `blobs.Remove` (`nodes.go:606-653`).
- "Versioning" is only `KeepReplacedVersions` (default true, `settings.go:38`). The replaced file becomes a **separate trash root with the same name** (`commit.go:91-95`). Nothing links versions to their live file. Versions disappear with trash retention (default 30 days, `settings.go:35`; purge at `admin.go:661-670`). There is no version list or "restore this version" API.

### 1.10 Activity, transfers, notifications

- `LogActivity` is fire-and-forget (errors ignored) (`internal/store/misc.go:214-217`). The API helper adds actor, device and IP (`server.go:507-518`). `audit=1` rows go to the admin audit log and are never pruned (`misc.go:268-272`).
- `PruneActivity` deletes non-audit activity, finished transfers and **all notifications** older than `ActivityRetentionDays` (default 180) (`misc.go:268-272`, `settings.go:36`).
- Devices report only `failed`/`skipped` transfers (≤200 per call) (`devices.go:320-345`) and notifications (≤50) (`devices.go:347-378`).

### 1.11 Maintenance jobs (`RunMaintenance`, `admin.go:634-680`)

It runs these steps in order: expire pending devices; purge expired sessions; clean expired uploads, cancelling open ones and deleting done/failed rows and temp files; mark stale transfers failed; purge trash older than the retention period across all users; prune activity; optional orphan scan. Admins can trigger it with `POST /admin/maintenance/cleanup` (`admin.go:626-630`).

### 1.12 Rate limiting and abuse controls

- Per-IP token buckets (see 1.2), plus `FailureTracker` login lockout (8 failures in 15 min blocks for 15 min) (`server.go:64`, `auth.go:183-244`).
- Argon2id hashing is bounded to 4 concurrent operations (`auth.go:32`).
- At most 5 pending device requests per user (`devices.go:18,68-76`).
- The nonce cache is in memory: requests timestamped before process start are rejected (`auth.go:276-279`), so clients must retry after a server restart.

### 1.13 Settings model

- Platform settings: a struct `Settings` (site name, registration, default quota, max file size, trash retention, activity retention, device TTL, keep replaced versions, min free bytes) (`settings.go:17-27`). It is validated (`:43-57`), cached in memory and saved as JSON (`:60-98`).
- Environment config: `config.Config` (`config.go:14-55`), including storage roots, chunk size, concurrency, upload TTL, TLS, proxies, update repo.
- The settings API returns a whole object, and `decode` uses `DisallowUnknownFields` (`server.go:166`). Adding fields is backward-compatible for the server but old web builds will drop new fields when they PUT. See risk R8.

### 1.14 Admin features and quotas

- Admin features: users (create/patch/delete with username confirmation, last-admin protection), sessions, password reset, usage recalculation, storage locations (path must be inside `VAULTDOCK_STORAGE_ROOTS`, no nesting, delete only when empty) (`admin.go:333-469`), devices, transfers, uploads, settings, health (DB size, quick_check, space, chunk slots) (`admin.go:566-613`), audit, maintenance.
- Quotas: per-user `quota_bytes` (0 = unlimited) against **logical** bytes including trash (`commit.go:108`, `users.go:227`). Deduplicated copies count in full. Physical (deduplicated) bytes are only reported globally (`misc.go:421`).

---

## 2. Reuse map (requirements 1-10)

Legend: **Reuse** = use as-is; **Extend** = add to it in a backward-compatible way; **Don't touch** = leave alone, build alongside.

| Req | Reuse as-is | Extend | Do not touch / keep separate |
|---|---|---|---|
| 1 Backup engine (discovery, AppData, registry, inventories) | Device auth and signing (`server.go:391-454`); `pkg/protocol`; `/device/me` capabilities (`devices.go:260-268`) | `/device/me` to advertise backup capabilities and limits; the server stores job definitions and app inventories (new tables). Discovery itself is desktop-side | `nodes`/`blobs` must not be used to store backup chunks (see §3.2) |
| 2 Independent jobs, CDC, dedup, resumable, incremental | `chunkSem` back-pressure pattern (`uploads.go:314-324`); `Place`-style "write temp, verify hash, rename under lock" (`storage.go:216-262`); 503/Retry-After contract | `isStreamedBody` (`server.go:387-389`) must recognise new object-PUT routes; rate limiter exemptions or a per-device limiter for the data plane | Existing 8 MiB upload sessions stay for sync; backup objects get their own simpler PUT path |
| 3 Recovery (reinstall, remap profile paths) | `GET /files/{id}/content` with Range (`files.go:373-429`) for sync-side restores | New device task queue so web-initiated restores reach the device; catalog of profile path, SID-free user name and hostname per snapshot | Server never applies ACLs; all mapping logic is client-side |
| 4 Error handling, skip, retry skipped | `transfers` table for sync failures (`store/uploads.go:236-271`) | New `run_issues` table; Retry Skipped becomes a device task | Fix the silent-skip in zip download (`files.go:500-507`) |
| 5 Continuous sync, versions, conflicts | Whole sync path: uploads, `CommitFile`, trash, dedup, `on_conflict` (`commit.go:72-106`) | Proper `file_versions` (replace trash-as-version, `commit.go:91-95`); sync-folder and ruleset storage (like `automations`); version list and restore endpoints | Keep backup snapshots in a separate catalog and namespace (no shared `nodes` rows) |
| 6 Storage integrations (local, SMB, S3, R2, B2) | `storage_locations` + marker + reserve concept (`storage.go:89-127`); `AllowedStoragePath` (`config.go:163-174`) | New `internal/objstore` interface; `localfs` implementation reuses `PrepareLocation`/`LocationHealthy`; `destinations` table with encrypted credentials | `storage.Manager` stays filesystem-only in phase 1 (http.ServeContent needs `*os.File` seek, `files.go:394,428`) |
| 7 Web management (browse, search, prune, analytics, GC) | `Search` filter style (`nodes.go:716-778`); stats patterns (`misc.go:338-427`); admin views | Snapshot browse, search and diff endpoints; prune dry-run plans; analytics per repo and job | Existing refcount GC for sync blobs (`purgeTree`) stays; backup GC is a separate mark-and-sweep (§3.6) |
| 8 Automations and AI | `automations` table and CRUD, validation, dry-run (`internal/api/automations.go`); devices pull definitions (`devices.go:299-316`) | New trigger and action kinds for backup, verify and replicate in `pkg/automation` (other agent); a server-side scheduler for server-owned tasks; encrypted AI key storage; approval gates | The automation engine is evaluated on the **device** today; the server does not execute automations |
| 9 Reliability and security | Ed25519 device signing, CSRF, Argon2id, marker-based mount safety, hash-verified finalisation | Append-only device role for backup data (no delete); immutable "protected" snapshots; pack-level server scrub; scheduled `VACUUM INTO` DB backup (`store.go:103`) to a storage location | Server must not hold the only copy of keys, index or manifests (§3.9) |
| 10 UX (setup, readiness, notifications) | `notifications` table and device notifications; `sync/overview` (`misc.go` API `:92-132`) | Readiness report endpoint aggregating last successful run per job, verification age and restore-test age; notification preferences | |

---

## 3. Required server changes

### 3.1 Guiding architecture decision

Build a **separate backup repository subsystem** next to the existing sync tree.

- Sync stays as it is: whole-file SHA-256 blobs, `nodes`, per-user refcounts.
- Backups use a self-describing repository of content-addressed **packs** produced by the client: CDC chunks, Zstd-compressed, optionally encrypted. The repository can live behind the VaultDock server or directly on S3, SMB or local disk.
- The server keeps a **catalog** (SQLite) that is a rebuildable cache of what is in the repository, not the source of truth.

This satisfies "sync and DR snapshots logically separate" (req 5) and "restore without the management server" (req 9). It also avoids loading millions of chunk rows into `nodes` and `blobs`.

### 3.2 Why not reuse `nodes`/`blobs` for backup data [H]

- `blobs` is keyed by whole-file hash and per-user (`migrations.go:82-90`), and dedup is whole-file only. CDC chunks would mean one `blobs` row and one file per chunk: millions of files in `blobs/<u>/aa/bb/` and a refcount transaction per chunk.
- `CommitFile` needs a path in a user's tree and does conflict and quota logic per file (`commit.go:36-145`). Snapshots are immutable trees, not a mutable namespace.
- Ciphertext chunks have no meaningful names or MIME types. Preview, zip and search on `nodes` would show garbage.

### 3.3 New packages and files

| Package / file | Purpose |
|---|---|
| `internal/objstore/objstore.go` | `Backend` interface (below), key validation, error types (`ErrNotExist`, `ErrUnavailable`, `ErrInsufficientSpace`) |
| `internal/objstore/localfs.go` | Filesystem backend: atomic temp+rename, marker file, `DiskSpace`; covers local disks and **host-mounted SMB/NFS** (the container cannot mount SMB itself: `cap_drop: [ALL]`, `read_only: true`, `docker-compose.yml`) |
| `internal/objstore/s3.go` | S3-compatible backend for AWS, R2, B2 (S3 API) and MinIO. Pure Go is required (`CGO_ENABLED=0`, distroless, `Dockerfile`). Library choice and licence are deferred to the OSS research agent |
| `internal/objstore/conformance_test.go` | Shared behaviour suite every backend must pass |
| `internal/secrets/secrets.go` | AEAD (XChaCha20-Poly1305 or AES-GCM) envelope encryption of destination credentials and AI keys with a server master key file in `StateDir` (`config.go:18`) |
| `internal/backup/` | `repo.go` (repository registry, object validation), `catalog.go` (snapshot catalog ingest and rebuild from repo), `locks.go` (repo locks with expiry), `retention.go` (keep-last/hourly/daily/weekly/monthly/yearly/within/tags policy evaluation), `gc.go` (mark-and-sweep on packs), `verify.go` (pack hash scrub), `replicate.go` (server-side copy between backends) |
| `internal/scheduler/scheduler.go` | Persistent server-side job runner (cron or interval) replacing ad-hoc additions to the `main.go:136-150` ticker; runs GC, verify, replicate, DB backup and catalog rebuild |
| `internal/ai/` (optional, late) | Provider proxy for user-supplied keys; returns *proposals* only and never executes destructive actions |
| `internal/store/*.go` (new files) | Query code per feature (see §6) |
| `internal/api/*.go` (new files) | Handlers per feature (see §6) |

Proposed `objstore.Backend` shape (server side; the desktop needs the same semantics via `pkg`, see §7):

```go
type Backend interface {
    Put(ctx context.Context, key string, r io.Reader, size int64, sha256hex string) error // atomic, idempotent; verify hash before publish
    Get(ctx context.Context, key string, off, n int64) (io.ReadCloser, error)              // ranged
    Stat(ctx context.Context, key string) (Info, error)
    Delete(ctx context.Context, key string) error
    List(ctx context.Context, prefix string, fn func(Info) error) error
    Health(ctx context.Context) error   // marker object/file present
    Space(ctx context.Context) (Space, bool, error) // ok=false for object stores
}
```

How the current `internal/storage` sits behind it:

- Phase 1: no change. `storage.Manager` keeps serving sync blobs.
- Phase 2 (optional, later): add `kind` and `config` columns to `storage_locations` and re-implement `BlobPath`/`Open`/`Place`/`Remove`/`CleanOrphans` on `Backend`. That needs three things:
  - (a) a `ReadSeeker` adapter over ranged `Get` for `http.ServeContent` (`files.go:428`);
  - (b) local assembly of upload temp files, because `OffsetWriter` random writes (`uploads.go:338`) are impossible on S3 (S3 multipart upload with 8 MiB parts would work since parts ≥5 MiB);
  - (c) `CheckSpace` (`storage.go:187-211`) degrading to "quota only" when `Space` is unknown.

Do not attempt phase 2 in the first wave. It touches the most fragile verified code paths.

### 3.4 Repository object model (server's view)

- Object kinds: `config` (repo params plus **wrapped** master key), `keys/*`, `index/*`, `snapshots/*`, `data/<aa>/<packid>`, and optionally `locks/*`.
- Object ID = SHA-256 of the **stored (ciphertext) bytes**. The server can then verify integrity on PUT and during scrubs **without keys**.
- Chunk IDs inside packs should be a keyed hash (HMAC with a repo key), so the server and storage cannot confirm plaintext content. The server never sees chunk IDs; it only sees pack IDs.
- Packs are the upload and retry unit, roughly 4-64 MiB. They are uploaded with a single signed streamed PUT, so no upload session table is needed. A pack that fails is simply re-PUT (idempotent).
- On snapshot commit the device sends a **plaintext list of referenced pack IDs** (opaque hashes) plus small cleartext stats. This lets the server do reachability GC and per-repo usage without decrypting. Optional "managed metadata" mode also sends a cleartext file listing for web browse and search (see §3.10).

### 3.5 New tables (migration convention: append to `migrations` in order)

Recommended numbering: land all of these together as **migration 2** (one reviewed schema) or as 2-6, one per area, owned by a single schema owner (§6). Columns use the existing conventions: INTEGER ms timestamps, `user_id` FK with `ON DELETE CASCADE`, JSON in TEXT.

| # | Table | Key columns |
|---|---|---|
| 2 | `secrets` | id, owner_user_id NULL=platform, kind (`dest_cred`, `ai_key`), key_id, nonce, ciphertext, created_at, rotated_at |
| 2 | `destinations` | id, user_id NULL=admin-global, name, kind (`server`, `local`, `smb`, `s3`), config JSON (endpoint, bucket, prefix, region, path; non-secret), secret_id FK, credential_scope (`client_only`, `server_held`), enabled, health_status, last_checked_at, created_at, updated_at |
| 3 | `repositories` | id, user_id, destination_id, repo_uuid UQ, format_version, chunker_params JSON, encryption (`none`, `client`), created_at, last_catalog_sync_at |
| 3 | `packs` | repo_id, pack_id (hex), size, created_at, last_verified_at, verify_status, deleted_at; PK(repo_id, pack_id). Pack-level rows keep the table small: ~65k rows per TB at 16 MiB packs |
| 3 | `snapshots` | id, repo_id, snapshot_oid UQ (repo object), job_id, device_id, user_id, parent_id, kind (`folder`, `app`, `project`, `registry`, `inventory`, `gamesave`), hostname, os_user, profile_path, client_time, committed_at, file_count, logical_bytes, new_bytes, tags JSON, protected, protected_until, status (`committed`, `forgotten`, `missing`), summary JSON |
| 3 | `snapshot_packs` | snapshot_id, pack_rowid; PK(snapshot_id, pack_rowid). Alternative: a compressed bitmap BLOB per snapshot if row counts become large |
| 3 | `snapshot_files` (managed-metadata mode only) | snapshot_id, path, size, mtime, kind, mode bits. Optional; enables web search |
| 4 | `backup_jobs` | id, user_id, device_id, repo_id, name, kind, mode (`full`, `data_only`, `reinstall_inventory`), spec JSON (paths, exclusions, app id), schedule JSON, retention_policy_id, priority, enabled, created_at, updated_at |
| 4 | `job_runs` | id, job_id, device_id, snapshot_id NULL, status (`queued`, `scanning`, `uploading`, `committing`, `completed`, `partial`, `failed`, `cancelled`), started_at, finished_at, files_total, files_done, bytes_total, bytes_new, bytes_uploaded, warnings, errors, summary JSON |
| 4 | `run_issues` | id, run_id, path, severity (`warning`, `skipped`, `error`), code, message, retryable, resolved_at, retry_run_id |
| 4 | `device_tasks` | id, user_id, device_id, kind (`backup_now`, `restore`, `retry_skipped`, `verify`, `prune_compact`), payload JSON, status (`pending`, `claimed`, `done`, `failed`, `cancelled`), requires_approval, approved_by, approved_at, created_by, created_at, claimed_at, finished_at, result JSON |
| 4 | `app_inventory` | snapshot_id, name, version, publisher, source (`winget`, `msstore`, `choco`, `msi`, `exe`), package_id, install_scope, mode. A searchable projection; the canonical copy stays in the repo |
| 5 | `retention_policies` | id, user_id, name, rules JSON, created_at, updated_at |
| 5 | `prune_plans` | id, repo_id, policy_id, created_by, status (`draft`, `approved`, `executing`, `done`, `failed`, `expired`), plan JSON (snapshots to forget, packs to delete, bytes freed), approved_by, approved_at, executed_at, error |
| 5 | `repo_locks` | repo_id, holder (device or server), kind (`shared`, `exclusive`), acquired_at, expires_at |
| 6 | `file_versions` | id, user_id, node_id (live file), sha256, size, modified_at, origin_device_id, created_at, pinned. Holds a **blob refcount** like a node does |
| 6 | `sync_folders` | id, user_id, device_id, local_path, remote_path, ruleset JSON, schedule JSON, conflict_policy, enabled, created_at, updated_at |
| 7 | `replication_policies` | id, user_id, src_repo_id, dst_destination_id, mode (`mirror`, `copy-new`), schedule JSON, last_cursor, last_run_at, status |
| 7 | `notification_prefs` | user_id, event, channel (`in_app`, `email`, `desktop`), enabled |

Also `ALTER TABLE users ADD COLUMN backup_quota_bytes INTEGER NOT NULL DEFAULT 0`. Backup usage is computed from `packs` per repo (physical, deduplicated), separate from sync `used_bytes`.

### 3.6 Dedup-aware GC, retention and pruning

- **Sync blobs today:** exact refcounting inside the commit/purge transactions (`commit.go:112-114`, `nodes.go:629-645`), then deferred file deletion re-checked under the blob lock (`storage.go:288-305`), plus orphan sweep after 2 h (`storage.go:365-441`). This is safe and should be kept.
- **Backup packs:** use mark-and-sweep, not refcounts. Refcounting per chunk on the server is impossible (the server cannot see chunks) and per pack is fragile under concurrent snapshot commits.
  1. Plan (dry run): evaluate the retention policy and produce `forget` snapshots, but never forget `protected` ones or anything newer than the last verified good snapshot per job. Compute live packs as the union of `snapshot_packs` over the kept snapshots. Candidates are packs not live and older than a **grace period** (for example 24 h, covering in-flight uploads not yet committed). Store the result as `prune_plans.plan`.
  2. Approve: session user plus CSRF (and re-auth for large deletes). AI or automation-originated plans always require this step (req 8).
  3. Execute: take an exclusive repo lock, which blocks new commits (devices get 409 `repo_locked` and retry). Re-validate the plan against current commits, mark snapshots `forgotten`, delete repo snapshot objects, then delete packs in batches. Use one small transaction per batch, not one giant transaction, because of the single-writer constraint (§4).
- **Partially-live packs** cannot be repacked by the server when encrypted (it cannot read chunks). Compaction must be a **device task** (`prune_compact`) run by a client holding keys. Until then partially-live packs are retained, which is a space overhead. Report it in analytics as "reclaimable after compaction".
- The repository must also carry the forget/prune state (deleted snapshot objects), so a restore tool without the server sees the same truth.

### 3.7 API endpoints (new)

All are under `/api/v1`. "dev" means signed device, "sess" means session + CSRF, "admin" means session + admin.

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/backup/capabilities` | dev, sess | Server limits: max pack size, supported repo formats, server destination enabled |
| GET/POST/PATCH/DELETE | `/destinations[/{id}]` | sess | User destinations; credentials are write-only (never returned) |
| POST | `/destinations/{id}/test` | sess | Connectivity and permission check (server-held credentials only) |
| GET/POST/PATCH/DELETE | `/admin/destinations[/{id}]` | admin | Global destinations and default policies |
| POST | `/repos` | sess, dev | Register a repository (uuid, destination, params). Device-created repos need user approval if the device lacks a role |
| GET | `/repos`, `/repos/{id}` | sess, dev | List and usage |
| HEAD | `/repos/{id}/objects/{kind}/{oid}` | dev | Existence check |
| POST | `/repos/{id}/objects/missing` | dev | Body: list of oids (≤2 MiB, roughly 25k ids); returns missing ones. This is the core of "minimal reuploads" |
| PUT | `/repos/{id}/objects/{kind}/{oid}` | dev | Streamed, signed-hash-verified pack or index upload. Server-destination repos only. Add to `isStreamedBody` and use `chunkSem` |
| GET | `/repos/{id}/objects/{kind}/{oid}` | dev, sess | Ranged read for restore and web decryption (server-destination repos) |
| POST | `/repos/{id}/locks`, DELETE `/repos/{id}/locks/{lid}` | dev | Shared lock for backup, exclusive for compaction; has expiry and refresh |
| POST | `/repos/{id}/snapshots` | dev | Commit a snapshot: oid, job, run, parent, stats, referenced pack list. The server verifies every referenced pack exists |
| GET | `/snapshots` | sess | Filter by repo, job, device, kind, tag, date, protected |
| GET | `/snapshots/{id}`, `/snapshots/{id}/files?path=` | sess | Details; browse in managed-metadata mode |
| GET | `/snapshots/diff?a=&b=` | sess | Changed files (managed mode) or pack delta |
| PATCH | `/snapshots/{id}` | sess | Tags, `protected`, `protected_until`. **Unprotect requires re-auth** |
| POST | `/catalog/rebuild` | sess, admin | Rebuild the catalog from repo objects |
| GET/POST/PATCH/DELETE | `/backup/jobs[/{id}]` | sess (CRUD), dev (GET own) | Job definitions; devices pull them like `/device/automations` |
| POST | `/backup/jobs/{id}/runs` | dev | Start a run |
| PATCH | `/backup/runs/{id}` | dev | Progress heartbeat and status (throttled; batch counters) |
| POST | `/backup/runs/{id}/issues` | dev | Batch of warnings, skips and errors (≤500 per call) |
| GET | `/backup/runs`, `/backup/runs/{id}`, `/backup/runs/{id}/issues` | sess | Completion summaries |
| POST | `/backup/runs/{id}/retry-skipped` | sess | Creates a `retry_skipped` device task |
| GET | `/device/tasks?wait=25` | dev | Long-poll task claim (no `WriteTimeout`, so long-poll is safe, `main.go:152-159`) |
| POST | `/device/tasks/{id}/result` | dev | Complete a task |
| POST | `/device/tasks` | sess | Create a task (restore, backup now, verify); destructive kinds get `requires_approval` |
| POST | `/device/tasks/{id}/approve` | sess | Approval gate |
| GET/POST/PATCH/DELETE | `/retention-policies[/{id}]` | sess | CRUD |
| POST | `/repos/{id}/prune/plan` | sess | Dry-run plan |
| POST | `/prune-plans/{id}/approve`, `/prune-plans/{id}/execute` | sess | Gate and execute (execute runs as a scheduler job) |
| GET | `/repos/{id}/analytics` | sess | Stored vs logical bytes, dedup ratio, growth, reclaimable, packs per job |
| POST | `/repos/{id}/verify` | sess | Server-side pack scrub (hash only); client-side deep verify through a device task |
| GET/POST/PATCH/DELETE | `/replication[/{id}]` | sess | Replication policies |
| GET | `/files/{id}/versions`; POST `/files/{id}/versions/{vid}/restore`; PUT `/files/{id}/versions/{vid}/pin` | user (session); devices read only | Sync file version history |
| GET/POST/PATCH/DELETE | `/sync-folders[/{id}]` | sess (CRUD), dev (GET own) | Watched folders and rulesets |
| GET | `/readiness` | sess | Recovery-readiness report: per job last success, issues, verification age, restore-test age, protected points |
| GET/PUT | `/account/notification-prefs` | sess | Notification preferences |
| POST | `/ai/diagnose`, `/ai/recommend` | sess | Optional; uses the user's stored key; returns proposals only |
| GET/PUT | `/admin/backup-settings` | admin | Grace period, max pack size, scrub schedule, DB auto-backup |

### 3.8 Backwards-compatible changes to existing endpoints

- `GET /info` and `GET /device/me`: add `features: ["backup.v1", "versions.v1", "device_tasks.v1"]` so old clients ignore it and new clients feature-detect (`server.go:668-678`, `devices.go:260-268`).
- **Device role restriction**: move `DELETE /trash`, `DELETE /trash/{id}` (and arguably `DELETE /files/{id}`, `POST /files/delete`) behind `requireSession`, or a per-device "can_delete" flag defaulting to true for existing devices so old sync clients keep working. Check first whether the desktop sync engine calls these: `pkg/client` only calls `/device/*`, `/files/ensure-folder`, `/info` and `/uploads*`. **[H]** for `pkg/client`; the desktop engine itself was not audited here. Locking them down is therefore likely safe.
- `CommitFile` with `KeepReplaced` (`commit.go:91-95`): write a `file_versions` row (blob refcount +1) instead of, or in addition to, a trash root. Keep the setting name for compatibility; add `version_retention` (count or days).
- Settings struct (`settings.go:17-27`): new fields need defaults in `defaultSettings`. Because PUT replaces the whole struct, old web UIs would zero new fields. Mitigate by merging the incoming JSON onto current values, or by moving backup settings to `/admin/backup-settings` (recommended).
- `RunMaintenance` (`admin.go:634-680`): do not grow it further. New periodic work goes to `internal/scheduler`.
- `handleZip` (`files.go:500-507`): report missing blobs, either with an `X-VD-Missing` trailer and a `MISSING.txt` entry or by failing. Required by req 4.

### 3.9 Server-independent restore: what the server must not be the only holder of

1. **Encryption keys**: the master key is stored wrapped (password-derived and recovery-key-derived) inside the repo `config`/`keys/*`. The server stores no plaintext key and is never the sole wrapper.
2. **Snapshot manifests, trees and index**: canonical copies live in the repository objects. The catalog tables are caches, rebuildable via `/catalog/rebuild`.
3. **App inventory, profile path and mapping metadata**: inside the snapshot objects.
4. **Destination credentials**: user-managed and held client-side (DPAPI) by default; the server copy is optional (`credential_scope`).
5. **Forget, prune and protect state**: protection must be enforced at the storage level where possible (S3 Object Lock / retention), because a server-side `protected` flag means nothing if a client writes to S3 directly.
6. **The sync tree itself (existing)**: today file names and paths exist only in SQLite (`nodes`); blobs on disk are bare hashes (`storage.go:160`). Loss of `/state` makes sync data unrecoverable in practice. Add scheduled `VACUUM INTO` copies to every healthy storage location (scheduler job), plus a documented restore procedure.

### 3.10 Client-side encryption implications

With the server seeing only ciphertext packs:

- **Breaks or degrades:**
  - Server-side preview, MIME sniffing (`uploads.go:440-454`), zip download, filename search and per-kind analytics (`misc.go:348-355`).
  - Server-side restore to the browser, unless the browser decrypts (WebCrypto plus passphrase) or a device performs the restore.
  - Server-side repacking and compaction.
  - Server-side dedup across users (none today anyway) and content-level verification.
- **Still works:** pack-hash integrity scrub, reachability GC from plaintext pack lists, quotas from pack sizes, replication (byte copy), retention by snapshot metadata (time, tags, job), locks, readiness reporting.
- **Decision needed:** offer "managed metadata" mode (cleartext path, size and mtime listing per snapshot in `snapshot_files`; contents still encrypted) for web browse and search, versus full E2E mode where web browse requires unlocking in the browser. Recommend per-repository opt-in, defaulting to managed-metadata for "server" destinations and E2E for third-party clouds.

---

## 4. Honest limitations and constraints

1. **SQLite single writer** with `_txlock=immediate` and `busy_timeout` 15 s (`store.go:37,40`). Every `Tx` serialises writes. Long transactions (tree purge, `DeleteUserRecords`, big snapshot commits, GC) stall chunk `MarkChunk` writes. After 15 s these fail as 500 "database is locked" (`writeErr` default branch, `server.go:150-152`). Backup catalog writes must be batched, and GC must run in small batches. **[H]** for the mechanism, **[M]** for impact.
2. **Per-user dedup only**: blob paths and PKs include `user_id` (`storage.go:160`, `migrations.go:89`). Cross-device dedup of the same user works for **whole identical files** only.
3. **Whole-file SHA-256 content addressing**: a 1-byte change to a 20 GB VHDX, PST or game archive re-uploads 20 GB through sync. Fixing that needs CDC, which belongs in the new backup repository, not in sync.
4. **8 MiB fixed upload chunks** are a transport unit only (resume granularity), not dedup units (`config.go:141-145`). Each upload also requires a full-size temp file on the target location plus a whole-file re-hash at complete (`uploads.go:427`). That doubles I/O for large files.
5. **Filesystem-only storage**: `storage.Manager` assumes POSIX/Win32 file semantics (rename atomicity, `OffsetWriter`, `statfs`, `*os.File` for `ServeContent`). There is no backend abstraction.
6. **Single process**: blob locks, the nonce cache, rate limiters, `lastTouch` and the settings cache are all in memory. Two server replicas against the same storage would be unsafe. A server restart rejects in-flight signed requests (`auth.go:277`).
7. **No server-side scheduler or automation runtime**: automations are evaluated on devices (`devices.go:299-316`). Server-owned periodic jobs exist only in `RunMaintenance` (10 min / hourly).
8. **No push channel to devices**: the server cannot initiate work on a device; it needs polling or long-polling (`/device/tasks`).
9. **Quotas are logical** and include trash (`users.go:227`, `commit.go:108`). Versions kept as trash consume quota without the user seeing them as versions.
10. **Signed device bodies are capped at 2 MiB** unless the route is whitelisted as streamed (`server.go:161,387-389,441`). Snapshot manifests, inventories and pack lists above 2 MiB need either the streamed object path or pagination.
11. **Per-IP rate limit** of 6000/min (burst 1200) (`server.go:62`). Many small-object backups from one NAT'd site share a bucket with that site's browsers.
12. **Container hardening** (`read_only`, `cap_drop: [ALL]`, distroless, `docker-compose.yml`) means no in-container SMB mounts. SMB must be host-mounted (already the documented pattern for `/storage/<name>`) or reached through a pure-Go SMB client. Any library must be pure Go (`CGO_ENABLED=0`).
13. **Activity logging swallows errors** (`misc.go:214-217`). This is fine for UX, but the backup audit trail of destructive actions (prune, unprotect) should use a checked insert in the same transaction.

---

## 5. Risks, verified bugs and tech debt

| # | Finding | Evidence | Confidence | Relevance |
|---|---|---|---|---|
| R1 | **Devices can permanently purge trash and delete files.** A stolen or compromised device key (or ransomware on the client) can destroy server-side history | `server.go:553-564` inside the `requireUser` group; `files.go:551-582` | H | Req 9 protected recovery points; fix before marketing "ransomware-safe" |
| R2 | **Zip download silently skips missing or unreadable blobs** and still returns 200 | `files.go:500-507` | H | Req 4 "never silently discard failures" |
| R3 | Chunk bytes are written to the temp file **before** hash verification. A corrupted retry of an already-received chunk overwrites good data; the bitmap still says received; finalize then fails with `hash_mismatch` and the upload is marked `failed` (permanent, `uploads.go:386-392`), wasting the whole upload. Web uploads without a declared sha256 and `X-Chunk-SHA256` are not verified at all and commit whatever arrived | `uploads.go:337-352`, `:110-118`, `:431` | H (mechanism), M (frequency) | Large backup uploads |
| R4 | The chunk handler checks `up.Status=="open"` from a non-transactional read (`uploads.go:288`) and then writes the file. A chunk racing with `complete` can write into the temp file while it is being hashed, or after rename into the blob inode (same file), corrupting a committed content-addressed blob. Requires a client bug or duplicate retry concurrent with complete | `uploads.go:284-339`, `storage.go:235` | L | Integrity; add an upload-level RW lock or re-check status under a lock |
| R5 | Maintenance cancels **expired** uploads even in `finalizing` state, deleting the temp file mid-finalize | `admin.go:645-649`, `store/uploads.go:203-205` | M | Rare (TTL is refreshed per chunk) but possible for very slow finalize of huge files |
| R6 | `copyFile` leaves `<sha>.copying` on crash; `CleanOrphans` ignores names that are not valid SHAs, so these leak forever | `storage.go:337`, `:409-412` | H | Disk leak |
| R7 | `GlobalMinFree` is written by the settings handler and read concurrently by `CheckSpace` without synchronisation (data race) | `admin.go:561`, `storage.go:206` | H (race exists), L (impact) | `go test -race` would flag it if covered |
| R8 | The settings PUT replaces the whole struct (`DisallowUnknownFields`), so an older cached web UI saving settings will reset new fields to zero values | `admin.go:551-564`, `server.go:166` | M | Any new settings |
| R9 | "Versions" are anonymous trash roots; the 30-day trash purge deletes all history and the user cannot list versions per file | `commit.go:91-95`, `admin.go:661-670` | H | Req 5 version history |
| R10 | No scheduled DB backup. File names and tree structure live only in SQLite | `main.go:325-345`, `storage.go:160` | H | Server-side DR |
| R11 | `PurgeTrash` across all users runs as one transaction during maintenance; a large trash can hold the write lock long enough to fail concurrent writes (>15 s busy timeout) | `nodes.go:557-602`, `store.go:37` | M | Scale |
| R12 | `testserver.Start` re-implements bootstrap and drifts from `main.go` (no `RecoverAfterRestart`, `ensureDefaultLocation`) | `testserver.go:41-76` vs `main.go:96-126` | H | New services must be wired twice |
| R13 | No unit tests for `internal/store`, `internal/storage` or `internal/auth`; behaviour is tested only through `internal/api/api_test.go` (8 test functions, `:212-811`) | file listing | H | Migration and GC work needs a store-level harness |
| R14 | `isUniqueErr` uses string matching; a driver message change breaks conflict detection | `store.go:141-143` | L | Low |
| R15 | `ListTrash` runs one recursive size query per trash root (N+1) | `nodes.go:468-486` | H | Slow with many versions in trash, which version-heavy sync makes worse |

---

## 6. Server workstreams with exclusive file ownership

Hotspots:

- `internal/api/server.go`: route registration, `Server` struct, `isStreamedBody`, guards.
- `internal/store/migrations.go`.
- `cmd/vaultdock/main.go`: wiring.
- `internal/testserver/testserver.go`.
- `internal/api/settings.go`.
- `internal/config/config.go`.

Strategy: **one integration owner (W0)** owns every hotspot and lands first. W0:

- adds a `Server` field per new service (or one `ext *Extensions` struct);
- adds **mount hooks** in `Handler()` that call per-feature functions (`s.mountDestinations(r)`, `s.mountRepos(r)`, …) inside the correct auth groups;
- creates each feature's route file as a **stub with an empty `mountX`**, which the feature owner then exclusively edits;
- reviews and lands **all new DDL** as migrations 2-7 in one go, so migration order never depends on merge order;
- extends `isStreamedBody` to a route-pattern list.

Feature workstreams only touch their own new files after that. If a feature later needs a schema or route-guard change, it goes through W0.

| WS | Scope | Modifies existing files | Creates new files |
|---|---|---|---|
| **W0 Integration and schema** | Hotspots, DDL, wiring, guards, device delete restriction (R1), feature flags in `/info` and `/device/me`, settings split, testserver parity (R12) | `internal/api/server.go`, `internal/store/migrations.go`, `internal/store/store.go` (only if a batch helper is needed), `cmd/vaultdock/main.go`, `internal/testserver/testserver.go`, `internal/api/settings.go`, `internal/config/config.go`, `internal/api/devices.go` (`handleDeviceMe` features), `Dockerfile`, `docker-compose.yml` (env for master key file and S3 defaults) | `internal/store/migrate_test.go` (v1→vN upgrade test with a seeded v1 DB); stub route files listed in W1-W7 (empty `mountX`, handed over immediately) |
| **W1 Object storage and secrets** | `objstore` interface and backends, secrets envelope, destinations CRUD and test | none | `internal/objstore/{objstore.go,localfs.go,s3.go,conformance_test.go,localfs_test.go,s3_test.go}`, `internal/secrets/{secrets.go,secrets_test.go}`, `internal/store/destinations.go`, `internal/store/destinations_test.go`, `internal/api/destinations.go` |
| **W2 Repository data plane and catalog** | Repos, object HEAD/PUT/GET/missing, locks, snapshot commit, catalog browse, diff and rebuild, protection | none | `internal/backup/{repo.go,catalog.go,locks.go,repo_test.go,catalog_test.go}`, `internal/store/{repos.go,snapshots.go,snapshots_test.go}`, `internal/api/{backup_objects.go,snapshots.go}` |
| **W3 Jobs, runs, issues, device tasks** | Job definitions, run progress, issue batches, Retry Skipped, device task queue with long-poll and approval, inventory projection | none | `internal/store/{backup_jobs.go,device_tasks.go,inventory.go,backup_jobs_test.go}`, `internal/api/{backup_jobs.go,device_tasks.go}` |
| **W4 Retention, prune, GC, scheduler** | Policy evaluation, dry-run plans, approval and execute, mark-and-sweep with grace, batching, scheduler runtime, DB auto-backup job | none | `internal/scheduler/{scheduler.go,scheduler_test.go}`, `internal/backup/{retention.go,gc.go,dbbackup.go,retention_test.go,gc_test.go}`, `internal/store/{retention.go,prune.go}`, `internal/api/prune.go` |
| **W5 Sync enhancements** | `file_versions`, version APIs, sync folders and rulesets, conflict policy storage, zip failure reporting (R2), trash N+1 (R15) | `internal/store/commit.go`, `internal/store/nodes.go`, `internal/api/files.go` | `internal/store/{versions.go,sync_folders.go,versions_test.go}`, `internal/api/{versions.go,sync_folders.go}` |
| **W6 Replication and verification** | Server-side pack scrub, replication policies and copy jobs (registered with the W4 scheduler through its public `Register` API), analytics | none | `internal/backup/{verify.go,replicate.go,analytics.go,verify_test.go,replicate_test.go}`, `internal/store/replication.go`, `internal/api/{replication.go,analytics.go}` |
| **W7 Readiness, notifications, AI** | Readiness report, notification prefs, AI proxy returning proposals that feed `device_tasks` or `prune_plans` approval | none | `internal/api/{readiness.go,notify_prefs.go,ai.go}`, `internal/store/notify_prefs.go`, `internal/ai/{ai.go,providers.go,ai_test.go}` |
| **W8 Hardening of existing upload and storage code** | R3, R4, R5, R6, R7, R11 fixes, plus first unit tests for storage, auth and store | `internal/api/uploads.go`, `internal/storage/storage.go`, `internal/api/admin.go`, `internal/store/uploads.go`, `internal/store/users.go` | `internal/storage/storage_test.go`, `internal/auth/auth_test.go`, `internal/store/uploads_test.go`, `internal/store/nodes_test.go` |

Files deliberately left with **no owner** (no change needed): `internal/api/auth_handlers.go`, `internal/api/automations.go`, `internal/api/misc.go`, `internal/api/updates.go`, `internal/api/web.go`, `internal/store/devices.go`, `internal/store/misc.go`, `internal/auth/auth.go`, `internal/storage/disk_*.go`.

`internal/api/api_test.go` is shared by every workstream, so it is a hotspot. Rule: **nobody edits it**. Each workstream adds its own `internal/api/<feature>_test.go` reusing `newEnv` (`api_test.go:43`). W0 may extract `newEnv` helpers into `internal/api/testenv_test.go` first, if needed.

Ordering: W0 lands first, then W1, then W2. W3 and W5 can run in parallel with W1/W2 because they depend only on W0. W4 and W6 depend on W2. W7 comes last. W8 can run any time; it only conflicts with nobody because W5 does not touch `uploads.go` (versioning hooks live in `commit.go`).

---

## 7. Interfaces with desktop, pkg and web

**Needed from pkg and desktop (other agents):**

- `pkg/repo` (new, shared Go module code): repository format spec and implementation. It covers config and key wrapping, pack format (header, Zstd, AEAD), object ID = SHA-256(stored bytes), keyed chunk IDs, index and snapshot JSON schema, and lock objects. The server imports it **only** for validation and cleartext fields. The desktop and a standalone restore CLI use the full implementation. A shared format package is essential so both sides agree byte-for-byte.
- `pkg/objstore` vs `internal/objstore`: the desktop needs the same backend semantics to write straight to S3 or SMB. Recommendation: put the `Backend` interface and the S3 and localfs implementations in **`pkg/objstore`** so the desktop can import them, and keep only server-specific wiring in `internal`. If the pkg agent agrees, W1's files move to `pkg/objstore/*`; ownership is unchanged.
- `pkg/client`: new methods for every device endpoint in §3.7. Streamed object PUT reuses the existing signed streamed-body mechanism (body SHA in a header, `server.go:438-439`).
- `pkg/protocol`: no change to signing. The server's streamed-route list grows (W0).
- `pkg/automation`: new trigger kinds (`backup.completed`, `backup.failed`, `schedule.*` for jobs) and action kinds (`backup.run`, `verify.run`, `replicate.run`, `prune.plan`). Destructive actions must emit *plans* that the server gates.
- Desktop: implements `/device/tasks` long-poll, run progress throttling (≤1 PATCH per 2 s per run), batch issue reporting, and lock refresh.

**Provided to web and desktop:**

- REST endpoints (§3.7) with the existing error envelope `{"error":{code,message,details}}` (`server.go:154-158`).
- New error codes: `repo_locked` (409), `object_hash_mismatch` (422), `pack_missing` (409 on snapshot commit, `details.missing=[...]`), `approval_required` (403), `protected_snapshot` (409), `backup_quota_exceeded` (413).
- Capacity contract: 503 + `Retry-After` + `X-VD-Capacity: saturated` (`uploads.go:318-320`) also applies to object PUT.
- Feature flags in `/info` and `/device/me`.
- Web: snapshot browse and search (managed mode), prune dry-run tables, analytics series, readiness report, approval inbox (device tasks and prune plans pending approval).

---

## 8. Testing plan (server)

1. **Migration safety** (W0): build a v1 DB with users, nodes, blobs, uploads and devices; run migrations to latest; assert row counts, FKs (`PRAGMA foreign_key_check`), `quick_check`, and that the server refuses to start on newer-than-binary (`store.go:78-80`). Add a contiguity test for the migration registry.
2. **Store unit tests** (W2-W5, W8): temp-dir SQLite per test. Cover refcount invariants (`blobs.refcount == count(nodes)+count(file_versions)` per sha), version pin and retention, snapshot commit idempotency, and lock expiry.
3. **objstore conformance** (W1): one suite run against `localfs` (always) and against MinIO/R2/B2 when env vars are set (CI optional job). Cover atomic Put (no partial object visible), hash-mismatch rejection, ranged Get, List pagination, Delete idempotency and marker health.
4. **GC safety** (W4), property and concurrency tests:
   - A pack uploaded but not yet committed is never deleted within the grace period.
   - A commit racing an exclusive prune either fails with `repo_locked` or is included in the live set.
   - Protected snapshots are never forgotten.
   - A dry-run plan equals what execute does when nothing changed in between.
   - Fault injection: kill between DB batch and object delete, then re-run and converge.
5. **Retention evaluation** (W4): table-driven tests on policy rules (keep-last/hourly/daily/weekly/monthly/yearly/within, tag filters, timezone and DST boundaries).
6. **API tests** (each WS, own `_test.go`): auth matrix per endpoint (anonymous, other user, device of another user, session without CSRF, admin), following the pattern of `TestQuotaAndIsolation` (`api_test.go:584`). Signed streamed PUT with a wrong body hash must fail. Bodies over 2 MiB on non-streamed device routes must return 413.
7. **Security regressions** (W0): a device cannot purge trash or delete snapshots (R1); unprotect needs re-auth; secrets are never echoed by GET; the master key file is missing or wrong, so start fails loudly (no silent plaintext).
8. **Crash and restart** (W2, W8): extend `tests/integration` (phase-1/phase-2 pattern, `tests/integration/integration_test.go:210,398`) with in-flight object uploads and snapshot commits across a restart, plus the nonce-cache restart behaviour.
9. **Race detector**: run `go test -race ./internal/...` in CI. It should catch R7 and any new shared state in the scheduler and locks.
10. **Load and contention** (W4, W8): parallel chunk uploads while a large trash purge or GC runs; assert no "database is locked" 500s (validates batching, R11).
11. **Server-independent restore test** (W2, with desktop and pkg agents): create a repo through the server, delete the server state dir, restore with the standalone `pkg/repo` CLI directly from storage. This is the acceptance test for §3.9.
12. **Upgrade compatibility**: an old desktop client (current `pkg/client`) against the new server must still sync, using the feature flags.
