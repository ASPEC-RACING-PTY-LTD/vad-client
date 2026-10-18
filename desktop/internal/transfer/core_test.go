package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
)

// fakeServer implements the upload session endpoints the core uses.
type fakeServer struct {
	mu        sync.Mutex
	data      []byte
	chunk     int64
	received  map[int64][]byte
	failOnce  map[int64]bool // chunk -> respond 503 once
	puts      int
	completed bool
	createRes string // "complete" to short-circuit
}

func (f *fakeServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		total := (int64(len(f.data)) + f.chunk - 1) / f.chunk
		session := func() client.UploadSession {
			s := client.UploadSession{ID: "u1", Size: int64(len(f.data)), ChunkSize: f.chunk, ChunksTotal: total, Status: "open"}
			for i := range f.received {
				s.Received = append(s.Received, i)
				s.BytesReceived += int64(len(f.received[i]))
			}
			return s
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/uploads":
			if f.createRes == "complete" {
				_ = json.NewEncoder(w).Encode(client.UploadResult{Status: "complete", Result: "deduplicated"})
				return
			}
			s := session()
			_ = json.NewEncoder(w).Encode(client.UploadResult{Status: "open", Upload: &s})
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/v1/uploads/u1/chunks/"):
			var idx int64
			for _, c := range strings.TrimPrefix(r.URL.Path, "/api/v1/uploads/u1/chunks/") {
				idx = idx*10 + int64(c-'0')
			}
			body, _ := io.ReadAll(r.Body)
			f.puts++
			if f.failOnce[idx] {
				delete(f.failOnce, idx)
				w.WriteHeader(503)
				return
			}
			sum := sha256.Sum256(body)
			if hex.EncodeToString(sum[:]) != r.Header.Get("X-Chunk-SHA256") {
				t.Errorf("chunk %d hash header mismatch", idx)
			}
			f.received[idx] = body
		case r.Method == "POST" && r.URL.Path == "/api/v1/uploads/u1/complete":
			var all []byte
			for i := int64(0); i < total; i++ {
				all = append(all, f.received[i]...)
			}
			if !bytes.Equal(all, f.data) {
				w.WriteHeader(409)
				_, _ = w.Write([]byte(`{"error":{"code":"hash_mismatch","message":"x"}}`))
				return
			}
			f.completed = true
			_ = json.NewEncoder(w).Encode(client.UploadResult{Status: "complete", Result: "created"})
		default:
			w.WriteHeader(404)
		}
	})
}

func newFake(t *testing.T, f *fakeServer) *client.Client {
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return client.New(u, client.Options{})
}

func TestUploadResumesAndRetries(t *testing.T) {
	data := bytes.Repeat([]byte("vaultdock"), 1000) // 9000 bytes
	f := &fakeServer{data: data, chunk: 2048, received: map[int64][]byte{0: data[:2048]}, failOnce: map[int64]bool{2: true}}
	cl := newFake(t, f)
	core := New(8, 1)
	core.Attach(cl, 0)
	var sent atomic.Int64
	var phases []string
	res, err := core.Upload(context.Background(), cl, client.UploadRequest{Path: "/a", Size: int64(len(data))}, bytes.NewReader(data), Progress{Sent: &sent, Phase: func(p string) { phases = append(phases, p) }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != "created" || !f.completed {
		t.Fatalf("unexpected result %+v", res)
	}
	if sent.Load() != int64(len(data)) {
		t.Fatalf("progress %d, want %d", sent.Load(), len(data))
	}
	// 4 missing chunks + 1 retried; the resumed chunk 0 is not sent again.
	if f.puts != 5 {
		t.Fatalf("chunk requests = %d, want 5", f.puts)
	}
	if strings.Join(phases, ",") != "uploading,finalizing" {
		t.Fatalf("phases = %v", phases)
	}
	if core.Inflight() != 0 {
		t.Fatalf("inflight leaked: %d", core.Inflight())
	}
}

func TestUploadAlreadyComplete(t *testing.T) {
	f := &fakeServer{data: []byte("x"), chunk: 1, received: map[int64][]byte{}, createRes: "complete"}
	cl := newFake(t, f)
	res, err := New(4, 1).Upload(context.Background(), cl, client.UploadRequest{Path: "/a", Size: 1}, bytes.NewReader([]byte("x")), Progress{})
	if err != nil || res.Result != "deduplicated" || f.puts != 0 {
		t.Fatalf("res=%+v err=%v puts=%d", res, err, f.puts)
	}
}

func TestUploadStageError(t *testing.T) {
	data := []byte("hello world")
	f := &fakeServer{data: []byte("HELLO WORLD"), chunk: 4, received: map[int64][]byte{}}
	cl := newFake(t, f)
	_, err := New(4, 1).Upload(context.Background(), cl, client.UploadRequest{Path: "/a", Size: int64(len(data))}, bytes.NewReader(data), Progress{})
	var se *StageError
	if !errors.As(err, &se) || se.Stage != "complete" || !client.IsCode(err, "hash_mismatch") {
		t.Fatalf("err = %v", err)
	}
}

func TestHash(t *testing.T) {
	data := bytes.Repeat([]byte{1, 2, 3}, 1<<19)
	want := sha256.Sum256(data)
	got, err := New(1, 1).Hash(context.Background(), bytes.NewReader(data), int64(len(data)))
	if err != nil || got != hex.EncodeToString(want[:]) {
		t.Fatalf("hash = %s, %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(1, 1).Hash(ctx, bytes.NewReader(data), int64(len(data))); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled hash err = %v", err)
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		congestion bool
		net        bool
	}{
		{"429", &client.APIError{Status: 429}, true, false},
		{"503", &client.APIError{Status: 503}, true, false},
		{"400", &client.APIError{Status: 400}, false, false},
		{"eof", io.ErrUnexpectedEOF, false, true},
	}
	for _, tt := range tests {
		if got := IsCongestion(tt.err); got != tt.congestion {
			t.Errorf("%s: IsCongestion = %v", tt.name, got)
		}
		if got := IsNetErr(tt.err); got != tt.net {
			t.Errorf("%s: IsNetErr = %v", tt.name, got)
		}
	}
	for a := 0; a < 15; a++ {
		d := Backoff(a)
		if d < 5*time.Second || d > 36*time.Minute {
			t.Errorf("Backoff(%d) = %v", a, d)
		}
	}
}

func TestLimiter(t *testing.T) {
	var l Limiter
	data := bytes.Repeat([]byte("a"), 300<<10)
	start := time.Now()
	if n, _ := io.Copy(io.Discard, l.Wrap(bytes.NewReader(data))); n != int64(len(data)) {
		t.Fatalf("copied %d", n)
	}
	if time.Since(start) > time.Second {
		t.Fatal("unlimited reader was throttled")
	}
	l.SetKBps(256) // burst 256 KiB, so 300 KiB takes ~0.17s
	start = time.Now()
	if n, _ := io.Copy(io.Discard, l.Wrap(bytes.NewReader(data))); n != int64(len(data)) {
		t.Fatalf("copied %d", n)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Fatal("limited reader was not throttled")
	}
	l.SetKBps(0)
	if l.current() != nil {
		t.Fatal("limit not removed")
	}
}
