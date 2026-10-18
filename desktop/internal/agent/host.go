package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/modules"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/platform"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/profiles"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/tokens"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/updater"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/protocol"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// Options configures a Host. Zero values pick the per-user defaults of the
// current desktop app.
type Options struct {
	Log *slog.Logger
	// DataDir holds settings.json, device.key and (unless Store is set)
	// state.db. Default platform.DataDir().
	DataDir string
	// Store is an already-open state store; when nil the host opens
	// DataDir/state.db and closes it in Close.
	Store *state.Store
	// DeviceName, OS and Identifier describe this machine at enrolment.
	// Defaults come from package platform.
	DeviceName, OS, Identifier string
	// Keys stores the device signing key. Default: a DPAPI-protected file
	// DataDir/device.key (FileKeyStore).
	Keys KeyStore
	// AutoStart and AutoStartEnabled manage start-with-Windows. Both nil
	// means unmanaged (service mode; tests).
	AutoStart        func(enabled bool) error
	AutoStartEnabled func() bool

	// UpdateRepo is the release repository ("owner/name"); empty disables
	// update checks. UpdateDir is where installers are downloaded.
	UpdateRepo string
	UpdateDir  string
	// AutoUpdates runs the background check/download/install loop.
	AutoUpdates bool
	// InstallDir is passed to the installer; default: the executable's dir.
	InstallDir string
	// RequestExit is called when the host needs its owner to stop it (an
	// update is ready to install). The owner then calls Close.
	RequestExit func()

	// Service marks the machine-wide agent service. In service mode
	// InstallUpdate is refused (the admin installer handles updates); a
	// process running as LocalSystem is always treated as service mode.
	Service bool
	// Tokens issues the pipe caller's token for path access (default
	// tokens.Default). Guard checks caller-supplied paths (default
	// guard.Check). Profiles resolves the caller's profile directory
	// (default profiles.OSSystem{}). Tests inject fakes.
	Tokens   tokens.Provider
	Guard    func(op guard.Op, path string, p guard.Profile) error
	Profiles ProfileLister
	// ModeFile is the installer's mode.json (DSK-MOD); default
	// DataDir\config\mode.json.
	ModeFile string
	// Backup enables the backup stack (keystore, runner, scheduler, device
	// task loop; P1-26b). nil = off (per-user host, tests).
	Backup *BackupOptions

	// SnapshotInterval between KindSnapshot events (default 1s).
	SnapshotInterval time.Duration
	// EventBuffer per subscriber (default DefaultBuffer).
	EventBuffer int
	// LogEventLevel is the minimum level forwarded as KindLog (default Info).
	LogEventLevel slog.Leveler
}

// KeyStore persists the device's Ed25519 signing key.
type KeyStore interface {
	Load() (ed25519.PrivateKey, error)
	Save(ed25519.PrivateKey) error
	Delete() error
}

// FileKeyStore keeps the key in a file protected by platform.Protect
// (DPAPI on Windows).
type FileKeyStore struct{ Path string }

func (f FileKeyStore) Load() (ed25519.PrivateKey, error) {
	enc, err := os.ReadFile(f.Path)
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

func (f FileKeyStore) Save(k ed25519.PrivateKey) error {
	enc, err := platform.Protect(k)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, enc, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.Path)
}

