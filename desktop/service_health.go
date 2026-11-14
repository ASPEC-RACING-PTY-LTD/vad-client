package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/service"
)

// ServiceHealth tells the UI whether the VaultDock background service can be
// reached and, when it cannot, why and what helps.
type ServiceHealth struct {
	Mode      string `json:"mode"`      // service or in-process
	Installed bool   `json:"installed"` // the VaultDockAgent service is registered
	State     string `json:"state"`     // running, stopped, start_pending, ...
	Reachable bool   `json:"reachable"` // the app is talking to it
	AgentPath string `json:"agent_path,omitempty"`
	Missing   bool   `json:"missing"` // the service program file is gone
	Problem   string `json:"problem,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

// serviceInstalled reports whether the agent service is registered on this
// PC (the per-machine install). The app then always talks to it and never
// starts a second, separate agent of its own.
func serviceInstalled() bool {
	st, err := service.Query(service.DefaultName)
	return err == nil && st.Installed
}

// agentBinary returns the program path from a service command line.
func agentBinary(cmdline string) string {
	cmdline = strings.TrimSpace(cmdline)
	if strings.HasPrefix(cmdline, `"`) {
		if i := strings.Index(cmdline[1:], `"`); i >= 0 {
			return cmdline[1 : i+1]
		}
	}
	if i := strings.Index(strings.ToLower(cmdline), ".exe"); i >= 0 {
		return cmdline[:i+4]
	}
	return cmdline
}

// ServiceHealth reports the background service's state.
func (a *App) ServiceHealth() ServiceHealth {
	h := ServiceHealth{Mode: a.agentMode}
	if a.agentMode != "service" {
		h.Reachable = true
		return h
	}
	st, err := service.Query(service.DefaultName)
	if err != nil {
		h.Problem = "Windows did not report the service's state: " + err.Error()
		return h
	}
	h.Installed, h.State = st.Installed, st.State
	_, err = a.api.Bootstrap(a.bg)
	h.Reachable = err == nil
	if !st.Installed {
		h.Problem = "The VaultDock background service is not installed."
		h.Hint = "Reinstall VaultDock to restore it."
		return h
	}
	h.AgentPath = agentBinary(st.BinaryPath)
	if h.AgentPath != "" {
		if _, err := os.Stat(h.AgentPath); os.IsNotExist(err) {
			h.Missing = true
			h.Problem = "The service program " + filepath.Base(h.AgentPath) + " is missing from " + filepath.Dir(h.AgentPath) + "."
			h.Hint = "Security software may have quarantined it. Open your antivirus (Windows Security, Malwarebytes, ...), restore " +
				filepath.Base(h.AgentPath) + " from quarantine and allow it, or reinstall VaultDock. Its checksum is published with every release."
			return h
		}
	}
	switch {
	case h.Reachable:
	case st.State == "running" || st.State == "start_pending":
		h.Problem = "The service is starting."
		h.Hint = "This usually takes a few seconds after Windows starts."
	default:
		h.Problem = "The VaultDock background service is " + strings.ReplaceAll(st.State, "_", " ") + "."
		h.Hint = "Start it (administrator approval needed). If it stops again, security software may be blocking it: allow VaultDock in your antivirus, or reinstall."
	}
	return h
}

// StartService asks Windows to start the background service, through the
// agent program's own "start" command run with administrator approval.
func (a *App) StartService() error {
	st, err := service.Query(service.DefaultName)
	if err != nil || !st.Installed {
		return uiError(err)
	}
	if service.StartService(service.DefaultName) == nil {
		return nil
	}
	return runElevated(agentBinary(st.BinaryPath), "start")
}
