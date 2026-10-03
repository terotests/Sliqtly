#!/usr/bin/env node
// The Node server (mcp/) and the Go server (mcp-go/) answer the same MCP
// calls alike: both run here in link mode (no Firestore), with the site's
// themes served from this checkout, and every call's answer is compared.
//
//   node mcp-go/parity.mjs [path/to/go/server/binary]
//
// Without a binary it runs `go build` in mcp-go/ first (after go generate).
// Needs mcp/'s node_modules (npm ci in mcp/). Exit code 1 on any difference
// that is not listed in KNOWN below.

import fs from "node:fs";
import http from "node:http";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.dirname(here);

// the site: themes from the checkout, no Firebase config
function site() {
  return new Promise((ok) => {
    const s = http.createServer((req, res) => {
      const m = /^\/themes\/([a-z-]+)\.css$/.exec(req.url);
      const f = m && path.join(root, "themes", `${m[1]}.css`);
      if (f && fs.existsSync(f)) {
        res.writeHead(200, { "content-type": "text/css" });
        return res.end(fs.readFileSync(f));
      }
      res.writeHead(404).end();
    });
    s.listen(0, "127.0.0.1", () => ok(s));
  });
}

function startServer(cmd, args, env, port) {
  const p = spawn(cmd, args, { env: { ...process.env, ...env, PORT: String(port) }, stdio: ["ignore", "pipe", "pipe"] });
  let log = "";
  p.stdout.on("data", (d) => { log += d; });
  p.stderr.on("data", (d) => { log += d; });
  p.on("exit", (code) => { if (code) console.error(`${cmd} exited ${code}\n${log}`); });
  return { p, log: () => log };
}

async function waitFor(url) {
  for (let i = 0; i < 100; i++) {
    try { await fetch(url, { method: "OPTIONS" }); return; } catch {}
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`${url} did not start`);
}

let nextId = 1;
async function rpc(url, method, params) {
  const res = await fetch(url, {
    method: "POST",
    headers: { "content-type": "application/json", accept: "application/json, text/event-stream", "mcp-protocol-version": "2025-06-18" },
    body: JSON.stringify({ jsonrpc: "2.0", id: nextId++, method, params }),
  });
  const text = await res.text();
  const body = text.startsWith("event:") || text.startsWith("data:")
    ? JSON.parse(text.split("\n").find((l) => l.startsWith("data:")).slice(5))
    : JSON.parse(text);
  return { status: res.status, body };
}

