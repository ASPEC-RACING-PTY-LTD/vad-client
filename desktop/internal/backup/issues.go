package backup

import (
	"context"
	"errors"
	"net"
	"unicode/utf8"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// maxIssueDetail bounds the detail text sent with an issue.
const maxIssueDetail = 1000

// addIssue records an issue in backup.db; the reporter uploads it.
func (r *Runner) addIssue(ctx context.Context, lr *Run, is Issue) {
	is.Detail = truncate(is.Detail, maxIssueDetail)
	if err := r.Store.AddIssue(context.WithoutCancel(ctx), lr.JobID, lr.ClientRunUUID, is); err != nil {
		r.log().Error("backup: record issue", "run", lr.ClientRunUUID, "class", is.Class, "err", err)
	}
}

// flushIssues uploads a run's unreported issues in batches of
// wire.IssueBatchMax (local classes mapped per api-v1 §14 Rev 1.1b) and
// marks each batch reported once the server accepted it.
func (r *Runner) flushIssues(ctx context.Context, runUUID string, runID int64) error {
	if runID == 0 {
		return nil
	}
	for {
		pend, err := r.Store.PendingIssues(ctx, runUUID, wire.IssueBatchMax)
		if err != nil || len(pend) == 0 {
			return err
		}
		reps := make([]wire.IssueReport, len(pend))
		ids := make([]int64, len(pend))
		for i, p := range pend {
			reps[i], ids[i] = p.Report(), p.ID
		}
		if err := r.API.ReportIssues(ctx, runID, reps); err != nil {
			return err
		}
		if err := r.Store.MarkIssuesReported(ctx, ids); err != nil {
			return err
		}
		if len(pend) < wire.IssueBatchMax {
			return nil
		}
	}
}

// errorIssue turns a run failure into a run-level issue (no path).
func errorIssue(err error) Issue {
	class := wire.IssueClassOther
	var ae *client.APIError
	var ne net.Error
	switch {
	case errors.As(err, &ae):
		switch ae.Code {
		case wire.CodeModuleUnavailable:
			class = wire.IssueClassModuleUnavailable
		case wire.CodeProfileNotAuthorised, wire.CodeProfileBlacklisted:
			class = wire.IssueClassProfileUnauthorised
		default:
			class = wire.IssueClassServer
		}
	case errors.As(err, &ne):
		class = wire.IssueClassNetwork
	}
	return Issue{Class: class, Severity: wire.IssueSeverityError, Detail: err.Error(), Retryable: true}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// --- Retry Skipped ---

// runJob returns the job of a server run id from the local runs.
func (r *Runner) runJob(ctx context.Context, runID int64) (int64, error) {
	lr, err := r.Store.RunByServerID(ctx, runID)
	if errors.Is(err, ErrNotFound) {
		return 0, ErrUnknownRun
	}
	if err != nil {
		return 0, err
	}
	return lr.JobID, nil
}

// RetrySkipped handles a retry_skipped task (rev 1.0 §6): it starts a run
// of the source run's job with trigger retry_skipped and returns the new
// server run id. The server links the source run's open, retryable,
// skipped issues (limited to the task's issue_ids) to that run and
// resolves each one unless the run reports the same path and class again.
//
// The run is a normal incremental run of the job: files skipped earlier
// have no file-cache entry, so they are the ones read again, and the
// snapshot stays complete. Restricting the walk to the skipped paths would
// prune the job's file cache and produce a partial snapshot as the next
// parent (see the P1-26 report).
func (r *Runner) RetrySkipped(ctx context.Context, p wire.TaskRetrySkippedPayload) (int64, error) {
	jobID, err := r.runJob(ctx, p.RunID)
	if err != nil {
		return 0, err
	}
	return r.Start(ctx, jobID, wire.RunTriggerRetrySkipped)
}
