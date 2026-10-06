package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/terotests/sliqtly/mcp-go/store"
)

func TestFolderDB(t *testing.T) {
	ctx := context.Background()
	db, _, err := newFSStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	when := time.UnixMilli(1700000000123).UTC()
	if err := db.Set(ctx, "shares", "a1", Doc{"owner": "u1", "n": int64(3), "f": 1.5, "at": when, "list": []any{"x", int64(2)}}); err != nil {
		t.Fatal(err)
	}
	d, err := db.Get(ctx, "shares", "a1")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, d["n"], int64(3))
	eq(t, d["f"], 1.5)
	if at, ok := d["at"].(time.Time); !ok || !at.Equal(when) {
		t.Fatalf("time read back as %#v", d["at"])
	}
	if missing, err := db.Get(ctx, "shares", "nope"); missing != nil || err != nil {
		t.Fatalf("missing: %v %v", missing, err)
	}

	if err := db.Update(ctx, "shares", "a1", Doc{"n": int64(4), "nested.deep": "v"}); err != nil {
		t.Fatal(err)
	}
	d, _ = db.Get(ctx, "shares", "a1")
	eq(t, d["n"], int64(4))
	eq(t, d["nested"], map[string]any{"deep": "v"})
	eq(t, d["owner"], "u1", "update keeps the other fields")
	if err := db.Update(ctx, "shares", "nope", Doc{"n": int64(1)}); err == nil {
		t.Fatal("updated a document that is not there")
	}

	db.Set(ctx, "shares", "b2", Doc{"owner": "u2"})
	docs, ids, err := db.WhereEq(ctx, "shares", "owner", "u1")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, ids, []string{"a1"})
	eq(t, len(docs), 1)

	had, err := db.Create(ctx, "keys", "k", Doc{"v": "first"})
	if had != nil || err != nil {
		t.Fatalf("create: %v %v", had, err)
	}
	had, _ = db.Create(ctx, "keys", "k", Doc{"v": "second"})
	eq(t, had["v"], "first")

	db.Increment(ctx, "stats", "day", Doc{"hits": int64(1), "by": map[string]any{"example.com": int64(2)}})
	db.Increment(ctx, "stats", "day", Doc{"hits": int64(1), "by": map[string]any{"example.com": int64(1)}})
	d, _ = db.Get(ctx, "stats", "day")
	eq(t, d["hits"], int64(2))
	eq(t, d["by"], map[string]any{"example.com": int64(3)})

	db.Delete(ctx, "shares", "a1")
	if d, _ := db.Get(ctx, "shares", "a1"); d != nil {
		t.Fatal("not deleted")
	}
	for _, bad := range []string{"", "..", "a/b", `a\b`} {
		if _, err := db.Get(ctx, "shares", bad); err == nil {
			t.Fatalf("id %q was accepted", bad)
		}
	}
}

func TestFolderBucketStaysInside(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	_, b, _ := newFSStore(root, "local")
	if err := b.Save(ctx, "shares/x/media/a.png", "image/png", PNG, nil); err != nil {
		t.Fatal(err)
	}
	got, err := b.Read(ctx, "shares/x/media/a.png", 1<<20)
	if err != nil || string(got) != string(PNG) {
		t.Fatalf("read back: %v", err)
	}
	for _, bad := range []string{"../db/shares/x.json", "/etc/passwd", "shares/../../outside"} {
		if err := b.Save(ctx, bad, "text/plain", []byte("x"), nil); err == nil {
			t.Fatalf("saved outside the folder: %s", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "outside")); err == nil {
		t.Fatal("a file was written outside")
	}
}

// the local server end to end: MCP over HTTP, the folder, the pages
func startLocal(t *testing.T, dir, token string) (*httptest.Server, *mcp.ClientSession) {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	base := "http://" + srv.Listener.Addr().String()
	e, bucket, err := localEnv(dir, base, "local")
	if err != nil {
		t.Fatal(err)
	}
	e.Client = fakeNet
	srv.Config.Handler = newLocalServer(e, bucket, token, nil)
	srv.Start()
	hc := &http.Client{}
	if token != "" {
		hc.Transport = withToken{token, http.DefaultTransport}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: base + "/mcp", HTTPClient: hc}, nil)
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	return srv, session
}

