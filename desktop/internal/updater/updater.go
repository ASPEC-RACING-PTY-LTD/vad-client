// Package updater keeps the desktop app current. It checks the official
// GitHub releases, downloads the Windows installer over HTTPS from github.com
// only, verifies it against the SHA-256 published with the release, and
// runs it silently so the app restarts on the new version.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/release"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/version"
)

// InstallerPattern matches the Windows installer attached to each release.
var InstallerPattern = regexp.MustCompile(`^VaultDock-[0-9][0-9A-Za-z.+-]*-amd64-setup\.exe$`)

const maxInstallerBytes = 512 << 20

// States reported to the UI.
const (
	StateIdle        = "idle"
	StateDisabled    = "disabled"
	StateChecking    = "checking"
	StateUpToDate    = "up_to_date"
	StateAvailable   = "available"
	StateDownloading = "downloading"
	StateReady       = "ready"
	StateInstalling  = "installing"
	StateError       = "error"
)

// Status is shown in Settings and drives notifications.
type Status struct {
	State     string  `json:"state"`
	Current   string  `json:"current"`
	Latest    string  `json:"latest,omitempty"`
	URL       string  `json:"url,omitempty"`
	Notes     string  `json:"notes,omitempty"`
	Progress  float64 `json:"progress"`
	Error     string  `json:"error,omitempty"`
	CheckedAt int64   `json:"checked_at,omitempty"` // Unix milliseconds
}

// Updater is safe for concurrent use. Check, Download and Install never run
// at the same time.
type Updater struct {
	Repo     string
	Current  string
	Dir      string // where installers are downloaded
	Log      *slog.Logger
	OnChange func(Status)

	API *release.Client
	DL  *http.Client

	op sync.Mutex // serialises check, download and install

	mu       sync.Mutex
	status   Status
	rel      *release.Release
	asset    *release.Asset
	sha      string
	readyAt  string // verified installer path
	launcher func(path string, args string) error
}

// New returns an updater for repo. An empty repo disables updates.
func New(repo, current, dir string, log *slog.Logger) *Updater {
	u := &Updater{
		Repo: repo, Current: current, Dir: dir, Log: log,
		API: &release.Client{UserAgent: "VaultDock-Desktop/" + current},
		DL: &http.Client{
			CheckRedirect: release.TrustedRedirect,
			Transport:     &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 30 * time.Second, TLSHandshakeTimeout: 15 * time.Second},
		},
		launcher: launch,
	}
	u.status = Status{State: StateIdle, Current: current}
	if repo == "" {
		u.status.State = StateDisabled
	}
	return u
}

// Status returns the current state.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.status
}

func (u *Updater) set(f func(s *Status)) {
	u.mu.Lock()
	f(&u.status)
	s := u.status
	u.mu.Unlock()
	if u.OnChange != nil {
		u.OnChange(s)
	}
}

// Check looks for a newer release. It returns true when one is available.
func (u *Updater) Check(ctx context.Context) (bool, error) {
	if u.Repo == "" {
		return false, nil
	}
	u.op.Lock()
	defer u.op.Unlock()
	if s := u.Status().State; s == StateReady || s == StateInstalling {
		return true, nil
	}
	u.set(func(s *Status) { s.State, s.Error = StateChecking, "" })
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rel, err := u.API.Latest(ctx, u.Repo)
	now := time.Now().UnixMilli()
	if errors.Is(err, release.ErrNoRelease) {
		u.set(func(s *Status) { s.State, s.CheckedAt, s.Latest = StateUpToDate, now, "" })
		return false, nil
	}
	if err != nil {
		u.set(func(s *Status) { s.State, s.Error, s.CheckedAt = StateError, err.Error(), now })
		return false, err
	}
	newer := version.Newer(rel.Version(), u.Current)
	asset := rel.Find(InstallerPattern)
	u.mu.Lock()
	u.rel, u.asset = rel, asset
	u.mu.Unlock()
	u.set(func(s *Status) {
		s.CheckedAt, s.Latest, s.URL, s.Notes = now, rel.Version(), rel.HTMLURL, rel.Body
		s.State = StateUpToDate
		if newer {
			s.State = StateAvailable
			if asset == nil {
				s.State, s.Error = StateError, "This release has no Windows installer yet. Try again later."
			}
		}
	})
	return newer && asset != nil, nil
}

