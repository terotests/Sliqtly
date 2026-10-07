// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

// markdown-it's demo takes its pictures from the web; a slide shows only
// pictures kept with the deck, so they are fetched into media/
func TestWebPicturesFetchedIntoMedia(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "## Images\n\n![Cat](https://images.test/cat.png)\n![Again](https://images.test/cat.png \"The cat\")\n\n" +
		"<img src=\"https://images.test/cat.png\" width=\"64\">\n\n" +
		"Like links: ![Alt text][id]\n\n`![code](https://images.test/cat.png)`\n\n" +
		"![Gone](https://images.test/none.png)\n\n[id]: https://images.test/cat.png  \"The Dojocat\"\n\n[site]: https://images.test/cat.png\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Web", "markdown": md})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	got := f.db.doc("shares/" + id)["md"].(string)
	want := "## Images\n\n![Cat](media/cat.png)\n![Again](media/cat.png \"The cat\")\n\n" +
		"<img src=\"media/cat.png\" width=\"64\">\n\n" +
		"Like links: ![Alt text][id]\n\n`![code](https://images.test/cat.png)`\n\n" +
		"![Gone](https://images.test/none.png)\n\n[id]: media/cat.png  \"The Dojocat\"\n\n[site]: https://images.test/cat.png\n"
	eq(t, got, want)
	if f.bucket.saved["shares/"+id+"/media/cat.png"].contentType != "image/png" {
		t.Fatal("the picture was not kept")
	}
	text := textOf(c)
	match(t, text, `Fetched 1 picture\(s\) from the web into media/ \(cat\.png\)`)
	match(t, text, `Picture https://images\.test/none\.png was not fetched \(.*404`)

	// without sign-in nothing is fetched, and the reason is given
	anon := start(t, testEnv(&f, nil), "")
	defer anon.close()
	a := call(t, anon, "create_presentation", map[string]any{"title": "Web", "markdown": "## A\n\n![Cat](https://images.test/cat.png)\n"})
	if a.IsError {
		t.Fatal(textOf(a))
	}
	if !strings.Contains(textOf(a), "a slide shows only pictures kept with the deck") {
		t.Fatal(textOf(a))
	}
}

func TestWebPicturesNames(t *testing.T) {
	eq(t, WebPictures_static_nameFor("https://x.test/a/minion.png?x=1", []string{"minion.png"}), "minion-2.png")
	eq(t, WebPictures_static_nameFor("https://x.test/", nil), "web-picture")
}

// a picture still at a web address (an .ico, which a slide does not draw,
// or a deck written before pictures were fetched) is named on its slide
// in the layout report, render_slide's too
func TestWebPictureLeftIsFlaggedOnItsSlide(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "## Kuvat\n\n<img src=\"https://images.test/favicon.ico\" width=\"64\">\n\n## Muu\n\nTeksti.\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Web", "markdown": md})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	text := textOf(c)
	match(t, text, `Picture https://images\.test/favicon\.ico was not fetched \(.*image/x-icon, a kind of picture a slide does not draw`)
	match(t, text, `Slide 1 "Kuvat".*\n(.*\n)*  ⚠ picture https://images\.test/favicon\.ico is at a web address, not kept with the deck`)
	nomatch(t, text, `Slide 2 "Muu".*\n(- .*\n)*  ⚠ picture`)
	id := sc(c)["deck_id"]
	r := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": 1})
	match(t, lastText(r), `⚠ picture https://images\.test/favicon\.ico is at a web address`)
}
