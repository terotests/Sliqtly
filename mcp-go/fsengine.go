//go:build !nocloud

// SPDX-License-Identifier: AGPL-3.0-or-later

// Firestore as a store.Engine, for what the cloud keeps in the store's
// shape: the shared rooms, their memberships and the people of their chat
// (roomsapi.go, roomchat.go). Each document carries its revision in a
// field of its own (_rev), read and written in one transaction with it.
//
// A Query's equalities, one IN and one list-has go to Firestore; the rest
// of it (Or, Not, ranges, the order, the page) is the store's own Run over
// what came back, so the answer is the one every backend gives. Ids asked
// for by "_id" are read as documents. Firestore tells no changes here
// (Caps.Watch is false): the pages listen to Firestore themselves, under
// its rules.

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/terotests/sliqtly/mcp-go/store"
)

const (
	fsRevField = "_rev"
	// Firestore's most values in one IN
	fsInMax = 30
)

type fsEngine struct {
	c *firestore.Client
	// put before every collection's name (tests: one set of collections
	// each)
	prefix string
	closed atomic.Bool
}

func newFSEngine(c *firestore.Client, prefix string) *fsEngine {
	return &fsEngine{c: c, prefix: prefix}
}

// a name Firestore takes and a folder would too: the same rules as the
// store's other backends
func (e *fsEngine) check(col, id string) error {
	if e.closed.Load() {
		return store.ErrClosed
	}
	return fsName(col, id)
}

