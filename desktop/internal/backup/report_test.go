package backup

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/pack"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/vdcrypt"
)

type fakeTrees struct {
	loc   map[string]string // blob hex → pack
	trees map[string]*snapshot.Tree
	reads map[string]int
}

func hexID(n int) string { return fmt.Sprintf("%064x", n) }

func (f *fakeTrees) Lookup(id pack.BlobID) (repo.Location, bool) {
	p, ok := f.loc[fmt.Sprintf("%x", id[:])]
	return repo.Location{Pack: p}, ok
}

func (f *fakeTrees) ReadTree(_ context.Context, idHex string) (*snapshot.Tree, error) {
	f.reads[idHex]++
	t, ok := f.trees[idHex]
	if !ok {
		return nil, errors.New("no tree")
	}
	return t, nil
}

func TestSnapshotPacks(t *testing.T) {
	sp := func(s string) *string { return &s }
	shared, cont := hexID(10), hexID(11)
	f := &fakeTrees{reads: map[string]int{}, loc: map[string]string{
		hexID(1): "pm1", hexID(2): "pm1", shared: "pm2", cont: "pm2", hexID(20): "inv",
		hexID(100): "pd1", hexID(101): "pd2", hexID(102): "pd3", hexID(103): "pd4",
	}, trees: map[string]*snapshot.Tree{
		hexID(1): {Entries: []snapshot.Entry{{N: "a", T: snapshot.TypeFile, Ch: []string{hexID(100), hexID(101)}}, {N: "s", T: snapshot.TypeDir, Sub: sp(shared)}}},
		hexID(2): {Entries: []snapshot.Entry{{N: "s2", T: snapshot.TypeDir, Sub: sp(shared)}}},
		shared:   {Entries: []snapshot.Entry{{N: "b", T: snapshot.TypeFile, Ch: []string{hexID(101)}, X: &snapshot.Ext{ADS: []snapshot.ADS{{Name: "z", Ch: []string{hexID(103)}}}}}}, Cont: sp(cont)},
		cont:     {Entries: []snapshot.Entry{{N: "c", T: snapshot.TypeFile, Ch: []string{hexID(102)}}}},
	}}
	root := &snapshot.Root{Roots: []snapshot.RootRef{{TokenPath: "{DOCUMENTS}", Tree: hexID(1)}, {TokenPath: "{DESKTOP}", Tree: hexID(2)}}, Inventory: sp(hexID(20))}
	got, err := snapshotPacks(context.Background(), f, root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"inv", "pd1", "pd2", "pd3", "pd4", "pm1", "pm2"}
	if !slices.Equal(got, want) {
		t.Fatalf("packs %v, want %v", got, want)
	}
	if f.reads[shared] != 1 {
		t.Fatalf("shared subtree read %d times", f.reads[shared])
	}

	delete(f.loc, hexID(102))
	if _, err := snapshotPacks(context.Background(), f, root); !errors.Is(err, repo.ErrBlobNotFound) {
		t.Fatalf("missing blob: %v", err)
	}
	bad := &snapshot.Root{Roots: []snapshot.RootRef{{Tree: "xyz"}}}
	if _, err := snapshotPacks(context.Background(), f, bad); err == nil {
		t.Fatal("bad tree id accepted")
	}
}

func TestDeviceKeyID(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	other, _, _ := ed25519.GenerateKey(nil)
	dev := func(id int64, p ed25519.PublicKey) *vdcrypt.KeyFileDevice {
		return &vdcrypt.KeyFileDevice{ID: id, SignPub: p}
	}
	files := map[string]*vdcrypt.KeyFile{
		"a": {KeyID: "k1", Kind: vdcrypt.KindDevice, Epoch: 1, Device: dev(3, pub)},
		"b": {KeyID: "k2", Kind: vdcrypt.KindDevice, Epoch: 2, Device: dev(3, pub)},
		"c": {KeyID: "k3", Kind: vdcrypt.KindDevice, Epoch: 3, Device: dev(3, other)},
		"d": {KeyID: "k4", Kind: vdcrypt.KindDevice, Epoch: 4, Device: dev(4, pub)},
		"e": {KeyID: "k5", Kind: vdcrypt.KindEscrow, Epoch: 5},
	}
	if got := deviceKeyID(files, 3, pub); got != "k2" {
		t.Fatalf("key id %q", got)
	}
	if got := deviceKeyID(files, 9, pub); got != "" {
		t.Fatalf("unknown device key id %q", got)
	}
}

// TestRepoEngineCommitRefs runs the real pipeline (P1-25 fixture) through
// RepoEngine and checks that pack_oids covers earlier runs' packs.
func TestRepoEngineCommitRefs(t *testing.T) {
	ctx := context.Background()
	f := newPipeFixture(t)
	f.put("a.bin", f.random(2<<20), 100)
	f.put("sub/b.txt", []byte("hello"), 100)
	eng := &RepoEngine{Pipeline: f.pipeline()}
	res1, err := eng.Run(ctx, f.params("run-1"), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	idx1, packs1, err := eng.CommitRefs(ctx, res1.Root)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(res1.Packs)
	slices.Sort(want)
	if !slices.Equal(packs1, want) || len(idx1) == 0 {
		t.Fatalf("first run packs %v, want %v (index %v)", packs1, want, idx1)
	}

	f.put("c.txt", []byte("new file"), 100)
	res2, err := eng.Run(ctx, f.params("run-2"), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	idx2, packs2, err := eng.CommitRefs(ctx, res2.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(packs2) <= len(res2.Packs) || len(idx2) < len(idx1) {
		t.Fatalf("second run packs %v (run uploaded %v)", packs2, res2.Packs)
	}
	for _, p := range packs2 {
		if _, err := f.be.Stat(ctx, repo.KindData, p); err != nil {
			t.Fatalf("pack %s not stored: %v", p, err)
		}
	}
	for _, p := range res2.Packs {
		if !slices.Contains(packs2, p) {
			t.Fatalf("this run's pack %s missing from %v", p, packs2)
		}
	}
	// The packs collected by the pipeline match a read-back of the trees.
	if res2.ReferencedPacks == nil {
		t.Fatal("no ReferencedPacks")
	}
	read, err := snapshotPacks(ctx, repoTrees{f.pipeline().Repo}, res2.Root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(read, packs2) || !slices.Equal(res2.ReferencedPacks, packs2) {
		t.Fatalf("collected %v, read back %v, committed %v", res2.ReferencedPacks, read, packs2)
	}
	// Another root (not the latest Run's) is read back.
	if _, packs1b, err := eng.CommitRefs(ctx, res1.Root); err != nil || !slices.Equal(packs1b, packs1) {
		t.Fatalf("earlier root packs %v, %v; want %v", packs1b, err, packs1)
	}
}
