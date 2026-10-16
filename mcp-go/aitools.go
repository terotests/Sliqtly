// SPDX-License-Identifier: AGPL-3.0-or-later

// What the editor's AI assistant may look at before it answers: the read
// tools MCP gives an assistant (sliqtly_guide, get_display_list, list_files,
// read_file, render_slide, search_figma, get_figma_screen, the PowerPoint
// reads…), run by the same code (Tools.call) as the signed-in person, and
// two of its own that draw the Markdown open in the editor, unsaved changes
// included (Tools.renderOpen). The model calls them, reads what they say
// and see the pictures they draw, and calls again until it answers
// (aigemini.go AskReading); its changes still go through aiEditTools to the
// editor, as one edit the person can undo.

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
)

// what one read gave: its words, and the pictures it drew (JPEG or PNG)
type aiRead struct {
	Text   string
	Images [][]byte
	Failed bool
}

// the read tools of one question, and how to run them
type aiReader struct {
	Tools []aiTool
	Run   func(ctx context.Context, name string, args map[string]any) aiRead
	// where the person's presentations are kept can be changed
	// (aiPlaceTools, aiorganize.go): sliqtly.com
	Places bool
}

// a model that can read before it answers
type aiReading interface {
	AskReading(ctx context.Context, system string, turns []aiTurn, r *aiReader) (aiAnswer, error)
}

// most text a read hands the model, and most pictures
const (
	aiReadText   = 40000
	aiReadImages = 2
)

// MCP tools the drawer leaves out though they only read (list_presentations
// is in: it tells where each presentation is kept, for sorting them)
var aiReadSkip = []string{"list_comments"}

// the drawer's own: the Markdown in the editor, drawn
var aiLookTools = []aiTool{
	{"look_at_slide", "Draw one slide of the presentation as it is in the editor now, unsaved changes included, and say what its layout reports (boxes, overflow, warnings). Use it to check how a slide looks before and after changing it.",
		aiObj(map[string]any{"slide": aiInt("the slide's number in <slides>")}, "slide")},
	{"look_at_presentation", "Draw every slide of the presentation as it is in the editor now, small, numbered, with each slide's layout warnings.",
		aiObj(map[string]any{})},
	{"read_theme_css", "Read the presentation's stylesheet as it is in the editor's CSS tab: the theme's rules and the presentation's own. Read it before add_theme_css or replace_theme_css.",
		aiObj(map[string]any{})},
}

// The reader for a question asked by uid: MCP's read tools (those the
// server has) and the drawer's own.
func aiDeckReader(env *Env, uid string, q aiQuestion) *aiReader {
	h := &McpHost{env: env, ctx: context.Background(), images: map[int64][]byte{}}
	tools := append([]aiTool{}, aiLookTools...)
	var list []map[string]any
	_ = json.Unmarshal([]byte(JOut_static_text(Tools_static_list(h))), &list)
	for _, t := range list {
		name, _ := t["name"].(string)
		ann, _ := t["annotations"].(map[string]any)
		if ro, _ := ann["readOnlyHint"].(bool); !ro || name == "" || slices.Contains(aiReadSkip, name) {
			continue
		}
		desc, _ := t["description"].(string)
		schema, _ := t["inputSchema"].(map[string]any)
		tools = append(tools, aiTool{name, desc, aiSchema(schema)})
	}
	deckID := strings.TrimSpace(q.Context.DeckID)
	if deckID != "" {
		for i := range tools {
			if p, ok := tools[i].Params["properties"].(map[string]any); ok && p["deck_id"] != nil {
				tools[i].Description += " The open presentation's deck_id is " + deckID + "."
			}
		}
	}
	run := func(ctx context.Context, name string, args map[string]any) aiRead {
		h := &McpHost{env: env, ctx: ctx, images: map[int64][]byte{}}
		c := CreateNew_Caller()
		c.signedIn, c.uid, c.who = true, uid, "uid:"+uid
		raw, _ := json.Marshal(args)
		a := J_static_parse(string(raw))
		var r *MfJ
		switch name {
		case "read_theme_css":
			if strings.TrimSpace(q.Context.CSS) == "" {
				return aiRead{Text: "The CSS tab is empty: the presentation uses its theme (" + aiKnownTheme(q.Context.Theme) + ") as it is."}
			}
			return aiRead{Text: aiClip(q.Context.CSS, aiReadText)}
		case "look_at_slide", "look_at_presentation":
			r = Tools_static_renderOpen(h, c, a, name == "look_at_presentation", q.Context.DeckMD, aiKnownTheme(q.Context.Theme), q.Context.CSS, deckID)
		default:
			if !slices.ContainsFunc(tools, func(t aiTool) bool { return t.Name == name }) {
				return aiRead{Text: "There is no tool " + name + ".", Failed: true}
			}
			r = Tools_static_call(h, c, name, a)
		}
		if r == nil {
			return aiRead{Text: "There is no tool " + name + ".", Failed: true}
		}
		return aiReadOf(JOut_static_text(r))
	}
	return &aiReader{Tools: tools, Run: run, Places: aiPlacesOn(env)}
}

// An MCP answer ({"content": [{type: text|image, …}], "isError"}) as a read.
func aiReadOf(answer string) aiRead {
	var out struct {
		Content []struct {
			Type, Text, Data string
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal([]byte(answer), &out); err != nil {
		return aiRead{Text: "The tool's answer could not be read.", Failed: true}
	}
	r := aiRead{Failed: out.IsError}
	var b strings.Builder
	for _, c := range out.Content {
		switch c.Type {
		case "text":
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(c.Text)
		case "image":
			if len(r.Images) < aiReadImages {
				if png, err := base64.StdEncoding.DecodeString(c.Data); err == nil {
					r.Images = append(r.Images, png)
				}
			}
		}
	}
	r.Text = aiClip(b.String(), aiReadText)
	return r
}

// An MCP tool's input schema in the subset Gemini's function declarations
// take: types, descriptions, properties, items, enums, required and bounds.
func aiSchema(s map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range s {
		switch k {
		case "type":
			// ["string", "null"]: the first that is not null
			if list, ok := v.([]any); ok {
				for _, t := range list {
					if t != "null" {
						v = t
						break
					}
				}
			}
			out[k] = v
		case "description", "enum", "required", "minimum", "maximum", "minItems", "maxItems", "format", "nullable":
			out[k] = v
		case "properties":
			if m, ok := v.(map[string]any); ok {
				props := map[string]any{}
				for name, p := range m {
					if pm, ok := p.(map[string]any); ok {
						props[name] = aiSchema(pm)
					}
				}
				out[k] = props
			}
		case "items":
			if m, ok := v.(map[string]any); ok {
				out[k] = aiSchema(m)
			}
		}
	}
	if out["type"] == nil {
		out["type"] = "object"
	}
	if out["type"] == "object" && out["properties"] == nil {
		out["properties"] = map[string]any{}
	}
	return out
}
