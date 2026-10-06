// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// The server's GitHub token goes only to read_github_pr: a chart pointed at
// api.github.com is fetched as anyone, or a deck could draw what the token
// reaches (a private repository's file) into render_slide.
func TestGitHubTokenOnlyForPullRequests(t *testing.T) {
	var mu sync.Mutex
	asked := map[string]string{}
	gh := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.github.com" {
			mu.Lock()
			asked[r.URL.Path] = r.Header.Get("authorization")
			mu.Unlock()
			return respond(200, "text/csv", "a,b\nx,1\ny,2\n"), nil
		}
		return fakeNet.Transport.RoundTrip(r)
	})}
	f := fakeFirebase()
	env := withSignIn(testEnv(&f, nil))
	env.Client = gh
	env.GitHubToken = "ghtok"
	s := start(t, env, signIn(f))
	defer s.close()
	chart := "```vega-lite\n{\"data\":{\"url\":\"https://api.github.com/repos/tero/Secret/contents/x.csv\"},\"mark\":\"bar\",\"encoding\":{\"x\":{\"field\":\"a\",\"type\":\"nominal\"},\"y\":{\"field\":\"b\",\"type\":\"quantitative\"}}}\n```\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Leak", "markdown": "# Leak\n\n## Chart\n\n" + chart})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	r := call(t, s, "render_slide", map[string]any{"deck_id": sc(c)["deck_id"], "slide": 2})
	if r.IsError {
		t.Fatal(lastText(r))
	}
	mu.Lock()
	auth, ok := asked["/repos/tero/Secret/contents/x.csv"]
	mu.Unlock()
	if !ok {
		t.Fatal("the chart's data was not fetched; the test does not reach FetchText")
	}
	eq(t, auth, "", "no token on a chart's data")
}

// export_presentation writes beside the deck: only its owner, or whoever
// has its edit key, may.
func TestExportNeedsEditRights(t *testing.T) {
	f := fakeFirebase()
	anon := start(t, testEnv(&f, nil), "")
	defer anon.close()
	c := call(t, anon, "create_presentation", map[string]any{"title": "Anon", "markdown": "# Anon\n\n## Two\n\nText."})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id, key := sc(c)["deck_id"].(string), sc(c)["edit_key"].(string)
	match(t, textOf(call(t, anon, "export_presentation", map[string]any{"deck_id": id, "format": "pdf"})), `edit_key is needed`)
	match(t, textOf(call(t, anon, "export_presentation", map[string]any{"deck_id": id, "format": "pdf", "edit_key": "wrong"})), `The edit_key does not match`)
	for p := range f.bucket.saved {
		if strings.Contains(p, "/exports/") {
			t.Fatal("written without the key: " + p)
		}
	}
	ok := call(t, anon, "export_presentation", map[string]any{"deck_id": id, "format": "pdf", "edit_key": key})
	if ok.IsError {
		t.Fatal(textOf(ok))
	}

	// a signed-in user's deck: not for another account
	me := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer me.close()
	m := call(t, me, "create_presentation", map[string]any{"title": "Mine", "markdown": "# Mine"})
	if m.IsError {
		t.Fatal(textOf(m))
	}
	mid := sc(m)["deck_id"].(string)
	if r := call(t, me, "export_presentation", map[string]any{"deck_id": mid, "format": "pdf"}); r.IsError {
		t.Fatal(textOf(r))
	}
	match(t, textOf(call(t, anon, "export_presentation", map[string]any{"deck_id": mid, "format": "pdf"})), `edit_key is needed`)
}

// data_base64 that is not base64 is named as such, not as an empty picture
func TestBadBase64(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	r := call(t, s, "create_presentation", map[string]any{"title": "B", "markdown": "# B\n\n![](media/x.png)",
		"images": []any{map[string]any{"name": "x.png", "data_base64": "not base64!"}}})
	match(t, textOf(r), `Image x\.png: data_base64 is not valid base64\.`)
	r2 := call(t, s, "create_presentation", map[string]any{"title": "B", "markdown": "# B",
		"files": []any{map[string]any{"name": "d.csv", "data_base64": "@@@"}}})
	match(t, textOf(r2), `File d\.csv: data_base64 is not valid base64\.`)
}

// an export's sliqtly.com link works for downloadTTL, then answers 410
func TestDownloadLinkExpires(t *testing.T) {
	f := fakeFirebase()
	now := time.Now()
	env := withSignIn(testEnv(&f, nil))
	env.Now = func() time.Time { return now }
	s := start(t, env, signIn(f))
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{"title": "T", "markdown": "# T"})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	p := call(t, s, "export_presentation", map[string]any{"deck_id": sc(c)["deck_id"], "format": "pdf"})
	if p.IsError {
		t.Fatal(textOf(p))
	}
	match(t, textOf(p), `It works for 24 hours`)
	link := s.root + strings.TrimPrefix(sc(p)["url"].(string), BASE)
	get := func() int {
		r, err := http.Get(link)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	eq(t, get(), 200)
	now = now.Add(downloadTTL - time.Minute)
	eq(t, get(), 200, "still within the day")
	now = now.Add(2 * time.Minute)
	eq(t, get(), 410, "expired")
}

