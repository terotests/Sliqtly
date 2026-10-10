// SPDX-License-Identifier: AGPL-3.0-or-later

// A slide as a picture, for render_slide and render_overview: the display
// list the deck model built for it (PresDeck.slideList, the commands the
// editor's painter draws, as EVGDisplayList.toJson writes them) painted into
// pixels with the editor's own faces. Read lib/evg/html/evg-html.js beside
// this: it is the reference painter for the same list, and the conventions
// here (where a baseline goes, a border inside its box, object-fit: cover)
// are taken from it.
//
// What is not drawn: the built-in GPU effects (an effect's rectangle is
// drawn as its colour; a deck's own are drawn by fxvm.go), backdrop blur, a picture's rounded corners and rotation. An SVG
// picture is drawn by svgraster.go.

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

type dlShadow struct {
	X, Y, Blur float64
	C          []float64
}

type dlCmd struct {
	K      int       `json:"k"`
	X      float64   `json:"x"`
	Y      float64   `json:"y"`
	W      float64   `json:"w"`
	H      float64   `json:"h"`
	R      float64   `json:"r"`
	RC     []float64 `json:"rc"`
	T      float64   `json:"t"`
	C      []float64 `json:"c"`
	GD     *int      `json:"gd"`
	C2     []float64 `json:"c2"`
	Text   string    `json:"text"`
	Font   string    `json:"font"`
	Size   float64   `json:"size"`
	LS     float64   `json:"ls"`
	Weight string    `json:"weight"`
	Italic bool      `json:"italic"`
	Src    string    `json:"src"`
	FX     bool      `json:"fx"`
	FY     bool      `json:"fy"`
	CU     []float64 `json:"cu"`
	Rot    float64   `json:"rot"`
	Rox    *float64  `json:"rox"`
	Roy    *float64  `json:"roy"`
	Sh     *dlShadow `json:"sh"`
	Pts    []float64 `json:"pts"`
	Ends   []int     `json:"ends"`
	EO     int       `json:"eo"`
	Dash   string    `json:"dash"`
	Cap    int       `json:"cap"`
	Efx    string    `json:"efx"`
}

type dlDoc struct {
	Cmds    []dlCmd `json:"cmds"`
	Effects []dlFx  `json:"effects"`
}

type pt struct{ x, y float64 }

// painter draws one list into dst; k scales slide pixels to dst's pixels
type painter struct {
	dst   *image.RGBA
	k     float64
	off   pt // where the slide's (0, 0) is in dst
	clips []image.Rectangle
	pics  *renderPics
	z     vector.Rasterizer
	buf   sfnt.Buffer
	// the deck's own effects (fxvm.go) and the list's instances of them
	fx     *renderFx
	fxInst map[string]*dlFx
}

func colorOf(c []float64) color.NRGBA {
	if len(c) < 3 {
		return color.NRGBA{0, 0, 0, 255}
	}
	a := 1.0
	if len(c) >= 4 {
		a = c[3]
	}
	return color.NRGBA{uint8(clamp255(c[0])), uint8(clamp255(c[1])), uint8(clamp255(c[2])), uint8(clamp255(a * 255))}
}

func clamp255(v float64) float64 { return math.Max(0, math.Min(255, math.Round(v))) }

func (p *painter) clip() image.Rectangle {
	if len(p.clips) == 0 {
		return p.dst.Bounds()
	}
	return p.clips[len(p.clips)-1]
}

// slide pixels → dst pixels
func (p *painter) dev(x, y float64) pt { return pt{p.off.x + x*p.k, p.off.y + y*p.k} }

// rotate turns points about (cx, cy) in slide pixels
func rotated(rings [][]pt, deg, cx, cy float64) {
	if deg == 0 {
		return
	}
	a := deg * math.Pi / 180
	s, c := math.Sin(a), math.Cos(a)
	for _, r := range rings {
		for i, q := range r {
			dx, dy := q.x-cx, q.y-cy
			r[i] = pt{cx + dx*c - dy*s, cy + dx*s + dy*c}
		}
	}
}

func (p *painter) pivot(c *dlCmd) (float64, float64) {
	cx, cy := c.X+c.W/2, c.Y+c.H/2
	if c.Rox != nil {
		cx = *c.Rox
		if c.Roy != nil {
			cy = *c.Roy
		}
	}
	return cx, cy
}

