// SPDX-License-Identifier: AGPL-3.0-or-later

// Package rgit is Ranger's Git client (terotests/Ranger pkg/src: objects,
// trees, commits, packs, push) compiled to Go by gen.mjs (rgit.go,
// generated), and a bare repository on disk kept with it (this file): the
// objects are written and read in the client's formats, the host only
// moves files.
//
// A repository is a plain bare Git repository: `git clone`, `git log` and
// `git fsck` read it. Objects are written loose (objects/xx/…); packs a
// `git gc` made are read too.
package rgit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Repo is a bare repository in Dir.
type Repo struct {
	Dir string

	mu    sync.Mutex // packs
	packs []*packFile
	seen  map[string]bool // pack names loaded
}

type packFile struct {
	data []byte
	idx  *GitIdxFile
}

// ErrNotFound: no object of that id, or no such ref.
var ErrNotFound = errors.New("rgit: not found")

// ErrStale: a ref was not where the update expected it.
var ErrStale = errors.New("rgit: ref moved")

// Init makes a bare repository in dir whose HEAD names branch; one there
// already is opened as it is.
func Init(dir, branch string) (*Repo, error) {
	if r, err := Open(dir); err == nil {
		return r, nil
	}
	for _, d := range GitRepoLayout_static_bareDirs() {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(d)), 0o750); err != nil {
			return nil, err
		}
	}
	// HEAD last: a folder with HEAD is a repository
	files := GitRepoLayout_static_bareFiles(branch)
	sort.SliceStable(files, func(i, j int) bool { return files[j].path == "HEAD" })
	for _, f := range files {
		if err := writeAtomic(filepath.Join(dir, f.path), []byte(f.text), 0o644); err != nil {
			return nil, err
		}
	}
	return Open(dir)
}

// Open opens the bare repository in dir.
func Open(dir string) (*Repo, error) {
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		return nil, fmt.Errorf("rgit: %s is not a repository: %w", dir, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "objects")); err != nil {
		return nil, fmt.Errorf("rgit: %s is not a repository: %w", dir, err)
	}
	return &Repo{Dir: dir, seen: map[string]bool{}}, nil
}

// ID is the object id the bytes have as an object of kind (blob, tree,
// commit), written or not.
func ID(kind string, payload []byte) string {
	return GitObjects_static_id(kind, payload)
}

func (r *Repo) loosePath(sha string) string {
	return filepath.Join(r.Dir, filepath.FromSlash(GitObjects_static_loosePath(sha)))
}

// Has: the object is in the repository (loose or in a pack).
func (r *Repo) Has(sha string) bool {
	if !validID(sha) {
		return false
	}
	if _, err := os.Stat(r.loosePath(sha)); err == nil {
		return true
	}
	return r.packed(sha) != nil
}

// Put writes the object (once: the same bytes again write nothing) → its id
func (r *Repo) Put(kind string, payload []byte) (string, error) {
	sha := ID(kind, payload)
	if r.Has(sha) {
		return sha, nil
	}
	p := r.loosePath(sha)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return "", err
	}
	if err := writeAtomic(p, GitObjects_static_loose(kind, payload), 0o444); err != nil {
		return "", err
	}
	return sha, nil
}

// Get reads an object → its kind and payload
func (r *Repo) Get(sha string) (string, []byte, error) {
	if !validID(sha) {
		return "", nil, fmt.Errorf("rgit: bad object id %q", sha)
	}
	if b, err := os.ReadFile(r.loosePath(sha)); err == nil {
		raw := GitObjects_static_readLoose(b)
		if !raw.ok {
			return "", nil, fmt.Errorf("rgit: object %s: %s", sha, raw.err)
		}
		return raw.kind, raw.payload, checkID(sha, raw)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	if p := r.packed(sha); p != nil {
		raw := GitPackReader_static_find(p.data, p.idx, sha)
		if !raw.ok {
			return "", nil, fmt.Errorf("rgit: object %s: %s", sha, raw.err)
		}
		return raw.kind, raw.payload, checkID(sha, raw)
	}
	return "", nil, fmt.Errorf("%w: object %s", ErrNotFound, sha)
}

func checkID(sha string, raw *GitRaw) error {
	if got := ID(raw.kind, raw.payload); got != sha {
		return fmt.Errorf("rgit: object %s reads back as %s", sha, got)
	}
	return nil
}

// the pack holding sha, nil when none does; packs are loaded when first
// needed, and again when a new one appears
func (r *Repo) packed(sha string) *packFile {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.packs {
		if p.idx.offsetOf(sha) >= 0 {
			return p
		}
	}
	dir := filepath.Join(r.Dir, "objects", "pack")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var found *packFile
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".idx") || r.seen[name] {
			continue
		}
		ib, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		pb, err := os.ReadFile(filepath.Join(dir, strings.TrimSuffix(name, ".idx")+".pack"))
		if err != nil {
			continue
		}
		idx := GitIdxIO_static_parse(ib)
		if !idx.ok {
			continue
		}
		r.seen[name] = true
		p := &packFile{data: pb, idx: idx}
		r.packs = append(r.packs, p)
		if found == nil && idx.offsetOf(sha) >= 0 {
			found = p
		}
	}
	return found
}

