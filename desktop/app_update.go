package main

import "github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/updater"

// Updates run in the agent (internal/agent/updates.go); these bindings
// forward to it.

// GetUpdateStatus returns the current update state for Settings.
func (a *App) GetUpdateStatus() (updater.Status, error) { return a.api.UpdateStatus(a.bg) }

// CheckForUpdates checks now and, when a newer version exists, downloads and
// verifies it in the background so it is ready to install.
func (a *App) CheckForUpdates() (updater.Status, error) { return a.api.CheckForUpdates(a.bg) }

// InstallUpdate installs a downloaded update now. In-process the agent asks
// the UI to quit and launches the installer once it has stopped.
func (a *App) InstallUpdate() error { return a.api.InstallUpdate(a.bg) }
