package e2e

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/vad-server/testkit"
)

// localAlias is the server's URL under another host name, standing in for
// its LAN address.
func localAlias(t *testing.T, srv *testkit.Server) *url.URL {
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = "localhost:" + u.Port()
	return u
}

// The direct address is verified against the server itself, a different
// server is refused, and an administrator's reset raises the generation.
func TestDirectProbe(t *testing.T) {
	srv := testkit.Start(t)
	c, me := srv.Device()
	if !me.HasFeature("upload.direct") {
		t.Fatal("server does not advertise upload.direct")
	}
	ctx := context.Background()
	probe, err := c.VerifyDirect(ctx, localAlias(t, srv))
	if err != nil || probe.ResetGen != 0 {
		t.Fatalf("verify own address: %+v %v", probe, err)
	}

	other := testkit.Start(t)
	if _, err := c.VerifyDirect(ctx, localAlias(t, other)); err == nil || !strings.Contains(err.Error(), "not this VaultDock server") {
		t.Fatalf("a different server was accepted: %v", err)
	}
	dead, _ := url.Parse("http://127.0.0.1:" + freePort(t))
	if p, err := c.VerifyDirect(ctx, dead); err == nil || p == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("unreachable address: %+v %v", p, err)
	}

	srv.Do("POST", "/api/v1/admin/devices/"+itoa(c.DeviceID)+"/direct-reset", nil, 200)
	if gen, err := c.DirectResetGen(ctx); err != nil || gen != 1 {
		t.Fatalf("reset generation = %d %v, want 1", gen, err)
	}
}

// Uploads over the direct route arrive, compressed chunks are stored as
// they are, and a direct address that dies falls back to the public URL.
func TestDirectUploadsAndFallback(t *testing.T) {
	srv := testkit.Start(t)
	set := srv.Do("GET", "/api/v1/admin/settings", nil, 200)
	settings := set["settings"].(map[string]any)
	settings["storage_compression"] = true
	srv.Do("PUT", "/api/v1/admin/settings", settings, 200)

	e, st, c := newEngineClient(t, srv, config.DefaultSettings())
	c.SetDirect(localAlias(t, srv))

	root := t.TempDir()
	text := []byte(strings.Repeat("compressible sync data ", 1<<20)) // ~23 MiB
	writeFileBytes(t, filepath.Join(root, "notes.log"), text)
	if _, err := e.SaveConfig(config.SyncConfig{Name: "Logs", Enabled: true, Kind: "folder", LocalPath: root, Destination: "/Logs", Recursive: true, StableSeconds: 1, Mode: "auto", Compress: true}); err != nil {
		t.Fatal(err)
	}
	// Wait by path only: a download holds the blob open, which on Windows
	// keeps the raw file from being replaced by its compressed form.
	waitFor(t, 60*time.Second, "direct upload", func() bool { return exists(srv, "/Logs/notes.log") })
	// The stored blob ends up compressed (from the client's frames).
	waitFor(t, 30*time.Second, "compressed blob", func() bool { return countSuffix(t, os.Getenv("VAULTDOCK_DEFAULT_STORAGE"), ".zst") == 1 })
	if got, _, _ := srv.Content("/Logs/notes.log"); !bytes.Equal(got, text) {
		t.Fatal("content differs")
	}

	// The local address goes away: uploads continue over the public URL.
	dead, _ := url.Parse("http://127.0.0.1:" + freePort(t))
	c.SetDirect(dead)
	more := []byte(strings.Repeat("after the address died ", 4096))
	writeFileBytes(t, filepath.Join(root, "later.log"), more)
	waitFor(t, 60*time.Second, "fallback upload", func() bool { _, n, _ := srv.Content("/Logs/later.log"); return n != nil })
	if c.Direct() != nil {
		t.Fatal("dead direct route was not dropped")
	}
	waitFor(t, 10*time.Second, "idle", func() bool { p, f, _ := st.QueueStats(); return p == 0 && f == 0 })
}

// A pause, a route change and a resume start the interrupted upload over:
// its old session is wiped on the server.
func TestRouteChangeWhilePausedRestartsUploads(t *testing.T) {
	srv := testkit.Start(t)
	s := config.DefaultSettings()
	s.BandwidthKBps = 256 // slow, so the upload is still running when paused
	e, st, c := newEngineClient(t, srv, s)

	root := t.TempDir()
	big := make([]byte, 24<<20)
	writeFileBytes(t, filepath.Join(root, "big.bin"), big)
	if _, err := e.SaveConfig(config.SyncConfig{Name: "Big", Enabled: true, Kind: "folder", LocalPath: root, Destination: "/Big", Recursive: true, StableSeconds: 1, Mode: "auto"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "upload started", func() bool { return len(openUploads(t, srv)) == 1 })
	old := openUploads(t, srv)[0]
	e.SetPaused(true)

	c.SetDirect(localAlias(t, srv)) // the route changes while paused
	s.BandwidthKBps, s.Paused = 0, true
	e.ApplySettings(s)
	e.SetPaused(false)
	waitFor(t, 60*time.Second, "upload after resume", func() bool { _, n, _ := srv.Content("/Big/big.bin"); return n != nil })
	for _, id := range openUploads(t, srv) {
		if id == old {
			t.Fatal("interrupted session was not wiped")
		}
	}
	if _, err := srv.Store.UploadAny(context.Background(), old); err == nil {
		t.Fatal("interrupted session still exists")
	}
	waitFor(t, 10*time.Second, "idle", func() bool { p, f, _ := st.QueueStats(); return p == 0 && f == 0 })
}

func exists(srv *testkit.Server, path string) bool {
	_, ok := srv.Do("GET", "/api/v1/files/resolve?path="+url.QueryEscape(path), nil, 0)["node"].(map[string]any)
	return ok
}

func openUploads(t *testing.T, srv *testkit.Server) []string {
	out := srv.Do("GET", "/api/v1/admin/uploads", nil, 200)
	ids := []string{}
	list, _ := out["uploads"].([]any)
	for _, u := range list {
		if m, ok := u.(map[string]any); ok && m["status"] == "open" {
			ids = append(ids, m["id"].(string))
		}
	}
	return ids
}

// newEngineClient is newEngine with the given settings that also returns
// the engine's client.
func newEngineClient(t *testing.T, srv *testkit.Server, s config.Settings) (*engine.Engine, *state.Store, *client.Client) {
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := engine.New(st, slog.New(slog.NewTextHandler(io.Discard, nil)), "TEST-PC")
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	c, me := srv.Device()
	e.SetClient(c, me)
	e.ApplySettings(s)
	return e, st, c
}

func writeFileBytes(t *testing.T, p string, b []byte) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Add(-time.Minute)
	os.Chtimes(p, mt, mt)
}

func countSuffix(t *testing.T, root, suffix string) int {
	n := 0
	_ = filepath.WalkDir(filepath.Join(root, "blobs"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, suffix) {
			n++
		}
		return nil
	})
	return n
}

func freePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	l.Close()
	return port
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
