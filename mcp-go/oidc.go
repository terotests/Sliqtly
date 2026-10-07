// SPDX-License-Identifier: AGPL-3.0-or-later

// Sign-in on a server of one's own through a third-party OpenID Connect
// provider (Google, Microsoft Entra ID, Keycloak, Okta, ...), set with
// -oidc-issuer, -oidc-client-id and -oidc-client-secret.
//
// The server stays the OAuth 2.1 authorization server for its own clients
// (rgr/OAuth.rgr: registration, /oauth/authorize, codes, /oauth/token,
// refresh token rotation); the provider only says who the person is.
// The cloud asks Firebase on its /oauth.html page; here /oauth.html is
// this file's step instead:
//
//	/oauth/authorize   (OAuth.rgr) checks the client and its PKCE, keeps
//	                   the request, sends the browser to /oauth.html
//	/oauth.html        a client the server knows goes on at once; any other
//	                   is shown first, and goes on with a POST /oauth/start
//	                   from this page (localguard.go refuses one from
//	                   elsewhere), so no page can sign someone in to a
//	                   client they did not see
//	→ the provider     with the server's own PKCE (S256), state and nonce,
//	                   kept under mcp_oauth_oidc/<sha256(state)> for 15 min
//	/oauth/callback    the state taken (once), the code exchanged at the
//	                   provider's token endpoint, the ID token verified
//	                   (signature by the provider's JWKS, RS256 or ES256;
//	                   iss, aud, azp, exp, iat, nonce), the account checked
//	                   against -oidc-allow, then OAuth.approve gives the
//	                   client its code at its redirect_uri
//
// What approve is handed is not the provider's token but a one-use ticket
// for the account verified here (signInTickets), so POST /oauth/approve,
// which this server does not route, could not be used to skip the checks.

package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// what the flags set
type oidcConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	// emails and @domains let in; "*" lets in every account the provider
	// signs in
	Allow  []string
	Scopes string
}

func (c oidcConfig) on() bool { return c.Issuer != "" }

func (c oidcConfig) check() error {
	if !c.on() {
		return nil
	}
	u, err := url.Parse(c.Issuer)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && loopbackOrigin(c.Issuer))) {
		return fmt.Errorf("-oidc-issuer %q: an https:// address (http:// only on this computer)", c.Issuer)
	}
	if c.ClientID == "" {
		return errors.New("-oidc-client-id is needed with -oidc-issuer")
	}
	if len(c.Allow) == 0 {
		// a provider such as Google signs in anyone with an account: who may
		// use the server is said here, never assumed
		return errors.New(`-oidc-allow is needed with -oidc-issuer: the emails and @domains let in, or "*" for every account the provider signs in`)
	}
	for _, a := range c.Allow {
		if a != "*" && !strings.Contains(a, "@") {
			return fmt.Errorf("-oidc-allow %q: an email, an @domain or *", a)
		}
	}
	return nil
}

// --- the provider: discovery, keys, token endpoint, ID token checks

type oidcDiscovery struct {
	Issuer           string   `json:"issuer"`
	AuthEndpoint     string   `json:"authorization_endpoint"`
	TokenEndpoint    string   `json:"token_endpoint"`
	JWKSURI          string   `json:"jwks_uri"`
	TokenAuthMethods []string `json:"token_endpoint_auth_methods_supported"`
}

type oidcProvider struct {
	cfg    oidcConfig
	client *http.Client
	now    func() time.Time

	mu      sync.Mutex
	disc    *oidcDiscovery
	discAt  time.Time
	keys    map[string]crypto.PublicKey // by kid; "" for a key without one
	fetched time.Time                   // when the keys were last read
}

const (
	oidcDiscoveryTTL = time.Hour
	// an unknown kid reads the keys again (the provider rotated them), but
	// not more often than this, so tokens with made-up kids cannot make the
	// server hammer the provider
	oidcKeysMinAge = 10 * time.Second
	oidcClockSkew  = time.Minute
)

func newOIDCProvider(cfg oidcConfig) *oidcProvider {
	return &oidcProvider{cfg: cfg, client: &http.Client{Timeout: 15 * time.Second}, now: time.Now}
}

func (p *oidcProvider) getJSON(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	res, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("%s: %s", u, res.Status)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(v)
}

