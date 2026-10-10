// The Go side of the server: what Ranger's Go target does not have, behind
// the operators of rgr/McpHost.rgr. The server itself (MCP, the tools, OAuth,
// the checks) is Ranger, compiled to sliqtly_mcp.go by `go generate`.
//
// Env lasts for the instance: the clients, the rate limiter, the theme
// cache. McpHost is one request's view of it, holding that request's first
// error and the bytes of its pictures.

package main

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/terotests/sliqtly/mcp-go/presdata"
	"github.com/terotests/sliqtly/mcp-go/store"
)

//go:generate node gen.mjs
//go:embed assets/guide.md
var guideMD string

//go:embed assets/preview.html
var previewHTML string

// the plugins' instructions (rgr/Plugins.rgr)
//
//go:embed assets/plugins/*.md
var pluginDocs embed.FS

// Doc is a Firestore document as plain values: string, int64, float64, bool,
// time.Time, []any, map[string]any, nil.
type Doc = map[string]any

// DB is the little of Firestore the server uses; firebase.go has the real
// one, the tests a map.
type DB interface {
	Get(ctx context.Context, col, id string) (Doc, error) // nil, nil when missing
	Set(ctx context.Context, col, id string, d Doc) error
	Update(ctx context.Context, col, id string, d Doc) error
	Delete(ctx context.Context, col, id string) error
	WhereEq(ctx context.Context, col, field string, value any) ([]Doc, []string, error)
	// WhereHas: the documents whose array field `field` holds value
	WhereHas(ctx context.Context, col, field string, value any) ([]Doc, []string, error)
	ServerTime() any
	// Create writes d only when there is no such document; the one there
	// already is returned when there is.
	Create(ctx context.Context, col, id string, d Doc) (Doc, error)
	// Increment adds the int64 leaves of add to the document's fields
	// (nested maps for nested fields), creating what is missing.
	Increment(ctx context.Context, col, id string, add Doc) error
	// UpdateIf updates the document (writes it whole when there is none)
	// only when its string field `field` is `want` (missing reads as ""),
	// in one transaction; false when the field was something else.
	UpdateIf(ctx context.Context, col, id, field, want string, d Doc) (bool, error)
	// Take reads the document and deletes it in one step: of two callers
	// taking the same document, one gets it and the other nil (a one-time
	// code or refresh token is spent once).
	Take(ctx context.Context, col, id string) (Doc, error)
}

// Bucket is the little of Cloud Storage the server uses.
type Bucket interface {
	Name() string
	Save(ctx context.Context, path, contentType string, data []byte, metadata map[string]string) error
	Read(ctx context.Context, path string, limit int64) ([]byte, error)
}

// IDToken is what a verified Firebase ID token says about the person.
type IDToken struct {
	UID, Name, Email string
	Verified         bool // the provider vouches for Email
}

type Env struct {
	DB            DB     // nil: nothing is kept, the deck travels in the link
	Bucket        Bucket // with DB
	OAuth         bool   // sign-in, which needs DB
	VerifyIDToken func(ctx context.Context, token string) (*IDToken, error)
	BaseURL       string
	TrustHost     bool
	Client        *http.Client // pictures and client metadata: public addresses only
	ThemeClient   *http.Client // the site's own theme sheets
	Limiter       func(who string) string
	// Quota: writes per caller per day, counted in Firestore (dailyQuota);
	// nil: none. Registrations: a limiter for POST /oauth/register.
	Quota         func(ctx context.Context, who string) string
	Registrations func(who string) string
	// Hits: a limiter for the visit beacon POST /api/hit, per address.
	Hits func(who string) string
	// Renders: drawings (render_slide, render_overview,
	// export_presentation) per caller and per address per day, counted in
	// Firestore (dailyCount); nil: none. renders: what each caller is
	// drawing now (renderSlots).
	Renders func(ctx context.Context, who, ip string) string
	renders *renderSlots
	Now     func() time.Time // nil: time.Now
	// A server of one's own (decks in a folder, local.go): every caller is
	// LocalUser, files are read from FilesURL, themes are the built-in ones.
	LocalUser string
	// Editor: the server serves the editor (a server of one's own with
	// web/dist built in), so results carry an editor link; sliqtly.com
	// serves the viewer only
	Editor bool
	// Store: the documents of a server of one's own, under DB, for what
	// needs more than DB says (revisions, the change feed); nil elsewhere
	Store store.Engine
	// Chat: the rooms' messages, beside Store; nil elsewhere
	Chat store.ChatLog
	// Forms: questionnaires' links, responses and counters (forms.go),
	// beside Store; nil elsewhere
	Forms    store.Forms
	FilesURL string // e.g. https://host/files; "": Storage download URLs
	// GitHubToken: sent to api.github.com by read_github_pr only (never by
	// FetchText, which fetches what decks name), for its higher limit; ""
	// reads as anyone
	GitHubToken string
	// GitHubUsers: the Sliqtly user ids (Firebase uids) for whom the token
	// may read private repositories (SLIQTLY_GITHUB_USERS, comma-separated).
	// Anyone may call the server, so a private repository the token reaches
	// is refused to everyone else.
	GitHubUsers []string
	Themes      func(name string) (string, bool)
	// ImportDirs: folders of this computer pictures and data files may be
	// read from by path (importdirs.go); only on a server of one's own
	// started with SLIQTLY_IMPORT_DIRS, empty elsewhere
	ImportDirs importDirs
	// Plugins: the plugins sliqtly_plugin offers (SLIQTLY_PLUGINS, comma
	// separated, or "all"); none by default
	Plugins []string
	// the form a presentation's name must have (names.go): only on a server
	// of one's own, set from its settings page; nil: any name
	names atomic.Pointer[nameRule]
	// rooms (roomsapi.go): with Store and LocalUser; nil elsewhere
	rooms *roomService
	// shared: the files everyone on the server sees (sharedfiles.go), a
	// server of its own only; nil elsewhere
	shared *sharedFiles
	// cloudRooms: the cloud's shared rooms and their chat, for the editor's
	// signed-in people (POST /editor/api/rooms/<op>, editorrooms.go); nil
	// elsewhere
	cloudRooms *roomService
	// Clients: the OAuth clients a server of one's own knows without
	// registration (oidc.go builtinClient), as their client document; nil,
	// or nil for an id: none
	Clients func(id string) map[string]any

	themesMu sync.Mutex
	themes   map[string]string
	webCfg   string            // the site's /__/firebase/init.json once it has been read
	cache    map[string]string // host_cache_put: the day's visit salt
	// the viewer's page and the link cards' pictures (linkcard.go)
	cards linkCards

	// the owner's dashboard (admin.go); nil: no /main/admin/api
	Admin *adminConfig
	// the editor for signed-in people at /editor (editor.go); nil: none
	EditorGate *editorGate
}

