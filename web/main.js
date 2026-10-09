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

import { prepareDisplayList, setFontFallback, fontSpec, textObstacles } from "./gl/evg-webgl.js";
import { createA11yMirror, pressAtCentre } from "./gl/evg-a11y.js";
import { openVfs, memoryStore, kindOf, isText, placeFor, newId } from "./vfs.js";
import { accountStorage } from "./account.js";
import { sortFiles, pastePlan, fileClipboard, CLIP_KEY } from "./fileclip.js";
import { toBase64, fromBase64, fileBytes, fileState, plainChord, clipImgHtml } from "./slideclip.js";
import { deckKey, canReturn, reopenPlan, tabLabel, readDeckTabs, keepDeckTabs, rowToKeep } from "./decktabs.js";
import { lang, LANGS, t, translateDom, chooseLang, chooseTerm, handOver } from "./i18n.js";
import { createLiveSheets } from "./sheets-live.js";
import { scaled, previewOf, render, asPicture } from "./image-adjust.js";
import { decodePicture, pictureCache, isSvg, isSmartArt, SMARTART_TYPE } from "./picture.js";
import { DeckHistory, TAB, mergeCopies, resolveMerge, lineStats } from "./versions.js";
import { showHistory, askMerge } from "./versions-ui.js";
import { isViewFrame, readyMessage, readPacket } from "./version-view.js";
import { wantsIntro, INTRO_MS } from "./brand.js";
import { embeddedAsset, embeddedScriptUrl, embeddedDeck, fileData, playerHtml, base64 } from "./player-file.js";
import { deckRows, sortRows, deckListJson, nextSort, firstDir, roomShareRows } from "./decklist.js";
import { emptyRooms, readKept, changeKept, listRooms, roomDecks, createRoom, moveDeck, deckLines, touchRoom, activeRooms, searchRooms, orderRooms, updateRoom, archiveRoom, deleteRoom, moveRoom, isBuiltIn, ONBOARDING, GENERAL, roomOf, foldersOf, createFolder, renameFolder, deleteFolder } from "./rooms.js";
import { CollabSession, loadMe, saveMe, cleanName, chatTime, editsOf } from "./collab.js";
import { planFiles, seenAfterSave } from "./sharefiles.js";
import { Meet } from "./meet.js";
import { RoomChat, unreadRooms } from "./roomchat.js";
import { RdOtDelta, RdOtClient } from "./rangerdiff.mjs";
import { VoiceRecorder, VoicePlayer, clockText } from "./recorder.js";
import { secondaryPress, pickKeyHeld } from "./press.js";
import { linkTarget, FOLLOW_MS } from "./stagelink.js";
import { stampSvg, readStamp, retraceSource, svgTarget, looksFlat } from "./trace-source.js";
import { BookGL } from "./bookgl.js";
import { createApps } from "./apps.js";
import { themePicture, picturesToDraw, fitPage } from "./themepics.js";
import { autoTurn, grabTurn, dragTurn, releaseTurn, stepTurn, turnScene, turnPages } from "./bookturn.js";

// Where the editor's addresses start: sliqtly.com serves the editor at
// /editor to signed-in people (mcp-go/editor.go), a server of one's own at
// its root. A deck in it is editAddress(id): /editor/d/{id} there (/d/: a
// document, never a shared link; firestore.rules keeps it its owner's and
// the invited editors'), /s/{id}?edit on a server of one's own. An older
// /editor/s/{id}?edit still opens it.
const EDITOR_ROOT = /^\/editor(\/|$)/.test(location.pathname) ? "/editor" : "";
const DECK_PATH = /^(?:\/editor\/[sd]|\/s)\/([A-Za-z0-9]{6,32})\/?$/;
const SHARED_PATH = /^(?:\/editor\/[sd]|\/s)\//;
const DOC_PATH = /^\/editor\/d\//;
// sliqtly.com's editor keeps nothing in IndexedDB (Tero, 2026-10-09: the
// browser's store hung the page): its decks live in the cloud, the page's
// store lasts as long as the page, and a deck goes by its cloud id
// ("cloud:<id>") in tabs, the Rooms and the deck opened next time.
const CLOUD_ONLY = !!EDITOR_ROOT;
function editAddress(id) {
  return EDITOR_ROOT ? EDITOR_ROOT + "/d/" + id : "/s/" + id + "?edit";
}
// the address asks to edit the deck it names
function editAsked() {
  return DOC_PATH.test(location.pathname) || new URLSearchParams(location.search).has("edit");
}

// What this browser keeps for the account signed in (web/account.js): on
// the editor each Google account has its own keys, so another account in
// the same browser sees none of its rooms or tabs.
// account: "" until known (accountScope), then the account's id
let account = "";
function pageStorage(which) {
  try {
    const s = which === "session" ? sessionStorage : localStorage;
    if (s) return s;
  } catch (_) { /* refused: this page only */ }
  const m = new Map();
  return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: (k) => m.delete(k), key: (i) => [...m.keys()][i] ?? null, get length() { return m.size; } };
}
let mine = accountStorage(pageStorage("local"), "");
let mineTab = accountStorage(pageStorage("session"), "");
// The account the editor keeps things for: the signed-in user's id, or
// "signed-out" (keys of their own, nobody's work). A server of one's own
// and the pages without an editor keep the keys of old ("").
async function accountScope() {
  if (!EDITOR_ROOT || viewer) return "";
  const p = await pro();
  const u = await Promise.race([p.signedIn?.() ?? null, new Promise((ok) => setTimeout(() => ok(null), 8000))]);
  return u?.uid || "signed-out";
}
function useAccount(scope) {
  account = scope;
  mine = accountStorage(pageStorage("local"), scope);
  mineTab = accountStorage(pageStorage("session"), scope);
  try { foldersOpen = new Set(JSON.parse(mine.getItem(FOLDERS_OPEN_KEY) || "[]")); } catch (_) { /* none kept */ }
  try { roomsHere = readKept(mine, ROOMS_KEY); } catch (_) { /* none kept */ }
  Object.assign(collabMe, loadMe(mine));
  // another account signed in on this page (or one where nobody was): its
  // own keys, from the start
  if (scope && !accountWatched) {
    accountWatched = true;
    window.addEventListener("sliqtly:user", () => {
      const u = window.sliqtly?.user?.();
      if (u && u.uid !== account) location.reload();
    });
  }
}
let accountWatched = false;
// One beacon per page load for the visitor counts (mcp-go/rgr/Stats.rgr): the
// page, mobile or desktop on the server's side, and the site the visitor
// came from. No cookie, nothing kept in the browser; not sent when the
// browser asks not to be tracked, nor outside sliqtly.com.
(function countVisit() {
  if (!/^(sliqtly\.com|sliqtly\.web\.app)$/.test(location.hostname)) return;
  if (navigator.globalPrivacyControl || navigator.doNotTrack === "1") return;
  const shared = SHARED_PATH.test(location.pathname);
  const p = !shared ? "editor" : editAsked() ? "edit" : "view";
  let r = "";
  try { r = document.referrer ? new URL(document.referrer).hostname : ""; } catch { /* no referrer */ }
  try { navigator.sendBeacon("/api/hit", new Blob([JSON.stringify({ p, r })], { type: "application/json" })); } catch { /* not counted */ }
})();

const canvas = document.getElementById("c");
const stageEl = document.getElementById("stage");
const keys = document.getElementById("keys");
const loadNote = document.getElementById("loadNote");
const errEl = document.getElementById("err");
const statusEl = document.getElementById("status");
const filePick = document.getElementById("filepick");
const fileAdd = document.getElementById("fileadd");
const sampleSel = document.getElementById("sample");
const themeSel = document.getElementById("theme");
const playBtn = document.getElementById("play");
// The language: a select in the bar (drawn on the canvas bar too); choosing
// one reloads the page in it.
const langSel = document.getElementById("lang");
for (const [code, name] of LANGS) langSel.add(new Option(name, code));
langSel.value = lang;
langSel.addEventListener("change", () => { if (langSel.value !== lang) chooseLang(langSel.value); });

// The app's name is the one index.html gives its bar: a page built on this
// one renames it there, and the canvas follows.
const APP_NAME = document.querySelector("#bar .brand")?.textContent.trim() || "EVG Presentation";

const FACES = [
  ["Open Sans", "OpenSans-Regular.ttf"],
  ["Open Sans-Bold", "OpenSans-Bold.ttf"],
  ["Open Sans-Italic", "OpenSans-Italic.ttf"],
  ["Open Sans-BoldItalic", "OpenSans-BoldItalic.ttf"],
  ["Noto Sans", "NotoSans-Regular.ttf"],
  ["Noto Sans-Bold", "NotoSans-Bold.ttf"],
];
const THEMES = ["aurora", "nebula", "carbon", "ember", "midnight", "white", "corporate", "editorial", "pearl", "hive", "lattice", "apex", "tide", "mist", "forge", "foundry", "site", "clinic", "care", "vital"];
// The sample decks in the interface's language: samples/<key>.md is Finnish,
// samples/<key>.en.md English (any other language gets the English ones).
const sample = (key, en, fi) => lang === "fi" ? [fi, `./samples/${key}.md`] : [en, `./samples/${key}.en.md`];
const SAMPLES = {
  // the first visit's deck, on Aurora, with pictures of its own beside it
  // (samples/welcome/…, the same for both languages)
  welcome: [...sample("welcome", "Welcome: what Sliqtly can do", "Tervetuloa: mitä Sliqtlyllä voi tehdä"), "aurora",
    ["media/bg.png", "media/logo.svg", "media/radial.xml"]],
  talous: sample("talous", "Finance: take charge of your money", "Talous: oma talous haltuun"),
  ymparisto: sample("ymparisto", "Environment: your carbon footprint", "Ympäristö: hiilijalanjälki"),
  urheilu: sample("urheilu", "Sports: a 5 km running course", "Urheilu: 5 km juoksukoulu"),
  kulttuuri: sample("kulttuuri", "Culture: decades of music", "Kulttuuri: musiikin vuosikymmenet"),
  ohjelmointi: sample("ohjelmointi", "Programming: version control", "Ohjelmointi: versionhallinta"),
  matematiikka: sample("matematiikka", "Mathematics: formulas on slides", "Matematiikka: kaavat kalvoilla"),
  vegalite: sample("vegalite", "Vega-Lite: chart types", "Vega-Lite: kaaviotyypit"),
  raportti: sample("raportti", "Report: header, footer, page numbers", "Raportti: ylä- ja alaosa, sivunumerot"),
  mallit: sample("mallit", "Layouts: steps, SWOT, timeline", "Asettelut: vaiheet, SWOT, aikajana"),
  tyonkulku: sample("tyonkulku", "Workflows: XState statecharts", "Työnkulut: XState-tilakaaviot"),
  // the newest themes and features, on Nebula
  uutta: [...sample("uutta", "What's new: themes, effects, layouts", "Uutta: teemat, efektit, asettelut"), "nebula"],
  // programs on slides (```app), with their files (samples/pelit/apps/…)
  pelit: [...sample("pelit", "Games: programs on slides", "Pelit: ohjelmat kalvoilla"), "",
    ["apps/scaffold.tsx", "apps/scaffold.tsx.css", "apps/target.tsx", "apps/target.tsx.css"]],
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
// the day it was built (scripts/build.mjs), for Help → About
const BUILT = "__BUILT__";
// The faces carry their own version (the hash of the font files), so a new
// build does not make every browser fetch the same fonts again.
const FONTS = "__FONTS__";
const fontUrl = (file) => "./fonts/" + file + (FONTS.startsWith("__") ? "" : "?v=" + FONTS);
function fresh(url) {
  if (!url.startsWith("./") || BUILD.startsWith("__")) return url;
  return url + (url.includes("?") ? "&" : "?") + "v=" + BUILD;
}

// A player file (web/player-file.js) carries the page's own files: they are
// read from it, and fetched on any other page.
async function pageFetch(url) {
  return (await embeddedAsset(url)) || (await fetch(url));
}

async function bytesOf(url) {
  const res = await pageFetch(fresh(url));
  if (!res.ok) throw new Error(url + " → " + res.status);
  return await res.arrayBuffer();
}

async function fontBytes(file) {
  const res = await pageFetch(fontUrl(file));
  if (!res.ok) throw new Error(file + " → " + res.status);
  return await res.arrayBuffer();
}

async function textOf(url) {
  const res = await pageFetch(fresh(url));
  if (!res.ok) throw new Error(url + " → " + res.status);
  return await res.text();
}

const gl = canvas.getContext("webgl2", { antialias: true, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
if (!gl) {
  loadNote.textContent = t("WebGL 2 is not available in this browser.");
  throw new Error("no WebGL 2");
}
if (typeof globalThis.PresApp !== "function") {
  loadNote.textContent = t("pres_app.js is missing. Run `npm run build`.");
  throw new Error("engine bundle not loaded");
}
// the interface's language and its word for a room, before anything is
// built in it
handOver(globalThis.PresI18n);
translateDom();
// i18n: "Basic" "Work" "Health" "Effects" "Dark" (the theme list's groups)
for (const g of document.querySelectorAll("optgroup[label]")) g.label = t(g.label);
const app = new globalThis.PresApp();
window.__app = app;
app.setAppName(APP_NAME);

// A page built on this one can add buttons of its own to the bar: an element
// in #bar with data-canvas="<variant>" is drawn on the canvas bar after Ohje
// (with its text, followed as it changes), and pressing it clicks it. Its
// data-short is the label used when the bar is too narrow for the full ones.
function syncBarExtras() {
  const rows = [...document.querySelectorAll("#bar [data-canvas]")]
    .filter((el) => el.id && !el.hidden)
    .map((el) => [el.id, el.textContent.trim().replace(/\s+/g, " "), el.dataset.canvas || "secondary", (el.dataset.short || "").replace(/\s+/g, " ")].join("\t"));
  const joined = rows.join("\n");
  if (joined === barExtras) return;
  barExtras = joined;
  app.setToolbarExtras(joined);
  needsPaint = true;
}
let barExtras = null;

function setText(el, text) {
  if (el.textContent !== text) el.textContent = text;
}

let dpr = Math.min(window.devicePixelRatio || 1, 2);
let W = 0;
let H = 0;
let needsPaint = true;
let lastRev = "";
// programs on slides (```app, web/apps.js): CErXes in workers, painted by the app
const apps = createApps({ app, repaint: () => { needsPaint = true; }, toast, t });
window.__apps = apps;

function resize() {
  const r = stageEl.getBoundingClientRect();
  W = Math.max(320, Math.floor(r.width));
  H = Math.max(240, Math.floor(r.height));
  dpr = Math.min(window.devicePixelRatio || 1, 2);
  canvas.width = Math.round(W * dpr);
  canvas.height = Math.round(H * dpr);
  app.setPageSize(W, H);
  // the presenting bar lies over the slide's foot: a diagram opened over the
  // whole slide keeps its buttons above it
  const vb = document.getElementById("viewBar");
  const vr = vb && getComputedStyle(vb).display !== "none" ? vb.getBoundingClientRect() : null;
  app.setBarTop(vr && vr.height > 0 ? vr.top - r.top : -1);
  dropThumbs();
  needsPaint = true;
}

// A drawing made on a slide (drawings/*.ink, src/PresSketch.rgr): JSON, kept
// as text and handed to the slides as a text file is.
const SKETCH_TYPE = "application/vnd.sliqtly.ink+json";

// --- pictures -------------------------------------------------------------------
const pictures = new Map();
// the decks' pictures, decoded once for every deck that has them (a switch
// back to a deck hands over the same files again)
const decodeKept = pictureCache();
async function registerPicture(path, bytes, type) {
  if (isSmartArt(type, path)) return;
  pictures.set(path, (await decodeKept(bytes, type, path)).img);
}

// A picture of the deck handed to the slides: drawn for the screen, and its
// bytes (an SVG's PNG, web/picture.js) for the PDF and PPTX writers.
async function addPicture(path, bytes, type) {
  if (isSmartArt(type, path)) {
    // the Ranger side reads it, gives it its size and draws it
    app.addImage(path, asRangerBuffer(bytes.slice(0)), type || "", 0, 0);
    return { img: null, w: 0, h: 0, bytes, type: type || "" };
  }
  const p = await decodeKept(bytes, type || "image/png", path);
  app.addImage(path, asRangerBuffer(p.bytes.slice(0)), p.type || "image/png", p.w, p.h);
  // an SVG's own text too: the PDF and the PPTX keep it a vector
  if (p.svg) app.addSvgPicture(path, p.svg, asRangerBuffer(p.svgBytes.slice(0)), asRangerBuffer(p.fallback ? p.fallback.slice(0) : new ArrayBuffer(0)));
  pictures.set(path, p.img);
  return p;
}

// An <svg> written into the Markdown (MdSvg): drawn into the store under a
// name of its own, as an SVG file is, the first time the deck shows it.
let inlineSvgRev = "";
const inlineSvgBusy = new Set();
// a drawing that did not draw is not tried again on every change
const inlineSvgFailed = new Set();
function loadInlineSvgs(rev) {
  inlineSvgRev = rev;
  let list = [];
  try { list = JSON.parse(app.inlineSvgsJson() || "[]"); } catch (_) { return; }
  const fresh = list.filter((u) => !inlineSvgBusy.has(u.path) && !inlineSvgFailed.has(u.path));
  if (!fresh.length) return;
  for (const u of fresh) inlineSvgBusy.add(u.path);
  Promise.all(fresh.map((u) => addPicture(u.path, new TextEncoder().encode(u.svg).buffer, "image/svg+xml")
    .catch((e) => {
      inlineSvgFailed.add(u.path);
      console.warn("inline svg not drawn", e);
    })))
    .finally(() => {
      for (const u of fresh) inlineSvgBusy.delete(u.path);
      // laid out again with the pictures in the store
      app.inlineSvgsAdded();
      dropThumbs();
      needsPaint = true;
    });
}

// A picture pasted or dropped on the canvas opens the image window
// (PresChartEditor's "paste" mode): the part to keep (CropCtl), and whether it
// goes on the slide or behind this slide or every slide. Until Add is pressed
// it is only registered for drawing, under a path of its own; nothing is kept.
let pasteCount = 0;
let pasting = null;
async function addPictureFile(file) {
  const type = file.type || "image/png";
  const bytes = await file.arrayBuffer();
  const [w, h] = isSvg(type, file.name) || isSmartArt(type, file.name) ? [0, 0] : await imageSize(bytes, type);
  const alt = file.name && file.name !== "image.png" ? file.name.replace(/\.[^.]+$/, "") : "image";
  if (w > 0 && h > 0) {
    dropPasting();
    pasteCount += 1;
    const preview = `/__paste/${Date.now().toString(36)}-${pasteCount}`;
    await registerPicture(preview, bytes, type);
    pasting = { bytes, type, w, h, preview };
    if (app.openPaste(preview, w, h, alt, storageNote())) {
      needsPaint = true;
      measureFlat(pasting).catch(() => {});
      return;
    }
    dropPasting();
  }
  // no size to crop by (or presenting): straight onto the slide, as before
  const rel = await keepPicture(bytes, type);
  app.insertPicture(rel, alt);
  dropThumbs();
  afterInput();
}

// A flat picture on its way in (web/trace-source.js looksFlat) is added
// vectorized unless asked otherwise: the window says so.
async function measureFlat(p) {
  const bmp = await createImageBitmap(new Blob([p.bytes], { type: p.type }));
  const k = Math.min(1, 400 / Math.max(bmp.width, bmp.height));
  const c = document.createElement("canvas");
  c.width = Math.max(1, Math.round(bmp.width * k));
  c.height = Math.max(1, Math.round(bmp.height * k));
  const g = c.getContext("2d", { willReadFrequently: true });
  g.drawImage(bmp, 0, 0, c.width, c.height);
  bmp.close();
  const flat = looksFlat(g.getImageData(0, 0, c.width, c.height).data, c.width, c.height);
  if (pasting !== p) return;
  app.pasteFlat(flat);
  needsPaint = true;
}

function dropPasting() {
  if (pasting) pictures.delete(pasting.preview);
  pasting = null;
}

// Where a picture added now is kept, as the window says it.
function signedIn() {
  return !!(window.sliqtly && window.sliqtly.user());
}
function storageNote() {
  if (signedIn()) return t("PRO: the image is saved to your cloud files with this presentation, and a copy stays in this browser.");
  return vfs && vfs.persistent
    ? t("The image is saved in this browser only (IndexedDB). Sign in with PRO to keep it in the cloud.")
    : t("This browser does not allow storage: the image is kept only while this page is open.");
}

// The picture as the window left it: the part kept cut out (the original
// bytes when nothing was cut), kept, and placed.
async function placePasted() {
  const p = pasting;
  if (!p) return;
  const plan = JSON.parse(app.pastePlan());
  dropPasting();
  let { bytes, type } = p;
  if (!plan.whole) {
    const [x, y, cw, ch] = String(plan.crop).split(",").map(Number);
    if (cw > 0 && ch > 0) {
      const bmp = await createImageBitmap(new Blob([bytes], { type }), x, y, cw, ch);
      const c = document.createElement("canvas");
      c.width = cw;
      c.height = ch;
      c.getContext("2d").drawImage(bmp, 0, 0);
      bmp.close();
      // a photo stays a JPEG; anything else (a screenshot, a GIF) a PNG
      const out = /^image\/(jpeg|webp)$/.test(type) ? type : "image/png";
      const blob = await new Promise((r) => c.toBlob(r, out, 0.92));
      if (blob) {
        bytes = await blob.arrayBuffer();
        type = blob.type || out;
      }
    }
  }
  const rel = await keepPicture(bytes, type);
  app.placePicture(rel, plan.alt || "image", plan.to);
  dropThumbs();
  afterInput();
  if (plan.trace && !isSvg(type, rel)) await openTraceEditor(rel);
}

// A picture of the files tab, clicked: the image editor (PresChartEditor's
// "adjust" mode) crops it and changes its light and colours. The preview is
// the picture scaled down, drawn again as the sliders move; Save writes the
// whole picture back over its file, so every slide that shows it changes.
let adjusting = null;
async function openImageEditor(path) {
  const f = (await docFiles()).find((x) => x.path === path);
  if (!f || !(f.data instanceof Blob)) { toast(t("This file cannot be opened as an image.")); return; }
  const blob = f.data.type ? f.data : new Blob([f.data], { type: f.type || "image/png" });
  let bmp;
  try {
    bmp = await createImageBitmap(blob);
  } catch (_) {
    toast(t("This file cannot be opened as an image."));
    return;
  }
  const w = bmp.width;
  const h = bmp.height;
  const base = scaled(bmp, 1200);
  bmp.close();
  dropAdjusting();
  pasteCount += 1;
  const preview = `/__adjust/${Date.now().toString(36)}-${pasteCount}`;
  const first = previewOf(base, null);
  pictures.set(preview, first);
  // the slides show the preview while the editor is open; Cancel puts the
  // picture back
  const original = pictures.get("/" + path);
  adjusting = { path, blob, w, h, base, preview, original, queued: false };
  pictures.set("/" + path, first);
  if (!app.openAdjust(preview, path, w, h)) dropAdjusting();
  needsPaint = true;
}

function dropAdjusting() {
  const a = adjusting;
  if (!a) return;
  pictures.delete(a.preview);
  if (a.original) pictures.set("/" + a.path, a.original);
  adjusting = null;
  needsPaint = true;
}

// The preview again, at most once a frame however fast the sliders move.
function adjustPreview() {
  const a = adjusting;
  if (!a || a.queued) return;
  a.queued = true;
  requestAnimationFrame(() => {
    a.queued = false;
    if (adjusting !== a) return;
    const shown = previewOf(a.base, JSON.parse(app.adjustPlan()));
    pictures.set(a.preview, shown);
    pictures.set("/" + a.path, shown);
    needsPaint = true;
  });
}

async function saveAdjusted() {
  const a = adjusting;
  if (!a) return;
  const plan = JSON.parse(app.adjustPlan());
  // the slides keep the preview until the saved picture replaces it
  const original = a.original;
  a.original = null;
  dropAdjusting();
  const restore = () => { if (original) pictures.set("/" + a.path, original); needsPaint = true; };
  const crop = cropOf(plan);
  const neutral = !plan.bright && !plan.contrast && !plan.sat && !plan.temp && !plan.tint;
  if (!crop && neutral) { restore(); return; }
  const out = await render(a.blob, crop, plan).catch(() => null);
  if (!out) { restore(); toast(t("The image could not be saved.")); return; }
  app.addImage("/" + a.path, asRangerBuffer(out.bytes.slice(0)), out.type, out.w, out.h);
  await registerPicture("/" + a.path, out.bytes, out.type);
  // the version history keeps the picture as it first came and the edits
  // made to it (versions.js): an edit costs a few bytes there, not a picture
  const was = (await docFiles()).find((x) => x.path === a.path);
  const orig = was?.orig?.data
    ? { ...was.orig, plans: [...(was.orig.plans || []), plan] }
    : { data: a.blob, type: a.blob.type || was?.type || "", plans: [plan], stamp: Date.now() };
  await keepFile({ path: a.path, type: out.type, size: out.bytes.byteLength, data: new Blob([out.bytes], { type: out.type }), orig });
  dropThumbs();
  needsPaint = true;
  toast(t("Image saved: ") + a.path);
}

function cropOf(plan) {
  if (plan.whole) return null;
  const [x, y, cw, ch] = String(plan.crop).split(",").map(Number);
  return cw > 0 && ch > 0 ? [x, y, cw, ch] : null;
}

// A picture of the deck traced into an SVG (PresChartEditor's "trace" mode,
// opened from Edit image's "Vectorize…"). The tracer is src/PresTrace.rgr —
// lib/evg's EvgBitmapTracer — in its own bundle, run in a worker
// (trace-worker.js) so the page keeps drawing while it works. While the
// window is open the slides show the latest trace; Save keeps it as an SVG
// beside the picture and, when asked, points the picture's uses at it.
let tracing = null;
let traceWorker = null;
let traceSeq = 0;
const TRACE_MOST = 2000;

function traceWorkerOf() {
  if (!traceWorker) {
    traceWorker = new Worker("./trace-worker.js?v=" + BUILD, { name: BUILD });
    traceWorker.onmessage = (e) => traceResult(e.data).catch(fail);
    traceWorker.onerror = (e) => {
      traceWorker = null;
      if (tracing) app.traceDone("", "", t("The vectorizer did not load: ") + (e.message || ""));
      needsPaint = true;
    };
  }
  return traceWorker;
}

// An SVG's text (its first `most` bytes: the stamp is at its start).
async function fileText(f, most) {
  const blob = f.data instanceof Blob ? f.data : new Blob([f.data || ""]);
  return blob.slice(0, most || blob.size).text();
}

// Edit on an SVG of the files: the vectorizer again, on the picture it was
// traced from (web/trace-source.js) with the settings it was traced with;
// Save writes this SVG.
async function retraceSvg(svgPath) {
  const files = await docFiles();
  const f = files.find((x) => x.path === svgPath);
  if (!f) { toast(t("This file cannot be opened as an image.")); return; }
  const stamp = readStamp(await fileText(f, 8192));
  const source = retraceSource(svgPath, stamp, files.map((x) => x.path));
  if (!source) {
    toast(t("The picture this SVG was made from is no longer in the files, so it cannot be vectorized again."));
    return;
  }
  await openTraceEditor(source, { target: svgPath, settings: stamp && stamp.source === source ? stamp.settings : "" });
}

// Whether each SVG of the files can be traced again ("retrace" on its row):
// its stamp, read once per file and size.
const stampSeen = new Map();
async function retraceable(files) {
  const paths = files.map((x) => x.path);
  const out = new Set();
  for (const f of files) {
    if (!isSvg(f.type || "", f.path)) continue;
    const key = f.path + "\t" + f.size;
    if (!stampSeen.has(key)) stampSeen.set(key, await fileText(f, 8192).then(readStamp).catch(() => null));
    if (retraceSource(f.path, stampSeen.get(key), paths)) out.add(f.path);
  }
  return out;
}

async function openTraceEditor(path, opts = {}) {
  const f = (await docFiles()).find((x) => x.path === path);
  if (!f || !(f.data instanceof Blob)) { toast(t("This file cannot be opened as an image.")); return; }
  const blob = f.data.type ? f.data : new Blob([f.data], { type: f.type || "image/png" });
  if (isSvg(blob.type, path)) { toast(t("This picture is already a vector image.")); return; }
  let bmp;
  try {
    bmp = await createImageBitmap(blob);
  } catch (_) {
    toast(t("This file cannot be opened as an image."));
    return;
  }
  const w = bmp.width;
  const h = bmp.height;
  // the pixels the tracer gets: at most TRACE_MOST on the longer side (its
  // own maxSide scales them further), drawn by the browser
  const k = Math.min(1, TRACE_MOST / Math.max(w, h));
  const c = document.createElement("canvas");
  c.width = Math.max(1, Math.round(w * k));
  c.height = Math.max(1, Math.round(h * k));
  const g = c.getContext("2d", { willReadFrequently: true });
  g.drawImage(bmp, 0, 0, c.width, c.height);
  bmp.close();
  const rgba = g.getImageData(0, 0, c.width, c.height).data;
  dropTracing();
  traceSeq += 1;
  const orig = `/__trace/${Date.now().toString(36)}-${traceSeq}`;
  pictures.set(orig, asPicture(c));
  // the slides show the trace where they show the picture, or the SVG
  // being made again
  const target = opts.target || "";
  const shown = target || path;
  tracing = { path, target, shown, w, h, rgba, rw: c.width, rh: c.height, orig, preview: null, svg: null, run: 0, original: pictures.get("/" + shown), bytes: blob.size };
  if (!app.openTrace(orig, path, w, h)) { dropTracing(); return; }
  if (target) app.traceAgain(target, opts.settings || "");
  needsPaint = true;
}

function runTrace() {
  const tr = tracing;
  if (!tr) return;
  tr.run += 1;
  const copy = tr.rgba.slice().buffer;
  traceWorkerOf().postMessage({ seq: tr.run, rgba: copy, w: tr.rw, h: tr.rh, settings: app.traceSettings() }, [copy]);
}

function kb(n) {
  return n < 1024 * 1024 ? Math.max(1, Math.round(n / 1024)) + " KB" : (n / 1048576).toFixed(1) + " MB";
}

async function traceResult(r) {
  const tr = tracing;
  // an answer for a window since closed, or for settings since changed
  if (!tr || r.seq !== tr.run) return;
  if (r.err || !r.svg) {
    app.traceDone("", "", r.err || t("Nothing came out of the picture."));
    needsPaint = true;
    return;
  }
  const bytes = new TextEncoder().encode(r.svg);
  const p = await decodePicture(bytes.buffer, "image/svg+xml", tr.path + ".svg");
  if (tracing !== tr || r.seq !== tr.run) return;
  if (!p.img) {
    app.traceDone("", "", t("The SVG could not be drawn."));
    needsPaint = true;
    return;
  }
  tr.svg = r.svg;
  if (tr.preview) pictures.delete(tr.preview);
  tr.preview = `${tr.orig}-${r.seq}`;
  pictures.set(tr.preview, p.img);
  pictures.set("/" + tr.shown, p.img);
  let info = t("{n} colors, {size} SVG (the picture {was})").replace("{n}", String(r.layers)).replace("{size}", kb(bytes.byteLength)).replace("{was}", kb(tr.bytes));
  if (r.tracedW && (r.tracedW !== tr.w || r.tracedH !== tr.h)) info += t(", traced at ") + r.tracedW + "×" + r.tracedH;
  app.traceDone(tr.preview, info + ".", "");
  needsPaint = true;
}

function dropTracing() {
  const tr = tracing;
  if (!tr) return;
  pictures.delete(tr.orig);
  if (tr.preview) pictures.delete(tr.preview);
  if (tr.original) pictures.set("/" + tr.shown, tr.original);
  tracing = null;
  needsPaint = true;
}

window.__traceState = () => (tracing ? { path: tracing.path, run: tracing.run, svg: tracing.svg ? tracing.svg.length : 0 } : null);

// A trace saved: over the SVG it was opened from (Edit on an SVG), else as
// the picture's SVG — when that already exists, asked first whether to
// replace it or keep both ("confirm:tracesave", ":alt" a new name).
let traceAsk = null;

async function saveTraced() {
  const tr = tracing;
  if (!tr) return;
  const plan = JSON.parse(app.tracePlan());
  const svg = tr.svg;
  const settings = app.traceSettings();
  dropTracing();
  if (!svg) { toast(t("Nothing was saved: the picture had not been vectorized yet.")); return; }
  const job = { path: tr.path, svg: stampSvg(svg, tr.path, settings), replace: plan.replace };
  if (tr.target) {
    await writeTraced(job, tr.target, false);
    return;
  }
  const where = svgTarget(tr.path, (await docFiles()).map((x) => x.path));
  if (!where.existing) {
    await writeTraced(job, where.fresh, plan.replace);
    return;
  }
  traceAsk = { job, where };
  const name = where.existing.split("/").pop();
  app.openChoice("tracesave", t("Replace the earlier SVG?"),
    t("{file} is already in the files. Replace it with this one, or keep both?").replace("{file}", name),
    t("Replace"), t("Keep both"));
}

async function answerTraceSave(replace) {
  const ask = traceAsk;
  traceAsk = null;
  if (!ask) return;
  const target = replace ? ask.where.existing : ask.where.fresh;
  await writeTraced(ask.job, target, ask.job.replace);
}

async function writeTraced(job, target, swap) {
  const bytes = new TextEncoder().encode(job.svg);
  await addPicture("/" + target, bytes.buffer.slice(0), "image/svg+xml");
  await keepFile({ path: target, type: "image/svg+xml", size: bytes.byteLength, data: new Blob([bytes], { type: "image/svg+xml" }) });
  const n = swap ? app.swapPictureRefs(job.path, target) : 0;
  dropThumbs();
  refreshFiles().catch(() => {});
  afterInput();
  needsPaint = true;
  const said = n === 1 ? t("Saved {file}, used where the picture was.")
    : n > 1 ? t("Saved {file}, used in {n} places instead of the picture.")
    : t("Saved {file} beside the picture.");
  toast(said.replace("{file}", target).replace("{n}", String(n)));
}

// A picture made again from its original and the edits made to it, one
// after another as they were made (a version restored).
async function renderPlans(orig, plans, type) {
  let blob = orig;
  for (const plan of plans) {
    const out = await render(blob, cropOf(plan), plan);
    blob = new Blob([out.bytes], { type: out.type });
  }
  return { blob, type: blob.type || type };
}

// A new picture of the deck: registered for the slides and kept in this
// browser; signed in to PRO, the deck's cloud save takes it with the deck.
async function keepPicture(bytes, type) {
  const ext = (type.split("/")[1] || "png").replace("jpeg", "jpg").replace("svg+xml", "svg");
  pasteCount += 1;
  const rel = `media/liitetty-${Date.now().toString(36)}-${pasteCount}.${ext}`;
  await addPicture("/" + rel, bytes, type);
  const data = new Blob([bytes], { type });
  await keepFile({ path: rel, type, size: bytes.byteLength, data });
  return rel;
}

async function imageSize(bytes, type) {
  try {
    const bmp = await createImageBitmap(new Blob([bytes], { type }));
    const size = [bmp.width, bmp.height];
    bmp.close();
    return size;
  } catch (_) {
    return [0, 0];
  }
}

// --- the documents and their files ---------------------------------------------
// Every presentation is a record in this browser (web/vfs.js) with its files
// beside it: pictures under media/, the files its charts read under data/,
// chart specs kept as files under charts/. A deck is stored the first time it
// is changed (an opened sample nobody touched is not); from then on every
// change is saved a moment after it is made. The files of a deck not stored
// yet wait in `pending` and go in with it.
let vfs = null;
const doc = { id: newId(), persisted: false, loading: false, created: Date.now(), openedText: "", openedCss: null };
// PRO: the share the deck lives in (cloud), its text as last written or read
// there (cloudMd), the files as sent (cloudStamps: path → stamp), and what
// was last sent (cloudSig), and the CSS and theme as last written or read
// there (cloudCss, cloudTheme). cloudHalt: deleted, nothing more is sent.
// cloudFiles: the paths the share is known to have, kept with the deck in
// this browser (null: not known, as for a deck kept before it was), so a
// file this browser has and the share never got is told from one removed
// there (openOwnCloudNow). cloudSeen: path → the version there this
// browser's copy is (web/sharefiles.js), so a file written again elsewhere
// is taken.
Object.assign(doc, { cloud: null, cloudMd: null, cloudCss: null, cloudTheme: null, cloudStamps: new Map(), cloudSeen: new Map(), cloudFiles: null, cloudSig: "", cloudHalt: false });
const pending = new Map();
// A shared presentation opened to read: its files, which nothing saves
const readFiles = new Map();
let savedText = null;
// the editor's change count (app.mdVersion) when its text was last found
// equal to savedText: the 1.5 s check reads the whole text only when it
// moved. -1 whenever savedText is set from elsewhere.
let savedVersion = -1;
let savedCss = null;
let savedTheme = null;
let saving = null;

// The open presentations' tabs (web/decktabs.js, PresApp's deck tabs).
// shownKey: the tab of the deck shown, as it was keyed when it was shown
let shownKey = null;
// the tab whose deck is being opened: the deck it opens may be keyed
// otherwise (a kept deck opened from the cloud, where it is newer), and the
// tab then takes that key where it stands
let openingKey = null;

// A deck being opened: from beginDoc until shownDoc puts its text in the
// editor, the editor still shows the deck before it, so nothing is saved or
// sent (the old text would be written under the new deck's id: a deck
// overwritten by the one opened before it).
function beginDoc(text) {
  // the deck left: its tab goes when nothing could open it again
  if (shownKey && !canReturn(doc)) app.deckTabClose(shownKey);
  shownKey = null;
  // its theme and files are not laid out on the deck before (shownDoc lays
  // out this one)
  app.beginOpen();
  // the deck before's pictures go with it: one this deck names and does not
  // have is not drawn from that one's as if it were here (a copy whose
  // files did not come looked whole that way)
  dropPasting();
  pictures.clear();
  app.clearImages();
  apps.reset();
  doc.loading = true;
  doc.id = newId();
  doc.persisted = false;
  doc.src = null;
  doc.created = Date.now();
  doc.openedText = text;
  doc.openedCss = null;
  Object.assign(doc, { cloud: null, cloudMd: null, cloudCss: null, cloudTheme: null, cloudStamps: new Map(), cloudSeen: new Map(), cloudFiles: null, cloudSig: "", cloudHalt: false });
  versions = null;
  filesAtCommit = null;
  pending.clear();
  readFiles.clear();
  savedText = null;
  savedVersion = -1;
  savedCss = null;
  for (const k of Object.keys(editedCss)) delete editedCss[k];
  chartFiles.clear();
  chartFilesRev = -1;
  liveFromShare = false;
  proNow();
  liveNoted = false;
  liveCopies.clear();
  copyNoted = false;
  privateNoted = false;
  app.clearChartData();
  app.reviewLoad("", false);
  showLiveButton();
  if (app.openFilePath()) app.closeFile();
}

// quiet: no tab for it (the empty deck shown while a deleted one's
// successor is found)
// the Rooms panel's state (its requests: roomsRequest, below)
const ROOMS_KEY = "sliqtly.rooms";
let roomShown = "";
// the room whose "+ Add new presentation" opened File → New's window
let roomForNew = "";
let roomChatOne = null;
// the folder whose window is open ({ room, id }, id "" for a new one), and
// the folders shown open in the panel ("<room>/<folder>", this browser's)
let folderFor = null;
const FOLDERS_OPEN_KEY = "sliqtly.openFolders";
let foldersOpen = new Set();
function keepFoldersOpen() {
  try { mine.setItem(FOLDERS_OPEN_KEY, JSON.stringify([...foldersOpen])); } catch (_) { /* this page only */ }
}
let roomsHere = emptyRooms();
function shownDoc(text, quiet = false) {
  app.setSource(text);
  doc.loading = false;
  if (!viewer && !quiet) showDeckTab();
  // a room's chat over the work area steps aside for the presentation opened
  if (roomChatOne) roomChatOne.close();
  loadReview(false).catch((e) => console.warn("review comments not read", e));
  loadRecording().catch((e) => console.warn("recording not read", e));
  // the Rooms panel marks the presentation now open
  if (!viewer && roomShown) roomsRequest("room:list").catch(() => {});
}

// The deck opened next time when nothing else is asked: its id here, its
// cloud's on sliqtly.com's editor (nothing kept here past the page).
function lastKey() {
  return CLOUD_ONLY && doc.cloud ? "cloud:" + doc.cloud : doc.id;
}

// --- the open presentations' tabs (web/decktabs.js) ------------------------------
function keepTabs() {
  if (!viewer) keepDeckTabs(mineTab, rowToKeep(app.deckTabsState(), shownKey && !canReturn(doc) ? shownKey : ""));
}
function showDeckTab() {
  shownKey = deckKey(doc, CLOUD_ONLY);
  if (openingKey && openingKey !== shownKey) app.deckTabRename(openingKey, shownKey, tabLabel(exportName()));
  openingKey = null;
  app.deckTabOpen(shownKey, tabLabel(exportName()));
  keepTabs();
}
// after a save: its name may have changed, and a deck kept for the first
// time is its id from now on
function updateDeckTab() {
  if (viewer || !shownKey) return;
  const k = deckKey(doc, CLOUD_ONLY);
  app.deckTabRename(shownKey, k, tabLabel(exportName()));
  shownKey = k;
  keepTabs();
}
// The deck behind a tab, opened: false when it is not there any more.
async function openDeckKey(key) {
  openingKey = key;
  try {
    const p = reopenPlan(key);
    if (p.kind === "sample") {
      if (!(SAMPLES[p.arg] || HIDDEN_SAMPLES[p.arg])) return false;
      await openSample(p.arg);
      return true;
    }
    if (p.kind === "cloud") return await openOwnCloud(p.arg);
    const d = vfs ? await vfs.getDoc(p.arg) : null;
    if (!d) return false;
    // a PRO deck: from its share, which has the latest (as at the start)
    if (d.cloud && (await openOwnCloud(d.cloud).catch(() => false))) return true;
    return await openDoc(p.arg);
  } finally {
    openingKey = null;
  }
}
// A tab pressed or swiped to, or the one in front after a close.
async function switchDeck(key) {
  if (!key || key === shownKey) return;
  if (makingDeck) await makingDeck;
  const ok = await loadingScreen(() => openDeckKey(key).catch((e) => { console.warn(e); return false; }));
  if (!ok) {
    toast(t("This presentation is no longer here."));
    app.deckTabClose(key);
    if (shownKey) app.deckTabOpen(shownKey, tabLabel(exportName()));
    keepTabs();
  }
  needsPaint = true;
}

function bare(path) {
  return String(path).replace(/^\.?\//, "");
}

async function keepFile(rec) {
  const file = { doc: doc.id, updated: Date.now(), ...rec, path: bare(rec.path) };
  if (doc.persisted && vfs) await vfs.putFile(file);
  else pending.set(file.path, file);
  refreshFiles();
  cloudSoon();
}

// --- live spreadsheets (EVGSheets) -------------------------------------------------
// Where EVGSheets is served from: beside this page when the build put it
// there (sheets/), or its own site. See web/sheets-live.js.
const SHEETS_BASE = "__SHEETS_BASE__";
const XLSX_MIME = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet";

async function readDocFile(path) {
  const f = (await docFiles()).find((x) => x.path === bare(path));
  if (!f) return null;
  return typeof f.data === "string" ? new Blob([f.data]) : f.data;
}

// An edited workbook back into the deck: the .xlsx itself, and the CSV of
// each sheet, which the stills, tables and charts read.
// An edited workbook back into the deck. Only the .xlsx is kept; its sheets'
// CSVs (stills, tables, charts) are handed over in memory. A live sheet over a
// plain CSV writes that CSV back instead.
async function saveWorkbook(file, raw, sheetName) {
  const path = bare(file);
  const PresData = await loadPresData();
  const got = workbookSheets(/\.xlsx$/i.test(path) ? path : path.replace(/\.[^.\/]+$/, ".xlsx"), PresData, raw);
  if (got.error) {
    toast(t("Could not read the workbook: ") + got.error);
    return;
  }
  if (/\.xlsx$/i.test(path)) {
    await keepFile({ path, type: XLSX_MIME, size: raw.byteLength, data: new Blob([raw], { type: XLSX_MIME }) });
    for (const sh of got.sheets) {
      for (const n of sh.names) {
        chartFiles.set(n, Promise.resolve(sh.csv));
        app.setChartData(n, sh.csv);
      }
    }
  } else if (got.sheets.length) {
    const csv = got.sheets[0].csv;
    await keepFile({ path, type: "text/csv", size: csv.length, data: csv });
    chartFiles.set(path, Promise.resolve(csv));
    app.setChartData(path, csv);
  }
  await saveDoc(true);
  dropThumbs();
  needsPaint = true;
  toast(t("Saved ") + path.split("/").pop());
}

// A workbook is kept as itself. The CSV of each sheet — what tables, charts
// and a live sheet's still read — is derived from it when it is needed and
// never stored: `data/<book>-<Sheet>.csv` names a sheet of `data/<book>.xlsx`,
// and a one-sheet book's sheet is `data/<book>.csv` as well. `path` is the
// name the import shows; `names` every name the sheet answers to — a
// ```sheet fence names its sheet (`sheet: Kulut` → budjetti-Kulut.csv)
// however many the book has (PresTable.sheetCsv, mcp-go Files.tables).
function workbookSheets(xlsxPath, PresData, raw) {
  const r = JSON.parse(PresData.xlsxSheets(asRangerBuffer(raw.slice(0))));
  if (r.error) return { error: r.error, sheets: [] };
  const used = r.sheets.filter((sh) => sh.csv.replace(/[,\s]/g, "") !== "");
  const base = bare(xlsxPath).replace(/\.xlsx$/i, "");
  return {
    error: "",
    sheets: used.map((sh) => {
      const csv = tidyCsv(sh.csv);
      const named = `${base}-${sh.name.replace(/[\\/:*?"<>|\s]+/g, "-")}.csv`;
      const path = used.length > 1 ? named : base + ".csv";
      return { name: sh.name, path, names: path === named ? [path] : [path, named], csv, text: csv };
    }),
  };
}

// a kept workbook (path @ when it was saved) → Promise<Map(csv path → csv)>
const derived = new Map();
function sheetsOfWorkbook(f) {
  const key = f.path + "@" + (f.updated || 0);
  if (!derived.has(key)) {
    derived.set(key, (async () => {
      const PresData = await loadPresData();
      const blob = typeof f.data === "string" ? new Blob([f.data]) : f.data;
      const out = new Map();
      for (const sh of workbookSheets(f.path, PresData, await blob.arrayBuffer()).sheets) for (const n of sh.names) out.set(n, sh.csv);
      return out;
    })().catch(() => new Map()));
  }
  return derived.get(key);
}

/** A sheet's CSV, derived from the workbook it names, or null. */
async function derivedCsv(url) {
  const want = bare(url);
  for (const f of await docFiles()) {
    if (!/\.xlsx$/i.test(f.path)) continue;
    const base = f.path.replace(/\.xlsx$/i, "");
    if (want !== base + ".csv" && !want.startsWith(base + "-")) continue;
    const got = (await sheetsOfWorkbook(f)).get(want);
    if (got != null) return got;
  }
  return null;
}

const liveSheets = createLiveSheets({
  stageEl,
  canvas,
  keys,
  base: SHEETS_BASE,
  readFile: readDocFile,
  saveWorkbook,
  t,
  toast: (m) => toast(m),
  onChange: () => { needsPaint = true; },
});
window.__liveSheets = liveSheets;
// for check:web: a workbook saved as the spreadsheet editor saves it
window.__saveWorkbook = (path, raw) => saveWorkbook(path, raw);
// for check:web: what the document keeps as files
window.__docFiles = () => docFiles().then((fs) => fs.map((f) => f.path));
window.__docState = () => ({ id: doc.id, cloud: doc.cloud });
window.__allDocs = () => allDocs(true);
window.__docFile = (path) => readDocFile(path);

async function docFiles() {
  const out = new Map();
  if (doc.persisted && vfs) for (const f of await vfs.listFiles(doc.id)) out.set(f.path, f);
  for (const [k, f] of readFiles) out.set(k, f);
  for (const [k, f] of pending) out.set(k, f);
  return [...out.values()];
}

// One at a time: a save, and a look at what another tab saved (takeLocal).
let docQueue = Promise.resolve();
function exclusive(fn) {
  const run = docQueue.then(fn, fn);
  docQueue = run.catch(() => {});
  return run;
}

async function saveDoc(force) {
  if (!vfs || viewer || merging) return;
  return exclusive(() => saveDocNow(force));
}

async function saveDocNow(force) {
  if (merging || doc.loading) return;
  const key = themeSel.value || "";
  const css = key in editedCss ? editedCss[key] : null;
  const version = app.mdVersion();
  const same = css === savedCss && key === savedTheme;
  if (same && version === savedVersion && !force) return;
  const md = app.source();
  if (md === savedText && same && !force) {
    savedVersion = version;
    return;
  }
  // a deck as it was opened is not kept until someone changes it (a shared
  // deck opens with its own CSS: that is as opened too), nor an empty one
  if (!doc.persisted && !force && ((md === doc.openedText && css === doc.openedCss) || !md.trim())) return;
  let renamedNow = false;
  saving = (async () => {
    const cur = doc.persisted ? await vfs.getDoc(doc.id) : null;
    // another tab of this browser saved this deck since this one read it:
    // nothing is written over, the two are put together first
    if (cur && !collabOn() && changedElsewhere(cur)) return "merge";
    // its name changed (a heading edited): the room's list shows it
    if (cur && cur.name !== exportName()) renamedNow = true;
    await vfs.putDoc({
      ...(cur || {}), id: doc.id, name: exportName(), md, theme: key, css, created: doc.created, updated: Date.now(), by: TAB,
      cloud: doc.cloud, cloudMd: doc.cloudMd, cloudCss: doc.cloudCss, cloudTheme: doc.cloudTheme, cloudFiles: doc.cloudFiles,
    });
    if (!doc.persisted) {
      doc.persisted = true;
      for (const f of pending.values()) await vfs.putFile({ ...f, doc: doc.id });
      pending.clear();
      plainAddress();
    }
    updateDeckTab();
    savedText = md;
    savedVersion = version;
    savedCss = css;
    savedTheme = key;
    try { mine.setItem("evgp.doc", lastKey()); } catch (_) { /* the next start opens a sample */ }
  })();
  let r;
  try { r = await saving; } finally { saving = null; }
  if (r === "merge") return takeLocal();
  if (renamedNow) roomDecksAgain();
  refreshFiles();
  cloudSoon();
  tellTabs();
}

// Before another deck is opened in its place: this one kept, and a PRO
// deck's last changes written to its share (they would wait for the next
// visit of this deck otherwise). The cloud gets a while, not forever.
async function leaveDoc() {
  document.getElementById("welcomeCard")?.remove();
  document.getElementById("versions")?.remove();
  await saveDoc();
  await commitVersion("@auto").catch((e) => console.warn("no version kept", e));
  if (!doc.cloudHalt && cloudReady() && (cloudTimer || cloudBusy)) {
    clearTimeout(cloudTimer);
    cloudTimer = 0;
    const wait = new Promise((ok) => setTimeout(ok, 8000));
    await Promise.race([wait, cloudSync().catch(cloudTrouble)]);
  }
}

// File → New presentation, made: { name, theme, data } from the window.
// Kept at once under an id of its own, so the address names this deck and
// not the one before (a PRO deck gets its own share). `data` "sample" adds
// a slide with a chart; "file" was the file picker, opened by the press.
let makingDeck = null;
async function newDeck(plan) {
  await leaveDoc();
  const name = String(plan.name || "").replace(/\s+/g, " ").trim() || t("New presentation");
  let text = "# " + name + "\n\n" + t("Write here.") + "\n";
  if (plan.data === "sample") text += "\n" + sampleChartSlide();
  beginDoc(text);
  docName = name;
  if (plan.theme != null && [...themeSel.options].some((o) => o.value === plan.theme)) themeSel.value = plan.theme;
  useTheme(themeSel.value);
  shownDoc(text);
  app.showTab("md");
  dropThumbs();
  await saveDoc(true);
  await commitVersion("@created").catch((e) => console.warn("no version kept", e));
  if (cloudReady()) await cloudSync().catch(cloudTrouble);
  refreshFiles();
  needsPaint = true;
  // made by a room's "+ Add new presentation": it goes there
  const room = roomForNew;
  roomForNew = "";
  if (room) {
    const id = await currentRoomId();
    if (id) await roomsRequest("room:moveid:" + room + ":" + id);
  }
}
// File → Duplicate, its name asked first: a new deck from this one, its
// Markdown, theme CSS and files copied, named `asked` (empty: "<name>
// (copy)"). This one is saved first; the copy is kept at once under an id of
// its own (a PRO deck gets its own share).
// Rooms beside the rail (ADR 0001): on a server of one's own its rooms
// (POST /api/rooms/<op>); elsewhere this browser's (web/rooms.js), kept in
// localStorage. The open room's presentations are listed under it, at most
// fifteen, "… Show all" opening the rest in the presentations window.
// A change to the rooms: only what it changes is written, over what
// localStorage holds now (web/rooms.js changeKept: other tabs write too)
function keepRooms(fn) {
  try { roomsHere = changeKept(mine, ROOMS_KEY, fn); } catch (_) { roomsHere = fn(roomsHere); /* this page only */ }
}
// another tab changed the rooms: this one shows the same
window.addEventListener("storage", (e) => {
  const k = e.key === null ? null : mine.own(e.key);
  if (e.key !== null && (k === null || (k !== ROOMS_KEY && !k.startsWith(ROOMS_KEY + "/")))) return;
  try { roomsHere = readKept(mine, ROOMS_KEY); } catch (_) { return; }
  if (!viewer && roomShown) roomsRequest("room:list").catch(() => {});
});
async function roomsCall(op, args) {
  const res = await fetch("/api/rooms/" + op, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(args || {}) });
  const out = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(out.error || res.status);
  return out;
}
const clean = (s) => String(s || "").replace(/[\t\n\r]+/g, " ");
function sampleRows() {
  return [...sampleSel.options].filter((o) => o.value).map((o) => ({ key: o.value, name: o.textContent.trim(), current: doc.src === "sample:" + o.value }));
}
// The rooms in the panel's order (web/rooms.js orderRooms: newest first,
// then as dragged): [{ room_id, title, description, archived, presentations }]
async function roomsList({ archived = false } = {}) {
  if (ownServer()) return orderRooms((await roomsCall("list_rooms", { archived })).rooms || [], roomsHere.order);
  return listRooms(roomsHere, await allDocs(), sampleRows(), { archived });
}
// A deck's tab key (web/decktabs.js) as the room lists name it: a kept
// deck's id, on a server of one's own "cloud:<share>"; "" when not kept.
async function tabDeckId(key) {
  if (key.startsWith("sample:") || key.startsWith("cloud:")) return key;
  if (!ownServer()) return key;
  const d = (await vfs.listDocs()).find((x) => x.id === key);
  return d?.cloud ? "cloud:" + d.cloud : "";
}
// A copy of a presentation in `room`: it is opened and duplicated (File →
// Duplicate's way), and the copy, now open, goes to the room.
async function copyDeckTo(room, deck) {
  if (deck.startsWith("sample:")) await openSample(deck.slice(7));
  else await fileRequest("doc:" + deck);
  makingDeck = duplicateDeck("");
  try { await makingDeck; } finally { makingDeck = null; }
  const id = await currentRoomId();
  if (id) await roomsRequest("room:moveid:" + room + ":" + id);
}
// A room's presentations, [{ id, name, current, folder }], ids as
// fileRequest's "doc:" takes them ("sample:<key>" for a sample), and its
// folders, [{ id, name }]. The open deck shows its name as it is now
// (renamed, not saved yet).
async function roomView(room) {
  const live = (r) => (r.current && !r.id.startsWith("sample:") ? { ...r, name: exportName() || r.name } : r);
  if (ownServer()) {
    const g = await roomsCall("get_room", { room_id: room });
    return {
      rows: (g.presentations || []).map((p) => live({ id: "cloud:" + p.deck_id, name: p.name || t("presentation"), current: p.deck_id === doc.cloud, folder: p.folder_id || "" })),
      folders: (g.folders || []).map((f) => ({ id: f.folder_id, name: f.name })),
    };
  }
  return { rows: roomDecks(roomsHere, room, await allDocs(), sampleRows()).map(live), folders: foldersOf(roomsHere, room) };
}
async function roomRows(room) {
  return (await roomView(room)).rows;
}
// A room's presentations kept in this browser taken out of it (the room's
// gear → Clear, confirmed): each one's copy here deleted, the open one kept.
// Those only in the cloud are not this browser's to remove.
async function clearRoom(room) {
  if (ownServer() || !vfs) return;
  let gone = 0, kept = 0;
  for (const r of await roomRows(room)) {
    if (r.id.startsWith("cloud:") || r.id.startsWith("sample:")) continue;
    if (r.id === doc.id) { kept++; continue; }
    await vfs.deleteDoc(r.id);
    gone++;
  }
  toast(t("Removed from this browser: ") + gone + (kept ? " · " + t("the open presentation stays") : ""));
  await refreshDecks().catch(() => {});
  await roomsRequest("room:list");
}
// The open room's presentations listed again (the open deck renamed).
function roomDecksAgain() {
  if (roomShown) roomsRequest("room:decks:" + roomShown).catch(() => {});
}
// The open presentation's id in the room lists ("" while it is not kept yet,
// as an unedited sample is not).
async function currentRoomId() {
  if (ownServer()) return doc.cloud ? "cloud:" + doc.cloud : "";
  return (await allDocs()).find((d) => d.current)?.id || "";
}
// The room the presentation `id` (currentRoomId's) is in: on a server of
// one's own its share's, else this browser's placement.
async function deckRoom(id) {
  if (!ownServer()) return roomOf(roomsHere, id);
  const res = await fetch("/api/shares/" + encodeURIComponent(id.replace(/^cloud:/, "")));
  if (!res.ok) return "";
  return (await res.json()).room || "general";
}
// The Document settings window's rooms: the ones one may move the open
// presentation to (not Onboarding, which holds the samples), and the one it
// is in, archived or not.
async function docRooms() {
  const id = await currentRoomId();
  if (!id) {
    app.setDocRooms("", "", t("Make a change first: a sample becomes a presentation of your own when it is edited."));
    needsPaint = true;
    return;
  }
  const [rooms, all, cur] = await Promise.all([roomsList(), roomsList({ archived: true }), deckRoom(id)]);
  const shown = rooms.filter((x) => x.room_id !== ONBOARDING);
  if (cur && !shown.some((x) => x.room_id === cur)) {
    const x = all.find((y) => y.room_id === cur);
    if (x) shown.push(x);
  }
  const rows = shown.map((x) => x.room_id + "\t" + clean(x.title) + (x.archived ? " (" + t("archived") + ")" : ""));
  app.setDocRooms(rows.join("\n"), cur, "");
  needsPaint = true;
}
// the search field's text while it is open (null: the rooms one is active in)
let roomsQuery = null;
// the presentations the search found, by id (deckSearch's rows)
let foundDecks = new Map();
// The Rooms search through the presentations' words, not their Markdown's
// syntax (RangerMarkdown MdSearchText): on a server of one's own its
// search_presentations; on sliqtly.com/editor the server searches the
// signed-in user's own decks (GET /editor/api/search), and the decks kept
// only in this browser are searched here, by the same model in the app.
// → [{ id (as fileRequest's "doc:" takes it), name, room, roomTitle, snippet }]
const searchTexts = new Map();
async function deckSearch(q) {
  if (ownServer()) {
    const out = await roomsCall("search_presentations", { query: q });
    return (out.presentations || []).map((p) => ({ id: "cloud:" + p.deck_id, name: p.name || "", room: p.room_id || GENERAL, roomTitle: p.room || "", snippet: p.snippet || "" }));
  }
  const [local, rooms] = await Promise.all([vfs.listDocs(), roomsList({ archived: true })]);
  const titleOf = (id) => rooms.find((r) => r.room_id === id)?.title || "";
  const row = (id, name, snippet) => {
    const room = roomOf(roomsHere, id);
    return { id, name, room, roomTitle: titleOf(room), snippet };
  };
  const out = [];
  // the cloud's decks are the server's to search; a deck of this browser
  // that is kept there too is opened as this browser's
  let searched = null;
  if (window.sliqtly?.searchDecks && window.sliqtly.user?.()) {
    const kept = new Map(local.filter((d) => d.cloud).map((d) => [d.cloud, d]));
    const hits = await window.sliqtly.searchDecks(q);
    for (const p of hits) {
      const d = kept.get(p.deck_id);
      out.push(row(d ? d.id : "cloud:" + p.deck_id, d?.name || p.name || "", p.snippet || ""));
    }
    searched = new Set(kept.keys());
  }
  for (const d of local) {
    if (searched && d.cloud && searched.has(d.cloud)) continue;
    const md = String(d.md || "");
    let t = searchTexts.get(d.id);
    if (!t || t.md !== md) searchTexts.set(d.id, (t = { md, text: app.searchText(md) }));
    const hit = app.searchSnippet(d.name || "", t.text, q);
    if (hit) out.push(row(d.id, d.name || "", hit.slice(1)));
  }
  return out.slice(0, 50);
}
// the rooms the rail lists now (list_rooms' rows)
let roomsListed = [];
// The rail's room rows: "id TAB name TAB count TAB u", u when the room has
// messages this browser has not read (a blue dot). An empty room shows no
// count ("0" says nothing the empty list does not).
function showRoomRows() {
  let store = null;
  try { store = mine; } catch (_) { store = { getItem: () => null }; }
  const reading = roomChatOne && app.roomChatOpen() ? roomChatOne.room : "";
  const unread = unreadRooms(roomsListed, store, reading);
  const rows = roomsListed.map((x) => [x.room_id, clean(x.title) + (x.archived ? " (" + t("archived") + ")" : ""), x.presentations || "", unread.has(x.room_id) ? "u" : ""].join("\t"));
  app.setToolbarOptions("rooms", rows.join("\n"), roomShown);
  needsPaint = true;
}
// a room made, renamed, archived or removed, or a deck moved, by anyone:
// the server says so on the page's stream, and the list is read again
window.addEventListener("sliqtly:chat", (ev) => {
  // a message in a listed room: its dot, unless its chat is open and read
  const v = ev.detail;
  if (v?.t === "msg" && v.msg) {
    const r = roomsListed.find((x) => x.room_id === v.room);
    if (r && Number(v.msg.seq) > Number(r.chat_seq || 0)) {
      r.chat_seq = Number(v.msg.seq);
      showRoomRows();
    }
  }
  if ((ev.detail?.t === "rooms" || ev.detail?.t === "reopen") && ownServer()) {
    roomsRequest("room:list").then(() => { needsPaint = true; }, () => {});
    // an open Document settings window shows the deck's room as it is now
    if (app.chartIsOpen()) docRooms().catch(() => {});
  }
});
async function roomsRequest(r) {
  const [, action, ...rest] = r.split(":");
  const what = rest.join(":");
  if (action === "list") {
    const q = roomsQuery;
    const all = await roomsList();
    // typed on since: that text's own list is the one to show
    if (roomsQuery !== q) return;
    // the rooms one is active in, or what the search finds; the open room
    // stays listed
    let rooms, hidden = 0;
    const searching = q !== null && q.trim() !== "";
    if (searching) rooms = searchRooms(await roomsList({ archived: true }), q);
    else ({ shown: rooms, hidden } = activeRooms(all, roomsHere));
    // ...and the presentations whose words hold it, under the rooms
    let found = [];
    if (searching) {
      found = await deckSearch(q).catch((e) => { console.warn("deck search", e); return []; });
      if (roomsQuery !== q) return;
    }
    foundDecks = new Map(found.map((x) => [x.id, x]));
    app.setToolbarOptions("roomfound", found.map((x) => [x.id, clean(x.name) || t("presentation"), clean(x.roomTitle), clean(x.snippet)].join("\t")).join("\n"), "");
    if (!searching && !rooms.some((x) => x.room_id === roomShown)) {
      const open = all.find((x) => x.room_id === roomShown);
      if (open) rooms = [...rooms, open];
      else roomShown = rooms[0]?.room_id || "";
    }
    roomsListed = rooms;
    app.setToolbarOptions("roomsearch", "", t("Search rooms…") + (hidden ? " (" + hidden + t(" more") + ")" : ""));
    showRoomRows();
    if (roomShown && rooms.some((x) => x.room_id === roomShown)) await roomsRequest("room:decks:" + roomShown);
    else app.setToolbarOptions("roomdecks", "", roomShown);
  } else if (action === "search") {
    roomsQuery = what;
    // the words are searched once typing pauses (the server parses decks)
    if (what.trim()) await new Promise((ok) => setTimeout(ok, 200));
    if (roomsQuery !== what) return;
    await roomsRequest("room:list");
  } else if (action === "found") {
    // a presentation found: it opens, and its room with it
    const hit = foundDecks.get(what);
    roomsQuery = null;
    if (hit?.room) {
      roomShown = hit.room;
      keepRooms((s) => touchRoom(s, hit.room));
    }
    roomChat().close();
    await fileRequest("doc:" + what);
    await roomsRequest("room:list");
  } else if (action === "searchend") {
    roomsQuery = null;
    await roomsRequest("room:list");
  } else if (action === "searchgo" || action === "pick") {
    // Enter: the first room found; a press: that room. Either is now one of
    // the rooms one is active in
    const id = action === "pick" ? what : searchRooms(await roomsList({ archived: true }), what)[0]?.room_id;
    roomsQuery = null;
    if (id) {
      roomShown = id;
      keepRooms((s) => touchRoom(s, id));
    }
    await roomsRequest("room:list");
    if (id) await roomChat().open(id);
  } else if (action === "drop" || action === "tabdrop") {
    // a presentation dragged from the open room, or a deck's tab, onto a
    // room: Move, Copy or Cancel (a sample is only copied)
    const [room, ...deckParts] = rest;
    const deck = action === "tabdrop" ? await tabDeckId(deckParts.join(":")) : deckParts.join(":");
    if (!deck) {
      toast(t("Make a change first: a sample becomes a presentation of your own when it is edited."));
      return;
    }
    const all = await roomsList({ archived: true });
    const to = all.find((x) => x.room_id === room)?.title || room;
    if (deck.startsWith("sample:")) {
      await copyDeckTo(room, deck);
      return;
    }
    if (room === ONBOARDING) {
      toast(t("Onboarding holds the sample presentations only."));
      return;
    }
    app.openChoice("roomdrop:" + room + ":" + deck, t("Move or copy?"),
      t("Move the presentation to the room, or make a copy of it there?") + "\n" + "# " + to, t("Move"), t("Copy"));
  } else if (action === "dropgo") {
    // the answer: "<room>:<deck>" moved, "<room>:<deck>:alt" copied
    const copy = what.endsWith(":alt");
    const [room, ...deckParts] = (copy ? what.slice(0, -4) : what).split(":");
    const deck = deckParts.join(":");
    if (copy) await copyDeckTo(room, deck);
    else await roomsRequest("room:moveid:" + room + ":" + deck);
    return;
  } else if (action === "order") {
    // a room dragged before another (onto a built-in one: first)
    const [id, before] = rest;
    const rows = await roomsList({ archived: true });
    keepRooms((s) => moveRoom(s, rows, id, before || ""));
    await roomsRequest("room:list");
  } else if (action === "new") {
    app.openRoomDialog("", "new", "", "");
  } else if (action === "settings") {
    const x = (await roomsList({ archived: true })).find((r) => r.room_id === what);
    if (x && what !== ONBOARDING) app.openRoomDialog(what, isBuiltIn(what) ? "builtin" : x.archived ? "archived" : "made", x.title, x.description || "");
  } else if (action === "save") {
    // the room's window answered (newdeck-create, "ask" "room")
    const plan = JSON.parse(what);
    const id = plan.room;
    if (plan.act === "clear") {
      const x = (await roomsList({ archived: true })).find((r) => r.room_id === id);
      app.openConfirm("roomclear:" + id, t("Clear room"),
        t("Remove this browser's copies of the presentations in ") + "\"" + (x?.title || "") + "\"? " + t("Those saved in your cloud stay there; one kept only in this browser is gone for good. The open presentation stays."), t("Clear"));
      return;
    }
    if (plan.act === "delete") {
      const x = (await roomsList({ archived: true })).find((r) => r.room_id === id);
      app.openConfirm("roomdelete:" + id, t("Delete room"),
        t("Delete the room ") + "\"" + (x?.title || "") + "\"? " + t("Its presentations move to General; none of them is deleted."), t("Delete"));
      return;
    }
    if (plan.act === "archive" || plan.act === "unarchive") {
      const on = plan.act === "archive";
      if (ownServer()) await roomsCall("archive_room", { room_id: id, archived: on });
      else keepRooms((s) => archiveRoom(s, id, on));
      if (on && roomShown === id) roomShown = "";
      toast(on ? t("Room archived. Search finds it.") : t("Room restored."));
    } else if (id) {
      if (ownServer()) await roomsCall("update_room", { room_id: id, title: plan.name, description: plan.desc });
      else keepRooms((s) => updateRoom(s, id, { title: plan.name, description: plan.desc }));
    } else {
      await roomsRequest("room:create:" + JSON.stringify({ title: plan.name, description: plan.desc }));
      return;
    }
    await roomsRequest("room:list");
  } else if (action === "delete") {
    if (ownServer()) await roomsCall("delete_room", { room_id: what });
    else keepRooms((s) => deleteRoom(s, what));
    if (roomShown === what) roomShown = "";
    await roomsRequest("room:list");
  } else if (action === "moveid") {
    const [room, ...deckParts] = rest;
    const deck = deckParts.join(":");
    if (ownServer()) await roomsCall("move_presentation", { deck_id: deck.replace(/^cloud:/, ""), room_id: room });
    else if (!deck.startsWith("sample:")) keepRooms((s) => touchRoom(moveDeck(s, deck, room), room));
    await roomsRequest("room:list");
  } else if (action === "open" || action === "decks") {
    // a room pressed opens its chat (web/roomchat.js); the list shown again
    // ("decks") only lists its presentations
    roomShown = what;
    // pressed: a room one is active in (the list shown again is no use of it)
    if (action === "open") keepRooms((s) => touchRoom(s, roomShown));
    const { rows, folders } = await roomView(roomShown);
    const lines = deckLines(rows, {
      folders,
      open: folders.filter((f) => foldersOpen.has(roomShown + "/" + f.id)).map((f) => f.id),
      showAll: "… " + t("Show all") + " (" + rows.length + ")",
      addNew: roomShown === ONBOARDING ? "" : "+ " + t("Add new presentation"),
      newFolder: roomShown === ONBOARDING ? "" : "+ " + t("New folder"),
    });
    app.setToolbarOptions("roomdecks", lines, roomShown);
    if (action === "open") await roomChat().open(roomShown);
  } else if (action === "fold") {
    // a folder pressed: open or shut, in this browser
    const [room, folder] = rest;
    const key = room + "/" + folder;
    if (foldersOpen.has(key)) foldersOpen.delete(key);
    else foldersOpen.add(key);
    keepFoldersOpen();
    await roomsRequest("room:decks:" + room);
    return;
  } else if (action === "newfolder") {
    // "<room>" from the room's row, "<room>:<deck>" from a presentation's
    // Move to folder: the new folder then takes that presentation
    const [room, ...deckParts] = rest;
    folderFor = { room, id: "", deck: deckParts.join(":") };
    app.openFolderDialog("", "");
  } else if (action === "folderset") {
    const [room, folder] = rest;
    const f = (await roomView(room)).folders.find((x) => x.id === folder);
    if (!f) return;
    folderFor = { room, id: folder };
    app.openFolderDialog(folder, f.name);
  } else if (action === "foldersave") {
    // the folder's window answered (newdeck-create, "ask" "folder")
    const plan = JSON.parse(what);
    const at = folderFor;
    folderFor = null;
    if (!at) return;
    const { room, id } = at;
    if (plan.act === "delete" && id) {
      if (ownServer()) await roomsCall("delete_folder", { room_id: room, folder_id: id });
      else keepRooms((s) => deleteFolder(s, room, id));
      foldersOpen.delete(room + "/" + id);
      keepFoldersOpen();
      toast(t("Folder deleted. Its presentations are at the room's top."));
    } else if (id) {
      if (ownServer()) await roomsCall("rename_folder", { room_id: room, folder_id: id, name: plan.name });
      else keepRooms((s) => renameFolder(s, room, id, plan.name));
    } else if (String(plan.name || "").trim()) {
      let made = "";
      if (ownServer()) made = (await roomsCall("create_folder", { room_id: room, name: plan.name })).folder_id || "";
      else keepRooms((s) => {
        const r = createFolder(s, room, plan.name, newId);
        made = r.id;
        return r.state;
      });
      // a new folder is shown open, ready for what is dragged onto it
      if (made) {
        foldersOpen.add(room + "/" + made);
        keepFoldersOpen();
        if (at.deck) await roomsRequest("room:file:" + room + ":" + made + ":" + at.deck);
      }
    }
    if (roomShown === room) await roomsRequest("room:decks:" + room);
  } else if (action === "file") {
    // a presentation let go on a folder of its room ("<room>:<folder>:<deck>"),
    // or on its own room (folder ""): to the room's top
    const [room, folder, ...deckParts] = rest;
    const deck = deckParts.join(":");
    if (deck.startsWith("sample:")) return;
    if (ownServer()) await roomsCall("move_presentation", { deck_id: deck.replace(/^cloud:/, ""), room_id: room, folder_id: folder });
    else keepRooms((s) => moveDeck(s, deck, room, folder));
    if (folder) {
      foldersOpen.add(room + "/" + folder);
      keepFoldersOpen();
    }
    await roomsRequest("room:decks:" + room);
  } else if (action === "deck") {
    roomChat().close();
    if (what.startsWith("sample:")) await openSample(what.slice(7));
    else await fileRequest("doc:" + what);
    await roomsRequest("room:list");
  } else if (action === "all") {
    // the samples are in Open; a room's decks in the presentations window
    if (what === ONBOARDING) {
      app.openOpen([...sampleSel.options].map((o) => o.value + "\t" + o.textContent.trim()).join("\n"));
    } else {
      const title = (await roomsList()).find((x) => x.room_id === what)?.title || "";
      decksRoom = { room: what, title };
      await openDecks();
    }
  } else if (action === "create") {
    // { title, description }: the new room is first in the list, and open
    const { title, description } = JSON.parse(what);
    if (!String(title || "").trim()) return;
    if (ownServer()) {
      const { room_id } = await roomsCall("create_room", { title: String(title).replace(/\s+/g, " ").trim(), description: description || "" });
      if (room_id) {
        roomShown = room_id;
        keepRooms((s) => touchRoom(s, room_id));
      }
    } else {
      let id = "";
      keepRooms((s) => {
        const made = createRoom(s, title, newId, { description });
        id = made.id;
        return made.state;
      });
      if (id) roomShown = id;
    }
    await roomsRequest("room:list");
  } else if (action === "newin") {
    // File → New's window; the deck it makes goes to this room (newDeck)
    app.openNewDeck(selectRows(themeSel), themeSel.value || "", "");
    roomForNew = what;
  }
  needsPaint = true;
}
// A room's chat (web/roomchat.js): made the first time a room is opened
// (roomChatOne, declared with the Rooms panel's state above).
function roomChat() {
  if (roomChatOne) return roomChatOne;
  let store = null;
  try { store = mine; } catch (_) { store = { getItem: () => null, setItem: () => {} }; }
  roomChatOne = new RoomChat({
    app, store, t, toast, ownServer,
    name: shownName(),
    call: roomsCall,
    now: () => Date.now(),
    zone: () => -new Date().getTimezoneOffset(),
    rooms: () => roomsList({ archived: true }),
    roomsChanged: () => roomsRequest("room:list"),
    // the open room read: its dot goes
    readChanged: () => showRoomRows(),
    openLink: (u) => { if (/^https?:\/\//i.test(u)) window.open(u, "_blank", "noopener"); },
    openDeck: async (deck, slide) => {
      await roomsRequest("room:deck:cloud:" + deck);
      if (slide > 0) app.selectSlide(slide - 1);
      needsPaint = true;
    },
    openRoom: (id) => roomsRequest("room:pick:" + id),
    copy: async (s) => {
      const ok = await writeClip(s);
      toast(ok ? t("Copied") : t("Could not copy"));
    },
    paint: () => { needsPaint = true; },
    put: async (path, body, type) => {
      const res = await fetch(path, { method: "PUT", headers: { "Content-Type": type }, body });
      const out = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(out.error || res.status);
      return out;
    },
    // the picker now, while the press still counts as one
    pickFiles: () => new Promise((resolve) => {
      const input = document.createElement("input");
      input.type = "file";
      input.multiple = true;
      input.hidden = true;
      const done = (files) => { input.remove(); resolve(files); };
      input.addEventListener("change", () => done([...(input.files || [])]));
      input.addEventListener("cancel", () => done([]));
      document.body.append(input);
      input.click();
    }),
    sizeOf: async (file) => {
      const p = await decodePicture(new Uint8Array(await file.arrayBuffer()), file.type, file.name);
      return { w: p.img.width, h: p.img.height };
    },
    deckName: async (id) => {
      const res = await fetch("/api/shares/" + encodeURIComponent(id));
      return res.ok ? (await res.json()).name || "" : "";
    },
    load: async (src) => {
      const res = await fetch(src);
      if (!res.ok) throw new Error(res.status);
      const type = res.headers.get("Content-Type") || "image/png";
      return (await decodePicture(new Uint8Array(await res.arrayBuffer()), type, src)).img;
    },
  });
  window.addEventListener("sliqtly:chat", (ev) => { roomChatOne.event(ev.detail).catch(() => {}); });
  return roomChatOne;
}

// The name edited at the start of the bar: the deck's title (front matter
// `title:`, else its first heading) rewritten, as one edit that undoes, and
// the open presentations' tab follows.
function renameDeck(name) {
  name = String(name || "").replace(/\s+/g, " ").trim();
  if (!name) return;
  const before = app.source().split("\n");
  const after = retitled(app.source(), name).split("\n");
  const i = after.findIndex((l, k) => l !== before[k]);
  if (i >= 0) app.renameLine(i, after[i]);
  // no title anywhere: a heading first
  else if (app.docTitle() !== name) app.renameLine(0, "# " + name + "\n\n" + (before[0] || ""));
  docName = name;
  if (shownKey) app.deckTabOpen(shownKey, tabLabel(exportName()));
  collab?.takeLocal();
  roomDecksAgain();
  needsPaint = true;
}
function copyName() {
  return exportName() + " " + t("(copy)");
}
async function duplicateDeck(asked) {
  await leaveDoc();
  const key = themeSel.value || "";
  const css = key in editedCss ? editedCss[key] : null;
  const files = await docFiles();
  const name = String(asked || "").replace(/\s+/g, " ").trim() || copyName();
  const text = retitled(app.source(), name);
  beginDoc(text);
  docName = name;
  if (css != null) editedCss[key] = css;
  useTheme(key);
  for (const f of files) {
    const rec = { ...f, doc: doc.id, updated: Date.now() };
    pending.set(rec.path, rec);
    await useFile(rec);
  }
  shownDoc(text);
  dropThumbs();
  await saveDoc(true);
  await commitVersion("@created").catch((e) => console.warn("no version kept", e));
  if (cloudReady()) await cloudSync().catch(cloudTrouble);
  refreshFiles();
  needsPaint = true;
  toast(t("Duplicated as ") + name);
}
// File → New → Datasheet…: the spreadsheet editor on an empty workbook. Its
// first Save keeps it in this deck as data/sheet-<n>.xlsx, where charts and
// tables read its sheets as any added workbook's.
async function newSheet() {
  const have = new Set((await docFiles()).map((f) => f.path));
  let n = 1;
  while (have.has(`data/sheet-${n}.xlsx`)) n++;
  const path = `data/sheet-${n}.xlsx`;
  await liveSheets.openDialog({
    name: path.split("/").pop(),
    bytes: undefined,
    blank: true,
    onSave: (raw) => saveWorkbook(path, raw),
    onClose: () => { keys.focus({ preventScroll: true }); refreshFiles(); needsPaint = true; },
  }).catch((e) => toast(t("The spreadsheet editor did not load: ") + (e.message || e)));
}

// A row of a presentation's menu in Rooms or on its deck tab
// ("<room|tab>:<id>\t<request>"): the File menu's rows act on the open deck,
// so that deck opens first; one that did not open has nothing done to the
// deck open instead.
async function deckDo(what) {
  const tab = what.indexOf("\t");
  if (tab < 0) return;
  const [from, ...rest] = what.slice(0, tab).split(":");
  const id = rest.join(":");
  if (from === "tab") {
    await switchDeck(id);
    if (shownKey !== id) return;
  } else {
    await roomsRequest("room:deck:" + id);
    if (!id.startsWith("sample:") && (await currentRoomId()) !== id) return;
  }
  app.request(what.slice(tab + 1));
  handleRequests();
  needsPaint = true;
}

// File → Delete presentation…, once confirmed: the open deck removed from
// this browser and, for a PRO deck, its share and files from the cloud. The
// latest other deck opens in its place, or a new empty one.
async function deleteDeck() {
  const id = doc.id;
  const cloud = doc.cloud;
  const name = exportName();
  // nothing more is written to the cloud for it
  doc.cloudHalt = true;
  clearTimeout(cloudTimer);
  cloudTimer = 0;
  if (cloudBusy) await cloudBusy.catch(() => {});
  if (saving) await saving.catch(() => {});
  if (cloud && window.sliqtly?.user?.()) {
    try {
      await window.sliqtly.deleteShare(cloud);
    } catch (e) {
      // invited to edit someone else's: only this browser's copy goes, the
      // presentation stays its owner's
      if (e?.code === "permission-denied") {
        toast(t("Removed from this browser. The presentation is its owner's and stays in the cloud."));
      } else {
        doc.cloudHalt = false;
        toast(t("Deleting from the cloud failed: ") + (e.message || e));
        return;
      }
    }
  }
  if (vfs && doc.persisted) await vfs.deleteDoc(id);
  // its tab goes; the tab used before it comes to the front
  if (shownKey) app.deckTabClose(shownKey);
  shownKey = null;
  keepTabs();
  // let go of it, so leaving it does not save it again
  beginDoc("");
  shownDoc("", true);
  const front = app.deckTabFront();
  let opened = !!front && (await openDeckKey(front).catch(() => false));
  if (front && !opened) app.deckTabClose(front);
  if (!opened) {
    const next = vfs ? (await vfs.listDocs()).filter((d) => d.id !== id).sort((a, b) => (b.updated || 0) - (a.updated || 0))[0] : null;
    opened = !!next && (await openDoc(next.id));
  }
  if (!opened) {
    try { mine.removeItem("evgp.doc"); } catch (_) { /* fine */ }
    await newDeck({ name: t("New presentation"), theme: themeSel.value || "", data: "none" });
  }
  toast(t("Deleted ") + name);
}

// File → Export → All files (.zip): the Markdown, the theme's CSS as it is
// now (edits included) and every file of the deck at its own path.
// File → Export → .zip: a deck with review comments asks whether they go in
// the ZIP too (they are notes on the deck, not part of it).
async function askZip() {
  const has = (await docFiles()).some((f) => f.path === REVIEW_PATH) && app.reviewCount() > 0;
  if (has && app.openChoice("zip", t("Export ZIP"), t("This presentation has review comments. Include them in the ZIP file?"), t("Include comments"), t("Without comments"))) {
    needsPaint = true;
    return;
  }
  await exportZip(true);
}
async function exportZip(withComments) {
  const enc = new TextEncoder();
  const base = exportName();
  const entries = [
    { name: base + ".md", data: enc.encode(app.source()) },
    { name: (themeSel.value || "theme") + ".css", data: enc.encode(app.themeCss()) },
  ];
  for (const f of await docFiles()) {
    if (!withComments && f.path === REVIEW_PATH) continue;
    const data = typeof f.data === "string" ? enc.encode(f.data) : new Uint8Array(await f.data.arrayBuffer());
    entries.push({ name: f.path, data });
  }
  window.__lastDownload = deliver(zipStore(entries), base + ".zip", "application/zip");
}

// A zip of `entries` ({ name, data: Uint8Array }), stored (no compression:
// the pictures in it are compressed already), names in UTF-8.
const CRC_TABLE = (() => {
  const tbl = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    tbl[n] = c >>> 0;
  }
  return tbl;
})();
function crc32(bytes) {
  let c = 0xffffffff;
  for (let i = 0; i < bytes.length; i++) c = CRC_TABLE[(c ^ bytes[i]) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}
function zipStore(entries) {
  const enc = new TextEncoder();
  const d = new Date();
  const time = (d.getHours() << 11) | (d.getMinutes() << 5) | (d.getSeconds() >> 1);
  const date = ((d.getFullYear() - 1980) << 9) | ((d.getMonth() + 1) << 5) | d.getDate();
  const parts = [];
  const central = [];
  let offset = 0;
  for (const e of entries) {
    const name = enc.encode(e.name);
    const crc = crc32(e.data);
    const head = new DataView(new ArrayBuffer(30));
    head.setUint32(0, 0x04034b50, true);
    head.setUint16(4, 20, true);
    head.setUint16(6, 0x0800, true); // UTF-8 names
    head.setUint16(8, 0, true); // stored
    head.setUint16(10, time, true);
    head.setUint16(12, date, true);
    head.setUint32(14, crc, true);
    head.setUint32(18, e.data.length, true);
    head.setUint32(22, e.data.length, true);
    head.setUint16(26, name.length, true);
    parts.push(new Uint8Array(head.buffer), name, e.data);
    const c = new DataView(new ArrayBuffer(46));
    c.setUint32(0, 0x02014b50, true);
    c.setUint16(4, 20, true);
    c.setUint16(6, 20, true);
    c.setUint16(8, 0x0800, true);
    c.setUint16(10, 0, true);
    c.setUint16(12, time, true);
    c.setUint16(14, date, true);
    c.setUint32(16, crc, true);
    c.setUint32(20, e.data.length, true);
    c.setUint32(24, e.data.length, true);
    c.setUint16(28, name.length, true);
    c.setUint32(42, offset, true);
    central.push(new Uint8Array(c.buffer), name);
    offset += 30 + name.length + e.data.length;
  }
  const size = central.reduce((n, p) => n + p.length, 0);
  const end = new DataView(new ArrayBuffer(22));
  end.setUint32(0, 0x06054b50, true);
  end.setUint16(8, entries.length, true);
  end.setUint16(10, entries.length, true);
  end.setUint32(12, size, true);
  end.setUint32(16, offset, true);
  const all = [...parts, ...central, new Uint8Array(end.buffer)];
  const out = new Uint8Array(all.reduce((n, p) => n + p.length, 0));
  let at = 0;
  for (const p of all) { out.set(p, at); at += p.length; }
  return out;
}
window.__zipStore = zipStore;

// The deck's title (front matter title:, else the first heading) as `name`.
function retitled(md, name) {
  const lines = md.split("\n");
  if (/^---\s*$/.test(lines[0] || "")) {
    for (let i = 1; i < lines.length && !/^(---|\.\.\.)\s*$/.test(lines[i]); i++) {
      const m = /^title\s*:\s*(.*)$/.exec(lines[i]);
      if (m && m[1].trim()) {
        lines[i] = "title: " + JSON.stringify(name);
        return lines.join("\n");
      }
    }
  }
  let fence = "";
  for (let i = 0; i < lines.length; i++) {
    const f = /^\s*(`{3,}|~{3,})/.exec(lines[i]);
    if (f) {
      if (!fence) fence = f[1][0];
      else if (f[1][0] === fence) fence = "";
      continue;
    }
    // a heading's {.class} stays
    const h = !fence && /^(#{1,6})\s+\S.*?(\s+\{[^{}]*\})?\s*$/.exec(lines[i]);
    if (h) {
      lines[i] = h[1] + " " + name + (h[2] || "");
      return lines.join("\n");
    }
  }
  return md;
}
function sampleChartSlide() {
  const rows = [[t("Q1"), 28], [t("Q2"), 55], [t("Q3"), 43], [t("Q4"), 91]]
    .map(([a, b]) => `    {"${t("quarter")}": ${JSON.stringify(a)}, "${t("sales")}": ${b}}`).join(",\n");
  return "## " + t("Chart") + "\n\n```vega-lite\n{\n  \"data\": {\"values\": [\n" + rows + "\n  ]},\n  \"mark\": \"bar\",\n"
    + `  "encoding": {\n    "x": {"field": "${t("quarter")}", "type": "nominal", "axis": {"labelAngle": 0}},\n    "y": {"field": "${t("sales")}", "type": "quantitative"}\n  }\n}\n` + "```\n";
}

// A deck opened from a link (#md=…, /s/{id}?edit, ?sample=…) is the reader's
// own once it is saved: the address loses the link, so a reload opens the
// saved deck, pictures and all, and not the link's text again.
function plainAddress() {
  // a PRO deck: its own address, which a reload opens from the cloud
  if (doc.cloud) {
    history.replaceState(null, "", editAddress(doc.cloud));
    lastHash = "";
    return;
  }
  const q = new URLSearchParams(location.search);
  const shared = SHARED_PATH.test(location.pathname);
  if (!location.hash && !shared && !q.has("sample") && !q.has("deck")) return;
  q.delete("sample");
  q.delete("edit");
  q.delete("deck");
  q.delete("from");
  const search = q.toString();
  history.replaceState(null, "", (shared ? EDITOR_ROOT + "/" : location.pathname) + (search ? "?" + search : ""));
  lastHash = "";
}

// A file of the document put to use: a picture registered for the slides, a
// text file handed to the charts.
// the files the slides were given (useFile), path → stamp
const usedStamps = new Map();
async function useFile(f) {
  usedStamps.set(f.path, stampOf(f));
  if (kindOf(f.path, f.type) === "image" && f.data instanceof Blob) {
    const bytes = await f.data.arrayBuffer();
    await addPicture("/" + f.path, bytes, f.type);
  } else if (typeof f.data === "string") {
    if (f.path === REC_JSON) app.loadRecording(f.data);
    if (f.path.startsWith("data/live/")) liveCopies.set(f.path, f.data);
    chartFiles.set(f.path, Promise.resolve(f.data));
    app.setChartData(f.path, f.data);
  }
}

async function openDoc(id) {
  try {
    return await openDocNow(id);
  } catch (e) {
    // stopped half way: the editor shows the deck it had begun to open
    if (doc.loading) shownDoc(doc.openedText);
    throw e;
  }
}
async function openDocNow(id) {
  if (!vfs) return false;
  await leaveDoc();
  const d = await vfs.getDoc(id);
  if (!d) return false;
  beginDoc(d.md);
  doc.id = d.id;
  doc.persisted = true;
  doc.created = d.created || Date.now();
  doc.cloud = d.cloud || null;
  doc.cloudMd = d.cloudMd ?? null;
  doc.cloudCss = d.cloudCss ?? null;
  doc.cloudTheme = d.cloudTheme ?? null;
  doc.cloudFiles = d.cloudFiles ?? null;
  themeSel.value = d.theme || "";
  if (d.css != null) editedCss[d.theme || ""] = d.css;
  useTheme(themeSel.value);
  const files = await vfs.listFiles(doc.id);
  for (const f of files) await useFile(f);
  // files the share has not got yet (the page was left while they went):
  // sent now, not on the next change
  const there = new Set(doc.cloudFiles || []);
  if (doc.cloud && doc.cloudFiles && files.some((f) => !there.has(f.path))) setTimeout(cloudSoon, 0);
  docName = d.name || "presentation";
  shownDoc(d.md);
  savedText = d.md;
  savedVersion = -1;
  savedCss = d.css == null ? null : d.css;
  savedTheme = d.theme || "";
  try { mine.setItem("evgp.doc", lastKey()); } catch (_) { /* fine */ }
  plainAddress();
  dropThumbs();
  needsPaint = true;
  refreshFiles();
  return true;
}

function whenText(t) {
  const d = new Date(t || 0);
  const two = (n) => String(n).padStart(2, "0");
  return `${d.getDate()}.${d.getMonth() + 1}.${d.getFullYear()} ${two(d.getHours())}:${two(d.getMinutes())}`;
}

// Signed in: the user's own shares; those this browser does not keep are
// "cloud:<share id>" decks (opened from the cloud, as /s/{id}?edit is).
// error: why the last read failed, said in the presentations window.
let cloudList = { uid: null, at: 0, rows: [], error: "" };
let cloudListing = null;
function readCloudList(uid) {
  // and the ones others invited the user to edit
  cloudListing ??= Promise.all([window.sliqtly.listMine(), window.sliqtly.listInvited?.().catch(() => []) ?? []])
    .then(([mine, invited]) => mine.concat(invited))
    .then((rows) => { cloudList = { uid, at: Date.now(), rows, error: "" }; })
    .catch((e) => {
      console.warn("listing the cloud decks failed", e);
      cloudList = { uid, at: Date.now(), rows: [], error: e?.message || String(e) };
    })
    .finally(() => { cloudListing = null; });
  return cloudListing;
}
// The user's cloud shares, signed in ([] otherwise): read at sign-in; after
// that, a list older than 30 s is read again behind the one shown, which is
// redrawn when it arrives. fresh: read now (the presentations window opening,
// so a deck an assistant just made is there).
async function cloudShares(fresh = false) {
  const user = window.sliqtly?.user?.();
  if (!user || !window.sliqtly.listMine) return [];
  if (cloudList.uid !== user.uid || fresh) await readCloudList(user.uid);
  else if (Date.now() - cloudList.at > 30000 && !cloudListing) readCloudList(user.uid).then(() => refreshFiles());
  return cloudList.rows;
}
// Every deck: this browser's and the cloud's, one row each (web/decklist.js).
async function allDocs(fresh = false) {
  const local = await vfs.listDocs();
  return deckRows(local, await cloudShares(fresh), doc.id, { byCloud: CLOUD_ONLY });
}

// File → Presentations…: the window over the editor, last changed first
// unless sorted otherwise; again when the list changes while it shows.
// { by, dir }, kept as "by" or "by:dir" (a column's head pressed again
// turns its order round)
let decksSort = { by: "updated", dir: "desc" };
// the room the window lists, from its "… Show all" ({ room, title }), or null
// for every presentation
let decksRoom = null;
try {
  const [by, dir] = (localStorage.getItem("sliqtly.decksSort") || "updated").split(":");
  decksSort = { by, dir: dir || firstDir(by) };
} catch (_) { /* the default */ }
// The window's rows for a room's "… Show all": on a server of one's own the
// room's presentations as the server keeps them (many are made by an
// assistant and are not this browser's), else this browser's decks in it.
async function roomWindowRows(room, fresh) {
  if (ownServer()) {
    const g = await roomsCall("get_room", { room_id: room });
    return roomShareRows(g.presentations, await vfs.listDocs(), doc.id);
  }
  const ids = new Set((await roomRows(room)).map((x) => x.id));
  return (await allDocs(fresh)).filter((d) => ids.has(d.id));
}
async function decksJson(fresh = false) {
  const signedOut = window.sliqtly && !window.sliqtly.user();
  const note = cloudList.error
    ? t("The presentations in your cloud could not be read: ") + cloudList.error
    : signedOut ? t("Sign in (PRO) to see the presentations in your cloud, such as those made by an assistant.") : "";
  const rows = decksRoom ? await roomWindowRows(decksRoom.room, fresh) : await allDocs(fresh);
  const inRoom = decksRoom ? t("Room: ") + decksRoom.title : "";
  return deckListJson(rows, decksSort.by, t, [inRoom, note].filter(Boolean).join(" · "), decksSort.dir);
}
async function openDecks() {
  if (!vfs) return;
  app.openDecks(await decksJson());
  needsPaint = true;
  // the cloud read again: a deck made elsewhere since shows at once
  if (window.sliqtly?.user?.()) refreshDecks(true).catch(() => {});
}
async function refreshDecks(fresh = false) {
  if (!vfs || !app.decksShowing()) return;
  app.setDecks(await decksJson(fresh));
  needsPaint = true;
}
async function decksRequest(r) {
  const [, action, ...rest] = r.split(":");
  const what = rest.join(":");
  if (action === "sort") {
    decksSort = nextSort(decksSort, what);
    try { localStorage.setItem("sliqtly.decksSort", decksSort.by + ":" + decksSort.dir); } catch (_) { /* this session only */ }
  } else if (action === "del") {
    // a deck kept here, not the open one; one only in the cloud is deleted
    // once open (File → Delete presentation…)
    if (what !== doc.id && !what.startsWith("cloud:")) await vfs.deleteDoc(what);
  }
  await refreshDecks();
  refreshRecent().catch(() => {});
}

// File → Recent: the decks edited last, the open one left out: this
// browser's, and signed in, the cloud's too.
let recentSynced = "";
async function refreshRecent() {
  if (!vfs || viewer) return;
  const rows = sortRows(await allDocs())
    .filter((d) => d.id !== doc.id)
    .slice(0, 8)
    .map((d) => d.id + "\t" + String(d.name || "presentation").replace(/[\t\n\r]+/g, " "));
  const key = rows.join("\n");
  if (key === recentSynced) return;
  recentSynced = key;
  app.setToolbarOptions("recent", key, "");
}

// The files tab's list, when it shows; again when PRO signs in or out.
let filesListing = false;
window.addEventListener("sliqtly:user", () => refreshFiles());
async function refreshFiles() {
  refreshRecent().catch(() => {});
  refreshDecks().catch(() => {});
  if (app.editorTab() !== "files" || filesListing) return;
  filesListing = true;
  try {
    // no store in this browser (it did not open, or gave up on a call):
    // said, never "Loading files…" for good
    if (!vfs) throw new Error(t("this browser's storage did not open"));
    const all = await docFiles();
    const again = await retraceable(all);
    const take = all.find((f) => f.path === REC_JSON);
    const files = all
      // the recording is one row (its sound goes with it)
      .filter((f) => !(take && f.path.startsWith("recordings/take.") && f.path !== REC_JSON))
      .map((f) => {
        if (f === take) {
          const audio = all.find((x) => x.path === app.recordingAudio());
          return {
            path: f.path, kind: "recording", title: t("Recording"),
            size: (f.size || 0) + (audio ? audio.size || 0 : 0),
            note: clockText(app.recordingPlayLength()) + " · " + (audio ? t("with voice") : t("no sound")),
          };
        }
        const row = { path: f.path, size: f.size == null ? -1 : f.size, kind: kindOf(f.path, f.type) };
        // a copy kept of a linked source: named after it, with where and when it was read
        if (f.path.startsWith("data/live/")) {
          const source = copySource(f);
          let where = t("Linked data");
          if (source) where = isSheet(source) ? t("Google Sheets") : (() => { try { return new URL(source).host; } catch (_) { return source; } })();
          Object.assign(row, {
            kind: "live", source, tag: source && isSheet(source) ? "SHEET" : "LIVE",
            title: f.title || liveTitle(source) || f.path.split("/").pop(),
            note: where + " · " + t("read ") + whenText(liveRead.get(f.path) || f.read || f.updated),
          });
        }
        // a picture's pixels, for the preview beside the row
        const img = row.kind === "image" ? pictures.get("/" + f.path) : null;
        if (img) Object.assign(row, { w: img.naturalWidth, h: img.naturalHeight });
        if (again.has(f.path)) row.retrace = true;
        return row;
      });
    const sorted = sortFiles(files);
    const name = (docName || "presentation").replace(/\s+/g, "-");
    const head = [{ path: name + ".md", size: new TextEncoder().encode(app.source()).length, kind: "md" }];
    const key = themeSel.value || "";
    head.push({ path: (key || "theme") + ".css", size: -1, kind: "css" });
    let note = vfs.persistent
      ? t("Files live only in this browser (IndexedDB). Share links carry only the text and theme, not images or data files.")
      : t("This browser does not allow storage: files are kept only while this page is open.");
    // PRO (sliqtly.js): the files in the cloud, offered at the top
    const promo = !window.sliqtly ? null : window.sliqtly.user()
      ? { title: t("PRO is active"), text: t("Your presentations and their pictures and data are saved in the cloud as you work."), button: "" }
      : {
        title: t("Share images and data with PRO"),
        text: t("PRO keeps your decks and their files in the cloud. Share links then carry images, plus the CSV and JSON data behind your charts and tables."),
        button: t("Sign in with Google"),
      };
    // PRO: the deck and its files live in the cloud share, and go with its links
    if (signedIn()) {
      note = doc.cloudHalt
        ? t("This presentation was changed elsewhere, so it is not saved to the cloud now. A copy stays in this browser.")
        : cloudError && cloudNoRight
          ? cloudError
          : cloudError
          ? t("Saving to the cloud failed: ") + cloudError + ". " + t("It is tried again on the next change; a copy stays in this browser.")
          : t("PRO: this presentation and its files are saved to your cloud and go with share links. A copy stays in this browser.");
    }
    if (!doc.persisted) note = t("This presentation is not saved yet: it saves when you change it. ") + note;
    const clip = fileClip()?.note();
    app.setFileList(JSON.stringify({
      doc: exportName(), files: head.concat(sorted), note,
      ...(promo ? { promo } : {}), ...(clip ? { clip: { count: clip.count, from: clip.from } } : {}),
    }));
    needsPaint = true;
  } catch (e) {
    console.warn("listing the files failed", e);
    app.setFileList(JSON.stringify({
      doc: exportName(), files: [],
      note: t("The files of this presentation could not be read: ") + (e?.message || e) + ". " + t("Reload the page to try again."),
    }));
    needsPaint = true;
  } finally {
    filesListing = false;
  }
}

// `ask`: a dropped data file opens the import dialog; one added in the files
// tab is only kept.
async function addDocFile(file, ask = false) {
  const type = file.type || "";
  if (/\.(md|markdown)$/i.test(file.name)) {
    await leaveDoc();
    const text = await file.text();
    beginDoc(text);
    docName = file.name.replace(/\.(md|markdown)$/i, "") || "presentation";
    shownDoc(text);
    dropThumbs();
    needsPaint = true;
    return;
  }
  if (kindOf(file.name, type) === "image") {
    const bytes = await file.arrayBuffer();
    const path = placeFor(file.name, type);
    await addPicture("/" + path, bytes, type);
    const kept = type || (isSmartArt(type, file.name) ? SMARTART_TYPE : "image/png");
    await keepFile({ path, type: kept, size: bytes.byteLength, data: new Blob([bytes], { type: kept }) });
    return;
  }
  if ((ask || /\.xlsx$/i.test(file.name)) && /\.(csv|json|xlsx)$/i.test(file.name) && (await importData(file, ask))) return;
  const text = await file.text();
  let path = placeFor(file.name, type);
  if (/\.json$/i.test(file.name) && /vega\.github\.io\/schema\/vega/.test(text)) path = "charts/" + file.name;
  await keepFile({ path, type: type || "text/plain", size: text.length, data: text });
  chartFiles.set(path, Promise.resolve(text));
  app.setChartData(path, text);
  dropThumbs();
}

// --- data files ------------------------------------------------------------------
// A .csv, .json or .xlsx opens the import dialog (PresImport): a chart, a
// table or just the file. Nothing is kept until the dialog says which sheet
// (`data-keep:<i>`). A workbook is read by its own bundle, loaded the first
// time one arrives.
let importing = null;
let presData = null;
function loadPresData() {
  if (!presData) {
    presData = embeddedScriptUrl("./pres_data.js").then((own) => new Promise((ok, bad) => {
      const s = document.createElement("script");
      s.src = own || "./pres_data.js?v=" + BUILD;
      s.onload = () => ok(globalThis.PresData);
      s.onerror = () => { presData = null; bad(new Error("pres_data.js did not load")); };
      document.head.appendChild(s);
    }));
  }
  return presData;
}

// A sheet's CSV as a chart reads it: title rows above the header dropped,
// and a formula's 59.699999999999996 written as 59.7.
function tidyCsv(csv) {
  const lines = csv.split("\n");
  const filled = (l) => l.split(",").filter((c) => c.trim() !== "").length;
  let top = 0;
  while (top < lines.length - 1 && filled(lines[top]) < 2 && lines.slice(top + 1).some((l) => filled(l) >= 2)) top++;
  return lines.slice(top).join("\n")
    .replace(/(^|,)(-?\d+\.\d{12,})(?=,|$)/gm, (m, a, n) => a + String(+Number(n).toPrecision(12)));
}

// JSON rows as CSV: an array of objects, or the first array inside an object.
function jsonCsv(text) {
  let j;
  try { j = JSON.parse(text); } catch { return null; }
  if (!Array.isArray(j) && j && typeof j === "object") j = Object.values(j).find(Array.isArray);
  if (!Array.isArray(j) || !j.length || typeof j[0] !== "object" || j[0] === null) return null;
  const cols = [];
  for (const row of j.slice(0, 200)) for (const k of Object.keys(row || {})) if (!cols.includes(k)) cols.push(k);
  const cell = (v) => {
    const s = v == null ? "" : typeof v === "object" ? JSON.stringify(v) : String(v);
    return /[",\n\r]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
  };
  return [cols.map(cell).join(","), ...j.map((row) => cols.map((k) => cell(row && row[k])).join(","))].join("\n") + "\n";
}

// False when the file is not data after all (a Vega-Lite spec, say).
async function importData(file, ask) {
  const base = file.name.replace(/\.[^.]+$/, "").replace(/[\\/:*?"<>|]+/g, "-");
  let sheets;
  let xlsx = null;
  if (/\.xlsx$/i.test(file.name)) {
    const PresData = await loadPresData();
    const bytes = await file.arrayBuffer();
    xlsx = { path: placeFor(file.name, XLSX_MIME), bytes: bytes.slice(0), kept: false };
    const got = workbookSheets(xlsx.path, PresData, bytes);
    if (got.error) {
      toast(t("Could not read the workbook: ") + got.error);
      return true;
    }
    sheets = got.sheets;
  } else {
    const text = await file.text();
    let csv = text;
    if (/\.json$/i.test(file.name)) {
      if (/vega\.github\.io\/schema\/vega/.test(text)) return false;
      csv = jsonCsv(text);
      if (csv == null) return false;
    }
    sheets = [{ name: file.name, path: placeFor(file.name, file.type || ""), csv, text }];
  }
  if (!sheets.length) {
    toast(t("No rows in this file."));
    return true;
  }
  importing = { sheets, xlsx };
  if (!ask) {
    for (let i = 0; i < sheets.length; i++) await keepData(i);
    return true;
  }
  app.openImport(JSON.stringify({ name: file.name, xlsx: xlsx ? xlsx.path : "", sheets: sheets.map(({ name, path, csv }) => ({ name, path, csv })) }));
  needsPaint = true;
  return true;
}

// A Google Sheet's link, or the address of a CSV / TSV / JSON file, pasted on
// its own into the editor: the import dialog, as "Link live data", asks
// whether it becomes a chart or a table that reads it live (or is only
// pasted as text). Live data is PRO (liveAllowed).
function dataLink(text) {
  const u = text.trim();
  if (!/^https:\/\/\S+$/.test(u)) return false;
  return /^https:\/\/docs\.google\.com\/spreadsheets\/d\//.test(u) || /\.(csv|tsv|json)([?#]|$)/i.test(u);
}
async function linkData(link) {
  if (!liveAllowed()) {
    toast(t("Live chart data is a PRO feature: sign in with PRO to fetch it."));
    app.pasteText(link);
    afterInput();
    return;
  }
  const url = app.liveUrl(link);
  if (isSheet(url)) {
    // asked first; read while the card is up, so a sheet shared by link needs
    // no Google window and a private one gets it from the press
    let open = null;
    const pub = fetchLive(url).then((x) => (open = x), () => null);
    const go = await pressCard("gLink", t("Link this Google Sheet directly to this document? Its data is read again each time the deck opens."),
      t("Link the sheet"), t("Paste as text"),
      () => {
        if (open == null && window.sliqtly?.user?.() && !window.sliqtly.sheetsToken()) {
          // a closed window cancels; a blocked one leaves it to googleTap
          return window.sliqtly.askSheets().then(() => true, (e) => e?.code === "auth/popup-blocked");
        }
        return true;
      });
    if (!go) {
      app.pasteText(link);
      afterInput();
      return;
    }
    await pub;
  }
  let text;
  try {
    text = await readLive(url, true);
  } catch (e) {
    if (e?.code === "picker") toast(t("Google's file picker did not open. The site's Google API key must allow the Google Picker API."));
    else if (e?.code === "auth/popup-blocked") toast(t("The browser blocked Google's sign-in window: allow pop-ups for this site and paste the link again."));
    else if (e?.code !== "auth/popup-closed-by-user" && e?.code !== "auth/cancelled-popup-request") liveFailed(url);
    return;
  }
  let csv = text;
  if (/^\s*[[{]/.test(text)) {
    csv = jsonCsv(text);
    if (csv == null) {
      toast(t("No rows in this file."));
      return;
    }
  } else if (/\.tsv([?#]|$)/i.test(link)) {
    csv = text.split("\n").map((l) => l.split("\t").map((c) => /[",]/.test(c) ? `"${c.replace(/"/g, '""')}"` : c).join(",")).join("\n");
  }
  csv = tidyCsv(csv);
  const name = /docs\.google\.com/.test(link) ? "Google Sheet" : decodeURIComponent(link.split(/[?#]/)[0].split("/").pop() || link);
  importing = { live: true, url, sheets: [{ name, path: link, csv, text }] };
  app.openImport(JSON.stringify({ name, live: true, sheets: [{ name, path: link, csv }] }));
  needsPaint = true;
}

async function keepData(i) {
  const sh = importing && importing.sheets[i];
  if (!sh) return;
  // a link read live: drawn now, never kept with the deck
  if (importing.live) {
    chartFiles.set(importing.url, Promise.resolve(sh.text));
    app.setChartData(importing.url, sh.text);
    showLiveButton();
    dropThumbs();
    needsPaint = true;
    return;
  }
  // The workbook itself too, once: a live sheet (```sheet) opens it, and the
  // files tab edits it.
  const wb = importing.xlsx;
  if (wb) {
    // only the workbook is a file; its sheets are read from it
    if (!wb.kept) {
      wb.kept = true;
      await keepFile({ path: wb.path, type: XLSX_MIME, size: wb.bytes.byteLength, data: new Blob([wb.bytes], { type: XLSX_MIME }) });
    }
    for (const one of importing.sheets) {
      for (const n of one.names) {
        chartFiles.set(n, Promise.resolve(one.text));
        app.setChartData(n, one.text);
      }
    }
    await saveDoc(true);
    dropThumbs();
    refreshFiles();
    needsPaint = true;
    return;
  }
  await keepFile({ path: sh.path, type: /\.json$/i.test(sh.path) ? "application/json" : "text/csv", size: sh.text.length, data: sh.text });
  chartFiles.set(sh.path, Promise.resolve(sh.text));
  app.setChartData(sh.path, sh.text);
  await saveDoc(true);
  dropThumbs();
  refreshFiles();
  needsPaint = true;
}

async function fileRequest(r) {
  const [action, ...rest] = r.split(":");
  const what = rest.join(":");
  if (action === "add") {
    addAsks = false;
    fileAdd.click();
  } else if (action === "promo") {
    document.getElementById("pro")?.click();
  } else if (action === "new") {
    roomForNew = "";
    // asked first: an accidental press is cancelled and the deck stays
    app.openNewDeck(selectRows(themeSel), themeSel.value || "", "");
  } else if (action === "newsheet") {
    await newSheet();
  } else if (action === "duplicate") {
    // the copy's name asked first, as for a new deck
    app.openDupDeck(copyName());
  } else if (action === "open") {
    const f = (await docFiles()).find((x) => x.path === what);
    if (!f) return;
    if (/\.xlsx$/i.test(f.path)) {
      const blob = typeof f.data === "string" ? new Blob([f.data]) : f.data;
      liveSheets.openDialog({
        name: f.path.split("/").pop(),
        bytes: await blob.arrayBuffer(),
        onSave: (raw) => saveWorkbook(f.path, raw),
        onClose: () => { keys.focus({ preventScroll: true }); needsPaint = true; },
      }).catch((e) => toast(t("The spreadsheet editor did not load: ") + (e.message || e)));
      return;
    }
    const text = typeof f.data === "string" ? f.data : (isText(f.path, f.type) ? await f.data.text() : null);
    if (text == null) { toast(t("This file cannot be opened as text.")); return; }
    // a CSV opens in the spreadsheet editor; Save writes it back as CSV
    if (/\.csv$/i.test(f.path)) {
      liveSheets.openDialog({
        name: f.path.split("/").pop(),
        csv: text,
        onSave: (raw) => saveWorkbook(f.path, raw),
        onClose: () => { keys.focus({ preventScroll: true }); needsPaint = true; },
      }).catch((e) => {
        // no spreadsheet editor (offline): the text, as before
        toast(t("The spreadsheet editor did not load: ") + (e.message || e));
        app.openFile(f.path, text);
      });
      return;
    }
    app.openFile(f.path, text);
  } else if (action === "source" || action === "refresh" || action === "unlink") {
    const f = (await docFiles()).find((x) => x.path === what);
    const url = f ? copySource(f) : "";
    if (action === "source" && url) window.open(sourcePage(url), "_blank", "noopener");
    else if (action === "refresh" && url) await refreshLive([url]);
    else if (action === "unlink" && f) await unlinkLive(f, url);
  } else if (action === "imgedit") {
    if (isSvg("", what)) await retraceSvg(what);
    else await openImageEditor(what);
    return;
  } else if (action === "recplay") {
    if (app.hasRecording()) app.replayStart(0);
  } else if (action === "recedit") {
    app.recEditOpen(true);
  } else if (action === "del" && what === REC_JSON) {
    await deleteRecording();
  } else if (action === "del") {
    pending.delete(what);
    if (doc.persisted) await vfs.deleteFile(doc.id, what);
    if (app.openFilePath() === what) app.closeFile();
    cloudSoon();
  } else if (action === "copy") {
    await copyFiles(what.split("\n"));
  } else if (action === "delmany") {
    await deleteFiles(what.split("\n"));
  } else if (action === "paste") {
    await pasteFiles();
  } else if (action === "doc") {
    const opened = await loadingScreen(() => what.startsWith("cloud:")
      ? openOwnCloud(what.slice(6)).catch((e) => { console.warn(e); return false; })
      : openDoc(what));
    if (!opened) toast(t("Presentation not found."));
  } else if (action === "deletedeck") {
    // asked first, in the app's own window; "confirm:deletedeck" deletes
    const cloud = !!doc.cloud && !!window.sliqtly?.user?.();
    app.openConfirm("deletedeck", t("Delete presentation"),
      t("Delete “") + exportName() + t("”? It is removed from this browser") +
      (cloud ? t(" and from the cloud, and its share link stops working") : "") +
      t(". This cannot be undone."), t("Delete"));
  }
  refreshFiles();
  needsPaint = true;
}

window.__fileRequest = (r) => fileRequest(r);

// --- Copy and Paste between presentations (web/fileclip.js) ----------------------
// The ticked files are copied to this browser's clipboard store; Paste in
// any deck, in this tab or another, adds them there. A name the deck already
// has gets -2, -3…; a file it already has as it is stays as it is.
let clipboard = null;
function fileClip() {
  if (!vfs) return null;
  if (!clipboard) {
    let storage = null;
    try { storage = mine; } catch (_) { /* this page only */ }
    clipboard = fileClipboard(vfs, storage);
  }
  return clipboard;
}
// another tab copied: its Paste shows here too
window.addEventListener("storage", (e) => { if (e.key !== null && mine.own(e.key) === CLIP_KEY) refreshFiles(); });

async function copyFiles(paths) {
  const clip = fileClip();
  if (!clip) return;
  const want = new Set(paths);
  const files = (await docFiles()).filter((f) => want.has(f.path));
  if (!files.length) return;
  await clip.copy(files, exportName());
  toast(files.length === 1
    ? t("Copied 1 file: open another presentation and press Paste in Files.")
    : t("Copied ") + files.length + t(" files: open another presentation and press Paste in Files."));
}

async function deleteFiles(paths) {
  const files = await docFiles();
  for (const path of paths) {
    const f = files.find((x) => x.path === path);
    if (!f) continue;
    pending.delete(path);
    if (doc.persisted) await vfs.deleteFile(doc.id, path);
    if (path.startsWith("data/live/")) liveCopies.delete(path);
    if (app.openFilePath() === path) app.closeFile();
  }
  if (paths.includes(REC_JSON)) {
    await dropTakeFiles([]);
    app.dropRecording();
  }
  cloudSoon();
  dropThumbs();
  toast(paths.length === 1 ? t("Deleted 1 file.") : t("Deleted ") + paths.length + t(" files."));
}

async function pasteFiles() {
  const clip = fileClip();
  if (!clip || viewer) return;
  const copies = await clip.files();
  if (!copies.length) { toast(t("Nothing to paste: copy files in another presentation first.")); return; }
  const plan = await pastePlan(copies, await docFiles());
  let added = 0;
  let renamed = 0;
  for (let i = 0; i < plan.length; i++) {
    const step = plan[i];
    if (step.same) continue;
    const { doc: _d, updated: _u, ...rec } = copies[i];
    const path = step.to;
    if (kindOf(path, rec.type) === "image") {
      const blob = typeof rec.data === "string" ? new Blob([rec.data], { type: rec.type || "" }) : rec.data;
      await addPicture("/" + path, await blob.arrayBuffer(), rec.type || "");
    } else if (typeof rec.data === "string") {
      chartFiles.set(path, Promise.resolve(rec.data));
      app.setChartData(path, rec.data);
      if (path.startsWith("data/live/")) liveCopies.set(path, rec.data);
    }
    await keepFile({ ...rec, path });
    added += 1;
    if (path !== step.from) renamed += 1;
  }
  dropThumbs();
  const left = plan.length - added;
  if (!added) { toast(t("These files are here already.")); return; }
  let msg = added === 1 ? t("Pasted 1 file.") : t("Pasted ") + added + t(" files.");
  if (renamed) msg += " " + renamed + t(" got a new name, as the names were taken.");
  if (left) msg += " " + left + t(" were here already.");
  toast(msg);
}
window.__openDecks = () => openDecks();

// A file edited in the files tab: kept, and handed to the charts again.
async function saveOpenFile(path) {
  const text = app.fileText();
  const f = (await docFiles()).find((x) => x.path === path);
  await keepFile({ path, type: (f && f.type) || "text/plain", size: text.length, data: text });
  chartFiles.set(path, Promise.resolve(text));
  app.setChartData(path, text);
  dropThumbs();
  needsPaint = true;
}

// --- painting -------------------------------------------------------------------
// Thumbnails are the slides at rest, so they are built once per deck revision
// and kept; the stage and the chrome are built every paint. A kept frame is
// only good while the glyph atlas it was built against stands, so a paint
// that grows the atlas drops the kept ones and draws again.
// File → Settings: automatic contrast correction, on unless turned off here.
let autoContrast = true;
try { autoContrast = localStorage.getItem("sliqtly.autoContrast") !== "off"; } catch (_) { /* on */ }

// --- review mode: comments pinned to the slides (src/PresReviewUi.rgr) ----------------
// File → Settings turns it on or off; without a choice made it is on where
// the page is served by a server of one's own (a team's review tool) and off
// on the site. The comments are the deck's file review/comments.json
// (src/PresReview.rgr); a copy changed elsewhere (another person, an
// assistant through MCP) is united with this one, not put in its place.
const REVIEW_PATH = "review/comments.json";
const REVIEW_KEY = "sliqtly.review";
function reviewChoice() {
  try { return localStorage.getItem(REVIEW_KEY); } catch (_) { return null; }
}
function ownServer() {
  return typeof window.sliqtly?.serverVersion === "function";
}
function applyReviewMode() {
  const choice = reviewChoice();
  const on = !viewer && !versionFrame && (choice ? choice === "on" : ownServer());
  if (app.reviewMode() !== on) {
    app.setReviewMode(on);
    document.body.classList.toggle("reviewing", on);
    needsPaint = true;
  }
}
function reviewMe() {
  const u = window.sliqtly?.user?.();
  // on a server of one's own everyone is its one user: the name chosen for
  // editing together says who wrote what
  const name = collabOn() || ownServer() ? shownName() : (u?.displayName || shownName());
  const color = collabOn() ? collab.me.color : collabMe.color;
  app.reviewSetMe(collabMe.who, name, color, collabMe.client);
}
// the deck's comments into the review; `merge` unites them with what is here
async function loadReview(merge) {
  const which = doc.id;
  const blob = await readDocFile(REVIEW_PATH);
  if (doc.id !== which) return;
  const text = blob ? await blob.text() : "";
  if (!merge || text) app.reviewLoad(text, merge);
  needsPaint = true;
}
async function keepReview() {
  const text = app.reviewFile();
  await keepFile({ path: REVIEW_PATH, type: "application/json", size: new Blob([text]).size, data: text });
}
// The share's comments as they are there (changed by someone else, or by an
// assistant): united with the ones here. When the two differ, the united
// file is what goes up next.
async function takeCloudReview(s) {
  const f = (s.files || []).find((x) => x.path === REVIEW_PATH);
  if (!f || !(await docFiles()).some((x) => x.path === REVIEW_PATH)) return false;
  let text = "";
  try {
    const res = await fetch(f.url, { cache: "no-store" });
    if (!res.ok) return false;
    text = await res.text();
  } catch (_) {
    return false;
  }
  const before = app.reviewFile();
  app.reviewLoad(text, true);
  const after = app.reviewFile();
  if (after !== before) await keepReview();
  const push = after !== text;
  if (push) doc.cloudStamps.delete(REVIEW_PATH);
  needsPaint = true;
  // true when the comments here changed or theirs need ours
  return after !== before || push;
}
// The editor's skin (File → Settings → Look): "" or "retro", and the skin's
// base colour as a hue (its sheets' --retro-hue), per browser.
let skin = "";
let skinHue = 88;
// The standard skin's colours: "light" (the default), "dark" or "system"
// (the device's setting, followed as it changes), per browser. The slides
// keep their own theme in both.
let mode = "light";
try {
  skin = localStorage.getItem("sliqtly.skin") === "retro" ? "retro" : "";
  const h = parseInt(localStorage.getItem("sliqtly.skinHue") || "", 10);
  if (h >= 0 && h < 360) skinHue = h;
  const m = localStorage.getItem("sliqtly.mode");
  if (m === "dark" || m === "system") mode = m;
} catch (_) { /* standard */ }
const darkQuery = window.matchMedia ? window.matchMedia("(prefers-color-scheme: dark)") : null;
function isDark() {
  return mode === "dark" || (mode === "system" && !!(darkQuery && darkQuery.matches));
}
// the theme name every chrome host is applied with: "", "dark" or "retro"
function lookTheme() {
  if (skin === "retro") return "retro";
  return isDark() ? "dark" : "";
}
// Every chrome sheet without the skins, as loaded (start), and the skins'
// own text: the skins go after each sheet with the chosen hue at the end.
const chromeSheets = { files: "", chrome: null, chart: null, hint: null, panels: null, toolbar: null };
// The dark look of a sheet is derived from it (EVGUI's UiDark, through
// PresApp.darkCss): every colour it sets, scoped to .theme-dark and mapped so
// each contrast stays what it was. Worked out once per sheet and host theme,
// and only while the dark look is on.
const darkOverlays = new Map();
function darkOverlay(base, theme) {
  if (lookTheme() !== "dark" || !base) return "";
  const key = theme + "\n" + base;
  let o = darkOverlays.get(key);
  if (o == null) {
    o = PresApp.darkCss(base, theme);
    darkOverlays.set(key, o);
  }
  return o;
}
// What goes after a host's sheet: its dark overlay (dark look only), then
// the skins (retro, and the dark look's hand-set colours), then the hue.
function skinCss(base, theme) {
  if (!chromeSheets.files) return "";
  return "\n" + darkOverlay(base, theme) + chromeSheets.files + "\n@vars retro { --retro-hue: " + skinHue + "; }\n";
}
function sendChromeCss() {
  const c = chromeSheets;
  if (c.chart != null) app.setChartCss(c.chart + skinCss(c.chart, "ce"));
  if (c.hint != null) app.setHintCss(c.hint + skinCss(c.hint, "hp"));
  if (c.panels != null) app.setPanelsCss(c.panels + skinCss(c.panels, "pn"));
  if (c.toolbar != null) app.setToolbarCss(c.toolbar + skinCss(c.toolbar, "tb"));
}
function sendAllChromeCss() {
  if (chromeSheets.chrome != null) app.setChromeCss(chromeSheets.chrome + skinCss(chromeSheets.chrome, ""));
  sendChromeCss();
}
function setSkinHue(h) {
  if (!(h >= 0 && h < 360)) return;
  skinHue = h;
  try { localStorage.setItem("sliqtly.skinHue", String(h)); } catch (_) { /* this session only */ }
  sendAllChromeCss();
  applySkin();
  needsPaint = true;
}
// the sheets carry the dark overlay only under the dark look: sent again
// when a change turns it on or off
function relook(change) {
  const before = lookTheme();
  change();
  if (lookTheme() !== before && chromeSheets.chrome != null) sendAllChromeCss();
  applySkin();
  needsPaint = true;
}
function setSkinName(name) {
  relook(() => {
    skin = name === "retro" ? "retro" : "";
    try { localStorage.setItem("sliqtly.skin", skin || "standard"); } catch (_) { /* this session only */ }
  });
}
function setMode(m) {
  relook(() => {
    mode = m === "light" || m === "dark" ? m : "system";
    try { localStorage.setItem("sliqtly.mode", mode); } catch (_) { /* this session only */ }
  });
}
// the bar's quick switch: to the other one of light and dark (from the
// retro skin, to the standard one in light)
function toggleMode() {
  const dark = skin !== "retro" && !isDark();
  if (skin === "retro") setSkinName("");
  setMode(dark ? "dark" : "light");
}
darkQuery?.addEventListener?.("change", () => { if (mode === "system") relook(() => {}); });
window.__skin = { set: setSkinName, hue: setSkinHue, mode: setMode };
function applySkin() {
  app.setSkin(lookTheme(), skinHue);
  app.setLookMode(mode);
  const root = document.documentElement;
  root.dataset.skin = skin || "standard";
  root.dataset.mode = lookTheme() === "dark" ? "dark" : "light";
  root.style.setProperty("--retro-hue", String(skinHue));
  // the rail's switch shows Light while the editor is dark
  app.setToolbarOptions("dark", "", lookTheme() !== "" ? "1" : "");
  const btn = document.getElementById("modeBtn");
  if (btn) {
    const dark = lookTheme() !== "";
    const label = dark ? "☀️" : "🌙";
    if (btn.textContent !== label) btn.textContent = label;
    btn.title = dark ? t("Light editor") : t("Dark editor");
  }
}

let thumbs = new Map();
let thumbRev = -1;
// the slide lists of thumbnails cut by the strip's edge, as JSON text
let thumbDocs = new Map();
let thumbDocsRev = -1;
function dropThumbs() {
  for (const f of thumbs.values()) f.dispose();
  thumbs = new Map();
}

// AN EFFECT'S CLOCK. Playing or presenting: the app's `fxTime`, the time the
// slide has been on screen, which goes on while a step or the slide's end
// waits for a click (the deck clock stops there), so rain keeps falling while
// the speaker talks. A replay is the same picture. Editing: each effect's own
// clock, started when it first showed with the settings it has now, so a
// changed setting is seen from the start (rain on a dry pane) and typing
// elsewhere does not restart it.
const fxSince = new Map();
function effectClock(layout) {
  if (layout.playing && layout.fxTime >= 0) return () => layout.fxTime;
  const now = performance.now();
  return (e) => {
    const sig = e.kind + " " + JSON.stringify(e.p || {});
    let at = fxSince.get(e.id);
    if (!at || at.sig !== sig) {
      at = { sig, now };
      fxSince.set(e.id, at);
    }
    return (now - at.now) / 1000;
  };
}

// `t` a time for every effect, or a function of the effect.
function withTime(doc, t) {
  const fx = doc.list && doc.list.effects;
  if (fx) for (const e of fx) e.time = typeof t === "function" ? t(e) : t;
  return doc;
}

// The moment a thumbnail and an exported still show an effect at. Rain is
// drawn half a minute in, when it has landed, run and left its trails; the
// rest two seconds in, as they always were.
const FX_STILL_T = { drops: 30, raindrops2: 30 };
function atRest(doc) {
  return withTime(doc, (e) => FX_STILL_T[e.kind] ?? 2.0);
}

// --- a realistic book, presented ------------------------------------------------
// `mode: book` with `render: realistic`: while presenting, the spread is
// drawn as paper (web/bookgl.js, in this canvas's own context between the
// chrome and what goes over the slide) and a page turns by its corner in 3D
// (web/bookturn.js). The app still says which spread is open
// (layoutJson's "book"); a change of one spread turns the page there, and
// a page let go over the spine moves the app on.
let bookGl = null;
let bookTurn = null;
// the spread drawn last, -1: whatever the app has, without a turn
let bookShown = -1;
let bookPlace = null;
let bookPageGl = null;
const bookPageCanvas = document.createElement("canvas");

// Page `page` drawn by EVG into a picture as sharp as the screen shows it,
// kept as its texture until the deck changes (a page whose pictures have
// not all arrived is drawn again on the next paint).
function bookPage(page, b, pxW, rev) {
  const key = rev + ":" + pxW;
  if (bookGl.hasPage(page, key)) return;
  if (!bookPageGl) bookPageGl = bookPageCanvas.getContext("webgl2", { antialias: true, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
  if (!bookPageGl) return;
  const k = pxW / b.w;
  bookPageCanvas.width = pxW;
  bookPageCanvas.height = Math.round(b.h * k);
  const doc = atRest(JSON.parse(app.slideJson(page)));
  doc.width = b.w;
  doc.height = b.h;
  const f = prepareDisplayList(bookPageGl, doc, { dpr: k, images: pictures, contrastGuard: true, contrastRepair: autoContrast });
  f.draw(null, null);
  f.dispose();
  const waiting = (doc.list.cmds || []).some((c) => c.k === 2 && c.src && !pictures.has(c.src));
  bookGl.setPage(page, bookPageCanvas, waiting ? "" : key);
}

// --- the theme picker's pictures ------------------------------------------------
// Each theme's tile (Slide → Theme…, New presentation) shows a sample slide
// laid out in it (PresApp.themeSampleJson), drawn by EVG into a canvas of
// its own once and kept among the pictures under themePicture(key). One a
// frame, so the window opens at once and fills in.
const THEME_PIC = { w: 110, h: 62, dpr: 2 };
const themePicDrawn = new Map();
let themePicQueue = [];
let themePicGl = null;
const themePicCanvas = document.createElement("canvas");
function queueThemePictures() {
  themePicQueue = picturesToDraw(selectRows(themeSel), themePicDrawn, lang);
  needsPaint = true;
}
function drawThemePicture() {
  const key = themePicQueue.shift();
  if (key === undefined) return;
  // the theme as it ships: the tile shows what picking it gives
  const text = app.themeSampleJson(key ? themeCss[key] || "" : "");
  if (!text) return;
  const doc = atRest(JSON.parse(text));
  const at = fitPage(doc.width, doc.height, THEME_PIC.w, THEME_PIC.h);
  const k = at.s * THEME_PIC.dpr;
  themePicCanvas.width = Math.max(1, Math.round(doc.width * k));
  themePicCanvas.height = Math.max(1, Math.round(doc.height * k));
  if (!themePicGl) themePicGl = themePicCanvas.getContext("webgl2", { antialias: true, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
  if (!themePicGl) return;
  const f = prepareDisplayList(themePicGl, doc, { dpr: k, images: pictures, contrastGuard: true, contrastRepair: autoContrast });
  f.draw(null, null);
  f.dispose();
  const c = document.createElement("canvas");
  c.width = THEME_PIC.w * THEME_PIC.dpr;
  c.height = THEME_PIC.h * THEME_PIC.dpr;
  const g = c.getContext("2d");
  g.fillStyle = "#e4e4e7";
  g.fillRect(0, 0, c.width, c.height);
  g.drawImage(themePicCanvas, Math.round(at.x * THEME_PIC.dpr), Math.round(at.y * THEME_PIC.dpr));
  pictures.set(themePicture(key), asPicture(c));
  themePicDrawn.set(themePicture(key), lang);
  needsPaint = true;
}
// how many are drawn (scripts/check-web.mjs)
window.__themePictures = () => themePicDrawn.size;

function paintBookSpread(layout) {
  const b = layout.book;
  if (!bookGl) bookGl = new BookGL(gl);
  const [sx, sy, sc] = layout.stage;
  const pxW = Math.min(2048, Math.max(256, Math.round(b.w * sc * dpr)));
  if (!bookTurn && bookShown >= 0 && Math.abs(b.spread - bookShown) === 1) {
    bookTurn = autoTurn(b.spreads, bookShown, b.spread - bookShown, b.w, b.h, null);
  }
  // the pages first: drawing one takes a while, and the turn's clock starts
  // once they are there
  for (const p of turnPages(b.spreads, bookTurn ? bookTurn.from : b.spread, bookTurn)) bookPage(p, b, pxW, layout.rev);
  if (bookTurn) {
    const state = stepTurn(bookTurn, performance.now(), b.h);
    if (state === "over" || state === "back") {
      const t = bookTurn;
      bookTurn = null;
      // a page let go over the spine: the app goes there (a step on the
      // page may hold it; whatever it opens is shown as it is)
      if (state === "over" && t.byHand) {
        if (t.side > 0) app.next();
        else app.prev();
        bookShown = -1;
        needsPaint = true;
        return;
      }
    }
  }
  const s = bookTurn ? bookTurn.from : b.spread;
  bookShown = bookTurn ? bookShown : b.spread;
  // the table round the book
  const [cx, cy, cw, ch] = layout.clip;
  gl.enable(gl.SCISSOR_TEST);
  gl.scissor(Math.round(cx * dpr), Math.round(canvas.height - (cy + ch) * dpr), Math.round(cw * dpr), Math.round(ch * dpr));
  gl.clearColor(0.17, 0.16, 0.15, 1);
  gl.clear(gl.COLOR_BUFFER_BIT);
  gl.disable(gl.SCISSOR_TEST);
  bookPlace = { spineX: sx + b.spine * sc, top: sy, scale: sc, b, s };
  bookGl.draw({ place: bookPlace, W: b.w, H: b.h, dpr, ...turnScene(b.spreads, s, bookTurn, b.w, b.h) });
  if (bookTurn) {
    needsPaint = true;
    return;
  }
  // lying open: the pages a turn either way shows, drawn ahead, one a
  // frame, so the turn starts at once
  for (const n of [s + 1, s - 1]) {
    if (n < 0 || n >= b.spreads.length) continue;
    const p = b.spreads[n].find((q) => q >= 0 && !bookGl.hasPage(q, layout.rev + ":" + pxW));
    if (p !== undefined) {
      bookPage(p, b, pxW, layout.rev);
      needsPaint = true;
      return;
    }
  }
}

function forgetBook() {
  bookTurn = null;
  bookShown = -1;
  bookPlace = null;
}

let lastLayout = null;
function paintOnce() {
  errEl.textContent = "";
  const layout = JSON.parse(app.layoutJson());
  lastLayout = layout;
  liveSheets.sync(layout);
  if (layout.rev !== thumbRev) {
    dropThumbs();
    thumbRev = layout.rev;
  }
  const clock = effectClock(layout);
  // THE GLYPH ATLAS IS SHARED, and when a frame needs glyphs it does not hold
  // it grows into a NEW texture and deletes the old one. A kept thumbnail
  // still points at the old one, so drawing it after that is
  // "bindTexture: attempt to use a deleted object". So: the chrome and the
  // stage are built first, and if either grew the atlas the kept thumbnails
  // are dropped before any is drawn; a thumbnail built now that grows it
  // makes the others stale for the NEXT paint, so they are dropped after.
  const grewBy = (stats) => !!(stats && (stats.atlasRebuilt || stats.atlasAdded > 0));
  let grew = false;
  // Slides and thumbnails are drawn with EVG's contrast guard: a run of text
  // that does not stand out from what is under it (WCAG 4.5:1, 3:1 for large
  // text) is drawn in a colour that reads, or, on big letters, with a thin
  // outline. File → Settings turns the correction off (the runs are still
  // listed for the editor's warnings). The chrome is drawn without it.
  const chrome = JSON.parse(app.chromeJson());
  window.__lastChrome = chrome;
  const cf = prepareDisplayList(gl, chrome, { dpr });
  grew = grewBy(cf.draw(null, null)) || grew;
  cf.dispose();
  if (layout.book) {
    window.__lastStage = null;
    paintBookSpread(layout);
  } else forgetBook();
  if (layout.slides > 0 && !layout.book) {
    const st = withTime(JSON.parse(app.stageJson()), clock);
    window.__lastStage = st;
    st.width = W;
    st.height = H;
    const sf = prepareDisplayList(gl, st, { dpr, images: pictures, contrastGuard: true, contrastRepair: autoContrast });
    const stageStats = sf.draw(null, [layout.stage[0], layout.stage[1], layout.stage[2]], { clear: false });
    grew = grewBy(stageStats) || grew;
    sf.dispose();
    // what is drawn on the slide and the pointer (presenting with the pen,
    // recording, a replay), in the slide's units like the stage
    const ij = app.inkJson();
    if (ij) {
      const ink = JSON.parse(ij);
      ink.width = W;
      ink.height = H;
      const inf = prepareDisplayList(gl, ink, { dpr });
      grew = grewBy(inf.draw(null, [layout.stage[0], layout.stage[1], layout.stage[2]], { clear: false })) || grew;
      inf.dispose();
    }
    // Only while editing: not to an audience, and not mid-animation, where a
    // fading line is briefly faint by design.
    if (layout.mode !== "present" && !viewer && !app.isPlaying()) {
      // Runs too faint on their slide: marked in the editors' gutters, not
      // laid over the slide. A change shows on the next paint.
      try {
        if (app.setContrast(JSON.stringify((stageStats && stageStats.lowContrast) || []))) needsPaint = true;
        // the image editor says whether the slide's text still reads
        if (adjusting) app.setAdjustContrast(JSON.stringify((stageStats && stageStats.lowContrast) || []));
      } catch (e) { console.warn("contrast notes", e); }
    }
  }
  // the element picked on the slide: its outline, spacing bands and buttons
  if (layout.slides > 0 && layout.mode !== "present") {
    const pj = app.pickJson();
    if (pj) {
      const pk = JSON.parse(pj);
      pk.width = W;
      pk.height = H;
      const pf = prepareDisplayList(gl, pk, { dpr });
      grew = grewBy(pf.draw(null, [0, 0, 1], { clear: false })) || grew;
      pf.dispose();
    }
  }
  if (grew) dropThumbs();
  let thumbsGrew = false;
  if (layout.rev !== thumbDocsRev) {
    thumbDocs = new Map();
    thumbDocsRev = layout.rev;
  }
  for (const [i, x, y, s, , cut] of layout.thumbs) {
    if (cut) {
      // cut by the strip's edge: drawn from its own list with a clip at the
      // edge, rebuilt each paint (one or two at a time)
      let text = thumbDocs.get(i);
      if (!text) {
        text = app.slideJson(i);
        thumbDocs.set(i, text);
      }
      const doc = atRest(JSON.parse(text));
      doc.width = W;
      doc.height = H;
      const [cx, cy, cw, ch] = layout.strip;
      const none = [0, 0, 0, 0];
      doc.list.cmds.unshift({ k: 4, x: (cx - x) / s, y: (cy - y) / s, w: cw / s, h: ch / s, c: none });
      doc.list.cmds.push({ k: 5, x: 0, y: 0, w: 0, h: 0, c: none });
      const cf = prepareDisplayList(gl, doc, { dpr, images: pictures, contrastGuard: true, contrastRepair: autoContrast });
      if (grewBy(cf.draw(null, [x, y, s], { clear: false }))) thumbsGrew = true;
      cf.dispose();
      continue;
    }
    let f = thumbs.get(i);
    const fresh = !f;
    if (fresh) {
      const doc = atRest(JSON.parse(app.slideJson(i)));
      doc.width = W;
      doc.height = H;
      f = prepareDisplayList(gl, doc, { dpr, images: pictures, contrastGuard: true, contrastRepair: autoContrast });
      thumbs.set(i, f);
    }
    const stats = f.draw(null, [x, y, s], { clear: false });
    if (fresh && grewBy(stats)) thumbsGrew = true;
  }
  if (thumbsGrew) dropThumbs();
  // the top bar, over the chrome; with a menu or a list open, over everything
  const paintBar = () => {
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
  };
  // The surfaces over the chrome, bottom first, in the order the app keeps
  // (PresApp.layerOrder, EVGUI's UiLayers): the bar and the docked help and
  // files panels, then the windows (the value popover, the chart editor; the
  // one opened or pressed last on top), the modal dialog, the bar's open
  // menu, the toast. A press goes to them in the same order, top first.
  const paintList = (j, images, at) => {
    if (!j) return;
    const doc = typeof j === "string" ? JSON.parse(j) : j;
    doc.width = W;
    doc.height = H;
    const f = prepareDisplayList(gl, doc, images ? { dpr, images } : { dpr });
    if (grewBy(f.draw(null, at ? at(doc) : [0, 0, 1], { clear: false }))) dropThumbs();
    f.dispose();
  };
  // the panels' three layers: with the pictures, the files tab previews the
  // one under the pointer
  const PANEL_PART = { panels: "docked", dialog: "dialog", toast: "toast" };
  for (const layer of app.layerOrder().split(",")) {
    if (layer === "bar") {
      if (canvasBar) paintBar();
    } else if (layer === "sketch") {
      paintList(app.sketchJson());
    } else if (layer === "room") {
      // the channel's pictures (attached ones, embedded slides) as they load
      const j = app.roomJson();
      if (j && roomChatOne) {
        const d = JSON.parse(j);
        roomChatOne.want((d.list?.cmds || []).filter((c) => c.k === 2 && c.src).map((c) => c.src));
        paintList(d, roomChatOne.pictures);
      } else paintList(j);
    } else if (layer === "review") {
      paintList(app.reviewJson());
    } else if (layer === "hint") {
      paintList(app.hintJson());
    } else if (layer === "chart") {
      // placed like a thumbnail: a page the size of the canvas, moved by the
      // camera; with the pictures: the image window shows the one being added
      paintList(app.chartJson(), pictures, (cj) => [cj.x, cj.y, 1]);
    } else if (PANEL_PART[layer]) {
      paintList(app.panelsPartJson(PANEL_PART[layer]), pictures);
    }
  }
  // only when they change: the bar's observer reads every write as new
  // buttons, and a frame that writes them asks for the next frame
  setText(statusEl, app.statusText());
  setText(playBtn, layout.playing && layout.mode === "edit" ? t("⏸ Pause") : t("▶ Play"));
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

// --- accessibility and the keyboard ------------------------------------------------
// The canvas is one empty graphic to a screen reader, so the app publishes what
// it shows as a tree (PresApp.a11yJson: the bar, the editor's tabs, the slides,
// the open popover, dialog or panel) and evg-a11y.js mirrors it as real DOM over
// the canvas: buttons a reader can find and press, and a keyboard order.
//
//   F6 / Shift+F6    the next / previous region: bar, editor tabs, editor,
//                    slides, the help panel (an open popover or dialog keeps
//                    the keyboard inside it)
//   Tab, arrows      from control to control inside a region
//   Enter, Space     press it (a reader's activation does the same)
//   arrows on a slider  move it
//   Esc              back to the editor (closing a popover or dialog first)
//   on a slide of the strip (a click on it puts the keyboard there):
//     arrows, Home, End    the slide before / after, the first, the last
//     Ctrl/⌘ + arrow       move the slide
//     Delete, Backspace    delete it (Ctrl/⌘+Z brings it back)
//     Shift+F10, menu key  its context menu (a right click opens it too)
//     Enter                its heading in the editor
//   Ctrl+Space       the value popover at the caret, with the keyboard in it
let lastA11yRev = "";
let a11yTree = null;
const mirror = createA11yMirror(stageEl, {
  canvas,
  label: APP_NAME + t(". F6 moves between areas, Esc returns to the editor."),
  tabbable: "all",
  onActivate: (node) => {
    pressAtCentre(node, (x, y) => {
      app.setCtrl(false);
      app.pointerDown(x, y, false, 1);
      app.pointerUp();
    });
    afterInput();
    needsPaint = true;
    paintOnce();
    mirrorA11y();
    // what was pressed may be gone (a popover that closed): the keyboard goes
    // back to the editor rather than to nowhere
    const pressedEl = mirror.elementOf(node.id);
    requestAnimationFrame(() => {
      const el = mirror.elementOf(node.id);
      const act = document.activeElement;
      // only when the keyboard is really lost: focus moved on since (another
      // shortcut, a click) is left alone
      const lost = !act || act === document.body || act === pressedEl;
      if (lost && (!el || !el.isConnected)) {
        const reg = regionOf(node.id);
        if (reg === "ctx" || !focusRegion(reg)) focusApp();
      }
    });
  },
  onFocus: (node) => app.setA11yFocus(node.id),
});
function mirrorA11y() {
  try {
    a11yTree = JSON.parse(app.a11yJson());
    a11yTree.byId = new Map(a11yTree.nodes.map((n) => [n.id, n]));
    mirror.update(a11yTree);
  } catch (e) {
    console.warn(e);
  }
}
function regionOf(id) {
  if (!id) return "";
  if (id.startsWith("tb-m-ctx")) return "ctx";
  if (id.startsWith("tb-")) return "bar";
  if (id.startsWith("edtabs")) return "tabs";
  if (id.startsWith("thumb-")) return "slides";
  if (id.startsWith("pn-")) return "panels";
  if (id.startsWith("hp-")) return "hint";
  if (id.startsWith("ce-")) return "chart";
  return "";
}
function focusables(region) {
  if (!a11yTree) return [];
  return a11yTree.nodes.filter((n) => n.focusable && !n.disabled && regionOf(n.id) === region && mirror.elementOf(n.id));
}
function focusNode(node) {
  app.setA11yFocus(node.id);
  const el = mirror.elementOf(node.id);
  if (el) el.focus({ preventScroll: true });
}
function focusRegion(region) {
  if (region === "editor") {
    if (JSON.parse(app.layoutJson()).mode === "present") return false;
    app.setA11yFocus("");
    focusKeys("editor");
    return true;
  }
  mirrorA11y();
  const list = focusables(region);
  if (!list.length) return false;
  focusNode(list.find((n) => n.selected) || list[0]);
  return true;
}
// The keyboard on the strip: on the selected slide's mirrored option, so a
// reader hears which slide it is and the keys go to the strip, not the editor.
function focusStrip() {
  mirrorA11y();
  const node = a11yTree && a11yTree.byId.get("thumb-" + app.selectedSlide());
  if (!node || !mirror.elementOf(node.id)) return false;
  focusNode(node);
  return true;
}
// The keyboard back where the app says it is (after a menu, Esc).
function focusApp() {
  const where = app.focusTarget();
  if (where === "strip" && focusStrip()) return;
  focusKeys(where === "stage" || where === "strip" ? where : "editor");
}
// The strip's context menu was opened: the keyboard goes to its first row.
function focusSlideMenu() {
  paintOnce();
  mirrorA11y();
  const first = a11yTree && a11yTree.nodes.find((n) => regionOf(n.id) === "ctx" && n.focusable && !n.disabled && /-item-/.test(n.id) && mirror.elementOf(n.id));
  if (first) focusNode(first);
}
const STRIP_KEYS = { ArrowLeft: "left", ArrowRight: "right", ArrowUp: "up", ArrowDown: "down", Home: "home", End: "end", Delete: "delete", Backspace: "backspace", Enter: "enter", Escape: "escape" };
// A key on a slide of the strip; true when it was the strip's.
function stripKey(ev) {
  const mod = ev.ctrlKey || ev.metaKey;
  if (ev.key === "ContextMenu" || (ev.shiftKey && ev.key === "F10")) {
    ev.preventDefault();
    if (app.slideMenuAtSelected()) {
      afterInput();
      focusSlideMenu();
    }
    return true;
  }
  const name = STRIP_KEYS[ev.key];
  const menuKey = !ev.altKey && !ev.shiftKey && ev.key.length === 1 ? ev.key.toLowerCase() : "";
  const req = ev.ctrlKey && !ev.metaKey && menuKey === "m" ? "slide:new" : mod && menuKey === "d" ? "slide:duplicate" : "";
  if (name) app.key(name, ev.shiftKey, mod);
  else if (req) app.request(req);
  else if (mod && /^[zy]$/i.test(ev.key)) app.chord(ev.key.toLowerCase());
  else return false;
  ev.preventDefault();
  afterInput();
  paintOnce();
  focusApp();
  return true;
}
function currentRegion() {
  const el = document.activeElement;
  if (el === keys) return "editor";
  const id = el && el.dataset ? el.dataset.a11yId : "";
  return regionOf(id);
}
// An open popover or dialog holds the keyboard; otherwise the regions in order.
function regionOrder() {
  if (app.chartIsOpen()) return ["chart"];
  if (app.shareIsOpen()) return ["panels"];
  if (app.hintIsOpen() && currentRegion() === "hint") return ["hint", "editor"];
  return ["bar", "tabs", "editor", "slides", "panels"];
}
function cycleRegion(back) {
  const order = regionOrder();
  const at = order.indexOf(currentRegion());
  for (let k = 1; k <= order.length; k += 1) {
    const r = order[(at + (back ? -k : k) + order.length * 2) % order.length];
    if (focusRegion(r)) return;
  }
}
function moveInRegion(step) {
  const el = document.activeElement;
  const id = el && el.dataset ? el.dataset.a11yId : "";
  const list = focusables(regionOf(id));
  if (!list.length) return;
  const at = list.findIndex((n) => n.id === id);
  focusNode(list[(at + step + list.length) % list.length]);
}
window.__kb = { focusables, focusRegion, currentRegion };
mirror.root.addEventListener("keydown", (ev) => {
  const el = ev.target;
  const id = el && el.dataset ? el.dataset.a11yId : "";
  if (!id) return;
  const node = a11yTree && a11yTree.byId.get(id);
  if (ev.key === "F6") {
    ev.preventDefault();
    cycleRegion(ev.shiftKey);
    return;
  }
  if (ev.key === "Tab") {
    ev.preventDefault();
    moveInRegion(ev.shiftKey ? -1 : 1);
    return;
  }
  if (/^thumb-\d+$/.test(id) && stripKey(ev)) return;
  // Shift+F10 or the menu key on a presentation in Rooms or its tab: its menu
  if ((id.startsWith("tb-roomdeck-d-") || id.startsWith("decktabs-tab-")) && node && (ev.key === "ContextMenu" || (ev.shiftKey && ev.key === "F10"))) {
    ev.preventDefault();
    let opened = false;
    pressAtCentre(node, (x, y) => { opened = app.deckMenuAt(x, y); });
    if (opened) {
      afterInput();
      focusSlideMenu();
    }
    return;
  }
  if (node && node.role === "slider" && /^Arrow/.test(ev.key)) {
    ev.preventDefault();
    const up = ev.key === "ArrowRight" || ev.key === "ArrowUp";
    if (app.a11ySlide(id, up ? 1 : -1)) {
      afterInput();
      needsPaint = true;
      paintOnce();
      mirrorA11y();
      focusNode(node);
    }
    return;
  }
  if (ev.key === "ArrowRight" || ev.key === "ArrowDown") {
    ev.preventDefault();
    moveInRegion(1);
    return;
  }
  if (ev.key === "ArrowLeft" || ev.key === "ArrowUp") {
    ev.preventDefault();
    moveInRegion(-1);
    return;
  }
  if (ev.key === " " && el.tagName !== "BUTTON") {
    ev.preventDefault();
    el.click();
    return;
  }
  if (ev.key === "Escape") {
    ev.preventDefault();
    const reg = regionOf(id);
    if (reg === "hint") app.closeHint();
    else if (reg === "chart") app.key("escape", false, false);
    else if (reg === "panels" && app.shareIsOpen()) app.closeShare();
    else if (reg === "bar" || reg === "ctx") app.key("escape", false, false);
    afterInput();
    app.setA11yFocus("");
    if (reg === "ctx") focusApp();
    else focusKeys(app.focusTarget() === "stage" ? "stage" : "editor");
    needsPaint = true;
  }
});

// --- files the charts read -------------------------------------------------------
// A chart may take its rows from a file (`"data": {"url": "data/movies.json"}`,
// as the Vega-Lite examples do). The page fetches each one once: beside the
// page first, then — for a relative path — from the Vega example datasets.
// A chart's live data (`"url": "https://…"`, or a Google Sheet, which the
// markdown module turns into the sheet's CSV address) is fetched fresh each
// time the presentation opens and again on R / ⟳ while presenting
// (refreshLiveData). It is never kept with the document: an export (PDF, PPTX)
// is a snapshot of what was on the slides when it was made.
const chartFiles = new Map();
let chartFilesRev = -1;
const isLive = (url) => /^https?:/.test(url);
// Live data is PRO: fetched for a signed-in user, in a deck opened from a
// cloud share (/s/{id}: made by a PRO owner), and in local development.
let liveFromShare = false;
let liveAuthKnown = false;
let liveNoted = false;
function liveAllowed() {
  return liveFromShare || /^(localhost|127\.0\.0\.1)$/.test(location.hostname) || !!window.sliqtly?.user?.();
}
// The slides' line art ({art=waves}) is PRO as well, on the same terms. The
// layouts drawn from lists (```process, ```swot, ```timeline) are free.
function proNow() {
  app.setPro(liveAllowed());
  needsPaint = true;
}
proNow();
// signed in or out: the live data is looked at again
window.addEventListener("sliqtly:user", () => {
  proNow();
  liveAuthKnown = true;
  chartFilesRev = -1;
  needsPaint = true;
});
async function fetchLive(url) {
  const r = await fetch(url, { cache: "no-store" });
  if (!r.ok) throw new Error(String(r.status));
  const text = await r.text();
  // a sheet nobody may read without signing in answers with a page, not CSV
  if (/^\s*</.test(text) && !/\.(xml|svg)(\?|$)/i.test(url)) throw new Error("html");
  return text;
}
// A live source's data: fetched as it is; a Google Sheet that is not shared by
// link, through the Sheets API as the signed-in owner (sliqtly.js readSheet;
// `ask`: a press or a paste, which may open Google's popup and Picker).
// Each good read is kept with the deck under data/live/ (only when it
// changed), and that copy is what shows when the source cannot be read:
// a reader of a shared deck, who may not read the owner's sheet, sees the
// copy the owner's editor last kept.
const isSheet = (url) => /^https:\/\/docs\.google\.com\/spreadsheets\/d\//.test(url);
const liveCopies = new Map();
let copyNoted = false;
let privateNoted = false;
function copyPath(url) {
  let h = 2166136261;
  for (let i = 0; i < url.length; i++) h = Math.imul(h ^ url.charCodeAt(i), 16777619) >>> 0;
  return "data/live/" + h.toString(36) + ".csv";
}
const liveRead = new Map(); // copy path → when its source was last read
async function keepLiveCopy(url, text) {
  if (viewer) return;
  const path = copyPath(url);
  liveRead.set(path, Date.now());
  const named = liveName(url);
  if (liveCopies.get(path) === text && !named) return;
  liveCopies.set(path, text);
  const have = (await docFiles()).find((f) => f.path === path);
  // a name from the Sheets API wins over the address's own
  const title = named || have?.title || liveTitle(url);
  if (have && have.data === text && have.title === title && have.source === url) return;
  await keepFile({ path, type: "text/csv", size: text.length, data: text, source: url, title, read: Date.now() });
}
// A live source's name for Files: the spreadsheet and its tab as the Sheets
// API told them (a private sheet), else what the address says.
function liveName(url) {
  const n = isSheet(url) && window.sliqtly?.sheetName?.(url);
  return n && n.title ? n.title + (n.tab ? " · " + n.tab : "") : "";
}
function liveTitle(url) {
  if (!url) return "";
  const named = liveName(url);
  if (named) return named;
  const q = (() => { try { return new URL(url).searchParams; } catch (_) { return new URLSearchParams(); } })();
  if (isSheet(url)) return t("Google Sheet") + (q.get("sheet") ? " · " + q.get("sheet") : "");
  try { return decodeURIComponent(url.split(/[?#]/)[0].split("/").pop() || url); } catch (_) { return url; }
}
// The address a person opens for a source: a sheet's own page, not its CSV.
function sourcePage(url) {
  const m = /^https:\/\/docs\.google\.com\/spreadsheets\/d\/([^/?#]+)/.exec(url);
  if (!m) return url;
  const gid = (() => { try { return new URL(url).searchParams.get("gid"); } catch (_) { return null; } })();
  return "https://docs.google.com/spreadsheets/d/" + m[1] + "/edit" + (gid ? "#gid=" + gid : "");
}
// The live address a kept copy stands for: as kept, else one the deck reads.
function copySource(f) {
  return f.source || [...chartFiles.keys()].filter(isLive).find((u) => copyPath(u) === f.path) || "";
}
async function liveCopy(url) {
  const path = copyPath(url);
  if (liveCopies.has(path)) return liveCopies.get(path);
  const have = (await docFiles()).find((f) => f.path === path);
  return have && typeof have.data === "string" ? have.data : null;
}
// A small card that asks for a press. `onPress` runs within the press, so a
// Google window it opens is not blocked (Safari on a phone counts a press as
// spent once the page has waited on the network). → what onPress gives, or
// false on the second button.
function pressCard(id, message, okLabel, noLabel, onPress) {
  return new Promise((ok) => {
    document.getElementById(id)?.remove();
    const box = document.createElement("div");
    box.id = id;
    box.className = "gCard";
    box.setAttribute("role", "dialog");
    box.setAttribute("aria-label", t("Google Sheets"));
    const text = document.createElement("p");
    text.textContent = message;
    const go = document.createElement("button");
    go.className = "primary";
    go.textContent = okLabel;
    const no = document.createElement("button");
    no.textContent = noLabel;
    const done = (v) => { box.remove(); ok(v); };
    go.addEventListener("click", () => {
      // no await before onPress: the window opens within the press
      Promise.resolve(onPress()).then(done, () => done(false));
    });
    no.addEventListener("click", () => done(false));
    box.append(text, go, no);
    document.body.appendChild(box);
    go.focus();
  });
}
// Google's window blocked after all: one more press opens it. → true when a
// token came.
let tapping = null;
function googleTap() {
  tapping ??= pressCard("gTap", t("This Google Sheet is private. Google asks once whether Sliqtly may read the sheets you pick."),
    t("Continue with Google"), t("Cancel"),
    () => window.sliqtly.askSheets().then((tok) => !!tok)).finally(() => { tapping = null; });
  return tapping;
}

async function readLive(url, ask) {
  let text = null;
  let why = null;
  try {
    text = await fetchLive(url);
  } catch (e) {
    why = e;
  }
  if (text == null && isSheet(url) && window.sliqtly?.user?.()) {
    try {
      text = await window.sliqtly.readSheet(url, ask);
    } catch (e) {
      why = e;
      // the press was spent on the fetch before it (Safari): a press of its own
      if (ask && e?.code === "auth/popup-blocked" && (await googleTap())) {
        try {
          text = await window.sliqtly.readSheet(url, true);
        } catch (e2) {
          why = e2;
        }
      }
    }
  }
  if (text != null) {
    keepLiveCopy(url, text).catch(fail);
    return text;
  }
  const copy = await liveCopy(url);
  if (copy != null) {
    if (isSheet(url) && why?.code === "auth" && !privateNoted && !viewer) {
      privateNoted = true;
      toast(t("A private Google Sheet: the saved copy is shown. Press R while presenting to sign in to Google and read it again."));
    } else if (!copyNoted && !(isSheet(url) && why?.code === "auth")) {
      copyNoted = true;
      toast(t("The data source could not be read: the saved copy is shown."));
    }
    return copy;
  }
  throw why || new Error("unreadable");
}
// for scripts/check-web.mjs: the copy kept of a live source
window.__liveCopy = (url) => liveCopy(url);
function liveFailed(url) {
  toast(/docs\.google\.com\/spreadsheets/.test(url)
    ? (window.sliqtly?.user?.() ? t("Could not read the Google Sheet. Paste its link again to pick it in Google's file picker, or share it as \"Anyone with the link\": ") : t("Could not read the Google Sheet. Share it as \"Anyone with the link\": ")) + url
    : t("Could not load the chart file: ") + url);
}
function showLiveButton() {
  const b = document.getElementById("vData");
  if (b) b.hidden = ![...chartFiles.keys()].some(isLive);
}
let refreshing = null;
function refreshLiveData() {
  return refreshLive([...chartFiles.keys()].filter(isLive));
}
function refreshLive(urls) {
  if (!urls.length || refreshing) return refreshing;
  refreshing = (async () => {
    let failed = 0;
    await Promise.all(urls.map(async (url) => {
      try {
        const text = await readLive(url, true);
        chartFiles.set(url, Promise.resolve(text));
        app.setChartData(url, text);
      } catch (_) {
        failed += 1;
        liveFailed(url);
      }
    }));
    dropThumbs();
    needsPaint = true;
    if (!failed) toast(t("Data refreshed"));
    refreshing = null;
    refreshFiles();
  })();
  return refreshing;
}

// Files → Unlink: the charts that read a linked source read its last copy
// instead, kept as an ordinary data file named after the source; the source
// is not read again.
async function unlinkLive(f, url) {
  const files = await docFiles();
  const base = (f.title || liveTitle(url) || "data").replace(/[^\p{L}\p{N}]+/gu, "-").replace(/^-+|-+$/g, "").slice(0, 60) || "data";
  let path = "data/" + base + ".csv";
  for (let n = 2; files.some((x) => x.path === path); n += 1) path = "data/" + base + "-" + n + ".csv";
  let changed = 0;
  const md = app.source().replace(/("url"\s*:\s*")([^"]+)(")/g, (all, a, u, b) => {
    if (!url || (u !== url && app.liveUrl(u) !== url)) return all;
    changed += 1;
    return a + path + b;
  });
  if (!changed && url && (app.deck?.dataUrls || []).includes(url)) {
    // read through a fence this cannot rewrite ({"source": "google-sheets", …})
    toast(t("A chart names this source in its own way: change its data source in the chart editor."));
    return;
  }
  const text = typeof f.data === "string" ? f.data : await f.data.text();
  if (changed) {
    await keepFile({ path, type: "text/csv", size: text.length, data: text });
    chartFiles.set(path, Promise.resolve(text));
    app.setChartData(path, text);
    app.setSource(md);
  }
  pending.delete(f.path);
  if (doc.persisted) await vfs.deleteFile(doc.id, f.path);
  liveCopies.delete(f.path);
  if (url) chartFiles.delete(url);
  if (changed) afterInput();
  await saveDoc(true);
  showLiveButton();
  dropThumbs();
  toast(changed ? t("Unlinked: the charts now read ") + path : t("No chart reads this source any more: its copy was removed."));
}
function fetchChartFiles(rev) {
  if (rev === chartFilesRev) return;
  chartFilesRev = rev;
  const wanted = (app.chartDataWanted() || "").split("\n").filter(Boolean);
  for (const url of wanted) {
    if (chartFiles.has(url)) continue;
    if (isLive(url)) {
      if (!liveAllowed()) {
        if (liveAuthKnown && !liveNoted) {
          liveNoted = true;
          toast(t("Live chart data is a PRO feature: sign in with PRO to fetch it."));
        }
        continue;
      }
      const got = readLive(url, false).catch(() => null);
      chartFiles.set(url, got);
      showLiveButton();
      got.then((text) => {
        if (text == null) return liveFailed(url);
        app.setChartData(url, text);
        dropThumbs();
        needsPaint = true;
      });
      continue;
    }
    const tries = ["./" + url.replace(/^\.?\//, ""), "https://cdn.jsdelivr.net/npm/vega-datasets@2/" + url.replace(/^\.?\//, "")];
    let fromWorkbook = false;
    const got = (async () => {
      // the document's own copy, when it has one
      const mine = (await docFiles()).find((f) => f.path === bare(url));
      if (mine && typeof mine.data === "string") return mine.data;
      // a sheet of a workbook the document keeps
      const sheet = /\.csv$/i.test(url) ? await derivedCsv(url) : null;
      if (sheet != null) {
        fromWorkbook = true;
        return sheet;
      }
      for (const u of tries) {
        try {
          const r = await fetch(u);
          if (r.ok) return await r.text();
        } catch (_) { /* the next place */ }
      }
      return null;
    })();
    chartFiles.set(url, got);
    got.then((text) => {
      if (text == null) {
        // a program's stylesheet beside it (app.tsx.css) is optional
        if (!/\.tsx\.css$/i.test(url)) toast(t("Could not load the chart file: ") + url);
        app.setChartDataMissing(url);
        needsPaint = true;
        return;
      }
      app.setChartData(url, text);
      dropThumbs();
      needsPaint = true;
      // kept with the document, so it opens without the network next time
      // (a workbook's sheet is the workbook's: nothing to keep)
      if (fromWorkbook) return;
      docFiles().then((have) => {
        if (!have.some((f) => f.path === bare(url))) keepFile({ path: bare(url), type: "text/plain", size: text.length, data: text }).catch(fail);
      });
    });
  }
}

function frame() {
  try {
    const now = performance.now();
    app.setUiTime(now / 1000);
    app.reviewClock(Date.now(), -new Date().getTimezoneOffset());
    if (app.uiBusy()) needsPaint = true;
    // charts whose theme changed are drawn again a few a frame (PresApp.settle)
    if (app.settle()) needsPaint = true;
    if (themePicQueue.length) drawThemePicture();
    if (app.isReplaying()) {
      // a recording played: its time drives the presentation (recFrame)
      needsPaint = true;
    } else if (app.isPlaying()) {
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
    recFrame();
    if ((doc.cloud || "") !== collabWant) collabFollow();
    collab?.tick();
    meet.tick();
    apps.tick(app.revision());
    const rev = app.revision();
    const effects = window.__lastStage && window.__lastStage.list && window.__lastStage.list.effects && window.__lastStage.list.effects.length > 0;
    if (needsPaint || rev !== lastRev || effects) {
      needsPaint = false;
      lastRev = rev;
      syncEndPanel();
      syncCounter();
      syncRecBar();
      loadLookFaces();
      if (rev !== inlineSvgRev) loadInlineSvgs(rev);
      paintOnce();
      handleRequests();
      followAddress();
      syncPageTitle(rev);
      if (lastLayout) fetchChartFiles(lastLayout.rev);
      if (rev !== lastA11yRev) {
        lastA11yRev = rev;
        mirrorA11y();
      }
    }
  } catch (e) {
    fail(e);
  }
  requestAnimationFrame(frame);
}

// --- editing together (a server of one's own) ---------------------------------------
// The editor's deck, when its share is on a server of one's own, is a room
// everyone with it open edits at once (web/collab.js, mcp-go/collab.go):
// their edits arrive as they type, their carets show in the Markdown, and a
// chat sits beside the deck. In the bar: one's own name (pressed: renamed)
// with how many others are here, and Chat with the count of unread messages.
// (read again for the account signed in: useAccount)
const collabMe = loadMe(mine);
let collab = null;
let collabWant = "";
let collabPeople = new Map();
const collabEditor = {
  version: () => app.mdVersion(),
  text: () => app.source(),
  caret: () => app.mdCaret(),
  anchor: () => app.mdAnchor(),
  apply: (offset, removed, text) => app.applyRemoteMd(offset, removed, text),
  // a presentation goes on where it was: the app moved its clock (PresFollow)
  synced: () => { app.syncRemote(); rebaseClock(); needsPaint = true; },
  setPeers: (rows) => { app.setPeers(rows); needsPaint = true; },
};
function collabOn() {
  return !!(collab && collab.active() && collab.id === doc.cloud);
}
// the deck's call (web/meet.js, src/PresMeet.rgr, mcp-go/meet.go): in the
// bar as Call / Join call · 2 / In call · 2 while the deck is a room
const meet = new Meet({
  app,
  session: () => (collabOn() ? collab : null),
  mic: () => navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true } }),
  toast,
  t,
  paint: () => { needsPaint = true; refreshCollabBar(); },
  micHelp: (e) => micHelp(e),
  now: () => performance.now(),
});
window.__meet = meet;
// the room of the deck open now; the one before is left
function collabFollow() {
  const tr = window.sliqtly?.collab;
  const want = tr && !viewer && doc.persisted && !doc.loading && doc.cloud ? doc.cloud : "";
  if (want === collabWant) return;
  collabWant = want;
  meet.reset();
  if (collab) collab.stop();
  collab = null;
  collabPeople = new Map();
  if (app.chatIsOpen()) app.chatOpen(false);
  refreshCollabBar();
  if (!want) return;
  // a copy: the room may give this page another name or colour than the
  // one kept in this browser (someone here has it), for this deck only
  const s = new CollabSession({ RdOtDelta, RdOtClient }, tr, collabEditor, { ...collabMe }, {
    peers: (list) => collabPeers(list),
    me: (name, color) => {
      app.chatRename(collabMe.who, name, color);
      refreshCollabBar();
      needsPaint = true;
    },
    call: (m) => meet.event(m),
    chat: (m) => {
      if (app.chatAdd(m.id, m.who, m.name, m.color, m.text, chatTime(m.at, Date.now(), lang), m.who === collabMe.who)) refreshCollabBar();
      needsPaint = true;
    },
  });
  collab = s;
  app.meetClient(s.me.client);
  s.start(want).then(() => refreshCollabBar(), (e) => {
    console.warn("editing together is off for this deck", e);
    if (collab === s) collab = null;
    refreshCollabBar();
  });
}
// who is here: the others, once each (a person may have the deck open twice)
function collabPeers(list) {
  const next = new Map();
  for (const p of list) if (p.who !== collabMe.who && !next.has(p.who)) next.set(p.who, p);
  for (const [who, p] of next) {
    if (!collabPeople.has(who)) toast(t("%s is editing this presentation too").replace("%s", p.name));
    app.chatRename(who, p.name, p.color);
  }
  collabPeople = next;
  refreshCollabBar();
}
// the name this page goes by in the room now
function shownName() {
  return collabOn() ? collab.me.name : collabMe.name;
}
function renameMe(name) {
  const n = cleanName(name);
  if (!n || n === shownName()) return;
  collabMe.name = n;
  saveMe(mine, collabMe);
  app.chatRename(collabMe.who, n, collabOn() ? collab.me.color : collabMe.color);
  collab?.rename(n).catch(() => {});
  reviewMe();
  refreshCollabBar();
}
function collabButton(id, onClick) {
  let b = document.getElementById(id);
  if (b) return b;
  b = document.createElement("button");
  b.id = id;
  b.hidden = true;
  b.dataset.canvas = "secondary";
  b.addEventListener("click", onClick);
  const bar = document.getElementById("bar");
  bar.insertBefore(b, document.getElementById("pro"));
  return b;
}
function refreshCollabBar() {
  reviewMe();
  const on = collabOn();
  const me = collabButton("collabName", () => { if (app.openAskName(shownName())) needsPaint = true; });
  const chat = collabButton("collabChat", () => {
    app.chatOpen(!app.chatIsOpen());
    refreshCollabBar();
    needsPaint = true;
  });
  const call = collabButton("collabCall", () => {
    app.meetPress();
    handleRequests();
    refreshCollabBar();
    needsPaint = true;
  });
  const callText = "📞 " + app.meetButton();
  if (call.hidden === on) call.hidden = !on;
  if (call.textContent !== callText) call.textContent = callText;
  const n = app.meetCount();
  const callShort = "📞" + (n > 0 ? " " + n : "");
  if (call.dataset.short !== callShort) call.dataset.short = callShort;
  const others = [...collabPeople.values()];
  const meText = shownName() + (others.length ? " +" + others.length : "");
  const meTitle = t("Your name for the others: press to change it") + (others.length ? "\n" + t("Here now: ") + others.map((p) => p.name).join(", ") : "");
  const chatText = t("Chat") + (app.chatIsOpen() ? "" : (app.chatBadge() ? " " + app.chatBadge() : ""));
  if (me.hidden === on) me.hidden = !on;
  if (chat.hidden === on) chat.hidden = !on;
  if (me.textContent !== meText) me.textContent = meText;
  const meShort = "👤" + (others.length ? " +" + others.length : "");
  if (me.dataset.short !== meShort) me.dataset.short = meShort;
  if (me.title !== meTitle) me.title = meTitle;
  if (chat.textContent !== chatText) chat.textContent = chatText;
  const chatShort = "💬" + (app.chatIsOpen() ? "" : (app.chatBadge() ? " " + app.chatBadge() : ""));
  if (chat.dataset.short !== chatShort) chat.dataset.short = chatShort;
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

// for scripts/check-web.mjs: the requests run now, a picture's pixel size
window.__handleRequests = () => handleRequests();
window.__picturePixel = (p, x, y) => {
  const img = pictures.get(p);
  if (!img) return [];
  const c = document.createElement("canvas");
  c.width = img.naturalWidth;
  c.height = img.naturalHeight;
  const g = c.getContext("2d");
  g.drawImage(img, 0, 0);
  return [...g.getImageData(x, y, 1, 1).data];
};
window.__pictureTag = (p) => (pictures.get(p) ? pictures.get(p).tagName : "");
window.__pictureSize = (p) => {
  const img = pictures.get(p);
  return img ? [img.naturalWidth, img.naturalHeight] : [0, 0];
};

// Help → About: the app's name, the server's version when the page is
// served by a server of one's own (mcp-go/assets/sliqtly-local.js), and
// the build
async function openAbout() {
  const lines = [APP_NAME];
  const server = await window.sliqtly?.serverVersion?.().catch(() => "");
  if (server) lines.push(t("Version") + " " + server);
  const built = BUILT.startsWith("__") ? "" : " (" + BUILT + ")";
  lines.push(t("Build") + " " + (BUILD.startsWith("__") ? "dev" : BUILD) + built);
  if (app.openAbout(t("About"), lines.join("\n"))) needsPaint = true;
}

// A link on a slide (src/PresStageClick.rgr, web/stagelink.js): this deck's
// own slide in the app; a Sliqtly presentation of one's own opens here, as
// from the presentations window, someone else's in a new tab, as does any
// other web address.
let linkTimer = 0;
async function followLink(href) {
  if (app.followLink(href)) {
    needsPaint = true;
    return;
  }
  const to = linkTarget(href, location.origin, SITE);
  if (!to) return;
  if (to.deck) {
    if (to.deck === doc.cloud) {
      app.selectSlide(to.slide);
      needsPaint = true;
      return;
    }
    const opened = await loadingScreen(() => openOwnCloud(to.deck).catch((e) => { console.warn(e); return false; }));
    if (opened) {
      if (to.slide > 0) app.selectSlide(to.slide);
      refreshFiles();
      needsPaint = true;
      return;
    }
  }
  window.open(to.url, "_blank", "noopener");
}

function handleRequests() {
  for (;;) {
    const r = app.takeRequest();
    if (!r) break;
    if (r === "fullscreen") {
      document.body.classList.add("presenting");
      wakeViewer();
      presentStartedAt = performance.now();
      refreshLiveData();
      rebaseClock();
      if (!viewer && document.documentElement.requestFullscreen && !document.fullscreenElement) {
        document.documentElement.requestFullscreen().catch(() => {});
      }
      // Going full screen can take the focus away from the field the keys
      // arrive in; a presentation without keys is a slideshow nobody can drive.
      keys.focus({ preventScroll: true });
      requestAnimationFrame(resize);
    } else if (r.startsWith("meet:")) {
      meet.request(r);
      refreshCollabBar();
    } else if (recRequest(r)) {
      // Record, Play recording, the voice (above)
    } else if (r.startsWith("link:")) {
      // a link clicked on the slide: followed once it is not the first click
      // of a double click (which picks the block); presenting, at once
      clearTimeout(linkTimer);
      const href = r.slice(5);
      const presenting = lastLayout && lastLayout.mode === "present";
      linkTimer = setTimeout(() => followLink(href).catch((e) => console.warn("link not followed", e)), presenting ? 0 : FOLLOW_MS);
    } else if (r.startsWith("click:")) {
      // the canvas bar: the page's own button does what it always did
      const b = document.getElementById(r.slice(6));
      if (b) b.click();
    } else if (r === "docset") {
      // the document settings window: the deck's pictures for the logo
      docFiles().then((fs) => {
        const pics = fs.filter((f) => kindOf(f.path, f.type) === "image").map((f) => f.path).sort();
        if (app.openDocSettings(pics.join("\n"))) {
          needsPaint = true;
          docRooms().catch((e) => console.warn("no rooms for the settings", e));
        }
      });
    } else if (r.startsWith("docroom:")) {
      // Document settings moved the presentation to another room
      const room = r.slice(8);
      currentRoomId().then(async (id) => {
        if (!id) return;
        await roomsRequest("room:moveid:" + room + ":" + id);
        const to = (await roomsList({ archived: true })).find((x) => x.room_id === room)?.title || room;
        toast(t("Moved to ") + to);
      }).catch((e) => toast(t("Rooms: ") + (e.message || e)));
    } else if (r === "about") {
      openAbout().catch(fail);
    } else if (r === "help-guide") {
      app.openHelpTab("guide");
      needsPaint = true;
    } else if (r === "settings") {
      app.openSettings(autoContrast);
      needsPaint = true;
    } else if (r.startsWith("setting:contrast:")) {
      autoContrast = r.endsWith(":on");
      try { localStorage.setItem("sliqtly.autoContrast", autoContrast ? "on" : "off"); } catch (_) { /* this session only */ }
      dropThumbs();
      needsPaint = true;
    } else if (r.startsWith("setting:review:")) {
      try { localStorage.setItem(REVIEW_KEY, r.endsWith(":on") ? "on" : "off"); } catch (_) { /* this session only */ }
      applyReviewMode();
    } else if (r === "review-save") {
      keepReview().catch(fail);
    } else if (r === "confirm:zip") {
      exportZip(true).catch(fail);
    } else if (r === "confirm:zip:alt") {
      exportZip(false).catch(fail);
    } else if (r.startsWith("setting:skin:")) {
      setSkinName(r.endsWith(":retro") ? "retro" : "");
    } else if (r.startsWith("setting:mode:")) {
      setMode(r.slice("setting:mode:".length));
    } else if (r.startsWith("setting:term:")) {
      // the page opens again with the new word, the deck saved first
      saveDoc(true).catch(() => {}).finally(() => chooseTerm(r.slice("setting:term:".length)));
    } else if (r.startsWith("setting:skinhue:")) {
      setSkinHue(parseInt(r.slice("setting:skinhue:".length), 10));
    } else if (r.startsWith("room:")) {
      roomsRequest(r).catch((e) => toast(t("Rooms: ") + (e.message || e)));
    } else if (r.startsWith("roomchat:")) {
      roomChat().request(r.slice(9)).catch((e) => toast(t("Chat: ") + (e.message || e)));
    } else if (r.startsWith("title:")) {
      renameDeck(r.slice(6));
    } else if (r === "rail:review") {
      // the rail's Review: review mode on or off, kept as Settings keeps it
      const on = !app.reviewMode();
      try { localStorage.setItem(REVIEW_KEY, on ? "on" : "off"); } catch (_) { /* this session only */ }
      applyReviewMode();
    } else if (r === "decks") {
      decksRoom = null;
      openDecks().catch(fail);
    } else if (r.startsWith("decks:")) {
      decksRequest(r).catch(fail);
    } else if (r === "openbox") {
      // Open: a file from the computer, or a sample deck
      app.openOpen([...sampleSel.options].map((o) => o.value + "\t" + o.textContent.trim()).join("\n"));
      needsPaint = true;
    } else if (r.startsWith("showtab:")) {
      app.showTab(r.slice(8));
      needsPaint = true;
    } else if (r.startsWith("copy:")) {
      copyShare(r.slice(5)).catch(fail);
    } else if (r.startsWith("edit:")) {
      editRequest(r.slice(5)).catch(fail);
    } else if (r === "confirm:clip" || r === "confirm:clip:alt") {
      clipAnswered("clip", r.endsWith(":alt"));
    } else if (r === "confirm:clipimg" || r === "confirm:clipimg:alt") {
      clipAnswered("clipimg", r.endsWith(":alt"));
    } else if (r.startsWith("clip:undo:") || r.startsWith("clip:redo:")) {
      clipFilesBack(Number(r.slice(10)), r.startsWith("clip:undo:")).catch(fail);
    } else if (r.startsWith("clip:")) {
      // Copy ▸ / Export ▸ Clipboard: the Markdown, with the comments, the slide's
      writeClip(app.copyText(r.slice(5))).then((ok) => toast(ok ? t("Copied") : t("Could not copy"))).catch(fail);
    } else if (r.startsWith("openlink:")) {
      // a link in a comment (review mode): web addresses only, in a new tab
      const u = r.slice(9);
      if (/^https?:\/\//i.test(u)) window.open(u, "_blank", "noopener");
    } else if (r === "review-copy") {
      // a comment thread, or the open comments with their slides (review mode)
      writeClip(app.reviewClip()).then((ok) => toast(ok ? t("Copied") : t("Could not copy"))).catch(fail);
    } else if (r.startsWith("select:")) {
      const [, id, ...rest] = r.split(":");
      const sel = document.getElementById(id);
      if (sel) {
        sel.value = rest.join(":");
        sel.dispatchEvent(new Event("change"));
      }
    } else if (r === "themes") {
      // Slide → Theme…: the themes as tiles with their pictures
      if (app.openThemes(selectRows(themeSel), themeSel.value || "")) needsPaint = true;
    } else if (r === "theme-pictures") {
      queueThemePictures();
    } else if (r === "theme-edited") {
      editedCss[themeSel.value || ""] = app.themeCss();
      dropThumbs();
    } else if (r === "files-list") {
      refreshFiles();
    } else if (r.startsWith("files:")) {
      fileRequest(r.slice(6)).catch(fail);
    } else if (r.startsWith("deckdo:")) {
      deckDo(r.slice(7)).catch(fail);
    } else if (r.startsWith("deck:switch:")) {
      switchDeck(r.slice(12)).catch(fail);
    } else if (r === "deck:tabs") {
      keepTabs();
      needsPaint = true;
    } else if (r.startsWith("sketch-file:")) {
      // a drawing made on a slide (PresSketch): its file, written again
      const path = bare(r.slice(12));
      const text = app.sketchFileBody(path);
      keepFile({ path, type: SKETCH_TYPE, size: text.length, data: text }).catch(fail);
      dropThumbs();
    } else if (r.startsWith("chart-file:")) {
      const path = bare(r.slice(11));
      const text = app.chartFileBody();
      keepFile({ path, type: "application/json", size: text.length, data: text }).catch(fail);
      chartFiles.set(path, Promise.resolve(text));
      dropThumbs();
    } else if (r.startsWith("confirm:roomclear:")) {
      clearRoom(r.slice("confirm:roomclear:".length)).catch((e) => toast(t("Rooms: ") + (e.message || e)));
    } else if (r.startsWith("confirm:roomdelete:")) {
      roomsRequest("room:delete:" + r.slice("confirm:roomdelete:".length)).catch((e) => toast(t("Rooms: ") + (e.message || e)));
    } else if (r.startsWith("confirm:roomdrop:")) {
      roomsRequest("room:dropgo:" + r.slice("confirm:roomdrop:".length)).catch((e) => toast(t("Rooms: ") + (e.message || e)));
    } else if (r === "confirm:tracesave" || r === "confirm:tracesave:alt") {
      answerTraceSave(r === "confirm:tracesave").catch(fail);
    } else if (r === "confirm:deletedeck") {
      deleteDeck().catch(fail);
    } else if (r === "newdeck-create") {
      const plan = JSON.parse(app.newDeckPlan());
      if (plan.ask === "room") {
        roomsRequest("room:save:" + JSON.stringify(plan)).catch((e) => toast(t("Rooms: ") + (e.message || e)));
      } else if (plan.ask === "folder") {
        roomsRequest("room:foldersave:" + JSON.stringify(plan)).catch((e) => toast(t("Rooms: ") + (e.message || e)));
      } else if (plan.ask === "name") {
        renameMe(plan.name);
      } else if (plan.ask === "editors") {
        saveEditors(plan.editors || "").catch((e) => toast(t("Could not save who can edit: ") + (e?.code || e?.message || e)));
      } else if (plan.dup) {
        makingDeck = duplicateDeck(plan.name).catch((e) => toast(t("Duplicating failed: ") + (e.message || e))).finally(() => { makingDeck = null; });
      } else {
        // the picker now, while the press still counts as one; the file goes
        // into the new deck once it is made
        if (plan.data === "file") {
          addAsks = true;
          fileAdd.click();
        }
        makingDeck = newDeck(plan).catch(fail).finally(() => { makingDeck = null; });
      }
    } else if (r === "share-invite") {
      openInvite();
    } else if (r === "share-stop") {
      stopSharing().catch((e) => toast(t("Could not stop sharing: ") + (e?.code || e?.message || e)));
    } else if (r === "chat-send") {
      const text = app.chatTakeSent();
      collab?.say(text).catch((e) => toast(t("The message was not sent: ") + (e?.message || e)));
    } else if (r === "chat-closed") {
      refreshCollabBar();
    } else if (r === "picture-place") {
      placePasted().catch(fail);
    } else if (r === "picture-cancel") {
      dropPasting();
    } else if (r === "image-adjust") {
      adjustPreview();
    } else if (r === "image-save") {
      saveAdjusted().catch(fail);
    } else if (r === "image-cancel") {
      dropAdjusting();
    } else if (r.startsWith("image-edit:")) {
      openImageEditor(r.slice(11)).catch(fail);
    } else if (r.startsWith("image-trace:")) {
      openTraceEditor(r.slice(12)).catch(fail);
    } else if (r === "trace-run") {
      runTrace();
    } else if (r === "trace-save") {
      saveTraced().catch(fail);
    } else if (r === "trace-cancel") {
      dropTracing();
    } else if (r === "sheet-edit") {
      liveSheets.editFirst();
    } else if (r.startsWith("data-keep:")) {
      keepData(+r.slice(10)).catch(fail);
    } else if (r.startsWith("file-save:")) {
      saveOpenFile(r.slice(10)).catch(fail);
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
    if (liveSheets.editing()) {
      // Esc in full screen is the browser's: it was meant for the sheet
      liveSheets.finishEditing();
    } else if (!viewer && !byApp && was && app.isPlaying() && JSON.parse(app.layoutJson()).mode === "present") {
      app.endPresent();
      handleRequests();
    }
  }
  requestAnimationFrame(resize);
});

let docName = "presentation";

// The browser's tab says which presentation is open: the name the bar shows
// (front matter `title:`, else the first heading, else the file's name).
// Looked at again only when the text or the open deck changed.
let pageTitleKey = "";
function syncPageTitle(rev) {
  const key = rev + "\n" + docName;
  if (key === pageTitleKey) return;
  pageTitleKey = key;
  const name = String(app.docTitle() || "").replace(/\s+/g, " ").trim() || docName;
  const want = name ? name + " · Sliqtly" : "Sliqtly";
  if (document.title !== want) document.title = want;
}

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
  const fx = JSON.parse(app.fxSlidesJson());
  // a slide's own picture (bg=) as well: the PPTX takes it, cut and dimmed
  // as the stage shows it, as the slide's background
  const list = fx.concat(JSON.parse(app.bgSlidesJson()));
  if (!list.length) return;
  if (fx.length) {
    toast(t("Rendering effects of ") + fx.length + t(" slides for export…"));
    // the toast gets a frame to show before the work starts
    await new Promise((r) => requestAnimationFrame(() => setTimeout(r, 0)));
  }
  const c = document.createElement("canvas");
  const g = c.getContext("webgl2", { antialias: false, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
  if (!g) return;
  for (const i of list) {
    const doc = atRest(JSON.parse(app.fxJson(i)));
    // The still is drawn without the slide's text, which goes on top of it in
    // the export; rain that flows around text is given the text's boxes from
    // the whole slide, so the still has the same dry text the stage has.
    const effects = (doc.list && doc.list.effects) || [];
    if (effects.some((e) => e.kind === "raindrops2")) {
      const cmds = JSON.parse(app.slideJson(i)).list.cmds || [];
      for (const e of effects) {
        if (e.kind === "raindrops2") e.obstacles = textObstacles(cmds, e.box || [0, 0, doc.width, doc.height]);
      }
    }
    const k = FX_STILL_W / doc.width;
    c.width = FX_STILL_W;
    c.height = Math.round(doc.height * k);
    const f = prepareDisplayList(g, doc, { dpr: k, images: pictures });
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

// The faces a diagram look draws with ({style=cartoon}, …): fetched the first
// time a deck asks for one (app.wantedFaces), not with every page. Until a
// face is here the look's words are set in Open Sans.
const LOOK_FACES = {
  "Gloria Hallelujah": "GloriaHallelujah.ttf",
  "Fjalla One": "FjallaOne-Regular.ttf",
  "Josefin Sans-Bold": "JosefinSans-Bold.ttf",
  "Droid Serif-BoldItalic": "DroidSerif-BoldItalic.ttf",
  // the faces a deck's CSS can name besides Open Sans and Noto Sans
  // (src/PresFonts.rgr), fetched when a deck uses them
  "Droid Serif": "DroidSerif.ttf",
  "Droid Serif-Bold": "DroidSerif-Bold.ttf",
  "Droid Serif-Italic": "DroidSerif-Italic.ttf",
  "Lato": "Lato-Regular.ttf",
  "Lato-Bold": "Lato-Bold.ttf",
  "Lato-Italic": "Lato-Italic.ttf",
  "Lato-BoldItalic": "Lato-BoldItalic.ttf",
};
let lookFacesAsked = "";
const lookFacesHad = new Set();
let pageFaces = [];
function loadLookFaces() {
  const want = app.wantedFaces();
  if (want === lookFacesAsked) return;
  lookFacesAsked = want;
  for (const name of want.split(",")) {
    const file = LOOK_FACES[name];
    if (!file || lookFacesHad.has(name)) continue;
    lookFacesHad.add(name);
    fontBytes(file)
      .then(async (bytes) => {
        const face = new FontFace(name, bytes);
        await face.load();
        document.fonts.add(face);
        // the family the painter names it by must be one it knows is loaded
        pageFaces = pageFaces.concat(name);
        setFontFallback(pageFaces);
        app.attachFont(name, asRangerBuffer(bytes.slice(0)));
        needsPaint = true;
      })
      .catch((e) => console.warn("face not loaded: " + name, e));
  }
}

// The PDF writer's fallback for emoji (monochrome Noto Emoji). Only the
// writer gets it: the screen keeps the browser's own colour emoji. Fetched
// the first time a PDF is made, since it is large (0.9 MB) and most visits
// make none. The dash in the name puts it in the fallback pool.
let emojiFace = null;
function loadEmojiFace() {
  emojiFace ??= fontBytes("NotoEmoji-Regular.ttf")
    .then((bytes) => { app.attachFont("Noto Emoji-Regular", asRangerBuffer(bytes.slice(0))); })
    .catch((e) => { console.warn("emoji face not loaded", e); });
  return emojiFace;
}

// `picked`: only the slides picked on the strip (Ctrl/⌘ or Shift + click),
// named by their numbers
function pickedName() {
  return exportName() + " (" + t("slides") + " " + app.pickList() + ")";
}
async function exportPdf(picked = false) {
  await Promise.all([renderFxStills(), loadEmojiFace()]);
  window.__lastDownload = picked && app.pickCount() > 0
    ? deliver(app.pdfPicked(), pickedName() + ".pdf", "application/pdf")
    : deliver(app.pdf(), exportName() + ".pdf", "application/pdf");
}
// The runs the contrast guard repairs on the stage (a colour that reads, or an
// outline round big letters) are a judgement made while drawing, over the
// pictures under them, so the PPTX would have the theme's colour where the
// slide showed another. Each slide is drawn once here at rest, small, and the
// guard's list goes to the app for the PPTX's runs.
async function judgeExportContrast() {
  app.clearExportContrast();
  if (!autoContrast) return;
  // every chart drawn, also on the slides never brought into view
  app.settleAll();
  const n = app.deck.slideCount();
  const c = document.createElement("canvas");
  const g = c.getContext("webgl2", { antialias: false, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
  if (!g) return;
  for (let i = 0; i < n; i += 1) {
    const doc = atRest(JSON.parse(app.slideJson(i)));
    const k = 640 / doc.width;
    c.width = 640;
    c.height = Math.round(doc.height * k);
    const f = prepareDisplayList(g, doc, { dpr: k, images: pictures, contrastGuard: true, contrastRepair: true });
    const stats = f.draw(null, null);
    f.dispose();
    const low = (stats && stats.lowContrast) || [];
    if (low.length) app.addExportContrast(i, JSON.stringify(low));
  }
  const lose = g.getExtension("WEBGL_lose_context");
  if (lose) lose.loseContext();
}
window.__judgeExportContrast = judgeExportContrast;

async function exportPptx(picked = false) {
  await renderFxStills();
  await judgeExportContrast();
  const some = picked && app.pickCount() > 0;
  window.__lastDownload = deliver(some ? app.pptxPicked() : app.pptx(), (some ? pickedName() : exportName()) + ".pptx",
    "application/vnd.openxmlformats-officedocument.presentationml.presentation");
}
// Word: the blocks only the stage can draw (a diagram, a chart, a list
// figure, a SmartArt) go in as pictures. The app names each one's
// rectangle on its slide and what to draw for it (PresDocx.shotList: a
// diagram whole and still, a chart at rest, else the slide at rest); each is
// drawn here, cut to its rectangle and handed back as a PNG, and the app
// writes the file.
const DOCX_SHOT_W = 1400;
async function renderDocxShots(shots) {
  if (!shots.length) return;
  const c = document.createElement("canvas");
  const g = c.getContext("webgl2", { antialias: true, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
  if (!g) return;
  const cut = document.createElement("canvas");
  const cg = cut.getContext("2d");
  for (const s of shots) {
    const doc = atRest(JSON.parse(app.docxShotJson(s.key)));
    // sharp enough for a page wide picture, within the GPU's limits
    const k = Math.min(4, DOCX_SHOT_W / Math.max(1, s.w), 4096 / doc.width, 4096 / doc.height);
    c.width = Math.round(doc.width * k);
    c.height = Math.round(doc.height * k);
    const f = prepareDisplayList(g, doc, { dpr: k, images: pictures });
    f.draw(null, null);
    const x = Math.max(0, Math.floor(s.x * k));
    const y = Math.max(0, Math.floor(s.y * k));
    const w = Math.min(c.width - x, Math.ceil(s.w * k));
    const h = Math.min(c.height - y, Math.ceil(s.h * k));
    if (w > 0 && h > 0) {
      // GL rows run bottom up
      const up = new Uint8Array(w * h * 4);
      g.readPixels(x, c.height - y - h, w, h, g.RGBA, g.UNSIGNED_BYTE, up);
      const rgba = new Uint8ClampedArray(w * h * 4);
      for (let r = 0; r < h; r++) rgba.set(up.subarray((h - 1 - r) * w * 4, (h - r) * w * 4), r * w * 4);
      for (let p = 3; p < rgba.length; p += 4) rgba[p] = 255;
      cut.width = w;
      cut.height = h;
      cg.putImageData(new ImageData(rgba, w, h), 0, 0);
      const blob = await new Promise((r) => cut.toBlob(r, "image/png"));
      if (blob) app.setDocxShot(s.key, asRangerBuffer(await blob.arrayBuffer()), w, h);
    }
    f.dispose();
  }
  const lose = g.getExtension("WEBGL_lose_context");
  if (lose) lose.loseContext();
}
async function exportDocx(picked = false) {
  const some = picked && app.pickCount() > 0;
  await renderDocxShots(JSON.parse(app.docxBegin(some)));
  window.__lastDownload = deliver(app.docxEnd(), (some ? pickedName() : exportName()) + ".docx",
    "application/vnd.openxmlformats-officedocument.wordprocessingml.document");
}
// A web page: the same reading as Word, the same pictures, one .html file.
async function exportHtml(picked = false) {
  const some = picked && app.pickCount() > 0;
  await renderDocxShots(JSON.parse(app.docxBegin(some)));
  window.__lastDownload = deliver(app.htmlEnd(), (some ? pickedName() : exportName()) + ".html", "text/html;charset=utf-8");
}
// A presentation player: the site's own player page (web/dist/player.html,
// scripts/player.mjs) with this deck in it, one .html file that plays
// offline. Review comments stay out: they are notes on the deck.
async function exportPlayer() {
  const enc = new TextEncoder();
  const files = [];
  for (const f of await docFiles()) {
    if (f.path === REVIEW_PATH) continue;
    if (typeof f.data === "string") files.push({ path: f.path, type: f.type || "", text: f.data });
    else if (isText(f.path, f.type)) files.push({ path: f.path, type: f.type || "", text: await f.data.text() });
    else files.push({ path: f.path, type: f.type || f.data.type || "", b64: base64(new Uint8Array(await f.data.arrayBuffer())) });
  }
  const theme = themeSel.value || "";
  const deck = { name: exportName(), md: app.source(), theme, css: editedCss[theme] ?? null, files };
  const html = playerHtml(await textOf("./player.html"), deck);
  window.__lastDownload = deliver(enc.encode(html), exportName() + "-player.html", "text/html;charset=utf-8");
}
window.__exportPlayer = exportPlayer;
window.__exportDocx = exportDocx;
window.__exportHtml = exportHtml;
window.__renderDocxShots = renderDocxShots;
document.getElementById("docx").addEventListener("click", () => { exportDocx().catch(fail); });
document.getElementById("docxPicked").addEventListener("click", () => { exportDocx(true).catch(fail); });
document.getElementById("html").addEventListener("click", () => { exportHtml().catch(fail); });
document.getElementById("player").addEventListener("click", () => { exportPlayer().catch(fail); });
document.getElementById("htmlPicked").addEventListener("click", () => { exportHtml(true).catch(fail); });
document.getElementById("pdf").addEventListener("click", () => { exportPdf().catch(fail); });
document.getElementById("pptx").addEventListener("click", () => { exportPptx().catch(fail); });
document.getElementById("zip").addEventListener("click", () => { askZip().catch(fail); });
document.getElementById("pdfPicked").addEventListener("click", () => { exportPdf(true).catch(fail); });
document.getElementById("pptxPicked").addEventListener("click", () => { exportPptx(true).catch(fail); });
// Open: a presentation (.md) replaces the deck; data (Excel, CSV, JSON) and
// pictures go to the Files tab as if dropped there. One data file opens the
// import dialog, like a drop on the editor; several are only kept.
filePick.addEventListener("change", async () => {
  const list = [...(filePick.files || [])];
  filePick.value = "";
  const doc = list.find((f) => /\.(md|markdown|txt)$/i.test(f.name));
  if (doc) {
    docName = doc.name.replace(/\.(md|markdown|txt)$/i, "") || "presentation";
    await saveDoc();
    const text = await doc.text();
    beginDoc(text);
    shownDoc(text);
    dropThumbs();
  }
  const rest = list.filter((f) => f !== doc);
  if (rest.length) {
    for (const f of rest) await addDocFile(f, rest.length === 1);
    await saveDoc(true);
    app.showTab("files");
    refreshFiles();
  }
  needsPaint = true;
});

// a file picked for a new deck's data: the import dialog asks what to make
let addAsks = false;
fileAdd.addEventListener("cancel", () => { addAsks = false; });
fileAdd.addEventListener("change", async () => {
  const list = [...(fileAdd.files || [])];
  fileAdd.value = "";
  const ask = addAsks && list.length === 1;
  addAsks = false;
  if (makingDeck) await makingDeck;
  for (const f of list) await addDocFile(f, ask);
  if (list.length) await saveDoc(true);
  refreshFiles();
  needsPaint = true;
});

// A change is saved a moment after it is made, whichever way it came (the
// editor, the chart editor, the theme); and once more as the page goes.
setInterval(() => { saveDoc().catch(fail); }, 1500);
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "hidden") saveDoc().catch(() => {});
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
// the address's #…, or where the page has no address of its own (the
// assistant's preview writes it as srcdoc) the one it was given in
// <meta name="sliqtly-link">
function hashParams() {
  const given = document.querySelector('meta[name="sliqtly-link"]');
  return new URLSearchParams((given ? given.content : location.hash).replace(/^#/, ""));
}

let toastTimer = 0;

function toast(text) {
  app.toast(text);
  needsPaint = true;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(function hide() {
    // its text selected (to be copied): it stays until let go
    if (app.toastHeld()) { toastTimer = setTimeout(hide, 1000); return; }
    app.toast(""); needsPaint = true;
  }, 3200);
}

// Two links to the same deck: one that opens straight into the presentation
// (no editor, no toolbar), one that opens the editor. The dialog shows both
// and copies the one asked for.
// PRO (sliqtly.js), once it has run: module scripts run in order, but a
// caller may come before.
function pro() {
  return window.sliqtly ? Promise.resolve(window.sliqtly)
    : new Promise((ok) => window.addEventListener("sliqtly:ready", () => ok(window.sliqtly), { once: true }));
}

// Signed in to PRO, a deck lives in the cloud as a share (web/sliqtly.js):
// made on its first save, written again a moment after every change, and
// opened from there at /s/{id}?edit, the address the editor then shows. The
// same share is what Share links to and what an assistant edits (mcp-go/).
function stampOf(f) {
  return (f.size ?? "") + ":" + (f.updated ?? "");
}
function cloudReady() {
  return !!(vfs && !viewer && doc.persisted && !doc.loading && window.sliqtly?.user?.());
}
let cloudTimer = 0;
function cloudSoon() {
  if (!cloudReady() || doc.cloudHalt) return;
  clearTimeout(cloudTimer);
  cloudTimer = setTimeout(() => { cloudTimer = 0; cloudSync().catch(cloudTrouble); }, 2000);
}
// sliqtly.com's editor keeps nothing past the page: leaving it before the
// deck is in the cloud (not saved there at all, or its last change on the
// way) asks first.
window.addEventListener("beforeunload", (e) => {
  if (!CLOUD_ONLY || viewer || !doc.persisted || doc.loading) return;
  const changed = savedText !== null && app.source() !== savedText;
  if (!doc.cloud || cloudTimer || cloudBusy || changed) e.preventDefault();
});
let cloudBusy = null;
let cloudWarned = false;
let cloudError = "";
let cloudNoRight = false; // cloudError is the license's answer (sliqtly.js editRight)
function cloudTrouble(e) {
  console.warn("cloud save failed", e);
  cloudNoRight = e?.code === "no-edit-right";
  cloudError = cloudNoRight ? e.message : String(e?.code || e?.message || e);
  refreshFiles();
  if (cloudWarned) return;
  cloudWarned = true;
  toast(cloudNoRight ? e.message : t("Saving to the cloud failed: ") + (e?.code || e?.message || String(e)) + ". " + t("The presentation is kept in this browser."));
}
// The deck as the cloud keeps it, and a signature of it.
async function cloudDeck() {
  const key = themeSel.value || "";
  const deck = {
    deckId: doc.id, name: exportName(), md: app.source(), theme: key,
    css: key in editedCss ? editedCss[key] : null,
    files: (await docFiles()).map((f) => ({ path: f.path, type: f.type || "", data: f.data, stamp: stampOf(f) })),
  };
  // in a room the room writes the Markdown: only the rest goes from here
  const md = collabOn() ? "" : deck.md;
  return { deck, sig: JSON.stringify([deck.name, md, deck.theme, deck.css, deck.files.map((f) => f.path + "=" + f.stamp)]) };
}
// Writes the deck to its share, making the share first if it has none.
// Resolves to the share's id.
async function cloudSync() {
  if (cloudBusy) await cloudBusy.catch(() => {});
  if (!cloudReady()) return null;
  clearTimeout(cloudTimer);
  const p = window.sliqtly;
  const { deck, sig } = await cloudDeck();
  if (doc.cloud && (sig === doc.cloudSig || doc.cloudHalt)) return doc.cloud;
  const which = doc.id;
  // the deck `which` is this share's from here on, in this browser too,
  // whichever deck is open by then: `files` are the paths the share has
  const keepShare = async (id, files) => {
    const kept = { cloud: id, cloudMd: deck.md, cloudCss: deck.css, cloudTheme: deck.theme, cloudFiles: files };
    if (doc.id === which) Object.assign(doc, kept);
    const cur = await vfs.getDoc(which);
    if (cur) await vfs.putDoc({ ...cur, ...kept });
  };
  cloudBusy = (async () => {
    let id = doc.cloud;
    let files = deck.files.map((f) => f.path);
    let entries = null;
    let sent = null;
    if (!id) {
      // The share is kept with the deck as soon as it is made, before its
      // files go: while they go (a few big pictures take a while) the deck
      // is not a second, empty one in the cloud's list, and if they do not
      // all go, the next save sends them again to this share
      const made = async (made) => {
        await keepShare(made, []);
        if (doc.id === which) {
          doc.cloudStamps = new Map();
          plainAddress();
        }
      };
      try {
        id = await p.share(deck, made);
      } catch (e) {
        // made, but a file did not go: kept already, unless made() was not
        // reached
        if (e?.shareId && (await vfs.getDoc(which))?.cloud !== e.shareId) await made(e.shareId);
        throw e;
      }
    } else {
      try {
        sent = new Set(deck.files.filter((f) => doc.cloudStamps.get(f.path) !== f.stamp).map((f) => f.path));
        entries = await p.saveShare(id, deck, { md: doc.cloudMd, stamps: doc.cloudStamps, collab: collabOn() });
        files = entries.map((f) => f.path);
      } catch (e) {
        if (e?.code !== "changed-elsewhere") throw e;
        // changed elsewhere (another device, an assistant) since this page
        // read it: put together with this one here, then written
        if (doc.id === which) setTimeout(() => checkElsewhere(), 0);
        return id;
      }
    }
    await keepShare(id, files);
    if (doc.id !== which) return id; // another deck was opened meanwhile
    doc.cloudSig = sig;
    doc.cloudStamps = new Map(deck.files.map((f) => [f.path, f.stamp]));
    if (entries) doc.cloudSeen = seenAfterSave(entries, doc.cloudSeen, sent);
    cloudWarned = false;
    cloudError = "";
    cloudNoRight = false;
    plainAddress();
    return id;
  })();
  try { return await cloudBusy; } finally {
    cloudBusy = null;
    pushVersions();
  }
}
window.addEventListener("sliqtly:user", () => cloudSoon());

// A share's files, fetched all at once (one after another, each waited a
// round trip: slow over a VPN or far away), in their order; each with its
// data or the error it got.
function shareFiles(files, init) {
  return Promise.all((files || []).map(async (f) => {
    try {
      const res = await fetch(f.url, init);
      if (!res.ok) throw new Error("HTTP " + res.status);
      return { f, data: isText(f.path, f.type) ? await res.text() : await res.blob() };
    } catch (error) {
      return { f, error };
    }
  }));
}

// Opens the signed-in owner's deck from its share (/s/{id}?edit): the cloud
// has the latest, an assistant's changes included. Kept in this browser
// under the id it had here, or a new one. False when it is not theirs.
async function openOwnCloud(id) {
  try {
    return await openOwnCloudNow(id);
  } catch (e) {
    if (doc.loading) shownDoc(doc.openedText);
    throw e;
  }
}
async function openOwnCloudNow(id) {
  const p = await pro();
  const who = await Promise.race([p.signedIn(), new Promise((ok) => setTimeout(() => ok(null), 8000))]);
  if (!who || !vfs) return false;
  const shared = await p.loadShare(id);
  // the owner's, or one its owner invited this user to edit (firestore.rules)
  const mine = typeof p.mayChange === "function" ? p.mayChange(shared) : shared.owner === who.uid;
  if (!shared || !mine) return false;
  const local = (await vfs.listDocs()).find((d) => d.cloud === id);
  // changes made here that the cloud does not have yet, and nobody changed
  // it since: this browser's copy is the newer, and goes up on the next save
  if (local && local.md !== local.cloudMd && shared.md === local.cloudMd) return openDoc(local.id);
  await leaveDoc();
  beginDoc(shared.md || "");
  doc.src = "cloud:" + id;
  doc.id = local?.id || newId();
  // the cloud's files replace this browser's (its versions stay), but not
  // one the share never got: a file still on its way when the deck was
  // left, or one that failed. One the share had and has no more was
  // removed there. Not known what the share had (a deck kept before this
  // was noted): every file of this browser's that the share lacks stays.
  const unsent = [];
  if (local) {
    const there = new Set((shared.files || []).map((f) => f.path));
    const had = new Set(local.cloudFiles || []);
    for (const f of await vfs.listFiles(local.id)) if (!there.has(f.path) && !had.has(f.path)) unsent.push(f);
    await vfs.deleteDoc(local.id, true);
  }
  doc.created = local?.created || Date.now();
  if (shared.theme != null) {
    themeSel.value = shared.theme;
    app.setStyleSheet(shared.theme ? themeCss[shared.theme] || "" : "");
  }
  if (shared.css != null) {
    editedCss[themeSel.value || ""] = shared.css;
    app.setStyleSheet(shared.css);
  }
  const missing = [];
  for (const { f, data, error } of await shareFiles(shared.files)) {
    try {
      if (error) throw error;
      const rec = { doc: doc.id, path: f.path, type: f.type, size: f.size, data, updated: Date.now() };
      pending.set(rec.path, rec);
      await useFile(rec);
    } catch (e) {
      console.warn("cloud file not loaded: " + f.path, e);
      missing.push(f.path);
    }
  }
  if (missing.length) toast(t("Some pictures or data files of this presentation could not be loaded: ") + missing.join(", "));
  // the files that did come are what the share has; one that did not is
  // not sent back, so the share keeps it
  doc.cloudStamps = new Map([...pending.values()].map((f) => [f.path, stampOf(f)]));
  doc.cloudSeen = seenAfterSave((shared.files || []).filter((f) => pending.has(f.path)));
  // this browser's own, not in the share: kept, and sent on the next save
  for (const f of unsent) {
    const rec = { ...f, doc: doc.id };
    pending.set(rec.path, rec);
    await useFile(rec);
  }
  docName = shared.name || "presentation";
  shownDoc(shared.md || "");
  doc.cloud = id;
  doc.cloudFiles = (shared.files || []).map((f) => f.path);
  // on a server of one's own the rooms panel shows the deck's room
  if (ownServer() && shared.room) {
    roomShown = shared.room;
    keepRooms((s) => touchRoom(s, shared.room));
  }
  doc.cloudMd = shared.md || "";
  doc.cloudCss = shared.css ?? null;
  doc.cloudTheme = shared.theme || "";
  await saveDoc(true);
  // with files of this browser's the share lacks, the next sync sends them
  doc.cloudSig = unsent.length ? "" : (await cloudDeck()).sig;
  if (unsent.length) cloudSoon();
  if (shared.head) await followCloudHead(shared.head).catch((e) => console.warn("versions not read", e));
  plainAddress();
  dropThumbs();
  needsPaint = true;
  return true;
}

// --- versions, and edits made in two places at once (web/versions.js) ---------------
// The open deck's history: commits of its files, kept in this browser and,
// for a PRO deck, beside its share. A version is made when the deck was
// created, when 10 or more lines changed since the last one or a file did,
// after 10 minutes of smaller changes, when another deck is opened, around
// a merge and a restore, and from Version history → Save version.
//
// The same deck open in two places: another tab of this browser saves
// through the same record (saveDoc compares before it writes; the tabs also
// tell each other at once), another device or an assistant through the
// share (read when the window gets the focus and every minute while it is
// seen). Changed only there: taken here. Changed on both sides: merged
// against what both started from; where both changed the same lines the
// merge dialog asks.
let versions = null;
let merging = false;
let filesAtCommit = null;
function deckVersions() {
  if (!vfs || viewer || !doc.persisted || doc.loading) return null;
  if (!versions || versions.docId !== doc.id) {
    versions = new DeckHistory({
      vfs, docId: doc.id, cloud: () => doc.cloud,
      pro: () => (window.sliqtly?.user?.() ? window.sliqtly : null),
    });
  }
  return versions;
}
window.__versions = () => deckVersions();

// The deck as versions see it (versions.js snapshots).
async function workingCopy() {
  const key = themeSel.value || "";
  return {
    name: exportName(), md: app.source(), theme: key, css: key in editedCss ? editedCss[key] : null,
    files: (await docFiles()).map((f) => ({ path: f.path, type: f.type || "", data: f.data, stamp: stampOf(f), orig: f.orig })),
  };
}

// alone: not with another tab's newer head as a parent (a copy of this
// one's own before a merge)
async function commitVersion(message, alone = false, extra = [], force = false) {
  const h = deckVersions();
  if (!h) return null;
  const id = await h.commit(await workingCopy(), message, extra, alone, force);
  if (id) pushVersions();
  return id;
}

let versionsPush = Promise.resolve();
function pushVersions() {
  const h = deckVersions();
  if (!h || !doc.cloud || doc.cloudHalt || cloudNoRight || !window.sliqtly?.user?.()) return versionsPush;
  versionsPush = versionsPush.then(async () => {
    if (cloudBusy) await cloudBusy.catch(() => {});
    // the share's head moved elsewhere: the copies are merged first
    if (!(await h.pushCloud(null))) setTimeout(() => checkElsewhere(), 0);
  }).catch((e) => console.warn("versions not sent to the cloud", e));
  return versionsPush;
}

async function maybeCommit() {
  const h = deckVersions();
  if (!h || merging || saving) return;
  await h.ready;
  const files = (await docFiles()).map((f) => f.path + "=" + stampOf(f) + (f.orig ? "+" + f.orig.plans.length : "")).join("\n");
  if (!h.head) {
    filesAtCommit = files;
    await commitVersion("@created");
    return;
  }
  const head = h.repo.commit(h.head);
  const s = lineStats(h.repo.fileText(h.head, "deck.md"), app.source());
  const age = Date.now() - (Date.parse(head?.time || "") || 0);
  const filesMoved = filesAtCommit != null && files !== filesAtCommit;
  filesAtCommit ??= files;
  if (s.added + s.removed >= 10 || filesMoved || age >= 10 * 60 * 1000) {
    filesAtCommit = files;
    await commitVersion("@auto");
  }
}
setInterval(() => { maybeCommit().catch((e) => console.warn("no version kept", e)); }, 30000);

// --- the other copy
const sameCopy = (a, b) => (a.md ?? "") === (b.md ?? "") && (a.css ?? null) === (b.css ?? null) && (a.theme || "") === (b.theme || "");
function changedElsewhere(rec) {
  if (savedText == null) return false;
  return !sameCopy({ md: rec.md, css: rec.css, theme: rec.theme }, { md: savedText, css: savedCss, theme: savedTheme });
}

function editorCopy() {
  const key = themeSel.value || "";
  return { md: app.source(), css: key in editedCss ? editedCss[key] : null, theme: key };
}

// The editor shows `c`: { md, css, theme }.
function applyCopy(c) {
  const key = c.theme || "";
  if ([...themeSel.options].some((o) => o.value === key)) themeSel.value = key;
  for (const k of Object.keys(editedCss)) delete editedCss[k];
  if (c.css != null) editedCss[key] = c.css;
  useTheme(themeSel.value);
  if ((c.md ?? "") !== app.source()) app.setSource(c.md ?? "");
  dropThumbs();
  needsPaint = true;
}

// `theirs` in the editor: as it is when nothing was changed here since
// `base`, else merged with this one. → true when this one had changes.
async function takeCopy(base, theirs, where) {
  const mine = editorCopy();
  if (sameCopy(mine, base) || sameCopy(mine, theirs)) {
    applyCopy(theirs);
    return false;
  }
  merging = true;
  try {
    // this one's changes as they were, a version to go back to
    await commitVersion("@before-merge", true).catch(() => null);
    const r = mergeCopies(base, mine, theirs);
    let snap = r.snap;
    if (r.conflicts.length) snap = resolveMerge(r, await askMerge(r, where)).snap;
    else toast(t("Changes made elsewhere were combined with yours."));
    applyCopy({ md: snap.md ?? "", css: snap.css ?? null, theme: snap.theme ?? mine.theme });
  } finally {
    merging = false;
  }
  return true;
}

// What another tab of this browser saved, taken here (runs one at a time
// with the saves: exclusive).
async function takeLocal() {
  const cur = await vfs.getDoc(doc.id);
  if (!cur || !changedElsewhere(cur)) return;
  const base = { md: savedText, css: savedCss, theme: savedTheme };
  const theirs = { md: cur.md ?? "", css: cur.css ?? null, theme: cur.theme || "" };
  const mergedHere = await takeCopy(base, theirs, "tab");
  // the record is what this one stands on now
  savedText = theirs.md;
  savedVersion = -1;
  savedCss = theirs.css;
  savedTheme = theirs.theme;
  if (cur.cloud) doc.cloud = cur.cloud;
  if (cur.cloudMd != null) {
    doc.cloudMd = cur.cloudMd;
    doc.cloudCss = cur.cloudCss ?? null;
    doc.cloudTheme = cur.cloudTheme ?? null;
  }
  for (const f of await vfs.listFiles(doc.id)) if (usedStamps.get(f.path) !== stampOf(f)) await useFile(f);
  refreshFiles();
  if (mergedHere) {
    await saveDocNow();
    await commitVersion("@merge");
  }
}

// PRO: what the share has now, when another device or an assistant changed it.
let cloudChecking = null;
let cloudAgain = false;
async function cloudCheck() {
  if (!cloudReady() || !doc.cloud || doc.cloudHalt || merging) return;
  // asked while a look is under way: one more after it, which sees what
  // changed meanwhile
  if (cloudChecking) {
    cloudAgain = true;
    return cloudChecking;
  }
  cloudChecking = (async () => {
    if (cloudBusy) await cloudBusy.catch(() => {});
    const p = window.sliqtly;
    const which = doc.id;
    // Firestore waits quietly while it cannot reach the server
    const late = new Promise((_, no) => setTimeout(() => no(Object.assign(new Error("timeout"), { code: "timeout" })), 20000));
    const s = await Promise.race([p.readHead(doc.cloud), late]);
    if (!s || doc.id !== which || merging) return;
    const theirs = { md: s.md ?? "", css: s.css ?? null, theme: s.theme || "" };
    const base = { md: doc.cloudMd ?? "", css: doc.cloudCss ?? null, theme: doc.cloudTheme ?? theirs.theme };
    // in a room the Markdown arrives as edits (web/collab.js): not compared
    if (collabOn()) theirs.md = base.md;
    const moved = !sameCopy(theirs, base);
    const reviewMoved = await takeCloudReview(s);
    const filesMoved = await takeCloudFiles(s);
    if (filesMoved) await loadReview(true);
    let mergedHere = false;
    if (moved) mergedHere = await exclusive(() => takeCopy(base, theirs, "cloud"));
    if (moved || filesMoved) {
      doc.cloudMd = theirs.md;
      doc.cloudCss = theirs.css;
      doc.cloudTheme = theirs.theme;
      // written next as it is now (merged), over what was just read
      doc.cloudSig = "";
      await saveDoc(true);
      refreshFiles();
    }
    if (s.head) await followCloudHead(s.head, mergedHere);
    if (moved || filesMoved || reviewMoved) cloudSoon();
  })();
  try {
    return await cloudChecking;
  } finally {
    cloudChecking = null;
    if (cloudAgain) {
      cloudAgain = false;
      setTimeout(() => { cloudCheck().catch((e) => console.warn("could not compare with the cloud copy", e)); }, 0);
    }
  }
}

// Files the share has that were added, written again or removed elsewhere
// since this page last wrote or read it (web/sharefiles.js): a drawing
// someone changed on a slide reaches the others in the room, and an old
// copy here does not go up over it.
async function takeCloudFiles(s) {
  const local = new Map((await docFiles()).map((f) => [f.path, stampOf(f)]));
  const plan = planFiles({ remote: s.files || [], local, stamps: doc.cloudStamps, seen: doc.cloudSeen });
  let moved = false;
  const failed = new Set();
  for (const f of [...plan.add, ...plan.update]) {
    try {
      const res = await fetch(f.url, { cache: "no-cache" });
      if (!res.ok) throw new Error("HTTP " + res.status);
      const data = isText(f.path, f.type) ? await res.text() : await res.blob();
      const rec = { doc: doc.id, path: f.path, type: f.type, size: f.size, data, updated: Date.now() };
      await vfs.putFile(rec);
      await useFile(rec);
      doc.cloudStamps.set(f.path, stampOf(rec));
      moved = true;
    } catch (e) {
      console.warn("cloud file not loaded: " + f.path, e);
      failed.add(f.path);
    }
  }
  for (const path of plan.remove) {
    await vfs.deleteFile(doc.id, path);
    moved = true;
  }
  const remote = new Set((s.files || []).map((f) => f.path));
  for (const path of [...doc.cloudStamps.keys()]) if (!remote.has(path)) doc.cloudStamps.delete(path);
  // one that did not come is looked at again next time
  for (const path of failed) {
    if (doc.cloudSeen.has(path)) plan.seen.set(path, doc.cloudSeen.get(path));
    else plan.seen.delete(path);
  }
  doc.cloudSeen = plan.seen;
  doc.cloudFiles = [...remote];
  if (moved) dropThumbs();
  return moved;
}

// The share's newest version: this one follows it when it comes after this
// one's, or makes a version with both as parents.
async function followCloudHead(head, merged = false) {
  const h = deckVersions();
  if (!h) return;
  await h.ready;
  if (head === h.head || head === h.cloudHead) return;
  await h.ensureLine(head);
  if (!h.repo.has(head)) return;
  if (!h.head || h.repo.isAncestor(h.head, head)) {
    // nothing in this one's versions that the share lacks: its line is
    // this one's, and what is in the editor now a version after it
    h.head = head;
    h.cloudHead = head;
    await h.persist();
    await commitVersion(merged ? "@merge" : "@auto");
    return;
  }
  if (h.repo.isAncestor(head, h.head)) return; // this one is newer: it goes up next
  await commitVersion("@merge", false, [head]);
}

async function checkElsewhere() {
  if (!vfs || viewer || merging || !doc.persisted || doc.loading) return;
  try {
    // another tab of this browser in the same room has its edits there too
    if (!collabOn()) await exclusive(takeLocal);
    await cloudCheck();
  } catch (e) {
    console.warn("could not compare with the other copy", e);
  }
}
window.__checkElsewhere = () => checkElsewhere();
// a server of one's own is back after an update or a restart: what could
// not be saved meanwhile goes now (mcp-go/assets/sliqtly-local.js)
window.__cloudSoon = () => cloudSoon();

const tabs = typeof BroadcastChannel === "function" ? new BroadcastChannel("sliqtly-docs") : null;
tabs?.addEventListener("message", (ev) => {
  if (ev.data?.doc === doc.id && ev.data.tab !== TAB) checkElsewhere();
});
function tellTabs() {
  tabs?.postMessage({ doc: doc.id, tab: TAB });
}
window.addEventListener("focus", () => checkElsewhere());
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") checkElsewhere();
});
setInterval(() => { if (document.visibilityState === "visible") checkElsewhere(); }, 60000);

// --- Version history
async function restoreVersion(id) {
  const h = deckVersions();
  if (!h) return;
  await saveDoc();
  await commitVersion("@auto");
  const snap = await h.checkout(id, renderPlans);
  if (!snap) throw new Error(t("This version is not here or in the cloud."));
  const when = h.repo.commit(id)?.time || "";
  const now = h.head ? h.repo.tree(h.repo.commit(h.head).tree) : null;
  merging = true;
  try {
    applyCopy({ md: snap.md, css: snap.css, theme: snap.theme });
    const keep = new Set(snap.files.map((f) => f.path));
    for (const f of await docFiles()) {
      if (keep.has(f.path)) continue;
      await vfs.deleteFile(doc.id, f.path);
      chartFiles.delete(f.path);
    }
    for (const f of snap.files) {
      const at = now?.find(f.path);
      if (at && at.blob === f.blob && (at.recipe || "") === (f.recipe || "")) continue; // as it is
      const size = typeof f.data === "string" ? new TextEncoder().encode(f.data).length : f.data.size;
      const rec = { doc: doc.id, path: f.path, type: f.type, size, data: f.data, updated: Date.now() };
      if (f.orig) rec.orig = { ...f.orig, stamp: Date.now() };
      await vfs.putFile(rec);
      await useFile(rec);
    }
  } finally {
    merging = false;
  }
  await saveDoc(true);
  filesAtCommit = null;
  await commitVersion("@restore " + when);
  refreshFiles();
  toast(t("Version restored. The one before it is in the history."));
}

async function openHistory() {
  if (!vfs || viewer) return;
  if (!doc.persisted) {
    toast(t("Versions are kept once the presentation is changed."));
    return;
  }
  await saveDoc();
  const h = deckVersions();
  if (!h) return;
  showHistory({
    entries: async () => {
      let remote = [];
      if (doc.cloud && window.sliqtly?.readHead) {
        const s = await window.sliqtly.readHead(doc.cloud).catch(() => null);
        remote = s?.log || [];
      }
      const list = await h.log(remote);
      return list.map((e) => ({ ...e, current: e.id === h.head }));
    },
    changes: (id) => h.changes(id),
    diff: (id, path) => h.diffText(id, path),
    checkout: (id) => h.checkout(id, renderPlans),
    restore: (id) => restoreVersion(id),
    save: async (message) => {
      await saveDoc(true);
      const id = await commitVersion(message || "@auto", false, [], !!message);
      toast(id ? t("Version saved.") : t("Nothing changed since the last version."));
    },
  });
}
document.getElementById("history").addEventListener("click", () => { openHistory().catch(fail); });

// Share, signed in: the deck's own share, saved first. → the share's id
async function shareCloud() {
  await saveDoc(true);
  // Firestore waits quietly when it cannot write (no database yet, rules
  // that refuse): a share that has not happened in 20 s has failed
  const timeout = new Promise((_, no) => setTimeout(() => no(Object.assign(new Error("timeout"), { code: "timeout" })), 20000));
  return Promise.race([timeout, cloudSync().then((id) => id || Promise.reject(new Error("not saved")))]);
}

function cloudFailure(e) {
  console.warn("cloud sharing failed", e);
  const why = e?.code === "permission-denied" || e?.code === "storage/unauthorized" ? t("the cloud refused it (rules not set up?)")
    : e?.code === "timeout" ? t("no answer from the cloud (is Firestore created?)")
    : (e?.code || e?.message || String(e));
  return t("Cloud sharing failed: ") + why + ". " + t("Sharing the text in the link instead.");
}

function showShare(showUrl, editUrl, note, viewUrl = "", live = false) {
  app.setShareView(viewUrl, live);
  app.openShare(showUrl, editUrl, note);
  window.__lastShare = editUrl;
  window.__lastShareShow = showUrl;
  window.__lastShareView = viewUrl;
  needsPaint = true;
}

// The dialog opens at once with the text packed into the link; signed in to
// PRO, it says a short link is on its way and shows it when the cloud has it.
// the addresses the owner invited to edit the deck open now, as Share last
// read them (People who can edit… starts from these)
let shareEditors = [];
// Share → People who can edit…: the window, then the list saved on the share
function openInvite() {
  const u = window.sliqtly?.user?.();
  if (!doc.cloud || !u) return;
  if (app.openInvite(shareEditors.join("\n"), (u.email || "").toLowerCase())) needsPaint = true;
}
async function saveEditors(text) {
  const list = text.split("\n").map((a) => a.trim()).filter(Boolean);
  await window.sliqtly.setEditors(doc.cloud, list);
  shareEditors = list;
  toast(list.length
    ? t("{n} people can edit this presentation besides you.").replace("{n}", String(list.length))
    : t("Only you edit this presentation."));
}
// Share → Stop sharing: the viewing link stops showing it
async function stopSharing() {
  if (!doc.cloud) return;
  await window.sliqtly.stopSharing(doc.cloud);
  toast(t("The presentation link no longer shows this presentation. Share makes a new one."));
}

async function shareLink() {
  const text = app.source();
  const code = await packText(text);
  const q = new URLSearchParams();
  q.set("md", code);
  if (themeSel.value) q.set("theme", themeSel.value);
  if ((themeSel.value || "") in editedCss) q.set("css", await packText(editedCss[themeSel.value || ""]));
  // the page's own address, also when it is at /s/{id}?edit
  const base = new URL(".", document.baseURI).href;
  const editUrl = base + "#" + q.toString();
  q.set("mode", "show");
  const showUrl = base + "#" + q.toString();
  // the slides picked on the strip: a link of their own, which carries only
  // their text, or (in the cloud) names them in the deck's short link
  const picked = app.pickCount() > 0;
  const keys = picked ? app.pickKeys() : "";
  let viewUrl = "";
  if (picked) {
    q.set("md", await packText(app.pickViewText()));
    viewUrl = base + "#" + q.toString();
  }
  const pictures = /\]\(media\//.test(text) ? t(" Attached images are not included in the link.") : "";
  const textNote = editUrl.length + t(" characters.") + pictures;
  if (!window.sliqtly?.user?.()) {
    showShare(showUrl, editUrl, textNote, viewUrl);
    return;
  }
  const p = window.sliqtly;
  // in the cloud editor: the presentation's own id is never handed out.
  // Its owner shares a viewing link of its own; someone invited to edit it
  // shares nothing
  if (typeof p.viewLink === "function") {
    app.setShareEditors(-1);
    showShare("", "", t("Creating a short link in the cloud…"), "");
    try {
      const id = await shareCloud();
      const cur = await p.loadShare(id);
      if (cur && cur.owner !== p.user()?.uid) {
        app.closeShare?.();
        toast(t("Only the owner of this presentation shares it. You were invited to edit it."));
        return;
      }
      const link = await p.viewLink(id);
      const short = location.origin + "/s/" + link;
      const view = picked ? short + "?slides=" + encodeURIComponent(keys) : "";
      shareEditors = cur?.editors || [];
      app.setShareEditors(shareEditors.length);
      showShare(short, "", t("A link of its own that shows the presentation, with its images and data, as you change it. Anyone with the link can view it; it never lets anyone edit."), view, true);
    } catch (e) {
      showShare("", "", cloudFailure(e), "");
    }
    return;
  }
  showShare(showUrl, editUrl, t("Creating a short link in the cloud…"), viewUrl);
  try {
    const id = await shareCloud();
    // the deck is private in the cloud until shared: Share opens it to
    // anyone with the link
    await window.sliqtly.setVisibility(id, "link");
    const short = location.origin + "/s/" + id;
    const view = picked ? short + "?slides=" + encodeURIComponent(keys) : "";
    showShare(short, short + "?edit", t("A short link to a copy in the cloud, with its images and data. Anyone with the link can view it; only you can change the original."), view, true);
  } catch (e) {
    showShare(showUrl, editUrl, cloudFailure(e) + " " + textNote, viewUrl);
  }
}

// Edit in Claude: the assistant opens with a prompt that names the
// deck, and edits it through the Sliqtly connector (mcp-go/): get_presentation
// reads a share, update_presentation saves it when the assistant is signed
// in as the share's owner. So the deck handed over
// is a share: the one this page was opened from when it is the reader's own
// and unchanged, else a fresh share of the deck (signed in). Signed out, the
// Markdown goes in the prompt for create_presentation. The changes land in
// the share, not in this page's deck: /s/{id}?edit opens them here again.
let originShare = null;
// the links in the prompt: always the site's own address, also when the
// page was opened at sliqtly.web.app or on a local server
// (a server of one's own names its address in <meta name="sliqtly-site">)
const SITE = document.querySelector('meta[name="sliqtly-site"]')?.content || "https://sliqtly.com";
const AI = {
  claude: (q) => "https://claude.ai/new?q=" + encodeURIComponent(q),
};
// what fits in an address with room to spare
const AI_MAX_PROMPT = 6000;

async function editInAI(which) {
  // opened now, while the press still counts as one; pointed at the
  // assistant once the prompt is ready
  const win = window.open("", "_blank");
  const go = (url) => {
    if (win && !win.closed) {
      win.opener = null;
      win.location.href = url;
    } else {
      window.open(url, "_blank", "noopener");
    }
  };
  try {
    const text = app.source();
    const user = window.sliqtly?.user?.();
    let id = null;
    if (user) {
      if (!doc.cloud) toast(t("Saving a copy in the cloud for the assistant…"));
      id = await shareCloud();
    } else if (originShare && originShare.md === text) {
      id = originShare.id;
    }
    const connect = t("If you have no Sliqtly tools, tell me to add the Sliqtly connector: ") + SITE + "/connect.html";
    let prompt;
    if (id) {
      prompt = t("Edit my Sliqtly presentation {id} ({link}) with the Sliqtly connector. Load it with get_presentation (deck_id {id}), summarize it briefly and ask what to change. Save each change with update_presentation (deck_id {id}). If saving is refused, tell me why and ask before making a copy: a copy made with create_presentation is a separate presentation with its own link, and {link} stays as it was. After saving, give me the link {edit} to open it in the editor.")
        .replaceAll("{id}", id).replaceAll("{link}", SITE + "/s/" + id).replaceAll("{edit}", SITE + (SITE === "https://sliqtly.com" || EDITOR_ROOT ? "/editor/d/" + id : "/s/" + id + "?edit"));
    } else {
      prompt = t("Make this Markdown a Sliqtly presentation with create_presentation from the Sliqtly connector (theme {theme}), give me its link and ask what to change. Save later changes with update_presentation.")
        .replaceAll("{theme}", themeSel.value || "-");
      // data files stay in this browser: name them, so the assistant asks
      // for them as attachments and keeps them with files / write_workbook
      const data = (await docFiles()).map((f) => f.path).filter((p) => p.startsWith("data/"));
      if (data.length) {
        prompt += " " + t("The deck also reads these data files, which this message does not carry: {files}. Ask me to attach them here, then keep them with the presentation (files in create_presentation, or write_workbook for a tidied workbook).")
          .replaceAll("{files}", data.join(", "));
      }
      prompt += "\n\n```markdown\n" + text + "\n```";
      if (prompt.length > AI_MAX_PROMPT) {
        if (win) win.close();
        toast(t("This presentation is too long to hand over in a link. Sign in with PRO first."));
        return;
      }
      if (/\]\(media\//.test(text)) toast(t("Pictures are not handed over without PRO sign-in."));
    }
    go(AI[which](prompt + "\n\n" + connect));
  } catch (e) {
    if (win) win.close();
    console.warn("handing over to the assistant failed", e);
    toast(t("Could not save a copy for the assistant: ") + (e?.code || e?.message || String(e)));
  }
}
document.getElementById("aiClaude").addEventListener("click", () => { editInAI("claude"); });

// Text to the clipboard: the async API, else a selected textarea; -> copied.
async function writeClip(text) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch (_) {
    const ta = document.createElement("textarea");
    ta.value = text;
    document.body.append(ta);
    ta.select();
    const copied = !!(document.execCommand && document.execCommand("copy"));
    ta.remove();
    focusKeys(app.focusTarget());
    return copied;
  }
}

// A copy button in the share dialog (drawn on the canvas): the browser copies.
let copiedTimer = 0;
async function copyShare(which) {
  const text = which === "show" ? window.__lastShareShow : which === "view" ? window.__lastShareView : window.__lastShare;
  const copied = await writeClip(text);
  app.shareCopied(which, copied);
  needsPaint = true;
  clearTimeout(copiedTimer);
  copiedTimer = setTimeout(() => { app.shareCopied("", true); needsPaint = true; }, 1600);
}

// --- the end of a presentation -------------------------------------------------
// Past the last slide (PresApp.atEnd) a panel says so and offers the way on:
// the first slide, the slide before, or out. In the editor out ends the
// presentation; a shared one has nowhere to go back to, so there it closes
// the panel (and full screen) and leaves the last slide showing.
const endPanel = document.getElementById("endPanel");
function syncEndPanel() {
  const on = app.atEnd();
  if (on === !endPanel.hidden) return;
  endPanel.hidden = !on;
  document.body.classList.toggle("ended", on);
  document.getElementById("endExit").textContent = viewer ? t("✕ Close") : t("✕ Exit");
}
function endAction(fn) {
  fn();
  afterInput();
  syncEndPanel();
  if (!viewer && !isCoarse()) keys.focus({ preventScroll: true });
}
document.getElementById("endRestart").addEventListener("click", () => endAction(() => app.restart()));
// the first step back only leaves the end; the second is the slide before
document.getElementById("endPrev").addEventListener("click", () => endAction(() => { app.prev(); app.prev(); }));
document.getElementById("endExit").addEventListener("click", () => endAction(() => {
  if (!viewer) return app.endPresent();
  app.prev();
  if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
}));

// --- a shared presentation (#…&mode=show) ---------------------------------------
// The slides only: no toolbar, no editor, and no way back to one. Full screen
// is offered, not forced — a browser gives it only to a tap of the viewer's own.
let viewer = false;
let idleTimer = 0;
// A finger rather than a mouse; followed when it changes (a tablet with a
// keyboard attached), for the sheets' `@media (pointer: coarse)`.
const coarseQuery = window.matchMedia ? window.matchMedia("(pointer: coarse)") : null;
function isCoarse() { return !!(coarseQuery && coarseQuery.matches); }
if (coarseQuery && coarseQuery.addEventListener) {
  coarseQuery.addEventListener("change", () => { app.setCoarse(isCoarse()); needsPaint = true; });
}
// `from`: what is shown ({ from: "link" } or { from: "share" }), which decides
// whether Sliqtly's intro plays first (web/brand.js).
function enterViewer(from) {
  viewer = true;
  document.body.classList.add("viewer");
  // the slide the link names, read now: the page writes its own into the
  // address while the intro plays
  const n = parseInt(hashParams().get("slide") || "", 10);
  const begin = () => beginShow(n > 1 ? n - 1 : 0);
  if (!wantsIntro(from)) begin();
  // the page still loading: the intro follows the loader (start)
  else if (window.__pageStarted) playIntro().then(begin);
  else introPending = begin;
  wakeViewer();
}
function beginShow(slide) {
  if (slide > 0) app.selectSlide(slide);
  app.present(slide === 0);
  handleRequests();
  needsPaint = true;
}
// Sliqtly's intro on a shared presentation (web/brand.js): the logo turns
// and the name shows, then the slides begin, so the first slide's animation
// is seen. A click, a tap or a key skips it, and is not also taken as "next".
let introPending = null; // what begins the show once the intro has played
function hideIntro() {
  const el = document.getElementById("brandIntro");
  if (!el || el.hidden) return;
  el.classList.remove("on");
  el.classList.add("out");
  setTimeout(() => { el.hidden = true; el.classList.remove("out"); }, 350);
}
// Another deck being opened (a tab of the presentations' row, the
// presentations window). The logo is the page's start only: shown again
// for a switch it flashed over the editor each time a deck took a moment.
// The deck before stays in view, the pointer says the page is busy, and a
// note comes only when it takes long (files from the cloud).
let loadingDepth = 0;
async function loadingScreen(work) {
  loadingDepth++;
  document.body.classList.add("busy");
  const timer = setTimeout(() => toast(t("Loading presentation…")), 1500);
  try {
    return await work();
  } finally {
    clearTimeout(timer);
    if (--loadingDepth === 0) document.body.classList.remove("busy");
  }
}
window.__loadingScreen = loadingScreen;
function playIntro() {
  const el = document.getElementById("brandIntro");
  if (!el) return Promise.resolve();
  return new Promise((done) => {
    let timer = 0;
    const skip = (ev) => {
      ev.preventDefault();
      ev.stopImmediatePropagation();
      end();
    };
    const end = () => {
      clearTimeout(timer);
      window.removeEventListener("pointerdown", skip, true);
      window.removeEventListener("keydown", skip, true);
      hideIntro();
      done();
    };
    // shown already while the page loaded (index.html), or now
    const at = window.__introAt ?? performance.now();
    window.__introAt = undefined;
    el.hidden = false;
    el.classList.add("on");
    window.addEventListener("pointerdown", skip, true);
    window.addEventListener("keydown", skip, true);
    timer = setTimeout(end, Math.max(0, INTRO_MS - (performance.now() - at)));
  });
}
function wakeViewer() {
  document.body.classList.remove("idle");
  clearTimeout(idleTimer);
  // the bar stays while its menu is open
  idleTimer = setTimeout(() => {
    if (vMenu.hidden && vGo.hidden && !app.navTyping()) document.body.classList.add("idle");
    else wakeViewer();
  }, 2500);
}
// Esc leaves full screen (the browser does that), never the presentation:
// there is no editor to go back to.
window.addEventListener("keydown", (ev) => {
  if (viewer && ev.key === "Escape") {
    ev.stopImmediatePropagation();
    if (!vGo.hidden) closeGoTo();
    else if (app.navTyping()) {
      app.navClear();
      needsPaint = true;
    } else if (!vMenu.hidden) {
      toggleViewMenu(false);
      vMore.focus();
    }
  }
}, true);
for (const ev of ["pointermove", "pointerdown", "keydown"]) {
  window.addEventListener(ev, () => { if (viewer || presentingNow()) wakeViewer(); }, { passive: true });
}
document.getElementById("vFirst").addEventListener("click", () => { app.firstSlide(); afterInput(); });
document.getElementById("vPrev").addEventListener("click", () => { app.prev(); afterInput(); });
document.getElementById("vNext").addEventListener("click", () => { app.next(); afterInput(); });
document.getElementById("vData").addEventListener("click", () => refreshLiveData());
document.getElementById("vFull").addEventListener("click", () => {
  if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
  else if (document.documentElement.requestFullscreen) document.documentElement.requestFullscreen().catch(() => {});
});

// The slide counter, "3 / 12" (PresApp.slideLabel; the number being typed
// while digits come in). Pressed, it becomes a field: a number and Enter go
// to that slide, shown whole (PresNav); Esc or leaving it goes nowhere.
const vCount = document.getElementById("vCount");
const vGo = document.getElementById("vGo");
function presentingNow() {
  return !!(lastLayout && lastLayout.mode === "present");
}
function syncCounter() {
  if (!presentingNow()) return;
  const label = app.slideLabel();
  if (vCount.textContent !== label) vCount.textContent = label;
  vCount.classList.toggle("typing", app.navTyping());
}
function openGoTo() {
  vCount.hidden = true;
  vGo.hidden = false;
  vGo.value = "";
  vGo.placeholder = app.slideLabel().split(" /")[0];
  wakeViewer();
  vGo.focus();
}
function closeGoTo() {
  if (vGo.hidden) return;
  vGo.hidden = true;
  vCount.hidden = false;
  if (!viewer && !isCoarse()) keys.focus({ preventScroll: true });
  else vCount.focus({ preventScroll: true });
}
vCount.addEventListener("click", openGoTo);
// presenting from the editor, a button pressed hands the keys back to the
// slides (the arrows, PageDown…); the shared page's buttons keep theirs
document.getElementById("viewBar").addEventListener("click", (ev) => {
  if (viewer || isCoarse() || ev.target === vCount || ev.target.closest?.("#vGo, #vMore, #vMenu")) return;
  if (ev.target.closest?.("button")) keys.focus({ preventScroll: true });
});
// Tips: what a button does, in words, shortly after the pointer rests on
// it (the browser's own title tip comes late and small). The presenting
// bar's buttons say their title; on the canvas the app says (app.tipAt:
// review mode's bar and pins). One tip at a time, above what it is about.
const tipEl = document.getElementById("tip");
let tipTimer = 0, tipText = "", tipFor = null;
function showTip(text, x, top, owner) {
  if (text === tipText && owner === tipFor) return;
  hideTip();
  if (!text) return;
  tipText = text;
  tipFor = owner;
  tipTimer = setTimeout(() => {
    tipEl.textContent = text;
    tipEl.hidden = false;
    const w = tipEl.offsetWidth, h = tipEl.offsetHeight;
    const left = Math.max(8, Math.min(window.innerWidth - w - 8, x - w / 2));
    tipEl.style.left = left + "px";
    tipEl.style.top = Math.max(8, top - h - 8) + "px";
  }, 350);
}
function hideTip() {
  clearTimeout(tipTimer);
  tipEl.hidden = true;
  tipText = "";
  tipFor = null;
}
for (const b of document.querySelectorAll("#viewBar > button")) {
  // the title moves to data-tip while the tip shows, so the browser's own
  // does not show over it
  b.addEventListener("pointerenter", (ev) => {
    if (ev.pointerType !== "mouse") return;
    const t = b.getAttribute("title") || b.dataset.tip || "";
    if (b.hasAttribute("title")) { b.dataset.tip = t; b.removeAttribute("title"); }
    const r = b.getBoundingClientRect();
    showTip(t, r.left + r.width / 2, r.top, b);
  });
  b.addEventListener("pointerleave", () => {
    if (b.dataset.tip && !b.hasAttribute("title")) b.setAttribute("title", b.dataset.tip);
    if (tipFor === b) hideTip();
  });
  b.addEventListener("click", () => { if (tipFor === b) hideTip(); });
}
vGo.addEventListener("keydown", (ev) => {
  if (ev.key === "Enter") {
    ev.preventDefault();
    const went = app.goToSlide(vGo.value);
    closeGoTo();
    if (went) afterInput();
  } else if (ev.key === "Escape") {
    ev.preventDefault();
    closeGoTo();
  }
  ev.stopPropagation();
});
vGo.addEventListener("input", () => { vGo.value = vGo.value.replace(/[^0-9]/g, "").slice(0, 4); });
vGo.addEventListener("blur", () => setTimeout(closeGoTo, 0));
window.__goTo = { open: openGoTo, close: closeGoTo, label: () => vCount.textContent };

// --- drawing on the slide, Record and Play recording ----------------------------------
// Presenting, ✎ in the bar turns the pen on: a press on the slide that moves
// draws (one that does not still goes on), the pointer over the slide is
// drawn as an arrow, Backspace wipes (PresInk). Record → Record presentation
// presents from the start with the pen on and the microphone recording
// (web/recorder.js); everything the presentation does is written down
// (PresRecord) and kept beside the sound in the deck's recordings/. Play
// recording presents again from it, through the voice chosen in Record →
// Voice. The bar while recording: the red time (pressed: stop) and pause;
// while playing: back, pause, on, the time (pressed: stop).
const REC_JSON = "recordings/take.json";
const PEN_TOOLS = ["pen", "arrow", "line", "ellipse", "text"];
const PEN_ICONS = { pen: "〰", arrow: "↗", line: "╱", ellipse: "◯", text: "Aa" };
const PEN_COLORS = ["#ef4444", "#facc15", "#22c55e", "#3b82f6", "#ffffff", "#111111"];
let voiceRec = null;
let player = null;
const vPen = document.getElementById("vPen");
const vTool = document.getElementById("vTool");
const vColor = document.getElementById("vColor");
const vWipe = document.getElementById("vWipe");
const vBack = document.getElementById("vBack");
const vPause = document.getElementById("vPause");
const vFwd = document.getElementById("vFwd");
const vRec = document.getElementById("vRec");
const vPlayRec = document.getElementById("vPlayRec");

// The deck's recording, read when it opens (and when its file arrives from
// the share).
async function loadRecording() {
  const which = doc.id;
  const blob = await readDocFile(REC_JSON);
  if (doc.id !== which) return;
  app.loadRecording(blob ? await blob.text() : "");
  needsPaint = true;
}

// Record asks first: with the voice or without (PresApp "confirm:recstart",
// ":alt" without). With it the microphone is asked for before anything runs;
// then 3, 2, 1 (Esc cancels) and the presentation starts recording.
let recReady = null;
let recCounting = null;
const recCount = document.getElementById("recCount");
const recCountN = document.getElementById("recCountN");
async function prepareRecording(withVoice) {
  const r = new VoiceRecorder();
  if (withVoice) {
    // The microphone first, before full screen: a permission question
    // asked while the page goes full screen can be dismissed by the switch,
    // which the page sees as NotAllowedError.
    try {
      await r.open();
    } catch (e) {
      console.warn("no microphone", e);
      // asked, not recorded silently: the voice was what was chosen
      app.openConfirm("recnomic", t("The microphone is not available"), await micHelp(e), t("Record without voice"));
      needsPaint = true;
      return;
    }
  }
  // full screen while the press that chose still counts as one (at once
  // when the microphone was already allowed)
  if (!viewer && document.documentElement.requestFullscreen && !document.fullscreenElement) {
    document.documentElement.requestFullscreen().catch(() => {});
  }
  if (!(await countDown())) {
    r.close();
    return;
  }
  recReady = r;
  app.record();
  if (!app.isRecording()) {
    // nothing to present (no slides): the microphone is let go
    recReady = null;
    r.close();
  }
  handleRequests();
  needsPaint = true;
}

// Why the microphone was refused, and where to allow it: this site blocked
// in the browser, the browser blocked by the system (macOS: System Settings
// → Privacy & Security → Microphone), no microphone, or one in use.
async function micHelp(e) {
  const name = (e && e.name) || "";
  const msg = (e && e.message) || "";
  let state = "";
  try { state = (await navigator.permissions.query({ name: "microphone" })).state; } catch (_) { /* not asked */ }
  if (!navigator.mediaDevices || !window.isSecureContext) {
    // a server of one's own answers https:// too, with its own certificate
    // (mcp-go/owncert.go): installed once, from its /ca page
    if (window.sliqtly?.collab && location.protocol === "http:") {
      return t("The browser allows the microphone only on https:// addresses. Open %s on this computer once to install the server's certificate, then open the presentation at %h.")
        .replace("%s", location.origin + "/ca").replace("%h", "https://" + location.host + location.pathname + location.search + location.hash);
    }
    return t("The browser allows the microphone only on https:// or localhost addresses. Open Sliqtly through one of them.");
  }
  if (name === "NotFoundError" || name === "OverconstrainedError") {
    return t("No microphone was found. Connect one and try again.");
  }
  if (name === "NotReadableError" || name === "AbortError") {
    return t("The microphone is in use by another program, or the system did not let the browser open it. Close the other program and try again.");
  }
  if (state === "denied") {
    return t("This site is blocked from the microphone. Click the icon left of the address (the lock or the settings icon), set Microphone to Allow, reload the page and record again.");
  }
  if (/system/i.test(msg)) {
    return t("The operating system blocks the browser from the microphone. On a Mac: System Settings → Privacy & Security → Microphone, turn your browser on and restart it.");
  }
  return t("The browser did not give the microphone (") + (msg || name) + t("). If it asked, choose Allow; otherwise click the icon left of the address, set Microphone to Allow and record again.");
}

// 3, 2, 1 over the page; false when Esc (or a press on it) cancelled.
function countDown() {
  return new Promise((resolve) => {
    let n = 3;
    document.getElementById("recCountWords").textContent = t("Recording starts. Esc cancels.");
    recCount.hidden = false;
    const show = () => { recCountN.textContent = String(n); };
    show();
    const done = (ok) => {
      clearInterval(timer);
      recCount.hidden = true;
      recCounting = null;
      resolve(ok);
    };
    const timer = setInterval(() => {
      n -= 1;
      if (n <= 0) done(true);
      else show();
    }, 800);
    recCounting = () => done(false);
  });
}
recCount.addEventListener("pointerdown", (ev) => {
  ev.preventDefault();
  recCounting?.();
});

async function startVoice() {
  const r = recReady || new VoiceRecorder();
  recReady = null;
  voiceRec = r;
  recShownAt = performance.now();
  document.body.classList.add("recording");
  // shown at once, not when the canvas next paints
  syncRecBadge(JSON.parse(app.inkState()));
  await r.begin();
  needsPaint = true;
  // left before the recording began
  if (voiceRec !== r) await r.stop();
}

// Files of an older take that the new one does not use.
async function dropTakeFiles(keep) {
  for (const f of await docFiles()) {
    if (!f.path.startsWith("recordings/take.") || keep.includes(f.path)) continue;
    pending.delete(f.path);
    if (doc.persisted && vfs) await vfs.deleteFile(doc.id, f.path);
  }
}

async function finishRecording() {
  const r = voiceRec;
  voiceRec = null;
  document.body.classList.remove("recording", "recPaused");
  syncRecBadge();
  if (!r) return;
  const { blob, ext, type } = await r.stop();
  let audio = "";
  if (blob && blob.size) {
    audio = "recordings/take." + ext;
    await keepFile({ path: audio, type, size: blob.size, data: blob });
  }
  app.setRecordingAudio(audio);
  const text = app.recordingJson();
  await keepFile({ path: REC_JSON, type: "application/json", size: new Blob([text]).size, data: text });
  await dropTakeFiles([audio, REC_JSON]);
  app.loadRecording(text);
  toast(t("Recording kept (") + clockText(app.recordingDuration()) + t("). Play or edit it in Files or the Record menu."));
  refreshFiles();
  needsPaint = true;
}

async function startReplay() {
  const at = app.replayTime();
  const path = app.recordingAudio();
  const blob = path ? await readDocFile(path) : null;
  if (!app.isReplaying()) return;
  player?.close();
  player = new VoicePlayer(blob, app.recordingVoice(), app.recordingDuration());
  document.body.classList.add("replaying");
  await player.play(at);
}

function stopReplay() {
  player?.close();
  player = null;
  document.body.classList.remove("replaying", "recPaused");
}

async function keepVoice(v) {
  app.setRecordingVoice(v);
  player?.setVoice(v);
  if (!app.hasRecording()) return;
  const text = app.recordingJson();
  await keepFile({ path: REC_JSON, type: "application/json", size: new Blob([text]).size, data: text });
}

async function deleteRecording() {
  await dropTakeFiles([]);
  app.dropRecording();
  cloudSoon();
  refreshFiles();
  toast(t("Recording deleted."));
}

// The take kept again after a cut (Edit recording).
async function saveTake() {
  if (!app.hasRecording()) return;
  const text = app.recordingJson();
  await keepFile({ path: REC_JSON, type: "application/json", size: new Blob([text]).size, data: text });
  cloudSoon();
}

// The voice's loudness for Edit recording's timeline: the sound decoded
// once, the loudest sample in each tenth of a second.
async function recordingPeaks() {
  const path = app.recordingAudio();
  const blob = path ? await readDocFile(path) : null;
  if (!blob) { app.setRecordingPeaks(""); return; }
  const Ctx = window.OfflineAudioContext || window.webkitOfflineAudioContext;
  const ctx = new Ctx(1, 1, 8000);
  const buf = await ctx.decodeAudioData(await blob.arrayBuffer());
  const data = buf.getChannelData(0);
  const step = Math.max(1, Math.round(buf.sampleRate * 0.1));
  const peaks = [];
  let top = 0;
  for (let i = 0; i < data.length; i += step) {
    let m = 0;
    const end = Math.min(data.length, i + step);
    for (let j = i; j < end; j++) m = Math.max(m, Math.abs(data[j]));
    peaks.push(m);
    top = Math.max(top, m);
  }
  // quiet speech still shows: scaled to the loudest part
  const k = top > 0 ? 1 / top : 0;
  app.setRecordingPeaks(peaks.map((v) => (v * k).toFixed(2)).join(","));
  needsPaint = true;
}

function recRequest(r) {
  if (r === "confirm:recstart") prepareRecording(true).catch(fail);
  else if (r === "confirm:recstart:alt" || r === "confirm:recnomic") prepareRecording(false).catch(fail);
  else if (r === "record:start") startVoice().catch(fail);
  else if (r === "rec:save") saveTake().catch(fail);
  else if (r === "rec:peaks") recordingPeaks().catch((e) => { console.warn("peaks", e); app.setRecordingPeaks(""); needsPaint = true; });
  else if (r === "record:stop") finishRecording().catch(fail);
  else if (r === "replay:start") startReplay().catch(fail);
  else if (r === "replay:stop") stopReplay();
  else if (r.startsWith("rec:voice-")) keepVoice(r.slice(10)).catch(fail);
  else if (r === "rec:delete") {
    app.openConfirm("recdelete", t("Delete recording"), t("Delete this presentation's recording, its sound and what was drawn? This cannot be undone."), t("Delete"));
  } else if (r === "confirm:recdelete") deleteRecording().catch(fail);
  else return false;
  return true;
}

// The time the recording is at, each frame; a replay at its end stops.
function recFrame() {
  if (voiceRec && app.isRecording()) app.recordTime(voiceRec.time());
  if (app.isReplaying() && player) {
    // a part cut away (Edit recording) is jumped over
    const at = player.time();
    const past = app.recordingSkip(at);
    if (past > at + 0.01) player.seek(past);
    app.replayAt(player.time());
    if (player.ended() && !player.paused) {
      app.replayStop();
      handleRequests();
    }
  }
  if (app.inkBusy()) needsPaint = true;
}

function togglePause() {
  const p = voiceRec || player;
  if (!p) return;
  if (p.paused) p.resume();
  else p.pause();
  document.body.classList.toggle("recPaused", p.paused);
  needsPaint = true;
}

// s seconds on or back in the recording as played (cuts left out)
function seekBy(s) {
  if (!player) return;
  const p = app.recordingPlayTime(player.time()) + s;
  player.seek(app.recordingSourceTime(Math.max(0, Math.min(app.recordingPlayLength(), p))));
  app.replayAt(player.time());
  needsPaint = true;
}

let penShown = "";
function syncRecBar() {
  if (!presentingNow()) return;
  const st = JSON.parse(app.inkState());
  const sig = [st.on, st.tool, st.color, st.recording, st.replaying, st.strokes > 0, Math.floor(st.t), !!(voiceRec || player)?.paused, app.hasRecording(), st.typing, !!voiceRec].join();
  if (sig === penShown) return;
  penShown = sig;
  const drive = !st.replaying;
  vPen.hidden = !drive || st.recording;
  vPen.setAttribute("aria-pressed", st.on ? "true" : "false");
  vTool.hidden = !drive || !st.on;
  vTool.textContent = PEN_ICONS[st.tool] || "〰";
  vColor.hidden = !drive || !st.on;
  vColor.style.setProperty("--pen", st.color);
  vWipe.hidden = !drive || st.strokes === 0;
  vBack.hidden = vFwd.hidden = !st.replaying;
  vPause.hidden = !st.recording && !st.replaying;
  const paused = !!(voiceRec || player)?.paused;
  vPause.textContent = paused ? "▶︎" : "⏸︎";
  vRec.hidden = !st.recording && !st.replaying;
  vRec.textContent = st.recording ? "● " + clockText(st.t) + " ■" : clockText(app.recordingPlayTime(st.t)) + " / " + clockText(app.recordingPlayLength()) + " ■";
  vPlayRec.hidden = !app.hasRecording() || st.recording;
  syncRecBadge(st);
}

// The badge at the top while recording: REC and the time, no sound when
// recorded without, Pause and Stop, and what can be done (for the first
// seconds, while paused and while writing).
const recBadge = document.getElementById("recBadge");
const recHint = document.getElementById("recHint");
let recShownAt = 0;
function syncRecBadge(st) {
  const on = !!(st && st.recording && voiceRec);
  recBadge.hidden = !on;
  if (!on) return;
  // paused by the user, not the clock before it has started
  const paused = !!voiceRec.paused && voiceRec.clock.at >= 0;
  document.getElementById("recTime").textContent = clockText(st.t);
  document.getElementById("recMute").hidden = voiceRec.hasSound;
  document.getElementById("recPauseBtn").textContent = paused ? t("▶ Go on") : t("⏸ Pause");
  const fresh = performance.now() - recShownAt < 12000;
  const hint = st.typing
    ? t("Writing: Enter ends it, Shift+Enter a new line, Backspace deletes")
    : paused ? t("Paused: nothing is recorded until you go on")
    : st.tool === "text" ? t("Aa: click where to write, then type. ← → PageUp PageDown change slides.")
    : fresh ? t("Draw with the mouse; the pen button's Aa writes text. ← → change slides. Esc stops.")
    : "";
  recHint.textContent = hint;
  recHint.hidden = !hint;
}
document.getElementById("recPauseBtn").addEventListener("click", () => { togglePause(); keys.focus({ preventScroll: true }); });
document.getElementById("recStopBtn").addEventListener("click", () => {
  app.endPresent();
  handleRequests();
  needsPaint = true;
});

vPen.addEventListener("click", () => { app.setInk(vPen.getAttribute("aria-pressed") !== "true"); needsPaint = true; });
vTool.addEventListener("click", () => {
  const st = JSON.parse(app.inkState());
  app.setInkTool(PEN_TOOLS[(PEN_TOOLS.indexOf(st.tool) + 1) % PEN_TOOLS.length]);
  needsPaint = true;
});
vColor.addEventListener("click", () => {
  const st = JSON.parse(app.inkState());
  app.setInkColor(PEN_COLORS[(PEN_COLORS.indexOf(st.color) + 1) % PEN_COLORS.length]);
  needsPaint = true;
});
vWipe.addEventListener("click", () => { app.key("backspace", false, false); needsPaint = true; });
vPause.addEventListener("click", togglePause);
vBack.addEventListener("click", () => seekBy(-10));
vFwd.addEventListener("click", () => seekBy(10));
vRec.addEventListener("click", () => {
  if (app.isReplaying()) app.replayStop();
  else app.endPresent();
  handleRequests();
  needsPaint = true;
});

// A replay's keys: Space pauses, the arrows seek, Esc stops; the rest is
// not for it. True when the key was a replay's.
function replayKey(ev) {
  if (!app.isReplaying()) return false;
  ev.preventDefault();
  if (ev.key === " ") togglePause();
  else if (ev.key === "ArrowLeft") seekBy(-5);
  else if (ev.key === "ArrowRight") seekBy(5);
  else if (ev.key === "Escape") {
    app.replayStop();
    handleRequests();
  }
  return true;
}

// The … menu: the deck as PDF, PPTX or Markdown (the editor's exports), a new
// deck of the reader's own based on this one, and, for the signed-in owner of
// a cloud share, Edit, which opens their own deck in the editor.
// Embedded in an assistant's preview (mcp-go/assets/preview.html writes the page as
// srcdoc, with <meta name="sliqtly-link">) the page has no address and its
// sandbox allows no downloads or windows: every item opens sliqtly.com in a
// new tab through the preview (window.__sliqtlyOpenLink, the host's
// ui/open-link), exports with ?export=pdf|pptx|docx|html|md, which the site runs on load.
const vMenu = document.getElementById("vMenu");
const vMore = document.getElementById("vMore");
const vExportSub = document.getElementById("vExportSub");
const vExport = document.getElementById("vExport");
const vCopySub = document.getElementById("vCopySub");
const vCopy = document.getElementById("vCopy");
const framed = location.protocol === "blob:" || !!document.querySelector('meta[name="sliqtly-link"]');
function siteUrl(path) {
  return new URL(path, framed ? SITE + "/" : document.baseURI).href;
}
let viewShare = null; // { id, owner, deck } of a cloud share being shown
function ownsShare() {
  const u = window.sliqtly?.user?.();
  return !!(u && viewShare && viewShare.deck && viewShare.owner === u.uid);
}
function toggleViewMenu(open) {
  vMenu.hidden = !open;
  vMore.setAttribute("aria-expanded", String(open));
  if (!open) {
    openViewSub(null);
    return;
  }
  document.getElementById("vEdit").hidden = !ownsShare();
  document.getElementById("vSpeaker").setAttribute("aria-checked", String(app.speakerOn()));
  document.getElementById("vAuto").setAttribute("aria-checked", String(app.autoOn()));
  wakeViewer();
  [...vMenu.querySelectorAll("button")].find((b) => b.offsetParent)?.focus();
}
window.addEventListener("sliqtly:user", () => { document.getElementById("vEdit").hidden = !ownsShare(); });
function siteLink(url) {
  if (framed) {
    let open = null;
    try { open = window.parent !== window && window.parent.__sliqtlyOpenLink; } catch (_) { /* another origin */ }
    if (typeof open === "function") open(url);
    else window.open(url, "_blank", "noopener");
  } else {
    location.assign(url);
    // only the hash changed: the page would stay the viewer
    if (new URL(url).pathname === location.pathname && !new URL(url).search) location.reload();
  }
}
function createFromViewed() {
  if (viewShare) return siteLink(siteUrl("s/" + viewShare.id + "?edit"));
  const q = hashParams();
  q.delete("mode");
  q.delete("export");
  siteLink(siteUrl("").replace(/#.*$/, "") + "#" + q.toString());
}
// the shown deck on the site, exporting itself there
function exportOnSite(kind) {
  if (viewShare) return siteLink(siteUrl("s/" + viewShare.id + "?" + (viewShare.slides ? "slides=" + encodeURIComponent(viewShare.slides) + "&" : "") + "export=" + kind));
  const q = hashParams();
  q.set("mode", "show");
  q.set("export", kind);
  siteLink(siteUrl("").replace(/#.*$/, "") + "#" + q.toString());
}
const EXPORTS = { pdf: () => exportPdf(), pptx: () => exportPptx(), docx: () => exportDocx(), html: () => exportHtml(), player: () => exportPlayer(), md: () => exportMd() };
async function exportMd() {
  window.__lastDownload = deliver(new TextEncoder().encode(app.source()), exportName() + ".md", "text/markdown");
}
window.__viewMenu = { toggle: toggleViewMenu, ownsShare, share: () => viewShare };
vMore.addEventListener("click", () => toggleViewMenu(vMenu.hidden));
// Export ▸ and Copy ▸: one open at a time (null: neither)
function openViewSub(which) {
  for (const [btn, sub] of [[vExport, vExportSub], [vCopy, vCopySub]]) {
    sub.hidden = btn !== which;
    btn.setAttribute("aria-expanded", String(btn === which));
  }
  if (which) (which === vExport ? vExportSub : vCopySub).querySelector("button").focus();
}
vExport.addEventListener("click", () => openViewSub(vExportSub.hidden ? vExport : null));
vCopy.addEventListener("click", () => openViewSub(vCopySub.hidden ? vCopy : null));
// Copy ▸ Markdown: the deck's text; Copy ▸ Comments: the open comments
// numbered, each slide's Markdown under them (PresReview.openMarkdown)
const VIEW_COPIES = { md: () => app.copyText("md"), mdc: () => app.copyText("mdc"), comments: () => app.copyText("comments") };
vMenu.addEventListener("click", (ev) => {
  const what = ev.target.closest("[data-copy]")?.dataset.copy;
  if (what && VIEW_COPIES[what]) {
    toggleViewMenu(false);
    writeClip(VIEW_COPIES[what]()).then((ok) => toast(ok ? t("Copied") : t("Could not copy"))).catch(fail);
    return;
  }
  const act = ev.target.closest("[data-act]")?.dataset.act;
  if (!act) return;
  toggleViewMenu(false);
  if (EXPORTS[act]) {
    if (framed) exportOnSite(act);
    else EXPORTS[act]().catch(fail);
  } else if (act === "speaker") {
    app.setSpeaker(!app.speakerOn());
    needsPaint = true;
  } else if (act === "auto") {
    app.setAuto(!app.autoOn());
    needsPaint = true;
  } else if (act === "new") createFromViewed();
  else if (act === "playrec") {
    app.replayFromSlide();
    handleRequests();
  }
  else if (act === "edit" && ownsShare()) {
    siteLink(siteUrl("s/" + viewShare.id + "?edit"));
  }
});
vMenu.addEventListener("keydown", (ev) => {
  const items = [...vMenu.querySelectorAll("button")].filter((b) => b.offsetParent);
  const at = items.indexOf(document.activeElement);
  if (ev.key === "Escape") {
    toggleViewMenu(false);
    vMore.focus();
  } else if (ev.key === "ArrowDown" || ev.key === "ArrowUp") {
    items[(at + (ev.key === "ArrowDown" ? 1 : items.length - 1)) % items.length]?.focus();
  } else return;
  ev.preventDefault();
  ev.stopPropagation();
});
document.addEventListener("pointerdown", (ev) => {
  if (!vMenu.hidden && !ev.target.closest("#viewBar")) toggleViewMenu(false);
});

let lastHash = "";
async function openFromHash() {
  const q = hashParams();
  if (!q.has("md")) return false;
  lastHash = location.hash;
  try {
    const text = await unpackText(q.get("md"));
    await leaveDoc();
    beginDoc(text);
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
    docName = "shared";
    shownDoc(text);
    dropThumbs();
    needsPaint = true;
    if (q.get("mode") === "show") enterViewer({ from: "link" });
    return true;
  } catch (e) {
    // stopped half way: the editor shows the deck it had begun to open
    if (doc.loading) shownDoc(doc.openedText);
    toast(t("Could not read the link's contents."));
    console.warn(e);
    return false;
  }
}
window.addEventListener("hashchange", () => {
  if (location.hash === lastHash) return;
  if (hashParams().has("md")) openFromHash();
  else useAddress(hashParams());
});

// The address follows what is on screen, so a reload comes back to it:
// #slide={n}&tab=css|files&view=present|play, each left out at its default.
// Replaced, not pushed: Back does not walk through slides. Kept beside a
// link's own keys (#md=…, #share=…), never in the assistant's preview, which
// has no address of its own.
// The deck this tab has is not in the address (the site's own address stays
// as it was typed): the tab keeps it in sessionStorage, which a reload keeps
// and other tabs do not share. A #doc={id} in an older link still opens that
// deck, and is then left out of the address.
const ADDRESS_KEYS = ["doc", "slide", "tab", "view"];
const TAB_DOC = "sliqtly.tabDoc";
let tabDocKept = null;
function keepTabDoc() {
  const id = doc.persisted && !doc.cloud && !viewer ? doc.id : "";
  if (id === tabDocKept) return;
  tabDocKept = id;
  try {
    if (id) mineTab.setItem(TAB_DOC, id);
    else mineTab.removeItem(TAB_DOC);
  } catch (_) { /* a reload opens the deck worked on last */ }
}
function followAddress() {
  if (framed || !window.__pageStarted || !lastLayout) return;
  keepTabDoc();
  const q = hashParams();
  const was = q.toString();
  for (const k of ADDRESS_KEYS) q.delete(k);
  if (lastLayout.slide > 0 && lastLayout.slide < lastLayout.slides) q.set("slide", String(lastLayout.slide + 1));
  if (!viewer) {
    const tab = app.editorTab();
    if (tab && tab !== "md") q.set("tab", tab);
    if (lastLayout.mode === "present") q.set("view", "present");
    else if (lastLayout.playing) q.set("view", "play");
  }
  const now = q.toString();
  if (now === was) return;
  history.replaceState(history.state, "", location.pathname + location.search + (now ? "#" + now : ""));
  lastHash = location.hash;
}
// …and back: the slide, the tab and the presentation the address names
function useAddress(q) {
  const n = parseInt(q.get("slide") || "", 10);
  const presenting = lastLayout && lastLayout.mode === "present";
  if (n >= 1) app.selectSlide(n - 1);
  if (viewer) {
    // the viewer presents from the start: again, from this slide
    if (n > 1) {
      app.present(false);
      handleRequests();
    }
  } else {
    if (q.has("tab")) app.showTab(q.get("tab"));
    const view = q.get("view");
    if (view === "present" && !presenting) {
      app.present(false);
      handleRequests();
    } else if (view === "play" && !app.isPlaying()) {
      app.play();
      rebaseClock();
    }
  }
  needsPaint = true;
}

// /s/{id}: a deck shared through PRO, read from the cloud. Shown as a
// presentation; with ?edit, opened as a new deck of the reader's own.
// #share={id}: the same presentation where the page is not at its own
// address — the preview an AI assistant shows (mcp-go/assets/preview.html) runs
// this page as an iframe's srcdoc, since the assistant does not let it
// frame sliqtly.com. Always only shown.
function hashShare() {
  const id = hashParams().get("share");
  return id && /^[A-Za-z0-9]{6,32}$/.test(id) ? id : null;
}
// ?deck={deckId}&from={shareId}: the owner's Edit from a shared presentation.
// The deck itself when this browser keeps it; else the share's copy, kept
// under the deck's id so sharing it again updates the same cloud deck.
function ownDeck() {
  const q = new URLSearchParams(location.search);
  const deck = q.get("deck");
  const from = q.get("from");
  if (!deck || !/^[A-Za-z0-9_-]{1,64}$/.test(deck)) return null;
  return { deck, from: from && /^[A-Za-z0-9]{6,32}$/.test(from) ? from : null };
}
// This browser's copy of the owner's deck wins unless the share has changed
// since (an assistant saves to the share).
async function ownIsNewer(own) {
  const local = await vfs.getDoc(own.deck);
  if (!local) return false;
  if (!own.from) return true;
  try {
    const shared = await (await pro()).loadShare(own.from);
    if (!shared) return true;
    const at = shared.updated || shared.created;
    const ms = at?.toMillis ? at.toMillis() : Number(at) || 0;
    return local.md === shared.md || (local.updated || 0) >= ms;
  } catch (_) {
    return true;
  }
}
async function openFromShare() {
  const m = DECK_PATH.exec(location.pathname);
  const own = m || hashShare() ? null : ownDeck();
  const id = m ? m[1] : hashShare() || own?.from;
  if (!id) return false;
  try {
    const shared = await (await pro()).loadShare(id);
    if (!shared) {
      toast(t("This shared presentation was not found."));
      return false;
    }
    // someone else's in the cloud: shown, never copied into an editable deck.
    // Only its owner and the people they invite edit it (openOwnCloud opened
    // it for them before this)
    const notMine = !!m && editAsked() && !own && !ownServer();
    const editing = ((!!m && editAsked()) || !!own) && !notMine;
    // ?slides=…: a view of only some of its slides (PresPick), shown, never
    // edited
    const slides = m && !editing ? new URLSearchParams(location.search).get("slides") : null;
    if (editing) originShare = { id, owner: shared.owner || "", md: shared.md || "" };
    beginDoc(shared.md || "");
    liveFromShare = !editing;
    proNow();
    if (own) doc.id = own.deck;
    else if (!editing) viewShare = { id, owner: shared.owner, deck: shared.deck, shown: shared, slides };
    if (shared.theme != null) {
      themeSel.value = shared.theme;
      app.setStyleSheet(shared.theme ? themeCss[shared.theme] || "" : "");
    }
    if (shared.css != null) {
      editedCss[themeSel.value || ""] = shared.css;
      doc.openedCss = shared.css;
      app.setStyleSheet(shared.css);
    }
    // the files come from Storage by fetch(), which the bucket must allow
    // for this origin (storage.cors.json); a picture that does not come is
    // said, not left out in silence
    const missing = [];
    for (const { f, data, error } of await shareFiles(shared.files)) {
      try {
        if (error) throw error;
        const rec = { doc: doc.id, path: f.path, type: f.type, size: f.size, data, updated: Date.now() };
        // the reader's copy keeps them: they are saved with it on its first change
        if (editing) pending.set(rec.path, rec);
        // a reader's are the deck's files all the same: a live sheet opens its
        // workbook, a chart reads a sheet of it
        else readFiles.set(rec.path, rec);
        await useFile(rec);
      } catch (e) {
        console.warn("shared file not loaded: " + f.path, e);
        missing.push(f.path);
      }
    }
    if (missing.length) toast(t("Some pictures or data files of this presentation could not be loaded: ") + missing.join(", "));
    docName = shared.name || "shared";
    shownDoc(shared.md || "");
    // cut once the deck is laid out with its theme, which says at which
    // heading level its slides break
    if (slides) showSlidesOf(shared.md || "", slides);
    dropThumbs();
    needsPaint = true;
    if (!editing) {
      enterViewer({ from: "share" });
      // changed while it opened: followed now
      if (shareMoved) followShare(id);
    } else if (!own) notOwnerNotice(id, shared.owner || "");
    if (notMine) toast(t("This presentation belongs to another account. Only its owner and the people they invite can edit it. You can view it here."));
    return true;
  } catch (e) {
    if (doc.loading) shownDoc(doc.openedText);
    console.warn(e);
    toast(e?.code === "private"
      ? (EDITOR_ROOT
        ? t("This presentation is private. Only its owner and the people they invite can open it: sign in with that Google account, or ask the owner to invite you.")
        : t("This presentation is private: sign in with the Google account that owns it."))
      : t("Could not open the shared presentation."));
    return false;
  }
}

// /s/{id}?edit of a deck that is not the signed-in user's own opens a copy:
// nothing done here (text, comments) reaches the share, nor an assistant
// reading it. Said plainly. Signed in, the user may still be its owner under
// another Google account (an assistant's connector signed in with that one),
// so switching is offered; once the owner is signed in, the deck itself opens.
const SWITCH_FLAG = "sliqtly:switchFor";
async function notOwnerNotice(id, owner) {
  if (ownServer()) return;
  const p = await pro();
  if (typeof p.switchAccount !== "function") return;
  const who = await Promise.race([p.signedIn(), new Promise((ok) => setTimeout(() => ok(null), 8000))]);
  // its owner after all (the cloud copy did not open): not a copy to warn of
  if (who && who.uid === owner) return;
  // the owner signs in from here on (this question, or Sign in): theirs opens
  window.addEventListener("sliqtly:user", () => {
    const u = p.user?.();
    if (u && owner && u.uid === owner && u.uid !== who?.uid) location.href = editAddress(id);
  });
  let asked = false;
  try { asked = sessionStorage.getItem(SWITCH_FLAG) === id; sessionStorage.removeItem(SWITCH_FLAG); } catch (_) { /* ask */ }
  if (!who || owner === "mcp" || !owner || asked) {
    toast(t("You are editing a copy: changes and comments stay in your copy, and the shared presentation does not change."));
    return;
  }
  // after the deck is drawn, so it shows behind the question
  setTimeout(() => {
    const q = t("This presentation belongs to another Sliqtly account than {account}. Your changes and comments go to your own copy, not to it, and an assistant reading it does not see them.\n\nIf it is yours under another Google account (for example the one the Claude connector signed in with), press OK and choose that account. Cancel keeps editing a copy.")
      .replace("{account}", who.email || who.displayName || "");
    if (!confirm(q)) {
      toast(t("You are editing a copy: changes and comments stay in your copy, and the shared presentation does not change."));
      return;
    }
    try { sessionStorage.setItem(SWITCH_FLAG, id); } catch (_) { /* asked again after a redirect */ }
    p.switchAccount();
  }, 400);
}

// A view of some slides: the share's Markdown with only the sections the
// link names. None of them there any more (renamed, deleted): said, and
// nothing of the rest shown.
function slidesOf(md, keys) {
  const v = app.viewOf(md, keys);
  return v || "# " + t("These slides are no longer in the presentation.") + "\n";
}
function showSlidesOf(md, keys) {
  document.body.classList.add("slidesView");
  app.setSource(slidesOf(md, keys));
}

// The player of a share on a server of one's own follows its deck as it
// changes there (mcp-go/assets/sliqtly-local.js tells it): someone editing,
// an assistant's update. The Markdown's changes come as edits, so the slide
// shown stays shown, at the same time into it (PresFollow), as in an editor
// in the room; the theme and the files changed are taken as they are.
let shareMoved = false;
let shareFollow = null;
function followShare(id) {
  if (!viewer || !viewShare || viewShare.id !== id) {
    shareMoved = true;
    return Promise.resolve();
  }
  shareMoved = false;
  // one at a time; a change meanwhile is read once more after it
  if (shareFollow) {
    shareFollow.again = true;
    return shareFollow.done;
  }
  const run = { again: false, done: null };
  shareFollow = run;
  run.done = (async () => {
    try {
      do {
        run.again = false;
        await followShareNow(id);
      } while (run.again);
    } catch (e) {
      console.warn("could not follow the shared presentation", e);
    } finally {
      shareFollow = null;
    }
  })();
  return run.done;
}
async function followShareNow(id) {
  const shared = await (await pro()).loadShare(id);
  if (!shared || !viewShare || viewShare.id !== id) return;
  const was = viewShare.shown;
  viewShare.shown = shared;
  if (shared.theme !== was.theme || shared.css !== was.css) {
    themeSel.value = shared.theme || "";
    app.setStyleSheet(shared.css != null ? shared.css : shared.theme ? themeCss[shared.theme] || "" : "");
  }
  const had = new Map((was.files || []).map((f) => [f.path, JSON.stringify(f)]));
  const moved = (shared.files || []).filter((f) => had.get(f.path) !== JSON.stringify(f));
  for (const { f, data, error } of await shareFiles(moved, { cache: "no-cache" })) {
    if (error) {
      console.warn("shared file not loaded: " + f.path, error);
      continue;
    }
    const rec = { doc: doc.id, path: f.path, type: f.type, size: f.size, data, updated: Date.now() };
    readFiles.set(rec.path, rec);
    await useFile(rec);
  }
  for (const f of was.files || []) if (!(shared.files || []).some((g) => g.path === f.path)) readFiles.delete(f.path);
  const md = viewShare.slides ? slidesOf(shared.md || "", viewShare.slides) : shared.md || "";
  for (const e of editsOf(RdOtDelta.diff(app.source(), md, -1))) app.applyRemoteMd(e.offset, e.removed, e.text);
  app.syncRemote();
  rebaseClock();
  if (moved.length) dropThumbs();
  needsPaint = true;
}
window.__followShare = (id) => followShare(id);

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
    await leaveDoc();
    const text = await textOf(s[1]);
    beginDoc(text);
    if (s[2]) {
      themeSel.value = s[2];
      useTheme(s[2]);
    }
    doc.src = "sample:" + key;
    await useSampleFiles(key, s[3] || []);
    shownDoc(text);
    dropThumbs();
    needsPaint = true;
  } catch (e) {
    // stopped half way: the editor shows the deck it had begun to open
    if (doc.loading) shownDoc(doc.openedText);
    fail(e);
  }
}

// A sample's own files (samples/<key>/<path>), the deck's files as a shared
// one's are: shown at once, kept with the deck on its first change.
const SAMPLE_TYPES = { tsx: "text/plain", css: "text/css", png: "image/png", jpg: "image/jpeg", jpeg: "image/jpeg", svg: "image/svg+xml", xml: SMARTART_TYPE };
async function useSampleFiles(key, paths) {
  const got = await Promise.all(paths.map(async (path) => {
    const res = await fetch(fresh(`./samples/${key}/${path}`));
    if (!res.ok) throw new Error(path + " → " + res.status);
    // text (a program and its stylesheet) as text, as a file added by hand is
    return { path, data: isText(path, "") ? await res.text() : await res.blob() };
  }));
  for (const { path, data } of got) {
    const type = SAMPLE_TYPES[path.split(".").pop().toLowerCase()] || data.type || "text/plain";
    const rec = typeof data === "string"
      ? { doc: doc.id, path, type, size: data.length, data, updated: Date.now() }
      : { doc: doc.id, path, type, size: data.size, data: new Blob([data], { type }), updated: Date.now() };
    pending.set(rec.path, rec);
    await useFile(rec);
  }
}

// A first visit (no deck kept in this browser, none asked for) opens the
// welcome deck; this card next to it leads to a deck of one's own.
function welcomeCard() {
  if (viewer) return;
  const box = document.createElement("div");
  box.id = "welcomeCard";
  box.className = "gCard";
  box.setAttribute("role", "region");
  box.setAttribute("aria-label", t("Welcome"));
  const text = document.createElement("p");
  text.textContent = t("New here? This deck shows what Sliqtly can do. Read on, press Present, or start a deck of your own.");
  const go = document.createElement("button");
  go.className = "primary";
  go.textContent = t("Start your own deck");
  const no = document.createElement("button");
  no.textContent = t("Close");
  go.addEventListener("click", () => {
    box.remove();
    fileRequest("new").catch(fail);
  });
  no.addEventListener("click", () => box.remove());
  box.append(text, go, no);
  document.body.appendChild(box);
}

// --- the keyboard -------------------------------------------------------------------
const KEY_MAP = {
  Backspace: "backspace", Enter: "enter", Tab: "tab", Delete: "delete",
  ArrowLeft: "left", ArrowRight: "right", ArrowUp: "up", ArrowDown: "down",
  Home: "home", End: "end", PageUp: "pageUp", PageDown: "pageDown", Escape: "escape",
};
const CLIPBOARD_CHORD = /^[cxvCXV]$/;
// The code editor's text size: Ctrl/Cmd + plus / minus / 0 while the editor
// has the keyboard (the browser's own zoom everywhere else), and Ctrl + wheel
// or a trackpad pinch over it. Kept per browser.
const EDITOR_ZOOM = "sliqtly.editorFontSize";
function editorZoomStep(ev) {
  if (ev.altKey) return null;
  if (ev.key === "+" || ev.key === "=" || ev.code === "NumpadAdd") return 1;
  if (ev.key === "-" || ev.key === "_" || ev.code === "NumpadSubtract") return -1;
  if (ev.key === "0" || ev.code === "Numpad0") return 0;
  return null;
}
function zoomEditor(steps) {
  if (!app.zoomEditor(steps)) return;
  try { localStorage.setItem(EDITOR_ZOOM, String(app.editorFontSize())); } catch (_) { /* this session only */ }
  closeHint();
  needsPaint = true;
}
function restoreEditorZoom() {
  let size = NaN;
  try { size = parseFloat(localStorage.getItem(EDITOR_ZOOM) || ""); } catch (_) { /* the default */ }
  if (size > 0) app.setEditorFontSize(size);
}
let editorWheel = 0;
// The emoji picker's key: ⌃⌘Space, the Mac's own, and Ctrl+Shift+Space
// everywhere. The picker (EVGUI's) offers the emojis the slides and their PDF
// can draw. Ctrl+Space alone is the value popover.
const IS_MAC = /Mac|iPhone|iPad/.test(navigator.userAgentData?.platform || navigator.platform || "");
function isEmojiChord(ev) {
  if (!(ev.key === " " || ev.code === "Space") || !ev.ctrlKey || ev.altKey) return false;
  return IS_MAC ? ev.metaKey || ev.shiftKey : ev.shiftKey && !ev.metaKey;
}
// Recent emojis, kept by this browser.
const EMOJI_RECENT = "sliqtly.emoji.recent";
let emojiRecent = "";
function keepEmojiRecent() {
  const now = app.emojiRecent();
  if (now === emojiRecent) return;
  emojiRecent = now;
  try { localStorage.setItem(EMOJI_RECENT, now); } catch (_) { /* not kept */ }
}
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
  app.setA11yFocus("");
  app.setFocus(where);
  keys.focus({ preventScroll: true });
  mirrorLine();
  restartBlink();
}

function afterInput() {
  // each keystroke its own edit for the others (web/collab.js)
  collab?.takeLocal();
  keepEmojiRecent();
  mirrorLine();
  handleRequests();
  rebaseClock();
  needsPaint = true;
}

keys.addEventListener("keydown", (ev) => {
  if (canvasPicking && ev.key === "Escape") {
    ev.preventDefault();
    endCanvasPick("");
    return;
  }
  if (ev.key === "F6") {
    ev.preventDefault();
    cycleRegion(ev.shiftKey);
    return;
  }
  if (isEmojiChord(ev)) {
    ev.preventDefault();
    if (app.emojiIsOpen()) closeHint();
    else if (app.openEmojiAtCaret()) {
      // nothing the pointer started replaces it
      clearTimeout(hintTimer);
      clearTimeout(hintCloseTimer);
      hint = null;
    }
    afterInput();
    paintOnce();
    return;
  }
  if (ev.ctrlKey && (ev.key === " " || ev.code === "Space")) {
    // the value popover at the caret, with the keyboard in it
    ev.preventDefault();
    if (app.chartIsOpen()) {
      // the chart editor is already open: the keyboard goes into it
      paintOnce();
      focusRegion("chart");
      return;
    }
    let h = null;
    try { h = JSON.parse(app.hintAtCaret() || "null"); } catch (_) { h = null; }
    if (h && isChartFence(h)) {
      app.openChartEditor(h.line);
      afterInput();
      paintOnce();
      focusRegion("chart");
    } else if (app.openImageAtCaret()) {
      // a picture's line: its settings
      afterInput();
      paintOnce();
      focusRegion("chart");
    } else if (h) {
      showHint(h);
      paintOnce();
      focusRegion("hint");
    }
    return;
  }
  if (hint && !app.hintHasKeys() && ev.key !== "Escape" && ev.key !== "Shift" && ev.key !== "Control" && ev.key !== "Alt" && ev.key !== "Meta") closeHint();
  // A composition that ended without a compositionend (a dead key, an IME
  // cancelled by a click) must not leave typing switched off.
  if (!ev.isComposing && ev.keyCode !== 229) composing = false;
  if (recCounting) {
    ev.preventDefault();
    if (ev.key === "Escape") recCounting();
    return;
  }
  const presenting = lastLayout && lastLayout.mode === "present";
  if (presenting && replayKey(ev)) return;
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
  if (!presenting && mod && app.focusTarget() === "editor") {
    const steps = editorZoomStep(ev);
    if (steps !== null) {
      ev.preventDefault();
      zoomEditor(steps);
      return;
    }
  }
  const special = KEY_MAP[ev.key];
  if (special) {
    if (special === "tab" && app.focusTarget() !== "editor" && app.focusTarget() !== "chart" && app.focusTarget() !== "room") return;
    if (app.key(special, ev.shiftKey, mod)) ev.preventDefault();
    else if (app.focusTarget() === "editor" || app.focusTarget() === "chart") ev.preventDefault();
    afterInput();
    return;
  }
  if (presenting) {
    // with the text tool (Aa) letters write on the slide
    const writing = app.inkWrites();
    if ((ev.key === "r" || ev.key === "R") && !mod && !ev.altKey && !app.reviewHasKeys() && !writing) {
      ev.preventDefault();
      refreshLiveData();
      return;
    }
    if (ev.key.length === 1 || (writing && !mod && [...ev.key].length === 1)) {
      ev.preventDefault();
      app.text(ev.key);
      afterInput();
    }
    return;
  }
  // the menus' keys (shown beside their rows): Ctrl+M a new slide (Ctrl on
  // a Mac too: ⌘M minimises the window), ⌘/Ctrl+D duplicate it, ⌘/Ctrl+O
  // open, ⌘/Ctrl+S save .md
  const menuKey = !ev.altKey && !ev.shiftKey && ev.key.length === 1 ? ev.key.toLowerCase() : "";
  const menuReq = ev.ctrlKey && !ev.metaKey && menuKey === "m" ? "slide:new"
    : mod && menuKey === "d" ? "slide:duplicate"
    : mod && menuKey === "o" ? "openbox"
    : mod && menuKey === "s" ? "click:save"
    : "";
  if (menuReq) {
    ev.preventDefault();
    app.request(menuReq);
    afterInput();
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
  // the viewer's buttons and menu keep their own keys (Enter, Tab, arrows)
  if (ev.target.closest?.("#viewBar, #endPanel")) return;
  // a live sheet being edited (or the workbook dialog) has the keyboard
  if (liveSheets.owns(ev.target)) return;
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
  const kind = app.clipCopyKind();
  if (kind) {
    ev.preventDefault();
    clipCopy(kind).catch(fail);
    return;
  }
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
  // while a room's chat is shown, files go with the message being written
  // (into the room's files), text into its composer
  if (app.roomTakesPaste()) {
    const files = items.filter((it) => it.kind === "file").map((it) => it.getAsFile()).filter(Boolean);
    if (files.length) {
      roomChat().attach(files, app.roomChatInThread()).catch(fail);
      needsPaint = true;
      return;
    }
  }
  const text = ev.clipboardData?.getData("text/plain") || "";
  // Sliqtly's own copy carries a picture of itself for other apps: here the
  // text is what is pasted
  const ours = !!text && app.clipKindOf(text) === "sliqtly";
  const picture = items.find((it) => it.kind === "file" && /^image\/(png|jpeg|gif|webp|svg\+xml)$/.test(it.type));
  if (picture && !ours) {
    const file = picture.getAsFile();
    if (file) addPictureFile(file).catch(fail);
    return;
  }
  // slides, an element, a diagram or a spec: asked about, then pasted
  if (text && clipPaste(text, performance.now() - plainAt < 1500)) return;
  if (text && app.focusTarget() === "editor" && dataLink(text)) {
    linkData(text.trim()).catch(fail);
    return;
  }
  if (text) {
    app.pasteText(text);
    afterInput();
  }
});
keys.addEventListener("focus", () => { composing = false; mirrorLine(); needsPaint = true; });
keys.addEventListener("blur", () => { composing = false; });

// --- Copy and paste of slides and elements (src/PresClip.rgr) ----------------------
// Ctrl/⌘+C on the filmstrip or the slide copies the picked slides (else the
// selected one) or the element picked on the slide as Sliqtly's clipboard
// text: the Markdown, the theme rules it uses and its files in base64. A
// paste of that, or of a Mermaid / PlantUML / Graphviz diagram or a
// Vega-Lite spec, is asked about first (the app's question window), files
// before the text so the slides find them; Undo takes the text back and the
// files with it (clip:undo:<n>).
let clipAnswer = null;
let plainAt = -1e9;
const clipFiles = new Map();

// The app's question for a key ("clip", "clipimg"): "ok", "alt", or null
// (not asked, or a newer question took its place; Cancel sends nothing).
function clipAsk(key, open) {
  if (clipAnswer) clipAnswer.resolve(null);
  return new Promise((resolve) => {
    clipAnswer = { key, resolve };
    if (!open()) {
      clipAnswer = null;
      resolve(null);
    }
    needsPaint = true;
  });
}
function clipAnswered(key, alt) {
  if (!clipAnswer || clipAnswer.key !== key) return;
  const { resolve } = clipAnswer;
  clipAnswer = null;
  resolve(alt ? "alt" : "ok");
}

// The copy goes on the clipboard as Sliqtly's text and, for apps that do not
// read it (chat, mail, Word, an image editor), as a PNG of the slides or the
// element, also in HTML as an <img>. The ClipboardItem is made at once, in
// the key's own turn, with promises of its parts: Safari takes a write only
// then. Without ClipboardItem, or when the write is refused, the text alone.
function clipCopy(kind) {
  const spec = JSON.parse(app.clipPictureJson(kind));
  const text = clipCopyText(kind);
  const png = spec.pages ? text.then((s) => (s ? clipPicture(spec) : null)) : Promise.resolve(null);
  let rich = null;
  if (spec.pages && typeof ClipboardItem === "function" && navigator.clipboard?.write) {
    const need = (p) => p.then((v) => v || Promise.reject(new Error("nothing to copy")));
    const parts = {
      "text/plain": need(text).then((s) => new Blob([s], { type: "text/plain" })),
      "image/png": need(png),
    };
    if (!ClipboardItem.supports || ClipboardItem.supports("text/html")) {
      parts["text/html"] = need(png).then(async (b) => new Blob([clipImgHtml(toBase64(await b.arrayBuffer()))], { type: "text/html" }));
    }
    try {
      rich = navigator.clipboard.write([new ClipboardItem(parts)]).then(() => true, () => false);
    } catch (_) {
      rich = null;
    }
  }
  return (async () => {
    const s = await text;
    if (!s) return;
    let ok = rich ? await rich : false;
    if (!ok) ok = await writeClip(s);
    const n = app.clipCount();
    if (!ok) toast(t("Could not copy"));
    else if (kind === "element") toast(t("Copied"));
    else toast(n === 1 ? t("Copied 1 slide.") : t("Copied {n} slides.").replace("{n}", n));
  })();
}

// Sliqtly's clipboard text of the copy, "" when there is nothing to copy.
async function clipCopyText(kind) {
  const files = await docFiles();
  const names = app.clipBegin(kind, files.map((f) => f.path).join("\n"));
  if (!app.clipCount()) return "";
  for (const p of names.split("\n").filter(Boolean)) {
    const f = files.find((x) => x.path === p);
    if (f) app.clipAddFile(p, f.type || "", toBase64(await fileBytes(f)));
  }
  return app.clipText();
}

// The copy's picture (app.clipPictureJson): each slide at rest as the stage
// draws it, one under another, or the element's box of its slide; a PNG
// Blob, null when there is no GPU for it.
const CLIP_PIC_W = 1920;
const CLIP_PIC_MAX_H = 16000;
const CLIP_PIC_GAP = 24;
async function clipPicture(spec) {
  app.settleAll();
  const docs = spec.pages.map((i) => atRest(JSON.parse(app.slideJson(i))));
  if (!docs.length) return null;
  const box = spec.box || null;
  const bw = box ? box[2] : docs[0].width;
  const bh = box ? box[3] : docs[0].height;
  // one slide sharp, many smaller; within the GPU's 4096 and a picture the
  // apps take
  let k = Math.min((docs.length > 1 ? 1280 : CLIP_PIC_W) / bw, 4096 / docs[0].width, 4096 / docs[0].height);
  if (box) k = Math.min(4, k);
  const tall = (h) => docs.length * h + (docs.length - 1) * CLIP_PIC_GAP;
  if (!box && tall(bh * k) > CLIP_PIC_MAX_H) k = (CLIP_PIC_MAX_H - (docs.length - 1) * CLIP_PIC_GAP) / docs.length / bh;
  const w = Math.max(1, Math.round(bw * k));
  const h = Math.max(1, Math.round(bh * k));
  const out = document.createElement("canvas");
  out.width = w;
  out.height = box ? h : tall(h);
  const og = out.getContext("2d");
  og.fillStyle = "#ffffff";
  og.fillRect(0, 0, out.width, out.height);
  const c = document.createElement("canvas");
  const g = c.getContext("webgl2", { antialias: true, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
  if (!g) return null;
  const cut = document.createElement("canvas");
  const cg = cut.getContext("2d");
  docs.forEach((doc, n) => {
    c.width = Math.round(doc.width * k);
    c.height = Math.round(doc.height * k);
    const f = prepareDisplayList(g, doc, { dpr: k, images: pictures, contrastGuard: true, contrastRepair: autoContrast });
    f.draw(null, null);
    const x = box ? Math.max(0, Math.floor(box[0] * k)) : 0;
    const y = box ? Math.max(0, Math.floor(box[1] * k)) : 0;
    const cw = Math.min(c.width - x, w);
    const ch = Math.min(c.height - y, h);
    if (cw > 0 && ch > 0) {
      // GL rows run bottom up
      const up = new Uint8Array(cw * ch * 4);
      g.readPixels(x, c.height - y - ch, cw, ch, g.RGBA, g.UNSIGNED_BYTE, up);
      const rgba = new Uint8ClampedArray(cw * ch * 4);
      for (let r = 0; r < ch; r++) rgba.set(up.subarray((ch - 1 - r) * cw * 4, (ch - r) * cw * 4), r * cw * 4);
      for (let p = 3; p < rgba.length; p += 4) rgba[p] = 255;
      cut.width = cw;
      cut.height = ch;
      cg.putImageData(new ImageData(rgba, cw, ch), 0, 0);
      og.drawImage(cut, 0, n * (h + CLIP_PIC_GAP));
    }
    f.dispose();
  });
  const lose = g.getExtension("WEBGL_lose_context");
  if (lose) lose.loseContext();
  return new Promise((r) => out.toBlob(r, "image/png"));
}

// A pasted file into the deck, as Files → Paste puts one.
async function clipStore(path, type, bytes) {
  if (kindOf(path, type) === "image") {
    await addPicture("/" + path, bytes.slice(0), type || "");
  } else if (isText(path, type)) {
    const text = new TextDecoder().decode(bytes);
    chartFiles.set(path, Promise.resolve(text));
    app.setChartData(path, text);
  }
  await keepFile({ path, type: type || "", size: bytes.byteLength, data: new Blob([bytes], { type: type || "" }) });
}
async function clipDrop(path) {
  pending.delete(path);
  if (doc.persisted && vfs) await vfs.deleteFile(doc.id, path);
  refreshFiles();
  cloudSoon();
}

// Text pasted: true when it is the app's to take (a target of PresClip's),
// which then goes on by itself.
function clipPaste(text, plain) {
  if (viewer) return false;
  const info = JSON.parse(app.clipPaste(text, plain));
  if (!info.target) return false;
  pasteClip(info).catch(fail);
  return true;
}
async function pasteClip(info) {
  const have = new Map((await docFiles()).map((f) => [f.path, f]));
  let steps = [];
  if (info.files) {
    const states = [];
    for (let i = 0; i < info.files; i += 1) states.push(await fileState(have.get(app.clipFileName(i)), app.clipFileData(i)));
    steps = app.clipFileSteps(states.join("\n")).split("\n");
  }
  const answer = await clipAsk("clip", () => app.clipAsk());
  if (!answer) return;
  const put = [];
  for (let i = 0; i < steps.length; i += 1) {
    const name = app.clipFileName(i);
    if (steps[i] === "add") put.push({ i, to: name });
    else if (steps[i] === "ask") {
      const a = await clipAsk("clipimg", () => app.clipAskPicture(name.split("/").pop()));
      if (!a) return;
      if (a === "ok") put.push({ i, to: name });
      else put.push({ i, to: app.clipKeepBoth(name, [...have.keys(), ...put.map((p) => p.to)].join("\n")) });
    }
  }
  const undo = [];
  for (const p of put) {
    const type = app.clipFileMime(p.i);
    const bytes = fromBase64(app.clipFileData(p.i));
    const prev = have.get(p.to) || null;
    undo.push({ path: p.to, type, bytes, prev: prev ? { type: prev.type || "", bytes: await fileBytes(prev) } : null });
    await clipStore(p.to, type, bytes);
  }
  const id = app.clipApply(answer === "alt");
  if (id && undo.length) clipFiles.set(id, undo);
  dropThumbs();
  afterInput();
  focusApp();
}

// Undo / Redo of a paste: its files back as they were, or in again.
async function clipFilesBack(id, back) {
  const list = clipFiles.get(id);
  if (!list) return;
  for (const f of back ? [...list].reverse() : list) {
    if (!back) await clipStore(f.path, f.type, f.bytes);
    else if (f.prev) await clipStore(f.path, f.prev.type, f.prev.bytes);
    else await clipDrop(f.path);
  }
  dropThumbs();
  needsPaint = true;
}

// Edit's rows and the slide menu's Copy / Paste.
async function editRequest(what) {
  if (what === "undo" || what === "redo") {
    app.chord(what === "undo" ? "z" : "y");
    afterInput();
  } else if (what === "copySlides" || what === "copyElement") {
    await clipCopy(what === "copySlides" ? "slides" : "element");
  } else if (what === "copy") {
    const kind = app.clipCopyKind();
    if (kind) await clipCopy(kind);
    else {
      const text = app.copySelection();
      if (text) await writeClip(text);
    }
  } else if (what === "cut") {
    const text = app.cutSelection();
    if (text) await writeClip(text);
    afterInput();
  } else if (what === "paste" || what === "pastePlain") {
    let text = "";
    try {
      text = await navigator.clipboard.readText();
    } catch (_) {
      toast(t("The browser did not let the page read the clipboard: press Ctrl+V (⌘V on a Mac) instead."));
      return;
    }
    if (!text) return;
    if (!clipPaste(text, what === "pastePlain")) {
      app.pasteText(text);
      afterInput();
    }
  }
  needsPaint = true;
}

document.addEventListener("keydown", (ev) => {
  if (plainChord(ev)) plainAt = performance.now();
}, true);
// The filmstrip's slides have the keyboard on the a11y mirror, not on the
// hidden field: their copy and paste arrive here.
document.addEventListener("copy", (ev) => {
  if (ev.target === keys || !mirror.root.contains(ev.target)) return;
  const kind = app.clipCopyKind();
  if (!kind) return;
  ev.preventDefault();
  clipCopy(kind).catch(fail);
});
document.addEventListener("paste", (ev) => {
  if (ev.target === keys || !mirror.root.contains(ev.target)) return;
  const text = ev.clipboardData?.getData("text/plain") || "";
  if (!text) return;
  ev.preventDefault();
  clipPaste(text, performance.now() - plainAt < 1500);
});

// --- the pointer ----------------------------------------------------------------------
function at(ev) {
  const r = canvas.getBoundingClientRect();
  return [ev.clientX - r.left, ev.clientY - r.top];
}

// A realistic book's page taken by its corner while presenting: the
// presses before the stage's own (capture), which do not see them.
const onBookPage = (ev) => {
  const [x, y] = at(ev);
  return { x: (x - bookPlace.spineX) / bookPlace.scale, y: (y - bookPlace.top) / bookPlace.scale };
};
canvas.addEventListener("pointerdown", (ev) => {
  if (!bookPlace || bookTurn || ev.button !== 0 || !lastLayout || !lastLayout.book) return;
  const { b, s } = bookPlace;
  const p = onBookPage(ev);
  const t = grabTurn(b.spreads, s, p.x, p.y, b.w, b.h);
  if (!t) return;
  t.held = { id: ev.pointerId, t: performance.now(), x: p.x, vx: 0 };
  t.byHand = true;
  bookTurn = t;
  canvas.setPointerCapture(ev.pointerId);
  ev.stopImmediatePropagation();
  needsPaint = true;
}, true);
canvas.addEventListener("pointermove", (ev) => {
  if (!bookPlace || !lastLayout || !lastLayout.book) return;
  if (!bookTurn && ev.pointerType === "mouse") {
    // a corner that can be taken: the hand says so
    const { b, s } = bookPlace;
    const p = onBookPage(ev);
    if (grabTurn(b.spreads, s, p.x, p.y, b.w, b.h)) {
      canvas.style.cursor = "grab";
      ev.stopImmediatePropagation();
    }
    return;
  }
  if (!bookTurn || !bookTurn.held || bookTurn.held.id !== ev.pointerId) return;
  ev.stopImmediatePropagation();
  canvas.style.cursor = "grabbing";
  const p = onBookPage(ev);
  dragTurn(bookTurn, p.x, p.y, performance.now());
  needsPaint = true;
}, true);
const letBookPage = (ev) => {
  if (!bookTurn || !bookTurn.held || bookTurn.held.id !== ev.pointerId) return;
  ev.stopImmediatePropagation();
  canvas.style.cursor = "";
  releaseTurn(bookTurn, bookPlace.b.w, performance.now());
  needsPaint = true;
};
canvas.addEventListener("pointerup", letBookPage, true);
canvas.addEventListener("pointercancel", letBookPage, true);

let clicks = 0;
let lastDown = 0;
let lastDownAt = [0, 0];

// Fingers on the canvas. Two on the stage are a pinch: the slide is seen
// closer (or further) and moved with them, and nothing under them is
// pressed. The page itself does not zoom (touch-action: none), so this is
// how a phone gets to read a slide's small print.
const touches = new Map();
let pinch = null;
function pinchSpan() {
  const [a, b] = [...touches.values()];
  return { d: Math.hypot(a[0] - b[0], a[1] - b[1]) || 1, mx: (a[0] + b[0]) / 2, my: (a[1] + b[1]) / 2 };
}

// The colour card's pipette: the screen's own picker where the browser has
// one (EyeDropper), else the next press on the page samples its pixel.
let canvasPicking = false;
// the release of the press that sampled: not a click on what is under it
let pickRelease = false;

function startColorPick() {
  if (window.EyeDropper) {
    new window.EyeDropper().open().then(
      (r) => { app.hintPicked(r.sRGBHex || ""); afterInput(); },
      () => { app.hintPicked(""); afterInput(); },
    );
    return;
  }
  canvasPicking = true;
  canvas.style.cursor = "crosshair";
}

function endCanvasPick(hex) {
  canvasPicking = false;
  canvas.style.cursor = "";
  app.hintPicked(hex);
  afterInput();
}

function pixelAt(x, y) {
  const r = canvas.getBoundingClientRect();
  const px = Math.floor((x * canvas.width) / r.width);
  const py = canvas.height - 1 - Math.floor((y * canvas.height) / r.height);
  const out = new Uint8Array(4);
  gl.readPixels(px, py, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, out);
  return "#" + [out[0], out[1], out[2]].map((v) => v.toString(16).padStart(2, "0")).join("");
}

canvas.addEventListener("pointerdown", (ev) => {
  const [x, y] = at(ev);
  if (canvasPicking) {
    ev.preventDefault();
    pickRelease = true;
    endCanvasPick(pixelAt(x, y));
    return;
  }
  const finger = ev.pointerType !== "mouse";
  if (finger) touches.set(ev.pointerId, [x, y]);
  if (finger && touches.size === 2) {
    const p = pinchSpan();
    if (app.canZoomAt(p.mx, p.my)) {
      app.cancelPress();
      pinch = p;
      clicks = 0;
      ev.preventDefault();
      try { canvas.setPointerCapture(ev.pointerId); } catch (_) { /* no capture */ }
      needsPaint = true;
      return;
    }
  }
  if (pinch) return;
  // the secondary button (or Control + click on a Mac) on a slide of the
  // strip: its menu (contextmenu below), not a press that picks the slide
  if (secondaryPress(ev, IS_MAC) && (app.inStrip(x, y) || app.inDeckRow(x, y))) {
    ev.preventDefault();
    return;
  }
  const now = performance.now();
  // the second click of a double click on a link: the block is picked, the
  // link stays where it is
  clearTimeout(linkTimer);
  const near = Math.hypot(x - lastDownAt[0], y - lastDownAt[1]) < 40;
  clicks = now - lastDown < 400 && (!finger || near) ? clicks + 1 : 1;
  lastDown = now;
  lastDownAt = [x, y];
  // a double tap on the slide while editing: in to read it, or back out
  if (finger && clicks === 2 && !(lastLayout && lastLayout.mode === "present") && app.canZoomAt(x, y)) {
    ev.preventDefault();
    app.cancelPress();
    app.viewToggle(x, y);
    // the first tap's note about the diagram is not wanted
    clearTimeout(diagramTimer);
    clicks = 0;
    needsPaint = true;
    return;
  }
  // a program's box (```app) takes the press, and the keyboard with it
  if (apps.pointerDown(x, y, !!(lastLayout && lastLayout.mode === "present"))) {
    ev.preventDefault();
    return;
  }
  // a finger wobbles: it has to travel further than a mouse before a tap
  // on the stage becomes a drag
  app.setDragSlop(finger ? 16 : 6);
  app.setTouch(finger);
  app.setCtrl(pickKeyHeld(ev, IS_MAC));
  const where = app.pointerDown(x, y, ev.shiftKey, Math.min(clicks, 3));
  ev.preventDefault();
  if (where === "hint" && app.hintWantsPick()) startColorPick();
  if (where === "editor" || where === "sep" || where === "scrub" || where === "stage" || where === "chart" || where === "hint" || where === "thumb" || where === "select" || where === "panel" || where === "decktabs") {
    try { canvas.setPointerCapture(ev.pointerId); } catch (_) { /* no capture */ }
  }
  if (where === "editor" && clicks === 1) {
    const emojiAtPress = app.emojiIsOpen();
    setTimeout(() => {
      // the emoji picker opened from the keyboard since the press: it stays
      if (!emojiAtPress && app.emojiIsOpen()) return;
      // a low-contrast mark in the gutter: its warning, and nothing else
      let g = null;
      try { g = JSON.parse(app.contrastHintAt(x, y) || "null"); } catch (_) { g = null; }
      if (g) { showHint(g); return; }
      let h = null;
      try { h = JSON.parse(app.hintAtCaret() || "null"); } catch (_) { h = null; }
      if (h && isChartFence(h)) {
        // a chart's fence opens the chart editor, not a list of languages
        closeHint();
        app.openChartEditor(h.line);
        focusKeys("editor");
        afterInput();
      } else if (app.openImageAtCaret()) {
        // a picture's line opens the picture's settings
        closeHint();
        focusKeys("editor");
        afterInput();
      } else if (h) showHint(h);
      else closeHint();
    }, 0);
  } else if (where === "select") {
    // a button or a band of the picked element: the app opened its popover,
    // which stays until Esc, × or a press outside; a hover's timers are done
    clearTimeout(hintTimer);
    clearTimeout(hintCloseTimer);
    hint = null;
    hintKey = "";
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
  } else if (where === "thumb") {
    // the keyboard to the strip (endPointer moves it to the slide selected)
    paintOnce();
    focusStrip();
  } else {
    focusKeys(where === "editor" ? "editor" : app.focusTarget());
  }
  afterInput();
});
canvas.addEventListener("pointermove", (ev) => {
  const [x, y] = at(ev);
  apps.pointerMove(x, y);
  if (touches.has(ev.pointerId)) touches.set(ev.pointerId, [x, y]);
  if (pinch) {
    if (touches.size >= 2) {
      const p = pinchSpan();
      app.viewPinch(p.mx, p.my, p.d / pinch.d, p.mx - pinch.mx, p.my - pinch.my);
      pinch = p;
      needsPaint = true;
    }
    return;
  }
  app.pointerMove(x, y);
  canvas.style.cursor = app.cursorAt(x, y);
  if (ev.buttons) { needsPaint = true; if (tipFor === canvas) hideTip(); }
  else if (ev.pointerType === "mouse") {
    hintHover(x, y);
    const tip = app.tipAt(x, y);
    if (tip) showTip(tip, ev.clientX, ev.clientY - 12, canvas);
    else if (tipFor === canvas) hideTip();
  }
});
// off the page a diagram's buttons fade, as when the pointer leaves the diagram
canvas.addEventListener("pointerleave", () => { app.pointerLeft(); if (tipFor === canvas) hideTip(); });
canvas.addEventListener("pointerdown", () => { if (tipFor === canvas) hideTip(); });

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
  return h.name === t("language") && /^vega-?lite$/.test(h.value);
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
  // opened from the slide: stays until a press outside it
  if (app.hintPinned()) return;
  if (app.hintIsOpen() && app.hintHas(x, y)) {
    clearTimeout(hintCloseTimer);
    return;
  }
  hintTimer = setTimeout(() => {
    // a card opened from the slide meanwhile (Style, a band) or the emoji
    // picker from the keyboard is not the hover's
    if (app.hintPinned()) return;
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
    if (!overHint() && !app.hintPinned()) closeHint();
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

// --- the help panel ---------------------------------------------------------------
// What the selected slide is made of — a highlight, a list, a formula — with
// how each is written and the theme's CSS rules that change how it looks,
// at the values the theme gives them now. Only what the slide has is
// listed. A property opens the theme at its line (added when the theme has
// none) with its value popover.
// Help for this slide closes the panel when it already shows the slide;
// Help → How to use Sliqtly opens it at the guide (PresHelp.guide).
function toggleHelp(on) {
  if (on ?? !(app.helpIsOpen() && app.helpTab() === "slide")) app.openHelpTab("slide");
  else app.setHelp(false);
  needsPaint = true;
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
document.getElementById("modeBtn").addEventListener("click", () => toggleMode());

function endPointer(ev) {
  touches.delete(ev.pointerId);
  if (pickRelease) {
    pickRelease = false;
    return;
  }
  if (pinch) {
    // the pinch lasts until the last finger is lifted
    if (touches.size === 0) {
      pinch = null;
      app.viewSettle();
      needsPaint = true;
    }
    return;
  }
  app.pointerUp();
  if (ev.pointerType === "mouse" && ev.button !== 2 && app.focusTarget() === "strip" && !app.toolbarOnTop()) {
    paintOnce();
    focusStrip();
  }
  // a tap that made a diagram the one a finger moves says so, once
  // (after a moment: the tap may be the first of a double tap)
  if (app.takeActivated() && !diagramTold) {
    clearTimeout(diagramTimer);
    diagramTimer = setTimeout(() => {
      diagramTold = true;
      toast(t("Diagram selected: drag to move it, pinch to zoom the slide"));
    }, 420);
  }
  // a click on the stage acts on release (a press that moves is a drag),
  // so what it did to the clock is taken up here
  afterInput();
}
// The page itself must not move under a gesture on the canvas: no bounce,
// no browser zoom (iOS Safari zooms on a pinch despite touch-action and the
// viewport tag), no address bar sliding in and out and resizing the canvas
// mid-pinch.
canvas.addEventListener("touchmove", (ev) => ev.preventDefault(), { passive: false });
for (const g of ["gesturestart", "gesturechange", "gestureend"]) {
  document.addEventListener(g, (ev) => ev.preventDefault(), { passive: false });
}

let diagramTold = false;
let diagramTimer = 0;
canvas.addEventListener("pointerup", endPointer);
window.addEventListener("pointerup", () => apps.pointerUp());
// A right click on a slide of the strip: New, Duplicate, Move, Delete; on
// a room's chat, a message's menu; on a presentation in Rooms or a deck tab:
// the File menu's rows for it.
canvas.addEventListener("contextmenu", (ev) => {
  const [x, y] = at(ev);
  // on a room's chat: the message's menu (React, Reply, Quote, Copy, Edit, Delete)
  if (app.roomMenuAt(x, y)) {
    ev.preventDefault();
    closeHint();
    afterInput();
    return;
  }
  if (!app.deckMenuAt(x, y) && !app.slideMenuAt(x, y)) return;
  ev.preventDefault();
  closeHint();
  afterInput();
  focusSlideMenu();
});
canvas.addEventListener("pointercancel", endPointer);
canvas.addEventListener("wheel", (ev) => {
  const [x, y] = at(ev);
  // a trackpad pinch (or Ctrl + wheel) over the slide sees it closer
  if (ev.ctrlKey && app.canZoomAt(x, y)) {
    ev.preventDefault();
    app.viewPinch(x, y, Math.exp(-ev.deltaY * 0.01), 0, 0);
    needsPaint = true;
    return;
  }
  // over the code editor it sizes the text: a wheel notch (100 px, or three
  // lines) is a step, a pinch adds up its small deltas to one
  if (ev.ctrlKey && app.editorAt(x, y)) {
    ev.preventDefault();
    editorWheel += ev.deltaMode === 0 ? ev.deltaY : ev.deltaY * 40;
    while (Math.abs(editorWheel) >= 100) {
      zoomEditor(editorWheel < 0 ? 1 : -1);
      editorWheel -= Math.sign(editorWheel) * 100;
    }
    return;
  }
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
  // dropped on a room's chat: files for the message being written there
  const [dx, dy] = at(ev);
  if (files.length && app.roomChatOpen() && app.layerAt(dx, dy) === "room") {
    roomChat().attach(files, app.roomChatInThread()).catch(fail);
    return;
  }
  for (const f of files) {
    if (/^image\//.test(f.type)) addPictureFile(f).catch(fail);
    else addDocFile(f, true).catch(fail);
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
  const l = selectRows(langSel);
  const key = s + "|" + sampleSel.value + "|" + t + "|" + themeSel.value + "|" + langSel.value;
  if (key === barSynced) return;
  barSynced = key;
  app.setToolbarOptions("sample", s, sampleSel.value);
  app.setToolbarOptions("theme", t, themeSel.value);
  app.setToolbarOptions("lang", l, langSel.value);
}

// This browser's store is opened at a newer version than a tab loaded
// before an update has it open at: the page waits for that tab (web/vfs.js)
// and says so on the loading screen.
function tabsInTheWay() {
  loadNote.textContent = t("Waiting for your other Sliqtly tabs. Close or reload the ones opened before the update.");
}
// The browser's store did not answer a call in time (web/vfs.js): it was
// tried again over a new connection. Said once, while it lasts.
let stallNoted = false;
function storeStalled() {
  if (stallNoted) return;
  stallNoted = true;
  setTimeout(() => { stallNoted = false; }, 60000);
  if (document.body.classList.contains("booting")) loadNote.textContent = t("The browser's storage is not answering. Waiting for it; closing other Sliqtly tabs may help.");
  else toast(t("The browser's storage is not answering. Your changes are saved once it does; closing other Sliqtly tabs may help."));
}
// A newer Sliqtly in another tab took the store over: this page can no
// longer save, and says so until it is reloaded.
function closedByUpdate() {
  const note = document.createElement("div");
  note.id = "tabNotice";
  note.setAttribute("role", "alert");
  note.textContent = t("Sliqtly was updated in another tab. Reload this page to keep saving.");
  document.body.appendChild(note);
}

// View version (web/version-view.js): this page in the editor's frame,
// showing an older version of its deck. The viewer from the start, so no
// store is opened and nothing is saved.
const versionFrame = isViewFrame(location.search, window.parent !== window);
if (versionFrame) {
  viewer = true;
  document.body.classList.add("viewer", "versionFrame");
}
// A presentation exported as one .html file (web/player-file.js): the viewer
// from the start, the deck from the file, no store, nothing saved.
const playerDeck = versionFrame ? null : embeddedDeck(document);
if (playerDeck) {
  viewer = true;
  document.body.classList.add("viewer", "playerFile");
}
// → the version the editor hands over, once this page says it is ready
function versionFromEditor() {
  return new Promise((done) => {
    const take = (ev) => {
      if (ev.source !== window.parent || ev.origin !== location.origin) return;
      const v = readPacket(ev.data);
      if (!v) return;
      window.removeEventListener("message", take);
      done(v);
    };
    window.addEventListener("message", take);
    window.parent.postMessage(readyMessage(), location.origin);
  });
}
async function openVersionView() {
  const v = await versionFromEditor();
  beginDoc(v.md);
  themeSel.value = v.theme;
  app.setStyleSheet(v.theme ? themeCss[v.theme] || "" : "");
  if (v.css != null) {
    editedCss[v.theme] = v.css;
    app.setStyleSheet(v.css);
  }
  for (const f of v.files) {
    const size = typeof f.data === "string" ? f.data.length : f.data.size;
    const rec = { doc: doc.id, path: f.path, type: f.type, size, data: f.data, updated: Date.now() };
    readFiles.set(rec.path, rec);
    await useFile(rec).catch((e) => console.warn("version file not shown: " + f.path, e));
  }
  docName = v.name || "presentation";
  shownDoc(v.md);
  dropThumbs();
  needsPaint = true;
  enterViewer({ from: "version" });
}

async function openPlayerDeck() {
  const d = playerDeck;
  beginDoc(d.md);
  themeSel.value = d.theme;
  app.setStyleSheet(d.theme ? themeCss[d.theme] || "" : "");
  if (d.css != null) {
    editedCss[d.theme] = d.css;
    app.setStyleSheet(d.css);
  }
  for (const f of d.files) {
    const data = fileData(f);
    const size = typeof data === "string" ? data.length : data.size;
    const rec = { doc: doc.id, path: f.path, type: f.type, size, data, updated: Date.now() };
    readFiles.set(rec.path, rec);
    await useFile(rec).catch((e) => console.warn("file not shown: " + f.path, e));
  }
  docName = d.name;
  shownDoc(d.md);
  dropThumbs();
  needsPaint = true;
  enterViewer({ from: "file" });
}

async function start() {
  // everything start-up reads is asked for at once
  const toolbarCss = viewer ? null : textOf("./toolbar.css");
  toolbarCss?.catch(() => {});
  const themesGot = THEMES.map((name) => textOf("./themes/" + name + ".css"));
  for (const p of themesGot) p.catch(() => {});
  // the chart editor's controls: the kit's theme, then the app's colours
  // the skins go after every chrome sheet: their rules are theme-scoped, so
  // they only apply once the editor runs under that theme (applySkin)
  const [css0, kit, chartCss, skins] = await Promise.all([
    textOf("./pres.css"),
    textOf("./ui.css").catch(() => ""),
    textOf("./chart-editor.css").catch(() => ""),
    Promise.all(["ui-retro", "retro", "ui-dark", "dark"].map((f) => textOf("./skins/" + f + ".css")))
      .then((t) => "\n" + t.join("\n")).catch(() => ""),
  ]);
  chromeSheets.files = skins;
  chromeSheets.chrome = css0;
  const css = css0 + skinCss(css0, "");
  chromeSheets.chart = kit + "\n" + chartCss;
  app.setChartCss(chromeSheets.chart + skinCss(chromeSheets.chart, "ce"));
  textOf("./hint.css").then((c) => { chromeSheets.hint = kit + "\n" + chartCss + "\n" + c; app.setHintCss(chromeSheets.hint + skinCss(chromeSheets.hint, "hp")); }).catch(() => {});
  textOf("./panels.css").then((c) => { chromeSheets.panels = kit + "\n" + c; app.setPanelsCss(chromeSheets.panels + skinCss(chromeSheets.panels, "pn")); }).catch(() => {});
  // the signed-in account's own store and keys, before anything kept is read
  useAccount(await accountScope().catch((e) => { console.warn("no account known", e); return EDITOR_ROOT && !viewer ? "signed-out" : ""; }));
  if (!viewer) {
    // the bar moves onto the canvas: the HTML one stays, hidden, as what it
    // presses (its buttons and selects keep every behaviour they had)
    chromeSheets.toolbar = kit + "\n" + (await toolbarCss);
    app.setToolbarCss(chromeSheets.toolbar + skinCss(chromeSheets.toolbar, "tb"));
    document.body.classList.add("canvas-bar");
    canvasBar = true;
    // the presentations open in this tab, as they were before a reload
    app.useDeckTabs(true);
    app.deckTabsRestore(readDeckTabs(mineTab));
    syncBarExtras();
    new MutationObserver(syncBarExtras).observe(document.getElementById("bar"),
      { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ["hidden", "data-canvas", "data-short"] });
    app.useToolbar(true);
    requestAnimationFrame(resize);
  }
  const r = stageEl.getBoundingClientRect();
  app.init(css, Math.max(320, r.width), Math.max(240, r.height));
  if (!viewer) applySkin();
  reviewMe();
  applyReviewMode();
  // a server of one's own is known once its window.sliqtly is there
  window.addEventListener("sliqtly:ready", () => { applyReviewMode(); reviewMe(); });
  window.addEventListener("sliqtly:user", () => reviewMe());
  app.setCoarse(isCoarse());
  app.setMac(IS_MAC);
  try { emojiRecent = localStorage.getItem(EMOJI_RECENT) || ""; } catch (_) { emojiRecent = ""; }
  app.setEmojiRecent(emojiRecent);
  restoreEditorZoom();
  resize();
  window.addEventListener("resize", resize);

  const got = new Array(FACES.length).fill(false);
  await Promise.all(FACES.map(async ([name, file], i) => {
    try {
      const bytes = await fontBytes(file);
      got[i] = app.attachFont(name, asRangerBuffer(bytes.slice(0)));
      const face = new FontFace(name, bytes);
      await face.load();
      document.fonts.add(face);
    } catch (e) {
      console.warn("face not loaded: " + name, e);
    }
  }));
  pageFaces = FACES.filter((_, i) => got[i]).map(([name]) => name);
  setFontFallback(pageFaces);
  // An emoji, and anything no loaded face has, is drawn from a face only the
  // browser knows; the editor and the slides ask the browser for its width,
  // per cluster, in the font stack the painter draws with.
  {
    const m = document.createElement("canvas").getContext("2d");
    app.setPlatformMeasure((text, family, size) => {
      m.font = fontSpec({ font: family, size }, 1);
      return m.measureText(text).width;
    });
  }

  for (const [i, name] of THEMES.entries()) {
    try {
      themeCss[name] = await themesGot[i];
      app.addTemplate(name, themeCss[name]);
    } catch (_) { /* one template fewer */ }
  }

  const q = new URLSearchParams(location.search);
  // read before a deck is opened: opening one tidies the address
  const at = framed ? new URLSearchParams() : hashParams();
  const theme = q.has("theme") ? q.get("theme") : "aurora";
  themeSel.value = theme;
  app.setStyleSheet(theme ? themeCss[theme] || "" : "");
  if (!viewer && !hashShare()) vfs = CLOUD_ONLY ? memoryStore() : await openVfs({ waiting: tabsInTheWay, closed: closedByUpdate, stalled: storeStalled });
  const own = ownDeck();
  // /s/{id}?edit (or an older ?deck=…&from={id}) of the signed-in owner's
  // own deck: opened from the cloud, where it lives
  const editId = DECK_PATH.exec(location.pathname)?.[1] || own?.from;
  const editing = !!own || (!!editId && editAsked());
  // the browser's store not answering (web/vfs.js gives up on a stuck
  // call) leaves no page behind the loader: the welcome deck opens, and the
  // notice says why one's own is not there
  try {
    if (versionFrame) await openVersionView();
    else if (playerDeck) await openPlayerDeck();
    else if (editing && editId && (await openOwnCloud(editId).catch((e) => { console.warn(e); return false; }))) {
      // a deck's link on a server of one's own: its room open on the left,
      // with the room's presentations
      if (ownServer() && roomShown) {
        app.showRooms();
        roomsRequest("room:list").catch(() => {});
      }
    }
    else if (own && vfs && (await ownIsNewer(own)) && (await openDoc(own.deck))) plainAddress();
    else if (!(await openFromShare()) && !(await openFromHash())) {
      const want = q.get("sample");
      // the tab in front before a reload, when its deck is still there
      const front = want || at.get("doc") ? "" : app.deckTabFront();
      if (front && (await openDeckKey(front).catch(() => false))) { /* opened */ }
      else {
        if (front) app.deckTabClose(front);
        // no sample asked for: the deck worked on last, if this browser kept one,
        // from the cloud when it lives there
        let last = null;
        try { last = mine.getItem("evgp.doc"); } catch (_) { /* none */ }
        // the deck this tab had (keepTabDoc), or an older link's #doc={id},
        // when this browser keeps it
        let asked = at.get("doc");
        if (!asked && !framed) {
          try { asked = mineTab.getItem(TAB_DOC); } catch (_) { /* none */ }
        }
        if (asked && /^[A-Za-z0-9_-]{1,64}$/.test(asked) && vfs && (await vfs.getDoc(asked))) last = asked;
        const lastCloud = want || !last || !vfs ? null : last.startsWith("cloud:") ? last.slice(6) : (await vfs.getDoc(last))?.cloud;
        if (lastCloud && (await openOwnCloud(lastCloud).catch(() => false))) { /* opened */ }
        else if (want || !last || !(await openDoc(last))) {
          const sample = SAMPLES[want] || HIDDEN_SAMPLES[want] ? want : "welcome";
          if (SAMPLES[sample]) sampleSel.value = sample;
          await openSample(sample);
          if (!want) welcomeCard();
        }
      }
    }
  } catch (e) {
    console.warn("the deck could not be opened", e);
    if (doc.loading || !app.source()) await openSample("welcome");
  }
  refreshRecent().catch(() => {});
  useAddress(at);

  // A narrow window gets the slides without the editor (PresApp.isCompact,
  // decided on every layout, so it follows the window); on a touch screen
  // the hidden text field is not focused, so no keyboard comes up.
  if (viewer || isCoarse()) keys.blur();

  // the loading screen has its moment: the logo's turn (1.2 s from the
  // page's start), then it fades as the editor appears; a shared
  // presentation keeps it as its intro
  if (!introPending) await new Promise((r) => setTimeout(r, Math.max(0, 1200 - performance.now())));
  document.body.classList.remove("booting");
  if (!viewer && !isCoarse()) focusKeys("editor");
  window.__pageStarted = true;
  if (introPending) {
    playIntro().then(introPending);
    introPending = null;
  } else {
    window.__introAt = undefined;
    hideIntro();
  }
  // opening the deck tidied the address; it follows the screen from here
  followAddress();
  // ?export=pdf|pptx|docx|html|md (or in the #…): an export asked for from the
  // assistant's preview, which cannot download
  const ask = q.get("export") || hashParams().get("export");
  if (viewer && !framed && EXPORTS[ask]) {
    toast(t("Preparing the download…"));
    EXPORTS[ask]().catch(fail);
    // once: a reload shows the deck without downloading it again
    const h = hashParams();
    h.delete("export");
    q.delete("export");
    const search = q.toString();
    history.replaceState(null, "", location.pathname + (search ? "?" + search : "") + (location.hash ? "#" + h.toString() : ""));
    lastHash = location.hash;
  }
  requestAnimationFrame(frame);
}

start().catch(fail);
