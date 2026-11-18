// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

func TestGamesTextWarnings(t *testing.T) {
	cases := []struct {
		path, text string
		n          int
	}{
		{"game.tsx", `<div>1. Lue\n2. Tee</div>`, 1},
		{"game.tsx", `const s = "1. Lue\\n2. Tee";`, 1},
		{"data.json", `{"t": "a\\nb"}`, 1},
		{"game.tsx", `const s = "1. Lue\n2. Tee";`, 0},
		{"game.tsx", `<div>{"a\nb"}</div>`, 0},
		{"lib/x.ts", `const re = /\\n/; // rg-allow-backslash-n`, 0},
		{"style.css", `.a::after { content: "\\n"; }`, 0},
	}
	for _, c := range cases {
		if got := gamesTextWarnings(c.path, []byte(c.text)); len(got) != c.n {
			t.Errorf("%s %q: %v", c.path, c.text, got)
		}
	}
}

func TestGamesWriteWarnsOfLiteralNewline(t *testing.T) {
	env, db, _ := gamesEnv(t)
	tok := gamesToken(db, "u1", "admin@example.com", gamesBase+gamesMcpPath)
	g := gamesMustTool(t, env, tok, "create_game", map[string]any{"name": "Go"})
	id := g["game_id"].(string)
	tsx := "function view() {\n  return <div>{\"1. Lue tavoite.\\\\n2. Tee siirto.\"}</div>;\n}\n"
	out := gamesMustTool(t, env, tok, "write_files", map[string]any{"game_id": id, "files": []any{
		map[string]any{"path": "game.tsx", "text": tsx},
	}})
	ws, _ := out["warnings"].([]any)
	if len(ws) != 1 || !strings.Contains(ws[0].(string), "game.tsx:2:") {
		t.Fatalf("warnings %v", out["warnings"])
	}
	out = gamesMustTool(t, env, tok, "write_files", map[string]any{"game_id": id, "files": []any{
		map[string]any{"path": "game.tsx", "text": "function view() {\n  return <div><div>1. Lue tavoite.</div><div>2. Tee siirto.</div></div>;\n}\n"},
	}})
	if out["warnings"] != nil {
		t.Fatalf("warnings for a clean file: %v", out["warnings"])
	}
}
