package main

import (
	"context"
	"os/user"
	"strconv"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/agent"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/profiles"
)

// --- first-run wizard (agent) ---
//
// Order: connect (ConnectScreen) → KeyStatus → ConfirmServerIdentity when
// the SIK pin is unpinned or mismatched → InitRepository when repo_state is
// "none" → show the recovery key once → ConfirmRecoveryKey → profile
// consent. The recovery key only passes through these calls; the UI
// process never stores or logs it.

// ConfirmServerIdentity pins (or re-pins) the server identity after the
// administrator compared the fingerprint.
func (a *App) ConfirmServerIdentity(fingerprint string) (agent.KeyStatus, error) {
	ks, err := a.api.ConfirmServerIdentity(a.bg, fingerprint)
	return ks, uiError(err)
}

// InitRepository creates the account's repository as the first device.
// escrowMode is "full" (default) or "metadata". The result's recovery_key
// is shown once by the wizard.
func (a *App) InitRepository(escrowMode string) (agent.InitRepositoryResult, error) {
	if escrowMode == "" {
		escrowMode = "full"
	}
	r, err := a.api.InitRepository(a.bg, agent.InitRepositoryRequest{EscrowMode: escrowMode})
	return r, uiError(err)
}

// ConfirmRecoveryKey checks the recovery key the user re-typed.
func (a *App) ConfirmRecoveryKey(text string) error {
	return uiError(a.api.ConfirmRecoveryKey(a.bg, text))
}

// consentGiver is the reserved agent method GiveConsent (api.go
// "reserved"): the agent verifies the user-scope DPAPI proof under the
// caller's session token, records it and reports the profile.
type consentGiver interface {
	GiveConsent(ctx context.Context, proof []byte) error
}

// ConsentStatus tells the wizard whether the consent step can run.
type ConsentStatus struct {
	Supported bool   `json:"supported"`
	User      string `json:"user"`
}

// GetConsentStatus reports whether the agent accepts profile consent from
// the UI yet, and the Windows account that would consent.
func (a *App) GetConsentStatus() ConsentStatus {
	_, ok := a.api.(consentGiver)
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	return ConsentStatus{Supported: ok, User: name}
}

// GiveProfileConsent creates the consent proof for the signed-in user with
// user-scope DPAPI, in this (the user's) process, and sends it to the agent
// (§7.8 "Allow VaultDock to protect my profile").
func (a *App) GiveProfileConsent() error {
	g, ok := a.api.(consentGiver)
	if !ok {
		return uiError(&agent.Error{Code: agent.CodeNotImplemented,
			Message: "This version of the VaultDock service cannot record consent from the app yet."})
	}
	s, err := a.api.GetSettings(a.bg)
	if err != nil {
		return uiError(err)
	}
	if s.DeviceID == 0 {
		return uiError(&agent.Error{Code: agent.CodeUnavailable, Message: "Connect this device to a server first."})
	}
	u, err := user.Current()
	if err != nil {
		return uiError(err)
	}
	proof, err := profiles.CreateConsentProof(strconv.FormatInt(s.DeviceID, 10), u.Uid)
	if err != nil {
		return uiError(err)
	}
	return uiError(g.GiveConsent(a.bg, proof))
}