// what may differ between the two by design
const norm = (v) => JSON.parse(JSON.stringify(v), (k, x) => {
  if (k === "id" && typeof x === "number") return 0;
  if (typeof x === "string") {
    return x
      .replace(/http:\/\/127\.0\.0\.1:\d+/g, "SITE")
      .replace(/#md=[A-Za-z0-9_-]+/g, "#md=…")
      .replace(/ui:\/\/sliqtly\/preview-[a-f0-9]+\.html/g, "ui://sliqtly/preview.html");
  }
  return x;
});

// Differences that are known and kept, each with why. A key is a call name
// plus a JSON path; the value says why it differs.
const KNOWN = {
  "initialize body.result.serverInfo": "each server names its own build",
  // Go lays the deck out with the editor's PresDeck and judges contrast on
  // the display list; Node estimates from the theme. The Go warnings are the
  // ones kept.
  "create:vegalite body.result.structuredContent.warnings": "Go checks with the editor's layout",
  "create:vegalite body.result.content": "carries the warnings",
  "create:contrast body.result.structuredContent.warnings": "Go checks with the editor's layout",
  "create:contrast body.result.content": "carries the warnings",
  "create:overflow body.result.structuredContent.warnings": "Go checks with the editor's layout",
  "create:overflow body.result.content": "carries the warnings",
  "create:overflow body.result.structuredContent.slides": "Go counts slides as the player does",
  // the Node SDK checks arguments with zod and answers an unknown tool as a
  // tool error; Go words the argument error itself and answers an unknown
  // tool as a JSON-RPC error (what the MCP spec asks)
  "create:no-markdown body.result.content.0.text": "who words the argument error",
  "unknown tool body": "tool error vs JSON-RPC error",
};

// A tool as a client reads it. The schemas' JSON Schema details differ by
// generator (zod vs written by hand: additionalProperties, integer bounds,
// how a union is spelled), so a schema is compared by its properties, their
// types and descriptions, and what is required.
function shape(schema) {
  if (!schema || typeof schema !== "object") return schema;
  const props = {};
  for (const [k, v] of Object.entries(schema.properties || {})) props[k] = { type: v.type, description: v.description, enum: v.enum };
  return { type: schema.type, properties: props, required: [...(schema.required || [])].sort() };
}
const tool = (t) => ({ name: t.name, title: t.title, description: t.description, annotations: t.annotations, _meta: t._meta, inputSchema: shape(t.inputSchema), outputSchema: shape(t.outputSchema) });

function diff(a, b, at, out) {
  if (JSON.stringify(a) === JSON.stringify(b)) return;
  if (a && b && typeof a === "object" && typeof b === "object" && Array.isArray(a) === Array.isArray(b)) {
    for (const k of new Set([...Object.keys(a), ...Object.keys(b)])) diff(a[k], b[k], at ? `${at}.${k}` : k, out);
    return;
  }
  out.push({ at, node: a, go: b });
}

const DECKS = {
  plain: "# Hello\n\nFirst.\n{.lead}\n\n## Second\n\n- one\n- two\n",
  vegalite: fs.readFileSync(path.join(root, "samples", "vegalite.en.md"), "utf8"),
  contrast: "# Pale {color=#eeeeee}\n\nOn white.\n",
  overflow: "# Long\n\n" + Array.from({ length: 60 }, (_, i) => `- line ${i}`).join("\n") + "\n",
};

async function main() {
  let bin = process.argv[2];
  if (!bin) {
    bin = path.join(fs.mkdtempSync(path.join(process.env.TMPDIR || "/tmp", "mcp-go-")), "server");
    const b = spawnSync("go", ["build", "-o", bin, "."], { cwd: here, stdio: "inherit" });
    if (b.status) process.exit(1);
  }
  const s = await site();
  const base = `http://127.0.0.1:${s.address().port}`;
  const env = { SLIQTLY_URL: base, SLIQTLY_STORE: "link", GOOGLE_APPLICATION_CREDENTIALS: "" };
  const nodePort = 18790 + Math.floor(Math.random() * 1000);
  const goPort = nodePort + 1000;
  const node = startServer(process.execPath, [path.join(root, "mcp", "local.js")], env, nodePort);
  const go = startServer(bin, [], env, goPort);
  const urls = { node: `http://127.0.0.1:${nodePort}/mcp`, go: `http://127.0.0.1:${goPort}/mcp` };
  const found = [];
  try {
    await Promise.all([waitFor(urls.node), waitFor(urls.go)]);
    const calls = [
      ["initialize", "initialize", { protocolVersion: "2025-06-18", capabilities: {}, clientInfo: { name: "parity", version: "1" } }],
      ["tools/list", "tools/list", {}],
      ["resources/list", "resources/list", {}],
      ["guide", "tools/call", { name: "sliqtly_guide", arguments: {} }],
      ...Object.entries(DECKS).map(([k, markdown]) => [`create:${k}`, "tools/call", { name: "create_presentation", arguments: { title: k, markdown } }]),
      ["create:no-markdown", "tools/call", { name: "create_presentation", arguments: { title: "x" } }],
      ["update:no-key", "tools/call", { name: "update_presentation", arguments: { deck_id: "nope", edit_key: "x", markdown: "# a" } }],
      ["get:missing", "tools/call", { name: "get_presentation", arguments: { deck_id: "nope" } }],
      ["list_files:missing", "tools/call", { name: "list_files", arguments: { deck_id: "nope" } }],
      ["unknown tool", "tools/call", { name: "no_such_tool", arguments: {} }],
      ["unknown method", "no/such", {}],
    ];
    for (const [name, method, params] of calls) {
      const [a, b] = await Promise.all([rpc(urls.node, method, params), rpc(urls.go, method, params)]);
      if (method === "tools/list") for (const r of [a, b]) r.body.result.tools = r.body.result.tools.map(tool);
      const out = [];
      diff(norm(a), norm(b), "", out);
      for (const d of out) {
        const key = `${name} ${d.at}`;
        const known = Object.keys(KNOWN).find((k) => key === k || key.startsWith(k + "."));
        if (!known) found.push({ call: name, ...d });
      }
    }
    // a browser opening /mcp, and a GET without the event stream
    for (const [k, u] of Object.entries(urls)) {
      const r = await fetch(u, { redirect: "manual" });
      if (r.status !== 302 || r.headers.get("location") !== `${base}/connect.html`) found.push({ call: "GET /mcp", at: k, node: 302, go: r.status });
    }
    // the visit beacon: always 204, from any origin (mcp/local.js has no
    // counter, the function has; its answer is 204 too)
    for (const origin of [base, "https://elsewhere.test"]) {
      const r = await fetch(urls.go.replace("/mcp", "/api/hit"), { method: "POST", headers: { origin, "content-type": "application/json" }, body: '{"p":"editor"}' });
      if (r.status !== 204) found.push({ call: "POST /api/hit", at: origin, node: 204, go: r.status });
    }
  } finally {
    node.p.kill();
    go.p.kill();
    s.close();
  }
  if (found.length) {
    for (const f of found) console.log(`${f.call} ${f.at}\n  node: ${JSON.stringify(f.node)?.slice(0, 400)}\n  go:   ${JSON.stringify(f.go)?.slice(0, 400)}`);
    console.log(`\n${found.length} difference(s)`);
    process.exit(1);
  }
  console.log("Node and Go answer alike.");
}

main().catch((e) => { console.error(e); process.exit(1); });
