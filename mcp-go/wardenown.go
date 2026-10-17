// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's own money: one person's regular income and expenses, the parts of
// their estimated budget (food, fuel…), and the irregular income and the
// irregular or surprise expenses they add, so they see roughly how much they
// have to spend each month. It is the signed-in person's alone: kept in
// warden_own under their uid, in no space, and every call reads and writes
// only the caller's own document. No other member, owner or assistant of
// someone else sees it.
//
//	warden_own/{uid}  {items: [{id, kind, title, amount, every_months, date,
//	                   until, note, added}], updated}
//
// A regular item or an estimate is paid every every_months months, from its
// date's month (the month it was added without one) to until; an irregular
// one once, in its date's month.

package main

import (
	"context"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	wardenOwn         = "warden_own"
	wardenMaxOwnItems = 300
)

// the kinds of items, and whether each is money in
var wardenOwnKinds = []string{"regular_income", "regular_expense", "estimate", "irregular_income", "irregular_expense"}

func wardenOwnIncome(kind string) bool {
	return kind == "regular_income" || kind == "irregular_income"
}

func wardenOwnOnce(kind string) bool {
	return kind == "irregular_income" || kind == "irregular_expense"
}

var wardenOwnItemProps = map[string]any{
	"kind":         map[string]any{"type": "string", "enum": wardenOwnKinds, "description": "regular_income (salary, child benefit…), regular_expense (rent, insurance, car insurance, loan, phone…), estimate (a part of the estimated budget, e.g. food or fuel a month), irregular_income (a one-off income) or irregular_expense (an irregular or surprise expense)"},
	"title":        strProp("What it is, e.g. \"Vuokra\" or \"Autovakuutus\""),
	"amount":       map[string]any{"type": "number", "description": "Euros each time it is paid"},
	"every_months": map[string]any{"type": "integer", "description": "Regular items and estimates: months between payments, 1 (monthly, the default), 3, 6, 12 (yearly)…"},
	"date":         strProp("YYYY-MM-DD: an irregular item's day (required for one); a regular item's first payment day (its month and day of month)"),
	"until":        strProp("YYYY-MM-DD: a regular item's last month; leave out while it goes on"),
	"note":         strProp("A note"),
}

// the person's items, from their own document
func (s *wardenService) ownItems(ctx context.Context, who wardenWho) ([]map[string]any, error) {
	if who.uid == "" {
		return nil, roomErr{"sign in first"}
	}
	d, err := s.db.Get(ctx, wardenOwn, who.uid)
	if err != nil || d == nil {
		return []map[string]any{}, err
	}
	return docMaps(d, "items"), nil
}

func (s *wardenService) saveOwn(ctx context.Context, who wardenWho, items []map[string]any) error {
	return s.db.Set(ctx, wardenOwn, who.uid, Doc{"items": anys(items), "updated": s.now().UnixMilli()})
}

func wardenOwnItemJSON(it map[string]any) map[string]any {
	out := map[string]any{"item_id": docStr(it, "id"), "kind": docStr(it, "kind"), "title": docStr(it, "title"), "amount": wardenNum(it["amount"])}
	if !wardenOwnOnce(docStr(it, "kind")) {
		out["every_months"] = wardenOwnEvery(it)
	}
	for _, k := range []string{"date", "until", "note", "added"} {
		if v := docStr(it, k); v != "" {
			out[k] = v
		}
	}
	return out
}

func wardenOwnEvery(it map[string]any) int {
	n := int(wardenNum(it["every_months"]))
	if n < 1 {
		return 1
	}
	return n
}

// save_own_item: a new item, or the given fields of one
func (s *wardenService) saveOwnItem(ctx context.Context, who wardenWho, a map[string]any) (any, error) {
	items, err := s.ownItems(ctx, who)
	if err != nil {
		return nil, err
	}
	id := argStr(a, "item_id")
	at := -1
	it := map[string]any{}
	if id != "" {
		for i, x := range items {
			if docStr(x, "id") == id {
				at = i
				it = x
			}
		}
		if at < 0 {
			return nil, roomErr{"no such item; own_budget lists them"}
		}
	} else {
		if len(items) >= wardenMaxOwnItems {
			return nil, roomErr{"at most 300 items"}
		}
		it = map[string]any{"id": wardenRandomID("o"), "added": s.now().Format("2006-01")}
	}
	if v, ok := a["kind"]; ok {
		k, _ := v.(string)
		if !slices.Contains(wardenOwnKinds, k) {
			return nil, roomErr{"kind is one of " + strings.Join(wardenOwnKinds, ", ")}
		}
		it["kind"] = k
	}
	if _, ok := a["title"]; ok {
		t, err := wardenName(a, "title", 120)
		if err != nil {
			return nil, err
		}
		it["title"] = t
	}
	if v, ok := a["amount"]; ok {
		n, ok := v.(float64)
		if !ok || n < 0 || n > 10000000 || math.IsNaN(n) {
			return nil, roomErr{"amount is euros, 0 to 10 000 000"}
		}
		it["amount"] = math.Round(n*100) / 100
	}
	if v, ok := a["every_months"]; ok && v != nil {
		n, ok := v.(float64)
		if !ok || n < 1 || n > 60 || n != math.Trunc(n) {
			return nil, roomErr{"every_months is a whole number of months, 1 to 60"}
		}
		it["every_months"] = int64(n)
	}
	for _, k := range []string{"date", "until"} {
		v, ok := a[k]
		if !ok {
			continue
		}
		str, _ := v.(string)
		if v == nil || strings.TrimSpace(str) == "" {
			delete(it, k)
			continue
		}
		if !wardenDate(strings.TrimSpace(str)) {
			return nil, roomErr{k + " is a day, YYYY-MM-DD"}
		}
		it[k] = strings.TrimSpace(str)
	}
	if v, ok := a["note"]; ok {
		n, _ := v.(string)
		n = strings.TrimSpace(n)
		if len([]rune(n)) > 500 {
			return nil, roomErr{"note is at most 500 characters"}
		}
		if n == "" {
			delete(it, "note")
		} else {
			it["note"] = n
		}
	}
	kind := docStr(it, "kind")
	switch {
	case kind == "":
		return nil, roomErr{"kind is needed: " + strings.Join(wardenOwnKinds, ", ")}
	case docStr(it, "title") == "":
		return nil, roomErr{"title is needed"}
	case it["amount"] == nil:
		return nil, roomErr{"amount is needed"}
	case wardenOwnOnce(kind) && docStr(it, "date") == "":
		return nil, roomErr{"an irregular item needs its date"}
	case docStr(it, "until") != "" && docStr(it, "date") != "" && docStr(it, "until") < docStr(it, "date"):
		return nil, roomErr{"until is before date"}
	}
	if wardenOwnOnce(kind) {
		delete(it, "every_months")
		delete(it, "until")
	}
	if at >= 0 {
		items[at] = it
	} else {
		items = append(items, it)
	}
	if err := s.saveOwn(ctx, who, items); err != nil {
		return nil, err
	}
	out := wardenOwnItemJSON(it)
	out["private"] = true
	return out, nil
}

