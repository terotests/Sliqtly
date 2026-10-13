// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The light player (web/play.html at /r/{id}/{hash}): a presentation as
// recorded for the web (web/recstream.js), painted frame by frame by EVG's
// WebGL painter. What it loads: this module, the painter, the recording
// (its pictures inside it) and the faces the slides are set in. No layout,
// no sign-in: slide scripts and 3-D worlds show as they were recorded.
// Programs on slides (```app) run in their boxes as in the viewer
// (web/viewplay.js), their engine loaded only for a recording that has one.
//
// ← → go from slide to slide (each from its master time, playing its way
// in), Space through the build steps (the sub times) and on, A plays it all.
//
// Exported with the 3-D flight (version 3-D), the recording also carries
// the world flown through (web/playflight.js): 3-D on the bar, or G,
// flies it in full screen; Esc lands back on the slide it ended at.
//
// The same address can hold the presentation as a video (the editor's
// Export ▸ Cloud video, a WebM): the browser's own player plays it. A video
// kept in Storage comes as a redirect there, and the player streams it in
// pieces (Range) as it plays; one the server answers with (no Storage)
// plays from the bytes already read.

import { prepareDisplayList, setFontFallback, imageChanged } from "./gl/evg-webgl.js";
import { registerDeckEffects, reducedMotion, holdStill } from "./fxdeck.js";
import { RecNav, unpackStream } from "./recstream.js";
import { playLinkOf, playUrl, recUrl, recActionForKey, slideOfHash } from "./playlink.js";
import { fitSlide } from "./viewlink.js";

const FONTS = document.querySelector('meta[name="fonts"]')?.content || "";
const fi = /^fi\b/i.test(navigator.language || "");
const say = (en, fiText) => (fi ? fiText : en);

const canvas = document.getElementById("c");
const note = document.getElementById("note");
const bar = document.getElementById("bar");
const pCount = document.getElementById("pCount");
const pAuto = document.getElementById("pAuto");
for (const [id, en, fiText] of [
  ["pPrev", "Previous slide (←)", "Edellinen dia (←)"],
  ["pNext", "Next step (Space)", "Seuraava vaihe (välilyönti)"],
  ["pAuto", "Play it all (A)", "Toista kaikki (A)"],
  ["pFull", "Full screen", "Koko näyttö"],
  ["pShare", "Share this slide (copies its link)", "Jaa tämä dia (kopioi sen linkin)"],
  ["pFlight", "Fly through it in 3-D (G)", "Lennä sen läpi 3D:nä (G)"],
]) document.getElementById(id).title = say(en, fiText);

let gl = null;
let nav = null;
const pictures = new Map();
let navAt = 0;
let raf = 0;
const startedAt = performance.now();

// the 3-D flight (web/playflight.js), when the recording carries one
let flight = null;
// the recording's programs running (web/viewplay.js), when it has some
let plays = null;

function paint() {
  raf = 0;
  if (!gl || !nav || flight?.flying()) return;
  const now = performance.now();
  if (nav.playing) {
    if (navAt) nav.tick(Math.min(0.25, (now - navAt) / 1000));
    navAt = nav.playing ? now : 0;
  } else navAt = 0;
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
  const rec = nav.rec;
  const view = fitSlide(w, h, rec.width, rec.height);
  const list = rec.list(nav.frame());
  const still = reducedMotion();
  const t = (now - startedAt) / 1000;
  for (const e of list.effects || []) {
    if (still) holdStill(e);
    else e.time = t;
  }
  const f = prepareDisplayList(gl, { width: w, height: h, view, list }, { dpr, images: pictures });
  f.draw(null, null, { clear: false });
  f.dispose();
  // the shown slide's programs in their boxes (the frames leave them
  // empty), once the slide has come in
  const sl = rec.slides[nav.slide];
  if (plays && sl && nav.t >= (sl.enter || sl.start) - 1e-6) for (const pl of plays.listsFor(nav.slide, view)) {
    const pf = prepareDisplayList(gl, { width: w, height: h, view, list: pl }, { dpr, images: pictures });
    pf.draw(null, null, { clear: false });
    pf.dispose();
  }
  showState();
  const moving = !still && (list.effects || []).length > 0;
  if (moving || nav.playing) raf = requestAnimationFrame(paint);
}
// the count and the ⏵ button as the recording stands, and the slide in the
// address (#slide=N: a reload or a copied address opens it)
let link = null;
let shownSlide = -1;
function showState() {
  if (nav.slide !== shownSlide) {
    shownSlide = nav.slide;
    // the page's own path: a bare "#…" would go against <base href="/">
    history.replaceState(null, "", location.pathname + location.search + "#slide=" + (nav.slide + 1));
  }
  pCount.textContent = nav.slide + 1 + " / " + nav.count;
  pAuto.textContent = nav.auto && nav.playing ? "⏸\uFE0E" : "⏵\uFE0E";
  pAuto.setAttribute("aria-pressed", String(nav.auto && nav.playing));
}
function repaint() {
  if (!raf) raf = requestAnimationFrame(paint);
}

