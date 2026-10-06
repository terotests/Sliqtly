// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
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

// maxPicturePixels is the most pixels a picture is decoded with. Decoding
// takes 4 bytes a pixel at once, and a few bytes of PNG can claim any size,
// so the size is read from the header first: 40 million (8000×5000, 160 MB)
// is above any camera's or screen's picture a slide needs.
const maxPicturePixels = 40_000_000

// decodePicture decodes a raster picture whose header claims no more than
// maxPicturePixels.
func decodePicture(data []byte) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxPicturePixels {
		return nil, fmt.Errorf("the picture is %d×%d pixels; pictures of up to %d million pixels are read", cfg.Width, cfg.Height, maxPicturePixels/1_000_000)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

// ImageGrid is the picture in the handle as the contrast check reads it:
// width, height, then gridSize×gridSize RGBA values (each cell the mean of
// the pixels it covers, colours not premultiplied). Empty when the bytes do
// not decode (a broken file). An SVG has its own size (web/picture.js
// svgSize), so the layout gives it the room the player does, and the grid
// of its drawing (svgraster.go); its size alone when it does not draw.
func (h *McpHost) ImageGrid(handle int64) []int64 {
	data := h.images[handle]
	img, err := decodePicture(data)
	if err != nil {
		w, ht, ok := svgSize(data)
		if !ok {
			return []int64{}
		}
		pic, err := drawSvg(data, svgGridSide)
		if err != nil {
			return []int64{w, ht}
		}
		g := lumaGrid(pic)
		g[0], g[1] = w, ht
		return g
	}
	return lumaGrid(img)
}

// ImageError is why a raster picture's bytes do not decode ("" when they do):
// a cut-short base64 string otherwise becomes a picture the slide draws as
// nothing, with no word why.
func (h *McpHost) ImageError(handle int64) string {
	if _, err := decodePicture(h.images[handle]); err != nil {
		return err.Error()
	}
	return ""
}

// the longer side an SVG is drawn at for the contrast grid: ten pixels
// and more to each of its cells
const svgGridSide = 480

var (
	svgRoot    = regexp.MustCompile(`(?is)<svg\b((?:[^>"']|"[^"]*"|'[^']*')*)>`)
	svgComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	svgAttr    = regexp.MustCompile(`([^\s=/]+)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	svgLen     = regexp.MustCompile(`(?i)^\s*([+-]?(?:\d+\.?\d*|\.\d+)(?:e[+-]?\d+)?)\s*([a-z]*)\s*$`)
	svgUnits   = map[string]float64{"": 1, "px": 1, "pt": 96.0 / 72, "pc": 16, "in": 96, "cm": 96 / 2.54, "mm": 96 / 25.4, "q": 96 / 101.6, "em": 16, "rem": 16, "ex": 8, "ch": 8}
)

// svgRootTag is the outermost <svg …> start tag: where it is in data, what
// is inside it, and its attributes (comments before it skipped, as
// web/picture.js rootTag does). ok false: not an SVG.
func svgRootTag(data []byte) (start, end int, inner string, attrs map[string]string, ok bool) {
	text := svgComment.ReplaceAllStringFunc(string(data), func(c string) string { return strings.Repeat(" ", len(c)) })
	m := svgRoot.FindStringSubmatchIndex(text)
	if m == nil {
		return 0, 0, "", nil, false
	}
	inner = text[m[2]:m[3]]
	attrs = map[string]string{}
	for _, a := range svgAttr.FindAllStringSubmatch(inner, -1) {
		attrs[a[1]] = a[2] + a[3]
	}
	return m[0], m[1], inner, attrs, true
}

// svgViewBox is the viewBox's four numbers, nil when it has none that
// could be drawn
func svgViewBox(attrs map[string]string) []float64 {
	var vb []float64
	for _, f := range strings.FieldsFunc(attrs["viewBox"], func(r rune) bool { return r == ' ' || r == ',' || r == '\t' || r == '\n' || r == '\r' }) {
		n, err := strconv.ParseFloat(f, 64)
		if err != nil {
			return nil
		}
		vb = append(vb, n)
	}
	if len(vb) != 4 || vb[2] <= 0 || vb[3] <= 0 {
		return nil
	}
	return vb
}

var svgSizeAttr = regexp.MustCompile(`(?i)\s(width|height)\s*=\s*("[^"]*"|'[^']*')`)

// svgSizedTo is the SVG with its root sized w × h pixels, as
// web/picture.js svgSizedTo makes it before a browser draws it: an SVG
// without a viewBox gets one of its own size, so it is scaled, not cut.
func svgSizedTo(data []byte, w, h int) ([]byte, bool) {
	start, end, inner, attrs, ok := svgRootTag(data)
	if !ok {
		return nil, false
	}
	sw, sh, _ := svgSizeF(data)
	// <svg …/>: the attributes go before its slash
	inner, closed := strings.CutSuffix(strings.TrimRight(svgSizeAttr.ReplaceAllString(inner, ""), " \t\r\n"), "/")
	inner = strings.TrimRight(inner, " \t\r\n")
	if svgViewBox(attrs) == nil {
		inner += " viewBox=\"0 0 " + strconv.FormatFloat(sw, 'g', -1, 64) + " " + strconv.FormatFloat(sh, 'g', -1, 64) + "\""
	}
	inner += " width=\"" + strconv.Itoa(w) + "\" height=\"" + strconv.Itoa(h) + "\""
	out := make([]byte, 0, len(data)+40)
	out = append(out, data[:start]...)
	if closed {
		inner += "/"
	}
	out = append(out, "<svg"+inner+">"...)
	return append(out, data[end:]...), true
}

// svgSize is an SVG's own size in CSS pixels, as web/picture.js svgSize
// works it out: width and height, one of them with the viewBox's shape, the
// viewBox alone, or 300 × 150. ok false: not an SVG.
func svgSize(data []byte) (int64, int64, bool) {
	w, h, ok := svgSizeF(data)
	return int64(math.Round(w)), int64(math.Round(h)), ok
}

// svgSizeF is svgSize unrounded
func svgSizeF(data []byte) (float64, float64, bool) {
	_, _, _, attrs, ok := svgRootTag(data)
	if !ok {
		return 0, 0, false
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
	if vb := svgViewBox(attrs); vb != nil {
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
	return w, h, true
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
