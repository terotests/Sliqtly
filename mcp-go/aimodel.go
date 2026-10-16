//go:build !nocloud

// SPDX-License-Identifier: AGPL-3.0-or-later

// The AI assistant's model (aiassist.go), as SLIQTLY_AI says: Gemini
// (aigemini.go), or Claude through the Anthropic Go SDK on Vertex AI or the
// Claude API.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/vertex"
	"golang.org/x/oauth2/google"
)

type claudeModel struct {
	client anthropic.Client
	model  string
}

func (m *claudeModel) modelName() string { return m.model }

// the same client with another model (WARDEN_AI_MODEL)
func (m *claudeModel) withModel(name string) aiModel {
	return &claudeModel{client: m.client, model: name}
}

// The model the environment names; nil when it names none (no assistant).
//
//	gemini     Gemini (gemini-3.5-flash-lite unless SLIQTLY_AI_MODEL): with
//	           GEMINI_API_KEY through the Gemini API (AI Studio), else on
//	           Vertex AI signed by the service's own account
//	vertex     Claude on Vertex AI (claude-opus-5-5 unless SLIQTLY_AI_MODEL)
//	anthropic  Claude through the Claude API, with ANTHROPIC_API_KEY
func newAIModel(ctx context.Context) (aiModel, string) {
	region := env("SLIQTLY_AI_REGION", "global")
	project := env("SLIQTLY_AI_PROJECT", cloudProject())
	switch strings.ToLower(os.Getenv("SLIQTLY_AI")) {
	case "gemini":
		model := env("SLIQTLY_AI_MODEL", "gemini-3.5-flash-lite")
		if key := os.Getenv("GEMINI_API_KEY"); key != "" {
			return &geminiModel{http: &http.Client{Timeout: 3 * time.Minute}, model: model, key: key, tools: aiEditTools}, "Gemini API (" + model + ")"
		}
		c, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/cloud-platform")
		if err != nil {
			log.Printf("ai: no Google credentials for Gemini on Vertex AI: %v; no assistant", err)
			return nil, ""
		}
		c.Timeout = 3 * time.Minute
		return &geminiModel{http: c, model: model, project: project, region: region, tools: aiEditTools}, "Gemini on Vertex AI (" + project + ", " + region + ", " + model + ")"
	case "vertex":
		model := env("SLIQTLY_AI_MODEL", "claude-opus-5-5")
		c := anthropic.NewClient(vertex.WithGoogleAuth(ctx, region, project, "https://www.googleapis.com/auth/cloud-platform"))
		return &claudeModel{client: c, model: model}, "Claude on Vertex AI (" + project + ", " + region + ", " + model + ")"
	case "anthropic":
		model := env("SLIQTLY_AI_MODEL", "claude-opus-5-5")
		if os.Getenv("ANTHROPIC_API_KEY") == "" {
			log.Print("ai: SLIQTLY_AI=anthropic needs ANTHROPIC_API_KEY; no assistant")
			return nil, ""
		}
		return &claudeModel{client: anthropic.NewClient(option.WithMaxRetries(1)), model: model}, "Claude API (" + model + ")"
	}
	return nil, ""
}

func claudeMessages(turns []aiTurn) []anthropic.MessageParam {
	msgs := make([]anthropic.MessageParam, 0, len(turns))
	for _, t := range turns {
		if t.Role == "assistant" {
			msgs = append(msgs, anthropic.NewAssistantMessage(anthropic.NewTextBlock(t.Text)))
		} else {
			blocks := []anthropic.ContentBlockParamUnion{}
			for _, im := range t.Images {
				blocks = append(blocks, anthropic.NewImageBlockBase64(im.MimeType, im.Data))
			}
			msgs = append(msgs, anthropic.NewUserMessage(append(blocks, anthropic.NewTextBlock(t.Text))...))
		}
	}
	return msgs
}

func claudeErr(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403 || apiErr.StatusCode == 404) {
		return fmt.Errorf("%w: %v", errAINotSetUp, err)
	}
	return err
}

// One question with r's tools: each call the model makes is run and its
// answer handed back, until it answers without calling or the rounds are
// spent. (Warden's assistant: every tool is run at once, changes too.)
func (m *claudeModel) AskReading(ctx context.Context, system string, turns []aiTurn, r *aiReader) (aiAnswer, error) {
	if r == nil || len(r.Tools) == 0 {
		return m.Ask(ctx, system, turns)
	}
	tools := make([]anthropic.ToolUnionParam, 0, len(r.Tools))
	for _, t := range r.Tools {
		req, _ := t.Params["required"].([]string)
		tools = append(tools, anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
			Name: t.Name, Description: anthropic.String(t.Description),
			InputSchema: anthropic.ToolInputSchemaParam{Properties: t.Params["properties"], Required: req},
		}})
	}
	msgs := claudeMessages(turns)
	var a aiAnswer
	for round := 0; round < r.rounds(); round++ {
		resp, err := m.client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.Model(m.model),
			MaxTokens: 16000,
			System:    []anthropic.TextBlockParam{{Text: system, CacheControl: anthropic.NewCacheControlEphemeralParam()}},
			Messages:  msgs,
			Tools:     tools,
		})
		a.Calls++
		if err != nil {
			return a, claudeErr(err)
		}
		a.In += resp.Usage.InputTokens + resp.Usage.CacheReadInputTokens + resp.Usage.CacheCreationInputTokens
		a.Out += resp.Usage.OutputTokens
		if resp.StopReason == anthropic.StopReasonRefusal {
			a.Declined = true
			return a, nil
		}
		var b strings.Builder
		results := []anthropic.ContentBlockParamUnion{}
		for _, block := range resp.Content {
			switch v := block.AsAny().(type) {
			case anthropic.TextBlock:
				b.WriteString(v.Text)
			case anthropic.ToolUseBlock:
				args := map[string]any{}
				json.Unmarshal(v.Input, &args)
				got := r.Run(ctx, v.Name, args)
				results = append(results, anthropic.NewToolResultBlock(v.ID, got.Text, got.Failed))
			}
		}
		if t := strings.TrimSpace(b.String()); t != "" {
			a.Text = t
		}
		if len(results) == 0 {
			return a, nil
		}
		msgs = append(msgs, resp.ToParam(), anthropic.NewUserMessage(results...))
	}
	return a, nil
}

func (m *claudeModel) Ask(ctx context.Context, system string, turns []aiTurn) (aiAnswer, error) {
	msgs := claudeMessages(turns)
	resp, err := m.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(m.model),
		MaxTokens: 16000,
		// the guide is the same on every call: cached, it costs a tenth
		System:       []anthropic.TextBlockParam{{Text: system, CacheControl: anthropic.NewCacheControlEphemeralParam()}},
		Messages:     msgs,
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortLow},
	})
	if err != nil {
		return aiAnswer{}, claudeErr(err)
	}
	a := aiAnswer{In: resp.Usage.InputTokens + resp.Usage.CacheReadInputTokens + resp.Usage.CacheCreationInputTokens, Out: resp.Usage.OutputTokens}
	if resp.StopReason == anthropic.StopReasonRefusal {
		a.Declined = true
		return a, nil
	}
	var b strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	a.Text = strings.TrimSpace(b.String())
	return a, nil
}