// the provider's /.well-known/openid-configuration, read once an hour
func (p *oidcProvider) discovery(ctx context.Context) (*oidcDiscovery, error) {
	p.mu.Lock()
	if p.disc != nil && p.now().Sub(p.discAt) < oidcDiscoveryTTL {
		d := p.disc
		p.mu.Unlock()
		return d, nil
	}
	p.mu.Unlock()
	var d oidcDiscovery
	u := strings.TrimRight(p.cfg.Issuer, "/") + "/.well-known/openid-configuration"
	if err := p.getJSON(ctx, u, &d); err != nil {
		return nil, fmt.Errorf("OIDC discovery: %w", err)
	}
	// the document must be the issuer's own (OpenID Connect Discovery §4.3)
	if strings.TrimRight(d.Issuer, "/") != strings.TrimRight(p.cfg.Issuer, "/") {
		return nil, fmt.Errorf("OIDC discovery: issuer %q is not %q", d.Issuer, p.cfg.Issuer)
	}
	if d.AuthEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "" {
		return nil, errors.New("OIDC discovery: authorization_endpoint, token_endpoint or jwks_uri missing")
	}
	p.mu.Lock()
	p.disc, p.discAt = &d, p.now()
	p.mu.Unlock()
	return &d, nil
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (k jwk) public() (crypto.PublicKey, error) {
	b := base64.RawURLEncoding
	switch k.Kty {
	case "RSA":
		n, err1 := b.DecodeString(k.N)
		e, err2 := b.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(n) < 256 || len(e) == 0 || len(e) > 4 {
			return nil, errors.New("bad RSA key")
		}
		ei := 0
		for _, c := range e {
			ei = ei<<8 | int(c)
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: ei}, nil
	case "EC":
		if k.Crv != "P-256" {
			return nil, errors.New("EC key not on P-256")
		}
		x, err1 := b.DecodeString(k.X)
		y, err2 := b.DecodeString(k.Y)
		if err1 != nil || err2 != nil || len(x) != 32 || len(y) != 32 {
			return nil, errors.New("bad EC key")
		}
		pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
			return nil, errors.New("EC point not on the curve")
		}
		return pub, nil
	}
	return nil, fmt.Errorf("key type %q", k.Kty)
}

