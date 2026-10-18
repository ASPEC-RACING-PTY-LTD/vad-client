package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/client"
)

const (
	maxChunkParallelPerFile = 4
	maxAttempts             = 12
)

func (e *Engine) dispatchLoop() {
	defer e.wg.Done()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-e.wake:
		case <-timer.C:
		}
		e.dispatch()
		wait := time.Second
		if next := e.st.NextWake(); next > 0 {
			if d := time.Until(time.UnixMilli(next)); d > 0 && d < wait {
				wait = d
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(max(50*time.Millisecond, wait))
	}
}

func (e *Engine) dispatch() {
	e.mu.Lock()
	c := e.client
	paused := e.paused
	offline := time.Now().Before(e.offlineUntil)
	activeN := len(e.active)
	exclude := map[string]bool{}
	for id, cfg := range e.configs {
		if !cfg.Enabled {
			exclude[id] = true
		}
	}
	e.mu.Unlock()
	switch {
	case c == nil:
		e.setStatusIf("offline", "Not connected to a VaultDock server")
		return
	case paused:
		e.setStatusIf("paused", "Synchronisation is paused")
		return
	case offline:
		return
	}
	// Allow a couple of extra files in flight so hashing overlaps uploads.
	capacity := min(e.ctrl.Limit()+2, absoluteMax) - activeN
	if capacity <= 0 {
		return
	}
	items, err := e.st.ClaimReady(capacity, exclude)
	if err != nil {
		e.log.Error("claim queue items", "err", err)
		return
	}
	for _, it := range items {
		at := &activeTransfer{item: it, size: it.Size, started: time.Now(), lastT: time.Now()}
		at.phase.Store("starting")
		e.mu.Lock()
		e.active[it.ID] = at
		e.mu.Unlock()
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			e.process(at)
			e.mu.Lock()
			delete(e.active, at.item.ID)
			e.mu.Unlock()
			e.finishRunIfIdle(at.item.ConfigID)
			e.kick()
		}()
	}
	e.refreshStatus()
}

func (e *Engine) setStatusIf(status, msg string) {
	e.mu.Lock()
	if e.status != "revoked" {
		e.status, e.message = status, msg
	}
	e.mu.Unlock()
}

func (e *Engine) refreshStatus() {
	pending, failed, _ := e.st.QueueStats()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.status == "revoked" {
		return
	}
	scanning := false
	for _, s := range e.scanning {
		scanning = scanning || s
	}
	switch {
	case e.client == nil:
		e.status, e.message = "offline", "Not connected to a VaultDock server"
	case e.paused:
		e.status, e.message = "paused", "Synchronisation is paused"
	case time.Now().Before(e.offlineUntil):
		e.status, e.message = "offline", "Server unreachable; retrying automatically"
	case len(e.active) > 0 || pending > 0:
		e.status, e.message = "syncing", fmt.Sprintf("%d file(s) remaining", pending)
	case scanning:
		e.status, e.message = "scanning", "Looking for changes"
	case failed > 0:
		e.status, e.message = "error", fmt.Sprintf("%d file(s) failed", failed)
	default:
		e.status, e.message = "idle", "Everything is up to date"
	}
}

func backoff(attempts int) time.Duration {
	d := 5 * time.Second << min(attempts, 10)
	d = min(d, 30*time.Minute)
	jitter := time.Duration(rand.Int64N(int64(d / 5)))
	return d + jitter
}

func isNetErr(err error) bool {
	var ne net.Error
	var oe *net.OpError
	return errors.As(err, &ne) || errors.As(err, &oe) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}

