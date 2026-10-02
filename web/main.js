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
import { createA11yMirror, pressAtCentre } from "./gl/evg-a11y.js";
import { openVfs, kindOf, isText, placeFor, newId } from "./vfs.js";
import { lang, LANGS, t, pairs, translateDom, chooseLang } from "./i18n.js";

const canvas = document.getElementById("c");
const stageEl = document.getElementById("stage");
const keys = document.getElementById("keys");
const hintEl = document.getElementById("hint");
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
const THEMES = ["aurora", "nebula", "carbon", "ember", "midnight", "corporate", "editorial"];
const SAMPLES = {
  talous: ["Talous: oma talous haltuun", "./samples/talous.md"],
  ymparisto: ["Ympäristö: hiilijalanjälki", "./samples/ymparisto.md"],
  urheilu: ["Urheilu: 5 km juoksukoulu", "./samples/urheilu.md"],
  kulttuuri: ["Kulttuuri: musiikin vuosikymmenet", "./samples/kulttuuri.md"],
  ohjelmointi: ["Ohjelmointi: versionhallinta", "./samples/ohjelmointi.md"],
  matematiikka: ["Matematiikka: kaavat kalvoilla", "./samples/matematiikka.md"],
  vegalite: ["Vega-Lite: kaaviotyypit", "./samples/vegalite.md"],
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
  hintEl.textContent = t("WebGL 2 is not available in this browser.");
  throw new Error("no WebGL 2");
}
if (typeof globalThis.PresApp !== "function") {
  hintEl.textContent = t("pres_app.js is missing. Run `npm run build`.");
  throw new Error("engine bundle not loaded");
}
// the interface's language, before anything is built in it
globalThis.PresI18n.use(lang, pairs());
translateDom();
// i18n: "Dark" "Light" (the theme list's groups)
for (const g of document.querySelectorAll("optgroup[label]")) g.label = t(g.label);
const app = new globalThis.PresApp();
window.__app = app;
app.setAppName(APP_NAME);

// A page built on this one can add buttons of its own to the bar: an element
// in #bar with data-canvas="<variant>" is drawn on the canvas bar after Ohje
// (with its text, followed as it changes), and pressing it clicks it.
function syncBarExtras() {
  const rows = [...document.querySelectorAll("#bar [data-canvas]")]
    .filter((el) => el.id && !el.hidden)
    .map((el) => [el.id, el.textContent.trim().replace(/\s+/g, " "), el.dataset.canvas || "secondary"].join("\t"));
  app.setToolbarExtras(rows.join("\n"));
  needsPaint = true;
}

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
  await keepFile({ path: rel, type, size: bytes.byteLength, data: new Blob([bytes], { type }) });
  app.insertPicture(rel, file.name && file.name !== "image.png" ? file.name.replace(/\.[^.]+$/, "") : "image");
  dropThumbs();
  afterInput();
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
const doc = { id: newId(), persisted: false, created: Date.now(), openedText: "" };
const pending = new Map();
let savedText = null;
let savedCss = null;
let savedTheme = null;
let saving = null;

function beginDoc(text) {
  doc.id = newId();
  doc.persisted = false;
  doc.created = Date.now();
  doc.openedText = text;
  pending.clear();
  savedText = null;
  savedCss = null;
  for (const k of Object.keys(editedCss)) delete editedCss[k];
  chartFiles.clear();
  chartFilesRev = -1;
  app.clearChartData();
  if (app.openFilePath()) app.closeFile();
}