// fill paints rings (slide pixels) with src; rings of opposite winding cut
// holes, as the rasterizer sums signed coverage
func (p *painter) fill(rings [][]pt, src image.Image) {
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	dr := make([][]pt, 0, len(rings))
	for _, r := range rings {
		if len(r) < 3 {
			continue
		}
		d := make([]pt, len(r))
		for i, q := range r {
			d[i] = p.dev(q.x, q.y)
			minX, minY = math.Min(minX, d[i].x), math.Min(minY, d[i].y)
			maxX, maxY = math.Max(maxX, d[i].x), math.Max(maxY, d[i].y)
		}
		dr = append(dr, d)
	}
	if len(dr) == 0 {
		return
	}
	box := image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX))+1, int(math.Ceil(maxY))+1).Intersect(p.clip())
	if box.Empty() {
		return
	}
	p.z.Reset(box.Dx(), box.Dy())
	ox, oy := float32(box.Min.X), float32(box.Min.Y)
	for _, d := range dr {
		p.z.MoveTo(float32(d[0].x)-ox, float32(d[0].y)-oy)
		for _, q := range d[1:] {
			p.z.LineTo(float32(q.x)-ox, float32(q.y)-oy)
		}
		p.z.ClosePath()
	}
	p.z.Draw(p.dst, box, src, box.Min)
}

// a rounded rectangle as one ring, clockwise; radii TL, TR, BR, BL
func roundRect(x, y, w, h float64, r [4]float64) []pt {
	lim := math.Min(w, h) / 2
	for i := range r {
		r[i] = math.Max(0, math.Min(r[i], lim))
	}
	var out []pt
	corner := func(cx, cy, rad, a0 float64) {
		if rad <= 0 {
			out = append(out, pt{cx, cy})
			return
		}
		for i := 0; i <= 6; i++ {
			a := (a0 + float64(i)*15) * math.Pi / 180
			out = append(out, pt{cx + rad*math.Cos(a), cy + rad*math.Sin(a)})
		}
	}
	if r[0] > 0 {
		corner(x+r[0], y+r[0], r[0], 180)
	} else {
		corner(x, y, 0, 0)
	}
	if r[1] > 0 {
		corner(x+w-r[1], y+r[1], r[1], 270)
	} else {
		corner(x+w, y, 0, 0)
	}
	if r[2] > 0 {
		corner(x+w-r[2], y+h-r[2], r[2], 0)
	} else {
		corner(x+w, y+h, 0, 0)
	}
	if r[3] > 0 {
		corner(x+r[3], y+h-r[3], r[3], 90)
	} else {
		corner(x, y+h, 0, 0)
	}
	return out
}

func radii(c *dlCmd) [4]float64 {
	if len(c.RC) >= 4 {
		return [4]float64{c.RC[0], c.RC[1], c.RC[2], c.RC[3]}
	}
	return [4]float64{c.R, c.R, c.R, c.R}
}

func reversed(r []pt) []pt {
	out := make([]pt, len(r))
	for i, q := range r {
		out[len(r)-1-i] = q
	}
	return out
}

// a two-stop gradient across (gd 1) or down the command's box, in dst
// pixels
type gradient struct {
	x0, y0, x1, y1 float64
	a, b           color.NRGBA
}

func (g *gradient) ColorModel() color.Model { return color.NRGBAModel }
func (g *gradient) Bounds() image.Rectangle {
	return image.Rect(-1e9, -1e9, 1e9, 1e9)
}
func (g *gradient) At(x, y int) color.Color {
	dx, dy := g.x1-g.x0, g.y1-g.y0
	l := dx*dx + dy*dy
	t := 0.0
	if l > 0 {
		t = ((float64(x)+0.5-g.x0)*dx + (float64(y)+0.5-g.y0)*dy) / l
	}
	t = math.Max(0, math.Min(1, t))
	mix := func(a, b uint8) uint8 { return uint8(math.Round(float64(a)*(1-t) + float64(b)*t)) }
	return color.NRGBA{mix(g.a.R, g.b.R), mix(g.a.G, g.b.G), mix(g.a.B, g.b.B), mix(g.a.A, g.b.A)}
}

