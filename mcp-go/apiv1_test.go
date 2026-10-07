// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// a server of one's own on a folder, set up by configure before it starts
func startV1(t *testing.T, token string, configure func(*localServer)) (*httptest.Server, *localServer) {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	base := "http://" + srv.Listener.Addr().String()
	e, bucket, err := localEnv(t.TempDir(), base, "local")
	if err != nil {
		t.Fatal(err)
	}
	e.Client = fakeNet
	ls := newLocalServer(e, bucket, token, nil).(*localServer)
	if configure != nil {
		configure(ls)
	}
	srv.Config.Handler = ls
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, ls
}

type v1Answer struct {
	status int
	header http.Header
	body   map[string]any
	raw    string
}

// one request: body is JSON when not nil; headers as name, value pairs
func v1Do(t *testing.T, method, url string, body any, headers ...string) v1Answer {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	a := v1Answer{status: res.StatusCode, header: res.Header, raw: string(raw)}
	json.Unmarshal(raw, &a.body)
	return a
}

func (a v1Answer) want(t *testing.T, status int) v1Answer {
	t.Helper()
	if a.status != status {
		t.Fatalf("status %d, want %d: %s", a.status, status, a.raw)
	}
	return a
}

func (a v1Answer) str(k string) string {
	s, _ := a.body[k].(string)
	return s
}

