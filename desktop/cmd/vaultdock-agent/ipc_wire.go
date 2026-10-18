package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/agent"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/ipc"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/ipc/caller"
)

// pipeName is the pipe the agent serves on (a variable for tests).
var pipeName = ipc.PipeName

func init() { ServeIPC = serveIPC }

// serveIPC serves api on the named pipe with the §7.6 policy until ctx is
// cancelled. It returns only after the IPC server has closed every
// connection, so agentHandler.Stop (which waits for this) closes the IPC
// server before the host.
func serveIPC(ctx context.Context, api agent.API, log *slog.Logger) error {
	l, err := ipc.Listen(pipeName)
	if err != nil {
		return err
	}
	srv := ipc.NewServer(api, ipc.ServerOptions{Log: log, Authorize: authorizeLogged(log)})
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(l) }()
	log.Info("IPC server listening", "pipe", pipeName)

	select {
	case <-ctx.Done():
		_ = srv.Close()
		<-errc
		log.Info("IPC server stopped")
		return nil
	case err := <-errc:
		_ = srv.Close()
		if errors.Is(err, ipc.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// authorizeLogged applies agent.Authorize, logs the caller SID at debug
// level for every call and logs refusals at warn level.
func authorizeLogged(log *slog.Logger) func(context.Context, caller.Caller, string) error {
	return func(ctx context.Context, c caller.Caller, method string) error {
		err := agent.Authorize(ctx, c, method)
		if err != nil {
			log.Warn("ipc call refused", "sid", c.SID, "session", c.SessionID, "pid", c.PID, "method", method, "admin", c.Admin, "elevated", c.Elevated)
			return err
		}
		log.Debug("ipc call", "sid", c.SID, "session", c.SessionID, "method", method)
		return nil
	}
}
