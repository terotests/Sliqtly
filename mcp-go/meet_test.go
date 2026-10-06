// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// a page in a call, as a test has it: its stream, its connection to the
// server, and the voices it hears (by whose they are)
type callPage struct {
	t      *testing.T
	base   string
	id     string
	client string
	s      *testSocket
	pc     *webrtc.PeerConnection
	mic    *webrtc.TrackLocalStaticSample
	mu     sync.Mutex
	heard  map[string]int // packets heard from each page
	call   []any          // the call as told last
}

func joinCall(t *testing.T, base, id, client, name string, net []webrtc.NetworkType) *callPage {
	t.Helper()
	q := fmt.Sprintf("?room=%s&client=%s&who=w%s&name=%s&color=%%23ea580c&rev=0", id, client, client, name)
	p := &callPage{t: t, base: base, id: id, client: client, s: dialSocket(t, base, q), heard: map[string]int{}}
	p.s.next("peers", roomMsg("peers"))
	se := webrtc.SettingEngine{}
	if net != nil {
		se.SetNetworkTypes(net)
	}
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	p.pc = pc
	p.mic, _ = webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "mic", client)
	pc.OnTrack(func(tr *webrtc.TrackRemote, rc *webrtc.RTPReceiver) {
		from := tr.StreamID()
		// the first place is this page's microphone, which a browser sends
		// only: a voice coming on it is never heard there
		for _, x := range pc.GetTransceivers() {
			if x.Receiver() == rc && x.Mid() == "0" {
				t.Errorf("%s's voice came on the microphone's place", from)
			}
		}
		go func() {
			for {
				if _, _, err := tr.ReadRTP(); err != nil {
					return
				}
				p.mu.Lock()
				p.heard[from]++
				p.mu.Unlock()
			}
		}()
	})
	// the server's offers, answered as they come
	go func() {
		first := true
		for m := range p.s.msgs {
			v, _ := m["v"].(map[string]any)
			switch v["t"] {
			case "call":
				ms, _ := v["members"].([]any)
				p.mu.Lock()
				p.call = ms
				p.mu.Unlock()
			case "call-offer":
				sdp, _ := v["sdp"].(string)
				if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}); err != nil {
					t.Error(err)
					return
				}
				if first {
					first = false
					if _, err := pc.AddTrack(p.mic); err != nil {
						t.Error(err)
						return
					}
				}
				a, err := pc.CreateAnswer(nil)
				if err != nil {
					t.Error(err)
					return
				}
				pc.SetLocalDescription(a)
				p.post(map[string]any{"op": "answer", "sdp": a.SDP, "n": v["n"]})
			}
		}
	}()
	code, body := p.post(map[string]any{"op": "join"})
	if code != 200 {
		t.Fatalf("join: %d %s", code, body)
	}
	return p
}

func (p *callPage) post(b map[string]any) (int, string) {
	b["client"] = p.client
	j, _ := json.Marshal(b)
	return req(p.t, "POST", p.base+"/api/collab/"+p.id+"/call", "application/json", string(j))
}

// speaks (any bytes do: the server forwards, it does not decode) until stop
func (p *callPage) speak(stop chan struct{}) {
	go func() {
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				p.mic.WriteSample(media.Sample{Data: []byte{0xfc, 0xff, 0xfe}, Duration: 20 * time.Millisecond})
			}
		}
	}()
}

func (p *callPage) heardFrom(who string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.heard[who]
}

