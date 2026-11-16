// SPDX-License-Identifier: AGPL-3.0-or-later

// Games' screenshot_game: the game run here, in the RangerGames engine
// itself (its web build rg_web.wasm, read as /games/engine/ reads it, run
// by wazero), for a number of frames with the taps and keys asked for;
// its draw stream (crates/rg_engine/src/draw.rs, as web/js/painter.js
// paints it) is turned into the display list render.go paints, and the
// picture goes back to the assistant as a PNG with what the game logged.
//
// Emoji are drawn as Noto Emoji's outlines in the text's colour (no colour
// emoji); 3D is drawn the way the engine paints it without WebGL.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"golang.org/x/image/draw"
)

const (
	gamesShotMaxFrames = 1200 // 20 s of game time
	gamesShotTimeout   = 25 * time.Second
	gamesShotMemPages  = 2048 // 128 MB for a game (the starter takes 5)
)

// one engine compiled, kept while the build stays the same
type gamesShotEngine struct {
	mu       sync.Mutex
	sum      [32]byte
	rt       wazero.Runtime
	compiled wazero.CompiledModule
	// at most two games run at once on an instance
	slots chan struct{}
}

var gamesShots = &gamesShotEngine{slots: make(chan struct{}, 2)}

func (e *gamesShotEngine) module(ctx context.Context, wasm []byte) (wazero.Runtime, wazero.CompiledModule, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	sum := sha256.Sum256(wasm)
	if e.compiled != nil && sum == e.sum {
		return e.rt, e.compiled, nil
	}
	if e.rt != nil {
		e.rt.Close(context.Background())
		e.rt, e.compiled = nil, nil
	}
	rt := wazero.NewRuntimeWithConfig(context.Background(), wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).WithMemoryLimitPages(gamesShotMemPages))
	if _, err := wasi_snapshot_preview1.Instantiate(context.Background(), rt); err != nil {
		rt.Close(context.Background())
		return nil, nil, err
	}
	c, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		rt.Close(context.Background())
		return nil, nil, err
	}
	e.sum, e.rt, e.compiled = sum, rt, c
	return rt, c, nil
}

// what a screenshot is asked to do before it is taken
type gamesShotInput struct {
	frames int
	w, h   float64
	// per frame: taps (game coordinates) and keys
	taps map[int][][2]float64
	keys map[int][]string
}

type gamesShotResult struct {
	png  []byte
	log  string
	fail string // the script threw on a frame
}

// the running engine: its calls and memory
type gamesVM struct {
	ctx context.Context
	mod api.Module
}

func (v *gamesVM) call(name string, args ...uint64) (uint64, error) {
	f := v.mod.ExportedFunction(name)
	if f == nil {
		return 0, fmt.Errorf("the engine has no %s", name)
	}
	r, err := f.Call(v.ctx, args...)
	if err != nil {
		return 0, err
	}
	rt := f.Definition().ResultTypes()
	if len(rt) == 0 || len(r) == 0 {
		return 0, nil
	}
	// an i32 is the low half: the compiler leaves the slot's high half as
	// the argument it held
	if rt[0] == api.ValueTypeI32 {
		return uint64(uint32(r[0])), nil
	}
	return r[0], nil
}

// bytes into the module's memory: (ptr, len), freed with free
func (v *gamesVM) put(b []byte) (uint64, uint64, error) {
	p, err := v.call("rg_alloc", uint64(len(b)))
	if err != nil {
		return 0, 0, err
	}
	if !v.mod.Memory().Write(uint32(p), b) {
		return 0, 0, errors.New("engine memory")
	}
	return p, uint64(len(b)), nil
}

func (v *gamesVM) free(p, n uint64) { v.call("rg_free", p, n) }

func (v *gamesVM) withText(name string, s []byte, more ...uint64) (uint64, error) {
	p, n, err := v.put(s)
	if err != nil {
		return 0, err
	}
	defer v.free(p, n)
	return v.call(name, append([]uint64{p, n}, more...)...)
}

// bytes the module hands out with a ptr and len pair of calls
func (v *gamesVM) bytesOf(ptrFn, lenFn string) []byte {
	p, err1 := v.call(ptrFn)
	n, err2 := v.call(lenFn)
	if err1 != nil || err2 != nil || n == 0 {
		return nil
	}
	b, ok := v.mod.Memory().Read(uint32(p), uint32(n))
	if !ok {
		return nil
	}
	return append([]byte(nil), b...)
}

