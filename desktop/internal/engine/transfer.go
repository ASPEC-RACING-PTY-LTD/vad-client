package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/transfer"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
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
	// A file the server is finishing no longer sends anything, so it does
	// not hold one of the transfer slots: the next file starts uploading.
	activeN, finishing := 0, 0
	for _, a := range e.active {
		if a.phase.Load() == "finalizing" {
			finishing++
		} else {
			activeN++
		}
	}
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
	case e.held.Load():
		return // the computer is busy; govern wakes the dispatcher
	}
	// At most as many files as the transfer limit are in flight, so the
	// first files in the queue get the connections instead of every file
	// trickling along at once.
	capacity := min(e.ctrl.Limit(), absoluteMax) - activeN
	// Bound the files waiting for the server, so a slow server is not
	// handed an unbounded backlog.
	capacity = min(capacity, 2*absoluteMax-activeN-finishing)
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
		at.ctx, at.cancel = context.WithCancel(e.ctx)
		at.phase.Store("starting")
		e.mu.Lock()
		e.active[it.ID] = at
		e.mu.Unlock()
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			e.process(at)
			at.cancel()
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
	case e.held.Load() && pending > 0:
		e.status, e.message = "waiting", "Your computer is busy; transfers continue when it has room"
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
	case client.IsDirectLost(err):
		// The local address went away: the route is dropped, so the next
		// try goes to the public URL. Not the file's fault.
		_ = e.st.Defer(it.ID, time.Now().UnixMilli(), "server's local address unreachable; using the public address")
		return
	case transfer.IsNetErr(err) && !errors.As(err, &ae):
		e.mu.Lock()
		e.netFailures++
		offline := e.netFailures >= 3
		if offline {
			e.offlineUntil = time.Now().Add(min(time.Duration(e.netFailures)*10*time.Second, 5*time.Minute))
		}
		e.mu.Unlock()
		e.log.Warn("network error", "file", it.RelPath, "err", err)
		if offline {
			// The server is unreachable, not this file: wait without
			// counting an attempt.
			_ = e.st.Defer(it.ID, time.Now().Add(transfer.Backoff(min(it.Attempts, 4))).UnixMilli(), "server unreachable; waiting")
			return
		}
		if it.Attempts+1 < transfer.MaxAttempts {
			_ = e.st.Retry(it.ID, time.Now().Add(transfer.Backoff(it.Attempts)).UnixMilli(), msg)
			return
		}
	case errors.As(err, &ae) && ae.Status == 507:
		_ = e.st.Retry(it.ID, time.Now().Add(15*time.Minute).UnixMilli(), "server is low on disk space; retrying later")
		return
	case client.Retryable(err) && it.Attempts+1 < transfer.MaxAttempts:
		wait := transfer.Backoff(it.Attempts)
		if errors.As(err, &ae) && ae.RetryAfter > wait {
			wait = ae.RetryAfter
		}
		_ = e.st.Retry(it.ID, time.Now().Add(wait).UnixMilli(), msg)
		e.log.Warn("transfer failed, will retry", "file", it.RelPath, "attempt", it.Attempts+1, "err", err)
		return
	}
	msg = transfer.FailReason(err, it.Attempts+1)
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
	ctx := at.ctx
	if ctx == nil {
		ctx = e.ctx
	}
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
	// Stat and open run as the configuration's owner; the open handle keeps
	// the access granted to them, so hashing and uploading read through it
	// without the token.
	var fi os.FileInfo
	err := e.asOwner(cfg, func() error {
		var serr error
		fi, serr = os.Stat(abs)
		return serr
	})
	if IsOwnerError(err) {
		_ = e.st.Defer(it.ID, time.Now().Add(time.Minute).UnixMilli(), ownerMessage(err))
		return
	}
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
	var f *os.File
	err = e.asOwner(cfg, func() error {
		var oerr error
		f, oerr = openForUpload(abs)
		return oerr
	})
	if IsOwnerError(err) {
		_ = e.st.Defer(it.ID, time.Now().Add(time.Minute).UnixMilli(), ownerMessage(err))
		return
	}
	if err != nil {
		e.deferUnreadable(at, size, mtime, err)
		return
	}
	defer f.Close()
	e.clearOpenDeferral(it.ConfigID, it.RelPath)

	// hash (bounded to avoid disk contention on HDDs)
	at.phase.Store("hashing")
	sum, err := e.xfer.Hash(ctx, f, size)
	if err != nil {
		e.handleErr(at, err, "hash")
		return
	}
	var fi2 os.FileInfo
	err = e.asOwner(cfg, func() error {
		var serr error
		fi2, serr = os.Stat(abs)
		return serr
	})
	if err != nil || fi2.Size() != size || fi2.ModTime().UnixNano() != mtime {
		_ = e.st.Defer(it.ID, time.Now().Add(stable).UnixMilli(), "file changed while hashing; waiting")
		return
	}

	req := client.UploadRequest{Path: it.RemotePath(), Size: size, SHA256: sum, ModifiedAt: fi.ModTime().UnixMilli(), Tags: it.Tags, OnConflict: cfg.OnConflict}
	// The server keeps compressed chunks as they are and does not compress
	// the raw ones again (they did not shrink here).
	req.Compressed = cfg.Compress && e.serverHas(wire.FeatureUploadCompressedFlag)
	// Queue order is the priority: the file that started first gets the
	// connections and finishes first.
	res, err := e.xfer.Upload(ctx, c, req, f, transfer.Progress{Sent: &at.sent, Phase: func(p string) { at.phase.Store(p); e.kickIf(p == "finalizing") }, Compress: cfg.Compress, Priority: it.ID, Session: func(id string) { at.uploadID.Store(id) }, FinishLater: e.bulkRun(it.ConfigID)})
	if err != nil {
		var se *transfer.StageError
		if !errors.As(err, &se) {
			e.handleErr(at, err, "upload")
			return
		}
		if se.Stage == "complete" {
			switch {
			case client.IsCode(se.Err, "hash_mismatch") || client.IsCode(se.Err, "size_mismatch"):
				// The file changed during the upload; start again once stable.
				_ = e.st.Defer(it.ID, time.Now().Add(stable).UnixMilli(), "file changed during upload; will upload the new version")
				return
			case client.IsCode(se.Err, "not_found") || client.IsCode(se.Err, "upload_gone"):
				// Restarting counts as a try, so a file that keeps losing its
				// session fails with a reason instead of looping.
				if it.Attempts+1 < transfer.MaxAttempts {
					_ = e.st.Retry(it.ID, time.Now().UnixMilli(), "upload session expired; restarting")
					return
				}
			}
		}
		e.handleErr(at, se.Err, se.Stage)
		return
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
	if id, _ := at.uploadID.Load().(string); res.Status == "finalizing" && id != "" {
		// The server finishes it later; checked once the sync is done.
		e.deferredSent(it, id, sum, size, mtime)
		e.log.Info("uploaded; the server finishes it later", "file", it.RelPath, "bytes", size, "ms", time.Since(at.started).Milliseconds())
		return
	}
	_ = e.st.PutFile(state.FileEntry{ConfigID: it.ConfigID, RelPath: it.RelPath, Size: size, MTime: mtime, SHA256: sum, RemotePath: it.RemotePath(), Status: "synced", UploadedAt: time.Now().UnixMilli()})
	_ = e.st.Done(it.ID)
	e.st.AddHistory(state.HistoryEntry{ConfigID: it.ConfigID, RelPath: it.RelPath, RemotePath: it.RemotePath(), Size: size, Status: "completed", Result: res.Result, DurationMS: time.Since(at.started).Milliseconds()})
	e.log.Info("uploaded", "file", it.RelPath, "remote", it.RemotePath(), "result", res.Result, "bytes", size, "ms", time.Since(at.started).Milliseconds())
}

