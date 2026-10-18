package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/agent"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/modules"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/updater"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// ClientOptions configures a Client. Zero values select the defaults.
type ClientOptions struct {
	// Dial opens a raw connection to the agent. Default: DialPipe(PipeName).
	Dial func(ctx context.Context) (net.Conn, error)
	// VerifyServer, when set, checks a freshly dialled connection before
	// the handshake (e.g. RequireSystemServer against pipe squatting).
	VerifyServer func(conn net.Conn) error
	Log          *slog.Logger
	MaxFrame     int           // default DefaultMaxFrame
	DialTimeout  time.Duration // dial + handshake, default 3s
	WriteTimeout time.Duration // per frame, default 10s
	MinBackoff   time.Duration // first reconnect delay, default 250ms
	MaxBackoff   time.Duration // reconnect delay cap, default 10s
	EventBuffer  int           // per subscription, default agent.DefaultBuffer
}

// Client implements agent.API over IPC. It connects lazily, reconnects with
// exponential backoff after the connection is lost and re-establishes live
// subscriptions on the new connection. Calls in flight when a connection
// drops fail with code "unavailable"; they are not retried because they may
// not be idempotent.
type Client struct {
	opts ClientOptions
	log  *slog.Logger

	mu       sync.Mutex
	cc       *clientConn
	closed   bool
	backoff  time.Duration
	nextDial time.Time
	retrying bool
	subs     map[uint64]*clientSub
	nextSub  uint64
	done     chan struct{}
}

var _ agent.API = (*Client)(nil)

// NewClient returns a client that connects on first use.
func NewClient(opts ClientOptions) *Client {
	if opts.Dial == nil {
		opts.Dial = func(ctx context.Context) (net.Conn, error) { return DialPipe(ctx, PipeName) }
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.MaxFrame <= 0 {
		opts.MaxFrame = DefaultMaxFrame
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 3 * time.Second
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = 10 * time.Second
	}
	if opts.MinBackoff <= 0 {
		opts.MinBackoff = 250 * time.Millisecond
	}
	if opts.MaxBackoff < opts.MinBackoff {
		opts.MaxBackoff = max(10*time.Second, opts.MinBackoff)
	}
	if opts.EventBuffer <= 0 {
		opts.EventBuffer = agent.DefaultBuffer
	}
	return &Client{opts: opts, log: opts.Log.With("component", "ipc-client"), subs: map[uint64]*clientSub{}, done: make(chan struct{})}
}

// Dial returns a client that is already connected, or an error (code
// "unavailable" when no agent is listening, "invalid" on a protocol
// version mismatch).
func Dial(ctx context.Context, opts ClientOptions) (*Client, error) {
	c := NewClient(opts)
	if _, err := c.conn(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// Close disconnects and closes every subscription. Later calls fail with
// agent.ErrClosed.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.done)
	cc := c.cc
	c.cc = nil
	subs := c.subs
	c.subs = map[uint64]*clientSub{}
	c.mu.Unlock()
	if cc != nil {
		cc.close(agent.ErrClosed)
	}
	for _, s := range subs {
		s.end()
	}
	return nil
}

func unavailable(format string, args ...any) *agent.Error {
	return &agent.Error{Code: agent.CodeUnavailable, Message: fmt.Sprintf(format, args...)}
}

// conn returns the live connection, dialling if needed and allowed by the
// backoff schedule.
func (c *Client) conn(ctx context.Context) (*clientConn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, agent.ErrClosed
	}
	if c.cc != nil {
		return c.cc, nil
	}
	if wait := time.Until(c.nextDial); wait > 0 {
		return nil, unavailable("ipc: agent not reachable (next attempt in %s)", wait.Round(time.Millisecond))
	}
	cc, err := c.dial(ctx)
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) && ae.Code == agent.CodeInvalid {
			// A version mismatch will not fix itself; back off fully.
			c.backoff = c.opts.MaxBackoff
		} else if c.backoff == 0 {
			c.backoff = c.opts.MinBackoff
		} else {
			c.backoff = min(2*c.backoff, c.opts.MaxBackoff)
		}
		c.nextDial = time.Now().Add(c.backoff)
		return nil, err
	}
	c.backoff, c.nextDial = 0, time.Time{}
	c.cc = cc
	go cc.readLoop()
	if len(c.subs) > 0 {
		subs := make([]*clientSub, 0, len(c.subs))
		for _, s := range c.subs {
			subs = append(subs, s)
		}
		go c.resubscribe(cc, subs)
	}
	return cc, nil
}

