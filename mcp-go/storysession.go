// SPDX-License-Identifier: AGPL-3.0-or-later

// A Story's session and its forks (docs/story-workspace.md): the visible
// history of the work on a presentation, written by the people and the
// assistants who work on it, and forks that take that history up to one of
// its entries into a Story of their own.
//
// The model is store/session.go; these are its operations, for the
// assistant (MCP tools) and the page (POST /api/rooms/<op>) alike. Reading
// a session is reading its presentation (a role in its room); writing in it
// and forking need Editor there. A fork goes into its parent's room and
// folder and is linked to it (deck:<fork> forked_from deck:<parent>).

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/terotests/sliqtly/mcp-go/store"
)

const storyIdea = "A presentation is a Story: besides its slides it has a session, the visible history of the work on it (what people asked, what the assistants answered, tools called, files and searches used, artifacts made, findings and decisions). Only what was said and done goes in, never hidden reasoning."

var sessionTools = []roomTool{
	{name: "get_session", title: "Read a Story's session", readOnly: true,
		desc: storyIdea + " Read it before working on a Story, to start from the same context as the people and agents before you. A fork's session begins with its parent's entries up to the fork point (origin names the Story each was written in). Each entry: n (its place), kind, who (by: a person or an agent), text, tool, refs. Pages: after (the last n you have) and limit (default 100, at most 500). Also says where the Story was forked from and its forks.",
		props: map[string]any{
			"deck_id": strProp("The presentation's deck_id"),
			"after":   map[string]any{"type": "integer", "description": "Only entries after this n"},
			"limit":   map[string]any{"type": "integer", "description": "How many (default 100, at most 500)"},
		},
		required: []string{"deck_id"}},
	{name: "log_session", title: "Add to a Story's session",
		desc: "Write what you did into a Story's session, so the people and the other agents see where the work came from: the user's prompt (kind prompt), your answer (reply), a tool call (tool, with tool), a search (search), a file read or written (file), something you made (artifact), a decision, a finding, or a note. Keep each entry what a colleague needs to read, not every token; long text goes in a file the entry refers to. refs name what it used or made: file:<path of the deck's file>, deck:<deck_id>, url:https://…, entry:<n> (an earlier entry). Your own changes to the deck (update_presentation, write_workbook, add_comment …) are logged by the server already. → the entries as kept",
		props: map[string]any{
			"deck_id": strProp("The presentation's deck_id"),
			"agent":   strProp("Your name as people see it: \"Claude\", \"Codex\", … (default \"Assistant\")"),
			"entries": map[string]any{"type": "array", "description": "At most 20 entries, in order", "items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"kind": map[string]any{"type": "string", "enum": store.SessionKinds},
					"text": strProp("What was asked, answered, done or decided"),
					"tool": strProp("kind tool: the tool's name"),
					"refs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "kind:id of what it used or made"},
					"by":   strProp("kind prompt: the person who asked, when you know their name"),
				},
				"required": []string{"kind"},
			}},
		},
		required: []string{"deck_id", "entries"}},
	{name: "fork_story", title: "Fork a Story with its context",
		desc: "Fork a Story: a new presentation with the same slides and files, whose session starts with this Story's history up to entry at (default: all of it), to work on in a branch of its own (a security review, a UX proposal) without changing the original. Name it for what the branch is for. It is a draft in the same room and folder, as private as the original; the review comments, hidden files and anything that looks like credentials or keys are not copied. The original's session tells that it was forked. → deck_id of the fork",
		props: map[string]any{
			"deck_id": strProp("The Story to fork"),
			"name":    strProp("What the fork is for: \"security-review\", \"ux-review\" (at most 80 characters)"),
			"at":      map[string]any{"type": "integer", "description": "The session entry (n) to fork at; default the newest"},
			"agent":   strProp("Your name as people see it, for the fork entry (default \"Assistant\")"),
		},
		required: []string{"deck_id", "name"}},
	{name: "list_forks", title: "List a Story's forks", readOnly: true,
		desc:     "The forks of a Story that you may see: each one's deck_id, name, who made it and when, the entry it was forked at and how many entries it has written since.",
		props:    map[string]any{"deck_id": strProp("The presentation's deck_id")},
		required: []string{"deck_id"}},
}

func isSessionTool(name string) bool {
	for _, t := range sessionTools {
		if t.name == name {
			return true
		}
	}
	return false
}

const (
	sessionPage    = 100
	sessionPageMax = 500
	// entries one log_session call writes
	sessionBatch = 20
)

