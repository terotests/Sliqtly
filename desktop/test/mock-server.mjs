#!/usr/bin/env node
// SPDX-License-Identifier: MIT
//
// A stand-in Sliqtly server: the REST API v1 the editor speaks
// (/mnt/project-files/desktop-editor/API-v1.md), in one file, Node only.
// The app, its tests and the CI checks run against this, not the Go server.
//
//   node test/mock-server.mjs [--port 8089] [--auth token|oauth|none]
//                             [--token secret] [--tls] [--token-ttl SECONDS]
//
// What it has:
//   GET  /api/v1/info, /api/v1/me
//   GET/POST /api/v1/decks, GET/PUT/DELETE /api/v1/decks/{id}  (versions, 409)
//   GET  /api/v1/decks/{id}/view   the deck "Sliqtly Editor sample" answers
//        with a display list made by the real server (test/fixtures/
//        view-sample.json, GET /api/view/<id> of the Go server); any other
//        deck gets a simple layout made here from its Markdown (a heading and
//        its lines per slide), so an edit shows in the preview after a save
//   Bearer token (static), or OAuth 2.1: /.well-known/oauth-authorization-server,
//        /oauth/authorize (signs in at once: no provider), /oauth/token
//        (authorization_code with PKCE S256, refresh_token with rotation)
//   CORS for localhost / 127.0.0.1 origins (the web build)
//   --tls: https with a CA of its own made with openssl; /ca.crt, and
//        info.tls = { ownCA, fingerprint (SHA-256 of the CA's DER), ca }
//   test hooks (no auth): GET /__test/state, POST /__test/bump/{id} (an edit
//        made elsewhere: the next save with the old version gets 409),
//        POST /__test/expire (every access token stops working: 401 → refresh)
//
// Prints "mock: listening on <url>" when it is ready.

import crypto from "node:crypto";
import fs from "node:fs";
import http from "node:http";
import https from "node:https";
import os from "node:os";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const argv = process.argv.slice(2);
const opt = (name, fallback) => {
  const i = argv.indexOf("--" + name);
  return i >= 0 && i + 1 < argv.length ? argv[i + 1] : fallback;
};
const flag = (name) => argv.includes("--" + name);

const PORT = Number(opt("port", process.env.PORT || "8089"));
const AUTH = opt("auth", "token"); // token | oauth | none
const TOKEN = opt("token", "secret");
const TLS = flag("tls");
const TTL = Number(opt("token-ttl", "3600"));
const QUIET = flag("quiet");

// --- the decks -------------------------------------------------------------------
const decks = new Map();
let seq = 0;
const stats = { saves: 0, conflicts: 0, refreshes: 0, views: 0, requests: 0 };
const newId = () => "d" + crypto.randomBytes(6).toString("hex");
const newVersion = () => "v" + ++seq + "-" + crypto.randomBytes(3).toString("hex");

function slideCount(md) {
  return splitSlides(md).length;
}

