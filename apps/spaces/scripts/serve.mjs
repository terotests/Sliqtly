#!/usr/bin/env node
/** npm run spaces:serve — serve apps/spaces/web/dist (npm run spaces:build first). PORT, default 8090. */
import http from "node:http";
import fs from "node:fs";
import path from "node:path";
import { appDir } from "./lib.mjs";

const dist = path.join(appDir, "web", "dist");
const TYPES = { ".html": "text/html; charset=utf-8", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml" };

/** A handler for the files under `dir`: (path below it, response). */
export function serveDir(dir) {
  return (rel, res) => {
    const file = path.join(dir, path.normalize("/" + decodeURIComponent(rel || "index.html")));
    if (!file.startsWith(dir + path.sep) || !fs.existsSync(file) || !fs.statSync(file).isFile()) {
      res.writeHead(404);
      res.end("not found");
      return;
    }
    res.writeHead(200, { "content-type": TYPES[path.extname(file)] || "application/octet-stream" });
    fs.createReadStream(file).pipe(res);
  };
}

/** A static server for the built page; resolves to the server once it listens. */
export function serveDist(port = 0) {
  const files = serveDir(dist);
  const server = http.createServer((req, res) => files(new URL(req.url, "http://localhost").pathname.slice(1), res));
  return new Promise((ok) => server.listen(port, "127.0.0.1", () => ok(server)));
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const server = await serveDist(Number(process.env.PORT || 8090));
  console.log(`Spaces on http://127.0.0.1:${server.address().port}/`);
}
