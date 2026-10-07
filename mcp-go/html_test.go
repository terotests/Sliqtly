// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"regexp"
	"testing"
)

// HTML written into the Markdown: what is drawn is drawn (lists, a div's
// plate, span styles, an inline <svg>), what is not is said in the notes,
// and a table with a formula in a cell is still reported as a table.
func TestHTMLInMarkdown(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md, err := os.ReadFile("testdata/html-deck.md")
	if err != nil {
		t.Fatal(err)
	}
	c := call(t, s, "create_presentation", map[string]any{"title": "HTML", "markdown": string(md)})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	text := textOf(c)
	t.Log(text)
	match(t, text, `HTML on slide "3 · span styles": style letter-spacing on <span> is not drawn`)
	match(t, text, `HTML on slide "10 · Embeds": <iframe> is not supported`)
	match(t, text, `HTML on slide "10 · Embeds": <video> is not supported`)
	match(t, text, `HTML on slide "11 · Safety": onclick on <span> is left out`)
	match(t, text, `HTML on slide "11 · Safety": a link to a script address`)
	match(t, text, `HTML on slide "11 · Safety": <script> is shown as text and never run`)
	match(t, text, `HTML on slide "7 · Table: caption, list and link in a cell, nested": a table inside a table cell is drawn as lines`)
	// the table with $E=mc^2$ in a cell is a table, not a formula
	match(t, text, `Slide 5 "7 · Table[^"]*".*\n(- .*\n)*- table at`)
	nomatch(t, text, `- math at`)
	// the HTML list is a list, not markup shown as code
	match(t, text, `Slide 4 "5 · HTML lists".*\n(- .*\n)*- list "ul list`)
	nomatch(t, text, `inline <svg> was not drawn`)

	// the inline <svg> is drawn: its cyan circle is on the slide
	id := sc(c)["deck_id"]
	r := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": 6})
	if r.IsError {
		t.Fatal(lastText(r))
	}
	img := decodeJPEG(t, r)
	cyan := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 2 {
		for x := b.Min.X; x < b.Max.X; x += 2 {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 < 0x80 && g>>8 > 0xc0 && bl>>8 > 0xe0 {
				cyan++
			}
		}
	}
	if cyan < 50 {
		t.Fatalf("the <svg> circle is not drawn (%d cyan points)", cyan)
	}
}

func nomatch(t *testing.T, s, re string) {
	t.Helper()
	if regexp.MustCompile(re).MatchString(s) {
		t.Fatalf("%q found in:\n%s", re, s)
	}
}
