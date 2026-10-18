// Package agent is the transport-independent core of the VaultDock agent:
// the API the UI calls (API), the Host that implements it by owning the
// sync engine, state store, settings, device identity and updater, an event
// bus for server-push notifications, and an in-process client.
//
// The named-pipe transport (internal/ipc) and the Windows service wrapper
// (internal/service) are built on top of this package; nothing here depends
// on Wails, Windows services or pipes.
package agent

import (
	"context"
	"errors"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/modules"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/updater"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// ProtocolVersion is the version of the API surface below. Transports send
// it in their handshake; incompatible changes bump it.
const ProtocolVersion = 1

// API is every operation the UI needs from the agent. All request and
// response types are JSON-serialisable so a transport can marshal them.
//
// Desktop-only operations (folder/file pickers, opening folders or URLs,
// showing the window, quitting the UI, SuggestFolders, the logs folder) are
// not part of the API: they run in the user's UI process, under the user's
// token. Errors returned across a transport are *Error values.
type API interface {
	// --- lifecycle / connection ---

	// Bootstrap returns everything the UI needs for its first render.
	Bootstrap(ctx context.Context) (Bootstrap, error)
	// ProbeServer checks a server URL; Pin is a certificate fingerprint the
	// user explicitly chose to trust (empty for normal CA validation).
	ProbeServer(ctx context.Context, req ProbeRequest) (ProbeResult, error)
	// Connect verifies credentials and starts device authorisation. The
	// password is sent once over TLS and never stored or logged.
	Connect(ctx context.Context, req ConnectRequest) (EnrollView, error)
	CancelEnrollment(ctx context.Context) error
	// Disconnect removes this device's credentials. Sync configurations are
	// kept; nothing on the server is deleted.
	Disconnect(ctx context.Context) error

	// --- sync configurations ---

	ListConfigs(ctx context.Context) ([]engine.ConfigView, error)
	SaveConfig(ctx context.Context, c config.SyncConfig) (config.SyncConfig, error)
	DeleteConfig(ctx context.Context, id string) error
	// SyncNow scans one configuration, or every enabled one when id is "".
	SyncNow(ctx context.Context, id string) error
	// PreviewConfig dry-runs a saved or draft configuration.
	PreviewConfig(ctx context.Context, c config.SyncConfig) (PreviewResult, error)

	// --- transfers ---

	Snapshot(ctx context.Context) (engine.Snapshot, error)
	SetPaused(ctx context.Context, paused bool) error
	// RetryFailed re-queues one failed item, or all of them when id is 0.
	RetryFailed(ctx context.Context, id int64) (int64, error)
	// ListQueue lists queue items; status is "", pending, active or failed.
	ListQueue(ctx context.Context, status string) ([]state.Item, error)
	// ListHistory lists finished transfers; status "" means all.
	ListHistory(ctx context.Context, status string) ([]state.HistoryEntry, error)

	// --- automations ---

	ListAutomations(ctx context.Context) ([]engine.AutomationView, error)
	RefreshAutomations(ctx context.Context) ([]engine.AutomationView, error)

	// --- settings ---

	GetSettings(ctx context.Context) (config.Settings, error)
	// SaveSettings stores UI-editable settings; identity fields (server,
	// pin, device id, username, device name, key fingerprint) are ignored.
	SaveSettings(ctx context.Context, s config.Settings) (config.Settings, error)

	// --- updates ---

	UpdateStatus(ctx context.Context) (updater.Status, error)
	// CheckForUpdates checks now and downloads a newer version in the
	// background so it is ready to install.
	CheckForUpdates(ctx context.Context) (updater.Status, error)
	// InstallUpdate installs a downloaded (or downloads then installs) update;
	// the agent asks its owner to exit so the installer can run.
	InstallUpdate(ctx context.Context) error

	// --- events ---

	// Subscribe returns a stream of events of the given kinds (all kinds
	// when none are given). The caller must Close the subscription.
	Subscribe(ctx context.Context, kinds ...EventKind) (Subscription, error)

	// --- backup: first run and Simple screens (P1-26b) ---

	// KeyStatus returns the server identity (SIK pin) and repository key
	// state. It never contains secrets.
	KeyStatus(ctx context.Context) (KeyStatus, error)
	// ConfirmServerIdentity pins (or re-pins after a mismatch) the server's
	// current identity once the administrator compared fingerprint.
	ConfirmServerIdentity(ctx context.Context, fingerprint string) (KeyStatus, error)
	// InitRepository sets up the account's repository as the first device
	// and returns the recovery key text once.
	InitRepository(ctx context.Context, req InitRepositoryRequest) (InitRepositoryResult, error)
	// ConfirmRecoveryKey checks the recovery key the user kept.
	ConfirmRecoveryKey(ctx context.Context, text string) error
	ListBackupJobs(ctx context.Context) ([]BackupJob, error)
	// ListBackupRuns lists a job's latest runs, newest first.
	ListBackupRuns(ctx context.Context, jobID int64) ([]BackupRun, error)
	ListIssues(ctx context.Context, q IssueQuery) ([]BackupIssue, error)
	// RunBackup starts a job now and returns the server run id.
	RunBackup(ctx context.Context, jobID int64) (int64, error)
	// RetrySkipped re-runs a job to read the files its last run skipped.
	RetrySkipped(ctx context.Context, jobID int64) (int64, error)
	Readiness(ctx context.Context) (BackupReadiness, error)
	// SaveBackupJob creates (ID 0) or updates (If-Match Revision) a backup
	// job on the server for the caller's own profile, then re-syncs jobs.
	SaveBackupJob(ctx context.Context, j wire.Job) (BackupJob, error)
	// SkipIssues marks issues of a job's latest registered run skipped on
	// the server, by local issue ids or by wire issue class.
	SkipIssues(ctx context.Context, req SkipIssuesRequest) error
	// GiveConsent records the caller's consent proof (profiles §6B) and
	// reports profiles to the server.
	GiveConsent(ctx context.Context, proof []byte) error
	// GetMode returns the install mode and module states.
	GetMode(ctx context.Context) (modules.ModulesState, error)

	// --- reserved (not implemented yet; added by later tasks) ---
	//
	// Backup (DSK-C): DeleteBackupJob, CancelBackup, ListSnapshots,
	//   ResolveIssue.
	// Restore (DSK-G): PlanRestore, StartRestore, CancelRestore.
	// Modules (DSK-MOD): SetMode, ListModules, SetModule.
	// Profiles (DSK-P): ListProfiles, RevokeConsent.
	//
	// New methods are appended to this interface; existing ones keep their
	// signatures within a ProtocolVersion. Hosts return ErrNotImplemented
	// for a reserved operation until it lands.
}

// Subscription is a live event stream from Subscribe.
type Subscription interface {
	// C delivers events; it is closed when the subscription or bus closes.
	C() <-chan Event
	// Dropped counts events discarded because this subscriber fell behind.
	Dropped() uint64
	Close()
}

// --- request / response types ---

// Bootstrap is the UI's initial state.
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

// ServerInfo summarises the connected server and account.
type ServerInfo struct {
	Version  string `json:"version"`
	SiteName string `json:"site_name"`
	Quota    int64  `json:"quota"`
	Used     int64  `json:"used"`
}

// ProbeRequest is the input of ProbeServer.
type ProbeRequest struct {
	URL string `json:"url"`
	Pin string `json:"pin,omitempty"`
}

// ProbeResult describes a probed server.
type ProbeResult struct {
	OK          bool           `json:"ok"`
	Info        map[string]any `json:"info,omitempty"`
	Untrusted   bool           `json:"untrusted"`
	Fingerprint string         `json:"fingerprint,omitempty"`
	Subject     string         `json:"subject,omitempty"`
	Reason      string         `json:"reason,omitempty"`
	URL         string         `json:"url"`
}

// ConnectRequest is the input of Connect. Password is never logged.
type ConnectRequest struct {
	URL      string `json:"url"`
	Login    string `json:"login"`
	Password string `json:"password"`
	Pin      string `json:"pin,omitempty"`
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

// PreviewResult is the outcome of PreviewConfig.
type PreviewResult struct {
	Entries []engine.PreviewEntry `json:"entries"`
	Total   int                   `json:"total"`
}

// --- errors ---

// Error codes carried across transports.
const (
	CodeInvalid        = "invalid"
	CodeNotFound       = "not_found"
	CodeUnavailable    = "unavailable"
	CodeNotImplemented = "not_implemented"
	CodeCanceled       = "canceled"
	CodeInternal       = "internal"
)

// Error is the transport-safe error form: a stable code plus a message
// suitable for the UI.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// Is matches another *Error with the same code, so errors.Is(err,
// ErrNotImplemented) works after a transport round trip.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

var (
	ErrNotImplemented = &Error{Code: CodeNotImplemented, Message: "not implemented"}
	ErrClosed         = &Error{Code: CodeUnavailable, Message: "the agent is shutting down"}
)

func invalidf(msg string) error { return &Error{Code: CodeInvalid, Message: msg} }

// AsError converts any error to *Error; nil stays nil.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return &Error{Code: CodeCanceled, Message: err.Error()}
	case errors.Is(err, state.ErrNotFound):
		return &Error{Code: CodeNotFound, Message: err.Error()}
	}
	return &Error{Code: CodeInternal, Message: err.Error()}
}
