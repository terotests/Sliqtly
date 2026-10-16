// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's entry types (assets/warden-types.json): what an entry beyond a
// plain event can be, the fields it carries, and its cost, which goes to the
// space's budget. An entry is a document of warden_events like any event; its
// type, its fields and its cost are kept beside the event's own:
//
//	type    a type's id ("" or "event": a plain event)
//	fields  {key: value}, checked against the type's fields
//	cost    {amount, payer, scope, instalments, first, every_months}
//
// A type of kind "record" (a thing, a supply, a contact) needs no day; one of
// kind "deadline" (a passport's or an insurance's last day) shows as due
// remind_days before its day.

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

//go:embed assets/warden-types.json
var wardenTypesJSON []byte

type wardenText map[string]string

type wardenField struct {
	Key     string                `json:"key"`
	Type    string                `json:"type"`
	Name    wardenText            `json:"name"`
	Options []string              `json:"options,omitempty"`
	Labels  map[string]wardenText `json:"labels,omitempty"`
}

type wardenType struct {
	ID      string        `json:"id"`
	Kind    string        `json:"kind"`
	Packs   []string      `json:"packs"`
	Icon    string        `json:"icon"`
	Repeats bool          `json:"repeats,omitempty"`
	Name    wardenText    `json:"name"`
	Help    wardenText    `json:"help,omitempty"`
	Fields  []wardenField `json:"fields"`
	Cost    wardenText    `json:"cost,omitempty"`
}

var wardenTypes = func() []wardenType {
	var f struct {
		Types []wardenType `json:"types"`
	}
	if err := json.Unmarshal(wardenTypesJSON, &f); err != nil {
		panic("assets/warden-types.json: " + err.Error())
	}
	return f.Types
}()

var wardenFieldTypes = []string{"text", "longtext", "date", "time", "number", "money", "bool", "choice", "ward", "person", "phone", "email", "url", "months"}

var wardenPhoneRe = regexp.MustCompile(`^\+?[0-9 ()./-]{3,40}$`)

const (
	wardenMaxCost        = 1e7 // euros of one cost
	wardenMaxInstalments = 120
	wardenRemindDays     = 30 // a deadline is due this many days before, unless it says
)

func wardenTypeOf(id string) *wardenType {
	for i := range wardenTypes {
		if wardenTypes[i].ID == id {
			return &wardenTypes[i]
		}
	}
	return nil
}

// the types a vocabulary pack offers; "demo" shows them all
func wardenTypesFor(vocab string) []wardenType {
	out := []wardenType{}
	for _, t := range wardenTypes {
		if vocab == "" || vocab == "demo" || slices.Contains(t.Packs, "*") || slices.Contains(t.Packs, vocab) {
			out = append(out, t)
		}
	}
	return out
}

func (sp *wardenSpace) typeIDs() []string {
	ids := []string{}
	for _, t := range wardenTypesFor(docStr(sp.doc, "vocab")) {
		ids = append(ids, t.ID)
	}
	return ids
}

// the type an entry names, offered by the space's pack
func (sp *wardenSpace) entryType(id string) (*wardenType, error) {
	if id == "" {
		id = "event"
	}
	t := wardenTypeOf(id)
	if t == nil || !slices.Contains(sp.typeIDs(), id) {
		return nil, roomErr{"type is one of " + strings.Join(sp.typeIDs(), ", ") + " (list_entry_types describes them)"}
	}
	return t, nil
}

func (f wardenField) choices() string { return strings.Join(f.Options, ", ") }

