package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/filter"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/state"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/automation"
)

func toSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// absPath maps a relative path back to the local filesystem.
func absPath(c *config.SyncConfig, rel string) string {
	return filepath.Join(c.LocalPath, filepath.FromSlash(rel))
}

// relPath returns the slash-separated path of abs below the config root, or
// "" if it is outside.
func relPath(c *config.SyncConfig, abs string) string {
	r, err := filepath.Rel(c.LocalPath, abs)
	if err != nil || r == "." || strings.HasPrefix(r, "..") {
		return ""
	}
	return toSlash(r)
}

// Scan walks a configuration, queueing new and changed files. manual marks
// work requested by the user (Sync now), which also works for manual-mode
// configurations.
func (e *Engine) Scan(cfgID string, manual bool) (int, error) {
	cfg, flt := e.config(cfgID)
	if cfg == nil {
		return 0, fmt.Errorf("unknown configuration")
	}
	if !cfg.Enabled && !manual {
		return 0, nil
	}
	e.mu.Lock()
	if e.scanning[cfgID] {
		e.mu.Unlock()
		return 0, nil
	}
	e.scanning[cfgID] = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.scanning[cfgID] = false
		e.lastScan[cfgID] = time.Now()
		e.mu.Unlock()
		e.refreshStatus()
		e.kick()
	}()
	e.refreshStatus()

	index, err := e.st.Index(cfgID)
	if err != nil {
		return 0, err
	}
	// The walk runs as the configuration's owner and only collects; issues,
	// logging and queueing happen afterwards with the agent's identity.
	var w walkResult
	if err := e.asOwner(cfg, func() error { w = e.collect(cfg, flt, index); return nil }); err != nil {
		e.setLastErr(cfgID, ownerMessage(err))
		e.log.Warn("scan: owner identity unavailable", "config", cfg.Name, "err", err)
		return 0, err
	}
	if w.rootErr != nil {
		root := cfg.LocalPath
		e.setLastErr(cfgID, "The folder "+root+" is not available (was it moved, renamed, is the drive disconnected, or can its owner no longer open it?)")
		return 0, fmt.Errorf("folder %s is not available", root)
	}
	queued := 0
	// skipped records an unreadable, locked or inaccessible entry once per
	// path per scan as a visible issue (B12); the scan carries on.
	reported := map[string]bool{}
	skipped := func(rel string, err error) {
		if rel == "" {
			rel = "."
		}
		if reported[rel] {
			return
		}
		reported[rel] = true
		class := classifyIssue(err)
		if class == issueNotFound && cfg.Kind != "files" {
			return // vanished during the walk: not a skip
		}
		e.log.Warn("scan: skipped entry", "config", cfg.Name, "path", rel, "class", class, "err", err)
		e.recordIssue(issueSourceScan, cfgID, rel, class, err.Error())
	}
	for _, sk := range w.skips {
		skipped(sk.rel, sk.err)
	}
	for _, h := range w.hits {
		if h.probe != nil {
			skipped(h.rel, h.probe)
			if classifyIssue(h.probe) != issueLocked {
				continue // cannot be read at all; a locked file is still queued and deferred by the transfer
			}
		}
		prev, known := index[h.rel]
		ev := "file.created"
		if known && prev.Status != "deleted" {
			ev = "file.modified"
		}
		if e.consider(cfg, flt, h.rel, h.abs, h.fi, ev, manual, "", 0) {
			queued++
		}
	}
	e.setLastErr(cfgID, "")
	// Files that disappeared: record as deleted locally (server copies stay).
	for _, rel := range w.gone {
		e.fileDeleted(cfg, rel)
	}
	if w.walkErr != nil && e.ctx != nil && e.ctx.Err() == nil {
		e.log.Warn("scan incomplete", "config", cfg.Name, "err", w.walkErr)
	}
	e.log.Info("scan finished", "config", cfg.Name, "queued", queued, "files_seen", len(w.seen))
	return queued, w.walkErr
}

// scanHit is a new or changed file found by collect.
type scanHit struct {
	rel, abs string
	fi       os.FileInfo
	probe    error // probeRead result
}

