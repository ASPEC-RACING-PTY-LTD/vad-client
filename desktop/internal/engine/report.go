package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/outbox"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
)

// Version is the desktop client version reported to the server.
var Version = "dev"

// timerLoop runs periodic work: concurrency control, progress, rescans,
// status reports, automation refreshes and schedules.
func (e *Engine) timerLoop() {
	defer e.wg.Done()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	n := 0
	for {
		select {
		case <-e.ctx.Done():
			return
		case now := <-tick.C:
			n++
			e.updateSpeeds(now)
			if n%2 == 0 {
				e.xfer.Tick(now)
			}
			if n%3 == 0 {
				e.govern()
			}
			if n%5 == 0 {
				e.refreshStatus()
			}
			if n%10 == 0 {
				go e.flushReports()
			}
			if n%15 == 0 {
				go e.reportState()
			}
			if n%30 == 0 {
				e.runSchedules()
				e.periodicRescans(now)
			}
			if n%60 == 0 {
				e.checkDeferredSoon()
			}
			if n%120 == 0 {
				go e.refreshAutomations()
			}
		}
	}
}

func (e *Engine) updateSpeeds(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, a := range e.active {
		a.mu.Lock()
		dt := now.Sub(a.lastT).Seconds()
		if dt > 0 {
			cur := a.sent.Load()
			inst := float64(cur-a.lastSent) / dt
			if inst < 0 {
				inst = 0
			}
			if a.speed == 0 {
				a.speed = inst
			} else {
				a.speed = a.speed*0.6 + inst*0.4
			}
			a.lastSent, a.lastT = cur, now
		}
		a.mu.Unlock()
	}
}

func (e *Engine) periodicRescans(now time.Time) {
	for _, c := range e.Configs() {
		if !c.Enabled || c.Mode != "auto" {
			continue
		}
		e.mu.Lock()
		last := e.lastScan[c.ID]
		busy := e.scanning[c.ID]
		e.mu.Unlock()
		if !busy && now.Sub(last) >= time.Duration(c.ScanIntervalMinutes)*time.Minute {
			id := c.ID
			go func() { _, _ = e.Scan(id, false) }()
		}
	}
}

// reportState sends a status summary to the server for the web dashboard.
func (e *Engine) reportState() {
	c := e.getClient()
	if c == nil {
		return
	}
	snap := e.Snapshot()
	views := e.ConfigViews()
	type cfgReport struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		LocalPath    string `json:"local_path"`
		Destination  string `json:"destination"`
		Mode         string `json:"mode"`
		Enabled      bool   `json:"enabled"`
		Recursive    bool   `json:"recursive"`
		FilesSynced  int64  `json:"files_synced"`
		FilesPending int64  `json:"files_pending"`
		LastScanAt   int64  `json:"last_scan_at"`
		LastError    string `json:"last_error"`
	}
	cfgs := []cfgReport{}
	for _, v := range views {
		cfgs = append(cfgs, cfgReport{v.ID, v.Name, v.LocalPath, v.Destination, v.Mode, v.Enabled, v.Recursive, v.FilesSynced, v.FilesPending, v.LastScanAt, v.LastError})
	}
	active := []map[string]any{}
	for i, a := range snap.Active {
		if i >= 10 {
			break
		}
		active = append(active, map[string]any{"path": a.Path, "size": a.Size, "sent": a.Sent, "speed": a.Speed})
	}
	state := map[string]any{
		"client_version": Version,
		"status":         snap.Status,
		"message":        snap.Message,
		"paused":         snap.Paused,
		"configs":        cfgs,
		"stats": map[string]any{
			"active": snap.Stats.Active, "queued": snap.Stats.Queued, "failed": snap.Stats.Failed, "speed_bps": snap.Stats.SpeedBps,
			"concurrency": snap.Stats.Concurrency, "concurrency_limit": snap.Stats.ConcurrencyLimit, "eta_seconds": snap.Stats.ETASeconds,
		},
		"active": active,
		// Shown in the admin panel, which can turn direct uploads off.
		"direct_upload": map[string]any{"enabled": snap.Direct.Enabled, "active": snap.Direct.Active, "url": snap.Direct.URL},
	}
	ctx, cancel := context.WithTimeout(e.ctx, 20*time.Second)
	defer cancel()
	if err := c.ReportState(ctx, state); err != nil {
		e.checkRevoked(err)
		e.log.Debug("state report failed", "err", err)
	}
}

// Report kinds stored in the outbox. Each payload is a JSON array that fits
// one server request.
const (
	reportKindTransfers     = "transfer_report"
	reportKindNotifications = "notification"

	maxTransfersPerReport     = 200 // api: at most 200 per /device/transfers
	maxNotificationsPerReport = 50
)

