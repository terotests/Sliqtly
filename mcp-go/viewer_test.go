// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

// the Personal package's server has the viewer built in, not the editor:
// its front page is the list of decks and no link offers ?edit
func TestViewerOnly(t *testing.T) {
	viewer := fstest.MapFS{"index.html": {}, "view.js": {}}
	editor := fstest.MapFS{"index.html": {}, "pres_app.js": {}, "view.js": {}}
	eq(t, viewerOnly(viewer), true)
	eq(t, viewerOnly(editor), false)
	eq(t, viewerOnly(fstest.MapFS{"index.html": {}}), false)
	eq(t, viewerOnly(nil), false)
}

// every theme is built in: sliqtly.com no longer serves /themes/, and a
// deck still lays out and its CSS can still change
func TestThemesBuiltIn(t *testing.T) {
	for _, name := range []string{"aurora", "carbon", "corporate", "editorial", "ember", "midnight", "nebula", "pearl", "hive", "lattice", "apex", "tide", "mist"} {
		if _, ok := builtinTheme(name); !ok {
			t.Fatalf("theme %s is not built in", name)
		}
	}
	f := fakeFirebase()
	e := testEnv(&f, nil)
	e.ThemeClient = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return respond(404, "text/plain", "not found"), nil
	})}
	s := start(t, e, "")
	defer s.close()
	for _, theme := range []string{"aurora", "corporate"} {
		c := call(t, s, "create_presentation", map[string]any{"title": "T", "markdown": "# One\n\nText\n\n## Two\n\nMore", "theme": theme})
		if c.IsError {
			t.Fatal(theme, textOf(c))
		}
		eq(t, sc(c)["slides"], 2, theme)
	}
}

// sliqtly.com has no rooms, so its guide says nothing of them
func TestGuideWithoutRooms(t *testing.T) {
	g := withoutRooms(guideMD)
	if strings.Contains(g, "Topic: rooms") || strings.Contains(g, "topic=rooms") || strings.Contains(g, "rooms -->") || !strings.Contains(guideMD, "# Topic: rooms") {
		t.Fatal("the Rooms parts are not cut out as marked")
	}
	if !strings.Contains(g, "## When another assistant works on the same deck") || !strings.Contains(g, "`delete_presentation`") {
		t.Fatal("a part around Rooms went too")
	}
	r := withRooms(guideMD)
	if strings.Contains(r, "<!-- rooms -->") || strings.Contains(r, "<!-- /rooms -->") || !strings.Contains(r, "# Topic: rooms") || !strings.Contains(r, "| `rooms` |") {
		t.Fatal("a server with rooms does not get the Rooms parts as they are")
	}
}
