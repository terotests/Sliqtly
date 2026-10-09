// SPDX-License-Identifier: AGPL-3.0-or-later

// The editor on sliqtly.com, at /editor, for signed-in people only.
//
// The public site is the viewer (scripts/build-view.mjs): the editor's code
// is not among Hosting's files. firebase.json rewrites /editor and
// /editor/** here, and this server sends the editor's page and every file of
// it (web/dist, built into the binary as webdist/) only to a request that
// carries a valid Google sign-in. Anyone else gets the sign-in page, which
// holds no editor code. Hiding the editor behind a sign-in screen in the
// browser would not do: the files would still be on the public site.
//
// The sign-in travels as the cookie __session, the only cookie Firebase
// Hosting passes on to Cloud Run. It holds the page's Firebase ID token
// (an hour long, verified here as /main/admin verifies its bearer token);
// the page sends a fresh one before it runs out (web/sliqtly.js), and the
// sign-in page gets one again without a press while Firebase still knows
// the user. Responses are private: the CDN keeps none of them.
//
//	POST /editor/api/session  {idToken}  sets the cookie (the sign-in page, the editor)
//	POST /editor/api/signout             clears it
//	GET  /editor/api/license             the signed-in user's license
//	POST /editor/api/claim    {id}       a presentation taken under the license
//
// Licenses: licenses/{uid} in Firestore, written only here and by the
// owner in the Firebase console (firestore.rules lets a user read their
// own). Made at the first visit as a Trial: two presentations. maxDocs -1
// is no limit; the accounts in SLIQTLY_ADMIN_EMAILS always have none.
// editUntil (a timestamp, optional) ends the right to change: a license
// never takes presentations away, it only stops changes to them in the
// cloud (firestore.rules checks the same document on every write of a
// share). `docs` are the share ids the license covers: a presentation is
// added to it when it is first saved to the cloud, while there is room.

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	editorPath     = "/editor"
	editorCookie   = "__session"
	editorTrialMax = 2
	licenseCol     = "licenses"
)

//go:embed assets/editor-login.html
var editorLoginHTML string

// ---------------------------------------------------------------- license --

// what a license lets its holder do, as licenses/{uid} says
type license struct {
	Plan      string    // trial, admin, or what the owner wrote
	MaxDocs   int       // presentations it covers; < 0: no limit
	Docs      []string  // the share ids it covers
	EditUntil time.Time // zero: no end
	Rev       string    // changes with every write here (claim)
}

// The license in a stored document; a missing one is a Trial. An admin's
// is always without limits, whatever the document says.
func licenseOf(d Doc, admin bool) license {
	l := license{Plan: "trial", MaxDocs: editorTrialMax}
	if d != nil {
		if s, ok := d["plan"].(string); ok && s != "" {
			l.Plan = s
		}
		if v, ok := d["maxDocs"]; ok && v != nil {
			l.MaxDocs = int(docInt(v))
		}
		if list, ok := d["docs"].([]any); ok {
			for _, x := range list {
				if s, ok := x.(string); ok && s != "" && !slices.Contains(l.Docs, s) {
					l.Docs = append(l.Docs, s)
				}
			}
		}
		l.EditUntil = docTime(d["editUntil"])
		l.Rev, _ = d["rev"].(string)
	}
	if admin {
		l.Plan, l.MaxDocs, l.EditUntil = "admin", -1, time.Time{}
	}
	return l
}

// a timestamp as Firestore gives it (time.Time) or as text (RFC 3339)
func docTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		if p, err := time.Parse(time.RFC3339, t); err == nil {
			return p
		}
	}
	return time.Time{}
}

func (l license) expired(now time.Time) bool {
	return !l.EditUntil.IsZero() && !now.Before(l.EditUntil)
}

// "" when the license lets its holder change presentation id now, else why not
func (l license) canEdit(id string, now time.Time) string {
	if l.expired(now) {
		return "expired"
	}
	if l.MaxDocs < 0 || slices.Contains(l.Docs, id) {
		return ""
	}
	return "full"
}

// The license with presentation id taken under it: the same when it
// covers id already; why not ("expired", "full") when it cannot.
func (l license) claim(id string, now time.Time) (license, bool, string) {
	if why := l.canEdit(id, now); why == "" {
		return l, false, ""
	} else if why == "expired" {
		return l, false, why
	}
	if len(l.Docs) >= l.MaxDocs {
		return l, false, "full"
	}
	l.Docs = append(slices.Clone(l.Docs), id)
	return l, true, ""
}

