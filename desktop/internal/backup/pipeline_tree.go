package backup

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/pack"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/repo"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/vdcrypt"
)

// errBadName is a file name a tree blob cannot hold (backup-format §8.2).
var errBadName = errors.New("backup: file name cannot be stored in a tree")

// dirNode is one directory of the snapshot being built.
type dirNode struct {
	meta  *snapshot.Entry // the directory's own m, c, a (nil = not walked: defaults)
	files []snapshot.Entry
	dirs  map[string]*dirNode
	names map[string]bool // file names
}

func newDirNode() *dirNode { return &dirNode{dirs: map[string]*dirNode{}, names: map[string]bool{}} }

// treeBuilder collects file entries under the job's roots and encodes the
// tree blobs bottom-up. A directory root's tree is the directory itself; a
// single-file root is recorded as a tree holding just that file under the
// file's parent token path (roots sharing a parent share one tree).
type treeBuilder struct {
	spec  []string            // job roots (token paths, no trailing "/")
	roots map[string]*dirNode // root token path → tree
	order []string
	dirs  int64
}

func newTreeBuilder(paths []string) *treeBuilder {
	b := &treeBuilder{roots: map[string]*dirNode{}}
	for _, p := range paths {
		if p = strings.TrimSuffix(p, "/"); p != "" {
			b.spec = append(b.spec, p)
		}
	}
	// Shortest first: the walker merges nested roots into the outer one,
	// so an entry belongs to the shortest root that prefixes it.
	slices.SortStableFunc(b.spec, func(a, c string) int { return len(a) - len(c) })
	return b
}

func validTreeName(n string) bool {
	return n != "" && n != "." && n != ".." && utf8.ValidString(n) && !strings.ContainsAny(n, "/\\:\x00")
}

// locate maps a token path to its root key and the components below it.
// A directory at a root's own path is that root (rel is empty); a file
// there is a single-file root under its parent.
func (b *treeBuilder) locate(tokenPath string, dir bool) (string, []string, error) {
	var rootKey string
	var rel []string
	found := false
	for _, p := range b.spec {
		switch {
		case tokenPath == p && dir:
			rootKey, rel, found = p, nil, true
		case tokenPath == p:
			tok, comps, err := snapshot.SplitTokenPath(p)
			if err != nil || len(comps) == 0 {
				return "", nil, fmt.Errorf("backup: file root %q has no parent", p)
			}
			if rootKey, err = snapshot.JoinTokenPath(tok, comps[:len(comps)-1]...); err != nil {
				return "", nil, err
			}
			rel, found = comps[len(comps)-1:], true
		case strings.HasPrefix(tokenPath, p+"/"):
			rootKey, rel, found = p, strings.Split(tokenPath[len(p)+1:], "/"), true
		}
		if found {
			break
		}
	}
	if !found {
		return "", nil, fmt.Errorf("backup: %q is outside the job's roots", tokenPath)
	}
	for _, n := range rel {
		if !validTreeName(n) {
			return "", nil, fmt.Errorf("%w: %q", errBadName, n)
		}
	}
	return rootKey, rel, nil
}

// node returns the directory node at rel below rootKey, creating it and its
// parents.
func (b *treeBuilder) node(tokenPath, rootKey string, rel []string) (*dirNode, error) {
	node := b.roots[rootKey]
	if node == nil {
		node = newDirNode()
		b.roots[rootKey] = node
		b.order = append(b.order, rootKey)
	}
	for _, n := range rel {
		c := node.dirs[n]
		if c == nil && node.names[n] {
			return nil, fmt.Errorf("backup: duplicate entry %q", tokenPath)
		}
		if c == nil {
			c = newDirNode()
			node.dirs[n] = c
		}
		node = c
	}
	return node, nil
}

// addDir records a directory (e.N, e.T and e.Sub are ignored) at its token
// path, so it is kept when empty and carries its own metadata. A directory
// root's metadata is not stored (a root has no parent entry), but the root
// is kept even without files.
func (b *treeBuilder) addDir(tokenPath string, e snapshot.Entry) error {
	rootKey, rel, err := b.locate(tokenPath, true)
	if err != nil {
		return err
	}
	node, err := b.node(tokenPath, rootKey, rel)
	if err != nil {
		return err
	}
	e.A |= AttrDirectory
	node.meta = &e
	return nil
}