func (c *Client) dial(ctx context.Context) (*clientConn, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.DialTimeout)
	defer cancel()
	raw, err := c.opts.Dial(ctx)
	if err != nil {
		return nil, unavailable("ipc: agent not reachable: %v", err)
	}
	if c.opts.VerifyServer != nil {
		if err := c.opts.VerifyServer(raw); err != nil {
			_ = raw.Close()
			return nil, unavailable("ipc: agent identity check failed: %v", err)
		}
	}
	deadline, _ := ctx.Deadline()
	_ = raw.SetDeadline(deadline)
	if err := writeFrame(raw, &Frame{Type: frameHello, Version: agent.ProtocolVersion}, c.opts.MaxFrame); err != nil {
		_ = raw.Close()
		return nil, unavailable("ipc: handshake: %v", err)
	}
	w, err := readFrame(raw, c.opts.MaxFrame)
	if err != nil {
		_ = raw.Close()
		return nil, unavailable("ipc: handshake: %v", err)
	}
	_ = raw.SetDeadline(time.Time{})
	if w.Type != frameWelcome {
		_ = raw.Close()
		return nil, unavailable("ipc: handshake: unexpected %q frame", w.Type)
	}
	if w.Error != nil {
		_ = raw.Close()
		return nil, w.Error
	}
	if w.Version != agent.ProtocolVersion {
		_ = raw.Close()
		return nil, &agent.Error{Code: agent.CodeInvalid, Message: fmt.Sprintf("ipc: protocol version mismatch (agent %d, client %d)", w.Version, agent.ProtocolVersion)}
	}
	return &clientConn{c: c, raw: raw, pending: map[uint64]chan *Frame{}, dead: make(chan struct{})}, nil
}

// lost is called once by a connection's read loop when it ends.
func (c *Client) lost(cc *clientConn, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cc != cc {
		return
	}
	c.cc = nil
	if c.closed {
		return
	}
	c.log.Info("agent connection lost", "err", err)
	if len(c.subs) > 0 && !c.retrying {
		c.retrying = true
		go c.reconnectLoop()
	}
}

// reconnectLoop redials with backoff while subscriptions want events.
func (c *Client) reconnectLoop() {
	defer func() {
		c.mu.Lock()
		c.retrying = false
		c.mu.Unlock()
	}()
	for {
		c.mu.Lock()
		if c.closed || len(c.subs) == 0 || c.cc != nil {
			c.mu.Unlock()
			return
		}
		wait := max(time.Until(c.nextDial), 0)
		c.mu.Unlock()
		select {
		case <-c.done:
			return
		case <-time.After(wait):
		}
		if _, err := c.conn(context.Background()); err == nil {
			c.log.Info("agent connection re-established")
			return
		}
	}
}

func (c *Client) resubscribe(cc *clientConn, subs []*clientSub) {
	for _, s := range subs {
		ctx, cancel := context.WithTimeout(context.Background(), c.opts.DialTimeout)
		err := cc.call(ctx, methodSubscribe, subscribeParams{Sub: s.id, Kinds: s.kinds}, nil)
		cancel()
		if err != nil {
			c.log.Warn("agent resubscribe failed", "err", err)
		}
	}
}

// call performs one request on the current connection.
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	if err := ctx.Err(); err != nil {
		return agent.AsError(err)
	}
	cc, err := c.conn(ctx)
	if err != nil {
		return err
	}
	return cc.call(ctx, method, params, out)
}

// --- connection ---

type clientConn struct {
	c   *Client
	raw net.Conn
	wmu sync.Mutex

	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan *Frame
	dead    chan struct{}
	err     error
	once    sync.Once
}

