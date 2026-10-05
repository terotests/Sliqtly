// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// --- two assistants on one deck: the model (rgr/Work.rgr)

func claim(id string, slides []int64, titles ...string) *WorkClaim {
	c := CreateNew_WorkClaim()
	c.id, c.agent, c.slides, c.titles = id, "Agent "+id, slides, titles
	c.started, c.expires = 1000, 2000
	return c
}

func TestWorkClaimsOverlapBySlideTitleOrWholeDeck(t *testing.T) {
	a := claim("a", []int64{2, 3}, "Budget", "Risks")
	eq(t, a.overlaps(claim("b", []int64{3})), true)
	eq(t, a.overlaps(claim("b", []int64{4, 5})), false)
	// slides moved: the same title is still the same slide
	eq(t, a.overlaps(claim("b", []int64{4}, "Risks")), true)
	eq(t, a.overlaps(claim("b", nil)), true)
	eq(t, claim("b", nil).overlaps(a), true)
	eq(t, a.what(), `slides 2, 3 ("Budget", "Risks")`)
	eq(t, claim("b", nil).what(), "the whole deck")
	a.note = "fixing the chart"
	eq(t, a.describe(), `Agent a is working on slides 2, 3 ("Budget", "Risks"): fixing the chart`)
}

func TestWorkBoardClaimsExpireAndRoundTrip(t *testing.T) {
	b := CreateNew_WorkBoard()
	b.put(claim("a", []int64{2}))
	c := claim("b", []int64{2, 4})
	eq(t, len(b.clashes(c)), 1)
	eq(t, len(b.clashes(claim("b", []int64{5}))), 0)
	b.put(c)
	eq(t, len(b.claims), 2)
	// the same id again replaces the claim and keeps when it started
	again := claim("b", []int64{5})
	again.started = 1500
	b.put(again)
	eq(t, len(b.claims), 2)
	eq(t, b.claims[1].slides, []int64{5})
	eq(t, b.claims[1].started, int64(1000))
	eq(t, len(b.touching("a", []int64{5})), 1)
	eq(t, len(b.touching("b", []int64{5})), 0)

	eq(t, b.renew("a", 5000), true)
	eq(t, b.renew("zz", 5000), false)
	b.prune(3000)
	eq(t, len(b.claims), 1)
	eq(t, b.claims[0].id, "a")
	eq(t, b.drop("a"), true)
	eq(t, b.drop("a"), false)

	b.put(claim("c", []int64{1}, "Intro"))
	b.stamp = "s1"
	r := WorkBoard_static_fromJson(MfJ_static_parse(JOut_static_text(b.toJson())))
	eq(t, r.stamp, "s1")
	eq(t, len(r.claims), 1)
	eq(t, []any{r.claims[0].id, r.claims[0].slides, r.claims[0].titles, r.claims[0].expires}, []any{"c", []int64{1}, []string{"Intro"}, int64(2000)})
}

func TestWorkBoardKeepsTheNewestVersions(t *testing.T) {
	b := CreateNew_WorkBoard()
	gone := []string{}
	for i := 0; i < 14; i++ {
		gone = append(gone, b.keepBase(string(rune('a'+i)))...)
	}
	eq(t, gone, []string{"a", "b"})
	eq(t, len(b.bases), 12)
	// kept again: it becomes the newest, nothing goes
	eq(t, len(b.keepBase("c")), 0)
	eq(t, b.bases[11], "c")
	eq(t, b.hasBase("n"), true)
	eq(t, b.hasBase("a"), false)
}

func TestWorkSpansNumberLinesBySlide(t *testing.T) {
	md := "# Plan\n\nIntro\n\n## Budget\n\nNumbers\n\n## Risks\n\nSome"
	starts := WorkSpans_static_lineStarts(md, []int64{2, int64(strings.Index(md, "Budget")), -1, int64(strings.Index(md, "Risks"))})
	eq(t, starts, []int64{0, 4, 4, 8})
	eq(t, WorkSpans_static_slideOfLine(starts, 0), int64(1))
	eq(t, WorkSpans_static_slideOfLine(starts, 6), int64(3))
	eq(t, WorkSpans_static_slideOfLine(starts, 10), int64(4))
	eq(t, WorkSpans_static_slidesOf(starts, 3, 9), []int64{1, 3, 4})
	// an insertion is on the slide of the line before it
	eq(t, WorkSpans_static_slidesOf(starts, 8, 8), []int64{3})
	eq(t, WorkSpans_static_listText([]int64{2, 3, 5}), "2, 3 and 5")
}

