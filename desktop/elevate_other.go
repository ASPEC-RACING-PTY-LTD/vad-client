//go:build !windows

package main

import "errors"

func currentElevation() Elevation { return Elevation{} }

func relaunchElevated() error {
	return errors.New("restarting as administrator is only supported on Windows")
}

func waitForParent([]string) {}

func runElevated(string, string) error {
	return errors.New("starting the service is only supported on Windows")
}