func get(t *testing.T, url string) (int, string, string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header.Get("Content-Type"), string(b)
}

func TestLocalServer(t *testing.T) {
	dir := t.TempDir()
	srv, session := startLocal(t, dir, "")
	s := &testServer{root: srv.URL, session: session}

	c := call(t, s, "create_presentation", map[string]any{
		"title": "Pilot", "markdown": DECK,
		"images": []any{
			map[string]any{"name": "cat.png", "url": "https://images.test/cat.png"},
			map[string]any{"name": "dot.png", "data_base64": base64.StdEncoding.EncodeToString(PNG)},
		},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	out := sc(c)
	id := out["deck_id"].(string)
	eq(t, out["share_url"], srv.URL+"/s/"+id)
	match(t, textOf(c), `Saved on this Sliqtly server. It has no sign-in`)

	// kept in the folder, pictures served by the server itself
	docs, err := store.OpenSQLiteStore(filepath.Join(dir, docsFile))
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	share, _, _ := docs.Get(context.Background(), "shares", id)
	files := list(share["files"])
	eq(t, mapOf(files[0])["url"], srv.URL+"/files/shares/"+id+"/media/cat.png")
	code, ct, body := get(t, srv.URL+"/files/shares/"+id+"/media/cat.png")
	eq(t, []any{code, ct, body == string(PNG)}, []any{200, "image/png", true})
	code, _, _ = get(t, srv.URL+"/files/shares/"+id+"/media/cat.png.type")
	eq(t, code, 404)

	// the pages
	code, ct, body = get(t, srv.URL+"/s/"+id+"/1.jpg")
	eq(t, []any{code, ct, len(body) > 1000}, []any{200, "image/jpeg", true})
	code, ct, _ = get(t, srv.URL+"/s/"+id+"/overview.jpg")
	eq(t, []any{code, ct}, []any{200, "image/jpeg"})
	code, _, body = get(t, srv.URL+"/s/"+id)
	eq(t, code, 200)
	match(t, body, `2 slides · theme aurora`)
	match(t, body, `/s/`+id+`/2\.jpg`)
	code, _, body = get(t, srv.URL+"/decks")
	match(t, body, `href="/s/`+id+`">Pilot<`)
	code, _, _ = get(t, srv.URL+"/s/nothere123")
	eq(t, code, 404)
	code, ct, _ = get(t, srv.URL+"/themes/aurora.css")
	eq(t, []any{code, ct}, []any{200, "text/css; charset=utf-8"})
	session.Close()
	srv.Close()

	// a new server on the same folder has the deck
	srv2, session2 := startLocal(t, dir, "")
	defer srv2.Close()
	defer session2.Close()
	s2 := &testServer{root: srv2.URL, session: session2}
	u := call(t, s2, "update_presentation", map[string]any{"deck_id": id, "markdown": DECK + "\n## More\n\nText.\n"})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	eq(t, sc(u)["slides"], 3)
	match(t, textOf(call(t, s2, "list_presentations", map[string]any{})), `Pilot \(`+id+`, link\)`)
	g := sc(call(t, s2, "get_presentation", map[string]any{"deck_id": id}))
	eq(t, len(list(g["images"])), 2)
}

func TestLocalServerToken(t *testing.T) {
	dir := t.TempDir()
	srv, session := startLocal(t, dir, "s3cret")
	defer srv.Close()
	defer session.Close()
	if r := call(t, &testServer{session: session}, "list_presentations", map[string]any{}); r.IsError {
		t.Fatal(textOf(r))
	}
	res, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	eq(t, res.StatusCode, 401)
}

func req(t *testing.T, method, url, ct string, body string) (int, string) {
	t.Helper()
	r, _ := http.NewRequest(method, url, strings.NewReader(body))
	if ct != "" {
		r.Header.Set("Content-Type", ct)
	}
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// the page's own API (assets/sliqtly-local.js) and the built page
func TestLocalWebAndAPI(t *testing.T) {
	dir := t.TempDir()
	web := fstest.MapFS{
		"index.html":        {Data: []byte("<html><head><title>x</title></head><body><script type=\"module\" src=\"./sliqtly.js?v=1\"></script></body></html>")},
		"main.js":           {Data: []byte("console.log(1)")},
		"themes/custom.css": {Data: []byte("page { background-color: #123456; }")},
	}
	srv := httptest.NewUnstartedServer(nil)
	base := "http://" + srv.Listener.Addr().String()
	e, bucket, err := localEnv(dir, base, "local")
	if err != nil {
		t.Fatal(err)
	}
	e.Client = fakeNet
	srv.Config.Handler = newLocalServer(e, bucket, "", web)
	srv.Start()
	defer srv.Close()

	// the page, with this server's address for its links
	for _, p := range []string{"/", "/index.html", "/s/abcdef1234"} {
		code, _, body := get(t, srv.URL+p)
		eq(t, code, 200, p)
		match(t, body, `<meta name="sliqtly-site" content="`+regexp.QuoteMeta(base)+`" />\s*</head>`)
	}
	code, ct, body := get(t, srv.URL+"/sliqtly.js?v=1")
	eq(t, []any{code, ct}, []any{200, "text/javascript; charset=utf-8"})
	match(t, body, `window\.sliqtly = \{`)
	code, _, body = get(t, srv.URL+"/main.js?v=1")
	eq(t, []any{code, body}, []any{200, "console.log(1)"})
	code, _, body = get(t, srv.URL+"/themes/custom.css")
	eq(t, []any{code, body}, []any{200, "page { background-color: #123456; }"})
	code, _, _ = get(t, srv.URL+"/themes/aurora.css")
	eq(t, code, 200, "the built-in themes stay")
	code, ct, body = get(t, srv.URL+"/fonts/OpenSans-Regular.ttf?v=1")
	eq(t, []any{code, ct, len(body) > 10000}, []any{200, "font/ttf", true}, "the page's faces come from the server's own copy")

	code, body = req(t, "GET", srv.URL+"/api/me", "", "")
	eq(t, []any{code, strings.TrimSpace(body)}, []any{200, `{"name":"local","uid":"local"}`})

	// a form post is not taken
	code, _ = req(t, "POST", srv.URL+"/api/shares", "application/x-www-form-urlencoded", "name=x")
	eq(t, code, 415)

	code, body = req(t, "POST", srv.URL+"/api/shares", "application/json", `{"name":"Web deck","md":"# One\n","theme":"aurora","css":null,"deck":"d1"}`)
	eq(t, code, 201)
	var made map[string]string
	json.Unmarshal([]byte(body), &made)
	id := made["id"]

	code, body = req(t, "PUT", srv.URL+"/api/files/shares/"+id+"/media/dot.png", "image/png", string(PNG))
	eq(t, code, 200)
	var f map[string]any
	json.Unmarshal([]byte(body), &f)
	eq(t, f["url"], base+"/files/shares/"+id+"/media/dot.png")
	code, ct, got := get(t, srv.URL+"/files/shares/"+id+"/media/dot.png")
	eq(t, []any{code, ct, got == string(PNG)}, []any{200, "image/png", true})

	files, _ := json.Marshal([]any{f})
	code, _ = req(t, "PATCH", srv.URL+"/api/shares/"+id, "application/json", `{"md":"# One\n\n## Two\n","files":`+string(files)+`,"ifMd":"# One\n"}`)
	eq(t, code, 200)
	code, body = req(t, "PATCH", srv.URL+"/api/shares/"+id, "application/json", `{"md":"# lost","ifMd":"# One\n"}`)
	eq(t, code, 409, "a share changed since is not written over")
	match(t, body, `changed-elsewhere`)

	// the assistant sees what the page saved
	s := &testServer{root: srv.URL}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: base + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	s.session = session
	g := sc(call(t, s, "get_presentation", map[string]any{"deck_id": id}))
	eq(t, g["markdown"], "# One\n\n## Two\n")
	eq(t, len(list(g["images"])), 1)

	// the version head moves only from where it was
	code, body = req(t, "POST", srv.URL+"/api/shares/"+id+"/head", "application/json", `{"expect":null,"head":"h1","entries":[{"id":"h1"}]}`)
	eq(t, code, 200)
	match(t, body, `"ok":true`)
	code, body = req(t, "POST", srv.URL+"/api/shares/"+id+"/head", "application/json", `{"expect":"h0","head":"h2","entries":[{"id":"h2"}]}`)
	match(t, body, `^\{"head":"h1",.*"ok":false\}`)

	code, body = req(t, "GET", srv.URL+"/api/shares", "", "")
	match(t, body, `"id":"`+id+`","name":"Web deck"`)

	code, _ = req(t, "DELETE", srv.URL+"/api/files/shares/"+id+"/media/dot.png", "", "")
	eq(t, code, 200)
	code, _, _ = get(t, srv.URL+"/files/shares/"+id+"/media/dot.png")
	eq(t, code, 404)
	code, _ = req(t, "PUT", srv.URL+"/api/files/shares/"+id+"/../../db/x.json", "text/plain", "x")
	if code < 400 {
		t.Fatal("a file outside the share was taken")
	}

	code, _ = req(t, "DELETE", srv.URL+"/api/shares/"+id, "", "")
	eq(t, code, 204)
	code, _ = req(t, "GET", srv.URL+"/api/shares/"+id, "", "")
	eq(t, code, 404)
	if _, err := os.Stat(filepath.Join(dir, "files", "shares", shard(id), id)); err == nil {
		t.Fatal("the share's files are still there")
	}
}

// a page hears of a deck's change the moment it is written
func TestLocalEvents(t *testing.T) {
	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	res, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	eq(t, res.Header.Get("Content-Type"), "text/event-stream")
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	c := call(t, &testServer{session: session}, "create_presentation", map[string]any{"title": "E", "markdown": "# E\n"})
	id := sc(c)["deck_id"].(string)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("the stream ended")
			}
			if l == `data: {"id":"`+id+`"}` {
				return
			}
		case <-deadline:
			t.Fatal("no event for " + id)
		}
	}
}

