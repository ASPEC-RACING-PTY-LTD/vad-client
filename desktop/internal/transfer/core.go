// Package transfer is the reusable upload core shared by the sync engine and
// (later) the backup engine and agent service: resumable chunked upload
// sessions, adaptive request concurrency, bandwidth limiting, retry/backoff
// classification and byte-level progress reporting.
//
// One Core is shared by every queue so that the concurrency controller and
// the bandwidth limit apply to the device as a whole.
package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
)

const (
	// MaxChunkParallelPerFile bounds the parallel chunk requests of one file.
	// With priorities the first file can use this many connections before
	// the next file gets any.
	MaxChunkParallelPerFile = 8
	// chunkAttempts is how often one chunk is tried within an upload.
	chunkAttempts = 5
)

// Settings are the user-tunable transfer settings.
type Settings struct {
	MaxParallel    int // automatic upper bound on parallel requests
	ManualParallel int // fixed parallel requests; 0 = adaptive
	BandwidthKBps  int // upload limit in KiB/s; 0 = unlimited
}

// Progress receives progress of one upload. Both fields are optional.
type Progress struct {
	// Sent counts bytes handed to the transport, including bytes the server
	// already had when a session is resumed. Bytes of failed chunk requests
	// are subtracted again.
	Sent *atomic.Int64
	// Phase is called with "uploading" and "finalizing".
	Phase func(string)
	// Compress sends chunks zstd-compressed where that saves bytes
	// (upload.zstd). A server without support is detected on the first
	// refused chunk and the rest go raw.
	Compress bool
	// Priority orders this upload's chunk requests against other uploads:
	// lower values get free connections first (the queue order).
	Priority int64
	// Session, if set, receives the server's upload session id.
	Session func(id string)
	// FinishLater asks the server to finish the upload without waiting for
	// it (upload.deferred_finish). The result then has Status "finalizing"
	// unless the server was already done; the outcome is checked later.
	FinishLater bool
}

func (p Progress) phase(s string) {
	if p.Phase != nil {
		p.Phase(s)
	}
}

// Core owns the shared transfer resources.
type Core struct {
	ctrl    *Controller
	limiter Limiter
	hashSem chan struct{}
	// noZstdUntil (unix ms): the server refused a compressed chunk (no
	// upload.zstd), so chunks go raw until then. It expires so a server that
	// is upgraded later is used compressed without restarting the agent.
	noZstdUntil atomic.Int64
	zstdLogged  atomic.Bool
}

// zstdRetry is how long chunks go raw after a server refused compression.
const zstdRetry = 10 * time.Minute

func (c *Core) zstdOff() bool { return time.Now().UnixMilli() < c.noZstdUntil.Load() }

// New returns a core with an automatic maximum of maxParallel parallel
// requests and at most hashParallel concurrent whole-file hashes (bounded to
// avoid disk contention on HDDs).
func New(maxParallel, hashParallel int) *Core {
	return &Core{ctrl: NewController(maxParallel), hashSem: make(chan struct{}, max(1, hashParallel))}
}

// Controller returns the adaptive concurrency controller.
func (c *Core) Controller() *Controller { return c.ctrl }

// Limit returns the current parallel request limit.
func (c *Core) Limit() int { return c.ctrl.Limit() }

// Inflight returns the number of chunk requests in progress.
func (c *Core) Inflight() int { return c.ctrl.Inflight() }

// Tick evaluates the controller window; call it about every 2 seconds.
func (c *Core) Tick(now time.Time) { c.ctrl.Tick(now) }

// ApplySettings updates the concurrency bounds and the bandwidth limit.
func (c *Core) ApplySettings(s Settings) {
	c.ctrl.SetBounds(s.MaxParallel, s.ManualParallel)
	c.limiter.SetKBps(s.BandwidthKBps)
}

// Attach routes cl's chunk bodies through the bandwidth limiter and caps the
// concurrency at the server's advertised capacity (0 = not advertised).
func (c *Core) Attach(cl *client.Client, serverMaxChunks int) {
	cl.ChunkBody = c.limiter.Wrap
	if serverMaxChunks > 0 {
		c.ctrl.CapMax(serverMaxChunks)
	}
}

// WrapBody throttles r with the shared bandwidth limit.
func (c *Core) WrapBody(r io.Reader) io.Reader { return c.limiter.Wrap(r) }

