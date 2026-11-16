// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const gamesBase = "https://sliqtly.test"

func gamesEnv(t *testing.T) (*Env, *fakeDB, *fakeBucket) {
	t.Helper()
	db, bucket := newFakeDB(), &fakeBucket{saved: map[string]savedFile{}}
	env := &Env{DB: db, Bucket: bucket, OAuth: true, BaseURL: gamesBase}
	env.games = newGamesService(db, bucket, []string{"admin@example.com"})
	env.games.base = gamesBase
	env.games.removePrefix = func(ctx context.Context, prefix string) { bucket.RemovePrefix(ctx, prefix) }
	return env, db, bucket
}

// an access token for /games/mcp, kept as the OAuth server keeps one
func gamesToken(db *fakeDB, uid, email, resource string) string {
	tok := "tok-" + uid + "-" + resource
	sum := sha256.Sum256([]byte(tok))
	db.Set(context.Background(), "mcp_oauth_tokens", hex.EncodeToString(sum[:]), Doc{
		"uid": uid, "name": uid, "email": email, "kind": "access", "resource": resource,
		"exp": time.Now().Add(time.Hour).UnixMilli()})
	return tok
}

func gamesServe(env *Env, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	if !serveGames(env, w, r) {
		w.WriteHeader(599)
	}
	return w
}

// one tool call: its structured answer, or its error text
func gamesTool(t *testing.T, env *Env, tok, name string, args map[string]any) (map[string]any, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	r := httptest.NewRequest("POST", gamesMcpPath, bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	w := gamesServe(env, r)
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
	}
	var out struct {
		Result struct {
			IsError           bool
			Content           []struct{ Text string }
			StructuredContent map[string]any
		}
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Result.IsError {
		return nil, out.Result.Content[0].Text
	}
	if out.Result.StructuredContent == nil {
		return map[string]any{"text": out.Result.Content[0].Text}, ""
	}
	return out.Result.StructuredContent, ""
}

func gamesMustTool(t *testing.T, env *Env, tok, name string, args map[string]any) map[string]any {
	t.Helper()
	out, e := gamesTool(t, env, tok, name, args)
	if e != "" {
		t.Fatalf("%s: %s", name, e)
	}
	return out
}

// the token in a play link
func gamesTokenOf(t *testing.T, link any) string {
	t.Helper()
	s, _ := link.(string)
	if !strings.HasPrefix(s, gamesBase+"/games/p/") {
		t.Fatalf("link %q", s)
	}
	return strings.TrimPrefix(s, gamesBase+"/games/p/")
}

func gamesPack(env *Env, token string) (int, []byte) {
	w := gamesServe(env, httptest.NewRequest("GET", "/games/p/"+token+"/game.rgpk", nil))
	return w.Code, w.Body.Bytes()
}

// a pack's files, as the engine's Vfs.mount_pack reads them
func gamesUnpack(t *testing.T, b []byte) map[string]string {
	t.Helper()
	out := map[string]string{}
	line := func() string {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			t.Fatalf("pack ends early")
		}
		s := string(b[:i])
		b = b[i+1:]
		return s
	}
	if line() != "RGPK1" {
		t.Fatal("not RGPK1")
	}
	var n int
	json.Unmarshal([]byte(line()), &n)
	for i := 0; i < n; i++ {
		p := line()
		var size int
		json.Unmarshal([]byte(line()), &size)
		out[p] = string(b[:size])
		b = b[size:]
	}
	if len(b) != 0 {
		t.Fatalf("%d bytes after the files", len(b))
	}
	return out
}