func (p *painter) srcOf(c *dlCmd) image.Image {
	if c.GD != nil && len(c.C2) >= 3 {
		a, b := p.dev(c.X, c.Y), p.dev(c.X, c.Y+c.H)
		if *c.GD == 1 {
			b = p.dev(c.X+c.W, c.Y)
		}
		return &gradient{a.x, a.y, b.x, b.y, colorOf(c.C), colorOf(c.C2)}
	}
	return image.NewUniform(colorOf(c.C))
}

func (p *painter) rect(c *dlCmd) {
	if c.W <= 0 || c.H <= 0 {
		return
	}
	cx, cy := p.pivot(c)
	if c.Sh != nil && len(c.Sh.C) >= 3 {
		// the shadow softened by a few wider, fainter copies
		sc := colorOf(c.Sh.C)
		steps := 3
		for i := steps; i >= 1; i-- {
			grow := c.Sh.Blur * float64(i) / float64(steps) / 2
			s := sc
			s.A = uint8(float64(sc.A) / float64(steps+1))
			r := radii(c)
			for j := range r {
				r[j] += grow
			}
			rings := [][]pt{roundRect(c.X+c.Sh.X-grow, c.Y+c.Sh.Y-grow, c.W+2*grow, c.H+2*grow, r)}
			rotated(rings, c.Rot, cx, cy)
			p.fill(rings, image.NewUniform(s))
		}
	}
	rings := [][]pt{roundRect(c.X, c.Y, c.W, c.H, radii(c))}
	rotated(rings, c.Rot, cx, cy)
	p.fill(rings, p.srcOf(c))
}

// a border is drawn inside its box
func (p *painter) border(c *dlCmd) {
	t := c.T
	if t <= 0 {
		t = 1
	}
	if c.W <= t || c.H <= t {
		return
	}
	r := radii(c)
	in := [4]float64{}
	for i := range r {
		in[i] = math.Max(0, r[i]-t)
	}
	rings := [][]pt{roundRect(c.X, c.Y, c.W, c.H, r), reversed(roundRect(c.X+t, c.Y+t, c.W-2*t, c.H-2*t, in))}
	cx, cy := p.pivot(c)
	rotated(rings, c.Rot, cx, cy)
	p.fill(rings, image.NewUniform(colorOf(c.C)))
}

func ringsOf(c *dlCmd) [][]pt {
	ends := c.Ends
	if len(ends) == 0 {
		ends = []int{len(c.Pts)}
	}
	var out [][]pt
	at := 0
	for _, e := range ends {
		if e > len(c.Pts) {
			e = len(c.Pts)
		}
		var r []pt
		for i := at; i+1 < e; i += 2 {
			r = append(r, pt{c.Pts[i], c.Pts[i+1]})
		}
		if len(r) > 0 {
			out = append(out, r)
		}
		at = e
	}
	return out
}

func (p *painter) path(c *dlCmd) {
	rings := ringsOf(c)
	cx, cy := p.pivot(c)
	rotated(rings, c.Rot, cx, cy)
	p.fill(rings, p.srcOf(c))
}

// dashes splits a polyline by a stroke-dasharray
func dashes(r []pt, pattern string) [][]pt {
	var pat []float64
	for _, f := range strings.FieldsFunc(pattern, func(r rune) bool { return r == ' ' || r == ',' }) {
		if v, err := strconv.ParseFloat(f, 64); err == nil && v >= 0 {
			pat = append(pat, v)
		}
	}
	total := 0.0
	for _, v := range pat {
		total += v
	}
	if len(pat) == 0 || total <= 0 {
		return [][]pt{r}
	}
	if len(pat)%2 == 1 {
		pat = append(pat, pat...)
	}
	var out [][]pt
	idx, left, on := 0, pat[0], true
	cur := []pt{r[0]}
	for i := 1; i < len(r); i++ {
		a, b := r[i-1], r[i]
		seg := math.Hypot(b.x-a.x, b.y-a.y)
		pos := 0.0
		for seg-pos > left {
			pos += left
			q := pt{a.x + (b.x-a.x)*pos/seg, a.y + (b.y-a.y)*pos/seg}
			if on {
				cur = append(cur, q)
				out = append(out, cur)
				cur = nil
			} else {
				cur = []pt{q}
			}
			on = !on
			idx = (idx + 1) % len(pat)
			left = pat[idx]
		}
		left -= seg - pos
		if on {
			cur = append(cur, b)
		} else {
			cur = []pt{b}
		}
	}
	if on && len(cur) > 1 {
		out = append(out, cur)
	}
	return out
}

