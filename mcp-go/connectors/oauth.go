// SPDX-License-Identifier: AGPL-3.0-or-later

package connectors

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// A person connects their own account at a service (identity "user"):
//
//	Start        a state (random, one use, 15 min) bound to the person and
//	             the connector, and a PKCE verifier; the browser goes to
//	             the service's authorize address
//	Finish       the service sends the browser back to the callback with
//	             code and state; the state is taken, the code exchanged
//	             (with the client secret and the verifier), and the token
//	             kept encrypted (Tokens) for that person
//
// The callback needs no sign-in of its own: the state says who started it,
// and only that browser has it.
type pending struct {
	connector, who, verifier, redirect string
	exp                                time.Time
}

type states struct {
	mu sync.Mutex
	m  map[string]pending
}

func (s *states) put(p pending) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.m {
		if now.After(v.exp) {
			delete(s.m, k)
		}
	}
	if len(s.m) >= 1000 {
		return "", errors.New("too many sign-ins under way")
	}
	s.m[id] = p
	return id, nil
}

func (s *states) take(id string) (pending, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.m[id]
	delete(s.m, id)
	if !ok || time.Now().After(p.exp) {
		return pending{}, false
	}
	return p, true
}

func verifier() (v, challenge string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	v = base64.RawURLEncoding.EncodeToString(b)
	return v, challengeFor(v), nil
}

// challengeFor is the S256 code challenge of a PKCE verifier
func challengeFor(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// StartOAuth is the address to send the person's browser to, for
// connecting their account at the connector's service. redirectURI is the
// server's callback as registered at the service.
func (g *Gateway) StartOAuth(connector, who, redirectURI string) (string, error) {
	c := g.reg.Get(connector)
	if c == nil {
		return "", &Error{Code: "not_found", Msg: "no such connector"}
	}
	if c.OAuth == nil {
		return "", &Error{Code: "bad_request", Msg: "this connector has no sign-in of its own"}
	}
	if c.OAuth.ClientSecret.Value() == "" {
		return "", &Error{Code: "not_configured", Msg: "the server has no client secret for " + c.ID + " (" + c.OAuth.ClientSecret.Env + " is not set)"}
	}
	v, ch, err := verifier()
	if err != nil {
		return "", err
	}
	state, err := g.states.put(pending{connector: connector, who: who, verifier: v, redirect: redirectURI, exp: time.Now().Add(15 * time.Minute)})
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", c.OAuth.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", ch)
	q.Set("code_challenge_method", "S256")
	if len(c.OAuth.Scopes) > 0 {
		q.Set("scope", strings.Join(c.OAuth.Scopes, " "))
	}
	sep := "?"
	if strings.Contains(c.OAuth.AuthorizeURL, "?") {
		sep = "&"
	}
	return c.OAuth.AuthorizeURL + sep + q.Encode(), nil
}

