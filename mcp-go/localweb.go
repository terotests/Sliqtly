// SPDX-License-Identifier: AGPL-3.0-or-later

// The editor and player (web/dist) on a server of one's own, and the API
// that web/sliqtly.js's stand-in (assets/sliqtly-local.js) keeps decks with.
// The page is the one sliqtly.com serves; only /sliqtly.js differs, so the
// deck is read from and saved to this server's folder instead of Firebase.
//
// The web files come from -web (a built web/dist) or, when the binary was
// built after `npm run build` and `go generate`, from the copy in webdist/.

package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
)

//go:embed all:webdist
var webdistFiles embed.FS

//go:embed assets/sliqtly-local.js
var sliqtlyLocalJS string

// the built page, from dir or the copy built in; nil when there is none
func webFiles(dir string) fs.FS {
	if dir != "" {
		return os.DirFS(dir)
	}
	sub, err := fs.Sub(webdistFiles, "webdist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

// the public viewer's build (web/dist-view, npm run build:view) rather
// than the editor's: it plays presentations and has no editor, so the
// server's front page is its list of decks
func viewerOnly(web fs.FS) bool {
	if web == nil {
		return false
	}
	_, err := fs.Stat(web, "view.js")
	_, editor := fs.Stat(web, "pres_app.js")
	return err == nil && editor != nil
}

// --- the page

var (
	shareAPIPath = regexp.MustCompile(`^/api/shares/([A-Za-z0-9]{6,32})(/head)?$`)
	shareID      = regexp.MustCompile(`^[A-Za-z0-9]{6,32}$`)
)

// the page with this server's address for the links it hands out
func (s *localServer) page(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(s.web, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	meta := `<meta name="sliqtly-site" content="` + htmlAttr(s.env.BaseURL) + `" />`
	html := strings.Replace(string(b), "</head>", meta+"\n</head>", 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Private-Network", "true")
	io.WriteString(w, html)
}

func htmlAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// a file of the page; false when there is no such file
func (s *localServer) static(w http.ResponseWriter, r *http.Request) bool {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "index.html" {
		return false
	}
	// the page's faces are the ones the server lays slides out with
	// (fonts.go): one copy in the binary, not two
	if strings.HasPrefix(name, "fonts/") {
		if b, err := fontFiles.ReadFile(name); err == nil {
			w.Header().Set("Content-Type", "font/ttf")
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Write(b)
			return true
		}
	}
	if name == "sliqtly.js" {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		io.WriteString(w, sliqtlyLocalJS)
		return true
	}
	f, err := s.web.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		return false
	}
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// every URL the page loads carries the build's hash (?v=…)
	if r.URL.RawQuery != "" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	// the assistant's preview fetches the page's files from its own origin
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Private-Network", "true")
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, name, st.ModTime(), rs)
		return true
	}
	io.Copy(w, f)
	return true
}

// --- the API

type apiError struct {
	status int
	code   string
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func fail(status int, code, msg string) error { return &apiError{status, code, msg} }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func newShareID() string {
	const abc = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 10)
	rand.Read(b)
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return string(b)
}

// one change to a share at a time: read, check, write. Each share has its
// own turn, so an upload to one deck or a room saving its text holds up
// no other deck.
var shareLocks = keyedLocks{m: map[string]*keyedLock{}}

type keyedLocks struct {
	mu sync.Mutex
	m  map[string]*keyedLock
}

type keyedLock struct {
	sync.Mutex
	users int
}

// lock takes the share's turn; the func it returns gives it back
func (k *keyedLocks) lock(id string) func() {
	k.mu.Lock()
	l := k.m[id]
	if l == nil {
		l = &keyedLock{}
		k.m[id] = l
	}
	l.users++
	k.mu.Unlock()
	l.Lock()
	return func() {
		l.Unlock()
		k.mu.Lock()
		if l.users--; l.users == 0 {
			delete(k.m, id)
		}
		k.mu.Unlock()
	}
}

const maxUpload = 20 << 20
const logMax = 300

func (s *localServer) api(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	// the reads are open to the assistant's preview, which runs on another
	// origin (and may ask first, as a page on the internet asking a private
	// address does)
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
		w.WriteHeader(204)
		return
	}
	if r.Method == http.MethodGet {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
	}
	// a write is JSON or a file, never a form: a page elsewhere cannot send
	// one without asking first, and is not answered
	if r.Method == http.MethodPost || r.Method == http.MethodPatch {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeJSON(w, 415, map[string]string{"error": "send JSON"})
			return
		}
	}
	var out any
	var err error
	status := 200
	switch {
	case p == "/api/events" && r.Method == http.MethodGet:
		s.events(w, r)
		return
	case p == "/api/socket" && r.Method == http.MethodGet:
		s.socket(w, r)
		return
	case collabPath.MatchString(p):
		m := collabPath.FindStringSubmatch(p)
		out, err = s.collabAPI(r, m[1], m[2])
	case p == "/api/me" && r.Method == http.MethodGet:
		out = map[string]string{"uid": s.env.LocalUser, "name": s.env.LocalUser}
	case p == "/api/shares" && r.Method == http.MethodGet:
		out, err = s.listShares(r.Context())
	case p == "/api/shares" && r.Method == http.MethodPost:
		out, err = s.createShare(r)
		status = 201
	case strings.HasPrefix(p, "/api/rooms/") && r.Method == http.MethodPost:
		out, err = s.roomsAPI(r, strings.TrimPrefix(p, "/api/rooms/"))
	case strings.HasPrefix(p, "/api/files/rooms/") && r.Method == http.MethodPut:
		out, err = s.roomFileAPI(r, strings.TrimPrefix(p, "/api/files/rooms/"))
		status = 201
	case strings.HasPrefix(p, "/api/files/shares/"):
		out, err = s.fileAPI(r, strings.TrimPrefix(p, "/api/files/"))
	case shareAPIPath.MatchString(p):
		m := shareAPIPath.FindStringSubmatch(p)
		id := m[1]
		switch {
		case m[2] == "/head" && r.Method == http.MethodPost:
			out, err = s.pushHead(r, id)
		case m[2] != "":
			err = fail(405, "", "method not allowed")
		case r.Method == http.MethodGet:
			out, err = s.getShare(r.Context(), id)
		case r.Method == http.MethodPatch:
			out, err = s.patchShare(r, id)
		case r.Method == http.MethodDelete:
			err = s.deleteShare(r.Context(), id)
			status = 204
		default:
			err = fail(405, "", "method not allowed")
		}
	default:
		err = fail(404, "", "not found")
	}
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) {
			writeJSON(w, ae.status, map[string]string{"error": ae.msg, "code": ae.code})
		} else {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
		}
		return
	}
	if status == 204 {
		w.WriteHeader(204)
		return
	}
	writeJSON(w, status, out)
}

