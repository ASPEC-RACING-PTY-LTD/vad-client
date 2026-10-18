// Command vaultdock-agent is the VaultDock Windows service (VaultDockAgent).
//
//	vaultdock-agent run        run under SCM, or in the console when started interactively
//	vaultdock-agent service    alias of run (the SCM binary path written by the installer)
//	vaultdock-agent install    register the service (admin)
//	vaultdock-agent configure  rewrite the registration, e.g. after a mode switch (admin)
//	vaultdock-agent uninstall  stop and delete the service (admin)
//	vaultdock-agent start      start the service (admin)
//	vaultdock-agent stop       stop the service (admin)
//	vaultdock-agent status     print the service status as JSON (no admin needed)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // backup schedules name IANA zones; Windows has no zoneinfo

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/agent"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/service"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/modules"
)

// version is set at build time with -ldflags "-X main.version=x.y.z".
var version = "0.1.0"

// updateRepo is the release repository for agent self-updates (ldflags).
var updateRepo = "ASPEC-RACING-PTY-LTD/vad-client"

// IPCServeFunc serves the agent API to UI clients until ctx is cancelled.
// It must return promptly after cancellation.
type IPCServeFunc func(ctx context.Context, api agent.API, log *slog.Logger) error

// ServeIPC is the IPC hook. It is nil until P0-28 / INT wires the named
// pipe server, typically by adding a file in this package:
//
//	func init() { ServeIPC = ipc.Serve }
//
// While nil, the agent runs headless (engine only) and logs a warning.
var ServeIPC IPCServeFunc

// OnSessionChange is the hook for session notifications (profile work,
// §7.8). nil = log only. It runs on the SCM control loop and must not block.
var OnSessionChange func(api agent.API, ev service.SessionEvent)

const usage = `usage: vaultdock-agent <command> [flags]

commands:
  run        run the agent (as a service under SCM, else in the console)
  service    alias of run
  install    register the VaultDockAgent service (admin)
  configure  rewrite the service registration and privileges (admin)
  uninstall  stop and delete the service (admin)
  start      start the service (admin)
  stop       stop the service (admin)
  status     print the service status as JSON
`

// cmdLine is the parsed command line.
type cmdLine struct {
	cmd         string
	dataDir     string
	logDir      string
	mode        modules.Mode
	extraPrivs  []string
	exe         string
	timeout     time.Duration
	autoUpdates bool
}

// adminCommands need an elevated token.
var adminCommands = map[string]bool{"install": true, "configure": true, "uninstall": true, "start": true, "stop": true}

func parseArgs(args []string, errOut io.Writer) (cmdLine, error) {
	if len(args) == 0 {
		return cmdLine{}, errors.New("missing command")
	}
	cl := cmdLine{cmd: args[0], timeout: 60 * time.Second}
	if cl.cmd == "service" {
		cl.cmd = "run"
	}
	fs := flag.NewFlagSet(cl.cmd, flag.ContinueOnError)
	fs.SetOutput(errOut)
	switch cl.cmd {
	case "run":
		fs.StringVar(&cl.dataDir, "data-dir", "", "data directory (default %ProgramData%\\VaultDock)")
		fs.StringVar(&cl.logDir, "log-dir", "", "log directory (default <data-dir>\\logs)")
		fs.BoolVar(&cl.autoUpdates, "auto-updates", false, "enable the background auto-update loop")
	case "install", "configure":
		mode := fs.String("mode", string(modules.ModeSimple), "install mode: simple or advanced")
		privs := fs.String("extra-privileges", "", "comma-separated privileges added to the mode set")
		fs.StringVar(&cl.exe, "exe", "", "service executable (default: this executable)")
		if err := fs.Parse(args[1:]); err != nil {
			return cl, err
		}
		cl.mode = modules.Mode(*mode)
		if !cl.mode.Valid() {
			return cl, fmt.Errorf("invalid -mode %q (simple or advanced)", *mode)
		}
		for _, p := range strings.Split(*privs, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cl.extraPrivs = append(cl.extraPrivs, p)
			}
		}
		if _, err := service.PrivilegesForMode(cl.mode, cl.extraPrivs...); err != nil {
			return cl, err
		}
		return cl, noArgs(fs)
	case "uninstall", "stop":
		fs.DurationVar(&cl.timeout, "timeout", cl.timeout, "how long to wait for the service to stop")
	case "start", "status":
	case "help", "-h", "-help", "--help":
		return cmdLine{cmd: "help"}, nil
	default:
		return cl, fmt.Errorf("unknown command %q", args[0])
	}
	if err := fs.Parse(args[1:]); err != nil {
		return cl, err
	}
	return cl, noArgs(fs)
}

func noArgs(fs *flag.FlagSet) error {
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return nil
}

func main() {
	engine.Version = version
	os.Exit(realMain(os.Args[1:]))
}

func realMain(args []string) int {
	cl, err := parseArgs(args, os.Stderr)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "vaultdock-agent:", err)
		}
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	if cl.cmd == "help" {
		fmt.Print(usage)
		return 0
	}
	if adminCommands[cl.cmd] && !service.IsElevated() {
		fmt.Fprintf(os.Stderr, "vaultdock-agent: %q requires administrator rights; run it from an elevated prompt\n", cl.cmd)
		return 3
	}
	if err := execute(cl); err != nil {
		fmt.Fprintln(os.Stderr, "vaultdock-agent:", err)
		return 1
	}
	return 0
}

