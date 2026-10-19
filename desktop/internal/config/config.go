// Package config defines sync configurations and application settings.
package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/automation"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/pathutil"
)

// SyncConfig describes one selection of local files to synchronise.
type SyncConfig struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// OwnerSID is the Windows user whose profile owns the configuration
	// (MASTER_PLAN §7.8). In the agent service every scan, stat and file
	// open runs with this user's token; a configuration without an owner
	// is not synchronised there. Set by the agent from the caller, never
	// taken from a pipe client.
	OwnerSID string `json:"owner_sid,omitempty"`
	// Kind is "folder" (a folder and optionally its subfolders) or "files"
	// (an explicit list of individual files).
	Kind      string   `json:"kind"`
	LocalPath string   `json:"local_path"`
	Files     []string `json:"files,omitempty"`
	// Destination is the folder in VaultDock storage, e.g. "/Medal".
	Destination string `json:"destination"`
	Recursive   bool   `json:"recursive"`
	// Compress sends uploads zstd-compressed when the server supports it
	// (upload.zstd): faster on slow links for compressible files, no gain
	// for photos, video or archives. Files are stored the same either way.
	Compress bool `json:"compress,omitempty"`
	// IncludeSubfolders restricts recursion to these relative folders
	// (empty means all); ExcludeSubfolders always wins.
	IncludeSubfolders []string `json:"include_subfolders,omitempty"`
	ExcludeSubfolders []string `json:"exclude_subfolders,omitempty"`
	IncludeExtensions []string `json:"include_extensions,omitempty"`
	ExcludeExtensions []string `json:"exclude_extensions,omitempty"`
	MinSize           int64    `json:"min_size,omitempty"`
	MaxSize           int64    `json:"max_size,omitempty"`
	// IncludePatterns/ExcludePatterns are globs over the relative path
	// (or the file name when the pattern contains no "/").
	IncludePatterns []string `json:"include_patterns,omitempty"`
	ExcludePatterns []string `json:"exclude_patterns,omitempty"`
	// ModifiedAfter skips files last modified before this date (RFC 3339).
	ModifiedAfter string `json:"modified_after,omitempty"`
	SkipHidden    bool   `json:"skip_hidden"`
	// Mode is "auto" (watch and upload continuously) or "manual" (only
	// when the user presses Sync now).
	Mode                string `json:"mode"`
	ScanIntervalMinutes int    `json:"scan_interval_minutes"`
	StableSeconds       int    `json:"stable_seconds"`
	// AutomationMode is "all" (every enabled automation), "selected" or "none".
	AutomationMode string  `json:"automation_mode"`
	Automations    []int64 `json:"automations,omitempty"`
	// OnConflict when the destination already has a different file:
	// "replace" (previous version goes to trash), "rename" or "skip".
	OnConflict string `json:"on_conflict"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

// Normalize fills defaults and cleans values.
func (c *SyncConfig) Normalize() {
	c.Name = strings.TrimSpace(c.Name)
	if c.Kind == "" {
		c.Kind = "folder"
	}
	if c.Mode == "" {
		c.Mode = "auto"
	}
	if c.ScanIntervalMinutes <= 0 {
		c.ScanIntervalMinutes = 15
	}
	if c.StableSeconds <= 0 {
		c.StableSeconds = 10
	}
	if c.AutomationMode == "" {
		c.AutomationMode = "all"
	}
	if c.OnConflict == "" {
		c.OnConflict = "replace"
	}
	c.LocalPath = filepath.Clean(strings.TrimSpace(c.LocalPath))
	c.Destination = pathutil.SanitizePath(c.Destination)
	norm := func(list []string, ext bool) []string {
		var out []string
		seen := map[string]bool{}
		for _, s := range list {
			s = strings.TrimSpace(s)
			if ext {
				s = strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(s, "*"), "."))
			} else {
				s = strings.Trim(strings.ReplaceAll(s, `\`, "/"), "/")
			}
			if s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
		return out
	}
	c.IncludeExtensions = norm(c.IncludeExtensions, true)
	c.ExcludeExtensions = norm(c.ExcludeExtensions, true)
	c.IncludeSubfolders = norm(c.IncludeSubfolders, false)
	c.ExcludeSubfolders = norm(c.ExcludeSubfolders, false)
	var files []string
	for _, f := range c.Files {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, filepath.Clean(f))
		}
	}
	c.Files = files
}

// Validate checks a configuration. It never touches the filesystem except
// to reject obviously dangerous roots.
func (c *SyncConfig) Validate() error {
	if c.Name == "" || len(c.Name) > 80 {
		return errors.New("give the configuration a name (up to 80 characters)")
	}
	switch c.Kind {
	case "folder":
		if !filepath.IsAbs(c.LocalPath) {
			return errors.New("choose a local folder")
		}
		if isDriveRoot(c.LocalPath) {
			return errors.New("VaultDock does not synchronise an entire drive. Choose a specific folder instead")
		}
	case "files":
		if len(c.Files) == 0 {
			return errors.New("choose at least one file")
		}
		if len(c.Files) > 5000 {
			return errors.New("at most 5000 individual files per configuration; use a folder configuration instead")
		}
		for _, f := range c.Files {
			if !filepath.IsAbs(f) {
				return fmt.Errorf("file path %q must be absolute", f)
			}
		}
	default:
		return errors.New("kind must be folder or files")
	}
	if _, err := pathutil.Clean(c.Destination); err != nil {
		return fmt.Errorf("destination folder: %v", err)
	}
	switch c.Mode {
	case "auto", "manual":
	default:
		return errors.New("mode must be auto or manual")
	}
	switch c.AutomationMode {
	case "all", "selected", "none":
	default:
		return errors.New("automation mode must be all, selected or none")
	}
	switch c.OnConflict {
	case "replace", "rename", "skip":
	default:
		return errors.New("conflict handling must be replace, rename or skip")
	}
	if c.MinSize < 0 || c.MaxSize < 0 || (c.MaxSize > 0 && c.MinSize > c.MaxSize) {
		return errors.New("size limits are invalid (minimum must not exceed maximum)")
	}
	for _, p := range append(append([]string{}, c.IncludePatterns...), c.ExcludePatterns...) {
		if _, err := automation.CompileGlob(p, false); err != nil {
			return fmt.Errorf("pattern %q: %v", p, err)
		}
	}
	if c.ModifiedAfter != "" {
		if _, err := automation.ParseDate(c.ModifiedAfter); err != nil {
			return errors.New("'only files after' must be a date (YYYY-MM-DD)")
		}
	}
	if c.ScanIntervalMinutes < 1 || c.ScanIntervalMinutes > 1440 {
		return errors.New("rescan interval must be between 1 and 1440 minutes")
	}
	if c.StableSeconds < 1 || c.StableSeconds > 3600 {
		return errors.New("wait-for-stable must be between 1 and 3600 seconds")
	}
	return nil
}

func isDriveRoot(p string) bool {
	vol := filepath.VolumeName(p)
	rest := strings.Trim(strings.TrimPrefix(p, vol), `\/`)
	return rest == ""
}

// ModifiedAfterTime parses ModifiedAfter.
func (c *SyncConfig) ModifiedAfterTime() time.Time {
	if c.ModifiedAfter == "" {
		return time.Time{}
	}
	t, _ := automation.ParseDate(c.ModifiedAfter)
	return t
}

// Settings are per-installation application settings (no secrets).
type Settings struct {
	ServerURL       string `json:"server_url"`
	PinnedCert      string `json:"pinned_cert,omitempty"`
	DeviceID        int64  `json:"device_id,omitempty"`
	DeviceName      string `json:"device_name,omitempty"`
	Username        string `json:"username,omitempty"`
	KeyFingerprint  string `json:"key_fingerprint,omitempty"`
	AutoStart       bool   `json:"auto_start"`
	StartMinimized  bool   `json:"start_minimized"`
	CloseToTray     bool   `json:"close_to_tray"`
	ConcurrencyMode string `json:"concurrency_mode"` // auto | manual
	ManualParallel  int    `json:"manual_parallel"`
	MaxParallel     int    `json:"max_parallel"`
	BandwidthKBps   int    `json:"bandwidth_kbps"`
	Notifications   bool   `json:"notifications"`
	Paused          bool   `json:"paused"`
	// AutoUpdate installs new releases automatically when sync is idle.
	AutoUpdate bool `json:"auto_update"`
	// DirectUpload sends sync and backup uploads to the server's local
	// address DirectURL instead of the public URL (off by default). It is
	// used only while the address is verified to reach the same server, so a
	// device away from that network uses the public URL.
	DirectUpload bool   `json:"direct_upload"`
	DirectURL    string `json:"direct_url,omitempty"`
	// DirectResetGen is the server's reset generation when direct uploads
	// were turned on (-1: take it from the next check). An administrator's
	// reset raises the server's value, which turns DirectUpload off.
	DirectResetGen int64 `json:"direct_reset_gen,omitempty"`
}

func DefaultSettings() Settings {
	return Settings{CloseToTray: true, ConcurrencyMode: "auto", ManualParallel: 4, MaxParallel: 32, Notifications: true, AutoStart: true, AutoUpdate: true}
}

// Normalize clamps values.
func (s *Settings) Normalize() {
	if s.ConcurrencyMode != "manual" {
		s.ConcurrencyMode = "auto"
	}
	s.ManualParallel = max(1, min(s.ManualParallel, 128))
	if s.MaxParallel <= 0 {
		s.MaxParallel = 32
	}
	s.MaxParallel = max(1, min(s.MaxParallel, 128))
	if s.BandwidthKBps < 0 {
		s.BandwidthKBps = 0
	}
}
