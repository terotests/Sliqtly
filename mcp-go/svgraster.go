// SPDX-License-Identifier: AGPL-3.0-or-later

// An SVG picture drawn to pixels, so render_slide and render_overview show
// it and the contrast check reads it as it reads a photo. The player draws
// an SVG once to a PNG in the browser (web/picture.js decodePicture: its
// root sized to the picture, viewBox kept); this draws the same root, sized
// the same way, with resvg (svgraster/, built to WebAssembly and run by
// wazero, so the server stays one Go binary without cgo).
//
// As a picture in a browser, the SVG loads nothing from outside itself:
// pictures in it are drawn only from data: URLs, here too. Its text is
// drawn with the editor's faces (Open Sans for sans-serif and for any
// family it does not have), where a browser uses the viewer's own fonts.

package main

import (
	"bytes"
	lru "container/list"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

//go:embed svgraster.wasm
var svgRasterWasm []byte

// the longest side an SVG is drawn at here: sharper than a 960-pixel
// render_slide needs, a full-slide background included, and smaller than
// the player's 2560 (web/picture.js SVG_RASTER), which a render need not be.
// The layout report's "drawn" and the contrast grid read this same drawing,
// so what the report says is what render_slide shows.
const svgRenderSide = 1920

// how long one SVG may take to draw before it is given up as not drawn
const svgRenderTime = 10 * time.Second

// how many bytes of drawn SVGs are kept (a 1920×1080 drawing is 8 MB): a
// deck's pictures are drawn once for the check, the contrast grid and
// every render_slide after it, not once per use
const svgCacheBytes = 96 << 20

// A module's memory only grows: one big drawing (a photo SVG at 2560
// pixels takes some 180 MB) would stay held for as long as the server
// runs, in a 512 MB container. A module that has grown past this is let go
// after the call, and the next drawing starts a fresh one from the
// compiled code.
const svgKeepMemory = 64 << 20

type svgRasterizer struct {
	mu       sync.Mutex
	rt       wazero.Runtime
	compiled wazero.CompiledModule
	mod      api.Module
	err      error
}

var svgRaster svgRasterizer

// the module, compiled the first time it is needed, and an instance of it
// given the faces
func (r *svgRasterizer) start() error {
	if r.mod != nil || r.err != nil {
		return r.err
	}
	ctx := context.Background()
	if r.rt == nil {
		r.rt = wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true))
		compiled, err := r.rt.CompileModule(ctx, svgRasterWasm)
		if err != nil {
			r.err = err
			return err
		}
		r.compiled = compiled
	}
	mod, err := r.rt.InstantiateModule(ctx, r.compiled, wazero.NewModuleConfig())
	if err != nil {
		r.err = err
		return err
	}
	r.mod = mod
	// 1: also sans-serif, and serif, where a family not here goes
	for _, f := range []struct {
		name    string
		generic uint64
	}{
		{"Open Sans", 1}, {"Open Sans-Bold", 0}, {"Open Sans-Italic", 0}, {"Open Sans-BoldItalic", 0},
		{"Noto Sans", 0}, {"Noto Sans-Bold", 0}, {"Noto Sans-Italic", 0}, {"Noto Sans-BoldItalic", 0},
		{"DejaVu Sans", 0}, {"DejaVu Sans-Bold", 0}, {"Noto Emoji-Regular", 0},
	} {
		b := fontBytes(f.name)
		if b == nil {
			continue
		}
		p, err := r.put(ctx, b)
		if err != nil {
			r.err = err
			return err
		}
		if _, err := r.mod.ExportedFunction("add_font").Call(ctx, p, uint64(len(b)), f.generic); err != nil {
			r.err = err
			return err
		}
		r.mod.ExportedFunction("dealloc").Call(ctx, p, uint64(len(b)))
	}
	return nil
}

// put copies b into the module's memory; the pointer it is at
func (r *svgRasterizer) put(ctx context.Context, b []byte) (uint64, error) {
	res, err := r.mod.ExportedFunction("alloc").Call(ctx, uint64(len(b)))
	if err != nil {
		return 0, err
	}
	if !r.mod.Memory().Write(uint32(res[0]), b) {
		return 0, errors.New("svg: out of memory")
	}
	return res[0], nil
}

