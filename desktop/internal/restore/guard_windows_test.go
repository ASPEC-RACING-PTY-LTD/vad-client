//go:build windows

package restore

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

// TestGuardT1 is T-G1 for restore: targets in %WINDIR%, Program Files,
// another user's profile and through a junction are all blocked by the real
// guard, recorded as blocked items, and nothing is written there.
func TestGuardT1(t *testing.T) {
	f := newBackup(t, map[string]string{
		"win/w.txt":  "to windows",
		"pf/p.txt":   "to program files",
		"bob/b.txt":  "to bob",
		"junc/j.txt": "through a junction",
		"ok/o.txt":   "allowed",
	})
	base := t.TempDir()
	alice := filepath.Join(base, "alice")
	for _, d := range []string{filepath.Join(alice, "real"), filepath.Join(base, "bob")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(alice, "link")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, filepath.Join(alice, "real")).CombinedOutput(); err != nil {
		t.Skipf("mklink /J unavailable: %v: %s", err, out)
	}
	env := guard.SystemEnv()
	env.ProfilesDir = base // alice and bob are the profiles
	g, err := guard.New(env)
	if err != nil {
		t.Fatal(err)
	}
	winTarget := filepath.Join(os.Getenv("WINDIR"), "VaultDockRestoreT1")
	pfTarget := filepath.Join(os.Getenv("ProgramFiles"), "VaultDockRestoreT1")
	overrides := []wire.RestorePathMapping{
		{From: "{DOCUMENTS}/win", To: winTarget},
		{From: "{DOCUMENTS}/pf", To: pfTarget},
		{From: "{DOCUMENTS}/bob", To: filepath.Join(base, "bob", "Documents")},
		{From: "{DOCUMENTS}/junc", To: filepath.Join(link, "restored")},
		{From: "{DOCUMENTS}/ok", To: filepath.Join(alice, "real", "ok")},
	}
	c := &collected{}
	p, err := Run(t.Context(), f.r, Request{Roots: f.root.Roots, Overrides: overrides,
		Tokens:  snapshot.Tokens{snapshot.TokenDocuments: filepath.Join(alice, "Documents")},
		Profile: guard.Profile{SID: testSID, Root: alice}}, Options{Guard: g.Check, OnItem: c.add})
	if err != nil {
		t.Fatal(err)
	}
	blocked := map[string]string{}
	for _, it := range c.items {
		if it.Status == wire.RestoreItemBlocked && strings.HasSuffix(it.Path, ".txt") {
			blocked[it.Path] = it.Message
		}
	}
	for _, want := range []string{"{DOCUMENTS}/win/w.txt", "{DOCUMENTS}/pf/p.txt", "{DOCUMENTS}/bob/b.txt", "{DOCUMENTS}/junc/j.txt"} {
		if !strings.Contains(blocked[want], "blocked by system-file protection") {
			t.Errorf("%s not blocked (items %+v)", want, c.items)
		}
	}
	if p.FilesDone != 1 || p.Status() != wire.RestoreStatusPartial {
		t.Errorf("progress %+v", p)
	}
	if b, err := os.ReadFile(filepath.Join(alice, "real", "ok", "o.txt")); err != nil || string(b) != "allowed" {
		t.Errorf("allowed file: %q %v", b, err)
	}
	for _, p := range []string{winTarget, pfTarget, filepath.Join(base, "bob", "Documents"), filepath.Join(alice, "real", "restored")} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s was created", p)
		}
	}
}
