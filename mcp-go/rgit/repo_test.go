// SPDX-License-Identifier: AGPL-3.0-or-later

package rgit

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func haveGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git to check against")
	}
}

func put(t *testing.T, r *Repo, kind string, b []byte) string {
	t.Helper()
	id, err := r.Put(kind, b)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func commitFiles(t *testing.T, r *Repo, files map[string]string, parent, msg string, when time.Time) string {
	t.Helper()
	var es []Entry
	for p, text := range files {
		es = append(es, Entry{Path: p, ID: put(t, r, "blob", []byte(text))})
	}
	tree, err := r.WriteTree(es)
	if err != nil {
		t.Fatal(err)
	}
	c := Commit{Tree: tree, Author: Ident{"Äijä Örkki", "aija@example.com", when}, Committer: Ident{"Sliqtly server", "server@sliqtly.local", when}, Message: msg}
	if parent != "" {
		c.Parents = []string{parent}
	}
	id, err := r.WriteCommit(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetRef("refs/heads/main", parent, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRepoWriteReadsBackAndGitAgrees(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo.git")
	r, err := Init(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := r.Head(); b != "main" {
		t.Fatalf("head %q", b)
	}
	if got, _ := r.Ref("refs/heads/main"); got != "" {
		t.Fatalf("new repo has main at %s", got)
	}
	when := time.Date(2026, 10, 13, 12, 0, 0, 0, time.FixedZone("", 3*3600))
	c1 := commitFiles(t, r, map[string]string{
		"decks/abc/deck.md":        "# Hyvää päivää ✓\n",
		"decks/abc/media/kuva.png": "\x89PNG\r\n\x1a\nbinary\x00bytes",
		"decks/abc/data/tyhjä.csv": "",
		"rooms/r1.json":            "{}\n",
		"README.md":                "# repo\n",
	}, "", "Tuotu SQLitestä\n", when)
	c2 := commitFiles(t, r, map[string]string{
		"decks/abc/deck.md": "# Hyvää päivää ✓\n\n---\n\n## Toinen dia\n",
		"README.md":         "# repo\n",
	}, c1, "Pakka \"abc\": 1 dia lisätty", when.Add(time.Minute))

	// a stale update is refused
	if err := r.SetRef("refs/heads/main", c1, c1); !errors.Is(err, ErrStale) {
		t.Fatalf("stale SetRef: %v", err)
	}
	log, err := r.Log(c2, 10)
	if err != nil || len(log) != 2 || log[0].ID != c2 || log[1].ID != c1 {
		t.Fatalf("log %v %v", log, err)
	}
	if log[0].Author.Name != "Äijä Örkki" || !log[0].Author.When.Equal(when.Add(time.Minute)) {
		t.Fatalf("author %+v", log[0].Author)
	}
	if _, off := log[0].Author.When.Zone(); off != 3*3600 {
		t.Fatalf("zone %d", off)
	}
	files, err := r.ReadTree(log[1].Tree)
	if err != nil || len(files) != 5 {
		t.Fatalf("tree %v %v", files, err)
	}
	for _, f := range files {
		if f.Path == "decks/abc/data/tyhjä.csv" {
			if kind, b, err := r.Get(f.ID); err != nil || kind != "blob" || len(b) != 0 {
				t.Fatalf("csv %q %q %v", kind, b, err)
			}
		}
	}
	if _, _, err := r.Get(strings.Repeat("0", 40)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing object: %v", err)
	}

	haveGit(t)
	gitCmd(t, dir, "fsck", "--strict", "--no-dangling")
	if got := strings.TrimSpace(gitCmd(t, dir, "rev-parse", "main")); got != c2 {
		t.Fatalf("git main %s, want %s", got, c2)
	}
	if got := gitCmd(t, dir, "log", "--format=%an|%s", "main"); got != "Äijä Örkki|Pakka \"abc\": 1 dia lisätty\nÄijä Örkki|Tuotu SQLitestä\n" {
		t.Fatalf("git log %q", got)
	}
	if got := gitCmd(t, dir, "cat-file", "blob", "main:decks/abc/deck.md"); !strings.Contains(got, "Toinen dia") {
		t.Fatalf("deck.md %q", got)
	}
	clone := filepath.Join(t.TempDir(), "clone")
	gitCmd(t, "", "clone", "-q", dir, clone)
	if b, err := os.ReadFile(filepath.Join(clone, "decks", "abc", "deck.md")); err != nil || !strings.Contains(string(b), "Toinen dia") {
		t.Fatalf("clone %q %v", b, err)
	}

	// git commits, then packs everything: both read back through the packs
	work := filepath.Join(t.TempDir(), "work")
	gitCmd(t, "", "clone", "-q", dir, work)
	os.WriteFile(filepath.Join(work, "README.md"), []byte("# repo, git'n muokkaama\n"), 0o644)
	gitCmd(t, work, "-c", "user.name=G", "-c", "user.email=g@example.com", "commit", "-qam", "from git")
	gitCmd(t, work, "push", "-q", "origin", "main")
	gitCmd(t, dir, "gc", "-q", "--aggressive", "--prune=now")
	r2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tip, err := r2.Ref("refs/heads/main")
	if err != nil || tip == c2 || tip == "" {
		t.Fatalf("tip after gc %s %v", tip, err)
	}
	log, err = r2.Log(tip, 10)
	if err != nil || len(log) != 3 || log[1].ID != c2 {
		t.Fatalf("log through pack %v %v", log, err)
	}
	files, err = r2.ReadTree(log[0].Tree)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path == "README.md" {
			if _, b, err := r2.Get(f.ID); err != nil || string(b) != "# repo, git'n muokkaama\n" {
				t.Fatalf("readme %q %v", b, err)
			}
		}
	}
	// a commit on top of packed-refs
	c4 := commitFiles(t, r2, map[string]string{"README.md": "x\n"}, tip, "after gc", when.Add(time.Hour))
	gitCmd(t, dir, "fsck", "--strict", "--no-dangling")
	if got := strings.TrimSpace(gitCmd(t, dir, "rev-parse", "main")); got != c4 {
		t.Fatalf("main %s want %s", got, c4)
	}
}

func TestEmptyTree(t *testing.T) {
	r, err := Init(filepath.Join(t.TempDir(), "r.git"), "main")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := r.WriteTree(nil)
	if err != nil || tree != "4b825dc642cb6eb9a060e54bf8d69288fbee4904" {
		t.Fatalf("empty tree %s %v", tree, err)
	}
}

func TestBadPathsAndRefs(t *testing.T) {
	r, err := Init(filepath.Join(t.TempDir(), "r.git"), "main")
	if err != nil {
		t.Fatal(err)
	}
	blob := put(t, r, "blob", []byte("x"))
	for _, p := range []string{"", "/a", "a/", "a//b", "../a", "a/./b", ".git/config", "a/.GIT/x"} {
		if _, err := r.WriteTree([]Entry{{Path: p, ID: blob}}); err == nil {
			t.Errorf("path %q taken", p)
		}
	}
	if _, err := r.WriteTree([]Entry{{Path: "a", ID: blob}, {Path: "a", ID: blob}}); err == nil {
		t.Error("same path twice taken")
	}
	for _, n := range []string{"HEAD", "refs/../x", "refs/heads/a b", "refs/heads/x.lock"} {
		if err := r.SetRef(n, "", blob); err == nil {
			t.Errorf("ref %q taken", n)
		}
	}
}

func TestTreeAt(t *testing.T) {
	r, err := Init(filepath.Join(t.TempDir(), "repo.git"), "main")
	if err != nil {
		t.Fatal(err)
	}
	c := commitFiles(t, r, map[string]string{"decks/a/deck.md": "A", "decks/b/deck.md": "B", "README.md": "R"}, "", "m\n", time.Now())
	cm, _ := r.ReadCommit(c)
	sub, err := r.TreeAt(cm.Tree, "decks/a")
	if err != nil || sub == "" {
		t.Fatalf("decks/a: %q %v", sub, err)
	}
	files, _ := r.ReadTree(sub)
	if len(files) != 1 || files[0].Path != "deck.md" {
		t.Fatalf("files %v", files)
	}
	if none, err := r.TreeAt(cm.Tree, "decks/zz"); err != nil || none != "" {
		t.Fatalf("missing: %q %v", none, err)
	}
	if none, _ := r.TreeAt(cm.Tree, "README.md"); none != "" {
		t.Fatal("a file is not a tree")
	}
}
