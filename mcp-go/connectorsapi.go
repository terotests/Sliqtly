// SPDX-License-Identifier: AGPL-3.0-or-later

// Connectors on a server of one's own (-connectors; connectors/ has the
// model, /mnt/project-files/workflow/CONNECTORS.md the design). Off by
// default: without the flag none of this is routed.
//
//	/api/v1/connectors                      GET   the connectors (no secrets) and
//	                                              whether the caller has connected
//	/api/v1/connectors/<id>/connect         POST  {url}: where the caller's browser
//	                                              goes to connect their account
//	/api/v1/connectors/<id>/connection      DELETE forget the caller's sign-in
//	/api/v1/connectors/call                 POST  {deck, connector, op, args}
//	/api/v1/connectors/grants               GET/POST/DELETE  admin only
//	/connectors/oauth/callback              GET   the service sends the browser back
//
// The settings page uses the same through /api/settings/connectors, as the
// server's own user, and changes grants only from the server's computer.
//
// The admin approves every grant: the server's own user on the server's
// own computer, or a signed-in account listed in -admin.

package main

import (
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/terotests/sliqtly/mcp-go/connectors"
)

var connectorPath = regexp.MustCompile(`^/api/(?:v1|settings)/connectors/([a-z][a-z0-9-]{0,39})/(connect|connection)$`)

// whether this caller is the server's admin
func (s *localServer) connectorAdmin(r *http.Request, who *principal) bool {
	if who == nil {
		return false
	}
	if who.ID == s.env.LocalUser && fromHere(r) {
		return true
	}
	if who.Email == "" {
		return false
	}
	for _, a := range s.admins {
		if strings.EqualFold(a, who.Email) {
			return true
		}
	}
	return false
}

func (s *localServer) connectorRedirect(r *http.Request) string {
	return s.origin(r) + "/connectors/oauth/callback"
}

type connectorCallBody struct {
	Deck      string         `json:"deck"`
	Connector string         `json:"connector"`
	Op        string         `json:"op"`
	Args      map[string]any `json:"args"`
	Ops       []string       `json:"ops"`
	// the admin trying an operation from the settings page
	Test bool `json:"test"`
}

func readConnectorBody(r *http.Request) (connectorCallBody, error) {
	var b connectorCallBody
	data, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return b, err
	}
	if err := json.Unmarshal(data, &b); err != nil {
		return b, fail(400, "", "not JSON: "+err.Error())
	}
	return b, nil
}

