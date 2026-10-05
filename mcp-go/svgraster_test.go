// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"image"
	"image/color"
	"strings"
	"testing"
)

// a background as an assistant writes one: a gradient, a hatch pattern, a
// blurred glow and a word, sized only by its viewBox
const svgBackground = `<?xml version="1.0"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1920 1080">
  <defs>
    <linearGradient id="g" x1="0" y1="0" x2="1" y2="0">
      <stop offset="0" stop-color="#ff0000"/><stop offset="1" stop-color="#0000ff"/>
    </linearGradient>
    <pattern id="hatch" width="20" height="20" patternUnits="userSpaceOnUse">
      <rect width="20" height="20" fill="#00ff00"/>
    </pattern>
    <filter id="blur"><feGaussianBlur stdDeviation="8"/></filter>
  </defs>
  <rect width="1920" height="1080" fill="url(#g)"/>
  <rect x="0" y="900" width="1920" height="180" fill="url(#hatch)"/>
  <circle cx="960" cy="540" r="100" fill="#ffffff" filter="url(#blur)"/>
  <text x="100" y="200" font-family="Arial" font-size="120" fill="#000">Sliqtly</text>
</svg>`

func near(t *testing.T, what string, got color.Color, want color.RGBA) {
	t.Helper()
	r, g, b, _ := got.RGBA()
	d := func(a uint32, b uint8) int { x := int(a>>8) - int(b); return max(x, -x) }
	if d(r, want.R) > 24 || d(g, want.G) > 24 || d(b, want.B) > 24 {
		t.Errorf("%s: %v, want about %v", what, got, want)
	}
}

func TestDrawSvgAsThePlayer(t *testing.T) {
	img, err := drawSvg([]byte(svgBackground), 960)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 960 || b.Dy() != 540 {
		t.Fatalf("size %v, want 960×540 (the viewBox's shape)", b)
	}
	near(t, "gradient's left end", img.At(5, 300), color.RGBA{250, 0, 5, 255})
	near(t, "gradient's right end", img.At(955, 300), color.RGBA{5, 0, 250, 255})
	near(t, "pattern", img.At(480, 500), color.RGBA{0, 255, 0, 255})
	near(t, "glow's middle", img.At(480, 270), color.RGBA{255, 255, 255, 255})
	// the word: dark pixels where the text runs
	dark := 0
	for y := 50; y < 105; y++ {
		for x := 50; x < 300; x++ {
			if r, g, b, _ := img.At(x, y).RGBA(); r+g+b < 3*0x3000 {
				dark++
			}
		}
	}
	if dark < 500 {
		t.Errorf("text not drawn: %d dark pixels", dark)
	}
}

func TestDrawSvgSizedLikeThePlayer(t *testing.T) {
	// no viewBox: one of its own size, so the drawing is scaled, not cut
	img, err := drawSvg([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100"><rect x="100" width="100" height="100" fill="#00f"/></svg>`), 400)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 400 || b.Dy() != 200 {
		t.Fatalf("size %v", b)
	}
	near(t, "right half", img.At(390, 190), color.RGBA{0, 0, 255, 255})
	if _, _, _, a := img.At(10, 10).RGBA(); a != 0 {
		t.Error("left half should be clear")
	}
}

func TestDrawSvgLoadsNothingFromOutside(t *testing.T) {
	src := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 10 10"><image href="https://example.com/x.png" width="10" height="10"/><image href="/etc/passwd" width="10" height="10"/></svg>`
	img, err := drawSvg([]byte(src), 40)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range img.(*image.RGBA).Pix {
		if v != 0 {
			t.Fatal("drew something from outside the SVG")
		}
	}
}

func TestDrawSvgBroken(t *testing.T) {
	if _, err := drawSvg([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect`), 40); err == nil || !strings.HasPrefix(err.Error(), "svg: ") {
		t.Fatalf("a broken SVG: %v", err)
	}
	// and the next one still draws
	if _, err := drawSvg([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"/>`), 40); err != nil {
		t.Fatal(err)
	}
}

// as a browser: an SVG outside the SVG namespace is nothing
func TestDrawSvgNeedsTheNamespace(t *testing.T) {
	if _, err := drawSvg([]byte(`<svg viewBox="0 0 10 10"><rect width="10" height="10"/></svg>`), 40); err == nil || !strings.Contains(err.Error(), "xmlns") {
		t.Fatalf("drawn without xmlns: %v", err)
	}
}

func TestRenderPicDrawsSvg(t *testing.T) {
	h := &McpHost{}
	h.RenderPic("/media/bg.svg", []byte(svgBackground))
	if h.renderPics["/media/bg.svg"] == nil {
		t.Fatal("an SVG picture is not given to the painter")
	}
}
