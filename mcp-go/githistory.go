// SPDX-License-Identifier: AGPL-3.0-or-later

// A deck's history from the server's Git repository (git-storage DESIGN.md
// §5, phase 3): the commits that changed decks/<id>/, the deck as one of
// them has it, and what it changed. The editor's Version history lists
// them beside the browser's own versions, and /s/{id}@{version} shows the
// deck as that version had it (a link that stays on what it pointed at).
//
//	GET /api/history/{deck}                      {commits: [{id, subject, message, author, when, parent}]}
//	GET /api/history/{deck}/{rev}                {id, when, author, message, name, md, css, theme, files: [{path, type, size}]}
//	GET /api/history/{deck}/{rev}/changes        [{path, kind}] against the commit before
//	GET /api/history/{deck}/{rev}/file/{path}    a file's bytes
//
// rev is a commit (7–40 hex) or a branch ("main"). A deck is answered as
// /api/shares answers it; one deleted since is not.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/terotests/sliqtly/mcp-go/rgit"
)

var (
	historyPath = regexp.MustCompile(`^/api/history/([A-Za-z0-9]{6,32})(?:/([A-Za-z0-9._-]{1,64})(/changes|/file/.+)?)?$`)
	hexRev      = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	branchRev   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// how far back along the branch a deck's commits and a short id are looked for
const historyScan = 5000

// the files of a deck's directory that are not its own files
var deckMeta = map[string]bool{"deck.md": true, "deck.json": true, "theme.css": true}

type deckCommit struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Message string `json:"message"`
	Author  string `json:"author"`
	When    int64  `json:"when"`
	Parent  string `json:"parent,omitempty"`
}

func commitRow(c rgit.Commit) deckCommit {
	subject, _, _ := strings.Cut(c.Message, "\n")
	row := deckCommit{ID: c.ID, Subject: subject, Message: strings.TrimRight(c.Message, "\n"), Author: c.Author.Name, When: c.Committer.When.UnixMilli()}
	if len(c.Parents) > 0 {
		row.Parent = c.Parents[0]
	}
	return row
}

// deckTree is the deck's directory in a commit, "" when it has none
func (m *gitMirror) deckTree(c rgit.Commit, deck string) (string, error) {
	return m.repo.TreeAt(c.Tree, "decks/"+deck)
}

// deckLog: up to n commits that changed the deck, newest first, along the
// branch's first parents
func (m *gitMirror) deckLog(deck string, n int) ([]deckCommit, error) {
	head, err := m.Head()
	if err != nil || head == "" {
		return nil, err
	}
	var out []deckCommit
	at := head
	c, err := m.repo.ReadCommit(at)
	if err != nil {
		return nil, err
	}
	tree, err := m.deckTree(c, deck)
	if err != nil {
		return nil, err
	}
	for scanned := 0; scanned < historyScan && len(out) < n; scanned++ {
		var parent rgit.Commit
		before := ""
		if len(c.Parents) > 0 {
			if parent, err = m.repo.ReadCommit(c.Parents[0]); err != nil {
				return out, err
			}
			if before, err = m.deckTree(parent, deck); err != nil {
				return out, err
			}
		}
		if tree != before && tree != "" {
			out = append(out, commitRow(c))
		}
		if len(c.Parents) == 0 {
			break
		}
		c, tree = parent, before
	}
	return out, nil
}

// resolve: a commit id (short or whole) or a branch → the commit
func (m *gitMirror) resolve(rev string) (rgit.Commit, error) {
	switch {
	case hexRev.MatchString(rev) && len(rev) == 40:
		return m.repo.ReadCommit(rev)
	case hexRev.MatchString(rev):
		head, err := m.Head()
		if err != nil {
			return rgit.Commit{}, err
		}
		cs, err := m.repo.Log(head, historyScan)
		if err != nil {
			return rgit.Commit{}, err
		}
		var found []rgit.Commit
		for _, c := range cs {
			if strings.HasPrefix(c.ID, rev) {
				found = append(found, c)
			}
		}
		if len(found) == 1 {
			return found[0], nil
		}
		if len(found) > 1 {
			return rgit.Commit{}, fmt.Errorf("%s names %d commits: give more of it", rev, len(found))
		}
	case branchRev.MatchString(rev) && !strings.Contains(rev, ".."):
		id, err := m.repo.Ref("refs/heads/" + rev)
		if err != nil {
			return rgit.Commit{}, err
		}
		if id != "" {
			return m.repo.ReadCommit(id)
		}
	}
	return rgit.Commit{}, fmt.Errorf("%w: no version %s", rgit.ErrNotFound, rev)
}

type deckFileRow struct {
	Path string `json:"path"`
	Type string `json:"type,omitempty"`
	Size int64  `json:"size,omitempty"`
}

type deckVersion struct {
	deckCommit
	Name  string        `json:"name"`
	MD    string        `json:"md"`
	CSS   *string       `json:"css"`
	Theme string        `json:"theme"`
	Files []deckFileRow `json:"files"`
	ids   map[string]string
}