type scanSkip struct {
	rel string
	err error
}

// walkResult is what collect gathered.
type walkResult struct {
	hits    []scanHit
	skips   []scanSkip
	seen    map[string]bool
	gone    []string // indexed files that no longer exist
	rootErr error    // the folder itself is unavailable
	walkErr error
}

// collect walks cfg and compares it with index. It runs impersonating the
// owner, so it only touches the file system: no logging, no state.db, no
// goroutines.
func (e *Engine) collect(cfg *config.SyncConfig, flt *filter.Filter, index map[string]state.FileEntry) walkResult {
	w := walkResult{seen: map[string]bool{}}
	skip := func(rel string, err error) { w.skips = append(w.skips, scanSkip{rel, err}) }
	visit := func(abs string, fi os.FileInfo) {
		rel := relPath(cfg, abs)
		if rel == "" {
			return
		}
		if ok, _ := flt.Match(rel, fi.Size(), fi.ModTime(), isHidden(fi)); !ok {
			return
		}
		w.seen[rel] = true
		prev, known := index[rel]
		if known && prev.Size == fi.Size() && prev.MTime == fi.ModTime().UnixNano() && prev.Status != "deleted" && prev.Status != "failed" {
			return
		}
		w.hits = append(w.hits, scanHit{rel: rel, abs: abs, fi: fi, probe: probeRead(abs)})
	}

	switch cfg.Kind {
	case "files":
		for _, p := range cfg.Files {
			fi, err := os.Stat(p)
			if err != nil {
				rel := relPath(cfg, p)
				if rel == "" {
					rel = toSlash(p)
				}
				skip(rel, err)
				continue
			}
			if fi.IsDir() {
				continue
			}
			visit(p, fi)
		}
	default:
		root := cfg.LocalPath
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			if err == nil {
				err = fmt.Errorf("%s is not a folder", root)
			}
			w.rootErr = err
			return w
		}
		w.walkErr = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if e.ctx != nil && e.ctx.Err() != nil {
				return e.ctx.Err()
			}
			if err != nil {
				skip(relPath(cfg, p), err)
				if d != nil && d.IsDir() && p != root {
					return fs.SkipDir // unreadable folder: skip, keep scanning
				}
				return nil
			}
			if d.IsDir() {
				if p == root {
					return nil
				}
				info, err := d.Info()
				if err != nil {
					skip(relPath(cfg, p), err)
					return fs.SkipDir
				}
				if !flt.WalkDir(relPath(cfg, p), isHidden(info)) {
					return fs.SkipDir
				}
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
				return nil // never follow links or special files out of the selection
			}
			info, err := d.Info()
			if err != nil {
				skip(relPath(cfg, p), err)
				return nil
			}
			visit(p, info)
			return nil
		})
	}
	for rel, entry := range index {
		if w.seen[rel] || entry.Status == "deleted" {
			continue
		}
		if _, err := os.Stat(absPath(cfg, rel)); os.IsNotExist(err) {
			w.gone = append(w.gone, rel)
		}
	}
	return w
}

// ownerMessage is the configuration status text for an owner error.
func ownerMessage(err error) string {
	if errors.Is(err, ErrNoOwner) {
		return "This folder has no owner, so the VaultDock service does not synchronise it. Open VaultDock as the user who owns the folder and save it again."
	}
	return "VaultDock cannot act as this folder's owner right now (" + err.Error() + "); it will try again."
}

func (e *Engine) setLastErr(cfgID, msg string) {
	e.mu.Lock()
	e.lastErr[cfgID] = msg
	e.mu.Unlock()
}

func (e *Engine) fileEvent(cfg *config.SyncConfig, rel, abs string, fi os.FileInfo, evType, oldRel string) *automation.Event {
	return &automation.Event{
		Type: evType, Config: cfg.Name, Root: cfg.LocalPath, Destination: cfg.Destination, Device: e.deviceName, Now: time.Now(),
		File: &automation.FileInfo{Path: abs, RelPath: rel, Size: fi.Size(), Created: createdTime(fi), Modified: fi.ModTime(), Hidden: isHidden(fi), ReadOnly: fi.Mode().Perm()&0o200 == 0, OldRelPath: oldRel},
	}
}