// stroke draws polylines t wide with round joins and ends: every segment a
// quad and every joint a disc, all of one winding so they add up to one
// shape (a translucent line does not darken where its pieces meet)
func (p *painter) stroke(c *dlCmd) {
	t := c.T
	if t <= 0 {
		t = 1
	}
	var lines [][]pt
	for _, r := range ringsOf(c) {
		if len(r) < 2 {
			continue
		}
		if c.Dash != "" {
			lines = append(lines, dashes(r, c.Dash)...)
		} else {
			lines = append(lines, r)
		}
	}
	var rings [][]pt
	hw := t / 2
	disc := func(q pt) []pt {
		n := 12
		if hw*p.k < 1.5 {
			n = 6
		}
		out := make([]pt, n)
		for i := range out {
			a := float64(i) * 2 * math.Pi / float64(n)
			out[i] = pt{q.x + hw*math.Cos(a), q.y + hw*math.Sin(a)}
		}
		return out
	}
	for _, l := range lines {
		for i := 1; i < len(l); i++ {
			a, b := l[i-1], l[i]
			d := math.Hypot(b.x-a.x, b.y-a.y)
			if d == 0 {
				continue
			}
			nx, ny := -(b.y-a.y)/d*hw, (b.x-a.x)/d*hw
			q := []pt{{a.x + nx, a.y + ny}, {b.x + nx, b.y + ny}, {b.x - nx, b.y - ny}, {a.x - nx, a.y - ny}}
			if area(q) < 0 {
				q = reversed(q)
			}
			rings = append(rings, q)
		}
		round := c.Cap == 1 || c.Dash == ""
		for i, q := range l {
			if (i == 0 || i == len(l)-1) && !round {
				continue
			}
			rings = append(rings, disc(q))
		}
	}
	cx, cy := p.pivot(c)
	rotated(rings, c.Rot, cx, cy)
	p.fill(rings, image.NewUniform(colorOf(c.C)))
}

func area(r []pt) float64 {
	s := 0.0
	for i := range r {
		j := (i + 1) % len(r)
		s += r[i].x*r[j].y - r[j].x*r[i].y
	}
	return s / 2
}

// text: a run on one line, its baseline placed as evg-html.js baselineOf
// places it, each glyph's outline filled
func (p *painter) text(c *dlCmd) {
	if strings.TrimSpace(c.Text) == "" || c.Size <= 0 {
		return
	}
	f := face(c.Font, c.Weight, c.Italic)
	if f == nil {
		return
	}
	// a character the face lacks comes from Noto Sans (Open Sans has no
	// arrows: "→"), then Noto Emoji, the order the layout measured it in,
	// and last DejaVu Sans for the symbols none of those have (⇒ ✓ ★ ◆ ∈),
	// which a browser takes from a system font. A character asked for as
	// an emoji (U+FE0F after it) tries Noto Emoji first.
	fallback := []*sfnt.Font{face(notoLike(c.Font), c.Weight, c.Italic), loadFace("Noto Emoji-Regular"), loadFace(symbolLike(c.Font, c.Weight))}
	emojiFirst := []*sfnt.Font{fallback[1], fallback[0], fallback[2]}
	ppem := fixed.Int26_6(math.Round(c.Size * 64))
	m, err := f.Metrics(&p.buf, ppem, font.HintingNone)
	asc, desc := c.Size*1.05, c.Size*0.212
	if err == nil {
		asc, desc = float64(m.Ascent)/64, float64(m.Descent)/64
	}
	base := c.Y + asc
	if c.H > 0 {
		base = c.Y + (c.H-(asc+desc))/2 + asc
	}
	var curves []glyphPath
	x := c.X
	var prev sfnt.GlyphIndex
	runes := []rune(c.Text)
	for i, r := range runes {
		chain := fallback
		if i+1 < len(runes) && runes[i+1] == 0xFE0F {
			chain = emojiFirst
		}
		ff, gi := glyphOf(&p.buf, f, chain, r)
		if ff == f && prev != 0 && gi != 0 {
			if kern, err := f.Kern(&p.buf, prev, gi, ppem, font.HintingNone); err == nil {
				x += float64(kern) / 64
			}
		}
		if segs, err := ff.LoadGlyph(&p.buf, gi, ppem, nil); err == nil {
			curves = append(curves, glyphPath{x, base, append(sfnt.Segments(nil), segs...)})
		}
		adv, err := ff.GlyphAdvance(&p.buf, gi, ppem, font.HintingNone)
		if err == nil {
			x += float64(adv) / 64
		}
		x += c.LS
		prev = gi
		if ff != f {
			prev = 0
		}
	}
	cx, cy := c.X+(x-c.X)/2, base-asc+(asc+desc)/2
	if c.Rox != nil {
		cx = *c.Rox
		if c.Roy != nil {
			cy = *c.Roy
		}
	}
	p.glyphs(curves, c.Rot, cx, cy, image.NewUniform(colorOf(c.C)))
}

