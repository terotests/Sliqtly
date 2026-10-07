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
	"golang.org/x/image/font/sfnt"
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
	match(t, flagsOf(slideOf(d)), `diagram \(Mermaid\): its text is drawn at 14 px, too small to read \(it is 1704 px wide with 5 boxes in a row\)\. Shorten the labels or split it`)
	// held to its written direction: the advice is to let the slide turn it
	d.keep = true
	match(t, flagsOf(slideOf(d)), `too small to read \(it is 1704 px wide with 5 boxes in a row\)\. Remove \{layout=keep\}`)
}

// The side of its place that holds a diagram small is named, with the
// advice for that side; a diagram that could not be read says why; the
// tour is a line of its own.
func TestReportDiagramLimitTourRefused(t *testing.T) {
	d := block("diagram", "PlantUML", 400, 140, 146, 331)
	d.runs = append(d.runs, run("Step", 420, 190, 40, 9, 6.5, 0))
	d.limit, d.slotW, d.slotH, d.drawW, d.drawH = "height", 852, 348, 146, 331
	d.tour = "Start → Check? ⟨yes: Save | no: Reject⟩"
	o := Report_static_slide(slideOf(d))
	f := strings.Join(o.flags, "\n")
	match(t, f, `its height holds it: it is drawn 292×662 px in a place 1704×696 px\)\. Give it a taller place`)
	match(t, strings.Join(o.lines, "\n"), `- diagram \(PlantUML\) at .*\n  tour: Start → Check\? ⟨yes: Save \| no: Reject⟩`)
	d.keep = true
	match(t, flagsOf(slideOf(d)), `\{layout=keep\} holds it top to bottom`)

	bad := block("diagram", "Mermaid", 68, 150, 347, 20)
	bad.runs = append(bad.runs, run("the [ after A is not closed with ] — line 2", 68, 150, 347, 20, 20, 0))
	bad.refused = "the [ after A is not closed with ] — line 2"
	f2 := flagsOf(slideOf(bad))
	match(t, f2, `diagram \(Mermaid\) is not drawn: the \[ after A is not closed with \] — line 2\.`)
	if strings.Contains(f2, "most of the slide is empty: give it the room") {
		t.Fatal("a refused diagram is told to take more room:", f2)
	}
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
	return `{"data":{"values":[` + strings.Join(vals, ",") + `]},"mark":"bar","encoding":{"x":{"field":"m","type":"ordinal","sort":null,"axis":{"labelAngle":0,"labelOverlap":false}},"y":{"field":"v","type":"quantitative"}}}`
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
	// a line of text at the top of an empty slide is flagged too
	match(t, text, `Slide 4 "Plain".*\n.*\n.*\n  ⚠ The lower \d+% of the slide is empty \(the elements cover \d+%\)\.`)
	match(t, text, `Look at slides 2, 4 with render_slide`)
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

// A flowchart Mermaid would refuse is named with its line, in the notes and
// the slide's flags; a toured diagram's report carries the tour's order.
func TestLayoutReportFlowchartErrorsAndTour(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	deck := "# Flow\n\n## Broken\n\n```mermaid\nflowchart LR\n  A[Open --> B\n```\n\n" +
		"## Tour\n\n```mermaid\nflowchart TD\n  S([Start]) --> V{In stock?}\n  V -->|yes| K[Pick]\n  V -->|no| T[Order]\n  T --> V\n```\n{tour=on}\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Flow", "markdown": deck})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	text := textOf(c)
	match(t, text, `Note: The mermaid block on slide "Broken" is not shown: the \[ after A is not closed with \] — line 2\.`)
	match(t, text, `⚠ diagram \(Mermaid\) is not drawn: the \[ after A is not closed with \] — line 2\.`)
	match(t, text, `  tour: Start → In stock\? ⟨yes: Pick \| no: Order → In stock\? \(back\)⟩`)
}

