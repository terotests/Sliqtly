// SPDX-License-Identifier: AGPL-3.0-or-later

// A deck's history from the account's GitHub repository (cloudgit.go,
// phase G3): the editor's Version history lists the commits beside the
// browser's own versions, as on a server of one's own (githistory.go), with
// the same answers under /editor/api/history/:
//
//	GET /editor/api/history/{deck}                    {commits: [...]}
//	GET /editor/api/history/{deck}/{rev}              the deck as rev has it
//	GET /editor/api/history/{deck}/{rev}/changes      {changes: [{path, kind}]}
//	GET /editor/api/history/{deck}/{rev}/file/{path}  a file's bytes
//
// The deck's owner's repository answers, to the owner and to those invited
// to edit it.

package main

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var cloudHistoryPath = regexp.MustCompile(`^history/([A-Za-z0-9]{6,32})(?:/([A-Za-z0-9._-]{1,64})(/changes|/file/.+)?)?$`)

func ghCommitRow(c ghCommit) deckCommit {
	subject, _, _ := strings.Cut(c.Message, "\n")
	row := deckCommit{ID: c.SHA, Subject: subject, Message: strings.TrimRight(c.Message, "\n"), Author: c.Author.Name, When: c.Committer.Date.UnixMilli()}
	if len(c.Parents) > 0 {
		row.Parent = c.Parents[0].SHA
	}
	return row
}

// deckIn: the deck's files in the commit, path (in the deck) → blob; nil
// when the deck is not in it
func deckIn(ctx context.Context, r *ghRepoAt, c ghCommit, deck string) (map[string]string, error) {
	t, err := r.subtree(ctx, c.Tree.SHA, "decks/"+deck)
	if err != nil || t == "" {
		return nil, err
	}
	all, err := r.files(ctx, t)
	if err != nil {
		return nil, err
	}
	return all, nil
}

