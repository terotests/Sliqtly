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
import crypto from "node:crypto";
import zlib from "node:zlib";
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
// a slide with a script ({script=apps/red.tsx}) that turns its heading red
// while it runs (mcp-go/script_test.go writes it with SLIQTLY_WRITE_FIXTURES)
const SCRIPT_ID = "ScriptFix1";
const scriptDeck = fs.readFileSync(path.join(root, "scripts", "fixtures", "view-script.json"));
// the same with a 3-D world (allow: 3d, <scene3d>): an orange box
const WORLD_ID = "WorldFixture1";
const worldDeck = fs.readFileSync(path.join(root, "scripts", "fixtures", "view-app3d.json"));
// a world that reads the deck's .gltf (<SliqGltf src="data/m.gltf">): the
// orange box Sliqtly exported, served beside it
// (mcp-go/render3d_test.go TestSliqGltfReadsTheDecksModel writes both)
const GLTF_ID = "GltfFixture1";
const gltfDeck = fs.readFileSync(path.join(root, "scripts", "fixtures", "view-gltf.json"));
const gltfFile = fs.readFileSync(path.join(root, "scripts", "fixtures", "view-gltf.gltf"));
// the same model as a .glb (<SliqGltf src="data/m.glb">): its JSON chunk and
// the data: URI's bytes as the BIN chunk
const GLB_ID = "GlbFixture1";
const glbDeck = Buffer.from(String(gltfDeck).replaceAll("data/m.gltf", "data/m.glb").replace('"model/gltf+json"', '"model/gltf-binary"').replace("/fixture/view-gltf.gltf", "/fixture/view-gltf.glb"));
const glbFile = glbOf(JSON.parse(String(gltfFile)));
function glbOf(gltf) {
  const uri = gltf.buffers[0].uri;
  const bin = Buffer.from(uri.slice(uri.indexOf(",") + 1), "base64");
  delete gltf.buffers[0].uri;
  const pad = (b, fill) => Buffer.concat([b, Buffer.alloc((4 - (b.length % 4)) % 4, fill)]);
  const json = pad(Buffer.from(JSON.stringify(gltf)), 0x20);
  const data = pad(bin, 0);
  const head = Buffer.alloc(12);
  head.writeUInt32LE(0x46546c67, 0);
  head.writeUInt32LE(2, 4);
  head.writeUInt32LE(12 + 8 + json.length + 8 + data.length, 8);
  const chunk = (b, kind) => {
    const h = Buffer.alloc(8);
    h.writeUInt32LE(b.length, 0);
    h.writeUInt32LE(kind, 4);
    return Buffer.concat([h, b]);
  };
  return Buffer.concat([head, chunk(json, 0x4e4f534a), chunk(data, 0x004e4942)]);
}
// a file written for Ranger v2's 3-D façade, as Ranger has it
// (courtyard_live.tsx; mcp-go/render3d_test.go TestRangerThreeFileRunsAsItIs)
const THREE_ID = "ThreeFixture1";
// the Finance sample with `select-text: off` in its front matter
const NOSEL_ID = "NoSelect12";
const noSelDeck = JSON.stringify({ ...JSON.parse(deck), deck: { ...JSON.parse(deck).deck, selectText: false } });
const threeDeck = fs.readFileSync(path.join(root, "scripts", "fixtures", "view-three.json"));
// links on the slides (mcp-go/view_test.go TestViewLinks writes it): slide 1
// links to slide 2 by its heading, to slide 3 by number and to another deck
// (/s/AbCdEf1234#pdf-export, here the Finance sample)
const LINKS_ID = "LinksFix01";
const OTHER_ID = "AbCdEf1234";
const linksDeck = fs.readFileSync(path.join(root, "scripts", "fixtures", "view-links.json"));
// the Finance sample recorded for the web at 10 frames a second (the
// editor's Export ▸ Cloud HTML, web/main.js recordForWeb(10),
// gzipped as web/recstream.js packStream keeps it), under its hash
const recFile = fs.readFileSync(path.join(root, "scripts", "fixtures", "view-rec.sliqrec"));
const REC_HASH = crypto.createHash("sha256").update(recFile).digest("hex").slice(0, 24);
// the same as an export of now would be (web/recstream.js REV: every slide
// says where it is shown whole), and as one of format 1 (before #489): the
// fixture is from between the two, before its slides said it (#530)
const recNow = (() => {
  const s = JSON.parse(zlib.gunzipSync(recFile));
  return zlib.gzipSync(JSON.stringify({ ...s, rev: 1, slides: s.slides.map((x) => ({ ...x, settled: x.end })) }));
})();
const REC_NOW_HASH = crypto.createHash("sha256").update(recNow).digest("hex").slice(0, 24);
const recOne = zlib.gzipSync(JSON.stringify({ sliqrec: 1, width: 960, height: 540, fps: 10, frames: 1, duration: 0.1, slides: [{ start: 0, end: 0.1, steps: [] }], cmds: [], keys: [] }));
const REC_ONE_HASH = crypto.createHash("sha256").update(recOne).digest("hex").slice(0, 24);
// the same kind of recording exported with the 3-D flight (version 3-D)
const rec3dFile = fs.readFileSync(path.join(root, "scripts", "fixtures", "view-rec3d.sliqrec"));
const REC3D_HASH = crypto.createHash("sha256").update(rec3dFile).digest("hex").slice(0, 24);
// a presentation exported as a video (Export ▸ Cloud video): a 2 s WebM by
// Sliqtly's own encoder, at the same kind of address
const videoFile = fs.readFileSync(path.join(root, "scripts", "fixtures", "view-video.webm"));
const VIDEO_HASH = crypto.createHash("sha256").update(videoFile).digest("hex").slice(0, 24);
// the requests the video's file got (the player's own, not the page's fetch)
const videoReads = [];

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
  if (rel === "/fixture/view-gltf.glb") {
    res.writeHead(200, { "content-type": "model/gltf-binary", "access-control-allow-origin": "*" });
    res.end(glbFile);
    return;
  }
  if (rel === "/fixture/view-gltf.gltf") {
    res.writeHead(200, { "content-type": "model/gltf+json", "access-control-allow-origin": "*" });
    res.end(gltfFile);
    return;
  }
  // a video kept in Storage: sent there (View.rgr ViewPictures.recording)
  if (rel === "/api/view/" + ID + "/rec/" + VIDEO_HASH) {
    res.writeHead(302, { location: "/fixture/view-video.webm" });
    res.end();
    return;
  }
  if (rel === "/fixture/view-video.webm") {
    videoReads.push(req.headers.range || "");
    res.writeHead(200, { "content-type": "video/webm" });
    res.end(videoFile);
    return;
  }
  if (rel === "/api/view/" + ID + "/rec/" + REC3D_HASH) {
    res.writeHead(200, { "content-type": "application/octet-stream" });
    res.end(rec3dFile);
    return;
  }
  for (const [h, bytes] of [[REC_NOW_HASH, recNow], [REC_ONE_HASH, recOne]]) {
    if (rel !== "/api/view/" + ID + "/rec/" + h) continue;
    res.writeHead(200, { "content-type": "application/octet-stream" });
    res.end(bytes);
    return;
  }
  if (rel === "/api/view/" + ID + "/rec/" + REC_HASH) {
    res.writeHead(200, { "content-type": "application/octet-stream" });
    res.end(recFile);
    return;
  }
  if (rel.startsWith("/api/view/")) {
    const answers = { [ID]: deck, [APP_ID]: appDeck, [WORLD_ID]: worldDeck, [SCRIPT_ID]: scriptDeck, [GLTF_ID]: gltfDeck, [GLB_ID]: glbDeck, [THREE_ID]: threeDeck, [NOSEL_ID]: noSelDeck, [LINKS_ID]: linksDeck, [OTHER_ID]: deck };
    const found = answers[rel.slice("/api/view/".length)];
    res.writeHead(found ? 200 : 404, { "content-type": "application/json", "access-control-allow-origin": "*" });
    res.end(found || '{"error":"This shared presentation was not found."}');
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
  if (rel.startsWith("/r/")) rel = "/play.html";
  const file = path.join(viewDir, rel);
  if (!file.startsWith(viewDir) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) {
    missing.push(rel);
    res.writeHead(404);
    res.end();
    return;
  }
  // Hosting lets other sites read the files (firebase.json): the
  // assistant's preview fetches the viewer from its own origin
  res.writeHead(200, { "content-type": types[path.extname(file)] || "application/octet-stream", "access-control-allow-origin": "*" });
  fs.createReadStream(file).pipe(res);
});
await new Promise((ok) => server.listen(0, "127.0.0.1", ok));
const base = `http://127.0.0.1:${server.address().port}`;

