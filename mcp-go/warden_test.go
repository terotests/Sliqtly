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
	match(t, strings.Join(names, " "), "^list_spaces create_space get_space list_events list_entry_types budget own_budget save_own_item delete_own_item email_summary info_screen import_events import_ics calendar_feed save_event delete_event list_deleted restore_event")

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

// The open page asks whether its space changed: a change an assistant (or
// another page) made is news; one the asking page made itself is "mine".
func TestWardenChanges(t *testing.T) {
	env, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti"})
	sid := out["space_id"].(string)
	_, out = wardenCall(t, base, "tero", "changes", map[string]any{"space_id": sid})
	eq(t, []any{out["changed"], out["mine"]}, []any{"0", false}, "nothing yet")

	ctx := context.Background()
	who := wardenWho{uid: "u-tero", name: "Tero"}
	_, err := env.warden.call(ctx, who, "save_event", map[string]any{"space_id": sid, "title": "Uinti", "date": "2026-10-20"}, false)
	eq(t, err, nil)
	_, out = wardenCall(t, base, "tero", "changes", map[string]any{"space_id": sid})
	first := out["changed"]
	eq(t, []any{first != "0", out["mine"]}, []any{true, false}, "an assistant's change")
	_, sp := wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	eq(t, sp["changed"], first, "get_space says when")

	page := who
	page.client = "page-1"
	_, err = env.warden.call(ctx, page, "add_ward", map[string]any{"space_id": sid, "name": "Aino"}, true)
	eq(t, err, nil)
	got, _ := env.warden.call(ctx, page, "changes", map[string]any{"space_id": sid}, true)
	mine := got.(map[string]any)
	eq(t, mine["mine"], true, "the page's own")
	_, err = env.warden.call(ctx, who, "changes", map[string]any{"space_id": sid}, false)
	eq(t, err != nil, true, "not an assistant's operation")

	// reading changes nothing
	env.warden.call(ctx, who, "list_events", map[string]any{"space_id": sid}, false)
	got, _ = env.warden.call(ctx, page, "changes", map[string]any{"space_id": sid}, true)
	eq(t, got.(map[string]any)["changed"], mine["changed"], "a read")
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

// the space out as iCalendar: a feed to subscribe to, one entry to download
func TestWardenFeed(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Kotona"})
	sid := out["space_id"].(string)
	wardenCall(t, base, "tero", "invite_member", map[string]any{"space_id": sid, "email": "anna@example.com"})
	wardenCall(t, base, "anna", "list_spaces", nil)
	_, out = wardenCall(t, base, "tero", "import_events", map[string]any{"space_id": sid, "events": []any{
		map[string]any{"title": "Leevin jumppa", "date": "2026-10-21", "start": "17:00", "end": "18:00", "ward": "Leevi", "ext_id": "j",
			"rrule": "FREQ=WEEKLY;BYDAY=WE;UNTIL=20261216", "exdates": []any{"2026-11-04"}, "who": []any{map[string]any{"name": "Tero", "role": "vie"}}},
		map[string]any{"title": "Lapset Terolla", "date": "2026-10-30", "end_date": "2026-11-01", "notes": "Avaimet; muista, kiitos"},
		map[string]any{"title": "Ilman päivää", "type": "need"},
	}})
	jumppa := out["event_ids"].([]any)[0].(string)

	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	feed := out["space"].(map[string]any)["calendar_feed"].(string)
	path := feed[strings.Index(feed, "/apps/warden/cal/"):]
	_, out = wardenCall(t, base, "anna", "calendar_feed", map[string]any{"space_id": sid})
	eq(t, []any{out["url"], strings.HasPrefix(out["webcal"].(string), "webcal://")}, []any{feed, true}, "a member gets the same address")

	get := func(p, cookie string) (int, string) {
		res, body := editorDo(t, "GET", base+p, cookie, "", "")
		return res.StatusCode, body
	}
	code, ics := get(path, "")
	eq(t, code, 200, "no sign-in for the feed")
	for _, want := range []string{"X-WR-CALNAME:Kotona", "UID:" + jumppa + "@warden.sliqtly.com", "BEGIN:VTIMEZONE", "TZID:Europe/Helsinki",
		"BYMONTH=10;BYDAY=-1SU", "DTSTART;TZID=Europe/Helsinki:20261021T170000", "RRULE:FREQ=WEEKLY;BYDAY=WE;UNTIL=20261216T215959Z",
		"EXDATE;TZID=Europe/Helsinki:20261104T170000", "CATEGORIES:Leevi", `DESCRIPTION:Avaimet\; muista\, kiitos`, "DTEND;VALUE=DATE:20261102"} {
		if !strings.Contains(ics, want) {
			t.Errorf("feed has no %q:\n%s", want, ics)
		}
	}
	eq(t, strings.Count(ics, "BEGIN:VEVENT"), 2, "the entry without a day is left out")

	// what the address answers to, and its reads kept for calendar_feed
	res, _ := editorDo(t, "OPTIONS", base+path, "", "", "")
	eq(t, []any{res.StatusCode, res.Header.Get("Allow")}, []any{200, "GET, HEAD, OPTIONS"}, "OPTIONS says what it takes")
	res, _ = editorDo(t, "PROPFIND", base+path, "", "", "")
	eq(t, res.StatusCode, 405, "and nothing else")
	_, out = wardenCall(t, base, "tero", "calendar_feed", map[string]any{"space_id": sid})
	reads := out["reads"].([]any)
	eq(t, len(reads), 3, "three reads kept")
	last := reads[0].(map[string]any)
	first := reads[2].(map[string]any)
	eq(t, []any{last["method"], last["status"], first["method"], first["status"]}, []any{"PROPFIND", float64(405), "GET", float64(200)}, "newest first, with how and the answer")

	// a calendar of one ward, of the entries of no ward, of one type group:
	// each its own name and suggested colour
	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	leevi := ""
	for _, wd := range out["space"].(map[string]any)["wards"].([]any) {
		if wd.(map[string]any)["name"] == "Leevi" {
			leevi = wd.(map[string]any)["id"].(string)
		}
	}
	_, wl := get(path+"?ward="+leevi, "")
	eq(t, []any{strings.Count(wl, "BEGIN:VEVENT"), strings.Contains(wl, "Leevin jumppa"), strings.Contains(wl, "X-WR-CALNAME:Kotona · Leevi"), strings.Contains(wl, "X-APPLE-CALENDAR-COLOR:#")},
		[]any{1, true, true, true}, "Leevi's calendar")
	_, none := get(path+"?ward=none", "")
	eq(t, []any{strings.Count(none, "BEGIN:VEVENT"), strings.Contains(none, "Lapset Terolla"), strings.Contains(none, "X-WR-CALNAME:Kotona · Yhteiset")}, []any{1, true, true}, "the shared calendar")
	_, grp := get(path+"?group=health", "")
	eq(t, []any{strings.Count(grp, "BEGIN:VEVENT"), strings.Contains(grp, "X-WR-CALNAME:Kotona · Terveys"), strings.Contains(grp, "X-APPLE-CALENDAR-COLOR:#16A34A")}, []any{0, true, true}, "the health calendar, empty")
	_, ev := get(path+"?group=event", "")
	eq(t, strings.Count(ev, "BEGIN:VEVENT"), 2, "plain events")

	// read back as an import reads it: the same days and times
	loc, _ := time.LoadLocation(wardenTZ)
	evs, _, err := icsToEvents(ics, loc)
	if err != nil {
		t.Fatal(err)
	}
	j := evs[0].(map[string]any)
	if j["title"] != "Leevin jumppa" {
		j = evs[1].(map[string]any)
	}
	eq(t, []any{j["date"], j["start"], j["end"], j["exdates"]}, []any{"2026-10-21", "17:00", "18:00", []any{"2026-11-04"}})

	// the time changes: the same UID, the new time
	wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "event_id": jumppa, "start": "17:30", "end": "18:30"})
	_, ics = get(path, "")
	if !strings.Contains(ics, "UID:"+jumppa+"@") || !strings.Contains(ics, "Helsinki:20261021T173000") {
		t.Errorf("moved: %s", ics)
	}

	// one entry, signed in
	code, one := get("/apps/warden/ics/"+sid+"/"+jumppa+".ics", "anna")
	eq(t, []any{code, strings.Count(one, "BEGIN:VEVENT"), strings.Contains(one, "UID:"+jumppa+"@")}, []any{200, 1, true}, "anna downloads one")
	code, _ = get("/apps/warden/ics/"+sid+"/"+jumppa+".ics", "mallory")
	eq(t, code, 404, "not mallory's")

	// anna leaves: the address she had stops working
	wardenCall(t, base, "tero", "remove_member", map[string]any{"space_id": sid, "member": "anna@example.com"})
	code, _ = get(path, "")
	eq(t, code, 404, "old address")
	code, out = wardenCall(t, base, "tero", "calendar_feed", map[string]any{"space_id": sid, "reset": true})
	eq(t, []any{code, out["url"] != feed}, []any{200, true}, "reset")
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

