// SPDX-License-Identifier: AGPL-3.0-or-later

// Rooms for the assistant (MCP tools) and the page (POST /api/rooms/<op>):
// one set of operations, each made for a principal through store.Rooms,
// store.Links and a RoomPolicy-guarded store.Store, so the two cannot
// differ in what they let someone see or change (ADR 0001).
//
// On the folder server there are no access limits for now: every caller is
// the tenant's admin and sees every room. Decks start in General; there is
// a Playground beside it. A server with sign-in (the cloud, where the
// Google account is the boundary) will give its principals from that; the
// operations stay the same.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/terotests/sliqtly/mcp-go/store"
)

type roomService struct {
	st    *store.Store
	rooms store.Rooms
	links store.Links
	types *store.LinkTypes
}

// nil where decks are not kept in a store of rooms (the cloud for now)
func newRoomService(env *Env) *roomService {
	if env.Store == nil || env.LocalUser == "" {
		return nil
	}
	st := store.New(env.Store, store.RoomPolicy{Cols: map[string]bool{"shares": true}})
	types := store.DefaultLinkTypes()
	return &roomService{
		st:    st,
		rooms: store.Rooms{S: st},
		links: store.Links{S: st, Types: types, Resolve: resolveRef},
		types: types,
	}
}

// where a ref's document is kept; other kinds (jira:, url:) are told of by
// their own services
func resolveRef(r store.Ref) (string, string, bool) {
	switch r.Kind {
	case "deck":
		return "shares", r.ID, true
	case "room":
		return store.RoomsCol, r.ID, true
	}
	return "", "", false
}

func (s *roomService) principal(ctx context.Context, uid string) (store.Principal, error) {
	// everyone sees everything on the folder server, for now
	p := store.Principal{UserID: uid, TenantID: localTenant, Roles: []string{"admin"}}
	return s.rooms.For(ctx, p)
}

