// SPDX-License-Identifier: AGPL-3.0-or-later

// A deck's own effects (```fx, a theme's @effect) drawn without a GPU, for
// render_slide and render_overview. src/FxLang.rgr compiles an effect to
// register code (FxEffect.program); this runs that code once per pixel of
// the effect's box, with what the WebGL painter's plugin main does around
// it (lib/evg/gl/evg-webgl.js FX_MAIN_SOURCE / FX_MAIN_FILTER): a source is
// composited over the box after its rectangle, a backdrop rewrites what is
// behind the box before it, a filter rewrites the finished slide in the box.
//
// The library functions are the GLSL ones FxLang.libText writes, line for
// line; hash runs in float32 as a GPU does, so the noise is the player's.

package main

import (
	"encoding/json"
	"image"
	"image/color"
	"math"
)

type fxIns struct {
	op    string
	dst   int
	dims  int
	extra string
	oct   int
	args  []int
	adims []int
	vals  []float64
}

type fxProgram struct {
	name, layer string
	still       float64
	cost        int
	params      []fxParamDef
	regs, out   int
	outDims     int
	code        []fxIns
}

type fxParamDef struct {
	name          string
	value, lo, hi float64
}

// the programs of one render, and the moment they are drawn at
type renderFx struct {
	progs map[string]*fxProgram
	time  float64 // < 0: each at its still
}

// RenderFx keeps the deck's effects (FxLang.programsJson) for the renders
// that follow; "" forgets them.
func (h *McpHost) RenderFx(programs string, t float64) {
	if programs == "" || programs == "[]" {
		h.renderFx = nil
		return
	}
	progs, err := parseFxPrograms(programs)
	if err != nil {
		h.Log("render fx: " + err.Error())
		h.renderFx = nil
		return
	}
	h.renderFx = &renderFx{progs: progs, time: t}
}

func parseFxPrograms(text string) (map[string]*fxProgram, error) {
	var raw []struct {
		Name    string  `json:"name"`
		Layer   string  `json:"layer"`
		Still   float64 `json:"still"`
		Cost    int     `json:"cost"`
		Params  [][]any `json:"params"`
		Regs    int     `json:"regs"`
		Out     int     `json:"out"`
		OutDims int     `json:"outDims"`
		Code    [][]any `json:"code"`
	}
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, err
	}
	out := map[string]*fxProgram{}
	for _, r := range raw {
		p := &fxProgram{name: r.Name, layer: r.Layer, still: r.Still, cost: r.Cost, regs: r.Regs, out: r.Out, outDims: r.OutDims}
		for _, pa := range r.Params {
			if len(pa) < 4 {
				continue
			}
			name, _ := pa[0].(string)
			p.params = append(p.params, fxParamDef{name, num(pa[1]), num(pa[2]), num(pa[3])})
		}
		for _, c := range r.Code {
			if len(c) < 4 {
				continue
			}
			op, _ := c[0].(string)
			in := fxIns{op: op, dst: int(num(c[1])), dims: int(num(c[2]))}
			if s, ok := c[3].(string); ok {
				in.extra = s
			} else {
				in.oct = int(num(c[3]))
			}
			if op == "const" {
				for _, v := range c[4:] {
					in.vals = append(in.vals, num(v))
				}
			} else {
				for i := 4; i+1 < len(c); i += 2 {
					in.args = append(in.args, int(num(c[i])))
					in.adims = append(in.adims, int(num(c[i+1])))
				}
			}
			if in.dst < 0 || in.dst >= p.regs {
				continue
			}
			p.code = append(p.code, in)
		}
		if p.out < 0 || p.out >= p.regs || p.regs > 4096 {
			continue
		}
		out[p.name] = p
	}
	return out, nil
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

type vec4 [4]float64

// one run of a program over a box
type fxRun struct {
	prog *fxProgram
	reg  []vec4
	box  [4]float64 // x, y, w, h in slide units
	r    float64
	src  func(x, y float64) vec4 // the surface at a slide point, 0..1
}