const BASE_DECK = "# Plan\n\nIntro\n\n## Budget\n\nNumbers\n\n## Risks\n\nSome"

func baseStarts() []int64 { return []int64{0, 4, 8} }

func TestWorkMergeTakesEachSidesChangeOnOtherSlides(t *testing.T) {
	saved := strings.Replace(BASE_DECK, "Numbers", "Numbers for 2027", 1)
	mine := strings.Replace(BASE_DECK, "Some", "Some, ranked", 1)
	w := WorkMerge_static_run(BASE_DECK, saved, mine, baseStarts())
	eq(t, w.clean(), true)
	eq(t, w.text, "# Plan\n\nIntro\n\n## Budget\n\nNumbers for 2027\n\n## Risks\n\nSome, ranked")
	eq(t, w.theirs, []int64{2})
	eq(t, w.mine, []int64{3})
}

func TestWorkMergeRefusesTheSameLinesChangedTwice(t *testing.T) {
	saved := strings.Replace(BASE_DECK, "Numbers", "Numbers for 2027", 1)
	mine := strings.Replace(BASE_DECK, "Numbers", "Figures", 1)
	w := WorkMerge_static_run(BASE_DECK, saved, mine, baseStarts())
	eq(t, w.clean(), false)
	eq(t, w.clash, []int64{2})
	// the same change on both sides is no conflict
	same := WorkMerge_static_run(BASE_DECK, saved, saved, baseStarts())
	eq(t, same.clean(), true)
	eq(t, same.text, saved)
	// a slide added by each at the end of the same slide meets too
	a := BASE_DECK + "\n\n## Next A\n\nA"
	b := BASE_DECK + "\n\n## Next B\n\nB"
	eq(t, WorkMerge_static_run(BASE_DECK, a, b, baseStarts()).clash, []int64{3})
}

func TestWorkMergeWithManySlidesKeepsTheirOrder(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("# Deck\n")
	for i := 1; i <= 30; i++ {
		sb.WriteString("\n## S" + string(rune('A'+i%26)) + "\n\nline\n")
	}
	base := sb.String()
	saved := strings.Replace(base, "## SB\n\nline", "## SB\n\nline changed by them", 1)
	mine := strings.Replace(base, "## SZ\n\nline", "## SZ\n\nline changed by me", 1)
	w := WorkMerge_static_run(base, saved, mine, nil)
	eq(t, w.clean(), true)
	if !strings.Contains(w.text, "line changed by them") || !strings.Contains(w.text, "line changed by me") {
		t.Fatal(w.text)
	}
}

// --- the protocol through the tools (rgr/Tools.rgr, rgr/WorkStore.rgr)

