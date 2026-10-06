// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
		"title": "Viewed", "markdown": md,
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
