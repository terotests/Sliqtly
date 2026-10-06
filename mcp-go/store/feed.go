// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"sync"
)

// feed numbers an Engine's writes and hands them to its watchers: the
// last `keep` are kept for a watcher that comes asking from a little while
// back, as long as they hold no more than keepBytes of text between them
// (a change carries the whole document before and after it, and a deck is
// its whole Markdown). Each watcher has its own queue, so a slow one holds
// only its own changes; the writer never waits for it.
type feed struct {
	mu     sync.Mutex
	head   Seq
	recent []Change // the last ones, oldest first, from recent[start]
	start  int
	sizes  []int // each kept change's size, by the same index
	bytes  int   // the kept changes' sizes together
	keep   int
	subs   map[*watcher]struct{}
	closed bool
}

// the most text the kept changes hold between them
const keepBytes = 32 << 20

// about how much memory a change holds: its strings' lengths and a little
// for every value
func changeSize(c Change) int { return valueSize(c.Doc) + valueSize(c.Old) + 64 }

func valueSize(v any) int {
	switch x := v.(type) {
	case string:
		return len(x) + 16
	case Doc:
		n := 0
		for k, e := range x {
			n += len(k) + valueSize(e)
		}
		return n
	case []any:
		n := 0
		for _, e := range x {
			n += valueSize(e)
		}
		return n
	case []byte:
		return len(x)
	}
	return 16
}

type watcher struct {
	mu    sync.Mutex
	queue []Change
	wake  chan struct{}
}

func newFeed(keep int) *feed {
	return &feed{keep: keep, subs: map[*watcher]struct{}{}}
}

func (f *feed) Head() Seq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.head
}

// publish numbers c and tells it; called by the Engine in write order,
// while the write is still held, so the numbers follow the writes
func (f *feed) publish(c Change) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.head++
	c.Seq = f.head
	size := changeSize(c)
	f.recent = append(f.recent, c)
	f.sizes = append(f.sizes, size)
	f.bytes += size
	// the oldest go first, but the newest change is always kept
	for len(f.recent)-f.start > 1 && (len(f.recent)-f.start > f.keep || f.bytes > keepBytes) {
		f.bytes -= f.sizes[f.start]
		f.recent[f.start] = Change{}
		f.start++
	}
	// moved down once half is dropped: each change is copied about once
	if f.start > len(f.recent)/2 {
		f.recent = append(f.recent[:0:0], f.recent[f.start:]...)
		f.sizes = append(f.sizes[:0:0], f.sizes[f.start:]...)
		f.start = 0
	}
	for w := range f.subs {
		w.push(c)
	}
}

func (w *watcher) push(c Change) {
	w.mu.Lock()
	w.queue = append(w.queue, c)
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (f *feed) watch(ctx context.Context, after Seq) (<-chan Change, error) {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil, ErrClosed
	}
	if after > f.head {
		after = f.head
	}
	w := &watcher{wake: make(chan struct{}, 1)}
	if after < f.head {
		kept := f.recent[f.start:]
		first := f.head - Seq(len(kept)) + 1
		if after+1 < first {
			f.mu.Unlock()
			return nil, ErrTooOld
		}
		w.queue = append(w.queue, kept[after+1-first:]...)
		w.wake <- struct{}{}
	}
	f.subs[w] = struct{}{}
	f.mu.Unlock()
	out := make(chan Change)
	go func() {
		defer close(out)
		defer func() {
			f.mu.Lock()
			delete(f.subs, w)
			f.mu.Unlock()
		}()
		for {
			w.mu.Lock()
			q := w.queue
			w.queue = nil
			w.mu.Unlock()
			for _, c := range q {
				select {
				case out <- c:
				case <-ctx.Done():
					return
				}
			}
			// closed, and all it had told: the watch ends
			w.mu.Lock()
			empty := len(w.queue) == 0
			w.mu.Unlock()
			if empty && f.isClosed() {
				return
			}
			select {
			case <-w.wake:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (f *feed) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *feed) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	for w := range f.subs {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
}
