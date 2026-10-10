// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
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