// one field's value, checked; nil for an empty one
func (sp *wardenSpace) fieldValue(f wardenField, v any, newWards *bool) (any, error) {
	bad := func(what string) (any, error) {
		return nil, roomErr{fmt.Sprintf("fields.%s is %s", f.Key, what)}
	}
	text := func(max int) (string, bool) {
		s, ok := v.(string)
		if !ok {
			return "", false
		}
		s = strings.TrimSpace(s)
		return s, len([]rune(s)) <= max
	}
	num := func() (float64, bool) {
		n, ok := v.(float64)
		if !ok {
			if i, ok := v.(int); ok {
				n = float64(i)
			} else if s, ok := v.(string); ok {
				if _, err := fmt.Sscanf(strings.Replace(strings.TrimSpace(s), ",", ".", 1), "%g", &n); err != nil {
					return 0, false
				}
			} else {
				return 0, false
			}
		}
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	}
	if s, ok := v.(string); v == nil || (ok && strings.TrimSpace(s) == "") {
		return nil, nil
	}
	switch f.Type {
	case "text", "person", "longtext":
		max := 200
		if f.Type == "person" {
			max = 60
		}
		if f.Type == "longtext" {
			max = 2000
		}
		s, ok := text(max)
		if !ok {
			return bad(fmt.Sprintf("text of at most %d characters", max))
		}
		return s, nil
	case "date":
		s, _ := text(10)
		if !wardenDate(s) {
			return bad("a day as YYYY-MM-DD")
		}
		return s, nil
	case "time":
		s, _ := text(5)
		if !wardenTimeRe.MatchString(s) {
			return bad("a time as HH:MM")
		}
		return s, nil
	case "number", "money":
		n, ok := num()
		if !ok || n < 0 || n > wardenMaxCost {
			return bad("a number from 0 to 10000000")
		}
		if f.Type == "money" {
			n = math.Round(n*100) / 100
		}
		return n, nil
	case "months":
		n, ok := num()
		if !ok || n != math.Trunc(n) || n < 1 || n > 600 {
			return bad("a whole number of months from 1 to 600")
		}
		return n, nil
	case "bool":
		b, ok := v.(bool)
		if !ok {
			return bad("true or false")
		}
		return b, nil
	case "choice":
		s, _ := text(40)
		s = strings.ToLower(s)
		if !slices.Contains(f.Options, s) {
			return bad("one of " + f.choices())
		}
		return s, nil
	case "ward":
		s, ok := text(60)
		if !ok {
			return bad("a ward's id or name")
		}
		id := sp.wardID(s)
		if id == "" {
			w, err := sp.addWard(cleanName(s), "")
			if err != nil {
				return nil, err
			}
			id, *newWards = docStr(w, "id"), true
		}
		return id, nil
	case "phone":
		s, _ := text(40)
		if !wardenPhoneRe.MatchString(s) {
			return bad("a phone number")
		}
		return s, nil
	case "email":
		s, _ := text(200)
		if !wardenEmailRe.MatchString(s) {
			return bad("an e-mail address")
		}
		return s, nil
	case "url":
		s, _ := text(1000)
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return bad("a web address, https://…")
		}
		return s, nil
	}
	return bad("not a known kind of field")
}

// the type, fields and cost of an entry, checked into d; base is what the
// entry had before (its fields are kept unless changed)
func (sp *wardenSpace) entryDoc(a map[string]any, d Doc, newWards *bool) error {
	tid := docStr(d, "type")
	if v, ok := a["type"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return roomErr{"type is a type's id"}
		}
		tid = strings.ToLower(strings.TrimSpace(s))
	}
	t, err := sp.entryType(tid)
	if err != nil {
		return err
	}
	if t.ID == "event" {
		delete(d, "type")
	} else {
		d["type"] = t.ID
	}
	// fields: the type's only; those a changed type no longer has are dropped
	old, _ := d["fields"].(map[string]any)
	fields := map[string]any{}
	for _, f := range t.Fields {
		if v, ok := old[f.Key]; ok {
			fields[f.Key] = v
		}
	}
	if v, ok := a["fields"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return roomErr{"fields is an object of the type's fields"}
		}
		for k, v := range m {
			i := slices.IndexFunc(t.Fields, func(f wardenField) bool { return f.Key == k })
			if i < 0 {
				keys := []string{}
				for _, f := range t.Fields {
					keys = append(keys, f.Key)
				}
				if len(keys) == 0 {
					return roomErr{"fields: a " + t.ID + " has none"}
				}
				return roomErr{fmt.Sprintf("fields.%s is not a field of %s: %s", k, t.ID, strings.Join(keys, ", "))}
			}
			val, err := sp.fieldValue(t.Fields[i], v, newWards)
			if err != nil {
				return err
			}
			if val == nil {
				delete(fields, k)
			} else {
				fields[k] = val
			}
		}
	}
	if len(fields) > 0 {
		d["fields"] = fields
	} else {
		delete(d, "fields")
	}
	if v, ok := a["cost"]; ok {
		if v == nil {
			delete(d, "cost")
		} else {
			c, err := wardenCost(v)
			if err != nil {
				return err
			}
			d["cost"] = c
		}
	}
	if t.Kind != "record" && !wardenDate(docStr(d, "date")) {
		return roomErr{"date is missing: the day as YYYY-MM-DD"}
	}
	return nil
}