// Hash returns the hex SHA-256 of the first size bytes of r. It waits for a
// hashing slot first and returns ctx.Err() if cancelled.
func (c *Core) Hash(ctx context.Context, r io.ReaderAt, size int64) (string, error) {
	select {
	case c.hashSem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-c.hashSem }()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	sr := io.NewSectionReader(r, 0, size)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := sr.Read(buf)
		h.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Upload runs one resumable upload: it opens (or resumes) the server session
// for req, sends the missing chunks of r and completes the session. If the
// server already has the content the result is returned without sending.
// Errors are *StageError values wrapping the client error.
func (c *Core) Upload(ctx context.Context, cl *client.Client, req client.UploadRequest, r io.ReaderAt, p Progress) (*client.UploadResult, error) {
	p.phase("uploading")
	res, err := cl.CreateUpload(ctx, req)
	if err != nil {
		return nil, &StageError{Stage: "start upload", Err: err}
	}
	if res.Status == "complete" {
		return res, nil
	}
	up := res.Upload
	if p.Session != nil {
		p.Session(up.ID)
	}
	if p.Sent != nil {
		p.Sent.Store(up.BytesReceived)
	}
	if err := c.uploadChunks(ctx, cl, up, r, p.Sent, p.Compress, p.Priority); err != nil {
		return nil, &StageError{Stage: "upload", Err: err}
	}
	p.phase("finalizing")
	if p.FinishLater {
		res, err = cl.FinishLater(ctx, up.ID)
	} else {
		res, err = cl.CompleteUpload(ctx, up.ID)
	}
	if err != nil {
		return nil, &StageError{Stage: "complete", Err: err}
	}
	return res, nil
}

// UploadChunks sends the chunks of up that the server does not have yet,
// with bounded per-file parallelism under the global adaptive concurrency.
// sent (optional) receives byte progress.
func (c *Core) UploadChunks(ctx context.Context, cl *client.Client, up *client.UploadSession, r io.ReaderAt, sent *atomic.Int64) error {
	return c.uploadChunks(ctx, cl, up, r, sent, false, 0)
}

func (c *Core) uploadChunks(ctx context.Context, cl *client.Client, up *client.UploadSession, r io.ReaderAt, sent *atomic.Int64, compress bool, prio int64) error {
	missing := up.Missing()
	if len(missing) == 0 {
		return nil
	}
	if sent == nil {
		sent = new(atomic.Int64)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int64, len(missing))
	for _, idx := range missing {
		jobs <- idx
	}
	close(jobs)
	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)
	for w := 0; w < min(MaxChunkParallelPerFile, len(missing)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := c.sendChunk(ctx, cl, up, r, idx, sent, compress, prio); err != nil {
					errOnce.Do(func() { firstErr = err })
					cancel()
					return
				}
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// progressReaderAt reports bytes as they are read by the HTTP transport.
type progressReaderAt struct {
	r            io.ReaderAt
	total, local *atomic.Int64
}

func (p progressReaderAt) ReadAt(b []byte, off int64) (int, error) {
	n, err := p.r.ReadAt(b, off)
	p.total.Add(int64(n))
	p.local.Add(int64(n))
	return n, err
}

func (c *Core) sendChunk(ctx context.Context, cl *client.Client, up *client.UploadSession, r io.ReaderAt, idx int64, sent *atomic.Int64, compress bool, prio int64) error {
	off, n := idx*up.ChunkSize, up.ChunkLen(idx)
	sum, err := client.HashSection(r, off, n)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		if err := c.ctrl.AcquirePriority(ctx, prio); err != nil {
			return err
		}
		var local atomic.Int64
		start := time.Now()
		var err error
		zipped := false
		if compress && !c.zstdOff() {
			zipped, err = cl.PutChunkCompressed(ctx, up.ID, idx, r, off, n, sum)
			if zipped && err != nil && unsupportedEncoding(err) {
				// A server without upload.zstd: send this and later chunks raw.
				if c.noZstdUntil.Swap(time.Now().Add(zstdRetry).UnixMilli()) == 0 {
					slog.Warn("server does not accept compressed uploads (upload.zstd); sending uncompressed", "err", err)
				}
				zipped, err = false, nil
			}
			if zipped && err == nil {
				if !c.zstdLogged.Swap(true) {
					slog.Info("sending compressed upload chunks", "upload", up.ID)
				}
				local.Store(n) // progress counts raw bytes
				sent.Add(n)
			}
		}
		if !zipped {
			err = cl.PutChunkHashed(ctx, up.ID, idx, progressReaderAt{r: r, total: sent, local: &local}, off, n, sum)
		}
		lat := time.Since(start)
		var ok int64
		if err == nil {
			ok = n
		}
		c.ctrl.Release(ok, lat, err != nil && IsCongestion(err))
		if err == nil {
			sent.Add(n - local.Load())
			return nil
		}
		sent.Add(-local.Load())
		if errors.Is(err, context.Canceled) || !client.Retryable(err) || attempt >= chunkAttempts-1 {
			return err
		}
		wait := time.Duration(1<<attempt) * 500 * time.Millisecond
		var ae *client.APIError
		if errors.As(err, &ae) && ae.RetryAfter > 0 {
			wait = ae.RetryAfter
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// unsupportedEncoding: a server without upload.zstd rejects a compressed
// chunk by its length (400 bad_length) or the encoding (415).
func unsupportedEncoding(err error) bool {
	var ae *client.APIError
	return errors.As(err, &ae) && (ae.Status == 415 || (ae.Status == 400 && ae.Code == "bad_length"))
}
