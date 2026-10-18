// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden: shared spaces of a group, with the things its members are
// responsible for (wards), their events and who takes care of what. The
// page is the Spaces app built as Warden (apps/spaces, served by Hosting at
// /apps/warden/); everything it reads and writes comes through here, and so
// does everything an assistant does through Warden's own MCP server
// (wardenhttp.go):
//
//	POST /apps/warden/api/<op>   the page, signed in with the editor's cookie
//	POST /apps/warden/mcp        an assistant, signed in with OAuth
//
// Both run the same operations (wardenService.call) for one account. A space
// is one document, warden_spaces/{id}: its name, vocabulary pack, members,
// pending invites and wards. Each event is a document of warden_events with
// the space's id; an event brought in from elsewhere carries that source's
// id (ext_id), and its document id is derived from it, so bringing it in
// again changes it instead of making another. No Firestore rule matches
// either collection: only the server reads or writes them.
//
// Who may do what: only a member sees a space or changes its events and
// wards; the owner alone invites, removes members, renames or deletes it. An
// invite names an address; the invited person becomes a member the first time
// they open the page signed in with that address, verified.
//
// Dates and times are the space's own wall-clock (its time zone, by default
// Europe/Helsinki): "YYYY-MM-DD" and "HH:MM". A repeating event carries a
// subset of an iCalendar RRULE (FREQ DAILY, WEEKLY or MONTHLY, INTERVAL,
// BYDAY, UNTIL, COUNT) and the days it skips (exdates).

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // the image has no zone files
)

const (
	wardenSpaces = "warden_spaces"
	wardenEvents = "warden_events"
	wardenTZ     = "Europe/Helsinki"

	wardenMaxSpaces  = 20   // spaces one account owns
	wardenMaxMembers = 50   // members and invites of a space, together
	wardenMaxWards   = 50   // wards of a space
	wardenMaxEvents  = 3000 // events of a space
	wardenMaxImport  = 200  // events in one import
)

var wardenVocabs = []string{"demo", "family", "business", "sports"}

// the colours a ward or an event may name (the calendar's own), or #rrggbb
// the page's palette, given to new wards in turn; the older names still read
var wardenColors = []string{"blue", "emerald", "violet", "amber", "rose", "cyan", "indigo"}
var wardenOldColors = []string{"green", "red", "orange", "purple", "teal", "pink", "yellow", "gray"}

var (
	wardenDateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	wardenTimeRe  = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	wardenHexRe   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	wardenEmailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	wardenIDRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// the account calling: its user id, name and the address it signed in with
// (lower case, "" when not verified), and the open page calling ("" for an
// assistant)
type wardenWho struct {
	uid, name, email string
	client           string
}

type wardenService struct {
	db  DB
	now func() time.Time
	// the page's assistant (wardenai.go); nil: none
	ai *wardenAssist
	// the site's address, for the calendar feed's (wardenfeed.go)
	base string
	// removes every file under a prefix in Storage (a deleted document's,
	// wardendocs.go); nil: none kept
	removeFiles func(ctx context.Context, prefix string)
}

func newWardenService(db DB) *wardenService {
	if db == nil {
		return nil
	}
	return &wardenService{db: db, now: time.Now}
}

func wardenRandomID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// ------------------------------------------------------------------ space --

type wardenSpace struct {
	id   string
	doc  Doc
	who  wardenWho
	role string // owner or member
}

func docStr(d Doc, k string) string {
	s, _ := d[k].(string)
	return s
}

func docStrs(d Doc, k string) []string {
	out := []string{}
	switch v := d[k].(type) {
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, v...)
	}
	return out
}

func docMaps(d Doc, k string) []map[string]any {
	out := []map[string]any{}
	switch v := d[k].(type) {
	case []any:
		for _, e := range v {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
	case []map[string]any:
		out = append(out, v...)
	}
	return out
}

func anys[T any](l []T) []any {
	out := make([]any, len(l))
	for i, v := range l {
		out[i] = v
	}
	return out
}

// the space, for a member of it; a stranger hears it is not there
func (s *wardenService) space(ctx context.Context, who wardenWho, id string) (*wardenSpace, error) {
	if !wardenIDRe.MatchString(id) {
		return nil, roomErr{"space_id is missing or not a space's id; list_spaces names them"}
	}
	d, err := s.db.Get(ctx, wardenSpaces, id)
	if err != nil {
		return nil, err
	}
	if d == nil || !slices.Contains(docStrs(d, "members"), who.uid) {
		return nil, roomErr{"no such space, or you are not a member of it"}
	}
	role := "member"
	if docStr(d, "owner") == who.uid {
		role = "owner"
	}
	return &wardenSpace{id: id, doc: d, who: who, role: role}, nil
}

func (sp *wardenSpace) owner() error {
	if sp.role != "owner" {
		return roomErr{"only the space's owner can do that"}
	}
	return nil
}

func (s *wardenService) save(ctx context.Context, sp *wardenSpace) error {
	sp.doc["updated"] = s.now().UnixMilli()
	return s.db.Set(ctx, wardenSpaces, sp.id, sp.doc)
}

// the space as an answer
func (sp *wardenSpace) json() map[string]any {
	people, _ := sp.doc["people"].(map[string]any)
	members := []any{}
	for _, uid := range docStrs(sp.doc, "members") {
		p, _ := people[uid].(map[string]any)
		role := "member"
		if uid == docStr(sp.doc, "owner") {
			role = "owner"
		}
		members = append(members, map[string]any{"uid": uid, "name": docStr(p, "name"), "email": docStr(p, "email"), "role": role})
	}
	wards := []any{}
	for _, w := range docMaps(sp.doc, "wards") {
		wards = append(wards, wardenWardJSON(w))
	}
	tz := docStr(sp.doc, "tz")
	if tz == "" {
		tz = wardenTZ
	}
	return map[string]any{
		"space_id": sp.id, "name": docStr(sp.doc, "name"), "vocab": docStr(sp.doc, "vocab"), "lang": docStr(sp.doc, "lang"),
		"time_zone": tz, "role": sp.role, "members": members, "invites": anys(docStrs(sp.doc, "invites")), "wards": wards,
		"places": wardenPlacesJSON(sp.doc),
	}
}

func wardenPerson(who wardenWho) map[string]any {
	return map[string]any{"name": who.name, "email": who.email}
}

// list_spaces: the caller's spaces. On the page, with a verified address,
// the invites to it are taken first: the caller becomes a member.
func (s *wardenService) listSpaces(ctx context.Context, who wardenWho, accept bool) (any, error) {
	if accept && who.email != "" {
		docs, ids, err := s.db.WhereHas(ctx, wardenSpaces, "invites", who.email)
		if err != nil {
			return nil, err
		}
		for i, d := range docs {
			sp := &wardenSpace{id: ids[i], doc: d}
			sp.doc["invites"] = anys(slices.DeleteFunc(docStrs(d, "invites"), func(e string) bool { return e == who.email }))
			if m := docStrs(d, "members"); !slices.Contains(m, who.uid) {
				sp.doc["members"] = anys(append(m, who.uid))
			}
			people, _ := d["people"].(map[string]any)
			if people == nil {
				people = map[string]any{}
			}
			people[who.uid] = wardenPerson(who)
			sp.doc["people"] = people
			if err := s.save(ctx, sp); err != nil {
				return nil, err
			}
		}
	}
	docs, ids, err := s.db.WhereHas(ctx, wardenSpaces, "members", who.uid)
	if err != nil {
		return nil, err
	}
	out := []any{}
	order := make([]int, len(docs))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return docStr(docs[order[a]], "name") < docStr(docs[order[b]], "name") })
	for _, i := range order {
		sp := &wardenSpace{id: ids[i], doc: docs[i], who: who, role: "member"}
		if docStr(docs[i], "owner") == who.uid {
			sp.role = "owner"
		}
		out = append(out, sp.json())
	}
	res := map[string]any{"me": map[string]any{"uid": who.uid, "name": who.name, "email": who.email}, "spaces": out}
	if len(out) == 0 {
		res["hint"] = "No spaces yet: create_space makes one. An invite to a space is taken by opening the Warden page once, signed in with the invited address."
	}
	return res, nil
}

