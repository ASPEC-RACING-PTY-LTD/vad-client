package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/platform"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/updater"
)

const (
	updateFirstCheck = 2 * time.Minute
	updateInterval   = 6 * time.Hour
	updateIdlePoll   = time.Minute
)

func (a *App) updaterOrNil() *updater.Updater {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.upd
}

func (a *App) initUpdater() *updater.Updater {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.upd == nil {
		dir := filepath.Join(filepath.Dir(platform.LogDir()), "updates")
		a.upd = updater.New(UpdateRepo, engine.Version, dir, a.log)
		a.upd.OnChange = func(s updater.Status) {
			if a.ctx != nil {
				wruntime.EventsEmit(a.ctx, "update", s)
			}
		}
	}
	return a.upd
}

func (a *App) autoUpdateEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings.AutoUpdate
}

// runUpdates checks for releases in the background. With automatic updates
// on, a new version is downloaded, verified and installed as soon as sync is
// idle; the app restarts on the new version within seconds.
func (a *App) runUpdates() {
	u := a.initUpdater()
	if UpdateRepo == "" {
		return
	}
	if slices.Contains(os.Args[1:], "--updated") {
		a.toasts.Show("VaultDock updated", "You are now running version "+engine.Version+".", "info")
	}
	timer := time.NewTimer(updateFirstCheck)
	defer timer.Stop()
	for range timer.C {
		a.updateOnce(context.Background(), u)
		timer.Reset(updateInterval)
	}
}

func (a *App) updateOnce(ctx context.Context, u *updater.Updater) {
	avail, err := u.Check(ctx)
	if err != nil {
		a.log.Warn("update check failed", "err", err)
		return
	}
	if !avail || !a.autoUpdateEnabled() {
		if avail {
			a.notifyAvailableOnce(u.Status().Latest)
		}
		return
	}
	if err := u.Download(ctx); err != nil {
		a.log.Warn("update download failed", "err", err)
		return
	}
	// Wait until nothing is uploading so no transfer is interrupted.
	for !a.syncIdle() {
		if !a.autoUpdateEnabled() {
			return
		}
		time.Sleep(updateIdlePoll)
	}
	if err := a.installAndRestart(); err != nil {
		a.log.Warn("automatic update could not start", "err", err)
	}
}

func (a *App) notifyAvailableOnce(v string) {
	a.mu.Lock()
	seen := v == "" || v == a.notifiedUpdate
	a.notifiedUpdate = v
	a.mu.Unlock()
	if seen {
		return
	}
	a.toasts.Show("VaultDock "+v+" is available", "Open Settings to install the update.", "info")
}

func (a *App) syncIdle() bool {
	snap := a.eng.Snapshot()
	return snap.Stats.Active == 0 && (snap.Stats.Queued == 0 || snap.Paused || !snap.Connected)
}

// installAndRestart shuts the app down cleanly; the verified installer is
// started from shutdown once the sync engine has stopped.
func (a *App) installAndRestart() error {
	u := a.updaterOrNil()
	if u == nil || !u.Ready() {
		return errors.New("no verified update is ready")
	}
	a.log.Info("installing update", "version", u.Status().Latest)
	a.toasts.Show("Updating VaultDock", "VaultDock restarts on version "+u.Status().Latest+" in a few seconds.", "info")
	a.mu.Lock()
	a.installUpdate = true
	a.quitting = true
	a.mu.Unlock()
	wruntime.Quit(a.ctx)
	return nil
}

// launchPendingUpdate runs during shutdown, after the engine has stopped.
func (a *App) launchPendingUpdate() {
	a.mu.Lock()
	pending, u := a.installUpdate, a.upd
	a.mu.Unlock()
	if !pending || u == nil {
		return
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		a.log.Error("update: cannot locate the install directory", "err", err)
		return
	}
	if err := u.Install(filepath.Dir(exe)); err != nil {
		a.log.Error("update: installer did not start", "err", err)
	}
}

// --- bindings ---

// GetUpdateStatus returns the current update state for Settings.
func (a *App) GetUpdateStatus() updater.Status {
	return a.initUpdater().Status()
}

// CheckForUpdates checks now and, when a newer version exists, downloads and
// verifies it in the background so it is ready to install.
func (a *App) CheckForUpdates() (updater.Status, error) {
	u := a.initUpdater()
	avail, err := u.Check(context.Background())
	if err != nil {
		return u.Status(), friendlyNetErr(err)
	}
	if avail {
		go func() {
			if err := u.Download(context.Background()); err != nil {
				a.log.Warn("update download failed", "err", err)
			}
		}()
	}
	return u.Status(), nil
}

// InstallUpdate installs a downloaded update now and restarts the app.
func (a *App) InstallUpdate() error {
	u := a.initUpdater()
	if !u.Ready() {
		if err := u.Download(context.Background()); err != nil {
			return err
		}
	}
	return a.installAndRestart()
}
