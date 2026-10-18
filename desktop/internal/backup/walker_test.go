package backup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/tokens"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

const testSID = "S-1-5-21-1-2-3-1001"

// fakeToken counts impersonations and flags FS calls made outside one.
type fakeToken struct {
	src     tokens.Source
	inside  bool
	imps    int
	closed  bool
	impErr  error
	onEnter func()
}

func (t *fakeToken) SID() string                                  { return testSID }
func (t *fakeToken) Source() tokens.Source                        { return t.src }
func (t *fakeToken) SessionID() (uint32, bool)                    { return 1, t.src == tokens.SourceSession }
func (t *fakeToken) Caps() tokens.Caps                            { return tokens.CapsFor(t.src) }
func (t *fakeToken) Close() error                                 { t.closed = true; return nil }
func (t *fakeToken) AccessCheckPath(string, uint32) (bool, error) { return true, nil }
func (t *fakeToken) Impersonate(fn func() error) error {
	if t.impErr != nil {
		return t.impErr
	}
	t.imps++
	t.inside = true
	defer func() { t.inside = false }()
	if t.onEnter != nil {
		t.onEnter()
	}
	return fn()
}

type fakeProvider struct {
	tok *fakeToken
	err error
	sid string
}

func (p *fakeProvider) ForProfile(sid string) (tokens.Token, error) {
	p.sid = sid
	if p.err != nil {
		return nil, p.err
	}
	return p.tok, nil
}
func (p *fakeProvider) SessionOnly(sid string) (tokens.Token, error) { return p.ForProfile(sid) }

// fakeFS is an in-memory tree keyed by filepath.Join paths.
type fakeFS struct {
	t       *testing.T
	tok     *fakeToken
	nodes   map[string]FileInfo
	listErr map[string]error
	probErr map[string]error
	probed  []string
}

func newFakeFS(t *testing.T, tok *fakeToken) *fakeFS {
	return &fakeFS{t: t, tok: tok, nodes: map[string]FileInfo{}, listErr: map[string]error{}, probErr: map[string]error{}}
}

func (f *fakeFS) dir(p string) { f.nodes[p] = FileInfo{Name: filepath.Base(p), Attrs: AttrDirectory} }
func (f *fakeFS) file(p string, size int64, attrs uint32) {
	f.nodes[p] = FileInfo{Name: filepath.Base(p), Size: size, MTime: 100, CTime: 50, Attrs: attrs | AttrArchive}
}

func (f *fakeFS) mustImpersonate(op string) {
	if !f.tok.inside {
		f.t.Errorf("%s outside impersonation", op)
	}
}

func (f *fakeFS) Stat(p string) (FileInfo, error) {
	f.mustImpersonate("Stat")
	fi, ok := f.nodes[p]
	if !ok {
		return FileInfo{}, &fs.PathError{Op: "stat", Path: p, Err: fs.ErrNotExist}
	}
	return fi, nil
}

func (f *fakeFS) ReadDir(d string) ([]FileInfo, error) {
	f.mustImpersonate("ReadDir")
	if err := f.listErr[d]; err != nil {
		return nil, err
	}
	var out []FileInfo
	for p, fi := range f.nodes {
		if filepath.Dir(p) == d && p != d {
			out = append(out, fi)
		}
	}
	slices.Reverse(out) // the walker must sort
	return out, nil
}

func (f *fakeFS) Probe(p string) (string, error) {
	f.mustImpersonate("Probe")
	f.probed = append(f.probed, p)
	if err := f.probErr[p]; err != nil {
		return "", err
	}
	return "id:" + filepath.Base(p), nil
}

type walkResult struct {
	entries []Entry
	issues  []Issue
	stats   WalkStats
	err     error
}

func (r walkResult) paths() []string {
	var out []string
	for _, e := range r.entries {
		out = append(out, e.TokenPath)
	}
	return out
}

func (r walkResult) issue(path string) *Issue {
	for i := range r.issues {
		if r.issues[i].Path == path {
			return &r.issues[i]
		}
	}
	return nil
}