func wardenVocabArg(a map[string]any, def string) (string, error) {
	v := strings.ToLower(argStr(a, "vocab"))
	if v == "" {
		return def, nil
	}
	if !slices.Contains(wardenVocabs, v) {
		return "", roomErr{"vocab is one of " + strings.Join(wardenVocabs, ", ")}
	}
	return v, nil
}

func wardenLangArg(a map[string]any, def string) (string, error) {
	v := strings.ToLower(argStr(a, "lang"))
	if v == "" {
		return def, nil
	}
	if v != "fi" && v != "en" {
		return "", roomErr{"lang is fi or en"}
	}
	return v, nil
}

func wardenName(a map[string]any, k string, max int) (string, error) {
	n := cleanName(argStr(a, k))
	if n == "" {
		return "", roomErr{k + " is missing"}
	}
	if len([]rune(n)) > max {
		return "", roomErr{fmt.Sprintf("%s is longer than %d characters", k, max)}
	}
	return n, nil
}

func (s *wardenService) createSpace(ctx context.Context, who wardenWho, a map[string]any) (any, error) {
	name, err := wardenName(a, "name", 80)
	if err != nil {
		return nil, err
	}
	vocab, err := wardenVocabArg(a, "demo")
	if err != nil {
		return nil, err
	}
	lang, err := wardenLangArg(a, "fi")
	if err != nil {
		return nil, err
	}
	owned, _, err := s.db.WhereEq(ctx, wardenSpaces, "owner", who.uid)
	if err != nil {
		return nil, err
	}
	if len(owned) >= wardenMaxSpaces {
		return nil, roomErr{fmt.Sprintf("you own %d spaces, the most there may be; delete one first", wardenMaxSpaces)}
	}
	sp := &wardenSpace{id: wardenRandomID("s"), who: who, role: "owner", doc: Doc{
		"name": name, "vocab": vocab, "lang": lang, "tz": wardenTZ, "owner": who.uid,
		"members": []any{who.uid}, "people": map[string]any{who.uid: wardenPerson(who)},
		"invites": []any{}, "wards": []any{}, "created": s.now().UnixMilli(),
	}}
	if err := s.save(ctx, sp); err != nil {
		return nil, err
	}
	return sp.json(), nil
}

func (s *wardenService) setSpace(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	if err := sp.owner(); err != nil {
		return nil, err
	}
	if argStr(a, "name") != "" {
		name, err := wardenName(a, "name", 80)
		if err != nil {
			return nil, err
		}
		sp.doc["name"] = name
	}
	vocab, err := wardenVocabArg(a, docStr(sp.doc, "vocab"))
	if err != nil {
		return nil, err
	}
	lang, err := wardenLangArg(a, docStr(sp.doc, "lang"))
	if err != nil {
		return nil, err
	}
	sp.doc["vocab"], sp.doc["lang"] = vocab, lang
	if err := s.save(ctx, sp); err != nil {
		return nil, err
	}
	return sp.json(), nil
}

func (s *wardenService) deleteSpace(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	if err := sp.owner(); err != nil {
		return nil, err
	}
	if argStr(a, "confirm_name") != docStr(sp.doc, "name") {
		return nil, roomErr{"confirm_name must be the space's name exactly: deleting removes its events too"}
	}
	_, ids, err := s.db.WhereEq(ctx, wardenEvents, "space", sp.id)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err := s.db.Delete(ctx, wardenEvents, id); err != nil {
			return nil, err
		}
	}
	if err := s.releaseDocs(ctx, sp); err != nil {
		return nil, err
	}
	if err := s.db.Delete(ctx, wardenSpaces, sp.id); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": sp.id, "events": len(ids)}, nil
}

