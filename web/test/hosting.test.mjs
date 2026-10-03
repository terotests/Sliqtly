// node --test: every file the assistant's preview fetches from the site
// (mcp/src/preview.html: index.html, its scripts and pictures, and the
// modules they import) is served with Access-Control-Allow-Origin
// (firebase.json), or the preview fails with "Failed to fetch".
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const root = path.resolve(web, "..");

// the extensions the hosting's CORS rule covers: **/*.@(a|b|c)
function corsExtensions() {
  const hosting = JSON.parse(fs.readFileSync(path.join(root, "firebase.json"), "utf8")).hosting;
  const rule = hosting.headers.find((h) => h.headers.some((x) => x.key === "Access-Control-Allow-Origin"));
  const m = /^\*\*\/\*\.@\(([^)]*)\)$/.exec(rule.source);
  return new Set(m[1].split("|"));
}

// what the preview fetches: the page's <script src> and <img src>, and
// every relative import of those modules, followed through
function fetched() {
  const html = fs.readFileSync(path.join(web, "index.html"), "utf8");
  const files = new Set();
  const todo = [];
  for (const m of html.matchAll(/<(?:script|img)\b[^>]*\ssrc="\.\/([^"?]+)/g)) {
    files.add(m[1]);
    if (/\.m?js$/.test(m[1])) todo.push(m[1]);
  }
  while (todo.length) {
    const f = todo.pop();
    const file = path.join(web, f);
    if (!fs.existsSync(file)) continue; // built, not in web/ (pres_app.js, rangerdiff.mjs …)
    const text = fs.readFileSync(file, "utf8");
    for (const m of text.matchAll(/\b(?:from|import)\s*\(?\s*["'](\.{1,2}\/[^"']+)["']/g)) {
      const rel = path.posix.normalize(path.posix.join(path.posix.dirname(f), m[1]));
      if (!files.has(rel)) {
        files.add(rel);
        todo.push(rel);
      }
    }
  }
  return [...files];
}

test("every file the preview fetches is served to other origins", () => {
  const ok = corsExtensions();
  const files = fetched();
  assert.ok(files.includes("main.js") && files.includes("rangerdiff.mjs"), files.join(", "));
  const missing = files.filter((f) => !ok.has(path.extname(f).slice(1)));
  assert.deepEqual(missing, []);
});
