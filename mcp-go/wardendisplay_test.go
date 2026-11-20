// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// whether rules repeat on the days they should
func TestWardenOccurs(t *testing.T) {
	cases := []struct {
		o    icsOcc
		day  string
		want bool
	}{
		{icsOcc{day: "2026-11-20"}, "2026-11-20", true},
		{icsOcc{day: "2026-11-20"}, "2026-11-21", false},
		{icsOcc{day: "2026-11-20", lastDay: "2026-11-22"}, "2026-11-22", true},
		{icsOcc{day: "2026-10-19", rrule: "FREQ=WEEKLY;BYDAY=MO;UNTIL=20261218"}, "2026-11-23", true},
		{icsOcc{day: "2026-10-19", rrule: "FREQ=WEEKLY;BYDAY=MO;UNTIL=20261218"}, "2026-11-24", false},
		{icsOcc{day: "2026-10-19", rrule: "FREQ=WEEKLY;BYDAY=MO;UNTIL=20261218"}, "2026-12-21", false},
		{icsOcc{day: "2026-10-30", rrule: "FREQ=WEEKLY;UNTIL=20261204"}, "2026-11-20", true},
		{icsOcc{day: "2026-10-30", rrule: "FREQ=WEEKLY;UNTIL=20261204"}, "2026-11-19", false},
		{icsOcc{day: "2026-11-02", rrule: "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE"}, "2026-11-11", false},
		{icsOcc{day: "2026-11-02", rrule: "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE"}, "2026-11-18", true},
		{icsOcc{day: "2026-11-02", rrule: "FREQ=WEEKLY;BYDAY=MO", exdates: []string{"2026-11-16"}}, "2026-11-16", false},
		{icsOcc{day: "2026-11-01", rrule: "FREQ=DAILY;COUNT=3"}, "2026-11-03", true},
		{icsOcc{day: "2026-11-01", rrule: "FREQ=DAILY;COUNT=3"}, "2026-11-04", false},
		{icsOcc{day: "2026-01-15", rrule: "FREQ=MONTHLY"}, "2026-11-15", true},
		{icsOcc{day: "2026-01-01", rrule: "FREQ=MONTHLY;BYDAY=-1FR"}, "2026-11-27", true},
		{icsOcc{day: "2026-01-01", rrule: "FREQ=MONTHLY;BYDAY=1MO"}, "2026-11-02", true},
		{icsOcc{day: "2026-01-01", rrule: "FREQ=MONTHLY;BYDAY=1MO"}, "2026-11-09", false},
	}
	for _, c := range cases {
		if got := wardenOccurs(c.o, c.day); got != c.want {
			t.Errorf("%+v on %s: %v, want %v", c.o, c.day, got, c.want)
		}
	}
}

