package engine

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// Pausing stops an upload in progress and puts the file back in the queue.
func TestPauseCancelsActiveUpload(t *testing.T) {
	e, st := deferralEngine(t, filepath.Join(t.TempDir(), "state.db"))
	reached := make(chan struct{}, 1)
	quit := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case reached <- struct{}{}:
		default:
		}
		select { // the server never answers: only a cancel ends it
		case <-r.Context().Done():
		case <-quit:
		}
	}))
	defer srv.Close()
	defer close(quit)
	setTestClient(t, e, srv.URL)
	lockedFixture(t, e, st)
	stubOpen(t, nil)

	done := make(chan struct{})
	go func() {
		e.dispatch()
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("upload never reached the server")
	}
	e.SetPaused(true)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("pause did not stop the active upload")
	}
	items, _ := st.Items("pending", 10)
	if len(items) != 1 {
		t.Fatalf("pending after pause = %d, want the file requeued", len(items))
	}
	if items[0].Error != "" {
		t.Fatalf("requeued with error %q", items[0].Error)
	}
}
