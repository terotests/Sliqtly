// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWardenPrice(t *testing.T) {
	for in, want := range map[string]float64{
		"Lasten sänky 90x200, 120 €":    120,
		"Hinta 1 200€ tai tarjous":      1200,
		"€ 35,50 nouto Espoosta":        35.5,
		"Matto 40 euroa":                40,
		"Ei hintaa, 3 kpl":              0,
		"Pyörä 24\" – 85 EUR, käytetty": 85,
	} {
		eq(t, wardenPrice(in), want, in)
	}
}

// A thing still to get is watched: the searches go to each source with
// site:, results that are not offers, too dear, excluded or seen before are
// left out, the new ones are kept on the entry and put in a reminder.
func TestWardenWatch(t *testing.T) {
	e, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)
	_, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "Lasten sängyt", "type": "item", "fields": map[string]any{"state": "missing"}})
	id := out["event_id"].(string)

	e.warden.search = nil
	code, out := wardenCall(t, base, "tero", "set_watch", map[string]any{"space_id": sid, "event_id": id, "max_price": 150.0, "exclude": []any{"Ostetaan"}})
	eq(t, code, 200, "set without a key")
	w := out["watch"].(map[string]any)
	eq(t, []any{w["query"], w["sources"], w["max_price"], w["every_days"], w["active"], w["exclude"]}, []any{"Lasten sängyt", []any{"tori", "vinted", "huuto"}, 150.0, 1.0, true, []any{"ostetaan"}})
	if !strings.Contains(out["note"].(string), "no search API key") {
		t.Fatalf("note: %v", out["note"])
	}
	code, out = wardenCall(t, base, "tero", "run_watch", map[string]any{"space_id": sid, "event_id": id})
	eq(t, []any{code, out["error"]}, []any{400, "This server has no search API key yet (BRAVE_SEARCH_API_KEY)."})
	code, _ = wardenCall(t, base, "tero", "set_watch", map[string]any{"space_id": sid, "event_id": id, "sources": []any{"ebay"}})
	eq(t, code, 400, "no such source")

	var asked []string
	results := map[string][]map[string]string{
		"Lasten sängyt site:tori.fi": {
			{"title": "Lasten <strong>sänky</strong> 90x200", "url": "https://www.tori.fi/recommerce/forsale/item/111", "description": "Hyvä kunto, 80 €"},
			{"title": "Kerrossänky", "url": "https://www.tori.fi/recommerce/forsale/item/222", "description": "Hinta 300 €"},
			{"title": "Ostetaan lasten sänky", "url": "https://www.tori.fi/recommerce/forsale/item/333", "description": ""},
			{"title": "Sängyt – Tori", "url": "https://www.tori.fi/recommerce/forsale/search?q=s%C3%A4nky", "description": "Hakutulokset"},
			{"title": "Muualla", "url": "https://example.com/sanky", "description": "50 €"},
		},
		"Lasten sängyt site:vinted.fi": {
			{"title": "Pinnasänky", "url": "https://www.vinted.fi/items/444-pinnasanky", "description": "Hinta ei tiedossa"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") != "k" {
			w.WriteHeader(401)
			return
		}
		q := r.URL.Query().Get("q")
		asked = append(asked, q)
		res := []any{}
		for _, h := range results[q] {
			res = append(res, h)
		}
		json.NewEncoder(w).Encode(map[string]any{"web": map[string]any{"results": res}})
	}))
	defer srv.Close()
	e.warden.search = &braveSearch{key: "k", base: srv.URL, http: srv.Client()}
	e.warden.now = func() time.Time { return time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC) }

	code, out = wardenCall(t, base, "tero", "run_watch", map[string]any{"space_id": sid, "event_id": id})
	eq(t, code, 200, "run")
	eq(t, asked, []string{"Lasten sängyt site:tori.fi", "Lasten sängyt site:vinted.fi", "Lasten sängyt site:huuto.net"})
	fresh := out["new"].([]any)
	eq(t, len(fresh), 2, "the 80 € bed and the cot without a price")
	f := fresh[0].(map[string]any)
	eq(t, []any{f["title"], f["price"], f["source"], f["found"]}, []any{"Lasten sänky 90x200", 80.0, "Tori", "2026-10-17"})

	// on the entry, and a reminder of them
	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	var item, rem map[string]any
	for _, ev := range out["events"].([]any) {
		m := ev.(map[string]any)
		switch m["type"] {
		case "item":
			item = m
		case "reminder":
			rem = m
		}
	}
	eq(t, len(item["watch"].(map[string]any)["finds"].([]any)), 2, "finds on the entry")
	if _, ok := item["watch"].(map[string]any)["seen"]; ok {
		t.Fatal("seen is the server's own")
	}
	eq(t, []any{rem["title"], rem["date"], rem["source"]}, []any{"Tarjouksia: Lasten sängyt (2 uutta)", "2026-10-17", "Tarjousvahti"})
	if !strings.Contains(rem["notes"].(string), "https://www.tori.fi/recommerce/forsale/item/111") {
		t.Fatalf("notes: %v", rem["notes"])
	}

	// again: nothing new, the reminder stays as it was
	_, out = wardenCall(t, base, "tero", "run_watch", map[string]any{"space_id": sid, "event_id": id})
	eq(t, len(out["new"].([]any)), 0, "seen already")

	// the scheduled run: a wrong token, then not due yet, then due
	t.Setenv("WARDEN_CRON_TOKEN", "secret")
	run := func(tok string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", base+"/apps/warden/api/run_watches", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		o := map[string]any{}
		json.NewDecoder(res.Body).Decode(&o)
		return res.StatusCode, o
	}
	code, _ = run("wrong")
	eq(t, code, 403, "wrong token")
	code, out = run("secret")
	eq(t, []any{code, out["ran"], out["more"]}, []any{200, 0.0, false}, "ran an hour ago")
	results["Lasten sängyt site:huuto.net"] = []map[string]string{{"title": "Sänky", "url": "https://www.huuto.net/kohteet/sanky/555", "description": "Lähtöhinta 10 €"}}
	e.warden.now = func() time.Time { return time.Date(2026, 10, 18, 8, 0, 0, 0, time.UTC) }
	code, out = run("secret")
	eq(t, []any{code, out["ran"], out["found"]}, []any{200, 1.0, 1.0}, "a day later")

	// got: no longer watched
	wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "event_id": id, "fields": map[string]any{"state": "ok"}})
	e.warden.now = func() time.Time { return time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC) }
	_, out = run("secret")
	eq(t, out["ran"], 0.0, "got it")
	_, out = wardenCall(t, base, "tero", "list_watches", map[string]any{"space_id": sid})
	l := out["watches"].([]any)
	eq(t, len(l), 1)
	lw := l[0].(map[string]any)["watch"].(map[string]any)
	eq(t, []any{lw["active"], len(lw["finds"].([]any))}, []any{false, 3})

	// another member's space is not reachable
	code, _ = wardenCall(t, base, "anna", "set_watch", map[string]any{"space_id": sid, "event_id": id})
	eq(t, code, 400, "anna is not a member")
}
