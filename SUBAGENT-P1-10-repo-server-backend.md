# P1-10 SDK-REPO: repo/backend/server adapter
Status: completed

## Goal
Add `sdk/repo/backend/server`, a `repo.Backend` over `*client.Client` for one server repository (api-v1 §5, Rev 1.1b). It provides:
- streamed idempotent PUT, ranged GET, HEAD and a paged List;
- server-authoritative locks;
- error mapping and a bounded 503/429 retry;
- a pass-through `CommitSnapshot`;
- httptest-based tests, including a full repo round trip.

## Changes (repo: path, created/modified/deleted, why)
- VaultDock: `sdk/repo/backend/server/server.go` (created). The adapter.
- VaultDock: `sdk/repo/backend/server/fake_test.go` (created). An in-memory api-v1 §5 fake server:
  - objects, Range and HEAD through `http.ServeContent`;
  - list pages of 2;
  - shared and exclusive locks with `repo_locked` details, and `404 lock_not_found`;
  - config PUT requires an exclusive lock held by the calling device (`X-VD-Device`);
  - checks `X-VD-Content-SHA256` against the body, and returns 422 `object_hash_mismatch`;
  - the commit route, with a 201/200 replay and `pack_missing`;
  - injection of 503 (with `Retry-After`), 422 and an ignored-Range fault.
- VaultDock: `sdk/repo/backend/server/server_test.go` (created). Tests for objects and ranges (table-driven), paged List, retry and its bound, locks across two devices, commit, and the full round trip. The round trip runs init → data and meta packs → `SaveSnapshot` → second init gives `ErrExists` → no locks are left → reopen with the device credential on a new client and Backend → read back and verify → `CommitSnapshot`.
- No changes to `sdk/client` or `sdk/repo`.

## Interfaces exposed
```go
package server // github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/repo/backend/server

type Options struct {
    MaxRetries int           // retries on 503/429; default 4, <0 disables
    BaseDelay  time.Duration // default 500ms, doubles per attempt; Retry-After wins if longer
    MaxDelay   time.Duration // default 30s cap per delay
    Sleep      func(ctx context.Context, d time.Duration) error
    Now        func() time.Time
    Logger     *slog.Logger
}
func New(c *client.Client, repoID int64, opts Options) *Backend
func (b *Backend) RepoID() int64
// repo.Backend:
func (b *Backend) Put(ctx, kind repo.Kind, oid string, data []byte) error
func (b *Backend) Get(ctx, kind, oid) ([]byte, error)
func (b *Backend) GetRange(ctx, kind, oid, offset, length int64) ([]byte, error)
func (b *Backend) Stat(ctx, kind, oid) (int64, error)
func (b *Backend) List(ctx, kind) ([]repo.ObjectInfo, error)
func (b *Backend) Delete(ctx, kind, oid) error // always wraps errors.ErrUnsupported (no DELETE route, Rev 1.1b)
func (b *Backend) Lock(ctx, kind repo.LockKind, ttl time.Duration) (repo.Lock, error)
func (b *Backend) RefreshLock(ctx, id string, ttl time.Duration) (repo.Lock, error)
func (b *Backend) Unlock(ctx, id string) error
// catalog commit (api-v1 §5.2), idempotent by snapshot_oid, retried on 503/429:
func (b *Backend) CommitSnapshot(ctx, req wire.SnapshotCommitRequest) (*wire.SnapshotCommitResponse, bool /*created*/, error)

type LockedError struct{ Holder string; Expires time.Time; Err error }  // Is(repo.ErrLocked); Unwrap → *client.APIError
type HashMismatchError struct{ Kind repo.Kind; OID string; Err error }  // Is(repo.ErrInvalidObject)
```

Error mapping:

| Server reply or condition | Result |
|---|---|
| 409 `repo_locked` | `*LockedError`; `Expires` comes from `time.UnixMilli(expires_at)` |
| 422 `object_hash_mismatch`, or `client.ErrBodyMismatch` | `*HashMismatchError` |
| 404 `object_not_found`, or a 404 with an empty code | wraps `repo.ErrNotFound` |
| HEAD reports the object does not exist | `repo.ErrNotFound` |
| 400 `invalid_oid` | wraps `repo.ErrInvalidObject` |
| 416, a short ranged read, or a negative offset or length | wraps `repo.ErrRange` |
| Server ignored the Range header (over-read) | plain error |
| 404 `lock_not_found` on refresh | `repo.ErrNoLock` |
| 404 `lock_not_found` on unlock | nil |
| Anything else | stays a `*client.APIError`, for example `pack_missing` or `profile_not_authorised` |

Lock behaviour:
- The TTL is rounded up to whole seconds, with a minimum of 1.
- `Expires` comes from the reply's `expires_at` (ms). When the reply has none, it falls back to now + ttl.
- The lock kind is remembered by id, so the refresh result can report it.

Other operations:
- `Put` checks the object locally with `repo.CheckObject` before sending. For config it sends an explicit body SHA-256; for other kinds the SHA defaults to the oid.
- `List` follows `next_cursor` with `limit=500`. It rejects invalid oids and a repeated cursor.

## Commands run and results
All run from `D:/VaultDock/VaultDock/sdk`:
- `gofmt -l repo/backend/server`: clean.
- `go build ./...`: ok.
- `go vet ./repo/backend/server/...`: ok.
- `go test ./repo/backend/server/...` and `go test -count=1 -v ...`: ok. All 6 tests passed (TestObjects, TestListPaged, TestRetry, TestLocks, TestCommitSnapshot, TestRepoRoundTrip).
- `-race` was not run, because cgo is disabled on the host.

## Contract issues
None open. The Rev 1.1b resolutions (no object DELETE, `404 lock_not_found`, config PUT under the caller's exclusive lock, 422 `object_hash_mismatch`) are implemented as specified.

The contract does not say whether the lock refresh reply carries a body. The adapter accepts both an empty reply and a `{lock_id, expires_at}` reply, as `client.RefreshLock` already does.

## Gaps / follow-ups
- Only 503 and 429 are retried. Network errors are not retried, to avoid duplicating a non-idempotent lock POST; callers can retry whole operations.
- `Get` does not re-verify content hashes, because `repo.getObject` already does.
- `Resolve(repoUUID)` (paging `c.Repos`) is not implemented. The engine passes the server repo id.

## Notes for dependent tasks / INT
- The engine should call `repo.SaveSnapshot` (which stores the root through `Put`), then `(*server.Backend).CommitSnapshot` with the job, run, profile and consent fields. `pack_missing` comes back as a `*client.APIError` (`client.IsCode(err, wire.CodePackMissing)`).
- The `*client.Client` must have `DeviceID` and `Key` set, because unsigned requests do not carry `X-VD-Content-SHA256`.
- The server's config PUT must match the lock holder by device id. `repo.Init`, `Rotate` and `SetEscrowMode` take the exclusive lock through this Backend from the same device.
- No new dependencies, and no `go.mod` change.