// a key's or a button's action
function act(action) {
  if (!nav) return false;
  if (action === "next") nav.next();
  else if (action === "nextSlide") nav.nextSlide();
  else if (action === "prevSlide") nav.prevSlide();
  else if (action === "first") nav.go(0);
  else if (action === "last") {
    nav.go(nav.count - 1);
    nav.finish();
  } else if (action === "auto") nav.setAuto(!(nav.auto && nav.playing));
  else return false;
  navAt = 0;
  showState();
  repaint();
  return true;
}

// the bar goes after a while with no pointer or key, and comes back with one
let idleTimer = 0;
function wake() {
  document.body.classList.remove("idle");
  clearTimeout(idleTimer);
  idleTimer = setTimeout(() => document.body.classList.add("idle"), 2500);
}
window.addEventListener("pointermove", wake);
window.addEventListener("keydown", (ev) => {
  wake();
  if (ev.ctrlKey || ev.metaKey || ev.altKey) return;
  if (flight && !flight.flying() && (ev.key === "g" || ev.key === "G")) {
    ev.preventDefault();
    fly();
    return;
  }
  if (act(recActionForKey(ev.key))) ev.preventDefault();
});
// a click or a tap on the slide: the next step (a press a program took,
// web/viewplay.js, never reaches the canvas)
let pressed = false;
canvas.addEventListener("pointerdown", () => { pressed = true; });
canvas.addEventListener("click", () => {
  wake();
  if (!pressed) return;
  pressed = false;
  act("next");
});
document.getElementById("pPrev").addEventListener("click", () => act("prevSlide"));
document.getElementById("pNext").addEventListener("click", () => act("next"));
pAuto.addEventListener("click", () => act("auto"));
document.getElementById("pFull").addEventListener("click", () => {
  if (document.fullscreenElement) document.exitFullscreen?.();
  else document.documentElement.requestFullscreen?.().catch(() => {});
});
window.addEventListener("resize", repaint);
// Share: this slide's link copied, said for a moment on the button
const pShare = document.getElementById("pShare");
pShare.addEventListener("click", async () => {
  if (!nav) return;
  const url = playUrl(location.origin, link.id, link.hash) + "#slide=" + (nav.slide + 1);
  try {
    await navigator.clipboard.writeText(url);
    pShare.textContent = "✓";
  } catch (_) {
    window.prompt(say("The slide's link", "Dian linkki"), url);
  }
  window.__lastShare = url;
  setTimeout(() => { pShare.textContent = "⤴\uFE0E"; }, 1500);
});

// The 3-D flight from a press (it takes the full screen); a word for a
// moment when it cannot fly.
let toastTimer = 0;
function toast(text) {
  note.textContent = text;
  note.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { note.hidden = true; }, 3500);
}
function fly() {
  start3d.hidden = true;
  flight?.start().catch((e) => toast(String((e && e.message) || e)));
}
const pFlight = document.getElementById("pFlight");
const start3d = document.getElementById("start3d");
pFlight.addEventListener("click", fly);
start3d.addEventListener("click", fly);
async function offerFlight(data) {
  const { createPlayFlight } = await import("./playflight.js");
  flight = createPlayFlight({
    flight: data, fi, toast,
    slide: () => nav.slide,
    go: (i) => {
      nav.go(i);
      nav.finish();
    },
    onExit: () => {
      navAt = 0;
      showState();
      repaint();
    },
  });
  pFlight.hidden = false;
  start3d.textContent = say("▶ Fly through it in 3-D", "▶ Lennä läpi 3D:nä");
  start3d.hidden = false;
  window.__play.flight = flight;
}