func isCongestion(err error) bool {
	var ae *client.APIError
	if errors.As(err, &ae) {
		return ae.Status == 429 || ae.Status == 503 || ae.Status == 502 || ae.Status == 504
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// handleErr classifies a transfer error and reschedules or fails the item.
func (e *Engine) handleErr(at *activeTransfer, err error, stage string) {
	it := at.item
	msg := fmt.Sprintf("%s: %v", stage, err)
	var ae *client.APIError
	switch {
	case errors.Is(err, context.Canceled):
		_ = e.st.Defer(it.ID, time.Now().UnixMilli(), "")
		return
	case errors.As(err, &ae) && ae.Status == 401 && (ae.Code == "device_revoked" || ae.Code == "unknown_device" || ae.Code == "account_disabled" || ae.Code == "device_not_authorised"):
		_ = e.st.Defer(it.ID, time.Now().UnixMilli(), "")
		e.revoked(ae.Message)
		return
	case isNetErr(err) && !errors.As(err, &ae):
		e.mu.Lock()
		e.netFailures++
		if e.netFailures >= 3 {
			e.offlineUntil = time.Now().Add(min(time.Duration(e.netFailures)*10*time.Second, 5*time.Minute))
		}
		e.mu.Unlock()
		_ = e.st.Retry(it.ID, time.Now().Add(backoff(min(it.Attempts, 4))).UnixMilli(), msg)
		e.log.Warn("network error", "file", it.RelPath, "err", err)
		return
	case errors.As(err, &ae) && ae.Status == 507:
		_ = e.st.Retry(it.ID, time.Now().Add(15*time.Minute).UnixMilli(), "server is low on disk space; retrying later")
		return
	case client.Retryable(err) && it.Attempts+1 < maxAttempts:
		wait := backoff(it.Attempts)
		if errors.As(err, &ae) && ae.RetryAfter > wait {
			wait = ae.RetryAfter
		}
		_ = e.st.Retry(it.ID, time.Now().Add(wait).UnixMilli(), msg)
		e.log.Warn("transfer failed, will retry", "file", it.RelPath, "attempt", it.Attempts+1, "err", err)
		return
	}
	if errors.As(err, &ae) && ae.Message != "" {
		msg = ae.Message
	}
	_ = e.st.Fail(it.ID, msg)
	_ = e.st.PutFile(state.FileEntry{ConfigID: it.ConfigID, RelPath: it.RelPath, Size: it.Size, MTime: it.MTime, Status: "failed", Reason: msg})
	e.st.AddHistory(state.HistoryEntry{ConfigID: it.ConfigID, RelPath: it.RelPath, RemotePath: it.RemotePath(), Size: it.Size, Status: "failed", Error: msg, DurationMS: time.Since(at.started).Milliseconds()})
	e.mu.Lock()
	r := e.run(it.ConfigID)
	r.failed++
	if r.firstErr == "" {
		r.firstErr = msg
	}
	e.reports = append(e.reports, client.TransferReport{Path: it.RemotePath(), Size: it.Size, Status: "failed", Error: msg})
	e.mu.Unlock()
	e.log.Error("transfer failed", "file", it.RelPath, "err", msg)
}

func (e *Engine) run(cfgID string) *runStats {
	r := e.runs[cfgID]
	if r == nil {
		r = &runStats{}
		e.runs[cfgID] = r
	}
	r.touched = true
	return r
}

func (e *Engine) revoked(msg string) {
	e.mu.Lock()
	already := e.status == "revoked"
	e.status, e.message = "revoked", "This device's access was revoked. Reconnect to continue."
	e.client = nil
	e.mu.Unlock()
	if !already {
		e.log.Warn("device access revoked by server", "msg", msg)
		if e.OnRevoked != nil {
			go e.OnRevoked()
		}
	}
}

// process uploads one queued file.
func (e *Engine) process(at *activeTransfer) {
	it := at.item
	ctx := e.ctx
	c := e.getClient()
	if c == nil {
		_ = e.st.Defer(it.ID, time.Now().UnixMilli(), "")
		return
	}
	cfg, flt := e.config(it.ConfigID)
	if cfg == nil {
		_ = e.st.Done(it.ID)
		return
	}
	abs := absPath(cfg, it.RelPath)
	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			_ = e.st.Done(it.ID)
			_ = e.st.MarkDeleted(it.ConfigID, it.RelPath)
			return
		}
		e.handleErr(at, err, "read file")
		return
	}
	size, mtime := fi.Size(), fi.ModTime().UnixNano()
	if size != it.Size || mtime != it.MTime {
		// Changed since it was queued: evaluate again with fresh metadata.
		_ = e.st.Done(it.ID)
		e.consider(cfg, flt, it.RelPath, abs, fi, "file.modified", it.Manual, "", 0)
		return
	}
	stable := time.Duration(cfg.StableSeconds) * time.Second
	if age := time.Since(fi.ModTime()); age < stable {
		_ = e.st.Defer(it.ID, time.Now().Add(stable-age+500*time.Millisecond).UnixMilli(), "waiting for the file to stop changing")
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		_ = e.st.Defer(it.ID, time.Now().Add(30*time.Second).UnixMilli(), "file is in use; waiting")
		return
	}
	defer f.Close()

	// hash (bounded to avoid disk contention on HDDs)
	at.phase.Store("hashing")
	select {
	case e.hashSem <- struct{}{}:
	case <-ctx.Done():
		_ = e.st.Defer(it.ID, time.Now().UnixMilli(), "")
		return
	}
	sum, err := hashReader(ctx, f, size)
	<-e.hashSem
	if err != nil {
		e.handleErr(at, err, "hash")
		return
	}
	if fi2, err := os.Stat(abs); err != nil || fi2.Size() != size || fi2.ModTime().UnixNano() != mtime {
		_ = e.st.Defer(it.ID, time.Now().Add(stable).UnixMilli(), "file changed while hashing; waiting")
		return
	}

	at.phase.Store("uploading")
	res, err := c.CreateUpload(ctx, client.UploadRequest{Path: it.RemotePath(), Size: size, SHA256: sum, ModifiedAt: fi.ModTime().UnixMilli(), Tags: it.Tags, OnConflict: cfg.OnConflict})
	if err != nil {
		e.handleErr(at, err, "start upload")
		return
	}
	if res.Status != "complete" {
		up := res.Upload
		at.sent.Store(up.BytesReceived)
		if err := e.uploadChunks(ctx, c, at, f, up); err != nil {
			e.handleErr(at, err, "upload")
			return
		}
		at.phase.Store("finalizing")
		res, err = c.CompleteUpload(ctx, up.ID)
		if err != nil {
			switch {
			case client.IsCode(err, "hash_mismatch") || client.IsCode(err, "size_mismatch"):
				// The file changed during the upload; start again once stable.
				_ = e.st.Defer(it.ID, time.Now().Add(stable).UnixMilli(), "file changed during upload; will upload the new version")
				return
			case client.IsCode(err, "not_found") || client.IsCode(err, "upload_gone"):
				_ = e.st.Defer(it.ID, time.Now().UnixMilli(), "upload session expired; restarting")
				return
			case client.IsCode(err, "incomplete"):
				e.handleErr(at, err, "complete")
				return
			}
			e.handleErr(at, err, "complete")
			return
		}
	}
	// success
	e.mu.Lock()
	e.netFailures = 0
	r := e.run(it.ConfigID)
	if res.Result != "unchanged" && res.Result != "skipped" {
		r.uploaded++
		r.bytes += size
		e.session.completed++
		e.session.bytes += size
	}
	e.mu.Unlock()
	_ = e.st.PutFile(state.FileEntry{ConfigID: it.ConfigID, RelPath: it.RelPath, Size: size, MTime: mtime, SHA256: sum, RemotePath: it.RemotePath(), Status: "synced", UploadedAt: time.Now().UnixMilli()})
	_ = e.st.Done(it.ID)
	e.st.AddHistory(state.HistoryEntry{ConfigID: it.ConfigID, RelPath: it.RelPath, RemotePath: it.RemotePath(), Size: size, Status: "completed", Result: res.Result, DurationMS: time.Since(at.started).Milliseconds()})
	e.log.Info("uploaded", "file", it.RelPath, "remote", it.RemotePath(), "result", res.Result, "bytes", size, "ms", time.Since(at.started).Milliseconds())
}