// An assistant's chat as Claude shows the preview (mcp-go/assets/preview.html):
// in a sandboxed frame of another origin, under a CSP that lets scripts run
// only inline or from data:/blob: URLs and reach the site only by fetch, and
// handed a tool result naming WORLD_ID's link.
const previewHtml = fs.readFileSync(path.join(root, "mcp-go", "assets", "preview.html"), "utf8");
// At /gpt, as ChatGPT shows it (its script-src as Chrome reports it):
// scripts inline, from blob: URLs and the resourceDomains (the site) but
// not from data: URLs, a frame of its own origin, and the Finance sample's
// link.
const chat = http.createServer((req, res) => {
  const gpt = req.url.startsWith("/gpt");
  res.writeHead(200, {
    "content-type": "text/html",
    "content-security-policy": gpt
      ? `default-src 'none'; script-src 'self' 'wasm-unsafe-eval' 'unsafe-inline' 'unsafe-eval' blob: ${base}; style-src 'self' 'unsafe-inline' ${base}; img-src 'self' data: blob: ${base}; font-src 'self' data: ${base}; connect-src 'self' ${base}; frame-src 'self'`
      : `default-src 'none'; script-src 'unsafe-inline' data: blob: 'wasm-unsafe-eval'; worker-src data: blob:; style-src 'unsafe-inline'; img-src data: blob:; font-src data: ${base}; connect-src ${base}; frame-src data: blob:; base-uri 'self'`,
  });
  const result = gpt ? { share_url: base + "/s/" + ID, deck_id: ID, title: "Finance", slides } : { share_url: base + "/s/" + WORLD_ID, title: "3-D world", slides: 2 };
  res.end(`<!doctype html><meta charset="utf-8"><iframe id="w" sandbox="allow-scripts${gpt ? " allow-same-origin" : ""}" allow="fullscreen; clipboard-write" style="width:960px;height:640px;border:0"></iframe><script>
const w = document.getElementById("w");
addEventListener("message", (e) => {
  const m = e.data;
  // the preview's Download: the host refuses the tool call (as ChatGPT
  // does with a stale tool list), the link it opens instead is kept
  if (m && m.method === "tools/call") w.contentWindow.postMessage({ jsonrpc: "2.0", id: m.id, error: { code: -32000, message: "refused" } }, "*");
  if (m && m.method === "ui/open-link") { window.__opened = m.params.url; w.contentWindow.postMessage({ jsonrpc: "2.0", id: m.id, result: {} }, "*"); }
  if (!m || m.method !== "ui/initialize") return;
  w.contentWindow.postMessage({ jsonrpc: "2.0", id: m.id, result: {} }, "*");
  w.contentWindow.postMessage({ jsonrpc: "2.0", method: "ui/notifications/tool-result", params: { structuredContent: ${JSON.stringify(result)} } }, "*");
});
w.srcdoc = ${JSON.stringify(previewHtml).replace(/</g, "\\u003c")};
</script>`);
});
await new Promise((ok) => chat.listen(0, "127.0.0.1", ok));
const chatBase = `http://localhost:${chat.address().port}`;

