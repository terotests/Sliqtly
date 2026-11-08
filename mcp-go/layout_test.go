// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// sliqtly.com: the assistant sees and changes the account's topics,
// folders and stories, the ones the editor keeps too
func TestLayoutTools(t *testing.T) {
	f := fakeFirebase()
	env := withSignIn(testEnv(&f, nil))
	env.layouts = newLayoutService(env.DB)
	s := start(t, env, signIn(f))
	defer s.close()
	ctx := context.Background()
	md := "# Deck\n\n## One\n\nText"

	// the editor's words (topic, story) lead to the room tools
	_, init := rpc(t, s.root, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	match(t, mapOf(mapOf(init["result"]))["instructions"].(string), `a room is called a topic \(or a project\), a presentation in it a story[\s\S]*list_rooms`)
	lt, err := s.session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range lt.Tools {
		if tl.Name == "list_rooms" {
			eq(t, tl.Title, "List topics (rooms)")
			match(t, tl.Description, `calls a room a topic .*aihe.* each presentation in it a story`)
		}
	}

	r := call(t, s, "create_presentation", map[string]any{"title": "Plain", "markdown": md})
	plain := sc(r)["deck_id"].(string)
	rooms := sc(call(t, s, "list_rooms", map[string]any{}))["rooms"].([]any)
	eq(t, len(rooms), 2, "General and Playground")

	// a topic the editor made in this browser, synced to the server
	b, _ := json.Marshal(layoutRoom{ID: "r-web", Title: "Web launch", Created: 5})
	if _, err := env.layouts.sync(ctx, "u1", 0, []any{map[string]any{"k": "room/r-web", "v": string(b)}}, true); err != nil {
		t.Fatal(err)
	}
	r = call(t, s, "list_rooms", map[string]any{"query": "launch"})
	rooms = sc(r)["rooms"].([]any)
	eq(t, len(rooms), 1, "found by its name")
	eq(t, rooms[0].(map[string]any)["role"], "owner", "its own")

	// made into the topic: there, as a draft
	r = call(t, s, "create_presentation", map[string]any{"title": "Launch plan", "markdown": md, "room_id": "r-web"})
	if r.IsError {
		t.Fatal(textOf(r))
	}
	plan := sc(r)["deck_id"].(string)
	r = call(t, s, "create_presentation", map[string]any{"title": "Nowhere", "markdown": md, "room_id": "r-none"})
	eq(t, r.IsError, true, "no such topic")

	// a folder, and the plain deck moved into it
	fid := sc(call(t, s, "create_folder", map[string]any{"room_id": "r-web", "name": "Testing"}))["folder_id"].(string)
	mv := sc(call(t, s, "move_presentation", map[string]any{"deck_id": plain, "room_id": "r-web", "folder_id": fid}))
	eq(t, []any{mv["from_room_id"], mv["room"], mv["folder"], mv["moved"]}, []any{"general", "Web launch", "Testing", true})
	r = call(t, s, "move_presentation", map[string]any{"deck_id": "someones12", "room_id": "r-web"})
	eq(t, r.IsError, true, "not the account's deck")
	st := sc(call(t, s, "set_story", map[string]any{"deck_id": plain, "type": "review", "tags": []any{"q4"}}))
	eq(t, []any{st["type"], st["phase"]}, []any{"review", "draft"}, "made by the assistant: a draft")

	g := sc(call(t, s, "get_room", map[string]any{"room_id": "r-web"}))
	eq(t, len(g["presentations"].([]any)), 2, "both in the topic")
	eq(t, g["folders"].([]any)[0].(map[string]any)["presentations"], 1.0, "one in the folder")

	// list_presentations tells where each one is
	r = call(t, s, "list_presentations", map[string]any{})
	match(t, textOf(r), `Launch plan \(`+plan+`, [a-z]+, created [0-9-]+, in Web launch, story draft\)`)
	match(t, textOf(r), `Plain \(`+plain+`, [a-z]+, created [0-9-]+, in Web launch / Testing, review draft\)`)
	for _, d := range sc(r)["presentations"].([]any) {
		m := d.(map[string]any)
		if m["deck_id"] == plain {
			eq(t, []any{m["room_id"], m["folder_id"], m["folder"], m["type"], m["tags"]}, []any{"r-web", fid, "Testing", "review", []any{"q4"}})
		}
	}

	// the editor hears of it: the changes after the number it had
	out, err := env.layouts.sync(ctx, "u1", 1, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, out["full"], false)
	got := map[string]any{}
	for _, x := range out["facts"].([]map[string]any) {
		got[x["k"].(string)] = x["v"]
	}
	eq(t, got["deck/cloud:"+plain], "r-web", "the move")
	eq(t, got["filed/cloud:"+plain], fid, "into the folder")

	// the folder deleted: the deck at the topic's top, the page told so
	seq := out["seq"].(int64)
	del := sc(call(t, s, "delete_folder", map[string]any{"room_id": "r-web", "folder_id": fid}))
	eq(t, del["moved"], 1.0)
	out, _ = env.layouts.sync(ctx, "u1", seq, nil, false)
	removed := false
	for _, x := range out["facts"].([]map[string]any) {
		if x["k"] == "filed/cloud:"+plain && x["v"] == nil {
			removed = true
		}
	}
	eq(t, removed, true, "filed fact removed")
}

// create_presentation files the new deck in a topic and its folder in one
// call, says where it went, and says it when it could not (Tero
// 2026-11-08: assistants left decks in General with no word of it)
func TestLayoutCreateInFolder(t *testing.T) {
	f := fakeFirebase()
	env := withSignIn(testEnv(&f, nil))
	env.layouts = newLayoutService(env.DB)
	s := start(t, env, signIn(f))
	defer s.close()
	ctx := context.Background()
	md := "# Deck\n\n## One\n\nText"

	lt, err := s.session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range lt.Tools {
		if tl.Name == "create_presentation" {
			props := mapOf(mapOf(tl.InputSchema)["properties"])
			eq(t, props["room_id"] != nil && props["folder_id"] != nil, true, "room_id and folder_id offered")
		}
	}

	b, _ := json.Marshal(layoutRoom{ID: "r-mf", Title: "M-Files", Created: 5})
	if _, err := env.layouts.sync(ctx, "u1", 0, []any{map[string]any{"k": "room/r-mf", "v": string(b)}}, true); err != nil {
		t.Fatal(err)
	}
	fid := sc(call(t, s, "create_folder", map[string]any{"room_id": "r-mf", "name": "Arkkitehtuuri"}))["folder_id"].(string)

	r := call(t, s, "create_presentation", map[string]any{"title": "Plan", "markdown": md, "room_id": "r-mf", "folder_id": fid})
	if r.IsError {
		t.Fatal(textOf(r))
	}
	got := sc(r)
	id := got["deck_id"].(string)
	eq(t, []any{got["room_id"], got["folder_id"], got["room_note"]}, []any{"r-mf", fid, nil}, "where it went")
	g := sc(call(t, s, "get_room", map[string]any{"room_id": "r-mf"}))
	ps := g["presentations"].([]any)
	eq(t, len(ps), 1)
	eq(t, []any{ps[0].(map[string]any)["deck_id"], ps[0].(map[string]any)["folder_id"], ps[0].(map[string]any)["phase"]}, []any{id, fid, "draft"}, "in the folder, a draft")

	// a folder the topic does not have: nothing is written
	before := len(sc(call(t, s, "list_presentations", map[string]any{}))["presentations"].([]any))
	r = call(t, s, "create_presentation", map[string]any{"title": "X", "markdown": md, "room_id": "r-mf", "folder_id": "f-none"})
	eq(t, r.IsError, true, "unknown folder")
	match(t, textOf(r), `no folder f-none in room r-mf`)
	r = call(t, s, "create_presentation", map[string]any{"title": "X", "markdown": md, "folder_id": fid})
	eq(t, r.IsError, true, "folder without its room")
	after := len(sc(call(t, s, "list_presentations", map[string]any{}))["presentations"].([]any))
	eq(t, after, before, "none made")

	// no room given: General, and the assistant is told how to file it
	r = call(t, s, "create_presentation", map[string]any{"title": "Loose", "markdown": md})
	got = sc(r)
	eq(t, got["room_id"], "general")
	match(t, got["room_note"].(string), `move_presentation`)

	// the topic gone between the check and the write: the deck is in
	// General and the warning says so
	if msg := env.layouts.made(ctx, "u1", "abcdefghij", "r-gone"); msg == nil || !strings.Contains(msg.Error(), "no room r-gone") {
		t.Fatalf("made into a missing room: %v", msg)
	}
	l, _ := env.layouts.ls.read(ctx, "u1")
	v := viewOf(l)
	eq(t, v.roomOf(pageDeck("abcdefghij")), "general", "left in General")
	eq(t, v.storyOf(pageDeck("abcdefghij")).Phase, "draft", "still a draft")
}

// the page's sync: a first one fills in only what the server lacks; later
// ones send changes and get the others'
func TestLayoutSync(t *testing.T) {
	env, base, stop := editorServer(t)
	defer stop()
	env.layouts = newLayoutService(env.DB)
	do := func(who string, a map[string]any) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(a)
		res, body := editorDo(t, "POST", base+"/editor/api/layout", who, base, string(b))
		out := map[string]any{}
		json.Unmarshal([]byte(body), &out)
		return res.StatusCode, out
	}
	facts := func(out map[string]any) map[string]any {
		m := map[string]any{}
		l, _ := out["facts"].([]any)
		for _, x := range l {
			f := x.(map[string]any)
			m[f["k"].(string)] = f["v"]
		}
		return m
	}

	// one browser: its facts kept
	code, out := do("tero", map[string]any{"fill": true, "put": []any{map[string]any{"k": "deck/d1", "v": "r-a"}, map[string]any{"k": "order", "v": `["r-a"]`}}})
	eq(t, code, 200)
	eq(t, out["full"], true)
	eq(t, facts(out), map[string]any{"deck/d1": "r-a", "order": `["r-a"]`})
	seq := out["seq"].(float64)

	// another browser of the same account: the server's stay, its new one added
	_, out = do("tero", map[string]any{"fill": true, "put": []any{map[string]any{"k": "deck/d1", "v": "general"}, map[string]any{"k": "deck/d2", "v": "r-b"}}})
	eq(t, facts(out)["deck/d1"], "r-a", "the server's kept")
	eq(t, facts(out)["deck/d2"], "r-b", "the new one added")

	// the first browser hears only what changed since
	_, out = do("tero", map[string]any{"since": seq, "put": []any{map[string]any{"k": "order", "v": nil}}})
	eq(t, out["full"], false)
	eq(t, facts(out), map[string]any{"deck/d2": "r-b", "order": nil})

	// a presentation pinned first in its room is a fact like the others
	_, out = do("tero", map[string]any{"put": []any{map[string]any{"k": "pinned/cloud:d2", "v": "1"}}})
	eq(t, facts(out)["pinned/cloud:d2"], "1", "pinned kept")

	// and so is a vault, the decks chosen for one use (web/vaults.js)
	_, out = do("tero", map[string]any{"put": []any{map[string]any{"k": "vault/demo", "v": `{"title":"Demo Vault","decks":["cloud:d2"]}`}}})
	eq(t, facts(out)["vault/demo"], `{"title":"Demo Vault","decks":["cloud:d2"]}`, "vault kept")

	// another account sees none of it
	_, out = do("anna", map[string]any{})
	eq(t, facts(out), map[string]any{})

	// not a fact, not signed in, another site
	code, _ = do("tero", map[string]any{"put": []any{map[string]any{"k": "evil/x", "v": "1"}}})
	eq(t, code, 400, "not a fact")
	code, _ = do("", map[string]any{})
	eq(t, code, 401, "signed out")
	res, _ := editorDo(t, "POST", base+"/editor/api/layout", "tero", "https://evil.example", "{}")
	eq(t, res.StatusCode, 403, "from another site")
}

// a removed fact is told of a while, then forgotten: a page asking from
// before that gets everything
func TestLayoutPrune(t *testing.T) {
	l := layoutOf(nil)
	v := "x"
	l.put("deck/a", &v, 0)
	l.put("deck/b", &v, 0)
	l.put("deck/a", nil, 0)
	_, full := l.since(1)
	eq(t, full, false)
	l.prune(layoutGone.Milliseconds() + 1)
	_, full = l.since(1)
	eq(t, full, true, "asked from before the forgotten removal")
	facts, _ := l.since(0)
	eq(t, len(facts), 1, "only the live fact")
	back := layoutOf(l.doc("r1"))
	eq(t, []any{back.seq, back.floor, len(back.facts)}, []any{l.seq, l.floor, 1})
}
