// Package profiles is the agent's Windows multi-user profile core
// (MASTER_PLAN §7.8, api-v1 §6B, owner decision O6): local profile
// enumeration, active console user detection, the DPAPI-bound consent
// record store (state.db `profiles` table), the default access policy, the
// PUT /device/profiles report and the security-signal hook (Phase 3
// collectors plug into SignalSource).
//
// Policy: the active console user's profile becomes active_default when
// that user records the first-run consent; every other profile needs that
// user's own consent (authorised). The agent touches only profiles for
// which Allowed reports true. A blacklisted profile stays blocked until the
// server clears it (cleared_pending_consent) and the user consents again.
package profiles

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// StateDiscovered is the local-only state of a profile found in ProfileList
// that has neither consented nor been requested. It is never reported:
// api-v1 §6B has no matching state (see the P1-23 report).
const StateDiscovered = "discovered"

var (
	// ErrNotFound: no record for the SID.
	ErrNotFound = errors.New("profiles: profile not found")
	// ErrBlacklisted: the profile is blacklisted; consent is refused until
	// the server state is cleared_pending_consent.
	ErrBlacklisted = errors.New("profiles: profile is blacklisted")
	// ErrInvalidSID: not a user-account SID.
	ErrInvalidSID = errors.New("profiles: invalid profile SID")
	// ErrBadTransition: the requested state change is not allowed from the
	// current state.
	ErrBadTransition = errors.New("profiles: invalid state transition")
	// ErrUnsupported: not available on this platform.
	ErrUnsupported = errors.New("profiles: not supported on this platform")
	// ErrNoActiveUser: nobody is signed in at the console.
	ErrNoActiveUser = errors.New("profiles: no active console user")
)

// Record is one row of the state.db profiles table. Timestamps are unix ms;
// 0 means unset (NULL).
type Record struct {
	SID         string
	Username    string
	ProfilePath string
	State       string

	ConsentID   string
	ConsentAt   int64
	ConsentBlob []byte // DPAPI user-scope proof; never leaves the device
	ProofDigest string // hex SHA-256 of ConsentBlob, reported to the server

	RequestID    string
	RequestScope string // wire.ProfileRequestScope*
	RequestedBy  string

	BlacklistedAt   int64
	BlacklistReason string // wire.ProfileSignal*

	LastSecurityCheck    int64
	LastSecuritySignal   string
	LastSecurityEvidence string

	ReportedAt int64
	UpdatedAt  int64
}

// Consented reports whether the state grants access (api-v1 §6B rule 2).
func Consented(state string) bool {
	return state == wire.ProfileStateActiveDefault || state == wire.ProfileStateAuthorised
}

// Allowed reports whether the agent may read or write this profile: a
// consenting state with a recorded proof.
func (r *Record) Allowed() bool {
	return r != nil && Consented(r.State) && r.ConsentID != "" && r.ProofDigest != "" && len(r.ConsentBlob) > 0
}

// Store is the profiles table in state.db (migration 6, P0-22).
type Store struct{ DB *sql.DB }

const recordCols = `sid, username, profile_path, state, consent_id, consent_at, consent_blob, proof_digest,
	request_id, request_scope, requested_by, blacklisted_at, blacklist_reason,
	last_security_check, last_security_signal, last_security_evidence, reported_at, updated_at`

type querier interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func scanRecord(sc interface{ Scan(...any) error }) (*Record, error) {
	var r Record
	var consentAt, blAt, check, rep sql.NullInt64
	if err := sc.Scan(&r.SID, &r.Username, &r.ProfilePath, &r.State, &r.ConsentID, &consentAt, &r.ConsentBlob, &r.ProofDigest,
		&r.RequestID, &r.RequestScope, &r.RequestedBy, &blAt, &r.BlacklistReason,
		&check, &r.LastSecuritySignal, &r.LastSecurityEvidence, &rep, &r.UpdatedAt); err != nil {
		return nil, err
	}
	r.ConsentAt, r.BlacklistedAt, r.LastSecurityCheck, r.ReportedAt = consentAt.Int64, blAt.Int64, check.Int64, rep.Int64
	return &r, nil
}

func nullable(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func get(ctx context.Context, q querier, sid string) (*Record, error) {
	r, err := scanRecord(q.QueryRowContext(ctx, `SELECT `+recordCols+` FROM profiles WHERE sid = ?`, sid))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func put(ctx context.Context, q querier, r *Record) error {
	_, err := q.ExecContext(ctx, `INSERT INTO profiles(`+recordCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(sid) DO UPDATE SET username=excluded.username, profile_path=excluded.profile_path, state=excluded.state,
		consent_id=excluded.consent_id, consent_at=excluded.consent_at, consent_blob=excluded.consent_blob, proof_digest=excluded.proof_digest,
		request_id=excluded.request_id, request_scope=excluded.request_scope, requested_by=excluded.requested_by,
		blacklisted_at=excluded.blacklisted_at, blacklist_reason=excluded.blacklist_reason,
		last_security_check=excluded.last_security_check, last_security_signal=excluded.last_security_signal,
		last_security_evidence=excluded.last_security_evidence, reported_at=excluded.reported_at, updated_at=excluded.updated_at`,
		r.SID, r.Username, r.ProfilePath, r.State, r.ConsentID, nullable(r.ConsentAt), r.ConsentBlob, r.ProofDigest,
		r.RequestID, r.RequestScope, r.RequestedBy, nullable(r.BlacklistedAt), r.BlacklistReason,
		nullable(r.LastSecurityCheck), r.LastSecuritySignal, r.LastSecurityEvidence, nullable(r.ReportedAt), r.UpdatedAt)
	return err
}

// Get returns the record for sid or ErrNotFound.
func (s *Store) Get(ctx context.Context, sid string) (*Record, error) { return get(ctx, s.DB, sid) }

// List returns all records ordered by SID.
func (s *Store) List(ctx context.Context) ([]Record, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+recordCols+` FROM profiles ORDER BY sid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// Put inserts or replaces a record.
func (s *Store) Put(ctx context.Context, r *Record) error { return put(ctx, s.DB, r) }

// Update loads the record for sid (a zero Record with only SID set when
// missing; found reports which), applies fn and writes the result, all in
// one immediate transaction. fn returning an error aborts without writing.
func (s *Store) Update(ctx context.Context, sid string, fn func(r *Record, found bool) error) (*Record, error) {
	return s.update(ctx, sid, func(_ querier, r *Record, found bool) error { return fn(r, found) })
}

func (s *Store) update(ctx context.Context, sid string, fn func(q querier, r *Record, found bool) error) (*Record, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	r, err := get(ctx, tx, sid)
	found := err == nil
	if errors.Is(err, ErrNotFound) {
		r, err = &Record{SID: sid}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := fn(tx, r, found); err != nil {
		return nil, err
	}
	if err := put(ctx, tx, r); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("profiles: commit: %w", err)
	}
	return r, nil
}

// MarkReported sets reported_at on every record.
func (s *Store) MarkReported(ctx context.Context, at int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE profiles SET reported_at = ?`, at)
	return err
}

// hasActiveDefault reports whether a profile other than sid holds
// active_default.
func hasActiveDefault(ctx context.Context, q querier, except string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM profiles WHERE state = ? AND sid <> ?`,
		wire.ProfileStateActiveDefault, except).Scan(&n)
	return n > 0, err
}
