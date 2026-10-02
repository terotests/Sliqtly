// The server end to end over Streamable HTTP, with Firestore, Storage and
// the network replaced by fakes.

import test from "node:test";
import assert from "node:assert/strict";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { createApp, rateLimiter } from "../src/http.js";
import { createOAuth } from "../src/oauth.js";
import { FirebaseStore, LinkStore, unpackText } from "../src/store.js";

const BASE = "https://sliqtly.test";
const PNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==", "base64");

function fakeFirebase() {
  const data = new Map();
  const saved = new Map();
  const db = {
    collection: (c) => ({
      doc: (id) => {
        const k = `${c}/${id}`;
        return {
          get: async () => ({ exists: data.has(k), data: () => structuredClone(data.get(k)) }),
          set: async (v) => { data.set(k, structuredClone(v)); },
          update: async (v) => { data.set(k, { ...data.get(k), ...structuredClone(v) }); },
          delete: async () => { data.delete(k); },
        };
      },
      where: (field, op, value) => ({
        get: async () => ({
          docs: [...data].filter(([k, v]) => k.startsWith(c + "/") && v[field] === value)
            .map(([k, v]) => ({ id: k.slice(c.length + 1), data: () => structuredClone(v) })),
        }),
      }),
    }),
  };
  const bucket = { name: "bucket.test", file: (name) => ({ name, save: async (buf, o) => { saved.set(name, { buf, o }); } }) };
  let clock = 1000;
  const store = new FirebaseStore({ db, bucket, FieldValue: { serverTimestamp: () => clock++ } });
  return { data, saved, db, store };
}

async function fakeFetch(url) {
  const u = String(url);
  if (u === `${BASE}/themes/aurora.css`) return new Response("page { background-color: #0b1030; }");
  if (u === `${BASE}/themes/corporate.css`) return new Response("page { background-color: #fff; }");
  if (u === "https://images.test/cat.png") return new Response(PNG, { headers: { "content-type": "image/png" } });
  if (u === "https://client.test/meta.json") return Response.json({ client_id: u, client_name: "Test Client", redirect_uris: ["https://client.test/cb"] });
  if (u === "https://images.test/page.html") return new Response("<html>", { headers: { "content-type": "text/html" } });
  return new Response("no", { status: 404 });
}

async function start(store, limiter, oauth = null, token = null) {
  const app = createApp({ store, baseUrl: BASE, fetchImpl: fakeFetch, limiter, oauth });
  const srv = await new Promise((ok) => { const s = app.listen(0, () => ok(s)); });
  const root = `http://127.0.0.1:${srv.address().port}`;
  const url = `${root}/mcp`;
  const client = new Client({ name: "test", version: "1" });
  const headers = token ? { authorization: `Bearer ${token}` } : {};
  await client.connect(new StreamableHTTPClientTransport(new URL(url), { requestInit: { headers } }));
  return { root, url, client, srv, close: async () => { await client.close(); srv.close(); } };
}

const DECK = "# Hello\n\nFirst.\n{.lead}\n\n## Cat {bg=media/cat.png}\n\n![](media/dot.png)\n\n```mermaid\nflowchart LR\n## not a slide\n```\n";

