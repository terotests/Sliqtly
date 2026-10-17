// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"regexp"
	"slices"
	"testing"
	"time"
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
		for _, a := range ty.AlsoIn {
			if wardenTypeOf(a.Type) == nil || a.Type == ty.ID {
				t.Fatalf("%s: also listed as %q, no other type", ty.ID, a.Type)
			}
			if _, ok := ty.field(a.When.Field); !ok {
				t.Fatalf("%s: also listed while %s, which it has not", ty.ID, a.When.Field)
			}
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
		if g := ty.Get; g != nil {
			if f, ok := ty.field(g.By); g.By != "date" && (!ok || f.Type != "date") {
				t.Fatalf("%s: gets by %s, which is not a date", ty.ID, g.By)
			}
			for _, c := range append(slices.Clone(g.Done), func() []wardenCond {
				if g.When != nil {
					return []wardenCond{*g.When}
				}
				return nil
			}()...) {
				f, ok := ty.field(c.Field)
				if !ok || (c.Is != "" && !slices.Contains(f.Options, c.Is)) {
					t.Fatalf("%s: get condition on %s=%s", ty.ID, c.Field, c.Is)
				}
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

func TestWardenGetAndPay(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)

	// a window to get a gift in, before the party; it is reminded from its first day
	code, out := wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "Mummon synttärit", "date": "2026-12-12",
		"fields": map[string]any{"get_from": "2026-11-20", "get_by": "2026-12-10", "get_what": "lahja; kortti"}})
	eq(t, code, 200, "window: "+wardenErrOf(out))
	eq(t, out["get_window"], map[string]any{"from": "2026-11-20", "by": "2026-12-10"}, "the window")
	eq(t, out["reminders"], []any{map[string]any{"field": "get_by", "date": "2026-12-10", "day": "2026-11-20"}}, "reminded from its first day")
	code, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "x", "date": "2026-12-12",
		"fields": map[string]any{"get_from": "2026-12-11", "get_by": "2026-12-10"}})
	eq(t, code, 400, "a window backwards: "+wardenErrOf(out))

	// a missing thing is got by its day, in the two weeks before; not once it is in order
	_, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "item", "title": "Lasten sängyt", "date": "2026-10-30",
		"fields": map[string]any{"category": "home", "state": "missing"}})
	eq(t, out["get_window"], map[string]any{"from": "2026-10-16", "by": "2026-10-30"}, "a missing thing")
	_, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "event_id": out["event_id"], "fields": map[string]any{"state": "ok"}})
	eq(t, out["get_window"], nil, "in order: nothing to get")
	// a need by needed_by, until it is bought
	_, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "need", "title": "Talvikengät",
		"fields": map[string]any{"needed_by": "2026-11-15", "state": "searching"}})
	eq(t, out["get_window"], map[string]any{"from": "2026-11-01", "by": "2026-11-15"}, "a need")
	_, out = wardenCall(t, base, "tero", "list_events", map[string]any{"space_id": sid, "from": "2026-11-21", "to": "2026-11-30"})
	listed := []any{}
	for _, e := range out["events"].([]any) {
		listed = append(listed, e.(map[string]any)["title"])
	}
	eq(t, listed, []any{"Talvikengät", "Mummon synttärit"}, "the party for its window, and the need (a record, listed always)")

	// family or own budget per entry; a person's budget is their own and the family's
	wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "stock", "title": "Yhteinen varasto", "fields": map[string]any{"actual_cost": 40, "payer": "Mia"}, "date": ""})
	wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "Sähkösopimus", "date": "2026-11-05",
		"cost": map[string]any{"amount": 600, "payer": "Tero", "scope": "personal", "instalments": 12}})
	wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "Patja", "date": "2026-11-08",
		"fields": map[string]any{"actual_cost": 300, "budget_scope": "personal", "budget_owner": "Mia"}})
	_, out = wardenCall(t, base, "tero", "budget", map[string]any{"space_id": sid, "from": "2026-11", "to": "2026-11", "person": "Tero"})
	nov := out["months"].([]any)[0].(map[string]any)
	titles := []any{}
	for _, l := range nov["lines"].([]any) {
		titles = append(titles, l.(map[string]any)["title"])
	}
	eq(t, titles, []any{"Sähkösopimus"}, "Tero's November: his electricity, not Mia's mattress")
	_, out = wardenCall(t, base, "tero", "budget", map[string]any{"space_id": sid, "from": "2026-11", "to": "2026-11", "person": "Mia"})
	eq(t, []any{out["total"], out["personal"], out["by_payer"]}, []any{300.0, 300.0, map[string]any{"Mia": 300.0}}, "Mia's: her mattress")
}

func TestWardenNextPayment(t *testing.T) {
	d := Doc{"title": "Sähkösopimus", "date": "2026-01-31", "cost": map[string]any{"amount": 120.0, "scope": "shared", "instalments": 12.0, "every_months": 1.0}}
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	eq(t, wardenNextPayment(d, now), map[string]any{"date": "2026-10-31", "amount": 10.0, "n": 10, "of": 12, "remind_from": "2026-10-28"}, "October's, three days ahead")
	eq(t, wardenReminders(d, now), []any{map[string]any{"field": "cost", "date": "2026-10-31", "day": "2026-10-28"}}, "its reminder")
	d["cost"].(map[string]any)["remind_days"] = 0.0
	eq(t, wardenNextPayment(d, now)["remind_from"], nil, "0: no reminder")
	eq(t, wardenNextPayment(d, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)), map[string]any(nil), "all paid")
	one := Doc{"date": "2026-11-02", "cost": map[string]any{"amount": 50.0, "instalments": 1.0}}
	eq(t, wardenNextPayment(one, now), map[string]any{"date": "2026-11-02", "amount": 50.0, "n": 1, "of": 1}, "one payment: no reminder unless asked")
}

