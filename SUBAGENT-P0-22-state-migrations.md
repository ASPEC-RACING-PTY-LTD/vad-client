# P0-22 (DSK-H) dbutil + state.db schema migrations
Status: completed

## Goal
Add a shared SQLite open/migration helper (`desktop/internal/dbutil`) and move `state.db` onto versioned migrations. Adopt pre-migration databases. Add the §8.2 tables: issues, outbox, schedule_last, modules, profiles and sync_base. The DB location is unchanged; ProgramData is P0-28.

## Changes (repo: path, created/modified/deleted, why)
- VaultDock: `desktop/internal/dbutil/dbutil.go` (created). `Open` (WAL, busy_timeout 10000, synchronous NORMAL, foreign_keys on, `_txlock=immediate`, 4 conns, parent dir 0700) and the ordered migration runner.
- VaultDock: `desktop/internal/dbutil/dbutil_test.go` (created). Tests pragmas, idempotence, append-only upgrade, rollback on failure, baseline adoption, gap rejection and FK enforcement.
- VaultDock: `desktop/internal/state/migrate.go` (created). Migration list v1–v7 and the legacy baseline.
- VaultDock: `desktop/internal/state/state.go` (modified).
  - `schema` was renamed to `schemaV1` (migration 1).
  - `Open` now uses `dbutil.Open` + `dbutil.Migrate`. Its signature and crash recovery are unchanged.
  - Added `SchemaVersion` and the schedule_last and sync_base accessors.
- VaultDock: `desktop/internal/state/state_test.go` (created). Tests a fresh DB, adopting a legacy v0 DB (data kept, queue crash recovery still works), a partial legacy DB, idempotent reopen with re-migrate, and the accessors.

## Interfaces exposed
```go
// package dbutil
func Open(path string) (*sql.DB, error)
type Migration struct { Version int; Name string; SQL string; Up func(tx *sql.Tx) error }
type Baseline func(tx *sql.Tx) (int, error)
func Migrate(db *sql.DB, migs []Migration, baseline Baseline) (int, error)
func Version(db *sql.DB) (int, error)
func TableExists(q interface{ QueryRow(string, ...any) *sql.Row }, name string) (bool, error) // *sql.DB / *sql.Tx
```
- Versions must be contiguous from 1, and the list is append-only.
- Each migration runs in its own immediate transaction together with its `schema_version` row. The "already applied?" check runs inside that transaction.
- Baseline runs only when `schema_version` is empty. The versions it returns are recorded with `adopted=1`.
- A DB newer than the known migrations is left untouched (no error).

`schema_version(version INTEGER PK, name, applied_at ms, adopted 0|1)`.

```go
// package state
func Open(path string) (*Store, error)               // unchanged signature
func (s *Store) SchemaVersion() (int, error)
func (s *Store) ScheduleLast(key string) (last int64, ok bool, err error) // key "<automation id>/<node id>", unix ms
func (s *Store) SetScheduleLast(key string, last int64) error
type SyncBase struct { FolderID, RelPath, NodeID string; Revision int64; SHA256 string; Size, MTime int64 } // MTime unix ns
func (s *Store) SyncBase(folderID, rel string) (*SyncBase, error) // ErrNotFound
func (s *Store) PutSyncBase(b SyncBase) error
func (s *Store) DeleteSyncBase(folderID, rel string) error
```

Schema (all timestamps unix ms unless noted):

| v | Name | Tables |
|---|---|---|
| 1 | base | configs, files, queue, history, kv (unchanged legacy schema) |
| 2 | issues | `issues(id, source, config_id, run_id, rel_path, class, message, count, state 'open', first_seen, last_seen, reported_at NULL, UNIQUE(source, config_id, rel_path, class))` |
| 3 | outbox | `outbox(id, kind, method, path, body BLOB, dedupe_key UNIQUE NULL, attempts, next_at, last_error, created_at)` |
| 4 | schedule_last | `schedule_last(key PK, last_fire)` |
| 5 | modules | `install_mode(id=1 CHECK, mode, core_version, changed_at, reported_at)`; `modules(id PK, version, state 'disabled', error, updated_at)` |
| 6 | profiles | `profiles(sid PK, username, profile_path, state, consent_id, consent_at, consent_blob BLOB, proof_digest, request_id, request_scope, requested_by, blacklisted_at, blacklist_reason, last_security_check, last_security_signal, last_security_evidence, reported_at, updated_at)` |
| 7 | sync_base | `sync_base(folder_id, rel_path, node_id, revision, sha256, size, mtime ns, updated_at, PK(folder_id, rel_path))` |

Indexes on these tables:
- `issues`: `issues_state(state, last_seen)` and the partial index `issues_unreported`.
- `outbox`: `outbox_ready(next_at, id)`.

Field notes:
- `issues.class` uses the api-v1 §6 issue classes, plus local scan classes.
- The value sets for `profiles.state` and `blacklist_reason` follow api-v1 §6B. There are no CHECK constraints, so later contract additions need no table rebuild.

## Commands run and results
- `cd desktop && go build ./...`: ok
- `go vet ./internal/state/ ./internal/dbutil/`: ok
- `go test ./internal/state/ ./internal/dbutil/`: ok
- `cd desktop/e2e && go test ./...`: ok (18s)
- `go test ./internal/engine/`: ok (regression check)
- `gofmt -l` on the touched packages: clean

## Contract issues
None.

## Gaps / follow-ups
- Only trivial accessors were added: schedule_last and sync_base. The issues, outbox, modules/install_mode and profiles accessors belong to the feature tasks (P0-23, P0-25, P0-38, profiles tasks).
- There is no per-folder sync change-feed cursor (`next_since`) table. Use `kv` or add a migration in the sync task.
- Opening a legacy DB now also enables `foreign_keys`. The legacy schema has no FKs, so this is harmless.

## Notes for dependent tasks / INT
- New schema changes are new `dbutil.Migration` entries appended to `migrations` in `state/migrate.go`. Never edit released steps.
- `backup.db` (P1-24) should use `dbutil.Open` + `dbutil.Migrate(db, itsMigrations, nil)`.
- P0-23 (B3): replace the in-memory `scheduleLast` map with `Store.ScheduleLast` / `SetScheduleLast`. The key format matches the existing `a.ID + "/" + n.ID`.
- P0-28 only needs to change the path passed to `state.Open`. dbutil creates the parent dir with 0700, and ProgramData ACLs are that task's concern.
- No new dependencies.
