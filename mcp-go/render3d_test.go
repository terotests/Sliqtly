// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"image"
	"os"
	"regexp"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// render_slide shows a program as the page does (its first frames run in
// the built page's engine) and draws its 3-D worlds without a GPU
// (src/Pres3DStill.rgr): here a red world in the left half of a blue
// program. With overflow: hidden the world stays in its element; without
// it reaches past the program's box onto the slide, under the slide's text.
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
	render := func(overflow string) (img image.Image) {
		css := ".s { width: 480px; height: 270px; background-color: #0000ff }\n" +
			".w { position: absolute; left: 0px; top: 0px; width: 240px; height: 270px" + overflow + " }\n"
		c := call(t, s, "create_presentation", map[string]any{"title": "W", "markdown": md,
			"files": []any{map[string]any{"name": "w.tsx", "text": prog}, map[string]any{"name": "w.tsx.css", "text": css}}})
		if c.IsError {
			t.Fatal(textOf(c))
		}
		r := call(t, s, "render_slide", map[string]any{"deck_id": sc(c)["deck_id"], "slide": 2})
		if r.IsError {
			t.Fatal(lastText(r))
		}
		return decodeJPEG(t, r)
	}
	type spread struct{ red, blue, minRed, maxRed, minBlue, maxBlue, redOverTitle int }
	measure := func(img image.Image) spread {
		p := spread{minRed: 1 << 30, maxRed: -1, minBlue: 1 << 30, maxBlue: -1}
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y += 2 {
			for x := b.Min.X; x < b.Max.X; x += 2 {
				cr, cg, cb, _ := img.At(x, y).RGBA()
				cr, cg, cb = cr>>8, cg>>8, cb>>8
				if cr > 200 && cg < 60 && cb < 60 {
					p.red++
					p.minRed, p.maxRed = min(p.minRed, x), max(p.maxRed, x)
				}
				if cb > 200 && cr < 60 && cg < 60 {
					p.blue++
					p.minBlue, p.maxBlue = min(p.minBlue, x), max(p.maxBlue, x)
				}
				// the title "Play" (white) where the world is drawn: still white
				if y < b.Max.Y/5 && x < b.Max.X/4 && cr > 220 && cg > 220 && cb > 220 {
					p.redOverTitle++
				}
			}
		}
		return p
	}
	in := measure(render("; overflow: hidden"))
	if in.red < 2000 || in.blue < 2000 {
		t.Fatalf("want the red world beside the blue program, got %d red, %d blue points", in.red, in.blue)
	}
	if in.maxRed >= in.minBlue+8 || in.minRed > in.minBlue {
		t.Fatalf("the world is not on the program's left: red x %d..%d, blue from %d", in.minRed, in.maxRed, in.minBlue)
	}
	out := measure(render(""))
	if out.minRed >= in.minRed-8 {
		t.Fatalf("the world did not reach past the program's box: red from x %d, the box from %d", out.minRed, in.minRed)
	}
	if out.redOverTitle < 20 {
		t.Fatalf("the slide's title is not over the world (%d white points)", out.redOverTitle)
	}
}

