// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's home display: a page for a tablet that stays on at home (in a
// browser, or in the Warden Android app, android/warden, which keeps the
// screen on while the tablet charges and offers itself as its screensaver).
// It shows the clock, today and the next six days (one-off things large,
// what repeats every week small), the weather, and the next departures from
// the Nysse (Tampere's buses and trams) stops the family picked.
//
// The page itself holds nothing: what it shows comes only to a paired device.
// The device (the app, or a browser) makes a secret of its own and sends it
// with every call as `Authorization: Bearer <secret>`, over HTTPS. Unpaired,
// it asks for a pairing code, shows it, and a signed-in member enters the
// code in Warden (home_display pair_code). The server keeps only the
// secret's SHA-256: warden_tvdevices/{hash} {space, name, by, at, seen}; a
// member removes a device there (remove_device) and it shows nothing more.
//
//	GET  /apps/warden/tv/          the page (assets/warden-display.html)
//	POST /apps/warden/tv/pair      a pairing code for the device {code, expires}
//	GET  /apps/warden/tv/data      what it shows, as JSON (?lat=&lon=: the
//	                               tablet's place, for the nearest stops)
//	GET  /apps/warden/tv/stops?q=  Nysse stops by name, or ?lat=&lon=
//	POST /apps/warden/tv/stops     the stops shown {stops: [{id, lines}]}
//
// The space document keeps the stops (`display_stops`: [{id, name, lines}]).

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	wardenDisplayPath = wardenPath + "/tv/"
	wardenTVDevices   = "warden_tvdevices"
	wardenTVPairs     = "warden_tvpairs"
	wardenMaxStops    = 6 // stops on the display
	wardenMaxLines    = 12
	wardenMaxDevices  = 10 // paired devices of a space
	wardenDisplayDays = 7
	wardenPairTTL     = 15 * time.Minute
)

// The Android app: built by .github/workflows/warden-android.yml from
// android/warden and kept as a release asset of the (public) repository.
const wardenAppURL = "https://github.com/terotests/Sliqtly/releases/download/warden-android-latest/Warden.apk"

//go:embed assets/warden-display.html
var wardenDisplayHTML string

var (
	wardenStopRe   = regexp.MustCompile(`^[0-9A-Za-z]{1,8}$`)
	wardenLineRe   = regexp.MustCompile(`^[0-9A-Za-z]{1,6}$`)
	wardenSecretRe = regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)
	wardenCodeRe   = regexp.MustCompile(`^[0-9]{6}$`)
	// pairing codes asked for per address in ten minutes
	wardenPairLimit = rateLimiter(20, 10*time.Minute)
)

// the display page's address (the same for every space: the device's
// pairing says whose it is)
func (s *wardenService) displayURL() string { return s.base + wardenDisplayPath }

// the stops the display shows
func wardenDisplayStops(d Doc) []map[string]any {
	out := []map[string]any{}
	for _, m := range docMaps(d, "display_stops") {
		out = append(out, map[string]any{"id": docStr(m, "id"), "name": docStr(m, "name"), "lines": anys(docStrs(m, "lines"))})
	}
	return out
}

// a device secret's key: its SHA-256, the only thing kept of it
func wardenSecretHash(secret string) string {
	h := sha256.Sum256([]byte("warden-tv\x00" + secret))
	return hex.EncodeToString(h[:])
}

// the space's paired devices, oldest first
func (s *wardenService) displayDevices(ctx context.Context, sp *wardenSpace) ([]any, error) {
	docs, ids, err := s.db.WhereEq(ctx, wardenTVDevices, "space", sp.id)
	if err != nil {
		return nil, err
	}
	type dev struct {
		at int64
		m  map[string]any
	}
	l := []dev{}
	for i, d := range docs {
		at, _ := d["at"].(int64)
		if f, ok := d["at"].(float64); ok {
			at = int64(f)
		}
		m := map[string]any{"device_id": ids[i][:12], "name": docStr(d, "name"), "paired_by": docStr(d, "by"), "paired": time.UnixMilli(at).UTC().Format("2006-01-02")}
		if seen, ok := d["seen"].(string); ok {
			m["seen"] = seen
		}
		l = append(l, dev{at, m})
	}
	sort.Slice(l, func(a, b int) bool { return l[a].at < l[b].at })
	out := []any{}
	for _, d := range l {
		out = append(out, d.m)
	}
	return out, nil
}

