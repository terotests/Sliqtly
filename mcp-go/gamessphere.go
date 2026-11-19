package main

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"sync"
)

// A textured globe of the games engine (draw kind 8, RangerGames
// crates/rg_engine/src/draw.rs `sphere`), cast as the page's WebGL shader
// casts it (web/js/painter.js): through every pixel of the box a ray from the
// camera; where it meets the ball of radius 1 the map picture's colour at
// that longitude and latitude, lit by the sun. c.Pts holds the command's 20
// numbers: eye, right, up, forward, sun (the ball's own frame), focal, the
// view's width and height, night, lit.

var gamesMaps sync.Map // image.Image → *image.RGBA

func gamesMapPixels(img image.Image) *image.RGBA {
	if m, ok := gamesMaps.Load(img); ok {
		return m.(*image.RGBA)
	}
	b := img.Bounds()
	m := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(m, m.Bounds(), img, b.Min, draw.Src)
	gamesMaps.Store(img, m)
	return m
}

func (p *painter) sphere(c *dlCmd) {
	if len(c.Pts) < 20 || c.W <= 0 || c.H <= 0 {
		return
	}
	img := p.pics.get(c.Src)
	if img == nil {
		return
	}
	tex := gamesMapPixels(img)
	tw, th := tex.Bounds().Dx(), tex.Bounds().Dy()
	if tw == 0 || th == 0 {
		return
	}
	q := c.Pts
	e := [3]float64{q[0], q[1], q[2]}
	r := [3]float64{q[3], q[4], q[5]}
	u := [3]float64{q[6], q[7], q[8]}
	f := [3]float64{q[9], q[10], q[11]}
	s := [3]float64{q[12], q[13], q[14]}
	focal, vw, vh, night, lit := q[15], q[16], q[17], q[18], q[19] > 0.5
	a, z := p.dev(c.X, c.Y), p.dev(c.X+c.W, c.Y+c.H)
	box := image.Rect(int(math.Floor(a.x)), int(math.Floor(a.y)), int(math.Ceil(z.x)), int(math.Ceil(z.y))).Intersect(p.clip())
	for dy := box.Min.Y; dy < box.Max.Y; dy++ {
		gy := (float64(dy)+0.5-p.off.y)/p.k - vh/2
		for dx := box.Min.X; dx < box.Max.X; dx++ {
			gx := (float64(dx)+0.5-p.off.x)/p.k - vw/2
			d := [3]float64{f[0]*focal + r[0]*gx - u[0]*gy, f[1]*focal + r[1]*gx - u[1]*gy, f[2]*focal + r[2]*gx - u[2]*gy}
			l := math.Sqrt(d[0]*d[0] + d[1]*d[1] + d[2]*d[2])
			d[0], d[1], d[2] = d[0]/l, d[1]/l, d[2]/l
			b := e[0]*d[0] + e[1]*d[1] + e[2]*d[2]
			qx, qy, qz := e[0]-b*d[0], e[1]-b*d[1], e[2]-b*d[2]
			disc := 1 - (qx*qx + qy*qy + qz*qz)
			if disc < 0 {
				continue
			}
			sq := math.Sqrt(disc)
			if -b < sq {
				continue
			}
			nx, ny, nz := qx-sq*d[0], qy-sq*d[1], qz-sq*d[2]
			tu := math.Atan2(ny, nx)/(2*math.Pi) + 0.5
			tv := 0.5 - math.Asin(math.Max(-1, math.Min(1, nz)))/math.Pi
			ti, tj := int(tu*float64(tw)), int(tv*float64(th))
			ti = max(0, min(tw-1, ti))
			tj = max(0, min(th-1, tj))
			o := tj*tex.Stride + ti*4
			cr, cg, cb := float64(tex.Pix[o]), float64(tex.Pix[o+1]), float64(tex.Pix[o+2])
			if lit {
				sd := nx*s[0] + ny*s[1] + nz*s[2]
				t := math.Max(0, math.Min(1, (sd+0.06)/0.36))
				k := ((1 - night) + night*t*t*(3-2*t)) * (0.8 + 0.2*math.Max(sd, 0))
				cr, cg, cb = cr*k, cg*k, cb*k
			}
			p.dst.SetRGBA(dx, dy, color.RGBA{uint8(math.Min(255, cr)), uint8(math.Min(255, cg)), uint8(math.Min(255, cb)), 255})
		}
	}
}
