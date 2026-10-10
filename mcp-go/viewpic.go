// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// The public viewer's pictures (GET /api/view/<id>/pic?path=…, rgr/App.rgr
// servePicture): a stored picture as the page needs it to paint a slide.
// A deck's pictures are kept as they were given, often lossless PNGs of
// 1–2 MB each, and a phone fetched every one of them before it showed the
// first slide. Here an opaque picture becomes a JPEG and a larger one is
// scaled to viewPictureSide; the CDN keeps the answer (immutable, by the
// file's stamp), so each picture is made once.

import (
	"bytes"
	"crypto/sha256"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"sync"

	"golang.org/x/image/draw"
)

// the longer side, in pixels, of a picture the viewer gets: a full-slide
// picture stays sharp on a large screen
const viewPictureSide = 2048

// how the JPEGs are made: illustrations with lettering stay clean
const viewPictureQuality = 85

// at most this many pictures are decoded at once on an instance (each takes
// 4 bytes a pixel while it is made)
var viewPictureSlots = make(chan struct{}, 2)

// ViewPicture is the picture in data made for the viewer: a JPEG when it
// has no transparency, else a PNG scaled to `side`; empty when the file as
// it is should be sent (an SVG, a GIF, a picture that does not decode, or
// one the change would not make clearly smaller).
func (h *McpHost) ViewPicture(data []byte, side int64) []byte {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format == "gif" {
		return nil
	}
	big := int64(cfg.Width) > side || int64(cfg.Height) > side
	if (format == "jpeg" || format == "webp") && !big {
		return nil
	}
	viewPictureSlots <- struct{}{}
	defer func() { <-viewPictureSlots }()
	img, err := decodePicture(data)
	if err != nil {
		return nil
	}
	if big {
		img = scaledTo(img, int(side))
	}
	var out bytes.Buffer
	if opaque(img) {
		err = jpeg.Encode(&out, img, &jpeg.Options{Quality: viewPictureQuality})
	} else if big {
		err = png.Encode(&out, img)
	} else {
		return nil
	}
	// fewer pixels are worth any smaller file (the phone decodes them all);
	// the same pixels as a JPEG only a clearly smaller one
	if err != nil || out.Len() >= len(data) || (!big && out.Len() > len(data)*4/5) {
		return nil
	}
	return out.Bytes()
}

// ContentType is what the bytes are, by their first bytes ("image/png")
func (h *McpHost) ContentType(data []byte) string {
	return http.DetectContentType(data)
}

func scaledTo(img image.Image, side int) image.Image {
	b := img.Bounds()
	w, ht := b.Dx(), b.Dy()
	if w >= ht {
		ht, w = max(1, ht*side/w), side
	} else {
		w, ht = max(1, w*side/ht), side
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, ht))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

func opaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	return false
}

// --- the contrast check's samples, kept per instance ------------------------
//
// Every /api/view lays its deck out again, and the layout samples each
// picture it draws (ImageGrid), which decodes it whole: a deck of ten large
// PNGs spent most of its second there. A picture's samples depend on its
// bytes only, so they are kept here by the bytes' SHA-256.

const gridCacheMax = 512

var gridCache = struct {
	sync.Mutex
	m     map[[32]byte][]int64
	order [][32]byte
}{m: map[[32]byte][]int64{}}

func cachedGrid(data []byte, compute func() []int64) []int64 {
	key := sha256.Sum256(data)
	gridCache.Lock()
	g, ok := gridCache.m[key]
	gridCache.Unlock()
	if ok {
		return append([]int64(nil), g...)
	}
	g = compute()
	gridCache.Lock()
	defer gridCache.Unlock()
	if _, ok := gridCache.m[key]; !ok {
		if len(gridCache.order) >= gridCacheMax {
			delete(gridCache.m, gridCache.order[0])
			gridCache.order = gridCache.order[1:]
		}
		gridCache.m[key] = g
		gridCache.order = append(gridCache.order, key)
	}
	return append([]int64(nil), g...)
}