const browser = await chromium.launch({ ...chromium_(), args: ["--use-gl=swiftshader", "--enable-unsafe-swiftshader", "--ignore-gpu-blocklist"] });
const failures = [];
const fail = (what) => failures.push(what);
async function open(url, { at = base } = {}) {
  const page = await browser.newPage({ viewport: { width: 1280, height: 720 } });
  // a 404 is in `missing` (or is the missing presentation's, on purpose)
  // (the preview trying which script URLs the host's CSP lets run is no error)
  page.on("console", (m) => { if (m.type() === "error" && !/^Failed to load resource|^(Refused to load|Loading) the script '(data:text\/javascript,0|blob:)/.test(m.text())) fail(`${url}: console: ${m.text()}`); });
  page.on("pageerror", (e) => fail(`${url}: ${e.message}`));
  await page.goto(at + url, { waitUntil: "load" });
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

  // the deck exported to the cloud as a light page (/r/{id}/{hash},
  // web/play.js): its frames played from each slide's master time, held at
  // a build step until Space, → the next slide, A plays it all through;
  // the page loads the painter, the recording and its faces, nothing else
  {
    const loaded = [];
    const rp = await browser.newPage({ viewport: { width: 1280, height: 720 } });
    rp.on("console", (m) => { if (m.type() === "error") fail(`/r/: console: ${m.text()}`); });
    rp.on("pageerror", (e) => fail(`/r/: ${e.message}`));
    rp.on("request", (r) => loaded.push(new URL(r.url()).pathname));
    await rp.goto(base + "/r/" + ID + "/" + REC_HASH, { waitUntil: "load" });
    await rp.waitForFunction(() => !document.getElementById("bar").hidden, null, { timeout: 15000 });
    const recN = await rp.evaluate(() => window.__play.nav.count);
    const count = () => rp.evaluate(() => document.getElementById("pCount").textContent);
    const look = () => rp.evaluate(() => {
      const g = document.createElement("canvas");
      g.width = 96;
      g.height = 54;
      const x = g.getContext("2d");
      x.drawImage(document.getElementById("c"), 0, 0, 96, 54);
      return Array.from(x.getImageData(0, 0, 96, 54).data).join(",");
    });
    const allowed = /^\/(r\/|play\.js|playlink\.js|recstream\.js|fxdeck\.js|viewlink\.js|gl\/evg-webgl\.js|fonts\/[\w-]+\.ttf|favicon\.svg|api\/view\/[^/]+\/rec\/)/;
    const extra = loaded.filter((p) => !allowed.test(p));
    if (extra.length) fail("the light player loaded more than it needs: " + extra.join(", "));
    if (!loaded.some((p) => p.startsWith("/fonts/"))) fail("the light player loaded no face");
    if ((await count()) !== `1 / ${recN}`) fail(`the light player starts at "${await count()}"`);
    // slide 2 slides in ({transition=slide})
    await rp.keyboard.press("ArrowRight");
    await rp.waitForTimeout(50);
    const coming = await look();
    await rp.waitForTimeout(1500);
    if ((await look()) === coming) fail("a recorded slide did not move as it came in");
    // slide 3 waits at its first build step
    await rp.keyboard.press("ArrowRight");
    await rp.waitForTimeout(100);
    if ((await count()) !== `3 / ${recN}`) fail("→ → in the light player did not go to slide 3");
    await rp.waitForTimeout(1500);
    const held = await look();
    await rp.waitForTimeout(600);
    if ((await look()) !== held) fail("a recorded slide did not hold at its build step");
    await rp.keyboard.press(" ");
    await rp.waitForTimeout(1500);
    if ((await look()) === held) fail("Space did not play the recording's next step");
    if ((await count()) !== `3 / ${recN}`) fail("Space at a build step left the slide");
    await rp.keyboard.press("ArrowLeft");
    await rp.waitForTimeout(100);
    if ((await count()) !== `2 / ${recN}`) fail("← in the light player did not go back a slide");
    await rp.keyboard.press("a");
    await rp.waitForFunction((n) => document.getElementById("pCount").textContent === `4 / ${n}`, recN, { timeout: 15000 }).catch(() => fail("A (play it all) did not go on to slide 4"));
    await rp.close();
    const rp0Flightless = async () => {
      const p0 = await browser.newPage();
      await p0.goto(base + "/r/" + ID + "/" + REC_HASH, { waitUntil: "load" });
      await p0.waitForFunction(() => window.__pageStarted, null, { timeout: 15000 });
      const none = await p0.evaluate(() => document.getElementById("pFlight").hidden && document.getElementById("start3d").hidden && !window.__play.flight);
      await p0.close();
      return none;
    };
    // #slide=N opens that slide; Share copies its /r/ link
    const sp = await browser.newPage();
    await sp.goto(base + "/r/" + ID + "/" + REC_HASH + "#slide=3", { waitUntil: "load" });
    await sp.waitForFunction(() => window.__play, null, { timeout: 15000 });
    if ((await sp.evaluate(() => document.getElementById("pCount").textContent)) !== `3 / ${recN}`) fail("#slide=3 did not open the light player at slide 3");
    const addr = await sp.evaluate(() => location.pathname + location.hash);
    if (addr !== "/r/" + ID + "/" + REC_HASH + "#slide=3") fail(`the light player's address became "${addr}"`);
    await sp.click("#pShare");
    await sp.waitForTimeout(200);
    const shared = await sp.evaluate(() => window.__lastShare || "");
    if (!shared.endsWith("/r/" + ID + "/" + REC_HASH + "#slide=3")) fail(`Share in the light player gave "${shared}"`);
    await sp.close();
    // another hash: said so, no error
    const gone = await browser.newPage();
    await gone.goto(base + "/r/" + ID + "/0123456789abcdef01234567", { waitUntil: "load" });
    await gone.waitForFunction(() => window.__pageStarted, null, { timeout: 15000 });
    if (!(await gone.evaluate(() => document.getElementById("note").textContent)).length) fail("a missing recording says nothing");
    await gone.close();
    // made with an older Sliqtly: format 1 is not played and says to export
    // it again; one from before #530 plays, and says so too; one of now
    // plays without a word
    const aged = async (hash) => {
      const ap = await browser.newPage();
      ap.on("pageerror", (e) => fail(`/r/ older: ${e.message}`));
      await ap.goto(base + "/r/" + ID + "/" + hash, { waitUntil: "load" });
      await ap.waitForFunction(() => window.__pageStarted, null, { timeout: 15000 });
      const got = await ap.evaluate(() => ({
        note: document.getElementById("note").hidden ? "" : document.getElementById("note").textContent,
        older: document.getElementById("older").hidden ? "" : document.getElementById("older").textContent,
        bar: !document.getElementById("bar").hidden,
      }));
      await ap.close();
      return got;
    };
    const one = await aged(REC_ONE_HASH);
    if (one.bar || !/older Sliqtly: export it again/.test(one.note)) fail("a recording of format 1 is not said to be older: " + JSON.stringify(one));
    const before = await aged(REC_HASH);
    if (!before.bar || !/older Sliqtly: export it again/.test(before.older)) fail("a recording from before #530 does not play with a word that it is older: " + JSON.stringify(before));
    const now = await aged(REC_NOW_HASH);
    if (!now.bar || now.older || now.note) fail("a recording of now plays with a word: " + JSON.stringify(now));
    // exported with the 3-D flight: offered on the page and on the bar; a
    // press flies it in full screen (the editor's flight, web/flight3d.js),
    // Esc lands back in the slides; a 2-D recording offers none
    if (!(await rp0Flightless())) fail("a 2-D recording offers the 3-D flight");
    const fp = await browser.newPage({ viewport: { width: 1280, height: 720 } });
    fp.on("pageerror", (e) => fail(`/r/ 3-D: ${e.message}`));
    fp.on("console", (m) => { if (m.type() === "error") fail(`/r/ 3-D: console: ${m.text()}`); });
    await fp.goto(base + "/r/" + ID + "/" + REC3D_HASH, { waitUntil: "load" });
    await fp.waitForFunction(() => window.__pageStarted && window.__play?.flight, null, { timeout: 15000 }).catch(() => fail("the 3-D recording offers no flight"));
    const offered = await fp.evaluate(() => ({ bar: !document.getElementById("pFlight").hidden, start: !document.getElementById("start3d").hidden }));
    if (!offered.bar || !offered.start) fail("the 3-D flight is not offered: " + JSON.stringify(offered));
    await fp.click("#start3d");
    const flew = await fp.waitForFunction(() => window.__play.flight.flying(), null, { timeout: 15000 }).then(() => true, () => false);
    if (!flew) fail("the light player's 3-D flight did not take off: " + await fp.evaluate(() => document.getElementById("note").textContent));
    else {
      await fp.waitForTimeout(1500);
      // what the screen shows (the GL canvas keeps no frame to read back)
      const shot = await fp.screenshot({ path: process.env.SHOT_3D || undefined });
      const lit = await fp.evaluate(async (b64) => {
        const img = new Image();
        img.src = "data:image/png;base64," + b64;
        await img.decode();
        const g = document.createElement("canvas");
        g.width = 64;
        g.height = 36;
        const x = g.getContext("2d");
        x.drawImage(img, 0, 0, 64, 36);
        const d = x.getImageData(0, 0, 64, 36).data;
        let n = 0;
        for (let i = 0; i < d.length; i += 4) if (d[i] + d[i + 1] + d[i + 2] > 120) n++;
        return n;
      }, shot.toString("base64"));
      if (lit < 5) fail(`the 3-D flight drew nothing (${lit} bright cells)`);
      await fp.keyboard.press("Space");
      await fp.waitForTimeout(2500);
      if (process.env.SHOT_3D) await fp.screenshot({ path: process.env.SHOT_3D.replace(/\.png$/, "-2.png") });
      await fp.keyboard.press("Escape");
      const landed = await fp.waitForFunction(() => !window.__play.flight.flying(), null, { timeout: 5000 }).then(() => true, () => false);
      if (!landed) fail("Esc did not land the 3-D flight");
    }
    await fp.close();
    // a video at the same kind of address: the browser's player reads it from
    // where it is kept (a redirect), its whole length
    const vp = await browser.newPage();
    vp.on("pageerror", (e) => fail(`/r/ video: ${e.message}`));
    await vp.goto(base + "/r/" + ID + "/" + VIDEO_HASH, { waitUntil: "load" });
    await vp.waitForFunction(() => window.__pageStarted, null, { timeout: 15000 });
    const shown = await vp.evaluate(() => ({ video: !document.getElementById("v").hidden, canvas: !document.getElementById("c").hidden, bar: !document.getElementById("bar").hidden }));
    if (!shown.video || shown.canvas || shown.bar) fail("a video at /r/ is not shown in the video player: " + JSON.stringify(shown));
    const dur = await vp.waitForFunction(() => {
      const v = document.getElementById("v");
      return v.readyState >= 1 && v.duration;
    }, null, { timeout: 15000 }).then((h) => h.jsonValue()).catch(() => 0);
    if (Math.abs(dur - 2) > 0.05) fail(`the video at /r/ lasts ${dur} s, not 2 s`);
    if (!videoReads.length) fail("the video at /r/ was not read from where it is kept");
    await vp.close();
  }

  // the slide's text selected with the mouse (web/slidetext.js): a drag
  // over the two lines under the title copies them as one sentence, the
  // slide stays; the next click lets go, the one after is the next slide
  await page.keyboard.press("Home");
  await page.waitForTimeout(200);
  const W = JSON.parse(deck).deck.width;
  const H = JSON.parse(deck).deck.height;
  const toPage = async (x, y) => {
    const r = await page.evaluate(() => { const b = document.getElementById("c").getBoundingClientRect(); return [b.left, b.top, b.width, b.height]; });
    const sc = Math.min(r[2] / W, r[3] / H);
    return [r[0] + (r[2] - W * sc) / 2 + x * sc, r[1] + (r[3] - H * sc) / 2 + y * sc];
  };
  const selectLines = async () => {
    const [x0, y0] = await toPage(54.5, 154.2 + 20);
    const [x1, y1] = await toPage(900, 212.44 + 20);
    await page.mouse.move(x0, y0);
    await page.mouse.down();
    await page.mouse.move((x0 + x1) / 2, y0, { steps: 4 });
    await page.mouse.move(x1, y1, { steps: 4 });
    await page.mouse.up();
  };
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.evaluate(() => navigator.clipboard.writeText("before"));
  const cursor = await (async () => {
    const [x, y] = await toPage(100, 154.2 + 20);
    await page.mouse.move(x, y);
    return page.evaluate(() => document.getElementById("c").style.cursor);
  })();
  if (cursor !== "text") fail(`over the slide's text the pointer is "${cursor}", not the I-beam`);
  await selectLines();
  await page.keyboard.press("Control+c");
  await page.waitForTimeout(200);
  const lines = await page.evaluate(() => navigator.clipboard.readText()).catch((e) => "(" + e.message + ")");
  if (lines !== "A budget, a buffer and compound interest – three things that go a long way.") fail(`a drag over the slide's text copied ${JSON.stringify(lines)}`);
  if ((await text(page, "vCount")) !== `1 / ${slides}`) fail("a drag over the slide's text went to another slide");
  // a right click on it: the browser's text menu, opened for a hidden field
  // holding the words (web/textmenu.js), not its menu for the picture; its
  // Copy copies them and the slide stays
  {
    await page.evaluate(() => window.addEventListener("contextmenu", (ev) => { window.__menuOn = ev.target.tagName; }, true));
    const [rx, ry] = await toPage(300, 154.2 + 20);
    await page.mouse.click(rx, ry, { button: "right" });
    await page.waitForTimeout(150);
    const m = await page.evaluate(() => ({ on: window.__menuOn, value: document.activeElement.value, readOnly: document.activeElement.readOnly }));
    if (m.on !== "TEXTAREA" || m.value !== lines || !m.readOnly) fail(`a right click on the selected text opened no text menu for it: ${JSON.stringify(m)}`);
    await page.evaluate(() => navigator.clipboard.writeText("before"));
    await page.evaluate(() => document.execCommand("copy"));
    await page.waitForTimeout(100);
    const viaMenu = await page.evaluate(() => navigator.clipboard.readText()).catch((e) => "(" + e.message + ")");
    if (viaMenu !== lines) fail(`the text menu's Copy copied ${JSON.stringify(viaMenu)}`);
    if ((await text(page, "vCount")) !== `1 / ${slides}`) fail("a right click on the selected text went to another slide");
  }
  const [tx, ty] = await toPage(600, 154.2 + 20);
  await page.mouse.click(tx, ty);
  await page.waitForTimeout(200);
  if ((await text(page, "vCount")) !== `1 / ${slides}`) fail("the click that lets go of a selection went to another slide");
  await page.mouse.click(tx, ty);
  await page.waitForTimeout(200);
  if ((await text(page, "vCount")) !== `2 / ${slides}`) fail("a click on the slide's text did not go to the next slide");
  await page.keyboard.press("Home");
  await page.waitForTimeout(200);
  await page.keyboard.press("Control+a");
  await page.keyboard.press("Control+c");
  await page.waitForTimeout(200);
  const all = await page.evaluate(() => navigator.clipboard.readText()).catch((e) => "(" + e.message + ")");
  if (!all.startsWith("Take charge of your money\nA budget,")) fail(`Ctrl+A, Ctrl+C on a slide copied ${JSON.stringify(all)}`);
  await page.keyboard.press("Escape");

  // Export ▾ at the bar's right end: PDF, PowerPoint, Markdown; a download
  // saved under the deck's name, a refusal said on the button
  await page.click("#vExport");
  const formats = await page.$$eval("#vMenu [data-format]", (bs) => bs.map((b) => b.dataset.format).join(","));
  if (formats !== "pdf,pptx,md") fail(`Export offers "${formats}"`);
  const [dl] = await Promise.all([page.waitForEvent("download", { timeout: 5000 }).catch(() => null), page.click('#vMenu [data-format="md"]')]);
  if (!dl) fail("Markdown export downloaded nothing");
  else if (dl.suggestedFilename() !== "talous.en.md") fail(`Markdown saved as "${dl.suggestedFilename()}"`);
  // Copy Markdown: the same Markdown on the clipboard, said on the button
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.click("#vExport");
  await page.click("#vMenu #vCopyMd");
  await page.waitForFunction(() => /copied|kopioitu/.test(document.getElementById("vExport").textContent), null, { timeout: 5000 })
    .catch(() => fail("Copy Markdown did not say it copied"));
  const copied = await page.evaluate(() => navigator.clipboard.readText()).catch((e) => "(" + e.message + ")");
  if (copied !== "# Take charge of your money\n") fail(`Copy Markdown put ${JSON.stringify(copied)} on the clipboard`);
  // Image of this slide: the slide shown as a PNG named by its number;
  // Copy image: that PNG on the clipboard
  await page.click("#vExport");
  const [img] = await Promise.all([page.waitForEvent("download", { timeout: 10000 }).catch(() => null), page.click("#vMenu #vImage")]);
  if (!img) fail("Image of this slide downloaded nothing");
  else {
    const png = fs.readFileSync(await img.path());
    if (!/ - \d+\.png$/.test(img.suggestedFilename()) || png.subarray(1, 4).toString() !== "PNG") fail(`Image saved as "${img.suggestedFilename()}", ${png.length} bytes`);
  }
  await page.click("#vExport");
  await page.click("#vMenu #vCopyImg");
  await page.waitForFunction(() => /image copied|kuva kopioitu/i.test(document.getElementById("vExport").textContent), null, { timeout: 10000 })
    .catch(() => fail("Copy image did not say it copied"));
  const imgTypes = await page.evaluate(async () => (await navigator.clipboard.read()).flatMap((it) => it.types)).catch((e) => ["(" + e.message + ")"]);
  if (!imgTypes.includes("image/png")) fail(`Copy image put ${JSON.stringify(imgTypes)} on the clipboard`);
  await page.click("#vExport");
  await page.click('#vMenu [data-format="pdf"]');
  await page.waitForFunction(() => /failed|epäonnistui/.test(document.getElementById("vExport").textContent), null, { timeout: 5000 })
    .catch(() => fail("a refused PDF export was not said"));
  if (!(await page.evaluate(() => document.getElementById("vMenu").hidden))) fail("the Export menu stayed open");
  await page.close();

  // `select-text: off`: a drag over the text selects nothing
  page = await open("/s/" + NOSEL_ID);
  await page.waitForFunction(() => !document.getElementById("viewBar").hidden, null, { timeout: 15000 });
  await page.waitForTimeout(300);
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.evaluate(() => navigator.clipboard.writeText("before"));
  {
    const r = await page.evaluate(() => { const b = document.getElementById("c").getBoundingClientRect(); return [b.left, b.top, b.width, b.height]; });
    const W = JSON.parse(deck).deck.width;
    const H = JSON.parse(deck).deck.height;
    const sc = Math.min(r[2] / W, r[3] / H);
    const x = r[0] + (r[2] - W * sc) / 2 + 60 * sc;
    const y = r[1] + (r[3] - H * sc) / 2 + 174 * sc;
    await page.mouse.move(x, y);
    await page.mouse.down();
    await page.mouse.move(x, y + 60 * sc, { steps: 4 });
    await page.mouse.up();
  }
  await page.keyboard.press("Control+a");
  await page.keyboard.press("Control+c");
  await page.waitForTimeout(200);
  const none = await page.evaluate(() => navigator.clipboard.readText()).catch((e) => "(" + e.message + ")");
  // (Ctrl+A is then the page's own: the bar's labels)
  if (/budget/.test(none)) fail(`with select-text: off the slide's text was copied: ${JSON.stringify(none)}`);
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

  // a slide's script runs in the viewer: the heading turns red while it
  // runs, drawn from the slide the server sent with the frame laid over it
  page = await open("/s/" + SCRIPT_ID);
  await page.waitForFunction(() => !document.getElementById("viewBar").hidden, null, { timeout: 15000 });
  const count = (blue) => page.evaluate((blue) => {
    const c = document.getElementById("c");
    const g = document.createElement("canvas");
    g.width = 640;
    g.height = 360;
    const x = g.getContext("2d");
    x.drawImage(c, 0, 0, 640, 360);
    const d = x.getImageData(0, 0, 640, 360).data;
    let n = 0;
    for (let i = 0; i < d.length; i += 4) {
      const hot = blue ? d[i + 2] > 200 && d[i] < 90 && d[i + 1] < 90 : d[i] > 200 && d[i + 1] < 90 && d[i + 2] < 90;
      if (hot) n++;
    }
    return n;
  }, blue);
  const red = () => count(false);
  // the first paint shows the slide as the script opens it (start() turns
  // the heading blue), not where it ends: the server sends that pose
  if (!(await count(true))) fail("the slide's first paint is not the script's opening pose (its heading is not blue)");
  let redAt = 0;
  for (let i = 0; i < 40 && !(redAt = await red()); i++) await page.waitForTimeout(250);
  if (!redAt) fail("the slide's script did not run in the viewer (its heading never turned red)");
  await page.close();

  // a program's 3-D world is drawn (web/three3d.js, pres_3d.js) into its
  // box: the orange box it puts in its <scene3d> is on the slide
  page = await open("/s/" + WORLD_ID);
  await page.waitForFunction(() => !document.getElementById("viewBar").hidden, null, { timeout: 15000 });
  const orange = (where) => where.evaluate(() => {
    const c = document.getElementById("c");
    const g = document.createElement("canvas");
    g.width = 128;
    g.height = 72;
    const x = g.getContext("2d");
    x.drawImage(c, 0, 0, 128, 72);
    const d = x.getImageData(0, 0, 128, 72).data;
    let n = 0;
    for (let i = 0; i < d.length; i += 4) if (d[i] > 150 && d[i + 1] > 50 && d[i + 1] < 160 && d[i + 2] < 90) n++;
    return n;
  });
  let orangeAt = 0;
  for (let i = 0; i < 40 && (orangeAt = await orange(page)) < 100; i++) await page.waitForTimeout(250);
  if (orangeAt < 100) fail(`the program's 3-D world was not drawn (${orangeAt} orange pixels)`);
  await page.close();

  // the same box read from the deck's .gltf (web/three3d.js readFile)
  page = await open("/s/" + GLTF_ID);
  await page.waitForFunction(() => !document.getElementById("viewBar").hidden, null, { timeout: 15000 });
  let modelAt = 0;
  for (let i = 0; i < 40 && (modelAt = await orange(page)) < 100; i++) await page.waitForTimeout(250);
  if (modelAt < 100) fail(`the world's .gltf model was not drawn (${modelAt} orange pixels)`);
  await page.close();

  // and from the same model as a .glb
  page = await open("/s/" + GLB_ID);
  await page.waitForFunction(() => !document.getElementById("viewBar").hidden, null, { timeout: 15000 });
  let glbAt = 0;
  for (let i = 0; i < 40 && (glbAt = await orange(page)) < 100; i++) await page.waitForTimeout(250);
  if (glbAt < 100) fail(`the world's .glb model was not drawn (${glbAt} orange pixels)`);
  await page.close();

  // courtyard_live.tsx (import * as THREE from "ranger:three") as it is:
  // its coloured boxes on the slide
  page = await open("/s/" + THREE_ID);
  await page.waitForFunction(() => !document.getElementById("viewBar").hidden, null, { timeout: 15000 });
  const boxes = () => page.evaluate(() => {
    const c = document.getElementById("c");
    const g = document.createElement("canvas");
    g.width = 128;
    g.height = 72;
    const x = g.getContext("2d");
    x.drawImage(c, 0, 0, 128, 72);
    const d = x.getImageData(0, 0, 128, 72).data;
    const seen = { red: 0, green: 0, blue: 0 };
    for (let i = 0; i < d.length; i += 4) {
      const [r, gr, b] = [d[i], d[i + 1], d[i + 2]];
      if (gr > r + 40 && gr > b + 20) seen.green++;
      else if (b > r + 40 && b > gr) seen.blue++;
      else if (r > gr + 60 && r > b + 60) seen.red++;
    }
    return seen;
  });
  let seen = {};
  for (let i = 0; i < 40; i++) {
    seen = await boxes();
    if (seen.red > 3 && seen.green > 3 && seen.blue > 3) break;
    await page.waitForTimeout(250);
  }
  if (!(seen.red > 3 && seen.green > 3 && seen.blue > 3)) fail(`the ranger:three courtyard was not drawn (${JSON.stringify(seen)})`);
  await page.close();

  // …and in the assistant's preview, where the modules are data: URLs: the
  // program runs (its worker made by the preview) and its world is drawn
  page = await open("/", { at: chatBase });
  let viewer = null;
  for (let i = 0; i < 60 && !viewer; i++) {
    for (const f of page.frames()) if (await f.evaluate(() => !!document.getElementById("c") && !document.getElementById("viewBar").hidden).catch(() => false)) viewer = f;
    if (!viewer) await page.waitForTimeout(250);
  }
  if (!viewer) fail("the assistant's preview did not open the viewer");
  else {
    orangeAt = 0;
    for (let i = 0; i < 60 && (orangeAt = await orange(viewer)) < 100; i++) await page.waitForTimeout(250);
    if (orangeAt < 100) fail(`the program's 3-D world was not drawn in the assistant's preview (${orangeAt} orange pixels)`);
  }
  await page.close();

  // …and in ChatGPT's, where no data: script runs: the viewer starts
  page = await open("/gpt", { at: chatBase });
  viewer = null;
  for (let i = 0; i < 60 && !viewer; i++) {
    for (const f of page.frames()) if (await f.evaluate(() => !!document.getElementById("c") && !document.getElementById("viewBar").hidden).catch(() => false)) viewer = f;
    if (!viewer) await page.waitForTimeout(250);
  }
  if (!viewer) fail("ChatGPT's preview did not open the viewer");
  // Download → PDF with the tool call refused: the viewer's download opens
  const widget = page.frames().find((f) => f.parentFrame() === page.mainFrame());
  await widget.click("#download");
  await widget.click('button[data-format="pdf"]');
  await page.waitForFunction(() => window.__opened, null, { timeout: 5000 }).catch(() => {});
  const opened = await page.evaluate(() => window.__opened || "");
  if (opened !== base + "/api/export/" + ID + "/pdf") fail(`the preview's Download opened "${opened}", not the viewer's PDF download`);
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

  // a link on a slide: a click goes where it points, not to the next slide
  {
    const ld = JSON.parse(linksDeck);
    const W = ld.deck.width;
    const H = ld.deck.height;
    const ready = async (p) => {
      await p.waitForFunction(() => !document.getElementById("viewBar").hidden, null, { timeout: 15000 });
      await p.waitForTimeout(300);
    };
    // the middle of slide 1's k-th link on the screen
    const at = async (p, k) => {
      const r = await p.evaluate(() => { const b = document.getElementById("c").getBoundingClientRect(); return [b.left, b.top, b.width, b.height]; });
      const sc = Math.min(r[2] / W, r[3] / H);
      const a = ld.lists[0].links[k];
      return [r[0] + (r[2] - W * sc) / 2 + (a.x + a.w / 2) * sc, r[1] + (r[3] - H * sc) / 2 + (a.y + a.h / 3) * sc];
    };
    page = await open("/s/" + LINKS_ID);
    await ready(page);
    let [x, y] = await at(page, 0);
    await page.mouse.move(x, y);
    if ((await page.evaluate(() => document.getElementById("c").style.cursor)) !== "pointer") fail("links: no hand over a link");
    await page.mouse.click(x, y);
    await page.waitForTimeout(200);
    if ((await text(page, "vCount")) !== "2 / 3") fail(`links: #heading did not go to slide 2: ${await text(page, "vCount")}`);
    await page.keyboard.press("Home");
    await page.waitForTimeout(200);
    [x, y] = await at(page, 1);
    await page.mouse.click(x, y);
    await page.waitForTimeout(200);
    if ((await text(page, "vCount")) !== "3 / 3") fail(`links: #slide=3 did not go to slide 3: ${await text(page, "vCount")}`);
    // another deck: opened in this tab, Back returns
    await page.keyboard.press("Home");
    await page.waitForTimeout(200);
    [x, y] = await at(page, 2);
    await page.mouse.click(x, y);
    await page.waitForURL((u) => u.pathname === "/s/" + OTHER_ID, { timeout: 5000 }).catch(() => fail(`links: the other deck did not open: ${page.url()}`));
    await ready(page);
    if ((await text(page, "vCount")) !== `1 / ${slides}`) fail(`links: the other deck shows ${await text(page, "vCount")}`);
    await page.goBack();
    await ready(page);
    if (!page.url().includes("/s/" + LINKS_ID)) fail(`links: Back did not return: ${page.url()}`);
    await page.close();
    // a link from another deck to a heading here
    page = await open("/s/" + LINKS_ID + "#hinnat-ja-%C3%A4%C3%A4net");
    await ready(page);
    if ((await text(page, "vCount")) !== "2 / 3") fail(`links: /s/{id}#heading opened ${await text(page, "vCount")}`);
    if (/hinnat/.test(page.url())) fail(`links: the heading's name stayed in the address: ${page.url()}`);
    await page.close();
  }

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
  // phone's width included, the licenses too
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
    m.renderLicenses([
      { uid: "u1", email: "anna.with.a.long.address@example.com", name: "Anna", plan: "trial", maxDocs: 2, used: 2, seen: "2026-10-08T09:00:00Z" },
      { uid: "u2", email: "tero@example.com", plan: "admin", maxDocs: -1, used: 7, admin: true },
    ]);
    return {
      licenses: [document.querySelectorAll("#lBody tr").length, document.querySelectorAll("#lBody form.quota").length],
      tiles: document.querySelectorAll("#tiles .tile").length,
      bars: ["cVisitors", "cDecks", "cUsers", "cBilling"].map((id) => document.querySelectorAll(`#${id} svg path`).length),
      month: document.querySelector("#tiles").textContent.includes("€1.08"),
    };
  });
  if (drawn.licenses.join() !== "2,1") fail(`the licenses did not draw: ${JSON.stringify(drawn.licenses)}`);
  if (drawn.tiles !== 4 || drawn.bars.some((n) => n < 2) || !drawn.month) fail(`the dashboard did not draw: ${JSON.stringify(drawn)}`);
  await page.setViewportSize({ width: 360, height: 740 });
  await page.waitForTimeout(200);
  if (await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)) fail("the dashboard is wider than a phone");
  await page.close();
} finally {
  await browser.close();
  server.close();
  chat.close();
}
for (const m of new Set(missing)) fail(`404 ${m}`);
if (failures.length) {
  for (const f of failures) log(`FAIL ${f}`);
  process.exit(1);
}
log(`view   web/dist-view: ${slides} slides painted, a program and a script run, a 3-D world drawn (also from a .gltf and a .glb, from a ranger:three file and in the assistant's preview), the light player (/r/) played, a video there, keys, a pinch and a swipe, links, Export, 404, the front page and the dashboard`);
