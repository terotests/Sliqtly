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
	"strconv"
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
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Group   string   `json:"group"`
	Packs   []string `json:"packs"`
	Icon    string   `json:"icon"`
	Repeats bool     `json:"repeats,omitempty"`
	// the types whose days an entry of this one skips (a school day on a
	// holiday of the same ward), unless the day says school_open
	Skips  []string      `json:"skips,omitempty"`
	Name   wardenText    `json:"name"`
	Help   wardenText    `json:"help,omitempty"`
	Fields []wardenField `json:"fields"`
	Cost   wardenText    `json:"cost,omitempty"`
	// what an entry must have: date, start, end_date, ward or a field's key
	Required []string `json:"required"`
	// the money fields that put an entry without a cost into the budget,
	// the first one set counting
	Budget []string `json:"budget"`
	// the date fields ("date": the entry's day) that make reminders
	Reminders []wardenReminder `json:"reminders"`
	// the type's own window to get something in
	Get *wardenGet `json:"get,omitempty"`
	// the other types' lists an entry also belongs to while When holds
	AlsoIn []wardenAlso `json:"also,omitempty"`
	// the number fields that remind at once when they run low
	Low []wardenLow `json:"low,omitempty"`
	// the common fields the page's form shows for every entry, named here
	// too so a client reading one type sees them (wardenFormCommon)
	Also []string `json:"also_fields,omitempty"`
	// kept for entries already made; new ones take ReplacedBy
	// the page that shows its entries instead of the lists and the Add
	// menu ("questions": the Questions page, wardenask.go)
	Page       string `json:"page,omitempty"`
	Legacy     bool   `json:"legacy,omitempty"`
	ReplacedBy string `json:"replaced_by,omitempty"`
}

type wardenAlso struct {
	Type string     `json:"type"`
	When wardenCond `json:"when"`
}

// whether an entry of its own type, or one listed under typ by its type's also
func wardenListedAs(d map[string]any, typ string) bool {
	own := wardenOr(docStr(d, "type"), "event")
	if own == typ {
		return true
	}
	t := wardenTypeOf(own)
	if t == nil {
		return false
	}
	fields, _ := d["fields"].(map[string]any)
	for _, a := range t.AlsoIn {
		if a.Type == typ && a.When.holds(fields) {
			return true
		}
	}
	return false
}

// what a type gets by: the date field (or "date"), when the condition holds,
// until one of done does
type wardenGet struct {
	By   string       `json:"by"`
	When *wardenCond  `json:"when,omitempty"`
	Done []wardenCond `json:"done,omitempty"`
}

// a number field that is low at most At's value (Default when At is not set)
type wardenLow struct {
	Field   string  `json:"field"`
	At      string  `json:"at,omitempty"`
	Default float64 `json:"default"`
}

// the low fields of an entry: those set and at most their limit
func wardenLowOf(d Doc) []string {
	t := wardenTypeOf(docStr(d, "type"))
	fields, _ := d["fields"].(map[string]any)
	out := []string{}
	if t == nil || fields == nil {
		return out
	}
	for _, l := range t.Low {
		v, ok := fields[l.Field]
		if !ok {
			continue
		}
		at := l.Default
		if a, ok := fields[l.At]; ok && l.At != "" {
			at = wardenNum(a)
		}
		if wardenNum(v) <= at {
			out = append(out, l.Field)
		}
	}
	return out
}

// a field that is set (Is "") or has the value Is
type wardenCond struct {
	Field string `json:"field"`
	Is    string `json:"is,omitempty"`
}

func (c wardenCond) holds(fields map[string]any) bool {
	v, ok := fields[c.Field]
	if !ok || v == nil || v == "" || v == false {
		return false
	}
	return c.Is == "" || fmt.Sprint(v) == c.Is
}

type wardenReminder struct {
	Field string `json:"field"`
	Days  int    `json:"days"`
}

