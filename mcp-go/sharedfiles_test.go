// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// a shared file through the page's API: put, read once, drawn from its
// cuts, renamed, removed
func TestSharedFigma(t *testing.T) {
	fig, err := os.ReadFile("testdata/health.fig")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 4)
	var sh *sharedFiles
	srv, _ := startLocalWith(t, t.TempDir(), "", func(e *Env) {
		sh = e.shared
		e.shared.indexed = func(id string) { done <- id }
	})
	defer srv.Close()

	put := func(name string, body []byte) (int, map[string]any) {
		req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/shared/"+name, bytes.NewReader(body))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	wait := func(want string) {
		select {
		case id := <-done:
			if id != want {
				t.Fatalf("read %s, want %s", id, want)
			}
		case <-time.After(60 * time.Second):
			t.Fatal("the file was never read")
		}
	}

	if st, out := put("notes.txt", []byte("hello")); st != 400 || !strings.Contains(out["error"].(string), ".fig") {
		t.Fatalf("a text file: %d %v", st, out)
	}
	st, out := put("Health%20App.fig", fig)
	if st != 201 {
		t.Fatalf("put: %d %v", st, out)
	}
	eq(t, out["id"], "health-app")
	eq(t, out["name"], "Health App.fig")
	eq(t, out["status"], "indexing")
	wait("health-app")

	code, _, body := get(t, srv.URL+"/api/shared")
	if code != 200 {
		t.Fatal(code, body)
	}
	var list struct {
		Files []sharedFile `json:"files"`
	}
	json.Unmarshal([]byte(body), &list)
	if len(list.Files) != 1 {
		t.Fatalf("files: %s", body)
	}
	f := list.Files[0]
	if f.Status != "ready" || f.Screens != 3 || f.Pages != 1 || f.Bytes != int64(len(fig)) {
		t.Fatalf("card: %+v", f)
	}

	code, ct, ix := get(t, srv.URL+f.Index)
	if code != 200 || !strings.Contains(ct, "json") || !strings.Contains(ix, `"name":"Dashboard"`) {
		t.Fatalf("index: %d %s %.200s", code, ct, ix)
	}

	// a screen, then a part of it, at two pixels to one
	size := func(path string) (int, int) {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d %s", path, res.StatusCode, b)
		}
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return img.Bounds().Dx(), img.Bounds().Dy()
	}
	if w, h := size("/files/shared/health-app/png/1:2.png"); w != 804 || h != 2216 {
		t.Fatalf("Dashboard drawn %dx%d, want 804x2216", w, h)
	}
	if w, h := size("/files/shared/health-app/png/1:4.png?scale=1"); w != 362 || h != 92 {
		t.Fatalf("its header drawn %dx%d, want 362x92", w, h)
	}
	if w, h := size("/files/shared/health-app/png/1:147.png?max=200"); h != 200 || w < 85 || w > 95 {
		t.Fatalf("a screen held to 200 px: %dx%d", w, h)
	}
	// the second time it is a kept picture
	if w, _ := size("/files/shared/health-app/png/1:2.png"); w != 804 {
		t.Fatal("kept picture")
	}
	// a screen by its name, as figma:health-app/Dashboard draws it
	if w, h := size("/files/shared/health-app/png/Dashboard.png?scale=1"); w != 402 || h != 1108 {
		t.Fatalf("Dashboard by name drawn %dx%d, want 402x1108", w, h)
	}
	if code, _, body := get(t, srv.URL+"/files/shared/health-app/png/No%20such%20screen.png"); code != 404 || !strings.Contains(body, "no screen called") {
		t.Fatalf("an unknown name: %d %s", code, body)
	}
	if code, _, _ := get(t, srv.URL+"/files/shared/health-app/png/99:99.png"); code != 404 {
		t.Fatalf("an unknown layer: %d", code)
	}

	// a search reads the index once and keeps it
	rows, err := sh.indexes(t.Context())
	if err != nil || len(rows) != 1 || rows[0]["index"] == nil {
		t.Fatalf("indexes: %v %v", rows, err)
	}

	// the same name replaces it, and the file is read again
	if st, out := put("Health%20App.fig", fig); st != 201 || out["id"] != "health-app" {
		t.Fatalf("again: %d %v", st, out)
	}
	wait("health-app")

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/shared/health-app/rename", strings.NewReader(`{"name":"Health v2.fig"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("rename: %v %v", res, err)
	}
	res.Body.Close()

	// a file that is not a design fails the file, not the server
	if st, _ := put("broken.fig", []byte("PK not really a zip")); st != 201 {
		t.Fatal("broken put")
	}
	wait("broken")
	b, _ := sh.card(t.Context(), "broken")
	if b.Status != "failed" || b.Error == "" {
		t.Fatalf("broken: %+v", b)
	}

	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/api/shared/health-app", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 204 {
		t.Fatalf("delete: %v %v", res, err)
	}
	res.Body.Close()
	if code, _, _ := get(t, srv.URL+"/files/shared/health-app/png/1:2.png"); code != 404 {
		t.Fatalf("a removed file draws nothing: %d", code)
	}
	code, _, body = get(t, srv.URL+"/api/shared")
	if strings.Contains(body, "health-app") {
		t.Fatalf("still listed: %s", body)
	}
}

func TestSharedTooBig(t *testing.T) {
	srv, _ := startLocalWith(t, t.TempDir(), "", func(e *Env) { e.shared.max = 1000 })
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/shared/big.fig", bytes.NewReader(make([]byte, 2000)))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 413 {
		t.Fatalf("a file over the limit: %d", res.StatusCode)
	}
}

func TestSharedSlug(t *testing.T) {
	for in, want := range map[string]string{
		"App design.fig":            "app-design",
		"  Käyttöliittymä.fig":      "kayttoliittyma",
		"!!!.fig":                   "file",
		"Mobile/Web v2 (final).fig": "mobile-web-v2-final",
	} {
		eq(t, sharedSlug(in), want)
	}
}
