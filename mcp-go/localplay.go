// SPDX-License-Identifier: AGPL-3.0-or-later

// The light player and the video on a server of one's own: what sliqtly.com
// has Export ▸ Cloud HTML and Cloud video for. The editor keeps the
// recording (or the video) with the deck as recordings/web-<hash>.sliqrec
// (.webm), asks for a viewing link of that one file, and hands out
// /r/{link}/{hash} at the server's own address (SLIQTLY_URL):
//
//	POST   /api/shares/{id}/links         { days, file } → { id }
//	DELETE /api/shares/{id}/links?file=…  that file's links
//	GET    /r/{link}/{hash}               the player's page (web/play.html)
//	GET    /api/view/{link}/rec/{hash}    the recording (rgr/View.rgr)
//	GET    /api/published                 the recordings and their links
//	                                      (File → Shared links…)
//
// A link is a document of links/ as on sliqtly.com ({of, owner, since,
// days, file}, Store.shownAny): it works `days` (1 to 30) from when it was
// made and shows that file only, never the deck, whose id would open it
// for editing on a server without sign-in. Its id is 128 random bits.

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"net/http"
	"regexp"
	"time"
)

var (
	playPath    = regexp.MustCompile(`^/r/([A-Za-z0-9]{6,32})/([0-9a-f]{24})/?$`)
	recFilePath = regexp.MustCompile(`^recordings/web-[0-9a-f]{24}\.(sliqrec|webm)$`)
)

// the most days a link works (Limits.LINK_DAYS in rgr/Store.rgr)
const linkDaysMax = 30

// a viewing link's id: 32 hex digits, a GUID's worth of chance
func newLinkID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func linkDaysOf(v any) int64 {
	var n int64
	switch x := v.(type) {
	case int64:
		n = x
	case float64:
		n = int64(x)
	case int:
		n = int64(x)
	}
	if n < 1 {
		return 7
	}
	if n > linkDaysMax {
		return linkDaysMax
	}
	return n
}

// whether a link stopped working at `now` (ms), as Store.linkUntil reads it
func linkGone(d Doc, now int64) bool {
	since, days := millis(d["since"]), linkDaysOf(d["days"])
	if since <= 0 {
		return true
	}
	return now >= since+days*86400000
}

// the share's viewing links (links/ documents with `of`; the rooms' links
// to things, store/links.go, have none)
func (s *localServer) linksOf(ctx context.Context, id string) ([]Doc, []string, error) {
	return s.env.DB.WhereEq(ctx, "links", "of", id)
}

func (s *localServer) linksAPI(r *http.Request, id string) (any, error) {
	ctx := r.Context()
	switch r.Method {
	case http.MethodPost:
		body, err := readBody(r)
		if err != nil {
			return nil, err
		}
		file, _ := body["file"].(string)
		if !recFilePath.MatchString(file) {
			return nil, fail(400, "", "a link is made to one recording, recordings/web-<hash>.sliqrec or .webm")
		}
		cur, err := s.own(ctx, id)
		if err != nil {
			return nil, err
		}
		if !hasFile(cur, file) {
			return nil, fail(404, "not-found", "the presentation has no such recording")
		}
		return s.viewLink(ctx, id, file, linkDaysOf(body["days"]))
	case http.MethodDelete:
		if _, err := s.own(ctx, id); err != nil {
			return nil, err
		}
		file := r.URL.Query().Get("file")
		docs, ids, err := s.linksOf(ctx, id)
		if err != nil {
			return nil, err
		}
		for i, d := range docs {
			if f, _ := d["file"].(string); f == file {
				if err := s.env.DB.Delete(ctx, "links", ids[i]); err != nil {
					return nil, err
				}
			}
		}
		return map[string]bool{"ok": true}, nil
	}
	return nil, fail(405, "", "method not allowed")
}

func hasFile(share Doc, path string) bool {
	files, _ := share["files"].([]any)
	for _, f := range files {
		if m, ok := f.(map[string]any); ok && m["path"] == path {
			return true
		}
	}
	return false
}