func TestTwoAssistantsClaimSlidesAndMergeTheirEdits(t *testing.T) {
	f := fakeFirebase()
	now := time.Date(2026, 10, 5, 13, 0, 0, 0, time.UTC)
	e := testEnv(&f, nil)
	e.Now = func() time.Time { return now }
	s := start(t, e, "")
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{"title": "Work", "markdown": BASE_DECK})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	key := sc(c)["edit_key"]
	v0 := sc(c)["version"].(string)
	match(t, textOf(c), `Version: [0-9a-f]{12} \(base_version`)

	// A claims the budget slide
	a := call(t, s, "begin_work", map[string]any{"deck_id": id, "edit_key": key, "agent": "Claude (budget chat)", "slides": []any{2}, "note": "new numbers"})
	if a.IsError {
		t.Fatal(textOf(a))
	}
	eq(t, sc(a)["claimed"], true)
	eq(t, sc(a)["version"], v0)
	wa := sc(a)["work_id"].(string)
	match(t, textOf(a), `Claimed slide 2 \("Budget"\) of "Work" for Claude \(budget chat\) until 13:15 UTC`)

	// B asks for the budget by title and hears who holds it
	b := call(t, s, "begin_work", map[string]any{"deck_id": id, "edit_key": key, "agent": "Claude (risks chat)", "slides": []any{"budget", 3}})
	eq(t, sc(b)["claimed"], false)
	match(t, textOf(b), `Not claimed: slides 2, 3 \("Budget", "Risks"\)`)
	match(t, textOf(b), `- Claude \(budget chat\) is working on slide 2 \("Budget"\): new numbers \(until 13:15 UTC\)`)
	match(t, textOf(b), `ask the user whether to wait`)
	// and takes the risks slide only
	b = call(t, s, "begin_work", map[string]any{"deck_id": id, "edit_key": key, "agent": "Claude (risks chat)", "slides": []any{3}})
	eq(t, sc(b)["claimed"], true)
	wb := sc(b)["work_id"].(string)
	match(t, textOf(b), `Also working on this deck: Claude \(budget chat\)`)
	match(t, textOf(call(t, s, "begin_work", map[string]any{"deck_id": id, "edit_key": key, "slides": []any{9}})), `No slide 9 in this presentation; it has 3 slides: 1 "Plan", 2 "Budget", 3 "Risks"`)

	// get shows the claims and the version
	g := call(t, s, "get_presentation", map[string]any{"deck_id": id})
	eq(t, sc(g)["version"], v0)
	eq(t, len(list(sc(g)["work"])), 2)
	match(t, textOf(g), `Also working on this deck: Claude \(risks chat\) is working on slide 3`)

	// without base_version while another works on the deck: refused
	nb := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edit_key": key, "work_id": wa, "markdown": strings.Replace(BASE_DECK, "Numbers", "Numbers for 2027", 1)})
	eq(t, nb.IsError, true)
	match(t, textOf(nb), `Not saved: another assistant is working on this presentation \(Claude \(risks chat\)`)

	// A saves on v0
	ua := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edit_key": key, "work_id": wa, "base_version": v0, "markdown": strings.Replace(BASE_DECK, "Numbers", "Numbers for 2027", 1)})
	if ua.IsError {
		t.Fatal(textOf(ua))
	}
	v1 := sc(ua)["version"].(string)
	if v1 == v0 {
		t.Fatal("version did not change")
	}
	// B saves on v0 too: merged, A's change kept
	ub := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edit_key": key, "work_id": wb, "base_version": v0, "markdown": strings.Replace(BASE_DECK, "Some", "Some, ranked", 1)})
	if ub.IsError {
		t.Fatal(textOf(ub))
	}
	match(t, textOf(ub), `Merged with the changes saved since version `+v0+` \(slide 2, numbered as in that version\)`)
	doc, _ := f.db.Get(context.Background(), "shares", id)
	eq(t, doc["md"], "# Plan\n\nIntro\n\n## Budget\n\nNumbers for 2027\n\n## Risks\n\nSome, ranked")
	v2 := sc(ub)["version"].(string)

	// A changes the same line on v1 that B's merge did not touch, then
	// B edits the budget line on its old base: a conflict, nothing written
	ua2 := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edit_key": key, "work_id": wa, "base_version": v2, "markdown": "# Plan\n\nIntro\n\n## Budget\n\nNumbers for 2028\n\n## Risks\n\nSome, ranked"})
	if ua2.IsError {
		t.Fatal(textOf(ua2))
	}
	clash := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edit_key": key, "work_id": wb, "base_version": v2, "markdown": "# Plan\n\nIntro\n\n## Budget\n\nNo numbers\n\n## Risks\n\nSome, ranked"})
	eq(t, clash.IsError, true)
	match(t, textOf(clash), `Not saved: since version `+v2+` someone else changed the same lines on slide 2 \(numbered as in that version\); Claude \(budget chat\) is working on slide 2`)
	doc, _ = f.db.Get(context.Background(), "shares", id)
	match(t, doc["md"].(string), `Numbers for 2028`)

	// a version this server never handed out cannot be merged with
	match(t, textOf(call(t, s, "update_presentation", map[string]any{"deck_id": id, "edit_key": key, "base_version": "000000000000", "markdown": "# X"})), `that version is not kept here`)

	// B is done
	ew := call(t, s, "end_work", map[string]any{"deck_id": id, "edit_key": key, "work_id": wb})
	eq(t, sc(ew)["ended"], true)
	match(t, textOf(ew), `Still working on this deck: Claude \(budget chat\)`)
	match(t, textOf(call(t, s, "end_work", map[string]any{"deck_id": id, "edit_key": key, "work_id": wb})), `No claim `+wb)

	// A's claim runs out: the deck is free and an update without a base saves
	now = now.Add(20 * time.Minute)
	g = call(t, s, "get_presentation", map[string]any{"deck_id": id})
	eq(t, len(list(sc(g)["work"])), 0)
	free := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edit_key": key, "work_id": wa, "markdown": "# Plan\n\nDone"})
	if free.IsError {
		t.Fatal(textOf(free))
	}
	match(t, textOf(free), `Your claim `+wa+` had ended or run out`)

	// the claims and the kept versions are the server's own documents
	board, _ := f.db.Get(context.Background(), "mcp_work", id)
	if board == nil || board["expires"] == nil {
		t.Fatal("no mcp_work document with expires", board)
	}
	if got, _ := f.db.Get(context.Background(), "mcp_bases", id+"-"+v0); got == nil || got["md"] != BASE_DECK {
		t.Fatal("version v0 not kept", got)
	}
}

