package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/modules"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/updater"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// InProc is an API client that calls an API implementation in the same
// process. It gives the caller the same semantics as a transport: every
// request and response is copied through JSON (so nothing is shared by
// reference and non-serialisable types fail loudly) and every error comes
// back as *Error. Used by tests and by the dev mode in which the UI and the
// agent run in one process.
type InProc struct{ api API }

var _ API = (*InProc)(nil)

// NewInProc wraps api (normally a *Host).
func NewInProc(api API) *InProc { return &InProc{api: api} }

// roundTrip copies v through JSON.
func roundTrip[T any](v T) (T, error) {
	var out T
	b, err := json.Marshal(v)
	if err != nil {
		return out, &Error{Code: CodeInternal, Message: fmt.Sprintf("agent: marshal %T: %v", v, err)}
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return out, &Error{Code: CodeInternal, Message: fmt.Sprintf("agent: unmarshal %T: %v", v, err)}
	}
	return out, nil
}

// call runs fn with a JSON copy of req and returns a JSON copy of the result.
func call[Req, Resp any](ctx context.Context, req Req, fn func(context.Context, Req) (Resp, error)) (Resp, error) {
	var zero Resp
	if err := ctx.Err(); err != nil {
		return zero, AsError(err)
	}
	in, err := roundTrip(req)
	if err != nil {
		return zero, err
	}
	out, err := fn(ctx, in)
	if err != nil {
		return zero, AsError(err)
	}
	return roundTrip(out)
}

type none struct{}

func call0[Resp any](ctx context.Context, fn func(context.Context) (Resp, error)) (Resp, error) {
	return call(ctx, none{}, func(ctx context.Context, _ none) (Resp, error) { return fn(ctx) })
}

func callErr[Req any](ctx context.Context, req Req, fn func(context.Context, Req) error) error {
	_, err := call(ctx, req, func(ctx context.Context, r Req) (none, error) { return none{}, fn(ctx, r) })
	return err
}

func (c *InProc) Bootstrap(ctx context.Context) (Bootstrap, error) {
	return call0(ctx, c.api.Bootstrap)
}

func (c *InProc) ProbeServer(ctx context.Context, req ProbeRequest) (ProbeResult, error) {
	return call(ctx, req, c.api.ProbeServer)
}

func (c *InProc) Connect(ctx context.Context, req ConnectRequest) (EnrollView, error) {
	return call(ctx, req, c.api.Connect)
}

func (c *InProc) CancelEnrollment(ctx context.Context) error {
	return callErr(ctx, none{}, func(ctx context.Context, _ none) error { return c.api.CancelEnrollment(ctx) })
}

func (c *InProc) Disconnect(ctx context.Context) error {
	return callErr(ctx, none{}, func(ctx context.Context, _ none) error { return c.api.Disconnect(ctx) })
}

func (c *InProc) ListConfigs(ctx context.Context) ([]engine.ConfigView, error) {
	return call0(ctx, c.api.ListConfigs)
}

func (c *InProc) SaveConfig(ctx context.Context, cfg config.SyncConfig) (config.SyncConfig, error) {
	return call(ctx, cfg, c.api.SaveConfig)
}

func (c *InProc) DeleteConfig(ctx context.Context, id string) error {
	return callErr(ctx, id, c.api.DeleteConfig)
}

func (c *InProc) SyncNow(ctx context.Context, id string) error {
	return callErr(ctx, id, c.api.SyncNow)
}

func (c *InProc) PreviewConfig(ctx context.Context, cfg config.SyncConfig) (PreviewResult, error) {
	return call(ctx, cfg, c.api.PreviewConfig)
}

func (c *InProc) Snapshot(ctx context.Context) (engine.Snapshot, error) {
	return call0(ctx, c.api.Snapshot)
}

func (c *InProc) SetPaused(ctx context.Context, paused bool) error {
	return callErr(ctx, paused, c.api.SetPaused)
}

func (c *InProc) ResetSyncData(ctx context.Context) (int64, error) {
	return call0(ctx, c.api.ResetSyncData)
}

func (c *InProc) RetryFailed(ctx context.Context, id int64) (int64, error) {
	return call(ctx, id, c.api.RetryFailed)
}

