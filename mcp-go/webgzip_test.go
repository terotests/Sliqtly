// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServeWebGzip(t *testing.T) {
	js := []byte(strings.Repeat("function draw(){return 1}\n", 400))
	get := func(name, accept string, body []byte) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/"+name, nil)
		if accept != "" {
			r.Header.Set("Accept-Encoding", accept)
		}
		if !serveWebGzip(w, r, name, time.Time{}, func() ([]byte, error) { return body, nil }, int64(len(body))) {
			w.Write(body)
		}
		return w
	}

	w := get("pres_app.js", "gzip, deflate, br", js)
	if w.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(w.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("a script is sent gzipped: %v", w.Header())
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	back, _ := io.ReadAll(zr)
	if !bytes.Equal(back, js) || w.Body.Len() >= len(js) {
		t.Fatalf("gzipped copy: %d → %d bytes, same=%v", len(js), w.Body.Len(), bytes.Equal(back, js))
	}

	for _, accept := range []string{"", "identity", "gzip;q=0", "br"} {
		if w := get("pres_app.js", accept, js); w.Header().Get("Content-Encoding") != "" || w.Body.Len() != len(js) {
			t.Fatalf("Accept-Encoding %q: sent as it is", accept)
		}
	}
	png := append([]byte("\x89PNG"), js...)
	if w := get("pic.png", "gzip", png); w.Header().Get("Content-Encoding") != "" {
		t.Fatal("a picture is not gzipped again")
	}
	if w := get("tiny.css", "gzip", []byte("a{}")); w.Header().Get("Content-Encoding") != "" {
		t.Fatal("a tiny file is sent as it is")
	}
}

func TestAcceptsGzipWeights(t *testing.T) {
	if !acceptsGzip(&http.Request{Header: http.Header{"Accept-Encoding": {"br;q=1.0, gzip;q=0.8"}}}) {
		t.Fatal("gzip with a weight is taken")
	}
}
