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
// a deck with a program on its first slide (```app): its box, source and
// stylesheet as the server sends them (mcp-go/view_test.go TestViewPlays)
const APP_ID = "AppFixture1";
const appDeck = fs.readFileSync(path.join(root, "scripts", "fixtures", "view-app.json"));
// the same slide with a 3-D world (allow: 3d): a red box over the field
const WORLD_ID = "WorldFixture1";
const worldDeck = (() => {
  const d = JSON.parse(appDeck);
  const p = d.deck.plays[0];
  p.allow = ["3d"];
  p.text = "function view() {\n  return (\n    <div className=\"field\">\n      <scene3d className=\"world\">\n        <mesh shape=\"box\" size={2.4} color=\"#ff2020\" rx={20} ry={30} />\n      </scene3d>\n    </div>\n  );\n}\n";
  p.cssText += ".world { position: absolute; left: 0px; top: 0px; width: 320px; height: 180px; }\n";
  return JSON.stringify(d);
})();

// web/dist-view as firebase.json serves it: a file, /s/** the page, and
// /api/view/** the server's (here the fixture)
const types = { ".wasm": "application/wasm", ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".json": "application/json", ".svg": "image/svg+xml", ".ttf": "font/ttf" };
const missing = [];
const server = http.createServer((req, res) => {
  let rel = decodeURIComponent(new URL(req.url, "http://x").pathname);
  // the downloads (View.rgr ViewExport): the Markdown here, a refusal for PDF
  if (rel.startsWith("/api/export/")) {
    if (rel === "/api/export/" + ID + "/md") {
      res.writeHead(200, { "content-type": "text/markdown; charset=utf-8" });
      res.end("# Take charge of your money\n");
    } else {
      res.writeHead(429, { "content-type": "application/json" });
      res.end('{"error":"Too many drawings today."}');
    }
    return;
  }
  if (rel.startsWith("/api/view/")) {
    const answers = { [ID]: deck, [APP_ID]: appDeck, [WORLD_ID]: worldDeck };
    const got = answers[rel.slice("/api/view/".length)];
    res.writeHead(got ? 200 : 404, { "content-type": "application/json" });
    res.end(got || '{"error":"This shared presentation was not found."}');
    return;
  }
  // Hosting's Firebase config (viewauth.js): not here, as where sign-in is off
  if (rel.startsWith("/__/")) {
    res.writeHead(404);
    res.end();
    return;
  }
  // the owner's dashboard: the page, and the server refusing a visitor
  if (rel.startsWith("/main/admin/api/")) {
    res.writeHead(401, { "content-type": "application/json" });
    res.end('{"error":"Sign in with Google."}');
    return;
  }
  if (rel === "/main/admin") rel = "/main/admin.html";
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

  // Export ▾ at the bar's right end: PDF, PowerPoint, Markdown; a download
  // saved under the deck's name, a refusal said on the button
  await page.click("#vExport");
  const formats = await page.$$eval("#vMenu [data-format]", (bs) => bs.map((b) => b.dataset.format).join(","));
  if (formats !== "pdf,pptx,md") fail(`Export offers "${formats}"`);
  const [dl] = await Promise.all([page.waitForEvent("download", { timeout: 5000 }).catch(() => null), page.click('#vMenu [data-format="md"]')]);
  if (!dl) fail("Markdown export downloaded nothing");
  else if (dl.suggestedFilename() !== "talous.en.md") fail(`Markdown saved as "${dl.suggestedFilename()}"`);
  await page.click("#vExport");
  await page.click('#vMenu [data-format="pdf"]');
  await page.waitForFunction(() => /failed|epäonnistui/.test(document.getElementById("vExport").textContent), null, { timeout: 5000 })
    .catch(() => fail("a refused PDF export was not said"));
  if (!(await page.evaluate(() => document.getElementById("vMenu").hidden))) fail("the Export menu stayed open");
  await page.close();

  // a program on a slide runs in the viewer: its picture (the green field
  // its stylesheet gives it) is painted in its box, a click is its own and
  // not the next slide, and a key it asks slide.next() with moves on
  page = await open("/s/" + APP_ID);
  await page.waitForFunction(() => !document.getElementById("viewBar").hidden, null, { timeout: 15000 });
  const green = () => page.evaluate(() => {
    const c = document.getElementById("c");
    const g = document.createElement("canvas");
    g.width = 128;
    g.height = 72;
    const x = g.getContext("2d");
    x.drawImage(c, 0, 0, 128, 72);
    const d = x.getImageData(0, 0, 128, 72).data;
    let n = 0;
    for (let i = 0; i < d.length; i += 4) if (Math.abs(d[i] - 0x12) < 24 && Math.abs(d[i + 1] - 0xa3) < 24 && Math.abs(d[i + 2] - 0x4b) < 24) n++;
    return n;
  });
  let greenAt = 0;
  for (let i = 0; i < 40 && !(greenAt = await green()); i++) await page.waitForTimeout(250);
  if (!greenAt) fail("the program on the slide painted nothing in its box");
  await page.mouse.click(640, 400);
  await page.waitForTimeout(300);
  if ((await text(page, "vCount")) !== "1 / 2") fail("a click on the program went to another slide");
  await page.keyboard.press("n");
  await page.waitForFunction(() => document.getElementById("vCount").textContent === "2 / 2", null, { timeout: 5000 })
    .catch(() => fail("the program's slide.next() did not go to slide 2"));
  await page.close();

  // a program's 3-D world (allow: 3d) is drawn in the viewer as in the
  // editor: the red box over the green field
  page = await open("/s/" + WORLD_ID);
  await page.waitForFunction(() => !document.getElementById("viewBar").hidden, null, { timeout: 15000 });
  const red = () => page.evaluate(() => {
    const c = document.getElementById("c");
    const g = document.createElement("canvas");
    g.width = 128;
    g.height = 72;
    const x = g.getContext("2d");
    x.drawImage(c, 0, 0, 128, 72);
    const d = x.getImageData(0, 0, 128, 72).data;
    let n = 0;
    for (let i = 0; i < d.length; i += 4) if (d[i] > 80 && d[i] > d[i + 1] * 2 && d[i] > d[i + 2] * 2) n++;
    return n;
  });
  let redAt = 0;
  for (let i = 0; i < 60 && !(redAt = await red()); i++) await page.waitForTimeout(250);
  if (!redAt) fail("the program's 3-D world was not drawn in the viewer");
  await page.close();

  // on a phone: two fingers spread zoom the slide in (and go to no other
  // slide), pinched back it fits again, and a swipe goes on
  const phone = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true, deviceScaleFactor: 2 });
  page = await phone.newPage();
  page.on("pageerror", (e) => fail(`phone: ${e.message}`));
  await page.goto(base + "/s/" + ID, { waitUntil: "load" });
  await page.waitForFunction(() => !document.getElementById("viewBar").hidden, null, { timeout: 15000 });
  await page.waitForTimeout(300);
  const cdp = await phone.newCDPSession(page);
  const touch = (type, points) => cdp.send("Input.dispatchTouchEvent", { type, touchPoints: points.map(([x, y], id) => ({ x, y, id })) });
  async function fingers(from, to) {
    await touch("touchStart", from);
    for (let i = 1; i <= 8; i++) {
      await touch("touchMove", from.map(([x, y], k) => [x + ((to[k][0] - x) * i) / 8, y + ((to[k][1] - y) * i) / 8]));
      await page.waitForTimeout(16);
    }
    await touch("touchEnd", []);
    await page.waitForTimeout(150);
  }
  const look = () => page.evaluate(() => document.getElementById("c").toDataURL());
  const fitted = await look();
  await fingers([[175, 422], [215, 422]], [[95, 422], [295, 422]]);
  if ((await look()) === fitted) fail("a pinch on a phone did not zoom the slide");
  if ((await text(page, "vCount")) !== `1 / ${slides}`) fail("a pinch went to another slide");
  await fingers([[95, 422], [295, 422]], [[175, 422], [215, 422]]);
  if ((await look()) !== fitted) fail("pinched back, the slide does not fit the window again");
  await fingers([[300, 422]], [[100, 422]]);
  if ((await text(page, "vCount")) !== `2 / ${slides}`) fail("a swipe on a phone did not go to slide 2");
  await phone.close();

  // the slide a link names
  page = await open("/s/" + ID + "#slide=3");
  await page.waitForFunction(() => !document.getElementById("viewBar").hidden, null, { timeout: 15000 });
  if ((await text(page, "vCount")) !== `3 / ${slides}`) fail("#slide=3 did not open on slide 3");
  // Share slide: the link to the slide shown, in the query (the server's
  // link card reads it; a chat app never sees a hash), copied
  await page.evaluate(() => {
    window.__copied = "";
    Object.defineProperty(navigator, "clipboard", { value: { writeText: async (t) => { window.__copied = t; } } });
  });
  await page.click("#vShare");
  await page.waitForFunction(() => /copied|kopioitu/i.test(document.getElementById("vShare").textContent), null, { timeout: 5000 })
    .catch(() => fail("Share slide did not say the link was copied"));
  const shared = await page.evaluate(() => window.__copied);
  if (!shared.endsWith("/s/" + ID + "?slide=3")) fail("Share slide copied " + JSON.stringify(shared));
  await page.close();
  // …and that link opens on its slide
  page = await open("/s/" + ID + "?slide=4");
  await page.waitForFunction(() => !document.getElementById("viewBar").hidden, null, { timeout: 15000 });
  if ((await text(page, "vCount")) !== `4 / ${slides}`) fail("?slide=4 did not open on slide 4");
  await page.close();

  // one that is not there
  page = await open("/s/Nothing999");
  await page.waitForFunction(() => document.getElementById("note").textContent.length > 0, null, { timeout: 15000 });
  if (!/not found|ei löytynyt/.test(await text(page, "note"))) fail("a missing presentation was not said");
  // it may be private: its owner is offered a sign-in
  if (!(await page.$("#note #vSignIn"))) fail("a missing presentation offers no sign-in for its owner");
  await page.waitForTimeout(600);
  if (!(await page.isVisible("#note #vSignIn"))) fail("the not-found note does not stay on screen");
  if (!(await page.evaluate(() => window.__pageStarted))) fail("a missing presentation does not count as started");
  await page.close();

  // the front page: no editor; the MCP part, the way to the assistants'
  // page and the feature list; no presentation's screen over it
  page = await open("/");
  await page.waitForTimeout(500);
  const home = await page.evaluate(() => ({
    connect: !!document.querySelector('.home a[href="/connect.html"]'),
    mcp: !!document.getElementById("mcp"),
    features: document.querySelectorAll("#features .features > div").length > 0,
    intro: getComputedStyle(document.getElementById("brandIntro")).display,
    wide: document.documentElement.scrollWidth > window.innerWidth,
  }));
  if (!home.connect || !home.mcp || !home.features) fail(`the front page lacks a part: ${JSON.stringify(home)}`);
  if (home.intro !== "none") fail("the presentation's screen covers the front page");
  await page.setViewportSize({ width: 360, height: 740 });
  await page.waitForTimeout(200);
  if (await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)) fail("the front page is wider than a phone");
  await page.close();

  // the owner's dashboard: signed out it asks for sign-in and shows no
  // numbers; with the server's answer (mcp-go/admin.go) it draws them, a
  // phone's width included
  page = await open("/main/admin");
  await page.waitForFunction(() => !document.getElementById("signin").hidden, null, { timeout: 15000 })
    .catch(() => fail("/main/admin does not ask a visitor to sign in"));
  if (await page.isVisible("#board")) fail("/main/admin shows the board signed out");
  const drawn = await page.evaluate(async () => {
    const days = ["2026-10-06", "2026-10-07", "2026-10-08"];
    const r = {
      generated: "2026-10-08T15:00:00Z", days,
      visitors: { rows: days.map((day, i) => ({ day, visitors: i + 2, views: i + 3, editor: 0, view: i, edit: 1, mobile: 1 })), visitors: 9, views: 12, refs: [{ name: "google.com", value: 3 }] },
      decks: { rows: days.map((day, i) => ({ day, decks: i + 1, signedIn: i, anonymous: 1, people: i ? 1 : 0 })), total: 6, anonymous: 3, people: 1, allTime: 140 },
      users: { rows: days.map((day, i) => ({ day, new: i, active: 2 })), total: 12, new: 3, active: 6 },
      billing: { currency: "EUR", rows: days.map((day, i) => ({ day, cost: i * 0.25 })), range: 0.75, month: 1.08, lastMonth: 1, services: [{ name: "Cloud Run", value: 0.75 }], latest: "2026-10-07" },
    };
    const m = await import("./admin.js?v=" + document.querySelector('meta[name="build"]').content);
    m.show("board");
    m.render(r);
    return {
      tiles: document.querySelectorAll("#tiles .tile").length,
      bars: ["cVisitors", "cDecks", "cUsers", "cBilling"].map((id) => document.querySelectorAll(`#${id} svg path`).length),
      month: document.querySelector("#tiles").textContent.includes("€1.08"),
    };
  });
  if (drawn.tiles !== 4 || drawn.bars.some((n) => n < 2) || !drawn.month) fail(`the dashboard did not draw: ${JSON.stringify(drawn)}`);
  await page.setViewportSize({ width: 360, height: 740 });
  await page.waitForTimeout(200);
  if (await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)) fail("the dashboard is wider than a phone");
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
log(`view   web/dist-view: ${slides} slides painted, a program run, keys, a pinch and a swipe, links, Export, 404, the front page and the dashboard`);
