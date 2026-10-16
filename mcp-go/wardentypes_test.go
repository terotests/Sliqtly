// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"slices"
	"testing"
)

func TestWardenTypesSchema(t *testing.T) {
	seen := map[string]bool{}
	for _, ty := range wardenTypes {
		if seen[ty.ID] || !wardenIDRe.MatchString(ty.ID) {
			t.Fatalf("type id %q twice or not an id", ty.ID)
		}
		seen[ty.ID] = true
		if ty.Kind != "event" && ty.Kind != "deadline" && ty.Kind != "record" {
			t.Fatalf("%s: kind %q", ty.ID, ty.Kind)
		}
		if ty.Name["en"] == "" || ty.Name["fi"] == "" || (len(ty.Packs) == 0 && !ty.Legacy) {
			t.Fatalf("%s: names or packs missing", ty.ID)
		}
		if ty.Legacy && wardenTypeOf(ty.ReplacedBy) == nil {
			t.Fatalf("%s: replaced by no type", ty.ID)
		}
		keys := map[string]bool{}
		for _, f := range ty.Fields {
			if keys[f.Key] || f.Name["en"] == "" || f.Name["fi"] == "" {
				t.Fatalf("%s.%s: twice or unnamed", ty.ID, f.Key)
			}
			keys[f.Key] = true
			found := false
			for _, k := range wardenFieldTypes {
				found = found || k == f.Type
			}
			if !found {
				t.Fatalf("%s.%s: field type %q", ty.ID, f.Key, f.Type)
			}
			if (f.Type == "choice") != (len(f.Options) > 0) {
				t.Fatalf("%s.%s: options are a choice's", ty.ID, f.Key)
			}
			for _, o := range f.Options {
				if f.Labels[o]["en"] == "" || f.Labels[o]["fi"] == "" {
					t.Fatalf("%s.%s: option %s has no label", ty.ID, f.Key, o)
				}
			}
		}
		// what it requires, puts in the budget and reminds of are its own
		for _, k := range ty.Required {
			if _, ok := ty.field(k); !ok && !slices.Contains(wardenCoreKeys, k) {
				t.Fatalf("%s: requires %s, which it has not", ty.ID, k)
			}
		}
		for _, k := range ty.Budget {
			if f, ok := ty.field(k); !ok || f.Type != "money" {
				t.Fatalf("%s: budget field %s is not money", ty.ID, k)
			}
		}
		for _, r := range ty.Reminders {
			if f, ok := ty.field(r.Field); r.Field != "date" && (!ok || f.Type != "date") {
				t.Fatalf("%s: reminder field %s is not a date", ty.ID, r.Field)
			}
		}
		if ty.Group == "" {
			t.Fatalf("%s: no group", ty.ID)
		}
	}
	common := map[string]bool{}
	for _, f := range wardenCommon {
		if common[f.Key] || !slices.Contains(wardenFieldTypes, f.Type) {
			t.Fatalf("common %s: twice or of no known type", f.Key)
		}
		common[f.Key] = true
	}
	for _, ty := range wardenTypes {
		for _, f := range ty.Fields {
			if common[f.Key] {
				t.Fatalf("%s.%s is a common field too", ty.ID, f.Key)
			}
		}
	}
	if !seen["event"] {
		t.Fatal("no plain event type")
	}
	eq(t, len(wardenTypesFor("business")) < len(wardenTypesFor("family")), true, "packs filter")
	legacy := 0
	for _, ty := range wardenTypes {
		if ty.Legacy {
			legacy++
		}
	}
	eq(t, len(wardenTypesFor("demo")), len(wardenTypes)-legacy, "demo offers all but the legacy ones")
}