// call runs the module's export `fn` on `data` (and `args` after its
// pointer and length): the code it returned and the bytes it left at
// out_ptr.
func (r *svgRasterizer) call(fn string, data []byte, args ...uint64) (uint64, []byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.start(); err != nil {
		return 0, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), svgRenderTime)
	defer cancel()
	p, err := r.put(ctx, data)
	if err != nil {
		return 0, nil, err
	}
	res, err := r.mod.ExportedFunction(fn).Call(ctx, append([]uint64{p, uint64(len(data))}, args...)...)
	if err != nil {
		// a call that ran out of time closed the module: start again next
		// time, and let go of the old runtime's compiled code
		r.rt.Close(context.Background())
		r.mod, r.rt, r.compiled, r.err = nil, nil, nil, nil
		if ctx.Err() != nil {
			return 0, nil, errors.New("svg: it takes more than " + svgRenderTime.String() + " to draw")
		}
		return 0, nil, err
	}
	r.mod.ExportedFunction("dealloc").Call(ctx, p, uint64(len(data)))
	op, _ := r.mod.ExportedFunction("out_ptr").Call(ctx)
	ol, _ := r.mod.ExportedFunction("out_len").Call(ctx)
	out, ok := r.mod.Memory().Read(uint32(op[0]), uint32(ol[0]))
	if !ok {
		return 0, nil, errors.New("svg: result out of range")
	}
	// out is the module's memory: copied before the next call reuses it
	kept := append([]byte(nil), out...)
	if r.mod.Memory().Size() > svgKeepMemory {
		r.mod.Close(context.Background())
		r.mod = nil
	}
	return res[0], kept, nil
}

// draw is the SVG `data` at w×h pixels (its root already that size).
func (r *svgRasterizer) draw(data []byte, w, h int) (*image.RGBA, error) {
	code, out, err := r.call("render", data, uint64(w), uint64(h))
	if err != nil {
		return nil, err
	}
	switch code {
	case 0:
	case 1:
		return nil, errors.New("svg: " + string(out))
	default:
		return nil, errors.New("svg: bad size")
	}
	if len(out) != w*h*4 {
		return nil, errors.New("svg: short result")
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	copy(img.Pix, out) // premultiplied RGBA, as image.RGBA holds it
	return img, nil
}

// outline is the SVG `data` written again by resvg with its text turned
// into paths in the editor's faces (the ones render_slide draws text with),
// so the picture looks the same on every viewer.
func (r *svgRasterizer) outline(data []byte) (string, error) {
	code, out, err := r.call("outline_text", data)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", errors.New("svg: " + string(out))
	}
	return string(out), nil
}

// drawSvg is the SVG `data` drawn with its longer side `side` pixels, as
// the player draws it: the root sized to the SVG's own shape (svgSize), its
// viewBox kept. An error: not an SVG, or it does not draw. The same bytes
// at the same side are drawn once (svgCache).
func drawSvg(data []byte, side int) (image.Image, error) {
	key := svgKey{sha256.Sum256(data), side}
	if img, err, ok := svgDrawn.get(key); ok {
		if err != nil {
			return nil, err
		}
		return img, nil
	}
	img, err := drawSvgNow(data, side)
	svgDrawn.put(key, img, err)
	if err != nil {
		return nil, err
	}
	return img, nil
}

func drawSvgNow(data []byte, side int) (*image.RGBA, error) {
	w, h, ok := svgSize(data)
	if !ok {
		return nil, errors.New("not an SVG")
	}
	// a browser shows a picture whose root is not in the SVG namespace as
	// nothing; resvg would draw it, and the preview would show what the
	// player does not
	if _, _, _, attrs, _ := svgRootTag(data); strings.TrimSpace(attrs["xmlns"]) != "http://www.w3.org/2000/svg" {
		return nil, errors.New(`svg: its <svg> has no xmlns="http://www.w3.org/2000/svg"`)
	}
	k := float64(side) / math.Max(float64(max(w, h)), 1)
	pw, ph := max(1, int(math.Round(float64(w)*k))), max(1, int(math.Round(float64(h)*k)))
	sized, ok := svgSizedTo(data, pw, ph)
	if !ok {
		return nil, errors.New("not an SVG")
	}
	return svgRaster.draw(sized, pw, ph)
}

