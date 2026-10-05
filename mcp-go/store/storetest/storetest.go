// SPDX-License-Identifier: AGPL-3.0-or-later

// Package storetest is the store contract as tests: every backend runs
// Run and passes all of it, so code written against one backend means the
// same against the others.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// Run tests the Engine that open makes; each test gets a new, empty one.
func Run(t *testing.T, open func(t *testing.T) store.Engine) {
	tests := []struct {
		name string
		fn   func(t *testing.T, e store.Engine)
	}{
		{"GetMissing", getMissing},
		{"PutRevisions", putRevisions},
		{"Values", values},
		{"Copies", copies},
		{"UpdateAtomic", updateAtomic},
		{"UpdateError", updateError},
		{"Delete", deleteDoc},
		{"BadNames", badNames},
		{"Query", query},
		{"QueryOrder", queryOrder},
		{"Watch", watch},
		{"WatchReplay", watchReplay},
		{"WatchSlow", watchSlow},
		{"Close", closeEngine},
		{"GuardGet", guardGet},
		{"GuardQuery", guardQuery},
		{"GuardWatch", guardWatch},
		{"GuardWrite", guardWrite},
		{"GuardTenant", guardTenant},
		{"Links", links},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := open(t)
			defer e.Close()
			tc.fn(t, e)
		})
	}
}

var ctx = context.Background()

// must is v, or a failed test (a panic, which `go test` reports with
// where it came from)
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func eq(t *testing.T, got, want any, what ...any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v, want %#v", fmt.Sprint(what...), got, want)
	}
}

func isErr(t *testing.T, err, want error, what string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got error %v, want %v", what, err, want)
	}
}

func getMissing(t *testing.T, e store.Engine) {
	d, rev, err := e.Get(ctx, "decks", "nope")
	eq(t, []any{d == nil, rev, err}, []any{true, store.Rev(0), nil})
}

func putRevisions(t *testing.T, e store.Engine) {
	eq(t, must(store.Put(ctx, e, "decks", "a", store.Doc{"n": int64(1)}, 0)), store.Rev(1), "new")
	_, err := store.Put(ctx, e, "decks", "a", store.Doc{"n": int64(2)}, 0)
	isErr(t, err, store.ErrConflict, "made twice")
	eq(t, must(store.Put(ctx, e, "decks", "a", store.Doc{"n": int64(2)}, 1)), store.Rev(2), "on rev 1")
	_, err = store.Put(ctx, e, "decks", "a", store.Doc{"n": int64(3)}, 1)
	isErr(t, err, store.ErrConflict, "on an old rev")
	eq(t, must(store.Put(ctx, e, "decks", "a", store.Doc{"n": int64(4)}, store.AnyRev)), store.Rev(3), "any")
	d, rev, _ := e.Get(ctx, "decks", "a")
	eq(t, []any{d, rev}, []any{store.Doc{"n": int64(4)}, store.Rev(3)})
}

func values(t *testing.T, e store.Engine) {
	at := time.Date(2026, 10, 5, 12, 30, 1, 234_000_000, time.UTC)
	in := store.Doc{
		"s": "teksti ✓", "i": int64(-42), "big": int64(1) << 53, "f": 1.5, "b": true, "t": at, "nil": nil,
		"map":  map[string]any{"x": map[string]any{"y": int64(1)}},
		"list": []any{"a", int64(2), 2.5, false, map[string]any{"k": "v"}},
	}
	must(store.Put(ctx, e, "decks", "v", in, 0))
	d, _, _ := e.Get(ctx, "decks", "v")
	eq(t, d, in, "read back")
}

func copies(t *testing.T, e store.Engine) {
	in := store.Doc{"m": map[string]any{"x": "1"}, "l": []any{"a"}}
	must(store.Put(ctx, e, "decks", "c", in, 0))
	in["m"].(map[string]any)["x"] = "changed"
	d, _, _ := e.Get(ctx, "decks", "c")
	d["l"].([]any)[0] = "changed"
	d2, _, _ := e.Get(ctx, "decks", "c")
	eq(t, d2, store.Doc{"m": map[string]any{"x": "1"}, "l": []any{"a"}})
}

func updateAtomic(t *testing.T, e store.Engine) {
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				_, _, err := e.Update(ctx, "counters", "n", func(cur store.Doc, _ store.Rev) (store.Doc, error) {
					if cur == nil {
						cur = store.Doc{}
					}
					n, _ := cur["n"].(int64)
					cur["n"] = n + 1
					return cur, nil
				})
				if err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	d, rev, _ := e.Get(ctx, "counters", "n")
	eq(t, []any{d["n"], rev}, []any{int64(400), store.Rev(400)})
}

