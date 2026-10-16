// SPDX-License-Identifier: AGPL-3.0-or-later

// Where the person keeps their presentations, sorted from the editor's AI
// assistant: topics (rooms) and their folders on sliqtly.com. The drawer
// reads them with MCP's own read tools (list_presentations, list_rooms,
// get_room) and changes them with MCP's own operations (create_folder,
// move_presentation in layout.go), run by Tools.call as the person, as an
// assistant connected to Sliqtly runs them.
//
// The model's calls are a plan (aiPlan), made when it has answered: one
// move and the folder it needs at once, more moves only after the person
// says yes in the drawer (POST /editor/api/ai/organize with the plan's
// steps, src/PresAi.rgr). A folder the plan makes is "new:<name>" until it
// is made; create_folder of a name the topic has already is that folder.

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// One step of a plan: a folder made (create_folder: RoomID, Name) or a
// presentation moved (move_presentation: DeckID, RoomID, FolderID: a folder
// the topic has, "new:<name>" one the plan makes, "" its top). Deck, Room
// and Folder are their names, for the person to read.
type aiPlace struct {
	Tool     string `json:"tool"`
	RoomID   string `json:"room_id"`
	DeckID   string `json:"deck_id,omitempty"`
	FolderID string `json:"folder_id,omitempty"`
	Name     string `json:"name,omitempty"`
	Deck     string `json:"deck,omitempty"`
	Room     string `json:"room,omitempty"`
	Folder   string `json:"folder,omitempty"`
}

// what one step did: the step, and why it was not done ("" it was)
type aiPlaced struct {
	aiPlace
	Error string `json:"error,omitempty"`
}

// a folder the plan makes, by its name
const aiNewFolder = "new:"

// moves made without asking first
const aiMovesUnasked = 1

// the most steps one plan takes
const aiMaxPlaces = 200

// The drawer's changes to where presentations are kept.
var aiPlaceTools = []aiTool{
	{"create_folder", "Make a folder in a topic (room): one level, no folders in folders. A name the topic has already is that folder. Its folder_id is new:<name> (e.g. new:Testing) for move_presentation in this answer. Folders are optional: use one the topic has (get_room) when it fits, make one only when it clearly helps (many presentations of one kind), and ask the person when that is unclear.",
		aiObj(map[string]any{"room_id": aiStr("the topic's room_id (list_rooms; general for General)"), "name": aiStr("the folder's name, at most 100 characters")}, "room_id", "name")},
	{"move_presentation", "Move one presentation to a topic (room), at its top or into one of its folders. Moving more than one presentation is shown to the person to confirm in the drawer before anything moves; one moves when you finish.",
		aiObj(map[string]any{"deck_id": aiStr("the presentation's deck_id (list_presentations)"), "room_id": aiStr("the topic to move it to"), "folder_id": aiStr("a folder of that topic (get_room), new:<name> for one create_folder makes in this answer; leave out for the topic's top")}, "deck_id", "room_id")},
}

// the call as a step; false for a call that is not one, or lacks what it needs
func aiPlaceOf(name string, args map[string]any) (aiPlace, bool) {
	str := func(k string) string { v, _ := args[k].(string); return strings.TrimSpace(v) }
	p := aiPlace{Tool: name, RoomID: str("room_id")}
	switch name {
	case "create_folder":
		p.Name = strings.Join(strings.Fields(str("name")), " ")
		return p, p.RoomID != "" && p.Name != "" && len(p.Name) <= 100
	case "move_presentation":
		p.DeckID, p.FolderID = str("deck_id"), str("folder_id")
		if n, ok := strings.CutPrefix(p.FolderID, aiNewFolder); ok {
			n = strings.Join(strings.Fields(n), " ")
			if n == "" || len(n) > 100 {
				return aiPlace{}, false
			}
			p.FolderID = aiNewFolder + n
		}
		return p, p.RoomID != "" && p.DeckID != ""
	}
	return aiPlace{}, false
}

// the folder a step makes, or a move files into, as one key: the topic and
// the name in any case ("" a move to a folder there is, or to the top)
func (p aiPlace) newFolder() string {
	name := p.Name
	if p.Tool == "move_presentation" {
		n, ok := strings.CutPrefix(p.FolderID, aiNewFolder)
		if !ok {
			return ""
		}
		name = n
	}
	return p.RoomID + "\x00" + strings.ToLower(name)
}

// A plan: the steps in order, a folder once, each presentation's last move.
type aiPlan struct {
	Steps []aiPlace `json:"steps"`
}

func (pl *aiPlan) add(p aiPlace) {
	for i, s := range pl.Steps {
		if s.Tool != p.Tool {
			continue
		}
		if (p.Tool == "create_folder" && s.newFolder() == p.newFolder()) || (p.Tool == "move_presentation" && s.DeckID == p.DeckID) {
			pl.Steps = append(pl.Steps[:i], pl.Steps[i+1:]...)
			break
		}
	}
	if len(pl.Steps) < aiMaxPlaces {
		pl.Steps = append(pl.Steps, p)
	}
}

func (pl aiPlan) moves() int {
	n := 0
	for _, s := range pl.Steps {
		if s.Tool == "move_presentation" {
			n++
		}
	}
	return n
}

// whether the person says yes first: more than one presentation moves
func (pl aiPlan) asks() bool { return pl.moves() > aiMovesUnasked }

