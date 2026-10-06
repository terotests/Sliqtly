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
		"Like links: ![Alt text][id]\n\n`![code](https://images.test/cat.png)`\n\n" +
		"![Gone](https://images.test/none.png)\n\n[id]: https://images.test/cat.png  \"The Dojocat\"\n\n[site]: https://images.test/cat.png\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Web", "markdown": md})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	got := f.db.doc("shares/" + id)["md"].(string)
	want := "## Images\n\n![Cat](media/cat.png)\n![Again](media/cat.png \"The cat\")\n\n" +
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