func (cc *clientConn) close(err error) {
	cc.once.Do(func() {
		cc.mu.Lock()
		cc.err = err
		cc.mu.Unlock()
		close(cc.dead)
		_ = cc.raw.Close()
	})
}

func (cc *clientConn) write(f *Frame) error {
	cc.wmu.Lock()
	defer cc.wmu.Unlock()
	_ = cc.raw.SetWriteDeadline(time.Now().Add(cc.c.opts.WriteTimeout))
	err := writeFrame(cc.raw, f, cc.c.opts.MaxFrame)
	if err != nil && !errors.Is(err, ErrFrameTooLarge) {
		cc.close(err)
	}
	return err
}

func (cc *clientConn) readLoop() {
	var err error
	defer func() {
		cc.close(err)
		cc.c.lost(cc, err)
	}()
	for {
		var f *Frame
		f, err = readFrame(cc.raw, cc.c.opts.MaxFrame)
		if err != nil {
			return
		}
		switch f.Type {
		case frameRes:
			cc.mu.Lock()
			ch := cc.pending[f.ID]
			delete(cc.pending, f.ID)
			cc.mu.Unlock()
			if ch != nil {
				ch <- f
			}
		case frameEvent:
			if f.Event != nil {
				cc.c.deliver(f.ID, *f.Event, f.Dropped)
			}
		case frameSubEnd:
			// The agent's bus closed (agent stopping). The subscription
			// stays registered and is re-established on reconnect.
		default:
			err = fmt.Errorf("ipc: unexpected %q frame", f.Type)
			return
		}
	}
}

func (cc *clientConn) call(ctx context.Context, method string, params, out any) error {
	f := &Frame{Type: frameReq, Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return &agent.Error{Code: agent.CodeInternal, Message: "ipc: encode params: " + err.Error()}
		}
		f.Params = b
	}
	ch := make(chan *Frame, 1)
	cc.mu.Lock()
	cc.nextID++
	f.ID = cc.nextID
	cc.pending[f.ID] = ch
	cc.mu.Unlock()
	unregister := func() {
		cc.mu.Lock()
		delete(cc.pending, f.ID)
		cc.mu.Unlock()
	}
	if err := cc.write(f); err != nil {
		unregister()
		if errors.Is(err, ErrFrameTooLarge) {
			return &agent.Error{Code: agent.CodeInvalid, Message: "ipc: request exceeds frame size limit"}
		}
		return unavailable("ipc: send: %v", err)
	}
	select {
	case res := <-ch:
		if res.Error != nil {
			return res.Error
		}
		if out != nil && len(res.Result) > 0 {
			if err := json.Unmarshal(res.Result, out); err != nil {
				return &agent.Error{Code: agent.CodeInternal, Message: "ipc: decode result: " + err.Error()}
			}
		}
		return nil
	case <-ctx.Done():
		unregister()
		go func() { _ = cc.write(&Frame{Type: frameCancel, ID: f.ID}) }()
		return agent.AsError(ctx.Err())
	case <-cc.dead:
		unregister()
		cc.mu.Lock()
		err := cc.err
		cc.mu.Unlock()
		if ae, ok := err.(*agent.Error); ok {
			return ae
		}
		return unavailable("ipc: connection to agent lost: %v", err)
	}
}

// --- subscriptions ---

type clientSub struct {
	c     *Client
	id    uint64
	kinds []agent.EventKind

	mu      sync.Mutex
	ch      chan agent.Event
	ended   bool
	local   uint64 // dropped here
	remote  atomic.Uint64
	closeMu sync.Once
}

func (s *clientSub) C() <-chan agent.Event { return s.ch }

// Dropped counts events lost on either side of the connection.
func (s *clientSub) Dropped() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.local + s.remote.Load()
}

// Close ends the subscription and tells the agent (best effort).
func (s *clientSub) Close() {
	s.closeMu.Do(func() {
		c := s.c
		c.mu.Lock()
		delete(c.subs, s.id)
		cc := c.cc
		c.mu.Unlock()
		s.end()
		if cc != nil {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), c.opts.DialTimeout)
				defer cancel()
				_ = cc.call(ctx, methodUnsubscribe, s.id, nil)
			}()
		}
	})
}