// Every symbol a slide commonly uses has a glyph in some face the painter
// falls back to; a missing one would be drawn as an empty box ("52 ms ▯ 0,01 ms").
func TestPainterSymbolsHaveGlyphs(t *testing.T) {
	var buf sfnt.Buffer
	for _, fam := range []string{"Open Sans", "Open Sans-Bold", "Noto Sans"} {
		f := face(fam, "", false)
		chain := []*sfnt.Font{face(notoLike(fam), "", false), loadFace("Noto Emoji-Regular"), loadFace(symbolLike(fam, ""))}
		for _, r := range "äöå–—…•·→←↑↓↔⇒⇐⇔↗↘✓✔✗✘★☆≤≥≠≈±×÷∞√∑π°€£§©®™½²³₂µΩαβΔλ−∈∅⟶➜▶►◆●○■□▲▼♥⚠✅❌" {
			if _, gi := glyphOf(&buf, f, chain, r); gi == 0 {
				t.Errorf("%s: no face draws %c (U+%04X)", fam, r, r)
			}
		}
	}
}

// An HTML table is reported as a table (and gets a table's checks), and text
// struck through, drawn muted on purpose, is not warned of for contrast.
func TestReportHTMLTableAndStruckText(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "# Deck\n\n## Plans\n\n<table><thead><tr><th>Plan</th><th>Price</th></tr></thead>" +
		"<tbody><tr><td>Basic</td><td>9 €</td></tr><tr><td>Pro</td><td>19 €</td></tr></tbody></table>\n\n" +
		"## Prices\n\nWas ~~twenty euros a month~~ and is <del>fifteen</del> <s>twelve</s> ten now.\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Tables", "markdown": md})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	text := textOf(c)
	t.Log(text)
	match(t, text, `Slide 2 "Plans".*\n- heading "Plans".*\n- table at`)
	ws := fmt.Sprint(sc(c)["warnings"])
	for _, struck := range []string{"twenty euros", "fifteen", "twelve"} {
		if strings.Contains(ws, struck) {
			t.Fatalf("struck-through %q was warned of: %s", struck, ws)
		}
	}
}

// A tick label Vega hides to keep labels apart (labelOverlap) is drawn at no
// opacity: not on the screen, so it neither crowds its neighbours nor is hard
// to read. Counted, a horizontal bar chart on a dark slide was reported as
// "1,000"/"1,100" over each other and at 1.0:1 contrast.
func TestReportSkipsHiddenTickLabels(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	spec := `{"background": "rgba(0,0,0,0)", "config": {"axis": {"labelColor": "#c8d0f0", "titleColor": "#c8d0f0"}}, "width": 820, "height": 330,` +
		`"data": {"values": [{"a": "Etelä", "v": 2049}, {"a": "Länsi", "v": 1777}, {"a": "Itä", "v": 1419}, {"a": "Pohjoinen", "v": 1208}]},` +
		`"mark": "bar", "encoding": {"y": {"field": "a", "type": "nominal", "sort": "-x"}, "x": {"field": "v", "type": "quantitative"}}}`
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Hidden", "theme": "aurora", "markdown": "# Hidden labels\n\n## Bars\n\n```vega-lite\n" + spec + "\n```\n",
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	text := textOf(c)
	if strings.Contains(text, "drawn over each other") {
		t.Fatal("hidden tick labels counted as crowding:\n", text)
	}
	if ws := fmt.Sprint(sc(c)["warnings"]); strings.Contains(ws, "hard to read") {
		t.Fatal("hidden tick labels judged for contrast:", ws)
	}
}

// A document page (the editorial theme's A4) is read at print sizes: its
// 10.5 pt body is no "too small" warning, and the space under its last
// section is no "empty slide"; the same text on a slide still is.
func TestReportDocumentPage(t *testing.T) {
	p := block("text", "", 64, 64, 467, 40)
	p.runs = append(p.runs, run("Body text on an A4 page", 64, 64, 300, 17, 10.5, 0))
	pic := block("picture", "a.png", 64, 120, 200, 100)
	s := slideOf(p, pic)
	s.w, s.h = 595.28, 841.89
	s.page = true
	if f := flagsOf(s); f != "" {
		t.Fatal(f)
	}
	s.page = false
	if f := flagsOf(s); !strings.Contains(f, "too small to read") {
		t.Fatalf("a slide with 10.5 pt text: %q", f)
	}
}