func execute(cl cmdLine) error {
	name := service.DefaultName
	switch cl.cmd {
	case "run":
		return runAgent(cl)
	case "install", "configure":
		c, err := serviceConfig(cl)
		if err != nil {
			return err
		}
		if cl.cmd == "install" {
			err = service.Install(c)
		} else {
			err = service.Configure(c)
		}
		if err == nil {
			fmt.Printf("%s %sed (mode %s, privileges %s)\n", name, strings.TrimSuffix(cl.cmd, "e"), cl.mode, strings.Join(c.Privileges, ","))
		}
		return err
	case "uninstall":
		return service.Uninstall(name, cl.timeout)
	case "start":
		return service.StartService(name)
	case "stop":
		return service.StopService(name, cl.timeout)
	case "status":
		st, err := service.Query(name)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}
	return fmt.Errorf("unknown command %q", cl.cmd)
}

func serviceConfig(cl cmdLine) (service.Config, error) {
	privs, err := service.PrivilegesForMode(cl.mode, cl.extraPrivs...)
	if err != nil {
		return service.Config{}, err
	}
	exe := cl.exe
	if exe == "" {
		if exe, err = os.Executable(); err != nil {
			return service.Config{}, err
		}
	}
	if exe, err = filepath.Abs(exe); err != nil {
		return service.Config{}, err
	}
	return service.Config{ExePath: exe, Args: []string{"service"}, Privileges: privs}, nil
}

func runAgent(cl cmdLine) error {
	isSvc, err := service.IsWindowsService()
	if err != nil {
		return fmt.Errorf("detect service mode: %w", err)
	}
	if cl.dataDir == "" {
		cl.dataDir = service.ProgramDataDir()
	}
	if cl.logDir == "" {
		cl.logDir = filepath.Join(cl.dataDir, "logs")
	}
	if err := os.MkdirAll(cl.dataDir, 0o700); err != nil {
		return err
	}
	var extra []io.Writer
	if !isSvc {
		extra = append(extra, os.Stderr)
	}
	log, closer, err := service.OpenLog(cl.logDir, slog.LevelInfo, extra...)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer closer.Close()
	log.Info("VaultDock agent starting", "version", version, "service", isSvc, "data", cl.dataDir)

	h := newAgentHandler(log, agentOptions(cl, log, isSvc))
	opts := service.Options{Log: log, EventLog: isSvc}
	if isSvc {
		err = service.RunWithOptions(service.DefaultName, h, opts)
	} else {
		err = service.RunInteractive(context.Background(), h, opts)
	}
	if err != nil {
		log.Error("agent exited with error", "err", err)
	} else {
		log.Info("agent exited")
	}
	return err
}

// agentOptions builds the host options for "run". isSvc marks the
// machine-wide service (the host then refuses per-user update installs).
func agentOptions(cl cmdLine, log *slog.Logger, isSvc bool) agent.Options {
	o := agent.Options{
		Log:         log,
		DataDir:     cl.dataDir,
		UpdateRepo:  updateRepo,
		AutoUpdates: cl.autoUpdates,
		Service:     isSvc,
	}
	if isSvc {
		// The backup stack needs the machine keystore and ProgramData state,
		// which only the LocalSystem service may write.
		o.Backup = &agent.BackupOptions{}
	}
	return o
}

// agentHandler adapts agent.Host to service.Handler.
type agentHandler struct {
	log  *slog.Logger
	opts agent.Options

	host     *agent.Host
	exit     chan struct{}
	exitOnce sync.Once
	ipcStop  context.CancelFunc
	ipcDone  chan struct{}
}

func newAgentHandler(log *slog.Logger, opts agent.Options) *agentHandler {
	h := &agentHandler{log: log, opts: opts, exit: make(chan struct{})}
	h.opts.RequestExit = h.requestExit
	return h
}

func (h *agentHandler) requestExit() { h.exitOnce.Do(func() { close(h.exit) }) }

// ExitRequested implements service.ExitNotifier.
func (h *agentHandler) ExitRequested() <-chan struct{} { return h.exit }

func (h *agentHandler) Start(ctx context.Context) error {
	host, err := agent.NewHost(h.opts)
	if err != nil {
		return err
	}
	if err := host.Start(ctx); err != nil {
		_ = host.Close()
		return err
	}
	h.host = host
	if ServeIPC == nil {
		h.log.Warn("IPC server not wired; agent running headless")
		return nil
	}
	ictx, cancel := context.WithCancel(ctx)
	h.ipcStop, h.ipcDone = cancel, make(chan struct{})
	go func() {
		defer close(h.ipcDone)
		if err := ServeIPC(ictx, host, h.log); err != nil && ictx.Err() == nil {
			h.log.Error("IPC server stopped", "err", err)
		}
	}()
	return nil
}

func (h *agentHandler) Stop(ctx context.Context) error {
	if h.ipcStop != nil {
		h.ipcStop()
		select {
		case <-h.ipcDone:
		case <-ctx.Done():
			h.log.Warn("IPC server did not stop in time")
		}
	}
	if h.host == nil {
		return nil
	}
	return h.host.Close()
}

func (h *agentHandler) SessionChange(ev service.SessionEvent) {
	h.log.Info("session change", "event", ev.Type.String(), "session", ev.SessionID)
	if OnSessionChange != nil && h.host != nil {
		OnSessionChange(h.host, ev)
	}
}