// notoLike names Noto Sans in the weight and slant the family names
// ("Open Sans-BoldItalic" → "Noto Sans-BoldItalic")
func notoLike(family string) string {
	for _, suf := range []string{"-BoldItalic", "-Bold", "-Italic"} {
		if strings.HasSuffix(family, suf) {
			return "Noto Sans" + suf
		}
	}
	return "Noto Sans"
}

// glyphOf is the face that draws r and its glyph: f when f has it, else the
// first of chain that does; glyph 0 (an empty box) when none has it
func glyphOf(buf *sfnt.Buffer, f *sfnt.Font, chain []*sfnt.Font, r rune) (*sfnt.Font, sfnt.GlyphIndex) {
	if gi, _ := f.GlyphIndex(buf, r); gi != 0 {
		return f, gi
	}
	for _, alt := range chain {
		if alt == nil || alt == f {
			continue
		}
		if gi, _ := alt.GlyphIndex(buf, r); gi != 0 {
			return alt, gi
		}
	}
	return f, 0
}

// symbolLike is the DejaVu Sans face for a family and weight: bold when
// the run is bold, no italic (the symbols are upright in a browser too)
func symbolLike(family, weight string) string {
	w := strings.ToLower(weight)
	if strings.Contains(family, "-Bold") || strings.Contains(w, "bold") || w == "600" || w == "700" || w == "800" || w == "900" {
		return "DejaVu Sans-Bold"
	}
	return "DejaVu Sans"
}

type glyphPath struct {
	x, y float64
	segs sfnt.Segments
}

// glyph outlines (quadratic and cubic) filled in one pass
func (p *painter) glyphs(gs []glyphPath, deg, cx, cy float64, src image.Image) {
	if len(gs) == 0 {
		return
	}
	a := deg * math.Pi / 180
	sn, cs := math.Sin(a), math.Cos(a)
	tr := func(gx, gy float64, v fixed.Point26_6) pt {
		x, y := gx+float64(v.X)/64, gy+float64(v.Y)/64
		if deg != 0 {
			dx, dy := x-cx, y-cy
			x, y = cx+dx*cs-dy*sn, cy+dx*sn+dy*cs
		}
		return p.dev(x, y)
	}
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, g := range gs {
		for _, s := range g.segs {
			n := 1
			switch s.Op {
			case sfnt.SegmentOpQuadTo:
				n = 2
			case sfnt.SegmentOpCubeTo:
				n = 3
			}
			for i := 0; i < n; i++ {
				q := tr(g.x, g.y, s.Args[i])
				minX, minY = math.Min(minX, q.x), math.Min(minY, q.y)
				maxX, maxY = math.Max(maxX, q.x), math.Max(maxY, q.y)
			}
		}
	}
	if math.IsInf(minX, 0) {
		return
	}
	box := image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX))+1, int(math.Ceil(maxY))+1).Intersect(p.clip())
	if box.Empty() {
		return
	}
	p.z.Reset(box.Dx(), box.Dy())
	ox, oy := float32(box.Min.X), float32(box.Min.Y)
	for _, g := range gs {
		open := false
		for _, s := range g.segs {
			switch s.Op {
			case sfnt.SegmentOpMoveTo:
				if open {
					p.z.ClosePath()
				}
				q := tr(g.x, g.y, s.Args[0])
				p.z.MoveTo(float32(q.x)-ox, float32(q.y)-oy)
				open = true
			case sfnt.SegmentOpLineTo:
				q := tr(g.x, g.y, s.Args[0])
				p.z.LineTo(float32(q.x)-ox, float32(q.y)-oy)
			case sfnt.SegmentOpQuadTo:
				q1, q2 := tr(g.x, g.y, s.Args[0]), tr(g.x, g.y, s.Args[1])
				p.z.QuadTo(float32(q1.x)-ox, float32(q1.y)-oy, float32(q2.x)-ox, float32(q2.y)-oy)
			case sfnt.SegmentOpCubeTo:
				q1, q2, q3 := tr(g.x, g.y, s.Args[0]), tr(g.x, g.y, s.Args[1]), tr(g.x, g.y, s.Args[2])
				p.z.CubeTo(float32(q1.x)-ox, float32(q1.y)-oy, float32(q2.x)-ox, float32(q2.y)-oy, float32(q3.x)-ox, float32(q3.y)-oy)
			}
		}
		if open {
			p.z.ClosePath()
		}
	}
	p.z.Draw(p.dst, box, src, box.Min)
}

