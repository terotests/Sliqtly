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
	"context"
	"encoding/json"
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/draw"
)

// an instruction's operator, decided once when the program is read
type fxOp uint8

const (
	opConst fxOp = iota
	opAdd
	opSub
	opMul
	opDiv
	opLt
	opGt
	opLe
	opGe
	opEq
	opNe
	opAnd
	opOr
	opNeg
	opNot
	opSwz
	opSin
	opCos
	opTan
	opAsin
	opAcos
	opAbs
	opFloor
	opCeil
	opFract
	opSqrt
	opExp
	opLog
	opSign
	opNormalize
	opMin
	opMax
	opMod
	opPow
	opStep
	opClamp
	opMix
	opSmoothstep
	opLength
	opDistance
	opDot
	opAtan
	opVec
	opRgba
	opHsv
	opHash
	opNoise
	opFbm
	opVoronoi
	opRotate
	opSource
	opBlur
	opGlow
	opEdges
	opSelect
)

// each operator by FxCode's name, with how many arguments it takes (-1:
// one or more, -2: one or two)
var fxOps = map[string]struct {
	op    fxOp
	arity int
}{
	"const": {opConst, 0}, "+": {opAdd, 2}, "-": {opSub, 2}, "*": {opMul, 2}, "/": {opDiv, 2},
	"<": {opLt, 2}, ">": {opGt, 2}, "<=": {opLe, 2}, ">=": {opGe, 2}, "==": {opEq, 2}, "!=": {opNe, 2},
	"&&": {opAnd, 2}, "||": {opOr, 2}, "neg": {opNeg, 1}, "not": {opNot, 1}, "swz": {opSwz, 1},
	"sin": {opSin, 1}, "cos": {opCos, 1}, "tan": {opTan, 1}, "asin": {opAsin, 1}, "acos": {opAcos, 1},
	"abs": {opAbs, 1}, "floor": {opFloor, 1}, "ceil": {opCeil, 1}, "fract": {opFract, 1}, "sqrt": {opSqrt, 1},
	"exp": {opExp, 1}, "log": {opLog, 1}, "sign": {opSign, 1}, "normalize": {opNormalize, 1},
	"min": {opMin, 2}, "max": {opMax, 2}, "mod": {opMod, 2}, "pow": {opPow, 2}, "step": {opStep, 2},
	"clamp": {opClamp, 3}, "mix": {opMix, 3}, "smoothstep": {opSmoothstep, 3}, "length": {opLength, 1},
	"distance": {opDistance, 2}, "dot": {opDot, 2}, "atan": {opAtan, -2},
	"vec2": {opVec, -1}, "vec3": {opVec, -1}, "vec4": {opVec, -1}, "rgba": {opRgba, 2}, "hsv": {opHsv, 3},
	"hash": {opHash, 1}, "noise": {opNoise, 1}, "fbm": {opFbm, 1}, "voronoi": {opVoronoi, 1},
	"rotate": {opRotate, 2}, "source": {opSource, 1}, "blur": {opBlur, 2}, "glow": {opGlow, 2},
	"edges": {opEdges, 1}, "select": {opSelect, 3},
}

type fxIns struct {
	op    fxOp
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
	clockEnd    [4]float64
	params      []fxParamDef
	regs, out   int
	outDims     int
	code        []fxIns
}

type fxParamDef struct {
	name          string
	value, lo, hi float64
}

// how long one request may spend drawing the deck's own effects
const fxBudget = 10 * time.Second

// the programs of one render, and the moment they are drawn at
type renderFx struct {
	progs map[string]*fxProgram
	time  float64 // < 0: each at its still
	// past it the effects not yet drawn are left out (fxBudget, or the
	// request's own end)
	ctx      context.Context
	deadline time.Time
	cut      bool
	notes    *[]string
}

// RenderFx keeps the deck's effects (FxLang.programsJson) for the renders
// that follow; "" forgets them. An effect whose code does not read is left
// out and said (FxReport).
func (h *McpHost) RenderFx(programs string, t float64) {
	if programs == "" || programs == "[]" {
		h.renderFx = nil
		return
	}
	progs, bad := parseFxPrograms(programs)
	h.fxNotes = append(h.fxNotes, bad...)
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	h.renderFx = &renderFx{progs: progs, time: t, ctx: ctx, deadline: time.Now().Add(fxBudget), notes: &h.fxNotes}
}

