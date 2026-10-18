# P1-15 (SRV-2) Locks, snapshot commit (consent check), catalog list/get; `backup.v1` advertised
Status: completed

## Goal
Implement api-v1 §5 locks (with the rev 1.1b `404 lock_not_found`), the streamed `POST /repos/{id}/snapshots` commit (§5.2: object existence, profile consent, idempotent by `snapshot_oid`, linked to job/run), and `GET /snapshots` / `GET /snapshots/{id}` (§5.4). Advertise `backup.v1`.

## Changes (repo: path, created/modified/deleted, why)
All in vad-server.
- `internal/backup/locks.go` (created): lock manager over `repo_locks` (shared/exclusive, TTL, refresh, release, conflict details, expiry sweep).
- `internal/store/snapshots.go` (created): snapshot rows, transactional commit (existence check + `snapshot_packs` + `job_runs.snapshot_id`), get/by-oid, filtered cursor list.
- `internal/api/snapshots.go` (replaced the P0-03 stub): lock routes, commit, list/get; `init() { implementFeature(wire.FeatureBackup) }`. Diff, PATCH, tree, search and `/catalog/rebuild` stay `notImplemented` (P1-16 and later).
- `internal/api/snapshots_test.go` (created): `TestRepoLocks`, `TestSnapshotCommitConsentAndCatalog`.
- No migration was needed. The schema from migrations 3, 4 and 5 is used as-is.
- `devices_delete_test.go` was not touched. `TestStubRoutesAndAuthGroups` passes, because the big-body snapshot commit to repo 1 now gets 404 `repo_not_found`.

## Interfaces exposed
Routes:

| Route | Auth | Behaviour |
|---|---|---|
| `POST /repos/{id}/locks` | dev | `wire.LockRequest{kind, ttl_seconds}` returns `201 wire.Lock{lock_id, expires_at}`. `ttl_seconds` 0 means 300; the range is 1..3600, else 422 `validation`. An incompatible lock gives `409 repo_locked {holder, expires_at(ms)}`. Another user's repo gives 404 `repo_not_found`. |
| `PUT /repos/{id}/locks/{lock_id}` | dev | Optional body `{ttl_seconds}` (an optional `kind` is ignored). 0 or no body keeps the lock's TTL. Returns `200 wire.Lock`. |
| `DELETE /repos/{id}/locks/{lock_id}` | dev | `204`. |
| `POST /repos/{id}/snapshots` | dev, streamed (≤ 64 MiB) | Body `wire.SnapshotCommitRequest`. Returns `201 {snapshot_id}`, or `200` on replay. |
| `GET /snapshots?repo=&job=&device=&profile=&kind=&tag=&from=&to=&protected=&cursor=&limit=` | sess | Returns `List[wire.Snapshot]` of the caller's snapshots, newest first; the cursor is the last id. A bad numeric or bool filter gives 422 `validation`. |
| `GET /snapshots/{id}` | sess | Returns `wire.Snapshot`. Another user's snapshot gives 404 `snapshot_not_found`. |

Lock rules:
- Shared locks coexist.
- An exclusive lock conflicts with every unexpired lock, including the caller's own.
- Refresh or release of a lock that is unknown, expired, belongs to another device, or is on another repo gives `404 lock_not_found`.
- Expired rows are swept inside every Acquire.
- `holder` is the stored holder text, else `"device <id>"` or `"server"`, the same format as P1-14's `HoldsExclusiveRepoLock`. Locks acquired through the API store `holder='{}'`, `holder_kind='device'` and `holder_device_id`, so the P1-14 config PUT honours them. This is tested end to end.

Commit order:
1. Hash the whole body and verify it before any DB access. A mismatch is 400 `body_mismatch`; bad JSON is 400 `bad_json`.
2. Validate the request:
   - A bad snapshot, index or pack oid is 400 `invalid_oid`.
   - These fields give 422 `validation`:
     - unknown kind or mode;
     - `epoch` < 1;
     - `run_id` without `job_id`;
     - empty `user_sid` or `profile_consent_id`;
     - more than 50 tags, or a tag longer than 100 bytes;
     - a text field longer than 1 KiB;
     - negative stats.
