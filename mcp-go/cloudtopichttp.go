// SPDX-License-Identifier: AGPL-3.0-or-later

// The topic's GitHub page and calls (cloudtopicgit.go):
//
//	GET  /editor/git/topic?room=<id>              the page
//	GET  /editor/api/git/topic?room=<id>          the topic's state
//	GET  /editor/api/git/topic/conflict?room=&branch=&deck=   a conflict's pieces
//	POST /editor/api/git/topic/<op>  {room, …}
//	     link     {installation, repo, dir, public?}  the repository (and folder) the topic is kept in
//	     public   {}                         the repository may be public (it is on GitHub)
//	     unlink   {}                         not kept there any more
//	     sync     {branch?}                  main (or the branch) synced now
//	     branch   {name}                     a new branch from main
//	     open     {branch, deck}             main's presentation's copy in the branch → {deck}
//	     update   {branch}                   main's changes merged into the branch
//	     merge    {branch}                   the branch into main
//	     drop     {branch}                   the branch given up
//	     resolve  {branch?, deck, take, md?} a conflict chosen: main|branch|text, or github|sliqtly on main

package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

//go:embed assets/editor-topic.html
var editorTopicHTML string

const topicPagePath = gitPagePath + "/topic"

func serveTopicPage(env *Env, w http.ResponseWriter, r *http.Request) {
	g := env.EditorGate
	if g.user(r) == nil {
		http.Redirect(w, r, editorPath+"/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
	io.WriteString(w, editorTopicHTML)
}

func (c *cloudGit) serveTopicAPI(t *IDToken, w http.ResponseWriter, r *http.Request, op string) {
	ctx := r.Context()
	var in struct {
		Room         string `json:"room"`
		Installation int64  `json:"installation"`
		Repo         int64  `json:"repo"`
		Dir          string `json:"dir"`
		Branch       string `json:"branch"`
		Name         string `json:"name"`
		Deck         string `json:"deck"`
		Take         string `json:"take"`
		MD           string `json:"md"`
		Public       bool   `json:"public"`
	}
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			editorJSON(w, 400, map[string]string{"error": "bad body"})
			return
		}
	} else {
		q := r.URL.Query()
		in.Room, in.Branch, in.Deck = q.Get("room"), q.Get("branch"), q.Get("deck")
	}
	if in.Room == "" || len(in.Room) > 100 || strings.ContainsAny(in.Room, "/~") {
		editorJSON(w, 400, map[string]string{"error": "which topic (room)?"})
		return
	}
	fail := func(err error) {
		code := 500
		var stop errGitStop
		var re roomErr
		switch {
		case errors.Is(err, errTopicNotLinked), errors.Is(err, errNoBranch):
			code = 404
		case errors.Is(err, errTopicBusy):
			code = 429
		case errors.As(err, &stop), errors.As(err, &re):
			code = 409
		}
		editorJSON(w, code, map[string]string{"error": err.Error()})
	}
	if r.Method == http.MethodGet {
		switch op {
		case "":
			st, err := c.topicState(ctx, t.UID, in.Room)
			if err != nil {
				fail(err)
				return
			}
			editorJSON(w, 200, st)
		case "conflict":
			var out []topicChunk
			err := c.withTopic(ctx, t.UID, in.Room, func(l *topicLink, gr *ghRepoAt) error {
				b := l.Branches[in.Branch]
				if b == nil {
					return errNoBranch
				}
				var err error
				out, err = c.conflictChunks(ctx, l, gr, b, in.Deck)
				return err
			})
			if err != nil {
				fail(err)
				return
			}
			editorJSON(w, 200, map[string]any{"chunks": out})
		default:
			editorJSON(w, 404, map[string]string{"error": "no such call"})
		}
		return
	}
	if r.Method != http.MethodPost {
		editorJSON(w, 405, map[string]string{"error": "GET or POST"})
		return
	}
	res := newTopicResult()
	var out any = res
	who := t.Name
	branch := func(l *topicLink) (*topicBranch, error) {
		b := l.Branches[in.Branch]
		if b == nil || !b.open() {
			return nil, errNoBranch
		}
		return b, nil
	}
	var err error
	switch op {
	case "link":
		res, err = c.linkTopic(ctx, t.UID, in.Room, in.Installation, in.Repo, in.Dir, in.Public)
		out = res
	case "public":
		// the repository, public on GitHub, may be: the syncs go on
		var l *topicLink
		if l, err = c.topicLink(ctx, t.UID, in.Room); err == nil {
			if !l.linked() {
				err = errTopicNotLinked
			} else {
				err = c.db.Update(ctx, topicLinkCol, l.id, Doc{"public": true, "error": ""})
			}
		}
		out = map[string]bool{"ok": err == nil}
	case "unlink":
		err = c.withTopic(ctx, t.UID, in.Room, func(l *topicLink, gr *ghRepoAt) error {
			for _, b := range l.Branches {
				if b.open() {
					return roomErr{"merge or drop the topic's branches first"}
				}
			}
			return nil
		})
		if err == nil {
			err = c.db.Delete(ctx, topicLinkCol, topicLinkID(t.UID, in.Room))
		}
		out = map[string]bool{"ok": err == nil}
	case "sync":
		err = c.withTopic(ctx, t.UID, in.Room, func(l *topicLink, gr *ghRepoAt) error {
			if in.Branch == "" {
				return c.syncMain(ctx, l, gr, "", who, res)
			}
			b, err := branch(l)
			if err != nil {
				return err
			}
			return c.syncBranch(ctx, l, gr, b, "", who, res)
		})
	case "branch":
		err = c.withTopic(ctx, t.UID, in.Room, func(l *topicLink, gr *ghRepoAt) error {
			_, err := c.newBranch(ctx, l, gr, in.Name, who, res)
			return err
		})
	case "open":
		var deck string
		err = c.withTopic(ctx, t.UID, in.Room, func(l *topicLink, gr *ghRepoAt) error {
			b, err := branch(l)
			if err != nil {
				return err
			}
			if err := c.syncMain(ctx, l, gr, "", who, res); err != nil {
				return err
			}
			main, err := c.db.Get(ctx, "shares", in.Deck)
			if err != nil {
				return err
			}
			if main == nil || main["owner"] != t.UID {
				return roomErr{"no such presentation"}
			}
			if _, ok := l.Slugs[in.Deck]; !ok {
				return roomErr{"the presentation is not on the topic's main"}
			}
			cp, err := c.topicCopy(ctx, l, b, in.Deck, main)
			deck = cp.Deck
			return err
		})
		out = map[string]string{"deck": deck}
	case "update", "merge", "drop":
		err = c.withTopic(ctx, t.UID, in.Room, func(l *topicLink, gr *ghRepoAt) error {
			b, err := branch(l)
			if err != nil {
				return err
			}
			switch op {
			case "update":
				return c.updateBranch(ctx, l, gr, b, who, res)
			case "merge":
				if b.State == "review" && b.PR != 0 {
					if merged, err := c.app.pullMerged(ctx, l, b.PR); err == nil && merged {
						return c.finishMerge(ctx, l, gr, b, who, res)
					}
				}
				return c.mergeBranch(ctx, l, gr, b, who, res)
			}
			return c.dropBranch(ctx, l, b)
		})
	case "resolve":
		err = c.withTopic(ctx, t.UID, in.Room, func(l *topicLink, gr *ghRepoAt) error {
			if in.Branch == "" {
				return c.resolveMain(ctx, l, in.Deck, in.Take)
			}
			b, err := branch(l)
			if err != nil {
				return err
			}
			return c.resolveBranch(ctx, l, b, in.Deck, in.MD, in.Take)
		})
		out = map[string]bool{"ok": err == nil}
	default:
		editorJSON(w, 404, map[string]string{"error": "no such call"})
		return
	}
	if err != nil {
		fail(err)
		return
	}
	editorJSON(w, 200, out)
}
