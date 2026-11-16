// SPDX-License-Identifier: AGPL-3.0-or-later

// Games over HTTP (games.go has the operations):
//
//	POST /games/mcp                     the Games MCP server: an assistant
//	                                    connects to it as its own connector,
//	                                    signed in with the site's OAuth
//	                                    (Google), for this resource
//	GET  /.well-known/oauth-protected-resource/games/mcp   RFC 9728
//	GET  /games/p/<token>               a game's play link: sends the
//	                                    browser to /games/engine/?pack=
//	                                    (anyone with the link)
//	GET  /games/engine/…                the RangerGames web build, served
//	                                    from this site (gamesengine.go)
//	GET  /games/p/<token>/game.rgpk     the game as one pack, for the engine
//	GET  /games/main/admin              the admin's page: who is invited
//	                                    and their speech allowance (the
//	                                    editor's cookie, SLIQTLY_ADMIN_EMAILS)
//	POST /games/main/admin/api/<op>     its calls: users, invite, remove
//
// Who may use the MCP server: the admins and the addresses the admin page
// invited (games_users). Playing needs no sign-in.

package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	gamesPath     = "/games"
	gamesMcpPath  = gamesPath + "/mcp"
	gamesResource = "/.well-known/oauth-protected-resource" + gamesMcpPath
	gamesAdmin    = gamesPath + "/main/admin"
)

//go:embed assets/games-guide.md
var gamesGuide string

// the menu a pack carries (the engine opens /games/launcher when a game is
// closed): the game to play again and how to make one through the MCP
//
//go:embed assets/games-launcher.tsx
var gamesLauncherTSX []byte

//go:embed assets/games-launcher.css
var gamesLauncherCSS []byte

var gamesLauncherJSON = []byte(`{"name":"Sliqtly Games","description":"","width":360,"hidden":true}`)

//go:embed assets/games-admin.html
var gamesAdminHTML string

// tool calls that change something, per account in ten minutes
var gamesLimit = rateLimiter(400, 10*time.Minute)

// pack downloads per address in ten minutes
var gamesPlayLimit = rateLimiter(300, 10*time.Minute)

// screenshot_game runs the engine here: a few a minute for each account
var gamesShotLimit = rateLimiter(60, 10*time.Minute)

var gamesGameID = strProp("The game's id (list_games)")