func TestWardenPlaces(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	looked := []string{}
	was := wardenGeocode
	wardenGeocode = func(_ context.Context, name string) (wardenPlace, bool, error) {
		looked = append(looked, name)
		if strings.EqualFold(name, "kuopio") {
			return wardenPlace{Name: "Kuopio", Lat: 62.89238, Lon: 27.67703}, true, nil
		}
		return wardenPlace{}, false, nil
	}
	defer func() { wardenGeocode = was }()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)
	eq(t, out["places"], []any{}, "no places at first")
	wardenCall(t, base, "tero", "add_ward", map[string]any{"space_id": sid, "name": "Aino"})

	code, out := wardenCall(t, base, "tero", "add_place", map[string]any{"space_id": sid, "place": "Espoo", "lat": 60.2055, "lon": 24.6559})
	eq(t, []any{code, out["places"]}, []any{200, []any{map[string]any{"name": "Espoo", "lat": 60.2055, "lon": 24.6559}}}, "the space's place, as given")
	code, out = wardenCall(t, base, "tero", "add_place", map[string]any{"space_id": sid, "place": "kuopio", "ward": "aino"})
	w := out["wards"].([]any)[0].(map[string]any)
	eq(t, []any{code, w["places"], looked}, []any{200, []any{map[string]any{"name": "Kuopio", "lat": 62.8924, "lon": 27.677}}, []string{"kuopio"}}, "a ward's, looked up by its name")
	code, _ = wardenCall(t, base, "tero", "add_place", map[string]any{"space_id": sid, "place": "Kuopio", "ward": "Aino"})
	eq(t, code, 400, "twice")
	code, _ = wardenCall(t, base, "tero", "add_place", map[string]any{"space_id": sid, "place": "Atlantis"})
	eq(t, code, 400, "not found")
	code, _ = wardenCall(t, base, "tero", "add_place", map[string]any{"space_id": sid, "place": "Oulu", "lat": 65.0})
	eq(t, code, 400, "lat without lon")
	code, _ = wardenCall(t, base, "tero", "add_place", map[string]any{"space_id": sid, "place": "Oulu", "ward": "Nobody"})
	eq(t, code, 400, "no such ward")

	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	sp := out["space"].(map[string]any)
	eq(t, len(sp["places"].([]any)), 1, "kept")
	code, out = wardenCall(t, base, "tero", "remove_place", map[string]any{"space_id": sid, "place": "KUOPIO", "ward": "Aino"})
	w = out["wards"].([]any)[0].(map[string]any)
	eq(t, []any{code, w["places"]}, []any{200, []any{}}, "removed")
	code, _ = wardenCall(t, base, "tero", "remove_place", map[string]any{"space_id": sid, "place": "Kuopio"})
	eq(t, code, 400, "not there")
}

