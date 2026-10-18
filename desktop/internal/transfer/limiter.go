package transfer

import (
	"context"
	"io"
	"sync"

	"golang.org/x/time/rate"
)

// Limiter applies an optional upload bandwidth limit shared by every body it
// wraps. The zero value is unlimited and ready to use.
type Limiter struct {
	mu  sync.Mutex
	lim *rate.Limiter
}

// SetKBps sets the limit in KiB per second; 0 or less removes it.
func (l *Limiter) SetKBps(kbps int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if kbps > 0 {
		l.lim = rate.NewLimiter(rate.Limit(kbps*1024), max(256<<10, kbps*1024))
	} else {
		l.lim = nil
	}
}

func (l *Limiter) current() *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lim
}

// Wrap returns r throttled by the limiter. The limit is read on every Read,
// so changes apply to bodies already in flight.
func (l *Limiter) Wrap(r io.Reader) io.Reader {
	return &limitedReader{r: r, l: l}
}

type limitedReader struct {
	r io.Reader
	l *Limiter
}

func (l *limitedReader) Read(p []byte) (int, error) {
	lim := l.l.current()
	if lim != nil && len(p) > 64<<10 {
		p = p[:64<<10]
	}
	n, err := l.r.Read(p)
	if n > 0 && lim != nil {
		_ = lim.WaitN(context.Background(), min(n, lim.Burst()))
	}
	return n, err
}
