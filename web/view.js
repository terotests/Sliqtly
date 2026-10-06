// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The public viewer: a shared presentation, /s/{id}, as sliqtly.com shows it
// to anyone with the link.
//
// The slides are laid out on the server (GET /api/view/{id},
// mcp-go/rgr/View.rgr: the editor's own deck model, as render_slide uses
// it) and arrive as display lists at rest, every build step shown. This page
// only paints them with EVG's WebGL painter (gl/evg-webgl.js), so the site
// ships no Markdown, layout, chart or editor code (scripts/build-view.mjs).
// What that leaves out: build steps and the slides' own animations (a chart
// growing, a diagram tour); surface effects (waves, rain) still run, as the
// painter runs them.

import { prepareDisplayList, setFontFallback } from "./gl/evg-webgl.js";
import { decodePicture } from "./picture.js";
import { INTRO_MS } from "./brand.js";
import { linkOf, viewUrl, picturesOf, lookFacesOf, LOOK_FACES, slideForKey, fitSlide } from "./viewlink.js";

const FONTS = document.querySelector('meta[name="fonts"]')?.content || "";
const fi = /^fi\b/i.test(navigator.language || "");
const say = (en, fiText) => (fi ? fiText : en);
document.documentElement.lang = fi ? "fi" : "en";

const canvas = document.getElementById("c");
const intro = document.getElementById("brandIntro");
const note = document.getElementById("note");
const bar = document.getElementById("viewBar");
const vCount = document.getElementById("vCount");
const vGo = document.getElementById("vGo");
for (const [id, en, fiText] of [
  ["vFirst", "First slide (Home)", "Ensimmäinen dia (Home)"],
  ["vPrev", "Previous (←)", "Edellinen (←)"],
  ["vNext", "Next (→, Space)", "Seuraava (→, välilyönti)"],
  ["vCount", "Go to slide… (type the number and Enter)", "Siirry diaan… (kirjoita numero ja Enter)"],
  ["vFull", "Full screen", "Koko näyttö"],
]) document.getElementById(id).title = say(en, fiText);

// the address, or the one the assistant's preview gives in <meta>
const given = document.querySelector('meta[name="sliqtly-link"]')?.content || "";
const link = linkOf(given ? "" : location.pathname, given ? "" : location.search, location.hash, given);

const FACES = [
  ["Open Sans", "OpenSans-Regular.ttf"],
  ["Open Sans-Bold", "OpenSans-Bold.ttf"],
  ["Open Sans-Italic", "OpenSans-Italic.ttf"],
  ["Open Sans-BoldItalic", "OpenSans-BoldItalic.ttf"],
  ["Noto Sans", "NotoSans-Regular.ttf"],
  ["Noto Sans-Bold", "NotoSans-Bold.ttf"],
];
let faces = [];
async function loadFace(name, file) {
  const res = await fetch("./fonts/" + file + (FONTS && !FONTS.startsWith("__") ? "?v=" + FONTS : ""));
  if (!res.ok) throw new Error(file + " → " + res.status);
  const face = new FontFace(name, await res.arrayBuffer());
  await face.load();
  document.fonts.add(face);
  return name;
}
async function loadFaces(names) {
  const got = await Promise.all(names.map(([name, file]) => loadFace(name, file).catch((e) => {
    console.warn("face not loaded: " + name, e);
    return null;
  })));
  faces = faces.concat(got.filter(Boolean));
  setFontFallback(faces);
}

function showNote(html) {
  note.innerHTML = html;
  intro.hidden = false;
  started();
}

// the assistant's preview (mcp-go/assets/preview.html) waits for this: the
// viewer started, and showed slides or said why not
function started() {
  window.__pageStarted = true;
}

