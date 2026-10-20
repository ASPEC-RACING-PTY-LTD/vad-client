package ipc

import (
	"context"
	"encoding/json"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/agent"
)

// Special methods handled by the server itself rather than the handler
// table.
const (
	methodSubscribe   = "Subscribe"
	methodUnsubscribe = "Unsubscribe"
)

// handler runs one API method with JSON params. A nil result means the
// method returns only an error.
type handler func(ctx context.Context, api agent.API, params json.RawMessage) (any, error)

func decodeParams[P any](raw json.RawMessage) (P, error) {
	var p P
	if len(raw) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, &agent.Error{Code: agent.CodeInvalid, Message: "ipc: bad params: " + err.Error()}
	}
	return p, nil
}

func m0[R any](f func(agent.API, context.Context) (R, error)) handler {
	return func(ctx context.Context, api agent.API, _ json.RawMessage) (any, error) {
		r, err := f(api, ctx)
		if err != nil {
			return nil, err
		}
		return r, nil
	}
}

func m1[P, R any](f func(agent.API, context.Context, P) (R, error)) handler {
	return func(ctx context.Context, api agent.API, raw json.RawMessage) (any, error) {
		p, err := decodeParams[P](raw)
		if err != nil {
			return nil, err
		}
		r, err := f(api, ctx, p)
		if err != nil {
			return nil, err
		}
		return r, nil
	}
}

func e0(f func(agent.API, context.Context) error) handler {
	return func(ctx context.Context, api agent.API, _ json.RawMessage) (any, error) {
		return nil, f(api, ctx)
	}
}

func e1[P any](f func(agent.API, context.Context, P) error) handler {
	return func(ctx context.Context, api agent.API, raw json.RawMessage) (any, error) {
		p, err := decodeParams[P](raw)
		if err != nil {
			return nil, err
		}
		return nil, f(api, ctx, p)
	}
}

// handlers maps wire method names (identical to agent.API method names) to
// their implementation.
var handlers = map[string]handler{
	"Bootstrap":          m0(agent.API.Bootstrap),
	"ProbeServer":        m1(agent.API.ProbeServer),
	"Connect":            m1(agent.API.Connect),
	"CancelEnrollment":   e0(agent.API.CancelEnrollment),
	"Disconnect":         e0(agent.API.Disconnect),
	"ListConfigs":        m0(agent.API.ListConfigs),
	"SaveConfig":         m1(agent.API.SaveConfig),
	"DeleteConfig":       e1(agent.API.DeleteConfig),
	"SyncNow":            e1(agent.API.SyncNow),
	"PreviewConfig":      m1(agent.API.PreviewConfig),
	"Snapshot":           m0(agent.API.Snapshot),
	"SetPaused":          e1(agent.API.SetPaused),
	"RetryFailed":        m1(agent.API.RetryFailed),
	"ResetSyncData":      m0(agent.API.ResetSyncData),
	"ListQueue":          m1(agent.API.ListQueue),
	"ListHistory":        m1(agent.API.ListHistory),
	"ListAutomations":    m0(agent.API.ListAutomations),
	"RefreshAutomations": m0(agent.API.RefreshAutomations),
	"GetSettings":        m0(agent.API.GetSettings),
	"SaveSettings":       m1(agent.API.SaveSettings),
	"UpdateStatus":       m0(agent.API.UpdateStatus),
	"CheckForUpdates":    m0(agent.API.CheckForUpdates),
	"InstallUpdate":      e0(agent.API.InstallUpdate),
	"SetWindowVisible":   e1(agent.API.SetWindowVisible),

	"KeyStatus":             m0(agent.API.KeyStatus),
	"ConfirmServerIdentity": m1(agent.API.ConfirmServerIdentity),
	"InitRepository":        m1(agent.API.InitRepository),
	"ConfirmRecoveryKey":    e1(agent.API.ConfirmRecoveryKey),
	"ListBackupJobs":        m0(agent.API.ListBackupJobs),
	"ListBackupRuns":        m1(agent.API.ListBackupRuns),
	"ListIssues":            m1(agent.API.ListIssues),
	"RunBackup":             m1(agent.API.RunBackup),
	"RetrySkipped":          m1(agent.API.RetrySkipped),
	"Readiness":             m0(agent.API.Readiness),
	"SaveBackupJob":         m1(agent.API.SaveBackupJob),
	"SkipIssues":            e1(agent.API.SkipIssues),
	"GiveConsent":           e1(agent.API.GiveConsent),
	"GetMode":               m0(agent.API.GetMode),
}