var docs = filepath.Join("C:"+string(filepath.Separator), "Users", "alice", "Documents")

func testSpec() WalkSpec {
	return WalkSpec{ProfileSID: testSID, Paths: []string{"{DOCUMENTS}"}, Epoch: 1,
		Tokens: snapshot.Tokens{snapshot.TokenDocuments: docs}}
}

func runWalk(ctx context.Context, w *Walker, spec WalkSpec) walkResult {
	var r walkResult
	r.stats, r.err = w.Walk(ctx, spec, func(e Entry) error { r.entries = append(r.entries, e); return nil },
		func(is Issue) { r.issues = append(r.issues, is) })
	return r
}

// standardTree builds Documents with a mix of entries.
func standardTree(t *testing.T, tok *fakeToken) *fakeFS {
	f := newFakeFS(t, tok)
	j := func(p ...string) string { return filepath.Join(append([]string{docs}, p...)...) }
	f.dir(docs)
	f.file(j("a.txt"), 10, 0)
	f.file(j("b.tmp"), 5, 0)
	f.dir(j("sub"))
	f.file(j("sub", "c.doc"), 20, 0)
	f.dir(j("node_modules"))
	f.file(j("node_modules", "x.js"), 1, 0)
	f.file(j("cloud.docx"), 99, AttrRecallOnDataAccess|AttrReparsePoint)
	f.nodes[j("cloud.docx")] = FileInfo{Name: "cloud.docx", Size: 99, Attrs: AttrRecallOnDataAccess | AttrReparsePoint, ReparseTag: 0x9000101A}
	f.nodes[j("link")] = FileInfo{Name: "link", Attrs: AttrDirectory | AttrReparsePoint, ReparseTag: reparseTagSymlink}
	f.file(j("secret.efs"), 7, AttrEncrypted)
	f.file(j("locked.db"), 3, 0)
	f.probErr[j("locked.db")] = fmt.Errorf("%w: busy", ErrLocked)
	f.file(j("gone.txt"), 3, 0)
	f.probErr[j("gone.txt")] = &fs.PathError{Op: "open", Path: j("gone.txt"), Err: fs.ErrNotExist}
	f.file(j("denied.txt"), 3, 0)
	f.probErr[j("denied.txt")] = &fs.PathError{Op: "open", Path: j("denied.txt"), Err: fs.ErrPermission}
	f.dir(j("blocked"))
	f.file(j("blocked", "z.txt"), 1, 0)
	return f
}

