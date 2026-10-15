// SPDX-License-Identifier: AGPL-3.0-or-later

// Who is working on a Story now: the assistants calling this server's MCP
// tools on it and the people who have it open in the editor. Nothing is
// asked of an assistant for this: the server watches the calls it gets
// (POST /mcp), so any MCP client shows up as it is plugged in. The page
// reads it with POST /api/rooms/presence ({"deck_ids": [...]}) and shows a
// light and counts on each Story card, and the people and agents in a
// window of their own (web/presence.js).
//
// Kept in memory only, per server: a server of one's own is one process.
// An assistant is known by its MCP session (Mcp-Session-Id): its name is
// the one it gives itself in a tool's "agent" argument, else its client's
// (initialize's clientInfo), else "Assistant". It counts as working on a
// Story while a call on it runs, or for agentWorking after one, or while it
// holds slides there (begin_work until end_work); after that it is idle
// there, and gone from the Story agentGone after its last call.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
)

const (
	agentWorking = 45 * time.Second
	agentGone    = 5 * time.Minute
	// at most this many sessions are remembered; the longest quiet go first
	agentMax = 20000
)

type agentBoard struct {
	mu        sync.Mutex
	agents    map[string]*agentSeen
	lastPrune time.Time
}

type agentSeen struct {
	id     string
	name   string // the name it gave itself in a call
	client string // its MCP client's
	last   time.Time
	decks  map[string]*agentDeck
}

type agentDeck struct {
	first, last time.Time
	calls       int
	busy        int    // calls on it running now
	tool        string // the one running, or the last
	slides      []int  // claimed with begin_work; nil: none
	whole       bool   // claimed the whole deck
	note        string
}

// agentCall: what one tools/call says about who works where
type agentCall struct {
	Tool   string
	Deck   string
	Agent  string
	Note   string
	Slides []int
}

// a Story's assistant, as the page is told
type agentRow struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Client string `json:"client,omitempty"`
	// working or idle
	State string `json:"state"`
	Busy  bool   `json:"busy"`
	Tool  string `json:"tool,omitempty"`
	Calls int    `json:"calls"`
	// seconds since its first call on the Story, and since its last one
	// ended (0 while one runs)
	Since int64 `json:"since_s"`
	Seen  int64 `json:"seen_s"`
	// the slides it holds (begin_work), whole: the whole Story
	Slides []int  `json:"slides,omitempty"`
	Whole  bool   `json:"whole,omitempty"`
	Note   string `json:"note,omitempty"`
}

// sessionAgent: an MCP session's key on the board (not its id: that is the
// session's credential)
func sessionAgent(sid string) string {
	if sid == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("presence " + sid))
	return hex.EncodeToString(sum[:8])
}

func (b *agentBoard) seen(key string, now time.Time) *agentSeen {
	if b.agents == nil {
		b.agents = map[string]*agentSeen{}
	}
	a := b.agents[key]
	if a == nil {
		a = &agentSeen{id: key, decks: map[string]*agentDeck{}}
		b.agents[key] = a
	}
	a.last = now
	return a
}

// hello: a session started, with its client's name
func (b *agentBoard) hello(key, client string, now time.Time) {
	if key == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.seen(key, now)
	if client != "" {
		a.client = clipRunes(client, 60)
	}
	b.prune(now)
}

// begin: a call starts; done tells it ended. A call without a deck only
// keeps the session known.
func (b *agentBoard) begin(key string, c agentCall, now time.Time) (done func(time.Time)) {
	if key == "" {
		return func(time.Time) {}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.seen(key, now)
	if n := clipRunes(strings.TrimSpace(c.Agent), 60); n != "" {
		a.name = n
	}
	b.prune(now)
	if c.Deck == "" {
		return func(time.Time) {}
	}
	d := a.decks[c.Deck]
	if d == nil {
		d = &agentDeck{first: now}
		a.decks[c.Deck] = d
	}
	d.calls++
	d.busy++
	d.tool = c.Tool
	d.last = now
	if c.Tool == "end_work" {
		d.slides, d.whole, d.note = nil, false, ""
	}
	once := false
	return func(end time.Time) {
		b.mu.Lock()
		defer b.mu.Unlock()
		if once {
			return
		}
		once = true
		d.busy--
		d.last = end
		a.last = end
	}
}

// claim: begin_work took these slides (none: the whole deck)
func (b *agentBoard) claim(key string, c agentCall) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.agents[key]
	if a == nil || a.decks[c.Deck] == nil {
		return
	}
	d := a.decks[c.Deck]
	d.slides, d.whole, d.note = append([]int(nil), c.Slides...), len(c.Slides) == 0, clipRunes(strings.TrimSpace(c.Note), 140)
}

// bye: the session ended (its DELETE)
func (b *agentBoard) bye(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.agents, key)
}

