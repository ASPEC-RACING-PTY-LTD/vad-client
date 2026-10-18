//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// PipeSDDL is the security descriptor of the agent pipe:
//
//	D:P                      protected DACL (nothing inherited)
//	(D;;GA;;;NU)             deny NETWORK logons outright (go-winio also sets
//	                         FILE_PIPE_REJECT_REMOTE_CLIENTS)
//	(A;;GA;;;SY)             LocalSystem: full (the service; components)
//	(A;;GA;;;BA)             Administrators: full
//	(A;;GA;;;OW)             the pipe's owner (the creating process) may create
//	                         further instances; in the service that is SYSTEM
//	(A;;0x12019b;;;IU)       Interactive users (console and RDP): read/write
//	                         data only. 0x12019b is FILE_GENERIC_READ |
//	                         FILE_GENERIC_WRITE without FILE_APPEND_DATA, which
//	                         on a pipe is FILE_CREATE_PIPE_INSTANCE, so a normal
//	                         user cannot add a rogue server instance.
//
// Authorisation of individual calls is by caller SID (ServerOptions.Authorize),
// not by this ACL.
const PipeSDDL = "D:P(D;;GA;;;NU)(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;OW)(A;;0x12019b;;;IU)"

// clientAccess is what the client asks for: read plus write-data. It must
// stay within the IU grant above (GENERIC_WRITE would include
// FILE_CREATE_PIPE_INSTANCE and be denied).
const clientAccess = windows.GENERIC_READ | windows.FILE_WRITE_DATA

// Listen creates the server end of the named pipe name with PipeSDDL. The
// pipe uses byte mode: framing is the 4-byte length prefix, so message mode
// adds nothing. Creation fails if the name is already in use, which stops
// another process from squatting the agent's pipe before the agent.
func Listen(name string) (net.Listener, error) {
	return winio.ListenPipe(name, &winio.PipeConfig{
		SecurityDescriptor: PipeSDDL,
		InputBufferSize:    64 << 10,
		OutputBufferSize:   64 << 10,
	})
}

// DialPipe connects to the pipe name at SECURITY_IDENTIFICATION level, so
// the agent can identify (but not impersonate) the caller.
func DialPipe(ctx context.Context, name string) (net.Conn, error) {
	return winio.DialPipeAccessImpLevel(ctx, name, clientAccess, winio.PipeImpLevelIdentification)
}

type fder interface{ Fd() uintptr }

var procImpersonateNamedPipeClient = windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateNamedPipeClient")

func pipeHandle(conn net.Conn) (windows.Handle, error) {
	f, ok := conn.(fder)
	if !ok {
		return 0, fmt.Errorf("not a named pipe connection (%T)", conn)
	}
	return windows.Handle(f.Fd()), nil
}

// clientToken impersonates the pipe client on a dedicated, locked OS
// thread and returns a copy of its token. If RevertToSelf fails the thread
// stays locked and the runtime discards it when the goroutine exits, so an
// impersonating thread is never reused.
func clientToken(h windows.Handle) (windows.Token, error) {
	type result struct {
		tok windows.Token
		err error
	}
	ch := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		if r, _, e := procImpersonateNamedPipeClient.Call(uintptr(h)); r == 0 {
			runtime.UnlockOSThread()
			ch <- result{err: fmt.Errorf("ImpersonateNamedPipeClient: %w", e)}
			return
		}
		var tok windows.Token
		err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &tok)
		if rerr := windows.RevertToSelf(); rerr != nil {
			if err == nil {
				_ = tok.Close()
			}
			ch <- result{err: fmt.Errorf("RevertToSelf: %w", rerr)}
			return // thread stays locked and is destroyed
		}
		runtime.UnlockOSThread()
		if err != nil {
			// An anonymous-level client lands here (ERROR_CANT_OPEN_ANONYMOUS).
			err = fmt.Errorf("OpenThreadToken: %w", err)
		}
		ch <- result{tok: tok, err: err}
	}()
	r := <-ch
	return r.tok, r.err
}

// identifyConn derives the caller from the client's token. Must run after
// the first frame has been read (a pipe server can impersonate only after
// reading from the client).
func identifyConn(conn net.Conn) (Caller, error) {
	h, err := pipeHandle(conn)
	if err != nil {
		return Caller{}, err
	}
	var c Caller
	_ = windows.GetNamedPipeClientProcessId(h, &c.PID)
	tok, err := clientToken(h)
	if err != nil {
		return Caller{}, err
	}
	defer tok.Close()
	return tokenCaller(tok, c)
}

func tokenCaller(tok windows.Token, c Caller) (Caller, error) {
	user, err := tok.GetTokenUser()
	if err != nil {
		return Caller{}, fmt.Errorf("token user: %w", err)
	}
	c.SID = user.User.Sid.String()
	c.System = user.User.Sid.IsWellKnown(windows.WinLocalSystemSid)
	var n uint32
	if err := windows.GetTokenInformation(tok, windows.TokenSessionId, (*byte)(unsafe.Pointer(&c.SessionID)), uint32(unsafe.Sizeof(c.SessionID)), &n); err != nil {
		return Caller{}, fmt.Errorf("token session: %w", err)
	}
	if ba, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid); err == nil {
		c.Admin, _ = tok.IsMember(ba)
	}
	c.Elevated = tok.IsElevated()
	return c, nil
}

// RequireSystemServer is a ClientOptions.VerifyServer check that the pipe
// server process runs as LocalSystem, so a user process that grabbed the
// pipe name while the service was stopped is not trusted.
func RequireSystemServer(conn net.Conn) error {
	h, err := pipeHandle(conn)
	if err != nil {
		return err
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(h, &pid); err != nil {
		return fmt.Errorf("server pid: %w", err)
	}
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return fmt.Errorf("open server process: %w", err)
	}
	defer windows.CloseHandle(p)
	var tok windows.Token
	if err := windows.OpenProcessToken(p, windows.TOKEN_QUERY, &tok); err != nil {
		// A non-admin cannot open a SYSTEM process token; reaching a
		// LocalSystem server's token is not possible for a user either, so
		// fall back to the session: services run in session 0.
		var sess uint32
		if serr := windows.ProcessIdToSessionId(pid, &sess); serr != nil {
			return fmt.Errorf("server session: %w", serr)
		}
		if sess != 0 {
			return errors.New("pipe server is not a service (session " + fmt.Sprint(sess) + ")")
		}
		return nil
	}
	defer tok.Close()
	user, err := tok.GetTokenUser()
	if err != nil {
		return fmt.Errorf("server token user: %w", err)
	}
	if !user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		return errors.New("pipe server is not LocalSystem")
	}
	return nil
}
