// Package engine is the VaultDock desktop synchronisation engine: it scans
// and watches the user's selected folders, evaluates automations, and
// uploads changed files with resumable, verified, adaptively parallel
// transfers. Synchronisation is one-way (local -> VaultDock); the engine
// never deletes or modifies local files and never deletes server copies.
package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/filter"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/outbox"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/transfer"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/automation"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
)

// Notifier shows a desktop notification.
type Notifier func(title, message, level string)

type activeTransfer struct {
	item     state.Item
	ctx      context.Context
	cancel   context.CancelFunc // pause stops the transfer; the item is requeued
	phase    atomic.Value       // string
	sent     atomic.Int64
	size     int64
	started  time.Time
	mu       sync.Mutex
	speed    float64
	lastSent int64
	lastT    time.Time
	uploadID atomic.Value // string: the server's upload session, once opened
}

type runStats struct {
	uploaded int
	failed   int
	bytes    int64
	firstErr string
	touched  bool
	depth    int
}

// Engine coordinates scanning, watching and uploading.
type Engine struct {
	st   *state.Store
	log  *slog.Logger
	xfer *transfer.Core
	ctrl *Controller // == xfer.Controller()
	id   identity    // owner tokens (identity.go)

	mu           sync.Mutex
	client       *client.Client
	features     map[string]bool // the server's feature flags
	direct       DirectView
	deviceName   string
	configs      map[string]*config.SyncConfig
	filters      map[string]*filter.Filter
	watchers     map[string]*watcher
	scanning     map[string]bool
	lastScan     map[string]time.Time
	lastErr      map[string]string
	autos        []automation.Named
	autosLoaded  time.Time
	active       map[int64]*activeTransfer
	paused       bool
	status       string
	message      string
	runs         map[string]*runStats
	reports      []client.TransferReport
	notes        []client.NotificationReport
	outbox       *outbox.Outbox // durable report outbox; see reportOutbox
	lastStart    map[string]time.Time
	dedupe       map[string]time.Time
	removed      map[string]map[string]removal // config -> size|mtime -> removal
	netFailures  int
	offlineUntil time.Time
	notify       bool
	session      struct {
		completed int
		bytes     int64
	}

	wake     chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	Notifier Notifier
	// NotifyOwner, when set, replaces Notifier for automation messages and
	// receives the configuration's OwnerSID so only that user sees them.
	NotifyOwner func(ownerSID, title, message, level string)
	// OnRevoked is called when the server reports the device was revoked.
	OnRevoked func()
}

type removal struct {
	rel  string
	at   time.Time
	done bool
}

func New(st *state.Store, log *slog.Logger, deviceName string) *Engine {
	xfer := transfer.New(32, 2)
	return &Engine{
		st: st, log: log, xfer: xfer, ctrl: xfer.Controller(), deviceName: deviceName,
		configs: map[string]*config.SyncConfig{}, filters: map[string]*filter.Filter{}, watchers: map[string]*watcher{},
		scanning: map[string]bool{}, lastScan: map[string]time.Time{}, lastErr: map[string]string{}, active: map[int64]*activeTransfer{},
		runs: map[string]*runStats{}, lastStart: map[string]time.Time{}, dedupe: map[string]time.Time{},
		removed: map[string]map[string]removal{}, status: "idle", wake: make(chan struct{}, 1), notify: true,
	}
}

// NewID returns a random configuration id.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Start loads configurations and starts background work.
func (e *Engine) Start(parent context.Context) error {
	e.ctx, e.cancel = context.WithCancel(parent)
	// The report outbox is flushed by timerLoop (flushReports) while the
	// engine runs and stops with it.
	e.reportOutbox()
	cfgs, err := e.st.Configs()
	if err != nil {
		return err
	}
	e.mu.Lock()
	for i := range cfgs {
		c := cfgs[i]
		c.Normalize()
		e.configs[c.ID] = &c
		e.filters[c.ID] = filter.New(&c)
	}
	e.mu.Unlock()
	for _, c := range cfgs {
		if c.Enabled && c.Mode == "auto" {
			e.startWatcher(c.ID)
		}
	}
	e.wg.Add(2)
	go e.dispatchLoop()
	go e.timerLoop()
	go func() {
		// initial scan of automatic configurations
		for _, c := range e.Configs() {
			if c.Enabled && c.Mode == "auto" {
				_, _ = e.Scan(c.ID, false)
			}
		}
	}()
	return nil
}

