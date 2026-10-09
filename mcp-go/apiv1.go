// SPDX-License-Identifier: AGPL-3.0-or-later

// /api/v1: the REST API of a server of one's own for its desktop and web
// editors (docs/api-v1.md). The old /api is the built-in page's own and
// keeps localguard.go's rule; this one is for clients of other origins
// and programs, so it signs callers in instead:
//
//	Authorization: Bearer <the server's token (-token)>, compared in
//	constant time, or an access token the server's OAuth 2.1 server
//	issued (oidc.go, rgr/OAuth.rgr)
//
// The same check guards /mcp. A server with neither a token nor sign-in
// answers /api/v1 only to its own pages, pages on this computer and
// programs that send no Origin, as the old /api does.
//
// Saves go through saveShare, the editor's own path, so a deck open in a
// room hears a change made here like any other save. A deck's version is
// its revision in the store: a PUT with ifVersion (or If-Match) is written
// only over that version, and otherwise answered 409 with the deck as it
// is now.

package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/terotests/sliqtly/mcp-go/store"
)

var (
	v1DeckPath = regexp.MustCompile(`^/api/v1/decks/([A-Za-z0-9]{6,32})(/view)?$`)
)

// who a request is: the server's user (the token, or no sign-in at all),
// or an account signed in through the OAuth server
type principal struct {
	ID, Name, Email string
}

// whether /mcp and /api/v1 need a bearer token
func (s *localServer) authRequired() bool { return s.token != "" || s.oidc != nil }

var errNoToken = errors.New("no token")

// the bearer token's principal; errNoToken without one, a 401 apiError for
// one that does not hold, any other error a failure to look
func (s *localServer) authenticate(r *http.Request) (*principal, error) {
	if !s.authRequired() {
		return &principal{ID: s.env.LocalUser, Name: s.env.LocalUser}, nil
	}
	h := r.Header.Get("Authorization")
	if len(h) < 7 || !strings.EqualFold(h[:7], "bearer ") || strings.TrimSpace(h[7:]) == "" {
		return nil, errNoToken
	}
	tok := strings.TrimSpace(h[7:])
	if s.token != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) == 1 {
		return &principal{ID: s.env.LocalUser, Name: s.env.LocalUser}, nil
	}
	if s.oidc != nil {
		w := OAuth_static_who(s.host(r), "Bearer "+tok)
		switch w.state {
		case 1:
			return &principal{ID: w.uid, Name: w.name, Email: w.email}, nil
		case 3:
			return nil, errors.New("token check failed")
		}
	}
	return nil, fail(401, "unauthorized", "the token is not valid or has expired")
}

// the address this server is reached at for this request, as the OAuth
// documents name it (App.originOf)
func (s *localServer) origin(r *http.Request) string {
	return App_static_originOf(s.host(r), r)
}

// WWW-Authenticate for a request without a token that holds: where the
// client finds how to sign in, when it can
func (s *localServer) challenge(r *http.Request, resource string, invalid bool) string {
	v := `Bearer realm="sliqtly"`
	if invalid {
		v += `, error="invalid_token"`
	}
	if s.oidc != nil {
		v += `, resource_metadata="` + s.origin(r) + resource + `"`
	}
	return v
}

// the error codes of docs/api-v1.md, by status
func v1Code(status int) string {
	switch status {
	case 400, 415:
		return "bad_request"
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 404:
		return "not_found"
	case 405:
		return "method_not_allowed"
	case 409:
		return "conflict"
	case 413:
		return "too_large"
	}
	return "internal"
}

func v1Error(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": v1Code(status)})
}

