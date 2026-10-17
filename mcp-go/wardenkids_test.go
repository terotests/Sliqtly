// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The children's events watch: a search for events near home in the next
// month, kept apart from the calendar until one is added to it.
func TestWardenKids(t *testing.T) {
	e, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)

	_, out = wardenCall(t, base, "tero", "list_event_finds", map[string]any{"space_id": sid})
	w := out["watch"].(map[string]any)
	eq(t, []any{w["active"], w["ages"], w["area"]}, []any{false, []any{"small", "school"}, "Tampere"}, "none yet")
	code, _ := wardenCall(t, base, "tero", "set_event_watch", map[string]any{"space_id": sid, "ages": []any{"toddler"}})
	eq(t, code, 400, "no such age group")
	code, out = wardenCall(t, base, "tero", "set_event_watch", map[string]any{"space_id": sid, "interests": "nukketeatteri"})
	eq(t, []any{code, out["watch"].(map[string]any)["active"], out["watch"].(map[string]any)["every_days"]}, []any{200, true, 7.0})

	lines := []string{
		"Nukketeatteri | 2026-10-24 | 12:00 | Tahmelan Huvila, Tampere | ilmainen | 3-8 | 90 | puppet theatre",
		"Mennyt näyttely | 2026-10-01 | ? | Rulla | ? | ? | 80 | past",
		"Ei päivää | ? | ? | Metso | ? | ? | 70 | no day",
		"Karnevaali | 2026-10-20 | 13:00 | Särkänniemi | 18 € | ? | 40 | scary for small ones",
	}
	asked := ""
	redirects := map[string]string{}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if to, ok := redirects[r.URL.Path]; ok {
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
		var ask struct {
			Contents []struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"contents"`
		}
		json.NewDecoder(r.Body).Decode(&ask)
		asked = ask.Contents[0].Parts[0].Text
		chunks, supports := []any{}, []any{}
		for i, l := range lines {
			path := fmt.Sprintf("/grounding-api-redirect/k%d", i)
			redirects[path] = fmt.Sprintf("https://tapahtumat.tampere.fi/page/%d", i)
			chunks = append(chunks, map[string]any{"web": map[string]any{"uri": srv.URL + path}})
			supports = append(supports, map[string]any{"segment": map[string]any{"text": l}, "groundingChunkIndices": []int{i}})
		}
		json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{
			"content":           map[string]any{"role": "model", "parts": []any{map[string]any{"text": strings.Join(lines, "\n")}}},
			"groundingMetadata": map[string]any{"groundingChunks": chunks, "groundingSupports": supports},
		}}})
	}))
	defer srv.Close()
	e.warden.search = newWardenSearch(&geminiModel{http: srv.Client(), model: "g", key: "k", base: srv.URL, tools: aiEditTools})
	e.warden.now = func() time.Time { return time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC) }

	code, out = wardenCall(t, base, "tero", "run_event_watch", map[string]any{"space_id": sid})
	eq(t, code, 200, "run")
	for _, want := range []string{"in and near Tampere, Finland, between 2026-10-17 and 2026-11-16", "small children (3-6 years), school children (7-12 years)", "They like: nukketeatteri"} {
		if !strings.Contains(asked, want) {
			t.Fatalf("asked %q, want %q", asked, want)
		}
	}
	eq(t, len(out["new"].([]any)), 2, "the puppet show and the carnival")
	finds := out["watch"].(map[string]any)["finds"].([]any)
	f := finds[0].(map[string]any)
	eq(t, []any{f["title"], f["date"], f["start"], f["score"]}, []any{"Karnevaali", "2026-10-20", "13:00", 40.0}, "soonest first")
	f = finds[1].(map[string]any)
	eq(t, []any{f["title"], f["place"], f["price"], f["age"], f["url"]}, []any{"Nukketeatteri", "Tahmelan Huvila, Tampere", "ilmainen", "3-8", "https://tapahtumat.tampere.fi/page/0"})
	run := out["watch"].(map[string]any)["runs"].([]any)[0].(map[string]any)
	eq(t, []any{run["results"], run["new"], run["left_out"]}, []any{4.0, 2.0, map[string]any{"no_day": 2.0, "low_fit": 1.0}})
	eq(t, []any{run["near"], run["local"]}, []any{"Tampere", "2026-10-17 12:00"}, "where and when")

	// not in the calendar until added
	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	eq(t, len(out["events"].([]any)), 0, "kept apart")
	code, out = wardenCall(t, base, "tero", "add_event_find", map[string]any{"space_id": sid, "find_id": f["id"]})
	eq(t, code, 200, "added")
	ev := out["event"].(map[string]any)
	eq(t, []any{ev["title"], ev["date"], ev["start"], ev["place"], ev["source"]}, []any{"Nukketeatteri", "2026-10-24", "12:00", "Tahmelan Huvila, Tampere", "Tapahtumavahti"})
	eq(t, out["find"].(map[string]any)["added"], ev["event_id"])
	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	eq(t, len(out["events"].([]any)), 1, "in the calendar")

	// hidden: told to the next search
	kid := finds[0].(map[string]any)["id"]
	code, out = wardenCall(t, base, "tero", "rate_find", map[string]any{"space_id": sid, "find_id": kid, "rating": "hide", "reason": "not_interesting"})
	eq(t, []any{code, out["find"].(map[string]any)["rating"]}, []any{200, "hide"})
	e.warden.now = func() time.Time { return time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC) }
	_, out = wardenCall(t, base, "tero", "run_event_watch", map[string]any{"space_id": sid})
	if !strings.Contains(asked, "- Karnevaali (not interesting)") {
		t.Fatalf("asked %q", asked)
	}
	eq(t, len(out["new"].([]any)), 0, "seen")
	eq(t, len(out["watch"].(map[string]any)["finds"].([]any)), 1, "the carnival is past")

	// the daily run takes it when due
	e.warden.cronAuth = func(_ context.Context, tok string) bool { return tok == "secret" }
	cron := func() map[string]any {
		req, _ := http.NewRequest("POST", base+"/apps/warden/api/run_watches", nil)
		req.Header.Set("Authorization", "Bearer secret")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		o := map[string]any{}
		json.NewDecoder(res.Body).Decode(&o)
		return o
	}
	eq(t, cron()["ran"], 0.0, "ran just now")
	e.warden.now = func() time.Time { return time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC) }
	eq(t, cron()["ran"], 1.0, "a week later")
	code, _ = wardenCall(t, base, "anna", "list_event_finds", map[string]any{"space_id": sid})
	eq(t, code, 400, "anna is not a member")
}
