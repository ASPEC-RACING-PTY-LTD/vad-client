// Package backup is the client backup engine (MASTER_PLAN §7.9): the local
// backup.db (job mirror, runs, file cache, packs, pending issues), the job
// model and the profile-token walker. The pipeline, upload and runner live
// in their own files (P1-25, P1-26).
package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/dbutil"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// FileName is backup.db's name inside the ProgramData state directory.
const FileName = "backup.db"

// ErrNotFound is returned by single-row accessors when no row matches.
var ErrNotFound = errors.New("backup: not found")

// migrations is the ordered backup.db schema. Append only; never edit a
// released step. Timestamps are unix milliseconds unless noted; mtime and
// ctime are unix nanoseconds.
var migrations = []dbutil.Migration{
	// Mirror of this device's server jobs (api-v1 §6). job is the full
	// wire.Job JSON; the columns repeat what queries need.
	// suspended_by_mode is local (set by the module ItemSource).
	{Version: 1, Name: "jobs", SQL: `
CREATE TABLE jobs (
	id INTEGER PRIMARY KEY, revision INTEGER NOT NULL, profile_sid TEXT NOT NULL DEFAULT '',
	server_state TEXT NOT NULL DEFAULT 'active', enabled INTEGER NOT NULL DEFAULT 1,
	suspended_by_mode INTEGER NOT NULL DEFAULT 0, job TEXT NOT NULL,
	synced_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);`},
	// One row per run attempt, keyed by the idempotent client_run_uuid.
	// run_id is the server id (0 until POST …/runs answered). stats is the
	// last wire.RunProgress JSON.
	{Version: 2, Name: "runs", SQL: `
CREATE TABLE runs (
	client_run_uuid TEXT PRIMARY KEY, job_id INTEGER NOT NULL, run_id INTEGER NOT NULL DEFAULT 0,
	epoch INTEGER NOT NULL, trigger TEXT NOT NULL DEFAULT '', status TEXT NOT NULL,
	stats TEXT NOT NULL DEFAULT '{}', snapshot_id INTEGER, error TEXT NOT NULL DEFAULT '',
	started_at INTEGER NOT NULL, finished_at INTEGER, updated_at INTEGER NOT NULL
);
CREATE INDEX runs_job ON runs(job_id, started_at);`},
	// Incremental file cache: a file whose size, mtime and ctime match and
	// whose entry was made under the same key epoch is not re-read. h is
	// the keyed whole-file hash (K_fh); chunks is the concatenation of the
	// 32-byte data blob ids in file order.
	{Version: 3, Name: "file_cache", SQL: `
CREATE TABLE file_cache (
	job_id INTEGER NOT NULL, path TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL,
	ctime INTEGER NOT NULL DEFAULT 0, attrs INTEGER NOT NULL DEFAULT 0, file_id TEXT NOT NULL DEFAULT '',
	h BLOB, chunks BLOB, epoch INTEGER NOT NULL, seen_run TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL,
	PRIMARY KEY (job_id, path)
);
CREATE INDEX file_cache_seen ON file_cache(job_id, seen_run);`},
	// Packs built locally (backup-format §7.3). class: 0 data, 1 meta.
	{Version: 4, Name: "packs", SQL: `
CREATE TABLE packs (
	oid TEXT PRIMARY KEY, repo_id INTEGER NOT NULL, epoch INTEGER NOT NULL, class INTEGER NOT NULL DEFAULT 0,
	size INTEGER NOT NULL, run_uuid TEXT NOT NULL DEFAULT '', uploaded INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL, uploaded_at INTEGER
);
CREATE INDEX packs_pending ON packs(repo_id, uploaded);`},
	// Issues waiting to be reported (POST /backup/runs/{id}/issues). class
	// is an api-v1 §6 class or a local one (efs_needs_logon,
	// profile_needs_logon); see WireClass.
	{Version: 5, Name: "issues", SQL: `
CREATE TABLE issues (
	id INTEGER PRIMARY KEY, job_id INTEGER NOT NULL, run_uuid TEXT NOT NULL, path TEXT NOT NULL DEFAULT '',
	class TEXT NOT NULL, severity TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '', retryable INTEGER NOT NULL DEFAULT 0,
	count INTEGER NOT NULL DEFAULT 1, first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL, reported_at INTEGER,
	UNIQUE (run_uuid, path, class)
);
CREATE INDEX issues_unreported ON issues(run_uuid, id) WHERE reported_at IS NULL;`},
	// The committed snapshot's root oid (Root.Parent of the job's next
	// run), recorded with snapshot_id right after the commit. Looking runs
	// up by server id (Retry Skipped) uses runs_run_id.
	{Version: 6, Name: "runs_snapshot_oid", SQL: `
ALTER TABLE runs ADD COLUMN snapshot_oid TEXT NOT NULL DEFAULT '';
CREATE INDEX runs_run_id ON runs(run_id);`},
}

