package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/filter"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
)

func deferralEngine(t *testing.T, dbPath string) (*Engine, *state.Store) {
	t.Helper()
	st, err := state.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := New(st, slog.New(slog.NewTextHandler(io.Discard, nil)), "test")
	e.ctx, e.cancel = context.WithCancel(context.Background())
	t.Cleanup(e.cancel)
	return e, st
}

func setTestClient(t *testing.T, e *Engine, base string) {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.client = client.New(u, client.Options{})
	e.mu.Unlock()
}

// lockedFixture creates a config with one old file queued for upload.
func lockedFixture(t *testing.T, e *Engine, st *state.Store) (abs string, fi os.FileInfo) {
	t.Helper()
	root := t.TempDir()
	abs = filepath.Join(root, "mail.pst")
	if err := os.WriteFile(abs, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(abs, old, old); err != nil {
		t.Fatal(err)
	}
	c := config.SyncConfig{ID: "c1", Name: "c1", Enabled: true, LocalPath: root, Destination: "/x", Recursive: true}
	c.Normalize()
	e.mu.Lock()
	e.configs[c.ID] = &c
	e.filters[c.ID] = filter.New(&c)
	e.mu.Unlock()
	enqueueFile(t, st, abs)
	fi, _ = os.Stat(abs)
	return abs, fi
}

func enqueueFile(t *testing.T, st *state.Store, abs string) {
	t.Helper()
	fi, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Enqueue(state.Item{ConfigID: "c1", RelPath: "mail.pst", Event: "file.created", Size: fi.Size(), MTime: fi.ModTime().UnixNano(), RemoteDir: "/x", RemoteName: "mail.pst"}); err != nil {
		t.Fatal(err)
	}
}

// processNext claims the ready queue item and processes it; false when none.
func processNext(t *testing.T, e *Engine, st *state.Store) bool {
	t.Helper()
	items, err := st.ClaimReady(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		return false
	}
	e.process(&activeTransfer{item: items[0], size: items[0].Size, started: time.Now(), lastT: time.Now()})
	return true
}

func stubOpen(t *testing.T, err error) {
	t.Helper()
	old, oldDelay, oldMax, oldSpan := openForUpload, openRetryDelay, maxOpenDeferrals, maxOpenDeferSpan
	t.Cleanup(func() {
		openForUpload, openRetryDelay, maxOpenDeferrals, maxOpenDeferSpan = old, oldDelay, oldMax, oldSpan
	})
	openRetryDelay = 0
	if err != nil {
		openForUpload = func(name string) (*os.File, error) { return nil, &fs.PathError{Op: "open", Path: name, Err: err} }
	}
}

func transferIssues(t *testing.T, st *state.Store) map[string]string {
	t.Helper()
	rows, err := st.DB.Query(`SELECT rel_path, class FROM issues WHERE source = ?`, issueSourceTransfer)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var rel, class string
		if err := rows.Scan(&rel, &class); err != nil {
			t.Fatal(err)
		}
		out[rel] = class
	}
	return out
}

func TestLockedFileDeferralIsBounded(t *testing.T) {
	e, st := deferralEngine(t, filepath.Join(t.TempDir(), "state.db"))
	setTestClient(t, e, "http://127.0.0.1:1")
	lockErr := syscall.Errno(32) // ERROR_SHARING_VIOLATION
	stubOpen(t, lockErr)
	maxOpenDeferrals = 3
	_, fi := lockedFixture(t, e, st)
	wantClass := classifyIssue(&fs.PathError{Err: lockErr})

	for i := 1; i < maxOpenDeferrals; i++ {
		if !processNext(t, e, st) {
			t.Fatalf("deferral %d: item not ready", i)
		}
		items, _ := st.Items("pending", 10)
		if len(items) != 1 || items[0].Attempts != 0 {
			t.Fatalf("deferral %d: queue %+v", i, items)
		}
		var s, m, first int64
		var c int
		got := st.Get(openDeferKey("c1", "mail.pst"))
		if _, err := fmt.Sscanf(got, "%d %d %d %d", &s, &m, &c, &first); err != nil || s != fi.Size() || m != fi.ModTime().UnixNano() || c != i || first <= 0 {
			t.Fatalf("deferral %d: kv %q", i, got)
		}
		if len(transferIssues(t, st)) != 0 {
			t.Fatalf("deferral %d: issue recorded early", i)
		}
	}
	if !processNext(t, e, st) {
		t.Fatal("final attempt: item not ready")
	}
	if processNext(t, e, st) {
		t.Fatal("item still queued after the deferral bound")
	}
	if items, _ := st.Items("", 10); len(items) != 0 {
		t.Fatalf("queue not empty: %+v", items)
	}
	f, err := st.File("c1", "mail.pst")
	if err != nil || f.Status != "skipped" || f.Size != fi.Size() || f.MTime != fi.ModTime().UnixNano() {
		t.Fatalf("file entry %+v, %v", f, err)
	}
	if got := transferIssues(t, st); got["mail.pst"] != wantClass {
		t.Fatalf("issues %v; want class %q", got, wantClass)
	}
	if st.Get(openDeferKey("c1", "mail.pst")) != "" {
		t.Fatal("deferral counter not cleared")
	}
	e.mu.Lock()
	reports := e.reports
	e.mu.Unlock()
	if len(reports) != 1 || reports[0].Status != "skipped" || reports[0].Path != "/x/mail.pst" {
		t.Fatalf("reports %+v", reports)
	}
}

