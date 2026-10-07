// SPDX-License-Identifier: AGPL-3.0-or-later

// A server of one's own: decks kept in a folder (fsstore.go), no Google, no
// sign-in. Around the MCP server (NewApp) it serves what the cloud has
// Hosting and Storage for:
//
//	/                     the editor (web/dist, localweb.go), or the list below
//	                      when the server has no built page
//	/decks                the decks kept here, newest first
//	/s/{id}               the player (web/dist), or the slides as pictures
//	/s/{id}/slides        a deck as its slides, drawn by render.go
//	/s/{id}/{n}.jpg       slide n (1-based)
//	/s/{id}/overview.jpg  every slide as a thumbnail
//	/files/shares/…       pictures and data files of a deck
//	/themes/{name}.css    the built-in themes
//
// With a token (SLIQTLY_TOKEN), /mcp answers only requests that carry it as
// "Authorization: Bearer <token>".

package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed themes/*.css
var themeFiles embed.FS

func builtinTheme(name string) (string, bool) {
	if !regexp.MustCompile(`^[a-z0-9-]{1,40}$`).MatchString(name) {
		return "", false
	}
	b, err := themeFiles.ReadFile("themes/" + name + ".css")
	if err != nil {
		return "", false
	}
	return string(b), true
}

type localServer struct {
	env    *Env
	app    http.Handler
	bucket *localBucket
	token  string
	web    fs.FS      // the built page; nil: none
	viewer bool       // web is the viewer's build: plays, no editor
	hub    *changeHub // the decks' changes, for /api/events
	collab *collabRooms
	board  *statusBoard // the server's state, for /api/status and /api/events
	expo   *exposure    // who can connect (netaccess.go); nil: not managed here
	hosts  *hostGuard   // which names and pages it answers (localguard.go)
	// calls' network (meet.go), made when the first call starts
	callMu sync.Mutex
	cnet   *callNet
	certs  *ownCerts // https:// (owncert.go); nil: none
	// the decks are listed on / and /decks (settings/listing); off unless
	// turned on, so a deck opens only by its link
	listing atomic.Bool
}

// the env of a server whose decks are in dir, reached at baseURL
func localEnv(dir, baseURL, user string) (*Env, *localBucket, error) {
	db, bucket, err := newFSStore(dir, user)
	if err != nil {
		return nil, nil, err
	}
	e := &Env{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Client:    newPublicClient(),
		DB:        db,
		Store:     db.e,
		Chat:      db.chat,
		Bucket:    bucket,
		LocalUser: user,
		Themes:    builtinTheme,
		// everyone is one user here, so the limit is for the whole server
		Limiter: rateLimiter(2000, 10*time.Minute),
	}
	e.FilesURL = e.BaseURL + "/files"
	e.rooms = newRoomService(e)
	e.GitHubToken = os.Getenv("SLIQTLY_GITHUB_TOKEN")
	return e, bucket, nil
}

func newLocalServer(env *Env, bucket *localBucket, token string, web fs.FS) http.Handler {
	if web != nil {
		// the built page has every theme the editor offers (corporate and
		// editorial come from Ranger); the ones built in are the fallback
		env.Themes = func(name string) (string, bool) {
			if regexp.MustCompile(`^[a-z0-9-]{1,40}$`).MatchString(name) {
				if b, err := fs.ReadFile(web, "themes/"+name+".css"); err == nil {
					return string(b), true
				}
			}
			return builtinTheme(name)
		}
	}
	env.Editor = web != nil && !viewerOnly(web)
	s := &localServer{env: env, app: NewApp(env), bucket: bucket, token: token, web: web, viewer: viewerOnly(web), board: newStatusBoard("ready", version), hosts: newHostGuard(env.BaseURL, !env.TrustHost)}
	if env.Store != nil {
		s.hub = newChangeHub()
		s.collab = newCollabRooms()
		if env.rooms != nil {
			hub := s.hub
			env.rooms.notify = func(_ string, v map[string]any) {
				if b, err := json.Marshal(v); err == nil {
					hub.publishChat(b)
				}
			}
		}
		// every write, in the order made, from now: whoever made it (a
		// page, an assistant, a room) and whatever the store is
		changes, err := env.Store.Watch(context.Background(), env.Store.Head())
		if err == nil {
			go func() {
				for c := range changes {
					s.collabWritten(c)
					s.hub.written(c.Col, c.ID, c.Doc)
				}
			}()
		}
	}
	s.loadSettings()
	return s
}

var (
	slidePath    = regexp.MustCompile(`^/s/([A-Za-z0-9]{6,32})/([0-9]{1,4})\.jpg$`)
	overviewPath = regexp.MustCompile(`^/s/([A-Za-z0-9]{6,32})/overview\.jpg$`)
	deckPath     = regexp.MustCompile(`^/s/([A-Za-z0-9]{6,32})/?$`)
	slidesPath   = regexp.MustCompile(`^/s/([A-Za-z0-9]{6,32})/slides$`)
	themePath    = regexp.MustCompile(`^/themes/([a-z0-9-]{1,40})\.css$`)
)

func (s *localServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if !s.guard(w, r) {
		return
	}
	if p == "/mcp" && s.token != "" && !s.authorized(r) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("WWW-Authenticate", `Bearer realm="sliqtly"`)
		w.WriteHeader(401)
		io.WriteString(w, `{"jsonrpc":"2.0","error":{"code":-32001,"message":"This server needs Authorization: Bearer <token>."},"id":null}`)
		return
	}
	if p == "/api/status" {
		st, _ := s.board.get()
		w.Header().Set("Access-Control-Allow-Origin", "*")
		writeStatus(w, st, 200)
		return
	}
	if p == "/api/settings" || p == "/api/settings/check" || p == "/api/settings/network" || p == "/api/settings/listing" {
		s.settingsAPI(w, r)
		return
	}
	if p == "/ca" && r.Method == http.MethodGet {
		s.caPage(w, r)
		return
	}
	if p == "/ca.crt" && r.Method == http.MethodGet {
		s.caFile(w)
		return
	}
	if p == "/settings" && r.Method == http.MethodGet {
		s.settingsPage(w)
		return
	}
	if strings.HasPrefix(p, "/api/") && p != "/api/hit" && !strings.HasPrefix(p, "/api/view/") {
		s.api(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.app.ServeHTTP(w, r)
		return
	}
	switch {
	case (p == "/" && s.web != nil && !s.viewer) || (p == "/index.html" && s.web != nil):
		s.page(w, r)
	case p == "/" || p == "/decks":
		s.index(w, r)
	case p == "/healthz":
		io.WriteString(w, "ok\n")
	case strings.HasPrefix(p, "/files/shares/"), strings.HasPrefix(p, "/files/rooms/"):
		s.file(w, r, strings.TrimPrefix(p, "/files/"))
	case themePath.MatchString(p):
		css, ok := s.env.Themes(themePath.FindStringSubmatch(p)[1])
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
		io.WriteString(w, css)
	case slidePath.MatchString(p):
		m := slidePath.FindStringSubmatch(p)
		n, _ := strconv.Atoi(m[2])
		s.picture(w, r, "render_slide", map[string]any{"deck_id": m[1], "slide": n})
	case overviewPath.MatchString(p):
		s.picture(w, r, "render_overview", map[string]any{"deck_id": overviewPath.FindStringSubmatch(p)[1]})
	case deckPath.MatchString(p) && s.web != nil:
		s.page(w, r)
	case deckPath.MatchString(p):
		s.deck(w, r, deckPath.FindStringSubmatch(p)[1])
	case slidesPath.MatchString(p):
		s.deck(w, r, slidesPath.FindStringSubmatch(p)[1])
	default:
		if s.web != nil && !strings.HasPrefix(p, "/.well-known/") && !strings.HasPrefix(p, "/oauth/") && p != "/mcp" && s.static(w, r) {
			return
		}
		s.app.ServeHTTP(w, r)
	}
}

func (s *localServer) authorized(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(h, "Bearer ")), []byte(s.token)) == 1
}

func (s *localServer) file(w http.ResponseWriter, r *http.Request, path string) {
	f, ct, ok := s.bucket.Open(path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// a deck's file is whatever was uploaded with whatever type it was given:
	// opened by itself (an SVG, an .html), it runs as a page of no origin,
	// never as one of this server's, so its scripts cannot use the API
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts allow-popups allow-downloads")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	http.ServeContent(w, r, "", f.ModTime, f)
}

// one MCP tools/call made inside the server, as a client would make it
type toolResult struct {
	Content []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Data     string `json:"data"`
		MimeType string `json:"mimeType"`
	} `json:"content"`
	StructuredContent map[string]any `json:"structuredContent"`
	IsError           bool           `json:"isError"`
}

func (s *localServer) call(r *http.Request, tool string, args map[string]any) (*toolResult, error) {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body)).WithContext(r.Context())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.RemoteAddr = r.RemoteAddr
	rec := httptest.NewRecorder()
	s.app.ServeHTTP(rec, req)
	var out struct {
		Result *toolResult `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("%s: %d %s", tool, rec.Code, rec.Body.String())
	}
	if out.Error != nil {
		return nil, fmt.Errorf("%s: %s", tool, out.Error.Message)
	}
	if out.Result == nil {
		return nil, fmt.Errorf("%s: no result", tool)
	}
	return out.Result, nil
}

func (t *toolResult) text() string {
	var b strings.Builder
	for _, c := range t.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (s *localServer) picture(w http.ResponseWriter, r *http.Request, tool string, args map[string]any) {
	res, err := s.call(r, tool, args)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, c := range res.Content {
		if c.Type == "image" {
			b, err := base64.StdEncoding.DecodeString(c.Data)
			if err != nil {
				break
			}
			w.Header().Set("Content-Type", c.MimeType)
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Write(b)
			return
		}
	}
	http.Error(w, strings.TrimSpace(res.text()), 404)
}

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root { color-scheme: light dark; --bg: #f6f6f4; --fg: #1d1d1b; --muted: #6b6b66; --card: #fff; --line: #e2e2dc; }
@media (prefers-color-scheme: dark) { :root { --bg: #141414; --fg: #ececea; --muted: #9a9a94; --card: #1e1e1e; --line: #333; } }
body { margin: 0; background: var(--bg); color: var(--fg); font: 16px/1.5 system-ui, sans-serif; }
main { max-width: 1040px; margin: 0 auto; padding: 24px 16px 64px; }
h1 { font-size: 1.5rem; margin: 0 0 4px; }
.muted { color: var(--muted); font-size: .9rem; }
ul { list-style: none; padding: 0; }
li { padding: 10px 0; border-bottom: 1px solid var(--line); }
a { color: inherit; }
figure { margin: 24px 0; }
figure img { width: 100%; height: auto; display: block; border-radius: 6px; background: var(--card); box-shadow: 0 1px 3px rgba(0,0,0,.15); }
figcaption { color: var(--muted); font-size: .85rem; margin-top: 6px; }
pre { white-space: pre-wrap; background: var(--card); border: 1px solid var(--line); border-radius: 6px; padding: 12px; font-size: .85rem; }
</style></head><body><main>
{{if .Deck}}
<p class="muted">{{if .Listing}}<a href="/decks">All presentations</a>{{else}}Sliqtly{{end}}{{if .Web}} · <a href="/s/{{.ID}}">play</a>{{end}}{{if .Edit}} · <a href="/s/{{.ID}}?edit">edit</a>{{end}}</p>
<h1>{{.Title}}</h1>
<p class="muted">{{.Slides}} slides · theme {{.Theme}} · <a href="/s/{{.ID}}/overview.jpg">overview</a></p>
{{range .Numbers}}<figure><img loading="lazy" src="/s/{{$.ID}}/{{.N}}.jpg" alt="Slide {{.N}}: {{.Title}}" width="960" height="540"><figcaption>{{.N}}. {{.Title}}</figcaption></figure>
{{end}}
{{else}}
<h1>Presentations</h1>
<p class="muted">Kept in this server's folder. MCP: <code>{{.MCP}}</code> · <a href="/settings">Settings</a></p>
{{if not .Listing}}<p>Presentations here are not listed: each one opens only by its link. Listing them can be turned on in <a href="/settings">Settings</a>, on the server's own computer.</p>
{{else}}<ul>{{range .Decks}}<li><a href="/s/{{.ID}}">{{.Name}}</a> <span class="muted">{{.When}} · <a href="/s/{{.ID}}/slides">slides</a>{{if $.Edit}} · <a href="/s/{{.ID}}?edit">edit</a>{{end}}</span></li>
{{else}}<li class="muted">None yet. Ask an assistant connected to {{$.MCP}} to make one.</li>{{end}}</ul>
{{end}}{{end}}
</main></body></html>`))

type deckRow struct {
	ID, Name, When string
	at             int64
}

func millis(v any) int64 {
	switch x := v.(type) {
	case time.Time:
		return x.UnixMilli()
	case int64:
		return x
	case float64:
		return int64(x)
	}
	return 0
}

func (s *localServer) index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	listed := s.listing.Load()
	page := map[string]any{"Title": "Sliqtly", "MCP": s.env.BaseURL + "/mcp", "Web": s.web != nil, "Edit": s.web != nil && !s.viewer, "Listing": listed}
	if !listed {
		pageTmpl.Execute(w, page)
		return
	}
	docs, ids, err := s.env.DB.WhereEq(r.Context(), "shares", "owner", s.env.LocalUser)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	rows := []deckRow{}
	for i, d := range docs {
		at := millis(d["updated"])
		if at == 0 {
			at = millis(d["created"])
		}
		name, _ := d["name"].(string)
		if name == "" {
			name = ids[i]
		}
		when := ""
		if at > 0 {
			when = time.UnixMilli(at).Format("2006-01-02 15:04")
		}
		rows = append(rows, deckRow{ID: ids[i], Name: name, When: when, at: at})
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].at > rows[b].at })
	page["Decks"] = rows
	pageTmpl.Execute(w, page)
}

var (
	slideCount = regexp.MustCompile(`": (\d+) slides?, numbered`)
	slideTitle = regexp.MustCompile(`(?m)^Slide (\d+) "(.*)": `)
)

type slideRow struct {
	N     int
	Title string
}

func (s *localServer) deck(w http.ResponseWriter, r *http.Request, id string) {
	d, err := s.env.DB.Get(r.Context(), "shares", id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if d == nil {
		http.NotFound(w, r)
		return
	}
	// the slides as the player has them: what render_overview reports
	n, titles := 0, map[int]string{}
	if res, err := s.call(r, "render_overview", map[string]any{"deck_id": id}); err == nil {
		text := res.text()
		if m := slideCount.FindStringSubmatch(text); m != nil {
			n, _ = strconv.Atoi(m[1])
		}
		for _, m := range slideTitle.FindAllStringSubmatch(text, -1) {
			i, _ := strconv.Atoi(m[1])
			titles[i] = m[2]
		}
	}
	theme, _ := d["theme"].(string)
	slides := make([]slideRow, n)
	for i := range slides {
		slides[i] = slideRow{i + 1, titles[i+1]}
	}
	name, _ := d["name"].(string)
	if name == "" {
		name = id
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	pageTmpl.Execute(w, map[string]any{"Deck": true, "ID": id, "Title": name, "Slides": n, "Theme": theme, "Numbers": slides, "Web": s.web != nil, "Edit": s.web != nil && !s.viewer, "Listing": s.listing.Load()})
}
