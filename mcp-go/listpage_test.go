// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"testing"
)

// an account with more presentations than one page: the newest first, the
// rest a page at a time, and found by name (only the first 50 were listed,
// with nothing saying so)
func TestListPresentationsPages(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	for i := 1; i <= 60; i++ {
		f.db.Set(context.Background(), "shares", fmt.Sprintf("deck%06d", i), Doc{"name": fmt.Sprintf("Deck %d", i), "owner": "u1", "md": "# x", "created": int64(1000 + i), "updated": int64(1000 + i)})
	}
	f.db.Set(context.Background(), "shares", "other00001", Doc{"name": "Deck 99", "owner": "u2", "md": "# x", "updated": int64(9999)})

	r := call(t, s, "list_presentations", map[string]any{})
	page := sc(r)
	eq(t, len(page["presentations"].([]any)), 50)
	eq(t, []any{page["total"], page["next_offset"]}, []any{60.0, 50.0})
	eq(t, page["presentations"].([]any)[0].(map[string]any)["title"], "Deck 60", "newest first")
	match(t, textOf(r), `1–50 of 60\. More: offset 50\.`)

	page = sc(call(t, s, "list_presentations", map[string]any{"offset": 50}))
	eq(t, len(page["presentations"].([]any)), 10)
	eq(t, page["presentations"].([]any)[9].(map[string]any)["title"], "Deck 1", "the oldest last")
	eq(t, page["next_offset"], nil)

	page = sc(call(t, s, "list_presentations", map[string]any{"query": "deck 5", "limit": 3}))
	eq(t, []any{page["total"], len(page["presentations"].([]any))}, []any{11.0, 3}, "Deck 5 and 50–59, not someone else's")
}
