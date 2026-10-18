// Package service runs the VaultDock agent as a Windows service (SCM) or in
// a console, and installs, configures and removes the service registration.
// Non-Windows builds get stubs that return ErrUnsupported, except the
// console runner, which works everywhere (tests, development).
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Default service identity (matches the installer spike, P0-S5).
const (
	DefaultName        = "VaultDockAgent"
	DefaultDisplayName = "VaultDock Agent"
	DefaultDescription = "VaultDock backup and sync agent."
)

// DefaultStopTimeout bounds Handler.Stop when Options.StopTimeout is zero.
const DefaultStopTimeout = 30 * time.Second

// ErrUnsupported is returned by service functions on non-Windows platforms.
var ErrUnsupported = errors.New("windows services are not supported on this platform")

// ErrNotInstalled is returned when the service is not registered with SCM.
var ErrNotInstalled = errors.New("service is not installed")

// Handler is the agent's lifecycle as seen by the service runner.
type Handler interface {
	// Start brings the agent up. ctx is cancelled after Stop returns (or
	// times out). A non-nil error stops the service with a failure exit
	// code, which triggers the SCM recovery actions.
	Start(ctx context.Context) error
	// Stop shuts the agent down. ctx carries the stop timeout.
	Stop(ctx context.Context) error
	// SessionChange is called for SERVICE_CONTROL_SESSIONCHANGE. It runs on
	// the control loop and must not block.
	SessionChange(ev SessionEvent)
}

// ExitNotifier is optionally implemented by a Handler that can ask to be
// stopped (for example agent.Options.RequestExit before an update). When
// the channel is closed, the runner performs a normal stop.
type ExitNotifier interface {
	ExitRequested() <-chan struct{}
}

// Options tunes Run/RunWithOptions and RunInteractive.
type Options struct {
	Log         *slog.Logger  // default slog.Default()
	StopTimeout time.Duration // default DefaultStopTimeout
	// EventLog writes start/stop/failure entries to the Windows Application
	// event log under the service name (source registered by Install).
	EventLog bool
}

func (o Options) withDefaults() Options {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.StopTimeout <= 0 {
		o.StopTimeout = DefaultStopTimeout
	}
	return o
}

// Run runs h under the service control manager with default options and
// event logging. It returns when the service stops.
func Run(name string, h Handler) error {
	return RunWithOptions(name, h, Options{EventLog: true})
}

// SessionEventType is the WTS_* reason code of a session change.
type SessionEventType uint32

// Session change reasons (WTS_* values from winuser.h).
const (
	ConsoleConnect       SessionEventType = 0x1
	ConsoleDisconnect    SessionEventType = 0x2
	RemoteConnect        SessionEventType = 0x3
	RemoteDisconnect     SessionEventType = 0x4
	SessionLogon         SessionEventType = 0x5
	SessionLogoff        SessionEventType = 0x6
	SessionLock          SessionEventType = 0x7
	SessionUnlock        SessionEventType = 0x8
	SessionRemoteControl SessionEventType = 0x9
	SessionCreate        SessionEventType = 0xa
	SessionTerminate     SessionEventType = 0xb
)

var sessionEventNames = map[SessionEventType]string{
	ConsoleConnect:       "console_connect",
	ConsoleDisconnect:    "console_disconnect",
	RemoteConnect:        "remote_connect",
	RemoteDisconnect:     "remote_disconnect",
	SessionLogon:         "logon",
	SessionLogoff:        "logoff",
	SessionLock:          "lock",
	SessionUnlock:        "unlock",
	SessionRemoteControl: "remote_control",
	SessionCreate:        "create",
	SessionTerminate:     "terminate",
}

func (t SessionEventType) String() string {
	if s, ok := sessionEventNames[t]; ok {
		return s
	}
	return fmt.Sprintf("session_event_%d", uint32(t))
}

// SessionEvent is a session change notification.
type SessionEvent struct {
	Type      SessionEventType
	SessionID uint32
}

// runUntil starts h, waits for ctx or an exit request, then stops h within
// the timeout. It is the console runner's core and is platform independent.
func runUntil(ctx context.Context, h Handler, opts Options) error {
	opts = opts.withDefaults()
	hctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.Start(hctx); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	opts.Log.Info("agent running (console)")
	var exit <-chan struct{}
	if e, ok := h.(ExitNotifier); ok {
		exit = e.ExitRequested()
	}
	select {
	case <-ctx.Done():
	case <-exit:
		opts.Log.Info("agent requested exit")
	}
	return stopWithTimeout(h, opts)
}

// stopWithTimeout calls h.Stop with opts.StopTimeout and returns an error if
// Stop fails or does not return in time.
func stopWithTimeout(h Handler, opts Options) error {
	sctx, cancel := context.WithTimeout(context.Background(), opts.StopTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.Stop(sctx) }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("stop: %w", err)
		}
		return nil
	case <-sctx.Done():
		return fmt.Errorf("stop: timed out after %s", opts.StopTimeout)
	}
}
