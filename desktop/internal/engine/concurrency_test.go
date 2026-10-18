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
	e.ApplySettings(config.Settings{ConcurrencyMode: "manual", ManualParallel: 7, MaxParallel: 64})
	if got := e.xfer.Limit(); got != 7 {
		t.Fatalf("manual limit not applied: %d", got)
	}
	e.ApplySettings(config.Settings{MaxParallel: 3})
	if got := e.ctrl.Limit(); got > 3 {
		t.Fatalf("max not applied: %d", got)
	}
}
