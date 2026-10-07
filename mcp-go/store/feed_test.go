package store

import (
	"context"
	"strings"
	"testing"
	"unsafe"
)

// large documents: the history keeps what fits in keepBytes, the newest
// always, and a watcher from inside it still gets every change after
func TestFeedKeepsBoundedHistory(t *testing.T) {
	f := newFeed(4096)
	big := strings.Repeat("x", 1<<20) // a 1 MB deck
	for i := 0; i < 200; i++ {
		f.publish(Change{Col: "shares", ID: "d", Doc: Doc{"md": big}, Old: Doc{"md": big}})
	}
	if f.bytes > keepBytes {
		t.Fatalf("kept %d bytes, more than %d", f.bytes, keepBytes)
	}
	kept := len(f.recent) - f.start
	if kept < 10 || kept > 20 {
		t.Fatalf("kept %d changes of 2 MB in 32 MB", kept)
	}
	if cap(f.recent) > 4*kept+8 {
		t.Fatalf("the history's array grew to %d for %d changes", cap(f.recent), kept)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := f.watch(ctx, f.head-3)
	if err != nil {
		t.Fatal(err)
	}
	for want := f.head - 2; want <= f.head; want++ {
		if c := <-ch; c.Seq != want {
			t.Fatalf("got %d, want %d", c.Seq, want)
		}
	}
	if _, err := f.watch(ctx, 1); err != ErrTooOld {
		t.Fatalf("from the start: %v", err)
	}
	// one change larger than the budget is still kept, alone
	huge := strings.Repeat("y", 40<<20)
	f.publish(Change{Col: "shares", ID: "d", Doc: Doc{"md": huge}})
	eq := len(f.recent) - f.start
	if eq != 1 {
		t.Fatalf("kept %d with a change over the budget", eq)
	}
}

// an id cut out of a long text is copied, so the kept change does not
// hold the whole text
func TestFeedOwnsItsIds(t *testing.T) {
	f := newFeed(10)
	text := strings.Repeat("x", 1<<20) + "deck1"
	id := text[len(text)-5:]
	f.publish(Change{Col: "shares", ID: id})
	kept := f.recent[len(f.recent)-1].ID
	if kept != "deck1" || unsafe.StringData(kept) == unsafe.StringData(id) {
		t.Fatalf("the feed keeps the caller's string")
	}
}
