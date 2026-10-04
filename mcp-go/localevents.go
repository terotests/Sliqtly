// SPDX-License-Identifier: AGPL-3.0-or-later

// GET /api/events: a page open on a server of one's own hears when a deck
// changes (an assistant's update_presentation, another tab's save) the
// moment it is written, as Server-Sent Events. The editor then compares
// with the folder at once instead of on its next minute; the player
// reloads on the slide it showed (assets/sliqtly-local.js).
//
//	data: {"id":"<deck id>"}

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
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
	var wake chan struct{}
	if rm != nil {
		defer rm.leave(sub, client)
		wake = sub.wake
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // a proxy in front passes them on at once
	w.WriteHeader(200)
	io.WriteString(w, "retry: 3000\n\n")
	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)
	if sub != nil && !writeEvents(w, sub) {
		return
	}
	fl.Flush()
	// a comment now and then keeps proxies from closing a quiet stream
	tick := time.NewTicker(25 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			io.WriteString(w, ": keep-alive\n\n")
		case id := <-ch:
			b, _ := json.Marshal(map[string]string{"id": id})
			io.WriteString(w, "data: "+string(b)+"\n\n")
		case <-wake:
			if !writeEvents(w, sub) {
				// too far behind: it comes back with Last-Event-ID
				fl.Flush()
				return
			}
		}
		fl.Flush()
	}
}
