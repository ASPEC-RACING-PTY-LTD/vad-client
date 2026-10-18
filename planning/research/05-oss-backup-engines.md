# 05 - Open-source backup engines, repository formats, chunking, compression and encryption

Research only. No external code was copied. All licence and activity facts below were
checked against the upstream source (GitHub `LICENSE` files, GitHub API and commit and
release Atom feeds, or official docs) on **2026-10-17**. In this file, "activity" means
the newest commit or push and the latest release seen on that date.

Scope: backup engines, repository and snapshot formats, content-defined chunking (CDC),
deduplication, Zstd compression, client-side encryption, integrity verification,
prune/GC, and restore that does not depend on the server. Out of scope (covered by other
agents): Windows app discovery, package restore, VSS, registry, storage adapters and sync.

---

## 1. Summary table

| Project | Lang | Licence (SPDX) | MIT? | Activity verified (2026-10-17) | Relevance | Recommended use |
|---|---|---|---|---|---|---|
| restic/restic | Go | BSD-2-Clause | N | push 2026-10-17; release v0.19.1 (2026-07-05) [1] | Very high: reference for a documented pack/index/snapshot format | Architectural inspiration (format spec, invariants). Cannot be imported as a library (`internal/`). Optional external restore tool |
| restic/chunker | Go | BSD-2-Clause | N | commit 2026-06-20; v0.5.0 [2] | High: Rabin CDC, 512 KiB/1 MiB/8 MiB | **Dependency candidate** (permissive) |
| rustic-rs/rustic (+ rustic_core) | Rust | Apache-2.0 (rustic); MIT OR Apache-2.0 (rustic_core) | rustic_core: Y (dual) | push 2026-09-01; v0.11.4 (2026-08-18) [3][4] | Medium: second implementation of the restic format | Inspiration only (Rust, API "subject to change") |
| kopia/kopia | Go | Apache-2.0 | N | push 2026-10-16; v0.23.1 (2026-06-16) [5] | Very high: Go library with packs, indexes, manifests and time-based GC safety | Inspiration; embedding is feasible but not recommended (see §4) |
| borgbackup/borg | Python/C | BSD-3-Clause | N | push 2026-10-14; stable 1.4.5 (2026-07-18), 2.0.0b25 pre-release [6] | Medium: buzhash CDC params, append-only ideas | Inspiration only |
| gilbertchen/duplicacy | Go | Custom proprietary-ish (no SPDX; "free for personal use", paid commercial CLI) | N | push 2026-10-13; v3.2.5 (2025-05-03) [7] | High: lock-free two-step "fossil" GC | **Avoid as code or dependency**. Algorithm idea only |
| duplicati/duplicati | C# | MIT (plus a `proprietary/` carve-out) | Y | push 2026-10-16; v2.4.0.1 stable (2026-10-07) [8] | Low-medium: fixed-block, .NET, Windows UX | Inspiration (UX, Windows) only |
| andrewchambers/bupstash | Rust | MIT | Y | last push 2024-02-16; v0.12.0 (2022-11) [9] | Medium: per-item encryption, untrusted server | Inspiration only. **Inactive** |
| PlakarKorp/plakar (+ kloset) | Go | ISC | N (ISC is MIT-equivalent) | plakar push 2026-10-16, v1.1.7; kloset push 2026-10-16 [10][11] | High: modern Go, immutable store, ptar archives | Inspiration. kloset could be a dependency, but its API is young |
| PlakarKorp/go-cdc-chunkers | Go | ISC | N (MIT-equivalent) | push 2026-10-15; v1.1.0 (2026-06) [12] | High: FastCDC, UltraCDC, JC, **keyed FastCDC** | **Dependency candidate** |
| jotfs/fastcdc-go | Go | Apache-2.0 | N | last commit 2020-06-05; v0.2.0 [13] | Low | **Avoid** (unmaintained) |
| klauspost/compress (zstd) | Go (pure) | BSD-3-Clause (plus Apache-2.0 and other notices for sub-packages) | N | push 2026-10-16; v1.20.3 (2026-10-12) [14] | Very high: pure-Go Zstd used by restic and kopia | **Dependency** (preferred) |
| DataDog/zstd | Go + cgo | BSD-2-Clause ("Simplified BSD") | N | commit 2026-07-27; wrapper 1.5.7 patch 3 [15] | Medium | Avoid (cgo complicates the Wails/Windows cross-build) |
| FiloSottile/age (filippo.io/age) | Go | BSD-3-Clause | N | push 2026-08-29; v1.3.2 [16] | High: key wrapping and recovery files, spec in C2SP [17] | **Dependency candidate** for key and recovery-file wrapping |
| golang.org/x/crypto | Go | BSD-3-Clause | N | commit 2026-10-16 [18]; **already in VaultDock go.mod (v0.58.0)** | Very high: chacha20poly1305, hkdf, argon2, blake2b | **Dependency** (already present) |
| minio/sio (DARE) | Go | Apache-2.0 | N | commit 2026-04-29; v0.5.1 [19] | Medium: streaming AEAD with random access | Optional dependency or design reference |
| openziti/secretstream | Go | MIT | Y | commit 2026-09-10 [20] | Low-medium: libsodium secretstream port | Not needed (per-blob AEAD is simpler) |
| jedisct1/libsodium | C | ISC | N | commit 2026-10-15 [21] | Reference only | Avoid (cgo) |
| zeebo/blake3 | Go (asm) | CC0-1.0 | N | commit 2026-09-03; v0.2.4 (2024-08) [22] | Medium: fast chunk hashing | Optional dependency |
| lukechampine/blake3 | Go | MIT | Y | last commit 2025-05-08; v1.4.1 [23] | Medium | Optional (MIT, but quieter than zeebo) |
| etcd-io/bbolt | Go | MIT | Y | push 2026-10-16 [24] | Low: local index store option | Optional. VaultDock already uses SQLite, so prefer that |
| Arq 5/7 data format docs | spec | proprietary docs (format published) | n/a | docs online [25][26] | Medium: an openly published commercial format | Inspiration for documentation only |
| systemd/casync | C | LGPL-2.1 (no SPDX detected by GitHub) | N | last push 2025-09-22 [27] | Low | Avoid |

