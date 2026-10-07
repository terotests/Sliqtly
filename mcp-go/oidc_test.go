// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// A provider of one's own for the tests: discovery, JWKS, an authorize
// endpoint that signs in at once, and a token endpoint that checks the
// server's secret and PKCE before it hands out an ID token signed with
// its RSA (or EC) key. claims may change the token's claims.
type fakeProvider struct {
	srv    *httptest.Server
	rsaKey *rsa.PrivateKey
	ecKey  *ecdsa.PrivateKey
	mu     sync.Mutex
	codes  map[string]fakeCode
	email  string
	alg    string // RS256 (default) or ES256
	claims func(map[string]any)
	// what the token endpoint was sent last
	gotSecret bool
	jwksReads int
	extraKey  *rsa.PrivateKey // offered from the keys once set (rotation)
}

type fakeCode struct{ nonce, challenge, redirect string }

const fakeClientID, fakeSecret = "sliqtly-server", "provider-secret"

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ek, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	f := &fakeProvider{rsaKey: rk, ecKey: ek, codes: map[string]fakeCode{}, email: "ada@example.com", alg: "RS256"}
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.srv.URL,
			"authorization_endpoint":                f.srv.URL + "/authorize",
			"token_endpoint":                        f.srv.URL + "/token",
			"jwks_uri":                              f.srv.URL + "/jwks",
			"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.jwksReads++
		extra := f.extraKey
		f.mu.Unlock()
		b := base64.RawURLEncoding
		keys := []map[string]any{
			{"kty": "RSA", "kid": "r1", "use": "sig", "alg": "RS256", "n": b.EncodeToString(rk.N.Bytes()), "e": b.EncodeToString(big.NewInt(int64(rk.E)).Bytes())},
			{"kty": "EC", "kid": "e1", "use": "sig", "crv": "P-256", "x": b.EncodeToString(pad32(ek.X.Bytes())), "y": b.EncodeToString(pad32(ek.Y.Bytes()))},
		}
		if extra != nil {
			keys = append(keys, map[string]any{"kty": "RSA", "kid": "r2", "n": b.EncodeToString(extra.N.Bytes()), "e": "AQAB"})
		}
		json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("client_id") != fakeClientID || q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" || !strings.Contains(q.Get("scope"), "openid") {
			http.Error(w, "bad authorize request", 400)
			return
		}
		code := randomToken(12)
		f.mu.Lock()
		f.codes[code] = fakeCode{q.Get("nonce"), q.Get("code_challenge"), q.Get("redirect_uri")}
		f.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code="+code+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		id, secret, ok := r.BasicAuth()
		f.mu.Lock()
		c, known := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		f.gotSecret = ok && id == fakeClientID && secret == fakeSecret
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !known || !f.gotSecret || base64.RawURLEncoding.EncodeToString(sum[:]) != c.challenge || r.PostForm.Get("redirect_uri") != c.redirect {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		now := time.Now().Unix()
		claims := map[string]any{"iss": f.srv.URL, "sub": "user-1", "aud": fakeClientID, "exp": now + 300, "iat": now, "nonce": c.nonce, "email": f.email, "email_verified": true, "name": "Ada"}
		if f.claims != nil {
			f.claims(claims)
		}
		json.NewEncoder(w).Encode(map[string]any{"id_token": f.sign(claims, f.alg), "access_token": "provider-at", "token_type": "Bearer"})
	})
	return f
}

func pad32(b []byte) []byte {
	return append(make([]byte, 32-len(b)), b...)
}

// a JWT with the provider's key for alg
func (f *fakeProvider) sign(claims map[string]any, alg string) string {
	kid := "r1"
	if alg == "ES256" {
		kid = "e1"
	}
	return signJWT(claims, alg, kid, f.rsaKey, f.ecKey)
}

func signJWT(claims map[string]any, alg, kid string, rk *rsa.PrivateKey, ek *ecdsa.PrivateKey) string {
	b := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]any{"alg": alg, "kid": kid, "typ": "JWT"})
	c, _ := json.Marshal(claims)
	in := b.EncodeToString(h) + "." + b.EncodeToString(c)
	sum := sha256.Sum256([]byte(in))
	var sig []byte
	if alg == "ES256" {
		r, s, _ := ecdsa.Sign(rand.Reader, ek, sum[:])
		sig = append(pad32(r.Bytes()), pad32(s.Bytes())...)
	} else {
		sig, _ = rsa.SignPKCS1v15(rand.Reader, rk, crypto.SHA256, sum[:])
	}
	return in + "." + b.EncodeToString(sig)
}