// a timetable with different times on different weekdays: one VEVENT for
// each set of times, the child's holidays skipped
func TestWardenFeedTimetable(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)
	code, out := wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "school_timetable", "title": "Koulu", "date": "2026-08-12",
		"ward": "Leo", "rrule": "FREQ=WEEKLY;UNTIL=20270531", "fields": map[string]any{"times": "ma 8.15-13, ti 9-14.15, ke-pe 8:15-14"}})
	eq(t, code, 200, "timetable")
	id := out["event_id"].(string)
	code, _ = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "school_exception_day", "title": "Syysloma", "date": "2026-10-19", "end_date": "2026-10-23", "ward": "Leo"})
	eq(t, code, 200, "holiday")
	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	feed := out["space"].(map[string]any)["calendar_feed"].(string)
	_, ics := editorDo(t, "GET", base+feed[strings.Index(feed, "/apps/warden/cal/"):], "", "", "")
	for _, want := range []string{
		"UID:" + id + "-08151300@", "RRULE:FREQ=WEEKLY;BYDAY=MO;UNTIL=20270531T205959Z", "DTSTART;TZID=Europe/Helsinki:20260817T081500", "EXDATE;TZID=Europe/Helsinki:20261019T081500",
		"UID:" + id + "-09001415@", "RRULE:FREQ=WEEKLY;BYDAY=TU;", "DTSTART;TZID=Europe/Helsinki:20260818T090000", "EXDATE;TZID=Europe/Helsinki:20261020T090000",
		"UID:" + id + "-08151400@", "RRULE:FREQ=WEEKLY;BYDAY=WE,TH,FR;", "DTSTART;TZID=Europe/Helsinki:20260812T081500", "EXDATE;TZID=Europe/Helsinki:20261023T081500",
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("feed has no %q:\n%s", want, ics)
		}
	}
	eq(t, strings.Count(ics, "BEGIN:VEVENT"), 4, "three sets of times and the holiday")
	eq(t, strings.Contains(ics, "EXDATE;TZID=Europe/Helsinki:20261020T081500"), false, "a Tuesday is not skipped at Monday's time")
}

