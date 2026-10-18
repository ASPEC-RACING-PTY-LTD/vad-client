package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/agent"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/backup"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// uiError turns an agent error into one whose message is the JSON
// {"code","message"} of *agent.Error, so the frontend can tell
// "forbidden" (needs an administrator), "access_denied" and
// "updates_managed" (shown verbatim) and "sik_mismatch" apart. The
// frontend's apiError() parses it and falls back to the plain text.
func uiError(err error) error {
	if err == nil {
		return nil
	}
	ae := agent.AsError(err)
	b, jerr := json.Marshal(ae)
	if jerr != nil {
		return err
	}
	return errors.New(string(b))
}

// --- backup: Simple screens (agent) ---

func (a *App) KeyStatus() (agent.KeyStatus, error) {
	a.watchBackupEvents()
	ks, err := a.api.KeyStatus(a.bg)
	return ks, uiError(err)
}

func (a *App) ListBackupJobs() ([]agent.BackupJob, error) {
	jobs, err := a.api.ListBackupJobs(a.bg)
	return jobs, uiError(err)
}

// ListBackupRuns lists a job's latest runs, newest first.
func (a *App) ListBackupRuns(jobID int64) ([]agent.BackupRun, error) {
	runs, err := a.api.ListBackupRuns(a.bg, jobID)
	return runs, uiError(err)
}

func (a *App) ListIssues(q agent.IssueQuery) ([]agent.BackupIssue, error) {
	is, err := a.api.ListIssues(a.bg, q)
	return is, uiError(err)
}

// RunBackup starts a job now and returns the server run id.
func (a *App) RunBackup(jobID int64) (int64, error) {
	id, err := a.api.RunBackup(a.bg, jobID)
	return id, uiError(err)
}

// RetrySkipped re-runs a job to read the files its last run skipped.
func (a *App) RetrySkipped(jobID int64) (int64, error) {
	id, err := a.api.RetrySkipped(a.bg, jobID)
	return id, uiError(err)
}

// SkipIssues marks issues of a job skipped, by local issue ids and/or
// issue classes.
func (a *App) SkipIssues(jobID int64, issueIDs []int64, classes []string) error {
	return uiError(a.api.SkipIssues(a.bg, agent.SkipIssuesRequest{JobID: jobID, IssueIDs: issueIDs, Classes: classes}))
}

func (a *App) Readiness() (agent.BackupReadiness, error) {
	a.watchBackupEvents()
	r, err := a.api.Readiness(a.bg)
	return r, uiError(err)
}

// BackupJobDraft is what the Simple folder picker and schedule editor
// produce. Paths are token paths (backup-format §6), e.g.
// "{DOCUMENTS}/Projects".
type BackupJobDraft struct {
	ID       int64            `json:"id"`       // 0 = new job
	Revision int64            `json:"revision"` // the edited job's revision (If-Match)
	Name     string           `json:"name"`
	Paths    []string         `json:"paths"`
	Exclude  []string         `json:"exclude"`
	Schedule wire.JobSchedule `json:"schedule"`
	Enabled  bool             `json:"enabled"`
}

// jobSaver is the reserved agent method SaveBackupJob (api.go "reserved").
// It is used as soon as the agent implements it; until then saving answers
// not_implemented.
type jobSaver interface {
	SaveBackupJob(ctx context.Context, j wire.Job) (agent.BackupJob, error)
}

// SaveBackupJob validates a Simple folder job draft and hands it to the
// agent. Simple jobs are kind "folder", mode "full", VSS "auto" and read the
// calling user's own profile.
func (a *App) SaveBackupJob(d BackupJobDraft) (agent.BackupJob, error) {
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		return agent.BackupJob{}, uiError(&agent.Error{Code: agent.CodeInvalid, Message: "Give the backup a name."})
	}
	if len(d.Paths) == 0 {
		return agent.BackupJob{}, uiError(&agent.Error{Code: agent.CodeInvalid, Message: "Choose at least one folder."})
	}
	for _, p := range d.Paths {
		if tok, _, err := snapshot.SplitTokenPath(p); err != nil || !snapshot.ValidToken(tok) {
			return agent.BackupJob{}, uiError(&agent.Error{Code: agent.CodeInvalid, Message: "Not a valid folder: " + p})
		}
	}
	s, ok := a.api.(jobSaver)
	if !ok {
		return agent.BackupJob{}, uiError(&agent.Error{Code: agent.CodeNotImplemented,
			Message: "Saving backup jobs from this app is not available yet. Create the job in the VaultDock web interface."})
	}
	sid := ""
	if u, err := user.Current(); err == nil {
		sid = u.Uid
	}
	j := wire.Job{
		ID: d.ID, Revision: d.Revision, Name: d.Name, Kind: wire.JobKindFolder, Mode: wire.JobModeFull,
		Spec:     wire.JobSpec{Paths: d.Paths, Exclude: d.Exclude, UseVSS: wire.JobUseVSSAuto},
		Schedule: d.Schedule, Enabled: d.Enabled, ProfileSID: sid,
	}
	out, err := s.SaveBackupJob(a.bg, j)
	return out, uiError(err)
}

// KnownFolder is one path token of the signed-in user with its concrete
// folder, for the folder picker.
type KnownFolder struct {
	Token  string `json:"token"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

// userTokens resolves the signed-in user's path tokens with the same
// resolver the backup pipeline uses (folder redirection included).
func userTokens(ctx context.Context) (snapshot.Tokens, error) {
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return backup.OSFolders{}.Tokens(ctx, guard.Profile{SID: u.Uid, Root: home})
}

// KnownFolders lists the signed-in user's known folders (Documents,
// Pictures, …) for the folder picker. It runs in the UI process, as the
// user.
func (a *App) KnownFolders() ([]KnownFolder, error) {
	t, err := userTokens(a.bg)
	if err != nil {
		return nil, uiError(err)
	}
	out := make([]KnownFolder, 0, len(t))
	for tok, p := range t {
		st, serr := os.Stat(p)
		out = append(out, KnownFolder{Token: tok, Path: p, Exists: serr == nil && st.IsDir()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Token < out[j].Token })
	return out, nil
}

// EncodeFolder converts a folder chosen in the picker to its token path
// ({DOCUMENTS}/Projects). Folders outside the known folders are refused:
// Simple backups read only the user's own profile.
func (a *App) EncodeFolder(path string) (string, error) {
	t, err := userTokens(a.bg)
	if err != nil {
		return "", uiError(err)
	}
	tp, err := t.Encode(filepath.Clean(path))
	if err != nil {
		return "", uiError(&agent.Error{Code: agent.CodeInvalid,
			Message: "Choose a folder inside your user profile (for example Documents or Pictures)."})
	}
	return tp, nil
}

// ResolveFolder shows a token path as a concrete folder of this user.
func (a *App) ResolveFolder(tokenPath string) (string, error) {
	t, err := userTokens(a.bg)
	if err != nil {
		return "", uiError(err)
	}
	p, err := t.Resolve(tokenPath)
	return p, uiError(err)
}