func (v *gamesVM) view(i int) float64 {
	r, err := v.call("rg_view", uint64(int64(i)))
	if err != nil {
		return 0
	}
	return api.DecodeF64(r)
}

func (v *gamesVM) readFile(path string) []byte {
	r, err := v.withText("rg_read", []byte(path))
	if err != nil || int32(r) < 0 {
		return nil
	}
	return v.bytesOf("rg_result_ptr", "rg_result_len")
}

// runs the game and paints its last frame
func gamesScreenshot(ctx context.Context, wasm, pack []byte, gameID string, in gamesShotInput) (*gamesShotResult, error) {
	select {
	case gamesShots.slots <- struct{}{}:
		defer func() { <-gamesShots.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, gamesShotTimeout)
	defer cancel()
	rt, compiled, err := gamesShots.module(ctx, wasm)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().
		WithName("").WithStartFunctions().WithStdout(&out).WithStderr(&out).
		WithSysWalltime().WithSysNanotime())
	if err != nil {
		return nil, err
	}
	defer mod.Close(context.Background())
	v := &gamesVM{ctx, mod}
	if mod.ExportedFunction("_initialize") != nil {
		if _, err := v.call("_initialize"); err != nil {
			return nil, err
		}
	}
	logs := []string{}
	takeLog := func() {
		if b := v.bytesOf("rg_log_ptr", "rg_log_len"); len(b) > 0 {
			logs = append(logs, strings.TrimRight(string(b), "\n"))
		}
	}
	// rg_mount takes (prefix, plen, pack, len): both in memory
	pp, pn, err := v.put([]byte("/games"))
	if err != nil {
		return nil, err
	}
	dp, dn, err := v.put(pack)
	if err != nil {
		return nil, err
	}
	r, err := v.call("rg_mount", pp, pn, dp, dn)
	v.free(pp, pn)
	v.free(dp, dn)
	if err != nil {
		return nil, err
	}
	if int32(r) < 0 {
		return nil, errors.New("the game's pack does not read")
	}
	v.call("rg_seed", api.EncodeF64(12345))
	v.call("rg_resize", api.EncodeF64(in.w), api.EncodeF64(in.h))
	started, err := v.withText("rg_start", []byte(gameID))
	if err != nil {
		return nil, err
	}
	takeLog()
	res := &gamesShotResult{}
	if started != 0 {
		res.fail = "start"
	}
	held := map[int]bool{}
	for f := 1; f <= in.frames; f++ {
		// a tap: down on its frame, up on the next
		for id := range held {
			if held[id] {
				held[id] = false
				x, y := held2xy(v, in.taps, f-1, id)
				v.call("rg_pointer", api.EncodeI32(2), api.EncodeI32(int32(id)), api.EncodeF64(x), api.EncodeF64(y))
			}
		}
		for i := range in.taps[f] {
			x, y := held2xy(v, in.taps, f, i)
			v.call("rg_pointer", api.EncodeI32(0), api.EncodeI32(int32(i)), api.EncodeF64(x), api.EncodeF64(y))
			held[i] = true
		}
		// rg_key(down, key, len): down and up on the same frame
		for _, k := range in.keys[f] {
			kp, kn, err := v.put([]byte(k))
			if err == nil {
				v.call("rg_key", 1, kp, kn)
				v.call("rg_key", 0, kp, kn)
				v.free(kp, kn)
			}
		}
		r, err := v.call("rg_frame", api.EncodeF64(1000.0/60))
		if err != nil {
			if ctx.Err() != nil {
				return nil, errGamesShotSlow
			}
			return nil, err
		}
		takeLog()
		if r != 0 && res.fail == "" {
			res.fail = fmt.Sprintf("frame %d", f)
		}
	}
	stream := gamesFloats(v)
	strs := strings.Split(string(v.bytesOf("rg_strings_ptr", "rg_strings_len")), "\x00")
	view := [5]float64{v.view(0), v.view(1), v.view(2), v.view(3), v.view(4)}
	cmds, pics := gamesDisplayList(stream, strs)
	data := map[string][]byte{}
	for _, src := range pics {
		if b := v.readFile(src); b != nil {
			data[src] = b
		}
	}
	img := gamesPaint(cmds, data, int(in.w), int(in.h), view)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	res.png = buf.Bytes()
	if s := strings.TrimSpace(out.String()); s != "" {
		logs = append(logs, s)
	}
	res.log = strings.Join(logs, "\n")
	if len(res.log) > 4000 {
		res.log = res.log[len(res.log)-4000:]
	}
	return res, nil
}

