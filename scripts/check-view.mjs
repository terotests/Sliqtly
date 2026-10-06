#!/usr/bin/env node
/**
 * npm run check:view: the public site (web/dist-view, npm run build:view)
 * opened in Chrome as Hosting serves it, with /api/view/{id} answered from
 * scripts/fixtures/view-deck.json (the Go server's answer for the Finance
 * sample; mcp-go/view_test.go checks the server's side).
 *
 * Fails when a shared presentation does not paint, the counter or the keys
 * do not go round the slides, a missing one is not said, the front page
 * has no way on, the page asks for a file the build does not have, or
 * anything is logged as an error.
 *
 * Chromium: $CHROMIUM_PATH, Playwright's (/opt/pw-browsers), or Chrome.
 */
import fs from "node:fs";
import path from "node:path";
import http from "node:http";
import { root, log } from "./lib.mjs";
import { viewDir } from "./build-view.mjs";

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

if (!fs.existsSync(path.join(viewDir, "index.html"))) {
  log("no web/dist-view: npm run build:view first");
  process.exit(1);
}
const ID = "Fixture123";
const deck = fs.readFileSync(path.join(root, "scripts", "fixtures", "view-deck.json"));
const slides = JSON.parse(deck).deck.slides;

// web/dist-view as firebase.json serves it: a file, /s/** the page, and
// /api/view/** the server's (here the fixture)
const types = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".json": "application/json", ".svg": "image/svg+xml", ".ttf": "font/ttf" };
const missing = [];
const server = http.createServer((req, res) => {
  let rel = decodeURIComponent(new URL(req.url, "http://x").pathname);
  if (rel.startsWith("/api/view/")) {
    const found = rel === "/api/view/" + ID;
    res.writeHead(found ? 200 : 404, { "content-type": "application/json" });
    res.end(found ? deck : '{"error":"This shared presentation was not found."}');
    return;
  }
  if (rel === "/" || rel.startsWith("/s/")) rel = "/index.html";
  const file = path.join(viewDir, rel);
  if (!file.startsWith(viewDir) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) {
    missing.push(rel);
    res.writeHead(404);
    res.end();
    return;
  }
  res.writeHead(200, { "content-type": types[path.extname(file)] || "application/octet-stream" });
  fs.createReadStream(file).pipe(res);
});
await new Promise((ok) => server.listen(0, "127.0.0.1", ok));
const base = `http://127.0.0.1:${server.address().port}`;

const browser = await chromium.launch({ ...chromium_(), args: ["--use-gl=swiftshader", "--enable-unsafe-swiftshader", "--ignore-gpu-blocklist"] });
const failures = [];
const fail = (what) => failures.push(what);
async function open(url) {
  const page = await browser.newPage({ viewport: { width: 1280, height: 720 } });
  // a 404 is in `missing` (or is the missing presentation's, on purpose)
  page.on("console", (m) => { if (m.type() === "error" && !/^Failed to load resource/.test(m.text())) fail(`${url}: console: ${m.text()}`); });
  page.on("pageerror", (e) => fail(`${url}: ${e.message}`));
  await page.goto(base + url, { waitUntil: "load" });
  return page;
}
const text = (page, id) => page.evaluate((i) => document.getElementById(i).textContent, id);

try {
  // a shared presentation: past the intro, the first slide painted
  let page = await open("/s/" + ID);
  await page.waitForFunction(() => !document.getElementById("viewBar").hidden, null, { timeout: 15000 });
  await page.waitForTimeout(300);
  const counter = await text(page, "vCount");
  if (counter !== `1 / ${slides}`) fail(`counter "${counter}", not "1 / ${slides}"`);
  // the assistant's preview waits for this (mcp-go/assets/preview.html)
  if (!(await page.evaluate(() => window.__pageStarted))) fail("window.__pageStarted is not set");
  // more than one colour on the canvas: something was drawn
  const colours = await page.evaluate(() => {
    const c = document.getElementById("c");
    const g = document.createElement("canvas");
    g.width = 64;
    g.height = 36;
    const x = g.getContext("2d");
    x.drawImage(c, 0, 0, 64, 36);
    const d = x.getImageData(0, 0, 64, 36).data;
    const seen = new Set();
    for (let i = 0; i < d.length; i += 4) seen.add((d[i] >> 4) + "," + (d[i + 1] >> 4) + "," + (d[i + 2] >> 4));
    return seen.size;
  });
  if (colours < 3) fail(`the slide looks empty (${colours} colours on the canvas)`);
  await page.keyboard.press("ArrowRight");
  await page.waitForTimeout(200);
  if ((await text(page, "vCount")) !== `2 / ${slides}`) fail("→ did not go to slide 2");
  if (!/slide=2/.test(page.url())) fail(`the address does not name slide 2: ${page.url()}`);
  await page.keyboard.press("End");
  await page.waitForTimeout(200);
  if ((await text(page, "vCount")) !== `${slides} / ${slides}`) fail("End did not go to the last slide");
  await page.close();

  // the slide a link names
  page = await open("/s/" + ID + "#slide=3");
  await page.waitForFunction(() => !document.getElementById("viewBar").hidden, null, { timeout: 15000 });
  if ((await text(page, "vCount")) !== `3 / ${slides}`) fail("#slide=3 did not open on slide 3");
  await page.close();

  // one that is not there
  page = await open("/s/Nothing999");
  await page.waitForFunction(() => document.getElementById("note").textContent.length > 0, null, { timeout: 15000 });
  if (!/not found|ei löytynyt/.test(await text(page, "note"))) fail("a missing presentation was not said");
  if (!(await page.evaluate(() => window.__pageStarted))) fail("a missing presentation does not count as started");
  await page.close();

  // the front page: no editor; the MCP part, the way to the assistants'
  // page and the experimental-service terms; no presentation's screen over it
  page = await open("/");
  await page.waitForTimeout(500);
  const home = await page.evaluate(() => ({
    connect: !!document.querySelector('.home a[href="/connect.html"]'),
    mcp: !!document.getElementById("mcp"),
    terms: /experimental service[\s\S]*"AS IS"/i.test(document.getElementById("terms")?.textContent || ""),
    intro: getComputedStyle(document.getElementById("brandIntro")).display,
    wide: document.documentElement.scrollWidth > window.innerWidth,
  }));
  if (!home.connect || !home.mcp || !home.terms) fail(`the front page lacks a part: ${JSON.stringify(home)}`);
  if (home.intro !== "none") fail("the presentation's screen covers the front page");
  await page.setViewportSize({ width: 360, height: 740 });
  await page.waitForTimeout(200);
  if (await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)) fail("the front page is wider than a phone");
  await page.close();
} finally {
  await browser.close();
  server.close();
}
for (const m of new Set(missing)) fail(`404 ${m}`);
if (failures.length) {
  for (const f of failures) log(`FAIL ${f}`);
  process.exit(1);
}
log(`view   web/dist-view: ${slides} slides painted, keys, links, 404 and the front page`);
