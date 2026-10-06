// SPDX-License-Identifier: AGPL-3.0-or-later

// Calls: the people who have a deck open on a server of one's own talk with
// each other, the way they would in Teams, while they edit (or present)
// the deck. The deck is the call's topic; the call is its room's.
//
// The server is a forwarding unit (an SFU): each page has one WebRTC
// connection, to the server and never to another page, sends its
// microphone up it and gets everyone else's voice down it. The server
// forwards the Opus packets as they come, without decoding them, so a call
// costs it little more than the bytes. One page's microphone is never sent
// back to it.
//
// Signalling rides the room: a page asks with POST, the server answers on
// the room's stream, to that page only.
//
//	POST /api/collab/{id}/call {client, op:"join", muted}
//	                           {client, op:"answer", sdp, n}
//	                           {client, op:"mute", muted, target?}
//	                           {client, op:"leave"}
//	data: {"t":"call","members":[{client,who,name,color,muted,host}]}  (everyone in the room)
//	data: {"t":"call-offer","sdp":"…","n":3}                            (one page)
//
// The server always makes the offer (on joining, and whenever someone's
// voice comes or goes), so offers never cross. It is an ICE-lite agent with
// host candidates only: the browser checks them, nothing else is needed on a
// network the page's own HTTP reaches.
//
// Firewalls: the voice goes over UDP on the server's own port number, every
// call on that one port; where UDP does not pass, over TCP on the very port
// the page came in on (netaccess.go hands such connections here by their
// first byte). Anything that can open the page can carry its call.
//
// The call is told in the deck's home room chat (roomchat.go): a line by
// "Sliqtly" when it starts, changed to say how long it was and who took
// part when it ends. People in the room see a call is on and open the deck
// to join; assistants reading the room's chat (read_room_chat) see it too.
// A room's own call (the "huddle") can start one through startCall.
//
// Muting is kept by the server too: a muted member's packets are not
// forwarded, whatever the page sends. The first to join is the host, who
// may mute anyone; when the host leaves, the next one is.

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/logging"
	"github.com/pion/webrtc/v4"
	"github.com/terotests/sliqtly/mcp-go/store"
)

const callMost = 50 // members in one call

// one member of a call: its connection and what it sends and gets
type callMember struct {
	client   string
	who      string
	name     string
	color    string
	muted    bool
	joined   time.Time
	pc       *webrtc.PeerConnection
	out      map[string]*webrtc.RTPSender // whose voice → its sender here
	offering bool                         // an offer waits for its answer
	again    bool                         // something changed meanwhile: offer again
	n        int                          // the offer's number
}

type roomCall struct {
	rm      *collabRoom
	api     *webrtc.API
	mu      sync.Mutex
	members map[string]*callMember
	voices  map[string]*webrtc.TrackLocalStaticRTP // each member's voice, as forwarded
	started time.Time
	people  []string // everyone who was in it, by name, in the order they came
	// its line in the room's chat: posted is closed once it is (id "" when
	// it could not be)
	notice string
	posted chan struct{}
}

type callRow struct {
	Client string `json:"client"`
	Who    string `json:"who"`
	Name   string `json:"name"`
	Color  string `json:"color"`
	Muted  bool   `json:"muted"`
	Host   bool   `json:"host"`
}

func (rm *collabRoom) callOp(body map[string]any, client string) (any, error) {
	op, _ := body["op"].(string)
	rm.mu.Lock()
	p := rm.peers[client]
	var name, color, who string
	if p != nil {
		name, color, who = p.Name, p.Color, p.Who
	}
	c := rm.call
	started := false
	if c == nil && op == "join" && p != nil {
		var err error
		if c, err = rm.startCall(); err != nil {
			rm.mu.Unlock()
			return nil, err
		}
		started = true
	}
	rm.mu.Unlock()
	if started {
		go c.tellStart(name)
	}
	if p == nil {
		return nil, fail(409, "not-here", "open the room's stream first")
	}
	if c == nil {
		return nil, fail(409, "no-call", "nobody is in a call here")
	}
	muted, _ := body["muted"].(bool)
	switch op {
	case "join":
		return c.join(client, who, name, color, muted)
	case "answer":
		sdp, _ := body["sdp"].(string)
		n, _ := count(body["n"])
		return c.answer(client, sdp, n)
	case "mute":
		target, _ := body["target"].(string)
		if target == "" {
			target = client
		}
		return c.mute(client, target, muted)
	case "leave":
		c.leave(client)
		return map[string]bool{"ok": true}, nil
	}
	return nil, fail(400, "", "op: join, answer, mute or leave")
}

