// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- the layout report on its own (rgr/Report.rgr)

func block(kind, label string, x, y, w, h float64) *RBlock {
	b := CreateNew_RBlock()
	b.kind, b.label, b.x, b.y, b.w, b.h = kind, label, x, y, w, h
	return b
}

func run(text string, x, y, w, h, size, rot float64) *RRun {
	r := CreateNew_RRun()
	r.text, r.x, r.y, r.w, r.h, r.size, r.rot = text, x, y, w, h, size, rot
	r.px, r.py = x+w/2, y+h/2
	return r
}

func slideOf(blocks ...*RBlock) *RSlide {
	s := CreateNew_RSlide()
	s.title = "Test"
	s.w, s.h = 960, 540
	s.blocks = blocks
	return s
}

func flagsOf(s *RSlide) string { return strings.Join(Report_static_slide(s).flags, "\n") }

func TestReportCleanSlideHasNoFlags(t *testing.T) {
	h := block("heading", "Title", 54, 64, 852, 55)
	h.runs = append(h.runs, run("Title", 54, 64, 200, 36, 36, 0))
	c := block("chart", "Vega-Lite", 60, 140, 840, 340)
	for i := 0; i < 6; i++ {
		c.runs = append(c.runs, run(fmt.Sprintf("Q%d", i+1), 100+float64(i)*130, 460, 20, 16, 13, 0))
	}
	o := Report_static_slide(slideOf(h, c))
	if len(o.flags) > 0 {
		t.Fatal(o.flags)
	}
	// positions and sizes in px of a 1920×1080 screen: twice the slide's
	match(t, o.lines[0], `^Slide 1 "Test": the elements cover \d+% of the slide, from 108,128 to 1812,960; smallest text 26 px\.$`)
	match(t, o.lines[1], `^- heading "Title" at 108,128 size 1704×110, text 72 px$`)
	match(t, o.lines[2], `^- chart \(Vega-Lite\) at 120,280 size 1680×680: 6 labels, 0 shapes, smallest text 26 px$`)
}

func TestReportChartLabelsOverEachOther(t *testing.T) {
	c := block("chart", "Vega-Lite", 60, 140, 840, 340)
	months := []string{"January 2025", "February 2025", "March 2025", "April 2025"}
	for i, m := range months {
		c.runs = append(c.runs, run(m, 100+float64(i)*40, 460, 80, 16, 13, 0))
	}
	match(t, flagsOf(slideOf(c)), `chart \(Vega-Lite\): 3 pairs of labels drawn over each other, e\.g\. "January 2025"/"February 2025"`)

	// the same labels turned -45° side by side do not touch
	c2 := block("chart", "Vega-Lite", 60, 140, 840, 340)
	for i, m := range months {
		c2.runs = append(c2.runs, run(m, 100+float64(i)*40, 460, 80, 16, 13, -45))
	}
	if f := flagsOf(slideOf(c2)); strings.Contains(f, "labels drawn over") {
		t.Fatal("slanted labels side by side flagged:", f)
	}
}

func TestReportDiagramTextTooSmall(t *testing.T) {
	d := block("diagram", "Mermaid", 54, 140, 852, 120)
	for i := 0; i < 5; i++ {
		d.runs = append(d.runs, run("Step", 70+float64(i)*170, 190, 40, 9, 7, 0))
	}
	match(t, flagsOf(slideOf(d)), `diagram \(Mermaid\): its text is drawn at 14 px, too small to read \(it is 1704 px wide with 5 boxes in a row\)\. Lay it out top-down`)
}

func TestReportOverlapsAndEdges(t *testing.T) {
	p := block("picture", "cat.png", 500, 100, 400, 300)
	tx := block("text", "Caption", 400, 300, 300, 40)
	off := block("table", "", 54, 420, 950, 140)
	f := flagsOf(slideOf(p, tx, off))
	match(t, f, `picture \(cat\.png\) and text "Caption" overlap \(\d+% of the smaller one\)\.`)
	match(t, f, `table goes past the slide's edge \(right by 88 px, bottom by 40 px\)\.`)
}

