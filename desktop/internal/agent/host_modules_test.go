package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/modules"
	sdkmod "github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/modules"
)

func TestHostStartCreatesModuleManager(t *testing.T) {
	dir := t.TempDir()
	h, err := NewHost(testOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	if h.Modules() != nil {
		t.Fatal("module manager created before Start")
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	m := h.Modules()
	if m == nil {
		t.Fatal("Start did not create the module manager")
	}
	if got := m.Snapshot().InstallMode; got != sdkmod.ModeSimple {
		t.Fatalf("fresh install mode = %q, want simple", got)
	}
	f, err := modules.ReadModeFile(filepath.Join(dir, "config", "mode.json"))
	if err != nil || f.Mode != sdkmod.ModeSimple {
		t.Fatalf("mode file = %+v, %v", f, err)
	}
	// Close stops the report loop (waits for it) without hanging.
	done := make(chan struct{})
	go func() { _ = h.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung on the module report loop")
	}
}

func TestHostUpgradeWithSyncFoldersStartsAdvanced(t *testing.T) {
	dir := t.TempDir()
	modeFile := filepath.Join(dir, "cfg", "mode.json")

	// A pre-modules install: a sync folder in state.db, no mode file.
	h, err := NewHost(testOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.st.SaveConfig(config.SyncConfig{ID: "c1", Name: "Docs", Kind: "folder", LocalPath: dir, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(modeFile); !os.IsNotExist(err) {
		t.Fatalf("mode file exists before upgrade: %v", err)
	}

	opts := testOptions(dir)
	opts.ModeFile = modeFile
	h, err = NewHost(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.Modules().Snapshot().InstallMode; got != sdkmod.ModeAdvanced {
		t.Fatalf("upgrade install mode = %q, want advanced", got)
	}
	f, err := modules.ReadModeFile(modeFile)
	if err != nil || f.Mode != sdkmod.ModeAdvanced {
		t.Fatalf("mode file = %+v, %v", f, err)
	}
}

func TestHostModulesEventPublished(t *testing.T) {
	h := startHost(t, t.TempDir())
	sub, err := h.Subscribe(context.Background(), KindModules)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	if _, err := h.Modules().SetMode(context.Background(), sdkmod.ModeAdvanced); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-sub.C():
		var s modules.ModulesState
		if err := ev.Decode(&s); err != nil || s.InstallMode != sdkmod.ModeAdvanced {
			t.Fatalf("modules event = %s, %v", ev.Data, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no modules event after SetMode")
	}
}
