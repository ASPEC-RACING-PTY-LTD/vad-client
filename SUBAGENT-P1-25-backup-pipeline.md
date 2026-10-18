# P1-25 (DSK-C) Backup pipeline: chunk → index → pack → spool → upload
Status: completed

## Goal
Turn one job run into a stored, signed snapshot. The pipeline takes the walker's output and, for each file:
- reuses the file cache for unchanged files;
- otherwise chunks the file, dedupes against the repo index and a local seen-set, and packs new data.

It then spools sealed packs (bounded and resumable), uploads them through the repo backend, writes index objects, then the tree blobs and the snapshot root. It returns a result that the runner (P1-26) commits. It does not do the catalog commit or report runs and issues. (Resumed after the previous process exited before writing any owned file; all files below were written in this session.)

## Changes (repo: path, created/modified/deleted, why)
All in VaultDock, `desktop/internal/backup/`, all created:
- `pipeline.go`: `Pipeline`, `Params`, `Result`, `Run`.
  - Resume of pending packs, walk, read and chunk, dedup, data and meta packing, the snapshot root, and pruning.
  - Progress heartbeats and the issue mapping for read failures.
- `pipeline_tree.go`: the tree builder. It maps walker token paths to roots, builds the directory nodes and encodes trees bottom-up with `snapshot.EncodeTrees`. `usedTokens` is here too.
- `pipeline_open.go`: `Opener`/`OSOpener` (the open happens inside `Impersonate`), plus a shared provider so the walker and the reader use one profile token per run.
- `pipeline_folders.go`, `pipeline_folders_windows.go`, `pipeline_folders_other.go`: `FolderResolver` and `OSFolders`, which resolve known folders into `snapshot.Tokens` (fills the gap P1-24 left).
- `index.go`: `dedupIndex` (repo index plus seen-set) and `cacheQueue` (file-cache rows held until their packs are indexed).
- `spool.go`: the spool directory (`<SpoolDir>/<RepoID>/<oid>.pack`):
  - temp file + fsync + rename;
  - a byte bound that blocks writers and signals pressure;
  - a sweep of stray and temp files.
- `upload.go`: the `uploader` goroutine. It PUTs packs through `repo.SavePack` and writes an index every `IndexEvery` packs, or at once when the spool is full. Only after that does it `MarkPackUploaded` and remove the spool file.
- `pipeline_test.go`: the tests.

## Interfaces exposed
```go
const DefaultSpoolMax = 256 << 20; const DefaultIndexEvery = 16
const PhaseResume, PhaseScan, PhaseTrees, PhaseUpload, PhaseSnapshot = "resume", "scan", "trees", "upload", "snapshot"

type Pipeline struct {
    Store *Store; Repo *repo.Repo; RepoID int64 // server repo id (packs.repo_id, spool subdir)
    Tokens tokens.Provider; Walker Walker; Opener Opener; Folders FolderResolver
    SpoolDir string; SpoolMax int64; IndexEvery int; PackTarget int
    Progress func(RunStats); ProgressEvery time.Duration; OnIssue func(Issue)
    Log *slog.Logger; Now func() time.Time
}
type Params struct {
    JobID int64; RunUUID string; Spec WalkSpec // Tokens nil → Folders; Epoch 0 → repo epoch
    Root snapshot.Root // Parent, Job, Device, Host, Profile, InstallMode, Tags supplied by caller
    DeviceKey ed25519.PrivateKey // VD1 key that signs the root
}
type Result struct {
    SnapshotOID string; Root *snapshot.Root; Stats RunStats; Walk WalkStats; Issues []Issue
    Packs []string; Resumed int; Pruned int64
}
func (p *Pipeline) Run(ctx context.Context, in Params) (*Result, error) // Result is non-nil on error too

type Opener interface{ Open(tok tokens.Token, path string) (io.ReadCloser, error) }
type OSOpener struct{}
type FolderResolver interface{ Tokens(ctx context.Context, prof guard.Profile) (snapshot.Tokens, error) }
type OSFolders struct{}
```

