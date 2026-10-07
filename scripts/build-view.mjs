#!/usr/bin/env node
/**
 * npm run build:view: the public site, web/dist-view, which Firebase Hosting
 * serves (firebase.json "public").
 *
 * The viewer only: /s/{id} shows a shared presentation from display lists
 * the server lays out (/api/view/{id}, mcp-go/rgr/View.rgr), painted by
 * EVG's WebGL painter. No compiled engine (pres_app.js), no editor modules:
 * what is copied here is all the site sends. The editor's build (npm run
 * build, web/dist) is still what the local server and the checks use.
 *
 *   index.html   web/view.html: the viewer, and the front page
 *   view.js …    web/view.js, viewlink.js, viewauth.js (an owner's sign-in),
 *                picture.js and what it imports, brand.js
 *   gl/          evg-webgl.js, the painter
 *   fonts/       the faces the slides are drawn with
 *   personal-license.txt   the Personal package's license (its download)
 *   local.html                 how to run the Personal package on Ubuntu/Debian
 *   connect.html, oauth.html   the assistants' pages (/mcp sends a browser
 *                to the first; sign-in for the MCP server is the second)
 */
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { ensureRanger, root, webDir, log } from "./lib.mjs";
import { copyFaces, facesStamp } from "./build.mjs";
import { stampImports, unstampedImports } from "./stamp.mjs";

export const viewDir = path.join(webDir, "dist-view");

// everything the viewer's modules may import, and nothing more: a module
// that imports anything else fails the build (below)
const MODULES = [["view.js", "view.js"], ["viewlink.js", "viewlink.js"], ["viewauth.js", "viewauth.js"], ["picture.js", "picture.js"], ["image-adjust.js", "image-adjust.js"], ["brand.js", "brand.js"], ["book.js", "book.js"], ["bookgl.js", "bookgl.js"]];
const PAGES = [["view.html", "index.html"], ["connect.html", "connect.html"], ["oauth.html", "oauth.html"], ["local.html", "local.html"]];

function files(dir) {
  return fs.readdirSync(dir, { recursive: true })
    .map((f) => f.split(path.sep).join("/"))
    .filter((f) => fs.statSync(path.join(dir, f)).isFile())
    .sort();
}

export function buildView({ ranger } = {}) {
  ranger = ranger || ensureRanger();
  fs.rmSync(viewDir, { recursive: true, force: true });
  const copy = (from, to) => {
    fs.mkdirSync(path.dirname(path.join(viewDir, to)), { recursive: true });
    fs.copyFileSync(from, path.join(viewDir, to));
  };
  for (const [from, to] of [...PAGES, ...MODULES]) copy(path.join(webDir, from), to);
  copy(path.join(ranger, "lib/evg/gl/evg-webgl.js"), "gl/evg-webgl.js");
  copy(path.join(root, "brand/sliqtly-icon.svg"), "favicon.svg");
  // the Personal package's license, linked from the front page
  copy(path.join(root, "mcp-go/packaging/personal/LICENSE"), "personal-license.txt");
  copy(path.join(webDir, "examples/sliqtly-example.pdf"), "examples/sliqtly-example.pdf");
  copyFaces(ranger, viewDir);

  // every module's relative imports are among the files copied
  const shipped = new Set(files(viewDir));
  for (const f of [...shipped].filter((f) => f.endsWith(".js"))) {
    const text = fs.readFileSync(path.join(viewDir, f), "utf8");
    for (const m of text.matchAll(/(?:\bfrom\s*|\bimport\s*\(?\s*)["'](\.{1,2}\/[^"'?#]+)["']/g)) {
      const rel = path.posix.normalize(path.posix.join(path.posix.dirname(f), m[1]));
      if (!shipped.has(rel)) throw new Error(`build-view: ${f} imports ${m[1]}, which the viewer does not ship`);
    }
  }

  // the build's stamp on the page's script and every module import, as in
  // build.mjs: a reload never mixes an old module with a new one
  const h = crypto.createHash("sha1");
  for (const f of files(viewDir).filter((f) => !f.startsWith("fonts/"))) h.update(f).update(fs.readFileSync(path.join(viewDir, f)));
  const stamp = h.digest("hex").slice(0, 10);
  const fonts = facesStamp(viewDir);
  const html = path.join(viewDir, "index.html");
  fs.writeFileSync(html, fs.readFileSync(html, "utf8").split("__BUILD__").join(stamp).split("__FONTS__").join(fonts));
  const modules = files(viewDir).filter((f) => /\.m?js$/.test(f));
  for (const f of modules) {
    const file = path.join(viewDir, f);
    fs.writeFileSync(file, stampImports(fs.readFileSync(file, "utf8"), stamp));
  }
  const left = modules.flatMap((f) => unstampedImports(fs.readFileSync(path.join(viewDir, f), "utf8")).map((u) => `${f}: ${u}`));
  if (left.length) throw new Error("imports without the build stamp:\n  " + left.join("\n  "));
  const bytes = files(viewDir).reduce((n, f) => n + fs.statSync(path.join(viewDir, f)).size, 0);
  log(`build  web/dist-view (${stamp}, ${files(viewDir).length} files, ${(bytes / 1048576).toFixed(1)} MB)`);
  return stamp;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  try {
    buildView();
  } catch (e) {
    log(e.message);
    process.exit(1);
  }
}