// the license as the page reads it
func (l license) json(now time.Time) map[string]any {
	out := map[string]any{"plan": l.Plan, "maxDocs": l.MaxDocs, "docs": l.Docs, "canEdit": !l.expired(now)}
	if l.Docs == nil {
		out["docs"] = []string{}
	}
	if !l.EditUntil.IsZero() {
		out["editUntil"] = l.EditUntil.UTC().Format(time.RFC3339)
	}
	return out
}

// ------------------------------------------------------------------- gate --

type editorGate struct {
	web    fs.FS    // web/dist
	admins []string // lower case
	db     DB
	verify func(ctx context.Context, idToken string) (*IDToken, error)
	now    func() time.Time
	limit  func(who string) string
}

// The gate, when there is an editor to serve and sign-in to check it with;
// nil otherwise (no /editor).
func newEditorGate(web fs.FS, db DB, verify func(ctx context.Context, idToken string) (*IDToken, error), admins []string) *editorGate {
	if web == nil || db == nil || verify == nil || viewerOnly(web) {
		return nil
	}
	if _, err := fs.Stat(web, "index.html"); err != nil {
		return nil
	}
	list := []string{}
	for _, e := range admins {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			list = append(list, e)
		}
	}
	return &editorGate{web: web, admins: list, db: db, verify: verify, now: time.Now, limit: rateLimiter(120, 10*time.Minute)}
}

func (g *editorGate) admin(t *IDToken) bool {
	return t != nil && t.Verified && slices.Contains(g.admins, strings.ToLower(t.Email))
}