// (locked) startCall: the room's call, made now. The deck's page starts
// one by joining; a room's huddle would start it the same way.
func (rm *collabRoom) startCall() (*roomCall, error) {
	api, err := rm.s.callAPI()
	if err != nil {
		log.Printf("call: %v", err)
		return nil, fail(503, "no-call", "calls cannot start on this server: "+err.Error())
	}
	c := &roomCall{rm: rm, api: api, members: map[string]*callMember{}, voices: map[string]*webrtc.TrackLocalStaticRTP{}, started: time.Now(), posted: make(chan struct{})}
	rm.call = c
	return c, nil
}

// the call's line in the room's chat, when it starts and when it ends
func (c *roomCall) tellStart(by string) {
	defer close(c.posted)
	c.notice = c.rm.s.callNotice("", c.rm.id, fmt.Sprintf("📞 %s started a call on [[slides:%s]]. Open the presentation and press *Join call*.", by, c.rm.id))
}

func (c *roomCall) tellEnd() {
	<-c.posted
	if c.notice == "" {
		return
	}
	c.mu.Lock()
	people := append([]string{}, c.people...)
	c.mu.Unlock()
	mins := int(time.Since(c.started).Round(time.Minute) / time.Minute)
	took := "under a minute"
	if mins == 1 {
		took = "1 minute"
	} else if mins > 1 {
		took = fmt.Sprintf("%d minutes", mins)
	}
	c.rm.s.callNotice(c.notice, c.rm.id, fmt.Sprintf("📞 Call on [[slides:%s]] ended · %s · %s", c.rm.id, took, strings.Join(people, ", ")))
}

// callNotice posts text in deck's home room chat as Sliqtly (or changes
// the line id posted before) → its id; "" when the server keeps no rooms
func (s *localServer) callNotice(id, deck, text string) string {
	rs := s.env.rooms
	if rs == nil || s.env.DB == nil {
		return ""
	}
	ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	d, err := s.env.DB.Get(ctx, "shares", deck)
	if err != nil || d == nil {
		return ""
	}
	room, _ := d[store.RoomField].(string)
	if room == "" {
		return ""
	}
	a := map[string]any{"room_id": room, "text": text, "agent": "Sliqtly"}
	if id != "" {
		a["message_id"] = id
	}
	out, err := rs.callVia(ctx, s.env.LocalUser, viaMcp, "post_room_message", a)
	if err != nil {
		log.Printf("call: telling room %s: %v", room, err)
		return ""
	}
	m, _ := out.(map[string]any)
	got, _ := m["message_id"].(string)
	return got
}

func (c *roomCall) join(client, who, name, color string, muted bool) (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m := c.members[client]; m != nil {
		// joined again (the page lost its connection): a new one
		c.drop(m)
	}
	if len(c.members) >= callMost {
		return nil, fail(409, "call-full", "the call is full")
	}
	pc, err := c.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	m := &callMember{client: client, who: who, name: name, color: color, muted: muted, joined: time.Now(), pc: pc, out: map[string]*webrtc.RTPSender{}}
	// its microphone comes up this one
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		pc.Close()
		return nil, err
	}
	// everyone else's voice goes down it
	for from, v := range c.voices {
		c.send(m, from, v)
	}
	pc.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) { c.voice(m, t) })
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		if st == webrtc.PeerConnectionStateFailed {
			c.leaveMember(m)
		}
	})
	c.members[client] = m
	if !slices.Contains(c.people, name) {
		c.people = append(c.people, name)
	}
	c.offer(m)
	c.tell()
	return map[string]any{"ok": true}, nil
}

// (locked) one member's voice added to another's connection
func (c *roomCall) send(m *callMember, from string, v *webrtc.TrackLocalStaticRTP) {
	if from == m.client || m.out[from] != nil {
		return
	}
	// a place of its own, sent only: AddTrack would take the page's
	// microphone's place (the first, which the server only receives on)
	tr, err := m.pc.AddTransceiverFromTrack(v, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
	var s *webrtc.RTPSender
	if err == nil {
		s = tr.Sender()
	}
	if err != nil {
		log.Printf("call: forwarding %s to %s: %v", from, m.client, err)
		return
	}
	m.out[from] = s
	// its reports are read, or they pile up
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := s.Read(buf); err != nil {
				return
			}
		}
	}()
}