func wardenCost(v any) (map[string]any, error) {
	m, ok := v.(map[string]any)
	bad := func(why string) (map[string]any, error) { return nil, roomErr{"cost: " + why} }
	if !ok {
		return bad("an object {amount, payer, scope, instalments, first, every_months}")
	}
	f := wardenField{Key: "amount", Type: "money"}
	amt, err := (&wardenSpace{}).fieldValue(f, m["amount"], new(bool))
	if err != nil || amt == nil {
		return bad("amount is the price in euros, a number")
	}
	c := map[string]any{"amount": amt}
	payer := cleanName(argStr(m, "payer"))
	if len([]rune(payer)) > 60 {
		return bad("payer is a name of at most 60 characters")
	}
	if payer != "" {
		c["payer"] = payer
	}
	switch s := strings.ToLower(argStr(m, "scope")); s {
	case "", "shared":
		c["scope"] = "shared"
	case "personal":
		if payer == "" {
			return bad("a personal cost names its payer")
		}
		c["scope"] = s
	default:
		return bad("scope is shared or personal")
	}
	for _, k := range []struct {
		key string
		max int
	}{{"instalments", wardenMaxInstalments}, {"every_months", 12}} {
		n := 1
		if v, ok := m[k.key]; ok && v != nil {
			x, ok := v.(float64)
			if !ok || x != math.Trunc(x) || x < 1 || x > float64(k.max) {
				return bad(fmt.Sprintf("%s is a whole number from 1 to %d", k.key, k.max))
			}
			n = int(x)
		}
		c[k.key] = n
	}
	if s := argStr(m, "first"); s != "" {
		if !wardenDate(s) {
			return bad("first is the day of the first payment, YYYY-MM-DD")
		}
		c["first"] = s
	}
	return c, nil
}