// --- the intro: Sliqtly's logo and name before the slides (web/brand.js) ----
function playIntro() {
  return new Promise((done) => {
    let timer = 0;
    const end = (ev) => {
      if (ev) {
        ev.preventDefault();
        ev.stopImmediatePropagation();
      }
      clearTimeout(timer);
      window.removeEventListener("pointerdown", end, true);
      window.removeEventListener("keydown", end, true);
      intro.classList.add("out");
      setTimeout(() => { intro.hidden = true; intro.classList.remove("out"); }, 350);
      done();
    };
    window.addEventListener("pointerdown", end, true);
    window.addEventListener("keydown", end, true);
    timer = setTimeout(end, Math.max(0, INTRO_MS - (performance.now() - (window.__introAt ?? performance.now()))));
  });
}

// --- painting -----------------------------------------------------------------
let gl = null;
let deck = null;
let lists = [];
const pictures = new Map();
let at = 0;
let shownAt = 0;
let raf = 0;

function paint() {
  raf = 0;
  if (!gl || !lists.length) return;
  const dpr = Math.min(window.devicePixelRatio || 1, 3);
  const w = canvas.clientWidth;
  const h = canvas.clientHeight;
  if (canvas.width !== Math.round(w * dpr) || canvas.height !== Math.round(h * dpr)) {
    canvas.width = Math.round(w * dpr);
    canvas.height = Math.round(h * dpr);
  }
  gl.viewport(0, 0, canvas.width, canvas.height);
  gl.clearColor(0, 0, 0, 1);
  gl.clear(gl.COLOR_BUFFER_BIT | gl.STENCIL_BUFFER_BIT);
  const list = lists[at];
  const t = (performance.now() - shownAt) / 1000;
  for (const e of list.effects || []) e.time = t;
  const doc = { width: w, height: h, view: fitSlide(w, h, deck.width, deck.height), list };
  const f = prepareDisplayList(gl, doc, { dpr, images: pictures, contrastGuard: true, contrastRepair: true });
  f.draw(null, null, { clear: false });
  f.dispose();
  // a surface effect moves: drawn again on the next frame
  if ((list.effects || []).length) raf = requestAnimationFrame(paint);
}
function repaint() {
  if (!raf) raf = requestAnimationFrame(paint);
}
window.addEventListener("resize", repaint);

