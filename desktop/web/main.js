// SPDX-License-Identifier: MIT
//
// The browser host for Sliqtly Editor. It owns what a browser has and Ranger
// does not, and asks the app (src/EditorApp.rgr, compiled to JavaScript)
// everything else:
//
//   the network    fetch(), for the requests the app queues (takeRequest /
//                  deliver); CORS is the Sliqtly server's job
//   the settings   localStorage (the app's settingsJson)
//   the clipboard  the browser's copy / cut / paste events
//   signing in     PKCE S256 with crypto.subtle, client sliqtly-web, back
//                  through callback.html
//   the pixels     one WebGL 2 canvas: the app's display list, then the
//                  server's display list of the current slide in the preview
//                  rectangle the app reports (EVG's evg-webgl.js for both)

import { prepareDisplayList, setFontFallback } from "./gen/evg/gl/evg-webgl.js";
import { installCanvasMeasurer } from "./gen/evg/gl/evg-measure.js";
import AppModule, { EditorApp } from "./gen/app.js";
import { EDITOR_CSS } from "./gen/assets.js";

const canvas = document.getElementById("c");
const errEl = document.getElementById("err");
const SETTINGS_KEY = "sliqtly-editor.settings";
const OAUTH_KEY = "sliqtly-editor.oauth";
const RETURN_KEY = "sliqtly-editor.oauth-return";
const IS_MAC = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
const params = new URLSearchParams(location.search);

// --- fonts: the interface, the Markdown, the slides -------------------------------
const FACES = [
  ["Noto Sans", "NotoSans-Regular.ttf"],
  ["Noto Sans-Bold", "NotoSans-Bold.ttf"],
  ["DejaVu Sans Mono", "DejaVuSansMono.ttf"],
  ["DejaVu Sans Mono-Bold", "DejaVuSansMono-Bold.ttf"],
  ["Open Sans", "OpenSans-Regular.ttf"],
  ["Open Sans-Bold", "OpenSans-Bold.ttf"],
  ["Open Sans-Italic", "OpenSans-Italic.ttf"],
  ["Open Sans-BoldItalic", "OpenSans-BoldItalic.ttf"],
];
async function loadFaces() {
  const names = [];
  await Promise.all(FACES.map(async ([name, file]) => {
    try {
      const face = new FontFace(name, `url(./gen/fonts/${file})`);
      await face.load();
      document.fonts.add(face);
      names.push(name);
    } catch (e) {
      console.warn("font not loaded: " + file, e);
    }
  }));
  setFontFallback(FACES.map(([n]) => n).filter((n) => names.includes(n)));
}

// --- the app -----------------------------------------------------------------------
await loadFaces();
const measure = installCanvasMeasurer(AppModule, { fallback: FACES.map(([n]) => n) });
measure.refresh();
const app = new EditorApp();
app.init(EDITOR_CSS);
for (let i = 0; i < app.styleErrorCount(); i++) console.warn("editor.css:", app.styleErrorAt(i));
app.setPlatform("web");
app.setWebOrigin(location.origin);
window.__app = app;
let settingsText = "{}";
try { settingsText = localStorage.getItem(SETTINGS_KEY) || "{}"; } catch {}
app.loadSettings(settingsText);
app.setNow(Date.now());

const gl = canvas.getContext("webgl2", { alpha: false, antialias: true, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
if (!gl) errEl.textContent = "WebGL 2 is not available in this browser.";

let W = 0, H = 0, dpr = 1;
function resize() {
  dpr = Math.min(2, window.devicePixelRatio || 1);
  W = window.innerWidth;
  H = window.innerHeight;
  canvas.width = Math.round(W * dpr);
  canvas.height = Math.round(H * dpr);
  app.setViewport(W, H);
  wanted = true;
}
let wanted = true;
window.addEventListener("resize", resize);
resize();

// --- the network ---------------------------------------------------------------------
function perform(id) {
  const method = app.reqMethod(id);
  const url = app.reqUrl(id);
  const headers = {};
  for (const line of app.reqHeaders(id).split("\n")) {
    const at = line.indexOf(":");
    if (at > 0) headers[line.slice(0, at).trim()] = line.slice(at + 1).trim();
  }
  const body = app.reqBody(id);
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), app.reqTimeoutMs(id) || 20000);
  fetch(url, { method, headers, body: method === "GET" || method === "HEAD" ? undefined : body, signal: ctl.signal, cache: "no-store" })
    .then(async (res) => app.deliver(id, res.status, await res.text(), ""))
    .catch((e) => app.deliver(id, 0, "", e && e.name === "AbortError" ? "timed out" : String((e && e.message) || e)))
    .finally(() => { clearTimeout(timer); wanted = true; });
}