Raw data used for the table: GitHub API `repos/{owner}/{repo}` (licence SPDX and `pushed_at`)
for restic, rustic, kopia, borg, duplicacy, duplicati and bupstash. The API rate limit was
then reached, so the remaining repos were checked through
`https://github.com/{owner}/{repo}/commits.atom`, `/releases.atom` and the raw `LICENSE` files.

---

## 2. Licence classification

### 2.1 MIT candidates (SPDX `MIT`, or dual-licensed with MIT)

| Project | Notes |
|---|---|
| rustic_core | `MIT OR Apache-2.0` [4]. Rust, so not linkable from Go. Its API is self-described as early-stage. Useful as a second clean implementation of the restic format, for cross-checking a spec reading. |
| duplicati | MIT for everything outside `proprietary/` [8]. C#/.NET. Use for ideas only (Windows UX, fixed-block approach, local SQLite job database). The licence moved from LGPL to MIT; read the file before quoting older material. |
| bupstash | MIT [9]. Interesting crypto model (the server cannot read data, separate keys for metadata and data, "put-only" keys). Inactive since 2024. |
| openziti/secretstream | MIT [20]. A pure-Go libsodium secretstream. Only relevant if VaultDock wants libsodium wire compatibility, which it does not need. |
| lukechampine/blake3 | MIT [23]. Pure Go plus asm BLAKE3. Low commit rate. |
| etcd-io/bbolt | MIT [24]. Embedded KV store, an option for a client-side blob index. |

ISC (PlakarKorp/plakar, kloset, go-cdc-chunkers; libsodium) is functionally equivalent to
MIT/BSD-2: you keep the notice and may use it freely. It is listed under "Other" only
because the brief asks for strict MIT separation.

### 2.2 Other licences

**Permissive (Apache-2.0, BSD-2/3, ISC, CC0). Usable as Go deps if notices are shipped:**

- restic (BSD-2-Clause), restic/chunker (BSD-2-Clause), borg (BSD-3-Clause),
  klauspost/compress (BSD-3-Clause, with Apache-2.0 and other notices inside some
  sub-packages, so ship its full LICENSE), DataDog/zstd (BSD-2-Clause), filippo.io/age
  (BSD-3-Clause), golang.org/x/crypto (BSD-3-Clause), kopia (Apache-2.0), minio/sio
  (Apache-2.0), jotfs/fastcdc-go (Apache-2.0), plakar/kloset/go-cdc-chunkers (ISC),
  zeebo/blake3 (CC0-1.0).
- Apache-2.0 obligations: include LICENSE and any NOTICE file, and mark modified files.
  It carries an explicit patent grant, which is a plus. A Go binary is a "binary form",
  so the Wails installer and the server binary need a third-party-notices file. A
  `go-licenses`-style report generated at build time covers this.
- BSD-3-Clause adds a no-endorsement clause: do not use "restic", "borg" and so on in
  marketing as an endorsement.

**Copyleft / restrictive:**

- **GPL/AGPL:** none of the shortlisted backup engines are GPL. (Duplicati was LGPL in the
  past.) If VaultDock is closed-source or not yet licensed, linking GPL code into the
  server or the Windows client would force the combined binary under the GPL. AGPL would
  also cover the network-served server. Avoid both.
- **LGPL** (casync, old Duplicati): dynamic linking is workable in C, but Go links
  statically by default, so LGPL relinking obligations are awkward in practice. Avoid.
- **Duplicacy custom licence** [7]: not OSI. It is "free for personal use"; commercial
  backup use needs paid per-computer licences, and "commercial use of derivative works is
  subject to the same requirements". **Do not use its code or depend on it.** The
  *algorithm* (two-step fossil collection) is described publicly in its wiki [28].
  Algorithms are not copyrightable, but write any implementation independently from a
  description, never from the code.
- **SSPL / BSL / Elastic-style:** none found among the candidates. Do not adopt such
  components, because they restrict offering the software as a service.

---

## 3. Architecture lessons (concrete)

### 3.1 Repository layout: packs, indexes, snapshots, trees

**restic (documented format, v2)** [29]:
- A top-level `config` (encrypted JSON: `version`, `id`, `chunker_polynomial`), plus
  `keys/`, `locks/`, `snapshots/`, `index/` and `data/<2-hex>/`. Every file except
  `config` and `keys/*` is named by the **SHA-256 of its stored (encrypted) bytes**, so
  `sha256sum` alone detects bit-rot without the password.
- A pack is `EncBlob1 || ... || EncBlobN || EncHeader || HeaderLen(4 bytes, LE)`. The
  header goes **at the end**, so a pack can be streamed out while it is filled. Each blob
  is encrypted **independently**, so repacking can copy blobs without re-encrypting, and
  a single blob can be read with a ranged GET. Header entries per blob are type
  (data/tree, compressed or not), encrypted length, plaintext length and plaintext hash.
- Index files are encrypted JSON (zstd-compressed in v2) mapping blob ID to pack, offset,
  length and uncompressed length. Each index file is kept below about 8 MiB.
- Trees are JSON documents listing nodes. Each file node holds its ordered list of
  content blob IDs, and each directory node points to a subtree blob ID. A snapshot
  points to one root tree. This is a Merkle DAG, so unchanged subtrees share their IDs
  between snapshots.
- Default pack size: **16 MiB**, tunable with `--pack-size` [30].

