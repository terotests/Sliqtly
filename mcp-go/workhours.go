// SPDX-License-Identifier: AGPL-3.0-or-later

// Work hours from when things were edited: Export ▸ Sliqtly work hours on a
// server of one's own (workhoursapi.go reads the times from the server's Git
// repository). No I/O here: edits in, a month's report out.
//
// The estimate: edits closer together than Gap belong to one sitting. Each
// edit counts the time since the edit before it in its sitting; the first
// of a sitting counts Lead, the work that came before the first save. A
// sitting's time goes to the day it was on (split at midnight, in Loc) and
// to the topics its edits were in, an edit's time shared evenly among its
// topics.

package main

import (
	"errors"
	"sort"
	"time"
)

// workItem is what one edit touched: a deck in a topic, or a topic itself
// (Deck ""). Topic "" is a deck in no topic.
type workItem struct {
	Topic      string
	TopicTitle string
	Deck       string
	DeckName   string
}

// workEvent is one edit (a save): when, by whom, and what it touched.
type workEvent struct {
	At     time.Time
	Author string
	Items  []workItem
}

type workOptions struct {
	Gap  time.Duration // a pause longer than this starts a new sitting
	Lead time.Duration // counted before a sitting's first edit
	Loc  *time.Location
}

func (o *workOptions) defaults() {
	if o.Gap <= 0 {
		o.Gap = 30 * time.Minute
	}
	if o.Lead <= 0 {
		o.Lead = 10 * time.Minute
	}
	if o.Loc == nil {
		o.Loc = time.Local
	}
}

type workSpan struct {
	Start int64 `json:"start"` // ms
	End   int64 `json:"end"`
}

type workDeck struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type workTopic struct {
	ID      string     `json:"id"`
	Title   string     `json:"title"`
	Minutes int        `json:"minutes"`
	Edits   int        `json:"edits"`
	Decks   []workDeck `json:"decks"`
	secs    float64
	decks   map[string]bool
}

type workDay struct {
	Date     string       `json:"date"` // 2026-11-10
	Minutes  int          `json:"minutes"`
	Edits    int          `json:"edits"`
	Sessions []workSpan   `json:"sessions"`
	Topics   []*workTopic `json:"topics"`
	secs     float64
	topics   map[string]*workTopic
}

type workReport struct {
	Month    string     `json:"month"` // 2026-11
	Zone     string     `json:"zone"`
	Minutes  int        `json:"minutes"`
	Edits    int        `json:"edits"`
	Sessions int        `json:"sessions"`
	GapMin   int        `json:"gapMinutes"`
	LeadMin  int        `json:"leadMinutes"`
	Days     []*workDay `json:"days"`
}

// workMonth: "2026-11" → its first moment and the next month's, in loc
func workMonth(month string, loc *time.Location) (time.Time, time.Time, error) {
	t, err := time.ParseInLocation("2006-01", month, loc)
	if err != nil || len(month) != 7 {
		return time.Time{}, time.Time{}, errors.New("month is YYYY-MM")
	}
	return t, t.AddDate(0, 1, 0), nil
}

// workSlot is the time one edit counts: from Start to the edit
type workSlot struct {
	Start, End time.Time
	ev         *workEvent
	first      bool // starts a sitting
}

// workSlots: each edit's slot, sittings split where the pause exceeds gap
func workSlots(events []workEvent, o workOptions) []workSlot {
	evs := make([]*workEvent, len(events))
	for i := range events {
		evs[i] = &events[i]
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].At.Before(evs[j].At) })
	var out []workSlot
	for i, e := range evs {
		s := workSlot{End: e.At, ev: e}
		if i > 0 && e.At.Sub(evs[i-1].At) <= o.Gap {
			s.Start = evs[i-1].At
		} else {
			s.Start = e.At.Add(-o.Lead)
			s.first = true
		}
		out = append(out, s)
	}
	return out
}

