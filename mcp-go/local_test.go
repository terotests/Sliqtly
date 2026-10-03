package main

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFolderDB(t *testing.T) {
	ctx := context.Background()
	db, _, err := newFSStore(t.TempDir())
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
	_, b, _ := newFSStore(root)
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
	srv.Config.Handler = newLocalServer(e, bucket, token)
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
	match(t, textOf(c), `Saved in the Sliqtly account of local`)

	// kept in the folder, pictures served by the server itself
	if _, err := os.Stat(filepath.Join(dir, "db", "shares", id+".json")); err != nil {
		t.Fatal(err)
	}
	share, _ := (&fsDB{root: filepath.Join(dir, "db")}).Get(context.Background(), "shares", id)
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
	code, _, body = get(t, srv.URL+"/")
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
	u := call(t, s2, "update_presentation", map[string]any{"deck_id": id, "edit_key": out["edit_key"], "markdown": DECK + "\n## More\n\nText.\n"})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	eq(t, sc(u)["slides"], 3)
	match(t, textOf(call(t, s2, "list_presentations", map[string]any{})), `Pilot \(`+id+`\)`)
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
