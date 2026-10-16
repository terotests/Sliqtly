// SPDX-License-Identifier: AGPL-3.0-or-later

// Tampere Taxi Driver's /api/ttd/start (ttd.go) with a model that answers
// what the test says.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type ttdFake struct {
	text  string
	asked string
}

func (f *ttdFake) Ask(ctx context.Context, system string, turns []aiTurn) (aiAnswer, error) {
	f.asked = turns[0].Text
	return aiAnswer{Text: f.text}, nil
}

func ttdPost(t *testing.T, ai *ttdAI, origin, body string) (*httptest.ResponseRecorder, ttdAnswer) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, ttdPath, strings.NewReader(body))
	r.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	ai.serve(w, r)
	var a ttdAnswer
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
	}
	return w, a
}

func TestTTDStart(t *testing.T) {
	f := &ttdFake{text: "```json\n" + `{"name_ok": true, "note": "", "greeting": "Moro Make, hyvää iltavuoroa!", "lines": [
		{"type": 0, "text": "Onks tää se ratikka?"}, {"type": 1, "text": "Where is Näsinneula?"},
		{"type": 7, "text": "ei tällaista tyyppiä"}, {"type": 0, "text": "   "},
		{"type": 0, "text": "kolmas"}, {"type": 0, "text": "neljäs jää pois"}, {"type": 0, "text": "viides"}]}` + "\n```"}
	ai := &ttdAI{model: f, limit: rateLimiter(10, time.Minute), max: 5}
	w, a := ttdPost(t, ai, "https://tamperetaxidriver.app", `{"name": "  Make  ", "types": ["Opiskelija", "Turisti"], "hour": 21}`)
	if w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "https://tamperetaxidriver.app" {
		t.Errorf("CORS %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
	if !a.NameOK || a.Name != "Make" || a.Greeting == "" {
		t.Errorf("name: %+v", a)
	}
	// two types: the unknown one, the empty one and the fourth of type 0 left out
	if len(a.Lines) != 4 {
		t.Errorf("lines: %+v", a.Lines)
	}
	if !strings.Contains(f.asked, "1: Turisti") || !strings.Contains(f.asked, "«Make»") || !strings.Contains(f.asked, "Kello on 21") {
		t.Errorf("asked: %s", f.asked)
	}
}

func TestTTDNameRefused(t *testing.T) {
	f := &ttdFake{text: `{"name_ok": true, "greeting": "Moro!", "lines": []}`}
	ai := &ttdAI{model: f, limit: rateLimiter(10, time.Minute), max: 5}
	// a link is no name, whatever the model says: it is not even asked
	_, a := ttdPost(t, ai, "https://tampere-taxi-driver.web.app", `{"name": "http://x.y", "types": ["A"], "hour": 8}`)
	if a.NameOK || a.Name != "" || a.Note == "" {
		t.Errorf("%+v", a)
	}
	if strings.Contains(f.asked, "http") {
		t.Errorf("the name went to the model: %s", f.asked)
	}
	f.text = `{"name_ok": false, "note": "Kiroilua ei käytetä.", "greeting": "Moro!", "lines": []}`
	_, a = ttdPost(t, ai, "https://tampere-taxi-driver.web.app", `{"name": "Perkele", "types": ["A"], "hour": 8}`)
	if a.NameOK || a.Note != "Kiroilua ei käytetä." || a.Lines == nil {
		t.Errorf("%+v", a)
	}
}

func TestTTDGuards(t *testing.T) {
	f := &ttdFake{text: `{"name_ok": true, "lines": []}`}
	ai := &ttdAI{model: f, limit: rateLimiter(10, time.Minute), max: 2}
	if w, _ := ttdPost(t, ai, "https://example.com", `{"name": "A", "types": ["A"]}`); w.Code != 403 {
		t.Errorf("other page: %d", w.Code)
	}
	if w, _ := ttdPost(t, ai, "https://tamperetaxidriver.app", `{"name": "A", "types": []}`); w.Code != 400 {
		t.Errorf("no types: %d", w.Code)
	}
	for i, want := range []int{200, 200, 429} {
		if w, _ := ttdPost(t, ai, "http://localhost:8080", `{"name": "A", "types": ["A"]}`); w.Code != want {
			t.Errorf("call %d: %d, want %d (daily cap)", i, w.Code, want)
		}
	}
	var none *ttdAI
	if w, _ := ttdPost(t, none, "https://tamperetaxidriver.app", `{"name": "A", "types": ["A"]}`); w.Code != 503 {
		t.Errorf("no model: %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodOptions, ttdPath, nil)
	r.Header.Set("Origin", "https://tamperetaxidriver.app")
	w := httptest.NewRecorder()
	ai.serve(w, r)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Methods") != "POST" {
		t.Errorf("preflight: %d %v", w.Code, w.Header())
	}
}

func TestTTDModel(t *testing.T) {
	g := &geminiModel{model: "gemini-3.8-flash", tools: aiEditTools}
	ai := newTTDAI(g)
	m := ai.model.(*geminiModel)
	if m.tools != nil || m.model != "gemini-3.5-flash-lite" || g.tools == nil {
		t.Errorf("%+v", m)
	}
	if newTTDAI(&ttdFake{}) != nil {
		t.Error("only Gemini answers the taxi")
	}
}
