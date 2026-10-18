// Package modules is the agent's module manager (MASTER_PLAN §7.7, api-v1
// §6A). It persists the install mode and per-module state in state.db,
// toggles the built-in modules, switches modes without ever deleting jobs,
// folders or data, exposes the UI gating API and builds the
// PUT /device/modules report.
//
// Component download, verification and supervision are Phase 3; this
// package only defines the Downloader seam and a no-op stub.
package modules

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	sdkmod "github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/modules"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// State is the local state of a module. It is a superset of the wire states
// (sdk/modules.State): suspended and wanted are local only and are reported
// as "disabled" (see wireState).
type State string

const (
	StateEnabled    State = "enabled"
	StateDisabled   State = "disabled"
	StateSuspended  State = "suspended"  // turned off by a switch to Simple; data and binaries kept
	StateWanted     State = "wanted"     // component needed by the mode but not installed yet
	StateInstalling State = "installing" // component download/verify/install in progress
	StateFailed     State = "failed"     // component install failed; Error says why
)

// EventKind is the agent event kind for module changes. The agent package
// should add `KindModules EventKind = "modules"` (payload ModulesState) and
// wire Options.OnChange to Bus.Publish.
const EventKind = "modules"

var (
	ErrUnknownModule = errors.New("modules: unknown module")
	ErrNotBuiltin    = errors.New("modules: not a built-in module")
	ErrInvalidMode   = errors.New("modules: invalid install mode")
	ErrModeRequired  = errors.New("modules: module needs Advanced mode")
)

// ModuleInfo is one module in a ModulesState.
type ModuleInfo struct {
	ID        sdkmod.ID   `json:"id"`
	Kind      sdkmod.Kind `json:"kind"`
	Version   string      `json:"version"`
	State     State       `json:"state"`
	Error     string      `json:"error,omitempty"`
	UpdatedAt int64       `json:"updated_at"` // unix ms
}

// ModulesState is the UI gating snapshot and the payload of module events.
type ModulesState struct {
	InstallMode sdkmod.Mode  `json:"install_mode"`
	CoreVersion string       `json:"core_version"`
	ChangedAt   int64        `json:"changed_at"`  // unix ms
	ReportedAt  int64        `json:"reported_at"` // unix ms; 0 = never
	Modules     []ModuleInfo `json:"modules"`     // contract table order
	// PendingRequest is a server mode_change_request waiting for local
	// administrator confirmation (see ConfirmModeRequest); nil = none.
	PendingRequest *ModeRequest `json:"pending_request,omitempty"`
}

// AffectedItem is a job, sync folder, automation or profile that needs
// modules outside Simple mode.
type AffectedItem struct {
	Kind     string      `json:"kind"` // e.g. backup_job, sync_folder, automation, profile
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Requires []sdkmod.ID `json:"requires"`
	Reason   string      `json:"reason,omitempty"` // e.g. "two-way sync stops; no files are deleted"
}

// ItemSource is implemented by the owners of jobs, sync folders, automations
// and profiles. Suspend and Resume only flip a suspended_by_mode flag; they
// must never delete an item, its files or its backups.
type ItemSource interface {
	// Affected lists the items that need any of the given modules.
	Affected(ctx context.Context, disabling []sdkmod.ID) ([]AffectedItem, error)
	// Suspend marks the items suspended_by_mode.
	Suspend(ctx context.Context, items []AffectedItem) error
	// Resume clears suspended_by_mode on items whose modules are all enabled.
	Resume(ctx context.Context, enabled []sdkmod.ID) error
}

// SwitchPreview is what a mode switch will do; Advanced→Simple shows it for
// confirmation (§7.7 step 1).
type SwitchPreview struct {
	From      sdkmod.Mode    `json:"from"`
	To        sdkmod.Mode    `json:"to"`
	Enable    []sdkmod.ID    `json:"enable,omitempty"`
	Want      []sdkmod.ID    `json:"want,omitempty"` // components to download
	Suspend   []sdkmod.ID    `json:"suspend,omitempty"`
	Affected  []AffectedItem `json:"affected,omitempty"`
	NoDeletes bool           `json:"no_deletes"` // always true: switching never deletes anything
}

// Sender sends a PUT /device/modules body. P1-09 supplies the client call.
type Sender func(ctx context.Context, body wire.ModuleReport) error

