package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// Task loop defaults.
const (
	// DefaultTaskTimeout bounds one task handler; it stays under the
	// server's 10-minute claim lease so a result is posted before the task
	// is requeued.
	DefaultTaskTimeout = 8 * time.Minute
	DefaultMinBackoff  = time.Second
	DefaultMaxBackoff  = time.Minute
)

// TaskAPI is the device task surface; *client.Client satisfies it.
type TaskAPI interface {
	PollTasks(ctx context.Context, wait time.Duration) ([]wire.DeviceTask, error)
	TaskResult(ctx context.Context, taskID int64, status string, result json.RawMessage) error
	FinishKeyShare(ctx context.Context, taskID int64, res client.KeyTaskResult) error
	FinishRewrap(ctx context.Context, taskID int64, res client.KeyTaskResult) error
}

var _ TaskAPI = (*client.Client)(nil)

// KeyTasks handles key_share and rewrap; *keystore.Keystore satisfies it
// (its result is a client.KeyTaskResult).
type KeyTasks interface {
	HandleTask(ctx context.Context, t wire.DeviceTask) (any, error)
}

// BackupTasks starts runs for backup_now and retry_skipped; *Runner
// satisfies it.
type BackupTasks interface {
	Start(ctx context.Context, jobID int64, trigger string) (int64, error)
	RetrySkipped(ctx context.Context, p wire.TaskRetrySkippedPayload) (int64, error)
}

var _ BackupTasks = (*Runner)(nil)

// TaskHandler handles another task kind; its result is sent as the task
// result (done), its error fails the task.
type TaskHandler func(ctx context.Context, t wire.DeviceTask) (result any, err error)

// TaskLoop long-polls device tasks (api-v1 §6; a returned task is claimed
// under a lease) and dispatches them: backup_now and retry_skipped to the
// runner, key_share and rewrap to the keystore, other kinds to Handlers.
// A kind nobody handles is finished as failed.
type TaskLoop struct {
	API      TaskAPI
	Backups  BackupTasks
	Keys     KeyTasks
	Handlers map[string]TaskHandler // e.g. restore, profile_consent_request (other packages)

	Wait        time.Duration // long-poll wait; 0 = client.MaxTaskWait
	TaskTimeout time.Duration // 0 = DefaultTaskTimeout
	MinBackoff  time.Duration // poll error backoff; 0 = DefaultMinBackoff
	MaxBackoff  time.Duration // 0 = DefaultMaxBackoff
	Log         *slog.Logger
}

func (l *TaskLoop) log() *slog.Logger {
	if l.Log != nil {
		return l.Log
	}
	return slog.Default()
}

