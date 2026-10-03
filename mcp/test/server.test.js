// The server end to end over Streamable HTTP, with Firestore, Storage and
// the network replaced by fakes.

import zlib from "node:zlib";
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
  const bucket = { name: "bucket.test", file: (name) => ({ name, save: async (buf, o) => { saved.set(name, { buf, o }); }, download: async () => [saved.get(name).buf] }) };
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
    assert.deepEqual(tools.map((x) => x.name).sort(), ["bind_chart_data", "create_presentation", "get_presentation", "list_files", "list_presentations", "read_file", "sliqtly_guide", "update_presentation"]);
    const create = tools.find((x) => x.name === "create_presentation");
    assert.match(create._meta.ui.resourceUri, /^ui:\/\/sliqtly\/preview-[0-9a-f]{10}\.html$/);
    assert.equal(create._meta["openai/outputTemplate"], create._meta.ui.resourceUri);
    assert.ok(create.inputSchema.properties.theme.enum.includes("editorial"));
    const r = await t.client.readResource({ uri: create._meta.ui.resourceUri });
    assert.equal(r.contents[0].mimeType, "text/html;profile=mcp-app");
    assert.match(r.contents[0].text, /ui\/initialize/);
    assert.deepEqual(r.contents[0]._meta.ui.csp.frameDomains, [BASE, "https://sliqtly.com", "https://sliqtly.web.app"]);
    // Claude frames nothing but blob:, so the preview loads the viewer itself
    assert.ok(r.contents[0]._meta.ui.csp.resourceDomains.includes("https://www.gstatic.com"));
    assert.ok(r.contents[0]._meta.ui.csp.connectDomains.includes("https://firestore.googleapis.com"));
    assert.deepEqual(r.contents[0]._meta["openai/widgetCSP"].connect_domains, r.contents[0]._meta.ui.csp.connectDomains);
    // a client that kept an older tool list still gets the preview
    const old = await t.client.readResource({ uri: "ui://sliqtly/preview-0000000000.html" });
    assert.equal(old.contents[0].uri, "ui://sliqtly/preview-0000000000.html");
    assert.equal(old.contents[0].text, r.contents[0].text);
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

