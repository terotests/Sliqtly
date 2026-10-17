// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The Spaces page. The app (src/SpacesApp.rgr, compiled into spaces_app.js)
// builds one EVG tree; this file paints its display list on a WebGL canvas,
// sends it the pointer, the wheel and the keys, mirrors its accessible tree
// into DOM over the canvas, and does what a tree cannot: read a file or
// pasted cells for an import, read a picture for the assistant (made
// smaller first), put text on the clipboard, and save a download. A double-click
// opens the event under it; Backspace and Delete outside a text field go to
// the app, which deletes the selected event. It keeps the summary's clock
// and weather current.
//
// The Warden page is the same app on a server: its index.html names the
// server's path in <meta name="app-server">. The app then keeps an outbox
// of calls; `pump` sends them one at a time as POST <path>api/<op> with the
// sign-in cookie and hands each reply back (`answer`, or `failed` with the
// server's error). Signed out, the page goes to the server's sign-in page.
// Text fields are edited through EVG's text-input bridge.
//
// Every few seconds, while the page is in view, it asks the server whether
// the space changed elsewhere (`app.poll`), so what an assistant adds
// through Warden's MCP server shows up without a reload. The page names
// itself in X-Warden-Client, so its own changes are not news to it.
//
// The tab is in the address (#calendar): Back and Forward move between the
// tabs, and a link or a reload opens the same one.

import { renderDisplayList } from "./gl/evg-webgl.js";
import { createA11yMirror, pressAtCentre } from "./gl/evg-a11y.js";
import { installCanvasMeasurer } from "./gl/evg-measure.js";
import { createTextInputBridge } from "./gl/evg-textinput.js";

const M = globalThis.SpacesModule;
installCanvasMeasurer([M]);

const css = await (await fetch("./spaces.css?v=__BUILD__")).text();
const app = new M.SpacesApp();
const now = new Date();
const serverMeta = document.querySelector('meta[name="app-server"]');
const SERVER = serverMeta ? serverMeta.content : "";
const REMEMBER = "warden.space";
// the calendar's switches for windows to get things and payment days, per
// person signed in on this browser
const CAL_SHOWS = "warden.calShows.";
let calShowsFor = "";
function calShowsKey() { return app.account && app.account.uid ? CAL_SHOWS + app.account.uid : ""; }
function loadCalShows() {
  const key = calShowsKey();
  if (!key || key === calShowsFor) return;
  calShowsFor = key;
  let v = null;
  try { v = JSON.parse(localStorage.getItem(key) || "null"); } catch { /* storage off */ }
  if (!v) return;
  app.showGets = !!v.gets;
  app.showPays = !!v.pays;
  app.fillBoard();
  app.rebuild();
}
function saveCalShows() {
  const key = calShowsKey();
  if (!key) return;
  try { localStorage.setItem(key, JSON.stringify({ gets: app.showGets, pays: app.showPays })); } catch { /* storage off */ }
}
app.server = !!SERVER;
app.lang = (navigator.language || "fi").toLowerCase().startsWith("fi") ? "fi" : "en";
app.init(css);
app.useClock(now.getFullYear(), now.getMonth() + 1, now.getDate(), now.getHours() * 60 + now.getMinutes(), -now.getTimezoneOffset());

