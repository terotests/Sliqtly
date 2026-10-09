// SPDX-License-Identifier: AGPL-3.0-or-later

package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// a GitHub of our own: OAuth, /user and the workflow runs
type fakeGitHub struct {
	srv       *httptest.Server
	lastAuth  atomic.Value
	lastPath  atomic.Value
	exchanges atomic.Int32
	verifier  atomic.Value
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{}
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.exchanges.Add(1)
		f.verifier.Store(r.Form.Get("code_verifier"))
		if r.Form.Get("client_secret") != "s3cret" || r.Form.Get("code") != "good-code" || r.Form.Get("code_verifier") == "" {
			// GitHub answers a bad code with 200 and an error
			json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"access_token": "gho_user1", "token_type": "bearer", "scope": "repo:status"})
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gho_user1" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"login": "terotests", "id": 1})
	})
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		f.lastAuth.Store(r.Header.Get("Authorization"))
		f.lastPath.Store(r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer gho_user1" {
			w.WriteHeader(401)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/big") {
			w.Write([]byte(`{"x":"` + strings.Repeat("a", 5000) + `"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/away") {
			http.Redirect(w, r, "https://evil.example/steal", http.StatusFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"total_count": 2,
			"workflow_runs": []any{
				map[string]any{"name": "CI", "conclusion": "success", "head_sha": "abc", "actor": map[string]any{"login": "a"}},
				map[string]any{"name": "Deploy", "conclusion": "failure", "head_sha": "def", "actor": map[string]any{"login": "b"}},
			},
		})
	})
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) host() string {
	u, _ := url.Parse(f.srv.URL)
	return u.Hostname()
}

func writeConnector(t *testing.T, dir string, f *fakeGitHub, extra string) {
	t.Helper()
	cfg := `{
  "id": "github", "title": "GitHub", "type": "http",
  "baseUrl": "` + f.srv.URL + `",
  "egress": ["` + f.host() + `"],
  "oauth": {
    "clientId": "Iv1.test", "clientSecret": {"env": "TEST_GH_SECRET"},
    "scopes": ["read:user", "repo:status"],
    "authorizeUrl": "` + f.srv.URL + `/login/oauth/authorize",
    "tokenUrl": "` + f.srv.URL + `/login/oauth/access_token",
    "userUrl": "` + f.srv.URL + `/user", "userField": "login"
  },
  "operations": {
    "getWorkflowRuns": {
      "path": "/repos/{owner}/{repo}/actions/runs",
      "in": {"type": "object", "required": ["owner", "repo"], "properties": {
        "owner": {"type": "string", "pattern": "^[A-Za-z0-9-]+$"},
        "repo": {"type": "string", "pattern": "^[A-Za-z0-9._-]+$"},
        "per_page": {"type": "integer", "minimum": 1, "maximum": 50}
      }},
      "pick": ["total_count", "workflow_runs[].name", "workflow_runs[].conclusion"]
    },
    "big": {"path": "/repos/big"},
    "away": {"path": "/repos/away"}
  },
  "limits": {"perMinute": 5, "maxResponseBytes": 1000}` + extra + `
}`
	if err := os.WriteFile(filepath.Join(dir, "github.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func openTest(t *testing.T, f *fakeGitHub) (*Gateway, string) {
	t.Helper()
	t.Setenv("TEST_GH_SECRET", "s3cret")
	dir := t.TempDir()
	writeConnector(t, dir, f, "")
	g, err := Open(dir, f.srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Registry().Problems) > 0 {
		t.Fatalf("problems: %v", g.Registry().Problems)
	}
	return g, dir
}

// connect signs "tero" in through the fake GitHub's OAuth
func connect(t *testing.T, g *Gateway, who string) {
	t.Helper()
	addr, err := g.StartOAuth("github", who, "http://127.0.0.1:8080/connectors/oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(addr)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "Iv1.test" || q.Get("scope") != "read:user repo:status" {
		t.Fatalf("authorize address: %s", addr)
	}
	if _, _, err := g.FinishOAuth(context.Background(), q.Get("state"), "good-code"); err != nil {
		t.Fatal(err)
	}
}

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err != nil {
		return "other: " + err.Error()
	}
	return ""
}

func TestOAuthAndCall(t *testing.T) {
	f := newFakeGitHub(t)
	g, dir := openTest(t, f)
	ctx := context.Background()
	call := Call{Who: "tero", Deck: "AbCdEf12", Connector: "github", Op: "getWorkflowRuns", Args: map[string]any{"owner": "terotests", "repo": "sliqtly", "per_page": 5.0}}

	// no sign-in yet: still refused first for the grant, and asked
	if _, err := g.Do(ctx, call); code(err) != "forbidden" {
		t.Fatalf("without a grant: %v", err)
	}
	_, reqs := g.Grants().List()
	if len(reqs) != 1 || reqs[0].Op != "getWorkflowRuns" || reqs[0].Deck != "AbCdEf12" {
		t.Fatalf("requests: %+v", reqs)
	}
	if err := g.Grants().Approve("AbCdEf12", "github", []string{"getWorkflowRuns"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, reqs := g.Grants().List(); len(reqs) != 0 {
		t.Fatalf("approved request still open: %+v", reqs)
	}
	if _, err := g.Do(ctx, call); code(err) != "not_connected" {
		t.Fatalf("before connecting: %v", err)
	}
	connect(t, g, "tero")
	if ok, acct, _ := g.Connection("github", "tero"); !ok || acct != "terotests" {
		t.Fatalf("connection: %v %q", ok, acct)
	}
	out, err := g.Do(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"total_count": 2.0, "workflow_runs": []any{
		map[string]any{"name": "CI", "conclusion": "success"},
		map[string]any{"name": "Deploy", "conclusion": "failure"},
	}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("picked answer:\n got %#v\nwant %#v", out, want)
	}
	if p := f.lastPath.Load().(string); p != "/repos/terotests/sliqtly/actions/runs?per_page=5" {
		t.Fatalf("path %s", p)
	}
	// someone else on the same deck has no token of their own
	other := call
	other.Who = "maija"
	if _, err := g.Do(ctx, other); code(err) != "not_connected" {
		t.Fatalf("another person used tero's token: %v", err)
	}
	// the token is not in the folder as plain text
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			b, _ := os.ReadFile(p)
			if strings.Contains(string(b), "gho_user1") || strings.Contains(string(b), "s3cret") {
				t.Errorf("%s holds a secret in plain text", p)
			}
		}
		return nil
	})
	// the audit log has the calls, without the arguments
	log, _ := os.ReadFile(filepath.Join(dir, "audit.log"))
	if !strings.Contains(string(log), `"status":"forbidden"`) || !strings.Contains(string(log), `"status":"ok"`) || strings.Contains(string(log), "terotests/sliqtly") {
		t.Fatalf("audit:\n%s", log)
	}
	if err := g.Disconnect("github", "tero"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Do(ctx, call); code(err) != "not_connected" {
		t.Fatalf("after disconnect: %v", err)
	}
}

func TestOAuthState(t *testing.T) {
	f := newFakeGitHub(t)
	g, _ := openTest(t, f)
	ctx := context.Background()
	if _, _, err := g.FinishOAuth(ctx, "made-up", "good-code"); code(err) != "bad_request" {
		t.Fatalf("unknown state: %v", err)
	}
	addr, _ := g.StartOAuth("github", "tero", "http://x/cb")
	u, _ := url.Parse(addr)
	state := u.Query().Get("state")
	if _, _, err := g.FinishOAuth(ctx, state, "bad-code"); code(err) != "remote" {
		t.Fatalf("bad code: %v", err)
	}
	// a state is used once, even when its exchange failed
	if _, _, err := g.FinishOAuth(ctx, state, "good-code"); code(err) != "bad_request" {
		t.Fatalf("state used twice: %v", err)
	}
	// the verifier sent matches the challenge in the address
	addr, _ = g.StartOAuth("github", "tero", "http://x/cb")
	u, _ = url.Parse(addr)
	g.FinishOAuth(ctx, u.Query().Get("state"), "good-code")
	ch := u.Query().Get("code_challenge")
	v := f.verifier.Load().(string)
	if s := challengeFor(v); s != ch {
		t.Fatalf("PKCE challenge %s does not match verifier (%s)", ch, s)
	}
	// without the client secret in the environment, no sign-in starts
	t.Setenv("TEST_GH_SECRET", "")
	if _, err := g.StartOAuth("github", "tero", "http://x/cb"); code(err) != "not_configured" {
		t.Fatalf("no secret: %v", err)
	}
}

func TestCallChecks(t *testing.T) {
	f := newFakeGitHub(t)
	g, _ := openTest(t, f)
	ctx := context.Background()
	connect(t, g, "tero")
	g.Grants().Approve("*", "github", []string{"*"}, "admin")
	base := Call{Who: "tero", Deck: "D1", Connector: "github", Op: "getWorkflowRuns"}
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"owner": "terotests"}, "bad_request"},                                     // repo missing
		{map[string]any{"owner": "../x", "repo": "y"}, "bad_request"},                             // pattern
		{map[string]any{"owner": "a", "repo": "b", "per_page": 500.0}, "bad_request"},             // maximum
		{map[string]any{"owner": "a", "repo": "b", "per_page": 1.5}, "bad_request"},               // integer
		{map[string]any{"owner": "a", "repo": "b", "url": "https://evil.example"}, "bad_request"}, // unknown
	} {
		c := base
		c.Args = tc.args
		if _, err := g.Do(ctx, c); code(err) != tc.want {
			t.Errorf("%v: got %v, want %s", tc.args, err, tc.want)
		}
	}
	if _, err := g.Do(ctx, Call{Who: "tero", Deck: "D1", Connector: "github", Op: "big"}); code(err) != "too_large" {
		t.Errorf("big answer: %v", err)
	}
	if _, err := g.Do(ctx, Call{Who: "tero", Deck: "D1", Connector: "github", Op: "away"}); code(err) != "remote" {
		t.Errorf("redirect off the hosts: %v", err)
	}
	if _, err := g.Do(ctx, Call{Who: "tero", Deck: "D1", Connector: "nope", Op: "x"}); code(err) != "not_found" {
		t.Errorf("unknown connector: %v", err)
	}
	// perMinute 5: the calls above used it up
	c := base
	c.Args = map[string]any{"owner": "a", "repo": "b"}
	var last error
	for i := 0; i < 6; i++ {
		_, last = g.Do(ctx, c)
	}
	if code(last) != "quota" {
		t.Errorf("limit: %v", last)
	}
}

func TestAdminTestNeedsNoGrant(t *testing.T) {
	f := newFakeGitHub(t)
	g, _ := openTest(t, f)
	connect(t, g, "local")
	out, err := g.Do(context.Background(), Call{Who: "local", Connector: "github", Op: "getWorkflowRuns", Args: map[string]any{"owner": "a", "repo": "b"}, Admin: true})
	if err != nil || out == nil {
		t.Fatalf("admin test: %v", err)
	}
	if _, reqs := g.Grants().List(); len(reqs) != 0 {
		t.Fatalf("admin test made a request: %+v", reqs)
	}
}

func TestGrantsPersistAndRevoke(t *testing.T) {
	dir := t.TempDir()
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	s, _ := OpenGrants(dir, now)
	s.Approve("D1", "github", []string{"a", "b"}, "admin")
	s.Approve("D1", "github", []string{"c"}, "admin")
	s2, err := OpenGrants(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Allowed("D1", "github", "c") || !s2.Allowed("D1", "github", "a") || s2.Allowed("D2", "github", "a") {
		t.Fatal("grants not kept")
	}
	s2.Revoke("D1", "github", []string{"a"})
	if s2.Allowed("D1", "github", "a") || !s2.Allowed("D1", "github", "b") {
		t.Fatal("revoke one op")
	}
	s2.Revoke("D1", "github", nil)
	if s2.Allowed("D1", "github", "b") {
		t.Fatal("revoke all")
	}
	s2.Ask("D3", "github", "x", "tero")
	s2.Ask("D3", "github", "x", "tero")
	_, r := s2.List()
	if len(r) != 1 || r[0].Count != 2 {
		t.Fatalf("requests: %+v", r)
	}
	s2.Dismiss("D3", "github", "x")
	if _, r := s2.List(); len(r) != 0 {
		t.Fatal("dismiss")
	}
}

func TestTokensBoundToPerson(t *testing.T) {
	dir := t.TempDir()
	tk, err := OpenTokens(dir)
	if err != nil {
		t.Fatal(err)
	}
	tk.Put("github", "tero", &Token{AccessToken: "x"})
	// a file moved to another person's place does not open
	b, _ := os.ReadFile(tk.path("github", "tero"))
	os.MkdirAll(filepath.Dir(tk.path("github", "maija")), 0o700)
	os.WriteFile(tk.path("github", "maija"), b, 0o600)
	if _, err := tk.Get("github", "maija"); err == nil {
		t.Fatal("moved token opened for another person")
	}
	// another server's key does not open it
	other := t.TempDir()
	os.MkdirAll(filepath.Join(other, "tokens", "github"), 0o700)
	os.WriteFile(filepath.Join(other, "tokens", "github", filepath.Base(tk.path("github", "tero"))), b, 0o600)
	tk2, _ := OpenTokens(other)
	if _, err := tk2.Get("github", "tero"); err == nil {
		t.Fatal("token opened with another key")
	}
	if info, _ := os.Stat(filepath.Join(dir, "key")); info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", info.Mode().Perm())
	}
}

func TestConfigChecks(t *testing.T) {
	ok := `{"id":"x","baseUrl":"https://api.example.com","operations":{"get":{"path":"/a/{id}","in":{"required":["id"],"properties":{"id":{"type":"string"}}}}}}`
	if _, err := Parse([]byte(ok)); err != nil {
		t.Fatalf("good config: %v", err)
	}
	for name, bad := range map[string]string{
		"http":           `{"id":"x","baseUrl":"http://api.example.com","operations":{"g":{"path":"/a"}}}`,
		"egress":         `{"id":"x","baseUrl":"https://api.example.com","egress":["other.com"],"operations":{"g":{"path":"/a"}}}`,
		"literal secret": `{"id":"x","baseUrl":"https://a.com","auth":{"secret":"abc"},"operations":{"g":{"path":"/a"}}}`,
		"path param":     `{"id":"x","baseUrl":"https://a.com","operations":{"g":{"path":"/a/{id}"}}}`,
		"dotdot":         `{"id":"x","baseUrl":"https://a.com","operations":{"g":{"path":"/a/../b"}}}`,
		"auth header":    `{"id":"x","baseUrl":"https://a.com","headers":{"Authorization":"x"},"operations":{"g":{"path":"/a"}}}`,
		"own id":         `{"id":"sliqtly","baseUrl":"https://a.com","operations":{"g":{"path":"/a"}}}`,
		"unknown field":  `{"id":"x","baseUrl":"https://a.com","token":"abc","operations":{"g":{"path":"/a"}}}`,
		"user no oauth":  `{"id":"x","baseUrl":"https://a.com","identity":"user","operations":{"g":{"path":"/a"}}}`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	c, _ := Parse([]byte(`{"id":"gh","baseUrl":"https://api.github.com","oauth":{"provider":"github","clientId":"a","clientSecret":{"env":"GH"}},"operations":{"g":{"path":"/user"}}}`))
	if c.OAuth.TokenURL != "https://github.com/login/oauth/access_token" || c.Identity != "user" {
		t.Fatalf("github preset: %+v", c.OAuth)
	}
	pub, _ := json.Marshal(c.Public())
	if strings.Contains(string(pub), "GH") || strings.Contains(string(pub), "clientId") {
		t.Fatalf("public view shows secret names: %s", pub)
	}
}

func TestPick(t *testing.T) {
	var v any
	json.Unmarshal([]byte(`{"a":1,"b":{"c":2,"d":3},"list":[{"x":1,"y":{"z":2}},{"x":3,"y":{"z":4}}]}`), &v)
	got, _ := json.Marshal(Pick(v, []string{"a", "b.c", "list[].y.z", "missing.q"}))
	if string(got) != `{"a":1,"b":{"c":2},"list":[{"y":{"z":2}},{"y":{"z":4}}]}` {
		t.Fatalf("pick: %s", got)
	}
	var arr any
	json.Unmarshal([]byte(`[{"n":1,"m":2},{"n":3,"m":4}]`), &arr)
	got, _ = json.Marshal(Pick(arr, []string{"n"}))
	if string(got) != `[{"n":1},{"n":3}]` {
		t.Fatalf("pick top array: %s", got)
	}
}
