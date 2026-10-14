// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"testing/fstest"
	"time"
)

// Export ▸ Link: light player / video on a server of one's own: the
// recording kept with the deck gets a link of its own at /r/{link}/{hash},
// which plays that recording only, for its days, and never opens the deck.
func TestLocalPlayLinks(t *testing.T) {
	dir := t.TempDir()
	web := fstest.MapFS{
		"index.html": {Data: []byte("<html><head><title>x</title></head><body></body></html>")},
		"play.html":  {Data: []byte("<html><head><title>player</title></head><body>play</body></html>")},
	}
	srv := httptest.NewUnstartedServer(nil)
	base := "http://" + srv.Listener.Addr().String()
	e, bucket, err := localEnv(dir, base, "local")
	if err != nil {
		t.Fatal(err)
	}
	e.Client = fakeNet
	srv.Config.Handler = newLocalServer(e, bucket, "", web)
	srv.Start()
	defer srv.Close()

	code, body := req(t, "POST", srv.URL+"/api/shares", "application/json", `{"name":"Rec deck","md":"# One\n\n## Two\n","theme":"aurora","css":null}`)
	eq(t, code, 201)
	var made map[string]string
	json.Unmarshal([]byte(body), &made)
	id := made["id"]

	hash := "0123456789abcdef01234567"
	path := "recordings/web-" + hash + ".sliqrec"
	data := string([]byte{0x1f, 0x8b, 8, 0, 1, 2, 3})
	vhash := "89abcdef0123456789abcdef"
	vpath := "recordings/web-" + vhash + ".webm"
	vdata := string([]byte{0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3})
	var files []any
	for _, f := range [][3]string{{path, "application/vnd.sliqtly.rec+gzip", data}, {vpath, "video/webm", vdata}} {
		code, body = req(t, "PUT", srv.URL+"/api/files/shares/"+id+"/"+f[0], f[1], f[2])
		eq(t, code, 200, f[0])
		var kept map[string]any
		json.Unmarshal([]byte(body), &kept)
		files = append(files, kept)
	}
	fj, _ := json.Marshal(files)
	code, _ = req(t, "PATCH", srv.URL+"/api/shares/"+id, "application/json", `{"files":`+string(fj)+`}`)
	eq(t, code, 200)

	link := func(file string, days int) string {
		t.Helper()
		code, body := req(t, "POST", srv.URL+"/api/shares/"+id+"/links", "application/json", `{"days":`+itoa(int64(days))+`,"file":"`+file+`"}`)
		eq(t, code, 200, body)
		var out map[string]string
		json.Unmarshal([]byte(body), &out)
		return out["id"]
	}
	rl := link(path, 7)
	match(t, rl, `^[0-9a-f]{32}$`)
	eq(t, link(path, 1), rl, "the same recording keeps its link, with a new term")
	vl := link(vpath, 30)
	eq(t, vl != rl, true, "each recording its own link")

	// only a recording of the deck's own
	code, _ = req(t, "POST", srv.URL+"/api/shares/"+id+"/links", "application/json", `{"days":7,"file":"recordings/web-ffffffffffffffffffffffff.webm"}`)
	eq(t, code, 404)
	code, _ = req(t, "POST", srv.URL+"/api/shares/"+id+"/links", "application/json", `{"days":7,"file":"deck.md"}`)
	eq(t, code, 400)
	// a page elsewhere makes none
	r, _ := http.NewRequest("POST", srv.URL+"/api/shares/"+id+"/links", nil)
	r.Header.Set("Origin", "https://evil.test")
	r.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	eq(t, res.StatusCode, 403)

	// File → Shared links… lists both recordings, each with its link
	code, body = req(t, "GET", srv.URL+"/api/published", "", "")
	eq(t, code, 200, body)
	var pub struct {
		Shares []struct {
			ID, Name, Visibility string
			Files                []struct {
				Path     string
				Size, At int64
			}
		}
		Links []struct {
			ID, Of, File string
			Days         int64
		}
	}
	json.Unmarshal([]byte(body), &pub)
	eq(t, len(pub.Shares), 1)
	eq(t, []any{pub.Shares[0].ID, pub.Shares[0].Name, pub.Shares[0].Visibility, len(pub.Shares[0].Files)}, []any{id, "Rec deck", "private", 2})
	eq(t, []any{pub.Shares[0].Files[0].Path, pub.Shares[0].Files[0].Size, pub.Shares[0].Files[0].At > 0}, []any{path, int64(len(data)), true})
	got := map[string]string{}
	for _, l := range pub.Links {
		eq(t, l.Of, id)
		got[l.File] = l.ID
	}
	eq(t, got, map[string]string{path: rl, vpath: vl})

	// the player's page and its recording
	code, ct, page := get(t, srv.URL+"/r/"+rl+"/"+hash)
	eq(t, []any{code, ct, page}, []any{200, "text/html; charset=utf-8", "<html><head><title>player</title></head><body>play</body></html>"})
	code, ct, gotRec := get(t, srv.URL+"/api/view/"+rl+"/rec/"+hash)
	eq(t, []any{code, ct, gotRec == data}, []any{200, "application/octet-stream", true})
	code, ct, gotRec = get(t, srv.URL+"/api/view/"+vl+"/rec/"+vhash)
	eq(t, []any{code, ct, gotRec == vdata}, []any{200, "video/webm", true})
	// a link shows its own recording, never the other, nor the deck
	code, _, _ = get(t, srv.URL+"/api/view/"+rl+"/rec/"+vhash)
	eq(t, code, 404)
	code, _, _ = get(t, srv.URL+"/api/view/"+rl)
	eq(t, code, 404)
	code, _ = req(t, "GET", srv.URL+"/api/shares/"+rl, "", "")
	eq(t, code, 404)

	// past its term it shows nothing, and is not handed out again
	ctx := context.Background()
	if err := e.DB.Update(ctx, "links", rl, Doc{"since": time.Now().Add(-8 * 24 * time.Hour).UnixMilli(), "days": int64(7)}); err != nil {
		t.Fatal(err)
	}
	code, _, _ = get(t, srv.URL+"/api/view/"+rl+"/rec/"+hash)
	eq(t, code, 404)
	fresh := link(path, 7)
	eq(t, fresh != rl, true, "an expired link's address is not given out again")
	if d, _ := e.DB.Get(ctx, "links", rl); d != nil {
		t.Fatal("the expired link is still kept")
	}

	// a recording's links go with it, and the deck's with the deck
	code, _ = req(t, "DELETE", srv.URL+"/api/shares/"+id+"/links?file="+vpath, "", "")
	eq(t, code, 200)
	code, _, _ = get(t, srv.URL+"/api/view/"+vl+"/rec/"+vhash)
	eq(t, code, 404)
	code, _ = req(t, "DELETE", srv.URL+"/api/shares/"+id, "", "")
	eq(t, code, 204)
	if d, _ := e.DB.Get(ctx, "links", fresh); d != nil {
		t.Fatal("the deck's link outlived it")
	}

	// the page's sliqtly.js makes the links here
	_, _, js := get(t, srv.URL+"/sliqtly.js?v=1")
	match(t, js, regexp.QuoteMeta("local: true, links: true, viewLink, removeFileLinks, listPublished, removeShareFile"))
}
