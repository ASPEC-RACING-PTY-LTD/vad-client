# P0-07 (SRV-8) Storage hardening (B9) + unit tests
Status: completed

## Goal
Harden the storage layer (research 01 R5, R6, R7 plus the listed items): atomic writes, path containment with symlink/junction checks, permission bits, free-space checks, admin storage-location validation and consistent quota accounting. Add unit tests for `internal/storage` (none existed), `internal/store/users.go` and the hardened admin paths.

INT asked for two additions during the task, both done:
- B14: wire `SettingsCache.Merge` into the settings PUT.
- Adapt to P0-06's `cancelUpload` returning `bool`.

## Changes (repo: path, created/modified/deleted, why)

### vad-server: `internal/storage/storage.go` (modified)
- **R7 data race.** Added `SetGlobalMinFree(int64)`, which clamps to ≥ 0 and writes under `mu`. `CheckSpace` now reads the floor under `mu.RLock`.
  - The exported field `GlobalMinFree` stays, because `main.go`, `testserver.go` and the frozen `api_test.go` assign it before the manager is shared.
- **R6 leaked `.copying` files.** `CleanOrphans` now also removes stale `<sha>.copying` partial copies older than `minAge`, under the blob lock. It removes them even when the blob row exists, since a partial copy is never valid content.
- **Atomic writes.**
  - New `writeAtomic` and `writeFileAtomic`: create the temp file with `O_EXCL`, fill it, fsync, close, rename, then fsync the directory. The temp file is removed on any failure.
  - `copyFile` uses `writeAtomic` and clears a stale `.copying` file first.
  - The location marker is now written atomically.
- **Containment and link checks.**
  - `Within(root, p)`: a lexical containment check that fixes the `strings.HasPrefix(rel, "..")` bug, which wrongly treated a directory named `..foo` as outside.
  - `ResolveLocationPath(p, roots)`: resolves symlinks and **Windows junctions**, then requires the result to lie inside a resolved root. `filepath.EvalSymlinks` does not follow junctions on Go ≥ 1.23. The new `resolvePath`/`expandLink` handle them with `os.Readlink` and refuse unreadable reparse points.
  - `Overlaps(a, b)`: checks whether two resolved paths are equal or one contains the other.
  - `PrepareLocation` now:
    - refuses `blobs/` or `tmp/` if either is a symlink or junction;
    - refuses a marker that is not a regular file;
    - checks that free space can be read.
  - `Place` (new `checkTemp`) refuses a temp path that is outside `<location>/tmp` or is not a regular file.
  - `Open` and `LinkExisting` refuse a blob path that is not a regular file, such as a planted symlink.
- **Permissions.** On non-Windows systems, `PrepareLocation` restricts `blobs/` and `tmp/` to `0750` if they are group-writable or world-accessible.
- **Free space.** `CheckSpace` rejects a negative `need` and compares `need > avail - pending - reserve`, so a huge `need` cannot overflow.
- New errors: `ErrOutsideRoots`, `ErrUnsafePath`.

### vad-server: `internal/api/admin.go` (modified)
- **Create storage location.**
  - After the existing lexical `AllowedStoragePath` check, it now calls `storage.ResolveLocationPath`.
  - A link that escapes the roots returns 400 `path_not_allowed`. A missing path returns 400 `bad_path`.
  - The nesting check now uses `storage.Overlaps` on resolved paths. That also rejects the same path twice and paths that differ only by a link.
  - A `ListLocations` error is no longer ignored.
- **Patch storage location.** Enabling a location, or making it the default, requires its marker to be present; otherwise it returns 400 `location_unavailable`.
- **User location.** The new `checkUserLocation` requires the assigned location to exist and be enabled (400 `bad_location`), on both user create and user patch. The `UpdateUser` error on create is no longer swallowed.
- **Settings PUT (B14, INT request).** `handleAdminSaveSettings` decodes a `json.RawMessage` through `decode`, which keeps the 2 MiB limit and the 413 response. It then calls `s.settings.Merge` and `s.blobs.SetGlobalMinFree(v.MinFreeBytes)`.
  - Error codes are `bad_json` and `invalid_settings`, as P0-04 specified.
  - The response is still `{"settings": …}`.
