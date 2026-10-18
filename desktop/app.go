package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/platform"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/updater"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/protocol"
)

// UpdateRepo is the GitHub repository checked for new releases
// ("owner/name"). Override at build time; empty disables checks.
var UpdateRepo = "ASPEC-RACING-PTY-LTD/VaultDock"

// App is bound to the frontend.
type App struct {
	ctx    context.Context
	log    *slog.Logger
	st     *state.Store
	eng    *engine.Engine
	toasts *toasts

	mu         sync.Mutex
	settings   config.Settings
	client     *client.Client
	me         *client.Me
	enroll     *enrollment
	quitting   bool
	revoked    bool
	onSettings func(config.Settings)

	upd            *updater.Updater
	installUpdate  bool   // launch the downloaded installer once shut down
	notifiedUpdate string // last version announced in a notification
}

type enrollment struct {
	cancel      context.CancelFunc
	View        EnrollView
	key         ed25519.PrivateKey
	serverURL   string
	pin         string
	pollToken   string
	requestID   int64
	clientProbe *client.Client
}

// EnrollView is shown while waiting for approval.
type EnrollView struct {
	Status         string `json:"status"` // pending, approved, rejected, expired, error
	UserCode       string `json:"user_code"`
	ExpiresAt      int64  `json:"expires_at"`
	KeyFingerprint string `json:"key_fingerprint"`
	DeviceName     string `json:"device_name"`
	Username       string `json:"username"`
	ServerURL      string `json:"server_url"`
	Error          string `json:"error,omitempty"`
}

func dataPath(name string) string { return filepath.Join(platform.DataDir(), name) }

func NewApp(log *slog.Logger, st *state.Store, eng *engine.Engine) *App {
	a := &App{log: log, st: st, eng: eng, settings: config.DefaultSettings()}
	if raw, err := os.ReadFile(dataPath("settings.json")); err == nil {
		_ = json.Unmarshal(raw, &a.settings)
	}
	a.settings.Normalize()
	return a
}

func (a *App) saveSettingsLocked() error {
	b, _ := json.MarshalIndent(a.settings, "", "  ")
	tmp := dataPath("settings.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dataPath("settings.json"))
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.eng.OnRevoked = func() {
		a.mu.Lock()
		a.client = nil
		a.revoked = true
		a.mu.Unlock()
		wruntime.EventsEmit(a.ctx, "revoked")
		a.toasts.Show("VaultDock device access revoked", "Sign in again to continue synchronising.", "warning")
	}
	a.eng.ApplySettings(a.settings)
	a.eng.LoadCachedAutomations()
	if err := a.eng.Start(context.Background()); err != nil {
		a.log.Error("engine start", "err", err)
	}
	go a.restoreConnection()
	go a.runUpdates()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for range t.C {
			wruntime.EventsEmit(a.ctx, "snapshot", a.eng.Snapshot())
		}
	}()
}

// restoreConnection rebuilds the signed client from the stored device key.
func (a *App) restoreConnection() {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	if s.DeviceID == 0 || s.ServerURL == "" {
		return
	}
	key, err := loadKey()
	if err != nil {
		a.log.Warn("device key unavailable; reconnect required", "err", err)
		return
	}
	base, err := client.NormalizeURL(s.ServerURL)
	if err != nil {
		return
	}
	c := client.New(base, client.Options{PinnedCertSHA256: s.PinnedCert})
	c.DeviceID, c.Key = s.DeviceID, key
	c.UserAgent = "VaultDock-Desktop/" + engine.Version
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	me, err := c.Me(ctx)
	if err != nil {
		var ae *client.APIError
		if errors.As(err, &ae) && ae.Status == 401 && ae.Code != "clock_skew" && ae.Code != "replay" {
			a.log.Warn("stored device credentials rejected", "code", ae.Code)
			a.mu.Lock()
			a.revoked = true
			a.mu.Unlock()
			wruntime.EventsEmit(a.ctx, "revoked")
			return
		}
		a.log.Warn("server unreachable at startup; will keep retrying", "err", err)
		me = nil
	}
	a.mu.Lock()
	a.client, a.me = c, me
	a.mu.Unlock()
	a.eng.SetClient(c, me)
	wruntime.EventsEmit(a.ctx, "connected")
}

func (a *App) shutdown(ctx context.Context) {
	a.eng.Stop()
	a.launchPendingUpdate()
}

// --- bootstrap ---

