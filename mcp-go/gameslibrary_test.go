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