// (locked) what has been quiet for agentGone goes, at most every 30 s
func (b *agentBoard) prune(now time.Time) {
	if now.Sub(b.lastPrune) < 30*time.Second && len(b.agents) <= agentMax {
		return
	}
	b.lastPrune = now
	for k, a := range b.agents {
		for id, d := range a.decks {
			if d.busy == 0 && now.Sub(d.last) >= agentGone {
				delete(a.decks, id)
			}
		}
		if len(a.decks) == 0 && now.Sub(a.last) >= agentGone {
			delete(b.agents, k)
		}
	}
	if len(b.agents) > agentMax {
		all := make([]*agentSeen, 0, len(b.agents))
		for _, a := range b.agents {
			all = append(all, a)
		}
		sort.Slice(all, func(i, j int) bool { return all[i].last.Before(all[j].last) })
		for _, a := range all[:len(all)-agentMax] {
			delete(b.agents, a.id)
		}
	}
}

// deck: the assistants on a Story now, working ones first, then by name
func (b *agentBoard) deck(id string, now time.Time) []agentRow {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []agentRow
	for _, a := range b.agents {
		d := a.decks[id]
		if d == nil || (d.busy == 0 && now.Sub(d.last) >= agentGone) {
			continue
		}
		r := agentRow{ID: a.id, Name: a.name, Client: a.client, Busy: d.busy > 0, Tool: d.tool, Calls: d.calls,
			Since: secs(now.Sub(d.first)), Slides: d.slides, Whole: d.whole, Note: d.note}
		if !r.Busy {
			r.Seen = secs(now.Sub(d.last))
		}
		if r.Name == "" {
			r.Name = a.client
		}
		if r.Name == "" {
			r.Name = "Assistant"
		}
		r.State = "idle"
		if r.Busy || now.Sub(d.last) < agentWorking || len(d.slides) > 0 || d.whole {
			r.State = "working"
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].State == "working") != (out[j].State == "working") {
			return out[i].State == "working"
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func secs(d time.Duration) int64 {
	if d < 0 {
		return 0
	}
	return int64(d / time.Second)
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// --- watching POST /mcp

// one JSON-RPC message, as much of it as tells who works where
type rpcPeek struct {
	Method string `json:"method"`
	Params struct {
		Name       string `json:"name"`
		ClientInfo struct {
			Name  string `json:"name"`
			Title string `json:"title"`
		} `json:"clientInfo"`
		Arguments struct {
			DeckID string          `json:"deck_id"`
			Agent  string          `json:"agent"`
			Note   string          `json:"note"`
			Slides json.RawMessage `json:"slides"`
		} `json:"arguments"`
	} `json:"params"`
}

// the messages of a POST body (one, or a batch); none when it is not JSON-RPC
func peekRPC(body []byte) []rpcPeek {
	t := bytes.TrimLeft(body, " \t\r\n")
	if len(t) == 0 {
		return nil
	}
	if t[0] == '[' {
		var all []rpcPeek
		if json.Unmarshal(t, &all) != nil {
			return nil
		}
		return all
	}
	var one rpcPeek
	if json.Unmarshal(t, &one) != nil {
		return nil
	}
	return []rpcPeek{one}
}

// slides as begin_work takes them: numbers, or text such as "3-5, 7"
func peekSlides(raw json.RawMessage) []int {
	if len(raw) == 0 {
		return nil
	}
	var nums []int
	if json.Unmarshal(raw, &nums) == nil {
		return nums
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return nil
	}
	var out []int
	for _, part := range strings.Split(text, ",") {
		lo, hi, ok := strings.Cut(strings.TrimSpace(part), "-")
		a, b := atoiOr(lo, 0), atoiOr(hi, 0)
		if !ok {
			b = a
		}
		for n := a; n >= 1 && n <= b && n <= a+200; n++ {
			out = append(out, n)
		}
	}
	return out
}

func atoiOr(s string, or int) int {
	n := 0
	s = strings.TrimSpace(s)
	if s == "" {
		return or
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return or
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// watchMcp runs serve for an MCP request and tells the board about it. The
// body is read here once and handed on as it was.
func (b *agentBoard) watchMcp(w http.ResponseWriter, r *http.Request, serve func(http.ResponseWriter)) {
	switch r.Method {
	case http.MethodDelete:
		key := sessionAgent(r.Header.Get("Mcp-Session-Id"))
		serve(w)
		if key != "" {
			b.bye(key)
		}
		return
	case http.MethodPost:
	default:
		serve(w)
		return
	}
	body, err := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		serve(w)
		return
	}
	key := sessionAgent(r.Header.Get("Mcp-Session-Id"))
	var dones []func(time.Time)
	var claims []agentCall
	var hello string
	asked := false
	for _, m := range peekRPC(body) {
		switch m.Method {
		case "initialize":
			asked = true
			hello = m.Params.ClientInfo.Title
			if hello == "" {
				hello = m.Params.ClientInfo.Name
			}
		case "tools/call":
			a := m.Params.Arguments
			c := agentCall{Tool: m.Params.Name, Deck: a.DeckID, Agent: a.Agent, Note: a.Note, Slides: peekSlides(a.Slides)}
			dones = append(dones, b.begin(key, c, time.Now()))
			if c.Tool == "begin_work" && c.Deck != "" {
				claims = append(claims, c)
			}
		}
	}
	// begin_work's answer says whether the slides were taken
	var answer *bytes.Buffer
	if len(claims) > 0 {
		answer = &bytes.Buffer{}
		w = teeWriter{w, answer}
	}
	serve(w)
	now := time.Now()
	for _, d := range dones {
		d(now)
	}
	// one claim a request is what assistants send; a batch of several is
	// told by the first answer
	if answer != nil && bytes.Contains(answer.Bytes(), []byte(`"claimed":true`)) {
		b.claim(key, claims[0])
	}
	if asked {
		// a new session's id is in the answer
		if key == "" {
			key = sessionAgent(w.Header().Get("Mcp-Session-Id"))
		}
		b.hello(key, hello, now)
	}
}

// a response writer whose body is also kept
type teeWriter struct {
	http.ResponseWriter
	buf *bytes.Buffer
}

func (t teeWriter) Write(p []byte) (int, error) {
	if t.buf.Len() < 1<<20 {
		t.buf.Write(p)
	}
	return t.ResponseWriter.Write(p)
}

func (t teeWriter) Flush() {
	if f, ok := t.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// --- the page's question: who is on these Stories

// at most this many Stories one question asks about
const presenceDecks = 200

var presenceTools = []roomTool{
	{name: "presence", title: "Who is working on Stories", pageOnly: true, readOnly: true,
		desc: "The assistants and people working on these Stories now.",
		props: map[string]any{
			"deck_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The Stories' deck_ids"},
		},
		required: []string{"deck_ids"}},
}

func isPresenceTool(name string) bool { return name == "presence" }

// a person with the Story open in the editor
type personRow struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
	// the pages they have it open in
	Pages int `json:"pages"`
	// in Edit on one of them (the page's Edit … Save), and how long since
	// their last change there (-1: none)
	Editing bool `json:"editing"`
	EditedS int  `json:"edited_s"`
}

type presenceRow struct {
	DeckID string      `json:"deck_id"`
	Agents []agentRow  `json:"agents"`
	People []personRow `json:"people"`
}

func (s *roomService) runPresence(ctx context.Context, p store.Principal, a map[string]any) (any, error) {
	ids := asStrings(a["deck_ids"])
	if len(ids) > presenceDecks {
		return nil, roomErr{fmt.Sprintf("deck_ids: at most %d", presenceDecks)}
	}
	now := time.Now()
	out := []presenceRow{}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		var row presenceRow
		row.DeckID = id
		if s.agents != nil {
			row.Agents = s.agents.deck(id, now)
		}
		if s.peopleOn != nil {
			row.People = s.peopleOn(id)
		}
		if len(row.Agents) == 0 && len(row.People) == 0 {
			continue
		}
		// only what p may read is told
		if _, err := s.deckFor(ctx, p, id); err != nil {
			if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrDenied) {
				continue
			}
			return nil, err
		}
		if row.Agents == nil {
			row.Agents = []agentRow{}
		}
		if row.People == nil {
			row.People = []personRow{}
		}
		out = append(out, row)
	}
	return map[string]any{"decks": out}, nil
}

// the people with a deck open in the editor (its collab room): one row a
// person, however many pages they have it open in
func (s *localServer) peopleOn(id string) []personRow {
	rm := s.openRoom(id)
	if rm == nil {
		return nil
	}
	rm.mu.Lock()
	defer rm.mu.Unlock()
	by := map[string]*personRow{}
	now := time.Now()
	for _, q := range rm.peers {
		if q.open <= 0 {
			continue
		}
		r := by[q.Who]
		if r == nil {
			sum := sha256.Sum256([]byte("person " + q.Who))
			r = &personRow{ID: hex.EncodeToString(sum[:6]), Name: q.Name, Color: q.Color, EditedS: -1}
			by[q.Who] = r
		}
		r.Pages++
		r.Editing = r.Editing || q.Editing
		if !q.edited.IsZero() {
			if s := int(now.Sub(q.edited).Seconds()); r.EditedS < 0 || s < r.EditedS {
				r.EditedS = s
			}
		}
	}
	out := make([]personRow, 0, len(by))
	for _, r := range by {
		out = append(out, *r)
	}
	// the ones editing first
	sort.Slice(out, func(i, j int) bool {
		if out[i].Editing != out[j].Editing {
			return out[i].Editing
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}