// The whole server as one handler.
func NewApp(env *Env) http.Handler {
	if env.Limiter == nil {
		env.Limiter = rateLimiter(60, 10*time.Minute)
	}
	if env.ThemeClient == nil {
		env.ThemeClient = &http.Client{Timeout: 10 * time.Second}
	}
	if env.Registrations == nil {
		env.Registrations = rateLimiter(20, 10*time.Minute)
	}
	if env.Hits == nil {
		env.Hits = rateLimiter(120, 10*time.Minute)
	}
	if env.renders == nil {
		env.renders = &renderSlots{max: 2, lease: 3 * time.Minute, held: map[string][]time.Time{}}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 40<<20)
		if r.Method == http.MethodPost && r.URL.Path == "/oauth/register" && env.Registrations(clientIP(r)) != "" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(429)
			io.WriteString(w, `{"error":"slow_down","error_description":"Too many registrations from here; try again in a few minutes."}`)
			return
		}
		if strings.HasPrefix(r.URL.Path, adminPath) {
			serveAdmin(env, w, r)
			return
		}
		if r.URL.Path == editorPath || strings.HasPrefix(r.URL.Path, editorPath+"/") {
			serveEditor(env, w, r)
			return
		}
		if env.EditorGate != nil {
			if to, ok := editLinkTarget(r); ok {
				http.Redirect(w, r, to, http.StatusFound)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/d/") && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			serveDownload(env, w, r)
			return
		}
		h := &McpHost{env: env, r: r, ctx: r.Context(), images: map[int64][]byte{}}
		defer func() {
			if p := recover(); p != nil {
				log.Printf("request failed: %v", p)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(500)
				io.WriteString(w, `{"jsonrpc":"2.0","error":{"code":-32603,"message":"Internal error"},"id":null}`)
			}
		}()
		App_static_serve(h, r, w)
	})
}

// Writes per caller: a sliding window, per instance. Enough to stop a loop,
// not a quota system.
func rateLimiter(max int, window time.Duration) func(who string) string {
	var mu sync.Mutex
	hits := map[string][]time.Time{}
	return func(who string) string {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		keep := []time.Time{}
		for _, t := range hits[who] {
			if now.Sub(t) < window {
				keep = append(keep, t)
			}
		}
		if len(keep) >= max {
			hits[who] = keep
			return "Too many presentations from here in a short time; try again in a few minutes."
		}
		hits[who] = append(keep, now)
		if len(hits) > 5000 {
			for k, v := range hits {
				if len(v) == 0 || now.Sub(v[len(v)-1]) >= window {
					delete(hits, k)
				}
			}
		}
		return ""
	}
}

