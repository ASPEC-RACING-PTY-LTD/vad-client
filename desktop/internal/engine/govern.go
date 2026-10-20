package engine

import (
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/sysload"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/transfer"
)

// Parallel transfers are automatic: the network controller finds what the
// connection and server reward, under a ceiling the governor sets from how
// busy the computer is (transfer.Governor). Only a server administrator may
// choose a fixed number instead.

// applyBounds applies the stored settings to the transfer core.
func (e *Engine) applyBounds() {
	e.mu.Lock()
	s, admin := e.settings, e.admin
	e.mu.Unlock()
	manual := 0
	if admin && s.ConcurrencyMode == "manual" {
		manual = s.ManualParallel
	}
	e.manual.Store(manual > 0)
	e.xfer.ApplySettings(transfer.Settings{MaxParallel: transfer.AbsoluteMax, ManualParallel: manual, BandwidthKBps: s.BandwidthKBps})
	if manual > 0 {
		e.ctrl.SetCeiling(0)
		e.held.Store(false)
	}
}

// govern samples the computer's load and updates the ceiling (about every
// three seconds).
func (e *Engine) govern() {
	if e.manual.Load() {
		return
	}
	if e.sampler == nil {
		e.sampler = sysload.New()
		return // the first sample needs an interval
	}
	st := e.gov.Update(e.sampler.Sample())
	e.ctrl.SetCeiling(st.Ceiling)
	if was := e.held.Swap(st.Held); was && !st.Held {
		e.kick()
	}
}

// Governor returns the latest load decision (nil in fixed mode).
func (e *Engine) governorState() *transfer.GovernorState {
	if e.manual.Load() || e.sampler == nil {
		return nil
	}
	st := e.gov.State()
	return &st
}

// IsAdmin reports whether the connected account administers the server.
func (e *Engine) IsAdmin() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.admin
}
