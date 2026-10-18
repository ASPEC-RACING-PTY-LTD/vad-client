# P1-16 (SRV-C) Catalog ingestion (escrowed K_meta), DEK, blind index, tree/search
Status: completed

## Goal
Implement api-v1 §5.2 step 3 and §5.4 tree/search:
- after a snapshot commit, ingest its tree metadata asynchronously, idempotently and resumably;
- decrypt with the escrowed key bundle (audited, purpose `catalog_ingest`);
- store names and paths encrypted at rest with a per-user DEK;
- search through an HMAC blind index;
- skip gracefully, with the state recorded, when the server cannot read the metadata;
- update `repositories.last_catalog_sync_at`.

## Changes (repo: path, created/modified/deleted, why)
All changes are in vad-server.
- `internal/catalog/catalog.go` (created): `Service`, the worker (`Run`, `Enqueue`, startup and periodic pending scan), `Ingest`, the tree walk and the skip/failure bookkeeping.
- `internal/catalog/reader.go` (created): a metadata-only reader over `backup.Store`. It does the following:
  - unwraps the newest escrow key file through `keys.Manager.OpenKeyFile` (audited);
  - derives `K_meta` (full bundle) or takes it as-is (meta bundle), then runs `vdcrypt.WalkMeta`;
  - reads every index with `repo.OpenIndex` and the root with `snapshot.OpenRoot`;
  - opens tree blobs from ranged pack reads with `K_meta` (AEAD, blob id bound by the AD).
- `internal/catalog/crypto.go` (created): DEK custody, field encryption, blind index and tokenisation.
- `internal/catalog/browse.go` (created): `Tree` and `Search`, which decrypt rows into `wire.CatalogEntry`.
- `internal/catalog/crypto_test.go` (created): token and prefix rules, AD binding, and per-user keying.
- `internal/store/catalog.go` (created): `catalog_keys`, ingest state, clear/insert of catalog rows, children by parent blind index, token search, `last_catalog_sync_at`.
- `internal/api/catalog.go` (created): the `Catalog()`/`RunCatalog()`/`queueCatalogIngest()` wiring and the tree/search handlers.
- `internal/api/catalog_test.go` (created): end-to-end tests with a real SDK repository over the server's objects.
- `internal/api/snapshots.go` (modified, 3 lines): the tree and search routes now point at the new handlers, and `s.queueCatalogIngest(sn.ID)` is called after a new (non-replay) commit.
- `internal/app/app.go` (modified, 1 line): `go a.Server.RunCatalog(ctx)` in `App.Run`.
- Migrations: none. The migration 3 schema (`snapshot_files`, `snapshot_file_tokens`, `catalog_keys`, `snapshots.ingest_*`) is used as-is.

## Interfaces exposed
Routes (sess; snapshot owner **or admin**, everyone else gets `404 snapshot_not_found`):

| Route | Behaviour |
|---|---|
| `GET /snapshots/{id}/tree?path=&cursor=&limit=` | Returns `List[wire.CatalogEntry]`. `path=""` lists the backed-up roots (for example `{DOCUMENTS}`). Children come in tree order (bytewise by name within a directory). The cursor is the last row id. An unknown path gives 404 `path_not_found`; a file path gives 422 `validation`. |
| `GET /snapshots/{id}/search?q=&type=&min_size=&max_size=&cursor=&limit=` | Every word of `q` must match a whole name word or a prefix of one (at least 2 characters). Matching is case-insensitive, and words are runs of letters and digits. `type` is `file`, `dir` or `symlink`. Bad `q`, `type` or size values give 422 `validation`. |

Common errors on both routes:
- `409 not_browsable` while `ingest_state != done`.
- `409 escrow_unavailable` without key custody.

Go, package `internal/catalog`:
```go
const SkipPrefix = "escrow_unavailable: "
var ErrNoCatalogKey, ErrPathNotFound, ErrNotDirectory error
type Config struct { Store *store.Store; Keys func() *keys.Manager; Objects func(ctx, *store.Repository) (*backup.Store, error); Logger *slog.Logger }
func New(Config) *Service
func (s *Service) Enqueue(snapshotID int64)            // non-blocking
func (s *Service) Run(ctx context.Context)              // scan pending, then queue + rescan every minute
func (s *Service) Ingest(ctx context.Context, snapshotID int64) error // only pending; nil after recording done/failed; error = stays pending (retry)
type SearchQuery struct { Q, Type string; MinSize, MaxSize, AfterID int64; Limit int } // sizes -1 = unset
type Page struct { Items []wire.CatalogEntry; NextCursor string }
func (s *Service) Tree(ctx, sn *store.Snapshot, path string, afterID int64, limit int) (Page, error)
func (s *Service) Search(ctx, sn *store.Snapshot, q SearchQuery) (Page, error)
func HasQueryWords(q string) bool
```

