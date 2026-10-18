package profiles

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/tokens"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

const (
	sidA = "S-1-5-21-1000-2000-3000-1001"
	sidB = "S-1-5-21-1000-2000-3000-1002"
	sidC = "S-1-12-1-111-222-333-444"
)

type fakeSys struct {
	active ActiveUser
	err    error
	locals []LocalProfile
}

func (f *fakeSys) ActiveConsoleUser() (ActiveUser, error) {
	if f.err != nil {
		return ActiveUser{}, f.err
	}
	return f.active, nil
}

func (f *fakeSys) LocalProfiles() ([]LocalProfile, error) { return f.locals, nil }

// fakeToken impersonates by running fn in the current context, which is the
// test user's own session; that is what the service sees under a real
// session token.
type fakeToken struct {
	sid    string
	src    tokens.Source
	called bool
}

func (t *fakeToken) SID() string                                  { return t.sid }
func (t *fakeToken) Source() tokens.Source                        { return t.src }
func (t *fakeToken) SessionID() (uint32, bool)                    { return 1, t.src == tokens.SourceSession }
func (t *fakeToken) Caps() tokens.Caps                            { return tokens.CapsFor(t.src) }
func (t *fakeToken) AccessCheckPath(string, uint32) (bool, error) { return true, nil }
func (t *fakeToken) Close() error                                 { return nil }
func (t *fakeToken) Impersonate(fn func() error) error {
	t.called = true
	return fn()
}

type fakeProvider struct {
	src       tokens.Source
	loggedOff bool
}

func (p *fakeProvider) ForProfile(sid string) (tokens.Token, error) {
	return &fakeToken{sid: sid, src: p.src}, nil
}

func (p *fakeProvider) SessionOnly(sid string) (tokens.Token, error) {
	if p.loggedOff {
		return nil, &tokens.Error{SID: sid, Err: tokens.ErrNotLoggedOn}
	}
	return &fakeToken{sid: sid, src: tokens.SourceSession}, nil
}

func newManager(t *testing.T) (*Manager, *fakeSys) {
	t.Helper()
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sys := &fakeSys{
		active: ActiveUser{SessionID: 1, SID: sidA, Username: `PC\alice`},
		locals: []LocalProfile{
			{SID: sidA, Username: `PC\alice`, ProfilePath: `C:\Users\alice`},
			{SID: sidB, Username: `PC\bob`, ProfilePath: `C:\Users\bob`},
			{SID: "S-1-5-18", ProfilePath: `C:\Windows\system32\config\systemprofile`},
		},
	}
	return &Manager{Store: &Store{DB: st.DB}, Sys: sys, Tokens: &fakeProvider{src: tokens.SourceSession}, DeviceID: "dev-1"}, sys
}

