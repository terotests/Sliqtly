//go:build aieval && !nocloud

// SPDX-License-Identifier: AGPL-3.0-or-later

// The drawer's asks put to the real model (SLIQTLY_AI, as the service has
// it), and what it changed checked: run by hand, it costs a few questions.
//
//	SLIQTLY_AI=gemini SLIQTLY_AI_PROJECT=sliqtly go test -tags aieval -run TestAIEval -v
//
// The workflow AI eval runs it with the deploy account (Actions → AI eval).

package main

import (
	"context"
	"strings"
	"testing"
)

const aiEvalSlide = "## Market share\n\n| Country | Revenue | Share |\n|---|---:|---:|\n| Sweden | 180 | 33% |\n| Norway | 150 | 27% |\n| Finland | 120 | 22% |\n| Denmark | 95 | 18% |\n\n```vega-lite\n{\"data\": {\"values\": [{\"c\": \"Sweden\", \"v\": 180}, {\"c\": \"Norway\", \"v\": 150}, {\"c\": \"Finland\", \"v\": 120}, {\"c\": \"Denmark\", \"v\": 95}]}, \"mark\": {\"type\": \"arc\", \"innerRadius\": 50}, \"encoding\": {\"theta\": {\"field\": \"v\", \"type\": \"quantitative\"}, \"color\": {\"field\": \"c\", \"type\": \"nominal\"}}}\n```\n"

func aiEvalAsk(t *testing.T, m aiModel, message string) aiAnswer {
	t.Helper()
	deck := "---\ntitle: Q3 Review\n---\n\n# Q3 Review\n\nBoard meeting\n\n" + aiEvalSlide
	q := aiQuestion{Message: message, Scope: "slide", Context: aiContext{
		Slide: 2, Slides: 2, Title: "Market share", SlideMD: aiEvalSlide, DeckMD: deck, DeckName: "Q3 Review",
		FrontMatter: "---\ntitle: Q3 Review\n---", Outline: "1. Q3 Review\n2. Market share",
		Theme: "white", Themes: "Basic: white, corporate, editorial\nDark: aurora, carbon, midnight\nEffects: pearl, hive, lattice, apex, tide, mist, nebula, ember",
	}}
	_, uses := m.(aiToolUser)
	a, err := m.Ask(context.Background(), aiSystem(uses), q.turns())
	if err != nil {
		t.Fatalf("%s: %v", message, err)
	}
	t.Logf("%s\n→ %s\n→ %+v", message, a.Text, a.Edits)
	return a
}

func TestAIEval(t *testing.T) {
	m, name := newAIModel(context.Background())
	if m == nil {
		t.Skip("no model: set SLIQTLY_AI")
	}
	t.Log(name)
	for _, ask := range []string{
		"siirrä kaavio oikealle ja tekstit vasemmalle",
		"put the chart on the right and the table on the left",
	} {
		a := aiEvalAsk(t, m, ask)
		var md string
		for _, e := range a.Edits {
			if e.Tool == "replace_slide" || e.Tool == "replace_presentation" {
				md = e.Markdown
			}
		}
		if !strings.Contains(md, "layout=image-right") && !strings.Contains(md, "::: columns") {
			t.Errorf("%s: no image-right layout or columns in %q", ask, md)
		}
		if strings.Contains(md, "style=") || strings.Contains(md, "margin") {
			t.Errorf("%s: CSS in the Markdown: %q", ask, md)
		}
	}
	a := aiEvalAsk(t, m, "voitko vaihtaa tän esityksen vähän tummemaksi")
	dark := false
	for _, e := range a.Edits {
		dark = dark || (e.Tool == "set_theme" && strings.Contains("aurora carbon midnight nebula ember", e.Value))
	}
	if !dark {
		t.Errorf("darker: no dark theme set: %+v", a.Edits)
	}
	// Tero's asks: a set of charts came back as Chart.js in ```chart blocks,
	// and "move the donut higher" as style="margin-top:-60px". Checked on
	// the model's first answer, before the repair round (aiAssist.ask).
	for _, ask := range []string{"Tee näyttävä setti erilaisia chartteja tähän", "siirrä donitsikaaviota ylemmäs"} {
		a := aiEvalAsk(t, m, ask)
		check := aiDeckCheck(&Env{}, context.Background(), "white")
		if p := aiProblems(check, "---\ntitle: Q3 Review\n---", a.Edits); len(p) > 0 {
			t.Errorf("%s: %s", ask, strings.Join(p, "; "))
		}
		for _, e := range a.Edits {
			if e.Tool == "replace_presentation" {
				t.Errorf("%s: replaced the whole presentation", ask)
			}
		}
	}
}
