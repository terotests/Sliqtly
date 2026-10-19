// SPDX-License-Identifier: AGPL-3.0-or-later

// An account's own layout on sliqtly.com: its topics (rooms), which topic
// and folder each of its presentations is in, and each one's story (type,
// phase, tags, colour). The editor kept these only in the browser
// (web/rooms.js, localStorage) and so an assistant saw none of them; now the
// server keeps the same facts per account, the editor syncs its own with
// them (POST /editor/api/layout, web/layoutsync.js), and the assistant's
// room tools (list_rooms, get_room, move_presentation, folders, set_story)
// read and change them. Who sees a presentation does not change: a topic
// here is the account's own way of ordering its own presentations.
//
// A fact is web/rooms.js's: "room/<id>" (the room as JSON), "deck/<deck>"
// (its room), "folders/<room>" (JSON [{id, name}]), "filed/<deck>" (its
// folder), "story/<deck>" (JSON), "touched/<room>", "positions/<room>",
// "pinned/<deck>" ("1": first in its room), "vault/<vault>" (JSON {title, decks}: web/vaults.js), "order". Each change is numbered (seq): a page asks for the changes after
// the last number it has. A fact removed is kept as removed for a while so
// the pages hear of it; one that asks from before that is sent everything.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// layoutCol holds layouts/{uid}; no Firestore rule matches it, only the
// server reads and writes it
const layoutCol = "layouts"

const (
	// a removed fact is told of this long, then forgotten
	layoutGone = 30 * 24 * time.Hour
	// the most facts an account keeps (the removed ones not counted), and
	// the longest value of one
	layoutMaxFacts = 20000
	layoutMaxValue = 16 << 10
	// the most a page sends in one sync
	layoutMaxPut = 5000
)

var layoutKey = regexp.MustCompile(`^(?:order|(?:room|deck|touched|folders|filed|positions|story|pinned|vault)/[A-Za-z0-9:_.~-]{1,100})$`)

// one fact: V nil when it was removed; S the change's number, T when (ms)
type layoutFact struct {
	V *string
	S int64
	T int64
}

type layoutDoc struct {
	rev   string
	seq   int64
	floor int64 // the removed facts up to this number are forgotten
	facts map[string]layoutFact
}

func layoutOf(d Doc) layoutDoc {
	l := layoutDoc{facts: map[string]layoutFact{}}
	if d == nil {
		return l
	}
	l.rev, _ = d["rev"].(string)
	l.seq = millis(d["seq"])
	l.floor = millis(d["floor"])
	list, _ := d["facts"].([]any)
	for _, v := range list {
		m, _ := v.(map[string]any)
		k, _ := m["k"].(string)
		if k == "" {
			continue
		}
		f := layoutFact{S: millis(m["s"]), T: millis(m["t"])}
		if s, ok := m["v"].(string); ok {
			f.V = &s
		}
		l.facts[k] = f
	}
	return l
}

func (l layoutDoc) doc(rev string) Doc {
	keys := make([]string, 0, len(l.facts))
	for k := range l.facts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	list := make([]any, 0, len(keys))
	for _, k := range keys {
		f := l.facts[k]
		m := map[string]any{"k": k, "s": f.S, "t": f.T}
		if f.V != nil {
			m["v"] = *f.V
		}
		list = append(list, m)
	}
	return Doc{"rev": rev, "seq": l.seq, "floor": l.floor, "facts": list}
}

// put sets k to v (nil removes it); the same value again is no change
func (l *layoutDoc) put(k string, v *string, now int64) {
	cur, had := l.facts[k]
	if had && ((cur.V == nil && v == nil) || (cur.V != nil && v != nil && *cur.V == *v)) {
		return
	}
	if !had && v == nil {
		return
	}
	l.seq++
	l.facts[k] = layoutFact{V: v, S: l.seq, T: now}
}

func (l *layoutDoc) get(k string) (string, bool) {
	f, ok := l.facts[k]
	if !ok || f.V == nil {
		return "", false
	}
	return *f.V, true
}

