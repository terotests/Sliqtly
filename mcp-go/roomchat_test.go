// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func pagePost(t *testing.T, base, op string, a map[string]any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(a)
	code, body := req(t, "POST", base+"/api/rooms/"+op, "application/json", string(b))
	var m map[string]any
	json.Unmarshal([]byte(body), &m)
	return code, m
}

func chatMsg(kind string) func(map[string]any) bool {
	return func(m map[string]any) bool {
		v, _ := m["v"].(map[string]any)
		return m["k"] == "chat" && v["t"] == kind
	}
}

// A room's chat from the page and from an assistant, end to end: posts,
// threads, reactions, the assistant's progress message, who is here, and
// every change told on the page's stream.
func TestRoomChat(t *testing.T) {
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
	room := ok("create_room", map[string]any{"title": "PAY-817", "description": "Checkout retry"})["room_id"].(string)
	sock := dialSocket(t, srv.URL, "")
	defer sock.ws.Close()

	ada := map[string]any{"id": "ada-browser-1", "name": "Ada", "avatar": "knight", "color": "#EA580C"}
	bob := map[string]any{"id": "bob-browser-2", "name": "Bob", "avatar": "ghost", "color": "#2563eb"}

	// a person comes in and is counted here
	code, here := pagePost(t, srv.URL, "chat_here", map[string]any{"room_id": room, "as": ada})
	eq(t, code, 200)
	eq(t, here["here"], 1.0)
	h := sock.next("who is here", chatMsg("here"))["v"].(map[string]any)
	eq(t, []any{h["room"], h["here"]}, []any{room, 1.0})

	// the page posts as the person
	code, out := pagePost(t, srv.URL, "post_room_message", map[string]any{"room_id": room, "text": "Hi @Claude, can you check the retry deck?\n```\nretry(3)\n```", "as": ada})
	eq(t, code, 200)
	first := out["message_id"].(string)
	eq(t, out["seq"], 1.0)
	m := sock.next("the message", chatMsg("msg"))["v"].(map[string]any)
	msg := m["msg"].(map[string]any)
	from := msg["from"].(map[string]any)
	eq(t, []any{m["room"], msg["seq"], from["name"], from["kind"], from["avatar"], from["color"], from["id"]}, []any{room, 1.0, "Ada", "person", "knight", "#ea580c", "p-ada-browser-1"})
	match(t, msg["text"].(string), "retry\\(3\\)")

	// the page cannot speak as a robot, nor without saying who it is
	code, _ = pagePost(t, srv.URL, "post_room_message", map[string]any{"room_id": room, "text": "x", "as": map[string]any{"id": "x"}})
	eq(t, code, 400)
	code, _ = pagePost(t, srv.URL, "post_room_message", map[string]any{"room_id": room, "text": "x", "agent": "Claude"})
	eq(t, code, 400)

	// the assistant finds what it was asked, and answers as a robot in the
	// thread, then keeps one progress message up to date
	read := ok("read_room_chat", map[string]any{"room_id": room, "mentioning": "Claude"})
	eq(t, len(list(read["messages"])), 1)
	eq(t, mapOf(read["room"])["title"], "PAY-817")
	eq(t, mapOf(read["room"])["description"], "Checkout retry")
	eq(t, read["last_seq"], 1.0)
	eq(t, len(list(ok("read_room_chat", map[string]any{"room_id": room, "mentioning": "Cla"})["messages"])), 0)
	reply := ok("post_room_message", map[string]any{"room_id": room, "agent": "Claude", "thread_id": first, "text": "On it: reading the deck…"})
	progress := reply["message_id"].(string)
	got := sock.next("the reply", chatMsg("msg"))["v"].(map[string]any)["msg"].(map[string]any)
	eq(t, []any{got["thread"], mapOf(got["from"])["kind"], mapOf(got["from"])["avatar"], mapOf(got["from"])["id"]}, []any{first, "bot", "robot", "bot-claude"})
	root := sock.next("the root's count", chatMsg("msg"))["v"].(map[string]any)["msg"].(map[string]any)
	eq(t, []any{root["id"], root["replies"]}, []any{first, 1.0})
	ok("post_room_message", map[string]any{"room_id": room, "agent": "Claude", "message_id": progress, "text": "Done: 3 slides fixed ✓"})
	edited := sock.next("the progress", chatMsg("msg"))["v"].(map[string]any)["msg"].(map[string]any)
	eq(t, edited["text"], "Done: 3 slides fixed ✓")
	if edited["edited"] == nil {
		t.Fatal("not marked edited")
	}
	// another assistant may not change Claude's message
	bad("post_room_message", map[string]any{"room_id": room, "agent": "Cursor", "message_id": progress, "text": "mine now"}, `your own`)
	th := ok("read_room_chat", map[string]any{"room_id": room, "thread_id": first})
	eq(t, len(list(th["messages"])), 1)
	eq(t, mapOf(th["root"])["replies"], 1.0)
	top := ok("read_room_chat", map[string]any{"room_id": room})
	eq(t, len(list(top["messages"])), 1)

	// reactions: one per person and emoji, a second press takes it back
	pagePost(t, srv.URL, "chat_react", map[string]any{"room_id": room, "message_id": first, "emoji": "👍", "as": bob})
	r1 := sock.next("a reaction", chatMsg("msg"))["v"].(map[string]any)["msg"].(map[string]any)
	eq(t, fmt.Sprint(r1["reactions"]), "map[👍:[p-bob-browser-2]]")
	pagePost(t, srv.URL, "chat_react", map[string]any{"room_id": room, "message_id": first, "emoji": "👍", "as": bob})
	r2 := sock.next("a reaction taken back", chatMsg("msg"))["v"].(map[string]any)["msg"].(map[string]any)
	eq(t, r2["reactions"], nil)

	// only one's own message is deleted
	code, _ = pagePost(t, srv.URL, "chat_delete", map[string]any{"room_id": room, "message_id": first, "as": bob})
	eq(t, code, 400)
	code, _ = pagePost(t, srv.URL, "chat_delete", map[string]any{"room_id": room, "message_id": first, "as": ada})
	eq(t, code, 200)
	del := sock.next("the deletion", chatMsg("msg"))["v"].(map[string]any)["msg"].(map[string]any)
	eq(t, []any{del["deleted"], del["text"], del["replies"]}, []any{true, "", 1.0})

	// the room's people: Ada, here; Claude, who spoke
	code, here = pagePost(t, srv.URL, "chat_here", map[string]any{"room_id": room, "as": ada})
	eq(t, code, 200)
	people := list(here["people"])
	eq(t, len(people), 2)
	eq(t, []any{mapOf(people[0])["name"], mapOf(people[0])["here"], mapOf(people[1])["name"], mapOf(people[1])["kind"]}, []any{"Ada", true, "Claude", "bot"})
	pagePost(t, srv.URL, "chat_here", map[string]any{"room_id": room, "as": ada, "away": true})
	gone := sock.next("who left", func(m map[string]any) bool {
		v, _ := m["v"].(map[string]any)
		return m["k"] == "chat" && v["t"] == "here" && v["here"] == 0.0
	})
	_ = gone

	// what came after a seq, for a page that missed some
	pagePost(t, srv.URL, "post_room_message", map[string]any{"room_id": room, "text": "two", "as": bob})
	pagePost(t, srv.URL, "post_room_message", map[string]any{"room_id": room, "text": "three", "as": bob})
	after := ok("read_room_chat", map[string]any{"room_id": room, "after_seq": 2})
	var texts []string
	for _, x := range list(after["messages"]) {
		texts = append(texts, mapOf(x)["text"].(string))
	}
	eq(t, strings.Join(texts, ","), "two,three")

	// long text has a limit; an empty one is no message
	bad("post_room_message", map[string]any{"room_id": room, "text": strings.Repeat("x", chatMaxText+1)}, `at most`)
	bad("post_room_message", map[string]any{"room_id": room, "text": "  \n "}, `empty`)
	bad("read_room_chat", map[string]any{"room_id": "nothere"}, `not found`)

	// an archived room's chat is read, not written
	ok("archive_room", map[string]any{"room_id": room})
	bad("post_room_message", map[string]any{"room_id": room, "text": "late"}, `read only`)
	eq(t, len(list(ok("read_room_chat", map[string]any{"room_id": room})["messages"])), 3)

	// the page's own operations are not the assistant's tools
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	eq(t, []any{names["read_room_chat"], names["post_room_message"], names["chat_react"], names["chat_here"]}, []any{true, true, false, false})
}

func TestMentions(t *testing.T) {
	for _, c := range []struct {
		text, name string
		want       bool
	}{
		{"hi @Claude!", "Claude", true},
		{"hi @claude", "@CLAUDE", true},
		{"hi @Claudette", "Claude", false},
		{"@Claudette and @Claude", "Claude", true},
		{"email claude@x.fi", "Claude", false},
		{"anything", "", true},
	} {
		if got := mentions(c.text, c.name); got != c.want {
			t.Errorf("mentions(%q, %q) = %v", c.text, c.name, got)
		}
	}
}
