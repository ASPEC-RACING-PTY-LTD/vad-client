//go:build !windows

package ipc

import (
	"context"
	"errors"
	"net"
)

// Non-Windows builds have no named-pipe transport; the protocol itself is
// exercised over net.Pipe in tests.

// PipeSDDL is unused outside Windows.
const PipeSDDL = ""

var errNoPipes = errors.New("ipc: named pipes require Windows")

// Listen is unsupported outside Windows.
func Listen(name string) (net.Listener, error) { return nil, errNoPipes }

// DialPipe is unsupported outside Windows.
func DialPipe(ctx context.Context, name string) (net.Conn, error) { return nil, errNoPipes }

// RequireSystemServer is unsupported outside Windows.
func RequireSystemServer(conn net.Conn) error { return errNoPipes }

func identifyConn(conn net.Conn) (Caller, error) { return Caller{}, errNoPipes }
