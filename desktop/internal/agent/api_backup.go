package agent

import "github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"

// Backup error codes (P1-26b).
const (
	// CodeSIKMismatch: the server identity differs from the pinned one;
	// backups are stopped until ConfirmServerIdentity.
	CodeSIKMismatch = wire.CodeSIKMismatch
	// CodeIdentityUnpinned: no server identity is pinned yet.
	CodeIdentityUnpinned = "identity_unpinned"
	// CodeConflict: the operation conflicts with the current state (a run
	// already in progress, a repository that already exists).
	CodeConflict = "conflict"
)

// KeyStatus is the server identity and repository key state for first run.
// It never contains secrets.
type KeyStatus struct {
	// Available is false when this agent has no backup stack (per-user
	// host) or it could not open its keystore or backup.db (Error says why).
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
	Connected bool   `json:"connected"`
	// PinState is PinStateUnknown, PinStateUnpinned, PinStateVerified or
	// PinStateMismatch.
	PinState string `json:"pin_state"`
	// PinnedFingerprint is the trusted SIK fingerprint ("" = none);
	// ServerFingerprint is the server's current one, as last seen. The UI
	// shows both when they differ and asks the administrator to compare.
	PinnedFingerprint string `json:"pinned_fingerprint"`
	ServerFingerprint string `json:"server_fingerprint"`
	// RepoState is RepoStateUnknown, RepoStateNone, RepoStateInitialised or
	// RepoStateRemote.
	RepoState         string `json:"repo_state"`
	RepoID            int64  `json:"repo_id,omitempty"`
	RepoUUID          string `json:"repo_uuid,omitempty"`
	EscrowMode        string `json:"escrow_mode,omitempty"`
	RecoveryConfirmed bool   `json:"recovery_confirmed"`
}

// InitRepositoryRequest is the input of InitRepository. EscrowMode is
// "full" (default) or "metadata".
type InitRepositoryRequest struct {
	EscrowMode string `json:"escrow_mode"`
}

// InitRepositoryResult is returned once by InitRepository. RecoveryKey is
// the VDRK1- recovery key text: the UI shows it once, then calls
// ConfirmRecoveryKey. It is never stored or logged by the agent.
type InitRepositoryResult struct {
	RepoID      int64  `json:"repo_id"`
	RepoUUID    string `json:"repo_uuid"`
	EscrowMode  string `json:"escrow_mode"`
	RecoveryKey string `json:"recovery_key"`
}

// BackupJob is a mirrored server job plus local state.
type BackupJob struct {
	wire.Job
	EffectiveState  string     `json:"effective_state"` // wire.JobState*; suspended_by_mode wins
	SuspendedByMode bool       `json:"suspended_by_mode"`
	Running         bool       `json:"running"`
	LastRun         *BackupRun `json:"last_run"`
}

// BackupRun is one local run. Status is a wire.RunStatus* value or
// "skipped" (the profile's user must sign in first).
type BackupRun struct {
	ClientRunUUID string           `json:"client_run_uuid"`
	JobID         int64            `json:"job_id"`
	RunID         int64            `json:"run_id"` // server run id; 0 until registered
	Trigger       string           `json:"trigger"`
	Status        string           `json:"status"`
	Stats         wire.RunProgress `json:"stats"`
	SnapshotID    int64            `json:"snapshot_id"`
	Error         string           `json:"error,omitempty"`
	StartedAt     int64            `json:"started_at"`  // unix ms
	FinishedAt    int64            `json:"finished_at"` // unix ms; 0 = not finished
}

// IssueQuery selects issues: RunUUID wins; else JobID's latest run; else
// the latest run of every visible job.
type IssueQuery struct {
	JobID   int64  `json:"job_id,omitempty"`
	RunUUID string `json:"run_uuid,omitempty"`
}

// SkipIssuesRequest selects the issues SkipIssues marks skipped: local
// issue ids (BackupIssue.ID) and/or wire issue classes. Ids must belong to
// JobID's runs; classes apply to the job's latest registered run.
type SkipIssuesRequest struct {
	JobID    int64    `json:"job_id"`
	IssueIDs []int64  `json:"issue_ids,omitempty"`
	Classes  []string `json:"classes,omitempty"`
}

// BackupIssue is one recorded skip or warning of a run.
type BackupIssue struct {
	ID        int64  `json:"id"`
	JobID     int64  `json:"job_id"`
	RunUUID   string `json:"run_uuid"`
	Path      string `json:"path"`
	Class     string `json:"class"` // wire.IssueClass* or efs_needs_logon / profile_needs_logon
	Severity  string `json:"severity"`
	Detail    string `json:"detail"`
	Retryable bool   `json:"retryable"`
	Count     int64  `json:"count"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
	Reported  bool   `json:"reported"`
}

// ReadinessItem is one readiness finding. Severity is "error" (backups
// cannot run) or "warning".
type ReadinessItem struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// Readiness item codes.
const (
	ReadyUnavailable          = "unavailable"
	ReadyNotConnected         = "not_connected"
	ReadyIdentityUnpinned     = "identity_unpinned"
	ReadyIdentityMismatch     = "identity_mismatch"
	ReadyIdentityUnverified   = "identity_unverified"
	ReadyRepoNotInitialised   = "repo_not_initialised"
	ReadyRecoveryNotConfirmed = "recovery_not_confirmed"
	ReadyNoJobs               = "no_jobs"
	ReadyLastRunFailed        = "last_run_failed"
	ReadyOpenIssues           = "open_issues"
)

// BackupReadiness summarises whether this device is backing up.
type BackupReadiness struct {
	Ready         bool            `json:"ready"` // no error items
	Keys          KeyStatus       `json:"keys"`
	Jobs          int             `json:"jobs"`
	RunnableJobs  int             `json:"runnable_jobs"`
	Running       int             `json:"running"`
	LastRun       *BackupRun      `json:"last_run"`
	LastSuccessAt int64           `json:"last_success_at"` // unix ms of the newest completed/partial run's finish
	OpenIssues    int             `json:"open_issues"`     // issues of the visible jobs' latest runs
	Items         []ReadinessItem `json:"items"`
}
