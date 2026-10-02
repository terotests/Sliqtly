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
import { createLiveSheets } from "./sheets-live.js";

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
// The sample decks in the interface's language: samples/<key>.md is Finnish,
// samples/<key>.en.md English (any other language gets the English ones).
const sample = (key, en, fi) => lang === "fi" ? [fi, `./samples/${key}.md`] : [en, `./samples/${key}.en.md`];
const SAMPLES = {
  talous: sample("talous", "Finance: take charge of your money", "Talous: oma talous haltuun"),
  ymparisto: sample("ymparisto", "Environment: your carbon footprint", "Ympäristö: hiilijalanjälki"),
  urheilu: sample("urheilu", "Sports: a 5 km running course", "Urheilu: 5 km juoksukoulu"),
  kulttuuri: sample("kulttuuri", "Culture: decades of music", "Kulttuuri: musiikin vuosikymmenet"),
  ohjelmointi: sample("ohjelmointi", "Programming: version control", "Ohjelmointi: versionhallinta"),
  matematiikka: sample("matematiikka", "Mathematics: formulas on slides", "Matematiikka: kaavat kalvoilla"),
  vegalite: sample("vegalite", "Vega-Lite: chart types", "Vega-Lite: kaaviotyypit"),
  raportti: sample("raportti", "Report: header, footer, page numbers", "Raportti: ylä- ja alaosa, sivunumerot"),
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

// A picture pasted or dropped on the canvas opens the image window
// (PresChartEditor's "paste" mode): the part to keep (CropCtl), and whether it
// goes on the slide or behind this slide or every slide. Until Add is pressed
// it is only registered for drawing, under a path of its own; nothing is kept.
let pasteCount = 0;
let pasting = null;
async function addPictureFile(file) {
  const type = file.type || "image/png";
  const bytes = await file.arrayBuffer();
  const [w, h] = await imageSize(bytes, type);
  const alt = file.name && file.name !== "image.png" ? file.name.replace(/\.[^.]+$/, "") : "image";
  if (w > 0 && h > 0) {
    dropPasting();
    pasteCount += 1;
    const preview = `/__paste/${Date.now().toString(36)}-${pasteCount}`;
    await registerPicture(preview, bytes, type);
    pasting = { bytes, type, w, h, preview };
    if (app.openPaste(preview, w, h, alt, storageNote())) {
      needsPaint = true;
      return;
    }
    dropPasting();
  }
  // no size to crop by (or presenting): straight onto the slide, as before
  const rel = await keepPicture(bytes, type, w, h);
  app.insertPicture(rel, alt);
  dropThumbs();
  afterInput();
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
  let { bytes, type, w, h } = p;
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
        w = cw;
        h = ch;
      }
    }
  }
  const rel = await keepPicture(bytes, type, w, h);
  app.placePicture(rel, plan.alt || "image", plan.to);
  dropThumbs();
  afterInput();
}

