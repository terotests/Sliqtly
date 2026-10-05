// SPDX-License-Identifier: AGPL-3.0-or-later

package storetest

import (
	"testing"

	"github.com/terotests/sliqtly/mcp-go/store"
)

func deckRef(id string) store.Ref { return store.Ref{Kind: "deck", ID: id} }

func links(t *testing.T, e store.Engine) {
	s := guarded(t, e)
	ls := store.Links{S: s, Resolve: func(r store.Ref) (string, string, bool) {
		if r.Kind == "deck" {
			return "decks", r.ID, true
		}
		return "", "", false
	}}
	jira := store.Ref{Kind: "jira", ID: "PROJ-12345"}
	mineToShared := store.Link{From: deckRef("alices"), Rel: "reviews", To: deckRef("bobShared")}

	id, made, err := ls.Add(ctx, alice, mineToShared, nil)
	eq(t, []any{made, err}, []any{true, nil}, "added")
	id2, made, err := ls.Add(ctx, alice, mineToShared, store.Doc{"note": "again"})
	eq(t, []any{id2, made, err}, []any{id, false, nil}, "the same link once")

	// one link however its symmetric ends are given
	a := store.Link{From: deckRef("alices"), Rel: "related", To: deckRef("bobShared")}
	b := store.Link{From: deckRef("bobShared"), Rel: "related", To: deckRef("alices")}
	eq(t, store.LinkID(a), store.LinkID(b), "symmetric")
	if store.LinkID(mineToShared) == store.LinkID(store.Link{From: deckRef("bobShared"), Rel: "reviews", To: deckRef("alices")}) {
		t.Fatal("a directed link reversed is the same link")
	}

	// a link to what they cannot read is not made, and says nothing more
	// than a link to a missing deck does
	_, _, errHidden := ls.Add(ctx, alice, store.Link{From: deckRef("alices"), Rel: "related", To: deckRef("bobPrivate")}, nil)
	_, _, errMissing := ls.Add(ctx, alice, store.Link{From: deckRef("alices"), Rel: "related", To: deckRef("nothing")}, nil)
	isErr(t, errHidden, store.ErrNotFound, "to a hidden deck")
	eq(t, errHidden, errMissing, "hidden and missing alike")

	// ends the store does not hold (a Jira issue) are taken as they are
	_, made, err = ls.Add(ctx, alice, store.Link{From: deckRef("alices"), Rel: "ticket", To: jira}, nil)
	eq(t, []any{made, err}, []any{true, nil}, "to a ticket")

	got := must(ls.Of(ctx, alice, deckRef("alices")))
	eq(t, len(got), 2, "alice's deck has two links")
	got = must(ls.Of(ctx, bob, deckRef("bobShared")))
	eq(t, len(got), 0, "a link to bob's deck from one bob cannot read is not shown")
	got = must(ls.Of(ctx, bob, jira))
	eq(t, len(got), 0, "nor the ticket's link to it")
	must(store.Put(ctx, s.Privileged(), "decks", "alices", store.Doc{"tenant": "A", "owner": "alice", "readers": []any{"bob"}, "title": "mine"}, store.AnyRev))
	got = must(ls.Of(ctx, bob, deckRef("bobShared")))
	eq(t, got, []store.Link{mineToShared}, "shared with bob: the link is shown, whoever made it")
	got = must(ls.Of(ctx, eve, deckRef("alices")))
	eq(t, len(got), 0, "another tenant")

	// a link made between two decks bob owns, then one of them hidden from
	// alice: the link goes from alice's view
	_, _, err = ls.Add(ctx, bob, store.Link{From: deckRef("bobShared"), Rel: "related", To: deckRef("bobPrivate")}, nil)
	eq(t, err, nil)
	got = must(ls.Of(ctx, alice, deckRef("bobShared")))
	eq(t, got, []store.Link{mineToShared}, "the link to bob's private deck is not alice's to see")

	isErr(t, ls.Remove(ctx, bob, mineToShared), store.ErrDenied, "not bob's link")
	eq(t, ls.Remove(ctx, alice, mineToShared), nil, "alice's own")
	got = must(ls.Of(ctx, bob, deckRef("bobShared")))
	eq(t, len(got), 1, "gone, bob's own kept")

	for _, bad := range []store.Link{
		{From: deckRef("alices"), Rel: "Bad Rel", To: jira},
		{From: store.Ref{Kind: "deck", ID: "has space"}, Rel: "x", To: jira},
		{From: deckRef("alices"), Rel: "x", To: deckRef("alices")},
	} {
		if _, _, err := ls.Add(ctx, alice, bad, nil); err == nil {
			t.Fatalf("%v was taken", bad)
		}
	}
	r, err := store.ParseRef("file:aB3/media/cat.png")
	eq(t, []any{r, err}, []any{store.Ref{Kind: "file", ID: "aB3/media/cat.png"}, nil}, "a ref read")
}
