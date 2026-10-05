// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Rooms (ADR 0001): a room is Sliqtly's collaboration and authorization
// boundary. It is small: a room document, its memberships, and content
// documents that carry the room's id in their "room" field. Who may do
// what in a room is a membership; a room link never gives access.
//
//	rooms/<id>               tenant, title, kind, archived, created
//	room_members/<room>~<m>  tenant, room, member ("user:<id>" | "group:<id>"), role
//
// An archived room is read only for everyone, its owners too, until it
// is taken out of the archive; nothing in it is removed.

const (
	RoomsCol   = "rooms"
	MembersCol = "room_members"
)

// Role in a room, each with what the one before it has.
type Role string

const (
	NoRole Role = ""
	Viewer Role = "viewer"
	Editor Role = "editor"
	Owner  Role = "owner"
)

func (r Role) rank() int {
	switch r {
	case Viewer:
		return 1
	case Editor:
		return 2
	case Owner:
		return 3
	}
	return 0
}

// AtLeast: r has what w has.
func (r Role) AtLeast(w Role) bool { return r.rank() >= w.rank() && r != NoRole }

// Rooms makes and runs rooms for principals.
type Rooms struct {
	S   *Store
	Now func() time.Time
}

func (rs Rooms) now() time.Time {
	if rs.Now != nil {
		return rs.Now().UTC()
	}
	return time.Now().UTC()
}

func newID() string {
	b := make([]byte, 12)
	rand.Read(b)
	const abc = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	var sb strings.Builder
	for _, x := range b {
		sb.WriteByte(abc[int(x)%len(abc)])
	}
	return sb.String()
}

func memberID(room, member string) string {
	return room + "~" + strings.ReplaceAll(member, ":", "-")
}

// the member names p is known by
func memberNames(p Principal) []any {
	out := []any{"user:" + p.UserID}
	for _, g := range p.Groups {
		out = append(out, "group:"+g)
	}
	return out
}

// Access is p's role in each room of their tenant: the highest of their own
// and their groups' memberships; the tenant's admin is an owner of every
// room. A room in the archive gives no more than Viewer.
func (rs Rooms) Access(ctx context.Context, p Principal) (map[string]Role, error) {
	out := map[string]Role{}
	if p.UserID == "" || p.TenantID == "" {
		return out, nil
	}
	e := rs.S.Privileged()
	rooms, err := e.Query(ctx, Query{From: RoomsCol, Where: Eq("tenant", p.TenantID)})
	if err != nil {
		return nil, err
	}
	archived := map[string]bool{}
	for _, it := range rooms {
		archived[it.ID] = it.Doc["archived"] == true
		if p.HasRole("admin") {
			out[it.ID] = Owner
		}
	}
	ms, err := e.Query(ctx, Query{From: MembersCol, Where: And{Eq("tenant", p.TenantID), In("member", memberNames(p)...)}})
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		room, _ := m.Doc["room"].(string)
		role := Role(fmt.Sprint(m.Doc["role"]))
		if _, ok := archived[room]; !ok {
			continue // a membership of a room that is not there
		}
		if role.rank() > out[room].rank() {
			out[room] = role
		}
	}
	for id, r := range out {
		if archived[id] && r.rank() > Viewer.rank() {
			out[id] = Viewer
		}
	}
	return out, nil
}

// For is p with their rooms read: what every room-guarded call is made with.
func (rs Rooms) For(ctx context.Context, p Principal) (Principal, error) {
	acc, err := rs.Access(ctx, p)
	if err != nil {
		return p, err
	}
	p.Rooms = acc
	return p, nil
}

// Create makes a room in p's tenant with p as its owner. → its id
func (rs Rooms) Create(ctx context.Context, p Principal, title, kind string) (string, error) {
	if p.UserID == "" || p.TenantID == "" {
		return "", ErrDenied
	}
	e := rs.S.Privileged()
	id := newID()
	if _, err := Put(ctx, e, RoomsCol, id, Doc{"tenant": p.TenantID, "title": title, "kind": kind, "archived": false, "created": rs.now(), "createdBy": p.UserID}, 0); err != nil {
		return "", err
	}
	if err := rs.setMember(ctx, p.TenantID, id, "user:"+p.UserID, Owner); err != nil {
		return "", err
	}
	return id, nil
}

func (rs Rooms) setMember(ctx context.Context, tenant, room, member string, role Role) error {
	_, err := Put(ctx, rs.S.Privileged(), MembersCol, memberID(room, member), Doc{"tenant": tenant, "room": room, "member": member, "role": string(role)}, AnyRev)
	return err
}

// the room for p: ErrNotFound when p has no role in it
func (rs Rooms) room(ctx context.Context, p Principal, id string) (Doc, Role, error) {
	acc, err := rs.Access(ctx, p)
	if err != nil {
		return nil, NoRole, err
	}
	role := acc[id]
	if role == NoRole {
		return nil, NoRole, ErrNotFound
	}
	d, _, err := rs.S.Privileged().Get(ctx, RoomsCol, id)
	if err != nil || d == nil {
		return nil, NoRole, ErrNotFound
	}
	return d, role, nil
}

// Get is the room, for whoever has a role in it.
func (rs Rooms) Get(ctx context.Context, p Principal, id string) (Doc, Role, error) {
	return rs.room(ctx, p, id)
}