// blank: no pixel of img is drawn
func blank(img *image.RGBA) bool {
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 {
			return false
		}
	}
	return true
}

type svgKey struct {
	sum  [32]byte
	side int
}

type svgEntry struct {
	key svgKey
	img *image.RGBA
	err error
	png []byte // img as a PNG, once SvgPng has asked for it
}

// svgCache: drawn SVGs (and the reason one does not draw, so a picture
// that runs out of time is not drawn again on every call), the most
// recently used kept up to svgCacheBytes
type svgCache struct {
	mu    sync.Mutex
	order lru.List // of *svgEntry, most recent first
	at    map[svgKey]*lru.Element
	bytes int
}

var svgDrawn svgCache

func (c *svgCache) get(k svgKey) (*image.RGBA, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.at[k]
	if !ok {
		return nil, nil, false
	}
	c.order.MoveToFront(el)
	e := el.Value.(*svgEntry)
	return e.img, e.err, true
}

func (c *svgCache) put(k svgKey, img *image.RGBA, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.at == nil {
		c.at = map[svgKey]*lru.Element{}
	}
	if _, ok := c.at[k]; ok {
		return
	}
	if err != nil {
		img = nil
	}
	c.at[k] = c.order.PushFront(&svgEntry{key: k, img: img, err: err})
	c.bytes += entrySize(img)
	c.trim()
}

// pngOf is the PNG kept for k, nil when there is none yet
func (c *svgCache) pngOf(k svgKey) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.at[k]; ok {
		return el.Value.(*svgEntry).png
	}
	return nil
}

// keepPng keeps b as k's drawing in PNG, when k is still kept
func (c *svgCache) keepPng(k svgKey, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.at[k]; ok {
		if e := el.Value.(*svgEntry); e.png == nil {
			e.png = b
			c.bytes += len(b)
			c.trim()
		}
	}
}

func (c *svgCache) trim() {
	for c.bytes > svgCacheBytes && c.order.Len() > 1 {
		el := c.order.Back()
		e := el.Value.(*svgEntry)
		c.order.Remove(el)
		delete(c.at, e.key)
		c.bytes -= entrySize(e.img) + len(e.png)
	}
}

func entrySize(img *image.RGBA) int {
	if img == nil {
		return 64
	}
	return len(img.Pix)
}

// SvgError is host_svg_error: why the SVG does not draw, "" when it does.
// It is the drawing render_slide uses, so "drawn" in the report holds there.
func (h *McpHost) SvgError(data []byte) string {
	img, err := drawSvg(data, svgRenderSide)
	if err != nil {
		return strings.TrimPrefix(err.Error(), "svg: ")
	}
	if rgba, ok := img.(*image.RGBA); ok && blank(rgba) {
		return "every pixel of it is transparent"
	}
	return ""
}

// SvgPng is the SVG `data` drawn with its longer side `side` pixels as a
// PNG: what web/picture.js decodePicture gives the exports in the editor
// (SVG_RASTER for the slides and the PDF, SVG_FALLBACK beside the SVG in a
// PPTX). Empty when it is not an SVG or does not draw. The PNG is kept
// with the drawing: every export of a deck asks for the same pictures again.
func (h *McpHost) SvgPng(data []byte, side int64) []byte {
	img, err := drawSvg(data, int(side))
	if err != nil {
		return []byte{}
	}
	k := svgKey{sha256.Sum256(data), int(side)}
	if b := svgDrawn.pngOf(k); b != nil {
		return b
	}
	// read back at once by the export's own PNG reader: written fast,
	// not small (the default level spent seconds on a photo)
	var b bytes.Buffer
	if (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&b, img) != nil {
		return []byte{}
	}
	svgDrawn.keepPng(k, b.Bytes())
	return b.Bytes()
}

// SvgOutlined is host_svg_outlined: {"text": the SVG with its text as
// paths} or {"error": why not}.
func (h *McpHost) SvgOutlined(data []byte) string {
	text, err := svgRaster.outline(data)
	if err != nil {
		b, _ := json.Marshal(map[string]string{"error": strings.TrimPrefix(err.Error(), "svg: ")})
		return string(b)
	}
	b, _ := json.Marshal(map[string]string{"text": text})
	return string(b)
}