var wardenTypes, wardenCommon = func() ([]wardenType, []wardenField) {
	var f struct {
		Common []wardenField `json:"common"`
		Types  []wardenType  `json:"types"`
	}
	if err := json.Unmarshal(wardenTypesJSON, &f); err != nil {
		panic("assets/warden-types.json: " + err.Error())
	}
	for i := range f.Types {
		f.Types[i].Also = wardenFormCommon
	}
	return f.Types, f.Common
}()

// the common fields the page's entry form shows besides a type's own (its
// Hankinta and Budjetti sections, apps/spaces/src/SpacesApp.rgr commonField)
var wardenFormCommon = []string{"get_from", "get_by", "get_what", "estimated_cost_min", "estimated_cost_max", "estimated_cost", "actual_cost", "budget_scope", "budget_owner", "status"}

var wardenFieldTypes = []string{"text", "longtext", "date", "time", "number", "money", "bool", "choice", "ward", "person", "people", "list", "phone", "email", "url", "months", "weektimes"}

// the entry's own keys a type may require besides its fields
var wardenCoreKeys = []string{"title", "date", "start", "end_date", "ward", "place"}

// most items of a list or people field, and characters of one item
const (
	wardenMaxItems = 50
	wardenItemLen  = 200
)

var wardenPhoneRe = regexp.MustCompile(`^\+?[0-9 ()./-]{3,40}$`)

const (
	wardenMaxCost        = 1e7 // euros of one cost
	wardenMaxInstalments = 120
	wardenRemindDays     = 30 // a deadline is due this many days before, unless it says
	wardenPayRemindDays  = 3  // a cost paid in parts is reminded of this many days before each
	wardenGetDays        = 14 // a get_by without get_from: the days before it to get the thing in
)

func wardenTypeOf(id string) *wardenType {
	for i := range wardenTypes {
		if wardenTypes[i].ID == id {
			return &wardenTypes[i]
		}
	}
	return nil
}