// a tap's place in the engine's screen coordinates
func held2xy(v *gamesVM, taps map[int][][2]float64, f, i int) (float64, float64) {
	l := taps[f]
	if i >= len(l) {
		return 0, 0
	}
	s, ox, oy := v.view(0), v.view(1), v.view(2)
	if s <= 0 {
		s = 1
	}
	return ox + l[i][0]*s, oy + l[i][1]*s
}

// the frame's draw stream as numbers (rg_draw_len counts f32s)
func gamesFloats(v *gamesVM) []float32 {
	p, err1 := v.call("rg_draw_ptr")
	n, err2 := v.call("rg_draw_len")
	if err1 != nil || err2 != nil || n == 0 {
		return nil
	}
	b, ok := v.mod.Memory().Read(uint32(p), uint32(n*4))
	if !ok {
		return nil
	}
	out := make([]float32, n)
	for i := range out {
		u := uint32(b[i*4]) | uint32(b[i*4+1])<<8 | uint32(b[i*4+2])<<16 | uint32(b[i*4+3])<<24
		out[i] = math.Float32frombits(u)
	}
	return out
}

// The draw stream (web/js/painter.js reads it the same way) as render.go's
// display list, and the pictures it names.
func gamesDisplayList(nums []float32, strs []string) ([]dlCmd, []string) {
	const header = 23
	str := func(i float64) string {
		if i < 0 || int(i) >= len(strs) {
			return ""
		}
		return strs[int(i)]
	}
	var out []dlCmd
	var pics []string
	seen := map[string]bool{}
	f := func(i int) float64 { return float64(nums[i]) }
	for i := 0; i+header <= len(nums); {
		k := int(f(i))
		x, y, w, h := f(i+1), f(i+2), f(i+3), f(i+4)
		col := []float64{f(i + 5), f(i + 6), f(i + 7), f(i + 8)}
		rc := []float64{f(i + 9), f(i + 10), f(i + 11), f(i + 12)}
		t, rot, rox, roy := f(i+13), f(i+14), f(i+15), f(i+16)
		flags := int(f(i + 17))
		s0, s1, s2, ls := f(i+18), f(i+19), f(i+20), f(i+21)
		nx := int(f(i + 22))
		e := i + header
		next := e + nx
		if next > len(nums) || nx < 0 {
			break
		}
		c := dlCmd{K: k, X: x, Y: y, W: w, H: h, RC: rc, T: t, C: col, Rot: rot}
		if flags&1 != 0 && e+4 <= next {
			gd := 0
			if flags&2 != 0 {
				gd = 1
			}
			c.GD, c.C2 = &gd, []float64{f(e), f(e + 1), f(e + 2), f(e + 3)}
			e += 4
		}
		if flags&4 != 0 && e+7 <= next {
			c.Sh = &dlShadow{X: f(e), Y: f(e + 1), Blur: f(e + 2), C: []float64{f(e + 3), f(e + 4), f(e + 5), f(e + 6)}}
			e += 7
		}
		if flags&64 != 0 && e+4 <= next {
			c.CU = []float64{f(e), f(e + 1), f(e + 2), f(e + 3)}
			e += 4
		}
		if flags&1024 != 0 {
			e += 4 // tiles: drawn as one picture
		}
		if flags&128 != 0 {
			c.Rox, c.Roy = &rox, &roy
		}
		c.FX, c.FY = flags&8 != 0, flags&16 != 0
		switch k {
		case 2:
			c.Src = str(s0)
			if c.Src != "" && !seen[c.Src] {
				seen[c.Src] = true
				pics = append(pics, c.Src)
			}
		case 3:
			c.Text, c.Font, c.Weight, c.Size, c.LS = str(s0), str(s1), str(s2), t, ls
			c.Italic = flags&256 != 0
			c.H = h
			if rot != 0 && flags&128 == 0 {
				cx, cy := x+w/2, y+h/2
				c.Rox, c.Roy = &cx, &cy
			}
		case 6, 7:
			if e < next {
				np := int(f(e))
				pts := e + 1
				if np >= 0 && pts+np < next {
					for j := 0; j < np; j++ {
						c.Pts = append(c.Pts, f(pts+j))
					}
					ne := int(f(pts + np))
					for j := 0; j < ne && pts+np+1+j < next; j++ {
						c.Ends = append(c.Ends, int(f(pts+np+1+j)))
					}
				}
			}
			if flags&32 != 0 {
				c.EO = 1
			}
		}
		out = append(out, c)
		i = next
	}
	return out, pics
}

