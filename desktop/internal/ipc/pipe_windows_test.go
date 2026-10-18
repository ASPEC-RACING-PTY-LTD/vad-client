//go:build windows

package ipc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/agent"
)

func testPipeName(t *testing.T) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return `\\.\pipe\VaultDock.Test.` + hex.EncodeToString(b)
}

func currentUserSID(t *testing.T) string {
	t.Helper()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return u.User.Sid.String()
}

func TestNamedPipe(t *testing.T) {
	name := testPipeName(t)
	l, err := Listen(name)
	if err != nil {
		t.Fatal(err)
	}
	// The name cannot be taken twice (anti-squatting).
	if l2, err := Listen(name); err == nil {
		_ = l2.Close()
		t.Fatal("second listener on the same pipe name succeeded")
	}

	h := startHost(t)
	callers := make(chan Caller, 8)
	srv := NewServer(h, ServerOptions{Authorize: func(ctx context.Context, who Caller, method string) error {
		callers <- who
		return nil
	}})
	served := make(chan error, 1)
	go func() { served <- srv.Serve(l) }()
	t.Cleanup(func() {
		_ = srv.Close()
		if err := <-served; !errors.Is(err, ErrServerClosed) {
			t.Errorf("Serve = %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) { return DialPipe(ctx, name) }})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	b, err := c.Bootstrap(ctx)
	if err != nil || b.ProtocolVersion != agent.ProtocolVersion {
		t.Fatalf("Bootstrap = %+v, %v", b, err)
	}
	who := <-callers
	if who.SID != currentUserSID(t) || who.PID == 0 {
		t.Fatalf("caller = %+v, want SID %s", who, currentUserSID(t))
	}
	var sess uint32
	_ = windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &sess)
	if who.SessionID != sess {
		t.Fatalf("caller session = %d, want %d", who.SessionID, sess)
	}

	sub, err := c.Subscribe(ctx, agent.KindSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	waitEvent(t, sub, agent.KindSnapshot)

	// A test server runs as the user, not LocalSystem.
	conn, err := DialPipe(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireSystemServer(conn); err == nil {
		t.Fatal("RequireSystemServer accepted a user-owned server")
	}
	_ = conn.Close()

	// An anonymous-level client cannot be identified and is refused.
	_, err = Dial(ctx, ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return winio.DialPipeAccessImpLevel(ctx, name, clientAccess, winio.PipeImpLevelAnonymous)
	}})
	if errCode(err) != agent.CodeInvalid {
		t.Fatalf("anonymous client = %v", err)
	}
}