func (l *layoutDoc) set(k, v string, now int64) { l.put(k, &v, now) }
func (l *layoutDoc) remove(k string, now int64) { l.put(k, nil, now) }

// prune forgets the facts removed longer ago than layoutGone
func (l *layoutDoc) prune(now int64) {
	for k, f := range l.facts {
		if f.V == nil && now-f.T > layoutGone.Milliseconds() {
			l.floor = max(l.floor, f.S)
			delete(l.facts, k)
		}
	}
}

func (l *layoutDoc) live() int {
	n := 0
	for _, f := range l.facts {
		if f.V != nil {
			n++
		}
	}
	return n
}

// since is the facts changed after the number since, or all of them (full)
// when the page's number is not one of these facts'
func (l *layoutDoc) since(since int64) (facts []map[string]any, full bool) {
	full = since <= 0 || since < l.floor || since > l.seq
	facts = []map[string]any{}
	keys := make([]string, 0, len(l.facts))
	for k := range l.facts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f := l.facts[k]
		if full && f.V == nil {
			continue
		}
		if !full && f.S <= since {
			continue
		}
		m := map[string]any{"k": k, "v": nil}
		if f.V != nil {
			m["v"] = *f.V
		}
		facts = append(facts, m)
	}
	return facts, full
}

var errLayoutBusy = errors.New("the layout is being changed elsewhere; try again")

type layoutStore struct {
	db  DB
	now func() time.Time
}

func (ls *layoutStore) read(ctx context.Context, uid string) (layoutDoc, error) {
	d, err := ls.db.Get(ctx, layoutCol, uid)
	if err != nil {
		return layoutDoc{}, err
	}
	return layoutOf(d), nil
}

// change runs fn on the account's layout and writes what it made, once
// nobody else wrote meanwhile
func (ls *layoutStore) change(ctx context.Context, uid string, fn func(l *layoutDoc, now int64) error) (layoutDoc, error) {
	for try := 0; try < 5; try++ {
		l, err := ls.read(ctx, uid)
		if err != nil {
			return l, err
		}
		now := ls.now().UnixMilli()
		before := l.seq
		if err := fn(&l, now); err != nil {
			return l, err
		}
		if l.seq == before {
			return l, nil
		}
		l.prune(now)
		if l.live() > layoutMaxFacts {
			return l, roomErr{fmt.Sprintf("an account keeps at most %d topic, folder and story facts", layoutMaxFacts)}
		}
		ok, err := ls.db.UpdateIf(ctx, layoutCol, uid, "rev", l.rev, l.doc(newShareID()))
		if err != nil {
			return l, err
		}
		if ok {
			return l, nil
		}
	}
	return layoutDoc{}, errLayoutBusy
}

// ------------------------------------------------------------------ view --

// the page names a presentation of the cloud "cloud:<deck_id>" (web/main.js
// lastKey, web/decklist.js), and so do its facts
func pageDeck(deck string) string { return "cloud:" + deck }

// the rooms an account's layout holds, as web/rooms.js reads them
type layoutRoom struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Created     int64  `json:"created,omitempty"`
	Description string `json:"description,omitempty"`
	Archived    bool   `json:"archived,omitempty"`
	Shared      bool   `json:"shared,omitempty"`
	Role        string `json:"role,omitempty"`
}

type layoutView struct {
	rooms   map[string]layoutRoom
	placed  map[string]string
	folders map[string][]store.Folder
	filed   map[string]string
	stories map[string]store.Story
}

