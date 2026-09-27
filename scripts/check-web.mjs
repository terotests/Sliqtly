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
  await page.goto(url);
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
  check("the sample is five slides", r.slides === 5, String(r.slides));
  check("stage draws slide 1", r.stageTitle);
  check("slide 1 carries its starfield", r.stageFx === "starfield", r.stageFx);
  check("a build item is hidden before its step", !r.beforeStep);
  check("all four numbers at the end", r.afterAll === 4, String(r.afterAll));
  check("a frame is a function of its time", r.deterministic);
  check("typing a heading makes a slide", r.slidesAfterTyping === 6, String(r.slidesAfterTyping));
  check("the stage follows the caret to it", r.selectedAfterTyping === 5, String(r.selectedAfterTyping));
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
  await page.evaluate(() => { window.__app.endPresent(); window.__app.takeRequest(); });

  // A picture from the clipboard's point of view: bytes into the store,
  // markdown at the caret, a picture command on the slide.
  const pic = await page.evaluate(async () => {
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
  const n2 = notes.map((k) => pptx.get(k)).join(" ");
  check("the notes are the speaker's words without the cue marks", n2.includes("linkin saanut") && !n2.includes("[[1]]"));

  check("no page errors", errors.length === 0, errors.join(" | "));
} finally {
  await browser.close();
  server.close();
}
log(failures ? `${failures} failed` : "all passed");
process.exit(failures ? 1 : 0);
