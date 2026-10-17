// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's questions: one member asks another something (does the care
// time suit, can you pick the children up from school on Tuesday, can you
// pay 40 € to even out the budget) and the other answers yes or no, or picks
// one of the options. A question is an entry of type "question" (the page's
// Questions view lists them, not the lists or the Add menu); its fields say
// what is asked, of whom and by whom, and the answer when it comes.
//
// A yes makes what was asked for, linked with the question both ways
// (wardenlinks.go): a calendar entry (a care shift, a school run, an event),
// a purchase for the one asked to buy, or a budget settlement in the books,
// which the budget lists apart from the costs (wardentypes.go).
//
//	ask_question     the asker: kind, to whom (or everyone else), the day,
//	                 the times, the amount…; with event_id, an open one anew
//	answer_question  the one asked: yes, no, an option or a suggestion
//	list_questions   the open and answered questions, to and by the caller

package main

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// the kinds of question, as the type's ask field has them
var wardenAskKinds = []string{"free", "care_time", "take_kids", "buy", "event", "suggest", "payment", "school", "hobby"}

// ask_to_uid of a question asked of everyone else in the space
const wardenAll = "*"

// "", all, everyone, kaikki…: the question is for every other member
func wardenAskAll(to string) bool {
	return slices.Contains([]string{"", "*", "all", "everyone", "everyone else", "others", "kaikki", "kaikki muut", "muut"}, strings.ToLower(strings.TrimSpace(to)))
}

// en or fi by the space's language
func (sp *wardenSpace) lang2(en, fi string) string {
	if sp.lang() == "fi" {
		return fi
	}
	return en
}

// the member a name, an address or a uid names; "" when none does
func (sp *wardenSpace) memberUID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	people, _ := sp.doc["people"].(map[string]any)
	for _, uid := range docStrs(sp.doc, "members") {
		p, _ := people[uid].(map[string]any)
		if uid == v || strings.EqualFold(docStr(p, "email"), v) || strings.EqualFold(docStr(p, "name"), v) {
			return uid
		}
	}
	// a first name names the member whose name starts with it, when only one does
	found := ""
	for _, uid := range docStrs(sp.doc, "members") {
		p, _ := people[uid].(map[string]any)
		first, _, _ := strings.Cut(docStr(p, "name"), " ")
		if strings.EqualFold(first, v) {
			if found != "" {
				return ""
			}
			found = uid
		}
	}
	return found
}

// a member's name as the space knows it
func (sp *wardenSpace) nameOf(uid string) string {
	people, _ := sp.doc["people"].(map[string]any)
	p, _ := people[uid].(map[string]any)
	if n := docStr(p, "name"); n != "" {
		return n
	}
	if uid == sp.who.uid {
		return cleanName(sp.who.name)
	}
	return ""
}

func (sp *wardenSpace) lang() string {
	if docStr(sp.doc, "lang") == "en" {
		return "en"
	}
	return "fi"
}

func (sp *wardenSpace) today(now time.Time) string {
	if l, err := time.LoadLocation(wardenOr(docStr(sp.doc, "tz"), wardenTZ)); err == nil {
		now = now.In(l)
	}
	return now.Format("2006-01-02")
}

// "la 18.10." or "Sat 18 Oct"
func wardenDayText(lang, day string) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	if lang == "en" {
		return t.Format("Mon 2 Jan")
	}
	wd := []string{"su", "ma", "ti", "ke", "to", "pe", "la"}[t.Weekday()]
	return fmt.Sprintf("%s %d.%d.", wd, t.Day(), int(t.Month()))
}

func wardenMoneyText(lang string, v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimSuffix(s, ".00")
	if lang == "fi" {
		s = strings.ReplaceAll(s, ".", ",")
	}
	return s + " €"
}

