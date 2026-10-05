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
	"context"
	_ "embed"
	"errors"
	"image"
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
// the player's 2560 (web/picture.js SVG_RASTER), which a render need not be
const svgRenderSide = 1920

// how long one SVG may take to draw before it is given up as not drawn
const svgRenderTime = 10 * time.Second

type svgRasterizer struct {
	mu  sync.Mutex
	rt  wazero.Runtime
	mod api.Module
	err error
}

var svgRaster svgRasterizer

// the module, compiled and given the faces the first time it is needed
func (r *svgRasterizer) start() error {
	if r.mod != nil || r.err != nil {
		return r.err
	}
	ctx := context.Background()
	r.rt = wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true))
	mod, err := r.rt.Instantiate(ctx, svgRasterWasm)
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

// draw is the SVG `data` at w×h pixels (its root already that size).
func (r *svgRasterizer) draw(data []byte, w, h int) (*image.RGBA, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.start(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), svgRenderTime)
	defer cancel()
	p, err := r.put(ctx, data)
	if err != nil {
		return nil, err
	}
	res, err := r.mod.ExportedFunction("render").Call(ctx, p, uint64(len(data)), uint64(w), uint64(h))
	if err != nil {
		// a draw that ran out of time closed the module: start again next time
		r.mod, r.err = nil, nil
		return nil, err
	}
	r.mod.ExportedFunction("dealloc").Call(ctx, p, uint64(len(data)))
	op, _ := r.mod.ExportedFunction("out_ptr").Call(ctx)
	ol, _ := r.mod.ExportedFunction("out_len").Call(ctx)
	out, ok := r.mod.Memory().Read(uint32(op[0]), uint32(ol[0]))
	if !ok {
		return nil, errors.New("svg: result out of range")
	}
	switch res[0] {
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

// drawSvg is the SVG `data` drawn with its longer side `side` pixels, as
// the player draws it: the root sized to the SVG's own shape (svgSize), its
// viewBox kept. An error: not an SVG, or it does not draw.
func drawSvg(data []byte, side int) (image.Image, error) {
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

// SvgError is host_svg_error: why the SVG does not draw, "" when it does.
func (h *McpHost) SvgError(data []byte) string {
	if _, err := drawSvg(data, svgGridSide); err != nil {
		return strings.TrimPrefix(err.Error(), "svg: ")
	}
	return ""
}