// The recording's programs, run as the viewer runs them: each over its
// box on its slide while that slide is shown.
async function startRecPlays(s) {
  const { startPlays } = await import("./viewplay.js");
  plays = await startPlays({
    plays: s.plays, scripts: [], lists: [], slideW: nav.rec.width, slideH: nav.rec.height, canvas,
    current: () => nav.slide, count: () => nav.count, shownPages: () => [nav.slide],
    go: (i) => {
      nav.go(i);
      navAt = 0;
      showState();
      repaint();
    },
    repaint, pictures, gl: () => gl, dpr: () => Math.min(window.devicePixelRatio || 1, 3), imageChanged, slidePicture: () => null,
    onStage: (on) => document.body.classList.toggle("game-stage", on),
    readFile: () => "",
  });
  window.__play.plays = plays;
}

async function loadFace(name, file) {
  const res = await fetch("./fonts/" + file + (FONTS && !FONTS.startsWith("__") ? "?v=" + FONTS : ""));
  if (!res.ok) throw new Error(file + " → " + res.status);
  const face = new FontFace(name, await res.arrayBuffer());
  await face.load();
  document.fonts.add(face);
  return name;
}

async function loadPicture(p) {
  try {
    const bin = atob(p.b64);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    pictures.set(p.src, await createImageBitmap(new Blob([bytes], { type: p.type })));
  } catch (e) {
    console.warn("picture not shown: " + p.src, e);
  }
}

function fail(text) {
  note.textContent = text;
  note.hidden = false;
  window.__pageStarted = true;
}

// a WebM starts with the EBML header's id
function isWebm(b) {
  return b.length > 4 && b[0] === 0x1a && b[1] === 0x45 && b[2] === 0xdf && b[3] === 0xa3;
}
function showVideo(src) {
  const v = document.getElementById("v");
  v.preload = "metadata";
  v.src = src;
  canvas.hidden = true;
  note.hidden = true;
  v.hidden = false;
  v.addEventListener("error", () => fail(say("This browser cannot play the video (WebM).", "Selain ei osaa toistaa videota (WebM).")));
  window.__play = { video: v };
  window.__pageStarted = true;
}

async function start() {
  link = playLinkOf(location.pathname);
  if (!link) return fail(say("This address names no presentation.", "Tämä osoite ei ole esitys."));
  let rec;
  try {
    const res = await fetch(recUrl(link), { redirect: "manual" });
    if (res.type === "opaqueredirect") return showVideo(recUrl(link));
    if (!res.ok) throw new Error("HTTP " + res.status);
    const bytes = new Uint8Array(await res.arrayBuffer());
    if (isWebm(bytes)) return showVideo(URL.createObjectURL(new Blob([bytes], { type: "video/webm" })));
    rec = await unpackStream(bytes);
  } catch (e) {
    console.warn(e);
    return fail(say("This presentation is not here, or no longer shared.", "Esitystä ei löydy, tai sitä ei enää jaeta."));
  }
  gl = canvas.getContext("webgl2", { antialias: true, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
  if (!gl) return fail(say("This browser cannot draw the slides (no WebGL).", "Selain ei osaa piirtää dioja (ei WebGL:ää)."));
  const s = rec.s;
  if (s.deckEffects) registerDeckEffects(s.deckEffects);
  const faces = (s.faces || []).filter((f) => Array.isArray(f) && /^[\w-]+\.ttf$/.test(f[1]));
  const got = await Promise.all(faces.map(([name, file]) => loadFace(name, file).catch((e) => {
    console.warn("face not loaded: " + name, e);
    return null;
  })));
  setFontFallback(got.filter(Boolean));
  await Promise.all((s.pictures || []).map(loadPicture));
  nav = new RecNav(rec);
  // #slide=N: that slide as it has come in, not the one before it going
  const first = slideOfHash(location.hash);
  if (first > 0) nav.go(first, false);
  showState();
  note.hidden = true;
  bar.hidden = false;
  wake();
  window.__play = { nav, act };
  if ((s.plays || []).length) await startRecPlays(s).catch((e) => console.warn("programs on slides", e));
  if (s.flight) await offerFlight(s.flight).catch((e) => console.warn("no 3-D flight", e));
  window.__pageStarted = true;
  repaint();
}

start().catch((e) => {
  console.error(e);
  fail(say("The presentation could not be shown.", "Esitystä ei voitu näyttää."));
});
