// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The Spaces page. The app (src/SpacesApp.rgr, compiled into spaces_app.js)
// builds one EVG tree; this file paints its display list on a WebGL canvas,
// sends it the pointer, the wheel and the keys, mirrors its accessible tree
// into DOM over the canvas, and does the two things a tree cannot: read a
// file or pasted cells for an import, and save a download.

import { renderDisplayList } from "./gl/evg-webgl.js";
import { createA11yMirror, pressAtCentre } from "./gl/evg-a11y.js";
import { installCanvasMeasurer } from "./gl/evg-measure.js";

const M = globalThis.SpacesModule;
installCanvasMeasurer([M]);

const css = await (await fetch("./spaces.css?v=__BUILD__")).text();
const app = new M.SpacesApp();
const now = new Date();
app.lang = (navigator.language || "fi").toLowerCase().startsWith("fi") ? "fi" : "en";
app.init(css);
app.useClock(now.getFullYear(), now.getMonth() + 1, now.getDate(), now.getHours() * 60 + now.getMinutes(), -now.getTimezoneOffset());

const stage = document.getElementById("stage");
const canvas = document.getElementById("canvas");
const fileInput = document.getElementById("file");
const gl = canvas.getContext("webgl2", { antialias: true, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
if (!gl) document.body.textContent = "WebGL 2 is not available in this browser.";

let generation = 0;
let queued = false;
let dragging = false;
let lastTick = 0;

const mirror = createA11yMirror(stage, {
  canvas,
  label: "Spaces",
  tabbable: "all",
  onActivate: (node) => pressAtCentre(node, (x, y) => { app.press(app.hitId(x, y)); after(); }),
  onFocus: (node) => { app.setFocus(node.id); paint(); },
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
  if (gl) renderDisplayList(gl, { width: W, height: H, list }, { dpr });
  generation += 1;
  const tree = JSON.parse(app.a11yJson(generation, app.focused || ""));
  mirror.update(tree);
  const dt = lastTick ? Math.min(t - lastTick, 100) : 16;
  lastTick = t;
  if (app.tick(dt)) paint();
  else lastTick = 0;
}

// After a press: what the app left for the host to do.
function after() {
  const a = app.takeAction();
  if (a === "import") fileInput.click();
  else if (a.startsWith("export:")) {
    const id = a.slice(7);
    download(app.sheetFileName(id), app.sheetCsv(id), "text/csv");
  }
  paint();
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

// Cells copied from a spreadsheet arrive as tab-separated text.
document.addEventListener("paste", (ev) => {
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
});

canvas.addEventListener("pointerup", () => {
  dragging = false;
  app.setPressed("");
  app.pointerUp();
  paint();
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
  if (ev.target === fileInput) return;
  if (app.keyWith(ev.key, ev.shiftKey, ev.ctrlKey || ev.metaKey)) {
    ev.preventDefault();
    after();
  }
});

window.addEventListener("resize", paint);
document.fonts && document.fonts.ready.then(paint);
window.__spaces = app;
// for a check driving the page: press as a click does
window.__spacesPress = (id) => { app.press(id); after(); };
paint();
