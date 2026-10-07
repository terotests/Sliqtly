// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// --- export_presentation: the editor's own PDF and PPTX (src/PresExport.rgr)

func pptxSlides(t *testing.T, data []byte) map[string]string {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range z.File {
		if regexp.MustCompile(`^ppt/slides/slide\d+\.xml$`).MatchString(f.Name) {
			r, _ := f.Open()
			b, _ := io.ReadAll(r)
			r.Close()
			out[f.Name] = string(b)
		}
	}
	return out
}

// a picture big enough for the PDF to embed (it leaves out 1×1 ones)
func squarePNG() []byte {
	im := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range im.Pix {
		im.Pix[i] = 200
	}
	im.Set(3, 3, color.RGBA{200, 30, 30, 255})
	var b bytes.Buffer
	png.Encode(&b, im)
	return b.Bytes()
}

// how many pixels of a picture are not (nearly) white
func inked(im image.Image) int {
	n := 0
	b := im.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := im.At(x, y).RGBA()
			if r < 0xe000 || g < 0xe000 || bl < 0xe000 {
				n++
			}
		}
	}
	return n
}

func TestExportPdfAndPptx(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := fmt.Sprintf(testDeck, twentyMonths()) + "\n## Picture\n\n![A dot](media/dot.png)\n\n## Code\n\n```diff js {.numbers}\n@@ -1,2 +1,2 @@\n-let a = 1\n+let a = 2\n```\n"
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Q3 / review", "markdown": md,
		"files":  []any{map[string]any{"name": "sales.csv", "text": "month,sales\nJan,10\nFeb,14\nMar,9\n"}},
		"images": []any{map[string]any{"name": "dot.png", "data_base64": base64.StdEncoding.EncodeToString(squarePNG())}},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)
	n := int(sc(c)["slides"].(float64))

	p := call(t, s, "export_presentation", map[string]any{"deck_id": id, "format": "pdf"})
	if p.IsError {
		t.Fatal(textOf(p))
	}
	po := sc(p)
	eq(t, po["name"], "Q3 - review.pdf")
	eq(t, po["slides"], float64(n))
	match(t, textOf(p), `^PDF of "Q3 / review" \(\d+ slides, \d+ KB\): https://sliqtly\.test/d/[0-9a-f-]{36}/Q3%20-%20review\.pdf\n`)
	// the link is the site's own: /d/<token>/<name> sends the kept file
	link := po["url"].(string)
	got, err := http.Get(s.root + strings.TrimPrefix(link, BASE))
	if err != nil {
		t.Fatal(err)
	}
	sent, _ := io.ReadAll(got.Body)
	got.Body.Close()
	eq(t, got.StatusCode, 200)
	eq(t, got.Header.Get("Content-Type"), "application/pdf")
	eq(t, got.Header.Get("Content-Disposition"), "attachment; filename*=UTF-8''Q3%20-%20review.pdf")
	if !bytes.HasPrefix(sent, []byte("%PDF-")) {
		t.Fatal("the link does not send the PDF")
	}
	for _, bad := range []string{"/d/00000000-0000-4000-8000-000000000000/x.pdf", "/d/..%2F..%2Fsecret/x", "/d/"} {
		r, err := http.Get(s.root + bad)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		eq(t, r.StatusCode, 404, bad)
	}
	pdf := f.bucket.saved["shares/"+id+"/exports/Q3 - review.pdf"]
	eq(t, pdf.contentType, "application/pdf")
	if !bytes.HasPrefix(pdf.data, []byte("%PDF-")) {
		t.Fatal("not a PDF")
	}
	eq(t, len(regexp.MustCompile(`/Type\s*/Page[^s]`).FindAll(pdf.data, -1)), n, "a page per slide")
	if !bytes.Contains(pdf.data, []byte("/Subtype /Image")) && !bytes.Contains(pdf.data, []byte("/Subtype/Image")) {
		t.Fatal("the picture is not in the PDF")
	}

	x := call(t, s, "export_presentation", map[string]any{"deck_id": id, "format": "pptx", "slides": []any{2, 5}})
	if x.IsError {
		t.Fatal(textOf(x))
	}
	eq(t, sc(x)["name"], "Q3 - review (slides 2, 5).pptx")
	pp := f.bucket.saved["shares/"+id+"/exports/Q3 - review (slides 2, 5).pptx"]
	eq(t, pp.contentType, "application/vnd.openxmlformats-officedocument.presentationml.presentation")
	slides := pptxSlides(t, pp.data)
	eq(t, len(slides), 2, "only the slides asked for")
	all := ""
	for _, x := range slides {
		all += x
	}
	if !strings.Contains(all, "Twenty months") || !strings.Contains(all, "Picture") {
		t.Fatal("the slides asked for are not the ones in the file")
	}

	bad := call(t, s, "export_presentation", map[string]any{"deck_id": id, "format": "pptx", "slides": []any{99}})
	match(t, textOf(bad), `^No slide 99: the presentation has \d+ slides\.$`)
	match(t, textOf(call(t, s, "export_presentation", map[string]any{"deck_id": id, "format": "odt"})), `format is pdf, pptx, docx or html`)

	// Word: the deck as a document; the charts as the pictures this server
	// draws of them, the deck's picture as itself
	w := call(t, s, "export_presentation", map[string]any{"deck_id": id, "format": "docx"})
	if w.IsError {
		t.Fatal(textOf(w))
	}
	match(t, textOf(w), `^Word document of "Q3 / review"`)
	wd := f.bucket.saved["shares/"+id+"/exports/Q3 - review.docx"]
	eq(t, wd.contentType, "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	z, err := zip.NewReader(bytes.NewReader(wd.data), int64(len(wd.data)))
	if err != nil {
		t.Fatal(err)
	}
	media, body := 0, ""
	var shot []byte
	for _, zf := range z.File {
		r, _ := zf.Open()
		b, _ := io.ReadAll(r)
		r.Close()
		if strings.HasPrefix(zf.Name, "word/media/") {
			media++
			if strings.Contains(zf.Name, "image1") {
				shot = b
			}
		}
		if zf.Name == "word/document.xml" {
			body = string(b)
		}
	}
	if media < 3 {
		t.Fatalf("two chart pictures and the deck's picture, got %d", media)
	}
	if !strings.Contains(body, "Twenty months") || !strings.Contains(body, "w:val=\"Heading2\"") {
		t.Fatal("the slides' headings are not in the document")
	}
	im, err := png.Decode(bytes.NewReader(shot))
	if err != nil {
		t.Fatal("the chart's picture is not a PNG: ", err)
	}
	if im.Bounds().Dx() < 400 || inked(im) < 200 {
		t.Fatalf("the chart's picture is empty or small: %v, %d inked pixels", im.Bounds(), inked(im))
	}

	// a web page: one file, its pictures inside it
	h := call(t, s, "export_presentation", map[string]any{"deck_id": id, "format": "html"})
	if h.IsError {
		t.Fatal(textOf(h))
	}
	hp := f.bucket.saved["shares/"+id+"/exports/Q3 - review.html"]
	eq(t, hp.contentType, "text/html; charset=utf-8")
	page := string(hp.data)
	if !strings.HasPrefix(page, "<!DOCTYPE html>") || !strings.Contains(page, `<section class="slide" id="slide-2">`) {
		t.Fatal("not the deck as a page")
	}
	if n := strings.Count(page, `src="data:image/png;base64,`); n < 3 {
		t.Fatalf("two chart pictures and the deck's picture inside the page, got %d", n)
	}
	match(t, textOf(call(t, s, "export_presentation", map[string]any{"deck_id": "nosuchdeck1", "format": "pdf"})), `No presentation nosuchdeck1`)
}

