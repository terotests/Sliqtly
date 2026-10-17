// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's children's events watch (Lasten tapahtumavahti): a space can have
// the server look, now and then, for events and things to do for children
// near the family's home (wardenWatchArea's town, Tampere by default): shows,
// workshops, sports days, library hours, fairs. What it finds is kept for
// the summary's Tapahtumat tab, best fit first; one is put in the calendar
// with add_event_find.
//
// The search is the deal watcher's (wardenwatch.go): Gemini with Google
// Search, a line per event, each tied to the page it came from.
//
// The watch is the space's, warden_kidwatch/{space id}:
//
//	{active, ages, interests, area, every_days, finds, seen, runs, last_run,
//	 last_error, by}
//	ages:  age groups: baby (0-2), small (3-6), school (7-12), teen (13-17)
//	finds: [{id, title, url, date, start, place, price, age, score, why,
//	        found, rating, reason, added}] (added: the calendar entry's id)
//	runs:  the latest runs' logs, newest first, as an entry watch's
//	       (wardenwatch.go), with near and ages in place of query
//
// The daily run (POST /apps/warden/api/run_watches) runs the due ones after
// the entries' watches.

package main

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	wardenKidWatches = "warden_kidwatch"
	wardenKidFinds   = 40 // events kept, soonest first
	wardenKidHits    = 12 // events asked of one search
	wardenKidDays    = 30 // how far ahead a search looks
)

var wardenKidAges = []struct{ id, en, fi string }{
	{"baby", "babies and toddlers (0-2 years)", "vauvat ja taaperot"},
	{"small", "small children (3-6 years)", "leikki-ikäiset"},
	{"school", "school children (7-12 years)", "koululaiset"},
	{"teen", "teenagers (13-17 years)", "nuoret"},
}

var wardenKidDefault = []string{"small", "school"}

// one event a search found
type wardenEventHit struct {
	Title, URL, Date, Start, Place, Price, Age, Why string
	Score                                           int
}

type wardenEventAsk struct {
	Near, From, To, Ages, Interests string
	Avoid                           []string
	N                               int
	Lang                            string
}

const wardenKidAsk = `Search for upcoming events and things to do for children in and near %s, Finland, between %s and %s: shows, concerts, workshops, sports and club try-outs, library and museum events, fairs, family days.

The children: %s.

List up to %d individual events you found in the search results, one per line, as:
<the event's name> | <its day as YYYY-MM-DD> | <its start time as HH:MM, or ?> | <the place and town> | <the price in euros, free, or ?> | <the ages it is for, or ?> | <how well it suits these children, 0 to 100> | <a few words why>
Only real events from the results with a day between those days, nearest first, no other text.`

const wardenKidInterests = `

They like: %s`

func (g *geminiSearch) Events(ctx context.Context, a wardenEventAsk) ([]wardenEventHit, error) {
	ask := fmt.Sprintf(wardenKidAsk, a.Near, a.From, a.To, a.Ages, a.N)
	if a.Interests != "" {
		ask += fmt.Sprintf(wardenKidInterests, a.Interests)
	}
	if len(a.Avoid) > 0 {
		ask += fmt.Sprintf(wardenSearchAvoid, "- "+strings.Join(a.Avoid, "\n- "))
	}
	ask += wardenWhyLang(a.Lang)
	lines, err := g.ground(ctx, ask, 8)
	if err != nil {
		return nil, err
	}
	hits := []wardenEventHit{}
	for _, l := range lines {
		p := l.parts
		hits = append(hits, wardenEventHit{Title: p[0], URL: l.url, Date: p[1], Start: p[2], Place: aiClip(p[3], 120), Price: aiClip(p[4], 40), Age: aiClip(p[5], 40), Score: wardenFit(p[6]), Why: aiClip(p[7], 120)})
	}
	return hits, nil
}

func wardenKidAgeText(ids []string) string {
	l := []string{}
	for _, a := range wardenKidAges {
		if slices.Contains(ids, a.id) {
			l = append(l, a.en)
		}
	}
	if len(l) == 0 {
		return "children of any age"
	}
	return strings.Join(l, ", ")
}

