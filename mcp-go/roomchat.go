// SPDX-License-Identifier: AGPL-3.0-or-later

// A room's chat: the conversation of the people (and the assistants) in a
// room, kept in the folder's sliqtly.db (store.ChatLog). Like the rest of
// a room it is read by whoever has a role in it and written with Editor; an
// archived room's chat is read only.
//
// The same operations serve the page (POST /api/rooms/<op>) and the
// assistant (MCP tools). What differs is who speaks: the page speaks for a
// person, by the name, avatar and colour the browser chose (on the folder
// server nobody signs in, so a person is who they say they are, as in the
// deck's own chat); an assistant speaks as a robot by its own name
// ("Claude", "Cursor"), so the people see what the assistants do and what
// they were asked.
//
// Every message, change and reaction is told on the page's stream
// (localevents.go) as {"k":"chat","v":{"t":"msg","room":…,"msg":…}}; a
// page that sees a gap in a room's seq numbers asks for what came after
// (read_room_chat after_seq). Who is here: pages that have the room's chat
// open say so (chat_here) at least once a minute.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/terotests/sliqtly/mcp-go/store"
)

const (
	// a message's text; a longer one is a file of the room's
	chatMaxText = 40000
	// a page that has not said it is here for this long is not
	chatHereFor = 75 * time.Second
	// the people of a room (chat_people): one document per person per
	// tenant, with the rooms they were seen in (rooms: room → when; in: the
	// same rooms as a list, which a query asks for) and the rooms they have
	// open now (here: room → when they last said so). Kept with the
	// documents, not in the server's memory, so every instance of a cloud
	// server tells the same.
	chatPeopleCol = "chat_people"
)

var chatTools = []roomTool{
	{name: "read_room_chat", title: "Read a room's chat", readOnly: true,
		desc: "Read a room's chat: what the people (and other assistants) in the room are saying, newest last, with the room's name, description and who is here. Top-level messages, or one thread's replies with thread_id. For what came since you last looked, give after_seq (the last seq you saw); to read further back, before_seq. mentioning keeps only the messages that name you (\"@Claude\"): people ask an assistant for things that way. Text is Markdown-like: *bold*, _italic_, `code`, ``` blocks, > quotes, #room and @name.",
		props: map[string]any{
			"room_id":    strProp("room_id from list_rooms"),
			"thread_id":  strProp("A message's id: its thread's replies instead of the top level"),
			"before_seq": map[string]any{"type": "integer", "description": "Only messages numbered below this"},
			"after_seq":  map[string]any{"type": "integer", "description": "Only messages numbered above this (the last seq you saw)"},
			"limit":      map[string]any{"type": "integer", "description": "How many, the newest of them (default 50, at most 500)"},
			"mentioning": strProp("Only messages that @mention this name, e.g. your own"),
		},
		required: []string{"room_id"}},
	{name: "post_room_message", title: "Post to a room's chat",
		desc: "Post a message to a room's chat. You appear as a robot by your agent name, so the people in the room see what the assistants are doing there: say what you were asked and what you did, and post progress while a longer job runs (give message_id of your earlier message to replace its text instead of adding a new one, e.g. a status line you keep up to date). Answer a message in its thread with thread_id. Markdown-like text: *bold*, _italic_, `code`, ``` code blocks ```, > quote, - lists, links, :emoji:, @name, #room. A presentation is shown in the chat with [[slides:<deck_id>]] (one slide: [[slides:<deck_id>#3]]). Long text is folded in the chat with \"Show more\".",
		props: map[string]any{
			"room_id":    strProp("room_id from list_rooms"),
			"text":       strProp("The message"),
			"thread_id":  strProp("Reply in this message's thread"),
			"message_id": strProp("Replace the text of a message you posted earlier (progress) instead of posting a new one"),
			"agent":      strProp("Your name as the room sees it: \"Claude\", \"Cursor\", … (default \"Assistant\")"),
			"files":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Names of the room's files (list_room_files, or put one in with put_room_file) to show with the message: pictures are shown, other files as a link to download"},
		},
		required: []string{"room_id"}},
	{name: "put_room_file", title: "Put a file into a room",
		desc: "Put a picture or another file into a room's files, to show in its chat: then post_room_message with files [its name] (pictures are shown, other files as a link to download). Give one of data_base64, text, url or path. When the room has a file by the name already, the new one gets another name (\"chart (2).png\") unless replace is true; the answer has the name it was kept by. At most 20 MB.",
		props: map[string]any{
			"room_id":     strProp("room_id from list_rooms"),
			"name":        strProp("The file's name, with its extension: \"chart.png\", \"notes.pdf\""),
			"data_base64": strProp("The file's bytes as base64 (a data: URL is taken too)"),
			"text":        strProp("A text file's content as it is (an SVG, CSV, Markdown …), instead of base64"),
			"url":         strProp("A public https URL the server fetches the file from"),
			"path":        strProp("A Sliqtly server on your own computer started with import folders (SLIQTLY_IMPORT_DIRS): the file's absolute path in one of them"),
			"replace":     map[string]any{"type": "boolean", "description": "Replace the room's file of the same name instead of keeping both"},
		},
		required: []string{"room_id", "name"}},
	{name: "list_room_files", title: "List a room's files", readOnly: true,
		desc: "The room's own files: what people put into its chat (pictures, documents), with each one's name, type, size and address. A message shows them with post_room_message's files. The room's presentations have files of their own (list_files).",
		props: map[string]any{
			"room_id": strProp("room_id from list_rooms"),
		},
		required: []string{"room_id"}},
	// the page's own
	{name: "chat_delete", title: "Delete a chat message", pageOnly: true, destructive: true,
		props:    map[string]any{"room_id": strProp(""), "message_id": strProp(""), "as": map[string]any{"type": "object"}},
		required: []string{"room_id", "message_id"}},
	{name: "chat_react", title: "React to a chat message", pageOnly: true,
		props:    map[string]any{"room_id": strProp(""), "message_id": strProp(""), "emoji": strProp(""), "as": map[string]any{"type": "object"}},
		required: []string{"room_id", "message_id", "emoji"}},
	{name: "chat_here", title: "Say one is in a room's chat", pageOnly: true,
		props:    map[string]any{"room_id": strProp(""), "as": map[string]any{"type": "object"}, "away": map[string]any{"type": "boolean"}},
		required: []string{"room_id"}},
}

