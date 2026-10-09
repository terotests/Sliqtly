// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
)

const searchDeck = "---\ntheme: aurora\n---\n\n# Quarterly review {transition=slide}\n\nRevenue grew in [Pohjois-Savo](https://example.com/savo).\n\n```mermaid\nflowchart LR\n  Kuopio --> Iisalmi\n```\n\n::: notes\nMention the new warehouse.\n:::\n"

// what is searched is what a reader sees: never the syntax around it
func TestSearchDecks(t *testing.T) {
	docs := []deckDoc{
		{"deckAAAAAA", Doc{"name": "Q3", "md": searchDeck, "updated": int64(10), "room": "general"}},
		{"deckBBBBBB", Doc{"name": "Aurora plans", "md": "# Plans\n\nNothing here.\n", "updated": int64(20)}},
	}
	ids := func(q string) []string {
		out := []string{}
		for _, h := range searchDecks(docs, q, searchMax, func(id string) string { return "Room " + id }) {
			out = append(out, h.DeckID)
		}
		return out
	}
	eq(t, ids("revenue"), []string{"deckAAAAAA"})
	eq(t, ids("QUARTERLY grew"), []string{"deckAAAAAA"})
	eq(t, ids("warehouse"), []string{"deckAAAAAA"}, "speaker notes")
	eq(t, ids("pohjois-savo"), []string{"deckAAAAAA"}, "a link's words")
	// a deck's name is searched too; the newest first
	eq(t, ids("aurora"), []string{"deckBBBBBB"}, "front matter's value is not text")
	eq(t, ids("plans"), []string{"deckBBBBBB"})
	for _, q := range []string{"theme", "transition", "example.com", "flowchart", "Kuopio", "mermaid", ":::", "revenue zebra", "  "} {
		eq(t, ids(q), []string{}, q)
	}
	h := searchDecks(docs, "warehouse", searchMax, func(id string) string { return "Room " + id })[0]
	eq(t, []any{h.Name, h.RoomID, h.Room, h.Updated}, []any{"Q3", "general", "Room general", int64(10)})
	match(t, h.Snippet, `new warehouse`)
	// only the name: no text to show
	eq(t, searchDecks(docs, "aurora", searchMax, nil)[0].Snippet, "")
	eq(t, len(searchDecks(docs, "e", 1, nil)), 1, "at most max")
}

// the folder server's search_presentations: rooms named, the page's too
func TestSearchPresentationsOnFolderServer(t *testing.T) {
	ctx := context.Background()
	e, _, err := localEnv(t.TempDir(), "http://x", "local")
	if err != nil {
		t.Fatal(err)
	}
	e.DB.Set(ctx, "shares", "deck000001", Doc{"name": "Review", "owner": "local", "md": searchDeck})
	e.DB.Set(ctx, "shares", "deck000002", Doc{"name": "Other", "owner": "local", "md": "# Other\n"})
	as := func(op string, a map[string]any) (map[string]any, error) {
		out, err := e.rooms.callVia(ctx, "local", viaPage, op, a)
		b, _ := json.Marshal(out)
		var m map[string]any
		json.Unmarshal(b, &m)
		return m, err
	}
	out, err := as("search_presentations", map[string]any{"query": "warehouse"})
	if err != nil {
		t.Fatal(err)
	}
	found := list(out["presentations"])
	eq(t, len(found), 1)
	f := mapOf(found[0])
	eq(t, []any{f["deck_id"], f["name"], f["room_id"], f["room"]}, []any{"deck000001", "Review", "general", "General"})
	match(t, f["snippet"].(string), `warehouse`)
	if _, err := as("search_presentations", map[string]any{"query": " "}); err == nil {
		t.Fatal("an empty query answered")
	}
	if !findMcpRoomTool("search_presentations") {
		t.Fatal("not an assistant's tool")
	}
}

// sliqtly.com's editor: the signed-in user's own decks and those they were
// invited to edit, no one else's, whether private or not
func TestEditorSearchOwnDecksOnly(t *testing.T) {
	e, base, stop := editorServer(t)
	defer stop()
	ctx := context.Background()
	e.DB.Set(ctx, "shares", "deckAAAAAA", Doc{"name": "Anna's", "owner": "u-anna", "md": searchDeck, "visibility": "private"})
	e.DB.Set(ctx, "shares", "deckBBBBBB", Doc{"name": "Tero's", "owner": "u-tero", "md": searchDeck, "visibility": "link"})
	e.DB.Set(ctx, "shares", "deckCCCCCC", Doc{"name": "Tero's, Anna invited", "owner": "u-tero", "md": "# Budget\n", "visibility": "private", "editors": []any{"anna@example.com"}})
	search := func(who, q string) (int, []any) {
		res, body := editorDo(t, "GET", base+"/editor/api/search?q="+url.QueryEscape(q), who, "", "")
		var out map[string]any
		json.Unmarshal([]byte(body), &out)
		return res.StatusCode, list(out["presentations"])
	}
	code, _ := search("", "revenue")
	eq(t, code, 401)
	code, found := search("anna", "revenue")
	eq(t, []any{code, len(found)}, []any{200, 1})
	eq(t, mapOf(found[0])["deck_id"], "deckAAAAAA")
	_, found = search("tero", "warehouse")
	eq(t, len(found), 1)
	eq(t, mapOf(found[0])["deck_id"], "deckBBBBBB")
	_, found = search("anna", "budget")
	eq(t, len(found), 1, "invited")
	eq(t, mapOf(found[0])["deck_id"], "deckCCCCCC")
	_, found = search("tero", "budget")
	eq(t, len(found), 1, "the owner's own")
	_, found = search("anna", "")
	eq(t, len(found), 0)
}