func (s *wardenService) kidWatch(ctx context.Context, sp *wardenSpace) (Doc, error) {
	return s.db.Get(ctx, wardenKidWatches, sp.id)
}

// the watch as an answer, without what it remembers seeing
func wardenKidJSON(sp *wardenSpace, w Doc, today string) map[string]any {
	if w == nil {
		return map[string]any{"active": false, "ages": anys(wardenKidDefault), "every_days": 7, "area": wardenWatchArea(sp, Doc{}, nil), "finds": []any{}}
	}
	out := map[string]any{"active": w["active"] == true, "ages": anys(docStrs(w, "ages")), "every_days": wardenWatchEvery(w), "area": wardenWatchArea(sp, Doc{}, w)}
	if i := docStr(w, "interests"); i != "" {
		out["interests"] = i
	}
	if t := millis(w["last_run"]); t > 0 {
		out["last_run"] = time.UnixMilli(t).UTC().Format(time.RFC3339)
	}
	if e := docStr(w, "last_error"); e != "" {
		out["last_error"] = e
	}
	if r := docMaps(w, "runs"); len(r) > 0 {
		out["runs"] = anys(r)
	}
	finds := []any{}
	for _, f := range docMaps(w, "finds") {
		if docStr(f, "date") >= today {
			finds = append(finds, f)
		}
	}
	out["finds"] = finds
	return out
}

func (s *wardenService) today(sp *wardenSpace) string {
	return s.now().In(wardenLoc(sp)).Format("2006-01-02")
}

// set_event_watch: the space's children's events watch made or changed
func (s *wardenService) setKidWatch(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	w, err := s.kidWatch(ctx, sp)
	if err != nil {
		return nil, err
	}
	if w == nil {
		w = Doc{"ages": anys(wardenKidDefault), "every_days": float64(7)}
	}
	if v, ok := a["ages"]; ok && v != nil {
		l, _ := v.([]any)
		ages := []string{}
		for _, e := range l {
			id, _ := e.(string)
			if !slices.ContainsFunc(wardenKidAges, func(x struct{ id, en, fi string }) bool { return x.id == id }) {
				return nil, roomErr{"ages are baby, small, school and teen; got " + id}
			}
			if !slices.Contains(ages, id) {
				ages = append(ages, id)
			}
		}
		w["ages"] = anys(ages)
	}
	if v, ok := a["interests"]; ok {
		in, _ := v.(string)
		in = strings.TrimSpace(in)
		if len([]rune(in)) > 300 {
			return nil, roomErr{"interests is at most 300 characters"}
		}
		w["interests"] = in
	}
	if v, ok := a["area"]; ok {
		ar, _ := v.(string)
		ar = strings.TrimSpace(ar)
		if len([]rune(ar)) > 80 {
			return nil, roomErr{"area is at most 80 characters"}
		}
		w["area"] = ar
	}
	if v, ok := a["every_days"].(float64); ok {
		if v < 1 || v > 30 {
			return nil, roomErr{"every_days is 1 to 30"}
		}
		w["every_days"] = float64(int(v))
	}
	w["active"] = argBool(a, "active", true)
	w["space"] = sp.id
	w["by"] = sp.who.uid
	if err := s.db.Set(ctx, wardenKidWatches, sp.id, w); err != nil {
		return nil, err
	}
	out := map[string]any{"space_id": sp.id, "watch": wardenKidJSON(sp, w, s.today(sp))}
	if s.search == nil {
		out["note"] = "Saved. This server has no search (its assistant's model is not Gemini), so the watch does not search."
	}
	return out, nil
}

// list_event_finds: the watch and the events it found that are still to come
func (s *wardenService) listKidFinds(ctx context.Context, sp *wardenSpace) (any, error) {
	w, err := s.kidWatch(ctx, sp)
	if err != nil {
		return nil, err
	}
	return map[string]any{"space_id": sp.id, "watch": wardenKidJSON(sp, w, s.today(sp)), "search": s.search != nil}, nil
}

