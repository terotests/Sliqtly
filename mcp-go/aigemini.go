// SPDX-License-Identifier: AGPL-3.0-or-later

// Gemini for the AI assistant (aiassist.go): generateContent calls, by
// the Gemini API with an API key or on Vertex AI with the service's account.

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

type geminiModel struct {
	http  *http.Client
	model string
	// the Gemini API's key; "" for Vertex AI in project/region
	key             string
	project, region string
	// where the calls go ("" the real endpoints; the tests' server)
	base string
	// the functions it may call (aiEditTools)
	tools []aiTool
}

func (m *geminiModel) usesTools() bool { return len(m.tools) > 0 }

func (m *geminiModel) modelName() string { return m.model }

type geminiPart struct {
	Text             string          `json:"text,omitempty"`
	FunctionCall     *geminiCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiResponse `json:"functionResponse,omitempty"`
	InlineData       *geminiData     `json:"inlineData,omitempty"`
	Thought          bool            `json:"thought,omitempty"`
	Signature        json.RawMessage `json:"thoughtSignature,omitempty"`
}

type geminiCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

func (m *geminiModel) endpoint() string {
	if m.key != "" {
		base := m.base
		if base == "" {
			base = "https://generativelanguage.googleapis.com"
		}
		return base + "/v1beta/models/" + url.PathEscape(m.model) + ":generateContent"
	}
	base := m.base
	if base == "" {
		host := "aiplatform.googleapis.com"
		if m.region != "global" {
			host = m.region + "-" + host
		}
		base = "https://" + host
	}
	return fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models/%s:generateContent", base, url.PathEscape(m.project), url.PathEscape(m.region), url.PathEscape(m.model))
}

func (m *geminiModel) Ask(ctx context.Context, system string, turns []aiTurn) (aiAnswer, error) {
	return m.AskReading(ctx, system, turns, nil)
}

// the most calls one question makes when the model reads (aiReader)
const geminiRounds = 8

// One question: the model may call r's read tools, and hears their answers
// (and sees their pictures) in the next call, until it answers without
// reading or the rounds are spent. Its edit calls are collected from every
// round and answered "made when you finish".
func (m *geminiModel) AskReading(ctx context.Context, system string, turns []aiTurn, r *aiReader) (aiAnswer, error) {
	contents := make([]geminiContent, 0, len(turns)+2*geminiRounds)
	for _, t := range turns {
		role := "user"
		if t.Role == "assistant" {
			role = "model"
		}
		parts := []geminiPart{{Text: t.Text}}
		for _, im := range t.Images {
			parts = append(parts, geminiPart{InlineData: &geminiData{MimeType: im.MimeType, Data: im.Data}})
		}
		contents = append(contents, geminiContent{Role: role, Parts: parts})
	}
	decls := make([]map[string]any, 0, len(m.tools))
	reads := map[string]bool{}
	for _, t := range m.tools {
		decls = append(decls, map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Params})
	}
	if r != nil {
		for _, t := range r.Tools {
			decls = append(decls, map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Params})
			reads[t.Name] = true
		}
	}
	var a aiAnswer
	pictures := true
	mending := false
	for round := 0; ; round++ {
		res, err := m.generate(ctx, system, contents, decls)
		a.Calls++
		var bad *geminiError
		if errors.As(err, &bad) && bad.status == http.StatusBadRequest && pictures && geminiHasPictures(contents) {
			// a model that takes no pictures in a function's answer: the
			// words of the answers alone, from here on
			pictures = false
			geminiDropPictures(contents)
			res, err = m.generate(ctx, system, contents, decls)
			a.Calls++
		}
		if err != nil {
			return a, err
		}
		a.In += res.in
		a.Out += res.out
		if res.declined {
			a.Declined = true
			return a, nil
		}
		var b strings.Builder
		var answers []geminiPart
		edited, refused := false, false
		before := len(a.Edits)
		for _, p := range res.content.Parts {
			if c := p.FunctionCall; c != nil {
				var reply map[string]any
				switch {
				case reads[c.Name] && round < geminiRounds-2:
					got := r.Run(ctx, c.Name, c.Args)
					reply = map[string]any{"content": got.Text}
					if got.Failed {
						reply["error"] = true
					}
					fr := &geminiResponse{Name: c.Name, Response: reply}
					for _, png := range got.Images {
						if !pictures {
							break
						}
						// the slides come as JPEG; say what the bytes are
						fr.Parts = append(fr.Parts, geminiInline{InlineData: &geminiData{MimeType: http.DetectContentType(png), Data: base64.StdEncoding.EncodeToString(png)}})
					}
					answers = append(answers, geminiPart{FunctionResponse: fr})
					continue
				case reads[c.Name]:
					reply = map[string]any{"content": "No more looking for this question: make your changes now, or answer."}
				default:
					if e, ok := aiEditOf(c.Name, c.Args); ok {
						// a round that changes the presentation again, after
						// looking, makes the change over: its edits stand
						// (after a refused call the round mends that call: the
						// edits taken with it stand)
						if !edited && !mending {
							a.Edits = nil
						}
						edited = true
						// (a mended change made again replaces the earlier one)
						if mending {
							kept := slices.DeleteFunc(a.Edits[:before], e.sameAs)
							a.Edits = append(kept, a.Edits[before:]...)
							before = len(kept)
						}
						a.Edits = append(a.Edits, e)
						reply = map[string]any{"content": "Made in the editor when you finish."}
					} else {
						// said why, and heard again: the model can mend it
						reply = map[string]any{"content": aiRefusal(c.Name), "error": true}
						refused = refused || aiIsEdit(c.Name)
					}
				}
				answers = append(answers, geminiPart{FunctionResponse: &geminiResponse{Name: c.Name, Response: reply}})
				continue
			}
			if !p.Thought {
				b.WriteString(p.Text)
			}
		}
		if t := strings.TrimSpace(b.String()); t != "" {
			a.Text = t
		}
		read := false
		for _, p := range answers {
			read = read || reads[p.FunctionResponse.Name]
		}
		if (!read && !refused) || round >= geminiRounds-1 {
			return a, nil
		}
		mending = refused
		contents = append(contents, res.content, geminiContent{Role: "user", Parts: answers})
	}
}

