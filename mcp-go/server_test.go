// The server end to end over Streamable HTTP, with Firestore, Storage and the
// network replaced by fakes: mcp/test/server.test.js, case for case.

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const BASE = "https://sliqtly.test"

// a store for cases that do not look at it
var fb0 = fakeFirebase()

var PNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")

// the deck's mermaid fence draws nothing, and the editor's own model says so
const EMPTY_FLOW = "The mermaid block on slide \"Cat\" is not shown: a flowchart diagram — read, but nothing drew it. → topic=diagrams"

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
func (f *fakeDB) Take(_ context.Context, col, id string) (Doc, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.data[col+"/"+id]
	if !ok {
		return nil, nil
	}
	delete(f.data, col+"/"+id)
	return clone(d), nil
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
func (f *fakeDB) Create(_ context.Context, col, id string, d Doc) (Doc, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if had, ok := f.data[col+"/"+id]; ok {
		return clone(had), nil
	}
	f.data[col+"/"+id] = clone(d)
	return nil, nil
}
func (f *fakeDB) UpdateIf(_ context.Context, col, id, field, want string, d Doc) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur := f.data[col+"/"+id]
	if fieldText(cur, field) != want {
		return false, nil
	}
	if cur == nil {
		cur = Doc{}
		f.data[col+"/"+id] = cur
	}
	for k, v := range clone(d) {
		cur[k] = v
	}
	return true, nil
}
func (f *fakeDB) Increment(_ context.Context, col, id string, add Doc) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var inc func(cur, add Doc) Doc
	inc = func(cur, add Doc) Doc {
		if cur == nil {
			cur = Doc{}
		}
		for k, v := range add {
			if sub, ok := v.(map[string]any); ok {
				had, _ := cur[k].(map[string]any)
				cur[k] = inc(had, sub)
			} else {
				n, _ := cur[k].(int64)
				cur[k] = n + v.(int64)
			}
		}
		return cur
	}
	f.data[col+"/"+id] = inc(f.data[col+"/"+id], add)
	return nil
}
func (f *fakeDB) doc(k string) Doc {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.data[k]
}

// every document under a prefix ("col/"), by its key
func (f *fakeDB) all(prefix string) map[string]Doc {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]Doc{}
	for k, d := range f.data {
		if strings.HasPrefix(k, prefix) {
			out[k] = d
		}
	}
	return out
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
func (b *fakeBucket) RemovePrefix(_ context.Context, prefix string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for p := range b.saved {
		if strings.HasPrefix(p, prefix) {
			delete(b.saved, p)
		}
	}
	return nil
}
func (b *fakeBucket) Read(_ context.Context, path string, limit int64) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f, ok := b.saved[path]
	if !ok {
		return nil, fmt.Errorf("storage: object doesn't exist")
	}
	if int64(len(f.data)) > limit {
		return f.data[:limit], nil
	}
	return f.data, nil
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

// the site's own sheet, so the checks lay the deck out as the player does
var auroraCSS = func() string {
	b, err := os.ReadFile("../themes/aurora.css")
	if err != nil {
		panic(err)
	}
	return string(b)
}()

var fakeNet = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
	switch r.URL.String() {
	case BASE + "/themes/aurora.css":
		return respond(200, "text/css", auroraCSS), nil
	case BASE + "/themes/corporate.css":
		return respond(200, "text/css", "page { background-color: #fff; }\ndeck { split-level: 2; }"), nil
	case "https://images.test/cat.png":
		return respond(200, "image/png", string(PNG)), nil
	case "https://client.test/meta.json":
		return respond(200, "application/json", `{"client_id":"https://client.test/meta.json","client_name":"Test Client","redirect_uris":["https://client.test/cb"]}`), nil
	case BASE + "/__/firebase/init.json":
		return respond(200, "application/json", `{"projectId":"sliqtly-test","apiKey":"k"}`), nil
	case "https://images.test/page.html":
		return respond(200, "text/html", "<html>"), nil
	case "https://images.test/favicon.ico":
		return respond(200, "image/x-icon", "\x00\x00\x01\x00"), nil
	}
	return respond(404, "", "no"), nil
})}

type fb struct {
	db     *fakeDB
	bucket *fakeBucket
}

func fakeFirebase() fb {
	return fb{newFakeDB(), &fakeBucket{saved: map[string]savedFile{}}}
}

// the server with Firestore and Storage faked; nothing kept when f is nil
func testEnv(f *fb, limiter func(string) string) *Env {
	e := &Env{BaseURL: BASE, Client: fakeNet, ThemeClient: fakeNet, Limiter: limiter}
	if f != nil {
		e.DB, e.Bucket = f.db, f.bucket
	}
	return e
}

// signed in as u1 without the OAuth dance: an access token kept as the
// server keeps one
func signIn(f fb) string {
	tok := "test-access-token"
	sum := sha256.Sum256([]byte(tok))
	f.db.Set(context.Background(), "mcp_oauth_tokens", hex.EncodeToString(sum[:]), Doc{"uid": "u1", "name": "Tero", "kind": "access", "exp": time.Now().Add(time.Hour).UnixMilli()})
	return tok
}

