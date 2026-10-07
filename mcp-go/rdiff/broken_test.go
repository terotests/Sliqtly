// SPDX-License-Identifier: AGPL-3.0-or-later

package rdiff

// Damaged ZIPs and PNGs, and damaged deltas: a delta of any two files
// rebuilds the target byte for byte, and nothing panics or runs on. A part
// whose deflate stream never ended once made the backup's Deltify inflate
// without end, and a name past the end of the file panicked the server.

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"runtime/debug"
	"testing"
	"time"
)

func mkzip(r *rand.Rand) []byte {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for i := 0; i < 1+r.Intn(4); i++ {
		f, _ := w.Create(string(rune('a'+i)) + ".xml")
		p := make([]byte, r.Intn(3000))
		for j := range p {
			p[j] = byte('a' + r.Intn(5))
		}
		f.Write(p)
	}
	w.Close()
	return b.Bytes()
}

func TestBrokenZipsNeverPanic(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for it := 0; it < 1500; it++ {
		a, b := mkzip(r), mkzip(r)
		for k := 0; k < r.Intn(6); k++ {
			if len(b) > 0 {
				b[r.Intn(len(b))] = byte(r.Intn(256))
			}
		}
		if r.Intn(3) == 0 && len(a) > 0 {
			a[r.Intn(len(a))] = byte(r.Intn(256))
		}
		start := time.Now()
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("it %d panic: %v\n%s", it, p, debug.Stack())
				}
			}()
			d := Exact(a, b)
			got, err := Apply(a, d)
			if err != nil || !bytes.Equal(got, b) {
				t.Fatalf("it %d roundtrip", it)
			}
			d1 := Diff(a, b)
			for k := 0; k < 1+r.Intn(3); k++ {
				d1[r.Intn(len(d1))] = byte(r.Intn(256))
			}
			Apply(a, d1)
			Apply(a, d1[:r.Intn(len(d1))])
		}()
		if el := time.Since(start); el > time.Second {
			t.Fatalf("it %d slow %v", it, el)
		}
	}
}

func mkpng(r *rand.Rand) []byte {
	w, h := 1+r.Intn(60), 1+r.Intn(60)
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h/3; i++ {
		im.Set(r.Intn(w), r.Intn(h), color.NRGBA{uint8(r.Intn(256)), 9, 9, 255})
	}
	var b bytes.Buffer
	png.Encode(&b, im)
	return b.Bytes()
}

func TestBrokenPngsNeverPanic(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for it := 0; it < 500; it++ {
		a, b := mkpng(r), mkpng(r)
		for k := 0; k < r.Intn(4); k++ {
			b[8+r.Intn(len(b)-8)] = byte(r.Intn(256))
		}
		if r.Intn(3) == 0 {
			a[8+r.Intn(len(a)-8)] = byte(r.Intn(256))
		}
		start := time.Now()
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("it %d panic: %v\n%s", it, p, debug.Stack())
				}
			}()
			d := Exact(a, b)
			got, err := Apply(a, d)
			if err != nil || !bytes.Equal(got, b) {
				t.Fatalf("it %d roundtrip", it)
			}
			// a damaged delta never panics
			d2 := append([]byte{}, d...)
			for k := 0; k < 1+r.Intn(3); k++ {
				d2[r.Intn(len(d2))] = byte(r.Intn(256))
			}
			Apply(a, d2)
			Apply(a, d2[:r.Intn(len(d2))])
		}()
		if el := time.Since(start); el > time.Second {
			t.Fatalf("it %d slow %v", it, el)
		}
	}
}