// the file's link, its term from now; one past its term is not given out
// again (web/sliqtly.js viewLink) → { id }
func (s *localServer) viewLink(ctx context.Context, id, file string, days int64) (any, error) {
	now := time.Now().UnixMilli()
	term := Doc{"since": now, "days": days}
	docs, ids, err := s.linksOf(ctx, id)
	if err != nil {
		return nil, err
	}
	for i, d := range docs {
		if f, _ := d["file"].(string); f != file || d["owner"] != s.env.LocalUser {
			continue
		}
		if linkGone(d, now) {
			if err := s.env.DB.Delete(ctx, "links", ids[i]); err != nil {
				return nil, err
			}
			continue
		}
		if err := s.env.DB.Update(ctx, "links", ids[i], term); err != nil {
			return nil, err
		}
		return map[string]string{"id": ids[i]}, nil
	}
	for range 5 {
		link := newLinkID()
		// a share's id is never a link's (Store.shownAny reads shares first)
		if had, err := s.env.DB.Get(ctx, "shares", link); err != nil || had != nil {
			continue
		}
		d := Doc{"of": id, "owner": s.env.LocalUser, "created": now, "file": file}
		for k, v := range term {
			d[k] = v
		}
		had, err := s.env.DB.Create(ctx, "links", link, d)
		if err != nil {
			return nil, err
		}
		if had == nil {
			return map[string]string{"id": link}, nil
		}
	}
	return nil, fail(500, "", "no free link id")
}

// GET /r/{link}/{hash}: the light player's page, which reads the recording
// from /api/view/{link}/rec/{hash}; it shows nothing of a link that does
// not work, so the page itself is the same for every address
func (s *localServer) playPage(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(s.web, "play.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Write(b)
}

// the deck's viewing links go with it
func (s *localServer) dropLinks(ctx context.Context, id string) error {
	_, ids, err := s.linksOf(ctx, id)
	if err != nil {
		return err
	}
	for _, l := range ids {
		if err := s.env.DB.Delete(ctx, "links", l); err != nil {
			return err
		}
	}
	return nil
}

// GET /api/published: what this server hands out, for the editor's File →
// Shared links… (web/published.js publishedRows, the same shape as
// web/sliqtly.js listPublished): the user's decks that keep a recording,
// each with its recordings, and the viewing links made of them. A deck
// here is never open at its own address (its id would open it for
// editing), so each is "private". A recording's `at` is when its first
// link was made (its name is its content's hash: the same recording
// again keeps its name and its link), 0 for none.
func (s *localServer) published(ctx context.Context) (any, error) {
	type file struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
		At   int64  `json:"at"`
	}
	type share struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Visibility string `json:"visibility"`
		Files      []file `json:"files"`
	}
	type link struct {
		ID      string `json:"id"`
		Of      string `json:"of"`
		Created int64  `json:"created"`
		Since   int64  `json:"since"`
		Days    int64  `json:"days"`
		File    string `json:"file"`
	}
	ldocs, lids, err := s.env.DB.WhereEq(ctx, "links", "owner", s.env.LocalUser)
	if err != nil {
		return nil, err
	}
	links := []link{}
	first := map[string]int64{}
	for i, d := range ldocs {
		of, _ := d["of"].(string)
		if of == "" {
			continue
		}
		f, _ := d["file"].(string)
		l := link{lids[i], of, millis(d["created"]), millis(d["since"]), linkDaysOf(d["days"]), f}
		links = append(links, l)
		if k := of + ":" + f; first[k] == 0 || (l.Created > 0 && l.Created < first[k]) {
			first[k] = l.Created
		}
	}
	sdocs, sids, err := s.env.DB.WhereEq(ctx, "shares", "owner", s.env.LocalUser)
	if err != nil {
		return nil, err
	}
	shares := []share{}
	for i, d := range sdocs {
		kept := []file{}
		files, _ := d["files"].([]any)
		for _, f := range files {
			m, _ := f.(map[string]any)
			p, _ := m["path"].(string)
			if !recFilePath.MatchString(p) {
				continue
			}
			kept = append(kept, file{p, millis(m["size"]), first[sids[i]+":"+p]})
		}
		if len(kept) == 0 {
			continue
		}
		name, _ := d["name"].(string)
		shares = append(shares, share{sids[i], name, "private", kept})
	}
	return map[string]any{"shares": shares, "links": links}, nil
}