// SetMember gives member ("user:<id>" or "group:<id>") a role in the room,
// NoRole taking it away; owners only, and the last owner stays.
func (rs Rooms) SetMember(ctx context.Context, p Principal, room, member string, role Role) error {
	if !strings.HasPrefix(member, "user:") && !strings.HasPrefix(member, "group:") {
		return fmt.Errorf("store: member %q is neither user: nor group:", member)
	}
	if role != NoRole && role.rank() == 0 {
		return fmt.Errorf("store: no role %q", role)
	}
	d, mine, err := rs.room(ctx, p, room)
	if err != nil {
		return err
	}
	if d["archived"] == true || !mine.AtLeast(Owner) {
		return ErrDenied
	}
	e := rs.S.Privileged()
	if role != Owner {
		owners, err := e.Query(ctx, Query{From: MembersCol, Where: And{Eq("room", room), Eq("role", string(Owner))}})
		if err != nil {
			return err
		}
		if len(owners) == 1 && owners[0].Doc["member"] == member {
			return errors.New("store: a room keeps one owner at least")
		}
	}
	if role == NoRole {
		return Delete(ctx, e, MembersCol, memberID(room, member), AnyRev)
	}
	return rs.setMember(ctx, p.TenantID, room, member, role)
}

// Archive puts the room in the archive (on) or takes it out: owners only.
// Archived, it is read only; nothing in it is removed.
func (rs Rooms) Archive(ctx context.Context, p Principal, room string, on bool) error {
	acc, err := rs.Access(ctx, p)
	if err != nil {
		return err
	}
	if acc[room] == NoRole {
		return ErrNotFound
	}
	// an archived room caps everyone at Viewer: its owners are asked of
	// the memberships themselves
	owner := p.HasRole("admin")
	if !owner {
		ms, err := rs.S.Privileged().Query(ctx, Query{From: MembersCol, Where: And{Eq("room", room), Eq("role", string(Owner)), In("member", memberNames(p)...)}})
		if err != nil {
			return err
		}
		owner = len(ms) > 0
	}
	if !owner {
		return ErrDenied
	}
	_, _, err = rs.S.Privileged().Update(ctx, RoomsCol, room, func(cur Doc, _ Rev) (Doc, error) {
		if cur == nil {
			return nil, ErrNotFound
		}
		cur["archived"] = on
		if on {
			cur["archivedAt"] = rs.now()
		} else {
			delete(cur, "archivedAt")
		}
		return cur, nil
	})
	return err
}

// RoomPolicy guards documents that live in a room (their "room" field):
// read with any role there, written with Editor, in the principal's
// tenant only. A document moved between rooms needs Editor in both. The
// rooms themselves are read by whoever has a role in them, and changed
// only through Rooms. A collection not in Cols is not readable at all.
type RoomPolicy struct {
	Cols map[string]bool
}

func (pol RoomPolicy) role(p Principal, d Doc) Role {
	if d == nil || p.TenantID == "" || d["tenant"] != p.TenantID {
		return NoRole
	}
	room, _ := d["room"].(string)
	return p.Rooms[room]
}

func (pol RoomPolicy) Scope(p Principal, col string) Expr {
	if (!pol.Cols[col] && col != RoomsCol) || p.TenantID == "" {
		return False
	}
	var rooms []any
	for id, r := range p.Rooms {
		if r != NoRole {
			rooms = append(rooms, id)
		}
	}
	if len(rooms) == 0 {
		return False
	}
	if col == RoomsCol {
		return And{Eq("tenant", p.TenantID), In("_id", rooms...)}
	}
	return And{Eq("tenant", p.TenantID), In("room", rooms...)}
}

func (pol RoomPolicy) CanRead(p Principal, col, id string, d Doc) bool {
	if col == RoomsCol {
		return d != nil && d["tenant"] == p.TenantID && p.Rooms[id].AtLeast(Viewer)
	}
	return pol.Cols[col] && pol.role(p, d).AtLeast(Viewer)
}

func (pol RoomPolicy) CanWrite(p Principal, col, _ string, old, next Doc) bool {
	if !pol.Cols[col] {
		return false
	}
	if old != nil && !pol.role(p, old).AtLeast(Editor) {
		return false
	}
	return next == nil || pol.role(p, next).AtLeast(Editor)
}

// RoomItem is a room as List gives it: with p's role in it.
type RoomItem struct {
	ID   string
	Role Role
	Doc  Doc
}

// List is the rooms p has a role in, archived ones only when asked.
func (rs Rooms) List(ctx context.Context, p Principal, archived bool) ([]RoomItem, error) {
	acc, err := rs.Access(ctx, p)
	if err != nil || len(acc) == 0 {
		return nil, err
	}
	ids := make([]any, 0, len(acc))
	for id := range acc {
		ids = append(ids, id)
	}
	items, err := rs.S.Privileged().Query(ctx, Query{From: RoomsCol, Where: And{Eq("tenant", p.TenantID), In("_id", ids...)}, OrderBy: []Order{{Field: "title"}}})
	if err != nil {
		return nil, err
	}
	var out []RoomItem
	for _, it := range items {
		if it.Doc["archived"] == true && !archived {
			continue
		}
		out = append(out, RoomItem{ID: it.ID, Role: acc[it.ID], Doc: it.Doc})
	}
	return out, nil
}