func wardenNum(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

// the day a deadline starts to show as due
func wardenRemindFrom(d Doc) string {
	t := wardenTypeOf(docStr(d, "type"))
	day, err := time.Parse("2006-01-02", docStr(d, "date"))
	if t == nil || t.Kind != "deadline" || err != nil {
		return ""
	}
	n := wardenRemindDays
	if f, _ := d["fields"].(map[string]any); f != nil {
		if v, ok := f["remind_days"]; ok {
			n = int(wardenNum(v))
		}
	}
	return day.AddDate(0, 0, -n).Format("2006-01-02")
}

// the type's part of an entry's answer
func wardenEntryJSON(out map[string]any, d Doc) {
	if t := docStr(d, "type"); t != "" {
		out["type"] = t
	}
	if f, _ := d["fields"].(map[string]any); len(f) > 0 {
		out["fields"] = f
	}
	if c, _ := d["cost"].(map[string]any); c != nil {
		out["cost"] = c
	}
	if r := wardenRemindFrom(d); r != "" {
		out["remind_from"] = r
	}
}

// a thing to sell without a label gets the space's next number
func (sp *wardenSpace) saleLabel(d Doc) bool {
	if docStr(d, "type") != "sale_item" {
		return false
	}
	f, _ := d["fields"].(map[string]any)
	if f == nil {
		f = map[string]any{}
	}
	if docStr(f, "label") != "" {
		return false
	}
	n := int(wardenNum(sp.doc["sale_seq"])) + 1
	sp.doc["sale_seq"] = n
	f["label"] = fmt.Sprintf("%03d", n)
	d["fields"] = f
	return true
}

// list_entry_types: the types the space's pack offers (all without a space)
func (s *wardenService) listEntryTypes(ctx context.Context, who wardenWho, a map[string]any) (any, error) {
	vocab := ""
	if id := argStr(a, "space_id"); id != "" {
		sp, err := s.space(ctx, who, id)
		if err != nil {
			return nil, err
		}
		vocab = docStr(sp.doc, "vocab")
	}
	var about struct {
		About string `json:"about"`
	}
	json.Unmarshal(wardenTypesJSON, &about)
	return map[string]any{"about": about.About, "types": wardenTypesFor(vocab),
		"cost": "cost {amount (euros), payer (a name), scope shared|personal (a personal cost is in its payer's own budget and still listed in the group's spending), instalments (paid in this many parts, default 1), first (the first payment's day; the entry's day by default), every_months (months between parts, default 1)}"}, nil
}

// ----------------------------------------------------------------- budget --

func wardenMonth(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01", s)
	return t, err == nil && len(s) == 7
}

// budget: the instalments of the entries' costs, month by month
func (s *wardenService) budget(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	now := s.now()
	from, to := argStr(a, "from"), argStr(a, "to")
	if from == "" {
		from = now.Format("2006-01")
	}
	f, ok1 := wardenMonth(from)
	if to == "" {
		to = f.AddDate(0, 11, 0).Format("2006-01")
	}
	t, ok2 := wardenMonth(to)
	if !ok1 || !ok2 || t.Before(f) || t.After(f.AddDate(5, 0, 0)) {
		return nil, roomErr{"from and to are months as YYYY-MM, at most five years apart"}
	}
	payer := cleanName(argStr(a, "payer"))
	docs, ids, err := s.events(ctx, sp)
	if err != nil {
		return nil, err
	}
	type line struct {
		month string
		m     map[string]any
	}
	lines := []line{}
	for i, d := range docs {
		c, _ := d["cost"].(map[string]any)
		if c == nil {
			continue
		}
		who := docStr(c, "payer")
		if payer != "" && !strings.EqualFold(who, payer) {
			continue
		}
		first := docStr(c, "first")
		if first == "" {
			first = docStr(d, "date")
		}
		if first == "" {
			first = time.UnixMilli(int64(wardenNum(d["updated"]))).In(time.UTC).Format("2006-01-02")
		}
		day, err := time.Parse("2006-01-02", first)
		if err != nil {
			continue
		}
		n, every := int(wardenNum(c["instalments"])), int(wardenNum(c["every_months"]))
		if n < 1 {
			n = 1
		}
		if every < 1 {
			every = 1
		}
		total := wardenNum(c["amount"])
		part := math.Floor(total/float64(n)*100) / 100
		for k := 0; k < n; k++ {
			amt := part
			if k == n-1 {
				amt = math.Round((total-part*float64(n-1))*100) / 100
			}
			at := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, k*every, 0)
			if at.Before(f) || at.After(t) {
				continue
			}
			pd := at.AddDate(0, 0, day.Day()-1)
			if pd.Month() != at.Month() {
				pd = at.AddDate(0, 1, -1)
			}
			lines = append(lines, line{at.Format("2006-01"), map[string]any{
				"event_id": ids[i], "title": docStr(d, "title"), "type": wardenOr(docStr(d, "type"), "event"),
				"date": pd.Format("2006-01-02"), "amount": amt, "payer": who, "scope": docStr(c, "scope"), "n": k + 1, "of": n,
			}})
		}
	}
	sort.SliceStable(lines, func(i, j int) bool { return docStr(lines[i].m, "date") < docStr(lines[j].m, "date") })
	type sum struct {
		total, shared float64
		byPayer       map[string]float64
	}
	add := func(s *sum, m map[string]any) {
		amt := wardenNum(m["amount"])
		s.total += amt
		if docStr(m, "scope") != "personal" {
			s.shared += amt
		}
		if p := docStr(m, "payer"); p != "" {
			s.byPayer[p] += amt
		}
	}
	round := func(v float64) float64 { return math.Round(v*100) / 100 }
	jsonOf := func(s *sum) map[string]any {
		bp := map[string]any{}
		for k, v := range s.byPayer {
			bp[k] = round(v)
		}
		return map[string]any{"total": round(s.total), "shared": round(s.shared), "by_payer": bp}
	}
	all := &sum{byPayer: map[string]float64{}}
	months := []any{}
	for m := f; !m.After(t); m = m.AddDate(0, 1, 0) {
		key := m.Format("2006-01")
		ms := &sum{byPayer: map[string]float64{}}
		ls := []any{}
		for _, l := range lines {
			if l.month == key {
				add(ms, l.m)
				add(all, l.m)
				ls = append(ls, l.m)
			}
		}
		out := jsonOf(ms)
		out["month"], out["lines"] = key, ls
		months = append(months, out)
	}
	out := jsonOf(all)
	out["from"], out["to"], out["months"], out["currency"] = from, to, months, "EUR"
	if payer != "" {
		out["payer"] = payer
	}
	return out, nil
}

func wardenOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
