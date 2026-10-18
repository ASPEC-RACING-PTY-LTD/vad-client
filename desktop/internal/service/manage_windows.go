//go:build windows

package service

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

// serviceRequiredPrivilegesInfo is SERVICE_REQUIRED_PRIVILEGES_INFOW
// (not defined by x/sys): a MULTI_SZ of privilege names.
type serviceRequiredPrivilegesInfo struct {
	RequiredPrivileges *uint16
}

// servicePreshutdownInfo is SERVICE_PRESHUTDOWN_INFO (milliseconds).
type servicePreshutdownInfo struct {
	PreshutdownTimeout uint32
}

func (c Config) validate() error {
	if c.ExePath == "" || !filepath.IsAbs(c.ExePath) {
		return fmt.Errorf("service executable path must be absolute: %q", c.ExePath)
	}
	if len(c.Privileges) == 0 {
		return errors.New("service privilege allowlist is empty")
	}
	_, err := NormalizePrivileges(c.Privileges)
	return err
}

func (c Config) mgrConfig() mgr.Config {
	return mgr.Config{
		ServiceType:      windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:        mgr.StartAutomatic,
		ErrorControl:     mgr.ErrorNormal,
		ServiceStartName: "LocalSystem",
		DisplayName:      c.DisplayName,
		Description:      c.Description,
		SidType:          windows.SERVICE_SID_TYPE_UNRESTRICTED,
		DelayedAutoStart: true,
	}
}

// binaryPath quotes the executable and escapes the arguments the same way
// mgr.CreateService does, so there is no unquoted-path issue.
func (c Config) binaryPath() string {
	var b strings.Builder
	b.WriteString(`"` + c.ExePath + `"`)
	for _, a := range c.Args {
		b.WriteString(" " + syscall.EscapeArg(a))
	}
	return b.String()
}

// Install registers the service, or reconfigures it when it already exists
// (upgrade). Requires elevation. It does not start the service.
func Install(c Config) error {
	c = c.withDefaults()
	if err := c.validate(); err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()
	s, err := m.CreateService(c.Name, c.ExePath, c.mgrConfig(), c.Args...)
	if errors.Is(err, windows.ERROR_SERVICE_EXISTS) {
		s, err = m.OpenService(c.Name)
		if err == nil {
			err = s.UpdateConfig(withBinary(c))
		}
	}
	if err != nil {
		return fmt.Errorf("create service %s: %w", c.Name, err)
	}
	defer s.Close()
	if err := applyExtras(s, c); err != nil {
		return err
	}
	// Event log source for start/stop entries; an existing source is fine.
	if err := eventlog.InstallAsEventCreate(c.Name, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("register event log source: %w", err)
	}
	return nil
}

func withBinary(c Config) mgr.Config {
	mc := c.mgrConfig()
	mc.BinaryPathName = c.binaryPath()
	return mc
}

// Configure rewrites the registration of an installed service (binary path,
// start type, account, SID type, privileges, recovery). Used on upgrade and
// mode switch; the new privileges apply after the service restarts.
func Configure(c Config) error {
	c = c.withDefaults()
	if err := c.validate(); err != nil {
		return err
	}
	m, s, err := openService(c.Name)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	if err := s.UpdateConfig(withBinary(c)); err != nil {
		return fmt.Errorf("update service config: %w", err)
	}
	return applyExtras(s, c)
}

// SetRequiredPrivileges replaces only the RequiredPrivileges allowlist.
func SetRequiredPrivileges(name string, privs []string) error {
	m, s, err := openService(name)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	return setRequiredPrivileges(s.Handle, privs)
}

func applyExtras(s *mgr.Service, c Config) error {
	if err := setRequiredPrivileges(s.Handle, c.Privileges); err != nil {
		return err
	}
	actions := make([]mgr.RecoveryAction, len(RecoveryDelays))
	for i, d := range RecoveryDelays {
		actions[i] = mgr.RecoveryAction{Type: mgr.ServiceRestart, Delay: d}
	}
	if err := s.SetRecoveryActions(actions, uint32(RecoveryResetPeriod/time.Second)); err != nil {
		return fmt.Errorf("set recovery actions: %w", err)
	}
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return fmt.Errorf("set recovery failure flag: %w", err)
	}
	pre := servicePreshutdownInfo{PreshutdownTimeout: uint32(c.PreshutdownTimeout / time.Millisecond)}
	if err := windows.ChangeServiceConfig2(s.Handle, windows.SERVICE_CONFIG_PRESHUTDOWN_INFO, (*byte)(unsafe.Pointer(&pre))); err != nil {
		return fmt.Errorf("set preshutdown timeout: %w", err)
	}
	return nil
}

func setRequiredPrivileges(h windows.Handle, privs []string) error {
	privs, err := NormalizePrivileges(privs)
	if err != nil {
		return err
	}
	ms, err := multiSZ(privs)
	if err != nil {
		return err
	}
	info := serviceRequiredPrivilegesInfo{RequiredPrivileges: &ms[0]}
	if err := windows.ChangeServiceConfig2(h, windows.SERVICE_CONFIG_REQUIRED_PRIVILEGES_INFO, (*byte)(unsafe.Pointer(&info))); err != nil {
		return fmt.Errorf("set required privileges: %w", err)
	}
	return nil
}