// Store is backup.db.
type Store struct {
	DB  *sql.DB
	Now func() time.Time // nil = time.Now
}

// Open opens (creating and migrating) backup.db at path. The caller passes
// the ProgramData state dir path (platform.StateDir()); see OpenDir.
func Open(path string) (*Store, error) {
	db, err := dbutil.Open(path)
	if err != nil {
		return nil, err
	}
	if _, err := dbutil.Migrate(db, migrations, nil); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

// OpenDir opens dir\backup.db.
func OpenDir(dir string) (*Store, error) { return Open(filepath.Join(dir, FileName)) }

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

// SchemaVersion is the applied schema version.
func (s *Store) SchemaVersion() (int, error) { return dbutil.Version(s.DB) }

func (s *Store) nowMS() int64 {
	if s.Now != nil {
		return s.Now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

func nullInt(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: v != 0} }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// --- runs ---

// Run is one local run attempt.
type Run struct {
	ClientRunUUID string
	JobID         int64
	RunID         int64 // server run id; 0 until registered
	Epoch         int64 // key epoch the run writes under
	Trigger       string
	Status        string // wire.RunStatus*
	Stats         RunStats
	SnapshotID    int64  // 0 = none
	SnapshotOID   string // committed snapshot root oid; "" = none
	Error         string
	StartedAt     int64
	FinishedAt    int64 // 0 = running
}

// PutRun inserts or replaces a run.
func (s *Store) PutRun(ctx context.Context, r Run) error {
	if r.ClientRunUUID == "" {
		return errors.New("backup: run without client_run_uuid")
	}
	stats, err := marshalStats(r.Stats)
	if err != nil {
		return err
	}
	if r.StartedAt == 0 {
		r.StartedAt = s.nowMS()
	}
	// An upsert (not INSERT OR REPLACE) keeps the row's rowid, which breaks
	// started_at ties in Runs.
	_, err = s.DB.ExecContext(ctx, `INSERT INTO runs
		(client_run_uuid, job_id, run_id, epoch, trigger, status, stats, snapshot_id, snapshot_oid, error, started_at, finished_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(client_run_uuid) DO UPDATE SET job_id = excluded.job_id, run_id = excluded.run_id, epoch = excluded.epoch,
			trigger = excluded.trigger, status = excluded.status, stats = excluded.stats, snapshot_id = excluded.snapshot_id,
			snapshot_oid = excluded.snapshot_oid, error = excluded.error, started_at = excluded.started_at,
			finished_at = excluded.finished_at, updated_at = excluded.updated_at`,
		r.ClientRunUUID, r.JobID, r.RunID, r.Epoch, r.Trigger, r.Status, stats, nullInt(r.SnapshotID), r.SnapshotOID, r.Error,
		r.StartedAt, nullInt(r.FinishedAt), s.nowMS())
	return err
}

const runCols = `client_run_uuid, job_id, run_id, epoch, trigger, status, stats, snapshot_id, snapshot_oid, error, started_at, finished_at`

func scanRun(sc interface{ Scan(...any) error }) (Run, error) {
	var r Run
	var stats string
	var snap, fin sql.NullInt64
	if err := sc.Scan(&r.ClientRunUUID, &r.JobID, &r.RunID, &r.Epoch, &r.Trigger, &r.Status, &stats, &snap, &r.SnapshotOID, &r.Error,
		&r.StartedAt, &fin); err != nil {
		return r, err
	}
	r.SnapshotID, r.FinishedAt = snap.Int64, fin.Int64
	return r, json.Unmarshal([]byte(stats), &r.Stats)
}

// Run loads a run by client_run_uuid (ErrNotFound).
func (s *Store) Run(ctx context.Context, uuid string) (*Run, error) {
	r, err := scanRun(s.DB.QueryRowContext(ctx, "SELECT "+runCols+" FROM runs WHERE client_run_uuid = ?", uuid))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// RunByServerID loads the newest local run with the given server run id
// (ErrNotFound).
func (s *Store) RunByServerID(ctx context.Context, runID int64) (*Run, error) {
	if runID <= 0 {
		return nil, ErrNotFound
	}
	r, err := scanRun(s.DB.QueryRowContext(ctx, "SELECT "+runCols+" FROM runs WHERE run_id = ? ORDER BY started_at DESC, rowid DESC LIMIT 1", runID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Runs lists a job's runs, newest first (limit <= 0 = all).
func (s *Store) Runs(ctx context.Context, jobID int64, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT "+runCols+" FROM runs WHERE job_id = ? ORDER BY started_at DESC, rowid DESC LIMIT ?", jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UnfinishedRuns lists runs without a finish time (crash recovery).
func (s *Store) UnfinishedRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+runCols+" FROM runs WHERE finished_at IS NULL ORDER BY started_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetRunServerID records the server run id.
func (s *Store) SetRunServerID(ctx context.Context, uuid string, runID int64) error {
	return s.exec1(ctx, "UPDATE runs SET run_id = ?, updated_at = ? WHERE client_run_uuid = ?", runID, s.nowMS(), uuid)
}

// UpdateRunStats stores the latest status and stats.
func (s *Store) UpdateRunStats(ctx context.Context, uuid, status string, st RunStats) error {
	stats, err := marshalStats(st)
	if err != nil {
		return err
	}
	return s.exec1(ctx, "UPDATE runs SET status = ?, stats = ?, updated_at = ? WHERE client_run_uuid = ?", status, stats, s.nowMS(), uuid)
}

// SetRunSnapshot records the committed snapshot's server id and root oid
// (the next run's parent_snapshot_id and Root.Parent).
func (s *Store) SetRunSnapshot(ctx context.Context, uuid string, snapshotID int64, oid string) error {
	return s.exec1(ctx, "UPDATE runs SET snapshot_id = ?, snapshot_oid = ?, updated_at = ? WHERE client_run_uuid = ?",
		nullInt(snapshotID), oid, s.nowMS(), uuid)
}

// FinishRun sets the final status, snapshot id and error text. A
// snapshotID of 0 keeps the id recorded by SetRunSnapshot, if any.
func (s *Store) FinishRun(ctx context.Context, uuid, status string, snapshotID int64, errText string) error {
	now := s.nowMS()
	return s.exec1(ctx, "UPDATE runs SET status = ?, snapshot_id = COALESCE(?, snapshot_id), error = ?, finished_at = ?, updated_at = ? WHERE client_run_uuid = ?",
		status, nullInt(snapshotID), errText, now, now, uuid)
}

func (s *Store) exec1(ctx context.Context, q string, args ...any) error {
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- file cache ---

// CachedFile is one file cache row.
type CachedFile struct {
	JobID   int64
	Path    string // concrete Windows path as walked
	Size    int64
	MTime   int64 // unix ns
	CTime   int64 // unix ns (creation time)
	Attrs   uint32
	FileID  string
	H       []byte // keyed whole-file hash
	Chunks  []byte // concatenated 32-byte data blob ids
	Epoch   int64
	SeenRun string
}

// ChunkIDs splits Chunks into 32-byte blob ids.
func (c *CachedFile) ChunkIDs() [][32]byte {
	out := make([][32]byte, 0, len(c.Chunks)/32)
	for i := 0; i+32 <= len(c.Chunks); i += 32 {
		out = append(out, [32]byte(c.Chunks[i:i+32]))
	}
	return out
}

// FileCache loads one entry; ok is false when there is none.
func (s *Store) FileCache(ctx context.Context, jobID int64, path string) (c CachedFile, ok bool, err error) {
	c = CachedFile{JobID: jobID, Path: path}
	var attrs int64
	err = s.DB.QueryRowContext(ctx, `SELECT size, mtime, ctime, attrs, file_id, h, chunks, epoch, seen_run
		FROM file_cache WHERE job_id = ? AND path = ?`, jobID, path).
		Scan(&c.Size, &c.MTime, &c.CTime, &attrs, &c.FileID, &c.H, &c.Chunks, &c.Epoch, &c.SeenRun)
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	c.Attrs = uint32(attrs)
	return c, err == nil, err
}

// PutFileCache inserts or replaces an entry.
func (s *Store) PutFileCache(ctx context.Context, c CachedFile) error {
	_, err := s.DB.ExecContext(ctx, `INSERT OR REPLACE INTO file_cache
		(job_id, path, size, mtime, ctime, attrs, file_id, h, chunks, epoch, seen_run, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.JobID, c.Path, c.Size, c.MTime, c.CTime, int64(c.Attrs), c.FileID, c.H, c.Chunks, c.Epoch, c.SeenRun, s.nowMS())
	return err
}

// MarkFileSeen records that run saw an unchanged cached file.
func (s *Store) MarkFileSeen(ctx context.Context, jobID int64, path, runUUID string) error {
	return s.exec1(ctx, "UPDATE file_cache SET seen_run = ? WHERE job_id = ? AND path = ?", runUUID, jobID, path)
}

// PruneFileCache deletes a job's entries not seen by runUUID (call only
// after a run that walked every root completely). It returns the count.
func (s *Store) PruneFileCache(ctx context.Context, jobID int64, runUUID string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM file_cache WHERE job_id = ? AND seen_run <> ?", jobID, runUUID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DropFileCache deletes a job's whole cache (e.g. after a key epoch change
// or a repo change).
func (s *Store) DropFileCache(ctx context.Context, jobID int64) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM file_cache WHERE job_id = ?", jobID)
	return err
}

// --- packs ---

// Pack is a locally built pack.
type Pack struct {
	OID        string
	RepoID     int64
	Epoch      int64
	Class      int // 0 data, 1 meta
	Size       int64
	RunUUID    string
	Uploaded   bool
	CreatedAt  int64
	UploadedAt int64
}

// AddPack records a new (not yet uploaded) pack; re-adding the same oid is
// a no-op.
func (s *Store) AddPack(ctx context.Context, p Pack) error {
	if p.OID == "" {
		return errors.New("backup: pack without oid")
	}
	if p.CreatedAt == 0 {
		p.CreatedAt = s.nowMS()
	}
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO packs (oid, repo_id, epoch, class, size, run_uuid, uploaded, created_at, uploaded_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, p.OID, p.RepoID, p.Epoch, p.Class, p.Size, p.RunUUID, boolInt(p.Uploaded), p.CreatedAt, nullInt(p.UploadedAt))
	return err
}

// MarkPackUploaded flags a pack as stored on the server.
func (s *Store) MarkPackUploaded(ctx context.Context, oid string) error {
	return s.exec1(ctx, "UPDATE packs SET uploaded = 1, uploaded_at = ? WHERE oid = ?", s.nowMS(), oid)
}

// DeletePack removes a pack's row (ErrNotFound). It retires a pending pack
// that can never be uploaded (spool file missing or corrupt); its blobs are
// packed again by a later run if still needed.
func (s *Store) DeletePack(ctx context.Context, oid string) error {
	return s.exec1(ctx, "DELETE FROM packs WHERE oid = ?", oid)
}

const packCols = "oid, repo_id, epoch, class, size, run_uuid, uploaded, created_at, uploaded_at"

func scanPack(sc interface{ Scan(...any) error }) (Pack, error) {
	var p Pack
	var up int
	var at sql.NullInt64
	err := sc.Scan(&p.OID, &p.RepoID, &p.Epoch, &p.Class, &p.Size, &p.RunUUID, &up, &p.CreatedAt, &at)
	p.Uploaded, p.UploadedAt = up != 0, at.Int64
	return p, err
}

// Pack loads one pack (ErrNotFound).
func (s *Store) Pack(ctx context.Context, oid string) (*Pack, error) {
	p, err := scanPack(s.DB.QueryRowContext(ctx, "SELECT "+packCols+" FROM packs WHERE oid = ?", oid))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// PendingPacks lists a repo's packs not yet uploaded, oldest first.
func (s *Store) PendingPacks(ctx context.Context, repoID int64) ([]Pack, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+packCols+" FROM packs WHERE repo_id = ? AND uploaded = 0 ORDER BY created_at, oid", repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Pack
	for rows.Next() {
		p, err := scanPack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- pending issues ---

// AddIssue records an issue for a run, merging repeats of the same
// (run, path, class) into one row with a higher count.
func (s *Store) AddIssue(ctx context.Context, jobID int64, runUUID string, is Issue) error {
	now := s.nowMS()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO issues (job_id, run_uuid, path, class, severity, detail, retryable, first_seen, last_seen)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT (run_uuid, path, class) DO UPDATE SET count = count + 1, last_seen = excluded.last_seen,
			detail = excluded.detail, severity = excluded.severity, retryable = excluded.retryable, reported_at = NULL`,
		jobID, runUUID, is.Path, is.Class, is.Severity, is.Detail, boolInt(is.Retryable), now, now)
	return err
}

// PendingIssue is an unreported issue row.
type PendingIssue struct {
	ID      int64
	JobID   int64
	RunUUID string
	Issue
	Count     int64
	FirstSeen int64
	LastSeen  int64
}

// PendingIssues returns up to limit unreported issues of a run, in id
// order (limit <= 0 = all).
func (s *Store) PendingIssues(ctx context.Context, runUUID string, limit int) ([]PendingIssue, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, job_id, run_uuid, path, class, severity, detail, retryable, count, first_seen, last_seen
		FROM issues WHERE run_uuid = ? AND reported_at IS NULL ORDER BY id LIMIT ?`, runUUID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingIssue
	for rows.Next() {
		var p PendingIssue
		var retry int
		if err := rows.Scan(&p.ID, &p.JobID, &p.RunUUID, &p.Path, &p.Class, &p.Severity, &p.Detail, &retry, &p.Count, &p.FirstSeen, &p.LastSeen); err != nil {
			return nil, err
		}
		p.Retryable = retry != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkIssuesReported stamps the given issue ids as reported.
func (s *Store) MarkIssuesReported(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.nowMS()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, "UPDATE issues SET reported_at = ? WHERE id = ?", now, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RunStats is the last progress heartbeat of a run.
type RunStats = wire.RunProgress

func marshalStats(st RunStats) (string, error) {
	b, err := json.Marshal(st)
	return string(b), err
}

// Issue is one walker/run skip or warning.
type Issue struct {
	Path      string // concrete path ("" = whole job)
	Class     string // wire.IssueClass* or a local class
	Severity  string // wire.IssueSeverity*
	Detail    string
	Retryable bool
}