// connectorsAPI answers /api/v1/connectors… and /api/settings/connectors…
// for a caller already known (who). It returns false when the path is not
// one of these.
func (s *localServer) connectorsAPI(w http.ResponseWriter, r *http.Request, who *principal) {
	g := s.conn
	p := r.URL.Path
	rest := p[strings.Index(p, "/connectors"):]
	errOut := func(err error) {
		var ce *connectors.Error
		var ae *apiError
		switch {
		case errors.As(err, &ce):
			status := map[string]int{"not_found": 404, "bad_request": 400, "forbidden": 403, "not_connected": 409, "not_configured": 503, "quota": 429, "too_large": 502, "timeout": 504, "remote": 502}[ce.Code]
			if status == 0 {
				status = 500
			}
			out := map[string]any{"error": ce.Msg, "code": ce.Code}
			if ce.Status != 0 {
				out["status"] = ce.Status
			}
			writeJSON(w, status, out)
		case errors.As(err, &ae):
			v1Error(w, ae.status, ae.msg)
		default:
			v1Error(w, 500, err.Error())
		}
	}
	if g == nil {
		writeJSON(w, 404, map[string]string{"error": "connectors are off on this server (start it with -connectors)", "code": "not_found"})
		return
	}
	admin := s.connectorAdmin(r, who)
	switch m := connectorPath.FindStringSubmatch(p); {
	case rest == "/connectors" && r.Method == http.MethodGet:
		list := []map[string]any{}
		for _, id := range g.Registry().IDs() {
			c := g.Registry().Get(id)
			v := c.Public()
			if c.OAuth != nil {
				ok, acct, _ := g.Connection(id, who.ID)
				v["connected"], v["account"] = ok, acct
			}
			list = append(list, v)
		}
		out := map[string]any{"connectors": list, "admin": admin}
		if admin {
			out["problems"] = g.Registry().Problems
			out["callback"] = s.connectorRedirect(r)
		}
		writeJSON(w, 200, out)
	case m != nil && m[2] == "connect" && r.Method == http.MethodPost:
		u, err := g.StartOAuth(m[1], who.ID, s.connectorRedirect(r))
		if err != nil {
			errOut(err)
			return
		}
		writeJSON(w, 200, map[string]string{"url": u})
	case m != nil && m[2] == "connection" && r.Method == http.MethodDelete:
		if err := g.Disconnect(m[1], who.ID); err != nil {
			errOut(err)
			return
		}
		w.WriteHeader(204)
	case rest == "/connectors/call" && r.Method == http.MethodPost:
		b, err := readConnectorBody(r)
		if err != nil {
			errOut(err)
			return
		}
		call := connectors.Call{Who: who.ID, Deck: b.Deck, Connector: b.Connector, Op: b.Op, Args: b.Args}
		if b.Test {
			if !admin {
				errOut(fail(403, "", "only the server's admin tries operations without a deck"))
				return
			}
			call.Admin, call.Deck = true, ""
		} else {
			// the caller must be able to open the deck
			if _, err := s.v1Get(r.Context(), b.Deck); err != nil {
				errOut(fail(404, "", "no such deck"))
				return
			}
		}
		out, err := g.Do(r.Context(), call)
		if err != nil {
			errOut(err)
			return
		}
		writeJSON(w, 200, map[string]any{"result": out})
	case rest == "/connectors/grants":
		if !admin {
			errOut(fail(403, "", "only the server's admin sees and changes grants"))
			return
		}
		switch r.Method {
		case http.MethodGet:
			grants, reqs := g.Grants().List()
			writeJSON(w, 200, map[string]any{"grants": grants, "requests": reqs, "recent": g.Recent(30)})
		case http.MethodPost, http.MethodDelete:
			b, err := readConnectorBody(r)
			if err != nil {
				errOut(err)
				return
			}
			c := g.Registry().Get(b.Connector)
			if c == nil {
				errOut(fail(404, "", "no such connector"))
				return
			}
			for _, op := range b.Ops {
				if op != "*" && c.Operations[op] == nil {
					errOut(fail(400, "", b.Connector+" has no operation "+op))
					return
				}
			}
			if b.Deck != "*" && !shareID.MatchString(b.Deck) {
				errOut(fail(400, "", "deck: a deck's id, or * for every deck"))
				return
			}
			switch {
			case r.Method == http.MethodPost:
				err = g.Grants().Approve(b.Deck, b.Connector, b.Ops, who.ID)
			case b.Op != "":
				err = g.Grants().Dismiss(b.Deck, b.Connector, b.Op)
			default:
				err = g.Grants().Revoke(b.Deck, b.Connector, b.Ops)
			}
			if err != nil {
				errOut(err)
				return
			}
			grants, reqs := g.Grants().List()
			writeJSON(w, 200, map[string]any{"grants": grants, "requests": reqs})
		default:
			errOut(fail(405, "", "method not allowed"))
		}
	default:
		errOut(fail(404, "", "not found"))
	}
}

// the settings page's way in: the server's own user, as the rest of
// /api/settings, and only on a server without sign-in or from its own
// computer (a page cannot show a bearer token it does not have)
func (s *localServer) settingsConnectors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSON(w, 415, map[string]string{"error": "send JSON"})
		return
	}
	if s.authRequired() && !fromHere(r) {
		writeJSON(w, 403, map[string]string{"error": "on a server with sign-in, connectors are set on the server's own computer or through /api/v1"})
		return
	}
	s.connectorsAPI(w, r, &principal{ID: s.env.LocalUser, Name: s.env.LocalUser})
}

var connectedPage = template.Must(template.New("c").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sliqtly</title>
<style>:root{color-scheme:light dark}body{font:16px/1.5 system-ui,sans-serif;max-width:560px;margin:48px auto;padding:0 16px}</style>
</head><body>
<h1>{{if .OK}}Connected{{else}}Not connected{{end}}</h1>
<p>{{.Msg}}</p>
<p><a href="/settings#connectors">Back to the settings</a></p>
<script>try { window.opener && window.opener.postMessage({ sliqtly: "connector", ok: {{.OK}} }, location.origin); } catch (e) {}</script>
</body></html>`))

// GET /connectors/oauth/callback?code=…&state=…
func (s *localServer) connectorCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	data := struct {
		OK  bool
		Msg string
	}{}
	if e := q.Get("error"); e != "" {
		// the state is spent either way
		s.conn.FinishOAuth(r.Context(), q.Get("state"), "")
		data.Msg = "The service said: " + e + "."
		w.WriteHeader(400)
	} else if id, _, err := s.conn.FinishOAuth(r.Context(), q.Get("state"), q.Get("code")); err != nil {
		data.Msg = err.Error()
		w.WriteHeader(400)
	} else {
		data.OK = true
		data.Msg = "Your account is connected to " + s.conn.Registry().Get(id).Public()["title"].(string) + " on this server. You can close this window."
	}
	connectedPage.Execute(w, data)
}
