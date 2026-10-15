// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type mapPresence struct {
	mu     sync.Mutex
	docs   map[string]Doc
	writes int
}

func (m *mapPresence) put(_ context.Context, deck, agent string, d Doc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.docs == nil {
		m.docs = map[string]Doc{}
	}
	cur := m.docs[deck+"/"+agent]
	if cur == nil {
		cur = Doc{}
	}
	for k, v := range d {
		cur[k] = v
	}
	m.docs[deck+"/"+agent] = cur
	m.writes++
	return nil
}

func (m *mapPresence) drop(_ context.Context, deck, agent string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.docs, deck+"/"+agent)
	return nil
}

// An assistant's calls on sliqtly.com are told to Firestore when what the
// page shows changes or the refresh is due, never for a call that failed.
func TestPresenceCloudTells(t *testing.T) {
	st := &mapPresence{}
	b := &agentBoard{pub: newPresencePub(st)}
	sid := "s-1"
	key := sessionAgent(sid)
	answer := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`
	post := func(body string) {
		t.Helper()
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
		r.Header.Set("Mcp-Session-Id", sid)
		w := httptest.NewRecorder()
		b.watchMcp(w, r, func(w http.ResponseWriter) {
			io.Copy(io.Discard, r.Body)
			if strings.Contains(body, `"initialize"`) {
				w.Header().Set("Mcp-Session-Id", sid)
			}
			io.WriteString(w, answer)
		})
	}
	call := func(tool, args string) string {
		return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
	}
	post(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"clientInfo":{"name":"claude-code","title":"Claude Code"}}}`)
	post(call("get_presentation", `{"deck_id":"Deck1"}`))
	d := st.docs["Deck1/"+key]
	if d == nil || d["client"] != "Claude Code" || d["tool"] != "get_presentation" || d["id"] != key || d["first"] == nil || d["expires"] == nil {
		t.Fatalf("first call: %+v", st.docs)
	}
	if _, has := d["name"]; has {
		t.Fatalf("a name it never gave: %+v", d)
	}
	// the same again: nothing the page shows changed
	post(call("render_slide", `{"deck_id":"Deck1","slide":2}`))
	if st.writes != 1 {
		t.Fatalf("written again at once: %d", st.writes)
	}
	// it names itself and holds slides: told at once
	answer = `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"claimed\":true}"}],"structuredContent":{"claimed":true}}}`
	post(call("begin_work", `{"deck_id":"Deck1","agent":"Writer","slides":[2,3],"note":"charts"}`))
	if d := st.docs["Deck1/"+key]; st.writes != 2 || d["name"] != "Writer" || d["note"] != "charts" || len(d["slides"].([]any)) != 2 {
		t.Fatalf("after the claim (%d): %+v", st.writes, d)
	}
	// the refresh: told again once presenceRefresh has passed
	b.pub.mu.Lock()
	for k, s := range b.pub.sent {
		s.at = s.at.Add(-presenceRefresh)
		b.pub.sent[k] = s
	}
	b.pub.mu.Unlock()
	answer = `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`
	post(call("get_presentation", `{"deck_id":"Deck1"}`))
	if st.writes != 3 {
		t.Fatalf("not refreshed: %d", st.writes)
	}
	// a call that failed (another's Story) or an error is not told
	answer = `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"No such presentation"}],"isError":true}}`
	post(call("get_presentation", `{"deck_id":"Other"}`))
	answer = `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"bad"}}`
	post(call("get_presentation", `{"deck_id":"Other2"}`))
	// nor a deck id Firestore would not take
	answer = `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`
	post(call("get_presentation", `{"deck_id":"a/b"}`))
	if len(st.docs) != 1 {
		t.Fatalf("told: %+v", st.docs)
	}
	// its session ends: gone
	r := httptest.NewRequest("DELETE", "/mcp", nil)
	r.Header.Set("Mcp-Session-Id", sid)
	b.watchMcp(httptest.NewRecorder(), r, func(w http.ResponseWriter) { w.WriteHeader(204) })
	if len(st.docs) != 0 {
		t.Fatalf("after its session ended: %+v", st.docs)
	}
}

// a long answer's end still tells it failed
func TestFailedAnswerAtTheEnd(t *testing.T) {
	var buf strings.Builder
	rec := httptest.NewRecorder()
	ends := &answerEnds{}
	tw := teeWriter{rec, &bytes.Buffer{}, answerEnd, ends}
	tw.Write([]byte(`{"result":{"content":[{"type":"text","text":"` + strings.Repeat("x", 5000)))
	tw.Write([]byte(`"}],"isError":true}}`))
	buf.WriteString(rec.Body.String())
	if !failedAnswer(tw.buf.Bytes(), ends.tail) {
		t.Fatal("not seen")
	}
	if failedAnswer([]byte(`{"result":{"content":[{"type":"text","text":"say \"isError\":true"}]}}`), nil) {
		t.Fatal("words in the content")
	}
	if tw.buf.Len() != answerEnd || len(ends.tail) != answerEnd || buf.Len() < 5000 {
		t.Fatalf("kept %d / %d", tw.buf.Len(), len(ends.tail))
	}
}
