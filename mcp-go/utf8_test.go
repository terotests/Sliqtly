// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Text that is not ASCII goes through the Markdown engine's layout, CSS and
// anchor code compiled to Go as it came: on Go a Ranger `charAt` is a byte,
// and the copies that made a character of each byte (`strfromcode`) turned
// "ä" into "Ã¤" and split the emoji.
func TestUnicodeThroughLayoutCssAndHints(t *testing.T) {
	const h = "Äänisuotimet 🎵"
	check := func(what, got, want string) {
		t.Helper()
		if !utf8.ValidString(got) || got != want {
			t.Fatalf("%s: %q, want %q", what, got, want)
		}
	}
	check("oneLine", MdLayout_static_oneLine(h+"\nÖljy ✨"), h+" Öljy ✨")
	check("stripTags", MdLayout_static_stripTags("<b>"+h+"</b>\nä"), h+" ä")
	check("lower", MdContainer_static_lower("AB "+h), "ab "+h)
	check("anchor", MdAnchor_static_slug(h), "äänisuotimet-🎵")
	check("pick slug", PresPick_static_slug(h+" / Öljy"), "Äänisuotimet-🎵-Öljy")
	css := MdCss_static_desugar(`h2 { font-family: "` + h + `"; } li:nth(2) { color: red; }`)
	if !utf8.ValidString(css) || !strings.Contains(css, `"`+h+`"`) || strings.Contains(css, ":nth(") {
		t.Fatalf("desugar: %q", css)
	}
	eq(t, MdGallery_static_sides("10pt 20pt"), []float64{10, 20, 10, 20})

	// and a deck with the heading, its own CSS naming it, laid out by the
	// server: the words come back whole
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{"title": h, "markdown": "# " + h + "\n\n## " + h + "\n\nSävel 🎶 ja ääni.\n",
		"css": `h2::after { content: "` + h + `"; }`})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	dl := textOf(call(t, s, "get_display_list", map[string]any{"deck_id": sc(c)["deck_id"], "slide": 2}))
	if !utf8.ValidString(dl) || !strings.Contains(dl, "Äänisuotimet") || !strings.Contains(dl, "🎵") || strings.Contains(dl, "Ã") {
		t.Fatalf("the slide's words: %s", dl)
	}
	match(t, dl, "Sävel")
}
