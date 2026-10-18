# P0-26 (DSK-A) agent.API + host + event bus (in-process for tests)
Status: completed

## Goal
Add the transport-independent agent core in `desktop/internal/agent`:
- the `agent.API` interface;
- a `Host` that owns the engine, state store, settings, device identity and updater;
- a typed event bus with bounded, drop-oldest subscriber buffers;
- an in-process client (`NewInProc`).

`app.go` and `main.go` are unchanged. P0-29 rewires them.

## Changes (repo: path, created/modified/deleted, why)
All files are in the client repo `VaultDock` and were created by this task.
- `desktop/internal/agent/api.go`: `API` and `Subscription` interfaces, the request/response structs, `ProtocolVersion`, the transport-safe `*Error` with codes, and `AsError`.
- `desktop/internal/agent/events.go`: `EventKind` constants, `Event`, the payload types, `Bus`, and `LogHandler` (an slog tee onto the bus).
- `desktop/internal/agent/host.go`: `Options`, `KeyStore`, `FileKeyStore` (DPAPI file, the same format as today's `device.key`), and `Host`. `Host` handles lifecycle, connection, enrolment polling, configs, transfers, automations, settings and Subscribe. The logic moved from `app.go` with the Wails calls replaced by bus events.
- `desktop/internal/agent/updates.go`: the update API and the optional background auto-update loop, moved from `app_update.go`. `wruntime.Quit` is replaced by the `Options.RequestExit` hook, and the installer launches from `Host.Close`.
- `desktop/internal/agent/inproc.go`: `InProc`. Every request and response is copied through JSON, and errors come back as `*Error`, matching transport semantics.
- `desktop/internal/agent/events_test.go`: fan-out and kind filtering; slow subscriber (drop-oldest, `Dropped` count, fast subscriber not blocked); unsubscribe, ctx-bound subscription, bus close and publish after close; marshal error; `LogHandler` levels, attributes and groups.
- `desktop/internal/agent/host_test.go`:
  - Host lifecycle: idempotent Start and Close, the shutdown event, subscriptions closed on Close, `unavailable` after Close, `state.db` created.
  - Periodic snapshot events.
  - Round trips through InProc: SaveConfig (including the duplicate-name → `invalid` error), ListConfigs, PreviewConfig, SyncNow (queues the file without a server; unknown id → `not_found`), ListQueue (bad status → `invalid`), ListHistory, RetryFailed, Snapshot, ListAutomations.
  - Settings: GetSettings and SaveSettings (identity fields protected, `settings` event), SetPaused reaching the engine, settings persisted across a host restart.
  - Updates: UpdateStatus is disabled and InstallUpdate returns `unavailable`.
  - Connect input validation, CancelEnrollment, Disconnect and DeleteConfig.
  - A cancelled ctx returns `canceled`, and `Error.Is` still matches after a JSON round trip.
  - The tests use an in-memory KeyStore and no AutoStart hooks, so they touch no registry and no DPAPI.

## Interfaces exposed (exact Go/TS signatures, routes, schema others depend on)
```go
package agent // github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/agent

const ProtocolVersion = 1

type API interface {
	Bootstrap(ctx context.Context) (Bootstrap, error)
	ProbeServer(ctx context.Context, req ProbeRequest) (ProbeResult, error)
	Connect(ctx context.Context, req ConnectRequest) (EnrollView, error)
	CancelEnrollment(ctx context.Context) error
	Disconnect(ctx context.Context) error

	ListConfigs(ctx context.Context) ([]engine.ConfigView, error)
	SaveConfig(ctx context.Context, c config.SyncConfig) (config.SyncConfig, error)
	DeleteConfig(ctx context.Context, id string) error
	SyncNow(ctx context.Context, id string) error // "" = all enabled
	PreviewConfig(ctx context.Context, c config.SyncConfig) (PreviewResult, error)

	Snapshot(ctx context.Context) (engine.Snapshot, error)
	SetPaused(ctx context.Context, paused bool) error
	RetryFailed(ctx context.Context, id int64) (int64, error)            // 0 = all
	ListQueue(ctx context.Context, status string) ([]state.Item, error)  // "", pending, active, failed
	ListHistory(ctx context.Context, status string) ([]state.HistoryEntry, error)

	ListAutomations(ctx context.Context) ([]engine.AutomationView, error)
	RefreshAutomations(ctx context.Context) ([]engine.AutomationView, error)

	GetSettings(ctx context.Context) (config.Settings, error)
	SaveSettings(ctx context.Context, s config.Settings) (config.Settings, error) // identity fields ignored

	UpdateStatus(ctx context.Context) (updater.Status, error)
	CheckForUpdates(ctx context.Context) (updater.Status, error)
	InstallUpdate(ctx context.Context) error

	Subscribe(ctx context.Context, kinds ...EventKind) (Subscription, error) // ctx bounds the subscription's lifetime

	// Reserved (documented in api.go; not implemented; hosts return ErrNotImplemented until they land):
	//   Backup:   ListBackupJobs, SaveBackupJob, DeleteBackupJob, RunBackup, CancelBackup, ListSnapshots, Readiness, ListIssues, ResolveIssue
	//   Restore:  PlanRestore, StartRestore, CancelRestore
	//   Keys:     KeyStatus, ConfirmRecoveryKey
	//   Modules (P0-38): GetMode, SetMode, ListModules, SetModule
	//   Profiles: ListProfiles, GiveConsent, RevokeConsent
}

type Subscription interface {
	C() <-chan Event // closed when the subscription or bus closes
	Dropped() uint64
	Close()
}

type Bootstrap struct {
	Version         string          `json:"version"`
	ProtocolVersion int             `json:"protocol_version"`
	Connected       bool            `json:"connected"`
	Configured      bool            `json:"configured"`
	Revoked         bool            `json:"revoked"`
	Enrollment      *EnrollView     `json:"enrollment"`
	Settings        config.Settings `json:"settings"`
	DeviceName      string          `json:"device_name"`
	OS              string          `json:"os"`
	User            string          `json:"user"`
	ServerInfo      *ServerInfo     `json:"server_info"`
}
type ServerInfo struct {
	Version  string `json:"version"`
	SiteName string `json:"site_name"`
	Quota    int64  `json:"quota"`
	Used     int64  `json:"used"`
}
type ProbeRequest struct {
	URL string `json:"url"`
	Pin string `json:"pin,omitempty"`
}
type ProbeResult struct { // same JSON as app.go ProbeResult
	OK          bool           `json:"ok"`
	Info        map[string]any `json:"info,omitempty"`
	Untrusted   bool           `json:"untrusted"`
	Fingerprint string         `json:"fingerprint,omitempty"`
	Subject     string         `json:"subject,omitempty"`
	Reason      string         `json:"reason,omitempty"`
	URL         string         `json:"url"`
}
type ConnectRequest struct {
	URL      string `json:"url"`
	Login    string `json:"login"`
	Password string `json:"password"`
	Pin      string `json:"pin,omitempty"`
}
type EnrollView struct { // same JSON as app.go EnrollView
	Status         string `json:"status"` // pending, approved, rejected, expired, error
	UserCode       string `json:"user_code"`
	ExpiresAt      int64  `json:"expires_at"`
	KeyFingerprint string `json:"key_fingerprint"`
	DeviceName     string `json:"device_name"`
	Username       string `json:"username"`
	ServerURL      string `json:"server_url"`
	Error          string `json:"error,omitempty"`
}
type PreviewResult struct {
	Entries []engine.PreviewEntry `json:"entries"`
	Total   int                   `json:"total"`
}

// Errors
const (
	CodeInvalid        = "invalid"
	CodeNotFound       = "not_found"
	CodeUnavailable    = "unavailable"
	CodeNotImplemented = "not_implemented"
	CodeCanceled       = "canceled"
	CodeInternal       = "internal"
)
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
} // Error() = Message; Is() matches by Code
var ErrNotImplemented *Error // code not_implemented
var ErrClosed *Error         // code unavailable
func AsError(err error) *Error // nil→nil; ctx errors→canceled; state.ErrNotFound→not_found; else internal

// Events
type EventKind string
const (
	KindSnapshot     EventKind = "snapshot"     // engine.Snapshot, every SnapshotInterval (1s) while anyone subscribes
	KindConnection   EventKind = "connection"   // ConnectionEvent
	KindEnrollment   EventKind = "enrollment"   // EnrollView
	KindSettings     EventKind = "settings"     // config.Settings
	KindUpdate       EventKind = "update"       // updater.Status
	KindNotification EventKind = "notification" // Notification (the UI shows a toast)
	KindLog          EventKind = "log"          // LogEvent
	KindProgress     EventKind = "progress"     // ProgressEvent (backup/restore; not yet emitted)
	KindIssue        EventKind = "issue"        // IssueEvent (not yet emitted)
	KindShutdown     EventKind = "shutdown"     // ShutdownEvent
)
type Event struct {
	Seq  uint64          `json:"seq"`
	Kind EventKind       `json:"kind"`
	Time int64           `json:"time"` // unix ms
	Data json.RawMessage `json:"data,omitempty"`
}
func (e Event) Decode(v any) error

type ConnectionEvent struct{ State string `json:"state"` } // connected, disconnected, revoked
type Notification struct{ Title, Message, Level string }   // json: title, message, level (info|warning|error)
type LogEvent struct {
	Time    int64             `json:"time"`
	Level   string            `json:"level"`
	Message string            `json:"message"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}
