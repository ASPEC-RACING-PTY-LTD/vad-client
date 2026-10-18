//go:build !windows

package backup

import (
	"context"
	"errors"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/snapshot"
)

// OSFolders resolves known folders as the Windows defaults below the
// profile directory (non-Windows builds have no registry).
type OSFolders struct{}

// Tokens implements FolderResolver.
func (OSFolders) Tokens(_ context.Context, prof guard.Profile) (snapshot.Tokens, error) {
	if prof.Root == "" {
		return nil, errors.New("backup: profile directory unknown")
	}
	return profileTokens(prof.Root, nil, nil, nil), nil
}