// waits until `ok` holds
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	end := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(end) {
			t.Fatalf("no %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// the call as told last, once it has n members and `ok` holds: client →
// its row
func (p *callPage) callNow(t *testing.T, n int, ok func(map[string]map[string]any) bool) map[string]map[string]any {
	t.Helper()
	var out map[string]map[string]any
	waitFor(t, fmt.Sprintf("a call of %d", n), func() bool {
		p.mu.Lock()
		ms := p.call
		p.mu.Unlock()
		if len(ms) != n {
			return false
		}
		out = map[string]map[string]any{}
		for _, m := range ms {
			r := m.(map[string]any)
			out[r["client"].(string)] = r
		}
		return ok == nil || ok(out)
	})
	return out
}

// Two pages talk through the server: each hears the other and never
// itself; the host mutes the other (whose voice then stops at the server),
// not the other way round; one leaving is told.
func TestCallTwoPages(t *testing.T) {
	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	id := newDeck(t, srv.URL, "# Call\n")

	a := joinCall(t, srv.URL, id, "pageCA", "Ada", nil)
	defer a.pc.Close()
	rows := a.callNow(t, 1, nil)
	eq(t, rows["pageCA"]["host"], true)
	b := joinCall(t, srv.URL, id, "pageCB", "Bo", nil)
	defer b.pc.Close()
	rows = b.callNow(t, 2, nil)
	eq(t, rows["pageCA"]["host"], true)
	eq(t, rows["pageCB"]["name"], "Bo")

	stop := make(chan struct{})
	defer close(stop)
	a.speak(stop)
	b.speak(stop)
	waitFor(t, "Bo hearing Ada", func() bool { return b.heardFrom("pageCA") > 10 })
	waitFor(t, "Ada hearing Bo", func() bool { return a.heardFrom("pageCB") > 10 })
	eq(t, a.heardFrom("pageCA"), 0, "nobody hears themselves")

	// Bo cannot mute the host; the host mutes Bo
	code, _ := b.post(map[string]any{"op": "mute", "target": "pageCA", "muted": true})
	eq(t, code, 403)
	code, _ = a.post(map[string]any{"op": "mute", "target": "pageCB", "muted": true})
	eq(t, code, 200)
	a.callNow(t, 2, func(r map[string]map[string]any) bool { return r["pageCB"]["muted"] == true })
	time.Sleep(200 * time.Millisecond)
	was := a.heardFrom("pageCB")
	time.Sleep(300 * time.Millisecond)
	if got := a.heardFrom("pageCB"); got > was+2 {
		t.Fatalf("a muted voice was forwarded: %d → %d", was, got)
	}
	// only Bo unmutes Bo
	code, _ = a.post(map[string]any{"op": "mute", "target": "pageCB", "muted": false})
	eq(t, code, 403)
	code, _ = b.post(map[string]any{"op": "mute", "muted": false})
	eq(t, code, 200)
	waitFor(t, "Bo again", func() bool { return a.heardFrom("pageCB") > was+10 })

	// the host leaves: Bo is the host now
	code, _ = a.post(map[string]any{"op": "leave"})
	eq(t, code, 200)
	rows = b.callNow(t, 1, nil)
	eq(t, rows["pageCB"]["host"], true)

	// a page that comes to the room sees the call
	code, body := req(t, "GET", srv.URL+"/api/collab/"+id, "", "")
	eq(t, code, 200)
	var snap struct {
		Call []callRow `json:"call"`
	}
	json.Unmarshal([]byte(body), &snap)
	eq(t, len(snap.Call), 1)
	eq(t, snap.Call[0].Client, "pageCB")

	// the last page's stream goes: the call is over
	b.s.ws.Close()
	waitFor(t, "the call to end", func() bool {
		_, body := req(t, "GET", srv.URL+"/api/collab/"+id, "", "")
		return strings.Contains(body, `"call":[]`)
	})
	// the deck's room was told, and its line says the call ended, who was in it
	waitFor(t, "the room's line to say it ended", func() bool {
		_, body := req(t, "POST", srv.URL+"/api/rooms/read_room_chat", "application/json", `{"room_id":"general"}`)
		return strings.Contains(body, "Call on [[slides:"+id+"]] ended") && strings.Contains(body, "Ada, Bo") && !strings.Contains(body, "started a call")
	})
}

// Where UDP does not pass, the call goes over TCP on the HTTP port itself:
// the page's own way in carries its voice too.
func TestCallOverHTTPPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	base := "http://127.0.0.1:" + port
	e, bucket, err := localEnv(t.TempDir(), base, "local")
	if err != nil {
		t.Fatal(err)
	}
	e.Client = fakeNet
	ls := newLocalServer(e, bucket, "", nil).(*localServer)
	hs := &http.Server{Handler: ls}
	p, _ := newNetPolicy(accessLocal, nil, true)
	x := newExposure(hs, port, p)
	ls.expo = x
	if err := x.sync(true); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		hs.Shutdown(ctx)
		ls.callMu.Lock()
		if ls.cnet != nil {
			ls.cnet.close()
		}
		ls.callMu.Unlock()
	}()
	id := newDeck(t, base, "# TCP\n")
	tcp := []webrtc.NetworkType{webrtc.NetworkTypeTCP4}
	a := joinCall(t, base, id, "pageTA", "Ada", tcp)
	defer a.pc.Close()
	b := joinCall(t, base, id, "pageTB", "Bo", tcp)
	defer b.pc.Close()
	stop := make(chan struct{})
	defer close(stop)
	a.speak(stop)
	waitFor(t, "Bo hearing Ada over TCP", func() bool { return b.heardFrom("pageTA") > 10 })
	// the page itself is still served on the port
	code, _ := req(t, "GET", base+"/api/collab/"+id, "", "")
	eq(t, code, 200)
}

// The HTTP port tells ICE from HTTP by the first byte, and a page slow to
// send does not hold up the next one.
func TestSniffedPort(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := newNetPolicy(accessLocal, nil, true)
	x := newExposure(nil, "0", p)
	ice := x.iceListener()
	s := newSniffed(inner, x)
	defer s.Close()
	addr := inner.Addr().String()

	slow, _ := net.Dial("tcp", addr) // says nothing yet
	defer slow.Close()
	h, _ := net.Dial("tcp", addr)
	defer h.Close()
	h.Write([]byte("GET / HTTP/1.1\r\n"))
	got, err := s.Accept()
	if err != nil {
		t.Fatal(err)
	}
	line := make([]byte, 3)
	io.ReadFull(got, line)
	eq(t, string(line), "GET")

	c, _ := net.Dial("tcp", addr)
	defer c.Close()
	c.Write([]byte{0, 3, 1, 2, 3})
	ic, err := ice.Accept()
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 5)
	io.ReadFull(ic, frame)
	eq(t, frame, []byte{0, 3, 1, 2, 3})
}
