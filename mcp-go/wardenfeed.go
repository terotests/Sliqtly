// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's calendar out as iCalendar (wardenics.go brings one in):
//
//	GET /apps/warden/cal/<token>.ics            the space's dated entries, a
//	                                            feed a calendar app subscribes
//	                                            to (webcal://); the token is
//	                                            the space's secret, no sign-in
//	GET /apps/warden/ics/<space>/<event>.ics    one entry, to download; signed
//	                                            in with the page's cookie
//
// Each entry keeps its UID (its id here), so a calendar that reads the feed
// again, or opens the same entry's file again, moves the event it has
// instead of adding another. Times are the space's own, with its VTIMEZONE;
// a repeating event keeps its RRULE and the days it skips.

package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const wardenFeedPath = wardenPath + "/cal/"

// the feed's address, made the first time it is asked for
func (s *wardenService) feedURL(ctx context.Context, sp *wardenSpace, reset bool) (string, error) {
	if docStr(sp.doc, "feed") == "" || reset {
		sp.doc["feed"] = wardenRandomID("")
		if err := s.save(ctx, sp); err != nil {
			return "", err
		}
	}
	return s.base + wardenFeedPath + docStr(sp.doc, "feed") + ".ics", nil
}

// calendar_feed: the address a phone's or a computer's calendar subscribes
// to; reset (the owner) makes a new one and the old stops working
func (s *wardenService) calendarFeed(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	reset, _ := a["reset"].(bool)
	if reset {
		if err := sp.owner(); err != nil {
			return nil, err
		}
	}
	u, err := s.feedURL(ctx, sp, reset)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"url": u, "webcal": "webcal://" + strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://"),
		"how": "iPhone: open the webcal address, or Settings → Calendar → Accounts → Add Account → Other → Add Subscribed Calendar and paste the url. Mac Calendar: File → New Calendar Subscription. Google Calendar: Other calendars → From URL. The address is the space's secret: anyone with it sees the events.",
	}, nil
}

func serveWardenFeed(env *Env, w http.ResponseWriter, r *http.Request, p string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	tok, ok := strings.CutSuffix(strings.TrimPrefix(p, wardenFeedPath), ".ics")
	if !ok || len(tok) < 16 || !wardenIDRe.MatchString(tok) {
		http.NotFound(w, r)
		return
	}
	if why := wardenLimit("feed:" + tok); why != "" {
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	}
	docs, ids, err := env.DB.WhereEq(r.Context(), wardenSpaces, "feed", tok)
	if err != nil {
		http.Error(w, "The calendar could not be reached", http.StatusServiceUnavailable)
		return
	}
	if len(docs) != 1 {
		http.NotFound(w, r)
		return
	}
	sp := &wardenSpace{id: ids[0], doc: docs[0]}
	evs, eids, err := env.warden.events(r.Context(), sp)
	if err != nil {
		http.Error(w, "The calendar could not be reached", http.StatusServiceUnavailable)
		return
	}
	wardenICSOut(w, "", env.warden.icsCalendar(sp, evs, eids, true))
}

func serveWardenEventICS(env *Env, w http.ResponseWriter, r *http.Request, p string) {
	parts := strings.Split(strings.TrimPrefix(p, wardenPath+"/ics/"), "/")
	g := env.EditorGate
	if len(parts) != 2 || g == nil || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	t := g.user(r)
	if t == nil {
		http.Redirect(w, r, wardenPath+"/login", http.StatusFound)
		return
	}
	sp, err := env.warden.space(r.Context(), wardenWhoOf(t), parts[0])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSuffix(parts[1], ".ics")
	d, err := env.warden.event(r.Context(), sp, id)
	if err != nil || docStr(d, "date") == "" {
		http.NotFound(w, r)
		return
	}
	wardenICSOut(w, docStr(d, "title"), env.warden.icsCalendar(sp, []Doc{d}, []string{id}, false))
}

// a file name of the title's letters and digits
func wardenICSOut(w http.ResponseWriter, title, text string) {
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	if title != "" {
		name := strings.Map(func(r rune) rune {
			if r < 128 && (r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
				return r
			}
			if r == ' ' {
				return '-'
			}
			return -1
		}, title)
		if name == "" {
			name = "event"
		}
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.ics"`)
	}
	w.Write([]byte(text))
}

