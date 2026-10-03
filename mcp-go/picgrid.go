// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// gridSize is the side of the grid a picture is reduced to for the contrast
// check, as the editor's painter does (LUMA_GRID in lib/evg/gl/evg-webgl.js).
const gridSize = 48

// ImageGrid is the picture in the handle as the contrast check reads it:
// width, height, then gridSize×gridSize RGBA values (each cell the mean of
// the pixels it covers, colours not premultiplied). Empty when the bytes do
// not decode (an SVG, a broken file).
func (h *McpHost) ImageGrid(handle int64) []int64 {
	img, _, err := image.Decode(bytes.NewReader(h.images[handle]))
	if err != nil {
		return []int64{}
	}
	return lumaGrid(img)
}

func lumaGrid(img image.Image) []int64 {
	b := img.Bounds()
	w, ht := b.Dx(), b.Dy()
	if w <= 0 || ht <= 0 {
		return []int64{}
	}
	out := make([]int64, 2, 2+gridSize*gridSize*4)
	out[0], out[1] = int64(w), int64(ht)
	for gy := 0; gy < gridSize; gy++ {
		y0, y1 := b.Min.Y+gy*ht/gridSize, b.Min.Y+(gy+1)*ht/gridSize
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for gx := 0; gx < gridSize; gx++ {
			x0, x1 := b.Min.X+gx*w/gridSize, b.Min.X+(gx+1)*w/gridSize
			if x1 <= x0 {
				x1 = x0 + 1
			}
			// at most 8×8 samples a cell: a big photo is not read whole
			sx, sy := max(1, (x1-x0)/8), max(1, (y1-y0)/8)
			var r, g, bl, a, n float64
			for y := y0; y < y1 && y < b.Max.Y; y += sy {
				for x := x0; x < x1 && x < b.Max.X; x += sx {
					pr, pg, pb, pa := img.At(x, y).RGBA()
					r, g, bl, a, n = r+float64(pr), g+float64(pg), bl+float64(pb), a+float64(pa), n+1
				}
			}
			if n == 0 || a == 0 {
				out = append(out, 0, 0, 0, 0)
				continue
			}
			// premultiplied sums → straight colour, 0..255
			out = append(out, int64(r/a*255+0.5), int64(g/a*255+0.5), int64(bl/a*255+0.5), int64(a/n/257+0.5))
		}
	}
	return out
}

// Bytes is the handle's bytes as they are.
func (h *McpHost) Bytes(handle int64) []byte { return h.images[handle] }