// The slides of a Markdown text: a slide starts at # or ## outside code
// fences, after the front matter (as the editor's Session.slideOfLine).
function splitSlides(md) {
  const lines = String(md).replace(/\r\n/g, "\n").split("\n");
  let i = 0;
  if (lines[0] && lines[0].trim() === "---") {
    i = 1;
    while (i < lines.length && lines[i].trim() !== "---") i++;
    i++;
  }
  const slides = [];
  let cur = null;
  let fence = false;
  for (; i < lines.length; i++) {
    const t = lines[i];
    if (t.startsWith("```") || t.startsWith("~~~")) fence = !fence;
    if (!fence && (t.startsWith("# ") || t.startsWith("## "))) {
      cur = { title: t.replace(/^#+\s*/, ""), level: t.startsWith("## ") ? 2 : 1, lines: [] };
      slides.push(cur);
      continue;
    }
    if (!cur) {
      if (!t.trim()) continue;
      cur = { title: "", level: 1, lines: [] };
      slides.push(cur);
    }
    cur.lines.push(t);
  }
  return slides.length ? slides : [{ title: "", level: 1, lines: [] }];
}

function addDeck(name, markdown, extra = {}) {
  const d = { id: extra.id || newId(), name, markdown, theme: "aurora", updated: Date.now() - (extra.age || 0), version: newVersion(), ...extra };
  decks.set(d.id, d);
  return d;
}

const SAMPLE_MD = fs.readFileSync(path.join(HERE, "fixtures", "sample-deck.md"), "utf8");
const SAMPLE_VIEW = JSON.parse(fs.readFileSync(path.join(HERE, "fixtures", "view-sample.json"), "utf8"));
addDeck("Sliqtly Editor sample", SAMPLE_MD, { id: "sample", age: 3 * 60000, sample: true });
addDeck("Quarterly review", "# Quarterly review\n\nNumbers and next steps\n\n## Highlights\n\n- Revenue up 12 %\n- Two new markets\n- Hyvä tiimi: kiitos kaikille!\n\n## Next steps\n\n1. Hire\n2. Ship\n3. Repeat\n", { id: "quarterly", age: 26 * 3600000 });
addDeck("Workshop notes", "# Workshop notes\n\n## Agenda\n\n- Intro\n- Hands-on\n- Wrap-up\n", { id: "workshop", age: 9 * 86400000 });

const deckJson = (d) => ({ id: d.id, name: d.name, markdown: d.markdown, theme: d.theme, updated: d.updated, version: d.version });

// --- a layout of our own: what the preview paints for a deck that is not the sample
function wrap(text, max) {
  const out = [];
  let line = "";
  for (const w of text.split(/\s+/)) {
    if (!w) continue;
    if ((line + " " + w).trim().length > max && line) {
      out.push(line);
      line = w;
    } else line = (line + " " + w).trim();
  }
  if (line) out.push(line);
  return out;
}

function viewOf(d) {
  if (d.sample && d.markdown === SAMPLE_MD) return { ...SAMPLE_VIEW, deck: { ...SAMPLE_VIEW.deck, name: d.name } };
  const W = 960, H = 540;
  const lists = splitSlides(d.markdown).map((s, n) => {
    const cmds = [{ k: 0, x: 0, y: 0, w: W, h: H, c: [11, 16, 48, 1] }];
    let y = 64;
    if (s.title) {
      const size = s.level === 1 ? 48 : 40;
      cmds.push({ k: 3, x: 54, y, w: 852, h: size, c: [232, 236, 255, 1], text: s.title, font: "Open Sans-Bold", size });
      y += size + 23;
      cmds.push({ k: 0, x: 54, y, w: 852, h: 1, c: [45, 58, 122, 1] });
      y += 19;
    }
    for (const raw of s.lines) {
      if (y > H - 50) break;
      const t = raw.trim();
      if (!t || t.startsWith(":::") || t.startsWith("{")) continue;
      const bullet = /^([-*+]|\d+\.)\s+/.exec(t);
      const text = t.replace(/^([-*+]|\d+\.)\s+/, "").replace(/\*\*|__|\*|`/g, "").replace(/^>\s*/, "").replace(/^#+\s*/, "");
      const x = bullet ? 84 : 54;
      if (bullet) cmds.push({ k: 0, x: 60, y: y + 12, w: 9, h: 9, r: 4.5, c: [255, 170, 90, 1] });
      for (const part of wrap(text, bullet ? 48 : 50)) {
        cmds.push({ k: 3, x, y, w: 820, h: 30, c: [232, 236, 255, 1], text: part, font: "Open Sans", size: 28 });
        y += 40;
      }
      y += 4;
    }
    cmds.push({ k: 3, x: W - 80, y: H - 40, w: 40, h: 16, c: [120, 130, 170, 1], text: String(n + 1), font: "Open Sans", size: 16 });
    return { cmds };
  });
  return { deck: { name: d.name, width: W, height: H, slides: lists.length, files: [] }, lists };
}

// --- auth ------------------------------------------------------------------------
const access = new Map(); // token -> { exp, user }
const refreshes = new Map(); // refresh token -> user
const codes = new Map(); // code -> { challenge, redirect, client, exp }
const USER = { id: "u1", name: "Tero Tester", email: "tero@example.com" };

function bearer(req) {
  const h = req.headers.authorization || "";
  const m = /^Bearer\s+(.+)$/i.exec(h);
  return m ? m[1].trim() : "";
}

function authorized(req) {
  if (AUTH === "none") return true;
  const t = bearer(req);
  if (!t) return false;
  if (t === TOKEN) return true; // the static token works in both modes
  const a = access.get(t);
  return !!a && a.exp > Date.now();
}

function issue(user) {
  const at = "at-" + crypto.randomBytes(16).toString("hex");
  const rt = "rt-" + crypto.randomBytes(16).toString("hex");
  access.set(at, { exp: Date.now() + TTL * 1000, user });
  refreshes.set(rt, user);
  return { access_token: at, token_type: "Bearer", expires_in: TTL, refresh_token: rt };
}

const b64url = (buf) => buf.toString("base64").replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");

function redirectAllowed(client, uri) {
  if (client === "sliqtly-desktop") return /^http:\/\/127\.0\.0\.1:\d+\/callback$/.test(uri);
  if (client === "sliqtly-web") return /^https?:\/\/(localhost|127\.0\.0\.1)(:\d+)?\/callback\.html$/.test(uri);
  return false;
}

// --- TLS: a CA of our own and a certificate for localhost / 127.0.0.1 ------------
let tlsInfo = { ownCA: false };
let tlsOptions = null;
if (TLS) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sliqtly-mock-tls-"));
  const run = (...a) => execFileSync("openssl", a, { cwd: dir, stdio: ["ignore", "ignore", "pipe"] });
  run("req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", "ca.key", "-out", "ca.crt", "-days", "2", "-subj", "/CN=Sliqtly mock CA");
  run("req", "-newkey", "rsa:2048", "-nodes", "-keyout", "srv.key", "-out", "srv.csr", "-subj", "/CN=localhost");
  fs.writeFileSync(path.join(dir, "ext.cnf"), "subjectAltName=DNS:localhost,IP:127.0.0.1\nbasicConstraints=CA:FALSE\n");
  run("x509", "-req", "-in", "srv.csr", "-CA", "ca.crt", "-CAkey", "ca.key", "-CAcreateserial", "-out", "srv.crt", "-days", "2", "-extfile", "ext.cnf");
  const caPem = fs.readFileSync(path.join(dir, "ca.crt"), "utf8");
  const der = Buffer.from(caPem.replace(/-----[^-]+-----/g, "").replace(/\s+/g, ""), "base64");
  tlsInfo = { ownCA: true, fingerprint: crypto.createHash("sha256").update(der).digest("hex"), ca: "/ca.crt", pem: caPem };
  tlsOptions = { key: fs.readFileSync(path.join(dir, "srv.key")), cert: fs.readFileSync(path.join(dir, "srv.crt")) + caPem };
}

// --- the server ------------------------------------------------------------------
function cors(req, res) {
  const origin = req.headers.origin || "";
  if (/^https?:\/\/(localhost|127\.0\.0\.1)(:\d+)?$/.test(origin)) {
    res.setHeader("Access-Control-Allow-Origin", origin);
    res.setHeader("Vary", "Origin");
    res.setHeader("Access-Control-Allow-Headers", "Authorization, Content-Type");
    res.setHeader("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS");
    res.setHeader("Access-Control-Expose-Headers", "ETag, WWW-Authenticate");
    res.setHeader("Access-Control-Max-Age", "600");
  }
}

function send(res, status, body, headers = {}) {
  const text = body === undefined ? "" : JSON.stringify(body);
  res.writeHead(status, { ...(text ? { "Content-Type": "application/json; charset=utf-8" } : {}), "Cache-Control": "no-store", ...headers });
  res.end(text);
}

const fail = (res, status, code, error, extra = {}) => send(res, status, { error, code, ...extra });

function readBody(req) {
  return new Promise((resolve) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => resolve(Buffer.concat(chunks).toString("utf8")));
  });
}

function baseUrl(req) {
  return (TLS ? "https" : "http") + "://" + (req.headers.host || "localhost:" + PORT);
}

async function handle(req, res) {
  stats.requests++;
  cors(req, res);
  const url = new URL(req.url, "http://x");
  const p = url.pathname;
  if (req.method === "OPTIONS") {
    res.writeHead(204);
    res.end();
    return;
  }
  if (!QUIET) console.log(`mock: ${req.method} ${p}`);

  // --- test hooks
  if (p === "/__test/state") return send(res, 200, { stats, decks: [...decks.values()].map(deckJson) });
  if (p.startsWith("/__test/bump/") && req.method === "POST") {
    const d = decks.get(decodeURIComponent(p.slice(13)));
    if (!d) return fail(res, 404, "not_found", "no such deck");
    d.markdown += "\n\n## Edited elsewhere\n\nThis slide was added by someone else.\n";
    d.version = newVersion();
    d.updated = Date.now();
    return send(res, 200, deckJson(d));
  }
  if (p === "/__test/expire" && req.method === "POST") {
    for (const a of access.values()) a.exp = 0;
    return send(res, 200, { expired: access.size });
  }

  // --- TLS: the CA, for pinning
  if (p === "/ca.crt") {
    if (!TLS) return fail(res, 404, "not_found", "no own CA");
    res.writeHead(200, { "Content-Type": "application/x-pem-file" });
    res.end(tlsInfo.pem);
    return;
  }

  // --- OAuth
  if (p === "/.well-known/oauth-authorization-server") {
    if (AUTH !== "oauth") return fail(res, 404, "not_found", "no oauth");
    const b = baseUrl(req);
    return send(res, 200, {
      issuer: b,
      authorization_endpoint: b + "/oauth/authorize",
      token_endpoint: b + "/oauth/token",
      registration_endpoint: b + "/oauth/register",
      response_types_supported: ["code"],
      grant_types_supported: ["authorization_code", "refresh_token"],
      code_challenge_methods_supported: ["S256"],
      token_endpoint_auth_methods_supported: ["none"],
    });
  }
  if (p === "/.well-known/oauth-protected-resource") {
    const b = baseUrl(req);
    return send(res, 200, { resource: b, authorization_servers: [b] });
  }
  if (p === "/oauth/authorize") {
    if (AUTH !== "oauth") return fail(res, 404, "not_found", "no oauth");
    const q = url.searchParams;
    const client = q.get("client_id") || "";
    const redirect = q.get("redirect_uri") || "";
    if (!redirectAllowed(client, redirect)) return fail(res, 400, "bad_request", "unknown client or redirect_uri");
    if (q.get("response_type") !== "code" || q.get("code_challenge_method") !== "S256" || !q.get("code_challenge")) {
      return fail(res, 400, "bad_request", "PKCE S256 and response_type=code are required");
    }
    // The real server sends the browser to the provider's sign-in here; the
    // stand-in signs in at once.
    const code = "c-" + crypto.randomBytes(12).toString("hex");
    codes.set(code, { challenge: q.get("code_challenge"), redirect, client, exp: Date.now() + 60000 });
    const to = new URL(redirect);
    to.searchParams.set("code", code);
    if (q.get("state")) to.searchParams.set("state", q.get("state"));
    res.writeHead(302, { Location: to.toString() });
    res.end();
    return;
  }
  if (p === "/oauth/token" && req.method === "POST") {
    if (AUTH !== "oauth") return fail(res, 404, "not_found", "no oauth");
    const form = new URLSearchParams(await readBody(req));
    const grant = form.get("grant_type");
    if (grant === "authorization_code") {
      const c = codes.get(form.get("code") || "");
      codes.delete(form.get("code") || "");
      if (!c || c.exp < Date.now()) return send(res, 400, { error: "invalid_grant" });
      if (c.client !== form.get("client_id") || c.redirect !== form.get("redirect_uri")) return send(res, 400, { error: "invalid_grant", error_description: "client or redirect differs" });
      const challenge = b64url(crypto.createHash("sha256").update(form.get("code_verifier") || "").digest());
      if (challenge !== c.challenge) return send(res, 400, { error: "invalid_grant", error_description: "PKCE verifier does not match" });
      return send(res, 200, issue(USER));
    }
    if (grant === "refresh_token") {
      const rt = form.get("refresh_token") || "";
      const user = refreshes.get(rt);
      if (!user) return send(res, 400, { error: "invalid_grant" });
      refreshes.delete(rt); // rotation: a refresh token is good once
      stats.refreshes++;
      return send(res, 200, issue(user));
    }
    return send(res, 400, { error: "unsupported_grant_type" });
  }

  // --- the API
  if (!p.startsWith("/api/v1/")) return fail(res, 404, "not_found", "not found");
  const rest = p.slice(8);
  if (rest === "info" && req.method === "GET") {
    const b = baseUrl(req);
    const auth = { required: AUTH !== "none", token: AUTH !== "none", oauth: AUTH === "oauth" };
    if (AUTH === "oauth") Object.assign(auth, { issuer: b, provider: "Mock ID" });
    const tls = TLS ? { ownCA: true, fingerprint: tlsInfo.fingerprint, ca: "/ca.crt" } : { ownCA: false };
    return send(res, 200, { name: "Sliqtly", version: "mock-1", api: 1, user: os.userInfo().username, auth, tls });
  }
  if (!authorized(req)) {
    return fail(res, 401, "unauthorized", "sign in or send the token", {}, );
  }
  if (rest === "me" && req.method === "GET") return send(res, 200, USER);
  if (rest === "decks" && req.method === "GET") {
    const list = [...decks.values()].sort((a, b) => b.updated - a.updated).map((d) => ({ id: d.id, name: d.name, updated: d.updated, slides: slideCount(d.markdown) }));
    return send(res, 200, { decks: list });
  }
  if (rest === "decks" && req.method === "POST") {
    let body;
    try { body = JSON.parse(await readBody(req) || "{}"); } catch { return fail(res, 400, "bad_request", "not JSON"); }
    const d = addDeck(String(body.name || "Untitled"), String(body.markdown || ""));
    return send(res, 201, deckJson(d), { ETag: `"${d.version}"` });
  }
  const m = /^decks\/([^/]+)(\/view)?$/.exec(rest);
  if (m) {
    const d = decks.get(decodeURIComponent(m[1]));
    if (!d) return fail(res, 404, "not_found", "no such presentation");
    if (m[2]) {
      if (req.method !== "GET") return fail(res, 400, "bad_request", "GET only");
      stats.views++;
      return send(res, 200, viewOf(d));
    }
    if (req.method === "GET") return send(res, 200, deckJson(d), { ETag: `"${d.version}"` });
    if (req.method === "PUT") {
      let body;
      try { body = JSON.parse(await readBody(req) || "{}"); } catch { return fail(res, 400, "bad_request", "not JSON"); }
      if (body.ifVersion !== undefined && body.ifVersion !== d.version) {
        stats.conflicts++;
        return fail(res, 409, "conflict", "the presentation was changed meanwhile", { current: deckJson(d) });
      }
      if (typeof body.name === "string") d.name = body.name;
      if (typeof body.markdown === "string") d.markdown = body.markdown;
      d.version = newVersion();
      d.updated = Date.now();
      stats.saves++;
      return send(res, 200, deckJson(d), { ETag: `"${d.version}"` });
    }
    if (req.method === "DELETE") {
      decks.delete(d.id);
      res.writeHead(204);
      res.end();
      return;
    }
  }
  return fail(res, 404, "not_found", "not found");
}

const onRequest = (req, res) => {
  if (!authorized(req) && req.url.startsWith("/api/v1/") && !req.url.startsWith("/api/v1/info")) {
    res.setHeader("WWW-Authenticate", `Bearer resource_metadata="${baseUrl(req)}/.well-known/oauth-protected-resource"`);
  }
  handle(req, res).catch((e) => {
    console.error(e);
    if (!res.headersSent) fail(res, 500, "internal", String(e));
  });
};
const server = TLS ? https.createServer(tlsOptions, onRequest) : http.createServer(onRequest);
server.listen(PORT, "127.0.0.1", () => {
  const addr = server.address();
  console.log(`mock: listening on ${TLS ? "https" : "http"}://localhost:${addr.port} (auth ${AUTH}${AUTH !== "none" ? ", token " + TOKEN : ""})`);
});
for (const sig of ["SIGINT", "SIGTERM"]) process.on(sig, () => process.exit(0));