// --- refs

// Head is the branch HEAD names (refs/heads/main → "main").
func (r *Repo) Head() (string, error) {
	b, err := os.ReadFile(filepath.Join(r.Dir, "HEAD"))
	if err != nil {
		return "", err
	}
	t := GitRepoLayout_static_symbolicTarget(string(b))
	if !strings.HasPrefix(t, "refs/heads/") {
		return "", fmt.Errorf("rgit: HEAD is not on a branch")
	}
	return strings.TrimPrefix(t, "refs/heads/"), nil
}

// Ref is the commit a ref (refs/heads/main) names; "" when there is none
func (r *Repo) Ref(name string) (string, error) {
	if err := checkRefName(name); err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(r.Dir, filepath.FromSlash(name)))
	if err == nil {
		sha := GitRepoLayout_static_refFileSha(string(b))
		if sha == "" {
			return "", fmt.Errorf("rgit: %s holds no object id", name)
		}
		return sha, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	pb, err := os.ReadFile(filepath.Join(r.Dir, "packed-refs"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, ref := range GitRepoLayout_static_parsePacked(string(pb)) {
		if ref.name == name {
			return ref.sha, nil
		}
	}
	return "", nil
}

// SetRef points name at sha when it still names old ("" = no such ref
// yet); ErrStale when it has moved. The update is atomic: a lock file
// beside the ref, renamed over it.
func (r *Repo) SetRef(name, old, sha string) error {
	if err := checkRefName(name); err != nil {
		return err
	}
	if !validID(sha) {
		return fmt.Errorf("rgit: bad object id %q", sha)
	}
	p := filepath.Join(r.Dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	lock := p + ".lock"
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("rgit: %s is being updated: %w", name, err)
	}
	done := false
	defer func() {
		if !done {
			f.Close()
			os.Remove(lock)
		}
	}()
	cur, err := r.Ref(name)
	if err != nil {
		return err
	}
	if cur != old {
		return fmt.Errorf("%w: %s is at %s, not %s", ErrStale, name, short(cur), short(old))
	}
	if _, err := f.WriteString(GitRepoLayout_static_refFileText(sha)); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	done = true
	if err := os.Rename(lock, p); err != nil {
		os.Remove(lock)
		return err
	}
	return nil
}

func checkRefName(name string) error {
	if !strings.HasPrefix(name, "refs/") || strings.Contains(name, "..") || strings.HasSuffix(name, ".lock") || strings.ContainsAny(name, " ~^:?*[\\\x00") {
		return fmt.Errorf("rgit: bad ref name %q", name)
	}
	return nil
}

// --- trees

// Mode of an entry: a file (100644), an executable (100755) or a tree (40000).
const (
	ModeFile = "100644"
	ModeExec = "100755"
	ModeTree = "40000"
)

// Entry is a file of a tree, by its path from the root (a/b/c.md).
type Entry struct {
	Path string
	Mode string
	ID   string
}

// WriteTree writes the trees holding files (their blobs written already)
// → the root tree's id. Paths are unique and use "/".
func (r *Repo) WriteTree(files []Entry) (string, error) {
	items := make([]*GitTreeItem, 0, len(files))
	have := make(map[string]bool, len(files))
	for _, f := range files {
		if err := checkPath(f.Path); err != nil {
			return "", err
		}
		if have[f.Path] {
			return "", fmt.Errorf("rgit: %s twice in one tree", f.Path)
		}
		have[f.Path] = true
		if !validID(f.ID) {
			return "", fmt.Errorf("rgit: %s: bad object id %q", f.Path, f.ID)
		}
		mode := f.Mode
		if mode == "" {
			mode = ModeFile
		}
		items = append(items, &GitTreeItem{name: f.Path, mode: mode, sha: f.ID})
	}
	ts := GitTrees_static_build(items)
	for i, p := range ts.payloads {
		got, err := r.Put("tree", p)
		if err != nil {
			return "", err
		}
		if got != ts.shas[i] {
			return "", fmt.Errorf("rgit: tree %s written as %s", ts.shas[i], got)
		}
	}
	if len(ts.payloads) == 0 {
		// no files: the empty tree
		return r.Put("tree", nil)
	}
	return ts.root, nil
}

// ReadTree lists the files under a tree, depth first, with their paths
// from it.
func (r *Repo) ReadTree(tree string) ([]Entry, error) {
	var out []Entry
	var walk func(sha, prefix string, depth int) error
	walk = func(sha, prefix string, depth int) error {
		if depth > 64 {
			return fmt.Errorf("rgit: tree %s nested too deep", sha)
		}
		kind, payload, err := r.Get(sha)
		if err != nil {
			return err
		}
		if kind != "tree" {
			return fmt.Errorf("rgit: %s is a %s, not a tree", sha, kind)
		}
		for _, it := range GitObjects_static_parseTree(payload) {
			p := prefix + it.name
			if it.isTree() {
				if err := walk(it.sha, p+"/", depth+1); err != nil {
					return err
				}
				continue
			}
			out = append(out, Entry{Path: p, Mode: it.mode, ID: it.sha})
		}
		return nil
	}
	return out, walk(tree, "", 0)
}

func checkPath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") || strings.Contains(p, "\x00") {
		return fmt.Errorf("rgit: bad path %q", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return fmt.Errorf("rgit: bad path %q", p)
		}
	}
	return nil
}

