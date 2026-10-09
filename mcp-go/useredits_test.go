// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"strings"
	"testing"
)

// --- what the user changed by hand since an assistant last saw the deck:
// the model (rgr/UserEdits.rgr)

const CHART_DECK = "---\ntitle: Sales\n---\n\n# Sales\n\nIntro\n\n## Chart\n\n```chart {.wide}\ntitle: Sales by month\nstyle: bars\n```\n\n## End\n\nThanks"

func chartStarts() []int64 { return []int64{0, 8, 16} }

func TestUserEditsNameEachChangeAndWhereItIs(t *testing.T) {
	user := strings.Replace(CHART_DECK, "title: Sales by month", "title: Sales by month (EUR)", 1)
	user = strings.Replace(user, "Thanks", "Thank you!", 1)
	u := UserEdits_static_find(CHART_DECK, user, user, chartStarts(), false)
	eq(t, len(u.edits), 2)
	e := u.edits[0]
	eq(t, []any{e.slide, e.where, e.before, e.user, e.overwritten}, []any{int64(2), "```chart block", []string{"title: Sales by month"}, []string{"title: Sales by month (EUR)"}, false})
	eq(t, []any{u.edits[1].slide, u.edits[1].where}, []any{int64(3), "slide text"})
	// a read: no word of saving
	eq(t, u.text(40, false), "- slide 2, ```chart block:\n```diff\n-title: Sales by month\n+title: Sales by month (EUR)\n```\n- slide 3, slide text:\n```diff\n-Thanks\n+Thank you!\n```")
	// front matter, and spaces only are no change
	fm := strings.Replace(CHART_DECK, "title: Sales\n", "title: Sales 2026\n", 1)
	fm = strings.Replace(fm, "Intro", "Intro  ", 1)
	f := UserEdits_static_find(CHART_DECK, fm, fm, nil, false)
	eq(t, len(f.edits), 1)
	eq(t, []any{f.edits[0].slide, f.edits[0].where}, []any{int64(0), "front matter"})
	eq(t, UserEdits_static_find(CHART_DECK, CHART_DECK, CHART_DECK, nil, false).any(), false)
}

func TestUserEditsSeeWhatASaveOverwrites(t *testing.T) {
	user := strings.Replace(CHART_DECK, "style: bars", "style: lines", 1)
	user = strings.Replace(user, "Thanks", "Thank you!", 1)
	// the assistant's save, made on what it remembers: the old chart style
	// is back, its own change on the title slide
	saved := strings.Replace(CHART_DECK, "Intro", "Intro, updated", 1)
	u := UserEdits_static_find(CHART_DECK, user, saved, chartStarts(), false)
	eq(t, len(u.edits), 2)
	eq(t, []any{u.edits[0].overwritten, u.edits[0].saved}, []any{true, []string{"style: bars"}})
	eq(t, u.edits[1].overwritten, true)
	eq(t, len(u.overwritten()), 2)
	match(t, u.text(40, true), "- slide 2, ```chart block \\(overwritten by this save\\):\n```diff\n-style: bars\n\\+style: lines\n```\n  This save made them:\n```\nstyle: bars\n```")
	// a save that keeps them (made on the user's text)
	kept := strings.Replace(user, "Intro", "Intro, updated", 1)
	k := UserEdits_static_find(CHART_DECK, user, kept, chartStarts(), false)
	eq(t, len(k.overwritten()), 0)
	match(t, k.text(40, true), `chart block \(kept\)`)
	// lines the user deleted, put back by the save
	del := strings.Replace(CHART_DECK, "\n## End\n\nThanks", "", 1)
	d := UserEdits_static_find(CHART_DECK, del, CHART_DECK, nil, false)
	eq(t, []any{len(d.edits), d.edits[0].overwritten, d.edits[0].user}, []any{1, true, []string{}})
}

func TestUserEditsInAStylesheetByRule(t *testing.T) {
	seen := "h1 {\n  color: #222;\n}\n\nh2 {\n  color: #333;\n}"
	user := strings.Replace(seen, "#333", "#c00", 1)
	u := UserEdits_static_find(seen, user, seen, nil, true)
	eq(t, len(u.edits), 1)
	eq(t, []any{u.edits[0].where, u.edits[0].overwritten}, []any{"css rule h2", true})
}

func TestUserEditsTextIsKeptShort(t *testing.T) {
	var a, b strings.Builder
	for i := 0; i < 30; i++ {
		a.WriteString("line\n\nsame\n\n")
		b.WriteString("changed " + strings.Repeat("x", 200) + "\n\nsame\n\n")
	}
	u := UserEdits_static_find(a.String(), b.String(), b.String(), nil, false)
	txt := u.text(20, false)
	if n := strings.Count(txt, "\n") + 1; n > 21 {
		t.Fatal("too long", n)
	}
	match(t, txt, `- … and 26 more \(get_presentation shows`)
	match(t, txt, `\+changed x+\.\.\.\n`)
}

func TestUserEditsFenceEdges(t *testing.T) {
	lines := strings.Split("a\n```stats\nx\n```\nb\n~~~\ny", "\n")
	eq(t, UserEdits_static_mdPlace(lines, 1), "```stats block")
	eq(t, UserEdits_static_mdPlace(lines, 2), "```stats block")
	eq(t, UserEdits_static_mdPlace(lines, 3), "```stats block")
	eq(t, UserEdits_static_mdPlace(lines, 4), "slide text")
	eq(t, UserEdits_static_mdPlace(lines, 6), "code block")
	eq(t, UserEdits_static_mdPlace(lines, 99), "code block")
}