// add places a file entry (e.N is ignored) at its token path.
func (b *treeBuilder) add(tokenPath string, e snapshot.Entry) error {
	rootKey, rel, err := b.locate(tokenPath, false)
	if err != nil {
		return err
	}
	node, err := b.node(tokenPath, rootKey, rel[:len(rel)-1])
	if err != nil {
		return err
	}
	e.N = rel[len(rel)-1]
	if _, clash := node.dirs[e.N]; clash || node.names[e.N] {
		return fmt.Errorf("backup: duplicate entry %q", tokenPath)
	}
	node.names[e.N] = true
	node.files = append(node.files, e)
	return nil
}

// encode returns the root refs and every tree blob (heads and
// continuations), children before parents. kid is K_id.
func (b *treeBuilder) encode(kid vdcrypt.Key) ([]snapshot.RootRef, []snapshot.TreeBlob, error) {
	var blobs []snapshot.TreeBlob
	var enc func(n *dirNode) (string, error)
	enc = func(n *dirNode) (string, error) {
		entries := append([]snapshot.Entry(nil), n.files...)
		names := make([]string, 0, len(n.dirs))
		for name := range n.dirs {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			sub, err := enc(n.dirs[name])
			if err != nil {
				return "", err
			}
			b.dirs++
			de := snapshot.Entry{A: AttrDirectory}
			if m := n.dirs[name].meta; m != nil {
				de = snapshot.Entry{M: m.M, C: m.C, A: m.A}
			}
			de.N, de.T, de.Sub = name, snapshot.TypeDir, snapshot.HexPtr(sub)
			entries = append(entries, de)
		}
		tb, err := snapshot.EncodeTrees(kid, entries)
		if err != nil {
			return "", err
		}
		blobs = append(blobs, tb...)
		return tb[0].IDHex(), nil
	}
	refs := make([]snapshot.RootRef, 0, len(b.order))
	for _, key := range b.order {
		id, err := enc(b.roots[key])
		if err != nil {
			return nil, nil, err
		}
		b.dirs++
		refs = append(refs, snapshot.RootRef{TokenPath: key, Tree: id})
	}
	return refs, blobs, nil
}

// usedTokens keeps the token values the roots refer to (§6: the root
// records the concrete value of every token used).
func usedTokens(all snapshot.Tokens, refs []snapshot.RootRef) snapshot.Tokens {
	out := snapshot.Tokens{}
	for _, r := range refs {
		tok, _, err := snapshot.SplitTokenPath(r.TokenPath)
		if err != nil {
			continue
		}
		if v, ok := all[tok]; ok {
			out[tok] = v
		}
	}
	return out
}

// blobLookup is the index lookup referencedPacks needs (*repo.Index).
type blobLookup interface {
	Lookup(id pack.BlobID) (repo.Location, bool)
}

// referencedPacks returns, sorted, every pack holding one of the
// snapshot's tree blobs, file and stream chunks or inventory blob, from
// the in-memory trees (no tree is read back). It returns nil when a blob
// is not in the index, so the caller falls back to snapshotPacks.
func (b *treeBuilder) referencedPacks(ix blobLookup, blobs []snapshot.TreeBlob, inventory *string) []string {
	packs := map[string]bool{}
	add := func(id pack.BlobID) bool {
		loc, ok := ix.Lookup(id)
		if ok {
			packs[loc.Pack] = true
		}
		return ok
	}
	addHex := func(idHex string) bool {
		id, err := blobID(idHex)
		return err == nil && add(id)
	}
	for _, tb := range blobs {
		if !add(pack.BlobID(tb.ID)) {
			return nil
		}
	}
	if inventory != nil && !addHex(*inventory) {
		return nil
	}
	var walk func(n *dirNode) bool
	walk = func(n *dirNode) bool {
		for i := range n.files {
			e := &n.files[i]
			for _, c := range e.Ch {
				if !addHex(c) {
					return false
				}
			}
			if e.X != nil {
				for _, a := range e.X.ADS {
					for _, c := range a.Ch {
						if !addHex(c) {
							return false
						}
					}
				}
			}
		}
		for _, d := range n.dirs {
			if !walk(d) {
				return false
			}
		}
		return true
	}
	for _, key := range b.order {
		if !walk(b.roots[key]) {
			return nil
		}
	}
	out := make([]string, 0, len(packs))
	for p := range packs {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}
