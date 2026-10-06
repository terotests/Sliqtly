// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
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
