// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"testing"
	"time"
)

// What an assistant deletes is kept: list_deleted lists it and restore_event
// puts it back, linked again; one deleted before that is found in a backup.
func TestWardenDeleted(t *testing.T) {
	env, base, stop := editorServer(t)
	defer stop()
	now := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	env.warden.now = func() time.Time { return now }
	ctx := context.Background()
	who := wardenWho{uid: "u-tero", name: "Tero"}
	call := func(op string, a map[string]any) (map[string]any, error) {
		t.Helper()
		out, err := env.warden.call(ctx, who, op, a, false)
		m, _ := out.(map[string]any)
		return m, err
	}

	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)
	_, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "Vanha jumppa", "date": "2026-09-02", "start": "17:45", "rrule": "FREQ=WEEKLY;BYDAY=WE;UNTIL=20261209"})
	old := out["event_id"].(string)
	_, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "Jumppa", "date": "2026-10-28"})
	gym := out["event_id"].(string)
	_, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "Jumppakassi", "type": "need", "links": []any{gym}})
	bag := out["event_id"].(string)

	// a copy is taken three hours later; then "Vanha jumppa" goes as it went
	// before deletions were kept
	now = now.Add(3 * time.Hour)
	wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "event_id": gym, "place": "Koulu"})
	eq(t, env.warden.db.Delete(ctx, wardenEvents, old), nil)

	now = now.Add(time.Minute)
	_, err := call("delete_event", map[string]any{"space_id": sid, "event_id": gym})
	eq(t, err, nil)
	out, err = call("list_events", map[string]any{"space_id": sid})
	eq(t, err, nil)
	for _, e := range out["events"].([]any) {
		if e.(map[string]any)["event_id"] == gym {
			t.Fatal("still in the calendar")
		}
	}
	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	for _, e := range out["events"].([]any) {
		eq(t, e.(map[string]any)["links"], nil, "the bag no longer links the gym")
	}

	out, err = call("list_deleted", map[string]any{"space_id": sid})
	eq(t, err, nil)
	l := out["deleted"].([]any)
	eq(t, len(l), 2)
	a, b := l[0].(map[string]any), l[1].(map[string]any)
	eq(t, []any{a["event_id"], a["title"], a["place"], a["deleted"], a["deleted_by"]}, []any{gym, "Jumppa", "Koulu", "2026-10-21 16:01", "Tero"})
	eq(t, []any{b["event_id"], b["rrule"], b["deleted"]}, []any{old, "FREQ=WEEKLY;BYDAY=WE;UNTIL=20261209", "before 2026-10-21 16:00"}, "found in the backup")

	out, err = call("restore_event", map[string]any{"space_id": sid, "event_id": gym})
	eq(t, err, nil)
	eq(t, []any{out["title"], out["restored"], out["links"]}, []any{"Jumppa", true, []any{bag}})
	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	for _, e := range out["events"].([]any) {
		if e.(map[string]any)["event_id"] == bag {
			eq(t, e.(map[string]any)["links"], []any{gym}, "linked again")
		}
	}
	_, err = call("restore_event", map[string]any{"space_id": sid, "event_id": gym})
	eq(t, err, roomErr{"no such deleted entry in this space; list_deleted lists them"}, "it is back already")
	_, err = call("restore_event", map[string]any{"space_id": sid, "event_id": old})
	eq(t, err, nil)
	out, _ = call("list_deleted", map[string]any{"space_id": sid})
	eq(t, len(out["deleted"].([]any)), 0, "nothing deleted now")

	// another space's member sees none of them
	_, out = wardenCall(t, base, "anna", "create_space", map[string]any{"name": "Annan"})
	_, err = env.warden.call(ctx, wardenWho{uid: "u-anna", name: "Anna"}, "restore_event", map[string]any{"space_id": out["space_id"], "event_id": gym}, false)
	eq(t, err, roomErr{"no such deleted entry in this space; list_deleted lists them"})
}
