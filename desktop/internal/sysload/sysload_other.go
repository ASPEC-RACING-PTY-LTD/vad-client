//go:build !windows

package sysload

type noSampler struct{}

func newSampler() Sampler { return noSampler{} }

func (noSampler) Sample() Sample {
	return Sample{CPU: -1, OwnCPU: -1, Memory: -1, Disk: -1, GPU: -1, CPUs: cpus()}
}

func (noSampler) Close() {}