func (c *cloudGit) serveHistory(g *editorGate, w http.ResponseWriter, r *http.Request, op string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		editorJSON(w, 405, map[string]string{"error": "GET only"})
		return
	}
	t := g.user(r)
	if t == nil {
		editorJSON(w, 401, map[string]string{"error": "Sign in again.", "code": "signed-out"})
		return
	}
	mt := cloudHistoryPath.FindStringSubmatch(op)
	if mt == nil {
		editorJSON(w, 404, map[string]string{"error": "no such call"})
		return
	}
	deck, rev, rest := mt[1], mt[2], mt[3]
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	d, err := c.db.Get(ctx, "shares", deck)
	if err != nil || d == nil {
		editorJSON(w, 404, map[string]string{"error": "no such presentation"})
		return
	}
	owner, _ := d["owner"].(string)
	if owner != t.UID && !invitedTo(d, t) {
		editorJSON(w, 404, map[string]string{"error": "no such presentation"})
		return
	}
	l, _, err := c.link(ctx, owner)
	if err != nil {
		editorJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if !l.connected() {
		editorJSON(w, 404, map[string]any{"error": "no GitHub repository is connected", "git": false})
		return
	}
	repo, err := c.app.at(ctx, l.Installation, l.RepoID, l.Repo)
	if err != nil {
		editorJSON(w, 502, map[string]string{"error": gitAccessErr(err, l).Error()})
		return
	}
	branch := nonEmpty(l.Branch, "main")
	if rev == "" {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		if n <= 0 || n > 100 {
			n = 50
		}
		rows, err := repo.log(ctx, branch, "decks/"+deck, n)
		if err != nil {
			editorJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		out := []deckCommit{}
		for _, x := range rows {
			subject, _, _ := strings.Cut(x.Commit.Message, "\n")
			row := deckCommit{ID: x.SHA, Subject: subject, Message: strings.TrimRight(x.Commit.Message, "\n"), Author: x.Commit.Author.Name, When: x.Commit.Committer.Date.UnixMilli()}
			if len(x.Parents) > 0 {
				row.Parent = x.Parents[0].SHA
			}
			out = append(out, row)
		}
		editorJSON(w, 200, map[string]any{"commits": out})
		return
	}
	sha := rev
	if !hexRev.MatchString(rev) || len(rev) != 40 {
		if branchRev.MatchString(rev) && !hexRev.MatchString(rev) {
			if sha, err = repo.head(ctx, rev); err != nil || sha == "" {
				editorJSON(w, 404, map[string]string{"error": "no version " + rev})
				return
			}
		} else {
			editorJSON(w, 404, map[string]string{"error": "give the whole commit id"})
			return
		}
	}
	cm, err := repo.commit(ctx, sha)
	if err != nil {
		code := 502
		if s := ghStatus(err); s == 404 || s == 422 {
			code = 404
		}
		editorJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	files, err := deckIn(ctx, repo, cm, deck)
	if err != nil {
		editorJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	if files == nil {
		editorJSON(w, 404, map[string]string{"error": "the presentation is not in version " + short7(cm.SHA)})
		return
	}
	switch {
	case rest == "":
		v := deckVersion{deckCommit: ghCommitRow(cm), Files: []deckFileRow{}}
		var meta struct {
			Name  string `json:"name"`
			Theme string `json:"theme"`
			Files []struct {
				Path string `json:"path"`
				Type string `json:"type"`
				Size int64  `json:"size"`
			} `json:"files"`
		}
		for p, id := range files {
			if !deckMeta[p] {
				continue
			}
			b, err := repo.blob(ctx, id)
			if err != nil {
				editorJSON(w, 502, map[string]string{"error": err.Error()})
				return
			}
			switch p {
			case "deck.md":
				v.MD = string(b)
			case "theme.css":
				css := string(b)
				v.CSS = &css
			case "deck.json":
				json.Unmarshal(b, &meta)
			}
		}
		v.Name, v.Theme = meta.Name, meta.Theme
		types, sizes := map[string]string{}, map[string]int64{}
		for _, f := range meta.Files {
			types[f.Path], sizes[f.Path] = f.Type, f.Size
		}
		for p := range files {
			if deckMeta[p] {
				continue
			}
			typ := types[p]
			if typ == "" {
				typ = mime.TypeByExtension(path.Ext(p))
			}
			v.Files = append(v.Files, deckFileRow{Path: p, Type: typ, Size: sizes[p]})
		}
		sort.Slice(v.Files, func(i, j int) bool { return v.Files[i].Path < v.Files[j].Path })
		if hexRev.MatchString(rev) {
			w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		}
		editorJSON(w, 200, v)
	case rest == "/changes":
		was := map[string]string{}
		if len(cm.Parents) > 0 {
			pc, err := repo.commit(ctx, cm.Parents[0].SHA)
			if err != nil {
				editorJSON(w, 502, map[string]string{"error": err.Error()})
				return
			}
			if old, err := deckIn(ctx, repo, pc, deck); err != nil {
				editorJSON(w, 502, map[string]string{"error": err.Error()})
				return
			} else if old != nil {
				was = old
			}
		}
		out := []deckChange{}
		for p, id := range files {
			if p == "deck.json" {
				continue
			}
			switch old, ok := was[p]; {
			case !ok:
				out = append(out, deckChange{Path: p, Kind: "added", Text: historyText(p)})
			case old != id:
				out = append(out, deckChange{Path: p, Kind: "changed", Text: historyText(p)})
			}
		}
		for p := range was {
			if _, ok := files[p]; !ok && p != "deck.json" {
				out = append(out, deckChange{Path: p, Kind: "removed", Text: historyText(p)})
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
		editorJSON(w, 200, map[string]any{"changes": out})
	default:
		p := strings.TrimPrefix(rest, "/file/")
		id, ok := files[p]
		if !ok || p == "deck.json" {
			editorJSON(w, 404, map[string]string{"error": "no such file"})
			return
		}
		b, err := repo.blob(ctx, id)
		if err != nil {
			editorJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		typ := mime.TypeByExtension(path.Ext(p))
		switch {
		case p == "deck.md":
			typ = "text/markdown; charset=utf-8"
		case p == "theme.css":
			typ = "text/css; charset=utf-8"
		case typ == "":
			typ = "application/octet-stream"
		}
		w.Header().Set("Content-Type", typ)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "sandbox")
		w.Header().Set("ETag", `"`+id+`"`)
		w.Write(b)
	}
}