// the question in words, from what it asks, when the asker gave none
func wardenAskTitle(lang, kind string, f map[string]any, ward string) string {
	fi := lang == "fi"
	pick := func(en, fiText string) string {
		if fi {
			return fiText
		}
		return en
	}
	when := ""
	if d := docStr(f, "on_date"); d != "" {
		when = " " + wardenDayText(lang, d)
		if u := docStr(f, "until_date"); u != "" && u != d {
			when += "–" + wardenDayText(lang, u)
		}
	}
	if t := docStr(f, "from_time"); t != "" {
		when += pick(" at ", " klo ") + t
		if e := docStr(f, "to_time"); e != "" {
			when += "–" + e
		}
	}
	if h := wardenNum(f["hours"]); h > 0 {
		when += " " + strconv.FormatFloat(h, 'f', -1, 64) + pick(" hours", " tunniksi")
	}
	who := pick("the children", "lapset")
	if ward != "" {
		who = ward
	}
	what := docStr(f, "what")
	switch kind {
	case "care_time":
		return pick("Does the care time suit:", "Käykö hoitoaika:") + when + "?"
	case "take_kids":
		return pick("Can you look after ", "Voitko ottaa hoitoon: ") + who + when + "?"
	case "buy":
		return pick("Can you buy: ", "Voitko ostaa: ") + wardenOr(what, pick("this", "tämän")) + when + "?"
	case "event":
		return pick("May I take ", "Käykö, että vien: ") + who + pick(" to ", " – ") + wardenOr(what, pick("an event", "tapahtuma")) + when + "?"
	case "suggest":
		return pick("What day or time would suit: ", "Mikä päivä tai aika sopisi: ") + wardenOr(what, pick("this", "tämä")) + "?"
	case "payment":
		return pick("Can you pay ", "Voitko maksaa ") + wardenMoneyText(lang, wardenNum(f["amount"])) + pick(" to even out the budget?", " budjetin tasaukseksi?")
	case "school", "hobby":
		place := what
		if place == "" {
			place = pick("school", "koulu")
			if kind == "hobby" {
				place = pick("the hobby", "harrastus")
			}
		}
		verb := map[string]string{"take": pick("take ", "viedä: "), "pick": pick("pick up ", "hakea: "), "both": pick("take and pick up ", "viedä ja hakea: ")}[wardenOr(docStr(f, "direction"), "take")]
		return pick("Can you ", "Voitko ") + verb + who + " (" + place + ")" + when + "?"
	}
	return wardenOr(what, pick("A question", "Kysymys")) + when + "?"
}