// run_event_watch: the space's children's events watch, now
func (s *wardenService) runKidWatchOp(ctx context.Context, sp *wardenSpace) (any, error) {
	if s.search == nil {
		return nil, roomErr{"This server has no search (its assistant's model is not Gemini)."}
	}
	w, err := s.kidWatch(ctx, sp)
	if err != nil {
		return nil, err
	}
	if w == nil {
		return nil, roomErr{"this space has no children's events watch; set_event_watch makes one"}
	}
	if why := s.watchLimit(sp.who.uid); why != "" {
		return nil, roomErr{"Too many searches in a short time; try again in a few minutes."}
	}
	fresh, err := s.runKidWatch(ctx, sp, w)
	if err != nil {
		return nil, err
	}
	return map[string]any{"space_id": sp.id, "new": fresh, "watch": wardenKidJSON(sp, w, s.today(sp))}, nil
}

// One search for the space's children's events: the new ones are kept,
// soonest first, and past ones dropped. → the new events
func (s *wardenService) runKidWatch(ctx context.Context, sp *wardenSpace, w Doc) ([]any, error) {
	now := s.now()
	loc := wardenLoc(sp)
	from := now.In(loc).Format("2006-01-02")
	to := now.In(loc).AddDate(0, 0, wardenKidDays).Format("2006-01-02")
	old := []map[string]any{}
	for _, f := range docMaps(w, "finds") {
		if docStr(f, "date") >= from {
			old = append(old, f)
		}
	}
	ask := wardenEventAsk{Near: wardenWatchArea(sp, Doc{}, w), From: from, To: to, Ages: wardenKidAgeText(docStrs(w, "ages")), Interests: docStr(w, "interests"), Avoid: wardenWatchAvoided(docMaps(w, "finds")), N: wardenKidHits, Lang: wardenSpaceLang(sp)}
	seen := docStrs(w, "seen")
	left := map[string]int{}
	fresh := []map[string]any{}
	hits, err := s.search.Events(ctx, ask)
	run := wardenRunLog(sp, now)
	run["near"], run["ages"], run["results"] = ask.Near, anys(docStrs(w, "ages")), len(hits)
	if err != nil {
		w["last_error"] = err.Error()
		run["errors"] = []any{err.Error()}
	} else {
		delete(w, "last_error")
	}
	for _, h := range hits {
		u, err := url.Parse(h.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			left["not_event"]++
			continue
		}
		if !wardenDate(h.Date) || h.Date < from || h.Date > to {
			left["no_day"]++
			continue
		}
		key := wardenHashURL(h.URL + "\x00" + h.Date + "\x00" + strings.ToLower(h.Title))
		if slices.Contains(seen, key) {
			left["seen"]++
			continue
		}
		seen = append(seen, key)
		f := map[string]any{"id": key, "title": aiClip(h.Title, 160), "url": h.URL, "date": h.Date, "found": from}
		if t, err := time.Parse("15:04", h.Start); err == nil {
			f["start"] = t.Format("15:04")
		}
		for k, v := range map[string]string{"place": h.Place, "price": h.Price, "age": h.Age, "why": h.Why} {
			if v != "" {
				f[k] = v
			}
		}
		if h.Score >= 0 {
			f["score"] = float64(h.Score)
			if h.Score < wardenWatchFit {
				left["low_fit"]++
			}
		}
		fresh = append(fresh, f)
	}
	if n := len(seen); n > wardenWatchSeen {
		seen = seen[n-wardenWatchSeen:]
	}
	w["seen"] = anys(seen)
	finds := append(slices.Clone(fresh), old...)
	sort.SliceStable(finds, func(a, b int) bool {
		return docStr(finds[a], "date")+docStr(finds[a], "start") < docStr(finds[b], "date")+docStr(finds[b], "start")
	})
	if len(finds) > wardenKidFinds {
		finds = finds[:wardenKidFinds]
	}
	w["finds"] = anys(finds)
	run["new"] = len(fresh)
	if len(left) > 0 {
		lo := map[string]any{}
		for k, v := range left {
			lo[k] = v
		}
		run["left_out"] = lo
	}
	runs := append([]any{run}, anys(docMaps(w, "runs"))...)
	if len(runs) > wardenWatchRuns {
		runs = runs[:wardenWatchRuns]
	}
	w["runs"] = runs
	w["last_run"] = now.UnixMilli()
	if err := s.db.Set(ctx, wardenKidWatches, sp.id, w); err != nil {
		return nil, err
	}
	return anys(fresh), nil
}

