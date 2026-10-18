//go:build windows

package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
)

// Event log ids written by the runner.
const (
	eventStarted     = 1
	eventStopped     = 2
	eventStartFailed = 3
	eventStopFailed  = 4
)

// Service-specific exit codes reported to SCM (non-zero = failure, which
// triggers the recovery actions because Install sets the failure flag).
const (
	exitStartFailed = 1
	exitStopFailed  = 2
)

const (
	startWaitHint = 60 * time.Second
	pendingTick   = 2 * time.Second
)

func programData() string {
	p, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil || p == "" {
		return `C:\ProgramData`
	}
	return p
}

// IsWindowsService reports whether the process was started by SCM.
func IsWindowsService() (bool, error) { return svc.IsWindowsService() }

// IsElevated reports whether the process token is elevated (admin).
func IsElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

// RunWithOptions runs h under SCM until the service is stopped.
func RunWithOptions(name string, h Handler, opts Options) error {
	opts = opts.withDefaults()
	w := &winHandler{name: name, h: h, opts: opts, log: opts.Log}
	if opts.EventLog {
		if el, err := eventlog.Open(name); err == nil {
			w.elog = el
			defer el.Close()
		} else {
			opts.Log.Warn("event log unavailable", "err", err)
		}
	}
	if err := svc.Run(name, w); err != nil {
		return fmt.Errorf("service %s: %w", name, err)
	}
	return w.err
}

type winHandler struct {
	name string
	h    Handler
	opts Options
	log  *slog.Logger
	elog *eventlog.Log
	err  error
}

func (w *winHandler) event(id uint32, isErr bool, msg string) {
	if w.elog == nil {
		return
	}
	if isErr {
		_ = w.elog.Error(id, msg)
	} else {
		_ = w.elog.Info(id, msg)
	}
}

func (w *winHandler) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	s <- svc.Status{State: svc.StartPending, WaitHint: uint32(startWaitHint / time.Millisecond)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.h.Start(ctx); err != nil {
		w.err = fmt.Errorf("start: %w", err)
		w.log.Error("service start failed", "err", err)
		w.event(eventStartFailed, true, w.name+" failed to start: "+err.Error())
		return true, exitStartFailed
	}
	const accepts = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPreShutdown | svc.AcceptSessionChange
	s <- svc.Status{State: svc.Running, Accepts: accepts}
	w.log.Info("service running", "name", w.name)
	w.event(eventStarted, false, w.name+" started.")

	var exit <-chan struct{}
	if e, ok := w.h.(ExitNotifier); ok {
		exit = e.ExitRequested()
	}
	reason := ""
	for reason == "" {
		select {
		case <-exit:
			reason = "exit requested"
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				s <- c.CurrentStatus
			case svc.Stop:
				reason = "stop"
			case svc.Shutdown, svc.PreShutdown:
				reason = "shutdown"
			case svc.SessionChange:
				w.h.SessionChange(sessionEvent(c))
			default:
				w.log.Warn("unexpected service control", "cmd", uint32(c.Cmd))
			}
		}
	}
	w.log.Info("service stopping", "reason", reason)
	if err := w.stop(r, s); err != nil {
		w.err = err
		w.log.Error("service stop failed", "err", err)
		w.event(eventStopFailed, true, w.name+" did not stop cleanly: "+err.Error())
		return true, exitStopFailed
	}
	w.log.Info("service stopped")
	w.event(eventStopped, false, w.name+" stopped ("+reason+").")
	return false, 0
}

// stop runs Handler.Stop with the timeout while reporting STOP_PENDING with
// an increasing checkpoint, so SCM does not consider the service hung.
func (w *winHandler) stop(r <-chan svc.ChangeRequest, s chan<- svc.Status) error {
	hint := uint32((w.opts.StopTimeout + 5*time.Second) / time.Millisecond)
	cp := uint32(1)
	pending := svc.Status{State: svc.StopPending, WaitHint: hint, CheckPoint: cp}
	s <- pending
	done := make(chan error, 1)
	go func() { done <- stopWithTimeout(w.h, w.opts) }()
	tick := time.NewTicker(pendingTick)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-tick.C:
			cp++
			pending.CheckPoint = cp
			s <- pending
		case c := <-r:
			if c.Cmd == svc.Interrogate {
				s <- pending
			}
		}
	}
}

// sessionEvent decodes SERVICE_CONTROL_SESSIONCHANGE: EventType is the WTS_*
// reason, EventData points to a WTSSESSION_NOTIFICATION owned by SCM for
// the duration of the callback.
func sessionEvent(c svc.ChangeRequest) SessionEvent {
	ev := SessionEvent{Type: SessionEventType(c.EventType)}
	if c.EventData != 0 {
		// Reinterpret the uintptr as a pointer without a uintptr→Pointer
		// conversion (keeps go vet's unsafeptr check quiet).
		n := *(**windows.WTSSESSION_NOTIFICATION)(unsafe.Pointer(&c.EventData))
		ev.SessionID = n.SessionID
	}
	return ev
}