// SetOutbox replaces the durable report outbox. Its Sender should be
// e.DeliverReport. Without a call the engine creates one in Start (or on
// first use), backed by the state store's database.
func (e *Engine) SetOutbox(o *outbox.Outbox) {
	e.mu.Lock()
	e.outbox = o
	e.mu.Unlock()
}

// reportOutbox returns the engine's outbox, creating it on first use, or nil
// when there is no store.
func (e *Engine) reportOutbox() *outbox.Outbox {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.outbox == nil && e.st != nil && e.st.DB != nil {
		e.outbox = outbox.New(e.st.DB, e.DeliverReport, outbox.Options{Log: e.log, OnDrop: e.outboxDropped})
	}
	return e.outbox
}

// outboxDropped records an issue when reports are discarded undelivered.
func (e *Engine) outboxDropped(reason, kind string, n int) {
	e.recordIssue("outbox", "", kind, "report_dropped", fmt.Sprintf("%d undelivered report(s) dropped (%s)", n, reason))
}

// DeliverReport is the outbox Sender: it sends one stored report batch.
// Requests the server will never accept are marked permanent so they are
// dropped instead of retried.
func (e *Engine) DeliverReport(ctx context.Context, kind string, payload []byte) error {
	c := e.getClient()
	if c == nil {
		return errors.New("not connected")
	}
	var err error
	switch kind {
	case reportKindTransfers:
		var items []client.TransferReport
		if err := json.Unmarshal(payload, &items); err != nil {
			return outbox.Permanent(err)
		}
		err = c.ReportTransfers(ctx, items)
	case reportKindNotifications:
		var items []client.NotificationReport
		if err := json.Unmarshal(payload, &items); err != nil {
			return outbox.Permanent(err)
		}
		err = c.Notify(ctx, items)
	default:
		return outbox.Permanent(fmt.Errorf("unknown report kind %q", kind))
	}
	if err != nil {
		e.checkRevoked(err)
		var ae *client.APIError
		if errors.As(err, &ae) && ae.Status >= 400 && ae.Status < 500 {
			switch ae.Status {
			case 401, 403, 408, 429:
			default:
				return outbox.Permanent(err)
			}
		}
	}
	return err
}

// flushReports moves buffered transfer reports and notifications into the
// durable outbox (also while offline) and then delivers what is ready.
func (e *Engine) flushReports() {
	ob := e.reportOutbox()
	e.mu.Lock()
	reports := e.reports
	notes := e.notes
	e.reports, e.notes = nil, nil
	e.mu.Unlock()
	if ob == nil {
		e.requeueReports(reports, notes)
		return
	}
	for len(reports) > 0 {
		n := min(len(reports), maxTransfersPerReport)
		if err := enqueueJSON(ob, reportKindTransfers, reports[:n]); err != nil {
			e.log.Warn("could not queue transfer report", "err", err)
			break
		}
		reports = reports[n:]
	}
	for len(notes) > 0 {
		n := min(len(notes), maxNotificationsPerReport)
		if err := enqueueJSON(ob, reportKindNotifications, notes[:n]); err != nil {
			e.log.Warn("could not queue notification", "err", err)
			break
		}
		notes = notes[n:]
	}
	e.requeueReports(reports, notes)
	if e.getClient() == nil {
		return
	}
	ctx, cancel := context.WithTimeout(e.ctx, 20*time.Second)
	defer cancel()
	if _, err := ob.Flush(ctx); err != nil {
		e.log.Debug("report delivery failed", "err", err)
	}
}

func enqueueJSON(ob *outbox.Outbox, kind string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return ob.Enqueue(kind, "", b)
}

// requeueReports puts reports that could not be stored back in memory.
func (e *Engine) requeueReports(reports []client.TransferReport, notes []client.NotificationReport) {
	if len(reports) == 0 && len(notes) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(reports) > 0 && len(e.reports) < 1000 {
		e.reports = append(reports, e.reports...)
	}
	if len(notes) > 0 && len(e.notes) < 200 {
		e.notes = append(notes, e.notes...)
	}
}

// checkRevoked stops synchronisation when the server reports that this
// device may no longer authenticate.
func (e *Engine) checkRevoked(err error) {
	var ae *client.APIError
	if errors.As(err, &ae) && ae.Status == 401 {
		switch ae.Code {
		case "device_revoked", "unknown_device", "account_disabled", "device_not_authorised":
			e.revoked(ae.Message)
		}
	}
}