// A new picture of the deck: registered for the slides and kept in this
// browser; signed in to PRO, the deck's cloud save takes it with the deck.
async function keepPicture(bytes, type, w, h) {
  const ext = (type.split("/")[1] || "png").replace("jpeg", "jpg").replace("svg+xml", "svg");
  pasteCount += 1;
  const rel = `media/liitetty-${Date.now().toString(36)}-${pasteCount}.${ext}`;
  app.addImage("/" + rel, asRangerBuffer(bytes.slice(0)), type, w, h);
  await registerPicture("/" + rel, bytes, type);
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
const doc = { id: newId(), persisted: false, created: Date.now(), openedText: "" };
// PRO: the share the deck lives in (cloud), its text as last written or read
// there (cloudMd), the files as sent (cloudStamps: path → stamp), and what
// was last sent (cloudSig). cloudHalt: changed elsewhere, not written over.
Object.assign(doc, { cloud: null, cloudMd: null, cloudStamps: new Map(), cloudSig: "", cloudHalt: false });
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
  Object.assign(doc, { cloud: null, cloudMd: null, cloudStamps: new Map(), cloudSig: "", cloudHalt: false });
  pending.clear();
  savedText = null;
  savedCss = null;
  for (const k of Object.keys(editedCss)) delete editedCss[k];
  chartFiles.clear();
  chartFilesRev = -1;
  liveFromShare = false;
  liveNoted = false;
  liveCopies.clear();
  copyNoted = false;
  privateNoted = false;
  app.clearChartData();
  showLiveButton();
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
      chartFiles.set(sh.path, Promise.resolve(sh.csv));
      app.setChartData(sh.path, sh.csv);
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
// never stored: `data/<book>-<Sheet>.csv` (or `data/<book>.csv` for a
// one-sheet book) names a sheet of `data/<book>.xlsx`.
function workbookSheets(xlsxPath, PresData, raw) {
  const r = JSON.parse(PresData.xlsxSheets(asRangerBuffer(raw.slice(0))));
  if (r.error) return { error: r.error, sheets: [] };
  const used = r.sheets.filter((sh) => sh.csv.replace(/[,\s]/g, "") !== "");
  const base = bare(xlsxPath).replace(/\.xlsx$/i, "");
  return {
    error: "",
    sheets: used.map((sh) => {
      const csv = tidyCsv(sh.csv);
      const path = used.length > 1 ? `${base}-${sh.name.replace(/[\\/:*?"<>|\s]+/g, "-")}.csv` : base + ".csv";
      return { name: sh.name, path, csv, text: csv };
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
      for (const sh of workbookSheets(f.path, PresData, await blob.arrayBuffer()).sheets) out.set(sh.path, sh.csv);
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
// for check:web: what the document keeps as files
window.__docFiles = () => docFiles().then((fs) => fs.map((f) => f.path));

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
    await vfs.putDoc({ id: doc.id, name: exportName(), md, theme: key, css, created: doc.created, updated: Date.now(), cloud: doc.cloud, cloudMd: doc.cloudMd });
    if (!doc.persisted) {
      doc.persisted = true;
      for (const f of pending.values()) await vfs.putFile({ ...f, doc: doc.id });
      pending.clear();
      plainAddress();
    }
    savedText = md;
    savedCss = css;
    savedTheme = key;
    try { localStorage.setItem("evgp.doc", doc.id); } catch (_) { /* the next start opens a sample */ }
  })();
  try { await saving; } finally { saving = null; }
  refreshFiles();
  cloudSoon();
}

// Before another deck is opened in its place: this one kept, and a PRO
// deck's last changes written to its share (they would wait for the next
// visit of this deck otherwise). The cloud gets a while, not forever.
async function leaveDoc() {
  await saveDoc();
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
  app.setSource(text);
  app.showTab("md");
  dropThumbs();
  await saveDoc(true);
  if (cloudReady()) await cloudSync().catch(cloudTrouble);
  refreshFiles();
  needsPaint = true;
}
// File → Duplicate: a new deck from this one, its Markdown, theme CSS and
// files copied, named "<name> (copy)". This one is saved first; the copy is
// kept at once under an id of its own (a PRO deck gets its own share).
async function duplicateDeck() {
  await leaveDoc();
  const key = themeSel.value || "";
  const css = key in editedCss ? editedCss[key] : null;
  const files = await docFiles();
  const name = exportName() + " " + t("(copy)");
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
  app.setSource(text);
  dropThumbs();
  await saveDoc(true);
  if (cloudReady()) await cloudSync().catch(cloudTrouble);
  refreshFiles();
  needsPaint = true;
  toast(t("Duplicated as ") + name);
}
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
  return "## " + t("Chart") + "\n\n```vega-lite\n{\n  \"data\": {\"values\": [\n" + rows + "\n  ]},\n  \"mark\": \"bar\",\n  \"width\": 560,\n"
    + `  "encoding": {\n    "x": {"field": "${t("quarter")}", "type": "nominal", "axis": {"labelAngle": 0}},\n    "y": {"field": "${t("sales")}", "type": "quantitative"}\n  }\n}\n` + "```\n";
}

// A deck opened from a link (#md=…, /s/{id}?edit, ?sample=…) is the reader's
// own once it is saved: the address loses the link, so a reload opens the
// saved deck, pictures and all, and not the link's text again.
function plainAddress() {
  // a PRO deck: its own address, which a reload opens from the cloud
  if (doc.cloud) {
    history.replaceState(null, "", "/s/" + doc.cloud + "?edit");
    lastHash = "";
    return;
  }
  const q = new URLSearchParams(location.search);
  const shared = /^\/s\//.test(location.pathname);
  if (!location.hash && !shared && !q.has("sample") && !q.has("deck")) return;
  q.delete("sample");
  q.delete("edit");
  q.delete("deck");
  q.delete("from");
  const search = q.toString();
  history.replaceState(null, "", (shared ? "/" : location.pathname) + (search ? "?" + search : ""));
  lastHash = "";
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
    if (f.path.startsWith("data/live/")) liveCopies.set(f.path, f.data);
    chartFiles.set(f.path, Promise.resolve(f.data));
    app.setChartData(f.path, f.data);
  }
}