func (s *localServer) listShares(ctx context.Context) (any, error) {
	docs, ids, err := s.env.DB.WhereEq(ctx, "shares", "owner", s.env.LocalUser)
	if err != nil {
		return nil, err
	}
	type row struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Updated int64  `json:"updated"`
	}
	rows := []row{}
	for i, d := range docs {
		at := millis(d["updated"])
		if at == 0 {
			at = millis(d["created"])
		}
		name, _ := d["name"].(string)
		rows = append(rows, row{ids[i], name, at})
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].Updated > rows[b].Updated })
	return rows, nil
}

func (s *localServer) getShare(ctx context.Context, id string) (any, error) {
	d, err := s.env.DB.Get(ctx, "shares", id)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, fail(404, "not-found", "no such presentation")
	}
	return plain(d), nil
}

func readBody(r *http.Request) (map[string]any, error) {
	var body map[string]any
	dec := json.NewDecoder(io.LimitReader(r.Body, maxUpload))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil || body == nil {
		return nil, fail(400, "", "a JSON object is expected")
	}
	return decodeValue(body).(map[string]any), nil
}

// the fields the page sets on a share
func shareFields(body map[string]any) Doc {
	d := Doc{}
	for _, k := range []string{"name", "md", "theme"} {
		if v, ok := body[k].(string); ok {
			d[k] = v
		}
	}
	if v, ok := body["css"]; ok {
		if css, isStr := v.(string); isStr {
			d["css"] = css
		} else {
			d["css"] = nil
		}
	}
	if files, ok := body["files"].([]any); ok {
		d["files"] = files
	}
	return d
}

