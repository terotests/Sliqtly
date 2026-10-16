// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's iCalendar import (import_ics): the VEVENTs of a calendar file
// (what a calendar app exports, or an assistant reads from one) as events of
// a space. A time with a zone (TZID, or UTC "Z") is turned into the space's
// own; an all-day DTEND is the day after the last, as iCalendar has it.
// UID becomes ext_id, so importing the same file again changes the events
// instead of adding them twice. Cancelled events are left out.

package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// one VEVENT's properties: name → value, with the parameters kept
type icsProp struct {
	params map[string]string
	value  string
}

// the VEVENTs of the text, each as its properties (a property that comes
// more than once, as EXDATE may, keeps every value)
func icsEvents(text string) []map[string][]icsProp {
	// unfold: a line that starts with a space or tab goes on the last one
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := []string{}
	for _, l := range strings.Split(text, "\n") {
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(lines) > 0 {
			lines[len(lines)-1] += l[1:]
			continue
		}
		lines = append(lines, l)
	}
	out := []map[string][]icsProp{}
	var cur map[string][]icsProp
	depth := 0 // inside VEVENT, how deep (a VALARM is inside one)
	for _, l := range lines {
		l = strings.TrimRight(l, "\r")
		switch {
		case l == "BEGIN:VEVENT":
			cur, depth = map[string][]icsProp{}, 1
			continue
		case cur != nil && strings.HasPrefix(l, "BEGIN:"):
			depth++
			continue
		case cur != nil && l == "END:VEVENT" && depth == 1:
			out = append(out, cur)
			cur, depth = nil, 0
			continue
		case cur != nil && strings.HasPrefix(l, "END:"):
			depth--
			continue
		}
		if cur == nil || depth != 1 {
			continue
		}
		colon := icsColon(l)
		if colon < 0 {
			continue
		}
		head, value := l[:colon], l[colon+1:]
		parts := strings.Split(head, ";")
		p := icsProp{params: map[string]string{}, value: value}
		for _, kv := range parts[1:] {
			if k, v, ok := strings.Cut(kv, "="); ok {
				p.params[strings.ToUpper(k)] = strings.Trim(v, `"`)
			}
		}
		name := strings.ToUpper(parts[0])
		cur[name] = append(cur[name], p)
	}
	return out
}

// the colon that ends a property's name and parameters (not one inside a
// quoted parameter value)
func icsColon(l string) int {
	quoted := false
	for i, c := range l {
		switch c {
		case '"':
			quoted = !quoted
		case ':':
			if !quoted {
				return i
			}
		}
	}
	return -1
}

