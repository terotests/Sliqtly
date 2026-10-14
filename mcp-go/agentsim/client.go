// SPDX-License-Identifier: AGPL-3.0-or-later

package agentsim

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// Client is one agent's MCP connection: Streamable HTTP, JSON-RPC, its own
// session id. Many clients share one http.Client (its connection pool).
type Client struct {
	URL   string // the server's /mcp
	Token string // Authorization: Bearer, when the server wants one
	HTTP  *http.Client
	sid   string
	n     atomic.Int64
}

// Result is a tools/call answer: the text the model reads, the structured
// content, and whether the tool said it failed.
type Result struct {
	Text    string
	Data    map[string]any
	IsError bool
}

// ErrHTTP is a transport level failure: a status other than 200 or a body
// that is not JSON-RPC. Tool failures are Result.IsError, not errors.
type ErrHTTP struct {
	Status int
	Body   string
}

func (e ErrHTTP) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body) }

const protocol = "2025-06-18"

func (c *Client) rpc(ctx context.Context, method string, params any, notify bool) (json.RawMessage, error) {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	if !notify {
		msg["id"] = c.n.Add(1)
	}
	body, _ := json.Marshal(msg)
	req, err := http.NewRequestWithContext(ctx, "POST", c.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", protocol)
	if c.sid != "" {
		req.Header.Set("Mcp-Session-Id", c.sid)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if s := res.Header.Get("Mcp-Session-Id"); s != "" {
		c.sid = s
	}
	if notify && res.StatusCode/100 == 2 {
		return nil, nil
	}
	if res.StatusCode != 200 {
		return nil, ErrHTTP{res.StatusCode, clip(string(raw), 300)}
	}
	text := string(raw)
	if strings.HasPrefix(text, "event:") || strings.HasPrefix(text, "data:") || strings.HasPrefix(text, "id:") {
		text = sseData(text)
	}
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, ErrHTTP{res.StatusCode, "not JSON-RPC: " + clip(text, 200)}
	}
	if out.Error != nil {
		return nil, fmt.Errorf("rpc %d: %s", out.Error.Code, out.Error.Message)
	}
	return out.Result, nil
}

// the data lines of a Server-Sent Events answer, joined
func sseData(text string) string {
	var b strings.Builder
	for _, l := range strings.Split(text, "\n") {
		if d, ok := strings.CutPrefix(l, "data:"); ok {
			b.WriteString(strings.TrimPrefix(d, " "))
		}
	}
	return b.String()
}

// Init starts the MCP session.
func (c *Client) Init(ctx context.Context, name string) error {
	_, err := c.rpc(ctx, "initialize", map[string]any{
		"protocolVersion": protocol,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": name, "version": "1"},
	}, false)
	if err != nil {
		return err
	}
	_, err = c.rpc(ctx, "notifications/initialized", nil, true)
	return err
}

// Call calls one tool.
func (c *Client) Call(ctx context.Context, name string, args map[string]any) (Result, error) {
	raw, err := c.rpc(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, false)
	if err != nil {
		return Result{}, err
	}
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Structured map[string]any `json:"structuredContent"`
		IsError    bool           `json:"isError"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return Result{}, errors.New("tools/call: " + err.Error())
	}
	var text []string
	for _, c := range r.Content {
		if c.Type == "text" {
			text = append(text, c.Text)
		}
	}
	return Result{Text: strings.Join(text, "\n"), Data: r.Structured, IsError: r.IsError}, nil
}

// Close ends the MCP session.
func (c *Client) Close() {
	if c.sid == "" {
		return
	}
	req, err := http.NewRequest("DELETE", c.URL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Mcp-Session-Id", c.sid)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if res, err := c.HTTP.Do(req); err == nil {
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
