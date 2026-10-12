// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/terotests/sliqtly/mcp-go/figdv"
)

// a variation through the assistant's tools: made, changed, looked at,
// drawn, exported as a whole .fig, a view taken out; the original as it was
func TestSharedFigmaVariation(t *testing.T) {
	fig, err := os.ReadFile("testdata/health.fig")
	if err != nil {
		t.Fatal(err)
	}
	album := t.TempDir()
	os.WriteFile(album+"/Health App.fig", fig, 0o644)
	dirs, err := parseImportDirs(album)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 16)
	var sh *sharedFiles
	srv, session := startLocalWith(t, t.TempDir(), "", func(e *Env) {
		e.ImportDirs = dirs
		sh = e.shared
		e.shared.indexed = func(id string) { done <- id }
	})
	defer srv.Close()
	defer session.Close()
	s := &testServer{root: srv.URL, session: session}
	wait := func(want string) {
		t.Helper()
		select {
		case id := <-done:
			if id != want {
				t.Fatalf("read %s, want %s", id, want)
			}
		case <-time.After(60 * time.Second):
			t.Fatal("the file was never read")
		}
	}
	ok := func(name string, args map[string]any) map[string]any {
		t.Helper()
		r := call(t, s, name, args)
		if r.IsError {
			t.Fatalf("%s: %s", name, textOf(r))
		}
		return sc(r)
	}

	ok("add_shared_file", map[string]any{"path": album + "/Health App.fig"})
	wait("health-app")

	// an original is never changed
	refused := call(t, s, "edit_figma", map[string]any{"file_id": "health-app", "edits": []any{map[string]any{"op": "hide", "node": "1:5"}}})
	match(t, textOf(refused), `make a variation of it first`)

	v := ok("create_figma_variation", map[string]any{"file_id": "health-app", "name": "Tuesday"})
	eq(t, v["id"], "tuesday")
	eq(t, v["kind"], variationKind)
	eq(t, v["base"], "health-app")
	wait("tuesday")

	layers := ok("get_figma_layers", map[string]any{"file_id": "tuesday", "node_id": "1:6", "depth": 2})
	match(t, jsonOf(layers), `"text":"MONDAY, OCT 14"`)
	fields := ok("get_figma_layers", map[string]any{"file_id": "tuesday", "node_id": "1:7", "fields": true})
	match(t, jsonOf(fields), `"fontName"`)

	ed := ok("edit_figma", map[string]any{"file_id": "tuesday", "edits": []any{
		map[string]any{"op": "text", "node": "1:7", "text": "TUESDAY, OCT 15"},
		map[string]any{"op": "fill", "node": "1:3", "color": "#203040"},
		map[string]any{"op": "copy", "node": "1:9", "dy": 40},
		map[string]any{"op": "spin", "node": "1:9"},
	}})
	match(t, jsonOf(ed["refused"]), `unknown op`)
	eq(t, jsonOf(ed["created"]), `["9000:1"]`)
	match(t, ed["changes"].(string), `2 nodes changed, 1 copy`)
	wait("tuesday")

	// kept as the change alone, much smaller than the file
	delta, err := sh.files.Read(t.Context(), sharedVariationPath("tuesday"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta) > 2000 || !bytes.Contains(delta, []byte(`"TUESDAY, OCT 15"`)) {
		t.Fatalf("the variation: %d bytes %s", len(delta), delta)
	}
	got := ok("search_figma", map[string]any{"query": "tuesday", "file_id": "tuesday"})
	if len(got["hits"].([]any)) == 0 {
		t.Fatalf("the variation's index has not the new text: %s", jsonOf(got))
	}
	none := ok("search_figma", map[string]any{"query": "tuesday", "file_id": "health-app"})
	if len(none["hits"].([]any)) != 0 {
		t.Fatal("the original changed")
	}
	// drawn from the variation: a different picture than the original's
	a, err := sh.draw(t.Context(), "health-app", "1:2", 1, 512)
	if err != nil {
		t.Fatal(err)
	}
	b, err := sh.draw(t.Context(), "tuesday", "1:2", 1, 512)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("the variation draws as the original")
	}

	// exported: a whole .fig, with the change, that reads
	ex := ok("export_figma", map[string]any{"file_id": "tuesday"})
	res, err := http.Get(ex["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: %d %s", res.StatusCode, res.Header.Get("Content-Disposition"))
	}
	d := figdv.FigGo_static_open(body, "Tuesday.fig")
	if !figdv.FigGo_static_ok(d) {
		t.Fatalf("the export does not read: %s", figdv.FigGo_static_err(d))
	}
	if !strings.Contains(figdv.FigGo_static_indexJson(d), "TUESDAY") {
		t.Fatal("the export has not the change")
	}

	// a view taken out of the variation, as a file of its own
	one := ok("extract_figma_view", map[string]any{"file_id": "tuesday", "node_id": "1:2", "name": "Dashboard only"})
	eq(t, one["id"], "dashboard-only")
	wait("dashboard-only")
	ls := ok("list_shared_files", nil)
	match(t, jsonOf(ls), `"file_id":"dashboard-only"[^}]*"screens":1`)
	match(t, jsonOf(ls), `"base":"health-app","bytes":\d+,"changes":"2 nodes changed, 1 copy","file_id":"tuesday","kind":"figma-variation"`)

	// set back, the variation is empty again
	ok("edit_figma", map[string]any{"file_id": "tuesday", "edits": []any{map[string]any{"op": "revert"}}})
	wait("tuesday")
	delta, _ = sh.files.Read(t.Context(), sharedVariationPath("tuesday"), 1<<20)
	eq(t, string(delta), `{"format":"fig-delta/1"}`)

	// the original cannot go while a variation needs it
	if err := sh.remove(t.Context(), "health-app"); err == nil || !strings.Contains(err.Error(), "tuesday") {
		t.Fatalf("removed under its variation: %v", err)
	}
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
