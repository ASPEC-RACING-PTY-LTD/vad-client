# P1-06 SRV-ST: objstore interface, localfs backend, conformance suite
Status: completed

## Goal
Create `vad-server/internal/objstore`: a backend-neutral `Store` interface with strict key validation and typed errors, a `localfs` backend (atomic temp+fsync+rename, directory fsync, refusal of symlink/junction escapes), and a reusable conformance suite (`objstoretest.Run`) that `localfs` passes. The interface must take S3/R2/B2 backends in Phase 4 without changes.

## Changes (repo: path, created/modified/deleted, why)
All in vad-server, all new:
- `internal/objstore/objstore.go`: the `Store` interface, `ObjectInfo`/`ListPage`, typed errors, `ValidateKey`/`ValidatePrefix`/`ValidSHA256` and the limits.
- `internal/objstore/localfs/localfs.go`: the localfs backend.
- `internal/objstore/localfs/localfs_test.go`: runs the conformance suite, plus localfs-specific tests (on-disk layout, no temp leftovers, List skips temp and foreign files, symlink/junction escape refusal, root given as a link, `New` validation).
- `internal/objstore/objstoretest/objstoretest.go`: the conformance suite and helpers `Sum`, `MustPut`, `ReadAll`.

No new dependencies. `internal/storage` is imported (read-only) for `storage.ResolveLocationPath`, which resolves the root.

## Interfaces exposed
```go
package objstore

var ErrNotFound, ErrExists, ErrHashMismatch, ErrSizeMismatch,
    ErrInvalidKey, ErrInvalidRange, ErrUnsafePath error

const MaxKeyLen = 512; MaxSegmentLen = 128; DefaultListLimit = 1000; MaxListLimit = 1000

type ObjectInfo struct { Key string; Size int64; ModTime time.Time }
type ListPage   struct { Objects []ObjectInfo; NextCursor string } // "" = last page

type Store interface {
    Put(ctx context.Context, key string, r io.Reader, size int64, sha256hex string) error
    Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
    GetRange(ctx context.Context, key string, off, length int64) (io.ReadCloser, error)
    Stat(ctx context.Context, key string) (ObjectInfo, error)
    Delete(ctx context.Context, key string) error
    List(ctx context.Context, prefix, cursor string, limit int) (ListPage, error)
}

func ValidateKey(key string) error       // wraps ErrInvalidKey
func ValidatePrefix(prefix string) error // wraps ErrInvalidKey
func ValidSHA256(s string) bool

package localfs
func New(root string) (*Store, error) // root: existing absolute dir; links in root are resolved
func (s *Store) Root() string         // resolved root
// *localfs.Store implements objstore.Store

package objstoretest
func Run(t *testing.T, newStore func(t *testing.T) objstore.Store)
func Sum(b []byte) string
func MustPut(t testing.TB, s objstore.Store, key string, b []byte)
func ReadAll(t testing.TB, s objstore.Store, key string) []byte
```

Semantics, as the suite enforces them:
- **Put:**
  - reads exactly `size` bytes and hashes them;
  - a short or long body returns `ErrSizeMismatch`;
  - a wrong hash, or an expected hash that is not 64 lower-case hex characters, returns `ErrHashMismatch`;
  - nothing becomes visible on failure;
  - repeating a Put with the same content returns nil; different content returns `ErrExists`.
- **Get/Stat** return `ErrNotFound` for a missing key.
- **GetRange:**
  - `length < 0` reads to the end;
  - a range past the end is clamped;
  - `off` outside `[0, size]` returns `ErrInvalidRange`.
- **Delete** is idempotent: deleting a missing key returns nil.
- **List:**
  - returns keys in byte-wise lexicographic order;
  - `cursor` is exclusive (start after this key);
  - `limit <= 0` means 1000, and larger values are capped at 1000.
- **Context:** every method honours `ctx`, and Put also checks it during the copy.
- **Keys:**
  - `/`-separated, non-empty segments of `[a-z0-9._-]`, lower-case only so case-insensitive filesystems can't collide;
  - a segment cannot start with `.` (this also rules out `.`, `..` and the backend's temp names) or end with `.`;
  - Windows device names (`con`, `nul`, `com1`, …) are refused;
  - limits: 512 bytes per key, 128 per segment;
  - absolute paths, backslashes, `:` and empty segments are rejected.

localfs details:
- **Key mapping:** each key maps one-to-one to `root/<key>`, so the format's `data/<aa>/<oid>` gives the fan-out. Parents are created on demand (0700) and the parent directory is fsynced.
- **Put:** writes a `.tmp-<random>` file (0600) in the target directory, then fsync, a no-overwrite check under an in-process striped lock, rename, and a directory fsync. The fsync is ignored where unsupported (Windows).
- **Link refusal:** every component below the root is checked with Lstat, and any symlink, junction or reparse point gives `ErrUnsafePath`.
- **List:**
  - walks without following links;
  - skips dot-files and keys that fail validation;
  - prunes directories that can't match;
  - sorts the result.

## Commands run and results
- `cd D:/VaultDock/vad-server && go build ./...` passed.
- `go vet ./internal/objstore/...` passed.
- `gofmt -l internal/objstore` reported no files.
- `go test -count=3 ./internal/objstore/...` passed.
  - All 18 conformance subtests and the 7 localfs tests pass.
  - `TestRefusesLinkEscape` ran with a junction (`mklink /J`, no admin needed) and passed.
  - `TestRefusesFileSymlink` **skipped** on this host, because file symlinks need Developer Mode or admin. It runs on Linux CI.

## Contract issues
None. backup-format-v1 §2 keys (lower-case UUID and hex oids) all pass `ValidateKey`.

## Gaps / follow-ups
- **Key vs prefix:** localfs can't hold key `a` and key `a/b` at the same time, because a file can't be a directory. Put returns `ErrExists`, and reads below a file return `ErrNotFound`. The format never needs this. The conformance suite avoids the case, so S3 (which allows it) still passes.
- **Single process:** the no-overwrite check in Put is serialised in-process only, so a root must be owned by one server process. That is the default deployment.
- **Temp sweep:** stale `.tmp-*` files from a crash are not swept. A GC or repair task should delete `.tmp-*` files older than, for example, 1 hour; List already ignores them.
- **List cost:** List walks the whole subtree under the prefix's directory for every page (O(n) per page). That is fine per repository. A large-scale GC could later add a streaming iterator.
- **Not included:** no `Capabilities()`/`Health()`/`Space()`. Phase 4 should add optional capabilities (`Locker`, `Copier`, `Signer`, space and health) as separate interfaces found by type assertion, so `Store` itself stays unchanged.
- **Empty directories:** they are left behind after Delete; this is harmless.

## Notes for dependent tasks / INT
- **Usage:** `s, err := localfs.New(<location>/backup)`, then keys such as `"repo/<uuid>/data/<oid[:2]>/<oid>"`.
- **Root resolution:** `New` resolves the root with `storage.ResolveLocationPath(root, nil)`. Callers that enforce allowed storage roots should validate the location first, as they do today.
- **Phase 4 backends** should run `objstoretest.Run` against MinIO or a fake. An S3 Put that needs the idempotent/`ErrExists` behaviour can use `If-None-Match: *` plus a HEAD/compare on 412.
- **Error mapping:** API handlers can use `errors.Is`:
  - `ErrNotFound` → 404;
  - `ErrExists` → 409;
  - `ErrHashMismatch`/`ErrSizeMismatch`/`ErrInvalidKey`/`ErrInvalidRange` → 400/422/416.