Package `api`:
```go
func (s *Server) Catalog() *catalog.Service  // lazily created per Server
func (s *Server) RunCatalog(ctx context.Context)
```
Ingestion only runs while `RunCatalog` runs; the commit hook just enqueues. `api_test.go` `newEnv` does not start it, so existing tests still see `pending`.

Package `store`:
```go
type CatalogKey struct{ UserID int64; KEKID string; Wrapped []byte; CreatedAt, RotatedAt int64 }
func (s *Store) CatalogKey(ctx, userID) (*CatalogKey, error)
func (s *Store) InsertCatalogKey(ctx, *CatalogKey) (inserted bool, err error)
func (s *Store) PendingIngestSnapshots(ctx, limit) ([]int64, error)
func (s *Store) SetSnapshotIngest(ctx, snapshotID int64, state, errText string) error
func (s *Store) SnapshotIngestError(ctx, snapshotID) (string, error)
func (s *Store) TouchCatalogSync(ctx, repoID, at int64) error
type CatalogFile struct{ ID, SnapshotID int64; PathBI, ParentBI, PathCT, NameCT []byte; Tokens [][]byte; Type string; Size, MTime, Mode int64 }
func (s *Store) ClearCatalogFiles(ctx, snapshotID) error
func (s *Store) InsertCatalogFiles(ctx, snapshotID int64, files []CatalogFile) error
func (s *Store) CatalogFileByPath(ctx, snapshotID int64, pathBI []byte) (*CatalogFile, error)
func (s *Store) CatalogChildren(ctx, snapshotID int64, parentBI []byte, afterID int64, limit int) ([]CatalogFile, error)
type CatalogSearchFilter struct{ SnapshotID int64; Tokens [][]byte; Type string; MinSize, MaxSize, AfterID int64; Limit int }
func (s *Store) SearchCatalog(ctx, CatalogSearchFilter) ([]CatalogFile, error)
```

At-rest scheme:
- **DEK.** Each user has a random 32-byte DEK in `catalog_keys.dek_wrapped_by_kek`. It is sealed with XChaCha20-Poly1305 under a wrapping key `HKDF(SEK_priv_v, "dek-wrap")`, with AD `vaultdock.catalog-dek/1:<user>:<kek_id>`, and `kek_id = "sek:<v>"`.
  - The SEK private key is opened through `keys.Manager.OpenSEK` (KEK-sealed, audited `open_sek`, purpose `catalog_ingest`).
  - The derived wrapping key is cached per SEK version for the life of the process.
- **Row keys.** HKDF of the DEK gives `enc` and `blind-index`.
- **Ciphertexts.** `path_ct`/`name_ct` are XChaCha20-Poly1305 with AD `vaultdock.catalog/1:<snapshot_id>:<path|name>`.
- **Path index.** `path_bi`/`parent_bi` = `HMAC(bi, "p\0"+path)`; the parent of the roots is `""`.
- **Search tokens.** Each token is `HMAC(bi, "t\0"+tok)[:16]`. The tokens are the lowercased words and their prefixes of 2 or more runes; words are cut at 32 runes and names keep at most 96 tokens. They are stored in `blind_tokens` and mirrored into `snapshot_file_tokens`.
- **Other columns.** `mtime` is in ms (tree `m` ns / 1e6) and `mode` holds the FILE_ATTRIBUTE bits.

Ingestion states:
- `done` after a successful walk, which also sets `last_catalog_sync_at`.
- `failed` with `ingest_error`:
  - `escrow_unavailable: …` when there is no KEK or no escrow key file the server can open (the skip case);
  - `metadata: …` or `walk: …` for unreadable or corrupt metadata.
- Stays `pending` on transient errors (storage unavailable, DB errors), retried by the next scan.
- A re-ingestion clears the snapshot's rows first.

## Commands run and results
All run from `D:/VaultDock/vad-server`:
- `go build ./...`: OK.
- `go vet ./internal/catalog/ ./internal/api/ ./internal/store/ ./internal/app/`: OK.
- `gofmt -l` on all owned and edited files: clean.
  - My first edit converted `snapshots.go` and `app.go` to CRLF; I restored LF.
