package modules

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/state"
	sdkmod "github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/modules"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// fakeSource keeps items in memory; deleting is impossible by construction,
// and the test checks every item survives.
type fakeSource struct {
	mu        sync.Mutex
	items     map[string]AffectedItem
	suspended map[string]bool
}

func newSource(items ...AffectedItem) *fakeSource {
	f := &fakeSource{items: map[string]AffectedItem{}, suspended: map[string]bool{}}
	for _, it := range items {
		f.items[it.ID] = it
	}
	return f
}

func (f *fakeSource) Affected(_ context.Context, ids []sdkmod.ID) ([]AffectedItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []AffectedItem
	for _, it := range f.items {
		for _, r := range it.Requires {
			if slices.Contains(ids, r) {
				out = append(out, it)
				break
			}
		}
	}
	slices.SortFunc(out, func(a, b AffectedItem) int {
		if a.ID < b.ID {
			return -1
		}
		return 1
	})
	return out, nil
}

func (f *fakeSource) Suspend(_ context.Context, items []AffectedItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range items {
		f.suspended[it.ID] = true
	}
	return nil
}

func (f *fakeSource) Resume(_ context.Context, enabled []sdkmod.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id := range f.suspended {
		ok := true
		for _, r := range f.items[id].Requires {
			ok = ok && slices.Contains(enabled, r)
		}
		if ok {
			delete(f.suspended, id)
		}
	}
	return nil
}

type env struct {
	dir   string
	store *state.Store
	src   *fakeSource
	sent  []wire.ModuleReport
	evts  []ModulesState
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &env{dir: dir, store: st, src: newSource(
		AffectedItem{Kind: "sync_folder", ID: "f1", Name: "Two-way docs", Requires: []sdkmod.ID{sdkmod.Sync}},
		AffectedItem{Kind: "backup_job", ID: "j1", Name: "Apps", Requires: []sdkmod.ID{sdkmod.Discovery}},
		AffectedItem{Kind: "backup_job", ID: "j2", Name: "Docs", Requires: []sdkmod.ID{sdkmod.Core}},
	)}
}

func (e *env) modeFile() string { return filepath.Join(e.dir, "config", "mode.json") }