func newFxRun(prog *fxProgram, inst *dlFx, t float64) *fxRun {
	run := &fxRun{prog: prog, reg: make([]vec4, prog.regs), r: inst.R}
	copy(run.box[:], inst.Box)
	if t < 0 {
		t = prog.still
	}
	run.reg[2] = vec4{run.box[2], run.box[3]}
	run.reg[3] = vec4{t}
	run.reg[5] = vec4{math.Pi}
	clock := []string{"step", "steps", "steptime", "progress"}
	ends := []float64{0, 0, prog.still, 1}
	for i, name := range clock {
		v, ok := inst.P["clock-"+name]
		if !ok {
			v = ends[i]
		}
		run.reg[6+i] = vec4{v}
	}
	for i, pd := range prog.params {
		v, ok := inst.P[pd.name]
		if !ok {
			v = pd.value
		}
		run.reg[10+i] = vec4{math.Max(pd.lo, math.Min(pd.hi, v))}
	}
	return run
}

// boxDistance is evg-webgl.js fxBoxDistance: negative inside, rounded corners
func (r *fxRun) boxDistance(px, py float64) float64 {
	hx, hy := r.box[2]/2, r.box[3]/2
	cx, cy := r.box[0]+hx, r.box[1]+hy
	rad := math.Min(r.r, math.Min(hx, hy))
	dx := math.Abs(px-cx) - (hx - rad)
	dy := math.Abs(py-cy) - (hy - rad)
	return math.Hypot(math.Max(dx, 0), math.Max(dy, 0)) + math.Min(math.Max(dx, dy), 0) - rad
}

// at runs the program for the slide point (px, py): its output, 0..1
func (r *fxRun) at(px, py float64) vec4 {
	reg := r.reg
	reg[0] = vec4{(px - r.box[0]) / math.Max(r.box[2], 1), (py - r.box[1]) / math.Max(r.box[3], 1)}
	reg[1] = vec4{px, py}
	reg[4] = vec4{r.boxDistance(px, py)}
	for i := range r.prog.code {
		in := &r.prog.code[i]
		reg[in.dst] = r.exec(in)
	}
	o := reg[r.prog.out]
	if r.prog.outDims == 3 {
		o[3] = 1
	}
	return o
}

