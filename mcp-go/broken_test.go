// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
)

// --- a broken or missing file says why, not an empty box

func TestBrokenPictureIsRefused(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	png := squarePNG()
	for name, b64 := range map[string]string{
		"cut.png":  base64.StdEncoding.EncodeToString(png[:len(png)/2]),
		"junk.jpg": base64.StdEncoding.EncodeToString([]byte("hello world")),
	} {
		c := call(t, s, "create_presentation", map[string]any{"title": "B", "markdown": "# B\n\n![x](media/" + name + ")\n",
			"images": []any{map[string]any{"name": name, "data_base64": b64}}})
		match(t, textOf(c), `^Image `+strings.ReplaceAll(name, ".", `\.`)+` is not a picture Sliqtly can read \(.+\)\. Long base64 is easily corrupted`)
	}
	ok := call(t, s, "create_presentation", map[string]any{"title": "B", "markdown": "# B\n\n![x](media/ok.png)\n",
		"images": []any{map[string]any{"name": "ok.png", "data_base64": base64.StdEncoding.EncodeToString(png)}}})
	if ok.IsError {
		t.Fatal(textOf(ok))
	}
}

func TestMissingDataFileIsNamed(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "# B\n\n## Chart\n\n```vega-lite\n{\"data\":{\"url\":\"data/none.csv\"},\"mark\":\"bar\",\"encoding\":{\"x\":{\"field\":\"a\",\"type\":\"nominal\"},\"y\":{\"field\":\"b\",\"type\":\"quantitative\"}}}\n```\n\n## Table\n\n```table\ndata/gone.csv\nrows: 5\n```\n\n## Here\n\n```table\ndata/here.csv\n```\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "B", "markdown": md,
		"files": []any{map[string]any{"name": "here.csv", "text": "a,b\nx,1\n"}}})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	var gone []string
	for _, w := range list(sc(c)["warnings"]) {
		if strings.Contains(w.(string), "has no such file") {
			gone = append(gone, w.(string))
		}
	}
	eq(t, len(gone), 2, strings.Join(gone, "\n"))
	match(t, gone[0]+gone[1], `data/none\.csv is read by a chart or table but the presentation has no such file`)
	match(t, gone[0]+gone[1], `data/gone\.csv is read`)
}

// A picture nobody sent is named once; the "[alt]" standing in for it is no
// contrast warning of its own.
func TestMissingPictureStandIn(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	c := call(t, s, "create_presentation", map[string]any{"title": "B", "markdown": "# B\n\n## Gone\n\n![g](media/gone.png)\n"})
	ws := fmt.Sprint(sc(c)["warnings"])
	match(t, ws, `media/gone\.png is used in the Markdown but no image by that name was sent`)
	if strings.Contains(ws, "hard to read") {
		t.Fatal(ws)
	}
}

// A program for an app block is kept under apps/ with its stylesheet; one
// the block names and nobody sent is said, its optional stylesheet is not.
func TestAppProgramFiles(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "# B\n\n## Play\n\n```app\nsrc: apps/game.tsx\nsize: 480x270\nallow: deck.data\n```\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "B", "markdown": md,
		"files": []any{
			map[string]any{"name": "game.tsx", "text": "function view() { return <div className=\"b\" /> }\n"},
			map[string]any{"name": "apps/game.tsx.css", "text": ".b { width: 480px; height: 270px }\n"},
		}})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	ws := fmt.Sprint(sc(c)["warnings"])
	if strings.Contains(ws, "no such file") || strings.Contains(ws, "app:") {
		t.Fatal(ws)
	}
	id := sc(c)["deck_id"].(string)
	files := textOf(call(t, s, "list_files", map[string]any{"deck_id": id}))
	match(t, files, `apps/game\.tsx`)
	match(t, files, `apps/game\.tsx\.css`)

	gone := call(t, s, "create_presentation", map[string]any{"title": "C", "markdown": "# C\n\n## Play\n\n```app\nsrc: apps/none.tsx\ncolour: red\n```\n"})
	gw := fmt.Sprint(sc(gone)["warnings"])
	match(t, gw, `apps/none\.tsx is the program of an app block but the presentation has no such file`)
	match(t, gw, `unknown key "colour".*→ topic=apps`)
	if strings.Contains(gw, "none.tsx.css") {
		t.Fatal("the optional stylesheet was reported:", gw)
	}

	b64 := call(t, s, "create_presentation", map[string]any{"title": "D", "markdown": md,
		"files": []any{map[string]any{"name": "game.tsx", "data_base64": "aGk="}}})
	match(t, textOf(b64), `a program and its stylesheet are sent as text`)
}

