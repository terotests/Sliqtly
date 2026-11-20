package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// write_files {path, url}: the server fetches the file (here a stand-in for
// the network), checks it and keeps it like base64 would be kept
func TestGamesWriteFromURL(t *testing.T) {
	env, db, _ := gamesEnv(t)
	tok := gamesToken(db, "u1", "admin@example.com", gamesBase+gamesMcpPath)
	id := gamesMustTool(t, env, tok, "create_game", map[string]any{"name": "Musa"})["game_id"].(string)
	was := gamesFetchURL
	defer func() { gamesFetchURL = was }()
	asked := ""
	gamesFetchURL = func(ctx context.Context, u string) ([]byte, error) {
		asked = u
		if strings.Contains(u, "missing") {
			return nil, errors.New("the address answered HTTP 404")
		}
		return []byte("ID3fake mp3"), nil
	}
	gamesMustTool(t, env, tok, "write_files", map[string]any{"game_id": id, "files": []any{
		map[string]any{"path": "music/song.mp3", "url": "https://example.com/a/song.mp3"},
	}})
	if asked != "https://example.com/a/song.mp3" {
		t.Fatalf("asked %q", asked)
	}
	f := gamesMustTool(t, env, tok, "read_file", map[string]any{"game_id": id, "path": "music/song.mp3"})
	if f["base64"] != "SUQzZmFrZSBtcDM=" {
		t.Fatalf("read %v", f)
	}
	for _, bad := range []map[string]any{
		{"path": "music/a.mp3", "url": "http://example.com/a.mp3"},
		{"path": "music/a.mp3", "url": "https://user:pw@example.com/a.mp3"},
		{"path": "music/a.exe", "url": "https://example.com/a.exe"},
		{"path": "music/a.mp3", "url": "https://example.com/missing.mp3"},
	} {
		if _, e := gamesTool(t, env, tok, "write_files", map[string]any{"game_id": id, "files": []any{bad}}); e == "" {
			t.Fatalf("%v went through", bad)
		}
	}
}

// the real fetcher: an HTML page is not taken for the file, nor an error
func TestGamesFetchURLChecks(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte("<html>"))
		case "/gone":
			http.NotFound(w, r)
		default:
			w.Header().Set("Content-Type", "audio/mpeg")
			w.Write([]byte("ID3"))
		}
	}))
	defer srv.Close()
	was := gamesURLClient
	defer func() { gamesURLClient = was }()
	gamesURLClient = srv.Client()
	ctx := context.Background()
	if b, err := gamesFetchURL(ctx, srv.URL+"/a.mp3"); err != nil || string(b) != "ID3" {
		t.Fatalf("file: %q %v", b, err)
	}
	if _, err := gamesFetchURL(ctx, srv.URL+"/page"); err == nil {
		t.Fatal("a page was taken")
	}
	if _, err := gamesFetchURL(ctx, srv.URL+"/gone"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("404: %v", err)
	}
	// the public client refuses the loopback test server
	gamesURLClient = newPublicClient()
	if _, err := gamesFetchURL(ctx, srv.URL+"/a.mp3"); err == nil {
		t.Fatal("loopback fetched")
	}
}