var gamesTools = []roomTool{
	{name: "games_guide", title: "Games guide", readOnly: true,
		desc: "How a RangerGames game is written: the folder (game.json, game.tsx, style.css, pictures, sounds), the frame loop (init, tick, view, pointer and key handlers), the rg.* API (sounds, speech files, vibration, saves, celebrate, random, 3D) and the layout CSS the engine knows. Read it before writing a game."},
	{name: "list_games", title: "List games", readOnly: true,
		desc: "Your games: id, name, whether published, the preview_url (always plays the latest files) and the link when published."},
	{name: "create_game", title: "Create a game",
		desc:     "A new game with a small starter (a star to tap) that runs at once; change it with write_files.",
		props:    map[string]any{"name": strProp("The game's name, shown in the menu")},
		required: []string{"name"}},
	{name: "get_game", title: "Get a game", readOnly: true,
		desc:     "A game's files (path, size), its game.json and its links.",
		props:    map[string]any{"game_id": gamesGameID},
		required: []string{"game_id"}},
	{name: "read_file", title: "Read a file", readOnly: true,
		desc:     "One file of the game: text for code and data, base64 for pictures and sounds.",
		props:    map[string]any{"game_id": gamesGameID, "path": strProp("The file's path in the game's folder, e.g. game.tsx")},
		required: []string{"game_id", "path"}},
	{name: "write_files", title: "Write files",
		desc: "Adds or replaces files in the game's folder (a virtual directory kept in the cloud): code and data as text, pictures (.png .jpg .webp .gif), sounds (.wav .mp3 .ogg) and 3D models (.glb, or .gltf + .bin) as base64. Up to 60 files a call, 8 MB a file, 40 MB a game. The preview_url plays the new files at once.",
		props: map[string]any{"game_id": gamesGameID, "files": map[string]any{"type": "array", "description": "The files",
			"items": map[string]any{"type": "object", "properties": map[string]any{
				"path":   strProp("Path in the folder: game.json, game.tsx, style.css, lib/rules.ts, img/cat.png, sounds/hop.wav, models/knight.glb"),
				"text":   strProp("The file as text (code, CSS, JSON)"),
				"base64": strProp("The file's bytes as base64 (pictures, sounds)"),
			}, "required": []string{"path"}}}},
		required: []string{"game_id", "files"}},
	{name: "delete_files", title: "Delete files", destructive: true,
		desc:     "Takes files out of the game's folder (not game.json or game.tsx).",
		props:    map[string]any{"game_id": gamesGameID, "paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The files' paths"}},
		required: []string{"game_id", "paths"}},
	{name: "rename_game", title: "Rename a game",
		desc:     "Changes the game's name in the list (the menu's name is game.json's).",
		props:    map[string]any{"game_id": gamesGameID, "name": strProp("The new name")},
		required: []string{"game_id", "name"}},
	{name: "publish_game", title: "Publish a game",
		desc:     "Gives the game a secret link that anyone can play it with, no sign-in (published: false takes it away; new_link: true makes a new link and the old one stops). Ask the user before publishing.",
		props:    map[string]any{"game_id": gamesGameID, "published": map[string]any{"type": "boolean", "description": "true (the default) publishes, false unpublishes"}, "new_link": map[string]any{"type": "boolean", "description": "A new secret link; the old one stops working"}},
		required: []string{"game_id"}},
	{name: "delete_game", title: "Delete a game", destructive: true,
		desc:     "Deletes the game and its files for good.",
		props:    map[string]any{"game_id": gamesGameID, "confirm_name": strProp("The game's name exactly")},
		required: []string{"game_id", "confirm_name"}},
	{name: "speak", title: "Make speech",
		desc:     "Speech made from text (Google Cloud Text-to-Speech) saved as a .wav file in the game, played with rg.sound(path). Only for accounts whose plan includes speech; counts characters against the monthly allowance.",
		props:    map[string]any{"game_id": gamesGameID, "text": strProp("What is said, up to 1500 characters"), "path": strProp("Where the sound goes, e.g. sounds/hello.wav"), "lang": strProp("fi (default), en, es, sv, de, fr… or a full code such as en-GB"), "voice": strProp("A Google voice name, e.g. fi-FI-Wavenet-A; the language's default when left out")},
		required: []string{"game_id", "text", "path"}},
	{name: "screenshot_game", title: "Screenshot a game", readOnly: true,
		desc: "Runs the game here in the RangerGames engine for a number of frames (60 a second) and returns a picture of the last frame (PNG) with what the game logged and any error. taps and keys play the game on the way: a tap is pressed on its frame and let go on the next, at x, y in the game's own coordinates (game.json's width; height as the screen gives). Emoji are drawn as outlines in the text's colour, and 3D without WebGL lighting.",
		props: map[string]any{"game_id": gamesGameID,
			"frames": map[string]any{"type": "integer", "description": "Frames to run before the picture, 1 to 1200 (default 30, half a second)"},
			"width":  map[string]any{"type": "integer", "description": "Screen width in CSS pixels, 160 to 1280 (default 360)"},
			"height": map[string]any{"type": "integer", "description": "Screen height in CSS pixels, 160 to 1280 (default 640)"},
			"taps": map[string]any{"type": "array", "description": "Taps on the way", "items": map[string]any{"type": "object", "properties": map[string]any{
				"x": map[string]any{"type": "number"}, "y": map[string]any{"type": "number"},
				"frame": map[string]any{"type": "integer", "description": "The frame it is pressed on (1 = the first)"}}, "required": []string{"x", "y", "frame"}}},
			"keys": map[string]any{"type": "array", "description": "Key presses on the way (ArrowLeft, Enter, a, Space…)", "items": map[string]any{"type": "object", "properties": map[string]any{
				"key": strProp("The key, as a browser names it"), "frame": map[string]any{"type": "integer"}}, "required": []string{"key", "frame"}}},
		},
		required: []string{"game_id"}},
}