func withSignIn(e *Env) *Env {
	e.OAuth = true
	e.VerifyIDToken = func(_ context.Context, tok string) (*IDToken, error) {
		if tok != "google-ok" {
			return nil, fmt.Errorf("bad")
		}
		return &IDToken{UID: "u1", Name: "Tero"}, nil
	}
	return e
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

func start(t *testing.T, env *Env, token string) *testServer {
	t.Helper()
	app := NewApp(env)
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

func notMatch(t *testing.T, s, re string) {
	t.Helper()
	if regexp.MustCompile(re).MatchString(s) {
		t.Fatalf("%q matches %s", s, re)
	}
}

// --- the cases

func TestToolsUIMetadataAndPreview(t *testing.T) {
	s := start(t, testEnv(&fb0, nil), "")
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
	eq(t, names, []string{"add_comment", "begin_work", "bind_chart_data", "create_presentation", "delete_presentation", "end_work", "export_presentation", "get_presentation", "list_comments", "list_files", "list_presentations", "read_file", "read_github_pr", "render_overview", "render_slide", "resolve_comment", "sliqtly_guide", "update_presentation", "vectorize_image", "write_workbook"})
	uri, _ := create.Meta["ui"].(map[string]any)["resourceUri"].(string)
	match(t, uri, `^ui://sliqtly/preview-[0-9a-f]{10}\.html$`)
	eq(t, create.Meta["openai/outputTemplate"], uri)
	schema, _ := json.Marshal(create.InputSchema)
	match(t, string(schema), `"enum":\[[^\]]*"editorial"`)
	r, err := s.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
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
	// a client that kept an older tool list still gets the preview
	old, err := s.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "ui://sliqtly/preview-0000000000.html"})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, old.Contents[0].URI, "ui://sliqtly/preview-0000000000.html")
	eq(t, old.Contents[0].Text, r.Contents[0].Text)
	core := textOf(call(t, s, "sliqtly_guide", map[string]any{}))
	match(t, core, `## Topics`)
	if strings.Contains(core, "# Topic: pictures") || strings.Contains(core, "<!-- topic") {
		t.Fatal("Core carries a topic")
	}
	pics := textOf(call(t, s, "sliqtly_guide", map[string]any{"topic": "pictures"}))
	match(t, pics, `^# Topic: pictures\n`)
	match(t, pics, `## Gallery`)
	if strings.Contains(pics, "# Topic: css") {
		t.Fatal("a topic runs on into the next")
	}
	bad := call(t, s, "sliqtly_guide", map[string]any{"topic": "nope"})
	if !bad.IsError {
		t.Fatal("an unknown topic was answered")
	}
	match(t, textOf(bad), `There is no topic "nope"\. Topics: layout, effects, text, charts, diagrams, figures, smartart, pictures, css, data, editing, export, limits\.`)
}

// every topic Core lists is there, and every topic is listed in Core
func TestGuideTopics(t *testing.T) {
	for _, md := range []string{withoutRooms(guideMD), withRooms(guideMD)} {
		core := md[:strings.Index(md, "<!-- topic: ")]
		names := regexp.MustCompile(`<!-- topic: ([a-z]+) -->`).FindAllStringSubmatch(md, -1)
		listed := regexp.MustCompile("(?m)^\\| `([a-z]+)` \\|").FindAllStringSubmatch(core, -1)
		if len(names) != len(listed) || len(names) < 13 {
			t.Fatalf("%d topics, %d listed in Core", len(names), len(listed))
		}
		for i := range names {
			eq(t, listed[i][1], names[i][1])
		}
		// a topic another names is there
		for _, ref := range regexp.MustCompile("topic `([a-z]+)`").FindAllStringSubmatch(md, -1) {
			if !strings.Contains(md, "<!-- topic: "+ref[1]+" -->") {
				t.Fatal("no topic", ref[1])
			}
		}
	}
}

func TestCreateUpdateReadWithPictures(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
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
	eq(t, out["warnings"], []string{EMPTY_FLOW})
	eq(t, out["share_url"], BASE+"/s/"+id)
	// sliqtly.com serves the viewer only: no editor link
	if _, ok := out["edit_url"]; ok {
		t.Fatalf("edit_url %v", out["edit_url"])
	}
	share := f.db.doc("shares/" + id)
	eq(t, share["source"], "mcp")
	eq(t, share["theme"], "aurora")
	match(t, share["css"].(string), `#0b1030[\s\S]*font-size: 60pt`)
	files := list(share["files"])
	eq(t, []any{mapOf(files[0])["path"], mapOf(files[1])["path"]}, []string{"media/cat.png", "media/dot.png"})
	match(t, str(mapOf(files[0])["url"]), `^https://firebasestorage\.googleapis\.com/v0/b/bucket\.test/o/shares%2F.*%2Fmedia%2Fcat\.png\?alt=media&token=`)
	eq(t, f.bucket.saved["shares/"+id+"/media/cat.png"].contentType, "image/png")

	// the owner's deck: someone else, not signed in, with a wrong key
	anon := start(t, testEnv(&f, nil), "")
	bad := call(t, anon, "update_presentation", map[string]any{"deck_id": id, "markdown": "# x"})
	anon.close()
	if !bad.IsError {
		t.Fatal("a wrong key changed the deck")
	}
	eq(t, f.db.doc("shares/" + id)["md"], DECK)

	u := call(t, s, "update_presentation", map[string]any{"deck_id": id, "markdown": DECK + "\n## More\n\n![](media/new.png)\n", "theme": "corporate"})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	uo := sc(u)
	eq(t, uo["share_url"], out["share_url"])
	eq(t, uo["slides"], 3)
	eq(t, uo["warnings"], []string{"media/new.png is used in the Markdown but no image by that name was sent. → topic=pictures", EMPTY_FLOW})
	after := f.db.doc("shares/" + id)
	eq(t, after["theme"], "corporate")
	match(t, after["css"].(string), `added for this deck[\s\S]*font-size: 60pt`)
	if strings.Contains(after["css"].(string), "#0b1030") {
		t.Fatal("the old theme's sheet stayed:", after["css"])
	}
	eq(t, len(list(after["files"])), 2)

	// the preview reads the stored deck with the site's Firebase config
	eq(t, u.Meta["sliqtly/firebase"], map[string]any{"projectId": "sliqtly-test", "apiKey": "k"})

	g := sc(call(t, s, "get_presentation", map[string]any{"deck_id": id}))
	eq(t, g["markdown"], after["md"])
	imgs := list(g["images"])
	eq(t, []any{mapOf(imgs[0])["name"], mapOf(imgs[1])["name"]}, []string{"cat.png", "dot.png"})
}

func TestRefusesWhatItShouldNotFetchOrStore(t *testing.T) {
	s := start(t, withSignIn(testEnv(&fb0, nil)), signIn(fb0))
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
		{map[string]any{"name": "a.png"}, `url, data_base64 or \(for an SVG\) text`},
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
	s := start(t, testEnv(nil, nil), "")
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
	if _, ok := out["edit_url"]; ok {
		t.Fatalf("edit_url %v without an editor", out["edit_url"])
	}
}

