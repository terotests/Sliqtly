#!/usr/bin/env node
/**
 * npm run check:web — build, serve, and drive the page in headless Chromium.
 *
 * Needs `playwright-core` (npm install) and a Chromium: CHROMIUM_PATH, or
 * the one Playwright keeps under PLAYWRIGHT_BROWSERS_PATH. What it checks is
 * whether the page is WIRED — the chrome, the stage, the timeline, typing,
 * presenting, the two exports and a pasted picture — not how it looks.
 * `--shots=DIR` also writes screenshots.
 */
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import http from "node:http";
import zlib from "node:zlib";
import { distDir, log, ensureRanger } from "./lib.mjs";
import { build } from "./build.mjs";

const shotsArg = process.argv.find((a) => a.startsWith("--shots="));
const shots = shotsArg ? path.resolve(shotsArg.split("=")[1]) : "";

function chromiumPath() {
  if (process.env.CHROMIUM_PATH) return process.env.CHROMIUM_PATH;
  const base = process.env.PLAYWRIGHT_BROWSERS_PATH || "/opt/pw-browsers";
  const direct = path.join(base, "chromium");
  if (fs.existsSync(direct) && fs.statSync(direct).isFile()) return direct;
  return undefined;
}

function serve() {
  const types = { ".html": "text/html", ".js": "text/javascript", ".mjs": "text/javascript", ".css": "text/css", ".md": "text/markdown", ".ttf": "font/ttf" };
  const server = http.createServer((req, res) => {
    let rel = decodeURIComponent(new URL(req.url, "http://x").pathname);
    if (rel.endsWith("/")) rel += "index.html";
    const file = path.join(distDir, rel);
    if (!file.startsWith(distDir) || !fs.existsSync(file)) {
      res.writeHead(404);
      res.end();
      return;
    }
    res.writeHead(200, { "content-type": types[path.extname(file)] || "application/octet-stream" });
    fs.createReadStream(file).pipe(res);
  });
  return new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve(server)));
}

/** The names and inflated text of a zip's entries (enough for a .pptx). */
function unzip(buf) {
  const out = new Map();
  let at = 0;
  while (at + 30 <= buf.length && buf.readUInt32LE(at) === 0x04034b50) {
    const method = buf.readUInt16LE(at + 8);
    const size = buf.readUInt32LE(at + 18);
    const nameLen = buf.readUInt16LE(at + 26);
    const extra = buf.readUInt16LE(at + 28);
    const name = buf.toString("utf8", at + 30, at + 30 + nameLen);
    const start = at + 30 + nameLen + extra;
    const raw = buf.subarray(start, start + size);
    out.set(name, method === 8 ? zlib.inflateRawSync(raw).toString("utf8") : raw.toString("utf8"));
    at = start + size;
  }
  return out;
}

let failures = 0;
function check(name, ok, note = "") {
  if (!ok) failures += 1;
  log(`${ok ? "ok  " : "FAIL"} ${name}${note ? `  (${note})` : ""}`);
}

const { chromium } = await import("playwright-core").catch(() => {
  log("playwright-core is not installed: npm install");
  process.exit(1);
});