// --- signing in (PKCE S256) ----------------------------------------------------------
const b64url = (bytes) => btoa(String.fromCharCode(...new Uint8Array(bytes))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
const randomText = (n) => b64url(crypto.getRandomValues(new Uint8Array(n)));
async function beginSignIn() {
  const verifier = randomText(48);
  const state = randomText(16);
  const challenge = b64url(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier)));
  const redirect = location.origin + location.pathname.replace(/[^/]*$/, "") + "callback.html";
  sessionStorage.setItem(OAUTH_KEY, JSON.stringify({ verifier, state, redirect }));
  location.assign(app.oauthAuthorizeUrl(challenge, state, redirect));
}

// Back from callback.html?
let returned = null;
try {
  returned = JSON.parse(sessionStorage.getItem(RETURN_KEY) || "null");
  sessionStorage.removeItem(RETURN_KEY);
} catch {}
if (returned) {
  const pending = JSON.parse(sessionStorage.getItem(OAUTH_KEY) || "null");
  sessionStorage.removeItem(OAUTH_KEY);
  app.start();
  if (!pending) app.oauthFailed("the sign-in was not started here");
  else if (returned.error) app.oauthFailed(returned.error);
  else if (returned.state !== pending.state) app.oauthFailed("the answer did not match (state)");
  else app.oauthCode(returned.code, pending.verifier, pending.redirect);
} else {
  app.start();
}
if (params.get("script")) {
  fetch(params.get("script")).then((r) => r.text()).then((t) => app.runScript(t));
}

// --- what the app asks for -----------------------------------------------------------
function runCommands() {
  for (let name = app.takeCommand(); name; name = app.takeCommand()) {
    const arg = app.commandArg();
    switch (name) {
      case "save-settings":
        try { localStorage.setItem(SETTINGS_KEY, app.settingsJson()); } catch (e) { console.warn("settings not saved", e); }
        break;
      case "copy":
        navigator.clipboard?.writeText(arg).catch(() => {});
        break;
      case "paste":
        navigator.clipboard?.readText().then((t) => { app.paste(t); wanted = true; }).catch(() => {});
        break;
      case "open-url":
        window.open(arg, "_blank", "noopener");
        break;
      case "oauth":
        beginSignIn();
        break;
      case "log":
        console.log(arg);
        break;
      case "fail":
        console.error("script failed: " + arg);
        errEl.textContent = "script failed: " + arg;
        window.__scriptFailed = arg;
        break;
      case "quit":
        window.__scriptDone = true;
        break;
      case "shot":
        window.__shots = (window.__shots || 0) + 1;
        break;
      default:
        break;
    }
  }
}

// --- input -----------------------------------------------------------------------------
const local = (ev) => {
  const r = canvas.getBoundingClientRect();
  return [ev.clientX - r.left, ev.clientY - r.top];
};
canvas.addEventListener("pointerdown", (ev) => {
  canvas.focus();
  canvas.setPointerCapture(ev.pointerId);
  const [x, y] = local(ev);
  app.setNow(Date.now());
  app.pointerDown(x, y, ev.shiftKey);
  wanted = true;
});
canvas.addEventListener("pointermove", (ev) => {
  const [x, y] = local(ev);
  app.pointerMove(x, y, (ev.buttons & 1) === 1);
  const c = app.cursorAt(x, y);
  canvas.style.cursor = c === "text" ? "text" : c === "pointer" ? "pointer" : "default";
  wanted = true;
});
const up = (ev) => {
  const [x, y] = local(ev);
  app.pointerUp(x, y);
  wanted = true;
};
canvas.addEventListener("pointerup", up);
canvas.addEventListener("pointercancel", up);
let wheelAcc = 0;
canvas.addEventListener("wheel", (ev) => {
  ev.preventDefault();
  const [x, y] = local(ev);
  wheelAcc += ev.deltaMode === 1 ? ev.deltaY : ev.deltaY / 40;
  const lines = Math.trunc(wheelAcc);
  if (lines) {
    wheelAcc -= lines;
    app.wheel(x, y, lines);
    wanted = true;
  }
}, { passive: false });