// Sets the stops from [{id or stop, name?, lines?}]: each id a Nysse stop's
// code (0001) or a name to look up (the first stop found by it).
func (s *wardenService) setDisplayStops(ctx context.Context, sp *wardenSpace, list []any) error {
	if len(list) > wardenMaxStops {
		return roomErr{fmt.Sprintf("at most %d stops", wardenMaxStops)}
	}
	out := []any{}
	for _, e := range list {
		m, _ := e.(map[string]any)
		if m == nil {
			return roomErr{"each stop is an object {stop, lines}"}
		}
		id := strings.TrimSpace(argStr(m, "stop"))
		if id == "" {
			id = strings.TrimSpace(argStr(m, "id"))
		}
		name := strings.TrimSpace(argStr(m, "name"))
		if !wardenStopRe.MatchString(id) || !strings.ContainsAny(id, "0123456789") {
			found, err := wardenFindStops(ctx, id)
			if err != nil {
				return err
			}
			if len(found) == 0 {
				return roomErr{"no Nysse stop called " + strconv.Quote(id)}
			}
			id, name = found[0].ID, found[0].Name
		}
		if name == "" {
			if found, err := wardenFindStops(ctx, id); err == nil {
				for _, f := range found {
					if f.ID == id {
						name = f.Name
					}
				}
			}
		}
		lines := []any{}
		for _, l := range wardenArgStrings(m["lines"]) {
			l = strings.ToUpper(strings.TrimSpace(l))
			if l == "" {
				continue
			}
			if !wardenLineRe.MatchString(l) {
				return roomErr{"a line is its number, like 3 or 8B"}
			}
			if !slices.Contains(lines, any(l)) && len(lines) < wardenMaxLines {
				lines = append(lines, l)
			}
		}
		if len([]rune(name)) > 60 {
			name = string([]rune(name)[:60])
		}
		out = append(out, map[string]any{"id": id, "name": name, "lines": lines})
	}
	sp.doc["display_stops"] = out
	return s.save(ctx, sp)
}

// strings from a JSON array, or one string with commas
func wardenArgStrings(v any) []string {
	switch x := v.(type) {
	case []any:
		out := []string{}
		for _, e := range x {
			switch y := e.(type) {
			case string:
				out = append(out, y)
			case float64:
				out = append(out, strconv.FormatFloat(y, 'f', -1, 64))
			}
		}
		return out
	case string:
		return strings.Split(x, ",")
	}
	return nil
}

// home_display: the display's page and the app's address, the paired
// devices and the stops; pair_code pairs the device showing it,
// remove_device unpairs one, stops sets them, find_stop looks stops up
func (s *wardenService) homeDisplay(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	out := map[string]any{"url": s.displayURL(), "app": wardenAppURL,
		"how": "Open url on the tablet (or install the Warden Android app, app: an APK, which opens it full screen, keeps the screen on while charging and can be the tablet's screensaver). The tablet shows a six-digit pairing code; give it here as pair_code and the tablet shows the space: today and the coming week, the weather and the next departures of the stops. Only paired devices see anything; remove_device unpairs one."}
	if code := strings.TrimSpace(argStr(a, "pair_code")); code != "" {
		name, err := s.pairDisplay(ctx, sp, code, strings.TrimSpace(argStr(a, "name")))
		if err != nil {
			return nil, err
		}
		out["paired"] = name
	}
	if id := strings.TrimSpace(argStr(a, "remove_device")); id != "" {
		docs, ids, err := s.db.WhereEq(ctx, wardenTVDevices, "space", sp.id)
		if err != nil {
			return nil, err
		}
		gone := false
		for i := range docs {
			if strings.HasPrefix(ids[i], id) && len(id) >= 6 {
				if err := s.db.Delete(ctx, wardenTVDevices, ids[i]); err != nil {
					return nil, err
				}
				gone = true
			}
		}
		if !gone {
			return nil, roomErr{"no such device: home_display lists them (device_id)"}
		}
		out["removed"] = id
	}
	if l, ok := a["stops"].([]any); ok {
		if err := s.setDisplayStops(ctx, sp, l); err != nil {
			return nil, err
		}
	}
	devs, err := s.displayDevices(ctx, sp)
	if err != nil {
		return nil, err
	}
	out["devices"], out["stops"] = devs, anys(wardenDisplayStops(sp.doc))
	if q := strings.TrimSpace(argStr(a, "find_stop")); q != "" {
		found, err := wardenFindStops(ctx, q)
		if err != nil {
			return nil, err
		}
		l := []any{}
		for _, f := range found {
			l = append(l, map[string]any{"stop": f.ID, "name": f.Name, "lat": f.Lat, "lon": f.Lon})
		}
		out["found"] = l
	}
	return out, nil
}