test("bind_chart_data points a chart at live data", async () => {
  const { store, data } = fakeFirebase();
  const t = await start(store, rateLimiter());
  try {
    const md = "# Q3\n\n## Revenue\n\n```vega-lite\n" + JSON.stringify({ mark: "bar", data: { values: [{ m: "Jan", v: 1 }] }, encoding: { x: { field: "m" } } }) +
      "\n```\n\n## Costs\n\n```vega-lite\n" + JSON.stringify({ layer: [{ mark: "line", data: { values: [] } }] }) + "\n```\n";
    const c = await t.client.callTool({ name: "create_presentation", arguments: { title: "Q3", markdown: md } });
    const { deck_id, edit_key } = c.structuredContent;

    const a = await t.client.callTool({ name: "bind_chart_data", arguments: { deck_id, edit_key, chart: "revenue", source: { google_sheets: "SHEET1", range: "Monthly!A:B" } } });
    assert.ok(!a.isError, a.content[0].text);
    assert.equal(a.structuredContent.chart, 1);
    assert.deepEqual(a.structuredContent.spec.data, { source: "google-sheets", id: "SHEET1", range: "Monthly!A:B" });
    assert.deepEqual(a.structuredContent.spec.encoding, { x: { field: "m" } });
    assert.equal(a.structuredContent.share_url, c.structuredContent.share_url);

    const b = await t.client.callTool({ name: "bind_chart_data", arguments: { deck_id, edit_key, chart: 2, source: "https://data.test/costs.csv" } });
    assert.ok(!b.isError, b.content[0].text);
    assert.deepEqual(b.structuredContent.spec, { layer: [{ mark: "line" }], data: { url: "https://data.test/costs.csv" } });
    const stored = data.get(`shares/${deck_id}`).md;
    assert.match(stored, /"id": "SHEET1"/);
    assert.match(stored, /costs\.csv/);
    assert.match(stored, /## Costs/);

    for (const [args, re] of [
      [{ chart: 3, source: "https://data.test/x.csv" }, /has 2 charts/],
      [{ chart: "Nope", source: "https://data.test/x.csv" }, /No chart on a slide titled/],
      [{ chart: 1, source: "http://data.test/x.csv" }, /https URL/],
      [{ chart: 1, source: "https://data.test/x.csv", edit_key: "wrong" }, /edit_key does not match/],
    ]) {
      const r = await t.client.callTool({ name: "bind_chart_data", arguments: { deck_id, edit_key, ...args } });
      assert.ok(r.isError);
      assert.match(r.content[0].text, re);
    }
    assert.equal(data.get(`shares/${deck_id}`).md, stored);
  } finally { await t.close(); }
});

test("warns about an encoding type Vega-Lite does not know", async () => {
  const { store } = fakeFirebase();
  const t = await start(store, rateLimiter());
  try {
    const spec = { data: { values: [{ m: "Jan", h: 7.5 }] }, layer: [{ mark: "line", encoding: { x: { field: "m", type: "point" }, y: { field: "h", type: "quantitative" } } }] };
    const md = "# T\n\n## Wake-up\n\n```vega-lite\n" + JSON.stringify(spec) + "\n```\n";
    const c = await t.client.callTool({ name: "create_presentation", arguments: { title: "T", markdown: md } });
    assert.ok(!c.isError, c.content[0].text);
    assert.match(c.content[0].text, /Note: Chart 1 on "Wake-up": encoding x has type "point"; Vega-Lite types are quantitative, ordinal, nominal and temporal/);
    const ok = await t.client.callTool({ name: "update_presentation", arguments: { deck_id: c.structuredContent.deck_id, edit_key: c.structuredContent.edit_key, markdown: md.replace('"point"', '"ordinal"') } });
    assert.deepEqual(ok.structuredContent.warnings, []);
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

// A zip of stored entries, enough to stand in for an .xlsx.
function zipStored(entries) {
  const locals = [];
  const centrals = [];
  let offset = 0;
  for (const [name, text] of entries) {
    const data = Buffer.from(text, "utf8");
    const nameBuf = Buffer.from(name, "utf8");
    const crc = zlib.crc32(data);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0); local.writeUInt16LE(20, 4); local.writeUInt32LE(crc, 14);
    local.writeUInt32LE(data.length, 18); local.writeUInt32LE(data.length, 22); local.writeUInt16LE(nameBuf.length, 26);
    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0); central.writeUInt16LE(20, 4); central.writeUInt16LE(20, 6); central.writeUInt32LE(crc, 16);
    central.writeUInt32LE(data.length, 20); central.writeUInt32LE(data.length, 24); central.writeUInt16LE(nameBuf.length, 28);
    central.writeUInt32LE(offset, 42);
    locals.push(local, nameBuf, data);
    centrals.push(central, nameBuf);
    offset += 30 + nameBuf.length + data.length;
  }
  const dir = Buffer.concat(centrals);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0); end.writeUInt16LE(entries.length, 8); end.writeUInt16LE(entries.length, 10);
  end.writeUInt32LE(dir.length, 12); end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, dir, end]);
}