func TestWalkS4U(t *testing.T) {
	tok := &fakeToken{src: tokens.SourceS4U}
	f := standardTree(t, tok)
	j := func(p ...string) string { return filepath.Join(append([]string{docs}, p...)...) }
	var guardCalls []string
	w := &Walker{
		Tokens: &fakeProvider{tok: tok},
		FS:     f,
		Guard: func(op guard.Op, p string, prof guard.Profile) error {
			if tok.inside {
				t.Errorf("guard called inside impersonation for %s", p)
			}
			if op != guard.OpRead || prof.SID != testSID {
				t.Errorf("guard op %v profile %+v", op, prof)
			}
			guardCalls = append(guardCalls, p)
			if p == j("blocked") {
				return &guard.BlockedError{Op: op, Path: p, Reason: "test block", Err: guard.ErrProtected}
			}
			return nil
		},
	}
	spec := testSpec()
	spec.Profile.SID = testSID
	spec.Exclude = []string{"*.TMP", "node_modules/", " "}
	r := runWalk(context.Background(), w, spec)
	if r.err != nil {
		t.Fatal(r.err)
	}
	want := []string{"{DOCUMENTS}/a.txt", "{DOCUMENTS}/sub/c.doc"}
	if !slices.Equal(r.paths(), want) {
		t.Fatalf("entries = %v, want %v", r.paths(), want)
	}
	if r.entries[0].FileID != "id:a.txt" || r.entries[0].Path != j("a.txt") || r.entries[0].Unchanged {
		t.Fatalf("entry = %+v", r.entries[0])
	}
	if !tok.closed {
		t.Fatal("token not closed")
	}
	for path, class := range map[string]string{
		j("cloud.docx"): wire.IssueClassCloudPlaceholder,
		j("secret.efs"): IssueClassEFSNeedsLogon,
		j("locked.db"):  wire.IssueClassLocked,
		j("denied.txt"): wire.IssueClassAccessDenied,
		j("blocked"):    wire.IssueClassGuardBlocked,
	} {
		is := r.issue(path)
		if is == nil || is.Class != class {
			t.Errorf("issue for %s = %+v, want class %s", path, is, class)
		}
	}
	if is := r.issue(j("blocked")); is != nil && !strings.Contains(is.Detail, "test block") {
		t.Errorf("guard detail = %q", is.Detail)
	}
	if r.issue(j("gone.txt")) != nil || r.issue(j("link")) != nil {
		t.Error("vanished file or link reported")
	}
	if slices.Contains(f.probed, j("cloud.docx")) || slices.Contains(f.probed, j("secret.efs")) {
		t.Errorf("placeholder or EFS file opened: %v", f.probed)
	}
	st := r.stats
	if st.Files != 2 || st.Bytes != 30 || st.Excluded != 2 || st.Reparse != 1 || st.Skipped != 5 || !st.Complete ||
		st.Source != tokens.SourceS4U || st.Dirs != 2 {
		t.Fatalf("stats = %+v", st)
	}
	if !slices.Contains(guardCalls, j("a.txt")) || !slices.Contains(guardCalls, docs) {
		t.Fatalf("guard calls = %v", guardCalls)
	}
	// One impersonation for the root Stat, one per listing, one per probe batch.
	if tok.imps != 5 {
		t.Fatalf("impersonations = %d", tok.imps)
	}
}

func TestWalkSessionEFSAndCache(t *testing.T) {
	tok := &fakeToken{src: tokens.SourceSession}
	f := newFakeFS(t, tok)
	j := func(p ...string) string { return filepath.Join(append([]string{docs}, p...)...) }
	f.dir(docs)
	f.file(j("same.txt"), 10, 0)
	f.file(j("changed.txt"), 10, 0)
	f.file(j("oldepoch.txt"), 10, 0)
	f.file(j("nohash.txt"), 10, 0)
	f.file(j("secret.efs"), 7, AttrEncrypted)
	f.file(j("efsfail.txt"), 7, AttrEncrypted)
	f.probErr[j("efsfail.txt")] = fmt.Errorf("%w: x", ErrEFS)
	cache := map[string]CachedFile{
		j("same.txt"):     {Size: 10, MTime: 100, CTime: 50, Epoch: 1, H: []byte{1}, FileID: "cached-id"},
		j("changed.txt"):  {Size: 11, MTime: 100, CTime: 50, Epoch: 1, H: []byte{1}},
		j("oldepoch.txt"): {Size: 10, MTime: 100, CTime: 50, Epoch: 0, H: []byte{1}},
		j("nohash.txt"):   {Size: 10, MTime: 100, CTime: 50, Epoch: 1},
	}
	w := &Walker{Tokens: &fakeProvider{tok: tok}, FS: f, Guard: func(guard.Op, string, guard.Profile) error { return nil },
		Cache: func(_ context.Context, p string) (CachedFile, bool, error) { c, ok := cache[p]; return c, ok, nil }}
	r := runWalk(context.Background(), w, testSpec())
	if r.err != nil {
		t.Fatal(r.err)
	}
	var unchanged []string
	for _, e := range r.entries {
		if e.Unchanged {
			unchanged = append(unchanged, filepath.Base(e.Path))
			if e.Cached == nil || e.FileID != "cached-id" {
				t.Errorf("unchanged entry = %+v", e)
			}
		}
	}
	if !slices.Equal(unchanged, []string{"same.txt"}) || r.stats.Unchanged != 1 {
		t.Fatalf("unchanged = %v (%d)", unchanged, r.stats.Unchanged)
	}
	if slices.Contains(f.probed, j("same.txt")) {
		t.Fatal("unchanged file probed")
	}
	// A session token reads EFS files; a decrypt failure is efs_needs_logon.
	if !slices.Contains(r.paths(), "{DOCUMENTS}/secret.efs") {
		t.Fatalf("EFS file skipped under a session token: %v", r.paths())
	}
	if is := r.issue(j("efsfail.txt")); is == nil || is.Class != IssueClassEFSNeedsLogon || !is.Retryable {
		t.Fatalf("efs failure issue = %+v", is)
	}
	cacheErr := errors.New("db gone")
	w.Cache = func(context.Context, string) (CachedFile, bool, error) { return CachedFile{}, false, cacheErr }
	if r := runWalk(context.Background(), w, testSpec()); !errors.Is(r.err, cacheErr) {
		t.Fatalf("cache error = %v", r.err)
	}
}

