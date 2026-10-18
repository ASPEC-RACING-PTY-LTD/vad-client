package agent

import (
	"context"
	"errors"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/backup"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/ipc/caller"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/modules"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/profiles"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// Limits of SaveBackupJob and SkipIssues (the server enforces its own).
const (
	maxJobNameLen   = 200
	maxJobPaths     = 1000
	maxSkipIDs      = 1000
	maxSkipClasses  = 20
	issuePageLimit  = 500
	maxIssuePages   = 40
	jobKindsAllowed = "folder profile" // Simple screens; other kinds come from the web UI
)

// folderTokens resolves a profile's known-folder tokens; tests replace it.
var folderTokens = func(ctx context.Context, p guard.Profile) (snapshot.Tokens, error) {
	return backup.OSFolders{}.Tokens(ctx, p)
}

// apiErr maps a server error of a job or issue call to *Error.
func apiErr(err error) error {
	var api *client.APIError
	if !errors.As(err, &api) {
		return backupErr(err)
	}
	switch {
	case api.Code == "conflict" || api.Code == "plan_stale":
		return &Error{Code: CodeConflict, Message: "this backup was changed elsewhere; reload and try again"}
	case api.Status == 404:
		return &Error{Code: CodeNotFound, Message: api.Message}
	case api.Status == 400 || api.Status == 409 || api.Status == 422 || api.Status == 423:
		return &Error{Code: CodeInvalid, Message: api.Message}
	case api.Status == 403:
		return &Error{Code: CodeForbidden, Message: api.Message}
	}
	return &Error{Code: CodeUnavailable, Message: api.Message}
}

// jobProfileSID returns the profile a saved job runs as. A pipe user always
// gets their own SID (never a value from the request); trusted callers
// (in-process UI, SYSTEM) keep the requested one.
func jobProfileSID(ctx context.Context, requested string) string {
	if c, ok := caller.From(ctx); ok && !IsSystem(c) {
		return c.SID
	}
	return requested
}

// validateJob checks the fields SaveBackupJob sends to the server.
func validateJob(j *wire.Job) error {
	j.Name = strings.TrimSpace(j.Name)
	switch {
	case j.Name == "" || utf8.RuneCountInString(j.Name) > maxJobNameLen:
		return invalidf("the backup needs a name of at most 200 characters")
	case !slices.Contains(strings.Fields(jobKindsAllowed), j.Kind):
		return invalidf("unsupported backup kind " + strconv.Quote(j.Kind))
	case j.Mode != "" && j.Mode != wire.JobModeFull && j.Mode != wire.JobModeDataOnly:
		return invalidf("unsupported backup mode " + strconv.Quote(j.Mode))
	case len(j.Spec.Paths) == 0 || len(j.Spec.Paths) > maxJobPaths:
		return invalidf("choose between 1 and 1000 folders")
	}
	for _, p := range j.Spec.Paths {
		if strings.HasPrefix(p, "{") {
			if _, _, err := snapshot.SplitTokenPath(p); err != nil {
				return invalidf("invalid folder " + strconv.Quote(p))
			}
			continue
		}
		if !filepath.IsAbs(p) {
			return invalidf("folders must be absolute paths or known folders: " + strconv.Quote(p))
		}
	}
	if j.Mode == "" {
		j.Mode = wire.JobModeFull
	}
	return nil
}

// checkJobPaths verifies a pipe caller may read every folder of the job:
// known-folder tokens are resolved for the caller's profile, then guard and
// a read probe run as the caller (INT-FIX-A).
func (h *Host) checkJobPaths(ctx context.Context, paths []string) error {
	pa, err := h.callerAccess(ctx)
	if err != nil {
		return err
	}
	defer pa.Close()
	if pa.tok == nil {
		return nil
	}
	var toks snapshot.Tokens
	resolved := make([]string, 0, len(paths))
	for _, p := range paths {
		if strings.HasPrefix(p, "{") {
			if toks == nil {
				if toks, err = folderTokens(ctx, pa.profile); err != nil {
					return &Error{Code: CodeAccessDenied, Message: "access denied: your folders could not be resolved"}
				}
			}
			if p, err = toks.Resolve(p); err != nil {
				return invalidf("unknown folder " + strconv.Quote(p))
			}
		}
		resolved = append(resolved, p)
	}
	return pa.check(config.SyncConfig{Kind: "files", Files: resolved})
}

// SaveBackupJob creates or updates a backup job on the server.
func (h *Host) SaveBackupJob(ctx context.Context, j wire.Job) (BackupJob, error) {
	bk, err := h.backupReady()
	if err != nil {
		return BackupJob{}, err
	}
	s, err := bk.connectedStack()
	if err != nil {
		return BackupJob{}, err
	}
	if err := validateJob(&j); err != nil {
		return BackupJob{}, err
	}
	j.ProfileSID = jobProfileSID(ctx, j.ProfileSID)
	if err := h.checkJobPaths(ctx, j.Spec.Paths); err != nil {
		return BackupJob{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, serverCallTimeout)
	defer cancel()
	var saved *wire.Job
	if j.ID == 0 {
		repoID, _ := strconv.ParseInt(h.st.Get(kvRepoID), 10, 64)
		if j.RepoID == 0 {
			j.RepoID = repoID
		}
		if j.RepoID == 0 {
			return BackupJob{}, invalidf("set up the backup repository first")
		}
		saved, err = s.srv.CreateJob(cctx, wire.JobCreateRequest{DeviceID: s.c.DeviceID, RepoID: j.RepoID, Name: j.Name,
			Kind: j.Kind, Mode: j.Mode, Spec: j.Spec, Schedule: j.Schedule, RetentionPolicyID: j.RetentionPolicyID,
			Priority: j.Priority, Enabled: j.Enabled, ProfileSID: j.ProfileSID})
	} else {
		if _, err := h.visibleJobByID(ctx, bk, j.ID); err != nil {
			return BackupJob{}, err
		}
		upd := wire.JobUpdateRequest{Name: &j.Name, Mode: &j.Mode, Spec: &j.Spec, Schedule: &j.Schedule,
			RetentionPolicyID: j.RetentionPolicyID, Priority: &j.Priority, Enabled: &j.Enabled}
		if _, pipe := caller.From(ctx); pipe {
			upd.ProfileSID = &j.ProfileSID
		}
		saved, err = s.srv.UpdateJob(cctx, j.ID, j.Revision, upd)
	}
	if err != nil {
		return BackupJob{}, apiErr(err)
	}
	h.syncJobs(s)
	s.reload()
	out := BackupJob{Job: *saved, EffectiveState: saved.State}
	if lj, err := bk.st.Job(ctx, saved.ID); err == nil {
		out = BackupJob{Job: lj.Job, EffectiveState: lj.EffectiveState(), SuspendedByMode: lj.SuspendedByMode}
	}
	h.publishBackup()
	return out, nil
}

// localIssue is the part of a backup.db issue SkipIssues maps to the server.
type localIssue struct {
	runUUID, path, class string
}

// SkipIssues marks issues skipped on the server.
func (h *Host) SkipIssues(ctx context.Context, req SkipIssuesRequest) error {
	bk, err := h.backupReady()
	if err != nil {
		return err
	}
	s, err := bk.connectedStack()
	if err != nil {
		return err
	}
	switch {
	case len(req.IssueIDs) == 0 && len(req.Classes) == 0:
		return invalidf("choose issues or classes to skip")
	case len(req.IssueIDs) > maxSkipIDs || len(req.Classes) > maxSkipClasses:
		return invalidf("too many issues in one request")
	}
	if _, err := h.visibleJobByID(ctx, bk, req.JobID); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, serverCallTimeout)
	defer cancel()

	// Local ids → (server run, path, wire class) → server issue ids.
	byRun := map[string][]localIssue{}
	for _, id := range req.IssueIDs {
		var li localIssue
		err := bk.st.DB.QueryRowContext(ctx, `SELECT run_uuid, path, class FROM issues WHERE id = ? AND job_id = ?`,
			id, req.JobID).Scan(&li.runUUID, &li.path, &li.class)
		if err != nil {
			return &Error{Code: CodeNotFound, Message: "no such issue: " + strconv.FormatInt(id, 10)}
		}
		byRun[li.runUUID] = append(byRun[li.runUUID], li)
	}
	for uuid, want := range byRun {
		r, err := bk.st.Run(ctx, uuid)
		if err != nil {
			return backupErr(err)
		}
		if r.RunID == 0 {
			return invalidf("these issues have not reached the server yet; try again after the backup reports")
		}
		ids, err := serverIssueIDs(cctx, s.srv, r.RunID, want)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return &Error{Code: CodeNotFound, Message: "the server has no matching open issues"}
		}
		if _, err := s.srv.SkipIssues(cctx, r.RunID, wire.IssueSkipRequest{IDs: ids}); err != nil {
			return apiErr(err)
		}
	}
	if len(req.Classes) > 0 {
		runs, err := bk.st.Runs(ctx, req.JobID, 20)
		if err != nil {
			return backupErr(err)
		}
		var runID int64
		for _, r := range runs {
			if r.RunID != 0 {
				runID = r.RunID
				break
			}
		}
		if runID == 0 {
			return &Error{Code: CodeNotFound, Message: "this backup has no reported run yet"}
		}
		for _, c := range req.Classes {
			if _, err := s.srv.SkipIssues(cctx, runID, wire.IssueSkipRequest{Class: backup.WireClass(c)}); err != nil {
				return apiErr(err)
			}
		}
	}
	h.publishBackup()
	return nil
}

