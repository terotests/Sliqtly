// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/terotests/sliqtly/mcp-go/connectors"
)

// a GitHub of our own (OAuth, /user, one repository's workflow runs) and
// a connector to it in a folder
func fakeGitHubConnector(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("code") != "good-code" || r.Form.Get("client_secret") != "s3cret" {
			json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"access_token": "gho_x", "token_type": "bearer"})
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"login": "terotests"})
	})
	mux.HandleFunc("/repos/terotests/sliqtly/actions/runs", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gho_x" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"total_count":1,"workflow_runs":[{"name":"CI","conclusion":"success","secret_field":"x"}]}`))
	})
	gh := httptest.NewTLSServer(mux)
	t.Cleanup(gh.Close)
	u, _ := url.Parse(gh.URL)
	dir := t.TempDir()
	cfg := `{"id":"github","title":"GitHub","baseUrl":"` + gh.URL + `","egress":["` + u.Hostname() + `"],
 "oauth":{"clientId":"Iv1.t","clientSecret":{"env":"TEST_CONNECTOR_GH_SECRET"},
  "authorizeUrl":"` + gh.URL + `/login/oauth/authorize","tokenUrl":"` + gh.URL + `/login/oauth/access_token","userUrl":"` + gh.URL + `/user","userField":"login"},
 "operations":{"getWorkflowRuns":{"path":"/repos/{owner}/{repo}/actions/runs",
  "in":{"required":["owner","repo"],"properties":{"owner":{"type":"string"},"repo":{"type":"string"}}},
  "pick":["workflow_runs[].name","workflow_runs[].conclusion"]}}}`
	if err := os.WriteFile(filepath.Join(dir, "github.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_CONNECTOR_GH_SECRET", "s3cret")
	return gh, dir
}

