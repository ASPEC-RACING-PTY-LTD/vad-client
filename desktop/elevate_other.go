//go:build !windows

package main

import "errors"

func currentElevation() Elevation { return Elevation{} }

func relaunchElevated() error { return errors.New("restarting as administrator is only supported on Windows") }

func waitForParent([]string) {}