func (s *wardenService) invite(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	if err := sp.owner(); err != nil {
		return nil, err
	}
	email := strings.ToLower(argStr(a, "email"))
	if !wardenEmailRe.MatchString(email) || len(email) > 200 {
		return nil, roomErr{"email is not an address"}
	}
	invites := docStrs(sp.doc, "invites")
	people, _ := sp.doc["people"].(map[string]any)
	for _, p := range people {
		if m, _ := p.(map[string]any); docStr(m, "email") == email {
			return nil, roomErr{email + " is a member already"}
		}
	}
	if slices.Contains(invites, email) {
		return sp.json(), nil
	}
	if len(invites)+len(docStrs(sp.doc, "members")) >= wardenMaxMembers {
		return nil, roomErr{fmt.Sprintf("a space has at most %d members and invites", wardenMaxMembers)}
	}
	sp.doc["invites"] = anys(append(invites, email))
	if err := s.save(ctx, sp); err != nil {
		return nil, err
	}
	return sp.json(), nil
}

// remove_member: a member by user id or address, or an invite by address
func (s *wardenService) removeMember(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	m := strings.TrimSpace(argStr(a, "member"))
	if m == "" {
		return nil, roomErr{"member is missing: a member's uid or address, or an invited address"}
	}
	people, _ := sp.doc["people"].(map[string]any)
	uid := ""
	for _, id := range docStrs(sp.doc, "members") {
		p, _ := people[id].(map[string]any)
		if id == m || (docStr(p, "email") != "" && docStr(p, "email") == strings.ToLower(m)) {
			uid = id
		}
	}
	// a member may leave; only the owner removes others
	if uid != sp.who.uid {
		if err := sp.owner(); err != nil {
			return nil, err
		}
	}
	switch {
	case uid == docStr(sp.doc, "owner"):
		return nil, roomErr{"the owner stays in the space; delete the space instead"}
	case uid != "":
		sp.doc["members"] = anys(slices.DeleteFunc(docStrs(sp.doc, "members"), func(e string) bool { return e == uid }))
		delete(people, uid)
		// the calendar feed's address they had stops working
		if docStr(sp.doc, "feed") != "" {
			sp.doc["feed"] = wardenRandomID("")
		}
	default:
		inv := docStrs(sp.doc, "invites")
		left := slices.DeleteFunc(slices.Clone(inv), func(e string) bool { return e == strings.ToLower(m) })
		if len(left) == len(inv) {
			return nil, roomErr{"no such member or invite"}
		}
		sp.doc["invites"] = anys(left)
	}
	if err := s.save(ctx, sp); err != nil {
		return nil, err
	}
	// the documents are no longer theirs to open in Sliqtly (wardendocs.go)
	if uid != "" {
		docs, ids, err := s.docs(ctx, sp)
		if err == nil {
			err = s.syncDocEditors(ctx, sp, docs, ids)
		}
		if err != nil {
			return nil, err
		}
	}
	return sp.json(), nil
}

// A name as the member it means: "Tero" written on a cost is the one member
// whose first name it is ("Tero Tolonen"), so the budget has one Tero. A name
// no member has, or a first name two share, stays as written.
func (sp *wardenSpace) memberName(n string) string {
	n = strings.TrimSpace(n)
	if n == "" {
		return n
	}
	people, _ := sp.doc["people"].(map[string]any)
	match := ""
	for _, id := range docStrs(sp.doc, "members") {
		p, _ := people[id].(map[string]any)
		full := docStr(p, "name")
		if strings.EqualFold(full, n) {
			return full
		}
		if first, _, _ := strings.Cut(full, " "); strings.EqualFold(first, n) {
			if match != "" {
				return n
			}
			match = full
		}
	}
	if match != "" {
		return match
	}
	return n
}

// ------------------------------------------------------------------ wards --

func wardenColor(c string) (string, error) {
	c = strings.ToLower(strings.TrimSpace(c))
	if c == "" || slices.Contains(wardenColors, c) || slices.Contains(wardenOldColors, c) || wardenHexRe.MatchString(c) {
		return c, nil
	}
	return "", roomErr{"color is one of " + strings.Join(wardenColors, ", ") + ", or #rrggbb"}
}

// the ward named by id or name (any case); "" when none is
func (sp *wardenSpace) wardID(v string) string {
	v = strings.TrimSpace(v)
	for _, w := range docMaps(sp.doc, "wards") {
		if docStr(w, "id") == v {
			return v
		}
	}
	for _, w := range docMaps(sp.doc, "wards") {
		if strings.EqualFold(docStr(w, "name"), v) {
			return docStr(w, "id")
		}
	}
	return ""
}

// a new ward, kept in sp.doc (the caller saves it)
func (sp *wardenSpace) addWard(name, color string) (map[string]any, error) {
	wards := docMaps(sp.doc, "wards")
	if len(wards) >= wardenMaxWards {
		return nil, roomErr{fmt.Sprintf("a space has at most %d wards", wardenMaxWards)}
	}
	if color == "" {
		color = wardenColors[len(wards)%len(wardenColors)]
	}
	w := map[string]any{"id": wardenRandomID("w")[:9], "name": name, "color": color}
	sp.doc["wards"] = anys(append(wards, w))
	return w, nil
}

func (s *wardenService) addWardOp(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	name, err := wardenName(a, "name", 60)
	if err != nil {
		return nil, err
	}
	if id := sp.wardID(name); id != "" {
		return nil, roomErr{"there is a ward of that name already: " + id}
	}
	color, err := wardenColor(argStr(a, "color"))
	if err != nil {
		return nil, err
	}
	clothing := map[string]any{}
	if err := wardenSetClothing(clothing, a); err != nil {
		return nil, err
	}
	w, err := sp.addWard(name, color)
	if err != nil {
		return nil, err
	}
	for k, v := range clothing {
		w[k] = v
	}
	if err := s.save(ctx, sp); err != nil {
		return nil, err
	}
	return wardenWardJSON(w), nil
}

// ---------------------------------------------------------------- clothes --
// A ward's clothing (a child's): "sizes", the size it wears now in each kind
// of clothing and the one it grows into next, and by when; "clothes", what
// it should have, how many and how many it has. Each list is given whole.

const (
	wardenMaxSizes   = 20 // kinds of clothing with a size
	wardenMaxClothes = 60 // rows of a clothing list
)

var wardenMonthRe = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

