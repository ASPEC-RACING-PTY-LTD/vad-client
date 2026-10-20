package sysload

import (
	"testing"
	"time"
)

func TestSample(t *testing.T) {
	s := New()
	defer s.Close()
	time.Sleep(300 * time.Millisecond)
	got := s.Sample()
	t.Logf("%+v", got)
	if got.CPUs < 1 {
		t.Fatalf("cpus = %d", got.CPUs)
	}
	for name, v := range map[string]float64{"cpu": got.CPU, "own": got.OwnCPU, "memory": got.Memory, "disk": got.Disk, "gpu": got.GPU} {
		if v != -1 && (v < 0 || v > 1) {
			t.Fatalf("%s = %v out of range", name, v)
		}
	}
}
