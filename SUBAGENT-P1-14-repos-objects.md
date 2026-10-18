# P1-14 (SRV-2) Repos and objects, stored with localfs under `backup/`
Status: completed (one test owned by another task needs a one-line update: see Notes)

## Goal
Implement the api-v1 §2 capabilities route and the §5 repository and object routes in `vad-server`, with the Rev 1.1b resolutions:
- a built-in server destination created on demand;
- atomic config replace, guarded by an exclusive lock;
- 422 `object_hash_mismatch` and 400 `body_mismatch` kept separate;
- no object DELETE route.

The format §2 key layout is stored with localfs under `<default storage>/backup/`.

## Changes (repo: path, created/modified/deleted, why)
All changes are in vad-server.
- `internal/objstore/replace.go` (created): the optional `Replacer` interface. The `Store` interface is unchanged.
- `internal/objstore/localfs/replace.go` (created): the localfs `Replace`. It writes a temp file, verifies its size and hash, then renames it over the old object atomically.
- `internal/objstore/localfs/replace_test.go` (created): runs the Replacer conformance suite.
- `internal/objstore/objstoretest/replace.go` (created): `RunReplacer` conformance suite, plus the `ReplaceableStore` interface it tests.
- `internal/backup/repo.go` and `repo_test.go` (created): key mapping per format §2, validation of uuid, kind and oid, and the repo object `Store`. Local stores are opened through `OpenLocal`, which caches one store per root.
- `internal/store/repos.go` and `repos_test.go` (created): the built-in destination, repositories, usage, object rows, missing, quota, and a read-only lock check.
- `internal/api/backup_objects.go` (replaced the P0-03 stub): every route of this area.
- `internal/api/backup_objects_test.go` (created): integration tests.
- `internal/api/backup_objects_internal_test.go` (created): Range parsing tests.

## Interfaces exposed
`objstore`:
- `type Replacer interface { Replace(ctx, key string, r io.Reader, size int64, sha256hex string) error }`

`backup`:
- `const DirName = "backup"`
- `var ErrNotReplaceable`
- `ValidRepoUUID(string) bool`
- `ValidKind(string) bool`
- `ValidOID(kind, oid string) bool`
- `ObjectKey(repoUUID, kind, oid string) (string, error)`
- `New(objstore.Store) *Store`
- `OpenLocal(root string) (*Store, error)`
- `(*Store).Backend() objstore.Store`
- `(*Store).Put(ctx, repoUUID, kind, oid, r, size) error`: refuses config.
- `(*Store).PutConfig(ctx, repoUUID, r, size, sha) error`
- `(*Store).Get`, `GetRange` and `Stat`
- `(*Store).ConfigSHA256(ctx, repoUUID) (string, bool, error)`

`store`:
- Types: `Repository`, `RepositoryUsage`, `RepoObject`, `RepoLockHolder`.
- Errors: `ErrRepoExists`, `ErrBackupQuota`. `CreateRepository` also uses the existing `ErrConflict` when the uuid belongs to another user.
- `const BuiltinDestinationName = "VaultDock server"`
- `ServerDestination(ctx, defaultRoot) (id, root, err)`
- `DestinationTarget(ctx, id, userID) (kind, root, err)`
- `CreateRepository(ctx, *Repository) (created bool, err)`
- `Repository(ctx, id)`
- `ListRepositories(ctx, userID, afterID, limit)`
- `RepositoryUsage(ctx, repoID)`
- `RepoObject(ctx, repoID, kind, oid)`: live rows only.
- `CheckBackupQuota(ctx, userID, add)`
- `PutRepoObject(ctx, repoID, kind, oid, size) (existed bool, err)`
- `ListRepoObjects(ctx, repoID, kind, afterID, limit)`
- `MissingRepoObjects(ctx, repoID, kind, oids)`
- `HoldsExclusiveRepoLock(ctx, repoID, deviceID) (held bool, other *RepoLockHolder, err)`

Routes, all mounted in the stub's auth groups:

| Route | Auth | Behaviour |
|---|---|---|
| `GET /backup/capabilities` | dev, sess | Returns the §2 values. |
| `POST /repos` | dev | 201 when created, 200 on replay. `destination_id` omitted or 0 uses the built-in destination. 422 `validation` for bad input. 409 `conflict` when the uuid belongs to another user. 409 `repo_exists` with `details.repo_id` when the user already has a repo on that destination. 501 for non-server destinations. |
| `GET /repos`, `GET /repos/{id}` | dev, sess | Cursor = last id. Usage counts live objects plus committed snapshots. Another user's repo gives 404 `repo_not_found`. |
| `HEAD /repos/{id}/objects/{kind}/{oid}` | dev | 200 with `Content-Length`, or 404 `object_not_found`. |
| `GET /repos/{id}/objects/{kind}/{oid}` | dev, sess | A single Range gives 206. An unsatisfiable range gives 416 with `Content-Range: bytes */N`. A malformed or multi-range header is ignored and the whole object is served (200). |
| `PUT /repos/{id}/objects/{kind}/{oid}` | dev, streamed | See below. Returns `{oid,size}` with 201 (created or replaced) or 200 (already present or identical). |
| `GET /repos/{id}/objects?kind=&cursor=&limit=` | dev | An empty `kind` lists all kinds. |
| `POST /repos/{id}/objects/missing` | dev | Accepts at most 20000 oids, else 422. Pending-delete objects count as missing. The result is deduplicated and keeps request order. |