// Options configures a Manager.
type Options struct {
	DB          *sql.DB // state.Store.DB (migration 5 tables)
	ModeFile    string  // installer-written ...\config\mode.json; "" = none
	CoreVersion string
	Downloader  Downloader         // nil = NoopDownloader
	Sources     []ItemSource       // more can be added later with RegisterSource
	Send        Sender             // nil = reports are not sent
	OnChange    func(ModulesState) // e.g. bus.Publish(agent.KindModules, s)
	Log         *slog.Logger
	Now         func() time.Time
	// Report loop tuning (RunReporter); zero = defaults (24h, 30s, 1h).
	ReportInterval time.Duration
	RetryMin       time.Duration
	RetryMax       time.Duration
	// ModeRequestTimeout bounds how long HandleTask waits for the local
	// administrator; zero = 24h.
	ModeRequestTimeout time.Duration
}

// Manager owns the install mode and module states.
type Manager struct {
	opts Options
	log  *slog.Logger
	dl   Downloader

	mu      sync.Mutex
	st      ModulesState
	pending *pendingRequest // guarded by mu

	srcMu sync.Mutex
	srcs  []ItemSource

	kick      chan struct{} // buffered(1); wakes RunReporter
	reporting atomic.Bool   // RunReporter is running
}

// ModeFile is the JSON shape of config/mode.json (written by the installer
// and kept in sync by the Manager).
type ModeFile struct {
	Mode sdkmod.Mode `json:"mode"`
}

// New loads (or initialises) the persisted state. On first run the mode
// comes from the installer's mode file; without one it is Advanced when
// state.db already has sync folders (an upgrade) and Simple otherwise. Once
// state.db holds a mode it is authoritative.
func New(ctx context.Context, opts Options) (*Manager, error) {
	if opts.DB == nil {
		return nil, errors.New("modules: nil DB")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	dl := opts.Downloader
	if dl == nil {
		dl = NoopDownloader{}
	}
	m := &Manager{opts: opts, log: opts.Log.With("component", "modules"), dl: dl,
		srcs: slices.Clone(opts.Sources), kick: make(chan struct{}, 1)}
	if err := m.load(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) now() int64 { return m.opts.Now().UnixMilli() }

func (m *Manager) load(ctx context.Context) error {
	db := m.opts.DB
	var mode string
	var changed int64
	var reported sql.NullInt64
	err := db.QueryRowContext(ctx, "SELECT mode, changed_at, reported_at FROM install_mode WHERE id = 1").Scan(&mode, &changed, &reported)
	fresh := errors.Is(err, sql.ErrNoRows)
	if err != nil && !fresh {
		return fmt.Errorf("modules: load mode: %w", err)
	}
	if fresh {
		mode = string(sdkmod.ModeSimple)
		if f, ferr := ReadModeFile(m.opts.ModeFile); ferr == nil {
			mode = string(f.Mode)
		} else {
			if !errors.Is(ferr, os.ErrNotExist) {
				m.log.Warn("ignoring mode file", "path", m.opts.ModeFile, "err", ferr)
			}
			// Upgrade from a pre-modules release: sync folders are an
			// Advanced feature, so an install that already has them stays
			// Advanced (persist below writes mode.json). Fresh installs keep
			// the Simple default.
			if hasLegacySyncFolders(ctx, db) {
				m.log.Info("existing sync folders found; defaulting to advanced mode")
				mode = string(sdkmod.ModeAdvanced)
			}
		}
		changed = m.now()
	} else if !sdkmod.Mode(mode).Valid() {
		m.log.Warn("invalid stored mode, using simple", "mode", mode)
		mode = string(sdkmod.ModeSimple)
	}

	rows := map[sdkmod.ID]ModuleInfo{}
	r, err := db.QueryContext(ctx, "SELECT id, version, state, error, updated_at FROM modules")
	if err != nil {
		return fmt.Errorf("modules: load modules: %w", err)
	}
	for r.Next() {
		var mi ModuleInfo
		if err := r.Scan(&mi.ID, &mi.Version, &mi.State, &mi.Error, &mi.UpdatedAt); err != nil {
			r.Close()
			return err
		}
		rows[mi.ID] = mi
	}
	r.Close()
	if err := r.Err(); err != nil {
		return err
	}

	st := ModulesState{InstallMode: sdkmod.Mode(mode), CoreVersion: m.opts.CoreVersion, ChangedAt: changed, ReportedAt: reported.Int64}
	now := m.now()
	for _, id := range sdkmod.All {
		kind, _ := sdkmod.KindOf(id)
		mi, ok := rows[id]
		if !ok {
			mi = ModuleInfo{ID: id, State: defaultState(id, st.InstallMode), UpdatedAt: now}
		}
		mi.Kind = kind
		if id == sdkmod.Core {
			mi.Version, mi.State, mi.Error = m.opts.CoreVersion, StateEnabled, ""
		}
		if kind == sdkmod.KindBuiltin && mi.State == StateEnabled {
			mi.Version = m.opts.CoreVersion
		}
		st.Modules = append(st.Modules, mi)
	}
	m.st = st
	return m.persist(ctx, fresh)
}

// hasLegacySyncFolders reports whether state.db already holds sync folder
// configurations. A missing configs table (a store opened without the legacy
// schema) counts as none.
func hasLegacySyncFolders(ctx context.Context, db *sql.DB) bool {
	var n int
	if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM configs)").Scan(&n); err != nil {
		return false
	}
	return n != 0
}

