// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"fmt"
	"sort"
	"sync"
)

// LinkType is a kind of link: its name, the name it has read from the
// other end (Inverse: parent ↔ child), or none at all (Symmetric:
// relates_to). Label is what a person reads.
type LinkType struct {
	Name      string
	Inverse   string
	Symmetric bool
	Label     string
}

// LinkTypes is the list of link kinds a tenant uses: a few built in,
// more added by configuration. A link is kept in one direction only (a
// pair's Name, a symmetric one from its smaller end), so "A child of B"
// and "B parent of A" are one link with one id.
type LinkTypes struct {
	mu      sync.RWMutex
	byName  map[string]LinkType // both names of a pair
	primary map[string]string   // name → the name a link is kept by
}

// DefaultLinkTypes: relates_to, parent/child, references and
// inherits_files (ADR 0001).
func DefaultLinkTypes() *LinkTypes {
	lt := &LinkTypes{byName: map[string]LinkType{}, primary: map[string]string{}}
	for _, t := range []LinkType{
		{Name: "relates_to", Symmetric: true, Label: "liittyy"},
		{Name: "parent", Inverse: "child", Label: "yläpuolella"},
		{Name: "references", Inverse: "referenced_by", Label: "viittaa"},
		{Name: "inherits_files", Inverse: "files_used_by", Label: "käyttää tiedostoja"},
	} {
		if err := lt.Add(t); err != nil {
			panic(err)
		}
	}
	return lt
}

// Add configures one more kind. A name already in use is refused.
func (lt *LinkTypes) Add(t LinkType) error {
	if !relRe.MatchString(t.Name) || (t.Inverse != "" && !relRe.MatchString(t.Inverse)) {
		return fmt.Errorf("store: bad link type %q/%q", t.Name, t.Inverse)
	}
	if t.Symmetric && t.Inverse != "" {
		return fmt.Errorf("store: %s is symmetric and has an inverse", t.Name)
	}
	if t.Inverse == t.Name {
		return fmt.Errorf("store: %s is its own inverse: make it symmetric", t.Name)
	}
	lt.mu.Lock()
	defer lt.mu.Unlock()
	for _, n := range []string{t.Name, t.Inverse} {
		if _, ok := lt.byName[n]; ok && n != "" {
			return fmt.Errorf("store: link type %q exists", n)
		}
	}
	lt.byName[t.Name] = t
	lt.primary[t.Name] = t.Name
	if t.Inverse != "" {
		lt.byName[t.Inverse] = LinkType{Name: t.Inverse, Inverse: t.Name, Label: t.Label}
		lt.primary[t.Inverse] = t.Name
	}
	return nil
}

// Get is the kind named n, read from that end.
func (lt *LinkTypes) Get(n string) (LinkType, bool) {
	lt.mu.RLock()
	defer lt.mu.RUnlock()
	t, ok := lt.byName[n]
	return t, ok
}

// Names lists every name a link can be given, sorted.
func (lt *LinkTypes) Names() []string {
	lt.mu.RLock()
	defer lt.mu.RUnlock()
	out := make([]string, 0, len(lt.byName))
	for n := range lt.byName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Canonical is the link as it is kept: a pair's inverse turned round to
// its primary name, a symmetric one from its smaller end. A kind not
// configured is refused.
func (lt *LinkTypes) Canonical(l Link) (Link, error) {
	if err := l.Valid(); err != nil {
		return Link{}, err
	}
	lt.mu.RLock()
	t, ok := lt.byName[l.Rel]
	primary := lt.primary[l.Rel]
	lt.mu.RUnlock()
	if !ok {
		return Link{}, fmt.Errorf("store: no link type %q", l.Rel)
	}
	switch {
	case t.Symmetric:
		if l.To.String() < l.From.String() {
			l.From, l.To = l.To, l.From
		}
	case primary != l.Rel:
		l = Link{From: l.To, Rel: primary, To: l.From}
	}
	return l, nil
}

// From is the link read from end r: "B parent of A" read at A is "A child
// of B". A link not at r comes back as it is.
func (lt *LinkTypes) From(l Link, r Ref) Link {
	if l.From == r || l.To != r {
		return l
	}
	t, ok := lt.Get(l.Rel)
	switch {
	case ok && t.Symmetric:
		return Link{From: r, Rel: l.Rel, To: l.From}
	case ok && t.Inverse != "":
		return Link{From: r, Rel: t.Inverse, To: l.From}
	}
	return l
}