func updateError(t *testing.T, e store.Engine) {
	must(store.Put(ctx, e, "decks", "u", store.Doc{"v": "kept"}, 0))
	boom := errors.New("boom")
	_, _, err := e.Update(ctx, "decks", "u", func(cur store.Doc, _ store.Rev) (store.Doc, error) {
		cur["v"] = "lost"
		return nil, boom
	})
	isErr(t, err, boom, "fn's error")
	d, rev, _ := e.Get(ctx, "decks", "u")
	eq(t, []any{d["v"], rev}, []any{"kept", store.Rev(1)})
	// left absent: nothing written, nothing told
	head := e.Head()
	d, rev, err = e.Update(ctx, "decks", "none", func(cur store.Doc, _ store.Rev) (store.Doc, error) { return nil, nil })
	eq(t, []any{d == nil, rev, err, e.Head()}, []any{true, store.Rev(0), nil, head})
}

func deleteDoc(t *testing.T, e store.Engine) {
	must(store.Put(ctx, e, "decks", "d", store.Doc{}, 0))
	isErr(t, store.Delete(ctx, e, "decks", "d", 2), store.ErrConflict, "old rev")
	must(0, store.Delete(ctx, e, "decks", "d", 1))
	d, rev, _ := e.Get(ctx, "decks", "d")
	eq(t, []any{d == nil, rev}, []any{true, store.Rev(0)})
	must(0, store.Delete(ctx, e, "decks", "d", store.AnyRev))
	eq(t, must(store.Put(ctx, e, "decks", "d", store.Doc{}, 0)), store.Rev(1), "made again")
}

func badNames(t *testing.T, e store.Engine) {
	for _, n := range [][2]string{{"decks", ""}, {"decks", ".."}, {"decks", "a/b"}, {"decks", `a\b`}, {"", "x"}, {"../db", "x"}, {"decks", ".hidden"}} {
		if _, err := store.Put(ctx, e, n[0], n[1], store.Doc{}, store.AnyRev); err == nil {
			t.Fatalf("%q/%q was taken", n[0], n[1])
		}
	}
}

func seed(t *testing.T, e store.Engine) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	docs := map[string]store.Doc{
		"t1": {"kind": "ticket", "owner": "alice", "n": int64(3), "updated": at.Add(3 * time.Hour), "tags": []any{"x", "y"}, "meta": map[string]any{"epic": "E1"}},
		"t2": {"kind": "ticket", "owner": "bob", "n": 1.5, "updated": at.Add(1 * time.Hour), "tags": []any{"y"}},
		"t3": {"kind": "ticket", "owner": "alice", "n": int64(2), "updated": at.Add(2 * time.Hour)},
		"e1": {"kind": "epic", "owner": "alice", "n": int64(10)},
		"f1": {"kind": "folder", "owner": "carol"},
	}
	for id, d := range docs {
		must(store.Put(ctx, e, "containers", id, d, 0))
	}
}

func ids(items []store.Item) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func query(t *testing.T, e store.Engine) {
	seed(t, e)
	q := func(w store.Expr) []string {
		t.Helper()
		return ids(must(e.Query(ctx, store.Query{From: "containers", Where: w})))
	}
	eq(t, q(nil), []string{"e1", "f1", "t1", "t2", "t3"}, "all, by id")
	eq(t, q(store.And{store.Eq("kind", "ticket"), store.Eq("owner", "alice")}), []string{"t1", "t3"}, "and")
	eq(t, q(store.Or{store.Eq("kind", "epic"), store.Eq("owner", "carol")}), []string{"e1", "f1"}, "or")
	eq(t, q(store.Not{X: store.Eq("kind", "ticket")}), []string{"e1", "f1"}, "not")
	eq(t, q(store.Ne("owner", "alice")), []string{"f1", "t2"}, "ne")
	eq(t, q(store.Lt("n", int64(3))), []string{"t2", "t3"}, "numbers of either kind")
	eq(t, q(store.Ge("n", 2.0)), []string{"e1", "t1", "t3"}, "float against int")
	eq(t, q(store.Ne("n", int64(10))), []string{"t1", "t2", "t3"}, "a missing field matches nothing")
	eq(t, q(store.In("owner", "bob", "carol")), []string{"f1", "t2"}, "in")
	eq(t, q(store.Has("tags", "y")), []string{"t1", "t2"}, "list has")
	eq(t, q(store.Eq("meta.epic", "E1")), []string{"t1"}, "nested field")
	eq(t, q(store.Eq("_id", "t2")), []string{"t2"}, "the id")
	eq(t, q(store.Eq("n", "3")), []string{}, "a string is not a number")
	eq(t, q(store.False), []string{}, "false")
	_, err := e.Query(ctx, store.Query{From: "../x"})
	if err == nil {
		t.Fatal("a bad collection was read")
	}
	eq(t, ids(must(e.Query(ctx, store.Query{From: "empty"}))), []string{}, "a collection never written")
}