// Bounds on waiting for a file that cannot be opened, typically because
// another process holds it locked (B12). Variables so tests can shorten them.
var (
	openRetryDelay   = 30 * time.Second
	maxOpenDeferrals = 20
	maxOpenDeferSpan = 30 * time.Minute
	openForUpload    = os.Open
)

const (
	// issueSourceTransfer marks issues raised while uploading queued files.
	issueSourceTransfer = "transfer"
	// openDeferPrefix prefixes the kv keys holding open-failure counts.
	openDeferPrefix = "open_defer/"
)

func openDeferKey(configID, rel string) string { return openDeferPrefix + configID + "/" + rel }

// deferUnreadable reschedules a file that could not be opened. The count and
// first failure time are kept in kv per file version (size, mtime), so the
// bound survives restarts and resets when the file changes. Once
// maxOpenDeferrals or maxOpenDeferSpan is reached the file is marked skipped,
// which scans and the watcher leave alone until its size or mtime changes,
// and an issue is recorded.
func (e *Engine) deferUnreadable(at *activeTransfer, size, mtime int64, err error) {
	it := at.item
	key := openDeferKey(it.ConfigID, it.RelPath)
	now := time.Now()
	count, first := 0, now.UnixMilli()
	var s, m, f int64
	var c int
	if _, perr := fmt.Sscanf(e.st.Get(key), "%d %d %d %d", &s, &m, &c, &f); perr == nil && s == size && m == mtime {
		count, first = c, f
	}
	count++
	class := classifyIssue(err)
	if count < maxOpenDeferrals && now.Sub(time.UnixMilli(first)) < maxOpenDeferSpan {
		e.st.Set(key, fmt.Sprintf("%d %d %d %d", size, mtime, count, first))
		note := "file is in use; waiting"
		if class != issueLocked {
			note = "cannot open the file; waiting"
		}
		_ = e.st.Defer(it.ID, now.Add(openRetryDelay).UnixMilli(), note)
		return
	}
	e.clearOpenDeferral(it.ConfigID, it.RelPath)
	reason := "file is in use by another program; skipped until it changes"
	if class != issueLocked {
		reason = "file cannot be opened; skipped until it changes"
	}
	e.recordIssue(issueSourceTransfer, it.ConfigID, it.RelPath, class, fmt.Sprintf("%s (%d attempts): %v", reason, count, err))
	_ = e.st.Done(it.ID)
	_ = e.st.PutFile(state.FileEntry{ConfigID: it.ConfigID, RelPath: it.RelPath, Size: size, MTime: mtime, Status: "skipped", Reason: reason})
	e.mu.Lock()
	e.run(it.ConfigID)
	e.reports = append(e.reports, client.TransferReport{Path: it.RemotePath(), Size: size, Status: "skipped", Error: reason})
	e.mu.Unlock()
	e.log.Warn("file skipped: cannot open", "file", it.RelPath, "class", class, "attempts", count, "err", err)
}

// clearOpenDeferral forgets the open-failure count of a file.
func (e *Engine) clearOpenDeferral(configID, rel string) {
	key := openDeferKey(configID, rel)
	if e.st.Get(key) == "" {
		return
	}
	if _, err := e.st.DB.Exec("DELETE FROM kv WHERE k = ?", key); err != nil {
		e.log.Warn("clear open deferral", "file", rel, "err", err)
	}
}