// consider evaluates automations for a new or changed file and queues or
// skips it. It returns true when the file was queued.
func (e *Engine) consider(cfg *config.SyncConfig, flt *filter.Filter, rel, abs string, fi os.FileInfo, evType string, manual bool, oldRel string, depth int) bool {
	key := cfg.ID + "|" + rel + "|" + strconv.FormatInt(fi.Size(), 10) + "|" + strconv.FormatInt(fi.ModTime().UnixNano(), 10) + "|" + evType
	e.mu.Lock()
	if t, ok := e.dedupe[key]; ok && time.Since(t) < 10*time.Minute && !manual {
		e.mu.Unlock()
		return false
	}
	e.dedupe[key] = time.Now()
	if len(e.dedupe) > 50000 {
		for k, t := range e.dedupe {
			if time.Since(t) > 10*time.Minute {
				delete(e.dedupe, k)
			}
		}
	}
	e.mu.Unlock()

	ev := e.fileEvent(cfg, rel, abs, fi, evType, oldRel)
	plan := automation.EvaluateAll(e.autosFor(cfg), ev)
	e.applySideEffects(cfg, plan, depth)
	if evType == automation.EventFileModified && !setsDestination(plan) {
		// A changed file replaces its previous upload unless an automation
		// explicitly chooses a new destination.
		if prev, err := e.st.File(cfg.ID, rel); err == nil && prev.RemotePath != "" {
			plan.RemoteDir, plan.RemoteName = path.Split(prev.RemotePath)
			plan.RemoteDir = strings.TrimRight(plan.RemoteDir, "/")
			if plan.RemoteDir == "" {
				plan.RemoteDir = "/"
			}
		}
	}
	if plan.Decision == "skip" {
		_ = e.st.PutFile(state.FileEntry{ConfigID: cfg.ID, RelPath: rel, Size: fi.Size(), MTime: fi.ModTime().UnixNano(), Status: "skipped", Reason: plan.SkipReason})
		e.st.RemoveQueued(cfg.ID, rel)
		return false
	}
	next := time.Now()
	if ready := fi.ModTime().Add(time.Duration(cfg.StableSeconds) * time.Second); ready.After(next) {
		next = ready
	}
	_ = e.st.PutFile(state.FileEntry{ConfigID: cfg.ID, RelPath: rel, Size: fi.Size(), MTime: fi.ModTime().UnixNano(), Status: "queued"})
	if err := e.st.Enqueue(state.Item{ConfigID: cfg.ID, RelPath: rel, Event: evType, Size: fi.Size(), MTime: fi.ModTime().UnixNano(), RemoteDir: plan.RemoteDir, RemoteName: plan.RemoteName, Tags: plan.Tags, NextAt: next.UnixMilli(), Manual: manual}); err != nil {
		e.log.Error("enqueue", "file", rel, "err", err)
		return false
	}
	e.mu.Lock()
	e.run(cfg.ID)
	e.mu.Unlock()
	e.kick()
	if e.OnQueued != nil {
		e.OnQueued()
	}
	return true
}

func setsDestination(p automation.Plan) bool {
	for _, o := range p.Outcomes {
		if o.Triggered && (o.DestFolder != "" || o.DestName != "") {
			return true
		}
	}
	return false
}

// fileDeleted records a local deletion. The server copy is never deleted;
// automations may react (for example with a notification).
func (e *Engine) fileDeleted(cfg *config.SyncConfig, rel string) {
	entry, err := e.st.File(cfg.ID, rel)
	if err != nil || entry.Status == "deleted" {
		return
	}
	_ = e.st.MarkDeleted(cfg.ID, rel)
	e.st.RemoveQueued(cfg.ID, rel)
	ev := &automation.Event{Type: automation.EventFileDeleted, Config: cfg.Name, Root: cfg.LocalPath, Destination: cfg.Destination, Device: e.deviceName, Now: time.Now(),
		File: &automation.FileInfo{Path: absPath(cfg, rel), RelPath: rel, Size: entry.Size, Modified: time.Unix(0, entry.MTime)}}
	plan := automation.EvaluateAll(e.autosFor(cfg), ev)
	e.applySideEffects(cfg, plan, 0)
}