// create, list, read, save, a save on an old version, the view, delete
func TestV1Decks(t *testing.T) {
	srv, _ := startV1(t, "", nil)
	api := srv.URL + "/api/v1"

	info := v1Do(t, "GET", api+"/info", nil).want(t, 200)
	eq(t, info.body["api"], 1.0)
	eq(t, info.body["auth"].(map[string]any)["required"], false)

	md := "# One\n\nHello\n\n# Two\n\nWorld\n"
	made := v1Do(t, "POST", api+"/decks", map[string]any{"name": "Plan", "markdown": md}).want(t, 201)
	id, v1 := made.str("id"), made.str("version")
	if id == "" || v1 == "" {
		t.Fatalf("created: %s", made.raw)
	}
	eq(t, made.str("markdown"), md)
	eq(t, made.str("room"), "general", "a new deck is in General")
	eq(t, made.str("theme"), "aurora")

	list := v1Do(t, "GET", api+"/decks", nil).want(t, 200)
	decks := list.body["decks"].([]any)
	eq(t, len(decks), 1)
	row := decks[0].(map[string]any)
	eq(t, row["id"], id)
	eq(t, row["name"], "Plan")
	eq(t, row["slides"], 2.0)

	got := v1Do(t, "GET", api+"/decks/"+id, nil).want(t, 200)
	eq(t, got.header.Get("ETag"), `"`+v1+`"`)
	eq(t, got.str("version"), v1)

	saved := v1Do(t, "PUT", api+"/decks/"+id, map[string]any{"markdown": md + "\n# Three\n", "ifVersion": v1}).want(t, 200)
	v2 := saved.str("version")
	if v2 == v1 {
		t.Fatal("a save kept the version")
	}
	eq(t, saved.str("name"), "Plan", "a save keeps what it does not name")

	// the old /api reads what /api/v1 wrote: one store
	old := v1Do(t, "GET", srv.URL+"/api/shares/"+id, nil).want(t, 200)
	eq(t, old.str("md"), md+"\n# Three\n")

	stale := v1Do(t, "PUT", api+"/decks/"+id, map[string]any{"markdown": "lost", "ifVersion": v1}).want(t, 409)
	eq(t, stale.str("code"), "conflict")
	cur := stale.body["current"].(map[string]any)
	eq(t, cur["version"], v2)
	eq(t, cur["markdown"], md+"\n# Three\n")
	v1Do(t, "PUT", api+"/decks/"+id, map[string]any{"name": "x"}, "If-Match", `"`+v1+`"`).want(t, 409)
	v1Do(t, "PUT", api+"/decks/"+id, map[string]any{"name": "Plan B"}, "If-Match", `"`+v2+`"`).want(t, 200)

	view := v1Do(t, "GET", api+"/decks/"+id+"/view", nil).want(t, 200)
	if _, ok := view.body["lists"]; !ok {
		t.Fatalf("view: %.300s", view.raw)
	}

	// a write is JSON
	req, _ := http.NewRequest("POST", api+"/decks", strings.NewReader("name=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	eq(t, res.StatusCode, 415)

	v1Do(t, "DELETE", api+"/decks/"+id, nil).want(t, 204)
	gone := v1Do(t, "GET", api+"/decks/"+id, nil).want(t, 404)
	eq(t, gone.str("code"), "not_found")
	v1Do(t, "GET", api+"/nothing", nil).want(t, 404)
}

// The server's token on /api/v1 and /mcp
func TestV1Token(t *testing.T) {
	srv, _ := startV1(t, "s3cret", nil)
	api := srv.URL + "/api/v1"
	info := v1Do(t, "GET", api+"/info", nil).want(t, 200)
	auth := info.body["auth"].(map[string]any)
	eq(t, auth["required"], true)
	eq(t, auth["token"], true)
	eq(t, auth["oauth"], false)

	none := v1Do(t, "GET", api+"/decks", nil).want(t, 401)
	eq(t, none.str("code"), "unauthorized")
	if !strings.HasPrefix(none.header.Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("WWW-Authenticate: %q", none.header.Get("WWW-Authenticate"))
	}
	wrong := v1Do(t, "GET", api+"/decks", nil, "Authorization", "Bearer nope").want(t, 401)
	if !strings.Contains(wrong.header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatalf("WWW-Authenticate: %q", wrong.header.Get("WWW-Authenticate"))
	}
	v1Do(t, "GET", api+"/decks", nil, "Authorization", "Basic s3cret").want(t, 401)
	v1Do(t, "GET", api+"/decks", nil, "Authorization", "Bearer s3cret").want(t, 200)
	me := v1Do(t, "GET", api+"/me", nil, "Authorization", "bearer s3cret").want(t, 200)
	eq(t, me.str("id"), "local")

	// /mcp: refused without it, a client with it works
	res, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	eq(t, res.StatusCode, 401)
	mcpWorks(t, srv.URL, "s3cret")
}

// an MCP client with token lists the tools
func mcpWorks(t *testing.T, base, token string) {
	t.Helper()
	hc := &http.Client{Transport: withToken{token, http.DefaultTransport}}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: base + "/mcp", HTTPClient: hc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("no tools")
	}
}

// Pages of other origins: only those on the list, and with no sign-in
// only pages on this computer
func TestV1CORS(t *testing.T) {
	open, _ := startV1(t, "", func(ls *localServer) {
		ls.cors, _ = newCORSPolicy([]string{"https://editor.example.com"})
	})
	api := open.URL + "/api/v1"
	v1Do(t, "GET", api+"/decks", nil, "Origin", "https://evil.example").want(t, 403)
	// on the list, but the server has no sign-in: not for a page elsewhere
	v1Do(t, "GET", api+"/decks", nil, "Origin", "https://editor.example.com").want(t, 403)
	info := v1Do(t, "GET", api+"/info", nil, "Origin", "https://editor.example.com").want(t, 200)
	eq(t, info.header.Get("Access-Control-Allow-Origin"), "https://editor.example.com")
	local := v1Do(t, "POST", api+"/decks", map[string]any{"name": "x", "markdown": "# x"}, "Origin", "http://localhost:5173").want(t, 201)
	eq(t, local.header.Get("Access-Control-Allow-Origin"), "http://localhost:5173")
	// the old /api keeps its rule: no writes from another origin
	v1Do(t, "POST", open.URL+"/api/shares", map[string]any{"name": "x"}, "Origin", "http://localhost:5173").want(t, 403)

	srv, _ := startV1(t, "tok", func(ls *localServer) {
		ls.cors, _ = newCORSPolicy([]string{"https://editor.example.com"})
	})
	api = srv.URL + "/api/v1"
	pre := v1Do(t, "OPTIONS", api+"/decks/abcdef123", nil, "Origin", "https://editor.example.com", "Access-Control-Request-Method", "PUT", "Access-Control-Request-Headers", "authorization, content-type").want(t, 204)
	eq(t, pre.header.Get("Access-Control-Allow-Origin"), "https://editor.example.com")
	for _, h := range []string{"Authorization", "Content-Type"} {
		if !strings.Contains(pre.header.Get("Access-Control-Allow-Headers"), h) {
			t.Fatalf("Allow-Headers %q lacks %s", pre.header.Get("Access-Control-Allow-Headers"), h)
		}
	}
	if !strings.Contains(pre.header.Get("Access-Control-Allow-Methods"), "PUT") {
		t.Fatalf("Allow-Methods %q", pre.header.Get("Access-Control-Allow-Methods"))
	}
	v1Do(t, "OPTIONS", api+"/decks", nil, "Origin", "https://evil.example", "Access-Control-Request-Method", "GET").want(t, 403)
	got := v1Do(t, "GET", api+"/decks", nil, "Origin", "https://editor.example.com", "Authorization", "Bearer tok").want(t, 200)
	eq(t, got.header.Get("Access-Control-Allow-Origin"), "https://editor.example.com")
	if !strings.Contains(got.header.Get("Access-Control-Expose-Headers"), "ETag") {
		t.Fatalf("Expose-Headers %q", got.header.Get("Access-Control-Expose-Headers"))
	}
	// a page on the list still needs the token
	v1Do(t, "GET", api+"/decks", nil, "Origin", "https://editor.example.com").want(t, 401)
}

func TestCORSPolicy(t *testing.T) {
	c, err := newCORSPolicy([]string{"https://Editor.Example.com:443", "http://10.0.0.5:8080"})
	if err != nil {
		t.Fatal(err)
	}
	for o, want := range map[string]bool{
		"https://editor.example.com":      true,
		"http://editor.example.com":       false,
		"http://10.0.0.5:8080":            true,
		"http://10.0.0.5:8081":            false,
		"http://localhost:3000":           true,
		"https://127.0.0.1:9":             true,
		"http://[::1]:5173":               true,
		"null":                            false,
		"":                                false,
		"https://editor.example.com.evil": false,
	} {
		if c.allowed(o) != want {
			t.Errorf("%q: %v", o, !want)
		}
	}
	for _, bad := range []string{"editor.example.com", "https://x.com/path", "ftp://x.com"} {
		if _, err := newCORSPolicy([]string{bad}); err == nil {
			t.Errorf("%q taken", bad)
		}
	}
}