// A photo album for print: `@media print` in the deck's css gives a PDF on
// the print page with bleed and crop marks, and the result of create warns
// about a picture too small for paper.
func TestPrintAlbum(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	md := "# Summer {heading=hidden}\n\nThe pictures.\n\n## Beach {heading=hidden}\n\n```gallery\n- media/dot.png: The beach in July\n```\n{layout=full fit=cover caption=overlay}\n"
	css := "@media print {\n  page { width: 297mm; height: 210mm; bleed: 3mm; safe-area: 8mm; }\n  deck { crop-marks: on; }\n}\n"
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Album", "markdown": md, "css": css,
		"images": []any{map[string]any{"name": "dot.png", "data_base64": base64.StdEncoding.EncodeToString(squarePNG())}},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	eq(t, sc(c)["slides"], float64(2))
	ws := fmt.Sprint(sc(c)["warnings"])
	if !strings.Contains(ws, "media/dot.png: ") || !strings.Contains(ws, "dpi in print, under 300") {
		t.Fatal(ws)
	}
	id := sc(c)["deck_id"].(string)
	p := call(t, s, "export_presentation", map[string]any{"deck_id": id, "format": "pdf"})
	if p.IsError {
		t.Fatal(textOf(p))
	}
	pdf := f.bucket.saved["shares/"+id+"/exports/Album.pdf"]
	eq(t, len(regexp.MustCompile(`/Type\s*/Page[^s]`).FindAll(pdf.data, -1)), 2, "a page per slide")
	// 3mm bleed + 20pt slug = 28.5pt in; 297 × 210 mm = 841.89 × 595.28pt
	if !regexp.MustCompile(`/TrimBox \[28\.50\d* 28\.50\d* 870\.39\d* 623\.77\d*\]`).Match(pdf.data) {
		t.Fatal(regexp.MustCompile(`/TrimBox \[[^]]*\]`).FindString(string(pdf.data)))
	}
	if !bytes.Contains(pdf.data, []byte("/BleedBox [20 20 ")) {
		t.Fatal("no bleed box")
	}
}