function bare(path) {
  return String(path).replace(/^\.?\//, "");
}

async function keepFile(rec) {
  const file = { doc: doc.id, updated: Date.now(), ...rec, path: bare(rec.path) };
  if (doc.persisted && vfs) await vfs.putFile(file);
  else pending.set(file.path, file);
  refreshFiles();
}

async function docFiles() {
  const out = new Map();
  if (doc.persisted && vfs) for (const f of await vfs.listFiles(doc.id)) out.set(f.path, f);
  for (const [k, f] of pending) out.set(k, f);
  return [...out.values()];
}

async function saveDoc(force) {
  if (!vfs || viewer) return;
  if (saving) await saving;
  const md = app.source();
  const key = themeSel.value || "";
  const css = key in editedCss ? editedCss[key] : null;
  if (md === savedText && css === savedCss && key === savedTheme && !force) return;
  // a deck as it was opened is not kept until someone changes it, nor an
  // empty one
  if (!doc.persisted && !force && ((md === doc.openedText && css === null) || !md.trim())) return;
  saving = (async () => {
    await vfs.putDoc({ id: doc.id, name: exportName(), md, theme: key, css, created: doc.created, updated: Date.now() });
    if (!doc.persisted) {
      doc.persisted = true;
      for (const f of pending.values()) await vfs.putFile({ ...f, doc: doc.id });
      pending.clear();
    }
    savedText = md;
    savedCss = css;
    savedTheme = key;
    try { localStorage.setItem("evgp.doc", doc.id); } catch (_) { /* the next start opens a sample */ }
  })();
  try { await saving; } finally { saving = null; }
  refreshFiles();
}

// A file of the document put to use: a picture registered for the slides, a
// text file handed to the charts.
async function useFile(f) {
  if (kindOf(f.path, f.type) === "image" && f.data instanceof Blob) {
    const bytes = await f.data.arrayBuffer();
    const [w, h] = await imageSize(bytes, f.type);
    app.addImage("/" + f.path, asRangerBuffer(bytes.slice(0)), f.type || "image/png", w, h);
    await registerPicture("/" + f.path, bytes, f.type || "image/png");
  } else if (typeof f.data === "string") {
    chartFiles.set(f.path, Promise.resolve(f.data));
    app.setChartData(f.path, f.data);
  }
}

async function openDoc(id) {
  if (!vfs) return false;
  await saveDoc();
  const d = await vfs.getDoc(id);
  if (!d) return false;
  beginDoc(d.md);
  doc.id = d.id;
  doc.persisted = true;
  doc.created = d.created || Date.now();
  themeSel.value = d.theme || "";
  if (d.css != null) editedCss[d.theme || ""] = d.css;
  useTheme(themeSel.value);
  for (const f of await vfs.listFiles(doc.id)) await useFile(f);
  docName = d.name || "presentation";
  app.setSource(d.md);
  savedText = d.md;
  savedCss = d.css == null ? null : d.css;
  savedTheme = d.theme || "";
  try { localStorage.setItem("evgp.doc", doc.id); } catch (_) { /* fine */ }
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

// The files tab's list, when it shows; again when PRO signs in or out.
let filesListing = false;
window.addEventListener("sliqtly:user", () => refreshFiles());
async function refreshFiles() {
  if (!vfs || app.editorTab() !== "files" || filesListing) return;
  filesListing = true;
  try {
    const files = (await docFiles())
      .map((f) => ({ path: f.path, size: f.size == null ? -1 : f.size, kind: kindOf(f.path, f.type) }))
      .sort((a, b) => {
        const da = a.path.includes("/") ? 1 : 0;
        const db = b.path.includes("/") ? 1 : 0;
        return da - db || a.path.localeCompare(b.path);
      });
    const name = (docName || "presentation").replace(/\s+/g, "-");
    const head = [{ path: name + ".md", size: new TextEncoder().encode(app.source()).length, kind: "md" }];
    const key = themeSel.value || "";
    head.push({ path: (key || "theme") + ".css", size: -1, kind: "css" });
    const docs = (await vfs.listDocs())
      .sort((a, b) => (b.updated || 0) - (a.updated || 0))
      .map((d) => ({ id: d.id, name: d.name || "presentation", when: whenText(d.updated), current: d.id === doc.id }));
    let note = vfs.persistent
      ? t("Files live only in this browser (IndexedDB). Share links carry only the text and theme, not images or data files.")
      : t("This browser does not allow storage: files are kept only while this page is open.");
    // PRO (sliqtly.js): the files in the cloud, offered at the top
    const promo = !window.sliqtly ? null : window.sliqtly.user()
      ? { title: t("PRO is active"), text: t("Cloud storage and file sharing are coming here soon."), button: "" }
      : {
        title: t("Share images and data with PRO"),
        text: t("PRO keeps your decks and their files in the cloud. Share links then carry images, plus the CSV and JSON data behind your charts and tables."),
        button: t("Get PRO"),
      };
    if (!doc.persisted) note = t("This presentation is not saved yet: it saves when you change it. ") + note;
    app.setFileList(JSON.stringify({ doc: exportName(), files: head.concat(files), docs, note, ...(promo ? { promo } : {}) }));
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
    await saveDoc();
    const text = await file.text();
    beginDoc(text);
    docName = file.name.replace(/\.(md|markdown)$/i, "") || "presentation";
    app.setSource(text);
    dropThumbs();
    needsPaint = true;
    return;
  }
  if (kindOf(file.name, type) === "image") {
    const bytes = await file.arrayBuffer();
    const path = placeFor(file.name, type);
    const [w, h] = await imageSize(bytes, type);
    app.addImage("/" + path, asRangerBuffer(bytes.slice(0)), type || "image/png", w, h);
    await registerPicture("/" + path, bytes, type || "image/png");
    await keepFile({ path, type: type || "image/png", size: bytes.byteLength, data: new Blob([bytes], { type }) });
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
    presData = new Promise((ok, bad) => {
      const s = document.createElement("script");
      s.src = "./pres_data.js?v=" + BUILD;
      s.onload = () => ok(globalThis.PresData);
      s.onerror = () => { presData = null; bad(new Error("pres_data.js did not load")); };
      document.head.appendChild(s);
    });
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
  if (/\.xlsx$/i.test(file.name)) {
    const PresData = await loadPresData();
    const bytes = await file.arrayBuffer();
    const r = JSON.parse(PresData.xlsxSheets(asRangerBuffer(bytes.slice(0))));
    if (r.error) {
      toast(t("Could not read the workbook: ") + r.error);
      return true;
    }
    const used = r.sheets.filter((sh) => sh.csv.replace(/[,\s]/g, "") !== "");
    sheets = used.map((sh) => {
      const csv = tidyCsv(sh.csv);
      const name = used.length > 1 ? `${base}-${sh.name.replace(/[\\/:*?"<>|\s]+/g, "-")}.csv` : base + ".csv";
      return { name: sh.name, path: placeFor(name, "text/csv"), csv, text: csv };
    });
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
  importing = { sheets };
  if (!ask) {
    for (let i = 0; i < sheets.length; i++) await keepData(i);
    return true;
  }
  app.openImport(JSON.stringify({ name: file.name, sheets: sheets.map(({ name, path, csv }) => ({ name, path, csv })) }));
  needsPaint = true;
  return true;
}

async function keepData(i) {
  const sh = importing && importing.sheets[i];
  if (!sh) return;
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
    fileAdd.click();
  } else if (action === "promo") {
    document.getElementById("pro")?.click();
  } else if (action === "new") {
    await saveDoc();
    const text = t("# New presentation") + "\n\n" + t("Write here.") + "\n";
    beginDoc(text);
    docName = "new";
    app.setSource(text);
    app.showTab("md");
    dropThumbs();
  } else if (action === "open") {
    const f = (await docFiles()).find((x) => x.path === what);
    if (!f) return;
    const text = typeof f.data === "string" ? f.data : (isText(f.path, f.type) ? await f.data.text() : null);
    if (text == null) { toast(t("This file cannot be opened as text.")); return; }
    app.openFile(f.path, text);
  } else if (action === "del") {
    pending.delete(what);
    if (doc.persisted) await vfs.deleteFile(doc.id, what);
    if (app.openFilePath() === what) app.closeFile();
  } else if (action === "doc") {
    if (!(await openDoc(what))) toast(t("Presentation not found."));
  } else if (action === "deldoc") {
    if (what !== doc.id) await vfs.deleteDoc(what);
  }
  refreshFiles();
  needsPaint = true;
}

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
  const chrome = JSON.parse(app.chromeJson());
  window.__lastChrome = chrome;
  const cf = prepareDisplayList(gl, chrome, { dpr });
  grew = grewBy(cf.draw(null, null)) || grew;
  cf.dispose();
  if (layout.slides > 0) {
    const st = withTime(JSON.parse(app.stageJson()), clock);
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
  const barOnTop = canvasBar && app.toolbarOnTop();
  if (canvasBar && !barOnTop) paintBar();
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
  // the help panel, the share dialog, the toast
  const pj = app.panelsJson();
  if (pj) {
    const pn = JSON.parse(pj);
    pn.width = W;
    pn.height = H;
    const pf = prepareDisplayList(gl, pn, { dpr });
    if (grewBy(pf.draw(null, [0, 0, 1], { clear: false }))) dropThumbs();
    pf.dispose();
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
  if (barOnTop) paintBar();
  statusEl.textContent = app.statusText();
  playBtn.textContent = layout.playing && layout.mode === "edit" ? t("⏸ Pause") : t("▶ Play");
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
//   Ctrl+Space       the value popover at the caret, with the keyboard in it
let lastA11yRev = "";
let a11yTree = null;
const mirror = createA11yMirror(stageEl, {
  canvas,
  label: APP_NAME + t(". F6 moves between areas, Esc returns to the editor."),
  tabbable: "all",
  onActivate: (node) => {
    pressAtCentre(node, (x, y) => {
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
        if (!focusRegion(reg)) focusKeys(app.focusTarget());
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
    else if (reg === "bar") app.key("escape", false, false);
    afterInput();
    app.setA11yFocus("");
    focusKeys(app.focusTarget() === "stage" ? "stage" : "editor");
    needsPaint = true;
  }
});

// --- files the charts read -------------------------------------------------------
// A chart may take its rows from a file (`"data": {"url": "data/movies.json"}`,
// as the Vega-Lite examples do). The page fetches each one once: beside the
// page first, then — for a relative path — from the Vega example datasets.
const chartFiles = new Map();
let chartFilesRev = -1;
function fetchChartFiles(rev) {
  if (rev === chartFilesRev) return;
  chartFilesRev = rev;
  const wanted = (app.chartDataWanted() || "").split("\n").filter(Boolean);
  for (const url of wanted) {
    if (chartFiles.has(url)) continue;
    const tries = /^https?:/.test(url) ? [url] : ["./" + url.replace(/^\.?\//, ""), "https://cdn.jsdelivr.net/npm/vega-datasets@2/" + url.replace(/^\.?\//, "")];
    const got = (async () => {
      // the document's own copy, when it has one
      const mine = (await docFiles()).find((f) => f.path === bare(url));
      if (mine && typeof mine.data === "string") return mine.data;
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
        toast(t("Could not load the chart file: ") + url);
        return;
      }
      app.setChartData(url, text);
      dropThumbs();
      needsPaint = true;
      // kept with the document, so it opens without the network next time
      if (!/^https?:/.test(url)) {
        docFiles().then((have) => {
          if (!have.some((f) => f.path === bare(url))) keepFile({ path: bare(url), type: "text/plain", size: text.length, data: text }).catch(fail);
        });
      }
    });
  }
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
    } else if (r === "openbox") {
      // Open: a file from the computer, or a sample deck
      app.openOpen([...sampleSel.options].map((o) => o.value + "\t" + o.textContent.trim()).join("\n"));
      needsPaint = true;
    } else if (r.startsWith("showtab:")) {
      app.showTab(r.slice(8));
      needsPaint = true;
    } else if (r.startsWith("copy:")) {
      copyShare(r.slice(5)).catch(fail);
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
    } else if (r === "files-list") {
      refreshFiles();
    } else if (r.startsWith("files:")) {
      fileRequest(r.slice(6)).catch(fail);
    } else if (r.startsWith("chart-file:")) {
      const path = bare(r.slice(11));
      const text = app.chartFileBody();
      keepFile({ path, type: "application/json", size: text.length, data: text }).catch(fail);
      chartFiles.set(path, Promise.resolve(text));
      dropThumbs();
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
    if (!viewer && !byApp && was && app.isPlaying() && JSON.parse(app.layoutJson()).mode === "present") {
      app.endPresent();
      handleRequests();
    }
  }
  requestAnimationFrame(resize);
});

let docName = "presentation";

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
  toast(t("Rendering effects of ") + list.length + t(" slides for export…"));
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
    app.setSource(text);
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

fileAdd.addEventListener("change", async () => {
  const list = [...(fileAdd.files || [])];
  fileAdd.value = "";
  for (const f of list) await addDocFile(f);
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
function hashParams() {
  return new URLSearchParams(location.hash.replace(/^#/, ""));
}

let toastTimer = 0;
function toast(text) {
  app.toast(text);
  needsPaint = true;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { app.toast(""); needsPaint = true; }, 3200);
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

// Signed in to PRO: the deck is kept as its owner's in the cloud and a copy
// is shared under a short id, pictures and data files with it. The copy is
// read-only: the edit link opens it as a new deck of the reader's own.
// Resolves to the share's id; rejects with the reason it could not.
async function shareCloud() {
  const p = window.sliqtly;
  await saveDoc(true);
  const key = themeSel.value || "";
  // Firestore waits quietly when it cannot write (no database yet, rules
  // that refuse): a share that has not happened in 20 s has failed
  const timeout = new Promise((_, no) => setTimeout(() => no(Object.assign(new Error("timeout"), { code: "timeout" })), 20000));
  return Promise.race([timeout, p.share({
    deckId: doc.id, name: exportName(), md: app.source(), theme: key,
    css: key in editedCss ? editedCss[key] : null,
    files: (await docFiles()).map((f) => ({ path: f.path, type: f.type || "", data: f.data })),
  })]);
}

function cloudFailure(e) {
  console.warn("cloud sharing failed", e);
  const why = e?.code === "permission-denied" || e?.code === "storage/unauthorized" ? t("the cloud refused it (rules not set up?)")
    : e?.code === "timeout" ? t("no answer from the cloud (is Firestore created?)")
    : (e?.code || e?.message || String(e));
  return t("Cloud sharing failed: ") + why + ". " + t("Sharing the text in the link instead.");
}

function showShare(showUrl, editUrl, note) {
  app.openShare(showUrl, editUrl, note);
  window.__lastShare = editUrl;
  window.__lastShareShow = showUrl;
  needsPaint = true;
}

// The dialog opens at once with the text packed into the link; signed in to
// PRO, it says a short link is on its way and shows it when the cloud has it.
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
  const pictures = /\]\(media\//.test(text) ? t(" Attached images are not included in the link.") : "";
  const textNote = editUrl.length + t(" characters.") + pictures;
  if (!window.sliqtly?.user?.()) {
    showShare(showUrl, editUrl, textNote);
    return;
  }
  showShare(showUrl, editUrl, t("Creating a short link in the cloud…"));
  try {
    const id = await shareCloud();
    const short = location.origin + "/s/" + id;
    showShare(short, short + "?edit", t("A short link to a copy in the cloud, with its images and data. Only you can change the original."));
  } catch (e) {
    showShare(showUrl, editUrl, cloudFailure(e) + " " + textNote);
  }
}

// A copy button in the share dialog (drawn on the canvas): the browser copies.
let copiedTimer = 0;
async function copyShare(which) {
  const text = which === "show" ? window.__lastShareShow : window.__lastShare;
  let copied = false;
  try {
    await navigator.clipboard.writeText(text);
    copied = true;
  } catch (_) {
    const t = document.createElement("textarea");
    t.value = text;
    document.body.append(t);
    t.select();
    copied = !!(document.execCommand && document.execCommand("copy"));
    t.remove();
    focusKeys(app.focusTarget());
  }
  app.shareCopied(which, copied);
  needsPaint = true;
  clearTimeout(copiedTimer);
  copiedTimer = setTimeout(() => { app.shareCopied("", true); needsPaint = true; }, 1600);
}

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
    await saveDoc();
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
    app.setSource(text);
    dropThumbs();
    needsPaint = true;
    if (q.get("mode") === "show") enterViewer();
    return true;
  } catch (e) {
    toast(t("Could not read the link's contents."));
    console.warn(e);
    return false;
  }
}
window.addEventListener("hashchange", () => { if (location.hash !== lastHash) openFromHash(); });

// /s/{id}: a deck shared through PRO, read from the cloud. Shown as a
// presentation; with ?edit, opened as a new deck of the reader's own.
async function openFromShare() {
  const m = /^\/s\/([A-Za-z0-9]{6,32})\/?$/.exec(location.pathname);
  if (!m) return false;
  try {
    const shared = await (await pro()).loadShare(m[1]);
    if (!shared) {
      toast(t("This shared presentation was not found."));
      return false;
    }
    const editing = new URLSearchParams(location.search).has("edit");
    beginDoc(shared.md || "");
    if (shared.theme != null) {
      themeSel.value = shared.theme;
      app.setStyleSheet(shared.theme ? themeCss[shared.theme] || "" : "");
    }
    if (shared.css != null) {
      editedCss[themeSel.value || ""] = shared.css;
      app.setStyleSheet(shared.css);
    }
    for (const f of shared.files || []) {
      try {
        const res = await fetch(f.url);
        const data = isText(f.path, f.type) ? await res.text() : await res.blob();
        const rec = { doc: doc.id, path: f.path, type: f.type, size: f.size, data, updated: Date.now() };
        // the reader's copy keeps them: they are saved with it on its first change
        if (editing) pending.set(rec.path, rec);
        await useFile(rec);
      } catch (e) {
        console.warn("shared file not loaded: " + f.path, e);
      }
    }
    docName = shared.name || "shared";
    app.setSource(shared.md || "");
    dropThumbs();
    needsPaint = true;
    if (!editing) enterViewer();
    return true;
  } catch (e) {
    console.warn(e);
    toast(t("Could not open the shared presentation."));
    return false;
  }
}

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
    await saveDoc();
    const text = await textOf(s[1]);
    beginDoc(text);
    app.setSource(text);
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
  app.setA11yFocus("");
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
  if (ev.key === "F6") {
    ev.preventDefault();
    cycleRegion(ev.shiftKey);
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
      } else if (app.openImageAtCaret()) {
        // a picture's line opens the picture's settings
        closeHint();
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

// --- the help panel ---------------------------------------------------------------
// What the selected slide is made of — a highlight, a list, a formula — with
// how each is written and the theme's CSS rules that change how it looks,
// at the values the theme gives them now. Only what the slide has is
// listed. A property opens the theme at its line (added when the theme has
// none) with its value popover.
function toggleHelp(on) {
  app.setHelp(on ?? !app.helpIsOpen());
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

async function start() {
  // everything start-up reads is asked for at once
  const toolbarCss = viewer ? null : textOf("./toolbar.css");
  toolbarCss?.catch(() => {});
  const themesGot = THEMES.map((name) => textOf("./themes/" + name + ".css"));
  for (const p of themesGot) p.catch(() => {});
  // the chart editor's controls: the kit's theme, then the app's colours
  const [css, kit, chartCss] = await Promise.all([
    textOf("./pres.css"),
    textOf("./ui.css").catch(() => ""),
    textOf("./chart-editor.css").catch(() => ""),
  ]);
  app.setChartCss(kit + "\n" + chartCss);
  textOf("./hint.css").then((c) => app.setHintCss(kit + "\n" + chartCss + "\n" + c)).catch(() => {});
  textOf("./panels.css").then((c) => app.setPanelsCss(kit + "\n" + c)).catch(() => {});
  if (!viewer) {
    // the bar moves onto the canvas: the HTML one stays, hidden, as what it
    // presses (its buttons and selects keep every behaviour they had)
    app.setToolbarCss(kit + "\n" + (await toolbarCss));
    document.body.classList.add("canvas-bar");
    canvasBar = true;
    syncBarExtras();
    new MutationObserver(syncBarExtras).observe(document.getElementById("bar"),
      { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ["hidden", "data-canvas"] });
    app.useToolbar(true);
    requestAnimationFrame(resize);
  }
  const r = stageEl.getBoundingClientRect();
  app.init(css, Math.max(320, r.width), Math.max(240, r.height));
  app.setCoarse(isCoarse());
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

  for (const [i, name] of THEMES.entries()) {
    try {
      themeCss[name] = await themesGot[i];
      app.addTemplate(name, themeCss[name]);
    } catch (_) { /* one template fewer */ }
  }

  const q = new URLSearchParams(location.search);
  const theme = q.has("theme") ? q.get("theme") : "aurora";
  themeSel.value = theme;
  app.setStyleSheet(theme ? themeCss[theme] || "" : "");
  if (!viewer) vfs = await openVfs();
  if (!(await openFromShare()) && !(await openFromHash())) {
    const want = q.get("sample");
    // no sample asked for: the deck worked on last, if this browser kept one
    let last = null;
    try { last = localStorage.getItem("evgp.doc"); } catch (_) { /* none */ }
    if (want || !last || !(await openDoc(last))) {
      const sample = SAMPLES[want] || HIDDEN_SAMPLES[want] ? want : "talous";
      if (SAMPLES[sample]) sampleSel.value = sample;
      await openSample(sample);
    }
  }

  // A narrow window gets the slides without the editor (PresApp.isCompact,
  // decided on every layout, so it follows the window); on a touch screen
  // the hidden text field is not focused, so no keyboard comes up.
  if (viewer || isCoarse()) keys.blur();

  // the loader has its moment: at least one turn of the logo (0.6 s from
  // the page's start), then it fades as the editor appears
  await new Promise((r) => setTimeout(r, Math.max(0, 600 - performance.now())));
  hintEl.classList.add("done");
  setTimeout(() => hintEl.remove(), 260);
  document.body.classList.remove("booting");
  if (!viewer && !isCoarse()) focusKeys("editor");
  window.__pageStarted = true;
  requestAnimationFrame(frame);
}

start().catch(fail);
