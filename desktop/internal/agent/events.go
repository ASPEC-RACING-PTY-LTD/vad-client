package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// EventKind names an event stream. Kinds are stable wire strings.
type EventKind string

const (
	// KindSnapshot carries engine.Snapshot about once a second while anyone
	// subscribes (replaces the Wails "snapshot" ticker).
	KindSnapshot EventKind = "snapshot"
	// KindConnection carries ConnectionEvent (connected, disconnected, revoked).
	KindConnection EventKind = "connection"
	// KindEnrollment carries EnrollView whenever enrolment changes state.
	KindEnrollment EventKind = "enrollment"
	// KindSettings carries config.Settings after they change.
	KindSettings EventKind = "settings"
	// KindUpdate carries updater.Status on every updater state change.
	KindUpdate EventKind = "update"
	// KindNotification carries Notification; the UI shows it as a toast
	// (a session-0 service cannot).
	KindNotification EventKind = "notification"
	// KindLog carries LogEvent for records at or above the bus log level.
	KindLog EventKind = "log"
	// KindProgress carries ProgressEvent for long-running jobs (backup,
	// restore, verify). Sync transfer progress stays in KindSnapshot.
	KindProgress EventKind = "progress"
	// KindIssue carries IssueEvent when an issue is recorded or resolved.
	KindIssue EventKind = "issue"
	// KindShutdown carries ShutdownEvent when the agent is about to stop.
	KindShutdown EventKind = "shutdown"
)

// Event is one bus message. Data is the JSON encoding of the kind's payload,
// produced once at publish time and shared by every subscriber, so a
// transport can forward it verbatim.
type Event struct {
	Seq  uint64          `json:"seq"`
	Kind EventKind       `json:"kind"`
	Time int64           `json:"time"` // unix ms
	Data json.RawMessage `json:"data,omitempty"`
}

// Decode unmarshals the payload into v.
func (e Event) Decode(v any) error { return json.Unmarshal(e.Data, v) }

// ConnectionEvent reports a connection state change.
type ConnectionEvent struct {
	State string `json:"state"` // connected, disconnected, revoked
}

// Notification is a user-facing message (toast).
type Notification struct {
	Title   string `json:"title"`
	Message string `json:"message"`
	Level   string `json:"level"` // info, warning, error
}

