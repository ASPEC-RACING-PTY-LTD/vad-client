package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/tokens"
)

const ownerSID = "S-1-5-21-1111111111-2222222222-3333333333-1001"

// ownerToken tracks whether work runs inside Impersonate.
type ownerToken struct {
	mu     sync.Mutex
	sid    string
	deny   bool
	inside bool
	calls  int
	closed bool
}

func (t *ownerToken) SID() string                                  { return t.sid }
func (t *ownerToken) Source() tokens.Source                        { return tokens.SourceS4U }
func (t *ownerToken) SessionID() (uint32, bool)                    { return 0, false }
func (t *ownerToken) Caps() tokens.Caps                            { return tokens.CapsFor(tokens.SourceS4U) }
func (t *ownerToken) AccessCheckPath(string, uint32) (bool, error) { return !t.deny, nil }
func (t *ownerToken) Close() error {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	return nil
}
func (t *ownerToken) Impersonate(fn func() error) error {
	t.mu.Lock()
	t.calls++
	deny := t.deny
	t.mu.Unlock()
	if deny {
		return os.ErrPermission
	}
	t.mu.Lock()
	t.inside = true
	t.mu.Unlock()
	defer func() { t.mu.Lock(); t.inside = false; t.mu.Unlock() }()
	return fn()
}

func (t *ownerToken) isInside() bool { t.mu.Lock(); defer t.mu.Unlock(); return t.inside }

type ownerTokens struct {
	mu     sync.Mutex
	err    error
	deny   bool
	issued []*ownerToken
}

func (p *ownerTokens) ForProfile(sid string) (tokens.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	t := &ownerToken{sid: sid, deny: p.deny}
	p.issued = append(p.issued, t)
	return t, nil
}
func (p *ownerTokens) SessionOnly(sid string) (tokens.Token, error) { return p.ForProfile(sid) }

func (p *ownerTokens) count() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.issued) }
func (p *ownerTokens) last() *ownerToken {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.issued[len(p.issued)-1]
}

func ownerFixture(t *testing.T, owner string) (*Engine, *ownerTokens, config.SyncConfig) {
	t.Helper()
	e, _ := deferralEngine(t, filepath.Join(t.TempDir(), "state.db"))
	p := &ownerTokens{}
	e.SetTokens(p)
	root := t.TempDir()
	for _, n := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := config.SyncConfig{ID: "c1", Name: "c1", OwnerSID: owner, Enabled: true, LocalPath: root, Destination: "/x", Recursive: true, Mode: "manual"}
	addTestConfig(e, c)
	return e, p, c
}

func queued(t *testing.T, e *Engine) int {
	t.Helper()
	items, err := e.st.Items("", 100)
	if err != nil {
		t.Fatal(err)
	}
	return len(items)
}

func TestScanWithoutOwnerIsRefused(t *testing.T) {
	e, p, _ := ownerFixture(t, "")
	n, err := e.Scan("c1", true)
	if !errors.Is(err, ErrNoOwner) || n != 0 {
		t.Fatalf("Scan = %d, %v; want ErrNoOwner", n, err)
	}
	if p.count() != 0 {
		t.Fatal("a token was requested for an unowned folder")
	}
	if queued(t, e) != 0 {
		t.Fatal("files queued without an owner")
	}
	v := e.ConfigViews()
	if len(v) != 1 || !strings.Contains(v[0].LastError, "no owner") {
		t.Fatalf("last error = %+v", v)
	}
}

func TestScanReadsOnlyAsOwner(t *testing.T) {
	e, p, _ := ownerFixture(t, ownerSID)
	old := probeRead
	t.Cleanup(func() { probeRead = old })
	var outside []string
	var mu sync.Mutex
	probeRead = func(abs string) error {
		if p.count() == 0 || !p.last().isInside() {
			mu.Lock()
			outside = append(outside, abs)
			mu.Unlock()
		}
		return nil
	}
	n, err := e.Scan("c1", true)
	if err != nil || n != 2 {
		t.Fatalf("Scan = %d, %v", n, err)
	}
	if len(outside) != 0 {
		t.Fatalf("files probed outside the owner token: %v", outside)
	}
	if p.count() != 1 || p.last().sid != ownerSID || p.last().calls != 1 {
		t.Fatalf("tokens issued = %d", p.count())
	}
	// A second scan reuses the cached token.
	if _, err := e.Scan("c1", true); err != nil {
		t.Fatal(err)
	}
	if p.count() != 1 || p.last().calls != 2 {
		t.Fatalf("token not reused: issued %d, calls %d", p.count(), p.last().calls)
	}
}

