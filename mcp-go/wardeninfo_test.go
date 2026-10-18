// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The info screen is a Sliqtly presentation of the person's own: made, made
// again in place by the one who made it, a new one by another member; the
// morning setting is kept on the space.
func TestWardenInfoScreen(t *testing.T) {
	e, base, stop := editorServer(t)
	defer stop()
	e.layouts = newLayoutService(e.DB)
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)
	_, out = wardenCall(t, base, "tero", "invite_member", map[string]any{"space_id": sid, "email": "anna@example.com"})
	wardenCall(t, base, "anna", "list_spaces", nil)

	_, out = wardenCall(t, base, "tero", "info_screen", map[string]any{"space_id": sid})
	eq(t, []any{out["url"], out["morning"]}, []any{"", false}, "none yet")

	if e.warden.deck == nil {
		t.Fatal("no presentations for the info screen")
	}
	type call struct{ uid, op, deck string }
	var calls []call
	real := e.warden.deck
	e.warden.deck = func(ctx context.Context, uid, op string, a map[string]any) (map[string]any, error) {
		calls = append(calls, call{uid, op, argStr(a, "deck_id")})
		return real(ctx, uid, op, a)
	}
	md := "# Koti\n\n## Tänään\n\n- 08:15 Koulu · Leo\n"
	code, out := wardenCall(t, base, "tero", "info_screen", map[string]any{"space_id": sid, "markdown": md})
	eq(t, code, 200, "made: %v", out)
	url := out["url"].(string)
	eq(t, strings.Contains(url, "/s/"), true, "a viewer link: "+url)
	eq(t, out["made_by"], "Tero", "by")

	code, out = wardenCall(t, base, "tero", "info_screen", map[string]any{"space_id": sid, "markdown": md + "\n## Muista\n\n- Lasku\n"})
	eq(t, []any{code, out["url"]}, []any{200, url}, "made again, the same link")

	code, out = wardenCall(t, base, "anna", "info_screen", map[string]any{"space_id": sid, "markdown": md})
	eq(t, code, 200, "anna's")
	eq(t, out["url"] != url, true, "a deck of anna's own")
	eq(t, len(calls), 3, "three calls")
	eq(t, []any{calls[0].op, calls[1].op, calls[1].deck != "", calls[2].op, calls[2].uid}, []any{"create_presentation", "update_presentation", true, "create_presentation", "u-anna"})

	code, out = wardenCall(t, base, "tero", "info_screen", map[string]any{"space_id": sid, "markdown": "no title"})
	eq(t, []any{code, wardenErrOf(out)}, []any{400, "markdown starts with the \"# \" title slide"}, "not a deck")

	_, out = wardenCall(t, base, "anna", "info_screen", map[string]any{"space_id": sid, "morning": true})
	eq(t, out["morning"], true, "morning on")
	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	info := out["space"].(map[string]any)["info_screen"].(map[string]any)
	eq(t, []any{info["morning"], info["url"] != ""}, []any{true, true}, "the space says it")
	d, _ := e.DB.Get(context.Background(), wardenSpaces, sid)
	eq(t, []any{d["info_morning"], d["info_screen"].(map[string]any)["morning_by"]}, []any{true, "u-anna"}, "kept")

	code, _ = wardenCall(t, base, "mallory", "info_screen", map[string]any{"space_id": sid})
	eq(t, code != 200, true, "not mallory's space")
}

// In the morning the assistant makes the info screen of each space with
// morning on, as the member who turned it on.
func TestWardenMorning(t *testing.T) {
	e, base, stop := editorServer(t)
	defer stop()
	e.layouts = newLayoutService(e.DB)
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)
	wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Muu"})
	wardenCall(t, base, "tero", "info_screen", map[string]any{"space_id": sid, "morning": true})

	var asks []map[string]any
	replies := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"info_screen","args":{"markdown":"# Koti\n\n## Tänään\n\n- Ei menoja\n"}}}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Info-näyttö tehty."}]}}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		json.NewDecoder(r.Body).Decode(&got)
		asks = append(asks, got)
		io.WriteString(w, replies[len(asks)-1])
	}))
	defer srv.Close()
	e.warden.now = func() time.Time { return time.Date(2026, 10, 18, 3, 0, 0, 0, time.UTC) }
	e.warden.ai = newWardenAssist(&geminiModel{http: srv.Client(), model: "g", key: "k", base: srv.URL, tools: aiEditTools})
	made, failed := e.warden.morning(context.Background())
	eq(t, []any{made, failed, len(asks)}, []any{1, 0, 2}, "one space made")
	raw, _ := json.Marshal(asks[0])
	eq(t, strings.Contains(string(raw), "Make the space's info screen now"), true, "asked for it")
	_, out = wardenCall(t, base, "tero", "info_screen", map[string]any{"space_id": sid})
	eq(t, []any{out["url"] != "", out["made"], out["morning"]}, []any{true, "2026-10-18T06:00", true}, "made in the morning")
}