// the types a vocabulary pack offers; "demo" shows them all but the legacy
// ones
func wardenTypesFor(vocab string) []wardenType {
	out := []wardenType{}
	for _, t := range wardenTypes {
		if t.Legacy {
			continue
		}
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

// the type an entry names, offered by the space's pack; had: the type the
// entry has already (a type its pack no longer offers stays its own)
func (sp *wardenSpace) entryType(id, had string) (*wardenType, error) {
	if id == "" {
		id = "event"
	}
	t := wardenTypeOf(id)
	if t == nil || (!slices.Contains(sp.typeIDs(), id) && id != had) {
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
	case "weektimes":
		s, _ := text(400)
		w, err := wardenWeekTimes(s)
		if err != nil {
			return bad(err.Error())
		}
		return w.String(), nil
	case "list", "people":
		max := wardenItemLen
		if f.Type == "people" {
			max = 60
		}
		var items []string
		switch l := v.(type) {
		case []any:
			for _, x := range l {
				s, ok := x.(string)
				if !ok {
					return bad("a list of texts")
				}
				items = append(items, s)
			}
		case []string:
			items = l
		case string:
			items = strings.FieldsFunc(l, func(r rune) bool { return r == ';' || r == '\n' })
		default:
			return bad("a list of texts, or one text with ; between them")
		}
		out := []any{}
		for _, s := range items {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if f.Type == "people" {
				s = cleanName(s)
			}
			if len([]rune(s)) > max {
				return bad(fmt.Sprintf("a list of texts of at most %d characters each", max))
			}
			out = append(out, s)
		}
		if len(out) > wardenMaxItems {
			return bad(fmt.Sprintf("a list of at most %d", wardenMaxItems))
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	}
	return bad("not a known kind of field")
}

// a field an entry of type t may carry: the type's own, then the common ones
func (t *wardenType) field(key string) (wardenField, bool) {
	for _, f := range t.Fields {
		if f.Key == key {
			return f, true
		}
	}
	for _, f := range wardenCommon {
		if f.Key == key {
			return f, true
		}
	}
	return wardenField{}, false
}

// what an entry of type t lacks of what the type requires, by key
func (t *wardenType) missing(d Doc) []string {
	out := []string{}
	fields, _ := d["fields"].(map[string]any)
	for _, k := range t.Required {
		if slices.Contains(wardenCoreKeys, k) {
			if docStr(d, k) == "" {
				out = append(out, k)
			}
			continue
		}
		if v, ok := fields[k]; !ok || v == nil {
			out = append(out, "fields."+k)
		}
	}
	return out
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
	t, err := sp.entryType(tid, docStr(d, "type"))
	if err != nil {
		return err
	}
	if t.ID == "event" {
		delete(d, "type")
	} else {
		d["type"] = t.ID
	}
	// fields: the type's and the common ones; those a changed type no longer
	// has are dropped
	old, _ := d["fields"].(map[string]any)
	fields := map[string]any{}
	for k, v := range old {
		if _, ok := t.field(k); ok {
			fields[k] = v
		}
	}
	if v, ok := a["fields"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return roomErr{"fields is an object of the type's fields"}
		}
		for k, v := range m {
			f, ok := t.field(k)
			if !ok {
				keys := []string{}
				for _, f := range t.Fields {
					keys = append(keys, f.Key)
				}
				own := "none of its own"
				if len(keys) > 0 {
					own = strings.Join(keys, ", ")
				}
				return roomErr{fmt.Sprintf("fields.%s is not a field of %s: it has %s, and the common fields of list_entry_types", k, t.ID, own)}
			}
			val, err := sp.fieldValue(f, v, newWards)
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
	if f, b := docStr(fields, "get_from"), docStr(fields, "get_by"); f != "" && b != "" && f > b {
		return roomErr{"fields.get_from is after fields.get_by: the thing is got between them"}
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
	wardenWeekRule(t, d)
	if t.Kind != "record" && !wardenDate(docStr(d, "date")) {
		return roomErr{"date is missing: the day as YYYY-MM-DD"}
	}
	if miss := t.missing(d); len(miss) > 0 {
		return roomErr{fmt.Sprintf("a %s needs %s", t.ID, strings.Join(miss, ", "))}
	}
	return nil
}

func wardenCost(v any) (map[string]any, error) {
	m, ok := v.(map[string]any)
	bad := func(why string) (map[string]any, error) { return nil, roomErr{"cost: " + why} }
	if !ok {
		return bad("an object {amount, payer, scope, instalments, first, every_months, remind_days}")
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
	if v, ok := m["remind_days"]; ok && v != nil {
		x, ok := v.(float64)
		if !ok || x != math.Trunc(x) || x < 0 || x > 60 {
			return bad("remind_days is a whole number from 0 (no reminder) to 60")
		}
		c["remind_days"] = int(x)
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

// the day a deadline starts to show as due: remind_days before its day, or
// as many as the type's reminder on its day says, or wardenRemindDays; now
// when a low field has run low before that
func wardenRemindFrom(d Doc, now time.Time) string {
	t := wardenTypeOf(docStr(d, "type"))
	day, err := time.Parse("2006-01-02", docStr(d, "date"))
	if t == nil || t.Kind != "deadline" || err != nil {
		return ""
	}
	n := wardenRemindDays
	for _, r := range t.Reminders {
		if r.Field == "date" {
			n = r.Days
			break
		}
	}
	if f, _ := d["fields"].(map[string]any); f != nil {
		if v, ok := f["remind_days"]; ok {
			n = int(wardenNum(v))
		}
	}
	from := day.AddDate(0, 0, -n).Format("2006-01-02")
	if today := now.Format("2006-01-02"); len(wardenLowOf(d)) > 0 && today < from {
		from = today
	}
	return from
}

// the reminders the type makes of an entry's dates: {field, date, day}, the
// day the reminder is for; then the window to get something in (from its
// first day) and the cost's next payment on or after now
func wardenReminders(d Doc, now time.Time) []any {
	t := wardenTypeOf(wardenOr(docStr(d, "type"), "event"))
	fields, _ := d["fields"].(map[string]any)
	out := []any{}
	for _, r := range func() []wardenReminder {
		if t == nil {
			return nil
		}
		return t.Reminders
	}() {
		date := docStr(d, "date")
		if r.Field != "date" {
			date, _ = fields[r.Field].(string)
		}
		day, err := time.Parse("2006-01-02", date)
		if err != nil {
			continue
		}
		out = append(out, map[string]any{"field": r.Field, "date": date, "day": day.AddDate(0, 0, -r.Days).Format("2006-01-02")})
	}
	for _, f := range wardenLowOf(d) {
		today := now.Format("2006-01-02")
		out = append(out, map[string]any{"field": f, "date": today, "day": today, "low": true})
	}
	if from, by := wardenGetWindow(d); by != "" {
		out = append(out, map[string]any{"field": "get_by", "date": by, "day": from})
	}
	if p := wardenNextPayment(d, now); p != nil && p["remind_from"] != nil {
		out = append(out, map[string]any{"field": "cost", "date": p["date"], "day": p["remind_from"]})
	}
	return out
}

// the days between which something is got or bought: get_from and get_by
// as the fields say, else the type's own (a missing thing by its day, a
// need by needed_by until it is bought); get_by alone is got in the
// wardenGetDays before it, and get_from alone by the entry's day. "" when
// there is none.
func wardenGetWindow(d Doc) (from, by string) {
	fields, _ := d["fields"].(map[string]any)
	from, by = docStr(fields, "get_from"), docStr(fields, "get_by")
	if t := wardenTypeOf(docStr(d, "type")); by == "" && from == "" && t != nil && t.Get != nil {
		g := t.Get
		on := g.When == nil || g.When.holds(fields)
		for _, c := range g.Done {
			if c.holds(fields) {
				on = false
			}
		}
		if on {
			if g.By == "date" {
				by = docStr(d, "date")
			} else {
				by = docStr(fields, g.By)
			}
		}
	}
	if by == "" && from != "" {
		by = docStr(d, "date")
		if by < from {
			by = from
		}
	}
	if by == "" {
		return "", ""
	}
	if from == "" {
		t, _ := time.Parse("2006-01-02", by)
		from = t.AddDate(0, 0, -wardenGetDays).Format("2006-01-02")
	}
	return from, by
}

// a payment of an entry's cost: its day, its amount, which part of how many
type wardenPayment struct {
	day    time.Time
	amount float64
	n, of  int
}

// the payments of an entry's cost c: its instalments from first (the
// entry's day, else the day it was saved), every every_months months, each
// on the same day of the month (the month's last when it is shorter)
func wardenPayments(d Doc, c map[string]any) []wardenPayment {
	first := docStr(c, "first")
	if first == "" {
		first = docStr(d, "date")
	}
	if first == "" {
		first = time.UnixMilli(int64(wardenNum(d["updated"]))).In(time.UTC).Format("2006-01-02")
	}
	day, err := time.Parse("2006-01-02", first)
	if err != nil {
		return nil
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
	out := make([]wardenPayment, 0, n)
	for k := 0; k < n; k++ {
		amt := part
		if k == n-1 {
			amt = math.Round((total-part*float64(n-1))*100) / 100
		}
		at := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, k*every, 0)
		pd := at.AddDate(0, 0, day.Day()-1)
		if pd.Month() != at.Month() {
			pd = at.AddDate(0, 1, -1)
		}
		out = append(out, wardenPayment{pd, amt, k + 1, n})
	}
	return out
}

// how many days before each payment the cost is reminded of: remind_days,
// or wardenPayRemindDays for one paid in parts; -1 for none
func wardenPayRemind(c map[string]any) int {
	if v, ok := c["remind_days"]; ok {
		if n := int(wardenNum(v)); n > 0 {
			return n
		}
		return -1
	}
	if wardenNum(c["instalments"]) > 1 {
		return wardenPayRemindDays
	}
	return -1
}

// the cost's own, else the one the budget reads from the entry's fields
func wardenCostOf(d Doc) map[string]any {
	if c, _ := d["cost"].(map[string]any); c != nil {
		return c
	}
	return wardenDerivedCost(d)
}

// the next payment of the entry's cost on or after now's day, with the day
// its reminder starts; nil when none is left
func wardenNextPayment(d Doc, now time.Time) map[string]any {
	c := wardenCostOf(d)
	if c == nil {
		return nil
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for _, p := range wardenPayments(d, c) {
		if p.day.Before(today) {
			continue
		}
		out := map[string]any{"date": p.day.Format("2006-01-02"), "amount": p.amount, "n": p.n, "of": p.of}
		if r := wardenPayRemind(c); r > 0 {
			out["remind_from"] = p.day.AddDate(0, 0, -r).Format("2006-01-02")
		}
		return out
	}
	return nil
}

// An entry without a cost of its own that the budget still counts: the
// first of the type's budget fields that is set, then the common actual,
// committed and estimated costs; nil when none is set, or the entry is left
// out of the budget.
func wardenDerivedCost(d Doc) map[string]any {
	fields, _ := d["fields"].(map[string]any)
	if fields == nil {
		return nil
	}
	if in, ok := fields["include_in_budget"].(bool); ok && !in {
		return nil
	}
	keys := []string{}
	if t := wardenTypeOf(docStr(d, "type")); t != nil {
		keys = append(keys, t.Budget...)
	}
	keys = append(keys, "actual_cost", "committed_cost", "estimated_cost")
	for _, k := range keys {
		if amt := wardenNum(fields[k]); amt > 0 {
			c := map[string]any{"amount": amt, "scope": "shared", "instalments": 1, "every_months": 1, "field": k}
			// a personal one is its budget owner's, else its payer's
			p := docStr(fields, "payer")
			if docStr(fields, "budget_scope") == "personal" {
				if o := docStr(fields, "budget_owner"); o != "" {
					p = o
				}
				if p != "" {
					c["scope"] = "personal"
				}
			}
			if p != "" {
				c["payer"] = p
			}
			return c
		}
	}
	return nil
}

// the type's part of an entry's answer; now dates the next payment
func wardenEntryJSON(out map[string]any, d Doc, now time.Time) {
	if t := docStr(d, "type"); t != "" {
		out["type"] = t
	}
	if f, _ := d["fields"].(map[string]any); len(f) > 0 {
		out["fields"] = f
	}
	if c, _ := d["cost"].(map[string]any); c != nil {
		out["cost"] = c
	}
	if r := wardenRemindFrom(d, now); r != "" {
		out["remind_from"] = r
	}
	if from, by := wardenGetWindow(d); by != "" {
		out["get_window"] = map[string]any{"from": from, "by": by}
	}
	if p := wardenNextPayment(d, now); p != nil {
		out["next_payment"] = p
	}
	if r := wardenReminders(d, now); len(r) > 0 {
		out["reminders"] = r
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
	return map[string]any{"about": about.About, "common": wardenCommon, "types": wardenTypesFor(vocab),
		"cost": "cost {amount (euros), payer (a name), scope shared|personal (shared: the family budget; personal: the payer's own budget, still listed in the group's spending), instalments (paid in this many parts, default 1; a monthly bill such as an electricity contract is e.g. 12 parts every month), first (the first payment's day; the entry's day by default), every_months (months between parts, default 1), remind_days (days before each payment day to remind; default 3 for a cost in parts, 0 for none)}. An entry's answer carries next_payment {date, amount, n, of, remind_from} and get_window {from, by}."}, nil
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
	payer, person := cleanName(argStr(a, "payer")), cleanName(argStr(a, "person"))
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
		c := wardenCostOf(d)
		if c == nil {
			continue
		}
		who := docStr(c, "payer")
		if payer != "" && !strings.EqualFold(who, payer) {
			continue
		}
		// a person's budget: their own costs and the family's shared ones
		if person != "" && docStr(c, "scope") == "personal" && !strings.EqualFold(who, person) {
			continue
		}
		for _, p := range wardenPayments(d, c) {
			at := time.Date(p.day.Year(), p.day.Month(), 1, 0, 0, 0, 0, time.UTC)
			if at.Before(f) || at.After(t) {
				continue
			}
			lines = append(lines, line{at.Format("2006-01"), map[string]any{
				"event_id": ids[i], "title": docStr(d, "title"), "type": wardenOr(docStr(d, "type"), "event"),
				"date": p.day.Format("2006-01-02"), "amount": p.amount, "payer": who, "scope": docStr(c, "scope"), "n": p.n, "of": p.of,
			}})
			// a cost read from the entry's fields says which
			if f := docStr(c, "field"); f != "" {
				lines[len(lines)-1].m["from_field"] = f
			}
		}
	}
	sort.SliceStable(lines, func(i, j int) bool { return docStr(lines[i].m, "date") < docStr(lines[j].m, "date") })
	type sum struct {
		total, shared, personal float64
		byPayer                 map[string]float64
	}
	add := func(s *sum, m map[string]any) {
		amt := wardenNum(m["amount"])
		s.total += amt
		if docStr(m, "scope") != "personal" {
			s.shared += amt
		} else {
			s.personal += amt
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
		return map[string]any{"total": round(s.total), "shared": round(s.shared), "personal": round(s.personal), "by_payer": bp}
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
	// money paid between members to even out the costs: in the books, not a
	// cost (wardenask.go)
	who := payer
	if who == "" {
		who = person
	}
	settlements, net := wardenSettlements(docs, ids, f, t, who)
	out["settlements"] = settlements
	if len(settlements) > 0 {
		settled := map[string]any{}
		for k, v := range net {
			settled[k] = round(v)
		}
		out["settled"] = settled
	}
	if payer != "" {
		out["payer"] = payer
	}
	if person != "" {
		out["person"] = person
	}
	return out, nil
}

func wardenOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// --- times per weekday (a school's timetable) -----------------------------------

var wardenDayCodes = []string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"}

// the ways a weekday is written, English and Finnish, by its index in
// wardenDayCodes
var wardenDayNames = [][]string{
	{"mo", "mon", "monday", "ma", "maanantai", "maanantaisin"},
	{"tu", "tue", "tuesday", "ti", "tiistai", "tiistaisin"},
	{"we", "wed", "wednesday", "ke", "keskiviikko", "keskiviikkoisin"},
	{"th", "thu", "thursday", "to", "torstai", "torstaisin"},
	{"fr", "fri", "friday", "pe", "perjantai", "perjantaisin"},
	{"sa", "sat", "saturday", "la", "lauantai", "lauantaisin"},
	{"su", "sun", "sunday", "su", "sunnuntai", "sunnuntaisin"},
}

// one weekday's times; days not in the timetable are off
type wardenWeek [7]struct {
	on         bool
	start, end string
}

// "MO 08:15-13:00; TU 09:00-14:15", Monday first
func (w wardenWeek) String() string {
	parts := []string{}
	for i, d := range w {
		if d.on {
			parts = append(parts, wardenDayCodes[i]+" "+d.start+"-"+d.end)
		}
	}
	return strings.Join(parts, "; ")
}

func (w wardenWeek) days() []string {
	out := []string{}
	for i, d := range w {
		if d.on {
			out = append(out, wardenDayCodes[i])
		}
	}
	return out
}

func wardenDayIndex(s string) int {
	s = strings.ToLower(strings.Trim(s, " .:"))
	for i, names := range wardenDayNames {
		if slices.Contains(names, s) {
			return i
		}
	}
	return -1
}

var wardenWeekItemRe = regexp.MustCompile(`^\s*([^\d]*?)\s*(\d{1,2}(?:[:.]\d{2})?)\s*[-–—]\s*(\d{1,2}(?:[:.]\d{2})?)\s*$`)

var wardenKloRe = regexp.MustCompile(`(?i)\b(klo|kello|at|from)\b`)

// "8", "8.15", "08:15" as "08:15"
func wardenClock(s string) (string, bool) {
	h, m := s, "00"
	if i := strings.IndexAny(s, ":."); i >= 0 {
		h, m = s[:i], s[i+1:]
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh > 23 || mm > 59 {
		return "", false
	}
	return fmt.Sprintf("%02d:%02d", hh, mm), true
}

// A timetable as written: "ma 8.15-13, ti 9-14.15", "MO,WE 08:00-12:00;
// TU-TH 9:00-14:00". Days written without a time take the next time given.
func wardenWeekTimes(s string) (wardenWeek, error) {
	var w wardenWeek
	bad := func(why string) (wardenWeek, error) {
		return w, fmt.Errorf("times of the weekdays, e.g. \"MO 08:15-13:00; TU 09:00-14:15\" (%s)", why)
	}
	pending := []int{}
	for _, item := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ',' || r == '\n' }) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		m := wardenWeekItemRe.FindStringSubmatch(item)
		dayPart := item
		if m != nil {
			dayPart = m[1]
		}
		dayPart = strings.TrimSpace(wardenKloRe.ReplaceAllString(dayPart, " "))
		// "ma-pe", "ma–ke", "ma ke"
		var days []int
		if a, b, ok := strings.Cut(strings.NewReplacer("–", "-", "—", "-").Replace(dayPart), "-"); ok {
			i, j := wardenDayIndex(a), wardenDayIndex(b)
			if i < 0 || j < i {
				return bad(dayPart + " is not a range of weekdays")
			}
			for k := i; k <= j; k++ {
				days = append(days, k)
			}
		} else {
			for _, f := range strings.Fields(dayPart) {
				i := wardenDayIndex(f)
				if i < 0 {
					return bad(f + " is not a weekday")
				}
				days = append(days, i)
			}
		}
		if len(days) == 0 {
			return bad(item + " names no weekday")
		}
		pending = append(pending, days...)
		if m == nil {
			continue
		}
		start, ok1 := wardenClock(m[2])
		end, ok2 := wardenClock(m[3])
		if !ok1 || !ok2 || end <= start {
			return bad(item + " is not a time from start to end")
		}
		for _, i := range pending {
			if w[i].on {
				return bad(wardenDayCodes[i] + " is given twice")
			}
			w[i].on, w[i].start, w[i].end = true, start, end
		}
		pending = nil
	}
	if len(pending) > 0 {
		return bad("a weekday without a time")
	}
	if len(w.days()) == 0 {
		return bad("no weekdays")
	}
	return w, nil
}

// An entry with times per weekday repeats weekly on those days (until the
// day its rule ends) and starts and ends at its first day's times, which is
// what a reader that knows only one time a week shows.
func wardenWeekRule(t *wardenType, d Doc) {
	fields, _ := d["fields"].(map[string]any)
	for _, f := range t.Fields {
		s, _ := fields[f.Key].(string)
		if f.Type != "weektimes" || s == "" {
			continue
		}
		w, err := wardenWeekTimes(s)
		if err != nil {
			continue
		}
		r := "FREQ=WEEKLY;BYDAY=" + strings.Join(w.days(), ",")
		for _, p := range strings.Split(docStr(d, "rrule"), ";") {
			if strings.HasPrefix(p, "UNTIL=") {
				r += ";" + p
			}
		}
		d["rrule"] = r
		day := 0
		if dt, err := time.Parse("2006-01-02", docStr(d, "date")); err == nil {
			day = (int(dt.Weekday()) + 6) % 7
		}
		for k := 0; k < 7; k++ {
			if x := w[(day+k)%7]; x.on {
				d["start"], d["end"], d["end_date"] = x.start, x.end, ""
				break
			}
		}
		return
	}
}
