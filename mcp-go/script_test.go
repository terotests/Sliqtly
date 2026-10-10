package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
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

	// rounded columns (cornerRadiusEnd) are bars too
	round := "# Deck\n\n## Cups {script=apps/fx.tsx}\n\n```vega-lite\n" +
		`{"data":{"values":[{"d":"Mo","n":4},{"d":"Tu","n":7},{"d":"We","n":5}]},"mark":{"type":"bar","cornerRadiusEnd":4},` +
		`"encoding":{"x":{"field":"d","type":"ordinal"},"y":{"field":"n","type":"quantitative"}}}` + "\n```\n"
	rc := call(t, s, "create_presentation", map[string]any{"title": "Deck", "markdown": round,
		"files": []any{map[string]any{"name": "fx.tsx", "text": "function tick(dt) { find(\"chart:1 bar\").set({ scaleY: 0.5 }); }\n"}}})
	if w := fmt.Sprint(sc(rc)["warnings"]); strings.Contains(w, "bar") {
		t.Fatal("rounded bars not found:", w)
	}
	rd := call(t, s, "get_display_list", map[string]any{"deck_id": sc(rc)["deck_id"].(string), "slide": 2, "selector": "chart:1 bar"})
	match(t, textOf(rd), `find\("chart:1 bar"\) → 3 entities`)
	// a column's data is its category and its value from the chart's data
	match(t, fmt.Sprint(sc(rd)["entities"]), `label:Tu value:7`)

	// a > quote is a quote
	qc := call(t, s, "create_presentation", map[string]any{"title": "Deck", "markdown": "# Deck\n\n## Q {script=apps/fx.tsx}\n\n> Bread first\n",
		"files": []any{map[string]any{"name": "fx.tsx", "text": "function tick(dt) { find(\"quote word\").set({ opacity: 0.5 }); }\n"}}})
	if w := fmt.Sprint(sc(qc)["warnings"]); strings.Contains(w, "quote") {
		t.Fatal("the quote's words not found:", w)
	}
	qd := call(t, s, "get_display_list", map[string]any{"deck_id": sc(qc)["deck_id"].(string), "slide": 2})
	match(t, textOf(qd), `quote-1 quote "Bread first"`)

	// the module form: its selectors are checked the same way
	mod := "import { presentation } from \"Sliqtly\";\nconst slide = presentation.activeSlide;\nexport function tick() { slide.find(\"li:2\").set({ opacity: 0.5 }); slide.find(\"li:7\").set({ opacity: 0 }); }\n"
	mc := call(t, s, "create_presentation", map[string]any{"title": "Deck", "markdown": md,
		"files": []any{map[string]any{"name": "fx.tsx", "text": mod}}})
	mw := fmt.Sprint(sc(mc)["warnings"])
	match(t, mw, `find\("li:7"\) → 0 entities`)
	if strings.Contains(mw, `find("li:2")`) {
		t.Fatal("a selector that finds something was reported:", mw)
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

	// the module form runs the same: import { presentation } from "Sliqtly"
	// (its own `const slide` does not take the runtime's `slide` from it)
	mod := "import { presentation, env } from \"Sliqtly\";\nconst slide = presentation.activeSlide;\nconst items = slide.find(\"li\");\nlet t = 0;\n" +
		"export function tick(dt: number) { t += dt; items.first()?.set({ opacity: env.export && slide.index > 0 && slide.step >= 0 ? 0.25 : 1 }); }\n"
	mc := call(t, s, "create_presentation", map[string]any{"title": "Deck", "markdown": md,
		"files": []any{map[string]any{"name": "fx.tsx", "text": mod}}})
	if w := fmt.Sprint(sc(mc)["warnings"]); strings.Contains(w, "does not run") {
		t.Fatal(w)
	}
	md2 := call(t, s, "get_display_list", map[string]any{"deck_id": sc(mc)["deck_id"].(string), "slide": 2})
	match(t, textOf(md2), `Where the slide's script ends .*"li-1":\{"opacity":0\.25\}`)
	// shapes added off the slide (a script that took it for 1920 × 1080) are said
	far := "import { presentation } from \"Sliqtly\";\nconst s = presentation.activeSlide;\ns.add(\"rect\", { x: 1440, y: 40, w: 400, h: 20 });\ns.add(\"rect\", { x: s.width - 50, y: 10, w: 40, h: 20 });\nexport function tick() {}\n"
	fc := call(t, s, "create_presentation", map[string]any{"title": "Deck", "markdown": md,
		"files": []any{map[string]any{"name": "fx.tsx", "text": far}}})
	match(t, fmt.Sprint(sc(fc)["warnings"]), `Slide 2: script apps/fx\.tsx: 1 shape\(s\) it adds are off the slide, which is 960 × 540`)

	// the viewer gets how each script opens (start(), onEnter, its build
	// steps), so a reloaded page paints the slide so from its first frame
	op := "import { presentation } from \"Sliqtly\";\nconst s = presentation.activeSlide;\nexport function start() { s.find(\"li\").set({ opacity: 0 }); }\nexport function tick() { s.find(\"li\").set({ opacity: 1 }); }\nexport function final() { s.find(\"li\").set({ opacity: 1 }); }\n"
	oc := call(t, s, "create_presentation", map[string]any{"title": "Deck", "markdown": md, "visibility": "link",
		"files": []any{map[string]any{"name": "fx.tsx", "text": op}}})
	_, _, _, ob := getView(t, s.root+"/api/view/"+sc(oc)["deck_id"].(string))
	var ov struct {
		Scripts []struct {
			Open map[string]any `json:"open"`
		} `json:"scripts"`
	}
	if err := json.Unmarshal([]byte(ob), &ov); err != nil || len(ov.Scripts) != 1 {
		t.Fatalf("scripts: %v %.300s", err, ob)
	}
	match(t, fmt.Sprint(ov.Scripts[0].Open["p"]), `li-1:map\[opacity:0\]`)

	// a picture only a script names (add("image")) is the deck's too: drawn
	// where the script ends, and handed to the viewer
	pic := "import { presentation } from \"Sliqtly\";\npresentation.activeSlide.add(\"image\", { src: \"media/dot.png\", x: 20, y: 20, w: 40, h: 40 });\nexport function tick() {}\n"
	pc := call(t, s, "create_presentation", map[string]any{"title": "Deck", "markdown": md, "visibility": "link",
		"files":  []any{map[string]any{"name": "fx.tsx", "text": pic}},
		"images": []any{map[string]any{"name": "dot.png", "data_base64": base64.StdEncoding.EncodeToString(squarePNG())}}})
	if pc.IsError {
		t.Fatal(textOf(pc))
	}
	_, _, pv, _ := getView(t, s.root+"/api/view/"+sc(pc)["deck_id"].(string))
	drawn := false
	for _, c := range pv.Lists[1].Cmds {
		if c.K == 2 && c.Src == "/media/dot.png" {
			drawn = true
		}
	}
	handed := false
	for _, f := range pv.Deck.Files {
		if f.Path == "media/dot.png" {
			handed = true
		}
	}
	if !drawn || !handed {
		t.Fatalf("the script's picture: drawn %v, handed to the viewer %v", drawn, handed)
	}

	// a module the page does not have is said
	nomod := call(t, s, "create_presentation", map[string]any{"title": "Deck", "markdown": md,
		"files": []any{map[string]any{"name": "fx.tsx", "text": "import { x } from \"nope\";\nfunction tick() {}\n"}}})
	match(t, fmt.Sprint(sc(nomod)["warnings"]), `does not run: .*Cannot find module 'nope' \(modules here: Sliqtly[,)]`)

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

	// the public viewer: the slide at rest where the script ends (li-2
	// gone), and the script with the slide as the Markdown has it to run
	code, _, v, body := getView(t, s.root+"/api/view/"+id)
	if code != 200 {
		t.Fatalf("view: %d", code)
	}
	var got struct {
		Scripts []struct {
			Key   string         `json:"key"`
			Slide int            `json:"slide"`
			Text  string         `json:"text"`
			Tree  []any          `json:"tree"`
			Base  map[string]any `json:"base"`
		} `json:"scripts"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Scripts) != 1 || got.Scripts[0].Key != "script:apps/fx.tsx#1" || got.Scripts[0].Slide != 1 || got.Scripts[0].Text != src || len(got.Scripts[0].Tree) < 3 {
		t.Fatalf("view scripts: %.400s", body)
	}
	for _, k := range []string{"list", "scene", "ink", "accent"} {
		if _, ok := got.Scripts[0].Base[k]; !ok {
			t.Fatalf("view script base has no %s", k)
		}
	}
	var raw struct {
		Lists []json.RawMessage `json:"lists"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil || len(raw.Lists) != v.Deck.Slides {
		t.Fatalf("view lists: %v", err)
	}
	gone := regexp.MustCompile(`"c":\[\d+,\d+,\d+,0(\.0+)?\],"text":"Buns sell"`)
	if !gone.Match(raw.Lists[1]) {
		t.Fatalf("the slide at rest is not where its script ends: %.600s", raw.Lists[1])
	}
	base, _ := json.Marshal(got.Scripts[0].Base["list"])
	if gone.Match(base) || !strings.Contains(string(base), `"Buns sell"`) {
		t.Fatalf("the script's base is not the slide as the Markdown has it: %.600s", base)
	}

	// scripts/check-view.mjs's deck: a heading the script opens blue and
	// turns red while it runs (final() leaves it as the Markdown has it)
	if os.Getenv("SLIQTLY_WRITE_FIXTURES") != "" {
		red := call(t, s, "create_presentation", map[string]any{"title": "Script", "markdown": "## Turns red {script=apps/red.tsx}\n\n- One\n- Two\n",
			"files": []any{map[string]any{"name": "red.tsx", "text": "let t = 0;\nfunction start() { find(\"h2\").set({ color: \"#2020ff\" }); }\nfunction tick(dt) { t += dt; if (t > 1) find(\"h2\").set({ color: \"#ff2020\" }); }\nfunction final() { find(\"h2\").set({ color: null }); }\n"}}})
		_, _, _, rb := getView(t, s.root+"/api/view/"+sc(red)["deck_id"].(string))
		if err := os.WriteFile("../scripts/fixtures/view-script.json", []byte(rb), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	bad := call(t, s, "create_presentation", map[string]any{"title": "Deck", "markdown": md,
		"files": []any{map[string]any{"name": "fx.tsx", "text": "function tick( {"}}})
	match(t, fmt.Sprint(sc(bad)["warnings"]), `Slide 2: script apps/fx\.tsx does not run: .*→ topic=scripts`)
}
