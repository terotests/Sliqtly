// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// The model reads, hears the answer and sees the picture, then changes the
// slide and answers.
func TestAIGeminiReads(t *testing.T) {
	var asks []map[string]any
	replies := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"sliqtly_guide","args":{"topic":"smartart"}},"thoughtSignature":"s1"},{"functionCall":{"name":"look_at_slide","args":{"slide":2}}}]}}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":5}}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"replace_slide","args":{"slide":2,"markdown":"## Old"}}},{"functionCall":{"name":"look_at_slide","args":{"slide":2}}}]}}],"usageMetadata":{"promptTokenCount":200,"candidatesTokenCount":5}}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Made it a cycle."},{"functionCall":{"name":"replace_slide","args":{"slide":2,"markdown":"## Cycle"}}}]}}],"usageMetadata":{"promptTokenCount":300,"candidatesTokenCount":9}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		json.NewDecoder(r.Body).Decode(&got)
		asks = append(asks, got)
		io.WriteString(w, replies[len(asks)-1])
	}))
	defer srv.Close()
	var ran []string
	r := &aiReader{
		Tools: []aiTool{{"sliqtly_guide", "the guide", aiObj(map[string]any{})}, aiLookTools[0]},
		Run: func(_ context.Context, name string, args map[string]any) aiRead {
			ran = append(ran, fmt.Sprintf("%s %v", name, args))
			if name == "look_at_slide" {
				return aiRead{Text: "slide 2 drawn", Images: [][]byte{[]byte(fakePNG)}}
			}
			return aiRead{Text: "```smartart cycle"}
		},
	}
	m := &geminiModel{http: srv.Client(), model: "g", key: "k", base: srv.URL, tools: aiEditTools}
	a, err := m.AskReading(context.Background(), "sys", []aiTurn{{Role: "user", Text: "make it a cycle"}}, r)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, ran, []string{"sliqtly_guide map[topic:smartart]", "look_at_slide map[slide:2]", "look_at_slide map[slide:2]"})
	eq(t, []any{a.Text, a.Calls, a.In, a.Out}, []any{"Made it a cycle.", int64(3), int64(600), int64(19)})
	// the last round's change stands, the one made before looking again does not
	eq(t, a.Edits, []aiEdit{{Tool: "replace_slide", Slide: 2, Markdown: "## Cycle"}})
	match(t, fmt.Sprint(asks[0]["tools"]), "look_at_slide")
	// the second call carries the model's turn as it came, signature too, and
	// the answers with the picture
	raw, _ := json.Marshal(asks[1]["contents"])
	match(t, string(raw), `"thoughtSignature":"s1"`)
	match(t, string(raw), `"content":"`+"```smartart cycle")
	match(t, string(raw), `"inlineData":{"data":"`+base64.StdEncoding.EncodeToString([]byte(fakePNG))+`","mimeType":"image/png"}`)
}

// A model that will not take pictures in a function's answer gets the words.
func TestAIGeminiNoPictures(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		switch {
		case calls == 1:
			io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"look_at_presentation","args":{}}}]}}]}`)
		case strings.Contains(string(body), "inlineData"):
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":"parts not supported"}}`)
		default:
			if !strings.Contains(string(body), "cannot be shown to you") {
				t.Errorf("the model is not told the picture is gone: %s", body)
			}
			io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"Looks fine."}]}}]}`)
		}
	}))
	defer srv.Close()
	r := &aiReader{Tools: aiLookTools, Run: func(context.Context, string, map[string]any) aiRead {
		return aiRead{Text: "3 slides", Images: [][]byte{[]byte(fakePNG)}}
	}}
	m := &geminiModel{http: srv.Client(), model: "g", key: "k", base: srv.URL}
	a, err := m.AskReading(context.Background(), "sys", []aiTurn{{Role: "user", Text: "ok?"}}, r)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, []any{a.Text, a.Calls}, []any{"Looks fine.", int64(3)})
}

// A model that keeps reading is told to stop, and has one call left to
// answer in.
func TestAIGeminiRoundsEnd(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"look_at_presentation","args":{}}}]}}]}`)
	}))
	defer srv.Close()
	runs := 0
	r := &aiReader{Tools: aiLookTools, Run: func(context.Context, string, map[string]any) aiRead {
		runs++
		return aiRead{Text: "x"}
	}}
	m := &geminiModel{http: srv.Client(), model: "g", key: "k", base: srv.URL}
	a, _ := m.AskReading(context.Background(), "sys", []aiTurn{{Role: "user", Text: "?"}}, r)
	eq(t, []any{calls, runs, a.Calls}, []any{geminiRounds, geminiRounds - 2, int64(geminiRounds)})
}

func TestAIReadOf(t *testing.T) {
	png := base64.StdEncoding.EncodeToString([]byte("P1"))
	r := aiReadOf(`{"content":[{"type":"text","text":"a"},{"type":"image","data":"` + png + `","mimeType":"image/png"},{"type":"text","text":"b"},{"type":"image","data":"` + png + `"},{"type":"image","data":"` + png + `"}],"isError":true}`)
	eq(t, []any{r.Text, len(r.Images), string(r.Images[0]), r.Failed}, []any{"a\nb", aiReadImages, "P1", true})
	eq(t, aiReadOf("not json").Failed, true)
}

