package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// white, a red block and a black block
func blocksPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 60, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 60; x++ {
			c := color.RGBA{255, 255, 255, 255}
			if x >= 6 && x < 26 && y >= 8 && y < 32 {
				c = color.RGBA{255, 0, 0, 255}
			}
			if x >= 34 && x < 54 && y >= 8 && y < 32 {
				c = color.RGBA{0, 0, 0, 255}
			}
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestVectorizeADecksPicture(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Logo", "markdown": "# Logo\n\n![logo](media/logo.png)\n",
		"css":    "page { background-image: url(media/logo.png); }",
		"images": []any{map[string]any{"name": "logo.png", "data_base64": base64.StdEncoding.EncodeToString(blocksPNG(t))}},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	key := sc(c)["edit_key"]

	bad := call(t, s, "vectorize_image", map[string]any{"deck_id": id, "edit_key": key, "path": "media/logo.png", "options": map[string]any{"colorcount": 3}})
	match(t, textOf(bad), `Unknown option: colorcount`)
	missing := call(t, s, "vectorize_image", map[string]any{"deck_id": id, "edit_key": key, "path": "media/none.png"})
	match(t, textOf(missing), `has no file media/none\.png`)

	v := call(t, s, "vectorize_image", map[string]any{"deck_id": id, "edit_key": key, "path": "media/logo.png", "preset": "logo", "options": map[string]any{"colorCount": 3}})
	if v.IsError {
		t.Fatal(textOf(v))
	}
	match(t, textOf(v), `Vectorized media/logo\.png into media/logo\.svg: \d+ colour layers`)
	match(t, textOf(v), `1 in the Markdown, 1 in the theme CSS`)
	share := f.db.doc("shares/" + id)
	match(t, share["md"].(string), `!\[logo\]\(media/logo\.svg\)`)
	match(t, share["css"].(string), `url\(media/logo\.svg\)`)
	var paths []string
	for _, x := range list(share["files"]) {
		paths = append(paths, str(mapOf(x)["path"]))
	}
	eq(t, paths, []string{"media/logo.png", "media/logo.svg"})
	saved := f.bucket.saved["shares/"+id+"/media/logo.svg"]
	eq(t, saved.contentType, "image/svg+xml")
	if !strings.HasPrefix(string(saved.data), `<svg xmlns="http://www.w3.org/2000/svg" width="60" height="40" viewBox="0 0 60 40">`) {
		t.Fatalf("svg: %.120s", saved.data)
	}
	if !strings.Contains(strings.ToLower(string(saved.data)), `fill="#000000"`) {
		t.Fatalf("no black layer: %.400s", saved.data)
	}
}

func TestVectorizeAPictureGivenAsBase64(t *testing.T) {
	f := fakeFirebase()
	s := start(t, testEnv(&f, nil), "")
	defer s.close()
	r := call(t, s, "vectorize_image", map[string]any{"image_base64": "data:image/png;base64," + base64.StdEncoding.EncodeToString(blocksPNG(t)), "preset": "logo", "options": map[string]any{"maxSide": 30}})
	if r.IsError {
		t.Fatal(textOf(r))
	}
	out := sc(r)
	match(t, str(out["svg"]), `^<svg xmlns="http://www\.w3\.org/2000/svg" width="60" height="40" viewBox="0 0 30 20">`)
	eq(t, out["traced_width"], 30)
	if n, _ := out["layers"].(float64); n < 1 {
		t.Fatalf("layers %v", out["layers"])
	}
	match(t, textOf(r), `traced at 30x20 of 60x40`)
	none := call(t, s, "vectorize_image", map[string]any{})
	match(t, textOf(none), `Give deck_id and path, or one of image_base64 and image_url`)
}
