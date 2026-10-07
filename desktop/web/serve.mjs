#!/usr/bin/env node
// SPDX-License-Identifier: MIT
//
// Serve the web build on http://127.0.0.1:8140/ (npm run web).
// A static server rooted at web/ and nothing else; the Sliqtly server the
// page talks to is wherever the user points it (CORS is that server's job).
//
//   node web/serve.mjs [--port 8140]

import fs from "node:fs";
import http from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const argv = process.argv.slice(2);
const at = argv.indexOf("--port");
const PORT = at >= 0 ? Number(argv[at + 1]) : Number(process.env.PORT || 8140);
const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".png": "image/png",
  ".ttf": "font/ttf",
  ".txt": "text/plain; charset=utf-8",
};

const server = http.createServer((req, res) => {
  const pathname = decodeURIComponent(req.url.split("?")[0]);
  const name = pathname.endsWith("/") ? pathname + "index.html" : pathname;
  const file = path.join(HERE, path.normalize(name).replace(/^(\.\.[/\\])+/, ""));
  if (!file.startsWith(HERE) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) {
    res.writeHead(404, { "Content-Type": "text/plain" });
    res.end("not found");
    return;
  }
  res.writeHead(200, { "Content-Type": TYPES[path.extname(file)] ?? "application/octet-stream", "Cache-Control": "no-cache" });
  res.end(fs.readFileSync(file));
});
server.listen(PORT, "127.0.0.1", () => {
  process.stdout.write(`\n  Sliqtly Editor (web) on http://127.0.0.1:${PORT}/\n`);
});
