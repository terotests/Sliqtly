// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// --- GET /api/export/<id>/<format>: the viewer's downloads (rgr/View.rgr ViewExport)

func getExport(t *testing.T, url string) (int, http.Header, []byte) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, body
}

func TestViewerExports(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "---\ntitle: Säästöt\ntransition: fade\n---\n\n# Säästöt {art=waves}\n\nIntro\n{.lead}\n\n## Picture {#picture .build anim=rise}\n\nText $x^{2}$ and {braces}\n\n```css\ndeck { fx: starfield; }\n{.kept}\n```\n"
	c := call(t, s, "create_presentation", map[string]any{"title": "Säästöt 2026", "markdown": md, "visibility": "link"})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)

	code, h, pdf := getExport(t, s.root+"/api/export/"+id+"/pdf")
	eq(t, code, 200)
	eq(t, h.Get("Content-Type"), "application/pdf")
	eq(t, h.Get("Cache-Control"), "no-store")
	if !strings.HasPrefix(h.Get("Content-Disposition"), "attachment;") || !strings.Contains(h.Get("Content-Disposition"), "2026.pdf") {
		t.Fatalf("disposition %q", h.Get("Content-Disposition"))
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("not a PDF: %.20q", pdf)
	}

	code, h, pptx := getExport(t, s.root+"/api/export/"+id+"/pptx")
	eq(t, code, 200)
	eq(t, h.Get("Content-Type"), "application/vnd.openxmlformats-officedocument.presentationml.presentation")
	if !bytes.HasPrefix(pptx, []byte("PK")) {
		t.Fatalf("not a zip: %.20q", pptx)
	}

	code, h, text := getExport(t, s.root+"/api/export/"+id+"/md")
	eq(t, code, 200)
	eq(t, h.Get("Content-Type"), "text/markdown; charset=utf-8")
	want := "---\ntitle: Säästöt\n---\n\n# Säästöt\n\nIntro\n\n## Picture\n\nText $x^{2}$ and {braces}\n\n```css\ndeck { fx: starfield; }\n{.kept}\n```\n"
	eq(t, string(text), want)

	// the view link's sections only
	code, _, one := getExport(t, s.root+"/api/export/"+id+"/md?slides=picture")
	eq(t, code, 200)
	if strings.Contains(string(one), "Intro") || !strings.Contains(string(one), "## Picture") {
		t.Fatalf("slides=picture: %q", one)
	}

	code, _, _ = getExport(t, s.root+"/api/export/"+id+"/docx")
	eq(t, code, 400)

	// a private deck is not found, as in the viewer
	if err := f.db.Update(context.Background(), "shares", id, Doc{"visibility": "private"}); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"pdf", "pptx", "md"} {
		code, _, _ = getExport(t, s.root+"/api/export/"+id+"/"+format)
		eq(t, code, 404, format)
	}
	for _, bad := range []string{"/api/export/AbCdEf1234/pdf", "/api/export/../x/md", "/api/export/", "/api/export/" + id} {
		code, _, _ := getExport(t, s.root+bad)
		if code != 404 && code != 400 {
			t.Fatalf("%s: %d", bad, code)
		}
	}
}

func TestPlainMarkdown(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"# A {.x}", "# A"},
		{"para\n{.lead}\nnext", "para\nnext"},
		{"$a_{i}$ {b}", "$a_{i}$ {b}"},
		{"no {=x}", "no {=x}"},
		{"x {fx=drops furniture=off}", "x"},
		{"---\ntransition: fade\n---\n# T", "# T"},
		{"~~~~\n{.a}\n~~~\n{.a}\n~~~~\n{.b}", "~~~~\n{.a}\n~~~\n{.a}\n~~~~"},
	} {
		eq(t, PlainMd_static_text(c.in), c.want, fmt.Sprintf("%q", c.in))
	}
}
