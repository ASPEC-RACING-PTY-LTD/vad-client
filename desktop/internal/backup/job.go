package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/modules"
	sdkmod "github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/modules"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// ItemKindBackupJob is modules.AffectedItem.Kind for a backup job.
const ItemKindBackupJob = "backup_job"

// Job is the local mirror of a server job plus local state.
type Job struct {
	wire.Job
	// SuspendedByMode is set locally when a mode or module change disabled
	// a module the job requires; the job is kept, never deleted.
	SuspendedByMode bool
	SyncedAt        int64
}

// EffectiveState is the job state the agent acts on: a local mode
// suspension wins over the server state.
func (j *Job) EffectiveState() string {
	if j.SuspendedByMode {
		return wire.JobStateSuspendedByMode
	}
	if j.State == "" {
		return wire.JobStateActive
	}
	return j.State
}

// Runnable reports whether the job may run now (enabled and active).
func (j *Job) Runnable() bool { return j.Enabled && j.EffectiveState() == wire.JobStateActive }

// Requires returns the job's required modules as module ids.
func (j *Job) Requires() []sdkmod.ID {
	out := make([]sdkmod.ID, 0, len(j.RequiredModules))
	for _, m := range j.RequiredModules {
		out = append(out, sdkmod.ID(m))
	}
	return out
}

const jobCols = "job, suspended_by_mode, synced_at"

func scanJob(sc interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var raw string
	var susp int
	if err := sc.Scan(&raw, &susp, &j.SyncedAt); err != nil {
		return j, err
	}
	if err := json.Unmarshal([]byte(raw), &j.Job); err != nil {
		return j, fmt.Errorf("backup: decode job: %w", err)
	}
	j.SuspendedByMode = susp != 0
	return j, nil
}