func (f FileKeyStore) Delete() error {
	if err := os.Remove(f.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Host implements API by owning the sync engine, state store, settings,
// device identity and updater. Create with NewHost, then Start; Close stops
// everything.
type Host struct {
	opts      Options
	log       *slog.Logger
	bus       *Bus
	st        *state.Store
	ownsStore bool
	eng       *engine.Engine

	mu             sync.Mutex
	settings       config.Settings
	client         *client.Client
	me             *client.Me
	enroll         *enrollment
	revoked        bool
	started        bool
	closed         bool
	upd            *updater.Updater
	installPending bool
	notifiedUpdate string
	mods           *modules.Manager // nil before Start or when unavailable
	bk             *backupState     // nil when Options.Backup is nil; set once in NewHost

	ctx    context.Context // cancelled by Close
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type enrollment struct {
	cancel    context.CancelFunc
	view      EnrollView
	key       ed25519.PrivateKey
	serverURL string
	pin       string
	pollToken string
	requestID int64
	probe     *client.Client
}

var _ API = (*Host)(nil)

// NewHost loads settings and opens (or adopts) the state store. It does not
// start background work; call Start.
func NewHost(opts Options) (*Host, error) {
	if opts.DataDir == "" {
		opts.DataDir = platform.DataDir()
	}
	if opts.DeviceName == "" {
		opts.DeviceName = platform.DeviceName()
	}
	if opts.OS == "" {
		opts.OS = platform.OSDescription()
	}
	if opts.Identifier == "" {
		opts.Identifier = platform.DeviceIdentifier()
	}
	if opts.Keys == nil {
		opts.Keys = FileKeyStore{Path: filepath.Join(opts.DataDir, "device.key")}
	}
	if opts.UpdateDir == "" {
		opts.UpdateDir = filepath.Join(opts.DataDir, "updates")
	}
	if opts.SnapshotInterval <= 0 {
		opts.SnapshotInterval = time.Second
	}
	if opts.LogEventLevel == nil {
		opts.LogEventLevel = slog.LevelInfo
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.Tokens == nil {
		opts.Tokens = tokens.Default
	}
	if opts.Guard == nil {
		opts.Guard = guard.Check
	}
	if opts.Profiles == nil {
		opts.Profiles = profiles.OSSystem{}
	}
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return nil, err
	}
	h := &Host{opts: opts, bus: NewBus(opts.EventBuffer), settings: config.DefaultSettings()}
	h.log = slog.New(NewLogHandler(h.bus, opts.Log.Handler(), opts.LogEventLevel))
	h.ctx, h.cancel = context.WithCancel(context.Background())
	if raw, err := os.ReadFile(h.path("settings.json")); err == nil {
		if err := json.Unmarshal(raw, &h.settings); err != nil {
			h.log.Warn("settings unreadable; using defaults", "err", err)
			h.settings = config.DefaultSettings()
		}
	}
	h.settings.Normalize()
	h.st = opts.Store
	if h.st == nil {
		st, err := state.Open(h.path("state.db"))
		if err != nil {
			return nil, fmt.Errorf("open state: %w", err)
		}
		h.st, h.ownsStore = st, true
	}
	h.eng = engine.New(h.st, h.log, opts.DeviceName)
	h.openBackup()
	return h, nil
}

func (h *Host) path(name string) string { return filepath.Join(h.opts.DataDir, name) }

// Bus returns the host's event bus (for transports and the service wrapper).
func (h *Host) Bus() *Bus { return h.bus }

// Engine returns the owned sync engine (for wiring in tests and main).
func (h *Host) Engine() *engine.Engine { return h.eng }

// Start applies settings, starts the engine and background loops and
// restores the stored server connection asynchronously.
func (h *Host) Start(ctx context.Context) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return ErrClosed
	}
	if h.started {
		h.mu.Unlock()
		return nil
	}
	h.started = true
	s := h.settings
	h.mu.Unlock()

	h.eng.OnRevoked = h.onRevoked
	h.eng.Notifier = func(title, msg, level string) { h.notify(title, msg, level) }
	h.eng.NotifyOwner = func(owner, title, msg, level string) {
		_ = h.bus.Publish(KindNotification, Notification{Title: title, Message: msg, Level: level, OwnerSID: owner})
	}
	h.eng.ApplySettings(s)
	h.eng.LoadCachedAutomations()
	if h.serviceMode() {
		// The service runs as SYSTEM: read every folder as its owner.
		h.assignOwners()
		h.eng.SetTokens(h.opts.Tokens)
	}
	if err := h.eng.Start(h.ctx); err != nil {
		return fmt.Errorf("engine start: %w", err)
	}
	h.startModules()
	h.startBackupHost()
	h.goLoop(h.restoreConnection)
	h.goLoop(h.snapshotLoop)
	if h.opts.AutoUpdates && h.opts.UpdateRepo != "" {
		h.goLoop(h.updateLoop)
	}
	return nil
}

// goLoop runs f in a goroutine that Close waits for; after Close it is a
// no-op.
func (h *Host) goLoop(f func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.wg.Add(1)
	go func() { defer h.wg.Done(); f() }()
}

// Close stops background work and the engine, launches a pending update
// installer, closes the bus and (if owned) the state store. Idempotent.
func (h *Host) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	started, pending := h.started, h.installPending
	if h.enroll != nil {
		h.enroll.cancel()
		h.enroll = nil
	}
	h.mu.Unlock()

	reason := "stop"
	if pending {
		reason = "update"
	}
	_ = h.bus.Publish(KindShutdown, ShutdownEvent{Reason: reason})
	h.cancel()
	h.wg.Wait()
	h.closeBackup()
	if started {
		h.eng.Stop()
	}
	if pending {
		h.launchPendingUpdate()
	}
	h.bus.Close()
	if h.ownsStore {
		return h.st.Close()
	}
	return nil
}