// a member's microphone has come: forwarded to everyone else from now on
func (c *roomCall) voice(m *callMember, t *webrtc.TrackRemote) {
	if t.Kind() != webrtc.RTPCodecTypeAudio {
		return
	}
	v, err := webrtc.NewTrackLocalStaticRTP(t.Codec().RTPCodecCapability, "voice", m.client)
	if err != nil {
		log.Printf("call: %v", err)
		return
	}
	c.mu.Lock()
	if c.members[m.client] != m {
		c.mu.Unlock()
		return
	}
	c.voices[m.client] = v
	for _, o := range c.members {
		if o != m {
			c.send(o, m.client, v)
			c.offer(o)
		}
	}
	c.mu.Unlock()
	buf := make([]byte, 1500)
	for {
		n, _, err := t.Read(buf)
		if err != nil {
			return
		}
		c.mu.Lock()
		muted := m.muted
		c.mu.Unlock()
		if muted {
			continue
		}
		if _, err := v.Write(buf[:n]); err != nil && !errors.Is(err, net.ErrClosed) {
			return
		}
	}
}

// (locked) a new offer to the member's page, or one more after the one it
// is answering
func (c *roomCall) offer(m *callMember) {
	if m.offering {
		m.again = true
		return
	}
	m.offering = true
	m.again = false
	m.n++
	n := m.n
	go func() {
		o, err := m.pc.CreateOffer(nil)
		if err == nil {
			done := webrtc.GatheringCompletePromise(m.pc)
			err = m.pc.SetLocalDescription(o)
			if err == nil {
				<-done
			}
		}
		if err != nil {
			log.Printf("call: offer to %s: %v", m.client, err)
			c.leaveMember(m)
			return
		}
		sdp := m.pc.LocalDescription().SDP
		c.rm.mu.Lock()
		c.rm.sendTo(m.client, map[string]any{"t": "call-offer", "sdp": sdp, "n": n})
		c.rm.mu.Unlock()
	}()
}

func (c *roomCall) answer(client, sdp string, n int) (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.members[client]
	if m == nil {
		return nil, fail(409, "not-in-call", "join the call first")
	}
	if !m.offering || n != m.n {
		return nil, fail(409, "old-offer", "that offer is not the one waiting")
	}
	if err := m.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}); err != nil {
		return nil, fail(400, "", "answer: "+err.Error())
	}
	m.offering = false
	if m.again {
		c.offer(m)
	}
	return map[string]any{"ok": true}, nil
}

func (c *roomCall) mute(by, target string, muted bool) (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.members[target]
	if m == nil {
		return nil, fail(409, "not-in-call", "not in the call")
	}
	if by != target {
		// the host mutes others; nobody unmutes anyone but themselves
		if c.host() != by || !muted {
			return nil, fail(403, "", "only the host mutes others, and only they unmute themselves")
		}
	}
	m.muted = muted
	c.tell()
	return map[string]any{"ok": true}, nil
}

func (c *roomCall) leave(client string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m := c.members[client]; m != nil {
		c.drop(m)
		c.tell()
	}
}

func (c *roomCall) leaveMember(m *callMember) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.members[m.client] == m {
		c.drop(m)
		c.tell()
	}
}

// (locked) the member out, its voice taken from the others
func (c *roomCall) drop(m *callMember) {
	delete(c.members, m.client)
	go m.pc.Close()
	if c.voices[m.client] == nil {
		return
	}
	delete(c.voices, m.client)
	for _, o := range c.members {
		if s := o.out[m.client]; s != nil {
			delete(o.out, m.client)
			if err := o.pc.RemoveTrack(s); err == nil {
				c.offer(o)
			}
		}
	}
}

// (locked) the host: the one in the call longest
func (c *roomCall) host() string {
	var h *callMember
	for _, m := range c.members {
		if h == nil || m.joined.Before(h.joined) {
			h = m
		}
	}
	if h == nil {
		return ""
	}
	return h.client
}

// (locked) who is in the call, in the order they came
func (c *roomCall) rows() []callRow {
	ms := make([]*callMember, 0, len(c.members))
	for _, m := range c.members {
		ms = append(ms, m)
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].joined.Before(ms[j].joined) })
	host := c.host()
	out := make([]callRow, 0, len(ms))
	for _, m := range ms {
		out = append(out, callRow{Client: m.client, Who: m.who, Name: m.name, Color: m.color, Muted: m.muted, Host: m.client == host})
	}
	return out
}

