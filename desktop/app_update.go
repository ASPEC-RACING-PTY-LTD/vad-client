package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/agent"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/updater"
)

// Updates run in the agent (internal/agent/updates.go); these bindings
// forward to it.

// GetUpdateStatus returns the current update state for Settings.
func (a *App) GetUpdateStatus() (updater.Status, error) { return a.api.UpdateStatus(a.bg) }

// CheckForUpdates checks now and, when a newer version exists, downloads and
// verifies it in the background so it is ready to install.
func (a *App) CheckForUpdates() (updater.Status, error) { return a.api.CheckForUpdates(a.bg) }

// InstallUpdate installs a downloaded update now. In-process the agent asks
// the UI to quit and launches the installer once it has stopped. With the
// service (updates_managed), the service never runs the installer itself, so
// the UI downloads and verifies it, starts it through the UAC prompt (the
// installer then reopens the app as this user) and closes.
func (a *App) InstallUpdate() error {
	err := a.api.InstallUpdate(a.bg)
	if err == nil || agent.AsError(err).Code != agent.CodeUpdatesManaged {
		return uiError(err)
	}
	if UpdateRepo == "" {
		return uiError(err)
	}
	ctx, cancel := context.WithTimeout(a.bg, 15*time.Minute)
	defer cancel()
	u := updater.New(UpdateRepo, version, filepath.Join(os.TempDir(), "VaultDock-update"), a.log)
	newer, cerr := u.Check(ctx)
	if cerr != nil {
		return cerr
	}
	if !newer {
		return errors.New("VaultDock is already up to date")
	}
	if err := u.Download(ctx); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	if err := u.Install(filepath.Dir(exe)); err != nil {
		return err
	}
	a.Quit()
	return nil
}
