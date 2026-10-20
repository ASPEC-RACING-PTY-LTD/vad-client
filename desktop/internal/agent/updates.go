package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/ipc/caller"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/updater"
)

// When updates are checked:
//   - once at start (unless the last check was within the hour, so restarts
//     do not repeat it);
//   - every hour while the app window is open on screen, even if another
//     window is in front;
//   - every six hours while it is hidden (tray or taskbar) and the machine
//     is on;
//   - when a sync is queued for a changed file.
//
// Every trigger respects one shared "last checked" time: while the window is
// hidden nothing checks more often than every six hours (a start-up check
// followed by a sync a minute later is one check), and while it is open not
// more often than hourly. Opening the window checks when the last check is
// more than an hour old. The time is kept across restarts, and a failed
// check counts, so an offline machine does not retry constantly.
const (
	updateStartDelay  = 30 * time.Second
	updateOpen        = time.Hour
	updateHidden      = 6 * time.Hour
	updateTick        = time.Minute
	updateIdlePoll    = time.Minute
	updateLastCheckKV = "updates/last_check"
)

func (h *Host) updaterLocked() *updater.Updater {
	if h.upd == nil {
		h.upd = updater.New(h.opts.UpdateRepo, engine.Version, h.opts.UpdateDir, h.log)
		h.upd.OnChange = func(s updater.Status) { _ = h.bus.Publish(KindUpdate, s) }
	}
	return h.upd
}

func (h *Host) updater() *updater.Updater {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.updaterLocked()
}

func (h *Host) autoUpdateEnabled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.settings.AutoUpdate
}

// updateLoop checks for releases in the background. With automatic updates
// on, a new version is downloaded, verified and installed once sync is idle.
func (h *Host) updateLoop() {
	select {
	case <-h.ctx.Done():
		return
	case <-time.After(updateStartDelay): // let the network come up after sign-in
	}
	h.maybeCheckUpdates("start")
	tick := time.NewTicker(updateTick)
	defer tick.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-tick.C:
			h.maybeCheckUpdates("timer")
		}
	}
}

// maybeCheckUpdates runs a check when the interval for the window's state
// has passed since the last one. reason "start" checks unless the last check
// was within the hour.
func (h *Host) maybeCheckUpdates(reason string) {
	if !h.opts.AutoUpdates || h.opts.UpdateRepo == "" {
		return
	}
	h.mu.Lock()
	last := h.lastUpdateCheck
	if last.IsZero() && h.st != nil {
		if ms, err := strconv.ParseInt(h.st.Get(updateLastCheckKV), 10, 64); err == nil {
			last = time.UnixMilli(ms)
		}
	}
	if h.updateChecking || !updateDue(last, h.windowVisible, reason, time.Now()) {
		h.mu.Unlock()
		return
	}
	h.updateChecking = true
	h.lastUpdateCheck = time.Now()
	h.mu.Unlock()
	if h.st != nil {
		h.st.Set(updateLastCheckKV, strconv.FormatInt(time.Now().UnixMilli(), 10))
	}
	h.log.Info("checking for updates", "reason", reason)
	h.goLoop(func() {
		defer func() {
			h.mu.Lock()
			h.updateChecking = false
			h.mu.Unlock()
		}()
		h.updateOnce(h.updater())
	})
}

// updateDue reports whether a check is due: hourly while the window is open
// (and at start), every six hours while it is hidden.
func updateDue(last time.Time, visible bool, reason string, now time.Time) bool {
	interval := updateHidden
	if visible || reason == "start" {
		interval = updateOpen
	}
	return last.IsZero() || now.Sub(last) >= interval
}

// SetWindowVisible records whether the app window is open on screen; opening
// it checks for updates when the last check is more than an hour old.
func (h *Host) SetWindowVisible(ctx context.Context, visible bool) error {
	if err := h.live(); err != nil {
		return err
	}
	h.mu.Lock()
	h.windowVisible = visible
	h.mu.Unlock()
	if visible {
		h.maybeCheckUpdates("window")
	}
	return nil
}

func (h *Host) updateOnce(u *updater.Updater) {
	avail, err := u.Check(h.ctx)
	if err != nil {
		h.log.Warn("update check failed", "err", err)
		return
	}
	if !avail || !h.autoUpdateEnabled() {
		if avail {
			h.notifyAvailableOnce(u.Status().Latest)
		}
		return
	}
	if err := u.Download(h.ctx); err != nil {
		h.log.Warn("update download failed", "err", err)
		return
	}
	// Wait until nothing is uploading so no transfer is interrupted.
	for !h.syncIdle() {
		if !h.autoUpdateEnabled() {
			return
		}
		select {
		case <-h.ctx.Done():
			return
		case <-time.After(updateIdlePoll):
		}
	}
	if err := h.requestInstall(); err != nil {
		h.log.Warn("automatic update could not start", "err", err)
	}
}