test("list_files: the files a deck keeps, and what is in its workbooks", async () => {
  const { store, data, saved } = fakeFirebase();
  const sheetXml = (rows) => `<worksheet><sheetData>${rows}</sheetData></worksheet>`;
  const book = zipStored([
    ["xl/workbook.xml", '<workbook><sheets><sheet name="Sales" sheetId="1" r:id="rId1"/><sheet name="Q &amp; A" sheetId="2" r:id="rId2"/><sheet name="Empty" sheetId="3" r:id="rId3"/></sheets></workbook>'],
    ["xl/_rels/workbook.xml.rels", '<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Target="worksheets/sheet2.xml"/><Relationship Id="rId3" Target="worksheets/sheet3.xml"/></Relationships>'],
    ["xl/sharedStrings.xml", "<sst><si><t>Sales 2024</t></si><si><t>Region</t></si><si><t>Revenue</t></si><si><t>North</t></si><si><r><t>So</t></r><r><t>uth</t></r></si></sst>"],
    ["xl/worksheets/sheet1.xml", sheetXml('<row r="1"><c r="A1" t="s"><v>0</v></c></row><row r="2"><c r="A2" t="s"><v>1</v></c><c r="B2" t="s"><v>2</v></c></row><row r="3"><c r="A3" t="s"><v>3</v></c><c r="B3"><v>120</v></c></row><row r="4"><c r="A4" t="s"><v>4</v></c><c r="B4"><v>80</v></c></row>')],
    ["xl/worksheets/sheet2.xml", sheetXml('<row r="1"><c r="A1" t="inlineStr"><is><t>Question</t></is></c><c r="C1" t="inlineStr"><is><t>Answer</t></is></c></row><row r="2"><c r="A2" t="inlineStr"><is><t>Why?</t></is></c><c r="C2" t="b"><v>1</v></c></row>')],
    ["xl/worksheets/sheet3.xml", sheetXml("")],
  ]);
  data.set("shares/abcDEF1234", { name: "Data deck", md: "# D\n", theme: "aurora", files: [
    { path: "media/cat.png", type: "image/png", size: 68 },
    { path: "data/book.xlsx", type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", size: book.length },
    { path: "data/notes.csv", type: "text/csv", size: 10 },
  ] });
  saved.set("shares/abcDEF1234/data/book.xlsx", { buf: book });
  const t = await start(store);
  try {
    const r = await t.client.callTool({ name: "list_files", arguments: { deck_id: "abcDEF1234" } });
    assert.ok(!r.isError, r.content[0].text);
    const files = r.structuredContent.files;
    assert.deepEqual(files.map((f) => [f.path, f.kind]), [["media/cat.png", "picture"], ["data/book.xlsx", "workbook"], ["data/notes.csv", "data"]]);
    const wb = files[1];
    assert.deepEqual(wb.sheets, [
      { name: "Sales", columns: ["Region", "Revenue"], rows: 2, csv: "data/book-Sales.csv" },
      { name: "Q & A", columns: ["Question", "", "Answer"], rows: 1, csv: "data/book-Q-&-A.csv" },
    ]);
    assert.match(r.content[0].text, /sheet "Sales": 2 rows; columns "Region", "Revenue"; read as data\/book-Sales\.csv/);
    const g = await t.client.callTool({ name: "get_presentation", arguments: { deck_id: "abcDEF1234" } });
    assert.deepEqual(g.structuredContent.images.map((i) => i.name), ["cat.png"]);
    assert.equal(g.structuredContent.files[1].sheets[0].name, "Sales");
    assert.match(g.content[0].text, /data\/book\.xlsx \(workbook/);
  } finally { await t.close(); }
});

// A workbook with a title row over the header, a float and a second sheet.
function testBook() {
  const sheetXml = (rows) => `<worksheet><sheetData>${rows}</sheetData></worksheet>`;
  const rows = ['<row r="1"><c r="A1" t="inlineStr"><is><t>Card risk</t></is></c></row>',
    '<row r="2"><c r="A2" t="inlineStr"><is><t>Month</t></is></c><c r="B2" t="inlineStr"><is><t>Cards</t></is></c></row>'];
  for (let i = 1; i <= 12; i++) rows.push(`<row r="${i + 2}"><c r="A${i + 2}"><v>${i}</v></c><c r="B${i + 2}"><v>${i === 3 ? "0.30000000000000004" : i * 10}</v></c></row>`);
  return zipStored([
    ["xl/workbook.xml", '<workbook><sheets><sheet name="Monthly" sheetId="1" r:id="rId1"/><sheet name="Notes" sheetId="2" r:id="rId2"/></sheets></workbook>'],
    ["xl/_rels/workbook.xml.rels", '<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Target="worksheets/sheet2.xml"/></Relationships>'],
    ["xl/worksheets/sheet1.xml", sheetXml(rows.join(""))],
    ["xl/worksheets/sheet2.xml", sheetXml('<row r="1"><c r="A1" t="inlineStr"><is><t>Key</t></is></c><c r="B1" t="inlineStr"><is><t>Value</t></is></c></row><row r="2"><c r="A2" t="inlineStr"><is><t>source</t></is></c><c r="B2" t="inlineStr"><is><t>bank, "core"</t></is></c></row>')],
  ]);
}

test("read_file: a workbook's sheets, CSV, JSON, in windows", async () => {
  const { store, data, saved } = fakeFirebase();
  const book = testBook();
  data.set("shares/abcDEF1234", { name: "Risk", md: "# R\n", theme: "aurora", files: [
    { path: "media/cat.png", type: "image/png", size: 68 },
    { path: "data/risk.xlsx", type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", size: book.length },
    { path: "data/notes.csv", type: "text/csv", size: 30 },
    { path: "data/spec.json", type: "application/json", size: 9 },
  ] });
  saved.set("shares/abcDEF1234/data/risk.xlsx", { buf: book });
  saved.set("shares/abcDEF1234/data/notes.csv", { buf: Buffer.from('a,b\r\n"x, y",2\n\n3,"q"""\n') });
  saved.set("shares/abcDEF1234/data/spec.json", { buf: Buffer.from('{"a": 1}') });
  const t = await start(store);
  const read = (args) => t.client.callTool({ name: "read_file", arguments: { deck_id: "abcDEF1234", ...args } });
  try {
    const r = await read({ path: "data/risk.xlsx", limit: 5 });
    assert.ok(!r.isError, r.content[0].text);
    assert.deepEqual(r.structuredContent.sheet, { name: "Monthly", csv: "data/risk-Monthly.csv" });
    assert.deepEqual(r.structuredContent.columns, ["Month", "Cards"]);
    assert.equal(r.structuredContent.total_rows, 12);
    assert.deepEqual(r.structuredContent.rows[2], ["3", "0.3"]);
    assert.equal(r.structuredContent.next_offset, 5);
    assert.match(r.content[0].text, /Rows 1–5 of 12; next: offset 5/);
    assert.match(r.content[0].text, /Other sheets: "Notes"/);
    const last = await read({ path: "data/risk.xlsx", offset: 10 });
    assert.deepEqual(last.structuredContent.rows, [["11", "110"], ["12", "120"]]);
    assert.equal(last.structuredContent.next_offset, null);
    // by the name the deck reads the sheet by, and by sheet name
    const byCsv = await read({ path: "data/risk-Notes.csv" });
    assert.deepEqual(byCsv.structuredContent.rows, [["source", 'bank, "core"']]);
    assert.match(byCsv.content[0].text, /source,"bank, ""core"""/);
    const bySheet = await read({ path: "data/risk.xlsx", sheet: "notes" });
    assert.equal(bySheet.structuredContent.sheet.name, "Notes");
    const noSheet = await read({ path: "data/risk.xlsx", sheet: "Yearly" });
    assert.ok(noSheet.isError);
    assert.match(noSheet.content[0].text, /no sheet "Yearly". Its sheets: "Monthly", "Notes"/);
    const csv = await read({ path: "data/notes.csv" });
    assert.deepEqual(csv.structuredContent.columns, ["a", "b"]);
    assert.deepEqual(csv.structuredContent.rows, [["x, y", "2"], ["3", 'q"']]);
    const json = await read({ path: "data/spec.json" });
    assert.equal(json.structuredContent.text, '{"a": 1}');
    const pic = await read({ path: "media/cat.png" });
    assert.match(pic.content[0].text, /is a picture/);
    const none = await read({ path: "data/other.csv" });
    assert.match(none.content[0].text, /No file data\/other\.csv .* Its files: data\/risk\.xlsx, data\/notes\.csv, data\/spec\.json/);
  } finally { await t.close(); }
});

test("create and update keep data files the deck reads", async () => {
  const { store, data, saved } = fakeFirebase();
  const t = await start(store);
  try {
    const md = '# Risk\n\n## Monthly\n\n```vega-lite\n{"data": {"url": "data/risk-Monthly.csv"}, "mark": "bar"}\n```\n';
    const r = await t.client.callTool({ name: "create_presentation", arguments: {
      title: "Risk", markdown: md,
      files: [{ name: "risk.xlsx", data_base64: testBook().toString("base64") }, { name: "data/extra.csv", text: "a,b\n1,2\n" }],
    } });
    assert.ok(!r.isError, r.content[0].text);
    const id = r.structuredContent.deck_id;
    assert.match(r.content[0].text, /data\/risk\.xlsx: sheet "Monthly" \(12 rows; columns "Month", "Cards"\) read as data\/risk-Monthly\.csv/);
    assert.deepEqual(data.get(`shares/${id}`).files.map((f) => [f.path, f.type]), [
      ["data/risk.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"], ["data/extra.csv", "text/csv"]]);
    assert.equal(saved.get(`shares/${id}/data/extra.csv`).buf.toString(), "a,b\n1,2\n");
    const back = await t.client.callTool({ name: "read_file", arguments: { deck_id: id, path: "data/risk-Monthly.csv", limit: 1 } });
    assert.deepEqual(back.structuredContent.rows, [["1", "10"]]);
    // update adds a file and replaces one of the same name
    const u = await t.client.callTool({ name: "update_presentation", arguments: {
      deck_id: id, edit_key: r.structuredContent.edit_key, files: [{ name: "extra.csv", text: "a,b\n3,4\n" }, { name: "more.json", text: "[1]" }],
    } });
    assert.ok(!u.isError, u.content[0].text);
    assert.deepEqual(data.get(`shares/${id}`).files.map((f) => f.path), ["data/risk.xlsx", "data/extra.csv", "data/more.json"]);
    assert.equal(saved.get(`shares/${id}/data/extra.csv`).buf.toString(), "a,b\n3,4\n");
    // refused: not a workbook, a type that is not data, two sources
    for (const [f, why] of [
      [{ name: "bad.xlsx", data_base64: Buffer.from("nope").toString("base64") }, /not a workbook Sliqtly can read/],
      [{ name: "run.exe", text: "x" }, /data files are \.xlsx, \.csv/],
      [{ name: "a.csv", text: "x", url: "https://images.test/a.csv" }, /give one of text, data_base64 or url/],
      [{ name: "a.xlsx", text: "x" }, /sent as data_base64 or url/],
    ]) {
      const bad = await t.client.callTool({ name: "create_presentation", arguments: { title: "x", markdown: "# x", files: [f] } });
      assert.ok(bad.isError);
      assert.match(bad.content[0].text, why);
    }
  } finally { await t.close(); }
});

test("warns about text that likely does not stand out from its background picture", async () => {
  const { PNG: Png } = await import("pngjs");
  const sky = (top, bottom) => {
    const p = new Png({ width: 160, height: 90 });
    for (let y = 0; y < 90; y++) {
      for (let x = 0; x < 160; x++) {
        const c = y < 45 ? top : bottom, i = (y * 160 + x) * 4;
        p.data[i] = c[0]; p.data[i + 1] = c[1]; p.data[i + 2] = c[2]; p.data[i + 3] = 255;
      }
    }
    return Png.sync.write(p).toString("base64");
  };
  const css = "page { background-color: #0b1030; }\ndocument { font-size: 20pt; color: #e8ecff; }\n";
  const md = "# Deck\n\n## Cloudy {bg=media/sky.png}\n\nLight text over a white sky.\n\n## Night {bg=media/night.png}\n\nLight text over a dark picture.\n";
  const { store } = fakeFirebase();
  const t = await start(store);
  try {
    const r = await t.client.callTool({ name: "create_presentation", arguments: { title: "Sky", markdown: md, css, css_mode: "replace", images: [
      { name: "sky.png", data_base64: sky([244, 246, 248], [230, 235, 240]) },
      { name: "night.png", data_base64: sky([20, 30, 40], [28, 58, 36]) },
    ] } });
    const ws = r.structuredContent.warnings;
    assert.equal(ws.length, 1, JSON.stringify(ws));
    assert.match(ws[0], /^Slide "Cloudy": text is likely hard to read over the background picture \(estimated[^)]*\): the heading about 1\.\d:1 \(needs 3\.0:1\), the body text about 1\.\d:1 \(needs 3\.0:1\)\. Fix: a stronger dim, bg-dim=0\.\d+ in the slide's heading attributes, or a text colour such as #[0-9a-f]{6} in css\.$/);
    const dim = /bg-dim=(0\.\d+)/.exec(ws[0])[1];
    // the suggested dim is enough; an update reads the stored picture back
    const { deck_id, edit_key } = r.structuredContent;
    const same = await t.client.callTool({ name: "update_presentation", arguments: { deck_id, edit_key, markdown: md } });
    assert.match(same.structuredContent.warnings.join("\n"), /Slide "Cloudy": text is likely hard to read/);
    const u = await t.client.callTool({ name: "update_presentation", arguments: { deck_id, edit_key, markdown: md.replace("{bg=media/sky.png}", `{bg=media/sky.png bg-dim=${dim}}`) } });
    assert.deepEqual(u.structuredContent.warnings, []);
  } finally {
    await t.close();
  }
});

test("fetched URLs: redirects are checked, bodies are capped", async () => {
  const { loadImages, publicFetch } = await import("../src/deck.js");
  const big = new Uint8Array(6 * 1024 * 1024);
  const f = async (url) => {
    const u = String(url);
    if (u === "https://images.test/to-metadata") return new Response(null, { status: 302, headers: { location: "http://169.254.169.254/computeMetadata/v1/" } });
    if (u === "https://images.test/to-local") return new Response(null, { status: 301, headers: { location: "https://localhost/x.png" } });
    if (u === "https://images.test/to-cat") return new Response(null, { status: 302, headers: { location: "/cat.png" } });
    if (u === "https://images.test/cat.png") return new Response(PNG, { headers: { "content-type": "image/png" } });
    if (u === "https://images.test/loop") return new Response(null, { status: 302, headers: { location: "/loop" } });
    // no content-length: the size is only known by reading
    if (u === "https://images.test/big.png") return new Response(new ReadableStream({ start(c) { c.enqueue(big); c.close(); } }), { headers: { "content-type": "image/png" } });
    return new Response("no", { status: 404 });
  };
  await assert.rejects(loadImages([{ name: "a.png", url: "https://images.test/to-metadata" }], f), /only public https URLs/);
  await assert.rejects(loadImages([{ name: "a.png", url: "https://images.test/to-local" }], f), /only public https URLs/);
  await assert.rejects(loadImages([{ name: "a.png", url: "https://images.test/loop" }], f), /too many redirects/);
  await assert.rejects(loadImages([{ name: "a.png", url: "https://images.test/big.png" }], f), /larger than 5 MB/);
  const [cat] = await loadImages([{ name: "a.png", url: "https://images.test/to-cat" }], f);
  assert.equal(cat.data.length, PNG.length);
  await assert.rejects(publicFetch("https://localhost./x", f, { max: 10 }), /only public https URLs/);
});
