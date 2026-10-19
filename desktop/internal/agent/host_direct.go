package agent

import (
	"context"
	"errors"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// Direct (LAN) uploads: while Settings.DirectUpload is on, upload traffic
// goes to Settings.DirectURL once that address is proved to reach the same
// server (client.VerifyDirect). The check repeats every directRecheck, right
// away after a setting change or connect, and soon after the address
// stopped answering; meanwhile uploads use the public URL. Each check also
// reads the server's reset generation, so an administrator can turn the
// setting off from the admin panel.

var (
	directRecheck   = 5 * time.Minute
	directAfterLoss = time.Minute
)

func (h *Host) kickDirect() {
	select {
	case h.directKick <- struct{}{}:
	default:
	}
}

// attachDirect prepares a new client for direct uploads and checks the
// route.
func (h *Host) attachDirect(c *client.Client) {
	c.SetOnDirectLost(func(err error) {
		h.log.Warn("server's local address stopped answering; uploads use the public address", "err", err)
		h.eng.SetDirect(engine.DirectView{Enabled: true, Message: "Local address not reachable; using the public address"})
		go func() {
			select {
			case <-h.ctx.Done():
			case <-time.After(directAfterLoss):
				h.kickDirect()
			}
		}()
	})
	h.kickDirect()
}

func (h *Host) directLoop() {
	t := time.NewTimer(directRecheck)
	defer t.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-h.directKick:
		case <-t.C:
		}
		h.checkDirect()
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		t.Reset(directRecheck)
	}
}

// checkDirect verifies the configured local address and switches the
// client's upload route accordingly.
func (h *Host) checkDirect() {
	h.mu.Lock()
	s, c, me := h.settings, h.client, h.me
	h.mu.Unlock()
	if c == nil {
		return
	}
	view := engine.DirectView{Enabled: s.DirectUpload, URL: s.DirectURL}
	defer func() { h.eng.SetDirect(view) }()
	if !s.DirectUpload {
		c.SetDirect(nil)
		return
	}
	if me != nil && !me.HasFeature(wire.FeatureDirectUpload) {
		c.SetDirect(nil)
		view.Message = "The server does not support direct uploads yet; update it"
		return
	}
	base, err := client.NormalizeDirectURL(s.DirectURL)
	if err != nil {
		c.SetDirect(nil)
		view.Message = err.Error()
		return
	}
	ctx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
	defer cancel()
	probe, err := c.VerifyDirect(ctx, base)
	if probe != nil && h.directWasReset(probe.ResetGen) {
		c.SetDirect(nil)
		view = engine.DirectView{Message: "Turned off by an administrator"}
		return
	}
	if err != nil {
		c.SetDirect(nil)
		var ae *client.APIError
		if errors.As(err, &ae) && probe == nil {
			view.Message = "Could not check the local address: " + ae.Message
		} else {
			view.Message = err.Error() + "; using the public address"
		}
		return
	}
	if cur := c.Direct(); cur == nil || cur.String() != base.String() {
		h.log.Info("uploads go directly to the server's local address", "address", base.String())
	}
	c.SetDirect(base)
	view.Active = true
	view.Message = "Uploads go directly to " + base.Host
}

// directWasReset records the server's reset generation the first time
// after direct uploads were turned on, and turns them off when an
// administrator raised it since. It reports whether they were turned off.
func (h *Host) directWasReset(gen int64) bool {
	h.mu.Lock()
	switch {
	case h.settings.DirectResetGen < 0 || (h.settings.DirectResetGen == 0 && gen == 0):
		if h.settings.DirectResetGen != gen {
			h.settings.DirectResetGen = gen
			_ = h.saveSettingsLocked()
		}
		h.mu.Unlock()
		return false
	case gen <= h.settings.DirectResetGen:
		h.mu.Unlock()
		return false
	}
	h.settings.DirectUpload = false
	h.settings.DirectResetGen = gen
	err := h.saveSettingsLocked()
	s := h.settings
	h.mu.Unlock()
	if err != nil {
		h.log.Warn("could not save settings", "err", err)
	}
	h.log.Info("an administrator turned off direct uploads for this device")
	_ = h.bus.Publish(KindSettings, s)
	h.notify("Direct uploads turned off", "An administrator turned off direct LAN uploads for this device; uploads use the public address again.", "info")
	return true
}
