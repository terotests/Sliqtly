// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Links join things the system knows: a room to another (parent, child,
// references, inherits_files), a deck to a deck, a room to a Jira issue. A link is a
// document of LinksCol whose id is made of what it joins (LinkID), so one
// link between two things exists once, in a folder as in SQL (where the id
// is the primary key and from/rel/to also a unique index), and adding it
// again is no error and no second link. A link never grants access.

// LinksCol is the collection the links are in.
const LinksCol = "links"

// Ref names one thing: kind and id, written "kind:id" ("deck:aB3xY9pQ2k",
// "room:Xk2…", "file:aB3xY9pQ2k/media/cat.png", "jira:ABC-123"). A room's
// or a deck's id is the system's own, never an external key, so it stays
// the same when the Jira issue it refers to is renamed or moved.
type Ref struct {
	Kind string
	ID   string
}

var (
	kindRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	relRe  = kindRe
)

func (r Ref) String() string { return r.Kind + ":" + r.ID }

func (r Ref) Valid() error {
	if !kindRe.MatchString(r.Kind) {
		return fmt.Errorf("store: bad kind %q", r.Kind)
	}
	if r.ID == "" || len(r.ID) > 400 || strings.ContainsAny(r.ID, "\x00\n\r\t ") {
		return fmt.Errorf("store: bad id %q", r.ID)
	}
	return nil
}

// ParseRef reads "kind:id".
func ParseRef(s string) (Ref, error) {
	k, id, ok := strings.Cut(s, ":")
	if !ok {
		return Ref{}, fmt.Errorf("store: %q is not kind:id", s)
	}
	r := Ref{k, id}
	return r, r.Valid()
}

// Link is from —rel→ to.
type Link struct {
	From Ref
	Rel  string
	To   Ref
}

func (l Link) Valid() error {
	if err := l.From.Valid(); err != nil {
		return err
	}
	if err := l.To.Valid(); err != nil {
		return err
	}
	if !relRe.MatchString(l.Rel) {
		return fmt.Errorf("store: bad relation %q", l.Rel)
	}
	if l.From == l.To {
		return errors.New("store: a link to itself")
	}
	return nil
}

// LinkID is the document id of a link as kept (LinkTypes.Canonical):
// the same for the same link, whoever adds it.
func LinkID(l Link) string {
	h := sha256.Sum256([]byte(l.From.String() + "\x00" + l.Rel + "\x00" + l.To.String()))
	return "l" + hex.EncodeToString(h[:16])
}

// Resolver says where a ref's document is, so a link is shown only to whom
// may read both its ends. ok false: a kind the store does not hold (one
// told of by its own service); such an end is not checked here.
type Resolver func(r Ref) (col, id string, ok bool)

// Links adds and reads links for a principal. Who may see a link is
// decided by its ends, not by who made it: anyone in the tenant who may
// read both ends sees it, and nobody else learns it exists. So the link
// documents are kept through the Store's Engine, and every call checks
// the ends through the Store.
type Links struct {
	S       *Store
	Types   *LinkTypes // nil: DefaultLinkTypes
	Resolve Resolver
	Now     func() time.Time
}

func (ls Links) types() *LinkTypes {
	if ls.Types == nil {
		return DefaultLinkTypes()
	}
	return ls.Types
}

// ID is the id the link is kept by, as given or as its inverse.
func (ls Links) ID(l Link) (string, error) {
	c, err := ls.types().Canonical(l)
	if err != nil {
		return "", err
	}
	return LinkID(c), nil
}

// the end is readable by p, or not one the store holds
func (ls Links) canSee(ctx context.Context, p Principal, r Ref) bool {
	col, id, ok := ls.Resolve(r)
	if !ok {
		return true
	}
	_, _, err := ls.S.Get(ctx, p, col, id)
	return err == nil
}

func (ls Links) visible(ctx context.Context, p Principal, d Doc) (Link, bool) {
	from, err1 := ParseRef(fmt.Sprint(d["from"]))
	to, err2 := ParseRef(fmt.Sprint(d["to"]))
	rel, _ := d["rel"].(string)
	if err1 != nil || err2 != nil || p.TenantID == "" || d["tenant"] != p.TenantID {
		return Link{}, false
	}
	return Link{from, rel, to}, ls.canSee(ctx, p, from) && ls.canSee(ctx, p, to)
}

// Add makes the link for p, who must be able to read both ends. → its
// id, and whether it was made now (false: it was there already).
func (ls Links) Add(ctx context.Context, p Principal, l Link, extra Doc) (string, bool, error) {
	if err := l.Valid(); err != nil {
		return "", false, err
	}
	if p.UserID == "" || p.TenantID == "" {
		return "", false, ErrDenied
	}
	l, err := ls.types().Canonical(l)
	if err != nil {
		return "", false, err
	}
	if !ls.canSee(ctx, p, l.From) || !ls.canSee(ctx, p, l.To) {
		return "", false, ErrNotFound
	}
	d := Doc{}
	for k, v := range extra {
		d[k] = v
	}
	d["from"], d["rel"], d["to"] = l.From.String(), l.Rel, l.To.String()
	d["tenant"], d["owner"] = p.TenantID, p.UserID
	now := time.Now
	if ls.Now != nil {
		now = ls.Now
	}
	d["created"] = now().UTC()
	id := LinkID(l)
	made := false
	_, _, err = ls.S.Privileged().Update(ctx, LinksCol, id, func(cur Doc, _ Rev) (Doc, error) {
		if cur != nil {
			if cur["tenant"] != p.TenantID {
				// the same ends in another tenant: refs are ids, so this
				// is not a clash that can happen; refused, not shared
				return nil, ErrConflict
			}
			return cur, errLinkThere
		}
		made = true
		return d, nil
	})
	if errors.Is(err, errLinkThere) {
		return id, false, nil
	}
	return id, made && err == nil, err
}

var errLinkThere = errors.New("store: link there")

// Remove takes the link away: by whoever made it, or the tenant's admin,
// when they may still read both ends. No error when there is none (or
// none p may see).
func (ls Links) Remove(ctx context.Context, p Principal, l Link) error {
	e := ls.S.Privileged()
	id, err := ls.ID(l)
	if err != nil {
		return err
	}
	d, rev, err := e.Get(ctx, LinksCol, id)
	if err != nil || d == nil {
		return err
	}
	if _, ok := ls.visible(ctx, p, d); !ok {
		return nil
	}
	if d["owner"] != p.UserID && !p.HasRole("admin") {
		return ErrDenied
	}
	return Delete(ctx, e, LinksCol, id, rev)
}

// Of returns the links at r that p may see, each read from r ("r child
// of X" for a link kept as "X parent of r").
func (ls Links) Of(ctx context.Context, p Principal, r Ref) ([]Link, error) {
	if p.TenantID == "" || !ls.canSee(ctx, p, r) {
		return nil, nil
	}
	items, err := ls.S.Privileged().Query(ctx, Query{From: LinksCol,
		Where:   And{Eq("tenant", p.TenantID), Or{Eq("from", r.String()), Eq("to", r.String())}},
		OrderBy: []Order{{Field: "created"}}})
	if err != nil {
		return nil, err
	}
	var out []Link
	for _, it := range items {
		if l, ok := ls.visible(ctx, p, it.Doc); ok {
			out = append(out, ls.types().From(l, r))
		}
	}
	return out, nil
}
