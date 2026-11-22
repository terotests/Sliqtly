// SPDX-License-Identifier: AGPL-3.0-or-later
// What pull.mjs and push.mjs share: a game's files from Sliqtly Games by its
// preview link, the files of a folder on disk, and the manifest that says
// what Sliqtly Games has (<folder>/.studio/manifest.json, path -> sha1).
import { createHash } from "node:crypto";
import { existsSync, readFileSync, readdirSync, writeFileSync, mkdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { homedir } from "node:os";

export const expand = (p) => resolve(p.replace(/^~(?=$|\/)/, homedir()));
export const sha1 = (buf) => createHash("sha1").update(buf).digest("hex");

export function readJson(path, fallback) {
  if (!existsSync(path)) return fallback;
  try { return JSON.parse(readFileSync(path, "utf8")); } catch { return fallback; }
}

export function writeJson(path, value) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, JSON.stringify(value, null, 2) + "\n");
}

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

async function get(url, tries = 3) {
  for (let i = 1; ; i++) {
    try {
      const r = await fetch(url);
      if (!r.ok) throw new Error(`${url}: ${r.status}`);
      return r;
    } catch (e) {
      if (i >= tries) throw e;
    }
  }
}

// The game's files as Sliqtly Games has them: { gameId, files: Map rel -> Buffer }.
export async function fetchGame(link, log = (s) => process.stdout.write(s + "\n")) {
  // The preview page sends the browser on to the engine page, whose address
  // names the pack (?pack=…/game.rgpk) and the game (?game=<id>).
  const page = await fetch(link, { redirect: "follow" });
  if (!page.ok) throw new Error(`${link}: ${page.status}`);
  const engineUrl = new URL(page.url);
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
  const partUrl = (k) => packUrl + (packUrl.includes("?") ? "&" : "?") + "part=" + k;
  const all = new Map();
  const first = await get(partUrl(0));
  const parts = +(first.headers.get("X-Pack-Parts") || 1);
  parse(Buffer.from(await first.arrayBuffer()), all);
  log(`part 1/${parts}`);
  for (let k = 1; k < parts; k++) {
    const r = await get(partUrl(k));
    parse(Buffer.from(await r.arrayBuffer()), all);
    log(`part ${k + 1}/${parts}`);
  }
  // The pack holds the game under "<id>/" (and the menu's own files beside it).
  if (!gameId) {
    const tops = new Set([...all.keys()].map((p) => p.split("/")[0]).filter((t) => t !== "launcher"));
    if (tops.size === 1) gameId = [...tops][0];
  }
  const prefix = gameId + "/";
  const files = new Map();
  for (const [path, data] of all) {
    if (!path.startsWith(prefix)) continue;
    const rel = path.slice(prefix.length);
    if (!rel || rel.split("/").some((s) => s === ".." || s === "") || rel.startsWith(".studio")) continue;
    files.set(rel, data);
  }
  if (files.size === 0) throw new Error(`no files of ${gameId} in the pack`);
  return { gameId, files };
}

// Files of a folder, relative paths with "/", leaving out dot files and
// folders (.studio, .git) and node_modules.
export function walk(folder) {
  const out = [];
  const go = (dir, rel) => {
    if (!existsSync(dir)) return;
    for (const e of readdirSync(dir, { withFileTypes: true })) {
      if (e.name.startsWith(".") || e.name === "node_modules") continue;
      const r = rel ? rel + "/" + e.name : e.name;
      if (e.isDirectory()) go(join(dir, e.name), r);
      else if (e.isFile()) out.push(r);
    }
  };
  go(folder, "");
  return out.sort();
}

export const manifestPath = (folder) => join(folder, ".studio", "manifest.json");
export const sourcePath = (folder) => join(folder, ".studio", "source.json");

export function manifestOf(files) {
  const m = {};
  for (const [rel, data] of files) m[rel] = sha1(data);
  return m;
}