// Drawings per day (Tero 2026-10-06: rendering is where the bill can
// surprise): a caller without sign-in `anonymous` a day from its address, a
// signed-in one `signedIn` for the account and twice that for its address
// (many accounts behind one address). Counted like dailyQuota, under
// mcp_quota/<sha256("render " + who)>-<day>.
func dailyRenders(db DB, anonymous, signedIn int64, now func() time.Time) func(ctx context.Context, who, ip string) string {
	count := func(ctx context.Context, key string, max int64) bool {
		t := now().UTC()
		sum := sha256.Sum256([]byte("render " + key))
		id := hex.EncodeToString(sum[:]) + "-" + t.Format("2006-01-02")
		d, err := db.Get(ctx, "mcp_quota", id)
		if err != nil {
			log.Printf("render quota: %v", err)
			return true
		}
		var n int64
		if d != nil {
			if v, ok := d["n"].(int64); ok {
				n = v
			}
		}
		if n >= max {
			return false
		}
		if err := db.Set(ctx, "mcp_quota", id, Doc{"n": n + 1, "expires": t.Add(48 * time.Hour)}); err != nil {
			log.Printf("render quota: %v", err)
		}
		return true
	}
	return func(ctx context.Context, who, ip string) string {
		if !strings.HasPrefix(who, "uid:") {
			if !count(ctx, ip, anonymous) {
				return fmt.Sprintf("The daily limit of %d pictures and exports from here is used up; try again tomorrow, or sign in for a higher limit.", anonymous)
			}
			return ""
		}
		if !count(ctx, who, signedIn) || !count(ctx, ip, 2*signedIn) {
			return fmt.Sprintf("The daily limit of %d pictures and exports is used up; try again tomorrow.", signedIn)
		}
		return ""
	}
}

// Writes per caller per day, in Firestore so every instance counts the same:
// mcp_quota/<sha256(who)>-<UTC day>, deleted by the TTL policy on `expires`
// (as mcp/src/http.js dailyQuota). The 10-minute limiter stops a loop; this
// caps a day. Read and write are not one transaction: a burst across
// instances may pass a few over.
func dailyQuota(db DB, anonymous, signedIn int64, now func() time.Time) func(ctx context.Context, who string) string {
	return func(ctx context.Context, who string) string {
		t := now().UTC()
		max, more := anonymous, ", or sign in for a higher limit"
		if strings.HasPrefix(who, "uid:") {
			max, more = signedIn, ""
		}
		sum := sha256.Sum256([]byte(who))
		id := hex.EncodeToString(sum[:]) + "-" + t.Format("2006-01-02")
		d, err := db.Get(ctx, "mcp_quota", id)
		if err != nil {
			log.Printf("quota: %v", err)
			return ""
		}
		var n int64
		if d != nil {
			if v, ok := d["n"].(int64); ok {
				n = v
			}
		}
		if n >= max {
			return fmt.Sprintf("The daily limit of %d saved changes from here is used up; try again tomorrow%s.", max, more)
		}
		if err := db.Set(ctx, "mcp_quota", id, Doc{"n": n + 1, "expires": t.Add(48 * time.Hour)}); err != nil {
			log.Printf("quota: %v", err)
		}
		return ""
	}
}

type McpHost struct {
	env    *Env
	r      *http.Request
	ctx    context.Context
	err    error
	images map[int64][]byte
	next   int64
	// pictures for render.go, by the name the lists give them
	renderPics *renderPics
	// the deck's own effects for render.go (fxvm.go RenderFx)
	renderFx *renderFx
	// what the renders could not draw of them, for the tool's text (FxReport)
	fxNotes []string
	// the picture a host tool answered with (get_figma_screen), for
	// ToolImage
	toolImage []byte
	// what was read may read otherwise next time: live data from the web,
	// a picture or file that did not come (a layout made so is not kept,
	// viewcache.go)
	unsure bool
}

func (h *McpHost) fail(err error) {
	if err != nil && h.err == nil {
		h.err = err
	}
}

func (h *McpHost) Err() string {
	if h.err == nil {
		return ""
	}
	return h.err.Error()
}

func (h *McpHost) ClearErr() { h.err = nil }

func toJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	return strings.TrimSuffix(buf.String(), "\n")
}

// --- the request and the configuration

func (h *McpHost) BodyJSON() string {
	b, err := io.ReadAll(h.r.Body)
	if err != nil {
		return ""
	}
	ct, _, _ := mime.ParseMediaType(h.r.Header.Get("content-type"))
	if ct == "application/x-www-form-urlencoded" {
		v, _ := url.ParseQuery(string(b))
		out := map[string]any{}
		for k := range v {
			out[k] = v.Get(k)
		}
		return toJSON(out)
	}
	// JSON as sent, with what a client escaped (ä, surrogate pairs)
	// written out, which is what the Ranger reader expects
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil || dec.More() {
		return ""
	}
	return toJSON(v)
}

func queryJSON(r *http.Request) string {
	out := map[string]any{}
	for k, v := range r.URL.Query() {
		out[k] = v[0]
	}
	return toJSON(out)
}

