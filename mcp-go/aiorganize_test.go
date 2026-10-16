// SPDX-License-Identifier: AGPL-3.0-or-later

// The AI assistant sorts presentations into topics and folders
// (aiorganize.go).

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// The model's calls as a plan: a folder once, a deck's last move, asked
// first when more than one moves.
func TestAIPlan(t *testing.T) {
	_, ok := aiPlaceOf("move_presentation", map[string]any{"deck_id": "d1"})
	eq(t, ok, false, "no room")
	_, ok = aiPlaceOf("create_folder", map[string]any{"room_id": "r1", "name": "  "})
	eq(t, ok, false, "no name")
	_, ok = aiPlaceOf("delete_folder", map[string]any{"room_id": "r1", "folder_id": "f1"})
	eq(t, ok, false, "not the drawer's")
	_, ok = aiPlaceOf("move_presentation", map[string]any{"deck_id": "d1", "room_id": "r1", "folder_id": "new: "})
	eq(t, ok, false, "a new folder without a name")

	var pl aiPlan
	for _, c := range []struct {
		name string
		args map[string]any
	}{
		{"create_folder", map[string]any{"room_id": "r1", "name": " Testing  decks "}},
		{"move_presentation", map[string]any{"deck_id": "d1", "room_id": "general"}},
		{"create_folder", map[string]any{"room_id": "r1", "name": "testing decks"}},
		{"move_presentation", map[string]any{"deck_id": "d1", "room_id": "r1", "folder_id": "new:Testing decks"}},
	} {
		p, ok := aiPlaceOf(c.name, c.args)
		if !ok {
			t.Fatalf("%s %v not taken", c.name, c.args)
		}
		pl.add(p)
	}
	eq(t, pl.Steps, []aiPlace{
		{Tool: "create_folder", RoomID: "r1", Name: "testing decks"},
		{Tool: "move_presentation", RoomID: "r1", DeckID: "d1", FolderID: "new:Testing decks"},
	}, "the folder once, the deck's last move")
	eq(t, []any{pl.moves(), pl.asks()}, []any{1, false}, "one move: made at once")
	p, _ := aiPlaceOf("move_presentation", map[string]any{"deck_id": "d2", "room_id": "r1", "folder_id": "f9"})
	pl.add(p)
	p, _ = aiPlaceOf("move_presentation", map[string]any{"deck_id": "nobodys", "room_id": "r1"})
	pl.add(p)
	eq(t, pl.asks(), true, "several: asked first")

	dropped := pl.name(map[string]string{"d1": "Plan", "d2": "Test 2"}, map[string]string{"r1": "Web"}, map[string]map[string]string{"r1": {"f9": "Old"}})
	eq(t, dropped, 1, "a deck not theirs is left out")
	eq(t, []any{pl.Steps[1].Deck, pl.Steps[1].Room, pl.Steps[1].Folder, pl.Steps[2].Folder}, []any{"Plan", "Web", "Testing decks", "Old"})

	// made in order; the new folder once, its id given to the move
	var ran []string
	done := pl.run(func(op string, args map[string]any) (map[string]any, error) {
		b, _ := json.Marshal(args)
		ran = append(ran, op+" "+string(b))
		switch op {
		case "create_folder":
			return map[string]any{"folder_id": "f1"}, nil
		case "move_presentation":
			if args["deck_id"] == "d2" {
				return nil, errors.New("no folder f9 in that room")
			}
		}
		return map[string]any{"room": "Web", "folder": "testing decks"}, nil
	})
	eq(t, ran, []string{
		`create_folder {"name":"testing decks","room_id":"r1"}`,
		`move_presentation {"deck_id":"d1","folder_id":"f1","room_id":"r1"}`,
		`move_presentation {"deck_id":"d2","folder_id":"f9","room_id":"r1"}`,
	})
	eq(t, []any{done[1].Error, done[1].Folder, done[2].Error}, []any{"", "testing decks", "no folder f9 in that room"})

	// a move into a folder the plan does not make first makes it
	ran = nil
	aiPlan{Steps: []aiPlace{{Tool: "move_presentation", RoomID: "r1", DeckID: "d1", FolderID: "new:X"}}}.run(func(op string, args map[string]any) (map[string]any, error) {
		ran = append(ran, op)
		return map[string]any{"folder_id": "fx"}, nil
	})
	eq(t, ran, []string{"create_folder", "move_presentation"})
}

