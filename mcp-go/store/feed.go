// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"sync"
)

// feed numbers an Engine's writes and hands them to its watchers: the
// last `keep` are kept for a watcher that comes asking from a little while
// back. Each watcher has its own queue, so a slow one holds only its own
// changes; the writer never waits for it.
type feed struct {
	mu     sync.Mutex
	head   Seq
	recent []Change // the last ones, oldest first
	keep   int
	subs   map[*watcher]struct{}
	closed bool
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
	f.recent = append(f.recent, c)
	if len(f.recent) > f.keep {
		f.recent = append(f.recent[:0:0], f.recent[len(f.recent)-f.keep:]...)
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
		first := f.head - Seq(len(f.recent)) + 1
		if after+1 < first {
			f.mu.Unlock()
			return nil, ErrTooOld
		}
		w.queue = append(w.queue, f.recent[after+1-first:]...)
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
