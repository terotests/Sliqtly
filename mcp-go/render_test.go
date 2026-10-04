// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"image"
	"testing"
)

// ink counts the pixels a list painted into a white 200×60
func ink(t *testing.T, list string) int {
	t.Helper()
	dst := image.NewRGBA(image.Rect(0, 0, 200, 60))
	for i := range dst.Pix {
		dst.Pix[i] = 255
	}
	if err := renderList(dst, list, 200, 60, dst.Bounds(), nil); err != nil {
		t.Fatal(err)
	}
	n := 0
	for i := 0; i < len(dst.Pix); i += 4 {
		if dst.Pix[i] < 128 {
			n++
		}
	}
	return n
}

// Open Sans has no arrows: "→" comes from Noto Sans, in the same weight
func TestTextFallsBackToNotoSans(t *testing.T) {
	for _, font := range []string{"Open Sans", "Open Sans-Bold", "Open Sans-Italic"} {
		list := `{"cmds":[{"k":3,"x":10,"y":10,"w":180,"h":40,"text":"→","font":"` + font + `","size":36,"c":[0,0,0,1]}]}`
		if n := ink(t, list); n < 40 {
			t.Errorf("%s: → drew %d pixels", font, n)
		}
	}
	if got := notoLike("Open Sans-BoldItalic"); got != "Noto Sans-BoldItalic" {
		t.Errorf("notoLike: %q", got)
	}
}