func (s *localServer) createShare(r *http.Request) (any, error) {
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	d := shareFields(body)
	d["owner"] = s.env.LocalUser
	d["source"] = "web"
	if deck, ok := body["deck"].(string); ok {
		d["deck"] = deck
	}
	if _, ok := d["files"]; !ok {
		d["files"] = []any{}
	}
	d["created"] = time.Now().UTC()
	// Create writes only where there is nothing: a new id needs no turn
	for range 5 {
		id := newShareID()
		had, err := s.env.DB.Create(r.Context(), "shares", id, d)
		if err != nil {
			return nil, err
		}
		if had == nil {
			return map[string]string{"id": id}, nil
		}
	}
	return nil, errors.New("no free id")
}

// the share, read for a change: there, and this user's
func (s *localServer) own(ctx context.Context, id string) (Doc, error) {
	cur, err := s.env.DB.Get(ctx, "shares", id)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, fail(404, "not-found", "no such presentation")
	}
	if cur["owner"] != s.env.LocalUser {
		return nil, fail(403, "permission-denied", "not the owner")
	}
	return cur, nil
}

func (s *localServer) patchShare(r *http.Request, id string) (any, error) {
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	defer shareLocks.lock(id)()
	cur, err := s.own(r.Context(), id)
	if err != nil {
		return nil, err
	}
	// changed since the page last read it: an assistant saved meanwhile
	if want, ok := body["ifMd"].(string); ok {
		if have, _ := cur["md"].(string); have != want {
			return nil, fail(409, "changed-elsewhere", "changed elsewhere")
		}
	}
	patch := shareFields(body)
	patch["updated"] = time.Now().UTC()
	if err := s.env.DB.Update(r.Context(), "shares", id, patch); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func (s *localServer) deleteShare(ctx context.Context, id string) error {
	defer shareLocks.lock(id)()
	if _, err := s.own(ctx, id); err != nil {
		return err
	}
	// the records first: a deck whose files went but whose record did not
	// would point at nothing, while files left behind by a failed removal
	// are swept as named by nothing (sweepExpired)
	if err := s.env.DB.Delete(ctx, "mcp_keys", id); err != nil {
		return err
	}
	if err := s.env.DB.Delete(ctx, "shares", id); err != nil {
		return err
	}
	if err := s.bucket.RemoveAll("shares/" + id); err != nil {
		log.Printf("deck %s removed, its files not yet: %v", id, err)
	}
	return nil
}

// web/sliqtly.js pushHead: the head moves from expect to head, with the
// new log entries, only while it is still expect
func (s *localServer) pushHead(r *http.Request, id string) (any, error) {
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	head, _ := body["head"].(string)
	expect, _ := body["expect"].(string)
	entries, _ := body["entries"].([]any)
	defer shareLocks.lock(id)()
	cur, err := s.own(r.Context(), id)
	if err != nil {
		return nil, err
	}
	now, _ := cur["head"].(string)
	log, _ := cur["log"].([]any)
	if log == nil {
		log = []any{}
	}
	if now != expect && now != head {
		return map[string]any{"ok": false, "head": nullable(now), "log": plain(log)}, nil
	}
	seen := map[string]bool{}
	for _, e := range log {
		if m, ok := e.(map[string]any); ok {
			if id, ok := m["id"].(string); ok {
				seen[id] = true
			}
		}
	}
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := m["id"].(string); id != "" && seen[id] {
			continue
		}
		log = append(log, e)
	}
	if len(log) > logMax {
		log = log[len(log)-logMax:]
	}
	if err := s.env.DB.Update(r.Context(), "shares", id, Doc{"head": head, "log": log}); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "head": head, "log": plain(log)}, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// PUT and DELETE /api/files/shares/{id}/{path}