func (c *InProc) ListQueue(ctx context.Context, status string) ([]state.Item, error) {
	return call(ctx, status, c.api.ListQueue)
}

func (c *InProc) ListHistory(ctx context.Context, status string) ([]state.HistoryEntry, error) {
	return call(ctx, status, c.api.ListHistory)
}

func (c *InProc) ListAutomations(ctx context.Context) ([]engine.AutomationView, error) {
	return call0(ctx, c.api.ListAutomations)
}

func (c *InProc) RefreshAutomations(ctx context.Context) ([]engine.AutomationView, error) {
	return call0(ctx, c.api.RefreshAutomations)
}

func (c *InProc) GetSettings(ctx context.Context) (config.Settings, error) {
	return call0(ctx, c.api.GetSettings)
}

func (c *InProc) SaveSettings(ctx context.Context, s config.Settings) (config.Settings, error) {
	return call(ctx, s, c.api.SaveSettings)
}

func (c *InProc) UpdateStatus(ctx context.Context) (updater.Status, error) {
	return call0(ctx, c.api.UpdateStatus)
}

func (c *InProc) CheckForUpdates(ctx context.Context) (updater.Status, error) {
	return call0(ctx, c.api.CheckForUpdates)
}

func (c *InProc) SetWindowVisible(ctx context.Context, visible bool) error {
	return callErr(ctx, visible, c.api.SetWindowVisible)
}

func (c *InProc) InstallUpdate(ctx context.Context) error {
	return callErr(ctx, none{}, func(ctx context.Context, _ none) error { return c.api.InstallUpdate(ctx) })
}

func (c *InProc) KeyStatus(ctx context.Context) (KeyStatus, error) {
	return call0(ctx, c.api.KeyStatus)
}

func (c *InProc) ConfirmServerIdentity(ctx context.Context, fingerprint string) (KeyStatus, error) {
	return call(ctx, fingerprint, c.api.ConfirmServerIdentity)
}

func (c *InProc) InitRepository(ctx context.Context, req InitRepositoryRequest) (InitRepositoryResult, error) {
	return call(ctx, req, c.api.InitRepository)
}

func (c *InProc) ConfirmRecoveryKey(ctx context.Context, text string) error {
	return callErr(ctx, text, c.api.ConfirmRecoveryKey)
}

func (c *InProc) ListBackupJobs(ctx context.Context) ([]BackupJob, error) {
	return call0(ctx, c.api.ListBackupJobs)
}

func (c *InProc) ListBackupRuns(ctx context.Context, jobID int64) ([]BackupRun, error) {
	return call(ctx, jobID, c.api.ListBackupRuns)
}

func (c *InProc) ListIssues(ctx context.Context, q IssueQuery) ([]BackupIssue, error) {
	return call(ctx, q, c.api.ListIssues)
}

func (c *InProc) RunBackup(ctx context.Context, jobID int64) (int64, error) {
	return call(ctx, jobID, c.api.RunBackup)
}

func (c *InProc) RetrySkipped(ctx context.Context, jobID int64) (int64, error) {
	return call(ctx, jobID, c.api.RetrySkipped)
}

func (c *InProc) Readiness(ctx context.Context) (BackupReadiness, error) {
	return call0(ctx, c.api.Readiness)
}

func (c *InProc) SaveBackupJob(ctx context.Context, j wire.Job) (BackupJob, error) {
	return call(ctx, j, c.api.SaveBackupJob)
}

func (c *InProc) SkipIssues(ctx context.Context, req SkipIssuesRequest) error {
	return callErr(ctx, req, c.api.SkipIssues)
}

func (c *InProc) GiveConsent(ctx context.Context, proof []byte) error {
	return callErr(ctx, proof, c.api.GiveConsent)
}

func (c *InProc) GetMode(ctx context.Context) (modules.ModulesState, error) {
	return call0(ctx, c.api.GetMode)
}

// Subscribe passes through: events already carry JSON payloads.
func (c *InProc) Subscribe(ctx context.Context, kinds ...EventKind) (Subscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, AsError(err)
	}
	s, err := c.api.Subscribe(ctx, kinds...)
	if err != nil {
		return nil, AsError(err)
	}
	return s, nil
}