// ask_question: a question from the caller to another member
func (s *wardenService) askQuestion(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	kind := wardenOr(strings.ToLower(argStr(a, "kind")), "free")
	if !slices.Contains(wardenAskKinds, kind) {
		return nil, roomErr{"kind is one of " + strings.Join(wardenAskKinds, ", ")}
	}
	// an open question of the caller's own is asked again in new words
	id := argStr(a, "event_id")
	var was map[string]any
	if id != "" {
		d, err := s.event(ctx, sp, id)
		if err != nil {
			return nil, err
		}
		if docStr(d, "type") != "question" {
			return nil, roomErr{"event_id is not a question"}
		}
		was, _ = d["fields"].(map[string]any)
		if docStr(was, "asked_by_uid") != sp.who.uid {
			return nil, roomErr{"only " + docStr(was, "asked_by") + " changes this question"}
		}
		if docStr(was, "answer") != "" {
			return nil, roomErr{"it has been answered already: " + docStr(was, "answer")}
		}
	}
	to := strings.TrimSpace(argStr(a, "to"))
	f := map[string]any{"ask": kind, "asked_by": sp.nameOf(sp.who.uid), "asked_by_uid": sp.who.uid, "status": "planned"}
	if wardenAskAll(to) {
		// everyone else in the space: the first to answer does
		f["ask_to"], f["ask_to_uid"] = sp.lang2("Everyone else", "Kaikki muut"), wardenAll
	} else if uid := sp.memberUID(to); uid != "" {
		if uid == sp.who.uid {
			return nil, roomErr{"to: ask another member, not yourself"}
		}
		f["ask_to"], f["ask_to_uid"] = sp.nameOf(uid), uid
	} else {
		f["ask_to"] = cleanName(to)
	}
	if docStr(f, "ask_to") == "" {
		f["ask_to"] = cleanName(to)
	}
	for _, k := range []string{"what", "date", "until", "start", "end", "direction", "answer_by"} {
		key := map[string]string{"date": "on_date", "until": "until_date", "start": "from_time", "end": "to_time"}[k]
		if key == "" {
			key = k
		}
		if v := strings.TrimSpace(argStr(a, k)); v != "" {
			f[key] = v
		}
	}
	for _, k := range []string{"hours", "amount"} {
		if v, ok := argNum(a, k); ok && v != 0 {
			f[k] = v
		}
	}
	if l, ok := a["options"].([]any); ok && len(l) > 0 {
		f["options"] = l
	}
	f["add_to_calendar"] = argBool(a, "add_to_calendar", kind != "buy" && kind != "payment")
	// what each kind needs
	need := map[string][]string{
		"care_time": {"on_date"}, "take_kids": {"on_date"}, "event": {"on_date"},
		"school": {"on_date"}, "hobby": {"on_date"}, "buy": {"what"}, "payment": {"amount"},
	}[kind]
	names := map[string]string{"on_date": "date", "what": "what", "amount": "amount"}
	for _, k := range need {
		if f[k] == nil {
			return nil, roomErr{fmt.Sprintf("%s needs %s", kind, names[k])}
		}
	}
	if kind == "payment" && wardenNum(f["amount"]) <= 0 {
		return nil, roomErr{"amount is a sum in euros, more than 0"}
	}
	if kind == "free" && argStr(a, "question") == "" && docStr(f, "what") == "" {
		return nil, roomErr{"question: what is asked"}
	}
	ward := ""
	if w := argStr(a, "ward"); w != "" {
		id := sp.wardID(w)
		if id == "" {
			return nil, roomErr{"no such ward: " + w}
		}
		for _, m := range docMaps(sp.doc, "wards") {
			if docStr(m, "id") == id {
				ward = docStr(m, "name")
			}
		}
		a = wardenWith(a, map[string]any{"ward": id})
	}
	title := strings.TrimSpace(argStr(a, "question"))
	if title == "" {
		title = wardenAskTitle(sp.lang(), kind, f, ward)
	}
	args := map[string]any{"title": title, "type": "question", "fields": f}
	for _, k := range []string{"ward", "place", "notes"} {
		if v := argStr(a, k); v != "" || id != "" {
			args[k] = v
		}
	}
	if id != "" {
		args["event_id"] = id
		// what the new words leave out goes
		for k := range was {
			if _, ok := f[k]; !ok && !strings.HasPrefix(k, "answer") {
				f[k] = nil
			}
		}
	}
	out, err := s.saveEvent(ctx, sp, args)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// the answer as a word: yes, no, the option picked, or a suggestion
func wardenAnswerWord(v string, options []string, kind string) (string, bool, error) {
	low := strings.ToLower(strings.Trim(strings.TrimSpace(v), ".!"))
	if slices.Contains([]string{"no", "ei", "en", "ei käy", "ei sovi", "en voi", "ei onnistu"}, low) {
		return "no", false, nil
	}
	for _, o := range options {
		if strings.EqualFold(strings.TrimSpace(o), strings.TrimSpace(v)) {
			return o, true, nil
		}
	}
	if slices.Contains([]string{"yes", "kyllä", "joo", "ok", "käy", "sopii", "voin", "onnistuu", "kyl"}, low) {
		if len(options) > 0 {
			return "", false, roomErr{"answer: one of the options (" + strings.Join(options, ", ") + ") or no"}
		}
		return "yes", true, nil
	}
	if kind == "suggest" && low != "" {
		return strings.TrimSpace(v), true, nil
	}
	if len(options) > 0 {
		return "", false, roomErr{"answer: one of the options (" + strings.Join(options, ", ") + ") or no"}
	}
	return "", false, roomErr{"answer is yes or no"}
}

// answer_question: the one asked answers; a yes makes what was asked for
func (s *wardenService) answerQuestion(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	id := argStr(a, "event_id")
	d, err := s.event(ctx, sp, id)
	if err != nil {
		return nil, err
	}
	if docStr(d, "type") != "question" {
		return nil, roomErr{"event_id is not a question"}
	}
	f, _ := d["fields"].(map[string]any)
	if f == nil {
		f = map[string]any{}
	}
	if docStr(f, "answer") != "" {
		return nil, roomErr{"it has been answered already: " + docStr(f, "answer")}
	}
	if uid := docStr(f, "ask_to_uid"); uid == wardenAll {
		if docStr(f, "asked_by_uid") == sp.who.uid {
			return nil, roomErr{"the others answer this question, not the one who asked"}
		}
	} else if uid != "" && uid != sp.who.uid {
		return nil, roomErr{"only " + docStr(f, "ask_to") + " answers this question"}
	}
	kind := wardenOr(docStr(f, "ask"), "free")
	word, yes, err := wardenAnswerWord(argStr(a, "answer"), docStrs(f, "options"), kind)
	if err != nil {
		return nil, err
	}
	today := sp.today(s.now())
	upd := map[string]any{"answer": word, "answered_by": wardenOr(sp.nameOf(sp.who.uid), cleanName(sp.who.name)), "answered_on": today, "status": "cancelled"}
	if yes {
		upd["status"] = "confirmed"
	}
	if n := strings.TrimSpace(argStr(a, "note")); n != "" {
		upd["answer_note"] = n
	}
	// a suggestion's day and time, or the day the option picked
	for k, key := range map[string]string{"date": "on_date", "start": "from_time", "end": "to_time"} {
		if v := strings.TrimSpace(argStr(a, k)); v != "" && yes {
			upd[key] = v
		}
	}
	out, err := s.saveEvent(ctx, sp, map[string]any{"event_id": id, "fields": upd})
	if err != nil {
		return nil, err
	}
	res := map[string]any{"question": out, "answer": word}
	if !yes {
		return res, nil
	}
	merged := map[string]any{}
	for k, v := range f {
		merged[k] = v
	}
	for k, v := range upd {
		merged[k] = v
	}
	if docStr(f, "ask_to_uid") == wardenAll {
		// asked of everyone: the one who said yes does it
		merged["ask_to"] = upd["answered_by"]
	}
	made, err := s.questionResult(ctx, sp, id, d, merged, kind, word)
	if err != nil {
		// the answer stands; what it should have made is said
		res["not_made"] = err.Error()
		return res, nil
	}
	if made != nil {
		res["made"] = made
	}
	return res, nil
}

// what a yes makes: nil when the question asks for nothing to be kept
func (s *wardenService) questionResult(ctx context.Context, sp *wardenSpace, qid string, q Doc, f map[string]any, kind, word string) (any, error) {
	lang := sp.lang()
	pick := func(en, fi string) string {
		if lang == "fi" {
			return fi
		}
		return en
	}
	to, by := docStr(f, "ask_to"), docStr(f, "asked_by")
	note := pick("Agreed by a question: ", "Sovittu kyselyllä: ") + docStr(q, "title")
	if word != "yes" {
		note += " (" + word + ")"
	}
	if n := docStr(f, "answer_note"); n != "" {
		note += "\n" + n
	}
	args := map[string]any{"links": []any{qid}, "notes": note}
	if w := docStr(q, "ward"); w != "" {
		args["ward"] = w
	}
	if p := docStr(q, "place"); p != "" {
		args["place"] = p
	}
	what := docStr(f, "what")
	calendar, _ := f["add_to_calendar"].(bool)
	day := docStr(f, "on_date")
	if kind == "payment" {
		args["type"] = "budget_settlement"
		args["title"] = pick("Budget settlement: ", "Budjetin tasaus: ") + to + " → " + by
		args["date"] = sp.today(s.now())
		args["fields"] = map[string]any{"amount": wardenNum(f["amount"]), "from_person": to, "to_person": by}
		return s.saveEvent(ctx, sp, args)
	}
	if kind == "buy" {
		args["type"] = "purchase"
		args["title"] = wardenOr(what, docStr(q, "title"))
		fields := map[string]any{"buyer": to}
		if day != "" {
			fields["buy_by"] = day
		}
		if v := wardenNum(f["amount"]); v > 0 {
			fields["estimate"] = v
		}
		args["fields"] = fields
		return s.saveEvent(ctx, sp, args)
	}
	if !calendar || day == "" {
		return nil, nil
	}
	args["date"] = day
	start, end := docStr(f, "from_time"), docStr(f, "to_time")
	if h := wardenNum(f["hours"]); start != "" && end == "" && h > 0 {
		if t, err := time.Parse("15:04", start); err == nil {
			e := t.Add(time.Duration(math.Round(h*60)) * time.Minute)
			if e.Day() == t.Day() {
				end = e.Format("15:04")
			}
		}
	}
	if start != "" {
		args["start"] = start
		if end != "" {
			args["end"] = end
		}
	} else if u := docStr(f, "until_date"); u > day {
		args["end_date"] = u
	}
	switch kind {
	case "care_time", "take_kids":
		args["type"] = "childcare_shift"
		args["title"] = pick("Care: ", "Hoito: ") + to
		args["fields"] = map[string]any{"carer": to}
		args["who"] = []any{map[string]any{"name": to, "role": pick("looks after", "hoitaa")}}
	case "school", "hobby":
		place := what
		if place == "" {
			place = pick("School", "Koulu")
			if kind == "hobby" {
				place = pick("Hobby", "Harrastus")
			}
		}
		dir := wardenOr(docStr(f, "direction"), "take")
		roles := map[string][]string{"take": {pick("takes", "vie")}, "pick": {pick("picks up", "hakee")}, "both": {pick("takes", "vie"), pick("picks up", "hakee")}}[dir]
		verb := map[string]string{"take": pick("Take", "Vienti"), "pick": pick("Pick-up", "Haku"), "both": pick("Take and pick-up", "Vienti ja haku")}[dir]
		args["title"] = verb + ": " + place
		who := []any{}
		for _, r := range roles {
			who = append(who, map[string]any{"name": to, "role": r})
		}
		args["who"] = who
	case "event":
		args["title"] = wardenOr(what, docStr(q, "title"))
		args["who"] = []any{map[string]any{"name": by, "role": pick("takes", "vie")}}
	default:
		args["title"] = wardenOr(what, docStr(q, "title"))
		if kind == "suggest" && word != "yes" && what == "" {
			args["title"] = word
		}
		args["who"] = []any{map[string]any{"name": to, "role": pick("agreed", "sovittu")}}
	}
	// a who without a name is left out
	if l, ok := args["who"].([]any); ok {
		kept := []any{}
		for _, w := range l {
			if docStr(w.(map[string]any), "name") != "" {
				kept = append(kept, w)
			}
		}
		args["who"] = kept
	}
	return s.saveEvent(ctx, sp, args)
}

// list_questions: the questions of the space, the open ones first
func (s *wardenService) listQuestions(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	docs, ids, err := s.events(ctx, sp)
	if err != nil {
		return nil, err
	}
	open := argBool(a, "open_only", false)
	mine := strings.ToLower(argStr(a, "mine"))
	type row struct {
		open bool
		at   float64
		m    map[string]any
	}
	rows := []row{}
	for i, d := range docs {
		if docStr(d, "type") != "question" {
			continue
		}
		f, _ := d["fields"].(map[string]any)
		isOpen := docStr(f, "answer") == ""
		if open && !isOpen {
			continue
		}
		byMe := docStr(f, "asked_by_uid") == sp.who.uid
		toMe := docStr(f, "ask_to_uid") == sp.who.uid || (docStr(f, "ask_to_uid") == wardenAll && !byMe)
		if (mine == "to" && !toMe) || (mine == "by" && !byMe) {
			continue
		}
		m := wardenEventJSON(ids[i], d, s.now())
		m["open"] = isOpen
		m["to_me"] = toMe
		m["by_me"] = docStr(f, "asked_by_uid") == sp.who.uid
		rows = append(rows, row{isOpen, wardenNum(d["updated"]), m})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].open != rows[j].open {
			return rows[i].open
		}
		return rows[i].at > rows[j].at
	})
	out := []any{}
	for _, r := range rows {
		out = append(out, r.m)
	}
	return map[string]any{"space_id": sp.id, "questions": out}, nil
}

// the settlements between the from and to months: each line, and what
// each member paid (+) or received (−) in them
func wardenSettlements(docs []Doc, ids []string, from, to time.Time, who string) ([]any, map[string]float64) {
	lines := []any{}
	net := map[string]float64{}
	for i, d := range docs {
		if docStr(d, "type") != "budget_settlement" {
			continue
		}
		day, err := time.Parse("2006-01-02", docStr(d, "date"))
		if err != nil {
			continue
		}
		at := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
		if at.Before(from) || at.After(to) {
			continue
		}
		f, _ := d["fields"].(map[string]any)
		amt, a, b := wardenNum(f["amount"]), docStr(f, "from_person"), docStr(f, "to_person")
		if amt <= 0 || (who != "" && !strings.EqualFold(a, who) && !strings.EqualFold(b, who)) {
			continue
		}
		lines = append(lines, map[string]any{"event_id": ids[i], "title": docStr(d, "title"), "date": docStr(d, "date"), "amount": amt, "from": a, "to": b})
		net[a] += amt
		net[b] -= amt
	}
	sort.SliceStable(lines, func(i, j int) bool {
		return docStr(lines[i].(map[string]any), "date") < docStr(lines[j].(map[string]any), "date")
	})
	return lines, net
}
