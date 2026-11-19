package main

import (
	"image"
	"image/color"
	"testing"
)

// A textured globe (draw kind 8) is cast from its map: the middle of the
// disc has the map's colour at longitude 0, the corners stay as they were.
func TestGamesSphereCastsTheMap(t *testing.T) {
	m := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			m.SetRGBA(x, y, color.RGBA{0, 0, 255, 255})
		}
	}
	// longitude 0 (columns 4…5 of 8), latitude 0: red
	m.SetRGBA(4, 1, color.RGBA{255, 0, 0, 255})
	m.SetRGBA(4, 2, color.RGBA{255, 0, 0, 255})
	dst := image.NewRGBA(image.Rect(0, 0, 100, 100))
	p := &painter{dst: dst, k: 1, pics: &renderPics{img: map[string]image.Image{"/m.png": m}}}
	p.clips = []image.Rectangle{dst.Bounds()}
	p.sphere(&dlCmd{K: 8, X: 0, Y: 0, W: 100, H: 100, Src: "/m.png",
		Pts: []float64{3, 0, 0, 0, 1, 0, 0, 0, 1, -1, 0, 0, 1, 0, 0, 100, 100, 100, 0.9, 1}})
	if c := dst.RGBAAt(50, 50); c.R < 200 || c.B > 50 {
		t.Fatalf("middle %v, want the map's red", c)
	}
	if c := dst.RGBAAt(1, 1); c.A != 0 {
		t.Fatalf("corner %v, want untouched", c)
	}
}