// pairs the device whose code this is with the space: its name
func (s *wardenService) pairDisplay(ctx context.Context, sp *wardenSpace, code, name string) (string, error) {
	code = strings.ReplaceAll(code, " ", "")
	if !wardenCodeRe.MatchString(code) {
		return "", roomErr{"the pairing code is the six digits the display shows"}
	}
	p, err := s.db.Get(ctx, wardenTVPairs, code)
	if err != nil {
		return "", err
	}
	exp, _ := p["expires"].(int64)
	if f, ok := p["expires"].(float64); ok {
		exp = int64(f)
	}
	if p == nil || s.now().UnixMilli() > exp {
		return "", roomErr{"no such pairing code, or it is too old: the display shows a new one"}
	}
	devs, err := s.displayDevices(ctx, sp)
	if err != nil {
		return "", err
	}
	if len(devs) >= wardenMaxDevices {
		return "", roomErr{fmt.Sprintf("a space has at most %d displays: remove one first", wardenMaxDevices)}
	}
	if name == "" {
		name = docStr(p, "name")
	}
	if name == "" {
		name = "Kotinäyttö"
	}
	if len([]rune(name)) > 40 {
		name = string([]rune(name)[:40])
	}
	hash := docStr(p, "hash")
	if err := s.db.Set(ctx, wardenTVDevices, hash, Doc{"space": sp.id, "name": name, "by": sp.who.name, "uid": sp.who.uid, "at": s.now().UnixMilli()}); err != nil {
		return "", err
	}
	s.db.Delete(ctx, wardenTVPairs, code)
	return name, nil
}

// a six-digit code, for a device to be paired with
func wardenPairCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("%06d", n.Int64())
}

// ------------------------------------------------------------------- HTTP --

// the device's secret, from Authorization: Bearer
func wardenBearer(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) < 8 || !strings.EqualFold(h[:7], "bearer ") {
		return ""
	}
	if t := strings.TrimSpace(h[7:]); wardenSecretRe.MatchString(t) {
		return t
	}
	return ""
}

func wardenClientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		return strings.TrimSpace(strings.Split(f, ",")[0])
	}
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func serveWardenDisplay(env *Env, w http.ResponseWriter, r *http.Request, p string) {
	sub := strings.Trim(strings.TrimPrefix(p, wardenDisplayPath), "/")
	if sub == "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Robots-Tag", "noindex")
		w.Header().Set("Referrer-Policy", "no-referrer")
		io.WriteString(w, wardenDisplayHTML)
		return
	}
	secret := wardenBearer(r)
	if secret == "" {
		editorJSON(w, 401, map[string]any{"error": "This device is not paired.", "code": "unpaired"})
		return
	}
	hash := wardenSecretHash(secret)
	ctx := r.Context()
	if sub == "pair" && r.Method == http.MethodPost {
		if why := wardenPairLimit(wardenClientIP(r)); why != "" {
			editorJSON(w, 429, map[string]string{"error": "Too many requests; try again in a few minutes."})
			return
		}
		var in struct {
			Name string `json:"name"`
		}
		json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in)
		code := wardenPairCode()
		exp := env.warden.now().Add(wardenPairTTL)
		if err := env.DB.Set(ctx, wardenTVPairs, code, Doc{"hash": hash, "name": strings.TrimSpace(in.Name), "expires": exp.UnixMilli()}); err != nil {
			editorJSON(w, 503, map[string]string{"error": "Try again in a moment."})
			return
		}
		editorJSON(w, 200, map[string]any{"code": code, "expires": exp.UTC().Format(time.RFC3339)})
		return
	}
	if why := wardenLimit("tv:" + hash); why != "" {
		editorJSON(w, 429, map[string]string{"error": "Too many requests"})
		return
	}
	dev, err := env.DB.Get(ctx, wardenTVDevices, hash)
	if err != nil {
		editorJSON(w, 503, map[string]string{"error": "The display could not be reached"})
		return
	}
	if dev == nil {
		editorJSON(w, 401, map[string]any{"error": "This device is not paired.", "code": "unpaired"})
		return
	}
	sd, err := env.DB.Get(ctx, wardenSpaces, docStr(dev, "space"))
	if err != nil || sd == nil {
		editorJSON(w, 503, map[string]string{"error": "The space could not be reached"})
		return
	}
	sp := &wardenSpace{id: docStr(dev, "space"), doc: sd}
	switch {
	case sub == "data" && r.Method == http.MethodGet:
		out, err := env.warden.displayData(ctx, sp, wardenLatLon(r))
		if err != nil {
			editorJSON(w, 503, map[string]string{"error": "The space could not be reached"})
			return
		}
		out["device"] = docStr(dev, "name")
		// when it was last seen, kept a minute apart
		now := env.warden.now().UTC().Format("2006-01-02T15:04")
		if docStr(dev, "seen") != now {
			env.DB.Update(ctx, wardenTVDevices, hash, Doc{"seen": now})
		}
		editorJSON(w, 200, out)
	case sub == "stops" && r.Method == http.MethodGet:
		// by name, or the nearest to the tablet (?lat=&lon=)
		var found []nysseStop
		var dist []float64
		var err error
		if at := wardenLatLon(r); at != nil {
			found, dist, err = wardenNearStops(ctx, at[0], at[1], 6, 1500)
		} else {
			found, err = wardenFindStops(ctx, r.URL.Query().Get("q"))
		}
		if err != nil {
			editorJSON(w, 503, map[string]string{"error": "Nysse's stops could not be reached"})
			return
		}
		l := []any{}
		for i, f := range found {
			m := map[string]any{"id": f.ID, "name": f.Name, "lines": anys(wardenStopLines(ctx, f.ID))}
			if i < len(dist) {
				m["m"] = int(dist[i])
			}
			l = append(l, m)
		}
		editorJSON(w, 200, map[string]any{"stops": l})
	case sub == "stops" && r.Method == http.MethodPost:
		var in struct {
			Stops []any `json:"stops"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
			editorJSON(w, 400, map[string]string{"error": "A JSON object is expected."})
			return
		}
		if err := env.warden.setDisplayStops(ctx, sp, in.Stops); err != nil {
			code := 503
			if _, ok := err.(roomErr); ok {
				code = 400
			}
			editorJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		editorJSON(w, 200, map[string]any{"stops": anys(wardenDisplayStops(sp.doc))})
	default:
		http.NotFound(w, r)
	}
}

// ------------------------------------------------------------------- data --

// the tablet's place, ?lat=&lon= (from its last known location), or nil
func wardenLatLon(r *http.Request) []float64 {
	q := r.URL.Query()
	lat, err1 := strconv.ParseFloat(q.Get("lat"), 64)
	lon, err2 := strconv.ParseFloat(q.Get("lon"), 64)
	if err1 != nil || err2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return nil
	}
	return []float64{lat, lon}
}

func (sp *wardenSpace) loc() *time.Location {
	tz := docStr(sp.doc, "tz")
	if tz == "" {
		tz = wardenTZ
	}
	l, err := time.LoadLocation(tz)
	if err != nil {
		l, _ = time.LoadLocation(wardenTZ)
	}
	return l
}

// what the display shows: the days, the weather, the departures
// at: the tablet's place when it is known: with no stops picked, the
// departures of the two stops nearest to it
func (s *wardenService) displayData(ctx context.Context, sp *wardenSpace, at []float64) (map[string]any, error) {
	loc := sp.loc()
	now := s.now().In(loc)
	docs, _, err := s.events(ctx, sp)
	if err != nil {
		return nil, err
	}
	wards := map[string]map[string]any{}
	for _, w := range docMaps(sp.doc, "wards") {
		wards[docStr(w, "id")] = w
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := []any{}
	for i := 0; i < wardenDisplayDays; i++ {
		day := today.AddDate(0, 0, i)
		days = append(days, map[string]any{"day": day.Format("2006-01-02"), "items": wardenDayItems(docs, day, wards)})
	}
	lang := docStr(sp.doc, "lang")
	if lang == "" {
		lang = "fi"
	}
	out := map[string]any{
		"name": docStr(sp.doc, "name"), "lang": lang, "now": now.Format("2006-01-02T15:04:05"),
		"days": days, "app": wardenAppURL,
	}
	// the weather of the space's first place, Tampere's when it has none
	place := wardenPlace{Name: "Tampere", Lat: 61.4978, Lon: 23.7610}
	if ps := docMaps(sp.doc, "places"); len(ps) > 0 {
		lat, ok1 := ps[0]["lat"].(float64)
		lon, ok2 := ps[0]["lon"].(float64)
		if ok1 && ok2 {
			place = wardenPlace{Name: docStr(ps[0], "name"), Lat: lat, Lon: lon}
		}
	}
	if wx, err := wardenWeather(ctx, place, docStr(sp.doc, "tz")); err == nil {
		wx["place"] = place.Name
		out["weather"] = wx
	}
	stops := []any{}
	picked := wardenDisplayStops(sp.doc)
	near := map[string]int{}
	if len(picked) == 0 && at != nil {
		if found, dist, err := wardenNearStops(ctx, at[0], at[1], 2, 800); err == nil {
			for i, f := range found {
				picked = append(picked, map[string]any{"id": f.ID, "name": f.Name, "lines": []any{}})
				near[f.ID] = int(dist[i])
			}
		}
	}
	for _, st := range picked {
		id := st["id"].(string)
		lines := []string{}
		for _, l := range st["lines"].([]any) {
			lines = append(lines, l.(string))
		}
		row := map[string]any{"id": id, "name": st["name"], "lines": st["lines"]}
		if m, ok := near[id]; ok {
			row["near"], row["m"] = true, m
		}
		deps, err := wardenDepartures(ctx, id, now)
		if err != nil {
			row["error"] = true
			deps = nil
		}
		l := []any{}
		for _, d := range deps {
			if len(lines) > 0 && !slices.Contains(lines, strings.ToUpper(d.Line)) {
				continue
			}
			if len(l) >= 8 {
				break
			}
			mins := int(d.At.Sub(now).Minutes())
			l = append(l, map[string]any{"line": d.Line, "to": d.To, "at": d.At.In(loc).Format("15:04"), "min": mins, "live": d.Live})
		}
		row["departures"] = l
		stops = append(stops, row)
	}
	out["stops"] = stops
	return out, nil
}

// the entries of a day, for the display: by time, all-day ones first
func wardenDayItems(docs []Doc, day time.Time, wards map[string]map[string]any) []any {
	ds := day.Format("2006-01-02")
	type item struct {
		key string
		m   map[string]any
	}
	items := []item{}
	for _, d := range docs {
		if docStr(d, "date") == "" || wardenGroupOf(docStr(d, "type")) == "question" {
			continue
		}
		f, _ := d["fields"].(map[string]any)
		if st := docStr(f, "status"); st == "done" || st == "cancelled" || st == "archived" {
			continue
		}
		for _, o := range icsOccurrences(d, docs) {
			if !wardenOccurs(o, ds) {
				continue
			}
			repeats := o.rrule != ""
			m := map[string]any{"title": docStr(d, "title"), "group": wardenGroupOf(docStr(d, "type")), "once": !repeats}
			if o.start != "" {
				m["start"] = o.start
				if o.end != "" {
					m["end"] = o.end
				}
			}
			if p := docStr(d, "place"); p != "" {
				m["place"] = p
			}
			if b, _ := d["important"].(bool); b {
				m["important"] = true
			}
			if c := docStr(d, "color"); c != "" {
				m["color"] = c
			}
			if w := wards[docStr(d, "ward")]; w != nil {
				m["ward"] = docStr(w, "name")
				m["ward_color"] = docStr(w, "color")
			}
			// a plan over many days: which of them this is
			if !repeats && o.lastDay != "" && o.lastDay != o.day {
				m["from"], m["until"] = o.day, o.lastDay
			}
			for _, x := range docMaps(d, "who") {
				if n := docStr(x, "name"); n != "" {
					who, _ := m["who"].([]any)
					m["who"] = append(who, strings.TrimSpace(n+" "+docStr(x, "role")))
				}
			}
			k := "1"
			if o.start == "" {
				k = "0"
			}
			items = append(items, item{k + o.start + docStr(d, "title"), m})
		}
	}
	sort.SliceStable(items, func(a, b int) bool { return items[a].key < items[b].key })
	out := []any{}
	for _, it := range items {
		out = append(out, it.m)
	}
	return out
}

// whether an occurrence (icsOccurrences) falls on the day: its own days,
// or a day its rule repeats it on (FREQ DAILY, WEEKLY, MONTHLY, YEARLY with
// INTERVAL, BYDAY, UNTIL, COUNT)
func wardenOccurs(o icsOcc, ds string) bool {
	if o.day == "" || ds < o.day || slices.Contains(o.exdates, ds) {
		return false
	}
	if o.rrule == "" {
		last := o.lastDay
		if last == "" {
			last = o.day
		}
		return ds <= last
	}
	rule := map[string]string{}
	for _, p := range strings.Split(o.rrule, ";") {
		if k, v, ok := strings.Cut(p, "="); ok {
			rule[strings.ToUpper(k)] = strings.ToUpper(v)
		}
	}
	if u := rule["UNTIL"]; len(u) >= 8 {
		if ds > u[:4]+"-"+u[4:6]+"-"+u[6:8] {
			return false
		}
	}
	start, err1 := time.Parse("2006-01-02", o.day)
	day, err2 := time.Parse("2006-01-02", ds)
	if err1 != nil || err2 != nil {
		return false
	}
	if n, err := strconv.Atoi(rule["COUNT"]); err == nil && n > 0 {
		// the n first occurrences only: count them up to the day
		c := 0
		for d := start; !d.After(day); d = d.AddDate(0, 0, 1) {
			if wardenRuleDay(rule, start, d) {
				c++
			}
			if c > n || d.Sub(start) > 4*366*24*time.Hour {
				return false
			}
		}
		return wardenRuleDay(rule, start, day)
	}
	return wardenRuleDay(rule, start, day)
}

func wardenRuleDay(rule map[string]string, start, day time.Time) bool {
	iv, err := strconv.Atoi(rule["INTERVAL"])
	if err != nil || iv < 1 {
		iv = 1
	}
	wd := func(t time.Time) int { return (int(t.Weekday()) + 6) % 7 } // Monday 0
	// BYDAY: the weekdays, each with its place in the month (1MO, -1FR) or not
	type byday struct{ n, d int }
	bys := []byday{}
	for _, b := range strings.Split(rule["BYDAY"], ",") {
		if len(b) < 2 {
			continue
		}
		code := b[len(b)-2:]
		i := slices.Index(wardenDayCodes, code)
		if i < 0 {
			continue
		}
		n, _ := strconv.Atoi(b[:len(b)-2])
		bys = append(bys, byday{n, i})
	}
	onDay := func() bool {
		if len(bys) == 0 {
			return true
		}
		for _, b := range bys {
			if b.d != wd(day) {
				continue
			}
			if b.n == 0 {
				return true
			}
			// the n:th such weekday of the month (from its end when negative)
			if b.n > 0 && (day.Day()-1)/7+1 == b.n {
				return true
			}
			last := time.Date(day.Year(), day.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
			if b.n < 0 && (last-day.Day())/7+1 == -b.n {
				return true
			}
		}
		return false
	}
	switch rule["FREQ"] {
	case "DAILY":
		return int(day.Sub(start).Hours()/24+0.5)%iv == 0 && onDay()
	case "WEEKLY":
		monday := func(t time.Time) time.Time { return t.AddDate(0, 0, -wd(t)) }
		weeks := int(monday(day).Sub(monday(start)).Hours()/24+0.5) / 7
		if weeks%iv != 0 {
			return false
		}
		if len(bys) == 0 {
			return wd(day) == wd(start)
		}
		return onDay()
	case "MONTHLY":
		months := (day.Year()*12 + int(day.Month())) - (start.Year()*12 + int(start.Month()))
		if months%iv != 0 {
			return false
		}
		if len(bys) == 0 {
			return day.Day() == start.Day()
		}
		return onDay()
	case "YEARLY":
		return (day.Year()-start.Year())%iv == 0 && day.Month() == start.Month() && day.Day() == start.Day()
	}
	return false
}

// ---------------------------------------------------------------- weather --

var (
	wardenWxMu    sync.Mutex
	wardenWxCache = map[string]wardenCached{}
)

type wardenCached struct {
	at  time.Time
	val map[string]any
}

// the weather now, for the next hours and the coming days, from Open-Meteo
// (kept for ten minutes); tests replace it
var wardenWeather = openMeteoWeather

func openMeteoWeather(ctx context.Context, p wardenPlace, tz string) (map[string]any, error) {
	if tz == "" {
		tz = wardenTZ
	}
	key := fmt.Sprintf("%.3f,%.3f", p.Lat, p.Lon)
	wardenWxMu.Lock()
	c, ok := wardenWxCache[key]
	wardenWxMu.Unlock()
	if ok && time.Since(c.at) < 10*time.Minute {
		return c.val, nil
	}
	q := url.Values{
		"latitude": {strconv.FormatFloat(p.Lat, 'f', 4, 64)}, "longitude": {strconv.FormatFloat(p.Lon, 'f', 4, 64)},
		"current":  {"temperature_2m,weather_code,wind_speed_10m,precipitation"},
		"hourly":   {"temperature_2m,weather_code,precipitation_probability"},
		"daily":    {"weather_code,temperature_2m_max,temperature_2m_min,precipitation_sum"},
		"timezone": {tz}, "forecast_days": {"7"}, "wind_speed_unit": {"ms"},
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.open-meteo.com/v1/forecast?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	res, err := wardenGeoClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("weather: %s", res.Status)
	}
	var w struct {
		Current struct {
			Time   string  `json:"time"`
			Temp   float64 `json:"temperature_2m"`
			Code   int     `json:"weather_code"`
			Wind   float64 `json:"wind_speed_10m"`
			Precip float64 `json:"precipitation"`
		} `json:"current"`
		Hourly struct {
			Time []string  `json:"time"`
			Temp []float64 `json:"temperature_2m"`
			Code []int     `json:"weather_code"`
			Prob []float64 `json:"precipitation_probability"`
		} `json:"hourly"`
		Daily struct {
			Time   []string  `json:"time"`
			Code   []int     `json:"weather_code"`
			Max    []float64 `json:"temperature_2m_max"`
			Min    []float64 `json:"temperature_2m_min"`
			Precip []float64 `json:"precipitation_sum"`
		} `json:"daily"`
	}
	if err := json.NewDecoder(res.Body).Decode(&w); err != nil {
		return nil, err
	}
	hours := []any{}
	for i, t := range w.Hourly.Time {
		if t < w.Current.Time[:min(len(w.Current.Time), 13)] || len(hours) >= 12 || i >= len(w.Hourly.Temp) || i >= len(w.Hourly.Code) {
			continue
		}
		h := map[string]any{"time": t[11:], "temp": w.Hourly.Temp[i], "code": w.Hourly.Code[i]}
		if i < len(w.Hourly.Prob) {
			h["rain"] = w.Hourly.Prob[i]
		}
		hours = append(hours, h)
	}
	days := []any{}
	for i, t := range w.Daily.Time {
		if i >= len(w.Daily.Code) || i >= len(w.Daily.Max) || i >= len(w.Daily.Min) {
			break
		}
		d := map[string]any{"day": t, "code": w.Daily.Code[i], "max": w.Daily.Max[i], "min": w.Daily.Min[i]}
		if i < len(w.Daily.Precip) {
			d["rain"] = w.Daily.Precip[i]
		}
		days = append(days, d)
	}
	val := map[string]any{"now": map[string]any{"temp": w.Current.Temp, "code": w.Current.Code, "wind": w.Current.Wind, "rain": w.Current.Precip}, "hours": hours, "days": days}
	wardenWxMu.Lock()
	wardenWxCache[key] = wardenCached{time.Now(), val}
	wardenWxMu.Unlock()
	return val, nil
}
