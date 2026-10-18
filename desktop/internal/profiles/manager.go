package profiles

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/tokens"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// Manager applies the profile policy over the Store. All dependencies are
// injected; zero-value optional fields are skipped.
type Manager struct {
	Store    *Store
	Sys      System          // OS view (OSSystem{} in production)
	Tokens   tokens.Provider // tokens.Default in production
	DeviceID string          // binds consent proofs to this device

	SendReport ReportSender   // PUT /device/profiles
	SendEvent  EventSender    // POST /device/profiles/{sid}/security-event
	Signals    []SignalSource // Phase 3 collectors
	Log        *slog.Logger
	Now        func() time.Time
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

// Refresh enumerates local profiles and records new ones as
// StateDiscovered. Usernames are updated (a rename keeps the SID); the
// stored profile path of a consented profile is kept as the security
// baseline (a change is a Phase 3 profile_moved signal, not a silent
// update). Records whose profile disappeared are left untouched.
func (m *Manager) Refresh(ctx context.Context) error {
	locals, err := m.Sys.LocalProfiles()
	if err != nil {
		return err
	}
	ts := m.now().UnixMilli()
	for _, lp := range locals {
		if !tokens.IsUserSID(lp.SID) {
			continue
		}
		_, err := m.Store.Update(ctx, lp.SID, func(r *Record, found bool) error {
			if !found {
				r.State = StateDiscovered
				r.ProfilePath = lp.ProfilePath
			} else if (!Consented(r.State) && r.State != wire.ProfileStateBlacklisted) || r.ProfilePath == "" {
				r.ProfilePath = lp.ProfilePath
			}
			if lp.Username != "" {
				r.Username = lp.Username
			}
			r.UpdatedAt = ts
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Allowed reports whether the agent may touch the profile (api-v1 §6B rule
// 2). Errors are treated as "no".
func (m *Manager) Allowed(ctx context.Context, sid string) bool {
	r, err := m.Store.Get(ctx, sid)
	return err == nil && r.Allowed()
}

// RecordConsent handles a consent proof sent by the UI over IPC. callerSID
// is the pipe client's SID (ipc caller in ctx), never a value from the
// request body. The proof is verified under the caller's session token
// (the user is signed in right now). The new state is active_default when
// the caller is the active console user and no other profile holds
// active_default, otherwise authorised. Blacklisted profiles are refused.
func (m *Manager) RecordConsent(ctx context.Context, callerSID string, blob []byte) (*Record, error) {
	if !tokens.IsUserSID(callerSID) {
		return nil, ErrInvalidSID
	}
	if len(blob) == 0 || len(blob) > MaxProofSize {
		return nil, fmt.Errorf("%w: bad size", ErrConsentInvalid)
	}
	if cur, err := m.Store.Get(ctx, callerSID); err == nil && cur.State == wire.ProfileStateBlacklisted {
		return nil, ErrBlacklisted
	}
	tok, err := m.Tokens.SessionOnly(callerSID)
	if err != nil {
		if errors.Is(err, tokens.ErrNotLoggedOn) {
			return nil, fmt.Errorf("%w: %v", ErrNeedSession, err)
		}
		return nil, err
	}
	defer tok.Close()
	claim, err := VerifyConsentProof(tok, blob, m.DeviceID, callerSID)
	if err != nil {
		return nil, err
	}
	now := m.now()
	if err := CheckFresh(claim, now); err != nil {
		return nil, err
	}
	activeSID := ""
	if au, err := m.Sys.ActiveConsoleUser(); err == nil {
		activeSID = au.SID
	}
	rec, err := m.Store.update(ctx, callerSID, func(q querier, r *Record, found bool) error {
		if r.State == wire.ProfileStateBlacklisted {
			return ErrBlacklisted
		}
		next := wire.ProfileStateAuthorised
		if callerSID == activeSID {
			other, err := hasActiveDefault(ctx, q, callerSID)
			if err != nil {
				return err
			}
			if !other {
				next = wire.ProfileStateActiveDefault
			}
		}
		if r.State == wire.ProfileStateActiveDefault {
			next = r.State // renewing keeps active_default
		}
		r.State = next
		r.ConsentID, r.ConsentAt = claim.ConsentID, claim.Time
		r.ConsentBlob, r.ProofDigest = append([]byte(nil), blob...), ProofDigest(blob)
		r.RequestID, r.RequestScope, r.RequestedBy = "", "", ""
		r.UpdatedAt = now.UnixMilli()
		return nil
	})
	if err != nil {
		return nil, err
	}
	m.log().Info("profile consent recorded", "sid", callerSID, "state", rec.State, "consent_id", rec.ConsentID)
	return rec, nil
}

// VerifyStored re-verifies the stored proof for sid under the user's
// session token (service start, every 6 h, logon, before a job). It returns
// ErrNeedSession when the user is not signed in; that is "not checked",
// never a signal. Callers map other errors with ConsentSignal.
func (m *Manager) VerifyStored(ctx context.Context, sid string) error {
	r, err := m.Store.Get(ctx, sid)
	if err != nil {
		return err
	}
	if len(r.ConsentBlob) == 0 {
		return fmt.Errorf("%w: no stored proof", ErrConsentInvalid)
	}
	tok, err := m.Tokens.SessionOnly(sid)
	if err != nil {
		if errors.Is(err, tokens.ErrNotLoggedOn) {
			return ErrNeedSession
		}
		return err
	}
	defer tok.Close()
	claim, err := VerifyConsentProof(tok, r.ConsentBlob, m.DeviceID, sid)
	if err != nil {
		return err
	}
	if !eq(claim.ConsentID, r.ConsentID) {
		return fmt.Errorf("%w: consent id mismatch", ErrConsentInvalid)
	}
	return nil
}

// ApplyConsentRequest records an admin request (profile_consent_request
// device task). Targeted profiles that are discovered or revoked become
// requested; consented, blacklisted and cleared profiles keep their state.
// Nothing is read: the user must still consent locally.
func (m *Manager) ApplyConsentRequest(ctx context.Context, p wire.TaskProfileConsentRequestPayload) error {
	var targets []string
	scope := wire.ProfileRequestScopeSelected
	if p.SIDs.All {
		scope = wire.ProfileRequestScopeAll
		recs, err := m.Store.List(ctx)
		if err != nil {
			return err
		}
		for _, r := range recs {
			targets = append(targets, r.SID)
		}
	} else {
		targets = p.SIDs.SIDs
	}
	ts := m.now().UnixMilli()
	for _, sid := range targets {
		if !tokens.IsUserSID(sid) {
			continue
		}
		if _, err := m.Store.Update(ctx, sid, func(r *Record, found bool) error {
			switch r.State {
			case "", StateDiscovered, wire.ProfileStateRevoked, wire.ProfileStateRequested:
				r.State = wire.ProfileStateRequested
				r.RequestID, r.RequestScope, r.RequestedBy = p.RequestID, scope, p.RequestedBy
			}
			r.UpdatedAt = ts
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// Revoke withdraws consent (or a pending request) locally: the profile
// becomes revoked and the proof is discarded.
func (m *Manager) Revoke(ctx context.Context, sid string) error {
	_, err := m.Store.Update(ctx, sid, func(r *Record, found bool) error {
		if !found {
			return ErrNotFound
		}
		if !Consented(r.State) && r.State != wire.ProfileStateRequested {
			return fmt.Errorf("%w: %s -> revoked", ErrBadTransition, r.State)
		}
		r.State = wire.ProfileStateRevoked
		r.ConsentID, r.ConsentAt, r.ConsentBlob, r.ProofDigest = "", 0, nil, ""
		r.UpdatedAt = m.now().UnixMilli()
		return nil
	})
	return err
}

// MarkCleared applies a superuser clear seen from the server: blacklisted
// becomes cleared_pending_consent. Access still needs a new local consent.
func (m *Manager) MarkCleared(ctx context.Context, sid string) error {
	_, err := m.Store.Update(ctx, sid, func(r *Record, found bool) error {
		if r.State != wire.ProfileStateBlacklisted {
			return fmt.Errorf("%w: %s -> %s", ErrBadTransition, r.State, wire.ProfileStateClearedPendingConsent)
		}
		r.State = wire.ProfileStateClearedPendingConsent
		r.ConsentBlob, r.ProofDigest, r.ConsentID, r.ConsentAt = nil, "", "", 0
		r.UpdatedAt = m.now().UnixMilli()
		return nil
	})
	return err
}

// HandleSignal blacklists the profile locally first (all work stops at
// once, Allowed turns false) and then posts the security event. A send
// failure is returned; the local block holds meanwhile.
func (m *Manager) HandleSignal(ctx context.Context, s SecuritySignal) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if !tokens.IsUserSID(s.SID) {
		return ErrInvalidSID
	}
	at := s.ObservedAt.UnixMilli()
	if _, err := m.Store.Update(ctx, s.SID, func(r *Record, found bool) error {
		if r.State != wire.ProfileStateBlacklisted {
			r.State = wire.ProfileStateBlacklisted
			r.BlacklistedAt = m.now().UnixMilli()
			r.BlacklistReason = s.Signal
		}
		r.LastSecuritySignal, r.LastSecurityEvidence = s.Signal, s.Evidence
		r.LastSecurityCheck = m.now().UnixMilli()
		r.UpdatedAt = r.LastSecurityCheck
		return nil
	}); err != nil {
		return err
	}
	m.log().Warn("profile blacklisted after security signal", "sid", s.SID, "signal", s.Signal, "evidence", s.Evidence, "detail", s.Detail)
	if m.SendEvent == nil {
		return nil
	}
	return m.SendEvent(ctx, s.SID, wire.ProfileSecurityEventRequest{Signal: s.Signal, ObservedAt: at, Evidence: s.Evidence})
}

// CheckSecurity runs every SignalSource over each allowed profile and
// handles the signals they return. Collector errors are logged and do not
// stop the other checks.
func (m *Manager) CheckSecurity(ctx context.Context) error {
	recs, err := m.Store.List(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range recs {
		if !r.Allowed() {
			continue
		}
		for _, src := range m.Signals {
			sigs, err := src.Check(ctx, r)
			if err != nil {
				m.log().Warn("profile security check failed", "source", src.Name(), "sid", r.SID, "err", err)
				continue
			}
			for _, s := range sigs {
				s.SID = r.SID
				if err := m.HandleSignal(ctx, s); err != nil {
					errs = append(errs, err)
				}
			}
			if len(sigs) > 0 {
				break // blacklisted; further checks are moot
			}
		}
		ts := m.now().UnixMilli()
		if _, err := m.Store.Update(ctx, r.SID, func(rr *Record, _ bool) error {
			rr.LastSecurityCheck = ts
			return nil
		}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Report builds the profile report and sends it with SendReport, then
// marks every record reported.
func (m *Manager) Report(ctx context.Context) (wire.ProfileReport, error) {
	recs, err := m.Store.List(ctx)
	if err != nil {
		return wire.ProfileReport{}, err
	}
	rep := BuildReport(recs)
	if m.SendReport == nil {
		return rep, nil
	}
	if err := m.SendReport(ctx, rep); err != nil {
		return rep, err
	}
	return rep, m.Store.MarkReported(ctx, m.now().UnixMilli())
}