// PreviewEntry describes what would happen to one file.
type PreviewEntry struct {
	RelPath  string   `json:"rel_path"`
	Size     int64    `json:"size"`
	Modified int64    `json:"modified"`
	Decision string   `json:"decision"` // upload, skip, filtered, synced
	Reason   string   `json:"reason,omitempty"`
	Remote   string   `json:"remote,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

// Preview evaluates a configuration (saved or draft) against the files on
// disk without queuing anything: a dry run of filters and automations. run
// performs the file-system walk (the agent passes one that impersonates the
// caller); nil runs it directly. Only the walk runs inside run.
func (e *Engine) Preview(c config.SyncConfig, limit int, run func(func() error) error) ([]PreviewEntry, int, error) {
	if c.Kind == "files" && len(c.Files) > 0 {
		c.LocalPath = commonDir(c.Files)
	}
	c.Normalize()
	if err := c.Validate(); err != nil {
		return nil, 0, err
	}
	if run == nil {
		run = func(fn func() error) error { return fn() }
	}
	flt := filter.New(&c)
	var found []scanHit
	total := 0
	add := func(abs string, fi os.FileInfo) {
		if relPath(&c, abs) == "" {
			return
		}
		total++
		if len(found) < limit {
			found = append(found, scanHit{rel: relPath(&c, abs), abs: abs, fi: fi})
		}
	}
	err := run(func() error {
		if c.Kind == "files" {
			for _, p := range c.Files {
				if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
					add(p, fi)
				}
			}
			return nil
		}
		if st, err := os.Stat(c.LocalPath); err != nil || !st.IsDir() {
			return fmt.Errorf("folder %s is not available", c.LocalPath)
		}
		deadline := time.Now().Add(20 * time.Second)
		return filepath.WalkDir(c.LocalPath, func(p string, d fs.DirEntry, err error) error {
			if time.Now().After(deadline) || total > 200000 {
				return fs.SkipAll
			}
			if err != nil {
				if d != nil && d.IsDir() && p != c.LocalPath {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if p == c.LocalPath {
					return nil
				}
				info, err := d.Info()
				if err != nil || !flt.WalkDir(relPath(&c, p), isHidden(info)) {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			if info, err := d.Info(); err == nil {
				add(p, info)
			}
			return nil
		})
	})
	if err != nil && len(found) == 0 && total == 0 {
		return nil, 0, err
	}

	autos := e.autosFor(&c)
	var index map[string]state.FileEntry
	if c.ID != "" {
		index, _ = e.st.Index(c.ID)
	}
	out := make([]PreviewEntry, 0, len(found))
	for _, h := range found {
		fi := h.fi
		pe := PreviewEntry{RelPath: h.rel, Size: fi.Size(), Modified: fi.ModTime().UnixMilli()}
		if ok, why := flt.Match(h.rel, fi.Size(), fi.ModTime(), isHidden(fi)); !ok {
			pe.Decision, pe.Reason = "filtered", why
			out = append(out, pe)
			continue
		}
		plan := automation.EvaluateAll(autos, e.fileEvent(&c, h.rel, h.abs, fi, automation.EventFileCreated, ""))
		if plan.Decision == "skip" {
			pe.Decision, pe.Reason = "skip", plan.SkipReason
		} else {
			pe.Decision = "upload"
			pe.Remote = strings.TrimRight(plan.RemoteDir, "/") + "/" + plan.RemoteName
			pe.Tags = plan.Tags
			if prev, ok := index[h.rel]; ok && prev.Status == "synced" && prev.Size == fi.Size() && prev.MTime == fi.ModTime().UnixNano() {
				pe.Decision = "synced"
			}
		}
		out = append(out, pe)
	}
	return out, total, err
}