build();
// a file a chart reads (`"data": {"url": "data/…"}`), served beside the page
fs.mkdirSync(path.join(distDir, "data"), { recursive: true });
fs.writeFileSync(path.join(distDir, "data", "check-sales.csv"), "month,sales,date\nJan,12,2012-01-01\nFeb,30,2012-02-01\nMar,21,2012-03-01\n");
const server = await serve();
const url = `http://127.0.0.1:${server.address().port}/`;
const browser = await chromium.launch({
  executablePath: chromiumPath(),
  args: ["--use-angle=swiftshader", "--enable-unsafe-swiftshader", "--ignore-gpu-blocklist"],
});
try {
  // A phone: the filmstrip swipes, a tap picks a thumbnail, two fingers
  // zoom the slide (clipped to the stage) and a double tap goes back out.
  {
    const ctx = await browser.newContext({ viewport: { width: 390, height: 760 }, hasTouch: true, isMobile: true });
    const phone = await ctx.newPage();
    await phone.goto(url + "?sample=esittely");
    await phone.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    const cdp = await ctx.newCDPSession(phone);
    const touch = (type, pts) => cdp.send("Input.dispatchTouchEvent", { type, touchPoints: pts.map(([x, y], id) => ({ x, y, id })) });
    const lay = () => phone.evaluate(() => JSON.parse(window.__app.layoutJson()));
    let l = await lay();
    const sy = l.thumbs[0][2] + 30;
    await touch("touchStart", [[300, sy]]);
    for (let x = 300; x >= 60; x -= 20) await touch("touchMove", [[x, sy]]);
    await touch("touchEnd", []);
    const l1 = await lay();
    check("a swipe scrolls the filmstrip without picking a slide", l1.thumbs[0][0] > 0 && l1.slide === 0, `first ${l1.thumbs[0][0]}`);
    check("a thumbnail cut by the strip's edge is still drawn", l1.thumbs.some((t) => t[5] === 1), JSON.stringify(l1.thumbs.map((t) => [t[0], t[5]])));
    const th = l1.thumbs.find((t) => !t[5]);
    await touch("touchStart", [[th[1] + 40, th[2] + 20]]);
    await touch("touchEnd", []);
    l = await lay();
    check("a tap on a thumbnail picks its slide", l.slide === th[0], `${l.slide}`);
    const cx = l.clip[0] + l.clip[2] / 2, cy = l.clip[1] + l.clip[3] / 2;
    await touch("touchStart", [[cx - 20, cy], [cx + 20, cy]]);
    for (let d = 30; d <= 80; d += 10) await touch("touchMove", [[cx - d, cy], [cx + d, cy]]);
    await touch("touchEnd", []);
    const l2 = await lay();
    check("a pinch zooms the slide", l2.stage[2] > l.stage[2] * 3, `${l.stage[2].toFixed(2)} → ${l2.stage[2].toFixed(2)}`);
    const clipped = await phone.evaluate(() => JSON.parse(window.__app.stageJson()).list.cmds?.[0]?.k);
    check("the zoomed slide is clipped to the stage", clipped === 4, String(clipped));
    await phone.evaluate(([x, y]) => {
      const c = document.getElementById("c");
      const r = c.getBoundingClientRect();
      for (let k = 0; k < 2; k++) {
        const o = { pointerId: 90 + k, pointerType: "touch", clientX: r.left + x, clientY: r.top + y, bubbles: true, isPrimary: true };
        c.dispatchEvent(new PointerEvent("pointerdown", o));
        c.dispatchEvent(new PointerEvent("pointerup", o));
      }
    }, [cx, cy]);
    const l3 = await lay();
    check("a double tap goes back to the whole slide", Math.abs(l3.stage[2] - l.stage[2]) < 1e-6, `${l3.stage[2].toFixed(2)}`);
    // a little zoom near the slide's left side: on a portrait phone the
    // fitted slide has room beside it, which must not show as a stripe
    await touch("touchStart", [[cx - 120, cy], [cx - 80, cy]]);
    for (let d = 22; d <= 28; d += 2) await touch("touchMove", [[cx - 100 - d, cy], [cx - 100 + d, cy]]);
    await touch("touchEnd", []);
    const l4 = await lay();
    const pageW = await phone.evaluate(() => JSON.parse(window.__app.stageJson()).width);
    const right = l4.stage[0] + l4.stage[2] * pageW;
    check("a small zoom still covers the stage's edges", l4.stage[2] > l.stage[2] * 1.1 && l4.stage[0] <= l4.clip[0] + 0.5 && right >= l4.clip[0] + l4.clip[2] - 0.5,
      `${l4.stage[0].toFixed(1)}..${right.toFixed(1)} vs ${l4.clip[0]}..${l4.clip[0] + l4.clip[2]}, scale ${(l4.stage[2] / l.stage[2]).toFixed(2)}`);
    // the slide is drawn at the scale the clip was worked out from (a scale
    // rounded to 0.01 drew it short of the stage's edge, a stripe that
    // flickered as the zoom changed)
    const meet = await phone.evaluate(() => {
      const L = JSON.parse(window.__app.layoutJson());
      const c = JSON.parse(window.__app.stageJson()).list.cmds[0];
      return [L.stage[0] + (c.x + c.w) * L.stage[2], L.clip[0] + L.clip[2]];
    });
    check("the zoomed slide's clip meets the stage's edge", Math.abs(meet[0] - meet[1]) < 0.05, `${meet[0].toFixed(3)} vs ${meet[1]}`);
    await ctx.close();
  }

  const page = await browser.newPage({ viewport: { width: 1400, height: 820 } });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.addInitScript(() => document.addEventListener("DOMContentLoaded", () => {
    const el = document.getElementById("brandIntro");
    window.__loadScreen = !el.hidden && !!el.querySelector(".name")?.textContent && !document.getElementById("hint");
  }));
  await page.goto(url + "?sample=esittely");
  await page.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
  await page.waitForFunction(() => document.getElementById("brandIntro").hidden, null, { timeout: 20000 }).catch(() => {});
  const loading = await page.evaluate(() => ({ screen: window.__loadScreen === true, gone: document.getElementById("brandIntro").hidden }));
  check("the editor loads behind Sliqtly's logo and name, which then go", loading.screen && loading.gone, JSON.stringify(loading));
  await page.waitForTimeout(500);
  const shot = async (name) => { if (shots) { fs.mkdirSync(shots, { recursive: true }); await page.screenshot({ path: path.join(shots, name) }); } };
  await shot("1-editor.png");

  // The retro skin (File → Settings → Look): its sheets read cleanly, it
  // changes the chrome, its base colour (--retro-hue) re-colours it, and the
  // standard look comes back exactly.
  {
    const r = await page.evaluate(() => {
      const a = window.__app;
      const errs = a.chromeCssErrors();
      // the baseline is a rebuilt bar too: the first build at start-up can
      // differ from any rebuild by a fraction of a pixel
      const k = window.__skin;
      k.set("");
      const std = a.toolbarJson();
      k.set("retro");
      const lime = a.toolbarJson();
      k.hue(200);
      const ice = a.toolbarJson();
      const errs2 = a.chromeCssErrors();
      k.hue(88);
      const lime2 = a.toolbarJson();
      k.set("");
      return { errs: errs + errs2, changed: lime !== std, hue: ice !== lime, hueBack: lime2 === lime, back: a.toolbarJson() === std };
    });
    check("the chrome sheets and skins read without errors", r.errs === "", r.errs);
    check("the retro skin changes the bar", r.changed);
    check("…its base colour re-colours it, and comes back", r.hue && r.hueBack, JSON.stringify({ hue: r.hue, hueBack: r.hueBack }));
    check("the standard look comes back as it was", r.back);
  }

  // The dark look (File → Settings → Look, the bar's 🌙): derived from the
  // light sheets, it reads cleanly, changes the chrome, follows the device
  // under "system", stays out of the retro skin, and the light look comes
  // back exactly.
  {
    const r = await page.evaluate(() => {
      const a = window.__app;
      const k = window.__skin;
      k.set("");
      k.mode("light");
      const light = a.toolbarJson();
      k.mode("dark");
      const dark = a.toolbarJson();
      const errs = a.chromeCssErrors();
      const attr = document.documentElement.dataset.mode;
      k.set("retro");
      const retroDark = a.toolbarJson();
      k.mode("light");
      const retroLight = a.toolbarJson();
      k.set("");
      const back = a.toolbarJson();
      k.mode("system");
      const sys = a.toolbarJson();
      const deviceDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
      k.mode("light");
      return { errs, changed: dark !== light, attr, retro: retroDark === retroLight, back: back === light, sys: sys === (deviceDark ? dark : light) };
    });
    check("the dark look's sheets read without errors", r.errs === "", r.errs);
    check("the dark look changes the bar, and the page around it", r.changed && r.attr === "dark", JSON.stringify(r));
    check("…the retro skin is the same in either", r.retro);
    check("…the light look comes back as it was, and system follows the device", r.back && r.sys, JSON.stringify(r));
  }

  // A window carried by its title bar keeps the skin: the handle is not
  // painted chart-editor.css's white while dragging.
  {
    const r = await page.evaluate(() => {
      const a = window.__app;
      const k = window.__skin;
      k.set("retro");
      a.openDocSettings("");
      a.chartJson();
      const walk = (e, id) => { if ((e.className || "").split(" ").includes(id)) return e; for (const c of e.children || []) { const f = walk(c, id); if (f) return f; } return null; };
      const h = walk(a.chart.host.lastPage, "ui-window-handle");
      const x = h.calculatedX + 40, y = h.calculatedY + 20;
      a.pointerDown(x, y, false, 1);
      a.pointerMove(x + 30, y + 10);
      const json = a.chartJson();
      const dragging = !!walk(a.chart.host.lastPage, "ui-window-handle-dragging");
      a.pointerUp();
      a.key("escape", false, false);
      k.set("");
      return { dragging, white: json.includes("[250,250,250,1.00]") };
    });
    check("a window dragged by its title bar keeps the retro skin", r.dragging && !r.white, JSON.stringify(r));
  }

  // A text field in a window (InputCtl draws its own caret and selection):
  // the caret shows after what was typed, Ctrl+A's band covers exactly the
  // text, a double click takes a word, a triple click all of it, a drag
  // selects. The bug: no caret, and a band that stopped 16px short.
  {
    const r = await page.evaluate(() => {
      const a = window.__app;
      const walk = (e, id) => { if (e.id === id) return e; for (const c of e.children || []) { const f = walk(c, id); if (f) return f; } return null; };
      const el = (id) => { a.chartJson(); return walk(a.chart.host.lastPage, id); };
      const near = (x, y) => Math.abs(x - y) < 1;
      a.openDocSettings("");
      let t = el("ds-tabs-tab-header");
      a.pointerDown(t.calculatedX + 6, t.calculatedY + 6, false, 1); a.pointerUp();
      const f = el("ds-t-2");
      a.pointerDown(f.calculatedX + 20, f.calculatedY + 10, false, 1); a.pointerUp();
      a.text("{page} / {pages}");
      const txt = el("ds-t-2-text"), car = el("ds-t-2-caret");
      const out = { caret: car.calculatedWidth > 0 && car.calculatedHeight > 0 && near(car.calculatedX, txt.calculatedX + txt.calculatedWidth) };
      a.chord("a");
      const band = el("ds-t-2-sel");
      out.band = near(band.calculatedX, txt.calculatedX) && near(band.calculatedWidth, txt.calculatedWidth) && el("ds-t-2-caret").calculatedWidth === 0;
      const ic = a.chart.inputFor("ds-t-2");
      const sel = () => ic.value.slice(ic.selStart(), ic.selEnd());
      const x = (frac) => txt.calculatedX + txt.calculatedWidth * frac, y = txt.calculatedY + 5;
      a.pointerDown(x(0.75), y, false, 2); a.pointerUp();
      out.word = sel();
      a.pointerDown(x(0.75), y, false, 3); a.pointerUp();
      out.all = sel();
      a.pointerDown(txt.calculatedX + 1, y, false, 1); a.pointerMove(x(0.4), y); a.pointerUp();
      out.drag = [ic.selStart(), ic.selEnd()];
      a.key("escape", false, false);
      a.undo();
      return out;
    });
    check("a field's caret shows after the text typed", r.caret, JSON.stringify(r));
    check("…Ctrl+A's band covers exactly the text", r.band, JSON.stringify(r));
    check("…a double click takes the word, a triple click all of it", r.word === "pages" && r.all === "{page} / {pages}", JSON.stringify(r));
    check("…a drag selects from where it was pressed", r.drag[0] === 0 && r.drag[1] > 2 && r.drag[1] < 10, JSON.stringify(r));
  }

  // Text the reader needs elsewhere can be selected and copied (EVGUI
  // TextCtl): About's build line, a toast. A double click takes the build
  // hash, a drag selects, Ctrl+C (the copy event on the page's key field)
  // copies it, Ctrl+X cuts nothing from the editor then, and a toast stays
  // while its text is selected. The bug: drawn text could not be selected.
  {
    const r = await page.evaluate(() => {
      const a = window.__app;
      const walk = (e, id) => { if (e.id === id) return e; for (const c of e.children || []) { const f = walk(c, id); if (f) return f; } return null; };
      const el = (id) => { a.chartJson(); return walk(a.chart.host.lastPage, id); };
      const copy = () => {
        const dt = new DataTransfer();
        document.getElementById("keys").dispatchEvent(new ClipboardEvent("copy", { clipboardData: dt, bubbles: true, cancelable: true }));
        return dt.getData("text/plain");
      };
      a.openAbout("About", "Sliqtly\nBuild e22e565d1e (2026-10-04)");
      const run = el("ce-t-line1-text");
      const y = run.calculatedY + 6;
      const out = {};
      // "Build " is 6 characters: a third of the way in is inside the hash
      a.pointerDown(run.calculatedX + run.calculatedWidth * 0.35, y, false, 2); a.pointerUp();
      out.word = copy();
      out.band = el("ce-t-line1-sel0").calculatedWidth > 0;
      a.pointerDown(run.calculatedX + 1, y, false, 1);
      a.pointerMove(run.calculatedX + run.calculatedWidth + 20, y); a.pointerUp();
      out.drag = copy();
      const src = a.source();
      out.cut = a.cutSelection();
      out.kept = a.source() === src;
      a.key("escape", false, false);
      a.toast("Could not reach the server (503)");
      const pw = (id) => walk(a.panels.page(), id);
      const t = pw("pn-toast-text-text");
      a.pointerDown(t.calculatedX + 2, t.calculatedY + 6, false, 3); a.pointerUp();
      out.toast = copy();
      out.held = a.toastHeld();
      a.pointerDown(300, 300, false, 1); a.pointerUp();
      out.dropped = a.toastHeld() === false;
      a.toast("");
      return out;
    });
    check("About's build hash: a double click takes it and Ctrl+C copies it", r.word === "e22e565d1e" && r.band, JSON.stringify(r));
    check("…a drag over the line copies all of it", r.drag === "Build e22e565d1e (2026-10-04)", JSON.stringify(r));
    check("…Ctrl+X then cuts nothing from the editor", r.cut === "" && r.kept, JSON.stringify(r));
    check("a toast's text can be selected and copied, and stays while selected", r.toast === "Could not reach the server (503)" && r.held && r.dropped, JSON.stringify(r));
  }

  // "Your name" opens with the name selected, so typing replaces it; an
  // emoji typed in one go lands whole. The bug: the window's rebuild made a
  // new field with the caret at the end, and text went in one UTF-16 unit
  // at a time.
  {
    const r = await page.evaluate(() => {
      const a = window.__app;
      a.openAskName("Anonymous Narwhal");
      a.chartJson();
      a.text("Ada");
      a.text("\u{1F44D}\u{1F3FD}");
      const ic = a.chart.inputFor("nd-name");
      const out = { value: ic.value, caret: ic.caret };
      a.key("escape", false, false);
      return out;
    });
    check("\"Your name\" opens with the name selected: typing replaces it", r.value === "Ada\u{1F44D}\u{1F3FD}", JSON.stringify(r));
  }

  // Real keys, not calls: letters arrive through beforeinput, Backspace
  // through keydown, and a composition left open (a dead key, an IME
  // cancelled by a click) must not switch typing off.
  await page.mouse.click(300, 300);
  const len0 = await page.evaluate(() => window.__app.source().length);
  await page.keyboard.type("ab");
  const len1 = await page.evaluate(() => window.__app.source().length);
  await page.keyboard.press("Backspace");
  const len2 = await page.evaluate(() => window.__app.source().length);
  await page.evaluate(() => document.getElementById("keys").dispatchEvent(new CompositionEvent("compositionstart", { data: "" })));
  await page.keyboard.type("cd");
  const len3 = await page.evaluate(() => window.__app.source().length);
  // text that lands in the field without a beforeinput is carried over too
  await page.evaluate(() => {
    const k = document.getElementById("keys");
    const at = k.selectionStart;
    k.value = k.value.slice(0, at) + "e" + k.value.slice(at);
    k.dispatchEvent(new InputEvent("input", { inputType: "insertText", data: "e" }));
  });
  const len4 = await page.evaluate(() => window.__app.source().length);
  check("typed letters reach the editor", len1 === len0 + 2, `${len0} → ${len1}`);
  check("backspace deletes one", len2 === len1 - 1, `${len1} → ${len2}`);
  check("typing works after a composition that never ended", len3 === len2 + 2, `${len2} → ${len3}`);
  check("text that bypassed beforeinput is carried over", len4 === len3 + 1, `${len3} → ${len4}`);
  await page.keyboard.press("Backspace");
  await page.keyboard.press("Backspace");
  await page.keyboard.press("Backspace");
  await page.keyboard.press("Backspace");
  check("and the document is back as it was", (await page.evaluate(() => window.__app.source().length)) === len0);

  // Ctrl + plus / minus / 0 in the editor size its text, not the page, and
  // the size is kept; Ctrl + wheel over the editor does the same
  {
    const size = () => page.evaluate(() => window.__app.editorFontSize());
    const s0 = await size();
    await page.keyboard.press("Control+Equal");
    await page.keyboard.press("Control+Equal");
    const s1 = await size();
    await page.keyboard.press("Control+Minus");
    const s2 = await size();
    const kept = await page.evaluate(() => localStorage.getItem("sliqtly.editorFontSize"));
    await page.mouse.move(300, 300);
    await page.keyboard.down("Control");
    await page.mouse.wheel(0, -100);
    await page.keyboard.up("Control");
    const s3 = await size();
    await page.keyboard.press("Control+0");
    const s4 = await size();
    const zoom = await page.evaluate(() => window.visualViewport ? window.visualViewport.scale : 1);
    check("Ctrl + plus makes the editor's text bigger", s1 === s0 + 2, `${s0} → ${s1}`);
    check("…Ctrl + minus smaller", s2 === s1 - 1, `${s1} → ${s2}`);
    check("…the size is kept by the browser", kept === String(s2), String(kept));
    check("…a Ctrl + wheel notch over the editor is one step", s3 === s2 + 1, `${s2} → ${s3}`);
    check("…Ctrl + 0 goes back to the default", s4 === s0 && zoom === 1, `${s4}`);
  }

  // ⌃⌘Space (Ctrl+Shift+Space off a Mac): EVGUI's emoji picker at the
  // caret. Typing searches, Enter writes the emoji where the caret is (one
  // edit: Ctrl+Z takes it back), Esc closes it, a click on a cell picks it.
  {
    const src0 = await page.evaluate(() => window.__app.source());
    await page.keyboard.press("Control+Shift+Space");
    const open = await page.evaluate(() => window.__app.emojiIsOpen());
    await page.keyboard.type("thumbs up");
    await page.waitForTimeout(100);
    await shot("emoji-picker.png");
    const kept = await page.evaluate(() => window.__app.source());
    await page.keyboard.press("Enter");
    const src1 = await page.evaluate(() => window.__app.source());
    const at = [...src0].findIndex((c, i) => c !== [...src1][i]);
    const closed = !(await page.evaluate(() => window.__app.emojiIsOpen()));
    check("the emoji key opens the picker", open);
    check("…typing goes to its search, not the text", kept === src0);
    check("…Enter writes the emoji at the caret and closes it", src1.length === src0.length + "👍️".length && src1.includes("👍️") && closed, JSON.stringify(src1.slice(Math.max(0, at - 5), at + 8)));
    check("…and the caret is after it", await page.evaluate(() => { const a = window.__app; return a.currentLine().slice(0, a.caretCol()).endsWith("👍️"); }));
    check("…Recent is kept by the browser", (await page.evaluate(() => localStorage.getItem("sliqtly.emoji.recent"))) === "👍️");
    await page.keyboard.press("Control+z");
    check("…Ctrl+Z takes it back", (await page.evaluate(() => window.__app.source())) === src0);
    await page.keyboard.press("Control+Shift+Space");
    await page.keyboard.press("ArrowRight");
    await page.keyboard.press("Escape");
    check("Esc closes the picker and writes nothing", !(await page.evaluate(() => window.__app.emojiIsOpen())) && (await page.evaluate(() => window.__app.source())) === src0);
    await page.keyboard.press("Control+Shift+Space");
    await page.evaluate(() => window.__app.hintJson());
    const cell = await page.evaluate(() => {
      const walk = (e) => { if (e.id === "hp-emoji-e-0") return e; for (const c of e.children || []) { const f = walk(c); if (f) return f; } return null; };
      const el = walk(window.__app.hint.host.lastPage);
      return el ? [el.calculatedX + el.calculatedWidth / 2, el.calculatedY + el.calculatedHeight / 2, window.__app.hint.emoji.emojiAt(0)] : null;
    });
    const r = await page.evaluate(() => document.getElementById("c").getBoundingClientRect().toJSON());
    if (cell) await page.mouse.click(r.x + cell[0], r.y + cell[1]);
    const src2 = await page.evaluate(() => window.__app.source());
    check("a click on an emoji writes it", !!cell && src2.length === src0.length + cell[2].length && src2.includes(cell[2]), JSON.stringify(cell));
    await page.keyboard.press("Control+z");
    check("…and the document is back as it was", (await page.evaluate(() => window.__app.source())) === src0);
  }

  const r = await page.evaluate(() => {
    const a = window.__app;
    const out = {};
    const texts = (doc) => doc.list.cmds.filter((c) => c.k === 3).map((c) => c.text);
    const chrome = JSON.parse(a.chromeJson());
    out.chromeTracks = texts(chrome).includes("Text") && texts(chrome).includes("Animation");
    out.editorText = texts(chrome).some((t) => t.startsWith("# Gemini"));
    out.slides = a.deck.slideCount();
    const stage = JSON.parse(a.stageJson());
    out.stageTitle = texts(stage).includes("Gemini-botti palaverissa");
    out.stageFx = (stage.list.effects || []).map((e) => e.kind).join(",");
    // slide 2 before and after its first build step
    a.selectSlide(1);
    a.localT = 0.2;
    out.beforeStep = texts(JSON.parse(a.stageJson())).some((t) => t.startsWith("Liittyy"));
    a.localT = -1;
    out.afterAll = texts(JSON.parse(a.stageJson())).filter((t) => /^\d\.$/.test(t)).length;
    // the same time is the same frame
    out.deterministic = a.frameAtJson(9.3) === a.frameAtJson(9.3) && a.frameAtJson(9.3) !== a.frameAtJson(4.0);
    // typing a new slide at the end
    a.setFocus("editor");
    a.key("pageDown", false, true);
    for (let i = 0; i < 400; i += 1) a.key("down", false, false);
    a.key("end", false, false);
    a.text("\n\n## Uusi dia {transition=slide}\n\nKirjoitettu testissä\n");
    out.slidesAfterTyping = a.deck.slideCount();
    out.selectedAfterTyping = a.slideShown();
    // presenting: steps by click, then the next slide
    a.present(true);
    a.takeRequest();
    out.presentMode = JSON.parse(a.layoutJson()).mode;
    a.next();
    a.next();
    out.afterTwoClicks = a.slideShown();
    a.next();
    out.stepOnSecond = a.pStep;
    a.setTime(1000);
    out.heldAt = a.currentTime();
    // held, frame after frame as the host asks: the deck clock stays and the
    // slide's effect goes on
    const fx0 = JSON.parse(a.layoutJson()).fxTime;
    for (let i = 0; i < 10; i += 1) a.setTime(a.currentTime() + 0.1);
    out.heldStill = a.currentTime() === out.heldAt;
    out.fxOnBy = JSON.parse(a.layoutJson()).fxTime - fx0;
    a.key("escape", false, false);
    out.backToEdit = JSON.parse(a.layoutJson()).mode;
    return out;
  });
  check("chrome draws the three tracks", r.chromeTracks);
  check("editor draws the markdown", r.editorText);
  check("the sample is six slides", r.slides === 6, String(r.slides));
  check("stage draws slide 1", r.stageTitle);
  check("slide 1 carries its starfield", r.stageFx === "starfield", r.stageFx);
  check("a build item is hidden before its step", !r.beforeStep);
  check("all four numbers at the end", r.afterAll === 4, String(r.afterAll));
  check("a frame is a function of its time", r.deterministic);
  check("typing a heading makes a slide", r.slidesAfterTyping === 7, String(r.slidesAfterTyping));
  check("the stage follows the caret to it", r.selectedAfterTyping === 6, String(r.selectedAfterTyping));
  check("present mode", r.presentMode === "present");
  check("title slide has no steps: two clicks reach slide 2's first step", r.afterTwoClicks === 1, String(r.afterTwoClicks));
  check("a click releases a build step", r.stepOnSecond === 2, String(r.stepOnSecond));
  check("the clock holds at the next step", r.heldAt < 1000, String(r.heldAt));
  check("and the slide's effect goes on while it holds", r.heldStill && Math.abs(r.fxOnBy - 1) < 1e-6, String(r.fxOnBy));
  check("escape ends the presentation", r.backToEdit === "edit");

  // Past the last slide a panel offers the first slide, the one before, or out.
  {
    const toEnd = () => page.evaluate(() => {
      const a = window.__app;
      for (let i = 0; i < 200 && !a.atEnd(); i += 1) a.next();
      return a.atEnd();
    });
    const state = () => page.evaluate(() => ({
      shown: !document.getElementById("endPanel").hidden,
      end: window.__app.atEnd(),
      slide: window.__app.slideShown(),
      count: window.__app.deck.slideCount(),
      mode: JSON.parse(window.__app.layoutJson()).mode,
    }));
    await page.evaluate(() => { window.__app.present(true); window.__app.takeRequest(); });
    await page.waitForTimeout(100);
    const before = await state();
    const reached = await toEnd();
    await page.waitForFunction(() => !document.getElementById("endPanel").hidden, null, { timeout: 8000 }).catch(() => {});
    const atEnd = await state();
    await page.click("#endPrev");
    const prev = await state();
    await toEnd();
    await page.waitForFunction(() => !document.getElementById("endPanel").hidden, null, { timeout: 8000 }).catch(() => {});
    await page.click("#endRestart");
    const restart = await state();
    await toEnd();
    await page.waitForFunction(() => !document.getElementById("endPanel").hidden, null, { timeout: 8000 }).catch(() => {});
    // a finger sideways on the slide: right for the slide before (from the
    // end panel too), left for the next
    const swipe = (dx) => page.evaluate((dx) => {
      const a = window.__app;
      const w = innerWidth / 2, h = innerHeight / 2;
      a.pointerDown(w, h, false, 1);
      a.pointerMove(w + dx / 2, h + 4);
      a.pointerMove(w + dx, h + 6);
      a.pointerUp();
      return { slide: a.slideShown(), end: a.atEnd() };
    }, dx);
    const swipedBack = await swipe(120);
    const swipedOn = await swipe(-120);
    await toEnd();
    await page.waitForFunction(() => !document.getElementById("endPanel").hidden, null, { timeout: 8000 }).catch(() => {});
    await page.click("#endExit");
    await page.waitForTimeout(100);
    const exit = await state();
    check("a swipe right goes back a slide, a swipe left on", !swipedBack.end && swipedBack.slide === prev.count - 2 && swipedOn.slide === prev.count - 1, JSON.stringify({ swipedBack, swipedOn }));
    check("the end panel waits until the last slide is passed", !before.shown && reached && atEnd.shown && atEnd.slide === atEnd.count - 1, JSON.stringify({ before, atEnd }));
    check("end panel: Previous slide, From the start, Exit", !prev.shown && prev.slide === prev.count - 2 && restart.slide === 0 && !restart.end && exit.mode === "edit" && !exit.shown, JSON.stringify({ prev, restart, exit }));
    await page.evaluate(() => { while (window.__app.takeRequest()); document.body.classList.remove("presenting"); });
  }

  await page.evaluate(() => { window.__app.present(true); window.__app.takeRequest(); window.__app.speaker = true; window.__app.next(); });
  await page.waitForTimeout(300);
  await shot("2-speaker.png");
  await page.evaluate(() => window.__app.endPresent());

  // The "+" after the last thumbnail: pressed, it offers an empty slide or a
  // copy of the selected one, each put after the selected slide and selected.
  {
    const src0 = await page.evaluate(() => window.__app.source());
    const at = async (part) => page.evaluate((part) => {
      const a = window.__app;
      a.selectSlide(1);
      a.scrollStrip(1e6);
      a.layoutJson();
      const c = document.getElementById("c").getBoundingClientRect();
      const x = a.thumbX(a.deck.slideCount()) + a.thumbW / 2;
      const y = a.thumbY() + (part === "dup" ? a.thumbH * 0.75 : a.thumbH * 0.25);
      return [c.left + x, c.top + y];
    }, part);
    const state = () => page.evaluate(() => {
      const a = window.__app;
      return { n: a.deck.slideCount(), sel: a.selected, open: a.addOpen, sel1: a.deck.slideAt(1).title, title: a.deck.slideAt(a.selected).title, words: a.copySelection() };
    });
    const s0 = await state();
    await page.evaluate(() => { window.__app.selectSlide(1); window.__app.scrollStrip(1e6); });
    await page.waitForTimeout(300);
    await shot("slide-add.png");
    let p = await at("open");
    await page.mouse.click(p[0], p[1]);
    const s1 = await state();
    check("the + after the thumbnails offers its two choices", s1.open && s1.n === s0.n, JSON.stringify(s1));
    await shot("slide-add-open.png");
    p = await at("empty");
    await page.mouse.click(p[0], p[1]);
    const s2 = await state();
    check("…Empty slide puts a new slide after the selected one and selects its heading", !s2.open && s2.n === s0.n + 1 && s2.sel === 2 && s2.title === "New slide" && s2.words === "New slide", JSON.stringify(s2));
    p = await at("open");
    await page.mouse.click(p[0], p[1]);
    p = await at("dup");
    await page.mouse.click(p[0], p[1]);
    const s3 = await state();
    check("…Duplicate slide copies the selected slide after it", s3.n === s0.n + 2 && s3.sel === 2 && s3.title === s3.sel1, JSON.stringify(s3));
    p = await at("open");
    await page.mouse.click(p[0], p[1]);
    await page.mouse.click(700, 200);
    check("…a press elsewhere closes the choices", !(await state()).open);
    await page.evaluate((t) => window.__app.setSource(t), src0);
    await page.waitForTimeout(200);
  }

  // The filmstrip takes the keyboard: a click on a thumbnail puts the keys on
  // that slide (not in the editor), Delete deletes the slide and Ctrl+Z brings
  // it back, Ctrl+arrows and a drag move it, a right click (or Shift+F10)
  // opens its menu.
  {
    const src0 = await page.evaluate(() => window.__app.source());
    const md = "# A\n\naa\n\n# B\n\nbb\n\n# C\n\ncc\n\n# D\n\ndd\n";
    await page.evaluate((t) => { window.__app.setSource(t); window.__app.scrollStrip(-1e6); }, md);
    await page.waitForTimeout(200);
    const thumbAt = (i, fy = 0.5) => page.evaluate(([i, fy]) => {
      const a = window.__app;
      a.layoutJson();
      const c = document.getElementById("c").getBoundingClientRect();
      return [c.left + a.thumbX(i) + a.thumbW / 2, c.top + a.thumbY() + a.thumbH * fy];
    }, [i, fy]);
    const st = () => page.evaluate(() => {
      const a = window.__app;
      const el = document.activeElement;
      const titles = [];
      for (let k = 0; k < a.deck.slideCount(); k += 1) titles.push(a.deck.slideAt(k).title);
      return { order: titles.join(""), sel: a.selected, focus: a.focusTarget(), on: el && el.dataset ? el.dataset.a11yId || el.id : "", role: el ? el.getAttribute("role") : "", aria: el ? el.getAttribute("aria-selected") : "", menu: a.toolbar.openMenu(), src: a.source() };
    });
    await page.mouse.click(300, 300);
    let p = await thumbAt(1);
    await page.mouse.click(p[0], p[1]);
    let s1 = await st();
    check("a click on a thumbnail moves the keyboard to that slide of the strip", s1.focus === "strip" && s1.sel === 1 && s1.on === "thumb-1" && s1.role === "option" && s1.aria === "true", JSON.stringify({ ...s1, src: "" }));
    await shot("strip-focus.png");
    await page.keyboard.press("q");
    check("…a letter typed there does not reach the editor", (await st()).src === md);
    await page.keyboard.press("Delete");
    const s2 = await st();
    check("…Delete deletes the slide, not text in the editor", s2.order === "ACD" && s2.src === "# A\n\naa\n\n# C\n\ncc\n\n# D\n\ndd\n" && s2.focus === "strip" && s2.on === "thumb-1", JSON.stringify(s2));
    await page.keyboard.press("Control+z");
    const s3 = await st();
    check("…and Ctrl+Z brings it back", s3.src === md && s3.order === "ABCD", JSON.stringify(s3));
    await page.keyboard.press("ArrowRight");
    const s4 = await st();
    check("…the arrows move between slides, the keyboard with them", s4.sel === 2 && s4.on === "thumb-2", JSON.stringify({ ...s4, src: "" }));
    await page.keyboard.press("Control+ArrowLeft");
    const s5 = await st();
    check("…Ctrl+arrow moves the slide, as one edit", s5.order === "ACBD" && s5.sel === 1 && s5.src === "# A\n\naa\n\n# C\n\ncc\n\n# B\n\nbb\n\n# D\n\ndd\n" && s5.on === "thumb-1", JSON.stringify(s5));
    await page.keyboard.press("Control+ArrowRight");
    await page.keyboard.press("Control+ArrowRight");
    const s5b = await st();
    check("…to the end too", s5b.order === "ABDC" && s5b.sel === 3 && s5b.src === "# A\n\naa\n\n# B\n\nbb\n\n# D\n\ndd\n\n# C\n\ncc\n", JSON.stringify(s5b));
    await page.keyboard.press("Control+z");
    await page.keyboard.press("Control+z");
    await page.keyboard.press("Control+z");
    check("…each move undoes as one", (await st()).src === md);

    // the context menu, from a right click
    p = await thumbAt(3);
    await page.mouse.click(p[0], p[1], { button: "right" });
    await page.waitForTimeout(100);
    const s6 = await st();
    const rows = await page.evaluate(() => {
      const a = window.__app;
      a.toolbarJson();
      const pg = a.toolbar.host.lastPage;
      const find = (el, id) => { if (el.id === id) return el; for (const c of el.children) { const f = find(c, id); if (f) return f; } return null; };
      const out = {};
      for (const v of ["slideNew", "slideDuplicate", "slideLeft", "slideRight", "slideDelete"]) {
        const e = find(pg, "tb-m-ctx-item-" + v);
        out[v] = e ? [e.calculatedX + e.calculatedWidth / 2, e.calculatedY + e.calculatedHeight / 2, e.className.includes("disabled")] : null;
      }
      const card = find(pg, "tb-m-ctx-content");
      out.card = card ? card.calculatedWidth : -1;
      return out;
    });
    check("a right click on a thumbnail opens its menu, the keyboard in it", s6.menu === "tb-m-ctx" && s6.sel === 3 && /^tb-m-ctx-item-/.test(s6.on) && rows.slideNew && rows.slideRight && rows.slideRight[2] && !rows.slideLeft[2], JSON.stringify({ ...s6, src: "", rows }));
    // the card is as wide as its rows (210px and its chrome), not the window
    check("…its card is as wide as its rows, not the window", rows.card >= 210 && rows.card < 400, String(rows.card));
    await shot("strip-menu.png");
    const cr = await page.evaluate(() => { const c = document.getElementById("c").getBoundingClientRect(); return [c.left, c.top]; });
    await page.mouse.click(cr[0] + rows.slideLeft[0], cr[1] + rows.slideLeft[1]);
    const s7 = await st();
    check("…Move left moves it, and the keyboard is back on the slide", s7.order === "ABDC" && s7.sel === 2 && s7.menu === "" && s7.focus === "strip" && s7.on === "thumb-2", JSON.stringify({ ...s7, src: "" }));
    await page.keyboard.press("Shift+F10");
    const s8 = await st();
    await page.keyboard.press("Escape");
    const s9 = await st();
    check("…Shift+F10 opens it from the keyboard, Esc closes it back to the slide", s8.menu === "tb-m-ctx" && /^tb-m-ctx-item-/.test(s8.on) && s9.menu === "" && s9.on === "thumb-2", JSON.stringify([s8.on, s9.on, s9.menu]));
    await page.evaluate((t) => window.__app.setSource(t), md);

    // a drag puts the slide in another place
    p = await thumbAt(0);
    const q = await thumbAt(2);
    await page.mouse.move(p[0], p[1]);
    await page.mouse.down();
    for (let k = 1; k <= 10; k += 1) await page.mouse.move(p[0] + (q[0] + 70 - p[0]) * k / 10, p[1]);
    const mid = await page.evaluate(() => [window.__app.stripReorder, window.__app.stripDrop]);
    await page.mouse.up();
    const s10 = await st();
    check("a thumbnail dragged to another gap moves its slide there", mid[0] && mid[1] === 3 && s10.order === "BCAD" && s10.sel === 2 && s10.src === "# B\n\nbb\n\n# C\n\ncc\n\n# A\n\naa\n\n# D\n\ndd\n", JSON.stringify({ mid, ...s10 }));
    await page.keyboard.press("Escape");
    check("Esc on the strip gives the keyboard back to the editor", (await st()).focus === "editor");
    await page.evaluate((t) => window.__app.setSource(t), src0);
    await page.waitForTimeout(200);
  }

  // A diagram that asks: present the Kulku slide, wait for the question,
  // move the highlight with an arrow, take it with Enter, then go back two
  // steps with two quick Backspaces and see the question again.
  await page.evaluate(() => window.__app.selectSlide(3));
  await page.keyboard.press("Shift+F5");
  // no tour of its own: the whole diagram, and nothing to answer
  await page.waitForTimeout(1500);
  const still = await page.evaluate(() => { const a = window.__app; const d = a.deck.slideAt(3).diagrams[0].diagram; return { asking: a.deck.askingAt(3, a.stageTime()), tour: d.tour, can: d.canTour }; });
  check("a diagram does not tour on its own: the whole of it, no question", still.asking < 0 && !still.tour && still.can, JSON.stringify(still));
  const playAt = () => page.evaluate(() => { const a = window.__app; const u = a.deck.slideAt(3).diagrams[0]; return a.deck.diagramHit(3, a.stageTime(), u.bx + u.bw - 188, u.by + u.bh - 24); });
  // the buttons are hidden until the pointer moves over the diagram
  const hiddenHit = await playAt();
  const playXY = await page.evaluate(() => {
    const a = window.__app;
    const u = a.deck.slideAt(3).diagrams[0];
    const [sx, sy, sc] = JSON.parse(a.layoutJson()).stage;
    const c = document.getElementById("c").getBoundingClientRect();
    return [c.left + sx + (u.bx + u.bw - 188) * sc, c.top + sy + (u.by + u.bh - 24) * sc];
  });
  await page.mouse.move(playXY[0] - 30, playXY[1] - 30);
  await page.mouse.move(playXY[0], playXY[1], { steps: 4 });
  await page.waitForTimeout(300);
  const playHit = await playAt();
  check("the diagram's buttons are hidden until the pointer comes, then the ▶ beside the zoom buttons is the tour", hiddenHit !== "0:tour" && playHit === "0:tour", JSON.stringify([hiddenHit, playHit]));
  // T starts the tour
  await page.keyboard.press("t");
  await page.waitForFunction(() => window.__app.deck.askingAt(3, window.__app.stageTime()) >= 0, null, { timeout: 15000 }).catch(() => {});
  const q = () => page.evaluate(() => {
    const a = window.__app;
    const d = a.deck.slideAt(3).diagrams[0].diagram;
    return { asking: a.deck.askingAt(3, a.stageTime()), sel: d.optSel, choices: d.choices.length, slide: a.slideShown() };
  });
  const q0 = await q();
  check("the walk stops at the branch and asks", q0.asking === 0 && q0.slide === 3, JSON.stringify(q0));
  await shot("4-question.png");
  await page.keyboard.press("ArrowRight");
  const q1 = await q();
  check("an arrow moves the highlight, not the slide", q1.sel === 1 && q1.slide === 3, JSON.stringify(q1));
  await page.keyboard.press("Enter");
  const q2 = await q();
  check("Enter takes the highlighted way", q2.choices === 1 && q2.asking < 0, JSON.stringify(q2));
  await page.waitForTimeout(1600);
  await page.keyboard.press("Backspace");
  await page.keyboard.press("Backspace");
  const q3 = await q();
  check("two quick backs undo the choice", q3.choices === 0, JSON.stringify(q3));
  await page.waitForFunction(() => window.__app.deck.askingAt(3, window.__app.stageTime()) >= 0, null, { timeout: 8000 }).catch(() => {});
  check("and the question is asked again", (await q()).asking === 0);
  await page.evaluate(() => { window.__app.endPresent(); window.__app.takeRequest(); document.body.classList.remove("presenting"); });

  // The CSS tab: the theme is edited in place and the slides follow; the
  // Markdown tab brings the document back, untouched
  const tabbed = await page.evaluate(() => {
    const a = window.__app;
    const src0 = a.source();
    a.selectSlide(1);
    a.localT = -1;
    const size = () => (JSON.parse(a.stageJson()).list.cmds.find((c) => c.k === 3 && c.text && c.text.startsWith("Miten botti")) || {}).size;
    const before = size();
    a.showTab("css");
    a.setFocus("editor");
    a.key("pageDown", false, true);
    for (let i = 0; i < 200; i += 1) a.key("down", false, false);
    a.key("end", false, false);
    a.text("\nh2 { font-size: 18pt }\n");
    const after = size();
    const css = a.themeCss();
    a.showTab("md");
    return { before, after, css: css.includes("18pt"), same: a.source() === src0, tab: a.editorTab() };
  });
  check("the CSS tab edits the theme and the slide follows", tabbed.css && tabbed.after < tabbed.before, JSON.stringify(tabbed));
  check("the Markdown tab brings the document back", tabbed.same && tabbed.tab === "md");
  await page.evaluate(() => { const sel = document.getElementById("theme"); sel.value = "aurora"; sel.dispatchEvent(new Event("change")); });

  // Themes: a dark one with its own effect, and a light one whose diagram
  // draws dark lines.
  const th = await page.evaluate(() => {
    const sel = document.getElementById("theme");
    const a = window.__app;
    sel.value = "nebula";
    sel.dispatchEvent(new Event("change"));
    a.selectSlide(0);
    a.localT = -1;
    const fx = (JSON.parse(a.stageJson()).list.effects || []).map((e) => e.kind).join(",");
    sel.value = "corporate";
    sel.dispatchEvent(new Event("change"));
    const d = a.deck.slideAt(3).diagrams[0].diagram;
    const light = d.light;
    sel.value = "aurora";
    sel.dispatchEvent(new Event("change"));
    return { fx, light };
  });
  check("the nebula theme brings its starfield", th.fx.includes("starfield"), th.fx);
  check("a diagram on a light theme uses the light palette", th.light === true);

  // Share: the markdown goes into the link, and the link opens it again.
  await page.evaluate(() => window.__app.setSource(window.__app.source() + "\n\n## Jaettu dia\n\nÄäkkösiä ja 👀\n"));
  await page.evaluate(() => document.getElementById("share").click());
  await page.waitForFunction(() => !!window.__lastShare, null, { timeout: 5000 }).catch(() => {});
  const shared = await page.evaluate(() => ({ url: window.__lastShare || "", src: window.__app.source() }));
  check("share puts the document in the URL", /#md=/.test(shared.url), `${shared.url.length} chars`);
  const page2 = await browser.newPage({ viewport: { width: 1200, height: 760 } });
  await page2.goto(shared.url.replace(/^https?:\/\/[^/]+/, url.replace(/\/$/, "")));
  await page2.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
  const reopened = await page2.evaluate(() => window.__app.source());
  check("the shared link opens the same markdown", reopened === shared.src);
  // a picture added to the opened link is kept: once saved, the address
  // loses the link, so a reload opens the saved deck and not the link again
  {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "evgp-link-"));
    fs.writeFileSync(path.join(dir, "link-pic.png"), Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64"));
    await page2.setInputFiles("#fileadd", [path.join(dir, "link-pic.png")]);
    await page2.waitForTimeout(1800);
    const before = await page2.evaluate(() => location.href);
    await page2.reload();
    await page2.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    await page2.waitForTimeout(500);
    await page2.evaluate(() => window.__app.showTab("files"));
    await page2.waitForFunction(() => window.__app.panels.filesJson.includes("media/link-pic.png"), null, { timeout: 8000 }).catch(() => {});
    const after = await page2.evaluate(() => window.__app.panels.filesJson);
    check("a picture added to a shared link's deck survives a reload", !/#md=/.test(before) && after.includes("media/link-pic.png"), before.slice(0, 80));
  }
  await page2.close();
  // …and the presentation link: straight into the show, no toolbar, and Esc
  // does not lead back to an editor
  const showUrl = await page.evaluate(() => window.__lastShareShow || "");
  const dlg = await page.evaluate(() => {
    const a = window.__app;
    const open = a.shareIsOpen();
    const drawn = JSON.parse(a.panelsJson() || "{\"list\":{\"cmds\":[]}}").list.cmds.length;
    const walk = (e, id) => { if (e.id === id) return e; for (const k of e.children || []) { const r = walk(k, id); if (r) return r; } return null; };
    const b = walk(a.panels.host.lastPage, "pn-copy-show");
    a.pointerDown(b.calculatedX + 8, b.calculatedY + 8, false, 1);
    a.pointerUp();
    const req = a.takeRequest();
    a.pointerDown(4, 4, false, 1);
    a.pointerUp();
    return { open, drawn, req, closed: !a.shareIsOpen(), html: !!document.getElementById("shareDlg") };
  });
  check("share offers a presentation link", /mode=show/.test(showUrl));
  check("the share dialog is drawn on the canvas; a copy button asks the page to copy", dlg.open && dlg.drawn > 20 && dlg.req === "copy:show" && dlg.closed && !dlg.html, JSON.stringify(dlg));

  // Slides picked on the strip (Ctrl/⌘ + click, Shift + click): exported
  // alone, and shared as a link to only them (src/PresPick.rgr)
  {
    const src0 = await page.evaluate(() => window.__app.source());
    const picked = await page.evaluate(() => {
      const a = window.__app;
      a.setSource("---\nslide-split-level: 2\n---\n\n## Yksi\n\na\n\n## Kaksi\n\nb\n\n## Kolme\n\nc\n\n## Neljä\n\nd\n");
      a.place();
      const tap = (i, ctrl, shift) => {
        a.setCtrl(ctrl);
        a.pointerDown(a.thumbX(i) + 20, a.thumbY() + 20, shift, 1);
        a.pointerUp();
        a.setCtrl(false);
      };
      tap(1, false, false);
      tap(3, true, false);
      const two = a.pickList();
      // from the last one clicked
      tap(0, false, true);
      const run = a.pickList();
      tap(2, true, false);
      const out = { two, run, list: a.pickList(), bar: a.toolbar.picked, keys: a.pickKeys() };
      const u = new Uint8Array(a.pdfPicked());
      let pdf = "";
      for (let i = 0; i < u.length; i += 1) pdf += String.fromCharCode(u[i]);
      out.pdfPages = (pdf.match(/\/Type\s*\/Page[^s]/g) || []).length;
      const v = new Uint8Array(a.pptxPicked());
      let b = "";
      for (let i = 0; i < v.length; i += 1) b += String.fromCharCode(v[i]);
      out.pptx = btoa(b);
      out.view = a.pickViewText();
      return out;
    });
    const pptxNames = [...unzip(Buffer.from(picked.pptx, "base64")).keys()].filter((k) => /^ppt\/slides\/slide\d+\.xml$/.test(k));
    check("Ctrl+click picks the selected slide and the clicked one; Shift+click picks a run", picked.two === "2,4" && picked.run === "1,2,3,4", JSON.stringify(picked).slice(0, 200));
    check("…Ctrl+click again lets one go; the bar's Export offers the picked", picked.list === "1,2,4" && picked.bar === 3 && picked.keys === "yksi,kaksi,neljä", JSON.stringify({ list: picked.list, bar: picked.bar, keys: picked.keys }));
    check("…the PDF and the PPTX of the picked slides have only them", picked.pdfPages === 3 && pptxNames.length === 3, JSON.stringify({ pdf: picked.pdfPages, pptx: pptxNames }));
    check("…their view keeps only their sections", picked.view.includes("## Yksi") && picked.view.includes("## Kaksi") && picked.view.includes("## Neljä") && !picked.view.includes("## Kolme"), picked.view);
    await page.evaluate(() => { window.__lastShareView = ""; document.getElementById("share").click(); });
    await page.waitForFunction(() => !!window.__lastShareView, null, { timeout: 5000 }).catch(() => {});
    const viewUrl = await page.evaluate(() => window.__lastShareView || "");
    // drawn on the next frame
    await page.waitForFunction(() => !!window.__app.panels.host.lastPage, null, { timeout: 5000 }).catch(() => {});
    await page.waitForTimeout(200);
    const viewDlg = await page.evaluate(() => {
      const a = window.__app;
      const walk = (e, id) => { if (!e) return null; if (e.id === id) return e; for (const k of e.children || []) { const r = walk(k, id); if (r) return r; } return null; };
      const b = walk(a.panels.host.lastPage, "pn-copy-view");
      if (!b) return { button: false };
      a.pointerDown(b.calculatedX + 8, b.calculatedY + 8, false, 1);
      a.pointerUp();
      const req = a.takeRequest();
      a.closeShare();
      return { button: true, req };
    });
    check("…Share offers a link to only the picked slides", /#md=/.test(viewUrl) && /mode=show/.test(viewUrl) && viewDlg.button && viewDlg.req === "copy:view", JSON.stringify({ viewDlg, len: viewUrl.length }));
    const pv = await browser.newPage({ viewport: { width: 1200, height: 760 } });
    await pv.goto(viewUrl.replace(/^https?:\/\/[^/]+/, url.replace(/\/$/, "")));
    await pv.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    const seen = await pv.evaluate(() => ({ md: window.__app.source(), n: window.__app.deck.slideCount() }));
    check("…and that link shows only them", seen.n === 3 && seen.md.includes("## Neljä") && !seen.md.includes("## Kolme"), JSON.stringify(seen));
    await pv.close();
    // a plain click lets the pick go
    const cleared = await page.evaluate(() => {
      const a = window.__app;
      a.setCtrl(false);
      a.pointerDown(a.thumbX(0) + 20, a.thumbY() + 20, false, 1);
      a.pointerUp();
      return { n: a.pickCount(), bar: a.toolbar.picked };
    });
    check("…a plain click lets the picked slides go", cleared.n === 0 && cleared.bar === 0, JSON.stringify(cleared));
    await page.evaluate((src) => window.__app.setSource(src), src0);
  }
  const page3 = await browser.newPage({ viewport: { width: 1200, height: 760 } });
  await page3.addInitScript(() => {
    document.addEventListener("DOMContentLoaded", () => {
      const el = document.getElementById("brandIntro");
      // shown by the page's own HTML, before the app has loaded
      window.__introEarly = !el.hidden && window.__pageStarted !== true;
      const seen = new MutationObserver(() => {
        if (!el.classList.contains("on") || window.__introKey !== undefined) return;
        seen.disconnect();
        window.__introKey = true;
        window.__introName = el.querySelector(".name").getBoundingClientRect().height;
        document.getElementById("keys").dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true, cancelable: true }));
        window.__introSkipped = el.classList.contains("out");
      });
      seen.observe(el, { attributes: true });
    });
  });
  await page3.goto(showUrl.replace(/^https?:\/\/[^/]+/, url.replace(/\/$/, "")));
  await page3.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
  // a shared link opens with Sliqtly's intro (web/brand.js); a key
  // skips it and is not taken as "next"
  const intro = await page3.evaluate(() => ({
    early: window.__introEarly === true,
    shown: window.__introKey === true,
    name: window.__introName || 0,
  }));
  check("a shared link opens with the Sliqtly intro at once, the name large", intro.early && intro.shown && intro.name >= 48, JSON.stringify(intro));
  // the key comes the moment the intro can be skipped (init script above: a slow
  // page may take longer than the intro to answer a look from here)
  await page3.waitForFunction(() => document.getElementById("brandIntro").hidden, null, { timeout: 20000 }).catch(() => {});
  const skipped = await page3.evaluate(() => ({
    skipped: window.__introSkipped === true,
    hidden: document.getElementById("brandIntro").hidden,
    mode: JSON.parse(window.__app.layoutJson()).mode,
    slide: window.__app.selectedSlide ? window.__app.selectedSlide() : 0,
  }));
  check("a key skips the intro and the show starts from its first slide", skipped.skipped && skipped.hidden && skipped.mode === "present" && skipped.slide === 0, JSON.stringify(skipped));
  const shown = await page3.evaluate(() => ({
    mode: JSON.parse(window.__app.layoutJson()).mode,
    bar: getComputedStyle(document.getElementById("bar")).display,
    viewer: document.body.classList.contains("viewer"),
  }));
  check("the presentation link opens presenting, without the toolbar", shown.mode === "present" && shown.bar === "none" && shown.viewer, JSON.stringify(shown));
  await page3.keyboard.press("Escape");
  await page3.waitForTimeout(200);
  check("Esc does not leave the shared presentation", (await page3.evaluate(() => JSON.parse(window.__app.layoutJson()).mode)) === "present");
  {
    // on a page of its own, so the viewer's … menu checks below start fresh
    const pageE = await browser.newPage({ viewport: { width: 1200, height: 760 } });
    await pageE.goto(showUrl.replace(/^https?:\/\/[^/]+/, url.replace(/\/$/, "")));
    await pageE.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    // Sliqtly's intro skipped with a tap, so the show begins
    await pageE.evaluate(() => { if (!document.getElementById("brandIntro").hidden) window.dispatchEvent(new PointerEvent("pointerdown")); });
    await pageE.waitForFunction(() => JSON.parse(window.__app.layoutJson()).mode === "present", null, { timeout: 20000 });
    await pageE.evaluate(() => { const a = window.__app; for (let i = 0; i < 200 && !a.atEnd(); i += 1) a.next(); });
    await pageE.waitForFunction(() => !document.getElementById("endPanel").hidden, null, { timeout: 8000 }).catch(() => {});
    const end = await pageE.evaluate(() => ({ shown: !document.getElementById("endPanel").hidden, exit: document.getElementById("endExit").textContent }));
    await pageE.click("#endExit");
    const after = await pageE.evaluate(() => ({ shown: !document.getElementById("endPanel").hidden, mode: JSON.parse(window.__app.layoutJson()).mode, last: window.__app.slideShown() === window.__app.deck.slideCount() - 1 }));
    check("a shared deck's end panel closes to its last slide", end.shown && /Close/.test(end.exit) && !after.shown && after.mode === "present" && after.last, JSON.stringify({ end, after }));
    await pageE.close();
  }
  // The bar fades when the pointer rests for 2.5 s, and a slow frame can
  // take that long: each press moves the pointer first, and tries again if
  // the bar faded before the click landed.
  async function tapViewer(pg, sel) {
    for (let i = 0; ; i += 1) {
      await pg.mouse.move(300 + i, 300);
      try {
        await pg.click(sel, { timeout: 4000 });
        return;
      } catch (e) {
        if (i >= 5) throw e;
      }
    }
  }
  // the … menu: exports through the keyboard, Edit only for a signed-in owner.
  await tapViewer(page3, "#vMore");
  await page3.keyboard.press("Enter");
  await page3.waitForTimeout(100);
  const menu = await page3.evaluate(() => ({
    open: !document.getElementById("vMenu").hidden,
    sub: !document.getElementById("vExportSub").hidden,
    edit: !document.getElementById("vEdit").hidden,
    focus: document.activeElement?.dataset.act || "",
  }));
  check("the viewer's … menu opens Export from the keyboard; no Edit for a reader", menu.open && menu.sub && !menu.edit && menu.focus === "pdf", JSON.stringify(menu));
  await page3.keyboard.press("ArrowDown");
  await page3.keyboard.press("ArrowDown");
  await page3.evaluate(() => { window.__lastDownload = ""; });
  await page3.keyboard.press("Enter");
  await page3.waitForTimeout(200);
  const md = await page3.evaluate(() => ({ dl: window.__lastDownload, closed: document.getElementById("vMenu").hidden }));
  check("the viewer exports Markdown from the … menu", md.dl === "downloaded" && md.closed, JSON.stringify(md));
  await tapViewer(page3, "#vMore");
  await tapViewer(page3, "#vExport");
  await page3.evaluate(() => { window.__lastDownload = ""; });
  await tapViewer(page3, '#vMenu [data-act="pdf"]');
  const pdfAt = Date.now();
  await page3.waitForFunction(() => window.__lastDownload !== "", null, { timeout: 180000 }).catch(() => {});
  const pdfGot = await page3.evaluate(() => [window.__lastDownload, document.getElementById("err").textContent]);
  check("the viewer exports a PDF from the … menu", pdfGot[0] === "downloaded", JSON.stringify(pdfGot) + " " + (Date.now() - pdfAt) + " ms");
  // ?export=… (what the assistant's preview opens): the site downloads it on load, once
  const page4 = await browser.newPage({ viewport: { width: 1000, height: 640 } });
  await page4.goto(showUrl.replace(/^https?:\/\/[^/]+/, url.replace(/\/$/, "")) + "&export=md");
  await page4.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
  await page4.waitForFunction(() => window.__lastDownload !== "", null, { timeout: 30000 }).catch(() => {});
  const auto = await page4.evaluate(() => ({ dl: window.__lastDownload, hash: /export=/.test(location.hash), viewer: document.body.classList.contains("viewer") }));
  check("an ?export=md link downloads the shown deck once", auto.dl === "downloaded" && !auto.hash && auto.viewer, JSON.stringify(auto));
  await page4.close();
  const shownMd = await page3.evaluate(() => window.__app.source());
  await tapViewer(page3, "#vMore");
  await Promise.all([page3.waitForEvent("load", { timeout: 30000 }), page3.click('#vMenu [data-act="new"]')]);
  await page3.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
  const made = await page3.evaluate(() => ({ viewer: document.body.classList.contains("viewer"), mode: JSON.parse(window.__app.layoutJson()).mode, md: window.__app.source() }));
  check("Create New… opens the shown deck in the editor", !made.viewer && made.mode !== "present" && made.md === shownMd, JSON.stringify({ ...made, md: made.md.slice(0, 40) }));
  await page3.close();

  // A picture from the clipboard's point of view: bytes into the store,
  // markdown at the caret, a picture command on the slide.
  const pic = await page.evaluate(async () => {
    // setSource put the caret at the top; a picture there would push the
    // front matter down into a slide
    const app = window.__app;
    app.setFocus("editor");
    for (let i = 0; i < 400; i += 1) app.key("down", false, false);
    app.key("end", false, false);
    const c = document.createElement("canvas");
    c.width = 64;
    c.height = 40;
    const g = c.getContext("2d");
    g.fillStyle = "#e0457b";
    g.fillRect(0, 0, 64, 40);
    const blob = await new Promise((res) => c.toBlob(res, "image/png"));
    const file = new File([blob], "image.png", { type: "image/png" });
    const dt = new DataTransfer();
    dt.items.add(file);
    const keys = document.getElementById("keys");
    keys.focus();
    keys.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
    await new Promise((res) => setTimeout(res, 600));
    const a = window.__app;
    // the image window first: the picture in the crop, nothing in the deck yet
    const dialog = a.chartIsOpen();
    const win = JSON.parse(a.chartJson());
    const shown = win.list.cmds.find((c) => c.k === 2 && String(c.src || "").startsWith("/__paste/"));
    const before = a.source().includes("](media/liitetty-");
    // the frame's bottom-right handle dragged to the middle: the left-top
    // quarter of the picture is kept
    if (shown) {
      a.pointerDown(shown.x + shown.w - 1, shown.y + shown.h - 1, false, 1);
      a.pointerMove(shown.x + shown.w * 0.5, shown.y + shown.h * 0.5);
      a.pointerUp();
    }
    const plan = JSON.parse(a.pastePlan());
    // Enter adds it
    a.key("enter", false, false);
    window.__handleRequests();
    // cut and kept asynchronously (a canvas, IndexedDB)
    for (let i = 0; i < 80 && !a.source().includes("](media/liitetty-"); i += 1) await new Promise((res) => setTimeout(res, 100));
    const stage = JSON.parse(a.stageJson());
    const src = a.source();
    const rel = (src.match(/\]\((media\/liitetty-[^)]+)\)/) || [])[1] || "";
    const bmp = rel ? await window.__pictureSize("/" + rel) : [0, 0];
    return {
      dialog, shown: !!shown, before, plan, rel,
      md: !!rel,
      image: stage.list.cmds.some((c) => c.k === 2 && String(c.src || "").includes("media/liitetty-")),
      size: bmp,
    };
  });
  check("a pasted picture opens the image window with the picture in it", pic.dialog && pic.shown && !pic.before, JSON.stringify(pic));
  check("…the crop frame follows a handle", pic.plan.crop === "0,0,32,20", JSON.stringify(pic.plan));
  check("…and Add writes it into the markdown", pic.md);
  check("…drawn on the slide", pic.image);
  check("…cut to the part kept", pic.size.join("x") === "32x20", JSON.stringify(pic.size));

  // An SVG with only a viewBox, as the MCP server's decks have them: the
  // browser decodes no bitmap from it and gives it no size of its own, and the
  // PDF writer reads PNG and JPEG. It is drawn once at full-slide size
  // (web/picture.js) and the slides and the exports get that.
  const svgPic = await page.evaluate(async () => {
    const a = window.__app;
    const images = () => (new TextDecoder("latin1").decode(new Uint8Array(a.pdf())).match(/\/Subtype\s*\/Image/g) || []).length;
    const pdfBefore = images();
    const svg = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1600 900"><rect width="1600" height="900" fill="#2a7f3e"/><path d="M0 0L1600 900" stroke="#fff"/></svg>';
    const file = new File([svg], "kuva.svg", { type: "image/svg+xml" });
    const dt = new DataTransfer();
    dt.items.add(file);
    const keys = document.getElementById("keys");
    keys.focus();
    keys.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
    for (let i = 0; i < 80 && !/\]\(media\/liitetty-[^)]+\.svg\)/.test(a.source()); i += 1) await new Promise((res) => setTimeout(res, 100));
    const rel = (a.source().match(/\]\((media\/liitetty-[^)]+\.svg)\)/) || [])[1] || "";
    const stage = JSON.parse(a.stageJson());
    const cmd = stage.list.cmds.find((c) => c.k === 2 && String(c.src || "").includes(rel || "?"));
    return {
      rel, window: a.chartIsOpen(),
      drawn: !!cmd, shape: cmd ? Math.round((cmd.w / cmd.h) * 100) / 100 : 0,
      size: rel ? window.__pictureSize("/" + rel) : [0, 0],
      pixel: rel ? window.__picturePixel("/" + rel, 40, 600) : [],
      pdf: [pdfBefore, images()],
    };
  });
  check("a pasted SVG goes on the slide as an SVG file", !!svgPic.rel && !svgPic.window && svgPic.drawn, JSON.stringify(svgPic));
  check("…in its viewBox's shape, drawn at full-slide size", svgPic.shape === 1.78 && svgPic.size.join("x") === "2560x1440", JSON.stringify(svgPic));
  check("…with its own colours", svgPic.pixel[0] === 0x2a && svgPic.pixel[1] === 0x7f && svgPic.pixel[2] === 0x3e, JSON.stringify(svgPic.pixel));
  check("…and in the PDF", svgPic.pdf[1] === svgPic.pdf[0] + 1, JSON.stringify(svgPic.pdf));

  // the same picture as the background of the slide at the caret
  const bg = await page.evaluate(() => {
    const a = window.__app;
    const line = a.caretLine();
    a.placePicture("media/tausta.png", "", "bg-slide");
    const lines = a.source().split("\n");
    let k = Math.min(line, lines.length - 1);
    while (k >= 0 && !lines[k].startsWith("#")) k -= 1;
    return k >= 0 ? lines[k] : "";
  });
  check("a picture placed as the slide's background goes on its heading", /\bbg=media\/tausta\.png\b/.test(bg), bg);
  await shot("3-picture.png");

  // The files tab: the picture's row shows it on hover, and a click opens
  // the image editor, whose Save writes the adjusted picture over the file.
  const ed = await page.evaluate(async (rel) => {
    const a = window.__app;
    a.showTab("files");
    window.__handleRequests();
    const name = rel.split("/").pop();
    let row = null;
    for (let i = 0; i < 40 && !row; i += 1) {
      await new Promise((res) => setTimeout(res, 100));
      const pj = a.panelsJson();
      row = pj ? JSON.parse(pj).list.cmds.find((c) => c.k === 3 && c.text === name) : null;
    }
    if (!row) return { row: false };
    a.pointerMove(row.x + 4, row.y + 4);
    const cmds = JSON.parse(a.panelsJson()).list.cmds;
    const hover = cmds.some((c) => c.k === 2 && c.src === "/" + rel);
    const before = window.__picturePixel("/" + rel, 2, 2);
    a.pointerDown(row.x + 4, row.y + 4, false, 1);
    a.pointerUp();
    window.__handleRequests();
    for (let i = 0; i < 40 && !a.chartIsOpen(); i += 1) {
      await new Promise((res) => setTimeout(res, 100));
      window.__handleRequests();
    }
    const open = a.chartIsOpen();
    const win = open ? JSON.parse(a.chartJson()) : null;
    const shown = !!(win && win.list.cmds.find((c) => c.k === 2 && String(c.src || "").startsWith("/__adjust/")));
    // darker and greyer, as the sliders would set it
    a.chart.adjBright = -50;
    a.chart.adjSat = -100;
    a.chart.adjDirty = true;
    a.chart.changed = true;
    // the slide shows the change before Save, and the window says how its
    // text reads over it
    a.writeChart();
    window.__handleRequests();
    let live = before;
    for (let i = 0; i < 30; i += 1) {
      await new Promise((res) => requestAnimationFrame(() => setTimeout(res, 30)));
      live = window.__picturePixel("/" + rel, 2, 2);
      if (live.join() !== before.join()) break;
    }
    let note = "";
    for (let i = 0; i < 30 && !note; i += 1) {
      await new Promise((res) => requestAnimationFrame(() => setTimeout(res, 30)));
      note = a.chart.adjContrastNote;
    }
    const plan = JSON.parse(a.adjustPlan());
    a.key("enter", false, false);
    window.__handleRequests();
    let after = before;
    for (let i = 0; i < 60; i += 1) {
      await new Promise((res) => setTimeout(res, 100));
      // the saved file (an <img>), not the preview (a canvas)
      if (window.__pictureTag("/" + rel) !== "IMG") continue;
      after = window.__picturePixel("/" + rel, 2, 2);
      if (after.join() !== before.join()) break;
    }
    a.showTab("md");
    return { row: true, hover, open, shown, plan, before, live, note, after, closed: !a.chartIsOpen() };
  }, pic.rel);
  check("a picture in the files tab shows a preview on hover", ed.row && ed.hover, JSON.stringify(ed));
  check("…a click opens the image editor with the picture in it", ed.open && ed.shown, JSON.stringify(ed));
  const grey = ed.after && Math.abs(ed.after[0] - ed.after[1]) < 4 && Math.abs(ed.after[1] - ed.after[2]) < 4;
  check("…the slide shows the change while the sliders move", ed.live && ed.live.join() !== ed.before.join(), JSON.stringify({ before: ed.before, live: ed.live }));
  check("…and the window says how the slide's text reads over it", /^Slide \d+: /.test(ed.note || ""), JSON.stringify(ed.note));
  check("…and Save writes the adjusted picture over the file", ed.closed && grey && ed.after[0] < ed.before[0], JSON.stringify({ before: ed.before, after: ed.after, plan: ed.plan }));

  // TeX math: $…$ in a line and a $$ display are drawn as filled outlines
  const math = await page.evaluate(() => {
    const a = window.__app;
    const src0 = a.source();
    a.setSource("# M\n\n## Kaava\n\nPinta-ala $A = \\pi r^2$ ja\n\n$$x = \\frac{-b \\pm \\sqrt{b^2 - 4ac}}{2a}$$\n");
    a.selectSlide(1);
    a.localT = -1;
    const cmds = JSON.parse(a.stageJson()).list.cmds;
    const paths = cmds.filter((c) => c.k === 6).length;
    const raw = cmds.some((c) => c.k === 3 && c.text && c.text.includes("\\frac"));
    a.setSource(src0);
    return { paths, raw };
  });
  check("a formula is drawn as outlines, not as its TeX", math.paths >= 2 && !math.raw, JSON.stringify(math));

  // A backdrop effect (raindrop, liquid glass) renders the slide offscreen;
  // the editor drawn before it has to survive the frame, and the drops have
  // to show over the slide rather than under its paper.
  {
    const src0 = await page.evaluate(() => window.__app.source());
    const gutter = { x: 4, y: 90, width: 40, height: 160 };
    const cb = await page.locator("#c").boundingBox();
    const clip = { x: cb.x + gutter.x, y: cb.y + gutter.y, width: gutter.width, height: gutter.height };
    const put = async (fx) => {
      await page.evaluate((fx) => { const a = window.__app; a.showTab("md"); a.setSource("# D\n\n## Otsikko {fx=" + fx + "}\n\nTeksti\n"); a.selectSlide(1); }, fx);
      await page.waitForTimeout(700);
    };
    await put("starfield");
    const before = await page.screenshot({ clip });
    await put("raindrop");
    const after = await page.screenshot({ clip });
    const stage = await page.evaluate(() => {
      const cmds = window.__lastStage.list.cmds;
      const fxAt = cmds.findIndex((c) => c.efx);
      const textAt = cmds.map((c) => !!c.text).lastIndexOf(true);
      return { fxAt, textAt };
    });
    await page.evaluate((s) => window.__app.setSource(s), src0);
    check("a raindrop slide leaves the editor drawn", before.equals(after));
    check("…and the drops go over the slide's content", stage.fxAt > stage.textAt && stage.textAt >= 0, JSON.stringify(stage));
  }

  // A pasted document starts from its first slide; a paste into text does not move
  const paste = await page.evaluate(() => {
    const a = window.__app;
    const src0 = a.source();
    a.showTab("md");
    a.setSource("");
    a.setFocus("editor");
    a.pasteText("# Uusi\n\n## Eka\n\nteksti\n\n## Toka\n\nlisää\n\n## Kolmas\n\nloppu\n");
    const whole = [a.caretLine(), a.caretCol(), a.slideShown()];
    a.setSource("abc\n");
    a.setFocus("editor");
    a.key("End", false, false);
    a.pasteText("\nlisä\nrivi");
    const inside = [a.caretLine(), a.caretCol()];
    a.setSource(src0);
    return { whole, inside };
  });
  check("a pasted document opens at its first slide", paste.whole.join(",") === "0,0,0", JSON.stringify(paste));
  check("…and a paste into text leaves the caret after it", paste.inside.join(",") === "2,4", JSON.stringify(paste));

  // A selector in the theme lists what it can have, set and not yet set
  const selHint = await page.evaluate(() => {
    const a = window.__app;
    a.showTab("css");
    const lines = a.themeCss().split("\n");
    const ln = lines.findIndex((l) => /^page\s*\{/.test(l));
    const h = JSON.parse(a.hintFor(ln, 1) || "null");
    a.showTab("md");
    return h && { kind: h.kind, props: h.rule.props.map((p) => p.name + (p.value ? "=" : "+")) };
  });
  check("a selector's popover lists its properties", !!selHint && selHint.kind === "selector" && selHint.props.includes("padding=") && selHint.props.includes("background-image+"), JSON.stringify(selHint));

  // …and its doc line under the pointer never moves the rows: the card keeps the
  // tallest line's height, also when it sits above the value near the bottom
  const selHover = await page.evaluate(() => {
    const a = window.__app;
    a.showTab("css");
    const lines = a.themeCss().split("\n");
    const ln = lines.findIndex((l) => /^page\s*\{/.test(l));
    const h = JSON.parse(a.hintFor(ln, 1) || "null");
    const walk = (e, f, out = []) => { if (f(e)) out.push(e); for (const k of e.children || []) walk(k, f, out); return out; };
    const res = [];
    for (const y of [h.y, a.hint.h - 40]) {
      a.openHint(JSON.stringify({ ...h, y }));
      a.hintJson();
      const ys = () => walk(a.hint.host.lastPage, (e) => /^hp-prop-/.test(e.id || "")).map((e) => Math.round(e.calculatedY));
      const y0 = ys().join(",");
      let moved = 0;
      for (const r of walk(a.hint.host.lastPage, (e) => /^hp-prop-/.test(e.id || ""))) {
        a.pointerMove(r.calculatedX + 10, r.calculatedY + 6);
        a.hintJson();
        if (ys().join(",") !== y0) moved++;
      }
      res.push({ rows: y0.split(",").length, moved, tip: a.hint.tip.length > 0 });
      a.closeHint();
    }
    a.showTab("md");
    return res;
  });
  check("…and hovering its properties never moves the rows (also flipped above)", selHover.every((r) => r.rows > 3 && r.moved === 0 && r.tip), JSON.stringify(selHover));

  // A click on the slide picks the block under it: an outline, its theme
  // spacing as bands, "Edit content" and "Style" next to it
  const pk = await page.evaluate(() => {
    const a = window.__app;
    const src0 = a.source();
    const css0 = a.themeCss();
    a.showTab("md");
    a.setSource("# D\n\n## Otsikko\n\n- yksi\n- kaksi\n\nKappale tekstiä.\n{.lead}\n");
    a.selectSlide(1);
    a.place();
    const l = a.deck.layout();
    const r = a.slideRect, sc = a.slideScale();
    const boxes = l.boxes.filter((b) => b.page === a.selected && b.kind === 0);
    const at = (t) => { const b = boxes.find((b) => b.text.startsWith(t)); return [r.x + (b.x + 4) * sc, r.y + (b.y + b.h / 2) * sc]; };
    const click = ([x, y]) => { a.pointerDown(x, y, false, 1); a.pointerUp(); };
    const out = {};
    click(at("Otsikko"));
    out.head = [a.pick.sel, a.pick.bands.map((b) => b.sel + ":" + b.prop + (b.isSet ? "=" : "+")).join(" ")];
    out.drawn = /sel-box|"text":"Style"/.test(a.pickJson()) || a.pickJson().length > 100;
    a.pickStyle();
    out.style = [a.hintIsOpen(), a.hint.kind, a.hintPinned()];
    out.styleProps = [];
    for (let i = 0; i < 40; i++) {
      const p = a.hint.rule.get("props").at(i);
      if (!p || !p.isObject || !p.isObject()) break;
      out.styleProps.push(p.stringOr("sel", "") + ">" + p.stringOr("name", "") + (p.stringOr("value", "") ? "=" : "+"));
      // the colour's row says what the slide uses and which rule gives it
      if (p.stringOr("name", "") === "color") out.headInk = [p.stringOr("sel", ""), p.stringOr("eff", ""), p.stringOr("from", "-")];
    }
    const hc = a.pick.cascade(a.themeCss()).facet("color");
    out.headModel = [hc.own, hc.value, hc.from];
    a.closeHint();
    // a band: its property opens next to it, written into the theme
    a.pickJson();
    const bi = a.pick.bands.findIndex((b) => b.prop === "margin-bottom");
    const b = a.pick.bands[bi];
    a.pointerDown(b.sx + b.sw / 2, b.sy + b.sh / 2, false, 1);
    a.pointerUp();
    out.band = [a.edTab, a.hint.kind, a.hint.name, a.hintPinned(), a.hint.ax > r.x - 1];
    // …and the card stays off the element: under, over or beside it
    a.hintJson();
    const card = a.hint.cardEl();
    const ox = a.pick.ox, oy = a.pick.oy, ow = a.pick.ow, oh = a.pick.oh;
    out.offElement = card.calculatedY >= oy + oh || card.calculatedY + card.calculatedHeight <= oy || card.calculatedX >= ox + ow || card.calculatedX + card.calculatedWidth <= ox;
    a.closeHint();
    // a list item: its line in the Markdown
    a.showTab("md");
    click(at("kaksi"));
    const le = a.pick.cascade(a.themeCss());
    out.li = [a.pick.sel, le.facet("color").own === le.facet("color").from || le.facet("color").from === "" ? "text" : "?", le.facet("marker-color").own];
    a.pickContent();
    out.liContent = [a.edTab, a.anchorLine(), a.anchorCol(), a.caretLine(), a.caretCol()];
    click(at("Kappale"));
    out.p = [a.pick.sel, a.pick.cascade(a.themeCss()).facet("color").own];
    a.setFocus("stage");
    a.key("escape", false, false);
    out.cleared = !a.pick.on;
    // a press off the slide, and playing, let it go too
    click(at("Kappale"));
    const sa = a.stageArea;
    a.pointerDown(sa.x + 4, sa.y + sa.h - 4, false, 1);
    a.pointerUp();
    out.offSlide = !a.pick.on;
    click(at("Kappale"));
    a.play();
    out.onPlay = !a.pick.on && a.pickJson() === "";
    a.stop();
    a.setSource(src0);
    a.setStyleSheet(css0);
    return out;
  });
  check("a click on a heading picks it with the headings' margins as bands", pk.head[0] === "h2" && pk.head[1] === "heading:margin-top= heading:margin-bottom=" && pk.drawn, JSON.stringify(pk.head));
  check("…its Style lists h2's and all headings' properties, set and not", pk.style.join(",") === "true,selector,true" && pk.styleProps.includes("h2>font-size=") && pk.styleProps.includes("heading>margin-top=") && pk.styleProps.some((p) => p.endsWith("+")), JSON.stringify(pk.styleProps));
  check("…a band opens its value in the theme, next to the slide", pk.band.join(",") === "css,number,heading › margin-bottom,true,true", JSON.stringify(pk.band));
  check("…and its card does not cover the element", pk.offElement === true);
  check("…a press off the slide or Play lets the pick go", pk.offSlide === true && pk.onPlay === true, JSON.stringify([pk.offSlide, pk.onPlay]));
  check("…its colour row shows the value the slide uses and its rule, as the cascade has it", pk.headInk && pk.headInk[0] === "h2" && pk.headInk[1] === pk.headModel[1] && pk.headInk[2] === pk.headModel[2] && pk.headInk[1] !== "", JSON.stringify([pk.headInk, pk.headModel]));
  check("a list item is picked as li (text from the document, bullets from list)", pk.li.join("|") === "li|text|list" && pk.liContent.join(",") === "md,5,0,5,7", JSON.stringify([pk.li, pk.liContent]));
  check("…a paragraph with {.lead} gets the class's rule too, and Esc lets go", pk.p.join("|") === "p|.lead" && pk.cleared, JSON.stringify([pk.p, pk.cleared]));

  // A diagram: the pointer over it outlines it before anything is picked; a
  // click picks it, and its "Edit content" opens the diagram window, whose
  // look, boxes and links are written into the fence. A table's opens the
  // table window.
  {
    const src0 = await page.evaluate(() => window.__app.source());
    const md = "# D\n\n## Kulku\n\n```mermaid\nflowchart LR\n  A[Alku] --> B[Toinen]\n  B --> C[Loppu]\n```\n\n## Taulu\n\n| Alue | Myynti |\n| :--- | ---: |\n| Etelä | 120 |\n\n## Data\n\n```table\ndata/check-sales.csv\nrows: 8\n```\n";
    const at = await page.evaluate((t) => {
      const a = window.__app;
      a.showTab("md");
      a.setSource(t);
      a.selectSlide(1);
      a.place();
      a.pick.clear();
      const u = a.deck.slideAt(1).diagrams[0], r = a.slideRect, sc = a.slideScale();
      return [r.x + (u.bx + u.bw * 0.2) * sc, r.y + (u.by + u.bh * 0.8) * sc];
    }, md);
    await page.mouse.move(at[0] - 30, at[1]);
    await page.mouse.move(at[0], at[1]);
    const hov = await page.evaluate(() => { const a = window.__app; return [a.pick.on, a.pick.hoverOn, /sel-hover/.test(a.pickJson()) || a.pickJson().length > 0, a.pick.hoverOn]; });
    await page.mouse.click(at[0], at[1]);
    const btn = await page.evaluate(() => {
      const a = window.__app;
      a.pickJson();
      return [a.pick.sel, a.pick.cx + 6, a.pick.cy + 6, a.pick.partAt(a.pick.cx + 6, a.pick.cy + 6)];
    });
    await page.mouse.move(btn[1], btn[2]);
    await page.mouse.click(btn[1], btn[2]);
    // a control of the window by its id, pressed where it is drawn
    const press = async (tid) => {
      const p = await page.evaluate((tid) => {
        const c = window.__app.chart;
        c.page();
        const k = c.host.ctls.find((k) => k.tid === tid);
        return k && k.rootEl ? [k.rootEl.calculatedX + k.rootEl.calculatedWidth / 2, k.rootEl.calculatedY + k.rootEl.calculatedHeight / 2] : null;
      }, tid);
      if (p) { await page.mouse.click(p[0], p[1]); await page.waitForTimeout(60); }
      return !!p;
    };
    const win = await page.evaluate(() => { const c = window.__app.chart; return [c.isOpen, c.mode, c.flow.editable, c.flow.liveNodes().length]; });
    const pressed = [await press("de-look-sketch"), await press("de-dir-TD")];
    const fence = () => page.evaluate(() => window.__app.source().split("\n").slice(4, 10).join("\n"));
    const looks = await fence();
    // a box's words and a new box, from the Boxes tab
    await page.evaluate(() => { const c = window.__app.chart; c.deTab = "boxes"; c.rebuild(); });
    pressed.push(await press("de-n-B"));
    await page.keyboard.press("End");
    await page.keyboard.type("2");
    pressed.push(await press("de-shape-3"));
    await page.evaluate(() => { const c = window.__app.chart; c.deTab = "links"; c.rebuild(); });
    pressed.push(await press("de-from"));
    await page.keyboard.type("Loppu");
    pressed.push(await press("de-to"));
    await page.keyboard.type("Alku");
    pressed.push(await press("de-addedge"));
    const boxes = await fence();
    pressed.push(await press("ce-done"));
    const closed = await page.evaluate(() => !window.__app.chart.isOpen);
    // the table: its window, a cell and an alignment
    await page.evaluate(() => { const a = window.__app; a.selectSlide(2); a.place(); });
    const tl = await page.evaluate(() => { const a = window.__app; const t = a.source().split("\n"); const line = t.findIndex((l) => l.startsWith("| Alue")); return [a.openTableEditor(line), a.chart.mode, a.chart.grid.cols(), a.chart.grid.rowCount()]; });
    pressed.push(await press("ge-c-0-1"));
    await page.keyboard.press("End");
    await page.keyboard.type("5");
    pressed.push(await press("ge-align-center"));
    pressed.push(await press("ge-addrow"));
    await page.keyboard.type("Länsi");
    const table = await page.evaluate(() => { const t = window.__app.source().split("\n"); const at = t.findIndex((l) => l.startsWith("| Alue")); return t.slice(at, at + 4).join("\n"); });
    await page.keyboard.press("Escape");
    const dataTable = await page.evaluate(() => { const a = window.__app; const t = a.source().split("\n"); const line = t.findIndex((l) => l.startsWith("```table")); const ok = a.openTableEditor(line); const c = a.chart; return [ok, c.grid.kind, c.grid.file, c.grid.option("rows")]; });
    await page.evaluate(() => window.__app.closeChart());
    await page.evaluate((t) => { const a = window.__app; a.pick.clear(); a.setSource(t); }, src0);
    check("the pointer over a diagram outlines it before anything is picked", hov.join(",") === "false,true,true,true", JSON.stringify(hov));
    check("…a click picks it, and Edit content opens the diagram window", btn[0] === "diagram" && btn[3] === "content" && win.join(",") === "true,diagram,true,3", JSON.stringify([btn, win]));
    check("…its look and direction are written under and into the fence", /flowchart TD\n/.test(looks) && /\{style=sketch layout=keep\}/.test(looks), looks);
    check("…a box's words and shape, and a link to a box by its words", /B\{Toinen2\}/.test(boxes) && /C --> A\n/.test(boxes) && pressed.every(Boolean) && closed, JSON.stringify([boxes, pressed]));
    check("a table's Edit content opens the table window: a cell, an alignment and a row written", tl.join(",") === "true,grid,2,1" && table === "| Alue | Myynti |\n| :--- | :---: |\n| Etelä | 1205 |\n| Länsi |  |", JSON.stringify([tl, table]));
    check("…a data file's table opens with its options", dataTable.join(",") === "true,file,data/check-sales.csv,8", JSON.stringify(dataTable));
  }

  // Style with the Files tab open: the popover is a window, so it is drawn
  // over the docked files panel and a press on it is its own (UiLayers)
  const zo = await page.evaluate(() => {
    const a = window.__app;
    const src0 = a.source();
    a.setSource("# D\n\n## Otsikko\n\nKappale tekstiä.\n");
    a.showTab("files");
    a.selectSlide(1);
    a.place();
    const l = a.deck.layout();
    const r = a.slideRect, sc = a.slideScale();
    const b = l.boxes.find((b) => b.page === a.selected && b.kind === 0 && b.text.startsWith("Otsikko"));
    a.pointerDown(r.x + (b.x + 4) * sc, r.y + (b.y + b.h / 2) * sc, false, 1);
    a.pointerUp();
    a.pickStyle();
    a.hintJson();
    const out = { order: a.layerOrder(), tab: a.edTab };
    const card = a.hint.cardEl();
    const cx = card.calculatedX + card.calculatedWidth / 2, cy = card.calculatedY + 12;
    // the card where it covers the files panel, else its middle
    const e = a.edRect;
    const ox = Math.min(card.calculatedX + 8, e.x + e.w - 8);
    out.over = ox >= card.calculatedX && ox >= e.x && ox <= e.x + e.w;
    out.at = a.layerAt(out.over ? ox : cx, cy);
    out.files = a.layerAt(e.x + 10, e.y + e.h - 10);
    out.cursor = a.cursorAt(out.over ? ox : cx, cy);
    // the panels' layers are drawn apart: the docked one has the files, not a dialog
    out.docked = a.panelsPartJson("docked").length > 200 && a.panelsPartJson("dialog").length < a.panelsPartJson("docked").length;
    a.closeHint();
    out.closed = a.layerOrder();
    a.showTab("md");
    a.setSource(src0);
    return out;
  });
  check("Style opened with the Files tab: drawn over the files panel, and a press on it is the popover's", zo.tab === "files" && /panels.*hint/.test(zo.order) && zo.at === "hint" && zo.files === "panels" && zo.docked && !/hint/.test(zo.closed), JSON.stringify(zo));

  // A property opened from an element's Style list closes from its corner
  // and its ‹ goes back to the list; a page's padding slider stays on the slide
  const back = await page.evaluate(() => {
    const a = window.__app;
    const src0 = a.source();
    const css0 = a.themeCss();
    a.showTab("md");
    a.setSource("# D\n\n## Otsikko\n\nTeksti\n");
    a.selectSlide(1);
    a.place();
    const l = a.deck.layout();
    const r = a.slideRect, sc = a.slideScale();
    const bx = l.boxes.find((b) => b.page === a.selected && b.kind === 0 && b.text.startsWith("Otsikko"));
    a.pointerDown(r.x + (bx.x + 4) * sc, r.y + (bx.y + bx.h / 2) * sc, false, 1);
    a.pointerUp();
    a.pickStyle();
    const walk = (e, f) => { if (f(e)) return e; for (const k of e.children || []) { const x = walk(k, f); if (x) return x; } return null; };
    const press = (el) => { a.pointerDown(el.calculatedX + 6, el.calculatedY + 6, false, 1); a.pointerUp(); };
    const find = (f) => { a.hintJson(); return walk(a.hint.host.lastPage, f); };
    let i = 0;
    for (; i < 40; i++) { const p = a.hint.rule.get("props").at(i); if (!p || !p.isObject || !p.isObject()) { i = -1; break; } if (p.stringOr("name", "") === "color") break; }
    const out = {};
    press(find((e) => e.id === "hp-prop-" + i));
    out.color = [a.hint.kind, a.hint.canBack];
    const ids = [];
    find((e) => { if (e.tid) ids.push(e.tid); return false; });
    const bk = a.hint.host.lastPage && walk(a.hint.host.lastPage, (e) => (e.className || "").includes("hp-x") && e.calculatedX < a.hint.cardEl().calculatedX + 40);
    if (bk) press(bk);
    out.back = [a.hintIsOpen(), a.hint.kind];
    press(find((e) => e.id === "hp-prop-" + i));
    const cards = [];
    find((e) => { if ((e.className || "").includes("hp-x")) cards.push(e); return false; });
    if (cards.length) press(cards[cards.length - 1]);
    out.closed = !a.hintIsOpen();
    a.showTab("css");
    a.setStyleSheet("page {\n  padding: 2.25in;\n}\n");
    const h = JSON.parse(a.hintFor(1, 13) || "null");
    out.pad = h ? [h.min, h.max, h.step] : null;
    out.slideIn = Math.min(a.deck.pageW, a.deck.pageH) / 72;
    a.setSource(src0);
    a.setStyleSheet(css0);
    a.showTab("md");
    return out;
  });
  check("a property from an element's Style list has ‹ back to the list", back.color.join(",") === "color,true" && back.back.join(",") === "true,selector", JSON.stringify(back));
  check("…and × closes it", back.closed === true, JSON.stringify(back));
  check("a page padding slider ends where half of the slide is left", !!back.pad && back.pad[1] <= back.slideIn / 4 && back.pad[1] > back.slideIn / 5 && back.pad[2] <= 0.05, JSON.stringify(back));

  // A card opened from the slide is not the hover's: a hover timer started
  // on the way to the band must not close it while the pointer travels on
  const travel = await (async () => {
    const pts = await page.evaluate(() => {
      const a = window.__app;
      window.__src0 = a.source();
      a.closeHint();
      a.showTab("md");
      a.setSource("# D\n\n## Otsikko\n\nTeksti\n");
      a.selectSlide(1);
      a.place();
      const l = a.deck.layout();
      const r = a.slideRect, sc = a.slideScale();
      const bx = l.boxes.find((b) => b.page === a.selected && b.kind === 0 && b.text.startsWith("Otsikko"));
      a.pointerDown(r.x + (bx.x + 4) * sc, r.y + (bx.y + bx.h / 2) * sc, false, 1);
      a.pointerUp();
      a.pickJson();
      const b = a.pick.bands.find((b) => b.prop === "margin-top");
      return { band: [b.sx + b.sw / 2, b.sy + b.sh / 2], away: [r.x + r.w - 20, r.y + r.h - 20] };
    });
    await page.mouse.move(pts.away[0], pts.away[1], { steps: 4 });
    await page.mouse.move(pts.band[0], pts.band[1], { steps: 6 });
    await page.mouse.down();
    await page.mouse.up();
    await page.mouse.move(pts.away[0], pts.away[1], { steps: 12 });
    await page.waitForTimeout(1000);
    return page.evaluate(() => {
      const a = window.__app;
      const out = [a.hintIsOpen(), a.hint.kind];
      a.closeHint();
      a.pick.clear();
      a.setSource(window.__src0);
      return out;
    });
  })();
  check("a band's card stays open while the pointer moves on", travel.join(",") === "true,number", JSON.stringify(travel));

  // chart-effects takes any of its words together: a chip turns one on or off
  const fx = await page.evaluate(() => {
    const a = window.__app;
    const css0 = a.themeCss();
    a.showTab("css");
    a.setStyleSheet("chart {\n  chart-effects: glow gradient;\n}\n");
    const h = JSON.parse(a.hintFor(1, 20) || "null");
    a.openHint(a.hintFor(1, 20));
    const walk = (e, id) => { if (e.id === id || e.tid === id) return e; for (const k of e.children || []) { const r = walk(k, id); if (r) return r; } return null; };
    a.hintJson();
    const chip = walk(a.hint.host.lastPage, "hp-flag-1");
    if (chip) { a.pointerDown(chip.calculatedX + 6, chip.calculatedY + 6, false, 1); a.pointerUp(); }
    const after = a.themeCss().split("\n")[1].trim();
    a.closeHint();
    a.setStyleSheet(css0);
    a.showTab("md");
    return { kind: h && h.kind, options: h && h.options, chip: !!chip, after };
  });
  check("chart-effects opens as toggles and a chip adds its word", fx.kind === "flags" && fx.after === "chart-effects: glow shadow gradient;", JSON.stringify(fx));

  // The chart editor: a ```vega-lite fence as a kind and a table, written back
  const ce = await page.evaluate(() => {
    const a = window.__app;
    const src0 = a.source();
    a.setSource("# D\n\n## O\n\n```vega-lite\n{\"data\": {\"values\": [{\"f\": \"A\", \"u\": 90}, {\"f\": \"B\", \"u\": 75}]}, \"mark\": \"bar\", \"encoding\": {\"x\": {\"field\": \"f\", \"type\": \"nominal\"}, \"y\": {\"field\": \"u\", \"type\": \"quantitative\"}}}\n```\n");
    const opened = a.openChartEditor(5);
    const drawn = JSON.parse(a.chartJson()).list.cmds.length;
    const c = a.chart;
    const at = (id) => {
      const walk = (e) => { if (e.id === id) return e; for (const k of e.children || []) { const r = walk(k); if (r) return r; } return null; };
      const e = walk(c.host.lastPage);
      return [e.calculatedX + 8, e.calculatedY + 8];
    };
    let p = at("ce-tabs-tab-kaavio");
    a.pointerDown(p[0], p[1], false, 1);
    a.pointerUp();
    a.chartJson();
    p = at("ce-kind-6");
    a.pointerDown(p[0], p[1], false, 1);
    a.pointerUp();
    const line = a.source().includes('"mark": "line"');
    a.chartJson();
    p = at("ce-tabs-tab-tiedot");
    a.pointerDown(p[0], p[1], false, 1);
    a.pointerUp();
    a.chartJson();
    p = at("ce-c-0-1");
    a.pointerDown(p[0], p[1], false, 1);
    a.pointerUp();
    a.key("backspace", false, false);
    a.key("backspace", false, false);
    a.text("60");
    const typed = /"value":60/.test(a.source());
    // the window moves by its title bar, and a click outside it closes it
    a.chartJson();
    p = at("ce-win-titlebar");
    const x0 = c.x;
    a.pointerDown(p[0] + 40, p[1], false, 1);
    a.pointerMove(p[0] + 140, p[1] + 30);
    a.pointerUp();
    const moved = c.x - x0;
    a.chartJson();
    p = at("ce-win-titlebar");
    a.pointerDown(p[0] + 40, p[1], false, 1);
    const stillOpen = a.chartIsOpen();
    a.pointerUp();
    a.pointerDown(a.chart.win.frameEl.calculatedX + a.chart.win.frameEl.calculatedWidth + 30, 5, false, 1);
    a.pointerUp();
    const closed = !a.chartIsOpen();
    // a fence that is not a table opens for its look and says why
    a.setSource("# D\n\n## O\n\n```vega-lite\n{\"layer\": []}\n```\n");
    a.openChartEditor(5);
    const refused = a.chartIsOpen() && a.chart.model.lookOnly && a.chart.model.why.length > 0;
    a.key("escape", false, false);
    // the look: a palette, the effects, the text colour from the picker
    a.setSource("# D\n\n## O\n\n```vega-lite\n{\"data\": {\"values\": [{\"f\": \"A\", \"u\": 90}, {\"f\": \"B\", \"u\": 75}]}, \"mark\": \"bar\", \"encoding\": {\"x\": {\"field\": \"f\", \"type\": \"nominal\"}, \"y\": {\"field\": \"u\", \"type\": \"quantitative\"}}}\n```\n");
    a.openChartEditor(5);
    const press = (id, dx = 6, dy = 6) => { a.chartJson(); const q = at(id); a.pointerDown(q[0] - 8 + dx, q[1] - 8 + dy, false, 1); a.pointerUp(); };
    press("ce-tabs-tab-ulkoasu");
    press("ce-scheme-tableau10");
    press("ce-glow");
    press("ce-pick-text");
    a.chartJson();
    const walk = (e, id) => { if (e.id === id) return e; for (const k of e.children || []) { const r = walk(k, id); if (r) return r; } return null; };
    // EVGUI's picker: a preset, then a drag across the area
    const sw = walk(a.chart.host.lastPage, "ce-cp-preset-2");
    a.pointerDown(sw.calculatedX + 5, sw.calculatedY + 5, false, 1);
    a.pointerUp();
    const preset = /"labelColor":"#eab308"/.test(a.source());
    a.chartJson();
    const area = walk(a.chart.host.lastPage, "ce-cp-sv");
    a.pointerDown(area.calculatedX + 10, area.calculatedY + 10, false, 1);
    a.pointerMove(area.calculatedX + area.calculatedWidth - 2, area.calculatedY + 2);
    a.pointerUp();
    const dragged = a.chart.model.textColor;
    const styled = a.source();
    const looks = preset && /"scheme":"tableau10"/.test(styled) && /"presGlow":true/.test(styled) && styled.includes('"labelColor":"' + dragged + '"') && dragged !== "#eab308";
    // the size, by its slider
    press("ce-tabs-tab-kaavio");
    a.chartJson();
    const sl = walk(a.chart.host.lastPage, "ce-width");
    a.pointerDown(sl.calculatedX + sl.calculatedWidth * 0.5, sl.calculatedY + 8, false, 1);
    a.pointerMove(sl.calculatedX + sl.calculatedWidth * 0.75, sl.calculatedY + 8);
    a.pointerUp();
    const sized = /"width": 7\d0,/.test(a.source());
    // the height by its own slider: the width stays as it was
    a.chartJson();
    const hs = walk(a.chart.host.lastPage, "ce-height");
    a.pointerDown(hs.calculatedX + hs.calculatedWidth * 0.5, hs.calculatedY + 8, false, 1);
    a.pointerMove(hs.calculatedX + hs.calculatedWidth * 0.1, hs.calculatedY + 8);
    a.pointerUp();
    const heightOnly = /"width": 7\d0,/.test(a.source()) && /"height": 1\d0,/.test(a.source());
    a.key("escape", false, false);
    a.setSource(src0);
    return { opened, drawn, line, typed, moved, stillOpen, closed, refused, looks, sized, heightOnly, dragged };
  });
  check("the chart editor opens on a vega-lite fence and draws", ce.opened && ce.drawn > 50, JSON.stringify(ce));
  check("…a kind picked rewrites the fence", ce.line, JSON.stringify(ce));
  check("…a number typed into the table goes into the chart", ce.typed, JSON.stringify(ce));
  check("…its window moves by the title bar", ce.moved === 100 && ce.stillOpen, JSON.stringify(ce));
  check("…a click outside closes it; a chart it cannot tabulate opens for its look and says why", ce.closed && ce.refused, JSON.stringify(ce));
  check("…its look: a palette, a glow and a picked text colour go into the fence", ce.looks, JSON.stringify(ce));
  check("…its width, from its slider", ce.sized, JSON.stringify(ce));
  check("…its height, from its own slider, the width kept", ce.heightOnly, JSON.stringify(ce));

  // A click on the fence's `vega-lite` opens the chart editor, not the language list
  {
    const src0 = await page.evaluate(() => window.__app.source());
    await page.evaluate(() => { const a = window.__app; a.showTab("md"); a.setSource("# D\n\n## O\n\n```vega-lite\n{\"data\": {\"values\": [{\"f\": \"A\", \"u\": 1}, {\"f\": \"B\", \"u\": 2}]}, \"mark\": \"bar\", \"encoding\": {\"x\": {\"field\": \"f\", \"type\": \"nominal\"}, \"y\": {\"field\": \"u\", \"type\": \"quantitative\"}}}\n```\n"); });
    await page.waitForTimeout(300);
    const h = await page.evaluate(() => JSON.parse(window.__app.hintFor(4, 5)));
    const cb = await page.locator("#c").boundingBox();
    await page.mouse.click(cb.x + h.x + 10, cb.y + h.y + 8);
    await page.waitForTimeout(300);
    const res = await page.evaluate(() => ({ open: window.__app.chartIsOpen(), popover: window.__app.hintIsOpen() }));
    await page.evaluate((s) => { const a = window.__app; a.key("escape", false, false); a.setSource(s); }, src0);
    check("a click on the fence's vega-lite opens the chart editor", res.open && !res.popover, JSON.stringify(res));
  }

  // The top bar is on the canvas: a theme picked from its list, a button pressed
  {
    const bar = await page.evaluate(async () => {
      const a = window.__app;
      const t = a.toolbar;
      a.toolbarJson();
      const find = (id) => { const walk = (e) => { if (e.id === id) return e; for (const k of e.children || []) { const r = walk(k); if (r) return r; } return null; }; return walk(t.host.lastPage); };
      const press = (id) => { a.toolbarJson(); const e = find(id); a.pointerDown(e.calculatedX + 10, e.calculatedY + 8, false, 1); a.pointerUp(); };
      const theme0 = document.getElementById("theme").value;
      press("tb-m-slide-trigger");
      // the sub-trigger's chevron at the row's far edge, its name at the start
      a.toolbarJson();
      const row = find("tb-m-slide-item-theme");
      const [label, chev] = row.children || [];
      const edges = label && chev ? { rowL: row.calculatedX, rowR: row.calculatedX + row.calculatedWidth, labelL: label.calculatedX, label: label.textContent, chevR: chev.calculatedX + chev.calculatedWidth, chev: chev.textContent } : null;
      press("tb-m-slide-item-theme");
      const opened = t.openMenu() === "tb-m-slide";
      press("tb-m-slide-item-theme-item-t-editorial");
      const reqs = [];
      for (;;) { const r = a.takeRequest(); if (!r) break; reqs.push(r); }
      press("tb-m-help-trigger");
      press("tb-m-help-item-helpBtn");
      const help = [];
      for (;;) { const r = a.takeRequest(); if (!r) break; help.push(r); }
      // a menu open, then another button of the bar: the menu closes and the button acts
      press("tb-m-export-trigger");
      press("tb-share");
      const through = [];
      for (;;) { const r = a.takeRequest(); if (!r) break; through.push(r); }
      help.push(...through.map((r) => "after-menu:" + r));
      const closedAfter = t.openMenu() === "";
      return { edges, opened, reqs, help, closedAfter, drawn: JSON.parse(a.toolbarJson()).list.cmds.length, theme0, htmlBarHidden: getComputedStyle(document.getElementById("bar")).display === "none" };
    });
    check("the top bar is drawn on the canvas, the HTML one hidden", bar.drawn > 20 && bar.htmlBarHidden, JSON.stringify(bar));
    check("…Slide → Theme opens and a theme chosen becomes the page's select change", bar.opened && bar.reqs.includes("select:theme:editorial"), JSON.stringify(bar));
    const e = bar.edges;
    check("…a submenu's arrow sits at its row's right edge, the name at the left", !!e && e.label === "Theme" && e.chev === "▸" && e.rowR - e.chevR <= 12 && e.labelL - e.rowL <= 12 && e.chevR - e.labelL > 150, JSON.stringify(e));
    check("…a menu's row is the page's button pressed", bar.help.includes("click:helpBtn"), JSON.stringify(bar));
    check("…with a menu open, another button of the bar acts on the first press (and the menu closes)", bar.help.includes("after-menu:click:share") && bar.closedAfter, JSON.stringify(bar));
    await page.evaluate((th) => { const s = document.getElementById("theme"); s.value = th; s.dispatchEvent(new Event("change")); }, bar.theme0);
    await page.waitForTimeout(300);

    // the pointer resting on Slide → Theme opens its submenu without a press
    const find = (id) => `(() => { const a = window.__app; a.toolbarJson(); const w = (e) => { if (e.id === ${JSON.stringify(id)}) return e; for (const k of e.children || []) { const r = w(k); if (r) return r; } return null; }; return w(a.toolbar.host.lastPage); })()`;
    const rest = await page.evaluate(async (f) => {
      const a = window.__app;
      const at = (id) => eval(f[id]);
      const tr = at("trig");
      a.pointerDown(tr.calculatedX + 10, tr.calculatedY + 8, false, 1); a.pointerUp();
      const row = at("row");
      a.pointerMove(row.calculatedX + 20, row.calculatedY + 10);
      const before = !!at("sub");
      // the page's frames drive the clock, and a software-drawn frame is slow
      let after = false;
      for (let n = 0; n < 40 && !after; n++) { await new Promise((r) => setTimeout(r, 100)); after = !!at("sub"); }
      a.key("escape", false, false);
      return { before, after, closed: a.toolbar.openMenu() === "" };
    }, { trig: find("tb-m-slide-trigger"), row: find("tb-m-slide-item-theme"), sub: find("tb-m-slide-item-theme-content") });
    check("…resting the pointer on Theme opens its submenu, after a short delay", !rest.before && rest.after, JSON.stringify(rest));

    // over the Files tab: the File menu is drawn on top and a row under it is pressed
    await page.evaluate(() => window.__app.showTab("files"));
    await page.waitForTimeout(600);
    const onFiles = await page.evaluate(() => {
      const a = window.__app;
      const find = (id) => { a.toolbarJson(); const w = (e) => { if (e.id === id) return e; for (const k of e.children || []) { const r = w(k); if (r) return r; } return null; }; return w(a.toolbar.host.lastPage); };
      const press = (e) => { a.pointerDown(e.calculatedX + 10, e.calculatedY + 8, false, 1); a.pointerUp(); };
      press(find("tb-m-file-trigger"));
      const onTop = a.toolbarOnTop();
      const row = find("tb-m-file-item-save");
      const underPanel = a.panels.has(row.calculatedX + 10, row.calculatedY + 8);
      const why = underPanel ? null : { mode: a.mode, tab: a.edTab, file: a.filePath, open: a.panels.filesOpen, quiet: a.panels.quiet, box: [a.panels.filesX, a.panels.filesY, a.panels.filesW, a.panels.filesH], row: [row.calculatedX, row.calculatedY] };
      for (;;) { if (!a.takeRequest()) break; }
      press(row);
      const reqs = [];
      for (;;) { const r = a.takeRequest(); if (!r) break; reqs.push(r); }
      const closed = !a.toolbarOnTop();
      a.showTab("md");
      return { onTop, underPanel, reqs, closed, why };
    });
    check("…over the Files tab the File menu is on top, and its row takes the press", onFiles.onTop && onFiles.underPanel && onFiles.reqs.includes("click:save") && onFiles.closed, JSON.stringify(onFiles));

    // the File menu's groups: new | open | save | the assistants | settings, lines between them
    // that take no press
    const seps = await page.evaluate(() => {
      const a = window.__app;
      const all = () => { a.toolbarJson(); const out = []; const w = (e) => { if ((e.className || "").includes("ui-dropdownmenu-separator")) out.push(e); for (const k of e.children || []) w(k); }; w(a.toolbar.host.lastPage); return out; };
      const find = (id) => { a.toolbarJson(); const w = (e) => { if (e.id === id) return e; for (const k of e.children || []) { const r = w(k); if (r) return r; } return null; }; return w(a.toolbar.host.lastPage); };
      const press = (e) => { a.pointerDown(e.calculatedX + 10, e.calculatedY, false, 1); a.pointerUp(); };
      press(find("tb-m-file-trigger"));
      const lines = all();
      const ys = ["new", "openbox", "save", "aiClaude", "settings", "deleteDeck"].map((id) => find("tb-m-file-item-" + id).calculatedY);
      const between = lines.length === 5 && lines.every((l, i) => l.calculatedY > ys[i] && l.calculatedY < ys[i + 1] && l.calculatedHeight === 1);
      for (;;) { if (!a.takeRequest()) break; }
      press(lines[0]);
      const reqs = [];
      for (;;) { const r = a.takeRequest(); if (!r) break; reqs.push(r); }
      const stillOpen = a.toolbar.openMenu() !== "";
      a.key("escape", false, false);
      return { n: lines.length, between, reqs, stillOpen };
    });
    check("…the File menu is grouped by five lines, and a line takes no press", seps.between && seps.reqs.length === 0 && seps.stillOpen, JSON.stringify(seps));

    // File → Export: Markdown, PowerPoint, PDF and a zip of every file; File →
    // Delete presentation… is red, last, and asks in the app's window first
    const fx = await page.evaluate(async () => {
      const a = window.__app;
      const find = (id) => { a.toolbarJson(); const w = (e) => { if (e.id === id) return e; for (const k of e.children || []) { const r = w(k); if (r) return r; } return null; }; return w(a.toolbar.host.lastPage); };
      const press = (e) => { a.pointerDown(e.calculatedX + 10, e.calculatedY + 8, false, 1); a.pointerUp(); };
      const shut = () => { for (let n = 0; n < 3 && a.toolbar.openMenu() !== ""; n++) a.key("escape", false, false); };
      const take = () => { const out = []; for (;;) { const r = a.takeRequest(); if (!r) break; out.push(r); } return out; };
      shut();
      take();
      const newReqs = [];
      let newKids = [];
      for (const id of ["newPres", "newSheet"]) {
        press(find("tb-m-file-trigger"));
        press(find("tb-m-file-item-new"));
        let r = null;
        for (let n = 0; n < 40 && !r; n++) { r = find("tb-m-file-item-new-item-" + id); if (!r) await new Promise((ok) => setTimeout(ok, 100)); }
        const nc = find("tb-m-file-item-new-content");
        newKids = nc ? (nc.children || []).map((k) => k.id.split("-item-").pop()) : [];
        if (r) press(r);
        newReqs.push(...take());
        shut();
      }
      press(find("tb-m-file-trigger"));
      press(find("tb-m-file-item-export"));
      let row = null;
      for (let n = 0; n < 40 && !row; n++) { row = find("tb-m-file-item-export-item-x-zip"); if (!row) await new Promise((r) => setTimeout(r, 100)); }
      const c = find("tb-m-file-item-export-content");
      const kids = c ? (c.children || []).map((k) => k.id.split("-item-").pop()) : [];
      if (row) press(row);
      const zip = take();
      shut();
      press(find("tb-m-file-trigger"));
      const del = find("tb-m-file-item-deleteDeck");
      const red = !!del && (del.className || "").includes("ui-dropdownmenu-item-destructive");
      const rows = (find("tb-m-file-content")?.children || []).map((k) => k.id);
      const last = !!del && rows[rows.length - 1] === del.id;
      press(del);
      const asked = take();
      await window.__fileRequest("deletedeck");
      const confirm = a.chart.isOpen && a.chart.mode === "confirm";
      const ok = (() => { const w = (e) => { if (e.id === "cf-ok") return e; for (const k of e.children || []) { const r = w(k); if (r) return r; } return null; }; return a.chart.isOpen ? w(a.chart.host.root) : null; })();
      a.key("escape", false, false);
      const after = take();
      return { newKids, newReqs, kids, zip, red, last, asked, confirm, okDanger: !!ok && (ok.className || "").includes("ui-button-danger"), closed: !a.chart.isOpen, after };
    });
    check("…File → New offers Presentation… (the window) and Datasheet… (the spreadsheet editor)", fx.newKids.join() === "newPres,newSheet" && fx.newReqs.join() === "files:new,files:newsheet", JSON.stringify(fx));
    check("…File → Export lists .md, .pptx, .pdf and .zip, and the zip row is the page's ZIP button", fx.kids.join() === "x-save,x-pptx,x-pdf,x-zip" && fx.zip.includes("click:zip"), JSON.stringify(fx));
    check("…File → Delete presentation… is red and last, and asks first; Esc deletes nothing", fx.red && fx.last && fx.asked.includes("files:deletedeck") && fx.confirm && fx.okDanger && fx.closed && !fx.after.some((r) => r.startsWith("confirm:")), JSON.stringify(fx));

    // File → Recent: Browse all… first (the Files tab), a line, then the decks
    const recent = await page.evaluate(async () => {
      const a = window.__app;
      a.setToolbarOptions("recent", "deck-a\tOld deck\ndeck-b\tOlder deck", "");
      const find = (id) => { a.toolbarJson(); const w = (e) => { if (e.id === id) return e; for (const k of e.children || []) { const r = w(k); if (r) return r; } return null; }; return w(a.toolbar.host.lastPage); };
      const press = (e) => { a.pointerDown(e.calculatedX + 10, e.calculatedY + 8, false, 1); a.pointerUp(); };
      const shut = () => { for (let n = 0; n < 3 && a.toolbar.openMenu() !== ""; n++) a.key("escape", false, false); };
      const pick = async (id) => {
        shut();
        press(find("tb-m-file-trigger"));
        press(find("tb-m-file-item-recent"));
        let row = null;
        for (let n = 0; n < 40 && !row; n++) { row = find("tb-m-file-item-recent-item-" + id); if (!row) await new Promise((r) => setTimeout(r, 100)); }
        if (!row) return null;
        for (;;) { if (!a.takeRequest()) break; }
        press(row);
        const reqs = [];
        for (;;) { const r = a.takeRequest(); if (!r) break; reqs.push(r); }
        shut();
        return reqs;
      };
      const content = () => { const c = find("tb-m-file-item-recent-content"); return c ? (c.children || []).map((k) => k.id) : []; };
      press(find("tb-m-file-trigger"));
      press(find("tb-m-file-item-recent"));
      let order = [];
      for (let n = 0; n < 40 && order.length === 0; n++) { order = content(); if (!order.length) await new Promise((r) => setTimeout(r, 100)); }
      shut();
      const browse = await pick("browse");
      const deck = await pick("r-deck-a");
      return { order, browse, deck };
    });
    check("…File → Recent lists All presentations… first (the presentations window), then the decks, and each opens", recent.order.length === 4 && /browse$/.test(recent.order[0]) && /sep-1$/.test(recent.order[1]) && /r-deck-a$/.test(recent.order[2]) && (recent.browse || []).includes("decks") && (recent.deck || []).includes("files:doc:deck-a"), JSON.stringify(recent));

    // File → Presentations…: the window lists the decks with their added and
    // modified times; a column head sorts, a row opens its deck and closes
    // the window, the ✕ beside it asks once before it deletes, a deck only in
    // the cloud has no ✕; a long list turns pages
    const decks = await page.evaluate(() => {
      const a = window.__app;
      const walk = (e, id) => { if (e.id === id) return e; for (const k of e.children || []) { const r = walk(k, id); if (r) return r; } return null; };
      const texts = (e, out = []) => { if (e.textContent) out.push(e.textContent); for (const k of e.children || []) texts(k, out); return out; };
      const lp = () => { a.panelsJson(); return a.panels.host.lastPage; };
      const drain = () => { const out = []; for (;;) { const r = a.takeRequest(); if (!r) break; out.push(r); } return out; };
      const press = (id) => { const b = walk(lp(), id); if (!b) return false; a.pointerDown(b.calculatedX + 6, b.calculatedY + 6, false, 1); a.pointerUp(); return true; };
      const json = JSON.stringify({ sort: "updated", note: "", rows: [
        { id: "deck-a", name: "Old deck", added: "1.1.2026 10:00", modified: "2.1.2026 11:00", where: "This browser", cloudOnly: false, current: false },
        { id: "cloud:M1", name: "Made by Claude", added: "3.1.2026 09:00", modified: "3.1.2026 09:30", where: "In the cloud", cloudOnly: true, current: false },
      ] });
      drain();
      a.openDecks(json);
      const open = a.decksShowing();
      const shown = texts(lp());
      const delOnCloud = !!walk(lp(), "pd-x-1");
      const openButtons = shown.includes("Open");
      // the ✕ column lines up: both rows' open areas end at the same x
      const r0 = walk(lp(), "pd-r-0"), r1 = walk(lp(), "pd-r-1");
      const aligned = !!r0 && !!r1 && Math.abs((r0.calculatedX + r0.calculatedWidth) - (r1.calculatedX + r1.calculatedWidth)) < 1;
      press("pd-sort-name");
      const sort = drain();
      press("pd-x-0");
      const armed = drain();
      const asks = texts(lp()).includes("Delete?");
      press("pd-x-0");
      const del = drain();
      press("pd-r-1");
      const cloudOpen = drain();
      const closedByOpen = !a.decksShowing();
      a.openDecks(json);
      press("pd-close");
      const closed = !a.decksShowing();
      const many = [];
      for (let i = 1; i <= 23; i++) many.push({ id: "d" + i, name: "Deck " + i, added: "", modified: "", where: "This browser", cloudOnly: false, current: false });
      a.openDecks(JSON.stringify({ sort: "updated", note: "", rows: many }));
      const page1 = texts(lp()).includes("Deck 1") && !texts(lp()).includes("Deck 23");
      press("pd-pg-next");
      const page2 = !texts(lp()).includes("Deck 1") && texts(lp()).some((t) => /^Deck 1[1-9]$/.test(t));
      press("pd-pg-3");
      const page3 = texts(lp()).includes("Deck 23");
      // the arrow shows the way the list was turned: oldest first ↑
      a.openDecks(JSON.stringify({ sort: "created", dir: "asc", note: "", rows: many }));
      const arrowUp = texts(lp()).includes("Added ↑");
      a.openDecks(JSON.stringify({ sort: "created", dir: "desc", note: "", rows: many }));
      const arrowDown = texts(lp()).includes("Added ↓");
      press("pd-close");
      return { arrowUp, arrowDown, open, times: ["2.1.2026 11:00", "1.1.2026 10:00", "3.1.2026 09:30"].every((x) => shown.includes(x)), names: shown.includes("Made by Claude"), delOnCloud, openButtons, aligned, sort, armed, asks, del, cloudOpen, closedByOpen, closed, page1, page2, page3 };
    });
    check("…File → Presentations… lists the decks with added and modified times; a row opens, ✕ deletes after asking, a long list has pages",
      decks.open && decks.times && decks.names && !decks.delOnCloud && !decks.openButtons && decks.aligned && decks.sort.join() === "decks:sort:name" && decks.armed.length === 0 && decks.asks &&
      decks.del.join() === "decks:del:deck-a" && decks.cloudOpen.join() === "files:doc:cloud:M1" && decks.closedByOpen && decks.closed && decks.page1 && decks.page2 && decks.page3 && decks.arrowUp && decks.arrowDown, JSON.stringify(decks));

    // A long deck name widens the menu up to a limit and is cut with "…"
    // there; every row stays inside the card and they are all one width
    const long = await page.evaluate(async () => {
      const a = window.__app;
      const name = "Kesäinen Tampere – matkailijan parhaat palat ja muut kesän kohokohdat";
      a.setToolbarOptions("recent", "deck-a\t" + name + "\ndeck-b\tVuokra ja menot", "");
      const find = (id) => { a.toolbarJson(); const w = (e) => { if (e.id === id) return e; for (const k of e.children || []) { const r = w(k); if (r) return r; } return null; }; return w(a.toolbar.host.lastPage); };
      const press = (e) => { a.pointerDown(e.calculatedX + 10, e.calculatedY + 8, false, 1); a.pointerUp(); };
      press(find("tb-m-file-trigger"));
      press(find("tb-m-file-item-recent"));
      let c = null;
      for (let n = 0; n < 40 && !c; n++) { c = find("tb-m-file-item-recent-content"); if (!c) await new Promise((r) => setTimeout(r, 100)); }
      if (!c) return null;
      const right = c.calculatedX + c.calculatedWidth;
      const rows = (c.children || []).filter((k) => /-item-/.test(k.id));
      const text = (e) => e.textContent || (e.children || []).map(text).join("");
      const out = rows.map((r) => ({ id: r.id, x: r.calculatedX, w: r.calculatedWidth, text: text(r) }));
      return { right, cw: c.calculatedWidth, rows: out };
    });
    await shot("menu-long-name.png");
    await page.evaluate(() => { const a = window.__app; for (let n = 0; n < 3 && a.toolbar.openMenu() !== ""; n++) a.key("escape", false, false); });
    const lr = long && long.rows.find((r) => /r-deck-a$/.test(r.id));
    check("…a long Recent name grows the menu, is cut with …, and stays inside the card",
      !!lr && lr.text.endsWith("…") && long.cw > 220 && long.cw < 480 && long.rows.every((r) => r.x + r.w <= long.right + 0.5 && Math.abs(r.w - lr.w) < 0.5),
      JSON.stringify(long));
  }

  // Edit in Claude: a File menu row (ChatGPT's was taken out); signed out,
  // the assistant opens in a new tab with the deck's Markdown in its prompt
  {
    const rows = await page.evaluate(() => {
      const a = window.__app;
      const find = (id) => { a.toolbarJson(); const w = (e) => { if (e.id === id) return e; for (const k of e.children || []) { const r = w(k); if (r) return r; } return null; }; return w(a.toolbar.host.lastPage); };
      const press = (e) => { a.pointerDown(e.calculatedX + 10, e.calculatedY + 8, false, 1); a.pointerUp(); };
      for (;;) { if (!a.takeRequest()) break; }
      const reqs = [];
      press(find("tb-m-file-trigger"));
      const noChatgpt = !find("tb-m-file-item-aiChatgpt");
      const row = find("tb-m-file-item-aiClaude");
      if (row) press(row);
      for (;;) { const r = a.takeRequest(); if (!r) break; reqs.push(r); }
      return { reqs, noChatgpt };
    });
    await page.context().route(/^https:\/\/claude\.ai\//, (r) => r.fulfill({ status: 200, contentType: "text/html", body: "<title>ai</title>" }));
    const popup = page.waitForEvent("popup", { timeout: 5000 });
    await page.evaluate(() => document.getElementById("aiClaude").click());
    const p = await popup;
    await p.waitForURL(/^https:/, { timeout: 5000 }).catch(() => {});
    const url = p.url();
    await p.close();
    const src = await page.evaluate(() => window.__app.source());
    const q = (u) => { try { return new URL(u).searchParams.get("q") || ""; } catch (_) { return ""; } };
    check("File → Edit in Claude opens the assistant with the deck's Markdown and the connector's tools in the prompt",
      rows.reqs.includes("click:aiClaude") && url.startsWith("https://claude.ai/new?q=")
      && q(url).includes("create_presentation") && q(url).includes(src.split("\n").find((l) => l.trim()) || ""),
      JSON.stringify({ rows, url: url.slice(0, 80) }));
    check("…and the File menu has no Edit in ChatGPT", rows.noChatgpt && !(await page.evaluate(() => !!document.getElementById("aiChatgpt"))));
  }

  // The value popover is on the canvas: a chip, the colour picker and a slider write the text
  {
    const hp = await page.evaluate(() => {
      const a = window.__app;
      const src0 = a.source();
      const css0 = a.themeCss();
      const walk = (e, id) => { if (e.id === id) return e; for (const k of e.children || []) { const r = walk(k, id); if (r) return r; } return null; };
      const find = (id) => { a.hintJson(); return walk(a.hint.host.lastPage, id); };
      const click = (e, dx = 6, dy = 6) => { a.pointerDown(e.calculatedX + dx, e.calculatedY + dy, false, 1); a.pointerUp(); };
      a.showTab("md");
      a.setSource("# D\n\n## O {fx=starfield}\n\nTeksti\n");
      const line = a.source().split("\n").findIndex((l) => l.includes("fx=starfield"));
      a.editor.moveCaret(line, a.source().split("\n")[line].indexOf("starfield") + 2, false);
      const opened = a.openHintAtCaret();
      const drawn = JSON.parse(a.hintJson()).list.cmds.length;
      const pick = a.hint.options.find((o) => o !== "starfield");
      click(find("hp-opt-" + a.hint.options.indexOf(pick)));
      const chip = a.source().includes("{fx=" + pick + "}") && a.hintIsOpen();
      a.key("escape", false, false);
      const closed = !a.hintIsOpen();
      // a colour: EVGUI's picker, a preset
      a.showTab("css");
      const cl = a.themeCss().split("\n").findIndex((l) => /^\s*accent-color:/.test(l));
      a.editor.moveCaret(cl, a.themeCss().split("\n")[cl].indexOf(":") + 3, false);
      a.openHintAtCaret();
      const kind = a.hint.kind;
      click(find("hp-cp-preset-2"), 5, 5);
      const colour = /accent-color: #eab308/i.test(a.themeCss());
      a.closeHint();
      // a number: its slider
      const nl = a.themeCss().split("\n").findIndex((l) => /^\s*font-size:/.test(l));
      a.editor.moveCaret(nl, a.themeCss().split("\n")[nl].indexOf(":") + 3, false);
      a.openHintAtCaret();
      const before = a.themeCss().split("\n")[nl];
      const sl = find("hp-slider");
      a.pointerDown(sl.calculatedX + sl.calculatedWidth * 0.5, sl.calculatedY + 8, false, 1);
      a.pointerMove(sl.calculatedX + sl.calculatedWidth * 0.7, sl.calculatedY + 8);
      const mid = a.themeCss().split("\n")[nl];
      a.hintJson();
      const num = find("hp-num");
      const unitY = (() => { const w = (e) => { if (e.className === "hp-unit") return e; for (const k of e.children || []) { const r = w(k); if (r) return r; } return null; }; return w(a.hint.host.lastPage); })();
      const oneLine = !!unitY && Math.abs((unitY.calculatedY + unitY.calculatedHeight / 2) - (num.calculatedY + num.calculatedHeight / 2)) < 6;
      a.pointerMove(sl.calculatedX + sl.calculatedWidth * 0.9, sl.calculatedY + 8);
      a.pointerUp();
      const after = a.themeCss().split("\n")[nl];
      const twoMoves = mid !== before && after !== mid;
      a.closeHint();
      a.setStyleSheet(css0);
      a.showTab("md");
      a.setSource(src0);
      return { opened, drawn, chip, closed, kind, colour, slid: before !== after, twoMoves, oneLine, before, mid, after, htmlGone: !document.getElementById("valHint") };
    });
    check("the value popover is drawn on the canvas", hp.opened && hp.drawn > 10 && hp.htmlGone, JSON.stringify(hp));
    check("…a chip writes the value, Escape closes it", hp.chip && hp.closed, JSON.stringify(hp));
    check("…a colour from EVGUI's picker, a number from its slider", hp.kind === "color" && hp.colour && hp.slid, JSON.stringify(hp));
    check("…the slider follows every move of one drag, its unit stays on the line", hp.twoMoves && hp.oneLine, JSON.stringify(hp));
  }

  // The help panel is on the canvas: docked on the right, a property opens in the theme
  {
    const hp = await page.evaluate(() => {
      const a = window.__app;
      const src0 = a.source();
      const css0 = a.themeCss();
      a.setSource("# M\n\n## Muotoilu\n\n- <mark>tärkeä</mark> ja <kbd>Ctrl</kbd>\n");
      a.selectSlide(1);
      document.getElementById("helpBtn").click();
      const open = a.helpIsOpen();
      const j = JSON.parse(a.panelsJson());
      const walk = (e, id) => { if (e.id === id) return e; for (const k of e.children || []) { const r = walk(k, id); if (r) return r; } return null; };
      const row = walk(a.panels.host.lastPage, "pn-prop-0");
      const key = a.panels.helpKeys[0];
      a.pointerDown(row.calculatedX + 10, row.calculatedY + 6, false, 1);
      a.pointerUp();
      const tab = a.editorTab();
      const hint = a.hintIsOpen();
      const x = walk(a.panels.host.lastPage, "pn-help-close");
      a.pointerDown(x.calculatedX + 6, x.calculatedY + 6, false, 1);
      a.pointerUp();
      const closed = !a.helpIsOpen();
      a.closeHint();
      a.setStyleSheet(css0);
      a.showTab("md");
      a.setSource(src0);
      return { open, drawn: j.list.cmds.length, key, tab, hint, closed };
    });
    check("the help panel is drawn on the canvas", hp.open && hp.drawn > 30, JSON.stringify(hp));
    check("…a property opens in the theme with its popover; ✕ closes the panel", hp.tab === "css" && hp.hint && hp.closed, JSON.stringify(hp));
  }

  // A heading's colour: h2 { color } and heading { color }
  {
    const hc = await page.evaluate(() => {
      const a = window.__app;
      const src0 = a.source();
      const css0 = a.themeCss();
      a.setSource("# Esitys\n\n## Dia\n\nTeksti\n");
      a.setStyleSheet(css0 + "\nh2 {\n  color: #ddacac;\n}\nheading {\n  color: #22c55e;\n}\n");
      a.selectSlide(1);
      const st = JSON.stringify(JSON.parse(a.stageJson()).list);
      a.selectSlide(0);
      const st0 = JSON.stringify(JSON.parse(a.stageJson()).list);
      a.setStyleSheet(css0);
      a.setSource(src0);
      return { h2: st.includes("[221,172,172"), h1: st0.includes("[34,197,94") };
    });
    check("h2 { color } colours the slide titles, heading { color } the rest", hc.h2 && hc.h1, JSON.stringify(hc));
  }

  // Narrow and back: the slides only under 700px, the editor again when wider,
  // and the bar's @media rules follow the width
  {
    const vp = page.viewportSize();
    const narrowBar = async () => page.evaluate(() => {
      const a = window.__app;
      a.toolbarJson();
      const walk = (e, id) => { if (e.id === id) return e; for (const k of e.children || []) { const r = walk(k, id); if (r) return r; } return null; };
      const file = walk(a.toolbar.host.lastPage, "tb-m-file-trigger");
      const menu = walk(a.toolbar.host.lastPage, "tb-menu");
      return { compact: a.isCompact(), fileShown: !!file && file.calculatedWidth > 0, menuShown: !!menu && menu.calculatedWidth > 0 };
    });
    await page.setViewportSize({ width: 560, height: 760 });
    await page.waitForFunction(() => window.__app.isCompact(), null, { timeout: 5000 }).catch(() => {});
    const narrow = await narrowBar();
    await page.setViewportSize(vp);
    await page.waitForFunction(() => !window.__app.isCompact(), null, { timeout: 5000 }).catch(() => {});
    const wide = await narrowBar();
    check("a narrow window shows the slides only and the menu button for a drawer; widening brings the editor and the menus back", narrow.compact && !narrow.fileShown && narrow.menuShown && !wide.compact && wide.fileShown && !wide.menuShown, JSON.stringify({ narrow, wide }));
  }

  // Controls keep their size while they change: the popover's number field
  // during a slide, a chart table cell while typed into; a front-matter value
  // stops at its comment
  {
    const ks = await page.evaluate(() => {
      const a = window.__app;
      const src0 = a.source();
      const walk = (e, id) => { if (e.id === id) return e; for (const k of e.children || []) { const r = walk(k, id); if (r) return r; } return null; };
      a.showTab("md");
      a.setSource("---\nstep: 1.8   # seconds between steps\n---\n\n## O\n\n```vega-lite\n{\"data\": {\"values\": [{\"f\": \"A\", \"u\": 1}, {\"f\": \"B\", \"u\": 2}]}, \"mark\": \"bar\", \"encoding\": {\"x\": {\"field\": \"f\", \"type\": \"nominal\"}, \"y\": {\"field\": \"u\", \"type\": \"quantitative\"}}}\n```\n");
      a.editor.moveCaret(1, 7, false);
      a.openHintAtCaret();
      const value = a.hint.value;
      a.hintJson();
      const w0 = walk(a.hint.host.lastPage, "hp-num").calculatedWidth;
      const sl = walk(a.hint.host.lastPage, "hp-slider");
      a.pointerDown(sl.calculatedX + sl.calculatedWidth * 0.3, sl.calculatedY + 8, false, 1);
      a.pointerMove(sl.calculatedX + sl.calculatedWidth * 0.6, sl.calculatedY + 8);
      a.hintJson();
      const w1 = walk(a.hint.host.lastPage, "hp-num").calculatedWidth;
      const line1 = a.source().split("\n")[1];
      a.pointerUp();
      a.closeHint();
      a.openChartEditor(6);
      a.chartJson();
      const t = walk(a.chart.host.lastPage, "ce-tabs-tab-tiedot");
      a.pointerDown(t.calculatedX + 6, t.calculatedY + 6, false, 1); a.pointerUp();
      a.chartJson();
      const cellId = Object.keys((() => { const m = {}; const w = (e) => { if (/^ce-c-[0-9]+-[0-9]+$/.test(e.id || "")) m[e.id] = 1; for (const k of e.children || []) w(k); }; w(a.chart.host.lastPage); return m; })())[1];
      const c0 = walk(a.chart.host.lastPage, cellId);
      const cw0 = c0.calculatedWidth;
      a.pointerDown(c0.calculatedX + 10, c0.calculatedY + 8, false, 1); a.pointerUp();
      a.text("7");
      a.chartJson();
      const cw1 = walk(a.chart.host.lastPage, cellId).calculatedWidth;
      a.key("escape", false, false);
      if (a.chartIsOpen()) a.closeChart();
      a.setSource(src0);
      return { value, w0, w1, line1, cellId, cw0, cw1 };
    });
    check("a front-matter value stops at its comment, which the slider keeps", ks.value === "1.8" && /# seconds between steps$/.test(ks.line1), JSON.stringify(ks));
    check("the popover's number field keeps its width while the slider moves", Math.abs(ks.w0 - ks.w1) < 0.5, JSON.stringify(ks));
    check("a chart table cell keeps its width while typed into", !!ks.cellId && Math.abs(ks.cw0 - ks.cw1) < 0.5, JSON.stringify(ks));
  }

  // The keyboard and a screen reader: the canvas mirrored as DOM, F6 between
  // regions, Ctrl+Space into the value popover, Esc back to the editor
  {
    const src0 = await page.evaluate(() => window.__app.source());
    await page.evaluate(() => { const a = window.__app; a.showTab("md"); a.setSource("# D\n\n## O {fx=starfield}\n\nTeksti\n"); });
    await page.evaluate(() => document.getElementById("keys").focus());
    await page.waitForTimeout(300);
    const act = () => page.evaluate(() => { const e = document.activeElement; return (e.dataset && e.dataset.a11yId) || e.id; });
    const seen = [];
    for (let i = 0; i < 4; i += 1) { await page.keyboard.press("F6"); seen.push(await act()); }
    await page.evaluate(() => { const a = window.__app; a.showTab("md"); a.editor.moveCaret(2, 10, false); document.getElementById("keys").focus(); });
    await page.keyboard.press("Control+Space");
    await page.waitForTimeout(200);
    const inHint = await act();
    await page.keyboard.press("ArrowRight");
    await page.keyboard.press("Enter");
    await page.waitForTimeout(200);
    const line = await page.evaluate(() => window.__app.source().split("\n")[2]);
    await page.keyboard.press("Escape");
    const back = await act();
    const dom = await page.evaluate(() => ({
      nodes: document.querySelectorAll(".evg-a11y [data-a11y-id]").length,
      bar: [...document.querySelectorAll(".evg-a11y button")].some((b) => /Present/.test(b.textContent + (b.getAttribute("aria-label") || ""))),
      slides: document.querySelectorAll('.evg-a11y [data-a11y-id^="thumb-"]').length,
      status: [...document.querySelectorAll(".evg-a11y [role=status]")].map((e) => e.textContent).join("|"),
      canvasHidden: document.getElementById("c").getAttribute("aria-hidden") === "true",
    }));
    // the chart editor from the keyboard: Ctrl+Space on the fence, Tab to a slider, arrows move it
    await page.evaluate(() => { const a = window.__app; a.setSource("# D\n\n## O\n\n```vega-lite\n{\"data\": {\"values\": [{\"f\": \"A\", \"u\": 1}, {\"f\": \"B\", \"u\": 2}]}, \"mark\": \"bar\", \"width\": 640, \"encoding\": {\"x\": {\"field\": \"f\", \"type\": \"nominal\"}, \"y\": {\"field\": \"u\", \"type\": \"quantitative\"}}}\n```\n"); if (a.chartIsOpen()) a.closeChart(); a.showTab("md"); a.chart.tab = "kaavio"; a.editor.moveCaret(4, 5, false); document.getElementById("keys").focus(); });
    await page.keyboard.press("Control+Space");
    await page.waitForTimeout(200);
    const tabPath = [await act(), await page.evaluate(() => { const a = window.__app; return "open=" + a.chartIsOpen() + " src4=" + a.source().split("\n")[4] + " caret=" + a.editor.sel.caret.line + ":" + a.editor.sel.caret.col + " tab=" + a.editorTab() + " n=" + document.querySelectorAll('.evg-a11y [data-a11y-id^="ce-"]').length + " foc=" + window.__kb.focusables("chart").map((x) => x.id).slice(0, 3).join("/") + " a11y=" + window.__app.a11yFocus + " active=" + (document.activeElement && document.activeElement.id); })];
    for (let i = 0; i < 12 && (await act()) !== "ce-width-thumb"; i += 1) { await page.keyboard.press("Tab"); tabPath.push(await act()); }
    const w0 = await page.evaluate(() => +/"width": ?(\d+)/.exec(window.__app.source())[1]);
    await page.keyboard.press("ArrowRight");
    await page.waitForTimeout(100);
    const w1 = await page.evaluate(() => +/"width": ?(\d+)/.exec(window.__app.source())[1]);
    await page.keyboard.press("Escape");
    const chartBack = await act();
    await page.evaluate((s) => window.__app.setSource(s), src0);
    check("the chart editor from the keyboard: Tab to a slider, an arrow moves it, Esc closes", w1 > w0 && chartBack === "keys", JSON.stringify({ w0, w1, chartBack, tabPath: tabPath.slice(0, 3) }));
    check("F6 goes from region to region (slides, bar, tabs, editor)", seen.some((x) => /^thumb-/.test(x)) && seen.some((x) => /^tb-/.test(x)) && seen.some((x) => /^edtabs/.test(x)) && seen.includes("keys"), seen.join(","));
    check("Ctrl+Space opens the value popover with the keyboard in it; Enter picks, Esc returns", /^hp-opt-/.test(inHint) && !/fx=starfield/.test(line) && /fx=/.test(line) && back === "keys", JSON.stringify({ inHint, line, back }));
    check("the canvas is mirrored for a screen reader: the bar, the slides, where we are", dom.nodes > 10 && dom.bar && dom.slides >= 2 && /Slide \d+ \/ \d+/.test(dom.status) && dom.canvasHidden, JSON.stringify(dom));
  }

  // A chart slider shows on the slide while it moves, and one undo takes the whole drag back
  {
    const lv = await page.evaluate(() => {
      const a = window.__app;
      const src0 = a.source();
      a.showTab("md");
      a.setSource("# D\n\n## O\n\n```vega-lite\n{\"data\": {\"values\": [{\"f\": \"A\", \"u\": 1}, {\"f\": \"B\", \"u\": 2}]}, \"mark\": \"bar\", \"width\": 640, \"encoding\": {\"x\": {\"field\": \"f\", \"type\": \"nominal\"}, \"y\": {\"field\": \"u\", \"type\": \"quantitative\"}}}\n```\n");
      const before = a.source();
      a.chart.tab = "kaavio";
      a.openChartEditor(4);
      a.chartJson();
      const walk = (e, id) => { if (e.id === id) return e; for (const k of e.children || []) { const r = walk(k, id); if (r) return r; } return null; };
      const w = () => { const m = /"width": ?(\d+)/.exec(a.source()); return m ? +m[1] : 0; };
      const sl = walk(a.chart.host.lastPage, "ce-width");
      let t = 500;
      a.setUiTime(t);
      a.pointerDown(sl.calculatedX + sl.calculatedWidth * 0.5, sl.calculatedY + 8, false, 1);
      const seen = [];
      for (const f of [0.6, 0.7, 0.8]) { t += 0.1; a.setUiTime(t); a.pointerMove(sl.calculatedX + sl.calculatedWidth * f, sl.calculatedY + 8); seen.push(w()); }
      a.pointerUp();
      a.key("escape", false, false);
      a.undo();
      const undone = a.source() === before;
      a.setSource(src0);
      return { seen, undone };
    });
    check("a chart slider writes the fence while it moves; one undo takes the drag back", lv.seen[0] !== 640 && lv.seen[1] > lv.seen[0] && lv.seen[2] > lv.seen[1] && lv.undone, JSON.stringify(lv));
  }

  // A slide picked from the strip puts its heading about 15% from the editor's top
  {
    const hv = await page.evaluate(() => {
      const a = window.__app;
      const src0 = a.source();
      a.showTab("md");
      let md = "# Alku\n\n";
      for (let k = 1; k <= 8; k += 1) { md += "## Dia " + k + "\n\n"; for (let j = 0; j < 12; j += 1) md += "Rivi " + j + "\n\n"; }
      a.setSource(md);
      a.editor.layout.scrollLine = 0;
      const n = a.deck.slideCount();
      a.selectSlide(Math.floor(n / 2));
      const lay = a.editor.layout;
      const line = a.editor.sel.caret.line;
      const vis = lay.visibleLineCount();
      const at = (line - lay.scrollLine) / vis;
      const heading = a.editor.buf.lineAt(line);
      a.selectSlide(0);
      const top = lay.scrollLine;
      a.setSource(src0);
      return { at: Math.round(at * 100) / 100, heading, vis, top };
    });
    check("a slide picked from the strip has its heading near the editor's top, not its bottom", hv.at >= 0.1 && hv.at <= 0.2 && hv.top === 0, JSON.stringify(hv));
  }

  // Charts: roomier defaults, the theme's chart { } sizes, and one slide's own
  // (#anchor chart { }), named by its heading's anchor
  {
    const cc = await page.evaluate(() => {
      const a = window.__app;
      const src0 = a.source();
      const css0 = a.themeCss();
      const chart = "```vega-lite\n{\"title\": \"Otsikko\", \"data\": {\"values\": [{\"a\": \"x\", \"b\": 1}, {\"a\": \"y\", \"b\": 2}]}, \"mark\": \"bar\", \"encoding\": {\"x\": {\"field\": \"a\", \"type\": \"nominal\"}, \"y\": {\"field\": \"b\", \"type\": \"quantitative\"}}}\n```\n";
      a.setSource("# D\n\n## Mihin raha menee?\n\n" + chart + "\n## Toinen dia\n\n" + chart.replace("Otsikko", "Toinen"));
      // both drawn, also the one out of view (PresApp.settle draws only those in view)
      a.settleAll();
      // the drawn chart's height and its largest type (its title). A chart
      // that states no height is drawn the room's height whatever its title
      // takes, so a larger title shows in the type, not in a taller box.
      const biggest = (n) => { let m = (n.fontSize && typeof n.fontSize.pixels === "number") ? n.fontSize.pixels : 0; for (const c of n.children || []) m = Math.max(m, biggest(c)); return m; };
      const box = (i) => { const es = a.deck.md.edit.layout.embeds.entries; const want = i === 1 ? "Otsikko" : "Toinen"; const e = es.find((x) => x.source.includes(want)); return e ? { h: Math.round(e.height * 10) / 10, type: Math.round(biggest(e.root) * 10) / 10 } : { h: -1, type: -1 }; };
      const h1 = box(1), h2 = box(2);
      a.selectSlide(1);
      const help = JSON.parse(a.slideHelp()).find((f) => f.key === "chart");
      const sels = help ? help.rules.map((r) => r.sel) : [];
      a.setStyleSheet(css0 + "\n#mihin-raha-menee chart {\n  title-font-size: 44px;\n  title-gap: 40px;\n}\n");
      a.settleAll();
      const s1 = box(1), s2 = box(2);
      a.setStyleSheet(css0);
      a.setSource(src0);
      return { h1, h2, s1, s2, sels };
    });
    check("the help names the slide's own chart rule by its heading's anchor", cc.sels.includes("#mihin-raha-menee chart"), JSON.stringify(cc));
    check("#anchor chart { } sizes only that slide's chart", cc.s1.type >= 43.5 && cc.h1.type < 30 && cc.s2.type === cc.h2.type && cc.s2.h === cc.h2.h && cc.s1.h <= cc.h1.h + 1, JSON.stringify(cc));
  }

  // A smooth line is drawn curved, and its points can be dots
  {
    const sm = await page.evaluate(() => {
      const a = window.__app;
      const src0 = a.source();
      a.setSource("# D\n\n## O\n\n```vega-lite\n{\"data\": {\"values\": [{\"f\": \"A\", \"u\": 38}, {\"f\": \"B\", \"u\": 17}, {\"f\": \"C\", \"u\": 21}, {\"f\": \"D\", \"u\": 24}]}, \"mark\": \"line\", \"width\": 500, \"encoding\": {\"x\": {\"field\": \"f\", \"type\": \"nominal\"}, \"y\": {\"field\": \"u\", \"type\": \"quantitative\"}}}\n```\n");
      a.openChartEditor(4);
      a.chart.model.kind = 9;
      a.chart.model.points = true;
      a.chart.changed = true;
      a.writeChart();
      a.closeChart();
      const spec = a.source();
      a.selectSlide(1);
      const st = JSON.parse(a.stageJson());
      const most = Math.max(...st.list.cmds.filter((c) => c.k === 7).map((c) => (c.pts || []).length));
      const dots = st.list.cmds.filter((c) => c.k === 1 && c.r > 0 && c.w < 40).length;
      a.setSource(src0);
      return { monotone: /"interpolate": ?"monotone"/.test(spec), point: /"point": ?\{/.test(spec), most, dots };
    });
    check("a smooth line is drawn as a curve, with dots on its points when asked", sm.monotone && sm.point && sm.most > 20 && sm.dots >= 4, JSON.stringify(sm));
  }

  // A chart that reads a file: the page fetches it and the chart draws from it
  {
    const src0 = await page.evaluate(() => window.__app.source());
    await page.evaluate(() => window.__app.setSource("# D\n\n## Tiedosto\n\n```vega-lite\n{\"data\": {\"url\": \"data/check-sales.csv\"}, \"mark\": \"bar\", \"width\": 400, \"encoding\": {\"x\": {\"field\": \"month\", \"type\": \"nominal\", \"sort\": null}, \"y\": {\"field\": \"sales\", \"type\": \"quantitative\"}}}\n```\n"));
    await page.waitForFunction(() => window.__app.chartDataWanted() === "" , null, { timeout: 8000 }).catch(() => {});
    await page.waitForTimeout(400);
    const fd = await page.evaluate(() => {
      const a = window.__app;
      a.selectSlide(1);
      const st = JSON.parse(a.stageJson());
      const bars = st.list.cmds.filter((c) => c.k === 0 && c.w > 20 && c.h > 20 && c.w < 200).length;
      return { wanted: a.chartDataWanted(), bars, duration: a.deck.slideAt(1).duration };
    });
    await page.evaluate((s) => window.__app.setSource(s), src0);
    check("a chart's url data is fetched and drawn, the slide timed", fd.wanted === "" && fd.bars >= 3 && fd.duration > 0, JSON.stringify(fd));
  }

  // Live data: a Google Sheet (its CSV, as gviz serves it) is fetched when the
  // deck opens, and again on R while presenting.
  {
    const src0 = await page.evaluate(() => window.__app.source());
    let rows = [["Jan", 120], ["Feb", 95.5], ["Mar", 140]];
    const asked = [];
    const route = (r) => {
      asked.push(r.request().url());
      r.fulfill({ status: 200, contentType: "text/csv", headers: { "access-control-allow-origin": "*" }, body: "\"month\",\"km\"\n" + rows.map(([m, k]) => `"${m}","${k}"`).join("\n") + "\n" });
    };
    await page.context().route(/^https:\/\/docs\.google\.com\/spreadsheets\//, route);
    const barsNow = () => page.evaluate(() => {
      const a = window.__app;
      a.selectSlide(1);
      const st = JSON.parse(a.stageJson());
      return st.list.cmds.filter((c) => c.k === 0 && c.w > 20 && c.h > 20 && c.w < 200).length;
    });
    await page.evaluate(() => window.__app.setSource("# D\n\n## Km\n\n```vega-lite\n{\"data\": {\"source\": \"google-sheets\", \"id\": \"SHEET1\", \"range\": \"Monthly!A:B\"}, \"mark\": \"bar\", \"width\": 400, \"encoding\": {\"x\": {\"field\": \"month\", \"type\": \"nominal\", \"sort\": null}, \"y\": {\"field\": \"km\", \"type\": \"quantitative\"}}}\n```\n"));
    await page.waitForFunction(() => window.__app.chartDataWanted() === "", null, { timeout: 8000 }).catch(() => {});
    await page.waitForTimeout(400);
    const first = await barsNow();
    // while presenting the columns are still rising: count the rows the deck holds
    const rowsHeld = () => page.evaluate(() => {
      const d = window.__app.deck;
      const i = d.dataUrls.findIndex((u) => /SHEET1/.test(u));
      return i < 0 ? -1 : d.dataTexts[i].trim().split("\n").length - 1;
    });
    rows = [...rows, ["Apr", 80], ["May", 160]];
    await page.evaluate(() => window.__app.selectSlide(1));
    await page.keyboard.press("Shift+F5");
    await page.waitForTimeout(500);
    const onPresent = await rowsHeld();
    rows = [...rows, ["Jun", 60]];
    await page.keyboard.press("r");
    await page.waitForTimeout(500);
    const onR = await rowsHeld();
    await page.evaluate(() => window.__app.endPresent());
    await page.waitForTimeout(200);
    const after = await barsNow();
    await page.context().unroute(/^https:\/\/docs\.google\.com\/spreadsheets\//, route);
    await page.evaluate((s) => window.__app.setSource(s), src0);
    const url = asked[0] || "";
    check("a Google Sheet is read as its CSV (tab and range from the fence)", url === "https://docs.google.com/spreadsheets/d/SHEET1/gviz/tq?tqx=out:csv&headers=1&sheet=Monthly&range=A%3AB", url);
    check("live data: drawn on open, fetched again on presenting and on R", first === 3 && onPresent === 5 && onR === 6 && after === 6, JSON.stringify({ first, onPresent, onR, after, asked: asked.length }));
  }
  // A private sheet: the address answers with Google's sign-in page, the
  // signed-in owner's Sheets API read (stubbed here) gives the rows, and a
  // copy is kept with the deck for readers who cannot read the sheet
  {
    const src0 = await page.evaluate(() => window.__app.source());
    const route = (r) => r.fulfill({ status: 200, contentType: "text/html", headers: { "access-control-allow-origin": "*" }, body: "<!doctype html><title>Sign in</title>" });
    await page.context().route(/^https:\/\/docs\.google\.com\/spreadsheets\//, route);
    const res = await page.evaluate(async () => {
      const s = window.sliqtly;
      const was = { user: s.user, readSheet: s.readSheet };
      const asked = [];
      s.user = () => ({ uid: "u1", email: "u@example.com" });
      s.readSheet = async (url, ask) => { asked.push([url, ask]); return "Kk,Km\nTammi,10\nHelmi,20\nMaalis,30\nHuhti,40\n"; };
      const a = window.__app;
      a.setSource("# D\n\n## Km\n\n```vega-lite\n{\"data\": {\"url\": \"https://docs.google.com/spreadsheets/d/PRIV1/edit#gid=0\"}, \"mark\": \"bar\", \"width\": 400, \"encoding\": {\"x\": {\"field\": \"Kk\", \"type\": \"nominal\", \"sort\": null}, \"y\": {\"field\": \"Km\", \"type\": \"quantitative\"}}}\n```\n");
      for (let i = 0; i < 40 && a.chartDataWanted() !== ""; i += 1) await new Promise((r) => setTimeout(r, 100));
      await new Promise((r) => setTimeout(r, 400));
      a.selectSlide(1);
      const st = JSON.parse(a.stageJson());
      const bars = st.list.cmds.filter((c) => c.k === 0 && c.w > 20 && c.h > 20 && c.w < 300).length;
      const copy = await window.__liveCopy("https://docs.google.com/spreadsheets/d/PRIV1/gviz/tq?tqx=out:csv&headers=1&gid=0");
      s.user = was.user;
      s.readSheet = was.readSheet;
      return { bars, asked, copy: (copy || "").split("\n").length };
    });
    await page.context().unroute(/^https:\/\/docs\.google\.com\/spreadsheets\//, route);
    await page.evaluate((s) => window.__app.setSource(s), src0);
    check("a private sheet is read as its signed-in owner, without a popup on open", res.bars === 4 && res.asked.length === 1 && res.asked[0][1] === false && /PRIV1\/gviz/.test(res.asked[0][0]), JSON.stringify(res));
    check("…and a copy is kept with the deck for its readers", res.copy === 6, JSON.stringify(res));
  }

  // Files: the copy kept of a linked sheet is named after the sheet and its
  // tab, shown as linked data, and Unlink makes the chart read it as a file
  {
    const src0 = await page.evaluate(() => window.__app.source());
    const route = (r) => r.fulfill({ status: 200, contentType: "text/html", headers: { "access-control-allow-origin": "*" }, body: "<!doctype html><title>Sign in</title>" });
    await page.context().route(/^https:\/\/docs\.google\.com\/spreadsheets\//, route);
    await page.evaluate(async () => {
      const s = window.sliqtly;
      window.__was = { user: s.user, readSheet: s.readSheet, sheetName: s.sheetName };
      s.user = () => ({ uid: "u1", email: "u@example.com" });
      s.readSheet = async () => "Kk,Km\nTammi,10\nHelmi,20\n";
      s.sheetName = () => ({ title: "Budjetti", tab: "Syyskuu" });
      const a = window.__app;
      a.setSource("# D\n\n## Km\n\n```vega-lite\n{\"data\": {\"url\": \"https://docs.google.com/spreadsheets/d/PRIV3/edit#gid=0\"}, \"mark\": \"bar\", \"width\": 400, \"encoding\": {\"x\": {\"field\": \"Kk\", \"type\": \"nominal\"}, \"y\": {\"field\": \"Km\", \"type\": \"quantitative\"}}}\n```\n");
      for (let i = 0; i < 40 && a.chartDataWanted() !== ""; i += 1) await new Promise((r) => setTimeout(r, 100));
      await new Promise((r) => setTimeout(r, 400));
      a.showTab("files");
    });
    await page.waitForFunction(() => window.__app.panels.filesJson.includes("Budjetti"), null, { timeout: 8000 }).catch(() => {});
    const row = await page.evaluate(() => {
      try { return JSON.parse(window.__app.panels.filesJson).files.find((f) => f.path.startsWith("data/live/")) || null; } catch (_) { return null; }
    });
    const res = await page.evaluate(async (path) => {
      if (path) await window.__fileRequest("unlink:" + path);
      await new Promise((r) => setTimeout(r, 400));
      const files = await window.__docFiles();
      const a = window.__app;
      const md = a.source();
      a.selectSlide(1);
      const st = JSON.parse(a.stageJson());
      const bars = st.list.cmds.filter((c) => c.k === 0 && c.w > 20 && c.h > 20 && c.w < 300).length;
      Object.assign(window.sliqtly, window.__was);
      a.showTab("md");
      return { file: files.includes("data/Budjetti-Syyskuu.csv"), copy: files.includes(path), md: /"url": ?"data\/Budjetti-Syyskuu\.csv"/.test(md), bars };
    }, row && row.path);
    await page.context().unroute(/^https:\/\/docs\.google\.com\/spreadsheets\//, route);
    await page.evaluate((s) => window.__app.setSource(s), src0);
    check("Files: a linked sheet's copy is named after the sheet and tab, with its source", !!row && row.kind === "live" && row.title === "Budjetti · Syyskuu" && row.tag === "SHEET" && /PRIV3/.test(row.source) && /Google Sheets · /.test(row.note), JSON.stringify(row));
    check("…and Unlink makes the chart read it as an ordinary data file", res.file && !res.copy && res.md && res.bars === 2, JSON.stringify(res));
  }

  // A private sheet pasted where the browser blocks Google's window (the
  // press spent on the fetch): a card asks for one more press, which opens it
  {
    const src0 = await page.evaluate(() => window.__app.source());
    const route = (r) => r.fulfill({ status: 200, contentType: "text/html", headers: { "access-control-allow-origin": "*" }, body: "<!doctype html><title>Sign in</title>" });
    await page.context().route(/^https:\/\/docs\.google\.com\/spreadsheets\//, route);
    await page.evaluate(() => {
      const s = window.sliqtly;
      window.__was = { user: s.user, readSheet: s.readSheet, askSheets: s.askSheets };
      let tok = null;
      s.user = () => ({ uid: "u1", email: "u@example.com" });
      let asks = 0;
      // the first window (from the confirm card's press) blocked, the second opens
      s.askSheets = async () => {
        asks += 1;
        if (asks === 1) throw Object.assign(new Error("blocked"), { code: "auth/popup-blocked" });
        tok = "tok";
        return tok;
      };
      s.readSheet = async () => {
        if (!tok) throw Object.assign(new Error("blocked"), { code: "auth/popup-blocked" });
        return "Kk,Km\nTammi,10\nHelmi,20\n";
      };
      const a = window.__app;
      a.setSource("# D\n\n## Km\n\n");
      a.showTab("md");
      a.editor.moveCaret(4, 0, false);
      const keys = document.getElementById("keys");
      keys.focus();
      const dt = new DataTransfer();
      dt.setData("text/plain", "https://docs.google.com/spreadsheets/d/PRIV2/edit#gid=0");
      keys.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
    });
    const asked = await page.waitForSelector("#gLink button.primary", { timeout: 5000 }).then(() => true).catch(() => false);
    if (asked) await page.click("#gLink button.primary");
    const card = await page.waitForSelector("#gTap button.primary", { timeout: 5000 }).then(() => true).catch(() => false);
    if (card) await page.click("#gTap button.primary");
    await page.waitForFunction(() => window.__app.shareIsOpen(), null, { timeout: 5000 }).catch(() => {});
    const after = await page.evaluate(() => {
      const a = window.__app;
      const r = { open: a.shareIsOpen(), live: !!(a.panels.imp && a.panels.imp.live), rows: a.panels.imp ? a.panels.imp.rows : -1, card: !!document.getElementById("gTap") };
      if (a.shareIsOpen()) a.closeShare();
      Object.assign(window.sliqtly, window.__was);
      return r;
    });
    await page.context().unroute(/^https:\/\/docs\.google\.com\/spreadsheets\//, route);
    await page.evaluate((s) => window.__app.setSource(s), src0);
    check("a blocked Google window: a card asks for a press, then the sheet is linked", asked && card && after.open && after.live && after.rows === 2 && !after.card, JSON.stringify({ asked, card, ...after }));
  }

  // A sheet link pasted into the editor: "Link live data", then a chart and a
  // table that read the sheet live
  {
    const src0 = await page.evaluate(() => window.__app.source());
    const route = (r) => r.fulfill({ status: 200, contentType: "text/csv", headers: { "access-control-allow-origin": "*" }, body: '"Kuukausi","Km"\n"Tammi","120"\n"Helmi","95"\n"Tammi","30"\n"Maalis","140"\n' });
    await page.context().route(/^https:\/\/docs\.google\.com\/spreadsheets\//, route);
    const link = "https://docs.google.com/spreadsheets/d/SHEET2/edit?gid=0#gid=0";
    const res = await page.evaluate(async (link) => {
      const a = window.__app;
      a.setSource("# D\n\n## Km\n\n");
      a.showTab("md");
      a.editor.moveCaret(4, 0, false);
      const keys = document.getElementById("keys");
      keys.focus();
      const dt = new DataTransfer();
      dt.setData("text/plain", link);
      keys.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
      // first asked whether the sheet is linked to the document
      let card = null;
      for (let i = 0; i < 50 && !(card = document.querySelector("#gLink button.primary")); i += 1) await new Promise((r) => setTimeout(r, 100));
      const asked = !!card && !a.shareIsOpen() && !a.source().includes("docs.google.com");
      if (card) card.click();
      for (let i = 0; i < 50 && !a.shareIsOpen(); i += 1) await new Promise((r) => setTimeout(r, 100));
      const imp = a.panels.imp;
      const dialog = { asked, open: a.shareIsOpen(), live: imp && imp.live, rows: imp && imp.rows, pasted: a.source().includes("docs.google.com") };
      a.panels.requests.push("data:chart");
      a.takePanels();
      window.__handleRequests && window.__handleRequests();
      if (a.chartIsOpen()) a.closeChart();
      await new Promise((r) => setTimeout(r, 600));
      const src = a.source();
      a.selectSlide(1);
      const st = JSON.parse(a.stageJson());
      const bars = st.list.cmds.filter((c) => c.k === 0 && c.w > 20 && c.h > 20 && c.w < 300).length;
      return { dialog, fence: /"url": ?"https:\/\/docs\.google\.com\/spreadsheets\/d\/SHEET2\/edit/.test(src) && /"fold"/.test(src) && !/"values"/.test(src), bars, kept: src.length };
    }, link);
    await page.context().unroute(/^https:\/\/docs\.google\.com\/spreadsheets\//, route);
    await page.evaluate((s) => window.__app.setSource(s), src0);
    check("a pasted sheet link asks first, then opens Link live data before anything is pasted", res.dialog.asked && res.dialog.open && res.dialog.live && res.dialog.rows === 4 && !res.dialog.pasted, JSON.stringify(res.dialog));
    check("…and makes a chart that reads the sheet (no copied values), drawn", res.fence && res.bars === 3, JSON.stringify(res));
  }
  // …or a table that reads the sheet live, paged on its slide
  {
    const src0 = await page.evaluate(() => window.__app.source());
    let body = '"Kuukausi","Km"\n' + Array.from({ length: 12 }, (_, i) => `"K${i}","${100 + i}"`).join("\n") + "\n";
    const route = (r) => r.fulfill({ status: 200, contentType: "text/csv", headers: { "access-control-allow-origin": "*" }, body });
    await page.context().route(/^https:\/\/docs\.google\.com\/spreadsheets\//, route);
    const link = "https://docs.google.com/spreadsheets/d/SHEET3/edit?gid=0#gid=0";
    const texts = () => page.evaluate(() => { const a = window.__app; const u = a.deck.tables[0]; if (!u) return null; a.selectSlide(u.slide); return { pages: u.pages(), t: JSON.parse(a.stageJson()).list.cmds.filter((c) => c.k === 3).map((c) => c.text) }; });
    const made = await page.evaluate(async (link) => {
      const a = window.__app;
      a.setSource("# D\n\n## Km\n\n");
      a.showTab("md");
      a.editor.moveCaret(4, 0, false);
      const keys = document.getElementById("keys");
      keys.focus();
      const dt = new DataTransfer();
      dt.setData("text/plain", link);
      keys.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
      let card = null;
      for (let i = 0; i < 50 && !(card = document.querySelector("#gLink button.primary")); i += 1) await new Promise((r) => setTimeout(r, 100));
      if (card) card.click();
      for (let i = 0; i < 50 && !(a.shareIsOpen() && a.panels.imp && a.panels.imp.rows === 12); i += 1) await new Promise((r) => setTimeout(r, 100));
      a.panels.requests.push("data:table");
      a.takePanels();
      window.__handleRequests && window.__handleRequests();
      await new Promise((r) => setTimeout(r, 800));
      return a.source();
    }, link);
    const first = await texts();
    // the sheet changes: R reads it again and the table follows
    body = '"Kuukausi","Km"\n"Uusi","7"\n';
    await page.evaluate(() => document.getElementById("vData").click());
    let second = null;
    for (let i = 0; i < 30; i++) { await page.waitForTimeout(100); second = await texts(); if (second && second.t.includes("Uusi")) break; }
    await page.context().unroute(/^https:\/\/docs\.google\.com\/spreadsheets\//, route);
    await page.evaluate((s) => window.__app.setSource(s), src0);
    const res = { fence: /```table\nhttps:\/\/docs\.google\.com\/spreadsheets\/d\/SHEET3/.test(made), copied: /data\/[^\n]*\.csv/.test(made.split("```table")[1] || ""), first: first && { pages: first.pages, k0: first.t.includes("K0"), k11: first.t.includes("K11") }, refreshed: !!second && second.t.includes("Uusi") };
    check("…or makes a table that reads the sheet live, paged on its slide", res.fence && !res.copied && res.first && res.first.pages === 2 && res.first.k0 && !res.first.k11, JSON.stringify(res));
    check("…and a re-read sheet redraws the table", res.refreshed, JSON.stringify(res));
  }
  // …and "Paste as text" on that question only pastes the link
  {
    const src0 = await page.evaluate(() => window.__app.source());
    const res = await page.evaluate(async () => {
      const a = window.__app;
      a.setSource("# D\n\n## Km\n\n");
      a.showTab("md");
      a.editor.moveCaret(4, 0, false);
      const keys = document.getElementById("keys");
      keys.focus();
      const dt = new DataTransfer();
      if (a.shareIsOpen()) a.closeShare?.();
      const before = a.shareIsOpen();
      dt.setData("text/plain", "https://docs.google.com/spreadsheets/d/SHEET3/edit");
      keys.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
      let no = null;
      for (let i = 0; i < 50 && !(no = document.querySelector("#gLink button:not(.primary)")); i += 1) await new Promise((r) => setTimeout(r, 100));
      if (no) no.click();
      await new Promise((r) => setTimeout(r, 300));
      return { card: !!no, pasted: a.source().includes("spreadsheets/d/SHEET3/edit"), dialog: a.shareIsOpen() && !before, gone: !document.getElementById("gLink") };
    });
    await page.evaluate((s) => window.__app.setSource(s), src0);
    check("…and Paste as text on that question pastes the link only", res.card && res.pasted && !res.dialog && res.gone, JSON.stringify(res));
  }


  // The document's own files (web/vfs.js): a changed deck is kept in the
  // browser, files added in the files tab go with it, a chart can live in a
  // file, an edited data file redraws the charts, and a reload opens the deck
  // worked on last.
  {
    const src0 = await page.evaluate(() => window.__app.source());
    const idbDocs = () => page.evaluate(() => new Promise((res) => {
      const r = indexedDB.open("evg-presentation");
      r.onsuccess = () => { const q = r.result.transaction("docs").objectStore("docs").getAll(); q.onsuccess = () => res(q.result); };
      r.onerror = () => res([]);
    }));
    await page.waitForTimeout(1800);
    const kept = (await idbDocs()).length;
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "evgp-"));
    fs.writeFileSync(path.join(dir, "vfs-sales.csv"), "kk,euroa\ntammi,120\nhelmi,180\nmaalis,90\n");
    fs.writeFileSync(path.join(dir, "vfs-chart.json"), JSON.stringify({ $schema: "https://vega.github.io/schema/vega-lite/v6.json", width: 300, data: { url: "data/vfs-sales.csv" }, mark: "bar", encoding: { x: { field: "kk", type: "nominal", sort: null }, y: { field: "euroa", type: "quantitative" } } }));
    await page.evaluate(() => window.__app.showTab("files"));
    await page.setInputFiles("#fileadd", [path.join(dir, "vfs-sales.csv"), path.join(dir, "vfs-chart.json")]);
    await page.waitForFunction(() => window.__app.panels.filesJson.includes("charts/vfs-chart.json"), null, { timeout: 8000 }).catch(() => {});
    const listed = await page.evaluate(() => window.__app.panels.filesJson);
    await shot("vfs-files.png");
    await page.evaluate(() => { window.__app.showTab("md"); window.__app.setSource("# V\n\n## Tiedostosta\n\n```vega-lite\ncharts/vfs-chart.json\n```\n"); });
    await page.waitForTimeout(800);
    const barsOf = () => page.evaluate(() => {
      const a = window.__app;
      a.selectSlide(1);
      const st = JSON.parse(a.stageJson());
      return st.list.cmds.filter((c) => c.k === 0 && c.w > 10 && c.h > 10 && c.w < 200).map((c) => Math.round(c.h));
    });
    const bars1 = await barsOf();
    // the data file edited in the files tab: the chart follows
    // Open on a CSV: the spreadsheet editor (EVGSheets), its rows in the cells
    await page.evaluate(() => {
      const ls = window.__liveSheets;
      const was = ls.openDialog;
      ls.openDialog = (o) => { window.__sheetAsked = { name: o.name, csv: o.csv }; ls.openDialog = was; return was(o); };
      const a = window.__app;
      a.panels.requests.push("files:open:data/vfs-sales.csv");
      a.takePanels();
    });
    await page.waitForFunction(() => !!document.querySelector(".sheet-dialog canvas") || window.__app.openFilePath() === "data/vfs-sales.csv", null, { timeout: 30000 }).catch(() => {});
    const csvOpen = await page.evaluate(() => ({ asked: window.__sheetAsked || null, dialog: !!document.querySelector(".sheet-dialog canvas"), name: document.querySelector(".sheet-dialog-bar strong")?.textContent || "", asText: window.__app.openFilePath() === "data/vfs-sales.csv" }));
    await page.evaluate(() => [...document.querySelectorAll(".sheet-dialog-bar button")].find((b) => !b.classList.contains("primary"))?.click());
    await page.waitForTimeout(300);
    // without EVGSheets (no copy beside the page, its site out of reach) the
    // text editor is the fallback
    const sheetsHere = fs.existsSync(path.join(distDir, "sheets", "evgsheets.mjs"));
    const asked = csvOpen.asked && csvOpen.asked.name === "vfs-sales.csv" && /^kk,euroa/i.test(csvOpen.asked.csv || "");
    check("Open on a CSV file: the spreadsheet editor, not the text" + (sheetsHere ? "" : " (EVGSheets unreachable: asked, then the text)"),
      asked && (sheetsHere ? csvOpen.dialog && csvOpen.name === "vfs-sales.csv" && !csvOpen.asText : csvOpen.asText), JSON.stringify(csvOpen).slice(0, 200));
    // the text editor still edits a data file (JSON, or a CSV without EVGSheets)
    await page.evaluate(() => { const a = window.__app; a.showTab("files"); a.openFile("data/vfs-sales.csv", "kk,euroa\n"); });
    await page.waitForFunction(() => window.__app.openFilePath() === "data/vfs-sales.csv", null, { timeout: 5000 }).catch(() => {});
    await page.evaluate(() => { const a = window.__app; a.fileEditor.init("kk,euroa\ntammi,120\nhelmi,180\nmaalis,90\nhuhti,300\n"); a.syncEditor(); });
    await page.waitForTimeout(1000);
    const bars2 = await barsOf();
    // the chart editor on a fence that names a file: it opens on the file's
    // spec, and its changes go into the file, not into the fence
    const viaFile = await page.evaluate(() => {
      const a = window.__app;
      a.showTab("md");
      const lines = a.source().split("\n");
      const at = lines.findIndex((l) => l.startsWith("```vega-lite"));
      const opened = a.openChartEditor(at);
      const look = a.chart.model.lookOnly;
      a.chart.model.title = "Myynti tiedostosta";
      a.chart.changed = true;
      a.closeChart();
      return { opened, look, fence: a.source().includes("charts/vfs-chart.json"), file: a.chartFileBody() };
    });
    await page.waitForTimeout(1800);
    // a reload with no sample asked for opens this deck again, files and all
    await page.goto(url);
    await page.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    await page.waitForTimeout(1500);
    const back = await page.evaluate(() => window.__app.source());
    const fileBack = await page.evaluate(() => window.__app.deck.dataTexts.join("\n"));
    const bars3 = await barsOf();
    check("a changed deck is kept in the browser", kept >= 1, String(kept));
    check("files added in the files tab are listed in their folders", listed.includes("data/vfs-sales.csv") && listed.includes("charts/vfs-chart.json"), listed.slice(0, 200));
    check("a chart kept in a file draws from the document's data", bars1.length === 3, JSON.stringify(bars1));
    check("an edited data file redraws the chart", bars2.length === 4, JSON.stringify(bars2));
    check("the chart editor edits a chart kept in a file, in the file", viaFile.opened && viaFile.fence && viaFile.file.includes("Myynti tiedostosta") && fileBack.includes("Myynti tiedostosta"), JSON.stringify(viaFile).slice(0, 160));
    check("a reload opens the deck worked on last, with its files", back.includes("charts/vfs-chart.json") && bars3.length === 4, JSON.stringify(bars3));
    await page.goto(url + "?sample=esittely");
    await page.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    await page.waitForTimeout(500);
    await page.evaluate((s) => window.__app.setSource(s), src0);
  }

  // Pictures: a size and a place from the attribute line, fitted into the
  // slide; the picture window rewrites that line and can make the picture a
  // slide's background; a header and a footer on every slide.
  {
    const src0 = await page.evaluate(() => window.__app.source());
    const png = (w, h) => {
      const crc = (buf) => { let c = ~0; for (const b of buf) { c ^= b; for (let k = 0; k < 8; k += 1) c = (c >>> 1) ^ (0xedb88320 & -(c & 1)); } return ~c >>> 0; };
      const chunk = (type, data) => { const t = Buffer.from(type); const len = Buffer.alloc(4); len.writeUInt32BE(data.length); const c = Buffer.alloc(4); c.writeUInt32BE(crc(Buffer.concat([t, data]))); return Buffer.concat([len, t, data, c]); };
      const ihdr = Buffer.alloc(13); ihdr.writeUInt32BE(w, 0); ihdr.writeUInt32BE(h, 4); ihdr[8] = 8; ihdr[9] = 2;
      const raw = Buffer.alloc((w * 3 + 1) * h); for (let y = 0; y < h; y += 1) for (let x = 0; x < w; x += 1) { const o = y * (w * 3 + 1) + 1 + x * 3; raw[o] = 250; raw[o + 1] = 120 + y; raw[o + 2] = 60; }
      return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk("IHDR", ihdr), chunk("IDAT", zlib.deflateSync(raw)), chunk("IEND", Buffer.alloc(0))]);
    };
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "evgp-img-"));
    fs.writeFileSync(path.join(dir, "check-pic.png"), png(300, 200));
    await page.setInputFiles("#fileadd", [path.join(dir, "check-pic.png")]);
    await page.waitForTimeout(800);
    await page.evaluate(() => {
      const a = window.__app;
      a.showTab("md");
      a.setSource("---\nheader: ACME Oy\nfooter: {page} / {pages}\n---\n\n# K\n\n## Kuva\n\n![kuva](media/check-pic.png)\n{width=30% align=right}\n\n## Toinen\n\nTekstiä.\n");
    });
    await page.waitForTimeout(600);
    const pic = await page.evaluate(() => {
      const a = window.__app;
      a.selectSlide(1);
      const st = JSON.parse(a.stageJson());
      const img = st.list.cmds.find((c) => c.k === 2 && /check-pic/.test(c.src || ""));
      const texts = st.list.cmds.filter((c) => c.k === 3).map((c) => c.text || "");
      return { img: img ? [Math.round(img.x), Math.round(img.w), Math.round(img.h)] : null, header: texts.includes("ACME Oy"), footer: texts.includes("2 / 3"), w: a.deck.pageW };
    });
    // the picture window: the place, then the slide's background
    const ed = await page.evaluate(() => {
      const a = window.__app;
      const lines = a.source().split("\n");
      const at = lines.findIndex((l) => l.startsWith("![kuva]"));
      const opened = a.openImageEditor(at);
      a.chart.img.align = "center";
      a.chart.img.width = "50%";
      a.chart.changed = true;
      a.closeChart();
      const after = a.source().split("\n")[at + 1];
      a.openImageEditor(at);
      a.chart.wantsBg = "slide";
      a.chart.wantsClose = true;
      a.afterChart();
      const src = a.source();
      a.selectSlide(1);
      const st = JSON.parse(a.stageJson());
      const bg = st.list.cmds.find((c) => c.k === 2 && /check-pic/.test(c.src || "") && c.w >= a.deck.pageW - 1);
      return { opened, after, heading: src.includes("## Kuva {bg=media/check-pic.png}"), gone: !src.includes("![kuva]"), bg: !!bg };
    });
    await page.evaluate((s) => window.__app.setSource(s), src0);
    check("a picture's width and place come from its attribute line", pic.img && Math.abs(pic.img[1] - (pic.w - 2 * 54) * 0.3) < 40 && pic.img[0] > pic.w * 0.5, JSON.stringify(pic));
    check("a header and a footer are on every slide, the page numbers filled in", pic.header && pic.footer, JSON.stringify(pic));
    check("the picture window rewrites the attribute line", ed.opened && ed.after === "{width=50% align=center}", JSON.stringify(ed));
    check("…and makes the picture the slide's background, covering it", ed.heading && ed.gone && ed.bg, JSON.stringify(ed));
  }

  // The help panel: only what the slide has, and a property opened in the theme
  const help = await page.evaluate(() => {
    const a = window.__app;
    const src0 = a.source();
    const css0 = a.themeCss();
    a.setSource("# M\n\n## Muotoilu\n\n- <mark>tärkeä</mark> ja <kbd>Ctrl</kbd>\n\n## Kaava\n\nPinta-ala $A = \\pi r^2$\n");
    a.selectSlide(1);
    const first = JSON.parse(a.slideHelp()).map((f) => f.key);
    a.selectSlide(2);
    const second = JSON.parse(a.slideHelp()).map((f) => f.key);
    a.helpEdit("mark", "background-color", "#ffe766");
    const tab = a.editorTab();
    const hint = JSON.parse(a.hintAtCaret() || "null");
    const added = /mark \{\s*background-color: #ffe766;\s*\}/.test(a.themeCss());
    a.setStyleSheet(css0);
    a.setSource(src0);
    return { first, second, tab, hint: hint && hint.name, added };
  });
  check("help lists the slide's highlight and key, not formulas", help.first.includes("mark") && help.first.includes("kbd") && help.first.includes("list") && !help.first.includes("math"), help.first.join(","));
  check("…and formulas only where there is one", help.second.includes("math") && !help.second.includes("mark"), help.second.join(","));
  check("a property from the help opens in the theme with its popover", help.added && help.tab === "css" && help.hint === "background-color", JSON.stringify(help));

  // Hints: what a value is and what it can be, and a choice written back
  const hints = await page.evaluate(() => {
    const a = window.__app;
    const src0 = a.source();
    a.showTab("md");
    a.setSource("---\ntransition: fade\n---\n\n# Otsikko {fx=starfield fx-density=1.2}\n\nTeksti\n{.lead}\n");
    const at = (line, word, off) => {
      const text = a.source().split("\n")[line];
      a.editor.moveCaret(line, text.indexOf(word) + (off || 1), false);
      const j = a.hintAtCaret();
      return j ? JSON.parse(j) : null;
    };
    const out = {};
    const fx = at(4, "starfield");
    out.fx = fx && fx.kind === "enum" && fx.options.includes("smoke") && fx.adds.some((x) => x.startsWith("fx-hue="));
    a.replaceRange(4, fx.start, fx.end, "smoke");
    out.picked = a.source().split("\n")[4].includes("{fx=smoke fx-density=1.2}");
    const dens = at(4, "1.2");
    out.density = dens && dens.kind === "number" && dens.max > 1.2;
    a.replaceRange(4, dens.wholeStart, dens.wholeEnd, "");
    out.removed = a.source().split("\n")[4].endsWith("{fx=smoke}");
    const front = at(1, "fade");
    out.front = front && front.kind === "enum" && front.options.includes("zoom");
    const cls = at(7, "lead");
    out.cls = cls && cls.kind === "class";
    out.plain = at(6, "Teksti") === null;
    a.setSource(src0);
    a.showTab("css");
    const lines = a.themeCss().split("\n");
    const find = (re) => lines.findIndex((l) => re.test(l));
    const hint = (re, off) => {
      const li = find(re);
      a.editor.moveCaret(li, lines[li].indexOf(":") + (off || 3), false);
      const j = a.hintAtCaret();
      return j ? JSON.parse(j) : null;
    };
    out.color = (hint(/^\s*accent-color:/) || {}).kind === "color";
    out.number = (hint(/^\s*font-size:/) || {}).unit === "pt";
    out.font = ((hint(/^\s*font-family:/) || {}).options || []).includes("Noto Sans");
    out.pretty = lines.some((l) => /^\s+chart-style: \w+;$/.test(l));
    a.showTab("md");
    return out;
  });
  check("an effect's hint lists the others and its parameters", hints.fx, JSON.stringify(hints));
  check("a choice is written into the text", hints.picked && hints.removed, JSON.stringify(hints));
  check("a parameter is a number with a range", hints.density, JSON.stringify(hints));
  check("front matter and classes have hints; plain text none", hints.front && hints.cls && hints.plain, JSON.stringify(hints));
  check("the theme's colours, sizes and faces have hints", hints.color && hints.number && hints.font, JSON.stringify(hints));
  check("the theme CSS is one declaration per line", hints.pretty, JSON.stringify(hints));

  // An emoji is measured as wide as the browser draws it, so the caret after
  // one is at the end of the text and the space after one on a slide is
  // there. ♨️ and 🍽️ carry U+FE0F, 👨‍👩‍👧 is joined, 🇫🇮 a pair, 1️⃣ a keycap:
  // each is ONE picture the text faces know nothing about.
  const emoji = await page.evaluate(() => {
    const a = window.__app;
    const c = document.createElement("canvas").getContext("2d");
    c.font = "13px 'Open Sans'";
    const lines = ["## ✨ Key Features 📈 {fx=a}", "- ♨️ **Saunaan** – 🍽️ x", "👨‍👩‍👧 🇫🇮 1️⃣ 👍🏽 ok"];
    return lines.map((s) => ({ s, ours: a.tr.measureWidth(s, 13), slide: a.measurer.measureTextWidth(s, "Open Sans", 13), browser: c.measureText(s).width }));
  });
  check("a line with emoji is measured as the browser draws it", emoji.every((e) => Math.abs(e.ours - e.browser) < 1.5), JSON.stringify(emoji));
  check("…and so is the same line on a slide", emoji.every((e) => Math.abs(e.slide - e.browser) < 1.5), JSON.stringify(emoji));

  // The caret steps over a whole emoji, never between ♨ and its U+FE0F
  const caret = await page.evaluate(() => {
    const a = window.__app;
    const src0 = a.source();
    a.setSource("a♨️b👨‍👩‍👧c");
    a.setFocus("editor");
    a.editor.moveCaret(0, 1, false);
    a.key("right", false, false);
    const afterHot = a.editor.sel.caret.col;
    a.key("right", false, false);
    a.key("right", false, false);
    const afterFam = a.editor.sel.caret.col;
    a.editor.moveCaret(0, 2, false);
    const snapped = a.editor.sel.caret.col;
    a.setSource(src0);
    return { afterHot, afterFam, snapped };
  });
  check("the caret steps over an emoji as one character", caret.afterHot === 3 && caret.afterFam === 12 && caret.snapped === 1, JSON.stringify(caret));

  // The editor never sits scrolled sideways past every line on screen, and a
  // sideways swipe scrolls it sideways
  const hs = await page.evaluate(() => {
    const a = window.__app;
    const src0 = a.source();
    const lines = ["# Pitkä rivi", "x".repeat(400)];
    for (let i = 0; i < 80; i += 1) lines.push("lyhyt " + i);
    a.setSource(lines.join("\n"));
    a.showTab("md");
    a.setFocus("editor");
    a.key("pageUp", false, true);
    a.key("down", false, false);
    a.key("end", false, false);
    const lay = () => a.editor.layout.scrollX;
    const out = { atEnd: lay() };
    a.key("down", false, false);
    out.shortLine = lay();
    a.key("up", false, false);
    a.key("end", false, false);
    const r = a.edRect;
    const cx = r.x + r.w / 2;
    const cy = r.y + r.h / 2;
    a.wheelXY(cx, cy, 0, 400);
    out.scrolledDown = lay();
    a.wheelXY(cx, cy, 0, -2000);
    a.wheelXY(cx, cy, 300, 0);
    out.swipe = lay();
    a.wheelXY(cx, cy, -5000, 0);
    out.swipeBack = lay();
    a.setSource(src0);
    return out;
  });
  check("the view follows the caret to the end of a long line", hs.atEnd > 0, JSON.stringify(hs));
  check("…and comes back when the caret moves to a short line", hs.shortLine === 0, JSON.stringify(hs));
  check("scrolled down among short lines the view is not left empty", hs.scrolledDown === 0, JSON.stringify(hs));
  check("a sideways swipe scrolls the editor sideways, both ways", hs.swipe > 0 && hs.swipeBack === 0, JSON.stringify(hs));

  const exp = await page.evaluate(() => {
    const a = window.__app;
    a.setSource(a.source());
    const toB64 = (buf) => {
      const u = new Uint8Array(buf);
      let s = "";
      for (let i = 0; i < u.length; i += 1) s += String.fromCharCode(u[i]);
      return btoa(s);
    };
    return { pdf: toB64(a.pdf()), pptx: toB64(a.pptx()) };
  });
  const pdf = Buffer.from(exp.pdf, "base64");
  check("PDF export", pdf.subarray(0, 5).toString() === "%PDF-", `${pdf.length} bytes`);
  // A slide's effect goes into both exports as a picture under the content
  const fxExp = await page.evaluate(async () => {
    const a = window.__app;
    const src0 = a.source();
    a.setSource("# E\n\n## Tähdet {fx=starfield}\n\nteksti\n\n## Sade {fx=raindrop}\n\nteksti\n\n## Ilman\n\nteksti\n");
    const toB64 = (buf) => {
      const u = new Uint8Array(buf);
      let s = "";
      for (let i = 0; i < u.length; i += 1) s += String.fromCharCode(u[i]);
      return btoa(s);
    };
    await window.__renderFxStills();
    const pdf = toB64(a.pdf());
    await window.__renderFxStills();
    const pptx = toB64(a.pptx());
    a.setSource(src0);
    return { pdf, pptx };
  });
  const fxPdf = Buffer.from(fxExp.pdf, "base64").toString("latin1");
  const fxImages = (fxPdf.match(/\/Subtype \/Image/g) || []).length;
  check("PDF: each slide's effect is a picture", fxImages === 2, `${fxImages} images`);
  // A JPEG as a slide's background and as the header's logo, and a picture
  // the deck has no file for: the PDF has no file system to open them from
  // (it once failed with "require is not defined").
  {
    const jpg = await page.evaluate(() => {
      const c = document.createElement("canvas");
      c.width = 64;
      c.height = 48;
      const g = c.getContext("2d");
      g.fillStyle = "#c33";
      g.fillRect(0, 0, 64, 48);
      return c.toDataURL("image/jpeg").split(",")[1];
    });
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "evgp-jpg-"));
    fs.writeFileSync(path.join(dir, "check-bg.jpg"), Buffer.from(jpg, "base64"));
    await page.setInputFiles("#fileadd", [path.join(dir, "check-bg.jpg")]);
    await page.waitForTimeout(800);
    const jp = await page.evaluate(() => {
      const a = window.__app;
      const src0 = a.source();
      a.setSource("---\nheader-right: ![](media/check-bg.jpg)\n---\n\n# J\n\n## Tausta {bg=media/check-bg.jpg}\n\nteksti\n\n## Puuttuu\n\n![x](media/ei-ole.jpg)\n");
      let out;
      try {
        const u = new Uint8Array(a.pdf());
        let s = "";
        for (let i = 0; i < u.length; i += 1) s += String.fromCharCode(u[i]);
        out = { head: s.slice(0, 5), images: (s.match(/\/Subtype \/Image/g) || []).length };
      } catch (e) {
        out = { error: String(e) };
      }
      a.setSource(src0);
      return out;
    });
    check("PDF: a JPEG background and header logo export; a missing picture is left out", jp.head === "%PDF-" && jp.images >= 1, JSON.stringify(jp));
  }
  // A SmartArt file added like a picture and referenced like one: drawn on
  // the stage by the layout engine, in the PDF, and in the PPTX as SmartArt
  // PowerPoint can edit (data, layout, style, colours and the drawing) —
  // never as a picture of its XML.
  {
    const steps = '<dgm:dataModel xmlns:dgm="http://schemas.openxmlformats.org/drawingml/2006/diagram" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><dgm:ptLst><dgm:pt modelId="0" type="doc"><dgm:prSet loTypeId="urn:microsoft.com/office/officeart/2005/8/layout/process1"/></dgm:pt><dgm:pt modelId="1"><dgm:t><a:p><a:r><a:t>Suunnittelu</a:t></a:r></a:p></dgm:t></dgm:pt><dgm:pt modelId="2"><dgm:t><a:p><a:r><a:t>Hämeenlinna</a:t></a:r></a:p></dgm:t></dgm:pt></dgm:ptLst><dgm:cxnLst><dgm:cxn srcId="0" destId="1"/><dgm:cxn srcId="0" destId="2"/></dgm:cxnLst></dgm:dataModel>';
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "evgp-sa-"));
    fs.writeFileSync(path.join(dir, "check-steps.xml"), steps);
    await page.setInputFiles("#fileadd", [path.join(dir, "check-steps.xml")]);
    await page.waitForTimeout(800);
    const sa = await page.evaluate(() => {
      const a = window.__app;
      const src0 = a.source();
      a.setSource("# S\n\n## Vaiheet\n\n![Vaiheet](media/check-steps.xml)\n{colors=colorful1}\n");
      const toB64 = (buf) => {
        const u = new Uint8Array(buf);
        let s = "";
        for (let i = 0; i < u.length; i += 1) s += String.fromCharCode(u[i]);
        return btoa(s);
      };
      const out = {};
      try {
        const st = JSON.stringify(JSON.parse(a.slideJson(1)));
        out.stage = st.includes("Suunnittelu") && st.includes("Hämeenlinna");
        out.pdf = toB64(a.pdf());
        out.pptx = toB64(a.pptx());
      } catch (e) {
        out.error = String(e);
      }
      a.setSource(src0);
      return out;
    });
    check("SmartArt: exports without an error", !sa.error, sa.error || "");
    if (!sa.error) {
      check("SmartArt: drawn on the stage, the Finnish intact", sa.stage === true);
      const saPdf = Buffer.from(sa.pdf, "base64");
      check("SmartArt: the PDF is written", saPdf.subarray(0, 5).toString() === "%PDF-");
      const saPptx = unzip(Buffer.from(sa.pptx, "base64"));
      const slide2 = saPptx.get("ppt/slides/slide2.xml") || "";
      const rels2 = saPptx.get("ppt/slides/_rels/slide2.xml.rels") || "";
      const ct = saPptx.get("[Content_Types].xml") || "";
      const data = saPptx.get("ppt/diagrams/data1.xml") || "";
      const drawing = saPptx.get("ppt/diagrams/drawing1.xml") || "";
      check("SmartArt: the PPTX slide holds it as SmartArt", /<dgm:relIds [^>]*r:dm="rIdDgm-data1"/.test(slide2), slide2.slice(0, 200));
      check("SmartArt: its five parts named by the slide", ["data1", "layout1", "quickStyle1", "colors1", "drawing1"].every((p) => rels2.includes(`Target="../diagrams/${p}.xml"`)), rels2);
      check("SmartArt: and typed", /diagrams\/data1\.xml" ContentType="application\/vnd\.openxmlformats-officedocument\.drawingml\.diagramData\+xml"/.test(ct) && /diagrams\/drawing1\.xml" ContentType="application\/vnd\.ms-office\.drawingml\.diagramDrawing\+xml"/.test(ct));
      check("SmartArt: its data holds the steps, the Finnish intact", data.includes("Suunnittelu") && data.includes("Hämeenlinna") && data.includes("layout/process1"), data.slice(0, 200));
      check("SmartArt: the colours the Markdown asked for", data.includes("colors/colorful1") && (saPptx.get("ppt/diagrams/colors1.xml") || "").includes("colors/colorful1"));
      check("SmartArt: its drawing as the stage drew it", /prst="roundRect"/.test(drawing) && /prst="rightArrow"/.test(drawing) && drawing.includes("Suunnittelu"));
      check("SmartArt: not as a picture of the XML", !/<p:pic>/.test(slide2) && ![...saPptx.keys()].some((k) => /media\/.*\.xml$/.test(k)));
    }
  }
  const fxPptx = unzip(Buffer.from(fxExp.pptx, "base64"));
  const bgs = [2, 3, 4].map((n) => /<p:bg><p:bgPr><a:blipFill>/.test(fxPptx.get(`ppt/slides/slide${n}.xml`) || ""));
  check("PPTX: the effect is the slide's background, only where there is one", bgs.join(",") === "true,true,false", bgs.join(","));
  // A slide's own picture (bg=, cut and dimmed as the stage shows it) is the
  // PPTX slide's background too, and the file is laid out like the stage:
  // a task list keeps its boxes, text the size the slide drew it.
  const own = await page.evaluate(async () => {
    const a = window.__app;
    const src0 = a.source();
    a.setSource("# E\n\n## Kuva {bg=media/check-pic.png bg-dim=0.4}\n\n- [x] tehty\n- [ ] auki\n\n## Ilman\n\nteksti\n");
    const toB64 = (buf) => {
      const u = new Uint8Array(buf);
      let s = "";
      for (let i = 0; i < u.length; i += 1) s += String.fromCharCode(u[i]);
      return btoa(s);
    };
    await window.__renderFxStills();
    await window.__judgeExportContrast();
    const pptx = toB64(a.pptx());
    a.setSource(src0);
    return pptx;
  });
  const ownPptx = unzip(Buffer.from(own, "base64"));
  const ownBgs = [2, 3].map((n) => /<p:bg><p:bgPr><a:blipFill>/.test(ownPptx.get(`ppt/slides/slide${n}.xml`) || ""));
  check("PPTX: a slide's own picture is its background", ownBgs.join(",") === "true,false", ownBgs.join(","));
  const ownSlide = ownPptx.get("ppt/slides/slide2.xml") || "";
  check("PPTX: a task list keeps its boxes", ownSlide.includes('char="☑"') && ownSlide.includes('char="☐"'));
  const ownSizes = [...ownSlide.matchAll(/<a:rPr[^>]* sz="(\d+)"/g)].map((m) => +m[1]);
  check("PPTX: text is the size the stage draws it, not the markdown default 20 pt", ownSizes.length > 0 && ownSizes.every((v) => v !== 2000), ownSizes.join(","));
  const names = await page.evaluate(() => {
    const a = window.__app;
    const src = a.source();
    const fromTitle = window.__exportName();
    a.setSource("# Eka otsikko: osa 1/2\n\nteksti\n");
    const fromHeading = window.__exportName();
    a.setSource(src);
    return [fromTitle, fromHeading];
  });
  check("an export is named after the front matter title", names[0] === "Gemini-botti palaverissa", names[0]);
  check("…or the first heading, without what a file name cannot hold", names[1] === "Eka otsikko osa 1 2", names[1]);
  const pptx = unzip(Buffer.from(exp.pptx, "base64"));
  const slide2 = pptx.get("ppt/slides/slide2.xml") || "";
  const notes = [...pptx.keys()].filter((k) => /notesSlides\/notesSlide\d+\.xml$/.test(k));
  check("PPTX has a notes page for the slides with notes", notes.length >= 2, notes.join(" "));
  // Each notes page links back to its own slide and to a notes master;
  // Keynote refuses the file otherwise.
  const misLinked = notes.filter((k) => {
    const n = k.match(/notesSlide(\d+)\.xml$/)[1];
    const rels = pptx.get(k.replace("notesSlides/", "notesSlides/_rels/") + ".rels") || "";
    return !rels.includes(`Target="../slides/slide${n}.xml"`) || !rels.includes("../notesMasters/notesMaster1.xml");
  });
  check("PPTX notes pages point at their own slide and a notes master", misLinked.length === 0 && pptx.has("ppt/notesMasters/notesMaster1.xml"), misLinked.join(" "));
  check("PPTX slide 2 has a push transition", /<p:transition[^>]*>[\s\S]*<p:push/.test(slide2));
  const paras = new Set((slide2.match(/<p:pRg st="(\d+)"/g) || []));
  check("PPTX slide 2 builds its four list items one paragraph at a time", paras.size === 4, [...paras].join(" "));
  // Keynote imports a build only with its build list and group ids, and
  // warns about any face it does not have
  check("PPTX slide 2 declares its paragraph build", /<p:bldLst><p:bldP spid="\d+" grpId="0" build="p"\/>/.test(slide2) && /grpId="0" nodeType="clickEffect"/.test(slide2));
  const faces = new Set();
  for (const [k, v] of pptx) if (k.endsWith(".xml")) for (const m of v.matchAll(/typeface="([^"]+)"/g)) if (m[1]) faces.add(m[1]);
  check("PPTX names only faces every machine has", [...faces].every((f) => f === "Arial" || f.startsWith("+")), [...faces].join(", "));
  // A formula in a line is an equation PowerPoint can edit (Office Math in
  // the a14 choice) and Unicode text in the fallback, never its TeX source.
  // Equations are set in Cambria Math, which every Office install has.
  const mathB64 = await page.evaluate(() => {
    const a = window.__app;
    const src0 = a.source();
    a.setSource("# M\n\n## Korko\n\nLaskettu: $FV = PMT \\cdot \\frac{(1+r)^n - 1}{r}$\n");
    const u = new Uint8Array(a.pptx());
    let s = "";
    for (let i = 0; i < u.length; i += 1) s += String.fromCharCode(u[i]);
    a.setSource(src0);
    return btoa(s);
  });
  const mathBuf = Buffer.from(mathB64, "base64");
  if (shots) { fs.mkdirSync(shots, { recursive: true }); fs.writeFileSync(path.join(shots, "formula.pptx"), mathBuf); }
  const mathSlides = [...unzip(mathBuf)].filter(([k]) => /slides\/slide\d+\.xml$/.test(k)).map(([, v]) => v).join("");
  check("PPTX: a formula in a line is an equation", /<mc:Choice[^>]*Requires="a14"><p:sp>[\s\S]*<a14:m><m:oMath[\s\S]*<m:f><m:num>/.test(mathSlides));
  check("…with its Unicode text as the fallback, and no TeX", mathSlides.includes("FV = PMT ⋅ ((1 + r)ⁿ − 1)/r") && !mathSlides.includes("\\frac"));
  check("…set in Cambria Math", /<a:latin typeface="Cambria Math"/.test(mathSlides));
  const n2 = notes.map((k) => pptx.get(k)).join(" ");
  check("the notes are the speaker's words without the cue marks", n2.includes("linkin saanut") && !n2.includes("[[1]]"));

  // A data file dropped on the editor: the import dialog, a chart made from
  // its columns, and a paged table (a .xlsx through its own bundle).
  {
    const pd = await browser.newPage({ viewport: { width: 1400, height: 820 } });
    const perr = [];
    pd.on("pageerror", (e) => perr.push(e.message));
    await pd.goto(url + "?sample=esittely");
    await pd.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    const drop = (name, b64, type) => pd.evaluate(([name, b64, type]) => {
      const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
      const dt = new DataTransfer();
      dt.items.add(new File([bytes], name, { type }));
      document.querySelector("canvas").dispatchEvent(new DragEvent("drop", { dataTransfer: dt, bubbles: true, cancelable: true }));
    }, [name, b64, type]);
    const press = (id) => pd.evaluate((id) => {
      const a = window.__app;
      a.panelsJson();
      const walk = (e) => { if (e.id === id) return e; for (const k of e.children || []) { const r = walk(k); if (r) return r; } return null; };
      const b = walk(a.panels.host.lastPage);
      if (!b) return false;
      a.pointerDown(b.calculatedX + 8, b.calculatedY + 8, false, 1);
      a.pointerUp();
      return true;
    }, id);
    const rows = ["Region,Product,Revenue"];
    for (let i = 0; i < 20; i++) rows.push(`${["North", "South", "East", "West"][i % 4]},P${i},${100 + i * 10}`);
    await drop("check-import.csv", Buffer.from(rows.join("\n") + "\n").toString("base64"), "text/csv");
    await pd.waitForFunction(() => window.__app.shareIsOpen() && window.__app.panels.dialogKind === "data", null, { timeout: 5000 }).catch(() => {});
    const dlgOpen = await pd.evaluate(() => window.__app.shareIsOpen() && window.__app.panels.dialogKind === "data");
    await press("pn-d-chart");
    const plan = await pd.evaluate(() => { const i = window.__app.panels.imp; return { cat: i.catCol, rev: i.valueOn[2] }; });
    await press("pn-d-make");
    await pd.waitForTimeout(500);
    const chart = await pd.evaluate(() => ({ open: window.__app.chartIsOpen(), fence: /```vega-lite[\s\S]*North[\s\S]*1000/.test(window.__app.source()) }));
    check("a dropped CSV asks what to make; Chart groups Revenue by Region and opens the chart editor", dlgOpen && plan.cat === 0 && plan.rev && chart.open && chart.fence, JSON.stringify({ dlgOpen, plan, chart }));
    await pd.evaluate(() => { const a = window.__app; a.closeChart(); a.setSource(a.source() + "\n\n## Data table\n\n"); a.mdEditor.moveCaret(a.mdEditor.buf.lineCount() - 1, 0, false); });
    await drop("check-import.csv", Buffer.from(rows.join("\n") + "\n").toString("base64"), "text/csv");
    await pd.waitForTimeout(300);
    await press("pn-d-table");
    await pd.waitForTimeout(800);
    const table = await pd.evaluate(() => {
      const a = window.__app;
      const u = a.deck.tables[0];
      if (!u) return { none: true };
      a.selectSlide(u.slide);
      const texts = () => JSON.parse(a.stageJson()).list.cmds.filter((c) => c.k === 3).map((c) => c.text);
      const p1 = texts();
      const turned = a.deck.tablePress(u.slide, u.nextX + 4, u.prevY + 4);
      const p2 = texts();
      return { fence: a.source().includes("```table\ndata/check-import.csv"), pages: u.pages(), p1: p1.includes("P0") && p1.includes("1 / 3"), turned, p2: p2.includes("P8") && !p2.includes("P0") && p2.includes("2 / 3") };
    });
    check("Table: a ```table fence, eight rows a page, ‹ › turns the page", table.fence && table.pages === 3 && table.p1 && table.turned && table.p2, JSON.stringify(table));
    const xlsx = path.join(ensureRanger(), "gallery/datagrid/fixtures/sales.xlsx");
    if (fs.existsSync(xlsx)) {
      await drop("check-book.xlsx", fs.readFileSync(xlsx).toString("base64"), "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet");
      await pd.waitForFunction(() => window.__app.shareIsOpen() && window.__app.panels.imp.name === "check-book.xlsx", null, { timeout: 15000 }).catch(() => {});
      const book = await pd.evaluate(() => { const i = window.__app.panels.imp; return { sheets: i.sheetNames.join(","), head: i.headers.join(","), path: i.sheetPaths[0] }; });
      check("a .xlsx: one CSV per sheet, title rows above the header dropped", book.sheets === "Sales,Summary" && book.head === "Product,Qty,Price,Total" && book.path === "data/check-book-Sales.csv", JSON.stringify(book));
      // Live spreadsheet: the workbook on the slide (EVGSheets, when the build
      // put a copy beside the page — EVGSHEETS_DIST or .deps/EVGSheets/dist).
      if (fs.existsSync(path.join(distDir, "sheets", "evgsheets.mjs"))) {
        await pd.evaluate(() => { const a = window.__app; a.setSource(a.source() + "\n\n## Live sheet\n\n"); a.mdEditor.moveCaret(a.mdEditor.buf.lineCount() - 1, 0, false); });
        await press("pn-d-workbook");
        await pd.waitForTimeout(1500);
        const fence = await pd.evaluate(() => {
          const a = window.__app;
          const u = a.deck.tables.find((x) => x.live);
          return u ? { src: /```sheet\ndata\/check-book\.xlsx\nsheet: Sales\ndata: data\/check-book-Sales\.csv/.test(a.source()), loaded: u.loaded, rows: u.rows } : null;
        });
        check("Live spreadsheet: a ```sheet fence naming the kept workbook, its still drawn from the sheet's CSV", fence && fence.src && fence.loaded && fence.rows > 0, JSON.stringify(fence));
        const kept = (await pd.evaluate(() => window.__docFiles())).filter((p) => p.startsWith("data/check-book"));
        check("a workbook is kept as itself: the .xlsx, and no CSV files of its sheets", kept.includes("data/check-book.xlsx") && !kept.some((p) => p.endsWith(".csv")), JSON.stringify(kept));
        const last = await pd.evaluate(() => window.__app.deck.slideCount() - 1);
        await pd.evaluate((i) => { const a = window.__app; a.selectSlide(i); a.present(false); }, last);
        await pd.waitForFunction(() => { const e = document.querySelector(".sheet-live"); return e && e.querySelector("canvas") && e.style.visibility === "visible"; }, null, { timeout: 30000 }).catch(() => {});
        const shown = await pd.evaluate(() => { const e = document.querySelector(".sheet-live"); return e ? { inert: e.hasAttribute("inert"), canvas: !!e.querySelector("canvas"), w: parseInt(e.style.width, 10) } : null; });
        check("presenting: the workbook is over the box, inert until Edit", shown && shown.inert && shown.canvas && shown.w > 200, JSON.stringify(shown));
        const slideBefore = await pd.evaluate(() => JSON.parse(window.__app.layoutJson()).slide);
        await pd.keyboard.press("e");
        await pd.waitForFunction(() => window.__liveSheets.editing(), null, { timeout: 15000 }).catch(() => {});
        await pd.waitForTimeout(400);
        // A1 is the book's title and A2 its header: A3 is the first product.
        await pd.keyboard.press("ArrowDown");
        await pd.keyboard.press("ArrowDown");
        await pd.keyboard.type("4242");
        await pd.keyboard.press("Enter");
        await pd.waitForTimeout(300);
        const during = await pd.evaluate(() => ({ editing: window.__liveSheets.editing(), slide: JSON.parse(window.__app.layoutJson()).slide }));
        check("E hands the keyboard to the sheet: arrows move cells, not slides", during.editing && during.slide === slideBefore, JSON.stringify(during));
        await pd.keyboard.press("Escape");
        await pd.waitForTimeout(1500);
        const after = await pd.evaluate(() => {
          const u = window.__app.deck.tables.find((x) => x.live);
          let cells = [];
          for (let r = 0; r < Math.min(u.rows, 4); r++) cells.push(u.cell(r, 0));
          return { editing: window.__liveSheets.editing(), cells };
        });
        check("Esc hands it back, and the edit is saved into the workbook and the slide's still", !after.editing && after.cells.includes("4242"), JSON.stringify(after));
        await pd.evaluate(() => window.__app.endPresent());
      } else {
        log("skip live spreadsheet checks: no web/dist/sheets (set EVGSHEETS_DIST to an EVGSheets build)");
      }
    }
    // File → Open takes data and pictures too: they go to the Files tab, a
    // single data file through the same import dialog as a drop.
    await pd.evaluate(() => window.__app.closeShare());
    await pd.evaluate(() => window.__app.showTab("md"));
    await pd.setInputFiles("#filepick", { name: "check-open.csv", mimeType: "text/csv", buffer: Buffer.from(rows.join("\n") + "\n") });
    // the dialog opens first; the Files tab once the deck is saved
    await pd.waitForFunction(() => window.__app.shareIsOpen() && window.__app.panels.imp.name === "check-open.csv" && window.__app.editorTab() === "files", null, { timeout: 5000 }).catch(() => {});
    const opened = await pd.evaluate(() => ({ dlg: window.__app.shareIsOpen() && window.__app.panels.imp.name === "check-open.csv", tab: window.__app.editorTab() }));
    await pd.evaluate(() => window.__app.closeShare());
    const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";
    await pd.setInputFiles("#filepick", { name: "check-open.png", mimeType: "image/png", buffer: Buffer.from(png, "base64") });
    await pd.waitForFunction(() => JSON.stringify(window.__app.panelsJson()).includes("check-open.png"), null, { timeout: 5000 }).catch(() => {});
    opened.picture = await pd.evaluate(() => JSON.stringify(window.__app.panelsJson()).includes("check-open.png"));
    opened.deck = await pd.evaluate(() => !window.__app.source().startsWith("Region,"));
    check("File → Open: a CSV opens the import dialog on the Files tab; a picture is listed in Files", opened.dlg && opened.tab === "files" && opened.picture && opened.deck, JSON.stringify(opened));
    check("no page errors in the data import", perr.length === 0, perr.join(" | "));
    await pd.close();
  }

  // A tab loaded before an update holds this browser's store at the older
  // version (and lets go of nothing): a new page waits for it, saying so,
  // instead of opening on a store in memory, and so does a second new page
  // queued behind the first. Once the old tab is closed both start on the
  // kept store. A newer page still lets go of this version's: the tab that
  // had it says to reload.
  {
    const ctx = await browser.newContext({ viewport: { width: 1200, height: 760 } });
    const old = await ctx.newPage();
    await old.goto(url + "connect.html");
    const held = await old.evaluate(() => new Promise((ok) => {
      const r = indexedDB.open("evg-presentation", 1);
      r.onupgradeneeded = () => r.result.createObjectStore("docs", { keyPath: "id" });
      r.onsuccess = () => { window.__held = r.result; ok(r.result.version); };
      r.onerror = () => ok(String(r.error));
    }));
    const memory = [];
    const open = async () => {
      const pg = await ctx.newPage();
      pg.on("console", (m) => { if (/kept in memory/.test(m.text())) memory.push(m.text()); });
      await pg.goto(url);
      return pg;
    };
    const a = await open();
    const b = await open();
    await a.waitForTimeout(3000);
    const before = await Promise.all([a, b].map((pg) => pg.evaluate(() => ({
      started: window.__pageStarted === true, line: document.getElementById("loadNote")?.textContent || "",
    }))));
    await old.close();
    const started = await Promise.all([a, b].map((pg) => pg.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 }).then(() => true, () => false)));
    check("a tab from before an update: new pages wait for it, say so, and start on the kept store once it closes",
      held === 1 && before.every((x) => !x.started && /Sliqtly/.test(x.line) && /(tabs|välilehti)/.test(x.line)) && started.every(Boolean) && memory.length === 0,
      JSON.stringify({ held, before, started, memory }));
    const c = await ctx.newPage();
    await c.goto(url + "connect.html");
    const newer = await c.evaluate(() => new Promise((ok) => {
      const r = indexedDB.open("evg-presentation", 99);
      r.onsuccess = () => { r.result.close(); ok("open"); };
      r.onblocked = () => ok("blocked");
      r.onerror = () => ok(String(r.error));
    }));
    const notice = await a.evaluate(() => document.getElementById("tabNotice")?.textContent || "");
    check("a newer page takes the store over; this one says to reload", newer === "open" && /Sliqtly/.test(notice), JSON.stringify({ newer, notice }));
    await ctx.close();
  }

  // PRO: a signed-in user's deck lives in the cloud (a share), against a
  // stand-in for Firebase kept here: saved on change under /s/{id}?edit,
  // opened from there on reload with its pictures, an assistant's change
  // picked up, a later edit written back, and a change made elsewhere not
  // written over.
  {
    const fakeDb = new Map();
    const fakeFiles = new Map();
    let puts = 0;
    let failPuts = 0;
    const stamp = (o) => { for (const k of Object.keys(o)) if (o[k] && o[k].__ts) o[k] = Date.now(); return o; };
    const ctx = await browser.newContext({ viewport: { width: 1400, height: 820 } });
    await ctx.exposeFunction("__fakeFirebase", (op, a) => {
      if (op === "get") return fakeDb.get(a.k) ?? null;
      if (op === "query") {
        return [...fakeDb.entries()].filter(([k, d]) => k.startsWith(a.c + "/") && d[a.f] === a.v).slice(0, a.n)
          .map(([k, d]) => ({ id: k.slice(a.c.length + 1), d }));
      }
      if (op === "set") fakeDb.set(a.k, stamp(a.merge ? { ...fakeDb.get(a.k), ...a.data } : a.data));
      else if (op === "update") {
        if (!fakeDb.has(a.k)) throw new Error("no document " + a.k);
        fakeDb.set(a.k, stamp({ ...fakeDb.get(a.k), ...a.data }));
      } else if (op === "put") {
        if (failPuts > 0) { failPuts -= 1; throw new Error("storage/retry-limit-exceeded"); }
        puts += 1; fakeFiles.set(a.p, { type: a.type, buf: Buffer.from(a.b64, "base64") }); }
      else if (op === "del") fakeFiles.delete(a.p);
      return null;
    });
    const fake = `(() => {
      const user = { uid: "u1", displayName: "Testi", email: "t@example.com" };
      const call = (op, a) => window.__fakeFirebase(op, a);
      const ref = (c, id) => ({
        set: (data, o) => call("set", { k: c + "/" + id, data, merge: !!(o && o.merge) }),
        update: (data) => call("update", { k: c + "/" + id, data }),
        get: async () => { const d = await call("get", { k: c + "/" + id }); return { exists: d != null, data: () => d }; },
      });
      const ms = (v) => (typeof v === "number" ? { toMillis: () => v } : v);
      const query = (c, f, v, n) => ({
        limit: (m) => query(c, f, v, m),
        get: async () => ({ docs: (await call("query", { c, f, v, n: n || 1000 })).map((x) => ({ id: x.id, data: () => ({ ...x.d, updated: ms(x.d.updated), created: ms(x.d.created) }) })) }),
      });
      const db = { collection: (c) => ({ doc: (id) => ref(c, id), where: (f, op, v) => query(c, f, v, 0) }) };
      const firestore = () => db;
      firestore.FieldValue = { serverTimestamp: () => ({ __ts: true }) };
      const storage = () => ({ ref: (p) => ({
        put: async (blob, meta) => { let s = ""; for (const x of new Uint8Array(await blob.arrayBuffer())) s += String.fromCharCode(x); return call("put", { p, type: (meta && meta.contentType) || blob.type, b64: btoa(s) }); },
        getDownloadURL: async () => location.origin + "/__fakefiles/" + encodeURIComponent(p),
        delete: () => call("del", { p }),
      }) });
      const auth = () => ({ onAuthStateChanged(cb) { setTimeout(() => cb(user), 0); return () => {}; }, signOut() {} });
      window.firebase = { auth, firestore, storage };
    })();`;
    await ctx.route(/^https:\/\/www\.gstatic\.com\/firebasejs\//, (r) => r.fulfill({ contentType: "text/javascript", body: /app-compat/.test(r.request().url()) ? fake : "" }));
    await ctx.route(/\/__\/firebase\/init\.js/, (r) => r.fulfill({ contentType: "text/javascript", body: "" }));
    await ctx.route(/\/__fakefiles\//, (r) => {
      const f = fakeFiles.get(decodeURIComponent(new URL(r.request().url()).pathname.replace(/^\/__fakefiles\//, "")));
      return f ? r.fulfill({ status: 200, contentType: f.type, body: f.buf }) : r.fulfill({ status: 404, body: "" });
    });
    await ctx.route(/\/s\/[A-Za-z0-9]+(\?|$)/, (r) => r.fulfill({ contentType: "text/html", body: fs.readFileSync(path.join(distDir, "index.html")) }));
    const pc = await ctx.newPage();
    const perr = [];
    pc.on("pageerror", (e) => perr.push(e.message));
    const started = async () => {
      await pc.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
      await pc.waitForTimeout(400);
    };
    const shareId = () => [...fakeDb.keys()].filter((k) => k.startsWith("shares/") && !k.startsWith("shares/zz")).map((k) => k.slice(7));
    const filesListed = async (name) => {
      await pc.evaluate(() => window.__app.showTab("files"));
      await pc.waitForFunction((n) => window.__app.panels.filesJson.includes(n), name, { timeout: 8000 }).catch(() => {});
      return pc.evaluate((n) => window.__app.panels.filesJson.includes(n), name);
    };
    // the user's own deck that only the cloud keeps, and someone else's
    fakeDb.set("shares/zzCloudOnly1", { name: "Vain pilvessä", md: "# Vain pilvessä\n\n## Dia\n", theme: "aurora", css: null, owner: "u1", files: [], created: 1000 });
    fakeDb.set("shares/zzSomeoneElse", { name: "Toisen esitys", md: "# Toisen\n", owner: "u2", files: [], created: 1000 });
    await pc.goto(url);
    await started();
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "evgp-cloud-"));
    fs.writeFileSync(path.join(dir, "cloud-pic.png"), Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64"));
    await pc.evaluate(() => window.__app.setSource("# Pilvi\n\n## Kuva\n\n![kuva](media/cloud-pic.png)\n"));
    await pc.setInputFiles("#fileadd", [path.join(dir, "cloud-pic.png")]);
    await pc.waitForFunction(() => /^\/s\/[A-Za-z0-9]+$/.test(location.pathname), null, { timeout: 15000 }).catch(() => {});
    await pc.waitForTimeout(2500);
    const ids = shareId();
    const id = ids[0] || "";
    const first = { ids, address: await pc.evaluate(() => location.pathname + location.search), share: fakeDb.get("shares/" + id) };
    check("PRO: a changed deck is saved to the cloud and the address names it",
      ids.length === 1 && first.address === "/s/" + id + "?edit" && first.share?.owner === "u1" && first.share.md.includes("## Kuva") && (first.share.files || []).some((f) => f.path === "media/cloud-pic.png"),
      JSON.stringify({ ids, address: first.address, files: first.share?.files }));

    await pc.reload();
    await started();
    const back = { md: await pc.evaluate(() => window.__app.source()), pic: await filesListed("media/cloud-pic.png") };
    check("PRO: a reload opens the deck from the cloud, its picture with it", back.md.includes("## Kuva") && back.pic, JSON.stringify(back).slice(0, 160));

    // an assistant changes it in the cloud; the editor opened at its plain
    // address gets the change
    fakeDb.set("shares/" + id, { ...fakeDb.get("shares/" + id), md: back.md + "\n## Avustajan dia\n\nTeksti.\n", updated: Date.now() });
    await pc.goto(url);
    await started();
    const viaAi = await pc.evaluate(() => ({ md: window.__app.source(), at: location.pathname }));
    check("PRO: the deck worked on last opens from the cloud, with an assistant's change", viaAi.md.includes("## Avustajan dia") && viaAi.at === "/s/" + id, JSON.stringify(viaAi).slice(0, 160));

    // an edit in the editor goes to the cloud; the unchanged picture is not sent again
    const putsBefore = puts;
    await pc.evaluate(() => window.__app.setSource(window.__app.source() + "\n## Editorin dia\n\nMuokattu.\n"));
    await pc.waitForTimeout(5000);
    const edited = fakeDb.get("shares/" + id);
    check("PRO: an edit in the editor is written to the cloud", edited.md.includes("## Editorin dia") && edited.md.includes("## Avustajan dia") && puts === putsBefore && shareId().length === 1,
      JSON.stringify({ puts: puts - putsBefore, shares: shareId().length }));

    // Share links to the same cloud deck, no new copy
    await pc.evaluate(() => { window.__lastShare = ""; document.getElementById("share").click(); });
    await pc.waitForFunction(() => /\/s\/[A-Za-z0-9]+\?edit$/.test(window.__lastShare || ""), null, { timeout: 10000 }).catch(() => {});
    const link = await pc.evaluate(() => window.__lastShare || "");
    check("PRO: Share links to the deck's own cloud copy", link.endsWith("/s/" + id + "?edit") && shareId().length === 1, link);
    await pc.evaluate(() => window.__app.closeShare());

    // changed elsewhere meanwhile: not written over, the two merged here
    // (web/versions.js) and then written
    const elsewhere = fakeDb.get("shares/" + id).md.replace("## Avustajan dia", "## Avustajan dia, muutettu muualla");
    fakeDb.set("shares/" + id, { ...fakeDb.get("shares/" + id), md: elsewhere, updated: Date.now() });
    await pc.evaluate(() => window.__app.setSource(window.__app.source() + "\n## Vielä yksi\n"));
    await pc.waitForFunction(() => window.__app.source().includes("muutettu muualla"), null, { timeout: 10000 }).catch(() => {});
    await pc.waitForTimeout(4000);
    const mergedMd = fakeDb.get("shares/" + id).md;
    check("PRO: a deck changed elsewhere is merged, not written over", mergedMd.includes("## Avustajan dia, muutettu muualla") && mergedMd.includes("## Vielä yksi") && (await pc.evaluate(() => window.__app.source())) === mergedMd, JSON.stringify(mergedMd.slice(-200)));

    // the window's Name field: a drag selects and typing replaces it,
    // Ctrl+A selects all, and the × at its end empties it
    {
      await pc.evaluate(() => window.__fileRequest("new"));
      await pc.waitForTimeout(300);
      const nameOf = () => pc.evaluate(() => JSON.parse(window.__app.newDeckPlan()).name);
      // empty, with the hint drawn as a placeholder and not as a value
      check("New presentation: Name starts empty", (await nameOf()) === "");
      await pc.keyboard.type("Myynti 2026");
      // the window opens under the top bar and the presentations' row
      const ny = 235 + (await pc.evaluate(() => window.__app.deckRect.h));
      await pc.mouse.move(584, ny);
      await pc.mouse.down();
      await pc.mouse.move(610, ny);
      await pc.mouse.move(628, ny);
      await pc.mouse.up();
      await pc.keyboard.type("Tulos");
      const replaced = await nameOf();
      await pc.keyboard.press("Meta+a");
      await pc.keyboard.type("Vanha");
      const allMeta = await nameOf();
      await pc.keyboard.press("Control+a");
      await pc.keyboard.type("Uusi");
      const all = await nameOf();
      await pc.mouse.click(832, ny);
      await pc.waitForTimeout(200);
      const cleared = await nameOf();
      await pc.keyboard.type("Z");
      const after = await nameOf();
      check("New presentation: a drag in Name selects, Ctrl+A selects all, × clears", replaced.startsWith("Tulos") && replaced.endsWith("2026") && allMeta === "Vanha" && all === "Uusi" && cleared === "" && after === "Z",
        JSON.stringify({ replaced, allMeta, all, cleared, after }));
      await pc.keyboard.press("Escape");
      await pc.waitForTimeout(300);
    }
    // File → New presentation asks first: Esc leaves the deck as it is
    const before = { md: await pc.evaluate(() => window.__app.source()), at: await pc.evaluate(() => location.pathname + location.search) };
    await pc.evaluate(() => window.__fileRequest("new"));
    await pc.waitForTimeout(300);
    const asked = await pc.evaluate(() => window.__app.chartIsOpen());
    await pc.keyboard.press("Escape");
    await pc.waitForTimeout(500);
    const kept = { md: await pc.evaluate(() => window.__app.source()), at: await pc.evaluate(() => location.pathname + location.search) };
    check("PRO: New presentation asks first, and Esc keeps the deck", asked && kept.md === before.md && kept.at === before.at, JSON.stringify(kept.at));
    // made: a share and an address of its own; the deck before stays as it was
    await pc.evaluate(() => window.__fileRequest("new"));
    await pc.waitForTimeout(300);
    await pc.keyboard.type("Uusi pakka");
    await pc.keyboard.press("Enter");
    await pc.waitForTimeout(3000);
    const made = { md: await pc.evaluate(() => window.__app.source()), at: await pc.evaluate(() => location.pathname + location.search), ids: shareId() };
    const newId = made.ids.find((k) => k !== id) || "";
    check("PRO: a new presentation gets its own share and address", made.ids.length === 2 && made.at === "/s/" + newId + "?edit" && made.md.startsWith("# Uusi pakka") && fakeDb.get("shares/" + newId)?.md === made.md && fakeDb.get("shares/" + id).md === mergedMd,
      JSON.stringify({ at: made.at, ids: made.ids }));
    // the last change of a deck reaches its share although another is made at once
    await pc.evaluate(() => window.__app.setSource(window.__app.source() + "\n## Viimeinen muutos\n"));
    await pc.waitForTimeout(1700);
    await pc.evaluate(() => window.__fileRequest("new"));
    await pc.waitForTimeout(300);
    await pc.keyboard.press("Enter");
    await pc.waitForTimeout(3000);
    check("PRO: a deck's last change is saved to its share before a new one is made", fakeDb.get("shares/" + newId).md.includes("## Viimeinen muutos") && shareId().length === 3);
    // File → Duplicate: the deck saved first, then a copy with its own share
    // and address, its Markdown retitled and its files with it
    await pc.evaluate(() => window.__app.setSource("# Alkuperäinen\n\n## Kuva\n\n![kuva](media/cloud-pic.png)\n"));
    await pc.setInputFiles("#fileadd", [path.join(dir, "cloud-pic.png")]);
    await pc.waitForTimeout(800);
    const origAt = await pc.evaluate(() => location.pathname);
    const origId = origAt.replace(/^\/s\//, "");
    await pc.evaluate(() => window.__fileRequest("duplicate"));
    await pc.waitForTimeout(300);
    // the copy's name is asked first: the suggestion, all selected; Esc
    // makes nothing
    const dupAsk = await pc.evaluate(() => JSON.parse(window.__app.newDeckPlan()));
    await pc.keyboard.press("Escape");
    await pc.waitForTimeout(800);
    check("PRO: Duplicate asks the copy's name, suggested; Esc keeps the deck",
      dupAsk.dup === true && /^Alkuperäinen \((copy|kopio)\)$/.test(dupAsk.name) && shareId().length === 3 && (await pc.evaluate(() => location.pathname)) === origAt,
      JSON.stringify({ dupAsk, ids: shareId() }));
    await pc.evaluate(() => window.__fileRequest("duplicate"));
    await pc.waitForTimeout(300);
    await pc.keyboard.press("Enter");
    await pc.waitForTimeout(3000);
    const dup = { md: await pc.evaluate(() => window.__app.source()), at: await pc.evaluate(() => location.pathname + location.search), ids: shareId() };
    const dupId = dup.ids.find((k) => k !== id && k !== newId && k !== origId) || "";
    const orig = fakeDb.get("shares/" + origId);
    check("PRO: Duplicate makes a copy with its own share, address, files and name",
      dup.ids.length === 4 && dup.at === "/s/" + dupId + "?edit" && /^# Alkuperäinen \((copy|kopio)\)\n/.test(dup.md) && dup.md.includes("## Kuva")
        && fakeDb.get("shares/" + dupId)?.md === dup.md && (fakeDb.get("shares/" + dupId)?.files || []).some((f) => f.path === "media/cloud-pic.png")
        && orig?.md.startsWith("# Alkuperäinen\n") && (orig.files || []).some((f) => f.path === "media/cloud-pic.png"),
      JSON.stringify({ at: dup.at, ids: dup.ids, md: dup.md.slice(0, 40) }));
    // File → New presentation, then New → Datasheet saved: the workbook goes
    // to the new deck's own share, and no second share is made
    {
      const before = shareId();
      await pc.evaluate(() => window.__fileRequest("new"));
      await pc.waitForTimeout(300);
      await pc.keyboard.type("Vuokra ja menot");
      await pc.keyboard.press("Enter");
      await pc.waitForTimeout(3000);
      // Kuukausi | Vuokra, 2026-01 | 950, as the MCP server's write_workbook writes it
      const book = [...fs.readFileSync(new URL("./fixtures/sheet-1.xlsx", import.meta.url))];
      await pc.evaluate((b) => window.__saveWorkbook("data/sheet-1.xlsx", new Uint8Array(b).buffer), book);
      await pc.waitForTimeout(4000);
      const made = shareId().filter((k) => !before.includes(k));
      const sh = made.length ? fakeDb.get("shares/" + made[0]) : null;
      const at = await pc.evaluate(() => location.pathname + location.search);
      check("PRO: a new deck's datasheet is saved to its share",
        made.length === 1 && (sh?.files || []).some((f) => f.path === "data/sheet-1.xlsx") && at === "/s/" + made[0] + "?edit",
        JSON.stringify({ made, at, files: (sh?.files || []).map((f) => f.path), md: sh?.md }));
    }
    // a file that fails while the share is made (Duplicate of the deck
    // above, its workbook with it): the copy keeps that share, its address
    // names it, and the next save sends the file again
    {
      const before = shareId();
      failPuts = 1;
      await pc.evaluate(() => window.__fileRequest("duplicate"));
      await pc.waitForTimeout(300);
      // a name typed over the selected suggestion is the copy's
      await pc.keyboard.type("Toinen kopio");
      await pc.keyboard.press("Enter");
      await pc.waitForTimeout(3500);
      const ids1 = shareId().filter((k) => !before.includes(k));
      const at1 = await pc.evaluate(() => location.pathname);
      await pc.evaluate(() => window.__app.setSource(window.__app.source() + "\n## Uudelleen\n"));
      await pc.waitForTimeout(4000);
      const ids2 = shareId().filter((k) => !before.includes(k));
      const sh = ids2.length ? fakeDb.get("shares/" + ids2[0]) : null;
      check("PRO: a share whose file failed is kept, and the file goes on the next save",
        ids1.length === 1 && ids2.length === 1 && at1 === "/s/" + ids1[0] && (sh?.files || []).some((f) => f.path === "data/sheet-1.xlsx") && sh.md.includes("## Uudelleen") && sh.md.startsWith("# Toinen kopio\n"),
        JSON.stringify({ ids1, ids2, at1, files: (sh?.files || []).map((f) => f.path) }));
    }
    // the user's own shares this browser does not keep: listed in File →
    // Presentations…, and opened from the cloud; one an assistant made after
    // sign-in (the MCP server writes shares/{id} with owner = the user) is
    // there as the window opens. The Files tab lists the deck's files only.
    // (kept since the start of the PRO checks, so the list read at sign-in has them)
    fakeDb.set("shares/zzMadeByAi1", { name: "Tekoälyn tekemä", md: "# Tekoäly\n", theme: "aurora", css: null, owner: "u1", deck: "mcp", source: "mcp", files: [], created: Date.now() });
    await pc.evaluate(() => window.__openDecks());
    await pc.waitForFunction(() => window.__app.panels.decksJson.includes("zzMadeByAi1"), null, { timeout: 8000 }).catch(() => {});
    const deckList = await pc.evaluate(() => { try { return JSON.parse(window.__app.panels.decksJson); } catch (_) { return null; } });
    const listed = !!deckList && deckList.rows.some((r) => r.id === "cloud:zzCloudOnly1" && r.cloudOnly);
    const madeByAi = !!deckList && deckList.rows[0]?.id === "cloud:zzMadeByAi1";
    const other = await pc.evaluate(() => window.__app.panels.decksJson.includes("zzSomeoneElse"));
    await pc.evaluate(() => window.__app.panels.closeShare());
    await pc.evaluate(() => window.__app.showTab("files"));
    await pc.evaluate(() => window.__fileRequest("noop"));
    await pc.waitForFunction(() => window.__app.panels.filesJson.includes("\"files\""), null, { timeout: 8000 }).catch(() => {});
    const filesOnly = await pc.evaluate(() => { try { const j = JSON.parse(window.__app.panels.filesJson); return !("docs" in j) && !window.__app.panels.filesJson.includes("zzCloudOnly1"); } catch (_) { return false; } });
    check("PRO: File → Presentations… lists the user's cloud decks with this browser's, an assistant's new one first; the Files tab does not",
      listed && madeByAi && !other && filesOnly, JSON.stringify({ rows: deckList?.rows?.map((r) => r.id), filesOnly, files: await pc.evaluate(() => window.__app.panels.filesJson.slice(0, 300)) }));
    await pc.evaluate(() => window.__fileRequest("doc:cloud:zzCloudOnly1"));
    await pc.waitForTimeout(2000);
    const opened = { md: await pc.evaluate(() => window.__app.source()), at: await pc.evaluate(() => location.pathname + location.search) };
    check("PRO: a cloud deck opens from the cloud", opened.md.startsWith("# Vain pilvessä"), JSON.stringify(opened));
    // every shared deck opens with Sliqtly's intro (web/brand.js), a PRO
    // owner's too, and the show begins when it ends by itself
    // (looked at as the page's HTML is read: a slow load may outlast the intro)
    await pc.addInitScript(() => document.addEventListener("DOMContentLoaded", () => { window.__introEarly = !document.getElementById("brandIntro").hidden; }));
    await pc.goto(url.replace(/\/$/, "") + "/s/zzSomeoneElse");
    await pc.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    const proShare = await pc.evaluate(() => ({ intro: window.__introEarly === true, viewer: document.body.classList.contains("viewer") }));
    check("a PRO owner's share opens with the intro too", proShare.viewer && proShare.intro, JSON.stringify(proShare));
    await pc.waitForFunction(() => document.getElementById("brandIntro").hidden, null, { timeout: 20000 }).catch(() => {});
    const after = await pc.evaluate(() => ({ intro: !document.getElementById("brandIntro").hidden, mode: JSON.parse(window.__app.layoutJson()).mode }));
    check("the intro ends by itself and the show begins", !after.intro && after.mode === "present", JSON.stringify(after));
    // an address that turns out to show nothing: the intro the page's HTML
    // put up goes again
    await pc.goto(url.replace(/\/$/, "") + "/s/zzNoSuchShare");
    await pc.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    await pc.waitForFunction(() => document.getElementById("brandIntro").hidden, null, { timeout: 20000 }).catch(() => {});
    const gone = await pc.evaluate(() => ({ intro: !document.getElementById("brandIntro").hidden, viewer: document.body.classList.contains("viewer") }));
    check("a share that is not found leaves no intro over the page", !gone.intro && !gone.viewer, JSON.stringify(gone));
    // a view of some slides (?slides=): only their sections, and still only
    // them after the deck changed in the cloud
    fakeDb.set("shares/zzSliced", { name: "Osa", owner: "u2", created: 1000, files: [],
      md: "---\nslide-split-level: 2\n---\n\n## Yksi\n\na\n\n## Kaksi\n\nb\n\n## Kolme\n\nc\n" });
    await pc.goto(url.replace(/\/$/, "") + "/s/zzSliced?slides=kaksi,kolme");
    await pc.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    const sliced = await pc.evaluate(() => ({ md: window.__app.source(), n: window.__app.deck.slideCount(), view: document.body.classList.contains("slidesView") }));
    check("a share's view of some slides shows only them", sliced.n === 2 && sliced.view && sliced.md.includes("## Kaksi") && !sliced.md.includes("## Yksi"), JSON.stringify(sliced));
    fakeDb.set("shares/zzSliced", { ...fakeDb.get("shares/zzSliced"), md: "---\nslide-split-level: 2\n---\n\n## Uusi\n\nz\n\n## Yksi\n\na\n\n## Kaksi\n\nb2\n\n## Kolme\n\nc\n", updated: Date.now() });
    await pc.evaluate(() => window.__followShare("zzSliced"));
    const followed = await pc.evaluate(() => ({ md: window.__app.source(), n: window.__app.deck.slideCount() }));
    check("…and follows the deck's changes to them, not the slides added", followed.n === 2 && followed.md.includes("b2") && !followed.md.includes("## Uusi"), JSON.stringify(followed));
    // A shared deck read in the player: its workbook is among the deck's
    // files, so a ```sheet naming the one sheet of a one-sheet book (the
    // still reads data/<book>-<Sheet>.csv) is drawn from it, and the live
    // sheet would open it rather than EVGSheets' demo workbook.
    const bookPath = "shares/zzSheetBook/data/kulut.xlsx";
    const bookBytes = fs.readFileSync(new URL("./fixtures/kulut.xlsx", import.meta.url));
    fakeFiles.set(bookPath, { type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buf: bookBytes });
    fakeDb.set("shares/zzSheetBook", { name: "Kirja", owner: "u2", created: 1000,
      md: "# Kirja\n\n## Taulukko\n\n```sheet\ndata/kulut.xlsx\nsheet: Kulut\nrows: 8\n```\n",
      files: [{ path: "data/kulut.xlsx", type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", size: bookBytes.length, url: url.replace(/\/$/, "") + "/__fakefiles/" + encodeURIComponent(bookPath) }] });
    await pc.goto(url.replace(/\/$/, "") + "/s/zzSheetBook");
    await pc.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    await pc.waitForFunction(() => { const u = window.__app.deck.tables[0]; return !!u && u.loaded; }, null, { timeout: 15000 }).catch(() => {});
    const readBook = await pc.evaluate(async () => {
      const u = window.__app.deck.tables[0];
      return { viewer: document.body.classList.contains("viewer"), loaded: !!u && u.loaded, rows: u ? u.rows : 0, path: u ? u.path : "", files: await window.__docFiles() };
    });
    check("a shared deck's one-sheet workbook: the ```sheet still reads data/<book>-<Sheet>.csv, the player has the .xlsx",
      readBook.viewer && readBook.loaded && readBook.rows === 6 && readBook.path === "data/kulut-Kulut.csv" && readBook.files.includes("data/kulut.xlsx"), JSON.stringify(readBook));
    check("no page errors with PRO", perr.length === 0, perr.join(" | "));
    await ctx.close();
  }

  // Someone else's deck opened at its editor's address (/s/{id}?edit, signed
  // out): a copy of their own, kept only once they change it, the share's
  // own CSS counting as it was opened. Untouched, nothing is kept, and the
  // site's plain address opens the welcome deck with no #doc.
  {
    const share = { md: "# Toisen pakka\n\n## Dia\n\nteksti\n", theme: "aurora", css: "h1 { color: #c00; }", owner: "someoneElse", deck: "dElse", name: "Toisen pakka", files: [] };
    const fake = `(() => {
      const ref = (c, id) => ({ get: async () => ({ exists: c === "shares", data: () => (${JSON.stringify(share)}) }) });
      const firestore = () => ({ collection: (c) => ({ doc: (id) => ref(c, id) }) });
      const auth = () => ({ onAuthStateChanged(cb) { setTimeout(() => cb(null), 0); return () => {}; }, signOut() {} });
      window.firebase = { auth, firestore, storage: () => ({}) };
    })();`;
    const ctx = await browser.newContext({ viewport: { width: 1400, height: 820 } });
    await ctx.route(/^https:\/\/www\.gstatic\.com\/firebasejs\//, (r) => r.fulfill({ contentType: "text/javascript", body: /app-compat/.test(r.request().url()) ? fake : "" }));
    await ctx.route(/\/__\/firebase\/init\.js/, (r) => r.fulfill({ contentType: "text/javascript", body: "" }));
    await ctx.route(/\/s\/[A-Za-z0-9]+(\?|$)/, (r) => r.fulfill({ contentType: "text/html", body: fs.readFileSync(path.join(distDir, "index.html")) }));
    const pc = await ctx.newPage();
    const cerr = [];
    pc.on("pageerror", (e) => cerr.push(e.message));
    await pc.goto(url.replace(/\/$/, "") + "/s/zzOthersDeck?edit");
    await pc.waitForFunction(() => window.__pageStarted === true && window.__app.source().startsWith("# Toisen pakka"), null, { timeout: 90000 });
    await pc.waitForTimeout(2500); // the 1.5 s save has had its turn
    const untouched = await pc.evaluate(() => ({ kept: localStorage.getItem("evgp.doc"), at: location.pathname + location.hash }));
    check("someone else's deck at /s/{id}?edit with its own CSS is not kept until changed", !untouched.kept && !untouched.at.includes("doc="), JSON.stringify(untouched));
    await pc.goto(url);
    await pc.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    await pc.waitForTimeout(500);
    const home = await pc.evaluate(() => ({ md: window.__app.source(), hash: location.hash }));
    check("after it, the site's plain address opens the welcome deck with no #doc", home.md.startsWith("---\ntitle: Sliqtly - Demo") && home.hash === "", JSON.stringify({ ...home, md: home.md.slice(0, 80) }));
    await pc.goto(url.replace(/\/$/, "") + "/s/zzOthersDeck?edit");
    await pc.waitForFunction(() => window.__pageStarted === true && window.__app.source().startsWith("# Toisen pakka"), null, { timeout: 90000 });
    await pc.evaluate(() => window.__app.setSource(window.__app.source() + "\n## Oma lisäys\n"));
    await pc.waitForTimeout(2500);
    const changed = await pc.evaluate(() => ({ kept: localStorage.getItem("evgp.doc"), hash: location.hash }));
    check("changed, the copy is kept, with no #doc in the address", !!changed.kept && !changed.hash.includes("doc="), JSON.stringify(changed));
    check("no page errors opening someone else's deck", cerr.length === 0, cerr.join(" | "));
    await ctx.close();
  }

  // Open → Sample documents: English decks for an English interface, and the
  // prompt is the trigger's text, not a row in the list
  const samples = await page.evaluate(() => {
    const a = window.__app;
    a.openOpen([...document.getElementById("sample").options].map((o) => o.value + "\t" + o.textContent.trim()).join("\n"));
    const sel = a.panels.samplePick;
    const out = { trigger: sel.labelOf(sel.value), rows: sel.items.map((it) => it.value + "=" + it.name) };
    a.closeShare();
    return out;
  });
  check("Open: the samples list starts with a deck, the prompt only on the trigger", samples.trigger === "Open sample document…" && samples.rows[0] === "welcome=Welcome: what Sliqtly can do" && !samples.rows.some((r) => r.startsWith("=")), JSON.stringify(samples));

  // a first visit (nothing kept in this browser) opens the welcome deck in
  // English, with a card that starts a deck of one's own
  {
    const ctx = await browser.newContext({ viewport: { width: 1200, height: 760 }, locale: "en-US" });
    const pw = await ctx.newPage();
    const werr = [];
    pw.on("pageerror", (e) => werr.push(e.message));
    // the deck's own pictures, beside it in samples/welcome/
    const pics = [];
    pw.on("response", (r) => { if (r.url().includes("/samples/welcome/")) pics.push(r.status() + " " + new URL(r.url()).pathname); });
    await pw.goto(url);
    await pw.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    const first = await pw.evaluate(() => ({
      md: window.__app.source().slice(0, 200),
      card: !!document.getElementById("welcomeCard"),
      theme: document.getElementById("theme").value,
    }));
    check("first visit: the welcome deck opens, in English on Aurora, with the welcome card", first.md.includes("# Sliqtly {bg=media/bg.png") && first.md.includes("## Publishing process") && first.card && first.theme === "aurora", JSON.stringify(first));
    check("first visit: the welcome deck's pictures come from samples/welcome/", ["bg.png", "logo.svg", "radial.xml"].every((f) => pics.some((p) => p.startsWith("200 ") && p.includes("/media/" + f))), pics.join(", "));
    const files = await pw.evaluate(async () => (await window.__docFiles()).sort().join(","));
    check("first visit: the pictures are the deck's files", files === "media/bg.png,media/logo.svg,media/radial.xml", files);
    await pw.click("#welcomeCard button.primary");
    await pw.waitForTimeout(300);
    const started = await pw.evaluate(() => ({ open: window.__app.chartIsOpen(), mode: window.__app.chart.mode, card: !!document.getElementById("welcomeCard") }));
    check("first visit: Start your own deck opens the New presentation window", started.open && started.mode === "newdeck" && !started.card, JSON.stringify(started));
    check("no page errors on a first visit", werr.length === 0, werr.join(" | "));
    await ctx.close();
  }

  // the document settings window: opened from the front matter's popover
  // and from the page's pick, it rewrites the front matter as one undo step
  {
    const pd = await browser.newPage({ viewport: { width: 1400, height: 860 } });
    const derr = [];
    pd.on("pageerror", (e) => derr.push(e.message));
    await pd.goto(url + "?sample=esittely");
    await pd.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    const ds = await pd.evaluate(async () => {
      const a = window.__app;
      const walk = (e, id) => { if (e.id === id) return e; for (const k of e.children || []) { const r = walk(k, id); if (r) return r; } return null; };
      const tap = (root, id) => { const e = walk(root, id); if (!e) return false; a.pointerDown(e.calculatedX + 6, e.calculatedY + 6, false, 1); a.pointerUp(); return true; };
      const settle = async () => { window.__handleRequests(); await new Promise((r) => setTimeout(r, 400)); };
      const out = {};
      a.setSource("---\ntitle: Q3\nfooter-right: \"{page} / {pages}\"\n---\n\n# Cover\n\n## Two\n\ntext\n");
      a.showTab("md");
      a.openHint(a.hintFor(0, 1));
      a.hintJson();
      out.hintBtn = tap(a.hint.host.lastPage, "hp-docset");
      await settle();
      out.fromHint = a.chartIsOpen() && a.chart.mode;
      a.chartJson();
      out.firstTab = !!walk(a.chart.host.lastPage, "ds-title");
      tap(a.chart.host.lastPage, "ds-tabs-tab-header");
      a.chartJson();
      tap(a.chart.host.lastPage, "ds-t-0");
      a.text("Acme");
      a.chartJson();
      tap(a.chart.host.lastPage, "ds-first");
      out.written = a.source().split("\n").slice(0, 6).join("|");
      a.key("escape", false, false);
      a.undo();
      out.undone = a.source().split("\n").slice(0, 4).join("|");
      a.selectSlide(1);
      a.place();
      const r = a.slideRect;
      a.pointerDown(r.x + r.w * 0.8, r.y + r.h * 0.75, false, 1);
      a.pointerUp();
      out.page = [a.pick.sel, a.pickJson().includes("Document settings")];
      a.pickContent();
      await settle();
      out.fromPage = a.chartIsOpen() && a.chart.mode;
      return out;
    });
    check("Document settings opens on the presentation's title", ds.firstTab, JSON.stringify(ds));
    check("Document settings opens from the front matter popover and the page's pick", ds.hintBtn && ds.fromHint === "docset" && ds.page[0] === "page" && ds.page[1] && ds.fromPage === "docset", JSON.stringify(ds));
    check("Document settings writes the front matter and undoes as one step", ds.written === "---|title: Q3|footer-right: \"{page} / {pages}\"|header-left: Acme|header-skip: first|---" && ds.undone === "---|title: Q3|footer-right: \"{page} / {pages}\"|---", JSON.stringify(ds));
    check("no page errors in the document settings", derr.length === 0, derr.join(" | "));
    await pd.close();
  }

  // the address follows the slide, the editor's tab and the presentation,
  // and a reload comes back to them
  {
    const ph = await browser.newPage({ viewport: { width: 1200, height: 760 } });
    await ph.goto(url + "?sample=esittely");
    await ph.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    await ph.evaluate(() => { window.__app.selectSlide(2); window.__app.showTab("css"); });
    await ph.waitForFunction(() => /slide=3/.test(location.hash) && /tab=css/.test(location.hash), null, { timeout: 5000 }).catch(() => {});
    const hashed = await ph.evaluate(() => location.hash);
    await ph.reload();
    await ph.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    const back = await ph.evaluate(() => ({ slide: JSON.parse(window.__app.layoutJson()).slide, tab: window.__app.editorTab() }));
    await ph.evaluate(() => document.getElementById("present").click());
    await ph.waitForFunction(() => /view=present/.test(location.hash), null, { timeout: 5000 }).catch(() => {});
    await ph.reload();
    await ph.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    const shown = await ph.evaluate(() => { const l = JSON.parse(window.__app.layoutJson()); return { mode: l.mode, slide: l.slide, len: history.length }; });
    await ph.close();
    check("the address keeps the slide, the tab and the presentation over a reload", hashed === "#slide=3&tab=css" && back.slide === 2 && back.tab === "css" && shown.mode === "present" && shown.slide === 2, JSON.stringify({ hashed, back, shown }));
    // a new deck kept in this browser: the site's address stays as typed (no
    // #doc), and a reload opens this tab's deck even when another tab saved
    // a deck of its own since; an older link's #doc still opens its deck
    const pctx = await browser.newContext({ viewport: { width: 1200, height: 760 } });
    const pn = await pctx.newPage();
    await pn.goto(url);
    await pn.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    await pn.evaluate(() => window.__fileRequest("new"));
    await pn.waitForTimeout(300);
    await pn.keyboard.type("Vuokra ja menot");
    await pn.keyboard.press("Enter");
    await pn.waitForFunction(() => !!localStorage.getItem("evgp.doc"), null, { timeout: 8000 }).catch(() => {});
    await pn.waitForTimeout(500);
    const addr = await pn.evaluate(() => location.hash);
    const kept = await pn.evaluate(() => localStorage.getItem("evgp.doc"));
    await pn.evaluate(() => localStorage.setItem("evgp.doc", "some-other-deck"));
    await pn.reload();
    await pn.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    await pn.waitForTimeout(500);
    const reopened = await pn.evaluate(() => ({ md: window.__app.source().slice(0, 20), at: location.hash }));
    // a page of its own in the same browser (the deck is in its IndexedDB)
    const pl = await pctx.newPage();
    await pn.close();
    await pl.goto(url + "#doc=" + kept);
    await pl.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    await pl.waitForTimeout(500);
    const linked = await pl.evaluate(() => ({ md: window.__app.source().slice(0, 20), at: location.hash }));
    await pctx.close();
    check("a new deck leaves the address without #doc and a reload opens that deck", addr === "" && !!kept && reopened.md.startsWith("# Vuokra ja menot") && reopened.at === "", JSON.stringify({ addr, kept, reopened }));
    check("an older link's #doc opens its deck and leaves the address", linked.md.startsWith("# Vuokra ja menot") && linked.at === "", JSON.stringify(linked));
  }

  // Versions (web/versions.js) and one deck open in two places. Two tabs of
  // one browser: a change in one shows in the other, edits to different
  // lines are merged, edits to the same line ask, and a version restored
  // reaches both. Then two "devices" over a share kept by this script in
  // place of Firestore and Storage: a change made on one is taken by the
  // other when it gets the focus, edits are merged through the share, and
  // an assistant's edit to the share is taken.
  {
    const verr = [];
    const ctx = await browser.newContext({ viewport: { width: 1200, height: 760 } });
    const tab = async (u) => {
      const p = await ctx.newPage();
      p.on("pageerror", (e) => verr.push(e.message));
      await p.goto(u);
      await p.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
      return p;
    };
    const src = (p) => p.evaluate(() => window.__app.source());
    const edit = (p, from, to) => p.evaluate(([a, b]) => window.__app.setSource(window.__app.source().replace(a, b)), [from, to]);
    const base = "# Kaksi ikkunaa\n\n## Yksi\n\nrivi 1\nrivi 2\nrivi 3\n\n## Kaksi\n\nrivi 4\nrivi 5\nrivi 6\n";
    const a = await tab(url);
    await a.evaluate(() => window.__fileRequest("new"));
    await a.waitForTimeout(300);
    await a.keyboard.type("Kaksi ikkunaa");
    await a.keyboard.press("Enter");
    await a.waitForFunction(() => !!localStorage.getItem("evgp.doc"), null, { timeout: 8000 }).catch(() => {});
    await a.evaluate((md) => window.__app.setSource(md), base);
    await a.waitForTimeout(2500);
    const b = await tab(url + "#doc=" + (await a.evaluate(() => localStorage.getItem("evgp.doc"))));
    await edit(a, "rivi 1", "rivi 1 A");
    await b.waitForFunction(() => window.__app.source().includes("rivi 1 A"), null, { timeout: 15000 }).catch(() => {});
    check("versions: a change in one tab shows in the other", (await src(b)).includes("rivi 1 A"));
    await edit(a, "rivi 2", "rivi 2 A");
    await edit(b, "rivi 5", "rivi 5 B");
    await a.waitForTimeout(6000);
    const m = [await src(a), await src(b)];
    check("versions: edits to different lines in two tabs are merged", m[0] === m[1] && m[0].includes("rivi 2 A") && m[0].includes("rivi 5 B"), JSON.stringify(m));
    await edit(a, "rivi 3", "rivi 3 A");
    await edit(b, "rivi 3", "rivi 3 B");
    await a.waitForTimeout(4000);
    const asked = (await a.evaluate(() => !!document.getElementById("mergeCard"))) ? a : (await b.evaluate(() => !!document.getElementById("mergeCard"))) ? b : null;
    check("versions: the same line edited in two tabs asks", !!asked);
    if (asked) {
      await asked.evaluate(() => document.querySelector('#mergeCard input[value="both"]').click());
      await asked.evaluate(() => document.querySelector("#mergeCard button.primary").click());
    }
    await a.waitForTimeout(5000);
    const both = [await src(a), await src(b)];
    check("versions: both kept as asked, and the tabs agree", both[0] === both[1] && both[0].includes("rivi 3 A") && both[0].includes("rivi 3 B"), JSON.stringify(both));
    await a.evaluate(() => document.getElementById("history").click());
    await a.waitForSelector("#versions .vItem", { timeout: 8000 }).catch(() => {});
    await a.fill("#versions .vSave input", "Ennen palautusta");
    await a.click("#versions .vSave button");
    await a.waitForFunction(() => [...document.querySelectorAll("#versions .vMsg")].some((e) => e.textContent === "Ennen palautusta"), null, { timeout: 5000 }).catch(() => {});
    const msgs = await a.evaluate(() => [...document.querySelectorAll("#versions .vMsg")].map((e) => e.textContent));
    check("versions: the history lists versions, one saved by hand with its message", msgs.length >= 3 && msgs[0] === "Ennen palautusta", JSON.stringify(msgs));
    const tops = await a.$$("#versions .vItem .vTop");
    await tops[tops.length - 1].click();
    await a.waitForTimeout(400);
    await tops[tops.length - 2].click();
    await a.waitForTimeout(400);
    const links = await a.$$("#versions .vLink");
    if (links.length) await links[links.length - 1].click();
    await a.waitForTimeout(300);
    const diff = await a.evaluate(() => [...document.querySelectorAll("#versions .vDiff")].map((e) => e.textContent).join("\n"));
    check("versions: a version's changes as a unified diff", /^@@ /m.test(diff) && /^[-+]rivi/m.test(diff), diff.slice(0, 120));
    const before = await src(a);
    // View version: the version's slides in a frame, read only; the deck
    // in the editor stays as it is, Back closes the view
    await a.evaluate(() => [...document.querySelectorAll("#versions .vBody .vShow")].find((x) => x.offsetParent).click());
    const frame = await (await a.waitForSelector("#versionView iframe", { timeout: 8000 })).contentFrame();
    await frame.waitForFunction(() => window.__pageStarted === true && document.body.classList.contains("viewer") && window.__app.source().length > 0, null, { timeout: 90000 }).catch(() => {});
    const seen = await frame.evaluate(() => ({ md: window.__app.source(), mode: JSON.parse(window.__app.layoutJson()).mode })).catch((e) => ({ md: "", mode: String(e) }));
    check("versions: View version shows that version's slides in the viewer", seen.md.includes("Kaksi ikkunaa") && seen.md !== before && !seen.md.includes("rivi 3 A") && seen.mode === "present", JSON.stringify(seen).slice(0, 160));
    await a.waitForTimeout(2000);
    check("versions: viewing a version leaves the open deck as it is", (await src(a)) === before);
    await a.click("#versionView .vRow button:not(.primary)");
    check("versions: Back closes the view, the history stays", await a.evaluate(() => !document.getElementById("versionView") && !!document.getElementById("versions")));
    await a.evaluate(() => [...document.querySelectorAll("#versions .vBody .vRestore")].find((x) => x.offsetParent).click());
    await a.waitForTimeout(1500);
    const restored = await src(a);
    await b.waitForFunction((t) => window.__app.source() === t, restored, { timeout: 8000 }).catch(() => {});
    check("versions: the version restored is the one that was viewed", restored === seen.md);
    check("versions: a version restored, and the other tab follows", restored !== before && !restored.includes("rivi 3 A") && (await src(b)) === restored, JSON.stringify(restored));
    await ctx.close();

    // two devices and a share
    const shares = {};
    const objects = {};
    const ops = {
      share(deck) { const id = "check" + (Object.keys(shares).length + 1) + "share"; shares[id] = { ...deck, owner: "u1", head: null, log: [] }; return id; },
      saveShare(id, deck, since) {
        const cur = shares[id];
        if (since.md != null && cur.md !== since.md) return { error: "changed-elsewhere" };
        Object.assign(cur, deck);
        return cur.files;
      },
      load(id) { return shares[id] || null; },
      put(s, o, b64) { objects[s + "/" + o] = b64; return true; },
      get(s, o) { return objects[s + "/" + o] || null; },
      push(s, expect, head, entries) {
        const cur = shares[s];
        if (cur.head !== (expect || null) && cur.head !== head) return { ok: false, head: cur.head, log: cur.log };
        const seen = new Set(cur.log.map((e) => e.id));
        cur.log = cur.log.concat(entries.filter((e) => !seen.has(e.id)));
        cur.head = head;
        return { ok: true, head, log: cur.log };
      },
    };
    const device = async () => {
      const dc = await browser.newContext({ viewport: { width: 1200, height: 760 } });
      await dc.exposeFunction("__cloud", (op, args) => JSON.stringify(ops[op](...JSON.parse(args))));
      const p = await dc.newPage();
      p.on("pageerror", (e) => verr.push(e.message));
      await p.goto(url);
      await p.waitForFunction(() => window.__pageStarted === true && !!window.sliqtly, null, { timeout: 90000 });
      await p.evaluate(() => {
        const call = async (op, ...args) => {
          const r = JSON.parse(await window.__cloud(op, JSON.stringify(args)));
          if (r && r.error) throw Object.assign(new Error(r.error), { code: r.error });
          return r;
        };
        const b64 = (u8) => { let s = ""; for (const x of u8) s += String.fromCharCode(x); return btoa(s); };
        const asUrl = async (f) => {
          const blob = f.data instanceof Blob ? f.data : new Blob([f.data ?? ""], { type: f.type || "text/plain" });
          return { path: f.path, type: f.type || blob.type, size: blob.size, url: "data:application/octet-stream;base64," + b64(new Uint8Array(await blob.arrayBuffer())) };
        };
        const deckOf = async (d) => ({ name: d.name, md: d.md, theme: d.theme || "", css: d.css ?? null, files: await Promise.all((d.files || []).map(asUrl)) });
        Object.assign(window.sliqtly, {
          user: () => ({ uid: "u1" }),
          signedIn: async () => ({ uid: "u1" }),
          share: async (d) => call("share", await deckOf(d)),
          saveShare: async (id, d, since) => call("saveShare", id, await deckOf(d), { md: since.md }),
          loadShare: (id) => call("load", id),
          readHead: (id) => call("load", id),
          putObject: (s, o, bytes) => call("put", s, o, b64(bytes)),
          getObject: async (s, o) => {
            const x = await call("get", s, o);
            return x ? Uint8Array.from(atob(x), (c) => c.charCodeAt(0)) : null;
          },
          pushHead: (s, e, h, entries) => call("push", s, e, h, entries),
        });
        window.dispatchEvent(new Event("sliqtly:user"));
      });
      return { p, dc };
    };
    const A = await device();
    await A.p.evaluate(() => window.__fileRequest("new"));
    await A.p.waitForTimeout(300);
    await A.p.keyboard.type("Kaksi konetta");
    await A.p.keyboard.press("Enter");
    await A.p.waitForTimeout(500);
    await A.p.evaluate((md) => window.__app.setSource(md), base.replace("Kaksi ikkunaa", "Kaksi konetta"));
    for (let i = 0; i < 40 && !(Object.values(shares)[0]?.md || "").includes("rivi 6"); i++) await A.p.waitForTimeout(250);
    await A.p.evaluate(() => window.__checkElsewhere());
    await A.p.waitForTimeout(1500);
    const id = Object.keys(shares)[0];
    check("versions: a PRO deck's versions go up beside its share", !!id && !!shares[id].head && Object.keys(objects).length >= 3, String(Object.keys(objects).length));
    const B = await device();
    await B.p.evaluate((x) => window.__fileRequest("doc:cloud:" + x), id);
    await B.p.waitForTimeout(2500);
    await edit(B.p, "rivi 1", "rivi 1 B");
    for (let i = 0; i < 40 && !shares[id].md.includes("rivi 1 B"); i++) await B.p.waitForTimeout(250);
    await A.p.evaluate(() => window.dispatchEvent(new Event("focus")));
    await A.p.waitForFunction(() => window.__app.source().includes("rivi 1 B"), null, { timeout: 8000 }).catch(() => {});
    check("versions: the other device's change is taken when the window gets the focus", (await src(A.p)).includes("rivi 1 B"));
    await edit(A.p, "rivi 2", "rivi 2 A");
    await edit(B.p, "rivi 5", "rivi 5 B");
    await A.p.waitForTimeout(6000);
    for (let i = 0; i < 2; i++) {
      await A.p.evaluate(() => window.__checkElsewhere());
      await B.p.evaluate(() => window.__checkElsewhere());
      await A.p.waitForTimeout(3000);
    }
    const d = [await src(A.p), await src(B.p), shares[id].md];
    check("versions: edits on two devices are merged through the share", d[0] === d[1] && d[1] === d[2] && d[0].includes("rivi 2 A") && d[0].includes("rivi 5 B"), JSON.stringify(d));
    shares[id].md = shares[id].md.replace("rivi 6", "rivi 6 AI");
    await A.p.evaluate(() => window.__checkElsewhere());
    await A.p.waitForFunction(() => window.__app.source().includes("rivi 6 AI"), null, { timeout: 8000 }).catch(() => {});
    check("versions: an assistant's edit to the share is taken", (await src(A.p)).includes("rivi 6 AI"));
    await A.dc.close();
    await B.dc.close();
    check("no page errors with versions", verr.length === 0, verr.join(" | "));
  }

  // The Files tab as a data table (web/fileclip.js, UiPick): files ticked
  // in one deck are copied, a deck in another tab pastes them (a taken name
  // gets -2, a file already there is left be), and ticked files are deleted
  // together.
  {
    const ctx = await browser.newContext({ viewport: { width: 1300, height: 820 } });
    const fdir = fs.mkdtempSync(path.join(os.tmpdir(), "evgp-clip-"));
    fs.writeFileSync(path.join(fdir, "cars.json"), "[{\"a\":1}]");
    fs.writeFileSync(path.join(fdir, "ohlc.json"), "[{\"o\":2}]");
    fs.writeFileSync(path.join(fdir, "sales.csv"), "kk,euroa\ntammi,1\n");
    const deck = async (sample) => {
      const pg = await ctx.newPage();
      await pg.goto(url + "?sample=" + sample);
      await pg.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
      await pg.evaluate(() => { window.__app.setSource(window.__app.source() + "\n"); window.__app.showTab("files"); });
      await pg.waitForTimeout(1500);
      return pg;
    };
    const ask = (pg, r) => pg.evaluate((r) => { const a = window.__app; a.panels.requests.push(r); a.takePanels(); window.__handleRequests(); }, r);
    const files = (pg) => pg.evaluate(() => window.__docFiles()).then((l) => l.sort());
    const A = await deck("esittely");
    await A.setInputFiles("#fileadd", ["cars.json", "ohlc.json", "sales.csv"].map((f) => path.join(fdir, f)));
    await A.waitForFunction(() => window.__app.panels.filesJson.includes("sales.csv"), null, { timeout: 8000 }).catch(() => {});
    const ticked = await A.evaluate(() => {
      const pk = window.__app.panels.filesPick;
      pk.toggle("data/cars.json");
      pk.extendTo("data/sales.csv");
      return pk.selected();
    });
    await ask(A, "files:copy:data/cars.json\ndata/sales.csv");
    await A.waitForTimeout(800);
    const B = await deck("uutta");
    // the same name with other contents in B: pasted beside it
    fs.mkdirSync(path.join(fdir, "b"));
    fs.writeFileSync(path.join(fdir, "b", "ohlc.json"), "[{\"o\":3}]");
    await B.setInputFiles("#fileadd", [path.join(fdir, "b", "ohlc.json")]);
    await B.waitForTimeout(800);
    await ask(A, "files:copy:data/cars.json\ndata/ohlc.json\ndata/sales.csv");
    await B.waitForFunction(() => (JSON.parse(window.__app.panels.filesJson || "{}").clip || {}).count === 3, null, { timeout: 8000 }).catch(() => {});
    const clipB = await B.evaluate(() => JSON.parse(window.__app.panels.filesJson).clip);
    await B.evaluate(() => window.__fileRequest("paste"));
    await B.waitForTimeout(800);
    const pasted = await files(B);
    await B.evaluate(() => window.__fileRequest("paste"));
    await B.waitForTimeout(800);
    const again = await files(B);
    await ask(B, "files:delmany:data/ohlc.json\ndata/ohlc-2.json");
    await B.waitForTimeout(800);
    await B.evaluate(() => window.__app.showTab("files"));
    await B.waitForTimeout(800);
    const deleted = await files(B);
    check("Files: shift-click ticks the rows between", ticked.join() === "data/cars.json,data/ohlc.json,data/sales.csv", ticked.join());
    check("Files: another tab offers Paste for what was copied", clipB && clipB.count === 3, JSON.stringify(clipB));
    check("Files: Paste adds the copies, a taken name gets -2", ["data/cars.json", "data/ohlc.json", "data/ohlc-2.json", "data/sales.csv"].every((p) => pasted.includes(p)) && pasted.length === 4, pasted.join());
    check("Files: pasting again adds nothing that is already there", again.length === pasted.length, again.join());
    check("Files: ticked files are deleted together", deleted.join() === "data/cars.json,data/sales.csv", deleted.join());
    await ctx.close();
  }

  // The presentations open in a tab of the browser, a tab each in the row
  // under the top bar (PresApp deck tabs, EVGUI DocTabsCtl, web/decktabs.js):
  // a sample's tab takes the deck's id when it is kept, a press opens a
  // deck, a drag moves its tab, the row lasts over a reload, closing the tab
  // in front opens the one used before and keeps the deck, a finger swipes.
  {
    const ctx = await browser.newContext({ viewport: { width: 1400, height: 860 } });
    const derr = [];
    const pg = await ctx.newPage();
    pg.on("pageerror", (e) => derr.push(e.message));
    await pg.goto(url + "?sample=esittely");
    await pg.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    const row = (p) => p.evaluate(() => window.__app.deckTabsState().split("\n"));
    const keysOf = (r) => r.slice(1).map((l) => l.split("\t")[0]);
    const front = (p, k) => p.waitForFunction((k) => window.__app.deckTabFront() === k, k, { timeout: 30000 }).then(() => p.waitForTimeout(600), () => {});
    const go = async (p, k) => { await p.evaluate((k) => { window.__app.request("deck:switch:" + k); window.__handleRequests(); }, k); await front(p, k); };
    const boxOf = (p, id) => p.evaluate((id) => {
      const a = window.__app;
      a.chromeJson();
      const walk = (el) => { if (!el) return null; if (el.id === id) return el; for (let i = 0; i < el.getChildCount(); i++) { const f = walk(el.getChild(i)); if (f) return f; } return null; };
      const el = walk(a.chromeRoot);
      return el ? [el.calculatedX + el.calculatedWidth / 2, el.calculatedY + el.calculatedHeight / 2, el.calculatedWidth] : null;
    }, id);
    const drag = async (p, id, dx) => {
      const b = await boxOf(p, id);
      if (!b) return;
      await p.mouse.move(b[0], b[1]);
      await p.mouse.down();
      for (let i = 1; i <= 8; i++) await p.mouse.move(b[0] + (dx * i) / 8, b[1]);
      await p.mouse.up();
      await p.waitForTimeout(400);
      await p.evaluate(() => window.__handleRequests());
    };
    const one = await row(pg);
    await go(pg, "sample:uutta");
    await pg.evaluate(() => window.__app.setSource("# Myynti 2027\n\nLuvut.\n"));
    await pg.waitForFunction(() => !window.__app.deckTabFront().startsWith("sample:"), null, { timeout: 15000 }).catch(() => {});
    const kept = await row(pg);
    const keptId = kept[0];
    await go(pg, "sample:welcome");
    const three = await row(pg);
    const shape = await pg.evaluate(() => {
      const a = window.__app;
      a.chromeJson();
      const walk = (el, id) => { if (!el) return null; if (el.id === id) return el; for (let i = 0; i < el.getChildCount(); i++) { const f = walk(el.getChild(i), id); if (f) return f; } return null; };
      const r = walk(a.chromeRoot, "decktabs");
      const t = walk(a.chromeRoot, "decktabs-tab-sample:welcome");
      return { y: r.calculatedY, h: r.calculatedHeight, bar: a.toolbar.barH, wings: t.getChildCount(), edY: a.tabRect.y };
    });
    // a press on the first tab opens its deck
    await drag(pg, "decktabs-tab-sample:esittely", 0);
    await front(pg, "sample:esittely");
    const pressed = (await row(pg))[0];
    // dragged past the others: last
    await drag(pg, "decktabs-tab-sample:esittely", 420);
    const moved = keysOf(await row(pg));
    await pg.reload();
    await pg.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    await pg.waitForTimeout(800);
    const reloaded = await row(pg);
    // the tab in front closed: the one used before opens, the deck is kept
    await drag(pg, "decktabs-close-sample:esittely", 0);
    await front(pg, "sample:welcome");
    const closed = await row(pg);
    await go(pg, keptId);
    const back = await pg.evaluate(() => window.__app.source());
    check("deck tabs: the row is under the top bar and the editor under it", shape.y === shape.bar && shape.h === 40 && shape.edY === shape.bar + 40 && shape.wings === 3, JSON.stringify(shape));
    check("deck tabs: the deck shown has the one tab", one.length === 2 && one[0] === "sample:esittely", one.join(" | "));
    check("deck tabs: a sample's tab takes the deck's id once it is kept", keptId && !keptId.startsWith("sample:") && kept[2] === keptId + "\tMyynti 2027", kept.join(" | "));
    check("deck tabs: another deck adds a tab, in front", keysOf(three).join() === ["sample:esittely", keptId, "sample:welcome"].join() && three[0] === "sample:welcome", three.join(" | "));
    check("deck tabs: a press on a tab opens its deck", pressed === "sample:esittely", pressed);
    check("deck tabs: a tab dragged past the others goes last", moved.join() === [keptId, "sample:welcome", "sample:esittely"].join(), moved.join());
    check("deck tabs: the row and the tab in front last over a reload", keysOf(reloaded).join() === moved.join() && reloaded[0] === "sample:esittely", reloaded.join(" | "));
    check("deck tabs: closing the tab in front opens the one used before", closed[0] === "sample:welcome" && keysOf(closed).join() === [keptId, "sample:welcome"].join(), closed.join(" | "));
    check("deck tabs: a closed tab's deck is still kept", back.includes("Myynti 2027"), back.slice(0, 40));
    // a finger: a swipe to the right brings the deck on the left
    const phone = await browser.newContext({ viewport: { width: 390, height: 800 }, hasTouch: true, isMobile: true });
    const m = await phone.newPage();
    m.on("pageerror", (e) => derr.push(e.message));
    await m.goto(url + "?sample=esittely");
    await m.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
    await go(m, "sample:uutta");
    const rb = await boxOf(m, "decktabs");
    await m.evaluate(([x, y]) => { const a = window.__app; a.setTouch(true); a.pointerDown(x, y, false, 1); a.pointerMove(x + 120, y + 4); a.pointerUp(); window.__handleRequests(); }, [rb[0] - 60, rb[1]]);
    await front(m, "sample:esittely");
    const swiped = await row(m);
    check("deck tabs: a swipe to the right brings the deck on the left", swiped[0] === "sample:esittely", swiped.join(" | "));
    check("no page errors with deck tabs", derr.length === 0, derr.join(" | "));
    await phone.close();
    await ctx.close();
  }

  // the interface in another language: ?lang=fi, the canvas bar and the page alike
  const pageFi = await browser.newPage({ viewport: { width: 1200, height: 760 } });
  await pageFi.goto(url + "?lang=fi&sample=talous");
  await pageFi.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
  const fi = await pageFi.evaluate(() => ({
    lang: document.documentElement.lang,
    bar: window.__app.toolbarJson().includes("Esitä"),
    html: document.getElementById("present").textContent,
    langSel: document.getElementById("lang").value,
    langBar: window.__app.toolbarJson().includes("Suomi"),
    sample: window.__app.source().includes("# Oma talous haltuun"),
  }));
  await pageFi.close();
  check("?lang=fi: the bar drawn and the page's own words in Finnish", fi.lang === "fi" && fi.bar && /Esitä/.test(fi.html) && fi.langSel === "fi" && fi.langBar && fi.sample, JSON.stringify(fi));

  check("no page errors", errors.length === 0, errors.join(" | "));
} finally {
  await browser.close();
  server.close();
}
log(failures ? `${failures} failed` : "all passed");
process.exit(failures ? 1 : 0);
