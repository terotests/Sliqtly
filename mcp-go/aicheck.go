// SPDX-License-Identifier: AGPL-3.0-or-later

// The assistant's changes, read before the editor makes them. A model writes
// what it has seen elsewhere: Chart.js JSON in a ```chart block, or
// style="margin-top: -60px" to move a chart up. Sliqtly shows the first as
// code and ignores the second, and the person sees the assistant say it did
// something it did not. aiProblems names what is wrong in a model's own
// terms, and aiAssist.ask gives the model one round to write it again.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// What is wrong with the Markdown the edits write, one line per problem;
// none when Sliqtly reads all of it.
func aiProblems(edits []aiEdit) []string {
	var out []string
	for _, e := range edits {
		if e.Markdown == "" {
			continue
		}
		where := "the presentation"
		switch e.Tool {
		case "replace_slide":
			where = fmt.Sprintf("slide %d", e.Slide)
		case "insert_slide":
			where = fmt.Sprintf("the slide inserted after %d", e.After)
		}
		for _, p := range mdProblems(e.Markdown) {
			out = append(out, where+": "+p)
		}
	}
	return out
}

// the problems in one piece of Markdown
func mdProblems(md string) []string {
	var out []string
	styled := false
	fence, lang := "", ""
	var body []string
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(t, fence) && strings.Trim(t, fence[:1]) == "" {
				if p := fenceProblem(lang, strings.Join(body, "\n")); p != "" {
					out = append(out, p)
				}
				fence, body = "", nil
				continue
			}
			body = append(body, line)
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			ch := t[:1]
			n := len(t) - len(strings.TrimLeft(t, ch))
			fence = strings.Repeat(ch, n)
			lang = strings.ToLower(strings.TrimSpace(t[n:]))
			if i := strings.IndexAny(lang, " {"); i >= 0 {
				lang = lang[:i]
			}
			continue
		}
		if !styled && strings.Contains(strings.ReplaceAll(t, " ", ""), "style=") {
			styled = true
			out = append(out, "style= is ignored: Sliqtly reads no CSS, HTML or pixel positions in the Markdown; place things with the heading's attributes ({layout=image-right}, valign=…) or the order of the blocks")
		}
	}
	return out
}

// the problem with one fenced block, "" when there is none
func fenceProblem(lang, body string) string {
	switch lang {
	case "chart", "chartjs", "chart.js":
		return "```" + lang + " is not a chart in Sliqtly and shows as code; a chart is a ```vega-lite block with a Vega-Lite v5 spec"
	case "vega-lite", "vegalite", "vega":
	default:
		return ""
	}
	var spec map[string]any
	if err := json.Unmarshal([]byte(body), &spec); err != nil {
		return "the ```" + lang + " block is not one JSON object (" + err.Error() + "); it shows as code"
	}
	if _, ok := spec["datasets"]; ok {
		return "the ```" + lang + " block is Chart.js (\"datasets\"), not Vega-Lite; write \"data\": {\"values\": [...]}, \"mark\" and \"encoding\""
	}
	if dat, ok := spec["data"].(map[string]any); ok {
		if _, ok := dat["datasets"]; ok {
			return "the ```" + lang + " block is Chart.js (\"datasets\"), not Vega-Lite; write \"data\": {\"values\": [...]}, \"mark\" and \"encoding\""
		}
	}
	if lang == "vega" {
		return ""
	}
	for _, k := range []string{"mark", "layer", "concat", "hconcat", "vconcat", "repeat", "facet", "spec"} {
		if _, ok := spec[k]; ok {
			return ""
		}
	}
	return "the ```" + lang + " block has no \"mark\" (or \"layer\"), so it draws nothing"
}

// Ask the model; when its changes have problems Sliqtly would not read, tell
// it what they are and ask once more. The second answer stands either way;
// the tokens of both are counted.
func (a *aiAssist) ask(ctx context.Context, turns []aiTurn) (aiAnswer, error) {
	ans, err := a.model.Ask(ctx, a.system, turns)
	if err != nil || ans.Declined {
		return ans, err
	}
	problems := aiProblems(ans.Edits)
	if len(problems) == 0 {
		return ans, nil
	}
	said := ans.Text
	if said == "" {
		said = "I made the change."
	}
	again := append(append([]aiTurn{}, turns...),
		aiTurn{Role: "assistant", Text: said},
		aiTurn{Role: "user", Text: "Your change was not made, because Sliqtly would not read it:\n- " + strings.Join(problems, "\n- ") +
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
