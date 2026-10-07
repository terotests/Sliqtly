// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

// --- the fixes on their own (rgr/SvgFix.rgr)

func TestSvgFixAddsTheNamespacesItLacks(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		// the SVG namespace, which a browser needs to draw it at all
		{`<svg viewBox="0 0 10 10"><rect/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect/></svg>`},
		// after a declaration and a comment, on the root only
		{`<?xml version="1.0"?><!-- <svg> --><svg width="4"><g><svg/></g></svg>`, `<?xml version="1.0"?><!-- <svg> --><svg xmlns="http://www.w3.org/2000/svg" width="4"><g><svg/></g></svg>`},
		// xlink: written and not declared
		{`<svg><use xlink:href="#a"/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><use xlink:href="#a"/></svg>`},
		// a prefixed root gets its own prefix declared
		{`<svg:svg><svg:rect/></svg:svg>`, `<svg:svg xmlns:svg="http://www.w3.org/2000/svg"><svg:rect/></svg:svg>`},
	} {
		eq(t, SvgFix_static_withNamespaces(c.in), c.want, c.in)
	}
	for _, same := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><use xlink:href="#a"/></svg>`,
		// declared deeper: left to the file
		`<svg xmlns="http://www.w3.org/2000/svg"><g xmlns:xlink="http://www.w3.org/1999/xlink"><use xlink:href="#a"/></g></svg>`,
		// another namespace named: not a fix with one answer
		`<svg xmlns="http://example.com/other"><rect/></svg>`,
		// "xlink:" in text is not an attribute
		`<svg xmlns="http://www.w3.org/2000/svg"><text>xlink:href</text></svg>`,
		`<html><svg/></html>`,
		`not xml`,
	} {
		eq(t, SvgFix_static_withNamespaces(same), same)
	}
}

func TestSvgFixFindsAndReplacesOutsidePictures(t *testing.T) {
	src := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">` +
		`<image href="https://images.test/cat.png?a=1&amp;b=2"/><image xlink:href='http://x.test/a.png'/>` +
		`<image href="data:image/png;base64,AA=="/><use href="#a"/><filter><feImage href="https://images.test/f.png"/></filter>` +
		`<image href="https://images.test/cat.png?a=1&amp;b=2"/></svg>`
	got := SvgFix_static_outsidePictures(src)
	eq(t, got, []string{"https://images.test/cat.png?a=1&amp;b=2", "http://x.test/a.png", "https://images.test/f.png"})
	eq(t, SvgFix_static_unescaped(got[0]), "https://images.test/cat.png?a=1&b=2")
	out := SvgFix_static_withHref(src, got[0], "data:image/png;base64,QQ==")
	eq(t, strings.Count(out, `href="data:image/png;base64,QQ=="`), 2)
	eq(t, strings.Contains(out, "cat.png"), false)
	eq(t, SvgFix_static_withHref(src, got[1], "data:x"), strings.Replace(src, `'http://x.test/a.png'`, `'data:x'`, 1))
	eq(t, SvgFix_static_dataUrl("image/png", []byte("Man")), "data:image/png;base64,TWFu")
}

// --- on the way in (Deck.fixSvg), through create_presentation

