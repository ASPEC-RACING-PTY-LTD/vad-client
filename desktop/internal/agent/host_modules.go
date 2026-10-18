package agent

import (
	"path/filepath"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/modules"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/client"
)

// KindModules carries modules.ModulesState whenever the install mode,
// a module or a pending mode change request changes (DSK-MOD, P1-28).
const KindModules EventKind = "modules"

// modeFilePath is the installer's mode file: Options.ModeFile, else
// DataDir\config\mode.json (= platform.ConfigDir()\mode.json for the
// service, whose DataDir is %ProgramData%\VaultDock).
func (h *Host) modeFilePath() string {
	if h.opts.ModeFile != "" {
		return h.opts.ModeFile
	}
	return filepath.Join(h.opts.DataDir, "config", "mode.json")
}

// startModules creates the module manager (which resolves the install mode,
// including the upgrade default, and persists mode.json), registers the
// sync folder source and starts the PUT /device/modules report loop. A
// state.db failure leaves modules unavailable (logged) without failing Start.
func (h *Host) startModules() {
	m := modules.NewForHost(h.st.DB, h.modeFilePath(), engine.Version, h.currentClient,
		func(p any) { _ = h.bus.Publish(KindModules, p) })
	if m == nil {
		h.log.Warn("module manager unavailable")
		return
	}
	m.RegisterSource(modules.NewSyncFolderSource(h.st.DB, h.eng, nil))
	h.mu.Lock()
	h.mods = m
	h.mu.Unlock()
	h.goLoop(func() { m.RunReporter(h.ctx) })
}

// Modules returns the module manager, or nil before Start or when it is
// unavailable.
func (h *Host) Modules() *modules.Manager {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.mods
}

// currentClient returns the enrolled server client (nil while not
// connected); the module report sender calls it per report.
func (h *Host) currentClient() *client.Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.client
}

// kickModules asks the report loop to report now (after a connection or
// enrolment), instead of waiting out the not-connected backoff.
func (h *Host) kickModules() {
	if m := h.Modules(); m != nil {
		m.Kick()
	}
}
