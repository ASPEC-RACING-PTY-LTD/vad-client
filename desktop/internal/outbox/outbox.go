// Package outbox is a durable queue of pending server reports (transfer
// reports, run reports, issues) kept in the state.db outbox table. Items are
// delivered at least once through a Sender callback, retried with exponential
// backoff, and the queue is capped by dropping the oldest items.
package outbox

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Defaults for Options fields left zero.
const (
	DefaultMaxItems  = 10000
	DefaultBaseDelay = 5 * time.Second
	DefaultMaxDelay  = 15 * time.Minute
	DefaultInterval  = 10 * time.Second
	DefaultBatch     = 50
)

// Drop reasons passed to Options.OnDrop.
const (
	DropOverflow = "overflow" // the queue exceeded MaxItems; oldest removed
	DropRejected = "rejected" // the sender returned a Permanent error
)

// Sender delivers one item. A nil error removes the item; an error wrapped
// with Permanent drops it; any other error schedules a retry. The item's
// idempotency key is available through KeyFromContext.
type Sender func(ctx context.Context, kind string, payload []byte) error

// Options tune an Outbox. Zero values select the defaults above.
type Options struct {
	MaxItems  int
	BaseDelay time.Duration
	MaxDelay  time.Duration
	Interval  time.Duration // Run's flush period
	Batch     int           // items read per query during Flush
	// OnDrop is called (outside any transaction) when items are discarded
	// without delivery, for example to record an issue.
	OnDrop func(reason, kind string, n int)
	Log    *slog.Logger
}

// Outbox is safe for concurrent use.
type Outbox struct {
	db   *sql.DB
	send Sender
	opt  Options
	now  func() time.Time
	kick chan struct{}

	flushMu sync.Mutex // one Flush at a time
}

// New returns an outbox stored in db, which must contain the outbox table
// (state.db migration 3).
func New(db *sql.DB, send Sender, opt Options) *Outbox {
	if opt.MaxItems <= 0 {
		opt.MaxItems = DefaultMaxItems
	}
	if opt.BaseDelay <= 0 {
		opt.BaseDelay = DefaultBaseDelay
	}
	if opt.MaxDelay <= 0 {
		opt.MaxDelay = DefaultMaxDelay
	}
	if opt.Interval <= 0 {
		opt.Interval = DefaultInterval
	}
	if opt.Batch <= 0 {
		opt.Batch = DefaultBatch
	}
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	return &Outbox{db: db, send: send, opt: opt, now: time.Now, kick: make(chan struct{}, 1)}
}

type permanentError struct{ err error }

func (p permanentError) Error() string { return p.err.Error() }
func (p permanentError) Unwrap() error { return p.err }

// Permanent marks a Sender error as final: the item is dropped, not retried.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err}
}

// IsPermanent reports whether err was wrapped with Permanent.
func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

type keyCtx struct{}

// KeyFromContext returns the idempotency key of the item being sent.
func KeyFromContext(ctx context.Context) string {
	k, _ := ctx.Value(keyCtx{}).(string)
	return k
}

