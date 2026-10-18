package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/ipc/caller"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/profiles"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/tokens"
)

const testUserSID = "S-1-5-21-1111111111-2222222222-3333333333-1001"

// fakeToken records impersonations; deny simulates a user whose ACLs do not
// grant access (the impersonated work fails without running).
type fakeToken struct {
	mu           sync.Mutex
	sid          string
	deny         bool
	impersonated int
	closed       bool
}

func (t *fakeToken) SID() string                                  { return t.sid }
func (t *fakeToken) Source() tokens.Source                        { return tokens.SourceSession }
func (t *fakeToken) SessionID() (uint32, bool)                    { return 1, true }
func (t *fakeToken) Caps() tokens.Caps                            { return tokens.CapsFor(tokens.SourceSession) }
func (t *fakeToken) AccessCheckPath(string, uint32) (bool, error) { return !t.deny, nil }
func (t *fakeToken) Close() error                                 { t.mu.Lock(); t.closed = true; t.mu.Unlock(); return nil }
func (t *fakeToken) Impersonate(fn func() error) error {
	t.mu.Lock()
	t.impersonated++
	deny := t.deny
	t.mu.Unlock()
	if deny {
		return os.ErrPermission
	}
	return fn()
}

type fakeTokens struct {
	mu    sync.Mutex
	tok   *fakeToken
	err   error
	calls []string
}

func (p *fakeTokens) ForProfile(sid string) (tokens.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, sid)
	if p.err != nil {
		return nil, p.err
	}
	return p.tok, nil
}
func (p *fakeTokens) SessionOnly(sid string) (tokens.Token, error) { return p.ForProfile(sid) }

type fakeProfiles []profiles.LocalProfile

func (f fakeProfiles) LocalProfiles() ([]profiles.LocalProfile, error) { return f, nil }

type guardCall struct {
	op      guard.Op
	path    string
	profile guard.Profile
}

type pathEnv struct {
	h      *Host
	toks   *fakeTokens
	root   string
	mu     sync.Mutex
	guards []guardCall
	block  bool
}