// Stop halts background work. In-flight uploads stop and resume later.
func (e *Engine) Stop() {
	if e.cancel == nil {
		return
	}
	e.cancel()
	e.mu.Lock()
	for id, w := range e.watchers {
		w.stop()
		delete(e.watchers, id)
	}
	e.mu.Unlock()
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
	e.st.ReleaseActive()
	if e.EnforcesOwner() {
		e.id.mu.Lock()
		p := e.id.p
		e.id.mu.Unlock()
		e.SetTokens(p) // closes the cached owner tokens
	}
}

// SetClient sets (or clears) the authenticated server client.
func (e *Engine) SetClient(c *client.Client, me *client.Me) {
	e.mu.Lock()
	e.client = c
	e.features = map[string]bool{}
	if me != nil {
		for _, f := range me.Features {
			e.features[f] = true
		}
	}
	if c != nil {
		serverMax := 0
		if me != nil {
			serverMax = me.Server.MaxConcurrentChunks
		}
		e.xfer.Attach(c, serverMax)
		e.netFailures = 0
		e.offlineUntil = time.Time{}
		// New credentials (reconnect after a revocation): the sticky
		// "revoked" status no longer applies.
		if e.status == "revoked" {
			e.status, e.message = "", ""
		}
	}
	e.mu.Unlock()
	if c != nil {
		e.refreshStatus()
		go e.refreshAutomations()
		e.kick()
	}
}

// ApplySettings updates concurrency, bandwidth and notification settings.
func (e *Engine) ApplySettings(s config.Settings) {
	manual := 0
	if s.ConcurrencyMode == "manual" {
		manual = s.ManualParallel
	}
	e.xfer.ApplySettings(transfer.Settings{MaxParallel: s.MaxParallel, ManualParallel: manual, BandwidthKBps: s.BandwidthKBps})
	e.mu.Lock()
	e.notify = s.Notifications
	e.paused = s.Paused
	e.mu.Unlock()
	e.kick()
}

func (e *Engine) kick() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// serverHas reports whether the connected server advertises feature f.
func (e *Engine) serverHas(f string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.features[f]
}

func (e *Engine) getClient() *client.Client {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.client
}

// --- configuration management ---

// ConfigView is a configuration with live status for the UI and server.
type ConfigView struct {
	config.SyncConfig
	FilesSynced  int64  `json:"files_synced"`
	FilesPending int64  `json:"files_pending"`
	FilesFailed  int64  `json:"files_failed"`
	LastScanAt   int64  `json:"last_scan_at"`
	Scanning     bool   `json:"scanning"`
	LastError    string `json:"last_error"`
	Watching     bool   `json:"watching"`
}

func (e *Engine) Configs() []config.SyncConfig {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]config.SyncConfig, 0, len(e.configs))
	for _, c := range e.configs {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func (e *Engine) ConfigViews() []ConfigView {
	cfgs := e.Configs()
	out := make([]ConfigView, 0, len(cfgs))
	for _, c := range cfgs {
		synced, pending, failed := e.st.ConfigCounts(c.ID)
		e.mu.Lock()
		v := ConfigView{SyncConfig: c, FilesSynced: synced, FilesPending: pending, FilesFailed: failed, Scanning: e.scanning[c.ID], LastError: e.lastErr[c.ID], Watching: e.watchers[c.ID] != nil}
		if t := e.lastScan[c.ID]; !t.IsZero() {
			v.LastScanAt = t.UnixMilli()
		}
		e.mu.Unlock()
		out = append(out, v)
	}
	return out
}

func (e *Engine) config(id string) (*config.SyncConfig, *filter.Filter) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.configs[id]
	if c == nil {
		return nil, nil
	}
	cp := *c
	return &cp, e.filters[id]
}