// a picture, object-fit: cover, or the crop window the command names
func (p *painter) picture(c *dlCmd) {
	img := p.pics.get(c.Src)
	if img == nil || c.W <= 0 || c.H <= 0 {
		return
	}
	b := img.Bounds()
	sw, sh := float64(b.Dx()), float64(b.Dy())
	if sw <= 0 || sh <= 0 {
		return
	}
	u0, v0, u1, v1 := 0.0, 0.0, 1.0, 1.0
	if len(c.CU) >= 4 {
		u0, v0, u1, v1 = c.CU[0], c.CU[1], c.CU[2], c.CU[3]
	} else if src, box := sw/sh, c.W/c.H; src > box {
		f := box / src
		u0, u1 = (1-f)/2, 1-(1-f)/2
	} else {
		f := src / box
		v0, v1 = (1-f)/2, 1-(1-f)/2
	}
	sr := image.Rect(b.Min.X+int(u0*sw), b.Min.Y+int(v0*sh), b.Min.X+int(math.Ceil(u1*sw)), b.Min.Y+int(math.Ceil(v1*sh)))
	a, z := p.dev(c.X, c.Y), p.dev(c.X+c.W, c.Y+c.H)
	dr := image.Rect(int(math.Round(a.x)), int(math.Round(a.y)), int(math.Round(z.x)), int(math.Round(z.y)))
	if dr.Empty() || sr.Empty() || dr.Intersect(p.clip()).Empty() {
		return
	}
	// scaled into a picture of its own, mirrored there, then laid over
	tmp := image.NewRGBA(image.Rect(0, 0, dr.Dx(), dr.Dy()))
	draw.ApproxBiLinear.Scale(tmp, tmp.Bounds(), img, sr, draw.Src, nil)
	if c.FX || c.FY {
		w, h := tmp.Bounds().Dx(), tmp.Bounds().Dy()
		m := image.NewRGBA(tmp.Bounds())
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				sx, sy := x, y
				if c.FX {
					sx = w - 1 - x
				}
				if c.FY {
					sy = h - 1 - y
				}
				m.SetRGBA(x, y, tmp.RGBAAt(sx, sy))
			}
		}
		tmp = m
	}
	alpha := 1.0
	if len(c.C) >= 4 {
		alpha = c.C[3]
	}
	target := dr.Intersect(p.clip())
	if alpha >= 1 {
		draw.Draw(p.dst, target, tmp, target.Min.Sub(dr.Min), draw.Over)
	} else {
		draw.DrawMask(p.dst, target, tmp, target.Min.Sub(dr.Min), image.NewUniform(color.Alpha{uint8(clamp255(alpha * 255))}), image.Point{}, draw.Over)
	}
}

