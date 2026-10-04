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
	h := &McpHost{images: map[int64][]byte{1: []byte(`<svg viewBox="0 0 200 120"/>`)}}
	if g := h.ImageGrid(1); len(g) != 2 || g[0] != 200 || g[1] != 120 {
		t.Errorf("ImageGrid of an SVG: %v", g)
	}
}
