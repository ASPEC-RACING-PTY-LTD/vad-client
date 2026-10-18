package backup

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/tokens"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/pack"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"
)

var errInjected = errors.New("injected backend failure")

// failBackend fails data PUTs once allowData successful ones happened
// (allowData < 0 = never fail).
type failBackend struct {
	*repo.MemBackend
	mu        sync.Mutex
	allowData int
	dataPuts  int
}

func (b *failBackend) Put(ctx context.Context, kind repo.Kind, oid string, data []byte) error {
	if kind == repo.KindData {
		b.mu.Lock()
		if b.allowData >= 0 && b.dataPuts >= b.allowData {
			b.mu.Unlock()
			return errInjected
		}
		b.dataPuts++
		b.mu.Unlock()
	}
	return b.MemBackend.Put(ctx, kind, oid, data)
}

func (b *failBackend) setAllow(n int) {
	b.mu.Lock()
	b.allowData, b.dataPuts = n, 0
	b.mu.Unlock()
}

type pipeFixture struct {
	t        *testing.T
	tok      *fakeToken
	fs       *fakeFS
	content  map[string][]byte
	openErr  map[string]error
	onOpen   func(path string)
	mu       sync.Mutex
	opened   []string
	be       *failBackend
	repo     *repo.Repo
	recovery string
	stateDir string
	st       *Store
	dev      ed25519.PrivateKey
	rnd      *rand.ChaCha8
}

func newPipeFixture(t *testing.T) *pipeFixture {
	t.Helper()
	tok := &fakeToken{src: tokens.SourceSession}
	f := &pipeFixture{t: t, tok: tok, fs: newFakeFS(t, tok), content: map[string][]byte{}, openErr: map[string]error{},
		be: &failBackend{MemBackend: repo.NewMemBackend(), allowData: -1}, stateDir: t.TempDir(),
		rnd: rand.NewChaCha8([32]byte{7})}
	f.fs.dir(docs)
	var err error
	f.repo, f.recovery, err = repo.Init(context.Background(), f.be, repo.InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if f.st, err = OpenDir(f.stateDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.st.Close() })
	_, f.dev, _ = ed25519.GenerateKey(nil)
	return f
}

