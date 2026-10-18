package transfer

import (
	"testing"
	"time"
)

// simulate drives the controller against a model link: each request moves
// perReq bytes per second until the aggregate exceeds capacity, and requests
// beyond errAbove parallel connections fail with congestion.
func simulate(c *Controller, capacity, perReq float64, errAbove int, windows int) []int {
	now := time.Unix(0, 0)
	c.winStart = now
	var limits []int
	for w := 0; w < windows; w++ {
		l := c.Limit()
		c.mu.Lock()
		c.inflight = l
		c.satSince = now
		c.mu.Unlock()
		tput := min(float64(l)*perReq, capacity)
		lat := time.Duration(float64(time.Second) * float64(l) * perReq / tput)
		bytes := int64(tput * 2)
		reqs := max(1, int(bytes/(8<<20)))
		for i := 0; i < reqs; i++ {
			c.mu.Lock()
			c.winBytes += bytes / int64(reqs)
			c.winReqs++
			c.winLatency += lat
			c.mu.Unlock()
		}
		if errAbove > 0 && l > errAbove {
			c.mu.Lock()
			c.winErrors += l - errAbove
			c.mu.Unlock()
		}
		now = now.Add(2 * time.Second)
		c.Tick(now)
		limits = append(limits, c.Limit())
	}
	return limits
}

func TestControllerClimbsOnFastLink(t *testing.T) {
	// 100 MB/s link, one request alone reaches 2 MB/s (high latency, many
	// small files): the controller should open many parallel requests.
	c := NewController(64)
	l := simulate(c, 100<<20, 2<<20, 0, 60)
	final := l[len(l)-1]
	if final < 32 {
		t.Fatalf("expected the limit to climb towards 50, got %d (%v)", final, l)
	}
}

func TestControllerStaysLowOnSlowLink(t *testing.T) {
	// One request already saturates a 5 MB/s uplink: more connections do
	// not help, so the controller must not keep growing.
	c := NewController(64)
	l := simulate(c, 5<<20, 5<<20, 0, 60)
	final := l[len(l)-1]
	if final > 6 {
		t.Fatalf("limit grew without throughput gain: %d (%v)", final, l)
	}
}

func TestControllerBacksOffOnErrors(t *testing.T) {
	// The server starts refusing above 10 parallel requests.
	c := NewController(64)
	l := simulate(c, 1<<30, 4<<20, 10, 80)
	for _, v := range l[len(l)-20:] {
		if v > 14 {
			t.Fatalf("limit stays above the congestion point: %v", l)
		}
	}
}

func TestManualOverride(t *testing.T) {
	c := NewController(64)
	c.SetBounds(64, 7)
	simulate(c, 1<<30, 1<<20, 0, 10)
	if c.Limit() != 7 {
		t.Fatalf("manual limit not respected: %d", c.Limit())
	}
	c.SetBounds(3, 0)
	if c.Limit() > 3 {
		t.Fatalf("max not respected: %d", c.Limit())
	}
}