// the provider's signing key kid; the keys are read again when kid is not
// among them
func (p *oidcProvider) key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	p.mu.Lock()
	k, ok := p.pick(kid)
	stale := p.now().Sub(p.fetched) >= oidcKeysMinAge
	p.mu.Unlock()
	if ok {
		return k, nil
	}
	if !stale {
		return nil, fmt.Errorf("no signing key %q", kid)
	}
	d, err := p.discovery(ctx)
	if err != nil {
		return nil, err
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := p.getJSON(ctx, d.JWKSURI, &set); err != nil {
		return nil, fmt.Errorf("OIDC keys: %w", err)
	}
	keys := map[string]crypto.PublicKey{}
	for _, j := range set.Keys {
		if j.Use != "" && j.Use != "sig" {
			continue
		}
		if pub, err := j.public(); err == nil {
			keys[j.Kid] = pub
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys, p.fetched = keys, p.now()
	if k, ok := p.pick(kid); ok {
		return k, nil
	}
	return nil, fmt.Errorf("no signing key %q", kid)
}

// a token without a kid is taken only when the provider has one key
func (p *oidcProvider) pick(kid string) (crypto.PublicKey, bool) {
	if kid != "" {
		k, ok := p.keys[kid]
		return k, ok
	}
	if len(p.keys) == 1 {
		for _, k := range p.keys {
			return k, true
		}
	}
	return nil, false
}

// who the provider says signed in
type oidcClaims struct {
	Sub           string
	Email         string
	EmailVerified *bool
	Name          string
}

// verifyIDToken checks an ID token as OpenID Connect Core §3.1.3.7 asks:
// the signature by the provider's key (RS256 or ES256 only, never "none"
// or a shared-secret algorithm), iss the provider's, aud this client (and
// azp when there are several audiences), not expired, not issued in the
// future, and the nonce this sign-in sent
func (p *oidcProvider) verifyIDToken(ctx context.Context, raw, nonce string) (*oidcClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("not a JWT")
	}
	b := base64.RawURLEncoding
	hb, err := b.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("bad JWT header")
	}
	var head struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(hb, &head); err != nil {
		return nil, errors.New("bad JWT header")
	}
	sig, err := b.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("bad JWT signature")
	}
	k, err := p.key(ctx, head.Kid)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	switch head.Alg {
	case "RS256":
		pub, ok := k.(*rsa.PublicKey)
		if !ok || rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig) != nil {
			return nil, errors.New("signature does not verify")
		}
	case "ES256":
		pub, ok := k.(*ecdsa.PublicKey)
		if !ok || len(sig) != 64 || !ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			return nil, errors.New("signature does not verify")
		}
	default:
		return nil, fmt.Errorf("algorithm %q is not accepted", head.Alg)
	}
	cb, err := b.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("bad JWT claims")
	}
	var c struct {
		Iss           string          `json:"iss"`
		Sub           string          `json:"sub"`
		Aud           json.RawMessage `json:"aud"`
		Azp           string          `json:"azp"`
		Exp           json.Number     `json:"exp"`
		Iat           json.Number     `json:"iat"`
		Nonce         string          `json:"nonce"`
		Email         string          `json:"email"`
		EmailVerified any             `json:"email_verified"`
		Name          string          `json:"name"`
	}
	dec := json.NewDecoder(strings.NewReader(string(cb)))
	dec.UseNumber()
	if err := dec.Decode(&c); err != nil {
		return nil, errors.New("bad JWT claims")
	}
	d, err := p.discovery(ctx)
	if err != nil {
		return nil, err
	}
	if c.Iss != d.Issuer {
		return nil, fmt.Errorf("issuer %q is not %q", c.Iss, d.Issuer)
	}
	var auds []string
	var one string
	if json.Unmarshal(c.Aud, &one) == nil {
		auds = []string{one}
	} else if json.Unmarshal(c.Aud, &auds) != nil {
		return nil, errors.New("bad audience")
	}
	mine := false
	for _, a := range auds {
		if a == p.cfg.ClientID {
			mine = true
		}
	}
	if !mine {
		return nil, fmt.Errorf("audience %v is not this server (%s)", auds, p.cfg.ClientID)
	}
	if (len(auds) > 1 || c.Azp != "") && c.Azp != p.cfg.ClientID {
		return nil, fmt.Errorf("authorized party %q is not this server", c.Azp)
	}
	now := p.now()
	exp, err := c.Exp.Float64()
	if err != nil || exp <= 0 {
		return nil, errors.New("no expiry")
	}
	if now.After(time.Unix(int64(exp), 0).Add(oidcClockSkew)) {
		return nil, errors.New("expired")
	}
	if iat, err := c.Iat.Float64(); err == nil && time.Unix(int64(iat), 0).After(now.Add(5*time.Minute)) {
		return nil, errors.New("issued in the future")
	}
	if nonce == "" || subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(nonce)) != 1 {
		return nil, errors.New("nonce does not match this sign-in")
	}
	if c.Sub == "" {
		return nil, errors.New("no subject")
	}
	out := &oidcClaims{Sub: c.Sub, Email: strings.ToLower(strings.TrimSpace(c.Email)), Name: c.Name}
	switch v := c.EmailVerified.(type) {
	case bool:
		out.EmailVerified = &v
	case string: // some providers send it as text
		b := v == "true"
		out.EmailVerified = &b
	}
	return out, nil
}

// the provider's sign-in page, for this sign-in
func (p *oidcProvider) authURL(ctx context.Context, redirect, state, nonce, challenge string) (string, error) {
	d, err := p.discovery(ctx)
	if err != nil {
		return "", err
	}
	scopes := p.cfg.Scopes
	if scopes == "" {
		scopes = "openid email profile"
	}
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {p.cfg.ClientID},
		"redirect_uri":          {redirect},
		"scope":                 {scopes},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(d.AuthEndpoint, "?") {
		sep = "&"
	}
	return d.AuthEndpoint + sep + q.Encode(), nil
}

// the provider's code → its ID token, at its token endpoint, with the
// server's PKCE verifier and, for a confidential client, its secret
// (client_secret_basic unless the provider lists only client_secret_post)
func (p *oidcProvider) exchange(ctx context.Context, code, verifier, redirect string) (string, error) {
	d, err := p.discovery(ctx)
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirect},
		"code_verifier": {verifier},
	}
	basic := false
	if p.cfg.ClientSecret != "" {
		basic = len(d.TokenAuthMethods) == 0
		for _, m := range d.TokenAuthMethods {
			if m == "client_secret_basic" {
				basic = true
			}
		}
		if !basic {
			form.Set("client_secret", p.cfg.ClientSecret)
		}
	}
	if !basic {
		form.Set("client_id", p.cfg.ClientID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if basic {
		// RFC 6749 §2.3.1: both form-encoded before the Basic encoding
		req.SetBasicAuth(url.QueryEscape(p.cfg.ClientID), url.QueryEscape(p.cfg.ClientSecret))
	}
	res, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var out struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("token endpoint: %s", res.Status)
	}
	if res.StatusCode != 200 || out.IDToken == "" {
		return "", fmt.Errorf("token endpoint: %s %s %s", res.Status, out.Error, out.Desc)
	}
	return out.IDToken, nil
}