// arg i of an instruction, component k, a float spread over a vector
func (r *fxRun) arg(in *fxIns, i, k int) float64 {
	if in.adims[i] == 1 {
		return r.reg[in.args[i]][0]
	}
	return r.reg[in.args[i]][k]
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func glslMod(x, y float64) float64 { return x - y*math.Floor(x/y) }

func fract(x float64) float64 { return x - math.Floor(x) }

func smooth(e0, e1, x float64) float64 {
	t := math.Max(0, math.Min(1, (x-e0)/(e1-e0)))
	return t * t * (3 - 2*t)
}

func sign(x float64) float64 {
	if x > 0 {
		return 1
	}
	if x < 0 {
		return -1
	}
	return 0
}

func (r *fxRun) exec(in *fxIns) vec4 {
	var o vec4
	n := in.dims
	switch in.op {
	case "const":
		for k := 0; k < len(in.vals) && k < 4; k++ {
			o[k] = in.vals[k]
		}
		return o
	case "+", "-", "*", "/":
		for k := 0; k < n; k++ {
			a, b := r.arg(in, 0, k), r.arg(in, 1, k)
			switch in.op {
			case "+":
				o[k] = a + b
			case "-":
				o[k] = a - b
			case "*":
				o[k] = a * b
			default:
				o[k] = a / b
			}
		}
		return o
	case "<", ">", "<=", ">=", "==", "!=", "&&", "||":
		a, b := r.arg(in, 0, 0), r.arg(in, 1, 0)
		switch in.op {
		case "<":
			o[0] = b2f(a < b)
		case ">":
			o[0] = b2f(a > b)
		case "<=":
			o[0] = b2f(a <= b)
		case ">=":
			o[0] = b2f(a >= b)
		case "==":
			o[0] = b2f(a == b)
		case "!=":
			o[0] = b2f(a != b)
		case "&&":
			o[0] = b2f(a != 0 && b != 0)
		default:
			o[0] = b2f(a != 0 || b != 0)
		}
		return o
	case "neg":
		for k := 0; k < n; k++ {
			o[k] = -r.arg(in, 0, k)
		}
		return o
	case "not":
		o[0] = b2f(r.arg(in, 0, 0) == 0)
		return o
	case "swz":
		src := r.reg[in.args[0]]
		for k := 0; k < len(in.extra) && k < 4; k++ {
			switch in.extra[k] {
			case 'x', 'r':
				o[k] = src[0]
			case 'y', 'g':
				o[k] = src[1]
			case 'z', 'b':
				o[k] = src[2]
			default:
				o[k] = src[3]
			}
		}
		return o
	case "sin", "cos", "tan", "asin", "acos", "abs", "floor", "ceil", "fract", "sqrt", "exp", "log", "sign":
		for k := 0; k < n; k++ {
			x := r.arg(in, 0, k)
			switch in.op {
			case "sin":
				o[k] = math.Sin(x)
			case "cos":
				o[k] = math.Cos(x)
			case "tan":
				o[k] = math.Tan(x)
			case "asin":
				o[k] = math.Asin(x)
			case "acos":
				o[k] = math.Acos(x)
			case "abs":
				o[k] = math.Abs(x)
			case "floor":
				o[k] = math.Floor(x)
			case "ceil":
				o[k] = math.Ceil(x)
			case "fract":
				o[k] = fract(x)
			case "sqrt":
				o[k] = math.Sqrt(x)
			case "exp":
				o[k] = math.Exp(x)
			case "log":
				o[k] = math.Log(x)
			default:
				o[k] = sign(x)
			}
		}
		return clean(o)
	case "normalize":
		l := r.length(in, 0)
		for k := 0; k < n; k++ {
			if l > 0 {
				o[k] = r.arg(in, 0, k) / l
			}
		}
		return o
	case "min", "max", "mod", "pow", "step":
		for k := 0; k < n; k++ {
			a, b := r.arg(in, 0, k), r.arg(in, 1, k)
			switch in.op {
			case "min":
				o[k] = math.Min(a, b)
			case "max":
				o[k] = math.Max(a, b)
			case "mod":
				o[k] = glslMod(a, b)
			case "pow":
				o[k] = math.Pow(a, b)
			default:
				o[k] = b2f(b >= a)
			}
		}
		return clean(o)
	case "clamp":
		for k := 0; k < n; k++ {
			o[k] = math.Min(math.Max(r.arg(in, 0, k), r.arg(in, 1, k)), r.arg(in, 2, k))
		}
		return o
	case "mix":
		for k := 0; k < n; k++ {
			a, b, t := r.arg(in, 0, k), r.arg(in, 1, k), r.arg(in, 2, k)
			o[k] = a + (b-a)*t
		}
		return o
	case "smoothstep":
		for k := 0; k < n; k++ {
			o[k] = smooth(r.arg(in, 0, k), r.arg(in, 1, k), r.arg(in, 2, k))
		}
		return clean(o)
	case "length":
		o[0] = r.length(in, 0)
		return o
	case "distance":
		s := 0.0
		for k := 0; k < in.adims[0]; k++ {
			d := r.arg(in, 0, k) - r.arg(in, 1, k)
			s += d * d
		}
		o[0] = math.Sqrt(s)
		return o
	case "dot":
		for k := 0; k < in.adims[0]; k++ {
			o[0] += r.arg(in, 0, k) * r.arg(in, 1, k)
		}
		return o
	case "atan":
		if len(in.args) == 2 {
			o[0] = math.Atan2(r.arg(in, 0, 0), r.arg(in, 1, 0))
		} else {
			o[0] = math.Atan(r.arg(in, 0, 0))
		}
		return o
	case "vec2", "vec3", "vec4":
		if len(in.args) == 1 && in.adims[0] == 1 {
			for k := 0; k < n; k++ {
				o[k] = r.arg(in, 0, 0)
			}
			return o
		}
		at := 0
		for i := range in.args {
			for k := 0; k < in.adims[i] && at < 4; k++ {
				o[at] = r.reg[in.args[i]][k]
				at++
			}
		}
		return o
	case "rgba":
		o = r.reg[in.args[0]]
		o[3] = r.arg(in, 1, 0)
		return o
	case "hsv":
		h, s, v := r.arg(in, 0, 0), r.arg(in, 1, 0), r.arg(in, 2, 0)
		s = math.Max(0, math.Min(1, s))
		off := [3]float64{0, 4, 2}
		for k := 0; k < 3; k++ {
			c := math.Max(0, math.Min(1, math.Abs(glslMod(h/60+off[k], 6)-3)-1))
			o[k] = v * (1 + (c-1)*s)
		}
		return o
	case "hash":
		o[0] = fxHash(r.arg(in, 0, 0), r.arg(in, 0, 1))
		return o
	case "noise":
		o[0] = fxNoise(r.arg(in, 0, 0), r.arg(in, 0, 1))
		return o
	case "fbm":
		x, y := r.arg(in, 0, 0), r.arg(in, 0, 1)
		s, a := 0.0, 0.5
		for i := 0; i < in.oct && i < 8; i++ {
			s += a * fxNoise(x, y)
			x, y = x*2.03+17.3, y*2.03+9.1
			a *= 0.5
		}
		o[0] = s
		return o
	case "voronoi":
		o[0] = fxVoronoi(r.arg(in, 0, 0), r.arg(in, 0, 1))
		return o
	case "rotate":
		a := r.arg(in, 1, 0) * math.Pi / 180
		c, s := math.Cos(a), math.Sin(a)
		x, y := r.arg(in, 0, 0), r.arg(in, 0, 1)
		o[0], o[1] = c*x-s*y, s*x+c*y
		return o
	case "source":
		if r.src == nil {
			return o
		}
		qx := math.Max(1/r.box[2], math.Min(1-1/r.box[2], r.arg(in, 0, 0)))
		qy := math.Max(1/r.box[3], math.Min(1-1/r.box[3], r.arg(in, 0, 1)))
		return r.src(r.box[0]+qx*r.box[2], r.box[1]+qy*r.box[3])
	case "select":
		if r.arg(in, 0, 0) != 0 {
			return r.reg[in.args[1]]
		}
		return r.reg[in.args[2]]
	}
	return o
}

func (r *fxRun) length(in *fxIns, i int) float64 {
	s := 0.0
	for k := 0; k < in.adims[i]; k++ {
		v := r.arg(in, i, k)
		s += v * v
	}
	return math.Sqrt(s)
}

// a NaN or an infinity (log 0, 0/0) would paint nothing sensible; a GPU
// leaves it undefined, this makes it 0
func clean(o vec4) vec4 {
	for k := range o {
		if math.IsNaN(o[k]) || math.IsInf(o[k], 0) {
			o[k] = 0
		}
	}
	return o
}

func fract32(x float32) float32 { return x - float32(math.Floor(float64(x))) }

// FxLang's fxl_hash, in float32 as the GPU computes it
func fxHash(x, y float64) float64 {
	qx := fract32(float32(x) * 127.31)
	qy := fract32(float32(y) * 311.7)
	d := qx*(qx+34.53) + qy*(qy+34.53)
	qx += d
	qy += d
	return float64(fract32(qx * qy))
}

func fxNoise(x, y float64) float64 {
	ix, iy := math.Floor(x), math.Floor(y)
	fx, fy := x-ix, y-iy
	fx = fx * fx * (3 - 2*fx)
	fy = fy * fy * (3 - 2*fy)
	a := fxHash(ix, iy)
	b := fxHash(ix+1, iy)
	c := fxHash(ix, iy+1)
	d := fxHash(ix+1, iy+1)
	ab := a + (b-a)*fx
	cd := c + (d-c)*fx
	return ab + (cd-ab)*fy
}

func fxVoronoi(x, y float64) float64 {
	ix, iy := math.Floor(x), math.Floor(y)
	fx, fy := x-ix, y-iy
	d := 8.0
	for gy := -1.0; gy <= 1; gy++ {
		for gx := -1.0; gx <= 1; gx++ {
			ox := fxHash(ix+gx, iy+gy)
			oy := fxHash(ix+gx+19.19, iy+gy+19.19)
			d = math.Min(d, math.Hypot(gx+ox-fx, gy+oy-fy))
		}
	}
	return d
}

// --- in the painter

type dlFx struct {
	ID   string             `json:"id"`
	Box  []float64          `json:"box"`
	R    float64            `json:"r"`
	P    map[string]float64 `json:"p"`
	Kind string             `json:"kind"`
}

// drawFx runs the effect of instance `id` over its box, for the layer
// asked: "source" composites over what is there, "backdrop" and "filter"
// rewrite it (FX_MAIN_FILTER, with FxLang's fxl_over)
func (p *painter) drawFx(id, layer string) {
	if p.fx == nil {
		return
	}
	inst := p.fxInst[id]
	if inst == nil || len(inst.Box) < 4 {
		return
	}
	prog := p.fx.progs[inst.Kind]
	if prog == nil || prog.layer != layer {
		return
	}
	run := newFxRun(prog, inst, p.fx.time)
	a, z := p.dev(run.box[0], run.box[1]), p.dev(run.box[0]+run.box[2], run.box[1]+run.box[3])
	area := image.Rect(int(math.Floor(a.x)), int(math.Floor(a.y)), int(math.Ceil(z.x)), int(math.Ceil(z.y))).Intersect(p.clip())
	if area.Empty() {
		return
	}
	// a big box of a costly effect is worked out on every other pixel
	stride := 1
	if float64(area.Dx()*area.Dy())*float64(prog.cost+10) > 6e7 {
		stride = 2
	}
	var snap *image.RGBA
	if layer != "source" {
		snap = image.NewRGBA(area)
		for y := area.Min.Y; y < area.Max.Y; y++ {
			i, j := snap.PixOffset(area.Min.X, y), p.dst.PixOffset(area.Min.X, y)
			copy(snap.Pix[i:i+4*area.Dx()], p.dst.Pix[j:j+4*area.Dx()])
		}
		run.src = func(x, y float64) vec4 {
			d := p.dev(x, y)
			px := image.Pt(int(math.Floor(d.x)), int(math.Floor(d.y)))
			if !px.In(area) {
				px.X = max(area.Min.X, min(area.Max.X-1, px.X))
				px.Y = max(area.Min.Y, min(area.Max.Y-1, px.Y))
			}
			c := snap.RGBAAt(px.X, px.Y)
			return vec4{float64(c.R) / 255, float64(c.G) / 255, float64(c.B) / 255, float64(c.A) / 255}
		}
	}
	for y := area.Min.Y; y < area.Max.Y; y += stride {
		for x := area.Min.X; x < area.Max.X; x += stride {
			sx := (float64(x) + 0.5*float64(stride) - p.off.x) / p.k
			sy := (float64(y) + 0.5*float64(stride) - p.off.y) / p.k
			c := run.at(sx, sy)
			cover := 1 - smooth(-0.75, 0.75, run.boxDistance(sx, sy)*p.k)
			for dy := 0; dy < stride && y+dy < area.Max.Y; dy++ {
				for dx := 0; dx < stride && x+dx < area.Max.X; dx++ {
					p.putFx(x+dx, y+dy, c, cover)
				}
			}
		}
	}
}

func unit(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// one pixel of an effect's output over what is there, its alpha times the
// box's coverage: a source's composite and a backdrop's fxl_over then
// coverage mix come to the same on an opaque surface
func (p *painter) putFx(x, y int, c vec4, cover float64) {
	here := p.dst.RGBAAt(x, y)
	hr, hg, hb := float64(here.R)/255, float64(here.G)/255, float64(here.B)/255
	a := unit(c[3]) * cover
	out := color.RGBA{
		uint8(math.Round(255 * (hr + (unit(c[0])-hr)*a))),
		uint8(math.Round(255 * (hg + (unit(c[1])-hg)*a))),
		uint8(math.Round(255 * (hb + (unit(c[2])-hb)*a))),
		255,
	}
	p.dst.SetRGBA(x, y, out)
}