**kopia** [31][32]:
- Three layers: BLOB storage → content-addressable block storage (CABS: contents packed
  into blobs) → content-addressable object storage (large objects split into contents
  and joined by "indirect" JSON objects). Snapshots and policies live in a
  label-addressed manifest store.
- Blob prefixes: `p` (data packs), `q` (metadata packs: directories, manifests, indirect
  objects), `x`/epoch index blobs, `n` (sessions/locks-like). **Metadata packs are kept
  separate from data packs**, so a client can cache all metadata locally and only ever
  range-read data packs. Contents are combined into packs of roughly 20-40 MB.
- Each pack also carries a **local index at its end**, so the global index can be rebuilt
  from packs if the index blobs are lost. restic gets the same property from its pack
  header.
- Defaults (source): hash `BLAKE2B-256-128`, encryption `AES256-GCM-HMAC-SHA256`,
  splitter `DYNAMIC-4M-BUZHASH` [33].

**Arq 7** [26]: separates `treepacks/`, `blobpacks/` and `largeblobpacks/`, and keeps
`standardobjects/` for unpacked items. Items up to `maxPackedItemLength` (sample 256,000
bytes) get packed. Per-folder `backuprecords/` carry the **per-file error list with a
severity**. That maps directly onto VaultDock requirement 4 (never silently discard
failures; a "Retry Skipped" workflow).

**Lessons for VaultDock:**
1. Packs hold independently-AEAD'd blobs, with the header at the end. A full index is
   also embedded in each pack, so the repository can always be rebuilt from packs.
2. Keep separate pack classes for **metadata** (trees, manifests, job manifests,
   app-inventory JSON) and **data**. Then browsing in the web UI, `diff` and restore
   planning only need metadata packs, which can be cached locally.
3. Name stored objects by the SHA-256 of their ciphertext. This fits VaultDock's existing
   `blobs/<user>/<aa>/<bb>/<sha256>` layout and its hash-verified upload, with no change
   to the server's integrity model.
4. Write order is **packs → index → snapshot**. Read order is **snapshots → index →
   packs** (restic invariants, §3.6).

### 3.2 CDC parameters and why

| Engine | Algorithm | min / avg / max |
|---|---|---|
| restic | Rabin fingerprint, 64-byte window, random irreducible polynomial per repo | 512 KiB / ~1 MiB / 8 MiB. Files < 512 KiB are not split [29] |
| kopia (default) | Buzhash32, `DYNAMIC-4M` | 2 MiB / 4 MiB / 8 MiB (min = avg/2, max = avg×2 in source) [33] |
| borg 1.x/2 | buzhash (4095-byte window), fastcdc optional | 512 KiB (2^19) / ~2 MiB (mask 21 bits) / 8 MiB (2^23) [34] |
| duplicacy | rolling hash, variable | 1 MiB / 4 MiB / 16 MiB. Fixed size if min=avg=max [35] |
| plakar | go-cdc-chunkers (FastCDC, UltraCDC, JC, keyed FastCDC) [12] | configurable |

Why these values:
- **Average size trades dedup ratio against index size and request count.** Halving the
  average roughly doubles the number of index entries and the per-object overhead (AEAD
  tag, header entry). It improves dedup for files with small scattered edits (VM images,
  PST files, game saves, databases).
- **min** stops tiny chunks, which bloat the index and give poor compression. **max**
  bounds memory and the read amplification of a single blob.
- **Max = 8 MiB** shows up in restic, kopia and borg. It also matches VaultDock's 8 MiB
  upload chunk, so one blob never needs more than one upload chunk.
- **Keyed or secret CDC:** Alexeev, Percival and Zhang (2025) show that an attacker who
  sees chunk sizes for a known file can recover a secret Rabin polynomial. restic 0.18
  mitigated this by randomly assigning chunks to packs [29]. Lesson: derive the CDC
  key/gear table from the repo master key, **and** shuffle or mix blobs across packs so
  the storage provider cannot infer chunk boundaries from pack layout.

**Recommendation:** FastCDC (normalised chunking, keyed gear table) with min 512 KiB,
avg 1 MiB and max 8 MiB, in line with restic and borg. Files under min are stored as one
blob and packed with others. Allow a per-job override, for example avg 4 MiB for large
media or VM jobs. The chunker parameters must be recorded in the repo config and become
immutable, since changing them destroys dedup.

### 3.3 Dedup scope

- restic, kopia and borg dedup across **the whole repository**: every host, every path
  and every snapshot sharing one repository key. Duplicacy explicitly sells
  cross-machine dedup without locks [7].
- Dedup reaches only as far as the keyed blob ID does. A blob ID is a keyed hash (or a
  plaintext hash inside an encrypted index), so two repositories with different master
  keys never dedup against each other. That is the price of client-side encryption.
- **Recommendation for VaultDock:** one repository per *user* per *destination*. All
  per-application, per-folder and per-project **jobs** share it and dedup against each
  other. Each job keeps its own snapshot stream and manifest (requirement 2), tagged with
  `job_id`, `device_id` and `kind` (files / appdata / app-inventory / project).
  Cross-user dedup is impossible with per-user keys and is not wanted.
- Keep the existing **sync** blob space (plaintext SHA-256, server-visible) logically
  separate from **backup** repositories (requirement 5). Do not try to dedup backup blobs
  against sync blobs. Doing so would require either plaintext hashes visible to the
  server or server-side decryption.

### 3.4 Compression before encryption

- restic v2 compresses each blob (and index/snapshot JSON) with zstd **before**
  encryption, and records the uncompressed length in the pack header [29]. Its levels are
  `off`, `fastest`, `auto` (default), `better` and `max` [30]. kopia compresses per
  content before encryption. Arq 7 uses LZ4 before AES [26].
