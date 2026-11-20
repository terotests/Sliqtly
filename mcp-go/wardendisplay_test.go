// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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
	wardenNearStops = func(_ context.Context, lat, lon float64, n int, max float64) ([]nysseStop, []float64, error) {
		l, d := []nysseStop{{ID: "0835", Name: "Hervannan kampus A"}, {ID: "0836", Name: "Hervannan kampus B"}, {ID: "0840", Name: "Insinöörinkatu"}}, []float64{120, 140, 600}
		return l[:min(n, 3)], d[:min(n, 3)], nil
	}
	defer func() {
		wardenWeather, wardenFindStops, wardenStopLines, wardenDepartures, wardenNearStops = openMeteoWeather, nysseFindStops, nysseStopLines, nysseDepartures, nysseNearStops
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
	tv := base + "/apps/warden/tv/"
	secret := "tablet-secret-0123456789abcdef0123456789"
	call := func(method, p, key, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, tv+p, strings.NewReader(body))
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res.StatusCode, string(b)
	}

	// the page holds nothing; its data needs a paired device
	code, page := call("GET", "", "", "")
	eq(t, []any{code, strings.Contains(page, "wakeLock"), strings.Contains(page, "Lapset")}, []any{200, true, false}, "the page is the same for everyone and keeps the screen on")
	code, _ = call("GET", "data", "", "")
	eq(t, code, 401, "no data without a device's secret")
	code, body := call("GET", "data", secret, "")
	eq(t, []any{code, strings.Contains(body, "unpaired")}, []any{401, true}, "nor with one not paired")
	code, _ = call("GET", "data", "short", "")
	eq(t, code, 401, "a secret is long")

	// pairing: the device asks for a code, a member enters it
	code, body = call("POST", "pair", secret, `{"name":"Eteisen tabletti"}`)
	var pr struct{ Code string }
	json.Unmarshal([]byte(body), &pr)
	eq(t, []any{code, len(pr.Code)}, []any{200, 6}, "a six-digit code")
	code, _ = wardenCall(t, base, "anna", "home_display", map[string]any{"space_id": sid, "pair_code": pr.Code})
	eq(t, code != 200, true, "a stranger pairs nothing")
	code, out = wardenCall(t, base, "tero", "home_display", map[string]any{"space_id": sid, "pair_code": "000000"})
	eq(t, code, 400, "a wrong code")
	code, out = wardenCall(t, base, "tero", "home_display", map[string]any{"space_id": sid, "pair_code": pr.Code[:3] + " " + pr.Code[3:],
		"stops": []any{map[string]any{"stop": "Keskustori", "lines": []any{"30", "8a"}}}})
	eq(t, code, 200, "the owner pairs it and sets a stop by its name")
	eq(t, out["paired"], "Eteisen tabletti", "with the name the device gave")
	eq(t, out["stops"], []any{map[string]any{"id": "0015", "name": "Keskustori D", "lines": []any{"30", "8A"}}}, "the stop found, the lines kept upper case")
	devs := out["devices"].([]any)
	eq(t, []any{len(devs), devs[0].(map[string]any)["name"], devs[0].(map[string]any)["paired_by"]}, []any{1, "Eteisen tabletti", "Tero"}, "the devices listed")
	code, _ = wardenCall(t, base, "tero", "home_display", map[string]any{"space_id": sid, "pair_code": pr.Code})
	eq(t, code, 400, "a code pairs once")
	_, sp := wardenCall(t, base, "tero", "get_space", map[string]any{"space_id": sid})
	eq(t, []any{sp["space"].(map[string]any)["home_display"], len(sp["space"].(map[string]any)["home_displays"].([]any))}, []any{"https://sliqtly.test/apps/warden/tv/", 1}, "get_space names the page and the devices")

	code, body = call("GET", "data", secret, "")
	eq(t, code, 200, "the paired device sees its space")
	var d struct {
		Name   string `json:"name"`
		Device string `json:"device"`
		Now    string `json:"now"`
		Days   []struct {
			Day   string           `json:"day"`
			Items []map[string]any `json:"items"`
		} `json:"days"`
		Weather map[string]any   `json:"weather"`
		Stops   []map[string]any `json:"stops"`
	}
	json.Unmarshal([]byte(body), &d)
	eq(t, []any{d.Name, d.Device, d.Now, len(d.Days), d.Days[0].Day, d.Days[6].Day}, []any{"Kotona", "Eteisen tabletti", "2026-11-20T09:30:00", 7, "2026-11-20", "2026-11-26"}, "seven days from today, in the space's time")
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
	code, body = call("GET", "stops?q=keskustori", secret, "")
	eq(t, []any{code, strings.Contains(body, `"lines":["3","8A","30"]`)}, []any{200, true}, "stops found with their lines")
	code, _ = call("GET", "stops?q=keskustori", "", "")
	eq(t, code, 401, "only for a paired device")
	code, body = call("POST", "stops", secret, `{"stops":[{"id":"0015","name":"Keskustori D","lines":[]}]}`)
	eq(t, []any{code, strings.Contains(body, `"lines":[]`)}, []any{200, true}, "the page sets the stops")
	code, _ = call("POST", "stops", secret, `{"stops":[{"id":"0015","lines":["3; drop"]}]}`)
	eq(t, code, 400, "a line is a line's number")

	// the tablet's place: the stops nearest to it, and with none picked the
	// display shows the two nearest
	code, body = call("GET", "stops?lat=61.4497&lon=23.8566", secret, "")
	eq(t, []any{code, strings.Count(body, `"m":`), strings.Contains(body, `"id":"0835","lines":["3","8A","30"],"m":120`)}, []any{200, 3, true}, "the nearest stops with their distance")
	call("POST", "stops", secret, `{"stops":[]}`)
	_, body = call("GET", "data?lat=61.4497&lon=23.8566", secret, "")
	json.Unmarshal([]byte(body), &d)
	eq(t, []any{len(d.Stops), d.Stops[0]["id"], d.Stops[0]["near"], d.Stops[0]["m"], d.Stops[1]["id"]}, []any{2, "0835", true, float64(120), "0836"}, "no stops picked: the two nearest")
	_, body = call("GET", "data", secret, "")
	d.Stops = nil
	json.Unmarshal([]byte(body), &d)
	eq(t, len(d.Stops), 0, "and none without the place")

	// unpairing: the device sees nothing more
	id := devs[0].(map[string]any)["device_id"].(string)
	code, out = wardenCall(t, base, "tero", "home_display", map[string]any{"space_id": sid, "remove_device": id})
	eq(t, []any{code, len(out["devices"].([]any))}, []any{200, 0}, "removed")
	code, _ = call("GET", "data", secret, "")
	eq(t, code, 401, "the removed device is unpaired")
}

func TestNysseDistance(t *testing.T) {
	// Keskustori to Hervannan kampus: about 7.4 km as the crow flies
	d := nysseDistance(61.4975, 23.7615, 61.4470, 23.8535)
	if d < 7200 || d > 7700 {
		t.Errorf("distance %f", d)
	}
}