func TestRateLimitAndBrowserVisit(t *testing.T) {
	s := start(t, testEnv(&fb0, rateLimiter(1, 10*time.Minute)), "")
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
	anon := start(t, withSignIn(testEnv(&f, nil)), "")
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
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
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
	me := start(t, withSignIn(testEnv(&f, nil)), tok["access_token"].(string))
	c := call(t, me, "create_presentation", map[string]any{"title": "Mine", "markdown": "# m"})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	match(t, textOf(c), `account of Tero`)
	// no editor on sliqtly.com, so no word on which account the editor writes as
	if strings.Contains(textOf(c), "editor") {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	eq(t, f.db.doc("shares/" + id)["owner"], "u1")
	u := call(t, me, "update_presentation", map[string]any{"deck_id": id, "markdown": "# m\n\n## two"})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	eq(t, sc(u)["slides"], 2)
	// a deck made without sign-in: only the session that made it
	match(t, textOf(call(t, me, "update_presentation", map[string]any{"deck_id": anonID, "markdown": "# x"})), `made without sign-in, and only the conversation that made it can change it`)
	// another account's editor deck: refused with the account named, no copy
	f.db.Set(context.Background(), "shares", "EdOther001", Doc{"owner": "u2", "source": "editor", "md": "# theirs", "theme": "aurora"})
	other := textOf(call(t, me, "update_presentation", map[string]any{"deck_id": "EdOther001", "markdown": "# x"}))
	match(t, other, `belongs to another Sliqtly account than the one this connector is signed in with \(Tero\)`)
	match(t, other, `do not make a copy unasked`)
	eq(t, f.db.doc("shares/EdOther001")["md"], "# theirs")
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
	// every sign-in record carries the timestamp Firestore's TTL deletes it by
	n := 0
	for k, v := range f.db.data {
		if regexp.MustCompile(`^mcp_oauth_(requests|codes|tokens)/`).MatchString(k) {
			n++
			eq(t, storedTime(t, v["expires"]).UnixMilli(), v["exp"], k)
		}
	}
	if n == 0 {
		t.Fatal("no sign-in records")
	}

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
	o := Deck_static_outline(DECK)
	eq(t, o.titles, []string{"Hello", "Cat"})
	eq(t, o.media, []string{"cat.png", "dot.png"})
	eq(t, Deck_static_cleanName("team photo.JPG"), "team-photo.jpg")
	eq(t, Deck_static_cleanName("../a.png"), "")
	eq(t, Deck_static_cleanName("media/My Pic.PNG"), "My-Pic.png")
	eq(t, (&McpHost{}).URIEncode("shares/a b/media/x(1).png"), "shares%2Fa%20b%2Fmedia%2Fx(1).png")
	eq(t, Deck_static_headingOf("## Cat {bg=media/cat.png}"), "Cat")
	eq(t, Deck_static_privateHost("172.20.1.1"), true)
	eq(t, Deck_static_privateHost("172.32.1.1"), false)
}

// --- loosely typed values of a stored document

func str(v any) string {
	s, _ := v.(string)
	return s
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func mapOf(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func rpc(t *testing.T, root, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", root+"/mcp", strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	json.NewDecoder(res.Body).Decode(&m)
	return res.StatusCode, m
}

func TestProtocolEdges(t *testing.T) {
	f := fakeFirebase()
	s := start(t, testEnv(&f, nil), "")
	defer s.close()
	// escaped non-ASCII, a surrogate pair among it, comes back as written
	st, r := rpc(t, s.root, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"create_presentation","arguments":{"title":"Hyvä","markdown":"# Hyvä 😀\n\n## \"Toka\" \\ dia"}}}`)
	eq(t, st, 200)
	out := mapOf(mapOf(r["result"])["structuredContent"])
	eq(t, f.db.doc("shares/" + out["deck_id"].(string))["md"], "# Hyvä 😀\n\n## \"Toka\" \\ dia")
	eq(t, out["title"], "Hyvä")
	eq(t, out["slides"], 2)
	eq(t, r["id"], 7)

	st, r = rpc(t, s.root, `{"jsonrpc":"2.0","id":"a","method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	eq(t, mapOf(r["error"])["code"], -32602)
	st, r = rpc(t, s.root, `{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`)
	eq(t, mapOf(r["error"])["code"], -32601)
	st, _ = rpc(t, s.root, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	eq(t, st, 202)
	st, r = rpc(t, s.root, `{"jsonrpc":"2.0","id":1,`)
	eq(t, st, 400)
	eq(t, mapOf(r["error"])["code"], -32700)
	st, r = rpc(t, s.root, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"create_presentation","arguments":{"title":5,"markdown":"# x"}}}`)
	eq(t, mapOf(r["result"])["isError"], true)
}

// The deck as the editor's own model (PresDeck, compiled from src/) reads it:
// the player's slide count, a slide that runs over, a chart that is not JSON.
// Many calls at once, so `go test -race` covers the model being shared.
func TestDeckIsCheckedByTheEditorsModel(t *testing.T) {
	s := start(t, testEnv(nil, nil), "")
	defer s.close()
	long := "## Long\n\n"
	for i := 1; i <= 30; i++ {
		long += fmt.Sprintf("%d. item %d\n", i, i)
	}
	md := "# T\n\n## Chart\n\n```vega-lite\n{\"mark\": \"bar\", \"data\": {\"values\": [1,2}\n```\n\n" + long
	var wg sync.WaitGroup
	results := make([]map[string]any, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := call(t, s, "create_presentation", map[string]any{"title": "x", "markdown": md})
			if c.IsError {
				t.Error(textOf(c))
				return
			}
			results[i] = sc(c)
		}(i)
	}
	wg.Wait()
	for _, out := range results {
		if out == nil {
			continue
		}
		ws := fmt.Sprint(out["warnings"])
		match(t, ws, `The vega-lite block on slide "Chart" is not shown: that is not JSON\.`)
		match(t, ws, `Slide "Long" does not fit and goes on over \d+ more slides?`)
		if n, _ := out["slides"].(float64); n < 4 {
			t.Fatalf("slides %v: the long slide is laid out over more than one", out["slides"])
		}
		eq(t, out["slides"], results[0]["slides"])
	}
}

func TestBindChartDataPointsAChartAtLiveData(t *testing.T) {
	f := fakeFirebase()
	s := start(t, testEnv(&f, nil), "")
	defer s.close()
	j := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	md := "# Q3\n\n## Revenue\n\n```vega-lite\n" + j(map[string]any{"mark": "bar", "data": map[string]any{"values": []any{map[string]any{"m": "Jan", "v": 1}}}, "encoding": map[string]any{"x": map[string]any{"field": "m"}}}) +
		"\n```\n\n## Costs\n\n```vega-lite\n" + j(map[string]any{"layer": []any{map[string]any{"mark": "line", "data": map[string]any{"values": []any{}}}}}) + "\n```\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Q3", "markdown": md})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)

	a := call(t, s, "bind_chart_data", map[string]any{"deck_id": id, "chart": "revenue", "source": map[string]any{"google_sheets": "SHEET1", "range": "Monthly!A:B"}})
	if a.IsError {
		t.Fatal(textOf(a))
	}
	ao := sc(a)
	eq(t, ao["chart"], 1)
	spec := mapOf(ao["spec"])
	eq(t, spec["data"], map[string]any{"source": "google-sheets", "id": "SHEET1", "range": "Monthly!A:B"})
	eq(t, spec["encoding"], map[string]any{"x": map[string]any{"field": "m"}})
	eq(t, ao["share_url"], sc(c)["share_url"])
	match(t, textOf(a), `Chart 1 \(on "Revenue"\) now reads \{"source":"google-sheets","id":"SHEET1","range":"Monthly!A:B"\}\. PDF and PPTX`)

	b := call(t, s, "bind_chart_data", map[string]any{"deck_id": id, "chart": 2, "source": "https://data.test/costs.csv"})
	if b.IsError {
		t.Fatal(textOf(b))
	}
	eq(t, sc(b)["spec"], map[string]any{"layer": []any{map[string]any{"mark": "line"}}, "data": map[string]any{"url": "https://data.test/costs.csv"}})
	stored := f.db.doc("shares/" + id)["md"].(string)
	match(t, stored, `"id": "SHEET1"`)
	match(t, stored, `costs\.csv`)
	match(t, stored, `## Costs`)
	// written as JSON.stringify(spec, null, 2)
	match(t, stored, "```vega-lite\n\\{\n  \"layer\": \\[\n    \\{\n      \"mark\": \"line\"\n    \\}\n  \\],\n  \"data\": \\{\n    \"url\": \"https://data.test/costs.csv\"\n  \\}\n\\}\n```")

	for _, tc := range []struct {
		args map[string]any
		re   string
	}{
		{map[string]any{"chart": 3, "source": "https://data.test/x.csv"}, `has 2 charts`},
		{map[string]any{"chart": "Nope", "source": "https://data.test/x.csv"}, `No chart on a slide titled`},
		{map[string]any{"chart": 1, "source": "http://data.test/x.csv"}, `https URL`},
	} {
		args := map[string]any{"deck_id": id}
		for k, v := range tc.args {
			args[k] = v
		}
		r := call(t, s, "bind_chart_data", args)
		if !r.IsError {
			t.Fatalf("%v did not fail", tc.args)
		}
		match(t, textOf(r), tc.re)
	}
	eq(t, f.db.doc("shares/" + id)["md"], stored)
}