// Download fetches and verifies the installer for the release found by
// Check. A verified download is reused.
func (u *Updater) Download(ctx context.Context) error {
	u.op.Lock()
	defer u.op.Unlock()
	u.mu.Lock()
	rel, asset, ready := u.rel, u.asset, u.readyAt
	u.mu.Unlock()
	if ready != "" {
		return nil
	}
	if rel == nil || asset == nil || !version.Newer(rel.Version(), u.Current) {
		return errors.New("no update to download")
	}
	path, err := u.download(ctx, rel, asset)
	if err != nil {
		u.set(func(s *Status) { s.State, s.Error, s.Progress = StateError, err.Error(), 0 })
		return err
	}
	u.mu.Lock()
	u.readyAt = path
	u.mu.Unlock()
	u.set(func(s *Status) { s.State, s.Progress, s.Error = StateReady, 1, "" })
	u.Log.Info("update downloaded and verified", "version", rel.Version(), "path", path)
	return nil
}

func (u *Updater) download(ctx context.Context, rel *release.Release, asset *release.Asset) (string, error) {
	if err := release.TrustedDownloadURL(asset.DownloadURL); err != nil {
		return "", err
	}
	if asset.Size <= 0 || asset.Size > maxInstallerBytes {
		return "", fmt.Errorf("unexpected installer size %d", asset.Size)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	sum, err := u.API.SHA256(ctx, rel, asset)
	if err != nil {
		return "", err
	}
	u.mu.Lock()
	u.sha = sum
	u.mu.Unlock()
	if err := os.MkdirAll(u.Dir, 0o700); err != nil {
		return "", err
	}
	u.cleanup(asset.Name)
	final := filepath.Join(u.Dir, asset.Name)
	if ok, _ := fileHasSHA256(final, sum); ok {
		return final, nil
	}

	u.set(func(s *Status) { s.State, s.Progress, s.Error = StateDownloading, 0, "" })
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.DownloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", u.API.UserAgent)
	resp, err := u.DL.Do(req)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed (HTTP %d)", resp.StatusCode)
	}
	part := final + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	pw := &progressWriter{total: asset.Size, report: func(p float64) {
		u.set(func(s *Status) { s.Progress = p })
	}}
	n, err := io.Copy(io.MultiWriter(f, h, pw), io.LimitReader(resp.Body, asset.Size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(part)
		return "", fmt.Errorf("download failed: %w", err)
	}
	if n != asset.Size {
		os.Remove(part)
		return "", fmt.Errorf("download incomplete: got %d of %d bytes", n, asset.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		os.Remove(part)
		return "", fmt.Errorf("checksum mismatch: the downloaded installer was rejected")
	}
	if err := os.Rename(part, final); err != nil {
		os.Remove(part)
		return "", err
	}
	return final, nil
}

// cleanup removes installers from earlier updates.
func (u *Updater) cleanup(keep string) {
	entries, _ := os.ReadDir(u.Dir)
	for _, e := range entries {
		if !e.IsDir() && e.Name() != keep && (strings.HasSuffix(e.Name(), ".exe") || strings.HasSuffix(e.Name(), ".part")) {
			os.Remove(filepath.Join(u.Dir, e.Name()))
		}
	}
}

// Ready reports whether a verified installer is waiting to be installed.
func (u *Updater) Ready() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.readyAt != ""
}

// Install verifies the downloaded installer once more and starts it silently,
// installing into installDir and relaunching the app afterwards. The caller
// must exit promptly so the installer can replace the files.
func (u *Updater) Install(installDir string) error {
	u.op.Lock()
	defer u.op.Unlock()
	u.mu.Lock()
	path, sum := u.readyAt, u.sha
	u.mu.Unlock()
	if path == "" {
		return errors.New("no verified update is ready")
	}
	if ok, err := fileHasSHA256(path, sum); !ok {
		u.mu.Lock()
		u.readyAt = ""
		u.mu.Unlock()
		os.Remove(path)
		if err == nil {
			err = errors.New("the downloaded installer changed on disk and was discarded")
		}
		u.set(func(s *Status) { s.State, s.Error = StateError, err.Error() })
		return err
	}
	if installDir == "" || strings.ContainsAny(installDir, "\"\r\n") {
		return fmt.Errorf("invalid install directory %q", installDir)
	}
	// NSIS: /S silent, /D= must be last and unquoted.
	args := "/S /RELAUNCH /D=" + installDir
	u.set(func(s *Status) { s.State, s.Error = StateInstalling, "" })
	if err := u.launcher(path, args); err != nil {
		u.set(func(s *Status) { s.State, s.Error = StateError, "could not start the installer: "+err.Error() })
		return err
	}
	u.Log.Info("update installer started", "path", path, "dir", installDir)
	return nil
}

func fileHasSHA256(path, want string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	return hex.EncodeToString(h.Sum(nil)) == want, nil
}

type progressWriter struct {
	total, done int64
	last        float64
	report      func(float64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if frac := float64(p.done) / float64(p.total); frac-p.last >= 0.01 || frac >= 1 {
		p.last = frac
		p.report(min(frac, 1))
	}
	return len(b), nil
}
