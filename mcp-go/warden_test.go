// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// the page's calls, as one signed in with cookie
func wardenCall(t *testing.T, base, cookie, op string, a map[string]any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(a)
	res, body := editorDo(t, "POST", base+"/apps/warden/api/"+op, cookie, base, string(b))
	out := map[string]any{}
	json.Unmarshal([]byte(body), &out)
	return res.StatusCode, out
}

func TestWardenPageSpacesMembers(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()

	code, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Kotona", "vocab": "family"})
	eq(t, code, 200, "tero makes a space")
	sid, _ := out["space_id"].(string)
	eq(t, []any{out["role"], out["vocab"], out["lang"], out["time_zone"]}, []any{"owner", "family", "fi", "Europe/Helsinki"})

	// signed out, another site, a stranger
	code, out = wardenCall(t, base, "", "list_spaces", nil)
	eq(t, []any{code, out["code"]}, []any{401, "signed-out"}, "signed out")
	res, _ := editorDo(t, "POST", base+"/apps/warden/api/list_spaces", "tero", "https://evil.example", "{}")
	eq(t, res.StatusCode, 403, "another site")
	code, out = wardenCall(t, base, "anna", "get_space", map[string]any{"space_id": sid})
	eq(t, []any{code, out["error"]}, []any{400, "no such space, or you are not a member of it"}, "anna is not a member")
	code, _ = wardenCall(t, base, "tero", "no_such_op", nil)
	eq(t, code, 404, "no such call")

	// tero invites anna by address; mallory, who claims the address
	// unverified, does not get in; anna does, by opening the page
	code, out = wardenCall(t, base, "tero", "invite_member", map[string]any{"space_id": sid, "email": "Anna@Example.com"})
	eq(t, []any{code, out["invites"]}, []any{200, []any{"anna@example.com"}}, "invited")
	_, out = wardenCall(t, base, "mallory", "list_spaces", nil)
	eq(t, len(out["spaces"].([]any)), 0, "mallory sees none")
	_, out = wardenCall(t, base, "anna", "list_spaces", nil)
	spaces := out["spaces"].([]any)
	eq(t, len(spaces), 1, "anna took the invite")
	sp := spaces[0].(map[string]any)
	eq(t, []any{sp["role"], len(sp["members"].([]any)), len(sp["invites"].([]any))}, []any{"member", 2, 0})

	// only the owner invites, renames or removes others; a member may leave
	code, out = wardenCall(t, base, "anna", "invite_member", map[string]any{"space_id": sid, "email": "x@example.com"})
	eq(t, []any{code, out["error"]}, []any{400, "only the space's owner can do that"}, "anna invites")
	code, _ = wardenCall(t, base, "anna", "set_space", map[string]any{"space_id": sid, "name": "Mine"})
	eq(t, code, 400, "anna renames")
	code, out = wardenCall(t, base, "tero", "remove_member", map[string]any{"space_id": sid, "member": "u-tero"})
	eq(t, []any{code, out["error"]}, []any{400, "the owner stays in the space; delete the space instead"})
	code, out = wardenCall(t, base, "anna", "remove_member", map[string]any{"space_id": sid, "member": "anna@example.com"})
	eq(t, []any{code, len(out["members"].([]any))}, []any{200, 1}, "anna leaves")
	code, _ = wardenCall(t, base, "anna", "get_space", map[string]any{"space_id": sid})
	eq(t, code, 400, "and is out")

	// delete asks for the name
	code, _ = wardenCall(t, base, "tero", "delete_space", map[string]any{"space_id": sid, "confirm_name": "kotona"})
	eq(t, code, 400, "the name exactly")
	code, out = wardenCall(t, base, "tero", "delete_space", map[string]any{"space_id": sid, "confirm_name": "Kotona"})
	eq(t, []any{code, out["deleted"]}, []any{200, sid})
}

