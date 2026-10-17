// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's deal watcher: an entry (a thing still to get, a need, a planned
// purchase, any entry) can carry a watch, and now and then the server looks
// for offers of it on second-hand sites and the web. New finds are kept on
// the entry and listed in the summary's Tarjouksia tab; nothing goes in the
// calendar.
//
// The search is Gemini's with Google Search (the assistant's model and
// account, aimodel.go), limited to a site per source with site:. Tori.fi,
// Vinted and Huuto.net have no public API, and the server does not read
// their pages itself.
//
// A watch lives on its entry, warden_events/{id}:
//
//	watch:    {query, intent, max_price, sources, exclude, every_days, finds,
//	           seen, runs, last_run, last_error, by}
//	finds:    [{id, title, url, source, found, price, location, snippet,
//	           score, why, rating, reason}] (rating: like or hide, rate_find)
//	runs:     the latest runs' logs, newest first: {at, local, by, query,
//	           near, sources, results, new, left_out: {reason: count},
//	           errors} (local: the space's time; by: who ran it, "auto"
//	           for the daily run; the Vahdit page lists them)
//	watching: true while it is on (what the daily run looks for)
//
// The runs:
//
//	POST /apps/warden/api/run_watches  Authorization: Bearer <a Google ID
//	    token of the service account in WARDEN_CRON_SA, for this address>
//	    the watches that are due, oldest first, at most wardenWatchBatch at
//	    a time; {ran, found, more} (.github/workflows/warden-watch.yml calls
//	    it daily, again while more)
//	run_watch (MCP, the page)           one entry's, now

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	wardenWatchFinds = 20  // finds kept on an entry, newest first
	wardenWatchSeen  = 300 // addresses remembered, so a find is new once
	wardenWatchBatch = 25  // watches one scheduled call runs
	wardenWatchHits  = 10  // results asked of one search
	wardenWatchMax   = 50  // watches of a space
	wardenWatchRuns  = 30  // run logs kept
	wardenWatchAvoid = 10  // hidden finds told to the search
	// a find that fits worse than this (0-100) is kept but not reminded of
	wardenWatchFit = 50
	// the watcher as the author of what it writes
	wardenWatchUID = "warden-watch"
)

// the town a watch prefers when nothing in the space names one
const wardenWatchHome = "Tampere"

// where a watch looks: a site (searched with site:) or the whole web
var wardenWatchSources = []struct{ id, site, name string }{
	{"tori", "tori.fi", "Tori"},
	{"vinted", "vinted.fi", "Vinted"},
	{"huuto", "huuto.net", "Huuto.net"},
	{"web", "", "Web"},
}

var wardenWatchDefault = []string{"tori", "vinted", "huuto"}

// ----------------------------------------------------------------- search --

// one result of a web search
type wardenHit struct {
	Title, URL, Snippet string
	// where the item is, as the listing says ("" when not said)
	Location string
	// how well it fits what the buyer wants, 0-100 (-1 not said), and why
	Score int
	Why   string
}

// what a search looks for
type wardenAsk struct {
	Query string
	// a town the listings near which come first ("" anywhere)
	Near string
	// what the buyer wants, in their words ("" the query)
	Intent string
	// listings the buyer did not want, with why ("IKEA Kura (too far)")
	Avoid []string
	N     int
	// the language the why is written in ("" English)
	Lang string
}

type wardenSearcher interface {
	// the hits, and when there are none a note on what the search answered
	Search(ctx context.Context, ask wardenAsk) ([]wardenHit, string, error)
	// children's events (wardenkids.go)
	Events(ctx context.Context, ask wardenEventAsk) ([]wardenEventHit, string, error)
}

// Gemini answering with Google Search (grounding), on the assistant's own
// model and account: it searches, lists the offers it found one per line,
// and each line is tied to the result it came from. Those results are
// Google's redirect addresses; the listing's own address is read from the
// redirect, without opening the listing.
type geminiSearch struct {
	m *geminiModel
	// follows nothing: the redirect's Location is the address
	redirect *http.Client
}

