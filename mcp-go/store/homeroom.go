// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// Home rooms (ADR 0001): every document has one home room. A document
// written without a room keeps the room it had, and a new one goes to the
// tenant's General room. A tenant starts with two rooms, General and
// Playground, made the first time they are needed; presentations are moved
// from General into rooms of their own as they get one. The room exists
// before any document names it, so a stop between the two writes leaves no
// document in a room that is not there.
//
// A document moved into a room does not see the room's files unless it
// says so: inherit_room_files is false until someone sets it.

const (
	RoomField    = "room"
	InheritField = "inherit_room_files"
)

// The rooms every tenant starts with. Their ids are their kinds; the UI
// names them in the user's language.
const (
	GeneralRoom    = "general"
	PlaygroundRoom = "playground"
)

var starterRooms = []struct{ id, title string }{
	{GeneralRoom, "General"},
	{PlaygroundRoom, "Playground"},
}

// StarterRooms makes General and Playground in tenant, owned by owner,
// where they are not there yet. → the documents it made
func StarterRooms(ctx context.Context, e Engine, tenant, owner string, now time.Time) (int, error) {
	made := 0
	create := func(d Doc) UpdateFunc {
		return func(cur Doc, _ Rev) (Doc, error) {
			if cur != nil {
				return cur, errExists
			}
			made++
			return d, nil
		}
	}
	for _, r := range starterRooms {
		room := Doc{"tenant": tenant, "title": r.title, "kind": r.id, "archived": false, "created": now.UTC(), "createdBy": owner}
		if _, _, err := e.Update(ctx, RoomsCol, r.id, create(room)); err != nil && !errors.Is(err, errExists) {
			return made, err
		}
		m := "user:" + owner
		mem := Doc{"tenant": tenant, "room": r.id, "member": m, "role": string(Owner)}
		if _, _, err := e.Update(ctx, MembersCol, memberID(r.id, m), create(mem)); err != nil && !errors.Is(err, errExists) {
			return made, err
		}
	}
	return made, nil
}

var (
	errExists   = errors.New("store: exists")
	errNeedRoom = errors.New("store: starter rooms needed")
)

// HomeRooms is an Engine whose documents in Cols always have a home room.
type HomeRooms struct {
	Engine
	Cols   map[string]bool
	Tenant string
	// the owner of the starter rooms
	Owner string
	Now   func() time.Time

	made atomic.Bool // the starter rooms are there
}

func (h *HomeRooms) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *HomeRooms) Update(ctx context.Context, col, id string, fn UpdateFunc) (Doc, Rev, error) {
	if !h.Cols[col] {
		return h.Engine.Update(ctx, col, id, fn)
	}
	for {
		d, rev, err := h.Engine.Update(ctx, col, id, func(cur Doc, rev Rev) (Doc, error) {
			next, err := fn(cur, rev)
			if err != nil || next == nil {
				return next, err
			}
			// the tenant rides on the document, as a row's would in SQL
			if next["tenant"] == nil {
				next["tenant"] = h.Tenant
			}
			if next[RoomField] != nil {
				return next, nil
			}
			if cur != nil && cur[RoomField] != nil {
				// a writer that replaces the document keeps its room
				next[RoomField] = cur[RoomField]
				if v, ok := cur[InheritField]; ok {
					if _, set := next[InheritField]; !set {
						next[InheritField] = v
					}
				}
				return next, nil
			}
			if !h.made.Load() {
				return nil, errNeedRoom
			}
			next[RoomField] = GeneralRoom
			if _, set := next[InheritField]; !set {
				next[InheritField] = false
			}
			return next, nil
		})
		if !errors.Is(err, errNeedRoom) {
			return d, rev, err
		}
		// made outside the document's write: an engine may hold one lock
		// for both
		if _, err := StarterRooms(ctx, h.Engine, h.Tenant, h.Owner, h.now()); err != nil {
			return nil, 0, err
		}
		h.made.Store(true)
	}
}

// HomeAll makes the starter rooms and puts every document of col that has
// no room into General, giving every one the tenant: what a folder written
// before rooms needs once. It is safe to stop and run again. → the
// documents it made (rooms and memberships)
func HomeAll(ctx context.Context, e Engine, col, tenant, owner string, now time.Time) (int, error) {
	made, err := StarterRooms(ctx, e, tenant, owner, now)
	if err != nil {
		return made, err
	}
	items, err := e.Query(ctx, Query{From: col})
	if err != nil {
		return made, err
	}
	h := &HomeRooms{Engine: e, Cols: map[string]bool{col: true}, Tenant: tenant, Owner: owner, Now: func() time.Time { return now }}
	h.made.Store(true)
	for _, it := range items {
		if it.Doc[RoomField] != nil && it.Doc["tenant"] != nil {
			continue
		}
		// the wrapper fills in what is missing
		if _, _, err := h.Update(ctx, col, it.ID, func(cur Doc, _ Rev) (Doc, error) { return cur, nil }); err != nil {
			return made, err
		}
	}
	return made, nil
}