3. Replay: if the same `snapshot_oid` is already in this repo, return 200 with the existing id. No consent check runs on replay.
4. `s.requireProfileConsent(device, user_sid, profile_consent_id)` gives 403 `profile_not_authorised` (unknown profile, no consent, or a stale consent id) or 423 `profile_blacklisted`.
5. Check the references. A failure is 422 `validation`.
   - The job must be the caller device's job, for the repo's user, with `repo_id` empty or equal to this repo.
   - The run must belong to that job and device.
   - The parent must be in the same repo.
   - An empty kind or mode is taken from the job; mode then defaults to `full`; a kind is required.
6. In one transaction:
   - Re-check idempotency.
   - Check existence: the root (kind `snapshots`), `index_oids` (kind `index`) and `pack_oids` (kind `data`). Pending-delete objects count as missing. Any missing object gives `409 pack_missing {missing[≤1000]}`.
   - Insert the `snapshots` row (`ingest_state` `pending`, `profile_username` from the device profile).
   - Insert `snapshot_packs`.
   - Set `job_runs.snapshot_id`.
7. Write an activity entry `snapshot.commit` (not audit).

Go:
```go
// package backup
const DefaultLockTTL, MinLockTTL, MaxLockTTL = 300, 1, 3600
var ErrLockNotFound, ErrInvalidLock error
type LockedError struct{ Holder string; ExpiresAt int64 }
type Lock struct{ ID string; RepoID int64; Kind string; DeviceID, ExpiresAt int64 }
type Locks struct{ DB *sql.DB; Now func() int64 }
func NewLocks(st *store.Store) *Locks
func ValidLockKind(string) bool
func NormalizeTTL(int) (int, error)
func ConflictHolder(holder string, deviceID sql.NullInt64) string
func (l *Locks) Acquire(ctx, repoID, deviceID int64, kind, holder string, ttl int) (*Lock, error) // deviceID 0 = server lock
func (l *Locks) Refresh(ctx, repoID, deviceID int64, lockID string, ttl int) (*Lock, error)
func (l *Locks) Release(ctx, repoID, deviceID int64, lockID string) error
func (l *Locks) Sweep(ctx) (int64, error)
func (l *Locks) RunSweeper(ctx, interval time.Duration)

// package store
type ErrObjectsMissing struct{ Missing []string }
type Snapshot struct{ ID, RepoID, UserID, DeviceID int64; SnapshotOID string; JobID, RunID, ParentSnapshotID int64; Kind, Mode, Hostname, OSUser, ProfileSID, ProfileUsername, ProfilePath, ProfileConsentID string; Epoch, ClientTime, CommittedAt int64; Tags []string; Stats wire.SnapshotStats; IndexOIDs []string; Protected bool; ProtectedUntil int64; Status, IngestState string }
func (s *Store) GetSnapshot(ctx, id) (*Snapshot, error)
func (s *Store) SnapshotByOID(ctx, repoID int64, oid string) (*Snapshot, error)
func (s *Store) CommitSnapshot(ctx, sn *Snapshot, packOIDs []string) (created bool, err error)
func (s *Store) SnapshotPackCount(ctx, snapshotID int64) (int64, error)
type SnapshotFilter struct{ UserID, RepoID, JobID, DeviceID int64; ProfileSID, Kind, Tag string; From, To int64; Protected *bool; BeforeID int64; Limit int }
func (s *Store) ListSnapshots(ctx, SnapshotFilter) ([]Snapshot, error)
```

## Commands run and results
All run from `D:/VaultDock/vad-server`:
- `go build ./...`: OK.
- `go vet ./internal/api/ ./internal/store/ ./internal/backup/`: OK.
- `gofmt -l` on the four owned files: clean.
- `go test ./internal/api/ -run 'TestRepoLocks|TestSnapshot|TestBackup|TestStubRoutesAndAuthGroups' -v`: all pass.
- `go test ./internal/...`: every package passes except `internal/api TestApprovalResumeThroughOperation`. It fails at approvals_test.go:198 with `409 plan_stale` on the `escrow_change` superuser approval.
  - The `escrow_change` resume handler is registered in `internal/api/repo_keys.go`, which P1-13 was editing during my run (modified 22:13). I didn't touch it, and the failure is unrelated to this task.
  - `go test ./internal/api/ -skip TestApprovalResumeThroughOperation`: OK.