// roomTool is one operation: the MCP tool's description and arguments
type roomTool struct {
	name, title, desc string
	props             map[string]any
	required          []string
	readOnly          bool
	destructive       bool
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// what a room is, for the assistant choosing where work goes
const roomIdea = "A room is one whole piece of work: a task, a Jira ticket, a user story, or another whole such as a project or a theme. It holds that work's presentations, members and links."

var roomTools = []roomTool{
	{name: "list_rooms", title: "List rooms", readOnly: true,
		desc:  "List the rooms you are in, newest first, with your role, description and how many presentations each holds. " + roomIdea + " Every presentation has one home room; new ones start in General, and there is a Playground for trying things.",
		props: map[string]any{"archived": map[string]any{"type": "boolean", "description": "Also list archived rooms"}}},
	{name: "get_room", title: "Show a room", readOnly: true,
		desc:     "A room's members, presentations (deck_id and name) and links.",
		props:    map[string]any{"room_id": strProp("room_id from list_rooms")},
		required: []string{"room_id"}},
	{name: "create_room", title: "Create a room",
		desc:     "Make a room for one whole piece of work, with you as its owner. " + roomIdea + " Name it as the work is known (e.g. \"PROJ-123 Checkout retry\" or the story's title) and put the ticket's link or the story in the description; then move its presentations there with move_presentation. → room_id",
		props:    map[string]any{"title": strProp("The room's name, as the task, ticket or story is known"), "description": strProp("What the work is: the ticket's or story's summary, a link to it"), "kind": strProp("What sort of room: task, ticket, story, project, team, … (free text)")},
		required: []string{"title"}},
	{name: "update_room", title: "Rename or describe a room",
		desc:     "Change a room's name or description (owners only; not General or Playground, nor a room in the archive). Leave out what stays as it is.",
		props:    map[string]any{"room_id": strProp("The room"), "title": strProp("Its new name"), "description": strProp("Its new description (\"\" clears it)")},
		required: []string{"room_id"}},
	{name: "delete_room", title: "Delete a room", destructive: true,
		desc:     "Delete a room (owners only; not General or Playground). Its presentations are not deleted: they move to General. To keep the room's history, archive it with archive_room instead.",
		props:    map[string]any{"room_id": strProp("The room")},
		required: []string{"room_id"}},
	{name: "move_presentation", title: "Move a presentation to a room",
		desc:     "Make room_id the presentation's home room. Needs editor rights in both rooms. Its files stay its own; the room's files are not shared with it.",
		props:    map[string]any{"deck_id": strProp("The presentation's deck_id"), "room_id": strProp("The room to move it to")},
		required: []string{"deck_id", "room_id"}},
	{name: "set_room_member", title: "Set a room member's role", destructive: true,
		desc:     "Give a user or group a role in a room (owners only): viewer reads, editor also changes presentations, owner also manages members. An empty role removes them; the last owner stays.",
		props:    map[string]any{"room_id": strProp("The room"), "member": strProp("user:<id> or group:<id>"), "role": map[string]any{"type": "string", "enum": []string{"viewer", "editor", "owner", ""}}},
		required: []string{"room_id", "member", "role"}},
	{name: "archive_room", title: "Archive a room", destructive: true,
		desc:     "Archive a room (owners only): it becomes read only for everyone and nothing in it is removed. archived false takes it out of the archive.",
		props:    map[string]any{"room_id": strProp("The room"), "archived": map[string]any{"type": "boolean", "description": "false to take it out of the archive (default true)"}},
		required: []string{"room_id"}},
	{name: "link_types", title: "List link types", readOnly: true,
		desc: "The kinds of link between rooms, presentations and outside things (relates_to, parent/child, references, inherits_files …)."},
	{name: "add_link", title: "Link two things",
		desc:     "Link from —rel→ to. Each end is kind:id: room:<room_id>, deck:<deck_id>, or an outside thing such as jira:ABC-123 or url:https://…. You must be able to read both ends. A link never gives anyone access. Adding a link that is there already is no error.",
		props:    map[string]any{"from": strProp("e.g. room:<room_id>"), "rel": strProp("A name from link_types"), "to": strProp("e.g. deck:<deck_id> or jira:ABC-123")},
		required: []string{"from", "rel", "to"}},
	{name: "remove_link", title: "Remove a link", destructive: true,
		desc:     "Remove the link from —rel→ to (who made it, or an admin).",
		props:    map[string]any{"from": strProp("kind:id"), "rel": strProp("The link's type"), "to": strProp("kind:id")},
		required: []string{"from", "rel", "to"}},
	{name: "links_of", title: "List links", readOnly: true,
		desc:     "The links at a room, a presentation or an outside thing that you may see, each read from it.",
		props:    map[string]any{"ref": strProp("room:<room_id>, deck:<deck_id> or e.g. jira:ABC-123")},
		required: []string{"ref"}},
}

func findRoomTool(name string) bool {
	for _, t := range roomTools {
		if t.name == name {
			return true
		}
	}
	return false
}

// the MCP tool list's entries
func (s *roomService) toolsJSON() []any {
	var out []any
	for _, t := range roomTools {
		props := t.props
		if props == nil {
			props = map[string]any{}
		}
		schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false, "$schema": "http://json-schema.org/draft-07/schema#"}
		if len(t.required) > 0 {
			schema["required"] = t.required
		}
		ann := map[string]any{"readOnlyHint": t.readOnly, "openWorldHint": false}
		if !t.readOnly {
			ann["destructiveHint"] = t.destructive
			ann["idempotentHint"] = t.name != "create_room"
		}
		out = append(out, map[string]any{"name": t.name, "title": t.title, "description": t.desc, "inputSchema": schema, "annotations": ann})
	}
	return out
}

// a caller's mistake, said as it is; anything else is the server's
type roomErr struct{ msg string }

func (e roomErr) Error() string { return e.msg }

func argStr(a map[string]any, k string) string {
	v, _ := a[k].(string)
	return strings.TrimSpace(v)
}

func argBool(a map[string]any, k string, def bool) bool {
	if v, ok := a[k].(bool); ok {
		return v
	}
	return def
}

func millisOf(v any) int64 { return millis(v) }

// call runs op for uid. → a JSON-able answer
func (s *roomService) call(ctx context.Context, uid, op string, a map[string]any) (any, error) {
	if !findRoomTool(op) {
		return nil, roomErr{"no such operation: " + op}
	}
	if uid == "" {
		return nil, roomErr{"rooms need sign-in"}
	}
	p, err := s.principal(ctx, uid)
	if err != nil {
		return nil, err
	}
	for _, t := range roomTools {
		if t.name == op {
			for _, k := range t.required {
				if _, ok := a[k]; !ok {
					return nil, roomErr{k + " is missing"}
				}
			}
		}
	}
	out, err := s.run(ctx, p, op, a)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, roomErr{"not found, or not yours to see"}
	case errors.Is(err, store.ErrDenied):
		return nil, roomErr{"your role does not allow that"}
	}
	return out, err
}