// Run polls until ctx is cancelled and returns ctx.Err(). Poll errors back
// off exponentially (honouring Retry-After). A task interrupted by the
// shutdown is left claimed; the server requeues it when its lease ends.
func (l *TaskLoop) Run(ctx context.Context) error {
	wait := l.Wait
	if wait <= 0 {
		wait = client.MaxTaskWait
	}
	minB, maxB := l.MinBackoff, l.MaxBackoff
	if minB <= 0 {
		minB = DefaultMinBackoff
	}
	if maxB <= 0 {
		maxB = DefaultMaxBackoff
	}
	backoff := minB
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tasks, err := l.API.PollTasks(ctx, wait)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			d := backoff
			var ae *client.APIError
			if errors.As(err, &ae) && ae.RetryAfter > d {
				d = min(ae.RetryAfter, maxB)
			}
			l.log().Warn("backup: task poll failed", "retry_in", d, "err", err)
			if err := sleep(ctx, d); err != nil {
				return err
			}
			backoff = min(backoff*2, maxB)
			continue
		}
		backoff = minB
		for _, t := range tasks {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			l.Handle(ctx, t)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Handle runs one claimed task and posts its result.
func (l *TaskLoop) Handle(ctx context.Context, t wire.DeviceTask) {
	log := l.log().With("task", t.ID, "kind", t.Kind)
	to := l.TaskTimeout
	if to <= 0 {
		to = DefaultTaskTimeout
	}
	hctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	var err error
	switch t.Kind {
	case wire.TaskKindKeyShare, wire.TaskKindRewrap:
		err = l.keyTask(hctx, ctx, t)
	case wire.TaskKindBackupNow, wire.TaskKindRetrySkipped:
		res, herr := l.backupTask(ctx, t)
		err = l.finish(ctx, t.ID, res, herr)
	default:
		if h := l.Handlers[t.Kind]; h != nil {
			res, herr := h(hctx, t)
			err = l.finish(ctx, t.ID, res, herr)
		} else {
			err = l.finish(ctx, t.ID, nil, fmt.Errorf("unsupported task kind %q", t.Kind))
		}
	}
	if err != nil && ctx.Err() == nil {
		log.Warn("backup: task result not posted", "err", err)
	}
}

// backupTask starts the run and reports as soon as the server run exists;
// the run continues in the background (a long backup must not hold the
// task lease).
func (l *TaskLoop) backupTask(ctx context.Context, t wire.DeviceTask) (any, error) {
	if l.Backups == nil {
		return nil, errors.New("backups are not available on this device")
	}
	p, err := wire.DecodeTaskPayload(t.Kind, t.Payload)
	if err != nil {
		return nil, err
	}
	var jobID, runID int64
	switch p := p.(type) {
	case *wire.TaskBackupNowPayload:
		jobID = p.JobID
		runID, err = l.Backups.Start(ctx, p.JobID, wire.RunTriggerTask)
	case *wire.TaskRetrySkippedPayload:
		runID, err = l.Backups.RetrySkipped(ctx, *p)
	}
	if errors.Is(err, ErrRunBusy) {
		return taskRunResult{JobID: jobID, AlreadyRunning: true}, nil
	}
	if err != nil {
		return nil, err
	}
	return taskRunResult{JobID: jobID, RunID: runID}, nil
}

// taskRunResult is the result of backup_now and retry_skipped (the
// contract leaves it open).
type taskRunResult struct {
	JobID          int64 `json:"job_id,omitempty"`
	RunID          int64 `json:"run_id,omitempty"`
	AlreadyRunning bool  `json:"already_running,omitempty"`
}

// taskError is the result of a failed task.
type taskError struct {
	Error string `json:"error"`
}

// keyTask runs a key task in hctx and posts its client.KeyTaskResult.
func (l *TaskLoop) keyTask(hctx, ctx context.Context, t wire.DeviceTask) error {
	var kr client.KeyTaskResult
	if l.Keys == nil {
		kr.Error = "the keystore is not available on this device"
	} else {
		res, err := l.Keys.HandleTask(hctx, t)
		if r, ok := res.(client.KeyTaskResult); ok {
			kr = r
		}
		if err != nil && kr.Error == "" {
			kr.Error = err.Error()
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	finish := l.API.FinishKeyShare
	if t.Kind == wire.TaskKindRewrap {
		finish = l.API.FinishRewrap
	}
	return postResult(ctx, func() error { return finish(ctx, t.ID, kr) })
}

// finish posts done with res, or failed with herr.
func (l *TaskLoop) finish(ctx context.Context, id int64, res any, herr error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	status := wire.TaskStatusDone
	if herr != nil {
		status, res = wire.TaskStatusFailed, taskError{Error: truncate(herr.Error(), maxIssueDetail)}
	}
	var raw json.RawMessage
	if res != nil {
		b, err := json.Marshal(res)
		if err != nil {
			return err
		}
		raw = b
	}
	return postResult(ctx, func() error { return l.API.TaskResult(ctx, id, status, raw) })
}

// postResult retries transient failures; 409 task_not_claimable (the
// lease ended) is final.
func postResult(ctx context.Context, fn func() error) error {
	return retry(ctx, fn)
}
