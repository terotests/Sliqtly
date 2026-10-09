// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/base64"
	"fmt"
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