// the caller's address, as the rate limits and quota key it (clientip.go)
func (h *McpHost) ClientIP() string { return clientIP(h.r) }

func (h *McpHost) TLS() bool       { return h.r.TLS != nil }
func (h *McpHost) BaseURL() string { return h.env.BaseURL }
func (h *McpHost) TrustHost() bool { return h.env.TrustHost }
func (h *McpHost) OAuthOn() bool   { return h.env.OAuth && h.env.DB != nil }
func (h *McpHost) Log(msg string)  { log.Print(msg) }
func (h *McpHost) NowMS() int64 {
	if h.env.Now != nil {
		return h.env.Now().UnixMilli()
	}
	return time.Now().UnixMilli()
}
func (h *McpHost) StoreKind() string {
	if h.env.DB == nil {
		return "link"
	}
	return "cloud"
}

func (h *McpHost) Asset(name string) string {
	switch name {
	case "guide.md":
		md := guideMD
		if h.env.rooms == nil {
			md = withoutRooms(md)
		} else {
			md = withRooms(md)
		}
		if h.env.shared == nil {
			return withoutPart(md, "figma")
		}
		return withPart(md, "figma")
	case "preview.html":
		return previewHTML
	}
	if strings.HasPrefix(name, "plugins/") && !strings.Contains(name[8:], "/") {
		if b, err := pluginDocs.ReadFile("assets/" + name); err == nil {
			return string(b)
		}
	}
	return ""
}

// Plugins: the plugins the admin turned on, comma separated
func (h *McpHost) Plugins() string { return strings.Join(h.env.Plugins, ",") }

// what a caller hears when a theme cannot be had; the cause goes to the log
func themeUnavailable(theme string) error {
	return fmt.Errorf("The theme %q could not be loaded. Try again in a moment, or choose another theme.", theme)
}

// the guide without its Rooms parts, on a server that has no rooms
// (sliqtly.com): each <!-- rooms --> … <!-- /rooms --> in assets/guide.md
// (the rooms topic, its row in Core's list of topics)
func withoutRooms(md string) string { return withoutPart(md, "rooms") }

// the guide with its Rooms parts, the marker lines taken out
func withRooms(md string) string { return withPart(md, "rooms") }

// the guide without each <!-- part --> … <!-- /part -->: what only some
// servers have (rooms; figma: the shared files of a server of one's own)
func withoutPart(md, part string) string {
	open, end := "<!-- "+part+" -->", "<!-- /"+part+" -->"
	for {
		i := strings.Index(md, open)
		j := strings.Index(md, end)
		if i < 0 || j < i {
			return md
		}
		md = md[:i] + strings.TrimLeft(md[j+len(end):], "\n")
	}
}

// the guide with those parts, the marker lines taken out
func withPart(md, part string) string {
	md = strings.ReplaceAll(md, "<!-- "+part+" -->\n", "")
	return strings.ReplaceAll(md, "<!-- /"+part+" -->\n", "")
}

// --- Firestore

// stored values → JSON values: timestamps as milliseconds
func plain(v any) any {
	switch x := v.(type) {
	case time.Time:
		return x.UnixMilli()
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = plain(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = plain(e)
		}
		return out
	}
	return v
}

// JSON values → stored values: whole numbers as integers, as the Node SDK
// stores a JavaScript number, {"$serverTime":true} as the server's time and
// {"$time": ms} as that timestamp (J.timeAt)
func (h *McpHost) stored(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		if len(x) == 1 && x["$serverTime"] == true {
			return h.env.DB.ServerTime()
		}
		if ms, ok := x["$time"].(json.Number); ok && len(x) == 1 {
			if i, err := ms.Int64(); err == nil {
				return time.UnixMilli(i).UTC()
			}
		}
		out := map[string]any{}
		for k, e := range x {
			out[k] = h.stored(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = h.stored(e)
		}
		return out
	}
	return v
}

func (h *McpHost) parseDoc(text string) Doc {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var d map[string]any
	if err := dec.Decode(&d); err != nil {
		h.fail(fmt.Errorf("bad document: %w", err))
		return nil
	}
	return h.stored(d).(map[string]any)
}

func (h *McpHost) db() bool {
	if h.env.DB == nil {
		h.fail(fmt.Errorf("no cloud storage configured"))
		return false
	}
	return true
}

func (h *McpHost) GetDoc(col, id string) string {
	if !h.db() {
		return ""
	}
	d, err := h.env.DB.Get(h.ctx, col, id)
	if err != nil || d == nil {
		h.fail(err)
		return ""
	}
	return toJSON(plain(d))
}

// the document, deleted as it is read: "" when it was not there (or
// another request took it first)
func (h *McpHost) TakeDoc(col, id string) string {
	if !h.db() {
		return ""
	}
	d, err := h.env.DB.Take(h.ctx, col, id)
	if err != nil || d == nil {
		h.fail(err)
		return ""
	}
	return toJSON(plain(d))
}

