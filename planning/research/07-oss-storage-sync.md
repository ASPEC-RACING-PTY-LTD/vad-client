# 07 - OSS research: storage backends, replication, credentials, continuous sync, AI integration

Scope: storage adapters (local disk, NAS/SMB, S3, Cloudflare R2, Backblaze B2, custom providers), replication and storage policies, credential storage, continuous-sync concerns (conflicts, version history, scheduling) and AI-assisted diagnostics patterns. Backup formats/chunking/crypto and Windows discovery/VSS are covered by other research documents.

Method: read-only. Licence and activity were checked on 2026-10-17 against the GitHub REST API (`license.spdx_id`, `pushed_at`, `archived`), the raw LICENSE files, and the commit Atom feeds (`https://github.com/<owner>/<repo>/commits/<branch>.atom`, latest `<updated>`). No external code was copied. Any statement not checked against a source this session is marked "(unverified)".

Requirement mapping (from `planning/REQUIREMENTS.md`): R5 continuous sync, R6 storage integrations, R7 storage analytics and GC, R8 automations and AI, R9 protected recovery points, offline operation, and restore without the management server.

Repo context observed (read-only): `internal/storage/storage.go` defines a `Manager` over SQLite `store.Location` rows. Every location is a local filesystem path. Blobs are placed with `Place`/`LinkExisting`, read with `Open` (which returns `*os.File`), cleaned with `CleanOrphans`, and space-checked with `CheckSpace`/`DiskSpace`. Any provider abstraction has to generalise "Location" without breaking these call sites. Today they assume a POSIX/NTFS path and a seekable `*os.File`.

---

## 1. Summary table

Activity = most recent push/commit date seen on 2026-10-17.

| Project | Lang | Licence (SPDX) | MIT? | Activity (verified) | Relevance | Recommended use |
|---|---|---|---|---|---|---|
| rclone/rclone | Go | MIT | Y | pushed 2026-10-17 | 70+ backends, `fs.Fs` abstraction, crypt, config encryption, bisync, librclone | Inspiration, plus an optional dependency for "long-tail" providers (see section 6) |
| minio/minio-go | Go | Apache-2.0 | N | pushed 2026-10-12 | Lean S3 client; works with S3/R2/B2/MinIO/Garage | Dependency candidate for the S3-compatible adapter |
| aws/aws-sdk-go-v2 | Go | Apache-2.0 | N | pushed 2026-10-14 | Official S3 client: object lock, conditional writes, checksums | Dependency candidate (alternative to minio-go) |
| Backblaze/blazer | Go | Apache-2.0 | N | commit 2026-10-15 (`main`) | Native B2 API client (large files, app keys) | Optional; prefer the S3-compat API instead |
| kurin/blazer | Go | Apache-2.0 | N | last commit 2023-05-22 | Original blazer | Avoid (stale; superseded by the Backblaze fork) |
| hirochachacha/go-smb2 | Go | BSD-2-Clause | N | commit 2026-10-11 | Pure-Go SMB2/3 client | Dependency candidate only for a "server talks SMB directly" mode |
| CloudSoda/go-smb2 | Go | BSD-2-Clause | N | pushed 2026-09-18 | Fork of go-smb2 (36 stars) | Watch only; prefer upstream |
| google/go-cloud (gocloud.dev/blob) | Go | Apache-2.0 | N | pushed 2026-10-14 | Portable blob API (S3, GCS, Azure, file) | Inspiration for interface shape; optional dependency |
| graymeta/stow | Go | Apache-2.0 | N | last commit 2021-01-21 (repo redirects) | Old multi-cloud abstraction | Avoid |
| flyteorg/stow | Go | Apache-2.0 | N | commit 2026-10-14 | Maintained stow fork | Inspiration only |
| thanos-io/objstore | Go | Apache-2.0 | N | pushed 2026-10-09 | Small `Bucket` interface for S3/GCS/Azure/filesystem | Inspiration (interface is close to what VaultDock needs) |
| syncthing/syncthing | Go | MPL-2.0 | N | pushed 2026-10-13 | Version vectors, conflict naming, versioning schemes | Inspiration only (design patterns) |
| haiwen/seafile | C | GPL-2.0 (+OpenSSL exception) | N | commit 2026-10-16 | Library/commit model, conflict naming | Inspiration only; do not link |
| nextcloud/desktop | C++ | GPL-2.0 | N | pushed 2026-10-17 | Conflict UX ("conflicted copy", badge) | UX inspiration only |
| bcpierce00/unison | OCaml | GPL-3.0 | N | pushed 2026-09-20 | Archive-based reconciliation | Conceptual inspiration only |
| mutagen-io/mutagen | Go | MIT (except `sspl/` = SSPL) | Partly | commit 2026-04-22 | Three-way merge, safe vs replica modes | Inspiration; avoid importing (SSPL parts, slowing activity) |
| robfig/cron | Go | MIT | Y | last commit 2021-01-06 | Cron parser/scheduler | Avoid as a new dependency (unmaintained) |
| go-co-op/gocron (v2) | Go | MIT | Y | pushed 2026-09-29 | Scheduler: singleton/limit modes, distributed locker, monitors | Dependency candidate if `pkg/automation` lacks a scheduler |
| anthropics/anthropic-sdk-go | Go | MIT | Y | pushed 2026-10-15 | Claude API, tool use | Optional dependency behind the AI provider interface |
| openai/openai-go | Go | Apache-2.0 | N | pushed 2026-10-17 | OpenAI API, tool calling | Optional dependency behind the same interface |
| modelcontextprotocol/go-sdk | Go | Apache-2.0 / MIT (relicensing transition) | Mixed | commit 2026-10-16 | Official MCP Go SDK | Later-phase option for exposing VaultDock tools over MCP |
| mark3labs/mcp-go | Go | MIT | Y | commit 2026-10-14 | Community MCP Go SDK | Alternative MCP option |

