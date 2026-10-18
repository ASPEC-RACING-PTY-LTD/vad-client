package main

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/agent"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/platform"
)

// UpdateRepo is the GitHub repository checked for new releases
// ("owner/name"). Override at build time; empty disables checks.
var UpdateRepo = "ASPEC-RACING-PTY-LTD/VaultDock"

// App is bound to the frontend. It keeps only UI-session concerns (dialogs,
// windows, the tray, opening folders and URLs) and forwards everything else
// to the agent: the VaultDockAgent service over IPC, or an in-process
// agent.Host. Exactly one engine runs, inside the agent.
type App struct {
	log    *slog.Logger
	api    agent.API
	toasts *toasts

	// bg bounds agent calls and the event bridge; cancelled on shutdown.
	bg     context.Context
	cancel context.CancelFunc

	// localAutoStart: the agent does not manage this user's autostart
	// (service mode), so the UI applies the setting to its own Run key.
	localAutoStart bool

	mu         sync.Mutex
	ctx        context.Context // Wails runtime context, set in startup
	snap       engine.Snapshot // last snapshot event (tray)
	haveSnap   bool
	onSettings func(config.Settings)
}

func NewApp(log *slog.Logger, api agent.API) *App {
	bg, cancel := context.WithCancel(context.Background())
	return &App{log: log, api: api, bg: bg, cancel: cancel}
}

// wctx returns the Wails runtime context (nil before startup).
func (a *App) wctx() context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ctx
}

func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	go a.bridgeEvents()
	if slices.Contains(os.Args[1:], "--updated") && UpdateRepo != "" && a.toasts != nil {
		a.toasts.Show("VaultDock updated", "You are now running version "+engine.Version+".", "info")
	}
}

func (a *App) shutdown(ctx context.Context) {
	a.cancel()
}

// bridgedKinds are the agent events forwarded to the frontend (log events
// stay in the agent's log).
var bridgedKinds = []agent.EventKind{
	agent.KindSnapshot, agent.KindConnection, agent.KindEnrollment, agent.KindSettings,
	agent.KindUpdate, agent.KindNotification, agent.KindShutdown,
}

// bridgeEvents re-emits agent bus events as the Wails events the frontend
// listens to: snapshot, connected / revoked / disconnected, enrollment,
// settings and update. Notifications become Windows toasts.
func (a *App) bridgeEvents() {
	sub, err := a.api.Subscribe(a.bg, bridgedKinds...)
	if err != nil {
		a.log.Error("agent event subscription failed", "err", err)
		return
	}
	defer sub.Close()
	for ev := range sub.C() {
		a.handleEvent(ev)
	}
}

func (a *App) emit(name string, data ...any) {
	if ctx := a.wctx(); ctx != nil {
		wruntime.EventsEmit(ctx, name, data...)
	}
}

func (a *App) handleEvent(ev agent.Event) {
	switch ev.Kind {
	case agent.KindSnapshot:
		var s engine.Snapshot
		if ev.Decode(&s) == nil {
			a.mu.Lock()
			a.snap, a.haveSnap = s, true
			a.mu.Unlock()
		}
		a.emit("snapshot", ev.Data)
	case agent.KindConnection:
		var c agent.ConnectionEvent
		if err := ev.Decode(&c); err != nil || c.State == "" {
			return
		}
		a.emit(c.State) // connected, disconnected, revoked
	case agent.KindEnrollment, agent.KindUpdate:
		a.emit(string(ev.Kind), ev.Data)
	case agent.KindSettings:
		var s config.Settings
		if ev.Decode(&s) == nil {
			a.mu.Lock()
			a.snap.Paused = s.Paused
			cb := a.onSettings
			a.mu.Unlock()
			if cb != nil {
				cb(s)
			}
		}
		a.emit("settings", ev.Data)
	case agent.KindNotification:
		var n agent.Notification
		if ev.Decode(&n) == nil && a.toasts != nil {
			a.toasts.Show(n.Title, n.Message, n.Level)
		}
	case agent.KindShutdown:
		var s agent.ShutdownEvent
		_ = ev.Decode(&s)
		a.log.Info("agent shutting down", "reason", s.Reason)
	}
}

// currentSnapshot returns the last snapshot event, asking the agent when
// none has arrived yet.
func (a *App) currentSnapshot() engine.Snapshot {
	a.mu.Lock()
	s, ok := a.snap, a.haveSnap
	a.mu.Unlock()
	if ok {
		return s
	}
	if s, err := a.api.Snapshot(a.bg); err == nil {
		return s
	}
	return s
}

// --- UI-session helpers (not part of agent.API) ---

func (a *App) ShowWindow() {
	if ctx := a.wctx(); ctx != nil {
		wruntime.WindowUnminimise(ctx)
		wruntime.WindowShow(ctx)
	}
}

// Quit closes the UI. In-process, the agent stops with it; the service keeps
// synchronising.
func (a *App) Quit() {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil {
		wruntime.Quit(ctx)
	}
}

func (a *App) OpenURL(u string) {
	if ctx := a.wctx(); ctx != nil && strings.HasPrefix(u, "https://") {
		wruntime.BrowserOpenURL(ctx, u)
	}
}

func (a *App) OpenFolder(p string) {
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		openFolder(p)
	}
}

func (a *App) OpenLogsFolder() { openFolder(platform.LogDir()) }

func openFolder(p string) {
	_ = os.MkdirAll(p, 0o700)
	// explorer.exe with a single, validated directory argument (no shell).
	_ = exec.Command("explorer.exe", filepath.Clean(p)).Start()
}
