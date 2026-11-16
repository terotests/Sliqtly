// SPDX-License-Identifier: AGPL-3.0-or-later

// sliqtly.com/games/: Games' own page. Signed out it is the sign-in page;
// signed in it lists the account's games with their links, and a game can
// be published or unpublished, hidden (neither link plays it) or deleted.
// It also tells how to connect the Games MCP to an assistant. Its calls are
// /games/api/<op>, POST from the same site with the editor's cookie.

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
)

//go:embed assets/games-home.html
var gamesHomeHTML string

var gamesHomeLoginHTML = strings.NewReplacer(
	"<title>Sliqtly editor</title>", "<title>Sliqtly Games</title>",
	">Sign in to the editor<", ">Sign in to Sliqtly Games<",
	"The Sliqtly editor is for signed-in users. Sign in with your Google account: a Trial license lets you edit two presentations in the cloud.",
	"Sliqtly Games: games for children's phones, made with an AI assistant through MCP. Sign in with your Google account to see your games.",
	`"Kirjaudu editoriin"`, `"Kirjaudu Sliqtly Gamesiin"`,
	"Sliqtlyn editori on kirjautuneille käyttäjille. Kirjaudu Google-tililläsi: Trial-lisenssillä voit muokata kahta esitystä pilvessä.",
	"Sliqtly Games: lasten puhelinpelejä, jotka tehdään tekoälyavustajalla MCP:n kautta. Kirjaudu Google-tililläsi nähdäksesi omat pelisi.",
).Replace(editorLoginHTML)

func serveGamesHome(env *Env, w http.ResponseWriter, r *http.Request) {
	g := env.EditorGate
	if g == nil {
		http.NotFound(w, r)
		return
	}
	t := g.user(r)
	if r.URL.Path == gamesPath || r.URL.Path == gamesPath+"/" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "GET only", 405)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if t == nil {
			io.WriteString(w, gamesHomeLoginHTML)
			return
		}
		io.WriteString(w, gamesHomeHTML)
		return
	}
	op := strings.TrimPrefix(r.URL.Path, gamesPath+"/api/")
	if r.Method != http.MethodPost || !sameSite(env, r) {
		editorJSON(w, 403, map[string]string{"error": "Not allowed."})
		return
	}
	if t == nil {
		editorJSON(w, 401, map[string]string{"error": "Sign in again."})
		return
	}
	if why := gamesLimit(t.UID); why != "" {
		editorJSON(w, 429, map[string]string{"error": "Too many changes in a short time; try again in a few minutes."})
		return
	}
	a := map[string]any{}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&a); err != nil && !errors.Is(err, io.EOF) {
		editorJSON(w, 400, map[string]string{"error": "A JSON object is expected."})
		return
	}
	who := gamesWho{uid: t.UID, name: t.Name, email: gamesVerifiedEmail(t)}
	out, err := env.games.homeCall(r.Context(), who, op, a)
	var re roomErr
	switch {
	case errors.As(err, &re):
		editorJSON(w, 400, map[string]string{"error": re.msg})
	case err != nil:
		log.Printf("games home %s: %v", op, err)
		editorJSON(w, 500, map[string]string{"error": "Games could not be reached; try again in a moment."})
	default:
		editorJSON(w, 200, out)
	}
}

// the page's calls; "me" works for anyone signed in, the rest for the
// invited, on their own games
func (s *gamesService) homeCall(ctx context.Context, who gamesWho, op string, a map[string]any) (any, error) {
	if op == "me" {
		out := map[string]any{"email": who.email, "name": who.name, "mcp": s.base + gamesMcpPath}
		u, err := s.user(ctx, who)
		var re roomErr
		if errors.As(err, &re) {
			out["invited"] = false
			out["why"] = re.msg
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		list, err := s.listGames(ctx, who)
		if err != nil {
			return nil, err
		}
		out["invited"] = true
		out["admin"] = u.admin
		out["max_games"] = u.maxGames
		out["tts_month"] = u.ttsMonth
		out["games"] = list.(map[string]any)["games"]
		if d, err := s.db.Get(ctx, gamesTTSCol, who.uid); err == nil && d != nil {
			out["tts_used"] = docInt(d[gamesMonth(s.now())])
		}
		return out, nil
	}
	if _, err := s.user(ctx, who); err != nil {
		return nil, err
	}
	id, _ := a["game_id"].(string)
	g, err := s.game(ctx, who, id)
	if err != nil {
		return nil, err
	}
	switch op {
	case "publish":
		return s.publish(ctx, g, a)
	case "hide":
		on, _ := a["hidden"].(bool)
		if err := s.save(ctx, g, Doc{"hidden": on}); err != nil {
			return nil, err
		}
		return s.json(g, false), nil
	case "delete":
		return s.deleteGame(ctx, g, a)
	}
	return nil, roomErr{"No such call."}
}
