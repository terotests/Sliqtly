// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"image/jpeg"
	"io"
	"net/http"
	"strings"
	"testing"
)

// --- GET /s/<id> and /api/card/<id>.jpg: a shared link's preview card
// (linkcard.go, rgr/View.rgr LinkCard)

// the site's index.html as the tests' Hosting serves it (fakeNet)
const viewerPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<base href="/" />
<title>Sliqtly: better slides through MCP</title>
<meta name="description" content="Sliqtly turns Markdown into slides." />
<script>/* the viewer */</script>
</head>
<body><canvas id="c"></canvas></body>
</html>`

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func fetch(t *testing.T, url string) (int, http.Header, string) {
	t.Helper()
	res, err := noRedirect.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(b)
}

func metaOf(page, prop string) string {
	for _, m := range metaTag.FindAllString(page, -1) {
		if strings.Contains(m, `property="`+prop+`"`) || strings.Contains(m, `name="`+prop+`"`) {
			for _, a := range attrPattern.FindAllStringSubmatch(m, -1) {
				if strings.EqualFold(a[1], "content") {
					return a[3]
				}
			}
		}
	}
	return ""
}

func TestLinkCard(t *testing.T) {
	f := fakeFirebase()
	s := start(t, testEnv(&f, nil), "")
	defer s.close()
	md := "---\ntitle: Kvartaalikatsaus\n---\n\n# Myynti & <kasvu>\n\nLiikevaihto **kasvoi** 12 % ja [asiakkaita](https://x.test) tuli lisää.\n\n```js\nsecret()\n```\n\n- Ensimmäinen kohta\n\n::: notes\nPuhujan muistiinpanot\n:::\n\n## Toinen dia\n\nLisää tekstiä.\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Q3 & \"vuosi\"", "markdown": md})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)

	code, h, page := fetch(t, s.root+"/s/"+id)
	eq(t, code, 200)
	eq(t, h.Get("Content-Type"), "text/html; charset=utf-8")
	eq(t, h.Get("Cache-Control"), "public, max-age=0, s-maxage=60")
	// the deck's name, escaped, in place of the site's title
	if !strings.Contains(page, "<title>Q3 &amp; &#34;vuosi&#34; · Sliqtly</title>") || strings.Contains(page, "better slides through MCP") {
		t.Fatalf("title: %s", page)
	}
	eq(t, metaOf(page, "og:title"), "Q3 &amp; &#34;vuosi&#34;")
	desc := metaOf(page, "og:description")
	eq(t, desc, "Myynti &amp; &lt;kasvu&gt; · Liikevaihto kasvoi 12 % ja asiakkaita tuli lisää. · Ensimmäinen kohta · Toinen dia · Lisää tekstiä.")
	eq(t, metaOf(page, "description"), desc)
	eq(t, strings.Count(page, `name="description"`), 1)
	eq(t, metaOf(page, "og:url"), BASE+"/s/"+id)
	eq(t, metaOf(page, "twitter:card"), "summary_large_image")
	pic := metaOf(page, "og:image")
	if !strings.HasPrefix(pic, BASE+"/api/card/"+id+".jpg?v=") {
		t.Fatalf("og:image %q", pic)
	}
	// the page is otherwise the site's own, script and all
	if !strings.Contains(page, "<script>/* the viewer */</script>") || !strings.Contains(page, `<canvas id="c">`) {
		t.Fatalf("page: %s", page)
	}
	code, _, _ = fetch(t, s.root+"/s/"+id+"/")
	eq(t, code, 200)

	// the picture: the first slide, 1200 wide
	at := strings.TrimPrefix(pic, BASE)
	code, h, body := fetch(t, s.root+at)
	eq(t, code, 200)
	eq(t, h.Get("Content-Type"), "image/jpeg")
	eq(t, h.Get("Cache-Control"), "public, max-age=86400, s-maxage=86400, immutable")
	img, err := jpeg.Decode(bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, img.Bounds().Dx(), 1200)
	// kept: the same bytes the second time
	_, _, again := fetch(t, s.root+at)
	eq(t, again == body, true)
	// another stamp (or none) is sent to the current picture, not drawn
	code, h, _ = fetch(t, s.root+"/api/card/"+id+".jpg?v=1")
	eq(t, code, 302)
	eq(t, h.Get("Location"), at)
	code, h, _ = fetch(t, s.root+"/api/card/"+id+".jpg")
	eq(t, code, 302)
	eq(t, h.Get("Location"), at)

	// a private deck has no card: the site's page as it is, no picture
	ctx := context.Background()
	if err := f.db.Update(ctx, "shares", id, Doc{"visibility": "private"}); err != nil {
		t.Fatal(err)
	}
	code, _, page = fetch(t, s.root+"/s/"+id)
	eq(t, code, 200)
	eq(t, page, viewerPage)
	code, h, _ = fetch(t, s.root+at)
	eq(t, code, 404)
	eq(t, h.Get("Cache-Control"), "no-store")

	// no such deck: the site's page; not an id: 404
	code, _, page = fetch(t, s.root+"/s/AbCdEf1234")
	eq(t, code, 200)
	eq(t, page, viewerPage)
	for _, bad := range []string{"/s/a", "/s/../x", "/s/" + id + "/more"} {
		code, _, _ = fetch(t, s.root+bad)
		eq(t, code, 404, bad)
	}
	code, _, _ = fetch(t, s.root+"/api/card/AbCdEf1234.jpg?v=1")
	eq(t, code, 404)
}

// without the site's page the reader still reaches the viewer
func TestLinkCardNoPage(t *testing.T) {
	f := fakeFirebase()
	env := testEnv(&f, nil)
	env.BaseURL = "https://down.test"
	s := start(t, env, "")
	defer s.close()
	code, h, _ := fetch(t, s.root+"/s/AbCdEf1234")
	eq(t, code, 302)
	eq(t, h.Get("Location"), "/#share=AbCdEf1234")
}

func TestCardClip(t *testing.T) {
	eq(t, cardClip("  a\n b  ", 10), "a b")
	long := strings.Repeat("sana ", 60)
	got := cardClip(long, 200)
	if len([]rune(got)) > 200 || !strings.HasSuffix(got, "sana…") {
		t.Fatalf("%q", got)
	}
	eq(t, cardClip(strings.Repeat("ä", 10), 5), "ääää…")
}