The tests cover:
- **Lock validation:** bad kind, bad TTL, another user's repo (404), and a session on the route (403).
- **Lock compatibility:** shared + shared across two devices; exclusive over shared gives 409 with holder and expiry.
- **Refresh and release:** a foreign, unknown, expired or double-released lock gives 404 `lock_not_found`. Refresh with a new TTL and with the kept TTL both work.
- **Exclusive lock:** it blocks both devices. The config PUT succeeds under the lock, gives 409 for the other device and fails after release; the other device can then take the lock.
- **Expiry:** `Sweep` removes an expired lock, which then no longer blocks.
- **Commit, refused:** an unknown profile (403), a stale consent id (403) and a blacklisted profile (423). Also a bad oid (400), a bad epoch (422), an unknown run (422) and an unknown parent (422).
- **Commit, missing objects:** `pack_missing` with the exact `details.missing`.
- **Commit, accepted:** 201, then a replay gives 200 with the same id. `snapshot_packs` is filled, `run.snapshot_id` is linked, and repo usage shows snapshots=1 with logical_bytes.
- **Catalog:** a child snapshot with `parent_id`; list order, view fields, the tag, repo, job, profile and protected filters, paging, a bad filter (422), and another user's get (404) and list (empty).
- **After blacklisting:** a replay still gives 200; a new commit gives 423.
- **Feature flag:** `backup.v1` appears in `/info`.

## Contract issues
None blocking. These are implementer choices for INT to record:
1. **Lock TTL bounds.** A default of 300 s and a range of 1..3600 s. Out-of-range values give 422 `validation`.
2. **Refresh shape.** Refresh takes an optional `{ttl_seconds}` and replies `200 {lock_id, expires_at}`. Release replies `204`. Acquire replies `201`.
3. **Exclusive conflicts with the caller's own locks.** A lock upgrade (shared → exclusive by the same device) is refused, as in restic.
4. **No consent check on replay.** A replay of an existing `snapshot_oid` returns 200 even if the profile has since been blacklisted. The snapshot already exists.
5. **`pack_missing` covers every reference.** It is used for a missing root, index or pack. Rev 1.0 §5.2 groups them under one code.
6. **Required fields and defaults.** `user_sid` and `profile_consent_id` are required (422 when empty). Kind and mode default from the job; mode then defaults to `full`.
7. **List order.** `GET /snapshots` is newest first, and the cursor is the last id returned.

## Gaps / follow-ups
- **Periodic lock sweep not wired.** Expired locks are swept on every Acquire. A periodic `backup.NewLocks(st).RunSweeper(ctx, time.Minute)` is available, but nothing starts it yet.
- **Not implemented here:** PATCH (tags/protect), diff, tree, search and `/catalog/rebuild` stay 501. Catalog ingestion is P1-16, which picks up rows with `ingest_state = 'pending'`.
- **Epoch is not checked against the repo.** `epoch` is only checked to be ≥ 1, not against `repositories.current_epoch`, because rewrap/rotation may advance the client first.
- **`index_oids` are not in `snapshot_packs`.** They are stored as JSON in `snapshots.index_oids`, and the root oid is `snapshots.snapshot_oid`. GC reachability (§9) must treat both as live along with `snapshot_packs`.
- **Lock routes ignore `job_id`/`run_id`.** Nor does the commit require the caller to hold a lock; the contract doesn't require it.

## Notes for dependent tasks / INT
- **App wiring (INT):** optionally start `backup.NewLocks(st).RunSweeper(ctx, time.Minute)` in `internal/app`.
- **Unrelated failing test:** `TestApprovalResumeThroughOperation` currently fails because of in-progress P1-13 work (the `escrow_change` handler in `repo_keys.go`).
- **P1-16:** use `store.GetSnapshot`, and update `ingest_state`/`ingest_error` and `repositories.last_catalog_sync_at` yourselves; `snapshotView` sets `browsable` from `ingest_state == done`.
- **P1-21 (readiness):** `snapshots.committed_at`, `profile_sid`, `job_id` and `device_id` are populated, and `job_runs.snapshot_id` is linked at commit.
- **Web (P1-32):** `GET /snapshots` is sess-only and newest first. `profile` is `{sid, username}`, and `parent_id`/`protected_until` are null when unset.
