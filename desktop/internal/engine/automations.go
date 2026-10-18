package engine

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/automation"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
)

// Guards against automation loops (sync.completed -> start task -> ...).
const (
	maxChainDepth     = 3
	startTaskCooldown = time.Minute
)

// AutomationView is shown in the desktop UI.
type AutomationView struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Priority int      `json:"priority"`
	Triggers []string `json:"triggers"`
	Actions  []string `json:"actions"`
}

func (e *Engine) refreshAutomations() {
	c := e.getClient()
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
	defer cancel()
	list, err := c.Automations(ctx)
	if err != nil {
		e.checkRevoked(err)
		e.log.Warn("could not refresh automations", "err", err)
		return
	}
	named := e.activeAutomations(list)
	e.mu.Lock()
	e.autos = named
	e.autosLoaded = time.Now()
	e.mu.Unlock()
	if raw, err := json.Marshal(list); err == nil {
		e.st.Set("automations_cache", string(raw))
	}
}

// LoadCachedAutomations restores automations saved during the last run so
// rules keep applying while offline.
func (e *Engine) LoadCachedAutomations() {
	raw := e.st.Get("automations_cache")
	if raw == "" {
		return
	}
	var list []client.RemoteAutomation
	if json.Unmarshal([]byte(raw), &list) != nil {
		return
	}
	named := e.activeAutomations(list)
	e.mu.Lock()
	e.autos = named
	e.mu.Unlock()
}

// activeAutomations parses and validates automations locally: the client
// never runs anything the shared validator rejects. Invalid ones are logged
// and recorded as issues (B4); disabled ones are skipped quietly.
func (e *Engine) activeAutomations(list []client.RemoteAutomation) []automation.Named {
	var named []automation.Named
	for _, a := range list {
		id := strconv.FormatInt(a.ID, 10)
		parsed, res := automation.Parse(a.Definition)
		if parsed == nil || !res.Valid() {
			msg := "automation " + strconv.Quote(a.Name) + " is invalid"
			if res != nil && len(res.Errors) > 0 {
				first := res.Errors[0]
				msg += ": " + first.Path + ": " + first.Message
			}
			if e.log != nil {
				e.log.Warn("automation: invalid, not applied", "automation", id, "name", a.Name, "errors", errCount(res), "detail", msg)
			}
			e.recordIssue(issueSourceAutomation, "", id, issueInvalid, msg)
			continue
		}
		if !parsed.Enabled {
			continue
		}
		named = append(named, automation.Named{ID: id, Automation: parsed})
	}
	return named
}

func errCount(res *automation.ValidationResult) int {
	if res == nil {
		return 0
	}
	return len(res.Errors)
}

// Automations lists the automations currently active on this device.
func (e *Engine) Automations() []AutomationView {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []AutomationView{}
	for _, n := range e.autos {
		id, _ := strconv.ParseInt(n.ID, 10, 64)
		v := AutomationView{ID: id, Name: n.Automation.Name, Priority: n.Automation.Priority, Triggers: []string{}, Actions: []string{}}
		for _, node := range n.Automation.Nodes {
			if node.Type == automation.TypeTrigger {
				v.Triggers = append(v.Triggers, node.Kind)
			} else if node.Type == automation.TypeAction {
				v.Actions = append(v.Actions, node.Kind)
			}
		}
		out = append(out, v)
	}
	return out
}

// RefreshAutomations fetches automations now.
func (e *Engine) RefreshAutomations() { e.refreshAutomations() }

func (e *Engine) autosFor(cfg *config.SyncConfig) []automation.Named {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch cfg.AutomationMode {
	case "none":
		return nil
	case "selected":
		want := map[string]bool{}
		for _, id := range cfg.Automations {
			want[strconv.FormatInt(id, 10)] = true
		}
		var out []automation.Named
		for _, a := range e.autos {
			if want[a.ID] {
				out = append(out, a)
			}
		}
		return out
	}
	return append([]automation.Named(nil), e.autos...)
}

// applySideEffects performs actions that do not change the upload itself:
// folder creation, notifications and starting other sync tasks.
func (e *Engine) applySideEffects(cfg *config.SyncConfig, plan automation.Plan, depth int) {
	c := e.getClient()
	if len(plan.CreateFolders) > 0 && c != nil {
		folders := append([]string(nil), plan.CreateFolders...)
		go func() {
			for _, f := range folders {
				ctx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
				if err := c.EnsureFolder(ctx, f); err != nil {
					e.log.Warn("automation: could not create folder", "folder", f, "err", err)
				}
				cancel()
			}
		}()
	}
	for _, n := range plan.Notifications {
		e.mu.Lock()
		if n.Channel == "web" || n.Channel == "both" {
			if len(e.notes) < 200 {
				e.notes = append(e.notes, client.NotificationReport{Title: n.Title, Message: n.Message, Level: n.Level})
			}
		}
		show := e.notify && (n.Channel == "desktop" || n.Channel == "both")
		e.mu.Unlock()
		switch {
		case !show:
		case e.NotifyOwner != nil:
			e.NotifyOwner(cfg.OwnerSID, n.Title, n.Message, n.Level)
		case e.Notifier != nil:
			e.Notifier(n.Title, n.Message, n.Level)
		}
	}
	for _, name := range plan.StartTasks {
		e.startTask(cfg, name, depth+1)
	}
}

