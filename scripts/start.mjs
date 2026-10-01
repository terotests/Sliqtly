#!/usr/bin/env node
/**
 * npm start [-- --port=8770] [-- --no-build]
 *
 * Builds when a source is newer than the bundle, then serves web/dist on
 * localhost. The server is the place the later stages hang their local API
 * on (speech synthesis, the agent); today it serves files and /api/health.
 */
import fs from "node:fs";
import path from "node:path";
import http from "node:http";
import { ensureRanger, srcDir, webDir, distDir, root, log } from "./lib.mjs";
import { build } from "./build.mjs";

const arg = (name, fallback) => {
  const hit = process.argv.find((a) => a.startsWith(`--${name}=`));
  return hit ? hit.split("=")[1] : fallback;
};
const port = parseInt(arg("port", process.env.PORT || "8770"), 10);

function newest(dir) {
  let t = 0;
  for (const name of fs.readdirSync(dir)) {
    const p = path.join(dir, name);
    const st = fs.statSync(p);
    t = Math.max(t, st.isDirectory() ? newest(p) : st.mtimeMs);
  }
  return t;
}

const ranger = ensureRanger();
const bundle = path.join(distDir, "pres_app.js");
const stale = !fs.existsSync(bundle)
  || Math.max(newest(srcDir), newest(webDir === distDir ? srcDir : path.join(root, "web")) , newest(path.join(root, "samples")), newest(path.join(root, "themes"))) > fs.statSync(bundle).mtimeMs;
if (!process.argv.includes("--no-build") && stale) build({ ranger });

const TYPES = {
  ".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".mjs": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8", ".md": "text/markdown; charset=utf-8", ".ttf": "font/ttf", ".png": "image/png",
  ".jpg": "image/jpeg", ".svg": "image/svg+xml", ".json": "application/json", ".wav": "audio/wav", ".mp3": "audio/mpeg",
};

const server = http.createServer((req, res) => {
  const url = new URL(req.url, "http://localhost");
  if (url.pathname === "/api/health") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ ok: true, tts: false, agent: false }));
    return;
  }
  let rel = decodeURIComponent(url.pathname);
  if (rel.endsWith("/")) rel += "index.html";
  const file = path.normalize(path.join(distDir, rel));
  if (!file.startsWith(distDir + path.sep) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) {
    res.writeHead(404);
    res.end("not found");
    return;
  }
  res.writeHead(200, { "content-type": TYPES[path.extname(file)] || "application/octet-stream", "cache-control": "no-cache" });
  fs.createReadStream(file).pipe(res);
});
server.listen(port, "127.0.0.1", () => log(`serve  http://localhost:${port}/`));
