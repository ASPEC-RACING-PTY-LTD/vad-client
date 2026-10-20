package transfer

import (
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/sysload"
)

func TestHardwareCeiling(t *testing.T) {
	for _, c := range []struct {
		cpus int
		ram  uint64
		want int
	}{
		{32, 64 << 30, AbsoluteMax},
		{24, 32 << 30, 96},
		{8, 16 << 30, 32},
		{8, 8 << 30, 24},
		{4, 4 << 30, 8},
		{1, 0, 4},
	} {
		if got := HardwareCeiling(c.cpus, c.ram); got != c.want {
			t.Errorf("HardwareCeiling(%d, %d GB) = %d, want %d", c.cpus, c.ram>>30, got, c.want)
		}
	}
}

func TestGovernor(t *testing.T) {
	idle := sysload.Sample{CPU: 0.2, OwnCPU: 0.05, Memory: 0.4, Disk: 0.3, GPU: 0.05, CPUs: 32, RAMBytes: 64 << 30}
	var g Governor
	var st GovernorState
	for range 30 {
		st = g.Update(idle)
	}
	if st.Ceiling != AbsoluteMax || st.Held || st.Heavy {
		t.Fatalf("idle powerful machine: %+v", st)
	}

	// A game: at most 30% of the hardware ceiling.
	game := idle
	game.FullScreen, game.GPU = true, 0.9
	for range 30 {
		st = g.Update(game)
	}
	if !st.Heavy || st.Ceiling > AbsoluteMax*3/10 || st.Ceiling < 2 {
		t.Fatalf("gaming: %+v", st)
	}

	// Disk busy above the target: the ceiling backs off.
	busy := idle
	busy.Disk = 0.95
	before := g.Update(idle).Ceiling
	if st = g.Update(busy); st.Ceiling >= before {
		t.Fatalf("busy disk did not lower the ceiling: %d -> %d", before, st.Ceiling)
	}

	// Other programs use almost all memory: new files wait, and keep
	// waiting until it eases clearly.
	full := idle
	full.Memory = 0.97
	if st = g.Update(full); !st.Held {
		t.Fatalf("full memory: %+v", st)
	}
	full.Memory = 0.9
	if st = g.Update(full); !st.Held {
		t.Fatal("released too early")
	}
	if st = g.Update(idle); st.Held {
		t.Fatal("still held when idle")
	}

	// Own processor use does not count as other programs.
	mine := idle
	mine.CPU, mine.OwnCPU = 0.97, 0.6
	if st = g.Update(mine); st.Held {
		t.Fatalf("held because of its own load: %+v", st)
	}
}