// consent writes a consented record directly (the DPAPI path is covered by
// the Windows tests).
func consent(t *testing.T, m *Manager, sid, st string) {
	t.Helper()
	if _, err := m.Store.Update(context.Background(), sid, func(r *Record, _ bool) error {
		r.State, r.ConsentID, r.ConsentAt, r.ConsentBlob, r.ProofDigest = st, "c-"+sid, 1000, []byte("blob"), ProofDigest([]byte("blob"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func stateOf(t *testing.T, m *Manager, sid string) string {
	t.Helper()
	r, err := m.Store.Get(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	return r.State
}

func TestRefreshDiscovers(t *testing.T) {
	ctx := context.Background()
	m, sys := newManager(t)
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	recs, err := m.Store.List(ctx)
	if err != nil || len(recs) != 2 {
		t.Fatalf("recs = %+v, %v", recs, err)
	}
	for _, r := range recs {
		if r.State != StateDiscovered || r.Allowed() {
			t.Errorf("%s: state %q", r.SID, r.State)
		}
	}
	if m.Allowed(ctx, sidA) {
		t.Error("discovered profile must not be allowed")
	}

	// A consented profile keeps its path baseline; usernames follow renames.
	consent(t, m, sidA, wire.ProfileStateActiveDefault)
	sys.locals[0] = LocalProfile{SID: sidA, Username: `PC\alice2`, ProfilePath: `D:\moved`}
	sys.locals[1].ProfilePath = `C:\Users\bob.new`
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ := m.Store.Get(ctx, sidA)
	b, _ := m.Store.Get(ctx, sidB)
	if a.ProfilePath != `C:\Users\alice` || a.Username != `PC\alice2` || a.State != wire.ProfileStateActiveDefault {
		t.Errorf("a = %+v", a)
	}
	if b.ProfilePath != `C:\Users\bob.new` {
		t.Errorf("b = %+v", b)
	}
}

func TestConsentRequestAndRevoke(t *testing.T) {
	ctx := context.Background()
	m, _ := newManager(t)
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	consent(t, m, sidA, wire.ProfileStateActiveDefault)
	req := wire.TaskProfileConsentRequestPayload{RequestID: "r1", RequestedBy: "admin", SIDs: wire.TaskSIDs{All: true}}
	if err := m.ApplyConsentRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, m, sidA); got != wire.ProfileStateActiveDefault {
		t.Errorf("consented profile changed to %q", got)
	}
	b, _ := m.Store.Get(ctx, sidB)
	if b.State != wire.ProfileStateRequested || b.RequestScope != wire.ProfileRequestScopeAll || b.RequestID != "r1" || b.Allowed() {
		t.Errorf("b = %+v", b)
	}

	// Selected request for an SID not on disk yet; invalid SIDs ignored.
	sel := wire.TaskProfileConsentRequestPayload{RequestID: "r2", SIDs: wire.TaskSIDs{SIDs: []string{sidC, "S-1-5-18", "junk"}}}
	if err := m.ApplyConsentRequest(ctx, sel); err != nil {
		t.Fatal(err)
	}
	c, err := m.Store.Get(ctx, sidC)
	if err != nil || c.State != wire.ProfileStateRequested || c.RequestScope != wire.ProfileRequestScopeSelected {
		t.Errorf("c = %+v, %v", c, err)
	}
	if _, err := m.Store.Get(ctx, "S-1-5-18"); !errors.Is(err, ErrNotFound) {
		t.Errorf("service SID recorded: %v", err)
	}

	if err := m.Revoke(ctx, sidA); err != nil {
		t.Fatal(err)
	}
	a, _ := m.Store.Get(ctx, sidA)
	if a.State != wire.ProfileStateRevoked || a.ConsentBlob != nil || a.ProofDigest != "" || m.Allowed(ctx, sidA) {
		t.Errorf("a = %+v", a)
	}
	if err := m.Revoke(ctx, sidA); !errors.Is(err, ErrBadTransition) {
		t.Errorf("double revoke = %v", err)
	}
	if err := m.Revoke(ctx, "S-1-5-21-9-9-9-9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown revoke = %v", err)
	}
	// A new request re-opens a revoked profile.
	if err := m.ApplyConsentRequest(ctx, wire.TaskProfileConsentRequestPayload{RequestID: "r3", SIDs: wire.TaskSIDs{SIDs: []string{sidA}}}); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, m, sidA); got != wire.ProfileStateRequested {
		t.Errorf("revoked + request = %q", got)
	}
}

type fakeSource struct {
	sigs []SecuritySignal
	err  error
	seen []string
}

func (f *fakeSource) Name() string { return "fake" }

func (f *fakeSource) Check(_ context.Context, r Record) ([]SecuritySignal, error) {
	f.seen = append(f.seen, r.SID)
	return f.sigs, f.err
}

func TestSecuritySignalBlacklists(t *testing.T) {
	ctx := context.Background()
	m, _ := newManager(t)
	consent(t, m, sidA, wire.ProfileStateActiveDefault)
	consent(t, m, sidB, wire.ProfileStateAuthorised)
	var sent []wire.ProfileSecurityEventRequest
	m.SendEvent = func(_ context.Context, sid string, ev wire.ProfileSecurityEventRequest) error {
		if sid != sidB {
			t.Errorf("event for %s", sid)
		}
		sent = append(sent, ev)
		return nil
	}
	obs := time.UnixMilli(1_700_000_000_000)
	quiet := &fakeSource{}
	broken := &fakeSource{err: errors.New("collector down")}
	m.Signals = []SignalSource{broken, quiet}
	if err := m.CheckSecurity(ctx); err != nil {
		t.Fatal(err)
	}
	if len(quiet.seen) != 2 || len(sent) != 0 {
		t.Fatalf("seen %v sent %v", quiet.seen, sent)
	}

	// Invalid signals are rejected without touching state.
	if err := m.HandleSignal(ctx, SecuritySignal{SID: sidB, Signal: "pin_changed", Evidence: "x", ObservedAt: obs}); err == nil {
		t.Error("invalid signal accepted")
	}
	if !m.Allowed(ctx, sidB) {
		t.Fatal("b should still be allowed")
	}

	sig := SecuritySignal{SID: sidB, Signal: wire.ProfileSignalPasswordReset, Evidence: wire.ProfileEvidenceEventLog4724, ObservedAt: obs}
	if err := m.HandleSignal(ctx, sig); err != nil {
		t.Fatal(err)
	}
	b, _ := m.Store.Get(ctx, sidB)
	if b.State != wire.ProfileStateBlacklisted || b.BlacklistReason != wire.ProfileSignalPasswordReset || b.BlacklistedAt == 0 || m.Allowed(ctx, sidB) {
		t.Errorf("b = %+v", b)
	}
	if len(sent) != 1 || sent[0].ObservedAt != obs.UnixMilli() || sent[0].Signal != sig.Signal || sent[0].Evidence != sig.Evidence {
		t.Errorf("sent = %+v", sent)
	}

	// Blacklisted: a consent request does not change it; clear -> pending.
	if err := m.ApplyConsentRequest(ctx, wire.TaskProfileConsentRequestPayload{SIDs: wire.TaskSIDs{SIDs: []string{sidB}}}); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, m, sidB); got != wire.ProfileStateBlacklisted {
		t.Errorf("request changed blacklisted to %q", got)
	}
	if err := m.MarkCleared(ctx, sidA); !errors.Is(err, ErrBadTransition) {
		t.Errorf("clear of non-blacklisted = %v", err)
	}
	if err := m.MarkCleared(ctx, sidB); err != nil {
		t.Fatal(err)
	}
	b, _ = m.Store.Get(ctx, sidB)
	if b.State != wire.ProfileStateClearedPendingConsent || m.Allowed(ctx, sidB) || b.ProofDigest != "" {
		t.Errorf("cleared b = %+v", b)
	}

	// A source that fires through CheckSecurity blacklists the profile and
	// stamps last_security_check.
	loud := &fakeSource{sigs: []SecuritySignal{{Signal: wire.ProfileSignalSIDChanged, Evidence: wire.ProfileEvidenceSIDLookup, ObservedAt: obs}}}
	m.Signals = []SignalSource{loud, quiet}
	m.SendEvent = nil
	if err := m.CheckSecurity(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ := m.Store.Get(ctx, sidA)
	if a.State != wire.ProfileStateBlacklisted || a.LastSecuritySignal != wire.ProfileSignalSIDChanged || a.LastSecurityCheck == 0 {
		t.Errorf("a = %+v", a)
	}
}

func TestReport(t *testing.T) {
	ctx := context.Background()
	m, _ := newManager(t)
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	consent(t, m, sidB, wire.ProfileStateAuthorised)
	// A consenting state without a proof is never claimed.
	if _, err := m.Store.Update(ctx, sidC, func(r *Record, _ bool) error {
		r.State = wire.ProfileStateAuthorised
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var got wire.ProfileReport
	m.SendReport = func(_ context.Context, rep wire.ProfileReport) error { got = rep; return nil }
	if _, err := m.Report(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got.Profiles) != 1 || got.Profiles[0].SID != sidB || got.Profiles[0].State != wire.ProfileStateAuthorised ||
		got.Profiles[0].ProfilePath != `C:\Users\bob` {
		t.Fatalf("profiles = %+v", got.Profiles)
	}
	want := wire.ProfileConsentRecord{ConsentID: "c-" + sidB, SID: sidB, ConsentAt: 1000, ProofDigest: ProofDigest([]byte("blob"))}
	if len(got.Consents) != 1 || got.Consents[0] != want || len(want.ProofDigest) != 64 {
		t.Fatalf("consents = %+v", got.Consents)
	}
	r, _ := m.Store.Get(ctx, sidB)
	if r.ReportedAt == 0 {
		t.Error("reported_at not set")
	}

	// Requested and blacklisted are reported without consents.
	if err := m.ApplyConsentRequest(ctx, wire.TaskProfileConsentRequestPayload{RequestID: "r", SIDs: wire.TaskSIDs{SIDs: []string{sidA}}}); err != nil {
		t.Fatal(err)
	}
	if err := m.HandleSignal(ctx, SecuritySignal{SID: sidB, Signal: wire.ProfileSignalAccountRemoved, Evidence: wire.ProfileEvidenceSIDLookup, ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	recs, _ := m.Store.List(ctx)
	rep := BuildReport(recs)
	if len(rep.Profiles) != 2 || rep.Profiles[0].SID != sidA || rep.Profiles[0].State != wire.ProfileStateRequested ||
		rep.Profiles[1].State != wire.ProfileStateBlacklisted || len(rep.Consents) != 0 {
		t.Errorf("rep = %+v", rep)
	}
	empty := BuildReport(nil)
	if empty.Profiles == nil || empty.Consents == nil {
		t.Error("empty report must encode [] not null")
	}
}

func TestConsentSignalMapping(t *testing.T) {
	tests := []struct {
		err    error
		signal string
		ok     bool
	}{
		{nil, "", false},
		{ErrNeedSession, "", false},
		{errors.New("io"), "", false},
		{ErrDPAPIKeyUnavailable, wire.ProfileSignalPasswordReset, true},
		{ErrConsentInvalid, wire.ProfileSignalConsentUnverifiable, true},
	}
	for _, tt := range tests {
		s, ev, ok := ConsentSignal(tt.err)
		if s != tt.signal || ok != tt.ok || (ok && ev != wire.ProfileEvidenceDPAPIUnprotectFailed) {
			t.Errorf("%v: %q %q %v", tt.err, s, ev, ok)
		}
	}
}

func TestVerifyNeedsSession(t *testing.T) {
	tok := &fakeToken{sid: sidA, src: tokens.SourceS4U}
	if _, err := VerifyConsentProof(tok, []byte("x"), "dev-1", sidA); !errors.Is(err, ErrNeedSession) {
		t.Fatalf("err = %v", err)
	}
	if tok.called {
		t.Error("S4U token must not be used for DPAPI")
	}
	if _, _, ok := ConsentSignal(ErrNeedSession); ok {
		t.Error("ErrNeedSession produced a signal")
	}
	if _, err := VerifyConsentProof(&fakeToken{sid: sidA, src: tokens.SourceSession}, nil, "dev-1", sidA); !errors.Is(err, ErrConsentInvalid) {
		t.Errorf("empty blob = %v", err)
	}
	if _, err := VerifyConsentProof(&fakeToken{sid: sidB, src: tokens.SourceSession}, []byte("x"), "dev-1", sidA); !errors.Is(err, ErrConsentInvalid) {
		t.Errorf("token for other user = %v", err)
	}

	m, _ := newManager(t)
	m.Tokens = &fakeProvider{loggedOff: true}
	consent(t, m, sidA, wire.ProfileStateAuthorised)
	if err := m.VerifyStored(context.Background(), sidA); !errors.Is(err, ErrNeedSession) {
		t.Errorf("VerifyStored logged off = %v", err)
	}
	if _, err := m.RecordConsent(context.Background(), sidB, []byte("x")); !errors.Is(err, ErrNeedSession) {
		t.Errorf("RecordConsent logged off = %v", err)
	}
	if got := stateOf(t, m, sidA); got != wire.ProfileStateAuthorised {
		t.Errorf("logged-off verification changed state to %q", got)
	}
}

func TestCheckFresh(t *testing.T) {
	now := time.Now()
	for d, ok := range map[time.Duration]bool{0: true, -time.Hour: true, 5 * time.Minute: true, time.Hour: false, -48 * time.Hour: false} {
		err := CheckFresh(ConsentClaim{Time: now.Add(d).UnixMilli()}, now)
		if (err == nil) != ok {
			t.Errorf("%v: %v", d, err)
		}
	}
}

func TestRecordConsentRefusals(t *testing.T) {
	ctx := context.Background()
	m, _ := newManager(t)
	if _, err := m.RecordConsent(ctx, "S-1-5-18", []byte("x")); !errors.Is(err, ErrInvalidSID) {
		t.Errorf("service SID = %v", err)
	}
	if _, err := m.RecordConsent(ctx, sidA, make([]byte, MaxProofSize+1)); !errors.Is(err, ErrConsentInvalid) {
		t.Errorf("oversized = %v", err)
	}
	consent(t, m, sidA, wire.ProfileStateAuthorised)
	sig := SecuritySignal{SID: sidA, Signal: wire.ProfileSignalPasswordChanged, Evidence: wire.ProfileEvidenceLocalPasswordAge, ObservedAt: time.Now()}
	if err := m.HandleSignal(ctx, sig); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RecordConsent(ctx, sidA, []byte("x")); !errors.Is(err, ErrBlacklisted) {
		t.Errorf("blacklisted = %v", err)
	}
}
