package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/agent"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/ipc/caller"
)

// ServerOptions configures a Server. Zero values select the defaults.
type ServerOptions struct {
	Log      *slog.Logger
	MaxFrame int // default DefaultMaxFrame
	// Identify returns the identity of the process behind conn. It runs
	// after the hello frame has been read. Default: the platform
	// implementation (named-pipe client token on Windows); a connection
	// that cannot be identified is refused.
	Identify func(conn net.Conn) (Caller, error)
	// Authorize, when set, is consulted before every method call
	// (including Subscribe). A non-nil error is returned to the client.
	Authorize        func(ctx context.Context, c Caller, method string) error
	MaxSubscriptions int           // per connection, default 16
	MaxInflight      int           // concurrent calls per connection, default 32
	HelloTimeout     time.Duration // default 10s
	WriteTimeout     time.Duration // per frame, default 10s
}

// Server serves an agent.API to IPC clients.
type Server struct {
	api  agent.API
	opts ServerOptions
	log  *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu        sync.Mutex
	closed    bool
	listeners map[net.Listener]struct{}
	conns     map[net.Conn]struct{}
}

// NewServer returns a server for api.
func NewServer(api agent.API, opts ServerOptions) *Server {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.MaxFrame <= 0 {
		opts.MaxFrame = DefaultMaxFrame
	}
	if opts.Identify == nil {
		opts.Identify = identifyConn
	}
	if opts.MaxSubscriptions <= 0 {
		opts.MaxSubscriptions = 16
	}
	if opts.MaxInflight <= 0 {
		opts.MaxInflight = 32
	}
	if opts.HelloTimeout <= 0 {
		opts.HelloTimeout = 10 * time.Second
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = 10 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		api: api, opts: opts, log: opts.Log.With("component", "ipc"),
		ctx: ctx, cancel: cancel,
		listeners: map[net.Listener]struct{}{},
		conns:     map[net.Conn]struct{}{},
	}
}

// ErrServerClosed is returned by Serve after Close.
var ErrServerClosed = errors.New("ipc: server closed")

// Serve accepts connections on l until l fails or the server is closed.
// It returns ErrServerClosed after Close.
func (s *Server) Serve(l net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = l.Close()
		return ErrServerClosed
	}
	s.listeners[l] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.listeners, l)
		s.mu.Unlock()
	}()
	for {
		conn, err := l.Accept()
		if err != nil {
			if s.ctx.Err() != nil {
				return ErrServerClosed
			}
			return err
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.ServeConn(conn)
		}()
	}
}

// Close stops all listeners and connections and waits for their handlers.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.cancel()
	for l := range s.listeners {
		_ = l.Close()
	}
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
	return nil
}

// serverConn is the state of one client connection.
type serverConn struct {
	s      *Server
	conn   net.Conn
	ctx    context.Context // carries the Caller; cancelled when the connection ends
	cancel context.CancelFunc
	log    *slog.Logger
	wg     sync.WaitGroup

	wmu sync.Mutex

	mu    sync.Mutex
	calls map[uint64]context.CancelFunc
	subs  map[uint64]agent.Subscription
}

// ServeConn runs the protocol on one accepted connection and returns when
// it ends. The connection is closed on return.
func (s *Server) ServeConn(conn net.Conn) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = conn.Close()
		return
	}
	s.conns[conn] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()

	c, ok := s.handshake(conn)
	if !ok {
		return
	}
	defer c.shutdown()
	c.readLoop()
}

func (s *Server) handshake(conn net.Conn) (*serverConn, bool) {
	_ = conn.SetReadDeadline(time.Now().Add(s.opts.HelloTimeout))
	hello, err := readFrame(conn, s.opts.MaxFrame)
	if err != nil {
		s.log.Debug("ipc hello failed", "err", err)
		return nil, false
	}
	_ = conn.SetReadDeadline(time.Time{})
	refuse := func(e *agent.Error) (*serverConn, bool) {
		s.log.Warn("ipc connection refused", "reason", e.Message)
		_ = conn.SetWriteDeadline(time.Now().Add(s.opts.WriteTimeout))
		_ = writeFrame(conn, &Frame{Type: frameWelcome, Version: agent.ProtocolVersion, Error: e}, s.opts.MaxFrame)
		return nil, false
	}
	if hello.Type != frameHello {
		return refuse(&agent.Error{Code: agent.CodeInvalid, Message: "ipc: expected hello frame"})
	}
	if hello.Version != agent.ProtocolVersion {
		return refuse(&agent.Error{Code: agent.CodeInvalid, Message: fmt.Sprintf("ipc: protocol version mismatch (agent %d, client %d)", agent.ProtocolVersion, hello.Version)})
	}
	who, err := s.opts.Identify(conn)
	if err != nil {
		return refuse(&agent.Error{Code: agent.CodeInvalid, Message: "ipc: cannot identify caller: " + err.Error()})
	}
	ctx, cancel := context.WithCancel(caller.With(s.ctx, who))
	c := &serverConn{
		s: s, conn: conn, ctx: ctx, cancel: cancel,
		log:   s.log.With("sid", who.SID, "session", who.SessionID, "pid", who.PID),
		calls: map[uint64]context.CancelFunc{},
		subs:  map[uint64]agent.Subscription{},
	}
	if err := c.write(&Frame{Type: frameWelcome, Version: agent.ProtocolVersion}); err != nil {
		cancel()
		return nil, false
	}
	c.log.Debug("ipc client connected", "admin", who.Admin)
	return c, true
}

// write sends one frame; a failed write ends the connection.
func (c *serverConn) write(f *Frame) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.s.opts.WriteTimeout))
	err := writeFrame(c.conn, f, c.s.opts.MaxFrame)
	if err != nil && !errors.Is(err, ErrFrameTooLarge) {
		_ = c.conn.Close()
	}
	return err
}

