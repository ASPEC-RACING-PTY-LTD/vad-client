package backup

import (
	"context"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/scheduler"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/schedule"
)

// SchedulerJobs is the scheduler's job source (scheduler.Config.Source):
// the runnable local jobs with their schedules and last run start.
type SchedulerJobs struct{ Store *Store }

var _ scheduler.JobSource = SchedulerJobs{}

// RunnableJobs lists enabled, active jobs. LastRun is the start of the
// job's latest local run, else the job's creation time.
func (s SchedulerJobs) RunnableJobs(ctx context.Context) ([]scheduler.Job, error) {
	jobs, err := s.Store.Jobs(ctx)
	if err != nil {
		return nil, err
	}
	var out []scheduler.Job
	for i := range jobs {
		j := &jobs[i]
		if !j.Runnable() {
			continue
		}
		var last time.Time
		runs, err := s.Store.Runs(ctx, j.ID, 1)
		if err != nil {
			return nil, err
		}
		if len(runs) > 0 {
			last = time.UnixMilli(runs[0].StartedAt)
		} else if j.CreatedAt > 0 {
			last = time.UnixMilli(j.CreatedAt)
		}
		out = append(out, scheduler.Job{ID: j.ID, Schedule: schedule.Schedule(j.Schedule), LastRun: last})
	}
	return out, nil
}

// SchedulerConfig returns a scheduler.Config wired to this runner: Source,
// Trigger (Runner.Trigger, which returns at once) and Running. The caller
// sets DeviceID, Loc and Log.
func (r *Runner) SchedulerConfig() scheduler.Config {
	return scheduler.Config{Source: SchedulerJobs{Store: r.Store}, Trigger: r.Trigger, Running: r.Running}
}