// The names the person reads: each presentation's (decks: deck_id → name;
// a move of one not there is left out: not theirs, or no such one), each
// topic's and folder's (rooms: room_id → title, folders: room_id → folder_id
// → name). → the moves left out
func (pl *aiPlan) name(decks, rooms map[string]string, folders map[string]map[string]string) int {
	out := pl.Steps[:0]
	dropped := 0
	for _, s := range pl.Steps {
		if s.Tool == "move_presentation" {
			d, ok := decks[s.DeckID]
			if !ok {
				dropped++
				continue
			}
			s.Deck = d
			if n, ok := strings.CutPrefix(s.FolderID, aiNewFolder); ok {
				s.Folder = n
			} else if s.FolderID != "" {
				s.Folder = folders[s.RoomID][s.FolderID]
			}
		}
		s.Room = rooms[s.RoomID]
		out = append(out, s)
	}
	pl.Steps = out
	return dropped
}

// The plan made, step by step with call (one MCP operation as the person →
// its answer). A folder the plan makes is made before the first move into
// it, the folder_id it answers given to the moves.
func (pl aiPlan) run(call func(op string, args map[string]any) (map[string]any, error)) []aiPlaced {
	made := map[string]string{}
	folder := func(room, name string) (string, error) {
		key := aiPlace{Tool: "create_folder", RoomID: room, Name: name}.newFolder()
		if id, ok := made[key]; ok {
			return id, nil
		}
		out, err := call("create_folder", map[string]any{"room_id": room, "name": name})
		if err != nil {
			return "", err
		}
		id, _ := out["folder_id"].(string)
		made[key] = id
		return id, nil
	}
	var done []aiPlaced
	for _, s := range pl.Steps {
		r := aiPlaced{aiPlace: s}
		var err error
		switch s.Tool {
		case "create_folder":
			_, err = folder(s.RoomID, s.Name)
		case "move_presentation":
			args := map[string]any{"deck_id": s.DeckID, "room_id": s.RoomID}
			fid := s.FolderID
			if n, ok := strings.CutPrefix(fid, aiNewFolder); ok {
				fid, err = folder(s.RoomID, n)
			}
			if err == nil {
				if fid != "" {
					args["folder_id"] = fid
				}
				var out map[string]any
				out, err = call("move_presentation", args)
				if room, _ := out["room"].(string); room != "" {
					r.Room = room
				}
				if f, _ := out["folder"].(string); f != "" {
					r.Folder = f
				}
			}
		}
		if err != nil {
			r.Error = err.Error()
		}
		done = append(done, r)
	}
	return done
}

// ------------------------------------------------------------------ server --

// whether this server keeps where an account's presentations are (sliqtly.com)
func aiPlacesOn(env *Env) bool {
	return env.layouts != nil && (&McpHost{env: env}).HasTool("move_presentation")
}

// One MCP operation run as uid, as the drawer's read tools run (Tools.call:
// the agent gate, the rate limit), → its structured answer.
func aiPlaceCall(env *Env, ctx context.Context, uid string) func(op string, args map[string]any) (map[string]any, error) {
	return func(op string, args map[string]any) (map[string]any, error) {
		h := &McpHost{env: env, ctx: ctx, images: map[int64][]byte{}}
		c := CreateNew_Caller()
		c.signedIn, c.uid, c.who = true, uid, "uid:"+uid
		raw, _ := json.Marshal(args)
		var out struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			Structured map[string]any `json:"structuredContent"`
			IsError    bool           `json:"isError"`
		}
		if err := json.Unmarshal([]byte(JOut_static_text(Tools_static_call(h, c, op, J_static_parse(string(raw))))), &out); err != nil {
			return nil, err
		}
		if out.IsError {
			why := "not done"
			if len(out.Content) > 0 && out.Content[0].Text != "" {
				why = out.Content[0].Text
			}
			return nil, roomErr{why}
		}
		return out.Structured, nil
	}
}

// The plan's steps named for uid (aiPlan.name) from the account's layout.
func aiNamePlan(env *Env, ctx context.Context, uid string, pl *aiPlan) error {
	if env.layouts == nil {
		return nil
	}
	ds, err := env.layouts.decks(ctx, uid, true)
	if err != nil {
		return err
	}
	l, err := env.layouts.ls.read(ctx, uid)
	if err != nil {
		return err
	}
	v := viewOf(l)
	decks := map[string]string{}
	for _, d := range ds {
		decks[d.id] = d.name
	}
	rooms := map[string]string{}
	folders := map[string]map[string]string{}
	for _, s := range pl.Steps {
		rooms[s.RoomID] = v.title(s.RoomID)
		fs := map[string]string{}
		for _, f := range v.folders[s.RoomID] {
			fs[f.ID] = f.Name
		}
		folders[s.RoomID] = fs
	}
	pl.name(decks, rooms, folders)
	return nil
}

// POST /editor/api/ai/organize {steps}: the plan the person said yes to in
// the drawer, made as they would make it. Asks no AI action.
func serveAIOrganize(env *Env, w http.ResponseWriter, r *http.Request, uid string) {
	var in struct {
		Steps []map[string]any `json:"steps"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		editorJSON(w, 400, map[string]string{"error": "The plan could not be read."})
		return
	}
	var pl aiPlan
	for _, s := range in.Steps {
		tool, _ := s["tool"].(string)
		if p, ok := aiPlaceOf(tool, s); ok {
			pl.add(p)
		}
	}
	if len(pl.Steps) == 0 {
		editorJSON(w, 400, map[string]string{"error": "Nothing to move."})
		return
	}
	if err := aiNamePlan(env, r.Context(), uid, &pl); err != nil {
		editorJSON(w, 503, map[string]string{"error": "The topics could not be read; try again in a moment."})
		return
	}
	editorJSON(w, 200, map[string]any{"placed": pl.run(aiPlaceCall(env, r.Context(), uid))})
}