func findGamesTool(name string) *roomTool {
	for i := range gamesTools {
		if gamesTools[i].name == name {
			return &gamesTools[i]
		}
	}
	return nil
}

const gamesInstructions = "Games makes RangerGames games (the engine for kids' phone games: TSX laid out with CSS, sounds, vibration) and keeps each as a folder in the cloud. Read games_guide first. create_game makes a game that runs at once; write game.tsx, style.css, pictures and sounds with write_files and give the user the preview_url to try it (it plays on a phone and in a browser). publish_game makes a secret link to share: ask the user before publishing. Games is by invitation."

// serveGames: true when the request was Games'
func serveGames(env *Env, w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	if p == gamesResource && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		if env.games == nil {
			http.NotFound(w, r)
			return true
		}
		o := env.games.origin()
		w.Header().Set("Access-Control-Allow-Origin", "*")
		editorJSON(w, 200, map[string]any{
			"resource": o + gamesMcpPath, "authorization_servers": []string{o},
			"scopes_supported": []string{"decks"}, "bearer_methods_supported": []string{"header"},
			"resource_name": "Games",
		})
		return true
	}
	if p != gamesPath && !strings.HasPrefix(p, gamesPath+"/") {
		return false
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Robots-Tag", "noindex")
	switch {
	case env.games == nil:
		http.NotFound(w, r)
	case p == gamesMcpPath:
		w.Header().Set("Cache-Control", "no-store")
		serveGamesMcp(env, w, r)
	case p == gamesPath+"/engine" || strings.HasPrefix(p, gamesEnginePath):
		serveGamesEngine(env, w, r)
	case strings.HasPrefix(p, gamesPath+"/p/"):
		serveGamesPlay(env, w, r, strings.TrimPrefix(p, gamesPath+"/p/"))
	case p == gamesAdmin || strings.HasPrefix(p, gamesAdmin+"/"):
		w.Header().Set("Cache-Control", "no-store")
		serveGamesAdmin(env, w, r)
	case p == gamesAgentStepPath:
		serveGamesAgentStep(env, w, r)
	case p == gamesPath || p == gamesPath+"/" || strings.HasPrefix(p, gamesPath+"/api/"):
		w.Header().Set("Cache-Control", "no-store")
		serveGamesHome(env, w, r)
	default:
		http.NotFound(w, r)
	}
	return true
}

// ------------------------------------------------------------------- play --

func serveGamesPlay(env *Env, w http.ResponseWriter, r *http.Request, rest string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET")
		http.Error(w, "GET only", 405)
		return
	}
	token, file, _ := strings.Cut(rest, "/")
	if file != "" && file != "game.rgpk" {
		http.NotFound(w, r)
		return
	}
	if why := gamesPlayLimit(clientIP(r)); why != "" {
		http.Error(w, "Too many requests; try again in a few minutes.", 429)
		return
	}
	g, err := env.games.byToken(r.Context(), token)
	if err != nil {
		log.Printf("games play: %v", err)
		http.Error(w, "The game could not be read; try again in a moment.", 503)
		return
	}
	if g == nil {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "No such game: the link is wrong, or the game is no longer shared.", 404)
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	if file == "" {
		// the engine's page on this site plays it (gamesengine.go)
		q := url.Values{}
		q.Set("pack", gamesPath+"/p/"+token+"/game.rgpk")
		q.Set("game", g.id)
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, gamesEnginePath+"?"+q.Encode(), http.StatusFound)
		return
	}
	etag := `"` + docStr(g.doc, "rev") + `"`
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(304)
		return
	}
	b, err := env.games.pack(r.Context(), g)
	if err != nil {
		log.Printf("games pack %s: %v", g.id, err)
		http.Error(w, "The game could not be read; try again in a moment.", 503)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(b)
}

