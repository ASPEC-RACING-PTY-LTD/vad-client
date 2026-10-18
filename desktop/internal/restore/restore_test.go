package restore

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/backup"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/tokens"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

const testSID = "S-1-5-21-1111111111-2222222222-3333333333-1001"

// passToken runs work directly and counts impersonations.
type passToken struct {
	mu    sync.Mutex
	calls int
}

func (t *passToken) SID() string                                  { return testSID }
func (t *passToken) Source() tokens.Source                        { return tokens.SourceSession }
func (t *passToken) SessionID() (uint32, bool)                    { return 1, true }
func (t *passToken) Caps() tokens.Caps                            { return tokens.CapsFor(tokens.SourceSession) }
func (t *passToken) AccessCheckPath(string, uint32) (bool, error) { return true, nil }
func (t *passToken) Close() error                                 { return nil }
func (t *passToken) Impersonate(fn func() error) error {
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	return fn()
}

type passProvider struct{ tok *passToken }

func (p passProvider) ForProfile(string) (tokens.Token, error)  { return p.tok, nil }
func (p passProvider) SessionOnly(string) (tokens.Token, error) { return p.tok, nil }

func allow(guard.Op, string, guard.Profile) error { return nil }

// backupFixture backs up a temp folder with the real pipeline and returns
// the repository and snapshot root.
type backupFixture struct {
	src   string
	files map[string]string // rel → content
	mtime time.Time
	r     *repo.Repo
	root  *snapshot.Root
	be    *repo.MemBackend
	rec   string
	oid   string
}

