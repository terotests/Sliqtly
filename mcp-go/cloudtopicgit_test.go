// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// signs u-tero in to GitHub (no repository of the account's own)
func (g *gitTest) signGitHub(t *testing.T) {
	t.Helper()
	res, _ := g.do(t, "GET", "/editor/github/login?mode=link", "tero", "")
	to, _ := url.Parse(res.Header.Get("Location"))
	res, body := g.do(t, "GET", "/editor/github/callback?code=good-code&state="+url.QueryEscape(to.Query().Get("state")), "tero", "")
	if res.StatusCode != 302 {
		t.Fatalf("callback: %d %s", res.StatusCode, body)
	}
}

// a topic call → its JSON
func (g *gitTest) topic(t *testing.T, op string, body map[string]any) map[string]any {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	body["room"] = "r1"
	b, _ := json.Marshal(body)
	path := "/editor/api/git/topic"
	if op != "" {
		path += "/" + op
	}
	res, out := g.do(t, "POST", path, "tero", string(b))
	if res.StatusCode != 200 {
		t.Fatalf("%s: %d %s", op, res.StatusCode, out)
	}
	var m map[string]any
	json.Unmarshal([]byte(out), &m)
	return m
}

func (g *gitTest) topicState(t *testing.T) map[string]any {
	t.Helper()
	res, out := g.do(t, "GET", "/editor/api/git/topic?room=r1", "tero", "")
	if res.StatusCode != 200 {
		t.Fatalf("state: %d %s", res.StatusCode, out)
	}
	var m map[string]any
	json.Unmarshal([]byte(out), &m)
	return m
}

func (g *gitTest) view(t *testing.T) layoutView {
	t.Helper()
	d, _ := g.f.db.Get(g.ctx, layoutCol, "u-tero")
	return viewOf(layoutOf(d))
}

const topicBase = "# Roadmap\n\n## A\n\nOne.\n\n## B\n\nTwo.\n\n## C\n\nThree.\n"

// a GitHub repository with other things in it, a topic "Plans" with one
// presentation, linked to the folder docs/plan
func linkedTopic(t *testing.T) *gitTest {
	g := newGitTest(t)
	g.gh.mu.Lock()
	c := g.gh.makeCommit("start", g.gh.treeOf(map[string]string{
		"README.md":           g.gh.putBlob([]byte("# Our docs\n")),
		"src/main.go":         g.gh.putBlob([]byte("package main\n")),
		"docs/plan/old.md":    g.gh.putBlob([]byte("# Old notes\n\nKept on GitHub.\n")),
		"docs/plan/README.md": g.gh.putBlob([]byte("# About this folder\n")),
	}), nil, "someone")
	g.gh.refs["heads/main"] = c
	g.gh.mu.Unlock()
	g.deck("Deck000001", "u-tero", topicBase, Doc{"css": "h1 { color: red; }"})
	g.deck("Deck000002", "u-tero", "# Elsewhere\n", nil)
	g.f.db.Set(g.ctx, layoutCol, "u-tero", layoutDocOf(map[string]string{"room/r1": `{"id":"r1","title":"Plans"}`, "deck/cloud:Deck000001": "r1"}))
	g.signGitHub(t)
	out := g.topic(t, "link", map[string]any{"installation": 7, "repo": 42, "dir": "/docs/plan/"})
	if out["commit"] == "" || len(out["created"].([]any)) != 1 {
		t.Fatalf("link: %v", out)
	}
	return g
}

