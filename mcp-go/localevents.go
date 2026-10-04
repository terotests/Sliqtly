// SPDX-License-Identifier: AGPL-3.0-or-later

// A page open on a server of one's own keeps one stream to it, and hears on
// it when a deck changes (an assistant's update_presentation, another tab's
// save) the moment it is written: the editor then compares with the folder
// at once instead of on its next minute; the player reloads on the slide it
// showed (assets/sliqtly-local.js). The same stream carries the server's own
// state (localstatus.go), when the page connects and when it changes, and,
// for a page editing a deck with others, that deck's room (collab.go).
//
// GET /api/socket is the stream as a WebSocket, one JSON message each:
//
//	{"k":"status","v":{"state":"ready","version":"1.1.3"}}
//	{"k":"changed","id":"<deck id>"}
//	{"k":"room","id":12,"v":{"t":"op",…}}      (id: the rev, when it has one)
//
// GET /api/events is the same as Server-Sent Events, for a page that cannot
// open a WebSocket (a proxy in front that does not pass them on):
//
//	event: status
//	data: {"state":"ready","version":"1.1.3"}
//
//	data: {"id":"<deck id>"}
//
//	id: 12
//	data: {"t":"op",…}
//
// Why a WebSocket: a browser opens at most six HTTP/1.1 connections to one
// server, for all its tabs together, and an event stream holds one for as
// long as the page is open. Three tabs with two streams each took all six,
// and every other request of every tab (opening a deck, saving it) waited
// until one closed. WebSockets are counted apart from those six, and a
// page keeps one stream, not one per purpose. (HTTP/2 would lift the limit
// too, but browsers speak it only over TLS, and this server is plain HTTP.)
//
// A server that is stopping says so and closes the stream.

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

// who is listening, and what to tell them
type changeHub struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

func newChangeHub() *changeHub { return &changeHub{subs: map[chan string]struct{}{}} }

func (h *changeHub) subscribe() chan string {
	ch := make(chan string, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *changeHub) unsubscribe(ch chan string) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// a listener too slow to take it misses one; the next change, or its
// minute's check, catches it up
func (h *changeHub) publish(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- id:
		default:
		}
	}
}

// the deck changes the folder sees: every write to a share
func (h *changeHub) written(col, id string, _ Doc) {
	if col == "shares" {
		h.publish(id)
	}
}

// what a page is told, by either transport
type eventSink interface {
	status(st serverStatus)
	changed(id string)
	room(e collabEvt)
	keepAlive()
	flush() error
}

// Server-Sent Events
type sseSink struct {
	w  io.Writer
	fl http.Flusher
}

func (k sseSink) status(st serverStatus) { io.WriteString(k.w, statusEvent(st)) }
func (k sseSink) changed(id string) {
	b, _ := json.Marshal(map[string]string{"id": id})
	io.WriteString(k.w, "data: "+string(b)+"\n\n")
}
func (k sseSink) room(e collabEvt) { k.w.Write(e.sse()) }
func (k sseSink) keepAlive()       { io.WriteString(k.w, ": keep-alive\n\n") }
func (k sseSink) flush() error     { k.fl.Flush(); return nil }

// a WebSocket: messages wait in out until flush sends them
type wsSink struct {
	ws  *websocket.Conn
	out [][]byte
}

func (k *wsSink) add(v any) {
	b, _ := json.Marshal(v)
	k.out = append(k.out, b)
}
func (k *wsSink) status(st serverStatus) { k.add(map[string]any{"k": "status", "v": st}) }
func (k *wsSink) changed(id string)      { k.add(map[string]any{"k": "changed", "id": id}) }
func (k *wsSink) room(e collabEvt) {
	m := map[string]any{"k": "room", "v": json.RawMessage(e.data)}
	if e.id > 0 {
		m["id"] = e.id
	}
	k.add(m)
}

// a word the page passes over: a connection gone without a word is found
// when this cannot be sent
func (k *wsSink) keepAlive() { k.add(map[string]any{"k": "ping"}) }
func (k *wsSink) flush() error {
	k.ws.SetWriteDeadline(time.Now().Add(30 * time.Second))
	for _, b := range k.out {
		if err := websocket.Message.Send(k.ws, string(b)); err != nil {
			return err
		}
	}
	k.out = k.out[:0]
	return nil
}

// A WebSocket stream: `serve` writes to the page until ctx ends, which is
// when the page goes (or closes it). Any page may open one, as the rest of
// /api/ answers any origin (Access-Control-Allow-Origin: *); it only tells.
func serveSocket(w http.ResponseWriter, r *http.Request, serve func(ctx context.Context, out *wsSink)) {
	websocket.Server{
		Handshake: func(*websocket.Config, *http.Request) error { return nil },
		Handler: func(ws *websocket.Conn) {
			defer ws.Close()
			ctx, done := context.WithCancel(context.Background())
			defer done()
			// the page sends nothing; its going is the end of the stream
			go func() {
				defer done()
				var msg string
				for websocket.Message.Receive(ws, &msg) == nil {
				}
			}()
			serve(ctx, &wsSink{ws: ws})
		},
	}.ServeHTTP(w, r)
}

func (s *localServer) socket(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		http.Error(w, "no events here", 500)
		return
	}
	// joined before the upgrade, so a wrong room is a plain 400
	rm, sub, client, err := s.joinRoom(r)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if rm != nil {
		defer rm.leave(sub, client)
	}
	serveSocket(w, r, func(ctx context.Context, out *wsSink) { s.stream(ctx, out, sub) })
}

func (s *localServer) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok || s.hub == nil {
		http.Error(w, "no events here", 500)
		return
	}
	// a page editing a deck with others hears them on the same stream
	// (collab.go)
	rm, sub, client, err := s.joinRoom(r)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if rm != nil {
		defer rm.leave(sub, client)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // a proxy in front passes them on at once
	w.WriteHeader(200)
	io.WriteString(w, "retry: 3000\n\n")
	s.stream(r.Context(), sseSink{w, fl}, sub)
}

// the stream itself, until the page goes or the server stops
func (s *localServer) stream(ctx context.Context, out eventSink, sub *collabSub) {
	st, changed := s.board.get()
	out.status(st)
	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)
	var wake chan struct{}
	if sub != nil {
		wake = sub.wake
		if !roomEvents(out, sub) {
			return
		}
	}
	if out.flush() != nil {
		return
	}
	// a word now and then keeps proxies from closing a quiet stream
	tick := time.NewTicker(25 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
			st, changed = s.board.get()
			out.status(st)
			if st.State == "stopping" {
				out.flush()
				return
			}
		case <-tick.C:
			out.keepAlive()
		case id := <-ch:
			out.changed(id)
		case <-wake:
			if !roomEvents(out, sub) {
				// too far behind: it comes back with the rev it had
				out.flush()
				return
			}
		}
		if out.flush() != nil {
			return
		}
	}
}

// the room's events waiting for this page; false when it fell too far behind
func roomEvents(out eventSink, sub *collabSub) bool {
	q, over := sub.take()
	for _, e := range q {
		out.room(e)
	}
	return !over
}
