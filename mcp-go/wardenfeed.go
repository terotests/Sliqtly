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
	"slices"
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
	reads := []any{}
	if d, _ := s.db.Get(ctx, wardenFeedLogs, docStr(sp.doc, "feed")); d != nil {
		if rs, ok := d["reads"].([]any); ok {
			reads = rs
		}
	}
	return map[string]any{
		"reads": reads,
		"url":   u, "webcal": "webcal://" + strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://"),
		"how": "iPhone: open the webcal address, or Settings → Calendar → Accounts → Add Account → Other → Add Subscribed Calendar and paste the url. Mac Calendar: File → New Calendar Subscription. Google Calendar: Other calendars → From URL. Each entry carries alarms (VALARM) on the days its type reminds of (Apple's calendars show them; Google Calendar leaves a subscribed calendar's alarms out). Add ?get=0, ?pay=0 or ?alarm=0 (joined with &) to leave out the windows to get things in, the payment days or the alarms. Add ?ward=<ward id> (or ward=none, the entries of no ward) or ?group=<type group: activity, care, health, travel, booking, deadline, reminder, financial, asset_action, event> for a calendar of only those entries, with its own name and suggested colour; subscribe to several to show, hide and colour them apart on the phone. The address is the space's secret: anyone with it sees the events. reads: the feed's last reads (when, how, by which app, with which status), to tell a calendar that will not subscribe from one that never reaches the server.",
	}, nil
}

