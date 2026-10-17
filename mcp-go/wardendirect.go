package main

// The offer watch reads Tori's and Huuto.net's own search, the way a person
// would: Google's index has few of their listings, so a web search answered
// with made-up ones. Huuto.net has an open API; Tori's search page lists the
// listings as links. A site whose search cannot be read falls back to the
// web search (wardenwatch.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// where the sites' searches are (tests point them at their own server)
type wardenSites struct {
	tori, huutoAPI, huuto string
}

var wardenSitesLive = wardenSites{tori: "https://www.tori.fi", huutoAPI: "https://api.huuto.net/1.1", huuto: "https://www.huuto.net"}

// A site's own listings for the words; ok is false when the site's search
// could not be read (then the web search looks instead), and the note says
// what was read.
func (g *geminiSearch) Listings(ctx context.Context, src, q string) (hits []wardenHit, note string, ok bool) {
	switch src {
	case "huuto":
		if hits, note, ok = g.huutoAPI(ctx, q); ok {
			return hits, note, true
		}
		h, n2, ok2 := g.pageListings(ctx, g.sites.huuto+"/haku?words="+url.QueryEscape(q), wardenHuutoItem, g.sites.huuto)
		return h, note + "; " + n2, ok2
	case "tori":
		return g.pageListings(ctx, g.sites.tori+"/recommerce/forsale/search?q="+url.QueryEscape(q), wardenToriItem, g.sites.tori)
	}
	return nil, "", false
}

var (
	wardenToriItem  = regexp.MustCompile(`^(?:https://www\.tori\.fi)?/recommerce/forsale/item/\d+`)
	wardenHuutoItem = regexp.MustCompile(`^(?:https://www\.huuto\.net)?/kohteet/[^/"?#]+/\d+`)
)

func (g *geminiSearch) get(ctx context.Context, u string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Warden; +https://sliqtly.com/apps/warden/)")
	req.Header.Set("Accept-Language", "fi-FI,fi;q=0.9")
	res, err := g.page.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	return res.StatusCode, raw, err
}

// Huuto.net's open API: GET /items?words=
func (g *geminiSearch) huutoAPI(ctx context.Context, q string) ([]wardenHit, string, bool) {
	code, raw, err := g.get(ctx, g.sites.huutoAPI+"/items?words="+url.QueryEscape(q)+"&limit=30")
	if err != nil {
		return nil, "api: " + err.Error(), false
	}
	if code != 200 {
		return nil, fmt.Sprintf("api: %d", code), false
	}
	var out struct {
		Items []struct {
			ID           any     `json:"id"`
			Title        string  `json:"title"`
			CurrentPrice float64 `json:"currentPrice"`
			BuyNowPrice  float64 `json:"buyNowPrice"`
			Location     string  `json:"location"`
			Links        struct {
				Alternative string `json:"alternative"`
			} `json:"links"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "api: " + err.Error(), false
	}
	hits := []wardenHit{}
	for _, it := range out.Items {
		u := it.Links.Alternative
		if u == "" || it.Title == "" {
			continue
		}
		price := it.BuyNowPrice
		if price == 0 {
			price = it.CurrentPrice
		}
		snippet := ""
		if price > 0 {
			snippet = strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", price), "0"), ".") + " €"
		}
		hits = append(hits, wardenHit{Title: it.Title, URL: u, Snippet: snippet, Location: wardenTitleCase(it.Location), Score: -1})
	}
	return hits, fmt.Sprintf("api: %d listings", len(hits)), true
}

var (
	wardenAnchorRe = regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a>`)
	wardenHrefRe   = regexp.MustCompile(`(?i)\bhref\s*=\s*["']([^"']+)["']`)
	wardenLabelRe  = regexp.MustCompile(`(?i)\b(?:aria-label|title)\s*=\s*["']([^"']+)["']`)
	wardenSpaceRe  = regexp.MustCompile(`\s+`)
)

// A search page's listings: its links to a listing, each with the link's
// text as the title and the price in the text that follows it.
func (g *geminiSearch) pageListings(ctx context.Context, u string, item *regexp.Regexp, base string) ([]wardenHit, string, bool) {
	code, raw, err := g.get(ctx, u)
	if err != nil {
		return nil, "page: " + err.Error(), false
	}
	if code != 200 {
		return nil, fmt.Sprintf("page: %d", code), false
	}
	page := string(raw)
	hits := []wardenHit{}
	at := map[string]int{}
	for _, m := range wardenAnchorRe.FindAllStringSubmatchIndex(page, -1) {
		attrs, inner := page[m[2]:m[3]], page[m[4]:m[5]]
		h := wardenHrefRe.FindStringSubmatch(attrs)
		if h == nil {
			continue
		}
		href := strings.ReplaceAll(h[1], "&amp;", "&")
		loc := item.FindString(href)
		if loc == "" {
			continue
		}
		if strings.HasPrefix(loc, "/") {
			loc = base + loc
		}
		title := wardenSpaceRe.ReplaceAllString(wardenPlain(inner), " ")
		if l := wardenLabelRe.FindStringSubmatch(attrs); title == "" && l != nil {
			title = wardenPlain(l[1])
		}
		// what follows the link: its price, often its town
		end := m[1] + 600
		if end > len(page) {
			end = len(page)
		}
		after := wardenSpaceRe.ReplaceAllString(wardenPlain(page[m[1]:end]), " ")
		snippet := ""
		if p := wardenPrice(after); p > 0 {
			snippet = strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", p), "0"), ".") + " €"
		}
		if i, seen := at[loc]; seen {
			// the same listing linked twice (its picture and its title)
			if hits[i].Title == "" {
				hits[i].Title = title
			}
			continue
		}
		at[loc] = len(hits)
		hits = append(hits, wardenHit{Title: title, URL: loc, Snippet: snippet, Score: -1})
	}
	kept := hits[:0]
	for _, h := range hits {
		if h.Title != "" {
			h.Title = aiClip(h.Title, 160)
			kept = append(kept, h)
		}
	}
	if len(kept) == 0 {
		// a page without a single listing link: read wrong, or the site
		// changed; the web search looks instead
		return nil, fmt.Sprintf("page: no listings in %d bytes", len(raw)), false
	}
	return kept, fmt.Sprintf("page: %d listings", len(kept)), true
}

// "TAMPERE" as "Tampere"
func wardenTitleCase(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.ToUpper(s) != s {
		return s
	}
	r := []rune(strings.ToLower(s))
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
}