// a Story's session entry as answered: its time also as text
type sessionOut struct {
	store.SessionEntry
	Time string `json:"time"`
}

func outEntries(es []store.SessionEntry) []sessionOut {
	out := make([]sessionOut, 0, len(es))
	for _, e := range es {
		out = append(out, sessionOut{e, time.UnixMilli(e.At).UTC().Format(time.RFC3339)})
	}
	return out
}

// a fork as list_forks and get_session tell it
type forkRow struct {
	DeckID string `json:"deck_id"`
	Name   string `json:"name"`
	By     string `json:"by,omitempty"`
	Made   int64  `json:"made,omitempty"`
	At     int64  `json:"at"`
	// entries it has written itself
	Since int    `json:"entries_since"`
	Phase string `json:"phase"`
}

// the deck p may read, by its id
func (s *roomService) deckFor(ctx context.Context, p store.Principal, id string) (store.Doc, error) {
	if id == "" {
		return nil, roomErr{"deck_id is missing"}
	}
	d, _, err := s.st.Get(ctx, p, "shares", id)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, store.ErrNotFound
	}
	return d, nil
}

// p may write in the deck's room
func (s *roomService) deckWritable(p store.Principal, d store.Doc) error {
	room, _ := d[store.RoomField].(string)
	if !p.Rooms[room].AtLeast(store.Editor) {
		return roomErr{"this presentation's room is read only for you (archived, or your role is viewer)"}
	}
	return nil
}

// what p reads of a Story's fork: none for a Story p may not see
func (s *roomService) forkReader(p store.Principal) store.ForkReader {
	return func(ctx context.Context, deck string) (store.Fork, error) {
		d, err := s.deckFor(ctx, p, deck)
		if err != nil {
			return store.Fork{}, err
		}
		return store.ForkOf(d), nil
	}
}

func (s *roomService) sessions() store.Sessions { return store.Sessions{Log: s.chat} }

// the session's author: the page's person or the assistant's robot
func sessionSpeaker(p store.Principal, via string, a map[string]any) (store.ChatFrom, error) {
	if via == viaPage {
		return chatSpeaker(p, via, a)
	}
	return chatSpeaker(p, viaMcp, map[string]any{"agent": a["agent"]})
}

func (s *roomService) runSession(ctx context.Context, p store.Principal, via, op string, a map[string]any) (any, error) {
	if s.chat == nil {
		return nil, roomErr{"this server keeps no sessions"}
	}
	id := argStr(a, "deck_id")
	d, err := s.deckFor(ctx, p, id)
	if err != nil {
		return nil, err
	}
	switch op {
	case "get_session":
		tl, err := s.sessions().Timeline(ctx, p.TenantID, id, s.forkReader(p))
		if err != nil {
			return nil, err
		}
		after, limit := argInt(a, "after"), argInt(a, "limit")
		if limit <= 0 {
			limit = sessionPage
		}
		limit = min(limit, sessionPageMax)
		from := min(max(after, 0), int64(len(tl)))
		to := min(from+limit, int64(len(tl)))
		forks, err := s.forksOf(ctx, p, id)
		if err != nil {
			return nil, err
		}
		name, _ := d["name"].(string)
		out := map[string]any{"deck_id": id, "name": name, "total": len(tl), "entries": outEntries(tl[from:to]), "forks": forks}
		if f := store.ForkOf(d); f.IsFork() {
			out["forked_from"] = map[string]any{"deck_id": f.From, "at": f.At, "name": f.Name}
		}
		if int(to) < len(tl) {
			out["next_after"] = to
		}
		return out, nil

	case "log_session":
		if err := s.deckWritable(p, d); err != nil {
			return nil, err
		}
		list, _ := a["entries"].([]any)
		if len(list) == 0 || len(list) > sessionBatch {
			return nil, roomErr{fmt.Sprintf("entries: 1 to %d entries", sessionBatch)}
		}
		by, err := sessionSpeaker(p, via, a)
		if err != nil {
			return nil, err
		}
		// all are checked before any is written
		var es []store.SessionEntry
		for i, v := range list {
			m, _ := v.(map[string]any)
			e := store.SessionEntry{By: by, Kind: argStr(m, "kind"), Text: argStr(m, "text"), Tool: argStr(m, "tool")}
			for _, r := range asStrings(m["refs"]) {
				e.Refs = append(e.Refs, r)
			}
			// a prompt is the person's who asked, told by the agent
			if who := cleanName(m["by"]); who != "" && e.Kind == store.KindPrompt && via == viaMcp {
				e.By = store.ChatFrom{ID: "p-" + slugOf(who), User: p.UserID, Name: who, Kind: "person"}
			}
			c, err := store.CleanEntry(e)
			if err != nil {
				return nil, roomErr{fmt.Sprintf("entries[%d]: %v", i, err)}
			}
			es = append(es, c)
		}
		var kept []store.SessionEntry
		for _, e := range es {
			k, err := s.sessions().Append(ctx, p.TenantID, id, e, false)
			if err != nil {
				return nil, err
			}
			kept = append(kept, k)
		}
		s.tellSession(d, id, kept)
		return map[string]any{"deck_id": id, "entries": outEntries(kept)}, nil

	case "fork_story":
		return s.forkStory(ctx, p, via, id, d, a)

	case "list_forks":
		forks, err := s.forksOf(ctx, p, id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"deck_id": id, "forks": forks}, nil
	}
	return nil, roomErr{"no such operation: " + op}
}

