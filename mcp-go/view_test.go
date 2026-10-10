// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// --- GET /api/view/<id>: the public viewer's slides as display lists (rgr/View.rgr)

type viewAnswer struct {
	Deck struct {
		Name   string  `json:"name"`
		Width  float64 `json:"width"`
		Height float64 `json:"height"`
		Slides int     `json:"slides"`
		Files  []struct {
			Path, Type, URL string
		} `json:"files"`
	} `json:"deck"`
	Lists []struct {
		Cmds []struct {
			K   int    `json:"k"`
			Src string `json:"src"`
		} `json:"cmds"`
	} `json:"lists"`
}

func getView(t *testing.T, url string) (int, http.Header, viewAnswer, string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	var v viewAnswer
	if res.StatusCode == 200 {
		if err := json.Unmarshal(body, &v); err != nil {
			t.Fatalf("%v: %.200s", err, body)
		}
	}
	return res.StatusCode, res.Header, v, string(body)
}

func TestViewLists(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := fmt.Sprintf(testDeck, twentyMonths()) + "\n## Picture\n\n![A dot](media/dot.png)\n"
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Viewed", "markdown": md, "visibility": "link",
		"files":  []any{map[string]any{"name": "sales.csv", "text": "month,sales\nJan,10\nFeb,14\nMar,9\n"}},
		"images": []any{map[string]any{"name": "dot.png", "data_base64": base64.StdEncoding.EncodeToString(squarePNG())}},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	n := int(sc(c)["slides"].(float64))

	code, h, v, _ := getView(t, s.root+"/api/view/"+id)
	eq(t, code, 200)
	eq(t, h.Get("Cache-Control"), "public, max-age=30, s-maxage=60")
	eq(t, v.Deck.Name, "Viewed")
	eq(t, v.Deck.Slides, n)
	eq(t, len(v.Lists), n)
	if v.Deck.Width <= 0 || v.Deck.Height <= 0 {
		t.Fatalf("page %v×%v", v.Deck.Width, v.Deck.Height)
	}
	// the picture a slide draws comes with its address; the CSV a chart read
	// was read here and is not sent
	eq(t, len(v.Deck.Files), 1)
	eq(t, v.Deck.Files[0].Path, "media/dot.png")
	if !strings.HasPrefix(v.Deck.Files[0].URL, "https://") {
		t.Fatalf("file url %q", v.Deck.Files[0].URL)
	}
	drawn := false
	for _, l := range v.Lists {
		for _, cmd := range l.Cmds {
			if cmd.Src == "/media/dot.png" {
				drawn = true
			}
		}
	}
	if !drawn {
		t.Fatal("no slide draws /media/dot.png")
	}

	// a view of some sections only (?slides=, the picked slides' link)
	code, _, one, _ := getView(t, s.root+"/api/view/"+id+"?slides=picture")
	eq(t, code, 200)
	eq(t, one.Deck.Slides, 1)
	eq(t, len(one.Lists), 1)
	code, _, gone, _ := getView(t, s.root+"/api/view/"+id+"?slides=nothing-like-it")
	eq(t, code, 200)
	eq(t, gone.Deck.Slides, 1)

	// a private deck is not shown: the viewer has no sign-in
	ctx := context.Background()
	if err := f.db.Update(ctx, "shares", id, Doc{"visibility": "private"}); err != nil {
		t.Fatal(err)
	}
	code, h, _, _ = getView(t, s.root+"/api/view/"+id)
	eq(t, code, 404)
	eq(t, h.Get("Cache-Control"), "no-store")
	// …but to its owner signed in on the page (a Firebase ID token), never cached
	code, h, mine := getAs(t, s.root+"/api/view/"+id, "google-ok")
	eq(t, code, 200)
	eq(t, h.Get("Cache-Control"), "private, no-store")
	if !strings.Contains(string(mine), `"name":"Viewed"`) {
		t.Fatalf("owner's view: %.200s", mine)
	}
	code, _, _ = getAs(t, s.root+"/api/view/"+id, "forged")
	eq(t, code, 404)

	// the owner's viewing link (links/{id}): the private deck to anyone with
	// it, its files from the deck's own place; a link to someone else's deck
	// shows nothing
	d, _ := f.db.Get(ctx, "shares", id)
	f.db.Set(ctx, "links", "LinkToIt01", Doc{"of": id, "owner": d["owner"]})
	f.db.Set(ctx, "links", "NotTheirs1", Doc{"of": id, "owner": "someone-else"})
	code, _, lv, _ := getView(t, s.root+"/api/view/LinkToIt01")
	eq(t, []any{code, lv.Deck.Name, lv.Deck.Slides}, []any{200, "Viewed", n})
	code, _, _, _ = getView(t, s.root+"/api/view/NotTheirs1")
	eq(t, code, 404)
	res, err := http.Get(s.root + "/api/export/LinkToIt01/md")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	eq(t, res.StatusCode, 200)

	// no such presentation, and an id that is not one
	for _, bad := range []string{"/api/view/AbCdEf1234", "/api/view/../x", "/api/view/", "/api/view/a"} {
		code, h, _, body := getView(t, s.root+bad)
		eq(t, code, 404, bad)
		eq(t, h.Get("Cache-Control"), "no-store", bad)
		if !strings.Contains(body, "not found") {
			t.Fatalf("%s: %s", bad, body)
		}
	}
}