type Bootstrap struct {
	Version    string          `json:"version"`
	Connected  bool            `json:"connected"`
	Configured bool            `json:"configured"`
	Revoked    bool            `json:"revoked"`
	Enrollment *EnrollView     `json:"enrollment"`
	Settings   config.Settings `json:"settings"`
	DeviceName string          `json:"device_name"`
	OS         string          `json:"os"`
	User       string          `json:"user"`
	ServerInfo map[string]any  `json:"server_info"`
}

func (a *App) GetBootstrap() Bootstrap {
	a.mu.Lock()
	defer a.mu.Unlock()
	b := Bootstrap{Version: engine.Version, Settings: a.settings, DeviceName: platform.DeviceName(), OS: platform.OSDescription(), User: a.settings.Username}
	b.Configured = a.settings.DeviceID != 0
	b.Connected = a.client != nil
	b.Revoked = a.revoked
	if a.enroll != nil {
		v := a.enroll.View
		b.Enrollment = &v
	}
	if a.me != nil {
		b.ServerInfo = map[string]any{"version": a.me.Server.Version, "site_name": a.me.Server.SiteName, "quota": a.me.User.QuotaBytes, "used": a.me.User.UsedBytes}
	}
	return b
}

// --- connection ---

type ProbeResult struct {
	OK          bool           `json:"ok"`
	Info        map[string]any `json:"info,omitempty"`
	Untrusted   bool           `json:"untrusted"`
	Fingerprint string         `json:"fingerprint,omitempty"`
	Subject     string         `json:"subject,omitempty"`
	Reason      string         `json:"reason,omitempty"`
	URL         string         `json:"url"`
}

// ProbeServer checks a server URL. pin is a certificate fingerprint the user
// has explicitly chosen to trust (empty for normal CA validation).
func (a *App) ProbeServer(raw, pin string) (*ProbeResult, error) {
	base, err := client.NormalizeURL(raw)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	info, err := client.Probe(ctx, base, pin)
	if err != nil {
		var ue *client.UntrustedCertError
		if errors.As(err, &ue) {
			return &ProbeResult{Untrusted: true, Fingerprint: client.FormatFingerprint(ue.Fingerprint), Subject: ue.Subject, Reason: ue.Reason, URL: base.String()}, nil
		}
		return nil, friendlyNetErr(err)
	}
	if info["name"] != "VaultDock" {
		return nil, errors.New("that address answered, but it is not a VaultDock server")
	}
	return &ProbeResult{OK: true, Info: info, URL: base.String()}, nil
}

func friendlyNetErr(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no such host"):
		return errors.New("the server address could not be found (DNS lookup failed)")
	case strings.Contains(msg, "connection refused") || strings.Contains(msg, "actively refused"):
		return errors.New("the server refused the connection; check the address and port and that VaultDock is running")
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded"):
		return errors.New("the server did not respond in time")
	}
	return err
}

// Connect verifies credentials and starts device authorisation. The password
// is sent once over TLS and never stored.
func (a *App) Connect(rawURL, login, password, pin string) (*EnrollView, error) {
	base, err := client.NormalizeURL(rawURL)
	if err != nil {
		return nil, err
	}
	pin = strings.ToLower(strings.ReplaceAll(pin, ":", ""))
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	c := client.New(base, client.Options{PinnedCertSHA256: pin, Timeout: 30 * time.Second})
	c.UserAgent = "VaultDock-Desktop/" + engine.Version
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := c.Enroll(ctx, client.EnrollRequest{
		Login: login, Password: password, DeviceName: platform.DeviceName(), OS: platform.OSDescription(),
		Identifier: platform.DeviceIdentifier(), PublicKey: protocol.EncodePublicKey(pub), ClientVersion: engine.Version,
	})
	if err != nil {
		var ae *client.APIError
		if errors.As(err, &ae) {
			return nil, errors.New(ae.Message)
		}
		return nil, friendlyNetErr(err)
	}
	a.mu.Lock()
	if a.enroll != nil {
		a.enroll.cancel()
	}
	pctx, pcancel := context.WithCancel(context.Background())
	en := &enrollment{cancel: pcancel, key: priv, serverURL: base.String(), pin: pin, pollToken: res.PollToken, requestID: res.RequestID, clientProbe: c,
		View: EnrollView{Status: "pending", UserCode: res.UserCode, ExpiresAt: res.ExpiresAt, KeyFingerprint: res.KeyFingerprint, DeviceName: platform.DeviceName(), Username: res.Username, ServerURL: base.String()}}
	a.enroll = en
	a.mu.Unlock()
	go a.pollEnrollment(pctx, en, max(2, res.PollInterval))
	v := en.View
	return &v, nil
}

