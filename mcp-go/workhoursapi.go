// SPDX-License-Identifier: AGPL-3.0-or-later

// Export ▸ Sliqtly work hours on a server of one's own: a month's work
// hours estimated from when the decks were saved, and the topics worked on
// each day (the estimate itself: workhours.go). The times are the commits
// of the server's Git repository (gitstore.go: one after each pause in
// editing, on Save version and when an assistant ends its work); a commit's
// changed decks/<id>/ and rooms/<id> say what was worked on, its deck.json
// the deck's topic. Off until turned on in the editor's Settings.
//
//	GET /api/settings/workhours   {enabled, git}
//	PUT /api/settings/workhours   {enabled}
//	GET /api/workhours?month=2026-11&tz=Europe/Helsinki
//	    → the report (workReport), {git: false} without a repository; 403 while off

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/terotests/sliqtly/mcp-go/rgit"
)

// how many commits back a report looks at most
const workScan = 100000

func (s *localServer) workHoursOn(ctx context.Context) bool {
	if s.env.DB == nil {
		return false
	}
	d, err := s.env.DB.Get(ctx, "settings", "workhours")
	if err != nil || d == nil {
		return false
	}
	on, _ := d["enabled"].(bool)
	return on
}

// the repository to read (its own handle: the mirror may be writing), its
// branch head, and the config the commits were made with
func (s *localServer) workRepo() (*rgit.Repo, string, gitConfig, error) {
	if s.gits == nil || s.gits.data == "" {
		return nil, "", gitConfig{}, nil
	}
	cfg, _, err := readGitConfig(s.gits.data)
	if err != nil {
		return nil, "", cfg, err
	}
	dir := filepath.Join(s.gits.data, gitRepoDir)
	if _, err := os.Stat(dir); err != nil {
		return nil, "", cfg, nil
	}
	repo, err := rgit.Open(dir)
	if err != nil {
		return nil, "", cfg, err
	}
	head, err := repo.Ref("refs/heads/" + cfg.Branch)
	return repo, head, cfg, err
}

func (s *localServer) workHoursAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/settings/workhours" {
		switch r.Method {
		case http.MethodGet:
		case http.MethodPut:
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				writeJSON(w, 415, map[string]string{"error": "send JSON"})
				return
			}
			var b struct {
				Enabled bool `json:"enabled"`
			}
			data, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
			if err := json.Unmarshal(data, &b); err != nil {
				writeJSON(w, 400, map[string]string{"error": "not JSON: " + err.Error()})
				return
			}
			if err := s.env.DB.Set(r.Context(), "settings", "workhours", Doc{"enabled": b.Enabled}); err != nil {
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}
		default:
			writeJSON(w, 405, map[string]string{"error": "GET or PUT"})
			return
		}
		repo, _, _, _ := s.workRepo()
		writeJSON(w, 200, map[string]any{"enabled": s.workHoursOn(r.Context()), "git": repo != nil})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]string{"error": "GET only"})
		return
	}
	if !s.workHoursOn(r.Context()) {
		writeJSON(w, 403, map[string]any{"error": "work hours are off: turn them on in Settings", "enabled": false})
		return
	}
	q := r.URL.Query()
	loc := time.Local
	if tz := q.Get("tz"); tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": "no such time zone: " + tz})
			return
		}
		loc = l
	}
	month := q.Get("month")
	if month == "" {
		month = time.Now().In(loc).Format("2006-01")
	}
	from, until, err := workMonth(month, loc)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	repo, head, cfg, err := s.workRepo()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	opt := workOptions{Gap: 30 * time.Minute, Lead: time.Duration(cfg.MaxMinutes) * time.Minute, Loc: loc}
	var events []workEvent
	if repo != nil && head != "" {
		opt.defaults()
		if events, err = gitWorkEvents(repo, head, from.Add(-opt.Gap), until, time.Duration(cfg.IdleSeconds)*time.Second); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
	}
	rep, err := buildWorkReport(events, month, opt)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	out := map[string]any{"git": repo != nil, "gitOn": s.gits.mirror() != nil, "report": rep}
	writeJSON(w, 200, out)
}

