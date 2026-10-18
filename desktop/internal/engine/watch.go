package engine

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/rjeczalik/notify"
)

// watcher receives filesystem change notifications for one configuration
// (ReadDirectoryChangesW on Windows) and debounces them per path.
type watcher struct {
	ch      chan notify.EventInfo
	quit    chan struct{}
	once    sync.Once
	mu      sync.Mutex
	pending map[string]*time.Timer
}

func (w *watcher) stop() {
	w.once.Do(func() {
		notify.Stop(w.ch)
		close(w.quit)
		w.mu.Lock()
		for _, t := range w.pending {
			t.Stop()
		}
		w.mu.Unlock()
	})
}

const debounce = 1500 * time.Millisecond

func (e *Engine) startWatcher(cfgID string) {
	cfg, _ := e.config(cfgID)
	if cfg == nil {
		return
	}
	w := &watcher{ch: make(chan notify.EventInfo, 8192), quit: make(chan struct{}), pending: map[string]*time.Timer{}}
	var paths []string
	switch cfg.Kind {
	case "files":
		// Watch the parent folders of the selected files (non-recursive).
		seen := map[string]bool{}
		for _, f := range cfg.Files {
			d := filepath.Dir(f)
			if !seen[d] {
				seen[d] = true
				paths = append(paths, d)
			}
		}
	default:
		p := cfg.LocalPath
		if cfg.Recursive {
			p = filepath.Join(p, "...")
		}
		paths = append(paths, p)
	}
	ok := false
	for _, p := range paths {
		if err := notify.Watch(p, w.ch, notify.All); err != nil {
			e.log.Warn("cannot watch folder; relying on periodic rescans", "path", p, "err", err)
			continue
		}
		ok = true
	}
	if !ok {
		notify.Stop(w.ch)
		e.setLastErr(cfgID, "Live change detection is unavailable for this folder; it is rescanned every "+strconv.Itoa(cfg.ScanIntervalMinutes)+" minutes")
		return
	}
	e.mu.Lock()
	if old := e.watchers[cfgID]; old != nil {
		old.stop()
	}
	e.watchers[cfgID] = w
	e.mu.Unlock()
	go func() {
		for {
			select {
			case <-w.quit:
				return
			case ev := <-w.ch:
				p := ev.Path()
				w.mu.Lock()
				if t, ok := w.pending[p]; ok {
					t.Reset(debounce)
				} else {
					w.pending[p] = time.AfterFunc(debounce, func() {
						w.mu.Lock()
						delete(w.pending, p)
						w.mu.Unlock()
						select {
						case <-w.quit:
							return
						default:
						}
						e.onPathChanged(cfgID, p)
					})
				}
				if len(w.pending) > 20000 {
					// Event storm: drop per-path tracking and rescan instead.
					for k, t := range w.pending {
						t.Stop()
						delete(w.pending, k)
					}
					go func() { _, _ = e.Scan(cfgID, false) }()
				}
				w.mu.Unlock()
			}
		}
	}()
}

// onPathChanged handles a debounced change notification.
func (e *Engine) onPathChanged(cfgID, abs string) {
	cfg, flt := e.config(cfgID)
	if cfg == nil || !cfg.Enabled || e.ctx.Err() != nil {
		return
	}
	if cfg.Kind == "files" {
		match := false
		for _, f := range cfg.Files {
			if filepath.Clean(f) == filepath.Clean(abs) {
				match = true
			}
		}
		if !match {
			return
		}
	}
	rel := relPath(cfg, abs)
	if rel == "" {
		return
	}
	var fi os.FileInfo
	err := e.asOwner(cfg, func() error {
		var serr error
		fi, serr = os.Stat(abs)
		return serr
	})
	if os.IsNotExist(err) {
		e.pathRemoved(cfg.ID, rel)
		return
	}
	if err != nil {
		return // includes paths the owner cannot see: never act on SYSTEM's view
	}
	if fi.IsDir() {
		// A folder appeared or was renamed: rescan to pick up its contents.
		go func() { _, _ = e.Scan(cfgID, false) }()
		return
	}
	if !fi.Mode().IsRegular() {
		return
	}
	// Only consider files inside folders the filter would walk into.
	dir := filepath.Dir(rel)
	if dir != "." {
		parts := []string{}
		for d := filepath.ToSlash(dir); d != "." && d != ""; d = filepath.ToSlash(filepath.Dir(d)) {
			parts = append([]string{d}, parts...)
		}
		for _, d := range parts {
			if !flt.WalkDir(d, false) {
				return
			}
		}
	}
	if ok, _ := flt.Match(rel, fi.Size(), fi.ModTime(), isHidden(fi)); !ok {
		return
	}
	prev, err := e.st.File(cfg.ID, rel)
	known := err == nil && prev.Status != "deleted"
	if known && prev.Size == fi.Size() && prev.MTime == fi.ModTime().UnixNano() {
		return
	}
	// Rename detection: a file with identical size and timestamp vanished
	// moments ago.
	if !known {
		key := strconv.FormatInt(fi.Size(), 10) + "|" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
		e.mu.Lock()
		r, ok := e.removed[cfg.ID][key]
		if ok && !r.done && time.Since(r.at) < 5*time.Second {
			r.done = true
			e.removed[cfg.ID][key] = r
			e.mu.Unlock()
			_ = e.st.MoveFile(cfg.ID, r.rel, rel)
			e.st.RemoveQueued(cfg.ID, r.rel)
			e.consider(cfg, flt, rel, abs, fi, "file.renamed", false, r.rel, 0)
			return
		}
		e.mu.Unlock()
	}
	ev := "file.created"
	if known {
		ev = "file.modified"
	}
	e.consider(cfg, flt, rel, abs, fi, ev, false, "", 0)
}

// pathRemoved waits briefly for a matching create (rename) before
// recording a deletion.
func (e *Engine) pathRemoved(cfgID, rel string) {
	entry, err := e.st.File(cfgID, rel)
	if err != nil || entry.Status == "deleted" {
		return
	}
	key := strconv.FormatInt(entry.Size, 10) + "|" + strconv.FormatInt(entry.MTime, 10)
	e.mu.Lock()
	if e.removed[cfgID] == nil {
		e.removed[cfgID] = map[string]removal{}
	}
	e.removed[cfgID][key] = removal{rel: rel, at: time.Now()}
	e.mu.Unlock()
	time.AfterFunc(4*time.Second, func() {
		e.mu.Lock()
		r := e.removed[cfgID][key]
		delete(e.removed[cfgID], key)
		e.mu.Unlock()
		if r.done {
			return
		}
		if cfg, _ := e.config(cfgID); cfg != nil {
			err := e.asOwner(cfg, func() error { _, serr := os.Stat(absPath(cfg, rel)); return serr })
			if os.IsNotExist(err) {
				e.fileDeleted(cfg, rel)
			}
		}
	})
}
