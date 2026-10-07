// SPDX-License-Identifier: AGPL-3.0-or-later

// Package store is where Sliqtly keeps its documents, behind one contract
// that a folder (FileStore), memory (MemStore) and later SQLite or
// PostgreSQL all keep the same way:
//
//	Engine   the backend: documents by collection and id, each with a
//	         revision; Update(fn) is read-change-write as one step; Query
//	         takes a Query AST, not SQL text; Watch tells every write in order.
//	         An Engine does not ask who is asking: it is the privileged side.
//	Store    an Engine with a Policy in front: every call names the
//	         Principal it is made for, and what the policy does not let
//	         them read is not found, in Get, Query and Watch alike.
//
// The contract is storetest.Run: every backend passes the same tests, the
// authorization ones included, whatever it enforces natively.
package store

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Doc is a document as plain values: string, int64, float64, bool,
// time.Time, []any, map[string]any, nil.
type Doc = map[string]any

// Rev is a document's revision: 1 when written first, one more on each
// write. 0 is "no document".
type Rev int64

// AnyRev written as an expected revision: whatever is there.
const AnyRev Rev = -1

// Seq orders every write of an Engine: a Change's place in its feed.
type Seq uint64

// Change is one write, as Watch tells it. Doc nil: the document was
// removed; Old nil: it was made.
type Change struct {
	Seq Seq
	Col string
	ID  string
	Rev Rev
	Doc Doc
	Old Doc
}

// Item is a document with its id and revision, as Query returns it.
type Item struct {
	ID  string
	Rev Rev
	Doc Doc
}

// Capabilities say what a backend does natively. The semantics are the
// same whatever they say: a backend without NativeACL is guarded by the
// Store's Policy, and leaks nothing more than one with it.
type Capabilities struct {
	Transactions bool // Update(fn) is atomic against other processes too
	Watch        bool
	FullText     bool
	NativeACL    bool
	RowSecurity  bool
	Durable      bool // what was written outlives the process
}

var (
	// ErrNotFound: no such document, or one the principal may not read:
	// the two are not told apart, so a Get cannot tell what exists
	ErrNotFound = errors.New("store: not found")
	// ErrConflict: the document's revision was not the one expected
	ErrConflict = errors.New("store: revision conflict")
	// ErrDenied: the principal may read the document but not make this
	// change
	ErrDenied = errors.New("store: not allowed")
	// ErrTooOld: Watch was asked for changes the feed no longer has
	ErrTooOld = errors.New("store: changes no longer kept")
	// ErrClosed: the Engine was closed
	ErrClosed = errors.New("store: closed")
)

// UpdateFunc gets the document as it is (nil, 0: there is none) and
// returns what it is to be (nil: removed). An error leaves it as it was
// and is what Update returns. It runs while the document is held: it must
// not call the Engine.
type UpdateFunc func(cur Doc, rev Rev) (Doc, error)

// Engine is a backend. Its calls name no principal: it is the privileged
// interface, for the server's own work and for a Store to guard.
type Engine interface {
	// Get returns the document and its revision; nil, 0, nil when missing.
	Get(ctx context.Context, col, id string) (Doc, Rev, error)
	// Update reads the document, calls fn and writes what it returned, as
	// one step: no other write to the document comes between. It returns
	// what was written and its revision (nil, 0 when removed or left
	// absent). fn returning cur unchanged still writes.
	Update(ctx context.Context, col, id string, fn UpdateFunc) (Doc, Rev, error)
	// Query returns the documents of q.From that match q.
	Query(ctx context.Context, q Query) ([]Item, error)
	// Head is the Seq of the last write; Watch(ctx, Head()) tells what
	// comes after now.
	Head() Seq
	// Watch sends every change after `after`, in order, until ctx ends
	// (then the channel is closed). A watcher that does not read holds
	// its own changes, not the writers.
	Watch(ctx context.Context, after Seq) (<-chan Change, error)
	Caps() Capabilities
	Close() error
}

// Put writes doc when the document's revision is ifRev (0: there must be
// none; AnyRev: whatever there is). → its new revision
func Put(ctx context.Context, e Engine, col, id string, doc Doc, ifRev Rev) (Rev, error) {
	if doc == nil {
		doc = Doc{}
	}
	_, rev, err := e.Update(ctx, col, id, func(_ Doc, rev Rev) (Doc, error) {
		if ifRev != AnyRev && rev != ifRev {
			return nil, ErrConflict
		}
		return doc, nil
	})
	return rev, err
}

// Delete removes the document when its revision is ifRev (AnyRev:
// whatever it is). A missing document is not an error with AnyRev.
func Delete(ctx context.Context, e Engine, col, id string, ifRev Rev) error {
	_, _, err := e.Update(ctx, col, id, func(_ Doc, rev Rev) (Doc, error) {
		if ifRev != AnyRev && rev != ifRev {
			return nil, ErrConflict
		}
		return nil, nil
	})
	return err
}

// Clone copies a document deeply, so what a caller changes is not what the
// store holds.
func Clone(d Doc) Doc {
	if d == nil {
		return nil
	}
	return cloneValue(d).(map[string]any)
}

func cloneValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = cloneValue(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = cloneValue(e)
		}
		return out
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case float32:
		return float64(x)
	case time.Time:
		return time.UnixMilli(x.UnixMilli()).UTC()
	case string:
		// its own bytes: a string cut from a request body keeps the whole
		// body alive, and the change feed, which keeps clones of recent
		// documents, counts only the string's own length
		return strings.Clone(x)
	}
	return v
}
