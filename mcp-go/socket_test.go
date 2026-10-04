// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// a page's one stream as a WebSocket (localevents.go)
type testSocket struct {
	t    *testing.T
	ws   *websocket.Conn
	msgs chan map[string]any
}

func dialSocket(t *testing.T, base, query string) *testSocket {
	t.Helper()
	url := "ws" + strings.TrimPrefix(base, "http") + "/api/socket" + query
	ws, err := websocket.Dial(url, "", base)
	if err != nil {
		t.Fatal(err)
	}
	s := &testSocket{t: t, ws: ws, msgs: make(chan map[string]any, 1000)}
	go func() {
		for {
			var raw string
			if websocket.Message.Receive(ws, &raw) != nil {
				close(s.msgs)
				return
			}
			var m map[string]any
			if json.Unmarshal([]byte(raw), &m) == nil {
				s.msgs <- m
			}
		}
	}()
	return s
}

// the next message for which `ok` holds, the others before it skipped
func (s *testSocket) next(what string, ok func(map[string]any) bool) map[string]any {
	s.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m, open := <-s.msgs:
			if !open {
				s.t.Fatalf("the socket closed before %s", what)
			}
			if ok(m) {
				return m
			}
		case <-deadline:
			s.t.Fatalf("no %s", what)
		}
	}
}

func roomMsg(t string) func(map[string]any) bool {
	return func(m map[string]any) bool {
		v, _ := m["v"].(map[string]any)
		return m["k"] == "room" && v["t"] == t
	}
}

// One stream carries all three: the server's state, decks changing, and the
// room of the deck being edited; a page that comes back names the rev it
// had and gets the edits it missed.
func TestLocalSocket(t *testing.T) {
	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	id := newDeck(t, srv.URL, "# Title\n")
	q := fmt.Sprintf("?room=%s&client=pageS1&who=wS&name=Ada&color=%%23ea580c&rev=0", id)
	s := dialSocket(t, srv.URL, q)
	first := s.next("status", func(map[string]any) bool { return true })
	eq(t, first["k"], "status")
	eq(t, first["v"].(map[string]any)["state"], "ready")
	s.next("peers", roomMsg("peers"))

	code, body := req(t, "POST", srv.URL+"/api/collab/"+id+"/op", "application/json",
		`{"client":"pageS1","rev":0,"seq":1,"ops":[{"retain":2},{"insert":"New "}]}`)
	eq(t, code, 200, body)
	op := s.next("op", roomMsg("op"))
	eq(t, op["id"], float64(1))
	eq(t, op["v"].(map[string]any)["rev"], float64(1))

	c := call(t, &testServer{session: session}, "create_presentation", map[string]any{"title": "E", "markdown": "# E\n"})
	made := sc(c)["deck_id"].(string)
	s.next("changed "+made, func(m map[string]any) bool { return m["k"] == "changed" && m["id"] == made })
	s.ws.Close()

	// back, with rev 0: the edit it missed comes first
	again := dialSocket(t, srv.URL, q)
	defer again.ws.Close()
	op = again.next("op again", roomMsg("op"))
	eq(t, op["id"], float64(1))

	// a wrong room is said before the upgrade
	if _, err := websocket.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/socket?room=x", "", srv.URL); err == nil {
		t.Fatal("a socket for a wrong room was opened")
	}
}