// NewKey returns a random idempotency key.
func NewKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Enqueue stores an item for delivery. key is its idempotency key: an item
// whose key is already queued is ignored. An empty key gets a random one.
// When the queue grows past MaxItems the oldest items are dropped.
func (o *Outbox) Enqueue(kind, key string, payload []byte) error {
	if kind == "" {
		return errors.New("outbox: empty kind")
	}
	if key == "" {
		key = NewKey()
	}
	tx, err := o.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t := o.now().UnixMilli()
	if _, err := tx.Exec(`INSERT INTO outbox (kind, method, path, body, dedupe_key, next_at, created_at)
VALUES (?, '', '', ?, ?, ?, ?) ON CONFLICT (dedupe_key) DO NOTHING`, kind, payload, key, t, t); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM outbox WHERE id IN (
	SELECT id FROM outbox ORDER BY id LIMIT max(0, (SELECT COUNT(*) FROM outbox) - ?))`, o.opt.MaxItems)
	if err != nil {
		return err
	}
	dropped, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return err
	}
	if dropped > 0 {
		o.dropped(DropOverflow, "", int(dropped))
	}
	o.Kick()
	return nil
}

func (o *Outbox) dropped(reason, kind string, n int) {
	o.opt.Log.Warn("outbox items dropped", "reason", reason, "kind", kind, "count", n)
	if o.opt.OnDrop != nil {
		o.opt.OnDrop(reason, kind, n)
	}
}

// Len returns the number of queued items.
func (o *Outbox) Len() (int, error) {
	var n int
	err := o.db.QueryRow("SELECT COUNT(*) FROM outbox").Scan(&n)
	return n, err
}

// Kick asks a running Run loop to flush now.
func (o *Outbox) Kick() {
	select {
	case o.kick <- struct{}{}:
	default:
	}
}

// backoff is the delay after the given number of failed attempts.
func (o *Outbox) backoff(attempts int) time.Duration {
	d := o.opt.BaseDelay
	for i := 1; i < attempts && d < o.opt.MaxDelay; i++ {
		d *= 2
	}
	return min(d, o.opt.MaxDelay)
}

type item struct {
	id       int64
	kind     string
	key      string
	body     []byte
	attempts int
}

// Flush delivers ready items in order until none are left, the context ends
// or a delivery fails (the server is then likely unavailable; the failed item
// is rescheduled with backoff). It returns the number delivered. A Flush that
// is already running makes this call return immediately.
func (o *Outbox) Flush(ctx context.Context) (int, error) {
	if !o.flushMu.TryLock() {
		return 0, nil
	}
	defer o.flushMu.Unlock()
	sent := 0
	for {
		items, err := o.ready()
		if err != nil || len(items) == 0 {
			return sent, err
		}
		for _, it := range items {
			if err := ctx.Err(); err != nil {
				return sent, err
			}
			err := o.send(context.WithValue(ctx, keyCtx{}, it.key), it.kind, it.body)
			switch {
			case err == nil:
				if _, err := o.db.Exec("DELETE FROM outbox WHERE id = ?", it.id); err != nil {
					return sent, err
				}
				sent++
			case IsPermanent(err):
				if _, derr := o.db.Exec("DELETE FROM outbox WHERE id = ?", it.id); derr != nil {
					return sent, derr
				}
				o.opt.Log.Warn("outbox item rejected", "kind", it.kind, "err", err)
				o.dropped(DropRejected, it.kind, 1)
			case ctx.Err() != nil:
				return sent, ctx.Err() // cancelled mid-send: not counted as an attempt
			default:
				next := o.now().Add(o.backoff(it.attempts + 1)).UnixMilli()
				msg := err.Error()
				if len(msg) > 500 {
					msg = msg[:500]
				}
				if _, uerr := o.db.Exec("UPDATE outbox SET attempts = attempts + 1, next_at = ?, last_error = ? WHERE id = ?", next, msg, it.id); uerr != nil {
					return sent, uerr
				}
				return sent, fmt.Errorf("outbox: deliver %s: %w", it.kind, err)
			}
		}
	}
}

func (o *Outbox) ready() ([]item, error) {
	rows, err := o.db.Query(`SELECT id, kind, COALESCE(dedupe_key, ''), body, attempts FROM outbox
WHERE next_at <= ? ORDER BY next_at, id LIMIT ?`, o.now().UnixMilli(), o.opt.Batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.kind, &it.key, &it.body, &it.attempts); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// Run flushes every Interval, and on Kick, until ctx ends.
func (o *Outbox) Run(ctx context.Context) {
	t := time.NewTicker(o.opt.Interval)
	defer t.Stop()
	for {
		if _, err := o.Flush(ctx); err != nil && ctx.Err() == nil {
			o.opt.Log.Debug("outbox flush", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-o.kick:
		}
	}
}
