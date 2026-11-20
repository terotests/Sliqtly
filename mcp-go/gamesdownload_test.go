// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func gamesCatalogOf(t *testing.T, env *Env) []map[string]any {
	t.Helper()
	gamesCatalogForget()
	w := gamesServe(env, httptest.NewRequest("GET", gamesCatalogPath, nil))
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("catalog: %d %s", w.Code, w.Body.String())
	}
	var c struct {
		Format int
		Games  []map[string]any
	}
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil || c.Format != 1 {
		t.Fatalf("catalog %s", w.Body.String())
	}
	return c.Games
}

// Accepted Downloadable Game: asked by the owner, accepted by the admin,
// listed in the catalog, the accepted files as a numbered pack that later
// writes do not change until accepted again
func TestGamesDownloadable(t *testing.T) {
	env, db, _ := gamesEnv(t)
	ctx := context.Background()
	env.games.inviteOnly = false
	admin := gamesToken(db, "u1", "admin@example.com", gamesBase+gamesMcpPath)
	kid := gamesToken(db, "u2", "kid@example.com", gamesBase+gamesMcpPath)
	id := gamesMustTool(t, env, kid, "create_game", map[string]any{"name": "Hyppy"})["game_id"].(string)
	gamesMustTool(t, env, kid, "write_files", map[string]any{"game_id": id, "files": []any{
		map[string]any{"path": "game.json", "text": `{"name":"Hyppy","icon":"🐸","section":"seikkailu","description":"Hypi"}`},
		map[string]any{"path": "sounds/a.wav", "base64": "UklGRg=="},
	}})
	if c := gamesCatalogOf(t, env); len(c) != 0 {
		t.Fatalf("listed before asked %v", c)
	}

	// the owner asks; not listed until the admin accepts
	out := gamesMustTool(t, env, kid, "publish_game", map[string]any{"game_id": id, "downloadable": true})
	if out["published"] != false || out["downloadable"].(map[string]any)["state"] != "asked" || !strings.Contains(out["download_note"].(string), "admin") {
		t.Fatalf("asked %v", out)
	}
	if c := gamesCatalogOf(t, env); len(c) != 0 {
		t.Fatal("listed before accepted")
	}
	rows, err := env.games.adminCall(ctx, "downloads", nil)
	if err != nil {
		t.Fatal(err)
	}
	l := rows.(map[string]any)["games"].([]map[string]any)
	if len(l) != 1 || l[0]["game_id"] != id || l[0]["asked"] != true {
		t.Fatalf("admin rows %v", l)
	}
	if _, err := env.games.adminCall(ctx, "download_accept", map[string]any{"game_id": id}); err != nil {
		t.Fatal(err)
	}
	c := gamesCatalogOf(t, env)
	if len(c) != 1 || c[0]["id"] != id || c[0]["name"] != "Hyppy" || c[0]["icon"] != "🐸" || c[0]["section"] != "seikkailu" ||
		c[0]["version"] != float64(1) || c[0]["kind"] != "game" || c[0]["pack"] != gamesBase+"/games/d/"+id+"/1.rgpk" {
		t.Fatalf("catalog %v", c)
	}

	// the pack: the game's folder, all files (no lazy clips, no launcher)
	w := gamesServe(env, httptest.NewRequest("GET", "/games/d/"+id+"/1.rgpk", nil))
	if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("pack %d", w.Code)
	}
	files := gamesUnpack(t, w.Body.Bytes())
	if _, ok := files[id+"/sounds/a.wav"]; !ok || !strings.Contains(files[id+"/game.json"], "Hyppy") {
		t.Fatalf("pack files %v", files)
	}
	for p := range files {
		if !strings.HasPrefix(p, id+"/") {
			t.Fatalf("outside the folder: %s", p)
		}
	}

	// a later write: the catalog still offers the accepted files and blobs
	gamesMustTool(t, env, kid, "write_files", map[string]any{"game_id": id, "files": []any{
		map[string]any{"path": "game.json", "text": `{"name":"Hyppy 2"}`},
	}})
	gamesPacks.mu.Lock()
	gamesPacks.parts = map[string]*gamesPackPart{}
	gamesPacks.size = 0
	gamesPacks.mu.Unlock()
	w = gamesServe(env, httptest.NewRequest("GET", "/games/d/"+id+"/1.rgpk", nil))
	if w.Code != 200 || !strings.Contains(gamesUnpack(t, w.Body.Bytes())[id+"/game.json"], `"Hyppy"`) {
		t.Fatalf("accepted pack after a write: %d", w.Code)
	}
	// another's game is the admin page's to accept: version 2, version 1 gone
	if _, e := gamesTool(t, env, admin, "publish_game", map[string]any{"game_id": id, "downloadable": true}); !strings.Contains(e, "No such game") {
		t.Fatalf("admin publishing another's game: %q", e)
	}
	if _, err := env.games.adminCall(ctx, "download_accept", map[string]any{"game_id": id}); err != nil {
		t.Fatal(err)
	}
	c = gamesCatalogOf(t, env)
	if c[0]["version"] != float64(2) || c[0]["name"] != "Hyppy 2" {
		t.Fatalf("v2 %v", c)
	}
	if w := gamesServe(env, httptest.NewRequest("GET", "/games/d/"+id+"/1.rgpk", nil)); w.Code != 410 {
		t.Fatalf("old version: %d", w.Code)
	}
	if w := gamesServe(env, httptest.NewRequest("GET", "/games/d/"+id+"/2.rgpk", nil)); w.Code != 200 {
		t.Fatalf("v2 pack: %d", w.Code)
	}

	// hidden on the Games page: out of the list; withdrawn: out for good
	env.games.homeCall(ctx, gamesWho{uid: "u2", email: "kid@example.com"}, "hide", map[string]any{"game_id": id, "hidden": true})
	if len(gamesCatalogOf(t, env)) != 0 {
		t.Fatal("hidden but listed")
	}
	env.games.homeCall(ctx, gamesWho{uid: "u2", email: "kid@example.com"}, "hide", map[string]any{"game_id": id, "hidden": false})
	out = gamesMustTool(t, env, kid, "publish_game", map[string]any{"game_id": id, "downloadable": false})
	if out["downloadable"] != nil || len(gamesCatalogOf(t, env)) != 0 {
		t.Fatalf("withdrawn %v", out)
	}
	if w := gamesServe(env, httptest.NewRequest("GET", "/games/d/"+id+"/2.rgpk", nil)); w.Code != 404 {
		t.Fatalf("withdrawn pack: %d", w.Code)
	}
	for _, bad := range []string{"/games/d/" + id + "/x.rgpk", "/games/d/nope/1.rgpk", "/games/d/" + id + "/0.rgpk"} {
		if w := gamesServe(env, httptest.NewRequest("GET", bad, nil)); w.Code != 404 {
			t.Fatalf("%s: %d", bad, w.Code)
		}
	}
}