func viewOf(l layoutDoc) layoutView {
	v := layoutView{rooms: map[string]layoutRoom{}, placed: map[string]string{}, folders: map[string][]store.Folder{}, filed: map[string]string{}, stories: map[string]store.Story{}}
	for k, f := range l.facts {
		if f.V == nil {
			continue
		}
		kind, id, _ := strings.Cut(k, "/")
		switch kind {
		case "room":
			var r layoutRoom
			if json.Unmarshal([]byte(*f.V), &r) == nil && r.ID == id && r.Title != "" {
				v.rooms[id] = r
			}
		case "deck":
			v.placed[id] = *f.V
		case "filed":
			v.filed[id] = *f.V
		case "folders":
			var list []any
			if json.Unmarshal([]byte(*f.V), &list) == nil {
				v.folders[id] = store.FoldersOf(store.Doc{"folders": list})
			}
		case "story":
			var m map[string]any
			if json.Unmarshal([]byte(*f.V), &m) == nil {
				v.stories[id] = store.StoryOf(store.Doc{store.StoryField: m})
			}
		}
	}
	return v
}

// a room a presentation can be in: General, Playground or one made
func (v layoutView) hasRoom(id string) bool {
	_, made := v.rooms[id]
	return id == store.GeneralRoom || id == store.PlaygroundRoom || made
}

// the room a presentation is in: the one it was moved to while that room
// is there, else General (web/rooms.js roomOf)
func (v layoutView) roomOf(deck string) string {
	if r := v.placed[deck]; r != "" && r != store.GeneralRoom && v.hasRoom(r) {
		return r
	}
	return store.GeneralRoom
}

// its folder in its room, "" at the room's top (also for a folder gone)
func (v layoutView) folderOf(deck string) string {
	f := v.filed[deck]
	if f != "" && store.HasFolder(v.folders[v.roomOf(deck)], f) {
		return f
	}
	return ""
}

func (v layoutView) storyOf(deck string) store.Story {
	if s, ok := v.stories[deck]; ok {
		return s
	}
	return store.StoryOf(nil)
}

func (v layoutView) title(room string) string {
	switch room {
	case store.GeneralRoom:
		return "General"
	case store.PlaygroundRoom:
		return "Playground"
	}
	return v.rooms[room].Title
}

// the account's role in a room of its layout: its own rooms are its own;
// a shared one's copy says the role it has there; an archived one is read
// only
func (v layoutView) role(room string) store.Role {
	r, made := v.rooms[room]
	switch {
	case !made:
		return store.Owner
	case r.Archived:
		return store.Viewer
	case r.Shared && r.Role != "":
		return store.Role(r.Role)
	}
	return store.Owner
}

func folderName(fs []store.Folder, id string) string {
	for _, f := range fs {
		if f.ID == id {
			return f.Name
		}
	}
	return ""
}

// ---------------------------------------------------------------- service --

// layoutService: the room tools of sliqtly.com, on each account's layout
type layoutService struct {
	ls    *layoutStore
	db    DB
	limit func(who string) string
}

func newLayoutService(db DB) *layoutService {
	return &layoutService{ls: &layoutStore{db: db, now: time.Now}, db: db, limit: rateLimiter(600, 10*time.Minute)}
}

// the operations an assistant has on sliqtly.com: the rooms of its own
// server's that make sense for one account's layout
var layoutOps = map[string]bool{
	"list_rooms": true, "get_room": true, "create_room": true, "update_room": true,
	"move_presentation": true, "create_folder": true, "rename_folder": true,
	"delete_folder": true, "set_story": true,
	// the account's vaults (vaults.go)
	"list_vaults": true, "create_vault": true, "add_to_vault": true, "remove_from_vault": true,
}

// the MCP tool list's entries
func (s *layoutService) toolsJSON() []any {
	var out []any
	for _, t := range roomTools {
		if layoutOps[t.name] {
			out = append(out, toolEntry(t))
		}
	}
	for _, t := range vaultTools {
		out = append(out, toolEntry(t))
	}
	return out
}

// one presentation of the account, as the layout lists it
type layoutDeck struct {
	id, name         string
	created, updated int64
	agents           string
}