// export_presentation format gltf writes the program's world as glTF 2.0:
// the box the program draws, its red in linear light, and the camera.
func TestExportGltfWritesTheWorld(t *testing.T) {
	if _, err := os.Stat("../web/dist/cerxes.wasm"); err != nil {
		t.Skip("no web/dist/cerxes.wasm (npm run build with Rust's wasm32-wasip1 target)")
	}
	t.Setenv("SLIQTLY_CERXES_DIR", "../web/dist")
	cerxes = cerxesEngine{}
	defer func() { cerxes = cerxesEngine{} }()
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "# B\n\n## Play\n\n```app\nsrc: apps/w.tsx\nsize: 480x270\nallow: 3d\n```\n\n## Plain\n\nNo world here.\n"
	prog := "function view() {\n  return (\n    <scene3d>\n" +
		"      <perspectiveCamera position={[0, 0, 3]} fov={50} />\n" +
		"      <mesh name=\"crate\"><boxGeometry args={[2, 1, 1]} /><meshStandardMaterial color=\"#ff0000\" roughness={0.3} /></mesh>\n" +
		"      <directionalLight position={[1, 2, 3]} />\n" +
		"    </scene3d>\n  );\n}\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "W", "markdown": md,
		"files": []any{map[string]any{"name": "w.tsx", "text": prog}}})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	x := call(t, s, "export_presentation", map[string]any{"deck_id": id, "format": "gltf"})
	if x.IsError {
		t.Fatal(textOf(x))
	}
	match(t, textOf(x), `3-D model \(glTF\) of "W" \(slide 2, apps/w.tsx`)
	saved := f.bucket.saved["shares/"+id+"/exports/W (slide 2).gltf"]
	eq(t, saved.contentType, "model/gltf+json")
	var g struct {
		Asset struct{ Version string }
		Nodes []struct {
			Name   string
			Mesh   *int
			Camera *int
		}
		Accessors []struct {
			Count int
			Max   []float64
		}
		Materials []struct {
			PbrMetallicRoughness struct {
				BaseColorFactor []float64
				RoughnessFactor float64
			}
		}
		Cameras []struct {
			Perspective struct{ AspectRatio, Yfov float64 }
		}
	}
	if err := json.Unmarshal(saved.data, &g); err != nil {
		t.Fatal(err)
	}
	eq(t, g.Asset.Version, "2.0")
	if len(g.Nodes) != 3 || g.Nodes[0].Name != "crate" || g.Nodes[0].Mesh == nil || g.Nodes[2].Camera == nil {
		t.Fatalf("want the crate, the sun and the camera, got %+v", g.Nodes)
	}
	if g.Accessors[0].Count != 24 || g.Accessors[0].Max[0] != 1 {
		t.Fatalf("want Three's 2 x 1 x 1 box, got %+v", g.Accessors[0])
	}
	m := g.Materials[0].PbrMetallicRoughness
	if m.BaseColorFactor[0] != 1 || m.BaseColorFactor[1] != 0 || m.RoughnessFactor != 0.3 {
		t.Fatalf("want the red material, got %+v", m)
	}
	if a := g.Cameras[0].Perspective.AspectRatio; a < 1.77 || a > 1.78 {
		t.Fatalf("want the world's 16:9 box, got %v", a)
	}
	// a slide with no world says so
	none := call(t, s, "export_presentation", map[string]any{"deck_id": id, "format": "gltf", "slides": []any{3}})
	if !none.IsError {
		t.Fatal("a slide with no world exported a model")
	}
	match(t, textOf(none), `No 3-D world on slide 3`)
}