Sources (licence / activity):
- rclone: https://github.com/rclone/rclone , https://github.com/rclone/rclone/blob/master/COPYING
- minio-go: https://github.com/minio/minio-go , https://github.com/minio/minio-go/blob/master/LICENSE
- aws-sdk-go-v2: https://github.com/aws/aws-sdk-go-v2 , https://github.com/aws/aws-sdk-go-v2/blob/main/LICENSE.txt
- Backblaze/blazer: https://github.com/Backblaze/blazer/blob/main/LICENSE , https://github.com/Backblaze/blazer/commits/main.atom
- kurin/blazer: https://github.com/kurin/blazer/blob/master/LICENSE , https://github.com/kurin/blazer/commits/master.atom
- go-smb2: https://github.com/hirochachacha/go-smb2 , https://github.com/hirochachacha/go-smb2/commits/master.atom
- CloudSoda fork: https://github.com/CloudSoda/go-smb2
- go-cloud: https://github.com/google/go-cloud , https://pkg.go.dev/gocloud.dev/blob/driver
- stow: https://github.com/graymeta/stow (API returns "Moved Permanently"; last master commit 2021-01-21), https://github.com/flyteorg/stow
- objstore: https://github.com/thanos-io/objstore
- Syncthing: https://github.com/syncthing/syncthing (MPL-2.0)
- Seafile: https://github.com/haiwen/seafile/blob/master/LICENSE.txt (GPLv2 with OpenSSL linking exception)
- Nextcloud desktop: https://github.com/nextcloud/desktop
- Unison: https://github.com/bcpierce00/unison
- Mutagen: https://github.com/mutagen-io/mutagen/blob/master/LICENSE (MIT, except code under `sspl/`, which is SSPL; official builds since v0.17 include SSPL code by default)
- robfig/cron: https://github.com/robfig/cron , https://github.com/robfig/cron/commits/master.atom
- gocron: https://github.com/go-co-op/gocron
- anthropic-sdk-go: https://github.com/anthropics/anthropic-sdk-go
- openai-go: https://github.com/openai/openai-go
- MCP go-sdk: https://github.com/modelcontextprotocol/go-sdk/blob/main/LICENSE
- mcp-go: https://github.com/mark3labs/mcp-go/blob/main/LICENSE

Note: GitHub's API reports "NOASSERTION" for blazer, Seafile and Mutagen. The values in the table come from reading the LICENSE files directly.

---

## 2. Licence groups

### 2.1 MIT candidates

| Project | Implication |
|---|---|
| rclone | MIT, so it can be vendored or imported freely with attribution. Its main costs are dependency weight and API stability. `librclone` says its interface is "experimental and may change". Importing `github.com/rclone/rclone/backend/all` pulls in dozens of cloud SDKs, which inflates binary size and the CVE surface. Importing single backends is possible but still drags in `fs`, `lib/*` and config machinery. |
| go-co-op/gocron v2 | MIT and actively maintained. It suits per-job schedules (singleton mode to prevent overlapping runs of one backup job; limit mode for global concurrency caps). |
| robfig/cron | MIT, but the last commit was in 2021. Not a sound new dependency. Its cron-expression syntax is still the de facto standard, so keep expressions compatible with it. |
| anthropic-sdk-go | MIT. It can sit behind a provider-neutral interface. |
| mark3labs/mcp-go | MIT. Use it if VaultDock later exposes an MCP server. |
| Mutagen (non-`sspl/` code) | MIT, but it shares a repo with SSPL code, and activity has slowed (last commit April 2026). Treat it as design reading only. |

### 2.2 Other licences

