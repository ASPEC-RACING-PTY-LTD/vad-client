//go:build !windows

package main

// windowOnScreen cannot tell on this platform; the window counts as open.
func windowOnScreen() bool { return true }