func fsName(col, id string) error {
	for _, n := range []string{col, id} {
		if n == "" || n == "." || n == ".." || strings.HasPrefix(n, ".") || strings.ContainsAny(n, `/\`) || (strings.HasPrefix(n, "__") && strings.HasSuffix(n, "__")) || len(n) > 700 {
			return fmt.Errorf("store: bad name %q/%q", col, id)
		}
	}
	return nil
}

func (e *fsEngine) col(name string) *firestore.CollectionRef { return e.c.Collection(e.prefix + name) }

// a snapshot as the store's document and revision
func fsDoc(snap *firestore.DocumentSnapshot) (store.Doc, store.Rev) {
	if snap == nil || !snap.Exists() {
		return nil, 0
	}
	d := snap.Data()
	rev, _ := d[fsRevField].(int64)
	delete(d, fsRevField)
	return store.Clone(d), store.Rev(rev)
}

func (e *fsEngine) Get(ctx context.Context, col, id string) (store.Doc, store.Rev, error) {
	if err := e.check(col, id); err != nil {
		return nil, 0, err
	}
	snap, err := e.col(col).Doc(id).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	d, rev := fsDoc(snap)
	return d, rev, nil
}

func (e *fsEngine) Update(ctx context.Context, col, id string, fn store.UpdateFunc) (store.Doc, store.Rev, error) {
	if err := e.check(col, id); err != nil {
		return nil, 0, err
	}
	ref := e.col(col).Doc(id)
	var out store.Doc
	var outRev store.Rev
	// fn runs again when another write came between: it gets the document
	// as it is then
	err := e.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		out, outRev = nil, 0
		snap, err := tx.Get(ref)
		if err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		cur, rev := fsDoc(snap)
		next, err := fn(cur, rev)
		if err != nil {
			return err
		}
		if next == nil {
			if cur == nil {
				return nil
			}
			return tx.Delete(ref)
		}
		w := store.Clone(next)
		w[fsRevField] = int64(rev) + 1
		if err := tx.Set(ref, w); err != nil {
			return err
		}
		out, outRev = store.Clone(next), rev+1
		return nil
	}, firestore.MaxAttempts(50))
	if err != nil {
		return nil, 0, err
	}
	return out, outRev, nil
}

func (e *fsEngine) Query(ctx context.Context, q store.Query) ([]store.Item, error) {
	if err := e.check(q.From, "x"); err != nil {
		return nil, err
	}
	if q.Where == store.False {
		return []store.Item{}, nil
	}
	conds := fsConds(q.Where)
	var items []store.Item
	var err error
	if ids, ok := fsIDs(conds); ok {
		items, err = e.byIDs(ctx, q.From, ids)
	} else {
		items, err = e.where(ctx, q.From, conds)
	}
	if err != nil {
		return nil, err
	}
	// the whole condition, the order and the page, as every backend does
	return store.Run(q, items), nil
}

// the comparisons a condition is an AND of (the rest stays for Run)
func fsConds(w store.Expr) []store.Cmp {
	switch x := w.(type) {
	case store.Cmp:
		return []store.Cmp{x}
	case store.And:
		var out []store.Cmp
		for _, s := range x {
			out = append(out, fsConds(s)...)
		}
		return out
	}
	return nil
}

// the ids the condition names ("_id" = or IN), when it names them
func fsIDs(conds []store.Cmp) ([]string, bool) {
	for _, c := range conds {
		if c.Field != "_id" {
			continue
		}
		switch c.Op {
		case store.OpEq:
			if s, ok := c.Value.(string); ok {
				return []string{s}, true
			}
		case store.OpIn:
			var out []string
			for _, v := range c.Value.([]any) {
				if s, ok := v.(string); ok {
					out = append(out, s)
				}
			}
			return out, true
		}
	}
	return nil, false
}

func (e *fsEngine) byIDs(ctx context.Context, col string, ids []string) ([]store.Item, error) {
	items := []store.Item{}
	var refs []*firestore.DocumentRef
	for _, id := range ids {
		if fsName(col, id) == nil {
			refs = append(refs, e.col(col).Doc(id))
		}
	}
	if len(refs) == 0 {
		return items, nil
	}
	snaps, err := e.c.GetAll(ctx, refs)
	if err != nil {
		return nil, err
	}
	for _, s := range snaps {
		if d, rev := fsDoc(s); d != nil {
			items = append(items, store.Item{ID: s.Ref.ID, Rev: rev, Doc: d})
		}
	}
	return items, nil
}

func (e *fsEngine) where(ctx context.Context, col string, conds []store.Cmp) ([]store.Item, error) {
	base := e.col(col).Query
	var in *store.Cmp
	has := false
	for i, c := range conds {
		if c.Field == "_id" {
			continue
		}
		switch c.Op {
		case store.OpEq:
			base = base.Where(c.Field, "==", c.Value)
		case store.OpHas:
			if !has {
				has = true
				base = base.Where(c.Field, "array-contains", c.Value)
			}
		case store.OpIn:
			if in == nil {
				in = &conds[i]
			}
		}
	}
	if in == nil {
		return fsRead(ctx, base)
	}
	vals, _ := in.Value.([]any)
	seen := map[string]bool{}
	items := []store.Item{}
	for start := 0; start < len(vals); start += fsInMax {
		part := vals[start:min(start+fsInMax, len(vals))]
		got, err := fsRead(ctx, base.Where(in.Field, "in", part))
		if err != nil {
			return nil, err
		}
		for _, it := range got {
			if !seen[it.ID] {
				seen[it.ID] = true
				items = append(items, it)
			}
		}
	}
	return items, nil
}

func fsRead(ctx context.Context, q firestore.Query) ([]store.Item, error) {
	it := q.Documents(ctx)
	defer it.Stop()
	items := []store.Item{}
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return items, nil
		}
		if err != nil {
			return nil, err
		}
		d, rev := fsDoc(snap)
		items = append(items, store.Item{ID: snap.Ref.ID, Rev: rev, Doc: d})
	}
}

func (e *fsEngine) Head() store.Seq { return 0 }

func (e *fsEngine) Watch(context.Context, store.Seq) (<-chan store.Change, error) {
	return nil, errors.New("store: Firestore tells no changes here; listen to it")
}

func (e *fsEngine) Caps() store.Capabilities {
	return store.Capabilities{Transactions: true, Durable: true}
}

// the client is the server's, closed with it: this only stops the engine
func (e *fsEngine) Close() error {
	e.closed.Store(true)
	return nil
}
