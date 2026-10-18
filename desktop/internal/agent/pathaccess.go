package agent

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/ipc/caller"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/profiles"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/tokens"
)

// CodeAccessDenied is the error code for a caller-supplied path the caller
// may not use: guard refused it, or the caller's own token cannot read it.
const CodeAccessDenied = "access_denied"

// ErrAccessDenied matches (errors.Is) every path-access refusal.
var ErrAccessDenied = &Error{Code: CodeAccessDenied, Message: "access denied"}

// ProfileLister resolves profile directories (profiles.OSSystem on Windows).
type ProfileLister interface {
	LocalProfiles() ([]profiles.LocalProfile, error)
}

var _ ProfileLister = profiles.OSSystem{}

// pathAccess performs path work for one API call on behalf of its caller.
// For in-process and SYSTEM callers tok is nil and everything runs with the
// agent's own identity (the in-process UI already runs as the user). For a
// pipe client tok is the caller's filtered token: paths are checked through
// guard and opened while impersonating the caller (MASTER_PLAN §7.6, C7).
type pathAccess struct {
	tok     tokens.Token
	profile guard.Profile
	guard   func(op guard.Op, path string, p guard.Profile) error
}

// callerAccess returns the pathAccess for ctx's caller. It fails closed: a
// pipe client whose token cannot be obtained gets an access_denied error,
// never SYSTEM's rights. The caller must Close the result.
func (h *Host) callerAccess(ctx context.Context) (*pathAccess, error) {
	c, ok := caller.From(ctx)
	if !ok || IsSystem(c) {
		return &pathAccess{}, nil
	}
	if !tokens.IsUserSID(c.SID) {
		// Authorize already refuses these; defend in depth.
		return nil, &Error{Code: CodeAccessDenied, Message: "access denied: caller has no user identity"}
	}
	tok, err := h.opts.Tokens.ForProfile(c.SID)
	if err != nil {
		h.log.Warn("caller token unavailable", "sid", c.SID, "err", err)
		return nil, &Error{Code: CodeAccessDenied, Message: "access denied: cannot act as the calling user"}
	}
	pa := &pathAccess{tok: tok, guard: h.opts.Guard, profile: guard.Profile{SID: c.SID}}
	if list, err := h.opts.Profiles.LocalProfiles(); err == nil {
		for _, p := range list {
			if strings.EqualFold(p.SID, c.SID) {
				pa.profile.Root = p.ProfilePath
				break
			}
		}
	} else {
		h.log.Debug("profile lookup failed", "err", err)
	}
	return pa, nil
}

// Close releases the caller token, if any. It is nil-safe.
func (pa *pathAccess) Close() {
	if pa != nil && pa.tok != nil {
		_ = pa.tok.Close()
	}
}

// do runs fn as the caller (or directly for trusted callers).
func (pa *pathAccess) do(fn func() error) error {
	if pa.tok == nil {
		return fn()
	}
	return pa.tok.Impersonate(fn)
}

// configPaths are the local paths a configuration reads.
func configPaths(c config.SyncConfig) []string {
	if c.Kind == "files" && len(c.Files) > 0 {
		return c.Files
	}
	return []string{c.LocalPath}
}

// check verifies that the caller may read every path of c: guard first,
// then an open (and, for folders, a one-entry listing) under the caller's
// token. Trusted callers are not restricted here; the engine validates
// paths as before.
func (pa *pathAccess) check(c config.SyncConfig) error {
	if pa.tok == nil {
		return nil
	}
	for _, p := range configPaths(c) {
		if strings.TrimSpace(p) == "" {
			return invalidf("missing folder")
		}
		if err := pa.guard(guard.OpRead, p, pa.profile); err != nil {
			var be *guard.BlockedError
			msg := "access denied: this location is protected"
			if errors.As(err, &be) && be.Reason != "" {
				msg = "access denied: " + be.Reason
			}
			return &Error{Code: CodeAccessDenied, Message: msg}
		}
		if err := pa.tok.Impersonate(func() error { return probeRead(p) }); err != nil {
			return &Error{Code: CodeAccessDenied, Message: "access denied: you cannot read " + p}
		}
	}
	return nil
}

// probeRead opens path for reading and, for a directory, lists one entry.
// It runs while impersonating, so it starts no goroutines.
func probeRead(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.IsDir() {
		if _, err := f.ReadDir(1); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}
	return nil
}

// checkCallerPaths is callerAccess + check for calls that only validate.
func (h *Host) checkCallerPaths(ctx context.Context, c config.SyncConfig) error {
	pa, err := h.callerAccess(ctx)
	if err != nil {
		return err
	}
	defer pa.Close()
	return pa.check(c)
}