// the provider as the pages name it: its host
func (p *oidcProvider) displayName() string {
	if u, err := url.Parse(p.cfg.Issuer); err == nil && u.Host != "" {
		return u.Host
	}
	return p.cfg.Issuer
}

// whether -oidc-allow lets the account in. An email the provider says is
// not verified is never matched: anyone can type any address into an
// account of their own on some providers.
func oidcAllowed(allow []string, c *oidcClaims) bool {
	for _, a := range allow {
		if a == "*" {
			return true
		}
	}
	if c.Email == "" || (c.EmailVerified != nil && !*c.EmailVerified) {
		return false
	}
	at := strings.LastIndex(c.Email, "@")
	if at < 0 {
		return false
	}
	domain := c.Email[at:]
	for _, a := range allow {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == c.Email || (strings.HasPrefix(a, "@") && a == domain) {
			return true
		}
	}
	return false
}

// --- the tickets handed to OAuth.approve

// One-use stand-ins for an account the callback verified, read back by
// env.VerifyIDToken when OAuth.approve asks who signed in. They live a
// minute and in memory: made and used within one request.
type signInTickets struct {
	mu  sync.Mutex
	m   map[string]ticket
	now func() time.Time
}

type ticket struct {
	who *IDToken
	exp time.Time
}

func newSignInTickets() *signInTickets {
	return &signInTickets{m: map[string]ticket{}, now: time.Now}
}

func (t *signInTickets) issue(who *IDToken) string {
	id := randomToken(32)
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for k, v := range t.m {
		if now.After(v.exp) {
			delete(t.m, k)
		}
	}
	t.m[id] = ticket{who, now.Add(time.Minute)}
	return id
}

func (t *signInTickets) take(_ context.Context, id string) (*IDToken, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	k, ok := t.m[id]
	delete(t.m, id)
	if !ok || t.now().After(k.exp) {
		return nil, errors.New("no such sign-in")
	}
	return k.who, nil
}

// n random bytes, base64url
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func sha256Hex(s string) string {
	x := sha256.Sum256([]byte(s))
	return hex.EncodeToString(x[:])
}

// --- the sign-in on this server

type oidcSignIn struct {
	p       *oidcProvider
	tickets *signInTickets
}

const (
	oidcStateCol = "mcp_oauth_oidc"
	oidcStateTTL = 15 * time.Minute
)

// useOIDC turns sign-in on: OAuth.rgr's endpoints answer, and its
// approve learns who signed in from the tickets
func (s *localServer) useOIDC(cfg oidcConfig) error {
	if err := cfg.check(); err != nil {
		return err
	}
	if !cfg.on() {
		return nil
	}
	if s.env.DB == nil {
		return errors.New("sign-in keeps its codes and tokens in the data folder: start with -data")
	}
	o := &oidcSignIn{p: newOIDCProvider(cfg), tickets: newSignInTickets()}
	s.oidc = o
	s.env.OAuth = true
	s.env.VerifyIDToken = o.tickets.take
	return nil
}

// where the provider sends the browser back: one fixed address, as it has
// to be registered there (-url sets it)
func (s *localServer) oidcRedirect() string {
	return s.env.BaseURL + "/oauth/callback"
}

// the request /oauth/authorize kept (OAuth.rgr), while it holds
func (s *localServer) signInRequest(ctx context.Context, rid string) (Doc, bool) {
	if len(rid) != 24 || !shareID.MatchString(rid) {
		return nil, false
	}
	d, err := s.env.DB.Get(ctx, OAuth_static_col("requests"), rid)
	if err != nil || d == nil {
		return nil, false
	}
	if exp := millis(d["exp"]); exp < time.Now().UnixMilli() {
		return nil, false
	}
	return d, true
}

