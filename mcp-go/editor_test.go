// SPDX-License-Identifier: AGPL-3.0-or-later

// The editor at /editor (editor.go): the license model, and that the
// editor's files go to signed-in people only.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

var editorNow = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func TestLicenseModel(t *testing.T) {
	l := licenseOf(nil, false)
	eq(t, []any{l.Plan, l.MaxDocs, len(l.Docs)}, []any{"trial", 2, 0})
	eq(t, l.canEdit("a", editorNow), "full")

	l, changed, why := l.claim("a", editorNow)
	eq(t, []any{changed, why, l.Docs}, []any{true, "", []string{"a"}})
	l2, changed, why := l.claim("a", editorNow)
	eq(t, []any{changed, why, len(l2.Docs)}, []any{false, "", 1})
	l, _, _ = l.claim("b", editorNow)
	_, changed, why = l.claim("c", editorNow)
	eq(t, []any{changed, why}, []any{false, "full"})
	eq(t, l.canEdit("b", editorNow), "")

	// what the owner writes in the console
	d := Doc{"plan": "pro", "maxDocs": int64(10), "docs": []any{"x", "x", "y"}, "editUntil": editorNow.Add(-time.Hour)}
	l = licenseOf(d, false)
	eq(t, []any{l.Plan, l.MaxDocs, l.Docs}, []any{"pro", 10, []string{"x", "y"}})
	eq(t, l.canEdit("x", editorNow), "expired")
	_, _, why = l.claim("z", editorNow)
	eq(t, why, "expired")
	eq(t, l.json(editorNow)["canEdit"], false)

	// no limit
	l = licenseOf(Doc{"maxDocs": int64(-1)}, false)
	eq(t, l.canEdit("any", editorNow), "")

	// an admin has none, whatever the document says
	l = licenseOf(d, true)
	eq(t, []any{l.Plan, l.MaxDocs, l.canEdit("q", editorNow)}, []any{"admin", -1, ""})
}

// a server with the editor, its sign-in faked: the cookie is the token
func editorServer(t *testing.T) (*Env, string, func()) {
	web := fstest.MapFS{
		"index.html":   {Data: []byte("<!doctype html><html><head>\n<base href=\"/\" />\n</head><body><script type=\"module\" src=\"./main.js?v=1\"></script></body></html>")},
		"main.js":      {Data: []byte("// the editor\n")},
		"pres_app.js":  {Data: []byte("// the engine\n")},
		"samples/a.md": {Data: []byte("# A\n")},
	}
	f := fakeFirebase()
	e := testEnv(&f, nil)
	verify := func(_ context.Context, tok string) (*IDToken, error) {
		switch tok {
		case "tero":
			return &IDToken{UID: "u-tero", Email: "teroktolonen@gmail.com", Name: "Tero", Verified: true}, nil
		case "anna":
			return &IDToken{UID: "u-anna", Email: "anna@example.com", Name: "Anna", Verified: true}, nil
		case "mallory":
			// claims Anna's address, unverified
			return &IDToken{UID: "u-mallory", Email: "Anna@example.com", Name: "Anna", Verified: false}, nil
		}
		return nil, errors.New("expired")
	}
	e.EditorGate = newEditorGate(web, e.DB, verify, []string{"TeroKTolonen@gmail.com"})
	if e.EditorGate == nil {
		t.Fatal("no gate")
	}
	e.EditorGate.now = func() time.Time { return editorNow }
	srv := httptest.NewServer(NewApp(e))
	return e, srv.URL, srv.Close
}

func editorDo(t *testing.T, method, url, cookie, origin, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "__session", Value: cookie})
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, string(b)
}

