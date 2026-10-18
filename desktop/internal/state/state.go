// Package state persists sync configurations, the file index, the upload
// queue and transfer history in a local SQLite database so work survives
// crashes and restarts.
package state

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/dbutil"
)

var ErrNotFound = errors.New("not found")

// schemaV1 is the original schema, applied as migration 1 (see migrate.go).
const schemaV1 = `
CREATE TABLE IF NOT EXISTS configs (id TEXT PRIMARY KEY, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS files (
	config_id TEXT NOT NULL, rel_path TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL,
	sha256 TEXT NOT NULL DEFAULT '', remote_path TEXT NOT NULL DEFAULT '', status TEXT NOT NULL,
	reason TEXT NOT NULL DEFAULT '', uploaded_at INTEGER, updated_at INTEGER NOT NULL,
	PRIMARY KEY (config_id, rel_path)
);
CREATE INDEX IF NOT EXISTS files_status ON files(config_id, status);
CREATE TABLE IF NOT EXISTS queue (
	id INTEGER PRIMARY KEY, config_id TEXT NOT NULL, rel_path TEXT NOT NULL, event TEXT NOT NULL,
	size INTEGER NOT NULL, mtime INTEGER NOT NULL, remote_dir TEXT NOT NULL, remote_name TEXT NOT NULL,
	tags TEXT NOT NULL DEFAULT '[]', attempts INTEGER NOT NULL DEFAULT 0, next_at INTEGER NOT NULL,
	status TEXT NOT NULL DEFAULT 'pending', error TEXT NOT NULL DEFAULT '', manual INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
	UNIQUE (config_id, rel_path)
);
CREATE INDEX IF NOT EXISTS queue_ready ON queue(status, next_at);
CREATE TABLE IF NOT EXISTS history (
	id INTEGER PRIMARY KEY, config_id TEXT NOT NULL, rel_path TEXT NOT NULL, remote_path TEXT NOT NULL,
	size INTEGER NOT NULL, status TEXT NOT NULL, result TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
	duration_ms INTEGER NOT NULL DEFAULT 0, finished_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS history_time ON history(finished_at);
CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v TEXT NOT NULL);
`

type Store struct{ DB *sql.DB }

