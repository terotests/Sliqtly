// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
)

// Principal is who a call is made for: Sliqtly's own user id (never an
// e-mail address or a sign-in provider's subject, which are attributes of
// the user), the tenant, and the groups and roles they have there.
type Principal struct {
	UserID   string
	TenantID string
	Groups   []string
	Roles    []string
}

func (p Principal) HasRole(r string) bool {
	for _, x := range p.Roles {
		if x == r {
			return true
		}
	}
	return false
}

func (p Principal) InGroup(g string) bool {
	for _, x := range p.Groups {
		if x == g {
			return true
		}
	}
	return false
}

// Policy says what a principal may read and change. The same policy holds
// whatever the backend: a backend with row security (PostgreSQL) enforces
// it a second time, it does not replace it.
type Policy interface {
	// Scope is the condition on a collection's documents that the
	// principal may read: ANDed into every query, so a SQL backend
	// filters in the database. False: none of it.
	Scope(p Principal, col string) Expr
	// CanRead is the same decision for one document; asked again of every
	// document a backend returns, so a scope that a backend compiled
	// wrong still leaks nothing.
	CanRead(p Principal, col, id string, d Doc) bool
	// CanWrite: may p change the document from old to next (old nil: it
	// is made; next nil: it is removed).
	CanWrite(p Principal, col, id string, old, next Doc) bool
}

// Store is an Engine guarded by a Policy: every call names its Principal.
// What p may not read is ErrNotFound, as a missing document is; it does
// not come back from Query or Watch either.
type Store struct {
	e   Engine
	pol Policy
}

func New(e Engine, pol Policy) *Store { return &Store{e: e, pol: pol} }

// Privileged is the Engine itself, unguarded: for the server's own work
// (migrations, expiry, the folder server's single user), named so that a
// call without a principal is never made by accident.
func (s *Store) Privileged() Engine { return s.e }

func (s *Store) Caps() Capabilities { return s.e.Caps() }

func (s *Store) Get(ctx context.Context, p Principal, col, id string) (Doc, Rev, error) {
	d, rev, err := s.e.Get(ctx, col, id)
	if err != nil {
		return nil, 0, err
	}
	if d == nil || !s.pol.CanRead(p, col, id, d) {
		return nil, 0, ErrNotFound
	}
	return d, rev, nil
}

// Update is Engine.Update for p: fn sees the document only when p may
// read it (otherwise it does not exist, for p), and what fn returns is
// written only when p may make that change.
func (s *Store) Update(ctx context.Context, p Principal, col, id string, fn UpdateFunc) (Doc, Rev, error) {
	d, rev, err := s.e.Update(ctx, col, id, func(cur Doc, rev Rev) (Doc, error) {
		if cur != nil && !s.pol.CanRead(p, col, id, cur) {
			// for p it is not there: fn is told so. Leaving it absent is
			// left as it is; making it is refused as an id already taken
			// (ids are random, so that tells nothing)
			next, err := fn(nil, 0)
			if err != nil {
				return nil, err
			}
			if next == nil {
				return nil, errHidden
			}
			return nil, ErrConflict
		}
		next, err := fn(Clone(cur), rev)
		if err != nil {
			return nil, err
		}
		if next == nil && cur == nil {
			return nil, nil
		}
		if !s.pol.CanWrite(p, col, id, cur, next) {
			return nil, ErrDenied
		}
		return next, nil
	})
	if err == errHidden {
		return nil, 0, nil
	}
	return d, rev, err
}

// errHidden ends an Update of a document p may not read, as one of a
// missing document ends
var errHidden = errors.New("store: hidden")

// Put writes doc for p when the revision is ifRev (0: new; AnyRev: any).
func (s *Store) Put(ctx context.Context, p Principal, col, id string, doc Doc, ifRev Rev) (Rev, error) {
	if doc == nil {
		doc = Doc{}
	}
	_, rev, err := s.Update(ctx, p, col, id, func(_ Doc, rev Rev) (Doc, error) {
		if ifRev != AnyRev && rev != ifRev {
			return nil, ErrConflict
		}
		return doc, nil
	})
	return rev, err
}

// Delete removes the document for p when its revision is ifRev.
func (s *Store) Delete(ctx context.Context, p Principal, col, id string, ifRev Rev) error {
	_, _, err := s.Update(ctx, p, col, id, func(cur Doc, rev Rev) (Doc, error) {
		if cur == nil && ifRev != AnyRev {
			return nil, ErrNotFound
		}
		if ifRev != AnyRev && rev != ifRev {
			return nil, ErrConflict
		}
		return nil, nil
	})
	return err
}

func (s *Store) Query(ctx context.Context, p Principal, q Query) ([]Item, error) {
	scope := s.pol.Scope(p, q.From)
	if scope == False {
		return nil, nil
	}
	// the page is taken after the policy has looked at each row: a
	// backend that filters right returns no row that is dropped here
	page := q
	page.Where = AndOf(scope, q.Where)
	items, err := s.e.Query(ctx, page)
	if err != nil {
		return nil, err
	}
	out := items[:0]
	for _, it := range items {
		if s.pol.CanRead(p, q.From, it.ID, it.Doc) {
			out = append(out, it)
		}
	}
	return out, nil
}

func (s *Store) Head() Seq { return s.e.Head() }

// Watch tells p the changes to what they may read: a document they could
// read and no longer can (removed, or its access changed) comes as a
// removal, with nothing of what it is now; one they could never read is not told at all.
func (s *Store) Watch(ctx context.Context, p Principal, after Seq) (<-chan Change, error) {
	in, err := s.e.Watch(ctx, after)
	if err != nil {
		return nil, err
	}
	out := make(chan Change)
	go func() {
		defer close(out)
		for c := range in {
			now := c.Doc != nil && s.pol.CanRead(p, c.Col, c.ID, c.Doc)
			was := c.Old != nil && s.pol.CanRead(p, c.Col, c.ID, c.Old)
			switch {
			case now:
				if !was {
					c.Old = nil
				}
			case was && c.Doc == nil:
				// removed: what it was, p could read
			case was:
				// out of p's reach now: told as removed, with nothing of
				// what it is now
				c = Change{Seq: c.Seq, Col: c.Col, ID: c.ID}
			default:
				continue
			}
			select {
			case out <- c:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