Object PUT rules:
- **Size:** `Content-Length` is required (411). Objects over 64 MiB get 413 `too_large`.
- **Capacity:** the PUT uses the upload chunk semaphore. When it is saturated the server answers 503 with `Retry-After` and `X-VD-Capacity: saturated`.
- **Kinds other than config:**
  - A signed hash ≠ oid: the body is verified anyway. A body that does not match its signed hash gives 400 `body_mismatch`; otherwise 422 `object_hash_mismatch`.
  - A new object is checked against the quota (413 `backup_quota_exceeded`), then stored with `objstore.Put`, which verifies the hash before its atomic rename. The DB row is written only after that verification.
  - A replay of an intact object returns 200 without rewriting it.
- **Config:**
  - The caller needs an unexpired exclusive lock row in `repo_locks` held by the calling device, else 409 `repo_locked` with details `{holder, expires_at}`.
  - Identical content returns 200. Different content gets a quota check on the size delta, then `Replace`, then 201.
  - A body/hash mismatch gives 400 `body_mismatch`, and the old config stays in place.

## Commands run and results
All run from `D:/VaultDock/vad-server`:
- `go build ./...`: OK.
- `go vet ./internal/api/ ./internal/store/ ./internal/backup/ ./internal/objstore/...`: OK.
- `gofmt -l` on all owned files: clean.
- `go test ./internal/...`: everything passes except one existing test, `internal/api TestStubRoutesAndAuthGroups` (see Notes).
- `go test ./internal/api/ -skip TestStubRoutesAndAuthGroups`: OK.

The new tests cover:
- idempotent PUT;
- a 3 MiB streamed object;
- HEAD, and GET with full, offset and suffix ranges, including a session GET;
- list paging and missing;
- 422 compared with 400;
- invalid kind;
- cross-user 404;
- quota 413;
- config lock: 409 with no lock, 409 with only a shared server lock, then create 201, identical 200, replace 201, and a failed replace that keeps the old config;
- on-disk layout under `<default>/backup/repo/<uuid>/data/<aa>/<oid>`;
- the Replacer conformance suite.

## Contract issues
None blocking. These are implementer choices for INT to record:
1. The status code for a second repo on the same (user, destination) is unspecified. I chose 409 `repo_exists` with `details.repo_id`. A repo_uuid owned by another user gives 409 `conflict`.
2. The response body of an object PUT is unspecified. I return `wire.ObjectInfo{oid,size}`.
3. The format of `LockedDetails.holder` is unspecified. It holds the `repo_locks.holder` text. When that is empty or `{}`, it is `"device <id>"` or `"server"`, and it is `""` when no lock exists. P1-15 should keep this consistent.
4. Two more statuses are my own choices:
   - 411 `length_required` when a PUT has no Content-Length.
   - 409 `object_corrupt` when the stored file for an oid exists with different content. That should be impossible except through disk corruption; scrub is the remedy.

## Gaps / follow-ups
- **Quota check is not atomic.** It is a pre-check, so concurrent PUTs can overshoot the quota slightly.
- **Pending-delete objects still count against quota.** Quota counts every `repo_objects` row, pending-delete ones included, since they still occupy disk until GC.
- **Replay verification checks size only.** A replay of an existing object compares sizes, not hashes; full verification belongs to scrub.
- **Windows replace limitation.** On Windows, `Replace` can fail while a reader holds the config file open. The client gets a 500 and can retry.
- **Other destination kinds return 501.** Non-server destinations (Phase 4) are refused in both `POST /repos` and object access.
- **`backup.v1` is not advertised.** `implementFeature(wire.FeatureBackup)` is not called; that is left to P1-15 as instructed.

## Notes for dependent tasks / INT
- **Required one-line fix in a file I don't own.** `internal/api/devices_delete_test.go:173-176` (`TestStubRoutesAndAuthGroups`) still expects 501 from the now-implemented `PUT /repos/1/objects/data/<0×64>` and fails, because the route now answers 404 `repo_not_found`. The test's real purpose, showing that the streamed route skips the 2 MiB cap, still holds.
  - Fix: exclude that path from the 501 loop and assert `st == 404 && code == "repo_not_found"` for it (any status other than 413 proves the point).
- **P1-15:**
  - The lock routes should insert `repo_locks` rows with `holder_kind='device'` and `holder_device_id` set. The config PUT reads those rows through `store.HoldsExclusiveRepoLock`.
  - Snapshot commit can check that objects exist with `store.RepoObject` or `MissingRepoObjects`.
  - It can open the repo's store with `s.repoObjects(ctx, rp)` and `backup.OpenLocal`.
- **Phase 4 (SRV-1 destinations):** `GET /destinations` should list the built-in row: `kind='server'`, `scope='global'`, `config {"root": "..."}`. Its root is fixed when the row is created.