// -------------------------------------------------------------------- MCP --

// the account an OAuth access token names, for this resource; nil when it
// holds none (or is another resource's)
func gamesTokenWho(ctx context.Context, env *Env, header string) (*gamesWho, error) {
	h := strings.TrimSpace(header)
	if len(h) < 8 || !strings.EqualFold(h[:7], "bearer ") {
		return nil, nil
	}
	tok := strings.TrimSpace(h[7:])
	if tok == "" || strings.Contains(tok, " ") {
		return nil, nil
	}
	sum := sha256.Sum256([]byte(tok))
	d, err := env.DB.Get(ctx, "mcp_oauth_tokens", hex.EncodeToString(sum[:]))
	if err != nil {
		return nil, err
	}
	if d == nil || docStr(d, "kind") != "access" || millis(d["exp"]) < time.Now().UnixMilli() || docStr(d, "uid") == "" {
		return nil, nil
	}
	// a token asked for another resource (sliqtly.com/mcp, Warden) is not
	// Games'
	if res := strings.TrimRight(docStr(d, "resource"), "/"); res != "" && res != env.games.origin()+gamesMcpPath {
		return nil, nil
	}
	return &gamesWho{uid: docStr(d, "uid"), name: docStr(d, "name"), email: strings.ToLower(docStr(d, "email"))}, nil
}

func serveGamesMcp(env *Env, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "WWW-Authenticate, Mcp-Session-Id")
	switch r.Method {
	case http.MethodOptions:
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Protocol-Version, Mcp-Session-Id")
		w.WriteHeader(204)
		return
	case http.MethodDelete:
		w.WriteHeader(204)
		return
	case http.MethodPost:
	default:
		w.Header().Set("Allow", "POST")
		editorJSON(w, 405, wardenRPCError(nil, -32000, "Method not allowed: POST."))
		return
	}
	if !env.OAuth {
		http.NotFound(w, r)
		return
	}
	who, err := gamesTokenWho(r.Context(), env, r.Header.Get("Authorization"))
	if err != nil {
		log.Printf("games mcp token: %v", err)
		editorJSON(w, 500, wardenRPCError(nil, -32603, "Internal error"))
		return
	}
	if who == nil {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+env.games.origin()+gamesResource+`"`)
		editorJSON(w, 401, wardenRPCError(nil, -32001, "Sign in to use Games."))
		return
	}
	var msg any
	if err := json.NewDecoder(io.LimitReader(r.Body, 40<<20)).Decode(&msg); err != nil {
		editorJSON(w, 400, wardenRPCError(nil, -32700, "Parse error"))
		return
	}
	if batch, ok := msg.([]any); ok {
		out := []any{}
		for _, m := range batch {
			if res := gamesRPC(r.Context(), env, *who, m); res != nil {
				out = append(out, res)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(202)
			return
		}
		editorJSON(w, 200, out)
		return
	}
	res := gamesRPC(r.Context(), env, *who, msg)
	if res == nil {
		w.WriteHeader(202)
		return
	}
	editorJSON(w, 200, res)
}

