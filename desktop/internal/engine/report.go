package engine

import (
	"context"
	"errors"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/client"
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
				e.ctrl.Tick(now)
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
	}
	ctx, cancel := context.WithTimeout(e.ctx, 20*time.Second)
	defer cancel()
	if err := c.ReportState(ctx, state); err != nil {
		e.checkRevoked(err)
		e.log.Debug("state report failed", "err", err)
	}
}

func (e *Engine) flushReports() {
	c := e.getClient()
	if c == nil {
		return
	}
	e.mu.Lock()
	reports := e.reports
	notes := e.notes
	e.reports, e.notes = nil, nil
	e.mu.Unlock()
	ctx, cancel := context.WithTimeout(e.ctx, 20*time.Second)
	defer cancel()
	for len(reports) > 0 {
		n := min(len(reports), 200)
		if err := c.ReportTransfers(ctx, reports[:n]); err != nil {
			e.mu.Lock()
			if len(e.reports) < 1000 {
				e.reports = append(reports, e.reports...)
			}
			e.mu.Unlock()
			break
		}
		reports = reports[n:]
	}
	for len(notes) > 0 {
		n := min(len(notes), 50)
		if err := c.Notify(ctx, notes[:n]); err != nil {
			e.mu.Lock()
			if len(e.notes) < 200 {
				e.notes = append(notes, e.notes...)
			}
			e.mu.Unlock()
			break
		}
		notes = notes[n:]
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