func TestWardenEntries(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)

	code, out := wardenCall(t, base, "tero", "list_entry_types", map[string]any{"space_id": sid})
	eq(t, code, 200, "types")
	eq(t, len(out["types"].([]any)), len(wardenTypesFor("family")))

	// a party invitation: its fields, a gift as a personal cost
	code, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "birthday_invitation", "title": "Ellan synttärit",
		"date": "2026-11-07", "start": "14:00", "ward": "Aino",
		"fields": map[string]any{"host": "Ella", "rsvp": "going", "takes": "Tero", "gift_bought": false},
		"cost":   map[string]any{"amount": 15.5, "payer": "Tero", "scope": "personal"}})
	eq(t, code, 200, "invitation saved")
	inv := out["event_id"].(string)
	eq(t, []any{out["type"], out["fields"].(map[string]any)["rsvp"], out["cost"].(map[string]any)["scope"]}, []any{"birthday_invitation", "going", "personal"})

	// fields are merged, null removes; a wrong field or choice is refused
	code, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "event_id": inv,
		"fields": map[string]any{"gift_bought": true, "gift_buyer": "Tero", "takes": nil}})
	f := out["fields"].(map[string]any)
	eq(t, []any{code, f["gift_bought"], f["host"], f["takes"]}, []any{200, true, "Ella", nil}, "merged")
	code, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "event_id": inv, "fields": map[string]any{"rsvp": "perhaps"}})
	eq(t, []any{code, out["error"]}, []any{400, "fields.rsvp is one of open, going, not_going, maybe"})
	code, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "event_id": inv, "fields": map[string]any{"colour": "x"}})
	eq(t, code, 400, "unknown field: "+out["error"].(string))
	code, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "x", "date": "2026-11-01", "type": "spaceship"})
	eq(t, code, 400, "unknown type")
	code, _ = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "x", "date": "2026-11-01", "cost": map[string]any{"amount": 5, "scope": "personal"}})
	eq(t, code, 400, "a personal cost names its payer")

	// records need no day; a thing to sell gets the next label
	code, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "item", "title": "Luistimet",
		"fields": map[string]any{"category": "hobby", "state": "repair", "location": "Eteinen", "repair_days": 7}})
	eq(t, []any{code, out["date"]}, []any{200, nil}, "a thing without a day")
	code, _ = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "Ei päivää"})
	eq(t, code, 400, "an event needs its day")
	_, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "sale_item", "title": "Talvitakki", "fields": map[string]any{"price": 12}})
	_, out2 := wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "sale_item", "title": "Pipo"})
	eq(t, []any{out["fields"].(map[string]any)["label"], out2["fields"].(map[string]any)["label"]}, []any{"001", "002"}, "labels")

	// a deadline is due remind_days before its day
	_, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "identity_document_event", "title": "Ainon passi", "date": "2027-03-01",
		"fields": map[string]any{"what": "passport", "holder": "Aino", "remind_days": 60}, "cost": map[string]any{"amount": 48}})
	eq(t, out["remind_from"], "2026-12-31", "remind")

	// list by type; records are listed for any range
	_, out = wardenCall(t, base, "tero", "list_events", map[string]any{"space_id": sid, "type": "item", "from": "2026-11-01"})
	eq(t, len(out["events"].([]any)), 1, "items")

	// budget: a hobby fee in three parts, shared
	wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "activity", "title": "Jalkapallo", "date": "2026-09-01",
		"start": "17:00", "rrule": "FREQ=WEEKLY;BYDAY=TU", "ward": "Aino", "cost": map[string]any{"amount": 300, "payer": "Anna", "instalments": 3, "first": "2026-10-31"}})
	code, out = wardenCall(t, base, "tero", "budget", map[string]any{"space_id": sid, "from": "2026-10", "to": "2027-03"})
	eq(t, code, 200, "budget")
	months := out["months"].([]any)
	eq(t, len(months), 6)
	nov := months[1].(map[string]any)
	eq(t, []any{nov["month"], nov["total"], nov["shared"], len(nov["lines"].([]any))}, []any{"2026-11", 115.5, 100.0, 2}, "november")
	dec := months[2].(map[string]any)
	eq(t, dec["lines"].([]any)[0].(map[string]any)["date"], "2026-12-31", "the day of the month, clamped")
	eq(t, []any{out["total"], out["shared"], out["by_payer"]}, []any{363.5, 348.0, map[string]any{"Anna": 300.0, "Tero": 15.5}})
	_, out = wardenCall(t, base, "tero", "budget", map[string]any{"space_id": sid, "from": "2026-10", "to": "2027-03", "payer": "tero"})
	eq(t, out["total"], 15.5, "tero's own")
	code, _ = wardenCall(t, base, "tero", "budget", map[string]any{"space_id": sid, "from": "2026-13"})
	eq(t, code, 400, "a bad month")

	// changing the type drops fields the new one has not
	_, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "event_id": inv, "type": "event", "cost": nil})
	eq(t, []any{out["type"], out["fields"], out["cost"]}, []any{nil, nil, nil}, "a plain event again")
}

