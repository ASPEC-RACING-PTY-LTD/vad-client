// Package sysload samples how busy the computer is (processor, memory, disk
// and graphics, and whether a full-screen program such as a game is in
// front), so transfers can use what is spare and step back when the user
// needs the machine.
package sysload

import "runtime"

// Sample is one measurement. Fractions are 0..1; a field that cannot be
// measured on this system is -1.
type Sample struct {
	CPU        float64 // whole machine
	OwnCPU     float64 // this process, of the whole machine
	Memory     float64 // physical memory in use
	Disk       float64 // busiest disk: share of time it is working
	GPU        float64 // 3D and compute engines
	FullScreen bool    // a full-screen program (game, video, presentation) is in front
	CPUs       int     // logical processors
	RAMBytes   uint64  // installed physical memory
}

// Sampler measures load; the first Sample after New returns rates measured
// since New.
type Sampler interface {
	Sample() Sample
	Close()
}

// New returns a sampler for this system.
func New() Sampler { return newSampler() }

func cpus() int { return runtime.NumCPU() }
