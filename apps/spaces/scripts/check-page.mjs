#!/usr/bin/env node
/**
 * npm run spaces:check:page — the built page (npm run spaces:build) in Chrome:
 * it paints at desktop and phone width without an error, the navigation
 * answers a click where it is drawn, and a CSV picked in the file dialog
 * lands in the sheets and the summary.
 *
 * Then the Warden page (npm run warden:build) against a stand-in for the
 * server: it reads the space from the API, a typed invite reaches the API,
 * and a signed-out answer sends the page to the sign-in page.
 */
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { log } from "../../../scripts/lib.mjs";
import http from "node:http";
import { serveDist, serveDir } from "./serve.mjs";
import { appDir } from "./lib.mjs";

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
    // The model takes the sheet at once; the canvas paints it on the next frame.
    const painted = await page
      .waitForFunction(() => window.__lastList && window.__lastList.cmds.some((c) => c.text === "Imported row"), null, { timeout: 3000 })
      .then(() => true, () => false);
    ok("a picked CSV is a sheet", painted);
    const home = await centre(page, "sp-nav-home");
    await page.mouse.click(home[0], home[1]);
    await page.waitForTimeout(300);
    ok("and its row is in today's summary", (await texts(page)).includes("Imported row"));
    const nodes = await page.evaluate(() => document.querySelectorAll(".evg-a11y [role]").length);
    ok("the accessible tree is mirrored", nodes > 20, String(nodes));
    ok("no errors", errors.length === 0, errors.join(" | "));
    await page.close();
  }

  // --- Warden, against a stand-in for the server -----------------------------------
  const wardenDist = path.join(appDir, "web", "dist-warden");
  if (!fs.existsSync(path.join(wardenDist, "index.html"))) throw new Error("npm run warden:build first");
  const calls = [];
  let signedIn = true;
  const iso = (d) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  const space = (extra = {}) => ({
    space_id: "sp1", name: "Home crew", vocab: "family", lang: "en", role: "owner",
    members: [{ uid: "u1", name: "Mia", email: "mia@example.com", role: "owner" }], invites: [],
    wards: [{ id: "w1", name: "Aino", color: "rose" }], ...extra,
  });
  const files = serveDir(wardenDist);
  const api = http.createServer(async (req, res) => {
    const url = new URL(req.url, "http://localhost");
    if (!url.pathname.startsWith("/apps/warden/")) return res.writeHead(404).end();
    const rest = url.pathname.slice("/apps/warden/".length);
    if (rest === "login") return res.writeHead(200, { "content-type": "text/html" }).end("<title>Sign in</title>");
    if (!rest.startsWith("api/")) return files(rest, res);
    let body = "";
    for await (const c of req) body += c;
    const op = rest.slice(4);
    calls.push({ op, body: JSON.parse(body || "{}"), type: req.headers["content-type"] });
    const send = (v, status = 200) => res.writeHead(status, { "content-type": "application/json" }).end(JSON.stringify(v));
    if (!signedIn) return send({ error: "sign in", code: "signed-out" }, 401);
    if (op === "list_spaces") return send({ me: { uid: "u1", name: "Mia", email: "mia@example.com" }, spaces: [space()] });
    if (op === "get_space") {
      return send({ space: space(), events: [{ event_id: "ev1", title: "Dentist", date: iso(new Date()), start: "23:00", end: "23:30", ward: "w1", who: [{ name: "Juho", role: "takes" }] }] });
    }
    if (op === "invite_member") return send(space({ invites: [calls[calls.length - 1].body.email] }));
    return send({ error: "not in this check" }, 400);
  });
  await new Promise((ok) => api.listen(0, "127.0.0.1", ok));
  const wardenUrl = `http://127.0.0.1:${api.address().port}/apps/warden/`;
  try {
    console.log("Warden 1280×800 en-US");
    const page = await browser.newPage({ viewport: { width: 1280, height: 800 }, locale: "en-US" });
    const errors = [];
    page.on("pageerror", (e) => errors.push(String(e)));
    page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
    await page.goto(wardenUrl);
    const shown = await page
      .waitForFunction(() => window.__lastList && window.__lastList.cmds.some((c) => c.text === "Aino · Takes: Juho"), null, { timeout: 8000 })
      .then(() => true, () => false);
    ok("the space comes from the API", shown, JSON.stringify(calls.map((c) => c.op)));
    ok("as JSON", calls.every((c) => c.type === "application/json"));
    const nav = await centre(page, "sp-nav-members");
    await page.mouse.click(nav[0], nav[1]);
    await page.waitForTimeout(300);
    const field = await centre(page, "sp-invite-email");
    await page.mouse.click(field[0], field[1]);
    await page.waitForTimeout(100);
    await page.keyboard.type("leena@example.com");
    await page.keyboard.press("Enter");
    const invited = await page
      .waitForFunction(() => window.__spaces.space.invites.length === 1, null, { timeout: 5000 })
      .then(() => true, () => false);
    const inv = calls.find((c) => c.op === "invite_member");
    ok("a typed invite reaches the API", invited && inv && inv.body.email === "leena@example.com" && inv.body.space_id === "sp1", JSON.stringify(inv));
    ok("no errors", errors.length === 0, errors.join(" | "));
    await page.close();

    signedIn = false;
    const out = await browser.newPage({ viewport: { width: 390, height: 844 } });
    await out.goto(wardenUrl);
    const moved = await out.waitForURL(/\/apps\/warden\/login$/, { timeout: 5000 }).then(() => true, () => false);
    ok("signed out, the page goes to sign in", moved, out.url());
    await out.close();
  } finally {
    api.close();
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
