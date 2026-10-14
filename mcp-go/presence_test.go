// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/terotests/sliqtly/mcp-go/agentsim"
)

func TestAgentBoardWorkingIdleGone(t *testing.T) {
	var b agentBoard
	t0 := time.Unix(1_000_000, 0)
	b.hello("a", "Claude Code", t0)
	done := b.begin("a", agentCall{Tool: "update_presentation", Deck: "d1"}, t0)
	rows := b.deck("d1", t0.Add(3*time.Second))
	if len(rows) != 1 || !rows[0].Busy || rows[0].State != "working" || rows[0].Name != "Claude Code" || rows[0].Tool != "update_presentation" || rows[0].Seen != 0 || rows[0].Since != 3 {
		t.Fatalf("while its call runs: %+v", rows)
	}
	done(t0.Add(5 * time.Second))
	done(t0.Add(9 * time.Second)) // told twice: the first counts
	if r := b.deck("d1", t0.Add(10*time.Second))[0]; r.Busy || r.State != "working" || r.Seen != 5 || r.Calls != 1 {
		t.Fatalf("just after: %+v", r)
	}
	if r := b.deck("d1", t0.Add(5*time.Second+agentWorking))[0]; r.State != "idle" {
		t.Fatalf("a while after: %+v", r)
	}
	if rows := b.deck("d1", t0.Add(5*time.Second+agentGone)); len(rows) != 0 {
		t.Fatalf("long after: %+v", rows)
	}
	if rows := b.deck("d2", t0); len(rows) != 0 {
		t.Fatalf("another deck: %+v", rows)
	}
}

func TestAgentBoardClaimsAndNames(t *testing.T) {
	var b agentBoard
	t0 := time.Unix(1_000_000, 0)
	// the name it gives itself wins over its client's
	b.hello("a", "Codex", t0)
	b.begin("a", agentCall{Tool: "begin_work", Deck: "d", Agent: "Reviewer", Slides: []int{2, 3}, Note: "fixing charts"}, t0)(t0)
	b.claim("a", agentCall{Tool: "begin_work", Deck: "d", Slides: []int{2, 3}, Note: "fixing charts"})
	// no name of any kind
	b.begin("b", agentCall{Tool: "get_presentation", Deck: "d"}, t0)(t0)
	later := t0.Add(2 * agentWorking)
	rows := b.deck("d", later)
	if len(rows) != 2 {
		t.Fatalf("rows %+v", rows)
	}
	// holding slides it is working, however long since its last call; the
	// working ones first
	if r := rows[0]; r.Name != "Reviewer" || r.Client != "Codex" || r.State != "working" || !reflect.DeepEqual(r.Slides, []int{2, 3}) || r.Note != "fixing charts" {
		t.Fatalf("claim: %+v", r)
	}
	if r := rows[1]; r.Name != "Assistant" || r.State != "idle" {
		t.Fatalf("no name: %+v", r)
	}
	// the whole deck: begin_work without slides
	b.claim("b", agentCall{Tool: "begin_work", Deck: "d"})
	if r := b.deck("d", later)[0]; r.Name != "Assistant" || !r.Whole || r.State != "working" {
		t.Fatalf("whole deck: %+v", b.deck("d", later))
	}
	b.begin("a", agentCall{Tool: "end_work", Deck: "d"}, later)(later)
	for _, r := range b.deck("d", later.Add(agentWorking)) {
		if r.Name == "Reviewer" && (r.Slides != nil || r.State != "idle") {
			t.Fatalf("after end_work: %+v", r)
		}
	}
	b.bye("a")
	if rows := b.deck("d", later); len(rows) != 1 || rows[0].ID != "b" {
		t.Fatalf("after its session ended: %+v", rows)
	}
}

func TestAgentBoardForgetsTheQuiet(t *testing.T) {
	var b agentBoard
	t0 := time.Unix(1_000_000, 0)
	b.begin("a", agentCall{Tool: "get_presentation", Deck: "d"}, t0)(t0)
	running := b.begin("b", agentCall{Tool: "render_slide", Deck: "d"}, t0)
	b.begin("c", agentCall{Tool: "list_presentations"}, t0.Add(agentGone+time.Minute))
	if _, ok := b.agents["a"]; ok {
		t.Fatal("a quiet session is kept")
	}
	if _, ok := b.agents["b"]; !ok {
		t.Fatal("a session whose call runs is forgotten")
	}
	running(t0)
}

