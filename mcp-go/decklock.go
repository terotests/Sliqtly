// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "sync"

// One writer at a time per deck, in this process. A deck's review comments
// (review/comments.json) and its files list are read, changed and written
// back; two assistants doing that at once each wrote the deck as they had
// read it, and one's comment or file was gone (cmd/agentsim found comments
// lost with ten agents on one deck). The Markdown is also guarded by
// compare-and-set (Store.save's guard), which is what keeps it across
// instances; this lock keeps the rest within one server, which is all of a
// server of one's own.
//
// The Ranger side takes a deck with host_deck_lock and lets it go with
// host_deck_unlock; a request holds a deck at most once however often it
// takes it (Tools.call takes it for a comment, Store.save again), and the
// request's end lets go of what it still holds.
type deckLocks struct {
	mu    sync.Mutex
	decks map[string]*deckLock
}

type deckLock struct {
	mu    sync.Mutex
	users int // holding or waiting; the entry goes at 0
}

func (l *deckLocks) lock(id string) {
	l.mu.Lock()
	if l.decks == nil {
		l.decks = map[string]*deckLock{}
	}
	d := l.decks[id]
	if d == nil {
		d = &deckLock{}
		l.decks[id] = d
	}
	d.users++
	l.mu.Unlock()
	d.mu.Lock()
}

func (l *deckLocks) unlock(id string) {
	l.mu.Lock()
	d := l.decks[id]
	if d == nil {
		l.mu.Unlock()
		return
	}
	d.users--
	if d.users == 0 {
		delete(l.decks, id)
	}
	l.mu.Unlock()
	d.mu.Unlock()
}

// DeckLock takes the deck for this request (again: counted, not waited for).
func (h *McpHost) DeckLock(id string) {
	if h.env == nil || id == "" {
		return
	}
	if h.held[id] > 0 {
		h.held[id]++
		return
	}
	h.env.decks.lock(id)
	if h.held == nil {
		h.held = map[string]int{}
	}
	h.held[id] = 1
}

// DeckUnlock lets go of the deck once taking it is matched.
func (h *McpHost) DeckUnlock(id string) {
	if h.held[id] == 0 {
		return
	}
	h.held[id]--
	if h.held[id] == 0 {
		delete(h.held, id)
		h.env.decks.unlock(id)
	}
}

// at the end of a request, whatever it still holds
func (h *McpHost) unlockDecks() {
	for id := range h.held {
		delete(h.held, id)
		h.env.decks.unlock(id)
	}
}
