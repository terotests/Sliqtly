// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// sliqtly.com's Figma files through /editor/api/shared: each account sees
// its own; a deck's pictures come from its owner's files, for the people
// it invites and for the server's layouts
func TestCloudSharedFiles(t *testing.T) {
	fig, err := os.ReadFile("testdata/health.fig")
	if err != nil {
		t.Fatal(err)
	}
	env, base, stop := editorServer(t)
	defer stop()
	_, bucket, err := newFSStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	env.cloudShared = newCloudShared(bucket, nil)
	done := make(chan string, 4)
	env.cloudShared.of("u-anna").indexed = func(id string) { done <- id }
	ctx := t.Context()

	call := func(method, path, who, body string) (int, map[string]any) {
		t.Helper()
		res, text := editorDo(t, method, base+"/editor/api/shared"+path, who, base, body)
		out := map[string]any{}
		json.Unmarshal([]byte(text), &out)
		return res.StatusCode, out
	}
	files := func(who string) []any {
		t.Helper()
		code, out := call("GET", "", who, "")
		eq(t, code, 200, who+" lists")
		l, _ := out["files"].([]any)
		return l
	}

	// anna's page uploads to her folder, then the server moves it in
	code, out := call("GET", "", "anna", "")
	eq(t, []any{code, out["upload"]}, []any{200, "users/u-anna/figma-upload/"}, "anna's upload folder")
	if err := bucket.Save(ctx, "users/u-anna/figma-upload/abcdef1234.fig", "application/octet-stream", fig, nil); err != nil {
		t.Fatal(err)
	}
	// not someone else's upload, not past the folder
	code, out = call("POST", "/add", "tero", `{"name":"Health App.fig","upload":"users/u-anna/figma-upload/abcdef1234.fig"}`)
	eq(t, []any{code, out["error"]}, []any{400, "the upload is not yours"}, "tero takes anna's upload")
	code, _ = call("POST", "/add", "anna", `{"name":"x.fig","upload":"users/u-anna/figma-upload/../decks/a.png"}`)
	eq(t, code, 400, "a path out of the folder")
	res, _ := editorDo(t, "POST", base+"/editor/api/shared/add", "anna", "https://evil.example", `{}`)
	eq(t, res.StatusCode, 403, "from another site")
	code, _ = call("GET", "", "", "")
	eq(t, code, 401, "signed out")

	code, out = call("POST", "/add", "anna", `{"name":"Health App.fig","upload":"users/u-anna/figma-upload/abcdef1234.fig"}`)
	eq(t, []any{code, out["id"], out["status"]}, []any{201, "health-app", "indexing"}, "anna adds it")
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the file was never read")
	}
	if _, err := bucket.Read(ctx, "users/u-anna/figma-upload/abcdef1234.fig", 10); err == nil {
		t.Fatal("the upload is left behind")
	}
	l := files("anna")
	if len(l) != 1 || l[0].(map[string]any)["status"] != "ready" || l[0].(map[string]any)["index"] != "/editor/api/shared/health-app/index.json" {
		t.Fatalf("anna's files: %v", l)
	}
	eq(t, len(files("tero")), 0, "tero sees none of anna's")

	png := func(who, path string) int {
		t.Helper()
		res, body := editorDo(t, "GET", base+"/editor/api/shared/"+path, who, "", "")
		if res.StatusCode == 200 && (res.Header.Get("Content-Type") != "image/png" || !strings.HasPrefix(body, "\x89PNG")) {
			t.Fatalf("%s: not a PNG", path)
		}
		return res.StatusCode
	}
	eq(t, png("anna", "health-app/png/1:2.png?max=300"), 200, "anna draws her screen")
	eq(t, png("anna", "health-app/png/Dashboard.png?max=300"), 200, "by its name")
	eq(t, png("tero", "health-app/png/1:2.png?max=300"), 404, "tero draws anna's screen")
	code, _ = call("GET", "/health-app/index.json", "tero", "")
	eq(t, code, 404, "tero reads anna's index")
	code, out = call("GET", "/health-app/index.json", "anna", "")
	eq(t, []any{code, out["screens"] != nil}, []any{200, true}, "anna's index")

	// anna's deck, which invites tero: its pictures are anna's screens
	env.DB.Set(ctx, "shares", "deckAnna01", Doc{"owner": "u-anna", "editors": []any{"teroktolonen@gmail.com"}})
	eq(t, png("tero", "health-app/png/1:2.png?max=300&deck=deckAnna01"), 200, "tero in anna's deck")
	eq(t, png("mallory", "health-app/png/1:2.png?max=300&deck=deckAnna01"), 404, "an address not verified")
	eq(t, png("tero", "health-app/png/1:2.png?max=300&deck=nosuchdeck1"), 404, "a deck not kept: tero's own")

	// the server's layouts: the deck's owner's files, else the caller's
	h := &McpHost{env: env, ctx: ctx, images: map[int64][]byte{}}
	h.SetCaller("u-tero")
	if h.SharedPng("health-app", "1:2", "deckAnna01") == 0 {
		t.Fatal("anna's deck does not draw her screen")
	}
	if h.SharedPng("health-app", "1:2", "") != 0 {
		t.Fatal("tero's new deck draws anna's screen")
	}
	h.SetCaller("u-anna")
	if h.SharedPng("health-app", "1:2", "") == 0 {
		t.Fatal("anna's new deck does not draw her screen")
	}

	// the assistant's tools: the caller's own, none added from a path
	tools := h.HostTools()
	if !strings.Contains(tools, `"search_figma"`) || strings.Contains(tools, `"add_shared_file"`) || !strings.Contains(tools, "seen by your account alone") {
		t.Fatalf("tools: %.300s", tools)
	}
	if got := h.CallTool("u-anna", "u-anna", "list_shared_files", "{}"); !strings.Contains(got, `"health-app"`) {
		t.Fatalf("anna's list: %s %s", got, h.Err())
	}
	if got := h.CallTool("u-tero", "u-tero", "search_figma", `{"query":"dashboard"}`); strings.Contains(got, "health-app") {
		t.Fatalf("tero finds anna's screens: %s", got)
	}
	h.ClearErr()
	if got := h.CallTool("", "1.2.3.4", "list_shared_files", "{}"); got != "" || !strings.Contains(h.Err(), "sign in") {
		t.Fatalf("no sign-in: %q %s", got, h.Err())
	}

	// rename and delete: anna's alone
	code, _ = call("POST", "/health-app/rename", "tero", `{"name":"Mine.fig"}`)
	eq(t, code, 404, "tero renames anna's")
	code, out = call("POST", "/health-app/rename", "anna", `{"name":"Health v2.fig"}`)
	eq(t, []any{code, out["name"]}, []any{200, "Health v2.fig"}, "anna renames")
	code, _ = call("DELETE", "/health-app", "tero", "")
	eq(t, code, 404, "tero deletes anna's")
	code, _ = call("DELETE", "/health-app", "anna", "")
	eq(t, code, 200, "anna deletes")
	eq(t, len(files("anna")), 0, "anna's files after")
}