// the windows to get things in and the payment days are in the feed, each
// left out with ?get=0 or ?pay=0
func TestWardenFeedGetsPays(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)
	code, out := wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "Luistimet", "date": "2026-11-20",
		"fields": map[string]any{"get_from": "2026-11-01", "get_by": "2026-11-15"}, "cost": map[string]any{"amount": 100, "instalments": 3, "first": "2026-11-01"}})
	eq(t, code, 200, "saved: "+wardenErrOf(out))
	id := out["event_id"].(string)
	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	feed := out["space"].(map[string]any)["calendar_feed"].(string)
	path := feed[strings.Index(feed, "/apps/warden/cal/"):]
	_, ics := editorDo(t, "GET", base+path, "", "", "")
	for _, want := range []string{"UID:" + id + "-get@", "SUMMARY:Hanki: Luistimet", "DTSTART;VALUE=DATE:20261101", "DTEND;VALUE=DATE:20261116",
		"UID:" + id + "-pay0@", "SUMMARY:Maksu: Luistimet 33\\,33 €", "UID:" + id + "-pay2@", "SUMMARY:Maksu: Luistimet 33\\,34 €", "DTSTART;VALUE=DATE:20270101"} {
		if !strings.Contains(ics, want) {
			t.Errorf("feed has no %q:\n%s", want, ics)
		}
	}
	eq(t, strings.Count(ics, "BEGIN:VEVENT"), 5, "the entry, its window and three payments")
	_, ics = editorDo(t, "GET", base+path+"?get=0&pay=0", "", "", "")
	eq(t, strings.Count(ics, "BEGIN:VEVENT"), 1, "only the entry")
	_, ics = editorDo(t, "GET", base+path+"?pay=0", "", "", "")
	eq(t, strings.Count(ics, "BEGIN:VEVENT"), 2, "and its window")
}

func TestWardenFeedAlarms(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)
	code, out := wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "type": "birthday_invitation", "title": "Ellan synttärit",
		"date": "2027-03-20", "start": "14:00", "ward": "Leevi", "fields": map[string]any{"rsvp_by": "2027-03-10"}})
	eq(t, code, 200, "saved: "+wardenErrOf(out))
	code, out = wardenCall(t, base, "tero", "save_event", map[string]any{"space_id": sid, "title": "Luistimet", "date": "2027-11-20",
		"fields": map[string]any{"get_from": "2027-11-01", "get_by": "2027-11-15"}, "cost": map[string]any{"amount": 100, "instalments": 2, "first": "2027-11-01"}})
	eq(t, code, 200, "saved: "+wardenErrOf(out))
	_, out = wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	feed := out["space"].(map[string]any)["calendar_feed"].(string)
	path := feed[strings.Index(feed, "/apps/warden/cal/"):]
	_, ics := editorDo(t, "GET", base+path, "", "", "")
	for _, want := range []string{"TRIGGER:-P3D", "DESCRIPTION:Ellan synttärit", "TRIGGER;VALUE=DATE-TIME:20270309T070000Z",
		"DESCRIPTION:Vastattava viimeistään 10.3.: Ellan synttärit", "TRIGGER:PT9H", "DESCRIPTION:Hanki: Luistimet", "TRIGGER:-PT63H"} {
		if !strings.Contains(ics, want) {
			t.Errorf("feed has no %q:\n%s", want, ics)
		}
	}
	// the window and two payments one each, the invitation two
	eq(t, strings.Count(ics, "BEGIN:VALARM"), 5, "alarms")
	_, ics = editorDo(t, "GET", base+path+"?alarm=0", "", "", "")
	eq(t, strings.Count(ics, "BEGIN:VALARM"), 0, "none asked")
	loc, _ := time.LoadLocation(wardenTZ)
	evs, _, err := icsToEvents(ics, loc)
	eq(t, []any{err, len(evs)}, []any{nil, 5}, "the feed reads back")
}

func TestWardenMemberName(t *testing.T) {
	sp := &wardenSpace{doc: Doc{"members": []any{"u1", "u2", "u3"}, "people": map[string]any{
		"u1": map[string]any{"name": "Tero Tolonen"}, "u2": map[string]any{"name": "Mia Ek"}, "u3": map[string]any{"name": "Mia Lund"}}}}
	for in, want := range map[string]string{"Tero": "Tero Tolonen", "tero tolonen": "Tero Tolonen", "Mia": "Mia", "Anna": "Anna", "": ""} {
		eq(t, sp.memberName(in), want, in)
	}
}