func (p *painter) paint(doc *dlDoc) {
	base := len(p.clips)
	for i := range doc.Cmds {
		c := &doc.Cmds[i]
		switch c.K {
		case 0:
			if c.Efx != "" {
				p.drawFx(c.Efx, "backdrop")
			}
			p.rect(c)
			if c.Efx != "" {
				p.drawFx(c.Efx, "source")
			}
		case 1:
			p.border(c)
		case 2:
			p.picture(c)
		case 3:
			p.text(c)
		case 4:
			a, z := p.dev(c.X, c.Y), p.dev(c.X+c.W, c.Y+c.H)
			r := image.Rect(int(math.Floor(a.x)), int(math.Floor(a.y)), int(math.Ceil(z.x)), int(math.Ceil(z.y)))
			p.clips = append(p.clips, r.Intersect(p.clip()))
		case 5:
			if len(p.clips) > base {
				p.clips = p.clips[:len(p.clips)-1]
			}
		case 6:
			p.path(c)
		case 7:
			p.stroke(c)
		}
	}
	p.clips = p.clips[:base]
	// a filter rewrites the finished slide in its box
	for i := range doc.Cmds {
		if c := &doc.Cmds[i]; c.K == 0 && c.Efx != "" {
			p.drawFx(c.Efx, "filter")
		}
	}
}

// renderList paints one slide's list into a w×h-sized area of dst at off
func renderList(dst *image.RGBA, listJSON string, slideW, slideH float64, area image.Rectangle, pics *renderPics, fx *renderFx) error {
	var doc dlDoc
	if err := json.Unmarshal([]byte(listJSON), &doc); err != nil {
		return err
	}
	for i := range doc.Cmds {
		c := &doc.Cmds[i]
		c.Text, c.Font, c.Src = utf8Of(c.Text), utf8Of(c.Font), utf8Of(c.Src)
	}
	p := &painter{dst: dst, k: float64(area.Dx()) / slideW, off: pt{float64(area.Min.X), float64(area.Min.Y)}, pics: pics}
	if fx != nil && len(doc.Effects) > 0 {
		p.fx = fx
		p.fxInst = map[string]*dlFx{}
		for i := range doc.Effects {
			e := &doc.Effects[i]
			e.Kind = utf8Of(e.Kind)
			p.fxInst[e.ID] = e
		}
	}
	// the area may reach past the picture (RenderCrop draws part of a slide)
	p.clips = []image.Rectangle{area.Intersect(dst.Bounds())}
	p.paint(&doc)
	return nil
}

// utf8Of undoes how a Ranger string reaches the JSON on Go, where a
// character is a byte: each byte of UTF-8 written as its own \u00XX.
func utf8Of(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 255 {
			return s
		}
		b = append(b, byte(r))
	}
	if !utf8.Valid(b) {
		return s
	}
	return string(b)
}

func jpegBase64(img image.Image) string {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 82}); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

// --- the host operators

// renderPics: the pictures the lists may name ("/media/x.png"), decoded or
// drawn (an SVG) the first time a list draws one, so a render_slide draws
// that slide's pictures and not every picture of the deck
type renderPics struct {
	data map[string][]byte
	img  map[string]image.Image
	log  func(string)
}

func (s *renderPics) get(name string) image.Image {
	if s == nil {
		return nil
	}
	if img, ok := s.img[name]; ok {
		return img
	}
	data, ok := s.data[name]
	if !ok {
		return nil
	}
	var img image.Image
	if pic, err := decodePicture(data); err == nil {
		img = pic
	} else if pic, err := drawSvg(data, svgRenderSide); err == nil {
		img = pic
	} else if _, _, isSvg := svgSize(data); isSvg && s.log != nil {
		s.log("render " + name + ": " + err.Error())
	}
	if s.img == nil {
		s.img = map[string]image.Image{}
	}
	s.img[name] = img
	return img
}

// RenderPic hands the painter a picture the lists name ("/media/x.png").
func (h *McpHost) RenderPic(name string, data []byte) {
	if h.renderPics == nil {
		h.renderPics = &renderPics{data: map[string][]byte{}, log: h.Log}
	}
	h.renderPics.data[name] = data
	delete(h.renderPics.img, name)
}

// Render is one slide, width pixels wide, as base64 JPEG; "" when the list
// does not read.
func (h *McpHost) Render(listJSON string, slideW, slideH float64, width int64) string {
	if slideW <= 0 || slideH <= 0 || width <= 0 {
		return ""
	}
	ht := int(math.Round(float64(width) * slideH / slideW))
	dst := image.NewRGBA(image.Rect(0, 0, int(width), ht))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	if err := renderList(dst, listJSON, slideW, slideH, dst.Bounds(), h.renderPics, h.renderFx); err != nil {
		h.Log("render: " + err.Error())
		return ""
	}
	return jpegBase64(dst)
}

