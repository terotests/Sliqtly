// Optional sign-in: the MCP server is its own OAuth 2.1 authorization
// server, and the person signs in with the Google account they use for PRO
// (Firebase Auth) on /oauth.html. Without a token every tool still works;
// with one, presentations are the person's own (share.owner = their uid).
//
//   /.well-known/oauth-protected-resource[/mcp]   RFC 9728
//   /.well-known/oauth-authorization-server       RFC 8414
//   POST /oauth/register     dynamic client registration (RFC 7591)
//   GET  /oauth/authorize    checks the request, sends the browser to /oauth.html
//   POST /oauth/approve      /oauth.html hands back a Firebase ID token → code
//   POST /oauth/token        code + PKCE (S256) → tokens; refresh_token rotates
//
// Clients are public (no secret), identified by a registered id or by a URL
// to their metadata document (client ID metadata documents). Codes and tokens
// are random; Firestore keeps only their SHA-256, under mcp_oauth/…, which no
// client rule reaches.

import { hashKey, shortId } from "./store.js";

export const SCOPE = "decks";
const CODE_TTL = 10 * 60 * 1000;
const REQUEST_TTL = 15 * 60 * 1000;
const ACCESS_TTL = 60 * 60 * 1000;
const REFRESH_TTL = 60 * 24 * 60 * 60 * 1000;

function token(n = 32) {
  return shortId(n);
}

function s256(verifier) {
  return Buffer.from(hashKey(verifier), "hex").toString("base64url");
}

function loopback(u) {
  return u.protocol === "http:" && (u.hostname === "localhost" || u.hostname === "127.0.0.1" || u.hostname === "[::1]");
}

// https anywhere, http only on this machine, or an app's own scheme (cursor://)
export function redirectAllowed(uri) {
  let u;
  try { u = new URL(uri); } catch { return false; }
  if (u.hash) return false;
  if (u.protocol === "https:" || loopback(u)) return true;
  return /^[a-z][a-z0-9+.-]*:$/.test(u.protocol) && !["http:", "javascript:", "data:", "file:", "vbscript:", "blob:"].includes(u.protocol);
}

// A loopback redirect may come back on any port (RFC 8252 §7.3).
function sameRedirect(registered, given) {
  if (registered === given) return true;
  try {
    const a = new URL(registered);
    const b = new URL(given);
    return loopback(a) && loopback(b) && a.hostname === b.hostname && a.pathname === b.pathname && a.search === b.search;
  } catch { return false; }
}