// (locked) everyone in the room hears who is in the call, as it is when
// told (so tellings that pass each other still end on the last state); an
// empty call is gone
func (c *roomCall) tell() {
	rm := c.rm
	go func() {
		rm.mu.Lock()
		defer rm.mu.Unlock()
		c.mu.Lock()
		rows := c.rows()
		c.mu.Unlock()
		if len(rows) == 0 && rm.call == c {
			rm.call = nil
			go c.tellEnd()
		}
		rm.broadcast(map[string]any{"t": "call", "members": rows}, 0)
	}()
}

// (locked by the room) the call's members, for a page that joins the room
func (rm *collabRoom) callRows() []callRow {
	if rm.call == nil {
		return []callRow{}
	}
	rm.call.mu.Lock()
	defer rm.call.mu.Unlock()
	return rm.call.rows()
}

// a member's name or colour changed in the room
func (rm *collabRoom) callRename(who, name, color string) {
	if rm.call == nil {
		return
	}
	c := rm.call
	go func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		changed := false
		for _, m := range c.members {
			if m.who == who {
				m.name, m.color, changed = name, color, true
			}
		}
		if changed {
			c.tell()
		}
	}()
}

// --- the network calls go over

// callNet is the server's one way to WebRTC: one UDP port for every call
// and the HTTP port's TCP, made when the first call starts and made again
// when the addresses the server is reached on have changed
type callNet struct {
	api *webrtc.API
	key string
	udp ice.UDPMux
	tcp *ice.TCPMuxDefault
}

func (s *localServer) callAPI() (*webrtc.API, error) {
	s.callMu.Lock()
	defer s.callMu.Unlock()
	key := ""
	if s.expo != nil {
		key = strings.Join(s.expo.listening(), ",") + "|" + s.expo.policy.Load().Access
	}
	if s.cnet != nil && (s.cnet.key == key || s.callsOn()) {
		return s.cnet.api, nil
	}
	if s.cnet != nil {
		s.cnet.close()
		s.cnet = nil
	}
	n, err := newCallNet(s.expo, key)
	if err != nil {
		return nil, err
	}
	s.cnet = n
	return n.api, nil
}

// is any room's call on (then the network is kept as it is)
func (s *localServer) callsOn() bool {
	if s.collab == nil {
		return false
	}
	s.collab.mu.Lock()
	rooms := make([]*collabRoom, 0, len(s.collab.rooms))
	for _, rm := range s.collab.rooms {
		rooms = append(rooms, rm)
	}
	s.collab.mu.Unlock()
	for _, rm := range rooms {
		if rm.call != nil {
			return true
		}
	}
	return false
}

func newCallNet(x *exposure, key string) (*callNet, error) {
	quiet := logging.NewDefaultLoggerFactory()
	quiet.DefaultLogLevel = logging.LogLevelError
	// the addresses offered are the ones the server is reached on
	keep := func(ip net.IP) bool { return x == nil || x.reachedOn(ip) }
	port := 0
	if x != nil {
		port, _ = strconv.Atoi(x.port)
	}
	se := webrtc.SettingEngine{LoggerFactory: quiet}
	se.SetLite(true)
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	se.SetIncludeLoopbackCandidate(true)
	se.SetIPFilter(keep)
	n := &callNet{key: key}
	udp, err := ice.NewMultiUDPMuxFromPort(port, ice.UDPMuxFromPortWithLoopback(), ice.UDPMuxFromPortWithIPFilter(keep), ice.UDPMuxFromPortWithLogger(quiet.NewLogger("udp")))
	if err != nil {
		// the port is taken for UDP: TCP alone still carries calls
		log.Printf("call: UDP port %d: %v (calls go over TCP)", port, err)
	} else {
		n.udp = udp
		se.SetICEUDPMux(udp)
	}
	types := []webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6}
	if x != nil {
		l := x.iceListener()
		n.tcp = ice.NewTCPMuxDefault(ice.TCPMuxParams{Listener: l, Logger: quiet.NewLogger("tcp"), ReadBufferSize: 64})
		se.SetICETCPMux(n.tcp)
		types = append(types, webrtc.NetworkTypeTCP4, webrtc.NetworkTypeTCP6)
	}
	if n.udp == nil && n.tcp == nil {
		return nil, err
	}
	se.SetNetworkTypes(types)
	var me webrtc.MediaEngine
	if err := me.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1"},
		PayloadType:        111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}
	n.api = webrtc.NewAPI(webrtc.WithMediaEngine(&me), webrtc.WithSettingEngine(se))
	return n, nil
}

func (n *callNet) close() {
	if n.udp != nil {
		n.udp.Close()
	}
	if n.tcp != nil {
		n.tcp.Close()
	}
}