func isChatTool(name string) bool {
	for _, t := range chatTools {
		if t.name == name {
			return true
		}
	}
	return false
}

var (
	personID   = regexp.MustCompile(`^[A-Za-z0-9_-]{4,40}$`)
	avatarName = regexp.MustCompile(`^[a-z0-9-]{1,24}$`)
	hexColor   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// the robots' colours, by name
var botColors = []string{"#0ea5e9", "#8b5cf6", "#10b981", "#f59e0b", "#ef4444", "#14b8a6"}

// who speaks: the page's person ("as": {id, name, avatar, color}) or the
// assistant's robot ("agent")
func chatSpeaker(p store.Principal, via string, a map[string]any) (store.ChatFrom, error) {
	if via == viaMcp {
		name := cleanName(a["agent"])
		if name == "" {
			name = "Assistant"
		}
		slug := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(name), "-"), "-")
		if slug == "" {
			slug = "assistant"
		}
		h := 0
		for _, r := range slug {
			h = h*31 + int(r)
		}
		if h < 0 {
			h = -h
		}
		return store.ChatFrom{ID: "bot-" + slug, User: p.UserID, Name: name, Kind: "bot", Avatar: "robot", Color: botColors[h%len(botColors)]}, nil
	}
	as, _ := a["as"].(map[string]any)
	id, _ := as["id"].(string)
	if !personID.MatchString(id) {
		return store.ChatFrom{}, roomErr{"as.id: who is writing, 4 to 40 letters, digits, - or _"}
	}
	f := store.ChatFrom{ID: "p-" + id, User: p.UserID, Name: cleanName(as["name"]), Kind: "person"}
	if f.Name == "" {
		f.Name = "Anonymous"
	}
	if v, _ := as["avatar"].(string); avatarName.MatchString(v) {
		f.Avatar = v
	}
	if v, _ := as["color"].(string); hexColor.MatchString(v) {
		f.Color = strings.ToLower(v)
	}
	return f, nil
}