func TestEditorOnlySignedIn(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()

	res, _ := editorDo(t, "GET", base+"/editor?x=1", "", "", "")
	eq(t, []any{res.StatusCode, res.Header.Get("Location")}, []any{302, "/editor/?x=1"})

	// signed out (or a sign-in that ran out): the sign-in page, none of the editor
	for _, cookie := range []string{"", "old"} {
		for _, p := range []string{"/editor/", "/editor/d/abcdef1234", "/editor/s/abcdef1234"} {
			res, body := editorDo(t, "GET", base+p, cookie, "", "")
			eq(t, res.StatusCode, 200, p)
			match(t, body, "Sign in to the editor")
			if strings.Contains(body, "main.js") {
				t.Fatalf("%s: the editor's page went out signed out", p)
			}
			match(t, res.Header.Get("Cache-Control"), "private")
		}
		for _, p := range []string{"/editor/main.js?v=1", "/editor/pres_app.js", "/editor/samples/a.md", "/editor/index.html"} {
			res, body := editorDo(t, "GET", base+p, cookie, "", "")
			if res.StatusCode == 200 && (strings.Contains(body, "// the e") || strings.Contains(body, "main.js")) {
				t.Fatalf("%s went out signed out", p)
			}
		}
	}

	// signed in: the page, its files under /editor/, who and the license
	res, body := editorDo(t, "GET", base+"/editor/d/abcdef1234", "anna", "", "")
	eq(t, res.StatusCode, 200)
	match(t, body, `<base href="/editor/" />`)
	match(t, body, `name="sliqtly-editor"`)
	match(t, body, `&quot;uid&quot;:&quot;u-anna&quot;`)
	match(t, res.Header.Get("Cache-Control"), "private")
	res, body = editorDo(t, "GET", base+"/editor/main.js?v=1", "anna", "", "")
	eq(t, []any{res.StatusCode, body}, []any{200, "// the editor\n"})
	eq(t, res.Header.Get("Cache-Control"), "private, max-age=31536000, immutable")
	res, _ = editorDo(t, "GET", base+"/editor/../main.js", "anna", "", "")
	if res.StatusCode == 200 {
		t.Fatal("a path out of the editor")
	}
	res, _ = editorDo(t, "GET", base+"/editor/nothing.js", "anna", "", "")
	eq(t, res.StatusCode, 404)
}

func TestEditorSession(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	api := base + "/editor/api/session"

	res, _ := editorDo(t, "POST", api, "", "https://evil.example", `{"idToken":"anna"}`)
	eq(t, res.StatusCode, 403)
	res, _ = editorDo(t, "POST", api, "", base, `{"idToken":"nope"}`)
	eq(t, res.StatusCode, 401)
	res, body := editorDo(t, "POST", api, "", base, `{"idToken":"anna"}`)
	eq(t, res.StatusCode, 200)
	c := res.Cookies()
	if len(c) != 1 || c[0].Name != "__session" || c[0].Value != "anna" || !c[0].HttpOnly || !c[0].Secure || c[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie %+v", c)
	}
	var out map[string]any
	json.Unmarshal([]byte(body), &out)
	eq(t, out["license"].(map[string]any)["plan"], "trial")

	res, _ = editorDo(t, "POST", base+"/editor/api/signout", "anna", base, "{}")
	eq(t, []any{res.StatusCode, res.Cookies()[0].MaxAge}, []any{200, -1})
}

func TestEditorClaim(t *testing.T) {
	e, base, stop := editorServer(t)
	defer stop()
	claim := func(who, id string) (int, map[string]any) {
		res, body := editorDo(t, "POST", base+"/editor/api/claim", who, base, `{"id":"`+id+`"}`)
		var out map[string]any
		json.Unmarshal([]byte(body), &out)
		return res.StatusCode, out
	}
	code, _ := claim("", "abcdef1234")
	eq(t, code, 401)
	code, _ = claim("anna", "../x")
	eq(t, code, 400)

	for _, id := range []string{"deckAAAAAA", "deckBBBBBB", "deckAAAAAA"} {
		code, out := claim("anna", id)
		eq(t, code, 200, id)
		_ = out
	}
	code, out := claim("anna", "deckCCCCCC")
	eq(t, []any{code, out["code"], out["why"]}, []any{403, "no-edit-right", "full"})

	// the license as kept, for the console and the rules
	d, _ := e.DB.Get(context.Background(), "licenses", "u-anna")
	eq(t, []any{d["email"], d["plan"], d["docs"]}, []any{"anna@example.com", "trial", []any{"deckAAAAAA", "deckBBBBBB"}})

	// the owner gives more in the console
	e.DB.Update(context.Background(), "licenses", "u-anna", Doc{"maxDocs": int64(3)})
	code, _ = claim("anna", "deckCCCCCC")
	eq(t, code, 200)
	// and ends it: nothing is taken away, nothing more is changed
	e.DB.Update(context.Background(), "licenses", "u-anna", Doc{"editUntil": editorNow.Add(-time.Minute).Format(time.RFC3339)})
	code, out = claim("anna", "deckAAAAAA")
	eq(t, []any{code, out["why"]}, []any{403, "expired"})
	d, _ = e.DB.Get(context.Background(), "licenses", "u-anna")
	eq(t, len(d["docs"].([]any)), 3)

	// the admin: no limit
	for _, id := range []string{"t000000001", "t000000002", "t000000003"} {
		code, _ := claim("tero", id)
		eq(t, code, 200, id)
	}
	d, _ = e.DB.Get(context.Background(), "licenses", "u-tero")
	eq(t, []any{d["plan"], d["maxDocs"]}, []any{"admin", int64(-1)})
}

