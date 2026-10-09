// SPDX-License-Identifier: AGPL-3.0-or-later

package connectors

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Error is a refused or failed call, with a code a script can act on:
// not_found, bad_request, forbidden (no grant), not_connected (the person
// has not signed in to the service), not_configured (a secret is missing),
// quota, too_large, timeout, remote (the service failed or refused).
type Error struct {
	Code   string
	Msg    string
	Status int // the service's status, for remote
}

func (e *Error) Error() string { return e.Msg }

// Gateway is every call to a connector: the checks, the request, the
// secret or token, the limits, the answer cut down, the audit line.
type Gateway struct {
	reg    *Registry
	grants *Grants
	tokens *Tokens
	client *http.Client
	now    func() time.Time
	states *states
	dir    string

	mu     sync.Mutex
	counts map[string]*count
	logMu  sync.Mutex
}

type count struct {
	minute, day     int64
	inMinute, inDay int
}

// Open loads the connectors in dir and the grants and tokens kept there.
// client makes the outgoing calls (one that refuses private addresses);
// the gateway adds its own check that every request and redirect stays on
// the connector's hosts.
func Open(dir string, client *http.Client, now func() time.Time) (*Gateway, error) {
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	reg, err := Load(dir)
	if err != nil {
		return nil, err
	}
	grants, err := OpenGrants(dir, now)
	if err != nil {
		return nil, err
	}
	tokens, err := OpenTokens(dir)
	if err != nil {
		return nil, err
	}
	c := *client
	return &Gateway{reg: reg, grants: grants, tokens: tokens, client: &c, now: now, states: &states{m: map[string]pending{}}, dir: dir, counts: map[string]*count{}}, nil
}

// Registry is the connectors loaded.
func (g *Gateway) Registry() *Registry { return g.reg }

// Grants is the grants and requests.
func (g *Gateway) Grants() *Grants { return g.grants }

// Call is one call to a connector's operation.
type Call struct {
	// who calls (a person's id on this server)
	Who string
	// the deck the call is made for; "" only with Admin
	Deck      string
	Connector string
	Op        string
	Args      map[string]any
	// the admin trying an operation from the settings: needs no grant
	Admin bool
}

// Do makes the call and returns the answer cut down to the operation's
// picks.
func (g *Gateway) Do(ctx context.Context, call Call) (any, error) {
	start := g.now()
	out, err := g.do(ctx, call)
	e := Entry{Who: call.Who, Deck: call.Deck, Connector: call.Connector, Op: call.Op, Ms: g.now().Sub(start).Milliseconds(), Status: "ok"}
	if call.Admin {
		e.Deck = "(admin test)"
	}
	if b, err := json.Marshal(call.Args); err == nil {
		h := sha256.Sum256(b)
		e.Args = hex.EncodeToString(h[:8])
	}
	var ce *Error
	if errors.As(err, &ce) {
		e.Status, e.Remote = ce.Code, ce.Status
	} else if err != nil {
		e.Status = "error"
	}
	g.audit(e)
	return out, err
}

