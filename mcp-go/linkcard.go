// SPDX-License-Identifier: AGPL-3.0-or-later

// Link previews of shared presentations (rgr/View.rgr LinkCard): a chat app
// (Slack, Teams, iMessage) reads /s/{id} without running the page, so the
// page it gets carries the deck's title, words and first slide in its head.
// The page itself is Hosting's index.html, read from the site and kept a
// minute per instance, so it is always the viewer that is deployed.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	pageKeep     = 30 * time.Second
	cardPictures = 64 // pictures kept per instance
	cardText     = 200
)

type linkCards struct {
	mu     sync.Mutex
	page   string
	pageAt time.Time
	pics   map[string][]byte
	order  []string
}

var (
	pageTitle = regexp.MustCompile(`(?is)<title>.*?</title>`)
	pageDesc  = regexp.MustCompile(`(?is)<meta\s+name="description"[^>]*>`)
)

// the site's index.html, or "" when it cannot be read
func (h *McpHost) sitePage() string {
	c := &h.env.cards
	c.mu.Lock()
	page, at := c.page, c.pageAt
	c.mu.Unlock()
	if page != "" && time.Since(at) < pageKeep {
		return page
	}
	ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(h.env.BaseURL, "/")+"/index.html", nil)
	if err == nil {
		var res *http.Response
		res, err = h.env.ThemeClient.Do(req)
		if err == nil {
			defer res.Body.Close()
			b, rerr := io.ReadAll(io.LimitReader(res.Body, 2<<20))
			if res.StatusCode == 200 && rerr == nil && strings.Contains(string(b), "</head>") {
				c.mu.Lock()
				c.page, c.pageAt = string(b), time.Now()
				c.mu.Unlock()
				return string(b)
			}
			log.Printf("viewer page: HTTP %d", res.StatusCode)
		}
	}
	if err != nil {
		log.Printf("viewer page: %v", err)
	}
	// an older copy is better than none
	return page
}

// ViewerPage is the site's page with the deck's card (LinkCard.json) in its
// head; the page as it is when card is ""; "" without the page.
func (h *McpHost) ViewerPage(id, card string) string {
	page := h.sitePage()
	if page == "" || card == "" {
		return page
	}
	var c cardInfo
	if err := json.Unmarshal([]byte(card), &c); err != nil {
		log.Printf("card %s: %v", id, err)
		return page
	}
	return withCard(page, strings.TrimRight(h.env.BaseURL, "/"), id, c)
}

// what LinkCard.json says of a deck, and of the slide a link names
type cardInfo struct {
	Title, Text, Stamp, Keys string
	Slide, Slides            int
	List                     string
}

func withCard(page, base, id string, c cardInfo) string {
	title := cardClip(c.Title, 120)
	if title == "" {
		title = "Presentation"
	}
	text := c.Text
	alt := "The first slide of " + title
	q := url.Values{}
	if c.Slide > 0 {
		// a link to one slide: its heading beside the deck's name, its
		// words for the description
		head, words := slideWords(c.List)
		if head != "" && head != title {
			title = cardClip(title+" · "+head, 120)
		}
		if words != "" {
			text = words
		}
		alt = fmt.Sprintf("Slide %d of %d of %s", c.Slide, c.Slides, c.Title)
		if c.Slide > 1 {
			q.Set("slide", fmt.Sprint(c.Slide))
		}
	}
	if c.Keys != "" {
		q.Set("slides", c.Keys)
	}
	text = cardClip(text, cardText)
	if text == "" {
		text = "A presentation made with Sliqtly."
	}
	esc := html.EscapeString
	link := base + "/s/" + id
	if len(q) > 0 {
		link += "?" + q.Encode()
	}
	pq := url.Values{"v": {c.Stamp}}
	for k, v := range q {
		pq[k] = v
	}
	pic := base + "/api/card/" + id + ".jpg?" + pq.Encode()
	var b strings.Builder
	b.WriteString("<title>" + esc(title) + " · Sliqtly</title>")
	head := []string{
		`<meta name="description" content="` + esc(text) + `" />`,
		`<meta property="og:type" content="website" />`,
		`<meta property="og:site_name" content="Sliqtly" />`,
		`<meta property="og:url" content="` + esc(link) + `" />`,
		`<meta property="og:title" content="` + esc(title) + `" />`,
		`<meta property="og:description" content="` + esc(text) + `" />`,
		`<meta property="og:image" content="` + esc(pic) + `" />`,
		`<meta property="og:image:type" content="image/jpeg" />`,
		`<meta property="og:image:alt" content="` + esc(alt) + `" />`,
		`<meta name="twitter:card" content="summary_large_image" />`,
	}
	t := pageTitle.ReplaceAllLiteralString(page, b.String())
	if t == page { // no <title>: the card goes in anyway
		head = append([]string{b.String()}, head...)
	}
	page = pageDesc.ReplaceAllLiteralString(t, "")
	return strings.Replace(page, "</head>", strings.Join(head, "\n")+"\n</head>", 1)
}

