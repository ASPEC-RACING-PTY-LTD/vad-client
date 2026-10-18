package engine

import (
	"net/url"
	"path/filepath"
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
)

// A reconnect after a revocation clears the sticky "revoked" status.
func TestSetClientClearsRevoked(t *testing.T) {
	e, _ := deferralEngine(t, filepath.Join(t.TempDir(), "state.db"))
	e.revoked("device revoked")
	if st, _ := e.statusMsg(); st != "revoked" {
		t.Fatalf("status after revoke = %q", st)
	}
	u, _ := url.Parse("http://127.0.0.1:1")
	e.SetClient(client.New(u, client.Options{}), nil)
	if st, msg := e.statusMsg(); st == "revoked" || msg == "This device's access was revoked. Reconnect to continue." {
		t.Fatalf("status after reconnect = %q (%q)", st, msg)
	}
}

func (e *Engine) statusMsg() (string, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status, e.message
}
