// SPDX-License-Identifier: AGPL-3.0-or-later

package storetest

import (
	"context"
	"testing"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// the guard tests: what a principal may not read does not reach them by
// any way in, whatever the backend enforces itself (store.OwnerPolicy)
var (
	alice  = store.Principal{UserID: "alice", TenantID: "A"}
	bob    = store.Principal{UserID: "bob", TenantID: "A"}
	carol  = store.Principal{UserID: "carol", TenantID: "A", Groups: []string{"team"}}
	adminA = store.Principal{UserID: "root", TenantID: "A", Roles: []string{"admin"}}
	eve    = store.Principal{UserID: "eve", TenantID: "B", Roles: []string{"admin"}}
	nobody = store.Principal{}
)

func guarded(t *testing.T, e store.Engine) *store.Store {
	t.Helper()
	s := store.New(e, store.OwnerPolicy{})
	for id, d := range map[string]store.Doc{
		"bobPrivate": {"tenant": "A", "owner": "bob", "title": "secret plan"},
		"bobShared":  {"tenant": "A", "owner": "bob", "readers": []any{"alice"}, "title": "shared"},
		"bobTeam":    {"tenant": "A", "owner": "bob", "writers": []any{"group:team"}, "title": "team"},
		"alices":     {"tenant": "A", "owner": "alice", "title": "mine"},
		"eves":       {"tenant": "B", "owner": "eve", "title": "other tenant"},
	} {
		must(store.Put(ctx, e, "decks", id, d, 0))
	}
	return s
}

func guardGet(t *testing.T, e store.Engine) {
	s := guarded(t, e)
	_, _, errHidden := s.Get(ctx, alice, "decks", "bobPrivate")
	_, _, errMissing := s.Get(ctx, alice, "decks", "noSuchDeck")
	isErr(t, errHidden, store.ErrNotFound, "Bob's private deck")
	eq(t, errHidden, errMissing, "hidden and missing are the same answer")
	d, _, err := s.Get(ctx, alice, "decks", "bobShared")
	eq(t, []any{d["title"], err}, []any{"shared", nil}, "shared with alice")
	_, _, err = s.Get(ctx, nobody, "decks", "alices")
	isErr(t, err, store.ErrNotFound, "no principal")
	d, _, _ = s.Get(ctx, adminA, "decks", "bobPrivate")
	eq(t, d["title"], "secret plan", "the tenant's admin")
}

func guardQuery(t *testing.T, e store.Engine) {
	s := guarded(t, e)
	q := func(p store.Principal, w store.Expr) []string {
		t.Helper()
		return ids(must(s.Query(ctx, p, store.Query{From: "decks", Where: w})))
	}
	eq(t, q(alice, nil), []string{"alices", "bobShared"}, "alice sees alice's own and the shared one")
	eq(t, q(alice, store.Eq("title", "secret plan")), []string{}, "a search does not find Bob's private deck")
	eq(t, q(alice, store.Eq("owner", "bob")), []string{"bobShared"}, "nor by its owner")
	eq(t, q(carol, nil), []string{"bobTeam"}, "by carol's group")
	eq(t, q(adminA, nil), []string{"alices", "bobPrivate", "bobShared", "bobTeam"}, "admin: the whole tenant, no more")
	eq(t, q(nobody, nil), []string{}, "no principal")
	page := must(s.Query(ctx, alice, store.Query{From: "decks", Limit: 1, OrderBy: []store.Order{{Field: "_id"}}}))
	eq(t, ids(page), []string{"alices"}, "a page is of what alice may read")
}

func guardWatch(t *testing.T, e store.Engine) {
	s := guarded(t, e)
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	ch := must(s.Watch(wctx, alice, s.Head()))
	p := s.Privileged()
	must(store.Put(ctx, p, "decks", "bobPrivate", store.Doc{"tenant": "A", "owner": "bob", "title": "secret v2"}, store.AnyRev))
	must(store.Put(ctx, p, "decks", "eves", store.Doc{"tenant": "B", "owner": "eve", "title": "B v2"}, store.AnyRev))
	quiet(t, ch)
	// shared with alice: told, with no word of before
	must(store.Put(ctx, p, "decks", "bobPrivate", store.Doc{"tenant": "A", "owner": "bob", "readers": []any{"alice"}, "title": "secret v3"}, store.AnyRev))
	c := next(t, ch)
	eq(t, []any{c.ID, c.Doc["title"], c.Old == nil}, []any{"bobPrivate", "secret v3", true}, "shared")
	// unshared: it goes from alice's view, with nothing of what it is now
	must(store.Put(ctx, p, "decks", "bobPrivate", store.Doc{"tenant": "A", "owner": "bob", "title": "secret v4"}, store.AnyRev))
	c = next(t, ch)
	eq(t, []any{c.ID, c.Doc == nil, c.Old == nil, c.Rev}, []any{"bobPrivate", true, true, store.Rev(0)}, "unshared")
	must(0, store.Delete(ctx, p, "decks", "bobPrivate", store.AnyRev))
	quiet(t, ch)
	must(0, store.Delete(ctx, p, "decks", "alices", store.AnyRev))
	c = next(t, ch)
	eq(t, []any{c.ID, c.Doc == nil, c.Old["title"]}, []any{"alices", true, "mine"}, "alice's removed")
}

func guardWrite(t *testing.T, e store.Engine) {
	s := guarded(t, e)
	p := s.Privileged()
	// a document alice may not read is, for alice, not there: fn hears so,
	// and nothing is written over it
	_, _, err := s.Update(ctx, alice, "decks", "bobPrivate", func(cur store.Doc, rev store.Rev) (store.Doc, error) {
		if cur != nil || rev != 0 {
			t.Fatalf("alice saw %v", cur)
		}
		return store.Doc{"tenant": "A", "owner": "alice", "title": "taken over"}, nil
	})
	isErr(t, err, store.ErrConflict, "made over a hidden one")
	_, err = s.Put(ctx, alice, "decks", "bobPrivate", store.Doc{"tenant": "A", "owner": "alice"}, store.AnyRev)
	isErr(t, err, store.ErrConflict, "put over a hidden one")
	must(0, s.Delete(ctx, alice, "decks", "bobPrivate", store.AnyRev))
	isErr(t, s.Delete(ctx, alice, "decks", "bobPrivate", 1), store.ErrNotFound, "delete of a rev alice cannot see")
	d, _, _ := p.Get(ctx, "decks", "bobPrivate")
	eq(t, d["title"], "secret plan", "still Bob's, untouched")

	// read only
	_, err = s.Put(ctx, alice, "decks", "bobShared", store.Doc{"tenant": "A", "owner": "bob", "readers": []any{"alice"}, "title": "edited"}, store.AnyRev)
	isErr(t, err, store.ErrDenied, "a reader writes")
	// a writer changes the content, not who has it
	_, err = s.Put(ctx, carol, "decks", "bobTeam", store.Doc{"tenant": "A", "owner": "bob", "writers": []any{"group:team"}, "title": "edited"}, store.AnyRev)
	eq(t, err, nil, "a writer edits")
	_, err = s.Put(ctx, carol, "decks", "bobTeam", store.Doc{"tenant": "A", "owner": "carol", "writers": []any{"group:team"}}, store.AnyRev)
	isErr(t, err, store.ErrDenied, "a writer takes it over")
	isErr(t, s.Delete(ctx, carol, "decks", "bobTeam", store.AnyRev), store.ErrDenied, "a writer removes it")
	// made only as one's own, in one's own tenant
	_, err = s.Put(ctx, alice, "decks", "new1", store.Doc{"tenant": "A", "owner": "bob"}, 0)
	isErr(t, err, store.ErrDenied, "made as Bob's")
	_, err = s.Put(ctx, alice, "decks", "new2", store.Doc{"tenant": "B", "owner": "alice"}, 0)
	isErr(t, err, store.ErrDenied, "made in another tenant")
	eq(t, must(s.Put(ctx, alice, "decks", "new3", store.Doc{"tenant": "A", "owner": "alice"}, 0)), store.Rev(1), "alice's own")
	_, err = s.Put(ctx, alice, "decks", "new3", store.Doc{"tenant": "B", "owner": "alice"}, store.AnyRev)
	isErr(t, err, store.ErrDenied, "moved to another tenant")
	_, err = s.Put(ctx, nobody, "decks", "new4", store.Doc{}, 0)
	isErr(t, err, store.ErrDenied, "no principal")
}

func guardTenant(t *testing.T, e store.Engine) {
	s := guarded(t, e)
	// an admin is an admin of their own tenant only
	eq(t, ids(must(s.Query(ctx, eve, store.Query{From: "decks"}))), []string{"eves"}, "B's admin")
	for _, id := range []string{"bobPrivate", "bobShared", "alices"} {
		_, _, err := s.Get(ctx, eve, "decks", id)
		isErr(t, err, store.ErrNotFound, "B reads "+id)
		_, err = s.Put(ctx, eve, "decks", id, store.Doc{"tenant": "B", "owner": "eve"}, store.AnyRev)
		isErr(t, err, store.ErrConflict, "B writes "+id)
	}
	_, _, err := s.Get(ctx, adminA, "decks", "eves")
	isErr(t, err, store.ErrNotFound, "A's admin reads B")
}