// A narrow figure with its text beside it is not "small on an empty slide".
func TestReportFigureWithTextBeside(t *testing.T) {
	h := block("heading", "Title", 54, 64, 852, 55)
	h.runs = append(h.runs, run("Title", 54, 64, 200, 36, 36, 0))
	f := block("figure", "process", 64, 140, 490, 160)
	p := block("text", "", 590, 140, 300, 60)
	p.runs = append(p.runs, run("Text beside the figure", 590, 140, 280, 20, 20, 0))
	if fl := flagsOf(slideOf(h, f, p)); strings.Contains(fl, "most of the slide is empty") {
		t.Fatal(fl)
	}
	if fl := flagsOf(slideOf(h, f)); !strings.Contains(fl, "most of the slide is empty") {
		t.Fatalf("alone: %q", fl)
	}
}

// --- print (`@media print`): safe area and resolution (PrintReport)

func printPage(title string, safe float64) *RPrintPage {
	p := CreateNew_RPrintPage()
	p.title, p.w, p.h, p.safe = title, 841.89, 595.28, safe
	return p
}

func printPic(name string, shownW, shownH, w, h float64) *RPrintPic {
	q := CreateNew_RPrintPic()
	q.name, q.shownW, q.shownH, q.w, q.h = name, shownW, shownH, w, h
	return q
}

func TestPrintReportDpi(t *testing.T) {
	// 1500 px over 600 pt (8.33 in) is 180 dpi
	if d := PrintReport_static_dpi(printPic("a", 1500, 1500, 600, 300)); d < 179.9 || d > 180.1 {
		t.Fatalf("dpi %v", d)
	}
	p := printPage("Sauna", 22.68)
	p.pics = append(p.pics, printPic("media/sauna.jpg", 1500, 1000, 600, 400))
	p.pics = append(p.pics, printPic("media/sharp.jpg", 4000, 3000, 600, 400))
	q := printPage("Again", 22.68)
	q.pics = append(q.pics, printPic("media/sauna.jpg", 1500, 1000, 300, 200))
	notes := PrintReport_static_notes([]*RPrintPage{p, q})
	if len(notes) != 1 {
		t.Fatal(notes)
	}
	match(t, notes[0], `^media/sauna\.jpg: 180 dpi in print, under 300 \(Print page 1 "Sauna"\)\.$`)
}

func TestPrintReportSafeArea(t *testing.T) {
	p := printPage("Hietaniemi", 22.68)
	p.runs = append(p.runs, run("Inside", 40, 40, 100, 14, 12, 0))
	p.runs = append(p.runs, run("Too close", 10, 560, 100, 14, 12, 0))
	notes := PrintReport_static_notes([]*RPrintPage{p})
	if len(notes) != 1 {
		t.Fatal(notes)
	}
	match(t, notes[0], `^Print page 1 "Hietaniemi": text outside the safe area \(8 mm inside the trim\): "Too close"\.$`)
	// no safe area asked for: nothing to say
	p.safe = 0
	if n := PrintReport_static_notes([]*RPrintPage{p}); len(n) != 0 {
		t.Fatal(n)
	}
}

// A quote nested three deep is one element: its bars span every level's
// paragraph, and are not reported as quotes drawn over each other
func TestReportNestedQuoteIsOneElement(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Quotes", "markdown": "## Blockquotes\n\n> Blockquotes can also be nested...\n>> ...by using additional greater-than signs right next to each other...\n> > > ...or with spaces between arrows.\n",
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	text := textOf(c)
	if strings.Contains(text, "overlap") {
		t.Fatalf("nested quotes reported as overlapping:\n%s", text)
	}
	match(t, text, `- quote "Blockquotes can also be nested…" at \d+,\d+`)
}

