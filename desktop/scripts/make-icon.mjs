#!/usr/bin/env node
// SPDX-License-Identifier: MIT
//
// The application icon: the Sliqtly mark (EditorApp.iconDisplayListJson),
// painted by the native binary at 1024 px and kept as native/icon/icon-1024.png,
// plus native/icon/AppIcon.icns (written here, so it needs no macOS tools; on
// macOS build-native.mjs makes the bundle's own with sips / iconutil and uses
// this one only when those are missing). The host sets the window / Dock icon
// at runtime (it draws it itself); the web build copies the PNG.
//
//   node scripts/make-icon.mjs        (after npm run native)

import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { ROOT } from "./ranger.mjs";
import { pngOf, readPam } from "./png.mjs";

const bin = path.join(ROOT, "native", "build", "sliqtly-editor");
if (!fs.existsSync(bin)) { console.error("build it first: npm run native"); process.exit(2); }
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "sliqtly-icon-"));
const pam = path.join(tmp, "icon.pam");
let cmd = bin, args = ["--icon", pam];
if (process.platform === "linux" && !process.env.DISPLAY) {
  args = ["-a", "-s", "-screen 0 1280x800x24 +extension GLX", cmd, ...args];
  cmd = "xvfb-run";
}
const r = spawnSync(cmd, args, { stdio: "inherit", env: { ...process.env, SLIQTLY_EDITOR_CONFIG: path.join(tmp, "cfg") } });
if (r.status !== 0 || !fs.existsSync(pam)) { console.error("the icon was not drawn"); process.exit(1); }
const out = path.join(ROOT, "native", "icon", "icon-1024.png");
fs.mkdirSync(path.dirname(out), { recursive: true });
const full = readPam(pam);
fs.writeFileSync(out, pngOf(full));

// Halve with a 2×2 box filter: 1024 → 512 → … → 16.
const half = ({ w, h, data }) => {
  const W = w >> 1, H = h >> 1, o = Buffer.alloc(W * H * 4);
  for (let y = 0; y < H; y++) for (let x = 0; x < W; x++) for (let c = 0; c < 4; c++) {
    const at = (yy, xx) => data[(yy * w + xx) * 4 + c];
    o[(y * W + x) * 4 + c] = (at(2 * y, 2 * x) + at(2 * y, 2 * x + 1) + at(2 * y + 1, 2 * x) + at(2 * y + 1, 2 * x + 1) + 2) >> 2;
  }
  return { w: W, h: H, data: o };
};
const png = { 1024: pngOf(full) };
let img = full;
while (img.w > 16) { img = half(img); png[img.w] = pngOf(img); }
// ICNS: "icns" + total length, then (type, length incl. 8, PNG) per entry.
const entries = [["icp4", 16], ["icp5", 32], ["ic11", 32], ["ic12", 64], ["ic07", 128], ["ic13", 256], ["ic08", 256], ["ic14", 512], ["ic09", 512], ["ic10", 1024]];
const parts = entries.map(([type, size]) => {
  const head = Buffer.alloc(8);
  head.write(type, 0, "ascii");
  head.writeUInt32BE(png[size].length + 8, 4);
  return Buffer.concat([head, png[size]]);
});
const body = Buffer.concat(parts);
const head = Buffer.alloc(8);
head.write("icns", 0, "ascii");
head.writeUInt32BE(body.length + 8, 4);
fs.writeFileSync(path.join(ROOT, "native", "icon", "AppIcon.icns"), Buffer.concat([head, body]));
fs.rmSync(tmp, { recursive: true, force: true });
console.log("wrote native/icon/icon-1024.png, native/icon/AppIcon.icns");