// `mode: book`: the viewer is sent which pages face each other and how the
// book is drawn; a deck of slides is sent no book (PresBook, web/book.js)
func TestViewBook(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	pages := "# Kansi\n\nA\n\n# Kaksi\n\nB\n\n# Kolme\n\nC\n\n# Neljä\n\nD\n"
	open := func(front string) map[string]any {
		c := call(t, s, "create_presentation", map[string]any{"title": "Kirja", "markdown": front + pages, "visibility": "link"})
		if c.IsError {
			t.Fatal(textOf(c))
		}
		code, _, _, body := getView(t, s.root+"/api/view/"+sc(c)["deck_id"].(string))
		eq(t, code, 200)
		var raw struct {
			Deck map[string]any `json:"deck"`
		}
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatal(err)
		}
		return raw.Deck
	}
	d := open("---\nmode: book\nrender: realistic\n---\n\n")
	b, ok := d["book"].(map[string]any)
	if !ok {
		t.Fatalf("no book: %v", d)
	}
	eq(t, b["render"], "realistic")
	got, _ := json.Marshal(b["spreads"])
	eq(t, string(got), "[[-1,0],[1,2],[3,-1]]")
	if _, has := open("")["book"]; has {
		t.Fatal("a deck of slides has no book")
	}
}

// `select-text: off` in the front matter: the page lets nobody select the
// slides' text (web/slidetext.js); by default the view says nothing of it.
func TestViewSelectText(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	open := func(front string) map[string]any {
		c := call(t, s, "create_presentation", map[string]any{"title": "Teksti", "markdown": front + "# Otsikko\n\nTeksti\n", "visibility": "link"})
		if c.IsError {
			t.Fatal(textOf(c))
		}
		code, _, _, body := getView(t, s.root+"/api/view/"+sc(c)["deck_id"].(string))
		eq(t, code, 200)
		var raw struct {
			Deck map[string]any `json:"deck"`
		}
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatal(err)
		}
		return raw.Deck
	}
	if v, has := open("")["selectText"]; has {
		t.Fatalf("selectText %v without select-text in the front matter", v)
	}
	eq(t, open("---\nselect-text: off\n---\n\n")["selectText"], false)
}

