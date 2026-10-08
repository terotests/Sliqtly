// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"testing"
)

// the assistant's room tools on a folder server, end to end
func TestRoomTools(t *testing.T) {
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
	deck := func(title string) string {
		return ok("create_presentation", map[string]any{"title": title, "markdown": DECK})["deck_id"].(string)
	}
	a, b := deck("Alpha"), deck("Beta")

	// a new deck is in General; Playground is beside it
	rooms := list(ok("list_rooms", map[string]any{})["rooms"])
	eq(t, len(rooms), 2)
	gen, play := mapOf(rooms[0]), mapOf(rooms[1])
	eq(t, []any{gen["room_id"], gen["title"], gen["kind"], gen["role"], gen["presentations"]}, []any{"general", "General", "general", "owner", 2.0})
	eq(t, []any{play["room_id"], play["title"], play["presentations"]}, []any{"playground", "Playground", 0.0})

	// a room for a ticket, a deck moved into it
	room := ok("create_room", map[string]any{"title": "PAY-817 payments", "kind": "ticket"})["room_id"].(string)
	mv := ok("move_presentation", map[string]any{"deck_id": a, "room_id": room})
	eq(t, []any{mv["from_room_id"], mv["room"], mv["moved"]}, []any{"general", "PAY-817 payments", true})
	eq(t, ok("move_presentation", map[string]any{"deck_id": a, "room_id": room})["moved"], false)
	g := ok("get_room", map[string]any{"room_id": room})
	eq(t, mapOf(g["room"])["kind"], "ticket")
	decks := list(g["presentations"])
	eq(t, []any{len(decks), mapOf(decks[0])["deck_id"], mapOf(decks[0])["inherit_room_files"]}, []any{1, a, false})
	eq(t, list(g["members"]), []any{map[string]any{"member": "user:local", "role": "owner"}})
	bad("move_presentation", map[string]any{"deck_id": b, "room_id": "nothere"}, `not found`)
	bad("move_presentation", map[string]any{"deck_id": "nothere123", "room_id": room}, `not found`)

	// links: one link per ends and kind, read from either end
	made := ok("add_link", map[string]any{"from": "room:" + room, "rel": "references", "to": "jira:PAY-817"})
	eq(t, made["made"], true)
	again := ok("add_link", map[string]any{"from": "jira:PAY-817", "rel": "referenced_by", "to": "room:" + room})
	eq(t, []any{again["made"], again["link_id"]}, []any{false, made["link_id"]})
	ok("add_link", map[string]any{"from": "deck:" + a, "rel": "relates_to", "to": "deck:" + b})
	eq(t, list(ok("links_of", map[string]any{"ref": "jira:PAY-817"})["links"]),
		[]any{map[string]any{"from": "jira:PAY-817", "rel": "referenced_by", "to": "room:" + room}})
	eq(t, len(list(ok("links_of", map[string]any{"ref": "deck:" + b})["links"])), 1)
	eq(t, len(list(ok("get_room", map[string]any{"room_id": room})["links"])), 1)
	bad("add_link", map[string]any{"from": "room:" + room, "rel": "owns", "to": "jira:X-1"}, `link_types`)
	bad("add_link", map[string]any{"from": "room " + room, "rel": "references", "to": "jira:X-1"}, `from:`)
	types := list(ok("link_types", map[string]any{})["types"])
	if len(types) < 4 {
		t.Fatal(types)
	}
	ok("remove_link", map[string]any{"from": "deck:" + b, "rel": "relates_to", "to": "deck:" + a})
	eq(t, len(list(ok("links_of", map[string]any{"ref": "deck:" + b})["links"])), 0)

	// members: the last owner stays
	ok("set_room_member", map[string]any{"room_id": room, "member": "user:bob", "role": "editor"})
	bad("set_room_member", map[string]any{"room_id": room, "member": "user:local", "role": ""}, `one owner`)
	bad("set_room_member", map[string]any{"room_id": room, "member": "bob", "role": "editor"}, `neither user: nor group:`)

	// archived: read only, and listed only when asked
	ok("archive_room", map[string]any{"room_id": room})
	bad("move_presentation", map[string]any{"deck_id": b, "room_id": room}, `role does not allow`)
	eq(t, len(list(ok("list_rooms", map[string]any{})["rooms"])), 2)
	eq(t, len(list(ok("list_rooms", map[string]any{"archived": true})["rooms"])), 3)
	ok("archive_room", map[string]any{"room_id": room, "archived": false})
	ok("move_presentation", map[string]any{"deck_id": b, "room_id": room})

	// settings: a room described when made, renamed, newest listed first;
	// deleting one moves its decks to General and deletes none
	story := ok("create_room", map[string]any{"title": "Story: sign in with Google", "description": "As a user I sign in with my Google account"})["room_id"].(string)
	rows := list(ok("list_rooms", map[string]any{})["rooms"])
	eq(t, []any{mapOf(rows[2])["room_id"], mapOf(rows[2])["description"], mapOf(rows[3])["room_id"]}, []any{story, "As a user I sign in with my Google account", room})
	ok("update_room", map[string]any{"room_id": story, "title": "  US-4   sign in "})
	eq(t, []any{mapOf(ok("get_room", map[string]any{"room_id": story})["room"])["title"], mapOf(ok("get_room", map[string]any{"room_id": story})["room"])["description"]}, []any{"US-4 sign in", "As a user I sign in with my Google account"})
	ok("update_room", map[string]any{"room_id": story, "description": ""})
	eq(t, mapOf(ok("get_room", map[string]any{"room_id": story})["room"])["description"], nil)
	bad("update_room", map[string]any{"room_id": story, "title": " "}, `title`)
	bad("update_room", map[string]any{"room_id": "general", "title": "Mine"}, `denied|not allow`)
	ok("move_presentation", map[string]any{"deck_id": a, "room_id": story})
	eq(t, ok("delete_room", map[string]any{"room_id": story})["deleted"], true)
	bad("get_room", map[string]any{"room_id": story}, `not found`)
	eq(t, len(list(ok("get_room", map[string]any{"room_id": "general"})["presentations"])), 1)
	bad("delete_room", map[string]any{"room_id": "playground"}, `denied|not allow`)

	bad("get_room", map[string]any{}, `room_id is missing`)

	// a deck made straight into a room; a room found by its ticket code,
	// the latest worked in first, pages of it
	n11 := ok("create_room", map[string]any{"title": "N11-1234 Review", "description": "Checkout retry review"})["room_id"].(string)
	c := ok("create_presentation", map[string]any{"title": "N11-1234 review deck", "markdown": DECK, "room_id": n11})["deck_id"].(string)
	in := list(ok("get_room", map[string]any{"room_id": n11})["presentations"])
	eq(t, []any{len(in), mapOf(in[0])["deck_id"]}, []any{1, c})
	found := ok("list_rooms", map[string]any{"query": "n11-1234"})
	eq(t, []any{len(list(found["rooms"])), mapOf(list(found["rooms"])[0])["room_id"], found["total"]}, []any{1, n11, 1.0})
	eq(t, mapOf(list(ok("list_rooms", map[string]any{"order": "active"})["rooms"])[0])["room_id"], n11)
	paged := ok("list_rooms", map[string]any{"limit": 1, "offset": 1})
	eq(t, []any{len(list(paged["rooms"])), paged["next_offset"]}, []any{1, 2.0})
	bad("create_presentation", map[string]any{"title": "Lost", "markdown": DECK, "room_id": "nothere"}, `room_id nothere`)
	ok("archive_room", map[string]any{"room_id": n11})
	bad("create_presentation", map[string]any{"title": "Late", "markdown": DECK, "room_id": n11}, `read only`)
}

