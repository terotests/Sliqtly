// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "testing"

// a token asked for Games or Warden reads and changes no deck
func TestTokenForDecks(t *testing.T) {
	h := &McpHost{}
	for res, want := range map[string]bool{
		"":                                    true,
		"https://sliqtly.com":                 true,
		"https://sliqtly.com/":                true,
		"https://sliqtly.com/mcp":             true,
		"https://sliqtly.com/mcp/":            true,
		"https://sliqtly.com/mcp/account":     true,
		"http://localhost:8080/mcp":           true,
		"https://sliqtly.com/games/mcp":       false,
		"https://sliqtly.com/apps/warden/mcp": false,
		"https://sliqtly.com/mcpx":            false,
		"not a url":                           false,
	} {
		eq(t, OAuth_static_forDecks(h, res), want, res)
	}
}

// pages that act for a signed-in person are not framed by other sites; a
// game's player and Warden's feeds are left as they are
func TestNoForeignFrames(t *testing.T) {
	for p, want := range map[string]bool{
		"/editor":                  true,
		"/editor/":                 true,
		"/editor/api/save":         true,
		"/editorx":                 false,
		"/main/admin/api/stats":    true,
		"/apps/warden/":            true,
		"/apps/warden/api/list":    true,
		"/apps/warden/cal/abc.ics": false,
		"/apps/warden/ics/a/b.ics": false,
		"/games":                   true,
		"/games/editor":            true,
		"/games/main/admin":        true,
		"/games/p/tok":             false,
		"/games/x/shakki":          false,
		"/games/engine/index.html": false,
		"/s/abc":                   false,
		"/mcp":                     false,
	} {
		eq(t, noForeignFrames(p), want, p)
	}
}