func (g *Gateway) do(ctx context.Context, call Call) (any, error) {
	c := g.reg.Get(call.Connector)
	if c == nil {
		return nil, &Error{Code: "not_found", Msg: "no connector " + call.Connector}
	}
	op := c.Operations[call.Op]
	if op == nil {
		return nil, &Error{Code: "not_found", Msg: call.Connector + " has no operation " + call.Op}
	}
	if !call.Admin {
		if call.Deck == "" {
			return nil, &Error{Code: "bad_request", Msg: "a call is made for a deck"}
		}
		if !g.grants.Allowed(call.Deck, c.ID, call.Op) {
			if err := g.grants.Ask(call.Deck, c.ID, call.Op, call.Who); err != nil {
				return nil, err
			}
			return nil, &Error{Code: "forbidden", Msg: "this deck may not use " + c.ID + "." + call.Op + " yet; the server's admin has been asked"}
		}
	}
	method, addr, body, err := op.request(c.BaseURL, call.Args)
	if err != nil {
		return nil, &Error{Code: "bad_request", Msg: err.Error()}
	}
	u, err := url.Parse(addr)
	if err != nil || u.Scheme != "https" || !c.mayReach(u.Hostname()) {
		return nil, &Error{Code: "bad_request", Msg: "the address is not one of the connector's"}
	}
	if err := g.take(c); err != nil {
		return nil, err
	}
	auth := ""
	switch c.Identity {
	case "user":
		tok, err := g.userToken(ctx, c, call.Who)
		if err != nil {
			return nil, err
		}
		auth = "Bearer " + tok.AccessToken
	case "service":
		if c.Auth != nil {
			v := c.Auth.Secret.Value()
			if v == "" {
				return nil, &Error{Code: "not_configured", Msg: "the server has no secret for " + c.ID + " (" + c.Auth.Secret.Env + " is not set)"}
			}
			if c.Auth.Kind == "bearer" {
				auth = "Bearer " + v
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.Limits.TimeoutMs)*time.Millisecond)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, addr, rd)
	if err != nil {
		return nil, err
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if c.Auth != nil && c.Auth.Kind == "header" {
		req.Header.Set(c.Auth.Header, c.Auth.Secret.Value())
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Sliqtly-connector")
	}
	client := *g.client
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		if r.URL.Scheme != "https" || !c.mayReach(r.URL.Hostname()) {
			return errors.New("redirect away from the connector's hosts")
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &Error{Code: "timeout", Msg: c.title() + " did not answer in time"}
		}
		return nil, &Error{Code: "remote", Msg: "could not reach " + c.title()}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, c.Limits.MaxResponseBytes+1))
	if err != nil {
		return nil, &Error{Code: "remote", Msg: "the answer from " + c.title() + " broke off"}
	}
	if int64(len(data)) > c.Limits.MaxResponseBytes {
		return nil, &Error{Code: "too_large", Msg: fmt.Sprintf("the answer is over %d bytes", c.Limits.MaxResponseBytes)}
	}
	if resp.StatusCode == 401 && c.Identity == "user" {
		return nil, &Error{Code: "not_connected", Msg: c.title() + " no longer accepts your sign-in; connect again", Status: 401}
	}
	if resp.StatusCode >= 300 {
		// the service's own words stay out: they may carry what the
		// operation's picks would have cut
		return nil, &Error{Code: "remote", Msg: fmt.Sprintf("%s answered %d", c.title(), resp.StatusCode), Status: resp.StatusCode}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, &Error{Code: "remote", Msg: c.title() + " did not answer with JSON", Status: resp.StatusCode}
	}
	return Pick(v, op.Pick), nil
}

// take counts a call against the connector's limits
func (g *Gateway) take(c *Connector) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	t := g.now()
	minute, day := t.Unix()/60, t.Unix()/86400
	n := g.counts[c.ID]
	if n == nil {
		n = &count{}
		g.counts[c.ID] = n
	}
	if n.minute != minute {
		n.minute, n.inMinute = minute, 0
	}
	if n.day != day {
		n.day, n.inDay = day, 0
	}
	if n.inMinute >= c.Limits.PerMinute || n.inDay >= c.Limits.PerDay {
		return &Error{Code: "quota", Msg: c.title() + " has been called too often; try again later"}
	}
	n.inMinute++
	n.inDay++
	return nil
}

// Connection is whether a person has connected their account.
func (g *Gateway) Connection(connector, who string) (connected bool, account string, err error) {
	tok, err := g.tokens.Get(connector, who)
	if err != nil || tok == nil {
		return false, "", err
	}
	return true, tok.Account, nil
}

// Disconnect forgets a person's sign-in to a service (the service may
// still list this server as allowed until the person removes it there).
func (g *Gateway) Disconnect(connector, who string) error {
	g.audit(Entry{Who: who, Connector: connector, Op: "(disconnect)", Status: "ok"})
	return g.tokens.Delete(connector, who)
}

// Entry is one line of the audit log: no arguments (only a hash of them),
// no answers, no secrets.
type Entry struct {
	At        string `json:"at"`
	Who       string `json:"who"`
	Deck      string `json:"deck,omitempty"`
	Connector string `json:"connector"`
	Op        string `json:"op"`
	Args      string `json:"args,omitempty"`
	Status    string `json:"status"`
	Remote    int    `json:"remote,omitempty"`
	Ms        int64  `json:"ms,omitempty"`
}

func (g *Gateway) audit(e Entry) {
	e.At = g.now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	g.logMu.Lock()
	defer g.logMu.Unlock()
	f, err := os.OpenFile(filepath.Join(g.dir, "audit.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

// Recent is the newest audit lines, newest first.
func (g *Gateway) Recent(n int) []Entry {
	g.logMu.Lock()
	b, err := os.ReadFile(filepath.Join(g.dir, "audit.log"))
	g.logMu.Unlock()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	out := []Entry{}
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		var e Entry
		if json.Unmarshal([]byte(lines[i]), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}