// an admin's own game is accepted by publish_game; an add-on is packed into
// its base game's folder; one without a base is refused
func TestGamesDownloadableAddon(t *testing.T) {
	env, db, _ := gamesEnv(t)
	admin := gamesToken(db, "u1", "admin@example.com", gamesBase+gamesMcpPath)
	id := gamesMustTool(t, env, admin, "create_game", map[string]any{"name": "Lisätasot"})["game_id"].(string)
	gamesMustTool(t, env, admin, "write_files", map[string]any{"game_id": id, "files": []any{
		map[string]any{"path": "game.json", "text": `{"name":"Lisätasot","kind":"addon"}`},
	}})
	if _, e := gamesTool(t, env, admin, "publish_game", map[string]any{"game_id": id, "downloadable": true}); !strings.Contains(e, "extends") {
		t.Fatalf("add-on without a base: %q", e)
	}
	gamesMustTool(t, env, admin, "write_files", map[string]any{"game_id": id, "files": []any{
		map[string]any{"path": "game.json", "text": `{"name":"Lisätasot","kind":"addon","extends":"pupu"}`},
		map[string]any{"path": "levels.json", "text": `[1,2]`},
	}})
	out := gamesMustTool(t, env, admin, "publish_game", map[string]any{"game_id": id, "downloadable": true})
	if out["downloadable"].(map[string]any)["state"] != "accepted" || out["published"] != false {
		t.Fatalf("admin accept %v", out)
	}
	c := gamesCatalogOf(t, env)
	if len(c) != 1 || c[0]["kind"] != "addon" || c[0]["extends"] != "pupu" {
		t.Fatalf("addon catalog %v", c)
	}
	w := gamesServe(env, httptest.NewRequest("GET", "/games/d/"+id+"/1.rgpk", nil))
	files := gamesUnpack(t, w.Body.Bytes())
	if files["pupu/addons/"+id+"/levels.json"] != "[1,2]" {
		t.Fatalf("addon pack %v", files)
	}
}
