// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The browser host for the presentation editor. It owns what a browser owns:
//
//   the frame     one WebGL 2 canvas. The chrome (editor, tracks, filmstrip)
//                 is drawn first; each slide is a display list of its own,
//                 drawn through a camera into the rectangle the app names.
//   the clock     performance.now() while playing. The app answers with the
//                 time it accepted (a presentation holds at a build step
//                 until a click), and the clock is rebased on that answer.
//   the keyboard  a hidden field, as in gallery/r5: IME, dead keys and paste
//                 arrive for free.
//   the toolbar   plain HTML.
//   the files     open, save, PDF, PPTX, pictures pasted from the clipboard.
//
// What anything MEANS is PresApp.rgr's.

import { prepareDisplayList, setFontFallback } from "./gl/evg-webgl.js";

const canvas = document.getElementById("c");
const stageEl = document.getElementById("stage");
const keys = document.getElementById("keys");
const hintEl = document.getElementById("hint");
const errEl = document.getElementById("err");
const statusEl = document.getElementById("status");
const filePick = document.getElementById("filepick");
const sampleSel = document.getElementById("sample");
const themeSel = document.getElementById("theme");
const playBtn = document.getElementById("play");

const FACES = [
  ["Open Sans", "OpenSans-Regular.ttf"],
  ["Open Sans-Bold", "OpenSans-Bold.ttf"],
  ["Open Sans-Italic", "OpenSans-Italic.ttf"],
  ["Open Sans-BoldItalic", "OpenSans-BoldItalic.ttf"],
  ["Noto Sans", "NotoSans-Regular.ttf"],
  ["Noto Sans-Bold", "NotoSans-Bold.ttf"],
];
const THEMES = ["aurora", "corporate", "editorial"];
const SAMPLES = {
  esittely: ["Esittely: Gemini-botti", "./samples/esittely.md"],
  deck: ["Q3 Strategy (Ranger)", "./samples/deck.md"],
};

function fail(e) {
  errEl.textContent = String((e && e.stack) || e);
  console.error(e);
}

/** Ranger's `buffer` is an ArrayBuffer with a DataView hung off it. */
function asRangerBuffer(ab) {
  ab._view = new DataView(ab);
  return ab;
}

async function bytesOf(url) {
  const res = await fetch(url);
  if (!res.ok) throw new Error(url + " → " + res.status);
  return await res.arrayBuffer();
}

async function textOf(url) {
  const res = await fetch(url);
  if (!res.ok) throw new Error(url + " → " + res.status);
  return await res.text();
}