// a ward as the tools give it: its clothing, the missing counted, when it has any
func wardenWardJSON(w map[string]any) map[string]any {
	out := map[string]any{"id": docStr(w, "id"), "name": docStr(w, "name"), "color": docStr(w, "color"), "places": wardenPlacesJSON(w)}
	if l := docMaps(w, "sizes"); len(l) > 0 {
		out["sizes"] = anys(l)
	}
	if l := docMaps(w, "clothes"); len(l) > 0 {
		rows := []any{}
		for _, c := range l {
			r := map[string]any{}
			for k, v := range c {
				r[k] = v
			}
			r["missing"] = max(wardenCount(c["need"], 1)-wardenCount(c["have"], 0), 0)
			rows = append(rows, r)
		}
		out["clothes"] = rows
	}
	return out
}

// a count as JSON gives it (a number), or def
func wardenCount(v any, def int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return def
}

// the text k of a list row, trimmed and at most max characters
func wardenRowStr(row map[string]any, k string, max int, what string) (string, error) {
	v, ok := row[k]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", roomErr{what + ": " + k + " is a text"}
	}
	s = strings.TrimSpace(s)
	if len([]rune(s)) > max {
		return "", roomErr{fmt.Sprintf("%s: %s is at most %d characters", what, k, max)}
	}
	return s, nil
}

func wardenRows(v any, name string, maxRows int) ([]map[string]any, error) {
	l, ok := v.([]any)
	if !ok {
		return nil, roomErr{name + " is a list (an empty one clears it)"}
	}
	if len(l) > maxRows {
		return nil, roomErr{fmt.Sprintf("%s has at most %d rows", name, maxRows)}
	}
	out := []map[string]any{}
	for _, it := range l {
		row, ok := it.(map[string]any)
		if !ok {
			return nil, roomErr{name + ": each row is an object"}
		}
		out = append(out, row)
	}
	return out, nil
}

// sizes, checked: [{what, size, next_size, next_by, note}]
func wardenSizes(v any) ([]any, error) {
	rows, err := wardenRows(v, "sizes", wardenMaxSizes)
	if err != nil {
		return nil, err
	}
	out := []any{}
	seen := map[string]bool{}
	for _, row := range rows {
		d := map[string]any{}
		for _, f := range []struct {
			k   string
			max int
		}{{"what", 40}, {"size", 20}, {"next_size", 20}, {"next_by", 10}, {"note", 200}} {
			s, err := wardenRowStr(row, f.k, f.max, "sizes")
			if err != nil {
				return nil, err
			}
			if s != "" {
				d[f.k] = s
			}
		}
		what := docStr(d, "what")
		if what == "" {
			return nil, roomErr{"sizes: each row names what it is the size of (what), e.g. shoes"}
		}
		if seen[strings.ToLower(what)] {
			return nil, roomErr{"sizes: " + what + " is given twice"}
		}
		seen[strings.ToLower(what)] = true
		if by := docStr(d, "next_by"); by != "" && !wardenMonthRe.MatchString(by) && !wardenDate(by) {
			return nil, roomErr{"sizes: next_by is YYYY-MM or YYYY-MM-DD"}
		}
		out = append(out, d)
	}
	return out, nil
}

// a clothing list, checked: [{item, size, need, have, note}]
func wardenClothes(v any) ([]any, error) {
	rows, err := wardenRows(v, "clothes", wardenMaxClothes)
	if err != nil {
		return nil, err
	}
	out := []any{}
	for _, row := range rows {
		d := map[string]any{}
		for _, f := range []struct {
			k   string
			max int
		}{{"item", 60}, {"size", 20}, {"note", 200}} {
			s, err := wardenRowStr(row, f.k, f.max, "clothes")
			if err != nil {
				return nil, err
			}
			if s != "" {
				d[f.k] = s
			}
		}
		if docStr(d, "item") == "" {
			return nil, roomErr{"clothes: each row names its item, e.g. winter boots"}
		}
		for k, def := range map[string]int{"need": 1, "have": 0} {
			n := def
			if x, ok := row[k]; ok && x != nil {
				f, ok := x.(float64)
				if !ok || f != float64(int(f)) || f < 0 || f > 99 {
					return nil, roomErr{"clothes: " + k + " is a whole number from 0 to 99"}
				}
				n = int(f)
			}
			d[k] = n
		}
		out = append(out, d)
	}
	return out, nil
}

// the clothing lists given in a, checked into w
func wardenSetClothing(w map[string]any, a map[string]any) error {
	if v, ok := a["sizes"]; ok && v != nil {
		l, err := wardenSizes(v)
		if err != nil {
			return err
		}
		w["sizes"] = l
	}
	if v, ok := a["clothes"]; ok && v != nil {
		l, err := wardenClothes(v)
		if err != nil {
			return err
		}
		w["clothes"] = l
	}
	return nil
}

// set_ward: any member renames a ward, gives it another colour or sets its clothing
func (s *wardenService) setWard(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	id := sp.wardID(argStr(a, "ward"))
	if id == "" {
		return nil, roomErr{"no such ward: give its id or name"}
	}
	name := ""
	if argStr(a, "name") != "" {
		n, err := wardenName(a, "name", 60)
		if err != nil {
			return nil, err
		}
		if other := sp.wardID(n); other != "" && other != id {
			return nil, roomErr{"there is a ward of that name already: " + other}
		}
		name = n
	}
	color, err := wardenColor(argStr(a, "color"))
	if err != nil {
		return nil, err
	}
	clothing := map[string]any{}
	if err := wardenSetClothing(clothing, a); err != nil {
		return nil, err
	}
	var out map[string]any
	for _, w := range docMaps(sp.doc, "wards") {
		if docStr(w, "id") != id {
			continue
		}
		if name != "" {
			w["name"] = name
		}
		if color != "" {
			w["color"] = color
		}
		for k, v := range clothing {
			w[k] = v
		}
		out = wardenWardJSON(w)
	}
	if err := s.save(ctx, sp); err != nil {
		return nil, err
	}
	return out, nil
}