func TestWardenDisplay(t *testing.T) {
	env, base, stop := editorServer(t)
	defer stop()
	env.warden.now = func() time.Time { return time.Date(2026, 11, 20, 7, 30, 0, 0, time.UTC) } // 9.30 in Helsinki
	wardenWeather = func(context.Context, wardenPlace, string) (map[string]any, error) {
		return map[string]any{"now": map[string]any{"temp": -2.0, "code": 3}}, nil
	}
	wardenFindStops = func(_ context.Context, q string) ([]nysseStop, error) {
		if strings.Contains(strings.ToLower(q), "keskustori") || q == "0015" {
			return []nysseStop{{ID: "0015", Name: "Keskustori D"}}, nil
		}
		return nil, nil
	}
	wardenStopLines = func(context.Context, string) []string { return []string{"3", "8A", "30"} }
	wardenDepartures = func(_ context.Context, stop string, now time.Time) ([]nysseDeparture, error) {
		return []nysseDeparture{{Line: "30", To: "Pyynikintori", At: now.Add(4 * time.Minute), Live: true},
			{Line: "8A", To: "Atala", At: now.Add(6 * time.Minute), Live: true}, {Line: "3", To: "Hervanta", At: now.Add(9 * time.Minute)}}, nil
	}
	defer func() {
		wardenWeather, wardenFindStops, wardenStopLines, wardenDepartures = openMeteoWeather, nysseFindStops, nysseStopLines, nysseDepartures
	}()

	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Kotona"})
	sid := out["space_id"].(string)
	wardenCall(t, base, "tero", "import_events", map[string]any{"space_id": sid, "events": []any{
		map[string]any{"title": "Koulu", "date": "2026-10-19", "start": "08:15", "end": "13:15", "ward": "Iida", "ext_id": "k", "rrule": "FREQ=WEEKLY;BYDAY=FR;UNTIL=20261218"},
		map[string]any{"title": "Hammaslääkäri", "date": "2026-11-20", "start": "12:30", "ward": "Iida", "ext_id": "h", "important": true, "place": "Mekaniikanpolku 9"},
		map[string]any{"title": "Lapset Terolla", "date": "2026-11-19", "end_date": "2026-11-22", "ext_id": "l"},
		map[string]any{"title": "Ensi viikolla", "date": "2026-11-26", "ext_id": "e"},
		map[string]any{"title": "Liian kaukana", "date": "2026-11-27", "ext_id": "f"},
	}})
	code, out := wardenCall(t, base, "anna", "home_display", map[string]any{"space_id": sid})
	eq(t, code != 200, true, "a stranger gets nothing")
	code, out = wardenCall(t, base, "tero", "home_display", map[string]any{"space_id": sid, "stops": []any{map[string]any{"stop": "Keskustori", "lines": []any{"30", "8a"}}}})
	eq(t, code, 200, "the owner sets a stop by its name")
	u := out["url"].(string)
	eq(t, out["stops"], []any{map[string]any{"id": "0015", "name": "Keskustori D", "lines": []any{"30", "8A"}}}, "the stop found, the lines kept upper case")
	path := u[strings.Index(u, "/apps/warden/tv/"):]
	_, sp := wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	eq(t, sp["space"].(map[string]any)["home_display"], u, "get_space names the display")

	res, page := editorDo(t, "GET", base+path, "", "", "")
	eq(t, []any{res.StatusCode, strings.Contains(page, "wakeLock")}, []any{200, true}, "the page needs no sign-in and keeps the screen on")
	res, body := editorDo(t, "GET", base+path+"/data", "", "", "")
	eq(t, res.StatusCode, 200, "its data")
	var d struct {
		Name string `json:"name"`
		Now  string `json:"now"`
		Days []struct {
			Day   string           `json:"day"`
			Items []map[string]any `json:"items"`
		} `json:"days"`
		Weather map[string]any   `json:"weather"`
		Stops   []map[string]any `json:"stops"`
	}
	json.Unmarshal([]byte(body), &d)
	eq(t, []any{d.Name, d.Now, len(d.Days), d.Days[0].Day, d.Days[6].Day}, []any{"Kotona", "2026-11-20T09:30:00", 7, "2026-11-20", "2026-11-26"}, "seven days from today, in the space's time")
	titles := func(i int) []any {
		l := []any{}
		for _, it := range d.Days[i].Items {
			l = append(l, it["title"].(string)+map[bool]string{true: " once", false: " weekly"}[it["once"] == true])
		}
		return l
	}
	eq(t, titles(0), []any{"Lapset Terolla once", "Koulu weekly", "Hammaslääkäri once"}, "today: all-day first, then by time; repeating ones told apart")
	eq(t, titles(3), []any{}, "Monday has nothing")
	eq(t, titles(6), []any{"Ensi viikolla once"}, "the seventh day is the last")
	h := d.Days[0].Items[2]
	eq(t, []any{h["ward"], h["important"], h["place"], h["start"]}, []any{"Iida", true, "Mekaniikanpolku 9", "12:30"}, "with its ward, place and mark")
	l := d.Days[0].Items[0]
	eq(t, []any{l["from"], l["until"]}, []any{"2026-11-19", "2026-11-22"}, "a plan over days says which")
	eq(t, d.Weather["place"], "Tampere", "Tampere's weather when the space has no place")
	deps := d.Stops[0]["departures"].([]any)
	eq(t, len(deps), 2, "only the lines picked")
	eq(t, deps[0], map[string]any{"line": "30", "to": "Pyynikintori", "at": "09:34", "min": float64(4), "live": true}, "the next one first, in the space's time")

	// the display's own picker
	res, body = editorDo(t, "GET", base+path+"/stops?q=keskustori", "", "", "")
	eq(t, []any{res.StatusCode, strings.Contains(body, `"lines":["3","8A","30"]`)}, []any{200, true}, "stops found with their lines")
	res, body = editorDo(t, "POST", base+path+"/stops", "", "", `{"stops":[{"id":"0015","name":"Keskustori D","lines":[]}]}`)
	eq(t, []any{res.StatusCode, strings.Contains(body, `"lines":[]`)}, []any{200, true}, "the page sets the stops")
	res, _ = editorDo(t, "POST", base+path+"/stops", "", "", `{"stops":[{"id":"0015","lines":["3; drop"]}]}`)
	eq(t, res.StatusCode, 400, "a line is a line's number")

	// a new address: the old one stops working
	code, _ = wardenCall(t, base, "anna", "home_display", map[string]any{"space_id": sid, "reset": true})
	eq(t, code != 200, true, "only members, and only the owner resets")
	_, out = wardenCall(t, base, "tero", "home_display", map[string]any{"space_id": sid, "reset": true})
	eq(t, out["url"] != u, true, "a new address")
	res, _ = editorDo(t, "GET", base+path+"/data", "", "", "")
	eq(t, res.StatusCode, 404, "the old one is gone")
	res, _ = editorDo(t, "GET", base+"/apps/warden/tv/short", "", "", "")
	eq(t, res.StatusCode, 404, "a made-up address is nothing")
}
