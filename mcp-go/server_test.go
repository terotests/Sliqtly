// The server end to end over Streamable HTTP, with Firestore, Storage and the
// network replaced by fakes: mcp/test/server.test.js, case for case.

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const BASE = "https://sliqtly.test"

var PNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")

const DECK = "# Hello\n\nFirst.\n{.lead}\n\n## Cat {bg=media/cat.png}\n\n![](media/dot.png)\n\n```mermaid\nflowchart LR\n## not a slide\n```\n"

// --- fakes

type fakeDB struct {
	mu    sync.Mutex
	data  map[string]Doc
	clock int64
}

func newFakeDB() *fakeDB { return &fakeDB{data: map[string]Doc{}, clock: 1000} }

// a round trip through JSON, as a stored document would be
func clone(d Doc) Doc {
	b, _ := json.Marshal(d)
	var out Doc
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	dec.Decode(&out)
	return fixNumbers(out).(Doc)
}

func fixNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		for k, e := range x {
			x[k] = fixNumbers(e)
		}
	case []any:
		for i, e := range x {
			x[i] = fixNumbers(e)
		}
	}
	return v
}

func (f *fakeDB) Get(_ context.Context, col, id string) (Doc, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.data[col+"/"+id]
	if !ok {
		return nil, nil
	}
	return clone(d), nil
}
func (f *fakeDB) Set(_ context.Context, col, id string, d Doc) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[col+"/"+id] = clone(d)
	return nil
}
func (f *fakeDB) Update(_ context.Context, col, id string, d Doc) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur := f.data[col+"/"+id]
	for k, v := range clone(d) {
		cur[k] = v
	}
	return nil
}
func (f *fakeDB) Delete(_ context.Context, col, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, col+"/"+id)
	return nil
}
func (f *fakeDB) WhereEq(_ context.Context, col, field string, value any) ([]Doc, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	docs, ids := []Doc{}, []string{}
	for k, v := range f.data {
		if strings.HasPrefix(k, col+"/") && v[field] == value {
			docs = append(docs, clone(v))
			ids = append(ids, k[len(col)+1:])
		}
	}
	return docs, ids, nil
}
func (f *fakeDB) ServerTime() any {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clock++
	return f.clock
}
func (f *fakeDB) doc(k string) Doc {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.data[k]
}

type savedFile struct {
	data        []byte
	contentType string
}

type fakeBucket struct {
	mu    sync.Mutex
	saved map[string]savedFile
}

func (b *fakeBucket) Name() string { return "bucket.test" }
func (b *fakeBucket) Save(_ context.Context, path, ct string, data []byte, _ map[string]string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.saved[path] = savedFile{data, ct}
	return nil
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func respond(status int, ct, body string) *http.Response {
	h := http.Header{}
	if ct != "" {
		h.Set("content-type", ct)
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

var fakeNet = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
	switch r.URL.String() {
	case BASE + "/themes/aurora.css":
		return respond(200, "text/css", "page { background-color: #0b1030; }"), nil
	case BASE + "/themes/corporate.css":
		return respond(200, "text/css", "page { background-color: #fff; }"), nil
	case "https://images.test/cat.png":
		return respond(200, "image/png", string(PNG)), nil
	case "https://client.test/meta.json":
		return respond(200, "application/json", `{"client_id":"https://client.test/meta.json","client_name":"Test Client","redirect_uris":["https://client.test/cb"]}`), nil
	case "https://images.test/page.html":
		return respond(200, "text/html", "<html>"), nil
	}
	return respond(404, "", "no"), nil
})}

type fb struct {
	db     *fakeDB
	bucket *fakeBucket
	store  *FirebaseStore
}

func fakeFirebase() fb {
	db := newFakeDB()
	b := &fakeBucket{saved: map[string]savedFile{}}
	return fb{db, b, &FirebaseStore{DB: db, Bucket: b}}
}

type testServer struct {
	root    string
	session *mcp.ClientSession
	close   func()
}

type withToken struct {
	token string
	next  http.RoundTripper
}

func (w withToken) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("authorization", "Bearer "+w.token)
	return w.next.RoundTrip(r)
}