func (s *roomService) chatRole(p store.Principal, room string, write bool) error {
	r := p.Rooms[room]
	if r == store.NoRole {
		return store.ErrNotFound
	}
	if write && !r.AtLeast(store.Editor) {
		return roomErr{"this room is read only (archived, or your role is viewer)"}
	}
	return nil
}

func argInt(a map[string]any, k string) int64 {
	switch v := a[k].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

// a message as answered: the time also as text, for an assistant reading;
// its files with their addresses
type chatOut struct {
	store.ChatMsg
	Time  string        `json:"time"`
	Files []chatFileOut `json:"files,omitempty"`
}

type chatFileOut struct {
	store.ChatFile
	URL string `json:"url"`
}

func (s *roomService) outMsgs(ms []store.ChatMsg) []chatOut {
	out := make([]chatOut, 0, len(ms))
	for _, m := range ms {
		o := chatOut{ChatMsg: m, Time: time.UnixMilli(m.At).UTC().Format(time.RFC3339)}
		for _, f := range m.Files {
			o.Files = append(o.Files, chatFileOut{f, s.fileURL(m.Room, f.Name)})
		}
		out = append(out, o)
	}
	return out
}

// a person as the room knows them
type chatPerson struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Avatar string `json:"avatar,omitempty"`
	Color  string `json:"color,omitempty"`
	Here   bool   `json:"here"`
	Seen   int64  `json:"seen"`
}

func (s *roomService) now() time.Time { return time.Now() }

// remember who spoke or came in this room (their name, avatar and colour
// as they are now, for @mentions and the member count); here: they have the
// room's chat open now (chat_here), or, false, not any more; nil: as it was
func (s *roomService) seePerson(ctx context.Context, tenant, room string, f store.ChatFrom, at time.Time, here *bool) error {
	_, _, err := s.st.Privileged().Update(ctx, chatPeopleCol, tenant+"~"+f.ID, func(cur store.Doc, _ store.Rev) (store.Doc, error) {
		if cur == nil {
			cur = store.Doc{}
		}
		cur["tenant"], cur["id"], cur["name"], cur["kind"] = tenant, f.ID, f.Name, f.Kind
		cur["avatar"], cur["color"] = f.Avatar, f.Color
		rooms, _ := cur["rooms"].(map[string]any)
		if rooms == nil {
			rooms = map[string]any{}
		}
		rooms[room] = at.UnixMilli()
		cur["rooms"] = rooms
		in := []any{}
		for r := range rooms {
			in = append(in, r)
		}
		sort.Slice(in, func(i, j int) bool { return in[i].(string) < in[j].(string) })
		cur["in"] = in
		if here != nil {
			open, _ := cur["here"].(map[string]any)
			if open == nil {
				open = map[string]any{}
			}
			if *here {
				open[room] = at.UnixMilli()
			} else {
				delete(open, room)
			}
			cur["here"] = open
		}
		return cur, nil
	})
	return err
}