func TestPeekRPC(t *testing.T) {
	m := peekRPC([]byte(` {"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"begin_work","arguments":{"deck_id":"d","agent":"Claude","slides":[1,4],"markdown":"# big"}}}`))
	if len(m) != 1 || m[0].Method != "tools/call" || m[0].Params.Name != "begin_work" || m[0].Params.Arguments.DeckID != "d" || m[0].Params.Arguments.Agent != "Claude" {
		t.Fatalf("%+v", m)
	}
	if s := peekSlides(m[0].Params.Arguments.Slides); !reflect.DeepEqual(s, []int{1, 4}) {
		t.Fatalf("slides %v", s)
	}
	if s := peekSlides(json.RawMessage(`"2-4, 7"`)); !reflect.DeepEqual(s, []int{2, 3, 4, 7}) {
		t.Fatalf("slides as text %v", s)
	}
	b := peekRPC([]byte(`[{"method":"initialize","params":{"clientInfo":{"name":"x","title":"X"}}},{"method":"ping"}]`))
	if len(b) != 2 || b[0].Params.ClientInfo.Title != "X" {
		t.Fatalf("batch %+v", b)
	}
	if peekRPC([]byte(`not json`)) != nil {
		t.Fatal("not JSON-RPC")
	}
}

// Assistants plugged in over MCP show up on the Story they work on, with
// nothing asked of them, and the page reads who is there.
func TestPresenceSeesAgents(t *testing.T) {
	srv, session := startLocalWith(t, t.TempDir(), "", nil)
	defer srv.Close()
	session.Close()
	ctx := context.Background()
	hc := &http.Client{}
	writer := &agentsim.Client{URL: srv.URL + "/mcp", HTTP: hc}
	if err := writer.Init(ctx, "Claude Code"); err != nil {
		t.Fatal(err)
	}
	r, err := writer.Call(ctx, "create_presentation", map[string]any{"title": "Team", "markdown": "# Team\n\n## One\n\n- a\n\n## Two\n\n- b\n"})
	if err != nil || r.IsError {
		t.Fatalf("create: %v %s", err, r.Text)
	}
	deck, _ := r.Data["deck_id"].(string)
	reviewer := &agentsim.Client{URL: srv.URL + "/mcp", HTTP: hc}
	if err := reviewer.Init(ctx, "Codex"); err != nil {
		t.Fatal(err)
	}
	if r, err := reviewer.Call(ctx, "begin_work", map[string]any{"deck_id": deck, "agent": "Reviewer", "slides": []any{2}, "note": "checking the numbers"}); err != nil || r.IsError {
		t.Fatalf("begin_work: %v %s", err, r.Text)
	}
	// slides another holds are not taken: no claim is shown for it
	if r, err := writer.Call(ctx, "begin_work", map[string]any{"deck_id": deck, "slides": []any{2}}); err != nil || r.Data["claimed"] != false {
		t.Fatalf("overlapping begin_work: %v %+v", err, r)
	}

	ask := func() []presenceRow {
		t.Helper()
		code, body := req(t, "POST", srv.URL+"/api/rooms/presence", "application/json", `{"deck_ids":["`+deck+`","nothere"]}`)
		if code != 200 {
			t.Fatalf("presence: %d %s", code, body)
		}
		var out struct{ Decks []presenceRow }
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		return out.Decks
	}
	decks := ask()
	if len(decks) != 1 || decks[0].DeckID != deck || len(decks[0].Agents) != 2 {
		t.Fatalf("presence: %+v", decks)
	}
	byName := map[string]agentRow{}
	for _, a := range decks[0].Agents {
		byName[a.Name] = a
	}
	rv, cc := byName["Reviewer"], byName["Claude Code"]
	if rv.Client != "Codex" || rv.State != "working" || !reflect.DeepEqual(rv.Slides, []int{2}) || rv.Note != "checking the numbers" {
		t.Fatalf("reviewer: %+v", rv)
	}
	if cc.State != "working" || len(cc.Slides) != 0 || cc.Whole || cc.Calls != 1 {
		t.Fatalf("writer: %+v", cc)
	}

	// its session ended: gone from the Story
	reviewer.Close()
	decks = ask()
	if len(decks) != 1 || len(decks[0].Agents) != 1 || decks[0].Agents[0].Name != "Claude Code" {
		t.Fatalf("after the reviewer left: %+v", decks)
	}
	// not an assistant's tool
	if r, err := writer.Call(ctx, "presence", map[string]any{"deck_ids": []any{deck}}); err == nil && !r.IsError {
		t.Fatal("presence is offered to assistants")
	}
}
