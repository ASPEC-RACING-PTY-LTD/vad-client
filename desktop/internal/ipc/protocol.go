// Package ipc carries agent.API between the VaultDock UI and the agent
// service over the local named pipe \\.\pipe\VaultDock.Agent.v1.
//
// Wire format: every frame is a 4-byte big-endian length followed by that
// many bytes of UTF-8 JSON (one Frame object). Frames larger than the
// configured maximum (DefaultMaxFrame) end the connection.
//
//	client → server  {"t":"hello","v":1}
//	server → client  {"t":"welcome","v":1}                 or {"t":"welcome","v":1,"e":{…}} then close
//	client → server  {"t":"req","id":7,"m":"SaveConfig","p":{…}}
//	server → client  {"t":"res","id":7,"r":{…}}            or {"t":"res","id":7,"e":{"code":…,"message":…}}
//	client → server  {"t":"cancel","id":7}                 (best effort; the call's ctx is cancelled)
//	client → server  {"t":"req","id":8,"m":"Subscribe","p":{"sub":3,"kinds":["snapshot"]}}
//	server → client  {"t":"event","id":3,"ev":{agent.Event},"d":0}  (d = server-side dropped count)
//	server → client  {"t":"sub_end","id":3}                (the server-side subscription ended)
//	client → server  {"t":"req","id":9,"m":"Unsubscribe","p":3}
//
// Methods map 1:1 to agent.API method names. "p" is the method's single
// argument (absent for none) and "r" its single result (absent for none).
// Subscription ids are chosen by the client so a reconnecting client can
// re-subscribe under the same id.
package ipc

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/agent"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/ipc/caller"
)

// PipeName is the agent's named pipe.
const PipeName = `\\.\pipe\VaultDock.Agent.v1`

// DefaultMaxFrame bounds a single frame (4 MiB). Queue and history lists
// are the largest responses and stay far below this.
const DefaultMaxFrame = 4 << 20

// Frame types.
const (
	frameHello   = "hello"
	frameWelcome = "welcome"
	frameReq     = "req"
	frameRes     = "res"
	frameCancel  = "cancel"
	frameEvent   = "event"
	frameSubEnd  = "sub_end"
)

// Frame is one wire message.
type Frame struct {
	Type    string          `json:"t"`
	ID      uint64          `json:"id,omitempty"`
	Version int             `json:"v,omitempty"`
	Method  string          `json:"m,omitempty"`
	Params  json.RawMessage `json:"p,omitempty"`
	Result  json.RawMessage `json:"r,omitempty"`
	Error   *agent.Error    `json:"e,omitempty"`
	Event   *agent.Event    `json:"ev,omitempty"`
	Dropped uint64          `json:"d,omitempty"`
}

// subscribeParams is the argument of the Subscribe method.
type subscribeParams struct {
	Sub   uint64            `json:"sub"`
	Kinds []agent.EventKind `json:"kinds,omitempty"`
}

// ErrFrameTooLarge is returned when a frame exceeds the maximum size.
var ErrFrameTooLarge = fmt.Errorf("ipc: frame exceeds size limit")

// writeFrame encodes f as one length-prefixed frame. The caller serialises
// writers.
func writeFrame(w io.Writer, f *Frame, max int) error {
	b, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("ipc: encode frame: %w", err)
	}
	if len(b) > max {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(buf, uint32(len(b)))
	copy(buf[4:], b)
	_, err = w.Write(buf)
	return err
}

// readFrame reads one frame, rejecting frames larger than max before
// allocating their body.
func readFrame(r io.Reader, max int) (*Frame, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || uint64(n) > uint64(max) {
		return nil, ErrFrameTooLarge
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	var f Frame
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("ipc: decode frame: %w", err)
	}
	return &f, nil
}

// Caller is the identity of an IPC client; see package caller.
type Caller = caller.Caller

// CallerFromContext returns the IPC caller of a server-side call. Calls that
// did not arrive over IPC (in-process dev mode) have no caller.
func CallerFromContext(ctx context.Context) (Caller, bool) { return caller.From(ctx) }