// --- through the tools: the user edits in the editor between the
// assistant's read and its save

func userEdit(t *testing.T, f *fb, id string, change func(md string) string) {
	t.Helper()
	doc, _ := f.db.Get(context.Background(), "shares", id)
	doc["md"] = change(doc["md"].(string))
	f.db.Set(context.Background(), "shares", id, doc)
}

func TestSavesTellWhatTheUserChangedByHand(t *testing.T) {
	f := fakeFirebase()
	s := start(t, testEnv(&f, nil), "")
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{"title": "Sales", "markdown": BASE_DECK})
	id := sc(c)["deck_id"].(string)

	// the user retitles a line in the editor; the assistant saves the whole
	// text as it remembers it: saved, and told what it overwrote
	userEdit(t, &f, id, func(md string) string { return strings.Replace(md, "Numbers", "Numbers in EUR", 1) })
	u := call(t, s, "update_presentation", map[string]any{"deck_id": id, "markdown": strings.Replace(BASE_DECK, "Some", "Some, ranked", 1)})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	match(t, textOf(u), `The user changed this presentation by hand since an assistant last read it`)
	match(t, textOf(u), "- slide 2, slide text \\(overwritten by this save\\):\n```diff\n-Numbers\n\\+Numbers in EUR\n```")
	match(t, textOf(u), `This save overwrote 1 of those changes\. Unless the user asked for that, put the user's version back`)
	ue := sc(u)["user_edits"].(map[string]any)
	eq(t, ue["overwritten"], float64(1))
	md := list(ue["markdown"])
	eq(t, len(md), 1)
	eq(t, md[0].(map[string]any)["user"], []any{"Numbers in EUR"})

	// nothing changed by hand since that save: nothing said
	u2 := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edits": []any{map[string]any{"find": "Intro", "replace": "Intro!"}}})
	if strings.Contains(textOf(u2), "by hand") || sc(u2)["user_edits"] != nil {
		t.Fatal(textOf(u2))
	}

	// the user changes a line, then an edit elsewhere keeps it
	userEdit(t, &f, id, func(md string) string { return strings.Replace(md, "# Plan", "# Plan 2027", 1) })
	u3 := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edits": []any{map[string]any{"find": "ranked", "replace": "ranked by cost"}}})
	match(t, textOf(u3), "- slide 1, slide text \\(kept\\):\n```diff\n-# Plan\n\\+# Plan 2027\n```")
	match(t, textOf(u3), `These are the user's choices: keep them, and follow them`)

	// a read shows the user's changes since the last save, once
	userEdit(t, &f, id, func(md string) string { return strings.Replace(md, "Intro!", "Intro, short", 1) })
	g := call(t, s, "get_presentation", map[string]any{"deck_id": id})
	match(t, textOf(g), "- slide 1, slide text:\n```diff\n-Intro!\n\\+Intro, short\n```")
	if sc(g)["user_edits"] == nil {
		t.Fatal("no user_edits on get")
	}
	g2 := call(t, s, "get_presentation", map[string]any{"deck_id": id})
	if strings.Contains(textOf(g2), "by hand") {
		t.Fatal(textOf(g2))
	}

	// the stylesheet too: the user changes a colour, the assistant sends
	// its old rules again
	call(t, s, "update_presentation", map[string]any{"deck_id": id, "css": "h2 {\n  color: #333;\n}", "css_mode": "replace"})
	userEdit(t, &f, id, func(md string) string { return md })
	doc, _ := f.db.Get(context.Background(), "shares", id)
	doc["css"] = strings.Replace(doc["css"].(string), "#333", "#c00", 1)
	f.db.Set(context.Background(), "shares", id, doc)
	u4 := call(t, s, "update_presentation", map[string]any{"deck_id": id, "css": "h2 {\n  color: #333;\n}", "css_mode": "replace"})
	match(t, textOf(u4), "In the stylesheet \\(css\\):\n- css rule h2 \\(overwritten by this save\\):\n```diff\n-  color: #333;\n\\+  color: #c00;")
}

// with base_version the user's lines are merged in, and said so; a change
// to the same lines is refused with the user's version in the refusal
func TestMergedSavesShowTheUsersChanges(t *testing.T) {
	f := fakeFirebase()
	s := start(t, testEnv(&f, nil), "")
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{"title": "Sales", "markdown": BASE_DECK})
	id := sc(c)["deck_id"].(string)
	v0 := sc(c)["version"].(string)
	userEdit(t, &f, id, func(md string) string { return strings.Replace(md, "Numbers", "Numbers in EUR", 1) })
	u := call(t, s, "update_presentation", map[string]any{"deck_id": id, "base_version": v0, "markdown": strings.Replace(BASE_DECK, "Some", "Some, ranked", 1)})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	match(t, textOf(u), `Changed since version `+v0+`, which your edit started from`)
	match(t, textOf(u), `slide 2, slide text \(kept\)`)
	v1 := sc(u)["version"].(string)
	userEdit(t, &f, id, func(md string) string { return strings.Replace(md, "Numbers in EUR", "Numbers in USD", 1) })
	clash := call(t, s, "update_presentation", map[string]any{"deck_id": id, "base_version": v1, "markdown": strings.Replace(strings.Replace(BASE_DECK, "Some", "Some, ranked", 1), "Numbers", "Figures", 1)})
	eq(t, clash.IsError, true)
	match(t, textOf(clash), "What was changed since version "+v1+" \\(- as you had it, \\+ as it is now\\):\n- slide text:\n```diff\n-Numbers in EUR\n\\+Numbers in USD\n```")
}
