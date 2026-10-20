package engine

import (
	"context"
	"strconv"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/transfer"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// A big sync (a folder's first sync, or bulkBytes or more waiting) does not
// wait for the server to verify and store each file: every upload is handed
// to the server to finish later (upload.deferred_finish) and the next file
// starts at once. When the sync is done the outcomes are collected; a file
// the server did not end up storing (a failed finish, missing chunks, or no
// copy at all) is queued again, and after MaxAttempts it fails with the
// server's reason.

// bulkBytes is the queued size from which a sync is treated as big.
var bulkBytes int64 = 10 << 30

// deferredRecheck is how soon outcomes are asked again while the server is
// still finishing uploads.
const deferredRecheck = 5 * time.Second

// bulkRun reports whether uploads of cfgID are finished later. The choice is
// made once per sync run.
func (e *Engine) bulkRun(cfgID string) bool {
	if !e.serverHas(wire.FeatureDeferredFinish) {
		return false
	}
	e.mu.Lock()
	r := e.run(cfgID)
	decided, bulk := r.bulkSet, r.bulk
	e.mu.Unlock()
	if decided {
		return bulk
	}
	synced, _, _ := e.st.ConfigCounts(cfgID)
	bulk = synced == 0 || e.st.PendingBytesFor(cfgID) >= bulkBytes
	e.mu.Lock()
	r.bulkSet, r.bulk = true, bulk
	e.mu.Unlock()
	if bulk {
		e.log.Info("big sync: the server checks uploads after the sync", "config", cfgID)
	}
	return bulk
}

// deferredSent records an upload the server finishes later.
func (e *Engine) deferredSent(it state.Item, uploadID, sum string, size, mtime int64) {
	_ = e.st.AddDeferred(state.Deferred{UploadID: uploadID, SHA256: sum, Item: it})
	_ = e.st.PutFile(state.FileEntry{ConfigID: it.ConfigID, RelPath: it.RelPath, Size: size, MTime: mtime, SHA256: sum, RemotePath: it.RemotePath(), Status: "verifying"})
	_ = e.st.Done(it.ID)
}

// checkDeferredSoon collects outcomes in the background (one pass at a time).
func (e *Engine) checkDeferredSoon() {
	if e.ctx == nil || e.ctx.Err() != nil || e.st.DeferredCount("") == 0 || !e.checking.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer e.checking.Store(false)
		e.checkDeferred(e.ctx)
	}()
}

// checkDeferred asks the server about every waiting upload of the syncs
// that have nothing left to send.
func (e *Engine) checkDeferred(ctx context.Context) {
	c := e.getClient()
	if c == nil || !e.serverHas(wire.FeatureDeferredFinish) {
		return
	}
	e.lastCheck.Store(time.Now().UnixMilli())
	list, err := e.st.DeferredUploads(nil, 5000)
	if err != nil {
		return
	}
	var todo []state.Deferred
	pending := false
	for _, d := range list {
		if e.syncIdle(d.Item.ConfigID) {
			todo = append(todo, d)
		}
	}
	for len(todo) > 0 {
		batch := todo[:min(len(todo), wire.UploadResultsMax)]
		todo = todo[len(batch):]
		checks := make([]wire.UploadCheck, len(batch))
		for i, d := range batch {
			checks[i] = wire.UploadCheck{ID: d.UploadID, Path: d.Item.RemotePath(), Size: d.Item.Size, SHA256: d.SHA256}
		}
		out, err := c.UploadResults(ctx, checks)
		if err != nil || len(out) != len(batch) {
			e.log.Warn("checking uploads on the server", "err", err)
			return
		}
		for i, o := range out {
			e.deferredOutcome(batch[i], o)
			pending = pending || o.Status == wire.UploadPending
		}
	}
	e.refreshStatus()
	e.kick()
	if pending {
		// The server is still finishing some: ask again shortly.
		time.AfterFunc(deferredRecheck, e.checkDeferredSoon)
	}
}

// syncIdle reports whether a configuration has nothing queued or sending.
func (e *Engine) syncIdle(cfgID string) bool {
	if e.st.PendingForConfig(cfgID) > 0 {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, a := range e.active {
		if a.item.ConfigID == cfgID {
			return false
		}
	}
	return true
}

func (e *Engine) deferredOutcome(d state.Deferred, o wire.UploadOutcome) {
	it := d.Item
	switch o.Status {
	case wire.UploadPending:
		return
	case wire.UploadDone:
		_ = e.st.RemoveDeferred(it.ConfigID, it.RelPath)
		_ = e.st.PutFile(state.FileEntry{ConfigID: it.ConfigID, RelPath: it.RelPath, Size: it.Size, MTime: it.MTime, SHA256: d.SHA256, RemotePath: it.RemotePath(), Status: "synced", UploadedAt: time.Now().UnixMilli()})
		e.st.AddHistory(state.HistoryEntry{ConfigID: it.ConfigID, RelPath: it.RelPath, RemotePath: it.RemotePath(), Size: it.Size, Status: "completed", Result: o.Result})
		return
	}
	// The server does not have it: send it again (an incomplete session
	// resumes with the missing chunks).
	_ = e.st.RemoveDeferred(it.ConfigID, it.RelPath)
	reason := o.Error
	if reason == "" {
		reason = map[string]string{wire.UploadIncomplete: "the upload was incomplete on the server", wire.UploadMissing: "the server has no copy of this file"}[o.Status]
	}
	attempts := it.Attempts + 1
	if attempts < transfer.MaxAttempts {
		_ = e.st.Requeue(it, attempts, "server check: "+reason+"; uploading again")
		_ = e.st.PutFile(state.FileEntry{ConfigID: it.ConfigID, RelPath: it.RelPath, Size: it.Size, MTime: it.MTime, Status: "queued"})
		e.log.Warn("server check failed; uploading again", "file", it.RelPath, "status", o.Status, "reason", reason)
		return
	}
	msg := "the server did not store the file after " + strconv.Itoa(attempts) + " attempts: " + reason
	_ = e.st.PutFile(state.FileEntry{ConfigID: it.ConfigID, RelPath: it.RelPath, Size: it.Size, MTime: it.MTime, Status: "failed", Reason: msg})
	_ = e.st.Requeue(it, attempts, msg)
	_ = e.st.Fail(e.queueID(it), msg)
	e.st.AddHistory(state.HistoryEntry{ConfigID: it.ConfigID, RelPath: it.RelPath, RemotePath: it.RemotePath(), Size: it.Size, Status: "failed", Error: msg})
	e.mu.Lock()
	e.reports = append(e.reports, client.TransferReport{Path: it.RemotePath(), Size: it.Size, Status: "failed", Error: msg})
	e.mu.Unlock()
	e.log.Error("upload failed the server check", "file", it.RelPath, "err", msg)
}

// queueID returns the queue id of a configuration's file (0 if not queued).
func (e *Engine) queueID(it state.Item) int64 {
	var id int64
	_ = e.st.DB.QueryRow("SELECT id FROM queue WHERE config_id = ? AND rel_path = ?", it.ConfigID, it.RelPath).Scan(&id)
	return id
}