func start(t *testing.T, store Store, limiter func(string) string, oauth *OAuth, token string) *testServer {
	t.Helper()
	app := NewApp(AppOpts{Store: store, BaseURL: BASE, Client: fakeNet, ThemeClient: fakeNet, Limiter: limiter, OAuth: oauth})
	srv := httptest.NewServer(app)
	hc := &http.Client{}
	if token != "" {
		hc.Transport = withToken{token, http.DefaultTransport}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: hc}, nil)
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	return &testServer{root: srv.URL, session: session, close: func() { session.Close(); srv.Close() }}
}

func call(t *testing.T, s *testServer, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	r, err := s.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return r
}

func textOf(r *mcp.CallToolResult) string { return r.Content[0].(*mcp.TextContent).Text }

func sc(r *mcp.CallToolResult) map[string]any {
	b, _ := json.Marshal(r.StructuredContent)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

func eq(t *testing.T, got, want any, msg ...any) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Fatalf("%v: got %s, want %s", msg, g, w)
	}
}

func match(t *testing.T, s, re string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(s) {
		t.Fatalf("%q does not match %s", s, re)
	}
}

// --- the cases

func TestToolsUIMetadataAndPreview(t *testing.T) {
	s := start(t, fakeFirebase().store, nil, nil, "")
	defer s.close()
	ctx := context.Background()
	tools, err := s.session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	var create *mcp.Tool
	for _, x := range tools.Tools {
		names = append(names, x.Name)
		if x.Name == "create_presentation" {
			create = x
		}
	}
	sort.Strings(names)
	eq(t, names, []string{"create_presentation", "get_presentation", "list_presentations", "sliqtly_guide", "update_presentation"})
	eq(t, create.Meta["ui"].(map[string]any)["resourceUri"], PREVIEW_URI)
	eq(t, create.Meta["openai/outputTemplate"], PREVIEW_URI)
	schema, _ := json.Marshal(create.InputSchema)
	match(t, string(schema), `"enum":\[[^\]]*"editorial"`)
	r, err := s.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: PREVIEW_URI})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, r.Contents[0].MIMEType, "text/html;profile=mcp-app")
	match(t, r.Contents[0].Text, `ui/initialize`)
	eq(t, r.Contents[0].Meta["ui"].(map[string]any)["csp"].(map[string]any)["frameDomains"], []string{BASE, "https://sliqtly.com", "https://sliqtly.web.app"})
	// Claude frames nothing but blob:, so the preview loads the viewer itself
	uiCSP := r.Contents[0].Meta["ui"].(map[string]any)["csp"].(map[string]any)
	match(t, fmt.Sprint(uiCSP["resourceDomains"]), `https://www\.gstatic\.com`)
	match(t, fmt.Sprint(uiCSP["connectDomains"]), `https://firestore\.googleapis\.com`)
	eq(t, r.Contents[0].Meta["openai/widgetCSP"].(map[string]any)["connect_domains"], uiCSP["connectDomains"])
	match(t, textOf(call(t, s, "sliqtly_guide", map[string]any{})), `## Pictures`)
}

func TestCreateUpdateReadWithPictures(t *testing.T) {
	f := fakeFirebase()
	s := start(t, f.store, nil, nil, "")
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Cats", "markdown": DECK, "css": "h1 { font-size: 60pt; }",
		"images": []any{
			map[string]any{"name": "cat.png", "url": "https://images.test/cat.png"},
			map[string]any{"name": "dot.png", "data_base64": "data:image/png;base64," + base64.StdEncoding.EncodeToString(PNG)},
		},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	out := sc(c)
	id := out["deck_id"].(string)
	eq(t, out["slides"], 2)
	eq(t, out["warnings"], []string{})
	eq(t, out["share_url"], BASE+"/s/"+id)
	eq(t, out["edit_url"], BASE+"/s/"+id+"?edit")
	share := f.db.doc("shares/" + id)
	eq(t, share["source"], "mcp")
	eq(t, share["theme"], "aurora")
	match(t, share["css"].(string), `#0b1030[\s\S]*font-size: 60pt`)
	files := list(share["files"])
	eq(t, []any{mapOf(files[0])["path"], mapOf(files[1])["path"]}, []string{"media/cat.png", "media/dot.png"})
	match(t, str(mapOf(files[0])["url"]), `^https://firebasestorage\.googleapis\.com/v0/b/bucket\.test/o/shares%2F.*%2Fmedia%2Fcat\.png\?alt=media&token=`)
	eq(t, f.bucket.saved["shares/"+id+"/media/cat.png"].contentType, "image/png")
	if f.db.doc("mcp_keys/" + id)["hash"] == out["edit_key"] {
		t.Fatal("the key is stored as is")
	}

	bad := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edit_key": "wrong", "markdown": "# x"})
	if !bad.IsError {
		t.Fatal("a wrong key changed the deck")
	}
	eq(t, f.db.doc("shares/" + id)["md"], DECK)

	u := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edit_key": out["edit_key"], "markdown": DECK + "\n## More\n\n![](media/new.png)\n", "theme": "corporate"})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	uo := sc(u)
	eq(t, uo["share_url"], out["share_url"])
	eq(t, uo["slides"], 3)
	eq(t, uo["warnings"], []string{"media/new.png is used in the Markdown but no image by that name was sent."})
	after := f.db.doc("shares/" + id)
	eq(t, after["theme"], "corporate")
	eq(t, after["css"], nil, "a new theme without css drops the old theme's sheet")
	eq(t, len(list(after["files"])), 2)

	g := sc(call(t, s, "get_presentation", map[string]any{"deck_id": id}))
	eq(t, g["markdown"], after["md"])
	imgs := list(g["images"])
	eq(t, []any{mapOf(imgs[0])["name"], mapOf(imgs[1])["name"]}, []string{"cat.png", "dot.png"})
}

