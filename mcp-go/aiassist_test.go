// SPDX-License-Identifier: AGPL-3.0-or-later

// The editor's AI assistant (aiassist.go): what is sent to the model, and
// the month's AI actions.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// a model that answers by rote and keeps what it was asked
type fakeAI struct {
	asked  [][]aiTurn
	reply  string
	refuse bool
	err    error
}

func (f *fakeAI) Ask(_ context.Context, system string, turns []aiTurn) (aiAnswer, error) {
	f.asked = append(f.asked, turns)
	if f.err != nil {
		return aiAnswer{}, f.err
	}
	return aiAnswer{Text: f.reply, In: 100, Out: 20, Declined: f.refuse}, nil
}

func TestAISystemHasTheGuide(t *testing.T) {
	s := aiSystem()
	match(t, s, "AI assistant inside Sliqtly")
	match(t, s, "# Topic: layout")
	match(t, s, "# Topic: effects")
	if strings.Contains(s, "# Topic: rooms") || strings.Contains(s, "# Topic: apps") {
		t.Fatal("topics a slide does not need went in")
	}
}

func TestAITurns(t *testing.T) {
	q := aiQuestion{Message: "  Make it shorter ", Scope: "slide", Context: aiContext{Slide: 2, Slides: 5, SlideMD: "## Two\n\nText", Selection: "Text", DeckMD: "# All", DeckName: "Demo"},
		History: []aiTurn{{"assistant", "hello"}, {"user", "a"}, {"user", "b"}, {"assistant", "c"}, {"system", "x"}, {"user", "pending"}}}
	got := q.turns()
	// starts with the person, takes turns, the unanswered question dropped
	eq(t, len(got), 3)
	eq(t, []any{got[0].Role, got[0].Text, got[1].Role, got[1].Text}, []any{"user", "a\n\nb", "assistant", "c"})
	last := got[2]
	eq(t, last.Role, "user")
	match(t, last.Text, `<open_slide number="2" of="5">`)
	match(t, last.Text, "<selection>\nText\n</selection>")
	if strings.Contains(last.Text, "<presentation>") {
		t.Fatal("a slide question sent the whole presentation")
	}
	if !strings.HasSuffix(last.Text, "Make it shorter") {
		t.Fatalf("question last: %q", last.Text)
	}
	eq(t, q.cost(), int64(1))
	q.Scope = "deck"
	match(t, q.turns()[2].Text, "<presentation>\n# All\n</presentation>")
	eq(t, q.cost(), int64(2))

	eq(t, len(aiQuestion{Message: "   "}.turns()), 0)
	long := strings.Repeat("ä", aiMaxMessage)
	if c := aiClip(long, aiMaxMessage); !strings.HasSuffix(c, "…") || !strings.HasPrefix(c, "ää") || len(c) > aiMaxMessage+5 {
		t.Fatal("clip cut a letter in two or kept too much")
	}
}

func TestAICredits(t *testing.T) {
	eq(t, aiMonthly(nil, false), int64(aiTrialMonth))
	eq(t, aiMonthly(Doc{"aiMonthly": int64(500)}, false), int64(500))
	eq(t, aiMonthly(Doc{"aiMonthly": int64(500)}, true), int64(-1))
	c := aiCredits{Monthly: 10, Used: 12}
	eq(t, c.left(), int64(0))
	eq(t, aiCredits{Monthly: -1}.left(), int64(-1))
	eq(t, nextMonth(time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC)).Format("2006-01-02"), "2027-01-01")
}