// serverIssueIDs pages a run's server issues and returns the ids matching
// want by path and wire class.
func serverIssueIDs(ctx context.Context, srv BackupServer, runID int64, want []localIssue) ([]int64, error) {
	key := func(path, class string) string { return class + "\x00" + path }
	need := make(map[string]bool, len(want))
	for _, w := range want {
		need[key(w.path, backup.WireClass(w.class))] = true
	}
	var ids []int64
	cursor := ""
	for page := 0; page < maxIssuePages; page++ {
		l, err := srv.RunIssues(ctx, runID, "", cursor, issuePageLimit)
		if err != nil {
			return nil, apiErr(err)
		}
		for _, is := range l.Items {
			if need[key(is.Path, is.Class)] {
				ids = append(ids, is.ID)
			}
		}
		if l.NextCursor == "" || l.NextCursor == cursor {
			break
		}
		cursor = l.NextCursor
	}
	return ids, nil
}

// GiveConsent records the caller's consent proof and reports profiles.
func (h *Host) GiveConsent(ctx context.Context, proof []byte) error {
	if err := h.live(); err != nil {
		return err
	}
	var sid string
	if c, ok := caller.From(ctx); ok {
		if IsSystem(c) {
			return invalidf("consent must be given by the profile's own user")
		}
		sid = c.SID
	} else {
		if h.serviceMode() {
			return invalidf("consent must be given by the profile's own user")
		}
		u, err := user.Current()
		if err != nil {
			return &Error{Code: CodeUnavailable, Message: "the current user is unknown"}
		}
		sid = u.Uid
	}
	c := h.currentClient()
	if c == nil {
		return &Error{Code: CodeUnavailable, Message: "this device is not connected to a VaultDock server"}
	}
	m := h.profileManager(c)
	if _, err := m.RecordConsent(ctx, sid, proof); err != nil {
		switch {
		case errors.Is(err, profiles.ErrInvalidSID), errors.Is(err, profiles.ErrConsentInvalid), errors.Is(err, profiles.ErrConsentStale):
			return invalidf(err.Error())
		case errors.Is(err, profiles.ErrBlacklisted):
			return &Error{Code: CodeForbidden, Message: "this profile is blocked; an administrator must clear it on the server"}
		case errors.Is(err, profiles.ErrNeedSession), errors.Is(err, profiles.ErrDPAPIKeyUnavailable):
			return &Error{Code: CodeUnavailable, Message: "sign in to Windows as this user and try again"}
		}
		return &Error{Code: CodeInternal, Message: err.Error()}
	}
	if _, err := m.Report(ctx); err != nil {
		h.log.Warn("profile report not sent", "err", err)
	}
	h.publishBackup()
	return nil
}

// GetMode returns the install mode and module states.
func (h *Host) GetMode(ctx context.Context) (modules.ModulesState, error) {
	if err := h.live(); err != nil {
		return modules.ModulesState{}, err
	}
	m := h.Modules()
	if m == nil {
		return modules.ModulesState{}, &Error{Code: CodeUnavailable, Message: "modules are not available in this agent"}
	}
	return m.Snapshot(), nil
}
