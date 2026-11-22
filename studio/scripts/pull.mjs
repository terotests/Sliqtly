#!/usr/bin/env node
// SPDX-License-Identifier: AGPL-3.0-or-later
// npm run studio:pull -- <preview link> <game folder>
// npm run studio:pull -- <game folder>          (again, with the saved link)
//
// Brings a game from Sliqtly Games to a folder on disk: the newest files of
// the game behind its preview link (https://sliqtly.com/games/p/<token>, the
// link Games gives for trying a game). The link is saved in
// <folder>/.studio/source.json, so later pulls need only the folder.
//
// Only files that differ are written, so the Studio reloads just those (hot
// reload keeps the game running). A local file that was changed after the
// last pull is first copied to .studio/backup/<time>/. Files that are only
// on disk stay; they are listed at the end.
import { existsSync, mkdirSync, readFileSync, statSync, writeFileSync, copyFileSync, readdirSync } from "node:fs";
import { dirname, join, resolve, sep } from "node:path";
import { homedir } from "node:os";

const args = process.argv.slice(2);
const expand = (p) => resolve(p.replace(/^~(?=$|\/)/, homedir()));
let link = args.find((a) => /^https?:\/\//.test(a)) || "";
const folderArg = args.find((a) => !/^https?:\/\//.test(a) && !a.startsWith("--"));
if (!folderArg) {
  console.error("usage: npm run studio:pull -- [<preview link>] <game folder>");
  process.exit(2);
}
const folder = expand(folderArg);
const meta = join(folder, ".studio", "source.json");
let saved = {};
if (existsSync(meta)) {
  try { saved = JSON.parse(readFileSync(meta, "utf8")); } catch { saved = {}; }
}
if (!link) link = saved.preview || "";
if (!link) {
  console.error(`[pull] no link: give the game's preview link once (Sliqtly Games: get_game preview_url), e.g.\n  npm run studio:pull -- https://sliqtly.com/games/p/<token> ${folderArg}`);
  process.exit(2);
}

// The preview page sends the browser on to the engine page, whose address
// names the pack (?pack=…/game.rgpk) and the game (?game=<id>).
const page = await fetch(link, { redirect: "follow" });
if (!page.ok) throw new Error(`${link}: ${page.status}`);
let engineUrl = new URL(page.url);
let packPath = engineUrl.searchParams.get("pack");
let gameId = engineUrl.searchParams.get("game");
if (!packPath) {
  const html = await page.text();
  const m = html.match(/[?&]pack=([^"'&\s]+)/);
  if (m) packPath = decodeURIComponent(m[1]);
  const g = html.match(/[?&]game=([^"'&\s]+)/);
  if (g) gameId = decodeURIComponent(g[1]);
}
if (!packPath) throw new Error(`no pack address on ${page.url}`);
const packUrl = new URL(packPath, page.url).toString();

// The pack comes in parts (?part=k, at most ~12 MB each); the first answer
// says how many in X-Pack-Parts. Each part is a pack of its own:
// "RGPK1\n<count>\n" then per file "<path>\n<length>\n<bytes>".
function parse(buf, into) {
  let at = 0;
  const line = () => {
    const nl = buf.indexOf(10, at);
    if (nl < 0) throw new Error("pack cut short");
    const s = buf.subarray(at, nl).toString("utf8");
    at = nl + 1;
    return s;
  };
  if (line() !== "RGPK1") throw new Error("not a pack");
  const n = +line();
  for (let i = 0; i < n; i++) {
    const path = line();
    const len = +line();
    if (at + len > buf.length) throw new Error(`pack cut short at ${path}`);
    into.set(path, buf.subarray(at, at + len));
    at += len;
  }
}
const partUrl = (k) => packUrl + (packUrl.includes("?") ? "&" : "?") + "part=" + k;
const files = new Map();
const first = await fetch(partUrl(0));
if (!first.ok) throw new Error(`${partUrl(0)}: ${first.status}`);
const parts = +(first.headers.get("X-Pack-Parts") || 1);
parse(Buffer.from(await first.arrayBuffer()), files);
process.stdout.write(`[pull] part 1/${parts}\n`);
for (let k = 1; k < parts; k++) {
  const r = await fetch(partUrl(k));
  if (!r.ok) throw new Error(`${partUrl(k)}: ${r.status}`);
  parse(Buffer.from(await r.arrayBuffer()), files);
  process.stdout.write(`[pull] part ${k + 1}/${parts}\n`);
}

// The pack holds the game under "<id>/" (and the menu's own files beside it).
if (!gameId) {
  const tops = new Set([...files.keys()].map((p) => p.split("/")[0]).filter((t) => t !== "launcher"));
  if (tops.size === 1) gameId = [...tops][0];
}
const prefix = gameId + "/";
const pulledAt = Date.now();
const stamp = new Date(pulledAt).toISOString().replace(/[:.]/g, "-");
let written = 0, same = 0, backedUp = 0;
const remote = new Set();
for (const [path, data] of files) {
  if (!path.startsWith(prefix)) continue;
  const rel = path.slice(prefix.length);
  if (!rel || rel.split("/").some((s) => s === ".." || s === "") || rel.startsWith(".studio")) continue;
  remote.add(rel);
  const dest = join(folder, ...rel.split("/"));
  if (existsSync(dest)) {
    const old = readFileSync(dest);
    if (old.equals(data)) { same++; continue; }
    // changed here after the last pull: keep a copy before overwriting
    if (saved.pulledAt && statSync(dest).mtimeMs > saved.pulledAt + 2000) {
      const bak = join(folder, ".studio", "backup", stamp, ...rel.split("/"));
      mkdirSync(dirname(bak), { recursive: true });
      copyFileSync(dest, bak);
      backedUp++;
    }
  }
  mkdirSync(dirname(dest), { recursive: true });
  writeFileSync(dest, data);
  written++;
}
if (remote.size === 0) throw new Error(`no files of ${gameId} in the pack`);

// files only on disk (not removed: they may be local work)
const localOnly = [];
const walk = (dir, rel) => {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    if (e.name.startsWith(".") || e.name === "node_modules") continue;
    const r = rel ? rel + "/" + e.name : e.name;
    if (e.isDirectory()) walk(join(dir, e.name), r);
    else if (!remote.has(r)) localOnly.push(r);
  }
};
walk(folder, "");

mkdirSync(dirname(meta), { recursive: true });
writeFileSync(meta, JSON.stringify({ preview: link, game: gameId, pulledAt, files: remote.size }, null, 2) + "\n");
console.log(`[pull] ${gameId}: ${remote.size} files, ${written} updated, ${same} unchanged` +
  (backedUp ? `, ${backedUp} local edits kept in .studio/backup/${stamp}` : "") + ` -> ${folder}`);
if (localOnly.length) console.log(`[pull] only on disk (kept): ${localOnly.slice(0, 20).join(", ")}${localOnly.length > 20 ? ` … ${localOnly.length - 20} more` : ""}`);
