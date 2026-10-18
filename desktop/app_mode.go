package main

import (
	"context"
	"embed"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/agent"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/modules"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/platform"
	sdkmod "github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/modules"
)

// modeTextFS holds the install-mode info texts (P1-31). They are the single
// source of truth shared with the installer (MASTER_PLAN §7.7).
//
//go:embed build/windows/installer/modes/*.en.txt
var modeTextFS embed.FS

// ModeView is the install mode and module state the navigation is gated
// on. Source says where it came from:
//   - "agent": the agent's modules event (authoritative);
//   - "mode_file": config\mode.json with the mode's default modules, until
//     the agent API (GetMode), or the last modules event;
//   - "unknown": neither is available; the UI shows every screen.
type ModeView struct {
	Source      string               `json:"source"`
	InstallMode string               `json:"install_mode"`
	Modules     []modules.ModuleInfo `json:"modules"`
	// PendingRequest is a server mode change waiting for the local
	// administrator (agent source only).
	PendingRequest *modules.ModeRequest `json:"pending_request,omitempty"`
}

// uiWatch is the App's second agent subscription, for the backup and
// modules events the P0-29 bridge in app.go does not forward. It starts on
// the frontend's first GetModeState/KeyStatus/Readiness call.
var uiWatch struct {
	once sync.Once
	mu   sync.Mutex
	mods *modules.ModulesState
}

// watchBackupEvents forwards agent "modules" events as the Wails event
// "modules" (payload ModeView) and "backup" events as "backup" (payload
// agent.KeyStatus).
func (a *App) watchBackupEvents() {
	uiWatch.once.Do(func() {
		sub, err := a.api.Subscribe(a.bg, agent.KindModules, agent.KindBackup)
		if err != nil {
			a.log.Warn("backup/modules event subscription failed", "err", err)
			return
		}
		go func() {
			defer sub.Close()
			for ev := range sub.C() {
				switch ev.Kind {
				case agent.KindModules:
					var s modules.ModulesState
					if ev.Decode(&s) != nil {
						continue
					}
					uiWatch.mu.Lock()
					uiWatch.mods = &s
					uiWatch.mu.Unlock()
					a.emit("modules", viewFromState(s))
				case agent.KindBackup:
					a.emit("backup", ev.Data)
				}
			}
		}()
	})
}

func viewFromState(s modules.ModulesState) ModeView {
	return ModeView{Source: "agent", InstallMode: string(s.InstallMode), Modules: s.Modules, PendingRequest: s.PendingRequest}
}

// modeFileCandidates: the service's %ProgramData%\VaultDock\config\mode.json,
// or the in-process host's DataDir\config\mode.json.
func (a *App) modeFileCandidates() []string {
	machine := filepath.Join(platform.ConfigDir(), "mode.json")
	user := filepath.Join(platform.DataDir(), "config", "mode.json")
	if _, inproc := a.api.(*agent.InProc); inproc {
		return []string{user}
	}
	return []string{machine, user}
}

// GetModeState returns the install mode and enabled modules for the
// navigation (see ModeView).
func (a *App) GetModeState() ModeView {
	a.watchBackupEvents()
	ctx, cancel := context.WithTimeout(a.bg, 3*time.Second)
	st, err := a.api.GetMode(ctx)
	cancel()
	if err == nil {
		return viewFromState(st)
	}
	uiWatch.mu.Lock()
	s := uiWatch.mods
	uiWatch.mu.Unlock()
	if s != nil {
		return viewFromState(*s)
	}
	for _, p := range a.modeFileCandidates() {
		f, err := modules.ReadModeFile(p)
		if err != nil {
			continue
		}
		return modeFileView(f.Mode)
	}
	return ModeView{Source: "unknown"}
}

// modeFileView lists the mode's default module set as enabled.
func modeFileView(mode sdkmod.Mode) ModeView {
	now := time.Now().UnixMilli()
	v := ModeView{Source: "mode_file", InstallMode: string(mode)}
	for _, id := range sdkmod.ModulesForMode(mode) {
		k, _ := sdkmod.KindOf(id)
		v.Modules = append(v.Modules, modules.ModuleInfo{ID: id, Kind: k, State: modules.StateEnabled, UpdatedAt: now})
	}
	return v
}

// ModeTexts returns the plain-language mode texts by name: simple,
// advanced, compare and switching.
func (a *App) ModeTexts() map[string]string {
	out := map[string]string{}
	ents, err := modeTextFS.ReadDir("build/windows/installer/modes")
	if err != nil {
		return out
	}
	for _, e := range ents {
		b, err := modeTextFS.ReadFile("build/windows/installer/modes/" + e.Name())
		if err != nil {
			continue
		}
		out[strings.TrimSuffix(e.Name(), ".en.txt")] = strings.ReplaceAll(string(b), "\r\n", "\n")
	}
	return out
}