func TestRefusesWhatItShouldNotFetchOrStore(t *testing.T) {
	s := start(t, fakeFirebase().store, nil, nil, "")
	defer s.close()
	for _, c := range []struct {
		img map[string]any
		why string
	}{
		{map[string]any{"name": "a.png", "url": "http://images.test/cat.png"}, `public https`},
		{map[string]any{"name": "a.png", "url": "https://169.254.169.254/x"}, `public https`},
		{map[string]any{"name": "a.png", "url": "https://localhost/x"}, `public https`},
		{map[string]any{"name": "a.png", "url": "https://images.test/page.html"}, `not a picture`},
		{map[string]any{"name": "../a.png", "url": "https://images.test/cat.png"}, `not usable`},
		{map[string]any{"name": "a.bmp", "data_base64": "AAAA"}, `unknown picture type`},
		{map[string]any{"name": "a.png"}, `url or data_base64`},
	} {
		r := call(t, s, "create_presentation", map[string]any{"title": "x", "markdown": "# x", "images": []any{c.img}})
		if !r.IsError {
			t.Fatalf("%v was accepted", c.img)
		}
		match(t, textOf(r), c.why)
	}
	if !call(t, s, "create_presentation", map[string]any{"title": "x", "markdown": "  "}).IsError {
		t.Fatal("empty markdown was accepted")
	}
}

func TestPublicClientRefusesPrivateAddresses(t *testing.T) {
	// the dialer itself refuses, whatever name led to the address
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(PNG) }))
	defer srv.Close()
	_, err := newPublicClient().Get(srv.URL + "/x")
	if err == nil || !strings.Contains(err.Error(), "refusing to connect") {
		t.Fatalf("connected to a private address: %v", err)
	}
}

func TestDeckTravelsInTheLinkWithoutCloudStorage(t *testing.T) {
	s := start(t, LinkStore{}, nil, nil, "")
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{"title": "x", "markdown": DECK, "theme": "ember"})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	out := sc(c)
	u, _ := url.Parse(out["share_url"].(string))
	q, _ := url.ParseQuery(u.Fragment)
	md, err := unpackText(q.Get("md"))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, md, DECK)
	eq(t, q.Get("theme"), "ember")
	eq(t, q.Get("mode"), "show")
	if strings.Contains(out["edit_url"].(string), "mode=show") {
		t.Fatal("edit_url opens the show")
	}
}

