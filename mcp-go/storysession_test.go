// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"
)

// A Story's session and a fork with its context, end to end on a folder
// server: an assistant logs its work, forks the Story, the fork starts with
// the history up to the fork point and the deck's public files, not its
// review comments; the parent's session tells of the fork; the page forks
// as a person.
func TestStorySessionAndFork(t *testing.T) {
	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	s := &testServer{root: srv.URL, session: session}
	ok := func(name string, args map[string]any) map[string]any {
		t.Helper()
		r := call(t, s, name, args)
		if r.IsError {
			t.Fatalf("%s: %s", name, textOf(r))
		}
		return sc(r)
	}
	bad := func(name string, args map[string]any, want string) {
		t.Helper()
		r := call(t, s, name, args)
		if !r.IsError {
			t.Fatalf("%s worked: %s", name, textOf(r))
		}
		match(t, textOf(r), want)
	}
	main := ok("create_presentation", map[string]any{"title": "Passkey support", "markdown": DECK,
		"files": []any{map[string]any{"name": "req.csv", "text": "need,why\npasskeys,phishing\n"}}})["deck_id"].(string)
	ok("add_comment", map[string]any{"deck_id": main, "slide": 1, "text": "Is the fallback SMS?"})

	// the server logged the assistant's own deck tools
	got := ok("get_session", map[string]any{"deck_id": main})
	es := list(got["entries"])
	eq(t, len(es), 2)
	eq(t, []any{mapOf(es[0])["kind"], mapOf(es[0])["tool"], mapOf(es[1])["tool"], mapOf(mapOf(es[1])["by"])["kind"]}, []any{"tool", "create_presentation", "add_comment", "bot"})

	logged := ok("log_session", map[string]any{"deck_id": main, "agent": "Claude", "entries": []any{
		map[string]any{"kind": "prompt", "text": "We need passkeys because passwords get phished", "by": "Tero"},
		map[string]any{"kind": "tool", "tool": "read_file", "refs": []any{"file:data/req.csv"}},
		map[string]any{"kind": "reply", "text": "A spec draft is on slide 2"},
	}})
	ls := list(logged["entries"])
	eq(t, []any{mapOf(ls[0])["n"], mapOf(mapOf(ls[0])["by"])["name"], mapOf(mapOf(ls[0])["by"])["kind"]}, []any{3.0, "Tero", "person"})
	eq(t, mapOf(mapOf(ls[2])["by"])["name"], "Claude")
	bad("log_session", map[string]any{"deck_id": main, "entries": []any{map[string]any{"kind": "thought", "text": "x"}}}, `entries\[0\]: kind is one of`)
	bad("log_session", map[string]any{"deck_id": main, "entries": []any{map[string]any{"kind": "note", "text": "ok"}, map[string]any{"kind": "note", "refs": []any{"nope"}}}}, `entries\[1\]: ref`)
	// nothing of a refused batch was written
	eq(t, ok("get_session", map[string]any{"deck_id": main})["total"], 5.0)

	// fork at the tool call: the prompt and the file read come along
	bad("fork_story", map[string]any{"deck_id": main, "name": "x", "at": 9}, `at is an entry of the session, 0 to 5`)
	bad("fork_story", map[string]any{"deck_id": main, "name": "  "}, `name:`)
	fk := ok("fork_story", map[string]any{"deck_id": main, "name": "security-review", "at": 4, "agent": "Claude"})
	sec := fk["deck_id"].(string)
	eq(t, []any{fk["name"], fk["at"], fk["forked_from"], fk["skipped_files"]}, []any{"Passkey support · security-review", 4.0, main, []any{"review/comments.json"}})
	fs := ok("get_session", map[string]any{"deck_id": sec})
	eq(t, []any{fs["total"], mapOf(fs["forked_from"])["deck_id"], mapOf(fs["forked_from"])["at"]}, []any{4.0, main, 4.0})
	fe := list(fs["entries"])
	eq(t, []any{mapOf(fe[3])["tool"], mapOf(fe[3])["origin"], mapOf(fe[3])["n"]}, []any{"read_file", main, 4.0})

	// the fork's deck: the same slides and data, no review comments, a draft
	match(t, textOf(call(t, s, "get_presentation", map[string]any{"deck_id": sec})), `Passkey support · security-review`)
	files := textOf(call(t, s, "list_files", map[string]any{"deck_id": sec}))
	match(t, files, `data/req\.csv`)
	if c := ok("list_comments", map[string]any{"deck_id": sec}); c["open"] != 0.0 {
		t.Fatalf("the fork took the comments: %v", c)
	}
	csv := textOf(call(t, s, "read_file", map[string]any{"deck_id": sec, "path": "data/req.csv"}))
	match(t, csv, `phishing`)

	// work in the fork stays there
	ok("log_session", map[string]any{"deck_id": sec, "agent": "Claude", "entries": []any{map[string]any{"kind": "finding", "text": "No account recovery path"}}})
	eq(t, ok("get_session", map[string]any{"deck_id": sec})["total"], 5.0)
	// the parent's session tells of the fork; its total grew by that entry only
	ps := ok("get_session", map[string]any{"deck_id": main})
	eq(t, ps["total"], 6.0)
	last := mapOf(list(ps["entries"])[5])
	eq(t, []any{last["kind"], last["refs"]}, []any{"fork", []any{"deck:" + sec}})
	match(t, last["text"].(string), `Claude forked this Story as "security-review" at entry 4`)

	// a fork of the fork reads through both
	ux := ok("fork_story", map[string]any{"deck_id": sec, "name": "ux"})["deck_id"].(string)
	eq(t, ok("get_session", map[string]any{"deck_id": ux})["total"], 5.0)

	forks := list(ok("list_forks", map[string]any{"deck_id": main})["forks"])
	eq(t, len(forks), 1)
	f0 := mapOf(forks[0])
	eq(t, []any{f0["deck_id"], f0["name"], f0["at"], f0["entries_since"], f0["phase"]}, []any{sec, "security-review", 4.0, 2.0, "draft"})
	eq(t, len(list(ok("get_session", map[string]any{"deck_id": main})["forks"])), 1)
	// pages
	pg := ok("get_session", map[string]any{"deck_id": main, "after": 1, "limit": 2})
	eq(t, []any{len(list(pg["entries"])), pg["next_after"]}, []any{2, 3.0})
	// the link between them
	eq(t, list(ok("links_of", map[string]any{"ref": "deck:" + sec})["links"]),
		[]any{map[string]any{"from": "deck:" + sec, "rel": "forked_from", "to": "deck:" + main},
			map[string]any{"from": "deck:" + sec, "rel": "forks", "to": "deck:" + ux}})
	bad("get_session", map[string]any{"deck_id": "nothere123"}, `not found`)

	// the page forks and writes as its person
	code, out := pagePost(t, srv.URL, "fork_story", map[string]any{"deck_id": main, "name": "ux-review", "as": map[string]any{"id": "anna1", "name": "Anna"}})
	eq(t, code, 200, out)
	code, out = pagePost(t, srv.URL, "log_session", map[string]any{"deck_id": out["deck_id"], "entries": []any{map[string]any{"kind": "note", "text": "Looks good"}}, "as": map[string]any{"id": "anna1", "name": "Anna"}})
	eq(t, code, 200, out)
	eq(t, mapOf(mapOf(list(out["entries"])[0])["by"])["name"], "Anna")
	pe := list(ok("get_session", map[string]any{"deck_id": main})["entries"])
	match(t, mapOf(pe[len(pe)-1])["text"].(string), `Anna forked this Story as "ux-review"`)
}
