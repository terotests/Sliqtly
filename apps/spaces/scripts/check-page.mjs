#!/usr/bin/env node
/**
 * npm run spaces:check:page — the built page (npm run spaces:build) in Chrome:
 * it paints at desktop and phone width without an error, the navigation
 * answers a click where it is drawn, and a CSV picked in the file dialog
 * lands in the sheets and the summary.
 *
 * The summary shows the weather (Open-Meteo's answer is stubbed here).
 *
 * Then the Warden page (npm run warden:build) against a stand-in for the
 * server: it reads the space from the API, a typed invite reaches the API,
 * a double-click or a click opens an event in the form and a typed title
 * is saved, Backspace deletes the selected event, the Add menu makes a new entry, the
 * budget is read from the API, a picture picked for the assistant goes to
 * it with the text (made a JPEG) and its answer is shown, and a signed-out
 * answer sends the page to the sign-in page.
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
const eq = (what, got, want) => ok(what, got === want, `got ${JSON.stringify(got)} want ${JSON.stringify(want)}`);

// the summary's weather, answered here instead of by Open-Meteo
const stubWeather = (p) => p.route("https://api.open-meteo.com/**", (r) => r.fulfill({
  contentType: "application/json",
  body: JSON.stringify({ current: { temperature_2m: 6.6, weather_code: 2 },
    daily: { temperature_2m_max: [9.2], temperature_2m_min: [3.8], precipitation_probability_max: [20] } }),
}));

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

// a click on an element of the app's tree, scrolled into the window first
const clickId = async (p, id) => {
  const c = await centre(p, id);
  if (!c) return false;
  const top = await p.evaluate((y) => {
    const want = Math.max(0, y - window.innerHeight / 2);
    window.scrollTo(0, want);
    return window.scrollY;
  }, c[1]);
  await p.mouse.click(c[0], c[1] - top);
  return true;
};

try {
  for (const [w, h, locale, today] of [[1280, 800, "fi-FI", "Tänään"], [390, 844, "en-US", "Today"]]) {
    console.log(`${w}×${h} ${locale}`);
    const page = await browser.newPage({ viewport: { width: w, height: h }, locale });
    await stubWeather(page);
    const errors = [];
    page.on("pageerror", (e) => errors.push(String(e)));
    page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
    await page.goto(base);
    await page.waitForFunction(() => window.__lastList && window.__lastList.cmds.length > 20);
    ok("the summary paints", (await texts(page)).includes(today));
    const wx = await page
      .waitForFunction(() => window.__lastList.cmds.some((c) => c.text === "7°"), null, { timeout: 3000 })
      .then(() => true, () => false);
    ok("with the weather", wx);
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
  // what an assistant added through the MCP server, and the space's stamp
  const elsewhere = [];
  let stamp = "1";
  const iso = (d) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  const space = (extra = {}) => ({
    space_id: "sp1", name: "Home crew", vocab: "family", lang: "en", role: "owner",
    members: [{ uid: "u1", name: "Mia", email: "mia@example.com", role: "owner" }], invites: [],
    wards: [{ id: "w1", name: "Aino", color: "rose" }, { id: "w2", name: "alex", color: "blue" }], ...extra,
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
    calls.push({ op, body: JSON.parse(body || "{}"), type: req.headers["content-type"], client: req.headers["x-warden-client"] });
    const send = (v, status = 200) => res.writeHead(status, { "content-type": "application/json" }).end(JSON.stringify(v));
    if (!signedIn) return send({ error: "sign in", code: "signed-out" }, 401);
    if (op === "list_spaces") return send({ me: { uid: "u1", name: "Mia", email: "mia@example.com" }, spaces: [space()] });
    if (op === "get_space") {
      return send({ space: space(), events: [
        { event_id: "ev1", title: "Dentist", date: iso(new Date()), start: "23:00", end: "23:30", ward: "w1", who: [{ name: "Juho", role: "takes" }] },
        { event_id: "ev2", title: "Camp", date: iso(new Date()), ward: "w1", type: "local_event", fields: { registered: true } },
        ...elsewhere,
      ], changed: stamp });
    }
    if (op === "changes") return send({ changed: stamp, mine: false });
    const last = calls[calls.length - 1].body;
    if (op === "save_event") return send({ ...last, space_id: undefined, event_id: last.event_id || "ev" + calls.length });
    if (op === "delete_event") return send({ deleted: last.event_id });
    if (op === "budget") {
      return send({ from: last.from, to: last.to, total: 42, shared: 42, by_payer: { Mia: 42 }, months: [{ month: last.from, total: 42, shared: 42, by_payer: { Mia: 42 },
        lines: [{ event_id: "ev7", title: "Swimming fee", type: "hobby", date: last.from + "-10", amount: 42, payer: "Mia", scope: "shared", n: 1, of: 1 }] }] });
    }
    if (op === "remove_ward") return send({ ...space({ wards: [{ id: "w1", name: "Aino", color: "rose" }] }), removed_ward: last.ward, events_cleared: 0 });
    if (op === "invite_member") return send(space({ invites: [calls[calls.length - 1].body.email] }));
    if (op === "assist") return send({ space_id: "sp1", text: "Added Leo's swimming on Tuesdays.", changed: true, event_ids: ["ev9"], calls: [{ tool: "import_events", ok: true }] });
    return send({ error: "not in this check" }, 400);
  });
  await new Promise((ok) => api.listen(0, "127.0.0.1", ok));
  const wardenUrl = `http://127.0.0.1:${api.address().port}/apps/warden/`;
  try {
    console.log("Warden 1280×800 en-US");
    const page = await browser.newPage({ viewport: { width: 1280, height: 800 }, locale: "en-US" });
    await stubWeather(page);
    const errors = [];
    page.on("pageerror", (e) => errors.push(String(e)));
    page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
    await page.goto(wardenUrl);
    const shown = await page
      .waitForFunction(() => window.__lastList && window.__lastList.cmds.some((c) => c.text === "Dentist"), null, { timeout: 8000 })
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
    ok("once", calls.filter((c) => c.op === "invite_member").length === 1, JSON.stringify(calls.map((c) => c.op)));
    ok("a typed invite reaches the API", invited && inv && inv.body.email === "leena@example.com" && inv.body.space_id === "sp1", JSON.stringify(inv));

    // a ward added by mistake: Remove, then Remove again in the question
    await clickId(page, "sp-unward-1");
    await page.waitForTimeout(200);
    const wardAsked = (await texts(page)).some((t) => t.startsWith("Remove alex?"));
    await clickId(page, "sp-unward-yes");
    const removed = await page
      .waitForFunction(() => window.__spaces.outCount() === 0 && window.__spaces.space.resources.length === 1, null, { timeout: 5000 })
      .then(() => true, () => false);
    const rw = calls.find((c) => c.op === "remove_ward");
    ok("a ward is removed with two presses", wardAsked && removed && rw && rw.body.ward === "w2", JSON.stringify({ wardAsked, removed, rw }));
    await page.evaluate(() => window.scrollTo(0, 0));

    // a double-click on the calendar opens the event; a typed title is saved
    const calNav = await centre(page, "sp-nav-calendar");
    await page.mouse.click(calNav[0], calNav[1]);
    await page.waitForTimeout(300);
    const bandId = await page.evaluate(() => {
      const a = window.__spaces;
      const k = a.occIds.indexOf(a.space.events.find((e) => e.title === "Camp").id);
      return "spc-band-" + a.boardIds[k];
    });
    const band = await centre(page, bandId);
    ok("the all-day event is drawn on this week", !!band, bandId);
    if (band) {
      await page.mouse.dblclick(band[0], band[1]);
      await page.waitForTimeout(300);
      ok("a double-click opens it in the form", await page.evaluate(() => window.__spaces.editing && window.__spaces.editType === "local_event"));
      await clickId(page, "sp-ed-0");
      await page.waitForTimeout(100);
      await page.keyboard.press("End");
      await page.keyboard.type(" weekend");
      await clickId(page, "sp-ed-save");
      const saved = await page
        .waitForFunction(() => window.__spaces.outCount() === 0 && !window.__spaces.editing, null, { timeout: 5000 })
        .then(() => true, () => false);
      const sv = calls.find((c) => c.op === "save_event");
      ok("the typed title is saved", saved && sv && sv.body.event_id === "ev2" && sv.body.title === "Camp weekend" && sv.body.type === "local_event" && sv.body.fields.registered === true, JSON.stringify(sv));

      // a click opens it too; selected (from the keyboard), Backspace deletes it
      await clickId(page, bandId);
      await page.waitForTimeout(200);
      ok("a click opens it in the form", await page.evaluate(() => window.__spaces.editing && window.__spaces.editId !== ""));
      // Escape closes it (its Cancel sits at the foot of a page taller than
      // the window, where a click can land before the scroll has settled)
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => !window.__spaces.editing, null, { timeout: 3000 }).catch(() => {});
      const before = await page.evaluate(() => ({ editing: window.__spaces.editing, active: document.activeElement && document.activeElement.tagName }));
      // focus and the key in one step: a repaint in between (the page asks
      // for changes every few seconds) would hand the focus back to the DOM's
      await page.evaluate((id) => {
        window.__spaces.focused = id;
        document.dispatchEvent(new KeyboardEvent("keydown", { key: "Backspace", bubbles: true, cancelable: true }));
      }, bandId);
      const deleted = await page
        .waitForFunction(() => window.__spaces.outCount() === 0 && !window.__spaces.space.events.some((e) => e.remoteId === "ev2"), null, { timeout: 5000 })
        .then(() => true, () => false);
      const del = calls.find((c) => c.op === "delete_event");
      ok("Backspace deletes the selected event", deleted && del && del.body.event_id === "ev2", JSON.stringify({ calls: calls.map((c) => c.op), before }));
      ok("with Undo offered", (await texts(page)).includes("Undo"));
    }

    // the Add menu: a new thing, with no date
    await clickId(page, "sp-add");
    await page.waitForTimeout(200);
    const thing = await clickId(page, "sp-addtype-item");
    ok("the Add menu offers a thing", thing);
    if (thing) {
      await page.waitForTimeout(200);
      await clickId(page, "sp-ed-0");
      await page.waitForTimeout(100);
      await page.keyboard.type("Bike helmet");
      await page.keyboard.press("Enter");
      const made = await page
        .waitForFunction(() => window.__spaces.space.events.some((e) => e.title === "Bike helmet" && e.remoteId), null, { timeout: 5000 })
        .then(() => true, () => false);
      const nt = calls.filter((c) => c.op === "save_event").pop();
      ok("Enter saves a new thing", made && nt.body.type === "item" && nt.body.date === "" && nt.body.event_id === "", JSON.stringify(nt));
      ok("once", calls.filter((c) => c.op === "save_event" && c.body.title === "Bike helmet").length === 1, JSON.stringify(calls.map((c) => c.op)));
    }

    // the budget comes from the API
    await clickId(page, "sp-nav-budget");
    const budget = await page
      .waitForFunction(() => window.__lastList && window.__lastList.cmds.some((c) => c.text === "Swimming fee"), null, { timeout: 5000 })
      .then(() => true, () => false);
    ok("the budget is read from the API", budget, JSON.stringify(calls.map((c) => c.op)));

    // the assistant: a picture and a line of text
    await clickId(page, "sp-nav-assistant");
    await page.waitForTimeout(200);
    const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAQAAAAECAIAAAAmkwkpAAAAEElEQVR4nGP4z8AARwzEcQCukw/x0F8jngAAAABJRU5ErkJggg==", "base64");
    await page.setInputFiles("#picture", { name: "letter.png", mimeType: "image/png", buffer: png });
    const picked = await page.waitForFunction(() => window.__spaces.pics.length === 1, null, { timeout: 5000 }).then(() => true, () => false);
    ok("a picked picture is added", picked);
    await clickId(page, "sp-assist-text");
    await page.waitForTimeout(100);
    await page.keyboard.type("Swimming on Tuesdays");
    await page.keyboard.press("Enter");
    const answered = await page
      .waitForFunction(() => window.__lastList && window.__lastList.cmds.some((c) => c.text === "Added Leo's swimming on Tuesdays."), null, { timeout: 5000 })
      .then(() => true, () => false);
    const asked = calls.filter((c) => c.op === "assist").pop();
    ok("the assistant gets the text and a JPEG", asked && asked.body.text === "Swimming on Tuesdays" && asked.body.images.length === 1
      && asked.body.images[0].mimeType === "image/jpeg" && Buffer.from(asked.body.images[0].data, "base64")[0] === 0xff, JSON.stringify(asked && asked.body.text));
    const read = calls.filter((c) => c.op !== "changes");
    ok("its answer is shown and the space read again", answered && read[read.length - 1].op === "get_space", JSON.stringify(read.slice(-3).map((c) => c.op)));
    if (process.env.SPACES_SHOTS) await page.screenshot({ path: path.join(process.env.SPACES_SHOTS, "assistant.png") });
    await page.evaluate(() => { window.__copied = null; navigator.clipboard.writeText = async (t) => { window.__copied = t; }; });
    await clickId(page, "sp-copy-a-0");
    const copied = await page.waitForFunction(() => window.__copied, null, { timeout: 3000 }).then((h) => h.jsonValue(), () => null);
    ok("a press copies the answer", copied === "Added Leo's swimming on Tuesdays.", String(copied));
    await clickId(page, "sp-nav-calendar");
    await clickId(page, "sp-add");
    await page.waitForTimeout(200);
    if (process.env.SPACES_SHOTS) await page.screenshot({ path: path.join(process.env.SPACES_SHOTS, "addmenu.png"), fullPage: true });
    ok("no errors", errors.length === 0, errors.join(" | "));
    await page.close();

    // the tab is in the address: a link opens it, Back returns to it
    console.log("Warden: the address and changes made elsewhere");
    const live = await browser.newPage({ viewport: { width: 1280, height: 800 }, locale: "en-US" });
    await stubWeather(live);
    live.on("pageerror", (e) => errors.push(String(e)));
    await live.goto(wardenUrl + "#calendar");
    const onCal = await live.waitForFunction(() => window.__spaces.tab === "calendar" && !window.__spaces.loading, null, { timeout: 8000 })
      .then(() => true, () => false);
    ok("#calendar opens the calendar", onCal && (await live.evaluate(() => location.hash)) === "#calendar");
    await clickId(live, "sp-nav-members");
    await live.waitForTimeout(200);
    eq("a tab picked is in the address", await live.evaluate(() => location.hash), "#members");
    await live.goBack();
    const back = await live.waitForFunction(() => window.__spaces.tab === "calendar", null, { timeout: 3000 }).then(() => true, () => false);
    ok("Back returns to the calendar", back && (await live.evaluate(() => location.hash)) === "#calendar");
    await live.goForward();
    const fwd = await live.waitForFunction(() => window.__spaces.tab === "members", null, { timeout: 3000 }).then(() => true, () => false);
    ok("and Forward to the members", fwd);

    // an assistant adds an event through the MCP server: the page shows it
    elsewhere.push({ event_id: "ev50", title: "Added elsewhere", date: iso(new Date()), start: "22:00", end: "22:30", ward: "w1" });
    stamp = "2";
    await clickId(live, "sp-nav-home");
    const appeared = await live
      .waitForFunction(() => window.__spaces.space.events.some((e) => e.title === "Added elsewhere"), null, { timeout: 12000 })
      .then(() => true, () => false);
    ok("a change made elsewhere shows up without a reload", appeared, JSON.stringify(calls.slice(-4).map((c) => c.op)));
    eq("on the same tab", await live.evaluate(() => window.__spaces.tab), "home");
    const asks = calls.filter((c) => c.op === "changes");
    ok("the page names itself when it asks", asks.length > 0 && asks.every((c) => /^p[a-z0-9]+$/.test(c.client || "")), JSON.stringify(asks.slice(-1)));
    ok("no errors", errors.length === 0, errors.join(" | "));
    await live.close();

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
