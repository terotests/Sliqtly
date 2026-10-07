# Sliqtly REST API v1 (contract for the desktop and web editors)

Served by a server of one's own (`sliqtly-server -data <folder>`,
`mcp-go/apiv1.go`); sliqtly.com does not serve it.

Base: `<server>/api/v1`, JSON (UTF-8). The same port answers http:// and
https:// (the server's own CA, or a certificate given with
`-tls-cert`/`-tls-key`).

## Auth

`Authorization: Bearer <token>`, where the token is either

- the server's static token (`-token` / `SLIQTLY_TOKEN`; it covers `/api/v1`
  as well as `/mcp`), compared in constant time, or
- an OAuth access token the server issued (its own OAuth 2.1 authorization
  server, sign-in delegated to a third-party OIDC provider when `-oidc-*` is
  set; see below).

Both work on `/mcp` too.

A request without a token that holds gets 401 with
`WWW-Authenticate: Bearer realm="sliqtly"`, plus `error="invalid_token"` when
a token was sent, plus, when OAuth is on,
`resource_metadata="<server>/.well-known/oauth-protected-resource"`
(`.../oauth-protected-resource/mcp` on `/mcp`).

With neither a token nor OIDC set, the server has no sign-in: `/api/v1`
answers the server's own pages, pages on this computer (loopback origins)
and programs that send no `Origin`; `/api/v1/info` is also readable by the
origins on `-cors-origins`.

Errors: `{"error": "<human text>", "code": "<machine code>"}`.
Codes: `unauthorized` (401), `forbidden` (403), `not_found` (404),
`method_not_allowed` (405), `conflict` (409), `bad_request` (400, and 415
for a write that is not `Content-Type: application/json`), `too_large`
(413), `internal` (500).

## Endpoints

```
GET /api/v1/info                      no auth
  { "name": "Sliqtly", "version": "<server version>", "api": 1,
    "user": "<the server's user>",
    "auth": { "required": bool, "token": bool, "oauth": bool,
              "issuer": "<server base url>",          // when oauth
              "provider": "<OIDC issuer host>" },       // when oauth
    "tls":  { "enabled": bool,                          // https:// served on this port
              "ownCA": bool,                            // by the server's own CA
              "fingerprint": "<sha256 of the CA certificate (DER), lower-case hex>",  // when ownCA
              "ca": "/ca.crt" } }                       // when ownCA

GET /api/v1/me                         auth
  { "id": "...", "name": "...", "email": "..."? }
  With the static token (or no sign-in) id and name are the server's user;
  signed in through OIDC, id is the provider's subject.

GET /api/v1/decks                      auth
  { "decks": [ { "id", "name", "updated": <ms>, "slides": <int>, "room": "<room id>"? } ] }
  newest first; slides as written in the Markdown (the player may split a
  slide that runs over)

POST /api/v1/decks { "name", "markdown", "theme"? }      auth → 201 Deck
  theme defaults to "aurora"

GET /api/v1/decks/{id}                 auth → Deck, header ETag: "<version>"
  Deck = { "id", "name", "markdown", "theme", "updated": <ms>,
           "version": "<opaque string>", "room": "<room id>"? }

PUT /api/v1/decks/{id} { "name"?, "markdown"?, "theme"?, "ifVersion"? }   auth
  → 200 Deck (new version), header ETag
  → 409 { "error", "code": "conflict", "current": Deck }  when ifVersion is stale
  `If-Match: "<version>"` works in place of ifVersion. Without either the
  save is unconditional. Collab rooms open on the deck get the change like
  any other save.

DELETE /api/v1/decks/{id}              auth → 204

GET /api/v1/decks/{id}/view[?slides=]  auth → the same JSON as /api/view/<id>
  (slides laid out by the server as EVG display lists, plus picture files)
```

## Cross-origin (browser clients)

Bearer-token requests carry no ambient credentials, so CORS is answered for
the origins on the allowlist: `-cors-origins` / `SLIQTLY_CORS_ORIGINS`
(comma separated, `scheme://host[:port]`), plus http(s) on `localhost`,
`127.0.0.1` and `[::1]`, any port, by default. That covers `/api/v1`,
`/oauth/token` and the OAuth metadata documents. Preflights are answered
(`Access-Control-Allow-Headers: Authorization, Content-Type, If-Match`);
`ETag` and `WWW-Authenticate` are exposed. A page of an origin not on the
list gets 403. The old `/api` (the built-in page's) keeps its own rule.

## OAuth (when -oidc-* is set)

- Metadata: `/.well-known/oauth-authorization-server` (RFC 8414),
  `/.well-known/oauth-protected-resource[/mcp]` (RFC 9728).
- Clients are public, PKCE S256 only:
  - `sliqtly-desktop`: redirect `http://127.0.0.1:<any port>/callback` or
    `http://localhost:<any port>/callback`;
  - `sliqtly-web`: redirect `<origin>/callback.html` for an origin on
    `-cors-origins`, the server's own origin, or `http://localhost` /
    `http://127.0.0.1` on any port;
  - any other client registers at `POST /oauth/register` (RFC 7591) and is
    shown to the person on a consent page before the provider's sign-in.
- Flow: `GET /oauth/authorize` (client_id, redirect_uri, response_type=code,
  code_challenge, code_challenge_method=S256, state) → the provider's sign-in
  (server-side PKCE, state and nonce) → `/oauth/callback` → ID token verified
  (signature by the provider's JWKS, RS256 or ES256; iss, aud, azp, exp,
  iat, nonce) → account allowed by `-oidc-allow` (emails, `@domain`, or `*`)
  → `<redirect_uri>?code=…&state=…&iss=<server>`. A sign-in that is
  refused comes back as `?error=access_denied&error_description=…&state=…`.
- `POST /oauth/token` (form-encoded or JSON):
  `grant_type=authorization_code` with code, redirect_uri, code_verifier
  (client_id optional), or `grant_type=refresh_token` with refresh_token.
  → `{ access_token, token_type: "Bearer", expires_in: 3600, refresh_token,
  scope }`. Codes live 10 minutes and are spent once; refresh tokens live 60
  days and rotate: each is spent once, by the refresh that replaces it.
- The server keeps only the SHA-256 of codes and tokens.
- Issued tokens work for `/mcp` and `/api/v1`.