// a container's plate (no text of its own) is what its text is drawn on
func TestReportContainerPlateIsNotAnOverlap(t *testing.T) {
	plate := block("text", "", 100, 100, 800, 200)
	inner := block("text", "here be dragons", 140, 140, 400, 60)
	inner.runs = append(inner.runs, run("here be dragons", 140, 140, 400, 60, 40, 0))
	if f := flagsOf(slideOf(plate, inner)); strings.Contains(f, "overlap") {
		t.Fatal(f)
	}
	// two texts over each other still are
	other := block("text", "other", 150, 150, 400, 60)
	other.runs = append(other.runs, run("other", 150, 150, 400, 60, 40, 0))
	if f := flagsOf(slideOf(inner, other)); !strings.Contains(f, "overlap") {
		t.Fatal("overlapping texts not flagged")
	}
}

// A table at 62% with a card beside it, too long for one slide: on the
// continuation the title said again is a heading of its own, not part of
// the table, and the card beside the table's rest does not overlap it.
func TestReportTableContinuationWithCard(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "---\ntheme: corporate\n---\n\n## Asiakkaat\n\n| # | Asiakas | Kaupunki | Maa | Summa |\n|---|---|---|---|---:|\n"
	for i := 1; i <= 14; i++ {
		md += fmt.Sprintf("| %d | Hungry Owl All-Night Grocers | Buenos Aires | Argentiina | %d |\n", i, 900+i)
	}
	md += "{width=62%}\n\n- Kaikki yli 900\n- Kaksi Buenos Airesista\n{container=box}\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Split", "markdown": md})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	text := textOf(c)
	t.Log(text)
	match(t, text, `Slide 2: .*\n- heading "Asiakkaat" .*\n- table at 108,`)
	match(t, text, `Slide \d+: .*\n- heading "Asiakkaat" .*\n- table at .*\n- list "•" at 1\d\d\d,`)
	if strings.Contains(text, "overlap") {
		t.Fatal("the card beside the table's rest is reported as overlapping it")
	}
}

// Few elements stacked at the top of a slide: the empty room under them is
// flagged on any slide (not only one with a picture), but not on the deck's
// title slide nor on content set in the middle.
func TestReportSparseSlide(t *testing.T) {
	h := block("heading", "Title", 54, 64, 852, 40)
	h.runs = append(h.runs, run("Title", 54, 64, 200, 30, 30, 0))
	l := block("list", "", 64, 120, 400, 90)
	l.runs = append(l.runs, run("First point", 90, 120, 200, 24, 24, 0))
	s := slideOf(h, l)
	s.index = 3
	if f := flagsOf(s); !strings.Contains(f, "is empty (the elements cover") {
		t.Fatalf("a list at the top of an empty slide: %q", f)
	}
	s.index = 0
	if f := flagsOf(s); strings.Contains(f, "is empty") {
		t.Fatalf("the title slide: %q", f)
	}
	// the same set in the middle (valign=center, layout=section)
	mh := block("heading", "Title", 54, 200, 852, 40)
	mh.runs = append(mh.runs, run("Title", 54, 200, 200, 30, 30, 0))
	ml := block("list", "", 64, 256, 400, 90)
	ml.runs = append(ml.runs, run("First point", 90, 256, 200, 24, 24, 0))
	m := slideOf(mh, ml)
	m.index = 3
	if f := flagsOf(m); strings.Contains(f, "is empty") {
		t.Fatalf("centred: %q", f)
	}
}

func TestReportCornerLogo(t *testing.T) {
	h := block("heading", "Title", 54, 64, 760, 40)
	h.runs = append(h.runs, run("Title", 54, 64, 200, 30, 30, 0))
	logo := block("picture", "logo.svg", 840, 48, 72, 72)
	l := block("list", "", 54, 130, 760, 200)
	l.runs = append(l.runs, run("First point", 80, 130, 200, 24, 24, 0))
	s := slideOf(h, logo, l)
	s.index = 3
	if f := flagsOf(s); !strings.Contains(f, "takes 1% of the slide") {
		t.Fatalf("a small picture in the flow: %q", f)
	}
	logo.pinned = true
	if f := flagsOf(s); strings.Contains(f, "of the slide and most of the slide is empty") {
		t.Fatalf("a corner logo is not content left small: %q", f)
	}
}
