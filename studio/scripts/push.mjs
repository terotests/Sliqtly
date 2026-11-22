#!/usr/bin/env node
// SPDX-License-Identifier: AGPL-3.0-or-later
// npm run studio:push -- <upload address> <game folder> [--all] [--dry]
//
// Sends a game's changed files from a folder on disk to Sliqtly Games. The
// upload address is the short-lived secret one Sliqtly Games gives
// (upload_url: https://sliqtly.com/games/u/<token>, 30 minutes); each file is
// PUT to <address>/<path>, one after another. SLIQTLY_UPLOAD_URL works too.
//
// Only files that differ from what Sliqtly Games has are sent. What it has is
// .studio/manifest.json (path -> sha1), written by pull.mjs and kept up to
// date here; without one, or with --all, the game is first fetched by its
// preview link (.studio/source.json) and compared byte for byte. With no
// link either, every file is sent.
//
// A publish pipeline, in the game's studio.json:
//
//   "publish": { "before": "node tools/tiles.mjs", "from": "dist", "skip": ["raw/", "*.psd"] }
//
// before  a shell command run in the game folder first (preprocessing:
//         tiles, compressed pictures, a bundle); a failure stops the push
// from    the folder whose files are sent (default: the game folder)
// skip    paths left out: a prefix ending in "/", "*.ext", or an exact path
//
// studio.json itself, dot files (.studio, .git) and node_modules are never
// sent. Files only on Sliqtly Games are listed, not deleted.
import { existsSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { execSync } from "node:child_process";
import { expand, fetchGame, walk, readJson, writeJson, sha1, manifestOf, manifestPath, sourcePath } from "./pack.mjs";

const MAX = 8 * 1024 * 1024; // write_files' limit for one file

const args = process.argv.slice(2);
const flags = new Set(args.filter((a) => a.startsWith("--")));
let upload = args.find((a) => /^https?:\/\/.*\/games\/u\//.test(a)) || process.env.SLIQTLY_UPLOAD_URL || "";
const folderArg = args.find((a) => !/^https?:\/\//.test(a) && !a.startsWith("--"));
if (!folderArg) {
  console.error("usage: npm run studio:push -- <upload address> <game folder> [--all] [--dry]");
  process.exit(2);
}
const dry = flags.has("--dry");
if (!upload && !dry) {
  console.error("[push] no upload address: ask Sliqtly Games for one (upload_url, https://sliqtly.com/games/u/<token>) and give it first");
  process.exit(2);
}
upload = upload.replace(/\/+$/, "");
const folder = expand(folderArg);
if (!existsSync(join(folder, "game.json"))) {
  console.error(`[push] ${folder} has no game.json`);
  process.exit(2);
}

// the publish pipeline
const studio = readJson(join(folder, "studio.json"), {});
const pub = studio.publish || {};
if (pub.before) {
  console.log(`[push] before: ${pub.before}`);
  try {
    execSync(pub.before, { cwd: folder, stdio: "inherit", shell: "/bin/sh" });
  } catch (e) {
    console.error(`[push] "${pub.before}" failed (exit ${e.status}); nothing sent`);
    process.exit(1);
  }
}
const base = pub.from ? join(folder, pub.from) : folder;
const skips = Array.isArray(pub.skip) ? pub.skip : [];
const skipped = (rel) => rel === "studio.json" || skips.some((s) =>
  s.endsWith("/") ? rel.startsWith(s) : s.startsWith("*.") ? rel.endsWith(s.slice(1)) : rel === s);

// what Sliqtly Games has
let manifest = readJson(manifestPath(folder), null);
const source = readJson(sourcePath(folder), {});
if ((!manifest || flags.has("--all")) && source.preview) {
  console.log("[push] reading what Sliqtly Games has now");
  const { files } = await fetchGame(source.preview, (s) => console.log("[push] " + s));
  manifest = manifestOf(files);
  if (!dry) writeJson(manifestPath(folder), manifest);
}
if (!manifest) {
  console.log("[push] no manifest and no preview link: sending every file");
  manifest = {};
}

const local = walk(base).filter((r) => !skipped(r));
const changed = [], tooBig = [];
let same = 0;
for (const rel of local) {
  const path = join(base, ...rel.split("/"));
  if (statSync(path).size > MAX) { tooBig.push(rel); continue; }
  const h = sha1(readFileSync(path));
  if (manifest[rel] === h) same++;
  else changed.push({ rel, path, h });
}
const onlyRemote = Object.keys(manifest).filter((r) => !local.includes(r) && !skipped(r));

console.log(`[push] ${changed.length} to send, ${same} unchanged` + (dry ? " (--dry: nothing sent)" : ""));
let sent = 0, failed = 0;
for (const f of changed) {
  if (dry) { console.log(`[push]   ${f.rel}`); continue; }
  const url = upload + "/" + f.rel.split("/").map(encodeURIComponent).join("/");
  let status = 0, text = "";
  try {
    const r = await fetch(url, { method: "PUT", body: readFileSync(f.path) });
    status = r.status;
    text = await r.text();
  } catch (e) {
    text = String(e.message || e);
  }
  if (status === 200) {
    sent++;
    manifest[f.rel] = f.h;
    console.log(`[push] 200 ${f.rel}`);
  } else {
    failed++;
    let msg = text;
    try { msg = JSON.parse(text).error || text; } catch {}
    console.log(`[push] ${status || "error"} ${f.rel}: ${msg.slice(0, 200)}`);
    if (/ended|No such upload/.test(msg)) {
      console.log("[push] the upload address has ended: ask for a new one and push again (sent files are remembered)");
      break;
    }
  }
}
if (!dry) writeJson(manifestPath(folder), manifest);
if (tooBig.length) console.log(`[push] over 8 MB, not sent: ${tooBig.join(", ")}`);
if (onlyRemote.length) console.log(`[push] only on Sliqtly Games (not deleted): ${onlyRemote.slice(0, 20).join(", ")}${onlyRemote.length > 20 ? ` … ${onlyRemote.length - 20} more` : ""}`);
console.log(`[push] done: ${sent} sent, ${failed} failed, ${same} unchanged`);
process.exit(failed ? 1 : 0);
