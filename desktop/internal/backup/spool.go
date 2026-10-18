package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/repo"
)

// DefaultSpoolMax bounds the bytes of sealed packs waiting in the spool
// directory (written but not yet uploaded and indexed).
const DefaultSpoolMax = 256 << 20

const spoolExt = ".pack"

// spool is the local directory sealed packs wait in until they are uploaded
// and covered by an index object. Each pack is <oid>.pack, written to a
// temporary name and renamed, then recorded in backup.db (AddPack); the row
// stays pending until MarkPackUploaded, so a crash or cancellation leaves a
// resumable queue. Space is bounded: write blocks while the spool is full.
type spool struct {
	dir string
	max int64

	mu      sync.Mutex
	used    int64
	waiting bool
	freed   chan struct{} // closed and replaced on every release
	press   chan struct{} // signalled (cap 1) when a writer starts waiting
}

func newSpool(dir string, max int64) (*spool, error) {
	if max <= 0 {
		max = DefaultSpoolMax
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("backup: spool dir: %w", err)
	}
	return &spool{dir: dir, max: max, freed: make(chan struct{}), press: make(chan struct{}, 1)}, nil
}

func (s *spool) path(oid string) string { return filepath.Join(s.dir, oid+spoolExt) }

// account adds bytes already on disk (resumed packs) without blocking.
func (s *spool) account(n int64) {
	s.mu.Lock()
	s.used += n
	s.mu.Unlock()
}

// reserve waits until n bytes fit (a single pack larger than the bound is
// let through when the spool is empty) or ctx ends.
func (s *spool) reserve(ctx context.Context, n int64) error {
	for {
		s.mu.Lock()
		if s.used == 0 || s.used+n <= s.max {
			s.used += n
			s.waiting = false
			s.mu.Unlock()
			return nil
		}
		s.waiting = true
		freed := s.freed
		s.mu.Unlock()
		select {
		case s.press <- struct{}{}:
		default:
		}
		select {
		case <-freed:
		case <-ctx.Done():
			s.mu.Lock()
			s.waiting = false
			s.mu.Unlock()
			return ctx.Err()
		}
	}
}

func (s *spool) release(n int64) {
	s.mu.Lock()
	s.used -= n
	if s.used < 0 {
		s.used = 0
	}
	close(s.freed)
	s.freed = make(chan struct{})
	s.mu.Unlock()
}

// pressure reports a writer blocked on space: the uploader must flush the
// index (which frees spooled packs) instead of batching further.
func (s *spool) pressure() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waiting
}

// write stores data as <oid>.pack after reserving its space.
func (s *spool) write(ctx context.Context, oid string, data []byte) error {
	if !repo.IsOID(oid) {
		return fmt.Errorf("backup: spool: bad oid %q", oid)
	}
	if err := s.reserve(ctx, int64(len(data))); err != nil {
		return err
	}
	if err := writeFileSync(s.path(oid), data); err != nil {
		s.release(int64(len(data)))
		return fmt.Errorf("backup: spool %s: %w", oid, err)
	}
	return nil
}

func writeFileSync(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func (s *spool) read(oid string) ([]byte, error) { return os.ReadFile(s.path(oid)) }

// remove deletes a spooled pack and releases its space.
func (s *spool) remove(oid string, size int64) {
	// A file that cannot be removed is swept on the next run.
	_ = os.Remove(s.path(oid))
	s.release(size)
}

// sweep deletes temporary files and packs that backup.db does not list as
// pending (a crash between rename and AddPack, or after MarkPackUploaded or
// DeletePack).
func (s *spool) sweep(pending map[string]bool) error {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(name, ".tmp") || (strings.HasSuffix(name, spoolExt) && !pending[strings.TrimSuffix(name, spoolExt)]) {
			os.Remove(filepath.Join(s.dir, name))
		}
	}
	return nil
}
