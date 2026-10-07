// SPDX-License-Identifier: AGPL-3.0-or-later

// The faces the editor lays slides out with, copied from Ranger by gen.mjs:
// the deck model measures with them (host_font) and render.go draws with
// them, so a slide is broken into lines here where the editor breaks it.

package main

import (
	"embed"
	"strings"
	"sync"

	"golang.org/x/image/font/sfnt"
)

//go:embed fonts/*.ttf
var fontFiles embed.FS

// DejaVu Sans, kept in the repository (symbols/LICENSE): the symbols the
// editor's faces lack (⇒ ✓ ✗ ★ ◆ ∈ ∅ ₂ …), which a browser draws from a
// system font and a picture drawn here had as an empty box
//
//go:embed symbols/*.ttf
var symbolFiles embed.FS

// the layout's face names (web/main.js FACES) → files
var faceFiles = map[string]string{
	"Open Sans":            "OpenSans-Regular.ttf",
	"Open Sans-Bold":       "OpenSans-Bold.ttf",
	"Open Sans-Italic":     "OpenSans-Italic.ttf",
	"Open Sans-BoldItalic": "OpenSans-BoldItalic.ttf",
	"Noto Sans":            "NotoSans-Regular.ttf",
	"Noto Sans-Bold":       "NotoSans-Bold.ttf",
	"Noto Sans-Italic":     "NotoSans-Italic.ttf",
	"Noto Sans-BoldItalic": "NotoSans-BoldItalic.ttf",
	"Noto Emoji-Regular":   "NotoEmoji-Regular.ttf",
	// the diagram looks' faces (RangerFlow FlowLook: {style=cartoon} …)
	"Gloria Hallelujah":      "GloriaHallelujah.ttf",
	"Fjalla One":             "FjallaOne-Regular.ttf",
	"Josefin Sans-Bold":      "JosefinSans-Bold.ttf",
	"Droid Serif-BoldItalic": "DroidSerif-BoldItalic.ttf",
	// the faces a deck's CSS can name (src/PresFonts.rgr)
	"Droid Serif":        "DroidSerif.ttf",
	"Droid Serif-Bold":   "DroidSerif-Bold.ttf",
	"Droid Serif-Italic": "DroidSerif-Italic.ttf",
	"Lato":               "Lato-Regular.ttf",
	"Lato-Bold":          "Lato-Bold.ttf",
	"Lato-Italic":        "Lato-Italic.ttf",
	"Lato-BoldItalic":    "Lato-BoldItalic.ttf",
}

var symbolFaces = map[string]string{
	"DejaVu Sans":      "DejaVuSans.ttf",
	"DejaVu Sans-Bold": "DejaVuSans-Bold.ttf",
}

func fontBytes(name string) []byte {
	if f, ok := symbolFaces[name]; ok {
		b, err := symbolFiles.ReadFile("symbols/" + f)
		if err != nil {
			return nil
		}
		return b
	}
	f, ok := faceFiles[name]
	if !ok {
		return nil
	}
	b, err := fontFiles.ReadFile("fonts/" + f)
	if err != nil {
		return nil
	}
	return b
}

// Font is host_font: a fresh copy, since the deck model keeps the buffer.
func (h *McpHost) Font(name string) []byte {
	b := fontBytes(name)
	if b == nil {
		return []byte{}
	}
	return append([]byte(nil), b...)
}

var (
	facesMu sync.Mutex
	faces   = map[string]*sfnt.Font{}
)

// face is the parsed face for a family, weight and slant as a display list
// names them: the family may carry the layout's "-Bold" suffix, the weight
// may say bold; anything not here is drawn in Open Sans.
func face(family, weight string, italic bool) *sfnt.Font {
	fam := family
	bold := false
	for _, suf := range []string{"-BoldItalic", "-Bold", "-Italic", "-Regular"} {
		if strings.HasSuffix(fam, suf) {
			if strings.Contains(suf, "Bold") {
				bold = true
			}
			if strings.Contains(suf, "Italic") {
				italic = true
			}
			fam = strings.TrimSuffix(fam, suf)
		}
	}
	if w := strings.ToLower(weight); strings.Contains(w, "bold") || w == "600" || w == "700" || w == "800" || w == "900" {
		bold = true
	}
	if fam == "Noto Emoji" {
		return loadFace("Noto Emoji-Regular")
	}
	suffix := ""
	switch {
	case bold && italic:
		suffix = "-BoldItalic"
	case bold:
		suffix = "-Bold"
	case italic:
		suffix = "-Italic"
	}
	// a face this server has (a look's Fjalla One or Gloria Hallelujah, a
	// deck's Lato): the layout measured the words in it, so they are drawn
	// in it too; drawn in Open Sans they came out wider than their box
	if _, ok := faceFiles[fam+suffix]; ok {
		return loadFace(fam + suffix)
	}
	return loadFace("Open Sans" + suffix)
}

func loadFace(name string) *sfnt.Font {
	facesMu.Lock()
	defer facesMu.Unlock()
	if f, ok := faces[name]; ok {
		return f
	}
	var f *sfnt.Font
	if b := fontBytes(name); b != nil {
		f, _ = sfnt.Parse(b)
	}
	faces[name] = f
	return f
}
