// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
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
	match(t, ws, `"The theme’s light text over a white sky\." 1\.\d:1 \(needs 3\.0:1\)`)
	// aurora's slide is dark navy: dimming the picture towards it is the fix
	match(t, ws, `Fix: a stronger dim, bg-dim=0\.\d+ in the slide's heading attributes, or a text colour such as #[0-9a-f]{6} in css\.`)
	for _, ok := range []string{`Slide "Night"`, `Slide "Plain"`} {
		if strings.Contains(ws, ok) {
			t.Fatalf("%s reads and was warned of: %s", ok, ws)
		}
	}

	// the suggested dim is enough
	dim := regexp1(t, ws, `bg-dim=(0\.\d+)`)
	u := call(t, s, "update_presentation", map[string]any{"deck_id": out["deck_id"],
		"markdown": strings.Replace(md, "{bg=media/sky.png}", "{bg=media/sky.png bg-dim="+dim+"}", 1)})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	if uw := fmt.Sprint(sc(u)["warnings"]); strings.Contains(uw, "hard to read") {
		t.Fatalf("bg-dim=%s did not fix it: %s", dim, uw)
	}

	// an update reads the stored picture back for the check
	u2 := call(t, s, "update_presentation", map[string]any{"deck_id": out["deck_id"], "markdown": md})
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

// The SWOT layout's letter discs and titles read in the dark and light
// themes, and the contrast check says so: it judged a centred letter over
// its whole slot, half of it the dark card beside the disc (Tero's third
// test deck, 2026-10-04).
func TestSwotLettersAreNotContrastWarnings(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "# D\n\n## SWOT\n\n```swot\n- Vahvuudet: tekstipohjainen, sopii tekoälylle\n  - Nopea muokata\n- Heikkoudet: pieni käyttäjäkunta\n- Mahdollisuudet: yrityskäyttö, Confluence\n- Uhat: isot toimijat\n```\n"
	aurora, err := os.ReadFile("themes/aurora.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, css := range []string{
		string(aurora),
		string(aurora) + "\nfigure { colors: #e63946 #2a9d8f #e9c46a #8d5cf6; }\n",
		"page { background-color: #ffffff } document { color: #16202c }\nfigure { colors: #e63946 #2a9d8f #e9c46a #8d5cf6; }\n",
	} {
		c := call(t, s, "create_presentation", map[string]any{"title": "S", "markdown": md, "css": css})
		if c.IsError {
			t.Fatal(textOf(c))
		}
		if ws := fmt.Sprint(sc(c)["warnings"]); strings.Contains(ws, "hard to read") {
			t.Errorf("%s: %s", css[max(0, len(css)-60):], ws)
		}
	}
}

// a filled outline counts as the text's backdrop only where the text is
// inside it: dark text inside a pale triangle reads, the same text in the
// triangle's empty corner sits on the dark slide and is named
func TestPolygonBackdropFollowsTheOutline(t *testing.T) {
	list := CreateNew_EVGDisplayList()
	list.addRect(0, 0, 960, 540, EVGColor_static_rgb(16, 20, 40))
	list.addPolygon([]float64{100, 100, 700, 100, 100, 500}, EVGColor_static_rgb(240, 240, 235))
	ink := EVGColor_static_rgb(30, 30, 40)
	list.addText("Inside", 120, 120, 24, ink, "Inter", false, false, 120, 30)
	list.addText("Outside", 560, 440, 24, ink, "Inter", false, false, 120, 30)
	runs := Contrast_static_lowRuns(list, nil, "", 0, nil, nil, nil)
	low := map[string]bool{}
	for _, r := range runs {
		if r.low {
			low[r.text] = true
		}
	}
	if low["Inside"] {
		t.Errorf("text inside the pale outline was flagged: %v", low)
	}
	if !low["Outside"] {
		t.Errorf("text outside the outline on the dark slide was not flagged: %v", low)
	}
}
