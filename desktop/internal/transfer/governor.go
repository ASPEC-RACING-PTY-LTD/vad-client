package transfer

import (
	"sync"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/sysload"
)

// Governor fits automatic transfers to the computer. The network controller
// finds how many parallel requests the connection and server reward; the
// governor sets the ceiling it may use, from how busy the machine is:
//
//   - the ceiling for this hardware grows with its processors and memory, so
//     a powerful machine runs up to the absolute maximum of transfers and a
//     small one only a few;
//   - transfers keep about a quarter of the processor, memory and disk
//     spare: the ceiling shrinks while the busiest of them is above 75% and
//     grows again below 65%;
//   - while a full-screen program (a game) is in front, or the graphics
//     card is busy (games, AI), transfers keep to about 30% of the hardware
//     ceiling and keep 30% spare;
//   - when other programs leave almost nothing spare (95% processor or
//     memory, 85% while gaming), no new file starts until it eases.
type Governor struct {
	mu      sync.Mutex
	ceiling float64
	held    bool
	last    GovernorState
}

// GovernorState describes the latest decision.
type GovernorState struct {
	Ceiling  int     `json:"ceiling"`  // parallel transfers allowed now
	Hardware int     `json:"hardware"` // most this machine ever gets
	Held     bool    `json:"held"`     // no new file starts (the machine is busy)
	Heavy    bool    `json:"heavy"`    // a game or other graphics work is running
	Load     float64 `json:"load"`     // busiest of processor, memory and disk (0..1)
	Reason   string  `json:"reason,omitempty"`
}

// HardwareCeiling is the most parallel transfers a machine gets: four per
// logical processor, fewer with little memory.
func HardwareCeiling(cpus int, ram uint64) int {
	n := cpus * 4
	switch gb := ram >> 30; {
	case ram == 0:
	case gb < 8:
		n = min(n, 8)
	case gb < 16:
		n = min(n, 24)
	case gb < 32:
		n = min(n, 64)
	}
	return max(4, min(n, AbsoluteMax))
}

// Update takes a new sample and returns the decision.
func (g *Governor) Update(s sysload.Sample) GovernorState {
	g.mu.Lock()
	defer g.mu.Unlock()
	hw := HardwareCeiling(s.CPUs, s.RAMBytes)
	heavy := s.FullScreen || s.GPU >= 0.5
	limit, target, hold := float64(hw), 0.75, 0.95
	if heavy {
		limit, target, hold = max(2, float64(hw)*0.3), 0.70, 0.85
	}
	if g.ceiling == 0 {
		g.ceiling = max(2, float64(hw)/4)
	}
	load := max(0, s.CPU, s.Memory, s.Disk)
	// What other programs use: transfers can give back their own share.
	others := max(0, s.Memory)
	if s.CPU >= 0 {
		others = max(others, s.CPU-max(0, s.OwnCPU))
	}
	st := GovernorState{Hardware: hw, Heavy: heavy, Load: load}
	switch {
	case others >= hold || (g.held && others >= hold-0.10):
		g.held = true
		st.Reason = "your computer is busy; transfers wait until it has room"
	default:
		g.held = false
	}
	switch {
	case load > target+0.05:
		g.ceiling = max(1, g.ceiling*0.75)
	case load < target-0.10:
		g.ceiling += max(1, g.ceiling/4)
	}
	g.ceiling = max(1, min(g.ceiling, limit))
	st.Ceiling, st.Held = int(g.ceiling), g.held
	if heavy && st.Reason == "" {
		st.Reason = "a game or other graphics work is running; transfers use less of the computer"
	}
	g.last = st
	return st
}

// State returns the latest decision.
func (g *Governor) State() GovernorState {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.last
}
