// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func toolDescriptions(t *testing.T, s *testServer) string {
	t.Helper()
	tools, err := s.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(tools.Tools)
	return string(b)
}

func TestNamingRule(t *testing.T) {
	dir := t.TempDir()
	srv, session := startLocal(t, dir, "")
	s := &testServer{root: srv.URL, session: session}

	// off until it is turned on: any name, and nothing said about names
	if c := call(t, s, "create_presentation", map[string]any{"title": "Anything", "markdown": "# A\n"}); c.IsError {
		t.Fatal(textOf(c))
	}
	if d := toolDescriptions(t, s); contains(d, "Naming rule") {
		t.Fatal("a rule was described with none set")
	}
	code, body := req(t, "GET", srv.URL+"/api/settings", "", "")
	eq(t, code, 200)
	match(t, body, `"enabled":false`)
	match(t, body, `"pattern":"\^\(\[A-Z\]`)

	// a bad rule is not kept
	code, body = req(t, "PUT", srv.URL+"/api/settings", "application/json", `{"naming":{"enabled":true,"pattern":"([A-Z","example":"x"}}`)
	eq(t, code, 400)
	match(t, body, `not a regular expression`)
	code, body = req(t, "PUT", srv.URL+"/api/settings", "application/json", `{"naming":{"enabled":true,"pattern":"^([A-Z]+-[0-9]+) ","example":"no key"}}`)
	eq(t, code, 400)
	match(t, body, `does not match the pattern`)
	code, _ = req(t, "PUT", srv.URL+"/api/settings", "text/plain", `{}`)
	eq(t, code, 415)

	// a name tried on the page
	code, body = req(t, "POST", srv.URL+"/api/settings/check", "application/json", `{"naming":{"pattern":"^([A-Z][A-Z0-9]+-[0-9]+) +\\S","example":"ABC-1234 Review"},"name":"OPS-77 Launch"}`)
	eq(t, code, 200)
	match(t, body, `"ok":true`)
	match(t, body, `"key":"OPS-77"`)

	// turned on
	code, body = req(t, "PUT", srv.URL+"/api/settings", "application/json", `{"naming":{"enabled":true,"pattern":"^([A-Z][A-Z0-9]+-[0-9]+) +\\S","example":"ABC-1234 Quarterly review","rule":"Start with the ticket key."}}`)
	eq(t, code, 200)
	match(t, body, `"offCount":1,"offNames":\[\]`)
	// the names only when the decks are listed
	code, _ = req(t, "PUT", srv.URL+"/api/settings/listing", "application/json", `{"enabled":true}`)
	eq(t, code, 200)
	_, body = req(t, "GET", srv.URL+"/api/settings", "", "")
	match(t, body, `"offNames":\["Anything"\]`)

	d := toolDescriptions(t, s)
	match(t, d, `Naming rule on this server: Start with the ticket key\. For example: \\"ABC-1234 Quarterly review\\"`)
	guide := textOf(call(t, s, "sliqtly_guide", map[string]any{}))
	match(t, guide, `## Naming presentations on this server`)

	bad := call(t, s, "create_presentation", map[string]any{"title": "Quarterly review", "markdown": "# Q\n"})
	if !bad.IsError {
		t.Fatal("a name without a key was taken")
	}
	match(t, textOf(bad), `Not created: the name "Quarterly review" does not follow this server's naming rule\. Start with the ticket key\. For example: "ABC-1234 Quarterly review"`)
	good := call(t, s, "create_presentation", map[string]any{"title": "OPS-12 Quarterly review", "markdown": "# Q\n"})
	if good.IsError {
		t.Fatal(textOf(good))
	}
	id := sc(good)["deck_id"].(string)
	upd := call(t, s, "update_presentation", map[string]any{"deck_id": id, "title": "Renamed"})
	if !upd.IsError {
		t.Fatal("a rename without a key was taken")
	}
	match(t, textOf(upd), `Not updated`)
	// a change that leaves the name alone needs no rule
	if c := call(t, s, "update_presentation", map[string]any{"deck_id": id, "markdown": "# Q2\n"}); c.IsError {
		t.Fatal(textOf(c))
	}
	list := sc(call(t, s, "list_presentations", map[string]any{}))
	keys := map[string]any{}
	for _, x := range list["presentations"].([]any) {
		m := x.(map[string]any)
		keys[m["title"].(string)] = m["key"]
	}
	eq(t, keys["OPS-12 Quarterly review"], "OPS-12")
	eq(t, keys["Anything"], nil)
	session.Close()
	srv.Close()

	// kept in the folder: a new server on it has the rule
	srv2, session2 := startLocal(t, dir, "")
	defer srv2.Close()
	defer session2.Close()
	s2 := &testServer{root: srv2.URL, session: session2}
	if c := call(t, s2, "create_presentation", map[string]any{"title": "No key", "markdown": "# N\n"}); !c.IsError {
		t.Fatal("the rule was forgotten")
	}
	code, body = req(t, "GET", srv2.URL+"/settings", "", "")
	eq(t, code, 200)
	match(t, body, `Names of presentations`)
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
