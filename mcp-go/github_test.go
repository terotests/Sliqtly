// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"strings"
	"testing"
)

// --- read_github_pr (rgr/GitHub.rgr)

func TestGitHubRefs(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/terotests/Sliqtly/pull/107":       "terotests/Sliqtly#107",
		"https://github.com/terotests/Sliqtly/pull/107/files": "terotests/Sliqtly#107",
		"github.com/a-b/c.d/pull/7#discussion_r1":             "a-b/c.d#7",
		" terotests/Sliqtly#12 ":                              "terotests/Sliqtly#12",
		"terotests/Sliqtly/pull/3":                            "terotests/Sliqtly#3",
		"https://github.com/terotests/Sliqtly/issues/107":     "",
		"Sliqtly#107":                    "",
		"terotests/Sliqtly#0":            "",
		"terotests/Slic tly#1":           "",
		"https://github.com/../x/pull/1": "",
	} {
		r := GhRef_static_parse(in)
		got := ""
		if r.err == "" {
			got = r.name()
		}
		eq(t, got, want, in)
	}
	eq(t, GhRef_static_parse("o/r#5").api(), "https://api.github.com/repos/o/r/pulls/5")
}

func TestGitHubPatchTrimAndLanguage(t *testing.T) {
	p := "@@ -1,3 +1,3 @@\n a\n-b\n+c\n@@ -20,2 +20,9 @@\n x\n+1\n+2\n+3\n+4\n+5\n+6\n+7\n"
	eq(t, GhDeck_static_trimPatch(p, 40), strings.TrimRight(p, "\n"), "short: all of it")
	eq(t, GhDeck_static_trimPatch(p, 8), "@@ -1,3 +1,3 @@\n a\n-b\n+c", "whole hunks while they fit")
	eq(t, GhDeck_static_trimPatch("@@ -1 +1,9 @@\n+1\n+2\n+3\n+4\n+5", 3), "@@ -1 +1,9 @@\n+1\n+2", "else the first hunk cut")
	eq(t, GhDeck_static_langOf("src/a/Main.TS"), "ts")
	eq(t, GhDeck_static_langOf("x.mjs"), "js")
	eq(t, GhDeck_static_langOf("README.md"), "")
	eq(t, GhDeck_static_summary("<!-- template -->\r\nFixes the total.\r\n\r\n![shot](x.png)\r\n\r\nAlso VAT."), "Fixes the total.\n\nAlso VAT.")
	eq(t, GhDeck_static_summary(""), "No description.")
}

func ghJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestReadGitHubPr(t *testing.T) {
	api := "https://api.github.com/repos/terotests/Sliqtly/pulls/107"
	var auth []string
	gh := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		auth = append(auth, r.Header.Get("authorization"))
		switch r.URL.String() {
		case api:
			return respond(200, "application/json", ghJSON(map[string]any{
				"title": "Totals with VAT", "body": "Adds VAT to the total.", "user": map[string]any{"login": "tero"},
				"state": "closed", "merged_at": "2026-10-01T10:00:00Z", "draft": false,
				"base": map[string]any{"ref": "main"}, "head": map[string]any{"ref": "vat", "sha": "headsha1", "repo": map[string]any{"full_name": "tero/Sliqtly-fork"}},
				"additions": 12, "deletions": 3, "changed_files": 2, "commits": 2,
			})), nil
		case api + "/files?per_page=100":
			return respond(200, "application/json", ghJSON([]any{
				map[string]any{"filename": "docs/notes.md", "status": "modified", "additions": 1, "deletions": 0, "patch": "@@ -1 +1,2 @@\n a\n+b"},
				map[string]any{"filename": "src/total.js", "status": "modified", "additions": 11, "deletions": 3, "patch": "@@ -10,3 +10,4 @@\n let s = 0\n-for (const r of rows) s += r.a\n+for (const r of rows) {\n+  s += r.a * (1 + r.vat)\n+}\n\\ No newline at end of file"},
				map[string]any{"filename": "logo.png", "status": "added", "additions": 0, "deletions": 0},
			})), nil
		case api + "/commits?per_page=100":
			return respond(200, "application/json", ghJSON([]any{
				map[string]any{"sha": "abcdef1234", "commit": map[string]any{"message": "feat: VAT\n\nbody", "author": map[string]any{"name": "Tero", "date": "2026-09-30T08:00:00Z"}}, "author": map[string]any{"login": "tero"}},
				map[string]any{"sha": "1234567890", "commit": map[string]any{"message": "tests", "author": map[string]any{"name": "Tero", "date": "2026-10-01T08:00:00Z"}}},
			})), nil
		case "https://api.github.com/repos/tero/Sliqtly-fork/contents/src/total.js?ref=headsha1":
			if r.Header.Get("accept") != "application/vnd.github.raw+json" {
				return respond(415, "text/plain", "raw only"), nil
			}
			return respond(200, "text/plain", "export class Total extends Sum {\n  rows: Row[] = []\n  vat(rate) { return 1 }\n}\nclass Row { a: number }\n"), nil
		case "https://api.github.com/repos/terotests/Hidden/pulls/1":
			return respond(404, "application/json", `{"message":"Not Found"}`), nil
		}
		return fakeNet.Transport.RoundTrip(r)
	})}
	env := testEnv(nil, nil)
	env.Client = gh
	env.GitHubToken = "ghtok"
	s := start(t, env, "")
	defer s.close()

	r := call(t, s, "read_github_pr", map[string]any{"pr": "https://github.com/terotests/Sliqtly/pull/107"})
	if r.IsError {
		t.Fatal(textOf(r))
	}
	eq(t, auth[0], "Bearer ghtok", "the server's token goes to GitHub")
	o := sc(r)
	eq(t, o["state"], "merged")
	eq(t, len(list(o["files"])), 3)
	eq(t, len(list(o["commits"])), 2)
	md := o["markdown"].(string)
	match(t, textOf(r), `^terotests/Sliqtly#107: "Totals with VAT" by tero, merged; 2 files, \+12 −3, 2 commits\.`)
	for _, want := range []string{
		"# Totals with VAT\n",
		"## What and why\n\nAdds VAT to the total.\n\n- **2 files**, +12 −3\n- 2 commits, `vat` → `main`",
		"| `src/total.js` | modified | 11 | 3 |\n| `docs/notes.md` | modified | 1 | 0 |",
		"## total.js · +11 −3\n\n```diff js {.numbers}\n@@ -10,3 +10,4 @@\n",
		"```timeline\n- 2026-09-30: feat – VAT\n- 2026-10-01: tests\n```",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("the draft has no %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "No newline") {
		t.Fatal("git's no-newline note is not a line of the diff:\n" + md)
	}
	// the changed files' classes, read whole at the head commit of the fork
	for _, want := range []string{"## Classes in the change\n\n```mermaid\nclassDiagram\n", "Sum <|-- Total", "Total --> \"*\" Row : rows"} {
		if !strings.Contains(md, want) {
			t.Fatalf("the draft has no %q:\n%s", want, md)
		}
	}
	if !strings.Contains(o["uml"].(string), "class Total {") {
		t.Fatal(o["uml"])
	}
	if strings.Index(md, "## total.js") > strings.Index(md, "## notes.md") {
		t.Fatal("the biggest change goes first")
	}

	// the draft is a deck: it is made without warnings about its blocks
	f := fakeFirebase()
	s2 := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s2.close()
	c := call(t, s2, "create_presentation", map[string]any{"title": "Review", "markdown": md})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	for _, w := range list(sc(c)["warnings"]) {
		if strings.Contains(w.(string), "block") {
			t.Fatal(w)
		}
	}

	match(t, textOf(call(t, s, "read_github_pr", map[string]any{"pr": "terotests/Hidden#1"})), `no pull request terotests/Hidden#1 that this server can read`)
	match(t, textOf(call(t, s, "read_github_pr", map[string]any{"pr": "nonsense"})), `^Invalid arguments: pr is a pull request's link`)
}

