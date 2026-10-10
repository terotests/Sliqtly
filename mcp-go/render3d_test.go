// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"testing"
)

// render_slide shows a program as the page does (its first frames run in
// the built page's engine) and draws its 3-D worlds without a GPU
// (src/Pres3DStill.rgr): here a red world over the left half of a blue
// program.
func TestRenderSlideDrawsProgramsAndWorlds(t *testing.T) {
	if _, err := os.Stat("../web/dist/cerxes.wasm"); err != nil {
		t.Skip("no web/dist/cerxes.wasm (npm run build with Rust's wasm32-wasip1 target)")
	}
	t.Setenv("SLIQTLY_CERXES_DIR", "../web/dist")
	cerxes = cerxesEngine{}
	defer func() { cerxes = cerxesEngine{} }()
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "# B\n\n## Play\n\n```app\nsrc: apps/w.tsx\nsize: 480x270\nallow: 3d\n```\n"
	prog := "function view() {\n  return (\n    <div className=\"s\">\n      <scene3d className=\"w\">\n" +
		"        <perspectiveCamera position={[0, 0, 3]} />\n" +
		"        <mesh><boxGeometry args={[3, 3, 3]} /><meshBasicMaterial color=\"#ff0000\" /></mesh>\n" +
		"      </scene3d>\n    </div>\n  );\n}\n"
	css := ".s { width: 480px; height: 270px; background-color: #0000ff }\n" +
		".w { position: absolute; left: 0px; top: 0px; width: 240px; height: 270px }\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "W", "markdown": md,
		"files": []any{map[string]any{"name": "w.tsx", "text": prog}, map[string]any{"name": "w.tsx.css", "text": css}}})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	r := call(t, s, "render_slide", map[string]any{"deck_id": sc(c)["deck_id"], "slide": 2})
	if r.IsError {
		t.Fatal(lastText(r))
	}
	img := decodeJPEG(t, r)
	red, blue := 0, 0
	minRed, maxRed, minBlue := 1<<30, -1, 1<<30
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 2 {
		for x := b.Min.X; x < b.Max.X; x += 2 {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			cr, cg, cb = cr>>8, cg>>8, cb>>8
			if cr > 200 && cg < 60 && cb < 60 {
				red++
				minRed, maxRed = min(minRed, x), max(maxRed, x)
			}
			if cb > 200 && cr < 60 && cg < 60 {
				blue++
				minBlue = min(minBlue, x)
			}
		}
	}
	if red < 2000 || blue < 2000 {
		t.Fatalf("want the red world beside the blue program, got %d red, %d blue points", red, blue)
	}
	if maxRed >= minBlue+8 || minRed > minBlue {
		t.Fatalf("the world is not on the program's left: red x %d..%d, blue from %d", minRed, maxRed, minBlue)
	}
}
