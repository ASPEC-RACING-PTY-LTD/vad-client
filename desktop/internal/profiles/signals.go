package profiles

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// SecuritySignal is a detected credential or identity change for a
// consented profile (§7.8, spike P0-S4). Signal and Evidence use the
// wire.ProfileSignal* / wire.ProfileEvidence* values. Detail is local-only
// diagnostic text (e.g. a Win32 error code), never sent and never secret.
type SecuritySignal struct {
	SID        string
	Signal     string
	Evidence   string
	ObservedAt time.Time
	Detail     string
}

// SignalSource is the Phase 3 hook: one collector of the P0-S4 signal set
// (R1 SAM last-set, R2 Security log, R3 SID/profile/disabled, R4 DPAPI proof,
// R5 domain pwdLastSet). Check evaluates one consented profile against its
// stored baseline and returns confirmed signals only (debouncing is the
// collector's job). It must not return a signal for a check it could not
// perform (e.g. ErrNeedSession).
type SignalSource interface {
	Name() string
	Check(ctx context.Context, rec Record) ([]SecuritySignal, error)
}

// EventSender posts POST /device/profiles/{sid}/security-event (injected;
// typically the outbox).
type EventSender func(ctx context.Context, sid string, ev wire.ProfileSecurityEventRequest) error

var validSignals = map[string]bool{
	wire.ProfileSignalPasswordChanged: true, wire.ProfileSignalPasswordReset: true, wire.ProfileSignalSIDChanged: true,
	wire.ProfileSignalAccountDisabled: true, wire.ProfileSignalAccountRemoved: true, wire.ProfileSignalProfileMoved: true,
	wire.ProfileSignalConsentUnverifiable: true,
}

var validEvidence = map[string]bool{
	wire.ProfileEvidenceLocalPasswordAge: true, wire.ProfileEvidenceDPAPIUnprotectFailed: true, wire.ProfileEvidenceLDAPPwdLastSet: true,
	wire.ProfileEvidenceEventLog4723: true, wire.ProfileEvidenceEventLog4724: true, wire.ProfileEvidenceSIDLookup: true,
}

// Validate checks the signal against api-v1 §6B.
func (s SecuritySignal) Validate() error {
	if !validSignals[s.Signal] || !validEvidence[s.Evidence] || s.ObservedAt.IsZero() {
		return fmt.Errorf("profiles: invalid security signal %q/%q", s.Signal, s.Evidence)
	}
	return nil
}

// ConsentSignal maps a VerifyConsentProof error to a *candidate* signal
// (spike P0-S3 §3 / P0-S4 R4). ok is false for success, ErrNeedSession and
// any error that is not about the proof itself (no signal). A key-state
// failure is a password_reset candidate; Phase 3 must confirm it on a second
// logon session before blacklisting.
func ConsentSignal(err error) (signal, evidence string, ok bool) {
	switch {
	case err == nil, errors.Is(err, ErrNeedSession):
		return "", "", false
	case errors.Is(err, ErrDPAPIKeyUnavailable):
		return wire.ProfileSignalPasswordReset, wire.ProfileEvidenceDPAPIUnprotectFailed, true
	case errors.Is(err, ErrConsentInvalid):
		return wire.ProfileSignalConsentUnverifiable, wire.ProfileEvidenceDPAPIUnprotectFailed, true
	}
	return "", "", false
}