func (c *serverConn) readLoop() {
	sem := make(chan struct{}, c.s.opts.MaxInflight)
	for {
		f, err := readFrame(c.conn, c.s.opts.MaxFrame)
		if err != nil {
			if !errors.Is(err, io.EOF) && c.ctx.Err() == nil {
				c.log.Debug("ipc connection ended", "err", err)
			}
			return
		}
		switch f.Type {
		case frameReq:
			select {
			case sem <- struct{}{}:
			case <-c.ctx.Done():
				return
			}
			c.wg.Add(1)
			go func() {
				defer func() { <-sem; c.wg.Done() }()
				c.handle(f)
			}()
		case frameCancel:
			c.mu.Lock()
			if cancel := c.calls[f.ID]; cancel != nil {
				cancel()
			}
			c.mu.Unlock()
		default:
			c.log.Debug("ipc protocol error", "frame", f.Type)
			return
		}
	}
}

// shutdown cancels every call and subscription of the connection.
func (c *serverConn) shutdown() {
	c.cancel()
	_ = c.conn.Close()
	c.mu.Lock()
	for id, sub := range c.subs {
		sub.Close()
		delete(c.subs, id)
	}
	c.mu.Unlock()
	c.wg.Wait()
	c.log.Debug("ipc client disconnected")
}

func (c *serverConn) handle(f *Frame) {
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	c.mu.Lock()
	if _, dup := c.calls[f.ID]; dup {
		c.mu.Unlock()
		_ = c.write(&Frame{Type: frameRes, ID: f.ID, Error: &agent.Error{Code: agent.CodeInvalid, Message: "ipc: duplicate request id"}})
		return
	}
	c.calls[f.ID] = cancel
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.calls, f.ID)
		c.mu.Unlock()
	}()

	res := &Frame{Type: frameRes, ID: f.ID}
	result, err := c.dispatch(ctx, f)
	if err == nil && result != nil {
		b, merr := json.Marshal(result)
		if merr != nil {
			err = &agent.Error{Code: agent.CodeInternal, Message: "ipc: encode result: " + merr.Error()}
		} else {
			res.Result = b
		}
	}
	if err != nil {
		res.Error = agent.AsError(err)
		res.Result = nil
	}
	if werr := c.write(res); errors.Is(werr, ErrFrameTooLarge) {
		_ = c.write(&Frame{Type: frameRes, ID: f.ID, Error: &agent.Error{Code: agent.CodeInternal, Message: "ipc: response exceeds frame size limit"}})
	}
}

func (c *serverConn) dispatch(ctx context.Context, f *Frame) (any, error) {
	who, _ := caller.From(ctx)
	if c.s.opts.Authorize != nil {
		if err := c.s.opts.Authorize(ctx, who, f.Method); err != nil {
			return nil, err
		}
	}
	switch f.Method {
	case methodSubscribe:
		p, err := decodeParams[subscribeParams](f.Params)
		if err != nil {
			return nil, err
		}
		return nil, c.subscribe(p)
	case methodUnsubscribe:
		id, err := decodeParams[uint64](f.Params)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		sub := c.subs[id]
		delete(c.subs, id)
		c.mu.Unlock()
		if sub != nil {
			sub.Close()
		}
		return nil, nil
	}
	h, ok := handlers[f.Method]
	if !ok {
		return nil, &agent.Error{Code: agent.CodeNotImplemented, Message: "ipc: unknown method " + f.Method}
	}
	return h(ctx, c.s.api, f.Params)
}

func (c *serverConn) subscribe(p subscribeParams) error {
	if p.Sub == 0 {
		return &agent.Error{Code: agent.CodeInvalid, Message: "ipc: subscription id required"}
	}
	c.mu.Lock()
	if _, dup := c.subs[p.Sub]; dup {
		c.mu.Unlock()
		return &agent.Error{Code: agent.CodeInvalid, Message: "ipc: duplicate subscription id"}
	}
	if len(c.subs) >= c.s.opts.MaxSubscriptions {
		c.mu.Unlock()
		return &agent.Error{Code: agent.CodeInvalid, Message: "ipc: too many subscriptions"}
	}
	c.mu.Unlock()

	// The subscription lives as long as the connection.
	sub, err := c.s.api.Subscribe(c.ctx, p.Kinds...)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.ctx.Err() != nil {
		c.mu.Unlock()
		sub.Close()
		return agent.ErrClosed
	}
	if _, dup := c.subs[p.Sub]; dup || len(c.subs) >= c.s.opts.MaxSubscriptions {
		c.mu.Unlock()
		sub.Close()
		return &agent.Error{Code: agent.CodeInvalid, Message: "ipc: duplicate subscription id or too many subscriptions"}
	}
	c.subs[p.Sub] = sub
	c.mu.Unlock()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for ev := range sub.C() {
			if err := c.write(&Frame{Type: frameEvent, ID: p.Sub, Event: &ev, Dropped: sub.Dropped()}); err != nil && !errors.Is(err, ErrFrameTooLarge) {
				sub.Close()
				return
			}
		}
		c.mu.Lock()
		cur, still := c.subs[p.Sub]
		if still && cur == sub {
			delete(c.subs, p.Sub)
		}
		c.mu.Unlock()
		// Tell the client only when the server side ended it (bus closed);
		// an Unsubscribe or a closed connection needs no notice.
		if still && cur == sub && c.ctx.Err() == nil {
			_ = c.write(&Frame{Type: frameSubEnd, ID: p.Sub})
		}
	}()
	return nil
}