func (f *fakeProvider) config(allow ...string) oidcConfig {
	return oidcConfig{Issuer: f.srv.URL, ClientID: fakeClientID, ClientSecret: fakeSecret, Allow: allow}
}

// a server with sign-in through f, and the token too
func startOIDC(t *testing.T, f *fakeProvider, allow ...string) (*httptest.Server, *localServer) {
	t.Helper()
	return startV1(t, "static-token", func(ls *localServer) {
		ls.cors, _ = newCORSPolicy([]string{"https://editor.example.com"})
		if err := ls.useOIDC(f.config(allow...)); err != nil {
			t.Fatal(err)
		}
	})
}

// a browser: follows redirects until one goes to the app's own address
// (appHost), where it stops and hands the address back
type browser struct {
	t       *testing.T
	c       *http.Client
	appHost string
}

func newBrowser(t *testing.T, appHost string) *browser {
	b := &browser{t: t, appHost: appHost}
	b.c = &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Host == appHost {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	return b
}

// the last answer: its status and body, and where it sent the browser
func (b *browser) open(u string) (int, string, *url.URL) {
	b.t.Helper()
	res, err := b.c.Get(u)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	loc, _ := res.Location()
	return res.StatusCode, string(body), loc
}

type pkce struct{ verifier, challenge string }

func newPKCE() pkce {
	v := randomToken(32)
	sum := sha256.Sum256([]byte(v))
	return pkce{v, base64.RawURLEncoding.EncodeToString(sum[:])}
}

func authorizeURL(base, client, redirect string, p pkce) string {
	return base + "/oauth/authorize?" + url.Values{
		"client_id": {client}, "redirect_uri": {redirect}, "response_type": {"code"},
		"code_challenge": {p.challenge}, "code_challenge_method": {"S256"}, "state": {"app-state"},
	}.Encode()
}

func tokenCall(t *testing.T, base string, form url.Values, headers ...string) v1Answer {
	t.Helper()
	req, _ := http.NewRequest("POST", base+"/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	a := v1Answer{status: res.StatusCode, header: res.Header, raw: string(raw)}
	json.Unmarshal(raw, &a.body)
	return a
}

const appRedirect = "http://127.0.0.1:47123/callback"

// the desktop app's sign-in from start to end; → the app's callback URL
func desktopSignIn(t *testing.T, base string, p pkce) *url.URL {
	t.Helper()
	b := newBrowser(t, "127.0.0.1:47123")
	status, body, loc := b.open(authorizeURL(base, "sliqtly-desktop", appRedirect, p))
	if status != 302 || loc == nil {
		t.Fatalf("sign-in ended with %d: %.300s", status, body)
	}
	return loc
}

// authorize → provider → callback → code → tokens → /api/v1 and /mcp,
// then refresh token rotation
func TestOIDCSignIn(t *testing.T) {
	f := newFakeProvider(t)
	srv, _ := startOIDC(t, f, "@example.com")

	info := v1Do(t, "GET", srv.URL+"/api/v1/info", nil).want(t, 200)
	auth := info.body["auth"].(map[string]any)
	eq(t, auth["oauth"], true)
	eq(t, auth["issuer"], srv.URL)
	eq(t, auth["provider"], strings.TrimPrefix(f.srv.URL, "http://"))
	meta := v1Do(t, "GET", srv.URL+"/.well-known/oauth-authorization-server", nil).want(t, 200)
	eq(t, meta.str("token_endpoint"), srv.URL+"/oauth/token")

	// without a token: told where to sign in
	none := v1Do(t, "GET", srv.URL+"/api/v1/decks", nil).want(t, 401)
	if !strings.Contains(none.header.Get("WWW-Authenticate"), `resource_metadata="`+srv.URL+`/.well-known/oauth-protected-resource"`) {
		t.Fatalf("WWW-Authenticate: %q", none.header.Get("WWW-Authenticate"))
	}
	res, _ := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader("{}"))
	res.Body.Close()
	eq(t, res.StatusCode, 401)
	if !strings.Contains(res.Header.Get("WWW-Authenticate"), "/.well-known/oauth-protected-resource/mcp") {
		t.Fatalf("/mcp WWW-Authenticate: %q", res.Header.Get("WWW-Authenticate"))
	}

	p := newPKCE()
	back := desktopSignIn(t, srv.URL, p)
	q := back.Query()
	if q.Get("code") == "" || q.Get("state") != "app-state" || q.Get("iss") != srv.URL {
		t.Fatalf("back at the app with %s", back)
	}
	if !f.gotSecret {
		t.Fatal("the provider was not sent the client secret")
	}
	// the redirect may come back on another loopback port, never another host
	tok := tokenCall(t, srv.URL, url.Values{"grant_type": {"authorization_code"}, "code": {q.Get("code")}, "redirect_uri": {appRedirect}, "code_verifier": {p.verifier}, "client_id": {"sliqtly-desktop"}}).want(t, 200)
	access, refresh := tok.str("access_token"), tok.str("refresh_token")
	if access == "" || refresh == "" {
		t.Fatalf("tokens: %s", tok.raw)
	}
	// the code is spent
	tokenCall(t, srv.URL, url.Values{"grant_type": {"authorization_code"}, "code": {q.Get("code")}, "redirect_uri": {appRedirect}, "code_verifier": {p.verifier}}).want(t, 400)

	me := v1Do(t, "GET", srv.URL+"/api/v1/me", nil, "Authorization", "Bearer "+access).want(t, 200)
	eq(t, me.str("email"), "ada@example.com")
	eq(t, me.str("name"), "Ada")
	eq(t, me.str("id"), "user-1")
	v1Do(t, "POST", srv.URL+"/api/v1/decks", map[string]any{"name": "Signed in", "markdown": "# Hi"}, "Authorization", "Bearer "+access).want(t, 201)
	mcpWorks(t, srv.URL, access)
	// the server's token still works beside sign-in
	v1Do(t, "GET", srv.URL+"/api/v1/decks", nil, "Authorization", "Bearer static-token").want(t, 200)
	mcpWorks(t, srv.URL, "static-token")

	// only hashes are kept
	_, ids, _ := srvLocal(t, srv).env.DB.WhereEq(context.Background(), "mcp_oauth_tokens", "client_id", "sliqtly-desktop")
	hashed := false
	for _, id := range ids {
		if id == access || id == refresh {
			t.Fatal("a token kept as itself")
		}
		hashed = hashed || id == sha256Hex(access)
	}
	if !hashed {
		t.Fatalf("the access token is not kept by its hash: %v", ids)
	}

	// rotation: the new pair works, the old refresh token is spent
	again := tokenCall(t, srv.URL, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {"sliqtly-desktop"}}).want(t, 200)
	if again.str("refresh_token") == refresh || again.str("access_token") == "" {
		t.Fatalf("refresh: %s", again.raw)
	}
	tokenCall(t, srv.URL, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}).want(t, 400)
	v1Do(t, "GET", srv.URL+"/api/v1/decks", nil, "Authorization", "Bearer "+again.str("access_token")).want(t, 200)

	// the provider's ID token itself is no token here, nor is /oauth/approve open
	v1Do(t, "GET", srv.URL+"/api/v1/decks", nil, "Authorization", "Bearer provider-at").want(t, 401)
	v1Do(t, "POST", srv.URL+"/oauth/approve", map[string]any{"request": "x"}).want(t, 404)
}