func TestWardenEvents(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Demo"})
	sid := out["space_id"].(string)

	evs := []any{
		map[string]any{"title": "Swimming", "date": "2026-10-20", "start": "17:00", "end": "18:00", "ward": "Aino", "ext_id": "cal-1",
			"rrule": "freq=weekly;byday=TU;until=20261215T000000Z", "who": []any{map[string]any{"name": "Leo", "role": "takes"}}, "important": true},
		map[string]any{"title": "Autumn break", "date": "2026-10-19", "end_date": "2026-10-23", "ward": "aino"},
		map[string]any{"title": "Car service", "date": "2026-10-22", "start": "08:30", "ward": "Car", "ext_id": "cal-2"},
	}
	code, out := wardenCall(t, base, "tero", "import_events", map[string]any{"space_id": sid, "events": evs})
	eq(t, []any{code, out["created"], out["updated"]}, []any{200, 3.0, 0.0}, "imported")
	wards := out["wards"].([]any)
	eq(t, len(wards), 2, "Aino and Car made, aino is Aino")

	// again: the two with an ext_id are changed, the third is new
	evs[0].(map[string]any)["title"] = "Swimming school"
	code, out = wardenCall(t, base, "tero", "import_events", map[string]any{"space_id": sid, "events": evs})
	eq(t, []any{code, out["created"], out["updated"]}, []any{200, 1.0, 2.0}, "imported again")

	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	got := out["events"].([]any)
	eq(t, len(got), 4)
	var swim map[string]any
	for _, e := range got {
		if e.(map[string]any)["title"] == "Swimming school" {
			swim = e.(map[string]any)
		}
	}
	eq(t, []any{swim["rrule"], swim["important"], swim["start"], swim["end"]}, []any{"FREQ=WEEKLY;BYDAY=TU;UNTIL=20261215", true, "17:00", "18:00"})

	// the range: the car service is before the 23rd's week's Monday
	_, out = wardenCall(t, base, "tero", "list_events", map[string]any{"space_id": sid, "from": "2026-10-23", "to": "2026-10-31"})
	eq(t, len(out["events"].([]any)), 3, "swimming repeats, both breaks end the 23rd")
	_, out = wardenCall(t, base, "tero", "list_events", map[string]any{"space_id": sid, "important_only": true})
	eq(t, len(out["events"].([]any)), 1, "important only")

	// one bad event and nothing is saved
	code, out = wardenCall(t, base, "tero", "import_events", map[string]any{"space_id": sid, "events": []any{
		map[string]any{"title": "Fine", "date": "2026-11-01"},
		map[string]any{"title": "Bad", "date": "1.11.2026"},
	}})
	eq(t, []any{code, out["error"]}, []any{400, "events[1]: date is a day as YYYY-MM-DD; nothing was imported"})
	for _, bad := range []map[string]any{
		{"title": "x", "date": "2026-11-01", "start": "25:00"},
		{"title": "x", "date": "2026-11-01", "rrule": "FREQ=YEARLY"},
		{"title": "x", "date": "2026-11-01", "rrule": "FREQ=WEEKLY;BYDAY=1MO"},
		{"title": "x", "date": "2026-02-30"},
		{"title": "", "date": "2026-11-01"},
		{"title": "x", "date": "2026-11-01", "color": "url(x)"},
	} {
		bad["space_id"] = sid
		code, _ = wardenCall(t, base, "tero", "save_event", bad)
		eq(t, code, 400, bad)
	}

	// save, change one field, delete
	code, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "Dentist", "date": "2026-10-21", "start": "09:00"})
	eq(t, []any{code, out["end"]}, []any{200, "10:00"}, "an hour when no end")
	id := out["event_id"].(string)
	code, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "event_id": id, "place": "Clinic"})
	eq(t, []any{code, out["title"], out["place"]}, []any{200, "Dentist", "Clinic"})
	code, _ = wardenCall(t, base, "anna", "delete_event", map[string]any{"space_id": sid, "event_id": id})
	eq(t, code, 400, "anna is no member")
	code, _ = wardenCall(t, base, "tero", "delete_event", map[string]any{"space_id": sid, "event_id": id})
	eq(t, code, 200, "deleted")
	code, _ = wardenCall(t, base, "tero", "delete_event", map[string]any{"space_id": sid, "event_id": id})
	eq(t, code, 400, "gone")
}

const wardenICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" +
	"BEGIN:VEVENT\r\nUID:a1\r\nSUMMARY:Parents' evening\\, class 3\r\nDTSTART:20261021T150000Z\r\nDTEND:20261021T163000Z\r\nLOCATION:School\r\nDESCRIPTION:Bring the\r\n  form\r\nBEGIN:VALARM\r\nACTION:DISPLAY\r\nDESCRIPTION:alarm\r\nEND:VALARM\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:a2\r\nSUMMARY:Camp\r\nDTSTART;VALUE=DATE:20261102\r\nDTEND;VALUE=DATE:20261105\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:a3\r\nSUMMARY:Practice\r\nDTSTART;TZID=America/New_York:20261020T120000\r\nDTEND;TZID=America/New_York:20261020T130000\r\nRRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=10\r\nEXDATE;TZID=America/New_York:20261103T120000\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:a3\r\nRECURRENCE-ID;TZID=America/New_York:20261027T120000\r\nSUMMARY:Practice (moved)\r\nDTSTART;TZID=America/New_York:20261028T120000\r\nDTEND;TZID=America/New_York:20261028T130000\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:a3\r\nRECURRENCE-ID;TZID=America/New_York:20261110T120000\r\nSTATUS:CANCELLED\r\nDTSTART;TZID=America/New_York:20261110T120000\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:a4\r\nSUMMARY:Called off\r\nSTATUS:CANCELLED\r\nDTSTART:20261030T100000Z\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestWardenICS(t *testing.T) {
	loc, _ := time.LoadLocation(wardenTZ)
	evs, cancelled, err := icsToEvents(wardenICS, loc)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, []any{len(evs), cancelled}, []any{4, 2})
	pe := evs[0].(map[string]any)
	eq(t, []any{pe["title"], pe["date"], pe["start"], pe["end"], pe["place"], pe["notes"], pe["ext_id"]},
		[]any{"Parents' evening, class 3", "2026-10-21", "18:00", "19:30", "School", "Bring the form", "a1"}, "UTC to Helsinki, unfolded, unescaped, the alarm left out")
	camp := evs[1].(map[string]any)
	eq(t, []any{camp["date"], camp["end_date"], camp["start"]}, []any{"2026-11-02", "2026-11-04", nil}, "all day, DTEND the day after")
	pr := evs[2].(map[string]any)
	eq(t, []any{pr["start"], pr["rrule"], pr["exdates"]}, []any{"19:00", "FREQ=WEEKLY;BYDAY=TU;COUNT=10", []any{"2026-11-03", "2026-10-27", "2026-11-10"}}, "New York to Helsinki; moved and cancelled days skipped")
	mv := evs[3].(map[string]any)
	eq(t, []any{mv["ext_id"], mv["date"]}, []any{"a3/2026-10-27", "2026-10-28"})

	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Demo"})
	sid := out["space_id"].(string)
	for i, want := range [][]any{{200, 4.0, 0.0}, {200, 0.0, 4.0}} {
		code, out := wardenCall(t, base, "tero", "import_ics", map[string]any{"space_id": sid, "ics": wardenICS, "ward": "Aino", "important": true})
		eq(t, []any{code, out["created"], out["updated"]}, want, i)
	}
	_, out = wardenCall(t, base, "tero", "list_events", map[string]any{"space_id": sid, "important_only": true})
	eq(t, len(out["events"].([]any)), 4)
	eq(t, out["events"].([]any)[0].(map[string]any)["source"], "ics")
}