// pictures in the functions' answers so far
func geminiHasPictures(contents []geminiContent) bool {
	for _, c := range contents {
		for _, p := range c.Parts {
			if p.FunctionResponse != nil && len(p.FunctionResponse.Parts) > 0 {
				return true
			}
		}
	}
	return false
}

func geminiDropPictures(contents []geminiContent) {
	for _, c := range contents {
		for _, p := range c.Parts {
			if p.FunctionResponse != nil && len(p.FunctionResponse.Parts) > 0 {
				p.FunctionResponse.Parts = nil
				p.FunctionResponse.Response["content"] = fmt.Sprint(p.FunctionResponse.Response["content"]) + "\n(The picture it drew cannot be shown to you.)"
			}
		}
	}
}

// a call Gemini answered with an error status
type geminiError struct {
	status int
	msg    string
}

func (e *geminiError) Error() string { return e.msg }

type geminiResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
	// pictures the function drew, for the model to see
	Parts []geminiInline `json:"parts,omitempty"`
}

type geminiInline struct {
	InlineData *geminiData `json:"inlineData,omitempty"`
}

type geminiData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// what one generateContent call gave
type geminiResult struct {
	content  geminiContent
	in, out  int64
	declined bool
}

func (m *geminiModel) generate(ctx context.Context, system string, contents []geminiContent, decls []map[string]any) (geminiResult, error) {
	ask := map[string]any{
		"systemInstruction": geminiContent{Parts: []geminiPart{{Text: system}}},
		"contents":          contents,
		"generationConfig":  map[string]any{"maxOutputTokens": 8192},
	}
	if len(decls) > 0 {
		ask["tools"] = []map[string]any{{"functionDeclarations": decls}}
	}
	body, _ := json.Marshal(ask)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint(), bytes.NewReader(body))
	if err != nil {
		return geminiResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.key != "" {
		req.Header.Set("x-goog-api-key", m.key)
	}
	res, err := m.http.Do(req)
	if err != nil {
		return geminiResult{}, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode != http.StatusOK {
		err := &geminiError{res.StatusCode, fmt.Sprintf("gemini %s: %d %s", m.model, res.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), 500)])))}
		if res.StatusCode == 401 || res.StatusCode == 403 || res.StatusCode == 404 {
			return geminiResult{}, fmt.Errorf("%w: %v", errAINotSetUp, err)
		}
		return geminiResult{}, err
	}
	var out struct {
		Candidates []struct {
			Content      geminiContent `json:"content"`
			FinishReason string        `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
		Usage struct {
			Prompt   int64 `json:"promptTokenCount"`
			Answer   int64 `json:"candidatesTokenCount"`
			Thoughts int64 `json:"thoughtsTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return geminiResult{}, fmt.Errorf("gemini %s: %v", m.model, err)
	}
	g := geminiResult{in: out.Usage.Prompt, out: out.Usage.Answer + out.Usage.Thoughts}
	if out.PromptFeedback.BlockReason != "" || len(out.Candidates) == 0 {
		g.declined = true
		return g, nil
	}
	c := out.Candidates[0]
	switch c.FinishReason {
	case "SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII", "RECITATION":
		g.declined = true
		return g, nil
	}
	g.content = c.Content
	if g.content.Role == "" {
		g.content.Role = "model"
	}
	return g, nil
}
