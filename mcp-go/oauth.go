// Optional sign-in (mcp/src/oauth.js): the MCP server is its own OAuth 2.1
// authorization server, and the person signs in with the Google account they
// use for PRO (Firebase Auth) on /oauth.html. Without a token every tool
// still works; with one, presentations are the person's own.
//
//   /.well-known/oauth-protected-resource[/mcp]   RFC 9728
//   /.well-known/oauth-authorization-server       RFC 8414
//   POST /oauth/register     dynamic client registration (RFC 7591)
//   GET  /oauth/authorize    checks the request, sends the browser to /oauth.html
//   POST /oauth/approve      /oauth.html hands back a Firebase ID token → code
//   POST /oauth/token        code + PKCE (S256) → tokens; refresh_token rotates
//
// Codes and tokens are random; Firestore keeps only their SHA-256, under
// mcp_oauth_…, which no client rule reaches. The documents are the ones the
// Node server writes, so either server can finish a sign-in the other began.

package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	SCOPE       = "decks"
	CODE_TTL    = 10 * 60 * 1000
	REQUEST_TTL = 15 * 60 * 1000
	ACCESS_TTL  = 60 * 60 * 1000
	REFRESH_TTL = 60 * 24 * 60 * 60 * 1000
)

type User struct {
	UID  string `json:"uid"`
	Name string `json:"name"`
}

// IDToken is what a verified Firebase ID token says about the person.
type IDToken struct{ UID, Name, Email string }

type OAuth struct {
	DB            DB
	VerifyIDToken func(ctx context.Context, token string) (*IDToken, error)
	Client        *http.Client
	Now           func() int64
}

// What an endpoint answers: a redirect, plain text, or JSON.
type OAuthResult struct {
	Status   int
	Redirect string
	Text     string
	JSON     any
}

func s256(verifier string) string {
	b, _ := hex.DecodeString(hashKey(verifier))
	return base64.RawURLEncoding.EncodeToString(b)
}

func loopback(u *url.URL) bool {
	h := u.Hostname()
	return u.Scheme == "http" && (h == "localhost" || h == "127.0.0.1" || h == "::1")
}

var reScheme = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)

// https anywhere, http only on this machine, or an app's own scheme (cursor://)
func redirectAllowed(uri string) bool {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme == "" || u.Fragment != "" || strings.Contains(uri, "#") {
		return false
	}
	if u.Scheme == "https" || loopback(u) {
		return true
	}
	switch u.Scheme {
	case "http", "javascript", "data", "file", "vbscript", "blob":
		return false
	}
	return reScheme.MatchString(u.Scheme)
}

// A loopback redirect may come back on any port (RFC 8252 §7.3).
func sameRedirect(registered, given string) bool {
	if registered == given {
		return true
	}
	a, err1 := url.Parse(registered)
	b, err2 := url.Parse(given)
	if err1 != nil || err2 != nil {
		return false
	}
	return loopback(a) && loopback(b) && a.Hostname() == b.Hostname() && a.Path == b.Path && a.RawQuery == b.RawQuery
}

var reOAuthPrivate = regexp.MustCompile(`^(localhost|127\.|10\.|0\.|169\.254\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|\[)`)

func (o *OAuth) now() int64 {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now().UnixMilli()
}

func col(name string) string { return "mcp_oauth_" + name }

