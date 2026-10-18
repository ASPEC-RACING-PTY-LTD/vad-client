// Package scheduler starts scheduled backup runs on the desktop (P1-27).
//
// The Scheduler asks a JobSource for the runnable jobs, evaluates each job's
// schedule with sdk/schedule (missed-run catch-up within the grace period,
// deterministic per-device jitter), fires Trigger with trigger "schedule" for
// due jobs, and sleeps on an injectable Clock until the earliest next run, a
// Reload signal (job sync, mode change) or context cancellation.
//
// The package depends only on the small interfaces below; the backup engine
// is wired in by the agent host.
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/schedule"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// Trigger is the run trigger the scheduler passes to TriggerFunc.
const Trigger = wire.RunTriggerSchedule

// Default timings.
const (
	// DefaultMaxSleep caps one sleep so wall-clock jumps, resume from
	// sleep and zone changes are noticed.
	DefaultMaxSleep = 15 * time.Minute
	// DefaultRetry is the sleep after a JobSource error.
	DefaultRetry = time.Minute
)

// Job is one runnable job as the scheduler sees it.
type Job struct {
	ID       int64
	Schedule schedule.Schedule
	// LastRun is the start of the job's last run, or the job's creation
	// time if it never ran. Zero counts as overdue (one catch-up run).
	LastRun time.Time
}

// JobSource lists the jobs that may run now (enabled, active, not
// suspended by mode). Manual jobs may be included; they never fire.
type JobSource interface {
	RunnableJobs(ctx context.Context) ([]Job, error)
}

// TriggerFunc starts a run of jobID. It must return quickly (start the run
// asynchronously). An error is logged; the slot is not retried.
type TriggerFunc func(ctx context.Context, jobID int64, trigger string) error

// Clock is the time source; tests inject a fake.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Config configures a Scheduler. Source and Trigger are required.
type Config struct {
	Source  JobSource
	Trigger TriggerFunc
	// Running reports whether a run of jobID is in progress; a due slot
	// of a running job is skipped (consumed, not queued). Nil means never.
	Running func(jobID int64) bool
	// DeviceID is the stable device identifier for jitter seeds; it must
	// match the string the server uses (schedule.JitterSeed).
	DeviceID string
	// Loc is the device zone for "Local" schedules; nil means time.Local.
	Loc *time.Location
	// Grace is the on-time window; 0 means schedule.DefaultGrace.
	Grace    time.Duration
	Clock    Clock         // nil means the real clock
	MaxSleep time.Duration // 0 means DefaultMaxSleep
	Retry    time.Duration // 0 means DefaultRetry
	Log      *slog.Logger  // nil means slog.Default()
}

// Scheduler fires scheduled runs. Create it with New and drive it with Run.
type Scheduler struct {
	cfg    Config
	reload chan struct{}

	mu    sync.Mutex
	fired map[int64]time.Time // local record of the last slot taken per job
}

// New returns a Scheduler for cfg.
func New(cfg Config) *Scheduler {
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.MaxSleep <= 0 {
		cfg.MaxSleep = DefaultMaxSleep
	}
	if cfg.Retry <= 0 {
		cfg.Retry = DefaultRetry
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Scheduler{cfg: cfg, reload: make(chan struct{}, 1), fired: map[int64]time.Time{}}
}

// Reload makes Run re-read the jobs and recompute next run times now. It
// never blocks; several calls before Run wakes coalesce.
func (s *Scheduler) Reload() {
	select {
	case s.reload <- struct{}{}:
	default:
	}
}

// Run evaluates the jobs until ctx is cancelled and then returns ctx.Err().
func (s *Scheduler) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		wait := s.tick(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.reload:
		case <-s.cfg.Clock.After(wait):
		}
	}
}

// tick fires due jobs and returns how long to sleep.
func (s *Scheduler) tick(ctx context.Context) time.Duration {
	jobs, err := s.cfg.Source.RunnableJobs(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.cfg.Log.Warn("scheduler: list jobs", "err", err)
		}
		return s.cfg.Retry
	}
	now := s.cfg.Clock.Now()
	var earliest time.Time
	seen := make(map[int64]bool, len(jobs))
	for _, j := range jobs {
		seen[j.ID] = true
		next := s.evaluate(ctx, j, now)
		if !next.IsZero() && (earliest.IsZero() || next.Before(earliest)) {
			earliest = next
		}
	}
	s.mu.Lock()
	for id := range s.fired {
		if !seen[id] {
			delete(s.fired, id)
		}
	}
	s.mu.Unlock()

	wait := s.cfg.MaxSleep
	if !earliest.IsZero() {
		if d := earliest.Sub(now); d < wait {
			wait = d
		}
	}
	if wait < 0 {
		wait = 0
	}
	return wait
}

// evaluate fires j if due at now and returns its next run time (zero if
// none).
func (s *Scheduler) evaluate(ctx context.Context, j Job, now time.Time) time.Time {
	if j.Schedule.Type == schedule.TypeManual || j.Schedule.Type == "" {
		return time.Time{}
	}
	if err := j.Schedule.Validate(); err != nil {
		s.cfg.Log.Warn("scheduler: invalid schedule", "job", j.ID, "err", err)
		return time.Time{}
	}
	loc, err := schedule.ResolveLocation(j.Schedule.Timezone, s.cfg.Loc)
	if err != nil {
		s.cfg.Log.Warn("scheduler: time zone", "job", j.ID, "tz", j.Schedule.Timezone, "err", err)
		return time.Time{}
	}
	opt := schedule.Options{Loc: loc, JitterSeed: schedule.JitterSeed(s.cfg.DeviceID, j.ID), Grace: s.cfg.Grace}

	// The run the trigger starts may not be recorded by the source yet;
	// the local record prevents firing the same slot twice.
	last := j.LastRun
	s.mu.Lock()
	if f, ok := s.fired[j.ID]; ok && (last.IsZero() || f.After(last)) {
		last = f
	}
	s.mu.Unlock()

	d := j.Schedule.Due(last, now, opt)
	if !d.Due {
		if d.Missed {
			s.cfg.Log.Info("scheduler: missed runs skipped", "job", j.ID)
		}
		return d.Next
	}
	s.mu.Lock()
	s.fired[j.ID] = now
	s.mu.Unlock()
	if s.cfg.Running != nil && s.cfg.Running(j.ID) {
		s.cfg.Log.Info("scheduler: job already running, slot skipped", "job", j.ID)
		return d.Next
	}
	s.cfg.Log.Info("scheduler: start run", "job", j.ID, "catch_up", d.Missed)
	if err := s.cfg.Trigger(ctx, j.ID, Trigger); err != nil {
		s.cfg.Log.Warn("scheduler: trigger", "job", j.ID, "err", err)
	}
	return d.Next
}