// the user the request's cookie signs in, or nil
func (g *editorGate) user(r *http.Request) *IDToken {
	c, err := r.Cookie(editorCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	t, err := g.verify(r.Context(), c.Value)
	if err != nil || t == nil || t.UID == "" {
		return nil
	}
	return t
}

// The user's license, its document made (a Trial) when there is none, and
// who they are noted on it, so the owner finds them in the console.
func (g *editorGate) license(ctx context.Context, t *IDToken) (license, error) {
	d, err := g.db.Get(ctx, licenseCol, t.UID)
	if err != nil {
		return license{}, err
	}
	admin := g.admin(t)
	l := licenseOf(d, admin)
	seen := Doc{"email": t.Email, "name": t.Name, "seen": g.db.ServerTime()}
	if admin {
		seen["plan"], seen["maxDocs"] = "admin", int64(-1)
	}
	if d == nil {
		seen["plan"], seen["maxDocs"], seen["docs"], seen["created"], seen["rev"] = l.Plan, int64(l.MaxDocs), []any{}, g.db.ServerTime(), newShareID()
		if got, err := g.db.Create(ctx, licenseCol, t.UID, seen); err != nil {
			return license{}, err
		} else if got != nil {
			return licenseOf(got, admin), nil // made meanwhile by another request
		}
		return l, nil
	}
	// noted once a day at most: a page load is many requests
	if last := docTime(d["seen"]); admin && (d["plan"] != "admin" || docInt(d["maxDocs"]) != -1) || g.now().Sub(last) > 24*time.Hour {
		if err := g.db.Update(ctx, licenseCol, t.UID, seen); err != nil {
			log.Printf("editor: license not noted: %v", err)
		}
	}
	return l, nil
}

var errLicenseBusy = errors.New("the license changed meanwhile")

// invitedTo: the share's owner invited this user to edit it (`editors`,
// lower-case addresses; firestore.rules invited()), by a verified address
func invitedTo(share Doc, t *IDToken) bool {
	if !t.Verified || t.Email == "" {
		return false
	}
	list, _ := share["editors"].([]any)
	for _, x := range list {
		if s, ok := x.(string); ok && s == strings.ToLower(t.Email) {
			return true
		}
	}
	return false
}

// presentation id taken under the user's license; why not when it cannot.
// Someone else's presentation is not taken: "not-yours", unless its owner
// invited the user, who then edits it under a license that has not ended
// and spends none of its presentations on it.
func (g *editorGate) claim(ctx context.Context, t *IDToken, id string) (license, string, error) {
	share, err := g.db.Get(ctx, "shares", id)
	if err != nil {
		return license{}, "", err
	}
	if share != nil {
		if owner, _ := share["owner"].(string); owner != t.UID {
			l, err := g.license(ctx, t)
			if err != nil || !invitedTo(share, t) {
				return l, "not-yours", err
			}
			if l.expired(g.now()) {
				return l, "expired", nil
			}
			return l, "", nil
		}
	}
	for try := 0; try < 4; try++ {
		l, err := g.license(ctx, t)
		if err != nil {
			return l, "", err
		}
		next, changed, why := l.claim(id, g.now())
		if why != "" || !changed {
			return l, why, nil
		}
		docs := make([]any, len(next.Docs))
		for i, s := range next.Docs {
			docs[i] = s
		}
		ok, err := g.db.UpdateIf(ctx, licenseCol, t.UID, "rev", l.Rev, Doc{"docs": docs, "rev": newShareID()})
		if err != nil {
			return l, "", err
		}
		if ok {
			return next, "", nil
		}
	}
	return license{}, "", errLicenseBusy
}

// ------------------------------------------------------------------ route --

var editorDeck = regexp.MustCompile(`^/editor/[sd]/[A-Za-z0-9]{6,32}/?$`)

// a presentation's own address in the editor: /editor/d/{id}. It is never a
// shared link (that is /s/{linkId}, links/ in firestore.rules): only its
// owner and the people they invite open it. /editor/s/{id}?edit, the
// address before, leads there.
var editorOldEdit = regexp.MustCompile(`^/editor/s/([A-Za-z0-9]{6,32})/?$`)

// editDocPath: where the editor edits presentation id
func editDocPath(id string) string {
	return editorPath + "/d/" + id
}

// the query without "edit", which /editor/d/ says already
func withoutEdit(q url.Values) string {
	q.Del("edit")
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

// the editor's page: /editor/, or a presentation in it, /editor/d/{id}
func editorPage(p string) bool {
	return p == editorPath+"/" || p == editorPath+"/index.html" || editorDeck.MatchString(p)
}

func editorJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// the request comes from a page of this site (a form or script elsewhere
// cannot set or clear the sign-in)
func sameSite(env *Env, r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return r.Header.Get("Sec-Fetch-Site") == "same-origin"
	}
	if o == strings.TrimRight(env.BaseURL, "/") {
		return true
	}
	if h := r.Header.Get("X-Forwarded-Host"); h != "" && o == "https://"+h {
		return true
	}
	return o == "https://"+r.Host || o == "http://"+r.Host
}

func setEditorCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: editorCookie, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

func serveEditor(env *Env, w http.ResponseWriter, r *http.Request) {
	g := env.EditorGate
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if g == nil {
		http.NotFound(w, r)
		return
	}
	p := r.URL.Path
	if p == editorPath {
		to := editorPath + "/"
		if r.URL.RawQuery != "" {
			to += "?" + r.URL.RawQuery
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, to, http.StatusFound)
		return
	}
	if m := editorOldEdit.FindStringSubmatch(p); m != nil && r.URL.Query().Has("edit") {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, editDocPath(m[1])+withoutEdit(r.URL.Query()), http.StatusFound)
		return
	}
	if strings.HasPrefix(p, editorPath+"/api/") {
		w.Header().Set("Cache-Control", "no-store")
		serveEditorAPI(env, g, w, r, strings.TrimPrefix(p, editorPath+"/api/"))
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", 405)
		return
	}
	// nothing of this goes into a shared cache: each answer is the signed-in user's
	w.Header().Set("Vary", "Cookie")
	t := g.user(r)
	page := editorPage(p)
	if t == nil {
		w.Header().Set("Cache-Control", "private, no-store")
		if !page {
			http.Error(w, "Sign in at /editor", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, editorLoginHTML)
		return
	}
	if page {
		l, err := g.license(r.Context(), t)
		if err != nil {
			log.Printf("editor: license: %v", err)
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "The license could not be read; try again in a moment.", http.StatusServiceUnavailable)
			return
		}
		serveEditorPage(env, g, w, r, t, l)
		return
	}
	serveEditorFile(g, w, r, strings.TrimPrefix(path.Clean(p), editorPath+"/"))
}

// the editor's page, its files under /editor/, with who is signed in and
// their license for web/sliqtly.js
func serveEditorPage(env *Env, g *editorGate, w http.ResponseWriter, r *http.Request, t *IDToken, l license) {
	b, err := fs.ReadFile(g.web, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	info, _ := json.Marshal(map[string]any{"uid": t.UID, "email": t.Email, "license": l.json(g.now())})
	html := strings.Replace(string(b), `<base href="/" />`, `<base href="/editor/" />`, 1)
	meta := `<meta name="sliqtly-site" content="` + htmlAttr(strings.TrimRight(env.BaseURL, "/")) + `" />` +
		"\n" + `<meta name="sliqtly-editor" content="` + htmlAttr(string(info)) + `" />`
	html = strings.Replace(html, "</head>", meta+"\n</head>", 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	io.WriteString(w, html)
}

// a file of the editor, to a signed-in user only
func serveEditorFile(g *editorGate, w http.ResponseWriter, r *http.Request, name string) {
	if name == "" || name == "." || strings.HasPrefix(name, "..") {
		http.NotFound(w, r)
		return
	}
	// every URL the page loads carries the build's stamp (?v=…): kept by
	// this browser, never by the CDN
	cache := "private, no-cache"
	if r.URL.RawQuery != "" {
		cache = "private, max-age=31536000, immutable"
	}
	// the faces are the ones the server lays slides out with (fonts.go)
	if strings.HasPrefix(name, "fonts/") {
		if b, err := fontFiles.ReadFile(name); err == nil {
			w.Header().Set("Content-Type", "font/ttf")
			w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
			w.Write(b)
			return
		}
	}
	f, err := g.web.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", cache)
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, name, st.ModTime(), rs)
		return
	}
	io.Copy(w, f)
}

