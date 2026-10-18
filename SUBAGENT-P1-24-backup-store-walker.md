# P1-24 (DSK-C) backup.db, job model and walker (profile token, guard)
Status: completed

## Goal
New package `desktop/internal/backup`:
- the `backup.db` store (jobs, runs, file cache, packs, pending issues), built on `dbutil`;
- the job model: the server job list merged by revision, plus a modules `ItemSource`;
- a walker that runs as the profile's user. It checks the guard, never follows reparse points, and skips files that are access denied, locked, EFS-encrypted or cloud placeholders;
- tests.

This report covers the first session (store and job) and the resume session (walker, OS layer and tests).

## Changes (repo: path, created/modified/deleted, why)
All files are in the VaultDock repo, under `desktop/internal/backup/`.
- `store.go` (created in the first session; unchanged here): migrations v1–v5, `Store`, and the run, file cache, pack and issue accessors. It also defines `Issue`.
- `job.go` (created in the first session; unchanged here): `Job`, `SyncJobs`, `FetchJobs`, `SyncFromServer` and `JobSource`.
- `walker.go` (created): restored from `planning/drafts/walker.go.draft`.
  - Removed the draft's duplicate `Issue` type.
  - Dropped the unused parameters of `batch`.
  - Added `PendingIssue.Report()`, which applies the api-v1 §14 Rev 1.1b issue-class mapping.
- `walker_windows.go` (created): the `OSFS` file system for Windows.
  - Listing uses `FindFirstFile`/`FindNextFile` on `\\?\` paths, taking the attributes and the reparse tag (`Reserved0`) from the directory entry. Nothing is opened or recalled.
  - `Stat` reads the entry with `FindFirstFile` on the path itself. Volume roots fall back to `GetFileAttributesEx`.
  - `Probe` opens with `CreateFile(GENERIC_READ, share RWD, OPEN_REPARSE_POINT|BACKUP_SEMANTICS|OPEN_NO_RECALL)` and builds the file id from `GetFileInformationByHandle` as `"%08x-%08x%08x"` (volume serial, then file index).
  - Errors are mapped as follows; the result is still an `*fs.PathError`, so `fs.ErrPermission` and `fs.ErrNotExist` keep matching:

    | Win32 code | Mapped to |
    |---|---|
    | 32, 33 | `ErrLocked` |
    | 6000 | `ErrEFS` |
    | 362 | `ErrCloudPlaceholder` |
    | 206 | `ErrPathTooLong` |

- `walker_other.go` (created, `!windows`): `OSFS` built on `os.Lstat`/`os.ReadDir`.
  - Symlinks are reported as reparse points with the symlink tag; special files as reparse points with tag 0. Neither is followed.
  - `Probe` refuses anything that is not a regular file, and checks `SameFile` between the `Lstat` and the opened file.
  - The file id is `dev:ino` in hex. `ENAMETOOLONG` maps to `ErrPathTooLong`.
- `store_test.go`, `job_test.go`, `walker_test.go` (created): tests, described under Commands below.

## Interfaces exposed
The store and job interfaces are as reported in the first session and unchanged:
- `Store`, `Open`/`OpenDir`, the run, file cache, pack and issue accessors;
- `Issue`, `PendingIssue`;
- `Job`, `SyncJobs`, `SyncResult`, `JobLister`, `FetchJobs`, `SyncFromServer`;
- `JobSource`/`NewJobSource`, `ItemKindBackupJob`.

New in this session:
```go
const IssueClassEFSNeedsLogon = "efs_needs_logon"; const IssueClassProfileNeedsLogon = "profile_needs_logon"
func WireClass(class string) string                // efs→access_denied, profile→other, others unchanged
func (p PendingIssue) Report() wire.IssueReport    // local class → wire class, detail = "<local class>: <detail>"

const AttrReadOnly, AttrHidden, AttrSystem, AttrDirectory, AttrArchive, AttrReparsePoint, AttrOffline,
      AttrEncrypted, AttrRecallOnOpen, AttrRecallOnDataAccess uint32
type FileInfo struct{ Name string; Size, MTime, CTime int64; Attrs, ReparseTag uint32; FileID string }
func (fi FileInfo) IsDir() bool
type FS interface{ Stat(path string) (FileInfo, error); ReadDir(dir string) ([]FileInfo, error); Probe(path string) (fileID string, err error) }
type OSFS struct{}                                  // implements FS (windows / other)
var ErrLocked, ErrEFS, ErrCloudPlaceholder, ErrPathTooLong, ErrProfileNeedsLogon error

