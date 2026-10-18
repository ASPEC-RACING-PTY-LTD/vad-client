package main

import (
	"strings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/agent"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/platform"
)

// --- bootstrap and connection (agent) ---

func (a *App) GetBootstrap() (agent.Bootstrap, error) {
	b, err := a.api.Bootstrap(a.bg)
	if err == nil && a.localAutoStart {
		b.Settings.AutoStart = platform.AutoStartEnabled()
	}
	return b, err
}

// ProbeServer checks a server URL. req.Pin is a certificate fingerprint the
// user has explicitly chosen to trust (empty for normal CA validation).
func (a *App) ProbeServer(req agent.ProbeRequest) (agent.ProbeResult, error) {
	return a.api.ProbeServer(a.bg, req)
}

// Connect verifies credentials and starts device authorisation. The password
// is passed to the agent once and never stored.
func (a *App) Connect(req agent.ConnectRequest) (agent.EnrollView, error) {
	return a.api.Connect(a.bg, req)
}

func (a *App) CancelEnrollment() error { return a.api.CancelEnrollment(a.bg) }

// Disconnect removes this device's credentials. Sync configurations are kept
// so reconnecting is quick; nothing on the server is deleted.
func (a *App) Disconnect() error { return a.api.Disconnect(a.bg) }

// --- settings (agent; autostart is per user) ---

func (a *App) GetSettings() (config.Settings, error) {
	s, err := a.api.GetSettings(a.bg)
	if err == nil && a.localAutoStart {
		s.AutoStart = platform.AutoStartEnabled()
	}
	return s, err
}

func (a *App) SaveSettings(s config.Settings) (config.Settings, error) {
	out, err := a.api.SaveSettings(a.bg, s)
	if err != nil {
		return out, err
	}
	if a.localAutoStart {
		if aerr := platform.SetAutoStart(s.AutoStart); aerr != nil {
			a.log.Warn("autostart", "err", aerr)
		}
		out.AutoStart = s.AutoStart
	}
	return out, nil
}

// OpenWebInterface opens path on the connected server in the browser.
func (a *App) OpenWebInterface(path string) {
	ctx := a.wctx()
	if ctx == nil {
		return
	}
	s, err := a.api.GetSettings(a.bg)
	if err != nil || s.ServerURL == "" {
		return
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	wruntime.BrowserOpenURL(ctx, strings.TrimRight(s.ServerURL, "/")+path)
}
