// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"testing"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// the cloud's shared rooms through /editor/api/rooms, kept in memory
func TestCloudRooms(t *testing.T) {
	cloudRoomsFlow(t, store.NewMemStore(), store.NewMemChat())
}

// cloudRoomsFlow: an account sees only the rooms it is a member of, by user
// id or by a verified address, and speaks by its own name
func cloudRoomsFlow(t *testing.T, e store.Engine, chat store.ChatLog) {
	env, base, stop := editorServer(t)
	defer stop()
	env.cloudRooms = newCloudRoomService(env, e, chat)
	call := func(who, op string, a map[string]any) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(a)
		res, body := editorDo(t, "POST", base+"/editor/api/rooms/"+op, who, base, string(b))
		out := map[string]any{}
		json.Unmarshal([]byte(body), &out)
		return res.StatusCode, out
	}
	rooms := func(who string) []any {
		t.Helper()
		code, out := call(who, "list_rooms", nil)
		eq(t, code, 200, who+" lists")
		l, _ := out["rooms"].([]any)
		return l
	}

	code, out := call("anna", "create_room", map[string]any{"title": "Launch"})
	eq(t, code, 200, "anna makes a room")
	room, _ := out["room_id"].(string)
	eq(t, len(rooms("anna")), 1, "anna's rooms")
	eq(t, len(rooms("tero")), 0, "tero is in none")
	code, out = call("tero", "read_room_chat", map[string]any{"room_id": room})
	eq(t, []any{code, out["error"]}, []any{400, "not found, or not yours to see"}, "tero reads it")
	code, _ = call("tero", "post_room_message", map[string]any{"room_id": room, "text": "hi"})
	eq(t, code, 400, "tero posts to it")

	// not through here: another site, no sign-in, another kind of member
	res, _ := editorDo(t, "POST", base+"/editor/api/rooms/list_rooms", "anna", "https://evil.example", "{}")
	eq(t, res.StatusCode, 403, "from another site")
	code, _ = call("", "list_rooms", nil)
	eq(t, code, 401, "signed out")
	code, out = call("anna", "set_room_member", map[string]any{"room_id": room, "member": "user:u-tero", "role": "editor"})
	eq(t, []any{code, out["error"]}, []any{400, "invite people by address: member email:<address>"}, "a user id")
	code, out = call("anna", "move_presentation", map[string]any{"deck_id": "abcdef1234", "room_id": room})
	eq(t, []any{code, out["error"]}, []any{400, "move_presentation is not on sliqtly.com yet"}, "decks stay in the browser")

	// anna invites tero by address: tero sees the room and speaks as Tero
	code, _ = call("anna", "set_room_member", map[string]any{"room_id": room, "member": "email:TeroKTolonen@gmail.com", "role": "editor"})
	eq(t, code, 200, "anna invites tero")
	eq(t, len(rooms("tero")), 1, "tero's rooms")
	code, out = call("tero", "post_room_message", map[string]any{"room_id": room, "text": "Hello *all*", "as": map[string]any{"id": "u-anna", "name": "Anna", "avatar": "owl", "color": "#22c55e"}})
	eq(t, code, 200, "tero posts")
	code, out = call("anna", "read_room_chat", map[string]any{"room_id": room})
	eq(t, code, 200, "anna reads")
	msgs, _ := out["messages"].([]any)
	eq(t, len(msgs), 1, "one message")
	from := msgs[0].(map[string]any)["from"].(map[string]any)
	eq(t, []any{from["id"], from["name"], from["avatar"]}, []any{"p-u-tero", "Tero", "owl"}, "tero by tero's own name, the avatar chosen")
	code, out = call("anna", "get_room", map[string]any{"room_id": room})
	members, _ := out["members"].([]any)
	eq(t, []any{code, len(members)}, []any{200, 3}, "anna by id and address, tero by address")

	// here: who has the chat open, told the same by any instance
	code, out = call("tero", "chat_here", map[string]any{"room_id": room})
	eq(t, []any{code, out["here"]}, []any{200, 1.0}, "tero is here")
	code, out = call("anna", "chat_here", map[string]any{"room_id": room})
	eq(t, out["here"], 2.0, "and anna")
	code, out = call("tero", "chat_here", map[string]any{"room_id": room, "away": true})
	eq(t, out["here"], 1.0, "tero left")

	// the address must be one the provider vouches for
	eq(t, len(rooms("mallory")), 0, "an unverified address")
	code, _ = call("mallory", "read_room_chat", map[string]any{"room_id": room})
	eq(t, code, 400, "an unverified address reads")

	// taken out: nothing of it any more
	code, _ = call("tero", "set_room_member", map[string]any{"room_id": room, "member": "email:anna@example.com", "role": ""})
	eq(t, code, 400, "an editor removes the owner")
	code, _ = call("anna", "set_room_member", map[string]any{"room_id": room, "member": "email:teroktolonen@gmail.com", "role": ""})
	eq(t, code, 200, "anna takes tero out")
	eq(t, len(rooms("tero")), 0, "tero's rooms again")
	code, _ = call("tero", "read_room_chat", map[string]any{"room_id": room})
	eq(t, code, 400, "tero reads after")
}
