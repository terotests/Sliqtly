#!/usr/bin/env node
// SPDX-License-Identifier: AGPL-3.0-or-later
// npm run start:studio [-- <game folder> [studio options]]
//
// One command for the Game Studio: fetches what it is built from (once),
// builds it when a source is newer than the program, and starts it. The game
// folder given last time is remembered (build/last-project), so after the
// first run `npm run start:studio` alone is enough.
import { spawnSync } from "node:child_process";
import { existsSync, readFileSync, readdirSync, statSync, writeFileSync, mkdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { homedir } from "node:os";
import { fileURLToPath } from "node:url";

const studio = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const exe = join(studio, "build", "sliqtly-studio");
const lastFile = join(studio, "build", "last-project");

function have(cmd, args = ["--version"]) {
  return spawnSync(cmd, args, { stdio: "ignore" }).status === 0;
}

function run(cmd, args) {
  const r = spawnSync(cmd, args, { cwd: studio, stdio: "inherit" });
  if (r.status !== 0) {
    console.error(`\n[studio] ${cmd} ${args.join(" ")} failed (exit ${r.status}).`);
    process.exit(r.status || 1);
  }
}

function newest(dir) {
  let t = 0;
  if (!existsSync(dir)) return t;
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    if (e.name.startsWith(".") || e.name === "target") continue;
    const p = join(dir, e.name);
    t = Math.max(t, e.isDirectory() ? newest(p) : statSync(p).mtimeMs);
  }
  return t;
}

// what is missing, with the line that installs it
const missing = [];
if (!have("git")) missing.push("git");
if (!have("cargo")) missing.push("cargo (Rust): curl https://sh.rustup.rs -sSf | sh");
if (!have("pkg-config", ["--exists", "sdl3"])) {
  missing.push(process.platform === "darwin" ? "SDL3: brew install sdl3 pkg-config" : "SDL3 (pkg-config sdl3): build it from source if your system has no libsdl3-dev");
}
if (!have(process.env.CXX || "c++")) missing.push("a C++17 compiler (macOS: xcode-select --install)");
if (missing.length) {
  console.error("[studio] missing:\n  " + missing.join("\n  "));
  process.exit(1);
}

if (!existsSync(join(studio, ".deps", "Ranger")) || !existsSync(join(studio, ".deps", "RangerGames")) || !existsSync(join(studio, ".deps", "EVGSheets"))) {
  console.log("[studio] first run: fetching Ranger, EVGSheets and RangerGames into studio/.deps");
  run("bash", ["scripts/setup.sh"]);
}

const sources = Math.max(newest(join(studio, "src")), newest(join(studio, "native")), statSync(join(studio, "scripts", "build.sh")).mtimeMs);
const built = existsSync(exe) ? statSync(exe).mtimeMs : 0;
if (built < sources || process.argv.includes("--rebuild")) {
  console.log(built ? "[studio] sources changed: building" : "[studio] building (about 2 minutes the first time)");
  run("bash", ["scripts/build.sh"]);
}

// the game folder: the first argument that is not an option, else the last one used
const args = process.argv.slice(2).filter((a) => a !== "--rebuild");
const valued = new Set(["--game", "--file", "--size", "--scene", "--speed", "--frames", "--shot"]);
let folderAt = -1;
for (let i = 0; i < args.length; i++) {
  if (valued.has(args[i])) { i++; continue; }
  if (!args[i].startsWith("--")) { folderAt = i; break; }
}
const expand = (p) => resolve(p.replace(/^~(?=$|\/)/, homedir()));
let folder = folderAt >= 0 ? expand(args[folderAt]) : "";
if (!folder && existsSync(lastFile)) folder = readFileSync(lastFile, "utf8").trim();
if (!folder || !existsSync(folder)) {
  console.error("[studio] give a game folder (it holds game.json) or a folder of games:\n  npm run start:studio -- ~/games/solarpilot");
  process.exit(1);
}
mkdirSync(dirname(lastFile), { recursive: true });
writeFileSync(lastFile, folder + "\n");
if (folderAt >= 0) args.splice(folderAt, 1);

console.log(`[studio] ${folder}`);
const r = spawnSync(exe, [folder, ...args], { stdio: "inherit" });
process.exit(r.status ?? 0);