async function openDoc(id) {
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

// File → Recent: the decks of this browser edited last, the open one left
// out (a PRO deck is among them: the cloud save keeps it here as well).
let recentSynced = "";
async function refreshRecent() {
  if (!vfs || viewer) return;
  const rows = (await vfs.listDocs())
    .filter((d) => d.id !== doc.id)
    .sort((a, b) => (b.updated || 0) - (a.updated || 0))
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
      ? { title: t("PRO is active"), text: t("Your presentations and their pictures and data are saved in the cloud as you work."), button: "" }
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
    await leaveDoc();
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
  let text;
  try {
    text = await readLive(url, true);
  } catch (e) {
    if (e?.code === "auth/popup-blocked") toast(t("The browser blocked Google's sign-in window: allow pop-ups for this site and paste the link again."));
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
      chartFiles.set(one.path, Promise.resolve(one.text));
      app.setChartData(one.path, one.text);
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
    // asked first: an accidental press is cancelled and the deck stays
    const rows = [...themeSel.options].map((o) => o.value + "\t" + o.textContent.trim()).join("\n");
    app.openNewDeck(rows, themeSel.value || "", "");
  } else if (action === "duplicate") {
    makingDeck = duplicateDeck().catch((e) => toast(t("Duplicating failed: ") + (e.message || e))).finally(() => { makingDeck = null; });
    await makingDeck;
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
    app.openFile(f.path, text);
  } else if (action === "del") {
    pending.delete(what);
    if (doc.persisted) await vfs.deleteFile(doc.id, what);
    if (app.openFilePath() === what) app.closeFile();
    cloudSoon();
  } else if (action === "doc") {
    if (!(await openDoc(what))) toast(t("Presentation not found."));
  } else if (action === "deldoc") {
    if (what !== doc.id) await vfs.deleteDoc(what);
  }
  refreshFiles();
  needsPaint = true;
}

window.__fileRequest = (r) => fileRequest(r);

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
// the slide lists of thumbnails cut by the strip's edge, as JSON text
let thumbDocs = new Map();
let thumbDocsRev = -1;
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
  // that does not stand out from the picture under it (WCAG 4.5:1, 3:1 for
  // large text) gets a thin outline in black or white. The chrome has no
  // pictures under its text and is drawn without it.
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
    const sf = prepareDisplayList(gl, st, { dpr, images: pictures, contrastGuard: true });
    const stageStats = sf.draw(null, [layout.stage[0], layout.stage[1], layout.stage[2]], { clear: false });
    grew = grewBy(stageStats) || grew;
    sf.dispose();
    // Only while editing: not to an audience, and not mid-animation, where a
    // fading line is briefly faint by design.
    if (layout.mode !== "present" && !viewer && !app.isPlaying()) {
      // Runs too faint on their slide: marked in the editors' gutters, not
      // laid over the slide. A change shows on the next paint.
      try {
        if (app.setContrast(JSON.stringify((stageStats && stageStats.lowContrast) || []))) needsPaint = true;
      } catch (e) { console.warn("contrast notes", e); }
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
      const doc = withTime(JSON.parse(text), 2.0);
      doc.width = W;
      doc.height = H;
      const [cx, cy, cw, ch] = layout.strip;
      const none = [0, 0, 0, 0];
      doc.list.cmds.unshift({ k: 4, x: (cx - x) / s, y: (cy - y) / s, w: cw / s, h: ch / s, c: none });
      doc.list.cmds.push({ k: 5, x: 0, y: 0, w: 0, h: 0, c: none });
      const cf = prepareDisplayList(gl, doc, { dpr, images: pictures, contrastGuard: true });
      if (grewBy(cf.draw(null, [x, y, s], { clear: false }))) thumbsGrew = true;
      cf.dispose();
      continue;
    }
    let f = thumbs.get(i);
    const fresh = !f;
    if (fresh) {
      const doc = withTime(JSON.parse(app.slideJson(i)), 2.0);
      doc.width = W;
      doc.height = H;
      f = prepareDisplayList(gl, doc, { dpr, images: pictures, contrastGuard: true });
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
    // with the pictures: the image window shows the one being added
    const ce = prepareDisplayList(gl, cj, { dpr, images: pictures });
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
// signed in or out: the live data is looked at again
window.addEventListener("sliqtly:user", () => {
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
async function keepLiveCopy(url, text) {
  if (viewer) return;
  const path = copyPath(url);
  if (liveCopies.get(path) === text) return;
  liveCopies.set(path, text);
  const have = (await docFiles()).find((f) => f.path === path);
  if (have && have.data === text) return;
  await keepFile({ path, type: "text/csv", size: text.length, data: text });
}
async function liveCopy(url) {
  const path = copyPath(url);
  if (liveCopies.has(path)) return liveCopies.get(path);
  const have = (await docFiles()).find((f) => f.path === path);
  return have && typeof have.data === "string" ? have.data : null;
}
// Google's permission window opens only from a press, and a browser (Safari
// on a phone) counts a press as spent once the page has waited on the
// network. Then a small card asks for one more press, and that press opens
// the window at once (sliqtly.js askSheets). → true when a token came.
let tapping = null;
function googleTap() {
  tapping ??= new Promise((ok) => {
    const box = document.createElement("div");
    box.id = "gTap";
    box.setAttribute("role", "dialog");
    box.setAttribute("aria-label", t("Google Sheets"));
    const text = document.createElement("p");
    text.textContent = t("This Google Sheet is private. Google asks once whether Sliqtly may read the sheets you pick.");
    const go = document.createElement("button");
    go.className = "primary";
    go.textContent = t("Continue with Google");
    const no = document.createElement("button");
    no.textContent = t("Cancel");
    const done = (v) => { box.remove(); tapping = null; ok(v); };
    go.addEventListener("click", () => {
      // no await before this call: the window opens within the press
      window.sliqtly.askSheets().then((tok) => done(!!tok), () => done(false));
    });
    no.addEventListener("click", () => done(false));
    box.append(text, go, no);
    document.body.appendChild(box);
    go.focus();
  });
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
  const urls = [...chartFiles.keys()].filter(isLive);
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
  })();
  return refreshing;
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
        toast(t("Could not load the chart file: ") + url);
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

// for scripts/check-web.mjs: the requests run now, a picture's pixel size
window.__handleRequests = () => handleRequests();
window.__pictureSize = (p) => {
  const img = pictures.get(p);
  return img ? [img.naturalWidth, img.naturalHeight] : [0, 0];
};

function handleRequests() {
  for (;;) {
    const r = app.takeRequest();
    if (!r) break;
    if (r === "fullscreen") {
      document.body.classList.add("presenting");
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
    } else if (r === "newdeck-create") {
      const plan = JSON.parse(app.newDeckPlan());
      // the picker now, while the press still counts as one; the file goes
      // into the new deck once it is made
      if (plan.data === "file") {
        addAsks = true;
        fileAdd.click();
      }
      makingDeck = newDeck(plan).catch(fail).finally(() => { makingDeck = null; });
    } else if (r === "picture-place") {
      placePasted().catch(fail);
    } else if (r === "picture-cancel") {
      dropPasting();
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

// Signed in to PRO, a deck lives in the cloud as a share (web/sliqtly.js):
// made on its first save, written again a moment after every change, and
// opened from there at /s/{id}?edit, the address the editor then shows. The
// same share is what Share links to and what an assistant edits (mcp/).
function stampOf(f) {
  return (f.size ?? "") + ":" + (f.updated ?? "");
}
function cloudReady() {
  return !!(vfs && !viewer && doc.persisted && window.sliqtly?.user?.());
}
let cloudTimer = 0;
function cloudSoon() {
  if (!cloudReady() || doc.cloudHalt) return;
  clearTimeout(cloudTimer);
  cloudTimer = setTimeout(() => { cloudSync().catch(cloudTrouble); }, 2000);
}
let cloudBusy = null;
let cloudWarned = false;
function cloudTrouble(e) {
  console.warn("cloud save failed", e);
  if (cloudWarned) return;
  cloudWarned = true;
  toast(t("Saving to the cloud failed: ") + (e?.code || e?.message || String(e)) + ". " + t("The presentation is kept in this browser."));
}
// The deck as the cloud keeps it, and a signature of it.
async function cloudDeck() {
  const key = themeSel.value || "";
  const deck = {
    deckId: doc.id, name: exportName(), md: app.source(), theme: key,
    css: key in editedCss ? editedCss[key] : null,
    files: (await docFiles()).map((f) => ({ path: f.path, type: f.type || "", data: f.data, stamp: stampOf(f) })),
  };
  return { deck, sig: JSON.stringify([deck.name, deck.md, deck.theme, deck.css, deck.files.map((f) => f.path + "=" + f.stamp)]) };
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
  cloudBusy = (async () => {
    let id = doc.cloud;
    if (!id) {
      id = await p.share(deck);
    } else {
      try {
        await p.saveShare(id, deck, { md: doc.cloudMd, stamps: doc.cloudStamps });
      } catch (e) {
        if (e?.code !== "changed-elsewhere") throw e;
        if (doc.id === which) doc.cloudHalt = true;
        toast(t("This presentation was changed elsewhere (by an assistant?). Your changes here are not saved to the cloud: reload to get the newer version."));
        return id;
      }
    }
    if (doc.id !== which) return id; // another deck was opened meanwhile
    doc.cloud = id;
    doc.cloudMd = deck.md;
    doc.cloudSig = sig;
    doc.cloudStamps = new Map(deck.files.map((f) => [f.path, f.stamp]));
    cloudWarned = false;
    await vfs.putDoc({ ...(await vfs.getDoc(doc.id)), cloud: id, cloudMd: deck.md });
    plainAddress();
    return id;
  })();
  try { return await cloudBusy; } finally { cloudBusy = null; }
}
window.addEventListener("sliqtly:user", () => cloudSoon());

// Opens the signed-in owner's deck from its share (/s/{id}?edit): the cloud
// has the latest, an assistant's changes included. Kept in this browser
// under the id it had here, or a new one. False when it is not theirs.
async function openOwnCloud(id) {
  const p = await pro();
  const who = await Promise.race([p.signedIn(), new Promise((ok) => setTimeout(() => ok(null), 8000))]);
  if (!who || !vfs) return false;
  const shared = await p.loadShare(id);
  if (!shared || shared.owner !== who.uid) return false;
  const local = (await vfs.listDocs()).find((d) => d.cloud === id);
  // changes made here that the cloud does not have yet, and nobody changed
  // it since: this browser's copy is the newer, and goes up on the next save
  if (local && local.md !== local.cloudMd && shared.md === local.cloudMd) return openDoc(local.id);
  await leaveDoc();
  beginDoc(shared.md || "");
  doc.id = local?.id || newId();
  if (local) await vfs.deleteDoc(local.id); // the cloud's files replace this browser's
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
  for (const f of shared.files || []) {
    try {
      const res = await fetch(f.url);
      if (!res.ok) throw new Error("HTTP " + res.status);
      const data = isText(f.path, f.type) ? await res.text() : await res.blob();
      const rec = { doc: doc.id, path: f.path, type: f.type, size: f.size, data, updated: Date.now() };
      pending.set(rec.path, rec);
      await useFile(rec);
    } catch (e) {
      console.warn("cloud file not loaded: " + f.path, e);
      missing.push(f.path);
    }
  }
  if (missing.length) toast(t("Some pictures or data files of this presentation could not be loaded: ") + missing.join(", "));
  docName = shared.name || "presentation";
  app.setSource(shared.md || "");
  doc.cloud = id;
  doc.cloudMd = shared.md || "";
  // the files that did come are what the share has; one that did not is
  // not sent back, so the share keeps it
  doc.cloudStamps = new Map([...pending.values()].map((f) => [f.path, stampOf(f)]));
  await saveDoc(true);
  doc.cloudSig = (await cloudDeck()).sig;
  plainAddress();
  dropThumbs();
  needsPaint = true;
  return true;
}

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
  // the page's own address, also when it is at /s/{id}?edit
  const base = new URL(".", document.baseURI).href;
  const editUrl = base + "#" + q.toString();
  q.set("mode", "show");
  const showUrl = base + "#" + q.toString();
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

// Edit in Claude / ChatGPT: the assistant opens with a prompt that names the
// deck, and edits it through the Sliqtly connector (mcp/): get_presentation
// reads a share, update_presentation saves it when the assistant is signed
// in as the share's owner (or holds its edit key). So the deck handed over
// is a share: the one this page was opened from when it is the reader's own
// and unchanged, else a fresh share of the deck (signed in). Signed out, the
// Markdown goes in the prompt for create_presentation. The changes land in
// the share, not in this page's deck: /s/{id}?edit opens them here again.
let originShare = null;
// the links in the prompt: always the site's own address, also when the
// page was opened at sliqtly.web.app or on a local server
const SITE = "https://sliqtly.com";
const AI = {
  claude: (q) => "https://claude.ai/new?q=" + encodeURIComponent(q),
  chatgpt: (q) => "https://chatgpt.com/?q=" + encodeURIComponent(q),
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
      prompt = t("Edit my Sliqtly presentation {id} ({link}) with the Sliqtly connector. Load it with get_presentation (deck_id {id}), summarize it briefly and ask what to change. Save each change with update_presentation (deck_id {id}). If saving is refused, make a new presentation with create_presentation instead. After saving, give me the link {edit} to open it in the editor.")
        .replaceAll("{id}", id).replaceAll("{link}", SITE + "/s/" + id).replaceAll("{edit}", SITE + "/s/" + id + "?edit");
    } else {
      prompt = t("Make this Markdown a Sliqtly presentation with create_presentation from the Sliqtly connector (theme {theme}), give me its link and ask what to change. Save later changes with update_presentation.")
        .replaceAll("{theme}", themeSel.value || "-") + "\n\n```markdown\n" + text + "\n```";
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
document.getElementById("aiChatgpt").addEventListener("click", () => { editInAI("chatgpt"); });

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
  // the bar stays while its menu is open
  idleTimer = setTimeout(() => { if (vMenu.hidden) document.body.classList.add("idle"); }, 2500);
}
// Esc leaves full screen (the browser does that), never the presentation:
// there is no editor to go back to.
window.addEventListener("keydown", (ev) => {
  if (viewer && ev.key === "Escape") {
    ev.stopImmediatePropagation();
    if (!vMenu.hidden) {
      toggleViewMenu(false);
      vMore.focus();
    }
  }
}, true);
for (const ev of ["pointermove", "pointerdown", "keydown"]) {
  window.addEventListener(ev, () => { if (viewer) wakeViewer(); }, { passive: true });
}
document.getElementById("vPrev").addEventListener("click", () => { app.prev(); afterInput(); });
document.getElementById("vNext").addEventListener("click", () => { app.next(); afterInput(); });
document.getElementById("vData").addEventListener("click", () => refreshLiveData());
document.getElementById("vFull").addEventListener("click", () => {
  if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
  else if (document.documentElement.requestFullscreen) document.documentElement.requestFullscreen().catch(() => {});
});

// The … menu: the deck as PDF, PPTX or Markdown (the editor's exports), a new
// deck of the reader's own based on this one, and, for the signed-in owner of
// a cloud share, Edit, which opens their own deck in the editor.
// Embedded in an assistant's preview (mcp/src/preview.html writes the page as
// srcdoc, with <meta name="sliqtly-link">) the page has no address and its
// sandbox allows no downloads or windows: every item opens sliqtly.com in a
// new tab through the preview (window.__sliqtlyOpenLink, the host's
// ui/open-link), exports with ?export=pdf|pptx|md, which the site runs on load.
const vMenu = document.getElementById("vMenu");
const vMore = document.getElementById("vMore");
const vExportSub = document.getElementById("vExportSub");
const vExport = document.getElementById("vExport");
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
    vExportSub.hidden = true;
    vExport.setAttribute("aria-expanded", "false");
    return;
  }
  document.getElementById("vEdit").hidden = !ownsShare();
  wakeViewer();
  vMenu.querySelector("button:not([hidden])").focus();
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
  if (viewShare) return siteLink(siteUrl("s/" + viewShare.id + "?export=" + kind));
  const q = hashParams();
  q.set("mode", "show");
  q.set("export", kind);
  siteLink(siteUrl("").replace(/#.*$/, "") + "#" + q.toString());
}
const EXPORTS = { pdf: () => exportPdf(), pptx: () => exportPptx(), md: () => exportMd() };
async function exportMd() {
  window.__lastDownload = deliver(new TextEncoder().encode(app.source()), exportName() + ".md", "text/markdown");
}
window.__viewMenu = { toggle: toggleViewMenu, ownsShare, share: () => viewShare };
vMore.addEventListener("click", () => toggleViewMenu(vMenu.hidden));
vExport.addEventListener("click", () => {
  vExportSub.hidden = !vExportSub.hidden;
  vExport.setAttribute("aria-expanded", String(!vExportSub.hidden));
  if (!vExportSub.hidden) vExportSub.querySelector("button").focus();
});
vMenu.addEventListener("click", (ev) => {
  const act = ev.target.closest("[data-act]")?.dataset.act;
  if (!act) return;
  toggleViewMenu(false);
  if (EXPORTS[act]) {
    if (framed) exportOnSite(act);
    else EXPORTS[act]().catch(fail);
  } else if (act === "new") createFromViewed();
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
// #share={id}: the same presentation where the page is not at its own
// address — the preview an AI assistant shows (mcp/src/preview.html) runs
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
  const m = /^\/s\/([A-Za-z0-9]{6,32})\/?$/.exec(location.pathname);
  const own = m || hashShare() ? null : ownDeck();
  const id = m ? m[1] : hashShare() || own?.from;
  if (!id) return false;
  try {
    const shared = await (await pro()).loadShare(id);
    if (!shared) {
      toast(t("This shared presentation was not found."));
      return false;
    }
    const editing = (!!m && new URLSearchParams(location.search).has("edit")) || !!own;
    if (editing) originShare = { id, owner: shared.owner || "", md: shared.md || "" };
    beginDoc(shared.md || "");
    liveFromShare = !editing;
    if (own) doc.id = own.deck;
    else if (!editing) viewShare = { id, owner: shared.owner, deck: shared.deck };
    if (shared.theme != null) {
      themeSel.value = shared.theme;
      app.setStyleSheet(shared.theme ? themeCss[shared.theme] || "" : "");
    }
    if (shared.css != null) {
      editedCss[themeSel.value || ""] = shared.css;
      app.setStyleSheet(shared.css);
    }
    // the files come from Storage by fetch(), which the bucket must allow
    // for this origin (storage.cors.json); a picture that does not come is
    // said, not left out in silence
    const missing = [];
    for (const f of shared.files || []) {
      try {
        const res = await fetch(f.url);
        if (!res.ok) throw new Error("HTTP " + res.status);
        const data = isText(f.path, f.type) ? await res.text() : await res.blob();
        const rec = { doc: doc.id, path: f.path, type: f.type, size: f.size, data, updated: Date.now() };
        // the reader's copy keeps them: they are saved with it on its first change
        if (editing) pending.set(rec.path, rec);
        await useFile(rec);
      } catch (e) {
        console.warn("shared file not loaded: " + f.path, e);
        missing.push(f.path);
      }
    }
    if (missing.length) toast(t("Some pictures or data files of this presentation could not be loaded: ") + missing.join(", "));
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
    await leaveDoc();
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
    if ((ev.key === "r" || ev.key === "R") && !mod && !ev.altKey) {
      ev.preventDefault();
      refreshLiveData();
      return;
    }
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
  // the viewer's buttons and menu keep their own keys (Enter, Tab, arrows)
  if (ev.target.closest?.("#viewBar")) return;
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

// --- the pointer ----------------------------------------------------------------------
function at(ev) {
  const r = canvas.getBoundingClientRect();
  return [ev.clientX - r.left, ev.clientY - r.top];
}

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

canvas.addEventListener("pointerdown", (ev) => {
  const [x, y] = at(ev);
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
  const now = performance.now();
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
  // a finger wobbles: it has to travel further than a mouse before a tap
  // on the stage becomes a drag
  app.setDragSlop(finger ? 16 : 6);
  app.setTouch(finger);
  const where = app.pointerDown(x, y, ev.shiftKey, Math.min(clicks, 3));
  ev.preventDefault();
  if (where === "editor" || where === "sep" || where === "scrub" || where === "stage" || where === "chart" || where === "hint" || where === "thumb") {
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

function endPointer(ev) {
  touches.delete(ev.pointerId);
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
  if (!viewer && !hashShare()) vfs = await openVfs();
  const own = ownDeck();
  // /s/{id}?edit (or an older ?deck=…&from={id}) of the signed-in owner's
  // own deck: opened from the cloud, where it lives
  const editId = /^\/s\/([A-Za-z0-9]{6,32})\/?$/.exec(location.pathname)?.[1] || own?.from;
  const editing = !!own || (!!editId && q.has("edit"));
  if (editing && editId && (await openOwnCloud(editId).catch((e) => { console.warn(e); return false; }))) { /* opened */ }
  else if (own && vfs && (await ownIsNewer(own)) && (await openDoc(own.deck))) plainAddress();
  else if (!(await openFromShare()) && !(await openFromHash())) {
    const want = q.get("sample");
    // no sample asked for: the deck worked on last, if this browser kept one,
    // from the cloud when it lives there
    let last = null;
    try { last = localStorage.getItem("evgp.doc"); } catch (_) { /* none */ }
    const lastCloud = !want && last && vfs ? (await vfs.getDoc(last))?.cloud : null;
    if (lastCloud && (await openOwnCloud(lastCloud).catch(() => false))) { /* opened */ }
    else if (want || !last || !(await openDoc(last))) {
      const sample = SAMPLES[want] || HIDDEN_SAMPLES[want] ? want : "talous";
      if (SAMPLES[sample]) sampleSel.value = sample;
      await openSample(sample);
    }
  }
  refreshRecent().catch(() => {});

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
  // ?export=pdf|pptx|md (or in the #…): an export asked for from the
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