// the deal watcher's search on the assistant's model; nil when it is not
// Gemini (no search)
func newWardenSearch(m aiModel) wardenSearcher {
	g, ok := m.(*geminiModel)
	if !ok || g == nil {
		return nil
	}
	c := *g
	c.tools = nil
	c.model = env("WARDEN_SEARCH_MODEL", g.model)
	return &geminiSearch{m: &c, redirect: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

const wardenSearchAsk = `Search for current second-hand or for-sale listings matching: %s

What the buyer wants: %s

List up to %d individual listings you found in the search results, one per line, as:
<the listing's title> | <its price in euros, or ?> | <the town where the item is, or ?> | <how well it fits what the buyer wants, 0 to 100> | <a few words why>
Only real listings from the results, no shops' category pages, no other text.`

const wardenSearchNear = `

The buyer lives in %s, Finland: search for listings there and nearby first, and list those first.`

// the why in the space's language: the page shows it as it is
const wardenWhyFinnish = `

Write the few words why in Finnish.`

func wardenWhyLang(lang string) string {
	if lang == "fi" {
		return wardenWhyFinnish
	}
	return ""
}

// a space's language: Finnish unless it is English
func wardenSpaceLang(sp *wardenSpace) string {
	if docStr(sp.doc, "lang") == "en" {
		return "en"
	}
	return "fi"
}

const wardenSearchAvoid = `

The buyer did not want these earlier listings; give ones like them a low fit:
%s`

func (g *geminiSearch) Search(ctx context.Context, a wardenAsk) ([]wardenHit, string, error) {
	ask := fmt.Sprintf(wardenSearchAsk, a.Query, wardenOr(a.Intent, a.Query), a.N)
	if a.Near != "" {
		ask += fmt.Sprintf(wardenSearchNear, a.Near)
	}
	if len(a.Avoid) > 0 {
		ask += fmt.Sprintf(wardenSearchAvoid, "- "+strings.Join(a.Avoid, "\n- "))
	}
	ask += wardenWhyLang(a.Lang)
	lines, note, err := g.ground(ctx, ask, 5)
	if err != nil {
		return nil, "", err
	}
	hits := []wardenHit{}
	for _, l := range lines {
		title, price, place, why := l.parts[0], l.parts[1], l.parts[2], l.parts[4]
		snippet := ""
		if price != "" && price != "?" {
			snippet = price
			if !strings.ContainsAny(price, "€") {
				snippet += " €"
			}
		}
		hits = append(hits, wardenHit{Title: title, URL: l.url, Snippet: snippet, Location: aiClip(place, 60), Score: wardenFit(l.parts[3]), Why: aiClip(why, 120)})
	}
	return hits, note, nil
}

// a fit as the search wrote it ("85", "85%"); -1 when not a number 0-100
func wardenFit(s string) int {
	if n, err := strconv.Atoi(strings.TrimSuffix(s, "%")); err == nil && n >= 0 && n <= 100 {
		return n
	}
	return -1
}

// a line of a grounded answer: its "|" separated parts (n of them, "" for
// those missing, "?" read as ""), and the address of the result it came
// from
type wardenLine struct {
	parts []string
	url   string
}

// Gemini with Google Search, asked to answer a line per result: each line
// tied to a result, its redirect resolved to the page's own address
func (g *geminiSearch) ground(ctx context.Context, ask string, n int) ([]wardenLine, string, error) {
	body, _ := json.Marshal(map[string]any{
		"contents":         []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": ask}}}},
		"tools":            []any{map[string]any{"google_search": map[string]any{}}},
		"generationConfig": map[string]any{"temperature": 0},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.m.endpoint(), strings.NewReader(string(body)))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if g.m.key != "" {
		req.Header.Set("x-goog-api-key", g.m.key)
	}
	res, err := g.m.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode != 200 {
		return nil, "", fmt.Errorf("search: %s", res.Status)
	}
	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			Grounding struct {
				Chunks []struct {
					Web struct {
						URI   string `json:"uri"`
						Title string `json:"title"`
					} `json:"web"`
				} `json:"groundingChunks"`
				Supports []struct {
					Segment struct {
						Text string `json:"text"`
					} `json:"segment"`
					Chunks []int `json:"groundingChunkIndices"`
				} `json:"groundingSupports"`
			} `json:"groundingMetadata"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", fmt.Errorf("search: %v", err)
	}
	if len(out.Candidates) == 0 {
		return nil, "no answer", nil
	}
	c := out.Candidates[0]
	gm := c.Grounding
	text := ""
	for _, p := range c.Content.Parts {
		text += p.Text
	}
	// a line of the answer is tied to the results its supports cite: a
	// support's segment is the line, a part of it, or several lines
	lines := []wardenLine{}
	used := map[int]bool{}
	asked, unbacked, unresolved := 0, 0, 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*•0123456789. "))
		if !strings.Contains(line, "|") {
			continue
		}
		parts := strings.Split(line, "|")
		for len(parts) < n {
			parts = append(parts, "")
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(strings.Trim(strings.TrimSpace(parts[i]), "*"))
			if parts[i] == "?" {
				parts[i] = ""
			}
		}
		if parts[0] == "" {
			continue
		}
		asked++
		cited := false
		u := ""
	find:
		for _, sup := range gm.Supports {
			seg := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(sup.Segment.Text), "-*•0123456789. "))
			if seg == "" || !(strings.Contains(line, seg) || strings.Contains(seg, line)) {
				continue
			}
			for _, i := range sup.Chunks {
				if i < 0 || i >= len(gm.Chunks) || used[i] {
					continue
				}
				cited = true
				if u = g.resolve(ctx, gm.Chunks[i].Web.URI); u != "" {
					used[i] = true
					break find
				}
			}
		}
		switch {
		case u != "":
			lines = append(lines, wardenLine{parts: parts, url: u})
		case cited:
			unresolved++
		default:
			unbacked++
		}
	}
	note := ""
	if len(lines) == 0 {
		note = fmt.Sprintf("%d lines, %d without a result, %d results unresolved, %d results, %d supports: %s", asked, unbacked, unresolved, len(gm.Chunks), len(gm.Supports), aiClip(strings.Join(strings.Fields(text), " "), 200))
	}
	return lines, note, nil
}

// the address a grounding redirect leads to ("" when it leads nowhere)
func (g *geminiSearch) resolve(ctx context.Context, u string) string {
	if u == "" {
		return ""
	}
	if !strings.Contains(u, "grounding-api-redirect") {
		return u
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return ""
	}
	res, err := g.redirect.Do(req)
	if err == nil {
		res.Body.Close()
		if l := res.Header.Get("Location"); l != "" {
			return l
		}
	}
	// some redirects answer only GET
	if req, err = http.NewRequestWithContext(ctx, http.MethodGet, u, nil); err != nil {
		return ""
	}
	if res, err = g.redirect.Do(req); err != nil {
		return ""
	}
	res.Body.Close()
	return res.Header.Get("Location")
}

var wardenTagRe = regexp.MustCompile(`<[^>]*>`)

// a search result's text without its <strong> marks and entities
func wardenPlain(s string) string {
	s = wardenTagRe.ReplaceAllString(s, "")
	return strings.TrimSpace(strings.NewReplacer("&amp;", "&", "&quot;", `"`, "&#39;", "'", "&lt;", "<", "&gt;", ">", "&nbsp;", " ").Replace(s))
}

// a price in euros in a listing's text: "120 €", "1 200€", "€ 35,50"; 0
// when none
var wardenPriceRe = regexp.MustCompile(`(?:(\d{1,3}(?:[ \x{00a0}]\d{3})*|\d+)(?:[.,](\d{1,2}))?\s?(?:€|eur\b|euroa))|(?:€\s?(\d+)(?:[.,](\d{1,2}))?)`)

func wardenPrice(s string) float64 {
	m := wardenPriceRe.FindStringSubmatch(strings.ToLower(s))
	if m == nil {
		return 0
	}
	whole, frac := m[1], m[2]
	if whole == "" {
		whole, frac = m[3], m[4]
	}
	whole = strings.NewReplacer(" ", "", " ", "").Replace(whole)
	v, err := strconv.ParseFloat(whole+"."+wardenOr(frac, "0"), 64)
	if err != nil {
		return 0
	}
	return v
}

// ------------------------------------------------------------------ watch --

func wardenWatchSource(id string) (string, string, bool) {
	for _, s := range wardenWatchSources {
		if s.id == id {
			return s.site, s.name, true
		}
	}
	return "", "", false
}

func wardenHashURL(u string) string {
	h := sha256.Sum256([]byte(u))
	return hex.EncodeToString(h[:8])
}

// an entry that has been got (or is no longer wanted) is not watched
func wardenWatchDone(d Doc) bool {
	f, _ := d["fields"].(map[string]any)
	switch s, _ := f["status"].(string); s {
	case "done", "cancelled", "archived":
		return true
	}
	if b, _ := f["bought"].(bool); b {
		return true
	}
	st, _ := f["state"].(string)
	switch docStr(d, "type") {
	case "item":
		return st != "" && st != "missing"
	case "need":
		return st == "bought" || st == "chosen"
	}
	return false
}

// the town whose listings a watch prefers: its own area, else the first
// usual place of the entry's ward, else the space's (wardenplaces.go), else
// the entry's own place (an address names its town), else Tampere
func wardenWatchArea(sp *wardenSpace, d Doc, w map[string]any) string {
	if a := docStr(w, "area"); a != "" {
		return a
	}
	first := func(m map[string]any) string {
		for _, p := range docMaps(m, "places") {
			if n := docStr(p, "name"); n != "" {
				return n
			}
		}
		return ""
	}
	if ward := docStr(d, "ward"); ward != "" {
		for _, wd := range docMaps(sp.doc, "wards") {
			if docStr(wd, "id") == ward {
				if n := first(wd); n != "" {
					return n
				}
			}
		}
	}
	if n := first(sp.doc); n != "" {
		return n
	}
	if p := docStr(d, "place"); p != "" {
		return p
	}
	return wardenWatchHome
}

// what a watch searches with: its own words, or the entry's title
func wardenWatchQuery(d Doc, w map[string]any) string {
	if q := docStr(w, "query"); q != "" {
		return q
	}
	return docStr(d, "title")
}

// the watch as an answer, without what it remembers seeing
func wardenWatchJSON(d Doc) map[string]any {
	w, _ := d["watch"].(map[string]any)
	if w == nil {
		return nil
	}
	out := map[string]any{"query": wardenWatchQuery(d, w), "sources": anys(docStrs(w, "sources")), "every_days": wardenWatchEvery(w), "active": d["watching"] == true}
	if p := wardenNum(w["max_price"]); p > 0 {
		out["max_price"] = p
	}
	if l := docStrs(w, "exclude"); len(l) > 0 {
		out["exclude"] = anys(l)
	}
	if a := docStr(w, "area"); a != "" {
		out["area"] = a
	}
	if i := docStr(w, "intent"); i != "" {
		out["intent"] = i
	}
	if r := docMaps(w, "runs"); len(r) > 0 {
		out["runs"] = anys(r)
	}
	if t := millis(w["last_run"]); t > 0 {
		out["last_run"] = time.UnixMilli(t).UTC().Format(time.RFC3339)
	}
	if e := docStr(w, "last_error"); e != "" {
		out["last_error"] = e
	}
	finds := []any{}
	for _, f := range docMaps(w, "finds") {
		if docStr(f, "id") == "" {
			// a find kept before finds had ids: the id rate_find knows it by
			c := map[string]any{"id": wardenHashURL(docStr(f, "url"))}
			for k, v := range f {
				c[k] = v
			}
			f = c
		}
		finds = append(finds, f)
	}
	out["finds"] = finds
	if wardenWatchDone(d) {
		out["active"] = false
		out["paused"] = "the entry is done or got"
	}
	return out
}

func wardenWatchEvery(w map[string]any) int {
	n := int(wardenNum(w["every_days"]))
	if n < 1 {
		return 1
	}
	return n
}

// set_watch: a watch on an entry made or changed; off: false stops it
func (s *wardenService) setWatch(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	id := argStr(a, "event_id")
	d, err := s.event(ctx, sp, id)
	if err != nil {
		return nil, err
	}
	w := map[string]any{}
	if old, _ := d["watch"].(map[string]any); old != nil {
		for k, v := range old {
			w[k] = v
		}
	} else {
		docs, _, err := s.db.WhereEq(ctx, wardenEvents, "space", sp.id)
		if err != nil {
			return nil, err
		}
		n := 0
		for _, e := range docs {
			if e["watching"] == true {
				n++
			}
		}
		if n >= wardenWatchMax {
			return nil, roomErr{fmt.Sprintf("a space has at most %d watches", wardenWatchMax)}
		}
		w["sources"] = anys(wardenWatchDefault)
	}
	if v, ok := a["area"]; ok {
		ar, _ := v.(string)
		ar = strings.TrimSpace(ar)
		if len([]rune(ar)) > 80 {
			return nil, roomErr{"area is at most 80 characters"}
		}
		w["area"] = ar
	}
	if v, ok := a["intent"]; ok {
		in, _ := v.(string)
		in = strings.TrimSpace(in)
		if len([]rune(in)) > 300 {
			return nil, roomErr{"intent is at most 300 characters"}
		}
		w["intent"] = in
	}
	if v, ok := a["query"]; ok {
		q, _ := v.(string)
		q = strings.TrimSpace(q)
		if len([]rune(q)) > 120 {
			return nil, roomErr{"query is at most 120 characters"}
		}
		w["query"] = q
	}
	if v, ok := a["max_price"]; ok {
		if v == nil {
			delete(w, "max_price")
		} else if p, ok := v.(float64); !ok || p < 0 {
			return nil, roomErr{"max_price is a number of euros"}
		} else if p == 0 {
			delete(w, "max_price")
		} else {
			w["max_price"] = p
		}
	}
	if v, ok := a["sources"]; ok && v != nil {
		l, _ := v.([]any)
		srcs := []string{}
		for _, e := range l {
			id, _ := e.(string)
			id = strings.ToLower(strings.TrimSpace(id))
			if _, _, ok := wardenWatchSource(id); !ok {
				return nil, roomErr{"sources are tori, vinted, huuto and web; got " + id}
			}
			if !slices.Contains(srcs, id) {
				srcs = append(srcs, id)
			}
		}
		if len(srcs) == 0 {
			return nil, roomErr{"sources needs at least one of tori, vinted, huuto, web"}
		}
		w["sources"] = anys(srcs)
	}
	if v, ok := a["exclude"]; ok {
		l, _ := v.([]any)
		ex := []string{}
		for _, e := range l {
			if s, _ := e.(string); strings.TrimSpace(s) != "" && len(ex) < 10 {
				ex = append(ex, strings.ToLower(strings.TrimSpace(s)))
			}
		}
		w["exclude"] = anys(ex)
	}
	if v, ok := a["every_days"].(float64); ok {
		if v < 1 || v > 30 {
			return nil, roomErr{"every_days is 1 to 30"}
		}
		w["every_days"] = float64(int(v))
	}
	if wardenWatchQuery(d, w) == "" {
		return nil, roomErr{"query is missing and the entry has no title"}
	}
	w["by"] = sp.who.uid
	d["watch"] = w
	d["watching"] = argBool(a, "active", true)
	if err := s.db.Set(ctx, wardenEvents, id, d); err != nil {
		return nil, err
	}
	out := map[string]any{"event_id": id, "title": docStr(d, "title"), "watch": wardenWatchJSON(d)}
	if s.search == nil {
		out["note"] = "Saved. This server has no search (its assistant's model is not Gemini), so the watch does not search."
	}
	return out, nil
}

// list_watches: the space's entries that have a watch
func (s *wardenService) listWatches(ctx context.Context, sp *wardenSpace) (any, error) {
	docs, ids, err := s.events(ctx, sp)
	if err != nil {
		return nil, err
	}
	l := []any{}
	for i, d := range docs {
		if w := wardenWatchJSON(d); w != nil {
			l = append(l, map[string]any{"event_id": ids[i], "title": docStr(d, "title"), "type": wardenOr(docStr(d, "type"), "event"), "watch": w})
		}
	}
	return map[string]any{"space_id": sp.id, "watches": l, "search": s.search != nil}, nil
}

// run_watch: one entry's watch, now
func (s *wardenService) runWatchOp(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	if s.search == nil {
		return nil, roomErr{"This server has no search (its assistant's model is not Gemini)."}
	}
	id := argStr(a, "event_id")
	d, err := s.event(ctx, sp, id)
	if err != nil {
		return nil, err
	}
	if d["watch"] == nil {
		return nil, roomErr{"this entry has no watch; set_watch makes one"}
	}
	if why := s.watchLimit(sp.who.uid); why != "" {
		return nil, roomErr{"Too many searches in a short time; try again in a few minutes."}
	}
	found, err := s.runWatch(ctx, sp, id, d)
	if err != nil {
		return nil, err
	}
	return map[string]any{"event_id": id, "new": found, "watch": wardenWatchJSON(d)}, nil
}

// One watch's searches: the new finds are put first on the entry, best fit
// first, and a log of the run is kept ("new" counts those that fit). They
// are shown in the summary's Tarjouksia tab, not in the calendar. → the new
// finds
func (s *wardenService) runWatch(ctx context.Context, sp *wardenSpace, id string, d Doc) ([]any, error) {
	w, _ := d["watch"].(map[string]any)
	q := wardenWatchQuery(d, w)
	max := wardenNum(w["max_price"])
	exclude := docStrs(w, "exclude")
	seen := docStrs(w, "seen")
	srcs := docStrs(w, "sources")
	if len(srcs) == 0 {
		srcs = wardenWatchDefault
	}
	old := docMaps(w, "finds")
	ask := wardenAsk{Near: wardenWatchArea(sp, d, w), Intent: docStr(w, "intent"), Avoid: wardenWatchAvoided(old), N: wardenWatchHits, Lang: wardenSpaceLang(sp)}
	now := s.now()
	fresh := []map[string]any{}
	errs := []string{}
	notes := []string{}
	results := 0
	left := map[string]int{}
	for _, src := range srcs {
		site, name, ok := wardenWatchSource(src)
		if !ok {
			continue
		}
		ask.Query = q
		if site != "" {
			ask.Query += " site:" + site
		}
		hits, note, err := s.search.Search(ctx, ask)
		if err != nil {
			errs = append(errs, name+": "+err.Error())
			continue
		}
		if note != "" {
			notes = append(notes, name+": "+note)
		}
		results += len(hits)
	hit:
		for _, h := range hits {
			u, err := url.Parse(h.URL)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
				left["not_listing"]++
				continue
			}
			host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
			if site != "" && host != site && !strings.HasSuffix(host, "."+site) {
				left["other_site"]++
				continue
			}
			// a page of search results or a category, not an offer
			p := strings.ToLower(u.Path)
			if u.Query().Has("q") || u.Query().Has("search_text") || strings.Contains(p, "/search") || p == "" || p == "/" {
				left["not_listing"]++
				continue
			}
			text := strings.ToLower(h.Title + " " + h.Snippet)
			for _, x := range exclude {
				if strings.Contains(text, x) {
					left["excluded"]++
					continue hit
				}
			}
			price := wardenPrice(h.Title + " " + h.Snippet)
			if max > 0 && price > max {
				left["too_expensive"]++
				continue
			}
			key := wardenHashURL(h.URL)
			if slices.Contains(seen, key) {
				left["seen"]++
				continue
			}
			seen = append(seen, key)
			f := map[string]any{"id": key, "title": aiClip(h.Title, 160), "url": h.URL, "source": name, "found": now.In(wardenLoc(sp)).Format("2006-01-02")}
			if price > 0 {
				f["price"] = price
			}
			if h.Location != "" {
				f["location"] = h.Location
			}
			if h.Snippet != "" {
				f["snippet"] = aiClip(h.Snippet, 240)
			}
			if h.Score >= 0 {
				f["score"] = float64(h.Score)
			}
			if h.Why != "" {
				f["why"] = h.Why
			}
			fresh = append(fresh, f)
		}
	}
	sort.SliceStable(fresh, func(a, b int) bool { return wardenFindFit(fresh[a]) > wardenFindFit(fresh[b]) })
	notify := []map[string]any{}
	for _, f := range fresh {
		if wardenFindFit(f) >= wardenWatchFit {
			notify = append(notify, f)
		} else {
			left["low_fit"]++
		}
	}
	if len(errs) > 0 && len(errs) == len(srcs) {
		w["last_error"] = strings.Join(errs, "; ")
	} else {
		delete(w, "last_error")
	}
	if n := len(seen); n > wardenWatchSeen {
		seen = seen[n-wardenWatchSeen:]
	}
	w["seen"] = anys(seen)
	w["finds"] = anys(wardenTrimFinds(append(slices.Clone(fresh), old...)))
	run := wardenRunLog(sp, now)
	run["query"], run["near"], run["sources"] = q, ask.Near, anys(srcs)
	run["results"], run["new"] = results, len(notify)
	if len(left) > 0 {
		lo := map[string]any{}
		for k, v := range left {
			lo[k] = v
		}
		run["left_out"] = lo
	}
	if len(errs) > 0 {
		run["errors"] = anys(errs)
	}
	if len(notes) > 0 {
		run["notes"] = anys(notes)
	}
	runs := append([]any{run}, anys(docMaps(w, "runs"))...)
	if len(runs) > wardenWatchRuns {
		runs = runs[:wardenWatchRuns]
	}
	w["runs"] = runs
	w["last_run"] = now.UnixMilli()
	d["watch"] = w
	if err := s.db.Set(ctx, wardenEvents, id, d); err != nil {
		return nil, err
	}
	return anys(fresh), nil
}

// a run's log as it begins: when, in UTC and in the space's time, and who
// ran it ("auto" for the daily run)
func wardenRunLog(sp *wardenSpace, now time.Time) map[string]any {
	by := wardenOr(sp.who.name, sp.who.uid)
	if sp.who.uid == wardenWatchUID {
		by = "auto"
	}
	return map[string]any{"at": now.UTC().Format(time.RFC3339), "local": now.In(wardenLoc(sp)).Format("2006-01-02 15:04"), "by": by}
}

// a find's fit, 0-100; one the search gave none is taken to fit
func wardenFindFit(f map[string]any) float64 {
	if v, ok := f["score"].(float64); ok {
		return v
	}
	return 100
}

// the finds kept, newest first: the oldest go first, liked ones last
func wardenTrimFinds(l []map[string]any) []map[string]any {
	for i := len(l) - 1; i >= 0 && len(l) > wardenWatchFinds; i-- {
		if docStr(l[i], "rating") != "like" {
			l = slices.Delete(l, i, i+1)
		}
	}
	if len(l) > wardenWatchFinds {
		l = l[:wardenWatchFinds]
	}
	return l
}

// the finds the buyer hid, as told to the search: "title (why)"
func wardenWatchAvoided(finds []map[string]any) []string {
	out := []string{}
	for _, f := range finds {
		if docStr(f, "rating") != "hide" || len(out) >= wardenWatchAvoid {
			continue
		}
		t := docStr(f, "title")
		if r := wardenFindReasons[docStr(f, "reason")]; r != "" {
			t += " (" + r + ")"
		}
		out = append(out, t)
	}
	return out
}

// why a find was hidden, as rate_find takes it and as told to the search
var wardenFindReasons = map[string]string{
	"wrong_item":      "not the thing wanted",
	"too_far":         "too far away",
	"too_expensive":   "too expensive",
	"not_interesting": "not interesting",
}

// rate_find: like or hide one of an entry's finds, or without an entry one
// of the space's children's events (none clears it); hidden
// finds, with why, steer the next searches away from ones like them
func (s *wardenService) rateFind(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	rating, reason := argStr(a, "rating"), argStr(a, "reason")
	switch rating {
	case "like", "hide":
	case "none", "":
		rating = ""
	default:
		return nil, roomErr{"rating is like, hide or none"}
	}
	if _, ok := wardenFindReasons[reason]; reason != "" && (!ok || rating != "hide") {
		return nil, roomErr{"reason is for hide: wrong_item, too_far, too_expensive or not_interesting"}
	}
	id := argStr(a, "event_id")
	if id == "" {
		return s.rateKidFind(ctx, sp, argStr(a, "find_id"), rating, reason)
	}
	d, err := s.event(ctx, sp, id)
	if err != nil {
		return nil, err
	}
	w, _ := d["watch"].(map[string]any)
	if w == nil {
		return nil, roomErr{"this entry has no watch"}
	}
	finds := docMaps(w, "finds")
	fid := argStr(a, "find_id")
	var f map[string]any
	for _, e := range finds {
		if docStr(e, "id") == fid || (docStr(e, "id") == "" && wardenHashURL(docStr(e, "url")) == fid) {
			f = e
		}
	}
	if f == nil {
		return nil, roomErr{"no such find on this entry: " + fid}
	}
	f["id"] = wardenHashURL(docStr(f, "url"))
	delete(f, "rating")
	delete(f, "reason")
	if rating != "" {
		f["rating"] = rating
	}
	if reason != "" {
		f["reason"] = reason
	}
	w["finds"] = anys(finds)
	d["watch"] = w
	if err := s.db.Set(ctx, wardenEvents, id, d); err != nil {
		return nil, err
	}
	return map[string]any{"event_id": id, "find": f}, nil
}

func wardenLoc(sp *wardenSpace) *time.Location {
	if l, err := time.LoadLocation(wardenOr(docStr(sp.doc, "tz"), wardenTZ)); err == nil {
		return l
	}
	return time.UTC
}

// ---------------------------------------------------------------- the runs --

// the watches that are due, oldest run first, at most wardenWatchBatch:
// how many ran, the new finds, and whether more are due
func (s *wardenService) runDueWatches(ctx context.Context) (map[string]any, error) {
	if s.search == nil {
		return nil, roomErr{"no search (the assistant's model is not Gemini)"}
	}
	docs, ids, err := s.db.WhereEq(ctx, wardenEvents, "watching", true)
	if err != nil {
		return nil, err
	}
	now := s.now()
	due := []int{}
	for i, d := range docs {
		w, _ := d["watch"].(map[string]any)
		if w == nil || wardenWatchDone(d) {
			continue
		}
		// a little early still counts: the daily run need not come at the
		// same minute
		every := time.Duration(wardenWatchEvery(w))*24*time.Hour - 2*time.Hour
		if last := millis(w["last_run"]); last > 0 && now.Sub(time.UnixMilli(last)) < every {
			continue
		}
		due = append(due, i)
	}
	sort.SliceStable(due, func(a, b int) bool {
		wa, _ := docs[due[a]]["watch"].(map[string]any)
		wb, _ := docs[due[b]]["watch"].(map[string]any)
		return millis(wa["last_run"]) < millis(wb["last_run"])
	})
	more := len(due) > wardenWatchBatch
	if more {
		due = due[:wardenWatchBatch]
	}
	spaces := map[string]*wardenSpace{}
	ran, found := 0, 0
	for _, i := range due {
		d := docs[i]
		sid := docStr(d, "space")
		sp, ok := spaces[sid]
		if !ok {
			sd, err := s.db.Get(ctx, wardenSpaces, sid)
			if err != nil {
				return nil, err
			}
			if sd != nil {
				sp = &wardenSpace{id: sid, doc: sd, who: wardenWho{uid: wardenWatchUID, name: "Tarjousvahti"}, role: "member"}
			}
			spaces[sid] = sp
		}
		if sp == nil {
			continue
		}
		fresh, err := s.runWatch(ctx, sp, ids[i], d)
		if err != nil {
			return nil, err
		}
		ran++
		found += len(fresh)
	}
	// the children's events watches in what is left of the batch
	if left := wardenWatchBatch - ran; left > 0 {
		kr, kf, km, err := s.runDueKidWatches(ctx, left)
		if err != nil {
			return nil, err
		}
		ran, found, more = ran+kr, found+kf, more || km
	} else {
		more = true
	}
	return map[string]any{"ran": ran, "found": found, "more": more}, nil
}

// POST /apps/warden/api/run_watches, from the scheduler
func serveWardenWatchRun(env *Env, w http.ResponseWriter, r *http.Request) {
	auth := env.warden.cronAuth
	if r.Method != http.MethodPost || auth == nil || !auth(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
		editorJSON(w, 403, map[string]string{"error": "Not allowed."})
		return
	}
	out, err := env.warden.runDueWatches(r.Context())
	var re roomErr
	switch {
	case asRoomErr(err, &re):
		editorJSON(w, 400, map[string]string{"error": re.msg})
	case err != nil:
		editorJSON(w, 503, map[string]string{"error": err.Error()})
	default:
		editorJSON(w, 200, out)
	}
}
