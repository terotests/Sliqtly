// SPDX-License-Identifier: AGPL-3.0-or-later

package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// RunForms tests the Forms that open makes; each test gets an empty one.
func RunForms(t *testing.T, open func(t *testing.T) store.Forms) {
	tests := []struct {
		name string
		fn   func(t *testing.T, f store.Forms)
	}{
		{"Links", formLinks},
		{"SubmitTally", formSubmitTally},
		{"Limit", formLimit},
		{"OnceLink", formOnceLink},
		{"Remove", formRemove},
		{"DecksApart", formDecksApart},
		{"Key", formKey},
		{"Concurrent", formConcurrent},
		{"BadInput", formBadInput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { tc.fn(t, open(t)) })
	}
}

func link(t *testing.T, f store.Forms, hash, deck, file string, once bool) {
	t.Helper()
	if err := f.AddLink(context.Background(), store.FormLink{Hash: hash, ID: hash[:4], Deck: deck, File: file, Kind: "token", Once: once, Created: int64(len(hash))}); err != nil {
		t.Fatalf("add link %s: %v", hash, err)
	}
}

func answer(deck, file string, deltas string) store.FormResponse {
	return store.FormResponse{Deck: deck, File: file, Version: "v1", Record: `{"a":"x"}`, Deltas: deltas}
}

func tallyOf(t *testing.T, f store.Forms, deck, file string) map[string]int {
	t.Helper()
	rows, err := f.Tally(context.Background(), deck, file)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, r := range rows {
		out[r.Q+"/"+r.Key] = r.N
	}
	return out
}

