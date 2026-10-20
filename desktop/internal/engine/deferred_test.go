package engine

import (
	"path/filepath"
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/transfer"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// After a big sync, a file the server did not store is queued again, and
// fails with the server's reason once it has used its attempts.
func TestDeferredOutcomes(t *testing.T) {
	e, st := deferralEngine(t, filepath.Join(t.TempDir(), "state.db"))
	item := func(rel string, attempts int) state.Item {
		return state.Item{ConfigID: "c", RelPath: rel, Size: 10, MTime: 1, RemoteDir: "/Clips", RemoteName: rel, Attempts: attempts}
	}
	sent := func(it state.Item) state.Deferred {
		d := state.Deferred{UploadID: "up-" + it.RelPath, SHA256: "abc", Item: it}
		if err := st.AddDeferred(d); err != nil {
			t.Fatal(err)
		}
		return d
	}

	ok := sent(item("ok.mp4", 0))
	e.deferredOutcome(ok, wire.UploadOutcome{Status: wire.UploadDone, Result: "created"})
	if f, _ := st.File("c", "ok.mp4"); f == nil || f.Status != "synced" {
		t.Fatalf("done: %+v", f)
	}

	wait := sent(item("wait.mp4", 0))
	e.deferredOutcome(wait, wire.UploadOutcome{Status: wire.UploadPending})
	if n := st.DeferredCount("c"); n != 1 {
		t.Fatalf("pending upload forgotten: %d waiting", n)
	}

	lost := sent(item("lost.mp4", 0))
	e.deferredOutcome(lost, wire.UploadOutcome{Status: wire.UploadMissing})
	q, _ := st.Items("pending", 10)
	if len(q) != 1 || q[0].RelPath != "lost.mp4" || q[0].Attempts != 1 {
		t.Fatalf("missing file not queued again: %+v", q)
	}

	last := sent(item("broken.mp4", transfer.MaxAttempts-1))
	e.deferredOutcome(last, wire.UploadOutcome{Status: wire.UploadFailed, Error: "disk full"})
	failed, _ := st.Items("failed", 10)
	if len(failed) != 1 || failed[0].RelPath != "broken.mp4" {
		t.Fatalf("not failed after its attempts: %+v", failed)
	}
	if f, _ := st.File("c", "broken.mp4"); f == nil || f.Status != "failed" {
		t.Fatalf("index after failure: %+v", f)
	}
	if n := st.DeferredCount(""); n != 1 {
		t.Fatalf("waiting uploads = %d, want only the pending one", n)
	}
}