// An album the checks read as one: its pictures count as used, a full-page
// album's slides are named (not an overflow), and the layout report shows
// the gallery with its cells.
func TestGalleryChecks(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	pic := base64.StdEncoding.EncodeToString(squarePNG())
	md := "## Summer\n\n```gallery\n- media/k1.png: Beach\n- media/k2.png\n- media/k3.png\n- The first days went by just looking.\n```\n\n## Every day {heading=hidden}\n\n```gallery\n- media/k1.png: Morning\n- media/k2.png\n```\n{layout=full fit=cover}\n"
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Album", "markdown": md,
		"images": []any{
			map[string]any{"name": "k1.png", "data_base64": pic},
			map[string]any{"name": "k2.png", "data_base64": pic},
			map[string]any{"name": "k3.png", "data_base64": pic},
		},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	text := textOf(c)
	ws := fmt.Sprint(sc(c)["warnings"])
	if strings.Contains(ws, "does not use") || strings.Contains(ws, "does not fit") {
		t.Fatal(ws)
	}
	eq(t, sc(c)["slides"], float64(3))
	match(t, text, `- gallery \(grid, 3 pictures, 1 text cell\) at `)
	match(t, text, `Slide 3 "Every day \(2\)"`)
	match(t, text, `- gallery \(full page, 1 picture\) at `)
}

