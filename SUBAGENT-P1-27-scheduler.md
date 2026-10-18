# P1-27 DSK-C Desktop scheduler
Status: completed

## Goal
Add a desktop scheduler that starts scheduled backup runs. It evaluates each job's schedule with `sdk/schedule`: next run times, jitter, and catch-up for missed runs within the grace period. It sleeps on a clock that tests can replace, calls `Trigger(ctx, jobID, "schedule")` when a job is due, skips jobs that are already running, re-reads jobs when `Reload()` is called, and stops when ctx is cancelled. It depends only on its own interfaces; it does not import `internal/backup`.

## Changes (repo: path, created/modified/deleted, why)
All in the VaultDock client repo, all created:
- `desktop/internal/scheduler/scheduler.go`: the `Scheduler`, `Config`, the `JobSource`/`Clock`/`TriggerFunc` interfaces and the run loop.
- `desktop/internal/scheduler/scheduler_test.go`: a fake clock and table tests. There are 9 table cases (interval epoch grid, daily wall time, slot within grace, catch-up runs once after many missed slots, `catch_up: skip`, never ran with a zero last run, manual, invalid schedule, job already running), plus tests for:
  - `Reload` adding and removing a job;
  - a stale `LastRun` not causing a second run;
  - a source error leading to a retry sleep;
  - the `MaxSleep` cap and a clean stop.

## Interfaces exposed
```go
package scheduler // github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/scheduler

const Trigger = wire.RunTriggerSchedule // "schedule"
const DefaultMaxSleep = 15 * time.Minute; DefaultRetry = time.Minute

type Job struct { ID int64; Schedule schedule.Schedule; LastRun time.Time } // LastRun: last run start, else job creation time; zero = overdue
type JobSource interface { RunnableJobs(ctx context.Context) ([]Job, error) }
type TriggerFunc func(ctx context.Context, jobID int64, trigger string) error // must return quickly
type Clock interface { Now() time.Time; After(d time.Duration) <-chan time.Time }

type Config struct {
    Source JobSource; Trigger TriggerFunc   // required
    Running func(jobID int64) bool          // optional; a due slot of a running job is consumed, not queued
    DeviceID string                         // jitter seed: schedule.JitterSeed(DeviceID, jobID)
    Loc *time.Location                      // device zone for "Local"; nil = time.Local
    Grace, MaxSleep, Retry time.Duration    // 0 = schedule.DefaultGrace / DefaultMaxSleep / DefaultRetry
    Clock Clock; Log *slog.Logger           // nil = real clock / slog.Default()
}
func New(cfg Config) *Scheduler
func (s *Scheduler) Reload()                       // non-blocking; repeated calls before the next check count as one
func (s *Scheduler) Run(ctx context.Context) error // blocks; returns ctx.Err()
```

What the scheduler does on each check:
- It lists jobs and computes `Due` for each one. The last run time it uses is the later of `Job.LastRun` and the time this scheduler last fired that job. That local record prevents a double run before the source has recorded the new run.
- It sleeps until the earliest next run, at most `MaxSleep`. The cap means the scheduler notices wall-clock jumps and resume from sleep.
- `Manual`, empty, invalid and bad-time-zone schedules never fire; the invalid and bad-time-zone cases are logged.
- A `Trigger` error is logged and the slot is not retried.

## Commands run and results
- `gofmt -l internal/scheduler`: clean (after `gofmt -w`).
- `go vet ./internal/scheduler/...`: clean.
- `go test -count=3 ./internal/scheduler/...`: ok. `-race` could not run because cgo is disabled on this host.
- `go build ./internal/scheduler/`: ok.
- `go build ./...`: **fails in `internal/keystore`**, which another agent is editing (`undefined: pinRecord`, `loadPin`). The failure is not caused by this task.

## Contract issues
None new. This task relies on the P1-08 interpretations: ISO weekdays, an interval grid anchored to the epoch, 5-field cron and a 5-minute grace.

## Gaps / follow-ups
- There is no API that returns upcoming run times for the UI. P1-29 can call `schedule.Schedule.NextRun` directly with the same `Options` (zone from `ResolveLocation`, seed from `JitterSeed(deviceID, jobID)`).
- If a job is running when its slot arrives, that slot is consumed: it is not queued to run after the current run finishes.

## Notes for dependent tasks / INT
- **Wiring for P1-26/P1-29:** write an adapter over `backup.Store` that returns `Job`s from `Jobs()` filtered by `Runnable()`, with `Schedule: schedule.Schedule(j.Schedule)`.
  - Set `LastRun` to the start time of the job's latest run, from `Runs(ctx, id, 1)`. If the job has no runs, use its creation time.
  - Set `Trigger` to the engine's start-run call, which should start the run asynchronously.
  - Set `Running` to the engine's per-job busy check.
- Call `Reload()` after `SyncJobs`/`SyncFromServer`, after mode or module changes (`JobSource.Suspend` and `Resume`), and on resume from OS sleep.
- `DeviceID` must be the same stable device string the server uses for jitter, so both sides compute the same run times.
- `main` should `import _ "time/tzdata"` on Windows (see the P1-08 report).
