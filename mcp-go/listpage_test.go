// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"testing"
)

// an account with more presentations than one page: the newest first, the
// rest a page at a time, and found by name (only the first 50 were listed,
// with nothing saying so); the default page holds 200
func TestListPresentationsPages(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	for i := 1; i <= 210; i++ {
		f.db.Set(context.Background(), "shares", fmt.Sprintf("deck%06d", i), Doc{"name": fmt.Sprintf("Deck %d", i), "owner": "u1", "md": "# x", "created": int64(1000 + i), "updated": int64(1000 + i)})
	}
	f.db.Set(context.Background(), "shares", "other00001", Doc{"name": "Deck 99", "owner": "u2", "md": "# x", "updated": int64(9999)})

	r := call(t, s, "list_presentations", map[string]any{})
	page := sc(r)
	eq(t, len(page["presentations"].([]any)), 200)
	eq(t, []any{page["total"], page["next_offset"]}, []any{210.0, 200.0})
	eq(t, page["presentations"].([]any)[0].(map[string]any)["title"], "Deck 210", "newest first")
	match(t, textOf(r), `1–200 of 210\. More: offset 200\.`)

	page = sc(call(t, s, "list_presentations", map[string]any{"offset": 200}))
	eq(t, len(page["presentations"].([]any)), 10)
	eq(t, page["presentations"].([]any)[9].(map[string]any)["title"], "Deck 1", "the oldest last")
	eq(t, page["next_offset"], nil)

	page = sc(call(t, s, "list_presentations", map[string]any{"query": "deck 5", "limit": 3}))
	eq(t, []any{page["total"], len(page["presentations"].([]any))}, []any{11.0, 3}, "Deck 5 and 50–59, not someone else's")
	r = call(t, s, "list_presentations", map[string]any{"offset": 500})
	match(t, textOf(r), `No presentations on this page; 210 in all\.`)
	eq(t, sc(r)["total"], 210.0, "total on every page")
}