// gitWorkEvents: the edits from..until from the branch's commits, newest
// back along first parents. The first commit (the import of what was
// there) is not work. A commit is made idle after the last edit it
// holds, so that is when the edit was.
func gitWorkEvents(repo *rgit.Repo, head string, from, until time.Time, idle time.Duration) ([]workEvent, error) {
	r := workReader{repo: repo, decks: map[string]workDeckInfo{}, rooms: map[string]string{}}
	var out []workEvent
	at := head
	for n := 0; at != "" && n < workScan; n++ {
		c, err := repo.ReadCommit(at)
		if err != nil {
			return out, err
		}
		if len(c.Parents) == 0 {
			break
		}
		when := c.Committer.When.Add(-idle)
		if when.Before(from) {
			break
		}
		parent, err := repo.ReadCommit(c.Parents[0])
		if err != nil {
			return out, err
		}
		if when.Before(until) {
			items, err := r.changes(c.Tree, parent.Tree)
			if err != nil {
				return out, err
			}
			if len(items) > 0 {
				out = append(out, workEvent{At: when, Author: c.Author.Name, Items: items})
			}
		}
		at = c.Parents[0]
	}
	return out, nil
}

type workDeckInfo struct {
	Name string `json:"name"`
	Room string `json:"room"`
}

// workReader reads what commits changed, a deck.json or a room's file
// read once by its blob id
type workReader struct {
	repo  *rgit.Repo
	decks map[string]workDeckInfo // deck.json blob → name, room
	rooms map[string]string       // rooms/<id>.json blob → title
}

// dir: the names directly under tree/dir → their ids
func (r *workReader) dir(tree, dir string) (map[string]string, error) {
	sub, err := r.repo.TreeAt(tree, dir)
	if err != nil || sub == "" {
		return map[string]string{}, err
	}
	es, err := r.repo.List(sub)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(es))
	for _, e := range es {
		out[e.Path] = e.ID
	}
	return out, nil
}

// changes: the decks and rooms that differ between a commit's tree and its
// parent's, each with its topic as the commit has it (a deck gone since:
// as the parent had it)
func (r *workReader) changes(tree, before string) ([]workItem, error) {
	decks, err := r.dir(tree, "decks")
	if err != nil {
		return nil, err
	}
	oldDecks, err := r.dir(before, "decks")
	if err != nil {
		return nil, err
	}
	rooms, err := r.dir(tree, "rooms")
	if err != nil {
		return nil, err
	}
	oldRooms, err := r.dir(before, "rooms")
	if err != nil {
		return nil, err
	}
	var items []workItem
	have := map[string]bool{}
	for _, id := range changedKeys(decks, oldDecks) {
		sub, roomsAt := decks[id], rooms
		if sub == "" {
			sub, roomsAt = oldDecks[id], oldRooms
		}
		info, err := r.deck(sub)
		if err != nil {
			return nil, err
		}
		title, err := r.room(roomsAt[info.Room+".json"])
		if err != nil {
			return nil, err
		}
		items = append(items, workItem{Topic: info.Room, TopicTitle: title, Deck: id, DeckName: info.Name})
		have[info.Room] = true
	}
	// a room's own changes (its title, folders, files): rooms/<id>.json and rooms/<id>/
	for _, name := range changedKeys(rooms, oldRooms) {
		id := strings.TrimSuffix(name, ".json")
		if have[id] {
			continue
		}
		have[id] = true
		blob := rooms[id+".json"]
		if blob == "" {
			blob = oldRooms[id+".json"]
		}
		title, err := r.room(blob)
		if err != nil {
			return nil, err
		}
		items = append(items, workItem{Topic: id, TopicTitle: title})
	}
	return items, nil
}

// changedKeys: the names whose ids differ or that only one has, sorted
func changedKeys(a, b map[string]string) []string {
	var out []string
	for k, v := range a {
		if b[k] != v {
			out = append(out, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// deck: a deck's directory → its name and room, from its deck.json
func (r *workReader) deck(sub string) (workDeckInfo, error) {
	es, err := r.repo.List(sub)
	if err != nil {
		return workDeckInfo{}, err
	}
	for _, e := range es {
		if e.Path != "deck.json" {
			continue
		}
		if info, ok := r.decks[e.ID]; ok {
			return info, nil
		}
		_, b, err := r.repo.Get(e.ID)
		if err != nil {
			return workDeckInfo{}, err
		}
		var info workDeckInfo
		json.Unmarshal(b, &info)
		r.decks[e.ID] = info
		return info, nil
	}
	return workDeckInfo{}, nil
}

// room: rooms/<id>.json's blob → the room's title ("" for none)
func (r *workReader) room(blob string) (string, error) {
	if blob == "" {
		return "", nil
	}
	if t, ok := r.rooms[blob]; ok {
		return t, nil
	}
	kind, b, err := r.repo.Get(blob)
	if err != nil {
		return "", err
	}
	var room struct {
		Title string `json:"title"`
	}
	if kind == "blob" {
		json.Unmarshal(b, &room)
	}
	r.rooms[blob] = room.Title
	return room.Title, nil
}
