// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/terotests/sliqtly/mcp-go/rgit"
)

func gitTool(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git to check against")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func testGitConfig() gitConfig {
	c := gitConfig{Enabled: true}
	c.defaults()
	return c
}

// a file of the repository's head → its bytes
func headFile(t *testing.T, m *gitMirror, p string) (string, bool) {
	t.Helper()
	head, err := m.Head()
	if err != nil || head == "" {
		t.Fatalf("head %q %v", head, err)
	}
	c, err := m.repo.ReadCommit(head)
	if err != nil {
		t.Fatal(err)
	}
	files, err := m.repo.ReadTree(c.Tree)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path == p {
			_, b, err := m.repo.Get(f.ID)
			if err != nil {
				t.Fatal(err)
			}
			return string(b), true
		}
	}
	return "", false
}

// The folder moves to Git: the first commit holds the decks, their files
// and rooms and nothing personal; later commits name what changed and who
// edited it, and git itself reads the repository.
func TestGitMirrorCommitsTheFolder(t *testing.T) {
	ctx := context.Background()
	data := filepath.Join(t.TempDir(), "data")
	ls, s, stop := startLocalServer(t, data)
	defer stop()

	c := call(t, s, "create_presentation", map[string]any{
		"title": "Pilotti", "markdown": DECK,
		"images": []any{map[string]any{"name": "dot.png", "data_base64": base64.StdEncoding.EncodeToString(PNG)}},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)

	m, err := openGitMirror(data, testGitConfig(), ls.bucket.docs, ls.bucket.blobs)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.start(ctx); err != nil {
		t.Fatal(err)
	}
	first, _ := m.Head()
	if md, ok := headFile(t, m, "decks/"+id+"/deck.md"); !ok || md != DECK {
		t.Fatalf("deck.md %q %v", md, ok)
	}
	meta, ok := headFile(t, m, "decks/"+id+"/deck.json")
	if !ok {
		t.Fatal("no deck.json")
	}
	var dj map[string]any
	if err := json.Unmarshal([]byte(meta), &dj); err != nil {
		t.Fatal(err)
	}
	if dj["name"] != "Pilotti" || dj["room"] == nil || dj["owner"] != nil {
		t.Fatalf("deck.json %s", meta)
	}
	if png, ok := headFile(t, m, "decks/"+id+"/media/dot.png"); !ok || png != string(PNG) {
		t.Fatalf("dot.png %v", ok)
	}
	if readme, _ := headFile(t, m, "README.md"); !strings.Contains(readme, "[Pilotti](decks/"+id+"/deck.md)") {
		t.Fatalf("README %q", readme)
	}
	cs, _ := m.repo.Log(first, 5)
	if len(cs) != 1 || cs[0].Message != "Import from SQLite\n" || cs[0].Author.Name != "Sliqtly server" {
		t.Fatalf("first commit %+v", cs)
	}

	// nothing changed: no commit
	if again, err := m.commit(ctx, ""); err != nil || again != "" {
		t.Fatalf("empty commit %q %v", again, err)
	}

	// an edit by two people in a room
	if err := ls.env.DB.Update(ctx, "shares", id, Doc{"md": DECK + "\n---\n\n# Uusi dia\n"}); err != nil {
		t.Fatal(err)
	}
	m.noteEditor(id, "Äijä Örkki")
	m.noteEditor(id, "Bea")
	m.noteEditor(id, "Äijä Örkki")
	second, err := m.commit(ctx, "")
	if err != nil || second == "" {
		t.Fatalf("second commit %q %v", second, err)
	}
	cm, _ := m.repo.ReadCommit(second)
	if cm.Author.Name != "Äijä Örkki" || cm.Committer.Name != "Sliqtly server" || len(cm.Parents) != 1 || cm.Parents[0] != first {
		t.Fatalf("commit %+v", cm)
	}
	if want := "Deck \"Pilotti\": text changed\n\nCo-authored-by: Bea <server@sliqtly.invalid>\n"; cm.Message != want {
		t.Fatalf("message %q, want %q", cm.Message, want)
	}
	// the names were for that commit only
	if err := ls.bucket.Save(ctx, "shares/"+id+"/data/luvut.csv", "text/csv", []byte("a,b\n1,2\n"), nil); err != nil {
		t.Fatal(err)
	}
	third, err := m.commit(ctx, "")
	if err != nil || third == "" {
		t.Fatalf("third commit %q %v", third, err)
	}
	cm, _ = m.repo.ReadCommit(third)
	if cm.Author.Name != "Sliqtly server" || cm.Message != "Deck \"Pilotti\": 1 file added\n" {
		t.Fatalf("third %+v", cm)
	}
	if diff, err := m.verify(ctx); err != nil || len(diff) != 0 {
		t.Fatalf("verify %v %v", diff, err)
	}

	repo := filepath.Join(data, gitRepoDir)
	gitTool(t, repo, "fsck", "--strict", "--no-dangling")
	if got := gitTool(t, repo, "log", "--format=%an|%s", "main"); got != "Sliqtly server|Deck \"Pilotti\": 1 file added\nÄijä Örkki|Deck \"Pilotti\": text changed\nSliqtly server|Import from SQLite\n" {
		t.Fatalf("git log %q", got)
	}
	clone := filepath.Join(t.TempDir(), "clone")
	gitTool(t, "", "clone", "-q", repo, clone)
	if b, err := os.ReadFile(filepath.Join(clone, "decks", id, "data", "luvut.csv")); err != nil || string(b) != "a,b\n1,2\n" {
		t.Fatalf("clone csv %q %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(clone, "decks", id, "deck.md")); !strings.Contains(string(b), "Uusi dia") {
		t.Fatalf("clone deck.md %q", b)
	}
}

// In Git mode a deck's or a room's file over the limit is refused, by Save
// and SaveFrom alike; other files are not the repository's.
func TestGitRefusesTooBigFiles(t *testing.T) {
	ctx := context.Background()
	ls, _, stop := startLocalServer(t, filepath.Join(t.TempDir(), "data"))
	defer stop()
	b := ls.bucket
	b.gitMax = 1 << 10
	big := bytes.Repeat([]byte("x"), 2<<10)
	var api *apiError
	if err := b.Save(ctx, "shares/abc/media/big.bin", "", big, nil); !errors.As(err, &api) || api.status != 413 {
		t.Fatalf("deck file: %v", err)
	}
	if _, err := b.SaveFrom(ctx, "rooms/r1/files/big.bin", "", bytes.NewReader(big)); !errors.As(err, &api) || api.status != 413 {
		t.Fatalf("room file: %v", err)
	}
	if _, err := b.Read(ctx, "rooms/r1/files/big.bin", 1<<20); err == nil {
		t.Fatal("refused file kept")
	}
	if err := b.Save(ctx, "shares/abc/media/small.bin", "", big[:1<<10], nil); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SaveFrom(ctx, "shared/f1/source.fig", "", bytes.NewReader(big)); err != nil {
		t.Fatal(err)
	}
	b.gitMax = 0
	if err := b.Save(ctx, "shares/abc/media/big.bin", "", big, nil); err != nil {
		t.Fatal(err)
	}
}

// The server commits on its own: after the decks have been still for the
// idle time, and before it stops.
func TestGitMirrorRunCommitsWhenStill(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	data := filepath.Join(t.TempDir(), "data")
	ls, s, stop := startLocalServer(t, data)
	defer stop()
	c := call(t, s, "create_presentation", map[string]any{"title": "Ajastin", "markdown": DECK})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	cfg := testGitConfig()
	cfg.IdleSeconds = 1
	m, err := openGitMirror(data, cfg, ls.bucket.docs, ls.bucket.blobs)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.start(ctx); err != nil {
		t.Fatal(err)
	}
	first, _ := m.Head()
	done := make(chan struct{})
	go func() {
		m.run(ctx, ls.env.Store)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond) // the feed is watched
	if err := ls.env.DB.Update(ctx, "shares", id, Doc{"md": "# Muutettu\n"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		head, _ := m.Head()
		if head != first {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no commit after the idle time")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if md, _ := headFile(t, m, "decks/"+id+"/deck.md"); md != "# Muutettu\n" {
		t.Fatalf("deck.md %q", md)
	}
	cancel()
	<-done
	// what came after is committed when the server stops
	if err := ls.env.DB.Update(context.Background(), "shares", id, Doc{"md": "# Viimeinen\n"}); err != nil {
		t.Fatal(err)
	}
	m.stop()
	if md, _ := headFile(t, m, "decks/"+id+"/deck.md"); md != "# Viimeinen\n" {
		t.Fatalf("deck.md after stop %q", md)
	}
}

func runGitCmd(t *testing.T, want int, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := gitCmd(args, &out, &errOut); code != want {
		t.Fatalf("git %v: exit %d, want %d\n%s%s", args, code, want, out.String(), errOut.String())
	}
	return out.String() + errOut.String()
}

// sliqtly-server git init / log / verify / commit / off on a stopped folder
func TestGitCommand(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	ls, s, stop := startLocalServer(t, data)
	c := call(t, s, "create_presentation", map[string]any{"title": "Komento", "markdown": DECK,
		"images": []any{map[string]any{"name": "dot.png", "data_base64": base64.StdEncoding.EncodeToString(PNG)}}})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	stop()
	_ = ls

	runGitCmd(t, 1, "verify", "-data", data)
	// a limit the picture is over: init refuses and names it
	if out := runGitCmd(t, 1, "init", "-data", data, "-max-file-mb", "101"); !strings.Contains(out, "over 100") {
		t.Fatalf("init over the ceiling: %s", out)
	}
	out := runGitCmd(t, 0, "init", "-data", data, "-author", "server")
	if !strings.Contains(out, "read back and checked") || !strings.Contains(out, "1 decks") {
		t.Fatalf("init: %s", out)
	}
	cfg, on, err := readGitConfig(data)
	if err != nil || !on || !cfg.Enabled || cfg.Author != "server" || cfg.MaxFileMB != 20 {
		t.Fatalf("git.json %+v %v %v", cfg, on, err)
	}
	if out := runGitCmd(t, 0, "log", "-data", data); !strings.Contains(out, "Import from SQLite") {
		t.Fatalf("log: %s", out)
	}
	if out := runGitCmd(t, 0, "verify", "-data", data); !strings.Contains(out, "ok") {
		t.Fatalf("verify: %s", out)
	}
	if out := runGitCmd(t, 0, "commit", "-data", data); !strings.Contains(out, "nothing changed") {
		t.Fatalf("commit: %s", out)
	}

	// a server on the folder: Git on, an edit, committed when it stops
	e, bucket, err := localEnv(data, "http://localhost", "local")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.DB.Update(context.Background(), "shares", id, Doc{"md": "# Komennon jälkeen\n"}); err != nil {
		t.Fatal(err)
	}
	bucket.blobs.Close()
	bucket.docs.Close()
	if out := runGitCmd(t, 1, "verify", "-data", data); !strings.Contains(out, "differs: decks/"+id+"/deck.md") {
		t.Fatalf("verify after edit: %s", out)
	}
	if out := runGitCmd(t, 0, "commit", "-data", data, "-m", "Oma viesti"); !strings.Contains(out, "committed") {
		t.Fatalf("commit: %s", out)
	}
	if out := runGitCmd(t, 0, "log", "-data", data); !strings.Contains(strings.SplitN(out, "\n", 2)[0], "Oma viesti") {
		t.Fatalf("log: %s", out)
	}
	runGitCmd(t, 0, "off", "-data", data)
	if cfg, _, _ := readGitConfig(data); cfg.Enabled {
		t.Fatal("still on")
	}
	if _, err := rgit.Open(filepath.Join(data, gitRepoDir)); err != nil {
		t.Fatalf("repository gone: %v", err)
	}
}

func TestGitChangesDescribe(t *testing.T) {
	e := func(p, id string) rgit.Entry { return rgit.Entry{Path: p, ID: id} }
	before := []rgit.Entry{e("README.md", "1"), e("decks/a/deck.md", "2"), e("decks/a/media/x.png", "3"), e("decks/b/deck.md", "4"), e("rooms/r.json", "5")}
	after := []rgit.Entry{e("README.md", "9"), e("decks/a/deck.md", "2"), e("decks/a/media/x.png", "8"), e("decks/a/media/y.png", "7"), e("decks/c/deck.md", "6"), e("rooms/r.json", "5"), e("links/l.json", "6")}
	s := &gitSnapshot{Decks: map[string]string{"a": "Alfa", "c": "Gamma"}}
	got := gitChanges(before, after).describe(s)
	want := "3 decks changed\n\nDeck \"Alfa\": 1 file added, 1 file changed\nDeck \"b\": removed\nDeck \"Gamma\": added\nRooms and links: 1 file changed"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestGitConfigChecks(t *testing.T) {
	dir := t.TempDir()
	if c, on, err := readGitConfig(dir); err != nil || on || c.MaxFileMB != 20 || c.Author != "editor" || c.Branch != "main" {
		t.Fatalf("no file: %+v %v %v", c, on, err)
	}
	for body, ok := range map[string]bool{
		`{"enabled": true}`:                   true,
		`{"enabled": true, "maxFileMB": 100}`: true,
		`{"enabled": true, "maxFileMB": 101}`: false,
		`{"author": "someone"}`:               false,
		`{"branch": "a b"}`:                   false,
		`{`:                                   false,
	} {
		os.WriteFile(filepath.Join(dir, gitConfigFile), []byte(body), 0o600)
		if _, _, err := readGitConfig(dir); (err == nil) != ok {
			t.Errorf("%s: %v", body, err)
		}
	}
}
