package engine

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/tokens"
)

// Owner identity (MASTER_PLAN §7.8, C7). In the agent service the engine
// runs as SYSTEM; every file-system read for a configuration (scan, stat,
// open for upload) runs impersonating the configuration's owner, so the
// engine never reads what that user could not. The work done under a token
// is file-system only: no logging, no state.db access, no network I/O and
// no goroutines (tokens.Token.Impersonate).

// ErrNoOwner is returned for a configuration without an owner while owner
// identity is enforced.
var ErrNoOwner = errors.New("this folder has no owner; open VaultDock as the user who owns it and save it again")

// OwnerError is returned when the owner's token cannot be obtained. The
// file work did not run.
type OwnerError struct {
	SID string
	Err error
}

func (e *OwnerError) Error() string {
	return fmt.Sprintf("cannot act as the folder's owner: %v", e.Err)
}

func (e *OwnerError) Unwrap() error { return e.Err }

// IsOwnerError reports whether err means the owner's identity was not
// available (no token, or no owner at all).
func IsOwnerError(err error) bool {
	var oe *OwnerError
	return errors.As(err, &oe) || errors.Is(err, ErrNoOwner)
}

// ownerTokenTTL bounds how long one owner token is reused, so a session
// token replaces an S4U token soon after the user logs on (and the other
// way round). One token serves all of the owner's work meanwhile: each S4U
// token is a new LSA logon, never mint one per file.
var ownerTokenTTL = 5 * time.Minute

type cachedToken struct {
	tok    tokens.Token
	at     time.Time
	refs   int
	stale  bool
	closed bool
}

// identity caches owner tokens per SID.
type identity struct {
	mu   sync.Mutex
	p    tokens.Provider // nil: not enforced (per-user app, tests)
	toks map[string]*cachedToken
}

// SetTokens turns owner identity on (p != nil) or off. With it on, a
// configuration's files are only ever read as its OwnerSID; configurations
// without an owner are not synchronised.
func (e *Engine) SetTokens(p tokens.Provider) {
	e.id.mu.Lock()
	old := e.id.toks
	e.id.p, e.id.toks = p, map[string]*cachedToken{}
	e.id.mu.Unlock()
	for _, ct := range old {
		e.id.retire(ct)
	}
}

// EnforcesOwner reports whether owner identity is on.
func (e *Engine) EnforcesOwner() bool {
	e.id.mu.Lock()
	defer e.id.mu.Unlock()
	return e.id.p != nil
}

// asOwner runs fn as cfg's owner. With owner identity off it runs fn
// directly. fn's error is returned unchanged; token problems come back as
// *OwnerError or ErrNoOwner (see IsOwnerError).
func (e *Engine) asOwner(cfg *config.SyncConfig, fn func() error) error {
	return e.asSID(cfg.OwnerSID, fn)
}

func (e *Engine) asSID(sid string, fn func() error) error {
	e.id.mu.Lock()
	p := e.id.p
	e.id.mu.Unlock()
	if p == nil {
		return fn()
	}
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return ErrNoOwner
	}
	ct, err := e.id.acquire(sid)
	if err != nil {
		return &OwnerError{SID: sid, Err: err}
	}
	defer e.id.release(ct)
	return ct.tok.Impersonate(fn)
}

func (id *identity) acquire(sid string) (*cachedToken, error) {
	key := strings.ToUpper(sid)
	id.mu.Lock()
	if ct := id.toks[key]; ct != nil && !ct.stale && time.Since(ct.at) < ownerTokenTTL {
		ct.refs++
		id.mu.Unlock()
		return ct, nil
	}
	p := id.p
	id.mu.Unlock()

	tok, err := p.ForProfile(sid)
	if err != nil {
		return nil, err
	}
	fresh := &cachedToken{tok: tok, at: time.Now(), refs: 1}
	id.mu.Lock()
	if id.p != p { // SetTokens ran meanwhile
		id.mu.Unlock()
		_ = tok.Close()
		return nil, errors.New("owner identity was reconfigured")
	}
	old := id.toks[key]
	id.toks[key] = fresh
	id.mu.Unlock()
	if old != nil {
		id.retire(old)
	}
	return fresh, nil
}

func (id *identity) release(ct *cachedToken) {
	id.mu.Lock()
	ct.refs--
	closeNow := ct.stale && ct.refs == 0 && !ct.closed
	if closeNow {
		ct.closed = true
	}
	id.mu.Unlock()
	if closeNow {
		_ = ct.tok.Close()
	}
}

// retire marks ct stale and closes it once nobody uses it.
func (id *identity) retire(ct *cachedToken) {
	id.mu.Lock()
	ct.stale = true
	closeNow := ct.refs == 0 && !ct.closed
	if closeNow {
		ct.closed = true
	}
	id.mu.Unlock()
	if closeNow {
		_ = ct.tok.Close()
	}
}
