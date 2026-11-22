#!/usr/bin/env node
// SPDX-License-Identifier: AGPL-3.0-or-later
// npm run studio:pull -- <preview link> <game folder>
// npm run studio:pull -- <game folder>          (again, with the saved link)
//
// Brings a game from Sliqtly Games to a folder on disk: the newest files of
// the game behind its preview link (https://sliqtly.com/games/p/<token>, the
// link Games gives for trying a game). The link is saved in
// <folder>/.studio/source.json, so later pulls need only the folder; what
// Sliqtly Games has is noted in .studio/manifest.json, which push.mjs reads.
//
// Only files that differ are written, so the Studio reloads just those (hot
// reload keeps the game running). A local file that was changed after the
// last pull is first copied to .studio/backup/<time>/. Files that are only
// on disk stay; they are listed at the end.
import { existsSync, mkdirSync, readFileSync, statSync, writeFileSync, copyFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { expand, fetchGame, walk, readJson, writeJson, manifestOf, manifestPath, sourcePath } from "./pack.mjs";

const args = process.argv.slice(2);
let link = args.find((a) => /^https?:\/\//.test(a)) || "";
const folderArg = args.find((a) => !/^https?:\/\//.test(a) && !a.startsWith("--"));
if (!folderArg) {
  console.error("usage: npm run studio:pull -- [<preview link>] <game folder>");
  process.exit(2);
}
const folder = expand(folderArg);
const meta = sourcePath(folder);
const saved = readJson(meta, {});
if (!link) link = saved.preview || "";
if (!link) {
  console.error(`[pull] no link: give the game's preview link once (Sliqtly Games: get_game preview_url), e.g.\n  npm run studio:pull -- https://sliqtly.com/games/p/<token> ${folderArg}`);
  process.exit(2);
}

const { gameId, files } = await fetchGame(link, (s) => console.log("[pull] " + s));
const pulledAt = Date.now();
const stamp = new Date(pulledAt).toISOString().replace(/[:.]/g, "-");
let written = 0, same = 0, backedUp = 0;
for (const [rel, data] of files) {
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

// files only on disk (not removed: they may be local work)
const localOnly = walk(folder).filter((r) => !files.has(r));

writeJson(meta, { ...saved, preview: link, game: gameId, pulledAt, files: files.size });
writeJson(manifestPath(folder), manifestOf(files));
console.log(`[pull] ${gameId}: ${files.size} files, ${written} updated, ${same} unchanged` +
  (backedUp ? `, ${backedUp} local edits kept in .studio/backup/${stamp}` : "") + ` -> ${folder}`);
if (localOnly.length) console.log(`[pull] only on disk (kept): ${localOnly.slice(0, 20).join(", ")}${localOnly.length > 20 ? ` … ${localOnly.length - 20} more` : ""}`);
