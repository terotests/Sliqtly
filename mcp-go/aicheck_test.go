// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"strings"
	"testing"
)

// what Gemini wrote for "Tee näyttävä setti erilaisia chartteja tähän":
// Chart.js in ```chart blocks
const aiChartJs = "## Q3\n\n```chart\n{\"type\": \"bar\", \"data\": {\"labels\": [\"a\"], \"datasets\": [{\"data\": [1]}]}}\n```\n"

func TestAIProblems(t *testing.T) {
	check := aiDeckCheck(&Env{}, context.Background(), "white")
	fm := "---\ntitle: Q3\n---"
	good := "## Sales {layout=image-right}\n\nText\n\n```vega-lite\n{\"data\": {\"values\": [{\"a\": \"x\", \"b\": 1}]}, \"mark\": \"bar\", \"encoding\": {\"x\": {\"field\": \"a\", \"type\": \"nominal\"}, \"y\": {\"field\": \"b\", \"type\": \"quantitative\"}}}\n```\n"
	eq(t, aiProblems(check, aiContext{FrontMatter: fm}, []aiEdit{{Tool: "replace_slide", Slide: 2, Markdown: good}}), []string(nil))

	p := aiProblems(check, aiContext{FrontMatter: fm}, []aiEdit{{Tool: "set_theme", Value: "midnight"}, {Tool: "insert_slide", After: 3, Markdown: aiChartJs}})
	eq(t, len(p), 1, p)
	match(t, p[0], "^insert_slide after 3: The ```chart block on slide \"Q3\" is shown as code")

	inVega := "## A\n\n```vega-lite\n{\"type\": \"bar\", \"data\": {\"labels\": [\"a\"], \"datasets\": []}}\n```\n"
	match(t, strings.Join(aiProblems(check, aiContext{FrontMatter: fm}, []aiEdit{{Tool: "replace_slide", Slide: 1, Markdown: inVega}}), "|"), `Chart.js \("datasets"\), drawn empty`)
	noMark := "## A\n\n```vega-lite\n{\"data\": {\"values\": [{\"a\": 1}]}}\n```\n"
	match(t, strings.Join(aiProblems(check, aiContext{FrontMatter: fm}, []aiEdit{{Tool: "replace_slide", Slide: 1, Markdown: noMark}}), "|"), `no "mark"`)
	// "move the donut higher"
	moved := "## Share\n\n<div style=\"margin-top:-60px\">x</div>\n"
	match(t, strings.Join(aiProblems(check, aiContext{FrontMatter: fm}, []aiEdit{{Tool: "replace_slide", Slide: 4, Markdown: moved}}), "|"), "^replace_slide 4: HTML on slide \"Share\": style margin-top")
	eq(t, aiThemeAfter("white", []aiEdit{{Tool: "set_theme", Value: "carbon"}}), "carbon")
}

// a model that answers with each of its answers in turn
type scriptedAI struct {
	asked   [][]aiTurn
	answers []aiAnswer
}

func (s *scriptedAI) Ask(_ context.Context, _ string, turns []aiTurn) (aiAnswer, error) {
	s.asked = append(s.asked, turns)
	a := s.answers[0]
	if len(s.answers) > 1 {
		s.answers = s.answers[1:]
	}
	return a, nil
}

func TestAIRepairRound(t *testing.T) {
	check := func(theme string) aiChecker { return aiDeckCheck(&Env{}, context.Background(), theme) }
	bad := aiAnswer{Text: "Added charts.", In: 100, Out: 50, Edits: []aiEdit{{Tool: "insert_slide", After: 1, Markdown: aiChartJs}}}
	good := aiAnswer{Text: "Added a bar chart.", In: 300, Out: 60, Edits: []aiEdit{{Tool: "insert_slide", After: 1, Markdown: "## A\n\n```vega-lite\n{\"mark\": \"bar\"}\n```"}}}
	q := aiQuestion{Message: "Tee näyttävä setti erilaisia chartteja tähän", Scope: "slide", Context: aiContext{Slide: 1, Slides: 1, SlideMD: "## One"}}
	m := &scriptedAI{answers: []aiAnswer{bad, good}}
	a := &aiAssist{model: m}
	ans, err := a.ask(context.Background(), q, check, nil)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, len(m.asked), 2)
	last := m.asked[1]
	n := len(last)
	eq(t, last[n-2].Text, "Added charts.")
	match(t, last[n-1].Text, "insert_slide after 1: The ```chart block on slide \"Q3\" is shown as code")
	eq(t, ans.Text, "Added a bar chart.")
	eq(t, []int64{ans.In, ans.Out, ans.Calls}, []int64{400, 110, 2})

	// a clean answer is asked once
	m = &scriptedAI{answers: []aiAnswer{good}}
	a = &aiAssist{model: m}
	ans, _ = a.ask(context.Background(), q, check, nil)
	eq(t, len(m.asked), 1)
	eq(t, ans.Text, "Added a bar chart.")

	// a second answer with no changes: the first stands
	m = &scriptedAI{answers: []aiAnswer{bad, {Text: "Sorry."}}}
	a = &aiAssist{model: m}
	ans, _ = a.ask(context.Background(), q, check, nil)
	eq(t, ans.Text, "Added charts.")
	eq(t, ans.Calls, int64(2))
}

// A theme of its own: CSS added after the theme's, checked on the whole
// presentation; only what the new rules add is said.
func TestAIThemeCSS(t *testing.T) {
	e, ok := aiEditOf("add_theme_css", map[string]any{"css": " page { background-color: #0d1b2a; } "})
	eq(t, []any{ok, e}, []any{true, aiEdit{Tool: "add_theme_css", CSS: "page { background-color: #0d1b2a; }"}})
	_, ok = aiEditOf("replace_theme_css", map[string]any{"css": ""})
	eq(t, ok, false)
	eq(t, aiCSSAfter("a { }\n", []aiEdit{{Tool: "add_theme_css", CSS: "b { }"}}), "a { }\n\nb { }\n")
	eq(t, aiCSSAfter("a { }", []aiEdit{{Tool: "add_theme_css", CSS: "b { }"}, {Tool: "replace_theme_css", CSS: "c { }"}}), "c { }")

	check := aiDeckCheck(&Env{}, context.Background(), "white")
	c := aiContext{DeckMD: "# Sport\n\n## Treeni\n\n- Lämmittely\n- Juoksu\n", CSS: "page { background-color: #ffffff; }\ndocument { color: #222222; font-family: Open Sans; }\n"}
	eq(t, aiProblems(check, c, []aiEdit{{Tool: "add_theme_css", CSS: "heading { color: #ff5a1f; text-transform: uppercase; }"}}), []string(nil))
	p := aiProblems(check, c, []aiEdit{{Tool: "add_theme_css", CSS: "page { background: linear-gradient(#000, #fff); }"}})
	if len(p) == 0 {
		t.Fatal("a gradient is not said")
	}
	match(t, p[0], "^theme css: ")
}