// A program that does not run is said in the report (cerxescheck.go): its
// syntax error, and what its first frames threw; one that runs is not.
// Tried in the engine of the built page (npm run build: web/dist).
func TestAppProgramRuns(t *testing.T) {
	if _, err := os.Stat("../web/dist/cerxes.wasm"); err != nil {
		t.Skip("no web/dist/cerxes.wasm (npm run build with Rust's wasm32-wasip1 target)")
	}
	t.Setenv("SLIQTLY_CERXES_DIR", "../web/dist")
	cerxes = cerxesEngine{}
	defer func() { cerxes = cerxesEngine{} }()
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "# B\n\n## Play\n\n```app\nsrc: apps/game.tsx\nsize: 480x270\n```\n"
	try := func(src string) string {
		c := call(t, s, "create_presentation", map[string]any{"title": "B", "markdown": md,
			"files": []any{map[string]any{"name": "game.tsx", "text": src}}})
		if c.IsError {
			t.Fatal(textOf(c))
		}
		return fmt.Sprint(sc(c)["warnings"])
	}
	// spread, a default parameter, forEach into a JSX list, String.repeat
	ok := try("function box(l: number, extra: any = {}) { return { left: l, ...extra }; }\n" +
		"function view() {\n  const rows: any[] = [];\n  [1, 2].forEach((n) => rows.push(<span style={box(n, { opacity: 1 })}>{\"x\".repeat(n)}</span>));\n" +
		"  return <div className=\"b\">{rows}</div>;\n}\n")
	if strings.Contains(ok, "does not run") {
		t.Fatal(ok)
	}

	// the deck's files it imports: JSON parsed, CSV rows, any other file as text
	withFiles := func(src string, more ...map[string]any) string {
		files := []any{map[string]any{"name": "game.tsx", "text": src}}
		for _, m := range more {
			files = append(files, m)
		}
		c := call(t, s, "create_presentation", map[string]any{"title": "B", "markdown": md, "files": files})
		if c.IsError {
			t.Fatal(textOf(c))
		}
		return fmt.Sprint(sc(c)["warnings"])
	}
	reads := withFiles("import cfg from \"../data/world.json\";\nimport rows, { text } from \"/data/sales.csv\";\nimport notes from \"data/notes.txt\";\n"+
		"function view() {\n  if (cfg.speed !== 2 || rows.length !== 2 || rows[1].count !== 7 || rows[0].name !== \"Ada, Jr\" || notes !== \"hei\\n\" || text.indexOf(\"count\") < 0) throw new Error(\"read wrong: \" + JSON.stringify(rows));\n"+
		"  return <div className=\"b\">{cfg.title}</div>;\n}\n",
		map[string]any{"name": "data/world.json", "text": "{\"speed\": 2, \"title\": \"Maailma\"}"},
		map[string]any{"name": "data/sales.csv", "text": "name,count\n\"Ada, Jr\",3\nBo,7\n"},
		map[string]any{"name": "data/notes.txt", "text": "hei\n"})
	if strings.Contains(reads, "does not run") || strings.Contains(reads, "imports") {
		t.Fatal(reads)
	}
	gone := withFiles("import cfg from \"../data/none.json\";\nfunction view() { return <div>{cfg.title}</div>; }\n")
	match(t, gone, `apps/game\.tsx imports data/none\.json, which the presentation does not have`)
	match(t, gone, `apps/game\.tsx does not run: .*data/none\.json is not a file of the presentation`)
	bad := withFiles("import cfg from \"../data/w.json\";\nfunction view() { return <div>{cfg.title}</div>; }\n",
		map[string]any{"name": "data/w.json", "text": "{nope"})
	match(t, bad, `apps/game\.tsx does not run: .*data/w\.json is not JSON`)
	// a library programs share (apps/scene.ts), with its own import from
	// its folder and the "Sliqtly" helpers; the program's own names stay
	shared := withFiles("import { label, W } from \"./scene.tsx\";\nfunction lerp(a, b, k) { return 0; }\nconst orbit = 1;\n"+
		"function view() {\n  if (W !== 560) throw new Error(\"W \" + W);\n  return <div className=\"b\">{label([0, 0, 0], \"x\")}</div>;\n}\n",
		map[string]any{"name": "scene.tsx", "text": "import { camera } from \"Sliqtly\";\nimport { W, H } from \"./size.ts\";\nexport { W };\n" +
			"const cam = camera({ position: [0, 2, 8], width: W, height: H });\nexport function label(p: number[], t: string) { const s = cam.project(p); return <span style={{ left: s[0], top: s[1] }}>{t}</span>; }\n"},
		map[string]any{"name": "apps/size.ts", "text": "export const W: number = 560, H: number = 420;\n"})
	if strings.Contains(shared, "does not run") || strings.Contains(shared, "imports") || strings.Contains(shared, "has no such file") {
		t.Fatal(shared)
	}
	noLib := withFiles("import { label } from \"./scene.tsx\";\nfunction view() { return <div>{label([0, 0, 0], \"x\")}</div>; }\n")
	match(t, noLib, `apps/game\.tsx imports apps/scene\.tsx, which the presentation does not have`)
	if strings.Contains(noLib, "program of an app block") {
		t.Fatal("a missing library was taken for a program:", noLib)
	}
	badLib := withFiles("import { a } from \"./lib.ts\";\nfunction view() { return <div>{a}</div>; }\n",
		map[string]any{"name": "lib.ts", "text": "export const b = 1;\nexport const = 2;\n"})
	match(t, badLib, `apps/game\.tsx does not run: .*SyntaxError: .*\(apps/lib\.ts line 2\)`)
	// a demo on its own clock is told how its slide is seen through on a
	// video (presentation.video); one that says it, or has no tick, is not
	demo := try("let t = 0;\nfunction tick(dt) { t += dt; }\nfunction view() { return <div className=\"b\" />; }\n")
	match(t, demo, `Slide 2: apps/game\.tsx runs on its own clock \(tick\).*presentation\.video\(\{ seconds \}\).*→ topic=apps`)
	said := try("import { presentation } from \"Sliqtly\";\npresentation.video({ seconds: 25 });\nlet t = 0;\nfunction tick(dt) { t += dt; }\nfunction view() { return <div className=\"b\" />; }\n")
	if strings.Contains(said, "own clock") || strings.Contains(said, "does not run") {
		t.Fatal(said)
	}
	if strings.Contains(ok, "own clock") {
		t.Fatal("a program without tick was told:", ok)
	}
	if got := videoAsked(`[{"k":"deck.set"},{"k":"video","seconds":12.5}]`); got != 12.5 {
		t.Fatal(got)
	}
	match(t, try("function view() { return <div> }\n"), `Slide 2: apps/game\.tsx does not run: .+→ topic=apps`)
	match(t, try("function view() { return nothing.here; }\n"), `apps/game\.tsx does not run: .*nothing`)

	// a rule of its stylesheet the layout does not take is said too
	c := call(t, s, "create_presentation", map[string]any{"title": "B", "markdown": md,
		"files": []any{map[string]any{"name": "game.tsx", "text": "function view() { return <div className=\"b\" /> }\n"},
			map[string]any{"name": "game.tsx.css", "text": ".b.c { color: red }\ndiv > .b { color: blue }\n"}}})
	cw := fmt.Sprint(sc(c)["warnings"])
	match(t, cw, `apps/game\.tsx\.css: Unsupported selector.*div > \.b.*→ topic=apps`)
	if strings.Contains(cw, ".b.c") {
		t.Fatal("classes written together were reported:", cw)
	}
}
