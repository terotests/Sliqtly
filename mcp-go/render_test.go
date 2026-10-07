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

// inkRight is the rightmost column a list painted into a white 400×60
func inkRight(t *testing.T, list string) int {
	t.Helper()
	dst := image.NewRGBA(image.Rect(0, 0, 400, 60))
	for i := range dst.Pix {
		dst.Pix[i] = 255
	}
	if err := renderList(dst, list, 400, 60, dst.Bounds(), nil); err != nil {
		t.Fatal(err)
	}
	right := -1
	for y := 0; y < 60; y++ {
		for x := 0; x < 400; x++ {
			if dst.Pix[y*dst.Stride+x*4] < 128 && x > right {
				right = x
			}
		}
	}
	return right
}

// A diagram look's face is drawn in that face, the one the layout measured
// its words in: drawn in Open Sans, the romantic look's FJALLA ONE capitals
// came out wider than their box ("TOTEUT").
func TestLookFacesAreDrawnInTheirOwnFace(t *testing.T) {
	word := func(font string) string {
		return `{"cmds":[{"k":3,"x":0,"y":10,"w":400,"h":40,"text":"TOTEUTA","font":"` + font + `","size":36,"c":[0,0,0,1]}]}`
	}
	open, fjalla := inkRight(t, word("Open Sans")), inkRight(t, word("Fjalla One"))
	if fjalla <= 0 || fjalla >= open*85/100 {
		t.Fatalf("Fjalla One drawn %d px wide, Open Sans %d: not its own condensed face", fjalla, open)
	}
	for _, name := range []string{"Gloria Hallelujah", "Josefin Sans-Bold", "Droid Serif-BoldItalic"} {
		if face(name, "", false) == face("Open Sans", "", false) {
			t.Errorf("%s is drawn in Open Sans", name)
		}
	}
	// a face this server lacks is still drawn, in Open Sans
	if face("Comic Neue", "bold", false) != face("Open Sans-Bold", "", false) {
		t.Error("an unknown face is not Open Sans")
	}
}
