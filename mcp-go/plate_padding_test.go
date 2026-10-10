// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// `.cover { padding-top: 300pt }` on a slide's title: the room is over the
// text only and the title keeps its size. The padding used to go under the
// text too, and the title was set smaller and smaller to fit a plate whose
// padding no size could make smaller (72 px text down to 14 px).
func TestHeadingPaddingTop(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{"title": "W", "markdown": "# Deck\n\n## Title\n{.cover}\n\nText under\n",
		"css": ".cover { padding-top: 300pt; }\n"})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	m := regexp.MustCompile(`heading "Title" at [\d,]+ size \d+×(\d+), text (\d+) px`).FindStringSubmatch(textOf(c))
	if m == nil {
		t.Fatal(textOf(c))
	}
	h, _ := strconv.Atoi(m[1])
	px, _ := strconv.Atoi(m[2])
	// 300 pt is 600 px on a 1920 px slide, the text 111 px
	if px != 72 || h > 800 {
		t.Fatalf("the title is %d px tall with %d px text, want under 800 px and 72 px:\n%s", h, px, textOf(c))
	}
	if strings.Contains(textOf(c), "(continued)") {
		t.Fatal("the text under the title went to a slide of its own:", textOf(c))
	}
}