// rate_find without an event_id: one of the space's children's events liked
// or hidden
func (s *wardenService) rateKidFind(ctx context.Context, sp *wardenSpace, fid, rating, reason string) (any, error) {
	w, err := s.kidWatch(ctx, sp)
	if err != nil {
		return nil, err
	}
	finds := docMaps(w, "finds")
	for _, f := range finds {
		if docStr(f, "id") != fid {
			continue
		}
		delete(f, "rating")
		delete(f, "reason")
		if rating != "" {
			f["rating"] = rating
		}
		if reason != "" {
			f["reason"] = reason
		}
		w["finds"] = anys(finds)
		if err := s.db.Set(ctx, wardenKidWatches, sp.id, w); err != nil {
			return nil, err
		}
		return map[string]any{"space_id": sp.id, "find": f}, nil
	}
	return nil, roomErr{"no such find in the children's events: " + fid}
}

// add_event_find: one of the children's events put in the calendar, once
// (again: the same entry, renewed)
func (s *wardenService) addKidFind(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	w, err := s.kidWatch(ctx, sp)
	if err != nil {
		return nil, err
	}
	fid := argStr(a, "find_id")
	finds := docMaps(w, "finds")
	for _, f := range finds {
		if docStr(f, "id") != fid {
			continue
		}
		notes := docStr(f, "url")
		if p := docStr(f, "price"); p != "" {
			notes = p + "\n" + notes
		}
		ev := map[string]any{"title": docStr(f, "title"), "date": docStr(f, "date"), "notes": notes, "ext_id": "kidevent:" + fid, "source": "Tapahtumavahti"}
		if t := docStr(f, "start"); t != "" {
			ev["start"] = t
		}
		if p := docStr(f, "place"); p != "" {
			ev["place"] = p
		}
		if ward := argStr(a, "ward"); ward != "" {
			ev["ward"] = ward
		}
		saved, err := s.saveEvent(ctx, sp, ev)
		if err != nil {
			return nil, err
		}
		out, _ := saved.(map[string]any)
		f["added"] = out["event_id"]
		w["finds"] = anys(finds)
		if err := s.db.Set(ctx, wardenKidWatches, sp.id, w); err != nil {
			return nil, err
		}
		return map[string]any{"space_id": sp.id, "find": f, "event": saved}, nil
	}
	return nil, roomErr{"no such find in the children's events: " + fid}
}

// the children's events watches that are due, oldest run first, at most n
func (s *wardenService) runDueKidWatches(ctx context.Context, n int) (ran, found int, more bool, err error) {
	docs, ids, err := s.db.WhereEq(ctx, wardenKidWatches, "active", true)
	if err != nil {
		return 0, 0, false, err
	}
	now := s.now()
	due := []int{}
	for i, w := range docs {
		every := time.Duration(wardenWatchEvery(w))*24*time.Hour - 2*time.Hour
		if last := millis(w["last_run"]); last > 0 && now.Sub(time.UnixMilli(last)) < every {
			continue
		}
		due = append(due, i)
	}
	sort.SliceStable(due, func(a, b int) bool { return millis(docs[due[a]]["last_run"]) < millis(docs[due[b]]["last_run"]) })
	if len(due) > n {
		more = true
		due = due[:n]
	}
	for _, i := range due {
		sd, err := s.db.Get(ctx, wardenSpaces, ids[i])
		if err != nil {
			return ran, found, more, err
		}
		if sd == nil {
			continue
		}
		sp := &wardenSpace{id: ids[i], doc: sd, who: wardenWho{uid: wardenWatchUID, name: "Tapahtumavahti"}, role: "member"}
		fresh, err := s.runKidWatch(ctx, sp, docs[i])
		if err != nil {
			return ran, found, more, err
		}
		ran++
		found += len(fresh)
	}
	return ran, found, more, nil
}