func (h *McpHost) SetDoc(col, id, text string) {
	if d := h.parseDoc(text); d != nil && h.db() {
		// a new presentation is one more in its room: the pages' room
		// lists are read again
		fresh := false
		if col == "shares" && h.env.rooms != nil {
			was, err := h.env.DB.Get(h.ctx, col, id)
			fresh = err == nil && was == nil
		}
		err := h.env.DB.Set(h.ctx, col, id, d)
		h.fail(err)
		if err == nil && fresh {
			h.env.rooms.tell("", map[string]any{"t": "rooms"})
		}
	}
}

func (h *McpHost) UpdateDoc(col, id, text string) {
	if d := h.parseDoc(text); d != nil && h.db() {
		h.fail(h.env.DB.Update(h.ctx, col, id, d))
	}
}

// A Bucket that removes every file under a prefix (a deck's folder).
type prefixRemover interface {
	RemovePrefix(ctx context.Context, prefix string) error
}

func (h *McpHost) RemoveFiles(prefix string) {
	if h.env.Bucket == nil || !strings.HasSuffix(prefix, "/") || len(prefix) < 3 {
		return
	}
	if r, ok := h.env.Bucket.(prefixRemover); ok {
		h.fail(r.RemovePrefix(h.ctx, prefix))
	}
}

func (h *McpHost) DeleteDoc(col, id string) {
	if h.db() {
		h.fail(h.env.DB.Delete(h.ctx, col, id))
	}
}

func (h *McpHost) CreateDoc(col, id, text string) string {
	d := h.parseDoc(text)
	if d == nil || !h.db() {
		return ""
	}
	had, err := h.env.DB.Create(h.ctx, col, id, d)
	if err != nil || had == nil {
		h.fail(err)
		return ""
	}
	return toJSON(plain(had))
}

func (h *McpHost) UpdateDocIf(col, id, field, want, text string) string {
	d := h.parseDoc(text)
	if d == nil || !h.db() {
		return ""
	}
	ok, err := h.env.DB.UpdateIf(h.ctx, col, id, field, want, d)
	if err != nil {
		h.fail(err)
		return ""
	}
	if !ok {
		return "changed"
	}
	return ""
}

// the string a document's field holds, "" when it is missing or not a string
func fieldText(d Doc, field string) string {
	s, _ := d[field].(string)
	return s
}

func (h *McpHost) IncrementDoc(col, id, text string) {
	if d := h.parseDoc(text); d != nil && h.db() {
		h.fail(h.env.DB.Increment(h.ctx, col, id, d))
	}
}

func (h *McpHost) QueryEq(col, field, value string) string {
	if !h.db() {
		return "[]"
	}
	docs, ids, err := h.env.DB.WhereEq(h.ctx, col, field, value)
	if err != nil {
		h.fail(err)
		return "[]"
	}
	out := []any{}
	for i, d := range docs {
		p := plain(d).(map[string]any)
		p["id"] = ids[i]
		out = append(out, p)
	}
	return toJSON(out)
}

// --- pictures

func (h *McpHost) keep(data []byte) int64 {
	h.next++
	h.images[h.next] = data
	return h.next
}

func (h *McpHost) ImageFromBase64(data string) string {
	b, err := decodeBase64Err(data)
	if err != nil {
		return toJSON(map[string]any{"handle": 0, "size": 0, "bad": true})
	}
	return toJSON(map[string]any{"handle": h.keep(b), "size": len(b)})
}

func (h *McpHost) ImageFromURL(u string, limit int64) string {
	ctx, cancel := context.WithTimeout(h.ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return `{"status":0}`
	}
	req.Header.Set("user-agent", "Sliqtly-MCP/1.0")
	res, err := h.env.Client.Do(req)
	if err != nil {
		h.unsure = true
		return toJSON(map[string]any{"status": 0, "error": err.Error()})
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		h.unsure = true
	}
	ct := strings.TrimSpace(strings.Split(res.Header.Get("content-type"), ";")[0])
	length, _ := strconv.ParseInt(res.Header.Get("content-length"), 10, 64)
	out := map[string]any{"status": res.StatusCode, "type": ct, "length": length, "size": 0, "handle": 0}
	if res.StatusCode >= 200 && res.StatusCode <= 299 && length <= limit {
		b, err := io.ReadAll(io.LimitReader(res.Body, limit))
		if err != nil {
			return toJSON(map[string]any{"status": 0, "error": err.Error()})
		}
		out["size"], out["handle"] = len(b), h.keep(b)
	}
	return toJSON(out)
}