// LogEvent is one log record.
type LogEvent struct {
	Time    int64             `json:"time"` // unix ms
	Level   string            `json:"level"`
	Message string            `json:"message"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// ProgressEvent reports progress of a long-running job.
type ProgressEvent struct {
	Job        string `json:"job"` // e.g. "backup", "restore", "verify"
	ID         string `json:"id"`  // job or run id
	Phase      string `json:"phase"`
	Done       int64  `json:"done"`
	Total      int64  `json:"total"`
	Bytes      int64  `json:"bytes"`
	TotalBytes int64  `json:"total_bytes"`
	Message    string `json:"message,omitempty"`
}

// IssueEvent reports an issue (see the state issues table).
type IssueEvent struct {
	Source   string `json:"source"`
	ConfigID string `json:"config_id,omitempty"`
	RelPath  string `json:"rel_path,omitempty"`
	Class    string `json:"class"`
	Message  string `json:"message"`
	State    string `json:"state"` // open, resolved
}

// ShutdownEvent tells subscribers the agent is stopping.
type ShutdownEvent struct {
	Reason string `json:"reason"` // stop, update
}

// DefaultBuffer is the per-subscriber buffer when none is given.
const DefaultBuffer = 64

// Bus fans events out to subscribers. Publish never blocks: when a
// subscriber's buffer is full the oldest queued event is dropped.
type Bus struct {
	mu     sync.Mutex
	seq    uint64
	nextID uint64
	subs   map[uint64]*busSub
	closed bool
	buffer int
}

// NewBus returns a bus whose subscribers buffer up to buffer events
// (DefaultBuffer when buffer <= 0).
func NewBus(buffer int) *Bus {
	if buffer <= 0 {
		buffer = DefaultBuffer
	}
	return &Bus{subs: map[uint64]*busSub{}, buffer: buffer}
}

type busSub struct {
	bus     *Bus
	id      uint64
	kinds   []EventKind // empty = all
	ch      chan Event
	done    chan struct{} // closed with ch
	dropped atomic.Uint64
	once    sync.Once
}

func (s *busSub) C() <-chan Event { return s.ch }
func (s *busSub) Dropped() uint64 { return s.dropped.Load() }
func (s *busSub) Close()          { s.bus.remove(s) }
func (s *busSub) wants(k EventKind) bool {
	return len(s.kinds) == 0 || slices.Contains(s.kinds, k)
}

// Subscribe registers a subscriber for kinds (all when empty). On a closed
// bus the returned subscription's channel is already closed.
func (b *Bus) Subscribe(kinds ...EventKind) Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	s := &busSub{bus: b, id: b.nextID, kinds: slices.Clone(kinds), ch: make(chan Event, b.buffer), done: make(chan struct{})}
	if b.closed {
		s.shut()
		return s
	}
	b.subs[s.id] = s
	return s
}

// SubscribeContext is Subscribe plus automatic Close when ctx ends.
func (b *Bus) SubscribeContext(ctx context.Context, kinds ...EventKind) Subscription {
	s := b.Subscribe(kinds...).(*busSub)
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				s.Close()
			case <-s.done:
			}
		}()
	}
	return s
}

func (b *Bus) remove(s *busSub) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subs, s.id)
	s.shut()
}

// shut closes the channels once; callers hold b.mu.
func (s *busSub) shut() {
	s.once.Do(func() {
		close(s.ch)
		close(s.done)
	})
}

// HasSubscribers reports whether anyone listens for kind.
func (b *Bus) HasSubscribers(kind EventKind) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		if s.wants(kind) {
			return true
		}
	}
	return false
}

// Publish marshals payload and delivers it to every interested subscriber.
// It returns an error only when payload cannot be marshalled.
func (b *Bus) Publish(kind EventKind, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("agent: event %s: %w", kind, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.seq++
	ev := Event{Seq: b.seq, Kind: kind, Time: time.Now().UnixMilli(), Data: data}
	for _, s := range b.subs {
		if s.wants(kind) {
			s.deliver(ev)
		}
	}
	return nil
}

// deliver enqueues ev, dropping the oldest queued events while the buffer
// is full. Only publishers send (serialised by b.mu), so this terminates.
func (s *busSub) deliver(ev Event) {
	for {
		select {
		case s.ch <- ev:
			return
		default:
		}
		select {
		case <-s.ch:
			s.dropped.Add(1)
		default:
		}
	}
}

// Close closes every subscription; later publishes are ignored.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, s := range b.subs {
		delete(b.subs, id)
		s.shut()
	}
}

// --- slog bridge ---

// LogHandler tees log records at or above Level onto the bus as KindLog
// events and passes every record on to Next.
type LogHandler struct {
	Bus   *Bus
	Next  slog.Handler
	Level slog.Leveler
	attrs []slog.Attr
	group string
}

// NewLogHandler returns a LogHandler; next may be nil.
func NewLogHandler(bus *Bus, next slog.Handler, level slog.Leveler) *LogHandler {
	return &LogHandler{Bus: bus, Next: next, Level: level}
}

func (h *LogHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.Level.Level() || (h.Next != nil && h.Next.Enabled(ctx, l))
}

func (h *LogHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= h.Level.Level() && h.Bus.HasSubscribers(KindLog) {
		ev := LogEvent{Time: r.Time.UnixMilli(), Level: r.Level.String(), Message: r.Message}
		add := func(a slog.Attr) {
			if ev.Attrs == nil {
				ev.Attrs = map[string]string{}
			}
			ev.Attrs[a.Key] = a.Value.Resolve().String()
		}
		for _, a := range h.attrs {
			add(a)
		}
		r.Attrs(func(a slog.Attr) bool {
			if h.group != "" {
				a.Key = h.group + "." + a.Key
			}
			add(a)
			return true
		})
		_ = h.Bus.Publish(KindLog, ev)
	}
	if h.Next != nil && h.Next.Enabled(ctx, r.Level) {
		return h.Next.Handle(ctx, r)
	}
	return nil
}

func (h *LogHandler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.attrs = slices.Clone(h.attrs)
	for _, a := range as {
		if h.group != "" {
			a.Key = h.group + "." + a.Key
		}
		c.attrs = append(c.attrs, a)
	}
	if h.Next != nil {
		c.Next = h.Next.WithAttrs(as)
	}
	return &c
}

func (h *LogHandler) WithGroup(name string) slog.Handler {
	c := *h
	if h.group != "" {
		c.group = h.group + "." + name
	} else {
		c.group = name
	}
	if h.Next != nil {
		c.Next = h.Next.WithGroup(name)
	}
	return &c
}
