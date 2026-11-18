// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

const gamesBase = "https://sliqtly.test"

func gamesEnv(t *testing.T) (*Env, *fakeDB, *fakeBucket) {
	t.Helper()
	db, bucket := newFakeDB(), &fakeBucket{saved: map[string]savedFile{}}
	env := &Env{DB: db, Bucket: bucket, OAuth: true, BaseURL: gamesBase}
	env.games = newGamesService(db, bucket, []string{"admin@example.com"})
	env.games.base = gamesBase
	// most tests are of the invited; TestGamesOpen turns it off
	env.games.inviteOnly = true
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
	// the launcher only starts the game again (no menu)
	if l := files["launcher/game.tsx"]; !strings.Contains(l, "rg.launch(") || strings.Contains(l, "MINE") {
		t.Fatalf("launcher %.300s", l)
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
	// the API not turned on: said to the caller as Google says it
	off := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"error":{"code":403,"message":"Cloud Text-to-Speech API has not been used in project 1 before or it is disabled."}}`))
	}))
	defer off.Close()
	_, err = gamesGoogleSpeaker(off.Client(), off.URL)(context.Background(), "Hei", "fi", "")
	var re roomErr
	if !errors.As(err, &re) || !strings.Contains(re.msg, "has not been used") {
		t.Fatalf("403: %v", err)
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
	// compressed when the browser takes it, and not sent again when it has it
	r := httptest.NewRequest("GET", "/games/engine/rg_web.wasm", nil)
	r.Header.Set("Accept-Encoding", "gzip, br")
	w := gamesServe(env, r)
	zr, err := gzip.NewReader(w.Body)
	if err != nil || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip: %v %q", err, w.Header().Get("Content-Encoding"))
	}
	if b, _ := io.ReadAll(zr); string(b) != "\x00asm" {
		t.Fatalf("unzipped %q", b)
	}
	r = httptest.NewRequest("GET", "/games/engine/rg_web.wasm", nil)
	r.Header.Set("If-None-Match", w.Header().Get("ETag"))
	if w := gamesServe(env, r); w.Code != 304 || w.Header().Get("ETag") == "" {
		t.Fatalf("etag: %d", w.Code)
	}
	// kept: asked from the source once
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

// screenshot_game's painting: the engine's draw stream read as render.go's
// list (a rect and a clip), painted inside the game's area
func TestGamesShotPaint(t *testing.T) {
	rec := func(k, x, y, w, h, r, g, b, a float64) []float32 {
		n := make([]float32, 23)
		for i, v := range []float64{k, x, y, w, h, r, g, b, a} {
			n[i] = float32(v)
		}
		return n
	}
	var nums []float32
	nums = append(nums, rec(0, 0, 0, 100, 100, 255, 0, 0, 1)...)
	nums = append(nums, rec(4, 0, 0, 50, 50, 0, 0, 0, 0)...)
	nums = append(nums, rec(0, 0, 0, 100, 100, 0, 0, 255, 1)...)
	nums = append(nums, rec(5, 0, 0, 0, 0, 0, 0, 0, 0)...)
	cmds, pics := gamesDisplayList(nums, nil)
	if len(cmds) != 4 || len(pics) != 0 {
		t.Fatalf("cmds %d pics %d", len(cmds), len(pics))
	}
	// a 100×100 game at scale 2, 10 px from the left of a 220×200 screen
	img := gamesPaint(cmds, nil, 220, 200, [5]float64{2, 10, 0, 100, 100}, nil)
	at := func(x, y int) [3]uint8 {
		c := img.RGBAAt(x, y)
		return [3]uint8{c.R, c.G, c.B}
	}
	if at(5, 100) != [3]uint8{0, 0, 0} || at(215, 100) != [3]uint8{0, 0, 0} {
		t.Fatal("outside the game's area is not black")
	}
	if at(50, 50) != [3]uint8{0, 0, 255} || at(150, 150) != [3]uint8{255, 0, 0} {
		t.Fatalf("inside: %v %v", at(50, 50), at(150, 150))
	}
	// the arguments
	if _, err := gamesShotArgs(map[string]any{"frames": float64(5000)}); err == nil {
		t.Fatal("too many frames")
	}
	if _, err := gamesShotArgs(map[string]any{"frames": float64(10), "taps": []any{map[string]any{"x": 1.0, "y": 2.0, "frame": 11.0}}}); err == nil {
		t.Fatal("a tap after the last frame")
	}
	in, err := gamesShotArgs(map[string]any{"taps": []any{map[string]any{"x": 1.0, "y": 2.0, "frame": 3.0}}, "keys": []any{map[string]any{"key": "Enter", "frame": 4.0}}})
	if err != nil || in.frames != 30 || in.w != 360 || in.h != 640 || len(in.taps[3]) != 1 || in.keys[4][0] != "Enter" {
		t.Fatalf("args %+v %v", in, err)
	}
}

// sliqtly.com/games/: the page's calls, the per-account limit and hiding
func TestGamesHome(t *testing.T) {
	env, db, _ := gamesEnv(t)
	ctx := context.Background()
	s := env.games
	kid := gamesWho{uid: "u-kid", name: "Kid", email: "kid@example.com"}
	// signed in, not invited: told so
	me, err := s.homeCall(ctx, kid, "me", nil)
	if err != nil || me.(map[string]any)["invited"] != false {
		t.Fatalf("not invited: %v %v", me, err)
	}
	if _, err := s.homeCall(ctx, kid, "hide", map[string]any{"game_id": "g000000000000"}); err == nil {
		t.Fatal("a call without an invitation")
	}
	// invited with room for one game
	s.adminCall(ctx, "invite", map[string]any{"email": "kid@example.com", "max_games": float64(1)})
	tok := gamesToken(db, "u-kid", "kid@example.com", gamesBase+gamesMcpPath)
	g := gamesMustTool(t, env, tok, "create_game", map[string]any{"name": "Yksi"})
	if _, e := gamesTool(t, env, tok, "create_game", map[string]any{"name": "Kaksi"}); !strings.Contains(e, "(1)") {
		t.Fatalf("over the limit: %q", e)
	}
	id := g["game_id"].(string)
	me, _ = s.homeCall(ctx, kid, "me", nil)
	m := me.(map[string]any)
	if m["invited"] != true || m["max_games"] != 1 || len(m["games"].([]any)) != 1 || m["mcp"] != gamesBase+gamesMcpPath {
		t.Fatalf("me %v", m)
	}
	// publish, then hide: neither link plays
	out, err := s.homeCall(ctx, kid, "publish", map[string]any{"game_id": id, "published": true})
	if err != nil {
		t.Fatal(err)
	}
	link := gamesTokenOf(t, out.(map[string]any)["link"])
	preview := gamesTokenOf(t, g["preview_url"])
	if code, _ := gamesPack(env, link); code != 200 {
		t.Fatalf("published: %d", code)
	}
	s.homeCall(ctx, kid, "hide", map[string]any{"game_id": id, "hidden": true})
	if c1, _ := gamesPack(env, link); c1 != 404 {
		t.Fatalf("hidden, link: %d", c1)
	}
	if c2, _ := gamesPack(env, preview); c2 != 404 {
		t.Fatalf("hidden, preview: %d", c2)
	}
	s.homeCall(ctx, kid, "hide", map[string]any{"game_id": id, "hidden": false})
	if code, _ := gamesPack(env, preview); code != 200 {
		t.Fatalf("shown again: %d", code)
	}
	// another account's game is not theirs
	other := gamesWho{uid: "u-admin", email: "admin@example.com"}
	if _, err := s.homeCall(ctx, other, "delete", map[string]any{"game_id": id, "confirm_name": "Yksi"}); err == nil {
		t.Fatal("deleted another's game")
	}
	if _, err := s.homeCall(ctx, kid, "delete", map[string]any{"game_id": id, "confirm_name": "Yksi"}); err != nil {
		t.Fatal(err)
	}
	// the admin has no limit
	if u, _ := s.user(ctx, other); u.maxGames >= 0 {
		t.Fatal("admin limited")
	}
}

// The assistant on /games/: a run makes a game with the tools, round by
// round, its cost is counted, and the month's budget stops it.
func TestGamesAgent(t *testing.T) {
	env, _, _ := gamesEnv(t)
	ctx := context.Background()
	s := env.games
	s.now = func() time.Time { return time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC) }
	admin := gamesWho{uid: "u-admin", name: "Admin", email: "admin@example.com"}
	// the model: create_game, then write_files and a screenshot, then words
	var asked []map[string]any
	var paths []string
	gem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var ask map[string]any
		json.NewDecoder(r.Body).Decode(&ask)
		asked = append(asked, ask)
		contents := ask["contents"].([]any)
		var parts []any
		switch len(contents) {
		case 1:
			parts = []any{map[string]any{"functionCall": map[string]any{"name": "create_game", "args": map[string]any{"name": "Pallot"}}, "thoughtSignature": "c2ln"}}
		case 3:
			last := contents[2].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
			var made map[string]any
			json.Unmarshal([]byte(last["response"].(map[string]any)["content"].(string)), &made)
			id := made["game_id"].(string)
			parts = []any{
				map[string]any{"text": "Kirjoitan pelin."},
				map[string]any{"functionCall": map[string]any{"name": "write_files", "args": map[string]any{"game_id": id, "files": []any{map[string]any{"path": "notes.txt", "text": "hei"}}}}},
				map[string]any{"functionCall": map[string]any{"name": "publish_game", "args": map[string]any{"game_id": id}}},
			}
		default:
			parts = []any{map[string]any{"text": "Valmis: pallopeli."}}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"candidates":    []any{map[string]any{"content": map[string]any{"role": "model", "parts": parts}, "finishReason": "STOP"}},
			"usageMetadata": map[string]any{"promptTokenCount": 100000, "candidatesTokenCount": 1000, "thoughtsTokenCount": 9000},
		})
	}))
	defer gem.Close()
	s.agent = &geminiModel{http: gem.Client(), model: "gemini-4-argon", key: "k", base: gem.URL, maxOut: gamesAgentMaxOut}

	kid := gamesWho{uid: "u-kid", email: "kid@example.com"}
	s.adminCall(ctx, "invite", map[string]any{"email": "kid@example.com"})
	if _, err := s.homeCall(ctx, kid, "agent_start", map[string]any{"prompt": "peli"}); err == nil {
		t.Fatal("the assistant for an account not the admin's")
	}
	me, _ := s.homeCall(ctx, admin, "me", nil)
	if me.(map[string]any)["agent"] != true {
		t.Fatalf("me %v", me)
	}
	out, err := s.homeCall(ctx, admin, "agent_start", map[string]any{"prompt": "Tee pallopeli"})
	if err != nil {
		t.Fatal(err)
	}
	st := out.(map[string]any)
	id, key := st["run_id"].(string), st["key"].(string)
	if st["step_url"] != gamesAgentStepPath {
		t.Fatalf("step_url %v", st["step_url"])
	}
	if _, err := s.agentStep(ctx, id, "wrong"); err == nil {
		t.Fatal("a step with the wrong key")
	}
	// over HTTP, without a cookie: the key is enough
	step := func() map[string]any {
		r := httptest.NewRequest("POST", gamesAgentStepPath, strings.NewReader(`{"run_id":"`+id+`","key":"`+key+`"}`))
		r.Header.Set("Content-Type", "text/plain")
		w := gamesServe(env, r)
		var m map[string]any
		json.Unmarshal(w.Body.Bytes(), &m)
		if w.Code != 200 {
			t.Fatalf("step: %d %v", w.Code, m)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != gamesBase {
			t.Fatal("no CORS for the page")
		}
		return m
	}
	s1 := step()
	gid, _ := s1["game_id"].(string)
	if s1["done"] != false || gid == "" || s1["preview_url"] == nil {
		t.Fatalf("round 1: %v", s1)
	}
	// $2 / $10 a million in November: 100k in, 10k out (thinking too)
	if s1["step_usd"] != 0.3 {
		t.Fatalf("cost %v", s1["step_usd"])
	}
	s2 := step()
	logs := s2["log"].([]any)
	if len(logs) != 2 || logs[0].(map[string]any)["about"] != "notes.txt" || logs[1].(map[string]any)["error"] == nil {
		t.Fatalf("round 2 log: %v", logs)
	}
	if s2["text"] != "Kirjoitan pelin." {
		t.Fatalf("round 2 text %v", s2["text"])
	}
	g, _ := s.game(ctx, admin, gid)
	if g.doc["published"] == true {
		t.Fatal("the assistant published")
	}
	if _, err := s.readFile(ctx, g, map[string]any{"path": "notes.txt"}); err != nil {
		t.Fatalf("written: %v", err)
	}
	s3 := step()
	if s3["done"] != true || s3["text"] != "Valmis: pallopeli." || s3["cost_usd"] != 0.9 {
		t.Fatalf("round 3: %v", s3)
	}
	// the thought's signature went back as it came
	if b, _ := json.Marshal(asked[1]["contents"]); !strings.Contains(string(b), `"thoughtSignature":"c2ln"`) {
		t.Fatal("the signature was lost")
	}
	if tools := asked[0]["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any); len(tools) != len(gamesAgentTools) {
		t.Fatalf("%d tools", len(tools))
	}
	// the budget: $0.90 spent; a cap of $0.50 stops the next message
	a, _ := s.adminCall(ctx, "agent_set", map[string]any{"cap_usd": 0.5})
	if a.(map[string]any)["spent_usd"] != 0.9 {
		t.Fatalf("admin %v", a)
	}
	if _, err := s.homeCall(ctx, admin, "agent_start", map[string]any{"prompt": "lisää", "run_id": id, "key": key}); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("over the budget: %v", err)
	}
	// December's price
	if in, out := (gamesAgentConfig{}).prices("gemini-4-argon", time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)); in != 4 || out != 20 {
		t.Fatalf("december %v %v", in, out)
	}

	// a model chosen on the page: its address and its price
	s.adminCall(ctx, "agent_set", map[string]any{"cap_usd": 20.0})
	if ms := me.(map[string]any)["agent_models"].([]any); len(ms) < 3 || ms[0].(map[string]any)["id"] != "gemini-4-argon" {
		t.Fatalf("models %v", ms)
	}
	if _, err := s.homeCall(ctx, admin, "agent_start", map[string]any{"prompt": "lisää", "run_id": id, "key": key, "model": "gpt-9"}); err == nil {
		t.Fatal("an unknown model")
	}
	if _, err := s.homeCall(ctx, admin, "agent_start", map[string]any{"prompt": "lisää", "run_id": id, "key": key, "model": "gemini-3.8-flash"}); err != nil {
		t.Fatal(err)
	}
	s4 := step()
	if !strings.Contains(paths[len(paths)-1], "gemini-3.8-flash") || s4["model"] != "gemini-3.8-flash" {
		t.Fatalf("model %s %v", paths[len(paths)-1], s4["model"])
	}
	// $0.75 / $3.75 a million: 100k in, 10k out
	if c := s4["step_usd"].(float64); math.Abs(c-0.1125) > 0.006 {
		t.Fatalf("flash cost %v", c)
	}

	// pictures with a message: words that say what each is, then the picture
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG fake"))
	pic := func(mime, data string) map[string]any {
		return map[string]any{"mime": mime, "data": data, "note": "red ring at x=10, y=20"}
	}
	if _, err := s.homeCall(ctx, admin, "agent_start", map[string]any{"prompt": "", "run_id": id, "key": key, "images": []any{pic("image/svg+xml", png)}}); err == nil {
		t.Fatal("an SVG picture")
	}
	if _, err := s.homeCall(ctx, admin, "agent_start", map[string]any{"run_id": id, "key": key, "images": []any{pic("image/png", png), pic("image/png", png), pic("image/png", png), pic("image/png", png), pic("image/png", png)}}); err == nil {
		t.Fatal("five pictures")
	}
	if _, err := s.homeCall(ctx, admin, "agent_start", map[string]any{"run_id": id, "key": key, "images": []any{pic("image/png", png)}}); err != nil {
		t.Fatal(err)
	}
	step()
	cs := asked[len(asked)-1]["contents"].([]any)
	b, _ := json.Marshal(cs[len(cs)-1])
	if !strings.Contains(string(b), `"data":"`+png+`","mimeType":"image/png"`) || !strings.Contains(string(b), "Picture 1 from the person: red ring at x=10, y=20") || !strings.Contains(string(b), "See the pictures") {
		t.Fatalf("the message with a picture: %s", b)
	}
}

// only the latest screenshots keep their pictures
func TestGamesAgentForget(t *testing.T) {
	shot := func() geminiContent {
		return geminiContent{Role: "user", Parts: []geminiPart{{FunctionResponse: &geminiResponse{Name: "screenshot_game", Response: map[string]any{"content": "ok"}, Parts: []geminiInline{{InlineData: &geminiData{MimeType: "image/png", Data: "AA=="}}}}}}}
	}
	c := []geminiContent{shot(), shot(), shot()}
	gamesAgentForget(c, 2)
	if len(c[0].Parts[0].FunctionResponse.Parts) != 0 || len(c[1].Parts[0].FunctionResponse.Parts) != 1 || len(c[2].Parts[0].FunctionResponse.Parts) != 1 {
		t.Fatal("pictures kept")
	}
}

// the page's source view and zip: the owner's files, under games/<slug>/
func TestGamesZip(t *testing.T) {
	env, db, _ := gamesEnv(t)
	ctx := context.Background()
	s := env.games
	tok := gamesToken(db, "u-admin", "admin@example.com", gamesBase+gamesMcpPath)
	g := gamesMustTool(t, env, tok, "create_game", map[string]any{"name": "Ätsän Pallot!"})
	id := g["game_id"].(string)
	admin := gamesWho{uid: "u-admin", email: "admin@example.com"}
	f, err := s.homeCall(ctx, admin, "file", map[string]any{"game_id": id, "path": "game.tsx"})
	if err != nil || f.(map[string]any)["text"] == nil {
		t.Fatalf("file %v %v", f, err)
	}
	w := httptest.NewRecorder()
	if err := s.zip(ctx, w, admin, id); err != nil {
		t.Fatal(err)
	}
	if w.Header().Get("Content-Disposition") != `attachment; filename="atsan-pallot.zip"` {
		t.Fatalf("name %q", w.Header().Get("Content-Disposition"))
	}
	z, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, f := range z.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, " ") != "games/atsan-pallot/game.json games/atsan-pallot/game.tsx games/atsan-pallot/style.css" {
		t.Fatalf("zip %v", names)
	}
	s.adminCall(ctx, "invite", map[string]any{"email": "kid@example.com"})
	if err := s.zip(ctx, httptest.NewRecorder(), gamesWho{uid: "u-kid", email: "kid@example.com"}, id); err == nil {
		t.Fatal("another account's zip")
	}
}

// play_game's parts that need no engine: the names inspect_game reads, the
// steps, finding a button by its text, and the saved points
func TestGamesPlayParts(t *testing.T) {
	src := "import { x } from \"lib/a\";\n// let hidden = 1;\nlet score: number = 0;\nconst state = { level: 1, f() { let inner = 2; } };\nvar old = 'it\\'s';\nexport let shown = true;\nfunction view() { const local = 1; return <p>Don't {local}</p>; }\nclass Board {}\nlet s = `a ${score} {`;\nfor (let i = 0; i < 2; i++) {}\nfunction* gen() {}\n"
	got := strings.Join(gamesTopNames(src), ",")
	if got != "score,state,old,shown,view,Board,s,gen" {
		t.Fatalf("names %s", got)
	}
	expr := gamesGlobalsExpr([]string{"score", "bad-name"})
	if !strings.Contains(expr, `o["score"] = score`) || strings.Contains(expr, "bad-name") {
		t.Fatalf("expr %s", expr)
	}

	steps, err := gamesSteps([]any{
		map[string]any{"tap": map[string]any{"text": "Pelaa"}, "shot": true},
		map[string]any{"tap": map[string]any{"x": 10.0, "y": 20.0}, "hold": 30.0, "wait": 0.0},
		map[string]any{"drag": map[string]any{"from": map[string]any{"cls": "a"}, "to": map[string]any{"x": 1.0, "y": 2.0}}},
		map[string]any{"key": "ArrowLeft", "wait": 5.0},
		map[string]any{"wait": 60.0},
	})
	if err != nil {
		t.Fatal(err)
	}
	frames := []int{}
	for _, s := range steps {
		frames = append(frames, s.frames())
	}
	if fmt.Sprint(frames) != "[22 31 41 6 60]" || !steps[0].shot {
		t.Fatalf("frames %v", frames)
	}
	for _, bad := range []any{
		[]any{map[string]any{}},
		[]any{map[string]any{"tap": map[string]any{"text": " "}}},
		[]any{map[string]any{"tap": map[string]any{"x": 1.0, "y": 1.0}, "key": "a"}},
		[]any{map[string]any{"wait": 5000.0}},
		"tap",
	} {
		if _, err := gamesSteps(bad); err == nil {
			t.Fatalf("%v passed", bad)
		}
	}

	root := &gamesNode{T: "div", W: 360, H: 640, C: []*gamesNode{
		{T: "div", ID: "__h0", Cls: "btn big", X: 100, Y: 300, W: 160, H: 60, C: []*gamesNode{{T: "#text", X: 140, Y: 310, W: 80, H: 40, Text: "Pelaa"}}},
		{T: "div", X: 0, Y: 0, W: 360, H: 40, C: []*gamesNode{{T: "#text", X: 10, Y: 5, W: 200, H: 30, Text: "Pelaa  uudestaan"}}},
		{T: "div", ID: "__h1", Cls: "dot", X: 20, Y: 600, W: 20, H: 20},
	}}
	gamesFold(root)
	if root.C[0].Text != "Pelaa" || root.C[0].C != nil {
		t.Fatalf("fold %+v", root.C[0])
	}
	if n := gamesFind(root, &gamesTarget{text: "pelaa"}); n != root.C[0] {
		t.Fatalf("exact text %+v", n)
	}
	if n := gamesFind(root, &gamesTarget{text: "uudestaan"}); n != root.C[1] {
		t.Fatalf("contained text %+v", n)
	}
	if n := gamesFind(root, &gamesTarget{cls: "big"}); n != root.C[0] {
		t.Fatalf("class %+v", n)
	}
	if n := gamesFind(root, &gamesTarget{text: "ei ole"}); n != nil {
		t.Fatalf("missing %+v", n)
	}
	lines := gamesOnScreen(root, map[string]bool{"__h0": true, "__h1": true}, 10)
	want := "\"Pelaa\" [tap] x=180 y=330 (160×60)|\"Pelaa uudestaan\" x=180 y=20 (360×40)|[tap] div.dot x=30 y=610 (20×20)"
	if strings.Join(lines, "|") != want {
		t.Fatalf("on screen %q", lines)
	}

	// saved points: kept per game, the oldest go past the limit
	env, db, _ := gamesEnv(t)
	s := env.games
	all := map[string]gamesSnap{}
	for i := 0; i < gamesSnapsMax+2; i++ {
		if err := s.saveSnap(context.Background(), "g1", fmt.Sprintf("p%02d", i), all, gamesSnap{[]any{map[string]any{"wait": 10.0}}, 360, 640, 10, int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	back, err := s.snaps(context.Background(), "g1")
	if err != nil || len(back) != gamesSnapsMax {
		t.Fatalf("snaps %d %v", len(back), err)
	}
	if _, ok := back["p00"]; ok {
		t.Fatal("the oldest stayed")
	}
	if sn := back["p21"]; sn.w != 360 || sn.frames != 10 || len(sn.steps) != 1 {
		t.Fatalf("snap %+v", sn)
	}

	// through the tools: a point that is not saved, and a bad name
	tok := gamesToken(db, "u1", "admin@example.com", gamesBase+gamesMcpPath)
	g := gamesMustTool(t, env, tok, "create_game", map[string]any{"name": "Peli"})
	if _, e := gamesTool(t, env, tok, "play_game", map[string]any{"game_id": g["game_id"], "from": "taso 2"}); !strings.Contains(e, "no saved points") {
		t.Fatalf("from: %s", e)
	}
	if _, e := gamesTool(t, env, tok, "inspect_game", map[string]any{"game_id": g["game_id"], "save_as": "a/b"}); !strings.Contains(e, "save_as") {
		t.Fatalf("save_as: %s", e)
	}
}

// /games/editor: the assistant beside the game, behind the sign-in
func TestGamesEditorPage(t *testing.T) {
	env, _, _ := gamesEnv(t)
	web := fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}, "main.js": {Data: []byte("//")}, "pres_app.js": {Data: []byte("//")}}
	verify := func(_ context.Context, tok string) (*IDToken, error) {
		if tok == "tero" {
			return &IDToken{UID: "u-tero", Email: "teroktolonen@gmail.com", Name: "Tero", Verified: true}, nil
		}
		return nil, errors.New("expired")
	}
	env.EditorGate = newEditorGate(web, env.DB, verify, nil)
	if env.EditorGate == nil {
		t.Fatal("no gate")
	}
	r := httptest.NewRequest("GET", gamesEditorPath, nil)
	r.AddCookie(&http.Cookie{Name: editorCookie, Value: "tero"})
	if w := gamesServe(env, r); w.Code != 200 || w.Body.String() != gamesEditorHTML {
		t.Fatalf("signed in: %d %.200s", w.Code, w.Body.String())
	}
	w := gamesServe(env, httptest.NewRequest("GET", gamesEditorPath, nil))
	if w.Code != 200 || w.Body.String() != gamesHomeLoginHTML {
		t.Fatalf("signed out: %d %.200s", w.Code, w.Body.String())
	}
	if w := gamesServe(env, httptest.NewRequest("POST", gamesEditorPath, nil)); w.Code != 405 {
		t.Fatalf("POST: %d", w.Code)
	}
	for _, want := range []string{"agent_start", "/games/engine/?pack=", "mode=frame", "games.agent.run"} {
		if !strings.Contains(gamesEditorHTML, want) {
			t.Fatalf("editor page lacks %s", want)
		}
	}
}

// every pack (but ?menu=0) carries the launcher that restarts the game;
// ?lazy=1 leaves the .wav files
// to b/<sha>
func TestGamesPackMenuAndLazy(t *testing.T) {
	env, db, _ := gamesEnv(t)
	web := fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}, "main.js": {Data: []byte("//")}, "pres_app.js": {Data: []byte("//")}}
	verify := func(_ context.Context, tok string) (*IDToken, error) {
		if tok == "u1" {
			return &IDToken{UID: "u1", Email: "admin@example.com", Name: "U", Verified: true}, nil
		}
		return nil, errors.New("expired")
	}
	env.EditorGate = newEditorGate(web, env.DB, verify, nil)
	tok := gamesToken(db, "u1", "admin@example.com", gamesBase+gamesMcpPath)
	a := gamesMustTool(t, env, tok, "create_game", map[string]any{"name": "Eka"})
	b := gamesMustTool(t, env, tok, "create_game", map[string]any{"name": "Toka"})
	wav := []byte("RIFF....WAVEfmt fake clip")
	gamesMustTool(t, env, tok, "write_files", map[string]any{"game_id": a["game_id"], "files": []any{
		map[string]any{"path": "voice/hei.wav", "base64": base64.StdEncoding.EncodeToString(wav)},
	}})
	id := a["game_id"].(string)
	preview := gamesTokenOf(t, a["preview_url"])
	get := func(path, cookie, etag string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: editorCookie, Value: cookie})
		}
		if etag != "" {
			r.Header.Set("If-None-Match", etag)
		}
		return gamesServe(env, r)
	}

	// signed in or not, the same public pack: the launcher starts the game
	// again, it lists no games
	w := get("/games/p/"+preview+"/game.rgpk", "u1", "")
	files := gamesUnpack(t, w.Body.Bytes())
	menu := files["launcher/game.tsx"]
	if w.Code != 200 || !strings.Contains(menu, "rg.launch(") || strings.Contains(menu, "Toka") ||
		strings.Contains(menu, gamesTokenOf(t, b["preview_url"])) || strings.HasPrefix(w.Header().Get("Cache-Control"), "private") {
		t.Fatalf("launcher %d %s %.300s", w.Code, w.Header().Get("Cache-Control"), menu)
	}
	if files[id+"/voice/hei.wav"] != string(wav) {
		t.Fatal("the whole pack lacks the clip")
	}
	etag := w.Header().Get("ETag")
	if w := get("/games/p/"+preview+"/game.rgpk", "u1", etag); w.Code != 304 {
		t.Fatalf("same pack again: %d", w.Code)
	}
	if w := get("/games/p/"+preview+"/game.rgpk", "", etag); w.Code != 304 {
		t.Fatalf("signed out, same pack: %d", w.Code)
	}
	// ?menu=0: no launcher (the editor's frame), and another tag
	w = get("/games/p/"+preview+"/game.rgpk?menu=0", "u1", "")
	if f := gamesUnpack(t, w.Body.Bytes()); f["launcher/game.tsx"] != "" || w.Header().Get("ETag") == etag {
		t.Fatal("menu=0 has a launcher")
	}

	// lazy: the clip left out, listed with its sha, and served from b/<sha>
	w = get("/games/p/"+preview+"/game.rgpk?lazy=1", "", "")
	files = gamesUnpack(t, w.Body.Bytes())
	if _, ok := files[id+"/voice/hei.wav"]; ok || files[id+"/game.tsx"] == "" {
		t.Fatalf("lazy pack %v", files)
	}
	remote := map[string]string{}
	json.Unmarshal([]byte(files[id+"/.remote.json"]), &remote)
	sha := remote["voice/hei.wav"]
	if len(sha) != 64 {
		t.Fatalf("remote %v", files[id+"/.remote.json"])
	}
	if w.Header().Get("ETag") == get("/games/p/"+preview+"/game.rgpk", "", "").Header().Get("ETag") {
		t.Fatal("lazy and whole packs share an etag")
	}
	w = get("/games/p/"+preview+"/b/"+sha, "", "")
	if w.Code != 200 || w.Body.String() != string(wav) || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") || w.Header().Get("Content-Type") != "audio/wav" {
		t.Fatalf("blob %d %s %q", w.Code, w.Header().Get("Cache-Control"), w.Body.String())
	}
	// only the game's own files
	other := strings.Repeat("ab", 32)
	if w := get("/games/p/"+preview+"/b/"+other, "", ""); w.Code != 404 {
		t.Fatalf("foreign sha: %d", w.Code)
	}
	if w := get("/games/p/"+gamesTokenOf(t, b["preview_url"])+"/b/"+sha, "", ""); w.Code != 404 {
		t.Fatalf("another game's link: %d", w.Code)
	}
	// a new file reaches the pack at once (the editor reloads right after a write)
	gamesMustTool(t, env, tok, "write_files", map[string]any{"game_id": id, "files": []any{
		map[string]any{"path": "game.tsx", "text": "function view() { return <div>uusi</div>; }"},
	}})
	if f := gamesUnpack(t, get("/games/p/"+preview+"/game.rgpk?lazy=1", "", "").Body.Bytes()); !strings.Contains(f[id+"/game.tsx"], "uusi") {
		t.Fatal("stale pack after a write")
	}
}

// a new game from an old one: all its files, game.json with the new name,
// the original as it was
func TestGamesCopy(t *testing.T) {
	env, db, _ := gamesEnv(t)
	tok := gamesToken(db, "u1", "admin@example.com", gamesBase+gamesMcpPath)
	a := gamesMustTool(t, env, tok, "create_game", map[string]any{"name": "Eka"})
	id := a["game_id"].(string)
	png := []byte("\x89PNG\r\n\x1a\nfake")
	gamesMustTool(t, env, tok, "write_files", map[string]any{"game_id": id, "files": []any{
		map[string]any{"path": "game.tsx", "text": "function view() { return <div>vanha</div>; }"},
		map[string]any{"path": "img/a.png", "base64": base64.StdEncoding.EncodeToString(png)},
	}})
	c := gamesMustTool(t, env, tok, "create_game", map[string]any{"name": "Toka", "copy_of": id})
	cid := c["game_id"].(string)
	if cid == id || c["copy_of"] != id || len(c["files"].([]any)) != 4 {
		t.Fatalf("copy %v", c)
	}
	read := func(g, p string) any {
		return gamesMustTool(t, env, tok, "read_file", map[string]any{"game_id": g, "path": p})
	}
	if r := read(cid, "game.tsx").(map[string]any); r["text"] != "function view() { return <div>vanha</div>; }" {
		t.Fatalf("code %v", r)
	}
	if r := read(cid, "img/a.png").(map[string]any); r["base64"] != base64.StdEncoding.EncodeToString(png) {
		t.Fatalf("png %v", r)
	}
	if r := read(cid, "game.json").(map[string]any); !strings.Contains(r["text"].(string), `"Toka"`) {
		t.Fatalf("copy's game.json %v", r)
	}
	if r := read(id, "game.json").(map[string]any); !strings.Contains(r["text"].(string), `"Eka"`) {
		t.Fatalf("original's game.json %v", r)
	}
	// changing the copy leaves the original
	gamesMustTool(t, env, tok, "write_files", map[string]any{"game_id": cid, "files": []any{
		map[string]any{"path": "game.tsx", "text": "function view() { return <div>uusi</div>; }"},
	}})
	if r := read(id, "game.tsx").(map[string]any); r["text"] != "function view() { return <div>vanha</div>; }" {
		t.Fatalf("original changed %v", r)
	}
	// only your own games
	tok2 := gamesToken(db, "u2", "admin@example.com", gamesBase+gamesMcpPath)
	if _, e := gamesTool(t, env, tok2, "create_game", map[string]any{"name": "Varas", "copy_of": id}); !strings.Contains(e, "No such game") {
		t.Fatalf("someone else's game: %s", e)
	}
	// the editor's button
	out, err := env.games.homeCall(context.Background(), gamesWho{uid: "u1", email: "admin@example.com"}, "copy", map[string]any{"game_id": id, "name": "Kolmas"})
	if m, _ := out.(map[string]any); err != nil || m["name"] != "Kolmas" || m["copy_of"] != id {
		t.Fatalf("home copy %v %v", out, err)
	}
}

// without SLIQTLY_GAMES_INVITE_ONLY any verified Google address may make
// games: fewer of them and no speech until the admin page invites it
func TestGamesOpen(t *testing.T) {
	env, db, _ := gamesEnv(t)
	ctx := context.Background()
	env.games.inviteOnly = false
	anyone := gamesToken(db, "u9", "anyone@example.com", gamesBase+gamesMcpPath)
	if out := gamesMustTool(t, env, anyone, "list_games", nil); len(out["games"].([]any)) != 0 {
		t.Fatal("games before any")
	}
	me, err := env.games.homeCall(ctx, gamesWho{uid: "u9", email: "anyone@example.com"}, "me", nil)
	if err != nil || me.(map[string]any)["invited"] != true || me.(map[string]any)["max_games"] != gamesOpenGames || me.(map[string]any)["tts_month"] != int64(0) {
		t.Fatalf("me %v %v", me, err)
	}
	if me.(map[string]any)["agent"] == true {
		t.Fatal("the assistant for anyone")
	}
	// an address no provider vouches for is still out
	if _, err := env.games.user(ctx, gamesWho{uid: "u10"}); err == nil {
		t.Fatal("no verified address")
	}
}

// example games: the guide's topics, a game from one, playing one unsigned
func TestGamesExamples(t *testing.T) {
	env, db, _ := gamesEnv(t)
	ctx := context.Background()
	admin := gamesToken(db, "u-admin", "admin@example.com", gamesBase+gamesMcpPath)
	core := gamesMustTool(t, env, admin, "games_guide", nil)["text"].(string)
	if !strings.Contains(core, "topic") || strings.Contains(core, "AnimationMixer") {
		t.Fatal("the core guide: topics listed, 3D in its own")
	}
	for topic, want := range map[string]string{"3d": "AnimationMixer", "menus": "onBack", "examples": "muistipeli", "example:nappikisa": "function Half(", "EXAMPLE:muistipeli": "## style.css"} {
		if g := gamesMustTool(t, env, admin, "games_guide", map[string]any{"topic": topic})["text"].(string); !strings.Contains(g, want) {
			t.Fatalf("topic %s: no %q", topic, want)
		}
	}
	if _, e := gamesTool(t, env, admin, "games_guide", map[string]any{"topic": "nope"}); !strings.Contains(e, "Topics") {
		t.Fatalf("an unknown topic: %q", e)
	}
	for _, x := range gamesExamples {
		for _, f := range x.files {
			if strings.Contains(string(f.data), "rg.say") || strings.Contains(string(f.data), "rg.exit") {
				t.Fatalf("%s/%s uses the engine's own clips or menu", x.Name, f.path)
			}
		}
	}
	// create_game from an example: its files, the new name in game.json
	out := gamesMustTool(t, env, admin, "create_game", map[string]any{"name": "Oma muistipeli", "example": "muistipeli"})
	g, _ := env.games.game(ctx, gamesWho{uid: "u-admin"}, out["game_id"].(string))
	f, err := env.games.readFile(ctx, g, map[string]any{"path": "game.tsx"})
	if err != nil || !strings.Contains(f.(map[string]any)["text"].(string), "function Menu()") {
		t.Fatalf("example files: %v %v", f, err)
	}
	j, _ := env.games.readFile(ctx, g, map[string]any{"path": "game.json"})
	if s := j.(map[string]any)["text"].(string); !strings.Contains(s, `"Oma muistipeli"`) || strings.Contains(s, "i18n") {
		t.Fatalf("game.json %s", s)
	}
	if _, e := gamesTool(t, env, admin, "create_game", map[string]any{"name": "X", "example": "tetris"}); !strings.Contains(e, "No such example") {
		t.Fatalf("an unknown example: %q", e)
	}
	// the page: a copy of an example
	who := gamesWho{uid: "u-admin", email: "admin@example.com"}
	me, _ := env.games.homeCall(ctx, who, "me", nil)
	if len(me.(map[string]any)["examples"].([]gamesExample)) != len(gamesExamples) {
		t.Fatal("me: no examples")
	}
	n, err := env.games.homeCall(ctx, who, "from_example", map[string]any{"example": "nappikisa"})
	if err != nil || n.(map[string]any)["name"] != "Nappikisa" {
		t.Fatalf("from_example %v %v", n, err)
	}
	// played without signing in
	w := gamesServe(env, httptest.NewRequest("GET", "/games/x/nappikisa", nil))
	if w.Code != 302 || !strings.Contains(w.Header().Get("Location"), "pack=%2Fgames%2Fx%2Fnappikisa%2Fgame.rgpk") || !strings.Contains(w.Header().Get("Location"), "game=x-nappikisa") {
		t.Fatalf("play: %d %s", w.Code, w.Header().Get("Location"))
	}
	w = gamesServe(env, httptest.NewRequest("GET", "/games/x/nappikisa/game.rgpk", nil))
	if b := w.Body.String(); w.Code != 200 || !strings.HasPrefix(b, "RGPK1\n6\n") || !strings.Contains(b, "x-nappikisa/game.tsx\n") || !strings.Contains(b, "launcher/game.tsx\n") {
		t.Fatalf("pack: %d %.60q", w.Code, b)
	}
	if w := gamesServe(env, httptest.NewRequest("GET", "/games/x/nope", nil)); w.Code != 404 {
		t.Fatalf("no such example: %d", w.Code)
	}
}

// the source editor: its page, and saving one text file of one's own game
func TestGamesSource(t *testing.T) {
	env, _, _ := gamesEnv(t)
	ctx := context.Background()
	s := env.games
	s.inviteOnly = false
	who := gamesWho{uid: "u5", email: "maker@example.com"}
	other := gamesWho{uid: "u6", email: "other@example.com"}
	n, err := s.homeCall(ctx, who, "from_example", map[string]any{"example": "nappikisa", "name": "Oma"})
	if err != nil {
		t.Fatal(err)
	}
	id := n.(map[string]any)["game_id"].(string)
	if _, err := s.homeCall(ctx, who, "write", map[string]any{"game_id": id, "path": "lib/tasot.ts", "text": "export const N = 3;\n"}); err != nil {
		t.Fatal(err)
	}
	g, _ := s.game(ctx, who, id)
	if f, err := s.readFile(ctx, g, map[string]any{"path": "lib/tasot.ts"}); err != nil || f.(map[string]any)["text"] != "export const N = 3;\n" {
		t.Fatalf("written: %v %v", f, err)
	}
	if _, err := s.homeCall(ctx, who, "delete_file", map[string]any{"game_id": id, "path": "lib/tasot.ts"}); err != nil {
		t.Fatal(err)
	}
	// another account's game: not found, not written
	if _, err := s.homeCall(ctx, other, "write", map[string]any{"game_id": id, "path": "game.tsx", "text": "x"}); err == nil || !strings.Contains(err.Error(), "No such game") {
		t.Fatalf("another's game: %v", err)
	}
	if _, err := s.homeCall(ctx, who, "write", map[string]any{"game_id": id, "path": "../x.ts", "text": "x"}); err == nil {
		t.Fatal("a path out of the folder")
	}
	if !strings.Contains(gamesSourceHTML, `call("write"`) {
		t.Fatal("the page")
	}
}
