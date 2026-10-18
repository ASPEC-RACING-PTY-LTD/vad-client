package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/schedule"
)

// fakeClock is a manual clock. After registers a waiter that fires when
// Advance moves the time past its deadline.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	calls   int
	waiters []fakeWaiter
	called  chan struct{}
}

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now, called: make(chan struct{}, 100)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
	} else {
		c.waiters = append(c.waiters, fakeWaiter{c.now.Add(d), ch})
	}
	c.calls++
	c.mu.Unlock()
	c.called <- struct{}{}
	return ch
}

// Advance moves the clock by d and fires due waiters.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
		} else {
			kept = append(kept, w)
		}
	}
	c.waiters = kept
}

// waitSleep blocks until the scheduler has called After once more.
func (c *fakeClock) waitSleep(t *testing.T) {
	t.Helper()
	select {
	case <-c.called:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not sleep")
	}
}

type fakeSource struct {
	mu   sync.Mutex
	jobs []Job
	err  error
}

func (s *fakeSource) RunnableJobs(context.Context) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Job(nil), s.jobs...), s.err
}

func (s *fakeSource) set(jobs ...Job) {
	s.mu.Lock()
	s.jobs = jobs
	s.mu.Unlock()
}

type fired struct {
	id      int64
	at      time.Time
	trigger string
}

type recorder struct {
	mu    sync.Mutex
	clock *fakeClock
	got   []fired
}

func (r *recorder) trigger(_ context.Context, id int64, trig string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, fired{id, r.clock.Now(), trig})
	return nil
}

func (r *recorder) take() []fired {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.got
	r.got = nil
	return out
}

var t0 = time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC) // a Monday

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func interval(min int) schedule.Schedule {
	return schedule.Schedule{Type: schedule.TypeInterval, EveryMinutes: min, Timezone: "UTC"}
}

func daily(at, catchUp string) schedule.Schedule {
	return schedule.Schedule{Type: schedule.TypeDaily, At: at, Timezone: "UTC", CatchUp: catchUp}
}

type harness struct {
	clock  *fakeClock
	src    *fakeSource
	rec    *recorder
	s      *Scheduler
	cancel context.CancelFunc
	done   chan error
}

func start(t *testing.T, now time.Time, running func(int64) bool, jobs ...Job) *harness {
	t.Helper()
	h := &harness{clock: newFakeClock(now), src: &fakeSource{}, done: make(chan error, 1)}
	h.rec = &recorder{clock: h.clock}
	h.src.set(jobs...)
	h.s = New(Config{Source: h.src, Trigger: h.rec.trigger, Running: running, DeviceID: "dev",
		Loc: time.UTC, Clock: h.clock, MaxSleep: time.Hour, Log: quiet()})
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.done <- h.s.Run(ctx) }()
	h.clock.waitSleep(t)
	t.Cleanup(h.stop)
	return h
}

func (h *harness) stop() {
	h.cancel()
	<-h.done
}

// step advances the clock by d in one-minute steps, letting the scheduler
// wake and sleep again at each due waiter.
func (h *harness) step(t *testing.T, d time.Duration) {
	t.Helper()
	for end := h.clock.Now().Add(d); h.clock.Now().Before(end); {
		h.clock.mu.Lock()
		// Only the newest waiter is live; earlier ones were abandoned by a
		// Reload.
		var next time.Time
		if n := len(h.clock.waiters); n > 0 {
			next = h.clock.waiters[n-1].at
		}
		h.clock.mu.Unlock()
		if next.IsZero() || next.After(end) {
			h.clock.Advance(end.Sub(h.clock.Now()))
			return
		}
		h.clock.Advance(next.Sub(h.clock.Now()))
		h.clock.waitSleep(t)
	}
}

func times(fs []fired) []time.Time {
	out := make([]time.Time, len(fs))
	for i, f := range fs {
		out[i] = f.at
	}
	return out
}

