package agent

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/updater"
)

type memKeys struct {
	mu  sync.Mutex
	key ed25519.PrivateKey
}

func (m *memKeys) Load() (ed25519.PrivateKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.key == nil {
		return nil, os.ErrNotExist
	}
	return m.key, nil
}
func (m *memKeys) Save(k ed25519.PrivateKey) error { m.mu.Lock(); m.key = k; m.mu.Unlock(); return nil }
func (m *memKeys) Delete() error                   { m.mu.Lock(); m.key = nil; m.mu.Unlock(); return nil }

func testOptions(dir string) Options {
	return Options{DataDir: dir, Keys: &memKeys{}, DeviceName: "TEST-PC", OS: "test", Identifier: "test-id", SnapshotInterval: 20 * time.Millisecond}
}

func startHost(t *testing.T, dir string) *Host {
	t.Helper()
	h, err := NewHost(testOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func code(err error) string {
	var ae *Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func TestHostLifecycle(t *testing.T) {
	dir := t.TempDir()
	h := startHost(t, dir)
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	ctx := context.Background()
	shut, err := h.Subscribe(ctx, KindShutdown)
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.Bootstrap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b.Configured || b.Connected || b.Revoked || b.ProtocolVersion != ProtocolVersion || b.DeviceName != "TEST-PC" {
		t.Fatalf("unexpected bootstrap %+v", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.db")); err != nil {
		t.Fatalf("state.db not created: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	var se ShutdownEvent
	if err := recv(t, shut).Decode(&se); err != nil || se.Reason != "stop" {
		t.Fatalf("shutdown event %+v %v", se, err)
	}
	if _, ok := <-shut.C(); ok {
		t.Fatal("subscription should close with the host")
	}
	if _, err := h.Bootstrap(ctx); code(err) != CodeUnavailable {
		t.Fatalf("call after Close: %v", err)
	}
	if err := h.Start(ctx); code(err) != CodeUnavailable {
		t.Fatalf("Start after Close: %v", err)
	}
}

func TestHostSnapshotEvents(t *testing.T) {
	h := startHost(t, t.TempDir())
	c := NewInProc(h)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := c.Subscribe(ctx, KindSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	ev := recv(t, s)
	var snap struct {
		Status    string `json:"status"`
		Connected bool   `json:"connected"`
	}
	if err := ev.Decode(&snap); err != nil || snap.Status == "" || snap.Connected {
		t.Fatalf("bad snapshot %s: %v", ev.Data, err)
	}
}

func TestInProcRoundTrips(t *testing.T) {
	dir := t.TempDir()
	h := startHost(t, dir)
	c := NewInProc(h)
	ctx := context.Background()

	// Configurations.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved, err := c.SaveConfig(ctx, config.SyncConfig{Name: "Docs", Enabled: true, LocalPath: root, Destination: "/Docs", Recursive: true, Mode: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.CreatedAt == 0 {
		t.Fatalf("not saved: %+v", saved)
	}
	list, err := c.ListConfigs(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "Docs" {
		t.Fatalf("ListConfigs = %+v, %v", list, err)
	}
	if _, err := c.SaveConfig(ctx, config.SyncConfig{Name: "docs", LocalPath: root}); code(err) != CodeInvalid {
		t.Fatalf("duplicate name: %v", err)
	}
	prev, err := c.PreviewConfig(ctx, saved)
	if err != nil || prev.Total != 1 || len(prev.Entries) != 1 || prev.Entries[0].RelPath != "a.txt" {
		t.Fatalf("PreviewConfig = %+v, %v", prev, err)
	}
	if err := c.SyncNow(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.SyncNow(ctx, "nope"); code(err) != CodeNotFound {
		t.Fatalf("SyncNow unknown: %v", err)
	}
	// Without a server the scan queues the file.
	deadline := time.Now().Add(5 * time.Second)
	for {
		items, err := c.ListQueue(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 1 && items[0].RelPath == "a.txt" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("file never queued: %+v", items)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := c.ListQueue(ctx, "bogus"); code(err) != CodeInvalid {
		t.Fatalf("bad status: %v", err)
	}
	if hist, err := c.ListHistory(ctx, ""); err != nil || hist == nil {
		t.Fatalf("ListHistory = %v, %v", hist, err)
	}
	if n, err := c.RetryFailed(ctx, 0); err != nil || n != 0 {
		t.Fatalf("RetryFailed = %d, %v", n, err)
	}
	if snap, err := c.Snapshot(ctx); err != nil || snap.Connected || snap.Active == nil {
		t.Fatalf("Snapshot = %+v, %v", snap, err)
	}
	if autos, err := c.ListAutomations(ctx); err != nil || autos == nil {
		t.Fatalf("ListAutomations = %v, %v", autos, err)
	}

	// Settings: identity fields are not editable; changes are persisted and announced.
	sub, err := c.Subscribe(ctx, KindSettings)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	s, err := c.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.BandwidthKBps, s.DeviceID, s.ServerURL = 512, 99, "https://evil.example"
	out, err := c.SaveSettings(ctx, s)
	if err != nil || out.BandwidthKBps != 512 || out.DeviceID != 0 || out.ServerURL != "" {
		t.Fatalf("SaveSettings = %+v, %v", out, err)
	}
	var announced config.Settings
	if err := recv(t, sub).Decode(&announced); err != nil || announced.BandwidthKBps != 512 {
		t.Fatalf("settings event %+v, %v", announced, err)
	}
	if err := c.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	if snap, _ := c.Snapshot(ctx); !snap.Paused {
		t.Fatal("SetPaused did not reach the engine")
	}

	// Updates are disabled without a repository.
	us, err := c.UpdateStatus(ctx)
	if err != nil || us.State != updater.StateDisabled {
		t.Fatalf("UpdateStatus = %+v, %v", us, err)
	}
	if err := c.InstallUpdate(ctx); code(err) != CodeUnavailable {
		t.Fatalf("InstallUpdate: %v", err)
	}

	// Connection validation and disconnect without a server.
	if _, err := c.Connect(ctx, ConnectRequest{URL: "https://vault.example", Login: "", Password: ""}); code(err) != CodeInvalid {
		t.Fatalf("Connect without credentials: %v", err)
	}
	if err := c.CancelEnrollment(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}

	if err := c.DeleteConfig(ctx, ""); code(err) != CodeInvalid {
		t.Fatalf("DeleteConfig empty id: %v", err)
	}
	if err := c.DeleteConfig(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := c.ListConfigs(ctx); len(list) != 0 {
		t.Fatalf("config not deleted: %+v", list)
	}

	// Settings survive a restart of the host.
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	h2 := startHost(t, dir)
	got, err := NewInProc(h2).GetSettings(ctx)
	if err != nil || got.BandwidthKBps != 512 || !got.Paused {
		t.Fatalf("settings after restart = %+v, %v", got, err)
	}
}

func TestInProcCanceledContext(t *testing.T) {
	h := startHost(t, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewInProc(h).Bootstrap(ctx); code(err) != CodeCanceled {
		t.Fatalf("canceled ctx: %v", err)
	}
}

func TestErrorIs(t *testing.T) {
	rt, err := roundTrip(AsError(ErrNotImplemented))
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(rt, ErrNotImplemented) || errors.Is(rt, ErrClosed) {
		t.Fatal("Error.Is should match by code after a JSON round trip")
	}
	if AsError(nil) != nil || AsError(errors.New("x")).Code != CodeInternal {
		t.Fatal("AsError mapping")
	}
}