func srvLocal(t *testing.T, srv *httptest.Server) *localServer {
	ls, ok := srv.Config.Handler.(*localServer)
	if !ok {
		t.Fatal("not a local server")
	}
	return ls
}

// what the app is told when the sign-in does not hold: no code, and why
func TestOIDCRefused(t *testing.T) {
	cases := map[string]func(f *fakeProvider){
		"bad nonce":      func(f *fakeProvider) { f.claims = func(c map[string]any) { c["nonce"] = "other" } },
		"wrong audience": func(f *fakeProvider) { f.claims = func(c map[string]any) { c["aud"] = "someone-else" } },
		"expired": func(f *fakeProvider) {
			f.claims = func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }
		},
		"wrong issuer": func(f *fakeProvider) { f.claims = func(c map[string]any) { c["iss"] = "https://elsewhere.example" } },
		"not allowed":  func(f *fakeProvider) { f.email = "eve@elsewhere.example" },
		"unverified":   func(f *fakeProvider) { f.claims = func(c map[string]any) { c["email_verified"] = false } },
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeProvider(t)
			set(f)
			srv, _ := startOIDC(t, f, "@example.com")
			back := desktopSignIn(t, srv.URL, newPKCE())
			q := back.Query()
			if q.Get("code") != "" || q.Get("error") != "access_denied" || q.Get("state") != "app-state" {
				t.Fatalf("back at the app with %s", back)
			}
			if name == "not allowed" && !strings.Contains(q.Get("error_description"), "eve@elsewhere.example") {
				t.Fatalf("why: %q", q.Get("error_description"))
			}
		})
	}
}

