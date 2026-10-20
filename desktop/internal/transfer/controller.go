package transfer

import (
	"context"
	"math"
	"sync"
	"time"
)

// Controller adapts the number of parallel upload requests to the observed
// connection and server behaviour.
//
// It is a hill-climbing controller in the spirit of TCP congestion control:
//   - slow start: while throughput keeps improving the limit doubles;
//   - congestion avoidance: afterwards it grows by ~12% per window and keeps
//     an increase only if throughput rises by at least 5%;
//   - if throughput falls after an increase, the previous limit is restored
//     and held for a while before probing again;
//   - errors that indicate congestion (HTTP 429/503, timeouts, resets) or a
//     strong rise in request latency cut the limit by 30%.
//
// Only windows where the pipeline was actually saturated count as evidence,
// so an idle queue never inflates the limit. Upper bounds come from the
// user's setting, the server's advertised capacity and an absolute cap.
type Controller struct {
	mu       sync.Mutex
	limit    int
	max      int
	manual   int
	ceiling  int // the governor's ceiling for automatic mode (0 = none)
	inflight int
	changed  chan struct{}
	// waiting counts the Acquire calls waiting per priority; a free slot
	// goes to the lowest priority value first.
	waiting map[int64]int

	// current window
	winStart   time.Time
	winBytes   int64
	winReqs    int
	winLatency time.Duration
	winErrors  int
	satSince   time.Time
	satTime    time.Duration

	// history
	slowStart  bool
	ssthresh   int
	prevTput   float64
	prevLimit  int
	holdUntil  time.Time
	minLatency time.Duration
	lastTput   float64
}

// AbsoluteMax is the hard upper bound on parallel requests.
const AbsoluteMax = 128

// NewController returns a controller starting at two parallel requests.
func NewController(maxLimit int) *Controller {
	c := &Controller{limit: 2, max: clamp(maxLimit, 1, AbsoluteMax), changed: make(chan struct{}), waiting: map[int64]int{}, slowStart: true, ssthresh: AbsoluteMax, winStart: time.Now()}
	return c
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

func (c *Controller) notify() {
	close(c.changed)
	c.changed = make(chan struct{})
}

// SetBounds updates the automatic maximum and the manual override (0 = auto).
func (c *Controller) SetBounds(maxLimit, manual int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.max = clamp(maxLimit, 1, AbsoluteMax)
	c.manual = clamp(manual, 0, AbsoluteMax)
	if c.manual > 0 {
		c.limit = c.manual
	} else {
		c.limit = clamp(c.limit, 1, c.max)
	}
	c.notify()
}

// CapMax lowers the automatic maximum to n (for example the server's
// advertised chunk capacity). The limit itself is clamped on the next Tick.
func (c *Controller) CapMax(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.max = clamp(min(c.max, n), 1, AbsoluteMax)
}

// SetCeiling bounds the automatic limit by what the computer can spare
// (Governor); 0 removes the bound. A fixed (manual) limit ignores it.
func (c *Controller) SetCeiling(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n == c.ceiling {
		return
	}
	c.ceiling = max(0, n)
	c.notify()
}

// effective is the limit in force. Callers hold c.mu.
func (c *Controller) effective() int {
	if c.manual == 0 && c.ceiling > 0 {
		return max(1, min(c.limit, c.ceiling))
	}
	return c.limit
}

// Limit returns the current parallel request limit.
func (c *Controller) Limit() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.effective()
}

// Inflight returns the number of requests in progress.
func (c *Controller) Inflight() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inflight
}

// Throughput returns the last measured throughput in bytes per second.
func (c *Controller) Throughput() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastTput
}

// Acquire blocks until a request slot is free.
func (c *Controller) Acquire(ctx context.Context) error { return c.AcquirePriority(ctx, 0) }

