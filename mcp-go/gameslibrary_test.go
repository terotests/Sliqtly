package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGamesWritesCopyFromTheLibrary(t *testing.T) {
	w, err := gamesWrites([]any{map[string]any{"path": "img/earth.jpg", "library": "textures/planets/earth.jpg"}})
	if err != nil || len(w) != 1 || w[0].library != "textures/planets/earth.jpg" || w[0].data != nil {
		t.Fatalf("writes %+v, %v", w, err)
	}
	for _, bad := range []map[string]any{
		{"path": "img/earth.png", "library": "textures/planets/earth.jpg"},
		{"path": "img/x.jpg", "library": "../secret.jpg"},
		{"path": "img/x.jpg", "library": "js/host.jpg"},
		{"path": "img/x.jpg", "library": "textures/planets/earth.jpg?x=1"},
	} {
		if _, err := gamesWrites([]any{bad}); err == nil {
			t.Errorf("%v was taken", bad)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/textures/planets/earth.jpg" {
			http.NotFound(rw, r)
			return
		}
		rw.Write([]byte("JPEG"))
	}))
	defer srv.Close()
	s := &gamesService{engine: newGamesEngine(srv.Client()), engineSrc: srv.URL + "/"}
	if err := s.fetchLibrary(context.Background(), w); err != nil || string(w[0].data) != "JPEG" {
		t.Fatalf("fetched %q, %v", w[0].data, err)
	}
	miss := []gamesWrite{{path: "img/m.jpg", library: "textures/planets/moon.jpg"}}
	if err := s.fetchLibrary(context.Background(), miss); err == nil {
		t.Fatal("a missing library file was taken")
	}
}

func TestGamesWritesCopyFromARangerGamesPack(t *testing.T) {
	pack := "RGPK1\n3\ngame.json\n2\n{}models/a.glb\n4\nGLB1sounds/hum.wav\n3\nWAV"
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/packs/marsiin.rgpk" {
			http.NotFound(rw, r)
			return
		}
		rw.Write([]byte(pack))
	}))
	defer srv.Close()
	w, err := gamesWrites([]any{
		map[string]any{"path": "models/a.glb", "library": "games/marsiin/models/a.glb"},
		map[string]any{"path": "sounds/hum.wav", "library": "games/marsiin/sounds/hum.wav"},
	})
	if err != nil || len(w) != 2 {
		t.Fatalf("writes %+v, %v", w, err)
	}
	s := &gamesService{engine: newGamesEngine(srv.Client()), engineSrc: srv.URL + "/"}
	if err := s.fetchLibrary(context.Background(), w); err != nil || string(w[0].data) != "GLB1" || string(w[1].data) != "WAV" {
		t.Fatalf("fetched %q %q, %v", w[0].data, w[1].data, err)
	}
	for _, bad := range []map[string]any{
		{"path": "models/a.glb", "library": "games/../x/a.glb"},
		{"path": "models/a.glb", "library": "games/marsiin/../../a.glb"},
		{"path": "models/a.wav", "library": "games/marsiin/models/a.glb"},
	} {
		if _, err := gamesWrites([]any{bad}); err == nil {
			t.Errorf("%v was taken", bad)
		}
	}
	for _, miss := range [][]gamesWrite{
		{{path: "models/b.glb", library: "games/marsiin/models/b.glb"}},
		{{path: "models/a.glb", library: "games/other/models/a.glb"}},
	} {
		if err := s.fetchLibrary(context.Background(), miss); err == nil {
			t.Errorf("%v was taken", miss[0].library)
		}
	}
	if _, ok := gamesPackFile([]byte("RGPK1\n1\nx\n99\nshort"), "x"); ok {
		t.Error("a cut pack was read")
	}
}
