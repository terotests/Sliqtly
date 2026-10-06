// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// Export downloads on the site's own address. export_presentation keeps the
// file in Storage (shares/<id>/exports/<name>), but the link it gives the
// user is <BaseURL>/d/<token>/<name>: a link on sliqtly.com, not on
// firebasestorage.googleapis.com. The token is the one the upload was given;
// mcp_downloads/<token> says which object it is. Firebase Hosting sends /d/**
// here (firebase.json).

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// an export is read whole before it is sent, up to this
const maxDownload = 64 << 20

// how long a download link works; mcp_downloads/<token> carries `expires`
// for Firestore's TTL policy (firestore.indexes.json) and `exp` (ms) for
// serveDownload, which refuses the link once it has passed
const downloadTTL = 24 * time.Hour

func envNow(env *Env) time.Time {
	if env.Now != nil {
		return env.Now()
	}
	return time.Now()
}

// the ms a download record stops working at: `exp`, or (a link made before
// links expired) `at` + downloadTTL
func downloadExp(d Doc) int64 {
	for _, k := range []string{"exp", "at"} {
		var ms int64
		switch v := d[k].(type) {
		case int64:
			ms = v
		case int:
			ms = int64(v)
		case float64:
			ms = int64(v)
		default:
			continue
		}
		if k == "at" {
			ms += downloadTTL.Milliseconds()
		}
		return ms
	}
	return 0
}

// the link for a kept export: the server's own /files/ on a server of one's
// own, else /d/<token>/<name> with the object recorded under the token
func (h *McpHost) DownloadURL(path, token, contentType, name string) string {
	if h.env.FilesURL != "" || h.env.DB == nil {
		return h.FileURL(path, token)
	}
	now := envNow(h.env)
	exp := now.Add(downloadTTL)
	if err := h.env.DB.Set(h.ctx, "mcp_downloads", token, Doc{
		"path": path, "type": contentType, "name": name, "at": now.UnixMilli(),
		"exp": exp.UnixMilli(), "expires": exp.UTC(),
	}); err != nil {
		h.fail(err)
		return ""
	}
	return strings.TrimRight(h.env.BaseURL, "/") + "/d/" + token + "/" + url.PathEscape(name)
}

// GET /d/<token>/<name>: the export, as an attachment
func serveDownload(env *Env, w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/d/")
	token, _, _ := strings.Cut(rest, "/")
	if env.DB == nil || env.Bucket == nil || !downloadToken(token) {
		http.NotFound(w, r)
		return
	}
	d, err := env.DB.Get(r.Context(), "mcp_downloads", token)
	if err != nil || d == nil {
		http.NotFound(w, r)
		return
	}
	if envNow(env).UnixMilli() >= downloadExp(d) {
		http.Error(w, "This download link has expired; export the presentation again.", http.StatusGone)
		return
	}
	path, _ := d["path"].(string)
	ct, _ := d["type"].(string)
	name, _ := d["name"].(string)
	if !strings.HasPrefix(path, "shares/") || !strings.Contains(path, "/exports/") {
		http.NotFound(w, r)
		return
	}
	data, err := env.Bucket.Read(r.Context(), path, maxDownload+1)
	if err != nil {
		http.Error(w, "This file is no longer here; export the presentation again.", http.StatusNotFound)
		return
	}
	if len(data) > maxDownload {
		http.Error(w, "The file is too large to send.", http.StatusRequestEntityTooLarge)
		return
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	// the next export of the same format replaces the file behind the link
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		return
	}
	w.Write(data)
}

// the tokens UUID() makes: hex and dashes
func downloadToken(s string) bool {
	if len(s) < 16 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == '-') {
			return false
		}
	}
	return true
}