// AcquirePriority blocks until a request slot is free and no request with a
// lower priority value is waiting for one. Uploads pass their queue order,
// so the file that started first gets the connections and finishes first
// instead of every file sharing the bandwidth.
func (c *Controller) AcquirePriority(ctx context.Context, prio int64) error {
	c.mu.Lock()
	c.waiting[prio]++
	defer func() {
		if c.waiting[prio]--; c.waiting[prio] <= 0 {
			delete(c.waiting, prio)
		}
		c.notify() // the next waiter may now be first
		c.mu.Unlock()
	}()
	for {
		if c.inflight < c.effective() && c.first(prio) {
			c.inflight++
			if c.inflight >= c.effective() && c.satSince.IsZero() {
				c.satSince = time.Now()
			}
			return nil
		}
		ch := c.changed
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			c.mu.Lock()
			return ctx.Err()
		case <-ch:
		}
		c.mu.Lock()
	}
}

// first reports whether no waiter has a lower priority value than prio.
func (c *Controller) first(prio int64) bool {
	for p := range c.waiting {
		if p < prio {
			return false
		}
	}
	return true
}

// Release frees a slot and records the request outcome.
func (c *Controller) Release(bytes int64, latency time.Duration, congestion bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight >= c.effective() && !c.satSince.IsZero() {
		c.satTime += time.Since(c.satSince)
		c.satSince = time.Time{}
	}
	c.inflight--
	if congestion {
		c.winErrors++
	} else {
		c.winBytes += bytes
		c.winReqs++
		c.winLatency += latency
	}
	c.notify()
}

// Tick evaluates the finished window; call it periodically (about every 2s).
func (c *Controller) Tick(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elapsed := now.Sub(c.winStart)
	if elapsed <= 0 {
		return
	}
	sat := c.satTime
	if !c.satSince.IsZero() {
		sat += now.Sub(c.satSince)
		c.satSince = now
	}
	tput := float64(c.winBytes) / elapsed.Seconds()
	c.lastTput = tput
	var avgLat time.Duration
	if c.winReqs > 0 {
		avgLat = c.winLatency / time.Duration(c.winReqs)
		if c.minLatency == 0 || avgLat < c.minLatency {
			c.minLatency = avgLat
		}
	}
	errors := c.winErrors
	saturated := sat >= elapsed/2
	c.winStart, c.winBytes, c.winReqs, c.winLatency, c.winErrors, c.satTime = now, 0, 0, 0, 0, 0

	if c.manual > 0 {
		c.limit = c.manual
		return
	}
	old := c.limit
	switch {
	case errors > 0:
		c.ssthresh = max(1, int(math.Floor(float64(c.limit)*0.7)))
		c.limit = c.ssthresh
		c.slowStart = false
		c.holdUntil = now.Add(10 * time.Second)
	case !saturated || now.Before(c.holdUntil):
		// no evidence: keep the limit
	case c.minLatency > 0 && avgLat > 4*c.minLatency && tput <= c.prevTput*1.02 && c.limit > 1:
		// latency inflation without throughput gain: back off gently
		c.limit--
		c.slowStart = false
	case c.prevTput == 0 || tput >= c.prevTput*1.05:
		c.prevTput, c.prevLimit = tput, c.limit
		if c.slowStart && c.limit < c.ssthresh {
			c.limit *= 2
		} else {
			c.slowStart = false
			c.limit += max(1, c.limit/8)
		}
	case tput < c.prevTput*0.9 && c.limit > c.prevLimit && c.prevLimit > 0:
		// the last increase hurt: restore and hold
		c.limit = c.prevLimit
		c.slowStart = false
		c.holdUntil = now.Add(30 * time.Second)
	default:
		// plateau: hold, then probe again later
		c.slowStart = false
		c.prevTput = max(c.prevTput*0.98, tput)
		c.holdUntil = now.Add(20 * time.Second)
	}
	// Probing above the governor's ceiling would only measure a limit that
	// is not used.
	hi := c.max
	if c.ceiling > 0 {
		hi = min(hi, c.ceiling)
	}
	c.limit = clamp(c.limit, 1, hi)
	if c.limit != old {
		c.notify()
	}
}