- `go test ./internal/catalog/ -count=1`: ok.
- `go test ./internal/api/ -run TestCatalog -v`: both tests pass.
  - `TestCatalogIngestBrowseSearch` uses a real `sdk/repo` repository written through the SDK server backend, with full escrow. It covers:
    - the worker started via `RunCatalog`, with the commit hook enqueueing;
    - `browsable` and `last_catalog_sync_at`;
    - tree root, children, paging and a nested directory;
    - 404 for an unknown path and 422 for a file path;
    - search by prefix, multiple words, case, no substring, type and size filters, and bad params;
    - another user (404) and admin (200);
    - no plaintext in `snapshot_files`, the `sek:` kek_id, and the `catalog_ingest` unwrap audit row;
    - idempotent re-run and forced re-ingest (same 5 rows);
    - a repo without an escrow wrap: `failed`, `escrow_unavailable:` reason, tree 409;
    - a **metadata-escrow** repo: ingested and searchable.
  - `TestCatalogWithoutKeyCustody`: without a KEK the snapshot is skipped (recorded), and search answers 409.
- `go test ./internal/... -count=1`: all packages pass.

## Contract issues
1. **Snapshot download URL path (§5.4):** not implemented here; the download and content routes are outside P1-16's routes. Suggested route for whoever implements `POST /snapshots/{id}/download`: `GET /api/v1/snapshots/downloads/{token}` (single-use, 1 h). No route was registered.
2. **Error codes not in §1.1:**
   - `409 not_browsable` is reused from rev 1.0 §5.4.
   - `404 path_not_found` (tree) is new.
   - INT may want to list both.
3. **`symlink` type:** `wire` defines only `file`/`dir`. Symlinks and reparse points are returned as `"symlink"`, the rev 1.0 listing type, and `type=symlink` is accepted in search.
4. **Interpretation:** the task says to skip under "metadata-only". backup-format-v1 §1.2 and D9 say that metadata escrow still lets the server browse, so I ingest metadata-escrow repositories with `K_meta`. Only "no KEK" and "no openable escrow wrap" are skipped. If INT really wants metadata-escrow skipped, it is a one-line check on `rp.EscrowMode` in `Ingest`.
5. **Admin access:** per the task, admins may browse any user's catalog. `GET /snapshots/{id}` (P1-15) is owner-only, so an admin can browse a tree but gets 404 on the snapshot itself. INT should align one way or the other.

## Gaps / follow-ups
- **DEK wrapping:** the task asked for "wrapped by KEK via internal/keys", but `internal/keys` exposes no general KEK seal and I may not edit it. The DEK is therefore wrapped under an HKDF of the KEK-sealed SEK private key; that key is opened by `OpenSEK` (audited), and `kek_id` records the SEK version.
  - Recommended: add `keys.Manager.SealData(kind string, plaintext)/OpenData` over the KEK, and switch `crypto.go`'s `wrapKey`/`createDEK`/`userKeysFor`.
  - The switch can coexist with old rows: use a different `kek_id` prefix, for example `kek:1`.
- **Not implemented here:**
  - `POST /catalog/rebuild` (still 501). The building blocks exist: set `ingest_state` back to `pending` and `Enqueue`/`Ingest`.
  - Diff, content and download.
  - DEK rotation (`rotated_at` stays unused).
- **Snapshot root signature:** not verified against the device key file during ingestion (backup-format §8.1 treats a missing key as a warning).
- **Large catalogs:** search rows grow with name length, roughly `#words × prefix lengths`, capped at 96 per name.
- **Ingestion limits:** a snapshot may have at most 5M entries and a tree depth of 512. Indexes, chains and key files are read whole, each at most 64 MiB.
- **Retries:** failed snapshots are not retried automatically. For example, a snapshot committed before the KEK existed needs a rebuild.

## Notes for dependent tasks / INT
- **P1-21 (readiness):** `snapshots.ingest_state`/`ingest_error` (the `escrow_unavailable:` prefix means skipped) and `repositories.last_catalog_sync_at` are now maintained.
- **P1-36 (integration):** `testserver` runs `app.Run`, which starts `RunCatalog`. Wait for `GET /snapshots/{id}` to show `browsable:true`, then call tree/search.
- **Web (P1-32):**
  - The tree cursor is opaque (a row id).
  - `path` is the full token path from a previous item.
  - Search needs at least one word.
- **`catalog_rebuild` owner:** reset the state with `store.SetSnapshotIngest(ctx, id, "pending", "")`, then call `s.Catalog().Ingest(ctx, id)` (or `Enqueue`).