func TestWalkTokenErrors(t *testing.T) {
	for _, tc := range []struct {
		err       error
		needLogon bool
	}{
		{tokens.ErrNeedsLogon, true},
		{tokens.ErrS4UUnavailable, true},
		{tokens.ErrInsufficientPrivilege, false},
	} {
		w := &Walker{Tokens: &fakeProvider{err: tc.err}, FS: newFakeFS(t, &fakeToken{})}
		r := runWalk(context.Background(), w, testSpec())
		if !errors.Is(r.err, tc.err) || errors.Is(r.err, ErrProfileNeedsLogon) != tc.needLogon {
			t.Errorf("%v: err = %v", tc.err, r.err)
		}
		if tc.needLogon && (len(r.issues) != 1 || r.issues[0].Class != IssueClassProfileNeedsLogon) {
			t.Errorf("%v: issues = %+v", tc.err, r.issues)
		}
		if r.stats.Complete {
			t.Errorf("%v: walk marked complete", tc.err)
		}
	}
}

func TestWalkRoots(t *testing.T) {
	tok := &fakeToken{src: tokens.SourceSession}
	f := newFakeFS(t, tok)
	f.dir(docs)
	f.file(filepath.Join(docs, "a.txt"), 1, 0)
	f.dir(filepath.Join(docs, "inner"))
	f.file(filepath.Join(docs, "inner", "b.txt"), 1, 0)
	w := &Walker{Tokens: &fakeProvider{tok: tok}, FS: f, Guard: func(guard.Op, string, guard.Profile) error { return nil }}
	spec := testSpec()
	// Nested roots (dropped), an unmapped token and a missing folder.
	spec.Paths = []string{"{DOCUMENTS}/inner", "{DOCUMENTS}", "{DOCUMENTS}/a.txt", "{PICTURES}", "{DESKTOP}"}
	spec.Tokens[snapshot.TokenDesktop] = filepath.Join(docs, "..", "Desktop")
	r := runWalk(context.Background(), w, spec)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if want := []string{"{DOCUMENTS}/a.txt", "{DOCUMENTS}/inner/b.txt"}; !slices.Equal(r.paths(), want) {
		t.Fatalf("entries = %v, want %v", r.paths(), want)
	}
	if r.stats.Complete || r.issue("{PICTURES}") == nil || r.issue(filepath.Join(docs, "..", "Desktop")) == nil {
		t.Fatalf("issues = %+v complete=%v", r.issues, r.stats.Complete)
	}
	// A single-file root.
	spec = testSpec()
	spec.Paths = []string{"{DOCUMENTS}/a.txt"}
	if r := runWalk(context.Background(), w, spec); r.err != nil || !slices.Equal(r.paths(), []string{"{DOCUMENTS}/a.txt"}) || !r.stats.Complete {
		t.Fatalf("file root = %v %+v %v", r.paths(), r.stats, r.err)
	}
	// A root that is a link is not followed; a listing error makes the walk incomplete.
	f.nodes[docs] = FileInfo{Name: "Documents", Attrs: AttrDirectory | AttrReparsePoint, ReparseTag: 0xA0000003}
	r = runWalk(context.Background(), w, testSpec())
	if len(r.entries) != 0 || r.stats.Reparse != 1 || r.stats.Complete {
		t.Fatalf("junction root: %+v %+v", r.paths(), r.stats)
	}
	f.dir(docs)
	f.listErr[filepath.Join(docs, "inner")] = &fs.PathError{Op: "readdir", Err: fs.ErrPermission}
	r = runWalk(context.Background(), w, testSpec())
	if r.stats.Complete || r.issue(filepath.Join(docs, "inner")) == nil {
		t.Fatalf("listing error: %+v %+v", r.issues, r.stats)
	}
}

