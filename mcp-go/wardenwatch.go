// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's deal watcher: an entry (a thing still to get, a need, a planned
// purchase, any entry) can carry a watch, and now and then the server looks
// for offers of it on second-hand sites and the web. New finds are kept on
// the entry and the space gets a reminder of them, which the summary's
// Muista lists.
//
// The search is a web search API's (Brave Search, BRAVE_SEARCH_API_KEY),
// limited to a site per source with site:. Tori.fi, Vinted and Huuto.net
// have no public API, and the server does not read their pages itself.
//
// A watch lives on its entry, warden_events/{id}:
//
//	watch:    {query, max_price, sources, exclude, every_days, finds, seen,
//	           last_run, last_error, by}
//	watching: true while it is on (what the daily run looks for)
//
// The runs:
//
//	POST /apps/warden/api/run_watches  Authorization: Bearer $WARDEN_CRON_TOKEN
//	    the watches that are due, oldest first, at most wardenWatchBatch at
//	    a time; {ran, found, more} (.github/workflows/warden-watch.yml calls
//	    it daily, again while more)
//	run_watch (MCP, the page)           one entry's, now

package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
}

type wardenSearcher interface {
	Search(ctx context.Context, query string, n int) ([]wardenHit, error)
}

// Brave Search's web search API
type braveSearch struct {
	key  string
	base string // "" the real endpoint; the tests' server
	http *http.Client
}

func newWardenSearch() wardenSearcher {
	if k := os.Getenv("BRAVE_SEARCH_API_KEY"); k != "" {
		return &braveSearch{key: k, http: &http.Client{Timeout: 15 * time.Second}}
	}
	return nil
}

func (b *braveSearch) Search(ctx context.Context, query string, n int) ([]wardenHit, error) {
	base := b.base
	if base == "" {
		base = "https://api.search.brave.com"
	}
	q := url.Values{"q": {query}, "count": {strconv.Itoa(n)}, "country": {"FI"}, "search_lang": {"fi"}, "freshness": {"pm"}, "safesearch": {"moderate"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/res/v1/web/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.key)
	res, err := b.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("search: %s", res.Status)
	}
	var out struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("search: %v", err)
	}
	hits := []wardenHit{}
	for _, r := range out.Web.Results {
		hits = append(hits, wardenHit{Title: wardenPlain(r.Title), URL: r.URL, Snippet: wardenPlain(r.Description)})
	}
	return hits, nil
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
		out["note"] = "Saved. This server has no search API key yet (BRAVE_SEARCH_API_KEY), so the watch does not search until it gets one."
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
		return nil, roomErr{"This server has no search API key yet (BRAVE_SEARCH_API_KEY)."}
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
		hits, err := s.search.Search(ctx, query, wardenWatchHits)
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
		return nil, roomErr{"no search API key (BRAVE_SEARCH_API_KEY)"}
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

// POST /apps/warden/api/run_watches, with the scheduler's token
func serveWardenWatchRun(env *Env, w http.ResponseWriter, r *http.Request) {
	tok := os.Getenv("WARDEN_CRON_TOKEN")
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if r.Method != http.MethodPost || tok == "" || subtle.ConstantTimeCompare([]byte(got), []byte(tok)) != 1 {
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