func hashReader(ctx context.Context, f *os.File, size int64) (string, error) {
	h := sha256.New()
	buf := make([]byte, 1<<20)
	r := io.NewSectionReader(f, 0, size)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := r.Read(buf)
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

// uploadChunks sends missing chunks with bounded per-file parallelism and
// global adaptive concurrency.
func (e *Engine) uploadChunks(ctx context.Context, c *client.Client, at *activeTransfer, f *os.File, up *client.UploadSession) error {
	missing := up.Missing()
	if len(missing) == 0 {
		return nil
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
	for w := 0; w < min(maxChunkParallelPerFile, len(missing)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := e.sendChunk(ctx, c, at, f, up, idx); err != nil {
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

func (e *Engine) sendChunk(ctx context.Context, c *client.Client, at *activeTransfer, f *os.File, up *client.UploadSession, idx int64) error {
	off, n := idx*up.ChunkSize, up.ChunkLen(idx)
	sum, err := client.HashSection(f, off, n)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		if err := e.ctrl.Acquire(ctx); err != nil {
			return err
		}
		var local atomic.Int64
		start := time.Now()
		err := c.PutChunkHashed(ctx, up.ID, idx, progressReaderAt{r: f, total: &at.sent, local: &local}, off, n, sum)
		lat := time.Since(start)
		var ok int64
		if err == nil {
			ok = n
		}
		e.ctrl.Release(ok, lat, err != nil && isCongestion(err))
		if err == nil {
			at.sent.Add(n - local.Load())
			return nil
		}
		at.sent.Add(-local.Load())
		if errors.Is(err, context.Canceled) || !client.Retryable(err) || attempt >= 4 {
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
