// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"image"
	"image/color"
	"math"
)

// The engine's 3D triangles (6 floats a corner: x, y in the game's units,
// depth 0 near … 1 far, r, g, b 0…1) filled with a depth buffer, as the
// page's WebGL does (web/js/gl3d.js: depth test LEQUAL, no culling), at
// twice the size and then averaged down so the edges are smooth.
func gamesTris(dst *image.RGBA, tris []float32, scale, ox, oy float64, area image.Rectangle) {
	if len(tris) < 18 || area.Empty() {
		return
	}
	const ss = 2
	w, h := area.Dx()*ss, area.Dy()*ss
	col := make([][3]float32, w*h)
	zb := make([]float32, w*h)
	hit := make([]bool, w*h)
	for i := range zb {
		zb[i] = 1
	}
	// a corner on the big canvas
	px := func(i int) (float64, float64, float32) {
		return ((ox + float64(tris[i])*scale) - float64(area.Min.X)) * ss,
			((oy + float64(tris[i+1])*scale) - float64(area.Min.Y)) * ss, tris[i+2]
	}
	for t := 0; t+18 <= len(tris); t += 18 {
		x0, y0, z0 := px(t)
		x1, y1, z1 := px(t + 6)
		x2, y2, z2 := px(t + 12)
		d := (x1-x0)*(y2-y0) - (x2-x0)*(y1-y0)
		if d == 0 || math.IsNaN(d) {
			continue
		}
		minX := int(math.Max(0, math.Floor(math.Min(x0, math.Min(x1, x2)))))
		maxX := int(math.Min(float64(w-1), math.Ceil(math.Max(x0, math.Max(x1, x2)))))
		minY := int(math.Max(0, math.Floor(math.Min(y0, math.Min(y1, y2)))))
		maxY := int(math.Min(float64(h-1), math.Ceil(math.Max(y0, math.Max(y1, y2)))))
		for y := minY; y <= maxY; y++ {
			cy := float64(y) + 0.5
			for x := minX; x <= maxX; x++ {
				cx := float64(x) + 0.5
				// barycentric weights, the same sign as d inside
				a := ((x1-cx)*(y2-cy) - (x2-cx)*(y1-cy)) / d
				b := ((x2-cx)*(y0-cy) - (x0-cx)*(y2-cy)) / d
				c := 1 - a - b
				if a < 0 || b < 0 || c < 0 {
					continue
				}
				z := float32(a)*z0 + float32(b)*z1 + float32(c)*z2
				k := y*w + x
				if z > zb[k] {
					continue
				}
				zb[k] = z
				hit[k] = true
				fa, fb, fc := float32(a), float32(b), float32(c)
				col[k] = [3]float32{
					fa*tris[t+3] + fb*tris[t+9] + fc*tris[t+15],
					fa*tris[t+4] + fb*tris[t+10] + fc*tris[t+16],
					fa*tris[t+5] + fb*tris[t+11] + fc*tris[t+17],
				}
			}
		}
	}
	// down to the picture; pixels no triangle reached keep what is there
	for y := 0; y < area.Dy(); y++ {
		for x := 0; x < area.Dx(); x++ {
			var r, g, b float32
			n := 0
			for j := 0; j < ss; j++ {
				for i := 0; i < ss; i++ {
					k := (y*ss+j)*w + x*ss + i
					if hit[k] {
						c := col[k]
						r, g, b, n = r+c[0], g+c[1], b+c[2], n+1
					}
				}
			}
			if n == 0 {
				continue
			}
			px, py := area.Min.X+x, area.Min.Y+y
			o := dst.RGBAAt(px, py)
			f := float32(n) / ss / ss
			mix := func(v float32, under uint8) uint8 {
				v = v / float32(n) * 255
				return uint8(math.Max(0, math.Min(255, math.Round(float64(v*f+float32(under)*(1-f))))))
			}
			dst.SetRGBA(px, py, color.RGBA{mix(r, o.R), mix(g, o.G), mix(b, o.B), 255})
		}
	}
}