// --- the way round ---------------------------------------------------------------
function go(i) {
  const n = lists.length;
  if (!n) return;
  i = Math.max(0, Math.min(n - 1, i));
  if (i !== at) shownAt = performance.now();
  at = i;
  vCount.textContent = (at + 1) + " / " + n;
  if (!given) {
    const q = new URLSearchParams(location.hash.replace(/^#/, ""));
    if (at > 0) q.set("slide", String(at + 1));
    else q.delete("slide");
    const hash = q.toString();
    history.replaceState(null, "", location.pathname + location.search + (hash ? "#" + hash : ""));
  }
  repaint();
}

let idleTimer = 0;
function wake() {
  document.body.classList.remove("idle");
  clearTimeout(idleTimer);
  idleTimer = setTimeout(() => {
    if (vGo.hidden) document.body.classList.add("idle");
    else wake();
  }, 2500);
}
for (const ev of ["pointermove", "pointerdown", "keydown"]) window.addEventListener(ev, wake, { passive: true });

function openGoTo() {
  vGo.value = "";
  vGo.hidden = false;
  vCount.hidden = true;
  vGo.focus();
}
function closeGoTo() {
  vGo.hidden = true;
  vCount.hidden = false;
}
vCount.addEventListener("click", openGoTo);
vGo.addEventListener("keydown", (ev) => {
  ev.stopPropagation();
  if (ev.key === "Enter") {
    const n = parseInt(vGo.value, 10);
    closeGoTo();
    if (n > 0) go(n - 1);
  } else if (ev.key === "Escape") closeGoTo();
});
vGo.addEventListener("blur", closeGoTo);
document.getElementById("vFirst").addEventListener("click", () => go(0));
document.getElementById("vPrev").addEventListener("click", () => go(at - 1));
document.getElementById("vNext").addEventListener("click", () => go(at + 1));
document.getElementById("vFull").addEventListener("click", () => {
  if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
  else document.documentElement.requestFullscreen?.().catch(() => {});
});

// a number typed and Enter goes to that slide, as in the editor's presenting
let typed = "";
window.addEventListener("keydown", (ev) => {
  if (!lists.length || ev.ctrlKey || ev.metaKey || ev.altKey) return;
  if (/^[0-9]$/.test(ev.key)) {
    typed = (typed + ev.key).slice(-4);
    return;
  }
  if (ev.key === "Enter" && typed) {
    go(parseInt(typed, 10) - 1);
    typed = "";
    ev.preventDefault();
    return;
  }
  typed = "";
  if (ev.key === "f" || ev.key === "F") {
    document.getElementById("vFull").click();
    return;
  }
  if (ev.target.closest?.("#viewBar")) return;
  const to = slideForKey(ev.key, at, lists.length);
  if (to < 0) return;
  ev.preventDefault();
  go(to);
});

// a click or a tap: on the left third back, elsewhere on; a swipe sideways
let down = null;
canvas.addEventListener("pointerdown", (ev) => { down = { x: ev.clientX, y: ev.clientY }; });
canvas.addEventListener("pointerup", (ev) => {
  if (!down || !lists.length) return;
  const dx = ev.clientX - down.x;
  const dy = ev.clientY - down.y;
  down = null;
  if (Math.abs(dx) > 40 && Math.abs(dx) > Math.abs(dy)) go(at + (dx < 0 ? 1 : -1));
  else if (Math.abs(dx) < 10 && Math.abs(dy) < 10) go(at + (ev.clientX < canvas.clientWidth / 3 ? -1 : 1));
});

// --- start ---------------------------------------------------------------------
async function pictureOf(p) {
  try {
    const res = await fetch(p.url);
    if (!res.ok) throw new Error("HTTP " + res.status);
    const pic = await decodePicture(new Uint8Array(await res.arrayBuffer()), p.type, p.path);
    if (pic.img) pictures.set(p.src, pic.img);
  } catch (e) {
    console.warn("picture not loaded: " + p.path, e);
  }
}

async function start() {
  // the front page (view.html shows it when the address is no presentation)
  if (!link) return;
  if (link.md) {
    showNote(say(
      "This link carries the presentation in its address, which sliqtly.com does not open at the moment. Ask for a /s/ link to it.",
      "Tämä linkki kantaa esityksen osoitteessaan, eikä sliqtly.com avaa sellaisia nyt. Pyydä esitykseen /s/-linkki."));
    return;
  }
  gl = canvas.getContext("webgl2", { antialias: true, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
  if (!gl) {
    showNote(say("This browser cannot show the presentation (WebGL 2 is not available).", "Tämä selain ei pysty näyttämään esitystä (WebGL 2 ei ole käytettävissä)."));
    return;
  }
  const shown = playIntro();
  const fonts = loadFaces(FACES);
  let got;
  try {
    const res = await fetch(viewUrl(link));
    if (res.status === 404) {
      await shown;
      showNote(say("This shared presentation was not found.", "Jaettua esitystä ei löytynyt."));
      return;
    }
    if (!res.ok) throw new Error("HTTP " + res.status);
    got = await res.json();
  } catch (e) {
    console.warn(e);
    await shown;
    showNote(say("Could not open the shared presentation.", "Jaettua esitystä ei voitu avata."));
    return;
  }
  deck = got.deck;
  if (deck.name) document.title = deck.name + " · Sliqtly";
  const looks = lookFacesOf(got.lists).map((name) => [name, LOOK_FACES[name]]);
  await Promise.all([fonts.then(() => looks.length && loadFaces(looks)), ...picturesOf(deck).map(pictureOf)]);
  lists = got.lists;
  await shown;
  bar.hidden = false;
  shownAt = performance.now();
  at = -1;
  go(Math.min(link.slide, lists.length - 1));
  wake();
  started();
}
start();