func TestClaimsNeedTheEditKey(t *testing.T) {
	f := fakeFirebase()
	s := start(t, testEnv(&f, nil), "")
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{"title": "Work", "markdown": BASE_DECK})
	id := sc(c)["deck_id"].(string)
	match(t, textOf(call(t, s, "begin_work", map[string]any{"deck_id": id})), `edit_key is needed`)
	match(t, textOf(call(t, s, "begin_work", map[string]any{"deck_id": id, "edit_key": sc(c)["edit_key"], "minutes": 500})), `minutes is 1 to 120`)
	// forced onto slides another holds, after the user agreed
	key := sc(c)["edit_key"]
	call(t, s, "begin_work", map[string]any{"deck_id": id, "edit_key": key, "agent": "A"})
	f2 := call(t, s, "begin_work", map[string]any{"deck_id": id, "edit_key": key, "agent": "B", "slides": []any{2}, "force": true})
	eq(t, sc(f2)["claimed"], true)
	match(t, textOf(f2), `Taken although A is working on the whole deck`)
}

// the folder server writes over a field it read, or not at all
func TestFolderStoreUpdatesOnlyOverWhatItRead(t *testing.T) {
	db, _, err := newFSStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ok, err := db.UpdateIf(context.Background(), "mcp_work", "deck1", "stamp", "", Doc{"stamp": "s1", "claims": []any{}})
	eq(t, []any{ok, err}, []any{true, nil})
	ok, _ = db.UpdateIf(context.Background(), "mcp_work", "deck1", "stamp", "", Doc{"stamp": "s2"})
	eq(t, ok, false)
	ok, _ = db.UpdateIf(context.Background(), "mcp_work", "deck1", "stamp", "s1", Doc{"stamp": "s2"})
	eq(t, ok, true)
	d, _ := db.Get(context.Background(), "mcp_work", "deck1")
	eq(t, d["stamp"], "s2")
	eq(t, len(d["claims"].([]any)), 0)
}

// edits are made on the deck as it is: no base version, merged as any save
func TestEditsNeedNoBaseVersionWhileOthersWork(t *testing.T) {
	f := fakeFirebase()
	s := start(t, testEnv(&f, nil), "")
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{"title": "Work", "markdown": BASE_DECK})
	id, key := sc(c)["deck_id"].(string), sc(c)["edit_key"]
	call(t, s, "begin_work", map[string]any{"deck_id": id, "edit_key": key, "agent": "A", "slides": []any{2}})
	u := call(t, s, "update_presentation", map[string]any{"deck_id": id, "edit_key": key, "edits": []any{map[string]any{"find": "Some", "replace": "Some, ranked"}}})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	match(t, textOf(u), `Also working on this deck: A is working on slide 2`)
	doc, _ := f.db.Get(context.Background(), "shares", id)
	match(t, doc["md"].(string), `Some, ranked$`)
}