// remove_ward: owner only; its events stay, without a ward
func (s *wardenService) removeWard(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	if err := sp.owner(); err != nil {
		return nil, err
	}
	id := sp.wardID(argStr(a, "ward"))
	if id == "" {
		return nil, roomErr{"no such ward: give its id or name"}
	}
	docs, ids, err := s.db.WhereEq(ctx, wardenEvents, "space", sp.id)
	if err != nil {
		return nil, err
	}
	cleared := 0
	for i, d := range docs {
		// the entry's ward, and a typed field naming it (ward ids are random)
		hit := docStr(d, "ward") == id
		if hit {
			d["ward"] = ""
		}
		fields, _ := d["fields"].(map[string]any)
		for k, v := range fields {
			if v == id {
				delete(fields, k)
				hit = true
			}
		}
		if !hit {
			continue
		}
		s.stamp(d, sp)
		if err := s.db.Set(ctx, wardenEvents, ids[i], d); err != nil {
			return nil, err
		}
		cleared++
	}
	sp.doc["wards"] = anys(slices.DeleteFunc(docMaps(sp.doc, "wards"), func(w map[string]any) bool { return docStr(w, "id") == id }))
	if err := s.save(ctx, sp); err != nil {
		return nil, err
	}
	out := sp.json()
	out["removed_ward"], out["events_cleared"] = id, cleared
	return out, nil
}

// ----------------------------------------------------------------- events --

// an event's fields, checked; what the arguments leave out is left as base has it
func (sp *wardenSpace) eventDoc(a map[string]any, base Doc, newWards *bool) (Doc, error) {
	d := Doc{}
	for k, v := range base {
		d[k] = v
	}
	str := func(k string, max int) (string, bool, error) {
		v, ok := a[k]
		if !ok || v == nil {
			return "", false, nil
		}
		s, ok := v.(string)
		if !ok {
			return "", false, roomErr{k + " is text"}
		}
		s = strings.TrimSpace(s)
		if len([]rune(s)) > max {
			return "", false, roomErr{fmt.Sprintf("%s is longer than %d characters", k, max)}
		}
		return s, true, nil
	}
	if v, ok, err := str("title", 200); err != nil {
		return nil, err
	} else if ok {
		d["title"] = v
	}
	if docStr(d, "title") == "" {
		return nil, roomErr{"title is missing"}
	}
	for _, k := range []string{"date", "end_date"} {
		if v, ok, err := str(k, 10); err != nil {
			return nil, err
		} else if ok {
			if v != "" && !wardenDate(v) {
				return nil, roomErr{k + " is a day as YYYY-MM-DD"}
			}
			d[k] = v
		}
	}
	if e := docStr(d, "end_date"); e != "" && e <= docStr(d, "date") {
		d["end_date"] = ""
	}
	for _, k := range []string{"start", "end"} {
		if v, ok, err := str(k, 5); err != nil {
			return nil, err
		} else if ok {
			if v != "" && !wardenTimeRe.MatchString(v) {
				return nil, roomErr{k + " is a time as HH:MM (24 h)"}
			}
			d[k] = v
		}
	}
	if docStr(d, "start") == "" {
		d["end"] = ""
	} else {
		d["end_date"] = ""
		if e := docStr(d, "end"); e == "" || e <= docStr(d, "start") {
			d["end"] = wardenPlusHour(docStr(d, "start"))
		}
	}
	for _, f := range []struct {
		k   string
		max int
	}{{"place", 200}, {"notes", 2000}, {"source", 80}, {"ext_id", 300}} {
		if v, ok, err := str(f.k, f.max); err != nil {
			return nil, err
		} else if ok {
			d[f.k] = v
		}
	}
	if v, ok, err := str("color", 7); err != nil {
		return nil, err
	} else if ok {
		c, err := wardenColor(v)
		if err != nil {
			return nil, err
		}
		d["color"] = c
	}
	if v, ok, err := str("ward", 60); err != nil {
		return nil, err
	} else if ok {
		id := ""
		if v != "" {
			id = sp.wardID(v)
			if id == "" {
				// a ward named for the first time is made
				w, err := sp.addWard(cleanName(v), "")
				if err != nil {
					return nil, err
				}
				id = docStr(w, "id")
				*newWards = true
			}
		}
		d["ward"] = id
	}
	if v, ok, err := str("rrule", 200); err != nil {
		return nil, err
	} else if ok {
		r, err := wardenRule(v)
		if err != nil {
			return nil, err
		}
		d["rrule"] = r
	}
	if v, ok := a["exdates"]; ok && v != nil {
		l, ok := v.([]any)
		if !ok || len(l) > 200 {
			return nil, roomErr{"exdates is a list of days as YYYY-MM-DD"}
		}
		days := []any{}
		for _, e := range l {
			s, _ := e.(string)
			if !wardenDate(s) {
				return nil, roomErr{"exdates is a list of days as YYYY-MM-DD"}
			}
			days = append(days, s)
		}
		d["exdates"] = days
	}
	if v, ok := a["who"]; ok && v != nil {
		l, ok := v.([]any)
		if !ok || len(l) > 10 {
			return nil, roomErr{"who is a list of at most 10 {name, role}"}
		}
		who := []any{}
		for _, e := range l {
			m, _ := e.(map[string]any)
			name, role := cleanName(argStr(m, "name")), cleanName(argStr(m, "role"))
			if name == "" || len([]rune(name)) > 60 || len([]rune(role)) > 40 {
				return nil, roomErr{"who: each is {name, role}: a name (at most 60 characters) and a role such as takes, picks up, looks after"}
			}
			who = append(who, map[string]any{"name": name, "role": role})
		}
		d["who"] = who
	}
	if v, ok := a["important"]; ok && v != nil {
		b, ok := v.(bool)
		if !ok {
			return nil, roomErr{"important is true or false"}
		}
		d["important"] = b
	}
	// the type, its fields and the cost; a record may be without a day
	if err := sp.entryDoc(a, d, newWards); err != nil {
		return nil, err
	}
	return d, nil
}

