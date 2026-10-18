package backup

import (
	"context"
	"sync"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/pack"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/repo"
)

// dedupIndex answers "is this blob already stored or queued?" for one run:
// the repository index (blobs in indexed packs, loaded by repo.Open) and a
// local seen-set of blobs added to this run's packs that are not indexed
// yet. The seen-set maps a data blob to the data-pack seq that holds it
// (0 for meta blobs), so a file knows which pack its chunks wait for.
type dedupIndex struct {
	r    *repo.Repo
	seen map[pack.BlobID]int64
}

func newDedupIndex(r *repo.Repo) *dedupIndex {
	return &dedupIndex{r: r, seen: map[pack.BlobID]int64{}}
}

// lookup returns (seq, true) when the blob needs no new copy; seq is the
// pack it waits for (0 = already indexed).
func (d *dedupIndex) lookup(id pack.BlobID) (int64, bool) {
	if s, ok := d.seen[id]; ok {
		return s, true
	}
	if d.r.HasBlob(id) {
		return 0, true
	}
	return 0, false
}

func (d *dedupIndex) add(id pack.BlobID, seq int64) { d.seen[id] = seq }

// hasAll reports whether every cached chunk id is still stored or queued
// (a cache entry pointing at pruned data must be re-read). It returns the
// highest pack seq the ids wait for.
func (d *dedupIndex) hasAll(ids [][32]byte) (int64, bool) {
	var need int64
	for _, id := range ids {
		s, ok := d.lookup(pack.BlobID(id))
		if !ok {
			return 0, false
		}
		need = max(need, s)
	}
	return need, true
}

// cacheQueue holds file cache rows until the packs holding their chunks
// are uploaded and indexed: a row is written only once every data pack up
// to its seq is indexed, so the cache never points at unstored data.
type cacheQueue struct {
	st *Store

	mu      sync.Mutex
	indexed int64
	items   []queuedCache
	err     error
}

type queuedCache struct {
	need int64
	c    CachedFile
}

// add queues c; it is written at once when need is already indexed.
func (q *cacheQueue) add(ctx context.Context, need int64, c CachedFile) error {
	q.mu.Lock()
	if need > q.indexed {
		q.items = append(q.items, queuedCache{need: need, c: c})
		q.mu.Unlock()
		return nil
	}
	q.mu.Unlock()
	return q.st.PutFileCache(ctx, c)
}

// advance records that data packs up to seq are indexed and writes the
// rows that became safe.
func (q *cacheQueue) advance(ctx context.Context, seq int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.indexed = max(q.indexed, seq)
	keep := q.items[:0]
	for _, it := range q.items {
		if it.need > q.indexed {
			keep = append(keep, it)
			continue
		}
		if err := q.st.PutFileCache(ctx, it.c); err != nil && q.err == nil {
			q.err = err
		}
	}
	clear(q.items[len(keep):])
	q.items = keep
}

// Err is the first file cache write error.
func (q *cacheQueue) Err() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.err
}

// pending is the number of rows still waiting for their packs.
func (q *cacheQueue) pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}
