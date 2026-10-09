#!/usr/bin/env node
/**
 * npm run build: compile PresApp.rgr and assemble web/dist.
 *
 * The output is static — any file server can serve it — and `npm start`
 * serves it together with the local API the later stages add (TTS, agent).
 */
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { ensureRanger, ensureRangerDiff, depsUsed, compile, root, webDir, distDir, log } from "./lib.mjs";
import { createRequire } from "node:module";
import { formatCss } from "./format-css.mjs";
import { buildPlayer } from "./player.mjs";
import { stampImports, unstampedImports } from "./stamp.mjs";

// esbuild (npm install) shrinks the two compiled bundles to about 60 %
// (pres_app.js 8.2 MB → 4.9 MB, 1.7 MB → 1.4 MB gzipped): every visit
// downloads them. Names are kept, which the compiled classes may read.
// Without it the bundles go out as compiled.
function minify(file) {
  let esbuild;
  try { esbuild = createRequire(import.meta.url)("esbuild"); } catch (_) { return; }
  const out = esbuild.transformSync(fs.readFileSync(file, "utf8"), { minify: true, keepNames: true, legalComments: "none" });
  fs.writeFileSync(file, out.code);
}

// The faces the pages draw with, from Ranger's checkout into `<dir>/fonts`:
// Open Sans and Noto Sans, the diagram looks' faces (RangerFlow FlowLook,
// fetched when a deck uses one) and, for the PDF writer, Noto Emoji.
export function copyFaces(ranger, dir, { emoji = false } = {}) {
  const put = (from, name) => {
    fs.mkdirSync(path.join(dir, "fonts"), { recursive: true });
    fs.copyFileSync(path.join(ranger, "gallery/pdf_writer/assets/fonts", from), path.join(dir, "fonts", name));
  };
  for (const face of ["OpenSans-Regular", "OpenSans-Bold", "OpenSans-Italic", "OpenSans-BoldItalic"]) put(`Open_Sans/${face}.ttf`, `${face}.ttf`);
  for (const face of ["NotoSans-Regular", "NotoSans-Bold"]) put(`Noto_Sans/${face}.ttf`, `${face}.ttf`);
  if (emoji) put("Noto_Emoji/NotoEmoji-Regular.ttf", "NotoEmoji-Regular.ttf");
  for (const [d, face] of [["Gloria_Hallelujah", "GloriaHallelujah"], ["Fjalla_One", "FjallaOne-Regular"], ["Josefin_Sans", "JosefinSans-Bold"], ["Droid_Serif", "DroidSerif-BoldItalic"]]) {
    put(`${d}/${face}.ttf`, `${face}.ttf`);
  }
  // the faces a deck's CSS can name besides Open Sans and Noto Sans
  // (src/PresFonts.rgr), fetched when a deck uses them: Droid Serif from
  // Ranger, Lato from this repository (fonts/Lato, OFL)
  for (const face of ["DroidSerif", "DroidSerif-Bold", "DroidSerif-Italic"]) put(`Droid_Serif/${face}.ttf`, `${face}.ttf`);
  for (const face of DECK_FACES) {
    fs.mkdirSync(path.join(dir, "fonts"), { recursive: true });
    fs.copyFileSync(path.join(root, "fonts", "Lato", `${face}.ttf`), path.join(dir, "fonts", `${face}.ttf`));
  }
}

/** Lato, kept in this repository (fonts/Lato): the faces copyFaces adds. */
export const DECK_FACES = ["Lato-Regular", "Lato-Bold", "Lato-Italic", "Lato-BoldItalic"];

// The fonts change far more seldom than the code: their own hash, so a new
// build is not 1.5 MB of the same faces again for every visitor.
export function facesStamp(dir) {
  const fh = crypto.createHash("sha1");
  for (const f of fs.readdirSync(path.join(dir, "fonts")).sort()) fh.update(f).update(fs.readFileSync(path.join(dir, "fonts", f)));
  return fh.digest("hex").slice(0, 10);
}

// every file in web/dist, relative and sorted
function distFiles() {
  return fs.readdirSync(distDir, { recursive: true })
    .map((f) => f.split(path.sep).join("/"))
    .filter((f) => fs.statSync(path.join(distDir, f)).isFile())
    .sort();
}