### Semantics

**Token.** There is one token per run, from `Pipeline.Tokens.ForProfile`. The walker gets it through a non-closing wrapper, and the pipeline closes it. A token failure goes through the walker, so the `profile_needs_logon` issue is reported and `ErrProfileNeedsLogon` is returned.

**Unchanged files.** The cached chunk list is reused only when every chunk is still in the repo index or the seen-set; otherwise the file is re-read. The pipeline then calls `MarkFileSeen`.

**Changed files.**
- The file is read through `Opener` and chunked with `vd-fastcdc-1`.
- `h` is HMAC with `K_fh`.
- Blob ids are HMAC with `K_id`.
- Known-compressed extensions are stored raw.
- If the bytes read differ from the walked size, the file is not cached.

**Packs.**
- Data and meta pack writers use the current epoch, with `pack_target` from the config (`PackTarget` overrides it, for tests).
- On `ErrPackFull` the pack is sealed and the blob retried.
- Sealing a pack means: spool it, then `AddPack`, then queue it for upload.

**Upload and index order.**
- The order is PUT → `FlushIndex` → `MarkPackUploaded` → delete the spool file, which keeps §11 (packs, then index).
- A pack is never marked uploaded before an index lists it.
- An upload or index error cancels the run; the error returned is the upload error, not `context.Canceled`.

**File cache.** A row is written (with `seen_run = RunUUID` and the epoch) only after every data pack up to the one holding the file's last new chunk has been indexed. The cache therefore never points at data that is not stored.

**Resume.**
- At start the pipeline sweeps strays.
- It parses each pending pack's header, adds its blobs to the seen-set (so they are not packed again) and uploads it before new packs.
- A pending pack whose file is missing or corrupt is retired with `MarkPackUploaded` and a warning.

**Snapshot.**
- Trees are built after the walk, then `Repo.SaveSnapshot` runs with `DeviceKey`.
- `Root.Tokens` holds only the tokens the roots use.
- `IssuesSummary` counts skipped issues by wire class.
- `InstallMode` defaults to `simple`.

**Pruning.** `PruneFileCache(job, run)` runs only when the snapshot was saved and `WalkStats.Complete` is true.

**Roots.**
- A directory root's `token_path` is the spec path.
- A single-file root is recorded as a tree containing just that file, under the file's parent token path. Roots sharing a parent share one tree.
- Nested roots are attributed to the shortest spec prefix, matching the walker's merge.

**Progress.** Heartbeats come at most every `ProgressEvery` (default 1s), plus one forced heartbeat at each phase change. `BytesUploaded` and `QueuePacks` come from the uploader.

**Known folders (Windows).**
- The source is `HKU\<SID>\…\Explorer\User Shell Folders`, with `%USERPROFILE%` taken as `prof.Root` and other variables expanded with `registry.ExpandString`. This respects folder redirection, including UNC paths.
- Relative or unexpandable values fall back to the defaults below `prof.Root`.
- If the hive is not loaded, the defaults are used.
- Machine tokens come from `KnownFolderPath`.
- On non-Windows builds, `OSFolders` returns the defaults only.

## Commands run and results
All from `D:/VaultDock/VaultDock/desktop`:
- `go build ./...`: ok
- `go vet ./internal/backup/...`: ok
- `GOOS=linux go vet ./internal/backup/...`: ok
- `GOOS=linux go test -c ./internal/backup/`: compiles
- `go test -count=1 ./internal/backup/...`: ok. All P1-24 tests still pass. New tests, over `repo.MemBackend` with a failure-injecting wrapper, the fake FS and token, and a fake opener that checks the open happens inside impersonation:
  - **First backup:** dedup of duplicate content, root stats and tokens, a signed root that verifies, and a restore of every file (content and keyed hash) from a freshly reopened repo, which proves the index objects were written. Also checks the file cache rows, an empty spool and progress phases.
  - **Incremental:** only changed or new files are opened; `BytesNew` counts only the new bytes; the unchanged file is marked seen; the deleted file's cache row is pruned; a third, unchanged run writes no data packs.
  - **Resume after an interrupted upload:** an injected PUT failure returns the injected error, leaves no snapshot, and leaves the pending packs in the spool. The next run, in a new Store and a reopened repo, resumes all of them and restores correctly.
  - **Cancellation:** returns `context.Canceled` with no snapshot, the token closed and pending packs spooled. The next run completes.
  - **Spool bound:** with `IndexEvery = 1000`, a spool of about one pack must force index flushes (also a deadlock guard).
  - **Unreadable files:** locked and access-denied files become issues and appear in `IssuesSummary`; they are not in the tree and not cached.
  - Also covered: the `FolderResolver` used when `Tokens` is nil; a single-file root; parameter validation; `profileTokens` (redirection, UNC, relative, unexpanded, case-insensitive GUID names); spool reserve blocking, pressure, cancellation and oversize packs.
