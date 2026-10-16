// SPDX-License-Identifier: AGPL-3.0-or-later

// Who may use a server of one's own once others can reach it. On this
// computer only (SLIQTLY_LISTEN=local, the default) and without a token,
// everything is open: nobody else connects. When it listens further
// (wired, network: the .deb, Docker) or has a token (-token) or sign-in
// (-oidc-*), every request needs the server's credential, the one /mcp
// takes:
//
//	Authorization: Bearer <token>   a program: an MCP client, curl
//	the access cookie               the server's own pages in a browser
//
// The token is SLIQTLY_TOKEN; without one, the server makes its own the
// first time it needs one and keeps it in <data>/access-token, so a
// restart keeps it (the log says where). An access token from the OAuth
// server (oidc.go) is taken too, as on /mcp.
//
// A browser gets the cookie by opening any page of the server once with
// ?token=<token> (the server sends it on without the token in the
// address), or by typing the token into the page every refused page shows.
// The cookie is HttpOnly and SameSite=Strict: no script reads it, and no
// request another site starts carries it. Its value is a MAC of the token,
// never the token, so it does not work as a bearer token on /mcp. A link
// to the server clicked on another site comes without it (Strict); the
// refused page then asks the server, from its own origin, whether the
// browser has it, and opens the link again if so.
//
// Open still: the server's state (/healthz, /api/status), the sign-in and
// OAuth endpoints, the CA certificate, a connector's sign-in callback,
// questionnaires (forms.go) and a recording's viewing link (localplay.go),
// which are links meant for people without the token, and the built page's
// own files (scripts, styles, fonts, themes), which hold no one's data.
// /mcp and /api/v1 check the bearer token themselves, as before.

package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// where the server's own token is kept in the data folder
const accessTokenFile = "access-token"

// how long a browser keeps the access cookie
const accessCookieAge = 180 * 24 * time.Hour

// a recording opened by its viewing link (localplay.go): the link's 128
// random bits are the credential
var recLinkPath = regexp.MustCompile(`^/api/view/[0-9a-f]{32}/rec/[0-9a-f]{24}$`)

// whether others can connect: the server listens beyond this computer
func (s *localServer) exposed() bool {
	return s.expo != nil && s.expo.policy.Load().Access != accessLocal
}

// the token requests are checked against: SLIQTLY_TOKEN, or the server's
// own, made (and kept in the folder) the first time one is needed
func (s *localServer) secret() string {
	if s.token != "" {
		return s.token
	}
	s.autoMu.Lock()
	defer s.autoMu.Unlock()
	if s.autoToken != "" {
		return s.autoToken
	}
	t, where := "", "memory only (no data folder)"
	if s.tokenFile != "" {
		where = s.tokenFile
		if b, err := os.ReadFile(s.tokenFile); err == nil && len(strings.TrimSpace(string(b))) >= 16 {
			t = strings.TrimSpace(string(b))
		}
	}
	if t == "" {
		b := make([]byte, 24)
		rand.Read(b)
		t = base64.RawURLEncoding.EncodeToString(b)
		if s.tokenFile != "" {
			tmp := s.tokenFile + ".tmp"
			err := os.WriteFile(tmp, []byte(t+"\n"), 0o600)
			if err == nil {
				err = os.Rename(tmp, s.tokenFile)
			}
			if err != nil {
				log.Printf("access token: %v (it lasts until the server stops)", err)
				where = "memory only"
			}
		}
	}
	// said once a start: the image has no shell to read the file with, and
	// the token is the server's own to show, as SLIQTLY_TOKEN is the
	// starter's
	log.Printf("this server needs an access token and SLIQTLY_TOKEN is not set: it uses its own (kept in %s). Agents send Authorization: Bearer %s; a browser opens %s/?token=%s once", where, t, s.env.BaseURL, t)
	s.autoToken = t
	return t
}

// where the owner finds the token, for the settings page
func (s *localServer) secretWhere() string {
	if s.token != "" {
		return "SLIQTLY_TOKEN"
	}
	if s.tokenFile != "" {
		return s.tokenFile
	}
	return "the server's log"
}

// the cookie's name: a browser keeps cookies by name, not by port, so two
// servers on one computer keep one each
func accessCookieName(r *http.Request) string {
	if _, port, err := net.SplitHostPort(r.Host); err == nil && port != "" {
		return "sliqtly_access_" + port
	}
	return "sliqtly_access"
}

// the cookie's value: a MAC of the token, so the cookie is no bearer token
func (s *localServer) cookieValue() string {
	m := hmac.New(sha256.New, []byte(s.secret()))
	m.Write([]byte("sliqtly browser access 1"))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *localServer) setAccessCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: accessCookieName(r), Value: s.cookieValue(), Path: "/",
		MaxAge:   int(accessCookieAge / time.Second),
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
	})
}

// whether the request carries the server's credential: a bearer token
// /mcp takes, or the access cookie
func (s *localServer) credentialOK(r *http.Request) bool {
	if r.Header.Get("Authorization") != "" {
		_, err := s.authenticate(r)
		return err == nil
	}
	c, err := r.Cookie(accessCookieName(r))
	return err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.cookieValue())) == 1
}

