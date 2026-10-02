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
    await ctx.close();
  }

  const page = await browser.newPage({ viewport: { width: 1400, height: 820 } });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(url + "?sample=esittely");
  await page.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
  await page.waitForTimeout(500);
  const shot = async (name) => { if (shots) { fs.mkdirSync(shots, { recursive: true }); await page.screenshot({ path: path.join(shots, name) }); } };
  await shot("1-editor.png");

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
  check("escape ends the presentation", r.backToEdit === "edit");

  await page.evaluate(() => { window.__app.present(true); window.__app.takeRequest(); window.__app.speaker = true; window.__app.next(); });
  await page.waitForTimeout(300);
  await shot("2-speaker.png");
  await page.evaluate(() => window.__app.endPresent());

  // A diagram that asks: present the Kulku slide, wait for the question,
  // move the highlight with an arrow, take it with Enter, then go back two
  // steps with two quick Backspaces and see the question again.
  await page.evaluate(() => window.__app.selectSlide(3));
  await page.keyboard.press("Shift+F5");
  // no tour of its own: the whole diagram, and nothing to answer
  await page.waitForTimeout(1500);
  const still = await page.evaluate(() => { const a = window.__app; const d = a.deck.slideAt(3).diagrams[0].diagram; return { asking: a.deck.askingAt(3, a.stageTime()), tour: d.tour, can: d.canTour }; });
  check("a diagram does not tour on its own: the whole of it, no question", still.asking < 0 && !still.tour && still.can, JSON.stringify(still));
  const playHit = await page.evaluate(() => { const a = window.__app; const u = a.deck.slideAt(3).diagrams[0]; return a.deck.diagramHit(3, a.stageTime(), u.bx + u.bw - 188, u.by + u.bh - 24); });
  check("the ▶ beside the zoom buttons is the tour", playHit === "0:tour", playHit);
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
  const page3 = await browser.newPage({ viewport: { width: 1200, height: 760 } });
  await page3.goto(showUrl.replace(/^https?:\/\/[^/]+/, url.replace(/\/$/, "")));
  await page3.waitForFunction(() => window.__pageStarted === true, null, { timeout: 90000 });
  await page3.waitForTimeout(300);
  const shown = await page3.evaluate(() => ({
    mode: JSON.parse(window.__app.layoutJson()).mode,
    bar: getComputedStyle(document.getElementById("bar")).display,
    viewer: document.body.classList.contains("viewer"),
  }));
  check("the presentation link opens presenting, without the toolbar", shown.mode === "present" && shown.bar === "none" && shown.viewer, JSON.stringify(shown));
  await page3.keyboard.press("Escape");
  await page3.waitForTimeout(200);
  check("Esc does not leave the shared presentation", (await page3.evaluate(() => JSON.parse(window.__app.layoutJson()).mode)) === "present");
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
    const plan = JSON.parse(a.adjustPlan());
    a.key("enter", false, false);
    window.__handleRequests();
    let after = before;
    for (let i = 0; i < 60; i += 1) {
      await new Promise((res) => setTimeout(res, 100));
      after = window.__picturePixel("/" + rel, 2, 2);
      if (after.join() !== before.join()) break;
    }
    a.showTab("md");
    return { row: true, hover, open, shown, plan, before, after, closed: !a.chartIsOpen() };
  }, pic.rel);
  check("a picture in the files tab shows a preview on hover", ed.row && ed.hover, JSON.stringify(ed));
  check("…a click opens the image editor with the picture in it", ed.open && ed.shown, JSON.stringify(ed));
  const grey = ed.after && Math.abs(ed.after[0] - ed.after[1]) < 4 && Math.abs(ed.after[1] - ed.after[2]) < 4;
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
    a.key("escape", false, false);
    a.setSource(src0);
    return { opened, drawn, line, typed, moved, stillOpen, closed, refused, looks, sized, dragged };
  });
  check("the chart editor opens on a vega-lite fence and draws", ce.opened && ce.drawn > 50, JSON.stringify(ce));
  check("…a kind picked rewrites the fence", ce.line, JSON.stringify(ce));
  check("…a number typed into the table goes into the chart", ce.typed, JSON.stringify(ce));
  check("…its window moves by the title bar", ce.moved === 100 && ce.stillOpen, JSON.stringify(ce));
  check("…a click outside closes it; a chart it cannot tabulate opens for its look and says why", ce.closed && ce.refused, JSON.stringify(ce));
  check("…its look: a palette, a glow and a picked text colour go into the fence", ce.looks, JSON.stringify(ce));
  check("…its width, from its slider", ce.sized, JSON.stringify(ce));

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
      for (;;) { if (!a.takeRequest()) break; }
      press(row);
      const reqs = [];
      for (;;) { const r = a.takeRequest(); if (!r) break; reqs.push(r); }
      const closed = !a.toolbarOnTop();
      a.showTab("md");
      return { onTop, underPanel, reqs, closed };
    });
    check("…over the Files tab the File menu is on top, and its row takes the press", onFiles.onTop && onFiles.underPanel && onFiles.reqs.includes("click:save") && onFiles.closed, JSON.stringify(onFiles));

    // the File menu's groups: new | open | save | the assistants, lines between them
    // that take no press
    const seps = await page.evaluate(() => {
      const a = window.__app;
      const all = () => { a.toolbarJson(); const out = []; const w = (e) => { if ((e.className || "").includes("ui-dropdownmenu-separator")) out.push(e); for (const k of e.children || []) w(k); }; w(a.toolbar.host.lastPage); return out; };
      const find = (id) => { a.toolbarJson(); const w = (e) => { if (e.id === id) return e; for (const k of e.children || []) { const r = w(k); if (r) return r; } return null; }; return w(a.toolbar.host.lastPage); };
      const press = (e) => { a.pointerDown(e.calculatedX + 10, e.calculatedY, false, 1); a.pointerUp(); };
      press(find("tb-m-file-trigger"));
      const lines = all();
      const ys = ["new", "openbox", "save", "aiClaude"].map((id) => find("tb-m-file-item-" + id).calculatedY);
      const between = lines.length === 3 && lines.every((l, i) => l.calculatedY > ys[i] && l.calculatedY < ys[i + 1] && l.calculatedHeight === 1);
      for (;;) { if (!a.takeRequest()) break; }
      press(lines[0]);
      const reqs = [];
      for (;;) { const r = a.takeRequest(); if (!r) break; reqs.push(r); }
      const stillOpen = a.toolbar.openMenu() !== "";
      a.key("escape", false, false);
      return { n: lines.length, between, reqs, stillOpen };
    });
    check("…the File menu is grouped by three lines, and a line takes no press", seps.between && seps.reqs.length === 0 && seps.stillOpen, JSON.stringify(seps));

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
    check("…File → Recent lists Browse all… first, then the decks, and each opens", recent.order.length === 4 && /browse$/.test(recent.order[0]) && /sep-1$/.test(recent.order[1]) && /r-deck-a$/.test(recent.order[2]) && (recent.browse || []).includes("showtab:files") && (recent.deck || []).includes("files:doc:deck-a"), JSON.stringify(recent));
  }

  // Edit in Claude / ChatGPT: File menu rows; signed out, the assistant opens
  // in a new tab with the deck's Markdown in its prompt
  {
    const rows = await page.evaluate(() => {
      const a = window.__app;
      const find = (id) => { a.toolbarJson(); const w = (e) => { if (e.id === id) return e; for (const k of e.children || []) { const r = w(k); if (r) return r; } return null; }; return w(a.toolbar.host.lastPage); };
      const press = (e) => { a.pointerDown(e.calculatedX + 10, e.calculatedY + 8, false, 1); a.pointerUp(); };
      for (;;) { if (!a.takeRequest()) break; }
      const reqs = [];
      for (const id of ["aiClaude", "aiChatgpt"]) {
        press(find("tb-m-file-trigger"));
        const row = find("tb-m-file-item-" + id);
        if (row) press(row);
        for (;;) { const r = a.takeRequest(); if (!r) break; reqs.push(r); }
      }
      return reqs;
    });
    await page.context().route(/^https:\/\/(claude\.ai|chatgpt\.com)\//, (r) => r.fulfill({ status: 200, contentType: "text/html", body: "<title>ai</title>" }));
    const urls = [];
    for (const id of ["aiClaude", "aiChatgpt"]) {
      const popup = page.waitForEvent("popup", { timeout: 5000 });
      await page.evaluate((id) => document.getElementById(id).click(), id);
      const p = await popup;
      await p.waitForURL(/^https:/, { timeout: 5000 }).catch(() => {});
      urls.push(p.url());
      await p.close();
    }
    const src = await page.evaluate(() => window.__app.source());
    const q = (u) => { try { return new URL(u).searchParams.get("q") || ""; } catch (_) { return ""; } };
    check("File → Edit in Claude / ChatGPT open the assistant with the deck's Markdown and the connector's tools in the prompt",
      rows.includes("click:aiClaude") && rows.includes("click:aiChatgpt")
      && urls[0].startsWith("https://claude.ai/new?q=") && urls[1].startsWith("https://chatgpt.com/?q=")
      && urls.every((u) => q(u).includes("create_presentation") && q(u).includes(src.split("\n").find((l) => l.trim()) || "")),
      JSON.stringify({ rows, urls: urls.map((u) => u.slice(0, 80)) }));
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
      // the drawn chart's own height (the box on the slide is fitted to the room)
      const box = (i) => { const es = a.deck.md.edit.layout.embeds.entries; const want = i === 1 ? "Otsikko" : "Toinen"; const e = es.find((x) => x.source.includes(want)); return e ? Math.round(e.height * 10) / 10 : -1; };
      const h1 = box(1), h2 = box(2);
      a.selectSlide(1);
      const help = JSON.parse(a.slideHelp()).find((f) => f.key === "chart");
      const sels = help ? help.rules.map((r) => r.sel) : [];
      a.setStyleSheet(css0 + "\n#mihin-raha-menee chart {\n  title-font-size: 44px;\n  title-gap: 40px;\n}\n");
      const s1 = box(1), s2 = box(2);
      a.setStyleSheet(css0);
      a.setSource(src0);
      return { h1, h2, s1, s2, sels };
    });
    check("the help names the slide's own chart rule by its heading's anchor", cc.sels.includes("#mihin-raha-menee chart"), JSON.stringify(cc));
    check("#anchor chart { } sizes only that slide's chart", cc.s1 > cc.h1 + 20 && Math.abs(cc.s2 - cc.h2) < 1, JSON.stringify(cc));
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
    await page.evaluate(() => { const a = window.__app; a.panels.requests.push("files:open:data/vfs-sales.csv"); a.takePanels(); });
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
  // one is at the end of the text
  const emoji = await page.evaluate(() => {
    const a = window.__app;
    const c = document.createElement("canvas").getContext("2d");
    c.font = "13px 'Open Sans'";
    const s = "## ✨ Key Features 📈 {fx=a}";
    return { ours: a.tr.measureWidth(s, 13), browser: c.measureText(s).width };
  });
  check("a line with emoji is measured as the browser draws it", Math.abs(emoji.ours - emoji.browser) < 1.5, JSON.stringify(emoji));

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
  const fxPptx = unzip(Buffer.from(fxExp.pptx, "base64"));
  const bgs = [2, 3, 4].map((n) => /<p:bg><p:bgPr><a:blipFill>/.test(fxPptx.get(`ppt/slides/slide${n}.xml`) || ""));
  check("PPTX: the effect is the slide's background, only where there is one", bgs.join(",") === "true,true,false", bgs.join(","));
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

  // PRO: a signed-in user's deck lives in the cloud (a share), against a
  // stand-in for Firebase kept here: saved on change under /s/{id}?edit,
  // opened from there on reload with its pictures, an assistant's change
  // picked up, a later edit written back, and a change made elsewhere not
  // written over.
  {
    const fakeDb = new Map();
    const fakeFiles = new Map();
    let puts = 0;
    const stamp = (o) => { for (const k of Object.keys(o)) if (o[k] && o[k].__ts) o[k] = Date.now(); return o; };
    const ctx = await browser.newContext({ viewport: { width: 1400, height: 820 } });
    await ctx.exposeFunction("__fakeFirebase", (op, a) => {
      if (op === "get") return fakeDb.get(a.k) ?? null;
      if (op === "set") fakeDb.set(a.k, stamp(a.merge ? { ...fakeDb.get(a.k), ...a.data } : a.data));
      else if (op === "update") {
        if (!fakeDb.has(a.k)) throw new Error("no document " + a.k);
        fakeDb.set(a.k, stamp({ ...fakeDb.get(a.k), ...a.data }));
      } else if (op === "put") { puts += 1; fakeFiles.set(a.p, { type: a.type, buf: Buffer.from(a.b64, "base64") }); }
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
      const db = { collection: (c) => ({ doc: (id) => ref(c, id) }) };
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
    const shareId = () => [...fakeDb.keys()].filter((k) => k.startsWith("shares/")).map((k) => k.slice(7));
    const filesListed = async (name) => {
      await pc.evaluate(() => window.__app.showTab("files"));
      await pc.waitForFunction((n) => window.__app.panels.filesJson.includes(n), name, { timeout: 8000 }).catch(() => {});
      return pc.evaluate((n) => window.__app.panels.filesJson.includes(n), name);
    };
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

    // changed elsewhere meanwhile: not written over
    fakeDb.set("shares/" + id, { ...fakeDb.get("shares/" + id), md: "# Muualla muutettu\n", updated: Date.now() });
    await pc.evaluate(() => window.__app.setSource(window.__app.source() + "\n## Vielä yksi\n"));
    await pc.waitForTimeout(5000);
    check("PRO: a deck changed elsewhere is not written over", fakeDb.get("shares/" + id).md === "# Muualla muutettu\n");

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
    check("PRO: a new presentation gets its own share and address", made.ids.length === 2 && made.at === "/s/" + newId + "?edit" && made.md.startsWith("# Uusi pakka") && fakeDb.get("shares/" + newId)?.md === made.md && fakeDb.get("shares/" + id).md === "# Muualla muutettu\n",
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
    await pc.waitForTimeout(3000);
    const dup = { md: await pc.evaluate(() => window.__app.source()), at: await pc.evaluate(() => location.pathname + location.search), ids: shareId() };
    const dupId = dup.ids.find((k) => k !== id && k !== newId && k !== origId) || "";
    const orig = fakeDb.get("shares/" + origId);
    check("PRO: Duplicate makes a copy with its own share, address, files and name",
      dup.ids.length === 4 && dup.at === "/s/" + dupId + "?edit" && /^# Alkuperäinen \((copy|kopio)\)\n/.test(dup.md) && dup.md.includes("## Kuva")
        && fakeDb.get("shares/" + dupId)?.md === dup.md && (fakeDb.get("shares/" + dupId)?.files || []).some((f) => f.path === "media/cloud-pic.png")
        && orig?.md.startsWith("# Alkuperäinen\n") && (orig.files || []).some((f) => f.path === "media/cloud-pic.png"),
      JSON.stringify({ at: dup.at, ids: dup.ids, md: dup.md.slice(0, 40) }));
    check("no page errors with PRO", perr.length === 0, perr.join(" | "));
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
  check("Open: the samples list starts with a deck, the prompt only on the trigger", samples.trigger === "Open sample document…" && samples.rows[0] === "talous=Finance: take charge of your money" && !samples.rows.some((r) => r.startsWith("=")), JSON.stringify(samples));

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
