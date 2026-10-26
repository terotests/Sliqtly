// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/terotests/sliqtly/mcp-go/rgit"
)

// --- a GitHub of the test's own: the App's endpoints and one repository's
// Git Data API, its objects with Git's own ids

type fakeHub struct {
	t   *testing.T
	key *rsa.PublicKey
	mu  sync.Mutex

	blobs   map[string][]byte
	trees   map[string]map[string]ghEntry // tree → name → entry
	commits map[string]ghCommit
	refs    map[string]string
	private bool
	repoID  int64
	repo    string // owner/name
	// what the App may reach: installation 7, its repositories
	inst  []int64
	calls []string
	// tokens handed out: installation tokens and the user's
	appTokens int
	// pull requests: number → {head ref, base ref, merged}; noPR: the App
	// may not open them (no Pull requests permission)
	pulls map[int64]*fakePull
	noPR  bool
	// needsReview: a pull request is not merged by the API (branch protection)
	needsReview bool
}

type fakePull struct {
	head, base string
	merged     bool
}

// ancestor: a is c or one of its parents' line
func (g *fakeHub) ancestor(a, c string) bool {
	todo := []string{c}
	seen := map[string]bool{}
	for len(todo) > 0 {
		at := todo[0]
		todo = todo[1:]
		if at == a {
			return true
		}
		if seen[at] {
			continue
		}
		seen[at] = true
		for _, p := range g.commits[at].Parents {
			todo = append(todo, p.SHA)
		}
	}
	return false
}

func newFakeHub(t *testing.T, key *rsa.PublicKey) *fakeHub {
	g := &fakeHub{t: t, key: key, blobs: map[string][]byte{}, trees: map[string]map[string]ghEntry{}, commits: map[string]ghCommit{},
		refs: map[string]string{}, pulls: map[int64]*fakePull{}, private: true, repoID: 42, repo: "tero/sliqtly-decks", inst: []int64{42}}
	return g
}

func (g *fakeHub) putBlob(b []byte) string {
	id := rgit.ID("blob", b)
	g.blobs[id] = append([]byte(nil), b...)
	return id
}

// a tree from a flat map of paths
func (g *fakeHub) treeOf(files map[string]string) string {
	kids := map[string]map[string]string{}
	own := map[string]ghEntry{}
	for p, sha := range files {
		name, rest, deep := strings.Cut(p, "/")
		if deep {
			if kids[name] == nil {
				kids[name] = map[string]string{}
			}
			kids[name][rest] = sha
			continue
		}
		s := sha
		own[name] = ghEntry{Path: name, Mode: "100644", Type: "blob", SHA: &s}
	}
	for name, sub := range kids {
		s := g.treeOf(sub)
		own[name] = ghEntry{Path: name, Mode: "040000", Type: "tree", SHA: &s}
	}
	names := make([]string, 0, len(own))
	for n := range own {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s %s %s\n", own[n].Mode, n, *own[n].SHA)
	}
	id := rgit.ID("tree", []byte(b.String()))
	g.trees[id] = own
	return id
}

func (g *fakeHub) flat(tree, prefix string, out map[string]string) {
	for name, e := range g.trees[tree] {
		if e.Type == "tree" {
			g.flat(*e.SHA, prefix+name+"/", out)
		} else {
			out[prefix+name] = *e.SHA
		}
	}
}

func (g *fakeHub) files(commit string) map[string]string {
	out := map[string]string{}
	if c, ok := g.commits[commit]; ok {
		g.flat(c.Tree.SHA, "", out)
	}
	return out
}

func (g *fakeHub) makeCommit(msg, tree string, parents []string, who string) string {
	c := ghCommit{Message: msg}
	c.Tree.SHA = tree
	for _, p := range parents {
		c.Parents = append(c.Parents, struct {
			SHA string `json:"sha"`
		}{p})
	}
	c.Author = ghPerson{Name: who, Date: time.Now()}
	c.Committer = c.Author
	c.SHA = rgit.ID("commit", []byte(fmt.Sprintf("%s\n%s\n%v\n%d", msg, tree, parents, len(g.commits))))
	g.commits[c.SHA] = c
	return c.SHA
}

// push: someone commits files on GitHub (path → text; "" removes)
func (g *fakeHub) push(msg string, change map[string]string) string {
	return g.pushTo("main", msg, change)
}

// pushTo: someone commits files on GitHub on that branch
func (g *fakeHub) pushTo(branch, msg string, change map[string]string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	head := g.refs["heads/"+branch]
	files := g.files(head)
	for p, text := range change {
		if text == "" {
			delete(files, p)
		} else {
			files[p] = g.putBlob([]byte(text))
		}
	}
	c := g.makeCommit(msg, g.treeOf(files), []string{head}, "someone")
	g.refs["heads/"+branch] = c
	return c
}

func (g *fakeHub) text(commit, p string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return string(g.blobs[g.files(commit)[p]])
}

func (g *fakeHub) head() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.refs["heads/main"]
}

