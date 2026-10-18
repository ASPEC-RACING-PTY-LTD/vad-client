//go:build windows

package profiles

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/tokens"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

func selfSID(t *testing.T) string {
	t.Helper()
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	if !tokens.IsUserSID(sid) {
		t.Skipf("current user %s is not a profile user", sid)
	}
	return sid
}

// TestConsentRoundTrip seals and verifies a proof as the current user (the
// UI and the session-token verification path, without SYSTEM).
func TestConsentRoundTrip(t *testing.T) {
	sid := selfSID(t)
	blob, err := CreateConsentProof("dev-1", sid)
	if err != nil {
		t.Fatal(err)
	}
	tok := &fakeToken{sid: sid, src: tokens.SourceSession}
	claim, err := VerifyConsentProof(tok, blob, "dev-1", sid)
	if err != nil {
		t.Fatal(err)
	}
	if !tok.called || claim.SID != sid || claim.DeviceID != "dev-1" || claim.Scope != ScopeProfile || len(claim.ConsentID) != 32 {
		t.Fatalf("claim = %+v", claim)
	}
	if err := CheckFresh(claim, time.Now()); err != nil {
		t.Error(err)
	}

	// Wrong device (entropy), tampered blob, other SID.
	if _, err := VerifyConsentProof(tok, blob, "dev-2", sid); !errors.Is(err, ErrConsentInvalid) {
		t.Errorf("wrong device = %v", err)
	}
	bad := append([]byte(nil), blob...)
	bad[len(bad)/2] ^= 0xff
	if _, err := VerifyConsentProof(tok, bad, "dev-1", sid); !errors.Is(err, ErrConsentInvalid) {
		t.Errorf("tampered = %v", err)
	}
	other := "S-1-5-21-1-2-3-4242"
	if _, err := VerifyConsentProof(&fakeToken{sid: other, src: tokens.SourceSession}, blob, "dev-1", other); !errors.Is(err, ErrConsentInvalid) {
		t.Errorf("other SID = %v", err)
	}
	if _, err := CreateConsentProof("dev-1", other); err == nil {
		t.Error("created a proof for another user")
	}
	if _, err := CreateConsentProof("", sid); err == nil {
		t.Error("empty device id accepted")
	}
	if ProofDigest(blob) == ProofDigest(bad) || len(ProofDigest(blob)) != 64 {
		t.Error("digest")
	}
}

func TestRecordConsentDPAPI(t *testing.T) {
	ctx := context.Background()
	sid := selfSID(t)
	m, sys := newManager(t)
	sys.active = ActiveUser{SessionID: 1, SID: sid}
	blob, err := CreateConsentProof(m.DeviceID, sid)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := m.RecordConsent(ctx, sid, blob)
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != wire.ProfileStateActiveDefault || rec.ProofDigest != ProofDigest(blob) || !m.Allowed(ctx, sid) {
		t.Fatalf("rec = %+v", rec)
	}
	if err := m.VerifyStored(ctx, sid); err != nil {
		t.Errorf("VerifyStored = %v", err)
	}
	rep := BuildReport([]Record{*rec})
	if len(rep.Consents) != 1 || rep.Consents[0].ConsentID != rec.ConsentID || rep.Consents[0].ProofDigest != ProofDigest(blob) {
		t.Errorf("report = %+v", rep)
	}

	// Renewal keeps active_default and replaces the consent.
	blob2, _ := CreateConsentProof(m.DeviceID, sid)
	rec2, err := m.RecordConsent(ctx, sid, blob2)
	if err != nil || rec2.State != wire.ProfileStateActiveDefault || rec2.ConsentID == rec.ConsentID {
		t.Fatalf("renew = %+v, %v", rec2, err)
	}

	// Proof sealed for another device is refused.
	foreign, _ := CreateConsentProof("dev-other", sid)
	if _, err := m.RecordConsent(ctx, sid, foreign); !errors.Is(err, ErrConsentInvalid) {
		t.Errorf("foreign device = %v", err)
	}
}

// TestRecordConsentNotActive: a user who is not at the console (or when
// another profile already holds active_default) gets authorised.
func TestRecordConsentNotActive(t *testing.T) {
	ctx := context.Background()
	sid := selfSID(t)
	m, sys := newManager(t)
	sys.active = ActiveUser{SessionID: 2, SID: sidB}
	blob, err := CreateConsentProof(m.DeviceID, sid)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := m.RecordConsent(ctx, sid, blob)
	if err != nil || rec.State != wire.ProfileStateAuthorised {
		t.Fatalf("rec = %+v, %v", rec, err)
	}

	m2, sys2 := newManager(t)
	sys2.active = ActiveUser{SID: sid}
	consent(t, m2, sidB, wire.ProfileStateActiveDefault)
	rec, err = m2.RecordConsent(ctx, sid, blob)
	if err != nil || rec.State != wire.ProfileStateAuthorised {
		t.Fatalf("second active_default: %+v, %v", rec, err)
	}

	// From cleared_pending_consent a fresh consent restores access.
	sig := SecuritySignal{SID: sid, Signal: wire.ProfileSignalPasswordReset, Evidence: wire.ProfileEvidenceDPAPIUnprotectFailed, ObservedAt: time.Now()}
	if err := m2.HandleSignal(ctx, sig); err != nil {
		t.Fatal(err)
	}
	if err := m2.MarkCleared(ctx, sid); err != nil {
		t.Fatal(err)
	}
	if rec, err = m2.RecordConsent(ctx, sid, blob); err != nil || !m2.Allowed(ctx, sid) {
		t.Fatalf("after clear: %+v, %v", rec, err)
	}
}

func TestOSSystem(t *testing.T) {
	sys := OSSystem{}
	profiles, err := sys.LocalProfiles()
	if err != nil {
		t.Fatal(err)
	}
	self := selfSID(t)
	found := false
	for _, p := range profiles {
		if !tokens.IsUserSID(p.SID) || p.ProfilePath == "" {
			t.Errorf("bad profile %+v", p)
		}
		if p.SID == self {
			found = true
		}
	}
	if !found {
		t.Errorf("own profile %s not in %+v", self, profiles)
	}
	au, err := sys.ActiveConsoleUser()
	if errors.Is(err, ErrNoActiveUser) {
		t.Skip("no console user")
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := windows.StringToSid(au.SID); err != nil || au.Username == "" {
		t.Errorf("active = %+v", au)
	}
}