test("tools, UI metadata and the preview resource", async () => {
  const { store } = fakeFirebase();
  const t = await start(store);
  try {
    const { tools } = await t.client.listTools();
    assert.deepEqual(tools.map((x) => x.name).sort(), ["create_presentation", "get_presentation", "list_presentations", "sliqtly_guide", "update_presentation"]);
    const create = tools.find((x) => x.name === "create_presentation");
    assert.equal(create._meta.ui.resourceUri, "ui://sliqtly/preview.html");
    assert.equal(create._meta["openai/outputTemplate"], "ui://sliqtly/preview.html");
    assert.ok(create.inputSchema.properties.theme.enum.includes("editorial"));
    const r = await t.client.readResource({ uri: "ui://sliqtly/preview.html" });
    assert.equal(r.contents[0].mimeType, "text/html;profile=mcp-app");
    assert.match(r.contents[0].text, /ui\/initialize/);
    assert.deepEqual(r.contents[0]._meta.ui.csp.frameDomains, [BASE, "https://sliqtly.com", "https://sliqtly.web.app"]);
    const g = await t.client.callTool({ name: "sliqtly_guide", arguments: {} });
    assert.match(g.content[0].text, /## Pictures/);
  } finally { await t.close(); }
});

test("create, update and read a deck with pictures", async () => {
  const { store, data, saved } = fakeFirebase();
  const t = await start(store);
  try {
    const c = await t.client.callTool({ name: "create_presentation", arguments: {
      title: "Cats", markdown: DECK, css: "h1 { font-size: 60pt; }",
      images: [{ name: "cat.png", url: "https://images.test/cat.png" }, { name: "dot.png", data_base64: "data:image/png;base64," + PNG.toString("base64") }],
    } });
    assert.ok(!c.isError, c.content[0].text);
    const out = c.structuredContent;
    assert.equal(out.slides, 2);
    assert.deepEqual(out.warnings, []);
    assert.equal(out.share_url, `${BASE}/s/${out.deck_id}`);
    assert.equal(out.edit_url, `${BASE}/s/${out.deck_id}?edit`);
    const share = data.get(`shares/${out.deck_id}`);
    assert.equal(share.source, "mcp");
    assert.equal(share.theme, "aurora");
    assert.match(share.css, /#0b1030[\s\S]*font-size: 60pt/);
    assert.deepEqual(share.files.map((f) => f.path), ["media/cat.png", "media/dot.png"]);
    assert.match(share.files[0].url, /^https:\/\/firebasestorage\.googleapis\.com\/v0\/b\/bucket\.test\/o\/shares%2F.*%2Fmedia%2Fcat\.png\?alt=media&token=/);
    assert.equal(saved.get(`shares/${out.deck_id}/media/cat.png`).o.contentType, "image/png");
    assert.notEqual(data.get(`mcp_keys/${out.deck_id}`).hash, out.edit_key);

    const bad = await t.client.callTool({ name: "update_presentation", arguments: { deck_id: out.deck_id, edit_key: "wrong", markdown: "# x" } });
    assert.ok(bad.isError);
    assert.equal(data.get(`shares/${out.deck_id}`).md, DECK);

    const u = await t.client.callTool({ name: "update_presentation", arguments: {
      deck_id: out.deck_id, edit_key: out.edit_key, markdown: DECK + "\n## More\n\n![](media/new.png)\n", theme: "corporate",
    } });
    assert.ok(!u.isError, u.content[0].text);
    assert.equal(u.structuredContent.share_url, out.share_url);
    assert.equal(u.structuredContent.slides, 3);
    assert.deepEqual(u.structuredContent.warnings, ["media/new.png is used in the Markdown but no image by that name was sent."]);
    const after = data.get(`shares/${out.deck_id}`);
    assert.equal(after.theme, "corporate");
    assert.equal(after.css, null, "a new theme without css drops the old theme's sheet");
    assert.equal(after.files.length, 2);

    const g = await t.client.callTool({ name: "get_presentation", arguments: { deck_id: out.deck_id } });
    assert.equal(g.structuredContent.markdown, after.md);
    assert.deepEqual(g.structuredContent.images.map((i) => i.name), ["cat.png", "dot.png"]);
  } finally { await t.close(); }
});

test("refuses what it should not fetch or store", async () => {
  const { store } = fakeFirebase();
  const t = await start(store);
  try {
    for (const [img, why] of [
      [{ name: "a.png", url: "http://images.test/cat.png" }, /public https/],
      [{ name: "a.png", url: "https://169.254.169.254/x" }, /public https/],
      [{ name: "a.png", url: "https://localhost/x" }, /public https/],
      [{ name: "a.png", url: "https://images.test/page.html" }, /not a picture/],
      [{ name: "../a.png", url: "https://images.test/cat.png" }, /not usable/],
      [{ name: "a.bmp", data_base64: "AAAA" }, /unknown picture type/],
      [{ name: "a.png" }, /url or data_base64/],
    ]) {
      const r = await t.client.callTool({ name: "create_presentation", arguments: { title: "x", markdown: "# x", images: [img] } });
      assert.ok(r.isError, JSON.stringify(img));
      assert.match(r.content[0].text, why);
    }
    const e = await t.client.callTool({ name: "create_presentation", arguments: { title: "x", markdown: "  " } });
    assert.ok(e.isError);
  } finally { await t.close(); }
});

test("without cloud storage the deck travels in the link", async () => {
  const t = await start(new LinkStore());
  try {
    const c = await t.client.callTool({ name: "create_presentation", arguments: { title: "x", markdown: DECK, theme: "ember" } });
    assert.ok(!c.isError, c.content[0].text);
    const url = new URL(c.structuredContent.share_url);
    const q = new URLSearchParams(url.hash.slice(1));
    assert.equal(unpackText(q.get("md")), DECK);
    assert.equal(q.get("theme"), "ember");
    assert.equal(q.get("mode"), "show");
    assert.ok(!c.structuredContent.edit_url.includes("mode=show"));
  } finally { await t.close(); }
});

test("rate limit and a browser visit", async () => {
  const { store } = fakeFirebase();
  const t = await start(store, rateLimiter({ max: 1 }));
  try {
    const a = await t.client.callTool({ name: "create_presentation", arguments: { title: "x", markdown: "# x" } });
    assert.ok(!a.isError);
    const b = await t.client.callTool({ name: "create_presentation", arguments: { title: "x", markdown: "# x" } });
    assert.match(b.content[0].text, /Too many/);
    const g = await t.client.callTool({ name: "sliqtly_guide", arguments: {} });
    assert.ok(!g.isError, "reading is not limited");
    const res = await fetch(t.url, { redirect: "manual" });
    assert.equal(res.status, 302);
    assert.equal(res.headers.get("location"), `${BASE}/connect.html`);
  } finally { await t.close(); }
});

test("optional sign-in: OAuth with PKCE, own decks, refresh", async () => {
  const { store, data, db } = fakeFirebase();
  const oauth = createOAuth({ db, fetchImpl: fakeFetch, verifyIdToken: async (t) => { if (t !== "google-ok") throw new Error("bad"); return { uid: "u1", name: "Tero" }; } });
  const anon = await start(store, undefined, oauth);
  const root = anon.root;
  const form = (o) => ({ method: "POST", headers: { "content-type": "application/x-www-form-urlencoded" }, body: new URLSearchParams(o) });
  const json = (o) => ({ method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(o) });
  try {
    const prm = await (await fetch(`${root}/.well-known/oauth-protected-resource/mcp`)).json();
    assert.equal(prm.resource, `${BASE}/mcp`);
    const asm = await (await fetch(`${root}/.well-known/oauth-authorization-server`)).json();
    assert.equal(asm.token_endpoint, `${BASE}/oauth/token`);
    assert.ok(asm.client_id_metadata_document_supported);

    // anonymous use still works, and listing asks for sign-in
    const a = await anon.client.callTool({ name: "create_presentation", arguments: { title: "anon", markdown: "# a" } });
    assert.ok(!a.isError);
    assert.equal(data.get(`shares/${a.structuredContent.deck_id}`).owner, "mcp");
    const l = await anon.client.callTool({ name: "list_presentations", arguments: {} });
    assert.ok(l.isError);
    assert.match(l._meta["mcp/www_authenticate"][0], /resource_metadata=/);

    const bad = await (await fetch(`${root}/oauth/register`, json({ redirect_uris: ["http://evil.test/cb"] }))).json();
    assert.equal(bad.error, "invalid_redirect_uri");
    const reg = await fetch(`${root}/oauth/register`, json({ client_name: "Claude", redirect_uris: ["http://127.0.0.1:5000/cb"] }));
    assert.equal(reg.status, 201);
    const { client_id } = await reg.json();

    const verifier = "v".repeat(50);
    const challenge = (await import("node:crypto")).createHash("sha256").update(verifier).digest("base64url");
    const authorize = async (cid, redirect) => {
      const q = new URLSearchParams({ response_type: "code", client_id: cid, redirect_uri: redirect, code_challenge: challenge, code_challenge_method: "S256", state: "st", resource: `${BASE}/mcp` });
      const r = await fetch(`${root}/oauth/authorize?${q}`, { redirect: "manual" });
      assert.equal(r.status, 302);
      return new URL(r.headers.get("location"));
    };
    // a loopback redirect may use another port
    const page = await authorize(client_id, "http://127.0.0.1:6123/cb");
    assert.equal(page.origin + page.pathname, `${BASE}/oauth.html`);
    assert.equal(page.searchParams.get("client"), "Claude");
    assert.equal(page.searchParams.get("to"), "this computer");
    const request = page.searchParams.get("request");

    const wrongGoogle = await fetch(`${root}/oauth/approve`, json({ request, id_token: "nope" }));
    assert.equal(wrongGoogle.status, 401);
    // the request survives a failed Google check
    const ok = await (await fetch(`${root}/oauth/approve`, json({ request, id_token: "google-ok" }))).json();
    const back = new URL(ok.redirect);
    assert.equal(back.searchParams.get("state"), "st");
    assert.equal(back.searchParams.get("iss"), BASE);
    const code = back.searchParams.get("code");
    const used = await fetch(`${root}/oauth/approve`, json({ request, id_token: "google-ok" }));
    assert.equal(used.status, 400, "a request gives one code");

    const noPkce = await (await fetch(`${root}/oauth/token`, form({ grant_type: "authorization_code", code, client_id, redirect_uri: "http://127.0.0.1:6123/cb", code_verifier: "x".repeat(50) }))).json();
    assert.equal(noPkce.error, "invalid_grant");
    // the code is single-use: the failed attempt spent it
    const page3 = await authorize(client_id, "http://127.0.0.1:6123/cb");
    const ok3 = await (await fetch(`${root}/oauth/approve`, json({ request: page3.searchParams.get("request"), id_token: "google-ok" }))).json();
    const code3 = new URL(ok3.redirect).searchParams.get("code");
    const tok = await (await fetch(`${root}/oauth/token`, form({ grant_type: "authorization_code", code: code3, client_id, redirect_uri: "http://127.0.0.1:6123/cb", code_verifier: verifier }))).json();
    assert.equal(tok.token_type, "Bearer");
    const again = await (await fetch(`${root}/oauth/token`, form({ grant_type: "authorization_code", code: code3, client_id, redirect_uri: "http://127.0.0.1:6123/cb", code_verifier: verifier }))).json();
    assert.equal(again.error, "invalid_grant");

    // signed in: the deck is the user's, changed without an edit key, listed
    const me = await start(store, undefined, oauth, tok.access_token);
    try {
      const c = await me.client.callTool({ name: "create_presentation", arguments: { title: "Mine", markdown: "# m" } });
      assert.ok(!c.isError, c.content[0].text);
      assert.match(c.content[0].text, /account of Tero/);
      const id = c.structuredContent.deck_id;
      assert.equal(data.get(`shares/${id}`).owner, "u1");
      const u = await me.client.callTool({ name: "update_presentation", arguments: { deck_id: id, markdown: "# m\n\n## two" } });
      assert.ok(!u.isError, u.content[0].text);
      assert.equal(u.structuredContent.slides, 2);
      const notMine = await me.client.callTool({ name: "update_presentation", arguments: { deck_id: a.structuredContent.deck_id, markdown: "# x" } });
      assert.match(notMine.content[0].text, /edit_key is needed/);
      const list = await me.client.callTool({ name: "list_presentations", arguments: {} });
      assert.deepEqual(list.structuredContent.presentations.map((p) => p.deck_id), [id]);
    } finally { await me.close(); }
    const mineId = [...data.keys()].find((k) => k.startsWith("shares/") && data.get(k).owner === "u1").slice(7);
    const anonUpdate = await anon.client.callTool({ name: "update_presentation", arguments: { deck_id: mineId, markdown: "# x" } });
    assert.ok(anonUpdate.isError);

    // a token that does not hold: 401 with where to sign in
    const r401 = await fetch(`${root}/mcp`, { method: "POST", headers: { "content-type": "application/json", accept: "application/json, text/event-stream", authorization: "Bearer nope" }, body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }) });
    assert.equal(r401.status, 401);
    assert.match(r401.headers.get("www-authenticate"), /resource_metadata="https:\/\/sliqtly\.test\/\.well-known\/oauth-protected-resource\/mcp"/);

    // refresh rotates
    const r1 = await (await fetch(`${root}/oauth/token`, form({ grant_type: "refresh_token", refresh_token: tok.refresh_token, client_id }))).json();
    assert.ok(r1.access_token && r1.refresh_token !== tok.refresh_token);
    const r2 = await (await fetch(`${root}/oauth/token`, form({ grant_type: "refresh_token", refresh_token: tok.refresh_token, client_id }))).json();
    assert.equal(r2.error, "invalid_grant");

    // a client known by its metadata document URL
    const cimd = await authorize("https://client.test/meta.json", "https://client.test/cb");
    assert.equal(cimd.searchParams.get("client"), "Test Client");
    assert.equal(cimd.searchParams.get("to"), "client.test");
    const wrongRedirect = await fetch(`${root}/oauth/authorize?${new URLSearchParams({ response_type: "code", client_id: "https://client.test/meta.json", redirect_uri: "https://evil.test/cb", code_challenge: challenge, code_challenge_method: "S256" })}`, { redirect: "manual" });
    assert.equal(wrongRedirect.status, 400);
    // denying sends the client an error
    const den = await (await fetch(`${root}/oauth/approve`, json({ request: cimd.searchParams.get("request"), deny: true }))).json();
    assert.equal(new URL(den.redirect).searchParams.get("error"), "access_denied");
  } finally { await anon.close(); }
});