// the client's side of the server's OAuth: PKCE, redirect addresses,
// a client that is not built in, the token endpoint from a page
func TestOIDCClients(t *testing.T) {
	f := newFakeProvider(t)
	srv, _ := startOIDC(t, f, "ada@example.com")

	p := newPKCE()
	code := desktopSignIn(t, srv.URL, p).Query().Get("code")
	bad := tokenCall(t, srv.URL, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {appRedirect}, "code_verifier": {"not-the-verifier"}}).want(t, 400)
	eq(t, bad.str("error"), "invalid_grant")

	b := newBrowser(t, "never")
	for _, c := range []struct{ client, redirect string }{
		{"sliqtly-desktop", "https://evil.example/callback"},
		{"sliqtly-desktop", "http://127.0.0.1:47123/elsewhere"},
		{"sliqtly-web", "https://evil.example/callback.html"},
		{"sliqtly-web", "https://editor.example.com/other.html"},
		{"nobody-registered", appRedirect},
	} {
		status, body, _ := b.open(authorizeURL(srv.URL, c.client, c.redirect, p))
		if status != 400 {
			t.Fatalf("%s → %s: %d %.200s", c.client, c.redirect, status, body)
		}
	}
	// the web editor on a listed origin is sent on to the provider
	wb := newBrowser(t, "editor.example.com")
	status, body, loc := wb.open(authorizeURL(srv.URL, "sliqtly-web", "https://editor.example.com/callback.html", p))
	if status != 302 || loc == nil || loc.Query().Get("code") == "" {
		t.Fatalf("web sign-in: %d %v %.200s", status, loc, body)
	}

	// a registered client is shown to the person first
	reg := v1Do(t, "POST", srv.URL+"/oauth/register", map[string]any{"client_name": "Some Tool", "redirect_uris": []string{"http://127.0.0.1/cb"}}).want(t, 201)
	ob := newBrowser(t, "127.0.0.1:47999")
	status, body, _ = ob.open(authorizeURL(srv.URL, reg.str("client_id"), "http://127.0.0.1:47999/cb", p))
	if status != 200 || !strings.Contains(body, "Some Tool") || !strings.Contains(body, `action="/oauth/start"`) {
		t.Fatalf("consent page: %d %.300s", status, body)
	}
	rid := body[strings.Index(body, `name="request" value="`)+len(`name="request" value="`):]
	rid = rid[:strings.Index(rid, `"`)]
	// the page's Continue, but from another site: refused
	form := url.Values{"request": {rid}}
	req, _ := http.NewRequest("POST", srv.URL+"/oauth/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	eq(t, res.StatusCode, 403)
	// from the page itself: on to the provider and back to the tool
	req, _ = http.NewRequest("POST", srv.URL+"/oauth/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	res, err := ob.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	loc, _ = res.Location()
	if res.StatusCode != 302 || loc == nil || loc.Query().Get("code") == "" {
		t.Fatalf("after consent: %d %v", res.StatusCode, loc)
	}

	// /oauth/token from a page on the list, and from one that is not
	pre := v1Do(t, "OPTIONS", srv.URL+"/oauth/token", nil, "Origin", "https://editor.example.com", "Access-Control-Request-Method", "POST").want(t, 204)
	eq(t, pre.header.Get("Access-Control-Allow-Origin"), "https://editor.example.com")
	v1Do(t, "OPTIONS", srv.URL+"/oauth/token", nil, "Origin", "https://evil.example", "Access-Control-Request-Method", "POST").want(t, 403)
	cross := tokenCall(t, srv.URL, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"nope"}}, "Origin", "https://editor.example.com").want(t, 400)
	eq(t, cross.header.Get("Access-Control-Allow-Origin"), "https://editor.example.com")
	tokenCall(t, srv.URL, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"nope"}}, "Origin", "https://evil.example").want(t, 403)
}