// FinishOAuth takes the callback's state and code, and keeps the token.
// It returns the connector and the person it was for.
func (g *Gateway) FinishOAuth(ctx context.Context, state, code string) (connector, who string, err error) {
	p, ok := g.states.take(state)
	if !ok {
		return "", "", &Error{Code: "bad_request", Msg: "this sign-in has expired or was already used; start it again"}
	}
	c := g.reg.Get(p.connector)
	if c == nil || c.OAuth == nil {
		return "", "", &Error{Code: "not_found", Msg: "no such connector"}
	}
	if code == "" {
		return "", "", &Error{Code: "bad_request", Msg: "the service did not sign you in"}
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", p.redirect)
	form.Set("client_id", c.OAuth.ClientID)
	form.Set("client_secret", c.OAuth.ClientSecret.Value())
	form.Set("code_verifier", p.verifier)
	tok, err := g.tokenRequest(ctx, c, form)
	if err != nil {
		return "", "", err
	}
	tok.Account = g.account(ctx, c, tok)
	if err := g.tokens.Put(c.ID, p.who, tok); err != nil {
		return "", "", err
	}
	g.audit(Entry{Who: p.who, Connector: c.ID, Op: "(connect)", Status: "ok"})
	return c.ID, p.who, nil
}

// tokenRequest posts to the token address and reads the token (GitHub
// answers form-encoded unless asked for JSON; errors come with status 200)
func (g *Gateway) tokenRequest(ctx context.Context, c *Connector, form url.Values) (*Token, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", c.OAuth.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, &Error{Code: "remote", Msg: "could not reach the service's sign-in: " + err.Error()}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	var out struct {
		AccessToken  string  `json:"access_token"`
		TokenType    string  `json:"token_type"`
		Scope        string  `json:"scope"`
		RefreshToken string  `json:"refresh_token"`
		ExpiresIn    float64 `json:"expires_in"`
		Error        string  `json:"error"`
		ErrorDesc    string  `json:"error_description"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		// a form-encoded answer
		v, perr := url.ParseQuery(string(b))
		if perr != nil {
			return nil, &Error{Code: "remote", Msg: fmt.Sprintf("the service's sign-in answered %d", resp.StatusCode)}
		}
		out.AccessToken, out.TokenType, out.Scope = v.Get("access_token"), v.Get("token_type"), v.Get("scope")
		out.RefreshToken, out.Error, out.ErrorDesc = v.Get("refresh_token"), v.Get("error"), v.Get("error_description")
	}
	if out.Error != "" || out.AccessToken == "" {
		msg := out.Error
		if out.ErrorDesc != "" {
			msg += ": " + out.ErrorDesc
		}
		if msg == "" {
			msg = fmt.Sprintf("status %d, no token", resp.StatusCode)
		}
		return nil, &Error{Code: "remote", Msg: "the service did not give a token (" + msg + ")"}
	}
	tok := &Token{AccessToken: out.AccessToken, TokenType: out.TokenType, Scope: out.Scope, RefreshToken: out.RefreshToken, At: g.now().UnixMilli()}
	if out.ExpiresIn > 0 {
		tok.Expires = g.now().Add(time.Duration(out.ExpiresIn) * time.Second).UnixMilli()
	}
	return tok, nil
}

// account reads the signed-in account's name, for showing who is connected
// (best effort)
func (g *Gateway) account(ctx context.Context, c *Connector, tok *Token) string {
	if c.OAuth.UserURL == "" || c.OAuth.UserField == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", c.OAuth.UserURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var v map[string]any
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&v) != nil {
		return ""
	}
	s, _ := v[c.OAuth.UserField].(string)
	return s
}

// userToken is the person's token, refreshed when it has expired and the
// service gave a refresh token
func (g *Gateway) userToken(ctx context.Context, c *Connector, who string) (*Token, error) {
	tok, err := g.tokens.Get(c.ID, who)
	if err != nil {
		return nil, err
	}
	if tok == nil {
		return nil, &Error{Code: "not_connected", Msg: "connect your " + c.title() + " account first"}
	}
	if tok.Expires == 0 || g.now().UnixMilli() < tok.Expires-30_000 {
		return tok, nil
	}
	if tok.RefreshToken == "" {
		return nil, &Error{Code: "not_connected", Msg: "your " + c.title() + " sign-in has expired; connect again"}
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", tok.RefreshToken)
	form.Set("client_id", c.OAuth.ClientID)
	form.Set("client_secret", c.OAuth.ClientSecret.Value())
	fresh, err := g.tokenRequest(ctx, c, form)
	if err != nil {
		return nil, &Error{Code: "not_connected", Msg: "your " + c.title() + " sign-in could not be renewed; connect again"}
	}
	if fresh.RefreshToken == "" {
		fresh.RefreshToken = tok.RefreshToken
	}
	fresh.Account = tok.Account
	if err := g.tokens.Put(c.ID, who, fresh); err != nil {
		return nil, err
	}
	return fresh, nil
}

func (c *Connector) title() string {
	if c.Title != "" {
		return c.Title
	}
	return c.ID
}