// FxReport is what the renders since the last report could not draw of the
// deck's own effects, one line each; "" when they drew them all.
func (h *McpHost) FxReport() string {
	seen := map[string]bool{}
	var out []string
	for _, n := range h.fxNotes {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	h.fxNotes = nil
	return strings.Join(out, "\n")
}

// parseFxPrograms reads FxLang's register code. Every index an instruction
// names is checked here, once, so running it cannot reach outside its
// registers: a program that does is left out, with the reason.
func parseFxPrograms(text string) (map[string]*fxProgram, []string) {
	var raw []struct {
		Name     string    `json:"name"`
		Layer    string    `json:"layer"`
		Still    float64   `json:"still"`
		Cost     int       `json:"cost"`
		ClockEnd []float64 `json:"clockEnd"`
		Params   [][]any   `json:"params"`
		Regs     int       `json:"regs"`
		Out      int       `json:"out"`
		OutDims  int       `json:"outDims"`
		Code     [][]any   `json:"code"`
	}
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, []string{"The deck's own effects could not be read (" + err.Error() + "); they are not drawn."}
	}
	out := map[string]*fxProgram{}
	var bad []string
	for _, r := range raw {
		p, err := readFxProgram(r.Name, r.Layer, r.Still, r.Cost, r.ClockEnd, r.Params, r.Regs, r.Out, r.OutDims, r.Code)
		if err != "" {
			bad = append(bad, "The effect '"+r.Name+"' could not be run ("+err+"); it is not drawn.")
			continue
		}
		out[p.name] = p
	}
	return out, bad
}

