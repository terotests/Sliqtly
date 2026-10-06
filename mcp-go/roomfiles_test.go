// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// A room's own files: put in from the page, listed for the page and the
// assistant, shown with a message, read back at their address, gone with
// the room.
func TestRoomFiles(t *testing.T) {
	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	s := &testServer{root: srv.URL, session: session}
	ok := func(name string, args map[string]any) map[string]any {
		t.Helper()
		r := call(t, s, name, args)
		if r.IsError {
			t.Fatalf("%s: %s", name, textOf(r))
		}
		return sc(r)
	}
	bad := func(name string, args map[string]any, want string) {
		t.Helper()
		r := call(t, s, name, args)
		if !r.IsError {
			t.Fatalf("%s worked: %s", name, textOf(r))
		}
		match(t, textOf(r), want)
	}
	room := ok("create_room", map[string]any{"title": "Files"})["room_id"].(string)
	put := func(name, ct, body string, unique bool) (int, map[string]any) {
		t.Helper()
		u := srv.URL + "/api/files/rooms/" + room + "/" + url.PathEscape(name)
		if unique {
			u += "?unique=1"
		}
		code, b := req(t, "PUT", u, ct, body)
		var m map[string]any
		json.Unmarshal([]byte(b), &m)
		return code, m
	}

	code, f := put("plan v2.png", "image/png", "PNGDATA", true)
	eq(t, code, 201)
	eq(t, []any{f["name"], f["type"], f["size"]}, []any{"plan v2.png", "image/png", 7.0})
	match(t, f["url"].(string), `/files/rooms/`+room+`/files/plan%20v2.png$`)
	// the same name again: another name, unless it is meant to replace
	_, f2 := put("plan v2.png", "image/png", "OTHER", true)
	eq(t, f2["name"], "plan v2 (2).png")
	_, f3 := put("../../notes.txt", "text/plain", "hello", false)
	eq(t, f3["name"], "notes.txt")
	code, _ = put("x.txt", "text/plain", "x", false)
	eq(t, code, 201)
	code, _ = req(t, "PUT", srv.URL+"/api/files/rooms/nothere/x.txt", "text/plain", "x")
	eq(t, code, 404)

	// read back at its address, as a file of no origin
	res, err := http.Get(f["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	eq(t, []any{res.StatusCode, res.Header.Get("Content-Type"), res.Header.Get("Content-Security-Policy") != ""}, []any{200, "image/png", true})

	// listed for the assistant and the page
	files := list(ok("list_room_files", map[string]any{"room_id": room})["files"])
	var names []string
	for _, x := range files {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	eq(t, strings.Join(names, ","), "notes.txt,plan v2 (2).png,plan v2.png,x.txt")
	code, pf := pagePost(t, srv.URL, "list_room_files", map[string]any{"room_id": room})
	eq(t, []any{code, len(list(pf["files"]))}, []any{200, 4})

	// a message shows them: a picture with its size, the assistant's by name
	ada := map[string]any{"id": "ada-browser-1", "name": "Ada"}
	code, out := pagePost(t, srv.URL, "post_room_message", map[string]any{"room_id": room, "as": ada, "files": []any{map[string]any{"name": "plan v2.png", "w": 640, "h": 480}, "notes.txt"}})
	eq(t, code, 200)
	ok("post_room_message", map[string]any{"room_id": room, "text": "The notes", "agent": "Claude", "files": []any{"notes.txt"}})
	bad("post_room_message", map[string]any{"room_id": room, "text": "x", "files": []any{"nope.txt"}}, `no file called "nope.txt"`)
	msgs := list(ok("read_room_chat", map[string]any{"room_id": room})["messages"])
	eq(t, len(msgs), 2)
	first := msgs[0].(map[string]any)
	eq(t, first["id"], out["message_id"])
	fs := list(first["files"])
	pic, doc := fs[0].(map[string]any), fs[1].(map[string]any)
	eq(t, []any{pic["name"], pic["type"], pic["w"], pic["h"], doc["name"], doc["w"]}, []any{"plan v2.png", "image/png", 640.0, 480.0, "notes.txt", nil})
	match(t, pic["url"].(string), `/files/rooms/`+room+`/files/plan%20v2.png$`)

	// the room's files go with the room
	ok("delete_room", map[string]any{"room_id": room})
	res, err = http.Get(f["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	eq(t, res.StatusCode, 404)
}

func TestRoomFileNames(t *testing.T) {
	for in, want := range map[string]string{
		"a.png":                           "a.png",
		"../../etc/passwd":                "passwd",
		"c:\\x\\y.txt":                    "y.txt",
		"tab\there.txt":                   "tabhere.txt",
		"  spaced  ":                      "spaced",
		"..":                              "",
		"/":                               "",
		strings.Repeat("ä", 200) + ".pdf": strings.Repeat("ä", 116) + ".pdf",
	} {
		if got := roomFileName(in); got != want {
			t.Errorf("roomFileName(%q) = %q, want %q", in, got, want)
		}
	}
	taken := map[string]bool{"a.png": true, "a (2).png": true, "README": true}
	eq(t, []any{uniqueFileName("a.png", taken), uniqueFileName("b.png", taken), uniqueFileName("README", taken)}, []any{"a (3).png", "b.png", "README (2)"})
}

func TestPreviewLinks(t *testing.T) {
	text := "See https://example.com/a, and (https://x.org/b).\n`https://code.example` no\n```\nhttps://fenced.example\n```\nhttps://example.com/a again https://own.host/s/abc https://c.io https://d.io"
	eq(t, strings.Join(previewLinks(text, "https://own.host"), " "), "https://example.com/a https://x.org/b https://c.io")
}

func TestParsePreview(t *testing.T) {
	site, title, desc := parsePreview(`<html><head><title>Plain &amp; title</title>
<meta property="og:site_name" content="Example">
<meta name="description" content='The  page,
 in short'>
<meta content="OG title" property="og:title"></head></html>`)
	eq(t, []any{site, title, desc}, []any{"Example", "OG title", "The page, in short"})
	_, title, desc = parsePreview(`<title>Only a title</title>`)
	eq(t, []any{title, desc}, []any{"Only a title", ""})
	_, _, desc = parsePreview(`<meta name="description" content="` + strings.Repeat("w", 400) + `">`)
	eq(t, len([]rune(desc)), 300)
}

// a posted message's links are read and kept with it, and the pages told
func TestLinkPreviewsKept(t *testing.T) {
	pages := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/doc":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(`<title>The retry plan</title><meta name="description" content="Three tries, then a person.">`))
		case "/bin":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte("<title>no</title>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer pages.Close()
	var told []map[string]any
	s := &roomService{chat: store.NewMemChat(), client: pages.Client(), notify: func(_ string, v map[string]any) { told = append(told, v) }}
	ctx := context.Background()
	text := "plan: " + pages.URL + "/doc and " + pages.URL + "/bin and " + pages.URL + "/missing"
	m, err := s.chat.Append(ctx, store.ChatMsg{Tenant: "t", Room: "r", From: store.ChatFrom{ID: "p-a"}, Text: text})
	if err != nil {
		t.Fatal(err)
	}
	s.previews("t", "r", m.ID, text)
	got, _ := s.chat.Get(ctx, "t", "r", m.ID)
	eq(t, len(got.Links), 1)
	l := got.Links[0]
	eq(t, []any{l.URL, l.Title, l.Desc, l.Site}, []any{pages.URL + "/doc", "The retry plan", "Three tries, then a person.", "127.0.0.1"})
	eq(t, len(told), 1)
	// a message changed meanwhile keeps its new text and no stale preview
	m2, _ := s.chat.Append(ctx, store.ChatMsg{Tenant: "t", Room: "r", From: store.ChatFrom{ID: "p-a"}, Text: "now " + pages.URL + "/doc"})
	s.previews("t", "r", m2.ID, "before "+pages.URL+"/doc")
	got2, _ := s.chat.Get(ctx, "t", "r", m2.ID)
	eq(t, len(got2.Links), 0)
}

// a room made (or renamed, archived, removed) is told to every open page, so
// their lists show it without a reload
func TestRoomListEvents(t *testing.T) {
	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	res, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	ts := &testServer{root: srv.URL, session: session}
	r := call(t, ts, "create_room", map[string]any{"title": "Bob's room"})
	if r.IsError {
		t.Fatal(textOf(r))
	}
	wait := func(want string) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatal("the stream ended")
				}
				if l == want {
					return
				}
			case <-deadline:
				t.Fatal("no event " + want)
			}
		}
	}
	wait(`data: {"room":"","t":"rooms"}`)
	// described again: the room's open chats show it at once
	room := sc(r)["room_id"].(string)
	if r := call(t, ts, "update_room", map[string]any{"room_id": room, "description": "Retry, then a person"}); r.IsError {
		t.Fatal(textOf(r))
	}
	wait(`data: {"description":"Retry, then a person","room":"` + room + `","t":"room"}`)
}
