// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Tero Tolonen (Carnivore; MIT licence text in desktop/LICENSE)
// From Carnivore (github.com/terotests/CarnivoreMusicPlayer, MIT), scripts/ranger.mjs.
//
// The two things every build here needs from Ranger:
//
//   the compiler   ranger-compiler from npm (package.json devDependencies)
//   EVG            terotests/evg at the commit ranger.json names, fetched by
//                  `rgrc install` into Ranger's package cache and pinned by
//                  ranger.lock
//
// `evgDir()` is that package on disk: the storm/ directory of the EVG repo,
// which carries both the Ranger sources and the browser helpers (gl/*.js).

import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { createRequire } from "node:module";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

export const ROOT = path.join(path.dirname(fileURLToPath(import.meta.url)), "..");
const require = createRequire(import.meta.url);

export function rgrcPath() {
  try {
    return require.resolve("ranger-compiler/dist/rgrc.js", { paths: [ROOT] });
  } catch {
    console.error("ranger-compiler is not installed — run `npm install`");
    process.exit(2);
  }
}

// rgrc, run in this repository, with its output kept for the caller to judge.
export function rgrc(args) {
  const r = spawnSync(process.execPath, [rgrcPath(), ...args], { cwd: ROOT, encoding: "utf8" });
  return { status: r.status, log: (r.stdout || "") + (r.stderr || "") };
}

// Fetch what ranger.json names (a no-op when the cache already has it).
export function install() {
  const r = rgrc(["install"]);
  if (r.status !== 0) {
    process.stderr.write(r.log);
    console.error("`rgrc install` failed");
    process.exit(2);
  }
}

// Compile the app; exits with the log when it did not compile. The
// compiler has returned 0 on a failed build before, so the log and the output
// file decide.
export function compile(args, outFile) {
  fs.rmSync(outFile, { force: true });
  const r = rgrc(args);
  if (/Compilation FAILED|\[FAIL\]/.test(r.log) || !fs.existsSync(outFile)) {
    process.stderr.write(r.log);
    console.error("src/EditorApp.rgr did not compile");
    process.exit(1);
  }
}

// Where rgrc keeps packages: RANGER_PKG_CACHE, else ~/.cache/ranger/packages.
function cacheRoot() {
  if (process.env.RANGER_PKG_CACHE) return process.env.RANGER_PKG_CACHE;
  const home = process.env.HOME || os.homedir();
  return home ? path.join(home, ".cache", "ranger", "packages") : path.join(ROOT, ".ranger-cache", "packages");
}

export function evgDir() {
  const lock = JSON.parse(fs.readFileSync(path.join(ROOT, "ranger.lock"), "utf8"));
  const evg = lock.packages && lock.packages.evg;
  if (!evg || !evg.sha256) {
    console.error("ranger.lock has no evg entry — run `npm run deps`");
    process.exit(2);
  }
  const dir = path.join(cacheRoot(), evg.sha256);
  if (!fs.existsSync(path.join(dir, "EVGElement.rgr"))) {
    console.error(`EVG is not in the package cache (${dir}) — run \`npm run deps\``);
    process.exit(2);
  }
  return dir;
}
