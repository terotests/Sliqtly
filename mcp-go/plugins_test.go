// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// --- plugins (rgr/Plugins.rgr) and the code review plugin (rgr/plugins/)

func toolDescription(t *testing.T, s *testServer, name string) string {
	t.Helper()
	tools, err := s.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range tools.Tools {
		if x.Name == name {
			return x.Description
		}
	}
	return ""
}

func TestPluginsOffByDefault(t *testing.T) {
	s := start(t, testEnv(nil, nil), "")
	defer s.close()
	eq(t, toolDescription(t, s, "sliqtly_plugin"), "", "no plugin tool without plugins")
	if strings.Contains(textOf(call(t, s, "sliqtly_guide", map[string]any{})), "## Plugins") {
		t.Fatal("Core names no plugins when none is on")
	}
	if strings.Contains(s.session.InitializeResult().Instructions, "sliqtly_plugin") {
		t.Fatal("the instructions name no plugins when none is on")
	}
	r := call(t, s, "sliqtly_plugin", map[string]any{"name": "code-review"})
	if !r.IsError {
		t.Fatal("a plugin that is off does not answer")
	}
	match(t, textOf(r), `^This server has no plugins on\.`)
}

func TestPluginsOn(t *testing.T) {
	env := testEnv(nil, nil)
	env.Plugins = []string{"code-review"}
	s := start(t, env, "")
	defer s.close()
	match(t, toolDescription(t, s, "sliqtly_plugin"), "`code-review`: when reviewing a pull request.*Call sliqtly_plugin\\(name=\"code-review\"\\) before starting\\.")
	match(t, textOf(call(t, s, "sliqtly_guide", map[string]any{})), "## Plugins\n\n[^\n]+\n\n\\| Plugin \\| Call it when \\|\n\\|---\\|---\\|\n\\| `code-review` \\| reviewing a pull request")
	if strings.Contains(textOf(call(t, s, "sliqtly_guide", map[string]any{"topic": "charts"})), "## Plugins") {
		t.Fatal("a topic is only its topic")
	}
	match(t, s.session.InitializeResult().Instructions, `Plugins: for reviewing a pull request.*call sliqtly_plugin\(name="code-review"\) first\.$`)

	r := call(t, s, "sliqtly_plugin", map[string]any{"name": "code-review"})
	if r.IsError {
		t.Fatal(textOf(r))
	}
	match(t, textOf(r), `^# Plugin: code-review\n`)
	match(t, textOf(r), `plugin_version \d{4}-\d\d-\d\d(\.\d+)?$`)
	eq(t, sc(r)["ops"], []any{"start", "build"})
	match(t, sc(r)["instructions"].(string), `^# Plugin: code-review\n[\s\S]*## The model`)

	match(t, textOf(call(t, s, "sliqtly_plugin", map[string]any{"name": "code-review", "op": "merge"})), `has no operation "merge"\. Operations: start, build\.`)
	match(t, textOf(call(t, s, "sliqtly_plugin", map[string]any{"name": "lint"})), `no plugin "lint" on this server\. Plugins: code-review\.`)
	match(t, textOf(call(t, s, "sliqtly_plugin", map[string]any{"name": "code-review", "op": "build", "args": map[string]any{}})), `args\.pr is the pull request's link`)

	all := testEnv(nil, nil)
	all.Plugins = []string{"all"}
	s2 := start(t, all, "")
	defer s2.close()
	if toolDescription(t, s2, "sliqtly_plugin") == "" {
		t.Fatal(`"all" turns every plugin on`)
	}
}