- `gofmt -l internal/backup`: clean. The race detector was not run because cgo is unavailable on this host.

## Contract issues
None.

## Gaps / follow-ups
- **Directories.** The walker emits files only. Empty directories are not in snapshots, and directory entries carry no mtime or attributes; their `A` is set to `AttrDirectory`. Roots with no files are omitted from `Root.Roots`. This needs a walker change (P1-24 owner) that emits directory entries.
- **Names.** Names that are not valid UTF-8 or that contain `:` are skipped with an `other` issue. `nb` (raw UTF-16) is not written, and the walker's UTF-16 to string conversion is already lossy.
- **Memory.** The tree is held in memory for the whole run, with one `snapshot.Entry` per file. Streaming the directory encoding would need the walker's depth-first order guaranteed.
- **Idle seal.** The 30 s idle seal rule (§7.3) is not implemented. Packs are sealed at the target size and at the end of the job.
- **ADS, placeholder extension and inventory.** Not produced. `Root.Inventory` is left nil.
- **Spool sharing.** The spool is per repo id. Pending packs left by another job on the same repo are uploaded by whichever job runs next. That is intended, but the packs count against that run's `Packs` and `Resumed`.
- **Retired rows.** Pending rows whose spool file is missing or corrupt are retired with `MarkPackUploaded`, because `Store` has no `DeletePack`. A `DeletePack` accessor in `store.go` would be cleaner.
- **Real Windows paths.** `OSOpener` (`os.Open` under impersonation) and the registry folder resolution have not been exercised under real impersonation (that needs SYSTEM).

## Notes for dependent tasks / INT
**Runner (P1-26).**
1. Open the repo with `keystore.OpenRepo(ctx, server.New(client, repoID, …))`.
2. Build `Pipeline{Store, Repo, RepoID, SpoolDir: filepath.Join(platform.StateDir(), "spool"), Walker: Walker{}}` and `Params{JobID, RunUUID, Spec: SpecFor(job, nil, guard.Profile{SID, Root}, 0), Root: {Parent, Job, Device{ID, Name, KeyID}, Host, Profile, InstallMode}, DeviceKey}`.
   - With `Tokens` nil, the spec's tokens are resolved by `OSFolders`.
3. Then:
   - on success, `CommitSnapshot` with `Result.SnapshotOID` and `Result.Root`;
   - store `Result.Issues` with `AddIssue`, or use `OnIssue`;
   - persist `Result.Stats` with `UpdateRunStats`;
   - wire `Progress` to the run heartbeat.
- `Result.Issues` is non-empty, or `Walk.Complete` is false: the status is `partial`.
- The run returns `ErrProfileNeedsLogon`: the job is skipped.
- A different `Repo.Epoch()` from the cache epoch needs no action; it is handled by the walker's epoch check.
- **Device key and root signer:** `Root.Device.KeyID` must name the device's key file, so that `DeviceSignKey` verifies the signature.

**Store (P1-24 owner), optional:** add `DeletePack(ctx, oid)` for retiring corrupt spool rows.

**No new dependencies.** `golang.org/x/sys/windows/registry` is already used by `internal/profiles`.