// The ID token checks one by one, with ES256 and a key the provider
// rotated in
func TestOIDCVerify(t *testing.T) {
	f := newFakeProvider(t)
	p := newOIDCProvider(f.config("*"))
	ctx := context.Background()
	now := time.Now().Unix()
	base := func() map[string]any {
		return map[string]any{"iss": f.srv.URL, "sub": "u", "aud": fakeClientID, "exp": now + 60, "iat": now, "nonce": "n1", "email": "A@Example.com"}
	}
	c, err := p.verifyIDToken(ctx, f.sign(base(), "RS256"), "n1")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, c.Email, "a@example.com")
	if _, err := p.verifyIDToken(ctx, f.sign(base(), "ES256"), "n1"); err != nil {
		t.Fatalf("ES256: %v", err)
	}
	aud := base()
	aud["aud"] = []string{fakeClientID, "other"}
	aud["azp"] = fakeClientID
	if _, err := p.verifyIDToken(ctx, f.sign(aud, "RS256"), "n1"); err != nil {
		t.Fatalf("two audiences with azp: %v", err)
	}
	for name, change := range map[string]func(map[string]any){
		"two audiences, no azp": func(c map[string]any) { c["aud"] = []string{fakeClientID, "other"} },
		"no subject":            func(c map[string]any) { delete(c, "sub") },
		"future":                func(c map[string]any) { c["iat"] = now + 3600 },
		"no expiry":             func(c map[string]any) { delete(c, "exp") },
	} {
		cl := base()
		change(cl)
		if _, err := p.verifyIDToken(ctx, f.sign(cl, "RS256"), "n1"); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
	if _, err := p.verifyIDToken(ctx, f.sign(base(), "RS256"), "n2"); err == nil {
		t.Error("another sign-in's nonce taken")
	}
	// a token signed by someone else's key under the provider's kid
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	if _, err := p.verifyIDToken(ctx, signJWT(base(), "RS256", "r1", other, nil), "n1"); err == nil {
		t.Error("forged signature taken")
	}
	// "none" and HS256 are never taken
	tok := f.sign(base(), "RS256")
	parts := strings.Split(tok, ".")
	none := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"r1"}`)) + "." + parts[1] + "."
	if _, err := p.verifyIDToken(ctx, none, "n1"); err == nil {
		t.Error("alg none taken")
	}
	// the EC key's kid with an RSA algorithm
	if _, err := p.verifyIDToken(ctx, signJWT(base(), "RS256", "e1", f.rsaKey, nil), "n1"); err == nil {
		t.Error("RS256 under an EC key taken")
	}

	// a new key: read again for its kid, but not more often than
	// oidcKeysMinAge however many unknown kids come
	clock := time.Now()
	p.now = func() time.Time { return clock }
	rotated, _ := rsa.GenerateKey(rand.Reader, 2048)
	f.mu.Lock()
	f.extraKey = rotated
	reads := f.jwksReads
	f.mu.Unlock()
	clock = clock.Add(oidcKeysMinAge)
	if _, err := p.verifyIDToken(ctx, signJWT(base(), "RS256", "r2", rotated, nil), "n1"); err != nil {
		t.Fatalf("rotated key: %v", err)
	}
	for range 5 {
		p.verifyIDToken(ctx, signJWT(base(), "RS256", "made-up", rotated, nil), "n1")
	}
	f.mu.Lock()
	eq(t, f.jwksReads, reads+1, "the keys read once for the new kid, not for each made-up one")
	f.mu.Unlock()
}

func TestOIDCAllowList(t *testing.T) {
	yes, no := true, false
	for _, c := range []struct {
		allow []string
		claim oidcClaims
		want  bool
	}{
		{[]string{"ada@example.com"}, oidcClaims{Email: "ada@example.com"}, true},
		{[]string{"ada@example.com"}, oidcClaims{Email: "bob@example.com"}, false},
		{[]string{"@example.com"}, oidcClaims{Email: "bob@example.com", EmailVerified: &yes}, true},
		{[]string{"@example.com"}, oidcClaims{Email: "bob@example.com.evil"}, false},
		{[]string{"@example.com"}, oidcClaims{Email: "bob@sub.example.com"}, false},
		{[]string{"@example.com"}, oidcClaims{Email: "bob@example.com", EmailVerified: &no}, false},
		{[]string{"@example.com"}, oidcClaims{}, false},
		{[]string{"*"}, oidcClaims{}, true},
	} {
		if got := oidcAllowed(c.allow, &c.claim); got != c.want {
			t.Errorf("%v %+v: %v", c.allow, c.claim, got)
		}
	}
	for _, bad := range []oidcConfig{
		{Issuer: "https://idp.example", Allow: []string{"*"}},
		{Issuer: "https://idp.example", ClientID: "x"},
		{Issuer: "http://idp.example", ClientID: "x", Allow: []string{"*"}},
		{Issuer: "https://idp.example", ClientID: "x", Allow: []string{"example.com"}},
	} {
		if bad.check() == nil {
			t.Errorf("%+v taken", bad)
		}
	}
}
