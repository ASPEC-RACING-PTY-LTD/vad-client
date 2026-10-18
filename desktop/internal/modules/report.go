package modules

import (
	"context"
	"errors"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// ErrNotConnected is returned by ClientSender while the device has no
// enrolled server client; RunReporter retries with backoff.
var ErrNotConnected = errors.New("modules: not connected to a server")

const (
	defaultReportInterval = 24 * time.Hour
	defaultRetryMin       = 30 * time.Second
	defaultRetryMax       = time.Hour
)

// ClientSender adapts the agent's current server client into a Sender
// (PUT /device/modules, api-v1 §6A). cl is called per report so a
// reconnect or re-enrolment is picked up; it may return nil.
func ClientSender(cl func() *client.Client) Sender {
	return func(ctx context.Context, body wire.ModuleReport) error {
		var c *client.Client
		if cl != nil {
			c = cl()
		}
		if c == nil {
			return ErrNotConnected
		}
		return c.ReportModules(ctx, body)
	}
}

// Kick asks RunReporter to send a report now (e.g. right after enrolment).
// It never blocks; without a running reporter it does nothing.
func (m *Manager) Kick() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// RunReporter sends PUT /device/modules at startup, after every change
// (changed and Kick) and every ReportInterval (default daily) until ctx is
// done. A failed report is retried with exponential backoff from RetryMin
// to RetryMax. While it runs, changes no longer report synchronously.
func (m *Manager) RunReporter(ctx context.Context) {
	if m.opts.Send == nil {
		return
	}
	if !m.reporting.CompareAndSwap(false, true) {
		m.log.Warn("module reporter already running")
		return
	}
	defer m.reporting.Store(false)

	interval, rmin, rmax := m.opts.ReportInterval, m.opts.RetryMin, m.opts.RetryMax
	if interval <= 0 {
		interval = defaultReportInterval
	}
	if rmin <= 0 {
		rmin = defaultRetryMin
	}
	if rmax < rmin {
		rmax = max(defaultRetryMax, rmin)
	}

	backoff := time.Duration(0)
	timer := time.NewTimer(0) // startup report
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-m.kick:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		next := interval
		if err := m.Report(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			if backoff == 0 {
				backoff = rmin
			} else {
				backoff = min(backoff*2, rmax)
			}
			next = backoff
			if errors.Is(err, ErrNotConnected) {
				m.log.Debug("module report deferred", "retry_in", next)
			} else {
				m.log.Warn("module report failed", "err", err, "retry_in", next)
			}
		} else {
			backoff = 0
		}
		timer.Reset(next)
	}
}
