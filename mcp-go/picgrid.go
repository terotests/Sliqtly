// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"regexp"
	"strconv"
	"strings"

	_ "golang.org/x/image/webp"
)

// gridSize is the side of the grid a picture is reduced to for the contrast
// check, as the editor's painter does (LUMA_GRID in lib/evg/gl/evg-webgl.js).
const gridSize = 48

// ImageGrid is the picture in the handle as the contrast check reads it:
// width, height, then gridSize×gridSize RGBA values (each cell the mean of
// the pixels it covers, colours not premultiplied). Empty when the bytes do
// not decode (a broken file). An SVG is not painted here: it is its size
// alone (width, height and no grid), so the layout gives it the room the
// player does (web/picture.js svgSize).
func (h *McpHost) ImageGrid(handle int64) []int64 {
	data := h.images[handle]
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		if w, ht, ok := svgSize(data); ok {
			return []int64{w, ht}
		}
		return []int64{}
	}
	return lumaGrid(img)
}

var (
	svgRoot    = regexp.MustCompile(`(?is)<svg\b((?:[^>"']|"[^"]*"|'[^']*')*)>`)
	svgComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	svgAttr    = regexp.MustCompile(`([^\s=/]+)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	svgLen     = regexp.MustCompile(`(?i)^\s*([+-]?(?:\d+\.?\d*|\.\d+)(?:e[+-]?\d+)?)\s*([a-z]*)\s*$`)
	svgUnits   = map[string]float64{"": 1, "px": 1, "pt": 96.0 / 72, "pc": 16, "in": 96, "cm": 96 / 2.54, "mm": 96 / 25.4, "q": 96 / 101.6, "em": 16, "rem": 16, "ex": 8, "ch": 8}
)

// svgSize is an SVG's own size in CSS pixels, as web/picture.js svgSize
// works it out: width and height, one of them with the viewBox's shape, the
// viewBox alone, or 300 × 150. ok false: not an SVG.
func svgSize(data []byte) (int64, int64, bool) {
	text := svgComment.ReplaceAllStringFunc(string(data), func(c string) string { return strings.Repeat(" ", len(c)) })
	m := svgRoot.FindStringSubmatch(text)
	if m == nil {
		return 0, 0, false
	}
	attrs := map[string]string{}
	for _, a := range svgAttr.FindAllStringSubmatch(m[1], -1) {
		attrs[a[1]] = a[2] + a[3]
	}
	length := func(v string) float64 {
		lm := svgLen.FindStringSubmatch(v)
		if lm == nil {
			return 0
		}
		k, known := svgUnits[strings.ToLower(lm[2])]
		n, err := strconv.ParseFloat(lm[1], 64)
		if !known || err != nil || n <= 0 || math.IsInf(n, 0) {
			return 0
		}
		return n * k
	}
	w, h := length(attrs["width"]), length(attrs["height"])
	var vb []float64
	for _, f := range strings.FieldsFunc(attrs["viewBox"], func(r rune) bool { return r == ' ' || r == ',' || r == '\t' || r == '\n' }) {
		if n, err := strconv.ParseFloat(f, 64); err == nil {
			vb = append(vb, n)
		}
	}
	if len(vb) == 4 && vb[2] > 0 && vb[3] > 0 {
		switch {
		case w > 0 && h == 0:
			h = w * vb[3] / vb[2]
		case h > 0 && w == 0:
			w = h * vb[2] / vb[3]
		case w == 0 && h == 0:
			w, h = vb[2], vb[3]
		}
	}
	if w <= 0 {
		w = 300
	}
	if h <= 0 {
		h = 150
	}
	return int64(math.Round(w)), int64(math.Round(h)), true
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