// a file whose job never finished shows as failed, not indexing for ever
func TestCloudSharedStale(t *testing.T) {
	_, bucket, err := newFSStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	started := []string{}
	c := newCloudShared(bucket, func(_ context.Context, uid, id string) error {
		started = append(started, uid+" "+id)
		return nil
	})
	sh := c.of("u-anna")
	ctx := t.Context()
	bucket.Save(ctx, "users/u-anna/figma-upload/abcdef1234.fig", "application/octet-stream", []byte("not read here"), nil)
	if _, err := c.adopt(ctx, "u-anna", "App.fig", "", "users/u-anna/figma-upload/abcdef1234.fig"); err != nil {
		t.Fatal(err)
	}
	eq(t, started, []string{"u-anna app"}, "the job started for the file")
	l, _ := sh.list(ctx)
	eq(t, l[0].Status, "indexing", "just added")
	f, _ := sh.card(ctx, "app")
	b, _ := json.Marshal(sharedFile{ID: f.ID, Name: f.Name, Status: "indexing", Updated: time.Now().Add(-time.Hour).UnixMilli()})
	bucket.Save(ctx, cloudSharedRoot("u-anna")+sharedMetaPath("app"), "application/json", b, nil)
	l, _ = sh.list(ctx)
	if l[0].Status != "failed" || !strings.Contains(l[0].Error, "too large") {
		t.Fatalf("an hour later: %+v", l[0])
	}
	if c.of("") != nil || c.of("a/b") != nil {
		t.Fatal("an account id that is not one")
	}
}

// the viewer (a shared link, the assistant's preview) gets a deck's Figma
// pictures from its owner's files, through /api/view/<id>/pic
func TestViewFigmaPictures(t *testing.T) {
	fig, err := os.ReadFile("testdata/health.fig")
	if err != nil {
		t.Fatal(err)
	}
	f := fakeFirebase()
	env := withSignIn(testEnv(&f, nil))
	_, bucket, err := newFSStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	env.cloudShared = newCloudShared(bucket, nil)
	done := make(chan string, 1)
	env.cloudShared.of("u1").indexed = func(id string) { done <- id }
	s := start(t, env, signIn(f))
	defer s.close()
	ctx := t.Context()
	bucket.Save(ctx, "users/u1/figma-upload/abcdef1234.fig", "application/octet-stream", fig, nil)
	if _, err := env.cloudShared.adopt(ctx, "u1", "Health App.fig", "", "users/u1/figma-upload/abcdef1234.fig"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the file was never read")
	}

	c := call(t, s, "create_presentation", map[string]any{
		"title": "Screens", "visibility": "link",
		"markdown": "# Screens\n\n## Dashboard\n\n![Dashboard](figma:health-app/1:2)\n",
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	code, _, v, body := getView(t, s.root+"/api/view/"+id)
	eq(t, code, 200)
	url := ""
	for _, file := range v.Deck.Files {
		if file.Path == "figma:health-app/1:2" {
			url = file.URL
		}
	}
	if !strings.HasPrefix(url, "/api/view/"+id+"/pic?path=figma%3Ahealth-app%2F1%3A2&v=") {
		t.Fatalf("figma file url %q in %.400s", url, body)
	}
	get := func(u string) *http.Response {
		t.Helper()
		no := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := no.Get(s.root + u)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := get(url)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	eq(t, []any{res.StatusCode, res.Header.Get("Content-Type"), strings.HasPrefix(string(b), "\x89PNG")}, []any{200, "image/png", true}, "the screen drawn")
	// an old stamp (the file was added again since): to the current address
	res = get("/api/view/" + id + "/pic?path=figma%3Ahealth-app%2F1%3A2&v=old")
	res.Body.Close()
	eq(t, []any{res.StatusCode, res.Header.Get("Location")}, []any{302, url}, "an old stamp")
	// a screen the deck does not show: nothing of the owner's other files
	res = get("/api/view/" + id + "/pic?path=figma%3Ahealth-app%2F1%3A3&v=x")
	res.Body.Close()
	eq(t, res.StatusCode, 404, "a screen not on the deck")
}