func (e *env) open(t *testing.T) *Manager {
	t.Helper()
	m, err := New(context.Background(), Options{
		DB: e.store.DB, ModeFile: e.modeFile(), CoreVersion: "1.4.0",
		Sources:  []ItemSource{e.src},
		Send:     func(_ context.Context, b wire.ModuleReport) error { e.sent = append(e.sent, b); return nil },
		OnChange: func(s ModulesState) { e.evts = append(e.evts, s) },
		Now:      func() time.Time { return time.UnixMilli(1_700_000_000_000) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func stateOf(m *Manager, id sdkmod.ID) State {
	for _, mi := range m.Snapshot().Modules {
		if mi.ID == id {
			return mi.State
		}
	}
	return ""
}

func TestModePersistence(t *testing.T) {
	tests := []struct {
		name string
		file string // "" = no mode file
		want sdkmod.Mode
	}{
		{"no file defaults to simple", "", sdkmod.ModeSimple},
		{"installer chose advanced", `{"mode":"advanced"}`, sdkmod.ModeAdvanced},
		{"installer chose simple", `{"mode":"simple"}`, sdkmod.ModeSimple},
		{"bad file ignored", `{"mode":"turbo"}`, sdkmod.ModeSimple},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			if tt.file != "" {
				os.MkdirAll(filepath.Dir(e.modeFile()), 0o700)
				os.WriteFile(e.modeFile(), []byte(tt.file), 0o600)
			}
			m := e.open(t)
			if got := m.Mode(); got != tt.want {
				t.Fatalf("mode = %s, want %s", got, tt.want)
			}
			if !m.Enabled(sdkmod.Core) {
				t.Fatal("core must be enabled")
			}
			if got := m.Enabled(sdkmod.Sync); got != (tt.want == sdkmod.ModeAdvanced) {
				t.Fatalf("sync enabled = %v", got)
			}
			if tt.want == sdkmod.ModeAdvanced && stateOf(m, sdkmod.VSS) != StateWanted {
				t.Fatalf("vss = %s, want wanted", stateOf(m, sdkmod.VSS))
			}
			// The DB is authoritative after first run, even if the file changes.
			os.MkdirAll(filepath.Dir(e.modeFile()), 0o700)
			os.WriteFile(e.modeFile(), []byte(`{"mode":"advanced"}`), 0o600)
			if got := e.open(t).Mode(); got != tt.want {
				t.Fatalf("reopened mode = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSwitchUpAndDown(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	m := e.open(t)

	// Simple → Advanced.
	p, err := m.PreviewSwitch(ctx, sdkmod.ModeAdvanced)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Enable, []sdkmod.ID{sdkmod.Sync, sdkmod.Automation, sdkmod.Profiles}) ||
		!slices.Equal(p.Want, []sdkmod.ID{sdkmod.VSS, sdkmod.Discovery, sdkmod.Reinstall}) ||
		len(p.Suspend) != 0 || len(p.Affected) != 0 || !p.NoDeletes {
		t.Fatalf("up preview = %+v", p)
	}
	if _, err := m.SetMode(ctx, sdkmod.ModeAdvanced); err != nil {
		t.Fatal(err)
	}
	for _, id := range []sdkmod.ID{sdkmod.Sync, sdkmod.Automation, sdkmod.Profiles} {
		if !m.Enabled(id) {
			t.Fatalf("%s not enabled", id)
		}
	}
	for _, id := range sdkmod.ComponentsForMode(sdkmod.ModeAdvanced) {
		if m.Enabled(id) || stateOf(m, id) != StateWanted {
			t.Fatalf("%s = %s, want wanted", id, stateOf(m, id))
		}
	}
	if f, err := ReadModeFile(e.modeFile()); err != nil || f.Mode != sdkmod.ModeAdvanced {
		t.Fatalf("mode file = %+v, %v", f, err)
	}
	if len(e.evts) != 1 || e.evts[0].InstallMode != sdkmod.ModeAdvanced || len(e.sent) != 1 {
		t.Fatalf("events %d, reports %d", len(e.evts), len(e.sent))
	}

	// Advanced → Simple: preview lists what is suspended.
	p, err = m.PreviewSwitch(ctx, sdkmod.ModeSimple)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Suspend, sdkmod.All[1:]) || len(p.Enable)+len(p.Want) != 0 {
		t.Fatalf("down preview = %+v", p)
	}
	var ids []string
	for _, it := range p.Affected {
		ids = append(ids, it.ID)
	}
	if !slices.Equal(ids, []string{"f1", "j1"}) {
		t.Fatalf("affected = %v", ids)
	}
	if len(e.src.suspended) != 0 {
		t.Fatal("preview must not suspend")
	}
	if _, err := m.SetMode(ctx, sdkmod.ModeSimple); err != nil {
		t.Fatal(err)
	}
	if !e.src.suspended["f1"] || !e.src.suspended["j1"] || e.src.suspended["j2"] {
		t.Fatalf("suspended = %v", e.src.suspended)
	}
	for _, id := range sdkmod.All[1:] {
		if stateOf(m, id) != StateSuspended {
			t.Fatalf("%s = %s, want suspended", id, stateOf(m, id))
		}
	}
	// Persisted across restart.
	m2 := e.open(t)
	if m2.Mode() != sdkmod.ModeSimple || stateOf(m2, sdkmod.Sync) != StateSuspended {
		t.Fatalf("reopened: %+v", m2.Snapshot())
	}

	// Back up: suspended items resume.
	if _, err := m2.SetMode(ctx, sdkmod.ModeAdvanced); err != nil {
		t.Fatal(err)
	}
	if e.src.suspended["f1"] || !e.src.suspended["j1"] { // j1 still needs the discovery component
		t.Fatalf("after resume suspended = %v", e.src.suspended)
	}
}

func TestNeverDeletes(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	m := e.open(t)
	before := len(e.src.items)
	for _, mode := range []sdkmod.Mode{sdkmod.ModeAdvanced, sdkmod.ModeSimple, sdkmod.ModeAdvanced, sdkmod.ModeSimple} {
		if _, err := m.SetMode(ctx, mode); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := e.store.DB.QueryRow("SELECT COUNT(*) FROM modules").Scan(&n); err != nil || n != len(sdkmod.All) {
			t.Fatalf("module rows = %d, %v", n, err)
		}
	}
	if len(e.src.items) != before {
		t.Fatalf("items %d → %d", before, len(e.src.items))
	}
	if err := m.SetBuiltin(ctx, sdkmod.Sync, true); !errors.Is(err, ErrModeRequired) {
		t.Fatalf("enable in simple: %v", err)
	}
	if err := m.SetBuiltin(ctx, sdkmod.VSS, true); !errors.Is(err, ErrNotBuiltin) {
		t.Fatalf("toggle component: %v", err)
	}
	if _, err := m.SetMode(ctx, "turbo"); !errors.Is(err, ErrInvalidMode) {
		t.Fatalf("bad mode: %v", err)
	}
}

func TestBuiltinToggle(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	m := e.open(t)
	if _, err := m.SetMode(ctx, sdkmod.ModeAdvanced); err != nil {
		t.Fatal(err)
	}
	if err := m.SetBuiltin(ctx, sdkmod.Sync, false); err != nil {
		t.Fatal(err)
	}
	if m.Enabled(sdkmod.Sync) || !e.src.suspended["f1"] || len(e.src.items) != 3 {
		t.Fatalf("after disable: %v %v", m.Enabled(sdkmod.Sync), e.src.suspended)
	}
	if err := m.SetBuiltin(ctx, sdkmod.Sync, true); err != nil {
		t.Fatal(err)
	}
	if !m.Enabled(sdkmod.Sync) || e.src.suspended["f1"] {
		t.Fatal("re-enable did not resume")
	}
}

type fakeDL struct{ fail sdkmod.ID }

func (d fakeDL) Install(_ context.Context, id sdkmod.ID) (string, error) {
	if id == d.fail {
		return "", errors.New("checksum mismatch")
	}
	return "1.4.0", nil
}

func TestDownloaderAndReportBody(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	var got []byte
	m, err := New(ctx, Options{
		DB: e.store.DB, CoreVersion: "1.4.0", Downloader: fakeDL{fail: sdkmod.Reinstall},
		Send: func(_ context.Context, b wire.ModuleReport) error {
			var err error
			got, err = json.Marshal(b)
			return err
		},
		Now: func() time.Time { return time.UnixMilli(42) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetMode(ctx, sdkmod.ModeAdvanced); err != nil {
		t.Fatal(err)
	}
	if err := m.SetBuiltin(ctx, sdkmod.Profiles, false); err != nil {
		t.Fatal(err)
	}
	want := `{"install_mode":"advanced","core_version":"1.4.0","modules":[` +
		`{"id":"core","version":"1.4.0","state":"enabled"},` +
		`{"id":"sync","version":"1.4.0","state":"enabled"},` +
		`{"id":"automation","version":"1.4.0","state":"enabled"},` +
		`{"id":"profiles","version":"1.4.0","state":"disabled"},` +
		`{"id":"vss","version":"1.4.0","state":"enabled"},` +
		`{"id":"discovery","version":"1.4.0","state":"enabled"},` +
		`{"id":"reinstall","version":"","state":"failed","error":"checksum mismatch"}]}`
	if string(got) != want {
		t.Fatalf("report body\n got %s\nwant %s", got, want)
	}
	if m.Snapshot().ReportedAt != 42 {
		t.Fatalf("reported_at = %d", m.Snapshot().ReportedAt)
	}

	// Suspended and wanted map to "disabled" on the wire.
	if _, err := m.SetMode(ctx, sdkmod.ModeSimple); err != nil {
		t.Fatal(err)
	}
	for _, ms := range m.ReportBody().Modules {
		if ms.ID != "core" && ms.State != wire.ModuleStateDisabled {
			t.Fatalf("%s state %s", ms.ID, ms.State)
		}
		if !sdkmod.State(ms.State).Valid() {
			t.Fatalf("invalid wire state %q", ms.State)
		}
	}
}

func TestNoopDownloaderKeepsWanted(t *testing.T) {
	if _, err := (NoopDownloader{}).Install(context.Background(), sdkmod.VSS); !errors.Is(err, ErrDownloadUnavailable) {
		t.Fatal(err)
	}
}

func TestUpgradeWithSyncFoldersDefaultsToAdvanced(t *testing.T) {
	tests := []struct {
		name    string
		folders bool
		file    string // "" = no mode file
		want    sdkmod.Mode
	}{
		{"fresh install stays simple", false, "", sdkmod.ModeSimple},
		{"upgrade with sync folders", true, "", sdkmod.ModeAdvanced},
		{"installer choice wins", true, `{"mode":"simple"}`, sdkmod.ModeSimple},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			if tt.folders {
				if _, err := e.store.DB.Exec(`INSERT INTO configs(id, data) VALUES('c1', '{"id":"c1","enabled":true}')`); err != nil {
					t.Fatal(err)
				}
			}
			if tt.file != "" {
				os.MkdirAll(filepath.Dir(e.modeFile()), 0o700)
				os.WriteFile(e.modeFile(), []byte(tt.file), 0o600)
			}
			m := e.open(t)
			if got := m.Mode(); got != tt.want {
				t.Fatalf("mode = %s, want %s", got, tt.want)
			}
			f, err := ReadModeFile(e.modeFile())
			if err != nil || f.Mode != tt.want {
				t.Fatalf("mode file = %+v, %v; want %s", f, err, tt.want)
			}
			if tt.want == sdkmod.ModeAdvanced && !m.Enabled(sdkmod.Sync) {
				t.Fatal("sync must be enabled after an advanced upgrade")
			}
			// Persisted: a later run keeps the mode even after folders go.
			if _, err := e.store.DB.Exec(`DELETE FROM configs`); err != nil {
				t.Fatal(err)
			}
			if got := e.open(t).Mode(); got != tt.want {
				t.Fatalf("reopened mode = %s, want %s", got, tt.want)
			}
		})
	}
}