func formLinks(t *testing.T, f store.Forms) {
	ctx := context.Background()
	link(t, f, "aaaa1111", "d1", "forms/a.form.md", false)
	link(t, f, "bbbb22222", "d1", "forms/b.form.md", true)
	if err := f.AddLink(ctx, store.FormLink{Hash: "aaaa1111", ID: "aaaa", Deck: "d2", File: "x", Kind: "token"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("the same hash twice: %v", err)
	}
	l, err := f.Link(ctx, "bbbb22222")
	if err != nil || l.Deck != "d1" || l.File != "forms/b.form.md" || !l.Once || l.ID != "bbbb" {
		t.Fatalf("link back: %+v %v", l, err)
	}
	if _, err := f.Link(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("no such link: %v", err)
	}
	all, _ := f.Links(ctx, "d1", "")
	one, _ := f.Links(ctx, "d1", "forms/a.form.md")
	if len(all) != 2 || len(one) != 1 || all[0].ID != "aaaa" {
		t.Fatalf("links of a deck: %+v / %+v", all, one)
	}
	if err := f.SetRevoked(ctx, "d1", "aaaa", true); err != nil {
		t.Fatal(err)
	}
	if l, _ := f.Link(ctx, "aaaa1111"); !l.Revoked {
		t.Fatal("revoked link reads revoked")
	}
	if err := f.SetRevoked(ctx, "d2", "aaaa", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another deck's link is not revoked by this one: %v", err)
	}
}

func formSubmitTally(t *testing.T, f store.Forms) {
	ctx := context.Background()
	r, err := f.Submit(ctx, answer("d1", "f", `[["","",1],["q","",1],["q","yes",1]]`), 0, "")
	if err != nil || r.ID == "" || r.At == 0 {
		t.Fatalf("submit: %+v %v", r, err)
	}
	f.Submit(ctx, answer("d1", "f", `[["","",1],["q","",1],["q","no",1]]`), 0, "")
	f.Submit(ctx, answer("d1", "f", `[["","",1]]`), 0, "")
	got := tallyOf(t, f, "d1", "f")
	if got["/"] != 3 || got["q/"] != 2 || got["q/yes"] != 1 || got["q/no"] != 1 || len(got) != 4 {
		t.Fatalf("tally: %v", got)
	}
	list, _ := f.Responses(ctx, "d1", "f")
	if len(list) != 3 || list[0].ID != r.ID || list[0].Record != `{"a":"x"}` || list[0].Version != "v1" {
		t.Fatalf("responses: %+v", list)
	}
}

func formLimit(t *testing.T, f store.Forms) {
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := f.Submit(ctx, answer("d1", "f", `[["","",1]]`), 2, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.Submit(ctx, answer("d1", "f", `[["","",1]]`), 2, ""); !errors.Is(err, store.ErrFormFull) {
		t.Fatalf("third of two: %v", err)
	}
	if got := tallyOf(t, f, "d1", "f"); got["/"] != 2 {
		t.Fatalf("a refused response counts nothing: %v", got)
	}
}

func formOnceLink(t *testing.T, f store.Forms) {
	ctx := context.Background()
	link(t, f, "once0001", "d1", "f", true)
	link(t, f, "many0001", "d1", "f", false)
	if _, err := f.Submit(ctx, answer("d1", "f", `[["","",1]]`), 0, "once0001"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Submit(ctx, answer("d1", "f", `[["","",1]]`), 0, "once0001"); !errors.Is(err, store.ErrLinkUsed) {
		t.Fatalf("a used one-response link: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := f.Submit(ctx, answer("d1", "f", `[["","",1]]`), 0, "many0001"); err != nil {
			t.Fatal(err)
		}
	}
	if l, _ := f.Link(ctx, "once0001"); !l.Used {
		t.Fatal("the link reads used")
	}
	if got := tallyOf(t, f, "d1", "f"); got["/"] != 3 {
		t.Fatalf("tally: %v", got)
	}
}

func formRemove(t *testing.T, f store.Forms) {
	ctx := context.Background()
	a, _ := f.Submit(ctx, answer("d1", "f", `[["","",1],["q","yes",1]]`), 0, "")
	f.Submit(ctx, answer("d1", "f", `[["","",1],["q","yes",1]]`), 0, "")
	if err := f.Remove(ctx, "d1", "f", a.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove(ctx, "d1", "f", a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("removed twice: %v", err)
	}
	got := tallyOf(t, f, "d1", "f")
	if got["/"] != 1 || got["q/yes"] != 1 {
		t.Fatalf("tally after removal: %v", got)
	}
	list, _ := f.Responses(ctx, "d1", "f")
	if len(list) != 1 {
		t.Fatalf("responses after removal: %d", len(list))
	}
}

func formDecksApart(t *testing.T, f store.Forms) {
	ctx := context.Background()
	link(t, f, "d1link01", "d1", "f", false)
	link(t, f, "d2link01", "d2", "f", false)
	f.Submit(ctx, answer("d1", "f", `[["","",1]]`), 0, "")
	f.Submit(ctx, answer("d1", "g", `[["","",1]]`), 0, "")
	f.Submit(ctx, answer("d2", "f", `[["","",1]]`), 0, "")
	if got := tallyOf(t, f, "d1", "f"); got["/"] != 1 {
		t.Fatalf("one file's tally: %v", got)
	}
	if err := f.DropDeck(ctx, "d1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Link(ctx, "d1link01"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a dropped deck's link: %v", err)
	}
	if got := tallyOf(t, f, "d1", "g"); len(got) != 0 {
		t.Fatalf("a dropped deck's tally: %v", got)
	}
	if list, _ := f.Responses(ctx, "d1", "f"); len(list) != 0 {
		t.Fatal("a dropped deck's responses")
	}
	if _, err := f.Link(ctx, "d2link01"); err != nil {
		t.Fatalf("another deck stays: %v", err)
	}
	if got := tallyOf(t, f, "d2", "f"); got["/"] != 1 {
		t.Fatalf("another deck's tally stays: %v", got)
	}
}

func formKey(t *testing.T, f store.Forms) {
	ctx := context.Background()
	a, err := f.Key(ctx)
	if err != nil || len(a) != 32 {
		t.Fatalf("key: %d %v", len(a), err)
	}
	b, _ := f.Key(ctx)
	if string(a) != string(b) {
		t.Fatal("the key stays the same")
	}
}

func formConcurrent(t *testing.T, f store.Forms) {
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.Submit(ctx, answer("d1", "f", fmt.Sprintf(`[["","",1],["q","k%d",1]]`, i%3)), 25, "")
			if err != nil && !errors.Is(err, store.ErrFormFull) {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	got := tallyOf(t, f, "d1", "f")
	if got["/"] != 25 || got["q/k0"]+got["q/k1"]+got["q/k2"] != 25 {
		t.Fatalf("25 of 40 taken, counted once each: %v", got)
	}
}

func formBadInput(t *testing.T, f store.Forms) {
	ctx := context.Background()
	if _, err := f.Submit(ctx, answer("", "f", `[]`), 0, ""); err == nil {
		t.Fatal("a response without its deck")
	}
	if _, err := f.Submit(ctx, store.FormResponse{Deck: "d", File: "f", Record: "{", Deltas: "[]"}, 0, ""); err == nil {
		t.Fatal("a record that is not JSON")
	}
	if _, err := f.Submit(ctx, answer("d", "f", `[["q",1]]`), 0, ""); err == nil {
		t.Fatal("deltas that are not [q, key, n]")
	}
	if _, err := f.Submit(ctx, answer("d", "f", `[]`), 0, "nosuchlink"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a link that is not there: %v", err)
	}
	if got := tallyOf(t, f, "d", "f"); len(got) != 0 {
		t.Fatalf("nothing counted: %v", got)
	}
}