func asStrings(v any) []string {
	var out []string
	switch l := v.(type) {
	case []any:
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case []string:
		out = l
	}
	return out
}

func slugOf(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "person"
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// the pages showing the deck's room hear of new entries
func (s *roomService) tellSession(d store.Doc, deck string, es []store.SessionEntry) {
	room, _ := d[store.RoomField].(string)
	s.tell(room, map[string]any{"t": "session", "deck": deck, "entries": outEntries(es)})
}

// the forks of deck p may see, oldest first
func (s *roomService) forksOf(ctx context.Context, p store.Principal, deck string) ([]forkRow, error) {
	ls, err := s.links.Of(ctx, p, store.Ref{Kind: "deck", ID: deck})
	if err != nil {
		return nil, err
	}
	out := []forkRow{}
	for _, l := range ls {
		if l.Rel != "forks" || l.To.Kind != "deck" {
			continue
		}
		fd, err := s.deckFor(ctx, p, l.To.ID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		f := store.ForkOf(fd)
		if f.From != deck {
			continue
		}
		last, err := s.chat.Last(ctx, p.TenantID, store.SessionRoom(l.To.ID))
		if err != nil {
			return nil, err
		}
		out = append(out, forkRow{DeckID: l.To.ID, Name: f.Name, By: f.By, Made: f.Made, At: f.At, Since: int(last), Phase: store.StoryOf(fd).Phase})
	}
	sortForks(out)
	return out, nil
}

func sortForks(fs []forkRow) {
	for i := 1; i < len(fs); i++ {
		for j := i; j > 0 && (fs[j].Made < fs[j-1].Made || (fs[j].Made == fs[j-1].Made && fs[j].DeckID < fs[j-1].DeckID)); j-- {
			fs[j], fs[j-1] = fs[j-1], fs[j]
		}
	}
}

// fork_story: the plan (store.PlanFork), the files copied, the share
// written, the link made and the parent's session told
func (s *roomService) forkStory(ctx context.Context, p store.Principal, via, id string, d store.Doc, a map[string]any) (any, error) {
	if err := s.deckWritable(p, d); err != nil {
		return nil, err
	}
	name := store.CleanForkName(argStr(a, "name"))
	if name == "" {
		return nil, roomErr{"name: what the fork is for, e.g. security-review"}
	}
	tl, err := s.sessions().Timeline(ctx, p.TenantID, id, s.forkReader(p))
	if err != nil {
		return nil, err
	}
	at := int64(len(tl))
	if _, given := a["at"]; given {
		at = argInt(a, "at")
		if at < 0 || at > int64(len(tl)) {
			return nil, roomErr{fmt.Sprintf("at is an entry of the session, 0 to %d", len(tl))}
		}
	}
	by, err := sessionSpeaker(p, via, a)
	if err != nil {
		return nil, err
	}
	plan := store.PlanFork(d, id, at, name, p.UserID, s.now())
	plan.Doc["tenant"] = p.TenantID
	fid := newDeckID()
	files, err := s.copyFiles(ctx, id, fid, plan.Files)
	if err != nil {
		return nil, err
	}
	plan.Doc["files"] = files
	if _, err := s.st.Put(ctx, p, "shares", fid, plan.Doc, 0); err != nil {
		return nil, err
	}
	if _, _, err := s.links.Add(ctx, p, store.Link{From: store.Ref{Kind: "deck", ID: fid}, Rel: "forked_from", To: store.Ref{Kind: "deck", ID: id}}, nil); err != nil {
		return nil, err
	}
	what := fmt.Sprintf("%s forked this Story as %q", by.Name, name)
	if at < int64(len(tl)) {
		what += fmt.Sprintf(" at entry %d", at)
	}
	e, err := s.sessions().Append(ctx, p.TenantID, id, store.SessionEntry{Kind: store.KindFork, By: by, Text: what, Refs: []string{"deck:" + fid}}, true)
	if err != nil {
		return nil, err
	}
	s.tellSession(d, id, []store.SessionEntry{e})
	return map[string]any{"deck_id": fid, "name": plan.Doc["name"], "forked_from": id, "at": at, "skipped_files": nonNil(plan.Skipped)}, nil
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// a new deck's id: ten letters and digits, as the editor's shortId()
func newDeckID() string { return (&McpHost{}).ShortID(10) }

// the files of deck from copied to deck to, as to's share lists them
func (s *roomService) copyFiles(ctx context.Context, from, to string, files []store.Doc) ([]any, error) {
	out := []any{}
	if len(files) == 0 {
		return out, nil
	}
	if s.bucket == nil {
		return nil, roomErr{"this server keeps no files to copy"}
	}
	for _, f := range files {
		p, _ := f["path"].(string)
		b, err := s.bucket.Read(ctx, "shares/"+from+"/"+p, maxFileRead)
		if err != nil {
			return nil, fmt.Errorf("copying %s: %w", p, err)
		}
		typ, _ := f["type"].(string)
		token := (&McpHost{}).UUID()
		name := "shares/" + to + "/" + p
		if err := s.bucket.Save(ctx, name, typ, b, map[string]string{"firebaseStorageDownloadTokens": token}); err != nil {
			return nil, err
		}
		c := store.Doc{}
		for k, v := range f {
			c[k] = v
		}
		c["url"] = s.shareURL(name, token)
		out = append(out, c)
	}
	return out, nil
}

// the words a tool entry keeps of the tool's answer: its first line
const toolSummaryMax = 300

// SessionTool writes a deck tool an assistant ran (its arguments and answer
// as JSON) into the deck's Story session, as a tool entry by the agent
// (the call's agent or author, else "Assistant"). It never fails the call:
// what goes wrong is logged.
func (h *McpHost) SessionTool(uid, name, args, answer string) {
	rs := h.env.rooms
	if rs == nil || rs.chat == nil || uid == "" {
		return
	}
	var a, r map[string]any
	json.Unmarshal([]byte(args), &a)
	json.Unmarshal([]byte(answer), &r)
	deck := argStr(a, "deck_id")
	if deck == "" {
		sc, _ := r["structuredContent"].(map[string]any)
		deck, _ = sc["deck_id"].(string)
	}
	if deck == "" {
		return
	}
	if err := rs.logTool(h.ctx, uid, name, deck, a, toolSummary(r)); err != nil && !errors.Is(err, store.ErrNotFound) {
		log.Printf("session of %s: %v", deck, err)
	}
}

// the first line of a tool's answer, at most toolSummaryMax runes
func toolSummary(r map[string]any) string {
	content, _ := r["content"].([]any)
	for _, c := range content {
		m, _ := c.(map[string]any)
		if t, _ := m["text"].(string); t != "" {
			line, _, _ := strings.Cut(strings.TrimSpace(t), "\n")
			if utf8.RuneCountInString(line) > toolSummaryMax {
				line = string([]rune(line)[:toolSummaryMax-1]) + "…"
			}
			return line
		}
	}
	return ""
}

func (s *roomService) logTool(ctx context.Context, uid, name, deck string, a map[string]any, text string) error {
	p, err := s.rooms.For(ctx, s.localPrincipal(uid))
	if err != nil {
		return err
	}
	d, err := s.deckFor(ctx, p, deck)
	if err != nil {
		return err
	}
	agent := a["agent"]
	if cleanName(agent) == "" {
		agent = a["author"]
	}
	by, _ := chatSpeaker(p, viaMcp, map[string]any{"agent": agent})
	refs := []string{"deck:" + deck}
	if path := argStr(a, "path"); path != "" && !strings.ContainsAny(path, " \t\n") {
		refs = append(refs, "file:"+path)
	}
	e, err := s.sessions().Append(ctx, p.TenantID, deck, store.SessionEntry{Kind: store.KindTool, Tool: name, Text: text, Refs: refs, By: by}, false)
	if err != nil {
		return err
	}
	s.tellSession(d, deck, []store.SessionEntry{e})
	return nil
}
