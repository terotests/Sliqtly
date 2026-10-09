// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"image"
	"testing"
)

// The deck's own effects are drawn by render_slide, at their still or at
// the time asked, their clock with them; fx: false leaves them out.
func TestRenderSlideDrawsOwnEffects(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	deck := "```fx\neffect dawn source {\n  fallback = #000000\n" +
		"  output = vec3(progress, 0, step / max(steps, 1))\n}\n" +
		"effect veil backdrop {\n  output = rgba(vec3(0, 1, 0), 0.5)\n}\n" +
		"effect grain source {\n  output = vec3(fbm(uv * 8, 4), voronoi(uv * 5), hash(p))\n}\n```\n\n" +
		"## Dawn {fx=dawn}\n\n- one\n- two\n{.build}\n\n## Veil {fx=veil}\n\nText\n\n## Grain {fx=grain}\n\nText\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Fx", "markdown": deck})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	nomatch(t, textOf(c), `fx`+"`"+`|effect.*line \d`)
	id := sc(c)["deck_id"]
	corner := func(img image.Image) (uint32, uint32, uint32) {
		r, g, b, _ := img.At(20, 520).RGBA()
		return r >> 8, g >> 8, b >> 8
	}

	// at its still: the slide's end, progress 1 and the last step
	r := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": 1})
	if r.IsError {
		t.Fatal(lastText(r))
	}
	cr, cg, cb := corner(decodeJPEG(t, r))
	if cr < 230 || cg > 30 || cb < 230 {
		t.Fatalf("dawn at its still is %d,%d,%d, want magenta", cr, cg, cb)
	}
	match(t, lastText(r), `drawn at their still`)

	// at 0 s: before the first step, progress 0
	r0 := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": 1, "time": 0})
	cr, cg, cb = corner(decodeJPEG(t, r0))
	if cr > 30 || cb > 30 {
		t.Fatalf("dawn at 0 s is %d,%d,%d, want black", cr, cg, cb)
	}
	match(t, lastText(r0), `drawn 0(\.0)? s into the slide`)

	// left out: the fallback shows
	off := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": 1, "fx": false})
	cr, _, cb = corner(decodeJPEG(t, off))
	if cr > 30 || cb > 30 {
		t.Fatalf("dawn left out is %d,%d,%d, want its black fallback", cr, cg, cb)
	}

	// a backdrop covers what is behind it by its alpha
	v := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": 2})
	cr, cg, cb = corner(decodeJPEG(t, v))
	if cg < 100 || cg > 220 || cr > 200 {
		t.Fatalf("veil is %d,%d,%d, want half green over the page", cr, cg, cb)
	}

	// noise is drawn as noise
	g := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": "grain"})
	img := decodeJPEG(t, g)
	seen := map[[3]uint32]bool{}
	for y := 300; y < 530; y += 9 {
		for x := 10; x < 950; x += 9 {
			r, g, b, _ := img.At(x, y).RGBA()
			seen[[3]uint32{r >> 12, g >> 12, b >> 12}] = true
		}
	}
	if len(seen) < 40 {
		t.Fatalf("grain has %d colours", len(seen))
	}

	bad := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": 1, "time": -1})
	if !bad.IsError {
		t.Fatal("a negative time was taken")
	}
}

// The VM's library is FxLang's GLSL: hash and noise stay in 0..1, fbm with
// one octave is half a noise.
func TestFxLibrary(t *testing.T) {
	for i := 0; i < 200; i++ {
		x, y := float64(i)*0.37-20, float64(i)*1.13+3
		h, n := fxHash(x, y), fxNoise(x, y)
		if h < 0 || h >= 1 || n < 0 || n > 1 {
			t.Fatalf("hash %v noise %v at %v,%v", h, n, x, y)
		}
	}
	if v := fxVoronoi(0.5, 0.5); v < 0 || v > 1.5 {
		t.Fatalf("voronoi %v", v)
	}
}