func wardenDate(s string) bool {
	if !wardenDateRe.MatchString(s) {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func wardenPlusHour(hm string) string {
	h, _ := strconv.Atoi(hm[:2])
	if h >= 23 {
		return "23:59"
	}
	return fmt.Sprintf("%02d%s", h+1, hm[2:])
}

// an RRULE of what the calendar repeats, in a fixed order; "" for none
func wardenRule(s string) (string, error) {
	s = strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(s)), "RRULE:")
	if s == "" {
		return "", nil
	}
	parts := map[string]string{}
	for _, p := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return "", roomErr{"rrule: " + p + " is not KEY=VALUE"}
		}
		parts[k] = v
	}
	bad := func(why string) (string, error) { return "", roomErr{"rrule: " + why} }
	out := []string{}
	switch f := parts["FREQ"]; f {
	case "DAILY", "WEEKLY", "MONTHLY":
		out = append(out, "FREQ="+f)
	default:
		return bad("FREQ is DAILY, WEEKLY or MONTHLY")
	}
	for k, v := range parts {
		switch k {
		case "FREQ", "WKST":
		case "INTERVAL", "COUNT":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 1000 {
				return bad(k + " is a whole number from 1 to 1000")
			}
		case "BYDAY":
			for _, d := range strings.Split(v, ",") {
				if !slices.Contains([]string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"}, d) {
					return bad("BYDAY is weekdays MO,TU,WE,TH,FR,SA,SU (no ordinals)")
				}
			}
		case "UNTIL":
			if len(v) < 8 {
				return bad("UNTIL is a day YYYYMMDD")
			}
			if _, err := time.Parse("20060102", v[:8]); err != nil {
				return bad("UNTIL is a day YYYYMMDD")
			}
		default:
			return bad(k + " is not supported (FREQ, INTERVAL, BYDAY, UNTIL, COUNT)")
		}
	}
	for _, k := range []string{"INTERVAL", "BYDAY", "UNTIL", "COUNT"} {
		if v := parts[k]; v != "" {
			if k == "UNTIL" {
				v = v[:8]
			}
			out = append(out, k+"="+v)
		}
	}
	return strings.Join(out, ";"), nil
}

// the event as an answer
func wardenEventJSON(id string, d Doc, now time.Time) map[string]any {
	out := map[string]any{"event_id": id}
	for _, k := range []string{"title", "date", "end_date", "start", "end", "ward", "place", "notes", "color", "rrule", "source", "ext_id"} {
		if v := docStr(d, k); v != "" {
			out[k] = v
		}
	}
	if l := docStrs(d, "exdates"); len(l) > 0 {
		out["exdates"] = anys(l)
	}
	if l := docStrs(d, "links"); len(l) > 0 {
		out["links"] = anys(l)
	}
	if l := docMaps(d, "who"); len(l) > 0 {
		who := []any{}
		for _, m := range l {
			who = append(who, map[string]any{"name": docStr(m, "name"), "role": docStr(m, "role")})
		}
		out["who"] = who
	}
	if b, _ := d["important"].(bool); b {
		out["important"] = true
	}
	wardenEntryJSON(out, d, now)
	return out
}

// the id of an event brought in from elsewhere: the same source id, the same
// document
func wardenExtID(space, ext string) string {
	h := sha256.Sum256([]byte(space + "\x00" + ext))
	return "x" + hex.EncodeToString(h[:12])
}

func (s *wardenService) events(ctx context.Context, sp *wardenSpace) ([]Doc, []string, error) {
	docs, ids, err := s.db.WhereEq(ctx, wardenEvents, "space", sp.id)
	if err != nil {
		return nil, nil, err
	}
	order := make([]int, len(docs))
	for i := range order {
		order[i] = i
	}
	key := func(d Doc) string { return docStr(d, "date") + " " + docStr(d, "start") + " " + docStr(d, "title") }
	sort.SliceStable(order, func(a, b int) bool { return key(docs[order[a]]) < key(docs[order[b]]) })
	od, oi := make([]Doc, len(docs)), make([]string, len(docs))
	for n, i := range order {
		od[n], oi[n] = docs[i], ids[i]
	}
	return od, oi, nil
}

// the event of the space, or a caller's mistake
func (s *wardenService) event(ctx context.Context, sp *wardenSpace, id string) (Doc, error) {
	if !wardenIDRe.MatchString(id) {
		return nil, roomErr{"event_id is not an event's id"}
	}
	d, err := s.db.Get(ctx, wardenEvents, id)
	if err != nil {
		return nil, err
	}
	if d == nil || docStr(d, "space") != sp.id {
		return nil, roomErr{"no such event in this space"}
	}
	return d, nil
}

// get_space: the space and all its events (the page's whole view)
func (s *wardenService) getSpace(ctx context.Context, sp *wardenSpace) (any, error) {
	docs, ids, err := s.events(ctx, sp)
	if err != nil {
		return nil, err
	}
	evs := []any{}
	for i, d := range docs {
		evs = append(evs, wardenEventJSON(ids[i], d, s.now()))
	}
	space := sp.json()
	if space["calendar_feed"], err = s.feedURL(ctx, sp, false); err != nil {
		return nil, err
	}
	// the caller's own email summary of it (wardensummary.go)
	m, err := s.summaries(ctx, sp.who.uid)
	if err != nil {
		return nil, err
	}
	space["email_summary"] = wardenSummaryOf(m, sp.id)
	return map[string]any{"space": space, "events": evs, "changed": wardenChanged(sp.doc)}, nil
}

// list_events: the events that may happen between from and to (repeating
// ones whose rule has not ended), or all
func (s *wardenService) listEvents(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	from, to := argStr(a, "from"), argStr(a, "to")
	for _, d := range []string{from, to} {
		if d != "" && !wardenDate(d) {
			return nil, roomErr{"from and to are days as YYYY-MM-DD"}
		}
	}
	only := argBool(a, "important_only", false)
	typ := strings.ToLower(argStr(a, "type"))
	docs, ids, err := s.events(ctx, sp)
	if err != nil {
		return nil, err
	}
	evs := []any{}
	for i, d := range docs {
		if only {
			if b, _ := d["important"].(bool); !b {
				continue
			}
		}
		if typ != "" && !wardenListedAs(d, typ) {
			continue
		}
		// a window to get something in, or a payment, may fall outside the day
		first := docStr(d, "date")
		if g, _ := wardenGetWindow(d); g != "" && (first == "" || g < first) {
			first = g
		}
		if p := wardenPlannedDays(d); len(p) > 0 && (first == "" || p[0] < first) {
			first = p[0]
		}
		if to != "" && first > to {
			continue
		}
		if from != "" && docStr(d, "date") != "" {
			last := docStr(d, "date")
			if e := docStr(d, "end_date"); e != "" {
				last = e
			}
			if r := docStr(d, "rrule"); r != "" {
				last = "9999-12-31"
				if i := strings.Index(r, "UNTIL="); i >= 0 {
					u := r[i+6 : i+14]
					last = u[:4] + "-" + u[4:6] + "-" + u[6:]
				}
			}
			if _, by := wardenGetWindow(d); by > last {
				last = by
			}
			if p := wardenPlannedDays(d); len(p) > 0 && p[len(p)-1] > last {
				last = p[len(p)-1]
			}
			if c := wardenCostOf(d); c != nil {
				if p := wardenPayments(d, c); len(p) > 0 {
					if l := p[len(p)-1].day.Format("2006-01-02"); l > last {
						last = l
					}
				}
			}
			if last < from {
				continue
			}
		}
		evs = append(evs, wardenEventJSON(ids[i], d, s.now()))
	}
	return map[string]any{"space_id": sp.id, "time_zone": sp.json()["time_zone"], "events": evs}, nil
}

