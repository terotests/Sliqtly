// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// A Story's agent access end to end on a folder server: the page sets it,
// an assistant's tools hold it (hidden: not there at all, read: read only,
// only: the assistant's), and the page's own saves hold agents only.
func TestStoryAgentAccess(t *testing.T) {
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
	page := func(op string, a map[string]any) map[string]any {
		t.Helper()
		code, out := pagePost(t, srv.URL, op, a)
		if code != 200 {
			t.Fatalf("page %s: %d %v", op, code, out)
		}
		return out
	}
	deck := map[string]string{}
	for _, mode := range []string{"mixed", "read", "hidden", "only"} {
		id := ok("create_presentation", map[string]any{"title": "Deck " + mode, "markdown": "# Deck " + mode + "\n\n## Two\n\nWords " + mode + "\n"})["deck_id"].(string)
		deck[mode] = id
		eq(t, page("set_agents", map[string]any{"deck_id": id, "agents": mode})["agents"], mode)
	}
	if code, _ := pagePost(t, srv.URL, "set_agents", map[string]any{"deck_id": deck["mixed"], "agents": "everyone"}); code == 200 {
		t.Fatal("set_agents took a mode that is not one")
	}
	// the setting is a person's: an assistant has no tool for it
	if r := call(t, s, "set_agents", map[string]any{"deck_id": deck["read"], "agents": "mixed"}); !r.IsError {
		t.Fatal("an assistant set the agent access")
	}

	// hidden: not in any list, not readable, not changeable
	listed := textOf(call(t, s, "list_presentations", map[string]any{}))
	if strings.Contains(listed, deck["hidden"]) || !strings.Contains(listed, deck["read"]) {
		t.Fatalf("list_presentations: %s", listed)
	}
	room := ok("get_room", map[string]any{"room_id": "general"})
	var inRoom []string
	for _, p := range list(room["presentations"]) {
		inRoom = append(inRoom, mapOf(p)["deck_id"].(string)+"="+mapOf(p)["agents"].(string))
	}
	joined := strings.Join(inRoom, " ")
	if strings.Contains(joined, deck["hidden"]) || !strings.Contains(joined, deck["read"]+"=read") {
		t.Fatalf("get_room: %s", joined)
	}
	found := textOf(call(t, s, "search_presentations", map[string]any{"query": "Words"}))
	if strings.Contains(found, deck["hidden"]) || !strings.Contains(found, deck["mixed"]) {
		t.Fatalf("search_presentations: %s", found)
	}
	for _, tool := range []string{"get_presentation", "list_files", "list_comments", "get_session", "update_presentation"} {
		bad(tool, map[string]any{"deck_id": deck["hidden"], "markdown": "# x"}, `No presentation `+deck["hidden"])
	}
	bad("add_link", map[string]any{"from": "deck:" + deck["mixed"], "rel": "relates_to", "to": "deck:" + deck["hidden"]}, `No presentation`)
	// the person still sees it
	pr := page("get_room", map[string]any{"room_id": "general"})
	if !strings.Contains(jsonText(pr), deck["hidden"]) {
		t.Fatal("the page lost the hidden Story")
	}

	// read: read, never changed
	match(t, textOf(call(t, s, "get_presentation", map[string]any{"deck_id": deck["read"]})), `Deck read`)
	ok("list_comments", map[string]any{"deck_id": deck["read"]})
	ok("get_session", map[string]any{"deck_id": deck["read"]})
	for name, args := range map[string]map[string]any{
		"update_presentation": {"markdown": "# changed"},
		"add_comment":         {"slide": 1, "text": "hi"},
		"begin_work":          {"slides": []any{1}},
		"set_story":           {"phase": "done"},
		"log_session":         {"entries": []any{map[string]any{"kind": "note", "text": "x"}}},
		"fork_story":          {"name": "mine"},
		"move_presentation":   {"room_id": "playground"},
	} {
		args["deck_id"] = deck["read"]
		bad(name, args, `read only for assistants`)
	}
	match(t, textOf(call(t, s, "get_presentation", map[string]any{"deck_id": deck["read"]})), `# Deck read`)

	// only: the assistant's; the page views it and keeps its settings
	ok("update_presentation", map[string]any{"deck_id": deck["only"], "markdown": "# By the assistant\n"})
	code, body := req(t, "PATCH", srv.URL+"/api/shares/"+deck["only"], "application/json", `{"md":"# By a person\n"}`)
	if code != 403 || !strings.Contains(body, "agents-only") {
		t.Fatalf("a person saved an agents only Story: %d %s", code, body)
	}
	code, _ = req(t, "PUT", srv.URL+"/api/files/shares/"+deck["only"]+"/media/x.png", "image/png", "png")
	eq(t, code, 403)
	page("set_story", map[string]any{"deck_id": deck["only"], "phase": "review"})
	match(t, textOf(call(t, s, "get_presentation", map[string]any{"deck_id": deck["only"]})), `By the assistant`)
	page("set_agents", map[string]any{"deck_id": deck["only"], "agents": "mixed"})
	code, body = req(t, "PATCH", srv.URL+"/api/shares/"+deck["only"], "application/json", `{"md":"# By a person\n"}`)
	eq(t, code, 200, body)

	// mixed, as before
	ok("update_presentation", map[string]any{"deck_id": deck["mixed"], "markdown": "# Changed\n"})
	ok("set_story", map[string]any{"deck_id": deck["mixed"], "phase": "done"})
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
