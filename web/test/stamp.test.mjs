// node --test: a deploy never mixes module versions. index.html (no-cache)
// names main.js?v=<build>; every module it imports, and theirs, must carry
// the same stamp (scripts/stamp.mjs), and only stamped URLs may be cached
// for good (firebase.json). A module left out kept its old copy for an hour
// after a deploy: "does not provide an export named 'firstDir'".
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { stampImports, unstampedImports } from "../../scripts/stamp.mjs";

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const root = path.resolve(web, "..");

test("every kind of relative import gets the stamp", () => {
  const code = [
    'import { a } from "./a.js";',
    "import {\n  b,\n} from './gl/b.js';",
    'import "./side.js";',
    'export { c } from "../c.mjs";',
    'const m = await import("./lazy.js");',
    'import x from "https://cdn.example/x.js";',
    'const s = "./not-an-import.js";',
  ].join("\n");
  const out = stampImports(code, "abc");
  for (const u of ["./a.js", "./gl/b.js", "./side.js", "../c.mjs", "./lazy.js"]) assert.ok(out.includes(u + "?v=abc"), u);
  assert.ok(out.includes('"https://cdn.example/x.js"'));
  assert.ok(out.includes('"./not-an-import.js"'));
  assert.deepEqual(unstampedImports(out), []);
  assert.deepEqual(unstampedImports(code), ["./a.js", "./gl/b.js", "./side.js", "../c.mjs", "./lazy.js"]);
});

test("the page's modules import only in ways the build stamps", () => {
  for (const f of fs.readdirSync(web).filter((f) => /\.m?js$/.test(f))) {
    const code = fs.readFileSync(path.join(web, f), "utf8");
    assert.deepEqual(unstampedImports(stampImports(code, "x")), [], f);
  }
});

// Firebase Hosting globs as firebase.json uses them: **, *, @(a|b)
function globRe(glob) {
  let re = "";
  for (let i = 0; i < glob.length; i++) {
    const c = glob[i];
    if (glob.startsWith("**", i)) { re += ".*"; i++; }
    else if (c === "*") re += "[^/]*";
    else if (glob.startsWith("@(", i)) {
      const end = glob.indexOf(")", i);
      re += "(?:" + glob.slice(i + 2, end).split("|").map((s) => s.replace(/[.]/g, "\\.")).join("|") + ")";
      i = end;
    } else re += c.replace(/[.+?^${}()|[\]\\]/g, "\\$&");
  }
  return new RegExp("^" + re + "$");
}

function cacheControl(url) {
  const rules = JSON.parse(fs.readFileSync(path.join(root, "firebase.json"), "utf8")).hosting.headers;
  let value = null;
  for (const r of rules) {
    const src = r.source.startsWith("/") ? r.source : "/" + r.source;
    const h = r.headers.find((x) => x.key === "Cache-Control");
    if (h && globRe(src).test(url)) value = h.value;
  }
  return value;
}

test("the modules the page loads are cached for good (their URLs carry the build)", () => {
  const html = fs.readFileSync(path.join(web, "index.html"), "utf8");
  const todo = [...html.matchAll(/<script\b[^>]*\ssrc="\.\/([^"?]+)\?v=__BUILD__"/g)].map((m) => m[1]);
  assert.ok(todo.includes("main.js"));
  const seen = new Set();
  while (todo.length) {
    const f = todo.pop();
    if (seen.has(f)) continue;
    seen.add(f);
    const file = path.join(web, f);
    if (!fs.existsSync(file)) continue; // built or copied in (pres_app.js, rangerdiff.mjs, gl/…)
    for (const u of unstampedImports(fs.readFileSync(file, "utf8"))) todo.push(path.posix.normalize(path.posix.join(path.posix.dirname(f), u)));
  }
  for (const f of ["decklist.js", "brand.js", "collab.js", "rangerdiff.mjs", "gl/evg-webgl.js"]) assert.ok(seen.has(f), f);
  for (const f of seen) assert.match(cacheControl("/" + f) || "", /immutable/, f);
  assert.equal(cacheControl("/index.html"), "no-cache");
});