// save_event: a new event, or the given one changed
func (s *wardenService) saveEvent(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	id := argStr(a, "event_id")
	base := Doc{}
	if id != "" {
		d, err := s.event(ctx, sp, id)
		if err != nil {
			return nil, err
		}
		base = d
	}
	newWards := false
	d, err := sp.eventDoc(a, base, &newWards)
	if err != nil {
		return nil, err
	}
	if sp.saleLabel(d) {
		newWards = true // the space's next label number is kept with it
	}
	add, err := s.linkIDs(ctx, sp, a, "links", nil)
	if err != nil {
		return nil, err
	}
	drop, err := s.linkIDs(ctx, sp, a, "unlink", nil)
	if err != nil {
		return nil, err
	}
	if id == "" {
		if ext := docStr(d, "ext_id"); ext != "" {
			id = wardenExtID(sp.id, ext)
		} else {
			id = wardenRandomID("e")
		}
		_, all, err := s.db.WhereEq(ctx, wardenEvents, "space", sp.id)
		if err != nil {
			return nil, err
		}
		if len(all) >= wardenMaxEvents && !slices.Contains(all, id) {
			return nil, roomErr{fmt.Sprintf("a space has at most %d events", wardenMaxEvents)}
		}
	}
	if newWards {
		if err := s.save(ctx, sp); err != nil {
			return nil, err
		}
	}
	wardenSetLinks(d, id, add, drop)
	s.stamp(d, sp)
	if err := s.db.Set(ctx, wardenEvents, id, d); err != nil {
		return nil, err
	}
	if err := s.linkOthers(ctx, sp, id, add, drop); err != nil {
		return nil, err
	}
	return wardenEventJSON(id, d, s.now()), nil
}

func (s *wardenService) stamp(d Doc, sp *wardenSpace) {
	d["space"] = sp.id
	d["updated"] = s.now().UnixMilli()
	d["by"] = sp.who.uid
}

func (s *wardenService) deleteEvent(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	id := argStr(a, "event_id")
	d, err := s.event(ctx, sp, id)
	if err != nil {
		return nil, err
	}
	if err := s.db.Delete(ctx, wardenEvents, id); err != nil {
		return nil, err
	}
	if err := s.linkOthers(ctx, sp, id, nil, docStrs(d, "links")); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": id}, nil
}

// import_events: many events at once; one with an ext_id the space has
// already is changed, not made again. All are checked before any is written.
func (s *wardenService) importEvents(ctx context.Context, sp *wardenSpace, list []any, defaults map[string]any) (any, error) {
	if len(list) == 0 {
		return nil, roomErr{"events is empty"}
	}
	if len(list) > wardenMaxImport {
		return nil, roomErr{fmt.Sprintf("at most %d events at a time", wardenMaxImport)}
	}
	_, have, err := s.db.WhereEq(ctx, wardenEvents, "space", sp.id)
	if err != nil {
		return nil, err
	}
	type put struct {
		id        string
		doc       Doc
		new       bool
		args      map[string]any
		add, drop []string
	}
	puts := []put{}
	seen := map[string]bool{}
	newWards := false
	for i, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, roomErr{fmt.Sprintf("events[%d] is not an object", i)}
		}
		for k, v := range defaults {
			if _, ok := m[k]; !ok && v != nil {
				m[k] = v
			}
		}
		id, base := "", Doc{}
		if ext := argStr(m, "ext_id"); ext != "" {
			id = wardenExtID(sp.id, ext)
			if seen[id] {
				continue // the same event twice in one batch: the first holds
			}
			if slices.Contains(have, id) {
				if base, err = s.db.Get(ctx, wardenEvents, id); err != nil {
					return nil, err
				}
			}
		} else {
			id = wardenRandomID("e")
		}
		d, err := sp.eventDoc(m, base, &newWards)
		if err != nil {
			var re roomErr
			if ok := asRoomErr(err, &re); ok {
				return nil, roomErr{fmt.Sprintf("events[%d]: %s; nothing was imported", i, re.msg)}
			}
			return nil, err
		}
		if sp.saleLabel(d) {
			newWards = true
		}
		seen[id] = true
		puts = append(puts, put{id: id, doc: d, new: !slices.Contains(have, id), args: m})
	}
	// links may name entries of the same import by their ext_id
	for i := range puts {
		p := &puts[i]
		var err error
		if p.add, err = s.linkIDs(ctx, sp, p.args, "links", seen); err == nil {
			p.drop, err = s.linkIDs(ctx, sp, p.args, "unlink", seen)
		}
		if err != nil {
			var re roomErr
			if ok := asRoomErr(err, &re); ok {
				return nil, roomErr{fmt.Sprintf("events[%d]: %s; nothing was imported", i, re.msg)}
			}
			return nil, err
		}
		wardenSetLinks(p.doc, p.id, p.add, p.drop)
	}
	made := 0
	for _, p := range puts {
		if p.new {
			made++
		}
	}
	if len(have)+made > wardenMaxEvents {
		return nil, roomErr{fmt.Sprintf("a space has at most %d events; it has %d", wardenMaxEvents, len(have))}
	}
	if newWards {
		if err := s.save(ctx, sp); err != nil {
			return nil, err
		}
	}
	ids := []any{}
	for _, p := range puts {
		s.stamp(p.doc, sp)
		if err := s.db.Set(ctx, wardenEvents, p.id, p.doc); err != nil {
			return nil, err
		}
		ids = append(ids, p.id)
	}
	for _, p := range puts {
		if err := s.linkOthers(ctx, sp, p.id, p.add, p.drop); err != nil {
			return nil, err
		}
	}
	return map[string]any{"space_id": sp.id, "created": made, "updated": len(puts) - made, "event_ids": ids, "wards": sp.json()["wards"]}, nil
}