// the folder server has no access limits for now: another caller sees and
// changes everything the server's user does (the limits themselves are
// tested in store/storetest)
func TestRoomsOpenOnFolderServer(t *testing.T) {
	ctx := context.Background()
	e, _, err := localEnv(t.TempDir(), "http://x", "local")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.DB.Set(ctx, "shares", "deck000001", Doc{"name": "Mine", "owner": "local"}); err != nil {
		t.Fatal(err)
	}
	as := func(uid, op string, a map[string]any) map[string]any {
		t.Helper()
		out, err := e.rooms.call(ctx, uid, op, a)
		if err != nil {
			t.Fatalf("%s %s: %v", uid, op, err)
		}
		b, _ := json.Marshal(out)
		var m map[string]any
		json.Unmarshal(b, &m)
		return m
	}
	eq(t, len(list(as("bob", "list_rooms", nil)["rooms"])), 2)
	eq(t, len(list(as("bob", "get_room", map[string]any{"room_id": "general"})["presentations"])), 1)
	as("bob", "move_presentation", map[string]any{"deck_id": "deck000001", "room_id": "playground"})
	eq(t, len(list(as("local", "get_room", map[string]any{"room_id": "playground"})["presentations"])), 1)
	if _, err := e.rooms.call(ctx, "", "list_rooms", nil); err == nil {
		t.Fatal("no user, still answered")
	}
}

// the page's POST /api/rooms/<op>
func TestRoomsHTTP(t *testing.T) {
	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	code, body := req(t, "POST", srv.URL+"/api/rooms/create_room", "application/json", `{"title":"Team"}`)
	eq(t, code, 200)
	match(t, body, `"room_id"`)
	code, body = req(t, "POST", srv.URL+"/api/rooms/list_rooms", "application/json", ``)
	eq(t, code, 200)
	match(t, body, `"title":"Team"`)
	code, _ = req(t, "POST", srv.URL+"/api/rooms/list_rooms", "application/x-www-form-urlencoded", `a=b`)
	eq(t, code, 415)
	code, _ = req(t, "GET", srv.URL+"/api/rooms/list_rooms", "", "")
	eq(t, code, 404)
	code, _ = req(t, "POST", srv.URL+"/api/rooms/drop_tables", "application/json", `{}`)
	eq(t, code, 404)
	code, body = req(t, "POST", srv.URL+"/api/rooms/get_room", "application/json", `{"room_id":"nothere"}`)
	eq(t, code, 400)
	match(t, body, `not found`)
}

