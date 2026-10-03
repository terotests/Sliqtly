#!/usr/bin/env node
/**
 * npm run check:samples: opens every sample deck, in English and in Finnish,
 * from the built page (web/dist, served as Hosting serves it) and fails on
 * any response of 400 or more (a chart file missing from data/, a font, a
 * script) and on any error in the console or uncaught in the page.
 *
 * A request that never reaches a server (net::ERR_…: no network here) is
 * reported but not failed on; on a runner with the network it does not occur.
 *
 * Run after `npm run build`. Chromium: $CHROMIUM_PATH, Playwright's
 * (/opt/pw-browsers), or the installed Chrome.
 */
import fs from "node:fs";
import path from "node:path";
import http from "node:http";
import { distDir, root, log } from "./lib.mjs";

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

// web/dist as Hosting serves it: a file, else 404 (/s/** is the page)
const types = { ".html": "text/html", ".js": "text/javascript", ".mjs": "text/javascript", ".css": "text/css", ".json": "application/json", ".csv": "text/csv", ".md": "text/markdown", ".svg": "image/svg+xml", ".ttf": "font/ttf" };
const server = http.createServer((req, res) => {
  let rel = decodeURIComponent(new URL(req.url, "http://x").pathname);
  if (rel.endsWith("/") || rel.startsWith("/s/")) rel = "/index.html";
  const file = path.join(distDir, rel);
  if (!file.startsWith(distDir) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) {
    res.writeHead(404);
    res.end();
    return;
  }
  res.writeHead(200, { "content-type": types[path.extname(file)] || "application/octet-stream" });
  fs.createReadStream(file).pipe(res);
});
await new Promise((ok) => server.listen(0, "127.0.0.1", ok));
const base = `http://127.0.0.1:${server.address().port}/`;

// the sample keys: samples/<key>.md (Finnish) and <key>.en.md (English)
const keys = fs.readdirSync(path.join(root, "samples"))
  .filter((f) => f.endsWith(".en.md"))
  .map((f) => f.slice(0, -".en.md".length));

const browser = await chromium.launch({ ...chromium_(), args: ["--use-angle=swiftshader", "--enable-unsafe-swiftshader", "--ignore-gpu-blocklist"] });
let failures = 0;
try {
  for (const lang of ["en", "fi"]) {
    for (const key of keys) {
      const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 } });
      const page = await ctx.newPage();
      const bad = [];
      const offline = [];
      page.on("response", (r) => { if (r.status() >= 400) bad.push(`${r.status()} ${r.url()}`); });
      page.on("pageerror", (e) => bad.push(`uncaught: ${e.message}`));
      page.on("console", (m) => {
        if (m.type() !== "error") return;
        const text = m.text();
        if (/net::ERR_/.test(text)) offline.push(`${text} ${m.location().url || ""}`.trim());
        // a 400+ response is reported above, with its address
        else if (!/the server responded with a status of \d+/.test(text)) bad.push(`console: ${text.slice(0, 300)}`);
      });
      await page.goto(`${base}?sample=${key}&lang=${lang}`);
      await page.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 }).catch(() => bad.push("the page did not start"));
      await page.waitForLoadState("networkidle", { timeout: 30000 }).catch(() => {});
      await page.waitForTimeout(500);
      const name = `${key} (${lang})`;
      if (bad.length) {
        failures += 1;
        log(`FAIL ${name}`);
        for (const b of bad) log(`       ${b}`);
      } else log(`ok   ${name}`);
      if (offline.length) log(`     ${offline.length} request(s) did not reach a server (no network?)`);
      await ctx.close();
    }
  }
} finally {
  await browser.close();
  server.close();
}
if (failures) {
  log(`${failures} sample deck(s) with errors`);
  process.exit(1);
}
log("samples passed");
