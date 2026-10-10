// SPDX-License-Identifier: AGPL-3.0-or-later

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

// `page { background-image: url(media/bg.png) }` in the deck's css is drawn
// behind every slide (MdCssSupport lists it for page), and the picture it
// names is not reported as unused. The pictures handed to the layout used to
// be only the ones the Markdown names (Tools.backgrounds), so the sheet's
// picture was kept but never drawn.
func TestPageBackgroundImage(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	img := image.NewRGBA(image.Rect(0, 0, 64, 36))
	for y := 0; y < 36; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{0, 200, 0, 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	c := call(t, s, "create_presentation", map[string]any{"title": "W", "markdown": "# Deck\n\n## One\n\nText\n",
		"css":    "page { background-image: url(media/bg.png); }\n",
		"images": []any{map[string]any{"name": "bg.png", "data_base64": base64.StdEncoding.EncodeToString(b.Bytes())}}})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	if strings.Contains(textOf(c), "does not use media/bg.png") {
		t.Fatal("the sheet's picture was called unused:", textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	for _, sl := range []int{1, 2} {
		r := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": sl})
		if r.IsError {
			t.Fatal(lastText(r))
		}
		cr, cg, cb, _ := decodeJPEG(t, r).At(20, 520).RGBA()
		if cr>>8 > 40 || cg>>8 < 160 || cb>>8 > 40 {
			t.Fatalf("slide %d at 20,520 is %d,%d,%d, want the green page picture", sl, cr>>8, cg>>8, cb>>8)
		}
	}
}
