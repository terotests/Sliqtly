// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strings"
	"testing"
)

// a photo-like picture: near-white sky over the top half, dark trees below
func skyPNG(top, bottom color.RGBA) string {
	img := image.NewRGBA(image.Rect(0, 0, 160, 90))
	for y := 0; y < 90; y++ {
		for x := 0; x < 160; x++ {
			c := bottom
			if y < 45 {
				c = top
			}
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestWarnsOfTextThatDoesNotStandOut(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	light := skyPNG(color.RGBA{244, 246, 248, 255}, color.RGBA{230, 235, 240, 255})
	dark := skyPNG(color.RGBA{20, 30, 40, 255}, color.RGBA{28, 58, 36, 255})
	md := "# Deck\n\n## Cloudy {bg=media/sky.png}\n\nThe theme's light text over a white sky.\n\n" +
		"## Night {bg=media/night.png}\n\nLight text over a dark picture.\n\n## Plain\n\nNo picture at all.\n"
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Sky", "markdown": md,
		"images": []any{
			map[string]any{"name": "sky.png", "data_base64": light},
			map[string]any{"name": "night.png", "data_base64": dark},
		},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	out := sc(c)
	ws := fmt.Sprint(out["warnings"])
	t.Log(ws)
	match(t, ws, `Slide "Cloudy": text is hard to read over the background picture: "Cloudy" 1\.\d:1 \(needs 3\.0:1\)`)
	match(t, ws, `"The theme's light text over a white sky\." 1\.\d:1 \(needs 3\.0:1\)`)
	// aurora's slide is dark navy: dimming the picture towards it is the fix
	match(t, ws, `Fix: a stronger dim, bg-dim=0\.\d+ in the slide's heading attributes, or a text colour such as #[0-9a-f]{6} in css\.`)
	for _, ok := range []string{`Slide "Night"`, `Slide "Plain"`} {
		if strings.Contains(ws, ok) {
			t.Fatalf("%s reads and was warned of: %s", ok, ws)
		}
	}

	// the suggested dim is enough
	dim := regexp1(t, ws, `bg-dim=(0\.\d+)`)
	u := call(t, s, "update_presentation", map[string]any{"deck_id": out["deck_id"], "edit_key": out["edit_key"],
		"markdown": strings.Replace(md, "{bg=media/sky.png}", "{bg=media/sky.png bg-dim="+dim+"}", 1)})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	if uw := fmt.Sprint(sc(u)["warnings"]); strings.Contains(uw, "hard to read") {
		t.Fatalf("bg-dim=%s did not fix it: %s", dim, uw)
	}

	// an update reads the stored picture back for the check
	u2 := call(t, s, "update_presentation", map[string]any{"deck_id": out["deck_id"], "edit_key": out["edit_key"], "markdown": md})
	match(t, fmt.Sprint(sc(u2)["warnings"]), `Slide "Cloudy": text is hard to read over the background picture`)
}

func regexp1(t *testing.T, s, re string) string {
	t.Helper()
	m := regexp.MustCompile(re).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("%q not in %s", re, s)
	}
	return m[1]
}