func (s *localServer) apiV1(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	o := r.Header.Get("Origin")
	if o != "" && !s.hosts.sameOrigin(r) {
		// a page of another origin: one on the list, and with no sign-in
		// on this server only one on this computer (info aside, which
		// tells a client how to sign in)
		if !s.cors.allowed(o) || (!s.authRequired() && !loopbackOrigin(o) && p != "/api/v1/info") {
			v1Error(w, 403, "this page's origin may not use this server's API")
			return
		}
		if r.Method == http.MethodOptions {
			corsPreflight(w, r, o, "GET, POST, PUT, DELETE, OPTIONS")
			return
		}
		corsHeaders(w, o)
	} else if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if p == "/api/v1/info" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			v1Error(w, 405, "method not allowed")
			return
		}
		writeJSON(w, 200, s.info(r))
		return
	}
	who, err := s.authenticate(r)
	if err != nil {
		var ae *apiError
		switch {
		case errors.Is(err, errNoToken):
			w.Header().Set("WWW-Authenticate", s.challenge(r, "/.well-known/oauth-protected-resource", false))
			v1Error(w, 401, "this server needs Authorization: Bearer <token>")
		case errors.As(err, &ae):
			w.Header().Set("WWW-Authenticate", s.challenge(r, "/.well-known/oauth-protected-resource", true))
			v1Error(w, 401, ae.msg)
		default:
			v1Error(w, 500, err.Error())
		}
		return
	}
	// a write is JSON, never a form a page elsewhere could send unasked
	if (r.Method == http.MethodPost || r.Method == http.MethodPut) && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		v1Error(w, 415, "send JSON (Content-Type: application/json)")
		return
	}
	if p == "/api/v1/connectors" || strings.HasPrefix(p, "/api/v1/connectors/") {
		s.connectorsAPI(w, r, who)
		return
	}
	var out any
	status := 200
	switch m := v1DeckPath.FindStringSubmatch(p); {
	case p == "/api/v1/me" && r.Method == http.MethodGet:
		me := map[string]any{"id": who.ID, "name": who.Name}
		if who.Email != "" {
			me["email"] = who.Email
		}
		out = me
	case p == "/api/v1/decks" && r.Method == http.MethodGet:
		out, err = s.v1List(r.Context())
	case p == "/api/v1/decks" && r.Method == http.MethodPost:
		out, err = s.v1Create(r)
		status = 201
	case m != nil && m[2] == "/view" && r.Method == http.MethodGet:
		s.v1View(w, r, m[1])
		return
	case m != nil && m[2] == "" && r.Method == http.MethodGet:
		var d *v1Deck
		d, err = s.v1Get(r.Context(), m[1])
		if err == nil {
			w.Header().Set("ETag", `"`+d.Version+`"`)
			out = d
		}
	case m != nil && m[2] == "" && r.Method == http.MethodPut:
		var d *v1Deck
		d, err = s.v1Put(r, m[1])
		if err == nil {
			w.Header().Set("ETag", `"`+d.Version+`"`)
			out = d
		}
	case m != nil && m[2] == "" && r.Method == http.MethodDelete:
		err = s.deleteShare(r.Context(), m[1])
		status = 204
	case p == "/api/v1/me", p == "/api/v1/decks", m != nil:
		err = fail(405, "", "method not allowed")
	default:
		err = fail(404, "", "not found")
	}
	if err != nil {
		var ae *apiError
		var c *v1Conflict
		switch {
		case errors.As(err, &c):
			w.Header().Set("ETag", `"`+c.current.Version+`"`)
			writeJSON(w, 409, map[string]any{"error": "the deck was changed since that version", "code": "conflict", "current": c.current})
		case errors.As(err, &ae):
			v1Error(w, ae.status, ae.msg)
		default:
			v1Error(w, 500, err.Error())
		}
		return
	}
	if status == 204 {
		w.WriteHeader(204)
		return
	}
	writeJSON(w, status, out)
}

// GET /api/v1/info: what a client needs before it signs in
func (s *localServer) info(r *http.Request) map[string]any {
	auth := map[string]any{"required": s.authRequired(), "token": s.token != "", "oauth": s.oidc != nil}
	if s.oidc != nil {
		auth["issuer"] = s.origin(r)
		auth["provider"] = s.oidc.p.displayName()
	}
	tlsInfo := map[string]any{"enabled": s.certs != nil || s.fileCert != nil, "ownCA": s.certs != nil && s.fileCert == nil}
	if s.certs != nil && s.fileCert == nil {
		tlsInfo["fingerprint"] = s.certs.fingerprintHex()
		tlsInfo["ca"] = "/ca.crt"
	}
	return map[string]any{"name": "Sliqtly", "version": version, "api": 1, "user": s.env.LocalUser, "auth": auth, "tls": tlsInfo}
}

// a deck as /api/v1 has it
type v1Deck struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Markdown string `json:"markdown"`
	Theme    string `json:"theme"`
	Updated  int64  `json:"updated"`
	Version  string `json:"version"`
	Room     string `json:"room,omitempty"`
}

func deckOf(id string, d Doc, rev store.Rev) *v1Deck {
	at := millis(d["updated"])
	if at == 0 {
		at = millis(d["created"])
	}
	out := &v1Deck{ID: id, Updated: at, Version: strconv.FormatInt(int64(rev), 10)}
	out.Name, _ = d["name"].(string)
	out.Markdown, _ = d["md"].(string)
	out.Theme, _ = d["theme"].(string)
	out.Room, _ = d[store.RoomField].(string)
	return out
}