func TestGamesInviteOnly(t *testing.T) {
	env, db, _ := gamesEnv(t)
	// no token: the sign-in is asked for this resource
	r := httptest.NewRequest("POST", gamesMcpPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	w := gamesServe(env, r)
	if w.Code != 401 || !strings.Contains(w.Header().Get("WWW-Authenticate"), gamesBase+gamesResource) {
		t.Fatalf("no token: %d %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
	// a token for sliqtly.com/mcp is not Games'
	other := gamesToken(db, "u9", "admin@example.com", gamesBase+"/mcp")
	r = httptest.NewRequest("POST", gamesMcpPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	r.Header.Set("Authorization", "Bearer "+other)
	if w := gamesServe(env, r); w.Code != 401 {
		t.Fatalf("another resource's token: %d", w.Code)
	}
	stranger := gamesToken(db, "u2", "stranger@example.com", gamesBase+gamesMcpPath)
	if _, e := gamesTool(t, env, stranger, "list_games", nil); !strings.Contains(e, "invitation") {
		t.Fatalf("stranger: %q", e)
	}
	// the guide needs no invitation
	if g := gamesMustTool(t, env, stranger, "games_guide", nil); !strings.Contains(g["text"].(string), "rg.sound") {
		t.Fatal("guide")
	}
	// invited by the admin page: in
	if _, err := env.games.adminCall(context.Background(), "invite", map[string]any{"email": "Stranger@Example.com", "tts_month": float64(0)}); err != nil {
		t.Fatal(err)
	}
	if out := gamesMustTool(t, env, stranger, "list_games", nil); len(out["games"].([]any)) != 0 {
		t.Fatal("games before any")
	}
	users, _ := env.games.adminCall(context.Background(), "users", nil)
	l := users.(map[string]any)["users"].([]any)
	if len(l) != 1 || l[0].(map[string]any)["email"] != "stranger@example.com" || l[0].(map[string]any)["games"] != 0 {
		t.Fatalf("users %v", l)
	}
	env.games.adminCall(context.Background(), "remove", map[string]any{"email": "stranger@example.com"})
	if _, e := gamesTool(t, env, stranger, "list_games", nil); e == "" {
		t.Fatal("removed but still in")
	}
}

func TestGamesFolderAndPlay(t *testing.T) {
	env, db, bucket := gamesEnv(t)
	tok := gamesToken(db, "u1", "admin@example.com", gamesBase+gamesMcpPath)
	g := gamesMustTool(t, env, tok, "create_game", map[string]any{"name": "Pupu"})
	id := g["game_id"].(string)
	if !gamesIDRe.MatchString(id) || g["published"] != false || len(g["files"].([]any)) != 3 {
		t.Fatalf("created %v", g)
	}
	preview := gamesTokenOf(t, g["preview_url"])

	// the preview plays at once: a redirect to the engine with the pack
	w := gamesServe(env, httptest.NewRequest("GET", "/games/p/"+preview, nil))
	loc, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != 302 || !strings.HasPrefix(loc.String(), "/games/engine/?") ||
		loc.Query().Get("game") != id || loc.Query().Get("pack") != "/games/p/"+preview+"/game.rgpk" {
		t.Fatalf("play: %d %s", w.Code, loc)
	}
	code, pack := gamesPack(env, preview)
	files := gamesUnpack(t, pack)
	if code != 200 || !strings.Contains(files[id+"/game.json"], `"Pupu"`) || !strings.Contains(files[id+"/game.tsx"], "function view") {
		t.Fatalf("pack %d %v", code, files)
	}

	// write code and a picture; read them back
	png := []byte("\x89PNG\r\n\x1a\nfake")
	out := gamesMustTool(t, env, tok, "write_files", map[string]any{"game_id": id, "files": []any{
		map[string]any{"path": "game.tsx", "text": "function view() { return <div>hei</div>; }"},
		map[string]any{"path": "img/pupu.png", "base64": base64.StdEncoding.EncodeToString(png)},
	}})
	if out["file_count"] != float64(4) {
		t.Fatalf("after write %v", out)
	}
	if r := gamesMustTool(t, env, tok, "read_file", map[string]any{"game_id": id, "path": "game.tsx"}); r["text"] != "function view() { return <div>hei</div>; }" {
		t.Fatalf("read %v", r)
	}
	if r := gamesMustTool(t, env, tok, "read_file", map[string]any{"game_id": id, "path": "img/pupu.png"}); r["base64"] != base64.StdEncoding.EncodeToString(png) {
		t.Fatalf("read png %v", r)
	}
	_, pack = gamesPack(env, preview)
	if gamesUnpack(t, pack)[id+"/img/pupu.png"] != string(png) {
		t.Fatal("picture not in the pack")
	}
	// the old game.tsx blob is gone, the new one kept
	blobs := 0
	for p := range bucket.saved {
		if strings.HasPrefix(p, "games/"+id+"/blobs/") {
			blobs++
		}
	}
	if blobs != 4 {
		t.Fatalf("%d blobs", blobs)
	}

	// mistakes are said
	for _, bad := range []map[string]any{
		{"path": "../x.tsx", "text": "x"},
		{"path": "page.html", "text": "x"},
		{"path": "img/a.png", "text": "x"},
		{"path": "game.json", "text": "{}"},
	} {
		if _, e := gamesTool(t, env, tok, "write_files", map[string]any{"game_id": id, "files": []any{bad}}); e == "" {
			t.Fatalf("%v taken", bad)
		}
	}
	if _, e := gamesTool(t, env, tok, "delete_files", map[string]any{"game_id": id, "paths": []any{"game.tsx"}}); e == "" {
		t.Fatal("game.tsx deleted")
	}
	gamesMustTool(t, env, tok, "delete_files", map[string]any{"game_id": id, "paths": []any{"img/pupu.png"}})

	// another account does not see it
	env.games.adminCall(context.Background(), "invite", map[string]any{"email": "b@example.com"})
	other := gamesToken(db, "u2", "b@example.com", gamesBase+gamesMcpPath)
	if _, e := gamesTool(t, env, other, "get_game", map[string]any{"game_id": id}); !strings.Contains(e, "No such game") {
		t.Fatalf("other account: %q", e)
	}

	// the link plays only when published; a new link ends the old one
	link := db.data["games/"+id]["link"].(string)
	if code, _ := gamesPack(env, link); code != 404 {
		t.Fatalf("unpublished link: %d", code)
	}
	p := gamesMustTool(t, env, tok, "publish_game", map[string]any{"game_id": id})
	if gamesTokenOf(t, p["link"]) != link {
		t.Fatalf("published %v", p)
	}
	if code, _ := gamesPack(env, link); code != 200 {
		t.Fatalf("published link: %d", code)
	}
	p = gamesMustTool(t, env, tok, "publish_game", map[string]any{"game_id": id, "new_link": true})
	if code, _ := gamesPack(env, link); code != 404 {
		t.Fatal("old link still plays")
	}
	if code, _ := gamesPack(env, gamesTokenOf(t, p["link"])); code != 200 {
		t.Fatal("new link does not play")
	}

	if _, e := gamesTool(t, env, tok, "delete_game", map[string]any{"game_id": id, "confirm_name": "x"}); e == "" {
		t.Fatal("deleted without the name")
	}
	gamesMustTool(t, env, tok, "delete_game", map[string]any{"game_id": id, "confirm_name": "Pupu"})
	for p := range bucket.saved {
		if strings.HasPrefix(p, "games/"+id+"/") {
			t.Fatalf("%s left", p)
		}
	}
	if code, _ := gamesPack(env, preview); code != 404 {
		t.Fatal("deleted game plays")
	}
}

func TestGamesSpeechAllowance(t *testing.T) {
	env, db, _ := gamesEnv(t)
	said := []string{}
	env.games.tts = func(_ context.Context, text, lang, voice string) ([]byte, error) {
		said = append(said, lang+":"+text)
		return []byte("RIFF....WAVE"), nil
	}
	ctx := context.Background()
	env.games.adminCall(ctx, "invite", map[string]any{"email": "kid@example.com", "tts_month": float64(0)})
	kid := gamesToken(db, "k1", "kid@example.com", gamesBase+gamesMcpPath)
	id := gamesMustTool(t, env, kid, "create_game", map[string]any{"name": "Puhe"})["game_id"].(string)
	speak := func(tok, text string) string {
		_, e := gamesTool(t, env, tok, "speak", map[string]any{"game_id": id, "text": text, "path": "sounds/hei.wav"})
		return e
	}
	if e := speak(kid, "Hei"); !strings.Contains(e, "not in your plan") {
		t.Fatalf("no allowance: %q", e)
	}
	// the admin gives 10 characters a month
	env.games.adminCall(ctx, "invite", map[string]any{"email": "kid@example.com", "tts_month": float64(10)})
	if e := speak(kid, "Hei hei"); e != "" {
		t.Fatal(e)
	}
	if e := speak(kid, "Moikka"); !strings.Contains(e, "used up") {
		t.Fatalf("over the allowance: %q", e)
	}
	if len(said) != 1 || said[0] != "fi:Hei hei" {
		t.Fatalf("said %v", said)
	}
	if r := gamesMustTool(t, env, kid, "read_file", map[string]any{"game_id": id, "path": "sounds/hei.wav"}); r["base64"] != base64.StdEncoding.EncodeToString([]byte("RIFF....WAVE")) {
		t.Fatalf("sound %v", r)
	}
	users, _ := env.games.adminCall(ctx, "users", nil)
	if u := users.(map[string]any)["users"].([]any)[0].(map[string]any); u["tts_used"] != int64(7) {
		t.Fatalf("used %v", u)
	}
	// the admin has no limit
	admin := gamesToken(db, "a1", "admin@example.com", gamesBase+gamesMcpPath)
	aid := gamesMustTool(t, env, admin, "create_game", map[string]any{"name": "A"})["game_id"].(string)
	if _, e := gamesTool(t, env, admin, "speak", map[string]any{"game_id": aid, "text": strings.Repeat("a", 1400), "path": "sounds/a.wav"}); e != "" {
		t.Fatal(e)
	}
	// off on the server: said so
	env.games.tts = nil
	if e := speak(kid, "x"); !strings.Contains(e, "not turned on") {
		t.Fatalf("off: %q", e)
	}
}

func TestGamesTTSRequest(t *testing.T) {
	req, err := gamesTTSRequest("Hei", "fi", "")
	if err != nil || req["voice"].(map[string]any)["languageCode"] != "fi-FI" {
		t.Fatalf("%v %v", req, err)
	}
	req, _ = gamesTTSRequest("Hi", "en", "en-GB-Wavenet-B")
	if v := req["voice"].(map[string]any); v["languageCode"] != "en-GB" || v["name"] != "en-GB-Wavenet-B" {
		t.Fatalf("%v", v)
	}
	for _, bad := range [][2]string{{"xx", ""}, {"fi", "bad voice"}, {"FI", ""}} {
		if _, err := gamesTTSRequest("x", bad[0], bad[1]); err == nil {
			t.Fatalf("%v taken", bad)
		}
	}
	// the request Google gets, and the WAV its answer carries
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		if in["audioConfig"].(map[string]any)["audioEncoding"] != "LINEAR16" {
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"audioContent": base64.StdEncoding.EncodeToString([]byte("RIFFwav"))})
	}))
	defer srv.Close()
	b, err := gamesGoogleSpeaker(srv.Client(), srv.URL)(context.Background(), "Hei", "fi", "")
	if err != nil || string(b) != "RIFFwav" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestGamesEngineFromSite(t *testing.T) {
	env, _, _ := gamesEnv(t)
	asked := 0
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		switch r.URL.Path {
		case "/RangerGames/index.html":
			w.Write([]byte("<html>engine</html>"))
		case "/RangerGames/rg_web.wasm":
			w.Write([]byte("\x00asm"))
		default:
			w.WriteHeader(404)
		}
	}))
	defer src.Close()
	env.games.engineSrc = src.URL + "/RangerGames/"
	get := func(p string) *httptest.ResponseRecorder {
		return gamesServe(env, httptest.NewRequest("GET", p, nil))
	}
	if w := get("/games/engine/"); w.Code != 200 || !strings.Contains(w.Body.String(), "gamePack") || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("page: %d %q", w.Code, w.Body.String())
	}
	if w := get("/games/engine/rg_web.wasm"); w.Code != 200 || w.Header().Get("Content-Type") != "application/wasm" {
		t.Fatalf("wasm: %d", w.Code)
	}
	// kept: asked from the source once
	get("/games/engine/rg_web.wasm")
	if asked != 1 {
		t.Fatalf("source asked %d times", asked)
	}
	// only the engine's own files
	for _, p := range []string{"/games/engine/games.rgpk", "/games/engine/../mcp", "/games/engine/games/_voice/fi/x.wav", "/games/engine/js/../../x"} {
		if w := get(p); w.Code != 404 {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
	if w := get("/games/engine"); w.Code != 301 {
		t.Fatalf("no slash: %d", w.Code)
	}
	// the source down: the copy kept is served
	src.Close()
	env.games.engine.now = func() time.Time { return time.Now().Add(time.Hour) }
	if w := get("/games/engine/rg_web.wasm"); w.Code != 200 {
		t.Fatalf("stale copy: %d", w.Code)
	}
	if w := get("/games/engine/js/host.js"); w.Code != 503 {
		t.Fatalf("never read: %d", w.Code)
	}
}
