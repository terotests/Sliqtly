// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"slices"
)

// Entries are linked both ways: a thing and its repair, a gift idea and the
// reminder to buy it, a registration and the gear it needs. Each keeps the
// other's id in its "links"; save_event and import_events add them (links)
// and take them off (unlink), delete_event takes the deleted one off.

const wardenMaxLinks = 50

// the ids a links or unlink argument names: an event's id, or the ext_id of
// one (also one of the same import, in batch)
func (s *wardenService) linkIDs(ctx context.Context, sp *wardenSpace, a map[string]any, k string, batch map[string]bool) ([]string, error) {
	v, ok := a[k]
	if !ok || v == nil {
		return nil, nil
	}
	l, ok := v.([]any)
	if !ok {
		return nil, roomErr{k + " is a list of entries' ids"}
	}
	out := []string{}
	for _, x := range l {
		ref, _ := x.(string)
		if ref == "" {
			return nil, roomErr{k + " is a list of entries' ids"}
		}
		id := ""
		for _, c := range []string{ref, wardenExtID(sp.id, ref)} {
			if !wardenIDRe.MatchString(c) {
				continue
			}
			if batch[c] {
				id = c
				break
			}
			d, err := s.db.Get(ctx, wardenEvents, c)
			if err != nil {
				return nil, err
			}
			if d != nil && docStr(d, "space") == sp.id {
				id = c
				break
			}
		}
		if id == "" {
			return nil, roomErr{fmt.Sprintf("%s: no entry %q in this space", k, ref)}
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// the entry's own links with add put on and drop taken off
func wardenSetLinks(d Doc, self string, add, drop []string) {
	l := docStrs(d, "links")
	for _, id := range add {
		if id != self && !slices.Contains(l, id) && len(l) < wardenMaxLinks {
			l = append(l, id)
		}
	}
	l = slices.DeleteFunc(l, func(id string) bool { return slices.Contains(drop, id) })
	if len(l) == 0 {
		delete(d, "links")
		return
	}
	d["links"] = anys(l)
}

// the other ends: id put into the links of add and taken out of drop's
func (s *wardenService) linkOthers(ctx context.Context, sp *wardenSpace, id string, add, drop []string) error {
	for _, other := range append(slices.Clone(add), drop...) {
		if other == id {
			continue
		}
		o, err := s.db.Get(ctx, wardenEvents, other)
		if err != nil {
			return err
		}
		if o == nil || docStr(o, "space") != sp.id {
			continue
		}
		before := len(docStrs(o, "links"))
		if slices.Contains(add, other) {
			wardenSetLinks(o, other, []string{id}, nil)
		} else {
			wardenSetLinks(o, other, nil, []string{id})
		}
		if len(docStrs(o, "links")) == before {
			continue
		}
		s.stamp(o, sp)
		if err := s.db.Set(ctx, wardenEvents, other, o); err != nil {
			return err
		}
	}
	return nil
}
