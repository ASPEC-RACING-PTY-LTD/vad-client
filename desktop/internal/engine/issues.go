package engine

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"syscall"
	"time"
)

// Issue sources and local classes written to the state issues table (B4, B12).
const (
	issueSourceScan       = "scan"
	issueSourceAutomation = "automation"

	issueLocked           = "locked"
	issuePermissionDenied = "permission_denied"
	issueNotFound         = "not_found"
	issueIOError          = "io_error"
	issueInvalid          = "invalid_automation"
)

// classifyIssue maps a filesystem error to an issue class.
func classifyIssue(err error) string {
	var errno syscall.Errno
	switch {
	case runtime.GOOS == "windows" && errors.As(err, &errno) && (errno == 32 || errno == 33):
		return issueLocked // ERROR_SHARING_VIOLATION / ERROR_LOCK_VIOLATION
	case errors.Is(err, fs.ErrPermission):
		return issuePermissionDenied
	case errors.Is(err, fs.ErrNotExist):
		return issueNotFound
	}
	return issueIOError
}

// recordIssue upserts an open issue. A repeat of the same source, config,
// path and class bumps count and last_seen instead of adding a row, and
// reopens a resolved one. Failures are logged only.
func (e *Engine) recordIssue(source, configID, rel, class, msg string) {
	if e.st == nil || e.st.DB == nil {
		return
	}
	now := time.Now().UnixMilli()
	_, err := e.st.DB.Exec(`INSERT INTO issues (source, config_id, rel_path, class, message, first_seen, last_seen)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (source, config_id, rel_path, class) DO UPDATE SET
	count = count + 1, message = excluded.message, last_seen = excluded.last_seen,
	state = CASE WHEN state = 'resolved' THEN 'open' ELSE state END`,
		source, configID, rel, class, msg, now, now)
	if err != nil && e.log != nil {
		e.log.Warn("could not record issue", "source", source, "class", class, "err", err)
	}
}

// probeRead checks that a file can be opened for reading. Replaced in tests.
var probeRead = func(abs string) error {
	f, err := os.Open(abs)
	if err != nil {
		return err
	}
	return f.Close()
}
