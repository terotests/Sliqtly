package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A slide's script ({script=apps/fx.tsx}): get_display_list lists what it
// finds on its slide, a selector is tried there, and create says which of
// the script's selectors find nothing (PresScriptCheck, src/PresScript.rgr).
func TestSlideScript(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "# Deck\n\n## Sales grow {script=apps/fx.tsx}\n\n- Saturday record\n- Buns sell\n\n" +
		"```mermaid\nflowchart LR\n  A[Order] --> B[Bake]\n```\n"
	src := "function tick(dt) { find(\"li:2\").set({ opacity: 0.5 }); find(\"edge A->B\").set({ stroke: \"red\" }); find(\"chart:2 bar\").set({ scale: 2 }); }\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Deck", "markdown": md,
		"files": []any{map[string]any{"name": "fx.tsx", "text": src}}})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	w := fmt.Sprint(sc(c)["warnings"])
	match(t, w, `Slide 2: script apps/fx\.tsx: find\("chart:2 bar"\) → 0 entities → topic=scripts`)
	if strings.Contains(w, `find("li:2")`) || strings.Contains(w, `find("edge A->B")`) {
		t.Fatal("a selector that finds something was reported:", w)
	}
	id := sc(c)["deck_id"].(string)

	d := call(t, s, "get_display_list", map[string]any{"deck_id": id, "slide": "Sales grow", "selector": "li:2"})
	if d.IsError {
		t.Fatal(textOf(d))
	}
	txt := textOf(d)
	match(t, txt, `li-2 li "Buns sell"`)
	match(t, txt, `node-B node "Bake"`)
	match(t, txt, `find\("li:2"\) → 1 entities: li-2`)
	eq(t, fmt.Sprint(sc(d)["found"]), "[li-2]")
	if _, ok := sc(d)["entities"].([]any); !ok {
		t.Fatal("no entities:", sc(d))
	}

	bad := call(t, s, "get_display_list", map[string]any{"deck_id": id, "slide": 2, "selector": "li:x"})
	match(t, textOf(bad), `find\("li:x"\): .*no number`)
	none := call(t, s, "get_display_list", map[string]any{"deck_id": id, "slide": 9})
	if !none.IsError {
		t.Fatal("slide 9 of 2 was listed")
	}

	// a script the deck does not have is said once, as a script
	c2 := call(t, s, "create_presentation", map[string]any{"title": "Deck", "markdown": md})
	match(t, fmt.Sprint(sc(c2)["warnings"]), `apps/fx\.tsx is a slide's script .*→ topic=scripts`)
}

// The server runs a slide's script in CErXes (cerxescheck.go ScriptRun) to
// where it ends: get_display_list says that end, render_strip draws it over
// time, and a script that does not run is said on create.
func TestSlideScriptRunsOnServer(t *testing.T) {
	if _, err := os.Stat("../web/dist/cerxes.wasm"); err != nil {
		t.Skip("no web/dist/cerxes.wasm (npm run build with Rust's wasm32-wasip1 target)")
	}
	t.Setenv("SLIQTLY_CERXES_DIR", "../web/dist")
	cerxes = cerxesEngine{}
	defer func() { cerxes = cerxesEngine{} }()
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "# Deck\n\n## Sales grow {script=apps/fx.tsx export-frame=2s}\n\n- Saturday record\n- Buns sell\n"
	src := "let t = 0;\nfunction tick(dt) { t += dt; find(\"li:2\").set({ opacity: Math.max(0, 1 - t) }); }\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Deck", "markdown": md,
		"files": []any{map[string]any{"name": "fx.tsx", "text": src}}})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	if w := fmt.Sprint(sc(c)["warnings"]); strings.Contains(w, "does not run") {
		t.Fatal(w)
	}
	id := sc(c)["deck_id"].(string)
	d := call(t, s, "get_display_list", map[string]any{"deck_id": id, "slide": 2})
	match(t, textOf(d), `Where the slide's script ends .*"li-2":\{"opacity":0\}`)

	st := call(t, s, "render_strip", map[string]any{"deck_id": id, "slide": 2, "frames": 3})
	if st.IsError {
		t.Fatal(textOf(st))
	}
	var text string
	for _, ct := range st.Content {
		if tc, ok := ct.(*mcp.TextContent); ok {
			text = tc.Text
		}
	}
	match(t, text, `at 3 moments.*1: 0 s, 2: 1 s, 3: 2 s`)
	match(t, text, `Its script apps/fx\.tsx run to each moment`)
	if _, ok := st.Content[0].(*mcp.ImageContent); !ok {
		t.Fatal("no picture")
	}

	bad := call(t, s, "create_presentation", map[string]any{"title": "Deck", "markdown": md,
		"files": []any{map[string]any{"name": "fx.tsx", "text": "function tick( {"}}})
	match(t, fmt.Sprint(sc(bad)["warnings"]), `Slide 2: script apps/fx\.tsx does not run: .*→ topic=scripts`)
}