// a thing that needs repair is listed under repair too, and a missing one
// under need; one in order only as itself
func TestWardenListedAs(t *testing.T) {
	bikes := map[string]any{"type": "item", "fields": map[string]any{"state": "repair"}}
	lamp := map[string]any{"type": "item", "fields": map[string]any{"state": "missing"}}
	ok := map[string]any{"type": "item", "fields": map[string]any{"state": "ok"}}
	for _, c := range []struct {
		d    map[string]any
		typ  string
		want bool
	}{
		{bikes, "item", true}, {bikes, "repair", true}, {bikes, "need", false},
		{lamp, "need", true}, {lamp, "repair", false},
		{ok, "item", true}, {ok, "repair", false},
		{map[string]any{"title": "x"}, "event", true},
	} {
		if got := wardenListedAs(c.d, c.typ); got != c.want {
			t.Fatalf("%v as %s: %v", c.d, c.typ, got)
		}
	}
}

// every common field the page's form shows is named on every type that
// list_entry_types answers, so an MCP client reading one type finds them
func TestWardenFormFieldsListed(t *testing.T) {
	src, err := os.ReadFile("../apps/spaces/src/SpacesApp.rgr")
	if err != nil {
		t.Fatal(err)
	}
	i := regexp.MustCompile(`sfn commonField:string`).FindIndex(src)
	if i == nil {
		t.Fatal("SpacesApp.rgr has no commonField")
	}
	body := src[i[1]:]
	body = body[:regexp.MustCompile(`\n    sfn `).FindIndex(body)[0]]
	form := []string{}
	for _, m := range regexp.MustCompile(`return "(\w+)"`).FindAllSubmatch(body, -1) {
		form = append(form, string(m[1]))
	}
	if len(form) < 5 {
		t.Fatalf("read %v from commonField", form)
	}
	common := map[string]bool{}
	for _, f := range wardenCommon {
		common[f.Key] = true
	}
	for _, k := range wardenFormCommon {
		if !common[k] {
			t.Fatalf("%s is not a common field", k)
		}
	}
	for _, ty := range wardenTypesFor("family") {
		for _, k := range form {
			if !slices.Contains(ty.Also, k) {
				t.Fatalf("%s: the form's %s is not in its also_fields", ty.ID, k)
			}
		}
	}
}

func TestWardenWeekTimes(t *testing.T) {
	for in, want := range map[string]string{
		"ma 8.15-13, ti 9-14.15":                 "MO 08:15-13:00; TU 09:00-14:15",
		"MO,WE 08:00-12:00; TU 9:00–14:00":       "MO 08:00-12:00; TU 09:00-14:00; WE 08:00-12:00",
		"ma-ke 8-12; ke 9-13":                    "",
		"pe klo 8.00–12.00\nma–to 8:15 - 14:00":  "MO 08:15-14:00; TU 08:15-14:00; WE 08:15-14:00; TH 08:15-14:00; FR 08:00-12:00",
		"Monday 8:15-13:00, Tuesday 9:00-14:15":  "MO 08:15-13:00; TU 09:00-14:15",
		"ke 10-9":                                "",
		"ma 8-12, ma 9-13":                       "",
		"ma, ke":                                 "",
		"maanantaisin 8-12; keskiviikko 8.30-14": "MO 08:00-12:00; WE 08:30-14:00",
	} {
		w, err := wardenWeekTimes(in)
		got := ""
		if err == nil {
			got = w.String()
		}
		eq(t, got, want, in)
	}
}

func TestWardenSchoolTimetable(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)

	// the weekdays' times make the rule; the first day's times are its own
	code, out := wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "school_timetable", "title": "Koulu", "date": "2026-08-12",
		"ward": "Leo", "rrule": "FREQ=WEEKLY;UNTIL=20270531", "fields": map[string]any{"times": "ma 8.15-13, ti 9-14.15, ke-pe 8:15-14"}})
	eq(t, code, 200, "timetable: "+wardenErrOf(out))
	eq(t, []any{out["rrule"], out["start"], out["end"], out["fields"].(map[string]any)["times"]},
		[]any{"FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;UNTIL=20270531", "08:15", "14:00", "MO 08:15-13:00; TU 09:00-14:15; WE 08:15-14:00; TH 08:15-14:00; FR 08:15-14:00"}, "a Wednesday's times")
	code, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "school_timetable", "title": "Koulu", "date": "2026-08-12", "ward": "Leo"})
	eq(t, []any{code, out["error"]}, []any{400, "a school_timetable needs fields.times"})
	code, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "school_timetable", "title": "Koulu", "date": "2026-08-12", "ward": "Leo", "fields": map[string]any{"times": "ma 8-12, xx 9-13"}})
	eq(t, code, 400, "a weekday it does not know: "+wardenErrOf(out))
}