func serveEditorAPI(env *Env, g *editorGate, w http.ResponseWriter, r *http.Request, op string) {
	if why := g.limit(clientIP(r)); why != "" {
		editorJSON(w, 429, map[string]string{"error": "Too many requests; try again in a few minutes."})
		return
	}
	switch op {
	case "session":
		if r.Method != http.MethodPost || !sameSite(env, r) {
			editorJSON(w, 403, map[string]string{"error": "Not allowed."})
			return
		}
		var in struct {
			IDToken string `json:"idToken"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in); err != nil || in.IDToken == "" {
			editorJSON(w, 400, map[string]string{"error": "No sign-in given."})
			return
		}
		t, err := g.verify(r.Context(), in.IDToken)
		if err != nil || t == nil || t.UID == "" {
			editorJSON(w, 401, map[string]string{"error": "The sign-in could not be verified; sign in again."})
			return
		}
		l, err := g.license(r.Context(), t)
		if err != nil {
			log.Printf("editor: license: %v", err)
			editorJSON(w, 503, map[string]string{"error": "The license could not be read; try again in a moment."})
			return
		}
		// the token is good for an hour: the page sends a new one before that
		setEditorCookie(w, in.IDToken, 3600)
		editorJSON(w, 200, map[string]any{"uid": t.UID, "email": t.Email, "license": l.json(g.now())})
	case "signout":
		if r.Method != http.MethodPost || !sameSite(env, r) {
			editorJSON(w, 403, map[string]string{"error": "Not allowed."})
			return
		}
		setEditorCookie(w, "", -1)
		editorJSON(w, 200, map[string]bool{"ok": true})
	case "license", "claim":
		t := g.user(r)
		if t == nil {
			editorJSON(w, 401, map[string]string{"error": "Sign in again.", "code": "signed-out"})
			return
		}
		if op == "license" {
			l, err := g.license(r.Context(), t)
			if err != nil {
				editorJSON(w, 503, map[string]string{"error": "The license could not be read; try again in a moment."})
				return
			}
			editorJSON(w, 200, map[string]any{"uid": t.UID, "email": t.Email, "license": l.json(g.now())})
			return
		}
		if r.Method != http.MethodPost || !sameSite(env, r) {
			editorJSON(w, 403, map[string]string{"error": "Not allowed."})
			return
		}
		var in struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil || !shareID.MatchString(in.ID) {
			editorJSON(w, 400, map[string]string{"error": "No presentation given."})
			return
		}
		l, why, err := g.claim(r.Context(), t, in.ID)
		if err != nil {
			log.Printf("editor: claim: %v", err)
			editorJSON(w, 503, map[string]string{"error": "The license could not be changed; try again in a moment."})
			return
		}
		if why != "" {
			msg := fmt.Sprintf("Your %s license lets you edit %d presentations in the cloud.", l.Plan, l.MaxDocs)
			if why == "expired" {
				msg = "Your license to edit has ended. Your presentations stay yours: open, present and export them as before."
			}
			if why == "not-yours" {
				msg = "This presentation belongs to another account. Only its owner and the people they invite can edit it."
			}
			editorJSON(w, 403, map[string]any{"error": msg, "code": "no-edit-right", "why": why, "license": l.json(g.now())})
			return
		}
		editorJSON(w, 200, map[string]any{"license": l.json(g.now())})
	default:
		editorJSON(w, 404, map[string]string{"error": "No such call."})
	}
}

// /s/{id}?edit on the public site: the presentation in the editor
var shareEditPath = regexp.MustCompile(`^/s/[A-Za-z0-9]{6,32}/?$`)

func editLinkTarget(r *http.Request) (string, bool) {
	if (r.Method != http.MethodGet && r.Method != http.MethodHead) || !shareEditPath.MatchString(r.URL.Path) || !r.URL.Query().Has("edit") {
		return "", false
	}
	return editDocPath(strings.TrimPrefix(strings.TrimRight(r.URL.Path, "/"), "/s/")) + withoutEdit(r.URL.Query()), true
}