func (a *App) pollEnrollment(ctx context.Context, en *enrollment, interval int) {
	t := time.NewTicker(time.Duration(interval) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st, err := en.clientProbe.EnrollStatus(ctx, en.requestID, en.pollToken)
		if err != nil {
			if client.IsCode(err, "not_found") {
				a.finishEnrollment(en, "expired", "The request is no longer valid. Try connecting again.")
				return
			}
			continue // transient; keep polling
		}
		switch st.Status {
		case "pending":
			continue
		case "approved":
			if err := a.completeEnrollment(en, st.DeviceID); err != nil {
				a.finishEnrollment(en, "error", err.Error())
			}
			return
		case "rejected":
			a.finishEnrollment(en, "rejected", "The request was rejected in the web interface.")
			return
		default:
			a.finishEnrollment(en, "expired", "The request expired before it was approved. Try connecting again.")
			return
		}
	}
}

func (a *App) finishEnrollment(en *enrollment, status, msg string) {
	a.mu.Lock()
	en.View.Status, en.View.Error = status, msg
	v := en.View
	a.mu.Unlock()
	wruntime.EventsEmit(a.ctx, "enrollment", v)
}

func (a *App) completeEnrollment(en *enrollment, deviceID int64) error {
	if err := saveKey(en.key); err != nil {
		return fmt.Errorf("could not store the device key securely: %w", err)
	}
	base, _ := url.Parse(en.serverURL)
	c := client.New(base, client.Options{PinnedCertSHA256: en.pin})
	c.DeviceID, c.Key = deviceID, en.key
	c.UserAgent = "VaultDock-Desktop/" + engine.Version
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	me, err := c.Me(ctx)
	if err != nil {
		return fmt.Errorf("approved, but the first signed request failed: %v", err)
	}
	a.mu.Lock()
	prevServer, prevUser := a.settings.ServerURL, a.settings.Username
	a.settings.ServerURL, a.settings.PinnedCert, a.settings.DeviceID = en.serverURL, en.pin, deviceID
	a.settings.Username, a.settings.DeviceName, a.settings.KeyFingerprint = me.User.Username, me.Device.Name, en.View.KeyFingerprint
	_ = a.saveSettingsLocked()
	a.client, a.me = c, me
	a.revoked = false
	en.View.Status = "approved"
	v := en.View
	a.enroll = nil
	a.mu.Unlock()
	if prevServer != "" && (prevServer != en.serverURL || !strings.EqualFold(prevUser, me.User.Username)) {
		// Different server or account: local upload history no longer applies.
		_ = a.st.ResetIndex()
	}
	a.eng.SetClient(c, me)
	wruntime.EventsEmit(a.ctx, "enrollment", v)
	wruntime.EventsEmit(a.ctx, "connected")
	a.log.Info("device approved", "server", en.serverURL, "device", deviceID)
	if a.settings.AutoStart {
		_ = platform.SetAutoStart(true)
	}
	return nil
}

func (a *App) CancelEnrollment() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.enroll != nil {
		a.enroll.cancel()
		a.enroll = nil
	}
}

// Disconnect removes this device's credentials. Sync configurations are kept
// so reconnecting is quick; nothing on the server is deleted.
func (a *App) Disconnect() error {
	a.mu.Lock()
	a.client, a.me = nil, nil
	a.settings.DeviceID = 0
	a.settings.KeyFingerprint = ""
	err := a.saveSettingsLocked()
	a.mu.Unlock()
	a.eng.SetClient(nil, nil)
	_ = os.Remove(dataPath("device.key"))
	return err
}

func saveKey(k ed25519.PrivateKey) error {
	enc, err := platform.Protect(k)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(platform.DataDir(), 0o700); err != nil {
		return err
	}
	tmp := dataPath("device.key.tmp")
	if err := os.WriteFile(tmp, enc, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dataPath("device.key"))
}

func loadKey() (ed25519.PrivateKey, error) {
	enc, err := os.ReadFile(dataPath("device.key"))
	if err != nil {
		return nil, err
	}
	raw, err := platform.Unprotect(enc)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, errors.New("device key is corrupt")
	}
	return ed25519.PrivateKey(raw), nil
}

// --- sync configurations ---

func (a *App) ListConfigs() []engine.ConfigView { return a.eng.ConfigViews() }

func (a *App) SaveConfig(c config.SyncConfig) (config.SyncConfig, error) { return a.eng.SaveConfig(c) }

func (a *App) DeleteConfig(id string) error { return a.eng.DeleteConfig(id) }

func (a *App) SyncNow(id string) error {
	if id == "" {
		for _, c := range a.eng.Configs() {
			if c.Enabled {
				cid := c.ID
				go func() { _, _ = a.eng.Scan(cid, true) }()
			}
		}
		return nil
	}
	go func() { _, _ = a.eng.Scan(id, true) }()
	return nil
}

