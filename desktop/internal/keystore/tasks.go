package keystore

import (
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"math"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/vdcrypt"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

var (
	// ErrUnsupportedTask is returned by HandleTask for kinds other than
	// key_share and rewrap.
	ErrUnsupportedTask = errors.New("keystore: not a key task")
	// ErrTaskPayload is returned for an invalid key task payload.
	ErrTaskPayload = errors.New("keystore: invalid key task payload")
	// ErrTargetRevoked is returned when key_share names a revoked device.
	ErrTargetRevoked = errors.New("keystore: key_share target device is revoked")
)

// HandleTask runs a key_share or rewrap device task (api-v1 §5A, §14 rev
// 1.1b). The result is always a client.KeyTaskResult; on failure its Error
// is set and err is non-nil, so the caller can report it with
// client.FinishKeyShare / FinishRewrap either way.
func (ks *Keystore) HandleTask(ctx context.Context, t wire.DeviceTask) (any, error) {
	if t.Kind != wire.TaskKindKeyShare && t.Kind != wire.TaskKindRewrap {
		return client.KeyTaskResult{Error: ErrUnsupportedTask.Error()}, fmt.Errorf("%w: %q", ErrUnsupportedTask, t.Kind)
	}
	v, err := t.DecodePayload()
	if err != nil {
		err = fmt.Errorf("%w: %w", ErrTaskPayload, err)
		return client.KeyTaskResult{Error: err.Error()}, err
	}
	var res client.KeyTaskResult
	switch p := v.(type) {
	case *wire.TaskKeySharePayload:
		res, err = ks.keyShare(ctx, p)
	case *wire.TaskRewrapPayload:
		res, err = ks.rewrap(ctx, p)
	}
	if err != nil {
		res.Error = err.Error()
		ks.opts.Logger.Warn("keystore: key task failed", "task", t.ID, "kind", t.Kind, "repo", res.RepoID, "err", err)
		return res, err
	}
	ks.opts.Logger.Info("keystore: key task done", "task", t.ID, "kind", t.Kind, "repo", res.RepoID, "epoch", res.Epoch)
	return res, nil
}

func (ks *Keystore) backend(ctx context.Context, repoID int64) (repo.Backend, error) {
	if ks.opts.Backend == nil {
		return nil, ErrNoBackend
	}
	return ks.opts.Backend(ctx, repoID)
}

func (ks *Keystore) revoked(ctx context.Context) (map[int64]bool, error) {
	if ks.opts.Revoked == nil {
		return nil, nil
	}
	return ks.opts.Revoked(ctx)
}

func (ks *Keystore) keyShare(ctx context.Context, p *wire.TaskKeySharePayload) (client.KeyTaskResult, error) {
	res := client.KeyTaskResult{RepoID: p.RepoID}
	if p.RepoID <= 0 {
		return res, fmt.Errorf("%w: repo_id", ErrTaskPayload)
	}
	full := p.KeyFileOID != ""
	target := p.TargetDeviceID != 0 || p.TargetBoxPub != "" || p.TargetSignPub != ""
	if full == target {
		return res, fmt.Errorf("%w: exactly one of key_file_oid or the target fields is required", ErrTaskPayload)
	}
	be, err := ks.backend(ctx, p.RepoID)
	if err != nil {
		return res, err
	}
	if full {
		return ks.confirmShare(ctx, be, res, p.KeyFileOID)
	}
	return ks.shareToTarget(ctx, be, res, p)
}

// confirmShare (full escrow) checks that the server wrote a device key file
// for this device's box key and that it opens.
func (ks *Keystore) confirmShare(ctx context.Context, be repo.Backend, res client.KeyTaskResult, oid string) (client.KeyTaskResult, error) {
	if !repo.IsOID(oid) {
		return res, fmt.Errorf("%w: key_file_oid", ErrTaskPayload)
	}
	data, err := be.Get(ctx, repo.KindKeys, oid)
	if err != nil {
		return res, err
	}
	kf, err := vdcrypt.DecodeKeyFile(data)
	if err != nil {
		return res, err
	}
	pub := ks.BoxPub()
	if kf.Kind != vdcrypt.KindDevice || subtle.ConstantTimeCompare(kf.RecipientPub, pub[:]) != 1 {
		return res, fmt.Errorf("%w: key file %s is not wrapped to this device", vdcrypt.ErrWrongRecipient, oid)
	}
	priv, err := ks.boxPrivate()
	if err != nil {
		return res, err
	}
	defer priv.Wipe()
	b, err := kf.Open(priv)
	if err != nil {
		return res, err
	}
	b.Wipe()
	res.Epoch, res.KeyFileOID = int64(kf.Epoch), oid
	return res, nil
}

// shareToTarget (metadata escrow) wraps the current epoch to the target
// device. It is idempotent: an existing wrap for the target is reported.
func (ks *Keystore) shareToTarget(ctx context.Context, be repo.Backend, res client.KeyTaskResult, p *wire.TaskKeySharePayload) (client.KeyTaskResult, error) {
	if p.TargetDeviceID <= 0 {
		return res, fmt.Errorf("%w: target_device_id", ErrTaskPayload)
	}
	boxRaw, err := base64.StdEncoding.DecodeString(p.TargetBoxPub)
	if err != nil {
		return res, fmt.Errorf("%w: target_box_pub", ErrTaskPayload)
	}
	boxPub, err := vdcrypt.KeyFromBytes(boxRaw)
	if err != nil || boxPub.IsZero() {
		return res, fmt.Errorf("%w: target_box_pub", ErrTaskPayload)
	}
	signPub, err := base64.StdEncoding.DecodeString(p.TargetSignPub)
	if err != nil || len(signPub) != ed25519.PublicKeySize {
		return res, fmt.Errorf("%w: target_sign_pub", ErrTaskPayload)
	}
	rev, err := ks.revoked(ctx)
	if err != nil {
		return res, err
	}
	if rev[p.TargetDeviceID] {
		return res, ErrTargetRevoked
	}
	r, err := ks.OpenRepo(ctx, be)
	if err != nil {
		return res, err
	}
	defer r.Close()
	if r.Stale() {
		return res, repo.ErrStaleEpoch
	}
	e := r.Epoch()
	res.Epoch = int64(e)
	before := r.KeyFiles()
	for oid, kf := range before {
		if kf.Kind == vdcrypt.KindDevice && kf.Epoch == e && kf.Device != nil &&
			kf.Device.ID == p.TargetDeviceID && subtle.ConstantTimeCompare(kf.RecipientPub, boxPub[:]) == 1 {
			res.KeyFileOID = oid
			return res, nil
		}
	}
	rc := repo.DeviceRecipient(boxPub, p.TargetDeviceID, signPub, fmt.Sprintf("device %d", p.TargetDeviceID))
	if err := r.AddRecipient(ctx, rc); err != nil {
		return res, err
	}
	for oid := range r.KeyFiles() {
		if _, ok := before[oid]; !ok {
			res.KeyFileOID = oid
			break
		}
	}
	return res, nil
}

// rewrap rotates to p.Epoch when the repository is at p.Epoch-1 and reports
// done when it is already at or past it. Key files of devices this device
// considers revoked are excluded from the new epoch.
func (ks *Keystore) rewrap(ctx context.Context, p *wire.TaskRewrapPayload) (client.KeyTaskResult, error) {
	res := client.KeyTaskResult{RepoID: p.RepoID}
	if p.RepoID <= 0 || p.Epoch < 2 || p.Epoch > math.MaxUint32 {
		return res, fmt.Errorf("%w: repo_id or epoch", ErrTaskPayload)
	}
	want := uint32(p.Epoch)
	be, err := ks.backend(ctx, p.RepoID)
	if err != nil {
		return res, err
	}
	rev, err := ks.revoked(ctx)
	if err != nil {
		return res, err
	}
	self := ks.BoxPub()
	exclude := func(kf *vdcrypt.KeyFile) bool {
		return kf.Kind == vdcrypt.KindDevice && kf.Device != nil && rev[kf.Device.ID] &&
			subtle.ConstantTimeCompare(kf.RecipientPub, self[:]) != 1
	}
	// A concurrent rotation by another device shows up as ErrStaleEpoch;
	// reopen once to see whether it reached the wanted epoch.
	for attempt := 0; ; attempt++ {
		r, err := ks.OpenRepo(ctx, be)
		if err != nil {
			return res, err
		}
		cur := r.Epoch()
		res.Epoch = int64(cur)
		switch {
		case cur >= want:
			r.Close()
			return res, nil
		case r.Stale():
			r.Close()
			return res, repo.ErrStaleEpoch
		case cur != want-1:
			r.Close()
			return res, fmt.Errorf("keystore: repository is at epoch %d; cannot rewrap to epoch %d", cur, want)
		}
		next, err := r.Rotate(ctx, repo.RotateOptions{Exclude: exclude})
		r.Close()
		if errors.Is(err, repo.ErrStaleEpoch) && attempt == 0 {
			continue
		}
		if err != nil {
			return res, err
		}
		res.Epoch = int64(next)
		return res, nil
	}
}
