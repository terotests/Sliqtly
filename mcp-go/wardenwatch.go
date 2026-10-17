// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's deal watcher: an entry (a thing still to get, a need, a planned
// purchase, any entry) can carry a watch, and now and then the server looks
// for offers of it on second-hand sites and the web. New finds are kept on
// the entry and the space gets a reminder of them, which the summary's
// Muista lists.
//
// The search is Gemini's with Google Search (the assistant's model and
// account, aimodel.go), limited to a site per source with site:. Tori.fi,
// Vinted and Huuto.net have no public API, and the server does not read
// their pages itself.
//
// A watch lives on its entry, warden_events/{id}:
//
//	watch:    {query, max_price, sources, exclude, every_days, finds, seen,
//	           last_run, last_error, by}
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
}

type wardenSearcher interface {
	// near: a town the listings near which come first ("" anywhere)
	Search(ctx context.Context, query, near string, n int) ([]wardenHit, error)
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

List up to %d individual listings you found in the search results, one per line, as:
<the listing's title> | <its price in euros, or ?> | <the town where the item is, or ?>
Only real listings from the results, no shops' category pages, no other text.`

const wardenSearchNear = `

The buyer lives in %s, Finland: search for listings there and nearby first, and list those first.`

func (g *geminiSearch) Search(ctx context.Context, query, near string, n int) ([]wardenHit, error) {
	ask := fmt.Sprintf(wardenSearchAsk, query, n)
	if near != "" {
		ask += fmt.Sprintf(wardenSearchNear, near)
	}
	body, _ := json.Marshal(map[string]any{
		"contents":         []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": ask}}}},
		"tools":            []any{map[string]any{"google_search": map[string]any{}}},
		"generationConfig": map[string]any{"temperature": 0},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.m.endpoint(), strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if g.m.key != "" {
		req.Header.Set("x-goog-api-key", g.m.key)
	}
	res, err := g.m.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("search: %s", res.Status)
	}
	var out struct {
		Candidates []struct {
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
		return nil, fmt.Errorf("search: %v", err)
	}
	if len(out.Candidates) == 0 {
		return nil, nil
	}
	gm := out.Candidates[0].Grounding
	hits := []wardenHit{}
	used := map[int]bool{}
	for _, sup := range gm.Supports {
		for _, line := range strings.Split(sup.Segment.Text, "\n") {
			title, rest, _ := strings.Cut(strings.TrimSpace(strings.TrimLeft(line, "-*•0123456789. ")), "|")
			price, place, _ := strings.Cut(rest, "|")
			title, price, place = strings.TrimSpace(title), strings.TrimSpace(price), strings.TrimSpace(place)
			if place == "?" {
				place = ""
			}
			if title == "" || len(sup.Chunks) == 0 {
				continue
			}
			i := sup.Chunks[0]
			if i < 0 || i >= len(gm.Chunks) || used[i] {
				continue
			}
			used[i] = true
			u := g.resolve(ctx, gm.Chunks[i].Web.URI)
			if u == "" {
				continue
			}
			snippet := ""
			if price != "" && price != "?" {
				snippet = price
				if !strings.ContainsAny(price, "€") {
					snippet += " €"
				}
			}
			hits = append(hits, wardenHit{Title: title, URL: u, Snippet: snippet, Location: aiClip(place, 60)})
		}
	}
	return hits, nil
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
	if err != nil {
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
	if t := millis(w["last_run"]); t > 0 {
		out["last_run"] = time.UnixMilli(t).UTC().Format(time.RFC3339)
	}
	if e := docStr(w, "last_error"); e != "" {
		out["last_error"] = e
	}
	finds := []any{}
	for _, f := range docMaps(w, "finds") {
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

// One watch's searches: the new finds are put first on the entry, and a
// reminder of them is made or renewed. → the new finds
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
	now := s.now()
	fresh := []any{}
	errs := []string{}
	for _, src := range srcs {
		site, name, ok := wardenWatchSource(src)
		if !ok {
			continue
		}
		query := q
		if site != "" {
			query += " site:" + site
		}
		hits, err := s.search.Search(ctx, query, wardenWatchArea(sp, d, w), wardenWatchHits)
		if err != nil {
			errs = append(errs, name+": "+err.Error())
			continue
		}
	hit:
		for _, h := range hits {
			u, err := url.Parse(h.URL)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
				continue
			}
			host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
			if site != "" && host != site && !strings.HasSuffix(host, "."+site) {
				continue
			}
			// a page of search results or a category, not an offer
			p := strings.ToLower(u.Path)
			if u.Query().Has("q") || u.Query().Has("search_text") || strings.Contains(p, "/search") || p == "" || p == "/" {
				continue
			}
			text := strings.ToLower(h.Title + " " + h.Snippet)
			for _, x := range exclude {
				if strings.Contains(text, x) {
					continue hit
				}
			}
			price := wardenPrice(h.Title + " " + h.Snippet)
			if max > 0 && price > max {
				continue
			}
			key := wardenHashURL(h.URL)
			if slices.Contains(seen, key) {
				continue
			}
			seen = append(seen, key)
			f := map[string]any{"title": aiClip(h.Title, 160), "url": h.URL, "source": name, "found": now.In(wardenLoc(sp)).Format("2006-01-02")}
			if price > 0 {
				f["price"] = price
			}
			if h.Location != "" {
				f["location"] = h.Location
			}
			if h.Snippet != "" {
				f["snippet"] = aiClip(h.Snippet, 240)
			}
			fresh = append(fresh, f)
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
	finds := append(slices.Clone(fresh), anys(docMaps(w, "finds"))...)
	if len(finds) > wardenWatchFinds {
		finds = finds[:wardenWatchFinds]
	}
	w["finds"] = finds
	w["last_run"] = now.UnixMilli()
	d["watch"] = w
	if err := s.db.Set(ctx, wardenEvents, id, d); err != nil {
		return nil, err
	}
	if len(fresh) > 0 {
		if err := s.watchReminder(ctx, sp, id, d, fresh); err != nil {
			return fresh, err
		}
	}
	return fresh, nil
}

func wardenLoc(sp *wardenSpace) *time.Location {
	if l, err := time.LoadLocation(wardenOr(docStr(sp.doc, "tz"), wardenTZ)); err == nil {
		return l
	}
	return time.UTC
}

// the reminder of an entry's new finds: one per entry, renewed (dated
// today, not done) on every run that finds something
func (s *wardenService) watchReminder(ctx context.Context, sp *wardenSpace, id string, d Doc, fresh []any) error {
	fi := docStr(sp.doc, "lang") != "en"
	title := fmt.Sprintf("Offers: %s (%d new)", docStr(d, "title"), len(fresh))
	if fi {
		title = fmt.Sprintf("Tarjouksia: %s (%d uutta)", docStr(d, "title"), len(fresh))
	}
	var b strings.Builder
	for _, e := range fresh {
		f := e.(map[string]any)
		line := "• " + docStr(f, "title")
		if p := wardenNum(f["price"]); p > 0 {
			line += fmt.Sprintf(" – %s €", strconv.FormatFloat(p, 'f', -1, 64))
		}
		line += " (" + docStr(f, "source") + ") " + docStr(f, "url") + "\n"
		if b.Len()+len(line) > 1900 {
			break
		}
		b.WriteString(line)
	}
	a := map[string]any{
		"type":   "reminder",
		"title":  aiClip(title, 200),
		"date":   s.now().In(wardenLoc(sp)).Format("2006-01-02"),
		"notes":  strings.TrimSpace(b.String()),
		"ext_id": "watch:" + id,
		"source": "Tarjousvahti",
		"fields": map[string]any{"about": aiClip(docStr(d, "title"), 200)},
	}
	if w := docStr(d, "ward"); w != "" {
		a["ward"] = w
	}
	nw := false
	doc, err := sp.eventDoc(a, Doc{}, &nw)
	if err != nil {
		return err
	}
	s.stamp(doc, sp)
	return s.db.Set(ctx, wardenEvents, wardenExtID(sp.id, "watch:"+id), doc)
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