// the 409 answer: the deck as it is now
type v1Conflict struct{ current *v1Deck }

func (c *v1Conflict) Error() string { return "conflict" }

func (s *localServer) v1Get(ctx context.Context, id string) (*v1Deck, error) {
	d, rev, err := s.env.Store.Get(ctx, "shares", id)
	if err != nil {
		return nil, err
	}
	if err := s.mine(d); err != nil {
		return nil, err
	}
	return deckOf(id, d, rev), nil
}

type v1Row struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Updated int64  `json:"updated"`
	// the slides as written (a heading or --- starts one); the player may
	// split one that runs over
	Slides int    `json:"slides"`
	Room   string `json:"room,omitempty"`
}

func (s *localServer) v1List(ctx context.Context) (any, error) {
	docs, ids, err := s.env.DB.WhereEq(ctx, "shares", "owner", s.env.LocalUser)
	if err != nil {
		return nil, err
	}
	rows := []v1Row{}
	for i, d := range docs {
		dk := deckOf(ids[i], d, 0)
		rows = append(rows, v1Row{ID: dk.ID, Name: dk.Name, Updated: dk.Updated, Slides: len(Deck_static_outline(dk.Markdown).titles), Room: dk.Room})
	}
	sort.SliceStable(rows, func(a, b int) bool { return rows[a].Updated > rows[b].Updated })
	return map[string]any{"decks": rows}, nil
}

// the fields /api/v1 sets: name, markdown, theme
func v1Fields(body map[string]any) (Doc, error) {
	d := Doc{}
	for k, field := range map[string]string{"name": "name", "markdown": "md", "theme": "theme"} {
		v, ok := body[k]
		if !ok {
			continue
		}
		str, isStr := v.(string)
		if !isStr {
			return nil, fail(400, "", k+" is a string")
		}
		d[field] = str
	}
	return d, nil
}

func (s *localServer) v1Create(r *http.Request) (any, error) {
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	d, err := v1Fields(body)
	if err != nil {
		return nil, err
	}
	if _, ok := d["md"]; !ok {
		d["md"] = ""
	}
	// the theme create_presentation gives a deck that names none
	if t, _ := d["theme"].(string); t == "" {
		d["theme"] = "aurora"
	}
	id, err := s.newShare(r.Context(), d, "api")
	if err != nil {
		return nil, err
	}
	return s.v1Get(r.Context(), id)
}

func (s *localServer) v1Put(r *http.Request, id string) (*v1Deck, error) {
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	patch, err := v1Fields(body)
	if err != nil {
		return nil, err
	}
	// the version it was made on: ifVersion, or If-Match as HTTP has it
	want := ""
	switch v := body["ifVersion"].(type) {
	case string:
		want = v
	case int64:
		want = strconv.FormatInt(v, 10)
	case float64:
		want = strconv.FormatInt(int64(v), 10)
	case nil:
	default:
		return nil, fail(400, "", "ifVersion is the version a GET gave")
	}
	if want == "" {
		want = strings.Trim(strings.TrimPrefix(r.Header.Get("If-Match"), "W/"), `"`)
	}
	var check func(Doc, store.Rev) error
	if want != "" && want != "*" {
		check = func(cur Doc, rev store.Rev) error {
			if strconv.FormatInt(int64(rev), 10) != want {
				return &v1Conflict{deckOf(id, cur, rev)}
			}
			return nil
		}
	}
	d, rev, err := s.saveShare(r.Context(), id, patch, check)
	if err != nil {
		return nil, err
	}
	return deckOf(id, d, rev), nil
}

// GET /api/v1/decks/{id}/view: what /api/view/{id} answers the viewer
// (rgr/View.rgr), for this server's user
func (s *localServer) v1View(w http.ResponseWriter, r *http.Request, id string) {
	if _, err := s.own(r.Context(), id); err != nil {
		var ae *apiError
		if errors.As(err, &ae) {
			v1Error(w, ae.status, ae.msg)
		} else {
			v1Error(w, 500, err.Error())
		}
		return
	}
	body := View_static_json(s.host(r), id, r.URL.Query().Get("slides"), s.env.LocalUser)
	if body == "" {
		v1Error(w, 404, "no such presentation")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(body))
}
