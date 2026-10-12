// SPDX-License-Identifier: AGPL-3.0-or-later

// The signed-in account's own shared files for the editor at /editor
// (cloudshared.go), as a server of its own's /api/shared and
// /files/shared/… (sharedfiles.go) are for its page:
//
//	GET    /editor/api/shared                    the account's files, the
//	                                             largest it may add and the
//	                                             folder it uploads to
//	POST   /editor/api/shared/add {name, upload[, id]}
//	                                             a file the page uploaded to
//	                                             Storage, moved in and read
//	POST   /editor/api/shared/{id}/rename {name}
//	DELETE /editor/api/shared/{id}
//	GET    /editor/api/shared/{id}/index.json
//	GET    /editor/api/shared/{id}/source      the file itself
//	GET    /editor/api/shared/{id}/png/{node}.png[?scale=][&max=][&deck=]
//
// A picture asked for with ?deck= is drawn from the files of that deck's
// owner, when the account may open the deck: the people invited to edit a
// deck see the Figma screens its owner put on it. Otherwise (a deck not
// kept yet, or not theirs to open) it is drawn from the account's own.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// calls per account in ten minutes: a Figma window opened draws a tile for
// each screen
var cloudSharedLimit = rateLimiter(3000, 10*time.Minute)

var cloudSharedPng = regexp.MustCompile(`^([a-z0-9][a-z0-9-]{0,47})/png/(.+)\.png$`)

func serveCloudShared(env *Env, g *editorGate, w http.ResponseWriter, r *http.Request, rest string) {
	cs := env.cloudShared
	if cs == nil {
		editorJSON(w, 404, map[string]string{"error": "No such call."})
		return
	}
	t := g.user(r)
	if t == nil {
		editorJSON(w, 401, map[string]string{"error": "Sign in again.", "code": "signed-out"})
		return
	}
	if r.Method != http.MethodGet && !sameSite(env, r) {
		editorJSON(w, 403, map[string]string{"error": "Not allowed."})
		return
	}
	if cloudSharedLimit(t.UID) != "" {
		editorJSON(w, 429, map[string]string{"error": "Too many requests; try again in a few minutes."})
		return
	}
	sh := cs.of(t.UID)
	if sh == nil {
		editorJSON(w, 400, map[string]string{"error": "This account cannot keep files."})
		return
	}
	ctx := r.Context()
	// the layer as the page escaped it: a name may hold a slash
	if m := cloudSharedPng.FindStringSubmatch(strings.TrimPrefix(r.URL.EscapedPath(), editorPath+"/api/shared/")); m != nil && r.Method == http.MethodGet {
		cloudSharedPicture(ctx, env, t, sh, w, r, m[1], m[2])
		return
	}
	id, op, _ := strings.Cut(rest, "/")
	var out any
	var err error
	status := 200
	switch {
	case rest == "" && r.Method == http.MethodGet:
		var files []sharedFile
		files, err = sh.list(ctx)
		out = map[string]any{"files": files, "max": sh.max, "upload": cloudUploadDir(t.UID)}
	case rest == "add" && r.Method == http.MethodPost:
		var in struct {
			Name   string `json:"name"`
			Upload string `json:"upload"`
			ID     string `json:"id"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in) != nil {
			editorJSON(w, 400, map[string]string{"error": "Send {name, upload}."})
			return
		}
		out, err = cs.adopt(ctx, t.UID, in.Name, in.ID, in.Upload)
		status = 201
	case op == "rename" && r.Method == http.MethodPost:
		var in struct {
			Name string `json:"name"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in) != nil {
			editorJSON(w, 400, map[string]string{"error": "Send {name}."})
			return
		}
		out, err = sh.rename(ctx, id, in.Name)
	case op == "" && r.Method == http.MethodDelete:
		err = sh.remove(ctx, id)
		out = map[string]bool{"ok": true}
	case op == "source" && r.Method == http.MethodGet:
		var f sharedFile
		var b []byte
		if f, b, err = sh.source(ctx, id); err == nil {
			sharedSendSource(w, f, b)
			return
		}
	case op == "index.json" && r.Method == http.MethodGet:
		var f sharedFile
		if f, err = sh.card(ctx, id); err == nil {
			var ix string
			if ix, err = sh.index(ctx, f); err == nil {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				io.WriteString(w, ix)
				return
			}
		}
	default:
		editorJSON(w, 404, map[string]string{"error": "No such call."})
		return
	}
	if err != nil {
		cloudSharedFail(w, err)
		return
	}
	editorJSON(w, status, out)
}

func cloudSharedFail(w http.ResponseWriter, err error) {
	var se sharedErr
	switch {
	case errors.As(err, &se):
		editorJSON(w, se.status, map[string]string{"error": se.msg})
	case errors.Is(err, store.ErrNotFound):
		editorJSON(w, 404, map[string]string{"error": "No such file."})
	default:
		log.Printf("editor shared: %v", err)
		editorJSON(w, 503, map[string]string{"error": "The files could not be reached; try again in a moment."})
	}
}

// a screen or a part drawn: from the account's own files, or from the
// files of the owner of the deck ?deck= names when the account may open it
func cloudSharedPicture(ctx context.Context, env *Env, t *IDToken, sh *sharedFiles, w http.ResponseWriter, r *http.Request, id, rawNode string) {
	node, err := url.PathUnescape(rawNode)
	if err != nil {
		editorJSON(w, 404, map[string]string{"error": "No such layer."})
		return
	}
	// a deck not kept yet, or not the account's to open: its own files
	if deck := r.URL.Query().Get("deck"); deck != "" {
		if owner, err := deckOwnerFor(ctx, env, deck, t); err == nil && owner != t.UID {
			sh = env.cloudShared.of(owner)
		}
	}
	q := r.URL.Query()
	scale, _ := strconv.ParseFloat(q.Get("scale"), 64)
	if scale <= 0 || scale > 4 {
		scale = 2
	}
	max, _ := strconv.Atoi(q.Get("max"))
	b, err := sh.draw(ctx, id, node, scale, max)
	if err != nil {
		cloudSharedFail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Write(b)
}

// the owner of deck id (shares/{id}) when account t may open it for
// editing: its owner or one it invites; else not found
func deckOwnerFor(ctx context.Context, env *Env, id string, t *IDToken) (string, error) {
	if !shareID.MatchString(id) || env.DB == nil {
		return "", store.ErrNotFound
	}
	d, err := env.DB.Get(ctx, "shares", id)
	if err != nil {
		return "", err
	}
	owner := fieldText(d, "owner")
	if d == nil || owner == "" || (owner != t.UID && !invitedTo(d, t)) {
		return "", store.ErrNotFound
	}
	return owner, nil
}
