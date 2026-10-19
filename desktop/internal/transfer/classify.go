package transfer

import (
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/client"
)

// MaxAttempts is the number of times a queued transfer is tried before it is
// marked as failed: two failures are tolerated (a dropped connection, a
// proxy timeout), the third is reported with its reason.
const MaxAttempts = 3

// FailReason explains in plain words why a transfer failed after attempts
// tries.
func FailReason(err error, attempts int) string {
	var ae *client.APIError
	var ne net.Error
	why := err.Error()
	switch {
	case errors.As(err, &ae) && ae.Status == 524:
		why = "the server took longer than Cloudflare's 100-second limit to answer"
	case errors.As(err, &ae) && (ae.Status == 502 || ae.Status == 504 || (ae.Status >= 520 && ae.Status <= 527)):
		why = fmt.Sprintf("the proxy in front of the server gave up on the request (HTTP %d)", ae.Status)
	case errors.As(err, &ae) && ae.Status == 503:
		why = "the server was too busy"
	case errors.As(err, &ae) && ae.Message != "":
		why = ae.Message
	case errors.As(err, &ne) && ne.Timeout():
		why = "the connection to the server timed out"
	case IsNetErr(err):
		why = "the connection to the server dropped"
	}
	if attempts <= 1 {
		return why
	}
	return fmt.Sprintf("%s (failed %d times)", why, attempts)
}

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
// (HTTP 429/502/503/504, a proxy timeout such as Cloudflare's 524, or a
// timeout); the controller backs off on these.
func IsCongestion(err error) bool {
	var ae *client.APIError
	if errors.As(err, &ae) {
		return ae.Status == 429 || ae.Status == 503 || ae.Status == 502 || ae.Status == 504 || ae.Status == 524
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