func (h *McpHost) Upload(handle int64, path, contentType, token string) {
	if h.env.Bucket == nil {
		h.fail(fmt.Errorf("no cloud storage configured"))
		return
	}
	h.fail(h.env.Bucket.Save(h.ctx, path, contentType, h.images[handle], map[string]string{"firebaseStorageDownloadTokens": token}))
}

// a deck's data files are read whole, up to this
const maxFileRead = 20 << 20

func (h *McpHost) FileBytes(id, path string) string {
	if h.env.Bucket == nil {
		h.fail(fmt.Errorf("no cloud storage configured"))
		return `{"handle":0,"size":0}`
	}
	b, err := h.env.Bucket.Read(h.ctx, "shares/"+id+"/"+path, maxFileRead)
	if err != nil {
		h.unsure = true
		h.fail(err)
		return `{"handle":0,"size":0}`
	}
	return toJSON(map[string]any{"handle": h.keep(b), "size": len(b)})
}

func (h *McpHost) KeepText(text string) int64 { return h.keep([]byte(text)) }
func (h *McpHost) KeepBytes(b []byte) int64   { return h.keep(b) }
func (h *McpHost) Text(handle int64) string   { return string(h.images[handle]) }

func (h *McpHost) XlsxSheets(handle int64) string {
	return presdata.PresData_static_xlsxSheets(h.images[handle])
}

func (h *McpHost) Precision(n string, digits int64) string {
	f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
	if err != nil {
		return n
	}
	f, _ = strconv.ParseFloat(strconv.FormatFloat(f, 'g', int(digits), 64), 64)
	if a := math.Abs(f); a >= 1e21 || (a != 0 && a < 1e-6) {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func (h *McpHost) LocalUser() string { return h.env.LocalUser }

func (h *McpHost) EditorOn() bool { return h.env.Editor }

// the import folders, comma separated; "" when files are not read by path
func (h *McpHost) ImportDirs() string {
	if h.env.LocalUser == "" {
		return ""
	}
	return strings.Join(h.env.ImportDirs.list(), ", ")
}

// {"handle","size"} of the file at path in an import folder, at most limit
// bytes; {"size":0,"error":…} when it is not read
func (h *McpHost) ImportFile(path string, limit int64) string {
	if h.env.LocalUser == "" {
		return toJSON(map[string]any{"handle": 0, "size": 0, "error": "this server reads no files by path"})
	}
	b, err := h.env.ImportDirs.read(path, limit)
	if err != nil {
		return toJSON(map[string]any{"handle": 0, "size": 0, "error": err.Error()})
	}
	return toJSON(map[string]any{"handle": h.keep(b), "size": len(b)})
}

func (h *McpHost) GitHubPrivateOK(uid string) bool {
	if h.env.LocalUser != "" {
		return true
	}
	if uid == "" {
		return false
	}
	for _, u := range h.env.GitHubUsers {
		if u == uid {
			return true
		}
	}
	return false
}

// githubUsers reads SLIQTLY_GITHUB_USERS: ids split at commas and spaces
func githubUsers(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		out = append(out, f)
	}
	return out
}

// HostTools is the tools the Go side adds (rooms, roomsapi.go) as a JSON
// array of MCP tool entries; "[]" where it adds none
func (h *McpHost) HostTools() string {
	out := []any{}
	if h.env.rooms != nil {
		out = append(out, h.env.rooms.toolsJSON()...)
	}
	out = append(out, sharedToolsJSON(h.sharedToolsOn())...)
	return roomJSON(out)
}

// HasTool: name is one of HostTools
func (h *McpHost) HasTool(name string) bool {
	return (h.env.rooms != nil && findMcpRoomTool(name)) || h.hasSharedTool(name)
}

// CallTool runs one of HostTools for uid with args (JSON) → the answer as
// JSON text; a caller's mistake or a failure is the host's error. who is
// the rate limit's key, counted for the tools that change something.
func (h *McpHost) CallTool(uid, who, name, args string) string {
	if !h.HasTool(name) {
		h.fail(fmt.Errorf("no tool %s", name))
		return ""
	}
	for _, t := range append(allRoomTools(), sharedTools...) {
		if t.name == name && !t.readOnly {
			if why := h.env.Limiter(who); why != "" {
				h.fail(errors.New(why))
				return ""
			}
		}
	}
	a := map[string]any{}
	if strings.TrimSpace(args) != "" && strings.TrimSpace(args) != "null" {
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			h.fail(fmt.Errorf("the arguments are not a JSON object"))
			return ""
		}
	}
	var out any
	var err error
	if h.hasSharedTool(name) {
		out, err = h.callSharedTool(name, a)
	} else {
		out, err = h.env.rooms.call(h.ctx, uid, name, a)
	}
	if err != nil {
		h.fail(err)
		return ""
	}
	return roomJSON(out)
}

// NameRule says what a name must look like, for the tools' descriptions;
// "" when any name will do
func (h *McpHost) NameRule() string { return h.env.names.Load().describe() }