// A program on a slide (```app) comes with the view: the page runs it. Its
// box is the program's own shape, centred in the column (not stretched to
// the column's right edge).
func TestViewPlays(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "# Title\n\n## Video\n\n```app\nsrc: apps/v.tsx\nsize: 960x540\nallow: slide.nav\n```\n"
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Played", "markdown": md, "visibility": "link",
		"files": []any{
			map[string]any{"name": "v.tsx", "text": "function view() { return <div className=\"r\"/> }"},
			map[string]any{"name": "v.tsx.css", "text": ".r { background: red; }"},
		},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	res, err := http.Get(s.root + "/api/view/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var v struct {
		Deck struct {
			Width float64 `json:"width"`
			Plays []struct {
				Key, Src, CSS, Text, CSSText string
				Slide                        int
				W, H                         float64
				Allow                        []string
				Box                          []float64
			} `json:"plays"`
		} `json:"deck"`
	}
	if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	eq(t, len(v.Deck.Plays), 1)
	p := v.Deck.Plays[0]
	eq(t, p.Key, "apps/v.tsx#1")
	eq(t, p.Slide, 1)
	eq(t, p.W, 960.0)
	eq(t, fmt.Sprint(p.Allow), "[slide.nav]")
	eq(t, p.Text, "function view() { return <div className=\"r\"/> }")
	eq(t, p.CSSText, ".r { background: red; }")
	eq(t, len(p.Box), 4)
	// the box is left empty in the slide's list: the page paints the plate
	// and runs the program over it
	_, _, _, body := getView(t, s.root+"/api/view/"+id)
	if strings.Contains(body, "▶ v.tsx") {
		t.Fatal("the view's list draws the plate")
	}

	// the plate in the slide's list: as wide as 16:9 at the box's height
	r := call(t, s, "render_slide", map[string]any{"deck_id": id, "slide": 2})
	m := regexp.MustCompile(`diagram \(App\) at (\d+),\d+ size (\d+)×(\d+)`).FindStringSubmatch(lastText(r))
	if m == nil {
		t.Fatal(lastText(r))
	}
	w, h := atof(m[2]), atof(m[3])
	if d := w/h - 16.0/9.0; d > 0.01 || d < -0.01 {
		t.Fatalf("plate %v×%v is not 16:9", w, h)
	}
	if left, right := atof(m[1]), 1920-atof(m[1])-w; left-right > 4 || right-left > 4 {
		t.Fatalf("plate not centred: %v left, %v right", left, right)
	}
}

func atof(s string) float64 {
	var f float64
	fmt.Sscan(s, &f)
	return f
}

// A deck's own effect (```fx, src/FxLang.rgr): the viewer is sent the shader
// the deck's compiler wrote, and a block that does not compile is a warning
// with its line, not an effect
func TestViewDeckEffects(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "# Effects\n\n```fx\neffect embers source {\n  param heat = 0.6 [0, 1]\n  output = rgba(#ff3d00, heat)\n}\n```\n\n## Hot {fx=embers}\n\nText\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Fx", "markdown": md, "visibility": "link"})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	code, _, _, body := getView(t, s.root+"/api/view/"+id)
	eq(t, code, 200)
	var v struct {
		Effects []struct {
			Name, Layer, Frag string
			Params            map[string]float64
		} `json:"effects"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	eq(t, len(v.Effects), 1)
	eq(t, v.Effects[0].Name, "embers")
	eq(t, v.Effects[0].Layer, "source")
	eq(t, v.Effects[0].Params["heat"], 0.6)
	if !strings.Contains(v.Effects[0].Frag, "vec4 fxColor(vec2 p, vec2 local)") || !strings.Contains(v.Effects[0].Frag, "clamp(p_heat, 0.0, 1.0)") {
		t.Fatalf("frag: %s", v.Effects[0].Frag)
	}
	if strings.Contains(body, "effect embers") {
		t.Fatal("the block's text is on a slide")
	}

	bad := call(t, s, "create_presentation", map[string]any{"title": "Bad", "markdown": "# A\n\n```fx\neffect x source {\n  output = nope\n}\n```\n"})
	if bad.IsError {
		t.Fatal(textOf(bad))
	}
	if !strings.Contains(textOf(bad), "line 5: 'nope' has no value here") {
		t.Fatalf("no warning: %s", textOf(bad))
	}
}