// the room's people, here first, then the latest seen
func (s *roomService) people(ctx context.Context, tenant, room string) ([]chatPerson, error) {
	items, err := s.st.Privileged().Query(ctx, store.Query{From: chatPeopleCol, Where: store.And{store.Eq("tenant", tenant), store.Has("in", room)}})
	if err != nil {
		return nil, err
	}
	now := s.now().UnixMilli()
	out := []chatPerson{}
	for _, it := range items {
		rooms, _ := it.Doc["rooms"].(map[string]any)
		seen, ok := rooms[room]
		if !ok {
			continue
		}
		open, _ := it.Doc["here"].(map[string]any)
		at, isOpen := open[room]
		here := isOpen && now-millis(at) <= chatHereFor.Milliseconds()
		str := func(k string) string { v, _ := it.Doc[k].(string); return v }
		out = append(out, chatPerson{ID: str("id"), Name: str("name"), Kind: str("kind"), Avatar: str("avatar"), Color: str("color"), Here: here, Seen: millis(seen)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Here != out[j].Here {
			return out[i].Here
		}
		if out[i].Seen != out[j].Seen {
			return out[i].Seen > out[j].Seen
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// how many of them are here
func hereCount(ps []chatPerson) int {
	n := 0
	for _, p := range ps {
		if p.Here {
			n++
		}
	}
	return n
}

func (s *roomService) tell(room string, v map[string]any) {
	if s.notify != nil {
		v["room"] = room
		s.notify(room, v)
	}
}

func mentions(text, name string) bool {
	name = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "@"))
	if name == "" {
		return true
	}
	t := strings.ToLower(text)
	for i := strings.Index(t, "@"+name); i >= 0; {
		end := i + 1 + len(name)
		if end >= len(t) || !isWordByte(t[end]) {
			return true
		}
		j := strings.Index(t[end:], "@"+name)
		if j < 0 {
			break
		}
		i = end + j
	}
	return false
}

func isWordByte(b byte) bool {
	return b == '_' || b == '-' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || b >= 0x80
}

func (s *roomService) runChat(ctx context.Context, p store.Principal, via, op string, a map[string]any) (any, error) {
	if s.chat == nil {
		return nil, roomErr{"this server keeps no chat"}
	}
	room := argStr(a, "room_id")
	tenant := p.TenantID
	switch op {
	case "read_room_chat":
		if err := s.chatRole(p, room, false); err != nil {
			return nil, err
		}
		d, _, err := s.rooms.Get(ctx, p, room)
		if err != nil {
			return nil, err
		}
		thread := argStr(a, "thread_id")
		q := store.ChatPage{Thread: thread, Before: argInt(a, "before_seq"), After: argInt(a, "after_seq"), Limit: int(argInt(a, "limit"))}
		ms, err := s.chat.Page(ctx, tenant, room, q)
		if err != nil {
			return nil, err
		}
		if who := argStr(a, "mentioning"); who != "" {
			kept := ms[:0]
			for _, m := range ms {
				if !m.Deleted && mentions(m.Text, who) {
					kept = append(kept, m)
				}
			}
			ms = kept
		}
		last, err := s.chat.Last(ctx, tenant, room)
		if err != nil {
			return nil, err
		}
		ps, err := s.people(ctx, tenant, room)
		if err != nil {
			return nil, err
		}
		title, _ := d["title"].(string)
		about, _ := d["description"].(string)
		out := map[string]any{
			"room":     map[string]any{"room_id": room, "title": title, "description": about, "here": hereCount(ps), "members": len(ps), "archived": d["archived"] == true},
			"messages": s.outMsgs(ms),
			"last_seq": last,
			"people":   ps,
		}
		if thread != "" {
			root, err := s.chat.Get(ctx, tenant, room, thread)
			if err != nil {
				return nil, err
			}
			out["root"] = s.outMsgs([]store.ChatMsg{root})[0]
		}
		return out, nil

	case "post_room_message":
		if err := s.chatRole(p, room, true); err != nil {
			return nil, err
		}
		from, err := chatSpeaker(p, via, a)
		if err != nil {
			return nil, err
		}
		text := strings.TrimRight(strings.ReplaceAll(argRaw(a, "text"), "\r\n", "\n"), " \n\t")
		text = strings.TrimLeft(text, "\n")
		files, err := s.chatFiles(ctx, room, a["files"])
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(text) == "" && len(files) == 0 {
			return nil, roomErr{"the message is empty"}
		}
		if utf8.RuneCountInString(text) > chatMaxText {
			return nil, roomErr{fmt.Sprintf("a message is at most %d characters; put longer text in a file of the room", chatMaxText)}
		}
		var m store.ChatMsg
		edit := argStr(a, "message_id")
		if id := edit; id != "" {
			m, err = s.chat.Change(ctx, tenant, room, id, func(x *store.ChatMsg) error {
				if x.From.ID != from.ID || x.Deleted {
					return roomErr{"only a message of your own can be changed"}
				}
				if x.Text != text {
					x.Links = nil
				}
				x.Text = text
				if _, given := a["files"]; given {
					x.Files = files
				}
				x.Edited = time.Now().UnixMilli()
				x.From.Name, x.From.Avatar, x.From.Color = from.Name, from.Avatar, from.Color
				return nil
			})
		} else {
			m, err = s.chat.Append(ctx, store.ChatMsg{Tenant: tenant, Room: room, Thread: argStr(a, "thread_id"), From: from, Text: text, Files: files})
		}
		if err != nil {
			return nil, err
		}
		if err := s.seePerson(ctx, tenant, room, from, time.UnixMilli(m.At), nil); err != nil {
			return nil, err
		}
		s.tellMsg(ctx, tenant, room, m, edit == "")
		if len(m.Links) == 0 {
			id, said := m.ID, m.Text
			run := s.later
			if run == nil {
				run = func(fn func()) { go fn() }
			}
			run(func() { s.previews(tenant, room, id, said) })
		}
		return map[string]any{"message_id": m.ID, "seq": m.Seq, "thread_id": m.Thread}, nil

	case "chat_delete", "chat_react":
		if err := s.chatRole(p, room, true); err != nil {
			return nil, err
		}
		from, err := chatSpeaker(p, via, a)
		if err != nil {
			return nil, err
		}
		emoji := argStr(a, "emoji")
		if op == "chat_react" && (emoji == "" || utf8.RuneCountInString(emoji) > 16 || strings.ContainsAny(emoji, " \t\n")) {
			return nil, roomErr{"emoji: one emoji"}
		}
		m, err := s.chat.Change(ctx, tenant, room, argStr(a, "message_id"), func(x *store.ChatMsg) error {
			if op == "chat_react" {
				if x.Deleted {
					return roomErr{"that message was deleted"}
				}
				store.ToggleReaction(x, emoji, from.ID)
				return nil
			}
			if x.From.ID != from.ID {
				return roomErr{"only a message of your own can be deleted"}
			}
			x.Deleted, x.Text, x.Reactions = true, "", nil
			return nil
		})
		if err != nil {
			return nil, err
		}
		s.tellMsg(ctx, tenant, room, m, false)
		return map[string]any{"ok": true}, nil

	case "put_room_file":
		return s.putToolFile(ctx, p, room, a)

	case "list_room_files":
		if err := s.chatRole(p, room, false); err != nil {
			return nil, err
		}
		if s.files == nil {
			return nil, roomErr{"this server keeps no room files"}
		}
		_, out, err := s.roomFiles(ctx, room)
		if err != nil {
			return nil, err
		}
		return map[string]any{"files": out}, nil

	case "chat_here":
		if err := s.chatRole(p, room, false); err != nil {
			return nil, err
		}
		from, err := chatSpeaker(p, via, a)
		if err != nil {
			return nil, err
		}
		away := argBool(a, "away", false)
		ps, err := s.people(ctx, tenant, room)
		if err != nil {
			return nil, err
		}
		before := hereCount(ps)
		open := !away
		if err := s.seePerson(ctx, tenant, room, from, s.now(), &open); err != nil {
			return nil, err
		}
		if ps, err = s.people(ctx, tenant, room); err != nil {
			return nil, err
		}
		here := hereCount(ps)
		if here != before || away {
			s.tell(room, map[string]any{"t": "here", "here": here, "people": ps})
		}
		last, err := s.chat.Last(ctx, tenant, room)
		if err != nil {
			return nil, err
		}
		return map[string]any{"here": here, "people": ps, "last_seq": last}, nil
	}
	return nil, roomErr{"no such operation: " + op}
}

// the text as sent: not trimmed of its inner layout (code keeps its
// indent)
func argRaw(a map[string]any, k string) string {
	v, _ := a[k].(string)
	return v
}

// a message told to the pages; a new reply tells its root too (its count)
func (s *roomService) tellMsg(ctx context.Context, tenant, room string, m store.ChatMsg, posted bool) {
	s.tell(room, map[string]any{"t": "msg", "msg": s.outMsgs([]store.ChatMsg{m})[0]})
	if posted && m.Thread != "" {
		if root, err := s.chat.Get(ctx, tenant, room, m.Thread); err == nil {
			s.tell(room, map[string]any{"t": "msg", "msg": s.outMsgs([]store.ChatMsg{root})[0]})
		}
	}
}