// CheckName is "" for a name of the required form, else why it is not
func (h *McpHost) CheckName(name string) string { return h.env.names.Load().check(name) }

// NameKey is the key part of a name (ABC-1234), "" when it has none
func (h *McpHost) NameKey(name string) string { return h.env.names.Load().key(name) }

// where the page reads a kept file: the server's own /files/ or Storage's
// download URL, which carries the token the upload was given
func (h *McpHost) FileURL(name, token string) string {
	if h.env.FilesURL != "" {
		parts := strings.Split(name, "/")
		for i, p := range parts {
			parts[i] = url.PathEscape(p)
		}
		return h.env.FilesURL + "/" + strings.Join(parts, "/")
	}
	return "https://firebasestorage.googleapis.com/v0/b/" + h.Bucket() + "/o/" + h.URIEncode(name) + "?alt=media&token=" + token
}

func (h *McpHost) Bucket() string {
	if h.env.Bucket == nil {
		return ""
	}
	return h.env.Bucket.Name()
}

// --- the network

func (h *McpHost) ThemeCSS(theme string) string {
	e := h.env
	if e.Themes != nil {
		css, ok := e.Themes(theme)
		if !ok {
			h.fail(fmt.Errorf("There is no theme %q; the themes are aurora, nebula, carbon, ember, midnight (dark) and white, corporate, editorial, pearl, hive, lattice, apex, tide, mist (light) and forge, foundry, site (work) and clinic, care, vital (health).", theme))
		}
		return css
	}
	// every theme is built in (gen.mjs); the site is asked only for one
	// that is not, since sliqtly.com serves the viewer and no /themes/
	if css, ok := builtinTheme(theme); ok {
		return css
	}
	e.themesMu.Lock()
	css, ok := e.themes[theme]
	e.themesMu.Unlock()
	if ok {
		return css
	}
	ctx, cancel := context.WithTimeout(h.ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/themes/%s.css", e.BaseURL, theme), nil)
	if err != nil {
		log.Printf("theme %s: %v", theme, err)
		h.fail(themeUnavailable(theme))
		return ""
	}
	res, err := e.ThemeClient.Do(req)
	if err != nil {
		log.Printf("theme %s: %v", theme, err)
		h.fail(themeUnavailable(theme))
		return ""
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		log.Printf("theme %s: HTTP %d", theme, res.StatusCode)
		h.fail(themeUnavailable(theme))
		return ""
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		h.fail(err)
		return ""
	}
	e.themesMu.Lock()
	if e.themes == nil {
		e.themes = map[string]string{}
	}
	e.themes[theme] = string(b)
	e.themesMu.Unlock()
	return string(b)
}

// The web app's Firebase config (public) for the preview, which cannot read
// Hosting's /__/firebase/init.js across origins; "" while it cannot be read.
func (h *McpHost) WebConfig() string {
	e := h.env
	if e.LocalUser != "" {
		return ""
	}
	e.themesMu.Lock()
	cfg := e.webCfg
	e.themesMu.Unlock()
	if cfg != "" {
		return cfg
	}
	ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", e.BaseURL+"/__/firebase/init.json", nil)
	if err != nil {
		return ""
	}
	res, err := e.ThemeClient.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	var obj map[string]any
	if err != nil || res.StatusCode != 200 || json.Unmarshal(b, &obj) != nil || obj == nil {
		return ""
	}
	e.themesMu.Lock()
	e.webCfg = string(b)
	e.themesMu.Unlock()
	return string(b)
}

// A GET on a public address for anything a deck or a caller names (chart
// data, client metadata): never with the server's GitHub token, or a chart
// pointed at api.github.com would read what the token reaches.
func (h *McpHost) FetchText(u, accept string, limit int64) string {
	return h.fetch(u, accept, limit, "")
}

// FetchText for read_github_pr alone: api.github.com with the server's
// token (its higher limit, and the private repositories it reaches, which
// readGitHubPr refuses to callers not in GitHubUsers).
func (h *McpHost) FetchGitHub(u, accept string, limit int64) string {
	if !strings.HasPrefix(u, "https://api.github.com/") {
		return `{"status":0}`
	}
	return h.fetch(u, accept, limit, h.env.GitHubToken)
}

func (h *McpHost) fetch(u, accept string, limit int64, token string) string {
	h.unsure = true
	ctx, cancel := context.WithTimeout(h.ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return `{"status":0}`
	}
	req.Header.Set("accept", accept)
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	res, err := h.env.Client.Do(req)
	if err != nil {
		return `{"status":0}`
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, limit))
	return toJSON(map[string]any{"status": res.StatusCode, "body": string(b)})
}

func (h *McpHost) VerifyIDToken(token string) string {
	if h.env.VerifyIDToken == nil {
		return ""
	}
	t, err := h.env.VerifyIDToken(h.ctx, token)
	if err != nil || t == nil {
		return ""
	}
	return toJSON(map[string]any{"uid": t.UID, "name": t.Name, "email": t.Email})
}

