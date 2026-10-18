//go:build !windows

package agent

// processIsSystem is false off Windows: there is no LocalSystem service.
func processIsSystem() bool { return false }