func gamesRPC(ctx context.Context, env *Env, who gamesWho, msg any) any {
	m, ok := msg.(map[string]any)
	if !ok {
		return wardenRPCError(nil, -32600, "Invalid Request")
	}
	id, hasID := m["id"]
	method, _ := m["method"].(string)
	if method == "" || !hasID {
		return nil
	}
	params, _ := m["params"].(map[string]any)
	ok200 := func(res any) any { return map[string]any{"jsonrpc": "2.0", "id": id, "result": res} }
	switch method {
	case "initialize":
		v := wardenVersions[0]
		if asked, _ := params["protocolVersion"].(string); slices.Contains(wardenVersions, asked) {
			v = asked
		}
		return ok200(map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "games", "title": "Games", "version": "0.1.0"},
			"instructions":    gamesInstructions,
		})
	case "ping":
		return ok200(map[string]any{})
	case "tools/list":
		l := []any{}
		for _, t := range gamesTools {
			l = append(l, toolEntry(t))
		}
		return ok200(map[string]any{"tools": l})
	case "tools/call":
		name, _ := params["name"].(string)
		t := findGamesTool(name)
		if t == nil {
			return wardenRPCError(id, -32602, "Tool "+name+" not found")
		}
		args, _ := params["arguments"].(map[string]any)
		if !t.readOnly {
			if why := gamesLimit(who.uid); why != "" {
				return ok200(wardenToolText("Too many changes in a short time; try again in a few minutes.", true))
			}
		}
		if gamesRunsEngine(name) {
			if why := gamesShotLimit(who.uid); why != "" {
				return ok200(wardenToolText("Too many screenshots in a short time; try again in a few minutes.", true))
			}
		}
		out, err := env.games.call(ctx, who, name, args)
		var re roomErr
		switch {
		case errors.As(err, &re):
			return ok200(wardenToolText(re.msg, true))
		case err != nil:
			log.Printf("games mcp %s: %v", name, err)
			return ok200(wardenToolText("Games could not be reached; try again in a moment.", true))
		}
		if shot, ok := out.(*gamesShotResult); ok {
			content := []any{}
			for _, p := range shot.pictures() {
				content = append(content, map[string]any{"type": "image", "data": gamesPNGBase64(p), "mimeType": "image/png"})
			}
			content = append(content, map[string]any{"type": "text", "text": shot.text()})
			return ok200(map[string]any{"content": content, "isError": false})
		}
		if o, ok := out.(map[string]any); ok && name == "games_guide" {
			return ok200(wardenToolText(o["guide"].(string), false))
		}
		res := wardenToolText(roomJSON(out), false)
		if o, ok := out.(map[string]any); ok {
			res["structuredContent"] = o
		}
		return ok200(res)
	}
	return wardenRPCError(id, -32601, "Method not found: "+method)
}

// ------------------------------------------------------------------ admin --

func serveGamesAdmin(env *Env, w http.ResponseWriter, r *http.Request) {
	g := env.EditorGate
	if g == nil {
		http.NotFound(w, r)
		return
	}
	t := g.user(r)
	if r.URL.Path == gamesAdmin {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if t == nil {
			io.WriteString(w, gamesLoginHTML)
			return
		}
		if !env.games.isAdmin(gamesWho{email: gamesVerifiedEmail(t)}) {
			w.WriteHeader(403)
			io.WriteString(w, "<!doctype html><meta charset=utf-8><title>Games</title><p>This page is the admin's only.</p>")
			return
		}
		io.WriteString(w, gamesAdminHTML)
		return
	}
	op := strings.TrimPrefix(r.URL.Path, gamesAdmin+"/api/")
	if r.Method != http.MethodPost || !sameSite(env, r) {
		editorJSON(w, 403, map[string]string{"error": "Not allowed."})
		return
	}
	if t == nil {
		editorJSON(w, 401, map[string]string{"error": "Sign in again."})
		return
	}
	if !env.games.isAdmin(gamesWho{email: gamesVerifiedEmail(t)}) {
		editorJSON(w, 403, map[string]string{"error": "The admin's only."})
		return
	}
	a := map[string]any{}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&a); err != nil && !errors.Is(err, io.EOF) {
		editorJSON(w, 400, map[string]string{"error": "A JSON object is expected."})
		return
	}
	out, err := env.games.adminCall(r.Context(), op, a)
	var re roomErr
	switch {
	case errors.As(err, &re):
		editorJSON(w, 400, map[string]string{"error": re.msg})
	case err != nil:
		log.Printf("games admin %s: %v", op, err)
		editorJSON(w, 503, map[string]string{"error": "Try again in a moment."})
	default:
		editorJSON(w, 200, out)
	}
}

func gamesVerifiedEmail(t *IDToken) string {
	if t == nil || !t.Verified {
		return ""
	}
	return strings.ToLower(t.Email)
}