func TestLockedFileDeferralResetsWhenFileChanges(t *testing.T) {
	e, st := deferralEngine(t, filepath.Join(t.TempDir(), "state.db"))
	setTestClient(t, e, "http://127.0.0.1:1")
	stubOpen(t, syscall.Errno(32))
	maxOpenDeferrals = 3
	abs, _ := lockedFixture(t, e, st)
	processNext(t, e, st)
	processNext(t, e, st)

	// A new version of the file starts a fresh count.
	newer := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(abs, newer, newer); err != nil {
		t.Fatal(err)
	}
	enqueueFile(t, st, abs)
	processNext(t, e, st)
	var s, m, f int64
	var c int
	if _, err := fmt.Sscanf(st.Get(openDeferKey("c1", "mail.pst")), "%d %d %d %d", &s, &m, &c, &f); err != nil || c != 1 || m != newer.UnixNano() {
		t.Fatalf("count %d mtime %d, %v", c, m, err)
	}
	if len(transferIssues(t, st)) != 0 {
		t.Fatal("issue recorded")
	}
}

func TestLockedFileDeferralTimeBound(t *testing.T) {
	e, st := deferralEngine(t, filepath.Join(t.TempDir(), "state.db"))
	setTestClient(t, e, "http://127.0.0.1:1")
	stubOpen(t, syscall.Errno(32))
	_, fi := lockedFixture(t, e, st)
	// First failure an hour ago, only one attempt so far.
	st.Set(openDeferKey("c1", "mail.pst"), fmt.Sprintf("%d %d 1 %d", fi.Size(), fi.ModTime().UnixNano(), time.Now().Add(-time.Hour).UnixMilli()))
	processNext(t, e, st)
	if f, _ := st.File("c1", "mail.pst"); f == nil || f.Status != "skipped" {
		t.Fatalf("file entry %+v", f)
	}
	if len(transferIssues(t, st)) != 1 {
		t.Fatal("no issue recorded")
	}
}

func TestOpenSuccessClearsDeferral(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"bad_request","message":"no"}}`))
	}))
	defer srv.Close()
	e, st := deferralEngine(t, filepath.Join(t.TempDir(), "state.db"))
	setTestClient(t, e, srv.URL)
	stubOpen(t, nil)
	_, fi := lockedFixture(t, e, st)
	st.Set(openDeferKey("c1", "mail.pst"), fmt.Sprintf("%d %d 2 %d", fi.Size(), fi.ModTime().UnixNano(), time.Now().UnixMilli()))
	processNext(t, e, st)
	if got := st.Get(openDeferKey("c1", "mail.pst")); got != "" {
		t.Fatalf("deferral counter kept: %q", got)
	}
}

// reportServer records transfer reports; status is the response code.
type reportServer struct {
	mu     sync.Mutex
	status int
	got    []client.TransferReport
}

func (rs *reportServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if rs.status != http.StatusOK {
		w.WriteHeader(rs.status)
		_, _ = w.Write([]byte(`{"error":{"code":"x","message":"x"}}`))
		return
	}
	var body struct {
		Transfers []client.TransferReport `json:"transfers"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	rs.got = append(rs.got, body.Transfers...)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func outboxLen(t *testing.T, e *Engine) int {
	t.Helper()
	n, err := e.reportOutbox().Len()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTransferReportsSurviveRestart(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	e1, _ := deferralEngine(t, db)
	// Offline: the reports go to the durable outbox.
	e1.mu.Lock()
	for i := range 250 {
		e1.reports = append(e1.reports, client.TransferReport{Path: fmt.Sprintf("/x/%d", i), Status: "failed", Error: "boom"})
	}
	e1.mu.Unlock()
	e1.flushReports()
	if n := outboxLen(t, e1); n != 2 { // 200 + 50
		t.Fatalf("outbox has %d items", n)
	}
	e1.st.Close()

	// "Restart": a new engine on the same database delivers them.
	rs := &reportServer{status: http.StatusServiceUnavailable}
	srv := httptest.NewServer(rs)
	defer srv.Close()
	e2, _ := deferralEngine(t, db)
	setTestClient(t, e2, srv.URL)
	e2.flushReports()
	if n := outboxLen(t, e2); n != 2 {
		t.Fatalf("after failed delivery outbox has %d items", n)
	}
	rs.mu.Lock()
	rs.status = http.StatusOK
	rs.mu.Unlock()
	// The failed item waits for its backoff; make everything ready now.
	if _, err := e2.st.DB.Exec("UPDATE outbox SET next_at = 0"); err != nil {
		t.Fatal(err)
	}
	e2.flushReports()
	if n := outboxLen(t, e2); n != 0 {
		t.Fatalf("outbox still has %d items", n)
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if len(rs.got) != 250 || rs.got[0].Path != "/x/0" || rs.got[249].Path != "/x/249" {
		t.Fatalf("server got %d reports", len(rs.got))
	}
}

func TestRejectedReportIsDroppedWithIssue(t *testing.T) {
	rs := &reportServer{status: http.StatusUnprocessableEntity}
	srv := httptest.NewServer(rs)
	defer srv.Close()
	e, st := deferralEngine(t, filepath.Join(t.TempDir(), "state.db"))
	setTestClient(t, e, srv.URL)
	e.mu.Lock()
	e.reports = append(e.reports, client.TransferReport{Path: "/x/a", Status: "failed"})
	e.mu.Unlock()
	e.flushReports()
	if n := outboxLen(t, e); n != 0 {
		t.Fatalf("outbox has %d items", n)
	}
	var n int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM issues WHERE source = 'outbox' AND class = 'report_dropped'`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d drop issues", n)
	}
}