func TestWarnsOfUnknownEncodingTypes(t *testing.T) {
	f := fakeFirebase()
	s := start(t, testEnv(&f, nil), "")
	defer s.close()
	md := "# Day\n\n## Wake-up\n\n```vega-lite\n" +
		`{"data":{"values":[{"m":"Jan","h":7.5}]},"layer":[{"mark":"line","encoding":{"x":{"field":"m","type":"point"},"y":{"field":"h","type":"quantitative"}}}]}` +
		"\n```\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Day", "markdown": md})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	match(t, textOf(c), `Note: Chart 1 on "Wake-up": encoding x has type "point"; Vega-Lite types are quantitative, ordinal, nominal and temporal`)
}

// A workbook as Excel writes one: a title row over the header, dates, a long
// decimal, and a second sheet.
func testBook(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	put := func(name, text string) {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, text)
	}
	const ns = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"`
	put("[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/></Types>`)
	put("_rels/.rels", `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`)
	put("xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8"?><workbook `+ns+` xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Monthly" sheetId="1" r:id="rId1"/><sheet name="Notes" sheetId="2" r:id="rId2"/></sheets></workbook>`)
	put("xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/><Relationship Id="rId4" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/></Relationships>`)
	put("xl/styles.xml", `<?xml version="1.0" encoding="UTF-8"?><styleSheet `+ns+`><fonts count="1"><font><sz val="11"/></font></fonts><fills count="1"><fill><patternFill patternType="none"/></fill></fills><borders count="1"><border/></borders><cellStyleXfs count="1"><xf/></cellStyleXfs><cellXfs count="2"><xf numFmtId="0"/><xf numFmtId="14" applyNumberFormat="1"/></cellXfs></styleSheet>`)
	put("xl/sharedStrings.xml", `<?xml version="1.0" encoding="UTF-8"?><sst `+ns+`><si><t>Card risk 2022</t></si><si><t>month</t></si><si><t>cards</t></si><si><t>Key</t></si><si><t>Value</t></si><si><t>source</t></si><si><t>bank, "core"</t></si></sst>`)
	rows := `<row r="1"><c r="A1" t="s"><v>0</v></c></row><row r="2"><c r="A2" t="s"><v>1</v></c><c r="B2" t="s"><v>2</v></c></row>`
	for i := 1; i <= 12; i++ {
		v := fmt.Sprint(i * 10)
		if i == 3 {
			v = "0.30000000000000004"
		}
		rows += fmt.Sprintf(`<row r="%d"><c r="A%d" s="1"><v>%d</v></c><c r="B%d"><v>%s</v></c></row>`, i+2, i+2, 44562+(i-1)*31, i+2, v)
	}
	put("xl/worksheets/sheet1.xml", `<?xml version="1.0" encoding="UTF-8"?><worksheet `+ns+`><sheetData>`+rows+`</sheetData></worksheet>`)
	put("xl/worksheets/sheet2.xml", `<?xml version="1.0" encoding="UTF-8"?><worksheet `+ns+`><sheetData><row r="1"><c r="A1" t="s"><v>3</v></c><c r="B1" t="s"><v>4</v></c></row><row r="2"><c r="A2" t="s"><v>5</v></c><c r="B2" t="s"><v>6</v></c></row></sheetData></worksheet>`)
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReadFileWorkbookSheetsCSVAndJSON(t *testing.T) {
	f := fakeFirebase()
	book := testBook(t)
	f.db.data["shares/abcDEF1234"] = Doc{"name": "Risk", "md": "# R\n", "theme": "aurora", "files": []any{
		map[string]any{"path": "media/cat.png", "type": "image/png", "size": int64(68)},
		map[string]any{"path": "data/risk.xlsx", "type": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "size": int64(len(book))},
		map[string]any{"path": "data/notes.csv", "type": "text/csv", "size": int64(30)},
		map[string]any{"path": "data/spec.json", "type": "application/json", "size": int64(9)},
	}}
	f.bucket.saved["shares/abcDEF1234/data/risk.xlsx"] = savedFile{book, ""}
	f.bucket.saved["shares/abcDEF1234/data/notes.csv"] = savedFile{[]byte("a,b\r\n\"x, y\",2\n\n3,\"q\"\"\"\n"), ""}
	f.bucket.saved["shares/abcDEF1234/data/spec.json"] = savedFile{[]byte(`{"a": 1}`), ""}
	// a byte order mark is dropped; a first character that only starts
	// with the same byte (U+FF21, EF BC A1) is kept
	f.bucket.saved["shares/abcDEF1234/data/bom.csv"] = savedFile{[]byte("\xEF\xBB\xBFa,b\n1,2\n"), ""}
	f.bucket.saved["shares/abcDEF1234/data/wide.csv"] = savedFile{[]byte("\uFF21,b\n1,2\n"), ""}
	f.db.data["shares/abcDEF1234"]["files"] = append(f.db.data["shares/abcDEF1234"]["files"].([]any),
		map[string]any{"path": "data/bom.csv", "type": "text/csv", "size": int64(15)},
		map[string]any{"path": "data/wide.csv", "type": "text/csv", "size": int64(14)})
	s := start(t, testEnv(&f, nil), "")
	defer s.close()
	read := func(args map[string]any) *mcp.CallToolResult {
		args["deck_id"] = "abcDEF1234"
		return call(t, s, "read_file", args)
	}

	l := call(t, s, "list_files", map[string]any{"deck_id": "abcDEF1234"})
	if l.IsError {
		t.Fatal(textOf(l))
	}
	files := list(sc(l)["files"])
	eq(t, mapOf(files[1])["kind"], "workbook")
	eq(t, mapOf(files[1])["sheets"], []any{
		map[string]any{"name": "Monthly", "columns": []string{"month", "cards"}, "rows": 12, "csv": "data/risk-Monthly.csv"},
		map[string]any{"name": "Notes", "columns": []string{"Key", "Value"}, "rows": 1, "csv": "data/risk-Notes.csv"},
	})
	match(t, textOf(l), `sheet "Monthly": 12 rows; columns "month", "cards"; read as data/risk-Monthly\.csv`)

	r := read(map[string]any{"path": "data/risk.xlsx", "limit": 5})
	if r.IsError {
		t.Fatal(textOf(r))
	}
	o := sc(r)
	eq(t, o["sheet"], map[string]any{"name": "Monthly", "csv": "data/risk-Monthly.csv"})
	eq(t, o["columns"], []string{"month", "cards"})
	eq(t, o["total_rows"], 12)
	// dates as the workbook shows them, a long decimal as the editor tidies it
	eq(t, list(o["rows"])[0], []string{"01/01/2022", "10"})
	eq(t, list(o["rows"])[2], []string{"03/04/2022", "0.3"})
	eq(t, o["next_offset"], 5)
	match(t, textOf(r), `Rows 1–5 of 12; next: offset 5`)
	match(t, textOf(r), `Other sheets: "Notes"`)
	last := sc(read(map[string]any{"path": "data/risk.xlsx", "offset": 10}))
	eq(t, len(list(last["rows"])), 2)
	eq(t, last["next_offset"], nil)

	byCSV := read(map[string]any{"path": "data/risk-Notes.csv"})
	eq(t, sc(byCSV)["rows"], [][]string{{"source", `bank, "core"`}})
	match(t, textOf(byCSV), `source,"bank, ""core"""`)
	eq(t, sc(read(map[string]any{"path": "data/risk.xlsx", "sheet": "notes"}))["sheet"].(map[string]any)["name"], "Notes")
	no := read(map[string]any{"path": "data/risk.xlsx", "sheet": "Yearly"})
	if !no.IsError {
		t.Fatal("an unknown sheet was read")
	}
	match(t, textOf(no), `no sheet "Yearly"\. Its sheets: "Monthly", "Notes"`)

	c := sc(read(map[string]any{"path": "data/notes.csv"}))
	eq(t, c["columns"], []string{"a", "b"})
	eq(t, c["rows"], [][]string{{"x, y", "2"}, {"3", `q"`}})
	eq(t, sc(read(map[string]any{"path": "data/spec.json"}))["text"], `{"a": 1}`)
	eq(t, sc(read(map[string]any{"path": "data/bom.csv"}))["columns"], []string{"a", "b"})
	eq(t, sc(read(map[string]any{"path": "data/wide.csv"}))["columns"], []string{"\uFF21", "b"})
	match(t, textOf(read(map[string]any{"path": "media/cat.png"})), `is a picture`)
	match(t, textOf(read(map[string]any{"path": "data/other.csv"})), `No file data/other\.csv .* Its files: data/risk\.xlsx, data/notes\.csv, data/spec\.json`)

	g := sc(call(t, s, "get_presentation", map[string]any{"deck_id": "abcDEF1234"}))
	eq(t, len(list(g["images"])), 1)
	eq(t, mapOf(list(g["files"])[1])["sheets"].([]any)[0].(map[string]any)["name"], "Monthly")
}

func TestCreateAndUpdateKeepDataFiles(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "# Risk\n\n## Monthly\n\n```vega-lite\n{\"data\": {\"url\": \"data/risk-Monthly.csv\"}, \"mark\": \"bar\"}\n```\n"
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Risk", "markdown": md,
		"files": []any{
			map[string]any{"name": "risk.xlsx", "data_base64": base64.StdEncoding.EncodeToString(testBook(t))},
			map[string]any{"name": "data/extra.csv", "text": "a,b\n1,2\n"},
		},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	match(t, textOf(c), `data/risk\.xlsx: sheet "Monthly" \(12 rows; columns "month", "cards"\) read as data/risk-Monthly\.csv`)
	files := list(f.db.doc("shares/" + id)["files"])
	eq(t, []any{mapOf(files[0])["path"], mapOf(files[0])["type"], mapOf(files[1])["path"], mapOf(files[1])["type"]},
		[]string{"data/risk.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "data/extra.csv", "text/csv"})
	eq(t, string(f.bucket.saved["shares/"+id+"/data/extra.csv"].data), "a,b\n1,2\n")
	back := sc(call(t, s, "read_file", map[string]any{"deck_id": id, "path": "data/risk-Monthly.csv", "limit": 1}))
	eq(t, back["rows"], [][]string{{"01/01/2022", "10"}})

	u := call(t, s, "update_presentation", map[string]any{"deck_id": id,
		"files": []any{map[string]any{"name": "extra.csv", "text": "a,b\n3,4\n"}, map[string]any{"name": "more.json", "text": "[1]"}}})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	paths := []any{}
	for _, x := range list(f.db.doc("shares/" + id)["files"]) {
		paths = append(paths, mapOf(x)["path"])
	}
	eq(t, paths, []string{"data/risk.xlsx", "data/extra.csv", "data/more.json"})
	eq(t, string(f.bucket.saved["shares/"+id+"/data/extra.csv"].data), "a,b\n3,4\n")

	for _, bad := range []struct {
		file map[string]any
		why  string
	}{
		{map[string]any{"name": "bad.xlsx", "data_base64": base64.StdEncoding.EncodeToString([]byte("nope"))}, `not a workbook Sliqtly can read`},
		{map[string]any{"name": "run.exe", "text": "x"}, `data files are \.xlsx, \.csv`},
		{map[string]any{"name": "a.csv", "text": "x", "url": "https://images.test/a.csv"}, `give one of text, data_base64 or url`},
		{map[string]any{"name": "a.xlsx", "text": "x"}, `sent as data_base64 or url`},
	} {
		r := call(t, s, "create_presentation", map[string]any{"title": "x", "markdown": "# x", "files": []any{bad.file}})
		if !r.IsError {
			t.Fatalf("%v was kept", bad.file)
		}
		match(t, textOf(r), bad.why)
	}
}

func TestWriteWorkbookReplacesADecksWorkbook(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{"title": "Risk", "markdown": "# Risk",
		"files": []any{map[string]any{"name": "risk.xlsx", "data_base64": base64.StdEncoding.EncodeToString(testBook(t))}}})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	write := func(args map[string]any) *mcp.CallToolResult {
		a := map[string]any{"deck_id": id, "path": "data/risk.xlsx"}
		for k, v := range args {
			a[k] = v
		}
		return call(t, s, "write_workbook", a)
	}
	w := write(map[string]any{"sheets": []any{
		map[string]any{"name": "Menot", "rows": []any{[]any{"Kuukausi", "Vuokra", "Sähkö"}, []any{"2026-01", 950, "42.5"}, []any{"2026-02", 950, nil}}},
		map[string]any{"name": "Q & A", "csv": "a,b\n\"<x> & y\",2\n"},
		map[string]any{"name": "Sum", "rows": []any{[]any{"Total", "Note"}, []any{map[string]any{"f": "=SUM(Menot!B2:B3)", "v": 1900}, map[string]any{"f": `="a"&"b"`, "v": "ab"}}, []any{map[string]any{"f": "=1+1"}}}},
	}})
	if w.IsError {
		t.Fatal(textOf(w))
	}
	match(t, textOf(w), `data/risk\.xlsx: sheet "Menot" \(2 rows; columns "Kuukausi", "Vuokra", "Sähkö"\) read as data/risk-Menot\.csv`)
	match(t, textOf(w), `Formatting is not kept`)
	paths := []any{}
	for _, x := range list(f.db.doc("shares/" + id)["files"]) {
		paths = append(paths, mapOf(x)["path"])
	}
	eq(t, paths, []string{"data/risk.xlsx"})
	qa := sc(call(t, s, "read_file", map[string]any{"deck_id": id, "path": "data/risk.xlsx", "sheet": "Q & A"}))
	eq(t, qa["rows"], [][]string{{"<x> & y", "2"}})
	sum := sc(call(t, s, "read_file", map[string]any{"deck_id": id, "path": "data/risk.xlsx", "sheet": "Sum"}))
	// the editor's reader works out a formula with no value given
	eq(t, sum["rows"], [][]string{{"1900", "ab"}, {"2", ""}})
	zr, err := zip.NewReader(bytes.NewReader(f.bucket.saved["shares/"+id+"/data/risk.xlsx"].data), int64(len(f.bucket.saved["shares/"+id+"/data/risk.xlsx"].data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, zf := range zr.File {
		if zf.Name == "xl/worksheets/sheet3.xml" {
			rc, _ := zf.Open()
			x, _ := io.ReadAll(rc)
			match(t, string(x), `<c r="A2"><f>SUM\(Menot!B2:B3\)</f><v>1900</v></c><c r="B2" t="str"><f>&quot;a&quot;&amp;&quot;b&quot;</f><v>ab</v></c>`)
		}
	}
	menot := sc(call(t, s, "read_file", map[string]any{"deck_id": id, "path": "data/risk-Menot.csv"}))
	eq(t, menot["rows"], [][]string{{"2026-01", "950", "42.5"}, {"2026-02", "950", ""}})
	for _, bad := range []struct {
		args map[string]any
		why  string
	}{
		{map[string]any{"sheets": []any{map[string]any{"name": "a/b", "rows": []any{[]any{"x"}}}}}, `has one of`},
		{map[string]any{"sheets": []any{map[string]any{"name": "A", "rows": []any{[]any{"x"}}}, map[string]any{"name": "a", "rows": []any{[]any{"y"}}}}}, `Two sheets are named`},
		{map[string]any{"sheets": []any{map[string]any{"name": "A"}}}, `give rows or csv`},
		{map[string]any{"path": "data/x.csv", "sheets": []any{map[string]any{"name": "A", "rows": []any{[]any{"x"}}}}}, `ending in \.xlsx`},
	} {
		r := write(bad.args)
		if !r.IsError {
			t.Fatalf("%v was written", bad.args)
		}
		match(t, textOf(r), bad.why)
	}
}

// a stored timestamp, as the fake keeps it (time.Time through JSON)
func storedTime(t *testing.T, v any) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339Nano, fmt.Sprint(v))
	if err != nil {
		t.Fatalf("not a timestamp: %v", v)
	}
	return tm
}

func TestWithoutSignInTextOnlyAndDeletedAfter7Days(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), "")
	defer s.close()
	for _, args := range []map[string]any{
		{"images": []any{map[string]any{"name": "cat.png", "url": "https://images.test/cat.png"}}},
		{"files": []any{map[string]any{"name": "a.csv", "text": "a\n1\n"}}},
	} {
		args["title"], args["markdown"] = "x", "# x"
		r := call(t, s, "create_presentation", args)
		if !r.IsError {
			t.Fatal("stored an upload without sign-in")
		}
		match(t, textOf(r), `needs sign-in`)
		match(t, fmt.Sprint(r.Meta["mcp/www_authenticate"]), `resource_metadata=`)
	}
	if len(f.bucket.saved) != 0 {
		t.Fatal("bytes were saved")
	}
	c := call(t, s, "create_presentation", map[string]any{"title": "x", "markdown": "# x"})
	match(t, textOf(c), `deleted 7 days after its last change`)
	out := sc(c)
	id := out["deck_id"].(string)
	in7 := time.Now().Add(7 * 24 * time.Hour)
	for _, col := range []string{"shares/"} {
		if d := storedTime(t, f.db.doc(col + id)["expires"]).Sub(in7); d < -time.Minute || d > time.Minute {
			t.Fatalf("%s expires %v off 7 days", col, d)
		}
	}
	match(t, textOf(call(t, s, "update_presentation", map[string]any{"deck_id": id, "images": []any{map[string]any{"name": "cat.png", "url": "https://images.test/cat.png"}}})), `needs sign-in`)
	match(t, textOf(call(t, s, "write_workbook", map[string]any{"deck_id": id, "path": "data/a.xlsx", "sheets": []any{map[string]any{"name": "A", "rows": []any{[]any{"x"}}}}})), `needs sign-in`)

	me := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer me.close()
	mine := call(t, me, "create_presentation", map[string]any{"title": "x", "markdown": "# x", "images": []any{map[string]any{"name": "cat.png", "url": "https://images.test/cat.png"}}})
	if mine.IsError || strings.Contains(textOf(mine), "deleted") {
		t.Fatal(textOf(mine))
	}
	if _, ok := f.db.doc("shares/" + sc(mine)["deck_id"].(string))["expires"]; ok {
		t.Fatal("a signed-in user's deck expires")
	}
}

func TestDailyQuotaInFirestore(t *testing.T) {
	f := fakeFirebase()
	day := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	e := testEnv(&f, nil)
	e.Quota = dailyQuota(f.db, 2, 3, func() time.Time { return day })
	s := start(t, e, "")
	defer s.close()
	make := func() *mcp.CallToolResult {
		return call(t, s, "create_presentation", map[string]any{"title": "x", "markdown": "# x"})
	}
	if make().IsError || make().IsError {
		t.Fatal("refused under the quota")
	}
	match(t, textOf(make()), `daily limit of 2 .* sign in for a higher limit`)
	var counter Doc
	for k, v := range f.db.data {
		if strings.HasPrefix(k, "mcp_quota/") {
			match(t, k, `-2026-10-03$`)
			counter = v
		}
	}
	eq(t, storedTime(t, counter["expires"]), day.Add(48*time.Hour))
	if why := e.Quota(context.Background(), "uid:u1"); why != "" {
		t.Fatal("each caller has their own count")
	}
}

func TestClientRegistrationsLimitedPerAddress(t *testing.T) {
	f := fakeFirebase()
	e := withSignIn(testEnv(&f, nil))
	e.Registrations = rateLimiter(1, 10*time.Minute)
	srv := httptest.NewServer(NewApp(e))
	defer srv.Close()
	reg := func() *http.Response {
		r, err := http.Post(srv.URL+"/oauth/register", "application/json", strings.NewReader(`{"redirect_uris":["https://client.test/cb"]}`))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	eq(t, reg().StatusCode, 201)
	second := reg()
	eq(t, second.StatusCode, 429)
	b, _ := io.ReadAll(second.Body)
	match(t, string(b), `"slow_down"`)
}

// A one-sheet workbook's sheet is data/<book>.csv, and data/<book>-<Sheet>.csv
// as well: the name a ```sheet fence with `sheet: Kulut` reads the still by.
func TestOneSheetBookAnswersToTheSheetsName(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{"title": "Budget", "markdown": "# Budget",
		"files": []any{map[string]any{"name": "budjetti.xlsx", "data_base64": base64.StdEncoding.EncodeToString(testBook(t))}}})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	w := call(t, s, "write_workbook", map[string]any{"deck_id": id, "path": "data/budjetti.xlsx", "sheets": []any{
		map[string]any{"name": "Kulut", "rows": []any{[]any{"Kuukausi", "Vuokra"}, []any{"Tammi", 950}, []any{"Helmi", 975}}},
	}})
	if w.IsError {
		t.Fatal(textOf(w))
	}
	match(t, textOf(w), `read as data/budjetti\.csv`)
	for _, p := range []string{"data/budjetti.csv", "data/budjetti-Kulut.csv"} {
		r := call(t, s, "read_file", map[string]any{"deck_id": id, "path": p})
		if r.IsError {
			t.Fatal(p, textOf(r))
		}
		eq(t, sc(r)["rows"], [][]string{{"Tammi", "950"}, {"Helmi", "975"}})
	}
	// a chart reading the sheet by its name draws its bars' labels
	md := "# Budget\n\n## Rent\n\n```vega-lite\n{\"data\": {\"url\": \"data/budjetti-Kulut.csv\"}, \"mark\": \"bar\", " +
		"\"encoding\": {\"x\": {\"field\": \"Kuukausi\", \"type\": \"nominal\"}, \"y\": {\"field\": \"Vuokra\", \"type\": \"quantitative\"}}}\n```\n"
	u := call(t, s, "update_presentation", map[string]any{"deck_id": id, "markdown": md})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	match(t, textOf(u), `- chart \(Vega-Lite\) at \d+,\d+ size \d+×\d+: \d+ labels`)
}

func TestReviewCommentsReadAddAndResolve(t *testing.T) {
	f := fakeFirebase()
	s := start(t, testEnv(&f, nil), "")
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{"title": "Review", "markdown": "# Plan\n\n## Budget\n\nNumbers\n\n## Risks\n\nSome"})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	match(t, textOf(call(t, s, "list_comments", map[string]any{"deck_id": id})), `has no review comments`)

	a := call(t, s, "add_comment", map[string]any{"deck_id": id, "slide": 2, "text": "Add last year's numbers", "author": "Claude"})
	if a.IsError {
		t.Fatal(textOf(a))
	}
	match(t, textOf(a), `Comment added on slide 2 "Budget"`)
	th := mapOf(sc(a)["thread"])
	tid := th["thread_id"].(string)
	eq(t, []any{th["slide"], th["resolved"], th["x"]}, []any{2, false, 0.9})

	b := call(t, s, "add_comment", map[string]any{"deck_id": id, "slide_title": "risks", "x": 0.25, "y": 0.5, "text": "Rank these", "severity": "high"})
	match(t, textOf(b), `slide 3 "Risks".* Severity high\.`)
	tid2 := mapOf(sc(b)["thread"])["thread_id"].(string)
	eq(t, mapOf(sc(b)["thread"])["severity"], "high")
	match(t, textOf(call(t, s, "add_comment", map[string]any{"deck_id": id, "slide": 1, "text": "x", "severity": "urgent"})), `severity is low, medium, high or none`)
	match(t, textOf(call(t, s, "add_comment", map[string]any{"deck_id": id, "slide": 9, "text": "x"})), `from 1 to 3`)
	match(t, textOf(call(t, s, "add_comment", map[string]any{"deck_id": id, "slide_title": "Nope", "text": "x"})), `No slide is titled "Nope". The slides: 1 "Plan", 2 "Budget", 3 "Risks"`)

	r := call(t, s, "add_comment", map[string]any{"deck_id": id, "thread_id": tid, "text": "Also the forecast"})
	match(t, textOf(r), `Answered thread `+tid)
	match(t, textOf(call(t, s, "add_comment", map[string]any{"deck_id": id, "thread_id": "zz", "text": "x"})), `No comment thread zz`)

	z := call(t, s, "resolve_comment", map[string]any{"deck_id": id, "thread_id": tid, "text": "Done: both added"})
	match(t, textOf(z), `is resolved`)
	eq(t, mapOf(sc(z)["thread"])["resolved"], true)
	match(t, textOf(call(t, s, "resolve_comment", map[string]any{"deck_id": id, "thread_id": tid})), `was already resolved`)

	l := call(t, s, "list_comments", map[string]any{"deck_id": id})
	match(t, textOf(l), `2 review comment threads, 1 open`)
	match(t, textOf(l), `Thread `+tid+` \(resolved\) on slide 2 "Budget"`)
	match(t, textOf(l), `Claude \([^)]+\): Add last year's numbers\n  AI assistant \([^)]+\): Also the forecast\n  AI assistant \([^)]+\): Done: both added`)
	open := call(t, s, "list_comments", map[string]any{"deck_id": id, "include_resolved": false})
	eq(t, len(list(sc(open)["threads"])), 1)
	eq(t, mapOf(list(sc(open)["threads"])[0])["thread_id"], tid2)
	match(t, textOf(open), `Thread `+tid2+` \(open, severity high\)`)
	eq(t, mapOf(list(sc(open)["threads"])[0])["severity"], "high")
	m := call(t, s, "add_comment", map[string]any{"deck_id": id, "thread_id": tid2, "text": "Less urgent now", "severity": "low"})
	match(t, textOf(m), `Severity low\.`)
	eq(t, mapOf(sc(m)["thread"])["severity"], "low")

	// the file the editor reads: review/comments.json, slides counted from 1
	saved, ok := f.bucket.saved["shares/"+id+"/review/comments.json"]
	if !ok {
		t.Fatal("no review/comments.json")
	}
	var file map[string]any
	if err := json.Unmarshal(saved.data, &file); err != nil {
		t.Fatal(err)
	}
	eq(t, file["version"], 1)
	threads := list(file["threads"])
	eq(t, len(threads), 2)
	first := mapOf(threads[0])
	eq(t, []any{first["slide"], first["title"], first["closed"], len(list(first["messages"]))}, []any{2, "Budget", true, 3})
	eq(t, mapOf(list(first["messages"])[0])["who"], "ai")

	// opened again
	match(t, textOf(call(t, s, "resolve_comment", map[string]any{"deck_id": id, "thread_id": tid, "resolved": false})), `is open again`)
}
