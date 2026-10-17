// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"
	"time"
)

// One person's own budget: their regular and irregular items, what is left
// of a month and month by month; nobody else sees it, not even a member of
// the same space.
func TestWardenOwnBudget(t *testing.T) {
	e, base, stop := editorServer(t)
	defer stop()
	e.warden.now = func() time.Time { return time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC) }
	add := func(who string, a map[string]any) map[string]any {
		t.Helper()
		code, out := wardenCall(t, base, who, "save_own_item", a)
		eq(t, code, 200, out["error"])
		return out
	}
	add("tero", map[string]any{"kind": "regular_income", "title": "Palkka", "amount": 3000.0, "date": "2026-01-15"})
	rent := add("tero", map[string]any{"kind": "regular_expense", "title": "Vuokra", "amount": 1000.0})
	add("tero", map[string]any{"kind": "regular_expense", "title": "Autovakuutus", "amount": 600.0, "every_months": 6.0, "date": "2026-11-01"})
	add("tero", map[string]any{"kind": "estimate", "title": "Ruoka", "amount": 400.0})
	add("tero", map[string]any{"kind": "irregular_expense", "title": "Auton korjaus", "amount": 350.0, "date": "2026-12-03"})
	add("tero", map[string]any{"kind": "irregular_income", "title": "Veronpalautus", "amount": 200.0, "date": "2026-12-10"})
	code, out := wardenCall(t, base, "tero", "save_own_item", map[string]any{"kind": "irregular_expense", "title": "Lahja", "amount": 50.0})
	eq(t, []any{code, out["error"]}, []any{400, "an irregular item needs its date"})
	code, _ = wardenCall(t, base, "tero", "save_own_item", map[string]any{"kind": "salary", "title": "x", "amount": 1.0})
	eq(t, code, 400, "no such kind")

	code, out = wardenCall(t, base, "tero", "own_budget", map[string]any{"from": "2026-10", "to": "2026-12"})
	eq(t, code, 200, out["error"])
	eq(t, len(out["items"].([]any)), 6)
	eq(t, out["monthly"], map[string]any{"income": 3000.0, "expenses": 1500.0, "left": 1500.0}, "a month on average")
	months := out["months"].([]any)
	left := []any{}
	for _, m := range months {
		left = append(left, m.(map[string]any)["left"])
	}
	// October: 3000-1000-400; November: the car insurance too; December:
	// the repair and the tax refund
	eq(t, left, []any{1600.0, 1000.0, 1450.0})

	// changed: only the fields given
	add("tero", map[string]any{"item_id": rent["item_id"], "amount": 1100.0})
	_, out = wardenCall(t, base, "tero", "own_budget", map[string]any{"from": "2026-10", "to": "2026-10"})
	eq(t, out["months"].([]any)[0].(map[string]any)["left"], 1500.0)

	// anna, even in tero's space, sees only her own (none) and cannot touch his
	_, sp := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	wardenCall(t, base, "tero", "invite_member", map[string]any{"space_id": sp["space_id"], "email": "anna@example.com"})
	wardenCall(t, base, "anna", "list_spaces", nil)
	_, out = wardenCall(t, base, "anna", "own_budget", map[string]any{"space_id": sp["space_id"]})
	eq(t, len(out["items"].([]any)), 0, "anna sees none of tero's")
	code, _ = wardenCall(t, base, "anna", "delete_own_item", map[string]any{"item_id": rent["item_id"]})
	eq(t, code, 400, "anna cannot delete tero's")
	code, _ = wardenCall(t, base, "anna", "save_own_item", map[string]any{"item_id": rent["item_id"], "amount": 1.0})
	eq(t, code, 400, "anna cannot change tero's")
	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sp["space_id"]})
	if s := roomJSON(out); len(s) == 0 || contains(s, "Vuokra") {
		t.Fatalf("the space carries the own budget: %s", s)
	}

	code, _ = wardenCall(t, base, "tero", "delete_own_item", map[string]any{"item_id": rent["item_id"]})
	eq(t, code, 200)
	_, out = wardenCall(t, base, "tero", "own_budget", nil)
	eq(t, len(out["items"].([]any)), 5)
}
