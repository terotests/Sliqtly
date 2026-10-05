// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

// --- partial updates on their own (rgr/Edits.rgr, src/PresSlideSpans.rgr)

const EDECK = "---\ntitle: T\n---\n# Title\n\nIntro\n\n## One\n\n- a\n- b\n\n## Two\n\nText two\n\n## Three\n\nThe end\n"

// where each slide starts and its title, as PresDeck lays EDECK out
func edeckSlides() ([]int64, []string) {
	var starts []int64
	for _, h := range []string{"# Title", "## One", "## Two", "## Three"} {
		starts = append(starts, int64(strings.Index(EDECK, h)))
	}
	return starts, []string{"Title", "One", "Two", "Three"}
}

func textEdit(find, repl string, all bool) *DeckEdit {
	e := CreateNew_DeckEdit()
	e.kind, e.find, e.replace, e.all = "text", find, repl, all
	return e
}

func slideEdit(n int64, title, md string) *DeckEdit {
	e := CreateNew_DeckEdit()
	e.kind, e.slide, e.title, e.markdown = "slide", n, title, md
	return e
}

func insertEdit(after int64, md string) *DeckEdit {
	e := CreateNew_DeckEdit()
	e.kind, e.after, e.markdown = "insert", after, md
	return e
}

func applyEdits(edits ...*DeckEdit) *DeckEditsOut {
	starts, titles := edeckSlides()
	return DeckEdits_static_apply(EDECK, edits, starts, titles)
}

func TestSlideSpansCoverSections(t *testing.T) {
	starts, titles := edeckSlides()
	eq(t, PresSlideSpans_static_lines(EDECK, starts, titles, 0), []int64{3, 7})
	eq(t, PresSlideSpans_static_lines(EDECK, starts, titles, 1), []int64{7, 12})
	eq(t, PresSlideSpans_static_lines(EDECK, starts, titles, 3), []int64{16, 20})
	// a slide the overflow went on to (same title, starts mid-section) is its heading's
	ov := strings.Index(EDECK, "- b")
	s2 := []int64{starts[0], starts[1], int64(ov), starts[2], starts[3]}
	t2 := []string{"Title", "One", "One", "Two", "Three"}
	eq(t, PresSlideSpans_static_lines(EDECK, s2, t2, 2), []int64{7, 12})
	eq(t, PresSlideSpans_static_lines(EDECK, s2, t2, 9), []int64{-1, 20})
}

func TestEditsReplaceText(t *testing.T) {
	o := applyEdits(textEdit("Text two", "Second text", false))
	eq(t, o.err, "")
	eq(t, o.md, strings.Replace(EDECK, "Text two", "Second text", 1))
	eq(t, o.done, []string{"Edit 1: text replaced on slide 3."})

	o = applyEdits(textEdit("- ", "* ", false))
	match(t, o.err, `^Edit 1: the text in find is in the deck 2 times`)
	o = applyEdits(textEdit("- ", "* ", true))
	eq(t, o.md, strings.ReplaceAll(EDECK, "- ", "* "))
	eq(t, o.done, []string{"Edit 1: text replaced (2 times) on slide 2."})

	o = applyEdits(textEdit("Text  two", "x", false))
	match(t, o.err, `^Edit 1: the text in find is not in the deck`)
	if o.md != "" {
		t.Fatal("a refused edit gave text")
	}
}