// whether the request carries the server's own token or cookie (not an
// account's OAuth token): its owner, as on its own computer
func (s *localServer) ownerCredential(r *http.Request) bool {
	if h := r.Header.Get("Authorization"); h != "" {
		return len(h) > 7 && strings.EqualFold(h[:7], "bearer ") &&
			subtle.ConstantTimeCompare([]byte(strings.TrimSpace(h[7:])), []byte(s.secret())) == 1
	}
	c, err := r.Cookie(accessCookieName(r))
	return err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.cookieValue())) == 1
}

// what is answered without the credential (see the top of the file)
func (s *localServer) publicPath(r *http.Request) bool {
	p := r.URL.Path
	switch {
	case p == "/healthz", p == "/api/status", p == "/ca", p == "/ca.crt",
		p == "/mcp", p == "/api/v1", strings.HasPrefix(p, "/api/v1/"),
		p == "/oauth.html", strings.HasPrefix(p, "/oauth/"), strings.HasPrefix(p, "/.well-known/"),
		p == "/connectors/oauth/callback",
		formCodePath.MatchString(p), formTokenPath.MatchString(p), formResultsPath.MatchString(p),
		playPath.MatchString(p), recLinkPath.MatchString(p):
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return themePath.MatchString(p) || s.buildFile(p)
}

// a file of the built page other than the page itself: the scripts,
// styles and fonts every copy of the build has
func (s *localServer) buildFile(p string) bool {
	for _, pre := range []string{"/api/", "/files/", "/s/", "/r/", "/apps/"} {
		if strings.HasPrefix(p, pre) {
			return false
		}
	}
	name := strings.TrimPrefix(path.Clean(p), "/")
	switch {
	case name == "" || name == "." || name == "index.html" || name == "decks" || name == "settings":
		return false
	case name == "sliqtly.js":
		return s.web != nil
	case strings.HasPrefix(name, "fonts/"):
		if _, err := fontFiles.ReadFile(name); err == nil {
			return true
		}
	}
	if s.web == nil {
		return false
	}
	st, err := fs.Stat(s.web, name)
	return err == nil && !st.IsDir()
}

// false when the request was answered here: refused, or a browser handed
// the cookie
func (s *localServer) gate(w http.ResponseWriter, r *http.Request) bool {
	if !s.authRequired() {
		return true
	}
	switch r.URL.Path {
	case "/access":
		s.accessForm(w, r)
		return false
	case "/access/check":
		w.Header().Set("Cache-Control", "no-store")
		if s.credentialOK(r) {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusUnauthorized)
		}
		return false
	}
	if s.publicPath(r) || s.credentialOK(r) {
		return true
	}
	q := r.URL.Query()
	if t := q.Get("token"); t != "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		if subtle.ConstantTimeCompare([]byte(t), []byte(s.secret())) == 1 {
			s.setAccessCookie(w, r)
			q.Del("token")
			to := r.URL.Path
			if len(q) > 0 {
				to += "?" + q.Encode()
			}
			w.Header().Set("Referrer-Policy", "no-referrer")
			http.Redirect(w, r, to, http.StatusSeeOther)
			return false
		}
		s.refuse(w, r, "That is not this server's access token.")
		return false
	}
	s.refuse(w, r, "")
	return false
}

// 401: a page that asks for the token to a browser opening a page, JSON
// to anything else
func (s *localServer) refuse(w http.ResponseWriter, r *http.Request, why string) {
	w.Header().Set("Cache-Control", "no-store")
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.Contains(r.Header.Get("Accept"), "text/html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.WriteHeader(http.StatusUnauthorized)
		next := r.URL.Path
		if r.URL.RawQuery != "" {
			q := r.URL.Query()
			q.Del("token")
			if len(q) > 0 {
				next += "?" + q.Encode()
			}
		}
		accessTmpl.Execute(w, map[string]any{"Next": next, "Why": why, "Retry": why == ""})
		return
	}
	w.Header().Set("WWW-Authenticate", s.challenge(r, "/.well-known/oauth-protected-resource", false))
	writeJSON(w, http.StatusUnauthorized, map[string]string{
		"error": "this server needs its access token: Authorization: Bearer <token>, or in a browser open a page of it once with ?token=<token>",
		"code":  "unauthorized",
	})
}

// POST /access (the page's form: token, next): the cookie, then the page
func (s *localServer) accessForm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST the token", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "not a form", http.StatusBadRequest)
		return
	}
	next := r.PostForm.Get("next")
	if u, err := url.Parse(next); err != nil || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") || u.Host != "" {
		next = "/"
	}
	t := strings.TrimSpace(r.PostForm.Get("token"))
	if t == "" || subtle.ConstantTimeCompare([]byte(t), []byte(s.secret())) != 1 {
		r.Header.Set("Accept", "text/html")
		r.Method = http.MethodGet
		r.URL, _ = url.Parse(next)
		s.refuse(w, r, "That is not this server's access token.")
		return
	}
	s.setAccessCookie(w, r)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