func TestAIRoutes(t *testing.T) {
	e, base, stop := editorServer(t)
	defer stop()
	api := base + "/editor/api/ai/"

	// no model on the server: the drawer hears there is none
	res, body := editorDo(t, "GET", api+"credits", "anna", "", "")
	eq(t, res.StatusCode, 200)
	match(t, body, `"available":false`)

	ai := &fakeAI{reply: "Shorter:\n\n```markdown\n## Two\n```"}
	e.EditorGate.ai = newAIAssist(ai, e.DB)
	e.EditorGate.ai.now = func() time.Time { return editorNow }

	res, _ = editorDo(t, "GET", api+"credits", "", "", "")
	eq(t, res.StatusCode, 401)
	res, body = editorDo(t, "GET", api+"credits", "anna", "", "")
	eq(t, res.StatusCode, 200)
	match(t, body, `"left":50`)
	match(t, body, `"resets":"2026-11-01"`)

	ask := `{"message":"Make it shorter","action":"improve","scope":"slide","context":{"slide":1,"slides":1,"slideMarkdown":"## Two"}}`
	// from another site: refused
	res, _ = editorDo(t, "POST", api+"ask", "anna", "https://evil.example", ask)
	eq(t, res.StatusCode, 403)
	res, body = editorDo(t, "POST", api+"ask", "anna", base, ask)
	eq(t, res.StatusCode, 200, body)
	var out struct {
		Reply   string
		Credits map[string]any
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	eq(t, out.Reply, ai.reply)
	eq(t, out.Credits["left"], float64(49))
	res, body = editorDo(t, "POST", api+"ask", "anna", base, strings.Replace(ask, `"scope":"slide"`, `"scope":"deck"`, 1))
	match(t, body, `"left":47`)
	// counted in the store, with the tokens
	u, _ := e.DB.Get(context.Background(), aiUsageCol, "u-anna")
	m, _ := u["2026-10"].(map[string]any)
	eq(t, []any{docInt(m["actions"]), docInt(m["calls"]), docInt(m["in"])}, []any{int64(3), int64(2), int64(200)})

	// a declined answer costs nothing
	ai.refuse = true
	_, body = editorDo(t, "POST", api+"ask", "anna", base, ask)
	match(t, body, "can't help")
	match(t, body, `"left":47`)
	ai.refuse = false

	// used up: 402 before the model is asked
	if err := e.DB.Set(context.Background(), licenseCol, "u-anna", Doc{"aiMonthly": int64(3)}); err != nil {
		t.Fatal(err)
	}
	n := len(ai.asked)
	res, body = editorDo(t, "POST", api+"ask", "anna", base, ask)
	eq(t, res.StatusCode, 402)
	match(t, body, "used up")
	eq(t, len(ai.asked), n)

	// the owner has no limit
	res, body = editorDo(t, "POST", api+"ask", "tero", base, ask)
	eq(t, res.StatusCode, 200)
	match(t, body, `"unlimited":true`)

	// the model out of reach
	ai.err = errAINotSetUp
	res, body = editorDo(t, "POST", api+"ask", "tero", base, ask)
	eq(t, res.StatusCode, 502)
	match(t, body, "not set up")

	res, _ = editorDo(t, "POST", api+"ask", "tero", base, `{"message":""}`)
	eq(t, res.StatusCode, 400)
}

func TestAIGemini(t *testing.T) {
	var got map[string]any
	var path, key string
	status, reply := 200, `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hi "},{"text":"there"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":7,"thoughtsTokenCount":3}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key = r.URL.Path, r.Header.Get("x-goog-api-key")
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	defer srv.Close()
	m := &geminiModel{http: srv.Client(), model: "gemini-3.8-flash", key: "k1", base: srv.URL}
	a, err := m.Ask(context.Background(), "sys", []aiTurn{{"user", "a"}, {"assistant", "b"}, {"user", "c"}})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, []any{a.Text, a.In, a.Out, a.Declined}, []any{"Hi there", int64(120), int64(10), false})
	eq(t, []any{path, key}, []any{"/v1beta/models/gemini-3.8-flash:generateContent", "k1"})
	cs := got["contents"].([]any)
	eq(t, cs[1].(map[string]any)["role"], "model")
	match(t, fmt.Sprint(got["systemInstruction"]), "sys")

	// on Vertex AI: the project's endpoint, no key
	v := &geminiModel{http: srv.Client(), model: "gemini-3.8-flash", project: "p1", region: "global", base: srv.URL}
	if _, err := v.Ask(context.Background(), "sys", []aiTurn{{"user", "a"}}); err != nil {
		t.Fatal(err)
	}
	eq(t, []any{path, key}, []any{"/v1/projects/p1/locations/global/publishers/google/models/gemini-3.8-flash:generateContent", ""})

	reply = `{"candidates":[{"finishReason":"SAFETY"}]}`
	a, _ = m.Ask(context.Background(), "sys", []aiTurn{{"user", "a"}})
	eq(t, a.Declined, true)
	status, reply = 404, `{"error":{"message":"model not found"}}`
	_, err = m.Ask(context.Background(), "sys", []aiTurn{{"user", "a"}})
	if !errors.Is(err, errAINotSetUp) {
		t.Fatalf("404 should read as not set up: %v", err)
	}
}