func (s *localServer) fileAPI(r *http.Request, name string) (any, error) {
	parts := strings.SplitN(name, "/", 3)
	// the file stays inside its share: no "..", nothing to clean away
	if len(parts) < 3 || !shareID.MatchString(parts[1]) || parts[2] == "" || path.Clean(parts[2]) != parts[2] ||
		strings.HasPrefix(parts[2], "../") || parts[2] == ".." || strings.HasSuffix(parts[2], ".type") {
		return nil, fail(400, "", "bad file path")
	}
	id, rel := parts[1], parts[2]
	defer shareLocks.lock(id)()
	if _, err := s.own(r.Context(), id); err != nil {
		return nil, err
	}
	switch r.Method {
	case http.MethodPut:
		data, err := io.ReadAll(io.LimitReader(r.Body, maxUpload+1))
		if err != nil {
			return nil, err
		}
		if len(data) > maxUpload {
			return nil, fail(413, "", "a file is at most 20 MB")
		}
		ct := r.Header.Get("Content-Type")
		if ct == "" {
			ct = "application/octet-stream"
		}
		if err := s.bucket.Save(r.Context(), name, ct, data, nil); err != nil {
			return nil, fail(400, "", err.Error())
		}
		h := &McpHost{env: s.env}
		return map[string]any{"path": rel, "type": ct, "size": len(data), "url": h.FileURL(name, "")}, nil
	case http.MethodDelete:
		if err := s.bucket.Remove(name); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	}
	return nil, fail(405, "", "method not allowed")
}

// PUT /api/files/rooms/{room}/{name}: a file into the room's files
// (roomfiles.go); ?unique=1 gives it another name when the room has one
// by it
func (s *localServer) roomFileAPI(r *http.Request, rest string) (any, error) {
	if s.env.rooms == nil {
		return nil, fail(404, "", "not found")
	}
	room, name, ok := strings.Cut(rest, "/")
	if !ok || name == "" {
		return nil, fail(400, "", "bad file path")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxUpload+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxUpload {
		return nil, fail(413, "", "a file is at most 20 MB")
	}
	out, err := s.env.rooms.putFile(r.Context(), s.env.LocalUser, room, name, r.Header.Get("Content-Type"), data, r.URL.Query().Get("unique") == "1")
	var re roomErr
	switch {
	case errors.As(err, &re):
		return nil, fail(400, "", re.msg)
	case errors.Is(err, store.ErrNotFound):
		return nil, fail(404, "", "no such room")
	}
	return out, err
}

// what has expired goes, as Firestore's TTL policies do there: at start and
// then every hour until ctx ends. An expired deck's files go with it.
func (s *localServer) sweepExpired(ctx context.Context) {
	if s.env.Store == nil {
		return
	}
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		s.sweepOnce(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *localServer) sweepOnce(t time.Time) {
	for _, col := range ttlCollections {
		ids, err := sweepExpired(context.Background(), s.env.Store, col, t)
		if err != nil {
			log.Printf("expired %s: %v", col, err)
		}
		if col != "shares" {
			continue
		}
		for _, id := range ids {
			unlock := shareLocks.lock(id)
			if err := s.bucket.RemoveAll("shares/" + id); err != nil {
				log.Printf("expired deck %s: %v", id, err)
			}
			unlock()
		}
	}
	// the bytes of files nothing names any more (removed, replaced, or of
	// a deck that went)
	if n, err := s.bucket.collectBlobs(context.Background(), t); err != nil {
		log.Printf("unused files: %v", err)
	} else if n > 0 {
		log.Printf("removed %d unused files", n)
	}
}

// POST /api/rooms/<op> with the operation's arguments: the same operations
// as the assistant's room tools (roomsapi.go), for the page's own user. A
// POST of JSON only, so a page elsewhere can neither send one unasked nor
// read the answer.
func (s *localServer) roomsAPI(r *http.Request, op string) (any, error) {
	if s.env.rooms == nil || !findRoomTool(op) {
		return nil, fail(404, "", "not found")
	}
	a := map[string]any{}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&a); err != nil && !errors.Is(err, io.EOF) {
		return nil, fail(400, "", "a JSON object is expected")
	}
	out, err := s.env.rooms.callVia(r.Context(), s.env.LocalUser, viaPage, op, a)
	var re roomErr
	if errors.As(err, &re) {
		return nil, fail(400, "", re.msg)
	}
	return out, err
}
