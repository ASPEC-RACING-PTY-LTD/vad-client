// Package caller carries the identity of an IPC caller through a
// context.Context. It is a leaf package so that both the transport
// (internal/ipc) and the agent host (internal/agent) can import it without
// an import cycle.
package caller

import "context"

// Caller identifies the process on the other end of an IPC connection. On
// Windows it is taken from the client's token (impersonation at
// identification level), never from anything the client sends.
type Caller struct {
	SID       string `json:"sid"`        // user SID, e.g. S-1-5-21-…
	SessionID uint32 `json:"session_id"` // Terminal Services session of the token
	PID       uint32 `json:"pid"`        // client process id (informational only)
	Admin     bool   `json:"admin"`      // token is an effective member of BUILTIN\Administrators
	Elevated  bool   `json:"elevated"`   // token is elevated (UAC)
	System    bool   `json:"system"`     // caller is LocalSystem (S-1-5-18)
}

type key struct{}

// With returns ctx carrying c.
func With(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, key{}, c)
}

// From returns the caller stored in ctx, if any. In-process calls (dev
// mode) carry no caller.
func From(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(key{}).(Caller)
	return c, ok
}