// the account's own presentations; an assistant does not see the ones
// hidden from assistants
func (s *layoutService) decks(ctx context.Context, uid string, agent bool) ([]layoutDeck, error) {
	docs, ids, err := s.db.WhereEq(ctx, "shares", "owner", uid)
	if err != nil {
		return nil, err
	}
	out := make([]layoutDeck, 0, len(docs))
	for i, d := range docs {
		if agent && !store.AgentsSee(store.Doc(d)) {
			continue
		}
		name, _ := d["name"].(string)
		made, at := millis(d["created"]), millis(d["updated"])
		if at == 0 {
			at = made
		}
		if made == 0 {
			made = at
		}
		out = append(out, layoutDeck{ids[i], name, made, at, store.AgentsOf(store.Doc(d))})
	}
	return out, nil
}

func (s *layoutService) own(ctx context.Context, uid, deck string) (bool, error) {
	if deck == "" {
		return false, nil
	}
	d, err := s.db.Get(ctx, "shares", deck)
	if err != nil {
		return false, err
	}
	owner, _ := d["owner"].(string)
	return d != nil && owner == uid && store.AgentsWrite(store.Doc(d)), nil
}

// places is each of the account's presentations' room, folder and story,
// for list_presentations
func (s *layoutService) places(ctx context.Context, uid string) (map[string]any, error) {
	l, err := s.ls.read(ctx, uid)
	if err != nil {
		return nil, err
	}
	v := viewOf(l)
	ds, err := s.decks(ctx, uid, true)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, d := range ds {
		room := v.roomOf(pageDeck(d.id))
		folder := v.folderOf(pageDeck(d.id))
		st := v.storyOf(pageDeck(d.id))
		m := map[string]any{"room_id": room, "room": v.title(room), "folder_id": folder, "type": st.Type, "phase": st.Phase, "tags": st.Tags}
		if folder != "" {
			m["folder"] = folderName(v.folders[room], folder)
		}
		if st.Color != "" {
			m["color"] = st.Color
		}
		out[d.id] = m
	}
	return out, nil
}

// made: a presentation the assistant just made for uid goes into room
// ("" General) as a draft, as one made in the editor does
func (s *layoutService) made(ctx context.Context, uid, deck, room string) error {
	_, err := s.ls.change(ctx, uid, func(l *layoutDoc, now int64) error {
		v := viewOf(*l)
		if room != "" && room != store.GeneralRoom && v.hasRoom(room) {
			l.set("deck/"+pageDeck(deck), room, now)
		}
		st := store.StoryOf(nil)
		st.Phase = store.NewStoryPhase
		b, _ := json.Marshal(store.StoryDoc(st))
		l.set("story/"+pageDeck(deck), string(b), now)
		return nil
	})
	return err
}

// call runs op for uid, an assistant's tool
func (s *layoutService) call(ctx context.Context, uid, op string, a map[string]any) (any, error) {
	if !layoutOps[op] {
		return nil, roomErr{"no such operation: " + op}
	}
	if uid == "" {
		return nil, roomErr{"topics need sign-in: connect Sliqtly with sign-in (Google). A client that signs in only when the server asks (Cursor, for one) connects to this server's …/mcp/account address instead of …/mcp."}
	}
	t, ok := roomToolOf(op)
	if !ok {
		t, _ = vaultToolOf(op)
	}
	for _, k := range t.required {
		if _, ok := a[k]; !ok {
			return nil, roomErr{k + " is missing"}
		}
	}
	out, err := s.run(ctx, uid, op, a)
	if errors.Is(err, store.ErrNotFound) {
		return nil, roomErr{"not found, or not yours to see"}
	}
	return out, folderErr(err)
}