func (g *fakeHub) checkJWT(r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), ".")
	if len(parts) != 3 {
		return false
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	return rsa.VerifyPKCS1v15(g.key, crypto.SHA256, sum[:], sig) == nil
}

func (g *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := r.URL.Path
	g.calls = append(g.calls, r.Method+" "+p)
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	var in map[string]any
	if r.Body != nil && r.Header.Get("Content-Type") == "application/json" {
		json.NewDecoder(r.Body).Decode(&in)
	}
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	user := auth == "user-token"
	repoTok := strings.HasPrefix(auth, "inst-")
	base := "/repos/" + g.repo
	switch {
	case p == "/login/oauth/access_token":
		r.ParseForm()
		if r.Form.Get("code") != "good-code" || r.Form.Get("client_secret") != "secret" {
			reply(200, map[string]string{"error": "bad_verification_code"})
			return
		}
		reply(200, map[string]string{"access_token": "user-token"})
	case p == "/user" && user:
		reply(200, map[string]any{"id": 12410648, "login": "tero", "name": "Tero"})
	case p == "/user/emails" && user:
		reply(200, []map[string]any{{"email": "Tero@Example.com", "primary": true, "verified": true}})
	case p == "/user/installations" && user:
		reply(200, map[string]any{"installations": []map[string]any{{"id": 7, "account": map[string]string{"login": "tero"}, "repository_selection": "selected"}}})
	case p == "/user/installations/7/repositories" && user:
		var rs []map[string]any
		for _, id := range g.inst {
			name := g.repo
			if id != g.repoID {
				name = "tero/other-" + strconv.FormatInt(id, 10)
			}
			rs = append(rs, map[string]any{"id": id, "full_name": name, "private": g.private})
		}
		reply(200, map[string]any{"repositories": rs})
	case p == "/user/repos" && r.Method == "POST" && user:
		g.repoID, g.repo = 99, "tero/"+in["name"].(string)
		reply(201, map[string]any{"id": g.repoID, "full_name": g.repo, "private": true, "default_branch": "main"})
	case strings.HasPrefix(p, "/user/installations/7/repositories/") && r.Method == "PUT" && user:
		id, _ := strconv.ParseInt(strings.TrimPrefix(p, "/user/installations/7/repositories/"), 10, 64)
		g.inst = append(g.inst, id)
		w.WriteHeader(204)
	case p == "/app/installations/7/access_tokens":
		if !g.checkJWT(r) {
			reply(401, map[string]string{"message": "bad JWT"})
			return
		}
		if perms, _ := in["permissions"].(map[string]any); perms["pull_requests"] != nil && g.noPR {
			reply(422, map[string]string{"message": "The permissions requested are not granted to this installation."})
			return
		}
		g.appTokens++
		reply(201, map[string]any{"token": fmt.Sprintf("inst-%d", g.appTokens), "expires_at": time.Now().Add(time.Hour)})
	case !repoTok && strings.HasPrefix(p, "/repos/"):
		reply(401, map[string]string{"message": "Bad credentials"})
	case p == base:
		reply(200, map[string]any{"id": g.repoID, "full_name": g.repo, "private": g.private, "default_branch": "main"})
	case p == base+"/contents/README.md" && r.Method == "PUT":
		b, _ := base64.StdEncoding.DecodeString(in["content"].(string))
		c := g.makeCommit(in["message"].(string), g.treeOf(map[string]string{"README.md": g.putBlob(b)}), nil, "Sliqtly")
		g.refs["heads/main"] = c
		reply(201, map[string]any{"commit": map[string]string{"sha": c}})
	case strings.HasPrefix(p, base+"/git/ref/heads/"):
		sha, ok := g.refs["heads/"+strings.TrimPrefix(p, base+"/git/ref/heads/")]
		if !ok {
			reply(409, map[string]string{"message": "Git Repository is empty."})
			return
		}
		reply(200, map[string]any{"object": map[string]string{"sha": sha}})
	case strings.HasPrefix(p, base+"/git/commits/") && r.Method == "GET":
		c, ok := g.commits[strings.TrimPrefix(p, base+"/git/commits/")]
		if !ok {
			reply(404, map[string]string{"message": "Not Found"})
			return
		}
		reply(200, c)
	case strings.HasPrefix(p, base+"/git/trees/") && r.Method == "GET":
		sha := strings.TrimPrefix(p, base+"/git/trees/")
		if _, ok := g.trees[sha]; !ok {
			reply(404, map[string]string{"message": "Not Found"})
			return
		}
		var list []ghEntry
		if r.URL.Query().Get("recursive") != "" {
			flat := map[string]string{}
			g.flat(sha, "", flat)
			for fp, s := range flat {
				s := s
				list = append(list, ghEntry{Path: fp, Mode: "100644", Type: "blob", SHA: &s})
			}
		} else {
			for _, e := range g.trees[sha] {
				list = append(list, e)
			}
		}
		reply(200, map[string]any{"sha": sha, "tree": list, "truncated": false})
	case strings.HasPrefix(p, base+"/git/blobs/") && r.Method == "GET":
		b, ok := g.blobs[strings.TrimPrefix(p, base+"/git/blobs/")]
		if !ok {
			reply(404, map[string]string{"message": "Not Found"})
			return
		}
		reply(200, map[string]string{"content": base64.StdEncoding.EncodeToString(b), "encoding": "base64"})
	case p == base+"/git/blobs" && r.Method == "POST":
		b, _ := base64.StdEncoding.DecodeString(in["content"].(string))
		reply(201, map[string]string{"sha": g.putBlob(b)})
	case p == base+"/git/trees" && r.Method == "POST":
		files := map[string]string{}
		if bt, _ := in["base_tree"].(string); bt != "" {
			g.flat(bt, "", files)
		}
		for _, x := range in["tree"].([]any) {
			e := x.(map[string]any)
			if s, ok := e["sha"].(string); ok {
				if _, have := g.blobs[s]; !have {
					reply(422, map[string]string{"message": "no such blob " + s})
					return
				}
				files[e["path"].(string)] = s
			} else {
				delete(files, e["path"].(string))
			}
		}
		reply(201, map[string]string{"sha": g.treeOf(files)})
	case p == base+"/git/commits" && r.Method == "POST":
		var parents []string
		for _, x := range in["parents"].([]any) {
			parents = append(parents, x.(string))
		}
		who := ""
		if a, ok := in["author"].(map[string]any); ok {
			who, _ = a["name"].(string)
		}
		reply(201, map[string]string{"sha": g.makeCommit(in["message"].(string), in["tree"].(string), parents, who)})
	case strings.HasPrefix(p, base+"/git/refs/heads/") && r.Method == "PATCH":
		ref := "heads/" + strings.TrimPrefix(p, base+"/git/refs/heads/")
		sha := in["sha"].(string)
		if _, ok := g.refs[ref]; !ok {
			reply(422, map[string]string{"message": "Reference does not exist"})
			return
		}
		if g.refs[ref] == sha || !g.ancestor(g.refs[ref], sha) {
			reply(422, map[string]string{"message": "Update is not a fast forward"})
			return
		}
		g.refs[ref] = sha
		reply(200, map[string]any{"object": map[string]string{"sha": sha}})
	case strings.HasPrefix(p, base+"/git/refs/heads/") && r.Method == "DELETE":
		delete(g.refs, "heads/"+strings.TrimPrefix(p, base+"/git/refs/heads/"))
		w.WriteHeader(204)
	case p == base+"/git/refs" && r.Method == "POST":
		ref := strings.TrimPrefix(in["ref"].(string), "refs/")
		if _, ok := g.refs[ref]; ok {
			reply(422, map[string]string{"message": "Reference already exists"})
			return
		}
		g.refs[ref] = in["sha"].(string)
		reply(201, map[string]any{"ref": "refs/" + ref})
	case p == base+"/pulls" && r.Method == "GET":
		owner, _, _ := strings.Cut(g.repo, "/")
		var out []map[string]any
		for n, pr := range g.pulls {
			if !pr.merged && owner+":"+pr.head == r.URL.Query().Get("head") && pr.base == r.URL.Query().Get("base") {
				out = append(out, map[string]any{"number": n, "html_url": fmt.Sprintf("https://github.com/%s/pull/%d", g.repo, n)})
			}
		}
		reply(200, out)
	case p == base+"/pulls" && r.Method == "POST":
		n := int64(len(g.pulls) + 1)
		g.pulls[n] = &fakePull{head: in["head"].(string), base: in["base"].(string)}
		reply(201, map[string]any{"number": n, "html_url": fmt.Sprintf("https://github.com/%s/pull/%d", g.repo, n)})
	case strings.HasPrefix(p, base+"/pulls/") && strings.HasSuffix(p, "/merge") && r.Method == "PUT":
		n, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(p, base+"/pulls/"), "/merge"), 10, 64)
		pr := g.pulls[n]
		head := g.refs["heads/"+pr.head]
		if sha, _ := in["sha"].(string); sha != head {
			reply(409, map[string]string{"message": "Head branch was modified"})
			return
		}
		if g.needsReview {
			reply(405, map[string]string{"message": "At least 1 approving review is required"})
			return
		}
		baseHead := g.refs["heads/"+pr.base]
		if !g.ancestor(baseHead, head) {
			reply(405, map[string]string{"message": "Pull Request is not mergeable"})
			return
		}
		c := g.makeCommit("Merge pull request", g.commits[head].Tree.SHA, []string{baseHead, head}, "GitHub")
		g.refs["heads/"+pr.base] = c
		pr.merged = true
		reply(200, map[string]any{"merged": true, "sha": c})
	case strings.HasPrefix(p, base+"/pulls/") && r.Method == "GET":
		n, _ := strconv.ParseInt(strings.TrimPrefix(p, base+"/pulls/"), 10, 64)
		pr := g.pulls[n]
		if pr == nil {
			reply(404, map[string]string{"message": "Not Found"})
			return
		}
		reply(200, map[string]any{"number": n, "merged": pr.merged})
	case p == base+"/commits" && r.Method == "GET":
		dir := r.URL.Query().Get("path")
		var out []map[string]any
		for at := g.refs["heads/main"]; at != ""; {
			c := g.commits[at]
			parent := ""
			if len(c.Parents) > 0 {
				parent = c.Parents[0].SHA
			}
			now, was := map[string]string{}, map[string]string{}
			g.flat(c.Tree.SHA, "", now)
			if parent != "" {
				g.flat(g.commits[parent].Tree.SHA, "", was)
			}
			touched := false
			for fp, s := range now {
				if strings.HasPrefix(fp, dir+"/") && was[fp] != s {
					touched = true
				}
			}
			for fp := range was {
				if _, ok := now[fp]; strings.HasPrefix(fp, dir+"/") && !ok {
					touched = true
				}
			}
			if touched {
				row := map[string]any{"sha": c.SHA, "commit": map[string]any{"message": c.Message, "author": c.Author, "committer": c.Committer}, "parents": []map[string]string{}}
				if parent != "" {
					row["parents"] = []map[string]string{{"sha": parent}}
				}
				out = append(out, row)
			}
			at = parent
		}
		reply(200, out)
	default:
		reply(404, map[string]string{"message": "Not Found: " + r.Method + " " + p})
	}
}