// --- small things

func (h *McpHost) SHA256Hex(s string) string {
	x := sha256.Sum256([]byte(s))
	return hex.EncodeToString(x[:])
}

// BytesSHA256 is the SHA-256 of a handle's bytes (a picture as received),
// lowercase hex
func (h *McpHost) BytesSHA256(handle int64) string {
	x := sha256.Sum256(h.images[handle])
	return hex.EncodeToString(x[:])
}

func (h *McpHost) SameSecret(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (h *McpHost) SHA256B64URL(s string) string {
	x := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(x[:])
}

const abc = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// n characters of a-z, A-Z, 0-9, as the editor's shortId()
func (h *McpHost) ShortID(n int64) string {
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return string(b)
}

func (h *McpHost) UUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// a flate writer costs about 1 MB to set up, so they are kept
var flaters = sync.Pool{New: func() any { w, _ := flate.NewWriter(nil, 6); return w }}

// the editor's packText(): deflate-raw, then base64url
func (h *McpHost) PackText(text string) string {
	var buf bytes.Buffer
	w := flaters.Get().(*flate.Writer)
	defer flaters.Put(w)
	w.Reset(&buf)
	w.Write([]byte(text))
	w.Close()
	return base64.RawURLEncoding.EncodeToString(buf.Bytes())
}

func unpackText(code string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(code)
	if err != nil {
		return "", err
	}
	out, err := io.ReadAll(flate.NewReader(bytes.NewReader(b)))
	return string(out), err
}

func (h *McpHost) RateLimit(who string) string {
	if why := h.env.Limiter(who); why != "" || h.env.Quota == nil {
		return why
	}
	return h.env.Quota(h.ctx, who)
}

func (h *McpHost) HitLimit(who string) string { return h.env.Hits(who) }

func (h *McpHost) RenderLimit(who, ip string) string {
	if !h.env.renders.take(who) {
		return "Already drawing two pictures for you; wait for them, then ask again."
	}
	if h.env.Renders != nil {
		if why := h.env.Renders(h.ctx, who, ip); why != "" {
			h.env.renders.give(who)
			return why
		}
	}
	return ""
}

func (h *McpHost) RenderDone(who string) { h.env.renders.give(who) }

// A Content-Disposition that downloads the file as name (RFC 6266; a
// non-ASCII name goes as filename*).
func (h *McpHost) Attachment(name string) string {
	if v := mime.FormatMediaType("attachment", map[string]string{"filename": name}); v != "" {
		return v
	}
	return "attachment"
}

// What each caller is drawing now, per instance: at most max at once. A
// slot lasts at most lease, so one never given back (a request that
// failed midway) frees itself.
type renderSlots struct {
	mu    sync.Mutex
	max   int
	lease time.Duration
	held  map[string][]time.Time
}

func (r *renderSlots) take(who string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	keep := r.held[who][:0]
	for _, t := range r.held[who] {
		if now.Sub(t) < r.lease {
			keep = append(keep, t)
		}
	}
	if len(keep) >= r.max {
		r.held[who] = keep
		return false
	}
	r.held[who] = append(keep, now)
	return true
}

func (r *renderSlots) give(who string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h := r.held[who]; len(h) > 0 {
		r.held[who] = h[1:]
	}
	if len(r.held[who]) == 0 {
		delete(r.held, who)
	}
}

// at most a few keys: the cache is emptied when it grows past them
func (h *McpHost) CacheGet(key string) string {
	h.env.themesMu.Lock()
	defer h.env.themesMu.Unlock()
	return h.env.cache[key]
}

func (h *McpHost) CachePut(key, value string) {
	h.env.themesMu.Lock()
	defer h.env.themesMu.Unlock()
	if h.env.cache == nil || len(h.env.cache) > 16 {
		h.env.cache = map[string]string{}
	}
	h.env.cache[key] = value
}

func (h *McpHost) ISOTime(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}

// JavaScript's encodeURIComponent
func (h *McpHost) URIEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func (h *McpHost) ParseURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" {
		return ""
	}
	if (u.Scheme == "http" || u.Scheme == "https") && u.Host == "" {
		return ""
	}
	search := ""
	if u.RawQuery != "" {
		search = "?" + u.RawQuery
	}
	return toJSON(map[string]any{
		"scheme": strings.ToLower(u.Scheme), "host": strings.ToLower(u.Host), "hostname": strings.ToLower(u.Hostname()),
		"port": u.Port(), "path": u.EscapedPath(), "search": search, "hash": u.Fragment,
	})
}

func (h *McpHost) URLSet(s, params string) string {
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	var p map[string]string
	json.Unmarshal([]byte(params), &p)
	q := u.Query()
	for k, v := range p {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
