// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
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
	match(t, textOf(p), `^PDF of "Q3 / review" \(\d+ slides, \d+ KB\): https://firebasestorage\.googleapis\.com/v0/b/bucket\.test/o/shares%2F`+id+`%2Fexports%2FQ3%20-%20review\.pdf\?alt=media&token=`)
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
