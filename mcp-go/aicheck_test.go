// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"strings"
	"testing"
)

func TestAIProblems(t *testing.T) {
	good := "## Sales {layout=image-right}\n\nText\n\n```vega-lite\n{\"data\": {\"values\": [{\"a\": 1}]}, \"mark\": \"bar\", \"encoding\": {}}\n```\n{width=50%}\n\n```js\nstyle=\"x\"\n```\n"
	eq(t, len(aiProblems([]aiEdit{{Tool: "replace_slide", Slide: 2, Markdown: good}})), 0)
	layered := "```vegalite\n{\"layer\": []}\n```"
	eq(t, len(mdProblems(layered)), 0)

	// what Gemini wrote for "a set of charts": Chart.js in ```chart blocks
	chartjs := "## Q3\n\n```chart\n{\"type\": \"bar\", \"data\": {\"labels\": [\"a\"], \"datasets\": []}}\n```\n"
	p := aiProblems([]aiEdit{{Tool: "insert_slide", After: 3, Markdown: chartjs}})
	eq(t, len(p), 1)
	match(t, p[0], "^the slide inserted after 3: ```chart is not a chart")

	inVega := "```vega-lite\n{\"type\": \"bar\", \"data\": {\"labels\": [\"a\"], \"datasets\": []}}\n```"
	match(t, strings.Join(mdProblems(inVega), "|"), "Chart.js")
	match(t, strings.Join(mdProblems("```vega-lite {.wide}\n{\"data\": 1,}\n```"), "|"), "not one JSON object")
	match(t, strings.Join(mdProblems("```vega-lite\n{\"data\": {\"values\": []}}\n```"), "|"), `no "mark"`)

	// "move the chart up"
	moved := "## Share\n\n<div style=\"margin-top:-60px\">\n\nText {style=\"color: red\"}\n"
	p = aiProblems([]aiEdit{{Tool: "replace_slide", Slide: 4, Markdown: moved}})
	eq(t, len(p), 1)
	match(t, p[0], "^slide 4: style= is ignored")
	// an unclosed fence is read to the end
	eq(t, len(mdProblems("```chart\nx")), 0)
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
	bad := aiAnswer{Text: "Added charts.", In: 100, Out: 50, Edits: []aiEdit{{Tool: "insert_slide", After: 1, Markdown: "## A\n\n```chart\n{}\n```"}}}
	good := aiAnswer{Text: "Added a bar chart.", In: 300, Out: 60, Edits: []aiEdit{{Tool: "insert_slide", After: 1, Markdown: "## A\n\n```vega-lite\n{\"mark\": \"bar\"}\n```"}}}
	m := &scriptedAI{answers: []aiAnswer{bad, good}}
	a := &aiAssist{model: m}
	turns := []aiTurn{{Role: "user", Text: "Tee näyttävä setti erilaisia chartteja tähän"}}
	ans, err := a.ask(context.Background(), turns)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, len(m.asked), 2)
	last := m.asked[1]
	eq(t, len(last), 3)
	eq(t, last[1].Text, "Added charts.")
	match(t, last[2].Text, "slide inserted after 1: ```chart is not a chart")
	eq(t, ans.Text, "Added a bar chart.")
	eq(t, []int64{ans.In, ans.Out}, []int64{400, 110})

	// a clean answer is asked once
	m = &scriptedAI{answers: []aiAnswer{good}}
	a = &aiAssist{model: m}
	ans, _ = a.ask(context.Background(), turns)
	eq(t, len(m.asked), 1)
	eq(t, ans.Text, "Added a bar chart.")

	// a second answer with no changes: the first stands
	m = &scriptedAI{answers: []aiAnswer{bad, {Text: "Sorry."}}}
	a = &aiAssist{model: m}
	ans, _ = a.ask(context.Background(), turns)
	eq(t, ans.Text, "Added charts.")
}