var reClientID = regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`)

func (o *OAuth) client(ctx context.Context, clientID string) Doc {
	if strings.HasPrefix(clientID, "https://") {
		// a client ID metadata document: the id is the URL of its metadata
		u, err := url.Parse(clientID)
		if err != nil || reOAuthPrivate.MatchString(u.Host) || strings.HasSuffix(u.Hostname(), ".internal") || strings.HasSuffix(u.Hostname(), ".local") {
			return nil
		}
		rctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(rctx, "GET", clientID, nil)
		req.Header.Set("accept", "application/json")
		res, err := o.Client.Do(req)
		if err != nil {
			return nil
		}
		defer res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode > 299 {
			return nil
		}
		text, _ := io.ReadAll(io.LimitReader(res.Body, 20001))
		if len(text) > 20000 {
			return nil
		}
		var meta map[string]any
		if json.Unmarshal(text, &meta) != nil || meta["client_id"] != clientID {
			return nil
		}
		uris, ok := meta["redirect_uris"].([]any)
		if !ok {
			return nil
		}
		allowed := []any{}
		for _, r := range uris {
			if s, ok := r.(string); ok && redirectAllowed(s) {
				allowed = append(allowed, s)
			}
		}
		name := u.Hostname()
		if n := str(meta["client_name"]); n != "" {
			name = n
		}
		return Doc{"client_id": clientID, "client_name": name, "redirect_uris": allowed}
	}
	if !reClientID.MatchString(clientID) {
		return nil
	}
	d, _ := o.DB.Get(ctx, col("clients"), clientID)
	return d
}

func (o *OAuth) Metadata(origin string) map[string]any {
	return map[string]any{
		"issuer":                                origin,
		"authorization_endpoint":                origin + "/oauth/authorize",
		"token_endpoint":                        origin + "/oauth/token",
		"registration_endpoint":                 origin + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{SCOPE},
		"client_id_metadata_document_supported": true,
		"service_documentation":                 origin + "/connect.html",
	}
}

func (o *OAuth) ResourceMetadata(origin string) map[string]any {
	return map[string]any{
		"resource":                 origin + "/mcp",
		"authorization_servers":    []string{origin},
		"scopes_supported":         []string{SCOPE},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Sliqtly",
		"resource_documentation":   origin + "/connect.html",
	}
}

func bad(errCode, description string) OAuthResult {
	j := map[string]any{"error": errCode}
	if description != "" {
		j["error_description"] = description
	}
	return OAuthResult{Status: 400, JSON: j}
}

func (o *OAuth) Register(ctx context.Context, body map[string]any) (OAuthResult, error) {
	raw, _ := body["redirect_uris"].([]any)
	uris := []any{}
	for _, r := range raw {
		uris = append(uris, fmt.Sprint(r))
	}
	okAll := len(uris) > 0 && len(uris) <= 10
	for _, u := range uris {
		okAll = okAll && redirectAllowed(u.(string))
	}
	if !okAll {
		return bad("invalid_redirect_uri", "redirect_uris: https, a loopback http address, or an app scheme"), nil
	}
	name := "MCP client"
	if n, ok := body["client_name"]; ok && n != nil && fmt.Sprint(n) != "" {
		name = fmt.Sprint(n)
	}
	if r := []rune(name); len(r) > 100 {
		name = string(r[:100])
	}
	created := o.now()
	doc := Doc{"client_id": shortID(24), "client_name": name, "redirect_uris": uris, "created": created}
	if err := o.DB.Set(ctx, col("clients"), doc["client_id"].(string), doc); err != nil {
		return OAuthResult{}, err
	}
	return OAuthResult{Status: 201, JSON: map[string]any{
		"client_id": doc["client_id"], "client_name": name, "redirect_uris": uris,
		"client_id_issued_at": created / 1000,
		"grant_types":         []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
		"token_endpoint_auth_method": "none", "scope": SCOPE,
	}}, nil
}

// → a redirect to the sign-in page, or plain text when the request cannot be
// sent back to the client at all
func (o *OAuth) Authorize(ctx context.Context, q url.Values, origin string) (OAuthResult, error) {
	var c Doc
	if id := q.Get("client_id"); id != "" {
		c = o.client(ctx, id)
	}
	if c == nil {
		return OAuthResult{Status: 400, Text: "Unknown client_id."}, nil
	}
	registered := list(c["redirect_uris"])
	redirect := q.Get("redirect_uri")
	if redirect == "" && len(registered) == 1 {
		redirect = str(registered[0])
	}
	known := false
	for _, r := range registered {
		known = known || sameRedirect(str(r), redirect)
	}
	if redirect == "" || !known {
		return OAuthResult{Status: 400, Text: "redirect_uri is not registered for this client."}, nil
	}
	back := func(errCode, description string) OAuthResult {
		u, _ := url.Parse(redirect)
		p := u.Query()
		p.Set("error", errCode)
		if description != "" {
			p.Set("error_description", description)
		}
		if s := q.Get("state"); s != "" {
			p.Set("state", s)
		}
		p.Set("iss", origin)
		u.RawQuery = p.Encode()
		return OAuthResult{Redirect: u.String()}
	}
	if q.Get("response_type") != "code" {
		return back("unsupported_response_type", ""), nil
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		return back("invalid_request", "PKCE with S256 is required"), nil
	}
	rid := shortID(24)
	err := o.DB.Set(ctx, col("requests"), rid, Doc{
		"client_id": c["client_id"], "client_name": c["client_name"], "redirect_uri": redirect,
		"state": nilIfEmpty(q.Get("state")), "code_challenge": q.Get("code_challenge"),
		"resource": nilIfEmpty(q.Get("resource")), "origin": origin, "exp": o.now() + REQUEST_TTL,
	})
	if err != nil {
		return OAuthResult{}, err
	}
	// the page names where the code goes, since a registered name is the
	// client's own claim
	to, _ := url.Parse(redirect)
	where := strings.TrimSuffix(to.Scheme, ":") + " app"
	if loopback(to) {
		where = "this computer"
	} else if to.Scheme == "https" {
		where = to.Host
	}
	return OAuthResult{Redirect: fmt.Sprintf("%s/oauth.html?request=%s&client=%s&to=%s", origin, rid, encodeURIComponent(str(c["client_name"])), encodeURIComponent(where))}, nil
}

var reRequestID = regexp.MustCompile(`^[A-Za-z0-9]{24}$`)

// /oauth.html: { request, id_token } or { request, deny: true } → { redirect }
func (o *OAuth) Approve(ctx context.Context, body map[string]any) (OAuthResult, error) {
	rid := asString(body["request"])
	if !reRequestID.MatchString(rid) {
		return OAuthResult{Status: 400, JSON: map[string]any{"error": "invalid_request"}}, nil
	}
	expired := OAuthResult{Status: 400, JSON: map[string]any{"error": "invalid_request", "error_description": "The sign-in request has expired. Start again from your assistant."}}
	r, err := o.DB.Get(ctx, col("requests"), rid)
	if err != nil {
		return OAuthResult{}, err
	}
	if r == nil {
		return expired, nil
	}
	if num(r["exp"]) < o.now() {
		o.DB.Delete(ctx, col("requests"), rid)
		return expired, nil
	}
	u, _ := url.Parse(str(r["redirect_uri"]))
	p := u.Query()
	if s := str(r["state"]); s != "" {
		p.Set("state", s)
	}
	p.Set("iss", str(r["origin"]))
	if truthy(body["deny"]) {
		o.DB.Delete(ctx, col("requests"), rid)
		p.Set("error", "access_denied")
		u.RawQuery = p.Encode()
		return OAuthResult{Status: 200, JSON: map[string]any{"redirect": u.String()}}, nil
	}
	// a failed Google check leaves the request for another try
	who, err := o.VerifyIDToken(ctx, asString(body["id_token"]))
	if err != nil || who == nil {
		return OAuthResult{Status: 401, JSON: map[string]any{"error": "invalid_token", "error_description": "Google sign-in could not be verified."}}, nil
	}
	o.DB.Delete(ctx, col("requests"), rid)
	code := shortID(32)
	name := who.Name
	if name == "" {
		name = who.Email
	}
	err = o.DB.Set(ctx, col("codes"), hashKey(code), Doc{
		"uid": who.UID, "name": name, "client_id": r["client_id"], "redirect_uri": r["redirect_uri"],
		"code_challenge": r["code_challenge"], "resource": r["resource"], "exp": o.now() + CODE_TTL,
	})
	if err != nil {
		return OAuthResult{}, err
	}
	p.Set("code", code)
	u.RawQuery = p.Encode()
	return OAuthResult{Status: 200, JSON: map[string]any{"redirect": u.String()}}, nil
}

func (o *OAuth) issue(ctx context.Context, grant Doc) (map[string]any, error) {
	access := shortID(32)
	refresh := shortID(40)
	a := Doc{"kind": "access", "exp": o.now() + ACCESS_TTL}
	r := Doc{"kind": "refresh", "exp": o.now() + REFRESH_TTL}
	for k, v := range grant {
		a[k], r[k] = v, v
	}
	if err := o.DB.Set(ctx, col("tokens"), hashKey(access), a); err != nil {
		return nil, err
	}
	if err := o.DB.Set(ctx, col("tokens"), hashKey(refresh), r); err != nil {
		return nil, err
	}
	return map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": ACCESS_TTL / 1000, "refresh_token": refresh, "scope": SCOPE}, nil
}

func (o *OAuth) Token(ctx context.Context, b map[string]any) (OAuthResult, error) {
	switch asString(b["grant_type"]) {
	case "authorization_code":
		id := hashKey(asString(b["code"]))
		c, err := o.DB.Get(ctx, col("codes"), id)
		if err != nil {
			return OAuthResult{}, err
		}
		if c == nil {
			return bad("invalid_grant", ""), nil
		}
		o.DB.Delete(ctx, col("codes"), id)
		if num(c["exp"]) < o.now() {
			return bad("invalid_grant", "code expired"), nil
		}
		if cid := asString(b["client_id"]); cid != "" && cid != str(c["client_id"]) {
			return bad("invalid_grant", "client_id does not match"), nil
		}
		if ru := asString(b["redirect_uri"]); ru == "" || !sameRedirect(str(c["redirect_uri"]), ru) {
			return bad("invalid_grant", "redirect_uri does not match"), nil
		}
		if v := asString(b["code_verifier"]); v == "" || s256(v) != str(c["code_challenge"]) {
			return bad("invalid_grant", "PKCE verification failed"), nil
		}
		j, err := o.issue(ctx, Doc{"uid": c["uid"], "name": c["name"], "client_id": c["client_id"], "resource": c["resource"]})
		return OAuthResult{Status: 200, JSON: j}, err
	case "refresh_token":
		id := hashKey(asString(b["refresh_token"]))
		t, err := o.DB.Get(ctx, col("tokens"), id)
		if err != nil {
			return OAuthResult{}, err
		}
		if t == nil || str(t["kind"]) != "refresh" {
			return bad("invalid_grant", ""), nil
		}
		o.DB.Delete(ctx, col("tokens"), id)
		if num(t["exp"]) < o.now() {
			return bad("invalid_grant", "refresh token expired"), nil
		}
		if cid := asString(b["client_id"]); cid != "" && cid != str(t["client_id"]) {
			return bad("invalid_grant", "client_id does not match"), nil
		}
		j, err := o.issue(ctx, Doc{"uid": t["uid"], "name": t["name"], "client_id": t["client_id"], "resource": t["resource"]})
		return OAuthResult{Status: 200, JSON: j}, err
	}
	return bad("unsupported_grant_type", ""), nil
}

var reBearer = regexp.MustCompile(`(?i)^Bearer\s+(\S+)$`)

// Authorization: Bearer … → (user, true) | (nil, true) with no header |
// (nil, false) for a token that does not hold
func (o *OAuth) Who(ctx context.Context, header string) (*User, bool, error) {
	m := reBearer.FindStringSubmatch(strings.TrimSpace(header))
	if m == nil {
		return nil, true, nil
	}
	t, err := o.DB.Get(ctx, col("tokens"), hashKey(m[1]))
	if err != nil {
		return nil, false, err
	}
	if t == nil || str(t["kind"]) != "access" || num(t["exp"]) < o.now() {
		return nil, false, nil
	}
	return &User{UID: str(t["uid"]), Name: str(t["name"])}, true, nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// String(x) of a JSON or form value, "" for a missing one
func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	}
	return true
}
