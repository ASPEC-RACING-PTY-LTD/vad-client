package modules

import (
	"context"
	"errors"

	sdkmod "github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/modules"
)

// ErrDownloadUnavailable means the component pipeline cannot install yet;
// the component stays wanted.
var ErrDownloadUnavailable = errors.New("modules: component download not available")

// Downloader fetches, verifies (sdk/modules Verifier: SHA-256, Ed25519
// manifest signature, Authenticode) and installs a component into
// %ProgramFiles%\VaultDock\components\<id>\<version>, keeping the previous
// version for rollback. It returns the installed version. Phase 3 (P3)
// implements it; an already-installed, verified version should return
// quickly so a switch back to Advanced is cheap.
type Downloader interface {
	Install(ctx context.Context, id sdkmod.ID) (version string, err error)
}

// NoopDownloader is the Phase 0 stub: every component stays wanted.
type NoopDownloader struct{}

// Install always returns ErrDownloadUnavailable.
func (NoopDownloader) Install(context.Context, sdkmod.ID) (string, error) {
	return "", ErrDownloadUnavailable
}
