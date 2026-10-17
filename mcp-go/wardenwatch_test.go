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
	eq(t, code, 200, "set without a search")
	w := out["watch"].(map[string]any)
	eq(t, []any{w["query"], w["sources"], w["max_price"], w["every_days"], w["active"], w["exclude"]}, []any{"Lasten sängyt", []any{"tori", "vinted", "huuto"}, 150.0, 1.0, true, []any{"ostetaan"}})
	if !strings.Contains(out["note"].(string), "has no search") {
		t.Fatalf("note: %v", out["note"])
	}
	code, out = wardenCall(t, base, "tero", "run_watch", map[string]any{"space_id": sid, "event_id": id})
	eq(t, []any{code, out["error"]}, []any{400, "This server has no search (its assistant's model is not Gemini)."})
	code, _ = wardenCall(t, base, "tero", "set_watch", map[string]any{"space_id": sid, "event_id": id, "sources": []any{"ebay"}})
	eq(t, code, 400, "no such source")

	// Gemini with Google Search: a line per listing, each tied to a result
	// whose redirect leads to the listing
	type listing struct{ title, price, url, site, town, fit string }
	results := map[string][]listing{
		"Lasten sängyt site:tori.fi": {
			{"Kerrossänky", "300", "https://www.tori.fi/recommerce/forsale/item/222", "tori.fi", "Tampere", "90 | bunk bed"},
			{"Ostetaan lasten sänky", "?", "https://www.tori.fi/recommerce/forsale/item/333", "tori.fi", "Tampere", "10 | wanted ad"},
			{"Sängyt – Tori", "?", "https://www.tori.fi/recommerce/forsale/search?q=s%C3%A4nky", "tori.fi", "Tampere", "?"},
			{"Muualla", "50", "https://example.com/sanky", "example.com", "?", "?"},
			{"Nukensänky", "5 €", "https://www.tori.fi/recommerce/forsale/item/666", "tori.fi", "Nokia", "20 | a doll's bed"},
			{"Lasten sänky 90x200", "80 €", "https://www.tori.fi/recommerce/forsale/item/111", "tori.fi", "Tampere", "85 | a child's bed, 90x200"},
		},
		"Lasten sängyt site:vinted.fi": {
			{"Pinnasänky", "?", "https://www.vinted.fi/items/444-pinnasanky", "vinted.fi", "?", ""},
		},
	}
	var asked []string
	intent, avoid := "Lasten sängyt", ""
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
			Tools []map[string]any `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&ask)
		if r.Header.Get("x-goog-api-key") != "k" || len(ask.Tools) != 1 || ask.Tools[0]["google_search"] == nil {
			w.WriteHeader(400)
			return
		}
		text := ask.Contents[0].Parts[0].Text
		q, _, _ := strings.Cut(strings.TrimPrefix(text, "Search for current second-hand or for-sale listings matching: "), "\n")
		asked = append(asked, q)
		if !strings.Contains(text, "The buyer lives in Tampere, Finland") || !strings.Contains(text, "What the buyer wants: "+intent) || !strings.Contains(text, avoid) {
			w.WriteHeader(400)
			return
		}
		chunks, supports, lines := []any{}, []any{}, []string{}
		for i, l := range results[q] {
			path := fmt.Sprintf("/grounding-api-redirect/%d-%d", len(asked), i)
			redirects[path] = l.url
			chunks = append(chunks, map[string]any{"web": map[string]any{"uri": srv.URL + path, "title": l.site}})
			line := l.title + " | " + l.price + " | " + l.town
			if l.fit != "" {
				line += " | " + l.fit
			}
			lines = append(lines, line)
			supports = append(supports, map[string]any{"segment": map[string]any{"text": line}, "groundingChunkIndices": []int{i}})
		}
		json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{
			"content":           map[string]any{"role": "model", "parts": []any{map[string]any{"text": strings.Join(lines, "\n")}}},
			"groundingMetadata": map[string]any{"groundingChunks": chunks, "groundingSupports": supports},
		}}})
	}))
	defer srv.Close()
	e.warden.search = newWardenSearch(&geminiModel{http: srv.Client(), model: "g", key: "k", base: srv.URL, tools: aiEditTools})
	e.warden.now = func() time.Time { return time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC) }

	code, out = wardenCall(t, base, "tero", "run_watch", map[string]any{"space_id": sid, "event_id": id})
	eq(t, code, 200, "run")
	eq(t, asked, []string{"Lasten sängyt site:tori.fi", "Lasten sängyt site:vinted.fi", "Lasten sängyt site:huuto.net"})
	fresh := out["new"].([]any)
	eq(t, len(fresh), 3, "the 80 € bed, the doll's bed and the cot without a price")
	f := fresh[1].(map[string]any)
	eq(t, []any{f["title"], f["price"], f["location"], f["source"], f["found"], f["score"], f["why"]}, []any{"Lasten sänky 90x200", 80.0, "Tampere", "Tori", "2026-10-17", 85.0, "a child's bed, 90x200"})
	eq(t, fresh[0].(map[string]any)["title"], "Pinnasänky", "no fit given counts as fitting")
	eq(t, fresh[2].(map[string]any)["title"], "Nukensänky", "worst last")
	if _, ok := fresh[0].(map[string]any)["location"]; ok {
		t.Fatal("a town not given is left out")
	}
	lw := out["watch"].(map[string]any)
	r0 := lw["runs"].([]any)[0].(map[string]any)
	eq(t, []any{r0["results"], r0["new"], r0["left_out"]}, []any{7.0, 2.0, map[string]any{"too_expensive": 1.0, "excluded": 1.0, "not_listing": 1.0, "other_site": 1.0, "low_fit": 1.0}})

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
	eq(t, len(item["watch"].(map[string]any)["finds"].([]any)), 3, "finds on the entry")
	if _, ok := item["watch"].(map[string]any)["seen"]; ok {
		t.Fatal("seen is the server's own")
	}
	eq(t, []any{rem["title"], rem["date"], rem["source"]}, []any{"Tarjouksia: Lasten sängyt (2 uutta)", "2026-10-17", "Tarjousvahti"})
	if n := rem["notes"].(string); !strings.HasPrefix(n, "Paras: Pinnasänky\n") || !strings.Contains(n, "https://www.tori.fi/recommerce/forsale/item/111") || strings.Contains(n, "Nukensänky") {
		t.Fatalf("notes: %v", rem["notes"])
	}

	// the doll's bed hidden as the wrong thing: later searches are told of
	// it, and of the intent
	doll := fresh[2].(map[string]any)["id"].(string)
	code, _ = wardenCall(t, base, "tero", "rate_find", map[string]any{"space_id": sid, "event_id": id, "find_id": doll, "rating": "like", "reason": "too_far"})
	eq(t, code, 400, "a reason only with hide")
	code, out = wardenCall(t, base, "tero", "rate_find", map[string]any{"space_id": sid, "event_id": id, "find_id": doll, "rating": "hide", "reason": "wrong_item"})
	eq(t, []any{code, out["find"].(map[string]any)["rating"], out["find"].(map[string]any)["reason"]}, []any{200, "hide", "wrong_item"})
	code, _ = wardenCall(t, base, "tero", "rate_find", map[string]any{"space_id": sid, "event_id": id, "find_id": "nope", "rating": "like"})
	eq(t, code, 400, "no such find")
	_, out = wardenCall(t, base, "tero", "rate_find", map[string]any{"space_id": sid, "event_id": id, "find_id": fresh[0].(map[string]any)["id"], "rating": "like"})
	_, out = wardenCall(t, base, "tero", "rate_find", map[string]any{"space_id": sid, "event_id": id, "find_id": fresh[0].(map[string]any)["id"], "rating": "none"})
	if _, ok := out["find"].(map[string]any)["rating"]; ok {
		t.Fatal("none clears the rating")
	}
	wardenCall(t, base, "tero", "set_watch", map[string]any{"space_id": sid, "event_id": id, "intent": "Sänky 6-vuotiaalle"})
	intent, avoid = "Sänky 6-vuotiaalle", "- Nukensänky (not the thing wanted)"

	// again: nothing new, the reminder stays as it was
	code, out = wardenCall(t, base, "tero", "run_watch", map[string]any{"space_id": sid, "event_id": id})
	eq(t, code, 200, "told of the hidden find and the intent")
	eq(t, len(out["new"].([]any)), 0, "seen already")
	eq(t, out["watch"].(map[string]any)["intent"], "Sänky 6-vuotiaalle")

	// the scheduled run: a wrong token, then not due yet, then due
	e.warden.cronAuth = func(_ context.Context, tok string) bool { return tok == "secret" }
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
	results["Lasten sängyt site:huuto.net"] = []listing{{"Sänky", "10 €", "https://www.huuto.net/kohteet/sanky/555", "huuto.net", "Oulu", ""}}
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
	lw = l[0].(map[string]any)["watch"].(map[string]any)
	eq(t, []any{lw["active"], len(lw["finds"].([]any)), len(lw["runs"].([]any))}, []any{false, 4, 3})

	// another member's space is not reachable
	code, _ = wardenCall(t, base, "anna", "set_watch", map[string]any{"space_id": sid, "event_id": id})
	eq(t, code, 400, "anna is not a member")
}

// The town a watch prefers: its own, the ward's, the space's, the entry's
// place, else Tampere.
func TestWardenWatchArea(t *testing.T) {
	sp := &wardenSpace{doc: Doc{"wards": []any{map[string]any{"id": "w1", "places": []any{map[string]any{"name": "Kuopio"}}}, map[string]any{"id": "w2"}}}}
	eq(t, wardenWatchArea(sp, Doc{"ward": "w1"}, map[string]any{"area": "Espoo"}), "Espoo")
	eq(t, wardenWatchArea(sp, Doc{"ward": "w1"}, map[string]any{}), "Kuopio")
	eq(t, wardenWatchArea(sp, Doc{"ward": "w2", "place": "Hervanta, Tampere"}, map[string]any{}), "Hervanta, Tampere")
	eq(t, wardenWatchArea(sp, Doc{}, map[string]any{}), "Tampere")
	sp.doc["places"] = []any{map[string]any{"name": "Lahti", "lat": 60.98, "lon": 25.66}}
	eq(t, wardenWatchArea(sp, Doc{"ward": "w2", "place": "Koti"}, map[string]any{}), "Lahti")
}