// the editor's sign-in page, in Games' words
var gamesLoginHTML = strings.NewReplacer(
	"<title>Sliqtly editor</title>", "<title>Games admin</title>",
	">Sign in to the editor<", ">Sign in to Games admin<",
	"The Sliqtly editor is for signed-in users. Sign in with your Google account: a Trial license lets you edit two presentations in the cloud.",
	"The Games admin page is for the site's admin. Sign in with your Google account.",
	`"Kirjaudu editoriin"`, `"Kirjaudu Games-ylläpitoon"`,
	"Sliqtlyn editori on kirjautuneille käyttäjille. Kirjaudu Google-tililläsi: Trial-lisenssillä voit muokata kahta esitystä pilvessä.",
	"Games-ylläpito on sivuston ylläpitäjälle. Kirjaudu Google-tililläsi.",
).Replace(editorLoginHTML)

// the admin page's calls
func (s *gamesService) adminCall(ctx context.Context, op string, a map[string]any) (any, error) {
	switch op {
	case "users":
		return s.adminUsers(ctx)
	case "agent":
		return s.agentAdmin(ctx)
	case "agent_set":
		return s.agentAdminSet(ctx, a)
	case "invite":
		email, _ := a["email"].(string)
		email = strings.ToLower(strings.TrimSpace(email))
		if !wardenEmailRe.MatchString(email) {
			return nil, roomErr{"An email address."}
		}
		tts := int64(0)
		if v, ok := a["tts_month"].(float64); ok {
			if v < 0 || v > 10_000_000 {
				return nil, roomErr{"Speech: 0 to 10 000 000 characters a month."}
			}
			tts = int64(v)
		}
		maxGames := int64(gamesMaxGames)
		if v, ok := a["max_games"].(float64); ok {
			if v < 0 || v > 1000 {
				return nil, roomErr{"Games: 0 to 1000."}
			}
			maxGames = int64(v)
		}
		note, _ := a["note"].(string)
		if len([]rune(note)) > 200 {
			return nil, roomErr{"Note: at most 200 characters."}
		}
		d, err := s.db.Get(ctx, gamesUsersCol, email)
		if err != nil {
			return nil, err
		}
		set := Doc{"email": email, "tts_month": tts, "max_games": maxGames, "note": note, "listed": true}
		if d == nil {
			set["invited"] = s.now().UnixMilli()
			if err := s.db.Set(ctx, gamesUsersCol, email, set); err != nil {
				return nil, err
			}
		} else if err := s.db.Update(ctx, gamesUsersCol, email, set); err != nil {
			return nil, err
		}
		return s.adminUsers(ctx)
	case "remove":
		email, _ := a["email"].(string)
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" {
			return nil, roomErr{"An email address."}
		}
		if err := s.db.Delete(ctx, gamesUsersCol, email); err != nil {
			return nil, err
		}
		return s.adminUsers(ctx)
	}
	return nil, roomErr{"No such call."}
}

func (s *gamesService) adminUsers(ctx context.Context) (any, error) {
	// every invited address: all of them have listed: true
	docs, ids, err := s.db.WhereEq(ctx, gamesUsersCol, "listed", true)
	if err != nil {
		return nil, err
	}
	month := gamesMonth(s.now())
	l := []any{}
	for i, d := range docs {
		row := map[string]any{"email": ids[i], "tts_month": docInt(d["tts_month"]), "max_games": gamesUserMax(d), "note": docStr(d, "note"), "name": docStr(d, "name")}
		if uid := docStr(d, "uid"); uid != "" {
			if u, err := s.db.Get(ctx, gamesTTSCol, uid); err == nil && u != nil {
				row["tts_used"] = docInt(u[month])
			}
			if _, gids, err := s.db.WhereEq(ctx, gamesCol, "owner", uid); err == nil {
				row["games"] = len(gids)
			}
		}
		l = append(l, row)
	}
	slices.SortFunc(l, func(a, b any) int {
		return strings.Compare(a.(map[string]any)["email"].(string), b.(map[string]any)["email"].(string))
	})
	return map[string]any{"users": l, "admins": s.admins, "month": month, "speech_on": s.tts != nil}, nil
}
