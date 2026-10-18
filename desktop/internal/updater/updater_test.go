package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/release"
)

// fakeGitHub serves the releases API and asset downloads in-process. The
// asset URLs keep their real github.com host so the trust checks apply.
type fakeGitHub struct {
	tag, digest string
	installer   []byte
	redirectTo  string
}

func (f *fakeGitHub) RoundTrip(r *http.Request) (*http.Response, error) {
	resp := func(code int, body []byte) *http.Response {
		return &http.Response{StatusCode: code, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}, Request: r}
	}
	name := "VaultDock-" + strings.TrimPrefix(f.tag, "v") + "-amd64-setup.exe"
	switch {
	case r.URL.Host == "api.github.com" && r.URL.Path == "/repos/acme/vault/releases/latest":
		body := fmt.Sprintf(`{"tag_name":%q,"html_url":"https://github.com/acme/vault/releases/tag/%s","body":"notes","assets":[{"name":%q,"size":%d,"browser_download_url":"https://github.com/acme/vault/releases/download/%s/%s","digest":%q}]}`,
			f.tag, f.tag, name, len(f.installer), f.tag, name, f.digest)
		return resp(200, []byte(body)), nil
	case r.URL.Host == "github.com" && strings.HasSuffix(r.URL.Path, "/"+name):
		if f.redirectTo != "" {
			res := resp(302, nil)
			res.Header.Set("Location", f.redirectTo)
			return res, nil
		}
		return resp(200, f.installer), nil
	}
	return resp(404, []byte(`{}`)), nil
}

func newTestUpdater(t *testing.T, gh *fakeGitHub, current string) (*Updater, *[]string) {
	t.Helper()
	u := New("acme/vault", current, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	u.API.HTTP = &http.Client{Transport: gh}
	u.DL = &http.Client{Transport: gh, CheckRedirect: release.TrustedRedirect}
	var launched []string
	u.launcher = func(path, args string) error {
		launched = append(launched, path+" "+args)
		return nil
	}
	return u, &launched
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

func TestCheckDownloadInstall(t *testing.T) {
	payload := []byte("MZ installer bytes")
	gh := &fakeGitHub{tag: "v1.3.0", installer: payload, digest: digest(payload)}
	u, launched := newTestUpdater(t, gh, "1.2.0")

	avail, err := u.Check(context.Background())
	if err != nil || !avail {
		t.Fatalf("check: %v %v", avail, err)
	}
	if s := u.Status(); s.State != StateAvailable || s.Latest != "1.3.0" {
		t.Fatalf("status %+v", s)
	}
	if err := u.Download(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !u.Ready() || u.Status().State != StateReady {
		t.Fatalf("not ready: %+v", u.Status())
	}
	if err := u.Install(`C:\Users\a b\AppData\Local\Programs\VaultDock`); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(u.Dir, "VaultDock-1.3.0-amd64-setup.exe") + ` /S /RELAUNCH /D=C:\Users\a b\AppData\Local\Programs\VaultDock`
	if len(*launched) != 1 || (*launched)[0] != want {
		t.Fatalf("launched %v", *launched)
	}
}

func TestChecksumMismatchIsRejected(t *testing.T) {
	gh := &fakeGitHub{tag: "v1.3.0", installer: []byte("tampered"), digest: digest([]byte("original"))}
	u, launched := newTestUpdater(t, gh, "1.2.0")
	if _, err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := u.Download(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got %v", err)
	}
	if u.Ready() || u.Install(`C:\x`) == nil || len(*launched) != 0 {
		t.Fatal("tampered installer became installable")
	}
	entries, _ := os.ReadDir(u.Dir)
	if len(entries) != 0 {
		t.Fatalf("rejected download left files: %v", entries)
	}
}

func TestFileChangedBeforeInstall(t *testing.T) {
	payload := []byte("good installer")
	gh := &fakeGitHub{tag: "v2.0.0", installer: payload, digest: digest(payload)}
	u, launched := newTestUpdater(t, gh, "1.0.0")
	u.Check(context.Background())
	if err := u.Download(context.Background()); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(u.Dir, "VaultDock-2.0.0-amd64-setup.exe"), []byte("swapped"), 0o600)
	if err := u.Install(`C:\x`); err == nil || len(*launched) != 0 {
		t.Fatal("modified installer was launched")
	}
}

func TestUntrustedRedirectAndMissingChecksum(t *testing.T) {
	payload := []byte("x")
	gh := &fakeGitHub{tag: "v1.1.0", installer: payload, digest: digest(payload), redirectTo: "https://evil.example/payload.exe"}
	u, _ := newTestUpdater(t, gh, "1.0.0")
	u.Check(context.Background())
	if err := u.Download(context.Background()); err == nil {
		t.Fatal("download followed an untrusted redirect")
	}

	gh2 := &fakeGitHub{tag: "v1.1.0", installer: payload}
	u2, _ := newTestUpdater(t, gh2, "1.0.0")
	u2.Check(context.Background())
	if err := u2.Download(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("release without checksum accepted: %v", err)
	}
}

func TestUpToDateAndDevBuilds(t *testing.T) {
	gh := &fakeGitHub{tag: "v1.0.0", installer: []byte("x"), digest: digest([]byte("x"))}
	for _, cur := range []string{"1.0.0", "1.1.0", "dev"} {
		u, _ := newTestUpdater(t, gh, cur)
		avail, err := u.Check(context.Background())
		if err != nil || avail || u.Status().State != StateUpToDate {
			t.Fatalf("current %s: avail=%v err=%v state=%s", cur, avail, err, u.Status().State)
		}
	}
	if New("", "1.0.0", t.TempDir(), slog.Default()).Status().State != StateDisabled {
		t.Fatal("empty repo should disable updates")
	}
}