func TestScanOwnerDeniedQueuesNothing(t *testing.T) {
	e, p, _ := ownerFixture(t, ownerSID)
	p.deny = true
	if _, err := e.Scan("c1", true); err == nil {
		t.Fatal("scan succeeded although the owner cannot read the folder")
	}
	if queued(t, e) != 0 {
		t.Fatal("files queued with SYSTEM's view")
	}
	p.deny, p.err = false, errors.New("no logon")
	e.SetTokens(p) // drop the cached denying token
	_, err := e.Scan("c1", true)
	if !IsOwnerError(err) {
		t.Fatalf("Scan = %v; want an owner error", err)
	}
}

func TestOwnerTokenRefreshedAfterTTL(t *testing.T) {
	old := ownerTokenTTL
	t.Cleanup(func() { ownerTokenTTL = old })
	ownerTokenTTL = 0
	e, p, _ := ownerFixture(t, ownerSID)
	for i := 0; i < 2; i++ {
		if _, err := e.Scan("c1", true); err != nil {
			t.Fatal(err)
		}
	}
	if p.count() != 2 {
		t.Fatalf("tokens issued = %d, want 2", p.count())
	}
	if !p.issued[0].closed || p.issued[1].closed {
		t.Fatal("expired token not closed, or the live one closed")
	}
	e.SetTokens(nil)
	if !p.issued[1].closed {
		t.Fatal("SetTokens did not close cached tokens")
	}
}

func TestUploadOpensAsOwner(t *testing.T) {
	e, st := deferralEngine(t, filepath.Join(t.TempDir(), "state.db"))
	setTestClient(t, e, "http://127.0.0.1:1")
	p := &ownerTokens{}
	e.SetTokens(p)
	lockedFixture(t, e, st)
	e.mu.Lock()
	e.configs["c1"].OwnerSID = ownerSID
	e.mu.Unlock()

	stubOpen(t, nil)
	var openedInside []bool
	openForUpload = func(name string) (*os.File, error) {
		openedInside = append(openedInside, p.count() > 0 && p.last().isInside())
		return nil, os.ErrPermission
	}
	if !processNext(t, e, st) {
		t.Fatal("item not ready")
	}
	if len(openedInside) != 1 || !openedInside[0] {
		t.Fatalf("open ran inside the owner token = %v", openedInside)
	}

	// Without the owner's token nothing is opened; the item waits.
	openedInside = nil
	p.err = errors.New("no logon")
	e.SetTokens(p)
	if _, err := st.DB.Exec("UPDATE queue SET next_at = 0"); err != nil {
		t.Fatal(err)
	}
	if !processNext(t, e, st) {
		t.Fatal("item not ready")
	}
	if len(openedInside) != 0 {
		t.Fatal("file opened without the owner's token")
	}
	items, _ := st.Items("pending", 10)
	if len(items) != 1 || !strings.Contains(items[0].Error, "owner") {
		t.Fatalf("queue = %+v", items)
	}
}

func TestAutomationStartsOnlySameOwnerTasks(t *testing.T) {
	e, _, from := ownerFixture(t, ownerSID)
	addTestConfig(e, config.SyncConfig{ID: "c2", Name: "Theirs", OwnerSID: "S-1-5-21-1111111111-2222222222-3333333333-1002", Enabled: true, LocalPath: t.TempDir(), Destination: "/y", Mode: "manual"})
	addTestConfig(e, config.SyncConfig{ID: "c3", Name: "Mine", OwnerSID: ownerSID, Enabled: true, LocalPath: t.TempDir(), Destination: "/z", Mode: "manual"})
	e.startTask(&from, "Theirs", 1)
	e.startTask(&from, "Mine", 1)
	e.mu.Lock()
	_, theirs := e.lastStart["c2"]
	_, mine := e.lastStart["c3"]
	e.mu.Unlock()
	if theirs || !mine {
		t.Fatalf("started other user's task = %v, own task = %v", theirs, mine)
	}
}
