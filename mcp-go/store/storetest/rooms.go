// SPDX-License-Identifier: AGPL-3.0-or-later

package storetest

import (
	"testing"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// rooms (ADR 0001): a membership decides, a link does not, and an archived
// room is read only
func rooms(t *testing.T, e store.Engine) {
	s := store.New(e, store.RoomPolicy{Cols: map[string]bool{"decks": true}})
	rs := store.Rooms{S: s}
	pay := must(rs.Create(ctx, alice, "Payment retry", "ticket"))
	brand := must(rs.Create(ctx, bob, "Brand", "assets"))
	must(0, rs.SetMember(ctx, alice, pay, "group:team", store.Viewer))
	must(0, rs.SetMember(ctx, alice, pay, "user:bob", store.Editor))
	isErr(t, rs.SetMember(ctx, bob, pay, "user:eve", store.Owner), store.ErrDenied, "an editor adds members")
	_, _, err := rs.Get(ctx, eve, pay)
	isErr(t, err, store.ErrNotFound, "another tenant sees the room")
	if rs.SetMember(ctx, alice, pay, "user:alice", store.Editor) == nil {
		t.Fatal("the last owner stepped down")
	}

	as := func(p store.Principal) store.Principal { return must(rs.For(ctx, p)) }
	A, B, C, E := as(alice), as(bob), as(carol), as(eve)
	eq(t, []any{A.Rooms[pay], B.Rooms[pay], C.Rooms[pay], E.Rooms[pay]}, []any{store.Owner, store.Editor, store.Viewer, store.NoRole}, "roles, carol by group")
	eq(t, A.Rooms[brand], store.NoRole, "alice is not in Brand")

	deck := store.Doc{"tenant": "A", "room": pay, "title": "architecture"}
	eq(t, must(s.Put(ctx, B, "decks", "arch", deck, 0)), store.Rev(1), "an editor makes a deck")
	_, err = s.Put(ctx, C, "decks", "c1", store.Doc{"tenant": "A", "room": pay}, 0)
	isErr(t, err, store.ErrDenied, "a viewer makes a deck")
	d, _, err := s.Get(ctx, C, "decks", "arch")
	eq(t, []any{d["title"], err}, []any{"architecture", nil}, "a viewer reads")
	must(s.Put(ctx, B, "decks", "logo", store.Doc{"tenant": "A", "room": brand, "title": "brand deck"}, 0))
	_, _, err = s.Get(ctx, A, "decks", "logo")
	isErr(t, err, store.ErrNotFound, "a deck of a room alice is not in")
	eq(t, ids(must(s.Query(ctx, A, store.Query{From: "decks"}))), []string{"arch"}, "alice's search")
	eq(t, ids(must(s.Query(ctx, B, store.Query{From: "decks"}))), []string{"arch", "logo"}, "bob's search")
	_, err = s.Put(ctx, B, "decks", "arch", store.Doc{"tenant": "A", "room": brand, "title": "moved"}, store.AnyRev)
	eq(t, err, nil, "bob moves it: editor in both")
	_, err = s.Put(ctx, B, "decks", "logo", store.Doc{"tenant": "A", "room": "elsewhere"}, store.AnyRev)
	isErr(t, err, store.ErrDenied, "moved to a room bob is not in")
	_, _, err = s.Get(ctx, A, "decks", "arch")
	isErr(t, err, store.ErrNotFound, "moved out of alice's reach")
	must(s.Put(ctx, B, "decks", "arch", deck, store.AnyRev))

	// a link between rooms gives nothing: alice links Payment to Brand,
	// which alice cannot read, and is told it is not there
	ls := store.Links{S: s, Resolve: func(r store.Ref) (string, string, bool) {
		if r.Kind == "room" {
			return store.RoomsCol, r.ID, true
		}
		return "", "", false
	}}
	payBrand := store.Link{From: store.Ref{Kind: "room", ID: pay}, Rel: "inherits_files", To: store.Ref{Kind: "room", ID: brand}}
	_, _, err = ls.Add(ctx, A, payBrand, nil)
	isErr(t, err, store.ErrNotFound, "alice links to a room outside alice's reach")
	_, made, err := ls.Add(ctx, B, payBrand, nil)
	eq(t, []any{made, err}, []any{true, nil}, "bob, in both, links them")
	eq(t, len(must(ls.Of(ctx, A, store.Ref{Kind: "room", ID: pay}))), 0, "alice is not told of a link to Brand")
	eq(t, ids(must(s.Query(ctx, as(alice), store.Query{From: "decks"}))), []string{"arch"}, "and the link gives alice nothing of Brand")
	eq(t, ids(must(s.Query(ctx, A, store.Query{From: store.RoomsCol}))), []string{pay}, "rooms alice reads")

	// archived: read only for everyone, owners too, nothing removed
	isErr(t, rs.Archive(ctx, bob, pay, true), store.ErrDenied, "an editor archives")
	must(0, rs.Archive(ctx, alice, pay, true))
	A, B = as(alice), as(bob)
	eq(t, []any{A.Rooms[pay], B.Rooms[pay]}, []any{store.Viewer, store.Viewer}, "archived: viewers")
	_, err = s.Put(ctx, A, "decks", "arch", deck, store.AnyRev)
	isErr(t, err, store.ErrDenied, "the owner writes in the archive")
	d, _, _ = s.Get(ctx, B, "decks", "arch")
	eq(t, d["title"], "architecture", "still there, still readable")
	eq(t, len(must(rs.List(ctx, alice, false))), 0, "the archive is not listed")
	eq(t, len(must(rs.List(ctx, alice, true))), 1, "unless asked")
	isErr(t, rs.SetMember(ctx, alice, pay, "user:carol", store.Editor), store.ErrDenied, "members change in the archive")
	must(0, rs.Archive(ctx, alice, pay, false))
	eq(t, as(alice).Rooms[pay], store.Owner, "out of the archive")
}
