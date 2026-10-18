package modules

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	sdkmod "github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/modules"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

var (
	// ErrUnsupportedTask is returned by HandleTask for a kind it does not own.
	ErrUnsupportedTask = errors.New("modules: unsupported task kind")
	// ErrNoModeRequest means the id is not the pending request.
	ErrNoModeRequest = errors.New("modules: no such pending mode request")
	// ErrModeRequestSuperseded fails a request replaced by a newer one.
	ErrModeRequestSuperseded = errors.New("modules: mode request superseded by a newer request")
	// ErrModeRequestTimeout fails a request nobody confirmed in time.
	ErrModeRequestTimeout = errors.New("modules: mode request not confirmed in time")
)

const defaultModeRequestTimeout = 24 * time.Hour

// ModeRequest is a server mode_change_request waiting for a local Windows
// administrator (MASTER_PLAN §7.7, api-v1 §6). The UI shows it with Preview
// and answers through ConfirmModeRequest.
type ModeRequest struct {
	ID          string        `json:"id"`
	TaskID      int64         `json:"task_id"`
	CurrentMode sdkmod.Mode   `json:"current_mode"`
	TargetMode  sdkmod.Mode   `json:"target_mode"`
	RequestedBy string        `json:"requested_by"`
	ReceivedAt  int64         `json:"received_at"` // unix ms
	Preview     SwitchPreview `json:"preview"`
}

// ModeChangeResult is the device task result of a mode_change_request.
type ModeChangeResult struct {
	Approved    bool           `json:"approved"`
	FromMode    sdkmod.Mode    `json:"from_mode"`
	InstallMode sdkmod.Mode    `json:"install_mode"` // mode after the decision
	Applied     *SwitchPreview `json:"applied,omitempty"`
}

type decision struct {
	res ModeChangeResult
	err error
}

type pendingRequest struct {
	req  ModeRequest
	done chan decision // buffered(1)
}

func newRequestID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// HandleTask is the device-task handler for the kinds this package owns
// (mode_change_request). The task loop (P1-26) calls it with a claimed task
// and posts the result: err != nil → status "failed" with err's text,
// otherwise "done" with result as JSON.
//
// A mode change is never applied by the server alone: HandleTask publishes
// the request as ModulesState.PendingRequest and blocks until
// ConfirmModeRequest, a newer request, ModeRequestTimeout or ctx.
func (m *Manager) HandleTask(ctx context.Context, t wire.DeviceTask) (any, error) {
	if t.Kind != wire.TaskKindModeChangeRequest {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedTask, t.Kind)
	}
	v, err := wire.DecodeTaskPayload(t.Kind, t.Payload)
	if err != nil {
		return nil, err
	}
	p := v.(*wire.TaskModeChangeRequestPayload)
	target := sdkmod.Mode(p.TargetMode)
	if !target.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidMode, p.TargetMode)
	}
	cur := m.Mode()
	if cur == target {
		return ModeChangeResult{Approved: true, FromMode: cur, InstallMode: cur}, nil
	}
	preview, err := m.PreviewSwitch(ctx, target)
	if err != nil {
		return nil, err
	}
	id, err := newRequestID()
	if err != nil {
		return nil, err
	}
	pr := &pendingRequest{
		req: ModeRequest{ID: id, TaskID: t.ID, CurrentMode: cur, TargetMode: target,
			RequestedBy: p.RequestedBy, ReceivedAt: m.now(), Preview: preview},
		done: make(chan decision, 1),
	}
	m.mu.Lock()
	if old := m.pending; old != nil {
		old.done <- decision{err: ErrModeRequestSuperseded}
	}
	m.pending = pr
	m.mu.Unlock()
	m.log.Info("mode change requested by server; waiting for local administrator",
		"task", t.ID, "from", cur, "to", target, "requested_by", p.RequestedBy)
	m.notify()

	timeout := m.opts.ModeRequestTimeout
	if timeout <= 0 {
		timeout = defaultModeRequestTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case d := <-pr.done:
		return d.res, d.err
	case <-timer.C:
		err = ErrModeRequestTimeout
	case <-ctx.Done():
		err = ctx.Err()
	}
	if m.clearPending(pr) {
		m.notify()
	}
	// A decision may have raced with the timeout or ctx; prefer it.
	select {
	case d := <-pr.done:
		return d.res, d.err
	default:
	}
	return nil, err
}

// PendingModeRequest returns the request waiting for confirmation, or nil.
func (m *Manager) PendingModeRequest() *ModeRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		return nil
	}
	r := m.pending.req
	return &r
}

// ConfirmModeRequest answers the pending request. The caller (agent IPC)
// must already have verified that the user is a local Windows administrator
// (UAC); this package cannot. Approving applies SetMode and returns what was
// applied; denying leaves the mode unchanged. Either way the waiting
// HandleTask returns a "done" result for the server.
func (m *Manager) ConfirmModeRequest(ctx context.Context, id string, approve bool) (SwitchPreview, error) {
	m.mu.Lock()
	pr := m.pending
	if pr == nil || pr.req.ID != id {
		m.mu.Unlock()
		return SwitchPreview{}, ErrNoModeRequest
	}
	m.pending = nil
	m.mu.Unlock()

	from := m.Mode()
	if !approve {
		m.log.Info("mode change request denied by local administrator", "task", pr.req.TaskID, "to", pr.req.TargetMode)
		pr.done <- decision{res: ModeChangeResult{Approved: false, FromMode: from, InstallMode: from}}
		m.notify()
		return SwitchPreview{}, nil
	}
	applied, err := m.SetMode(ctx, pr.req.TargetMode) // notifies and reports
	if err != nil {
		pr.done <- decision{err: fmt.Errorf("modules: apply mode change: %w", err)}
		m.notify()
		return applied, err
	}
	m.log.Info("mode change request approved by local administrator", "task", pr.req.TaskID, "from", from, "to", pr.req.TargetMode)
	pr.done <- decision{res: ModeChangeResult{Approved: true, FromMode: from, InstallMode: m.Mode(), Applied: &applied}}
	return applied, nil
}

// clearPending removes pr if it is still the pending request.
func (m *Manager) clearPending(pr *pendingRequest) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending != pr {
		return false
	}
	m.pending = nil
	return true
}

// notify publishes the state without reporting (the pending request is
// local-only).
func (m *Manager) notify() {
	if m.opts.OnChange != nil {
		m.opts.OnChange(m.Snapshot())
	}
}
