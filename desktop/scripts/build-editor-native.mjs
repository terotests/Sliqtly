#!/usr/bin/env node
// SPDX-License-Identifier: MIT
//
// Sliqtly, native: the browser editor's app (src/PresApp.rgr + EVGUI) to C++
// with the same Ranger checkout the web build uses (.deps, scripts/lib.mjs),
// then one SDL2 + OpenGL binary with native/editor_host.cpp and EVG's native
// painter.
//
//   node scripts/build-editor-native.mjs [--run [args…]]
//
// EVG_NATIVE=<evg>/storm/native picks the painter; without it, the EVG
// package `rgrc install` fetched (scripts/ranger.mjs evgDir()).
//
// Writes native/build-editor/sliqtly-native and native/build-editor/editor-res/
// (the stylesheets, themes, fonts and the welcome deck the host reads); on
// macOS also native/build-editor/Sliqtly.app with editor-res in its Resources.
// SDL2_FRAMEWORK and UNIVERSAL=1 as in scripts/native-link.mjs.

import fs from "node:fs";
import path from "node:path";
import { execSync, execFileSync, spawnSync } from "node:child_process";
import { MAC, cxx, sdl2, glFlags, macFlags, macApp } from "./native-link.mjs";
import { ensureRanger, LINK, root } from "../../scripts/lib.mjs";
import { ROOT, evgDir } from "./ranger.mjs";
import { themeCss, themeNames } from "../../scripts/themes.mjs";

const NATIVE = path.join(ROOT, "native");
const BUILD = path.join(NATIVE, "build-editor");
const RES = path.join(BUILD, "editor-res");
const argv = process.argv.slice(2);
const runAt = argv.indexOf("--run");
const APP_NAME = "Sliqtly";
const BUNDLE_ID = "com.sliqtly.app";
const VERSION = JSON.parse(fs.readFileSync(path.join(ROOT, "package.json"), "utf8")).version;
const die = (msg) => { console.error(msg); process.exit(1); };
fs.mkdirSync(BUILD, { recursive: true });

// --- 1. Ranger → C++ -------------------------------------------------------------
const ranger = ensureRanger();
const r = spawnSync(process.execPath, ["--max-old-space-size=12000", "dist/rgrc.js", "-l=cpp", `./${LINK}/PresApp.rgr`, `-d=${BUILD}`, "-o=PresApp.cpp"], {
  cwd: ranger,
  encoding: "utf8",
  maxBuffer: 64 * 1024 * 1024,
  env: { ...process.env, RANGER_LIB: "./compiler/Lang.rgr:./lib/stdops.rgr" },
});
const out = `${r.stdout || ""}${r.stderr || ""}`;
if (r.status !== 0 || /Compilation FAILED/.test(out)) die(out.split("\n").filter((l) => /FAIL|error/i.test(l)).slice(0, 30).join("\n"));
console.log("  1/3 src/PresApp.rgr -> native/build-editor/PresApp.cpp");

// --- 2. What the host reads at start ---------------------------------------------
fs.mkdirSync(path.join(RES, "themes"), { recursive: true });
fs.mkdirSync(path.join(RES, "fonts"), { recursive: true });
const copy = (from, to) => fs.copyFileSync(from, to);
copy(path.join(root, ".deps", "EVGUI", "theme", "base.css"), path.join(RES, "base.css"));
for (const f of ["chart-editor.css", "hint.css", "panels.css", "toolbar.css", "pres.css"]) copy(path.join(root, "web", f), path.join(RES, f));
for (const t of themeNames()) fs.writeFileSync(path.join(RES, "themes", `${t}.css`), themeCss(t));
for (const f of fs.readdirSync(path.join(NATIVE, "fonts")).filter((f) => f.endsWith(".ttf"))) copy(path.join(NATIVE, "fonts", f), path.join(RES, "fonts", f));
copy(path.join(root, "samples", "welcome.en.md"), path.join(RES, "welcome.en.md"));
console.log("  2/3 stylesheets, themes, fonts -> native/build-editor/editor-res/");

// --- 3. The binary (and on macOS the .app) ------------------------------------
const { flags: sdl, framework } = sdl2();
const EVG_NATIVE = process.env.EVG_NATIVE || path.join(evgDir(), "native");
if (!fs.existsSync(path.join(EVG_NATIVE, "gl", "EvgGlPainter.h"))) die(`no EVG native painter at ${EVG_NATIVE}`);
const sources = [path.join(NATIVE, "editor_host.cpp"), path.join(NATIVE, "http_curl.cpp"), ...fs.readdirSync(path.join(EVG_NATIVE, "gl")).filter((f) => f.endsWith(".cpp")).map((f) => path.join(EVG_NATIVE, "gl", f))]
  .map((f) => JSON.stringify(f)).join(" ");
const bin = path.join(BUILD, "sliqtly-native");
// -w: the generated PresApp.cpp is large and not written for warnings
execSync(`${cxx()} -std=c++17 ${macFlags(framework)}${process.env.CXX_OPT || "-O1"} -w -I${JSON.stringify(NATIVE)} -I${JSON.stringify(BUILD)} -I${JSON.stringify(EVG_NATIVE)} ${sources} -o ${JSON.stringify(bin)} ${sdl} ${glFlags()} -lcurl -lpthread`, { stdio: "inherit" });
if (MAC) {
  const { icon } = macApp({ nativeDir: NATIVE, buildDir: BUILD, name: APP_NAME, bundleId: BUNDLE_ID, version: VERSION, bin, exe: "sliqtly", resources: [RES], framework });
  console.log(`  3/3 native/build-editor/sliqtly-native, native/build-editor/${APP_NAME}.app${icon ? "" : " (no icon)"}${framework ? ", SDL2.framework inside" : ""}`);
} else {
  console.log("  3/3 native/build-editor/sliqtly-native");
}

if (runAt >= 0) execFileSync(bin, argv.slice(runAt + 1), { stdio: "inherit" });
