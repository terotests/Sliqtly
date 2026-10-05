// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"image/color"
	"regexp"
	"strings"
	"testing"
)

// --- the SVG check on its own (rgr/SvgCheck.rgr)

func svgReport(src string, bg bool, drawErr string) *SvgOut {
	return SvgCheck_static_report(SvgCheck_static_read(src), "media/bg.svg", bg, 960, 540, drawErr)
}

func TestSvgCheckSaysWhatItIs(t *testing.T) {
	o := svgReport(svgBackground, true, "")
	match(t, o.line, `^media/bg\.svg: SVG, viewBox 1920×1080 \(16:9\), 4 shapes, 1 text, 1 gradient, 1 pattern, 1 filter; the slide's background \(it covers the slide: scaled to fill it, the middle kept, the rest cut off, as preserveAspectRatio="xMidYMid slice"\); drawn in render_slide\.$`)
	flags := strings.Join(o.flags, "\n")
	match(t, flags, `media/bg\.svg has text in Arial: each viewer's own fonts draw it`)
	match(t, flags, `uses filter effects \(feGaussianBlur\)`)
	if len(o.flags) != 2 {
		t.Fatalf("flags: %q", o.flags)
	}

	clean := svgReport(`<?xml version="1.0"?><!-- a background --><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1920 1080"><path d="M0 0h10v10z"/><path d="M5 5h1"/><g><circle r="4"/></g></svg>`, true, "")
	match(t, clean.line, `^media/bg\.svg: SVG ok, viewBox 1920×1080 \(16:9\), 2 paths, 1 shape; the slide's background`)
	if len(clean.flags) > 0 {
		t.Fatalf("flags on a clean SVG: %q", clean.flags)
	}
	// not a background: its shape is its own business
	inline := svgReport(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 400"><rect/></svg>`, false, "")
	match(t, inline.line, `^media/bg\.svg: SVG ok, viewBox 400×400 \(1:1\), 1 shape; drawn in render_slide\.$`)
}

func TestSvgCheckWarnsOfWhatThePlayerWillNotShow(t *testing.T) {
	for _, c := range []struct {
		src, want string
	}{
		// a browser shows an SVG outside the SVG namespace as nothing
		{`<svg viewBox="0 0 1920 1080"><rect/></svg>`, `has no xmlns="http://www.w3.org/2000/svg" on its <svg>: a browser shows such a file as nothing`},
		{`<svg xmlns="http://www.w3.org/2000/svg" width="1920" height="1080"><rect/></svg>`, `has no viewBox: give it one \(viewBox="0 0 1920 1080" for a 16:9 slide\)`},
		{`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 800 600"><rect/></svg>`, `is 4:3 but the slide is 16:9: as the background it is cut to the slide's shape, 25% of its height off\. Make it 16:9 \(viewBox="0 0 1920 1080"\)`},
		{`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 1920 1080"><image xlink:href="https://example.com/a.png"/><image href="data:image/png;base64,AAAA"/><use href="#a"/><rect fill="url(#g)" style="fill: url('logo.svg#x')"/></svg>`, `loads https://example\.com/a\.png and 1 more from outside the file`},
		{`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1920 1080"><style>@import url(https://fonts.example/css);</style><rect/></svg>`, `loads https://fonts\.example/css from outside the file`},
		{`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1920 1080"><style>@import "theme.css";</style><rect/></svg>`, `loads theme\.css from outside the file`},
		{`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1920 1080"><style><![CDATA[ text { font-family: 'Inter', sans-serif } ]]></style><text>Hi</text></svg>`, `has text in Inter: each viewer's own fonts draw it`},
		{`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1920 1080"><text font-family="sans-serif">Hi</text></svg>`, `has text: each viewer's own fonts draw it`},
		{`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1920 1080"><script>x()</script></svg>`, `has a <script>: an SVG shown as a picture runs no scripts`},
		{`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1920 1080"><foreignObject/></svg>`, `has a <foreignObject>`},
		{`<html><svg viewBox="0 0 1 1"/></html>`, `is not an SVG: its first element is not <svg>`},
	} {
		o := svgReport(c.src, true, "")
		if !regexp.MustCompile(c.want).MatchString(strings.Join(o.flags, "\n")) {
			t.Errorf("%s\n flags %q\n want %s", c.src, o.flags, c.want)
		}
	}
	o := svgReport(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect`, false, "unexpected end of stream")
	match(t, o.line, `does not draw: unexpected end of stream\.$`)
	match(t, o.flags[0], `^media/bg\.svg does not draw \(unexpected end of stream\); a browser will not draw it either`)
}

func TestSvgCheckSizeAsThePlayer(t *testing.T) {
	for _, c := range []struct {
		src  string
		w, h float64
	}{
		{`<svg viewBox="0 0 200 120">`, 200, 120},
		{`<svg width="400" viewBox="0, 0, 200, 100">`, 400, 200},
		{`<svg width="2in" height="1in">`, 192, 96},
		{`<svg width="50%" height="10">`, 300, 10},
		{`<svg>`, 300, 150},
	} {
		f := SvgCheck_static_read(c.src)
		if f.w != c.w || f.h != c.h {
			t.Errorf("%s: %v×%v, want %v×%v", c.src, f.w, f.h, c.w, c.h)
		}
	}
}

// --- through the tools: an SVG sent as text, as a slide's background

func TestSvgBackgroundSentAsTextIsDrawnAndChecked(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	const bg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1920 1080"><rect width="1920" height="1080" fill="#00ff00"/><path d="M0 0h960v1080H0z" fill="#ff0000"/></svg>`
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Vector", "markdown": "# Vector {bg=media/bg.svg}\n\n## Plain\n\nText.\n",
		"images": []any{map[string]any{"name": "bg.svg", "text": bg}},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	text := textOf(c)
	match(t, text, `SVG pictures \(render_slide and render_overview draw them`)
	match(t, text, `- media/bg\.svg: SVG ok, viewBox 1920×1080 \(16:9\), 1 path, 1 shape; the slide's background`)
	out := sc(c)
	id := out["deck_id"].(string)
	eq(t, f.bucket.saved["shares/"+id+"/media/bg.svg"].contentType, "image/svg+xml")
	if got := string(f.bucket.saved["shares/"+id+"/media/bg.svg"].data); got != bg {
		t.Fatalf("stored %q", got)
	}

	// drawn: red on the left half, green on the right
	r := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": 1})
	if r.IsError {
		t.Fatal(lastText(r))
	}
	img := decodeJPEG(t, r)
	near(t, "left half", img.At(40, 500), color.RGBA{255, 0, 0, 255})
	near(t, "right half", img.At(920, 500), color.RGBA{0, 255, 0, 255})
	match(t, lastText(r), `SVG pictures are\.`)
	match(t, lastText(r), `- media/bg\.svg: SVG ok`)

	// read back from storage on update: still an SVG, still checked
	u := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edit_key": out["edit_key"], "markdown": "# Vector {bg=media/bg.svg}\n\nMore.\n"})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	match(t, textOf(u), `- media/bg\.svg: SVG ok, viewBox 1920×1080`)

	// text is for an SVG only, and one source at a time
	for _, img := range []map[string]any{
		{"name": "a.png", "text": "x"},
		{"name": "a.svg", "text": bg, "data_base64": "AAAA"},
	} {
		r := call(t, s, "create_presentation", map[string]any{"title": "x", "markdown": "# x", "images": []any{img}})
		if !r.IsError {
			t.Fatalf("%v was accepted", img)
		}
	}
}
