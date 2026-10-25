package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/filter"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// identityServer answers every upload as finished and records the requests.
type identityServer struct {
	mu  sync.Mutex
	got []client.UploadRequest
}

func (s *identityServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req client.UploadRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	s.mu.Lock()
	s.got = append(s.got, req)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"complete","result":"replaced","node":{}}`))
}

func (s *identityServer) last(t *testing.T) client.UploadRequest {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.got) == 0 {
		t.Fatal("nothing uploaded")
	}
	return s.got[len(s.got)-1]
}

func hexSHA(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// An edited clip is uploaded as an update of its earlier upload (base
// content, same remote path); a rename or move found by a scan, also one
// made while VaultDock was closed, names the old remote path; the creation
// time on the PC goes along. Change detection still uses size and mtime.
func TestUploadIdentity(t *testing.T) {
	srv := &identityServer{}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	db := filepath.Join(t.TempDir(), "state.db")
	e, st := deferralEngine(t, db)
	setTestClient(t, e, hs.URL)
	e.mu.Lock()
	e.features = map[string]bool{wire.FeatureUploadIdentity: true}
	e.mu.Unlock()

	root := t.TempDir()
	c := config.SyncConfig{ID: "c1", Name: "c1", Enabled: true, LocalPath: root, Destination: "/PC/Clips", Recursive: true, OnConflict: "rename"}
	c.Normalize()
	e.mu.Lock()
	e.configs[c.ID] = &c
	e.filters[c.ID] = filter.New(&c)
	e.mu.Unlock()

	dir := filepath.Join(root, "Rocket League", "2026")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(dir, "clip.mp4")
	stamp := time.Now().Add(-2 * time.Hour)
	write := func(p string, data []byte) {
		t.Helper()
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		stamp = stamp.Add(time.Minute) // an edit moves the mtime; old enough to be stable
		if err := os.Chtimes(p, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	syncAll := func() {
		t.Helper()
		if _, err := e.Scan("c1", true); err != nil {
			t.Fatal(err)
		}
		for processNext(t, e, st) {
		}
	}
	const remote = "/PC/Clips/Rocket League/2026/clip.mp4"

	v1 := []byte("first version of the clip")
	write(abs, v1)
	syncAll()
	req := srv.last(t)
	if req.Path != remote || req.BaseSHA256 != "" || req.PreviousPath != "" || req.FileCreatedAt <= 0 {
		t.Fatalf("new clip: %+v", req)
	}
	if req.ModifiedAt != stamp.UnixMilli() {
		t.Fatalf("modified_at %d, want the file's mtime %d", req.ModifiedAt, stamp.UnixMilli())
	}

	// Edited twice: each upload continues the previous one.
	prev := v1
	for i, data := range [][]byte{[]byte("trimmed clip, second version"), []byte("third version, louder")} {
		write(abs, data)
		syncAll()
		req = srv.last(t)
		if req.Path != remote || req.BaseSHA256 != hexSHA(prev) || req.PreviousPath != "" || req.SHA256 != hexSHA(data) {
			t.Fatalf("edit %d: %+v", i, req)
		}
		prev = data
	}
	n := len(srv.got)
	syncAll() // nothing changed: nothing uploaded
	if len(srv.got) != n {
		t.Fatalf("unchanged clip uploaded again: %+v", srv.last(t))
	}

	// Renamed into another nested folder while VaultDock was closed: the
	// next scan (a fresh engine on the same state) syncs it as a move.
	st.Close()
	e2, st2 := deferralEngine(t, db)
	setTestClient(t, e2, hs.URL)
	e2.mu.Lock()
	e2.features = map[string]bool{wire.FeatureUploadIdentity: true}
	e2.configs[c.ID] = &c
	e2.filters[c.ID] = filter.New(&c)
	e2.mu.Unlock()
	e, st = e2, st2
	moved := filepath.Join(root, "Rocket League", "Best", "goal.mp4")
	if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(abs, moved); err != nil {
		t.Fatal(err)
	}
	syncAll()
	req = srv.last(t)
	if req.Path != "/PC/Clips/Rocket League/Best/goal.mp4" || req.PreviousPath != remote || req.BaseSHA256 != hexSHA(prev) {
		t.Fatalf("rename: %+v", req)
	}
	if f, err := st.File("c1", "Rocket League/2026/clip.mp4"); err == nil && f.Status != "deleted" {
		t.Fatalf("old path still indexed: %+v", f)
	}
	if f, err := st.File("c1", "Rocket League/Best/goal.mp4"); err != nil || f.Status != "synced" || f.RemotePath != "/PC/Clips/Rocket League/Best/goal.mp4" {
		t.Fatalf("new path: %+v %v", f, err)
	}

	// A genuinely new clip next to it is a new file.
	write(filepath.Join(root, "Rocket League", "Best", "save.mp4"), []byte("another clip"))
	syncAll()
	if req = srv.last(t); req.Path != "/PC/Clips/Rocket League/Best/save.mp4" || req.BaseSHA256 != "" || req.PreviousPath != "" {
		t.Fatalf("new clip: %+v", req)
	}

	// Servers without the feature get the request as before.
	e.mu.Lock()
	e.features = map[string]bool{}
	e.mu.Unlock()
	write(moved, []byte("fourth version"))
	syncAll()
	if req = srv.last(t); req.BaseSHA256 != "" || req.PreviousPath != "" || req.FileCreatedAt != 0 {
		t.Fatalf("old server got identity fields: %+v", req)
	}
}
