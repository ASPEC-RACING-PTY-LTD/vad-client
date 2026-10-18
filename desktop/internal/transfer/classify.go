package transfer

import (
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/client"
)

// MaxAttempts is the number of times a queued transfer is tried before it is
// marked as failed.
const MaxAttempts = 12

// Backoff returns the wait before retry number attempts: exponential from
// 5s, capped at 30 minutes, plus up to 20% jitter.
func Backoff(attempts int) time.Duration {
	d := 5 * time.Second << min(attempts, 10)
	d = min(d, 30*time.Minute)
	jitter := time.Duration(rand.Int64N(int64(d / 5)))
	return d + jitter
}

// IsNetErr reports whether err is a transport-level (network) failure.
func IsNetErr(err error) bool {
	var ne net.Error
	var oe *net.OpError
	return errors.As(err, &ne) || errors.As(err, &oe) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}

// IsCongestion reports whether err indicates an overloaded link or server
// (HTTP 429/502/503/504 or a timeout); the controller backs off on these.
func IsCongestion(err error) bool {
	var ae *client.APIError
	if errors.As(err, &ae) {
		return ae.Status == 429 || ae.Status == 503 || ae.Status == 502 || ae.Status == 504
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// StageError tags an error with the upload stage that produced it:
// "start upload", "upload" or "complete".
type StageError struct {
	Stage string
	Err   error
}

func (e *StageError) Error() string { return e.Stage + ": " + e.Err.Error() }
func (e *StageError) Unwrap() error { return e.Err }
