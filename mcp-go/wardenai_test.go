// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// A school letter and its photo in: the model reads the space's types, makes
// the entries with Warden's own tools, and answers.
func TestWardenAssist(t *testing.T) {
	e, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)

	// no model: said so
	code, out := wardenCall(t, base, "tero", "assist", map[string]any{"space_id": sid, "text": "hei"})
	eq(t, []any{code, out["error"]}, []any{400, "The assistant is not available on this server."})

	var asks []map[string]any
	replies := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"import_events","args":{"events":[
			{"title":"Ellan synttärit","type":"birthday_invitation","date":"2026-11-07","start":"14:00","ward":"Aino","ext_id":"assist:kutsu:ella",
			 "fields":{"birthday_child":"Ella","rsvp_by":"2026-11-01","gift_idea":"Kirja"}},
			{"title":"Uinti","type":"activity","date":"2026-11-03","start":"17:00","end":"18:00","ward":"Leo","rrule":"FREQ=WEEKLY;BYDAY=TU;UNTIL=20270531",
			 "fields":{"hobby":"Uinti","season_fee":120,"gear":["uimalasit","pyyhe"]}}]}}}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"save_event","args":{"title":"Hoitaja","type":"babysitter_booking","date":"2026-11-07"}}}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Lisäsin Ellan synttärit 7.11. ja Leon uinnit tiistaisin."}]}}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		json.NewDecoder(r.Body).Decode(&got)
		asks = append(asks, got)
		io.WriteString(w, replies[len(asks)-1])
	}))
	defer srv.Close()
	e.warden.now = func() time.Time { return time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC) }
	e.warden.ai = newWardenAssist(&geminiModel{http: srv.Client(), model: "g", key: "k", base: srv.URL, tools: aiEditTools})

	pic := base64.StdEncoding.EncodeToString([]byte(fakePNG))
	code, out = wardenCall(t, base, "tero", "assist", map[string]any{"space_id": sid, "today": "2026-10-16",
		"text":   "Ellan synttärit la 7.11. klo 14, vastaus 1.11. mennessä. Leon uinti tiistaisin 17-18 toukokuun loppuun.",
		"images": []any{map[string]any{"mimeType": "image/png", "data": pic}}})
	eq(t, code, 200, "assist")
	eq(t, []any{out["text"], out["changed"], len(out["event_ids"].([]any))}, []any{"Lisäsin Ellan synttärit 7.11. ja Leon uinnit tiistaisin.", true, 2})
	eq(t, out["calls"], []any{map[string]any{"tool": "import_events", "ok": true}, map[string]any{"tool": "save_event", "ok": false}})

	// the model was told the day, the types and the common fields; its
	// tools are Warden's, without space_id; the picture went with the text
	sys := fmt.Sprint(asks[0]["systemInstruction"])
	for _, want := range []string{"<today>2026-10-16, Friday</today>", "- birthday_invitation (event, event)", "schedule_exception", "<common_fields>subtype:text"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
	tools, _ := json.Marshal(asks[0]["tools"])
	match(t, string(tools), `"name":"import_events"`)
	// Gemini refuses an object with no properties: list_entry_types has none
	match(t, string(tools), `{"description":"[^"]*","name":"list_entry_types"}`)
	match(t, string(tools), `"name":"set_ward"`)
	if strings.Contains(string(tools), "space_id") || strings.Contains(string(tools), `"name":"list_spaces"`) || strings.Contains(string(tools), "replace_slide") {
		t.Errorf("tools: %s", tools)
	}
	contents, _ := json.Marshal(asks[0]["contents"])
	match(t, string(contents), `"inlineData":{"data":"`+pic)
	// the refusal went back to the model to mend
	again, _ := json.Marshal(asks[2]["contents"])
	match(t, string(again), "a babysitter_booking needs start, fields.sitter")

	// the entries are in the space
	_, out = wardenCall(t, base, "tero", "list_events", map[string]any{"space_id": sid, "from": "2026-11-01", "to": "2026-11-30"})
	titles := []string{}
	for _, ev := range out["events"].([]any) {
		m := ev.(map[string]any)
		titles = append(titles, m["title"].(string)+" "+m["type"].(string))
	}
	eq(t, strings.Join(titles, ", "), "Uinti activity, Ellan synttärit birthday_invitation")

	// an empty question is not sent to the model
	code, out = wardenCall(t, base, "tero", "assist", map[string]any{"space_id": sid, "text": "  "})
	eq(t, []any{code, out["error"], len(asks)}, []any{400, "Write something or add a picture.", 3})
}

// The page's assistant has every MCP tool of a space, as the MCP server
// gives it: a tool added to wardenTools reaches the assistant, and one the
// assistant does not get is named in wardenAssistLeftOut.
func TestWardenAssistToolsAreMCP(t *testing.T) {
	for _, name := range wardenAssistLeftOut {
		if findWardenTool(name) == nil {
			t.Errorf("left out %s is no MCP tool", name)
		}
	}
	got := map[string]aiTool{}
	for _, at := range wardenAssistToolList() {
		got[at.Name] = at
	}
	for _, mt := range wardenTools {
		at, ok := got[mt.name]
		delete(got, mt.name)
		if slices.Contains(wardenAssistLeftOut, mt.name) {
			if ok {
				t.Errorf("%s is left out but the assistant has it", mt.name)
			}
			continue
		}
		if !ok {
			t.Errorf("the assistant lacks the MCP tool %s", mt.name)
			continue
		}
		if at.Description != mt.desc {
			t.Errorf("%s: the assistant's description differs", mt.name)
		}
		props, _ := at.Params["properties"].(map[string]any)
		for k, v := range mt.props {
			if k == "space_id" {
				continue
			}
			av, ok := props[k]
			if !ok {
				t.Errorf("%s: the assistant lacks the argument %s", mt.name, k)
				continue
			}
			// fields (and an event's) are typed for the model: the same kind
			if k == "fields" || k == "events" {
				if av.(map[string]any)["type"] != v.(map[string]any)["type"] {
					t.Errorf("%s.%s: another type", mt.name, k)
				}
				continue
			}
			if fmt.Sprint(av) != fmt.Sprint(v) {
				t.Errorf("%s.%s differs: %v", mt.name, k, av)
			}
		}
		if len(props) != len(mt.props)-btoi(mt.props["space_id"] != nil) {
			t.Errorf("%s: the assistant has other arguments: %v", mt.name, props)
		}
		req := slices.DeleteFunc(slices.Clone(mt.required), func(k string) bool { return k == "space_id" })
		if fmt.Sprint(at.Params["required"]) != fmt.Sprint(req) {
			t.Errorf("%s: required %v, MCP %v", mt.name, at.Params["required"], mt.required)
		}
	}
	for name := range got {
		t.Errorf("the assistant has %s, which MCP has not", name)
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