func TestConnectorsAPI(t *testing.T) {
	gh, dir := fakeGitHubConnector(t)
	srv, _ := startV1(t, "tok", func(ls *localServer) {
		g, err := connectors.Open(dir, gh.Client(), nil)
		if err != nil {
			t.Fatal(err)
		}
		ls.conn = g
	})
	auth := []string{"Authorization", "Bearer tok"}
	deck := v1Do(t, "POST", srv.URL+"/api/v1/decks", map[string]any{"name": "Status", "markdown": "# Build"}, auth...).want(t, 201).body["id"].(string)

	v1Do(t, "GET", srv.URL+"/api/v1/connectors", nil).want(t, 401)
	list := v1Do(t, "GET", srv.URL+"/api/v1/connectors", nil, auth...).want(t, 200)
	if !strings.Contains(list.raw, `"id":"github"`) || !strings.Contains(list.raw, `"connected":false`) || strings.Contains(list.raw, "TEST_CONNECTOR_GH_SECRET") {
		t.Fatalf("list: %s", list.raw)
	}
	if list.body["callback"] != srv.URL+"/connectors/oauth/callback" {
		t.Fatalf("callback for the admin: %v", list.body["callback"])
	}

	// connect: the browser goes to the service, which sends it back
	go1 := v1Do(t, "POST", srv.URL+"/api/v1/connectors/github/connect", map[string]any{}, auth...).want(t, 200)
	u, _ := url.Parse(go1.body["url"].(string))
	if u.Query().Get("redirect_uri") != srv.URL+"/connectors/oauth/callback" {
		t.Fatalf("redirect_uri %s", u.Query().Get("redirect_uri"))
	}
	bad := v1Do(t, "GET", srv.URL+"/connectors/oauth/callback?state=nope&code=good-code", nil).want(t, 400)
	if !strings.Contains(bad.raw, "Not connected") {
		t.Fatalf("bad state page: %s", bad.raw)
	}
	page := v1Do(t, "GET", srv.URL+"/connectors/oauth/callback?state="+url.QueryEscape(u.Query().Get("state"))+"&code=good-code", nil).want(t, 200)
	if !strings.Contains(page.raw, "Connected") {
		t.Fatalf("callback page: %s", page.raw)
	}
	if l := v1Do(t, "GET", srv.URL+"/api/v1/connectors", nil, auth...); !strings.Contains(l.raw, `"account":"terotests"`) {
		t.Fatalf("after connect: %s", l.raw)
	}

	call := map[string]any{"deck": deck, "connector": "github", "op": "getWorkflowRuns", "args": map[string]any{"owner": "terotests", "repo": "sliqtly"}}
	if a := v1Do(t, "POST", srv.URL+"/api/v1/connectors/call", call, auth...).want(t, 403); a.body["code"] != "forbidden" {
		t.Fatalf("no grant: %s", a.raw)
	}
	reqs := v1Do(t, "GET", srv.URL+"/api/v1/connectors/grants", nil, auth...).want(t, 200)
	if !strings.Contains(reqs.raw, `"op":"getWorkflowRuns"`) {
		t.Fatalf("request not listed: %s", reqs.raw)
	}
	v1Do(t, "POST", srv.URL+"/api/v1/connectors/grants", map[string]any{"deck": deck, "connector": "github", "ops": []string{"nope"}}, auth...).want(t, 400)
	v1Do(t, "POST", srv.URL+"/api/v1/connectors/grants", map[string]any{"deck": deck, "connector": "github", "ops": []string{"getWorkflowRuns"}}, auth...).want(t, 200)
	res := v1Do(t, "POST", srv.URL+"/api/v1/connectors/call", call, auth...).want(t, 200)
	if res.raw != `{"result":{"workflow_runs":[{"conclusion":"success","name":"CI"}]}}`+"\n" {
		t.Fatalf("result: %s", res.raw)
	}
	// a deck that is not there
	call["deck"] = "NoSuchDeck1"
	v1Do(t, "POST", srv.URL+"/api/v1/connectors/call", call, auth...).want(t, 404)

	// the settings page's way in, from this computer
	if s := v1Do(t, "GET", srv.URL+"/api/settings/connectors", nil).want(t, 200); s.body["admin"] != true {
		t.Fatalf("settings: %s", s.raw)
	}
	test := v1Do(t, "POST", srv.URL+"/api/settings/connectors/call", map[string]any{"test": true, "connector": "github", "op": "getWorkflowRuns", "args": map[string]any{"owner": "terotests", "repo": "sliqtly"}}).want(t, 200)
	if !strings.Contains(test.raw, `"CI"`) {
		t.Fatalf("admin test: %s", test.raw)
	}
	v1Do(t, "DELETE", srv.URL+"/api/v1/connectors/github/connection", map[string]any{}, auth...).want(t, 204)
	call["deck"] = deck
	if a := v1Do(t, "POST", srv.URL+"/api/v1/connectors/call", call, auth...).want(t, 409); a.body["code"] != "not_connected" {
		t.Fatalf("after disconnect: %s", a.raw)
	}
}

func TestConnectorsOffByDefault(t *testing.T) {
	srv, _ := startV1(t, "", nil)
	v1Do(t, "GET", srv.URL+"/api/v1/connectors", nil).want(t, 404)
	v1Do(t, "GET", srv.URL+"/api/settings/connectors", nil).want(t, 404)
	if a := v1Do(t, "GET", srv.URL+"/connectors/oauth/callback?state=x&code=y", nil); strings.Contains(a.raw, "Connected") {
		t.Fatalf("callback answered with connectors off: %s", a.raw)
	}
}

func TestConnectorAdmin(t *testing.T) {
	ls := &localServer{env: &Env{LocalUser: "local"}, admins: []string{"Tero@Example.com"}}
	here := httptest.NewRequest("GET", "/", nil)
	here.RemoteAddr = "127.0.0.1:5000"
	away := httptest.NewRequest("GET", "/", nil)
	away.RemoteAddr = "10.0.0.7:5000"
	for _, tc := range []struct {
		r    *http.Request
		who  *principal
		want bool
	}{
		{here, &principal{ID: "local"}, true},
		{away, &principal{ID: "local"}, false},
		{here, &principal{ID: "sub-1", Email: "maija@example.com"}, false},
		{away, &principal{ID: "sub-2", Email: "tero@example.com"}, true},
		{here, nil, false},
	} {
		if got := ls.connectorAdmin(tc.r, tc.who); got != tc.want {
			t.Errorf("%s %+v: %v", tc.r.RemoteAddr, tc.who, got)
		}
	}
}