// delete_own_item
func (s *wardenService) deleteOwnItem(ctx context.Context, who wardenWho, a map[string]any) (any, error) {
	items, err := s.ownItems(ctx, who)
	if err != nil {
		return nil, err
	}
	id := argStr(a, "item_id")
	rest := []map[string]any{}
	for _, x := range items {
		if docStr(x, "id") != id {
			rest = append(rest, x)
		}
	}
	if len(rest) == len(items) {
		return nil, roomErr{"no such item; own_budget lists them"}
	}
	if err := s.saveOwn(ctx, who, rest); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": id}, nil
}

// the month index (year*12 + month-1) of "YYYY-MM…"
func wardenOwnMonth(s string) int {
	t, err := time.Parse("2006-01", s[:min(len(s), 7)])
	if err != nil {
		return -1
	}
	return t.Year()*12 + int(t.Month()) - 1
}

// the item's payment in month m: true when it has one
func wardenOwnIn(it map[string]any, m int) bool {
	start := wardenOwnMonth(docStr(it, "date"))
	if wardenOwnOnce(docStr(it, "kind")) {
		return start == m
	}
	if start < 0 {
		start = wardenOwnMonth(docStr(it, "added"))
	}
	if start < 0 {
		start = m
	}
	if m < start {
		return false
	}
	if u := docStr(it, "until"); u != "" && m > wardenOwnMonth(u) {
		return false
	}
	return (m-start)%wardenOwnEvery(it) == 0
}

// own_budget: the person's items, what is left of a month on average, and
// month by month
func (s *wardenService) ownBudget(ctx context.Context, who wardenWho, a map[string]any) (any, error) {
	items, err := s.ownItems(ctx, who)
	if err != nil {
		return nil, err
	}
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
	round := func(v float64) float64 { return math.Round(v*100) / 100 }
	sort.SliceStable(items, func(i, j int) bool {
		return slices.Index(wardenOwnKinds, docStr(items[i], "kind")) < slices.Index(wardenOwnKinds, docStr(items[j], "kind"))
	})
	list := []any{}
	// a month on average: the regular items and estimates going on now
	var inAvg, outAvg float64
	nowM := now.Year()*12 + int(now.Month()) - 1
	for _, it := range items {
		list = append(list, wardenOwnItemJSON(it))
		kind := docStr(it, "kind")
		if wardenOwnOnce(kind) {
			continue
		}
		if u := docStr(it, "until"); u != "" && wardenOwnMonth(u) < nowM {
			continue
		}
		per := wardenNum(it["amount"]) / float64(wardenOwnEvery(it))
		if wardenOwnIncome(kind) {
			inAvg += per
		} else {
			outAvg += per
		}
	}
	months := []any{}
	var inAll, outAll float64
	for m := f; !m.After(t); m = m.AddDate(0, 1, 0) {
		mi := m.Year()*12 + int(m.Month()) - 1
		var in, out float64
		lines := []any{}
		for _, it := range items {
			if !wardenOwnIn(it, mi) {
				continue
			}
			amt := wardenNum(it["amount"])
			if wardenOwnIncome(docStr(it, "kind")) {
				in += amt
			} else {
				out += amt
			}
			l := map[string]any{"item_id": docStr(it, "id"), "title": docStr(it, "title"), "kind": docStr(it, "kind"), "amount": amt}
			if d := docStr(it, "date"); d != "" {
				l["day"] = d[8:]
			}
			lines = append(lines, l)
		}
		inAll += in
		outAll += out
		months = append(months, map[string]any{"month": m.Format("2006-01"), "income": round(in), "expenses": round(out), "left": round(in - out), "lines": lines})
	}
	return map[string]any{
		"private": "Only you see this: it is kept under your own account, in no space.",
		"items":   list, "currency": "EUR", "from": from, "to": to,
		"monthly": map[string]any{"income": round(inAvg), "expenses": round(outAvg), "left": round(inAvg - outAvg)},
		"total":   map[string]any{"income": round(inAll), "expenses": round(outAll), "left": round(inAll - outAll)},
		"months":  months,
	}, nil
}
