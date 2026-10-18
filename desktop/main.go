// Command vaultdock-desktop is the VaultDock Windows client.
package main

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/energye/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/agent"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/ipc"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/platform"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed assets/tray.ico
var trayIcon []byte

// version is set at build time with -ldflags "-X main.version=x.y.z".
var version = "0.1.0"

func main() {
	engine.Version = version
	if generatingBindings {
		_ = wails.Run(&options.App{Title: "VaultDock", Bind: []interface{}{&App{}}})
		return
	}
	if err := os.MkdirAll(platform.DataDir(), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	log, closer := newLogger(platform.LogDir())
	defer closer.Close()
	log.Info("VaultDock desktop starting", "version", version, "data", platform.DataDir())

	// Reach the agent: the VaultDockAgent service over the named pipe when it
	// is running, otherwise an agent.Host inside this process (dev mode and
	// the per-user install). Exactly one engine runs, inside the agent.
	var appRef atomic.Pointer[App]
	quit := func() {
		if a := appRef.Load(); a != nil {
			a.Quit()
		}
	}
	ag, err := connectAgent(log, quit)
	if err != nil {
		log.Error("start agent", "err", err)
		fmt.Fprintln(os.Stderr, "VaultDock could not start its sync agent:", err)
		os.Exit(1)
	}
	defer ag.close()

	app := NewApp(log, ag.api)
	app.localAutoStart = ag.mode == "service"
	app.toasts = newToasts(log)
	appRef.Store(app)

	settings, err := app.GetSettings()
	if err != nil {
		log.Warn("read settings from agent", "err", err)
		settings = config.DefaultSettings()
	}
	startHidden := slices.Contains(os.Args[1:], "--minimized") || slices.Contains(os.Args[1:], "--updated") || (settings.StartMinimized && slices.Contains(os.Args[1:], "--autostart"))

	tray := &trayController{app: app, service: ag.mode == "service"}
	app.onSettings = func(s config.Settings) { tray.update() }

	err = wails.Run(&options.App{
		Title:             "VaultDock",
		Width:             1180,
		Height:            760,
		MinWidth:          920,
		MinHeight:         600,
		StartHidden:       startHidden,
		HideWindowOnClose: settings.CloseToTray,
		BackgroundColour:  &options.RGBA{R: 248, G: 250, B: 252, A: 255},
		AssetServer:       &assetserver.Options{Assets: assets},
		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
			go tray.run()
		},
		OnBeforeClose: func(ctx context.Context) bool {
			// With close-to-tray the window hides instead (HideWindowOnClose).
			return false
		},
		OnShutdown: func(ctx context.Context) {
			app.shutdown(ctx)
			// In-process this stops the engine and launches a pending
			// update installer.
			ag.close()
			systray.Quit()
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "vaultdock-desktop-5c1f7e0a",
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) { app.ShowWindow() },
		},
		Bind: []interface{}{app},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			Theme:                windows.SystemDefault,
		},
	})
	if err != nil {
		log.Error("wails", "err", err)
		fmt.Fprintln(os.Stderr, "Error:", err.Error())
	}
}

// agentConn is how this UI process reaches the agent.
type agentConn struct {
	api   agent.API // the IPC client (service) or an InProc over a local Host
	mode  string    // "service" or "in-process"
	close func()    // idempotent
}

// connectAgent dials the agent service and falls back to an in-process
// agent. A user-owned process squatting the pipe name is rejected
// (RequireSystemServer) unless VAULTDOCK_DATA_DIR marks a development setup,
// where the agent runs as the user. quit is the in-process agent's
// RequestExit hook (it closes the UI, which stops the agent).
func connectAgent(log *slog.Logger, quit func()) (*agentConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	opts := ipc.ClientOptions{Log: log}
	if os.Getenv(platform.EnvDataDir) == "" {
		opts.VerifyServer = ipc.RequireSystemServer
	}
	c, err := ipc.Dial(ctx, opts)
	if err == nil {
		log.Info("agent mode: service", "pipe", ipc.PipeName)
		return &agentConn{api: c, mode: "service", close: sync.OnceFunc(func() { _ = c.Close() })}, nil
	}
	log.Info("agent mode: in-process", "reason", err.Error())
	api, stop, err := newInProcAgent(context.Background(), log, quit)
	if err != nil {
		return nil, err
	}
	return &agentConn{api: api, mode: "in-process", close: sync.OnceFunc(stop)}, nil
}

// newInProcAgent starts an agent.Host inside the UI process with the
// per-user data directory and hooks. The caller calls the returned stop
// function on shutdown.
func newInProcAgent(ctx context.Context, log *slog.Logger, quit func()) (agent.API, func(), error) {
	h, err := agent.NewHost(agent.Options{
		Log:              log,
		AutoStart:        platform.SetAutoStart,
		AutoStartEnabled: platform.AutoStartEnabled,
		UpdateRepo:       UpdateRepo,
		UpdateDir:        filepath.Join(filepath.Dir(platform.LogDir()), "updates"),
		AutoUpdates:      true,
		RequestExit:      quit,
	})
	if err != nil {
		return nil, nil, err
	}
	if err := h.Start(ctx); err != nil {
		_ = h.Close()
		return nil, nil, err
	}
	return agent.NewInProc(h), func() { _ = h.Close() }, nil
}

// trayController owns the notification-area icon. energye/systray runs its
// own message loop, so it lives on a dedicated locked OS thread.
type trayController struct {
	app     *App
	service bool // the agent service keeps syncing after the UI quits
	pause   *systray.MenuItem
}

func (t *trayController) run() {
	runtime.LockOSThread()
	systray.Run(t.onReady, func() {})
}

func (t *trayController) onReady() {
	systray.SetIcon(trayIcon)
	systray.SetTitle("VaultDock")
	systray.SetTooltip("VaultDock")
	systray.SetOnClick(func(menu systray.IMenu) { t.app.ShowWindow() })
	systray.SetOnDClick(func(menu systray.IMenu) { t.app.ShowWindow() })
	systray.SetOnRClick(func(menu systray.IMenu) { menu.ShowMenu() })

	open := systray.AddMenuItem("Open VaultDock", "Show the VaultDock window")
	open.Click(t.app.ShowWindow)
	syncNow := systray.AddMenuItem("Sync all now", "Scan every enabled configuration now")
	syncNow.Click(func() { _ = t.app.SyncNow("") })
	t.pause = systray.AddMenuItem("Pause synchronisation", "Pause or resume uploads")
	t.pause.Click(func() {
		// The agent publishes a settings event, which updates the tray and
		// the window.
		_ = t.app.SetPaused(!t.app.currentSnapshot().Paused)
	})
	web := systray.AddMenuItem("Open web interface", "Open VaultDock in your browser")
	web.Click(func() { t.app.OpenWebInterface("/") })
	systray.AddSeparator()
	quitTip := "Stop synchronising and exit"
	if t.service {
		quitTip = "Close VaultDock; the VaultDock service keeps synchronising"
	}
	quit := systray.AddMenuItem("Quit VaultDock", quitTip)
	quit.Click(t.app.Quit)
	t.update()
	go func() {
		for range time.NewTicker(5 * time.Second).C {
			t.update()
		}
	}()
}

func (t *trayController) update() {
	if t.pause == nil {
		return
	}
	snap := t.app.currentSnapshot()
	if snap.Paused {
		t.pause.SetTitle("Resume synchronisation")
	} else {
		t.pause.SetTitle("Pause synchronisation")
	}
	tip := "VaultDock: " + snap.Message
	if len(tip) > 120 {
		tip = tip[:120]
	}
	systray.SetTooltip(tip)
}