// buildWorkReport: the month's days from the edits (those before the month
// may still count into its first minutes; give them from Gap before it)
func buildWorkReport(events []workEvent, month string, o workOptions) (*workReport, error) {
	o.defaults()
	from, until, err := workMonth(month, o.Loc)
	if err != nil {
		return nil, err
	}
	rep := &workReport{Month: month, Zone: o.Loc.String(), GapMin: int(o.Gap / time.Minute), LeadMin: int(o.Lead / time.Minute), Days: []*workDay{}}
	days := map[string]*workDay{}
	day := func(t time.Time) *workDay {
		k := t.In(o.Loc).Format("2006-01-02")
		d := days[k]
		if d == nil {
			d = &workDay{Date: k, Sessions: []workSpan{}, Topics: []*workTopic{}, topics: map[string]*workTopic{}}
			days[k] = d
		}
		return d
	}
	// the sittings as spans (cut at midnight below)
	var spans []workSpan
	for _, s := range workSlots(events, o) {
		a, b := s.Start, s.End
		if a.Before(from) {
			a = from
		}
		if b.After(until) {
			b = until
		}
		// an edit saved together with the one before counts no time, but
		// is listed
		if n := len(spans); a.Before(b) && (s.first || n == 0 || spans[n-1].End < a.UnixMilli()) {
			spans = append(spans, workSpan{a.UnixMilli(), b.UnixMilli()})
		} else if a.Before(b) && b.UnixMilli() > spans[n-1].End {
			spans[n-1].End = b.UnixMilli()
		}
		if !s.End.Before(from) && s.End.Before(until) {
			d := day(s.End)
			d.Edits++
			rep.Edits++
		}
		// the slot day by day
		for a.Before(b) {
			y, m, dd := a.In(o.Loc).Date()
			midnight := time.Date(y, m, dd+1, 0, 0, 0, 0, o.Loc)
			end := b
			if midnight.Before(end) {
				end = midnight
			}
			secs := end.Sub(a).Seconds()
			d := day(a)
			d.secs += secs
			topics := map[string]workItem{}
			var order []string
			for _, it := range s.ev.Items {
				if _, ok := topics[it.Topic]; !ok {
					order = append(order, it.Topic)
					topics[it.Topic] = it
				}
			}
			for _, id := range order {
				it := topics[id]
				tp := d.topics[id]
				if tp == nil {
					tp = &workTopic{ID: id, Title: it.TopicTitle, Decks: []workDeck{}, decks: map[string]bool{}}
					d.topics[id] = tp
					d.Topics = append(d.Topics, tp)
				}
				if tp.Title == "" {
					tp.Title = it.TopicTitle
				}
				tp.secs += secs / float64(len(order))
			}
			a = end
		}
		// what each touched, and how many edits, on the edit's own day
		if !s.End.Before(from) && s.End.Before(until) {
			d := day(s.End)
			seen := map[string]bool{}
			for _, it := range s.ev.Items {
				tp := d.topics[it.Topic]
				if tp == nil {
					tp = &workTopic{ID: it.Topic, Title: it.TopicTitle, Decks: []workDeck{}, decks: map[string]bool{}}
					d.topics[it.Topic] = tp
					d.Topics = append(d.Topics, tp)
				}
				if !seen[it.Topic] {
					seen[it.Topic] = true
					tp.Edits++
				}
				if it.Deck != "" && !tp.decks[it.Deck] {
					tp.decks[it.Deck] = true
					tp.Decks = append(tp.Decks, workDeck{ID: it.Deck, Name: it.DeckName})
				}
			}
		}
	}
	// spans into the days they are on (a span is cut at midnight)
	for _, sp := range spans {
		a, b := time.UnixMilli(sp.Start), time.UnixMilli(sp.End)
		for a.Before(b) {
			y, m, dd := a.In(o.Loc).Date()
			midnight := time.Date(y, m, dd+1, 0, 0, 0, 0, o.Loc)
			end := b
			if midnight.Before(end) {
				end = midnight
			}
			d := day(a)
			d.Sessions = append(d.Sessions, workSpan{a.UnixMilli(), end.UnixMilli()})
			rep.Sessions++
			a = end
		}
	}
	keys := make([]string, 0, len(days))
	for k := range days {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var total float64
	for _, k := range keys {
		d := days[k]
		if d.secs <= 0 && d.Edits == 0 {
			continue
		}
		d.Minutes = roundMinutes(d.secs)
		total += d.secs
		for _, tp := range d.Topics {
			tp.Minutes = roundMinutes(tp.secs)
		}
		// most time first, then by title
		sort.SliceStable(d.Topics, func(i, j int) bool {
			if d.Topics[i].secs != d.Topics[j].secs {
				return d.Topics[i].secs > d.Topics[j].secs
			}
			return d.Topics[i].Title < d.Topics[j].Title
		})
		rep.Days = append(rep.Days, d)
	}
	rep.Minutes = roundMinutes(total)
	return rep, nil
}

func roundMinutes(secs float64) int {
	return int(secs/60 + 0.5)
}