func serveWardenFeed(env *Env, w http.ResponseWriter, r *http.Request, p string) {
	tok, ok := strings.CutSuffix(strings.TrimPrefix(p, wardenFeedPath), ".ics")
	if !ok || len(tok) < 16 || !wardenIDRe.MatchString(tok) {
		http.NotFound(w, r)
		return
	}
	rec := &wardenFeedStatus{ResponseWriter: w, code: http.StatusOK}
	defer func() { wardenFeedLog(env, r, tok, rec.code) }()
	w = rec
	// a calendar app may ask what the address answers to before it reads it
	if r.Method == http.MethodOptions {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
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
	// ?get=0, ?pay=0, ?alarm=0: without the windows to get things in, the
	// payment days, the alarms; ?ward=, ?group=: only one ward's entries
	// ("none": those of no ward) or one type group's, a calendar of their own
	q := r.URL.Query()
	opt := icsOptions{feed: true, gets: q.Get("get") != "0", pays: q.Get("pay") != "0", alarms: q.Get("alarm") != "0",
		ward: q.Get("ward"), group: q.Get("group")}
	docs, dids := evs, eids
	if opt.ward != "" || opt.group != "" {
		docs, dids = nil, nil
		for i, d := range evs {
			if wardenFeedHas(d, opt) {
				docs, dids = append(docs, d), append(dids, eids[i])
			}
		}
	}
	wardenICSOut(w, "", env.warden.icsCalendar(sp, docs, dids, evs, opt))
}

// the status a feed's read was answered with
type wardenFeedStatus struct {
	http.ResponseWriter
	code int
}

func (s *wardenFeedStatus) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

const wardenFeedLogs = "warden_feedlog"

// The feed's last reads (wardenFeedLogN of them), by the address's token:
// when, how, by which app and with what answer, so that a calendar that
// will not subscribe can be told from one that never reaches the server.
const wardenFeedLogN = 20

func wardenFeedLog(env *Env, r *http.Request, tok string, code int) {
	if env.DB == nil {
		return
	}
	ua := r.UserAgent()
	if len(ua) > 160 {
		ua = ua[:160]
	}
	e := map[string]any{"at": time.Now().UTC().Format(time.RFC3339), "method": r.Method, "status": int64(code), "agent": ua, "query": r.URL.RawQuery}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, _ := env.DB.Get(ctx, wardenFeedLogs, tok)
	reads := []any{e}
	if d != nil {
		if old, ok := d["reads"].([]any); ok {
			reads = append(reads, old...)
		}
	}
	if len(reads) > wardenFeedLogN {
		reads = reads[:wardenFeedLogN]
	}
	env.DB.Set(ctx, wardenFeedLogs, tok, Doc{"reads": reads})
}

// whether an entry is in a feed of one ward or one type group
func wardenFeedHas(d Doc, opt icsOptions) bool {
	if opt.ward != "" {
		wd := docStr(d, "ward")
		if opt.ward == "none" && wd != "" || opt.ward != "none" && wd != opt.ward {
			return false
		}
	}
	if opt.group != "" && wardenGroupOf(docStr(d, "type")) != opt.group {
		return false
	}
	return true
}

// an entry type's group, as the page's colours and Add menu group them
// ("event" for a plain event or an unknown type)
func wardenGroupOf(typ string) string {
	if t := wardenTypeOf(typ); t != nil && t.Group != "" {
		if _, ok := wardenGroupNames[t.Group]; ok {
			return t.Group
		}
	}
	return "event"
}

// the type groups' names in the calendar's title, and the colours the
// page draws them in (spaces.css .sp-c-g-*), suggested to the phone
var wardenGroupNames = map[string][2]string{
	"event": {"Tapahtumat", "Events"}, "activity": {"Harrastukset", "Hobbies"}, "care": {"Hoito ja arki", "Care and daily routine"},
	"health": {"Terveys", "Health"}, "travel": {"Matkat", "Travel"}, "booking": {"Varaukset", "Bookings"},
	"deadline": {"Määräpäivät", "Deadlines"}, "reminder": {"Muistutukset", "Reminders"}, "financial": {"Raha ja hankinnat", "Money and purchases"},
	"asset_action": {"Koti ja tavarat", "Home and things"}, "question": {"Kyselyt", "Questions"},
}

var wardenGroupColors = map[string]string{
	"event": "#d9573f", "activity": "#0d9488", "care": "#db2777", "health": "#16a34a", "travel": "#0284c7", "booking": "#a16207",
	"deadline": "#b91c1c", "reminder": "#ca8a04", "financial": "#475569", "asset_action": "#78716c", "question": "#7c3aed",
}

// the wards' colours as the page draws them (spaces.css .sp-c-*)
var wardenColorHex = map[string]string{
	"blue": "#3b82f6", "emerald": "#10b981", "violet": "#8b5cf6", "amber": "#f59e0b", "rose": "#f43f5e", "cyan": "#06b6d4",
	"indigo": "#6366f1", "orange": "#f97316", "green": "#10b981", "red": "#f43f5e", "purple": "#8b5cf6", "teal": "#06b6d4",
	"pink": "#f43f5e", "yellow": "#f59e0b", "gray": "#64748b",
}

// a feed's name and suggested colour: the space's, or with the ward's or
// the group's after it
func (s *wardenService) feedTitle(sp *wardenSpace, opt icsOptions) (string, string) {
	name, color := docStr(sp.doc, "name"), "#d9573f"
	fi := docStr(sp.doc, "lang") != "en"
	var parts []string
	if opt.ward == "none" {
		parts = append(parts, map[bool]string{true: "Yhteiset", false: "Shared"}[fi])
	} else if opt.ward != "" {
		for _, wd := range docMaps(sp.doc, "wards") {
			if docStr(wd, "id") == opt.ward {
				parts = append(parts, docStr(wd, "name"))
				c := docStr(wd, "color")
				if h, ok := wardenColorHex[c]; ok {
					color = h
				} else if wardenHexRe.MatchString(c) {
					color = c
				}
			}
		}
	}
	if n, ok := wardenGroupNames[opt.group]; ok {
		parts = append(parts, map[bool]string{true: n[0], false: n[1]}[fi])
		color = wardenGroupColors[opt.group]
	}
	if len(parts) > 0 {
		name += " · " + strings.Join(parts, " · ")
	}
	return name, color
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
	all, _, err := env.warden.events(r.Context(), sp)
	if err != nil {
		http.Error(w, "The calendar could not be reached", http.StatusServiceUnavailable)
		return
	}
	wardenICSOut(w, docStr(d, "title"), env.warden.icsCalendar(sp, []Doc{d}, []string{id}, all, icsOptions{alarms: true}))
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

// what a VCALENDAR has: feed, a calendar to subscribe to (its name, how
// often to read it again); gets and pays, the windows to get things in and
// the payment days, as the page's calendar shows them; alarms, a VALARM on
// each day an entry reminds of
type icsOptions struct {
	feed, gets, pays, alarms bool
	// the feed of one ward ("none": of no ward) or one type group
	ward, group string
}

// the space's dated entries as one VCALENDAR; all: the space's entries, for
// the days a timetable skips
func (s *wardenService) icsCalendar(sp *wardenSpace, docs []Doc, ids []string, all []Doc, opt icsOptions) string {
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
	if opt.feed {
		line("METHOD:PUBLISH")
		name, color := s.feedTitle(sp, opt)
		line("X-WR-CALNAME:" + icsEsc(name))
		line("X-APPLE-CALENDAR-COLOR:" + strings.ToUpper(color))
		line("X-WR-TIMEZONE:" + tz)
		line("REFRESH-INTERVAL;VALUE=DURATION:PT1H")
		line("X-PUBLISHED-TTL:PT1H")
	}
	for _, l := range icsTimezone(tz, loc, s.now().Year()-1) {
		line(l)
	}
	stamp := s.now().UTC().Format("20060102T150405Z")
	fi := docStr(sp.doc, "lang") != "en"
	for i, d := range docs {
		occs := []icsOcc{}
		if docStr(d, "date") != "" {
			occs = icsOccurrences(d, all)
			if opt.alarms {
				for j := range occs {
					occs[j].alarms = icsAlarms(d, occs[j].start != "", fi, loc, s.now())
				}
			}
		}
		occs = append(occs, icsDerived(d, opt, fi)...)
		for _, o := range occs {
			line("BEGIN:VEVENT")
			line("UID:" + ids[i] + o.uid + "@warden.sliqtly.com")
			line("DTSTAMP:" + stamp)
			if u, ok := d["updated"].(int64); ok {
				line("LAST-MODIFIED:" + time.UnixMilli(u).UTC().Format("20060102T150405Z"))
			} else if f, ok := d["updated"].(float64); ok {
				line("LAST-MODIFIED:" + time.UnixMilli(int64(f)).UTC().Format("20060102T150405Z"))
			}
			icsWhenLines(line, o, tz, loc)
			title := docStr(d, "title")
			if o.title != "" {
				title = o.title
			}
			icsAbout(line, d, title, wards, s.base)
			for _, a := range o.alarms {
				line("BEGIN:VALARM")
				line("ACTION:DISPLAY")
				line("TRIGGER" + a.trigger)
				line("DESCRIPTION:" + icsEsc(wardenOr(a.text, title)))
				line("END:VALARM")
			}
			line("END:VEVENT")
		}
	}
	line("END:VCALENDAR")
	return b.String()
}

// one VEVENT's days: an entry is one, a timetable with different times on
// different weekdays one for each set of times (its UID the entry's and the
// times)
type icsOcc struct {
	uid, title, day, start, end, lastDay, rrule string
	exdates                                     []string
	alarms                                      []icsAlarm
}

// a VEVENT's alarm: its TRIGGER's parameters and value (":-P1D"), what it
// says ("" the event's title)
type icsAlarm struct{ trigger, text string }

// an alarm n days before an occurrence: at its time, or an hour before on
// its own day; for a day without a time at 09:00 of the day
func icsBefore(n int, timed bool) string {
	if timed {
		if n == 0 {
			return ":-PT1H"
		}
		return fmt.Sprintf(":-P%dD", n)
	}
	if h := 24*n - 9; h > 0 {
		return fmt.Sprintf(":-PT%dH", h)
	} else {
		return fmt.Sprintf(":PT%dH", -h)
	}
}

// the alarms of an entry's own days, as its type reminds of them: of its
// day (remind_days of its fields first) before each occurrence, of its other
// dates (answer by, pay by…) at 09:00 of the day, those not past yet. None
// once it is done, cancelled or archived.
func icsAlarms(d Doc, timed, fi bool, loc *time.Location, now time.Time) []icsAlarm {
	t := wardenTypeOf(wardenOr(docStr(d, "type"), "event"))
	fields, _ := d["fields"].(map[string]any)
	if st := docStr(fields, "status"); t == nil || st == "done" || st == "cancelled" || st == "archived" {
		return nil
	}
	today := now.In(loc).Format("2006-01-02")
	out := []icsAlarm{}
	for _, r := range t.Reminders {
		if r.Field == "date" {
			n := r.Days
			if v, ok := fields["remind_days"]; ok {
				n = int(wardenNum(v))
			}
			out = append(out, icsAlarm{trigger: icsBefore(n, timed)})
			continue
		}
		date, _ := fields[r.Field].(string)
		day, err := time.Parse("2006-01-02", date)
		if err != nil {
			continue
		}
		at := day.AddDate(0, 0, -r.Days)
		if at.Format("2006-01-02") < today {
			continue
		}
		name := r.Field
		for _, f := range append(slices.Clone(t.Fields), wardenCommon...) {
			if f.Key == r.Field {
				name = wardenOr(f.Name["en"], name)
				if fi {
					name = wardenOr(f.Name["fi"], name)
				}
				break
			}
		}
		when := time.Date(at.Year(), at.Month(), at.Day(), 9, 0, 0, 0, loc).UTC().Format("20060102T150405Z")
		out = append(out, icsAlarm{trigger: ";VALUE=DATE-TIME:" + when,
			text: fmt.Sprintf("%s %d.%d.: %s", name, day.Day(), int(day.Month()), docStr(d, "title"))})
	}
	return out
}

func icsOccurrences(d Doc, all []Doc) []icsOcc {
	o := icsOcc{day: docStr(d, "date"), start: docStr(d, "start"), end: docStr(d, "end"), lastDay: docStr(d, "end_date"), rrule: docStr(d, "rrule"),
		exdates: append(docStrs(d, "exdates"), icsSkipped(d, all)...)}
	if o.start != "" && o.end == "" {
		o.end = wardenPlusHour(o.start)
	}
	w, ok := icsWeek(d)
	if !ok {
		return []icsOcc{o}
	}
	until := ""
	for _, p := range strings.Split(o.rrule, ";") {
		if strings.HasPrefix(p, "UNTIL=") {
			until = ";" + p
		}
	}
	first, _ := time.Parse("2006-01-02", o.day)
	// the weekdays of each start and end, in the order they first come
	groups, days := []string{}, map[string][]int{}
	for i, x := range w {
		if x.on {
			k := x.start + "-" + x.end
			if days[k] == nil {
				groups = append(groups, k)
			}
			days[k] = append(days[k], i)
		}
	}
	out := []icsOcc{}
	for _, k := range groups {
		g := o
		g.start, g.end, _ = strings.Cut(k, "-")
		if len(groups) > 1 {
			g.uid = "-" + strings.NewReplacer(":", "", "-", "").Replace(k)
		}
		codes := []string{}
		on := map[int]bool{}
		for _, i := range days[k] {
			codes = append(codes, wardenDayCodes[i])
			on[i] = true
		}
		g.rrule = "FREQ=WEEKLY;BYDAY=" + strings.Join(codes, ",") + until
		// the first of these weekdays from the entry's day on
		dt := first
		for !on[(int(dt.Weekday())+6)%7] {
			dt = dt.AddDate(0, 0, 1)
		}
		g.day = dt.Format("2006-01-02")
		g.exdates = nil
		for _, x := range o.exdates {
			if xd, err := time.Parse("2006-01-02", x); err == nil && on[(int(xd.Weekday())+6)%7] {
				g.exdates = append(g.exdates, x)
			}
		}
		out = append(out, g)
	}
	return out
}

// what the page's calendar shows of an entry besides its own days: the
// window to get its thing in, its payment days and the days it is planned to
// be done on (an entry done, cancelled or archived has none), each a day or
// days of its own (UID the entry's and "-get", "-payN", "-planN")
func icsDerived(d Doc, opt icsOptions, fi bool) []icsOcc {
	f, _ := d["fields"].(map[string]any)
	if st := docStr(f, "status"); st == "done" || st == "cancelled" || st == "archived" {
		return nil
	}
	out := []icsOcc{}
	word := func(fiw, en string) string {
		if fi {
			return fiw
		}
		return en
	}
	if opt.gets {
		if from, by := wardenGetWindow(d); by != "" {
			o := icsOcc{uid: "-get", title: word("Hanki: ", "Get: ") + docStr(d, "title"), day: from, lastDay: by}
			if opt.alarms {
				o.alarms = []icsAlarm{{trigger: icsBefore(0, false)}}
			}
			out = append(out, o)
		}
	}
	if c := wardenCostOf(d); opt.pays && c != nil {
		n := wardenPayRemind(c)
		for _, p := range wardenPayments(d, c) {
			amount := strings.TrimSuffix(fmt.Sprintf("%.2f", p.amount), ".00")
			if fi {
				amount = strings.ReplaceAll(amount, ".", ",")
			}
			o := icsOcc{uid: fmt.Sprintf("-pay%d", p.n-1), title: word("Maksu: ", "Payment: ") + docStr(d, "title") + " " + amount + " €", day: p.day.Format("2006-01-02")}
			if opt.alarms && n >= 0 {
				o.alarms = []icsAlarm{{trigger: icsBefore(n, false)}}
			}
			out = append(out, o)
		}
	}
	for i, p := range wardenPlannedDays(d) {
		o := icsOcc{uid: fmt.Sprintf("-plan%d", i), title: word("Suunniteltu: ", "Planned: ") + docStr(d, "title"), day: p}
		if opt.alarms {
			o.alarms = []icsAlarm{{trigger: icsBefore(0, false)}}
		}
		out = append(out, o)
	}
	return out
}

// the times per weekday of an entry whose type has them
func icsWeek(d Doc) (wardenWeek, bool) {
	t := wardenTypeOf(docStr(d, "type"))
	if t == nil {
		return wardenWeek{}, false
	}
	fields, _ := d["fields"].(map[string]any)
	for _, f := range t.Fields {
		if s, _ := fields[f.Key].(string); f.Type == "weektimes" && s != "" {
			w, err := wardenWeekTimes(s)
			return w, err == nil
		}
	}
	return wardenWeek{}, false
}

// the days a repeating entry skips because its type does (a school day on
// a holiday or an exception day of the same ward, or of none, unless that
// day says school is open), as the page has them
func icsSkipped(d Doc, all []Doc) []string {
	t := wardenTypeOf(docStr(d, "type"))
	if t == nil || len(t.Skips) == 0 || docStr(d, "rrule") == "" {
		return nil
	}
	out := []string{}
	for _, x := range all {
		f, _ := x["fields"].(map[string]any)
		if open, _ := f["school_open"].(bool); open || !slices.Contains(t.Skips, docStr(x, "type")) {
			continue
		}
		if w := docStr(x, "ward"); w != "" && w != docStr(d, "ward") {
			continue
		}
		from, err := time.Parse("2006-01-02", docStr(x, "date"))
		if err != nil {
			continue
		}
		to := from
		if e, err := time.Parse("2006-01-02", docStr(x, "end_date")); err == nil && e.After(from) {
			to = e
		}
		for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
			out = append(out, day.Format("2006-01-02"))
		}
	}
	return out
}

// DTSTART, DTEND, RRULE and EXDATE in the space's zone, or as days
func icsWhenLines(line func(string), o icsOcc, tz string, loc *time.Location) {
	dt, _ := time.Parse("2006-01-02", o.day)
	timed := o.start != ""
	at := func(d time.Time, hm string) string {
		return ";TZID=" + tz + ":" + d.Format("20060102") + "T" + strings.ReplaceAll(hm, ":", "") + "00"
	}
	if timed {
		line("DTSTART" + at(dt, o.start))
		line("DTEND" + at(dt, o.end))
	} else {
		last := dt
		if t, err := time.Parse("2006-01-02", o.lastDay); err == nil && t.After(dt) {
			last = t
		}
		line("DTSTART;VALUE=DATE:" + dt.Format("20060102"))
		line("DTEND;VALUE=DATE:" + last.AddDate(0, 0, 1).Format("20060102"))
	}
	if o.rrule == "" {
		return
	}
	line("RRULE:" + icsUntil(o.rrule, timed, loc))
	seen := map[string]bool{}
	for _, x := range o.exdates {
		xd, err := time.Parse("2006-01-02", x)
		if err != nil || seen[x] {
			continue
		}
		seen[x] = true
		if timed {
			line("EXDATE" + at(xd, o.start))
		} else {
			line("EXDATE;VALUE=DATE:" + xd.Format("20060102"))
		}
	}
}

// what the event is, where, whose and who takes care of it
func icsAbout(line func(string), d Doc, title string, wards map[string]string, base string) {
	line("SUMMARY:" + icsEsc(title))
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
	if base != "" {
		line("URL:" + base + wardenPath + "/")
	}
	if f, _ := d["fields"].(map[string]any); docStr(f, "status") == "cancelled" {
		line("STATUS:CANCELLED")
	}
	if b, _ := d["important"].(bool); b {
		line("PRIORITY:1")
	}
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