func TestWardenFamilyEntries(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)

	// list_entry_types names the common fields and each type's rules
	_, out = wardenCall(t, base, "tero", "list_entry_types", map[string]any{"space_id": sid})
	eq(t, len(out["common"].([]any)), len(wardenCommon), "common fields")
	ids := []string{}
	for _, ty := range out["types"].([]any) {
		ids = append(ids, ty.(map[string]any)["id"].(string))
	}
	eq(t, []any{slices.Contains(ids, "birthday_invitation"), slices.Contains(ids, "invitation"), slices.Contains(ids, "schedule_exception")}, []any{true, false, true}, "family types, no legacy")

	// required: a babysitter needs its time and its sitter
	code, out := wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "babysitter_booking", "title": "Hoitaja", "date": "2026-11-07"})
	eq(t, []any{code, out["error"]}, []any{400, "a babysitter_booking needs start, fields.sitter"})

	// common fields on any type; lists from an array or from text
	code, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "school_event", "title": "Retki Nuuksioon", "date": "2026-11-12", "ward": "Leo",
		"fields": map[string]any{"what": "trip", "bring": "eväät; kumisaappaat\nsadetakki", "status": "confirmed", "responsible": []any{"Mia"}, "fee": 8, "pay_by": "2026-11-05"}})
	eq(t, code, 200, "school trip: "+wardenErrOf(out))
	f := out["fields"].(map[string]any)
	eq(t, []any{f["bring"], f["responsible"], f["status"]}, []any{[]any{"eväät", "kumisaappaat", "sadetakki"}, []any{"Mia"}, "confirmed"})
	// its reminders: paying, and the day before
	eq(t, out["reminders"], []any{
		map[string]any{"field": "pay_by", "date": "2026-11-05", "day": "2026-11-02"},
		map[string]any{"field": "date", "date": "2026-11-12", "day": "2026-11-11"}}, "reminders")
	code, _ = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "school_event", "title": "x", "date": "2026-11-12", "ward": "Leo", "fields": map[string]any{"status": "sometime"}})
	eq(t, code, 400, "a common choice is checked")

	// the budget counts a type's money field when there is no cost
	wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "bill", "title": "Sähkölasku", "date": "2026-11-20",
		"fields": map[string]any{"amount": 84.2, "payer": "Mia"}})
	wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "need", "title": "Talvikengät",
		"fields": map[string]any{"estimate": 60, "include_in_budget": false}})
	_, out = wardenCall(t, base, "tero", "budget", map[string]any{"space_id": sid, "from": "2026-11", "to": "2026-11"})
	eq(t, []any{out["total"], out["by_payer"]}, []any{92.2, map[string]any{"Mia": 84.2}}, "the bill and the trip's fee, not the need left out")
}

func wardenErrOf(out map[string]any) string {
	s, _ := out["error"].(string)
	return s
}

func TestWardenLegacyType(t *testing.T) {
	sp := &wardenSpace{doc: Doc{"vocab": "family"}}
	if _, err := sp.entryType("invitation", ""); err == nil {
		t.Fatal("a new invitation in a family space")
	}
	if _, err := sp.entryType("invitation", "invitation"); err != nil {
		t.Fatal("an invitation made before stays one")
	}
}
