// The Sliqtly MCP server (mcp/src/server.js): tools that turn Markdown (+ a
// theme, CSS and pictures) into a presentation at sliqtly.com and hand back
// its link.
//
// Tools: sliqtly_guide, create_presentation, update_presentation,
// get_presentation, list_presentations. create/update also name a UI
// resource (MCP Apps, and the same template for ChatGPT) that shows the deck
// inline in the chat.

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// assets/ is copied from mcp/ by `go generate` (and by the Dockerfile), so
// both servers hand out the same guide and preview.
//
//go:generate sh -c "mkdir -p assets && cp ../mcp/guide.md ../mcp/src/preview.html assets/"
//go:embed assets/guide.md
var GUIDE string

//go:embed assets/preview.html
var PREVIEW string

const (
	VERSION     = "1.0.0"
	PREVIEW_URI = "ui://sliqtly/preview.html"
	APP_MIME    = "text/html;profile=mcp-app"
)

// the preview loads Sliqtly from the site (or frames it); both domains serve it
var SITES = []string{"https://sliqtly.com", "https://sliqtly.web.app"}

// Sign-in is optional (ChatGPT reads this to offer both)
var EITHER = []any{map[string]any{"type": "noauth"}, map[string]any{"type": "oauth2", "scopes": []string{"decks"}}}

func uiMeta() mcp.Meta {
	return mcp.Meta{
		"securitySchemes":         EITHER,
		"ui":                      map[string]any{"resourceUri": PREVIEW_URI},
		"ui/resourceUri":          PREVIEW_URI,
		"openai/outputTemplate":   PREVIEW_URI,
		"openai/widgetAccessible": false,
	}
}

type ServerOpts struct {
	Store       Store
	BaseURL     string
	Client      *http.Client // pictures
	ThemeClient *http.Client
	Themes      *ThemeCache
}

// ThemeCache keeps the theme sheets for the life of the instance (the Node
// server fetches them again for every request).
type ThemeCache struct {
	mu sync.Mutex
	m  map[string]string
}

func (t *ThemeCache) get(ctx context.Context, client *http.Client, baseURL, theme string) (string, error) {
	t.mu.Lock()
	css, ok := t.m[theme]
	t.mu.Unlock()
	if ok {
		return css, nil
	}
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(rctx, "GET", fmt.Sprintf("%s/themes/%s.css", baseURL, theme), nil)
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("theme %s: %w", theme, err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return "", fmt.Errorf("theme %s: %d", theme, res.StatusCode)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	t.mu.Lock()
	if t.m == nil {
		t.m = map[string]string{}
	}
	t.m[theme] = string(b)
	t.mu.Unlock()
	return string(b), nil
}

// --- input schemas, as the Node server's zod gives them

func prop(typ, desc string) map[string]any {
	p := map[string]any{"type": typ}
	if desc != "" {
		p["description"] = desc
	}
	return p
}

func objectSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "$schema": "http://json-schema.org/draft-07/schema#"}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func deckFields(p map[string]any) map[string]any {
	theme := prop("string", "Theme: aurora (default), nebula, carbon, ember, midnight (dark); corporate, editorial (light)")
	theme["enum"] = THEMES
	mode := prop("string", "extend (default): css is added after the theme's rules. replace: css is the whole stylesheet.")
	mode["enum"] = []string{"extend", "replace"}
	images := prop("array", "Pictures the Markdown refers to as media/<name>")
	images["items"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        prop("string", `File name the Markdown uses as media/<name>, e.g. "team.jpg" → ![](media/team.jpg)`),
			"url":         prop("string", "A public https URL of the picture"),
			"data_base64": prop("string", "The picture's bytes as base64 (instead of url)"),
			"mime_type":   prop("string", "image/png, image/jpeg, image/gif, image/webp or image/svg+xml; inferred from the name when left out"),
		},
		"required": []string{"name"},
	}
	p["theme"] = theme
	p["css"] = prop("string", "CSS rules added on top of the theme (selectors: page, document, h1, h2, p, list, li, code, table, chart, diagram, .lead …). See sliqtly_guide.")
	p["css_mode"] = mode
	p["images"] = images
	return p
}