// Text outside ASCII and SVG backgrounds in the exports: the PPTX and the
// Word document carry "ä" and "·" as themselves (they came out as "Ã¤"),
// and a slide's SVG background is in the PDF and the PPTX (it was left
// out: their writers read PNG and JPEG only).
func TestExportUnicodeAndSvgBackground(t *testing.T) {
	f := fakeFirebase()
	s := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s.close()
	grad := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1600 900"><defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#1f2a4d"/><stop offset="1" stop-color="#4a5a8c"/></linearGradient></defs><rect width="1600" height="900" fill="url(#g)"/></svg>`
	flat := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1600 900"><rect width="1600" height="900" fill="#f4f5f9"/><rect width="1600" height="12" fill="#2e3a63"/></svg>`
	md := "# Kasvusuunnitelma {bg=media/title.svg}\n\nJOHDON KATSAUS · 91 ASIAKASTA\n\nMissä kasvu on ja mitä teemme vuosineljänneksittäin.\n\n## Avainluvut {bg=media/content.svg}\n\n- Summa ≥ 800 → avainasiakas\n"
	c := call(t, s, "create_presentation", map[string]any{
		"title": "Kasvu", "markdown": md,
		"images": []any{
			map[string]any{"name": "title.svg", "text": grad},
			map[string]any{"name": "content.svg", "text": flat},
		},
	})
	if c.IsError {
		t.Fatal(textOf(c))
	}
	id := sc(c)["deck_id"].(string)

	x := call(t, s, "export_presentation", map[string]any{"deck_id": id, "format": "pptx"})
	if x.IsError {
		t.Fatal(textOf(x))
	}
	pp := f.bucket.saved["shares/"+id+"/exports/Kasvu.pptx"]
	all := ""
	for _, sx := range pptxSlides(t, pp.data) {
		all += sx
	}
	for _, want := range []string{"Missä kasvu on", "vuosineljänneksittäin", "KATSAUS · 91", "≥ 800 → avainasiakas"} {
		if !strings.Contains(all, want) {
			t.Fatalf("%q is not in the slides: %s", want, all)
		}
	}
	if strings.Contains(all, "Ã") {
		t.Fatal("text written twice as UTF-8")
	}
	// each slide's background, drawn as the stage shows it, behind it
	for name, sx := range pptxSlides(t, pp.data) {
		if !strings.Contains(sx, "<p:bg><p:bgPr><a:blipFill>") {
			t.Fatalf("%s has no background picture", name)
		}
	}
	z, err := zip.NewReader(bytes.NewReader(pp.data), int64(len(pp.data)))
	if err != nil {
		t.Fatal(err)
	}
	stills := 0
	for _, zf := range z.File {
		if strings.HasPrefix(zf.Name, "ppt/media/") {
			r, _ := zf.Open()
			b, _ := io.ReadAll(r)
			r.Close()
			im, err := jpeg.Decode(bytes.NewReader(b))
			if err != nil {
				t.Fatal(zf.Name, err)
			}
			// the title's dark gradient is drawn, not left white
			if zf.Name == "ppt/media/image1.jpeg" && inked(im) < im.Bounds().Dx()*im.Bounds().Dy()/2 {
				t.Fatalf("%s is not the gradient", zf.Name)
			}
			stills++
		}
	}
	eq(t, stills, 2, "a background picture per slide")

	p := call(t, s, "export_presentation", map[string]any{"deck_id": id, "format": "pdf"})
	if p.IsError {
		t.Fatal(textOf(p))
	}
	pdf := f.bucket.saved["shares/"+id+"/exports/Kasvu.pdf"]
	if n := len(regexp.MustCompile(`/Subtype\s*/Image`).FindAll(pdf.data, -1)); n < 1 {
		t.Fatalf("the gradient background is not in the PDF (%d pictures)", n)
	}

	w := call(t, s, "export_presentation", map[string]any{"deck_id": id, "format": "docx"})
	if w.IsError {
		t.Fatal(textOf(w))
	}
	wd := f.bucket.saved["shares/"+id+"/exports/Kasvu.docx"]
	wz, err := zip.NewReader(bytes.NewReader(wd.data), int64(len(wd.data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, zf := range wz.File {
		if zf.Name == "word/document.xml" {
			r, _ := zf.Open()
			b, _ := io.ReadAll(r)
			r.Close()
			if !strings.Contains(string(b), "Missä kasvu on") || strings.Contains(string(b), "Ã") {
				t.Fatal("the Word document's text is not the deck's")
			}
		}
	}
	h := call(t, s, "export_presentation", map[string]any{"deck_id": id, "format": "html"})
	if h.IsError {
		t.Fatal(textOf(h))
	}
	page := string(f.bucket.saved["shares/"+id+"/exports/Kasvu.html"].data)
	if !strings.Contains(page, "Missä kasvu on") || strings.Contains(page, "Ã") {
		t.Fatal("the web page's text is not the deck's")
	}
}