// what has expired goes from the folder as Firestore's TTL takes it there:
// a deck with its files; what is still valid, or has no `expires`, stays
func TestFolderExpired(t *testing.T) {
	ctx := context.Background()
	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	ls := srv.Config.Handler.(*localServer)
	db := ls.env.DB
	now := time.Now()
	old, later := now.Add(-time.Hour), now.Add(time.Hour)
	db.Set(ctx, "stats_seen", "a", Doc{"expires": old})
	db.Set(ctx, "stats_seen", "b", Doc{"expires": later})
	db.Set(ctx, "mcp_quota", "c", Doc{"n": 1})
	db.Set(ctx, "shares", "gone1", Doc{"owner": "mcp", "md": "x", "expires": old})
	ls.bucket.Save(ctx, "shares/gone1/media/p.png", "image/png", []byte("png"), nil)
	ls.bucket.Save(ctx, "shares/kept1/media/p.png", "image/png", []byte("png"), nil)
	db.Set(ctx, "shares", "kept1", Doc{"owner": "mcp", "md": "x", "expires": later})
	ls.sweepOnce(now)
	has := func(col, id string) bool {
		d, err := db.Get(ctx, col, id)
		if err != nil {
			t.Fatal(err)
		}
		return d != nil
	}
	eq(t, []bool{has("stats_seen", "a"), has("stats_seen", "b"), has("mcp_quota", "c"), has("shares", "gone1"), has("shares", "kept1")}, []bool{false, true, true, false, true})
	_, errGone := ls.bucket.Read(ctx, "shares/gone1/media/p.png", 10)
	_, errKept := ls.bucket.Read(ctx, "shares/kept1/media/p.png", 10)
	eq(t, []bool{errGone != nil, errKept == nil}, []bool{true, true})
}