// --- the setup

type gitTest struct {
	env  *Env
	f    fb
	gh   *fakeHub
	c    *cloudGit
	srv  *httptest.Server
	hub  *httptest.Server
	ctx  context.Context
	base string
	key  string // the App's private key, PEM
}

func newGitTest(t *testing.T) *gitTest {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	gh := newFakeHub(t, &key.PublicKey)
	hub := httptest.NewServer(gh)
	app, err := newGithubApp("123", "Iv1.client", "secret", "sliqtly-test", pemKey)
	if err != nil {
		t.Fatal(err)
	}
	app.API, app.Web, app.client = hub.URL, hub.URL, hub.Client()
	f := fakeFirebase()
	e := testEnv(&f, nil)
	web := mapFS(map[string]string{"index.html": "<!doctype html><html><head>\n<base href=\"/\" />\n</head><body></body></html>", "main.js": "//"})
	verify := func(_ context.Context, tok string) (*IDToken, error) {
		switch tok {
		case "tero":
			return &IDToken{UID: "u-tero", Email: "tero@example.com", Name: "Tero", Verified: true}, nil
		case "anna":
			return &IDToken{UID: "u-anna", Email: "anna@example.com", Name: "Anna", Verified: true}, nil
		case "gh":
			return &IDToken{UID: "gh-12410648", Name: "Tero"}, nil
		}
		return nil, fmt.Errorf("expired")
	}
	e.EditorGate = newEditorGate(web, e.DB, verify, nil)
	srv := httptest.NewServer(NewApp(e))
	e.BaseURL = srv.URL
	c := newCloudGit(app, e.DB, e.Bucket, srv.URL)
	c.hookSecret, c.sweepKey = "hook-secret", "sweep-key"
	c.logf = t.Logf
	c.mint = func(_ context.Context, uid string, u ghUser) (string, error) { return "custom:" + uid, nil }
	e.CloudGit = c
	t.Cleanup(func() { srv.Close(); hub.Close() })
	return &gitTest{env: e, f: f, gh: gh, c: c, srv: srv, hub: hub, ctx: context.Background(), base: srv.URL, key: pemKey}
}

