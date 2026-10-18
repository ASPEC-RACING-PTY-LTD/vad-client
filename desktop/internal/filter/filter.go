// Package filter decides which local files a sync configuration selects.
package filter

import (
	"path"
	"strings"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/automation"
)

// Filter is a compiled view of a SyncConfig's selection rules.
type Filter struct {
	c             *config.SyncConfig
	after         time.Time
	includeExt    map[string]bool
	excludeExt    map[string]bool
	builtinIgnore []string
}

// Built-in exclusions for files that are never useful to upload and are
// often locked or rewritten constantly.
var builtinIgnore = []string{"desktop.ini", "thumbs.db", ".ds_store", "~$*", "*.crdownload", "*.part", "*.partial", "*.tmp", "*.!ut", "*.download"}

func New(c *config.SyncConfig) *Filter {
	f := &Filter{c: c, after: c.ModifiedAfterTime(), includeExt: map[string]bool{}, excludeExt: map[string]bool{}, builtinIgnore: builtinIgnore}
	for _, e := range c.IncludeExtensions {
		f.includeExt[e] = true
	}
	for _, e := range c.ExcludeExtensions {
		f.excludeExt[e] = true
	}
	return f
}

func matchesAny(patterns []string, rel string) bool {
	name := path.Base(rel)
	for _, p := range patterns {
		target := rel
		if !strings.Contains(p, "/") {
			target = name
		}
		if automation.MatchGlob(p, target, false) {
			return true
		}
	}
	return false
}

func underAny(rel string, folders []string) bool {
	low := strings.ToLower(rel)
	for _, f := range folders {
		lf := strings.ToLower(f)
		if low == lf || strings.HasPrefix(low, lf+"/") {
			return true
		}
	}
	return false
}

// WalkDir reports whether the scanner should descend into a directory
// (relative path with forward slashes).
func (f *Filter) WalkDir(rel string, hidden bool) bool {
	if rel == "" {
		return true
	}
	if !f.c.Recursive {
		return false
	}
	if f.c.SkipHidden && hidden {
		return false
	}
	if underAny(rel, f.c.ExcludeSubfolders) {
		return false
	}
	if len(f.c.IncludeSubfolders) > 0 {
		// descend if rel is inside an included folder or is an ancestor of one
		if underAny(rel, f.c.IncludeSubfolders) {
			return true
		}
		low := strings.ToLower(rel) + "/"
		for _, inc := range f.c.IncludeSubfolders {
			if strings.HasPrefix(strings.ToLower(inc)+"/", low) {
				return true
			}
		}
		return false
	}
	// exclude patterns that end in /** prune whole directories
	for _, p := range f.c.ExcludePatterns {
		if strings.HasSuffix(p, "/**") && automation.MatchGlob(strings.TrimSuffix(p, "/**"), rel, false) {
			return false
		}
	}
	return true
}

// Match reports whether a file is selected; when not, reason explains why.
func (f *Filter) Match(rel string, size int64, mtime time.Time, hidden bool) (bool, string) {
	name := path.Base(rel)
	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	if matchesAny(f.builtinIgnore, name) {
		return false, "temporary or system file"
	}
	if f.c.SkipHidden && hidden {
		return false, "hidden file"
	}
	if dir != "" {
		if !f.c.Recursive && f.c.Kind == "folder" {
			return false, "in a subfolder (recursion is off)"
		}
		if underAny(dir, f.c.ExcludeSubfolders) {
			return false, "in an excluded subfolder"
		}
		if len(f.c.IncludeSubfolders) > 0 && !underAny(dir, f.c.IncludeSubfolders) {
			return false, "not in an included subfolder"
		}
	} else if len(f.c.IncludeSubfolders) > 0 && f.c.Kind == "folder" {
		return false, "not in an included subfolder"
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	if len(f.includeExt) > 0 && !f.includeExt[ext] {
		return false, "file type not included"
	}
	if f.excludeExt[ext] {
		return false, "file type excluded"
	}
	if f.c.MinSize > 0 && size < f.c.MinSize {
		return false, "smaller than the minimum size"
	}
	if f.c.MaxSize > 0 && size > f.c.MaxSize {
		return false, "larger than the maximum size"
	}
	if len(f.c.IncludePatterns) > 0 && !matchesAny(f.c.IncludePatterns, rel) {
		return false, "does not match the include patterns"
	}
	if matchesAny(f.c.ExcludePatterns, rel) {
		return false, "matches an exclude pattern"
	}
	if !f.after.IsZero() && mtime.Before(f.after) {
		return false, "older than the configured date"
	}
	return true, ""
}
