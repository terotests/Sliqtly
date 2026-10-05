// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The SVG compatibility benchmark (bench/svgcompat/README.md): each SVG of
// a test suite drawn by the preview (drawSvg) and compared with Chromium's
// drawing of it as the player makes it (bench/svgcompat/chrome.mjs). Runs
// only when SVGCOMPAT_SUITE (the .svg files) and SVGCOMPAT_CHROME (their
// PNGs) are set; writes results.json and the preview's PNGs to
// SVGCOMPAT_OUT.
//
//	SVGCOMPAT_SUITE=… SVGCOMPAT_CHROME=… SVGCOMPAT_OUT=… go test -run SvgCompat -v .

// a pixel differs when a channel, over white, is more than this apart:
// anti-aliasing at edges stays under it, a wrong colour or a missing shape
// does not
const compatTolerance = 48

type compatResult struct {
	Name     string  `json:"name"`
	Chapter  string  `json:"chapter"`
	Diff     float64 `json:"diff_percent"`
	Class    string  `json:"class"`
	Error    string  `json:"error,omitempty"`
	Millis   float64 `json:"ms"`
	Chromium string  `json:"chromium,omitempty"`
}

func overWhite(img image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	if img != nil {
		draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Over)
	}
	return dst
}

// the share of pixels that differ, 0..100: a pixel of one picture differs
// when no pixel of the other within one pixel of it has its colour, both
// ways, so text and edges set a pixel apart (another rasteriser's rounding)
// do not count, and a wrong colour or a missing shape does
func diffPercent(a, b *image.RGBA) float64 {
	w, h := a.Bounds().Dx(), a.Bounds().Dy()
	if w == 0 || h == 0 || b.Bounds().Dx() != w || b.Bounds().Dy() != h {
		return 100
	}
	near := func(p, q *image.RGBA, x, y int) bool {
		i := p.PixOffset(x, y)
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				qx, qy := x+dx, y+dy
				if qx < 0 || qy < 0 || qx >= w || qy >= h {
					continue
				}
				j := q.PixOffset(qx, qy)
				ok := true
				for k := 0; k < 3; k++ {
					d := int(p.Pix[i+k]) - int(q.Pix[j+k])
					if d > compatTolerance || d < -compatTolerance {
						ok = false
						break
					}
				}
				if ok {
					return true
				}
			}
		}
		return false
	}
	bad := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !near(a, b, x, y) || !near(b, a, x, y) {
				bad++
			}
		}
	}
	return 100 * float64(bad) / float64(w*h)
}

// same: as good as identical; close: the same picture, text and edges a
// little apart (looked at by eye); differs: something is drawn differently
func compatClass(d float64) string {
	switch {
	case d <= 1:
		return "same"
	case d <= 5:
		return "close"
	}
	return "differs"
}

func TestSvgCompat(t *testing.T) {
	suite, chrome, out := os.Getenv("SVGCOMPAT_SUITE"), os.Getenv("SVGCOMPAT_CHROME"), os.Getenv("SVGCOMPAT_OUT")
	if suite == "" || chrome == "" {
		t.Skip("SVGCOMPAT_SUITE and SVGCOMPAT_CHROME not set")
	}
	if out == "" {
		out = t.TempDir()
	}
	os.MkdirAll(filepath.Join(out, "preview"), 0o755)
	files, _ := filepath.Glob(filepath.Join(suite, "*.svg"))
	sort.Strings(files)
	// the first draw compiles the module; it is not the SVG's time
	drawSvg([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"/>`), 8)
	var results []compatResult
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".svg")
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		r := compatResult{Name: name, Chapter: strings.SplitN(name, "-", 2)[0]}
		start := time.Now()
		ours, err := drawSvg(data, 480)
		r.Millis = float64(time.Since(start).Microseconds()) / 1000
		if err != nil {
			r.Error = err.Error()
		}
		base := filepath.Join(chrome, name)
		var theirs image.Image
		if fh, err := os.Open(base + ".png"); err == nil {
			theirs, err = png.Decode(fh)
			fh.Close()
			if err != nil {
				t.Fatal(err)
			}
		} else {
			for _, why := range []string{"tainted", "failed"} {
				if _, err := os.Stat(base + "." + why); err == nil {
					r.Chromium = why
				}
			}
			if r.Chromium == "" {
				continue
			}
		}
		w, h := 480, 360
		if theirs != nil {
			w, h = theirs.Bounds().Dx(), theirs.Bounds().Dy()
		} else if ours != nil {
			w, h = ours.Bounds().Dx(), ours.Bounds().Dy()
		}
		a := overWhite(ours, w, h)
		b := overWhite(theirs, w, h)
		r.Diff = diffPercent(a, b)
		r.Class = compatClass(r.Diff)
		if theirs == nil {
			// the player shows nothing: so should the preview
			r.Class = "player-shows-nothing"
		}
		if ours != nil {
			if fh, err := os.Create(filepath.Join(out, "preview", name+".png")); err == nil {
				png.Encode(fh, ours)
				fh.Close()
			}
		}
		results = append(results, r)
	}
	counts := map[string]int{}
	for _, r := range results {
		counts[r.Class]++
	}
	j, _ := json.MarshalIndent(results, "", " ")
	os.WriteFile(filepath.Join(out, "results.json"), j, 0o644)
	t.Logf("%d SVGs: %v", len(results), counts)
	fmt.Fprintln(os.Stderr, "wrote", filepath.Join(out, "results.json"))
}
