package keystore

import (
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/vdcrypt"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// ErrRecoveryMismatch is returned by CheckRecoveryKey when the text is not
// the repository's recovery key.
var ErrRecoveryMismatch = errors.New("this recovery key does not belong to this repository")

// ErrNoRepoID is returned by InitRepo when InitParams.RepoID is zero. The
// repository id must be the server repository's repo_uuid (ParseRepoID):
// the server rejects key files of any other repository.
var ErrNoRepoID = errors.New("keystore: repository id (repo_uuid) is required")

const redacted = "[REDACTED]"

// RecoveryKey holds the VDRK1- recovery key text returned once by InitRepo.
// It is never persisted; String, GoString, LogValue and MarshalText redact
// it. Show Text() to the user once, then call Wipe.
type RecoveryKey struct{ text *string }

// Text returns the recovery key text for display.
func (k RecoveryKey) Text() string {
	if k.text == nil {
		return ""
	}
	return *k.text
}

// Wipe drops the reference to the text (Go strings cannot be cleared).
func (k RecoveryKey) Wipe() {
	if k.text != nil {
		*k.text = ""
	}
}

func (RecoveryKey) String() string               { return redacted }
func (RecoveryKey) GoString() string             { return redacted }
func (RecoveryKey) Format(f fmt.State, _ rune)   { _, _ = f.Write([]byte(redacted)) }
func (RecoveryKey) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (RecoveryKey) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// InitParams describe first-device repository init (backup-format §4.7).
type InitParams struct {
	Backend    repo.Backend
	RepoID     vdcrypt.RepoID // required: ParseRepoID(repo_uuid) of the server repository
	EscrowMode string         // repo.EscrowFull (default) or repo.EscrowMetadata
	DeviceID   int64
	SignPub    ed25519.PublicKey // this device's VD1 Ed25519 key
	Label      string
	// Server is the current server identity (GET /keys/server); it must
	// match the pin.
	Server *wire.ServerIdentity
}

// InitRepo creates a repository wrapped to this device, the pinned server's
// escrow key and the recovery recipient. The recovery key text is returned
// once and is never written anywhere.
func (ks *Keystore) InitRepo(ctx context.Context, p InitParams) (*repo.Repo, RecoveryKey, error) {
	if p.Backend == nil || p.DeviceID <= 0 || len(p.SignPub) != ed25519.PublicKeySize {
		return nil, RecoveryKey{}, fmt.Errorf("%w: backend, device id and sign_pub are required", repo.ErrRecipient)
	}
	if p.RepoID == (vdcrypt.RepoID{}) {
		return nil, RecoveryKey{}, ErrNoRepoID
	}
	ident, err := ks.Verify(p.Server)
	if err != nil {
		return nil, RecoveryKey{}, err
	}
	label := p.Label
	if label == "" {
		label = fmt.Sprintf("device %d", p.DeviceID)
	}
	recips := []repo.Recipient{
		repo.DeviceRecipient(ks.BoxPub(), p.DeviceID, p.SignPub, label),
		repo.EscrowRecipient(ident.SEKPub, ident.SIKPub, ident.SEKSig, ident.SEKVersion, "server"),
	}
	r, text, err := repo.Init(ctx, p.Backend, repo.InitOptions{
		Options: ks.repoOptions(), RepoID: p.RepoID, EscrowMode: p.EscrowMode, Recipients: recips,
	})
	if err != nil {
		return nil, RecoveryKey{}, err
	}
	// The escrow recipient was built from an identity Verify matched to the
	// pin (SEK signature checked), so no post-init VerifyEscrow is needed;
	// failing here would lose the only copy of the recovery key text.
	ks.opts.Logger.Info("keystore: repository initialised", "repo", r.ID().String(), "escrow_mode", r.Config().EscrowMode)
	return r, RecoveryKey{text: &text}, nil
}

// CheckRecoveryKey reports whether text is r's recovery key by comparing the
// derived recovery public key with config.recovery_pub (ConfirmRecoveryKey).
func CheckRecoveryKey(r *repo.Repo, text string) error {
	secret, err := vdcrypt.DecodeRecoveryKey(text)
	if err != nil {
		return err
	}
	defer secret.Wipe()
	priv, pub, err := vdcrypt.RecoveryKeyPair(r.ID(), secret)
	if err != nil {
		return err
	}
	priv.Wipe()
	if subtle.ConstantTimeCompare(pub[:], r.Config().RecoveryPub) != 1 {
		return ErrRecoveryMismatch
	}
	return nil
}

// OpenRepo opens be with the device box key and checks that every escrow
// key file of the opened epoch is signed by the pinned SIK.
func (ks *Keystore) OpenRepo(ctx context.Context, be repo.Backend) (*repo.Repo, error) {
	sik, err := ks.pinnedSIK()
	if err != nil {
		return nil, err
	}
	priv, err := ks.boxPrivate()
	if err != nil {
		return nil, err
	}
	cred := repo.DeviceCredential(priv)
	defer cred.Wipe()
	r, err := repo.Open(ctx, be, cred, ks.repoOptions())
	if err != nil {
		return nil, err
	}
	if err := verifyEscrow(r, sik); err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

// verifyEscrow checks the escrow key files of r's current epoch.
func verifyEscrow(r *repo.Repo, sik ed25519.PublicKey) error {
	e := r.Epoch()
	for oid, kf := range r.KeyFiles() {
		if kf.Kind != vdcrypt.KindEscrow || kf.Epoch != e {
			continue
		}
		if err := kf.VerifyEscrow(sik); err != nil {
			return fmt.Errorf("keystore: escrow key file %s: %w", oid, err)
		}
	}
	return nil
}

func (ks *Keystore) repoOptions() repo.Options {
	return repo.Options{Rand: ks.opts.Rand, Now: ks.opts.Now, Logger: ks.opts.Logger}
}