type ProgressEvent struct{ Job, ID, Phase string; Done, Total, Bytes, TotalBytes int64; Message string } // json snake_case: job,id,phase,done,total,bytes,total_bytes,message
type IssueEvent struct{ Source, ConfigID, RelPath, Class, Message, State string }                       // json: source,config_id,rel_path,class,message,state
type ShutdownEvent struct{ Reason string `json:"reason"` }                                               // stop, update

const DefaultBuffer = 64
func NewBus(buffer int) *Bus
func (b *Bus) Subscribe(kinds ...EventKind) Subscription // none = all kinds
func (b *Bus) SubscribeContext(ctx context.Context, kinds ...EventKind) Subscription
func (b *Bus) Publish(kind EventKind, payload any) error // never blocks; drop-oldest per subscriber; err only on marshal failure
func (b *Bus) HasSubscribers(kind EventKind) bool
func (b *Bus) Close()
func NewLogHandler(bus *Bus, next slog.Handler, level slog.Leveler) *LogHandler // slog.Handler

// Host
type Options struct {
	Log                        *slog.Logger
	DataDir                    string       // settings.json, device.key, state.db; default platform.DataDir()
	Store                      *state.Store // optional; else DataDir/state.db, owned and closed by Host
	DeviceName, OS, Identifier string       // defaults from platform
	Keys                       KeyStore     // default FileKeyStore{DataDir/device.key}
	AutoStart                  func(enabled bool) error // nil = unmanaged (service mode, tests)
	AutoStartEnabled           func() bool
	UpdateRepo, UpdateDir      string // empty repo disables updates
	AutoUpdates                bool
	InstallDir                 string // default: executable dir
	RequestExit                func() // owner should stop the host; Close launches the pending installer
	SnapshotInterval           time.Duration
	EventBuffer                int
	LogEventLevel              slog.Leveler
}
type KeyStore interface {
	Load() (ed25519.PrivateKey, error)
	Save(ed25519.PrivateKey) error
	Delete() error
}
type FileKeyStore struct{ Path string } // platform.Protect / Unprotect
func NewHost(opts Options) (*Host, error)
func (h *Host) Start(ctx context.Context) error // idempotent; ErrClosed after Close
func (h *Host) Close() error                    // idempotent
func (h *Host) Bus() *Bus
func (h *Host) Engine() *engine.Engine

