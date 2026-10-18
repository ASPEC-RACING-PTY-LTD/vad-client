package modules

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/client"
	sdkmod "github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/modules"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// fakeSender records reports and fails the first `fail` calls.
type fakeSender struct {
	mu    sync.Mutex
	fail  int
	calls []time.Time
	sent  []wire.ModuleReport
	got   chan struct{}
}

func newFakeSender(fail int) *fakeSender {
	return &fakeSender{fail: fail, got: make(chan struct{}, 64)}
}

func (f *fakeSender) send(_ context.Context, b wire.ModuleReport) error {
	f.mu.Lock()
	defer func() { f.mu.Unlock(); f.got <- struct{}{} }()
	f.calls = append(f.calls, time.Now())
	if f.fail > 0 {
		f.fail--
		return errors.New("boom")
	}
	f.sent = append(f.sent, b)
	return nil
}

func (f *fakeSender) wait(t *testing.T, n int) {
	t.Helper()
	for range n {
		select {
		case <-f.got:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for report")
		}
	}
}

func (f *fakeSender) snapshot() (calls int, sent []wire.ModuleReport) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls), append([]wire.ModuleReport(nil), f.sent...)
}

func openStore(t *testing.T) (*state.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, dir
}

func newMgr(t *testing.T, st *state.Store, dir string, o Options) *Manager {
	t.Helper()
	o.DB, o.ModeFile, o.CoreVersion = st.DB, filepath.Join(dir, "config", "mode.json"), "1.4.0"
	m, err := New(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestReportLoop(t *testing.T) {
	st, dir := openStore(t)
	fs := newFakeSender(2)
	m := newMgr(t, st, dir, Options{Send: fs.send, ReportInterval: 150 * time.Millisecond,
		RetryMin: 10 * time.Millisecond, RetryMax: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.RunReporter(ctx); close(done) }()

	// Startup report fails twice, then succeeds after backoff.
	fs.wait(t, 3)
	if calls, sent := fs.snapshot(); calls != 3 || len(sent) != 1 || sent[0].InstallMode != "simple" {
		t.Fatalf("startup: calls=%d sent=%v", calls, sent)
	}
	if m.Snapshot().ReportedAt == 0 {
		t.Fatal("reported_at not recorded")
	}

	// A change is sent by the loop (not synchronously) right away.
	if _, err := m.SetMode(ctx, sdkmod.ModeAdvanced); err != nil {
		t.Fatal(err)
	}
	fs.wait(t, 1)
	if _, sent := fs.snapshot(); len(sent) != 2 || sent[1].InstallMode != "advanced" {
		t.Fatalf("after change: %v", sent)
	}

	// The periodic ("daily") report follows without a change.
	fs.wait(t, 1)
	if _, sent := fs.snapshot(); len(sent) != 3 {
		t.Fatalf("periodic: %d reports", len(sent))
	}

	// Only one loop may run.
	m.RunReporter(ctx) // returns at once
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reporter did not stop")
	}
}

func TestReportLoopKick(t *testing.T) {
	st, dir := openStore(t)
	fs := newFakeSender(0)
	m := newMgr(t, st, dir, Options{Send: fs.send})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.RunReporter(ctx)
	fs.wait(t, 1)
	m.Kick()
	fs.wait(t, 1)
	if calls, _ := fs.snapshot(); calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestClientSenderNotConnected(t *testing.T) {
	s := ClientSender(func() *client.Client { return nil })
	if err := s(context.Background(), wire.ModuleReport{}); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
	if err := ClientSender(nil)(context.Background(), wire.ModuleReport{}); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("nil getter: %v", err)
	}
}

func TestNewForHost(t *testing.T) {
	st, dir := openStore(t)
	var mu sync.Mutex
	var got []any
	m := NewForHost(st.DB, filepath.Join(dir, "mode.json"), "2.0.0", func() *client.Client { return nil },
		func(p any) { mu.Lock(); got = append(got, p); mu.Unlock() })
	if m == nil || m.Mode() != sdkmod.ModeSimple || m.Snapshot().CoreVersion != "2.0.0" {
		t.Fatalf("manager: %+v", m)
	}
	// Not connected: the change is published, the report fails softly.
	if _, err := m.SetMode(context.Background(), sdkmod.ModeAdvanced); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("published %d", len(got))
	}
	if s, ok := got[0].(ModulesState); !ok || s.InstallMode != sdkmod.ModeAdvanced {
		t.Fatalf("payload %#v", got[0])
	}
}

func modeTask(id int64, target string) wire.DeviceTask {
	p, _ := json.Marshal(wire.TaskModeChangeRequestPayload{TargetMode: target, RequestedBy: "admin@example"})
	return wire.DeviceTask{ID: id, Kind: wire.TaskKindModeChangeRequest, Payload: p}
}

type taskOut struct {
	res any
	err error
}

func startTask(m *Manager, ctx context.Context, t wire.DeviceTask) chan taskOut {
	ch := make(chan taskOut, 1)
	go func() { r, err := m.HandleTask(ctx, t); ch <- taskOut{r, err} }()
	return ch
}

func waitPending(t *testing.T, m *Manager) *ModeRequest {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r := m.PendingModeRequest(); r != nil {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no pending request")
	return nil
}

func recv(t *testing.T, ch chan taskOut) taskOut {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("HandleTask did not return")
	}
	return taskOut{}
}

func TestModeRequestConfirmDeny(t *testing.T) {
	st, dir := openStore(t)
	fs := newFakeSender(0)
	src := newSource(AffectedItem{Kind: "sync_folder", ID: "f1", Requires: []sdkmod.ID{sdkmod.Sync}})
	var evMu sync.Mutex
	var evts []ModulesState
	m := newMgr(t, st, dir, Options{Send: fs.send, Sources: []ItemSource{src},
		OnChange: func(s ModulesState) { evMu.Lock(); evts = append(evts, s); evMu.Unlock() }})
	ctx := context.Background()

	// Deny: mode unchanged, task done with approved=false.
	ch := startTask(m, ctx, modeTask(7, "advanced"))
	req := waitPending(t, m)
	if req.TaskID != 7 || req.TargetMode != sdkmod.ModeAdvanced || req.CurrentMode != sdkmod.ModeSimple ||
		req.RequestedBy != "admin@example" || len(req.Preview.Enable) == 0 {
		t.Fatalf("request %+v", req)
	}
	if m.Snapshot().PendingRequest == nil {
		t.Fatal("snapshot lacks pending request")
	}
	evMu.Lock()
	if n := len(evts); n == 0 || evts[n-1].PendingRequest == nil {
		t.Fatal("pending request not published")
	}
	evMu.Unlock()
	if _, err := m.ConfirmModeRequest(ctx, "wrong", true); !errors.Is(err, ErrNoModeRequest) {
		t.Fatalf("wrong id: %v", err)
	}
	if _, err := m.ConfirmModeRequest(ctx, req.ID, false); err != nil {
		t.Fatal(err)
	}
	o := recv(t, ch)
	if r, ok := o.res.(ModeChangeResult); o.err != nil || !ok || r.Approved || r.InstallMode != sdkmod.ModeSimple {
		t.Fatalf("deny result %+v %v", o.res, o.err)
	}
	if m.Mode() != sdkmod.ModeSimple || m.PendingModeRequest() != nil {
		t.Fatal("deny changed the mode or left the request pending")
	}
	if _, sent := fs.snapshot(); len(sent) != 0 {
		t.Fatalf("deny reported %d", len(sent))
	}

	// Approve: mode switched, reported, result carries what was applied.
	ch = startTask(m, ctx, modeTask(8, "advanced"))
	req = waitPending(t, m)
	applied, err := m.ConfirmModeRequest(ctx, req.ID, true)
	if err != nil || applied.To != sdkmod.ModeAdvanced {
		t.Fatalf("approve: %+v %v", applied, err)
	}
	o = recv(t, ch)
	r, ok := o.res.(ModeChangeResult)
	if o.err != nil || !ok || !r.Approved || r.FromMode != sdkmod.ModeSimple || r.InstallMode != sdkmod.ModeAdvanced || r.Applied == nil {
		t.Fatalf("approve result %+v %v", o.res, o.err)
	}
	if !m.Enabled(sdkmod.Sync) {
		t.Fatal("sync not enabled")
	}
	if _, sent := fs.snapshot(); len(sent) != 1 || sent[0].InstallMode != "advanced" {
		t.Fatalf("approve reports %v", sent)
	}

	// Already in the target mode: immediate done result, nothing pending.
	o = recv(t, startTask(m, ctx, modeTask(9, "advanced")))
	if r, ok := o.res.(ModeChangeResult); o.err != nil || !ok || !r.Approved || r.Applied != nil {
		t.Fatalf("same-mode result %+v %v", o.res, o.err)
	}

	// Downgrade via request suspends items (never deletes).
	ch = startTask(m, ctx, modeTask(10, "simple"))
	req = waitPending(t, m)
	if len(req.Preview.Affected) != 1 || req.Preview.Affected[0].ID != "f1" {
		t.Fatalf("downgrade preview %+v", req.Preview)
	}
	if _, err := m.ConfirmModeRequest(ctx, req.ID, true); err != nil {
		t.Fatal(err)
	}
	recv(t, ch)
	if !src.suspended["f1"] || len(src.items) != 1 {
		t.Fatalf("f1 not suspended: %+v", src.suspended)
	}
}

func TestModeRequestErrors(t *testing.T) {
	st, dir := openStore(t)
	m := newMgr(t, st, dir, Options{ModeRequestTimeout: 50 * time.Millisecond})
	ctx := context.Background()

	if _, err := m.HandleTask(ctx, wire.DeviceTask{Kind: wire.TaskKindBackupNow}); !errors.Is(err, ErrUnsupportedTask) {
		t.Fatalf("kind: %v", err)
	}
	if _, err := m.HandleTask(ctx, modeTask(1, "expert")); !errors.Is(err, ErrInvalidMode) {
		t.Fatalf("mode: %v", err)
	}
	bad := wire.DeviceTask{Kind: wire.TaskKindModeChangeRequest, Payload: json.RawMessage(`{"target_mode":"advanced","x":1}`)}
	if _, err := m.HandleTask(ctx, bad); err == nil {
		t.Fatal("unknown payload field accepted")
	}

	// Timeout.
	if _, err := m.HandleTask(ctx, modeTask(2, "advanced")); !errors.Is(err, ErrModeRequestTimeout) {
		t.Fatalf("timeout: %v", err)
	}
	if m.PendingModeRequest() != nil {
		t.Fatal("timed-out request still pending")
	}

	// Superseded by a newer request; ctx cancel ends the newer one.
	m.opts.ModeRequestTimeout = time.Minute
	first := startTask(m, ctx, modeTask(3, "advanced"))
	r1 := waitPending(t, m)
	cctx, cancel := context.WithCancel(ctx)
	second := startTask(m, cctx, modeTask(4, "advanced"))
	if o := recv(t, first); !errors.Is(o.err, ErrModeRequestSuperseded) {
		t.Fatalf("superseded: %v", o.err)
	}
	r2 := waitPending(t, m)
	if r2.ID == r1.ID || r2.TaskID != 4 {
		t.Fatalf("second request %+v", r2)
	}
	cancel()
	if o := recv(t, second); !errors.Is(o.err, context.Canceled) {
		t.Fatalf("cancel: %v", o.err)
	}
	if m.Mode() != sdkmod.ModeSimple {
		t.Fatal("mode changed without confirmation")
	}
}

func TestRegisterSourceSuspendResume(t *testing.T) {
	st, dir := openStore(t)
	m := newMgr(t, st, dir, Options{})
	ctx := context.Background()
	if _, err := m.SetMode(ctx, sdkmod.ModeAdvanced); err != nil {
		t.Fatal(err)
	}
	src := newSource(
		AffectedItem{Kind: "automation", ID: "a1", Requires: []sdkmod.ID{sdkmod.Automation}},
		AffectedItem{Kind: "backup_job", ID: "j1", Requires: []sdkmod.ID{sdkmod.Core}},
	)
	m.RegisterSource(src)
	m.RegisterSource(nil) // ignored

	if err := m.SetBuiltin(ctx, sdkmod.Automation, false); err != nil {
		t.Fatal(err)
	}
	if !src.suspended["a1"] || src.suspended["j1"] {
		t.Fatalf("suspend: %+v", src.suspended)
	}
	if err := m.SetBuiltin(ctx, sdkmod.Automation, true); err != nil {
		t.Fatal(err)
	}
	if len(src.suspended) != 0 || len(src.items) != 2 {
		t.Fatalf("resume: %+v items=%d", src.suspended, len(src.items))
	}
}

// fakeConfigs is an in-memory SyncConfigStore.
type fakeConfigs struct {
	mu   sync.Mutex
	cfgs map[string]config.SyncConfig
}

func (f *fakeConfigs) Configs() []config.SyncConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []config.SyncConfig
	for _, c := range f.cfgs {
		out = append(out, c)
	}
	return out
}

