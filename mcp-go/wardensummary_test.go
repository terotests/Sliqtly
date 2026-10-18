// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "testing"

func TestWardenEmailSummary(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti"})
	sid := out["space_id"].(string)
	_, out = wardenCall(t, base, "tero", "email_summary", map[string]any{"space_id": sid})
	eq(t, []any{out["period"], out["sending"]}, []any{"off", false}, "off at first")
	_, out = wardenCall(t, base, "tero", "email_summary", map[string]any{"space_id": sid, "period": "week"})
	eq(t, out["period"], "week", "set")
	_, out = wardenCall(t, base, "tero", "email_summary", map[string]any{"space_id": sid})
	eq(t, out["period"], "week", "kept")
	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	eq(t, out["space"].(map[string]any)["email_summary"], "week", "the space says it")
	code, out := wardenCall(t, base, "tero", "email_summary", map[string]any{"space_id": sid, "period": "hourly"})
	eq(t, []any{code, wardenErrOf(out)}, []any{400, "period is off, day or week"}, "bad period")
	code, _ = wardenCall(t, base, "mallory", "email_summary", map[string]any{"space_id": sid, "period": "day"})
	eq(t, code != 200, true, "not mallory's space")
	_, out = wardenCall(t, base, "tero", "email_summary", map[string]any{"space_id": sid, "period": "off"})
	eq(t, out["period"], "off", "off again")
}