func icsText(v string) string {
	r := strings.NewReplacer(`\n`, "\n", `\N`, "\n", `\,`, ",", `\;`, ";", `\\`, `\`)
	return strings.TrimSpace(r.Replace(v))
}

// a DATE or DATE-TIME in the space's zone: the day, the time ("" for a
// date), or an error
func icsWhen(p icsProp, loc *time.Location) (day, hm string, err error) {
	v := strings.TrimSpace(p.value)
	if p.params["VALUE"] == "DATE" || len(v) == 8 {
		t, err := time.Parse("20060102", v)
		if err != nil {
			return "", "", fmt.Errorf("%q is not a date", v)
		}
		return t.Format("2006-01-02"), "", nil
	}
	in := loc
	if strings.HasSuffix(v, "Z") {
		in = time.UTC
		v = strings.TrimSuffix(v, "Z")
	} else if tz := p.params["TZID"]; tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			in = l
		}
	}
	t, err := time.ParseInLocation("20060102T150405", v, in)
	if err != nil {
		return "", "", fmt.Errorf("%q is not a date and time", p.value)
	}
	t = t.In(loc)
	return t.Format("2006-01-02"), t.Format("15:04"), nil
}

func icsFirst(e map[string][]icsProp, k string) (icsProp, bool) {
	l := e[k]
	if len(l) == 0 {
		return icsProp{}, false
	}
	return l[0], true
}

// the VEVENTs as import_events' events
func icsToEvents(text string, loc *time.Location) ([]any, int, error) {
	evs := icsEvents(text)
	out := []any{}
	cancelled := 0
	moved := map[string][]any{}
	for i, e := range evs {
		if st, ok := icsFirst(e, "STATUS"); ok && strings.EqualFold(st.value, "CANCELLED") {
			// a cancelled day of a repeating event is a day it skips
			u, hasUID := icsFirst(e, "UID")
			if rid, ok := icsFirst(e, "RECURRENCE-ID"); ok && hasUID {
				if d, _, err := icsWhen(rid, loc); err == nil {
					moved[strings.TrimSpace(u.value)] = append(moved[strings.TrimSpace(u.value)], d)
				}
			}
			cancelled++
			continue
		}
		start, ok := icsFirst(e, "DTSTART")
		if !ok {
			return nil, 0, roomErr{fmt.Sprintf("event %d has no DTSTART", i+1)}
		}
		day, hm, err := icsWhen(start, loc)
		if err != nil {
			return nil, 0, roomErr{fmt.Sprintf("event %d: DTSTART %s", i+1, err)}
		}
		m := map[string]any{"date": day}
		if s, ok := icsFirst(e, "SUMMARY"); ok {
			m["title"] = icsText(s.value)
		}
		if t, _ := m["title"].(string); t == "" {
			m["title"] = "(no title)"
		}
		if end, ok := icsFirst(e, "DTEND"); ok {
			eday, ehm, err := icsWhen(end, loc)
			if err != nil {
				return nil, 0, roomErr{fmt.Sprintf("event %d: DTEND %s", i+1, err)}
			}
			if hm == "" {
				// the day after the last
				t, _ := time.Parse("2006-01-02", eday)
				if last := t.AddDate(0, 0, -1).Format("2006-01-02"); last > day {
					m["end_date"] = last
				}
			} else if eday == day {
				m["end"] = ehm
			} else {
				m["end"] = "23:59"
			}
		}
		if hm != "" {
			m["start"] = hm
		}
		if l, ok := icsFirst(e, "LOCATION"); ok {
			m["place"] = icsText(l.value)
		}
		if d, ok := icsFirst(e, "DESCRIPTION"); ok {
			n := icsText(d.value)
			if r := []rune(n); len(r) > 2000 {
				n = string(r[:2000])
			}
			m["notes"] = n
		}
		if u, ok := icsFirst(e, "UID"); ok && strings.TrimSpace(u.value) != "" {
			m["ext_id"] = strings.TrimSpace(u.value)
			// one changed day of a repeating event: its own event, and a day
			// the repeating one skips
			if rid, ok := icsFirst(e, "RECURRENCE-ID"); ok {
				if d, _, err := icsWhen(rid, loc); err == nil {
					m["ext_id"] = strings.TrimSpace(u.value) + "/" + d
					moved[strings.TrimSpace(u.value)] = append(moved[strings.TrimSpace(u.value)], d)
				}
			}
		}
		if r, ok := icsFirst(e, "RRULE"); ok {
			m["rrule"] = r.value
		}
		ex := []any{}
		for _, p := range e["EXDATE"] {
			for _, v := range strings.Split(p.value, ",") {
				d, _, err := icsWhen(icsProp{params: p.params, value: v}, loc)
				if err == nil {
					ex = append(ex, d)
				}
			}
		}
		if len(ex) > 0 {
			m["exdates"] = ex
		}
		out = append(out, m)
	}
	for _, e := range out {
		m := e.(map[string]any)
		if days := moved[argStr(m, "ext_id")]; len(days) > 0 && argStr(m, "rrule") != "" {
			ex, _ := m["exdates"].([]any)
			m["exdates"] = append(ex, days...)
		}
	}
	return out, cancelled, nil
}

func (s *wardenService) importICS(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	text := argStr(a, "ics")
	if !strings.Contains(text, "BEGIN:VEVENT") {
		return nil, roomErr{"ics is the text of an iCalendar file (BEGIN:VEVENT … END:VEVENT)"}
	}
	if len(text) > 2<<20 {
		return nil, roomErr{"ics is larger than 2 MB"}
	}
	tz, _ := sp.json()["time_zone"].(string)
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc, _ = time.LoadLocation(wardenTZ)
	}
	evs, cancelled, err := icsToEvents(text, loc)
	if err != nil {
		return nil, err
	}
	if len(evs) == 0 {
		return nil, roomErr{"the file has no events to import"}
	}
	defaults := map[string]any{"source": "ics"}
	for _, k := range []string{"ward", "color", "source"} {
		if v := argStr(a, k); v != "" {
			defaults[k] = v
		}
	}
	if v, ok := a["important"].(bool); ok {
		defaults["important"] = v
	}
	out, err := s.importEvents(ctx, sp, evs, defaults)
	if err != nil {
		return nil, err
	}
	res := out.(map[string]any)
	if cancelled > 0 {
		res["cancelled_skipped"] = cancelled
	}
	return res, nil
}