- Ciphertext does not compress, so compress first. Use **per-blob** compression with a
  flag in the header so incompressible blobs (JPEG, ZIP, game archives) can be stored raw.
  A cheap heuristic is to try zstd-fastest on the first 64 KiB and keep the result only
  if the saving is at least about 5-10%.
- Compression leaks plaintext compressibility through ciphertext length (CRIME-class).
  For backups at rest this is generally accepted, but document it. Never mix
  attacker-controlled and secret data in one compressed blob, which is a non-issue for
  file chunks.
- **Library:** `github.com/klauspost/compress/zstd`, pure Go, used by restic (go.mod
  shows `klauspost/compress v1.20.0`) [36]. Reuse one encoder per worker and set
  `WithEncoderConcurrency(1)`; the default spins up a goroutine per CPU per encoder,
  which is costly with many parallel jobs. Bound decoder `WithDecoderMaxMemory`.

### 3.5 Key hierarchy (master key, passphrase and recovery wrapping)

**restic:** a random master key (AES-256 + Poly1305 key) is stored encrypted in
`keys/<id>` JSON files. Each file wraps the same master key under one password via scrypt
(N, r, p and salt stored). Several key files mean several passwords. The docs state that
a leaked master key requires re-encrypting the whole repository [29].

**kopia:** password → KDF → format/encryption keys stored in `kopia.repository`, with
AEAD contents (`AES256-GCM-HMAC-SHA256` default) [33].

**Arq 7:** a random keyset (encryption key, HMAC key, blob-ID salt) in
`encryptedkeyset.dat`, wrapped with PBKDF2-SHA256 at 200,000 rounds [26]. This shows an
explicit **salted, keyed blob identifier**.

**age** [17]: a random 128-bit file key, wrapped in header "stanzas" per recipient
(X25519 or scrypt passphrase). The payload uses HKDF-derived keys and ChaCha20-Poly1305
STREAM in 64 KiB chunks with a last-chunk flag (protects against truncation). **An scrypt
stanza must be the only stanza in the header**, so you cannot wrap one file to both a
passphrase and a public key.

**Recommended VaultDock hierarchy:**
```
RepoMasterKey (32 random bytes, generated on the client, never sent in clear)
 ├─ HKDF("vd/blob-enc")   → K_enc   : XChaCha20-Poly1305 per blob/object (random 24B nonce)
 ├─ HKDF("vd/blob-id")    → K_id    : blobID = HMAC-SHA256(K_id, plaintext) (or keyed BLAKE3)
 ├─ HKDF("vd/cdc")        → K_cdc   : gear table seed for keyed FastCDC
 └─ HKDF("vd/meta")       → K_meta  : (optional) separate key for metadata packs
Key-wrap files  keys/<id>  (one file per unlock method, restic-style, NOT one age header):
  - passphrase  : Argon2id (already used by VaultDock web auth) → KEK → AEAD(RepoMasterKey)
  - recovery key: 256-bit random, shown once as a word list + QR / printable PDF → KEK
  - device keys : per-device X25519 (or age recipient), so scheduled backups run unattended
                  without the passphrase; private key protected by DPAPI as device Ed25519 is today
```
- Use **XChaCha20-Poly1305** (golang.org/x/crypto, already a dependency). Its random
  192-bit nonces are safe across many parallel writers and devices. AES-GCM's 96-bit
  random nonces risk collisions at very high message counts.
- Use one AEAD call per blob, with associated data = blob type plus blob ID, so blobs
  cannot be swapped between slots.
- The server never receives RepoMasterKey or the passphrase. The recovery key is the only
  path when the passphrase is lost. Losing both means losing the data, and the UX must
  say so plainly (requirement 10, recovery-readiness).
- Rotation: "change password" means re-wrapping only. Rotating the master key means
  re-encrypting everything (as restic documents). Optionally support key *epochs*: new
  blobs use the new key while old blobs stay readable, and GC or repack migrates them over
  time.

### 3.6 Integrity checks (check / verify, partial reads)

- **Structural check** (restic `check`, kopia `snapshot verify`/`content verify`):
  load all snapshots, walk trees, and confirm every referenced blob exists in the index
  and every indexed pack exists in storage (size and listing only).
- **Data check**: download and decrypt packs and verify each blob's MAC and hash. restic
  supports `--read-data-subset` (n/m or a percentage) so a rolling sample of the
  repository is checked on each run [37]. kopia has `--verify-files-percent` [38].
  VaultDock should schedule a **rolling verification**, for example 1-5% of packs per
  day, so that the whole repository is re-read every N weeks. It should also run
  **restore tests** (requirement 9: "recovery testing"), which restore a random job
  snapshot to a temp dir and compare hashes.
- **Cheap server-side check without keys:** objects are named by the SHA-256 of their
  ciphertext, so the VaultDock server (or a NAS/S3 lifecycle job) can verify stored bytes
  against names with no keys. That matches the existing server-side hash verification.
- **Partial reads:** per-blob AEAD and per-pack offsets allow ranged GETs of single
  blobs. Restoring one file reads only the packs (and byte ranges) it needs. The existing
  VaultDock download path should support `Range`, and S3/R2/B2 do.
- **Repair:** pack-embedded headers make `rebuild-index` possible after index loss.
  Snapshots that reference missing blobs should be flagged, not deleted. restic has
  `repair snapshots`; follow the same idea.

### 3.7 Prune / GC with concurrent writers and locking

Three proven strategies:

1. **restic: locks plus mark-and-sweep.** Backups take non-exclusive lock files and
   prune takes an exclusive one. Locks are stale after 30 minutes, or if the PID is gone
   on the same host. A new lock is written, the client waits, then re-checks for
   conflicting locks [29]. Removal rules: remove a pack from the index *before* deleting
   it, and when rewriting, write the new pack, then the new index, then delete the old
   pack. The weakness: an exclusive lock over object storage is best-effort, and prune
   blocks all backups.