func queryOrder(t *testing.T, e store.Engine) {
	seed(t, e)
	q := store.Query{From: "containers", Where: store.Eq("kind", "ticket"), OrderBy: []store.Order{{Field: "updated", Desc: true}}}
	eq(t, ids(must(e.Query(ctx, q))), []string{"t1", "t3", "t2"}, "by time, newest first")
	q.Limit, q.Offset = 1, 1
	eq(t, ids(must(e.Query(ctx, q))), []string{"t3"}, "a page")
	q = store.Query{From: "containers", OrderBy: []store.Order{{Field: "owner"}, {Field: "n", Desc: true}}}
	eq(t, ids(must(e.Query(ctx, q))), []string{"e1", "t1", "t3", "t2", "f1"}, "two keys")
	q = store.Query{From: "containers", OrderBy: []store.Order{{Field: "updated"}}}
	eq(t, ids(must(e.Query(ctx, q))), []string{"t2", "t3", "t1", "e1", "f1"}, "missing last")
	items := must(e.Query(ctx, store.Query{From: "containers", Where: store.Eq("_id", "t1")}))
	eq(t, []any{items[0].Rev, items[0].Doc["owner"]}, []any{store.Rev(1), "alice"}, "item")
}

func next(t *testing.T, ch <-chan store.Change) store.Change {
	t.Helper()
	select {
	case c, ok := <-ch:
		if !ok {
			t.Fatal("the watch ended")
		}
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("no change came")
	}
	return store.Change{}
}

func quiet(t *testing.T, ch <-chan store.Change) {
	t.Helper()
	select {
	case c := <-ch:
		t.Fatalf("told %+v", c)
	case <-time.After(50 * time.Millisecond):
	}
}

func watch(t *testing.T, e store.Engine) {
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	ch := must(e.Watch(wctx, e.Head()))
	must(store.Put(ctx, e, "decks", "w", store.Doc{"v": "1"}, 0))
	must(store.Put(ctx, e, "decks", "w", store.Doc{"v": "2"}, 1))
	must(0, store.Delete(ctx, e, "decks", "w", store.AnyRev))
	a, b, c := next(t, ch), next(t, ch), next(t, ch)
	eq(t, []any{a.Col, a.ID, a.Rev, a.Doc, a.Old == nil}, []any{"decks", "w", store.Rev(1), store.Doc{"v": "1"}, true}, "made")
	eq(t, []any{b.Rev, b.Doc, b.Old}, []any{store.Rev(2), store.Doc{"v": "2"}, store.Doc{"v": "1"}}, "changed")
	eq(t, []any{c.Rev, c.Doc == nil, c.Old}, []any{store.Rev(0), true, store.Doc{"v": "2"}}, "removed")
	if !(a.Seq < b.Seq && b.Seq < c.Seq && c.Seq == e.Head()) {
		t.Fatalf("seqs %d %d %d, head %d", a.Seq, b.Seq, c.Seq, e.Head())
	}
	stop()
	for range ch {
	}
}

func watchReplay(t *testing.T, e store.Engine) {
	start := e.Head()
	for i := 0; i < 5; i++ {
		must(store.Put(ctx, e, "decks", fmt.Sprint("r", i), store.Doc{}, 0))
	}
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	ch := must(e.Watch(wctx, start+2))
	for i := 2; i < 5; i++ {
		eq(t, next(t, ch).ID, fmt.Sprint("r", i), "replayed")
	}
	must(store.Put(ctx, e, "decks", "r5", store.Doc{}, 0))
	eq(t, next(t, ch).ID, "r5", "then live")
}

// a watcher that does not read does not hold the writers, and loses
// nothing
func watchSlow(t *testing.T, e store.Engine) {
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	slow := must(e.Watch(wctx, e.Head()))
	done := make(chan struct{})
	go func() {
		for i := 0; i < 300; i++ {
			must(store.Put(ctx, e, "decks", "s", store.Doc{"i": int64(i)}, store.AnyRev))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the writer waited for the watcher")
	}
	for i := 0; i < 300; i++ {
		eq(t, next(t, slow).Doc["i"], int64(i))
	}
}

func closeEngine(t *testing.T, e store.Engine) {
	ch := must(e.Watch(ctx, e.Head()))
	e.Close()
	_, _, err := e.Get(ctx, "decks", "x")
	isErr(t, err, store.ErrClosed, "get")
	_, err = store.Put(ctx, e, "decks", "x", store.Doc{}, store.AnyRev)
	isErr(t, err, store.ErrClosed, "put")
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("a change after close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not end")
	}
}