// <SliqGltf src> reads a .gltf the deck keeps: a world exported as glTF and
// sent back with files is drawn again by render_slide, a named child
// changes its object, and a file the deck lacks is said.
func TestSliqGltfReadsTheDecksModel(t *testing.T) {
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
	world := func(inner string) string {
		return "function view() {\n  return (\n    <scene3d>\n" +
			"      <perspectiveCamera position={[0, 0, 3]} />\n" + inner + "\n    </scene3d>\n  );\n}\n"
	}
	first := call(t, s, "create_presentation", map[string]any{"title": "M", "markdown": md,
		"files": []any{map[string]any{"name": "w.tsx", "text": world(
			"<mesh name=\"crate\"><boxGeometry args={[3, 3, 3]} /><meshBasicMaterial color=\"#e8741e\" /></mesh>")}}})
	if first.IsError {
		t.Fatal(textOf(first))
	}
	id := sc(first)["deck_id"].(string)
	if x := call(t, s, "export_presentation", map[string]any{"deck_id": id, "format": "gltf"}); x.IsError {
		t.Fatal(textOf(x))
	}
	model := string(f.bucket.saved["shares/"+id+"/exports/M (slide 2).gltf"].data)
	count := func(r *mcp.CallToolResult, want func(cr, cg, cb uint32) bool) int {
		img := decodeJPEG(t, r)
		n := 0
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y += 2 {
			for x := b.Min.X; x < b.Max.X; x += 2 {
				cr, cg, cb, _ := img.At(x, y).RGBA()
				if want(cr>>8, cg>>8, cb>>8) {
					n++
				}
			}
		}
		return n
	}
	orange := func(cr, cg, cb uint32) bool { return cr > 200 && cg > 80 && cg < 160 && cb < 70 }
	green := func(cr, cg, cb uint32) bool { return cg > 200 && cr < 60 && cb < 60 }
	again := call(t, s, "create_presentation", map[string]any{"title": "N", "markdown": md,
		"files": []any{
			map[string]any{"name": "w.tsx", "text": world("<SliqGltf src=\"data/m.gltf\" />")},
			map[string]any{"name": "m.gltf", "text": model},
		}})
	if again.IsError {
		t.Fatal(textOf(again))
	}
	r := call(t, s, "render_slide", map[string]any{"deck_id": sc(again)["deck_id"], "slide": 2})
	if r.IsError {
		t.Fatal(lastText(r))
	}
	if n := count(r, orange); n < 2000 {
		t.Fatalf("want the model's orange box drawn, got %d orange points", n)
	}
	// the viewer's fixture (scripts/check-view.mjs): this deck, its model
	// served beside it
	if os.Getenv("SLIQTLY_WRITE_FIXTURES") != "" {
		one := call(t, s, "create_presentation", map[string]any{"title": "N", "markdown": "## Play\n\n```app\nsrc: apps/w.tsx\nsize: 480x270\nallow: 3d\n```\n",
			"files": []any{
				map[string]any{"name": "w.tsx", "text": world("<SliqGltf src=\"data/m.gltf\" />")},
				map[string]any{"name": "m.gltf", "text": model},
			}})
		_, _, _, vb := getView(t, s.root+"/api/view/"+sc(one)["deck_id"].(string))
		var v map[string]any
		if err := json.Unmarshal([]byte(vb), &v); err != nil {
			t.Fatal(err)
		}
		for _, f := range v["deck"].(map[string]any)["files"].([]any) {
			if m := f.(map[string]any); m["path"] == "data/m.gltf" {
				m["url"] = "/fixture/view-gltf.gltf"
			}
		}
		out, _ := json.Marshal(v)
		if err := os.WriteFile("../scripts/fixtures/view-gltf.json", out, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("../scripts/fixtures/view-gltf.gltf", []byte(model), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// a named child changes the file's object
	over := call(t, s, "create_presentation", map[string]any{"title": "O", "markdown": md,
		"files": []any{
			map[string]any{"name": "w.tsx", "text": world(
				"<SliqGltf src=\"data/m.gltf\"><mesh name=\"crate\"><meshBasicMaterial color=\"#00ff00\" /></mesh></SliqGltf>")},
			map[string]any{"name": "m.gltf", "text": model},
		}})
	if over.IsError {
		t.Fatal(textOf(over))
	}
	r2 := call(t, s, "render_slide", map[string]any{"deck_id": sc(over)["deck_id"], "slide": 2})
	if r2.IsError {
		t.Fatal(lastText(r2))
	}
	if n, m := count(r2, green), count(r2, orange); n < 2000 || m > 0 {
		t.Fatalf("want the crate green, got %d green, %d orange points", n, m)
	}
	// a file the deck does not have
	lost := call(t, s, "create_presentation", map[string]any{"title": "L", "markdown": md,
		"files": []any{map[string]any{"name": "w.tsx", "text": world("<SliqGltf src=\"data/none.gltf\" />")}}})
	if lost.IsError {
		t.Fatal(textOf(lost))
	}
	r3 := call(t, s, "export_presentation", map[string]any{"deck_id": sc(lost)["deck_id"], "format": "gltf"})
	match(t, textOf(r3), `the deck has no file data/none.gltf`)
}

// A file written for Ranger v2's 3-D façade runs on a slide as it is:
// courtyard_live.tsx (import * as THREE from "ranger:three", init, tick in
// milliseconds) drawn by render_slide, its sandstone floor and coloured
// boxes as Ranger draws them.
func TestRangerThreeFileRunsAsItIs(t *testing.T) {
	if _, err := os.Stat("../web/dist/cerxes.wasm"); err != nil {
		t.Skip("no web/dist/cerxes.wasm (npm run build with Rust's wasm32-wasip1 target)")
	}
	t.Setenv("SLIQTLY_CERXES_DIR", "../web/dist")
	cerxes = cerxesEngine{}
	defer func() { cerxes = cerxesEngine{} }()
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	prog, err := os.ReadFile("../scripts/fixtures/courtyard_live.tsx")
	if err != nil {
		t.Fatal(err)
	}
	md := "## Courtyard\n\n```app\nsrc: apps/courtyard.tsx\nsize: 480x270\nallow: 3d\n```\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "C", "markdown": md,
		"files": []any{map[string]any{"name": "courtyard.tsx", "text": string(prog)}}})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	if txt := textOf(c); regexp.MustCompile(`(?i)courtyard\.tsx[^\n]*(error|not|fail)`).MatchString(txt) {
		t.Fatalf("the program was not taken: %s", txt)
	}
	// the viewer's fixture (scripts/check-view.mjs)
	if os.Getenv("SLIQTLY_WRITE_FIXTURES") != "" {
		_, _, _, vb := getView(t, s.root+"/api/view/"+sc(c)["deck_id"].(string))
		if err := os.WriteFile("../scripts/fixtures/view-three.json", []byte(vb), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := call(t, s, "render_slide", map[string]any{"deck_id": sc(c)["deck_id"], "slide": 1})
	if r.IsError {
		t.Fatal(lastText(r))
	}
	img := decodeJPEG(t, r)
	sand, colours := 0, map[string]bool{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 2 {
		for x := b.Min.X; x < b.Max.X; x += 2 {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			cr, cg, cb = cr>>8, cg>>8, cb>>8
			// the sandstone floor under four lights: bright, a little warm
			if cr > 200 && cg > 200 && cb > 150 && cb+10 < cr {
				sand++
			}
			switch {
			case cg > cr+40 && cg > cb+20:
				colours["green"] = true
			case cb > cr+40 && cb > cg:
				colours["blue"] = true
			case cr > cg+60 && cr > cb+60:
				colours["red"] = true
			}
		}
	}
	if sand < 1500 || len(colours) < 3 {
		t.Fatalf("want the floor and the coloured boxes, got %d floor points and %v", sand, colours)
	}
}

// export_presentation pdf and pptx run the program as render_slide does and
// carry its 3-D world as a picture (Check.worldPictures), not the fence's
// plate; the PDF keeps the world's see-through pixels in a soft mask.
func TestExportCarriesTheWorld(t *testing.T) {
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
	prog := "function view() {\n  return (\n    <scene3d>\n" +
		"      <perspectiveCamera position={[0, 0, 3]} />\n" +
		"      <mesh><sphereGeometry args={[0.5]} /><meshBasicMaterial color=\"#ff0000\" /></mesh>\n" +
		"    </scene3d>\n  );\n}\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "W", "markdown": md,
		"files": []any{map[string]any{"name": "w.tsx", "text": prog}}})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	p := call(t, s, "export_presentation", map[string]any{"deck_id": id, "format": "pdf"})
	if p.IsError {
		t.Fatal(textOf(p))
	}
	pdf := f.bucket.saved["shares/"+id+"/exports/W.pdf"].data
	if !regexp.MustCompile(`/SMask \d+ 0 R`).Match(pdf) {
		t.Fatal("the PDF has no world with see-through pixels (no /SMask)")
	}
	x := call(t, s, "export_presentation", map[string]any{"deck_id": id, "format": "pptx"})
	if x.IsError {
		t.Fatal(textOf(x))
	}
	pptx := f.bucket.saved["shares/"+id+"/exports/W.pptx"].data
	if !bytes.Contains(pptx, []byte("ppt/media/")) {
		t.Fatal("the PPTX has no picture of the world")
	}
}