2. **kopia: time-based safety, no global exclusive lock for backups** [32][39]. Only a
   designated **maintenance owner** runs GC. `SafetyFull` parameters in source:
   `MinContentAgeSubjectToGC=24h`, `MarginBetweenSnapshotGC=4h`,
   `RequireTwoGCCycles=true`, `PackDeleteMinAge=24h`,
   `DropContentFromIndexExtraMargin=1h`, `RewriteMinAge=2h`,
   `SessionExpirationAge=96h`. Unreferenced contents are first *marked deleted* in the
   index, and their packs are removed only after two GC cycles and a minimum age. Any
   concurrent writer that re-uses a content during that window "undeletes" it.
3. **duplicacy: two-step fossil collection, lock-free** [28]. Unreferenced chunks are
   renamed to "fossils" and recorded in a fossil-collection file. They are deleted only
   after every snapshot ID has produced a new snapshot that started after the
   collection. Backups never reference fossils, so they re-upload. Restore and check may
   read fossils.

**Recommended for VaultDock:** a hybrid that uses the server's position as a coordinator.
- The VaultDock server already has SQLite (WAL) and per-user `blobs(refcount)`. Make the
  **server the lock and lease authority** for repositories it hosts: a "backup session"
  row with a heartbeat, and a "maintenance lease" row. This is far more reliable than
  lock files on S3. For third-party destinations (S3/NAS) with no VaultDock server
  involved, fall back to restic-style lock objects **plus** kopia-style time margins.
- GC algorithm (client-driven, because only the client has keys):
  1. Take the maintenance lease. Snapshot the list of all snapshot manifests, then load
     the index (in that order).
  2. Mark reachable blob IDs by walking trees.
  3. Packs that are 100% unreferenced **and** older than the safety age (24h or more) go
     to "pending delete" (a fossil list). Delete them in the *next* GC run, if no backup
     session that started before the mark phase is still open.
  4. Packs that are partly referenced, under a threshold (for example under 50%
     useful): repack the live blobs into new packs → write the new index → mark the old
     packs pending delete.
- **Optional server-assisted GC without keys:** each snapshot manifest also uploads a
  *plaintext list of the pack object IDs it references*. Pack IDs are already opaque
  ciphertext hashes. The server can then count pack refcounts, show real storage
  analytics, block deletes of referenced packs (protected recovery points, requirement 9)
  and run the final physical delete, while repacking still needs the client.
  Trade-off: it leaks which snapshots share packs, a coarse dedup ratio. Note this in the
  threat model.
- **Dry-run** (requirement 7): steps 1-3 without deletes produce exactly "N packs / X GiB
  would be freed; these snapshots are removed by the retention rule". Make a dry-run
  mandatory before any AI-suggested destructive operation (requirement 8 approval gates).

### 3.8 Incremental snapshot strategies

- Every snapshot is a **full logical snapshot** (a complete tree). Increments come free
  from dedup: unchanged files point to existing blobs, and unchanged directories reuse the
  same tree blob ID (Merkle). No full/incremental chains exist, so any snapshot can be
  deleted independently. restic, kopia, borg and duplicacy all do this.
