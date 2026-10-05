// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "testing"

// An SVG has the size the player gives it (web/picture.js svgSize), so the
// layout gives it the same room: Tero's test deck's viewBox-only drawing
// took the whole slide on the server and pushed its caption on.
func TestSvgSize(t *testing.T) {
	for _, c := range []struct {
		src  string
		w, h int64
	}{
		{`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 120"><rect/></svg>`, 200, 120},
		{`<?xml version="1.0"?><!-- <svg width="9"> --><svg width="400" viewBox="0 0 200 100">`, 400, 200},
		{`<svg width="2in" height="1in">`, 192, 96},
		{`<svg>`, 300, 150},
	} {
		w, h, ok := svgSize([]byte(c.src))
		if !ok || w != c.w || h != c.h {
			t.Errorf("%s: got %d×%d %v, want %d×%d", c.src, w, h, ok, c.w, c.h)
		}
	}
	if _, _, ok := svgSize([]byte("\x89PNG")); ok {
		t.Error("a PNG is not an SVG")
	}
	// its own size, and the grid of its drawing: the contrast check reads
	// an SVG background as it reads a photo
	h := &McpHost{images: map[int64][]byte{
		1: []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 120"><rect width="200" height="120" fill="#102030"/></svg>`),
		2: []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 120"><rect</svg>`),
	}}
	g := h.ImageGrid(1)
	if len(g) != 2+gridSize*gridSize*4 || g[0] != 200 || g[1] != 120 {
		t.Fatalf("ImageGrid of an SVG: %d values, size %v", len(g), g[:min(2, len(g))])
	}
	if c := g[2+(24*gridSize+24)*4:][:4]; c[0] != 0x10 || c[1] != 0x20 || c[2] != 0x30 || c[3] != 255 {
		t.Errorf("middle cell %v, want the SVG's #102030", c)
	}
	// one that does not draw: its size alone
	if g := h.ImageGrid(2); len(g) != 2 || g[0] != 200 || g[1] != 120 {
		t.Errorf("ImageGrid of a broken SVG: %v", g)
	}
}

func TestSvgSizedTo(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 10" width="2" height="1"><g/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 10" width="40" height="20"><g/></svg>`},
		{`<?xml version="1.0"?><svg width="20" height='10'><g/></svg>`, `<?xml version="1.0"?><svg viewBox="0 0 20 10" width="40" height="20"><g/></svg>`},
		{`<svg viewBox="0 0 20 10" />`, `<svg viewBox="0 0 20 10" width="40" height="20"/>`},
	} {
		got, ok := svgSizedTo([]byte(c.src), 40, 20)
		if !ok || string(got) != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}