// RenderStill is one slide's list as JPEG bytes `width` pixels wide, as
// web/main.js renderFxStills makes a slide's background for the PPTX
// (quality 0.9). nil when the list does not read.
func (h *McpHost) RenderStill(listJSON string, slideW, slideH float64, width int64) []byte {
	if slideW <= 0 || slideH <= 0 || width <= 0 {
		return nil
	}
	ht := int(math.Round(float64(width) * slideH / slideW))
	dst := image.NewRGBA(image.Rect(0, 0, int(width), ht))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	if err := renderList(dst, listJSON, slideW, slideH, dst.Bounds(), h.renderPics, h.renderFx); err != nil {
		h.Log("render: " + err.Error())
		return nil
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, dst, &jpeg.Options{Quality: 90}); err != nil {
		return nil
	}
	return b.Bytes()
}

// RenderCrop is the (x, y, w, h) rectangle of a slide's list, in slide
// units, as a PNG `width` pixels wide (the height as w:h has it): the Word
// and web page exports' picture of a block only the stage draws
// (src/PresDocx.rgr). nil when the list does not read.
func (h *McpHost) RenderCrop(listJSON string, slideW, slideH, x, y, w, ht float64, width int64) []byte {
	if slideW <= 0 || slideH <= 0 || w <= 0 || ht <= 0 || width <= 0 {
		return nil
	}
	k := float64(width) / w
	ph := int(math.Floor(float64(width)*ht/w + 0.5))
	dst := image.NewRGBA(image.Rect(0, 0, int(width), ph))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	// the whole slide, placed so the rectangle lands on the picture
	ox, oy := int(math.Round(-x*k)), int(math.Round(-y*k))
	area := image.Rect(ox, oy, ox+int(math.Round(slideW*k)), oy+int(math.Round(slideH*k)))
	if err := renderList(dst, listJSON, slideW, slideH, area, h.renderPics, h.renderFx); err != nil {
		h.Log("render: " + err.Error())
		return nil
	}
	var b bytes.Buffer
	if err := png.Encode(&b, dst); err != nil {
		return nil
	}
	return b.Bytes()
}

// RenderGrid is every slide as a thumbnail in one picture, cols across,
// each numbered in its corner; base64 JPEG.
func (h *McpHost) RenderGrid(lists []string, slideW, slideH float64, cols, width int64) string {
	n := len(lists)
	if n == 0 || cols <= 0 || slideW <= 0 || slideH <= 0 {
		return ""
	}
	gap := 12
	tw := (int(width) - gap*(int(cols)+1)) / int(cols)
	th := int(math.Round(float64(tw) * slideH / slideW))
	rows := (n + int(cols) - 1) / int(cols)
	dst := image.NewRGBA(image.Rect(0, 0, int(width), rows*(th+gap)+gap))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.NRGBA{38, 38, 42, 255}), image.Point{}, draw.Src)
	for i, l := range lists {
		x := gap + (i%int(cols))*(tw+gap)
		y := gap + (i/int(cols))*(th+gap)
		area := image.Rect(x, y, x+tw, y+th)
		draw.Draw(dst, area, image.White, image.Point{}, draw.Src)
		if err := renderList(dst, l, slideW, slideH, area, h.renderPics, h.renderFx); err != nil {
			h.Log("render: " + err.Error())
		}
		label(dst, area, strconv.Itoa(i+1))
	}
	return jpegBase64(dst)
}

// label puts a slide's number in a dark tab at the area's top-left corner
func label(dst *image.RGBA, area image.Rectangle, s string) {
	size := 14.0
	w := 10 + 9*float64(len(s))
	tab := image.Rect(area.Min.X, area.Min.Y, area.Min.X+int(w), area.Min.Y+22)
	draw.Draw(dst, tab, image.NewUniform(color.NRGBA{20, 20, 24, 230}), image.Point{}, draw.Over)
	p := &painter{dst: dst, k: 1, pics: nil}
	p.clips = []image.Rectangle{tab}
	c := dlCmd{K: 3, X: float64(tab.Min.X) + 5, Y: float64(tab.Min.Y) + 3, H: 16, Size: size, Text: s, Font: "Open Sans-Bold", C: []float64{255, 255, 255, 1}}
	p.text(&c)
}