func TestWalkCancelAndEmitError(t *testing.T) {
	tok := &fakeToken{src: tokens.SourceSession}
	f := newFakeFS(t, tok)
	f.dir(docs)
	for i := range 5 {
		f.file(filepath.Join(docs, fmt.Sprintf("f%d", i)), 1, 0)
		f.dir(filepath.Join(docs, fmt.Sprintf("d%d", i)))
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &Walker{Tokens: &fakeProvider{tok: tok}, FS: f, Guard: func(guard.Op, string, guard.Profile) error { return nil }}
	n := 0
	_, err := w.Walk(ctx, testSpec(), func(Entry) error {
		if n++; n == 2 {
			cancel()
		}
		return nil
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err = %v", err)
	}
	stop := errors.New("stop")
	_, err = w.Walk(context.Background(), testSpec(), func(Entry) error { return stop }, nil)
	if !errors.Is(err, stop) {
		t.Fatalf("emit err = %v", err)
	}
	tok.impErr = errors.New("no impersonation")
	if _, err := w.Walk(context.Background(), testSpec(), func(Entry) error { return nil }, nil); !errors.Is(err, tok.impErr) {
		t.Fatalf("impersonate err = %v", err)
	}
}

func TestAllowedReparse(t *testing.T) {
	for tag, want := range map[uint32]bool{
		0x9000001A: true, 0x9000F01A: true, reparseTagDedup: true, reparseTagWOF: true, reparseTagOneDrive: true,
		reparseTagSymlink: false, 0xA0000003: false, 0x80000014: false,
	} {
		if got := allowedReparse(tag); got != want {
			t.Errorf("allowedReparse(%#x) = %v", tag, got)
		}
	}
}

func TestSpecFor(t *testing.T) {
	j := &Job{Job: wj(1, 1, "a")}
	j.Spec.Exclude = []string{"*.tmp"}
	s := SpecFor(j, snapshot.Tokens{}, guard.Profile{Root: `C:\Users\a`}, 3)
	if s.ProfileSID != j.ProfileSID || s.Profile.SID != j.ProfileSID || s.Epoch != 3 || s.Exclude[0] != "*.tmp" {
		t.Fatalf("spec = %+v", s)
	}
}

// TestOSFS checks the real FS on a temp tree (no impersonation needed).
func TestOSFS(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	var o OSFS
	list, err := o.ReadDir(dir)
	if err != nil || len(list) != 2 {
		t.Fatalf("ReadDir = %+v, %v", list, err)
	}
	slices.SortFunc(list, func(a, b FileInfo) int { return strings.Compare(a.Name, b.Name) })
	if list[0].Name != "a.txt" || list[0].Size != 5 || list[0].IsDir() || list[0].MTime == 0 || !list[1].IsDir() {
		t.Fatalf("list = %+v", list)
	}
	st, err := o.Stat(filepath.Join(dir, "a.txt"))
	if err != nil || st.Size != 5 || st.Name != "a.txt" {
		t.Fatalf("Stat = %+v, %v", st, err)
	}
	if st, err := o.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("Stat dir = %+v, %v", st, err)
	}
	id, err := o.Probe(filepath.Join(dir, "a.txt"))
	if err != nil || id == "" {
		t.Fatalf("Probe = %q, %v", id, err)
	}
	id2, _ := o.Probe(filepath.Join(dir, "a.txt"))
	if id != id2 {
		t.Fatalf("file id not stable: %q %q", id, id2)
	}
	if _, err := o.Stat(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat missing = %v", err)
	}
	if _, err := o.Probe(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Probe missing = %v", err)
	}
	if _, err := o.ReadDir(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadDir missing = %v", err)
	}
}

func TestWalkEmitDirs(t *testing.T) {
	tok := &fakeToken{src: tokens.SourceS4U}
	f := standardTree(t, tok)
	j := func(p ...string) string { return filepath.Join(append([]string{docs}, p...)...) }
	f.nodes[docs] = FileInfo{Name: "Documents", MTime: 11, CTime: 10, Attrs: AttrDirectory | AttrReadOnly}
	f.nodes[j("empty")] = FileInfo{Name: "empty", MTime: 777, CTime: 700, Attrs: AttrDirectory | AttrHidden}
	f.dir(j("broken"))
	f.listErr[j("broken")] = &fs.PathError{Op: "readdir", Path: j("broken"), Err: fs.ErrPermission}
	f.nodes[j("cloudfolder")] = FileInfo{Name: "cloudfolder", Attrs: AttrDirectory | AttrRecallOnOpen}
	w := &Walker{Tokens: &fakeProvider{tok: tok}, FS: f, EmitDirs: true,
		Guard: func(op guard.Op, p string, _ guard.Profile) error {
			if p == j("blocked") {
				return &guard.BlockedError{Op: op, Path: p, Reason: "test", Err: guard.ErrProtected}
			}
			return nil
		}}
	spec := testSpec()
	spec.Exclude = []string{"*.tmp", "node_modules/"}
	r := runWalk(context.Background(), w, spec)
	if r.err != nil {
		t.Fatal(r.err)
	}
	// Depth-first pre-order: a directory, its files, then its subdirectories.
	want := []string{"{DOCUMENTS}", "{DOCUMENTS}/a.txt", "{DOCUMENTS}/empty", "{DOCUMENTS}/sub", "{DOCUMENTS}/sub/c.doc"}
	if !slices.Equal(r.paths(), want) {
		t.Fatalf("entries = %v, want %v", r.paths(), want)
	}
	root, empty := r.entries[0], r.entries[2]
	if !root.IsDir() || root.Path != docs || root.MTime != 11 || root.Attrs&AttrReadOnly == 0 {
		t.Errorf("root entry = %+v", root)
	}
	if !empty.IsDir() || empty.Path != j("empty") || empty.MTime != 777 || empty.CTime != 700 ||
		empty.Attrs != AttrDirectory|AttrHidden || empty.Unchanged || empty.Cached != nil {
		t.Errorf("empty dir entry = %+v", empty)
	}
	if slices.Contains(f.probed, j("empty")) || slices.Contains(f.probed, docs) {
		t.Errorf("directory probed: %v", f.probed)
	}
	if st := r.stats; st.Files != 2 || st.Complete {
		t.Errorf("stats = %+v (dirs are not files; an unlisted dir makes the walk incomplete)", st)
	}

	// Without EmitDirs only files are emitted.
	w.EmitDirs = false
	if r := runWalk(context.Background(), w, spec); !slices.Equal(r.paths(), []string{"{DOCUMENTS}/a.txt", "{DOCUMENTS}/sub/c.doc"}) {
		t.Fatalf("entries without EmitDirs = %v", r.paths())
	}

	// An emit error on a directory aborts the walk.
	w.EmitDirs = true
	boom := errors.New("boom")
	_, err := w.Walk(context.Background(), spec, func(e Entry) error {
		if e.IsDir() {
			return boom
		}
		return nil
	}, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("emit error = %v", err)
	}
}
