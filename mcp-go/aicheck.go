// SPDX-License-Identifier: AGPL-3.0-or-later

// The assistant's changes, checked before the editor makes them. A model
// writes what it has seen elsewhere: Chart.js JSON in a ```chart block, or
// style="margin-top: -60px" to move a chart up. Sliqtly shows the first as
// code and ignores the second, and the person sees the assistant say it did
// something it did not. The changes go through the check update_presentation
// gives an assistant over MCP (Tools.checkDeck: blocks not shown, charts that
// draw nothing, slides that do not fit, HTML that is not drawn), and the
// model gets one round to fix what it reports.

package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// what the check says about one piece of a presentation's Markdown, laid
// out with css (the whole stylesheet, as the editor's CSS tab holds it; ""
// the theme's own)
type aiChecker func(md, css string) []string

// The deck check MCP runs, on Markdown laid out with the theme (aurora, as
// MCP's, when the editor named none Sliqtly has).
func aiDeckCheck(env *Env, ctx context.Context, theme string) aiChecker {
	theme = aiKnownTheme(theme)
	return func(md, css string) []string {
		h := &McpHost{env: env, ctx: ctx, images: map[int64][]byte{}}
		bgs := Tools_static_backgrounds(h, md, css, nil, "")
		return Tools_static_checkDeck(h, md, theme, css != "", css, bgs, nil, "", nil).warnings
	}
}

// the theme a presentation is laid out with: aurora, as MCP's, when the
// editor named none Sliqtly has
func aiKnownTheme(theme string) string {
	if !slices.Contains(Deck_static_THEMES(), theme) {
		return "aurora"
	}
	return theme
}

// the stylesheet the edits leave: css with rules added or replaced
func aiCSSAfter(css string, edits []aiEdit) string {
	for _, e := range edits {
		switch e.Tool {
		case "replace_theme_css":
			css = e.CSS
		case "add_theme_css":
			css = strings.TrimRight(css, "\n") + "\n\n" + e.CSS + "\n"
		}
	}
	return css
}

// What the check says about the Markdown the edits write, each line saying
// which edit; none when Sliqtly draws all of it. A slide is checked under
// the presentation's front matter and the stylesheet the edits leave, as it
// will be shown. A changed stylesheet is checked on the whole presentation,
// and only what it adds to the warnings is said.
func aiProblems(check aiChecker, c aiContext, edits []aiEdit) []string {
	var out []string
	css := aiCSSAfter(c.CSS, edits)
	for _, e := range edits {
		if e.Markdown == "" {
			continue
		}
		where, md := "replace_presentation", e.Markdown
		switch e.Tool {
		case "replace_slide":
			where = fmt.Sprintf("replace_slide %d", e.Slide)
		case "insert_slide":
			where = fmt.Sprintf("insert_slide after %d", e.After)
		}
		if e.Tool != "replace_presentation" && strings.TrimSpace(c.FrontMatter) != "" {
			md = strings.TrimSpace(c.FrontMatter) + "\n\n" + md
		}
		for _, w := range check(md, css) {
			out = append(out, where+": "+w)
		}
	}
	if css != c.CSS {
		deck := c.DeckMD
		if strings.TrimSpace(deck) == "" {
			deck = strings.TrimSpace(c.FrontMatter + "\n\n# Title\n\nText\n\n## Slide\n\n- a point\n")
		}
		before := check(deck, c.CSS)
		for _, w := range check(deck, css) {
			if !slices.Contains(before, w) {
				out = append(out, "theme css: "+w)
			}
		}
	}
	return out
}

// the theme the answer leaves the presentation in
func aiThemeAfter(theme string, edits []aiEdit) string {
	for _, e := range edits {
		if e.Tool == "set_theme" {
			theme = e.Value
		}
	}
	return theme
}

// Ask the model; when the check finds problems in its changes, tell it what
// they are and ask once more. The second answer stands when it changes
// something; the tokens of both are counted.
func (a *aiAssist) ask(ctx context.Context, q aiQuestion, check func(theme string) aiChecker, r *aiReader) (aiAnswer, error) {
	turns := q.turns()
	ans, err := a.askModel(ctx, turns, r)
	if err != nil || ans.Declined || check == nil {
		return ans, err
	}
	problems := aiProblems(check(aiThemeAfter(q.Context.Theme, ans.Edits)), q.Context, ans.Edits)
	if len(problems) == 0 {
		return ans, nil
	}
	said := ans.Text
	if said == "" {
		said = "I made the change."
	}
	again := append(append([]aiTurn{}, turns...),
		aiTurn{Role: "assistant", Text: said},
		aiTurn{Role: "user", Text: "Your change was not made: Sliqtly's check of it says\n- " + strings.Join(problems, "\n- ") +
			"\nMake the whole change again with these fixed, with your functions, and say in a sentence what you changed."})
	fixed, err := a.askModel(ctx, again, r)
	if err != nil || fixed.Declined || len(fixed.Edits) == 0 {
		// the first answer, as the model gave it, rather than nothing
		ans.In += fixed.In
		ans.Out += fixed.Out
		ans.Calls = max(ans.Calls, 1) + max(fixed.Calls, 1)
		return ans, nil
	}
	if len(fixed.Places.Steps) == 0 {
		fixed.Places = ans.Places
	}
	fixed.In += ans.In
	fixed.Out += ans.Out
	fixed.Calls = max(fixed.Calls, 1) + max(ans.Calls, 1)
	return fixed, nil
}

// One answer from the model, reading with r first when it can.
func (a *aiAssist) askModel(ctx context.Context, turns []aiTurn, r *aiReader) (aiAnswer, error) {
	if m, ok := a.model.(aiReading); ok && r != nil {
		return m.AskReading(ctx, a.system, turns, r)
	}
	return a.model.Ask(ctx, a.system, turns)
}