func (s *clientSub) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ended {
		s.ended = true
		close(s.ch)
	}
}

// push delivers ev without blocking, dropping the oldest buffered event
// when the consumer is slow (the same policy as agent.Bus).
func (s *clientSub) push(ev agent.Event, remoteDropped uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.remote.Store(remoteDropped)
	for {
		select {
		case s.ch <- ev:
			return
		default:
		}
		select {
		case <-s.ch:
			s.local++
		default:
		}
	}
}

func (c *Client) deliver(id uint64, ev agent.Event, dropped uint64) {
	c.mu.Lock()
	s := c.subs[id]
	c.mu.Unlock()
	if s != nil {
		s.push(ev, dropped)
	}
}

// Subscribe implements agent.API. The subscription survives agent restarts:
// after a reconnect it is re-established under the same id. It ends on
// Close, when ctx ends or when the Client closes.
func (c *Client) Subscribe(ctx context.Context, kinds ...agent.EventKind) (agent.Subscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, agent.AsError(err)
	}
	cc, err := c.conn(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.nextSub++
	s := &clientSub{c: c, id: c.nextSub, kinds: kinds, ch: make(chan agent.Event, c.opts.EventBuffer)}
	c.subs[s.id] = s
	c.mu.Unlock()
	if err := cc.call(ctx, methodSubscribe, subscribeParams{Sub: s.id, Kinds: kinds}, nil); err != nil {
		c.mu.Lock()
		delete(c.subs, s.id)
		c.mu.Unlock()
		s.end()
		return nil, err
	}
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				s.Close()
			case <-c.done:
			}
		}()
	}
	return s, nil
}

// --- agent.API ---

func invoke[R any](c *Client, ctx context.Context, method string, params any) (R, error) {
	var out R
	err := c.call(ctx, method, params, &out)
	return out, err
}

func (c *Client) Bootstrap(ctx context.Context) (agent.Bootstrap, error) {
	return invoke[agent.Bootstrap](c, ctx, "Bootstrap", nil)
}

func (c *Client) ProbeServer(ctx context.Context, req agent.ProbeRequest) (agent.ProbeResult, error) {
	return invoke[agent.ProbeResult](c, ctx, "ProbeServer", req)
}

func (c *Client) Connect(ctx context.Context, req agent.ConnectRequest) (agent.EnrollView, error) {
	return invoke[agent.EnrollView](c, ctx, "Connect", req)
}

func (c *Client) CancelEnrollment(ctx context.Context) error {
	return c.call(ctx, "CancelEnrollment", nil, nil)
}

func (c *Client) Disconnect(ctx context.Context) error {
	return c.call(ctx, "Disconnect", nil, nil)
}

func (c *Client) ListConfigs(ctx context.Context) ([]engine.ConfigView, error) {
	return invoke[[]engine.ConfigView](c, ctx, "ListConfigs", nil)
}

func (c *Client) SaveConfig(ctx context.Context, cfg config.SyncConfig) (config.SyncConfig, error) {
	return invoke[config.SyncConfig](c, ctx, "SaveConfig", cfg)
}

func (c *Client) DeleteConfig(ctx context.Context, id string) error {
	return c.call(ctx, "DeleteConfig", id, nil)
}

func (c *Client) SyncNow(ctx context.Context, id string) error {
	return c.call(ctx, "SyncNow", id, nil)
}

func (c *Client) PreviewConfig(ctx context.Context, cfg config.SyncConfig) (agent.PreviewResult, error) {
	return invoke[agent.PreviewResult](c, ctx, "PreviewConfig", cfg)
}

func (c *Client) Snapshot(ctx context.Context) (engine.Snapshot, error) {
	return invoke[engine.Snapshot](c, ctx, "Snapshot", nil)
}

func (c *Client) SetPaused(ctx context.Context, paused bool) error {
	return c.call(ctx, "SetPaused", paused, nil)
}

func (c *Client) RetryFailed(ctx context.Context, id int64) (int64, error) {
	return invoke[int64](c, ctx, "RetryFailed", id)
}

func (c *Client) ListQueue(ctx context.Context, status string) ([]state.Item, error) {
	return invoke[[]state.Item](c, ctx, "ListQueue", status)
}

