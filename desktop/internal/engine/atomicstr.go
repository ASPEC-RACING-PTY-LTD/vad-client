package engine

import "sync/atomic"

// atomicString is a string read and swapped without the engine lock.
type atomicString struct{ v atomic.Value }

func (a *atomicString) Load() string {
	s, _ := a.v.Load().(string)
	return s
}

func (a *atomicString) Swap(s string) string {
	old, _ := a.v.Swap(s).(string)
	return old
}
