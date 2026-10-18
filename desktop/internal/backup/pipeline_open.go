package backup

import (
	"io"
	"os"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/tokens"
)

// Opener opens a file the walker emitted for reading as the profile's
// user. The open must happen inside tok.Impersonate; the returned reader
// is read outside it (Windows checks access when the handle is opened).
type Opener interface {
	Open(tok tokens.Token, path string) (io.ReadCloser, error)
}

// OSOpener opens real files under the profile token.
type OSOpener struct{}

// Open implements Opener.
func (OSOpener) Open(tok tokens.Token, path string) (io.ReadCloser, error) {
	var f *os.File
	err := tok.Impersonate(func() error {
		var oerr error
		f, oerr = os.Open(path)
		return oerr
	})
	if err != nil {
		return nil, err
	}
	return f, nil
}

// sharedProvider hands the pipeline's one token to the walker (one token,
// so one S4U logon, per job run); the pipeline closes it, not the walker.
type sharedProvider struct {
	tok tokens.Token
	err error
}

type noCloseToken struct{ tokens.Token }

func (noCloseToken) Close() error { return nil }

func (p sharedProvider) ForProfile(string) (tokens.Token, error) {
	if p.err != nil {
		return nil, p.err
	}
	return noCloseToken{p.tok}, nil
}

func (p sharedProvider) SessionOnly(sid string) (tokens.Token, error) { return p.ForProfile(sid) }
