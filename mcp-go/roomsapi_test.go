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
	ok("move_presentation", map[string]any{"deck_id": a, "room_id": room})
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
