#!/usr/bin/env node
/**
 * npm run spaces:check:page — the built page (npm run spaces:build) in Chrome:
 * it paints at desktop and phone width without an error, the navigation
 * answers a click where it is drawn, and a CSV picked in the file dialog
 * lands in the sheets and the summary.
 */
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { log } from "../../../scripts/lib.mjs";
import { serveDist } from "./serve.mjs";

const { chromium } = await import("playwright-core").catch(() => {
  log("playwright-core is not installed: npm install");
  process.exit(1);
});

function chromium_() {
  if (process.env.CHROMIUM_PATH) return { executablePath: process.env.CHROMIUM_PATH };
  const direct = path.join(process.env.PLAYWRIGHT_BROWSERS_PATH || "/opt/pw-browsers", "chromium");
  if (fs.existsSync(direct) && fs.statSync(direct).isFile()) return { executablePath: direct };
  return { channel: "chrome" };
}

let failed = 0;
const ok = (what, cond, detail = "") => {
  console.log(`  ${cond ? "PASS" : "FAIL"} ${what}${!cond && detail ? ": " + detail : ""}`);
  if (!cond) failed++;
};

const server = await serveDist(0);
const base = `http://127.0.0.1:${server.address().port}/`;
const browser = await chromium.launch({ ...chromium_(), args: ["--use-angle=swiftshader", "--enable-unsafe-swiftshader", "--ignore-gpu-blocklist"] });
const texts = (p) => p.evaluate(() => (window.__lastList ? window.__lastList.cmds.filter((c) => c.text).map((c) => c.text) : []));
// the centre of an element of the app's tree, in page pixels
const centre = (p, id) => p.evaluate((id) => {
  const a = window.__spaces;
  a.laidOut();
  const find = (el) => {
    if (el.id === id) return el;
    for (let i = 0; i < el.getChildCount(); i++) {
      const r = find(el.getChild(i));
      if (r) return r;
    }
    return null;
  };
  const e = find(a.root);
  return e ? [e.calculatedX + e.calculatedWidth / 2, e.calculatedY + e.calculatedHeight / 2] : null;
}, id);

try {
  for (const [w, h, locale, today] of [[1280, 800, "fi-FI", "Tänään"], [390, 844, "en-US", "Today"]]) {
    console.log(`${w}×${h} ${locale}`);
    const page = await browser.newPage({ viewport: { width: w, height: h }, locale });
    const errors = [];
    page.on("pageerror", (e) => errors.push(String(e)));
    page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
    await page.goto(base);
    await page.waitForFunction(() => window.__lastList && window.__lastList.cmds.length > 20);
    ok("the summary paints", (await texts(page)).includes(today));
    const nav = await centre(page, "sp-nav-calendar");
    await page.mouse.click(nav[0], nav[1]);
    await page.waitForTimeout(300);
    ok("a click on Calendar opens it", await page.evaluate(() => window.__spaces.tab === "calendar"));
    const sheets = await centre(page, "sp-nav-sheets");
    await page.mouse.click(sheets[0], sheets[1]);
    await page.waitForTimeout(300);
    const csv = path.join(os.tmpdir(), "spaces-check.csv");
    const d = new Date();
    fs.writeFileSync(csv, `Date,Time,What\n${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()},23:00,Imported row\n`);
    const chooser = page.waitForEvent("filechooser");
    const imp = await centre(page, "sp-import");
    await page.mouse.click(imp[0], imp[1]);
    await (await chooser).setFiles(csv);
    await page.waitForFunction(() => window.__spaces.space.sheets.length === 2);
    ok("a picked CSV is a sheet", (await texts(page)).includes("Imported row"));
    const home = await centre(page, "sp-nav-home");
    await page.mouse.click(home[0], home[1]);
    await page.waitForTimeout(300);
    ok("and its row is in today's summary", (await texts(page)).includes("Imported row"));
    const nodes = await page.evaluate(() => document.querySelectorAll(".evg-a11y [role]").length);
    ok("the accessible tree is mirrored", nodes > 20, String(nodes));
    ok("no errors", errors.length === 0, errors.join(" | "));
    await page.close();
  }
} finally {
  await browser.close();
  server.close();
}
if (failed > 0) {
  console.log(`${failed} failed`);
  process.exit(1);
}
console.log("ALL PASS");
