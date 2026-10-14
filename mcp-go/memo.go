// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	clist "container/list"
	"crypto/sha256"
	"sync"
)

// Answers worth keeping that depend only on what they were asked: where a
// deck's slides start in its Markdown (Check.slideStarts) and its slides'
// titles (Tools.reviewTitles). Each is a whole layout of the deck. Many
// agents on one deck asked them of the same text again and again (every
// begin_work, add_comment and merge laid the deck out anew), which with ten
// agents on a deck was a third of the server's time (cmd/agentsim). Kept
// under the hash of the question, the newest few hundred, in this process.
// What read something that may read otherwise next time (h.unsure) is not
// kept.
type memo struct {
	mu    sync.Mutex
	max   int
	order *clist.List // front: the newest
	items map[[32]byte]*clist.Element
}

type memoItem struct {
	key [32]byte
	val string
}

const memoMax = 512

func (m *memo) get(key [32]byte) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.items[key]
	if e == nil {
		return "", false
	}
	m.order.MoveToFront(e)
	return e.Value.(*memoItem).val, true
}

func (m *memo) put(key [32]byte, val string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.items == nil {
		m.items = map[[32]byte]*clist.Element{}
		m.order = clist.New()
		if m.max == 0 {
			m.max = memoMax
		}
	}
	if e := m.items[key]; e != nil {
		e.Value.(*memoItem).val = val
		m.order.MoveToFront(e)
		return
	}
	m.items[key] = m.order.PushFront(&memoItem{key, val})
	for m.order.Len() > m.max {
		last := m.order.Back()
		m.order.Remove(last)
		delete(m.items, last.Value.(*memoItem).key)
	}
}

func memoKey(kind, key string) [32]byte {
	h := sha256.New()
	h.Write([]byte(kind))
	h.Write([]byte{0})
	h.Write([]byte(key))
	var k [32]byte
	h.Sum(k[:0])
	return k
}

// MemoGet: what was kept for kind and key, "" when nothing is (so what
// is kept is never "": the Ranger side puts a mark before it).
func (h *McpHost) MemoGet(kind, key string) string {
	if h.env == nil {
		return ""
	}
	v, _ := h.env.memo.get(memoKey(kind, key))
	return v
}

// MemoPut keeps val; not when this request read something that may read
// otherwise next time.
func (h *McpHost) MemoPut(kind, key, val string) {
	if h.env == nil || h.unsure || h.err != nil {
		return
	}
	h.env.memo.put(memoKey(kind, key), val)
}
