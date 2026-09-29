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
const THEMES = ["aurora", "nebula", "carbon", "ember", "midnight", "corporate", "editorial"];
const SAMPLES = {
  talous: ["Talous: oma talous haltuun", "./samples/talous.md"],
  ymparisto: ["Ympäristö: hiilijalanjälki", "./samples/ymparisto.md"],
  urheilu: ["Urheilu: 5 km juoksukoulu", "./samples/urheilu.md"],
  kulttuuri: ["Kulttuuri: musiikin vuosikymmenet", "./samples/kulttuuri.md"],
  ohjelmointi: ["Ohjelmointi: versionhallinta", "./samples/ohjelmointi.md"],
  matematiikka: ["Matematiikka: kaavat kalvoilla", "./samples/matematiikka.md"],
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

// The build's stamp (scripts/build.mjs writes it in). Every file of the
// page's own is fetched with it, so a new build is never drawn with an old
// stylesheet the browser kept — the chart editor's sheets were, and a new
// editor came up in the old one's colours.
const BUILD = "__BUILD__";
function fresh(url) {
  if (!url.startsWith("./") || BUILD.startsWith("__")) return url;
  return url + (url.includes("?") ? "&" : "?") + "v=" + BUILD;
}

async function bytesOf(url) {
  const res = await fetch(fresh(url));
  if (!res.ok) throw new Error(url + " → " + res.status);
  return await res.arrayBuffer();
}

async function textOf(url) {
  const res = await fetch(fresh(url));
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
  // the top bar, over the chrome (its lists open over everything under it)
  if (canvasBar) {
    syncToolbar();
    const tj = app.toolbarJson();
    if (tj) {
      const tb = JSON.parse(tj);
      tb.width = W;
      tb.height = H;
      const tf = prepareDisplayList(gl, tb, { dpr });
      if (grewBy(tf.draw(null, [0, 0, 1], { clear: false }))) dropThumbs();
      tf.dispose();
    }
  }
  // the value popover, over the editor and the bar
  const hj = app.hintJson();
  if (hj) {
    const hp = JSON.parse(hj);
    hp.width = W;
    hp.height = H;
    const hf = prepareDisplayList(gl, hp, { dpr });
    if (grewBy(hf.draw(null, [0, 0, 1], { clear: false }))) dropThumbs();
    hf.dispose();
  }
  // the chart editor, over everything
  if (app.chartIsOpen()) {
    const cj = JSON.parse(app.chartJson());
    // placed like a thumbnail: a page the size of the canvas, moved by the camera
    cj.width = W;
    cj.height = H;
    const ce = prepareDisplayList(gl, cj, { dpr });
    if (grewBy(ce.draw(null, [cj.x, cj.y, 1], { clear: false }))) dropThumbs();
    ce.dispose();
  }
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
    app.setUiTime(now / 1000);
    if (app.uiBusy()) needsPaint = true;
    if (app.isPlaying()) {
      const want = clockBase + (now - clockAt) / 1000;
      const got = app.setTime(want);
      // held back (a step, a question) or moved on (a skipped question):
      // the clock goes on from where the app put it
      if (Math.abs(got - want) > 1e-6) {
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
    helpTick(now);
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
      if (!viewer && document.documentElement.requestFullscreen && !document.fullscreenElement) {
        document.documentElement.requestFullscreen().catch(() => {});
      }
      // Going full screen can take the focus away from the field the keys
      // arrive in; a presentation without keys is a slideshow nobody can drive.
      keys.focus({ preventScroll: true });
      requestAnimationFrame(resize);
    } else if (r.startsWith("click:")) {
      // the canvas bar: the page's own button does what it always did
      const b = document.getElementById(r.slice(6));
      if (b) b.click();
    } else if (r.startsWith("select:")) {
      const [, id, ...rest] = r.split(":");
      const sel = document.getElementById(id);
      if (sel) {
        sel.value = rest.join(":");
        sel.dispatchEvent(new Event("change"));
      }
    } else if (r === "theme-edited") {
      editedCss[themeSel.value || ""] = app.themeCss();
      dropThumbs();
    } else if (r === "exit-fullscreen") {
      document.body.classList.remove("presenting");
      if (document.fullscreenElement && document.exitFullscreen) {
        leavingByApp = true;
        document.exitFullscreen().catch(() => { leavingByApp = false; });
      }
      requestAnimationFrame(resize);
    }
  }
}

// Leaving full screen ends the presentation only when the VIEWER left it
// (Esc handled by the browser). The app's own exit arrives here later, and
// by then a new presentation may already have started: ending that one was
// a race.
let leavingByApp = false;
let fullscreenOn = false;
document.addEventListener("fullscreenchange", () => {
  keys.focus({ preventScroll: true });
  if (document.fullscreenElement) {
    fullscreenOn = true;
  } else {
    const byApp = leavingByApp;
    leavingByApp = false;
    const was = fullscreenOn;
    fullscreenOn = false;
    if (!viewer && !byApp && was && app.isPlaying() && JSON.parse(app.layoutJson()).mode === "present") {
      app.endPresent();
      handleRequests();
    }
  }
  requestAnimationFrame(resize);
});

let docName = "esitys";

// --- the toolbar ------------------------------------------------------------------
// Not in the menu: the deck the page checks drive (npm run check:web opens
// it with ?sample=esittely), kept as it is so the checks stay put.
const HIDDEN_SAMPLES = { esittely: ["Esittely", "./samples/esittely.md"] };
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
document.getElementById("share").addEventListener("click", () => { shareLink().catch(fail); });
document.getElementById("save").addEventListener("click", () => {
  window.__lastDownload = deliver(new TextEncoder().encode(app.source()), docName + ".md", "text/markdown");
});
// An export is named after the deck: its front matter title, else its first
// heading, else the file it came from. Only what a file system refuses is
// taken out; spaces and letters like ä stay.
function exportName() {
  const t = String(app.docTitle() || "")
    .replace(/[\/\\:*?"<>|\u0000-\u001f]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .replace(/^\.+|\.+$/g, "")
    .slice(0, 120)
    .trim();
  return t || docName;
}
window.__exportName = exportName;
// A slide's effect is a shader, which neither a PDF nor a PowerPoint file can
// hold. Before an export each one is drawn here, on its slide's paper and at
// the moment the thumbnails show it, into a canvas of its own; the app puts
// the picture under the slide's content (the PDF) or behind it as the slide
// background (the PPTX). Text and shapes stay vector on top.
const FX_STILL_W = 1600;
async function renderFxStills() {
  app.clearFxStills();
  const list = JSON.parse(app.fxSlidesJson());
  if (!list.length) return;
  toast(`Piirretään ${list.length} dian efektit vientiä varten…`);
  // the toast gets a frame to show before the work starts
  await new Promise((r) => requestAnimationFrame(() => setTimeout(r, 0)));
  const c = document.createElement("canvas");
  const g = c.getContext("webgl2", { antialias: false, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
  if (!g) return;
  for (const i of list) {
    const doc = withTime(JSON.parse(app.fxJson(i)), 2.0);
    const k = FX_STILL_W / doc.width;
    c.width = FX_STILL_W;
    c.height = Math.round(doc.height * k);
    const f = prepareDisplayList(g, doc, { dpr: k });
    f.draw(null, null);
    const w = c.width;
    const h = c.height;
    const up = new Uint8Array(w * h * 4);
    g.readPixels(0, 0, w, h, g.RGBA, g.UNSIGNED_BYTE, up);
    f.dispose();
    // GL rows run bottom up; a picture's run top down, and opaque
    const rgba = new Uint8Array(w * h * 4);
    for (let y = 0; y < h; y++) rgba.set(up.subarray((h - 1 - y) * w * 4, (h - y) * w * 4), y * w * 4);
    for (let p = 3; p < rgba.length; p += 4) rgba[p] = 255;
    const blob = await new Promise((r) => c.toBlob(r, "image/jpeg", 0.9));
    const jpeg = blob ? await blob.arrayBuffer() : new ArrayBuffer(0);
    app.setFxStill(i, asRangerBuffer(rgba.buffer), w, h, asRangerBuffer(jpeg));
  }
  const lose = g.getExtension("WEBGL_lose_context");
  if (lose) lose.loseContext();
}
window.__renderFxStills = renderFxStills;

async function exportPdf() {
  await renderFxStills();
  window.__lastDownload = deliver(app.pdf(), exportName() + ".pdf", "application/pdf");
}
async function exportPptx() {
  await renderFxStills();
  window.__lastDownload = deliver(app.pptx(), exportName() + ".pptx",
    "application/vnd.openxmlformats-officedocument.presentationml.presentation");
}
document.getElementById("pdf").addEventListener("click", () => { exportPdf().catch(fail); });
document.getElementById("pptx").addEventListener("click", () => { exportPptx().catch(fail); });
filePick.addEventListener("change", async () => {
  const file = filePick.files && filePick.files[0];
  if (!file) return;
  docName = file.name.replace(/\.(md|markdown|txt)$/i, "") || "esitys";
  app.setSource(await file.text());
  filePick.value = "";
  dropThumbs();
  needsPaint = true;
});

// --- sharing: the markdown in the URL ------------------------------------------
// `#md=<deflate-raw, base64url>&theme=<name>`. Everything stays in the link:
// the page has no server to keep a copy. Pictures pasted into the document
// are bytes in this tab and do not travel with it.
function b64url(bytes) {
  let s = "";
  const u = new Uint8Array(bytes);
  for (let i = 0; i < u.length; i += 1) s += String.fromCharCode(u[i]);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
function unb64url(text) {
  const s = atob(text.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((text.length + 3) % 4));
  const u = new Uint8Array(s.length);
  for (let i = 0; i < s.length; i += 1) u[i] = s.charCodeAt(i);
  return u;
}
async function packText(text) {
  const stream = new Blob([new TextEncoder().encode(text)]).stream().pipeThrough(new CompressionStream("deflate-raw"));
  return b64url(await new Response(stream).arrayBuffer());
}
async function unpackText(code) {
  const stream = new Blob([unb64url(code)]).stream().pipeThrough(new DecompressionStream("deflate-raw"));
  return new TextDecoder().decode(await new Response(stream).arrayBuffer());
}
function hashParams() {
  return new URLSearchParams(location.hash.replace(/^#/, ""));
}

let toastTimer = 0;
function toast(text) {
  const el = document.getElementById("toast");
  el.textContent = text;
  el.classList.add("on");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove("on"), 3200);
}

// Two links to the same deck: one that opens straight into the presentation
// (no editor, no toolbar), one that opens the editor. The dialog shows both
// and copies the one asked for.
const shareDlg = document.getElementById("shareDlg");
async function shareLink() {
  const text = app.source();
  const code = await packText(text);
  const q = new URLSearchParams();
  q.set("md", code);
  if (themeSel.value) q.set("theme", themeSel.value);
  if ((themeSel.value || "") in editedCss) q.set("css", await packText(editedCss[themeSel.value || ""]));
  const base = location.origin + location.pathname;
  const editUrl = base + "#" + q.toString();
  q.set("mode", "show");
  const showUrl = base + "#" + q.toString();
  history.replaceState(null, "", editUrl);
  lastHash = location.hash;
  document.getElementById("shareEdit").value = editUrl;
  document.getElementById("shareShow").value = showUrl;
  const pictures = /\]\(media\//.test(text) ? " Liitetyt kuvat eivät kulje linkissä." : "";
  document.getElementById("shareNote").textContent = `${editUrl.length} merkkiä.` + pictures;
  window.__lastShare = editUrl;
  window.__lastShareShow = showUrl;
  if (shareDlg.showModal && !shareDlg.open) shareDlg.showModal();
}
shareDlg.addEventListener("click", async (ev) => {
  const id = ev.target && ev.target.dataset && ev.target.dataset.copy;
  if (ev.target === shareDlg || ev.target.id === "shareClose") {
    shareDlg.close();
    return;
  }
  if (!id) return;
  const field = document.getElementById(id);
  let copied = false;
  try {
    await navigator.clipboard.writeText(field.value);
    copied = true;
  } catch (_) {
    field.select();
    copied = document.execCommand && document.execCommand("copy");
  }
  ev.target.textContent = copied ? "Kopioitu ✓" : "Valitse ja kopioi";
  setTimeout(() => { ev.target.textContent = "Kopioi"; }, 1600);
});
shareDlg.addEventListener("close", () => focusKeys(app.focusTarget()));

// --- a shared presentation (#…&mode=show) ---------------------------------------
// The slides only: no toolbar, no editor, and no way back to one. Full screen
// is offered, not forced — a browser gives it only to a tap of the viewer's own.
let viewer = false;
let idleTimer = 0;
function enterViewer() {
  viewer = true;
  document.body.classList.add("viewer");
  app.present(true);
  handleRequests();
  wakeViewer();
}
function wakeViewer() {
  document.body.classList.remove("idle");
  clearTimeout(idleTimer);
  idleTimer = setTimeout(() => document.body.classList.add("idle"), 2500);
}
// Esc leaves full screen (the browser does that), never the presentation:
// there is no editor to go back to.
window.addEventListener("keydown", (ev) => {
  if (viewer && ev.key === "Escape") ev.stopImmediatePropagation();
}, true);
for (const ev of ["pointermove", "pointerdown", "keydown"]) {
  window.addEventListener(ev, () => { if (viewer) wakeViewer(); }, { passive: true });
}
document.getElementById("vPrev").addEventListener("click", () => { app.prev(); afterInput(); });
document.getElementById("vNext").addEventListener("click", () => { app.next(); afterInput(); });
document.getElementById("vFull").addEventListener("click", () => {
  if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
  else if (document.documentElement.requestFullscreen) document.documentElement.requestFullscreen().catch(() => {});
});

let lastHash = "";
async function openFromHash() {
  const q = hashParams();
  if (!q.has("md")) return false;
  lastHash = location.hash;
  try {
    const text = await unpackText(q.get("md"));
    if (q.has("theme")) {
      const th = q.get("theme");
      themeSel.value = th;
      app.setStyleSheet(th ? themeCss[th] || "" : "");
    }
    if (q.has("css")) {
      const css = await unpackText(q.get("css"));
      editedCss[themeSel.value || ""] = css;
      app.setStyleSheet(css);
    }
    docName = "jaettu";
    app.setSource(text);
    dropThumbs();
    needsPaint = true;
    if (q.get("mode") === "show") enterViewer();
    return true;
  } catch (e) {
    toast("Linkin sisältöä ei voitu lukea.");
    console.warn(e);
    return false;
  }
}
window.addEventListener("hashchange", () => { if (location.hash !== lastHash) openFromHash(); });

const themeCss = {};
// A theme edited in the CSS tab: kept for the session under its name (the
// original is still in themeCss), and carried by a share link.
const editedCss = {};
function useTheme(key) {
  const k = key || "";
  app.setStyleSheet(k in editedCss ? editedCss[k] : (key ? themeCss[key] || "" : ""));
  dropThumbs();
  needsPaint = true;
}

async function openSample(key) {
  const s = SAMPLES[key] || HIDDEN_SAMPLES[key];
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
  if (hint && !app.hintHasKeys() && ev.key !== "Escape" && ev.key !== "Shift" && ev.key !== "Control" && ev.key !== "Alt" && ev.key !== "Meta") closeHint();
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
    if (special === "tab" && app.focusTarget() !== "editor" && app.focusTarget() !== "chart") return;
    if (app.key(special, ev.shiftKey, mod)) ev.preventDefault();
    else if (app.focusTarget() === "editor" || app.focusTarget() === "chart") ev.preventDefault();
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
    app.pasteText(text);
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
  // a finger wobbles: it has to travel further than a mouse before a tap
  // on the stage becomes a drag
  app.setDragSlop(ev.pointerType === "mouse" ? 6 : 16);
  const where = app.pointerDown(x, y, ev.shiftKey, Math.min(clicks, 3));
  ev.preventDefault();
  if (where === "editor" || where === "sep" || where === "scrub" || where === "stage" || where === "chart" || where === "hint") {
    try { canvas.setPointerCapture(ev.pointerId); } catch (_) { /* no capture */ }
  }
  if (where === "editor" && clicks === 1) {
    setTimeout(() => {
      let h = null;
      try { h = JSON.parse(app.hintAtCaret() || "null"); } catch (_) { h = null; }
      if (h && isChartFence(h)) {
        // a chart's fence opens the chart editor, not a list of languages
        closeHint();
        app.openChartEditor(h.line);
        focusKeys("editor");
        afterInput();
      } else if (h) showHint(h);
      else closeHint();
    }, 0);
  } else if (where !== "editor" && where !== "hint") {
    closeHint();
  }
  if (where === "hint" && !app.hintIsOpen()) { hint = null; hintKey = ""; }
  if (ev.pointerType !== "mouse" && where !== "editor") {
    // A tap outside the editor must not focus the hidden text field: on a
    // phone that opens the keyboard, the page resizes under the finger and
    // the tap lands somewhere else when it is released.
    app.setFocus(app.focusTarget());
    keys.blur();
  } else {
    focusKeys(where === "editor" ? "editor" : app.focusTarget());
  }
  afterInput();
});
canvas.addEventListener("pointermove", (ev) => {
  const [x, y] = at(ev);
  app.pointerMove(x, y);
  canvas.style.cursor = app.cursorAt(x, y);
  if (ev.buttons) needsPaint = true;
  else if (ev.pointerType === "mouse") hintHover(x, y);
});

// --- hints: what a value under the pointer does, and what else it can be ------------
//
// Over `{fx=starfield}` in the Markdown or `chart-style: forge` in the theme,
// a small popover says what the value is for and offers the others: a list
// for a word with a fixed set, a colour picker, a slider with a number, the
// faces there are. A choice is written into the text as an ordinary edit
// (Ctrl+Z undoes it). A click on a value opens it too, which is how it opens
// on a touch screen.
let hint = null;
let hintTimer = 0;
let hintCloseTimer = 0;
let hintKey = "";
let pointerAt = [-1, -1];

// The ```vega-lite fence's language word: a click there opens the chart editor.
function isChartFence(h) {
  return h.name === "kieli" && /^vega-?lite$/.test(h.value);
}

function hintId(h) {
  return h ? h.tab + ":" + h.line + ":" + h.start + ":" + h.name : "";
}

// Over the card itself (or typing in it) the popover stays.
function overHint() {
  return app.hintIsOpen() && (app.hintHas(pointerAt[0], pointerAt[1]) || app.hintHasKeys());
}

function hintHover(x, y) {
  pointerAt = [x, y];
  clearTimeout(hintTimer);
  if (app.hintIsOpen() && app.hintHas(x, y)) {
    clearTimeout(hintCloseTimer);
    return;
  }
  hintTimer = setTimeout(() => {
    let h = null;
    try { h = JSON.parse(app.hintAt(x, y) || "null"); } catch (_) { h = null; }
    if (h && hintId(h) === hintKey && app.hintIsOpen()) {
      clearTimeout(hintCloseTimer);
      return;
    }
    if (h && isChartFence(h)) h = null;
    if (h) showHint(h);
    else if (!overHint()) scheduleHintClose();
  }, 380);
}

function scheduleHintClose() {
  clearTimeout(hintCloseTimer);
  hintCloseTimer = setTimeout(() => {
    if (!overHint()) closeHint();
  }, 450);
}

function closeHint() {
  clearTimeout(hintTimer);
  clearTimeout(hintCloseTimer);
  app.closeHint();
  hint = null;
  hintKey = "";
  needsPaint = true;
}

// The popover is drawn on the canvas (src/PresHintPopover.rgr); a choice in
// it is written into the text by the app, as an ordinary edit.
function showHint(h) {
  clearTimeout(hintCloseTimer);
  hint = h;
  hintKey = hintId(h);
  app.openHint(JSON.stringify(h));
  needsPaint = true;
}

function el(tag, attrs, kids) {
  const e = document.createElement(tag);
  for (const k in attrs || {}) {
    if (k === "text") e.textContent = attrs[k];
    else if (k.startsWith("on")) e.addEventListener(k.slice(2), attrs[k]);
    else e.setAttribute(k, attrs[k]);
  }
  for (const c of kids || []) e.append(c);
  return e;
}

// --- the help panel ---------------------------------------------------------------
// What the selected slide is made of — a highlight, a list, a formula — with
// how each is written and the theme's CSS rules that change how it looks,
// at the values the theme gives them now. Only what the slide has is
// listed. A property opens the theme at its line (added when the theme has
// none) with its value popover.
const helpEl = document.getElementById("help");
let helpJson = "";
let helpAt = 0;

function helpOpen() { return document.body.classList.contains("helpOpen"); }

function toggleHelp(on) {
  document.body.classList.toggle("helpOpen", on ?? !helpOpen());
  helpJson = "";
  placeHelp();
  requestAnimationFrame(resize);
  if (helpOpen()) renderHelp();
}

function placeHelp() {
  const top = canvasBar ? 48 : document.getElementById("bar").getBoundingClientRect().bottom;
  helpEl.style.top = top + "px";
  helpEl.style.height = (window.innerHeight - top) + "px";
}

function helpTick(now) {
  if (!helpOpen() || now - helpAt < 300) return;
  helpAt = now;
  renderHelp();
}

function isColour(v) { return /^(#[0-9a-f]{3,8}|rgba?\(.*\))$/i.test((v || "").trim()); }

function renderHelp() {
  const j = app.slideHelp();
  if (j === helpJson) return;
  helpJson = j;
  let feats = [];
  try { feats = JSON.parse(j); } catch (_) { feats = []; }
  const shown = app.slideShown();
  const kids = [el("div", { class: "top" }, [
    el("b", { text: `Ohje: dia ${shown + 1}` }),
    el("button", { text: "✕", title: "Sulje ohje", onclick: () => toggleHelp(false) }),
  ])];
  kids.push(el("div", { class: "intro", text: "Tällä dialla on nämä osat. Napsauta ominaisuutta muuttaaksesi sitä teeman CSS:ssä." }));
  for (const f of feats) {
    const sec = [el("h3", { text: f.title })];
    if (f.syntax) sec.push(el("div", { class: "syn" }, f.syntax.split("   ").map((x) => el("code", { text: x }))));
    if (f.doc) sec.push(el("div", { class: "doc", text: f.doc }));
    for (const r of f.rules) {
      const rule = [el("div", {}, [el("span", { class: "sel", text: r.sel + " { }" }), el("span", { class: "sd", text: r.doc })])];
      for (const p of r.props) {
        const val = p.value
          ? el("span", { class: "val" }, (isColour(p.value) ? [el("span", { class: "sw", style: "background:" + p.value })] : []).concat([p.value]))
          : p.eff
            ? el("span", { class: "val none" }, (isColour(p.eff) ? [el("span", { class: "sw", style: "background:" + p.eff })] : []).concat(["oletus " + p.eff + " – lisää"]))
            : el("span", { class: "val none", text: "ei asetettu – lisää" });
        rule.push(el("div", { class: "prop", title: p.value ? "Muokkaa teemassa" : "Lisää teemaan: " + p.name + ": " + p.def,
          onclick: () => helpEdit(r.sel, p.name, p.def) }, [
          el("span", { class: "nm", text: p.name }), val,
          ...(p.doc ? [el("span", { class: "pd", text: p.doc })] : []),
        ]));
      }
      sec.push(el("div", { class: "rule" }, rule));
    }
    for (const a of f.attrs || []) {
      sec.push(el("div", { class: "attr" }, [a.name + "=" + a.value + " ", el("span", { class: "pd", text: a.doc })]));
    }
    kids.push(el("section", {}, sec));
  }
  const top = helpEl.scrollTop;
  helpEl.replaceChildren(...kids);
  helpEl.scrollTop = top;
}

function helpEdit(sel, prop, def) {
  closeHint();
  app.helpEdit(sel, prop, def);
  afterInput();
  focusKeys("editor");
  const h = JSON.parse(app.hintAtCaret() || "null");
  if (h) showHint(h);
}

document.getElementById("helpBtn").addEventListener("click", () => toggleHelp());
window.addEventListener("resize", () => { if (helpOpen()) placeHelp(); });

function endPointer() {
  app.pointerUp();
  // a click on the stage acts on release (a press that moves is a drag),
  // so what it did to the clock is taken up here
  afterInput();
}
canvas.addEventListener("pointerup", endPointer);
canvas.addEventListener("pointercancel", endPointer);
canvas.addEventListener("wheel", (ev) => {
  const [x, y] = at(ev);
  const step = ev.deltaMode === 1 ? 18 : ev.deltaMode === 2 ? 400 : 1;
  // sideways: a trackpad swipe, a tilt wheel, or Shift with a plain wheel
  let dx = ev.deltaX;
  let dy = ev.deltaY;
  if (ev.shiftKey && dx === 0) {
    dx = dy;
    dy = 0;
  }
  if (app.wheelXY(x, y, dx * step, dy * step)) {
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
// The bar on the canvas (PresToolbar) shows the page's own selects: their
// options go over as "value<TAB>label<TAB>group" rows whenever they change.
let canvasBar = false;
let barSynced = "";
function selectRows(sel) {
  const rows = [];
  for (const o of sel.options) {
    const g = o.parentElement && o.parentElement.tagName === "OPTGROUP" ? o.parentElement.label : "";
    rows.push([o.value, o.textContent.trim(), g].join("\t"));
  }
  return rows.join("\n");
}
function syncToolbar() {
  const s = selectRows(sampleSel);
  const t = selectRows(themeSel);
  const key = s + "|" + sampleSel.value + "|" + t + "|" + themeSel.value;
  if (key === barSynced) return;
  barSynced = key;
  app.setToolbarOptions("sample", s, sampleSel.value);
  app.setToolbarOptions("theme", t, themeSel.value);
}

async function start() {
  const css = await textOf("./pres.css");
  // the chart editor's controls: the kit's theme, then the app's colours
  const kit = await textOf("./ui.css").catch(() => "");
  const chartCss = await textOf("./chart-editor.css").catch(() => "");
  app.setChartCss(kit + "\n" + chartCss);
  textOf("./hint.css").then((c) => app.setHintCss(kit + "\n" + chartCss + "\n" + c)).catch(() => {});
  if (!viewer) {
    // the bar moves onto the canvas: the HTML one stays, hidden, as what it
    // presses (its buttons and selects keep every behaviour they had)
    app.setToolbarCss(kit + "\n" + (await textOf("./toolbar.css")));
    document.body.classList.add("canvas-bar");
    canvasBar = true;
    app.useToolbar(true);
    requestAnimationFrame(resize);
  }
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
  // An emoji is drawn from the platform's emoji face; the editor measures
  // with Open Sans, which has none. Told the real width, its caret stays
  // at the end of a line that has one.
  try {
    const m = document.createElement("canvas").getContext("2d");
    m.font = "100px 'Open Sans'";
    const em = m.measureText("\u{1F600}").width / 100;
    if (em > 0.3 && em < 3) app.setMissingGlyphEm(em);
  } catch (_) { /* measured as the face says */ }
  // The PDF writer's fallback for emoji (monochrome Noto Emoji). Only the
  // writer gets it: the screen keeps the browser's own colour emoji. Loaded
  // after start-up because it is large; a PDF made before it arrives just
  // has no emoji. The dash in the name puts it in the fallback pool.
  bytesOf("./fonts/NotoEmoji-Regular.ttf")
    .then((bytes) => app.attachFont("Noto Emoji-Regular", asRangerBuffer(bytes.slice(0))))
    .catch((e) => console.warn("emoji face not loaded", e));

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
  if (!(await openFromHash())) {
    const want = q.get("sample");
    const sample = SAMPLES[want] || HIDDEN_SAMPLES[want] ? want : "talous";
    if (SAMPLES[sample]) sampleSel.value = sample;
    await openSample(sample);
  }

  hintEl.remove();
  focusKeys("editor");
  window.__pageStarted = true;
  requestAnimationFrame(frame);
}

start().catch(fail);
