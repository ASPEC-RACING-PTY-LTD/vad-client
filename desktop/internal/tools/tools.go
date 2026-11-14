//go:build tools

// Package tools pins modules that only build helpers use:
// build/windows/installer/build-agent.go writes the agent's Windows version
// information and manifest with winres.
package tools

import _ "github.com/tc-hib/winres"
