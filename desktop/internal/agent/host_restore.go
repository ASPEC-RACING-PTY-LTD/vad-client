package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/backup"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/restore"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/tokens"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

// restoreJobs builds the runner of server-requested restores (api-v1 rev
// 1.1c, P2-D3: the task is acked at once, progress goes to the restore
// routes).
func (h *Host) restoreJobs(s *backupStack) *restore.Jobs {
	return &restore.Jobs{
		Server: s.srv,
		OpenRepo: func(ctx context.Context, id int64) (restore.Repo, error) {
			if err := h.verifyIdentity(ctx, s); err != nil {
				return nil, err
			}
			return h.bk.ks.OpenRepo(ctx, h.bk.backend(s.c, id, h))
		},
		Target: h.restoreTarget,
		Guard:  h.opts.Guard,
		Log:    h.log,
	}
}

// restoreTask is the TaskKindRestore handler.
func (h *Host) restoreTask(s *backupStack, jobs *restore.Jobs) backup.TaskHandler {
	return func(_ context.Context, t wire.DeviceTask) (any, error) {
		p, err := wire.DecodeTaskPayload(t.Kind, t.Payload)
		if err != nil {
			return nil, err
		}
		req, ok := p.(*wire.TaskRestorePayload)
		if !ok {
			return nil, fmt.Errorf("unexpected payload %T", p)
		}
		if err := jobs.Start(s.ctx, req.RestoreID); err != nil {
			return nil, err
		}
		return map[string]bool{"started": true}, nil
	}
}

// restoreTarget resolves a consented profile for writing. The agent never
// writes into a profile without consent (api-v1 §6B rule 2); in service
// mode every write runs under the owner's token.
func (h *Host) restoreTarget(ctx context.Context, sid string) (restore.Target, error) {
	info, err := h.profileLookup()(ctx, sid)
	if err != nil {
		return restore.Target{}, err
	}
	if !info.Allowed {
		return restore.Target{}, errors.New("the profile has not consented to VaultDock")
	}
	prof := guard.Profile{SID: info.SID, Root: info.ProfilePath}
	toks, err := backup.OSFolders{}.Tokens(ctx, prof)
	if err != nil {
		return restore.Target{}, err
	}
	t := restore.Target{Profile: prof, Tokens: toks}
	if h.serviceMode() {
		var tok tokens.Token
		if tok, err = h.opts.Tokens.ForProfile(info.SID); err != nil {
			return restore.Target{}, err
		}
		t.Token = tok
		t.Close = func() { _ = tok.Close() }
	}
	return t, nil
}