func TestScheduler(t *testing.T) {
	tests := []struct {
		name    string
		now     time.Time
		job     Job
		running bool
		advance time.Duration
		want    []time.Time
	}{
		{
			name:    "interval fires on epoch grid",
			now:     t0.Add(time.Minute),
			job:     Job{ID: 1, Schedule: interval(15), LastRun: t0},
			advance: 31 * time.Minute,
			want:    []time.Time{t0.Add(15 * time.Minute), t0.Add(30 * time.Minute)},
		},
		{
			name:    "daily fires once at wall time",
			now:     t0,
			job:     Job{ID: 1, Schedule: daily("10:30", ""), LastRun: t0.Add(-time.Hour)},
			advance: 24 * time.Hour,
			want:    []time.Time{t0.Add(90 * time.Minute)},
		},
		{
			name: "slot within grace runs at start",
			now:  t0.Add(3 * time.Minute),
			job:  Job{ID: 1, Schedule: daily("09:00", ""), LastRun: t0.Add(-24 * time.Hour)},
			want: []time.Time{t0.Add(3 * time.Minute)},
		},
		{
			name: "missed slots catch up once",
			now:  t0.Add(5 * time.Hour),
			job:  Job{ID: 1, Schedule: interval(60), LastRun: t0.Add(-48 * time.Hour)},
			want: []time.Time{t0.Add(5 * time.Hour)},
		},
		{
			name: "missed slot skipped by catch_up skip",
			now:  t0.Add(2 * time.Hour),
			job:  Job{ID: 1, Schedule: daily("09:00", schedule.CatchUpSkip), LastRun: t0.Add(-24 * time.Hour)},
			want: nil,
		},
		{
			name: "never ran (zero last) is overdue",
			now:  t0.Add(time.Hour),
			job:  Job{ID: 1, Schedule: daily("09:00", "")},
			want: []time.Time{t0.Add(time.Hour)},
		},
		{
			name:    "manual never fires",
			now:     t0,
			job:     Job{ID: 1, Schedule: schedule.Schedule{Type: schedule.TypeManual}},
			advance: 48 * time.Hour,
			want:    nil,
		},
		{
			name:    "invalid schedule ignored",
			now:     t0,
			job:     Job{ID: 1, Schedule: interval(1)},
			advance: time.Hour,
			want:    nil,
		},
		{
			name:    "running job slot skipped",
			now:     t0.Add(time.Minute),
			job:     Job{ID: 1, Schedule: interval(15), LastRun: t0},
			running: true,
			advance: time.Hour,
			want:    nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := start(t, tc.now, func(int64) bool { return tc.running }, tc.job)
			h.step(t, tc.advance)
			got := h.rec.take()
			if len(got) != len(tc.want) {
				t.Fatalf("fired at %v, want %v", times(got), tc.want)
			}
			for i, f := range got {
				if !f.at.Equal(tc.want[i]) || f.id != tc.job.ID || f.trigger != "schedule" {
					t.Fatalf("fire %d = %+v, want job %d at %v trigger schedule", i, f, tc.job.ID, tc.want[i])
				}
			}
		})
	}
}

func TestReloadPicksUpNewJob(t *testing.T) {
	h := start(t, t0.Add(time.Minute), nil)
	h.src.set(Job{ID: 7, Schedule: interval(5), LastRun: t0})
	h.s.Reload()
	h.clock.waitSleep(t)
	h.step(t, 5*time.Minute)
	got := h.rec.take()
	if len(got) != 1 || got[0].id != 7 || !got[0].at.Equal(t0.Add(5*time.Minute)) {
		t.Fatalf("got %+v", got)
	}

	// Removing the job (mode change) stops it firing.
	h.src.set()
	h.s.Reload()
	h.clock.waitSleep(t)
	h.step(t, time.Hour)
	if got := h.rec.take(); len(got) != 0 {
		t.Fatalf("removed job fired: %+v", got)
	}
}

func TestStaleLastRunDoesNotRefire(t *testing.T) {
	// The source never updates LastRun; the local record must prevent the
	// catch-up from firing again on every wake.
	h := start(t, t0.Add(5*time.Hour), nil, Job{ID: 1, Schedule: interval(60), LastRun: t0.Add(-48 * time.Hour)})
	h.s.Reload()
	h.clock.waitSleep(t)
	h.step(t, 90*time.Minute)
	got := times(h.rec.take())
	want := []time.Time{t0.Add(5 * time.Hour), t0.Add(6 * time.Hour)}
	if len(got) != 2 || !got[0].Equal(want[0]) || !got[1].Equal(want[1]) {
		t.Fatalf("fired at %v, want %v", got, want)
	}
}

func TestSourceErrorRetries(t *testing.T) {
	h := start(t, t0, nil)
	h.src.mu.Lock()
	h.src.err = errors.New("db locked")
	h.src.mu.Unlock()
	h.s.Reload()
	h.clock.waitSleep(t)
	h.clock.mu.Lock()
	w := h.clock.waiters[len(h.clock.waiters)-1]
	h.clock.mu.Unlock()
	if got := w.at.Sub(t0); got != DefaultRetry {
		t.Fatalf("retry sleep %v, want %v", got, DefaultRetry)
	}
}

func TestSleepCappedAndStop(t *testing.T) {
	h := start(t, t0, nil, Job{ID: 1, Schedule: daily("09:00", ""), LastRun: t0})
	h.clock.mu.Lock()
	w := h.clock.waiters[0]
	h.clock.mu.Unlock()
	if got := w.at.Sub(t0); got != time.Hour {
		t.Fatalf("sleep %v, want MaxSleep 1h", got)
	}
	h.cancel()
	select {
	case err := <-h.done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v", err)
		}
		h.done <- err
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}
