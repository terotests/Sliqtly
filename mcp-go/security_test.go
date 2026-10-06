// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
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

// export_presentation writes beside the deck: only its owner, or the
// session that made it without sign-in, may.
func TestExportNeedsEditRights(t *testing.T) {
	f := fakeFirebase()
	anon := start(t, testEnv(&f, nil), "")
	defer anon.close()
	stranger := start(t, testEnv(&f, nil), "")
	defer stranger.close()
	c := call(t, anon, "create_presentation", map[string]any{"title": "Anon", "markdown": "# Anon\n\n## Two\n\nText."})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	match(t, textOf(call(t, stranger, "export_presentation", map[string]any{"deck_id": id, "format": "pdf"})), `made without sign-in in another Sliqtly session`)
	for p := range f.bucket.saved {
		if strings.Contains(p, "/exports/") {
			t.Fatal("written by another session: " + p)
		}
	}
	ok := call(t, anon, "export_presentation", map[string]any{"deck_id": id, "format": "pdf"})
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
	match(t, textOf(call(t, anon, "export_presentation", map[string]any{"deck_id": mid, "format": "pdf"})), `belongs to a signed-in Sliqtly user`)
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

// sliqtly.com is an open demo: a deck is seen by its link unless made
// private, and the result says so. A private deck: only that Google account
// reads it through the read tools; visibility "link" opens it to anyone with
// the id, and only the owner changes that. A deck made without sign-in is
// read by anyone with its id and changed only by the session that made it.
func TestPrivateDeckOnlyForItsOwner(t *testing.T) {
	f := fakeFirebase()
	me := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer me.close()
	other := start(t, withSignIn(testEnv(&f, nil)), signInAs(f, "other-token", "u2", "Someone"))
	defer other.close()
	anon := start(t, testEnv(&f, nil), "")
	defer anon.close()

	open := call(t, me, "create_presentation", map[string]any{"title": "Open", "markdown": "# Open\n\nText."})
	if open.IsError {
		t.Fatal(textOf(open))
	}
	eq(t, sc(open)["visibility"], "link")
	match(t, textOf(open), `not for private or confidential data\. Anyone who has this presentation's link can open every slide`)

	c := call(t, me, "create_presentation", map[string]any{"title": "Mine", "markdown": "# Mine\n\n## Two\n\nText.", "visibility": "private",
		"files": []any{map[string]any{"name": "d.csv", "text": "a,b\nx,1\n"}}})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	if _, has := sc(c)["edit_key"]; has {
		t.Fatal("an edit key handed out")
	}
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
	}
	// list_presentations: only the owner's, with its visibility
	match(t, textOf(call(t, me, "list_presentations", map[string]any{})), id+`, private, created `)
	if strings.Contains(textOf(call(t, other, "list_presentations", map[string]any{})), id) {
		t.Fatal("listed for another account")
	}

	// only the owner opens it by link
	match(t, textOf(call(t, anon, "update_presentation", map[string]any{"deck_id": id, "visibility": "link"})), `belongs to a signed-in Sliqtly user`)
	match(t, textOf(call(t, other, "update_presentation", map[string]any{"deck_id": id, "visibility": "link"})), `belongs to another Sliqtly account`)
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
	match(t, textOf(l), `Anyone who has this presentation.s link can open every slide`)

	// without sign-in: always by link; private needs sign-in
	a := call(t, anon, "create_presentation", map[string]any{"title": "Anon", "markdown": "# Anon"})
	eq(t, sc(a)["visibility"], "link")
	match(t, textOf(a), `not for private or confidential data`)
	eq(t, f.db.doc("shares/" + str(sc(a)["deck_id"]))["visibility"], "link")
	match(t, textOf(call(t, anon, "create_presentation", map[string]any{"title": "P", "markdown": "# P", "visibility": "private"})), `A private presentation needs sign-in`)
	match(t, textOf(call(t, anon, "update_presentation", map[string]any{"deck_id": sc(a)["deck_id"], "visibility": "private"})), `made without sign-in`)

	// a deck made without sign-in: read by anyone with its id, changed only
	// in the session that made it, and no longer once that session ended
	aid := str(sc(a)["deck_id"])
	match(t, textOf(a), `only this conversation's Sliqtly session can change it`)
	if r := call(t, anon, "update_presentation", map[string]any{"deck_id": aid, "markdown": "# Anon 2"}); r.IsError {
		t.Fatal(textOf(r))
	}
	stranger := start(t, testEnv(&f, nil), "")
	defer stranger.close()
	if r := call(t, stranger, "get_presentation", map[string]any{"deck_id": aid}); r.IsError {
		t.Fatal(textOf(r))
	}
	for _, who := range []*testServer{stranger, other, me} {
		match(t, textOf(call(t, who, "update_presentation", map[string]any{"deck_id": aid, "markdown": "# x"})), `made without sign-in in another Sliqtly session`)
		match(t, textOf(call(t, who, "add_comment", map[string]any{"deck_id": aid, "slide": 1, "text": "x"})), `made without sign-in in another Sliqtly session`)
	}
	eq(t, f.db.doc("shares/" + aid)["md"], "# Anon 2")
	// the session's record: the hash of its id, never the id
	var held int
	for k, d := range f.db.all("mcp_sessions/") {
		held++
		match(t, k, `^mcp_sessions/[0-9a-f]{64}$`)
		eq(t, list(d["decks"]), []any{aid})
	}
	eq(t, held, 1)
	anon.close()
	if n := len(f.db.all("mcp_sessions/")); n != 0 {
		t.Fatalf("%d sessions left after the client ended its own", n)
	}

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

// What one caller may keep on a shared server (Tero, 2026-10-06): decks per
// session without sign-in and per account, slides per deck, a deck's bytes,
// and drawings at once.
func TestLimitsPerCaller(t *testing.T) {
	f := fakeFirebase()
	anon := start(t, testEnv(&f, nil), "")
	defer anon.close()
	for i := 1; i <= 3; i++ {
		if c := call(t, anon, "create_presentation", map[string]any{"title": "A", "markdown": "# A"}); c.IsError {
			t.Fatal(textOf(c))
		}
	}
	match(t, textOf(call(t, anon, "create_presentation", map[string]any{"title": "A", "markdown": "# A"})), `without sign-in one conversation makes at most 3 presentations`)
	// the guide says so before a deck is written
	match(t, textOf(call(t, anon, "sliqtly_guide", map[string]any{})), `## Room for new presentations\s+This conversation \(without sign-in\) already keeps 3 presentations, the most it may`)

	long := func(n int) string {
		md := "# Long"
		for i := 2; i <= n; i++ {
			md += fmt.Sprintf("\n\n## Slide %d\n\nText.", i)
		}
		return md
	}
	other := start(t, testEnv(&f, nil), "")
	defer other.close()
	match(t, textOf(call(t, other, "create_presentation", map[string]any{"title": "L", "markdown": long(21)})), `21 slides; a presentation has at most 20.*sign in for up to 100`)
	match(t, textOf(call(t, other, "sliqtly_guide", map[string]any{})), `keeps 0 of at most 3 presentations; 3 more can be created`)
	ok := call(t, other, "create_presentation", map[string]any{"title": "L", "markdown": long(20)})
	if ok.IsError {
		t.Fatal(textOf(ok))
	}
	match(t, textOf(call(t, other, "update_presentation", map[string]any{"deck_id": sc(ok)["deck_id"], "markdown": long(22)})), `22 slides; a presentation has at most 20`)

	me := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer me.close()
	if c := call(t, me, "create_presentation", map[string]any{"title": "L", "markdown": long(60)}); c.IsError {
		t.Fatal(textOf(c))
	}
	// a deck's pictures and files together: 200 MB
	f.db.Set(context.Background(), "shares", "BigDeck001", Doc{"owner": "u1", "md": "# Big", "theme": "aurora", "visibility": "private",
		"files": []any{map[string]any{"path": "data/huge.csv", "type": "text/csv", "size": int64(200 << 20), "url": "x"}}})
	match(t, textOf(call(t, me, "update_presentation", map[string]any{"deck_id": "BigDeck001", "files": []any{map[string]any{"name": "more.csv", "text": "a,b\n1,2\n"}}})), `would come to 200 MB; one presentation keeps at most 200 MB`)
	if r := call(t, me, "update_presentation", map[string]any{"deck_id": "BigDeck001", "files": []any{map[string]any{"name": "huge.csv", "text": "a,b\n1,2\n"}}}); r.IsError {
		t.Fatal("replacing the big file refused: " + textOf(r))
	}
	// an account's decks: 50
	for i := 0; i < 50; i++ {
		f.db.Set(context.Background(), "shares", fmt.Sprintf("Many%06d", i), Doc{"owner": "u1", "md": "# M"})
	}
	match(t, textOf(call(t, me, "create_presentation", map[string]any{"title": "M", "markdown": "# M"})), `already keeps 5[0-9] presentations, the most it may`)
	match(t, textOf(call(t, me, "sliqtly_guide", map[string]any{})), `This Sliqtly account already keeps 5[0-9] presentations, the most it may: create_presentation will refuse a new one`)
}

// At most two drawings at once per caller; a daily count per caller and per
// address on top (dailyRenders).
func TestRenderSlotsAndDailyRenders(t *testing.T) {
	r := &renderSlots{max: 2, lease: time.Minute, held: map[string][]time.Time{}}
	if !r.take("a") || !r.take("a") || r.take("a") {
		t.Fatal("two at once")
	}
	if !r.take("b") {
		t.Fatal("another caller waits for a")
	}
	r.give("a")
	if !r.take("a") {
		t.Fatal("a slot given back")
	}
	stale := &renderSlots{max: 1, lease: time.Millisecond, held: map[string][]time.Time{}}
	stale.take("a")
	time.Sleep(3 * time.Millisecond)
	if !stale.take("a") {
		t.Fatal("a slot never given back frees itself")
	}

	db := newFakeDB()
	day := func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }
	q := dailyRenders(db, 2, 3, day)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		eq(t, q(ctx, "1.2.3.4", "1.2.3.4"), "")
	}
	match(t, q(ctx, "1.2.3.4", "1.2.3.4"), `daily limit of 2 pictures and exports from here.*sign in`)
	for i := 0; i < 3; i++ {
		eq(t, q(ctx, "uid:u1", "5.6.7.8"), "")
	}
	match(t, q(ctx, "uid:u1", "5.6.7.8"), `daily limit of 3 pictures`)
	// the address: twice an account's count (6), whichever accounts
	for i := 0; i < 2; i++ {
		eq(t, q(ctx, "uid:u2", "5.6.7.8"), "")
	}
	eq(t, q(ctx, "uid:u3", "5.6.7.8"), "")
	match(t, q(ctx, "uid:u3", "5.6.7.8"), `daily limit of 3`)
	eq(t, q(ctx, "uid:u3", "9.9.9.9"), "")
}