// deckAt is the deck as commit c has it; nil when the deck is not in it
func (m *gitMirror) deckAt(c rgit.Commit, deck string) (*deckVersion, error) {
	tree, err := m.deckTree(c, deck)
	if err != nil || tree == "" {
		return nil, err
	}
	entries, err := m.repo.ReadTree(tree)
	if err != nil {
		return nil, err
	}
	v := &deckVersion{deckCommit: commitRow(c), ids: map[string]string{}, Files: []deckFileRow{}}
	var meta struct {
		Name  string `json:"name"`
		Theme string `json:"theme"`
		Files []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"files"`
	}
	types := map[string]string{}
	for _, e := range entries {
		v.ids[e.Path] = e.ID
		if !deckMeta[e.Path] {
			continue
		}
		_, b, err := m.repo.Get(e.ID)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Path, err)
		}
		switch e.Path {
		case "deck.md":
			v.MD = string(b)
		case "theme.css":
			css := string(b)
			v.CSS = &css
		case "deck.json":
			if err := json.Unmarshal(b, &meta); err != nil {
				return nil, fmt.Errorf("deck.json: %w", err)
			}
		}
	}
	v.Name, v.Theme = meta.Name, meta.Theme
	sizes := map[string]int64{}
	for _, f := range meta.Files {
		types[f.Path], sizes[f.Path] = f.Type, f.Size
	}
	for _, e := range entries {
		if deckMeta[e.Path] {
			continue
		}
		typ := types[e.Path]
		if typ == "" {
			typ = mime.TypeByExtension(path.Ext(e.Path))
		}
		v.Files = append(v.Files, deckFileRow{Path: e.Path, Type: typ, Size: sizes[e.Path]})
	}
	sort.Slice(v.Files, func(i, j int) bool { return v.Files[i].Path < v.Files[j].Path })
	return v, nil
}

type deckChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"` // added, removed, changed
	Text bool   `json:"text,omitempty"`
}

// deckChanges: what commit c changed in the deck against its first parent
func (m *gitMirror) deckChanges(c rgit.Commit, deck string) ([]deckChange, error) {
	now, err := m.deckAt(c, deck)
	if err != nil {
		return nil, err
	}
	was := map[string]string{}
	if len(c.Parents) > 0 {
		p, err := m.repo.ReadCommit(c.Parents[0])
		if err != nil {
			return nil, err
		}
		if old, err := m.deckAt(p, deck); err != nil {
			return nil, err
		} else if old != nil {
			was = old.ids
		}
	}
	have := map[string]string{}
	if now != nil {
		have = now.ids
	}
	var out []deckChange
	for p, id := range have {
		if p == "deck.json" {
			continue
		}
		switch old, ok := was[p]; {
		case !ok:
			out = append(out, deckChange{Path: p, Kind: "added"})
		case old != id:
			out = append(out, deckChange{Path: p, Kind: "changed"})
		}
	}
	for p := range was {
		if _, ok := have[p]; !ok && p != "deck.json" {
			out = append(out, deckChange{Path: p, Kind: "removed"})
		}
	}
	for i := range out {
		out[i].Text = historyText(out[i].Path)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func historyText(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".markdown", ".txt", ".css", ".json", ".csv", ".tsv", ".svg", ".html", ".xml", ".yaml", ".yml":
		return true
	}
	return false
}

// historyAPI answers /api/history/… (see the top of the file)
func (s *localServer) historyAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSON(w, 405, map[string]string{"error": "GET only"})
		return
	}
	mt := historyPath.FindStringSubmatch(r.URL.Path)
	if mt == nil {
		writeJSON(w, 404, map[string]string{"error": "no such call"})
		return
	}
	deck, rev, rest := mt[1], mt[2], mt[3]
	m := s.gits.mirror()
	if m == nil {
		writeJSON(w, 404, map[string]any{"error": "this server does not keep its decks in Git", "git": false})
		return
	}
	// the deck as /api/shares answers it (getShare): one deleted since has
	// no history here either
	if d, err := s.env.DB.Get(r.Context(), "shares", deck); err != nil || d == nil {
		writeJSON(w, 404, map[string]string{"error": "no such presentation"})
		return
	}
	if rev == "" {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		if n <= 0 || n > 200 {
			n = 50
		}
		cs, err := m.deckLog(deck, n)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		if cs == nil {
			cs = []deckCommit{}
		}
		writeJSON(w, 200, map[string]any{"commits": cs})
		return
	}
	c, err := m.resolve(rev)
	if err != nil {
		code := 500
		if errors.Is(err, rgit.ErrNotFound) || strings.Contains(err.Error(), "names") {
			code = 404
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	switch {
	case rest == "":
		v, err := m.deckAt(c, deck)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		if v == nil {
			writeJSON(w, 404, map[string]string{"error": "the presentation is not in version " + short7(c.ID)})
			return
		}
		// a commit's content never changes; a branch's does
		if hexRev.MatchString(rev) {
			w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-store")
		}
		writeJSON(w, 200, v)
	case rest == "/changes":
		ch, err := m.deckChanges(c, deck)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		if ch == nil {
			ch = []deckChange{}
		}
		writeJSON(w, 200, map[string]any{"changes": ch})
	default:
		p := strings.TrimPrefix(rest, "/file/")
		v, err := m.deckAt(c, deck)
		if err != nil || v == nil {
			writeJSON(w, 404, map[string]string{"error": "no such file"})
			return
		}
		id, ok := v.ids[p]
		if !ok || p == "deck.json" {
			writeJSON(w, 404, map[string]string{"error": "no such file"})
			return
		}
		_, b, err := m.repo.Get(id)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		typ := "text/plain; charset=utf-8"
		for _, f := range v.Files {
			if f.Path == p && f.Type != "" {
				typ = f.Type
			}
		}
		if p == "deck.md" {
			typ = "text/markdown; charset=utf-8"
		}
		if p == "theme.css" {
			typ = "text/css; charset=utf-8"
		}
		// what a deck's file holds is shown, never run as a page of this origin
		w.Header().Set("Content-Type", typ)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "sandbox")
		w.Header().Set("ETag", `"`+id+`"`)
		w.Header().Set("Last-Modified", time.UnixMilli(v.When).UTC().Format(http.TimeFormat))
		w.Write(b)
	}
}
