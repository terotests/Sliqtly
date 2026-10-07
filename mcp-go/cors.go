// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Which pages of other origins may call /api/v1 and /oauth/token from a
// browser (apiv1.go). Those requests carry a bearer token the page holds,
// never a cookie, so answering them leaks nothing the page did not already
// have; the list only says which pages are meant to be clients at all. The
// old /api stays with localguard.go's rule: it has no sign-in, and a page
// elsewhere may only read what a link to one deck opens.
//
// The list is -cors-origins / SLIQTLY_CORS_ORIGINS (comma separated, each
// scheme://host[:port]), and pages on this computer (http or https on
// localhost, 127.0.0.1 or [::1], any port) are on it by default: the
// desktop app's own page and a web UI under development.
type corsPolicy struct {
	origins map[string]bool
}

func newCORSPolicy(list []string) (*corsPolicy, error) {
	c := &corsPolicy{origins: map[string]bool{}}
	for _, o := range list {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		n, ok := normalOrigin(o)
		if !ok {
			return nil, fmt.Errorf("cors origin %q: an origin is scheme://host[:port], http or https, with no path", o)
		}
		c.origins[n] = true
	}
	return c, nil
}

// scheme://host[:port] in lower case, the default port left out, as a
// browser sends it in Origin
func normalOrigin(o string) (string, bool) {
	u, err := url.Parse(o)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return u.Scheme + "://" + host, true
}

// a page on this computer: http(s) on a loopback name or address
func loopbackOrigin(o string) bool {
	u, err := url.Parse(o)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// whether a page of origin o may call the API from a browser
func (c *corsPolicy) allowed(o string) bool {
	if o == "" || o == "null" {
		return false
	}
	if loopbackOrigin(o) {
		return true
	}
	n, ok := normalOrigin(o)
	return ok && c.origins[n]
}

// the listed origins, for the web client's redirect addresses
func (c *corsPolicy) list() []string {
	out := make([]string, 0, len(c.origins))
	for o := range c.origins {
		out = append(out, o)
	}
	return out
}

// headers on an answer to a page of origin o (allowed already checked)
func corsHeaders(w http.ResponseWriter, o string) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", o)
	h.Add("Vary", "Origin")
	h.Set("Access-Control-Expose-Headers", "ETag, WWW-Authenticate")
}

// the answer to a preflight from o, for methods
func corsPreflight(w http.ResponseWriter, r *http.Request, o, methods string) {
	corsHeaders(w, o)
	h := w.Header()
	h.Set("Access-Control-Allow-Methods", methods)
	h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-Match")
	h.Set("Access-Control-Max-Age", "600")
	// a page on a public address asking one on the network (Chrome's
	// Private Network Access) asks this too
	if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
		h.Set("Access-Control-Allow-Private-Network", "true")
	}
	w.WriteHeader(http.StatusNoContent)
}

// a ResponseWriter that, whatever the handler behind it set, answers
// origin's page with origin in Access-Control-Allow-Origin: the Ranger
// app (rgr/App.rgr cors) says "*" on everything, which this server narrows
// to the pages on its list
type corsWriter struct {
	http.ResponseWriter
	origin string
	done   bool
}

func (c *corsWriter) WriteHeader(code int) {
	if !c.done {
		c.done = true
		h := c.Header()
		h.Del("Access-Control-Allow-Origin")
		corsHeaders(c.ResponseWriter, c.origin)
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *corsWriter) Write(b []byte) (int, error) {
	if !c.done {
		c.WriteHeader(http.StatusOK)
	}
	return c.ResponseWriter.Write(b)
}
