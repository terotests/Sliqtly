// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

var whLoc = func() *time.Location {
	l, err := time.LoadLocation("Europe/Helsinki")
	if err != nil {
		panic(err)
	}
	return l
}()

func whAt(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, whLoc)
	if err != nil {
		panic(err)
	}
	return t
}

func whEdit(at, topic, deck string) workEvent {
	return workEvent{At: whAt(at), Items: []workItem{{Topic: topic, TopicTitle: "T " + topic, Deck: deck, DeckName: "D " + deck}}}
}

func whReport(t *testing.T, month string, evs ...workEvent) *workReport {
	t.Helper()
	r, err := buildWorkReport(evs, month, workOptions{Gap: 30 * time.Minute, Lead: 10 * time.Minute, Loc: whLoc})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Edits within the gap are one sitting: its span plus the lead before the
// first edit; a longer pause starts another sitting
func TestWorkHoursSittings(t *testing.T) {
	r := whReport(t, "2026-11",
		whEdit("2026-11-10 09:00", "a", "d1"),
		whEdit("2026-11-10 09:20", "a", "d1"),
		whEdit("2026-11-10 09:45", "a", "d2"),
		// 75 min pause: a new sitting
		whEdit("2026-11-10 11:00", "b", "d3"),
	)
	if len(r.Days) != 1 {
		t.Fatalf("days %d", len(r.Days))
	}
	d := r.Days[0]
	// 10 + 45, then 10
	if d.Date != "2026-11-10" || d.Minutes != 65 || r.Minutes != 65 || d.Edits != 4 {
		t.Fatalf("day %+v", d)
	}
	if len(d.Sessions) != 2 || r.Sessions != 2 {
		t.Fatalf("sessions %v", d.Sessions)
	}
	if s := d.Sessions[0]; s.Start != whAt("2026-11-10 08:50").UnixMilli() || s.End != whAt("2026-11-10 09:45").UnixMilli() {
		t.Fatalf("first sitting %v", s)
	}
	if len(d.Topics) != 2 || d.Topics[0].ID != "a" || d.Topics[0].Minutes != 55 || d.Topics[1].Minutes != 10 {
		t.Fatalf("topics %+v %+v", d.Topics[0], d.Topics[1])
	}
	if a := d.Topics[0]; a.Edits != 3 || len(a.Decks) != 2 || a.Decks[0].Name != "D d1" || a.Title != "T a" {
		t.Fatalf("topic a %+v", a)
	}
}

// An edit touching two topics shares its time between them
func TestWorkHoursSharedEdit(t *testing.T) {
	e := workEvent{At: whAt("2026-11-03 14:00"), Items: []workItem{{Topic: "a", Deck: "x"}, {Topic: "b", Deck: "y"}, {Topic: "a", Deck: "z"}}}
	r := whReport(t, "2026-11", e)
	d := r.Days[0]
	if d.Minutes != 10 || len(d.Topics) != 2 || d.Topics[0].Minutes != 5 || d.Topics[1].Minutes != 5 {
		t.Fatalf("day %+v", d)
	}
	if len(d.Topics[0].Decks) != 2 || d.Topics[0].Edits != 1 {
		t.Fatalf("topic a %+v", d.Topics[0])
	}
}

// A sitting over midnight is split between the two days; the month's
// edges cut it too, and edits outside the month are not listed
func TestWorkHoursMidnightAndMonth(t *testing.T) {
	r := whReport(t, "2026-11",
		whEdit("2026-10-31 23:40", "a", "d"),
		whEdit("2026-11-01 00:20", "a", "d"),
		whEdit("2026-11-30 23:50", "a", "d"),
		whEdit("2026-12-01 00:10", "a", "d"),
	)
	if len(r.Days) != 2 {
		t.Fatalf("days %+v", r.Days)
	}
	// 40 min pause is over the gap: 00:20 starts a sitting at 00:10
	if d := r.Days[0]; d.Date != "2026-11-01" || d.Minutes != 10 || d.Edits != 1 {
		t.Fatalf("first %+v", d)
	}
	// 23:40–24:00 in November (the December edit's time up to midnight
	// too), the rest is December's; that edit is not listed
	if d := r.Days[1]; d.Date != "2026-11-30" || d.Minutes != 20 || d.Edits != 1 {
		t.Fatalf("last %+v", d)
	}
	r = whReport(t, "2026-11",
		whEdit("2026-11-09 23:50", "a", "d"),
		whEdit("2026-11-10 00:15", "a", "d"),
	)
	if len(r.Days) != 2 || r.Days[0].Minutes != 20 || r.Days[1].Minutes != 15 || r.Minutes != 35 {
		t.Fatalf("over midnight %+v %+v", r.Days[0], r.Days[1])
	}
	if len(r.Days[0].Sessions) != 1 || len(r.Days[1].Sessions) != 1 || r.Days[1].Sessions[0].Start != whAt("2026-11-10 00:00").UnixMilli() {
		t.Fatalf("spans %v %v", r.Days[0].Sessions, r.Days[1].Sessions)
	}
	// the 09.11 edit's topic is listed on 09.11, the time after midnight on 10.11 too
	if len(r.Days[1].Topics) != 1 || r.Days[1].Topics[0].Edits != 1 {
		t.Fatalf("topics %+v", r.Days[1].Topics)
	}
}

// Nothing to count, a month badly given, the order the edits come in
func TestWorkHoursEdges(t *testing.T) {
	if r := whReport(t, "2026-02"); len(r.Days) != 0 || r.Minutes != 0 || r.Days == nil {
		t.Fatalf("empty %+v", r)
	}
	if _, err := buildWorkReport(nil, "2026-1", workOptions{}); err == nil {
		t.Fatal("bad month taken")
	}
	if _, err := buildWorkReport(nil, "november", workOptions{}); err == nil {
		t.Fatal("bad month taken")
	}
	a := whReport(t, "2026-11", whEdit("2026-11-10 10:20", "a", "d"), whEdit("2026-11-10 10:00", "a", "d"))
	if a.Days[0].Minutes != 30 || len(a.Days[0].Sessions) != 1 {
		t.Fatalf("unsorted %+v", a.Days[0])
	}
	// two saves at the same moment: both listed, the time once
	z := whReport(t, "2026-11", whEdit("2026-11-10 10:00", "a", "d"), whEdit("2026-11-10 10:00", "b", "e"))
	if z.Edits != 2 || z.Days[0].Minutes != 10 || len(z.Days[0].Topics) != 2 || len(z.Days[0].Sessions) != 1 {
		t.Fatalf("same moment %+v", z.Days[0])
	}
	// a deck in no topic, and a topic edited without a deck
	n := whReport(t, "2026-11", workEvent{At: whAt("2026-11-10 10:00"), Items: []workItem{{Deck: "d", DeckName: "Loose"}, {Topic: "r", TopicTitle: "Room"}}})
	if len(n.Days[0].Topics) != 2 {
		t.Fatalf("topics %+v", n.Days[0].Topics)
	}
	for _, tp := range n.Days[0].Topics {
		if tp.ID == "r" && len(tp.Decks) != 0 || tp.ID == "" && tp.Decks[0].Name != "Loose" {
			t.Fatalf("topic %+v", tp)
		}
	}
}

// The API: off until turned on, the month's report from the server's Git
// commits (the first, the import, is not work), each edit's deck in its
// topic; no repository says so
func TestWorkHoursAPI(t *testing.T) {
	ctx := context.Background()
	data := filepath.Join(t.TempDir(), "data")
	ls, s, stop := startLocalServer(t, data)
	defer stop()
	ls.gits = &gitService{data: data, docs: ls.bucket.docs, blobs: ls.bucket.blobs, feed: ls.env.Store, bucket: ls.bucket, logf: t.Logf}
	defer ls.gits.stop()

	if code, st := gitJSONReq(t, "GET", s.root+"/api/settings/workhours", ""); code != 200 || st["enabled"] != false || st["git"] != false {
		t.Fatalf("state %d %v", code, st)
	}
	if code, _ := gitJSONReq(t, "GET", s.root+"/api/workhours?month=2026-11", ""); code != 403 {
		t.Fatalf("off: %d", code)
	}
	if code, st := gitJSONReq(t, "PUT", s.root+"/api/settings/workhours", `{"enabled":true}`); code != 200 || st["enabled"] != true {
		t.Fatalf("on %d %v", code, st)
	}
	// no repository yet: an empty report that says so
	code, out := gitJSONReq(t, "GET", s.root+"/api/workhours?month=2026-11&tz=UTC", "")
	if code != 200 || out["git"] != false || len(out["report"].(map[string]any)["days"].([]any)) != 0 {
		t.Fatalf("no git %d %v", code, out)
	}
	if code, _ := gitJSONReq(t, "GET", s.root+"/api/workhours?month=11-2026", ""); code != 400 {
		t.Fatalf("bad month %d", code)
	}
	if code, _ := gitJSONReq(t, "GET", s.root+"/api/workhours?tz=Mars/Base", ""); code != 400 {
		t.Fatalf("bad zone %d", code)
	}

	c := call(t, s, "create_presentation", map[string]any{"title": "Plan", "markdown": "# Plan\n"})
	id := sc(c)["deck_id"].(string)
	r := call(t, s, "create_room", map[string]any{"title": "Launch"})
	room := sc(r)["room_id"].(string)
	o := call(t, s, "create_presentation", map[string]any{"title": "Loose", "markdown": "# Loose\n"})
	other := sc(o)["deck_id"].(string)
	if err := ls.env.DB.Update(ctx, "shares", id, Doc{"room": room}); err != nil {
		t.Fatal(err)
	}
	if code, st := gitJSONReq(t, "PUT", s.root+"/api/settings/git", `{"enabled":true}`); code != 200 {
		t.Fatalf("git on: %d %v", code, st)
	}
	if err := ls.env.DB.Update(ctx, "shares", id, Doc{"md": "# Plan\n\n## Two\n"}); err != nil {
		t.Fatal(err)
	}
	gitJSONReq(t, "POST", s.root+"/api/git/save", `{"message":"two"}`)
	if err := ls.env.DB.Update(ctx, "shares", other, Doc{"md": "# Loose 2\n"}); err != nil {
		t.Fatal(err)
	}
	gitJSONReq(t, "POST", s.root+"/api/git/save", `{"message":"loose"}`)
	head, _ := ls.gits.mirror().Head()
	hc, _ := ls.gits.mirror().repo.ReadCommit(head)
	month := hc.Committer.When.Add(-60 * time.Second).UTC().Format("2006-01")

	code, out = gitJSONReq(t, "GET", s.root+"/api/workhours?month="+month+"&tz=UTC", "")
	if code != 200 || out["git"] != true || out["gitOn"] != true {
		t.Fatalf("report %d %v", code, out)
	}
	rep := out["report"].(map[string]any)
	days := rep["days"].([]any)
	if len(days) != 1 || rep["edits"] != float64(2) || rep["sessions"] != float64(1) || rep["zone"] != "UTC" {
		t.Fatalf("report %v", rep)
	}
	day := days[0].(map[string]any)
	// two saves at once: one sitting of the lead (10 minutes)
	if day["minutes"] != float64(10) {
		t.Fatalf("day %v", day)
	}
	got := map[string]string{}
	for _, x := range day["topics"].([]any) {
		tp := x.(map[string]any)
		decks := tp["decks"].([]any)
		if len(decks) != 1 {
			t.Fatalf("topic %v", tp)
		}
		got[tp["title"].(string)] = decks[0].(map[string]any)["name"].(string)
	}
	if len(got) != 2 || got["Launch"] != "Plan" || got["General"] != "Loose" {
		t.Fatalf("topics %v", got)
	}
	// another month has nothing
	if _, out = gitJSONReq(t, "GET", s.root+"/api/workhours?month=2001-01&tz=UTC", ""); len(out["report"].(map[string]any)["days"].([]any)) != 0 {
		t.Fatalf("2001 %v", out)
	}
}