- **R5 maintenance (with P0-06's `cancelUpload` → `bool`).**
  - Expired `finalizing` uploads are skipped. A finalisation interrupted by a crash is reopened by `RecoverAfterRestart` and expires normally.
  - The old open/finalizing branch is now a `switch`, and the unreachable finalizing cancel path is gone.
  - An `open` upload is counted only if `cancelUpload` returns true.
- **Admin cancel.** `DELETE /admin/uploads/{id}` on a finalising upload returns 409 `upload_busy`, matching the user cancel handler.

### vad-server: `internal/store/users.go` (modified)
- New `ErrInvalidQuota`. `CreateUser` and `UpdateUser` reject a negative quota at the store level.
- `CreateUser` stores `location_id` as NULL when `LocationID` is 0, instead of a dangling id 0.
- `RecalculateUsage` now uses the same sum as `CommitFile` and `purgeTree`: file nodes with `sha256 IS NOT NULL`, trash included. It returns `ErrNotFound` for an unknown user.
- New read-only `UsageDrift(ctx, userID) (stored, actual int64, err error)`.

### New test files (vad-server)
- `internal/storage/storage_test.go` covers:
  - `Within`;
  - `ResolveLocationPath` and `Overlaps`, including a link escaping the root;
  - `PrepareLocation`, including a junctioned `blobs/`;
  - atomic writes and failure cleanup;
  - `CheckSpace` floor, negative and overflow sizes, unknown and unmounted locations;
  - concurrent `SetGlobalMinFree`/`CheckSpace`;
  - `Place` (stray temp refused, failed commit cleanup, success) and `Open` refusing a linked blob;
  - `CleanOrphans` (orphan blob, stale and young `.copying` files, stale temp, unrelated file kept).
- `internal/store/users_test.go` covers:
  - create validation (negative quota, NULL location, duplicate);
  - quota update as a table test;
  - quota accounting through `CommitFile` create, duplicate content, quota rejection, replace, keep-replaced trash and `PurgeTrash`;
  - drift repair with `RecalculateUsage`, and unknown users.
- `internal/api/admin_storage_test.go` (package `api_test`, reuses the frozen `api_test.go` helpers) covers:
  - location validation as a table test (outside roots, missing, nested in either direction, duplicate, OK) plus a junction escaping the root;
  - assigning users to a disabled location;
  - enabling or defaulting an unmounted location;
  - negative quota;
  - **settings partial PUT keeps other fields**, plus `bad_json`/`invalid_settings` cases and `min_free_bytes` reaching the storage manager (507);
  - R5: maintenance keeps an expired finalising upload, admin cancel returns 409 `upload_busy`, and a reopened upload expires.
- Link creation in tests uses `os.Symlink` and falls back to a `mklink /J` junction on Windows, where this host has no symlink privilege. The tests skip only if both fail. On this host every test ran; none were skipped.

## Interfaces exposed
```go
// internal/storage
var ErrOutsideRoots, ErrUnsafePath error
func (m *Manager) SetGlobalMinFree(v int64)
func Within(root, p string) bool
func ResolveLocationPath(p string, roots []string) (string, error)
func Overlaps(a, b string) bool
// internal/store
var ErrInvalidQuota error
func (s *Store) UsageDrift(ctx context.Context, userID int64) (stored, actual int64, err error)
```
New API error codes:
- 400 `location_unavailable` (PATCH `/admin/storage/{id}`);
- 400 `bad_location` for a disabled location;
- 409 `upload_busy` (DELETE `/admin/uploads/{id}`);
- the settings PUT now merges (B14).

## Commands run and results
- `cd D:/VaultDock/vad-server && go build ./... && go vet ./internal/storage/ ./internal/api/ ./internal/store/ && go test ./internal/storage/ ./internal/api/ ./internal/store/`: all ok (storage 0.6s, api 5.1s, store ok).
- `go test -count=1 -v -run 'TestAdminStorage|TestAdminSettings|TestMaintenance' ./internal/api/`: all PASS, nothing skipped.
- `gofmt -l` on the owned and new files: clean.
- `go test -race ./internal/storage/` could not run here (needs cgo). CI should run it to confirm R7.

## Contract issues
None found. The new 400/409 codes are admin-only error details; `upload_busy` matches P0-06.

## Gaps / follow-ups
- The stored location path is the cleaned path the admin entered, not the resolved one. A link swapped in after creation (TOCTOU) is not re-checked at runtime; checking it would need a periodic `ResolveLocationPath` health check.
- A Windows volume mount point (a drive mounted into a folder) inside a root resolves to `\\?\Volume{…}` and is rejected as outside the roots. Document this, or allow it if wanted.
- `internal/storage/disk_unix.go` and `disk_windows.go` show up in `gofmt -l` (CRLF/format). They were left untouched because they are unowned.
- R11 (one big `PurgeTrash` transaction) and the auth/store unit tests other than `users.go` were out of this task's owned files.

## Notes for dependent tasks / INT
- Code that changes the free-space floor at runtime must call `blobs.SetGlobalMinFree`. Direct assignment to `GlobalMinFree` is only safe before serving starts (as in `main.go`, `testserver.go` and `api_test.go`).
- `handleAdminDeleteUser` still calls `cancelUpload` and ignores the result. A finalising upload of the deleted user then has its row removed by `DeleteUserRecords`, but its temp file stays until `CleanOrphans` runs. That is harmless, but INT may want to wait for or refuse deletion here. `devices.go:213` (device revoke) also ignores the bool; it is unowned.
- Line endings: the edited files were written with LF. With `core.autocrlf=true`, `git diff` shows only the real changes.