func newPathEnv(t *testing.T) *pathEnv {
	t.Helper()
	e := &pathEnv{toks: &fakeTokens{tok: &fakeToken{sid: testUserSID}}, root: t.TempDir()}
	if err := os.WriteFile(filepath.Join(e.root, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := testOptions(t.TempDir())
	opts.Tokens = e.toks
	opts.Profiles = fakeProfiles{{SID: testUserSID, ProfilePath: `C:\Users\alice`}}
	opts.Guard = func(op guard.Op, path string, p guard.Profile) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.guards = append(e.guards, guardCall{op, path, p})
		if e.block {
			return &guard.BlockedError{Op: op, Path: path, Reason: "another user's profile", Err: guard.ErrOutsideProfile}
		}
		return nil
	}
	h, err := NewHost(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	e.h = h
	return e
}

func (e *pathEnv) cfg() config.SyncConfig {
	return config.SyncConfig{Name: "Docs", Enabled: true, LocalPath: e.root, Destination: "/Docs", Recursive: true, Mode: "manual"}
}

func userCtx() context.Context {
	return caller.With(context.Background(), caller.Caller{SID: testUserSID, SessionID: 1})
}

func systemCtx() context.Context {
	return caller.With(context.Background(), caller.Caller{SID: sidSystem, System: true})
}

func TestPreviewConfigCallerIdentity(t *testing.T) {
	tests := []struct {
		name        string
		ctx         context.Context
		block, deny bool
		tokErr      error
		wantCode    string
		wantToken   bool // a caller token was requested
		wantGuard   bool
		wantImperso bool
	}{
		{"in-process caller is trusted", context.Background(), false, false, nil, "", false, false, false},
		{"system pipe caller is trusted", systemCtx(), false, false, nil, "", false, false, false},
		{"user pipe caller runs as the user", userCtx(), false, false, nil, "", true, true, true},
		{"guard refuses", userCtx(), true, false, nil, CodeAccessDenied, true, true, false},
		{"user cannot read the folder", userCtx(), false, true, nil, CodeAccessDenied, true, true, true},
		{"no token fails closed", userCtx(), false, false, tokens.ErrNotLoggedOn, CodeAccessDenied, true, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newPathEnv(t)
			e.block, e.toks.tok.deny, e.toks.err = tt.block, tt.deny, tt.tokErr
			res, err := e.h.PreviewConfig(tt.ctx, e.cfg())
			if code(err) != tt.wantCode {
				t.Fatalf("PreviewConfig err = %v, want code %q", err, tt.wantCode)
			}
			if tt.wantCode != "" && !errors.Is(err, ErrAccessDenied) {
				t.Fatalf("errors.Is(ErrAccessDenied) = false for %v", err)
			}
			if tt.wantCode == "" && res.Total != 1 {
				t.Fatalf("preview total = %d, want 1", res.Total)
			}
			if got := len(e.toks.calls) > 0; got != tt.wantToken {
				t.Fatalf("token requested = %v", got)
			}
			if got := len(e.guards) > 0; got != tt.wantGuard {
				t.Fatalf("guard called = %v", got)
			}
			if tt.wantGuard {
				g := e.guards[0]
				if g.op != guard.OpRead || g.path != e.root || g.profile.SID != testUserSID || g.profile.Root != `C:\Users\alice` {
					t.Fatalf("guard call = %+v", g)
				}
			}
			if got := e.toks.tok.impersonated > 0; got != tt.wantImperso {
				t.Fatalf("impersonated = %d", e.toks.tok.impersonated)
			}
			if tt.wantToken && tt.tokErr == nil && !e.toks.tok.closed {
				t.Fatal("caller token not closed")
			}
		})
	}
}

func TestPreviewWalkRunsImpersonated(t *testing.T) {
	e := newPathEnv(t)
	if _, err := e.h.PreviewConfig(userCtx(), e.cfg()); err != nil {
		t.Fatal(err)
	}
	// One probe for the folder plus the walk itself.
	if e.toks.tok.impersonated != 2 {
		t.Fatalf("impersonated = %d, want 2", e.toks.tok.impersonated)
	}
}

func TestPreviewFilesKindChecksEveryFile(t *testing.T) {
	e := newPathEnv(t)
	other := filepath.Join(t.TempDir(), "b.txt")
	os.WriteFile(other, []byte("x"), 0o600)
	c := e.cfg()
	c.Kind, c.LocalPath, c.Files = "files", "", []string{filepath.Join(e.root, "a.txt"), other}
	if _, err := e.h.PreviewConfig(userCtx(), c); err != nil {
		t.Fatal(err)
	}
	if len(e.guards) != 2 || e.guards[0].path != c.Files[0] || e.guards[1].path != c.Files[1] {
		t.Fatalf("guard calls = %+v", e.guards)
	}
}

func TestSaveConfigAndSyncNowCallerIdentity(t *testing.T) {
	e := newPathEnv(t)
	// A user may not save a folder they cannot read.
	e.toks.tok.deny = true
	if _, err := e.h.SaveConfig(userCtx(), e.cfg()); code(err) != CodeAccessDenied {
		t.Fatalf("SaveConfig as denied user = %v", err)
	}
	if v, _ := e.h.ListConfigs(context.Background()); len(v) != 0 {
		t.Fatalf("denied config was saved: %+v", v)
	}
	// Saved in-process for another user: the pipe user cannot see or
	// trigger it.
	other := e.cfg()
	other.Name, other.OwnerSID = "Other", "S-1-5-21-1111111111-2222222222-3333333333-1002"
	foreign, err := e.h.SaveConfig(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.h.SyncNow(userCtx(), foreign.ID); code(err) != CodeNotFound {
		t.Fatalf("SyncNow(foreign) = %v", err)
	}
	// The user's own folder: refused while their token cannot read it.
	e.toks.tok.deny = false
	saved, err := e.h.SaveConfig(userCtx(), e.cfg())
	if err != nil {
		t.Fatal(err)
	}
	if saved.OwnerSID != testUserSID {
		t.Fatalf("owner = %q", saved.OwnerSID)
	}
	e.toks.tok.deny = true
	if err := e.h.SyncNow(userCtx(), saved.ID); code(err) != CodeAccessDenied {
		t.Fatalf("SyncNow(id) as denied user = %v", err)
	}
	if err := e.h.SyncNow(userCtx(), ""); err != nil {
		t.Fatalf("SyncNow(all) skips inaccessible folders, got %v", err)
	}
	e.block = true
	e.toks.tok.deny = false
	if err := e.h.SyncNow(userCtx(), saved.ID); code(err) != CodeAccessDenied {
		t.Fatalf("SyncNow(id) on guarded path = %v", err)
	}
	e.block = false
	if err := e.h.SyncNow(userCtx(), saved.ID); err != nil {
		t.Fatalf("SyncNow(id) as permitted user = %v", err)
	}
	if err := e.h.SyncNow(systemCtx(), foreign.ID); err != nil {
		t.Fatalf("SyncNow as SYSTEM = %v", err)
	}
	if err := e.h.SyncNow(userCtx(), "missing"); code(err) != CodeNotFound {
		t.Fatalf("SyncNow(missing) = %v", err)
	}
}

func TestInstallUpdateRefusedInServiceMode(t *testing.T) {
	newHost := func(t *testing.T, service bool) *Host {
		opts := testOptions(t.TempDir())
		opts.Service = service
		opts.UpdateRepo = "example/repo"
		h, err := NewHost(opts)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Close() })
		return h
	}
	t.Run("service option", func(t *testing.T) {
		h := newHost(t, true)
		for _, ctx := range []context.Context{context.Background(), systemCtx(), userCtx()} {
			if err := h.InstallUpdate(ctx); !errors.Is(err, ErrUpdatesManaged) {
				t.Fatalf("InstallUpdate = %v, want %v", err, ErrUpdatesManaged)
			}
		}
		if err := h.requestInstall(); !errors.Is(err, ErrUpdatesManaged) {
			t.Fatalf("requestInstall (auto-update path) = %v", err)
		}
	})
	t.Run("process is LocalSystem", func(t *testing.T) {
		old := runningAsSystem
		runningAsSystem = func() bool { return true }
		t.Cleanup(func() { runningAsSystem = old })
		h := newHost(t, false)
		if err := h.InstallUpdate(context.Background()); code(err) != CodeUpdatesManaged {
			t.Fatalf("InstallUpdate as SYSTEM = %v", err)
		}
	})
	t.Run("pipe call", func(t *testing.T) {
		old := runningAsSystem
		runningAsSystem = func() bool { return false }
		t.Cleanup(func() { runningAsSystem = old })
		h := newHost(t, false)
		if err := h.InstallUpdate(systemCtx()); code(err) != CodeUpdatesManaged {
			t.Fatalf("InstallUpdate over the pipe = %v", err)
		}
	})
	t.Run("in-process desktop app is unchanged", func(t *testing.T) {
		old := runningAsSystem
		runningAsSystem = func() bool { return false }
		t.Cleanup(func() { runningAsSystem = old })
		opts := testOptions(t.TempDir()) // UpdateRepo "" = updates disabled
		h, err := NewHost(opts)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Close() })
		if err := h.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := h.InstallUpdate(context.Background()); code(err) != CodeUnavailable {
			t.Fatalf("InstallUpdate in-process = %v, want unavailable (disabled build)", err)
		}
	})
}