func TestSvgPicturesFixedOnTheWayIn(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "## A {bg=media/bare.svg}\n\n## B\n\n![Logo](media/linked.svg)\n\n## C\n\n![T](media/words.svg)\n"
	c := call(t, s, "create_presentation", map[string]any{
		"title": "SVG", "markdown": md,
		"images": []any{
			map[string]any{"name": "bare.svg", "text": `<svg viewBox="0 0 1920 1080"><rect width="1920" height="1080" fill="#123"/></svg>`},
			map[string]any{"name": "linked.svg", "text": `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 100 100">` +
				`<image width="50" height="50" xlink:href="https://images.test/cat.png"/><image href="https://images.test/none.png"/><image href="http://images.test/cat.png"/></svg>`},
			map[string]any{"name": "words.svg", "text_to_path": true, "text": `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 50"><text x="5" y="30" font-size="20">Hello</text></svg>`},
		},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	kept := func(name string) string {
		return string(f.bucket.saved["shares/"+id+"/media/"+name].data)
	}
	eq(t, kept("bare.svg"), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1920 1080"><rect width="1920" height="1080" fill="#123"/></svg>`)
	linked := kept("linked.svg")
	if !strings.Contains(linked, `xlink:href="data:image/png;base64,`+base64.StdEncoding.EncodeToString(PNG)+`"`) {
		t.Fatalf("the linked picture is not in it: %s", linked)
	}
	if !strings.Contains(linked, `href="https://images.test/none.png"`) || !strings.Contains(linked, `href="http://images.test/cat.png"`) {
		t.Fatalf("what could not be fetched is left as it was: %s", linked)
	}
	words := kept("words.svg")
	if strings.Contains(words, "<text") || !strings.Contains(words, "<path") {
		t.Fatalf("text not turned into paths: %s", words)
	}
	text := textOf(c)
	match(t, text, `media/bare\.svg: added xmlns to its <svg>`)
	match(t, text, `media/linked\.svg: fetched https://images\.test/cat\.png into it as data: URLs`)
	match(t, text, `media/linked\.svg: https://images\.test/none\.png answered 404; it is not drawn`)
	match(t, text, `media/linked\.svg: http://images\.test/cat\.png not fetched \(only public https addresses are\)`)
	match(t, text, `media/words\.svg: text turned into paths`)
	// the report no longer says the namespace is missing
	if strings.Contains(text, "has no xmlns") {
		t.Fatal(text)
	}

	bad := call(t, s, "create_presentation", map[string]any{"title": "x", "markdown": "## A\n",
		"images": []any{map[string]any{"name": "a.svg", "text": "<svg/>", "text_to_path": "yes"}}})
	if !bad.IsError || !strings.Contains(textOf(bad), "text_to_path is true or false") {
		t.Fatal(textOf(bad))
	}
}

// a chart's image marks name the deck's pictures: used, and missed when not sent
func TestChartPicturesCountAsUsed(t *testing.T) {
	md := "## A\n\n```vega-lite\n{\"data\": {\"values\": [{\"img\": \"media/logo.svg\"}, {\"img\": \"media/gone.svg\"}]}, \"mark\": \"image\"}\n```\n"
	ws := strings.Join(Deck_static_warnings(md, []string{"logo.svg"}, []string{}), "\n")
	if strings.Contains(ws, "logo.svg was sent but") {
		t.Fatal(ws)
	}
	match(t, ws, `media/gone\.svg is used in the Markdown but no image by that name was sent`)
}

// a Vega-Lite image mark draws the deck's own SVG (it drew an empty grid)
func TestChartImageMarkDrawsTheDecksPicture(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	const red = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10" fill="#ff0000"/></svg>`
	spec := `{"background":"rgba(0,0,0,0)","data":{"values":[{"x":1,"y":1,"img":"media/red.svg"}]},` +
		`"mark":{"type":"image","width":160,"height":160},"encoding":{"x":{"field":"x","type":"quantitative","axis":null,"scale":{"domain":[0,2]}},` +
		`"y":{"field":"y","type":"quantitative","axis":null,"scale":{"domain":[0,2]}},"url":{"field":"img","type":"nominal"}}}`
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Marks", "markdown": "## Logos\n\n```vega-lite\n" + spec + "\n```\n",
		"images": []any{map[string]any{"name": "red.svg", "text": red}},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	if strings.Contains(textOf(c), "red.svg was sent but") {
		t.Fatal(textOf(c))
	}
	r := call(t, s, "render_slide", map[string]any{"deck_id": sc(c)["deck_id"].(string), "slide": 1})
	if r.IsError {
		t.Fatal(lastText(r))
	}
	img := decodeJPEG(t, r)
	reds := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			if cr>>8 > 200 && cg>>8 < 60 && cb>>8 < 60 {
				reds++
			}
		}
	}
	t.Logf("red samples: %d", reds)
	// a 160-point square on a 960-pixel render is some 1000 samples
	if reds < 300 {
		t.Fatalf("the picture is not drawn in the chart: %d red samples", reds)
	}
}