func (s *layoutService) run(ctx context.Context, uid, op string, a map[string]any) (any, error) {
	if _, ok := vaultToolOf(op); ok {
		return s.runVault(ctx, uid, op, a)
	}
	switch op {
	case "list_rooms":
		q, err := roomQueryOf(a)
		if err != nil {
			return nil, err
		}
		l, err := s.ls.read(ctx, uid)
		if err != nil {
			return nil, err
		}
		v := viewOf(l)
		ds, err := s.decks(ctx, uid, true)
		if err != nil {
			return nil, err
		}
		count := map[string]int{}
		latest := map[string]int64{}
		for _, d := range ds {
			r := v.roomOf(pageDeck(d.id))
			count[r]++
			latest[r] = max(latest[r], d.updated, d.created)
		}
		rows := []roomRow{
			{RoomID: store.GeneralRoom, Title: "General", Role: string(store.Owner), Decks: count[store.GeneralRoom], Active: latest[store.GeneralRoom]},
			{RoomID: store.PlaygroundRoom, Title: "Playground", Role: string(store.Owner), Decks: count[store.PlaygroundRoom], Active: latest[store.PlaygroundRoom]},
		}
		archived := argBool(a, "archived", false)
		for id, r := range v.rooms {
			if r.Archived && !archived {
				continue
			}
			rows = append(rows, roomRow{RoomID: id, Title: r.Title, About: r.Description, Created: r.Created, Role: string(v.role(id)), Archived: r.Archived, Decks: count[id], Active: max(r.Created, latest[id])})
		}
		page, total := q.pick(rows)
		out := map[string]any{"rooms": page, "total": total, "order": q.Order}
		if next := q.Offset + len(page); next < total {
			out["next_offset"] = next
		}
		return out, nil

	case "get_room":
		id := argStr(a, "room_id")
		l, err := s.ls.read(ctx, uid)
		if err != nil {
			return nil, err
		}
		v := viewOf(l)
		if !v.hasRoom(id) {
			return nil, store.ErrNotFound
		}
		ds, err := s.decks(ctx, uid, true)
		if err != nil {
			return nil, err
		}
		type deckRow struct {
			DeckID  string `json:"deck_id"`
			Name    string `json:"name"`
			Updated int64  `json:"updated"`
			Created int64  `json:"created"`
			Folder  string `json:"folder_id"`
			Agents  string `json:"agents"`
			store.Story
		}
		type folderRow struct {
			FolderID string `json:"folder_id"`
			Name     string `json:"name"`
			Decks    int    `json:"presentations"`
		}
		fs := v.folders[id]
		inFolder := map[string]int{}
		decks := []deckRow{}
		for _, d := range ds {
			pk := pageDeck(d.id)
			if v.roomOf(pk) != id {
				continue
			}
			f := v.folderOf(pk)
			inFolder[f]++
			decks = append(decks, deckRow{d.id, d.name, d.updated, d.created, f, d.agents, v.storyOf(pk)})
		}
		sort.SliceStable(decks, func(i, j int) bool {
			if decks[i].Created != decks[j].Created {
				return decks[i].Created < decks[j].Created
			}
			return decks[i].DeckID < decks[j].DeckID
		})
		folders := []folderRow{}
		for _, f := range fs {
			folders = append(folders, folderRow{f.ID, f.Name, inFolder[f.ID]})
		}
		r := v.rooms[id]
		row := roomRow{RoomID: id, Title: v.title(id), About: r.Description, Created: r.Created, Role: string(v.role(id)), Archived: r.Archived, Decks: len(decks)}
		return map[string]any{"room": row, "members": []any{}, "folders": folders, "presentations": decks, "links": []any{}}, nil

	case "create_room":
		title := strings.Join(strings.Fields(argStr(a, "title")), " ")
		if title == "" || len(title) > 200 {
			return nil, roomErr{"a room needs a title of at most 200 characters"}
		}
		about := strings.TrimSpace(strings.ReplaceAll(argStr(a, "description"), "\r", ""))
		if len(about) > 2000 {
			return nil, roomErr{"a room's description is at most 2000 characters"}
		}
		id := ""
		_, err := s.ls.change(ctx, uid, func(l *layoutDoc, now int64) error {
			v := viewOf(*l)
			for id = "r-" + store.NewFolderID(); v.hasRoom(id); id = "r-" + store.NewFolderID() {
			}
			// newer than every room there, as web/rooms.js createRoom
			made := now
			for _, r := range v.rooms {
				made = max(made, r.Created+1)
			}
			b, _ := json.Marshal(layoutRoom{ID: id, Title: title, Created: made, Description: about})
			l.set("room/"+id, string(b), now)
			l.set("touched/"+id, fmt.Sprint(now), now)
			return nil
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"room_id": id}, nil

	case "update_room":
		id := argStr(a, "room_id")
		_, err := s.ls.change(ctx, uid, func(l *layoutDoc, now int64) error {
			v := viewOf(*l)
			r, ok := v.rooms[id]
			if !ok {
				if v.hasRoom(id) {
					return roomErr{"General and Playground keep their names"}
				}
				return store.ErrNotFound
			}
			if r.Archived || r.Shared {
				return roomErr{"your role does not allow that"}
			}
			if t, ok := a["title"].(string); ok {
				t = strings.Join(strings.Fields(t), " ")
				if t == "" || len(t) > 200 {
					return roomErr{"a room needs a title of at most 200 characters"}
				}
				r.Title = t
			}
			if d, ok := a["description"].(string); ok {
				d = strings.TrimSpace(strings.ReplaceAll(d, "\r", ""))
				if len(d) > 2000 {
					return roomErr{"a room's description is at most 2000 characters"}
				}
				r.Description = d
			}
			b, _ := json.Marshal(r)
			l.set("room/"+id, string(b), now)
			return nil
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil

	case "move_presentation":
		deck, room, folder := argStr(a, "deck_id"), argStr(a, "room_id"), argStr(a, "folder_id")
		mine, err := s.own(ctx, uid, deck)
		if err != nil {
			return nil, err
		}
		if !mine {
			return nil, store.ErrNotFound
		}
		var out map[string]any
		_, err = s.ls.change(ctx, uid, func(l *layoutDoc, now int64) error {
			v := viewOf(*l)
			if !v.hasRoom(room) {
				return store.ErrNotFound
			}
			if !v.role(room).AtLeast(store.Editor) {
				return roomErr{"your role does not allow that"}
			}
			fs := v.folders[room]
			if folder != "" && !store.HasFolder(fs, folder) {
				return roomErr{"no folder " + folder + " in that room: get_room lists its folders, create_folder makes one"}
			}
			pk := pageDeck(deck)
			from, fromFolder := v.roomOf(pk), v.folderOf(pk)
			if room == store.GeneralRoom {
				l.remove("deck/"+pk, now)
			} else {
				l.set("deck/"+pk, room, now)
			}
			if folder != "" {
				l.set("filed/"+pk, folder, now)
			} else {
				l.remove("filed/"+pk, now)
			}
			if from != room {
				fromFolder = ""
			}
			out = map[string]any{"deck_id": deck, "room_id": room, "from_room_id": from, "folder_id": folder, "from_folder_id": fromFolder, "moved": from != room || fromFolder != folder, "room": v.title(room)}
			if folder != "" {
				out["folder"] = folderName(fs, folder)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return out, nil

	case "create_folder", "rename_folder", "delete_folder":
		room, id := argStr(a, "room_id"), argStr(a, "folder_id")
		var out map[string]any
		_, err := s.ls.change(ctx, uid, func(l *layoutDoc, now int64) error {
			v := viewOf(*l)
			if !v.hasRoom(room) {
				return store.ErrNotFound
			}
			if !v.role(room).AtLeast(store.Editor) {
				return roomErr{"your role does not allow that"}
			}
			fs := v.folders[room]
			var next []store.Folder
			var err error
			switch op {
			case "create_folder":
				var fid string
				var made bool
				next, fid, made, err = store.AddFolder(fs, argStr(a, "name"), store.NewFolderID)
				out = map[string]any{"folder_id": fid, "made": made}
			case "rename_folder":
				next, err = store.RenameFolder(fs, id, argStr(a, "name"))
				out = map[string]any{"ok": true}
			default:
				next, err = store.RemoveFolder(fs, id)
				// its presentations are at the room's top again
				moved := 0
				for deck, f := range v.filed {
					if f == id && v.roomOf(deck) == room {
						l.remove("filed/"+deck, now)
						moved++
					}
				}
				out = map[string]any{"deleted": true, "moved": moved}
			}
			if err != nil {
				return err
			}
			if len(next) == 0 {
				l.remove("folders/"+room, now)
			} else {
				b, _ := json.Marshal(store.FolderDocs(next))
				l.set("folders/"+room, string(b), now)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return out, nil

	case "set_story":
		deck := argStr(a, "deck_id")
		mine, err := s.own(ctx, uid, deck)
		if err != nil {
			return nil, err
		}
		if !mine {
			return nil, store.ErrNotFound
		}
		var c store.StoryChange
		for k, at := range map[string]**string{"type": &c.Type, "phase": &c.Phase, "color": &c.Color} {
			if v, ok := a[k]; ok {
				str, ok := v.(string)
				if !ok {
					return nil, roomErr{k + " is not a string"}
				}
				*at = &str
			}
		}
		if v, ok := a["tags"]; ok {
			list, ok := v.([]any)
			if !ok {
				return nil, roomErr{"tags is not a list of strings"}
			}
			tags := []string{}
			for _, t := range list {
				if t, ok := t.(string); ok {
					tags = append(tags, t)
				}
			}
			c.Tags = &tags
		}
		var now store.Story
		_, err = s.ls.change(ctx, uid, func(l *layoutDoc, at int64) error {
			next, err := c.Apply(viewOf(*l).storyOf(pageDeck(deck)))
			if err != nil {
				return err
			}
			now = next
			b, _ := json.Marshal(store.StoryDoc(next))
			l.set("story/"+pageDeck(deck), string(b), at)
			return nil
		})
		if errors.Is(err, store.ErrStory) {
			return nil, roomErr{"type is one of " + strings.Join(store.StoryTypes, ", ") + "; phase one of " + strings.Join(store.StoryPhases, ", ") + "; color #rrggbb or \"\""}
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"deck_id": deck, "type": now.Type, "phase": now.Phase, "tags": now.Tags, "color": now.Color}, nil
	}
	return nil, roomErr{"no such operation: " + op}
}

// ----------------------------------------------------------- page's sync --

// sync: the facts the page changed (put, v null removes), then the changes
// after the number it has (since). fill (all of put's, or a fact's own):
// the page's first sync sends what it kept before, which goes only where
// the server has nothing, so what another browser already keeps there stays
// and the page takes it.
func (s *layoutService) sync(ctx context.Context, uid string, since int64, put []any, fill bool) (map[string]any, error) {
	if len(put) > layoutMaxPut {
		return nil, roomErr{fmt.Sprintf("at most %d facts in one sync", layoutMaxPut)}
	}
	type change struct {
		k    string
		v    *string
		fill bool
	}
	var cs []change
	for _, x := range put {
		m, _ := x.(map[string]any)
		k, _ := m["k"].(string)
		if !layoutKey.MatchString(k) {
			return nil, roomErr{"not a layout fact: " + k}
		}
		c := change{k: k, fill: fill || m["fill"] == true}
		switch v := m["v"].(type) {
		case string:
			if len(v) > layoutMaxValue {
				return nil, roomErr{"a fact is at most 16 KB: " + k}
			}
			c.v = &v
		case nil:
		default:
			return nil, roomErr{"a fact's value is a string or null: " + k}
		}
		cs = append(cs, c)
	}
	l, err := s.ls.change(ctx, uid, func(l *layoutDoc, now int64) error {
		for _, c := range cs {
			if c.fill {
				if _, known := l.facts[c.k]; known || c.v == nil {
					continue
				}
			}
			l.put(c.k, c.v, now)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	facts, full := l.since(since)
	return map[string]any{"seq": l.seq, "full": full, "facts": facts}, nil
}

// POST /editor/api/layout {since, put: [{k, v}], fill}: the signed-in
// account's layout, as the page keeps it (web/layoutsync.js)
func serveLayout(env *Env, g *editorGate, w http.ResponseWriter, r *http.Request) {
	s := env.layouts
	if s == nil {
		editorJSON(w, 404, map[string]string{"error": "No such call."})
		return
	}
	if r.Method != http.MethodPost || !sameSite(env, r) {
		editorJSON(w, 403, map[string]string{"error": "Not allowed."})
		return
	}
	t := g.user(r)
	if t == nil {
		editorJSON(w, 401, map[string]string{"error": "Sign in again.", "code": "signed-out"})
		return
	}
	if s.limit(t.UID) != "" {
		editorJSON(w, 429, map[string]string{"error": "Too many requests; try again in a few minutes."})
		return
	}
	a := map[string]any{}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&a); err != nil && !errors.Is(err, io.EOF) {
		editorJSON(w, 400, map[string]string{"error": "A JSON object is expected."})
		return
	}
	put, _ := a["put"].([]any)
	out, err := s.sync(r.Context(), t.UID, argInt(a, "since"), put, argBool(a, "fill", false))
	var re roomErr
	switch {
	case errors.As(err, &re):
		editorJSON(w, 400, map[string]string{"error": re.msg})
	case err != nil:
		log.Printf("editor layout: %v", err)
		editorJSON(w, 503, map[string]string{"error": "The topics could not be saved; trying again shortly."})
	default:
		editorJSON(w, 200, out)
	}
}

// ------------------------------------------------------------------ host --

// Places is each of uid's presentations' room (topic), folder and story as
// JSON {deck_id: {room_id, room, folder_id, folder, type, phase, tags}},
// for list_presentations: on a server of one's own from its rooms, on
// sliqtly.com from the account's layout; "{}" where neither is kept
func (h *McpHost) Places(uid string) string {
	var out map[string]any
	var err error
	switch {
	case h.env.rooms != nil:
		out, err = h.env.rooms.places(h.ctx, uid)
	case h.env.layouts != nil && uid != "":
		out, err = h.env.layouts.places(h.ctx, uid)
	}
	if err != nil {
		// the list is still worth having without them
		log.Printf("places: %v", err)
	}
	if out == nil {
		return "{}"
	}
	return roomJSON(out)
}

// DeckMade: the assistant made deck for uid into room ("" General). On
// sliqtly.com it goes there in the account's layout, as a draft; a server
// of one's own wrote both on the deck itself.
func (h *McpHost) DeckMade(uid, deck, room string) {
	if h.env.rooms != nil || h.env.layouts == nil || uid == "" {
		return
	}
	if err := h.env.layouts.made(h.ctx, uid, deck, room); err != nil {
		log.Printf("deck made %s: %v", deck, err)
	}
}

// places on a server of one's own: from the decks' own room, folder and
// story, as the assistant may see them
func (s *roomService) places(ctx context.Context, uid string) (map[string]any, error) {
	p := s.localPrincipal(uid)
	p.Agent = true
	p, err := s.rooms.For(ctx, p)
	if err != nil {
		return nil, err
	}
	rs, err := s.rooms.List(ctx, p, true)
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	folders := map[string][]store.Folder{}
	for _, r := range rs {
		titles[r.ID], _ = r.Doc["title"].(string)
		folders[r.ID] = store.FoldersOf(r.Doc)
	}
	items, err := s.st.Query(ctx, p, store.Query{From: "shares"})
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, it := range items {
		room, _ := it.Doc[store.RoomField].(string)
		folder, _ := it.Doc[store.FolderField].(string)
		if !store.HasFolder(folders[room], folder) {
			folder = ""
		}
		st := store.StoryOf(it.Doc)
		m := map[string]any{"room_id": room, "room": titles[room], "folder_id": folder, "type": st.Type, "phase": st.Phase, "tags": st.Tags}
		if folder != "" {
			m["folder"] = folderName(folders[room], folder)
		}
		if st.Color != "" {
			m["color"] = st.Color
		}
		out[it.ID] = m
	}
	return out, nil
}