// paints the list as the page does: black, then the game's area
func gamesPaint(cmds []dlCmd, pics map[string][]byte, w, h int, view [5]float64) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	scale := view[0]
	if scale <= 0 {
		scale = 1
	}
	area := image.Rect(int(view[1]), int(view[2]), int(math.Round(view[1]+view[3]*scale)), int(math.Round(view[2]+view[4]*scale)))
	p := &painter{dst: dst, k: scale, off: pt{view[1], view[2]}, pics: &renderPics{data: pics}}
	p.clips = []image.Rectangle{area.Intersect(dst.Bounds())}
	p.paint(&dlDoc{Cmds: cmds})
	return dst
}

func gamesPNGBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// what goes beside the picture
func (r *gamesShotResult) text() string {
	var b strings.Builder
	switch {
	case r.fail == "start":
		b.WriteString("The game did not start (an error in game.tsx or game.json; see the log).\n")
	case r.fail != "":
		b.WriteString("The game's code threw an error on " + r.fail + " (see the log).\n")
	default:
		b.WriteString("The game ran without errors.\n")
	}
	if r.log != "" {
		b.WriteString("Log:\n" + r.log)
	} else {
		b.WriteString("Nothing was logged.")
	}
	return b.String()
}

// screenshot_game: the owner's game run here for the frames asked
func (s *gamesService) screenshot(ctx context.Context, g *gamesGame, a map[string]any) (*gamesShotResult, error) {
	in, err := gamesShotArgs(a)
	if err != nil {
		return nil, err
	}
	pack, err := s.pack(ctx, g)
	if err != nil {
		return nil, err
	}
	if s.engine == nil {
		return nil, roomErr{"Screenshots are not available here."}
	}
	wasm, err := s.engine.get(ctx, s.engineSrc, "rg_web.wasm")
	if err != nil {
		return nil, err
	}
	res, err := gamesScreenshot(ctx, wasm, pack, g.id, in)
	if err != nil {
		if errors.Is(err, errGamesShotSlow) {
			return nil, roomErr{"The game took too long to run; ask for fewer frames."}
		}
		return nil, err
	}
	return res, nil
}

var errGamesShotSlow = errors.New("the game took too long to run")

func gamesShotArgs(a map[string]any) (gamesShotInput, error) {
	num := func(k string, def, lo, hi float64) (float64, error) {
		v, ok := a[k]
		if !ok || v == nil {
			return def, nil
		}
		f, ok := v.(float64)
		if !ok || f < lo || f > hi || math.IsNaN(f) {
			return 0, roomErr{fmt.Sprintf("%s: a number from %g to %g.", k, lo, hi)}
		}
		return math.Round(f), nil
	}
	in := gamesShotInput{taps: map[int][][2]float64{}, keys: map[int][]string{}}
	fr, err := num("frames", 30, 1, gamesShotMaxFrames)
	if err != nil {
		return in, err
	}
	if in.w, err = num("width", 360, 160, 1280); err != nil {
		return in, err
	}
	if in.h, err = num("height", 640, 160, 1280); err != nil {
		return in, err
	}
	in.frames = int(fr)
	taps, _ := a["taps"].([]any)
	keys, _ := a["keys"].([]any)
	if len(taps)+len(keys) > 200 {
		return in, roomErr{"taps and keys: up to 200 in all."}
	}
	for _, t := range taps {
		m, _ := t.(map[string]any)
		x, ok1 := m["x"].(float64)
		y, ok2 := m["y"].(float64)
		f, ok3 := m["frame"].(float64)
		if !ok1 || !ok2 || !ok3 || f < 1 || int(f) > in.frames {
			return in, roomErr{"taps: each {x, y, frame} with frame from 1 to frames."}
		}
		in.taps[int(f)] = append(in.taps[int(f)], [2]float64{x, y})
	}
	for _, k := range keys {
		m, _ := k.(map[string]any)
		key, _ := m["key"].(string)
		f, ok := m["frame"].(float64)
		if key == "" || len(key) > 32 || !ok || f < 1 || int(f) > in.frames {
			return in, roomErr{"keys: each {key, frame} with frame from 1 to frames."}
		}
		in.keys[int(f)] = append(in.keys[int(f)], key)
	}
	return in, nil
}