// In-process client
func NewInProc(api API) *InProc // implements API
```

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `gofmt -l internal/agent`: clean.
- `go build ./...`: ok.
- `go vet ./internal/agent/`: ok.
- `go test ./internal/agent/`: ok. Repeated with `-count=5`: ok.
- `-race` was not run because cgo is unavailable on this host.

## Contract issues
None. The agent API is internal and not part of `api-v1.md`.

## Gaps / follow-ups
- **Issues and progress events are defined but never emitted.**
  - `engine.recordIssue` has no hook. DSK-B/DSK-H could add an `Engine.OnIssue` callback in `engine/issues.go` for the Host to forward as `KindIssue`.
  - `KindProgress` waits for the backup engine.
- **No caller identity or authorisation.** P0-28 should carry the caller SID and admin flag in `ctx`, for example an `agent.WithCaller(ctx, Caller)` helper added in this package by P0-28 or a follow-up. The Host would then check it.
- **No ListIssues API method yet.** It is reserved, and `state` has no issue accessors.
- **Not done in the Host:**
  - the `--updated` "VaultDock updated" toast, which is a UI/`main.go` concern;
  - `SyncNow` scan goroutines are not awaited by `Close`. This matches today's `app.go`.
- **Persisted notifications are not implemented.** Research §3.1 wants notifications persisted for when no UI is running.

## Notes for dependent tasks / INT
- **Desktop-only operations stay in the UI process** and are deliberately not in `API`: PickFolder, PickFiles, SuggestFolders, OpenFolder, OpenLogsFolder, OpenURL, OpenWebInterface, ShowWindow and Quit. They need the user's token or session. The web interface base URL is `Bootstrap.Settings.ServerURL`.
- **P0-27 (service):**
  - `NewHost(Options{DataDir: <ProgramData dir>, Log, UpdateRepo, AutoUpdates: true, RequestExit: <signal SCM stop>})`, then `Start`; call `Close` on stop.
  - Leave `AutoStart`/`AutoStartEnabled` nil in service mode.
- **P0-28 (IPC):**
  - Map wire methods 1:1 to `API` method names.
  - Marshal requests and responses as JSON; errors are `*Error` (use `AsError`).
  - Events: forward `Event` verbatim (`Data` is already JSON) from `Host.Subscribe(connCtx, kinds...)`.
  - Handshake on `ProtocolVersion`.
  - The machine-scope DPAPI key should be supplied as a custom `KeyStore` (or `FileKeyStore` once `platform.Protect` is mode-aware).
- **P0-29 (`NewApp(log, api agent.API)`):**
  - Event mapping from Wails:
    - `snapshot` → `KindSnapshot`;
    - `connected`/`revoked` → `KindConnection` (`state`);
    - `enrollment`, `settings` and `update` map 1:1;
    - toasts → `KindNotification`.
  - Two JSON shape changes for the frontend:
    - `Bootstrap.server_info` is now a typed object with the same keys as before, and Bootstrap gains `protocol_version`;
    - `ProbeServer`/`Connect` take request structs.
  - Per-user/dev mode:
    `h, _ := agent.NewHost(agent.Options{Log: log, AutoStart: platform.SetAutoStart, AutoStartEnabled: platform.AutoStartEnabled, UpdateRepo: UpdateRepo, AutoUpdates: true, RequestExit: func(){ wruntime.Quit(ctx) }})`, then `h.Start(ctx)`, `api := agent.NewInProc(h)`, and `h.Close()` on shutdown.
  - `Host` writes `settings.json`, `device.key` and `state.db` in `DataDir` with the same formats as today's `app.go`, so existing installs carry over. Until P0-29 lands, do not run `App` and `Host` against the same `DataDir` at once.
- **P0-38 (modules):** add module methods to `API` (reserved names above) and implement them on `Host` plus `InProc`. Add any new event kinds to `events.go`.
- No new dependencies.