| Licence | Projects | Implication for VaultDock |
|---|---|---|
| Apache-2.0 | minio-go, aws-sdk-go-v2, blazer, go-cloud, objstore, stow fork, openai-go, MCP go-sdk (new contributions) | Permissive and compatible with an MIT project. Ship a NOTICE/attribution and keep the licence text. Apache-2.0 includes a patent grant. Using these as libraries is fine. |
| BSD-2-Clause | go-smb2 | Permissive. Retain the copyright notice in distributed binaries (third-party notices file). |
| MPL-2.0 | Syncthing | File-level copyleft. Copying or modifying Syncthing files would make those files MPL. Use it for design inspiration only, or import unmodified packages (not recommended; Syncthing's internals are not a stable library API). |
| GPL-2.0 / GPL-3.0 | Seafile, Nextcloud desktop, Unison | Strong copyleft. Do not link, vendor or port code. Reading documentation and behaviour for UX ideas is fine. |
| SSPL | Mutagen `sspl/` | Not OSI-approved and has broad obligations. Avoid entirely. |

Practical rule: the VaultDock server and client can depend on MIT/BSD/Apache-2.0 libraries. Keep a generated `THIRD_PARTY_NOTICES` file (for example via `go-licenses`; tool choice not evaluated here). Keep GPL/SSPL/MPL projects to "read the docs" use.

---

## 3. Technical findings: storage providers

### 3.1 How existing projects abstract providers

**rclone `fs.Fs`** (read from `fs/types.go`, https://github.com/rclone/rclone/blob/master/fs/types.go):
- Core `Fs`: `List(ctx, dir)`, `NewObject(ctx, remote)`, `Put(ctx, in, src ObjectInfo, opts...)`, `Mkdir`, `Rmdir`, plus `Info` (Name, Root, Precision, Hashes, Features).
- `Object`: `Open(ctx, opts...)` (range reads come through `OpenOption`s), `Update`, `Remove`, `SetModTime`. `ObjectInfo.Hash(ctx, hash.Type)` exposes provider-native hashes (MD5, SHA-1, and others).
- Optional capabilities are separate small interfaces (`Copier`, `Mover`, `DirMover`, `Purger`, `PutStreamer`, `Abouter`, `SetTierer`/`GetTierer`, `Metadataer`, `ListRFn`). They are discovered through a `Features` struct with `Fill`/`Mask`/`Wrap` (https://pkg.go.dev/github.com/rclone/rclone/fs).
- Lessons: (1) keep the mandatory surface tiny and probe optional capabilities; (2) hashes are per-provider and may be missing or slow, so the caller must cope; (3) it models a file tree (directories, modtimes), which is more than a content-addressed blob store needs.

**gocloud.dev `driver.Bucket`** (https://pkg.go.dev/gocloud.dev/blob/driver):
- `Attributes`, `ListPaged`, `NewRangeReader(key, offset, length)`, `NewTypedWriter`, `Copy`, `Delete`, `SignedURL`, `Close`, plus `ErrorCode` (maps provider errors to portable codes such as NotFound and Unimplemented) and `As`/`ErrorAs` escape hatches to reach the native client.
- `WriterOptions.IfNotExist` gives a portable create-only write. `ContentMD5`, `BufferSize` and `MaxConcurrency` cover integrity and multipart tuning without exposing multipart in the API.
- Lessons: (1) a portable error taxonomy is essential; (2) hide multipart inside the writer; (3) escape hatches let you use object lock or other vendor features without polluting the core interface.

**thanos-io/objstore** (https://github.com/thanos-io/objstore): a small `Bucket` interface (Upload, Delete, Get, GetRange, Iter, Exists, Attributes, IsObjNotFoundErr) designed for immutable, content-addressed blocks. This is the closest match to VaultDock's SHA-256 blob model (method names from memory of the project README; unverified this session).

### 3.2 Proposed VaultDock provider interface (design sketch, not code)

Core (every provider implements these):

| Op | Semantics | Notes |
|---|---|---|
| `Put(key, reader, size, opts)` | Atomic: the object is visible fully or not at all | opts: expected SHA-256, content length, `IfNotExists`, metadata. The adapter chooses single-part or multipart internally. |
| `Get(key)` / `GetRange(key, off, len)` | Streaming reader | Replace `*os.File` call sites with `io.ReadSeekCloser` or `io.ReaderAt` (needed for HTTP Range in web preview/download). |
| `Stat(key)` | size, etag/native hash, modtime, lock/retention state | Must return a typed `ErrNotFound`. |
| `List(prefix, cursor)` | Paged, lexicographic | Needed for orphan GC and repair. Can be eventually consistent on some providers. |
| `Delete(key)` | Idempotent (deleting a missing key is fine) | Must surface `ErrLocked`/`ErrRetention` distinctly. |
| `Capabilities()` | static flags | `ConditionalPut`, `ObjectLock`, `ServerSideCopy`, `NativeSHA256`, `StrongListConsistency`, `Tiering`, `MaxObjectSize`, `MinPartSize`. |
| `Health()/Space()` | reachability, free/used, quota | Generalises `CheckSpace`/`LocationHealthy`. Object stores report "unknown/unbounded". |

Optional (probed by type assertion, rclone/gocloud style):
- `Locker`: `SetRetention(key, until, mode)`, `LegalHold(key, on)`, used for protected recovery points (R9).
- `Copier`: server-side copy, for replication within one provider or tier changes.
- `Signer`: presigned GET/PUT, used for desktop-direct uploads (section 6.3).
- `MultipartResumer`: expose upload IDs so a crashed upload can resume across process restarts. This matches the existing resumable chunk protocol and desktop queue.
- `Tierer`: set the storage class (S3 `STANDARD_IA`/Glacier, R2 `STANDARD_IA`).

Error taxonomy (gocloud-style): NotFound, AlreadyExists/PreconditionFailed, Locked/Retention, PermissionDenied/AuthExpired, Throttled (retryable with backoff), Transient (retryable), QuotaExceeded, Unimplemented. The upload queue's retry/skip logic (R4) keys on these codes, never on provider strings.

Key layout: keep the existing content-addressed SHA-256 naming (for example `u/<userID>/<sha[0:2]>/<sha>`). Content addressing makes `Put` idempotent and lets replication and verification compare by key and hash alone. Avoid listing-dependent logic on hot paths. Keep authoritative state in SQLite (which blob is on which destination, with what verified hash and when) and use `List` only for reconciliation and GC.

### 3.3 Multipart, conditional writes, consistency

- **Conditional writes:** AWS S3 supports `If-None-Match: *` on PutObject and CompleteMultipartUpload, and buckets can enforce conditional writes by policy (announced 2024-11-25). CreateMultipartUpload/UploadPart don't accept conditional headers. Sources: https://aws.amazon.com/about-aws/whats-new/2024/11/amazon-s3-enforcement-conditional-write-operations-general-purpose-buckets , https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes-enforce.html . R2 supports `If-Match`, `If-None-Match`, `If-Modified-Since` and `If-Unmodified-Since` on PutObject, evaluated atomically, so concurrent create-only writes give at most one success and 412 for the rest (https://developers.cloudflare.com/r2/api/s3/api/). B2 S3-compat conditional-write support was not confirmed this session, so treat it as absent and probe at runtime.
  - Use for VaultDock: content-addressed blobs barely need conditional writes (same key means same bytes). Mutable objects do need them: manifest heads, snapshot index pointers, lock/lease files for multi-writer repos. Design the manifest-head update as "write new immutable manifest, then conditionally swap a small pointer". Where `ConditionalPut` is unavailable, serialise through the server/SQLite (the single writer) instead of trusting the store.
- **Multipart:** B2 large files allow 5 MB to 5 GB parts with at least 2 parts, files up to 10 TB, a per-part SHA-1, and a recommended part size from `b2_authorize_account`. Ranged downloads of large files return no whole-file SHA-1 (https://www.backblaze.com/docs/cloud-storage-large-files). R2 replaces a re-uploaded part number, and a failed re-upload loses the original part (https://developers.cloudflare.com/r2/api/s3/api/). Recommendation: use a fixed part size per upload (S3-family rule: all parts except the last equal size), at least 8 MiB to match the current 8 MiB protocol chunk, and verify the end-to-end SHA-256 yourself rather than relying on ETags (multipart ETags are not content hashes).
- **Eventual consistency:** AWS S3 has been strongly read-after-write consistent since Dec 2020 (unverified this session). Do not assume this of every S3-compatible target (MinIO gateways, older NAS S3 servers, Wasabi-likes). Expose `StrongListConsistency` in `Capabilities()`. GC must use a grace period (the existing `CleanOrphans(minAge)` already does) and must never delete based on a single listing.

### 3.4 R2 / B2 / generic S3-compat quirks (adapter checklist)

| Topic | Cloudflare R2 | Backblaze B2 (S3 API) | Action in the adapter |
|---|---|---|---|
| Region | `auto`; empty or `us-east-1` treated as `auto` | Region embedded in endpoint (unverified detail) | Make the endpoint and region user-configurable. Default path-style off, but allow it for MinIO/NAS. |
| ACLs | Unimplemented | Bucket-level private/public-read only | Never send ACL headers. |
| Object lock | S3 object-lock headers/APIs unimplemented. Use R2 bucket locks (age/date/indefinite rules, prefix-scoped, up to 1,000 rules, configured via dashboard, Wrangler or the Cloudflare API `/lock` endpoint, not S3) | Object Lock supported via S3 and native API, governance or compliance mode, legal hold, can be enabled on existing buckets and not turned off | `Locker` capability: an S3 implementation for AWS/B2/MinIO and a separate Cloudflare-API implementation for R2 (needs a Cloudflare API token, which is a different credential from S3 keys). |
| Versioning | Get/PutBucketVersioning unimplemented | Buckets keep versions/hide markers (unverified detail) | Do not depend on provider versioning. VaultDock keeps its own snapshots. On B2, a "delete" may only hide a file, so lifecycle rules are needed to actually free space (and cost). |
| Checksums | CRC64NVME full-object only; CRC32/CRC32C/SHA1/SHA256 composite only | per-part SHA-1 on native large files | Do not use provider checksums as the integrity source of truth. VaultDock's SHA-256 (and per-chunk hashes) remains authoritative. Recent aws-sdk-go-v2 releases compute default request checksums; S3-compatible targets may reject them unless they are set to "when required" (see https://docs.aws.amazon.com/sdkref/latest/guide/feature-dataintegrity.html; per-provider behaviour unverified). |
| Tagging | Unimplemented | Rejected on Put/Copy | Store metadata in SQLite, not tags. |
| Storage class | `STANDARD`, `STANDARD_IA` | single class (unverified) | Tiering is a policy hint and must degrade gracefully. |
| SSE | SSE-C only | SSE-B2 and SSE-C | Client-side encryption (R9) makes SSE optional. Never rely on SSE for confidentiality from the provider. |

Sources: https://developers.cloudflare.com/r2/api/s3/api/ , https://developers.cloudflare.com/r2/buckets/bucket-locks/ , https://www.backblaze.com/docs/cloud-storage-s3-compatible-api , https://www.backblaze.com/docs/cloud-storage-object-lock .

**minio-go vs aws-sdk-go-v2:** minio-go is one module with a smaller dependency tree and is tuned for S3-compatible targets. aws-sdk-go-v2 tracks new AWS features first (conditional writes, checksums, object lock), but it is many modules and its defaults are AWS-centric (checksum behaviour above). Recommendation: pick one S3 client for all S3-family providers (AWS, R2, B2, MinIO, Wasabi, NAS S3) and treat each as a named profile (endpoint, region, path-style, capability overrides), not a separate adapter. minio-go is the lighter default. Revisit if AWS-only features are needed.

**Native B2 API (blazer):** not needed. The S3-compat API covers put/get/range/list/delete/multipart/object lock. Add blazer only if a B2-specific feature turns out to be missing (for example native `large_file_sha1`).

### 3.5 SMB/NAS: Go client vs OS-mounted shares

| Approach | Windows desktop client | Linux Docker server |
|---|---|---|
| OS-mounted share as a local path (UNC `\\nas\share` or mapped drive; CIFS mount or Docker volume on Linux) | Works with the existing `Location` path code. Credentials live in Windows Credential Manager. Service-account context matters: a Windows service does not see a user's mapped drives, so use UNC paths and per-service credentials. | Mount on the host (`mount -t cifs`) or with a Docker `cifs` volume driver and bind into the container. The container sees a plain directory, so there is zero new code. Caveats: atomic rename and fsync semantics on CIFS differ from local ext4, and `syncDir` on a CIFS directory may be a no-op. |
| In-process SMB client (go-smb2, BSD-2) | Rarely needed on Windows, where the OS client is better (Kerberos, SMB signing and encryption, DFS). | Lets users add "NAS" in the web UI without host mount privileges (Docker without `CAP_SYS_ADMIN`). Costs: VaultDock handles NTLM credentials, reconnects and DFS referrals (support in go-smb2 unverified), with less battle-testing than the kernel client. |

Recommendation: phase 1 treats NAS/SMB as a `local` provider pointing at an OS-mounted path (server: Docker volume or bind mount; client: UNC path), plus a health check that writes, fsyncs, renames and reads back a probe file to detect broken semantics. Phase 2, only if users need in-UI SMB without host mounts: an `smb` provider built on go-smb2 behind the same interface. Also note that many NASes expose S3 (MinIO, Synology C2/S3 apps, TrueNAS) or WebDAV, which the S3 profile or an rclone fallback can cover.

### 3.6 Replication strategies

| Strategy | Description | When |
|---|---|---|
| Synchronous fan-out | An upload is acknowledged only after N destinations have committed | Small, critical metadata (manifests, snapshot index). Too slow and fragile for bulk blobs; one slow cloud blocks the client. |
| Async primary + replicas (recommended for blobs) | Commit to the primary (local disk or NAS), then enqueue a replication task per (blob, destination) in SQLite. A worker pool copies with per-destination concurrency, bandwidth and backoff | Bulk data. Matches R2 "uploading must never wait" and the existing 503 back-pressure. |
| Server-side copy | Provider `Copy` when source and destination are the same provider or account | Tier moves, bucket-to-bucket. |
| Verify | Periodic `Stat` (size + native hash where available) is cheap. A sampled full read with SHA-256 recompute is costly (egress) | Cheap verify daily. Deep verify of a random sample, plus everything referenced by "protected" recovery points, on a slower cadence. |
| Repair | If a destination is missing or corrupt, re-copy from any healthy replica (choosing the cheapest egress source). If no healthy replica exists, mark the blob as lost and raise an actionable alert listing the affected snapshots/files (R4: never silent) | Triggered by verify, GC reconciliation, or a failed read. |

State model: a `blob_replicas(blob_id, destination_id, state[pending|uploading|present|verified|missing|corrupt|locked_until], last_verified_at, provider_etag, attempts, last_error)` table. "Present on N destinations" drives policy compliance and readiness reports (R10). GC deletes a blob from a destination only after (a) zero references across retained snapshots and sync history, (b) a grace period has passed, and (c) retention/lock has expired. A blob still under lock is marked `locked_until` and retried later, never treated as an error.

### 3.7 Storage policies

A policy is data, not code, and is evaluated by the replication scheduler:
- **Destinations per job:** each backup job or sync folder references a policy, and a policy lists destinations with a role (`primary`, `replica`, `archive`), a minimum copy count, and optional filters (job type, size, path class).
- **Tiering:** for example, "keep the latest 30 days on local and R2; older snapshots only on B2 or S3-IA". Implement as replica placement plus GC per destination, not as moving objects.
- **Protection:** "protected recovery points" means: on destinations with `Locker`, set retention until T on every blob and manifest the snapshot references. Because blobs are deduplicated, extend retention, never shorten it. A shared blob's lock is `max(all referencing protected snapshots)`. On destinations without lock support, protection is a VaultDock-side delete block only. Say so honestly in the UI.
- **Bandwidth/cost limits:** per-destination rate limits, upload windows (for example nightly only), and monthly egress caps.
- **Dry-run:** the policy engine produces a plan (copies to add, deletes, lock extensions, estimated bytes and cost) before applying. Reuse it for R7 dry-run previews.

### 3.8 Credential storage

Patterns observed:
- rclone stores per-remote secrets in `rclone.conf`. The `obscure` mechanism is reversible obfuscation, not encryption. Real protection is optional whole-config encryption with a user password (https://rclone.org/docs/#configuration-encryption ; https://rclone.org/commands/rclone_obscure/ , details unverified this session). Lesson: never present obfuscation as encryption.
- The VaultDock desktop already uses DPAPI for device keys (repo facts).

Recommended design:
1. **Server secret store (envelope encryption).** Each provider credential (S3 access key/secret, Cloudflare API token, SMB password, AI API key) is encrypted with a random per-secret DEK (AES-256-GCM or XChaCha20-Poly1305, with secret ID and owner as associated data). The DEK is wrapped by a KEK. KEK sources, in order of preference: an external KMS or `VAULTDOCK_MASTER_KEY` from a Docker secret or file mount; otherwise a key file generated on first run with 0600 permissions, stored outside the data directory if possible. Store only ciphertext, wrapped DEK, KEK ID and a non-secret fingerprint (last 4 characters) in SQLite. KEK rotation re-wraps DEKs only.
2. **User-managed keys.** Optionally, credentials can be additionally wrapped with a user passphrase (Argon2id, which is already in the stack). The server then cannot use them unattended. This suits "desktop-direct" destinations, where the client holds the creds and the server never sees them.
3. **Desktop:** DPAPI (CurrentUser, or LocalMachine for a service) for any credential the client holds. Prefer Windows Credential Manager for SMB.
4. **Least privilege guidance in the UI:** B2 application keys restricted to a single bucket and prefix, R2 tokens scoped to one bucket, an S3 IAM policy without `s3:BypassGovernanceRetention`, a separate credential for lock administration (R2 needs a Cloudflare API token anyway).
5. **Never log secrets:** a typed `Secret` wrapper whose `String()`/`MarshalJSON`/`LogValue` (slog) returns `[REDACTED]`. Scrub URLs (presigned query strings, `X-Amz-Signature`, `Authorization` headers) in HTTP debug logs. Exclude secrets from exports and diagnostics bundles, and from AI context (section 5).
6. **Write-only in the API:** after creation the UI shows a fingerprint, never the secret. Provide "test connection" and "rotate" actions.
7. **Recovery independence (R9):** restore without the server needs destination credentials and the repository key. Put this in the recovery kit: an encrypted export protected by the user's recovery passphrase, generated at setup.

### 3.9 Egress and cost considerations

- R2 advertises no egress fees, with charges per storage and per operation class (Class A writes/lists, Class B reads). B2 offers some free egress relative to stored data, and AWS S3 charges egress per GB. Check current pricing pages before shipping numbers in the UI (pricing not verified this session: https://developers.cloudflare.com/r2/pricing/ , https://www.backblaze.com/cloud-storage/pricing , https://aws.amazon.com/s3/pricing/).
- Design implications: (1) repair and deep verify should read from the cheapest-egress replica; (2) dedup GC and listing cost operations, so batch deletes (S3 `DeleteObjects`, up to 1000 keys) and avoid frequent full listings by relying on SQLite state; (3) small objects multiply per-request costs, so pack small chunks into larger pack objects (the chunking agent's decision; it matters for cost here); (4) S3 IA and Glacier tiers have minimum storage durations and retrieval fees, so warn when retention prunes objects early; (5) show estimated monthly cost per destination in storage analytics (R7) using user-editable price tables.

---

## 4. Continuous sync

### 4.1 Conflict detection

| Model | Used by | Pros | Cons |
|---|---|---|---|
| Version vectors per file | Syncthing BEP (https://docs.syncthing.net/specs/bep-v1.html, unverified detail) | Correct causality across N devices with no central clock | More metadata. Needs stable device IDs (VaultDock already has Ed25519 device identities). |
| Three-way merge against the last-synced ancestor | Mutagen (https://mutagen.io/documentation/synchronization), Unison | Simple for two endpoints. Detects "both changed since last agreement". | One ancestor record per endpoint pair. |
| Server revision + base revision (optimistic concurrency) | Nextcloud/ownCloud (ETags), Seafile (commit IDs) | Natural for hub-and-spoke (client to server), which is VaultDock's topology | The server is the arbiter, so offline clients reconcile on reconnect. |
| mtime + size only | naive tools | Cheap | Wrong under clock skew, FAT/exFAT 2-second mtime, and tools that preserve mtimes. Never sufficient alone. |

Recommendation for VaultDock (hub-and-spoke, server authoritative): each `nodes` row carries a monotonically increasing `revision` and the content SHA-256. Every desktop sync record stores `base_revision` and `base_sha` (the last state both sides agreed on). On upload, the client sends `If-Match: base_revision`. The server rejects with a conflict if its current revision differs and its content hash differs from the client's. Equal hashes are a "false conflict", so fast-forward silently. Locally, change detection uses mtime+size as a cheap trigger, confirmed by hash before declaring a change. This is effectively a three-way merge (base, local, remote) over a central server. Version vectors are only needed if peer-to-peer sync is ever added.

### 4.2 Conflict resolution and naming

Observed conventions:
- Syncthing: `<filename>.sync-conflict-<date>-<time>-<modifiedBy>.<ext>`. The file with the older mtime is renamed (ties broken by device ID). If a delete wins against a modify, the modified file is kept as a conflict copy. Conflict copies sync like normal files. Temp files use a `.syncthing.` prefix (`~syncthing~` on Windows) (https://docs.syncthing.net/users/syncing.html).
- Nextcloud desktop: the remote version keeps the original name, and the local edits become `name (conflicted copy <timestamp>).ext`. Conflicts are flagged via notifications and an "unresolved conflicts" badge. Conflict copies are not uploaded by default (https://docs.nextcloud.com/server/stable/user_manual/en/desktop/conflicts.html).
- Mutagen: in the safe modes, conflicts that would lose data are not auto-resolved. They are listed, and the user resolves by deleting the loser. Replica and one-way modes avoid conflicts by declaring a winner (https://mutagen.io/documentation/synchronization).

Recommended VaultDock behaviour:
- Never lose data. The server copy keeps the canonical name, and the local divergent copy becomes `<stem> (conflict <device-name> <YYYY-MM-DD HHMMSS>)<ext>`. Keep the extension last so apps still open it. Sanitize device names for Windows reserved characters. Respect `MAX_PATH`/long-path settings, falling back to a hash-shortened name the way Syncthing does.
- Per-folder conflict policy: `keep-both` (default), `prefer-server`, `prefer-local`, `newest-mtime` (opt-in, with a warning), and for one-way folders `replica` (Mutagen-style: the destination is overwritten, but the overwritten content goes to version history first).
- Modify vs delete: modify wins (resurrect), and the deletion is recorded in the activity log.
- Surface conflicts in a dedicated list (desktop tray badge plus web UI) with "keep mine / keep theirs / keep both / open both". This fits R4's actionable-warnings model.
- Configure whether conflict copies upload. Default: upload, so other devices and the version history see them (this differs from Nextcloud and is debatable; record it as an open decision).
- Exclude own temp-file prefixes and common editor temp patterns (`~$*`, `*.tmp`, `.~lock.*`) via the existing `desktop/internal/filter`.

### 4.3 Version history and retention

Syncthing's schemes (https://docs.syncthing.net/users/versioning.html): trash can (keep the last deleted/replaced copy, `cleanoutDays`), simple (`keep` N versions plus `cleanoutDays`), staggered (one version per 30 s for the first hour, per hour for the first day, per day for 30 days, then per week until `maxAge`), and external (a script). Versioning is per folder, archives only changes that arrive from other devices, uses a `.stversions` folder, and has `cleanupIntervalS`.

For VaultDock: version history is server-side. Superseded file contents are already content-addressed blobs, so keeping a version costs a `node_versions` row plus a GC reference, and no copy. Offer the Syncthing presets as retention policies on sync folders: `none`, `trash` (N days), `simple` (keep N), `staggered` (as above, with editable buckets), and `custom` (rules engine shared with R7 pruning). Apply retention on the server on a schedule (gocron job). GC respects references from sync versions and from backup snapshots alike.

### 4.4 Keep sync separate from DR snapshots

- Different intent: sync mirrors user edits, including deletions and ransomware-encrypted overwrites. DR snapshots are point-in-time and immutable.
- Separation rules: (1) separate namespaces and tables (`sync_folders`/`node_versions` vs `backup_jobs`/`snapshots`), both pointing at the shared deduplicated blob store; (2) sync retention can never prune blobs referenced by snapshots (shared reference counting or mark-and-sweep across both); (3) protected recovery points and object locks apply to snapshots, never to sync version history (version history is user-editable); (4) a sync-folder deletion propagates to other devices but never to snapshots; (5) UI: "Version history" (sync) and "Recovery points" (backup) are distinct screens with distinct restore semantics.
- Ransomware heuristic (optional, a natural AI-diagnostics input): a sudden mass-modify or rename with high entropy pauses sync propagation and asks the user, while snapshots continue.

### 4.5 Scheduling

- Triggers: filesystem watcher (existing engine), periodic rescan (watchers miss events on network shares and after sleep), cron schedules per folder or job, time windows ("only 22:00 to 06:00"), conditions (on AC power, on unmetered network, idle), and manual runs.
- Library: go-co-op/gocron v2 (MIT, active) offers cron/duration/daily/weekly/monthly/one-time jobs, singleton mode (no overlapping runs of the same job), a global limit mode, event listeners, monitors, and an optional distributed locker/elector (https://github.com/go-co-op/gocron). robfig/cron is MIT but unmaintained since 2021. First check whether `pkg/automation` already has a scheduler, and extend it rather than adding a second one.
- Persist the schedule definition and last/next run in SQLite. Missed runs (laptop asleep) use a catch-up policy: run once on wake, never N times.
- Server-side schedules (retention, replication, verify, GC) and client-side schedules (backup and sync jobs) are separate executors. They share the automation definition format so the visual builder (R8) edits both.

---

## 5. AI integration patterns (diagnostics and recommendations)

Requirement R8: optional user-supplied API keys, with approval gates for destructive operations.

### 5.1 Provider-agnostic interface

- A small internal `ai.Provider` interface: `Complete(ctx, messages, tools, opts) (response with text and tool calls)`, `Models()`, and `Validate()` (key test). Adapters for Anthropic (anthropic-sdk-go, MIT), OpenAI (openai-go, Apache-2.0), and an OpenAI-compatible base URL (covers local models such as Ollama or LM Studio, so users can keep data on-prem). Without a key configured, the feature is fully disabled and nothing is sent anywhere.
- Store the API key in the credential store (section 3.8) with an `ai` scope. Allow per-user or admin-only configuration.
- Tool definitions are VaultDock's own JSON-schema tools (same shape as MCP tools), so a later MCP server (MCP go-sdk or mcp-go) can expose the same catalogue to external agents.

### 5.2 What the model may do

- Read-only tools (no approval needed): `get_job_status`, `get_failure_summary(job, run)`, `get_destination_health`, `get_policy_compliance`, `get_storage_analytics`, `explain_error(code)`. These return structured, redacted data.
- Proposal tools (produce a plan, never act): `propose_retention_change`, `propose_exclusions`, `propose_schedule`, `propose_repair`. The output is a VaultDock dry-run plan object (the same one as R7 dry-run previews).
- Execution happens only through the normal API after the user approves the plan in the UI. The model never holds a token that can execute. This is stronger than "ask before calling the tool": the destructive path does not exist inside the AI loop at all.
- The MCP spec says there "SHOULD always be a human in the loop with the ability to deny tool invocations". It also advises clients to show tool inputs before calling, to prompt for confirmation on sensitive operations, to log tool usage for audit, and to treat tool annotations from untrusted servers as untrusted (https://modelcontextprotocol.io/specification/2025-06-18/server/tools). Use `readOnlyHint`/`destructiveHint`-style flags internally, but enforce them in server code, never through model instructions.

### 5.3 Approval gates

- Destructive classes: delete snapshots or versions, prune or GC, shorten retention, remove a destination, disable locks, overwrite on restore, rotate or delete credentials, change sync conflict policy to a lossy mode.
- The gate shows a diff or plan (counts, bytes, affected paths in redacted or real form for the local user, cost estimate) and requires explicit confirmation. For large deletions, use a typed confirmation or a second admin. The plan is bound to a hash. Approval executes exactly that plan and is invalidated if the state changes (time-boxed).
- Protected recovery points cannot be deleted via AI-originated plans at all. A manual admin flow is required.
- Prompt-injection hygiene: file names, log lines and error strings are untrusted input to the model. Wrap them as data, never let them alter tool permissions, and never auto-execute anything derived from them.

### 5.4 Redaction before sending

- Default "privacy mode": replace paths and names with stable tokens before sending (for example `C:\Users\Alice\Documents\Tax 2025\return.pdf` becomes `<PROFILE>\<DIR_17>\<DIR_42>\<FILE_311>.pdf`). Keep extensions and known-safe well-known folder classes (Documents, AppData, game-save root) because they carry diagnostic value. Mapping tables stay local and de-tokenise the model's reply for display.
- Always strip secrets (keys, tokens, presigned URLs, `Authorization` headers), email addresses, usernames and SIDs, hostnames and IPs (optionally), and file contents. Never send file contents by default.
- Show the user exactly what will be sent (a preview panel) on first use, with per-category toggles.
- Size-limit and aggregate: send histograms and error-code counts rather than raw logs where possible.

### 5.5 Audit log

Append-only rows: who, when, provider/model, a redacted prompt digest (hash plus stored redacted payload, optional), tool calls requested, plans proposed, approvals or denials with approver, executed actions and outcomes. Keep this separate from secrets and exclude it from AI context unless the user opts in. Surface it in the web UI next to automation run history.

---

## 6. Recommendation for VaultDock

### 6.1 Options compared

| Option | Pros | Cons |
|---|---|---|
| A. Own small provider interface (`local`, `s3` profile via minio-go or aws-sdk-go-v2, later `smb`) | Matches the content-addressed blob model exactly. Small dependency set. Full control of errors, retries, locks, metrics, back-pressure. Easy to unit-test with an in-memory fake (filling the current test gap in `internal/storage`). | You write and maintain adapters. Fewer providers out of the box. |
| B. rclone as a library (import `fs` + selected backends, or librclone RPC) | 70+ providers (Drive, OneDrive, Dropbox, SFTP, WebDAV, SMB, B2, Azure). Mature retry, pacing and multipart. MIT. | Huge transitive dependency tree and binary bloat. API is not semver-stable for library use (librclone explicitly experimental). File-tree semantics (dirs, modtime) mismatch blob keys. Global config/state patterns. Object lock not modelled uniformly. Harder to reason about errors. |
| C. gocloud.dev/blob | Clean portable API, `IfNotExist`, range reads, error codes, `As` escape hatch. Apache-2.0, active. | Mostly hyperscalers (S3, GCS, Azure, file, mem). No SMB/B2-native. Object lock only via `As`. Adds an abstraction layer over the S3 SDK that you might not need. |
| D. rclone as an external process (`rclone serve`/`rcd` or a `rclone` CLI sidecar) | Zero code coupling. User brings any rclone remote. | Process management. Another binary to update and secure. Credentials live in rclone.conf (obscured, not encrypted, unless config encryption is used). |

### 6.2 Recommended path

1. **Build option A** in `internal/storage`: a `Provider` interface (core ops + capabilities + optional `Locker`/`Copier`/`Signer`/`MultipartResumer`) and a provider registry keyed by type. The existing path-based `Manager` becomes the `local` provider. `store.Location` gains `type` and an encrypted `config_secret_id`. Refactor `Open` to return a range-capable reader. Keep `Place`'s "temp file, verify hash, commit" semantics: for object stores, the commit becomes "PUT with expected hash (+ `IfNotExists` when supported), then SQLite commit".
2. **One S3-family adapter** with per-provider profiles (AWS, R2, B2, MinIO/NAS, Wasabi, generic), with capability overrides from section 3.4. Use minio-go by default. R2 bucket-lock management is a separate small Cloudflare API client behind `Locker`.
3. **NAS/SMB** uses the `local` provider on OS-mounted paths first, with a semantic health probe. go-smb2 comes later only if needed.
4. **Extensibility ("custom providers"):** a stable internal interface plus a registration hook. For long-tail providers, offer an `rclone` provider later that shells out to a user-installed rclone (option D) or embeds selected rclone backends behind a build tag (option B), so the default binary stays lean. Document that `rclone` destinations get capability-degraded behaviour (no locks, list-based consistency).
5. **Replication, verify, repair and policy engine** live in the server (`internal/` new package, e.g. `internal/replication`) with SQLite state (section 3.6). Scheduling is reused from or added to `pkg/automation` (gocron v2 if a library is needed).
6. **Credential store**: a new `internal/secrets` (envelope encryption, KEK from Docker secret or file) used by storage and AI alike.

### 6.3 Where uploads happen: server-side vs desktop-direct

| Mode | Flow | Pros | Cons |
|---|---|---|---|
| Server-side (default) | Desktop uploads chunks to the VaultDock server (existing protocol). The server commits to its primary and replicates async to clouds. | One credential holder. Policies and dedup centralised. Works with the existing protocol. Desktop stays simple. | Server bandwidth and disk become the bottleneck. A home server on slow uplink delays offsite copies. |
| Desktop-direct via presigned URLs | The server authorises the upload and issues presigned PUT/multipart URLs (`Signer`). The desktop uploads encrypted chunks straight to S3/R2/B2. The server records completion after `Stat`/hash verification. | Offsite copy is not limited by server uplink. Credentials stay on the server. | Requires CORS-free native clients (fine for Wails/Go). Presigned multipart is fiddly. Verification costs a HEAD per object. |
| Desktop-direct with client-held credentials | The desktop holds destination creds (DPAPI) and uploads itself | Works with no server at all (supports R9 "restore without the management server"). Users with zero-trust needs keep keys off the server. | Credential sprawl across devices. Policy enforcement is client-side. |

Recommendation: keep server-side as the default (it fits the current architecture). Design the `Provider` interface and blob-key scheme so the same code compiles into the desktop module (`desktop/` has its own go.mod, so put the interface and S3 adapter in `pkg/` for sharing). Enable desktop-direct presigned uploads as phase 2 for cloud-primary users. Client-held credentials are reserved for the "server-independent recovery" path (the recovery kit can reconstruct access from the destination alone).

### 6.4 Open questions for planning

- Upload conflict copies by default (VaultDock-proposed) or keep them local (Nextcloud default)?
- Minimum KEK story for Docker users without a secrets manager: an auto-generated key file is acceptable, but it must not be backed up into the same destination unencrypted.
- Should protected recovery points require a lock-capable destination, or allow "soft protection" with a clear UI warning?
- Pack-file size for small chunks (coordinate with the chunking/format research) because of per-request pricing on R2/S3.
- Does `pkg/automation` already contain a scheduler and run-history model to extend (verify in code before choosing gocron)?
