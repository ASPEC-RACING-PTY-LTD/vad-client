package service

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// RunInteractive runs h in the foreground until ctx is cancelled, Ctrl+C /
// SIGTERM arrives or the handler requests exit. Session changes are not
// delivered in console mode.
func RunInteractive(ctx context.Context, h Handler, opts Options) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runUntil(ctx, h, opts)
}