func TestAISchema(t *testing.T) {
	var s map[string]any
	json.Unmarshal([]byte(`{"type":"object","additionalProperties":false,"$schema":"x","properties":{"deck_id":{"type":["string","null"],"description":"d","default":"z"},"slides":{"type":"array","items":{"type":"integer","minimum":1}},"opts":{"type":"object"}},"required":["deck_id"]}`), &s)
	out, _ := json.Marshal(aiSchema(s))
	eq(t, string(out), `{"properties":{"deck_id":{"description":"d","type":"string"},"opts":{"properties":{},"type":"object"},"slides":{"items":{"minimum":1,"type":"integer"},"type":"array"}},"required":["deck_id"],"type":"object"}`)
}

// The reader offers MCP's read tools, not its writes, and runs them as the
// person asking.
func TestAIDeckReader(t *testing.T) {
	f := fakeFirebase()
	env := testEnv(&f, nil)
	r := aiDeckReader(env, "u1", aiQuestion{Context: aiContext{DeckMD: "# Deck\n\n## One\n\nHello\n", DeckID: "abcdef12"}})
	var names []string
	for _, t := range r.Tools {
		names = append(names, t.Name)
	}
	for _, want := range []string{"look_at_slide", "look_at_presentation", "sliqtly_guide", "render_slide", "list_files", "read_file"} {
		if !slices.Contains(names, want) {
			t.Errorf("no %s in %v", want, names)
		}
	}
	for _, not := range []string{"update_presentation", "create_presentation", "delete_presentation", "list_presentations"} {
		if slices.Contains(names, not) {
			t.Errorf("%s offered", not)
		}
	}
	got := r.Run(context.Background(), "sliqtly_guide", map[string]any{"topic": "smartart"})
	match(t, got.Text, "smartart")
	got = r.Run(context.Background(), "look_at_slide", map[string]any{"slide": 1})
	if got.Failed || len(got.Images) != 1 {
		t.Fatalf("the open slide not drawn: %+v", got.Text)
	}
	got = r.Run(context.Background(), "update_presentation", map[string]any{})
	eq(t, got.Failed, true)
}

// add_file writes text files where the deck keeps them, nowhere else.
func TestAIAddFile(t *testing.T) {
	for in, want := range map[string]string{
		"media/forest.svg": "media/forest.svg", "/data/sales.csv": "data/sales.csv", "charts/c.json": "charts/c.json",
		"code/app.tsx": "code/app.tsx", "media/x.png": "", "media/../x.svg": "", "x.svg": "", "media/.svg": "",
		"media/a b.svg": "", "other/x.svg": "", "data/x": "", "media/sub/x.svg": "",
	} {
		eq(t, aiFilePath(in), want)
	}
	e, ok := aiEditOf("add_file", map[string]any{"path": "media/forest.svg", "text": "<svg/>"})
	eq(t, []any{ok, e}, []any{true, aiEdit{Tool: "add_file", Path: "media/forest.svg", Text: "<svg/>"}})
	_, ok = aiEditOf("add_file", map[string]any{"path": "../etc/x.svg", "text": "<svg/>"})
	eq(t, ok, false)
	_, ok = aiEditOf("add_file", map[string]any{"path": "data/a.csv", "text": " "})
	eq(t, ok, false)
}

// The model reads the CSS tab and sees the slides drawn with it.
func TestAIReadThemeCSS(t *testing.T) {
	f := fakeFirebase()
	css := "page { background-color: #ff0000; }\ndocument { color: #ffffff; font-family: Open Sans; }\n"
	r := aiDeckReader(testEnv(&f, nil), "u1", aiQuestion{Context: aiContext{DeckMD: "# A\n\nText\n", CSS: css}})
	eq(t, r.Run(context.Background(), "read_theme_css", nil).Text, css)
	got := r.Run(context.Background(), "look_at_slide", map[string]any{"slide": 1})
	if len(got.Images) != 1 {
		t.Fatalf("not drawn: %s", got.Text)
	}
	img, _, err := image.Decode(bytes.NewReader(got.Images[0]))
	if err != nil {
		t.Fatal(err)
	}
	cr, cg, cb, _ := img.At(5, 5).RGBA()
	if cr>>8 < 230 || cg>>8 > 30 || cb>>8 > 30 {
		t.Fatalf("the page is not the CSS tab's red: %d %d %d", cr>>8, cg>>8, cb>>8)
	}
	empty := aiDeckReader(testEnv(&f, nil), "u1", aiQuestion{Context: aiContext{Theme: "forge"}})
	match(t, empty.Run(context.Background(), "read_theme_css", nil).Text, `forge\) as it is`)
}

// bytes that read as a PNG
const fakePNG = "\x89PNG\r\n\x1a\n0000IHDR"
