package state

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/dbutil"
)

var newTables = []string{"issues", "outbox", "schedule_last", "install_mode", "modules", "profiles", "sync_base"}

func checkSchema(t *testing.T, s *Store) {
	t.Helper()
	v, err := s.SchemaVersion()
	if err != nil || v != len(migrations) {
		t.Fatalf("version = %d, %v; want %d", v, err, len(migrations))
	}
	for _, tb := range append(append([]string{}, legacyTables...), newTables...) {
		if ok, err := dbutil.TableExists(s.DB, tb); err != nil || !ok {
			t.Fatalf("table %s missing (%v)", tb, err)
		}
	}
}

func TestFreshDB(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sub", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkSchema(t, s)
	var adopted int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM schema_version WHERE adopted = 1").Scan(&adopted); err != nil || adopted != 0 {
		t.Fatalf("adopted rows = %d, %v", adopted, err)
	}
	var fk int
	if err := s.DB.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys = %d, %v", fk, err)
	}
}

func TestAdoptLegacyDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	// A v0 database exactly as the pre-migration client created it.
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(schemaV1); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO configs(id, data) VALUES('c1', '{"id":"c1"}');
		INSERT INTO kv(k, v) VALUES('device', 'abc');
		INSERT INTO queue(config_id, rel_path, event, size, mtime, remote_dir, remote_name, next_at, status, created_at, updated_at)
		VALUES('c1', 'a.txt', 'create', 1, 1, '/', 'a.txt', 0, 'active', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkSchema(t, s)
	var adopted int
	if err := s.DB.QueryRow("SELECT adopted FROM schema_version WHERE version = 1").Scan(&adopted); err != nil || adopted != 1 {
		t.Fatalf("v1 adopted = %d, %v", adopted, err)
	}
	if s.Get("device") != "abc" {
		t.Fatal("kv data lost")
	}
	cs, err := s.Configs()
	if err != nil || len(cs) != 1 || cs[0].ID != "c1" {
		t.Fatalf("configs = %+v, %v", cs, err)
	}
	items, err := s.Items("pending", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("crash recovery: items = %+v, %v", items, err)
	}
}

func TestPartialLegacyDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("CREATE TABLE kv (k TEXT PRIMARY KEY, v TEXT NOT NULL); INSERT INTO kv VALUES('x', 'y')"); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkSchema(t, s)
	if s.Get("x") != "y" {
		t.Fatal("kv data lost")
	}
}

func TestIdempotentReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	for i := 0; i < 3; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		checkSchema(t, s)
		var n int
		if err := s.DB.QueryRow("SELECT COUNT(*) FROM schema_version").Scan(&n); err != nil || n != len(migrations) {
			t.Fatalf("schema_version rows = %d, %v", n, err)
		}
		if _, err := dbutil.Migrate(s.DB, migrations, baseline); err != nil {
			t.Fatalf("re-migrate: %v", err)
		}
		s.Close()
	}
}

func TestAccessors(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, ok, err := s.ScheduleLast("a/n"); ok || err != nil {
		t.Fatalf("unexpected last fire: %v %v", ok, err)
	}
	if err := s.SetScheduleLast("a/n", 100); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScheduleLast("a/n", 200); err != nil {
		t.Fatal(err)
	}
	if last, ok, err := s.ScheduleLast("a/n"); !ok || err != nil || last != 200 {
		t.Fatalf("last = %d %v %v", last, ok, err)
	}

	if _, err := s.SyncBase("f", "a.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	b := SyncBase{FolderID: "f", RelPath: "a.txt", NodeID: "n1", Revision: 3, SHA256: "ab", Size: 5, MTime: 7}
	if err := s.PutSyncBase(b); err != nil {
		t.Fatal(err)
	}
	b.Revision = 4
	if err := s.PutSyncBase(b); err != nil {
		t.Fatal(err)
	}
	got, err := s.SyncBase("f", "a.txt")
	if err != nil || *got != b {
		t.Fatalf("got %+v, %v", got, err)
	}
	if err := s.DeleteSyncBase("f", "a.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SyncBase("f", "a.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v", err)
	}
}
