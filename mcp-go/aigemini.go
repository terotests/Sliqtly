// SPDX-License-Identifier: AGPL-3.0-or-later

// Gemini for the AI assistant (aiassist.go): one generateContent call, by
// the Gemini API with an API key or on Vertex AI with the service's account.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

type geminiPart struct {
	Text         string          `json:"text,omitempty"`
	FunctionCall *geminiCall     `json:"functionCall,omitempty"`
	Thought      bool            `json:"thought,omitempty"`
	Signature    json.RawMessage `json:"thoughtSignature,omitempty"`
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
	contents := make([]geminiContent, 0, len(turns))
	for _, t := range turns {
		role := "user"
		if t.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, geminiContent{Role: role, Parts: []geminiPart{{Text: t.Text}}})
	}
	ask := map[string]any{
		"systemInstruction": geminiContent{Parts: []geminiPart{{Text: system}}},
		"contents":          contents,
		"generationConfig":  map[string]any{"maxOutputTokens": 8192},
	}
	if len(m.tools) > 0 {
		decls := make([]map[string]any, 0, len(m.tools))
		for _, t := range m.tools {
			decls = append(decls, map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Params})
		}
		ask["tools"] = []map[string]any{{"functionDeclarations": decls}}
	}
	body, _ := json.Marshal(ask)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint(), bytes.NewReader(body))
	if err != nil {
		return aiAnswer{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.key != "" {
		req.Header.Set("x-goog-api-key", m.key)
	}
	res, err := m.http.Do(req)
	if err != nil {
		return aiAnswer{}, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode != http.StatusOK {
		err := fmt.Errorf("gemini %s: %d %s", m.model, res.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), 500)])))
		if res.StatusCode == 401 || res.StatusCode == 403 || res.StatusCode == 404 {
			return aiAnswer{}, fmt.Errorf("%w: %v", errAINotSetUp, err)
		}
		return aiAnswer{}, err
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
		return aiAnswer{}, fmt.Errorf("gemini %s: %v", m.model, err)
	}
	a := aiAnswer{In: out.Usage.Prompt, Out: out.Usage.Answer + out.Usage.Thoughts}
	if out.PromptFeedback.BlockReason != "" || len(out.Candidates) == 0 {
		a.Declined = true
		return a, nil
	}
	c := out.Candidates[0]
	switch c.FinishReason {
	case "SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII", "RECITATION":
		a.Declined = true
		return a, nil
	}
	var b strings.Builder
	for _, p := range c.Content.Parts {
		if p.FunctionCall != nil {
			if e, ok := aiEditOf(p.FunctionCall.Name, p.FunctionCall.Args); ok {
				a.Edits = append(a.Edits, e)
			}
			continue
		}
		if !p.Thought {
			b.WriteString(p.Text)
		}
	}
	a.Text = strings.TrimSpace(b.String())
	return a, nil
}