func TestCodeReviewHints(t *testing.T) {
	patch := "@@ -10,3 +10,6 @@\n const a = 1\n-old()\n+await db.save(row)\n+if (err != nil) { throw err }\n \n+x = 2\n+emit(\"done\")"
	hs := CodeReview_static_hints("src/a.ts", patch, 10)
	got := []string{}
	for _, h := range hs {
		got = append(got, h.ref+" "+strings.Join(h.kinds, ","))
	}
	eq(t, got, []string{"src/a.ts:11 wait,store", "src/a.ts:12 failure", "src/a.ts:15 event"})
	eq(t, len(CodeReview_static_hints("src/a.ts", patch, 1)), 1, "at most max")
	eq(t, CodeReview_static_lineCount("a\nb\n"), int64(2))
	eq(t, CodeReview_static_lineCount("a\nb"), int64(2))
	eq(t, CodeReview_static_lineCount(""), int64(0))
	for in, want := range map[string]string{
		"src/a.ts:12": "src/a.ts 12 12 true", "src/a.ts:3-9": "src/a.ts 3 9 true",
		"src/a.ts": "src/a.ts 0 0 true", "src/a.ts:9-3": "src/a.ts 9 3 false", "c:\\x:y": "c:\\x -1 -1 false",
	} {
		r := CrRef_static_parse(in)
		eq(t, strings.Join([]string{r.path, itoa(r.from), itoa(r.to), map[bool]string{true: "true", false: "false"}[r.ok]}, " "), want, in)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

// a review model of a small change, as an assistant would write it
func reviewModel() map[string]any {
	return map[string]any{
		"title": "Approval state",
		"actors": []any{
			map[string]any{"id": "user", "label": "Reviewer", "kind": "user", "refs": []any{"web/review.tsx:2"}},
			map[string]any{"id": "api", "label": "Review API", "kind": "service", "change": "changed", "refs": []any{"src/api.ts:1-4"}},
			map[string]any{"id": "mailer", "label": "Mailer", "kind": "external", "refs": []any{"src/mail.ts:1"}},
		},
		"stores": []any{
			map[string]any{"id": "db", "label": "reviews table", "kind": "db", "durable": true, "fields": []any{"review_status"}, "change": "changed", "refs": []any{"db/031.sql:1"}},
		},
		"states": []any{
			map[string]any{"id": "draft", "label": "Draft", "of": "review", "refs": []any{"src/states.ts:1"}},
			map[string]any{"id": "inreview", "label": "In review", "of": "review", "refs": []any{"src/states.ts:2"}},
			map[string]any{"id": "approved", "label": "Approved", "of": "review", "change": "added", "refs": []any{"src/states.ts:3"}},
		},
		"transitions": []any{
			map[string]any{"from": "draft", "to": "inreview", "trigger": "SUBMIT", "refs": []any{"src/states.ts:5"}},
			map[string]any{"from": "inreview", "to": "approved", "trigger": "APPROVE", "change": "added", "refs": []any{"src/states.ts:6"}},
			map[string]any{"from": "inreview", "to": "draft", "trigger": "REJECT", "guard": "role:reviewer", "refs": []any{"src/states.ts:7"}},
		},
		"flows": []any{
			map[string]any{"id": "f1", "from": "user", "to": "api", "payload": "ApproveRequest", "critical": true, "refs": []any{"web/review.tsx:3"}},
			map[string]any{"id": "f2", "from": "api", "to": "db", "payload": "review row", "transform": "map", "critical": true, "change": "changed", "refs": []any{"src/api.ts:3"}},
		},
		"events": []any{
			map[string]any{"id": "e1", "name": "review.approved", "from": "api", "to": []any{"mailer"}, "change": "added", "refs": []any{"src/api.ts:4"}},
		},
		"waits": []any{
			map[string]any{"id": "w1", "who": "api", "on": "mailer", "what": "send result", "blocking": true, "refs": []any{"src/api.ts:4"}},
		},
		"failures": []any{
			map[string]any{"id": "x1", "at": "f2", "when": "DB write fails", "then": "return 500", "path": "error", "refs": []any{"src/api.ts:99"}},
		},
		"slices": []any{
			map[string]any{"id": "approve", "label": "Reviewer approves", "items": []any{"api", "f2"}},
		},
		"narrative": map[string]any{"summary": "Adds an Approved state.", "critical_path": []any{"user", "f1", "api", "f2", "db"}},
	}
}

func TestCodeReviewPlugin(t *testing.T) {
	api := "https://api.github.com/repos/tero/app/pulls/5"
	sha := "0123456789abcdef0123456789abcdef01234567"
	contents := map[string]string{
		"web/review.tsx": "a\nb\nc\n", "src/api.ts": "1\n2\n3\n4\n", "src/mail.ts": "m\n",
		"db/031.sql": "alter table\n", "src/states.ts": "1\n2\n3\n4\n5\n6\n7\n",
	}
	big := ""
	for i := 1; i <= 900; i++ {
		big += fmt.Sprintf("line %d\n", i)
	}
	contents["src/big.ts"] = big
	gh := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		u := r.URL.String()
		switch u {
		case api:
			return respond(200, "application/json", ghJSON(map[string]any{"title": "Approval", "state": "open", "user": map[string]any{"login": "tero"},
				"base": map[string]any{"ref": "main"}, "head": map[string]any{"ref": "approve", "sha": sha}, "changed_files": 2, "additions": 6, "deletions": 1})), nil
		case api + "/files?per_page=100":
			return respond(200, "application/json", ghJSON([]any{
				map[string]any{"filename": "src/api.ts", "status": "modified", "additions": 3, "deletions": 1, "patch": "@@ -1,2 +1,4 @@\n 1\n-2\n+2\n+await db.save(row)\n+emit(\"review.approved\")"},
				map[string]any{"filename": "src/states.ts", "status": "modified", "additions": 3, "deletions": 0, "patch": "@@ -1,4 +1,7 @@\n 1\n 2\n+3\n"},
			})), nil
		case api + "/commits?per_page=100":
			return respond(200, "application/json", "[]"), nil
		}
		const pre = "https://api.github.com/repos/tero/app/contents/"
		if strings.HasPrefix(u, pre) && strings.HasSuffix(u, "?ref="+sha) {
			if r.Header.Get("accept") != "application/vnd.github.raw" {
				t.Errorf("files are read raw: %s", r.Header.Get("accept"))
			}
			if c, ok := contents[strings.TrimSuffix(strings.TrimPrefix(u, pre), "?ref="+sha)]; ok {
				return respond(200, "text/plain", c), nil
			}
		}
		return respond(404, "application/json", `{}`), nil
	})}
	env := testEnv(nil, nil)
	env.Client = gh
	env.Plugins = []string{"code-review"}
	s := start(t, env, "")
	defer s.close()

	st := call(t, s, "sliqtly_plugin", map[string]any{"name": "code-review", "op": "start", "args": map[string]any{"pr": "tero/app#5"}})
	if st.IsError {
		t.Fatal(textOf(st))
	}
	eq(t, sc(st)["head_sha"], sha)
	match(t, textOf(st), `(?m)^- src/api\.ts:3 \[wait,store\] await db\.save\(row\)$`)
	match(t, textOf(st), `(?m)^- src/api\.ts:4 \[event\] emit`)

	b := call(t, s, "sliqtly_plugin", map[string]any{"name": "code-review", "op": "build", "args": map[string]any{"pr": "tero/app#5", "model": reviewModel()}})
	if b.IsError {
		t.Fatal(textOf(b))
	}
	o := sc(b)
	eq(t, o["inferred"], []any{"x1"}, "src/api.ts:99 is no line of the file")
	eq(t, o["risk"], "high", "a blocking wait on an outside service")
	risks := []string{}
	for _, r := range list(o["risks"]) {
		m := r.(map[string]any)
		risks = append(risks, m["level"].(string)+" "+m["text"].(string))
	}
	eq(t, risks, []string{
		`high Review API blocks on the outside service Mailer`,
		`medium Review API writes to reviews table with no validation on the way`,
		`medium Review API waits for send result with no timeout`,
		`low the added transition In review → Approved has no guard`,
	})
	steps := []string{}
	for _, x := range list(o["order"]) {
		m := x.(map[string]any)
		steps = append(steps, m["step"].(string))
	}
	eq(t, steps, []string{"Entry point", "Critical path", "State changes", "Side effects", "Failure handling"}, "the store is on the critical path already")
	md := o["markdown"].(string)
	for _, want := range []string{
		"# Approval state\n\n[tero/app#5](https://github.com/tero/app/pull/5) · Risk: **high**\n{.lead}",
		"## What changed\n\nAdds an Approved state.\n\n- **Critical path:** Reviewer → Review API → reviews table",
		"1. **Entry point**: Reviewer · [web/review.tsx:2](https://github.com/tero/app/blob/" + sha + "/web/review.tsx#L2)",
		"## Overview\n\n```mermaid\nflowchart TD\n",
		"  n_user ==> n_api\n",
		"  n_api ==>|map| n_db\n",
		"  n_api -.->|DB write fails| n_x1\n",
		"  class n_x1 inferred",
		"## States of review\n\n```xstate\n",
		`"initial": "Draft"`,
		`"type": "final"`,
		"## Data flow\n\n```mermaid\nflowchart LR\n",
		"  n_f2[/\"review row\"/]\n  n_api ==> n_f2\n  n_f2 ==>|map| n_db\n",
		"## When something fails\n\n```mermaid\n",
		"## Who waits for whom\n\n```mermaid\nsequenceDiagram\n",
		"  Note over n_api,n_mailer: no timeout",
		"| **[reviews table](https://github.com/tero/app/blob/" + sha + "/db/031.sql#L1)** | db, durable | `review_status` | Review API | changed |",
		"::: notes\n- failure **DB write fails** (same): inferred, no source line",
		"## Risks\n\n- **high** Review API blocks on the outside service Mailer",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("the deck has no %q:\n%s", want, md)
		}
	}
	match(t, textOf(b), `Slices \(args\.slice builds the deck for one\): approve\.`)
	// the source files the deck talks about, for the source viewer
	files := map[string]string{}
	for _, x := range list(o["files"]) {
		m := x.(map[string]any)
		files[m["name"].(string)] = m["text"].(string)
	}
	eq(t, files["code/src/api.ts"], "1\n2\n3\n4\n")
	eq(t, files["code/src/states.ts.diff"], "--- a/src/states.ts\n+++ b/src/states.ts\n@@ -1,4 +1,7 @@\n 1\n 2\n+3\n")
	var srcs struct {
		Repos map[string]map[string]string
		Files []struct {
			Path, Status, Diff string
			Lines              int
		}
	}
	if err := json.Unmarshal([]byte(files["code/sources.json"]), &srcs); err != nil {
		t.Fatal(err)
	}
	eq(t, srcs.Repos["app"]["head"], sha)
	eq(t, srcs.Repos["app"]["change_url"], "https://github.com/tero/app/pull/5")
	eq(t, srcs.Files[1].Path+" "+srcs.Files[1].Status+" "+srcs.Files[1].Diff, "src/api.ts modified src/api.ts.diff")
	for _, want := range []string{
		"## Critical path: Review API\n\n[src/api.ts:1-4](https://github.com/tero/app/blob/" + sha + "/src/api.ts#L1-L4)\n\n```diff ts {.numbers lines=1-4}\n@@ -1,2 +1,4 @@\n 1\n-2\n+2\n",
		"::: code\n- src/api.ts#L1-4 \"Review API\"\n:::",
		"```\n\n::: code\nn_user web/review.tsx#L2 \"Reviewer\"\nn_api src/api.ts#L1-4 \"Review API\"\n",
		"Draft src/states.ts#L1 \"Draft\"\n- src/states.ts#L2 \"In review\"\n",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("the deck has no %q:\n%s", want, md)
		}
	}
	match(t, textOf(b), `Source files: 5 under code/`)

	// a long file: only the lines the deck talks about, with their numbers
	bm := reviewModel()
	bm["actors"] = append(bm["actors"].([]any), map[string]any{"id": "log", "label": "Logger", "refs": []any{"src/big.ts:500-502", "src/big.ts:505"}})
	bb := sc(call(t, s, "sliqtly_plugin", map[string]any{"name": "code-review", "op": "build", "args": map[string]any{"pr": "tero/app#5", "model": bm}}))
	for _, x := range list(bb["files"]) {
		m := x.(map[string]any)
		files[m["name"].(string)] = m["text"].(string)
	}
	want := ""
	for i := 494; i <= 511; i++ {
		want += fmt.Sprintf("line %d\n", i)
	}
	eq(t, files["code/src/big.ts"], want)
	match(t, files["code/sources.json"], `\{"path":"src/big\.ts","repo":"app","commit":"[0-9a-f]+","status":"same","lines":900,"ranges":\[\[494,511\]\]\}`)

	// no story given: one drafted from the model, the critical path as
	// "therefore", what can go wrong on it as "but"
	match(t, textOf(b), `The story was drafted from the model\.`)
	for _, want := range []string{
		"## The story\n\n- Reviewer passes ApproveRequest to Review API · ",
		"\n- **Therefore** Review API writes review row to reviews table (map) · ",
		"\n- **Therefore** APPROVE moves it from In review to Approved · ",
		"\n- **But** when DB write fails, return 500",
		"{.build}\n\n::: notes\nDrafted from the model",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("the deck has no %q:\n%s", want, md)
		}
	}

	// a story told "and then", in beats of one length, is told so
	told := reviewModel()
	told["narrative"] = map[string]any{"summary": "x", "story": []any{
		map[string]any{"text": "A reviewer approves the draft now."},
		map[string]any{"link": "then", "text": "The API saves the new state."},
		map[string]any{"link": "therefore", "text": "The mailer sends the author mail."},
		map[string]any{"link": "but", "text": "Nobody waits.", "refs": []any{"src/api.ts:4"}},
	}}
	tb := call(t, s, "sliqtly_plugin", map[string]any{"name": "code-review", "op": "build", "args": map[string]any{"pr": "tero/app#5", "model": told}})
	tw, _ := json.Marshal(sc(tb)["warnings"])
	match(t, string(tw), `narrative\.story beat 2 is joined by then: say what gets in the way`)
	match(t, string(tw), `narrative\.story beats 1–3 are all about 6 words long: vary the length`)
	if strings.Contains(textOf(tb), "drafted from the model") {
		t.Fatal("a story given is the story told")
	}
	match(t, sc(tb)["markdown"].(string), `## The story

- A reviewer approves the draft now\.
- \*\*Then\*\* The API saves the new state\.
- \*\*Therefore\*\* The mailer sends the author mail\.
- \*\*But\*\* Nobody waits\. · \[src/api\.ts:4\]`)

	// the deck is drawn without a block it cannot show
	f := fakeFirebase()
	s2 := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s2.close()
	c := call(t, s2, "create_presentation", map[string]any{"title": "Review", "markdown": md, "files": o["files"]})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	for _, w := range list(sc(c)["warnings"]) {
		if strings.Contains(w.(string), "block") || strings.Contains(w.(string), "diagram") || strings.Contains(w.(string), "code") {
			t.Fatal(w)
		}
	}

	// a source file keeps its repository path, and is sent as text
	for _, bad := range []map[string]any{{"name": "code/../x.ts", "text": "x"}, {"name": "code/src/a.ts", "data_base64": "eA=="}} {
		e := call(t, s2, "create_presentation", map[string]any{"title": "Bad", "markdown": "# x", "files": []any{bad}})
		if !e.IsError {
			t.Fatalf("%v was taken", bad)
		}
	}

	// one slice: its items and what touches them
	sl := call(t, s, "sliqtly_plugin", map[string]any{"name": "code-review", "op": "build", "args": map[string]any{"pr": "tero/app#5", "model": reviewModel(), "slice": "approve"}})
	if sl.IsError {
		t.Fatal(textOf(sl))
	}
	smd := sc(sl)["markdown"].(string)
	if !strings.Contains(smd, "# Reviewer approves") || strings.Contains(smd, "## States of review") {
		t.Fatal("the slice's deck:\n" + smd)
	}
	match(t, textOf(call(t, s, "sliqtly_plugin", map[string]any{"name": "code-review", "op": "build", "args": map[string]any{"pr": "tero/app#5", "model": reviewModel(), "slice": "pay"}})), `no slice "pay"\. Slices: approve\.`)

	// the model's mistakes are told
	bad := reviewModel()
	bad["flows"] = append(bad["flows"].([]any), map[string]any{"id": "f3", "from": "api", "to": "nowhere", "refs": []any{"src/api.ts:2"}})
	bad["transitions"] = append(bad["transitions"].([]any), map[string]any{"from": "draft", "to": "db", "refs": []any{"src/states.ts:5"}})
	w := sc(call(t, s, "sliqtly_plugin", map[string]any{"name": "code-review", "op": "build", "args": map[string]any{"pr": "tero/app#5", "model": bad}}))["warnings"]
	ws, _ := json.Marshal(w)
	match(t, string(ws), `flow f3 points at \\"nowhere\\", which is no item`)
	match(t, string(ws), `transition transition4 goes to or from store \\"db\\"`)
	match(t, string(ws), `x1: src/api\.ts:99 is no line`)
}