// --- commits

// Ident is an author or committer.
type Ident struct {
	Name  string
	Email string
	When  time.Time
}

// Commit is a commit object.
type Commit struct {
	ID        string // when read
	Tree      string
	Parents   []string
	Author    Ident
	Committer Ident
	Message   string
}

func (id Ident) git() *GitIdent {
	g := CreateNew_GitIdent()
	g.name, g.email = id.Name, id.Email
	g.time = id.When.Unix()
	_, off := id.When.Zone()
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	g.tz = fmt.Sprintf("%s%02d%02d", sign, off/3600, off/60%60)
	return g
}

func identOf(g *GitIdent) Ident {
	when := time.Unix(g.time, 0).UTC()
	if len(g.tz) == 5 {
		var h, m int
		if _, err := fmt.Sscanf(g.tz[1:], "%02d%02d", &h, &m); err == nil {
			off := h*3600 + m*60
			if g.tz[0] == '-' {
				off = -off
			}
			when = when.In(time.FixedZone("", off))
		}
	}
	return Ident{Name: g.name, Email: g.email, When: when}
}

// WriteCommit writes c (its tree and parents written already) → its id
func (r *Repo) WriteCommit(c Commit) (string, error) {
	g := CreateNew_GitCommit()
	if !validID(c.Tree) {
		return "", fmt.Errorf("rgit: commit with bad tree %q", c.Tree)
	}
	g.tree = c.Tree
	for _, p := range c.Parents {
		if !validID(p) {
			return "", fmt.Errorf("rgit: commit with bad parent %q", p)
		}
	}
	g.parents = append([]string(nil), c.Parents...)
	g.author, g.committer = c.Author.git(), c.Committer.git()
	g.message = c.Message
	return r.Put("commit", GitObjects_static_commitPayload(g))
}

// ReadCommit reads a commit.
func (r *Repo) ReadCommit(sha string) (Commit, error) {
	kind, payload, err := r.Get(sha)
	if err != nil {
		return Commit{}, err
	}
	if kind != "commit" {
		return Commit{}, fmt.Errorf("rgit: %s is a %s, not a commit", sha, kind)
	}
	g := GitObjects_static_parseCommit(payload)
	if !g.ok {
		return Commit{}, fmt.Errorf("rgit: commit %s: %s", sha, g.err)
	}
	return Commit{ID: sha, Tree: g.tree, Parents: g.parents, Author: identOf(g.author), Committer: identOf(g.committer), Message: g.message}, nil
}

// Log is up to n commits from tip back along first parents, newest first.
func (r *Repo) Log(tip string, n int) ([]Commit, error) {
	var out []Commit
	for tip != "" && len(out) < n {
		c, err := r.ReadCommit(tip)
		if err != nil {
			return out, err
		}
		out = append(out, c)
		tip = ""
		if len(c.Parents) > 0 {
			tip = c.Parents[0]
		}
	}
	return out, nil
}

// --- files

// writes b to p by a file beside it renamed over p, synced first: a reader
// sees the old bytes or the new ones, never a part (objects are read only,
// as git keeps them)
func writeAtomic(p string, b []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(p), ".tmp-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(b); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	ok = true
	return nil
}

func validID(sha string) bool {
	if len(sha) != 40 {
		return false
	}
	for i := 0; i < 40; i++ {
		c := sha[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func short(sha string) string {
	if sha == "" {
		return "(none)"
	}
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
