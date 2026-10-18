package dbutil

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func openTemp(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "x", "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenPragmas(t *testing.T) {
	db := openTemp(t)
	var mode string
	var fk, busy int
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys = %d, %v", fk, err)
	}
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil || busy != 10000 {
		t.Fatalf("busy_timeout = %d, %v", busy, err)
	}
}

func TestMigrate(t *testing.T) {
	db := openTemp(t)
	calls := 0
	migs := []Migration{
		{Version: 1, Name: "a", SQL: "CREATE TABLE a (id INTEGER PRIMARY KEY)"},
		{Version: 2, Name: "b", Up: func(tx *sql.Tx) error {
			calls++
			_, err := tx.Exec("CREATE TABLE b (id INTEGER PRIMARY KEY, a_id INTEGER REFERENCES a(id))")
			return err
		}},
	}
	for i := 0; i < 2; i++ {
		v, err := Migrate(db, migs, nil)
		if err != nil || v != 2 {
			t.Fatalf("run %d: v = %d, %v", i, v, err)
		}
	}
	if calls != 1 {
		t.Fatalf("Up ran %d times", calls)
	}
	// foreign keys are enforced
	if _, err := db.Exec("INSERT INTO b(id, a_id) VALUES(1, 99)"); err == nil {
		t.Fatal("foreign key not enforced")
	}
	// appending a migration applies only the new one
	migs = append(migs, Migration{Version: 3, Name: "c", SQL: "CREATE TABLE c (id INTEGER)"})
	if v, err := Migrate(db, migs, nil); err != nil || v != 3 {
		t.Fatalf("v = %d, %v", v, err)
	}
}

func TestMigrateFailureRollsBack(t *testing.T) {
	db := openTemp(t)
	migs := []Migration{
		{Version: 1, Name: "a", SQL: "CREATE TABLE a (id INTEGER)"},
		{Version: 2, Name: "bad", Up: func(tx *sql.Tx) error {
			if _, err := tx.Exec("CREATE TABLE half (id INTEGER)"); err != nil {
				return err
			}
			return errors.New("boom")
		}},
	}
	if _, err := Migrate(db, migs, nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if v, _ := Version(db); v != 1 {
		t.Fatalf("version = %d, want 1", v)
	}
	if ok, _ := TableExists(db, "half"); ok {
		t.Fatal("failed migration not rolled back")
	}
}

func TestMigrateBaseline(t *testing.T) {
	db := openTemp(t)
	if _, err := db.Exec("CREATE TABLE a (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	migs := []Migration{
		{Version: 1, Name: "a", SQL: "CREATE TABLE a (id INTEGER)"}, // would fail if re-run
		{Version: 2, Name: "b", SQL: "CREATE TABLE b (id INTEGER)"},
	}
	base := func(tx *sql.Tx) (int, error) {
		if ok, err := TableExists(tx, "a"); err != nil || !ok {
			return 0, err
		}
		return 1, nil
	}
	if v, err := Migrate(db, migs, base); err != nil || v != 2 {
		t.Fatalf("v = %d, %v", v, err)
	}
	var adopted int
	if err := db.QueryRow("SELECT adopted FROM schema_version WHERE version = 1").Scan(&adopted); err != nil || adopted != 1 {
		t.Fatalf("adopted = %d, %v", adopted, err)
	}
}

func TestMigrateRejectsGaps(t *testing.T) {
	db := openTemp(t)
	if _, err := Migrate(db, []Migration{{Version: 2, Name: "x", SQL: "SELECT 1"}}, nil); err == nil {
		t.Fatal("expected error for non-contiguous versions")
	}
}
