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
