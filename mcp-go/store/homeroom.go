// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"sync"
	"time"
)

// Home rooms (ADR 0001): every document has one home room. A document
// written without a room keeps the room it had, and a new one goes to its
// owner's home room, "My presentations", made the first time it is needed.
// The room exists before any document names it, so a stop between the two
// writes leaves no document in a room that is not there.
//
// A document moved into a room does not see the room's files unless it
// says so: inherit_room_files is false until someone sets it.

// HomeKind is the kind of an owner's home room; the UI names it in the
// user's language.
const HomeKind = "home"

const (
	RoomField    = "room"
	InheritField = "inherit_room_files"
)

var plainOwner = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// HomeRoomID is owner's home room: home-<owner> for a plain name, else
// home-<hash> so no owner name can make a bad or clashing id.
func HomeRoomID(owner string) string {
	if plainOwner.MatchString(owner) {
		return "home-" + owner
	}
	h := sha256.Sum256([]byte(owner))
	return "home-h" + hex.EncodeToString(h[:12])
}

// EnsureHomeRoom makes owner's home room in tenant and their owner
// membership, where they are not there yet. → the documents it made
func EnsureHomeRoom(ctx context.Context, e Engine, tenant, owner string, now time.Time) (int, error) {
	id := HomeRoomID(owner)
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
	room := Doc{"tenant": tenant, "title": "My presentations", "kind": HomeKind, "archived": false, "created": now.UTC(), "createdBy": owner}
	if _, _, err := e.Update(ctx, RoomsCol, id, create(room)); err != nil && !errors.Is(err, errExists) {
		return made, err
	}
	m := "user:" + owner
	mem := Doc{"tenant": tenant, "room": id, "member": m, "role": string(Owner)}
	if _, _, err := e.Update(ctx, MembersCol, memberID(id, m), create(mem)); err != nil && !errors.Is(err, errExists) {
		return made, err
	}
	return made, nil
}

var (
	errExists   = errors.New("store: exists")
	errNeedRoom = errors.New("store: home room needed")
)

// HomeRooms is an Engine whose documents in Cols always have a home room.
type HomeRooms struct {
	Engine
	Cols   map[string]bool
	Tenant string
	// the owner of a document with no "owner"
	Owner string
	Now   func() time.Time

	known sync.Map // home room id → made
}

func (h *HomeRooms) owner(d Doc) string {
	if o, ok := d["owner"].(string); ok && o != "" {
		return o
	}
	return h.Owner
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
		var need string
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
			owner := h.owner(next)
			room := HomeRoomID(owner)
			if _, ok := h.known.Load(room); !ok {
				need = owner
				return nil, errNeedRoom
			}
			next[RoomField] = room
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
		if _, err := EnsureHomeRoom(ctx, h.Engine, h.Tenant, need, h.now()); err != nil {
			return nil, 0, err
		}
		h.known.Store(HomeRoomID(need), true)
	}
}

// HomeAll puts every document of col that has no room into its owner's
// home room, and gives every one the tenant: what a folder written before
// rooms needs once. It is safe to
// stop and run again. → the documents it made (rooms and memberships)
func HomeAll(ctx context.Context, e Engine, col, tenant, defOwner string, now time.Time) (int, error) {
	items, err := e.Query(ctx, Query{From: col})
	if err != nil {
		return 0, err
	}
	h := &HomeRooms{Engine: e, Cols: map[string]bool{col: true}, Tenant: tenant, Owner: defOwner, Now: func() time.Time { return now }}
	made := 0
	for _, it := range items {
		if it.Doc[RoomField] != nil && it.Doc["tenant"] != nil {
			continue
		}
		if it.Doc[RoomField] == nil {
			owner := h.owner(it.Doc)
			n, err := EnsureHomeRoom(ctx, e, tenant, owner, now)
			made += n
			if err != nil {
				return made, err
			}
			h.known.Store(HomeRoomID(owner), true)
		}
		// the wrapper fills in what is missing
		_, _, err = h.Update(ctx, col, it.ID, func(cur Doc, _ Rev) (Doc, error) { return cur, nil })
		if err != nil {
			return made, err
		}
	}
	return made, nil
}