// reopen simulates a new process: a fresh Store and a repo opened from
// the backend (so only written index objects are known).
func (f *pipeFixture) reopen() {
	f.t.Helper()
	f.st.Close()
	var err error
	if f.st, err = OpenDir(f.stateDir); err != nil {
		f.t.Fatal(err)
	}
	cred, err := repo.RecoveryCredential(f.recovery)
	if err != nil {
		f.t.Fatal(err)
	}
	if f.repo, err = repo.Open(context.Background(), f.be, cred, repo.Options{}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *pipeFixture) random(n int) []byte {
	b := make([]byte, n)
	f.rnd.Read(b)
	return b
}

// put creates or replaces a file (mtime distinguishes versions).
func (f *pipeFixture) put(rel string, data []byte, mtime int64) {
	p := filepath.Join(docs, filepath.FromSlash(rel))
	for d := filepath.Dir(p); d != docs; d = filepath.Dir(d) {
		f.fs.dir(d)
	}
	f.fs.nodes[p] = FileInfo{Name: filepath.Base(p), Size: int64(len(data)), MTime: mtime, CTime: 50, Attrs: AttrArchive}
	f.content[p] = data
}

func (f *pipeFixture) remove(rel string) {
	p := filepath.Join(docs, filepath.FromSlash(rel))
	delete(f.fs.nodes, p)
	delete(f.content, p)
}

func (f *pipeFixture) Open(tok tokens.Token, path string) (io.ReadCloser, error) {
	var data []byte
	err := tok.Impersonate(func() error {
		if !f.tok.inside {
			f.t.Errorf("open %s outside impersonation", path)
		}
		f.mu.Lock()
		f.opened = append(f.opened, path)
		f.mu.Unlock()
		if f.onOpen != nil {
			f.onOpen(path)
		}
		if err := f.openErr[path]; err != nil {
			return err
		}
		var ok bool
		if data, ok = f.content[path]; !ok {
			return os.ErrNotExist
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *pipeFixture) pipeline() *Pipeline {
	return &Pipeline{Store: f.st, Repo: f.repo, RepoID: 7, Tokens: &fakeProvider{tok: f.tok},
		Walker: Walker{FS: f.fs, Guard: func(guard.Op, string, guard.Profile) error { return nil }},
		Opener: f, SpoolDir: filepath.Join(f.stateDir, "spool"), PackTarget: 1 << 20, IndexEvery: 2}
}

func (f *pipeFixture) params(run string) Params {
	return Params{JobID: 1, RunUUID: run, Spec: testSpec(),
		Root:      snapshot.Root{Job: snapshot.Job{ID: 1, Name: "Documents", Kind: "folders", Mode: "backup"}, Device: snapshot.Device{ID: 1}},
		DeviceKey: f.dev}
}

func (f *pipeFixture) resetOpened() {
	f.mu.Lock()
	f.opened = nil
	f.mu.Unlock()
}

// restore reads every file of a snapshot back through the repository.
func (f *pipeFixture) restore(oid string) map[string][]byte {
	f.t.Helper()
	ctx := context.Background()
	root, _, err := f.repo.LoadSnapshot(ctx, oid)
	if err != nil {
		f.t.Fatalf("load snapshot: %v", err)
	}
	keys, _ := f.repo.Keys()
	out := map[string][]byte{}
	var walk func(tp, tree string)
	walk = func(tp, tree string) {
		for tree != "" {
			tr, err := f.repo.ReadTree(ctx, tree)
			if err != nil {
				f.t.Fatalf("tree %s: %v", tp, err)
			}
			for _, e := range tr.Entries {
				p := tp + "/" + e.N
				if e.T == snapshot.TypeDir {
					walk(p, *e.Sub)
					continue
				}
				var buf []byte
				for _, c := range e.Ch {
					raw, _ := hex.DecodeString(c)
					b, err := f.repo.ReadBlob(ctx, pack.BlobID(raw))
					if err != nil {
						f.t.Fatalf("blob of %s: %v", p, err)
					}
					buf = append(buf, b...)
				}
				h := snapshot.FileHashBytes(keys.FileHash, buf)
				if !snapshot.VerifyFileHash(h, *e.H) || int64(len(buf)) != e.S {
					f.t.Errorf("%s: hash or size mismatch", p)
				}
				out[p] = buf
			}
			tree = ""
			if tr.Cont != nil {
				tree = *tr.Cont
			}
		}
	}
	for _, r := range root.Roots {
		walk(r.TokenPath, r.Tree)
	}
	return out
}

// expected maps the fixture's files to token paths.
func (f *pipeFixture) expected() map[string][]byte {
	out := map[string][]byte{}
	for p, b := range f.content {
		rel, _ := filepath.Rel(docs, p)
		out["{DOCUMENTS}/"+filepath.ToSlash(rel)] = b
	}
	return out
}

func (f *pipeFixture) checkRestore(oid string) {
	f.t.Helper()
	got, want := f.restore(oid), f.expected()
	if len(got) != len(want) {
		f.t.Fatalf("restored %d files, want %d: %v", len(got), len(want), keysOf(got))
	}
	for p, b := range want {
		if !bytes.Equal(got[p], b) {
			f.t.Errorf("%s: restored content differs", p)
		}
	}
}

func (f *pipeFixture) checkSpoolEmpty() {
	f.t.Helper()
	pend, err := f.st.PendingPacks(context.Background(), 7)
	if err != nil || len(pend) != 0 {
		f.t.Fatalf("pending packs %v %v", pend, err)
	}
	ents, _ := os.ReadDir(filepath.Join(f.stateDir, "spool", "7"))
	if len(ents) != 0 {
		f.t.Fatalf("spool not empty: %d files", len(ents))
	}
}

func keysOf(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestPipelineFirstBackup(t *testing.T) {
	f := newPipeFixture(t)
	big := f.random(3 << 20)
	f.put("a.bin", big, 100)
	f.put("dup.bin", big, 100) // same content: deduplicated
	f.put("sub/b.txt", []byte("hello world"), 100)
	f.put("sub/deeper/empty.txt", nil, 100)
	f.put("photo.JPG", f.random(700<<10), 100)

	var beats []RunStats
	var mu sync.Mutex
	p := f.pipeline()
	p.Progress = func(s RunStats) { mu.Lock(); beats = append(beats, s); mu.Unlock() }
	res, err := p.Run(context.Background(), f.params("run-1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.SnapshotOID == "" || res.Root == nil || res.Root.Sig == "" {
		t.Fatalf("no snapshot: %+v", res)
	}
	if res.Root.Stats.Files != 5 || res.Root.Stats.Bytes != int64(2*len(big)+11+700<<10) {
		t.Errorf("root stats %+v", res.Root.Stats)
	}
	if res.Root.Tokens[snapshot.TokenDocuments] != docs || len(res.Root.Roots) != 1 || res.Root.Roots[0].TokenPath != "{DOCUMENTS}" {
		t.Errorf("root tokens/roots %+v %+v", res.Root.Tokens, res.Root.Roots)
	}
	if res.Stats.BytesNew != int64(len(big)+11+700<<10) {
		t.Errorf("bytes new %d: duplicate content not deduplicated", res.Stats.BytesNew)
	}
	if res.Stats.FilesDone != 5 || res.Stats.BytesUploaded == 0 || len(res.Packs) < 2 {
		t.Errorf("stats %+v packs %d", res.Stats, len(res.Packs))
	}
	if len(beats) == 0 || beats[len(beats)-1].Phase != PhaseSnapshot {
		t.Errorf("progress beats %+v", beats)
	}
	f.checkSpoolEmpty()

	// The snapshot verifies and restores from a freshly opened repo, so the
	// index objects were written.
	f.reopen()
	if _, stored, err := f.repo.LoadSnapshot(context.Background(), res.SnapshotOID); err != nil {
		t.Fatal(err)
	} else if err := snapshot.VerifyRoot(stored, f.dev.Public().(ed25519.PublicKey)); err != nil {
		t.Fatal(err)
	}
	f.checkRestore(res.SnapshotOID)

	c, ok, err := f.st.FileCache(context.Background(), 1, filepath.Join(docs, "a.bin"))
	if err != nil || !ok || len(c.H) != 32 || len(c.Chunks) == 0 || c.SeenRun != "run-1" || c.Epoch != 1 {
		t.Fatalf("file cache %+v %v %v", c, ok, err)
	}
}

func TestPipelineIncremental(t *testing.T) {
	f := newPipeFixture(t)
	f.put("keep.bin", f.random(2<<20), 100)
	f.put("change.txt", []byte("version 1"), 100)
	f.put("gone.txt", []byte("bye"), 100)
	r1, err := f.pipeline().Run(context.Background(), f.params("run-1"))
	if err != nil {
		t.Fatal(err)
	}

	f.reopen()
	f.resetOpened()
	f.put("change.txt", []byte("version 2"), 200)
	f.put("new.txt", []byte("new file"), 100)
	f.remove("gone.txt")
	parent := r1.SnapshotOID
	in := f.params("run-2")
	in.Root.Parent = &parent
	r2, err := f.pipeline().Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(f.opened)
	want := []string{filepath.Join(docs, "change.txt"), filepath.Join(docs, "new.txt")}
	if strings.Join(f.opened, "|") != strings.Join(want, "|") {
		t.Errorf("opened %v, want only %v", f.opened, want)
	}
	if r2.Stats.BytesNew != int64(len("version 2")+len("new file")) {
		t.Errorf("bytes new %d", r2.Stats.BytesNew)
	}
	if r2.Walk.Unchanged != 1 || !r2.Walk.Complete || r2.Pruned != 1 {
		t.Errorf("walk %+v pruned %d", r2.Walk, r2.Pruned)
	}
	if _, ok, _ := f.st.FileCache(context.Background(), 1, filepath.Join(docs, "gone.txt")); ok {
		t.Error("cache row of a deleted file not pruned")
	}
	if c, ok, _ := f.st.FileCache(context.Background(), 1, filepath.Join(docs, "keep.bin")); !ok || c.SeenRun != "run-2" {
		t.Errorf("unchanged file not marked seen: %+v", c)
	}
	f.checkRestore(r2.SnapshotOID)
	f.checkSpoolEmpty()

	// A third run with nothing changed writes no data packs.
	f.resetOpened()
	r3, err := f.pipeline().Run(context.Background(), f.params("run-3"))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.opened) != 0 || r3.Stats.BytesNew != 0 {
		t.Errorf("unchanged run read %v, new %d", f.opened, r3.Stats.BytesNew)
	}
	for _, oid := range r3.Packs {
		if pk, _ := f.st.Pack(context.Background(), oid); pk != nil && pk.Class == int(pack.ClassData) {
			t.Errorf("unchanged run wrote data pack %s", oid)
		}
	}
}

func TestPipelineResumeAfterInterruptedUpload(t *testing.T) {
	f := newPipeFixture(t)
	for i := range 6 {
		f.put("f"+string(rune('a'+i))+".bin", f.random(1200<<10), 100)
	}
	f.be.setAllow(1)
	res, err := f.pipeline().Run(context.Background(), f.params("run-1"))
	if !errors.Is(err, errInjected) {
		t.Fatalf("err = %v, want injected failure", err)
	}
	if res.SnapshotOID != "" {
		t.Fatal("snapshot written after a failed upload")
	}
	pend, _ := f.st.PendingPacks(context.Background(), 7)
	if len(pend) == 0 {
		t.Fatal("no pending packs left for resume")
	}
	for _, pk := range pend {
		if _, err := os.Stat(filepath.Join(f.stateDir, "spool", "7", pk.OID+spoolExt)); err != nil {
			t.Fatalf("pending pack %s not spooled: %v", pk.OID, err)
		}
	}

	f.be.setAllow(-1)
	f.reopen()
	res2, err := f.pipeline().Run(context.Background(), f.params("run-2"))
	if err != nil {
		t.Fatal(err)
	}
	if res2.Resumed != len(pend) {
		t.Errorf("resumed %d packs, want %d", res2.Resumed, len(pend))
	}
	f.checkSpoolEmpty()
	f.reopen()
	f.checkRestore(res2.SnapshotOID)
}

func TestPipelineCancel(t *testing.T) {
	f := newPipeFixture(t)
	for i := range 5 {
		f.put("f"+string(rune('a'+i))+".bin", f.random(1100<<10), 100)
	}
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	f.onOpen = func(string) {
		if n++; n == 3 {
			cancel()
		}
	}
	res, err := f.pipeline().Run(ctx, f.params("run-1"))
	if !errors.Is(err, context.Canceled) || res.SnapshotOID != "" {
		t.Fatalf("err = %v, snapshot %q", err, res.SnapshotOID)
	}
	if !f.tok.closed {
		t.Error("profile token not closed")
	}
	pend, _ := f.st.PendingPacks(context.Background(), 7)
	for _, pk := range pend {
		if _, err := os.Stat(filepath.Join(f.stateDir, "spool", "7", pk.OID+spoolExt)); err != nil {
			t.Fatalf("pending pack %s not spooled", pk.OID)
		}
	}

	f.onOpen = nil
	f.reopen()
	res2, err := f.pipeline().Run(context.Background(), f.params("run-2"))
	if err != nil {
		t.Fatal(err)
	}
	f.checkSpoolEmpty()
	f.checkRestore(res2.SnapshotOID)
}

func TestPipelineSpoolBoundFlushes(t *testing.T) {
	f := newPipeFixture(t)
	for i := range 8 {
		f.put("f"+string(rune('a'+i))+".bin", f.random(1100<<10), 100)
	}
	p := f.pipeline()
	p.SpoolMax = 2 << 20 // about one pack; IndexEvery alone would never flush
	p.IndexEvery = 1000
	done := make(chan struct{})
	var res *Result
	var err error
	go func() { res, err = p.Run(context.Background(), f.params("run-1")); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("pipeline deadlocked on a full spool")
	}
	if err != nil {
		t.Fatal(err)
	}
	if n := len(f.repo.Index().Files()); n < 2 {
		t.Errorf("index objects %d; a full spool must force flushes", n)
	}
	f.checkRestore(res.SnapshotOID)
}

func TestPipelineSkipsUnreadableFiles(t *testing.T) {
	f := newPipeFixture(t)
	f.put("ok.txt", []byte("fine"), 100)
	f.put("locked.db", []byte("busy"), 100)
	f.put("denied.txt", []byte("secret"), 100)
	f.openErr[filepath.Join(docs, "locked.db")] = ErrLocked
	f.openErr[filepath.Join(docs, "denied.txt")] = os.ErrPermission
	var seen []Issue
	p := f.pipeline()
	p.OnIssue = func(is Issue) { seen = append(seen, is) }
	res, err := p.Run(context.Background(), f.params("run-1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) != 2 || len(seen) != 2 {
		t.Fatalf("issues %+v", res.Issues)
	}
	byClass := res.Root.IssuesSummary.ByClass
	if res.Root.IssuesSummary.Skipped != 2 || byClass[wire.IssueClassLocked] != 1 || byClass[wire.IssueClassAccessDenied] != 1 {
		t.Errorf("issues summary %+v", res.Root.IssuesSummary)
	}
	got := f.restore(res.SnapshotOID)
	if len(got) != 1 || string(got["{DOCUMENTS}/ok.txt"]) != "fine" {
		t.Errorf("restored %v", keysOf(got))
	}
	if _, ok, _ := f.st.FileCache(context.Background(), 1, filepath.Join(docs, "locked.db")); ok {
		t.Error("skipped file cached")
	}
}

type fakeFolders struct {
	prof guard.Profile
	tok  snapshot.Tokens
}

func (ff *fakeFolders) Tokens(_ context.Context, p guard.Profile) (snapshot.Tokens, error) {
	ff.prof = p
	return ff.tok, nil
}

func TestPipelineResolvesKnownFolders(t *testing.T) {
	f := newPipeFixture(t)
	f.put("x.txt", []byte("x"), 100)
	ff := &fakeFolders{tok: snapshot.Tokens{snapshot.TokenDocuments: docs, snapshot.TokenDesktop: `C:\Users\alice\Desktop`}}
	p := f.pipeline()
	p.Folders = ff
	in := f.params("run-1")
	in.Spec.Tokens = nil
	in.Spec.Profile = guard.Profile{SID: testSID, Root: `C:\Users\alice`}
	res, err := p.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if ff.prof.SID != testSID {
		t.Errorf("resolver got %+v", ff.prof)
	}
	if len(res.Root.Tokens) != 1 || res.Root.Tokens[snapshot.TokenDocuments] != docs {
		t.Errorf("root tokens %v (only used tokens are recorded)", res.Root.Tokens)
	}
}

func TestPipelineSingleFileRoot(t *testing.T) {
	f := newPipeFixture(t)
	f.put("one.txt", []byte("1"), 100)
	f.put("two.txt", []byte("2"), 100)
	f.put("other.txt", []byte("3"), 100)
	in := f.params("run-1")
	in.Spec.Paths = []string{"{DOCUMENTS}/one.txt", "{DOCUMENTS}/two.txt"}
	res, err := f.pipeline().Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Root.Roots) != 1 || res.Root.Roots[0].TokenPath != "{DOCUMENTS}" {
		t.Fatalf("roots %+v", res.Root.Roots)
	}
	got := f.restore(res.SnapshotOID)
	if len(got) != 2 || string(got["{DOCUMENTS}/two.txt"]) != "2" {
		t.Errorf("restored %v", keysOf(got))
	}
}

func TestPipelineValidation(t *testing.T) {
	f := newPipeFixture(t)
	p := f.pipeline()
	in := f.params("run-1")
	in.DeviceKey = nil
	if _, err := p.Run(context.Background(), in); err == nil {
		t.Error("missing device key accepted")
	}
	in = f.params("")
	if _, err := p.Run(context.Background(), in); err == nil {
		t.Error("missing run uuid accepted")
	}
}

func TestProfileTokens(t *testing.T) {
	root := filepath.Join("C:"+string(filepath.Separator), "Users", "bob")
	values := map[string]string{
		"personal":                               `%USERPROFILE%\OneDrive\Documents`,
		"Desktop":                                `\\nas\home\bob\Desktop`,
		"My Pictures":                            `Pictures`, // relative: ignored
		"My Music":                               `%HOMESHARE%\Music`,
		"{374de290-123f-4565-9164-39c4925e467b}": `D:\Downloads`,
		"Favorites":                              `%USERPROFILE%\Favorites`, // not a token
	}
	expand := func(s string) string { return s } // %HOMESHARE% stays unexpanded
	tok := profileTokens(root, values, expand, snapshot.Tokens{snapshot.TokenProgramData: `C:\ProgramData`})
	want := map[string]string{
		snapshot.TokenProfile:     root,
		snapshot.TokenDocuments:   filepath.Clean(root + `\OneDrive\Documents`),
		snapshot.TokenDesktop:     filepath.Clean(`\\nas\home\bob\Desktop`),
		snapshot.TokenPictures:    filepath.Join(root, "Pictures"),
		snapshot.TokenMusic:       filepath.Join(root, "Music"),
		snapshot.TokenDownloads:   filepath.Clean(`D:\Downloads`),
		snapshot.TokenAppData:     filepath.Join(root, `AppData\Roaming`),
		snapshot.TokenProgramData: `C:\ProgramData`,
	}
	for k, v := range want {
		if tok[k] != v {
			t.Errorf("%s = %q, want %q", k, tok[k], v)
		}
	}
	if _, ok := tok["Favorites"]; ok {
		t.Error("non-token value mapped")
	}
}

func TestSpoolReserveBlocksAndCancels(t *testing.T) {
	sp, err := newSpool(t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := sp.reserve(ctx, 8); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() { errc <- sp.reserve(cctx, 8) }()
	select {
	case <-sp.press:
	case <-time.After(5 * time.Second):
		t.Fatal("no pressure signal")
	}
	if !sp.pressure() {
		t.Error("pressure not reported")
	}
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("reserve after cancel: %v", err)
	}
	go func() { errc <- sp.reserve(ctx, 8) }()
	sp.release(8)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	// An oversize pack still fits an empty spool.
	sp.release(8)
	if err := sp.reserve(ctx, 100); err != nil {
		t.Fatal(err)
	}
}

// dirEntries returns every directory entry of a snapshot by token path.
func (f *pipeFixture) dirEntries(root *snapshot.Root) map[string]snapshot.Entry {
	f.t.Helper()
	out := map[string]snapshot.Entry{}
	var walk func(tp, tree string)
	walk = func(tp, tree string) {
		for tree != "" {
			tr, err := f.repo.ReadTree(context.Background(), tree)
			if err != nil {
				f.t.Fatalf("tree %s: %v", tp, err)
			}
			for _, e := range tr.Entries {
				if e.T == snapshot.TypeDir {
					out[tp+"/"+e.N] = e
					walk(tp+"/"+e.N, *e.Sub)
				}
			}
			tree = ""
			if tr.Cont != nil {
				tree = *tr.Cont
			}
		}
	}
	for _, r := range root.Roots {
		walk(r.TokenPath, r.Tree)
	}
	return out
}

func TestPipelineDirectories(t *testing.T) {
	f := newPipeFixture(t)
	f.put("sub/b.txt", []byte("hello"), 100)
	sub, empty, deep := filepath.Join(docs, "sub"), filepath.Join(docs, "empty"), filepath.Join(docs, "empty", "deeper")
	f.fs.nodes[sub] = FileInfo{Name: "sub", MTime: 500, CTime: 400, Attrs: AttrDirectory | AttrReadOnly}
	f.fs.nodes[empty] = FileInfo{Name: "empty", MTime: 600, CTime: 300, Attrs: AttrDirectory | AttrHidden}
	f.fs.nodes[deep] = FileInfo{Name: "deeper", MTime: 650, Attrs: AttrDirectory}
	desktop := filepath.Join("C:"+string(filepath.Separator), "Users", "alice", "Desktop")
	f.fs.dir(desktop) // a root without files
	in := f.params("run-1")
	in.Spec.Paths = []string{"{DOCUMENTS}", "{DESKTOP}"}
	in.Spec.Tokens[snapshot.TokenDesktop] = desktop
	res, err := f.pipeline().Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var roots []string
	for _, r := range res.Root.Roots {
		roots = append(roots, r.TokenPath)
	}
	sort.Strings(roots)
	if strings.Join(roots, ",") != "{DESKTOP},{DOCUMENTS}" || res.Root.Tokens[snapshot.TokenDesktop] != desktop {
		t.Fatalf("roots %v tokens %v", roots, res.Root.Tokens)
	}
	dirs := f.dirEntries(res.Root)
	if len(dirs) != 3 {
		t.Fatalf("dir entries %v", dirs)
	}
	for tp, want := range map[string]snapshot.Entry{
		"{DOCUMENTS}/sub":          {M: 500, C: 400, A: AttrDirectory | AttrReadOnly},
		"{DOCUMENTS}/empty":        {M: 600, C: 300, A: AttrDirectory | AttrHidden},
		"{DOCUMENTS}/empty/deeper": {M: 650, A: AttrDirectory},
	} {
		got := dirs[tp]
		if got.M != want.M || got.C != want.C || got.A != want.A {
			t.Errorf("%s = m %d c %d a %#x, want %+v", tp, got.M, got.C, got.A, want)
		}
	}
	// Two roots and three subdirectories; one file.
	if res.Root.Stats.Dirs != 5 || res.Root.Stats.Files != 1 || res.Stats.FilesTotal != 1 {
		t.Errorf("root stats %+v run stats %+v", res.Root.Stats, res.Stats)
	}
	f.checkRestore(res.SnapshotOID)
}

func TestPipelineDirBadName(t *testing.T) {
	f := newPipeFixture(t)
	f.put("ok.txt", []byte("1"), 100)
	bad := filepath.Join(docs, "a:b")
	f.fs.dir(bad)
	res, err := f.pipeline().Run(context.Background(), f.params("run-1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) != 1 || res.Issues[0].Path != bad || res.Issues[0].Class != wire.IssueClassOther {
		t.Fatalf("issues %+v", res.Issues)
	}
	if len(f.dirEntries(res.Root)) != 0 {
		t.Error("bad directory name stored")
	}
}

func TestPipelineRetiresBrokenSpoolRows(t *testing.T) {
	f := newPipeFixture(t)
	f.put("a.txt", []byte("data"), 100)
	ctx := context.Background()
	missing, corrupt := strings.Repeat("1", 64), strings.Repeat("2", 64)
	for _, oid := range []string{missing, corrupt} {
		if err := f.st.AddPack(ctx, Pack{OID: oid, RepoID: 7, Size: 10, RunUUID: "old"}); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(f.stateDir, "spool", "7")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, corrupt+spoolExt), []byte("not a pack"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := f.pipeline().Run(ctx, f.params("run-1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Resumed != 0 {
		t.Errorf("resumed %d broken packs", res.Resumed)
	}
	for _, oid := range []string{missing, corrupt} {
		if _, err := f.st.Pack(ctx, oid); !errors.Is(err, ErrNotFound) {
			t.Errorf("pack %s row not deleted: %v", oid[:4], err)
		}
	}
	f.checkSpoolEmpty()
	f.checkRestore(res.SnapshotOID)
}
