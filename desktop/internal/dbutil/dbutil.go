// Package dbutil opens the client's local SQLite databases (state.db,
// backup.db) with consistent settings and applies ordered schema migrations
// tracked in a schema_version table.
package dbutil

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Open opens (creating if needed) the SQLite database at path with WAL
// journaling, a busy timeout, NORMAL synchronous mode, foreign keys enforced
// and immediate write transactions. The parent directory is created 0700.
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Migration is one schema step. Versions start at 1 and must be contiguous
// and ascending. Exactly one of SQL or Up is used (Up wins when set).
type Migration struct {
	Version int
	Name    string
	SQL     string
	Up      func(tx *sql.Tx) error
}

// Baseline inspects a database that has no recorded schema version and
// returns the version its existing tables already correspond to (0 = none).
// It lets a database created before migrations existed be adopted.
type Baseline func(tx *sql.Tx) (int, error)

const versionTable = `CREATE TABLE IF NOT EXISTS schema_version (
	version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL, adopted INTEGER NOT NULL DEFAULT 0
)`

// Version returns the highest applied schema version (0 when none).
func Version(db *sql.DB) (int, error) {
	if _, err := db.Exec(versionTable); err != nil {
		return 0, err
	}
	return currentVersion(db)
}

type querier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func currentVersion(q querier) (int, error) {
	var v sql.NullInt64
	if err := q.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&v); err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

// Migrate brings db up to the last migration in migs. Each migration runs in
// its own transaction together with its schema_version row, so a failure
// leaves the database at the previous version. Re-running is a no-op. When
// the database has no recorded version, baseline (optional) is consulted
// first and the versions it reports are recorded as adopted. It returns the
// resulting version; a database newer than migs is left untouched.
func Migrate(db *sql.DB, migs []Migration, baseline Baseline) (int, error) {
	for i, m := range migs {
		if m.Version != i+1 {
			return 0, fmt.Errorf("migration %d (%s): versions must be contiguous from 1", m.Version, m.Name)
		}
		if m.SQL == "" && m.Up == nil {
			return 0, fmt.Errorf("migration %d (%s): empty", m.Version, m.Name)
		}
	}
	if _, err := db.Exec(versionTable); err != nil {
		return 0, fmt.Errorf("create schema_version: %w", err)
	}
	if baseline != nil {
		if err := adopt(db, migs, baseline); err != nil {
			return 0, err
		}
	}
	for _, m := range migs {
		if err := apply(db, m); err != nil {
			return 0, fmt.Errorf("migration %d (%s): %w", m.Version, m.Name, err)
		}
	}
	return currentVersion(db)
}

func adopt(db *sql.DB, migs []Migration, baseline Baseline) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cur, err := currentVersion(tx)
	if err != nil {
		return err
	}
	if cur > 0 {
		return nil
	}
	v, err := baseline(tx)
	if err != nil {
		return fmt.Errorf("baseline: %w", err)
	}
	if v <= 0 {
		return nil
	}
	if v > len(migs) {
		return fmt.Errorf("baseline: version %d exceeds known migrations (%d)", v, len(migs))
	}
	t := time.Now().UnixMilli()
	for _, m := range migs[:v] {
		if _, err := tx.Exec("INSERT INTO schema_version(version, name, applied_at, adopted) VALUES(?, ?, ?, 1)", m.Version, m.Name, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// apply runs m if it has not been applied yet. The version check happens
// inside the (immediate) transaction so concurrent openers do not race.
func apply(db *sql.DB, m Migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version = ?", m.Version).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if m.Up != nil {
		err = m.Up(tx)
	} else {
		_, err = tx.Exec(m.SQL)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO schema_version(version, name, applied_at) VALUES(?, ?, ?)", m.Version, m.Name, time.Now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

// TableExists reports whether a table named name exists (for baselines).
func TableExists(q querier, name string) (bool, error) {
	var n int
	err := q.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return n > 0, err
}
