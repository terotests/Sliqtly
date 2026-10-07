#!/usr/bin/env node
// SPDX-License-Identifier: MIT
//
// The native editor (native/build-editor/sliqtly-native), headless: a click
// lands on the line under it, typing (ä, –) goes there, a wheel scrolls the
// way it was turned, and a frame is painted. Run under xvfb-run on Linux
// with no display.
//
//   node scripts/build-editor-native.mjs && node scripts/check-editor-native.mjs

import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { ROOT } from "./ranger.mjs";

const BUILD = path.join(ROOT, "native", "build-editor");
const bin = path.join(BUILD, "sliqtly-native");
if (!fs.existsSync(bin)) { console.error("build it first: node scripts/build-editor-native.mjs"); process.exit(2); }
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "sliqtly-native-"));
const deck = path.join(tmp, "deck.md");
const lines = ["---", "title: Check", "---", "# One", ...Array.from({ length: 40 }, (_, i) => `line ${i + 5}`)];
fs.writeFileSync(deck, lines.join("\n") + "\n");

// The editor's text starts at y 137 with 21 px lines in a 1280 x 800 window
// (the first screenshot of this host). y 158 is line 2.
const LINE2_Y = 158;
fs.writeFileSync(path.join(tmp, "steps.txt"), [
  `click 300 ${LINE2_Y}`,
  "key end",
  "type  Hyvää – X1",
  // one notch toward the user: the text moves up, a later line under y 158
  "wheel 300 400 0 -1 0",
  "frame",
  `click 300 ${LINE2_Y}`,
  "key end",
  "type  X2",
  "save",
].join("\n") + "\n");

const shot = path.join(tmp, "shot.pam");
let cmd = bin, args = [deck, "--res", path.join(BUILD, "editor-res"), "--size", "1280x800", "--script", path.join(tmp, "steps.txt"), "--shot", shot];
if (process.platform === "linux" && !process.env.DISPLAY) {
  args = ["-a", "-s", "-screen 0 1600x1000x24 +extension GLX", cmd, ...args];
  cmd = "xvfb-run";
}
const r = spawnSync(cmd, args, { encoding: "utf8" });
const fails = [];
const expect = (what, ok, detail = "") => {
  if (!ok) fails.push(what);
  console.log(`  ${ok ? "ok  " : "FAIL"} ${what}${ok ? "" : ` — ${detail}`}`);
};
expect("the host ran", r.status === 0, `${r.stdout}${r.stderr}`);
const saved = fs.existsSync(deck) ? fs.readFileSync(deck, "utf8").split("\n") : [];
expect("a click lands on the line under it", saved[1] === "title: Check Hyvää – X1", JSON.stringify(saved[1]));
const at2 = saved.findIndex((l) => l.endsWith(" X2"));
expect("a wheel turned toward the user scrolls the text up", at2 > 1, `X2 on line ${at2 + 1}`);
let lit = 0;
if (fs.existsSync(shot)) {
  const buf = fs.readFileSync(shot);
  const end = buf.indexOf("ENDHDR\n") + 7;
  for (let i = end + 3; i < buf.length; i += 4 * 97) if (buf[i] > 0) lit++;
}
expect("a frame is painted", lit > 100, `${lit} samples`);
fs.rmSync(tmp, { recursive: true, force: true });
process.exit(fails.length ? 1 : 0);
