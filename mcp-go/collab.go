// SPDX-License-Identifier: AGPL-3.0-or-later

// Editing a deck together on a server of one's own: everyone with the deck
// open sees the others' edits as they make them, where their carets are,
// and a chat beside the deck.
//
// One room per open deck holds its text and every edit taken (rev 1, 2, …).
// A page sends its edit as a text delta on the revision it had (RangerDiff's
// RdOt, the page's own code compiled to Go: rdiff/ot.go); one made on an
// older revision is transformed over the edits taken since, the
// way a central OT server does, so nobody's send is refused. Every edit goes
// out to everyone, its sender too (that is the sender's acknowledgement), in
// one order on the event stream:
//
//	GET  /api/collab/{id}          {rev, md, peers, chat}
//	POST /api/collab/{id}/op       {client, rev, ops, seq}  -> {rev}
//	POST /api/collab/{id}/presence {client, rev, caret, anchor, who, name, color}
//	POST /api/collab/{id}/chat     {client, text}           -> the message
//	POST /api/collab/{id}/call     {client, op, …}          (meet.go)
//	GET  /api/socket?room={id}&client=…&who=…&name=…&color=…&rev=…
//	GET  /api/events?room=…  (the same as Server-Sent Events)
//
//	id: 12
//	data: {"t":"op","rev":12,"client":"…","ops":[…]}
//	data: {"t":"cursor","client":"…","rev":12,"caret":40,"anchor":40}
//	data: {"t":"peers","rev":12,"peers":[…]}
//	data: {"t":"chat","msg":{…}}
//	data: {"t":"reset"}                      (catch up from GET /api/collab)
//
// A stream that drops reconnects with the last rev it had (rev=, or the
// Last-Event-ID of Server-Sent Events) and gets the edits it missed; a page
// whose stream is gone has left. The stream is the page's one stream to the
// server (localevents.go), which also says when decks change and what state
// the server is in. The room writes the deck's
// md shortly after edits stop; a write from elsewhere (an assistant's
// update_presentation, a page not in the room) comes in as an edit of its
// own, merged over what was typed meanwhile. The chat is kept beside the
// deck's files, in shares/{id}/.collab/chat.jsonl.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/terotests/sliqtly/mcp-go/rdiff"
	"github.com/terotests/sliqtly/mcp-go/store"
)

// the room's text is UTF-16 code units, as the page's and a delta's counts
func toU16(s string) []uint16   { return utf16.Encode([]rune(s)) }
func fromU16(u []uint16) string { return string(utf16.Decode(u)) }

// a JSON number that is a count (decoded with decodeValue or plain)
func count(v any) (int, bool) {
	switch x := v.(type) {
	case int64:
		return int(x), x >= 0
	case float64:
		return int(x), x >= 0 && x == float64(int(x))
	case int:
		return x, x >= 0
	}
	return 0, false
}

const (
	collabLogKeep  = 5000    // edits kept for late senders and reconnects
	collabQueueMax = 20000   // events waiting for one slow page before it is cut off
	collabMaxLen   = 4 << 20 // a deck's text, in UTF-16 units
	collabChatKeep = 500     // messages a page is given
	collabChatMax  = 2000    // characters in one message
	collabSaveWait = 400 * time.Millisecond
	collabSaveMost = 3 * time.Second
	collabIdle     = 5 * time.Minute
)

