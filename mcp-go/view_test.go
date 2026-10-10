// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand"
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
	// …made for the screen by this server (TestViewPictures)
	if !strings.HasPrefix(v.Deck.Files[0].URL, "/api/view/"+id+"/pic?path=media%2Fdot.png&v=") {
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

// the viewer's pictures: a deck anyone with the link sees gets its raster
// pictures from /api/view/<id>/pic, an opaque one as a JPEG and a large
// one scaled; the address carries the file's stamp, so the CDN keeps it
// for good. A private deck's page (its owner's) reads them from Storage.
func TestViewPictures(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	photo := func(w, h int, alpha bool) []byte {
		im := image.NewNRGBA(image.Rect(0, 0, w, h))
		rng := rand.New(rand.NewSource(1))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				a := uint8(255)
				if alpha && x < w/2 {
					a = 90
				}
				// a photograph's grain: what a PNG keeps every bit of
				n := uint8(rng.Intn(24))
				im.Set(x, y, color.NRGBA{uint8(x/12) + n, uint8(y/6) + n, 120 + n, a})
			}
		}
		var b bytes.Buffer
		png.Encode(&b, im)
		return b.Bytes()
	}
	big, clear := photo(2600, 1300, false), photo(2600, 1300, true)
	md := "# Pictures\n\n![Big](media/big.png)\n\n## Clear\n\n![Clear](media/clear.png)\n\n## Dot\n\n![Dot](media/dot.png)\n\n## Mark\n\n![Mark](media/mark.svg)\n"
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Pics", "markdown": md, "visibility": "link",
		"images": []any{
			map[string]any{"name": "big.png", "data_base64": base64.StdEncoding.EncodeToString(big)},
			map[string]any{"name": "clear.png", "data_base64": base64.StdEncoding.EncodeToString(clear)},
			map[string]any{"name": "dot.png", "data_base64": base64.StdEncoding.EncodeToString(squarePNG())},
			map[string]any{"name": "mark.svg", "data_base64": base64.StdEncoding.EncodeToString([]byte(markSVG))},
		},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	code, _, v, _ := getView(t, s.root+"/api/view/"+id)
	eq(t, code, 200)
	urls := map[string]string{}
	for _, f := range v.Deck.Files {
		urls[f.Path] = f.URL
	}
	get := func(u string) (*http.Response, []byte) {
		t.Helper()
		res, err := http.Get(s.root + u)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, b
	}
	size := func(b []byte) (int, int, string) {
		cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		return cfg.Width, cfg.Height, format
	}

	res, b := get(urls["media/big.png"])
	eq(t, res.StatusCode, 200)
	eq(t, res.Header.Get("Content-Type"), "image/jpeg")
	eq(t, res.Header.Get("Cache-Control"), "public, max-age=31536000, s-maxage=31536000, immutable")
	w, h, format := size(b)
	eq(t, []any{w, h, format}, []any{2048, 1024, "jpeg"})
	if len(b) >= len(big) {
		t.Fatalf("JPEG %d bytes, the PNG %d", len(b), len(big))
	}
	// transparent: stays a PNG, scaled
	res, b = get(urls["media/clear.png"])
	eq(t, res.StatusCode, 200)
	w, h, format = size(b)
	eq(t, []any{w, h, format}, []any{2048, 1024, "png"})
	// a small picture a JPEG would not shrink: the file as it is
	res, b = get(urls["media/dot.png"])
	eq(t, res.StatusCode, 200)
	eq(t, res.Header.Get("Content-Type"), "image/png")
	eq(t, bytes.Equal(b, squarePNG()), true)
	// an SVG as it is, kept by the CDN like the others, and running
	// nothing when opened on its own
	res, b = get(urls["media/mark.svg"])
	eq(t, res.StatusCode, 200)
	eq(t, res.Header.Get("Content-Type"), "image/svg+xml")
	eq(t, res.Header.Get("Cache-Control"), "public, max-age=31536000, s-maxage=31536000, immutable")
	match(t, res.Header.Get("Content-Security-Policy"), "sandbox")
	eq(t, string(b), markSVG)

	// an old stamp (the picture was replaced since): to the current address
	no := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := no.Get(s.root + "/api/view/" + id + "/pic?path=media%2Fbig.png&v=old")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	eq(t, res.StatusCode, 302)
	eq(t, res.Header.Get("Location"), urls["media/big.png"])
	// not a file of the deck, not a picture route
	for _, bad := range []string{"/api/view/" + id + "/pic?path=media%2Fnone.png&v=x", "/api/view/" + id + "/other", "/api/view/AbCdEf1234/pic?path=media%2Fbig.png"} {
		res, _ := get(bad)
		eq(t, res.StatusCode, 404, bad)
		eq(t, res.Header.Get("Cache-Control"), "no-store", bad)
	}

	// private: the owner's page reads Storage; the route shows nothing
	if err := f.db.Update(context.Background(), "shares", id, Doc{"visibility": "private"}); err != nil {
		t.Fatal(err)
	}
	code, _, mine := getAs(t, s.root+"/api/view/"+id, "google-ok")
	eq(t, code, 200)
	var pv viewAnswer
	json.Unmarshal(mine, &pv)
	for _, f := range pv.Deck.Files {
		if !strings.HasPrefix(f.URL, "https://") {
			t.Fatalf("private deck's %s: %s", f.Path, f.URL)
		}
	}
	res, _ = get(urls["media/big.png"])
	eq(t, res.StatusCode, 404)
}

const markSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20" viewBox="0 0 40 20"><rect width="40" height="20" fill="#e85d3a"/></svg>`

// A deck is laid out once per version of its document (viewcache.go): the
// layout is kept in Storage under the document's hash, a changed deck is
// laid out again, and one that read live data from the web is not kept.
func TestViewKept(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	forget := func() {
		viewMem.Lock()
		viewMem.m, viewMem.order = map[string]string{}, nil
		viewMem.Unlock()
	}
	stored := func(id string) (string, string, bool) {
		f.bucket.mu.Lock()
		defer f.bucket.mu.Unlock()
		o, ok := f.bucket.saved["views/"+id+".json"]
		key, body, _ := strings.Cut(string(o.data), "\n")
		return key, body, ok
	}
	c := call(t, s, "create_presentation", map[string]any{"title": "Kept", "markdown": "# One\n\n## Two\n", "visibility": "link"})
	id := sc(c)["deck_id"].(string)
	code, _, _, body := getView(t, s.root+"/api/view/"+id)
	eq(t, code, 200)
	key, kept, ok := stored(id)
	eq(t, ok, true)
	eq(t, len(key), 64)
	eq(t, kept, body)

	// a fresh instance reads it from Storage, not laying the deck out
	forget()
	f.bucket.Save(context.Background(), "views/"+id+".json", "application/json", []byte(key+"\n"+`{"deck":{"slides":7}}`), nil)
	_, _, _, again := getView(t, s.root+"/api/view/"+id)
	eq(t, again, `{"deck":{"slides":7}}`)

	// the deck changed: laid out again, and the new layout kept
	u := call(t, s, "update_presentation", map[string]any{"deck_id": id, "markdown": "# One\n\n## Two\n\n## Three\n"})
	if u.IsError {
		t.Fatal(textOf(u))
	}
	_, _, v, _ := getView(t, s.root+"/api/view/"+id)
	eq(t, v.Deck.Slides, 3)
	key2, _, _ := stored(id)
	if key2 == key {
		t.Fatal("the changed deck's layout is under the old key")
	}

	// the slides a link names are a layout of their own
	_, _, one, _ := getView(t, s.root+"/api/view/"+id+"?slides=two")
	eq(t, one.Deck.Slides, 1)
	_, _, all, _ := getView(t, s.root+"/api/view/"+id)
	eq(t, all.Deck.Slides, 3)

	// live data from the web: laid out for every reader
	live := call(t, s, "create_presentation", map[string]any{"title": "Live", "visibility": "link",
		"markdown": "# Costs\n\n```vega-lite\n{\"mark\": \"bar\", \"data\": {\"url\": \"https://data.test/costs.csv\"}, \"encoding\": {\"x\": {\"field\": \"a\"}, \"y\": {\"field\": \"b\"}}}\n```\n"})
	lid := sc(live)["deck_id"].(string)
	code, _, _, _ = getView(t, s.root+"/api/view/"+lid)
	eq(t, code, 200)
	_, _, ok = stored(lid)
	eq(t, ok, false)
}