// Jobs lists all mirrored jobs by id.
func (s *Store) Jobs(ctx context.Context) ([]Job, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+jobCols+" FROM jobs ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Job loads one job (ErrNotFound).
func (s *Store) Job(ctx context.Context, id int64) (*Job, error) {
	j, err := scanJob(s.DB.QueryRowContext(ctx, "SELECT "+jobCols+" FROM jobs WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func (s *Store) putJob(ctx context.Context, x execer, j Job) error {
	if j.ID <= 0 {
		return errors.New("backup: job without id")
	}
	raw, err := json.Marshal(j.Job)
	if err != nil {
		return err
	}
	now := s.nowMS()
	if j.SyncedAt == 0 {
		j.SyncedAt = now
	}
	state := j.State
	if state == "" {
		state = wire.JobStateActive
	}
	_, err = x.ExecContext(ctx, `INSERT OR REPLACE INTO jobs
		(id, revision, profile_sid, server_state, enabled, suspended_by_mode, job, synced_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		j.ID, j.Revision, j.ProfileSID, state, boolInt(j.Enabled), boolInt(j.SuspendedByMode), string(raw), j.SyncedAt, now)
	return err
}

// PutJob inserts or replaces a job as given (local edits, tests).
func (s *Store) PutJob(ctx context.Context, j Job) error { return s.putJob(ctx, s.DB, j) }

// DeleteJob removes a job and its file cache. Runs, packs and issues are
// kept (they belong to snapshots that still exist).
func (s *Store) DeleteJob(ctx context.Context, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM jobs WHERE id = ?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM file_cache WHERE job_id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetJobSuspended sets or clears the local suspended_by_mode flag.
func (s *Store) SetJobSuspended(ctx context.Context, id int64, suspended bool) error {
	return s.exec1(ctx, "UPDATE jobs SET suspended_by_mode = ?, updated_at = ? WHERE id = ?", boolInt(suspended), s.nowMS(), id)
}

// SyncResult lists what SyncJobs changed, by job id.
type SyncResult struct {
	Added, Updated, Removed []int64
	Stale                   []int64 // server copy older than the local one; ignored
}

// SyncJobs merges the server's job list into the mirror by revision: new
// jobs are added, a higher revision replaces the server fields (the local
// suspended_by_mode flag is kept), an equal revision is left alone and a
// lower one is ignored as stale. When complete is true the list is the
// device's full job list and local jobs missing from it are removed
// (with their file cache). Jobs of other devices are ignored when
// deviceID != 0. Everything happens in one transaction.
func (s *Store) SyncJobs(ctx context.Context, server []wire.Job, deviceID int64, complete bool) (SyncResult, error) {
	var res SyncResult
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	local := map[int64]Job{}
	rows, err := tx.QueryContext(ctx, "SELECT "+jobCols+" FROM jobs")
	if err != nil {
		return res, err
	}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			rows.Close()
			return res, err
		}
		local[j.ID] = j
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	seen := map[int64]bool{}
	for _, sj := range server {
		if sj.ID <= 0 || (deviceID != 0 && sj.DeviceID != 0 && sj.DeviceID != deviceID) {
			continue
		}
		seen[sj.ID] = true
		lj, ok := local[sj.ID]
		switch {
		case !ok:
			res.Added = append(res.Added, sj.ID)
		case sj.Revision > lj.Revision:
			res.Updated = append(res.Updated, sj.ID)
		case sj.Revision < lj.Revision:
			res.Stale = append(res.Stale, sj.ID)
			continue
		default:
			continue
		}
		if err := s.putJob(ctx, tx, Job{Job: sj, SuspendedByMode: lj.SuspendedByMode}); err != nil {
			return res, err
		}
	}
	if complete {
		for id := range local {
			if seen[id] {
				continue
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM jobs WHERE id = ?", id); err != nil {
				return res, err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM file_cache WHERE job_id = ?", id); err != nil {
				return res, err
			}
			res.Removed = append(res.Removed, id)
		}
	}
	slices.Sort(res.Removed)
	return res, tx.Commit()
}

// JobLister is one page of GET /backup/jobs (*client.Client.Jobs).
type JobLister func(ctx context.Context, cursor string, limit int) (*wire.List[wire.Job], error)

// FetchJobs pages through the server job list.
func FetchJobs(ctx context.Context, list JobLister) ([]wire.Job, error) {
	var out []wire.Job
	cursor := ""
	for range 10000 {
		page, err := list(ctx, cursor, 200)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if page.NextCursor == "" || page.NextCursor == cursor {
			return out, nil
		}
		cursor = page.NextCursor
	}
	return nil, errors.New("backup: job list did not terminate")
}

// SyncFromServer fetches the full job list and merges it (complete=true).
func (s *Store) SyncFromServer(ctx context.Context, list JobLister, deviceID int64) (SyncResult, error) {
	jobs, err := FetchJobs(ctx, list)
	if err != nil {
		return SyncResult{}, err
	}
	return s.SyncJobs(ctx, jobs, deviceID, true)
}

// JobSource adapts backup jobs to modules.ItemSource: Suspend and Resume
// only flip the local suspended_by_mode flag and never delete a job, its
// cache or its backups.
type JobSource struct {
	Store *Store
	mu    sync.Mutex
}

var _ modules.ItemSource = (*JobSource)(nil)

// NewJobSource returns the backup job ItemSource for
// modules.Manager.RegisterSource.
func NewJobSource(st *Store) *JobSource { return &JobSource{Store: st} }

// Affected lists jobs not yet suspended whose required modules include any
// of disabling.
func (s *JobSource) Affected(ctx context.Context, disabling []sdkmod.ID) ([]modules.AffectedItem, error) {
	jobs, err := s.Store.Jobs(ctx)
	if err != nil {
		return nil, err
	}
	var out []modules.AffectedItem
	for _, j := range jobs {
		if j.SuspendedByMode {
			continue
		}
		req := j.Requires()
		if !slices.ContainsFunc(req, func(id sdkmod.ID) bool { return slices.Contains(disabling, id) }) {
			continue
		}
		out = append(out, modules.AffectedItem{Kind: ItemKindBackupJob, ID: fmt.Sprint(j.ID), Name: j.Name,
			Requires: req, Reason: "the backup job pauses; existing backups are kept"})
	}
	return out, nil
}

// Suspend marks the given backup jobs suspended_by_mode.
func (s *JobSource) Suspend(ctx context.Context, items []modules.AffectedItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for _, it := range items {
		if it.Kind != ItemKindBackupJob {
			continue
		}
		var id int64
		if _, err := fmt.Sscan(it.ID, &id); err != nil {
			continue
		}
		if err := s.Store.SetJobSuspended(ctx, id, true); err != nil && !errors.Is(err, ErrNotFound) {
			errs = append(errs, fmt.Errorf("suspend backup job %d: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// Resume clears the flag on suspended jobs whose modules are all enabled.
func (s *JobSource) Resume(ctx context.Context, enabled []sdkmod.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs, err := s.Store.Jobs(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, j := range jobs {
		if !j.SuspendedByMode {
			continue
		}
		if slices.ContainsFunc(j.Requires(), func(id sdkmod.ID) bool { return !slices.Contains(enabled, id) }) {
			continue
		}
		if err := s.Store.SetJobSuspended(ctx, j.ID, false); err != nil && !errors.Is(err, ErrNotFound) {
			errs = append(errs, fmt.Errorf("resume backup job %d: %w", j.ID, err))
		}
	}
	return errors.Join(errs...)
}