func TestReportSmallVisualOnEmptySlide(t *testing.T) {
	h := block("heading", "Tiny chart", 54, 64, 852, 55)
	c := block("chart", "Vega-Lite", 400, 160, 170, 120)
	match(t, flagsOf(slideOf(h, c)), `chart \(Vega-Lite\) takes 4% of the slide and most of the slide is empty`)
}

func TestReportTableColumnWraps(t *testing.T) {
	tb := block("table", "", 54, 140, 852, 300)
	tb.heads = []string{"Role", "Notes", "When"}
	for col, w := range []float64{100, 120, 632} {
		c := CreateNew_RCell()
		c.col, c.w, c.lines = int64(col), w, 1
		if col == 1 {
			c.lines = 5
		}
		tb.cells = append(tb.cells, c)
	}
	match(t, flagsOf(slideOf(tb)), `table: column 2 "Notes" is 240 px wide and wraps a cell into 5 lines\.`)
}

// --- the painter (render.go)

func paintJSON(t *testing.T, cmds ...map[string]any) *image.RGBA {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"cmds": cmds})
	dst := image.NewRGBA(image.Rect(0, 0, 192, 108))
	if err := renderList(dst, string(b), 96, 54, dst.Bounds(), nil); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestPainterRectClipBorderText(t *testing.T) {
	img := paintJSON(t,
		map[string]any{"k": 0, "x": 0, "y": 0, "w": 96, "h": 54, "c": []any{255, 255, 255, 1}},
		map[string]any{"k": 4, "x": 0, "y": 0, "w": 48, "h": 54},
		map[string]any{"k": 0, "x": 10, "y": 10, "w": 80, "h": 10, "c": []any{255, 0, 0, 1}},
		map[string]any{"k": 5},
		map[string]any{"k": 1, "x": 10, "y": 30, "w": 30, "h": 20, "t": 2, "c": []any{0, 0, 255, 1}},
		map[string]any{"k": 3, "x": 50, "y": 30, "w": 40, "h": 14, "size": 12, "text": "Hg", "font": "Open Sans", "c": []any{0, 0, 0, 1}},
	)
	at := func(x, y int) color.RGBA { return img.RGBAAt(x, y) }
	// scaled 2×: the red bar from (20,20), cut by the clip at x = 96
	if c := at(40, 30); c.R != 255 || c.G != 0 {
		t.Fatalf("inside the clip %v", c)
	}
	if c := at(120, 30); c.R != 255 || c.G != 255 {
		t.Fatalf("past the clip %v", c)
	}
	// the border is drawn inside its box, the middle left alone
	if c := at(22, 70); c.B != 255 || c.R != 0 {
		t.Fatalf("border %v", c)
	}
	if c := at(50, 80); c != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("inside the border %v", c)
	}
	// the run left ink where it is
	dark := 0
	for y := 60; y < 90; y++ {
		for x := 100; x < 150; x++ {
			if at(x, y).R < 128 {
				dark++
			}
		}
	}
	if dark < 30 {
		t.Fatalf("text drew %d dark pixels", dark)
	}
}

func TestUTF8OfRangerJSON(t *testing.T) {
	// "ä" as Ranger's Go target writes it: one character per byte of UTF-8
	if got := utf8Of("SeinÃ¤joki"); got != "Seinäjoki" {
		t.Fatal(got)
	}
	if got := utf8Of("plain ä"); got != "plain ä" {
		t.Fatal(got)
	}
}

// --- end to end: the report on create, the pictures from stored decks

const testDeck = "# Test deck\n\n## Twenty months\n\n```vega-lite\n%s\n```\n\n## From a file\n\n```vega-lite\n" +
	`{"data":{"url":"data/sales.csv"},"mark":"bar","encoding":{"x":{"field":"month","type":"ordinal"},"y":{"field":"sales","type":"quantitative"}}}` +
	"\n```\n\n## Plain\n\nSome text.\n"

