package outbox

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/state"
)

type sent struct{ kind, key, body string }

type fakeSender struct {
	mu   sync.Mutex
	got  []sent
	fail error
}

func (f *fakeSender) send(ctx context.Context, kind string, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.got = append(f.got, sent{kind, KeyFromContext(ctx), string(payload)})
	return nil
}

func (f *fakeSender) bodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.got {
		out = append(out, s.body)
	}
	return out
}

func openStore(t *testing.T, path string) *state.Store {
	t.Helper()
	st, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newTest(t *testing.T, opt Options) (*Outbox, *fakeSender, *time.Time) {
	t.Helper()
	st := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	fs := &fakeSender{}
	o := New(st.DB, fs.send, opt)
	now := time.UnixMilli(1_700_000_000_000)
	o.now = func() time.Time { return now }
	return o, fs, &now
}

func mustLen(t *testing.T, o *Outbox, want int) {
	t.Helper()
	if n, err := o.Len(); err != nil || n != want {
		t.Fatalf("Len = %d, %v; want %d", n, err, want)
	}
}

func TestEnqueueFlushInOrderWithKeys(t *testing.T) {
	o, fs, _ := newTest(t, Options{Batch: 2})
	for _, b := range []string{"a", "b", "c"} {
		if err := o.Enqueue("transfers", "k-"+b, []byte(b)); err != nil {
			t.Fatal(err)
		}
	}
	// Same idempotency key: ignored.
	if err := o.Enqueue("transfers", "k-a", []byte("dup")); err != nil {
		t.Fatal(err)
	}
	mustLen(t, o, 3)
	n, err := o.Flush(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("Flush = %d, %v", n, err)
	}
	mustLen(t, o, 0)
	if got := fs.bodies(); len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("delivered %v", got)
	}
	if fs.got[0].key != "k-a" || fs.got[0].kind != "transfers" {
		t.Fatalf("first item %+v", fs.got[0])
	}
	if err := o.Enqueue("transfers", "", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if k := fs.got[3].key; len(k) != 32 {
		t.Fatalf("generated key %q", k)
	}
}

func TestRetryWithBackoff(t *testing.T) {
	o, fs, now := newTest(t, Options{BaseDelay: time.Second, MaxDelay: 4 * time.Second})
	_ = o.Enqueue("transfers", "k1", []byte("a"))
	fs.fail = errors.New("server down")
	for i, wantDelay := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second} {
		if n, err := o.Flush(context.Background()); err == nil || n != 0 {
			t.Fatalf("attempt %d: Flush = %d, %v; want error", i, n, err)
		}
		var attempts int
		var next int64
		var last string
		_ = o.db.QueryRow("SELECT attempts, next_at, last_error FROM outbox").Scan(&attempts, &next, &last)
		if attempts != i+1 || next != now.Add(wantDelay).UnixMilli() || last != "server down" {
			t.Fatalf("attempt %d: attempts=%d next=%d last=%q", i, attempts, next-now.UnixMilli(), last)
		}
		// Not ready yet: nothing is tried.
		if n, err := o.Flush(context.Background()); err != nil || n != 0 {
			t.Fatalf("early Flush = %d, %v", n, err)
		}
		*now = now.Add(wantDelay)
	}
	fs.fail = nil
	if n, err := o.Flush(context.Background()); err != nil || n != 1 {
		t.Fatalf("Flush = %d, %v", n, err)
	}
	mustLen(t, o, 0)
}

func TestPermanentErrorDrops(t *testing.T) {
	var drops []string
	o, fs, _ := newTest(t, Options{OnDrop: func(reason, kind string, n int) { drops = append(drops, reason+"/"+kind) }})
	_ = o.Enqueue("issues", "k1", []byte("a"))
	fs.fail = Permanent(errors.New("bad request"))
	if _, err := o.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustLen(t, o, 0)
	if len(drops) != 1 || drops[0] != DropRejected+"/issues" {
		t.Fatalf("drops %v", drops)
	}
	if !IsPermanent(fs.fail) || IsPermanent(errors.New("x")) || Permanent(nil) != nil {
		t.Fatal("IsPermanent/Permanent")
	}
}

func TestOverflowDropsOldest(t *testing.T) {
	dropped := 0
	o, fs, _ := newTest(t, Options{MaxItems: 3, OnDrop: func(reason, _ string, n int) {
		if reason == DropOverflow {
			dropped += n
		}
	}})
	for _, b := range []string{"1", "2", "3", "4", "5"} {
		if err := o.Enqueue("transfers", "", []byte(b)); err != nil {
			t.Fatal(err)
		}
	}
	mustLen(t, o, 3)
	if dropped != 2 {
		t.Fatalf("dropped %d", dropped)
	}
	_, _ = o.Flush(context.Background())
	if got := fs.bodies(); len(got) != 3 || got[0] != "3" || got[2] != "5" {
		t.Fatalf("delivered %v", got)
	}
}

func TestDurableAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := New(st.DB, nil, Options{}).Enqueue("transfers", "k1", []byte("a")); err != nil {
		t.Fatal(err)
	}
	st.Close()
	fs := &fakeSender{}
	o := New(openStore(t, path).DB, fs.send, Options{})
	if n, err := o.Flush(context.Background()); err != nil || n != 1 || fs.got[0].key != "k1" {
		t.Fatalf("Flush = %d, %v, %+v", n, err, fs.got)
	}
}

func TestCancelledSendNotCounted(t *testing.T) {
	o, _, _ := newTest(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	o.send = func(context.Context, string, []byte) error { cancel(); return context.Canceled }
	_ = o.Enqueue("transfers", "k1", []byte("a"))
	if _, err := o.Flush(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Flush err %v", err)
	}
	var attempts int
	_ = o.db.QueryRow("SELECT attempts FROM outbox").Scan(&attempts)
	if attempts != 0 {
		t.Fatalf("attempts %d", attempts)
	}
}

func TestRunDeliversOnKick(t *testing.T) {
	st := openStore(t, filepath.Join(t.TempDir(), "state.db"))
	got := make(chan string, 1)
	o := New(st.DB, func(_ context.Context, _ string, p []byte) error { got <- string(p); return nil }, Options{Interval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { o.Run(ctx); close(done) }()
	_ = o.Enqueue("transfers", "", []byte("hello"))
	select {
	case p := <-got:
		if p != "hello" {
			t.Fatalf("got %q", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("not delivered")
	}
	cancel()
	<-done
}
