// The server end to end over Streamable HTTP, with Firestore, Storage and
// the network replaced by fakes.

import test from "node:test";
import assert from "node:assert/strict";
import express from "express";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { mcpHandler, rateLimiter } from "../src/http.js";
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
        };
      },
    }),
  };
  const bucket = { name: "bucket.test", file: (name) => ({ name, save: async (buf, o) => { saved.set(name, { buf, o }); } }) };
  return { data, saved, store: new FirebaseStore({ db, bucket, FieldValue: { serverTimestamp: () => "now" } }) };
}

async function fakeFetch(url) {
  const u = String(url);
  if (u === `${BASE}/themes/aurora.css`) return new Response("page { background-color: #0b1030; }");
  if (u === `${BASE}/themes/corporate.css`) return new Response("page { background-color: #fff; }");
  if (u === "https://images.test/cat.png") return new Response(PNG, { headers: { "content-type": "image/png" } });
  if (u === "https://images.test/page.html") return new Response("<html>", { headers: { "content-type": "text/html" } });
  return new Response("no", { status: 404 });
}

async function start(store, limiter) {
  const app = express();
  app.use(express.json({ limit: "40mb" }));
  app.all("/mcp", mcpHandler({ store, baseUrl: BASE, fetchImpl: fakeFetch, limiter }));
  const srv = await new Promise((ok) => { const s = app.listen(0, () => ok(s)); });
  const url = `http://127.0.0.1:${srv.address().port}/mcp`;
  const client = new Client({ name: "test", version: "1" });
  await client.connect(new StreamableHTTPClientTransport(new URL(url)));
  return { url, client, close: async () => { await client.close(); srv.close(); } };
}

const DECK = "# Hello\n\nFirst.\n{.lead}\n\n## Cat {bg=media/cat.png}\n\n![](media/dot.png)\n\n```mermaid\nflowchart LR\n## not a slide\n```\n";

test("tools, UI metadata and the preview resource", async () => {
  const { store } = fakeFirebase();
  const t = await start(store);
  try {
    const { tools } = await t.client.listTools();
    assert.deepEqual(tools.map((x) => x.name).sort(), ["create_presentation", "get_presentation", "sliqtly_guide", "update_presentation"]);
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