// a slide's words from its display list: the line in the largest type
// (its heading) and the rest in reading order, top to bottom, left to right
func slideWords(listJSON string) (string, string) {
	var doc dlDoc
	if listJSON == "" || json.Unmarshal([]byte(listJSON), &doc) != nil {
		return "", ""
	}
	type line struct {
		y, x, size float64
		text       string
	}
	var lines []line
	for _, c := range doc.Cmds {
		t := strings.TrimSpace(utf8Of(c.Text))
		// a list's marks are drawn as runs of their own
		if c.K != 3 || t == "" || c.Size <= 0 || strings.Trim(t, "•◦▪▸–-*·") == "" {
			continue
		}
		// runs on one baseline make one line
		joined := false
		for i := range lines {
			if math.Abs(lines[i].y-c.Y) < c.Size/3 && lines[i].size == c.Size {
				if c.X < lines[i].x {
					lines[i].text, lines[i].x = t+" "+lines[i].text, c.X
				} else {
					lines[i].text += " " + t
				}
				joined = true
				break
			}
		}
		if !joined {
			lines = append(lines, line{c.Y, c.X, c.Size, t})
		}
	}
	if len(lines) == 0 {
		return "", ""
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if math.Abs(lines[i].y-lines[j].y) > 1 {
			return lines[i].y < lines[j].y
		}
		return lines[i].x < lines[j].x
	})
	top := 0
	for i, l := range lines {
		if l.size > lines[top].size {
			top = i
		}
	}
	// a paragraph's wrapped lines are one text; blocks are set apart by " · "
	var rest strings.Builder
	var prev *line
	for i := range lines {
		if i == top {
			continue
		}
		l := &lines[i]
		if prev != nil {
			if l.size == prev.size && l.y-prev.y < 2*l.size {
				rest.WriteString(" ")
			} else {
				rest.WriteString(" · ")
			}
		}
		rest.WriteString(l.text)
		prev = l
	}
	return cardClip(lines[top].text, 120), rest.String()
}

// s as one line of at most n characters, cut at a word with "…"
func cardClip(s string, n int) string {
	s = strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)[:n-1]
	cut := string(r)
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ·,;:-") + "…"
}

func (h *McpHost) CardGet(key string) []byte {
	c := &h.env.cards
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pics[key]
}

// CardPut keeps a picture; the oldest go first
func (h *McpHost) CardPut(key string, jpg []byte) {
	c := &h.env.cards
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pics == nil {
		c.pics = map[string][]byte{}
	}
	if _, ok := c.pics[key]; !ok {
		c.order = append(c.order, key)
	}
	c.pics[key] = jpg
	for len(c.order) > cardPictures {
		delete(c.pics, c.order[0])
		c.order = c.order[1:]
	}
}

func (h *McpHost) CardSlot() bool { return h.env.renders.take("card") }