// sliqtly.com: the drawer reads where decks are, moves one at once, asks
// before several, and makes the plan the person says yes to.
func TestAIOrganize(t *testing.T) {
	e, base, stop := editorServer(t)
	defer stop()
	e.layouts = newLayoutService(e.DB)
	ctx := context.Background()
	for id, name := range map[string]string{"deckaaa1": "Test one", "deckaaa2": "Test two", "deckaaa3": "Plan"} {
		if err := e.DB.Set(ctx, "shares", id, Doc{"owner": "u-anna", "name": name, "created": time.Now().UnixMilli()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.DB.Set(ctx, "shares", "bobsdeck", Doc{"owner": "u-bob", "name": "Bob's"}); err != nil {
		t.Fatal(err)
	}

	// the reader has the topics' reads and the moves
	r := aiDeckReader(e, "u-anna", aiQuestion{})
	var names []string
	for _, tl := range r.Tools {
		names = append(names, tl.Name)
	}
	for _, want := range []string{"list_presentations", "list_rooms", "get_room"} {
		if !slices.Contains(names, want) {
			t.Errorf("no %s in %v", want, names)
		}
	}
	eq(t, r.Places, true)
	got := r.Run(ctx, "list_presentations", map[string]any{})
	match(t, got.Text, "Test one")

	ai := &fakeAI{reply: "Moved it."}
	e.EditorGate.ai = newAIAssist(ai, e.DB)
	e.EditorGate.ai.now = func() time.Time { return editorNow }
	api := base + "/editor/api/ai/"
	ask := `{"message":"Put the test decks in a folder","scope":"slide","context":{}}`
	answer := func(body string) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(body)
		}
		return out
	}
	filed := func(deck string) (string, string) {
		l, _ := e.layouts.ls.read(ctx, "u-anna")
		v := viewOf(l)
		f := v.folderOf(pageDeck(deck))
		return f, folderName(v.folders[v.roomOf(pageDeck(deck))], f)
	}

	// one move, into a folder made for it: at once
	ai.places = []aiPlace{{Tool: "create_folder", RoomID: "general", Name: "Testing"}, {Tool: "move_presentation", RoomID: "general", DeckID: "deckaaa1", FolderID: "new:Testing"}}
	res, body := editorDo(t, "POST", api+"ask", "anna", base, ask)
	eq(t, res.StatusCode, 200, body)
	out := answer(body)
	eq(t, out["organize"], nil, "not asked", body)
	if out["placed"] == nil {
		t.Fatal(body)
	}
	placed := out["placed"].([]any)
	eq(t, len(placed), 2)
	mv := placed[1].(map[string]any)
	eq(t, []any{mv["deck"], mv["room"], mv["folder"], mv["error"]}, []any{"Test one", "General", "Testing", nil})
	_, fname := filed("deckaaa1")
	eq(t, fname, "Testing", "filed")

	// two moves (and someone else's deck, left out): asked first, nothing moved
	ai.places = []aiPlace{
		{Tool: "move_presentation", RoomID: "general", DeckID: "deckaaa2", FolderID: "new:Testing"},
		{Tool: "move_presentation", RoomID: "general", DeckID: "deckaaa3", FolderID: "new:Plans"},
		{Tool: "move_presentation", RoomID: "general", DeckID: "bobsdeck"},
	}
	_, body = editorDo(t, "POST", api+"ask", "anna", base, ask)
	out = answer(body)
	eq(t, out["placed"], nil, "nothing made yet")
	steps := out["organize"].([]any)
	eq(t, len(steps), 2, "Bob's deck is not in the plan")
	eq(t, steps[0].(map[string]any)["deck"], "Test two")
	if f, _ := filed("deckaaa2"); f != "" {
		t.Fatal("moved before the person said yes")
	}

	// the person says yes: made, no AI action spent
	before := answer(bodyOf(editorDo(t, "GET", api+"credits", "anna", "", "")))["credits"].(map[string]any)["left"]
	b, _ := json.Marshal(map[string]any{"steps": append(steps, map[string]any{"tool": "move_presentation", "room_id": "general", "deck_id": "bobsdeck"})})
	res, body = editorDo(t, "POST", api+"organize", "anna", "https://evil.example", string(b))
	eq(t, res.StatusCode, 403, "from another site")
	res, body = editorDo(t, "POST", api+"organize", "anna", base, string(b))
	eq(t, res.StatusCode, 200, body)
	placed = answer(body)["placed"].([]any)
	eq(t, len(placed), 2, "Bob's deck left out")
	for _, p := range placed {
		eq(t, p.(map[string]any)["error"], nil, p)
	}
	_, f2 := filed("deckaaa2")
	_, f3 := filed("deckaaa3")
	eq(t, []string{f2, f3}, []string{"Testing", "Plans"})
	after := answer(bodyOf(editorDo(t, "GET", api+"credits", "anna", "", "")))["credits"].(map[string]any)["left"]
	eq(t, after, before)

	res, _ = editorDo(t, "POST", api+"organize", "anna", base, `{"steps":[]}`)
	eq(t, res.StatusCode, 400)
}

func bodyOf[T any](_ T, body string) string { return body }

// Gemini's create_folder and move_presentation calls are the answer's plan,
// the folder's id new:<name> for the moves.
func TestAIGeminiPlaces(t *testing.T) {
	var asks []string
	replies := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"list_presentations","args":{}}}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Two test decks into Testing."},{"functionCall":{"name":"create_folder","args":{"room_id":"general","name":"Testing"}}},{"functionCall":{"name":"move_presentation","args":{"deck_id":"d1","room_id":"general","folder_id":"new:Testing"}}},{"functionCall":{"name":"move_presentation","args":{"deck_id":"d2","room_id":"general","folder_id":"new:Testing"}}},{"functionCall":{"name":"move_presentation","args":{"deck_id":"d3"}}}]}}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		asks = append(asks, string(b))
		io.WriteString(w, replies[len(asks)-1])
	}))
	defer srv.Close()
	r := &aiReader{
		Tools:  []aiTool{{"list_presentations", "decks", aiObj(map[string]any{})}},
		Run:    func(context.Context, string, map[string]any) aiRead { return aiRead{Text: `{"presentations":[]}`} },
		Places: true,
	}
	m := &geminiModel{http: srv.Client(), model: "g", key: "k", base: srv.URL, tools: aiEditTools}
	a, err := m.AskReading(context.Background(), "sys", []aiTurn{{Role: "user", Text: "sort the test decks"}}, r)
	if err != nil {
		t.Fatal(err)
	}
	match(t, asks[0], `"name":"move_presentation"`)
	eq(t, a.Text, "Two test decks into Testing.")
	eq(t, a.Places.Steps, []aiPlace{
		{Tool: "create_folder", RoomID: "general", Name: "Testing"},
		{Tool: "move_presentation", RoomID: "general", DeckID: "d1", FolderID: "new:Testing"},
		{Tool: "move_presentation", RoomID: "general", DeckID: "d2", FolderID: "new:Testing"},
	}, "the call without a room is refused")
	eq(t, a.Places.asks(), true)

	// without them (a server that keeps no topics) the model has no such tools
	asks = nil
	replies = []string{`{"candidates":[{"content":{"role":"model","parts":[{"text":"No."}]}}]}`}
	r.Places = false
	m.AskReading(context.Background(), "sys", []aiTurn{{Role: "user", Text: "sort"}}, r)
	if strings.Contains(asks[0], `"name":"move_presentation"`) {
		t.Fatal("move offered where there are no topics")
	}
}
