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

// what the check says about one piece of a presentation's Markdown
type aiChecker func(md string) []string

// The deck check MCP runs, on Markdown laid out with the theme (aurora, as
// MCP's, when the editor named none Sliqtly has).
func aiDeckCheck(env *Env, ctx context.Context, theme string) aiChecker {
	if !slices.Contains(Deck_static_THEMES(), theme) {
		theme = "aurora"
	}
	return func(md string) []string {
		h := &McpHost{env: env, ctx: ctx, images: map[int64][]byte{}}
		bgs := Tools_static_backgrounds(h, md, "", nil, "")
		return Tools_static_checkDeck(h, md, theme, false, "", bgs, nil, "", nil).warnings
	}
}

// What the check says about the Markdown the edits write, each line saying
// which edit; none when Sliqtly draws all of it. A slide is checked under
// the presentation's front matter, as it will be shown.
func aiProblems(check aiChecker, frontMatter string, edits []aiEdit) []string {
	var out []string
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
		if e.Tool != "replace_presentation" && strings.TrimSpace(frontMatter) != "" {
			md = strings.TrimSpace(frontMatter) + "\n\n" + md
		}
		for _, w := range check(md) {
			out = append(out, where+": "+w)
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
func (a *aiAssist) ask(ctx context.Context, q aiQuestion, check func(theme string) aiChecker) (aiAnswer, error) {
	turns := q.turns()
	ans, err := a.model.Ask(ctx, a.system, turns)
	if err != nil || ans.Declined || check == nil {
		return ans, err
	}
	problems := aiProblems(check(aiThemeAfter(q.Context.Theme, ans.Edits)), q.Context.FrontMatter, ans.Edits)
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
	fixed, err := a.model.Ask(ctx, a.system, again)
	if err != nil || fixed.Declined || len(fixed.Edits) == 0 {
		// the first answer, as the model gave it, rather than nothing
		return ans, nil
	}
	fixed.In += ans.In
	fixed.Out += ans.Out
	return fixed, nil
}