// Warden's own MCP server: OAuth, its tools, the page's spaces
func TestWardenMcp(t *testing.T) {
	env, base, stop := editorServer(t)
	defer stop()
	env.OAuth = true
	token := func(tok string, d Doc) string {
		sum := sha256.Sum256([]byte(tok))
		env.DB.Set(context.Background(), "mcp_oauth_tokens", hex.EncodeToString(sum[:]), d)
		return tok
	}
	exp := time.Now().Add(time.Hour).UnixMilli()
	mine := token("w-ok", Doc{"uid": "u-tero", "name": "Tero", "kind": "access", "exp": exp, "resource": BASE + "/apps/warden/mcp"})
	plain := token("w-plain", Doc{"uid": "u-tero", "name": "Tero", "kind": "access", "exp": exp})
	other := token("w-other", Doc{"uid": "u-tero", "name": "Tero", "kind": "access", "exp": exp, "resource": BASE + "/mcp"})
	old := token("w-old", Doc{"uid": "u-tero", "name": "Tero", "kind": "access", "exp": time.Now().Add(-time.Minute).UnixMilli()})

	rpc := func(tok string, body any) (*http.Response, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", base+"/apps/warden/mcp", strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out := map[string]any{}
		json.NewDecoder(res.Body).Decode(&out)
		return res, out
	}
	for _, tok := range []string{"", other, old, "nope"} {
		res, _ := rpc(tok, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}})
		eq(t, res.StatusCode, 401, tok)
		eq(t, res.Header.Get("WWW-Authenticate"), `Bearer resource_metadata="`+BASE+`/.well-known/oauth-protected-resource/apps/warden/mcp"`)
	}
	res, body := editorDo(t, "GET", base+"/.well-known/oauth-protected-resource/apps/warden/mcp", "", "", "")
	eq(t, res.StatusCode, 200)
	meta := map[string]any{}
	json.Unmarshal([]byte(body), &meta)
	eq(t, []any{meta["resource"], meta["resource_name"], meta["authorization_servers"]}, []any{BASE + "/apps/warden/mcp", "Warden", []any{BASE}})

	_, out := rpc(mine, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18"}})
	r := out["result"].(map[string]any)
	eq(t, []any{r["protocolVersion"], r["serverInfo"].(map[string]any)["title"]}, []any{"2025-06-18", "Warden"})
	res, _ = rpc(mine, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	eq(t, res.StatusCode, 202, "a notification")
	_, out = rpc(plain, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	tools := out["result"].(map[string]any)["tools"].([]any)
	names := []string{}
	for _, x := range tools {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	match(t, strings.Join(names, " "), "^list_spaces create_space get_space list_events list_entry_types budget import_events import_ics save_event delete_event")

	call := func(name string, args map[string]any) (bool, map[string]any, string) {
		t.Helper()
		_, out := rpc(mine, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
		r := out["result"].(map[string]any)
		text := r["content"].([]any)[0].(map[string]any)["text"].(string)
		sc, _ := r["structuredContent"].(map[string]any)
		return r["isError"].(bool), sc, text
	}
	// the space tero made on the page is the assistant's too
	_, sp := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti"})
	sid := sp["space_id"].(string)
	isErr, sc, _ := call("list_spaces", nil)
	eq(t, []any{isErr, len(sc["spaces"].([]any))}, []any{false, 1})
	isErr, sc, _ = call("import_events", map[string]any{"space_id": sid, "events": []any{map[string]any{"title": "Match", "date": "2026-10-24", "start": "10:00", "ward": "Leo", "ext_id": "g-1"}}})
	eq(t, []any{isErr, sc["created"]}, []any{false, 1.0})
	isErr, _, text := call("import_events", map[string]any{"space_id": "s-nope", "events": []any{}})
	eq(t, []any{isErr, text}, []any{true, "no such space, or you are not a member of it"})
	_, out = rpc(mine, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "nope"}})
	eq(t, out["error"].(map[string]any)["code"], -32602.0)
	_, page := wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	eq(t, len(page["events"].([]any)), 1, "the page sees what the assistant brought")
}

func TestWardenLogin(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	res, body := editorDo(t, "GET", base+"/apps/warden/login", "", "", "")
	eq(t, res.StatusCode, 200)
	for _, want := range []string{"<title>Warden</title>", "Sign in to Warden", "Kirjaudu Wardeniin", "Warden on ryhmän", "The page did not open", "Sivu ei auennut"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the sign-in page lacks %q: the editor's page changed its words", want)
		}
	}
	res, _ = editorDo(t, "GET", base+"/apps/warden/login", "tero", "", "")
	eq(t, []any{res.StatusCode, res.Header.Get("Location")}, []any{302, "/apps/warden/"})
	res, _ = editorDo(t, "GET", base+"/apps/warden/index.html", "tero", "", "")
	eq(t, res.StatusCode, 404, "the page is Hosting's")
}

func TestWardenRemoveWard(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)
	_, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "Uinti", "date": "2026-11-02", "ward": "Alex"})
	eid := out["event_id"].(string)
	_, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "Synttärit", "date": "2026-11-07", "ward": "Aino"})
	pid := out["event_id"].(string)
	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	eq(t, len(out["space"].(map[string]any)["wards"].([]any)), 2, "Alex and Aino")
	_, out = wardenCall(t, base, "tero", "invite_member", map[string]any{"space_id": sid, "email": "anna@example.com"})
	wardenCall(t, base, "anna", "list_spaces", nil)

	code, out := wardenCall(t, base, "anna", "remove_ward", map[string]any{"space_id": sid, "ward": "Alex"})
	eq(t, []any{code, out["error"]}, []any{400, "only the space's owner can do that"}, "anna removes")
	code, out = wardenCall(t, base, "tero", "remove_ward", map[string]any{"space_id": sid, "ward": "Nobody"})
	eq(t, code, 400, "no such ward")
	code, out = wardenCall(t, base, "tero", "remove_ward", map[string]any{"space_id": sid, "ward": "alex"})
	eq(t, []any{code, out["events_cleared"], len(out["wards"].([]any))}, []any{200, float64(1), 1}, "removed")

	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	for _, e := range out["events"].([]any) {
		m := e.(map[string]any)
		switch m["event_id"] {
		case eid:
			eq(t, m["ward"], nil, "the event stays, without a ward")
		case pid:
			eq(t, m["ward"] != "", true, "Aino stays")
		}
	}
}

