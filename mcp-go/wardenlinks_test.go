// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "testing"

func TestWardenLinks(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Demo"})
	sid := out["space_id"].(string)
	links := func(id string) []any {
		_, out := wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
		for _, e := range out["events"].([]any) {
			if m := e.(map[string]any); m["event_id"] == id {
				l, _ := m["links"].([]any)
				return l
			}
		}
		t.Fatalf("no %s", id)
		return nil
	}

	// a registration and its gear in one import, linked by ext_id
	code, out := wardenCall(t, base, "tero", "import_events", map[string]any{"space_id": sid, "events": []any{
		map[string]any{"title": "Luistelu 2027", "type": "registration", "date": "2026-11-30", "ext_id": "reg"},
		map[string]any{"title": "Luistimet", "type": "need", "ext_id": "skates", "links": []any{"reg"}},
	}})
	eq(t, code, 200, "imported")
	reg, skates := out["event_ids"].([]any)[0].(string), out["event_ids"].([]any)[1].(string)
	eq(t, links(reg), []any{skates}, "the registration knows its gear")
	eq(t, links(skates), []any{reg}, "and the gear its registration")

	// a thing, its repair and the repairer: links by event_id
	_, bike := wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "Pyörät", "type": "item"})
	bid := bike["event_id"].(string)
	_, rep := wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "Pyörien korjaus", "type": "repair", "date": "2027-04-01", "fields": map[string]any{"item": "Pyörät"}, "links": []any{bid}})
	rid := rep["event_id"].(string)
	eq(t, rep["links"], []any{bid}, "the answer has its links")
	eq(t, links(bid), []any{rid})

	// a change without links keeps them
	wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "event_id": bid, "notes": "Kevät"})
	eq(t, links(bid), []any{rid}, "kept")

	// unlink takes both ends off
	wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "event_id": rid, "unlink": []any{bid}})
	eq(t, len(links(bid)), 0, "unlinked")
	eq(t, len(links(rid)), 0, "both ways")

	// an unknown entry is refused
	code, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "event_id": rid, "links": []any{"nope"}})
	eq(t, []any{code, out["error"]}, []any{400, `links: no entry "nope" in this space`})

	// deleting one takes it off the others
	wardenCall(t, base, "tero", "delete_event", map[string]any{"space_id": sid, "event_id": skates})
	eq(t, len(links(reg)), 0, "the deleted is off")
}