// delete_presentation: the owner, or the session that made a deck without
// sign-in; the id alone deletes nothing. The record, the files and the
// session's hold go.
func TestDeletePresentation(t *testing.T) {
	f := fakeFirebase()
	me := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer me.close()
	other := start(t, withSignIn(testEnv(&f, nil)), signInAs(f, "other-token", "u2", "Someone"))
	defer other.close()
	anon := start(t, testEnv(&f, nil), "")
	defer anon.close()
	stranger := start(t, testEnv(&f, nil), "")
	defer stranger.close()

	c := call(t, me, "create_presentation", map[string]any{"title": "Mine", "markdown": "# Mine\n\n![dot](media/dot.png)",
		"images": []any{map[string]any{"name": "dot.png", "data_base64": base64.StdEncoding.EncodeToString(squarePNG())}}})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := str(sc(c)["deck_id"])
	files := func(id string) int {
		f.bucket.mu.Lock()
		defer f.bucket.mu.Unlock()
		n := 0
		for p := range f.bucket.saved {
			if strings.HasPrefix(p, "shares/"+id+"/") {
				n++
			}
		}
		return n
	}
	if files(id) == 0 {
		t.Fatal("no files kept to delete")
	}
	for _, who := range []*testServer{other, anon} {
		r := call(t, who, "delete_presentation", map[string]any{"deck_id": id})
		if !r.IsError {
			t.Fatal("deleted by someone else")
		}
		match(t, textOf(r), `^Not deleted: `)
	}
	if f.db.doc("shares/"+id) == nil {
		t.Fatal("gone after a refused delete")
	}
	r := call(t, me, "delete_presentation", map[string]any{"deck_id": id})
	if r.IsError {
		t.Fatal(textOf(r))
	}
	match(t, textOf(r), `Deleted "Mine"`)
	if f.db.doc("shares/"+id) != nil || files(id) != 0 {
		t.Fatalf("left behind: record %v, %d files", f.db.doc("shares/"+id) != nil, files(id))
	}
	if r := call(t, me, "delete_presentation", map[string]any{"deck_id": id}); !r.IsError {
		t.Fatal("deleted twice")
	}

	// without sign-in: only the session that made it, which may then make
	// another in its place
	var made []string
	for i := 0; i < 3; i++ {
		a := call(t, anon, "create_presentation", map[string]any{"title": "Anon", "markdown": "# Anon"})
		if a.IsError {
			t.Fatal(textOf(a))
		}
		made = append(made, str(sc(a)["deck_id"]))
	}
	match(t, textOf(call(t, stranger, "delete_presentation", map[string]any{"deck_id": made[0]})), `Not deleted: presentation \w+ was made without sign-in in another Sliqtly session`)
	if r := call(t, me, "delete_presentation", map[string]any{"deck_id": made[0]}); !r.IsError {
		t.Fatal("a signed-in account deleted another session's deck")
	}
	if r := call(t, anon, "delete_presentation", map[string]any{"deck_id": made[0]}); r.IsError {
		t.Fatal(textOf(r))
	}
	if f.db.doc("shares/"+made[0]) != nil {
		t.Fatal("the session's deck was not deleted")
	}
	for _, d := range f.db.all("mcp_sessions/") {
		if len(list(d["decks"])) == 3 {
			t.Fatal("the session still holds the deleted deck")
		}
	}
	if a := call(t, anon, "create_presentation", map[string]any{"title": "Again", "markdown": "# Again"}); a.IsError {
		t.Fatal(textOf(a))
	}
}

// Every deck says when it was made: "created" on create; a deck from
// before that was kept gets its last change as the earliest time known,
// shown at once and written with its next change.
func TestCreatedDate(t *testing.T) {
	f := fakeFirebase()
	me := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer me.close()
	ctx := context.Background()
	made := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	f.db.Set(ctx, "shares", "OldDeck123", Doc{"name": "Old", "md": "# Old", "theme": "aurora", "owner": "u1", "source": "mcp",
		"files": []any{}, "updated": made.UnixMilli()})
	match(t, textOf(call(t, me, "list_presentations", map[string]any{})), `Old \(OldDeck123, link, created 2026-03-04\)`)
	g := call(t, me, "get_presentation", map[string]any{"deck_id": "OldDeck123"})
	eq(t, sc(g)["created"], "2026-03-04T12:00:00.000Z")
	match(t, textOf(g), `created 2026-03-04`)
	if r := call(t, me, "update_presentation", map[string]any{"deck_id": "OldDeck123", "title": "Old 2"}); r.IsError {
		t.Fatal(textOf(r))
	}
	eq(t, f.db.doc("shares/OldDeck123")["created"], made.Format(time.RFC3339))
}
