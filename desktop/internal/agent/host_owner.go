package agent

import (
	"context"
	"encoding/json"
	"os/user"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/ipc/caller"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/tokens"
)

// Sync configurations are owned by one Windows user (MASTER_PLAN §7.8). A
// pipe client sees, edits, triggers and watches only the configurations it
// owns; in-process and SYSTEM callers see all of them. In the service the
// engine reads every configuration's files with its owner's token
// (engine.SetTokens), never with SYSTEM's rights.

// configScope is what a caller may see of the sync configurations.
type configScope struct {
	all bool   // in-process or SYSTEM caller
	sid string // the pipe caller's SID otherwise
}

func (h *Host) scopeOf(ctx context.Context) configScope {
	c, ok := caller.From(ctx)
	if !ok || IsSystem(c) {
		return configScope{all: true}
	}
	return configScope{sid: c.SID}
}

func (s configScope) owns(c config.SyncConfig) bool {
	return s.all || (s.sid != "" && strings.EqualFold(c.OwnerSID, s.sid))
}

// ownedIDs returns the ids of the configurations s may see; nil for all.
func (h *Host) ownedIDs(s configScope) []string {
	if s.all {
		return nil
	}
	ids := []string{}
	for _, c := range h.eng.Configs() {
		if s.owns(c) {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// visibleConfig returns configuration id if s may see it, else not_found.
func (h *Host) visibleConfig(s configScope, id string) (config.SyncConfig, error) {
	for _, c := range h.eng.Configs() {
		if c.ID == id && s.owns(c) {
			return c, nil
		}
	}
	return config.SyncConfig{}, &Error{Code: CodeNotFound, Message: "no such configuration"}
}

// setOwner decides OwnerSID for a configuration being saved by s. prev is
// the stored version for an update (nil for a new one).
func (h *Host) setOwner(s configScope, c *config.SyncConfig, prev *config.SyncConfig) error {
	if !s.all {
		c.OwnerSID = s.sid // a pipe client never chooses the owner
		return nil
	}
	c.OwnerSID = strings.TrimSpace(c.OwnerSID)
	if c.OwnerSID == "" && prev != nil {
		c.OwnerSID = prev.OwnerSID
	}
	if c.OwnerSID == "" && !h.serviceMode() {
		// The per-user app runs as the user: record them so the folder
		// keeps its owner when moved to the service.
		if u, err := user.Current(); err == nil && tokens.IsUserSID(u.Uid) {
			c.OwnerSID = u.Uid
		}
	}
	if c.OwnerSID != "" && !tokens.IsUserSID(c.OwnerSID) {
		return invalidf("the owner must be a user account")
	}
	return nil
}

// assignOwners gives configurations without an owner the user whose
// profile folder contains all of their paths. It runs at service start,
// before the engine loads them; a configuration that matches no single
// profile stays unowned and is not synchronised (engine.ErrNoOwner).
func (h *Host) assignOwners() {
	cfgs, err := h.st.Configs()
	if err != nil {
		h.log.Warn("owner assignment: cannot read configurations", "err", err)
		return
	}
	var unowned []config.SyncConfig
	for _, c := range cfgs {
		if strings.TrimSpace(c.OwnerSID) == "" {
			unowned = append(unowned, c)
		}
	}
	if len(unowned) == 0 {
		return
	}
	list, err := h.opts.Profiles.LocalProfiles()
	if err != nil {
		h.log.Warn("owner assignment: cannot list profiles", "err", err)
		return
	}
	for _, c := range unowned {
		owner := ""
		for _, p := range list {
			if !tokens.IsUserSID(p.SID) || p.ProfilePath == "" {
				continue
			}
			inside := true
			for _, path := range configPaths(c) {
				if !underDir(path, p.ProfilePath) {
					inside = false
					break
				}
			}
			if inside {
				owner = p.SID
				break
			}
		}
		if owner == "" {
			h.log.Warn("configuration has no owner and is not synchronised until its user saves it again", "config", c.ID, "name", c.Name)
			continue
		}
		c.OwnerSID = owner
		if err := h.st.SaveConfig(c); err != nil {
			h.log.Warn("owner assignment: save failed", "config", c.ID, "err", err)
			continue
		}
		h.log.Info("configuration owner assigned from its profile folder", "config", c.ID, "owner", owner)
	}
}

// underDir reports whether path is dir or inside it (case-insensitive).
func underDir(path, dir string) bool {
	p := strings.ToLower(filepath.Clean(path))
	d := strings.ToLower(filepath.Clean(dir))
	if p == d {
		return true
	}
	return strings.HasPrefix(p, strings.TrimRight(d, `\/`)+string(filepath.Separator))
}

// filterSnapshot drops the active transfers of configurations s may not see.
func (h *Host) filterSnapshot(s configScope, snap engine.Snapshot) engine.Snapshot {
	if s.all {
		return snap
	}
	ids := map[string]bool{}
	for _, id := range h.ownedIDs(s) {
		ids[id] = true
	}
	out := make([]engine.ActiveView, 0, len(snap.Active))
	for _, a := range snap.Active {
		if ids[a.ConfigID] {
			out = append(out, a)
		}
	}
	snap.Active = out
	return snap
}

// scopedEvents rewrites or drops bus events for a pipe subscriber: snapshot
// transfers and sync issues of other users' configurations are removed, and
// the agent log (which names other users' files) only reaches
// administrators.
func (h *Host) scopedEvents(ctx context.Context, inner Subscription) Subscription {
	s := h.scopeOf(ctx)
	if s.all {
		return inner
	}
	c, _ := caller.From(ctx)
	admin := IsAdmin(c)
	fs := &filteredSub{inner: inner, ch: make(chan Event, cap(inner.C())), done: make(chan struct{})}
	go func() {
		defer close(fs.ch)
		for ev := range inner.C() {
			ev, ok := h.scopeEvent(s, admin, ev)
			if !ok {
				continue
			}
			select {
			case fs.ch <- ev:
			case <-fs.done:
				return
			}
		}
	}()
	return fs
}

func (h *Host) scopeEvent(s configScope, admin bool, ev Event) (Event, bool) {
	switch ev.Kind {
	case KindLog:
		return ev, admin
	case KindNotification:
		var n Notification
		if err := ev.Decode(&n); err != nil {
			return ev, false
		}
		return ev, n.OwnerSID == "" || strings.EqualFold(n.OwnerSID, s.sid)
	case KindSnapshot:
		var snap engine.Snapshot
		if err := ev.Decode(&snap); err != nil {
			return ev, false
		}
		b, err := json.Marshal(h.filterSnapshot(s, snap))
		if err != nil {
			return ev, false
		}
		ev.Data = b
		return ev, true
	case KindIssue:
		var ie IssueEvent
		if err := ev.Decode(&ie); err != nil {
			return ev, false
		}
		if ie.ConfigID == "" {
			return ev, true
		}
		for _, id := range h.ownedIDs(s) {
			if id == ie.ConfigID {
				return ev, true
			}
		}
		return ev, false
	}
	return ev, true
}

// filteredSub is a Subscription fed through Host.scopeEvent.
type filteredSub struct {
	inner Subscription
	ch    chan Event
	done  chan struct{}
	once  sync.Once
}

func (f *filteredSub) C() <-chan Event { return f.ch }
func (f *filteredSub) Dropped() uint64 { return f.inner.Dropped() }
func (f *filteredSub) Close() {
	f.once.Do(func() { close(f.done) })
	f.inner.Close()
}
