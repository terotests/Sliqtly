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
 *   view.js …    web/view.js, viewlink.js, viewload.js, slidetext.js, viewauth.js (an owner's sign-in),
 *                picture.js and what it imports, brand.js
 *   play.html    /r/{id}/{hash}: a presentation exported as recorded (the
 *                editor's Export ▸ Cloud HTML), played by play.js with
 *                playlink.js, recstream.js, fxdeck.js and the painter only;
 *                one exported with the 3-D flight also playflight.js,
 *                flight3d.js, three3d.js and pres_3d.js, loaded then only
 *   gl/          evg-webgl.js, the painter
 *   viewplay.js, apps.js, pres_play.js, cerxes.wasm …   programs on slides
 *                (```app), loaded only for a deck that has one
 *   three3d.js, pres_3d.js   their 3-D worlds (allow: 3d), the engine
 *                loaded only when a world is on the slide
 *   fonts/       the faces the slides are drawn with
 *   personal-license.txt   the Personal package's license (its download)
 *   local.html                 how to run the Personal package on Ubuntu/Debian
 *   connect.html, oauth.html   the assistants' pages (/mcp sends a browser
 *                to the first; sign-in for the MCP server is the second)
 *   main/admin.html, admin.js  the owner's dashboard at /main/admin
 *                (numbers only for the accounts mcp-go/admin.go lets in)
 *   web/1.html, web/2.html, web/3.html  trial front pages at /web/1../web/3 (web/alt/),
 *                linked from nowhere and not indexed
 *   web/use-cases.html, web/use-cases/   the use cases page they link to, with
 *                pictures of its example decks (web/alt/use-cases/)
 *   apps/warden/  the Warden page (apps/spaces, npm run warden:build): the
 *                Spaces app on the server's /apps/warden/ API; built here when
 *                apps/spaces/web/dist-warden is not there yet
 */
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { ensureRanger, ensureCerxes, compile, root, webDir, log } from "./lib.mjs";
import { copyFaces, facesStamp, minify, buildThree, buildScriptSel, markParts } from "./build.mjs";
import { stampImports, unstampedImports } from "./stamp.mjs";
import { buildSpaces, wardenDir } from "../apps/spaces/scripts/build.mjs";

export const viewDir = path.join(webDir, "dist-view");

// everything the viewer's modules may import, and nothing more: a module
// that imports anything else fails the build (below)
const MODULES = [["view.js", "view.js"], ["viewlink.js", "viewlink.js"], ["viewload.js", "viewload.js"], ["slidetext.js", "slidetext.js"], ["viewauth.js", "viewauth.js"], ["picture.js", "picture.js"], ["image-adjust.js", "image-adjust.js"], ["brand.js", "brand.js"], ["book.js", "book.js"], ["bookgl.js", "bookgl.js"], ["bookturn.js", "bookturn.js"], ["admin.js", "admin.js"], ["viewplay.js", "viewplay.js"], ["apps.js", "apps.js"], ["apps-runtime.js", "apps-runtime.js"], ["sound.js", "sound.js"], ["script-runtime.js", "script-runtime.js"], ["sprite-runtime.js", "sprite-runtime.js"], ["presenter-runtime.js", "presenter-runtime.js"], ["cerxes-worker.js", "cerxes-worker.js"], ["fxdeck.js", "fxdeck.js"], ["play.js", "play.js"], ["playlink.js", "playlink.js"], ["recstream.js", "recstream.js"], ["three3d.js", "three3d.js"], ["sitescript.js", "sitescript.js"], ["flight3d.js", "flight3d.js"], ["playflight.js", "playflight.js"]];
const PAGES = [["view.html", "index.html"], ["connect.html", "connect.html"], ["oauth.html", "oauth.html"], ["local.html", "local.html"], ["admin.html", "main/admin.html"], ["play.html", "play.html"], ["alt/1.html", "web/1.html"], ["alt/2.html", "web/2.html"], ["alt/3.html", "web/3.html"], ["alt/use-cases.html", "web/use-cases.html"]];

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
  for (const f of files(path.join(webDir, "alt/use-cases"))) copy(path.join(webDir, "alt/use-cases", f), "web/use-cases/" + f);
  copyFaces(ranger, viewDir);

  // programs on slides (```app): the editor's runner (apps.js and CErXes
  // in a Worker) and PresPlayWeb, which lays a program's tree out and
  // paints it; loaded by view.js only for a deck that has one
  const playJs = path.join(viewDir, "pres_play.js");
  compile(ranger, "PresViewPlay.rgr", playJs);
  fs.writeFileSync(playJs, "// loaded on demand by viewplay.js: the programs and scripts on a deck's slides.\n"
    + "(function () {\n" + fs.readFileSync(playJs, "utf8") + "\n;globalThis.PresPlayWeb = PresPlayWeb;\nglobalThis.PresScriptWeb = PresScriptWeb;\n})();\n");
  minify(playJs);
  buildThree(ranger, viewDir);
  buildScriptSel(ranger, viewDir);
  const cerxes = ensureCerxes();
  copy(cerxes.runtime, "cerxes-runtime.js");
  copy(cerxes.wasi, "cerxes-wasi.js");
  if (cerxes.wasm) copy(cerxes.wasm, "cerxes.wasm");
  else if (process.env.SLIQTLY_NEED_CERXES) throw new Error(`cerxes not built: ${cerxes.why} (SLIQTLY_NEED_CERXES is set)`);
  else log(`cerxes not built: ${cerxes.why}; programs on slides show their plates`);

  // the Warden page, served at /apps/warden/
  if (!fs.existsSync(path.join(wardenDir, "index.html"))) buildSpaces({ warden: true });
  for (const f of files(wardenDir)) copy(path.join(wardenDir, f), "apps/warden/" + f);

  // every module's relative imports are among the files copied
  const shipped = new Set(files(viewDir));
  for (const f of [...shipped].filter((f) => f.endsWith(".js"))) {
    const text = fs.readFileSync(path.join(viewDir, f), "utf8");
    for (const m of text.matchAll(/(?:\bfrom\s*|\bimport\s*\(?\s*)["'](\.{1,2}\/[^"'?#]+)["']/g)) {
      const rel = path.posix.normalize(path.posix.join(path.posix.dirname(f), m[1]));
      if (!shipped.has(rel)) throw new Error(`build-view: ${f} imports ${m[1]}, which the viewer does not ship`);
    }
  }

  markParts(viewDir, ["pres_3d.js", "pres_play.js"]);
  // the build's stamp on the page's script and every module import, as in
  // build.mjs: a reload never mixes an old module with a new one
  const h = crypto.createHash("sha1");
  for (const f of files(viewDir).filter((f) => !f.startsWith("fonts/"))) h.update(f).update(fs.readFileSync(path.join(viewDir, f)));
  const stamp = h.digest("hex").slice(0, 10);
  const fonts = facesStamp(viewDir);
  for (const page of ["index.html", "main/admin.html", "play.html"]) {
    const html = path.join(viewDir, page);
    fs.writeFileSync(html, fs.readFileSync(html, "utf8").split("__BUILD__").join(stamp).split("__FONTS__").join(fonts));
  }
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