// SaveConfig validates and stores a configuration, restarting its watcher.
func (e *Engine) SaveConfig(c config.SyncConfig) (config.SyncConfig, error) {
	if c.Kind == "files" && len(c.Files) > 0 {
		c.LocalPath = commonDir(c.Files)
	}
	c.Normalize()
	if err := c.Validate(); err != nil {
		return c, err
	}
	for _, other := range e.Configs() {
		// Names are unique per owner (automations start tasks by name within
		// one owner), so the check never reveals another user's folders.
		if other.ID != c.ID && strings.EqualFold(other.Name, c.Name) && strings.EqualFold(other.OwnerSID, c.OwnerSID) {
			return c, fmt.Errorf("another configuration is already called %q", c.Name)
		}
	}
	t := time.Now().UnixMilli()
	var prev *config.SyncConfig
	if c.ID == "" {
		c.ID = NewID()
		c.CreatedAt = t
	} else {
		prev, _ = e.config(c.ID)
	}
	c.UpdatedAt = t
	if err := e.st.SaveConfig(c); err != nil {
		return c, err
	}
	e.mu.Lock()
	cp := c
	e.configs[c.ID] = &cp
	e.filters[c.ID] = filter.New(&cp)
	if w := e.watchers[c.ID]; w != nil {
		w.stop()
		delete(e.watchers, c.ID)
	}
	e.mu.Unlock()
	// If the selection or destination changed, previously skipped files must
	// be evaluated again.
	if prev != nil && (prev.Destination != c.Destination || prev.LocalPath != c.LocalPath) {
		_, _ = e.st.DB.Exec("DELETE FROM files WHERE config_id = ?", c.ID)
	} else if prev != nil {
		_, _ = e.st.DB.Exec("DELETE FROM files WHERE config_id = ? AND status IN ('skipped','queued')", c.ID)
	}
	if !c.Enabled {
		_, _ = e.st.DB.Exec("DELETE FROM queue WHERE config_id = ? AND status != 'active'", c.ID)
	}
	if c.Enabled && c.Mode == "auto" {
		e.startWatcher(c.ID)
		go func() { _, _ = e.Scan(c.ID, false) }()
	}
	return c, nil
}

func (e *Engine) DeleteConfig(id string) error {
	e.mu.Lock()
	if w := e.watchers[id]; w != nil {
		w.stop()
		delete(e.watchers, id)
	}
	delete(e.configs, id)
	delete(e.filters, id)
	e.mu.Unlock()
	return e.st.DeleteConfig(id)
}