// someone else's presentation: not taken under one's license, and not
// edited, unless its owner invited one's (verified) address
func TestEditorClaimOthers(t *testing.T) {
	e, base, stop := editorServer(t)
	defer stop()
	ctx := context.Background()
	e.DB.Set(ctx, "shares", "teroDeck01", Doc{"owner": "u-tero", "md": "# T", "editors": []any{"anna@example.com"}})
	e.DB.Set(ctx, "shares", "teroDeck02", Doc{"owner": "u-tero", "md": "# T2"})
	e.DB.Set(ctx, "shares", "annaDeck01", Doc{"owner": "u-anna", "md": "# A"})
	claim := func(who, id string) (int, map[string]any) {
		res, body := editorDo(t, "POST", base+"/editor/api/claim", who, base, `{"id":"`+id+`"}`)
		var out map[string]any
		json.Unmarshal([]byte(body), &out)
		return res.StatusCode, out
	}
	code, out := claim("anna", "teroDeck02")
	eq(t, []any{code, out["code"], out["why"]}, []any{403, "no-edit-right", "not-yours"})
	// invited: edited, and none of Anna's two presentations spent on it
	code, _ = claim("anna", "teroDeck01")
	eq(t, code, 200)
	d, _ := e.DB.Get(ctx, "licenses", "u-anna")
	eq(t, len(d["docs"].([]any)), 0)
	// her own as before
	code, _ = claim("anna", "annaDeck01")
	eq(t, code, 200)
	// the admin has no limit on his own, but Anna's is still hers
	code, out = claim("tero", "annaDeck01")
	eq(t, []any{code, out["why"]}, []any{403, "not-yours"})
	// an invited user whose license ended edits nothing more
	e.DB.Update(ctx, "licenses", "u-anna", Doc{"editUntil": editorNow.Add(-time.Minute).Format(time.RFC3339)})
	code, out = claim("anna", "teroDeck01")
	eq(t, []any{code, out["why"]}, []any{403, "expired"})
}

func TestInvitedTo(t *testing.T) {
	share := Doc{"editors": []any{"anna@example.com"}}
	eq(t, invitedTo(share, &IDToken{Email: "Anna@Example.com", Verified: true}), true)
	eq(t, invitedTo(share, &IDToken{Email: "anna@example.com", Verified: false}), false)
	eq(t, invitedTo(share, &IDToken{Email: "bo@example.com", Verified: true}), false)
	eq(t, invitedTo(Doc{}, &IDToken{Email: "anna@example.com", Verified: true}), false)
}

func TestEditLinksGoToTheEditor(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	res, _ := editorDo(t, "GET", base+"/s/abcdef1234?edit", "", "", "")
	eq(t, []any{res.StatusCode, res.Header.Get("Location")}, []any{302, "/editor/d/abcdef1234"})
	res, _ = editorDo(t, "GET", base+"/s/abcdef1234?edit&slide=3", "", "", "")
	eq(t, []any{res.StatusCode, res.Header.Get("Location")}, []any{302, "/editor/d/abcdef1234?slide=3"})
	// the editor's address before: to the document's own
	res, _ = editorDo(t, "GET", base+"/editor/s/abcdef1234?edit", "anna", "", "")
	eq(t, []any{res.StatusCode, res.Header.Get("Location")}, []any{302, "/editor/d/abcdef1234"})

	// without the editor, /editor is nothing and an edit link the viewer's
	f := fakeFirebase()
	srv := httptest.NewServer(NewApp(testEnv(&f, nil)))
	defer srv.Close()
	res, _ = editorDo(t, "GET", srv.URL+"/editor/", "anna", "", "")
	eq(t, res.StatusCode, 404)
	res, _ = editorDo(t, "GET", srv.URL+"/s/abcdef1234?edit", "", "", "")
	if res.StatusCode == 302 {
		t.Fatal("an edit link went to an editor that is not there")
	}
}

func TestEditorGateNeedsTheEditor(t *testing.T) {
	verify := func(context.Context, string) (*IDToken, error) { return nil, nil }
	db := newFakeDB()
	if newEditorGate(nil, db, verify, nil) != nil {
		t.Fatal("a gate without the editor's files")
	}
	viewer := fstest.MapFS{"index.html": {Data: []byte("x")}, "view.js": {Data: []byte("x")}}
	if newEditorGate(viewer, db, verify, nil) != nil {
		t.Fatal("a gate in front of the viewer's build")
	}
}