func TestRateLimitAndBrowserVisit(t *testing.T) {
	s := start(t, fakeFirebase().store, rateLimiter(1, 10*time.Minute), nil, "")
	defer s.close()
	if call(t, s, "create_presentation", map[string]any{"title": "x", "markdown": "# x"}).IsError {
		t.Fatal("first create refused")
	}
	match(t, textOf(call(t, s, "create_presentation", map[string]any{"title": "x", "markdown": "# x"})), `Too many`)
	if call(t, s, "sliqtly_guide", map[string]any{}).IsError {
		t.Fatal("reading is limited")
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := noRedirect.Get(s.root + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, res.StatusCode, 302)
	eq(t, res.Header.Get("location"), BASE+"/connect.html")
}

func TestOptionalSignIn(t *testing.T) {
	f := fakeFirebase()
	oauth := &OAuth{DB: f.db, Client: fakeNet, VerifyIDToken: func(_ context.Context, tok string) (*IDToken, error) {
		if tok != "google-ok" {
			return nil, fmt.Errorf("bad")
		}
		return &IDToken{UID: "u1", Name: "Tero"}, nil
	}}
	anon := start(t, f.store, nil, oauth, "")
	defer anon.close()
	root := anon.root
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	getJSON := func(u string) map[string]any {
		res, err := http.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var m map[string]any
		json.NewDecoder(res.Body).Decode(&m)
		return m
	}
	post := func(path string, ct string, body string) (int, map[string]any) {
		res, err := http.Post(root+path, ct, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var m map[string]any
		json.NewDecoder(res.Body).Decode(&m)
		return res.StatusCode, m
	}
	jsonPost := func(path string, v any) (int, map[string]any) {
		b, _ := json.Marshal(v)
		return post(path, "application/json", string(b))
	}
	formPost := func(path string, v map[string]string) map[string]any {
		q := url.Values{}
		for k, x := range v {
			q.Set(k, x)
		}
		_, m := post(path, "application/x-www-form-urlencoded", q.Encode())
		return m
	}

	eq(t, getJSON(root + "/.well-known/oauth-protected-resource/mcp")["resource"], BASE+"/mcp")
	asm := getJSON(root + "/.well-known/oauth-authorization-server")
	eq(t, asm["token_endpoint"], BASE+"/oauth/token")
	eq(t, asm["client_id_metadata_document_supported"], true)

	// anonymous use still works, and listing asks for sign-in
	a := call(t, anon, "create_presentation", map[string]any{"title": "anon", "markdown": "# a"})
	if a.IsError {
		t.Fatal(textOf(a))
	}
	anonID := sc(a)["deck_id"].(string)
	eq(t, f.db.doc("shares/" + anonID)["owner"], "mcp")
	l := call(t, anon, "list_presentations", map[string]any{})
	if !l.IsError {
		t.Fatal("listing without sign-in")
	}
	match(t, fmt.Sprint(l.Meta["mcp/www_authenticate"]), `resource_metadata=`)

	_, badReg := jsonPost("/oauth/register", map[string]any{"redirect_uris": []string{"http://evil.test/cb"}})
	eq(t, badReg["error"], "invalid_redirect_uri")
	st, reg := jsonPost("/oauth/register", map[string]any{"client_name": "Claude", "redirect_uris": []string{"http://127.0.0.1:5000/cb"}})
	eq(t, st, 201)
	clientID := reg["client_id"].(string)

	verifier := strings.Repeat("v", 50)
	challenge := s256(verifier)
	authorize := func(cid, redirect string) *url.URL {
		q := url.Values{"response_type": {"code"}, "client_id": {cid}, "redirect_uri": {redirect}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"st"}, "resource": {BASE + "/mcp"}}
		res, err := noRedirect.Get(root + "/oauth/authorize?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.StatusCode, 302)
		u, _ := url.Parse(res.Header.Get("location"))
		return u
	}
	// a loopback redirect may use another port
	page := authorize(clientID, "http://127.0.0.1:6123/cb")
	eq(t, page.Scheme+"://"+page.Host+page.Path, BASE+"/oauth.html")
	eq(t, page.Query().Get("client"), "Claude")
	eq(t, page.Query().Get("to"), "this computer")
	request := page.Query().Get("request")

	wrong, _ := jsonPost("/oauth/approve", map[string]any{"request": request, "id_token": "nope"})
	eq(t, wrong, 401)
	// the request survives a failed Google check
	_, ok := jsonPost("/oauth/approve", map[string]any{"request": request, "id_token": "google-ok"})
	back, _ := url.Parse(ok["redirect"].(string))
	eq(t, back.Query().Get("state"), "st")
	eq(t, back.Query().Get("iss"), BASE)
	code := back.Query().Get("code")
	used, _ := jsonPost("/oauth/approve", map[string]any{"request": request, "id_token": "google-ok"})
	eq(t, used, 400, "a request gives one code")

	noPkce := formPost("/oauth/token", map[string]string{"grant_type": "authorization_code", "code": code, "client_id": clientID, "redirect_uri": "http://127.0.0.1:6123/cb", "code_verifier": strings.Repeat("x", 50)})
	eq(t, noPkce["error"], "invalid_grant")
	// the code is single-use: the failed attempt spent it
	page3 := authorize(clientID, "http://127.0.0.1:6123/cb")
	_, ok3 := jsonPost("/oauth/approve", map[string]any{"request": page3.Query().Get("request"), "id_token": "google-ok"})
	b3, _ := url.Parse(ok3["redirect"].(string))
	code3 := b3.Query().Get("code")
	tok := formPost("/oauth/token", map[string]string{"grant_type": "authorization_code", "code": code3, "client_id": clientID, "redirect_uri": "http://127.0.0.1:6123/cb", "code_verifier": verifier})
	eq(t, tok["token_type"], "Bearer")
	again := formPost("/oauth/token", map[string]string{"grant_type": "authorization_code", "code": code3, "client_id": clientID, "redirect_uri": "http://127.0.0.1:6123/cb", "code_verifier": verifier})
	eq(t, again["error"], "invalid_grant")

	// signed in: the deck is the user's, changed without an edit key, listed
	me := start(t, f.store, nil, oauth, tok["access_token"].(string))
	c := call(t, me, "create_presentation", map[string]any{"title": "Mine", "markdown": "# m"})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	match(t, textOf(c), `account of Tero`)
	id := sc(c)["deck_id"].(string)
	eq(t, f.db.doc("shares/" + id)["owner"], "u1")
	u := call(t, me, "update_presentation", map[string]any{"deck_id": id, "markdown": "# m\n\n## two"})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	eq(t, sc(u)["slides"], 2)
	match(t, textOf(call(t, me, "update_presentation", map[string]any{"deck_id": anonID, "markdown": "# x"})), `edit_key is needed`)
	lst := list(sc(call(t, me, "list_presentations", map[string]any{}))["presentations"])
	eq(t, len(lst), 1)
	eq(t, mapOf(lst[0])["deck_id"], id)
	me.close()
	if !call(t, anon, "update_presentation", map[string]any{"deck_id": id, "markdown": "# x"}).IsError {
		t.Fatal("anonymous changed a signed-in user's deck")
	}

	// a token that does not hold: 401 with where to sign in
	req, _ := http.NewRequest("POST", root+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/event-stream")
	req.Header.Set("authorization", "Bearer nope")
	r401, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, r401.StatusCode, 401)
	match(t, r401.Header.Get("www-authenticate"), `resource_metadata="https://sliqtly\.test/\.well-known/oauth-protected-resource/mcp"`)

	// refresh rotates
	r1 := formPost("/oauth/token", map[string]string{"grant_type": "refresh_token", "refresh_token": tok["refresh_token"].(string), "client_id": clientID})
	if r1["access_token"] == nil || r1["refresh_token"] == tok["refresh_token"] {
		t.Fatalf("refresh did not rotate: %v", r1)
	}
	r2 := formPost("/oauth/token", map[string]string{"grant_type": "refresh_token", "refresh_token": tok["refresh_token"].(string), "client_id": clientID})
	eq(t, r2["error"], "invalid_grant")

	// a client known by its metadata document URL
	cimd := authorize("https://client.test/meta.json", "https://client.test/cb")
	eq(t, cimd.Query().Get("client"), "Test Client")
	eq(t, cimd.Query().Get("to"), "client.test")
	q := url.Values{"response_type": {"code"}, "client_id": {"https://client.test/meta.json"}, "redirect_uri": {"https://evil.test/cb"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	wr, _ := noRedirect.Get(root + "/oauth/authorize?" + q.Encode())
	eq(t, wr.StatusCode, 400)
	// denying sends the client an error
	_, den := jsonPost("/oauth/approve", map[string]any{"request": cimd.Query().Get("request"), "deny": true})
	dr, _ := url.Parse(den["redirect"].(string))
	eq(t, dr.Query().Get("error"), "access_denied")
}

func TestOutline(t *testing.T) {
	titles, media := outline(DECK)
	eq(t, titles, []string{"Hello", "Cat"})
	eq(t, media, []string{"cat.png", "dot.png"})
	eq(t, cleanName("team photo.JPG"), "team-photo.jpg")
	eq(t, cleanName("../a.png"), "")
	eq(t, encodeURIComponent("shares/a b/media/x(1).png"), "shares%2Fa%20b%2Fmedia%2Fx(1).png")
}
