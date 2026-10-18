package ipc

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/agent"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/tokens"
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

// passToken is a caller token whose impersonation is a no-op, so pipe
// callers in these tests can use temp folders.
type passToken struct{ sid string }

func (t passToken) SID() string                                { return t.sid }
func (passToken) Source() tokens.Source                        { return tokens.SourceSession }
func (passToken) SessionID() (uint32, bool)                    { return 1, true }
func (passToken) Caps() tokens.Caps                            { return tokens.CapsFor(tokens.SourceSession) }
func (passToken) Impersonate(fn func() error) error            { return fn() }
func (passToken) AccessCheckPath(string, uint32) (bool, error) { return true, nil }
func (passToken) Close() error                                 { return nil }

type passTokens struct{}

func (passTokens) ForProfile(sid string) (tokens.Token, error)  { return passToken{sid}, nil }
func (passTokens) SessionOnly(sid string) (tokens.Token, error) { return passToken{sid}, nil }

func startHost(t *testing.T) *agent.Host {
	t.Helper()
	return startHostWith(t, passTokens{})
}

func startHostWith(t *testing.T, tp tokens.Provider) *agent.Host {
	t.Helper()
	h, err := agent.NewHost(agent.Options{DataDir: t.TempDir(), Keys: &memKeys{}, DeviceName: "TEST-PC", OS: "test", Identifier: "test-id", SnapshotInterval: 20 * time.Millisecond,
		Tokens: tp, Guard: func(guard.Op, string, guard.Profile) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

var testCaller = Caller{SID: "S-1-5-21-1-2-3-1001", SessionID: 1, PID: 42}

func stubIdentify(net.Conn) (Caller, error) { return testCaller, nil }

// pipeDialer connects clients to srv over net.Pipe and remembers the
// server ends so tests can cut connections.
type pipeDialer struct {
	srv   *Server
	mu    sync.Mutex
	ends  []net.Conn
	dials atomic.Int32
}

func (d *pipeDialer) dial(ctx context.Context) (net.Conn, error) {
	d.dials.Add(1)
	a, b := net.Pipe()
	d.mu.Lock()
	d.ends = append(d.ends, a)
	d.mu.Unlock()
	go d.srv.ServeConn(a)
	return b, nil
}

func (d *pipeDialer) cutAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.ends {
		_ = c.Close()
	}
	d.ends = nil
}

func newPair(t *testing.T, api agent.API, so ServerOptions) (*Client, *pipeDialer) {
	t.Helper()
	if so.Identify == nil {
		so.Identify = stubIdentify
	}
	srv := NewServer(api, so)
	t.Cleanup(func() { _ = srv.Close() })
	d := &pipeDialer{srv: srv}
	c := NewClient(ClientOptions{Dial: d.dial, MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond})
	t.Cleanup(func() { _ = c.Close() })
	return c, d
}

func errCode(err error) string {
	var ae *agent.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func TestRoundTrips(t *testing.T) {
	h := startHost(t)
	c, _ := newPair(t, h, ServerOptions{})
	ctx := context.Background()

	b, err := c.Bootstrap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b.ProtocolVersion != agent.ProtocolVersion || b.DeviceName != "TEST-PC" {
		t.Fatalf("bootstrap = %+v", b)
	}

	dir := t.TempDir()
	saved, err := c.SaveConfig(ctx, config.SyncConfig{Name: "Docs", Enabled: true, LocalPath: dir, Destination: "/Docs", Recursive: true, Mode: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" {
		t.Fatal("saved config has no id")
	}
	list, err := c.ListConfigs(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListConfigs = %v, %v", list, err)
	}
	if err := c.SyncNow(ctx, "nope"); errCode(err) != agent.CodeNotFound {
		t.Fatalf("SyncNow(unknown) = %v", err)
	}
	if _, err := c.ListQueue(ctx, "bogus"); !errors.Is(err, &agent.Error{Code: agent.CodeInvalid}) {
		t.Fatalf("ListQueue(bogus) = %v", err)
	}
	if n, err := c.RetryFailed(ctx, 0); err != nil || n != 0 {
		t.Fatalf("RetryFailed = %d, %v", n, err)
	}
	if err := c.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	if !h.Engine().Paused() {
		t.Fatal("SetPaused did not reach the engine")
	}
	if err := c.DeleteConfig(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	// Over the pipe the agent never runs the per-user installer.
	if err := c.InstallUpdate(ctx); errCode(err) != agent.CodeUpdatesManaged {
		t.Fatalf("InstallUpdate = %v", err)
	}
}

// The backup methods cross the pipe; a host without the backup stack
// reports it unavailable, and the admin-only first-run calls are refused
// for a standard user by the agent policy.
func TestBackupMethodsOverPipe(t *testing.T) {
	h := startHost(t)
	c, _ := newPair(t, h, ServerOptions{Authorize: agent.Authorize})
	ctx := context.Background()
	ks, err := c.KeyStatus(ctx)
	if err != nil || ks.Available || ks.PinState == "" {
		t.Fatalf("KeyStatus = %+v, %v", ks, err)
	}
	if rd, err := c.Readiness(ctx); err != nil || rd.Ready {
		t.Fatalf("Readiness = %+v, %v", rd, err)
	}
	if _, err := c.ListBackupJobs(ctx); errCode(err) != agent.CodeUnavailable {
		t.Fatalf("ListBackupJobs = %v", err)
	}
	if _, err := c.RunBackup(ctx, 1); errCode(err) != agent.CodeUnavailable {
		t.Fatalf("RunBackup = %v", err)
	}
	if _, err := c.ListIssues(ctx, agent.IssueQuery{JobID: 1}); errCode(err) != agent.CodeUnavailable {
		t.Fatalf("ListIssues = %v", err)
	}
	// Key setup no longer needs an administrator; without a backup stack it
	// is unavailable rather than forbidden.
	if _, err := c.InitRepository(ctx, agent.InitRepositoryRequest{}); errCode(err) != agent.CodeUnavailable {
		t.Fatalf("InitRepository = %v", err)
	}
	if err := c.ConfirmRecoveryKey(ctx, "VDRK1-x"); errCode(err) != agent.CodeUnavailable {
		t.Fatalf("ConfirmRecoveryKey = %v", err)
	}
	if _, err := c.ConfirmServerIdentity(ctx, "AAAA"); errCode(err) != agent.CodeUnavailable {
		t.Fatalf("ConfirmServerIdentity = %v", err)
	}
}

func TestCallerContextAndAuthorize(t *testing.T) {
	h := startHost(t)
	var mu sync.Mutex
	var seen []string
	c, _ := newPair(t, h, ServerOptions{Authorize: func(ctx context.Context, who Caller, method string) error {
		got, ok := CallerFromContext(ctx)
		if !ok || got != who || who != testCaller {
			t.Errorf("caller in ctx = %+v, %v", got, ok)
		}
		mu.Lock()
		seen = append(seen, method)
		mu.Unlock()
		if method == "Disconnect" {
			return &agent.Error{Code: agent.CodeInvalid, Message: "administrators only"}
		}
		return nil
	}})
	ctx := context.Background()
	if _, err := c.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Disconnect(ctx); err == nil || !strings.Contains(err.Error(), "administrators only") {
		t.Fatalf("Disconnect = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(seen, ",") != "Snapshot,Disconnect" {
		t.Fatalf("authorised methods = %v", seen)
	}
}

func waitEvent(t *testing.T, s agent.Subscription, kind agent.EventKind) agent.Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-s.C():
			if !ok {
				t.Fatal("subscription closed")
			}
			if ev.Kind == kind {
				return ev
			}
		case <-timeout:
			t.Fatalf("no %s event", kind)
		}
	}
}

func TestEvents(t *testing.T) {
	h := startHost(t)
	c, _ := newPair(t, h, ServerOptions{})
	ctx := context.Background()

	snap, err := c.Subscribe(ctx, agent.KindSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	waitEvent(t, snap, agent.KindSnapshot)
	snap.Close()
	if _, ok := <-snap.C(); ok {
		// drain: a buffered event may precede the close
		for range snap.C() {
		}
	}

	set, err := c.Subscribe(ctx, agent.KindSettings)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.StartMinimized = !s.StartMinimized
	if _, err := c.SaveSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, set, agent.KindSettings)
	var got config.Settings
	if err := ev.Decode(&got); err != nil || got.StartMinimized != s.StartMinimized {
		t.Fatalf("settings event = %+v, %v", got, err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSubscriptionsEndWithConnection(t *testing.T) {
	h := startHost(t)
	c, _ := newPair(t, h, ServerOptions{})
	if _, err := c.Subscribe(context.Background(), agent.KindSettings); err != nil {
		t.Fatal(err)
	}
	eventually(t, "server subscription", func() bool { return h.Bus().HasSubscribers(agent.KindSettings) })
	_ = c.Close()
	eventually(t, "subscription cancelled", func() bool { return !h.Bus().HasSubscribers(agent.KindSettings) })

	// Ctx-bound subscription.
	c2, _ := newPair(t, h, ServerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	sub, err := c2.Subscribe(ctx, agent.KindSettings)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "server subscription", func() bool { return h.Bus().HasSubscribers(agent.KindSettings) })
	cancel()
	eventually(t, "unsubscribe", func() bool { return !h.Bus().HasSubscribers(agent.KindSettings) })
	for range sub.C() {
	}
}

func TestReconnectResubscribes(t *testing.T) {
	h := startHost(t)
	c, d := newPair(t, h, ServerOptions{})
	ctx := context.Background()
	sub, err := c.Subscribe(ctx, agent.KindSettings)
	if err != nil {
		t.Fatal(err)
	}
	d.cutAll()
	eventually(t, "redial", func() bool { return d.dials.Load() >= 2 })
	eventually(t, "resubscribe", func() bool { return h.Bus().HasSubscribers(agent.KindSettings) })

	s, err := c.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.CloseToTray = !s.CloseToTray
	if _, err := c.SaveSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, sub, agent.KindSettings)
}

func TestUnreachableBackoff(t *testing.T) {
	var dials atomic.Int32
	c := NewClient(ClientOptions{
		Dial:       func(context.Context) (net.Conn, error) { dials.Add(1); return nil, errors.New("no agent") },
		MinBackoff: time.Hour,
	})
	defer c.Close()
	ctx := context.Background()
	if _, err := c.Snapshot(ctx); errCode(err) != agent.CodeUnavailable {
		t.Fatalf("first call = %v", err)
	}
	if _, err := c.Snapshot(ctx); errCode(err) != agent.CodeUnavailable {
		t.Fatalf("second call = %v", err)
	}
	if n := dials.Load(); n != 1 {
		t.Fatalf("dials during backoff = %d, want 1", n)
	}
	if _, err := Dial(ctx, ClientOptions{Dial: func(context.Context) (net.Conn, error) { return nil, errors.New("x") }}); errCode(err) != agent.CodeUnavailable {
		t.Fatalf("Dial = %v", err)
	}
}

func rawHandshake(t *testing.T, srv *Server, version int) (net.Conn, *Frame) {
	t.Helper()
	a, b := net.Pipe()
	go srv.ServeConn(a)
	_ = b.SetDeadline(time.Now().Add(5 * time.Second))
	if err := writeFrame(b, &Frame{Type: frameHello, Version: version}, DefaultMaxFrame); err != nil {
		t.Fatal(err)
	}
	w, err := readFrame(b, DefaultMaxFrame)
	if err != nil {
		t.Fatal(err)
	}
	return b, w
}

func TestVersionMismatch(t *testing.T) {
	h := startHost(t)
	srv := NewServer(h, ServerOptions{Identify: stubIdentify})
	defer srv.Close()
	conn, w := rawHandshake(t, srv, agent.ProtocolVersion+1)
	defer conn.Close()
	if w.Type != frameWelcome || w.Error == nil || w.Error.Code != agent.CodeInvalid || !strings.Contains(w.Error.Message, "version") {
		t.Fatalf("welcome = %+v", w)
	}
	if _, err := readFrame(conn, DefaultMaxFrame); err == nil {
		t.Fatal("connection stayed open after mismatch")
	}

	// Unidentifiable callers are refused too.
	srv2 := NewServer(h, ServerOptions{Identify: func(net.Conn) (Caller, error) { return Caller{}, errors.New("anonymous") }})
	defer srv2.Close()
	conn2, w2 := rawHandshake(t, srv2, agent.ProtocolVersion)
	defer conn2.Close()
	if w2.Error == nil {
		t.Fatal("unidentified caller accepted")
	}
}

func TestFrameLimits(t *testing.T) {
	// Codec.
	var buf bytes.Buffer
	if err := writeFrame(&buf, &Frame{Type: frameReq, Method: strings.Repeat("x", 100)}, 64); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized write = %v", err)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 1<<30)
	if _, err := readFrame(bytes.NewReader(hdr[:]), DefaultMaxFrame); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized read = %v", err)
	}
	binary.BigEndian.PutUint32(hdr[:], 0)
	if _, err := readFrame(bytes.NewReader(hdr[:]), DefaultMaxFrame); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("empty frame = %v", err)
	}

	// Server drops a connection that announces an oversized frame.
	h := startHost(t)
	srv := NewServer(h, ServerOptions{Identify: stubIdentify, MaxFrame: 4096})
	defer srv.Close()
	conn, w := rawHandshake(t, srv, agent.ProtocolVersion)
	defer conn.Close()
	if w.Error != nil {
		t.Fatal(w.Error)
	}
	binary.BigEndian.PutUint32(hdr[:], 1<<20)
	if _, err := conn.Write(hdr[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil || !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("server kept oversized connection: %v", err)
	}

	// Client refuses to send an oversized request; the connection survives.
	c, _ := newPair(t, h, ServerOptions{MaxFrame: 4096})
	c.opts.MaxFrame = 4096
	_, err := c.ProbeServer(context.Background(), agent.ProbeRequest{URL: strings.Repeat("a", 8192)})
	if errCode(err) != agent.CodeInvalid {
		t.Fatalf("oversized request = %v", err)
	}
	if _, err := c.Snapshot(context.Background()); err != nil {
		t.Fatalf("connection unusable after refused request: %v", err)
	}
}

func TestCanceledCall(t *testing.T) {
	h := startHost(t)
	c, _ := newPair(t, h, ServerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Snapshot(ctx); errCode(err) != agent.CodeCanceled {
		t.Fatalf("canceled call = %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Snapshot(context.Background()); !errors.Is(err, agent.ErrClosed) {
		t.Fatalf("after Close = %v", err)
	}
}

// A pipe user whose token cannot be obtained never gets SYSTEM's view of a
// path: the refusal survives the transport as access_denied.
func TestPathCallsFailClosedWithoutCallerToken(t *testing.T) {
	h := startHostWith(t, tokens.Unsupported{})
	c, _ := newPair(t, h, ServerOptions{})
	ctx := context.Background()
	cfg := config.SyncConfig{Name: "Docs", Enabled: true, LocalPath: t.TempDir(), Destination: "/Docs", Recursive: true, Mode: "manual"}
	if _, err := c.PreviewConfig(ctx, cfg); !errors.Is(err, agent.ErrAccessDenied) {
		t.Fatalf("PreviewConfig = %v, want access_denied", err)
	}
	if _, err := c.SaveConfig(ctx, cfg); errCode(err) != agent.CodeAccessDenied {
		t.Fatalf("SaveConfig = %v, want access_denied", err)
	}
}
