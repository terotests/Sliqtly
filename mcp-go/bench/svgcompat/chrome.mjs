// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Each SVG of a test suite drawn as the player draws it: web/picture.js
// sizes its root, Chromium shows it in an <img> (from a Blob, so it loads
// nothing from outside itself) and draws that onto a canvas. Writes
// <out>/<name>.png.
//
//   node chrome.mjs <suite dir> <out dir> [side]
import fs from "node:fs";
import path from "node:path";
import { chromium } from "playwright-core";
import { svgSize, svgSizedTo, rasterSize } from "../../../web/picture.js";

const [dir, out, sideArg] = process.argv.slice(2);
const side = Number(sideArg || 480);
fs.mkdirSync(out, { recursive: true });
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM || "/opt/pw-browsers/chromium-1194/chrome-linux/chrome" });
const page = await browser.newPage();
await page.setContent("<!doctype html><body></body>");
const files = fs.readdirSync(dir).filter((f) => f.endsWith(".svg")).sort();
let n = 0;
for (const f of files) {
  const text = fs.readFileSync(path.join(dir, f), "utf8");
  const size = svgSize(text);
  if (!size) continue;
  const [w, h] = rasterSize(size[0], size[1], side);
  const sized = svgSizedTo(text, w, h);
  const url = await page.evaluate(async ({ sized, w, h }) => {
    const blob = new Blob([sized], { type: "image/svg+xml" });
    const u = URL.createObjectURL(blob);
    const img = new Image();
    img.src = u;
    const ok = await img.decode().then(() => true, () => false);
    URL.revokeObjectURL(u);
    const c = document.createElement("canvas");
    c.width = w;
    c.height = h;
    if (!ok) return "failed";
    c.getContext("2d").drawImage(img, 0, 0, w, h);
    // the player's canvas.toBlob refuses a canvas Chromium counts as
    // tainted (an SVG with a foreignObject): the picture is not shown
    try {
      return c.toDataURL("image/png");
    } catch {
      return "tainted";
    }
  }, { sized, w, h });
  const name = f.replace(/\.svg$/, ".png");
  if (url.startsWith("data:")) fs.writeFileSync(path.join(out, name), Buffer.from(url.split(",")[1], "base64"));
  else fs.writeFileSync(path.join(out, name.replace(/\.png$/, "." + url)), "");
  n++;
}
await browser.close();
console.log(`chromium: ${n} SVGs drawn to ${out}`);