// A signed-in user's deck is private by default: only that Google account
// (or the deck's edit key) reads it through the read tools; visibility
// "link" opens it to anyone with the id, and only the owner changes that.
func TestPrivateDeckOnlyForItsOwner(t *testing.T) {
	f := fakeFirebase()
	me := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer me.close()
	other := start(t, withSignIn(testEnv(&f, nil)), signInAs(f, "other-token", "u2", "Someone"))
	defer other.close()
	anon := start(t, testEnv(&f, nil), "")
	defer anon.close()

	c := call(t, me, "create_presentation", map[string]any{"title": "Mine", "markdown": "# Mine\n\n## Two\n\nText.",
		"files": []any{map[string]any{"name": "d.csv", "text": "a,b\nx,1\n"}}})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id, key := sc(c)["deck_id"].(string), sc(c)["edit_key"].(string)
	eq(t, sc(c)["visibility"], "private")
	eq(t, f.db.doc("shares/" + id)["visibility"], "private")
	match(t, textOf(c), `Private: only this Google account sees it`)
	match(t, str(sc(c)["preview_url"]), `/#md=`)

	reads := []struct {
		name string
		args map[string]any
	}{
		{"get_presentation", map[string]any{"deck_id": id}},
		{"render_slide", map[string]any{"deck_id": id, "slide": 1}},
		{"render_overview", map[string]any{"deck_id": id}},
		{"list_files", map[string]any{"deck_id": id}},
		{"read_file", map[string]any{"deck_id": id, "path": "data/d.csv"}},
		{"list_comments", map[string]any{"deck_id": id}},
	}
	for _, r := range reads {
		if got := call(t, me, r.name, r.args); got.IsError {
			t.Fatalf("%s as the owner: %s", r.name, textOf(got))
		}
		got := call(t, other, r.name, r.args)
		if !got.IsError {
			t.Fatalf("%s read another account's private deck", r.name)
		}
		match(t, textOf(got), `private to the Google account that owns it, and this connector is signed in with another account \(Someone\)`)
		got = call(t, anon, r.name, r.args)
		if !got.IsError {
			t.Fatalf("%s read a private deck without sign-in", r.name)
		}
		match(t, textOf(got), `this connector is not signed in`)
		withKey := map[string]any{"edit_key": key}
		for k, v := range r.args {
			withKey[k] = v
		}
		if got := call(t, anon, r.name, withKey); got.IsError {
			t.Fatalf("%s with the edit key: %s", r.name, textOf(got))
		}
	}
	// list_presentations: only the owner's, with its visibility
	match(t, textOf(call(t, me, "list_presentations", map[string]any{})), id+`, private\)`)
	if strings.Contains(textOf(call(t, other, "list_presentations", map[string]any{})), id) {
		t.Fatal("listed for another account")
	}

	// only the owner opens it by link; an edit key does not
	match(t, textOf(call(t, anon, "update_presentation", map[string]any{"deck_id": id, "edit_key": key, "visibility": "link"})), `only the presentation's owner, signed in, changes who sees it`)
	match(t, textOf(call(t, me, "update_presentation", map[string]any{"deck_id": id, "visibility": "public"})), `visibility is private or link`)
	u := call(t, me, "update_presentation", map[string]any{"deck_id": id, "visibility": "link"})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	eq(t, sc(u)["visibility"], "link")
	eq(t, f.db.doc("shares/" + id)["visibility"], "link")
	if got := call(t, other, "get_presentation", map[string]any{"deck_id": id}); got.IsError {
		t.Fatal("a link deck refused: " + textOf(got))
	}
	if r := call(t, me, "update_presentation", map[string]any{"deck_id": id, "visibility": "private"}); r.IsError {
		t.Fatal(textOf(r))
	}
	if got := call(t, other, "get_presentation", map[string]any{"deck_id": id}); !got.IsError {
		t.Fatal("private again, still read by another account")
	}

	// asked for by link at creation
	l := call(t, me, "create_presentation", map[string]any{"title": "Open", "markdown": "# Open", "visibility": "link"})
	eq(t, sc(l)["visibility"], "link")
	match(t, textOf(l), `Anyone with the link can view it`)

	// without sign-in: always by link; private needs sign-in
	a := call(t, anon, "create_presentation", map[string]any{"title": "Anon", "markdown": "# Anon"})
	eq(t, sc(a)["visibility"], "link")
	eq(t, f.db.doc("shares/" + str(sc(a)["deck_id"]))["visibility"], "link")
	match(t, textOf(call(t, anon, "create_presentation", map[string]any{"title": "P", "markdown": "# P", "visibility": "private"})), `A private presentation needs sign-in`)
	match(t, textOf(call(t, anon, "update_presentation", map[string]any{"deck_id": sc(a)["deck_id"], "edit_key": sc(a)["edit_key"], "visibility": "private"})), `made without sign-in`)

	// a share from before visibility existed stays readable by its id
	f.db.Set(context.Background(), "shares", "OldShare01", Doc{"name": "Old", "md": "# Old", "owner": "u1", "theme": "aurora", "files": []any{}})
	if got := call(t, other, "get_presentation", map[string]any{"deck_id": "OldShare01"}); got.IsError {
		t.Fatal("an older share refused: " + textOf(got))
	}
}

func signInAs(f fb, tok, uid, name string) string {
	sum := sha256.Sum256([]byte(tok))
	f.db.Set(context.Background(), "mcp_oauth_tokens", hex.EncodeToString(sum[:]), Doc{"uid": uid, "name": name, "kind": "access", "exp": time.Now().Add(time.Hour).UnixMilli()})
	return tok
}