func (c *Client) ListHistory(ctx context.Context, status string) ([]state.HistoryEntry, error) {
	return invoke[[]state.HistoryEntry](c, ctx, "ListHistory", status)
}

func (c *Client) ListAutomations(ctx context.Context) ([]engine.AutomationView, error) {
	return invoke[[]engine.AutomationView](c, ctx, "ListAutomations", nil)
}

func (c *Client) RefreshAutomations(ctx context.Context) ([]engine.AutomationView, error) {
	return invoke[[]engine.AutomationView](c, ctx, "RefreshAutomations", nil)
}

func (c *Client) GetSettings(ctx context.Context) (config.Settings, error) {
	return invoke[config.Settings](c, ctx, "GetSettings", nil)
}

func (c *Client) SaveSettings(ctx context.Context, s config.Settings) (config.Settings, error) {
	return invoke[config.Settings](c, ctx, "SaveSettings", s)
}

func (c *Client) UpdateStatus(ctx context.Context) (updater.Status, error) {
	return invoke[updater.Status](c, ctx, "UpdateStatus", nil)
}

func (c *Client) CheckForUpdates(ctx context.Context) (updater.Status, error) {
	return invoke[updater.Status](c, ctx, "CheckForUpdates", nil)
}

func (c *Client) InstallUpdate(ctx context.Context) error {
	return c.call(ctx, "InstallUpdate", nil, nil)
}

func (c *Client) KeyStatus(ctx context.Context) (agent.KeyStatus, error) {
	return invoke[agent.KeyStatus](c, ctx, "KeyStatus", nil)
}

func (c *Client) ConfirmServerIdentity(ctx context.Context, fingerprint string) (agent.KeyStatus, error) {
	return invoke[agent.KeyStatus](c, ctx, "ConfirmServerIdentity", fingerprint)
}

// InitRepository returns the recovery key text once; callers must not log
// or persist it.
func (c *Client) InitRepository(ctx context.Context, req agent.InitRepositoryRequest) (agent.InitRepositoryResult, error) {
	return invoke[agent.InitRepositoryResult](c, ctx, "InitRepository", req)
}

func (c *Client) ConfirmRecoveryKey(ctx context.Context, text string) error {
	return c.call(ctx, "ConfirmRecoveryKey", text, nil)
}

func (c *Client) ListBackupJobs(ctx context.Context) ([]agent.BackupJob, error) {
	return invoke[[]agent.BackupJob](c, ctx, "ListBackupJobs", nil)
}

func (c *Client) ListBackupRuns(ctx context.Context, jobID int64) ([]agent.BackupRun, error) {
	return invoke[[]agent.BackupRun](c, ctx, "ListBackupRuns", jobID)
}

func (c *Client) ListIssues(ctx context.Context, q agent.IssueQuery) ([]agent.BackupIssue, error) {
	return invoke[[]agent.BackupIssue](c, ctx, "ListIssues", q)
}

func (c *Client) RunBackup(ctx context.Context, jobID int64) (int64, error) {
	return invoke[int64](c, ctx, "RunBackup", jobID)
}

func (c *Client) RetrySkipped(ctx context.Context, jobID int64) (int64, error) {
	return invoke[int64](c, ctx, "RetrySkipped", jobID)
}

func (c *Client) Readiness(ctx context.Context) (agent.BackupReadiness, error) {
	return invoke[agent.BackupReadiness](c, ctx, "Readiness", nil)
}

func (c *Client) SaveBackupJob(ctx context.Context, j wire.Job) (agent.BackupJob, error) {
	return invoke[agent.BackupJob](c, ctx, "SaveBackupJob", j)
}

func (c *Client) SkipIssues(ctx context.Context, req agent.SkipIssuesRequest) error {
	return c.call(ctx, "SkipIssues", req, nil)
}

func (c *Client) GiveConsent(ctx context.Context, proof []byte) error {
	return c.call(ctx, "GiveConsent", proof, nil)
}

func (c *Client) GetMode(ctx context.Context) (modules.ModulesState, error) {
	return invoke[modules.ModulesState](c, ctx, "GetMode", nil)
}
