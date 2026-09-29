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
import path from "node:path";
import http from "node:http";
import zlib from "node:zlib";
import { distDir, log } from "./lib.mjs";
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
  const types = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".md": "text/markdown", ".ttf": "font/ttf" };
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
const server = await serve();
const url = `http://127.0.0.1:${server.address().port}/`;
const browser = await chromium.launch({
  executablePath: chromiumPath(),
  args: ["--use-angle=swiftshader", "--enable-unsafe-swiftshader", "--ignore-gpu-blocklist"],
});
try {
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
    out.chromeTracks = texts(chrome).includes("Teksti") && texts(chrome).includes("Animaatio");
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
  await page2.close();
  // …and the presentation link: straight into the show, no toolbar, and Esc
  // does not lead back to an editor
  const showUrl = await page.evaluate(() => window.__lastShareShow || "");
  await page.evaluate(() => document.getElementById("shareDlg").close());
  check("share offers a presentation link", /mode=show/.test(showUrl));
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
    const stage = JSON.parse(a.stageJson());
    return {
      md: a.source().includes("](media/liitetty-"),
      image: stage.list.cmds.some((c) => c.k === 2 && String(c.src || "").includes("media/liitetty-")),
    };
  });
  check("a pasted picture is written into the markdown", pic.md);
  check("…and drawn on the slide", pic.image);
  await shot("3-picture.png");

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
    // a fence that is not a table says why, and can start from one
    a.setSource("# D\n\n## O\n\n```vega-lite\n{\"layer\": []}\n```\n");
    a.openChartEditor(5);
    const refused = a.chart.model.ok === false && a.chart.model.note.length > 0;
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
  check("…a click outside closes it; a chart it cannot tabulate says why", ce.closed && ce.refused, JSON.stringify(ce));
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
      press("tb-theme-trigger");
      const opened = t.theme.open;
      press("tb-theme-item-editorial");
      const reqs = [];
      for (;;) { const r = a.takeRequest(); if (!r) break; reqs.push(r); }
      press("tb-helpBtn");
      const help = [];
      for (;;) { const r = a.takeRequest(); if (!r) break; help.push(r); }
      return { opened, reqs, help, drawn: JSON.parse(a.toolbarJson()).list.cmds.length, theme0, htmlBarHidden: getComputedStyle(document.getElementById("bar")).display === "none" };
    });
    check("the top bar is drawn on the canvas, the HTML one hidden", bar.drawn > 20 && bar.htmlBarHidden, JSON.stringify(bar));
    check("…its theme list opens and a choice becomes the page's select change", bar.opened && bar.reqs.includes("select:theme:editorial"), JSON.stringify(bar));
    check("…a button is the page's button pressed", bar.help.includes("click:helpBtn"), JSON.stringify(bar));
    await page.evaluate((th) => { const s = document.getElementById("theme"); s.value = th; s.dispatchEvent(new Event("change")); }, bar.theme0);
    await page.waitForTimeout(300);
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
      a.pointerMove(sl.calculatedX + sl.calculatedWidth * 0.9, sl.calculatedY + 8);
      a.pointerUp();
      const after = a.themeCss().split("\n")[nl];
      a.closeHint();
      a.setStyleSheet(css0);
      a.showTab("md");
      a.setSource(src0);
      return { opened, drawn, chip, closed, kind, colour, slid: before !== after, before, after, htmlGone: !document.getElementById("valHint") };
    });
    check("the value popover is drawn on the canvas", hp.opened && hp.drawn > 10 && hp.htmlGone, JSON.stringify(hp));
    check("…a chip writes the value, Escape closes it", hp.chip && hp.closed, JSON.stringify(hp));
    check("…a colour from EVGUI's picker, a number from its slider", hp.kind === "color" && hp.colour && hp.slid, JSON.stringify(hp));
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
  const n2 = notes.map((k) => pptx.get(k)).join(" ");
  check("the notes are the speaker's words without the cue marks", n2.includes("linkin saanut") && !n2.includes("[[1]]"));

  check("no page errors", errors.length === 0, errors.join(" | "));
} finally {
  await browser.close();
  server.close();
}
log(failures ? `${failures} failed` : "all passed");
process.exit(failures ? 1 : 0);