type roomRow struct {
	RoomID   string `json:"room_id"`
	Title    string `json:"title"`
	About    string `json:"description,omitempty"`
	Created  int64  `json:"created,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Role     string `json:"role"`
	Archived bool   `json:"archived"`
	Decks    int    `json:"presentations"`
}

type linkRow struct {
	From string `json:"from"`
	Rel  string `json:"rel"`
	To   string `json:"to"`
}

func (s *roomService) run(ctx context.Context, p store.Principal, op string, a map[string]any) (any, error) {
	switch op {
	case "list_rooms":
		rs, err := s.rooms.List(ctx, p, argBool(a, "archived", false))
		if err != nil {
			return nil, err
		}
		decks, err := s.st.Query(ctx, p, store.Query{From: "shares"})
		if err != nil {
			return nil, err
		}
		count := map[string]int{}
		for _, d := range decks {
			room, _ := d.Doc[store.RoomField].(string)
			count[room]++
		}
		rows := []roomRow{}
		for _, r := range rs {
			rows = append(rows, roomRowOf(r.ID, r.Role, r.Doc, count[r.ID]))
		}
		// General and Playground first, then the newest first: rooms are
		// a running process, the latest work on top
		first := func(id string) int {
			switch id {
			case store.GeneralRoom:
				return 0
			case store.PlaygroundRoom:
				return 1
			}
			return 2
		}
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := first(rows[i].RoomID), first(rows[j].RoomID)
			if a != b {
				return a < b
			}
			return rows[i].Created > rows[j].Created
		})
		return map[string]any{"rooms": rows}, nil

	case "get_room":
		id := argStr(a, "room_id")
		d, role, err := s.rooms.Get(ctx, p, id)
		if err != nil {
			return nil, err
		}
		ms, err := s.st.Privileged().Query(ctx, store.Query{From: store.MembersCol, Where: store.And{store.Eq("tenant", p.TenantID), store.Eq("room", id)}, OrderBy: []store.Order{{Field: "member"}}})
		if err != nil {
			return nil, err
		}
		members := []map[string]string{}
		for _, m := range ms {
			members = append(members, map[string]string{"member": fmt.Sprint(m.Doc["member"]), "role": fmt.Sprint(m.Doc["role"])})
		}
		items, err := s.st.Query(ctx, p, store.Query{From: "shares", Where: store.Eq(store.RoomField, id)})
		if err != nil {
			return nil, err
		}
		type deckRow struct {
			DeckID  string `json:"deck_id"`
			Name    string `json:"name"`
			Owner   string `json:"owner,omitempty"`
			Updated int64  `json:"updated"`
			Inherit bool   `json:"inherit_room_files"`
		}
		decks := []deckRow{}
		for _, it := range items {
			at := millisOf(it.Doc["updated"])
			if at == 0 {
				at = millisOf(it.Doc["created"])
			}
			name, _ := it.Doc["name"].(string)
			owner, _ := it.Doc["owner"].(string)
			decks = append(decks, deckRow{it.ID, name, owner, at, it.Doc[store.InheritField] == true})
		}
		sort.SliceStable(decks, func(i, j int) bool { return decks[i].Updated > decks[j].Updated })
		links, err := s.linksOf(ctx, p, store.Ref{Kind: "room", ID: id})
		if err != nil {
			return nil, err
		}
		row := roomRowOf(id, role, d, len(decks))
		return map[string]any{"room": row, "members": members, "presentations": decks, "links": links}, nil

	case "create_room":
		title := argStr(a, "title")
		if title == "" || len(title) > 200 {
			return nil, roomErr{"a room needs a title of at most 200 characters"}
		}
		about := argStr(a, "description")
		if len(about) > 2000 {
			return nil, roomErr{"a room's description is at most 2000 characters"}
		}
		id, err := s.rooms.Create(ctx, p, title, argStr(a, "kind"))
		if err != nil {
			return nil, err
		}
		if about != "" {
			if p, err = s.rooms.For(ctx, p); err != nil {
				return nil, err
			}
			if err := s.rooms.Edit(ctx, p, id, nil, &about); err != nil {
				return nil, err
			}
		}
		return map[string]any{"room_id": id}, nil

	case "update_room":
		var title, about *string
		if v, ok := a["title"].(string); ok {
			v = strings.Join(strings.Fields(v), " ")
			if v == "" || len(v) > 200 {
				return nil, roomErr{"a room needs a title of at most 200 characters"}
			}
			title = &v
		}
		if v, ok := a["description"].(string); ok {
			if len(v) > 2000 {
				return nil, roomErr{"a room's description is at most 2000 characters"}
			}
			about = &v
		}
		if err := s.rooms.Edit(ctx, p, argStr(a, "room_id"), title, about); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil

	case "delete_room":
		if err := s.rooms.Remove(ctx, p, argStr(a, "room_id"), "shares"); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true}, nil

	case "move_presentation":
		deck, room := argStr(a, "deck_id"), argStr(a, "room_id")
		if p.Rooms[room] == store.NoRole {
			return nil, store.ErrNotFound
		}
		_, _, err := s.st.Update(ctx, p, "shares", deck, func(cur store.Doc, _ store.Rev) (store.Doc, error) {
			if cur == nil {
				return nil, store.ErrNotFound
			}
			cur[store.RoomField] = room
			return cur, nil
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"deck_id": deck, "room_id": room}, nil

	case "set_room_member":
		role := store.Role(argStr(a, "role"))
		if err := s.rooms.SetMember(ctx, p, argStr(a, "room_id"), argStr(a, "member"), role); err != nil {
			if strings.HasPrefix(err.Error(), "store: ") {
				return nil, roomErr{strings.TrimPrefix(err.Error(), "store: ")}
			}
			return nil, err
		}
		return map[string]any{"ok": true}, nil

	case "archive_room":
		on := argBool(a, "archived", true)
		if err := s.rooms.Archive(ctx, p, argStr(a, "room_id"), on); err != nil {
			return nil, err
		}
		return map[string]any{"archived": on}, nil

	case "link_types":
		var out []map[string]any
		for _, n := range s.types.Names() {
			t, _ := s.types.Get(n)
			out = append(out, map[string]any{"name": t.Name, "inverse": t.Inverse, "symmetric": t.Symmetric, "label": t.Label})
		}
		return map[string]any{"types": out}, nil

	case "add_link", "remove_link":
		l, err := s.linkArg(a)
		if err != nil {
			return nil, err
		}
		if op == "remove_link" {
			return map[string]any{"ok": true}, s.links.Remove(ctx, p, l)
		}
		id, made, err := s.links.Add(ctx, p, l, nil)
		if err != nil {
			return nil, err
		}
		return map[string]any{"link_id": id, "made": made}, nil

	case "links_of":
		r, err := store.ParseRef(argStr(a, "ref"))
		if err != nil {
			return nil, roomErr{"ref: " + err.Error()}
		}
		links, err := s.linksOf(ctx, p, r)
		if err != nil {
			return nil, err
		}
		return map[string]any{"links": links}, nil
	}
	return nil, roomErr{"no such operation: " + op}
}

func roomRowOf(id string, role store.Role, d store.Doc, decks int) roomRow {
	title, _ := d["title"].(string)
	kind, _ := d["kind"].(string)
	about, _ := d["description"].(string)
	return roomRow{RoomID: id, Title: title, About: about, Created: millisOf(d["created"]), Kind: kind, Role: string(role), Archived: d["archived"] == true, Decks: decks}
}

func (s *roomService) linkArg(a map[string]any) (store.Link, error) {
	from, err := store.ParseRef(argStr(a, "from"))
	if err != nil {
		return store.Link{}, roomErr{"from: " + err.Error()}
	}
	to, err := store.ParseRef(argStr(a, "to"))
	if err != nil {
		return store.Link{}, roomErr{"to: " + err.Error()}
	}
	l := store.Link{From: from, Rel: argStr(a, "rel"), To: to}
	if _, err := s.types.Canonical(l); err != nil {
		return l, roomErr{fmt.Sprintf("%v; see link_types", err)}
	}
	return l, nil
}

func (s *roomService) linksOf(ctx context.Context, p store.Principal, r store.Ref) ([]linkRow, error) {
	ls, err := s.links.Of(ctx, p, r)
	if err != nil {
		return nil, err
	}
	out := []linkRow{}
	for _, l := range ls {
		out = append(out, linkRow{l.From.String(), l.Rel, l.To.String()})
	}
	return out, nil
}

// the answer as the MCP tool's text
func roomJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