type deckArgs struct {
	DeckID   *string    `json:"deck_id"`
	EditKey  *string    `json:"edit_key"`
	Title    *string    `json:"title"`
	Markdown *string    `json:"markdown"`
	Theme    *string    `json:"theme"`
	CSS      *string    `json:"css"`
	CSSMode  *string    `json:"css_mode"`
	Images   []imageArg `json:"images"`
}

// The checks zod makes on the Node server.
func parseArgs(raw json.RawMessage, required ...string) (*deckArgs, error) {
	a := &deckArgs{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, a); err != nil {
			return nil, inputErr("Invalid arguments: %s", err.Error())
		}
	}
	have := map[string]bool{"deck_id": a.DeckID != nil, "title": a.Title != nil, "markdown": a.Markdown != nil}
	for _, r := range required {
		if !have[r] {
			return nil, inputErr("Invalid arguments: %s is required.", r)
		}
	}
	if a.Title != nil && len([]rune(*a.Title)) > 200 {
		return nil, inputErr("Invalid arguments: title is longer than 200 characters.")
	}
	if a.Theme != nil && !slices.Contains(THEMES, *a.Theme) {
		return nil, inputErr("Invalid arguments: theme is one of %s.", strings.Join(THEMES, ", "))
	}
	if a.CSSMode != nil && *a.CSSMode != "extend" && *a.CSSMode != "replace" {
		return nil, inputErr("Invalid arguments: css_mode is extend or replace.")
	}
	for _, img := range a.Images {
		if img.Name == "" {
			return nil, inputErr("Invalid arguments: every image needs a name.")
		}
	}
	return a, nil
}

func fail(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: message}}}
}

func text(s string) []mcp.Content { return []mcp.Content{&mcp.TextContent{Text: s}} }

// Caller is who is asking, for one request; the server itself is shared.
type Caller struct {
	User   *User
	SignIn string                   // resource metadata URL, when sign-in is on
	Limit  func(kind string) string // a reason to refuse, or ""
}

type callerKey struct{}