const gl = canvas.getContext("webgl2", { antialias: true, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
if (!gl) {
  hintEl.textContent = "WebGL 2 ei ole käytettävissä tässä selaimessa.";
  throw new Error("no WebGL 2");
}
if (typeof globalThis.PresApp !== "function") {
  hintEl.textContent = "pres_app.js puuttuu. Aja `npm run build`.";
  throw new Error("engine bundle not loaded");
}
const app = new globalThis.PresApp();
window.__app = app;

let dpr = Math.min(window.devicePixelRatio || 1, 2);
let W = 0;
let H = 0;
let needsPaint = true;
let lastRev = "";

function resize() {
  const r = stageEl.getBoundingClientRect();
  W = Math.max(320, Math.floor(r.width));
  H = Math.max(240, Math.floor(r.height));
  dpr = Math.min(window.devicePixelRatio || 1, 2);
  canvas.width = Math.round(W * dpr);
  canvas.height = Math.round(H * dpr);
  app.setPageSize(W, H);
  dropThumbs();
  needsPaint = true;
}

// --- pictures -------------------------------------------------------------------
const pictures = new Map();
async function registerPicture(path, bytes, type) {
  const url = URL.createObjectURL(new Blob([bytes], { type }));
  await new Promise((resolve) => {
    const img = new Image();
    img.onload = () => { pictures.set(path, img); resolve(); };
    img.onerror = () => { pictures.set(path, null); resolve(); };
    img.src = url;
  });
}

let pasteCount = 0;
async function addPictureFile(file) {
  const type = file.type || "image/png";
  const ext = (type.split("/")[1] || "png").replace("jpeg", "jpg").replace("svg+xml", "svg");
  pasteCount += 1;
  const rel = `media/liitetty-${Date.now().toString(36)}-${pasteCount}.${ext}`;
  const bytes = await file.arrayBuffer();
  let w = 0;
  let h = 0;
  try {
    const bmp = await createImageBitmap(new Blob([bytes], { type }));
    w = bmp.width;
    h = bmp.height;
    bmp.close();
  } catch (_) { /* sized by the layout's default */ }
  app.addImage("/" + rel, asRangerBuffer(bytes.slice(0)), type, w, h);
  await registerPicture("/" + rel, bytes, type);
  app.insertPicture(rel, file.name && file.name !== "image.png" ? file.name.replace(/\.[^.]+$/, "") : "kuva");
  dropThumbs();
  afterInput();
}

// --- painting -------------------------------------------------------------------
// Thumbnails are the slides at rest, so they are built once per deck revision
// and kept; the stage and the chrome are built every paint. A kept frame is
// only good while the glyph atlas it was built against stands, so a paint
// that grows the atlas drops the kept ones and draws again.
let thumbs = new Map();
let thumbRev = -1;
function dropThumbs() {
  for (const f of thumbs.values()) f.dispose();
  thumbs = new Map();
}

const fxStart = performance.now();
function effectClock(layout) {
  // Playing or presenting: the deck's own time, so a replay is the same
  // picture. Editing: a free-running clock so a starfield still moves.
  return layout.playing ? layout.time : (performance.now() - fxStart) / 1000;
}

function withTime(doc, t) {
  const fx = doc.list && doc.list.effects;
  if (fx) for (const e of fx) e.time = t;
  return doc;
}

let lastLayout = null;
function paintOnce() {
  errEl.textContent = "";
  const layout = JSON.parse(app.layoutJson());
  lastLayout = layout;
  if (layout.rev !== thumbRev) {
    dropThumbs();
    thumbRev = layout.rev;
  }
  const t = effectClock(layout);
  // THE GLYPH ATLAS IS SHARED, and when a frame needs glyphs it does not hold
  // it grows into a NEW texture and deletes the old one. A kept thumbnail
  // still points at the old one, so drawing it after that is
  // "bindTexture: attempt to use a deleted object". So: the chrome and the
  // stage are built first, and if either grew the atlas the kept thumbnails
  // are dropped before any is drawn; a thumbnail built now that grows it
  // makes the others stale for the NEXT paint, so they are dropped after.
  const grewBy = (stats) => !!(stats && (stats.atlasRebuilt || stats.atlasAdded > 0));
  let grew = false;
  const chrome = JSON.parse(app.chromeJson());
  window.__lastChrome = chrome;
  const cf = prepareDisplayList(gl, chrome, { dpr });
  grew = grewBy(cf.draw(null, null)) || grew;
  cf.dispose();
  if (layout.slides > 0) {
    const st = withTime(JSON.parse(app.stageJson()), t);
    window.__lastStage = st;
    st.width = W;
    st.height = H;
    const sf = prepareDisplayList(gl, st, { dpr, images: pictures });
    grew = grewBy(sf.draw(null, [layout.stage[0], layout.stage[1], layout.stage[2]], { clear: false })) || grew;
    sf.dispose();
  }
  if (grew) dropThumbs();
  let thumbsGrew = false;
  for (const [i, x, y, s] of layout.thumbs) {
    let f = thumbs.get(i);
    const fresh = !f;
    if (fresh) {
      const doc = withTime(JSON.parse(app.slideJson(i)), 2.0);
      doc.width = W;
      doc.height = H;
      f = prepareDisplayList(gl, doc, { dpr, images: pictures });
      thumbs.set(i, f);
    }
    const stats = f.draw(null, [x, y, s], { clear: false });
    if (fresh && grewBy(stats)) thumbsGrew = true;
  }
  if (thumbsGrew) dropThumbs();
  statusEl.textContent = app.statusText();
  playBtn.textContent = layout.playing && layout.mode === "edit" ? "⏸ Pysäytä" : "▶ Toista";
  return layout;
}

// --- the clock --------------------------------------------------------------------
let clockBase = 0;
let clockAt = 0;
let presentStartedAt = 0;
function rebaseClock() {
  clockBase = app.currentTime();
  clockAt = performance.now();
}

function frame() {
  try {
    const now = performance.now();
    if (app.isPlaying()) {
      const want = clockBase + (now - clockAt) / 1000;
      const got = app.setTime(want);
      if (got < want - 1e-6) {
        clockBase = got;
        clockAt = now;
      }
      if (lastLayout && lastLayout.mode === "present") app.setElapsed((now - presentStartedAt) / 1000);
      needsPaint = true;
    }
    const rev = app.revision();
    const effects = window.__lastStage && window.__lastStage.list && window.__lastStage.list.effects && window.__lastStage.list.effects.length > 0;
    if (needsPaint || rev !== lastRev || effects) {
      needsPaint = false;
      lastRev = rev;
      paintOnce();
      handleRequests();
    }
  } catch (e) {
    fail(e);
  }
  requestAnimationFrame(frame);
}

// --- what the app asks the page to do ---------------------------------------------
function deliver(bytes, name, mime) {
  const view = bytes instanceof ArrayBuffer ? new Uint8Array(bytes) : bytes;
  if (!view || !view.length) return "empty";
  const blob = new Blob([view], { type: mime });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 4000);
  return "downloaded";
}
window.__lastDownload = "";

function handleRequests() {
  for (;;) {
    const r = app.takeRequest();
    if (!r) break;
    if (r === "fullscreen") {
      document.body.classList.add("presenting");
      presentStartedAt = performance.now();
      rebaseClock();
      if (document.documentElement.requestFullscreen && !document.fullscreenElement) {
        document.documentElement.requestFullscreen().catch(() => {});
      }
      // Going full screen can take the focus away from the field the keys
      // arrive in; a presentation without keys is a slideshow nobody can drive.
      keys.focus({ preventScroll: true });
      requestAnimationFrame(resize);
    } else if (r === "exit-fullscreen") {
      document.body.classList.remove("presenting");
      if (document.fullscreenElement && document.exitFullscreen) document.exitFullscreen().catch(() => {});
      requestAnimationFrame(resize);
    }
  }
}

document.addEventListener("fullscreenchange", () => {
  keys.focus({ preventScroll: true });
  if (!document.fullscreenElement && lastLayout && lastLayout.mode === "present") {
    app.endPresent();
    handleRequests();
  }
  requestAnimationFrame(resize);
});

let docName = "esitys";

// --- the toolbar ------------------------------------------------------------------
for (const [key, [label]] of Object.entries(SAMPLES)) {
  const o = document.createElement("option");
  o.value = key;
  o.textContent = label;
  sampleSel.appendChild(o);
}
sampleSel.addEventListener("change", () => openSample(sampleSel.value));
themeSel.addEventListener("change", () => useTheme(themeSel.value));
playBtn.addEventListener("click", () => {
  if (app.isPlaying()) app.stop(); else app.play();
  rebaseClock();
  needsPaint = true;
  keys.focus({ preventScroll: true });
});
document.getElementById("present").addEventListener("click", (ev) => {
  app.present(ev.shiftKey);
  handleRequests();
  keys.focus({ preventScroll: true });
});
document.getElementById("open").addEventListener("click", () => filePick.click());
document.getElementById("save").addEventListener("click", () => {
  window.__lastDownload = deliver(new TextEncoder().encode(app.source()), docName + ".md", "text/markdown");
});
document.getElementById("pdf").addEventListener("click", () => {
  try {
    window.__lastDownload = deliver(app.pdf(), docName + ".pdf", "application/pdf");
  } catch (e) { fail(e); }
});
document.getElementById("pptx").addEventListener("click", () => {
  try {
    window.__lastDownload = deliver(app.pptx(), docName + ".pptx",
      "application/vnd.openxmlformats-officedocument.presentationml.presentation");
  } catch (e) { fail(e); }
});
filePick.addEventListener("change", async () => {
  const file = filePick.files && filePick.files[0];
  if (!file) return;
  docName = file.name.replace(/\.(md|markdown|txt)$/i, "") || "esitys";
  app.setSource(await file.text());
  filePick.value = "";
  dropThumbs();
  needsPaint = true;
});

const themeCss = {};
function useTheme(key) {
  app.setStyleSheet(key ? themeCss[key] || "" : "");
  dropThumbs();
  needsPaint = true;
}

async function openSample(key) {
  const s = SAMPLES[key];
  if (!s) return;
  try {
    docName = key;
    app.setSource(await textOf(s[1]));
    dropThumbs();
    needsPaint = true;
  } catch (e) {
    fail(e);
  }
}

// --- the keyboard -------------------------------------------------------------------
const KEY_MAP = {
  Backspace: "backspace", Enter: "enter", Tab: "tab", Delete: "delete",
  ArrowLeft: "left", ArrowRight: "right", ArrowUp: "up", ArrowDown: "down",
  Home: "home", End: "end", PageUp: "pageUp", PageDown: "pageDown", Escape: "escape",
};
const CLIPBOARD_CHORD = /^[cxvCXV]$/;
let composing = false;

// What the field was last set to. Anything else in it got there without
// passing through beforeinput, and the input handler below carries it over.
let mirrored = "";
function mirrorLine() {
  if (app.focusTarget() !== "editor" || (lastLayout && lastLayout.mode === "present")) {
    if (keys.value !== "") keys.value = "";
    mirrored = "";
    return;
  }
  const line = app.currentLine();
  if (keys.value !== line) keys.value = line;
  mirrored = line;
  const col = Math.max(0, Math.min(line.length, app.caretCol()));
  let from = col;
  let to = col;
  if (app.hasSelection() && app.anchorLine() === app.caretLine()) {
    const anchor = Math.max(0, Math.min(line.length, app.anchorCol()));
    from = Math.min(anchor, col);
    to = Math.max(anchor, col);
  }
  try { keys.setSelectionRange(from, to); } catch (_) { /* detached */ }
}

let blinkPhase = 0;
let blinkTimer = 0;
function restartBlink() {
  blinkPhase = 0;
  app.setBlink(0);
  clearInterval(blinkTimer);
  blinkTimer = setInterval(() => {
    if (document.activeElement !== keys) return;
    blinkPhase += 1;
    app.setBlink(blinkPhase);
  }, 530);
}

function focusKeys(where) {
  app.setFocus(where);
  keys.focus({ preventScroll: true });
  mirrorLine();
  restartBlink();
}

function afterInput() {
  mirrorLine();
  handleRequests();
  rebaseClock();
  needsPaint = true;
}

keys.addEventListener("keydown", (ev) => {
  // A composition that ended without a compositionend (a dead key, an IME
  // cancelled by a click) must not leave typing switched off.
  if (!ev.isComposing && ev.keyCode !== 229) composing = false;
  const presenting = lastLayout && lastLayout.mode === "present";
  if (ev.key === "F5") {
    ev.preventDefault();
    app.present(!ev.shiftKey);
    afterInput();
    return;
  }
  if (!presenting && (ev.ctrlKey || ev.metaKey) && ev.key === "Enter") {
    ev.preventDefault();
    if (app.isPlaying()) app.stop(); else app.play();
    afterInput();
    return;
  }
  const mod = ev.ctrlKey || ev.metaKey;
  const special = KEY_MAP[ev.key];
  if (special) {
    if (special === "tab" && app.focusTarget() !== "editor") return;
    if (app.key(special, ev.shiftKey, mod)) ev.preventDefault();
    else if (app.focusTarget() === "editor") ev.preventDefault();
    afterInput();
    return;
  }
  if (presenting) {
    if (ev.key.length === 1) {
      ev.preventDefault();
      app.text(ev.key);
      afterInput();
    }
    return;
  }
  if (mod && ev.key.length === 1) {
    if (CLIPBOARD_CHORD.test(ev.key)) return; // copy / cut / paste fire on the field
    if (/^[azyAZY]$/.test(ev.key)) {
      ev.preventDefault();
      app.chord(ev.key.toLowerCase());
      afterInput();
    }
  }
});

document.addEventListener("keydown", (ev) => {
  if (ev.target === keys || !(lastLayout && lastLayout.mode === "present")) return;
  keys.focus({ preventScroll: true });
  keys.dispatchEvent(new KeyboardEvent("keydown", { key: ev.key, code: ev.code, shiftKey: ev.shiftKey, ctrlKey: ev.ctrlKey, metaKey: ev.metaKey, altKey: ev.altKey, bubbles: false, cancelable: true }));
  ev.preventDefault();
});

keys.addEventListener("compositionstart", () => { composing = true; });
keys.addEventListener("compositionend", (ev) => {
  composing = false;
  if (ev.data) app.text(ev.data);
  afterInput();
});

keys.addEventListener("beforeinput", (ev) => {
  if (composing || ev.isComposing) return;
  const type = ev.inputType;
  let handled = true;
  if (type === "insertText" && ev.data) app.text(ev.data);
  else if (type === "insertLineBreak" || type === "insertParagraph") app.key("enter", false, false);
  else if (type === "deleteContentBackward") app.key("backspace", false, false);
  else if (type === "deleteContentForward") app.key("delete", false, false);
  else if (type === "historyUndo") app.undo();
  else if (type === "historyRedo") app.redo();
  else handled = false;
  if (!handled) return; // the input handler below takes what arrives
  ev.preventDefault();
  afterInput();
});

// The safety net: text that reached the field anyway (no beforeinput, a
// browser that does not let it be cancelled, a composition that inserted
// directly) is the difference between the field and what was mirrored.
keys.addEventListener("input", (ev) => {
  if (composing || ev.isComposing) return;
  const now = keys.value;
  if (now === mirrored) return;
  let a = 0;
  while (a < now.length && a < mirrored.length && now[a] === mirrored[a]) a += 1;
  let z = 0;
  while (z < now.length - a && z < mirrored.length - a && now[now.length - 1 - z] === mirrored[mirrored.length - 1 - z]) z += 1;
  const removed = mirrored.length - a - z;
  const inserted = now.slice(a, now.length - z);
  for (let i = 0; i < removed; i += 1) app.key("backspace", false, false);
  if (inserted) app.text(inserted);
  afterInput();
});

keys.addEventListener("copy", (ev) => {
  const text = app.copySelection();
  if (!text) return;
  ev.preventDefault();
  ev.clipboardData?.setData("text/plain", text);
});
keys.addEventListener("cut", (ev) => {
  const text = app.cutSelection();
  if (!text) return;
  ev.preventDefault();
  ev.clipboardData?.setData("text/plain", text);
  afterInput();
});
keys.addEventListener("paste", (ev) => {
  ev.preventDefault();
  const items = ev.clipboardData ? [...ev.clipboardData.items] : [];
  const picture = items.find((it) => it.kind === "file" && /^image\/(png|jpeg|gif|webp)$/.test(it.type));
  if (picture) {
    const file = picture.getAsFile();
    if (file) addPictureFile(file).catch(fail);
    return;
  }
  const text = ev.clipboardData?.getData("text/plain") || "";
  if (text) {
    app.text(text);
    afterInput();
  }
});
keys.addEventListener("focus", () => { composing = false; mirrorLine(); needsPaint = true; });
keys.addEventListener("blur", () => { composing = false; });

// --- the pointer ----------------------------------------------------------------------
function at(ev) {
  const r = canvas.getBoundingClientRect();
  return [ev.clientX - r.left, ev.clientY - r.top];
}

let clicks = 0;
let lastDown = 0;
canvas.addEventListener("pointerdown", (ev) => {
  const [x, y] = at(ev);
  const now = performance.now();
  clicks = now - lastDown < 400 ? clicks + 1 : 1;
  lastDown = now;
  const where = app.pointerDown(x, y, ev.shiftKey, Math.min(clicks, 3));
  ev.preventDefault();
  if (where === "editor" || where === "sep" || where === "scrub") {
    try { canvas.setPointerCapture(ev.pointerId); } catch (_) { /* no capture */ }
  }
  focusKeys(where === "editor" ? "editor" : app.focusTarget());
  afterInput();
});
canvas.addEventListener("pointermove", (ev) => {
  const [x, y] = at(ev);
  app.pointerMove(x, y);
  canvas.style.cursor = app.cursorAt(x, y);
  if (ev.buttons) needsPaint = true;
});
function endPointer() {
  app.pointerUp();
  mirrorLine();
  needsPaint = true;
}
canvas.addEventListener("pointerup", endPointer);
canvas.addEventListener("pointercancel", endPointer);
canvas.addEventListener("wheel", (ev) => {
  const [x, y] = at(ev);
  const step = ev.deltaMode === 1 ? 18 : ev.deltaMode === 2 ? 400 : 1;
  const d = Math.abs(ev.deltaY) >= Math.abs(ev.deltaX) ? ev.deltaY : ev.deltaX;
  if (app.wheel(x, y, d * step)) {
    ev.preventDefault();
    needsPaint = true;
  }
}, { passive: false });

// A picture dropped on the canvas goes in like a pasted one.
canvas.addEventListener("dragover", (ev) => ev.preventDefault());
canvas.addEventListener("drop", (ev) => {
  ev.preventDefault();
  const files = ev.dataTransfer ? [...ev.dataTransfer.files] : [];
  for (const f of files) {
    if (/^image\//.test(f.type)) addPictureFile(f).catch(fail);
    else if (/\.(md|markdown|txt)$/i.test(f.name)) {
      f.text().then((t) => { docName = f.name.replace(/\.[^.]+$/, ""); app.setSource(t); dropThumbs(); needsPaint = true; });
    }
  }
});

// --- start ------------------------------------------------------------------------------
async function start() {
  const css = await textOf("./pres.css");
  const r = stageEl.getBoundingClientRect();
  app.init(css, Math.max(320, r.width), Math.max(240, r.height));
  resize();
  window.addEventListener("resize", resize);

  const got = new Array(FACES.length).fill(false);
  await Promise.all(FACES.map(async ([name, file], i) => {
    try {
      const bytes = await bytesOf("./fonts/" + file);
      got[i] = app.attachFont(name, asRangerBuffer(bytes.slice(0)));
      const face = new FontFace(name, bytes);
      await face.load();
      document.fonts.add(face);
    } catch (e) {
      console.warn("face not loaded: " + name, e);
    }
  }));
  setFontFallback(FACES.filter((_, i) => got[i]).map(([name]) => name));

  for (const name of THEMES) {
    try {
      themeCss[name] = await textOf("./themes/" + name + ".css");
      app.addTemplate(name, themeCss[name]);
    } catch (_) { /* one template fewer */ }
  }

  const q = new URLSearchParams(location.search);
  const theme = q.has("theme") ? q.get("theme") : "aurora";
  themeSel.value = theme;
  app.setStyleSheet(theme ? themeCss[theme] || "" : "");
  const sample = SAMPLES[q.get("sample")] ? q.get("sample") : "esittely";
  sampleSel.value = sample;
  await openSample(sample);

  hintEl.remove();
  focusKeys("editor");
  window.__pageStarted = true;
  requestAnimationFrame(frame);
}

start().catch(fail);