- **Change detection without reading data:** compare against the *parent snapshot* of the
  same job, using (path, size, mtime, ctime/inode). restic uses its parent snapshot
  plus inode/ctime; kopia keeps per-directory metadata. On Windows/NTFS use **file ID
  (FILE_ID_INFO), size, LastWriteTime and ChangeTime** (ChangeTime catches metadata-only
  changes). Optionally use the **USN change journal** for fast detection across large
  trees (that is the Windows agent's scope; noted here only as an input).
- Untouched-file fast path: reuse the previous node's content list, without even reading
  the file. Offer a periodic "deep" run that rehashes everything, to catch silent mtime
  preservation (some apps reset mtime).
- Per VaultDock requirement 2, each job has its own parent pointer, so jobs stay
  independent and can run in parallel.

### 3.9 Many small files

- Pack small files: restic does not split files under 512 KiB but still packs them, and
  Arq packs items of 256,000 bytes or less [26][29].
- One tree blob per directory gives a natural batching unit. Large directories (node_modules,
  game asset folders) can produce multi-MB tree JSON. kopia and restic accept this.
  Consider splitting tree blobs past about 1-4 MiB.
- Parallelism: a file-read worker pool (I/O-bound) feeds a chunk/compress/encrypt pool
  (CPU-bound), which feeds a pack assembler (one or a few open packs per job), which feeds
  the upload queue. Bound every stage so memory stays flat.
- Upload granularity: many tiny objects are expensive (one request each, and per-object
  costs on S3/B2). Target packs of 16-32 MiB. Over VaultDock's 8 MiB chunk protocol, that
  is 2-4 hash-verified chunks per pack, uploaded in parallel and resumable per chunk.
- Uploads must start as soon as **one pack is sealed**, never at the end of a job or run
  (requirement 2). Interrupted runs resume because packs are durable once uploaded. Write
  an index **checkpoint** every N packs or M minutes, so a crash loses at most the unsent
  tail. The final snapshot manifest is written last.

### 3.10 Client cache and index

- restic keeps an **encrypted local cache** of index, snapshot and tree data [29]. kopia
  caches metadata packs (`q`) and index blobs locally [31].
- Memory: a full in-memory blob index costs roughly 50-100 B per blob (32 B ID + pack ref
  + offset/len + map overhead). At 1 MiB average chunks, **1 TiB ≈ 1M blobs ≈ 50-100 MB
  RAM**, which is acceptable on desktop. At 10 TiB, or with small chunks, it is not.
  Options: (a) keep VaultDock's local SQLite (`desktop/internal/state`) as an on-disk
  index keyed by blob ID; (b) memory-map sorted index files with a Bloom/xor filter in
  front (kopia's compact binary index is an example). Prefer (a) for simplicity, with an
  in-memory Bloom filter for the "already have it?" hot path.
- The cache must be **disposable**: the repository alone is the source of truth (§3.11).

### 3.11 Restore without the original server

- restic and kopia repositories are **self-describing**: copy the directory or bucket
  anywhere and a stock binary plus the password restores everything. That is the gold
  standard for VaultDock requirement 9.
- Arq publishes its full data format [25][26] so customers can restore without Arq. It
  is a good model for *documentation*. Its gaps (no test vectors, typos) are the lesson:
  ship known-answer test vectors.
- **For VaultDock:**
  1. The repository is a plain object tree: `config`, `keys/`, `snapshots/`, `index/`,
     `packs/{data,meta}/<aa>/...`, plus `README-RESTORE.txt` (unencrypted, no secrets)
     that names the format version and the restorer tool.
  2. Ship a **standalone `vaultdock-restore` binary** (single static Go binary for
     Windows/Linux/macOS) that reads that layout from a local folder, SMB path or S3
     endpoint, with no server and no account. Use the passphrase or the recovery key.
  3. On the VaultDock server's own storage, backup objects live in
     `blobs/<user>/<aa>/<bb>/<sha256>`, and the repo metadata (which object is config,
     key, index or snapshot) lives in SQLite. Provide an **"export repository"**
     operation (and use the same layout for replication to S3/NAS) that writes the
     self-describing layout. Alternatively store repos in their own location prefix,
     `<location>/repos/<user>/<repo-id>/...`, so the disk *is* a restorable repo even if
     the server's DB is lost. **The second option is strongly preferred**, because
     disaster recovery must not depend on the server's SQLite.
  4. Write a format spec (`docs/backup-format.md`) with byte layouts and test vectors,
     versioned like restic's `version: 2`.

---

## 4. Build vs embed decision

### Option A: embed kopia as a Go library (Apache-2.0)
- Feasibility: kopia exposes non-`internal` packages (`repo`, `repo/blob`,
  `snapshot`, `snapshot/snapshotfs`), and a custom `blob.Storage` implementation could
  talk to VaultDock's upload API. Licence is fine (Apache-2.0 with NOTICE).
- Pros: battle-tested format, GC safety model, index, caching, verification, and a
  free external restore tool (the kopia CLI and KopiaUI) for requirement 9.
- Cons: v0.x with **no API stability promise** (it is an application whose packages
  happen to be importable). go.mod pins a recent toolchain (`go 1.27`, toolchain
  `go1.27.2`) and has a large dependency graph (about 139 versioned require lines),
  which pulls the desktop module's toolchain and dependencies along [40]. Its
  maintenance-owner model, policy and manifest system overlap VaultDock's own job and
  automation model ("avoid duplicate subsystems"). It is hard to give the server pack
  refcounts for analytics or protected recovery points without forking. Per-job error
  records, app inventory and Windows-specific metadata would sit in side manifests.
- Verdict: viable fallback, **not recommended** as the primary path.

### Option B: drive restic (or rustic) as an external binary
- restic's Go packages live under `internal/` [41], so Go forbids importing them. The
  only integration is spawning `restic --json` with a REST-server or rclone-style backend.
- Pros: zero format work, superb independent restore (restic and rustic both read the
  format), BSD-2.
- Cons: two processes and two progress models. VaultDock's per-job queues, retries,
  skip/skip-all and 503 back-pressure would be mediated through CLI JSON. Exclusive-lock
  prune blocks all jobs. Bundling an external binary complicates updates (VaultDock has
  its own self-updater) and code signing.
- Verdict: **not recommended** for the core. Possibly an "advanced/export" integration.

### Option C: own Go implementation of the *restic v2 repository format* (written from the published spec)
- Pros: any repository VaultDock writes can be restored with stock restic or rustic, so
  independent restore is guaranteed by two external tools. The spec is public and
  detailed [29].
- Cons: inherits restic's choices (AES-256-CTR + Poly1305-AES, scrypt, unkeyed SHA-256
  blob IDs inside an encrypted index, JSON trees, lock files). There is no room for
  server-side pack refcounts or VaultDock-specific job and error manifests, except as
  ignored extra files. Compatibility must be continuously tested against restic releases.
  Clean-room discipline is required (implement from `design.rst`, not from code).
- Verdict: an interesting **strategic alternative**. Choose it only if "restorable with
  a third-party open tool" outweighs a VaultDock-optimised format.

### Option D (recommended): own VaultDock repository format built from small permissive libs
Libraries:
- **Zstd:** `github.com/klauspost/compress/zstd` (BSD-3-Clause).
- **CDC:** `github.com/PlakarKorp/go-cdc-chunkers` (ISC; FastCDC with a keyed variant) or
  `github.com/restic/chunker` (BSD-2-Clause; Rabin, 512K/1M/8M). Prefer go-cdc-chunkers
  for keyed FastCDC. Because the chunker defines the format, **pin its version and
  freeze test vectors**: a library change that shifts boundaries silently breaks dedup.
  A small in-house FastCDC is also realistic, since the algorithm is published.
- **Crypto:** `golang.org/x/crypto` (already present): chacha20poly1305 (XChaCha), hkdf,
  argon2id. Optionally `filippo.io/age` (BSD-3-Clause) for device-key or recovery-file
  wrapping and for **exported encrypted single-file restores**. minio/sio is not needed.
- **Hash:** stdlib `crypto/sha256` (hardware-accelerated on modern CPUs) for object
  names; HMAC-SHA256 for blob IDs. BLAKE3 (`zeebo/blake3`, CC0) only if profiling shows
  hashing is the bottleneck.

How it maps onto existing VaultDock pieces:
- **Content-addressed blob store:** each **pack** (and index, snapshot or key file) is
  an opaque encrypted object whose name is the SHA-256 of its ciphertext. That is exactly
  the identity the server already verifies for `blobs`. The server never sees blob-level
  (chunk) IDs; those live inside encrypted pack headers and indexes. Store packs under a
  dedicated per-repo prefix (§3.11 point 3) rather than mixing them into the sync
  `nodes` tree, and track them in a new `backup_objects(repo_id, sha256, kind, size,
  created_at, pending_delete_at)` table instead of overloading `blobs.refcount`.
- **8 MiB chunk upload protocol:** reuse it unchanged. Create session → PUT 8 MiB chunks
  → complete, with the pack SHA-256 as the expected hash. Pack target 16-32 MiB means 2-4
  chunks. Blob max 8 MiB means a single chunk-boundary blob never spans more than 2
  upload chunks. 503 back-pressure and resume work as they do today. Add a cheap
  **"have objects?" batch endpoint** so a restarted job can skip packs already uploaded.
- **Desktop SQLite queue** (`desktop/internal/state`): holds per-job state (files done,
  packs sealed or uploaded, skipped-file list with error and severity, à la Arq backup
  records) plus the local blob index (§3.10).
- **Protocol package** (`pkg/protocol`): new messages for repo, snapshot manifest
  (encrypted body + plaintext pack-ID list), leases and object listing.

Trade-offs of D:
- Pros: fits per-job independence, the server as coordinator, server-side analytics and
  protected recovery points, Windows metadata and app-inventory manifests, and
  VaultDock's error model. Small dependency set with only permissive licences. Full
  control of the UX for progress and retries.
- Cons: **VaultDock owns a crypto and storage format forever.** It needs a written spec,
  test vectors, fuzzing of parsers, an external crypto review, a standalone restorer and
  backward-compatible readers for every format version. Expect more engineering than
  embedding.
- Mitigation: copy the *proven invariants* (restic write and read ordering, kopia GC
  safety windows, duplicacy two-phase delete), keep the format deliberately boring (JSON
  or CBOR metadata, simple binary pack header), and stay open to Option C later. A
  "restic-compatible export" tool could convert a VaultDock snapshot into a restic repo
  if third-party restorability becomes a requirement.

---

## 5. Risks

| Risk | Detail | Mitigation |
|---|---|---|
| Format lock-in | A custom format is only as recoverable as its restorer and spec | Versioned spec plus test vectors in-repo. A standalone restorer published with every release. Never break readers. "Restore test" automation. An optional restic-format export (Option C) |
| Chunker drift | A CDC lib update changes boundaries, so dedup silently collapses | Pin the version, store the chunker id and params in repo config, keep golden-vector tests, vendor-review the lib on update |
| Nonce misuse | Parallel writers on several devices with a shared key | XChaCha20-Poly1305 with random 192-bit nonces. Never a counter shared across processes |
| Unkeyed blob IDs | Plain SHA-256 chunk IDs allow confirmation-of-file attacks by anyone who sees IDs | HMAC or keyed IDs (Arq salts blob identifiers [26]). IDs only inside encrypted index or headers |
| CDC side channel | Chunk sizes reveal the secret polynomial or gear table (2025 paper, cited by restic [29]) | Keyed gear table, random blob-to-pack assignment, padding of tiny packs optional |
| Compression-length leak | Ciphertext length reveals compressibility | Accept and document. Optional padding buckets for metadata |
| Key loss | A lost passphrase means unrecoverable data (by design) | Mandatory recovery-key step at setup, a recovery-readiness report, a periodic "verify you can unlock" prompt |
| Key compromise | restic docs: a leaked master key requires a full re-encrypt [29] | Key epochs so new data uses a new key, with background repack. Device keys revocable (remove the wrap file) |
| GC data loss | Prune races a concurrent backup and deletes newly referenced packs | Server-held leases, kopia-like minimum ages (24h, two GC cycles [39]), two-phase delete (fossils [28]), strict write/read ordering [29], dry-run first |
| Eventual consistency (S3-likes) | Listings lag writes | Time margins (kopia `DisableEventualConsistencySafety=false` default), never delete objects newer than X |
| Index memory | About 50-100 B per blob in RAM; 10 TiB at 1 MiB avg is about 1 GB | On-disk SQLite index plus a Bloom filter; larger avg chunk for big-file jobs; metadata pack cache on disk |
| zstd memory | klauspost encoders allocate per-CPU goroutines and buffers by default | `WithEncoderConcurrency(1)`, pooled encoders, `WithDecoderMaxMemory` |
| Many-tiny-objects cost | S3/B2 per-request pricing and slow listings | 16-32 MiB packs, metadata/data pack split, index compaction |
| Licence creep | A transitive GPL or proprietary dep (e.g. duplicacy code) | Run `go-licenses` checks in CI. Allowlist MIT/BSD/ISC/Apache/CC0 |
| Server-side DB as single point of failure | Repo identity stored only in the VaultDock SQLite | Self-describing on-disk repo layout (§3.11); the server DB is a cache and coordinator |
| Toolchain coupling (if Option A) | kopia go.mod `go 1.27` forces the desktop toolchain [40] | Avoided by Option D |

---

## References

[1] restic (GitHub API: BSD-2-Clause; pushed 2026-10-17; release v0.19.1 2026-07-05): https://github.com/restic/restic , https://github.com/restic/restic/blob/master/LICENSE , https://github.com/restic/restic/releases
[2] restic/chunker (LICENSE BSD-2-Clause; commits.atom 2026-06-20; v0.5.0): https://github.com/restic/chunker , https://github.com/restic/chunker/blob/master/LICENSE
[3] rustic (GitHub API: Apache-2.0; pushed 2026-09-01; v0.11.4 2026-08-18): https://github.com/rustic-rs/rustic
[4] rustic_core (MIT OR Apache-2.0; "early development stage", API "subject to change"): https://github.com/rustic-rs/rustic_core
[5] kopia (GitHub API: Apache-2.0; pushed 2026-10-16; v0.23.1 2026-06-16): https://github.com/kopia/kopia , https://github.com/kopia/kopia/blob/master/LICENSE
[6] borg (LICENSE BSD-3-Clause text; pushed 2026-10-14; 1.4.5 2026-07-18; 2.0.0b25): https://github.com/borgbackup/borg/blob/master/LICENSE , https://github.com/borgbackup/borg/releases
[7] duplicacy (LICENSE.md custom: personal free, commercial paid; pushed 2026-10-13; v3.2.5 2025-05-03; README "lock-free"): https://github.com/gilbertchen/duplicacy/blob/master/LICENSE.md , https://github.com/gilbertchen/duplicacy
[8] duplicati (LICENSE MIT with `proprietary/` carve-out; pushed 2026-10-16; v2.4.0.1_stable_2026-10-07): https://github.com/duplicati/duplicati/blob/master/LICENSE , https://github.com/duplicati/duplicati/releases
[9] bupstash (GitHub API MIT; pushed 2024-02-16; v0.12.0 2022-11-07): https://github.com/andrewchambers/bupstash
[10] plakar (LICENSE ISC; commits.atom 2026-10-16; v1.1.7): https://github.com/PlakarKorp/plakar , https://github.com/PlakarKorp/plakar/blob/main/LICENSE
[11] kloset (LICENSE ISC; commits.atom 2026-10-16): https://github.com/PlakarKorp/kloset
[12] go-cdc-chunkers (LICENSE.md ISC; commits.atom 2026-10-15; v1.1.0; FastCDC, UltraCDC, JC, keyed FastCDC): https://github.com/PlakarKorp/go-cdc-chunkers
[13] jotfs/fastcdc-go (Apache-2.0; last commit 2020-06-05): https://github.com/jotfs/fastcdc-go
[14] klauspost/compress (LICENSE BSD-3-Clause + Apache-2.0 sections; commits 2026-10-16; v1.20.3): https://github.com/klauspost/compress , https://github.com/klauspost/compress/blob/master/LICENSE
[15] DataDog/zstd (LICENSE "Simplified BSD"; commit 2026-07-27): https://github.com/DataDog/zstd
[16] age (LICENSE BSD-3-Clause; commit 2026-08-29; v1.3.2): https://github.com/FiloSottile/age , https://github.com/FiloSottile/age/blob/main/LICENSE
[17] age format spec (C2SP): https://github.com/C2SP/C2SP/blob/main/age.md
[18] golang.org/x/crypto (BSD-3-Clause; commit 2026-10-16): https://github.com/golang/crypto , https://github.com/golang/crypto/blob/master/LICENSE
[19] minio/sio (Apache-2.0; commit 2026-04-29; v0.5.1; random-access ranges): https://github.com/minio/sio
[20] openziti/secretstream (MIT; commit 2026-09-10): https://github.com/openziti/secretstream
[21] libsodium (ISC; commit 2026-10-15): https://github.com/jedisct1/libsodium
[22] zeebo/blake3 (CC0-1.0; commit 2026-09-03; v0.2.4): https://github.com/zeebo/blake3
[23] lukechampine/blake3 (MIT; last commit 2025-05-08; v1.4.1): https://github.com/lukechampine/blake3
[24] etcd-io/bbolt (MIT; commit 2026-10-16): https://github.com/etcd-io/bbolt
[25] Arq 5 data format: https://www.arqbackup.com/arq_data_format.txt
[26] Arq 7 data format: https://www.arqbackup.com/documentation/arq7/English.lproj/dataFormat.html
[27] casync (last push 2025-09-22): https://github.com/systemd/casync
[28] Duplicacy lock-free deduplication (two-step fossil collection): https://github.com/gilbertchen/duplicacy/wiki/Lock-Free-Deduplication
[29] restic design doc (format v2, packs, index, keys, locks, read/write ordering, CDC 512 KiB-8 MiB, threat model incl. chunking-attack mitigation in 0.18): https://github.com/restic/restic/blob/master/doc/design.rst
[30] restic tuning (compression levels, 16 MiB default pack size): https://github.com/restic/restic/blob/master/doc/047_tuning_parameters.rst
[31] kopia architecture: https://kopia.io/docs/advanced/architecture/
[32] kopia maintenance: https://kopia.io/docs/advanced/maintenance/
[33] kopia defaults in source: https://github.com/kopia/kopia/blob/master/repo/splitter/splitter.go , https://github.com/kopia/kopia/blob/master/repo/splitter/splitter_buzhash32.go , https://github.com/kopia/kopia/blob/master/repo/encryption/encryption.go , https://github.com/kopia/kopia/blob/master/repo/hashing/hashing.go
[34] borg chunker constants: https://github.com/borgbackup/borg/blob/master/src/borg/constants.py
[35] Duplicacy chunk size details: https://forum.duplicacy.com/t/chunk-size-details/1082
[36] restic go.mod (klauspost/compress, restic/chunker deps): https://github.com/restic/restic/blob/master/go.mod
[37] restic check `--read-data-subset`: https://restic.readthedocs.io/en/stable/045_working_with_repos.html
[38] kopia snapshot verify: https://kopia.io/docs/reference/command-line/common/snapshot-verify/
[39] kopia GC safety parameters (source): https://github.com/kopia/kopia/blob/master/repo/maintenance/maintenance_safety.go
[40] kopia go.mod: https://github.com/kopia/kopia/blob/master/go.mod
[41] restic internal packages (not importable): https://github.com/restic/restic/tree/master/internal
