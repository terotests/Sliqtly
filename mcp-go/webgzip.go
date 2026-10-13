// SPDX-License-Identifier: AGPL-3.0-or-later

// The page's own files (web/dist: scripts, styles, faces) sent gzipped to a
// browser that takes it. pres_app.js alone is 7 MB as it is and 2 MB
// gzipped; neither Cloud Run nor the server's own net/http compresses, so
// without this the editor's first load is the files' full size. Each file
// is compressed once, the first time it is asked for, and kept: the files
// do not change while the server runs.

package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// the kinds that shrink; pictures, sound and video are compressed already
var webGzipKinds = map[string]bool{
	".js": true, ".mjs": true, ".css": true, ".html": true, ".json": true,
	".svg": true, ".ttf": true, ".otf": true, ".wasm": true, ".csv": true,
	".txt": true, ".md": true, ".map": true, ".xml": true,
}

// smaller than this, gzip's own bytes cost more than they save
const webGzipMin = 1024

type webGzipped struct {
	once sync.Once
	gz   []byte // nil when gzip does not make it smaller
}

var webGzipCache sync.Map // name|size|mtime → *webGzipped

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, params, _ := strings.Cut(part, ";")
		if !strings.EqualFold(strings.TrimSpace(enc), "gzip") {
			continue
		}
		// "gzip;q=0" says no
		if q, ok := strings.CutPrefix(strings.ReplaceAll(params, " ", ""), "q="); ok {
			f, err := strconv.ParseFloat(q, 64)
			return err == nil && f > 0
		}
		return true
	}
	return false
}

// serveWebGzip answers with the gzipped copy of b when the kind shrinks and
// the browser takes gzip; false leaves the answer to the caller. Headers
// such as Content-Type and Cache-Control are set by the caller first.
func serveWebGzip(w http.ResponseWriter, r *http.Request, name string, mtime time.Time, b func() ([]byte, error), size int64) bool {
	if !webGzipKinds[strings.ToLower(path.Ext(name))] || size < webGzipMin {
		return false
	}
	// the answer differs by what the browser takes, for any cache on the way
	w.Header().Add("Vary", "Accept-Encoding")
	if !acceptsGzip(r) {
		return false
	}
	key := name + "|" + strconv.FormatInt(size, 10) + "|" + strconv.FormatInt(mtime.UnixNano(), 10)
	v, _ := webGzipCache.LoadOrStore(key, &webGzipped{})
	e := v.(*webGzipped)
	e.once.Do(func() {
		raw, err := b()
		if err != nil {
			return
		}
		var out bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&out, gzip.BestCompression)
		zw.Write(raw)
		zw.Close()
		if out.Len() < len(raw) {
			e.gz = out.Bytes()
		}
	})
	if e.gz == nil {
		return false
	}
	h := w.Header()
	if !mtime.IsZero() {
		h.Set("Last-Modified", mtime.UTC().Format(http.TimeFormat))
		if t, err := http.ParseTime(r.Header.Get("If-Modified-Since")); err == nil && !mtime.Truncate(time.Second).After(t) {
			w.WriteHeader(http.StatusNotModified)
			return true
		}
	}
	h.Set("Content-Encoding", "gzip")
	h.Set("Content-Length", strconv.Itoa(len(e.gz)))
	if r.Method == http.MethodHead {
		return true
	}
	w.Write(e.gz)
	return true
}

// readAllFile reads a web file that is already open (for serveWebGzip)
func readAllFile(f fs.File) func() ([]byte, error) {
	return func() ([]byte, error) {
		if s, ok := f.(io.Seeker); ok {
			if _, err := s.Seek(0, io.SeekStart); err != nil {
				return nil, err
			}
		}
		return io.ReadAll(f)
	}
}