type PreviewResult struct {
	Entries []engine.PreviewEntry `json:"entries"`
	Total   int                   `json:"total"`
}

func (a *App) PreviewConfig(c config.SyncConfig) (*PreviewResult, error) {
	entries, total, err := a.eng.Preview(c, 500)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []engine.PreviewEntry{}
	}
	return &PreviewResult{Entries: entries, Total: total}, nil
}

func (a *App) PickFolder(title string) (string, error) {
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: title})
}

func (a *App) PickFiles() ([]string, error) {
	return wruntime.OpenMultipleFilesDialog(a.ctx, wruntime.OpenDialogOptions{Title: "Choose files to synchronise"})
}

// SuggestFolders returns existing well-known folders (Medal, screenshots).
func (a *App) SuggestFolders() map[string]string {
	home, _ := os.UserHomeDir()
	out := map[string]string{}
	candidates := map[string][]string{
		"medal":       {filepath.Join(home, "Videos", "Medal", "Clips"), filepath.Join(home, "Videos", "Medal"), filepath.Join(home, "Medal", "Clips"), `C:\Medal\Clips`, `D:\Medal\Clips`},
		"screenshots": {filepath.Join(home, "Pictures", "Screenshots")},
		"documents":   {filepath.Join(home, "Documents")},
		"videos":      {filepath.Join(home, "Videos")},
	}
	for k, list := range candidates {
		for _, p := range list {
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				out[k] = p
				break
			}
		}
	}
	return out
}

// --- transfers ---

func (a *App) GetSnapshot() engine.Snapshot { return a.eng.Snapshot() }

func (a *App) SetPaused(p bool) error {
	a.mu.Lock()
	a.settings.Paused = p
	err := a.saveSettingsLocked()
	a.mu.Unlock()
	a.eng.SetPaused(p)
	return err
}

func (a *App) RetryFailed(id int64) (int64, error) { return a.eng.RetryFailed(id) }

func (a *App) ListQueue(status string) ([]state.Item, error) { return a.eng.Items(status, 500) }

func (a *App) ListHistory(status string) ([]state.HistoryEntry, error) {
	return a.eng.History(status, 500)
}

// --- automations ---

func (a *App) ListAutomations() []engine.AutomationView { return a.eng.Automations() }

func (a *App) RefreshAutomations() []engine.AutomationView {
	a.eng.RefreshAutomations()
	return a.eng.Automations()
}

// --- settings ---

func (a *App) GetSettings() config.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.settings
	s.AutoStart = platform.AutoStartEnabled()
	return s
}

func (a *App) SaveSettings(s config.Settings) (config.Settings, error) {
	a.mu.Lock()
	// identity fields are not editable from the UI
	s.ServerURL, s.PinnedCert, s.DeviceID, s.Username, s.DeviceName, s.KeyFingerprint = a.settings.ServerURL, a.settings.PinnedCert, a.settings.DeviceID, a.settings.Username, a.settings.DeviceName, a.settings.KeyFingerprint
	s.Normalize()
	a.settings = s
	err := a.saveSettingsLocked()
	cb := a.onSettings
	a.mu.Unlock()
	if err != nil {
		return s, err
	}
	if aerr := platform.SetAutoStart(s.AutoStart); aerr != nil {
		a.log.Warn("autostart", "err", aerr)
	}
	a.eng.ApplySettings(s)
	if cb != nil {
		cb(s)
	}
	return s, nil
}

// --- helpers ---

func (a *App) OpenWebInterface(path string) {
	a.mu.Lock()
	base := a.settings.ServerURL
	a.mu.Unlock()
	if base == "" {
		return
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	wruntime.BrowserOpenURL(a.ctx, strings.TrimRight(base, "/")+path)
}

func (a *App) OpenLogsFolder() { openFolder(platform.LogDir()) }

func (a *App) OpenFolder(p string) {
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		openFolder(p)
	}
}

func openFolder(p string) {
	_ = os.MkdirAll(p, 0o700)
	// explorer.exe with a single, validated directory argument (no shell).
	_ = exec.Command("explorer.exe", filepath.Clean(p)).Start()
}

func (a *App) ShowWindow() {
	wruntime.WindowUnminimise(a.ctx)
	wruntime.WindowShow(a.ctx)
}

func (a *App) Quit() {
	a.mu.Lock()
	a.quitting = true
	a.mu.Unlock()
	wruntime.Quit(a.ctx)
}

func (a *App) OpenURL(u string) {
	if strings.HasPrefix(u, "https://") {
		wruntime.BrowserOpenURL(a.ctx, u)
	}
}