// a private repository read with the server's token: only for the users the
// server names, since anyone may call it
func TestGitHubPrivateRepos(t *testing.T) {
	api := "https://api.github.com/repos/tero/Secret/pulls/3"
	gh := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case api:
			return respond(200, "application/json", ghJSON(map[string]any{"title": "Secret", "state": "open",
				"base": map[string]any{"ref": "main", "repo": map[string]any{"private": true}}, "head": map[string]any{"ref": "x"}})), nil
		case api + "/files?per_page=100", api + "/commits?per_page=100":
			return respond(200, "application/json", "[]"), nil
		case "https://api.github.com/repos/tero/Secret":
			return respond(200, "application/json", `{"private":true}`), nil
		case "https://api.github.com/repos/tero/Secret/contents/src?ref=main":
			return respond(200, "application/json", `[{"type":"file","name":"a.go","path":"src/a.go","size":40}]`), nil
		case "https://api.github.com/repos/tero/Secret/contents/src/a.go?ref=main":
			return respond(200, "text/plain", "package a\ntype A struct {\n\tB *B\n}\ntype B struct{}\n"), nil
		}
		return respond(404, "application/json", `{}`), nil
	})}
	for _, c := range []struct {
		users  []string
		signed bool
		want   string
	}{
		{nil, false, `tero/Secret is private\. Sign in to Sliqtly`},
		{[]string{"someone"}, true, `only for the users it names\. Your Sliqtly user id is u1`},
		{[]string{"x", "u1"}, true, ``},
	} {
		f := fakeFirebase()
		env := testEnv(&f, nil)
		env.Client = gh
		env.GitHubToken = "ghtok"
		env.GitHubUsers = c.users
		tok := ""
		if c.signed {
			env = withSignIn(env)
			tok = signIn(f)
		}
		s := start(t, env, tok)
		for _, r := range []*mcp.CallToolResult{
			call(t, s, "read_github_pr", map[string]any{"pr": "tero/Secret#3"}),
			call(t, s, "source_uml", map[string]any{"github": "https://github.com/tero/Secret/tree/main/src"}),
		} {
			if c.want == "" {
				if r.IsError {
					t.Fatal(textOf(r))
				}
			} else {
				if !r.IsError {
					t.Fatal("a private repository was read for", c.users, c.signed)
				}
				match(t, textOf(r), c.want)
			}
		}
		s.close()
	}
	eq(t, strings.Join(githubUsers(" u1, u2 ,u3"), "|"), "u1|u2|u3")
}