export function build({ ranger } = {}) {
  ranger = ranger || ensureRanger();
  fs.mkdirSync(distDir, { recursive: true });
  const appJs = path.join(distDir, "pres_app.js");
  compile(ranger, "PresApp.rgr", appJs);

  // Loaded without require(), beside ES modules: publish the app and the
  // interface's words (PresI18n, filled before the app is made).
  const src = fs.readFileSync(appJs, "utf8");
  fs.writeFileSync(appJs, "// scoped: the page loads this beside other scripts, so it publishes two names.\n"
    + "(function () {\n" + src + "\n;globalThis.PresApp = PresApp;\nglobalThis.PresI18n = PresI18n;\n})();\n");

  // A dropped .xlsx is read by its own bundle (the workbook reader is large,
  // and most decks never need it): loaded the first time one arrives.
  const dataJs = path.join(distDir, "pres_data.js");
  compile(ranger, "PresData.rgr", dataJs);
  fs.writeFileSync(dataJs, "// loaded on demand: a .xlsx as CSV, one per sheet.\n"
    + "(function () {\n" + fs.readFileSync(dataJs, "utf8") + "\n;globalThis.PresData = PresData;\n})();\n");
  // The vectorizer (lib/evg's EvgBitmapTracer) likewise, loaded by its
  // worker (web/trace-worker.js) the first time a picture is vectorized.
  const traceJs = path.join(distDir, "pres_trace.js");
  compile(ranger, "PresTrace.rgr", traceJs);
  fs.writeFileSync(traceJs, "// loaded on demand by trace-worker.js: a picture traced into an SVG.\n"
    + "(function () {\n" + fs.readFileSync(traceJs, "utf8") + "\n;globalThis.PresTrace = PresTrace;\n})();\n");
  minify(appJs);
  minify(dataJs);
  minify(traceJs);

  const copy = (from, to) => {
    fs.mkdirSync(path.dirname(to), { recursive: true });
    fs.copyFileSync(from, to);
  };
  for (const f of ["index.html", "connect.html", "oauth.html", "main.js", "vfs.js", "i18n.js", "sheets-live.js", "image-adjust.js", "trace-worker.js", "picture.js", "versions.js", "versions-ui.js", "brand.js", "decklist.js", "rooms.js", "roomchat.js", "cloudchat.js", "fileclip.js", "slideclip.js", "decktabs.js", "account.js", "version-view.js", "player-file.js", "collab.js", "sharefiles.js", "eventline.js", "recorder.js", "meet.js", "press.js", "stagelink.js", "viewlink.js", "trace-source.js", "book.js", "bookgl.js", "bookturn.js", "themepics.js", "sliqtly.js", "storedtype.js", "pres.css", "chart-editor.css", "toolbar.css", "hint.css", "panels.css"]) copy(path.join(webDir, f), path.join(distDir, f));
  // versions and deltas (web/versions.js): RangerDiff's built module
  copy(path.join(ensureRangerDiff(), "dist", "rangerdiff.mjs"), path.join(distDir, "rangerdiff.mjs"));
  // the interface in other languages (web/i18n.js)
  for (const f of fs.readdirSync(path.join(webDir, "i18n")).filter((f) => f.endsWith(".json"))) copy(path.join(webDir, "i18n", f), path.join(distDir, "i18n", f));
  // the controls' own theme (EVGUI), for the chart editor
  copy(path.join(ranger, "gallery/evgui/theme/base.css"), path.join(distDir, "ui.css"));
  // the editor's optional skin (File → Settings → Look): EVGUI's for the
  // controls, ours for the rest of the chrome
  copy(path.join(ranger, "gallery/evgui/theme/skins/retro.css"), path.join(distDir, "skins/ui-retro.css"));
  copy(path.join(webDir, "skins/retro.css"), path.join(distDir, "skins/retro.css"));
  // the dark look's hand-set colours (the rest is derived, see main.js)
  copy(path.join(ranger, "gallery/evgui/theme/skins/dark.css"), path.join(distDir, "skins/ui-dark.css"));
  copy(path.join(webDir, "skins/dark.css"), path.join(distDir, "skins/dark.css"));
  copy(path.join(root, "brand/sliqtly-icon.svg"), path.join(distDir, "favicon.svg"));
  copy(path.join(ranger, "lib/evg/gl/evg-webgl.js"), path.join(distDir, "gl/evg-webgl.js"));
  copy(path.join(ranger, "lib/evg/gl/evg-a11y.js"), path.join(distDir, "gl/evg-a11y.js"));
  copyFaces(ranger, distDir, { emoji: true });
  for (const f of fs.readdirSync(path.join(root, "themes"))) copy(path.join(root, "themes", f), path.join(distDir, "themes", f));
  // Live spreadsheets (```sheet, .xlsx in Files) are EVGSheets. A built copy
  // goes beside the page when there is one — $EVGSHEETS_DIST, or
  // .deps/EVGSheets/dist — and otherwise the page loads it from its own
  // site (presentation.config.json "evgsheets.base").
  const config = JSON.parse(fs.readFileSync(path.join(root, "presentation.config.json"), "utf8"));
  let sheetsBase = (config.evgsheets && config.evgsheets.base) || "https://terotests.github.io/EVGSheets/";
  const sheetsDist = process.env.EVGSHEETS_DIST || path.join(root, ".deps", "EVGSheets", "dist");
  if (fs.existsSync(path.join(sheetsDist, "evgsheets.mjs"))) {
    fs.cpSync(sheetsDist, path.join(distDir, "sheets"), { recursive: true });
    sheetsBase = "./sheets/";
    log(`sheets ${sheetsDist} → web/dist/sheets`);
  } else {
    log(`sheets loaded from ${sheetsBase}`);
  }
  // GitHub Pages serves the directory as it is; no Jekyll pass over it.
  fs.writeFileSync(path.join(distDir, ".nojekyll"), "");
  for (const t of ["editorial"]) {
    // one declaration per line, as the editor's CSS tab shows our own themes
    fs.mkdirSync(path.join(distDir, "themes"), { recursive: true });
    fs.writeFileSync(path.join(distDir, `themes/${t}.css`),
      formatCss(fs.readFileSync(path.join(ranger, `gallery/markdown/fixtures/themes/${t}.css`), "utf8")));
  }
  for (const f of fs.readdirSync(path.join(root, "samples")).filter((f) => f.endsWith(".md"))) copy(path.join(root, "samples", f), path.join(distDir, "samples", f));
  // a sample's own pictures, samples/<key>/… (SAMPLES in web/main.js lists them)
  for (const d of fs.readdirSync(path.join(root, "samples"), { withFileTypes: true }).filter((d) => d.isDirectory() && d.name !== "data")) {
    for (const f of fs.readdirSync(path.join(root, "samples", d.name), { recursive: true })) {
      const from = path.join(root, "samples", d.name, f);
      if (fs.statSync(from).isFile()) copy(from, path.join(distDir, "samples", d.name, f));
    }
  }
  // the files the sample decks' charts read ("url": "data/…"), served beside
  // the page where the chart looks first (Vega's example datasets, samples/data)
  for (const f of fs.readdirSync(path.join(root, "samples", "data")).filter((f) => !f.endsWith(".md"))) copy(path.join(root, "samples", "data", f), path.join(distDir, "data", f));
  copy(path.join(ranger, "gallery/markdown/fixtures/deck.md"), path.join(distDir, "samples/deck.md"));

  // the commit each dependency was built from: the config names branches,
  // so this is what says which engine code a deployed page holds
  const deps = depsUsed();
  fs.writeFileSync(path.join(distDir, "deps.json"), JSON.stringify(deps, null, 2) + "\n");
  for (const [name, d] of Object.entries(deps)) log(`deps   ${name} ${d.ref || "HEAD"} ${d.commit.slice(0, 12)}${d.changed ? " (with local changes)" : ""}`);

  // Every URL the page loads carries the hash of the build, so a reload
  // never mixes an old script with a new one.
  const h = crypto.createHash("sha1");
  // player.html is made from the rest after this (scripts/player.mjs)
  for (const f of distFiles().filter((f) => !f.startsWith("fonts/") && f !== "player.html")) h.update(f).update(fs.readFileSync(path.join(distDir, f)));
  const stamp = h.digest("hex").slice(0, 10);
  const fonts = facesStamp(distDir);
  const html = path.join(distDir, "index.html");
  fs.writeFileSync(html, fs.readFileSync(html, "utf8").split("__BUILD__").join(stamp).split("__FONTS__").join(fonts));
  const main = path.join(distDir, "main.js");
  fs.writeFileSync(main, fs.readFileSync(main, "utf8")
    .split("__SHEETS_BASE__").join(sheetsBase)
    .split("__FONTS__").join(fonts)
    .split("__BUILT__").join(new Date().toISOString().slice(0, 10))
    .split("__BUILD__").join(stamp));
  // the player page bundles the modules as they are now, before their
  // imports carry the stamp
  const player = buildPlayer(distDir);
  if (player) log(`build  web/dist/player.html (${(player / 1048576).toFixed(1)} MB)`);
  // every relative import of every module we ship carries the stamp
  // (scripts/stamp.mjs); the two compiled bundles are classic scripts, and
  // EVGSheets' own build (sheets/) is left as it came
  const classic = new Set(["pres_app.js", "pres_data.js", "pres_trace.js", "trace-worker.js"]);
  const modules = distFiles().filter((f) => /\.m?js$/.test(f) && !classic.has(f) && !f.startsWith("sheets/"));
  for (const f of modules) {
    const file = path.join(distDir, f);
    fs.writeFileSync(file, stampImports(fs.readFileSync(file, "utf8"), stamp));
  }
  const left = modules.flatMap((f) => unstampedImports(fs.readFileSync(path.join(distDir, f), "utf8")).map((u) => `${f}: ${u}`));
  if (left.length) throw new Error("imports without the build stamp:\n  " + left.join("\n  "));
  log(`build  web/dist (${stamp})`);
  return stamp;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  try {
    build();
  } catch (e) {
    log(e.message);
    process.exit(1);
  }
}
