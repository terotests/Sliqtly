// node --test: what Firebase Hosting serves (firebase.json). The site is the
// viewer's build (scripts/build-view.mjs, page web/view.html), and every file
// the assistant's preview fetches from it (mcp-go/assets/preview.html:
// index.html, its scripts and pictures, and the modules they import) is
// served with Access-Control-Allow-Origin, or the preview fails with
// "Failed to fetch".
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
  const html = fs.readFileSync(path.join(web, "view.html"), "utf8");
  const files = new Set();
  const todo = [];
  for (const m of html.matchAll(/<(?:script|img)\b[^>]*\ssrc="\.\/([^"?]+)/g)) {
    files.add(m[1]);
    if (/\.m?js$/.test(m[1])) todo.push(m[1]);
  }
  while (todo.length) {
    const f = todo.pop();
    const file = path.join(web, f);
    if (!fs.existsSync(file)) continue; // built, not in web/ (gl/evg-webgl.js)
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
  assert.ok(files.includes("view.js") && files.includes("gl/evg-webgl.js"), files.join(", "));
  const missing = files.filter((f) => !ok.has(path.extname(f).slice(1)));
  assert.deepEqual(missing, []);
});

const hosting = () => JSON.parse(fs.readFileSync(path.join(root, "firebase.json"), "utf8")).hosting;

test("the site is the viewer's build, not the editor's", () => {
  assert.equal(hosting().public, "web/dist-view");
});

test("a shared presentation is the viewer's page, its slides the server's", () => {
  const rw = hosting().rewrites;
  const at = (src) => rw.findIndex((r) => r.source === src);
  // the page comes through the server, which writes the deck's link card
  // (og:title, og:image) into the site's index.html (mcp-go/linkcard.go)
  assert.equal(rw[at("/s/**")].run.serviceId, "sliqtly-mcp");
  assert.equal(rw[at("/api/card/**")].run.serviceId, "sliqtly-mcp");
  // the server sets the page's Cache-Control (a minute in the CDN)
  assert.ok(!hosting().headers.some((h) => h.source === "/s/**"));
  assert.equal(rw[at("/api/view/**")].run.serviceId, "sliqtly-mcp");
  // the first rule that matches wins
  assert.ok(at("/api/view/**") < at("/s/**"));
  // the viewer's downloads (Export ▾) are made there too
  assert.equal(rw[at("/api/export/**")].run.serviceId, "sliqtly-mcp");
});

test("the editor's pages and the engine are not in the viewer's build", async () => {
  const src = fs.readFileSync(path.join(root, "scripts", "build-view.mjs"), "utf8");
  for (const f of ["main.js", "pres_app.js", "sliqtly.js", "vfs.js", "rooms.js", "collab.js"]) {
    assert.ok(!src.includes(`"${f}"`), f);
  }
});

test("the owner's dashboard: its page, its numbers from the server, linked nowhere", () => {
  const rw = hosting().rewrites;
  const api = rw.find((r) => r.source === "/main/admin/api/**");
  assert.equal(api.run.serviceId, "sliqtly-mcp");
  assert.equal(rw.find((r) => r.source === "/main/admin").destination, "/main/admin.html");
  assert.ok(rw.indexOf(api) < rw.findIndex((r) => r.source === "/main/admin"));
  const src = fs.readFileSync(path.join(root, "scripts", "build-view.mjs"), "utf8");
  assert.ok(src.includes('["admin.html", "main/admin.html"]'));
  for (const page of ["view.html", "connect.html", "oauth.html", "local.html"]) {
    assert.ok(!fs.readFileSync(path.join(web, page), "utf8").includes("/main/admin"), page);
  }
});

test("the editor comes only from the server, which sends it to signed-in people (mcp-go/editor.go)", () => {
  const rw = hosting().rewrites;
  for (const src of ["/editor", "/editor/**"]) {
    const r = rw.find((x) => x.source === src);
    assert.equal(r?.run?.serviceId, "sliqtly-mcp", src);
  }
  // no rule of the site's own files catches /editor first
  const first = rw.findIndex((r) => r.source.startsWith("/editor"));
  assert.ok(rw.slice(0, first).every((r) => !r.destination), "a file rule before /editor");
  assert.ok(!fs.existsSync(path.join(web, "dist-view", "editor")));
});