// Open opens the state database at path and migrates it to the current
// schema.
func Open(path string) (*Store, error) {
	db, err := dbutil.Open(path)
	if err != nil {
		return nil, err
	}
	if _, err := dbutil.Migrate(db, migrations, baseline); err != nil {
		db.Close()
		return nil, fmt.Errorf("init state database: %w", err)
	}
	// Crash recovery: anything that was in flight goes back to pending.
	if _, err := db.Exec("UPDATE queue SET status = 'pending', next_at = ? WHERE status = 'active'", now()); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

// SchemaVersion returns the applied schema version.
func (s *Store) SchemaVersion() (int, error) { return dbutil.Version(s.DB) }

func now() int64 { return time.Now().UnixMilli() }

// --- configs ---

func (s *Store) Configs() ([]config.SyncConfig, error) {
	rows, err := s.DB.Query("SELECT data FROM configs")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []config.SyncConfig
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var c config.SyncConfig
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			continue
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) SaveConfig(c config.SyncConfig) error {
	b, _ := json.Marshal(c)
	_, err := s.DB.Exec("INSERT INTO configs(id, data) VALUES(?, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data", c.ID, string(b))
	return err
}

func (s *Store) DeleteConfig(id string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	for _, q := range []string{"DELETE FROM configs WHERE id = ?", "DELETE FROM files WHERE config_id = ?", "DELETE FROM queue WHERE config_id = ?"} {
		if _, err := tx.Exec(q, id); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// --- file index ---

type FileEntry struct {
	ConfigID   string
	RelPath    string
	Size       int64
	MTime      int64 // unix nanoseconds
	SHA256     string
	RemotePath string
	Status     string // synced, skipped, queued, failed, deleted
	Reason     string
	UploadedAt int64
}

func (s *Store) File(configID, rel string) (*FileEntry, error) {
	var f FileEntry
	var up sql.NullInt64
	err := s.DB.QueryRow("SELECT config_id, rel_path, size, mtime, sha256, remote_path, status, reason, uploaded_at FROM files WHERE config_id = ? AND rel_path = ?", configID, rel).
		Scan(&f.ConfigID, &f.RelPath, &f.Size, &f.MTime, &f.SHA256, &f.RemotePath, &f.Status, &f.Reason, &up)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	f.UploadedAt = up.Int64
	return &f, err
}

// Index loads all entries of a configuration keyed by relative path (for
// incremental scans of large directories).
func (s *Store) Index(configID string) (map[string]FileEntry, error) {
	rows, err := s.DB.Query("SELECT rel_path, size, mtime, sha256, remote_path, status, reason FROM files WHERE config_id = ?", configID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]FileEntry{}
	for rows.Next() {
		f := FileEntry{ConfigID: configID}
		if err := rows.Scan(&f.RelPath, &f.Size, &f.MTime, &f.SHA256, &f.RemotePath, &f.Status, &f.Reason); err != nil {
			return nil, err
		}
		out[f.RelPath] = f
	}
	return out, rows.Err()
}

func (s *Store) PutFile(f FileEntry) error {
	var up any
	if f.UploadedAt > 0 {
		up = f.UploadedAt
	}
	_, err := s.DB.Exec(`INSERT INTO files(config_id, rel_path, size, mtime, sha256, remote_path, status, reason, uploaded_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(config_id, rel_path) DO UPDATE SET size = excluded.size, mtime = excluded.mtime, sha256 = CASE WHEN excluded.sha256 != '' THEN excluded.sha256 ELSE files.sha256 END,
		remote_path = CASE WHEN excluded.remote_path != '' THEN excluded.remote_path ELSE files.remote_path END, status = excluded.status, reason = excluded.reason,
		uploaded_at = COALESCE(excluded.uploaded_at, files.uploaded_at), updated_at = excluded.updated_at`,
		f.ConfigID, f.RelPath, f.Size, f.MTime, f.SHA256, f.RemotePath, f.Status, f.Reason, up, now())
	return err
}

func (s *Store) MoveFile(configID, from, to string) error {
	_, err := s.DB.Exec("UPDATE OR REPLACE files SET rel_path = ? WHERE config_id = ? AND rel_path = ?", to, configID, from)
	return err
}

func (s *Store) MarkDeleted(configID, rel string) error {
	_, err := s.DB.Exec("UPDATE files SET status = 'deleted', updated_at = ? WHERE config_id = ? AND rel_path = ?", now(), configID, rel)
	return err
}

// ConfigCounts returns synced and pending counts for a configuration.
func (s *Store) ConfigCounts(configID string) (synced, pending, failed int64) {
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM files WHERE config_id = ? AND status = 'synced'", configID).Scan(&synced)
	_ = s.DB.QueryRow("SELECT COALESCE(SUM(status IN ('pending','active')),0), COALESCE(SUM(status = 'failed'),0) FROM queue WHERE config_id = ?", configID).Scan(&pending, &failed)
	return
}

// ResetIndex clears the index and queue (used when the server changes).
func (s *Store) ResetIndex() error {
	_, err := s.DB.Exec("DELETE FROM files; DELETE FROM queue;")
	return err
}

// ResetSyncIn forgets what was uploaded for the configurations in only (nil
// = all): the file index, the queue and the transfer history. The next scan
// uploads everything again. It returns how many indexed files were dropped.
func (s *Store) ResetSyncIn(only []string) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int64
	for _, table := range []string{"files", "queue", "history"} {
		q, args := inConfigs("DELETE FROM "+table+" WHERE 1 = 1", nil, only)
		res, err := tx.Exec(q, args...)
		if err != nil {
			return 0, err
		}
		if table == "files" {
			n, _ = res.RowsAffected()
		}
	}
	return n, tx.Commit()
}

// --- queue ---

type Item struct {
	ID         int64    `json:"id"`
	ConfigID   string   `json:"config_id"`
	RelPath    string   `json:"rel_path"`
	Event      string   `json:"event"`
	Size       int64    `json:"size"`
	MTime      int64    `json:"mtime"`
	RemoteDir  string   `json:"remote_dir"`
	RemoteName string   `json:"remote_name"`
	Tags       []string `json:"tags"`
	Attempts   int      `json:"attempts"`
	NextAt     int64    `json:"next_at"`
	Status     string   `json:"status"`
	Error      string   `json:"error"`
	Manual     bool     `json:"manual"`
	CreatedAt  int64    `json:"created_at"`
}

// RemotePath joins the destination.
func (i *Item) RemotePath() string {
	if i.RemoteDir == "/" || i.RemoteDir == "" {
		return "/" + i.RemoteName
	}
	return strings.TrimRight(i.RemoteDir, "/") + "/" + i.RemoteName
}

const itemCols = "id, config_id, rel_path, event, size, mtime, remote_dir, remote_name, tags, attempts, next_at, status, error, manual, created_at"

func scanItem(sc interface{ Scan(...any) error }) (*Item, error) {
	var it Item
	var tags string
	if err := sc.Scan(&it.ID, &it.ConfigID, &it.RelPath, &it.Event, &it.Size, &it.MTime, &it.RemoteDir, &it.RemoteName, &tags, &it.Attempts, &it.NextAt, &it.Status, &it.Error, &it.Manual, &it.CreatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(tags), &it.Tags)
	return &it, nil
}

// Enqueue inserts or refreshes a queue item (one per file).
func (s *Store) Enqueue(it Item) error {
	tags, _ := json.Marshal(it.Tags)
	t := now()
	if it.NextAt == 0 {
		it.NextAt = t
	}
	_, err := s.DB.Exec(`INSERT INTO queue(config_id, rel_path, event, size, mtime, remote_dir, remote_name, tags, attempts, next_at, status, error, manual, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,0,?,'pending','',?,?,?)
		ON CONFLICT(config_id, rel_path) DO UPDATE SET event = excluded.event, size = excluded.size, mtime = excluded.mtime, remote_dir = excluded.remote_dir,
		remote_name = excluded.remote_name, tags = excluded.tags, next_at = excluded.next_at, attempts = CASE WHEN queue.status = 'failed' THEN 0 ELSE queue.attempts END,
		status = CASE WHEN queue.status = 'active' THEN 'active' ELSE 'pending' END, error = '', updated_at = excluded.updated_at`,
		it.ConfigID, it.RelPath, it.Event, it.Size, it.MTime, it.RemoteDir, it.RemoteName, string(tags), it.NextAt, it.Manual, t, t)
	return err
}

// ClaimReady atomically marks up to n ready items active and returns them.
func (s *Store) ClaimReady(n int, exclude map[string]bool) ([]Item, error) {
	if n <= 0 {
		return nil, nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT "+itemCols+" FROM queue WHERE status = 'pending' AND next_at <= ? ORDER BY next_at, id LIMIT ?", now(), n+len(exclude))
	if err != nil {
		return nil, err
	}
	var items []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if exclude[it.ConfigID] {
			continue
		}
		items = append(items, *it)
		if len(items) == n {
			break
		}
	}
	rows.Close()
	for _, it := range items {
		if _, err := tx.Exec("UPDATE queue SET status = 'active', updated_at = ? WHERE id = ?", now(), it.ID); err != nil {
			return nil, err
		}
	}
	return items, tx.Commit()
}

// NextWake returns when the next pending item becomes ready (0 if none).
func (s *Store) NextWake() int64 {
	var t sql.NullInt64
	_ = s.DB.QueryRow("SELECT MIN(next_at) FROM queue WHERE status = 'pending'").Scan(&t)
	return t.Int64
}

func (s *Store) Done(id int64) error {
	_, err := s.DB.Exec("DELETE FROM queue WHERE id = ?", id)
	return err
}

// Retry schedules an item again after a backoff.
func (s *Store) Retry(id int64, nextAt int64, errMsg string) error {
	_, err := s.DB.Exec("UPDATE queue SET status = 'pending', attempts = attempts + 1, next_at = ?, error = ?, updated_at = ? WHERE id = ?", nextAt, errMsg, now(), id)
	return err
}

// Defer reschedules without counting an attempt (e.g. waiting for a file to
// stop changing).
func (s *Store) Defer(id int64, nextAt int64, note string) error {
	_, err := s.DB.Exec("UPDATE queue SET status = 'pending', next_at = ?, error = ?, updated_at = ? WHERE id = ?", nextAt, note, now(), id)
	return err
}

func (s *Store) Fail(id int64, errMsg string) error {
	_, err := s.DB.Exec("UPDATE queue SET status = 'failed', attempts = attempts + 1, error = ?, updated_at = ? WHERE id = ?", errMsg, now(), id)
	return err
}

// RetryFailed moves failed items back to pending (all when id is 0).
func (s *Store) RetryFailed(id int64) (int64, error) { return s.RetryFailedIn(id, nil) }

// RetryFailedIn is RetryFailed limited to the configurations in only (nil
// means all; an empty non-nil slice matches nothing).
func (s *Store) RetryFailedIn(id int64, only []string) (int64, error) {
	q := "UPDATE queue SET status = 'pending', attempts = 0, next_at = ?, error = '' WHERE status = 'failed'"
	args := []any{now()}
	if id > 0 {
		q += " AND id = ?"
		args = append(args, id)
	}
	q, args = inConfigs(q, args, only)
	res, err := s.DB.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ReleaseActive returns active items to pending (on pause/stop).
func (s *Store) ReleaseActive() {
	_, _ = s.DB.Exec("UPDATE queue SET status = 'pending' WHERE status = 'active'")
}

func (s *Store) RemoveQueued(configID, rel string) {
	_, _ = s.DB.Exec("DELETE FROM queue WHERE config_id = ? AND rel_path = ? AND status != 'active'", configID, rel)
}

// Items lists queue items by status ("" = all).
func (s *Store) Items(status string, limit int) ([]Item, error) { return s.ItemsIn(status, nil, limit) }

// ItemsIn is Items limited to the configurations in only (see RetryFailedIn).
func (s *Store) ItemsIn(status string, only []string, limit int) ([]Item, error) {
	q := "SELECT " + itemCols + " FROM queue WHERE 1 = 1"
	var args []any
	if status != "" {
		q += " AND status = ?"
		args = append(args, status)
	}
	q, args = inConfigs(q, args, only)
	rows, err := s.DB.Query(q+" ORDER BY status = 'active' DESC, next_at, id LIMIT ?", append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *it)
	}
	return out, rows.Err()
}

// QueueStats returns counts and pending bytes.
func (s *Store) QueueStats() (pending, failed int64, pendingBytes int64) {
	_ = s.DB.QueryRow("SELECT COALESCE(SUM(status IN ('pending','active')),0), COALESCE(SUM(status = 'failed'),0), COALESCE(SUM(CASE WHEN status IN ('pending','active') THEN size ELSE 0 END),0) FROM queue").Scan(&pending, &failed, &pendingBytes)
	return
}

// PendingForConfig counts unfinished items of a configuration.
func (s *Store) PendingForConfig(configID string) int64 {
	var n int64
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM queue WHERE config_id = ? AND status IN ('pending','active')", configID).Scan(&n)
	return n
}

// --- history ---

type HistoryEntry struct {
	ID         int64  `json:"id"`
	ConfigID   string `json:"config_id"`
	RelPath    string `json:"rel_path"`
	RemotePath string `json:"remote_path"`
	Size       int64  `json:"size"`
	Status     string `json:"status"`
	Result     string `json:"result"`
	Error      string `json:"error"`
	DurationMS int64  `json:"duration_ms"`
	FinishedAt int64  `json:"finished_at"`
}

func (s *Store) AddHistory(h HistoryEntry) {
	_, _ = s.DB.Exec("INSERT INTO history(config_id, rel_path, remote_path, size, status, result, error, duration_ms, finished_at) VALUES(?,?,?,?,?,?,?,?,?)",
		h.ConfigID, h.RelPath, h.RemotePath, h.Size, h.Status, h.Result, h.Error, h.DurationMS, now())
	// keep the table bounded
	_, _ = s.DB.Exec("DELETE FROM history WHERE id <= (SELECT id FROM history ORDER BY id DESC LIMIT 1 OFFSET 5000)")
}

func (s *Store) History(status string, limit int) ([]HistoryEntry, error) {
	return s.HistoryIn(status, nil, limit)
}

// HistoryIn is History limited to the configurations in only (see
// RetryFailedIn).
func (s *Store) HistoryIn(status string, only []string, limit int) ([]HistoryEntry, error) {
	q := "SELECT id, config_id, rel_path, remote_path, size, status, result, error, duration_ms, finished_at FROM history WHERE 1 = 1"
	var args []any
	if status != "" {
		q += " AND status = ?"
		args = append(args, status)
	}
	q, args = inConfigs(q, args, only)
	rows, err := s.DB.Query(q+" ORDER BY id DESC LIMIT ?", append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		var h HistoryEntry
		if err := rows.Scan(&h.ID, &h.ConfigID, &h.RelPath, &h.RemotePath, &h.Size, &h.Status, &h.Result, &h.Error, &h.DurationMS, &h.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// --- kv ---

func (s *Store) Get(k string) string {
	var v string
	_ = s.DB.QueryRow("SELECT v FROM kv WHERE k = ?", k).Scan(&v)
	return v
}

func (s *Store) Set(k, v string) {
	_, _ = s.DB.Exec("INSERT INTO kv(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v", k, v)
}

// --- schedule_last ---

// ScheduleLast returns the persisted last fire time (unix ms) of a schedule
// trigger keyed "<automation id>/<node id>"; ok is false when never fired.
func (s *Store) ScheduleLast(key string) (last int64, ok bool, err error) {
	err = s.DB.QueryRow("SELECT last_fire FROM schedule_last WHERE key = ?", key).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return last, err == nil, err
}

func (s *Store) SetScheduleLast(key string, last int64) error {
	_, err := s.DB.Exec("INSERT INTO schedule_last(key, last_fire) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET last_fire = excluded.last_fire", key, last)
	return err
}

// --- sync_base ---

// SyncBase is the last agreed state of a path in a sync folder (three-way
// merge base). MTime is unix nanoseconds.
type SyncBase struct {
	FolderID string
	RelPath  string
	NodeID   string
	Revision int64
	SHA256   string
	Size     int64
	MTime    int64
}

func (s *Store) SyncBase(folderID, rel string) (*SyncBase, error) {
	b := SyncBase{FolderID: folderID, RelPath: rel}
	err := s.DB.QueryRow("SELECT node_id, revision, sha256, size, mtime FROM sync_base WHERE folder_id = ? AND rel_path = ?", folderID, rel).
		Scan(&b.NodeID, &b.Revision, &b.SHA256, &b.Size, &b.MTime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *Store) PutSyncBase(b SyncBase) error {
	_, err := s.DB.Exec(`INSERT INTO sync_base(folder_id, rel_path, node_id, revision, sha256, size, mtime, updated_at) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(folder_id, rel_path) DO UPDATE SET node_id = excluded.node_id, revision = excluded.revision, sha256 = excluded.sha256,
		size = excluded.size, mtime = excluded.mtime, updated_at = excluded.updated_at`,
		b.FolderID, b.RelPath, b.NodeID, b.Revision, b.SHA256, b.Size, b.MTime, now())
	return err
}

func (s *Store) DeleteSyncBase(folderID, rel string) error {
	_, err := s.DB.Exec("DELETE FROM sync_base WHERE folder_id = ? AND rel_path = ?", folderID, rel)
	return err
}

// inConfigs appends "AND config_id IN (...)" for only; nil leaves q
// unchanged and an empty slice matches nothing.
func inConfigs(q string, args []any, only []string) (string, []any) {
	if only == nil {
		return q, args
	}
	if len(only) == 0 {
		return q + " AND 0 = 1", args
	}
	q += " AND config_id IN (?" + strings.Repeat(", ?", len(only)-1) + ")"
	for _, id := range only {
		args = append(args, id)
	}
	return q, args
}