var accessTmpl = template.Must(template.New("access").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sliqtly</title>
<style>
:root { color-scheme: light dark; --bg: #f6f6f4; --fg: #1d1d1b; --muted: #6b6b66; --line: #d6d6d0; }
@media (prefers-color-scheme: dark) { :root { --bg: #141414; --fg: #ececea; --muted: #9a9a94; --line: #3a3a3a; } }
body { margin: 0; background: var(--bg); color: var(--fg); font: 16px/1.5 system-ui, sans-serif; }
main { max-width: 520px; margin: 0 auto; padding: 48px 16px; }
h1 { font-size: 1.4rem; margin: 0 0 8px; }
.muted { color: var(--muted); font-size: .9rem; }
.bad { color: #c0392b; }
input { box-sizing: border-box; width: 100%; font: inherit; padding: 8px 10px; border: 1px solid var(--line); border-radius: 6px; background: transparent; color: inherit; }
button { margin-top: 12px; font: inherit; padding: 8px 16px; border-radius: 6px; border: 1px solid var(--line); background: var(--fg); color: var(--bg); cursor: pointer; }
</style></head><body><main>
<h1>This Sliqtly server needs its access token</h1>
<p class="muted">Its presentations open only with the token. It is <code>SLIQTLY_TOKEN</code> where the server was started, or, when that is not set, the file <code>access-token</code> in its data folder (the server's log shows it too).</p>
{{if .Why}}<p class="bad" role="alert">{{.Why}}</p>{{end}}
<form method="post" action="/access">
<label for="token">Access token</label>
<input id="token" name="token" type="password" autocomplete="current-password" required autofocus>
<input type="hidden" name="next" value="{{.Next}}">
<button type="submit">Open</button>
</form>
<p class="muted">This browser then keeps it for this server.</p>
</main>
{{if .Retry}}<script>
// a link clicked on another site comes without the cookie (SameSite=Strict):
// ask from this page, and if the browser has it, open the link again once
(async () => {
  try {
    const k = "sliqtly-access-retry:" + location.pathname + location.search;
    if (Date.now() - Number(sessionStorage.getItem(k) || 0) < 10000) return;
    const r = await fetch("/access/check", { cache: "no-store", credentials: "same-origin" });
    if (r.status !== 204) return;
    sessionStorage.setItem(k, String(Date.now()));
    location.replace(location.href);
  } catch (e) { /* the form stays */ }
})();
</script>{{end}}
</body></html>`))

// with -data: the server's own token is kept in the folder
func accessTokenPath(data string) string {
	return filepath.Join(data, accessTokenFile)
}

// What a page of another origin may read on this server, and so what is
// answered with Access-Control-Allow-Origin: one deck by its id (its
// unguessable id is the link), that deck's files and pictures, and the
// built page's own files, which the assistant's preview loads from its
// own origin. Only on a server nobody else connects to and with no
// token: with the credential required, a page elsewhere never has it
// (the cookie is SameSite=Strict), so nothing there is for it to read.
// Room files, the shared files, lists, streams and settings are this
// server's own pages' only (localguard.go answers them 403).
func (s *localServer) openToPages(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
		return false
	}
	p := r.URL.Path
	if themePath.MatchString(p) || s.buildFile(p) {
		return true
	}
	if s.authRequired() {
		return false
	}
	if m := shareAPIPath.FindStringSubmatch(p); m != nil {
		return m[2] == ""
	}
	return p == "/index.html" || p == "/api/me" || strings.HasPrefix(p, "/files/shares/") ||
		strings.HasPrefix(p, "/api/view/") || strings.HasPrefix(p, "/api/card/") || strings.HasPrefix(p, "/api/export/") ||
		slidePath.MatchString(p) || overviewPath.MatchString(p)
}

// Access-Control-Allow-Origin on an answer a page elsewhere may read
func (s *localServer) allowPages(w http.ResponseWriter, r *http.Request) {
	if s.openToPages(r) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
	}
}

// a ResponseWriter for the Ranger app, which says
// Access-Control-Allow-Origin: * on everything (rgr/App.rgr cors): kept
// only on what openToPages allows
type pageCORSWriter struct {
	http.ResponseWriter
	keep bool
	done bool
}

func (c *pageCORSWriter) WriteHeader(code int) {
	if !c.done {
		c.done = true
		if !c.keep && c.Header().Get("Access-Control-Allow-Origin") == "*" {
			c.Header().Del("Access-Control-Allow-Origin")
		}
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *pageCORSWriter) Write(b []byte) (int, error) {
	if !c.done {
		c.WriteHeader(http.StatusOK)
	}
	return c.ResponseWriter.Write(b)
}

// the app's streams (MCP's SSE) flush as they go
func (c *pageCORSWriter) Flush() {
	if !c.done {
		c.WriteHeader(http.StatusOK)
	}
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *pageCORSWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// the Ranger app, with its "*" narrowed
func (s *localServer) serveApp(w http.ResponseWriter, r *http.Request) {
	s.app.ServeHTTP(&pageCORSWriter{ResponseWriter: w, keep: s.openToPages(r)}, r)
}
