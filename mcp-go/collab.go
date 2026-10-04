// SPDX-License-Identifier: AGPL-3.0-or-later

// Editing a deck together on a server of one's own: everyone with the deck
// open sees the others' edits as they make them, where their carets are,
// and a chat beside the deck.
//
// One room per open deck holds its text and every edit taken (rev 1, 2, …).
// A page sends its edit as a delta (otdelta.go) on the revision it had; one
// made on an older revision is transformed over the edits taken since, the
// way a central OT server does, so nobody's send is refused. Every edit goes
// out to everyone, its sender too (that is the sender's acknowledgement), in
// one order on the event stream:
//
//	GET  /api/collab/{id}          {rev, md, peers, chat}
//	POST /api/collab/{id}/op       {client, rev, ops}       -> {rev}
//	POST /api/collab/{id}/presence {client, rev, caret, anchor, who, name, color}
//	POST /api/collab/{id}/chat     {client, text}           -> the message
//	GET  /api/events?room={id}&client=…&who=…&name=…&color=…&rev=…
//
//	id: 12
//	data: {"t":"op","rev":12,"client":"…","ops":[…]}
//	data: {"t":"cursor","client":"…","rev":12,"caret":40,"anchor":40}
//	data: {"t":"peers","rev":12,"peers":[…]}
//	data: {"t":"chat","msg":{…}}
//	data: {"t":"reset"}                      (catch up from GET /api/collab)
//
// A stream that drops reconnects with Last-Event-ID and gets the edits it
// missed; a page whose stream is gone has left. The room writes the deck's
// md shortly after edits stop; a write from elsewhere (an assistant's
// update_presentation, a page not in the room) comes in as an edit of its
// own, merged over what was typed meanwhile. The chat is kept beside the
// deck's files, in shares/{id}/.collab/chat.jsonl.

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

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
	collabPath = regexp.MustCompile(`^/api/collab/([A-Za-z0-9]{6,32})(/op|/presence|/chat)?$`)
	clientID   = regexp.MustCompile(`^[A-Za-z0-9_-]{4,64}$`)
	colorHex   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

