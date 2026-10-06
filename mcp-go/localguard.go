// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Who a server of one's own answers. The network policy (netaccess.go)
// decides which computers may connect; this decides which pages in their
// browsers may use it.
//
// The Host a request names must be this server's: localhost, an IP
// address, the machine's own name, the host of SLIQTLY_URL, or one listed
// in SLIQTLY_HOSTS. A page on the internet can point a name of its own at
// 127.0.0.1 (DNS rebinding) and then reach the server as if from its own
// origin; with its name refused, it reaches nothing.
//
// A request from another origin (a page elsewhere, or the assistant's
// preview, whose origin is "null") may only read what a link to one deck
// already opens: the page and its files, the themes, one deck by its id
// and that deck's files, and the server's status. The deck list, the
// rooms, editing together, the streams, the settings, every write and
// /mcp answer only this server's own pages and programs that send no
// Origin (an MCP client, curl).
type hostGuard struct {
	names map[string]bool
	// the names set where the server is started (SLIQTLY_URL when given,
	// SLIQTLY_HOSTS): a page there is the server's own even when a proxy
	// in front hands the request on under another Host
	own map[string]bool
}

// baseURL counts as the server's own name when it was set (setURL), not
// when it is the default localhost one
func newHostGuard(baseURL string, setURL bool) *hostGuard {
	g := &hostGuard{names: map[string]bool{"localhost": true}, own: map[string]bool{}}
	if u, err := url.Parse(baseURL); err == nil && u.Hostname() != "" {
		g.names[strings.ToLower(u.Hostname())] = true
		if setURL {
			g.own[strings.ToLower(u.Host)] = true
		}
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		h = strings.ToLower(h)
		g.names[h] = true
		g.names[strings.TrimSuffix(h, ".local")+".local"] = true
	}
	for _, h := range strings.Split(os.Getenv("SLIQTLY_HOSTS"), ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			g.names[h] = true
			g.own[h] = true
		}
	}
	return g
}

// a Host header (name or address, maybe with a port) that is this server's
func (g *hostGuard) hostOK(hostport string) bool {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	h = strings.ToLower(strings.TrimSuffix(strings.Trim(h, "[]"), "."))
	if h == "" {
		return false
	}
	if net.ParseIP(h) != nil {
		return true
	}
	return g.names[h] || strings.HasSuffix(h, ".localhost")
}

// whether the request comes from a page of this server itself (its origin
// is the Host asked, or a name set as the server's own), or from a program
// that is no page at all
func (g *hostGuard) sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil || u.Host == "" {
		return false
	}
	h := strings.ToLower(u.Host)
	return h == strings.ToLower(r.Host) || g.own[h] || g.own[strings.ToLower(u.Hostname())]
}

// what a page elsewhere may read: what a link to one deck opens
func crossOriginReadable(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
		return false
	}
	p := r.URL.Path
	switch {
	case p == "/api/status", p == "/api/me":
		return true
	case shareAPIPath.MatchString(p), strings.HasPrefix(p, "/api/view/"):
		return true
	case strings.HasPrefix(p, "/api/"), p == "/mcp", strings.HasPrefix(p, "/oauth/"),
		strings.HasPrefix(p, "/.well-known/"), p == "/settings", p == "/decks":
		return false
	}
	// the page, its files, the themes, a deck's player and pictures, its
	// files under /files/shares/
	return true
}

// false when the request was answered here (refused)
func (s *localServer) guard(w http.ResponseWriter, r *http.Request) bool {
	if !s.hosts.hostOK(r.Host) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusMisdirectedRequest)
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		// the name is the server's own to add; a page cannot read this
		io.WriteString(w, "This Sliqtly server does not answer to the name "+host+". If it is this server's own name, start the server with SLIQTLY_HOSTS="+host+" (or SLIQTLY_URL set to its address).\n")
		return false
	}
	if !s.hosts.sameOrigin(r) && !crossOriginReadable(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only this server's own pages may do this"})
		return false
	}
	return true
}