func (h *Host) live() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return ErrClosed
	}
	return nil
}

func (h *Host) notify(title, msg, level string) {
	_ = h.bus.Publish(KindNotification, Notification{Title: title, Message: msg, Level: level})
}

func (h *Host) setConnection(st string) {
	_ = h.bus.Publish(KindConnection, ConnectionEvent{State: st})
}

func (h *Host) onRevoked() {
	h.mu.Lock()
	h.client = nil
	h.revoked = true
	h.mu.Unlock()
	h.stopBackup()
	h.setConnection("revoked")
	h.notify("VaultDock device access revoked", "Sign in again to continue synchronising.", "warning")
}

func (h *Host) snapshotLoop() {
	t := time.NewTicker(h.opts.SnapshotInterval)
	defer t.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-t.C:
		}
		if h.bus.HasSubscribers(KindSnapshot) {
			_ = h.bus.Publish(KindSnapshot, h.eng.Snapshot())
		}
	}
}

func (h *Host) saveSettingsLocked() error {
	b, _ := json.MarshalIndent(h.settings, "", "  ")
	tmp := h.path("settings.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, h.path("settings.json"))
}

func (h *Host) newClient(base *url.URL, pin string, timeout time.Duration) *client.Client {
	c := client.New(base, client.Options{PinnedCertSHA256: pin, Timeout: timeout})
	c.UserAgent = "VaultDock-Desktop/" + engine.Version
	return c
}

// restoreConnection rebuilds the signed client from the stored device key.
func (h *Host) restoreConnection() {
	h.mu.Lock()
	s := h.settings
	h.mu.Unlock()
	if s.DeviceID == 0 || s.ServerURL == "" {
		return
	}
	key, err := h.opts.Keys.Load()
	if err != nil {
		h.log.Warn("device key unavailable; reconnect required", "err", err)
		return
	}
	base, err := client.NormalizeURL(s.ServerURL)
	if err != nil {
		return
	}
	c := h.newClient(base, s.PinnedCert, 0)
	c.DeviceID, c.Key = s.DeviceID, key
	ctx, cancel := context.WithTimeout(h.ctx, 20*time.Second)
	defer cancel()
	me, err := c.Me(ctx)
	if err != nil {
		var ae *client.APIError
		if errors.As(err, &ae) && ae.Status == 401 && ae.Code != "clock_skew" && ae.Code != "replay" {
			h.log.Warn("stored device credentials rejected", "code", ae.Code)
			h.mu.Lock()
			h.revoked = true
			h.mu.Unlock()
			h.setConnection("revoked")
			return
		}
		if h.ctx.Err() != nil {
			return
		}
		h.log.Warn("server unreachable at startup; will keep retrying", "err", err)
		me = nil
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.client, h.me = c, me
	h.mu.Unlock()
	h.eng.SetClient(c, me)
	h.setConnection("connected")
	h.kickModules()
	h.startBackup(c)
}

// --- lifecycle / connection ---

func (h *Host) Bootstrap(ctx context.Context) (Bootstrap, error) {
	if err := h.live(); err != nil {
		return Bootstrap{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	b := Bootstrap{
		Version: engine.Version, ProtocolVersion: ProtocolVersion, Settings: h.settings,
		DeviceName: h.opts.DeviceName, OS: h.opts.OS, User: h.settings.Username,
		Configured: h.settings.DeviceID != 0, Connected: h.client != nil, Revoked: h.revoked,
	}
	if h.enroll != nil {
		v := h.enroll.view
		b.Enrollment = &v
	}
	if h.me != nil {
		b.ServerInfo = &ServerInfo{Version: h.me.Server.Version, SiteName: h.me.Server.SiteName, Quota: h.me.User.QuotaBytes, Used: h.me.User.UsedBytes}
	}
	return b, nil
}

func (h *Host) ProbeServer(ctx context.Context, req ProbeRequest) (ProbeResult, error) {
	if err := h.live(); err != nil {
		return ProbeResult{}, err
	}
	base, err := client.NormalizeURL(req.URL)
	if err != nil {
		return ProbeResult{}, invalidf(err.Error())
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	info, err := client.Probe(ctx, base, normalizePin(req.Pin))
	if err != nil {
		var ue *client.UntrustedCertError
		if errors.As(err, &ue) {
			return ProbeResult{Untrusted: true, Fingerprint: client.FormatFingerprint(ue.Fingerprint), Subject: ue.Subject, Reason: ue.Reason, URL: base.String()}, nil
		}
		return ProbeResult{}, unavailable(friendlyNetErr(err))
	}
	if info["name"] != "VaultDock" {
		return ProbeResult{}, &Error{Code: CodeInvalid, Message: "that address answered, but it is not a VaultDock server"}
	}
	return ProbeResult{OK: true, Info: info, URL: base.String()}, nil
}

func normalizePin(pin string) string { return strings.ToLower(strings.ReplaceAll(pin, ":", "")) }

func unavailable(err error) error { return &Error{Code: CodeUnavailable, Message: err.Error()} }

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

func (h *Host) Connect(ctx context.Context, req ConnectRequest) (EnrollView, error) {
	if err := h.live(); err != nil {
		return EnrollView{}, err
	}
	base, err := client.NormalizeURL(req.URL)
	if err != nil {
		return EnrollView{}, invalidf(err.Error())
	}
	if strings.TrimSpace(req.Login) == "" || req.Password == "" {
		return EnrollView{}, invalidf("enter your username and password")
	}
	pin := normalizePin(req.Pin)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return EnrollView{}, err
	}
	c := h.newClient(base, pin, 30*time.Second)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := c.Enroll(ctx, client.EnrollRequest{
		Login: req.Login, Password: req.Password, DeviceName: h.opts.DeviceName, OS: h.opts.OS,
		Identifier: h.opts.Identifier, PublicKey: protocol.EncodePublicKey(pub), ClientVersion: engine.Version,
		BoxPub: h.boxPubB64(),
	})
	if err != nil {
		var ae *client.APIError
		if errors.As(err, &ae) {
			return EnrollView{}, invalidf(ae.Message)
		}
		return EnrollView{}, unavailable(friendlyNetErr(err))
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return EnrollView{}, ErrClosed
	}
	if h.enroll != nil {
		h.enroll.cancel()
	}
	pctx, pcancel := context.WithCancel(h.ctx)
	en := &enrollment{cancel: pcancel, key: priv, serverURL: base.String(), pin: pin, pollToken: res.PollToken, requestID: res.RequestID, probe: c,
		view: EnrollView{Status: "pending", UserCode: res.UserCode, ExpiresAt: res.ExpiresAt, KeyFingerprint: res.KeyFingerprint, DeviceName: h.opts.DeviceName, Username: res.Username, ServerURL: base.String()}}
	h.enroll = en
	v := en.view
	h.mu.Unlock()
	h.goLoop(func() { h.pollEnrollment(pctx, en, max(2, res.PollInterval)) })
	_ = h.bus.Publish(KindEnrollment, v)
	return v, nil
}

func (h *Host) pollEnrollment(ctx context.Context, en *enrollment, interval int) {
	t := time.NewTicker(time.Duration(interval) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st, err := en.probe.EnrollStatus(ctx, en.requestID, en.pollToken)
		if err != nil {
			if client.IsCode(err, "not_found") {
				h.finishEnrollment(en, "expired", "The request is no longer valid. Try connecting again.")
				return
			}
			continue // transient; keep polling
		}
		switch st.Status {
		case "pending":
			continue
		case "approved":
			if err := h.completeEnrollment(ctx, en, st.DeviceID, st.ServerIdentity); err != nil {
				h.finishEnrollment(en, "error", err.Error())
			}
			return
		case "rejected":
			h.finishEnrollment(en, "rejected", "The request was rejected in the web interface.")
			return
		default:
			h.finishEnrollment(en, "expired", "The request expired before it was approved. Try connecting again.")
			return
		}
	}
}

func (h *Host) finishEnrollment(en *enrollment, status, msg string) {
	h.mu.Lock()
	en.view.Status, en.view.Error = status, msg
	v := en.view
	h.mu.Unlock()
	_ = h.bus.Publish(KindEnrollment, v)
}

func (h *Host) completeEnrollment(ctx context.Context, en *enrollment, deviceID int64, ident *wire.ServerIdentity) error {
	if err := h.opts.Keys.Save(en.key); err != nil {
		return fmt.Errorf("could not store the device key securely: %w", err)
	}
	base, err := url.Parse(en.serverURL)
	if err != nil {
		return err
	}
	c := h.newClient(base, en.pin, 0)
	c.DeviceID, c.Key = deviceID, en.key
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	me, err := c.Me(ctx)
	if err != nil {
		return fmt.Errorf("approved, but the first signed request failed: %v", err)
	}
	h.mu.Lock()
	prevServer, prevUser := h.settings.ServerURL, h.settings.Username
	h.settings.ServerURL, h.settings.PinnedCert, h.settings.DeviceID = en.serverURL, en.pin, deviceID
	h.settings.Username, h.settings.DeviceName, h.settings.KeyFingerprint = me.User.Username, me.Device.Name, en.view.KeyFingerprint
	serr := h.saveSettingsLocked()
	h.client, h.me = c, me
	h.revoked = false
	en.view.Status = "approved"
	v := en.view
	if h.enroll == en {
		h.enroll = nil
	}
	s := h.settings
	h.mu.Unlock()
	if serr != nil {
		h.log.Warn("could not save settings", "err", serr)
	}
	if prevServer != "" && (prevServer != en.serverURL || !strings.EqualFold(prevUser, me.User.Username)) {
		// Different server or account: local upload history no longer applies.
		_ = h.st.ResetIndex()
	}
	h.eng.SetClient(c, me)
	h.pinEnrolledIdentity(ident)
	_ = h.bus.Publish(KindEnrollment, v)
	h.setConnection("connected")
	_ = h.bus.Publish(KindSettings, s)
	h.kickModules()
	h.startBackup(c)
	h.log.Info("device approved", "server", en.serverURL, "device", deviceID)
	if s.AutoStart && h.opts.AutoStart != nil {
		_ = h.opts.AutoStart(true)
	}
	return nil
}

func (h *Host) CancelEnrollment(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.enroll != nil {
		h.enroll.cancel()
		h.enroll = nil
	}
	return nil
}

func (h *Host) Disconnect(ctx context.Context) error {
	if err := h.live(); err != nil {
		return err
	}
	h.mu.Lock()
	h.client, h.me = nil, nil
	h.settings.DeviceID = 0
	h.settings.KeyFingerprint = ""
	err := h.saveSettingsLocked()
	s := h.settings
	h.mu.Unlock()
	h.stopBackup()
	h.eng.SetClient(nil, nil)
	if kerr := h.opts.Keys.Delete(); kerr != nil {
		h.log.Warn("could not delete the device key", "err", kerr)
	}
	h.setConnection("disconnected")
	_ = h.bus.Publish(KindSettings, s)
	return err
}

// --- sync configurations ---

func (h *Host) ListConfigs(ctx context.Context) ([]engine.ConfigView, error) {
	if err := h.live(); err != nil {
		return nil, err
	}
	sc := h.scopeOf(ctx)
	v := []engine.ConfigView{}
	for _, c := range h.eng.ConfigViews() {
		if sc.owns(c.SyncConfig) {
			v = append(v, c)
		}
	}
	return v, nil
}

func (h *Host) SaveConfig(ctx context.Context, c config.SyncConfig) (config.SyncConfig, error) {
	if err := h.live(); err != nil {
		return c, err
	}
	sc := h.scopeOf(ctx)
	var prev *config.SyncConfig
	if c.ID != "" {
		p, err := h.visibleConfig(sc, c.ID)
		if err != nil {
			return c, err
		}
		prev = &p
	}
	if err := h.setOwner(sc, &c, prev); err != nil {
		return c, err
	}
	if err := h.checkCallerPaths(ctx, c); err != nil {
		return c, err
	}
	out, err := h.eng.SaveConfig(c)
	if err != nil {
		// SaveConfig fails on validation or a duplicate name.
		return out, invalidf(err.Error())
	}
	return out, nil
}

func (h *Host) DeleteConfig(ctx context.Context, id string) error {
	if err := h.live(); err != nil {
		return err
	}
	if id == "" {
		return invalidf("missing configuration id")
	}
	if _, err := h.visibleConfig(h.scopeOf(ctx), id); err != nil {
		return err
	}
	return h.eng.DeleteConfig(id)
}

func (h *Host) SyncNow(ctx context.Context, id string) error {
	if err := h.live(); err != nil {
		return err
	}
	// A caller triggers only folders they own, and (defence in depth) only
	// those guard and their own token still allow; the scan itself reads as
	// the folder's owner (engine.SetTokens).
	sc := h.scopeOf(ctx)
	pa, err := h.callerAccess(ctx)
	if err != nil {
		return err
	}
	defer pa.Close()
	var ids []string
	if id == "" {
		for _, c := range h.eng.Configs() {
			if !c.Enabled || !sc.owns(c) {
				continue
			}
			if err := pa.check(c); err != nil {
				h.log.Warn("sync now: skipping folder the caller cannot access", "config", c.ID, "err", err)
				continue
			}
			ids = append(ids, c.ID)
		}
	} else {
		c, err := h.visibleConfig(sc, id)
		if err != nil {
			return err
		}
		if err := pa.check(c); err != nil {
			return err
		}
		ids = []string{id}
	}
	for _, cid := range ids {
		go func() { _, _ = h.eng.Scan(cid, true) }()
	}
	return nil
}

func (h *Host) PreviewConfig(ctx context.Context, c config.SyncConfig) (PreviewResult, error) {
	if err := h.live(); err != nil {
		return PreviewResult{}, err
	}
	pa, err := h.callerAccess(ctx)
	if err != nil {
		return PreviewResult{}, err
	}
	defer pa.Close()
	if c.ID != "" {
		if _, err := h.visibleConfig(h.scopeOf(ctx), c.ID); err != nil {
			return PreviewResult{}, err
		}
	}
	if err := pa.check(c); err != nil {
		return PreviewResult{}, err
	}
	// The walk runs as the caller, so subfolders they cannot read are
	// skipped by the OS rather than listed with SYSTEM's rights.
	var run func(func() error) error
	if pa.tok != nil {
		run = pa.do
	}
	entries, total, err := h.eng.Preview(c, 500, run)
	if err != nil {
		var ae *Error
		if errors.As(err, &ae) {
			return PreviewResult{}, err
		}
		return PreviewResult{}, invalidf(err.Error())
	}
	if entries == nil {
		entries = []engine.PreviewEntry{}
	}
	return PreviewResult{Entries: entries, Total: total}, nil
}

// --- transfers ---

func (h *Host) Snapshot(ctx context.Context) (engine.Snapshot, error) {
	if err := h.live(); err != nil {
		return engine.Snapshot{}, err
	}
	return h.filterSnapshot(h.scopeOf(ctx), h.eng.Snapshot()), nil
}

func (h *Host) SetPaused(ctx context.Context, paused bool) error {
	if err := h.live(); err != nil {
		return err
	}
	h.mu.Lock()
	h.settings.Paused = paused
	err := h.saveSettingsLocked()
	s := h.settings
	h.mu.Unlock()
	h.eng.SetPaused(paused)
	_ = h.bus.Publish(KindSettings, s)
	return err
}

func (h *Host) RetryFailed(ctx context.Context, id int64) (int64, error) {
	if err := h.live(); err != nil {
		return 0, err
	}
	if id < 0 {
		return 0, invalidf("invalid item id")
	}
	return h.eng.RetryFailedIn(id, h.ownedIDs(h.scopeOf(ctx)))
}

func validQueueStatus(s string) bool {
	switch s {
	case "", "pending", "active", "failed":
		return true
	}
	return false
}

func (h *Host) ListQueue(ctx context.Context, status string) ([]state.Item, error) {
	if err := h.live(); err != nil {
		return nil, err
	}
	if !validQueueStatus(status) {
		return nil, invalidf("unknown queue status")
	}
	return h.eng.Items(status, h.ownedIDs(h.scopeOf(ctx)), 500)
}

func (h *Host) ListHistory(ctx context.Context, status string) ([]state.HistoryEntry, error) {
	if err := h.live(); err != nil {
		return nil, err
	}
	if len(status) > 32 {
		return nil, invalidf("unknown history status")
	}
	return h.eng.History(status, h.ownedIDs(h.scopeOf(ctx)), 500)
}

// --- automations ---

func (h *Host) ListAutomations(ctx context.Context) ([]engine.AutomationView, error) {
	if err := h.live(); err != nil {
		return nil, err
	}
	return nonNil(h.eng.Automations()), nil
}

func (h *Host) RefreshAutomations(ctx context.Context) ([]engine.AutomationView, error) {
	if err := h.live(); err != nil {
		return nil, err
	}
	h.eng.RefreshAutomations()
	return nonNil(h.eng.Automations()), nil
}

func nonNil(v []engine.AutomationView) []engine.AutomationView {
	if v == nil {
		return []engine.AutomationView{}
	}
	return v
}

// --- settings ---

func (h *Host) GetSettings(ctx context.Context) (config.Settings, error) {
	if err := h.live(); err != nil {
		return config.Settings{}, err
	}
	h.mu.Lock()
	s := h.settings
	h.mu.Unlock()
	if h.opts.AutoStartEnabled != nil {
		s.AutoStart = h.opts.AutoStartEnabled()
	}
	return s, nil
}

func (h *Host) SaveSettings(ctx context.Context, s config.Settings) (config.Settings, error) {
	if err := h.live(); err != nil {
		return s, err
	}
	h.mu.Lock()
	// Identity fields are not editable from the UI.
	cur := h.settings
	s.ServerURL, s.PinnedCert, s.DeviceID, s.Username, s.DeviceName, s.KeyFingerprint = cur.ServerURL, cur.PinnedCert, cur.DeviceID, cur.Username, cur.DeviceName, cur.KeyFingerprint
	s.Normalize()
	h.settings = s
	err := h.saveSettingsLocked()
	h.mu.Unlock()
	if err != nil {
		return s, err
	}
	if h.opts.AutoStart != nil {
		if aerr := h.opts.AutoStart(s.AutoStart); aerr != nil {
			h.log.Warn("autostart", "err", aerr)
		}
	}
	h.eng.ApplySettings(s)
	_ = h.bus.Publish(KindSettings, s)
	return s, nil
}

// --- events ---

func (h *Host) Subscribe(ctx context.Context, kinds ...EventKind) (Subscription, error) {
	if err := h.live(); err != nil {
		return nil, err
	}
	return h.scopedEvents(ctx, h.bus.SubscribeContext(ctx, kinds...)), nil
}