type collabEntry struct {
	rev    int
	client string
	d      delta
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

// one page's stream: events wait here, never dropped; a page that falls too
// far behind is cut off and comes back with Last-Event-ID
type collabSub struct {
	mu   sync.Mutex
	q    [][]byte
	over bool
	wake chan struct{}
}

func (c *collabSub) push(b []byte) {
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

func (c *collabSub) take() ([][]byte, bool) {
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

	ready   bool
	text    []uint16
	rev     int
	log     []collabEntry // revs logFrom+1 … rev
	logFrom int
	peers   map[string]*collabPeer
	subs    map[*collabSub]struct{}
	chat    []collabChat

	// what the deck's file holds: fileMd, which was the text at fileRev
	// once fileGap's edits are applied to it (writes from elsewhere leave
	// such a gap until the room writes the file again)
	fileMd  []uint16
	fileRev int
	fileGap []delta
	// the room's own write under way
	saving    []uint16
	savingRev int

	dirty     bool
	saveTimer *time.Timer
	dirtyAt   time.Time
	idleTimer *time.Timer
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
		rm = &collabRoom{s: s, id: id, peers: map[string]*collabPeer{}, subs: map[*collabSub]struct{}{}}
		s.collab.rooms[id] = rm
	}
	s.collab.mu.Unlock()
	rm.load.Do(func() {
		// read without the room locked: a write meanwhile (its callback
		// sees the room not ready) is the newer text, and wins
		d, _ := s.env.DB.Get(ctx, "shares", id)
		md, _ := d["md"].(string)
		chat := readChat(s.chatFile(id))
		rm.mu.Lock()
		if !rm.ready {
			rm.text = toU16(md)
			rm.fileMd = rm.text
		}
		rm.chat = chat
		rm.ready = true
		rm.mu.Unlock()
	})
	return rm, nil
}

// fsDB tells every write, in order, while the folder is locked
func (s *localServer) collabWritten(col, id string, doc Doc) {
	if col != "shares" {
		return
	}
	rm := s.openRoom(id)
	if rm == nil {
		return
	}
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if doc == nil {
		rm.broadcast(map[string]any{"t": "reset"}, 0)
		return
	}
	md, _ := doc["md"].(string)
	got := toU16(md)
	if !rm.ready {
		rm.text = got
		rm.fileMd = got
		rm.ready = true
		return
	}
	if rm.saving != nil && sameU16(got, rm.saving) {
		// the room's own write
		rm.fileMd, rm.fileRev, rm.fileGap = rm.saving, rm.savingRev, nil
		rm.saving = nil
		return
	}
	if sameU16(got, rm.fileMd) {
		return
	}
	// a write from elsewhere, made on what the file held: an edit of that,
	// carried over what was typed since
	x := diffU16(rm.fileMd, got)
	var gap []delta
	if rm.fileRev < rm.logFrom {
		x, gap = diffU16(rm.text, got), nil
	} else {
		for _, g := range rm.fileGap {
			var g1 delta
			x, g1 = transform(x, g)
			gap = append(gap, g1)
		}
		for _, e := range rm.log[rm.fileRev-rm.logFrom:] {
			var e1 delta
			x, e1 = transform(x, e.d)
			gap = append(gap, e1)
		}
	}
	if !x.noop() {
		if err := rm.take(x, "server"); err != nil {
			// cannot be: x is on the text; read it again rather than guess
			rm.text = got
			gap = nil
			rm.broadcast(map[string]any{"t": "reset"}, 0)
		}
	}
	rm.fileMd, rm.fileRev, rm.fileGap = got, rm.rev, gap
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
func (rm *collabRoom) take(d delta, client string) error {
	out, err := d.apply(rm.text)
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
		p.Caret = d.transformIndex(p.Caret, own)
		p.Anchor = d.transformIndex(p.Anchor, own)
	}
	rm.broadcast(opEvent(rm.rev, client, d), rm.rev)
	rm.changed()
	return nil
}

func opEvent(rev int, client string, d delta) map[string]any {
	return map[string]any{"t": "op", "rev": rev, "client": client, "ops": d.json()}
}

func sseBytes(v any, id int) []byte {
	b, _ := json.Marshal(v)
	if id > 0 {
		return []byte("id: " + strconv.Itoa(id) + "\ndata: " + string(b) + "\n\n")
	}
	return []byte("data: " + string(b) + "\n\n")
}

// (locked)
func (rm *collabRoom) broadcast(v any, id int) {
	b := sseBytes(v, id)
	for c := range rm.subs {
		c.push(b)
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
	rm.mu.Lock()
	if !rm.dirty {
		rm.mu.Unlock()
		return
	}
	rm.saving = append([]uint16(nil), rm.text...)
	rm.savingRev = rm.rev
	rm.dirty = false
	md := fromU16(rm.saving)
	rm.mu.Unlock()
	// not under the room's lock: the write calls back into the room
	shareMu.Lock()
	err := rm.s.env.DB.Update(context.Background(), "shares", rm.id, Doc{"md": md, "updated": time.Now().UTC()})
	shareMu.Unlock()
	rm.mu.Lock()
	rm.saving = nil
	if err != nil {
		fmt.Fprintf(os.Stderr, "sliqtly: writing %s: %v\n", rm.id, err)
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

// --- the chat file

func (s *localServer) chatFile(id string) string {
	return filepath.Join(s.bucket.root, "shares", id, ".collab", "chat.jsonl")
}

func readChat(path string) []collabChat {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []collabChat
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var m collabChat
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.ID != "" {
			out = append(out, m)
		}
	}
	if len(out) > collabChatKeep {
		out = out[len(out)-collabChatKeep:]
	}
	return out
}

func appendChat(path string, m collabChat) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(m)
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
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
		return map[string]any{"rev": rm.rev, "md": fromU16(rm.text), "peers": rm.peerList(), "chat": chat}, nil
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
	}
	return rm.say(body, client)
}

// an edit made on revision `rev`
func (rm *collabRoom) submit(body map[string]any, client string) (any, error) {
	at, ok := count(body["rev"])
	if !ok {
		return nil, fail(400, "", "rev: a revision is expected")
	}
	d, err := parseDelta(body["ops"])
	if err != nil {
		return nil, fail(400, "", err.Error())
	}
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if at > rm.rev {
		return nil, fail(409, "ahead", "that revision is not here yet")
	}
	if at < rm.logFrom {
		return nil, fail(409, "too-old", "too far behind: read the presentation again")
	}
	for _, e := range rm.log[at-rm.logFrom:] {
		d, _ = transform(d, e.d)
	}
	if err := rm.take(d, client); err != nil {
		var ae *apiError
		if !errors.As(err, &ae) {
			err = fail(409, "does-not-apply", err.Error())
		}
		return nil, err
	}
	return map[string]int{"rev": rm.rev}, nil
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
		p.Name, named = n, true
	}
	if c, _ := body["color"].(string); colorHex.MatchString(c) && c != p.Color {
		p.Color, named = c, true
	}
	at, ok := count(body["rev"])
	caret, okc := count(body["caret"])
	anchor, oka := count(body["anchor"])
	if ok && okc && at <= rm.rev && at >= rm.logFrom {
		if !oka {
			anchor = caret
		}
		for _, e := range rm.log[at-rm.logFrom:] {
			caret = e.d.transformIndex(caret, false)
			anchor = e.d.transformIndex(anchor, false)
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
	rm.mu.Lock()
	defer rm.mu.Unlock()
	p := rm.peers[client]
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
		Who: p.Who, Name: p.Name, Color: p.Color, Text: text, At: now.UnixMilli(),
	}
	if err := appendChat(rm.s.chatFile(rm.id), m); err != nil {
		return nil, err
	}
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
func (rm *collabRoom) join(sub *collabSub, client, who, name, color string, from int) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.idleTimer != nil {
		rm.idleTimer.Stop()
		rm.idleTimer = nil
	}
	rm.subs[sub] = struct{}{}
	switch {
	case from < 0 || from == rm.rev:
	case from >= rm.logFrom && from < rm.rev:
		for _, e := range rm.log[from-rm.logFrom:] {
			sub.push(sseBytes(opEvent(e.rev, e.client, e.d), e.rev))
		}
	default:
		sub.push(sseBytes(map[string]any{"t": "reset"}, 0))
	}
	p := rm.peers[client]
	if p == nil {
		p = &collabPeer{Client: client, Who: who, Name: name, Color: color}
		// one person's other pages have their name
		for _, q := range rm.peers {
			if q.Who == who {
				p.Name, p.Color = q.Name, q.Color
			}
		}
		rm.peers[client] = p
	}
	p.open++
	rm.sendPeers()
}

func (rm *collabRoom) leave(sub *collabSub, client string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	delete(rm.subs, sub)
	if p := rm.peers[client]; p != nil {
		p.open--
		if p.open <= 0 {
			delete(rm.peers, client)
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

// the room part of GET /api/events, nil when the request names none
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
	rm.join(sub, client, who, name, color, from)
	return rm, sub, client, nil
}

func writeEvents(w io.Writer, sub *collabSub) bool {
	q, over := sub.take()
	for _, b := range q {
		w.Write(b)
	}
	return !over
}
