// Command vaultdock-desktop is the VaultDock Windows client.
package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/energye/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/platform"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/state"
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

	st, err := state.Open(filepath.Join(platform.DataDir(), "state.db"))
	if err != nil {
		log.Error("open state", "err", err)
		fmt.Fprintln(os.Stderr, "VaultDock could not open its state database:", err)
		os.Exit(1)
	}
	defer st.Close()

	eng := engine.New(st, log, platform.DeviceName())
	app := NewApp(log, st, eng)
	app.toasts = newToasts(log)
	eng.Notifier = app.toasts.Show

	startHidden := slices.Contains(os.Args[1:], "--minimized") || slices.Contains(os.Args[1:], "--updated") || (app.settings.StartMinimized && slices.Contains(os.Args[1:], "--autostart"))

	tray := &trayController{app: app}
	app.onSettings = func(s config.Settings) { tray.update() }

	err = wails.Run(&options.App{
		Title:             "VaultDock",
		Width:             1180,
		Height:            760,
		MinWidth:          920,
		MinHeight:         600,
		StartHidden:       startHidden,
		HideWindowOnClose: app.settings.CloseToTray,
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
			systray.Quit()
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "vaultdock-desktop-5c1f7e0a",
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
				wruntime.WindowUnminimise(app.ctx)
				wruntime.WindowShow(app.ctx)
			},
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

// trayController owns the notification-area icon. energye/systray runs its
// own message loop, so it lives on a dedicated locked OS thread.
type trayController struct {
	app   *App
	pause *systray.MenuItem
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
		_ = t.app.SetPaused(!t.app.eng.Paused())
		t.update()
		wruntime.EventsEmit(t.app.ctx, "settings")
	})
	web := systray.AddMenuItem("Open web interface", "Open VaultDock in your browser")
	web.Click(func() { t.app.OpenWebInterface("/") })
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit VaultDock", "Stop synchronising and exit")
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
	if t.app.eng.Paused() {
		t.pause.SetTitle("Resume synchronisation")
	} else {
		t.pause.SetTitle("Pause synchronisation")
	}
	snap := t.app.eng.Snapshot()
	tip := "VaultDock: " + snap.Message
	if len(tip) > 120 {
		tip = tip[:120]
	}
	systray.SetTooltip(tip)
}