func (f *fakeConfigs) SaveConfig(c config.SyncConfig) (config.SyncConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfgs[c.ID] = c
	return c, nil
}

func TestSyncFolderSource(t *testing.T) {
	st, dir := openStore(t)
	store := &fakeConfigs{cfgs: map[string]config.SyncConfig{
		"a": {ID: "a", Name: "Docs", Enabled: true},
		"b": {ID: "b", Name: "Off by user", Enabled: false},
		"c": {ID: "c", Name: "Photos", Enabled: true},
	}}
	src := NewSyncFolderSource(st.DB, store, nil)
	m := newMgr(t, st, dir, Options{})
	m.RegisterSource(src)
	ctx := context.Background()
	if _, err := m.SetMode(ctx, sdkmod.ModeAdvanced); err != nil {
		t.Fatal(err)
	}

	p, err := m.PreviewSwitch(ctx, sdkmod.ModeSimple)
	if err != nil || len(p.Affected) != 2 || p.Affected[0].ID != "a" || p.Affected[1].Kind != ItemKindSyncFolder {
		t.Fatalf("preview %+v %v", p.Affected, err)
	}
	if _, err := m.SetMode(ctx, sdkmod.ModeSimple); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if c, ok := store.cfgs[id]; !ok || c.Enabled {
			t.Fatalf("%s after suspend: %+v ok=%v", id, c, ok)
		}
	}
	if ids, _ := src.Suspended(ctx); len(ids) != 2 {
		t.Fatalf("recorded %v", ids)
	}

	// A fresh source (agent restart) still knows what it suspended.
	src2 := NewSyncFolderSource(st.DB, store, nil)
	if err := src2.Resume(ctx, []sdkmod.ID{sdkmod.Core}); err != nil || store.cfgs["a"].Enabled {
		t.Fatalf("resumed without sync: %v", err)
	}
	if err := src2.Resume(ctx, []sdkmod.ID{sdkmod.Core, sdkmod.Sync}); err != nil {
		t.Fatal(err)
	}
	if !store.cfgs["a"].Enabled || !store.cfgs["c"].Enabled || store.cfgs["b"].Enabled {
		t.Fatalf("resume: %+v", store.cfgs)
	}
	if ids, _ := src2.Suspended(ctx); len(ids) != 0 {
		t.Fatalf("still recorded %v", ids)
	}
}