func newBackup(t *testing.T, files map[string]string) *backupFixture {
	t.Helper()
	f := &backupFixture{src: filepath.Join(t.TempDir(), "Documents"), files: files, mtime: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	for rel, c := range files {
		p := filepath.Join(f.src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, f.mtime, f.mtime); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	f.be = repo.NewMemBackend()
	var err error
	if f.r, f.rec, err = repo.Init(ctx, f.be, repo.InitOptions{}); err != nil {
		t.Fatal(err)
	}
	st, err := backup.OpenDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	_, dev, _ := ed25519.GenerateKey(nil)
	prof := guard.Profile{SID: testSID, Root: filepath.Dir(f.src)}
	p := &backup.Pipeline{Store: st, Repo: f.r, RepoID: 1, Tokens: passProvider{&passToken{}},
		Walker: backup.Walker{Guard: allow, EmitDirs: true}, SpoolDir: filepath.Join(t.TempDir(), "spool")}
	res, err := p.Run(ctx, backup.Params{JobID: 1, RunUUID: "run-1",
		Spec: backup.WalkSpec{ProfileSID: testSID, Paths: []string{"{DOCUMENTS}"}, Tokens: snapshot.Tokens{snapshot.TokenDocuments: f.src}, Profile: prof, Epoch: 1},
		Root: snapshot.Root{Job: snapshot.Job{ID: 1, Name: "Docs", Kind: "folders", Mode: "backup"}, Device: snapshot.Device{ID: 1}}, DeviceKey: dev})
	if err != nil {
		t.Fatal(err)
	}
	f.root, f.oid = res.Root, res.SnapshotOID
	return f
}

type collected struct {
	mu    sync.Mutex
	items []wire.RestoreItem
}

func (c *collected) add(it wire.RestoreItem) {
	c.mu.Lock()
	c.items = append(c.items, it)
	c.mu.Unlock()
}

func (c *collected) byStatus(s string) []string {
	var out []string
	for _, it := range c.items {
		if it.Status == s {
			out = append(out, it.Path)
		}
	}
	sort.Strings(out)
	return out
}

func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	return out
}

func (f *backupFixture) run(t *testing.T, target, policy string, sel []wire.RestoreSelection, o Options) (Progress, *collected) {
	t.Helper()
	c := &collected{}
	if o.Guard == nil {
		o.Guard = allow
	}
	o.OnItem = c.add
	p, err := Run(context.Background(), f.r, Request{Roots: f.root.Roots, Selection: sel, Policy: policy,
		Tokens: snapshot.Tokens{snapshot.TokenDocuments: target}, Profile: guard.Profile{SID: testSID, Root: filepath.Dir(target)}}, o)
	if err != nil {
		t.Fatal(err)
	}
	return p, c
}

func TestRestoreToEmptyFolder(t *testing.T) {
	f := newBackup(t, map[string]string{"a.txt": "alpha", "sub/b.txt": "bravo", "sub/deep/c.txt": "charlie", "empty.txt": ""})
	target := filepath.Join(t.TempDir(), "Documents")
	tok := &passToken{}
	var beats []Progress
	p, c := f.run(t, target, "", nil, Options{Token: tok, OnProgress: func(p Progress) { beats = append(beats, p) }})
	if p.FilesTotal != 4 || p.FilesDone != 4 || p.Failed != 0 || p.Status() != wire.RestoreStatusCompleted {
		t.Fatalf("progress %+v; items %+v", p, c.items)
	}
	got := readTree(t, target)
	for rel, want := range f.files {
		if got[rel] != want {
			t.Errorf("%s = %q, want %q", rel, got[rel], want)
		}
	}
	if len(got) != len(f.files) {
		t.Errorf("extra files restored: %v", got)
	}
	if fi, err := os.Stat(filepath.Join(target, "sub", "b.txt")); err != nil || !fi.ModTime().Equal(f.mtime) {
		t.Errorf("mtime not restored: %v %v", fi.ModTime(), err)
	}
	if tok.calls == 0 {
		t.Error("nothing ran as the target user")
	}
	if len(beats) < 2 || beats[len(beats)-1].FilesDone != 4 {
		t.Errorf("progress beats %+v", beats)
	}
	// Selection: only sub, excluding deep.
	target2 := filepath.Join(t.TempDir(), "Documents")
	p, _ = f.run(t, target2, "", []wire.RestoreSelection{{Path: "{DOCUMENTS}/sub", Include: true}, {Path: "{DOCUMENTS}/sub/deep", Include: false}}, Options{})
	if got := readTree(t, target2); p.FilesDone != 1 || len(got) != 1 || got["sub/b.txt"] != "bravo" {
		t.Fatalf("selected restore %+v: %v", p, got)
	}
}

func TestRestoreConflictPolicies(t *testing.T) {
	f := newBackup(t, map[string]string{"a.txt": "alpha", "b.txt": "bravo"})
	target := filepath.Join(t.TempDir(), "Documents")
	f.run(t, target, "", nil, Options{})

	// Identical files are skipped (except overwrite).
	p, c := f.run(t, target, wire.RestoreConflictRename, nil, Options{})
	if p.Skipped != 2 || p.FilesDone != 0 || len(c.byStatus(wire.RestoreItemSkipped)) != 2 {
		t.Fatalf("second run %+v", p)
	}
	// Local edits: skip keeps them, rename restores beside them.
	os.WriteFile(filepath.Join(target, "a.txt"), []byte("local edit"), 0o644)
	if p, _ := f.run(t, target, wire.RestoreConflictSkip, nil, Options{}); p.FilesDone != 0 {
		t.Fatalf("skip restored %+v", p)
	}
	p, _ = f.run(t, target, wire.RestoreConflictRename, nil, Options{})
	got := readTree(t, target)
	if p.FilesDone != 1 || got["a.txt"] != "local edit" || got["a (restored).txt"] != "alpha" {
		t.Fatalf("rename: %+v %v", p, got)
	}
	// Newer: the local edit is newer than the snapshot copy, so it stays.
	if p, _ := f.run(t, target, wire.RestoreConflictNewer, nil, Options{}); p.FilesDone != 0 {
		t.Fatalf("newer replaced a newer local file: %+v", p)
	}
	// Overwrite keeps the previous version beside it; nothing is deleted.
	now := time.Date(2026, 10, 18, 15, 30, 5, 0, time.UTC)
	p, c = f.run(t, target, wire.RestoreConflictOverwrite, []wire.RestoreSelection{{Path: "{DOCUMENTS}/a.txt", Include: true}}, Options{Now: func() time.Time { return now }})
	got = readTree(t, target)
	if p.FilesDone != 1 || got["a.txt"] != "alpha" || got["a (replaced 2026-10-18 153005).txt"] != "local edit" {
		t.Fatalf("overwrite: %+v %v", p, got)
	}
	if done := c.items[0]; done.Action != wire.RestoreActionOverwrite || !strings.Contains(done.Message, "kept as") {
		t.Fatalf("overwrite item %+v", done)
	}
	for name := range got {
		if strings.Contains(name, ".vdrestore-") {
			t.Fatalf("temporary file left behind: %s", name)
		}
	}
}

func TestRestoreGuardBlocksAndRecords(t *testing.T) {
	f := newBackup(t, map[string]string{"ok.txt": "fine", "bad/x.txt": "nope"})
	target := filepath.Join(t.TempDir(), "Documents")
	block := func(op guard.Op, p string, prof guard.Profile) error {
		if op != guard.OpRestoreWrite || prof.SID != testSID {
			t.Errorf("guard called with %v %+v", op, prof)
		}
		if strings.Contains(p, `bad`) {
			return &guard.BlockedError{Op: op, Path: p, Reason: "protected system location", Err: guard.ErrProtected}
		}
		return nil
	}
	p, c := f.run(t, target, "", nil, Options{Guard: block})
	if p.Blocked != 2 || p.FilesDone != 1 || p.Status() != wire.RestoreStatusPartial {
		t.Fatalf("progress %+v; items %+v", p, c.items)
	}
	if b := c.byStatus(wire.RestoreItemBlocked); len(b) != 2 || b[0] != "{DOCUMENTS}/bad" || b[1] != "{DOCUMENTS}/bad/x.txt" {
		t.Fatalf("blocked %v", b)
	}
	if _, err := os.Stat(filepath.Join(target, "bad")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a blocked folder was created")
	}
	if _, err := Run(context.Background(), f.r, Request{Roots: f.root.Roots}, Options{}); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("no profile = %v", err)
	}
}

func TestRestoreRecoveryKeyOnly(t *testing.T) {
	// A repository opened with nothing but the recovery key restores the
	// same bytes (the server-independent path of T-R3).
	f := newBackup(t, map[string]string{"a.txt": "alpha", "sub/b.txt": "bravo"})
	cred, err := repo.RecoveryCredential(f.rec)
	if err != nil {
		t.Fatal(err)
	}
	r, err := repo.Open(context.Background(), f.be, cred, repo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	f.r = r
	target := filepath.Join(t.TempDir(), "Documents")
	if p, _ := f.run(t, target, "", nil, Options{}); p.FilesDone != 2 {
		t.Fatalf("recovery restore %+v", p)
	}
	if got := readTree(t, target); got["a.txt"] != "alpha" || got["sub/b.txt"] != "bravo" {
		t.Fatalf("restored %v", got)
	}
}