func (h *Host) notifyAvailableOnce(v string) {
	h.mu.Lock()
	seen := v == "" || v == h.notifiedUpdate
	h.notifiedUpdate = v
	h.mu.Unlock()
	if !seen {
		h.notify("VaultDock "+v+" is available", "Open Settings to install the update.", "info")
	}
}

func (h *Host) syncIdle() bool {
	snap := h.eng.Snapshot()
	return snap.Stats.Active == 0 && (snap.Stats.Queued == 0 || snap.Paused || !snap.Connected)
}

// requestInstall marks the verified update for installation at Close and
// asks the owner to stop the host.
func (h *Host) requestInstall() error {
	if h.serviceMode() {
		return ErrUpdatesManaged
	}
	u := h.updater()
	if !u.Ready() {
		return errors.New("no verified update is ready")
	}
	if h.opts.RequestExit == nil {
		return errors.New("this agent cannot restart itself to install updates")
	}
	h.log.Info("installing update", "version", u.Status().Latest)
	h.notify("Updating VaultDock", "VaultDock restarts on version "+u.Status().Latest+" in a few seconds.", "info")
	h.mu.Lock()
	h.installPending = true
	h.mu.Unlock()
	go h.opts.RequestExit()
	return nil
}

// launchPendingUpdate runs from Close, after the engine has stopped.
func (h *Host) launchPendingUpdate() {
	h.mu.Lock()
	u := h.upd
	h.mu.Unlock()
	if u == nil {
		return
	}
	if h.serviceMode() {
		// Never run the per-user installer as SYSTEM.
		h.log.Error("update: refusing to run the per-user installer in service mode")
		return
	}
	dir := h.opts.InstallDir
	if dir == "" {
		exe, err := os.Executable()
		if err == nil {
			exe, err = filepath.EvalSymlinks(exe)
		}
		if err != nil {
			h.log.Error("update: cannot locate the install directory", "err", err)
			return
		}
		dir = filepath.Dir(exe)
	}
	if err := u.Install(dir); err != nil {
		h.log.Error("update: installer did not start", "err", err)
	}
}

// CodeUpdatesManaged is the error code InstallUpdate returns in service
// mode: the machine-wide service is updated by the administrator installer,
// and the per-user installer must never run as SYSTEM.
const CodeUpdatesManaged = "updates_managed"

// ErrUpdatesManaged is the InstallUpdate refusal in service mode.
var ErrUpdatesManaged = &Error{Code: CodeUpdatesManaged,
	Message: "updates for the VaultDock service are installed by the administrator installer"}

// runningAsSystem reports whether this process is LocalSystem; a variable
// so tests can override it.
var runningAsSystem = processIsSystem

// serviceMode reports whether this host is the machine-wide service.
func (h *Host) serviceMode() bool { return h.opts.Service || runningAsSystem() }

// --- API ---

func (h *Host) UpdateStatus(ctx context.Context) (updater.Status, error) {
	if err := h.live(); err != nil {
		return updater.Status{}, err
	}
	return h.updater().Status(), nil
}

func (h *Host) CheckForUpdates(ctx context.Context) (updater.Status, error) {
	if err := h.live(); err != nil {
		return updater.Status{}, err
	}
	u := h.updater()
	avail, err := u.Check(ctx)
	if err != nil {
		return u.Status(), unavailable(friendlyNetErr(err))
	}
	if avail {
		h.goLoop(func() {
			if err := u.Download(h.ctx); err != nil {
				h.log.Warn("update download failed", "err", err)
			}
		})
	}
	return u.Status(), nil
}

func (h *Host) InstallUpdate(ctx context.Context) error {
	if err := h.live(); err != nil {
		return err
	}
	// A pipe call means this host is the agent process: the installer would
	// run with the agent's identity, not the caller's.
	if _, piped := caller.From(ctx); piped || h.serviceMode() {
		return ErrUpdatesManaged
	}
	u := h.updater()
	if h.opts.UpdateRepo == "" {
		return &Error{Code: CodeUnavailable, Message: "updates are disabled in this build"}
	}
	if !u.Ready() {
		if err := u.Download(ctx); err != nil {
			return unavailable(err)
		}
	}
	if err := h.requestInstall(); err != nil {
		return unavailable(err)
	}
	return nil
}
