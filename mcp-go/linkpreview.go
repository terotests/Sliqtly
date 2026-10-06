// SPDX-License-Identifier: AGPL-3.0-or-later

// Link previews for the rooms' chat: after a message is posted the server
// reads the pages its links lead to (the first few, public addresses only:
// Env.Client) and keeps each one's site name, title and summary with the
// message (store.ChatLink); the pages are told of it as a changed message.
// A link that does not answer, or answers with no HTML, has no preview.
// Pictures in a preview (og:image) are not fetched.

package main

import (
	"context"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/terotests/sliqtly/mcp-go/store"
)

const (
	chatMaxPreviews = 3
	previewRead     = 512 << 10
	previewWait     = 6 * time.Second
)

var (
	linkInText  = regexp.MustCompile(`https?://[^\s<>"'\x60\[\]]+`)
	titleTag    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	metaTag     = regexp.MustCompile(`(?is)<meta\s[^>]*>`)
	attrPattern = regexp.MustCompile(`(?is)([a-z:_-]+)\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))`)
	spaces      = regexp.MustCompile(`\s+`)
)

// the links of a message's text to preview: http(s), outside code, not
// this server's own, each once, the first chatMaxPreviews
func previewLinks(text, own string) []string {
	var out []string
	seen := map[string]bool{}
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		// `code` spans are not links
		parts := strings.Split(line, "`")
		for i := 0; i < len(parts); i += 2 {
			for _, u := range linkInText.FindAllString(parts[i], -1) {
				u = strings.TrimRight(u, ".,;:!?)*_~")
				if own != "" && strings.HasPrefix(u, own) {
					continue
				}
				if pu, err := url.Parse(u); err != nil || pu.Host == "" {
					continue
				}
				if !seen[u] {
					seen[u] = true
					out = append(out, u)
					if len(out) == chatMaxPreviews {
						return out
					}
				}
			}
		}
	}
	return out
}

// what a page says of itself: og:site_name / og:title / og:description,
// else <title> and the description
func parsePreview(page string) (site, title, desc string) {
	metas := map[string]string{}
	for _, tag := range metaTag.FindAllString(page, -1) {
		var key, content string
		for _, m := range attrPattern.FindAllStringSubmatch(tag, -1) {
			v := m[3] + m[4] + m[5]
			switch strings.ToLower(m[1]) {
			case "property", "name":
				key = strings.ToLower(v)
			case "content":
				content = v
			}
		}
		if key != "" && content != "" {
			if _, had := metas[key]; !had {
				metas[key] = content
			}
		}
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v := metas[k]; v != "" {
				return v
			}
		}
		return ""
	}
	site = pick("og:site_name", "application-name")
	title = pick("og:title", "twitter:title")
	if title == "" {
		if m := titleTag.FindStringSubmatch(page); m != nil {
			title = m[1]
		}
	}
	desc = pick("og:description", "description", "twitter:description")
	return clip(site, 60), clip(title, 150), clip(desc, 300)
}

// text as shown: entities read, one line, at most n characters
func clip(s string, n int) string {
	s = strings.TrimSpace(spaces.ReplaceAllString(html.UnescapeString(s), " "))
	if utf8.RuneCountInString(s) > n {
		s = strings.TrimSpace(string([]rune(s)[:n-1])) + "…"
	}
	return s
}

// one page's preview; ok false when there is none to show
func fetchPreview(ctx context.Context, c *http.Client, link string) (store.ChatLink, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return store.ChatLink{}, false
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", "Sliqtly link preview")
	res, err := c.Do(req)
	if err != nil {
		return store.ChatLink{}, false
	}
	defer res.Body.Close()
	ct, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if res.StatusCode != 200 || (ct != "text/html" && ct != "application/xhtml+xml") {
		return store.ChatLink{}, false
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, previewRead))
	if err != nil && len(b) == 0 {
		return store.ChatLink{}, false
	}
	site, title, desc := parsePreview(string(b))
	if title == "" && desc == "" {
		return store.ChatLink{}, false
	}
	if site == "" {
		site = strings.TrimPrefix(res.Request.URL.Hostname(), "www.")
	}
	return store.ChatLink{URL: link, Site: site, Title: title, Desc: desc}, true
}

// previews reads the links of message id and keeps what they say with it,
// unless its text changed in the meantime
func (s *roomService) previews(tenant, room, id, text string) {
	if s.client == nil {
		return
	}
	links := previewLinks(text, s.ownURL)
	if len(links) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewWait)
	defer cancel()
	got := make([]store.ChatLink, len(links))
	ok := make([]bool, len(links))
	done := make(chan struct{}, len(links))
	for i, l := range links {
		go func(i int, l string) {
			got[i], ok[i] = fetchPreview(ctx, s.client, l)
			done <- struct{}{}
		}(i, l)
	}
	for range links {
		<-done
	}
	var keep []store.ChatLink
	for i := range links {
		if ok[i] {
			keep = append(keep, got[i])
		}
	}
	if len(keep) == 0 {
		return
	}
	m, err := s.chat.Change(context.Background(), tenant, room, id, func(x *store.ChatMsg) error {
		if x.Deleted || x.Text != text {
			return errStale
		}
		x.Links = keep
		return nil
	})
	if err != nil {
		return
	}
	s.tellMsg(context.Background(), tenant, room, m, false)
}

type staleErr struct{}

func (staleErr) Error() string { return "the message changed" }

var errStale = staleErr{}