func TestWardenWardColors(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)
	colors := []any{}
	for _, n := range []string{"Alex", "Leevi", "Iida"} {
		_, out = wardenCall(t, base, "tero", "add_ward", map[string]any{"space_id": sid, "name": n})
		colors = append(colors, out["color"])
	}
	eq(t, colors, []any{"blue", "emerald", "violet"}, "each its own colour of the page's palette")
	code, out := wardenCall(t, base, "tero", "set_ward", map[string]any{"space_id": sid, "ward": "leevi", "color": "amber"})
	eq(t, []any{code, out["name"], out["color"]}, []any{200, "Leevi", "amber"}, "recoloured")
	code, _ = wardenCall(t, base, "tero", "set_ward", map[string]any{"space_id": sid, "ward": "Leevi", "color": "plaid"})
	eq(t, code, 400, "not a colour")
	code, _ = wardenCall(t, base, "tero", "set_ward", map[string]any{"space_id": sid, "ward": "Leevi", "name": "Iida"})
	eq(t, code, 400, "a name another ward has")
}

// A child's clothing sizes and clothing list are the ward's, set whole with
// set_ward (or add_ward) and given back with the space, what is missing counted.
func TestWardenWardClothing(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)
	code, out := wardenCall(t, base, "tero", "add_ward", map[string]any{"space_id": sid, "name": "Aino",
		"sizes": []any{map[string]any{"what": "kengät", "size": "30", "next_size": "31", "next_by": "2027-03"}}})
	eq(t, []any{code, out["sizes"]}, []any{200, []any{map[string]any{"what": "kengät", "size": "30", "next_size": "31", "next_by": "2027-03"}}}, "added with sizes")

	code, out = wardenCall(t, base, "tero", "set_ward", map[string]any{"space_id": sid, "ward": "aino",
		"clothes": []any{
			map[string]any{"item": "talvikengät", "size": "31", "need": 1},
			map[string]any{"item": "sukat", "need": 6, "have": 4, "note": "villasukat"},
			map[string]any{"item": "pipo", "have": 1},
		}})
	eq(t, code, 200, "clothes set")
	eq(t, out["clothes"], []any{
		map[string]any{"item": "talvikengät", "size": "31", "need": 1.0, "have": 0.0, "missing": 1.0},
		map[string]any{"item": "sukat", "need": 6.0, "have": 4.0, "note": "villasukat", "missing": 2.0},
		map[string]any{"item": "pipo", "need": 1.0, "have": 1.0, "missing": 0.0},
	})
	eq(t, len(out["sizes"].([]any)), 1, "the sizes were left as they were")

	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	w := out["space"].(map[string]any)["wards"].([]any)[0].(map[string]any)
	eq(t, []any{len(w["sizes"].([]any)), len(w["clothes"].([]any))}, []any{1, 3}, "with the space")

	for _, bad := range []map[string]any{
		{"sizes": "30"},
		{"sizes": []any{map[string]any{"size": "30"}}},
		{"sizes": []any{map[string]any{"what": "kengät"}, map[string]any{"what": "Kengät"}}},
		{"sizes": []any{map[string]any{"what": "kengät", "next_by": "maaliskuu"}}},
		{"clothes": []any{map[string]any{"need": 2}}},
		{"clothes": []any{map[string]any{"item": "sukat", "have": -1}}},
		{"clothes": []any{map[string]any{"item": "sukat", "need": 1.5}}},
	} {
		bad["space_id"], bad["ward"] = sid, "Aino"
		code, out = wardenCall(t, base, "tero", "set_ward", bad)
		eq(t, code, 400, bad, out)
	}
	code, out = wardenCall(t, base, "tero", "set_ward", map[string]any{"space_id": sid, "ward": "Aino", "sizes": []any{}})
	eq(t, []any{code, out["sizes"], len(out["clothes"].([]any))}, []any{200, nil, 3}, "[] clears the sizes")
}