func commonDir(files []string) string {
	if len(files) == 0 {
		return ""
	}
	dir := filepath.Dir(files[0])
	for _, f := range files[1:] {
		for !strings.HasPrefix(strings.ToLower(filepath.Dir(f))+`\`, strings.ToLower(dir)+`\`) && !strings.EqualFold(filepath.Dir(f), dir) {
			parent := filepath.Dir(dir)
			if parent == dir {
				return dir
			}
			dir = parent
		}
	}
	return dir
}

// --- pause / resume ---

// SetPaused pauses or resumes synchronisation. Pausing also stops the
// transfers in progress: they go back to the queue and start again (resuming
// the server-side upload session) when synchronisation is resumed.
func (e *Engine) SetPaused(p bool) {
	if !p && e.restartIfRouteChanged() {
		return // unpaused once the interrupted sessions are wiped
	}
	e.mu.Lock()
	e.paused = p
	if p {
		e.recordPausedUploads()
		for _, at := range e.active {
			if at.cancel != nil {
				at.cancel()
			}
		}
	}
	e.mu.Unlock()
	e.refreshStatus()
	e.kick()
}

func (e *Engine) Paused() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.paused
}

// ResetSyncIn forgets the upload index, queue and history of the
// configurations in only (nil = all) and clears their run statistics, so the
// next scan uploads everything again (for example to a fresh server).
func (e *Engine) ResetSyncIn(only []string) (int64, error) {
	n, err := e.st.ResetSyncIn(only)
	if err != nil {
		return 0, err
	}
	e.mu.Lock()
	for id := range e.runs {
		if only == nil || slices.Contains(only, id) {
			delete(e.runs, id)
		}
	}
	e.mu.Unlock()
	e.refreshStatus()
	return n, nil
}

// RetryFailed moves failed items back into the queue.
func (e *Engine) RetryFailed(id int64) (int64, error) { return e.RetryFailedIn(id, nil) }

// RetryFailedIn is RetryFailed limited to the configurations in only (nil
// means all).
func (e *Engine) RetryFailedIn(id int64, only []string) (int64, error) {
	n, err := e.st.RetryFailedIn(id, only)
	e.kick()
	return n, err
}

// --- snapshot ---

type ActiveView struct {
	ID       int64   `json:"id"`
	ConfigID string  `json:"config_id"`
	Config   string  `json:"config"`
	Path     string  `json:"path"`
	Remote   string  `json:"remote"`
	Size     int64   `json:"size"`
	Sent     int64   `json:"sent"`
	Speed    float64 `json:"speed"`
	Phase    string  `json:"phase"`
	Started  int64   `json:"started"`
	Attempts int     `json:"attempts"`
}

type Stats struct {
	Active           int     `json:"active"`
	Queued           int64   `json:"queued"`
	Failed           int64   `json:"failed"`
	PendingBytes     int64   `json:"pending_bytes"`
	SpeedBps         float64 `json:"speed_bps"`
	Concurrency      int     `json:"concurrency"`
	ConcurrencyLimit int     `json:"concurrency_limit"`
	ETASeconds       float64 `json:"eta_seconds"`
	SessionCompleted int     `json:"session_completed"`
	SessionBytes     int64   `json:"session_bytes"`
	// LastFailedAt (unix ms) lets the UI hide a dismissed failure warning
	// until a newer failure.
	LastFailedAt int64 `json:"last_failed_at"`
}

type Snapshot struct {
	Status      string       `json:"status"`
	Message     string       `json:"message"`
	Paused      bool         `json:"paused"`
	Connected   bool         `json:"connected"`
	Stats       Stats        `json:"stats"`
	Active      []ActiveView `json:"active"`
	Automations int          `json:"automations"`
	Direct      DirectView   `json:"direct"`
}

// DirectView is the state of direct (LAN) uploads for the UI.
type DirectView struct {
	Enabled bool   `json:"enabled"`
	Active  bool   `json:"active"` // uploads go to the local address now
	URL     string `json:"url"`
	Message string `json:"message"`
}

// SetDirect records the state of direct uploads (set by the agent host).
func (e *Engine) SetDirect(v DirectView) {
	e.mu.Lock()
	e.direct = v
	e.mu.Unlock()
}

func (e *Engine) Snapshot() Snapshot {
	pending, failed, pendingBytes := e.st.QueueStats()
	var lastFailed int64
	if failed > 0 {
		lastFailed = e.st.LastFailedAt()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s := Snapshot{Status: e.status, Message: e.message, Paused: e.paused, Connected: e.client != nil, Automations: len(e.autos), Direct: e.direct}
	var speed float64
	var sentActive int64
	for _, a := range e.active {
		a.mu.Lock()
		sp := a.speed
		a.mu.Unlock()
		speed += sp
		sentActive += a.sent.Load()
		ph, _ := a.phase.Load().(string)
		cfg := ""
		if c := e.configs[a.item.ConfigID]; c != nil {
			cfg = c.Name
		}
		s.Active = append(s.Active, ActiveView{ID: a.item.ID, ConfigID: a.item.ConfigID, Config: cfg, Path: a.item.RelPath, Remote: a.item.RemotePath(), Size: a.size, Sent: a.sent.Load(), Speed: sp, Phase: ph, Started: a.started.UnixMilli(), Attempts: a.item.Attempts})
	}
	// Queue order (the order files are claimed in), so a row keeps its place
	// when its transfer is retried instead of jumping to the end.
	sort.Slice(s.Active, func(i, j int) bool { return s.Active[i].ID < s.Active[j].ID })
	if s.Active == nil {
		s.Active = []ActiveView{}
	}
	s.Stats = Stats{Active: len(e.active), Queued: pending, Failed: failed, LastFailedAt: lastFailed, PendingBytes: pendingBytes, SpeedBps: speed, Concurrency: e.ctrl.Inflight(), ConcurrencyLimit: e.ctrl.Limit(), SessionCompleted: e.session.completed, SessionBytes: e.session.bytes}
	if speed > 1024 {
		s.Stats.ETASeconds = float64(max(0, pendingBytes-sentActive)) / speed
	}
	return s
}

func (e *Engine) setStatus(status, msg string) {
	e.mu.Lock()
	e.status, e.message = status, msg
	e.mu.Unlock()
}

// Items returns queue items for the UI, limited to the configurations in
// only (nil means all).
func (e *Engine) Items(status string, only []string, limit int) ([]state.Item, error) {
	return e.st.ItemsIn(status, only, limit)
}

// History returns finished transfers, limited to the configurations in only
// (nil means all).
func (e *Engine) History(status string, only []string, limit int) ([]state.HistoryEntry, error) {
	return e.st.HistoryIn(status, only, limit)
}

var errRevoked = errors.New("device revoked")
