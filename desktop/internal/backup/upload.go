package backup

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/pack"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/vdcrypt"
)

// DefaultIndexEvery is how many uploaded packs are batched into one index
// object (backup-format §11: packs, then the index that lists them).
const DefaultIndexEvery = 16

// spooled is one pack waiting in the spool. seq is the data-pack sequence
// of this run (0 for meta packs and packs resumed from an earlier run);
// entries is nil for resumed packs, which are parsed from the file.
type spooled struct {
	oid     string
	class   pack.Class
	size    int64
	seq     int64
	entries []pack.Entry
}

// uploader moves spooled packs to the repository: PUT data/<oid> through
// the repo backend (idempotent; the backend retries 429/503), then, every
// `every` packs or when the spool is full, one index object for them.
// Only then is each pack marked uploaded in backup.db and its spool file
// removed, so a pack is never forgotten before an index lists it.
type uploader struct {
	r      *repo.Repo
	st     *Store
	sp     *spool
	every  int
	log    *slog.Logger
	cancel context.CancelCauseFunc
	// onIndexed runs after each successful index flush with the highest
	// data-pack seq now indexed (data packs are uploaded in seq order).
	onIndexed func(ctx context.Context, seq int64)
	// onProgress runs after each pack upload.
	onProgress func()

	in   chan spooled
	done chan struct{}

	mu       sync.Mutex
	err      error
	uploaded []string

	bytesUploaded atomic.Int64
	queued        atomic.Int64
}

func (u *uploader) start(ctx context.Context) {
	if u.every <= 0 {
		u.every = DefaultIndexEvery
	}
	u.in = make(chan spooled, 4096)
	u.done = make(chan struct{})
	go u.run(ctx)
}

// enqueue hands a spooled pack to the uploader.
func (u *uploader) enqueue(ctx context.Context, s spooled) error {
	u.queued.Add(1)
	select {
	case u.in <- s:
		return nil
	case <-ctx.Done():
		u.queued.Add(-1)
		return ctx.Err()
	}
}

// finish closes the queue and waits for the final index flush.
func (u *uploader) finish() error {
	close(u.in)
	<-u.done
	return u.Err()
}

// Err is the first upload or index error.
func (u *uploader) Err() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.err
}

func (u *uploader) uploadedPacks() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.uploaded...)
}

func (u *uploader) fail(err error) {
	u.mu.Lock()
	if u.err == nil {
		u.err = err
	}
	u.mu.Unlock()
	u.cancel(err)
}

func (u *uploader) run(ctx context.Context) {
	defer close(u.done)
	var batch []spooled
	flush := func() {
		if len(batch) == 0 || u.Err() != nil {
			return
		}
		if err := u.flush(ctx, batch); err != nil {
			u.fail(err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case s, ok := <-u.in:
			if !ok {
				flush()
				return
			}
			u.queued.Add(-1)
			if u.Err() != nil {
				continue // drain after a failure; the packs stay pending
			}
			if err := u.upload(ctx, s); err != nil {
				u.fail(err)
				continue
			}
			batch = append(batch, s)
			if len(batch) >= u.every || u.sp.pressure() {
				flush()
			}
		case <-u.sp.press:
			flush()
		}
	}
}

// upload PUTs one pack and adds it to the repo's pending index records.
func (u *uploader) upload(ctx context.Context, s spooled) error {
	data, err := u.sp.read(s.oid)
	if err != nil {
		return fmt.Errorf("backup: read spooled pack %s: %w", s.oid, err)
	}
	if vdcrypt.OID(data) != s.oid {
		return fmt.Errorf("backup: spooled pack %s is corrupt", s.oid)
	}
	p := &pack.Pack{OID: s.oid, Bytes: data, Class: s.class, Entries: s.entries}
	if p.Entries == nil {
		pr, err := pack.NewReader(bytes.NewReader(data), int64(len(data)), u.r.Keyring(), s.class)
		if err != nil {
			return fmt.Errorf("backup: spooled pack %s: %w", s.oid, err)
		}
		p.Entries, p.Epoch = pr.Entries, pr.Epoch
	}
	if err := u.r.SavePack(ctx, p); err != nil {
		return err
	}
	u.bytesUploaded.Add(int64(len(data)))
	if u.onProgress != nil {
		u.onProgress()
	}
	return nil
}

// flush writes the index for the uploaded batch, then retires the packs.
func (u *uploader) flush(ctx context.Context, batch []spooled) error {
	if _, err := u.r.FlushIndex(ctx); err != nil {
		return fmt.Errorf("backup: write index: %w", err)
	}
	// The packs are durable and indexed: record that even if ctx ends now.
	wctx := context.WithoutCancel(ctx)
	var seq int64
	for _, s := range batch {
		if err := u.st.MarkPackUploaded(wctx, s.oid); err != nil {
			return fmt.Errorf("backup: mark pack %s uploaded: %w", s.oid, err)
		}
		u.sp.remove(s.oid, s.size)
		seq = max(seq, s.seq)
		u.mu.Lock()
		u.uploaded = append(u.uploaded, s.oid)
		u.mu.Unlock()
	}
	u.log.Debug("backup: packs indexed", "packs", len(batch))
	if u.onIndexed != nil {
		u.onIndexed(wctx, seq)
	}
	return nil
}
