// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
	"image/png"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/terotests/sliqtly/mcp-go/pptxgo"
)

// a PowerPoint deck among the shared files: added from an import folder,
// listed, searched, read slide by slide and as a style guide, drawn (a
// slide, a shape, on a Sliqtly slide), changed in place and downloaded
func TestSharedPptx(t *testing.T) {
	deck, err := os.ReadFile("testdata/business.pptx")
	if err != nil {
		t.Fatal(err)
	}
	album := t.TempDir()
	os.WriteFile(album+"/Q3 Review.pptx", deck, 0o644)
	dirs, err := parseImportDirs(album)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 4)
	srv, session := startLocalWith(t, t.TempDir(), "", func(e *Env) {
		e.ImportDirs = dirs
		e.shared.indexed = func(id string) { done <- id }
	})
	defer srv.Close()
	defer session.Close()
	s := &testServer{root: srv.URL, session: session}

	match(t, textOf(call(t, s, "sliqtly_guide", map[string]any{"topic": "powerpoint"})), `pptx:<file_id>/<slide>`)
	add := call(t, s, "add_shared_file", map[string]any{"path": album + "/Q3 Review.pptx"})
	if add.IsError {
		t.Fatal(textOf(add))
	}
	eq(t, sc(add)["id"], "q3-review")
	eq(t, sc(add)["kind"], "pptx")
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the deck was never read")
	}

	ls := call(t, s, "list_shared_files", nil)
	match(t, textOf(ls), `"kind": "pptx"`)
	match(t, textOf(ls), `"status": "ready"`)
	match(t, textOf(ls), `"screens": 5`)

	// a slide is found by its words, and its markdown is a pptx: picture
	found := call(t, s, "search_figma", map[string]any{"query": "agenda"})
	hits := sc(found)["hits"].([]any)
	if len(hits) == 0 {
		t.Fatalf("no hits: %s", textOf(found))
	}
	eq(t, hits[0].(map[string]any)["markdown"], "![Agenda](pptx:q3-review/2)")

	sl := call(t, s, "get_pptx_slide", map[string]any{"file_id": "q3-review", "slide": 2})
	if sl.IsError {
		t.Fatal(textOf(sl))
	}
	eq(t, sc(sl)["title"], "Agenda")
	eq(t, sc(sl)["markdown"], "![Agenda](pptx:q3-review/2)")
	match(t, sc(sl)["markdown_text"].(string), `^# Agenda\n\n1\. Highlights\n`)
	match(t, textOf(sl), `"placeholder": "title"`)
	if len(sl.Content) != 2 {
		t.Fatalf("no picture: %d parts", len(sl.Content))
	}
	pic := sl.Content[1].(*mcp.ImageContent)
	if im, err := png.Decode(bytes.NewReader(pic.Data)); err != nil || im.Bounds().Dx() != 2048 || im.Bounds().Dy() != 1152 {
		t.Fatalf("the slide drawn: %v", err)
	}
	match(t, textOf(call(t, s, "get_pptx_slide", map[string]any{"file_id": "q3-review", "slide": 9})), `slides 1–5`)

	st := call(t, s, "get_pptx_style", map[string]any{"file_id": "q3-review"})
	if st.IsError {
		t.Fatal(textOf(st))
	}
	match(t, textOf(st), `"accent1": "#4472C4"`)
	match(t, textOf(st), `"heading_font": "Calibri Light"`)
	match(t, textOf(st), `"title_styles": \[\s*\{\s*"font": "Calibri"`)

	// a slide and one shape on it, drawn at one pixel to one
	size := func(path string) (int, int) {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d", path, res.StatusCode)
		}
		im, err := png.Decode(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return im.Bounds().Dx(), im.Bounds().Dy()
	}
	if w, h := size("/files/shared/q3-review/png/1.png?scale=1"); w != 1280 || h != 720 {
		t.Fatalf("slide 1 drawn %dx%d", w, h)
	}
	if w, h := size("/files/shared/q3-review/png/1:2.png?scale=1"); w != 1088 || h != 144 {
		t.Fatalf("its title drawn %dx%d", w, h)
	}
	if w, _ := size("/files/shared/q3-review/png/Agenda.png?scale=1"); w != 1280 {
		t.Fatal("a slide by its title")
	}

	// on a Sliqtly slide, drawn by the server: a 16:9 picture
	c := call(t, s, "create_presentation", map[string]any{"title": "From PowerPoint", "markdown": "# Review\n\n## Agenda\n\n![Agenda](pptx:q3-review/2)\n"})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	dl := call(t, s, "get_display_list", map[string]any{"deck_id": sc(c)["deck_id"], "slide": 2})
	m := regexp.MustCompile(`image-1 image @ [\d.]+,[\d.]+ ([\d.]+)x([\d.]+)`).FindStringSubmatch(textOf(dl))
	if m == nil {
		t.Fatalf("no picture on the slide: %s", textOf(dl))
	}
	var w, h float64
	fmt.Sscan(m[1], &w)
	fmt.Sscan(m[2], &h)
	if r := w / h; r < 1.7 || r > 1.85 {
		t.Fatalf("the picture is %vx%v, not the slide's shape", w, h)
	}

	// a deck is not a Figma file to vary or export
	vary := call(t, s, "create_figma_variation", map[string]any{"file_id": "q3-review"})
	match(t, textOf(vary), `is a PowerPoint deck: .*edit_pptx`)
	exp := call(t, s, "export_figma", map[string]any{"file_id": "q3-review"})
	match(t, textOf(exp), `is a PowerPoint deck`)

	// changed in place: a title, then a slide taken out
	bad := call(t, s, "edit_pptx", map[string]any{"file_id": "q3-review", "edits": []any{map[string]any{"slide": 1, "shape": "0", "text": "x"}}})
	match(t, textOf(bad), `comes from the slide's layout`)
	ed := call(t, s, "edit_pptx", map[string]any{"file_id": "q3-review", "edits": []any{
		map[string]any{"slide": 2, "shape": "2", "text": "Mitä tänään"},
		map[string]any{"slide": 1, "remove": true},
	}})
	if ed.IsError {
		t.Fatal(textOf(ed))
	}
	match(t, textOf(ed), `"markdown_text": "# Mitä tänään[^"]*",\s*"slide": 1`)
	ls = call(t, s, "list_shared_files", nil)
	match(t, textOf(ls), `"screens": 4`)

	// the deck downloaded is the changed one, and reads
	res, err := http.Get(srv.URL + "/files/shared/q3-review/source")
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	got.ReadFrom(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Disposition"), "Q3%20Review.pptx") {
		t.Fatalf("download: %d %v", res.StatusCode, res.Header)
	}
	d := pptxgo.PptxGo_static_open(got.Bytes(), "")
	if !pptxgo.PptxGo_static_ok(d) || pptxgo.PptxGo_static_slideCount(d) != 4 || !strings.Contains(pptxgo.PptxGo_static_indexJson(d), `"name":"Mitä tänään"`) {
		t.Fatalf("the saved deck: %s", pptxgo.PptxGo_static_err(d))
	}
	match(t, textOf(ed), `edit_pptx with undo: true`)

	// taken back: the deck as it was before the edit, its five slides read
	// again; nothing older is kept, so a second undo says so
	un := call(t, s, "edit_pptx", map[string]any{"file_id": "q3-review", "undo": true})
	if un.IsError {
		t.Fatal(textOf(un))
	}
	match(t, textOf(un), `"undo_left": 0`)
	ls = call(t, s, "list_shared_files", nil)
	match(t, textOf(ls), `"screens": 5`)
	if sl := textOf(call(t, s, "get_pptx_slide", map[string]any{"file_id": "q3-review", "slide": 2})); strings.Contains(sl, "Mitä tänään") {
		t.Fatal("undo left the edit in")
	}
	match(t, textOf(call(t, s, "edit_pptx", map[string]any{"file_id": "q3-review", "undo": true})), `no earlier version`)
	both := call(t, s, "edit_pptx", map[string]any{"file_id": "q3-review", "undo": true, "edits": []any{map[string]any{"slide": 1, "remove": true}}})
	match(t, textOf(both), `either edits or undo`)

	// only the last pptxKeep versions are kept; undo walks back through them
	for i := range pptxKeep + 2 {
		e := call(t, s, "edit_pptx", map[string]any{"file_id": "q3-review", "edits": []any{map[string]any{"slide": 2, "shape": "2", "text": fmt.Sprintf("Round %d", i)}}})
		if e.IsError {
			t.Fatal(textOf(e))
		}
	}
	for want := pptxKeep + 1; want > 1; want-- {
		u := call(t, s, "edit_pptx", map[string]any{"file_id": "q3-review", "undo": true})
		if u.IsError {
			t.Fatal(textOf(u))
		}
		match(t, textOf(call(t, s, "get_pptx_slide", map[string]any{"file_id": "q3-review", "slide": 2})), fmt.Sprintf("Round %d", want-1))
	}
	match(t, textOf(call(t, s, "edit_pptx", map[string]any{"file_id": "q3-review", "undo": true})), `no earlier version`)
}
