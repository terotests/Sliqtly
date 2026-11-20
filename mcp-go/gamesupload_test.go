package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGamesUploadURL(t *testing.T) {
	env, db, _ := gamesEnv(t)
	tok := gamesToken(db, "u1", "admin@example.com", gamesBase+gamesMcpPath)
	g := gamesMustTool(t, env, tok, "create_game", map[string]any{"name": "Iso"})
	id := g["game_id"].(string)
	up := gamesMustTool(t, env, tok, "upload_url", map[string]any{"game_id": id})
	u, _ := up["url"].(string)
	if !strings.HasPrefix(u, gamesBase+"/games/u/") {
		t.Fatalf("upload_url %v", up)
	}
	path := strings.TrimPrefix(u, gamesBase)
	put := func(p, body string) int {
		return gamesServe(env, httptest.NewRequest("PUT", path+"/"+p, strings.NewReader(body))).Code
	}
	if c := put("img/map.jpg", "\xff\xd8JPEG"); c != 200 {
		t.Fatalf("put %d", c)
	}
	if c := put("img/../x.jpg", "x"); c != 400 {
		t.Fatalf("bad path %d", c)
	}
	if c := put("page.html", "x"); c != 400 {
		t.Fatalf("bad type %d", c)
	}
	if c := gamesServe(env, httptest.NewRequest("GET", path+"/img/map.jpg", nil)).Code; c != 405 {
		t.Fatalf("get %d", c)
	}
	r := gamesMustTool(t, env, tok, "read_file", map[string]any{"game_id": id, "path": "img/map.jpg"})
	if r["base64"] != "/9hKUEVH" {
		t.Fatalf("read %v", r)
	}
	// a made-up token and an ended one are refused
	if c := gamesServe(env, httptest.NewRequest("PUT", "/games/u/"+strings.Repeat("a", 32)+"/img/a.jpg", strings.NewReader("x"))).Code; c != 400 {
		t.Fatalf("unknown token %d", c)
	}
	now := env.games.now
	env.games.now = func() time.Time { return now().Add(time.Hour) }
	if c := put("img/late.jpg", "x"); c != 400 {
		t.Fatalf("ended token %d", c)
	}
}
