// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "testing"

// list_rooms' choosing, ordering and paging, on rows alone
func TestRoomQueryPick(t *testing.T) {
	rows := []roomRow{
		{RoomID: "general", Title: "General", Created: 1, Active: 50},
		{RoomID: "playground", Title: "Playground", Created: 1, Active: 1},
		{RoomID: "a", Title: "N11-1234 Checkout retry", About: "Payments fail on retry", Created: 10, Active: 40},
		{RoomID: "b", Title: "N11-2000 Login", Created: 30, Active: 30},
		{RoomID: "c", Title: "budget 2027", About: "Finance team's plan", Created: 20, Active: 90},
	}
	ids := func(rs []roomRow) []any {
		out := []any{}
		for _, r := range rs {
			out = append(out, r.RoomID)
		}
		return out
	}
	q := func(a map[string]any) roomQuery {
		t.Helper()
		x, err := roomQueryOf(a)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	got, total := q(nil).pick(rows)
	eq(t, []any{ids(got), total}, []any{[]any{"general", "playground", "b", "c", "a"}, 5})
	got, _ = q(map[string]any{"order": "active"}).pick(rows)
	eq(t, ids(got), []any{"c", "general", "a", "b", "playground"})
	got, _ = q(map[string]any{"order": "title"}).pick(rows)
	eq(t, ids(got), []any{"c", "general", "a", "b", "playground"})

	// every word, any case, in the title or the description
	got, total = q(map[string]any{"query": "n11"}).pick(rows)
	eq(t, []any{ids(got), total}, []any{[]any{"b", "a"}, 2})
	got, _ = q(map[string]any{"query": "n11 PAYMENTS"}).pick(rows)
	eq(t, ids(got), []any{"a"})
	got, _ = q(map[string]any{"query": "finance"}).pick(rows)
	eq(t, ids(got), []any{"c"})

	// pages
	got, total = q(map[string]any{"order": "active", "limit": 2.0, "offset": 2.0}).pick(rows)
	eq(t, []any{ids(got), total}, []any{[]any{"a", "b"}, 5})
	got, _ = q(map[string]any{"offset": 9.0}).pick(rows)
	eq(t, len(got), 0)
	eq(t, q(map[string]any{"limit": 5000.0}).Limit, roomPageMax)
	eq(t, q(nil).Limit, roomPageDefault)
	if _, err := roomQueryOf(map[string]any{"order": "newest"}); err == nil {
		t.Fatal("unknown order taken")
	}
}