var (
	collabPath = regexp.MustCompile(`^/api/collab/([A-Za-z0-9]{6,32})(/op|/presence|/chat|/call)?$`)
	clientID   = regexp.MustCompile(`^[A-Za-z0-9_-]{4,64}$`)
	colorHex   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

type collabEntry struct {
	rev    int
	client string
	d      rdiff.Delta
}

type collabPeer struct {
	Client string `json:"client"`
	Who    string `json:"who"`
	Name   string `json:"name"`
	Color  string `json:"color"`
	Caret  int    `json:"caret"`
	Anchor int    `json:"anchor"`
	open   int    // its open streams
}

type collabChat struct {
	ID    string `json:"id"`
	Who   string `json:"who"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Text  string `json:"text"`
	At    int64  `json:"at"`
}

// one room event: its JSON, and the rev it carries as the stream's event id
// (0: none), which a page that reconnects names to get what it missed
type collabEvt struct {
	id   int
	data []byte
}

func newEvt(v any, id int) collabEvt {
	b, _ := json.Marshal(v)
	return collabEvt{id: id, data: b}
}

// as Server-Sent Events have it
func (e collabEvt) sse() []byte {
	if e.id > 0 {
		return []byte("id: " + strconv.Itoa(e.id) + "\ndata: " + string(e.data) + "\n\n")
	}
	return []byte("data: " + string(e.data) + "\n\n")
}

// one page's stream: events wait here, never dropped; a page that falls too
// far behind is cut off and comes back with the last rev it had
type collabSub struct {
	mu   sync.Mutex
	q    []collabEvt
	over bool
	wake chan struct{}
}

func (c *collabSub) push(b collabEvt) {
	c.mu.Lock()
	if len(c.q) >= collabQueueMax {
		c.over = true
	} else {
		c.q = append(c.q, b)
	}
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *collabSub) take() ([]collabEvt, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	q := c.q
	c.q = nil
	return q, c.over
}

type collabRoom struct {
	s    *localServer
	id   string
	load sync.Once
	mu   sync.Mutex
	// one chat message at a time, held while its line is written
	chatMu sync.Mutex
	// this run of the room: revs count from 0 in each, so a page that comes
	// back from another run (the server restarted, or the room was left
	// empty and opened again) reads the deck again rather than taking this
	// run's edits as the ones after its own rev
	epoch string

	ready   bool
	text    []uint16
	rev     int
	log     []collabEntry // revs logFrom+1 … rev
	logFrom int
	peers   map[string]*collabPeer
	// each page's last edit taken (its "seq"): one sent again after a lost
	// answer is not taken twice
	seqs map[string]int
	subs map[*collabSub]string // each stream's page
	chat []collabChat
	// the call of the people here (meet.go), nil while nobody talks
	call *roomCall

	// what the deck's file holds: fileMd, which was the text at fileRev
	// once fileGap's edits are applied to it (writes from elsewhere leave
	// such a gap until the room writes the file again)
	fileMd  []uint16
	fileRev int
	fileGap []rdiff.Delta
	// the deck's revision the room has taken in: a change the store tells
	// that is not newer is one the room has read already
	docRev store.Rev
	// the room's own writes not yet told back by the store, oldest first;
	// saveMu keeps one write at a time, so a flush waits for a timer's
	// write rather than passing it
	pending []*collabSave
	saveMu  sync.Mutex

	dirty     bool
	saveTimer *time.Timer
	dirtyAt   time.Time
	idleTimer *time.Timer
}

// one write of the room's: its text, which was the room's at rev
type collabSave struct {
	text []uint16
	rev  int
}

type collabRooms struct {
	mu    sync.Mutex
	rooms map[string]*collabRoom
}

func newCollabRooms() *collabRooms { return &collabRooms{rooms: map[string]*collabRoom{}} }

// the open room of a deck, or nil
func (s *localServer) openRoom(id string) *collabRoom {
	if s.collab == nil {
		return nil
	}
	s.collab.mu.Lock()
	defer s.collab.mu.Unlock()
	return s.collab.rooms[id]
}

// the deck's room, opened (and read from the folder) when it is not
func (s *localServer) room(ctx context.Context, id string) (*collabRoom, error) {
	if s.collab == nil {
		return nil, fail(404, "", "no shared editing here")
	}
	if _, err := s.own(ctx, id); err != nil {
		return nil, err
	}
	s.collab.mu.Lock()
	rm := s.collab.rooms[id]
	if rm == nil {
		rm = &collabRoom{s: s, id: id, epoch: newEpoch(), peers: map[string]*collabPeer{}, seqs: map[string]int{}, subs: map[*collabSub]string{}}
		s.collab.rooms[id] = rm
	}
	s.collab.mu.Unlock()
	rm.load.Do(func() {
		// read without the room locked: a write meanwhile (its callback
		// sees the room not ready) is the newer text, and wins
		d, rev, _ := s.env.Store.Get(ctx, "shares", id)
		md, _ := d["md"].(string)
		chat := s.readChat(id)
		rm.mu.Lock()
		if !rm.ready || rev > rm.docRev {
			rm.text = toU16(md)
			rm.fileMd = rm.text
			rm.docRev = rev
		}
		rm.chat = chat
		rm.ready = true
		rm.mu.Unlock()
	})
	return rm, nil
}

// the store tells every write, in the order made (local.go)
func (s *localServer) collabWritten(c store.Change) {
	if c.Col != "shares" {
		return
	}
	rm := s.openRoom(c.ID)
	if rm == nil {
		return
	}
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if c.Doc == nil {
		rm.docRev, rm.pending = 0, nil
		rm.broadcast(map[string]any{"t": "reset"}, 0)
		return
	}
	if c.Rev <= rm.docRev {
		return
	}
	rm.docRev = c.Rev
	md, _ := c.Doc["md"].(string)
	got := toU16(md)
	if !rm.ready {
		rm.text = got
		rm.fileMd = got
		rm.ready = true
		return
	}
	if len(rm.pending) > 0 && sameU16(got, rm.pending[0].text) {
		// the room's own write
		w := rm.pending[0]
		rm.pending = rm.pending[1:]
		rm.fileMd, rm.fileRev, rm.fileGap = w.text, w.rev, nil
		return
	}
	if sameU16(got, rm.fileMd) {
		return
	}
	// a write from elsewhere, made on what the file held: an edit of that,
	// carried over what was typed since
	x := rdiff.TextDiff(rm.fileMd, got)
	var gap []rdiff.Delta
	if rm.fileRev < rm.logFrom {
		x, gap = rdiff.TextDiff(rm.text, got), nil
	} else {
		for _, g := range rm.fileGap {
			var g1 rdiff.Delta
			x, g1 = rdiff.Transform(x, g)
			gap = append(gap, g1)
		}
		for _, e := range rm.log[rm.fileRev-rm.logFrom:] {
			var e1 rdiff.Delta
			x, e1 = rdiff.Transform(x, e.d)
			gap = append(gap, e1)
		}
	}
	if !x.Noop() {
		if err := rm.take(x, "server"); err != nil {
			// cannot be: x is on the text; read it again rather than guess
			rm.text = got
			gap = nil
			rm.broadcast(map[string]any{"t": "reset"}, 0)
		}
	}
	rm.fileMd, rm.fileRev, rm.fileGap = got, rm.rev, gap
}

// a room run's name: random, so two runs of one room never share it
func newEpoch() string {
	return strconv.FormatUint(rand.Uint64(), 36)
}

func sameU16(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// an edit on the current text: applied, logged, carets moved, sent to all
// (locked)
func (rm *collabRoom) take(d rdiff.Delta, client string) error {
	out, err := d.Apply(rm.text)
	if err != nil {
		return err
	}
	if len(out) > collabMaxLen {
		return fail(413, "too-long", "the presentation would be too long")
	}
	rm.text = out
	rm.rev++
	rm.log = append(rm.log, collabEntry{rm.rev, client, d})
	if len(rm.log) > collabLogKeep {
		cut := len(rm.log) - collabLogKeep
		rm.log = append([]collabEntry(nil), rm.log[cut:]...)
		rm.logFrom += cut
	}
	for _, p := range rm.peers {
		own := p.Client == client
		p.Caret = d.TransformIndex(p.Caret, own)
		p.Anchor = d.TransformIndex(p.Anchor, own)
	}
	rm.broadcast(opEvent(rm.rev, client, d), rm.rev)
	rm.changed()
	return nil
}

func opEvent(rev int, client string, d rdiff.Delta) map[string]any {
	return map[string]any{"t": "op", "rev": rev, "client": client, "ops": d.JSON()}
}

// (locked)
func (rm *collabRoom) broadcast(v any, id int) {
	b := newEvt(v, id)
	for c := range rm.subs {
		c.push(b)
	}
}

// (locked) to one page's streams only
func (rm *collabRoom) sendTo(client string, v any) {
	b := newEvt(v, 0)
	for c, cl := range rm.subs {
		if cl == client {
			c.push(b)
		}
	}
}

func (rm *collabRoom) peerList() []*collabPeer {
	out := make([]*collabPeer, 0, len(rm.peers))
	for _, p := range rm.peers {
		out = append(out, p)
	}
	return out
}

// (locked)
func (rm *collabRoom) sendPeers() {
	rm.broadcast(map[string]any{"t": "peers", "rev": rm.rev, "peers": rm.peerList()}, 0)
}

// --- writing the deck

// (locked) the file is written once edits pause, and at least every few
// seconds while they do not
func (rm *collabRoom) changed() {
	now := time.Now()
	if !rm.dirty {
		rm.dirty = true
		rm.dirtyAt = now
	}
	wait := collabSaveWait
	if most := rm.dirtyAt.Add(collabSaveMost).Sub(now); most < wait {
		wait = max(most, 0)
	}
	if rm.saveTimer == nil {
		rm.saveTimer = time.AfterFunc(wait, rm.save)
	} else {
		rm.saveTimer.Reset(wait)
	}
}

func (rm *collabRoom) save() {
	rm.saveMu.Lock()
	defer rm.saveMu.Unlock()
	rm.mu.Lock()
	if !rm.dirty {
		rm.mu.Unlock()
		return
	}
	w := &collabSave{text: append([]uint16(nil), rm.text...), rev: rm.rev}
	rm.pending = append(rm.pending, w)
	rm.dirty = false
	md := fromU16(w.text)
	rm.mu.Unlock()
	unlock := shareLocks.lock(rm.id)
	err := rm.s.env.DB.Update(context.Background(), "shares", rm.id, Doc{"md": md, "updated": time.Now().UTC()})
	unlock()
	again := false
	if err != nil {
		fmt.Fprintf(os.Stderr, "sliqtly: writing %s: %v\n", rm.id, err)
		// kept to be written again, unless the deck is gone
		d, gerr := rm.s.env.DB.Get(context.Background(), "shares", rm.id)
		again = gerr != nil || d != nil
	}
	rm.mu.Lock()
	if err != nil {
		// not written: nothing will be told of it
		for i, p := range rm.pending {
			if p == w {
				rm.pending = append(rm.pending[:i:i], rm.pending[i+1:]...)
				break
			}
		}
	}
	if again {
		rm.changed()
	}
	rm.mu.Unlock()
}

// flushes the room's text to the folder now (for tests and shutdown)
func (rm *collabRoom) flush() {
	rm.mu.Lock()
	if rm.saveTimer != nil {
		rm.saveTimer.Stop()
	}
	rm.mu.Unlock()
	rm.save()
}

// writes every open room's text to the folder now, at shutdown: the edits
// the rooms took in their last moments are not lost with the process
func (s *localServer) flushRooms() {
	if s.collab == nil {
		return
	}
	s.collab.mu.Lock()
	rooms := make([]*collabRoom, 0, len(s.collab.rooms))
	for _, rm := range s.collab.rooms {
		rooms = append(rooms, rm)
	}
	s.collab.mu.Unlock()
	for _, rm := range rooms {
		rm.flush()
	}
}

// --- the chat file

// a room's chat: one JSON line per message, kept as the deck's file
// .collab/chat.jsonl (localBucket lines)
func chatPath(id string) string { return "shares/" + id + "/.collab/chat.jsonl" }

func (s *localServer) readChat(id string) []collabChat {
	lines, err := s.bucket.Lines(chatPath(id), collabChatKeep)
	if err != nil {
		return nil
	}
	var out []collabChat
	for _, l := range lines {
		var m collabChat
		if json.Unmarshal([]byte(l), &m) == nil && m.ID != "" {
			out = append(out, m)
		}
	}
	return out
}

func (s *localServer) appendChat(id string, m collabChat) error {
	b, _ := json.Marshal(m)
	return s.bucket.AppendLine(chatPath(id), string(b))
}

// --- the API

func (s *localServer) collabAPI(r *http.Request, id, what string) (any, error) {
	rm, err := s.room(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if what == "" {
		if r.Method != http.MethodGet {
			return nil, fail(405, "", "method not allowed")
		}
		rm.mu.Lock()
		defer rm.mu.Unlock()
		chat := rm.chat
		if chat == nil {
			chat = []collabChat{}
		}
		return map[string]any{"epoch": rm.epoch, "rev": rm.rev, "md": fromU16(rm.text), "peers": rm.peerList(), "chat": chat, "call": rm.callRows()}, nil
	}
	if r.Method != http.MethodPost {
		return nil, fail(405, "", "method not allowed")
	}
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	client, _ := body["client"].(string)
	if !clientID.MatchString(client) {
		return nil, fail(400, "", "client: a page's id is expected")
	}
	switch what {
	case "/op":
		return rm.submit(body, client)
	case "/presence":
		return rm.presence(body, client)
	case "/call":
		return rm.callOp(body, client)
	}
	return rm.say(body, client)
}

// an edit made on revision `rev`
func (rm *collabRoom) submit(body map[string]any, client string) (any, error) {
	at, ok := count(body["rev"])
	if !ok {
		return nil, fail(400, "", "rev: a revision is expected")
	}
	d, err := rdiff.ParseDelta(body["ops"])
	if err != nil {
		return nil, fail(400, "", err.Error())
	}
	seq, hasSeq := count(body["seq"])
	epoch, _ := body["epoch"].(string)
	rm.mu.Lock()
	defer rm.mu.Unlock()
	// made on another run's rev: the page reads the deck again
	if epoch != "" && epoch != rm.epoch {
		return nil, fail(409, "reset", "the room was opened again: read the presentation again")
	}
	if hasSeq && seq > 0 && seq <= rm.seqs[client] {
		return map[string]any{"rev": rm.rev, "again": true}, nil
	}
	if at > rm.rev {
		return nil, fail(409, "ahead", "that revision is not here yet")
	}
	if at < rm.logFrom {
		return nil, fail(409, "too-old", "too far behind: read the presentation again")
	}
	for _, e := range rm.log[at-rm.logFrom:] {
		d, _ = rdiff.Transform(d, e.d)
	}
	if err := rm.take(d, client); err != nil {
		var ae *apiError
		if !errors.As(err, &ae) {
			err = fail(409, "does-not-apply", err.Error())
		}
		return nil, err
	}
	if hasSeq {
		rm.seqs[client] = seq
	}
	return map[string]any{"rev": rm.rev}, nil
}

func cleanName(v any) string {
	s, _ := v.(string)
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s))
	if utf8.RuneCountInString(s) > 40 {
		s = string([]rune(s)[:40])
	}
	return s
}

// who the page is (name, colour) and where its caret is, on revision `rev`
func (rm *collabRoom) presence(body map[string]any, client string) (any, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	p := rm.peers[client]
	if p == nil {
		return nil, fail(409, "not-here", "open the room's stream first")
	}
	named := false
	if n := cleanName(body["name"]); n != "" && n != p.Name {
		p.Name, named = rm.freeName(p.Who, n), true
	}
	if c, _ := body["color"].(string); colorHex.MatchString(c) && c != p.Color {
		p.Color, named = rm.freeColor(p.Who, c), true
	}
	at, ok := count(body["rev"])
	caret, okc := count(body["caret"])
	anchor, oka := count(body["anchor"])
	if ok && okc && at <= rm.rev && at >= rm.logFrom {
		if !oka {
			anchor = caret
		}
		for _, e := range rm.log[at-rm.logFrom:] {
			caret = e.d.TransformIndex(caret, false)
			anchor = e.d.TransformIndex(anchor, false)
		}
		p.Caret, p.Anchor = min(caret, len(rm.text)), min(anchor, len(rm.text))
		if !named {
			rm.broadcast(map[string]any{"t": "cursor", "rev": rm.rev, "client": client, "caret": p.Caret, "anchor": p.Anchor}, 0)
		}
	}
	if named {
		for _, q := range rm.peers {
			if q.Who == p.Who {
				q.Name, q.Color = p.Name, p.Color
			}
		}
		rm.sendPeers()
		rm.callRename(p.Who, p.Name, p.Color)
	}
	return map[string]int{"rev": rm.rev}, nil
}

var chatSeq struct {
	sync.Mutex
	n int64
}

func (rm *collabRoom) say(body map[string]any, client string) (any, error) {
	text, _ := body["text"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fail(400, "", "text: a message is expected")
	}
	if utf8.RuneCountInString(text) > collabChatMax {
		return nil, fail(413, "", "the message is too long")
	}
	// one message at a time, in the file and the room in the same order;
	// the room itself (edits, presence, streams) is not held while the
	// line is written
	rm.chatMu.Lock()
	defer rm.chatMu.Unlock()
	rm.mu.Lock()
	p := rm.peers[client]
	var who, name, color string
	if p != nil {
		who, name, color = p.Who, p.Name, p.Color
	}
	rm.mu.Unlock()
	if p == nil {
		return nil, fail(409, "not-here", "open the room's stream first")
	}
	chatSeq.Lock()
	chatSeq.n++
	n := chatSeq.n
	chatSeq.Unlock()
	now := time.Now()
	m := collabChat{
		ID:  strconv.FormatInt(now.UnixMilli(), 36) + "-" + strconv.FormatInt(n, 36),
		Who: who, Name: name, Color: color, Text: text, At: now.UnixMilli(),
	}
	if err := rm.s.appendChat(rm.id, m); err != nil {
		return nil, err
	}
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.chat = append(rm.chat, m)
	if len(rm.chat) > collabChatKeep {
		rm.chat = append([]collabChat(nil), rm.chat[len(rm.chat)-collabChatKeep:]...)
	}
	rm.broadcast(map[string]any{"t": "chat", "msg": m}, 0)
	return m, nil
}

// --- the stream

// a page joins: its stream gets the edits after `from` (-1: none), then
// everyone hears who is here
func (rm *collabRoom) join(sub *collabSub, client, who, name, color string, from int, epoch string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.idleTimer != nil {
		rm.idleTimer.Stop()
		rm.idleTimer = nil
	}
	rm.subs[sub] = client
	switch {
	case epoch != "" && epoch != rm.epoch:
		// its rev is another run's: what it has is read again
		sub.push(newEvt(map[string]any{"t": "reset"}, 0))
	case from < 0 || from == rm.rev:
	case from >= rm.logFrom && from < rm.rev:
		for _, e := range rm.log[from-rm.logFrom:] {
			sub.push(newEvt(opEvent(e.rev, e.client, e.d), e.rev))
		}
	default:
		sub.push(newEvt(map[string]any{"t": "reset"}, 0))
	}
	p := rm.peers[client]
	if p == nil {
		p = &collabPeer{Client: client, Who: who, Name: name, Color: color}
		// one person's other pages have their name; someone new gets a name
		// and a colour nobody else here has
		same := false
		for _, q := range rm.peers {
			if q.Who == who {
				p.Name, p.Color, same = q.Name, q.Color, true
			}
		}
		if !same {
			p.Name, p.Color = rm.freeName(who, name), rm.freeColor(who, color)
		}
		rm.peers[client] = p
	}
	p.open++
	rm.sendPeers()
}

// The names and colours web/collab.js gives a new person (a test checks
// the two lists agree).
var (
	collabAnimals = []string{
		"Zebra", "Otter", "Panda", "Koala", "Lynx", "Falcon", "Heron", "Badger", "Beaver", "Bison",
		"Dolphin", "Ferret", "Gecko", "Hedgehog", "Ibis", "Jaguar", "Kiwi", "Lemur", "Moose", "Narwhal",
		"Ocelot", "Puffin", "Quokka", "Raven", "Seal", "Tapir", "Walrus", "Yak", "Fox", "Owl",
	}
	collabColors = []string{
		"#ea580c", "#0d9488", "#7c3aed", "#db2777", "#2563eb", "#16a34a", "#ca8a04", "#dc2626",
		"#0891b2", "#9333ea", "#65a30d", "#c2410c",
	}
)

// (locked) `name` when no one else here has it (letter case aside); else an
// "Anonymous…" name becomes another free animal, and once the animals are
// taken (or for a name of one's own) a number is added: "Ada 2", "Ada 3"
func (rm *collabRoom) freeName(who, name string) string {
	taken := map[string]bool{}
	for _, q := range rm.peers {
		if q.Who != who {
			taken[strings.ToLower(q.Name)] = true
		}
	}
	if !taken[strings.ToLower(name)] {
		return name
	}
	if strings.HasPrefix(name, "Anonymous") {
		var free []string
		for _, a := range collabAnimals {
			if !taken[strings.ToLower("Anonymous"+a)] {
				free = append(free, "Anonymous"+a)
			}
		}
		if len(free) > 0 {
			return free[rand.IntN(len(free))]
		}
	}
	for n := 2; ; n++ {
		if s := name + " " + strconv.Itoa(n); !taken[strings.ToLower(s)] {
			return s
		}
	}
}

// (locked) `color` when no one else here has it; else a free one of the
// palette, and when all twelve are taken, `color` after all
func (rm *collabRoom) freeColor(who, color string) string {
	taken := map[string]bool{}
	for _, q := range rm.peers {
		if q.Who != who {
			taken[strings.ToLower(q.Color)] = true
		}
	}
	if !taken[strings.ToLower(color)] {
		return color
	}
	var free []string
	for _, c := range collabColors {
		if !taken[c] {
			free = append(free, c)
		}
	}
	if len(free) == 0 {
		return color
	}
	return free[rand.IntN(len(free))]
}

func (rm *collabRoom) leave(sub *collabSub, client string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	delete(rm.subs, sub)
	if p := rm.peers[client]; p != nil {
		p.open--
		if p.open <= 0 {
			delete(rm.peers, client)
			// a page gone is out of the call too (not under the room's
			// lock: the call takes its own first)
			if rm.call != nil {
				go rm.call.leave(client)
			}
		}
	}
	rm.sendPeers()
	if len(rm.subs) == 0 {
		rm.idleTimer = time.AfterFunc(collabIdle, rm.close)
	}
}

// nobody has been here for a while: written, and forgotten
func (rm *collabRoom) close() {
	rm.flush()
	c := rm.s.collab
	c.mu.Lock()
	defer c.mu.Unlock()
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if len(rm.subs) == 0 && !rm.dirty && c.rooms[rm.id] == rm {
		delete(c.rooms, rm.id)
	}
}

// the room part of GET /api/events and /api/socket, nil when the request
// names none
func (s *localServer) joinRoom(r *http.Request) (*collabRoom, *collabSub, string, error) {
	q := r.URL.Query()
	id := q.Get("room")
	if id == "" {
		return nil, nil, "", nil
	}
	client := q.Get("client")
	who := q.Get("who")
	if !shareID.MatchString(id) || !clientID.MatchString(client) {
		return nil, nil, "", fail(400, "", "room and client are expected")
	}
	if !clientID.MatchString(who) {
		who = client
	}
	name := cleanName(q.Get("name"))
	if name == "" {
		name = "Anonymous"
	}
	color := q.Get("color")
	if !colorHex.MatchString(color) {
		color = "#64748b"
	}
	from := -1
	if v, err := strconv.Atoi(r.Header.Get("Last-Event-ID")); err == nil && v >= 0 {
		from = v
	} else if v, err := strconv.Atoi(q.Get("rev")); err == nil && v >= 0 {
		from = v
	}
	rm, err := s.room(r.Context(), id)
	if err != nil {
		return nil, nil, "", err
	}
	sub := &collabSub{wake: make(chan struct{}, 1)}
	rm.join(sub, client, who, name, color, from, q.Get("epoch"))
	return rm, sub, client, nil
}
