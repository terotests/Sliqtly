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

// RunChat tests the ChatLog that open makes; each test gets an empty one.
func RunChat(t *testing.T, open func(t *testing.T) store.ChatLog) {
	tests := []struct {
		name string
		fn   func(t *testing.T, c store.ChatLog)
	}{
		{"Order", chatOrder},
		{"Pages", chatPages},
		{"Threads", chatThreads},
		{"Change", chatChange},
		{"Reactions", chatReactions},
		{"RoomsApart", chatRoomsApart},
		{"Concurrent", chatConcurrent},
		{"FilesAndLinks", chatFilesAndLinks},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { tc.fn(t, open(t)) })
	}
}

func say(t *testing.T, c store.ChatLog, room, who, text, thread string) store.ChatMsg {
	t.Helper()
	m, err := c.Append(context.Background(), store.ChatMsg{Tenant: "t1", Room: room, Thread: thread, From: store.ChatFrom{ID: who, Name: who, Kind: "person"}, Text: text})
	if err != nil {
		t.Fatalf("append %q: %v", text, err)
	}
	return m
}

func texts(ms []store.ChatMsg) string {
	out := ""
	for i, m := range ms {
		if i > 0 {
			out += ","
		}
		out += m.Text
	}
	return out
}

func chatOrder(t *testing.T, c store.ChatLog) {
	ctx := context.Background()
	a := say(t, c, "r", "ann", "one", "")
	b := say(t, c, "r", "bob", "two", "")
	if a.Seq != 1 || b.Seq != 2 {
		t.Fatalf("seqs %d %d", a.Seq, b.Seq)
	}
	if a.ID == "" || a.ID == b.ID || a.At == 0 {
		t.Fatalf("ids %q %q at %d", a.ID, b.ID, a.At)
	}
	got, err := c.Get(ctx, "t1", "r", b.ID)
	if err != nil || got.Text != "two" || got.From.Name != "bob" || got.Seq != 2 {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := c.Get(ctx, "t1", "r", "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if n, _ := c.Last(ctx, "t1", "r"); n != 2 {
		t.Fatalf("last %d", n)
	}
	if n, _ := c.Last(ctx, "t1", "empty"); n != 0 {
		t.Fatalf("last of none %d", n)
	}
	// an id given is kept, once
	m, err := c.Append(ctx, store.ChatMsg{Tenant: "t1", Room: "r", ID: "mine", From: store.ChatFrom{ID: "ann"}, Text: "x"})
	if err != nil || m.ID != "mine" {
		t.Fatalf("own id: %+v %v", m, err)
	}
	if _, err := c.Append(ctx, store.ChatMsg{Tenant: "t1", Room: "r", ID: "mine", From: store.ChatFrom{ID: "ann"}, Text: "y"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("same id twice: %v", err)
	}
	if _, err := c.Append(ctx, store.ChatMsg{Tenant: "t1", Room: "r", Text: "nobody"}); err == nil {
		t.Fatal("a message without a writer was kept")
	}
}

func chatPages(t *testing.T, c store.ChatLog) {
	ctx := context.Background()
	for i := 1; i <= 12; i++ {
		say(t, c, "r", "ann", fmt.Sprint(i), "")
	}
	last, _ := c.Page(ctx, "t1", "r", store.ChatPage{Limit: 5})
	if texts(last) != "8,9,10,11,12" {
		t.Fatalf("newest five: %s", texts(last))
	}
	before, _ := c.Page(ctx, "t1", "r", store.ChatPage{Before: last[0].Seq, Limit: 5})
	if texts(before) != "3,4,5,6,7" {
		t.Fatalf("the five before: %s", texts(before))
	}
	after, _ := c.Page(ctx, "t1", "r", store.ChatPage{After: 3, Limit: 2})
	if texts(after) != "4,5" {
		t.Fatalf("the two after 3: %s", texts(after))
	}
	all, _ := c.Page(ctx, "t1", "r", store.ChatPage{})
	if len(all) != 12 {
		t.Fatalf("default page: %d", len(all))
	}
}

func chatThreads(t *testing.T, c store.ChatLog) {
	ctx := context.Background()
	root := say(t, c, "r", "ann", "root", "")
	say(t, c, "r", "bob", "r1", root.ID)
	say(t, c, "r", "cid", "r2", root.ID)
	r3 := say(t, c, "r", "bob", "r3", root.ID)
	say(t, c, "r", "ann", "next", "")
	top, _ := c.Page(ctx, "t1", "r", store.ChatPage{})
	if texts(top) != "root,next" {
		t.Fatalf("top level: %s", texts(top))
	}
	if top[0].Replies != 3 || top[0].LastReply != r3.At {
		t.Fatalf("root counts: %+v", top[0])
	}
	if fmt.Sprint(top[0].Repliers) != "[cid bob]" {
		t.Fatalf("repliers, newest last, once each: %v", top[0].Repliers)
	}
	th, _ := c.Page(ctx, "t1", "r", store.ChatPage{Thread: root.ID})
	if texts(th) != "r1,r2,r3" || th[0].Thread != root.ID {
		t.Fatalf("thread: %s", texts(th))
	}
	// a reply to a reply, or to nothing, is refused
	if _, err := c.Append(ctx, store.ChatMsg{Tenant: "t1", Room: "r", Thread: r3.ID, From: store.ChatFrom{ID: "ann"}, Text: "deep"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reply to a reply: %v", err)
	}
	if _, err := c.Append(ctx, store.ChatMsg{Tenant: "t1", Room: "r", Thread: "gone", From: store.ChatFrom{ID: "ann"}, Text: "x"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reply to nothing: %v", err)
	}
	// one room's root is not another's
	if _, err := c.Append(ctx, store.ChatMsg{Tenant: "t1", Room: "other", Thread: root.ID, From: store.ChatFrom{ID: "ann"}, Text: "x"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reply across rooms: %v", err)
	}
}

func chatChange(t *testing.T, c store.ChatLog) {
	ctx := context.Background()
	m := say(t, c, "r", "ann", "hello", "")
	got, err := c.Change(ctx, "t1", "r", m.ID, func(x *store.ChatMsg) error {
		x.Text = "hello there"
		x.Edited = 5
		x.Seq = 99 // not the change's to make
		x.ID = "other"
		return nil
	})
	if err != nil || got.Text != "hello there" || got.Seq != m.Seq || got.ID != m.ID || got.Edited != 5 {
		t.Fatalf("changed: %+v %v", got, err)
	}
	again, _ := c.Get(ctx, "t1", "r", m.ID)
	if again.Text != "hello there" {
		t.Fatalf("kept: %+v", again)
	}
	boom := errors.New("no")
	if _, err := c.Change(ctx, "t1", "r", m.ID, func(x *store.ChatMsg) error { x.Text = "lost"; return boom }); !errors.Is(err, boom) {
		t.Fatalf("fn's error: %v", err)
	}
	if still, _ := c.Get(ctx, "t1", "r", m.ID); still.Text != "hello there" {
		t.Fatalf("an error left it changed: %q", still.Text)
	}
	if _, err := c.Change(ctx, "t1", "r", "nope", func(*store.ChatMsg) error { return nil }); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func chatReactions(t *testing.T, c store.ChatLog) {
	ctx := context.Background()
	m := say(t, c, "r", "ann", "nice", "")
	react := func(emoji, who string) store.ChatMsg {
		got, err := c.Change(ctx, "t1", "r", m.ID, func(x *store.ChatMsg) error {
			store.ToggleReaction(x, emoji, who)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	react("👍", "bob")
	react("👍", "cid")
	got := react("🎉", "bob")
	if fmt.Sprint(got.Reactions["👍"]) != "[bob cid]" || len(got.Reactions["🎉"]) != 1 {
		t.Fatalf("reactions: %v", got.Reactions)
	}
	got = react("👍", "bob")
	if fmt.Sprint(got.Reactions["👍"]) != "[cid]" {
		t.Fatalf("taken back: %v", got.Reactions)
	}
	react("👍", "cid")
	got = react("🎉", "bob")
	if got.Reactions != nil {
		t.Fatalf("none left: %v", got.Reactions)
	}
}

func chatRoomsApart(t *testing.T, c store.ChatLog) {
	ctx := context.Background()
	say(t, c, "a", "ann", "in a", "")
	b := say(t, c, "b", "ann", "in b", "")
	if b.Seq != 1 {
		t.Fatalf("each room counts its own: %d", b.Seq)
	}
	other, err := c.Append(ctx, store.ChatMsg{Tenant: "t2", Room: "a", From: store.ChatFrom{ID: "x"}, Text: "t2"})
	if err != nil || other.Seq != 1 {
		t.Fatalf("another tenant's room of the same id: %+v %v", other, err)
	}
	if ms, _ := c.Page(ctx, "t1", "a", store.ChatPage{}); texts(ms) != "in a" {
		t.Fatalf("room a: %s", texts(ms))
	}
	if _, err := c.Get(ctx, "t2", "b", b.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("read across tenants: %v", err)
	}
}

func chatConcurrent(t *testing.T, c store.ChatLog) {
	ctx := context.Background()
	root := say(t, c, "r", "ann", "root", "")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			thread := ""
			if i%2 == 0 {
				thread = root.ID
			}
			if _, err := c.Append(ctx, store.ChatMsg{Tenant: "t1", Room: "r", Thread: thread, From: store.ChatFrom{ID: fmt.Sprint("p", i)}, Text: fmt.Sprint(i)}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	all := map[int64]bool{}
	for _, th := range []string{"", root.ID} {
		ms, _ := c.Page(ctx, "t1", "r", store.ChatPage{Thread: th, Limit: 500})
		for _, m := range ms {
			if all[m.Seq] {
				t.Fatalf("seq %d twice", m.Seq)
			}
			all[m.Seq] = true
		}
	}
	if len(all) != 21 {
		t.Fatalf("%d messages kept of 21", len(all))
	}
	r, _ := c.Get(ctx, "t1", "r", root.ID)
	if r.Replies != 10 {
		t.Fatalf("replies counted under load: %d", r.Replies)
	}
}

// a message's files and its links' previews are kept with it, and a change
// keeps them or sets them
func chatFilesAndLinks(t *testing.T, c store.ChatLog) {
	ctx := context.Background()
	m, err := c.Append(ctx, store.ChatMsg{Tenant: "t1", Room: "r", From: store.ChatFrom{ID: "ann"}, Text: "see https://example.com",
		Files: []store.ChatFile{{Name: "plan.png", Type: "image/png", Size: 120, W: 640, H: 480}, {Name: "notes.txt", Type: "text/plain", Size: 9}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(ctx, "t1", "r", m.ID)
	if err != nil || len(got.Files) != 2 || got.Files[0] != m.Files[0] || got.Files[1].Name != "notes.txt" {
		t.Fatalf("files: %+v %v", got.Files, err)
	}
	ch, err := c.Change(ctx, "t1", "r", m.ID, func(x *store.ChatMsg) error {
		x.Links = []store.ChatLink{{URL: "https://example.com", Site: "Example", Title: "Example Domain", Desc: "For examples."}}
		return nil
	})
	if err != nil || len(ch.Links) != 1 || len(ch.Files) != 2 {
		t.Fatalf("change: %+v %v", ch, err)
	}
	page, err := c.Page(ctx, "t1", "r", store.ChatPage{})
	if err != nil || len(page) != 1 || page[0].Links[0].Title != "Example Domain" || page[0].Files[0].W != 640 {
		t.Fatalf("page: %+v %v", page, err)
	}
}