func TestTopicSlug(t *testing.T) {
	for in, want := range map[string]string{"Hinnoittelu 2027 – luonnos": "hinnoittelu-2027-luonnos", "Ääkköset ja Öljy": "aakkoset-ja-oljy", "!!!": "presentation", "README": "presentation"} {
		if got := topicSlug(in); got != want {
			t.Errorf("topicSlug(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"/docs/plan/": "docs/plan", "": "", "a//b": "a/b"} {
		if got, ok := topicDir(in); !ok || got != want {
			t.Errorf("topicDir(%q) = %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{"../x", ".git", "a/../../b"} {
		if _, ok := topicDir(bad); ok {
			t.Errorf("topicDir(%q) taken", bad)
		}
	}
}

func TestTopicLinkCommitsAndImports(t *testing.T) {
	g := linkedTopic(t)
	head := g.gh.head()
	files := g.gh.files(head)
	if got := g.gh.text(head, "docs/plan/roadmap.md"); got != topicBase {
		t.Fatalf("roadmap.md %q", got)
	}
	if got := g.gh.text(head, "docs/plan/roadmap/theme.css"); got != "h1 { color: red; }" {
		t.Fatalf("theme.css %q", got)
	}
	var meta map[string]any
	json.Unmarshal([]byte(g.gh.text(head, "docs/plan/roadmap/deck.json")), &meta)
	if meta["id"] != "Deck000001" || meta["name"] != "Roadmap" {
		t.Fatalf("deck.json %v", meta)
	}
	// the rest of the repository as it was; the other topic's deck not in it
	if g.gh.text(head, "src/main.go") != "package main\n" || g.gh.text(head, "README.md") != "# Our docs\n" || g.gh.text(head, "docs/plan/README.md") != "# About this folder\n" {
		t.Fatal("other files changed")
	}
	for p := range files {
		if strings.Contains(p, "elsewhere") {
			t.Fatalf("a presentation of another topic went in: %s", p)
		}
	}
	// old.md is a presentation of the topic now
	st := g.topicState(t)
	decks := st["decks"].([]any)
	if len(decks) != 2 {
		t.Fatalf("decks %v", decks)
	}
	var old string
	for _, d := range decks {
		m := d.(map[string]any)
		if m["slug"] == "old" {
			old = m["deck"].(string)
		}
	}
	if old == "" || g.md(old) != "# Old notes\n\nKept on GitHub.\n" || g.view(t).roomOf(pageDeck(old)) != "r1" {
		t.Fatalf("old.md not taken in: %v", decks)
	}
	// its deck.json goes in with the next commit, its text stays where it was
	g.f.db.Update(g.ctx, "shares", "Deck000001", Doc{"md": strings.Replace(topicBase, "Two.", "Two, edited.", 1)})
	if _, body := g.do(t, "POST", "/editor/api/git/save", "tero", `{"deck":"Deck000001","message":"Before the review"}`); !strings.Contains(body, `"commit"`) {
		t.Fatalf("save: %s", body)
	}
	head = g.gh.head()
	if !strings.Contains(g.gh.text(head, "docs/plan/roadmap.md"), "Two, edited.") || !strings.HasPrefix(g.gh.commits[head].Message, "Before the review") {
		t.Fatalf("save commit %q", g.gh.commits[head].Message)
	}
	if !strings.Contains(g.gh.text(head, "docs/plan/old/deck.json"), old) {
		t.Fatal("old.md got no deck.json")
	}
	// the account's state names the topic
	_, body := g.do(t, "GET", "/editor/api/git", "tero", "")
	if !strings.Contains(body, `"repo":"tero/sliqtly-decks","dir":"docs/plan"`) && !strings.Contains(body, `"dir":"docs/plan"`) {
		t.Fatalf("status: %s", body)
	}
}

func TestTopicTakesInGitHub(t *testing.T) {
	g := linkedTopic(t)
	g.gh.push("edit on github", map[string]string{"docs/plan/roadmap.md": strings.Replace(topicBase, "One.", "One, from GitHub.", 1)})
	g.f.db.Update(g.ctx, "shares", "Deck000001", Doc{"md": strings.Replace(topicBase, "Three.", "Three, from Sliqtly.", 1)})
	g.topic(t, "sync", nil)
	want := strings.Replace(strings.Replace(topicBase, "One.", "One, from GitHub.", 1), "Three.", "Three, from Sliqtly.", 1)
	if got := g.md("Deck000001"); got != want {
		t.Fatalf("merged:\n%s", got)
	}
	if got := g.gh.text(g.gh.head(), "docs/plan/roadmap.md"); got != want {
		t.Fatalf("committed:\n%s", got)
	}
	// a file added on GitHub is a new presentation of the topic
	g.gh.push("new", map[string]string{"docs/plan/budget.md": "# Budget\n"})
	out := g.topic(t, "sync", nil)
	if len(out["created"].([]any)) != 1 {
		t.Fatalf("budget.md: %v", out)
	}
}

// the branch's copy and a new presentation: both into main through a pull request
func TestTopicBranchMerge(t *testing.T) {
	g := linkedTopic(t)
	out := g.topic(t, "branch", map[string]any{"name": "Pricing work"})
	if _, ok := g.gh.refs["heads/sliqtly/pricing-work"]; !ok {
		t.Fatalf("no GitHub branch: %v %v", out, g.gh.refs)
	}
	folder := ""
	for _, f := range g.view(t).folders["r1"] {
		if f.Name == "⎇ Pricing work" {
			folder = f.ID
		}
	}
	if folder == "" {
		t.Fatalf("no folder: %v", g.view(t).folders)
	}
	cp := g.topic(t, "open", map[string]any{"branch": "Pricing work", "deck": "Deck000001"})["deck"].(string)
	if cp == "" || g.md(cp) != topicBase || g.view(t).folderOf(pageDeck(cp)) != folder {
		t.Fatalf("copy %q", cp)
	}
	// opened again: the same copy
	if again := g.topic(t, "open", map[string]any{"branch": "Pricing work", "deck": "Deck000001"})["deck"]; again != cp {
		t.Fatalf("a second copy %v", again)
	}
	// work in the branch: the copy, and a presentation made in its folder
	g.f.db.Update(g.ctx, "shares", cp, Doc{"md": strings.Replace(topicBase, "Three.", "Three, in the branch.", 1)})
	g.deck("Deck000003", "u-tero", "# Prices\n\nNew.\n", nil)
	ld, _ := g.f.db.Get(g.ctx, layoutCol, "u-tero")
	lay := layoutOf(ld)
	lay.set("deck/cloud:Deck000003", "r1", 2)
	lay.set("filed/cloud:Deck000003", folder, 2)
	g.f.db.Set(g.ctx, layoutCol, "u-tero", lay.doc("r2"))
	// main changes meanwhile, on other lines
	g.f.db.Update(g.ctx, "shares", "Deck000001", Doc{"md": strings.Replace(topicBase, "One.", "One, on main.", 1)})
	if _, body := g.do(t, "POST", "/editor/api/git/sync", "tero", `{"deck":"`+cp+`"}`); !strings.Contains(body, `"commit"`) {
		t.Fatalf("branch sync: %s", body)
	}
	bh := g.gh.refs["heads/sliqtly/pricing-work"]
	if !strings.Contains(g.gh.text(bh, "docs/plan/roadmap.md"), "Three, in the branch.") || g.gh.text(bh, "docs/plan/prices.md") != "# Prices\n\nNew.\n" {
		t.Fatalf("branch files %v", g.gh.files(bh))
	}
	if strings.Contains(g.gh.text(g.gh.head(), "docs/plan/roadmap.md"), "in the branch") {
		t.Fatal("the branch's text went to main")
	}
	if d, _ := g.f.db.Get(g.ctx, "shares", "Deck000003"); d["branch"] == nil {
		t.Fatal("the new presentation is not the branch's")
	}

	out = g.topic(t, "merge", map[string]any{"branch": "Pricing work"})
	if out["merged"] != true || out["pr"] == nil {
		t.Fatalf("merge: %v", out)
	}
	want := strings.Replace(strings.Replace(topicBase, "One.", "One, on main.", 1), "Three.", "Three, in the branch.", 1)
	if got := g.md("Deck000001"); got != want {
		t.Fatalf("main after the merge:\n%s", got)
	}
	if got := g.gh.text(g.gh.head(), "docs/plan/roadmap.md"); got != want {
		t.Fatalf("GitHub main:\n%s", got)
	}
	if d, _ := g.f.db.Get(g.ctx, "shares", cp); d != nil {
		t.Fatal("the copy stayed")
	}
	if d, _ := g.f.db.Get(g.ctx, "shares", "Deck000003"); d == nil || d["branch"] != nil {
		t.Fatalf("the new presentation is not main's: %v", d)
	}
	v := g.view(t)
	if v.roomOf(pageDeck("Deck000003")) != "r1" || v.folderOf(pageDeck("Deck000003")) != "" || len(v.folders["r1"]) != 0 {
		t.Fatalf("layout after the merge: %v %v", v.filed, v.folders)
	}
	if _, ok := g.gh.refs["heads/sliqtly/pricing-work"]; ok {
		t.Fatal("the GitHub branch stayed")
	}
	if !g.gh.pulls[1].merged {
		t.Fatal("the pull request was not merged")
	}
	// nothing left to commit, and main's next sync changes nothing
	if out := g.topic(t, "sync", nil); out["commit"] != "" {
		t.Fatalf("a commit after the merge: %v", out)
	}
	st := g.topicState(t)
	if b := st["branches"].([]any)[0].(map[string]any); b["state"] != "merged" {
		t.Fatalf("branch %v", b)
	}
}

func TestTopicBranchConflict(t *testing.T) {
	g := linkedTopic(t)
	g.topic(t, "branch", map[string]any{"name": "fix"})
	cp := g.topic(t, "open", map[string]any{"branch": "fix", "deck": "Deck000001"})["deck"].(string)
	g.f.db.Update(g.ctx, "shares", cp, Doc{"md": strings.Replace(topicBase, "Two.", "Two, branch.", 1)})
	g.f.db.Update(g.ctx, "shares", "Deck000001", Doc{"md": strings.Replace(topicBase, "Two.", "Two, main.", 1)})
	out := g.topic(t, "merge", map[string]any{"branch": "fix"})
	if out["merged"] == true || len(out["conflicts"].([]any)) != 1 {
		t.Fatalf("merged over a conflict: %v", out)
	}
	_, body := g.do(t, "GET", "/editor/api/git/topic/conflict?room=r1&branch=fix&deck="+cp, "tero", "")
	var ch struct{ Chunks []topicChunk }
	json.Unmarshal([]byte(body), &ch)
	clash := 0
	for _, c := range ch.Chunks {
		if c.Clash {
			clash++
			if c.Main != "Two, main." || c.Branch != "Two, branch." {
				t.Fatalf("clash %+v", c)
			}
		}
	}
	if clash != 1 {
		t.Fatalf("chunks %s", body)
	}
	// marks left in: refused
	res, _ := g.do(t, "POST", "/editor/api/git/topic/resolve", "tero", `{"room":"r1","branch":"fix","deck":"`+cp+`","take":"text","md":"<<<<<<< mine\nx\n"}`)
	if res.StatusCode == 200 {
		t.Fatal("a text with conflict marks was taken")
	}
	both := strings.Replace(topicBase, "Two.", "Two, main.\nTwo, branch.", 1)
	g.topic(t, "resolve", map[string]any{"branch": "fix", "deck": cp, "take": "text", "md": both})
	out = g.topic(t, "merge", map[string]any{"branch": "fix"})
	if out["merged"] != true {
		t.Fatalf("merge after resolving: %v", out)
	}
	if got := g.md("Deck000001"); got != both {
		t.Fatalf("main:\n%s", got)
	}
}

func TestTopicMergeWithoutPullRights(t *testing.T) {
	g := linkedTopic(t)
	g.gh.noPR = true
	g.topic(t, "branch", map[string]any{"name": "quick"})
	cp := g.topic(t, "open", map[string]any{"branch": "quick", "deck": "Deck000001"})["deck"].(string)
	g.f.db.Update(g.ctx, "shares", cp, Doc{"md": strings.Replace(topicBase, "One.", "One, quick.", 1)})
	g.f.db.Update(g.ctx, "shares", "Deck000001", Doc{"md": strings.Replace(topicBase, "Three.", "Three, main.", 1)})
	out := g.topic(t, "merge", map[string]any{"branch": "quick"})
	if out["merged"] != true || out["pr"] != nil {
		t.Fatalf("merge: %v", out)
	}
	want := strings.Replace(strings.Replace(topicBase, "One.", "One, quick.", 1), "Three.", "Three, main.", 1)
	if got := g.md("Deck000001"); got != want {
		t.Fatalf("main:\n%s", got)
	}
}

func TestTopicMergeWaitsForReview(t *testing.T) {
	g := linkedTopic(t)
	g.gh.needsReview = true
	g.topic(t, "branch", map[string]any{"name": "reviewed"})
	cp := g.topic(t, "open", map[string]any{"branch": "reviewed", "deck": "Deck000001"})["deck"].(string)
	g.f.db.Update(g.ctx, "shares", cp, Doc{"md": strings.Replace(topicBase, "B", "Beta", 1)})
	out := g.topic(t, "merge", map[string]any{"branch": "reviewed"})
	if out["merged"] == true || out["prUrl"] == nil {
		t.Fatalf("merged without the review: %v", out)
	}
	st := g.topicState(t)
	if b := st["branches"].([]any)[0].(map[string]any); b["state"] != "review" {
		t.Fatalf("branch %v", b)
	}
	// reviewed and merged on GitHub; the push to main finishes it here
	g.gh.mu.Lock()
	pr := g.gh.pulls[1]
	head := g.gh.refs["heads/"+pr.head]
	c := g.gh.makeCommit("Merge pull request", g.gh.commits[head].Tree.SHA, []string{g.gh.refs["heads/main"], head}, "GitHub")
	g.gh.refs["heads/main"] = c
	pr.merged = true
	g.gh.mu.Unlock()
	g.c.topicPushed(g.ctx, 42, "refs/heads/main", c)
	if got := g.md("Deck000001"); got != strings.Replace(topicBase, "B", "Beta", 1) {
		t.Fatalf("main after GitHub's merge:\n%s", got)
	}
	if d, _ := g.f.db.Get(g.ctx, "shares", cp); d != nil {
		t.Fatal("the copy stayed")
	}
}

func TestTopicDropBranch(t *testing.T) {
	g := linkedTopic(t)
	g.topic(t, "branch", map[string]any{"name": "idea"})
	cp := g.topic(t, "open", map[string]any{"branch": "idea", "deck": "Deck000001"})["deck"].(string)
	g.f.db.Update(g.ctx, "shares", cp, Doc{"md": "# Thrown away\n"})
	g.topic(t, "drop", map[string]any{"branch": "idea"})
	if d, _ := g.f.db.Get(g.ctx, "shares", cp); d != nil {
		t.Fatal("the copy stayed")
	}
	if g.md("Deck000001") != topicBase {
		t.Fatal("main changed")
	}
	if _, ok := g.gh.refs["heads/sliqtly/idea"]; !ok {
		t.Fatal("the GitHub branch went (it stays for its history)")
	}
	// a branch of the same name can be made again
	g.topic(t, "branch", map[string]any{"name": "idea"})
	if _, ok := g.gh.refs["heads/sliqtly/idea-2"]; !ok {
		t.Fatalf("refs %v", g.gh.refs)
	}
}

func TestTopicOnlyOwnTopic(t *testing.T) {
	g := newGitTest(t)
	g.signGitHub(t)
	res, body := g.do(t, "POST", "/editor/api/git/topic/link", "tero", `{"room":"general","installation":7,"repo":42}`)
	if res.StatusCode == 200 {
		t.Fatalf("General linked: %s", body)
	}
	g.f.db.Set(g.ctx, layoutCol, "u-tero", layoutDocOf(map[string]string{"room/r1": `{"id":"r1","title":"Plans"}`}))
	res, body = g.do(t, "POST", "/editor/api/git/topic/link", "tero", `{"room":"r1","installation":7,"repo":99}`)
	if res.StatusCode == 200 {
		t.Fatalf("a repository the App may not write: %s", body)
	}
	res, _ = g.do(t, "POST", "/editor/api/git/topic/link", "anna", `{"room":"r1","installation":7,"repo":42}`)
	if res.StatusCode == 200 {
		t.Fatal("Anna linked Tero's topic")
	}
}