// defaultState is the state of a module on first run in mode.
func defaultState(id sdkmod.ID, mode sdkmod.Mode) State {
	switch {
	case id == sdkmod.Core:
		return StateEnabled
	case mode != sdkmod.ModeAdvanced:
		return StateDisabled
	case sdkmod.IsComponent(id):
		return StateWanted
	}
	return StateEnabled
}

// persist writes m.st to state.db (and mode.json when writeFile). Caller
// holds mu or is in New.
func (m *Manager) persist(ctx context.Context, writeFile bool) error {
	tx, err := m.opts.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var reported any
	if m.st.ReportedAt > 0 {
		reported = m.st.ReportedAt
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO install_mode (id, mode, core_version, changed_at, reported_at) VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET mode = excluded.mode, core_version = excluded.core_version,
		changed_at = excluded.changed_at, reported_at = excluded.reported_at`,
		string(m.st.InstallMode), m.st.CoreVersion, m.st.ChangedAt, reported); err != nil {
		return fmt.Errorf("modules: save mode: %w", err)
	}
	// Upsert only: module rows are never deleted.
	for _, mi := range m.st.Modules {
		if _, err := tx.ExecContext(ctx, `INSERT INTO modules (id, version, state, error, updated_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET version = excluded.version, state = excluded.state,
			error = excluded.error, updated_at = excluded.updated_at`,
			string(mi.ID), mi.Version, string(mi.State), mi.Error, mi.UpdatedAt); err != nil {
			return fmt.Errorf("modules: save %s: %w", mi.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if writeFile && m.opts.ModeFile != "" {
		if err := WriteModeFile(m.opts.ModeFile, m.st.InstallMode); err != nil {
			// The DB is authoritative; a stale file only affects a reinstall's preselection.
			m.log.Warn("write mode file", "path", m.opts.ModeFile, "err", err)
		}
	}
	return nil
}

// ReadModeFile reads and validates config/mode.json.
func ReadModeFile(path string) (ModeFile, error) {
	var f ModeFile
	if path == "" {
		return f, os.ErrNotExist
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return f, fmt.Errorf("modules: parse %s: %w", path, err)
	}
	if !f.Mode.Valid() {
		return f, fmt.Errorf("%w: %q", ErrInvalidMode, f.Mode)
	}
	return f, nil
}

// WriteModeFile atomically writes config/mode.json.
func WriteModeFile(path string, mode sdkmod.Mode) error {
	raw, err := json.Marshal(ModeFile{Mode: mode})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Mode returns the current install mode.
func (m *Manager) Mode() sdkmod.Mode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st.InstallMode
}

// Enabled reports whether a module is usable now (UI gating). Core is
// always enabled.
func (m *Manager) Enabled(id sdkmod.ID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mi := m.find(id); mi != nil {
		return mi.State == StateEnabled
	}
	return false
}

// Snapshot returns a copy of the current state.
func (m *Manager) Snapshot() ModulesState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *Manager) snapshotLocked() ModulesState {
	s := m.st
	s.Modules = slices.Clone(m.st.Modules)
	if m.pending != nil {
		r := m.pending.req
		s.PendingRequest = &r
	}
	return s
}

func (m *Manager) find(id sdkmod.ID) *ModuleInfo {
	for i := range m.st.Modules {
		if m.st.Modules[i].ID == id {
			return &m.st.Modules[i]
		}
	}
	return nil
}

func (m *Manager) setState(id sdkmod.ID, s State, version, errMsg string, now int64) {
	mi := m.find(id)
	if mi == nil || (mi.State == s && mi.Version == version && mi.Error == errMsg) {
		return
	}
	mi.State, mi.Version, mi.Error, mi.UpdatedAt = s, version, errMsg, now
}

// PreviewSwitch reports what SetMode(target) would change, including the
// items that would be suspended (never deleted) by a switch to Simple.
func (m *Manager) PreviewSwitch(ctx context.Context, target sdkmod.Mode) (SwitchPreview, error) {
	if !target.Valid() {
		return SwitchPreview{}, fmt.Errorf("%w: %q", ErrInvalidMode, target)
	}
	m.mu.Lock()
	p := m.planLocked(target)
	m.mu.Unlock()
	_, all, err := m.affected(ctx, m.sources(), p.Suspend)
	if err != nil {
		return SwitchPreview{}, fmt.Errorf("modules: preview: %w", err)
	}
	p.Affected = all
	return p, nil
}

// affected asks every source for the items needing ids, per source and in total.
func (m *Manager) affected(ctx context.Context, srcs []ItemSource, ids []sdkmod.ID) ([][]AffectedItem, []AffectedItem, error) {
	if len(ids) == 0 {
		return nil, nil, nil
	}
	per := make([][]AffectedItem, len(srcs))
	var all []AffectedItem
	for i, src := range srcs {
		items, err := src.Affected(ctx, ids)
		if err != nil {
			return nil, nil, err
		}
		per[i] = items
		all = append(all, items...)
	}
	return per, all, nil
}

// suspend marks the items needing ids suspended_by_mode in every source.
func (m *Manager) suspend(ctx context.Context, ids []sdkmod.ID) error {
	srcs := m.sources()
	per, _, err := m.affected(ctx, srcs, ids)
	if err != nil {
		return fmt.Errorf("modules: suspend: %w", err)
	}
	for i, items := range per {
		if len(items) == 0 {
			continue
		}
		if err := srcs[i].Suspend(ctx, items); err != nil {
			return fmt.Errorf("modules: suspend: %w", err)
		}
	}
	return nil
}

func (m *Manager) planLocked(target sdkmod.Mode) SwitchPreview {
	p := SwitchPreview{From: m.st.InstallMode, To: target, NoDeletes: true}
	for _, mi := range m.st.Modules {
		if mi.ID == sdkmod.Core {
			continue
		}
		in := sdkmod.InMode(mi.ID, target)
		switch {
		case in && mi.Kind == sdkmod.KindBuiltin && mi.State != StateEnabled:
			p.Enable = append(p.Enable, mi.ID)
		case in && mi.Kind == sdkmod.KindComponent && mi.State != StateEnabled:
			p.Want = append(p.Want, mi.ID)
		case !in && (mi.State == StateEnabled || mi.State == StateWanted || mi.State == StateInstalling || mi.State == StateFailed):
			p.Suspend = append(p.Suspend, mi.ID)
		}
	}
	return p
}

// SetMode switches the install mode (§7.7). The caller has already obtained
// the administrator confirmation and, for Advanced→Simple, the user's
// confirmation of PreviewSwitch. Nothing is ever deleted: Advanced-only
// items become suspended_by_mode, modules become suspended and component
// binaries stay installed. The returned preview is what was applied.
func (m *Manager) SetMode(ctx context.Context, target sdkmod.Mode) (SwitchPreview, error) {
	p, err := m.PreviewSwitch(ctx, target)
	if err != nil {
		return p, err
	}
	// Suspend items before turning their modules off so nothing runs half-enabled.
	if err := m.suspend(ctx, p.Suspend); err != nil {
		return p, err
	}

	m.mu.Lock()
	now := m.now()
	modeChanged := m.st.InstallMode != target
	m.st.InstallMode = target
	if modeChanged {
		m.st.ChangedAt = now
	}
	for _, id := range p.Suspend {
		mi := m.find(id)
		m.setState(id, StateSuspended, mi.Version, "", now)
	}
	for _, id := range p.Enable {
		m.setState(id, StateEnabled, m.opts.CoreVersion, "", now)
	}
	for _, id := range p.Want {
		mi := m.find(id)
		m.setState(id, StateWanted, mi.Version, "", now)
	}
	err = m.persist(ctx, true)
	m.mu.Unlock()
	if err != nil {
		return p, err
	}
	m.log.Info("install mode set", "from", p.From, "to", target, "enabled", p.Enable, "wanted", p.Want, "suspended", p.Suspend, "affected", len(p.Affected))

	m.installWanted(ctx, p.Want)
	m.resume(ctx)
	m.changed(ctx)
	return p, nil
}

// SetBuiltin toggles a built-in module (sync, automation, profiles).
// Enabling requires Advanced mode. Disabling suspends the items that need
// the module; nothing is deleted.
func (m *Manager) SetBuiltin(ctx context.Context, id sdkmod.ID, enabled bool) error {
	if !sdkmod.Known(id) {
		return fmt.Errorf("%w: %q", ErrUnknownModule, id)
	}
	if !sdkmod.IsBuiltin(id) {
		return fmt.Errorf("%w: %q", ErrNotBuiltin, id)
	}
	if enabled && m.Mode() != sdkmod.ModeAdvanced {
		return fmt.Errorf("%w: %q", ErrModeRequired, id)
	}
	if !enabled {
		if err := m.suspend(ctx, []sdkmod.ID{id}); err != nil {
			return err
		}
	}
	m.mu.Lock()
	if enabled {
		m.setState(id, StateEnabled, m.opts.CoreVersion, "", m.now())
	} else {
		m.setState(id, StateDisabled, m.find(id).Version, "", m.now())
	}
	err := m.persist(ctx, false)
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if enabled {
		m.resume(ctx)
	}
	m.changed(ctx)
	return nil
}

// installWanted hands wanted components to the Downloader. With the no-op
// stub they stay wanted.
func (m *Manager) installWanted(ctx context.Context, ids []sdkmod.ID) {
	for _, id := range ids {
		m.mu.Lock()
		prev := *m.find(id)
		m.mu.Unlock()
		version, err := m.dl.Install(ctx, id)
		m.mu.Lock()
		switch {
		case errors.Is(err, ErrDownloadUnavailable):
			// Stays wanted until the Phase 3 pipeline exists.
		case err != nil:
			m.log.Warn("component install failed", "module", id, "err", err)
			m.setState(id, StateFailed, prev.Version, err.Error(), m.now())
		default:
			m.setState(id, StateEnabled, version, "", m.now())
		}
		if perr := m.persist(ctx, false); perr != nil {
			m.log.Warn("persist module state", "module", id, "err", perr)
		}
		m.mu.Unlock()
	}
}

func (m *Manager) resume(ctx context.Context) {
	var enabled []sdkmod.ID
	for _, mi := range m.Snapshot().Modules {
		if mi.State == StateEnabled {
			enabled = append(enabled, mi.ID)
		}
	}
	for _, src := range m.sources() {
		if err := src.Resume(ctx, enabled); err != nil {
			m.log.Warn("resume items", "err", err)
		}
	}
}

// changed publishes the new state and reports it (best effort; the daily
// report retries).
func (m *Manager) changed(ctx context.Context) {
	if m.opts.OnChange != nil {
		m.opts.OnChange(m.Snapshot())
	}
	if m.opts.Send == nil {
		return
	}
	if m.reporting.Load() {
		m.Kick() // RunReporter sends it and retries with backoff
		return
	}
	if err := m.Report(ctx); err != nil {
		m.log.Warn("module report failed", "err", err)
	}
}

// wireState maps a local state to the api-v1 §6A report state.
func wireState(s State) string {
	switch s {
	case StateEnabled:
		return wire.ModuleStateEnabled
	case StateInstalling:
		return wire.ModuleStateInstalling
	case StateFailed:
		return wire.ModuleStateFailed
	}
	return wire.ModuleStateDisabled // disabled, suspended, wanted
}

// ReportBody builds the PUT /device/modules body.
func (m *Manager) ReportBody() wire.ModuleReport {
	s := m.Snapshot()
	r := wire.ModuleReport{InstallMode: string(s.InstallMode), CoreVersion: s.CoreVersion, Modules: make([]wire.ModuleStatus, 0, len(s.Modules))}
	for _, mi := range s.Modules {
		r.Modules = append(r.Modules, wire.ModuleStatus{ID: string(mi.ID), Version: mi.Version, State: wireState(mi.State), Error: mi.Error})
	}
	return r
}

// Report sends the current state through Options.Send (at enrolment, on
// change and daily) and records reported_at.
func (m *Manager) Report(ctx context.Context) error {
	if m.opts.Send == nil {
		return errors.New("modules: no sender")
	}
	if err := m.opts.Send(ctx, m.ReportBody()); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.ReportedAt = m.now()
	_, err := m.opts.DB.ExecContext(ctx, "UPDATE install_mode SET reported_at = ? WHERE id = 1", m.st.ReportedAt)
	return err
}