// ------------------------------------------------------------- iCalendar --

// the space's dated entries as one VCALENDAR; feed: a calendar to subscribe
// to (its name, how often to read it again)
func (s *wardenService) icsCalendar(sp *wardenSpace, docs []Doc, ids []string, feed bool) string {
	tz, _ := sp.json()["time_zone"].(string)
	loc, err := time.LoadLocation(tz)
	if err != nil {
		tz = wardenTZ
		if loc, err = time.LoadLocation(tz); err != nil {
			loc = time.UTC
		}
	}
	wards := map[string]string{}
	for _, wd := range docMaps(sp.doc, "wards") {
		wards[docStr(wd, "id")] = docStr(wd, "name")
	}
	var b strings.Builder
	line := func(l string) { b.WriteString(icsFold(l)) }
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//Sliqtly//Warden//FI")
	line("CALSCALE:GREGORIAN")
	if feed {
		line("METHOD:PUBLISH")
		line("X-WR-CALNAME:" + icsEsc(docStr(sp.doc, "name")))
		line("X-WR-TIMEZONE:" + tz)
		line("REFRESH-INTERVAL;VALUE=DURATION:PT1H")
		line("X-PUBLISHED-TTL:PT1H")
	}
	for _, l := range icsTimezone(tz, loc, s.now().Year()-1) {
		line(l)
	}
	stamp := s.now().UTC().Format("20060102T150405Z")
	for i, d := range docs {
		day := docStr(d, "date")
		if day == "" {
			continue
		}
		dt, _ := time.Parse("2006-01-02", day)
		line("BEGIN:VEVENT")
		line("UID:" + ids[i] + "@warden.sliqtly.com")
		line("DTSTAMP:" + stamp)
		if u, ok := d["updated"].(int64); ok {
			line("LAST-MODIFIED:" + time.UnixMilli(u).UTC().Format("20060102T150405Z"))
		} else if f, ok := d["updated"].(float64); ok {
			line("LAST-MODIFIED:" + time.UnixMilli(int64(f)).UTC().Format("20060102T150405Z"))
		}
		start := docStr(d, "start")
		timed := start != ""
		if timed {
			end := docStr(d, "end")
			if end == "" {
				end = wardenPlusHour(start)
			}
			line("DTSTART;TZID=" + tz + ":" + dt.Format("20060102") + "T" + strings.ReplaceAll(start, ":", "") + "00")
			line("DTEND;TZID=" + tz + ":" + dt.Format("20060102") + "T" + strings.ReplaceAll(end, ":", "") + "00")
		} else {
			last := dt
			if e := docStr(d, "end_date"); e != "" {
				if t, err := time.Parse("2006-01-02", e); err == nil && t.After(dt) {
					last = t
				}
			}
			line("DTSTART;VALUE=DATE:" + dt.Format("20060102"))
			line("DTEND;VALUE=DATE:" + last.AddDate(0, 0, 1).Format("20060102"))
		}
		if r := docStr(d, "rrule"); r != "" {
			line("RRULE:" + icsUntil(r, timed, loc))
			for _, x := range docStrs(d, "exdates") {
				xd, err := time.Parse("2006-01-02", x)
				if err != nil {
					continue
				}
				if timed {
					line("EXDATE;TZID=" + tz + ":" + xd.Format("20060102") + "T" + strings.ReplaceAll(start, ":", "") + "00")
				} else {
					line("EXDATE;VALUE=DATE:" + xd.Format("20060102"))
				}
			}
		}
		line("SUMMARY:" + icsEsc(docStr(d, "title")))
		if p := docStr(d, "place"); p != "" {
			line("LOCATION:" + icsEsc(p))
		}
		notes := []string{}
		if wn := wards[docStr(d, "ward")]; wn != "" {
			line("CATEGORIES:" + icsEsc(wn))
			notes = append(notes, wn)
		}
		for _, m := range docMaps(d, "who") {
			n := docStr(m, "name")
			if r := docStr(m, "role"); r != "" {
				n += " (" + r + ")"
			}
			notes = append(notes, n)
		}
		if n := docStr(d, "notes"); n != "" {
			notes = append(notes, n)
		}
		if len(notes) > 0 {
			line("DESCRIPTION:" + icsEsc(strings.Join(notes, "\n")))
		}
		if s.base != "" {
			line("URL:" + s.base + wardenPath + "/")
		}
		if f, _ := d["fields"].(map[string]any); docStr(f, "status") == "cancelled" {
			line("STATUS:CANCELLED")
		}
		if b, _ := d["important"].(bool); b {
			line("PRIORITY:1")
		}
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	return b.String()
}

// an RRULE's UNTIL day as iCalendar has it with a timed start: the end of
// that day, in UTC
func icsUntil(r string, timed bool, loc *time.Location) string {
	if !timed {
		return r
	}
	parts := strings.Split(r, ";")
	for i, p := range parts {
		if v, ok := strings.CutPrefix(p, "UNTIL="); ok && len(v) == 8 {
			if t, err := time.ParseInLocation("20060102", v, loc); err == nil {
				parts[i] = "UNTIL=" + t.Add(24*time.Hour-time.Second).UTC().Format("20060102T150405Z")
			}
		}
	}
	return strings.Join(parts, ";")
}

// text as an iCalendar value: backslash, semicolon, comma and line ends
// escaped
func icsEsc(s string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`).Replace(s)
}

// a content line folded at 75 octets (not inside a character), CRLF ended
func icsFold(l string) string {
	var b strings.Builder
	n := 0
	for _, r := range l {
		size := len(string(r))
		if n+size > 75 {
			b.WriteString("\r\n ")
			n = 1
		}
		b.WriteRune(r)
		n += size
	}
	b.WriteString("\r\n")
	return b.String()
}

var icsDays = []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

// the zone's VTIMEZONE: its changes in the year, each repeating yearly on
// the same weekday of the month (the last one, or the first, second…)
func icsTimezone(tz string, loc *time.Location, year int) []string {
	out := []string{"BEGIN:VTIMEZONE", "TZID:" + tz}
	off := func(t time.Time) (string, bool) {
		_, o := t.Zone()
		sign := "+"
		if o < 0 {
			sign, o = "-", -o
		}
		return fmt.Sprintf("%s%02d%02d", sign, o/3600, o%3600/60), t.IsDST()
	}
	t := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	end := t.AddDate(1, 0, 0)
	prev, _ := off(t.In(loc))
	changed := false
	for ; t.Before(end); t = t.Add(time.Hour) {
		now, dst := off(t.In(loc))
		if now == prev {
			continue
		}
		// the wall clock when it changes, in the time it changes from
		local := t.In(time.FixedZone("", icsSecs(prev)))
		kind := "STANDARD"
		if dst {
			kind = "DAYLIGHT"
		}
		last := time.Date(local.Year(), local.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		nth := fmt.Sprint((local.Day()-1)/7 + 1)
		if local.Day() > last-7 {
			nth = "-1"
		}
		name, _ := t.In(loc).Zone()
		out = append(out, "BEGIN:"+kind,
			"DTSTART:"+local.Format("20060102T150405"),
			fmt.Sprintf("RRULE:FREQ=YEARLY;BYMONTH=%d;BYDAY=%s%s", int(local.Month()), nth, icsDays[local.Weekday()]),
			"TZOFFSETFROM:"+prev, "TZOFFSETTO:"+now, "TZNAME:"+name, "END:"+kind)
		prev, changed = now, true
	}
	if !changed {
		name, _ := t.In(loc).Zone()
		out = append(out, "BEGIN:STANDARD", "DTSTART:19700101T000000", "TZOFFSETFROM:"+prev, "TZOFFSETTO:"+prev, "TZNAME:"+name, "END:STANDARD")
	}
	return append(out, "END:VTIMEZONE")
}

func icsSecs(off string) int {
	var h, m int
	fmt.Sscanf(off[1:], "%02d%02d", &h, &m)
	s := h*3600 + m*60
	if off[0] == '-' {
		return -s
	}
	return s
}
