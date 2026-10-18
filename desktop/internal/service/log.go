package service

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// LogFileName is the agent's log file inside the log directory.
const LogFileName = "agent.log"

// rotatingFile is a minimal size-based log rotator (agent.log, agent.log.1, ...).
type rotatingFile struct {
	mu   sync.Mutex
	path string
	max  int64
	keep int
	f    *os.File
	size int64
}

func (r *rotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, os.ErrClosed
	}
	if r.size+int64(len(p)) > r.max {
		r.f.Close()
		for i := r.keep - 1; i >= 1; i-- {
			_ = os.Rename(fmt.Sprintf("%s.%d", r.path, i), fmt.Sprintf("%s.%d", r.path, i+1))
		}
		_ = os.Rename(r.path, r.path+".1")
		if err := r.open(); err != nil {
			r.f = nil
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// OpenLog opens dir/agent.log (rotated at 10 MiB, 3 old files kept) and
// returns a JSON slog logger writing to it, plus to each extra writer (for
// example os.Stderr in console mode). The directory is created if needed;
// its ACL comes from the installer (Users: read on logs, §7.6).
func OpenLog(dir string, level slog.Leveler, extra ...io.Writer) (*slog.Logger, io.Closer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	rf := &rotatingFile{path: filepath.Join(dir, LogFileName), max: 10 << 20, keep: 3}
	if err := rf.open(); err != nil {
		return nil, nil, err
	}
	var w io.Writer = rf
	if len(extra) > 0 {
		w = io.MultiWriter(append([]io.Writer{rf}, extra...)...)
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})), rf, nil
}