// GET /oauth.html?request=… (from /oauth/authorize)
func (s *localServer) signInPage(w http.ResponseWriter, r *http.Request) {
	rid := r.URL.Query().Get("request")
	req, ok := s.signInRequest(r.Context(), rid)
	if !ok {
		signInError(w, 400, "This sign-in has expired. Start again from your app.")
		return
	}
	cid, _ := req["client_id"].(string)
	if s.builtinClient(cid) != nil {
		s.toProvider(w, r, rid)
		return
	}
	name, _ := req["client_name"].(string)
	to, _ := req["redirect_uri"].(string)
	where := to
	if u, err := url.Parse(to); err == nil {
		where = u.Scheme + "://" + u.Host
		if loopbackOrigin(where) {
			where = "an app on this computer"
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// the page must not be shown inside another site's frame, where a
	// click could be stolen
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
	consentTmpl.Execute(w, map[string]any{"Client": name, "Where": where, "Request": rid, "Provider": s.oidc.p.displayName()})
}

// POST /oauth/start (the page above): request=…, or deny=1
func (s *localServer) signInStart(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		signInError(w, 400, "Bad request.")
		return
	}
	rid := r.PostForm.Get("request")
	if _, ok := s.signInRequest(r.Context(), rid); !ok {
		signInError(w, 400, "This sign-in has expired. Start again from your app.")
		return
	}
	if r.PostForm.Get("deny") != "" {
		s.signInDeny(w, r, rid, "")
		return
	}
	s.toProvider(w, r, rid)
}

// the browser to the provider, with this sign-in's state, nonce and PKCE
func (s *localServer) toProvider(w http.ResponseWriter, r *http.Request, rid string) {
	state, nonce, verifier := randomToken(32), randomToken(32), randomToken(32)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	u, err := s.oidc.p.authURL(r.Context(), s.oidcRedirect(), state, nonce, challenge)
	if err != nil {
		log.Printf("sign-in: %v", err)
		signInError(w, 502, "The sign-in provider cannot be reached. Try again in a moment.")
		return
	}
	exp := time.Now().Add(oidcStateTTL)
	doc := Doc{"request": rid, "nonce": nonce, "verifier": verifier, "exp": exp.UnixMilli(), "expires": exp.UTC()}
	if err := s.env.DB.Set(r.Context(), oidcStateCol, sha256Hex(state), doc); err != nil {
		log.Printf("sign-in: %v", err)
		signInError(w, 500, "The sign-in could not be started.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u, http.StatusFound)
}

// GET /oauth/callback?code=…&state=… from the provider
func (s *localServer) signInCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	if state == "" {
		signInError(w, 400, "This sign-in has expired. Start again from your app.")
		return
	}
	// taken, not read: a state is good for one answer
	st, err := s.env.DB.Take(r.Context(), oidcStateCol, sha256Hex(state))
	if err != nil {
		log.Printf("sign-in: %v", err)
		signInError(w, 500, "The sign-in could not be finished.")
		return
	}
	if st == nil || millis(st["exp"]) < time.Now().UnixMilli() {
		signInError(w, 400, "This sign-in has expired. Start again from your app.")
		return
	}
	rid, _ := st["request"].(string)
	if e := q.Get("error"); e != "" {
		s.signInDeny(w, r, rid, "The sign-in at "+s.oidc.p.displayName()+" did not complete ("+e+").")
		return
	}
	nonce, _ := st["nonce"].(string)
	verifier, _ := st["verifier"].(string)
	raw, err := s.oidc.p.exchange(r.Context(), q.Get("code"), verifier, s.oidcRedirect())
	if err != nil {
		log.Printf("sign-in: %v", err)
		s.signInDeny(w, r, rid, "The sign-in could not be verified.")
		return
	}
	c, err := s.oidc.p.verifyIDToken(r.Context(), raw, nonce)
	if err != nil {
		log.Printf("sign-in: ID token refused: %v", err)
		s.signInDeny(w, r, rid, "The sign-in could not be verified.")
		return
	}
	if !oidcAllowed(s.oidc.p.cfg.Allow, c) {
		who := c.Email
		if who == "" {
			who = "This account"
		}
		log.Printf("sign-in: %s is not on -oidc-allow", who)
		s.signInDeny(w, r, rid, who+" may not use this server.")
		return
	}
	name := c.Name
	if name == "" {
		name = c.Email
	}
	t := s.oidc.tickets.issue(&IDToken{UID: c.Sub, Name: name, Email: c.Email})
	s.approve(w, r, map[string]any{"request": rid, "id_token": t})
}

// the client told no, with why
func (s *localServer) signInDeny(w http.ResponseWriter, r *http.Request, rid, reason string) {
	s.approve(w, r, map[string]any{"request": rid, "deny": true, "reason": reason})
}

// OAuth.approve, in-process; its answer names where the browser goes
func (s *localServer) approve(w http.ResponseWriter, r *http.Request, body map[string]any) {
	out := OAuth_static_approve(s.host(r), J_static_parse(toJSON(body)))
	to := ""
	if out.json != nil {
		to = out.json.str_("redirect")
	}
	if out.status != 200 || to == "" {
		msg := "This sign-in has expired. Start again from your app."
		if out.json != nil && out.json.str_("error_description") != "" {
			msg = out.json.str_("error_description")
		}
		signInError(w, int(out.status), msg)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, to, http.StatusFound)
}

// a host for calling the Ranger side from here
func (s *localServer) host(r *http.Request) *McpHost {
	return &McpHost{env: s.env, r: r, ctx: r.Context(), images: map[int64][]byte{}}
}

func signInError(w http.ResponseWriter, status int, msg string) {
	if status < 400 {
		status = 400
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	signInErrTmpl.Execute(w, msg)
}

var signInStyle = `<style>
:root { color-scheme: light dark; --bg: #f6f6f4; --fg: #1d1d1b; --muted: #6b6b66; }
@media (prefers-color-scheme: dark) { :root { --bg: #141414; --fg: #ececea; --muted: #9a9a94; } }
body { margin: 0; background: var(--bg); color: var(--fg); font: 16px/1.5 system-ui, sans-serif; }
main { max-width: 520px; margin: 0 auto; padding: 48px 16px; }
h1 { font-size: 1.4rem; margin: 0 0 12px; } .muted { color: var(--muted); font-size: .9rem; }
button { font: inherit; padding: 8px 18px; border-radius: 6px; border: 1px solid var(--fg); cursor: pointer; margin-right: 8px; }
button.go { background: var(--fg); color: var(--bg); } button.no { background: transparent; color: var(--fg); }
</style>`

var consentTmpl = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in to Sliqtly</title>` + signInStyle + `</head><body><main>
<h1>Sign in to Sliqtly</h1>
<p><strong>{{.Client}}</strong> asks to use the presentations on this server as you.</p>
<p class="muted">Its sign-in goes to {{.Where}}. Continue only if you started this from that app.</p>
<form method="post" action="/oauth/start">
<input type="hidden" name="request" value="{{.Request}}">
<button class="go" type="submit">Continue with {{.Provider}}</button>
<button class="no" type="submit" name="deny" value="1">Cancel</button>
</form>
</main></body></html>
`))

var signInErrTmpl = template.Must(template.New("signin-error").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign-in</title>` + signInStyle + `</head><body><main>
<h1>Sign-in</h1>
<p>{{.}}</p>
</main></body></html>
`))

// --- the clients known without registration

// sliqtly-desktop: the desktop app, which takes its code on a loopback
// port of its own (RFC 8252 §7.3: any port; OAuth.sameRedirect matches a
// loopback address whatever its port). sliqtly-web: the web editor, on
// this server or a page of a -cors-origins origin, at /callback.html.
func (s *localServer) builtinClient(id string) map[string]any {
	switch id {
	case "sliqtly-desktop":
		return map[string]any{
			"client_id": id, "client_name": "Sliqtly desktop app",
			"redirect_uris": []any{"http://127.0.0.1/callback", "http://localhost/callback"},
		}
	case "sliqtly-web":
		uris := []any{"http://127.0.0.1/callback.html", "http://localhost/callback.html"}
		if o, ok := normalOrigin(s.env.BaseURL); ok {
			uris = append(uris, o+"/callback.html")
		}
		if s.cors != nil {
			for _, o := range s.cors.list() {
				uris = append(uris, o+"/callback.html")
			}
		}
		return map[string]any{"client_id": id, "client_name": "Sliqtly web editor", "redirect_uris": uris}
	}
	return nil
}

func (h *McpHost) BuiltinClient(id string) string {
	if h.env.Clients == nil {
		return ""
	}
	if c := h.env.Clients(id); c != nil {
		return toJSON(c)
	}
	return ""
}
