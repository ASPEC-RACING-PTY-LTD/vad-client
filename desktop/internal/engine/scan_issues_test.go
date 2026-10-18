package engine

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/filter"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/state"
)

type issueRow struct {
	class string
	count int
}

func scanIssues(t *testing.T, st *state.Store, cfgID string) map[string]issueRow {
	t.Helper()
	rows, err := st.DB.Query(`SELECT rel_path, class, count, first_seen, last_seen FROM issues WHERE source = 'scan' AND config_id = ?`, cfgID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]issueRow{}
	for rows.Next() {
		var rel string
		var r issueRow
		var first, last int64
		if err := rows.Scan(&rel, &r.class, &r.count, &first, &last); err != nil {
			t.Fatal(err)
		}
		if first <= 0 || last < first {
			t.Errorf("%s: bad times %d %d", rel, first, last)
		}
		if _, dup := out[rel]; dup {
			t.Errorf("%s: more than one issue row", rel)
		}
		out[rel] = r
	}
	return out
}

func addTestConfig(e *Engine, c config.SyncConfig) {
	c.Normalize()
	e.mu.Lock()
	e.configs[c.ID] = &c
	e.filters[c.ID] = filter.New(&c)
	e.mu.Unlock()
}

func TestScanRecordsUnreadableFiles(t *testing.T) {
	e, st := testEngine(t, filepath.Join(t.TempDir(), "state.db"))
	root := t.TempDir()
	for _, n := range []string{"ok.txt", "denied.txt", "locked.txt"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lockErr := error(syscall.Errno(32)) // ERROR_SHARING_VIOLATION
	if runtime.GOOS != "windows" {
		lockErr = nil // the locked class is Windows-only
	}
	old := probeRead
	t.Cleanup(func() { probeRead = old })
	probeRead = func(abs string) error {
		switch filepath.Base(abs) {
		case "denied.txt":
			return &fs.PathError{Op: "open", Path: abs, Err: fs.ErrPermission}
		case "locked.txt":
			if lockErr != nil {
				return &fs.PathError{Op: "open", Path: abs, Err: lockErr}
			}
		}
		return nil
	}
	addTestConfig(e, config.SyncConfig{ID: "c1", Name: "c1", Enabled: true, LocalPath: root, Destination: "/x", Recursive: true})

	for i := 1; i <= 2; i++ {
		if _, err := e.Scan("c1", true); err != nil {
			t.Fatalf("scan %d: %v", i, err)
		}
		got := scanIssues(t, st, "c1")
		if r := got["denied.txt"]; r.class != issuePermissionDenied || r.count != i {
			t.Errorf("scan %d: denied.txt issue = %+v", i, r)
		}
		if _, ok := got["ok.txt"]; ok {
			t.Errorf("scan %d: issue recorded for a readable file", i)
		}
		if lockErr != nil {
			// Queued on the first scan; the transfer defers it from then on.
			if r := got["locked.txt"]; r.class != issueLocked || r.count != 1 {
				t.Errorf("scan %d: locked.txt issue = %+v", i, r)
			}
		}
	}
	// The scan carried on: readable and locked files are queued, the
	// unreadable one is not and is not mistaken for a deletion.
	if st.PendingForConfig("c1") != 2 {
		t.Fatalf("pending = %d, want 2", st.PendingForConfig("c1"))
	}
	if f, err := st.File("c1", "denied.txt"); err == nil && f.Status == "deleted" {
		t.Fatal("unreadable file marked deleted")
	}
}

func TestScanRecordsMissingSelectedFile(t *testing.T) {
	e, st := testEngine(t, filepath.Join(t.TempDir(), "state.db"))
	root := t.TempDir()
	present := filepath.Join(root, "here.txt")
	if err := os.WriteFile(present, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	addTestConfig(e, config.SyncConfig{ID: "c2", Name: "c2", Enabled: true, Kind: "files", LocalPath: root, Destination: "/x",
		Files: []string{present, filepath.Join(root, "gone.txt")}})
	if _, err := e.Scan("c2", true); err != nil {
		t.Fatal(err)
	}
	got := scanIssues(t, st, "c2")
	if r := got["gone.txt"]; r.class != issueNotFound || r.count != 1 {
		t.Fatalf("gone.txt issue = %+v (all: %+v)", r, got)
	}
	if len(got) != 1 {
		t.Fatalf("unexpected issues: %+v", got)
	}
}

func TestScanRecordsUnreadableFolder(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	e, st := testEngine(t, filepath.Join(t.TempDir(), "state.db"))
	root := t.TempDir()
	sub := filepath.Join(root, "secret")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sub, 0o755) })
	addTestConfig(e, config.SyncConfig{ID: "c3", Name: "c3", Enabled: true, LocalPath: root, Destination: "/x", Recursive: true})
	if _, err := e.Scan("c3", true); err != nil {
		t.Fatal(err)
	}
	if r := scanIssues(t, st, "c3")["secret"]; r.class != issuePermissionDenied {
		t.Fatalf("secret issue = %+v", r)
	}
	if st.PendingForConfig("c3") != 1 {
		t.Fatal("scan did not carry on past the unreadable folder")
	}
}

func TestClassifyIssue(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&fs.PathError{Err: fs.ErrPermission}, issuePermissionDenied},
		{&fs.PathError{Err: fs.ErrNotExist}, issueNotFound},
		{&fs.PathError{Err: syscall.Errno(5000)}, issueIOError},
	} {
		if got := classifyIssue(tc.err); got != tc.want {
			t.Errorf("classifyIssue(%v) = %s, want %s", tc.err, got, tc.want)
		}
	}
}
