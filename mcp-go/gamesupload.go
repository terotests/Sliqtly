// SPDX-License-Identifier: AGPL-3.0-or-later

// Big files without base64 through the assistant's context: upload_url gives
// a short-lived secret address, and the assistant's computer sends files to
// it straight (curl -T file <url>/<path>):
//
//	PUT /games/u/<token>/<path>   one file into the game, as write_files
//	                              would write it (same checks and limits)
//
// The token names one game and is kept in games_uploads with its end time.

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	gamesUploadCol  = "games_uploads"
	gamesUploadPath = gamesPath + "/u/"
	gamesUploadKeep = 30 * time.Minute
)

func (s *gamesService) uploadURL(ctx context.Context, g *gamesGame) (any, error) {
	token := gamesRandom(24)
	end := s.now().Add(gamesUploadKeep)
	if err := s.db.Set(ctx, gamesUploadCol, token, Doc{"game": g.id, "end": end.UnixMilli()}); err != nil {
		return nil, err
	}
	u := s.origin() + gamesUploadPath + token
	return map[string]any{
		"url":     u,
		"expires": end.UTC().Format(time.RFC3339),
		"how":     "PUT each file's bytes to url + \"/\" + its path in the game, one file a request, one after another (not in parallel), e.g. curl -sS -T map.jpg " + u + "/img/map.jpg . Same file types and limits as write_files (8 MB a file). Each answer is the game's JSON, or an error with status 400.",
	}, nil
}

func serveGamesUpload(env *Env, w http.ResponseWriter, r *http.Request, rest string) {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		http.Error(w, "PUT only", 405)
		return
	}
	token, path, _ := strings.Cut(rest, "/")
	out, err := env.games.upload(r.Context(), token, path, r.Body)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		var re roomErr
		if errors.As(err, &re) {
			w.WriteHeader(400)
		} else {
			w.WriteHeader(500)
		}
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(out)
}

func (s *gamesService) upload(ctx context.Context, token, path string, body io.Reader) (any, error) {
	if !gamesTokenRe.MatchString(token) {
		return nil, roomErr{"No such upload address (upload_url gives one)."}
	}
	d, err := s.db.Get(ctx, gamesUploadCol, token)
	if err != nil {
		return nil, err
	}
	if d == nil || docInt(d["end"]) < s.now().UnixMilli() {
		return nil, roomErr{"The upload address has ended; ask upload_url for a new one."}
	}
	data, err := io.ReadAll(io.LimitReader(body, gamesMaxFile+1))
	if err != nil {
		return nil, err
	}
	w, err := gamesWrites([]any{map[string]any{"path": path, "base64": base64.StdEncoding.EncodeToString(data)}})
	if err != nil {
		return nil, err
	}
	// another write may land in between: read the game again and retry
	for try := 0; ; try++ {
		gd, err := s.db.Get(ctx, gamesCol, docStr(d, "game"))
		if err != nil {
			return nil, err
		}
		if gd == nil {
			return nil, roomErr{"The game is gone."}
		}
		out, err := s.change(ctx, &gamesGame{docStr(d, "game"), gd}, w, nil)
		if err == nil || try >= 3 || !strings.Contains(err.Error(), "changed at the same time") {
			return out, err
		}
	}
}