const NAMED = {
  ArrowLeft: "Left", ArrowRight: "Right", ArrowUp: "Up", ArrowDown: "Down",
  Home: "Home", End: "End", PageUp: "PageUp", PageDown: "PageDown",
  Enter: "Enter", Backspace: "Backspace", Delete: "Delete", Tab: "Tab", Escape: "Escape", F5: "F5",
};
window.addEventListener("keydown", (ev) => {
  if (ev.isComposing) return;
  const mod = IS_MAC ? ev.metaKey : ev.ctrlKey;
  const named = NAMED[ev.key];
  const letter = ev.key.length === 1 ? ev.key.toLowerCase() : "";
  // copy, cut and paste come as the browser's own events (below)
  if (mod && (letter === "c" || letter === "x" || letter === "v")) return;
  app.setNow(Date.now());
  if (named || (mod && letter)) {
    if (app.key(named || letter, ev.shiftKey, mod, ev.altKey)) ev.preventDefault();
    wanted = true;
    return;
  }
  if (!mod && !ev.ctrlKey && [...ev.key].length === 1) {
    app.typeText(ev.key);
    ev.preventDefault();
    wanted = true;
  }
});
document.addEventListener("copy", (ev) => {
  const t = app.selectedText();
  if (!t) return;
  ev.clipboardData.setData("text/plain", t);
  ev.preventDefault();
});
document.addEventListener("cut", (ev) => {
  const t = app.cutText();
  if (!t) return;
  ev.clipboardData.setData("text/plain", t);
  ev.preventDefault();
  wanted = true;
});
document.addEventListener("paste", (ev) => {
  const t = ev.clipboardData.getData("text/plain");
  if (!t) return;
  app.paste(t);
  ev.preventDefault();
  wanted = true;
});

// --- painting ------------------------------------------------------------------------------
let viewGen = -1, view = null, shownSlide = -1, shownGen = -1, title = "";
function paint() {
  const list = JSON.parse(app.displayListJson());
  const ui = prepareDisplayList(gl, { width: W, height: H, list }, { dpr });
  ui.draw();
  ui.dispose();
  if (app.previewVisible()) {
    if (viewGen !== app.viewGeneration()) {
      viewGen = app.viewGeneration();
      try { view = JSON.parse(app.viewBody()); } catch { view = null; }
    }
    const slide = view && view.lists && view.lists[app.previewSlide()];
    if (slide) {
      const scale = app.previewW() / (view.deck.width || 1920);
      const x = app.previewX(), y = app.previewY(), w = app.previewW(), h = app.previewH();
      gl.enable(gl.SCISSOR_TEST);
      gl.scissor(Math.floor(x * dpr), Math.floor(canvas.height - (y + h) * dpr), Math.ceil(w * dpr), Math.ceil(h * dpr));
      const f = prepareDisplayList(gl, { width: W, height: H, view: { x, y, scale }, list: slide }, { dpr });
      f.draw(null, null, { clear: false });
      f.dispose();
      gl.disable(gl.SCISSOR_TEST);
    }
  }
}

function frame() {
  try {
    app.setNow(Date.now());
    app.scriptTick();
    for (let id = app.takeRequest(); id; id = app.takeRequest()) perform(id);
    runCommands();
    const t = app.windowTitle();
    if (t !== title) document.title = title = t;
    const changed = app.needsPaint() || app.viewGeneration() !== shownGen || app.previewSlide() !== shownSlide;
    if (gl && (wanted || changed)) {
      wanted = false;
      shownGen = app.viewGeneration();
      shownSlide = app.previewSlide();
      paint();
    }
  } catch (e) {
    errEl.textContent = String((e && e.stack) || e);
  }
  requestAnimationFrame(frame);
}
requestAnimationFrame(frame);