func mapFS(m map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for k, v := range m {
		out[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return out
}

// a deck of the account's own
func (g *gitTest) deck(id, uid, md string, extra Doc) {
	d := Doc{"name": deckTitle(md, id), "md": md, "owner": uid, "theme": "aurora", "files": []any{}, "created": int64(1)}
	for k, v := range extra {
		d[k] = v
	}
	g.f.db.Set(g.ctx, "shares", id, d)
}

func (g *gitTest) md(id string) string {
	d, _ := g.f.db.Get(g.ctx, "shares", id)
	s, _ := d["md"].(string)
	return s
}

func (g *gitTest) do(t *testing.T, method, path, cookie, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, g.base+path, strings.NewReader(body))
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "__session", Value: cookie})
	}
	if method == "POST" {
		req.Header.Set("Origin", g.base)
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

// connects u-tero to GitHub as the page does: sign-in, then the repository
func (g *gitTest) connect(t *testing.T) {
	t.Helper()
	res, _ := g.do(t, "GET", "/editor/github/login?mode=link", "tero", "")
	if res.StatusCode != 302 {
		t.Fatalf("login: %d", res.StatusCode)
	}
	to, _ := url.Parse(res.Header.Get("Location"))
	if to.Path != "/login/oauth/authorize" || to.Query().Get("client_id") != "Iv1.client" {
		t.Fatalf("to GitHub: %s", to)
	}
	state := to.Query().Get("state")
	res, body := g.do(t, "GET", "/editor/github/callback?code=good-code&state="+url.QueryEscape(state), "tero", "")
	if res.StatusCode != 302 || res.Header.Get("Location") != gitPagePath {
		t.Fatalf("callback: %d %s", res.StatusCode, body)
	}
	res, body = g.do(t, "POST", "/editor/api/git/connect", "tero", `{"installation":7,"repo":42}`)
	if res.StatusCode != 200 {
		t.Fatalf("connect: %d %s", res.StatusCode, body)
	}
}

// --- the tests

func TestGitHubAppJWT(t *testing.T) {
	g := newGitTest(t)
	tok, err := g.c.app.repoToken(g.ctx, 7, 42)
	if err != nil || tok != "inst-1" {
		t.Fatalf("token %q %v", tok, err)
	}
	// kept until it nears its end
	if tok2, _ := g.c.app.repoToken(g.ctx, 7, 42); tok2 != tok {
		t.Fatalf("a new token %q for the same repository", tok2)
	}
	// the key as one base64 line, as an environment variable holds it
	if _, err := newGithubApp("123", "Iv1.client", "secret", "sliqtly-test", base64.StdEncoding.EncodeToString([]byte(g.key))); err != nil {
		t.Fatalf("a base64 key: %v", err)
	}
}

func TestGitStateSigned(t *testing.T) {
	g := newGitTest(t)
	s := g.c.sign(githubState{Mode: "link", UID: "u-tero", Until: time.Now().Add(time.Minute).UnixMilli()})
	if got, err := g.c.unsign(s); err != nil || got.UID != "u-tero" {
		t.Fatalf("unsign: %+v %v", got, err)
	}
	body, sig, _ := strings.Cut(s, ".")
	forged, _ := json.Marshal(githubState{Mode: "link", UID: "u-anna", Until: time.Now().Add(time.Minute).UnixMilli()})
	if _, err := g.c.unsign(base64.RawURLEncoding.EncodeToString(forged) + "." + sig); err == nil {
		t.Fatal("a changed state was taken")
	}
	_ = body
	old := g.c.sign(githubState{Mode: "link", UID: "u-tero", Until: time.Now().Add(-time.Second).UnixMilli()})
	if _, err := g.c.unsign(old); err == nil {
		t.Fatal("an old state was taken")
	}
	for in, want := range map[string]string{"/editor/d/x": "/editor/d/x", "https://evil.example/": "/editor/", "//evil.example/": "/editor/", "/editor": "/editor", "/s/x": "/editor/"} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGitLinkWrongAccount(t *testing.T) {
	g := newGitTest(t)
	res, _ := g.do(t, "GET", "/editor/github/login?mode=link", "tero", "")
	to, _ := url.Parse(res.Header.Get("Location"))
	// Anna's browser cannot finish Tero's connection
	_, body := g.do(t, "GET", "/editor/github/callback?code=good-code&state="+url.QueryEscape(to.Query().Get("state")), "anna", "")
	if !strings.Contains(body, "someone else") {
		t.Fatalf("Anna finished Tero's sign-in: %s", body)
	}
	if d, _ := g.f.db.Get(g.ctx, gitUserCol, "12410648"); d != nil {
		t.Fatalf("GitHub connected: %v", d)
	}
}

func TestGitSyncCommitsDecks(t *testing.T) {
	g := newGitTest(t)
	g.deck("Deck000001", "u-tero", "# Roadmap\n\n## One\n\nFirst.\n", Doc{"css": "h1 { color: red; }", "files": []any{map[string]any{"path": "media/cat.png", "type": "image/png", "size": int64(4)}}})
	g.f.bucket.Save(g.ctx, "shares/Deck000001/media/cat.png", "image/png", []byte("\x89PNG"), nil)
	g.deck("Deck000002", "u-anna", "# Anna's\n", nil)
	g.f.db.Set(g.ctx, layoutCol, "u-tero", layoutDocOf(map[string]string{"room/r1": `{"title":"Plans"}`, "deck/Deck000001": "r1"}))
	g.connect(t)
	head := g.gh.head()
	if got := g.gh.text(head, "decks/Deck000001/deck.md"); got != "# Roadmap\n\n## One\n\nFirst.\n" {
		t.Fatalf("deck.md %q", got)
	}
	if got := g.gh.text(head, "decks/Deck000001/theme.css"); got != "h1 { color: red; }" {
		t.Fatalf("theme.css %q", got)
	}
	if got := g.gh.text(head, "decks/Deck000001/media/cat.png"); got != "\x89PNG" {
		t.Fatalf("picture %q", got)
	}
	var meta map[string]any
	json.Unmarshal([]byte(g.gh.text(head, "decks/Deck000001/deck.json")), &meta)
	if meta["room"] != "r1" || meta["name"] != "Roadmap" {
		t.Fatalf("deck.json %v", meta)
	}
	if !strings.Contains(g.gh.text(head, "rooms/r1.json"), "Plans") {
		t.Fatalf("room %q", g.gh.text(head, "rooms/r1.json"))
	}
	if _, ok := g.gh.files(head)["decks/Deck000002/deck.md"]; ok {
		t.Fatal("another account's deck went in")
	}
	if !strings.Contains(g.gh.text(head, "README.md"), "[Roadmap](decks/Deck000001/deck.md)") {
		t.Fatalf("README %q", g.gh.text(head, "README.md"))
	}

	// nothing changed: no commit, the picture not read again
	g.f.bucket.RemovePrefix(g.ctx, "shares/Deck000001/media/")
	res, err := g.c.sync(g.ctx, "u-tero", "", "")
	if err != nil || res.Commit != "" || g.gh.head() != head {
		t.Fatalf("a commit of nothing: %+v %v", res, err)
	}

	// Save version: the message first, only the changed file sent
	g.f.db.Update(g.ctx, "shares", "Deck000001", Doc{"md": "# Roadmap\n\n## One\n\nFirst, edited.\n"})
	g.gh.mu.Lock()
	g.gh.calls = nil
	g.gh.mu.Unlock()
	_, body := g.do(t, "POST", "/editor/api/git/save", "tero", `{"deck":"Deck000001","message":"Before the review"}`)
	var out gitSyncResult
	json.Unmarshal([]byte(body), &out)
	if out.Commit == "" || g.gh.head() != out.Commit {
		t.Fatalf("save: %s", body)
	}
	c := g.gh.commits[out.Commit]
	if !strings.HasPrefix(c.Message, "Before the review\n\nDeck \"Roadmap\": text changed") || c.Author.Name != "Tero" {
		t.Fatalf("commit %q by %q", c.Message, c.Author.Name)
	}
	blobs := 0
	for _, call := range g.gh.calls {
		if call == "POST /repos/tero/sliqtly-decks/git/blobs" {
			blobs++
		}
	}
	if blobs != 1 {
		t.Fatalf("%d blobs sent for one changed file: %v", blobs, g.gh.calls)
	}

	// a deck deleted here goes from the repository (its history stays)
	g.f.db.Delete(g.ctx, "shares", "Deck000001")
	if _, err := g.c.sync(g.ctx, "u-tero", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.gh.files(g.gh.head())["decks/Deck000001/deck.md"]; ok {
		t.Fatal("a deleted deck stayed")
	}
}

func TestGitTakesInGitHubChanges(t *testing.T) {
	g := newGitTest(t)
	base := "# Plan\n\n## A\n\nOne.\n\n## B\n\nTwo.\n\n## C\n\nThree.\n"
	g.deck("Deck000001", "u-tero", base, nil)
	g.connect(t)
	// GitHub changes slide A, Sliqtly slide C: both are kept
	g.gh.push("edit on github", map[string]string{"decks/Deck000001/deck.md": strings.Replace(base, "One.", "One, from GitHub.", 1)})
	g.f.db.Update(g.ctx, "shares", "Deck000001", Doc{"md": strings.Replace(base, "Three.", "Three, from Sliqtly.", 1)})
	res, err := g.c.sync(g.ctx, "u-tero", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(strings.Replace(base, "One.", "One, from GitHub.", 1), "Three.", "Three, from Sliqtly.", 1)
	if got := g.md("Deck000001"); got != want {
		t.Fatalf("merged:\n%s", got)
	}
	if got := g.gh.text(g.gh.head(), "decks/Deck000001/deck.md"); got != want {
		t.Fatalf("committed:\n%s", got)
	}
	if len(res.Imported) != 1 || res.Commit == "" {
		t.Fatalf("result %+v", res)
	}

	// the same line on both sides: Sliqtly's stays, GitHub's is kept to choose
	g.gh.push("clash", map[string]string{"decks/Deck000001/deck.md": strings.Replace(want, "Two.", "Two (GitHub).", 1)})
	g.f.db.Update(g.ctx, "shares", "Deck000001", Doc{"md": strings.Replace(want, "Two.", "Two (Sliqtly).", 1)})
	res, err = g.c.sync(g.ctx, "u-tero", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 || !strings.Contains(g.md("Deck000001"), "Two (Sliqtly).") {
		t.Fatalf("conflict: %+v\n%s", res, g.md("Deck000001"))
	}
	st, _ := g.c.status(g.ctx, "u-tero")
	if cs := st["conflicts"].([]any); len(cs) != 1 {
		t.Fatalf("conflicts listed: %v", st["conflicts"])
	}
	// GitHub's text chosen: the next sync commits it
	res2, body := g.do(t, "POST", "/editor/api/git/resolve", "tero", `{"deck":"Deck000001","take":"github"}`)
	if res2.StatusCode != 200 || !strings.Contains(g.md("Deck000001"), "Two (GitHub).") {
		t.Fatalf("resolve: %s\n%s", body, g.md("Deck000001"))
	}
	if _, err := g.c.sync(g.ctx, "u-tero", "", ""); err != nil {
		t.Fatal(err)
	}
	if got := g.gh.text(g.gh.head(), "decks/Deck000001/deck.md"); !strings.Contains(got, "Two (GitHub).") {
		t.Fatalf("after resolve:\n%s", got)
	}
}

func TestGitNewDeckFromGitHub(t *testing.T) {
	g := newGitTest(t)
	g.connect(t)
	g.gh.push("new deck", map[string]string{
		"decks/NewDeck0001/deck.md":     "# Written in VS Code\n\n## Hello\n",
		"decks/NewDeck0001/deck.json":   `{"name": "From the repo", "theme": "white"}`,
		"decks/NewDeck0001/media/a.svg": "<svg/>",
		"decks/../x/deck.md":            "# bad\n",
	})
	res, err := g.c.sync(g.ctx, "u-tero", "", "")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := g.f.db.Get(g.ctx, "shares", "NewDeck0001")
	if d == nil || d["owner"] != "u-tero" || d["visibility"] != "private" || d["name"] != "From the repo" || d["theme"] != "white" {
		t.Fatalf("made: %v (%+v)", d, res)
	}
	files := asList(d["files"])
	if len(files) != 1 || files[0].(map[string]any)["path"] != "media/a.svg" {
		t.Fatalf("files %v", files)
	}
	if b, err := g.f.bucket.Read(g.ctx, "shares/NewDeck0001/media/a.svg", 100); err != nil || string(b) != "<svg/>" {
		t.Fatalf("picture %q %v", b, err)
	}
	// a removal on GitHub does not remove the deck here
	g.gh.push("removed", map[string]string{"decks/NewDeck0001/deck.md": "", "decks/NewDeck0001/deck.json": "", "decks/NewDeck0001/media/a.svg": ""})
	if _, err := g.c.sync(g.ctx, "u-tero", "", ""); err != nil {
		t.Fatal(err)
	}
	if d, _ := g.f.db.Get(g.ctx, "shares", "NewDeck0001"); d == nil {
		t.Fatal("removed here too")
	}
	// another account's deck id on GitHub is left alone
	g.deck("AnnasDeck01", "u-anna", "# Anna\n", nil)
	g.gh.push("someone else's", map[string]string{"decks/AnnasDeck01/deck.md": "# Taken\n"})
	if _, err := g.c.sync(g.ctx, "u-tero", "", ""); err != nil {
		t.Fatal(err)
	}
	if g.md("AnnasDeck01") != "# Anna\n" {
		t.Fatal("another account's deck was changed")
	}
	if got := g.gh.text(g.gh.head(), "decks/AnnasDeck01/deck.md"); got != "# Taken\n" {
		t.Fatalf("a file not ours removed from the repository: %q", got)
	}
}

func TestGitPublicRepoRefused(t *testing.T) {
	g := newGitTest(t)
	g.deck("Deck000001", "u-tero", "# Secret\n", nil)
	g.connect(t)
	g.gh.mu.Lock()
	g.gh.private = false
	g.gh.mu.Unlock()
	head := g.gh.head()
	g.f.db.Update(g.ctx, "shares", "Deck000001", Doc{"md": "# Secret 2\n"})
	_, err := g.c.sync(g.ctx, "u-tero", "", "")
	if err == nil || !strings.Contains(err.Error(), "public") || g.gh.head() != head {
		t.Fatalf("pushed to a public repository: %v", err)
	}
	// and a public one is not connected unless the person says it may be
	if err := g.c.connect(g.ctx, "u-tero", 7, 42, false); err == nil {
		t.Fatal("a public repository connected")
	}
	if _, body := g.do(t, "POST", "/editor/api/git/public", "tero", `{}`); !strings.Contains(body, "ok") {
		t.Fatalf("public: %s", body)
	}
	if _, err := g.c.sync(g.ctx, "u-tero", "", ""); err != nil || g.gh.text(g.gh.head(), "decks/Deck000001/deck.md") != "# Secret 2\n" {
		t.Fatalf("not written once allowed: %v", err)
	}
}

func TestGitHookAndHistory(t *testing.T) {
	g := newGitTest(t)
	g.deck("Deck000001", "u-tero", "# A\n\nOne.\n", Doc{"editors": []any{"anna@example.com"}})
	g.connect(t)
	first := g.gh.head()
	g.gh.push("from GitHub", map[string]string{"decks/Deck000001/deck.md": "# A\n\nOne and two.\n"})
	ev := fmt.Sprintf(`{"ref":"refs/heads/main","after":%q,"repository":{"id":42}}`, g.gh.head())
	req, _ := http.NewRequest("POST", g.base+"/editor/github/hook", strings.NewReader(ev))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", "sha256=bad")
	res, _ := http.DefaultClient.Do(req)
	if res.StatusCode != 401 {
		t.Fatalf("unsigned hook: %d", res.StatusCode)
	}
	mac := hmac.New(sha256.New, []byte("hook-secret"))
	mac.Write([]byte(ev))
	req, _ = http.NewRequest("POST", g.base+"/editor/github/hook", strings.NewReader(ev))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != 204 || g.md("Deck000001") != "# A\n\nOne and two.\n" {
		t.Fatalf("hook: %d %q", res.StatusCode, g.md("Deck000001"))
	}

	// the history, to the owner and to the invited editor
	for _, who := range []string{"tero", "anna"} {
		_, body := g.do(t, "GET", "/editor/api/history/Deck000001", who, "")
		var list struct {
			Commits []deckCommit `json:"commits"`
		}
		json.Unmarshal([]byte(body), &list)
		if len(list.Commits) != 2 || list.Commits[1].ID != first || list.Commits[0].Subject != "from GitHub" {
			t.Fatalf("%s: history %s", who, body)
		}
	}
	_, body := g.do(t, "GET", "/editor/api/history/Deck000001/"+first, "tero", "")
	var v deckVersion
	json.Unmarshal([]byte(body), &v)
	if v.MD != "# A\n\nOne.\n" || v.Name != "A" {
		t.Fatalf("version %s", body)
	}
	_, body = g.do(t, "GET", "/editor/api/history/Deck000001/"+g.gh.head()+"/changes", "tero", "")
	if !strings.Contains(body, `"path":"deck.md","kind":"changed"`) {
		t.Fatalf("changes %s", body)
	}
	_, body = g.do(t, "GET", "/editor/api/history/Deck000001/"+first+"/file/deck.md", "tero", "")
	if body != "# A\n\nOne.\n" {
		t.Fatalf("file %q", body)
	}
	// a stranger sees none of it
	g.deck("Other00001", "u-anna", "# Anna's own\n", nil)
	res, _ = g.do(t, "GET", "/editor/api/history/Deck000001", "", "")
	if res.StatusCode != 401 {
		t.Fatalf("signed out: %d", res.StatusCode)
	}
}

func TestGitSignInWithGitHub(t *testing.T) {
	g := newGitTest(t)
	res, _ := g.do(t, "GET", "/editor/github/login?mode=in&nonce=0123456789abcdef0123&next=/editor/d/Deck000001", "", "")
	to, _ := url.Parse(res.Header.Get("Location"))
	_, body := g.do(t, "GET", "/editor/github/callback?code=good-code&state="+url.QueryEscape(to.Query().Get("state")), "", "")
	sum := sha256.Sum256([]byte("0123456789abcdef0123"))
	if !strings.Contains(body, `"token":"custom:gh-12410648"`) || !strings.Contains(body, hex.EncodeToString(sum[:])) || !strings.Contains(body, `"next":"/editor/d/Deck000001"`) {
		t.Fatalf("sign-in page: %s", body)
	}
	// connected to a Google account, the GitHub account signs that one in
	g2 := newGitTest(t)
	g2.connect(t)
	res, _ = g2.do(t, "GET", "/editor/github/login?mode=in&nonce=0123456789abcdef0123", "", "")
	to, _ = url.Parse(res.Header.Get("Location"))
	_, body = g2.do(t, "GET", "/editor/github/callback?code=good-code&state="+url.QueryEscape(to.Query().Get("state")), "", "")
	if !strings.Contains(body, `"token":"custom:u-tero"`) {
		t.Fatalf("connected account: %s", body)
	}
	// the login page offers GitHub
	_, page := g.do(t, "GET", "/editor/", "", "")
	if !strings.Contains(page, `<button class="secondary" id="github">`) {
		t.Fatal("no GitHub button")
	}
	g.c.mint = nil
	_, page = g.do(t, "GET", "/editor/", "", "")
	if strings.Contains(page, `<button class="secondary" id="github">`) {
		t.Fatal("a GitHub button without sign-in")
	}
}

func TestGitCreateRepo(t *testing.T) {
	g := newGitTest(t)
	g.gh.inst = nil
	g.deck("Deck000001", "u-tero", "# New home\n", nil)
	res, _ := g.do(t, "GET", "/editor/github/login?mode=create&name=sliqtly-decks", "tero", "")
	to, _ := url.Parse(res.Header.Get("Location"))
	res, body := g.do(t, "GET", "/editor/github/callback?code=good-code&state="+url.QueryEscape(to.Query().Get("state")), "tero", "")
	if res.StatusCode != 302 {
		t.Fatalf("create: %d %s", res.StatusCode, body)
	}
	l, _, _ := g.c.link(g.ctx, "u-tero")
	if l.Repo != "tero/sliqtly-decks" || l.RepoID != 99 || !l.connected() {
		t.Fatalf("link %+v", l)
	}
	// the first sync goes into the new repository
	if _, err := g.c.sync(g.ctx, "u-tero", "", ""); err != nil {
		t.Fatal(err)
	}
	if got := g.gh.text(g.gh.head(), "decks/Deck000001/deck.md"); got != "# New home\n" {
		t.Fatalf("deck.md %q", got)
	}
}

// the layout document of web/rooms.js's facts
func layoutDocOf(facts map[string]string) Doc {
	l := layoutDoc{facts: map[string]layoutFact{}}
	for k, v := range facts {
		v := v
		l.facts[k] = layoutFact{V: &v, S: 1, T: 1}
	}
	return l.doc("r1")
}