type Entry struct{ Path, TokenPath string; FileInfo; Unchanged bool; Cached *CachedFile }
type CacheLookup func(ctx context.Context, path string) (CachedFile, bool, error)
func StoreCache(s *Store, jobID int64) CacheLookup
type WalkSpec struct{ ProfileSID string; Paths, Exclude []string; Tokens snapshot.Tokens; Profile guard.Profile; Epoch int64 }
func SpecFor(j *Job, tok snapshot.Tokens, prof guard.Profile, epoch int64) WalkSpec
type WalkStats struct{ Source tokens.Source; Dirs, Files, Unchanged, Bytes, Skipped, Excluded, Reparse int64; Complete bool }
type Walker struct{ Tokens tokens.Provider; Guard func(guard.Op, string, guard.Profile) error; FS FS; Cache CacheLookup; Log *slog.Logger }
func (w *Walker) Walk(ctx context.Context, spec WalkSpec, emit func(Entry) error, report func(Issue)) (WalkStats, error)
```

Walk semantics:
- **Token.** One token per walk.
- **Token failures.** `ErrNeedsLogon`, `ErrNotLoggedOn` and `ErrS4UUnavailable` produce one `profile_needs_logon` issue and an error wrapping `ErrProfileNeedsLogon`.
- **Impersonation.** One `Impersonate` per root `Stat`, per directory listing and per probe batch.
- **Guard.** `guard.Check(OpRead)` runs outside impersonation, on each root, directory and file.
- **Skips before any open.**
  - Reparse points are never followed; cloud, dedup, WOF and OneDrive tags are treated as files.
  - Placeholder files and folders are skipped without being opened.
  - EFS files are skipped when the token is S4U.
- **File cache.** A file is unchanged when its size, mtime, ctime and epoch match the cache and `h` is non-empty. Unchanged files are not probed.
- **Excludes.** Globs from `sdk/automation`, case-insensitive:
  - a pattern starting with `{` matches the token path;
  - a pattern containing `/` matches the path relative to the root;
  - any other pattern matches the name;
  - a trailing `/` limits the pattern to directories.
- **Roots.** Nested roots are merged into the outer one.
- **`Complete`.** False when any root or directory could not be resolved or listed.
- **Errors and cancellation.** An error from `emit` or from the file cache aborts the walk; cancelling `ctx` returns `ctx.Err()`.

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `go build ./...`: ok. An earlier run failed in `internal/keystore` and `internal/engine`, which other tasks were still editing; it was clean on the final run.
- `go vet ./internal/backup/...`: ok
- `GOOS=linux go vet ./internal/backup/...`: ok
- `GOOS=linux go test -c ./internal/backup/`: compiles
- `go test -count=1 ./internal/backup/...`: ok. Coverage:
  - **Store:** migrations (v5, and a reopen is a no-op), runs, file cache (`ChunkIDs`, seen, prune, drop), packs (idempotent add, pending, uploaded), issues (upsert count, reopen after report, `Report` mapping).
  - **Jobs:** the job model, job accessors and delete together with the cache.
  - **`SyncJobs`:** add, update keeping the suspend flag, equal revision as a no-op, stale, other device ignored, complete removal together with the cache, incomplete list removing nothing.
  - **`SyncFromServer`:** paging and errors.
  - **`JobSource`:** affected, suspend, resume.
  - **Walker:** fake token provider and fake FS. Cases: excludes, cache hit, wrong epoch and missing hash, placeholders not opened, EFS under S4U versus a session token, `ErrEFS`, locked, access denied, a file that vanished, guard denial, guard never called while impersonating, FS never called outside impersonation, impersonation count, token errors, nested, single-file, unmapped, missing and junction roots, a listing error making the walk incomplete, cancellation, an `emit` error, a cache error, an impersonation error.
  - **Real `OSFS`:** a temp tree, checking that the file id is stable and that missing paths give `ErrNotExist`.
- `gofmt -l internal/backup`: clean

## Contract issues
- None open. The `efs_needs_logon` and `profile_needs_logon` mapping follows api-v1 §14 Rev 1.1b. It is implemented by `WireClass` and `PendingIssue.Report`, and the local class is kept in `backup.db`.

## Gaps / follow-ups
- **Known-folder resolution.** Resolving the profile's known folders into `snapshot.Tokens` under the user's token was not done; `WalkSpec.Tokens` is supplied by the caller. Resolving them needs `SHGetKnownFolderPath` with the user token, or the profile's registry hive when the user is logged off. That belongs with the runner or profile code (P1-26 or the profiles owner).
- **Windows code paths not exercised by tests:**
  - error codes 32, 33, 6000, 362 and 206, and real cloud-placeholder or EFS files;
  - real impersonation (needs SYSTEM).
- **128-bit file ids.** `Probe` uses the 64-bit file index. On ReFS, `FileIdInfo` (128-bit) would be stricter. The id is informational only and not part of the cache key.

## Notes for dependent tasks / INT
- **Wiring:** `mods.RegisterSource(backup.NewJobSource(store))`, with the store from `backup.OpenDir(platform.StateDir())`.
- **Runner (P1-26):**
  - Build the spec with `SpecFor(job, tokens, guard.Profile{SID, Root}, epoch)` and the walker with `Walker{Cache: StoreCache(store, job.ID)}`.
  - Call `MarkFileSeen` for unchanged entries and `PutFileCache` after chunking.
  - Call `PruneFileCache` only when `WalkStats.Complete`.
  - Record issues with `AddIssue`, and send them with `PendingIssues(...)` then `.Report()`.
- **No new dependencies.** The package uses `golang.org/x/sys/windows`, which is already in `go.mod`.
- **Draft cleanup.** `planning/drafts/walker.go.draft` can be deleted; it is now `walker.go`.