func TestEditsReplaceDeleteAndAddSlides(t *testing.T) {
	o := applyEdits(slideEdit(3, "", "## 2nd\n\nNew text\n\n\n"))
	eq(t, o.err, "")
	eq(t, o.md, strings.Replace(EDECK, "## Two\n\nText two\n", "## 2nd\n\nNew text\n", 1))
	eq(t, o.done, []string{`Edit 1: slide 3 "Two" replaced.`})

	// by title, any case; the last slide
	o = applyEdits(slideEdit(0, "three", "## Last\n\nBye"))
	eq(t, o.md, strings.Replace(EDECK, "## Three\n\nThe end\n", "## Last\n\nBye\n", 1))

	o = applyEdits(slideEdit(2, "", ""))
	eq(t, o.md, strings.Replace(EDECK, "## One\n\n- a\n- b\n\n", "", 1))
	eq(t, o.done, []string{`Edit 1: slide 2 "One" deleted.`})

	o = applyEdits(insertEdit(2, "## New\n\nx"))
	eq(t, o.md, strings.Replace(EDECK, "## Two", "## New\n\nx\n\n## Two", 1))
	o = applyEdits(insertEdit(4, "## After\n\ny\n"))
	eq(t, o.md, EDECK+"\n## After\n\ny\n")
	o = applyEdits(insertEdit(0, "## First"))
	eq(t, o.md, strings.Replace(EDECK, "---\n# Title", "---\n\n## First\n\n# Title", 1))

	// numbers are the deck's before the edits, in any order
	o = applyEdits(slideEdit(2, "", ""), textEdit("The end", "Fin", false), insertEdit(1, "## Mid"))
	eq(t, o.err, "")
	eq(t, o.md, "---\ntitle: T\n---\n# Title\n\nIntro\n\n## Mid\n\n## Two\n\nText two\n\n## Three\n\nFin\n")
}

func TestEditsRefuseUnclearOnes(t *testing.T) {
	match(t, applyEdits(slideEdit(9, "", "## x")).err, `^Edit 1: there is no slide 9; the deck has 4\.$`)
	match(t, applyEdits(slideEdit(0, "Nope", "## x")).err, `no slide is titled "Nope"`)
	match(t, applyEdits(slideEdit(2, "", "just text")).err, `starts with the slide's heading`)
	match(t, applyEdits(insertEdit(5, "## x")).err, `after is a slide number from 0 \(before the first\) to 4`)
	match(t, applyEdits(slideEdit(2, "", "## A"), textEdit("- a", "- c", false)).err, `^Edits 1 and 2 change the same part`)
	match(t, applyEdits(slideEdit(2, "", "## A"), slideEdit(0, "One", "## B")).err, `^Edits 1 and 2 change the same part`)
	starts, _ := edeckSlides()
	dup := DeckEdits_static_apply(EDECK, []*DeckEdit{slideEdit(0, "One", "## x")}, starts, []string{"Title", "One", "Two", "One"})
	match(t, dup.err, `slides 2, 4 are all titled "One": give the slide number`)
}

// --- through update_presentation

func TestUpdateWithEdits(t *testing.T) {
	f := fakeFirebase()
	s := start(t, testEnv(&f, nil), "")
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{"title": "E", "markdown": EDECK})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	out := sc(c)
	id, key := out["deck_id"].(string), out["edit_key"]

	u := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edit_key": key, "edits": []any{
		map[string]any{"find": "Intro", "replace": "Welcome"},
		map[string]any{"slide": 4, "markdown": "## Three\n\nThe very end"},
		map[string]any{"after_slide": 2, "markdown": "## Extra\n\n- z"},
	}})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	eq(t, f.db.doc("shares/" + id)["md"], "---\ntitle: T\n---\n# Title\n\nWelcome\n\n## One\n\n- a\n- b\n\n## Extra\n\n- z\n\n## Two\n\nText two\n\n## Three\n\nThe very end\n")
	eq(t, sc(u)["slides"], 5)
	match(t, textOf(u), `Edit 2: slide 4 "Three" replaced\.`)

	bad := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edit_key": key, "edits": []any{map[string]any{"find": "nothing like this", "replace": "x"}}})
	if !bad.IsError {
		t.Fatal("an edit that found nothing was saved")
	}
	match(t, textOf(bad), `^Not updated: Edit 1: the text in find is not in the deck`)
	both := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edit_key": key, "markdown": "# x", "edits": []any{}})
	match(t, textOf(both), `markdown \(the whole deck\) or edits, not both`)
	odd := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edit_key": key, "edits": []any{map[string]any{"slide": 2}}})
	match(t, textOf(odd), `Edit 1: markdown is needed`)
}
