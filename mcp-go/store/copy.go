// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// The way from one backend to another (the folder to SQLite, SQLite to
// PostgreSQL) is Copy: every document with its revision, then Verify that
// both hold the same, before the server is started on the new one. The
// old one is left as it was, so going back is starting on it again.

// Lister is an Engine that can say which collections it has.
type Lister interface {
	Collections(ctx context.Context) ([]string, error)
}

// Importer is an Engine that takes a document as it was elsewhere,
// revision and all, without telling it as a change: for Copy only, into a
// backend nothing else uses yet.
type Importer interface {
	Import(ctx context.Context, col, id string, d Doc, rev Rev) error
}

// CopyReport is what Copy did: documents per collection.
type CopyReport map[string]int

// Copy puts every document of src into dst. dst must be empty: a document
// already there is an error, not overwritten.
func Copy(ctx context.Context, dst Importer, src Lister) (CopyReport, error) {
	e, ok := src.(Engine)
	if !ok {
		return nil, fmt.Errorf("store: %T is not an Engine", src)
	}
	cols, err := src.Collections(ctx)
	if err != nil {
		return nil, err
	}
	rep := CopyReport{}
	for _, col := range cols {
		items, err := e.Query(ctx, Query{From: col})
		if err != nil {
			return rep, fmt.Errorf("%s: %w", col, err)
		}
		for _, it := range items {
			if err := dst.Import(ctx, col, it.ID, it.Doc, it.Rev); err != nil {
				return rep, fmt.Errorf("%s/%s: %w", col, it.ID, err)
			}
			rep[col]++
		}
	}
	return rep, nil
}

// Digest sums an Engine's documents and revisions, collection by
// collection: two backends with the same digests hold the same.
func Digest(ctx context.Context, src Lister) (map[string]string, error) {
	e := src.(Engine)
	cols, err := src.Collections(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, col := range cols {
		items, err := e.Query(ctx, Query{From: col})
		if err != nil {
			return nil, err
		}
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		h := sha256.New()
		for _, it := range items {
			// json sorts map keys: the same document, the same bytes
			b, err := json.Marshal(EncodeValue(it.Doc))
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(h, "%s\x00%d\x00%s\n", it.ID, it.Rev, b)
		}
		out[col] = hex.EncodeToString(h.Sum(nil))
	}
	return out, nil
}

// Verify: dst holds what src does.
func Verify(ctx context.Context, dst, src Lister) error {
	a, err := Digest(ctx, src)
	if err != nil {
		return err
	}
	b, err := Digest(ctx, dst)
	if err != nil {
		return err
	}
	for col, sum := range a {
		if b[col] != sum {
			return fmt.Errorf("store: %s differs after the copy", col)
		}
	}
	for col := range b {
		if _, ok := a[col]; !ok {
			return fmt.Errorf("store: %s is only in the copy", col)
		}
	}
	return nil
}
