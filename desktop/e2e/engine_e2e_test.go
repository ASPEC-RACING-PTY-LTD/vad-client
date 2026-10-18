package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/automation"
	"github.com/ASPEC-RACING-PTY-LTD/vad-server/testkit"
)

func writeFile(t *testing.T, p string, size int, age time.Duration) []byte {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	b := make([]byte, size)
	rand.Read(b)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Add(-age)
	os.Chtimes(p, mt, mt)
	return b
}

func waitFor(t *testing.T, d time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func newEngine(t *testing.T, srv *testkit.Server) (*engine.Engine, *state.Store) {
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := engine.New(st, log, "TEST-PC")
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	c, me := srv.Device()
	e.SetClient(c, me)
	s := config.DefaultSettings()
	e.ApplySettings(s)
	return e, st
}

func TestEngineEndToEnd(t *testing.T) {
	srv := testkit.Start(t)
	// Medal template as an enabled automation on the server.
	tpl := automation.BuiltinTemplates()[0].Automation
	srv.Do("POST", "/api/v1/automations", map[string]any{"definition": tpl}, 200)

	e, st := newEngine(t, srv)
	e.RefreshAutomations()
	if len(e.Automations()) != 1 {
		t.Fatalf("automation not loaded: %+v", e.Automations())
	}

	root := t.TempDir()
	clip := writeFile(t, filepath.Join(root, "Valorant", "clip1.mp4"), 19<<20, time.Minute)
	other := writeFile(t, filepath.Join(root, "Counter-Strike 2", "ace.mkv"), 2<<20, time.Minute)
	writeFile(t, filepath.Join(root, "Valorant", "tiny.mp4"), 1000, time.Minute)
	writeFile(t, filepath.Join(root, "notes.txt"), 5000, time.Minute)
	writeFile(t, filepath.Join(root, "Valorant", "recording.mp4.part"), 3<<20, time.Minute)
	writeFile(t, filepath.Join(root, "Excluded", "skip.mp4"), 2<<20, time.Minute)

	cfg, err := e.SaveConfig(config.SyncConfig{Name: "Medal Clips", Enabled: true, Kind: "folder", LocalPath: root, Destination: "/Medal", Recursive: true, ExcludeSubfolders: []string{"Excluded"}, StableSeconds: 1, Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	month := time.Now().Format("2006-01")
	waitFor(t, 30*time.Second, "uploads", func() bool {
		_, n, _ := srv.Content("/Medal/Counter-Strike 2/" + month + "/ace.mkv")
		_, n2, _ := srv.Content("/Medal/Valorant/" + month + "/clip1.mp4")
		return n != nil && n2 != nil
	})
	got, node, err := srv.Content("/Medal/Valorant/" + month + "/clip1.mp4")
	if err != nil || !bytes.Equal(got, clip) {
		t.Fatalf("clip content mismatch: %v", err)
	}
	if tags := node["tags"].([]any); len(tags) != 2 {
		t.Errorf("expected automation tags, got %v", tags)
	}
	got, _, _ = srv.Content("/Medal/Counter-Strike 2/" + month + "/ace.mkv")
	if !bytes.Equal(got, other) {
		t.Fatal("second clip mismatch")
	}
	for _, p := range []string{"/Medal/notes.txt", "/Medal/Valorant/tiny.mp4", "/Medal/Unsorted/" + month + "/tiny.mp4", "/Medal/Excluded/skip.mp4"} {
		if _, n, _ := srv.Content(p); n != nil {
			t.Errorf("%s should not have been uploaded", p)
		}
	}
	waitFor(t, 10*time.Second, "idle", func() bool { p, _, _ := st.QueueStats(); return p == 0 })

	// Unchanged files are not transferred again on rescan.
	before := countTransfers(t, srv)
	if _, err := e.Scan(cfg.ID, true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	if after := countTransfers(t, srv); after != before {
		t.Fatalf("rescan re-transferred unchanged files: %d -> %d", before, after)
	}

	// Modified file: picked up by the watcher and uploaded again; the old
	// version lands in trash.
	newContent := writeFile(t, filepath.Join(root, "Valorant", "clip1.mp4"), 3<<20, 0)
	waitFor(t, 30*time.Second, "modified upload", func() bool {
		b, _, _ := srv.Content("/Medal/Valorant/" + month + "/clip1.mp4")
		return bytes.Equal(b, newContent)
	})

	// Local deletion never deletes the server copy.
	os.Remove(filepath.Join(root, "Counter-Strike 2", "ace.mkv"))
	time.Sleep(7 * time.Second)
	if _, n, _ := srv.Content("/Medal/Counter-Strike 2/" + month + "/ace.mkv"); n == nil {
		t.Fatal("server copy deleted after local deletion")
	}
	entry, err := st.File(cfg.ID, "Counter-Strike 2/ace.mkv")
	if err != nil || entry.Status != "deleted" {
		t.Fatalf("local deletion not recorded: %+v %v", entry, err)
	}
}

func countTransfers(t *testing.T, srv *testkit.Server) int {
	out := srv.Do("GET", "/api/v1/transfers?limit=500", nil, 200)
	n := 0
	for _, it := range out["items"].([]any) {
		if it.(map[string]any)["status"] == "completed" {
			n++
		}
	}
	return n
}

func TestEngineRecoversInterruptedUpload(t *testing.T) {
	srv := testkit.Start(t)
	root := t.TempDir()
	data := writeFile(t, filepath.Join(root, "big.bin"), 40<<20, time.Minute)

	// First engine: start uploading, then "crash" after the first chunks.
	stPath := filepath.Join(t.TempDir(), "state.db")
	st, _ := state.Open(stPath)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := engine.New(st, log, "PC")
	e.Start(context.Background())
	c, me := srv.Device()
	e.SetClient(c, me)
	s := config.DefaultSettings()
	s.ConcurrencyMode, s.ManualParallel = "manual", 1
	s.BandwidthKBps = 2 * 1024 // slow it down so we can interrupt mid-way
	e.ApplySettings(s)
	cfg, err := e.SaveConfig(config.SyncConfig{Name: "Backup", Enabled: true, Kind: "folder", LocalPath: root, Destination: "/Backup", Recursive: true, StableSeconds: 1, AutomationMode: "none"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "partial upload", func() bool {
		out := srv.Do("GET", "/api/v1/uploads", nil, 200)
		ups := out["uploads"].([]any)
		return len(ups) == 1 && ups[0].(map[string]any)["bytes_received"].(float64) >= 8<<20
	})
	e.Stop()
	st.Close()
	if b, _, _ := srv.Content("/Backup/big.bin"); b != nil {
		t.Fatal("upload finished before the simulated crash; the test did not exercise resume")
	}

	// Second engine with the same state: resumes the same session.
	st2, err := state.Open(stPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	e2 := engine.New(st2, log, "PC")
	e2.Start(context.Background())
	defer e2.Stop()
	e2.SetClient(c, me)
	e2.ApplySettings(config.DefaultSettings())
	waitFor(t, 60*time.Second, "resumed upload", func() bool {
		b, _, _ := srv.Content("/Backup/big.bin")
		return bytes.Equal(b, data)
	})
	out := srv.Do("GET", "/api/v1/transfers?limit=50", nil, 200)
	completed := 0
	for _, it := range out["items"].([]any) {
		m := it.(map[string]any)
		if m["status"] == "completed" && strings.HasSuffix(m["path"].(string), "big.bin") {
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("expected exactly one completed transfer (resumed), got %d", completed)
	}
	_ = cfg
}