const stage = document.getElementById("stage");
const canvas = document.getElementById("canvas");
const fileInput = document.getElementById("file");
const pictureInput = document.getElementById("picture");
const gl = canvas.getContext("webgl2", { antialias: true, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
if (!gl) document.body.textContent = "WebGL 2 is not available in this browser.";

const CLIENT = "p" + Math.random().toString(36).slice(2, 12);
let generation = 0;
let queued = false;
let dragging = false;
let lastTick = 0;

const mirror = createA11yMirror(stage, {
  canvas,
  label: document.title,
  tabbable: "all",
  onActivate: (node) => pressAtCentre(node, (x, y) => { app.press(app.hitId(x, y)); after(); }),
  onFocus: (node) => { app.setFocus(node.id); syncText(); paint(); },
  // a field being edited keeps the keyboard in the text input's own
  // element: the mirror taking it lost the selection, so Ctrl+A then
  // Ctrl+C copied nothing
  canMoveFocus: () => !textInput.isActive(),
});

function paint() {
  if (queued) return;
  queued = true;
  requestAnimationFrame(frame);
}

function frame(t) {
  queued = false;
  app.setWidth(stage.clientWidth || window.innerWidth, window.innerHeight);
  const list = JSON.parse(app.displayListJson());
  const W = app.widthPx();
  const H = app.heightPx();
  const dpr = Math.min(2, window.devicePixelRatio || 1);
  canvas.style.width = W + "px";
  canvas.style.height = H + "px";
  canvas.width = Math.round(W * dpr);
  canvas.height = Math.round(H * dpr);
  stage.style.height = H + "px";
  window.__lastList = list;
  syncAddress(false);
  if (gl) renderDisplayList(gl, { width: W, height: H, list }, { dpr });
  generation += 1;
  const tree = JSON.parse(app.a11yJson(generation, app.focused || ""));
  mirror.update(tree);
  const dt = lastTick ? Math.min(t - lastTick, 100) : 16;
  lastTick = t;
  if (app.tick(dt)) paint();
  else lastTick = 0;
}

// The tab in the address. A tab the person picked is a step Back returns
// from; one the page moved to itself (opening a space) replaces the address.
const hashTab = () => decodeURIComponent(location.hash.slice(1));
let wantTab = hashTab();
let shownTab = "";
function syncAddress(step) {
  const t = app.tab;
  if (t === shownTab || wantTab) return;
  const url = location.pathname + location.search + "#" + t;
  if (step && shownTab) history.pushState(null, "", url);
  else history.replaceState(null, "", url);
  shownTab = t;
}

// the tab the address named when the page opened, once there is a space
// to show it in (or none)
function openWanted() {
  if (!wantTab) return;
  const t = wantTab;
  wantTab = "";
  if (app.goTab(t)) shownTab = t;
}

window.addEventListener("popstate", () => {
  const t = hashTab();
  if (!t || t === app.tab) return;
  if (app.loading) {
    wantTab = t;
    return;
  }
  shownTab = t;
  if (app.goTab(t)) after();
  else shownTab = "";
});

// After a press: what the app left for the host to do.
function after() {
  syncAddress(true);
  if (calShowsFor) saveCalShows();
  const a = app.takeAction();
  if (a === "import") fileInput.click();
  else if (a === "image") pictureInput.click();
  else if (a === "copy") copy(app.copyText);
  else if (a === "places") findPlaces(app.placeQuery);
  else if (a.startsWith("open:")) {
    const url = a.slice(5);
    if (/^https?:\/\//.test(url)) window.open(url, "_blank", "noopener");
    else location.href = /^[a-z]+:/.test(url) ? url : SERVER + url;
  }
  else if (a.startsWith("export:")) {
    const id = a.slice(7);
    download(app.sheetFileName(id), app.sheetCsv(id), "text/csv");
  }
  syncText();
  pump();
  paint();
}

// Text the app hands over goes on the clipboard; without the async
// clipboard (an http page, an old browser) through a hidden text area.
async function copy(text) {
  let ok = false;
  try {
    await navigator.clipboard.writeText(text);
    ok = true;
  } catch {
    const t = document.createElement("textarea");
    t.value = text;
    t.style.position = "fixed";
    t.style.opacity = "0";
    document.body.appendChild(t);
    t.select();
    try { ok = document.execCommand("copy"); } catch { ok = false; }
    t.remove();
  }
  app.copied(ok);
  paint();
}

// The app's calls to the server, one at a time, in order.
let pumping = false;
async function pump() {
  if (!SERVER || pumping) return;
  pumping = true;
  try {
    while (app.outCount() > 0) {
      const op = app.outOp();
      const body = app.outBody();
      let res;
      let text = "";
      try {
        res = await fetch(SERVER + "api/" + op, {
          method: "POST",
          credentials: "same-origin",
          headers: { "content-type": "application/json", "x-warden-client": CLIENT },
          body,
        });
        text = await res.text();
      } catch {
        // the call stays in the outbox for the next change to send
        app.status = app.lang === "fi" ? "Ei yhteyttä palvelimeen" : "No connection to the server";
        app.rebuild();
        break;
      }
      if (res.status === 401) {
        location.href = SERVER + "login";
        return;
      }
      if (res.ok) app.answer(text);
      else {
        let msg = text;
        try { msg = JSON.parse(text).error || msg; } catch { /* not JSON: the text is the message */ }
        app.failed(msg || String(res.status));
      }
      if (!app.loading) openWanted();
      try { if (app.spaceId) localStorage.setItem(REMEMBER, app.spaceId); } catch { /* storage off */ }
      loadCalShows();
      weatherIfMoved();
      paint();
    }
  } finally {
    pumping = false;
  }
  paint();
}

// The text-input session: a real <input> edits the focused field, so a
// phone's keyboard, IME, the clipboard and undo work; the app mirrors it.
const textInput = createTextInputBridge({
  host: stage,
  canvas,
  onEdit: ({ value, selStart, selEnd }) => {
    const tid = textInput.activeTid();
    if (!tid || !app.applyEdit(tid, value, selStart, selEnd)) return;
    paint();
    const st = JSON.parse(app.fieldStateJson(tid));
    if (st && st.value !== value) textInput.sync(st);
  },
  // Tab, Enter and Escape are the page's; every other key is the field's.
  onKey: (k) => {
    if (k.key !== "Tab" && k.key !== "Enter" && k.key !== "Escape") return false;
    const took = app.keyWith(k.key, k.shiftKey, k.ctrlKey || k.metaKey);
    after();
    return took;
  },
});

// Hand the keyboard to the field the app has focused, or take it back.
function syncText() {
  const tid = app.focusedField();
  if (!tid) {
    if (textInput.isActive()) textInput.blurField();
    return;
  }
  const st = JSON.parse(app.fieldStateJson(tid));
  if (!st) textInput.blurField();
  else if (textInput.activeTid() === tid) textInput.sync(st);
  else textInput.focusField(tid, st);
}

function download(name, text, type) {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

fileInput.addEventListener("change", async () => {
  const f = fileInput.files && fileInput.files[0];
  if (!f) return;
  app.importSheet(f.name.replace(/\.(csv|tsv|txt)$/i, ""), await f.text());
  fileInput.value = "";
  paint();
});

// A picture for the assistant: at most 1600 px on its longer side, as JPEG,
// so a phone's photo of a school letter stays readable and small.
const PICTURE_SIDE = 1600;
async function addPicture(file) {
  if (!file || !file.type.startsWith("image/")) return;
  try {
    const bmp = await createImageBitmap(file);
    const k = Math.min(1, PICTURE_SIDE / Math.max(bmp.width, bmp.height));
    const c = document.createElement("canvas");
    c.width = Math.max(1, Math.round(bmp.width * k));
    c.height = Math.max(1, Math.round(bmp.height * k));
    const g = c.getContext("2d");
    g.fillStyle = "#fff";
    g.fillRect(0, 0, c.width, c.height);
    g.drawImage(bmp, 0, 0, c.width, c.height);
    const url = c.toDataURL("image/jpeg", 0.85);
    app.addImage(file.name || "", "image/jpeg", url.slice(url.indexOf(",") + 1));
  } catch {
    app.status = app.lang === "fi" ? "Kuvaa ei voitu lukea" : "The picture could not be read";
    app.rebuild();
  }
  paint();
}

pictureInput.addEventListener("change", async () => {
  for (const f of Array.from(pictureInput.files || [])) await addPicture(f);
  pictureInput.value = "";
});

// Cells copied from a spreadsheet arrive as tab-separated text; a picture
// pasted on the Warden page goes to the assistant.
document.addEventListener("paste", (ev) => {
  const items = ev.clipboardData ? Array.from(ev.clipboardData.items || []) : [];
  const pic = items.find((it) => it.kind === "file" && it.type.startsWith("image/"));
  if (SERVER && pic) {
    ev.preventDefault();
    addPicture(pic.getAsFile());
    return;
  }
  const text = ev.clipboardData && ev.clipboardData.getData("text/plain");
  if (!text || !/\t/.test(text)) return;
  ev.preventDefault();
  app.importSheet(app.lang === "fi" ? "Liitetty taulukko" : "Pasted sheet", text);
  paint();
});

const at = (ev) => {
  const r = canvas.getBoundingClientRect();
  return [ev.clientX - r.left, ev.clientY - r.top];
};

canvas.addEventListener("pointerdown", (ev) => {
  const [x, y] = at(ev);
  const id = app.hitId(x, y);
  canvas.focus({ preventScroll: true });
  canvas.setPointerCapture(ev.pointerId);
  dragging = true;
  app.setPressed(id);
  app.pointerDown(id, x, y);
  after();
});

canvas.addEventListener("pointermove", (ev) => {
  const [x, y] = at(ev);
  if (dragging) {
    if (app.pointerMove(x, y)) paint();
    return;
  }
  const id = app.hitId(x, y);
  if (id !== app.hovered) {
    app.setHover(id);
    paint();
  }
  canvas.style.cursor = app.cursorAt(x, y) || "default";
  // a person's email as the browser's tooltip
  const tip = app.tipAt(x, y);
  if (canvas.title !== tip) canvas.title = tip;
});

canvas.addEventListener("pointerup", () => {
  dragging = false;
  app.setPressed("");
  app.pointerUp();
  syncText();
  pump();
  paint();
});

// A double-click opens the event under it in the form.
canvas.addEventListener("dblclick", (ev) => {
  const [x, y] = at(ev);
  if (app.openAt(x, y)) after();
});

canvas.addEventListener("pointerleave", () => {
  if (app.hovered) {
    app.setHover("");
    paint();
  }
});

canvas.addEventListener("wheel", (ev) => {
  const [x, y] = at(ev);
  app.cursorAt(x, y);
  if (app.scrollBy(ev.deltaY)) {
    ev.preventDefault();
    paint();
  }
}, { passive: false });

document.addEventListener("keydown", (ev) => {
  if (ev.target === fileInput || ev.target === pictureInput) return;
  // a key a text field's input already handed to the app (Enter, Tab,
  // Escape) is not the page's a second time
  if (ev.defaultPrevented) return;
  if (textInput.isActive() && ev.target === textInput.element()) return;
  if (app.keyWith(ev.key, ev.shiftKey, ev.ctrlKey || ev.metaKey)) {
    ev.preventDefault();
    after();
  }
});

// The summary's clock: each new minute is drawn; a new day loads the page
// again, so today is today (unless a text field is being edited).
setInterval(() => {
  const t = new Date();
  if (t.getDate() !== now.getDate()) {
    if (!app.focusedField()) location.reload();
    return;
  }
  app.setNow(t.getHours() * 60 + t.getMinutes());
  paint();
}, 15000);

// The weather on the summary, from Open-Meteo (no key), by the hour for
// the school day (when it starts, midday, when it ends) and the next one,
// for each of the space's places (Tampere when it has none); read again every half hour,
// and when the places change. A place without an answer has no weather.
let wxPlaces = "";
async function weather() {
  wxPlaces = app.weatherPlacesJson();
  const r = (v) => Math.round(Number(v) || 0);
  for (const p of JSON.parse(wxPlaces)) {
    try {
      const q = `latitude=${p.lat}&longitude=${p.lon}&current=temperature_2m,weather_code`
        + "&hourly=temperature_2m,precipitation_probability,weather_code&timezone=auto&forecast_days=3";
      const w = await (await fetch("https://api.open-meteo.com/v1/forecast?" + q)).json();
      if (w.hourly) app.setWeatherHours(p.name, r(w.current.temperature_2m), r(w.current.weather_code), JSON.stringify(w.hourly));
      paint();
    } catch { /* no weather for this place */ }
  }
}
function weatherIfMoved() {
  if (app.weatherPlacesJson() !== wxPlaces) weather();
}
weather();
setInterval(weather, 30 * 60 * 1000);

// A town in Finland for the places, from Open-Meteo's geocoding.
async function findPlaces(name) {
  let text = '{"results":[]}';
  try {
    const q = new URLSearchParams({ name, count: "6", language: app.lang, countryCode: "FI", format: "json" });
    text = await (await fetch("https://geocoding-api.open-meteo.com/v1/search?" + q)).text();
  } catch { /* nothing found */ }
  app.placesFound(text);
  paint();
}

// Changes made elsewhere: asked for every few seconds while the page is in
// view, at once when it comes back into view, never in the middle of a drag.
const POLL_MS = 5000;
function poll() {
  if (!SERVER || dragging || document.visibilityState !== "visible") return;
  if (app.poll()) pump();
}
setInterval(poll, POLL_MS);
document.addEventListener("visibilitychange", poll);
window.addEventListener("focus", poll);

window.addEventListener("resize", paint);
document.fonts && document.fonts.ready.then(paint);
window.__spaces = app;
// for a check driving the page: press as a click does
window.__spacesPress = (id) => { app.press(id); after(); };
if (!SERVER) openWanted();
if (SERVER) {
  try { app.prefer = localStorage.getItem(REMEMBER) || ""; } catch { /* storage off */ }
  app.start();
  pump();
}
paint();