func asRoomErr(err error, re *roomErr) bool {
	r, ok := err.(roomErr)
	if ok {
		*re = r
	}
	return ok
}

// ------------------------------------------------------------- operations --

// the operations, for the page and the assistant alike; page: the call
// comes from the page (it takes invites to a verified address)
func (s *wardenService) call(ctx context.Context, who wardenWho, op string, a map[string]any, page bool) (any, error) {
	if a == nil {
		a = map[string]any{}
	}
	switch op {
	case wardenAssistOp:
		return s.assist(ctx, who, a)
	case "list_spaces":
		return s.listSpaces(ctx, who, page)
	case "create_space":
		return s.createSpace(ctx, who, a)
	case "list_entry_types":
		return s.listEntryTypes(ctx, who, a)
	// the caller's own money, in no space (wardenown.go)
	case "own_budget":
		return s.ownBudget(ctx, who, a)
	case "save_own_item", "delete_own_item":
		if who.uid != "" {
			if err := s.backupOwn(ctx, who.uid, op, false); err != nil {
				log.Printf("warden own %s: backup before %s: %v", who.uid, op, err)
			}
		}
		if op == "save_own_item" {
			return s.saveOwnItem(ctx, who, a)
		}
		return s.deleteOwnItem(ctx, who, a)
	// the caller's own choice, kept under their account (wardensummary.go)
	case "email_summary":
		return s.emailSummary(ctx, who, a)
	}
	// the backups are the page's alone (wardenbackup.go): no tool, so no
	// assistant reaches them
	if page && op == wardenListBackupsOp {
		return s.listBackups(ctx, who, a)
	}
	if page && op == wardenRestoreBackupOp {
		return s.restoreBackup(ctx, who, a)
	}
	if op == wardenChangesOp && page {
		sp, err := s.space(ctx, who, argStr(a, "space_id"))
		if err != nil {
			return nil, err
		}
		by := docStr(sp.doc, "changed_by")
		return map[string]any{"changed": wardenChanged(sp.doc), "mine": by != "" && by == who.client}, nil
	}
	tool := findWardenTool(op)
	if tool == nil {
		return nil, roomErr{"no such operation: " + op}
	}
	sp, err := s.space(ctx, who, argStr(a, "space_id"))
	if err != nil {
		return nil, err
	}
	if !tool.readOnly {
		if err := s.backupBefore(ctx, sp, op); err != nil {
			return nil, err
		}
	}
	out, err := s.run(ctx, sp, op, a)
	if err == nil && wardenWrites[op] {
		s.touch(ctx, sp)
	}
	return out, err
}

// the operations that change a space or its events: each marks the space
// changed, so a page that has it open reads it again
var wardenWrites = map[string]bool{
	"set_space": true, "invite_member": true, "remove_member": true, "add_ward": true, "set_ward": true, "remove_ward": true,
	"save_event": true, "delete_event": true, "import_events": true, "import_ics": true,
	"ask_question": true, "answer_question": true, "save_doc": true, "delete_doc": true, "doc_folder": true,
}

// the page asks for this now and then (wardenChangesOp): when the space last
// changed, and whether the change was the asking page's own
func wardenChanged(d Doc) string {
	switch n := d["changed"].(type) {
	case int64:
		return strconv.FormatInt(n, 10)
	case float64:
		return strconv.FormatInt(int64(n), 10)
	}
	return "0"
}

func (s *wardenService) touch(ctx context.Context, sp *wardenSpace) {
	err := s.db.Update(ctx, wardenSpaces, sp.id, Doc{"changed": s.now().UnixMilli(), "changed_by": sp.who.client})
	if err != nil {
		log.Printf("warden %s: marking it changed: %v", sp.id, err)
	}
}

func (s *wardenService) run(ctx context.Context, sp *wardenSpace, op string, a map[string]any) (any, error) {
	switch op {
	case "get_space":
		return s.getSpace(ctx, sp)
	case "set_space":
		return s.setSpace(ctx, sp, a)
	case "delete_space":
		return s.deleteSpace(ctx, sp, a)
	case "invite_member":
		return s.invite(ctx, sp, a)
	case "remove_member":
		return s.removeMember(ctx, sp, a)
	case "add_ward":
		return s.addWardOp(ctx, sp, a)
	case "set_ward":
		return s.setWard(ctx, sp, a)
	case "remove_ward":
		return s.removeWard(ctx, sp, a)
	case "add_place":
		return s.addPlace(ctx, sp, a)
	case "remove_place":
		return s.removePlace(ctx, sp, a)
	case "list_events":
		return s.listEvents(ctx, sp, a)
	case "save_event":
		return s.saveEvent(ctx, sp, a)
	case "delete_event":
		return s.deleteEvent(ctx, sp, a)
	case "import_events":
		l, _ := a["events"].([]any)
		return s.importEvents(ctx, sp, l, nil)
	case "import_ics":
		return s.importICS(ctx, sp, a)
	case "calendar_feed":
		return s.calendarFeed(ctx, sp, a)
	case "budget":
		return s.budget(ctx, sp, a)
	case "ask_question":
		return s.askQuestion(ctx, sp, a)
	case "answer_question":
		return s.answerQuestion(ctx, sp, a)
	case "list_questions":
		return s.listQuestions(ctx, sp, a)
	case "list_docs":
		return s.listDocs(ctx, sp, a)
	case "get_doc":
		return s.getDoc(ctx, sp, a)
	case "save_doc":
		return s.saveDoc(ctx, sp, a)
	case "delete_doc":
		return s.deleteDoc(ctx, sp, a)
	case "doc_folder":
		return s.docFolder(ctx, sp, a)
	}
	return nil, roomErr{"no such operation: " + op}
}
