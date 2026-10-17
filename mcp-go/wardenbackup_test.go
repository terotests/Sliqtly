// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"testing"
	"time"
)

// A space emptied through the MCP server, and then deleted, comes back from
// its copies on the page; no assistant can list or restore them.
func TestWardenBackups(t *testing.T) {
	env, base, stop := editorServer(t)
	defer stop()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	env.warden.now = func() time.Time { return now }
	ctx := context.Background()
	who := wardenWho{uid: "u-tero", name: "Tero"}

	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)
	wardenCall(t, base, "tero", "invite_member", map[string]any{"space_id": sid, "email": "anna@example.com"})
	wardenCall(t, base, "anna", "list_spaces", nil)
	ids := []string{}
	for _, title := range []string{"Uinti", "Hammaslääkäri", "Synttärit"} {
		_, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": title, "date": "2026-10-20", "cost": map[string]any{"amount": 12.5}})
		ids = append(ids, out["event_id"].(string))
	}
	_, out = wardenCall(t, base, "tero", "list_backups", map[string]any{"space_id": sid})
	eq(t, len(out["backups"].([]any)), 1, "one copy so far: the empty space, before its first change")

	// three hours later an assistant removes everything
	now = now.Add(3 * time.Hour)
	for _, id := range ids {
		_, err := env.warden.call(ctx, who, "delete_event", map[string]any{"space_id": sid, "event_id": id}, false)
		eq(t, err, nil)
	}
	_, err := env.warden.call(ctx, who, "list_backups", map[string]any{"space_id": sid}, false)
	eq(t, err, roomErr{"no such operation: list_backups"}, "not a tool")
	_, err = env.warden.call(ctx, who, "restore_backup", map[string]any{"backup_id": sid + ".1"}, false)
	eq(t, err, roomErr{"no such operation: restore_backup"}, "not a tool")

	_, out = wardenCall(t, base, "tero", "list_backups", map[string]any{"space_id": sid})
	l := out["backups"].([]any)
	eq(t, len(l), 2, "a copy before the first delete")
	newest := l[0].(map[string]any)
	eq(t, []any{newest["events"], newest["reason"]}, []any{float64(3), "delete_event"})
	code, out := wardenCall(t, base, "anna", "list_backups", map[string]any{"space_id": sid})
	eq(t, []any{code, out["error"]}, []any{400, "only the space's owner can do that"}, "a member does not")
	code, _ = wardenCall(t, base, "anna", "restore_backup", map[string]any{"backup_id": newest["backup_id"]})
	eq(t, code, 400, "nor restores")

	// the assistant deletes the space too: a copy is taken first
	now = now.Add(time.Minute)
	_, err = env.warden.call(ctx, who, "delete_space", map[string]any{"space_id": sid, "confirm_name": "Koti"}, false)
	eq(t, err, nil)
	_, out = wardenCall(t, base, "tero", "list_backups", nil)
	gone := out["deleted_spaces"].([]any)
	eq(t, len(gone), 1, "the deleted space is listed")
	g := gone[0].(map[string]any)
	eq(t, []any{g["space_id"], g["name"], len(g["backups"].([]any))}, []any{sid, "Koti", 3})

	code, out = wardenCall(t, base, "tero", "restore_backup", map[string]any{"backup_id": newest["backup_id"]})
	eq(t, []any{code, out["events"]}, []any{200, float64(3)}, "restored")
	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	evs := out["events"].([]any)
	eq(t, len(evs), 3, "the events are back")
	eq(t, out["space"].(map[string]any)["invites"], []any{"anna@example.com"}, "anna is invited again")
	_, out = wardenCall(t, base, "anna", "list_spaces", nil)
	eq(t, len(out["spaces"].([]any)), 1, "and takes the invite")

	// restoring copies what is there first, so it can be undone
	_, out = wardenCall(t, base, "tero", "list_backups", map[string]any{"space_id": sid})
	l = out["backups"].([]any)
	empty := l[0].(map[string]any)
	eq(t, []any{empty["reason"], empty["events"]}, []any{"delete_space", float64(0)})
	code, _ = wardenCall(t, base, "tero", "restore_backup", map[string]any{"backup_id": empty["backup_id"]})
	eq(t, code, 200)
	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	eq(t, []any{len(out["events"].([]any)), len(out["space"].(map[string]any)["members"].([]any))}, []any{0, 2}, "empty again, anna stays")
	_, out = wardenCall(t, base, "tero", "list_backups", map[string]any{"space_id": sid})
	b := out["backups"].([]any)[0].(map[string]any)
	eq(t, []any{b["reason"], b["events"]}, []any{"before_restore", float64(3)})
}

func TestWardenOwnBackups(t *testing.T) {
	env, base, stop := editorServer(t)
	defer stop()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	env.warden.now = func() time.Time { return now }
	_, out := wardenCall(t, base, "tero", "save_own_item", map[string]any{"kind": "regular_expense", "title": "Vuokra", "amount": 900})
	eq(t, out["error"], nil)
	now = now.Add(3 * time.Hour)
	_, out = wardenCall(t, base, "tero", "own_budget", nil)
	items := out["items"].([]any)
	id := items[0].(map[string]any)["item_id"]
	wardenCall(t, base, "tero", "delete_own_item", map[string]any{"item_id": id})
	_, out = wardenCall(t, base, "tero", "list_backups", nil)
	own := out["own"].([]any)
	eq(t, len(own), 1)
	code, out := wardenCall(t, base, "anna", "restore_backup", map[string]any{"backup_id": own[0].(map[string]any)["backup_id"]})
	eq(t, code, 400, "not anna's")
	code, out = wardenCall(t, base, "tero", "restore_backup", map[string]any{"backup_id": own[0].(map[string]any)["backup_id"]})
	eq(t, []any{code, out["items"]}, []any{200, float64(1)})
}

func TestWardenBackupKeep(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	var list []map[string]any
	// one every 6 hours for 400 days
	for h := 0; h < 400*24; h += 6 {
		list = append(list, map[string]any{"at": now.Add(-time.Duration(h) * time.Hour).UnixMilli()})
	}
	keep, drop := wardenBackupKeep(list, now, time.UTC)
	eq(t, len(keep)+len(drop), len(list))
	// a week of 4 a day, 53 days of one, about 10 months of one
	if len(keep) < 28+53+9 || len(keep) > 29+54+12 {
		t.Fatalf("kept %d", len(keep))
	}
	for _, b := range keep {
		if now.Sub(time.UnixMilli(backupInt(b, "at"))) > 366*24*time.Hour {
			t.Fatal("kept one older than a year")
		}
	}
}
