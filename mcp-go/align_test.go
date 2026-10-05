// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// --- {.center} / {.right} and css text-align, as the editor lays them out
// and in the PowerPoint file

func TestTextAlign(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "# Title\n{.center}\n\nAt the end\n{.right}\n\n## Second\n\nPlain\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Align", "markdown": md, "css": "h2 { text-align: center }"})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	text := textOf(c)
	// the layout report has the right-set paragraph end where the column
	// does (the heading's rule spans it), the plain one start at its left
	head := regexp.MustCompile(`heading "Title" at (\d+),\d+ size (\d+)`).FindStringSubmatch(text)
	end := regexp.MustCompile(`text "At the end" at (\d+),\d+ size (\d+)`).FindStringSubmatch(text)
	plain := regexp.MustCompile(`text "Plain" at (\d+),`).FindStringSubmatch(text)
	if head == nil || end == nil || plain == nil {
		t.Fatal(text)
	}
	num := func(s string) int { n, _ := strconv.Atoi(s); return n }
	eq(t, plain[1], head[1], "plain text at the column's left")
	if d := num(head[1]) + num(head[2]) - (num(end[1]) + num(end[2])); d < 0 || d > 2 {
		t.Fatalf("the right-set paragraph ends %d px from the column's right:\n%s", d, text)
	}
	x := call(t, s, "export_presentation", map[string]any{"deck_id": sc(c)["deck_id"], "format": "pptx"})
	if x.IsError {
		t.Fatal(textOf(x))
	}
	var pp []byte
	for k, v := range f.bucket.saved {
		if strings.HasSuffix(k, ".pptx") {
			pp = v.data
		}
	}
	all := ""
	for _, sl := range pptxSlides(t, pp) {
		all += sl
	}
	eq(t, strings.Count(all, `algn="ctr"`), 2, "the title and the h2, centred in the file")
	eq(t, strings.Count(all, `algn="r"`), 1, "the right-set paragraph")
}
