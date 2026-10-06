// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strconv"
	"testing"
)

func sizedPNG(w, h int) string {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

// A picture given a width with text after it that goes beside it is as wide
// as asked (until the slide's height stops it): a wider width is never a
// smaller picture. It used to keep room under itself for the text beside it.
func TestPictureWidthBesideText(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	// the corporate theme's measures
	css := "page { width: 13.333in; height: 7.5in; padding: 0.7in; background-color: #ffffff; }\ndeck { split-level: 2; overflow: split; }\ndocument { font-family: Open Sans; font-size: 15pt; line-height: 1.4; color: #16202c; }\nheading { margin-top: 26pt; margin-bottom: 12pt; }\nh1 { font-size: 40pt; }\nh2 { font-size: 27pt; }\np { margin-bottom: 13pt; }\n.lead { font-size: 21pt; }\n"
	pic := sizedPNG(1200, 900)
	last := 0
	for _, pc := range []int{45, 50, 55, 60, 65} {
		md := fmt.Sprintf("# Album\n\n## Paras päivä\n\n![Näköalapaikalla](media/d1.png)\n{width=%d%%}\n\nViimeisenä iltana kiipesimme näköalapaikalle ja katsoimme, kun kaupungin valot syttyivät.\n{.lead}\n", pc)
		c := call(t, s, "create_presentation", map[string]any{"title": "W", "markdown": md, "css": css, "css_mode": "replace",
			"images": []any{map[string]any{"name": "d1.png", "data_base64": pic}}})
		if c.IsError {
			t.Fatal(textOf(c))
		}
		m := regexp.MustCompile(`picture \(d1\.png\) at [\d,]+ size (\d+)×\d+`).FindStringSubmatch(textOf(c))
		if m == nil {
			t.Fatal(textOf(c))
		}
		w, _ := strconv.Atoi(m[1])
		if w < last {
			t.Fatalf("width=%d%% is %d px, narrower than the one before (%d px)", pc, w, last)
		}
		// 1718 px is the column on a 1920 px slide
		if pc <= 55 && (w < 1718*pc/100-4) {
			t.Fatalf("width=%d%% is %d px, not %d", pc, w, 1718*pc/100)
		}
		last = w
	}
}