func multiSZ(ss []string) ([]uint16, error) {
	var out []uint16
	for _, s := range ss {
		u, err := windows.UTF16FromString(s) // includes the terminating NUL
		if err != nil {
			return nil, err
		}
		out = append(out, u...)
	}
	return append(out, 0), nil
}

func openService(name string) (*mgr.Mgr, *mgr.Service, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, nil, fmt.Errorf("connect to service manager: %w", err)
	}
	s, err := m.OpenService(name)
	if err != nil {
		m.Disconnect()
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return nil, nil, ErrNotInstalled
		}
		return nil, nil, fmt.Errorf("open service %s: %w", name, err)
	}
	return m, s, nil
}

// Uninstall stops the service (waiting up to timeout), deletes it and
// removes its event log source. Requires elevation.
func Uninstall(name string, timeout time.Duration) error {
	m, s, err := openService(name)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	if err := stopAndWait(s, timeout); err != nil {
		return err
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	_ = eventlog.Remove(name)
	return nil
}

// StartService starts the installed service. Already running is not an error.
func StartService(name string) error {
	m, s, err := openService(name)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return fmt.Errorf("start service: %w", err)
	}
	return nil
}

// StopService stops the service and waits up to timeout for STOPPED.
func StopService(name string, timeout time.Duration) error {
	m, s, err := openService(name)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	return stopAndWait(s, timeout)
}

func stopAndWait(s *mgr.Service, timeout time.Duration) error {
	st, err := s.Query()
	if err != nil {
		return fmt.Errorf("query service: %w", err)
	}
	if st.State == svc.Stopped {
		return nil
	}
	if st.State != svc.StopPending {
		if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return fmt.Errorf("stop service: %w", err)
		}
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st, err = s.Query()
		if err != nil {
			return fmt.Errorf("query service: %w", err)
		}
		if st.State == svc.Stopped {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("service did not stop within %s", timeout)
}

// Query reports the service status and configuration. It opens SCM with
// query-only rights, so it works without elevation.
func Query(name string) (Status, error) {
	st := Status{Name: name}
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return st, fmt.Errorf("connect to service manager: %w", err)
	}
	defer windows.CloseServiceHandle(scm)
	np, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return st, err
	}
	h, err := windows.OpenService(scm, np, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return st, nil
		}
		return st, fmt.Errorf("open service %s: %w", name, err)
	}
	s := &mgr.Service{Name: name, Handle: h}
	defer s.Close()
	st.Installed = true
	ss, err := s.Query()
	if err != nil {
		return st, fmt.Errorf("query service: %w", err)
	}
	st.State, st.PID = stateName(ss.State), ss.ProcessId
	var errs []string
	if cfg, err := s.Config(); err == nil {
		st.StartType = startTypeName(cfg.StartType, cfg.DelayedAutoStart)
		st.Account = cfg.ServiceStartName
		st.BinaryPath = cfg.BinaryPathName
		st.SidType = sidTypeName(cfg.SidType)
	} else {
		errs = append(errs, "config: "+err.Error())
	}
	if p, err := queryRequiredPrivileges(h); err == nil {
		st.Privileges = p
	} else {
		errs = append(errs, "privileges: "+err.Error())
	}
	st.QueryError = strings.Join(errs, "; ")
	return st, nil
}

func queryRequiredPrivileges(h windows.Handle) ([]string, error) {
	n := uint32(512)
	for {
		b := make([]byte, n)
		err := windows.QueryServiceConfig2(h, windows.SERVICE_CONFIG_REQUIRED_PRIVILEGES_INFO, &b[0], n, &n)
		if errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) && n > uint32(len(b)) {
			continue
		}
		if err != nil {
			return nil, err
		}
		info := (*serviceRequiredPrivilegesInfo)(unsafe.Pointer(&b[0]))
		return parseMultiSZ(info.RequiredPrivileges), nil
	}
}

// parseMultiSZ reads a double-NUL-terminated UTF-16 string list.
func parseMultiSZ(p *uint16) []string {
	var out []string
	next := func(q *uint16) *uint16 { return (*uint16)(unsafe.Add(unsafe.Pointer(q), 2)) }
	for p != nil && *p != 0 {
		var u []uint16
		for ; *p != 0; p = next(p) {
			u = append(u, *p)
		}
		out = append(out, string(utf16.Decode(u)))
		p = next(p)
	}
	return out
}

func stateName(s svc.State) string {
	switch s {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start_pending"
	case svc.StopPending:
		return "stop_pending"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "continue_pending"
	case svc.PausePending:
		return "pause_pending"
	case svc.Paused:
		return "paused"
	}
	return fmt.Sprintf("state_%d", uint32(s))
}

func startTypeName(t uint32, delayed bool) string {
	switch t {
	case mgr.StartAutomatic:
		if delayed {
			return "delayed_auto"
		}
		return "auto"
	case mgr.StartManual:
		return "manual"
	case mgr.StartDisabled:
		return "disabled"
	}
	return fmt.Sprintf("start_%d", t)
}

func sidTypeName(t uint32) string {
	switch t {
	case windows.SERVICE_SID_TYPE_NONE:
		return "none"
	case windows.SERVICE_SID_TYPE_UNRESTRICTED:
		return "unrestricted"
	case windows.SERVICE_SID_TYPE_RESTRICTED:
		return "restricted"
	}
	return fmt.Sprintf("sid_%d", t)
}
