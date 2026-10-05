// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"sort"
	"sync"
)

// MemStore keeps documents in memory: for tests, and the second backend
// that shows the contract is not the folder's.
type MemStore struct {
	mu     sync.Mutex
	cols   map[string]map[string]memDoc
	feed   *feed
	closed bool
}

type memDoc struct {
	doc Doc
	rev Rev
}

func NewMemStore() *MemStore {
	return &MemStore{cols: map[string]map[string]memDoc{}, feed: newFeed(4096)}
}

func (m *MemStore) Get(_ context.Context, col, id string) (Doc, Rev, error) {
	if err := checkName(col, id); err != nil {
		return nil, 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, 0, ErrClosed
	}
	d, ok := m.cols[col][id]
	if !ok {
		return nil, 0, nil
	}
	return Clone(d.doc), d.rev, nil
}

func (m *MemStore) Update(_ context.Context, col, id string, fn UpdateFunc) (Doc, Rev, error) {
	if err := checkName(col, id); err != nil {
		return nil, 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, 0, ErrClosed
	}
	cur, had := m.cols[col][id]
	next, err := fn(Clone(cur.doc), cur.rev)
	if err != nil {
		return nil, 0, err
	}
	if next == nil {
		if !had {
			return nil, 0, nil
		}
		delete(m.cols[col], id)
		m.feed.publish(Change{Col: col, ID: id, Old: Clone(cur.doc)})
		return nil, 0, nil
	}
	if m.cols[col] == nil {
		m.cols[col] = map[string]memDoc{}
	}
	d := memDoc{doc: Clone(next), rev: cur.rev + 1}
	m.cols[col][id] = d
	m.feed.publish(Change{Col: col, ID: id, Rev: d.rev, Doc: Clone(d.doc), Old: Clone(cur.doc)})
	return Clone(d.doc), d.rev, nil
}

func (m *MemStore) Query(_ context.Context, q Query) ([]Item, error) {
	if err := checkName(q.From, "x"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	items := make([]Item, 0, len(m.cols[q.From]))
	for id, d := range m.cols[q.From] {
		if Match(q.Where, id, d.doc) {
			items = append(items, Item{ID: id, Rev: d.rev, Doc: Clone(d.doc)})
		}
	}
	m.mu.Unlock()
	return Run(Query{OrderBy: q.OrderBy, Limit: q.Limit, Offset: q.Offset}, items), nil
}

func (m *MemStore) Head() Seq { return m.feed.Head() }

func (m *MemStore) Watch(ctx context.Context, after Seq) (<-chan Change, error) {
	return m.feed.watch(ctx, after)
}

func (m *MemStore) Caps() Capabilities {
	return Capabilities{Transactions: true, Watch: true}
}

func (m *MemStore) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.feed.close()
	return nil
}

func (m *MemStore) Collections(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for col, docs := range m.cols {
		if len(docs) > 0 {
			out = append(out, col)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *MemStore) Import(_ context.Context, col, id string, d Doc, rev Rev) error {
	if err := checkName(col, id); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cols[col][id]; ok {
		return ErrConflict
	}
	if m.cols[col] == nil {
		m.cols[col] = map[string]memDoc{}
	}
	m.cols[col][id] = memDoc{doc: Clone(d), rev: rev}
	return nil
}
