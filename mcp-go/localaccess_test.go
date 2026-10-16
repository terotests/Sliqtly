// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// a request with headers; the client does not follow redirects
func send(t *testing.T, c *http.Client, method, u string, h map[string]string, body string) (*http.Response, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, method, u, strings.NewReader(body))
	for k, v := range h {
		r.Header.Set(k, v)
	}
	if c == nil {
		c = &http.Client{}
	}
	cc := *c
	cc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := cc.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	// an event stream goes on: its headers are enough
	if strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		return res, ""
	}
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

// Once other computers can connect, every request but the few open ones
// needs the server's credential: a bearer token, or the cookie a browser
// gets with it. Nothing of the data says Access-Control-Allow-Origin: *.
func TestLocalAccess(t *testing.T) {
	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	ls := srv.Config.Handler.(*localServer)
	ts := &testServer{root: srv.URL, session: session}

	// a deck, its file and a room's file, made while only this computer
	// connects: no token asked
	code, body := req(t, "POST", srv.URL+"/api/shares", "application/json", `{"name":"Deck","md":"# One\n","theme":"aurora","css":null,"deck":"d1"}`)
	eq(t, code, 201, body)
	id := regexpFind(t, body, `"id":"([A-Za-z0-9]+)"`)
	code, _, _ = do(t, "PUT", srv.URL+"/api/files/shares/"+id+"/media/x.svg", "", "", "image/svg+xml", `<svg xmlns="http://www.w3.org/2000/svg"/>`)
	eq(t, code, 200)
	r := call(t, ts, "create_room", map[string]any{"title": "Plans"})
	if r.IsError {
		t.Fatal(textOf(r))
	}
	room := sc(r)["room_id"].(string)
	code, body = req(t, "PUT", srv.URL+"/api/files/rooms/"+room+"/plan.txt", "text/plain", "the secret plan")
	eq(t, code, 201, body)
	roomFile := "/files/rooms/" + room + "/files/plan.txt"
	code, _ = req(t, "PUT", srv.URL+"/api/settings/listing", "application/json", `{"enabled":true}`)
	eq(t, code, 200)

	// a room's file is this server's pages' only, also without a token
	res, body := send(t, nil, "GET", srv.URL+roomFile, nil, "")
	eq(t, res.StatusCode, 200)
	eq(t, body, "the secret plan")
	eq(t, res.Header.Get("Access-Control-Allow-Origin"), "")
	res, _ = send(t, nil, "GET", srv.URL+roomFile, map[string]string{"Origin": "https://evil.example"}, "")
	eq(t, res.StatusCode, 403)
	for _, p := range []string{"/api/status", "/api/settings", "/api/rooms/list_rooms"} {
		res, _ = send(t, nil, "GET", srv.URL+p, nil, "")
		eq(t, res.Header.Get("Access-Control-Allow-Origin"), "", p)
	}

	// others can connect
	p, _ := newNetPolicy("network", nil, true)
	ls.expo = newExposure(&http.Server{}, freePort(t), p)
	token := ls.secret()
	if len(token) < 30 {
		t.Fatalf("token %q", token)
	}

	type route struct{ method, path, ct, body string }
	protected := []route{
		{"GET", "/", "", ""},
		{"GET", "/decks", "", ""},
		{"GET", "/s/" + id, "", ""},
		{"GET", "/s/" + id + "/overview.jpg", "", ""},
		{"GET", "/api/me", "", ""},
		{"GET", "/api/shares", "", ""},
		{"GET", "/api/shares/" + id, "", ""},
		{"PATCH", "/api/shares/" + id, "application/json", `{"name":"Deck"}`},
		{"GET", "/api/collab/" + id, "", ""},
		{"POST", "/api/rooms/list_rooms", "application/json", `{}`},
		{"POST", "/api/rooms/get_room", "application/json", `{"room_id":"` + room + `"}`},
		{"GET", "/api/events", "", ""},
		{"GET", "/api/settings", "", ""},
		{"GET", "/api/settings/network", "", ""},
		{"GET", "/api/view/" + id, "", ""},
		{"GET", "/files/shares/" + id + "/media/x.svg", "", ""},
		{"GET", roomFile, "", ""},
		{"GET", "/settings", "", ""},
	}
	// no credential, a wrong one, a token in the address that is not it
	for _, c := range protected {
		for _, h := range []map[string]string{
			{},
			{"Authorization": "Bearer not-it"},
			{"Cookie": accessCookieName(&http.Request{Host: strings.TrimPrefix(srv.URL, "http://")}) + "=" + token},
		} {
			h["Content-Type"] = c.ct
			res, body := send(t, nil, c.method, srv.URL+c.path, h, c.body)
			eq(t, res.StatusCode, 401, c.method+" "+c.path+": "+body)
			eq(t, res.Header.Get("Access-Control-Allow-Origin"), "", c.path)
		}
	}
	res, _ = send(t, nil, "GET", srv.URL+"/api/shares?token=nope", nil, "")
	eq(t, res.StatusCode, 401)
	// /mcp and /api/v1 as before: the bearer token
	res, _ = send(t, nil, "POST", srv.URL+"/mcp", map[string]string{"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	eq(t, res.StatusCode, 401)
	res, _ = send(t, nil, "GET", srv.URL+"/api/v1/decks", nil, "")
	eq(t, res.StatusCode, 401)
	// the stream refuses before the upgrade
	ws := "ws://" + strings.TrimPrefix(srv.URL, "http://") + "/api/socket"
	if c, err := websocket.Dial(ws, "", srv.URL); err == nil {
		c.Close()
		t.Fatal("the stream opened without the token")
	}

	// a browser opening a page gets the page that asks for the token
	res, body = send(t, nil, "GET", srv.URL+"/s/"+id+"?x=1", map[string]string{"Accept": "text/html"}, "")
	eq(t, res.StatusCode, 401)
	match(t, body, `<form method="post" action="/access">`)
	match(t, body, `name="next" value="/s/`+id+`\?x=1"`)
	match(t, body, `/access/check`)

	// open still
	for _, p := range []string{"/healthz", "/api/status", "/api/v1/info", "/themes/aurora.css"} {
		res, body = send(t, nil, "GET", srv.URL+p, nil, "")
		eq(t, res.StatusCode, 200, p+": "+body)
	}
	res, _ = send(t, nil, "GET", srv.URL+"/access/check", nil, "")
	eq(t, res.StatusCode, 401)

	// the bearer token opens all of it
	bearer := func(c route) map[string]string {
		return map[string]string{"Authorization": "Bearer " + token, "Content-Type": c.ct, "Accept": "application/json, text/event-stream"}
	}
	for _, c := range protected {
		res, body := send(t, nil, c.method, srv.URL+c.path, bearer(c), c.body)
		eq(t, res.StatusCode, 200, c.method+" "+c.path+": "+body)
		if res.Header.Get("Access-Control-Allow-Origin") == "*" {
			t.Errorf("%s: Access-Control-Allow-Origin: *", c.path)
		}
	}
	res, _ = send(t, nil, "POST", srv.URL+"/mcp", bearer(route{ct: "application/json"}), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	eq(t, res.StatusCode, 200)
	eq(t, res.Header.Get("Access-Control-Allow-Origin"), "")
	res, _ = send(t, nil, "GET", srv.URL+"/api/v1/decks", bearer(route{}), "")
	eq(t, res.StatusCode, 200)
	cfg, _ := websocket.NewConfig(ws, srv.URL)
	cfg.Header.Set("Authorization", "Bearer "+token)
	wc, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("the stream with the token: %v", err)
	}
	wc.Close()

	// a browser: the token in the address once, then the cookie
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar}
	res, _ = send(t, browser, "GET", srv.URL+"/s/"+id+"?token="+url.QueryEscape(token)+"&x=1", nil, "")
	eq(t, res.StatusCode, 303)
	eq(t, res.Header.Get("Location"), "/s/"+id+"?x=1")
	ck := res.Cookies()
	eq(t, len(ck), 1)
	eq(t, []any{ck[0].HttpOnly, ck[0].SameSite, ck[0].Path}, []any{true, http.SameSiteStrictMode, "/"})
	if ck[0].Value == token || strings.Contains(ck[0].Value, token) {
		t.Fatal("the cookie carries the token")
	}
	for _, c := range protected {
		res, body := send(t, browser, c.method, srv.URL+c.path, map[string]string{"Content-Type": c.ct}, c.body)
		eq(t, res.StatusCode, 200, c.method+" "+c.path+" with the cookie: "+body)
		if res.Header.Get("Access-Control-Allow-Origin") == "*" {
			t.Errorf("%s: Access-Control-Allow-Origin: *", c.path)
		}
	}
	res, _ = send(t, browser, "GET", srv.URL+"/access/check", nil, "")
	eq(t, res.StatusCode, 204)
	cfg, _ = websocket.NewConfig(ws, srv.URL)
	for _, c := range jar.Cookies(&url.URL{Scheme: "http", Host: strings.TrimPrefix(srv.URL, "http://")}) {
		cfg.Header.Add("Cookie", c.Name+"="+c.Value)
	}
	wc, err = websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("the stream with the cookie: %v", err)
	}
	wc.Close()
	// the cookie is no bearer token: /mcp and /api/v1 still want that
	res, _ = send(t, browser, "POST", srv.URL+"/mcp", map[string]string{"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	eq(t, res.StatusCode, 401)
	// another site's page has no way in: refused, and told nothing
	res, _ = send(t, browser, "GET", srv.URL+"/api/shares/"+id, map[string]string{"Origin": "https://evil.example"}, "")
	eq(t, res.Header.Get("Access-Control-Allow-Origin"), "")

	// the page's form: a wrong token is refused, the right one hands the cookie
	form := map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": srv.URL}
	res, body = send(t, nil, "POST", srv.URL+"/access", form, "token=nope&next=%2Fdecks")
	eq(t, res.StatusCode, 401)
	match(t, body, `not this server&#39;s access token`)
	res, _ = send(t, nil, "POST", srv.URL+"/access", form, "token="+url.QueryEscape(token)+"&next=%2Fdecks")
	eq(t, res.StatusCode, 303)
	eq(t, res.Header.Get("Location"), "/decks")
	eq(t, len(res.Cookies()), 1)
	// never on to another site
	res, _ = send(t, nil, "POST", srv.URL+"/access", form, "token="+url.QueryEscape(token)+"&next=%2F%2Fevil.example")
	eq(t, res.Header.Get("Location"), "/")
	// nor from one
	res, _ = send(t, nil, "POST", srv.URL+"/access", map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": "https://evil.example"}, "token="+url.QueryEscape(token))
	eq(t, res.StatusCode, 403)
}

// a token set: needed also when only this computer connects
func TestLocalAccessToken(t *testing.T) {
	srv, session := startLocal(t, t.TempDir(), "s3cret")
	defer srv.Close()
	defer session.Close()
	res, _ := send(t, nil, "GET", srv.URL+"/api/shares", nil, "")
	eq(t, res.StatusCode, 401)
	res, _ = send(t, nil, "GET", srv.URL+"/files/rooms/general/files/x.txt", nil, "")
	eq(t, res.StatusCode, 401)
	res, _ = send(t, nil, "GET", srv.URL+"/api/me", map[string]string{"Authorization": "Bearer s3cret"}, "")
	eq(t, res.StatusCode, 200)
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar}
	res, _ = send(t, browser, "GET", srv.URL+"/?token=s3cret", nil, "")
	eq(t, res.StatusCode, 303)
	res, _ = send(t, browser, "GET", srv.URL+"/api/me", nil, "")
	eq(t, res.StatusCode, 200)
	// the server's own token is SLIQTLY_TOKEN: nothing made, nothing kept
	ls := srv.Config.Handler.(*localServer)
	eq(t, ls.secret(), "s3cret")
	eq(t, ls.autoToken, "")
}

// the server's own token is kept in the folder: a restart keeps it, and a
// browser's cookie with it
func TestLocalAccessTokenKept(t *testing.T) {
	dir := t.TempDir()
	a := &localServer{env: &Env{BaseURL: "http://localhost:8080"}, tokenFile: accessTokenPath(dir)}
	b := &localServer{env: &Env{BaseURL: "http://localhost:8080"}, tokenFile: accessTokenPath(dir)}
	t1 := a.secret()
	eq(t, b.secret(), t1)
	eq(t, a.cookieValue(), b.cookieValue())
	c := &localServer{env: &Env{BaseURL: "http://localhost:8080"}, tokenFile: accessTokenPath(t.TempDir())}
	if c.secret() == t1 {
		t.Fatal("two folders, one token")
	}
	st, err := os.Stat(accessTokenPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, st.Mode().Perm(), os.FileMode(0o600))
}