// folders in a room: made, a deck filed there (kept when the deck is
// written again), renamed, the deck moved out by another room, deleted
func TestRoomFolders(t *testing.T) {
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
	deck := func(title string) string {
		return ok("create_presentation", map[string]any{"title": title, "markdown": DECK})["deck_id"].(string)
	}
	a, b := deck("Alpha"), deck("Beta")
	room := ok("create_room", map[string]any{"title": "Sprint"})["room_id"].(string)

	made := ok("create_folder", map[string]any{"room_id": room, "name": "  Testing "})
	testing := made["folder_id"].(string)
	eq(t, made["made"], true)
	// the same name, any case, is that folder
	eq(t, ok("create_folder", map[string]any{"room_id": room, "name": "testing"}), map[string]any{"folder_id": testing, "made": false})
	ok("create_folder", map[string]any{"room_id": room, "name": "Drafts"})
	bad("create_folder", map[string]any{"room_id": room, "name": "  "}, `name of its own`)
	bad("create_folder", map[string]any{"room_id": "nothere", "name": "X"}, `not found`)
	// General has folders too
	ok("create_folder", map[string]any{"room_id": "general", "name": "Old"})

	mv := ok("move_presentation", map[string]any{"deck_id": a, "room_id": room, "folder_id": testing})
	eq(t, []any{mv["from_room_id"], mv["room"], mv["folder"], mv["moved"]}, []any{"general", "Sprint", "Testing", true})
	eq(t, ok("move_presentation", map[string]any{"deck_id": a, "room_id": room, "folder_id": testing})["moved"], false)
	ok("move_presentation", map[string]any{"deck_id": b, "room_id": room})
	bad("move_presentation", map[string]any{"deck_id": b, "room_id": room, "folder_id": "f-nope"}, `no folder f-nope`)

	g := ok("get_room", map[string]any{"room_id": room})
	folders := list(g["folders"])
	eq(t, len(folders), 2)
	eq(t, []any{mapOf(folders[0])["name"], mapOf(folders[1])["name"], mapOf(folders[1])["presentations"]}, []any{"Drafts", "Testing", 1.0})
	inFolder := map[string]any{}
	for _, d := range list(g["presentations"]) {
		inFolder[mapOf(d)["deck_id"].(string)] = mapOf(d)["folder_id"]
	}
	eq(t, inFolder, map[string]any{a: testing, b: ""})

	// writing the deck again keeps it in its folder
	ok("update_presentation", map[string]any{"deck_id": a, "markdown": DECK + "\n---\n\n# More\n"})
	for _, d := range list(ok("get_room", map[string]any{"room_id": room})["presentations"]) {
		if mapOf(d)["deck_id"] == a {
			eq(t, mapOf(d)["folder_id"], testing)
		}
	}

	ok("rename_folder", map[string]any{"room_id": room, "folder_id": testing, "name": "Tests"})
	bad("rename_folder", map[string]any{"room_id": room, "folder_id": testing, "name": "drafts"}, `name of its own`)
	bad("rename_folder", map[string]any{"room_id": room, "folder_id": "f-nope", "name": "X"}, `not found`)
	eq(t, mapOf(list(ok("get_room", map[string]any{"room_id": room})["folders"])[1])["name"], "Tests")

	// deleted: its decks are at the room's top again, none deleted
	eq(t, ok("delete_folder", map[string]any{"room_id": room, "folder_id": testing})["moved"], 1.0)
	g = ok("get_room", map[string]any{"room_id": room})
	eq(t, len(list(g["folders"])), 1)
	eq(t, len(list(g["presentations"])), 2)
	for _, d := range list(g["presentations"]) {
		eq(t, mapOf(d)["folder_id"], "")
	}

	// to another room the deck leaves its folder; a room deleted takes its
	// folders, and its decks are at General's top
	drafts := mapOf(list(g["folders"])[0])["folder_id"].(string)
	ok("move_presentation", map[string]any{"deck_id": b, "room_id": room, "folder_id": drafts})
	ok("move_presentation", map[string]any{"deck_id": b, "room_id": "general"})
	ok("move_presentation", map[string]any{"deck_id": b, "room_id": room, "folder_id": drafts})
	ok("delete_room", map[string]any{"room_id": room})
	for _, d := range list(ok("get_room", map[string]any{"room_id": "general"})["presentations"]) {
		eq(t, mapOf(d)["folder_id"], "")
	}

	// an archived room's folders are read only
	arch := ok("create_room", map[string]any{"title": "Done"})["room_id"].(string)
	ok("archive_room", map[string]any{"room_id": arch})
	bad("create_folder", map[string]any{"room_id": arch, "name": "X"}, `role does not allow`)
}