func twentyMonths() string {
	var vals []string
	for i := 0; i < 20; i++ {
		vals = append(vals, fmt.Sprintf(`{"m":"Month number %d","v":%d}`, i+1, (i*7)%23+3))
	}
	return `{"data":{"values":[` + strings.Join(vals, ",") + `]},"mark":"bar","encoding":{"x":{"field":"m","type":"ordinal","sort":null,"axis":{"labelAngle":0}},"y":{"field":"v","type":"quantitative"}}}`
}

func decodeJPEG(t *testing.T, r *mcp.CallToolResult) image.Image {
	t.Helper()
	for _, c := range r.Content {
		if im, ok := c.(*mcp.ImageContent); ok {
			if im.MIMEType != "image/jpeg" {
				t.Fatal(im.MIMEType)
			}
			img, err := jpeg.Decode(bytes.NewReader(im.Data))
			if err != nil {
				t.Fatal(err)
			}
			return img
		}
	}
	t.Fatal("no image in", r.Content)
	return nil
}

func lastText(r *mcp.CallToolResult) string {
	return r.Content[len(r.Content)-1].(*mcp.TextContent).Text
}

func TestLayoutReportAndRender(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Render", "markdown": fmt.Sprintf(testDeck, twentyMonths()),
		"files": []any{map[string]any{"name": "sales.csv", "text": "month,sales\nJan,10\nFeb,14\nMar,9\n"}},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	text := textOf(c)
	t.Log(text)
	match(t, text, `Layout \(px of a 1920×1080 screen`)
	match(t, text, `Slide 2 "Twenty months": the elements cover \d+% of the slide`)
	match(t, text, `⚠ chart \(Vega-Lite\): \d+ pairs of labels drawn over each other, e\.g\. "Month number 1"/"Month number 2"`)
	// the chart read the data file: it drew its three bars' labels
	match(t, text, `Slide 3 "From a file".*\n- heading "From a file".*\n- chart \(Vega-Lite\) at \d+,\d+ size \d+×\d+: \d+ labels`)
	match(t, text, `Look at slides 2 with render_slide`)
	lay := list(sc(c)["layout"])
	eq(t, len(lay), 4)
	eq(t, mapOf(lay[1])["slide"], float64(2))
	id := sc(c)["deck_id"]

	// one slide by number, 960 px wide; and by title
	r := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": 2})
	if r.IsError {
		t.Fatal(lastText(r))
	}
	img := decodeJPEG(t, r)
	eq(t, img.Bounds().Dx(), 960)
	eq(t, img.Bounds().Dy(), 540)
	match(t, lastText(r), `Slide 2 "Twenty months"`)
	// something was drawn where the chart is: not one colour
	seen := map[[3]uint32]bool{}
	for y := 200; y < 480; y += 7 {
		for x := 100; x < 860; x += 7 {
			r, g, b, _ := img.At(x, y).RGBA()
			seen[[3]uint32{r >> 12, g >> 12, b >> 12}] = true
		}
	}
	if len(seen) < 5 {
		t.Fatalf("the chart area has %d colours", len(seen))
	}
	r2 := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": "from a file"})
	match(t, lastText(r2), `Slide 3 "From a file"`)
	bad := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": 9})
	if !bad.IsError {
		t.Fatal("slide 9 of 4 drawn")
	}
	match(t, textOf(bad), `No slide 9: the presentation has 4 slides`)

	// all slides in one picture, a line for each
	o := call(t, s, "render_overview", map[string]any{"deck_id": id})
	if o.IsError {
		t.Fatal(lastText(o))
	}
	eq(t, decodeJPEG(t, o).Bounds().Dx(), 1600)
	ot := lastText(o)
	match(t, ot, `"Render": 4 slides`)
	match(t, ot, `Slide 4 "Plain": the elements cover`)
}
