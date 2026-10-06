// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

// css on update_presentation adds to the deck's own rules: one rule sent
// alone keeps the earlier ones; all of them sent again replace them;
// css_mode own starts them over.
func TestCssExtendKeepsOwnRules(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	own := ".polaroid cell { padding: 10pt; background-color: #fff; }\n@media print {\n  page { bleed: 3mm; }\n}"
	c := call(t, s, "create_presentation", map[string]any{"title": "Album", "markdown": "# Album\n\nText.\n", "css": own})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	stored := func() string { return f.db.doc("shares/" + id)["css"].(string) }

	u := call(t, s, "update_presentation", map[string]any{"deck_id": id, "css": "caption { color: #ffffff; }"})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	match(t, stored(), `added for this deck[\s\S]*\.polaroid cell[\s\S]*bleed: 3mm[\s\S]*caption \{ color: #ffffff; \}`)

	g := sc(call(t, s, "get_presentation", map[string]any{"deck_id": id}))
	eq(t, g["css_mode"], "own")
	got := g["css"].(string)
	if strings.Contains(got, "#0b1030") || !strings.Contains(got, ".polaroid cell") || !strings.Contains(got, "caption") {
		t.Fatal("get_presentation css is not the deck's own rules:", got)
	}

	// all of them again, with one more: no rule twice
	u2 := call(t, s, "update_presentation", map[string]any{"deck_id": id, "css": got + "\nh1 { font-size: 40pt; }"})
	if u2.IsError {
		t.Fatal(textOf(u2))
	}
	eq(t, strings.Count(stored(), ".polaroid cell"), 1)
	match(t, stored(), `h1 \{ font-size: 40pt; \}`)

	// own: starts them over
	u3 := call(t, s, "update_presentation", map[string]any{"deck_id": id, "css": "h2 { font-size: 30pt; }", "css_mode": "own"})
	if u3.IsError {
		t.Fatal(textOf(u3))
	}
	if strings.Contains(stored(), ".polaroid") {
		t.Fatal("css_mode own kept the old rules")
	}
	match(t, stored(), `#0b1030[\s\S]*added for this deck --- \*/\nh2 \{ font-size: 30pt; \}`)
}