func readFxProgram(name, layer string, still float64, cost int, clockEnd []float64, params [][]any, regs, outReg, outDims int, code [][]any) (*fxProgram, string) {
	p := &fxProgram{name: name, layer: layer, still: still, cost: cost, regs: regs, out: outReg, outDims: outDims}
	p.clockEnd = [4]float64{0, 0, still, 1}
	if len(clockEnd) == 4 {
		copy(p.clockEnd[:], clockEnd)
	}
	for _, pa := range params {
		if len(pa) < 4 {
			return nil, "a parameter is not [name, default, lo, hi]"
		}
		n, _ := pa[0].(string)
		p.params = append(p.params, fxParamDef{n, num(pa[1]), num(pa[2]), num(pa[3])})
	}
	if regs > 4096 || regs < 10+len(p.params) {
		return nil, "registers " + strconv.Itoa(regs)
	}
	if outReg < 0 || outReg >= regs || outDims < 1 || outDims > 4 {
		return nil, "its output register"
	}
	in := func(r int) bool { return r >= 0 && r < regs }
	for at, c := range code {
		where := "instruction " + strconv.Itoa(at+1)
		if len(c) < 4 {
			return nil, where + " is short"
		}
		opName, _ := c[0].(string)
		def, ok := fxOps[opName]
		if !ok {
			return nil, where + ": no operation '" + opName + "'"
		}
		ins := fxIns{op: def.op, dst: int(num(c[1])), dims: int(num(c[2]))}
		if !in(ins.dst) || ins.dims < 1 || ins.dims > 4 {
			return nil, where + ": its result"
		}
		if s, ok := c[3].(string); ok {
			ins.extra = s
		} else {
			ins.oct = int(num(c[3]))
		}
		if def.op == opConst {
			for _, v := range c[4:] {
				ins.vals = append(ins.vals, num(v))
			}
			if len(ins.vals) > 4 {
				return nil, where + ": more than 4 numbers"
			}
		} else {
			if (len(c)-4)%2 != 0 {
				return nil, where + ": an argument without its size"
			}
			for i := 4; i+1 < len(c); i += 2 {
				a, d := int(num(c[i])), int(num(c[i+1]))
				if !in(a) || d < 1 || d > 4 {
					return nil, where + ": argument " + strconv.Itoa((i-4)/2+1)
				}
				ins.args = append(ins.args, a)
				ins.adims = append(ins.adims, d)
			}
			n := len(ins.args)
			if (def.arity > 0 && n != def.arity) || (def.arity == -1 && n < 1) || (def.arity == -2 && (n < 1 || n > 2)) {
				return nil, where + ": " + opName + " with " + strconv.Itoa(n) + " arguments"
			}
		}
		p.code = append(p.code, ins)
	}
	return p, ""
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
	for i, name := range clock {
		v, ok := inst.P["clock-"+name]
		if !ok {
			v = prog.clockEnd[i]
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
	case opConst:
		copy(o[:], in.vals)
		return o
	case opAdd:
		for k := 0; k < n; k++ {
			o[k] = r.arg(in, 0, k) + r.arg(in, 1, k)
		}
		return o
	case opSub:
		for k := 0; k < n; k++ {
			o[k] = r.arg(in, 0, k) - r.arg(in, 1, k)
		}
		return o
	case opMul:
		for k := 0; k < n; k++ {
			o[k] = r.arg(in, 0, k) * r.arg(in, 1, k)
		}
		return o
	case opDiv:
		for k := 0; k < n; k++ {
			o[k] = r.arg(in, 0, k) / r.arg(in, 1, k)
		}
		return o
	case opLt, opGt, opLe, opGe, opEq, opNe, opAnd, opOr:
		a, b := r.arg(in, 0, 0), r.arg(in, 1, 0)
		switch in.op {
		case opLt:
			o[0] = b2f(a < b)
		case opGt:
			o[0] = b2f(a > b)
		case opLe:
			o[0] = b2f(a <= b)
		case opGe:
			o[0] = b2f(a >= b)
		case opEq:
			o[0] = b2f(a == b)
		case opNe:
			o[0] = b2f(a != b)
		case opAnd:
			o[0] = b2f(a != 0 && b != 0)
		default:
			o[0] = b2f(a != 0 || b != 0)
		}
		return o
	case opNeg:
		for k := 0; k < n; k++ {
			o[k] = -r.arg(in, 0, k)
		}
		return o
	case opNot:
		o[0] = b2f(r.arg(in, 0, 0) == 0)
		return o
	case opSwz:
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
	case opSin, opCos, opTan, opAsin, opAcos, opAbs, opFloor, opCeil, opFract, opSqrt, opExp, opLog, opSign:
		f := unaryFns[in.op]
		for k := 0; k < n; k++ {
			o[k] = f(r.arg(in, 0, k))
		}
		return clean(o)
	case opNormalize:
		l := r.length(in, 0)
		for k := 0; k < n; k++ {
			if l > 0 {
				o[k] = r.arg(in, 0, k) / l
			}
		}
		return o
	case opMin, opMax, opMod, opPow, opStep:
		for k := 0; k < n; k++ {
			a, b := r.arg(in, 0, k), r.arg(in, 1, k)
			switch in.op {
			case opMin:
				o[k] = math.Min(a, b)
			case opMax:
				o[k] = math.Max(a, b)
			case opMod:
				o[k] = glslMod(a, b)
			case opPow:
				o[k] = math.Pow(a, b)
			default:
				o[k] = b2f(b >= a)
			}
		}
		return clean(o)
	case opClamp:
		for k := 0; k < n; k++ {
			o[k] = math.Min(math.Max(r.arg(in, 0, k), r.arg(in, 1, k)), r.arg(in, 2, k))
		}
		return o
	case opMix:
		for k := 0; k < n; k++ {
			a, b, t := r.arg(in, 0, k), r.arg(in, 1, k), r.arg(in, 2, k)
			o[k] = a + (b-a)*t
		}
		return o
	case opSmoothstep:
		for k := 0; k < n; k++ {
			o[k] = smooth(r.arg(in, 0, k), r.arg(in, 1, k), r.arg(in, 2, k))
		}
		return clean(o)
	case opLength:
		o[0] = r.length(in, 0)
		return o
	case opDistance:
		s := 0.0
		for k := 0; k < in.adims[0]; k++ {
			d := r.arg(in, 0, k) - r.arg(in, 1, k)
			s += d * d
		}
		o[0] = math.Sqrt(s)
		return o
	case opDot:
		for k := 0; k < in.adims[0]; k++ {
			o[0] += r.arg(in, 0, k) * r.arg(in, 1, k)
		}
		return o
	case opAtan:
		if len(in.args) == 2 {
			o[0] = math.Atan2(r.arg(in, 0, 0), r.arg(in, 1, 0))
		} else {
			o[0] = math.Atan(r.arg(in, 0, 0))
		}
		return o
	case opVec:
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
	case opRgba:
		o = r.reg[in.args[0]]
		o[3] = r.arg(in, 1, 0)
		return o
	case opHsv:
		h, s, v := r.arg(in, 0, 0), r.arg(in, 1, 0), r.arg(in, 2, 0)
		s = math.Max(0, math.Min(1, s))
		off := [3]float64{0, 4, 2}
		for k := 0; k < 3; k++ {
			c := math.Max(0, math.Min(1, math.Abs(glslMod(h/60+off[k], 6)-3)-1))
			o[k] = v * (1 + (c-1)*s)
		}
		return o
	case opHash:
		o[0] = fxHash(r.arg(in, 0, 0), r.arg(in, 0, 1))
		return o
	case opNoise:
		o[0] = fxNoise(r.arg(in, 0, 0), r.arg(in, 0, 1))
		return o
	case opFbm:
		x, y := r.arg(in, 0, 0), r.arg(in, 0, 1)
		s, a := 0.0, 0.5
		for i := 0; i < in.oct && i < 8; i++ {
			s += a * fxNoise(x, y)
			x, y = x*2.03+17.3, y*2.03+9.1
			a *= 0.5
		}
		o[0] = s
		return o
	case opVoronoi:
		o[0] = fxVoronoi(r.arg(in, 0, 0), r.arg(in, 0, 1))
		return o
	case opRotate:
		a := r.arg(in, 1, 0) * math.Pi / 180
		c, s := math.Cos(a), math.Sin(a)
		x, y := r.arg(in, 0, 0), r.arg(in, 0, 1)
		o[0], o[1] = c*x-s*y, s*x+c*y
		return o
	case opSource:
		return r.sample(r.arg(in, 0, 0), r.arg(in, 0, 1))
	case opBlur:
		return r.blur(r.arg(in, 0, 0), r.arg(in, 0, 1), r.arg(in, 1, 0))
	case opGlow:
		qx, qy := r.arg(in, 0, 0), r.arg(in, 0, 1)
		s, b := r.sample(qx, qy), r.blur(qx, qy, r.arg(in, 1, 0))
		k := smooth(0.25, 0.9, lum(b)) * 1.2
		return vec4{s[0] + b[0]*k, s[1] + b[1]*k, s[2] + b[2]*k, s[3]}
	case opEdges:
		o[0] = r.edges(r.arg(in, 0, 0), r.arg(in, 0, 1))
		return o
	case opSelect:
		if r.arg(in, 0, 0) != 0 {
			return r.reg[in.args[1]]
		}
		return r.reg[in.args[2]]
	}
	return o
}

var unaryFns = [...]func(float64) float64{
	opSin: math.Sin, opCos: math.Cos, opTan: math.Tan, opAsin: math.Asin, opAcos: math.Acos,
	opAbs: math.Abs, opFloor: math.Floor, opCeil: math.Ceil, opFract: fract, opSqrt: math.Sqrt,
	opExp: math.Exp, opLog: math.Log, opSign: sign,
}

// sample is fxl_source: the surface at uv, held inside the box
func (r *fxRun) sample(qx, qy float64) vec4 {
	if r.src == nil {
		return vec4{}
	}
	qx = math.Max(1/r.box[2], math.Min(1-1/r.box[2], qx))
	qy = math.Max(1/r.box[3], math.Min(1-1/r.box[3], qy))
	return r.src(r.box[0]+qx*r.box[2], r.box[1]+qy*r.box[3])
}

// blur is fxl_blur: the point and two rings of eight
func (r *fxRun) blur(qx, qy, rad float64) vec4 {
	sx, sy := math.Max(rad, 0)/r.box[2], math.Max(rad, 0)/r.box[3]
	c := r.sample(qx, qy)
	for k := range c {
		c[k] *= 0.2
	}
	for i := 0; i < 8; i++ {
		a := float64(i) * 0.785398
		dx, dy := math.Cos(a)*sx, math.Sin(a)*sy
		far, near := r.sample(qx+dx, qy+dy), r.sample(qx+dx*0.5, qy+dy*0.5)
		for k := range c {
			c[k] += far[k]*0.06 + near[k]*0.04
		}
	}
	return c
}

func lum(c vec4) float64 { return c[0]*0.2126 + c[1]*0.7152 + c[2]*0.0722 }

// edges is fxl_edges: the Sobel of the luminance, 0..1
func (r *fxRun) edges(qx, qy float64) float64 {
	ex, ey := 1/r.box[2], 1/r.box[3]
	l := func(dx, dy float64) float64 { return lum(r.sample(qx+dx*ex, qy+dy*ey)) }
	tl, t, tr := l(-1, -1), l(0, -1), l(1, -1)
	ml, mr := l(-1, 0), l(1, 0)
	bl, b, br := l(-1, 1), l(0, 1), l(1, 1)
	gx := (tr + 2*mr + br) - (tl + 2*ml + bl)
	gy := (bl + 2*b + br) - (tl + 2*t + tr)
	return math.Min(1, math.Hypot(gx, gy))
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
	if p.fx.cut {
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
		if p.fx.overTime() {
			return
		}
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

// overTime says once that the effects ran out of time, and from then on
// that they did: what is left is drawn without them
func (f *renderFx) overTime() bool {
	if f.cut {
		return true
	}
	if time.Now().Before(f.deadline) && f.ctx.Err() == nil {
		return false
	}
	f.cut = true
	if f.notes != nil {
		*f.notes = append(*f.notes, "The deck's own effects took longer than "+strconv.Itoa(int(fxBudget/time.Second))+
			" s to draw here and were cut short; the rest of the picture is drawn without them. A smaller or cheaper effect draws in full.")
	}
	return true
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

// --- for the check

// FxGrid is a slide's effect (PresDeck.fxOnlyList) drawn at its still, as
// the contrast check samples a picture (ImageGrid): width, height, then the
// grid; empty without the deck's effects.
func (h *McpHost) FxGrid(listJSON string, slideW, slideH float64) []int64 {
	if h.renderFx == nil || slideW <= 0 || slideH <= 0 {
		return []int64{}
	}
	w := 480
	dst := image.NewRGBA(image.Rect(0, 0, w, int(math.Round(float64(w)*slideH/slideW))))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	if err := renderList(dst, listJSON, slideW, slideH, dst.Bounds(), h.renderPics, h.renderFx); err != nil {
		return []int64{}
	}
	return lumaGrid(dst)
}

// FxNotes is what the check says of the own effects on a slide's list:
// WCAG 2.3.1's general flash (a pair of opposing changes of 10% of relative
// luminance, the darker under 0.8, more than three a second over a tenth
// of the slide or more; each box sampled on an 8×6 grid for two seconds at
// 30 frames a second, a backdrop reading a grey page), and a backdrop or
// filter whose output lets the slide through (alpha under 1), which since
// 2026-10-09 leaves the slide as it was where it used to clear it.
func (h *McpHost) FxNotes(listJSON string, slideW, slideH float64) []string {
	out := []string{}
	if h.renderFx == nil || slideW <= 0 || slideH <= 0 {
		return out
	}
	var doc dlDoc
	if err := json.Unmarshal([]byte(listJSON), &doc); err != nil {
		return out
	}
	flashed, seen := false, map[string]bool{}
	for i := range doc.Effects {
		inst := &doc.Effects[i]
		prog := h.renderFx.progs[utf8Of(inst.Kind)]
		if prog == nil || len(inst.Box) < 4 {
			continue
		}
		if rate, share := fxFlashRate(prog, inst, slideW*slideH); share >= 0.1 && !flashed {
			flashed = true
			out = append(out, "the effect '"+prog.name+"' flashes about "+strconv.Itoa(int(math.Round(rate)))+
				" times a second over "+strconv.Itoa(int(math.Round(share*100)))+
				"% of the slide; more than 3 a second can cause seizures (WCAG 2.3.1). Slow it down or keep it to a small part of the slide.")
		}
		if prog.layer != "source" && !seen[prog.name] && fxSeeThrough(prog, inst) {
			seen[prog.name] = true
			out = append(out, "The "+prog.layer+" effect '"+prog.name+"' gives alpha under 1, so there the slide shows through: "+
				"alpha is how much of the effect's colour covers the slide, and rgba(c, 0) leaves it as it was. "+
				"Until 2026-10-09 alpha 0 cleared the slide instead; to hide the slide, give the colour to show with alpha 1 (vec3 or rgba(c, 1)).")
		}
	}
	return out
}

// fxSeeThrough is whether a backdrop or filter gives alpha under 1 anywhere
// on an 8×6 grid of its box, from 0 s to two seconds past its still, over
// an opaque grey page
func fxSeeThrough(prog *fxProgram, inst *dlFx) bool {
	run := newFxRun(prog, inst, prog.still)
	run.src = func(x, y float64) vec4 { return vec4{0.5, 0.5, 0.5, 1} }
	for t := 0.0; t <= math.Max(prog.still, 0)+2; t += 0.25 {
		run.reg[3] = vec4{t}
		for j := 0; j < 6; j++ {
			for i := 0; i < 8; i++ {
				c := run.at(run.box[0]+(float64(i)+0.5)*run.box[2]/8, run.box[1]+(float64(j)+0.5)*run.box[3]/6)
				if c[3] < 0.98 {
					return true
				}
			}
		}
	}
	return false
}

// fxFlashRate is the most flashes a second any flashing point of the box
// shows, and the share of the slide the flashing points stand for
func fxFlashRate(prog *fxProgram, inst *dlFx, slideArea float64) (float64, float64) {
	const cols, rows, fps, seconds = 8, 6, 30, 2
	run := newFxRun(prog, inst, prog.still)
	run.src = func(x, y float64) vec4 { return vec4{0.5, 0.5, 0.5, 1} }
	cell := run.box[2] * run.box[3] / (cols * rows)
	worst, flashing := 0.0, 0
	for j := 0; j < rows; j++ {
		for i := 0; i < cols; i++ {
			px := run.box[0] + (float64(i)+0.5)*run.box[2]/cols
			py := run.box[1] + (float64(j)+0.5)*run.box[3]/rows
			var lums []float64
			for f := 0; f < fps*seconds; f++ {
				run.reg[3] = vec4{prog.still + float64(f)/fps}
				c := run.at(px, py)
				a := unit(c[3])
				lums = append(lums, relLum(unit(c[0])*a+0.5*(1-a), unit(c[1])*a+0.5*(1-a), unit(c[2])*a+0.5*(1-a)))
			}
			if r := flashesPerSecond(lums, fps); r > 3 {
				flashing++
				worst = math.Max(worst, r)
			}
		}
	}
	return worst, float64(flashing) * cell / slideArea
}

func relLum(r, g, b float64) float64 {
	lin := func(c float64) float64 {
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// flashesPerSecond counts the opposing changes in a luminance series (a
// change: 0.1 or more from the last turn, the darker under 0.8) and gives
// the most flashes (two changes each) in any one second
func flashesPerSecond(lums []float64, fps int) float64 {
	var at []int
	ref, dir := lums[0], 0
	for i, l := range lums {
		d := l - ref
		switch {
		case dir >= 0 && d <= -0.1 && math.Min(l, ref) < 0.8:
			at, ref, dir = append(at, i), l, -1
		case dir <= 0 && d >= 0.1 && math.Min(l, ref) < 0.8:
			at, ref, dir = append(at, i), l, 1
		case (dir > 0 && l > ref) || (dir < 0 && l < ref):
			ref = l
		}
	}
	most := 0
	for i := range at {
		n := 0
		for k := i; k < len(at) && at[k]-at[i] < fps; k++ {
			n++
		}
		most = max(most, n)
	}
	return float64(most) / 2
}