func withCaller(ctx context.Context, c *Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

func callerOf(ctx context.Context) *Caller {
	if c, ok := ctx.Value(callerKey{}).(*Caller); ok {
		return c
	}
	return &Caller{}
}

// One server answers every request: the per-request part (who signed in,
// the rate limit key) comes with the request's context, so the tool
// definitions are built once per instance, not per request.
func NewServer(o ServerOpts) *mcp.Server {
	frames := []string{o.BaseURL}
	for _, s := range SITES {
		if !slices.Contains(frames, s) {
			frames = append(frames, s)
		}
	}

	// The stylesheet stored with the deck: nil keeps the theme as it is.
	sheet := func(ctx context.Context, theme string, css *string, mode string) (*string, error) {
		if css == nil || strings.TrimSpace(*css) == "" {
			if mode == "replace" {
				empty := ""
				return &empty, nil
			}
			return nil, nil
		}
		if len(*css) > MAX_CSS {
			return nil, inputErr("css is larger than 100 KB.")
		}
		if mode == "replace" {
			return css, nil
		}
		base, err := o.Themes.get(ctx, o.ThemeClient, o.BaseURL, theme)
		if err != nil {
			return nil, err
		}
		s := base + "\n/* --- added for this deck --- */\n" + *css + "\n"
		return &s, nil
	}

	links := func(id string) map[string]any {
		return map[string]any{"share_url": fmt.Sprintf("%s/s/%s", o.BaseURL, id), "edit_url": fmt.Sprintf("%s/s/%s?edit", o.BaseURL, id)}
	}

	linkOnly := func(md, theme string, css *string) map[string]any {
		q := "md=" + packText(md)
		if theme != "" {
			q += "&theme=" + encodeForm(theme)
		}
		if css != nil {
			q += "&css=" + packText(*css)
		}
		return map[string]any{"share_url": fmt.Sprintf("%s/#%s&mode=show", o.BaseURL, q), "edit_url": fmt.Sprintf("%s/#%s", o.BaseURL, q)}
	}

	result := func(ctx context.Context, out map[string]any, verb string) *mcp.CallToolResult {
		user := callerOf(ctx).User
		lines := []string{
			fmt.Sprintf(`%s: "%s" (%d slides, theme %s).`, verb, out["title"], out["slides"], out["theme"]),
			fmt.Sprintf("Presentation: %s", out["share_url"]),
			fmt.Sprintf("Open in the editor: %s", out["edit_url"]),
		}
		if user != nil {
			name := user.Name
			if name == "" {
				name = "the signed-in user"
			}
			lines = append(lines, fmt.Sprintf("Saved in the Sliqtly account of %s.", name))
		}
		if id, ok := out["deck_id"]; ok {
			lines = append(lines, fmt.Sprintf("deck_id: %s", id))
		}
		if key, ok := out["edit_key"]; ok {
			lines = append(lines, fmt.Sprintf("edit_key: %s (needed for update_presentation; do not show it to others)", key))
		}
		for _, w := range out["warnings"].([]string) {
			lines = append(lines, "Note: "+w)
		}
		lines = append(lines, "Give the user the presentation link.")
		return &mcp.CallToolResult{Content: text(strings.Join(lines, "\n")), StructuredContent: out, Meta: uiMeta()}
	}

	guarded := func(kind string, fn func(ctx context.Context, raw json.RawMessage) (*mcp.CallToolResult, error)) mcp.ToolHandler {
		return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if c := callerOf(ctx); c.Limit != nil {
				if why := c.Limit(kind); why != "" {
					return fail(why), nil
				}
			}
			r, err := fn(ctx, req.Params.Arguments)
			if err != nil {
				var ie *InputError
				if errors.As(err, &ie) {
					return fail(ie.msg), nil
				}
				log.Printf("%s failed: %v", kind, err)
				return fail(fmt.Sprintf("Sliqtly could not %s: %s", strings.Replace(kind, "_", " ", 1), err.Error())), nil
			}
			return r, nil
		}
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "sliqtly", Title: "Sliqtly", Version: VERSION, WebsiteURL: o.BaseURL}, &mcp.ServerOptions{
		Instructions: "Sliqtly turns Markdown into animated slide presentations. Call sliqtly_guide once for the syntax, then create_presentation. Always give the user the returned presentation link.",
	})

	f := false
	t := true
	server.AddTool(&mcp.Tool{
		Name:        "sliqtly_guide",
		Title:       "Sliqtly syntax guide",
		Description: "How to write a Sliqtly deck: slide structure, build animations, effects, pictures, charts (Vega-Lite), diagrams (Mermaid, Graphviz), math, themes and the CSS selectors. Read before the first create_presentation.",
		InputSchema: objectSchema(map[string]any{}),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &f},
		Meta:        mcp.Meta{"securitySchemes": EITHER},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: text(GUIDE)}, nil
	})

	server.AddTool(&mcp.Tool{
		Name:        "create_presentation",
		Title:       "Create a presentation",
		Description: "Create a Sliqtly slide presentation from Markdown (# title slide, ## per slide), an optional theme, extra CSS and pictures. Returns a link that opens the presentation and a link to edit a copy in the Sliqtly editor.",
		InputSchema: objectSchema(deckFields(map[string]any{
			"title":    map[string]any{"type": "string", "maxLength": 200, "description": "The presentation's name"},
			"markdown": prop("string", "The whole deck as Sliqtly Markdown"),
		}), "title", "markdown"),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &f, IdempotentHint: false, OpenWorldHint: &t},
		Meta:        uiMeta(),
	}, guarded("create_presentation", func(ctx context.Context, raw json.RawMessage) (*mcp.CallToolResult, error) {
		a, err := parseArgs(raw, "title", "markdown")
		if err != nil {
			return nil, err
		}
		md, theme, mode := *a.Markdown, or(a.Theme, "aurora"), or(a.CSSMode, "extend")
		if len(md) > MAX_MD {
			return nil, inputErr("markdown is larger than 300 KB.")
		}
		if strings.TrimSpace(md) == "" {
			return nil, inputErr("markdown is empty.")
		}
		css2, err := sheet(ctx, theme, a.CSS, mode)
		if err != nil {
			return nil, err
		}
		imgs, err := loadImages(ctx, a.Images, o.Client)
		if err != nil {
			return nil, err
		}
		names := []string{}
		for _, i := range imgs {
			names = append(names, i.Name)
		}
		titles, _ := outline(md)
		out := map[string]any{"title": *a.Title, "theme": theme, "slides": len(titles), "warnings": warnings(md, names, nil)}
		if o.Store.Kind() == "link" {
			if len(imgs) > 0 {
				out["warnings"] = append(out["warnings"].([]string), "Pictures are not stored on this server (no cloud storage configured); the slides show without them.")
			}
			for k, v := range linkOnly(md, theme, css2) {
				out[k] = v
			}
			return result(ctx, out, "Created"), nil
		}
		owner := "mcp"
		if user := callerOf(ctx).User; user != nil {
			owner = user.UID
		}
		id, key, err := o.Store.Create(ctx, DeckWrite{Name: a.Title, MD: &md, Theme: &theme, CSS: OptStr{Set: true, Val: css2}, Images: imgs}, owner)
		if err != nil {
			return nil, err
		}
		for k, v := range links(id) {
			out[k] = v
		}
		out["deck_id"], out["edit_key"] = id, key
		return result(ctx, out, "Created"), nil
	}))

	server.AddTool(&mcp.Tool{
		Name:        "update_presentation",
		Title:       "Update a presentation",
		Description: "Change a presentation made with create_presentation, keeping its link. Send only what changes: markdown replaces the whole text; images are added (or replace pictures of the same name).",
		InputSchema: objectSchema(deckFields(map[string]any{
			"deck_id":  prop("string", "deck_id from create_presentation"),
			"edit_key": prop("string", "edit_key from create_presentation; not needed when signed in as the presentation's owner"),
			"title":    map[string]any{"type": "string", "maxLength": 200},
			"markdown": prop("string", "The whole deck as Sliqtly Markdown"),
		}), "deck_id"),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &t, IdempotentHint: true, OpenWorldHint: &t},
		Meta:        uiMeta(),
	}, guarded("update_presentation", func(ctx context.Context, raw json.RawMessage) (*mcp.CallToolResult, error) {
		if o.Store.Kind() == "link" {
			return nil, inputErr("This server keeps no decks (no cloud storage configured): call create_presentation again with the whole deck.")
		}
		a, err := parseArgs(raw, "deck_id")
		if err != nil {
			return nil, err
		}
		if a.Markdown != nil && len(*a.Markdown) > MAX_MD {
			return nil, inputErr("markdown is larger than 300 KB.")
		}
		id := *a.DeckID
		cur, err := o.Store.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		user := callerOf(ctx).User
		mine := cur != nil && user != nil && str(cur["owner"]) == user.UID
		if cur == nil || (str(cur["source"]) != "mcp" && !mine) {
			return nil, inputErr("No presentation %s that this server can change.", id)
		}
		if !mine && (a.EditKey == nil || *a.EditKey == "") {
			return nil, inputErr("edit_key is needed: the presentation is not this signed-in user's own.")
		}
		th := "aurora"
		if a.Theme != nil {
			th = *a.Theme
		} else if s, ok := cur["theme"].(string); ok {
			th = s
		}
		// a new theme with no new css drops the old theme's sheet
		var css2 OptStr
		if a.CSS != nil {
			v, err := sheet(ctx, th, a.CSS, or(a.CSSMode, "extend"))
			if err != nil {
				return nil, err
			}
			css2 = OptStr{Set: true, Val: v}
		} else if a.Theme != nil && *a.Theme != str(cur["theme"]) {
			css2 = OptStr{Set: true}
		}
		imgs, err := loadImages(ctx, a.Images, o.Client)
		if err != nil {
			return nil, err
		}
		key := a.EditKey
		if mine {
			key = nil
		}
		saved, err := o.Store.Update(ctx, id, key, DeckWrite{Name: a.Title, MD: a.Markdown, Theme: a.Theme, CSS: css2, Images: imgs})
		if err != nil {
			return nil, err
		}
		if saved == nil {
			return nil, inputErr("The edit_key does not match this presentation.")
		}
		stored := []string{}
		for _, f := range list(saved["files"]) {
			stored = append(stored, strings.TrimPrefix(str(mapOf(f)["path"]), "media/"))
		}
		md := str(saved["md"])
		ws := []string{}
		for _, w := range warnings(md, nil, stored) {
			if !strings.HasPrefix(w, "Image") {
				ws = append(ws, w)
			}
		}
		titles, _ := outline(md)
		out := map[string]any{"title": saved["name"], "theme": saved["theme"], "slides": len(titles), "warnings": ws, "deck_id": id}
		for k, v := range links(id) {
			out[k] = v
		}
		return result(ctx, out, "Updated"), nil
	}))

	reDeck := regexp.MustCompile(`^[A-Za-z0-9]{6,32}$`)
	server.AddTool(&mcp.Tool{
		Name:        "get_presentation",
		Title:       "Read a presentation",
		Description: "Read a Sliqtly presentation's Markdown, theme, CSS and picture list by its deck_id (the id in https://sliqtly.com/s/<id>), to revise it.",
		InputSchema: objectSchema(map[string]any{"deck_id": prop("string", "The id in the share link /s/<id>")}, "deck_id"),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &f},
		Meta:        mcp.Meta{"securitySchemes": EITHER},
	}, guarded("get_presentation", func(ctx context.Context, raw json.RawMessage) (*mcp.CallToolResult, error) {
		if o.Store.Kind() == "link" {
			return nil, inputErr("This server keeps no decks (no cloud storage configured).")
		}
		a, err := parseArgs(raw, "deck_id")
		if err != nil {
			return nil, err
		}
		id := *a.DeckID
		if !reDeck.MatchString(id) {
			return nil, inputErr("deck_id is the 10-character id in the share link.")
		}
		d, err := o.Store.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if d == nil {
			return nil, inputErr("No presentation %s.", id)
		}
		images := []any{}
		names := []string{}
		for _, f := range list(d["files"]) {
			m := mapOf(f)
			n := strings.TrimPrefix(str(m["path"]), "media/")
			names = append(names, n)
			images = append(images, map[string]any{"name": n, "type": m["type"], "size": m["size"]})
		}
		var css any
		if c, ok := d["css"].(string); ok {
			css = c
		}
		out := map[string]any{"title": str(d["name"]), "theme": str(d["theme"]), "markdown": str(d["md"]), "css": css, "images": images}
		for k, v := range links(id) {
			out[k] = v
		}
		themeName := str(d["theme"])
		if themeName == "" {
			themeName = "(document's own)"
		}
		pics := strings.Join(names, ", ")
		if pics == "" {
			pics = "none"
		}
		parts := []string{fmt.Sprintf(`"%s", theme %s, %d pictures: %s.`, out["title"], themeName, len(images), pics)}
		if css != nil {
			parts = append(parts, "It has its own stylesheet (css below).")
		}
		parts = append(parts, "```markdown", str(d["md"]), "```")
		if css != nil {
			parts = append(parts, "```css\n"+css.(string)+"\n```")
		}
		return &mcp.CallToolResult{Content: text(strings.Join(nonEmpty(parts), "\n")), StructuredContent: out}, nil
	}))

	server.AddTool(&mcp.Tool{
		Name:        "list_presentations",
		Title:       "List my presentations",
		Description: "List the signed-in user's own Sliqtly presentations (newest first) with their links and deck_ids. Needs sign-in.",
		InputSchema: objectSchema(map[string]any{}),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &f},
		Meta:        mcp.Meta{"securitySchemes": []any{map[string]any{"type": "oauth2", "scopes": []string{"decks"}}}},
	}, guarded("list_presentations", func(ctx context.Context, _ json.RawMessage) (*mcp.CallToolResult, error) {
		c := callerOf(ctx)
		user := c.User
		if user == nil {
			r := fail("Listing presentations needs sign-in. Connect Sliqtly with sign-in (Google) to keep presentations in your own account.")
			if c.SignIn != "" {
				r.Meta = mcp.Meta{"mcp/www_authenticate": []string{fmt.Sprintf(`Bearer resource_metadata="%s", error="insufficient_scope", error_description="Sign in to Sliqtly to list your presentations"`, c.SignIn)}}
			}
			return r, nil
		}
		if o.Store.Kind() == "link" {
			return nil, inputErr("This server keeps no decks (no cloud storage configured).")
		}
		docs, err := o.Store.List(ctx, user.UID, 50)
		if err != nil {
			return nil, err
		}
		decks := []any{}
		lines := []string{}
		for _, d := range docs {
			id := str(d["id"])
			deck := map[string]any{"deck_id": id, "title": str(d["name"]), "theme": str(d["theme"]), "updated": d["updated"]}
			for k, v := range links(id) {
				deck[k] = v
			}
			decks = append(decks, deck)
			title := str(d["name"])
			if title == "" {
				title = "(untitled)"
			}
			lines = append(lines, fmt.Sprintf("- %s (%s): %s", title, id, deck["share_url"]))
		}
		txt := strings.Join(lines, "\n")
		if len(decks) == 0 {
			txt = "No presentations yet."
		}
		return &mcp.CallToolResult{Content: text(txt), StructuredContent: map[string]any{"presentations": decks}}, nil
	}))

	// the preview runs Sliqtly's viewer itself (preview.html): its scripts and
	// fonts from the site and Firebase's CDN, the share from Firestore, the
	// pictures from Storage
	resources := append(slices.Clone(frames), "https://www.gstatic.com")
	connects := append(slices.Clone(frames), "https://firestore.googleapis.com", "https://firebasestorage.googleapis.com")
	csp := map[string]any{"csp": map[string]any{"frameDomains": frames, "resourceDomains": resources, "connectDomains": connects}, "prefersBorder": false}
	server.AddResource(&mcp.Resource{
		Name:        "preview",
		URI:         PREVIEW_URI,
		Title:       "Sliqtly presentation",
		Description: "Shows the presentation inline",
		MIMEType:    APP_MIME,
		Meta:        mcp.Meta{"ui": csp},
	}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      PREVIEW_URI,
			MIMEType: APP_MIME,
			Text:     PREVIEW,
			Meta: mcp.Meta{
				"ui":                         csp,
				"openai/widgetCSP":           map[string]any{"connect_domains": connects, "resource_domains": resources, "frame_domains": frames},
				"openai/widgetDescription":   "The Sliqtly presentation, playable inline.",
				"openai/widgetPrefersBorder": true,
			},
		}}}, nil
	})

	return server
}

func or(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

func nonEmpty(l []string) []string {
	out := []string{}
	for _, s := range l {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// URLSearchParams' encoding of one value
func encodeForm(s string) string {
	return strings.ReplaceAll(encodeURIComponent(s), "%20", "+")
}