func (e *Engine) startTask(from *config.SyncConfig, name string, depth int) {
	if depth > maxChainDepth {
		e.log.Warn("automation: not starting sync task, chain too deep (possible loop)", "task", name)
		return
	}
	var target *config.SyncConfig
	for _, c := range e.Configs() {
		// An automation only starts folders of the same owner: one user's
		// rules never trigger reads of another user's folders.
		if strings.EqualFold(c.Name, name) && strings.EqualFold(c.OwnerSID, from.OwnerSID) {
			cc := c
			target = &cc
		}
	}
	if target == nil {
		e.log.Warn("automation: sync configuration not found", "task", name)
		return
	}
	e.mu.Lock()
	if t, ok := e.lastStart[target.ID]; ok && time.Since(t) < startTaskCooldown {
		e.mu.Unlock()
		e.log.Info("automation: sync task started recently; skipping to avoid a loop", "task", name)
		return
	}
	e.lastStart[target.ID] = time.Now()
	r := e.run(target.ID)
	r.depth = depth
	e.mu.Unlock()
	go func() { _, _ = e.Scan(target.ID, true) }()
}

// finishRunIfIdle emits sync.completed / sync.failed when a configuration's
// queue drains after doing work.
func (e *Engine) finishRunIfIdle(cfgID string) {
	if e.st.PendingForConfig(cfgID) > 0 {
		return
	}
	e.mu.Lock()
	for _, a := range e.active {
		if a.item.ConfigID == cfgID {
			e.mu.Unlock()
			return
		}
	}
	r := e.runs[cfgID]
	if r == nil || !r.touched || e.scanning[cfgID] {
		e.mu.Unlock()
		return
	}
	delete(e.runs, cfgID)
	e.mu.Unlock()
	cfg, _ := e.config(cfgID)
	if cfg == nil || (r.uploaded == 0 && r.failed == 0) {
		return
	}
	evType := automation.EventSyncCompleted
	if r.failed > 0 {
		evType = automation.EventSyncFailed
	}
	ev := &automation.Event{Type: evType, Config: cfg.Name, Root: cfg.LocalPath, Destination: cfg.Destination, Device: e.deviceName, Now: time.Now(),
		Sync: &automation.SyncInfo{FilesUploaded: r.uploaded, FilesFailed: r.failed, BytesUploaded: r.bytes, Error: r.firstErr}}
	plan := automation.EvaluateAll(e.autosFor(cfg), ev)
	e.applySideEffects(cfg, plan, r.depth)
	e.refreshStatus()
}

// runSchedules fires schedule.interval triggers that are due.
func (e *Engine) runSchedules() {
	e.mu.Lock()
	autos := append([]automation.Named(nil), e.autos...)
	e.mu.Unlock()
	now := time.Now()
	for _, a := range autos {
		for _, n := range a.Automation.Nodes {
			if n.Type != automation.TypeTrigger || n.Kind != "schedule.interval" {
				continue
			}
			mins, _ := n.Params["every_minutes"].(float64)
			if mins < 5 {
				continue
			}
			if !e.scheduleDue(a.ID+"/"+n.ID, now, time.Duration(mins)*time.Minute) {
				continue
			}
			ev := &automation.Event{Type: automation.EventScheduleTick, TriggerNodeID: n.ID, Device: e.deviceName, Now: now, Destination: "/"}
			plan := automation.EvaluateAll([]automation.Named{a}, ev)
			e.applySideEffects(&config.SyncConfig{Name: "schedule", Destination: "/"}, plan, 0)
		}
	}
}

// scheduleMu serialises read-modify-write of schedule state.
var scheduleMu sync.Mutex

// scheduleDue reports whether a schedule trigger is due and, if so, records
// now as its last fire time. The time is kept in state.db so restarts
// neither re-fire nor skip runs (B3). A trigger seen for the first time
// starts its interval now instead of firing on startup. On a database error
// the tick is skipped rather than risk repeated firing.
func (e *Engine) scheduleDue(key string, now time.Time, every time.Duration) bool {
	scheduleMu.Lock()
	defer scheduleMu.Unlock()
	lastMS, ok, err := e.st.ScheduleLast(key)
	if err != nil {
		e.log.Warn("automation: could not read schedule state", "key", key, "err", err)
		return false
	}
	last := time.UnixMilli(lastMS)
	due := ok && now.Sub(last) >= every
	if ok && !due && !last.After(now) {
		return false
	}
	// First sighting, due, or a last fire in the future (clock moved back):
	// record now as the start of the next interval.
	if err := e.st.SetScheduleLast(key, now.UnixMilli()); err != nil {
		e.log.Warn("automation: could not save schedule state", "key", key, "err", err)
		return false
	}
	return due
}