function privateHost(h) {
  return /^(localhost|127\.|10\.|0\.|169\.254\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|\[)/.test(h) || h.endsWith(".internal") || h.endsWith(".local");
}

export function createOAuth({ db, verifyIdToken, fetchImpl = fetch, now = () => Date.now() }) {
  const col = (name) => db.collection(`mcp_oauth_${name}`);

  async function client(clientId) {
    if (/^https:\/\//.test(clientId)) {
      // a client ID metadata document: the id is the URL of its metadata
      const u = new URL(clientId);
      if (privateHost(u.hostname)) return null;
      const res = await fetchImpl(u, { signal: AbortSignal.timeout(8000), headers: { accept: "application/json" } });
      if (!res.ok) return null;
      const text = await res.text();
      if (text.length > 20000) return null;
      let meta;
      try { meta = JSON.parse(text); } catch { return null; }
      if (meta.client_id !== clientId || !Array.isArray(meta.redirect_uris)) return null;
      return { client_id: clientId, client_name: String(meta.client_name || u.hostname), redirect_uris: meta.redirect_uris.filter(redirectAllowed) };
    }
    if (!/^[A-Za-z0-9]{8,64}$/.test(clientId)) return null;
    const snap = await col("clients").doc(clientId).get();
    return snap.exists ? snap.data() : null;
  }

  function metadata(origin) {
    return {
      issuer: origin,
      authorization_endpoint: `${origin}/oauth/authorize`,
      token_endpoint: `${origin}/oauth/token`,
      registration_endpoint: `${origin}/oauth/register`,
      response_types_supported: ["code"],
      grant_types_supported: ["authorization_code", "refresh_token"],
      code_challenge_methods_supported: ["S256"],
      token_endpoint_auth_methods_supported: ["none"],
      scopes_supported: [SCOPE],
      client_id_metadata_document_supported: true,
      service_documentation: `${origin}/connect.html`,
    };
  }

  function resourceMetadata(origin) {
    return {
      resource: `${origin}/mcp`,
      authorization_servers: [origin],
      scopes_supported: [SCOPE],
      bearer_methods_supported: ["header"],
      resource_name: "Sliqtly",
      resource_documentation: `${origin}/connect.html`,
    };
  }

  async function register(body) {
    const uris = Array.isArray(body?.redirect_uris) ? body.redirect_uris.map(String) : [];
    if (!uris.length || uris.length > 10 || !uris.every(redirectAllowed)) {
      return { status: 400, json: { error: "invalid_redirect_uri", error_description: "redirect_uris: https, a loopback http address, or an app scheme" } };
    }
    const doc = {
      client_id: token(24),
      client_name: String(body.client_name || "MCP client").slice(0, 100),
      redirect_uris: uris,
      created: now(),
    };
    await col("clients").doc(doc.client_id).set(doc);
    return {
      status: 201,
      json: {
        client_id: doc.client_id, client_name: doc.client_name, redirect_uris: uris,
        client_id_issued_at: Math.floor(doc.created / 1000),
        grant_types: ["authorization_code", "refresh_token"], response_types: ["code"],
        token_endpoint_auth_method: "none", scope: SCOPE,
      },
    };
  }

  // → { redirect } to the sign-in page, or { status, text } when the request
  // cannot be sent back to the client at all
  async function authorize(q, origin) {
    const c = q.client_id ? await client(String(q.client_id)) : null;
    if (!c) return { status: 400, text: "Unknown client_id." };
    const redirect = String(q.redirect_uri || (c.redirect_uris.length === 1 ? c.redirect_uris[0] : ""));
    if (!redirect || !c.redirect_uris.some((r) => sameRedirect(r, redirect))) return { status: 400, text: "redirect_uri is not registered for this client." };
    const back = (error, description) => {
      const u = new URL(redirect);
      u.searchParams.set("error", error);
      if (description) u.searchParams.set("error_description", description);
      if (q.state) u.searchParams.set("state", String(q.state));
      u.searchParams.set("iss", origin);
      return { redirect: u.toString() };
    };
    if (q.response_type !== "code") return back("unsupported_response_type");
    if (!q.code_challenge || q.code_challenge_method !== "S256") return back("invalid_request", "PKCE with S256 is required");
    const rid = token(24);
    await col("requests").doc(rid).set({
      client_id: c.client_id, client_name: c.client_name, redirect_uri: redirect,
      state: q.state ? String(q.state) : null, code_challenge: String(q.code_challenge),
      resource: q.resource ? String(q.resource) : null, origin, exp: now() + REQUEST_TTL,
    });
    return { redirect: `${origin}/oauth.html?request=${rid}&client=${encodeURIComponent(c.client_name)}` };
  }

  // /oauth.html: { request, id_token } or { request, deny: true } → { redirect }
  async function approve(body) {
    const rid = String(body?.request || "");
    if (!/^[A-Za-z0-9]{24}$/.test(rid)) return { status: 400, json: { error: "invalid_request" } };
    const ref = col("requests").doc(rid);
    const snap = await ref.get();
    if (!snap.exists) return { status: 400, json: { error: "invalid_request", error_description: "The sign-in request has expired. Start again from your assistant." } };
    const r = snap.data();
    if (r.exp < now()) {
      await ref.delete();
      return { status: 400, json: { error: "invalid_request", error_description: "The sign-in request has expired. Start again from your assistant." } };
    }
    const u = new URL(r.redirect_uri);
    if (r.state) u.searchParams.set("state", r.state);
    u.searchParams.set("iss", r.origin);
    if (body.deny) {
      await ref.delete();
      u.searchParams.set("error", "access_denied");
      return { status: 200, json: { redirect: u.toString() } };
    }
    // a failed Google check leaves the request for another try
    let who;
    try { who = await verifyIdToken(String(body.id_token || "")); } catch { return { status: 401, json: { error: "invalid_token", error_description: "Google sign-in could not be verified." } }; }
    await ref.delete();
    const code = token(32);
    await col("codes").doc(hashKey(code)).set({
      uid: who.uid, name: who.name || who.email || "", client_id: r.client_id, redirect_uri: r.redirect_uri,
      code_challenge: r.code_challenge, resource: r.resource, exp: now() + CODE_TTL,
    });
    u.searchParams.set("code", code);
    return { status: 200, json: { redirect: u.toString() } };
  }

  async function issue(grant) {
    const access = token(32);
    const refresh = token(40);
    await col("tokens").doc(hashKey(access)).set({ ...grant, kind: "access", exp: now() + ACCESS_TTL });
    await col("tokens").doc(hashKey(refresh)).set({ ...grant, kind: "refresh", exp: now() + REFRESH_TTL });
    return { access_token: access, token_type: "Bearer", expires_in: ACCESS_TTL / 1000, refresh_token: refresh, scope: SCOPE };
  }

  const bad = (error, error_description) => ({ status: 400, json: { error, ...(error_description ? { error_description } : {}) } });

  async function tokenEndpoint(body) {
    const b = body || {};
    if (b.grant_type === "authorization_code") {
      const ref = col("codes").doc(hashKey(String(b.code || "")));
      const snap = await ref.get();
      if (!snap.exists) return bad("invalid_grant");
      const c = snap.data();
      await ref.delete();
      if (c.exp < now()) return bad("invalid_grant", "code expired");
      if (b.client_id && b.client_id !== c.client_id) return bad("invalid_grant", "client_id does not match");
      if (!b.redirect_uri || !sameRedirect(c.redirect_uri, String(b.redirect_uri))) return bad("invalid_grant", "redirect_uri does not match");
      if (!b.code_verifier || s256(String(b.code_verifier)) !== c.code_challenge) return bad("invalid_grant", "PKCE verification failed");
      return { status: 200, json: await issue({ uid: c.uid, name: c.name, client_id: c.client_id, resource: c.resource }) };
    }
    if (b.grant_type === "refresh_token") {
      const ref = col("tokens").doc(hashKey(String(b.refresh_token || "")));
      const snap = await ref.get();
      if (!snap.exists || snap.data().kind !== "refresh") return bad("invalid_grant");
      const t = snap.data();
      await ref.delete();
      if (t.exp < now()) return bad("invalid_grant", "refresh token expired");
      if (b.client_id && b.client_id !== t.client_id) return bad("invalid_grant", "client_id does not match");
      return { status: 200, json: await issue({ uid: t.uid, name: t.name, client_id: t.client_id, resource: t.resource }) };
    }
    return bad("unsupported_grant_type");
  }

  // Authorization: Bearer … → { uid, name } | null (no header) | false (bad token)
  async function who(header) {
    const m = /^Bearer\s+(\S+)$/i.exec(String(header || "").trim());
    if (!m) return null;
    const snap = await col("tokens").doc(hashKey(m[1])).get();
    if (!snap.exists) return false;
    const t = snap.data();
    if (t.kind !== "access" || t.exp < now()) return false;
    return { uid: t.uid, name: t.name };
  }

  return { metadata, resourceMetadata, register, authorize, approve, token: tokenEndpoint, who };
}
