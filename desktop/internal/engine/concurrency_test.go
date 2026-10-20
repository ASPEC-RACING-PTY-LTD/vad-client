package engine

import (
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
)

// The controller's behaviour is tested in internal/transfer; this checks the
// engine routes its settings to the shared transfer core.
func TestApplySettingsReachesTransferCore(t *testing.T) {
	e := New(nil, nil, "test")
	if e.ctrl != e.xfer.Controller() {
		t.Fatal("engine controller is not the transfer core's controller")
	}
	// Only a server administrator may fix the number of transfers.
	fixed := config.Settings{ConcurrencyMode: "manual", ManualParallel: 7}
	e.ApplySettings(fixed)
	if got := e.xfer.Limit(); got == 7 || e.manual.Load() {
		t.Fatalf("a fixed limit applied for a non-administrator: %d", got)
	}
	e.mu.Lock()
	e.admin = true
	e.mu.Unlock()
	e.ApplySettings(fixed)
	if got := e.xfer.Limit(); got != 7 {
		t.Fatalf("administrator's fixed limit not applied: %d", got)
	}
	// Automatic mode is bounded by the governor's ceiling.
	e.ApplySettings(config.Settings{})
	e.ctrl.SetCeiling(3)
	if got := e.ctrl.Limit(); got > 3 {
		t.Fatalf("ceiling not applied: %d", got)
	}
}
