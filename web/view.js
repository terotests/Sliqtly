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

import { prepareDisplayList, setFontFallback, imageChanged } from "./gl/evg-webgl.js";
import { registerDeckEffects, effectStill, reducedMotion, holdStill } from "./fxdeck.js";
import { decodePicture } from "./picture.js";
import { INTRO_MS } from "./brand.js";
import { currentUser, signIn, authHeaders } from "./viewauth.js";
import { bookOf, spreadOfPage, firstPage, spreadLabel, spreadPages, grabAt } from "./book.js";
import { autoTurn, grabTurn, dragTurn, releaseTurn, stepTurn, turnScene, turnPages } from "./bookturn.js";
import { BookGL } from "./bookgl.js";
import { linkOf, slideLink, viewUrl, exportUrl, exportName, picturesOf, lookFacesOf, LOOK_FACES, slideForKey, fitSlide, pinchView, panView, isZoomed } from "./viewlink.js";

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
  ["vExport", "Download the presentation", "Lataa esitys"],
  ["vShare", "Share this slide (copies its link)", "Jaa tämä dia (kopioi sen linkin)"],
]) document.getElementById(id).title = say(en, fiText);
const vShare = document.getElementById("vShare");
const vShareLabel = vShare.querySelector(".lbl");
const SHARE_LABEL = say("Share slide", "Jaa dia");
vShareLabel.textContent = SHARE_LABEL;
vShare.setAttribute("aria-label", SHARE_LABEL);
const vExport = document.getElementById("vExport");
const vMenu = document.getElementById("vMenu");
const EXPORT_LABEL = say("Export ▾", "Vie ▾");
vExport.textContent = EXPORT_LABEL;
vMenu.setAttribute("aria-label", say("Download as", "Lataa muodossa"));
const vCopyMd = document.getElementById("vCopyMd");
const COPY_MD_LABEL = say("Copy Markdown", "Kopioi Markdown");
vCopyMd.firstChild.textContent = COPY_MD_LABEL + " ";
vCopyMd.title = say("Copy the presentation's Markdown to the clipboard", "Kopioi esityksen Markdown leikepöydälle");

// the address, or the one the assistant's preview gives in <meta>
const given = document.querySelector('meta[name="sliqtly-link"]')?.content || "";
const link = linkOf(given ? "" : location.pathname, given ? "" : location.search, location.hash, given);
// the assistant's preview is not at the slides' own address: no slide link
if (given) vShare.hidden = true;

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
      // a note said meanwhile (not found, no WebGL) keeps the screen up
      setTimeout(() => {
        intro.classList.remove("out");
        if (!note.textContent) intro.hidden = true;
      }, 350);
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
// the slide shown; -1 until the intro is over, so no slide's script runs
// (and a page's first look is not spent) behind it
let at = -1;
let shownAt = 0;
let raf = 0;
// the programs on the slides (```app, web/viewplay.js), null when none
let plays = null;
// the slide seen closer by a pinch ({ x, y, scale }), or null: fitted
let zoom = null;

// `mode: book` (web/book.js): the spreads, and with `render: realistic`
// the pages drawn as paper and turned by their corner (web/bookgl.js) on a
// canvas over this one
let book = null;
let bookGl = null;
let turn = null;
const sheet = document.createElement("canvas");
sheet.id = "bookSheet";
sheet.setAttribute("aria-hidden", "true");
sheet.hidden = true;
// what the viewer is as wide as: a page, or a book's two
const viewW = () => (book ? deck.width * 2 : deck.width);
const spreadNow = () => (book ? spreadOfPage(book.spreads, at) : at);
const realistic = () => !!(book && book.render === "realistic" && bookGl && !zoom);

function paint() {
  raf = 0;
  if (!gl || !lists.length || at < 0) return;
  const dpr = Math.min(window.devicePixelRatio || 1, 3);
  const w = canvas.clientWidth;
  const h = canvas.clientHeight;
  if (canvas.width !== Math.round(w * dpr) || canvas.height !== Math.round(h * dpr)) {
    canvas.width = Math.round(w * dpr);
    canvas.height = Math.round(h * dpr);
  }
  gl.viewport(0, 0, canvas.width, canvas.height);
  // a realistic book lies on a table, with room round it for its shadow
  // and the edges of its pages
  const table = realistic();
  if (table) gl.clearColor(0.17, 0.16, 0.15, 1);
  else gl.clearColor(0, 0, 0, 1);
  gl.clear(gl.COLOR_BUFFER_BIT | gl.STENCIL_BUFFER_BIT);
  const pad = table ? Math.round(Math.min(w, h) * 0.06) : 0;
  const fit = fitSlide(w - pad * 2, h - pad * 2, viewW(), deck.height);
  const view = zoom || { x: fit.x + pad, y: fit.y + pad, scale: fit.scale };
  if (table) {
    sheet.hidden = false;
    paintBook(view, dpr);
    return;
  }
  sheet.hidden = true;
  const t = (performance.now() - shownAt) / 1000;
  // a slide, or the pages of a book's spread side by side
  const shown = book ? spreadPages(book.spreads, spreadNow()) : [{ page: at, x: 0 }];
  let moving = false;
  // less motion asked for: every effect held at its still, drawn once
  const still = reducedMotion();
  for (const { page, x } of shown) {
    // a slide's script, while it runs, lays its frames over the list
    const atRest = (plays && plays.listOf(page)) || lists[page];
    const list = plays ? plays.withWorlds(page, atRest) : atRest;
    for (const e of list.effects || []) {
      if (still) holdStill(e);
      else e.time = t;
    }
    moving = moving || (!still && (list.effects || []).length > 0);
    const doc = { width: w, height: h, view: { x: view.x + x * deck.width * view.scale, y: view.y, scale: view.scale }, list };
    const f = prepareDisplayList(gl, doc, { dpr, images: pictures, contrastGuard: true, contrastRepair: true });
    f.draw(null, null, { clear: false });
    f.dispose();
    // its programs' pictures over it (each one's plate until it has one)
    if (plays) for (const pl of plays.listsFor(page, doc.view)) {
      const pf = prepareDisplayList(gl, { ...doc, list: pl }, { dpr, images: pictures });
      pf.draw(null, null, { clear: false });
      pf.dispose();
    }
  }
  // a surface effect moves: drawn again on the next frame
  if (moving) raf = requestAnimationFrame(paint);
}

// --- a realistic book ------------------------------------------------------------
// Each page drawn once by EVG into a picture as sharp as the screen shows
// it, kept by bookGl as a texture.
let pageGl = null;
const pageCanvas = document.createElement("canvas");
function ensurePage(page, pxW) {
  if (page < 0 || page >= lists.length || bookGl.hasPage(page, pxW)) return;
  if (!pageGl) pageGl = pageCanvas.getContext("webgl2", { antialias: true, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
  if (!pageGl) return;
  const k = pxW / deck.width;
  pageCanvas.width = pxW;
  pageCanvas.height = Math.round(deck.height * k);
  const list = lists[page];
  for (const e of list.effects || []) e.time = effectStill(e.kind);
  const f = prepareDisplayList(pageGl, { width: deck.width, height: deck.height, list }, { dpr: k, images: pictures, contrastGuard: true, contrastRepair: true });
  f.draw(null, null);
  f.dispose();
  bookGl.setPage(page, pageCanvas, pxW);
}

// A page as a small picture, its programs left out: the room a program's
// 3-D worlds stand in (web/three3d.js). An ImageData, null without one.
let roomGl = null;
const roomCanvas = document.createElement("canvas");
function pagePicture(page, w, h) {
  if (page < 0 || page >= lists.length) return null;
  roomCanvas.width = w;
  roomCanvas.height = h;
  if (!roomGl) roomGl = roomCanvas.getContext("webgl2", { antialias: true, premultipliedAlpha: false, stencil: true, preserveDrawingBuffer: true });
  if (!roomGl) return null;
  const list = lists[page];
  for (const e of list.effects || []) e.time = effectStill(e.kind);
  const images = new Map([...pictures].filter(([src]) => !src.startsWith("three:")));
  const f = prepareDisplayList(roomGl, { width: deck.width, height: deck.height, list }, { dpr: Math.min(w / deck.width, h / deck.height), images });
  f.draw(null, null);
  f.dispose();
  const c = document.createElement("canvas");
  c.width = w;
  c.height = h;
  const g = c.getContext("2d", { willReadFrequently: true });
  g.drawImage(roomCanvas, 0, 0);
  return g.getImageData(0, 0, w, h);
}

let place = null;
function paintBook(view, dpr) {
  const s = spreadNow();
  place = { spineX: view.x + deck.width * view.scale, top: view.y, scale: view.scale };
  const pxW = Math.min(2048, Math.max(256, Math.round(deck.width * view.scale * dpr)));
  for (const p of turnPages(book.spreads, s, turn)) ensurePage(p, pxW);
  if (turn) {
    const state = stepTurn(turn, performance.now(), deck.height);
    if (state === "over" || state === "back") {
      const to = turn.to;
      turn = null;
      if (state === "over") go(firstPage(book.spreads, to));
      else repaint();
      return paintBook(view, dpr);
    }
  }
  const scene = turnScene(book.spreads, s, turn, deck.width, deck.height);
  bookGl.draw({ place, W: deck.width, H: deck.height, dpr, ...scene });
  if (turn && turn.anim) raf = requestAnimationFrame(paint);
}

// A turn nobody holds: a key, a click, the buttons.
function turnBy(side) {
  if (turn) return;
  turn = autoTurn(book.spreads, spreadNow(), side, deck.width, deck.height, null);
  if (turn) repaint();
}

// The next or previous spread, or slide: turned in a realistic book.
function step(by) {
  if (realistic()) {
    turnBy(by);
    return;
  }
  if (book) go(firstPage(book.spreads, Math.max(0, Math.min(book.spreads.length - 1, spreadNow() + by))));
  else go(at + by);
}
function repaint() {
  if (!raf) raf = requestAnimationFrame(paint);
}
window.addEventListener("resize", () => {
  zoom = null;
  repaint();
});

// --- the way round ---------------------------------------------------------------
function go(i) {
  const n = lists.length;
  if (!n) return;
  i = Math.max(0, Math.min(n - 1, i));
  // a book is shown a spread at a time, from the spread's first page
  if (book) i = firstPage(book.spreads, spreadOfPage(book.spreads, i));
  if (i !== at) {
    shownAt = performance.now();
    zoom = null;
  }
  at = i;
  vCount.textContent = book ? spreadLabel(book.spreads, spreadNow(), n) : (at + 1) + " / " + n;
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
    if (vGo.hidden && vMenu.hidden) document.body.classList.add("idle");
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
document.getElementById("vPrev").addEventListener("click", () => step(-1));
document.getElementById("vNext").addEventListener("click", () => step(1));
document.getElementById("vFull").addEventListener("click", () => {
  if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
  else document.documentElement.requestFullscreen?.().catch(() => {});
});

// Share slide: the link to the slide shown, which opens at that slide and
// whose preview in a chat app (Slack, Teams) is that slide. The phone's
// share sheet where there is one, else copied.
vShare.addEventListener("click", async () => {
  if (!link?.id) return;
  const url = slideLink(location.origin, link, at);
  let said = say("Link copied", "Linkki kopioitu");
  try {
    if (navigator.share && matchMedia("(pointer: coarse)").matches) {
      await navigator.share({ url, title: deck?.name || "Sliqtly" });
      return;
    }
    await navigator.clipboard.writeText(url);
  } catch (e) {
    if (e?.name === "AbortError") return;
    window.prompt(say("The slide's link:", "Dian linkki:"), url);
    said = SHARE_LABEL;
  }
  // said on the button (its label, or ✓ where it shows the mark only)
  // and to a screen reader
  const mark = vShare.querySelector(".icon");
  vShareLabel.textContent = said;
  vShare.setAttribute("aria-label", said);
  if (said !== SHARE_LABEL) mark.textContent = "✓";
  setTimeout(() => {
    mark.textContent = "⤴\uFE0E";
    vShareLabel.textContent = SHARE_LABEL;
    vShare.setAttribute("aria-label", SHARE_LABEL);
  }, 2000);
});

// Export ▾: the deck as a PDF, a PowerPoint file or its Markdown, made on
// the server (GET /api/export/{id}/{format}) and saved under the deck's name;
// Copy Markdown puts that same Markdown on the clipboard
function openMenu(open) {
  vMenu.hidden = !open;
  vExport.setAttribute("aria-expanded", String(open));
  if (open) vMenu.querySelector("button")?.focus();
}
vExport.addEventListener("click", () => openMenu(vMenu.hidden));
document.addEventListener("pointerdown", (ev) => {
  if (!vMenu.hidden && !ev.target.closest?.("#vMenu, #vExport")) openMenu(false);
});
vMenu.addEventListener("keydown", (ev) => {
  ev.stopPropagation();
  const items = [...vMenu.querySelectorAll("button")];
  const i = items.indexOf(document.activeElement);
  if (ev.key === "Escape") {
    openMenu(false);
    vExport.focus();
  } else if (ev.key === "ArrowDown" || ev.key === "ArrowUp") {
    ev.preventDefault();
    items[(i + (ev.key === "ArrowDown" ? 1 : items.length - 1)) % items.length].focus();
  }
});
for (const b of vMenu.querySelectorAll("button[data-format]")) b.addEventListener("click", () => download(b.dataset.format));
vCopyMd.addEventListener("click", copyMarkdown);

async function fetchExport(format) {
  const res = await fetch(exportUrl(link, format), { headers: await authHeaders(user) });
  if (!res.ok) {
    const why = await res.json().catch(() => null);
    throw new Error((why && why.error) || "HTTP " + res.status);
  }
  return res.blob();
}

// said on the Export button for a while, then its own label again
function sayOnExport(text, ms) {
  vExport.textContent = text;
  setTimeout(() => { if (vExport.textContent === text) vExport.textContent = EXPORT_LABEL; }, ms);
}

// The clipboard is written while the click still counts as the user's
// (Safari refuses a write after an await): a ClipboardItem given the
// fetch's promise, else writeText once the text is here.
async function copyMarkdown() {
  openMenu(false);
  if (!link?.id || vExport.getAttribute("aria-busy") === "true") return;
  vExport.setAttribute("aria-busy", "true");
  vExport.textContent = say("Copying…", "Kopioidaan…");
  const text = fetchExport("md").then((b) => b.text());
  let said = say("Markdown copied ✓", "Markdown kopioitu ✓");
  try {
    if (window.ClipboardItem && navigator.clipboard?.write) {
      const blob = text.then((t) => new Blob([t], { type: "text/plain" }));
      try {
        await navigator.clipboard.write([new ClipboardItem({ "text/plain": blob })]);
      } catch (e) {
        // a failed fetch rethrows here and is said below; a refused write
        // tries writeText
        await navigator.clipboard.writeText(await text);
      }
    } else {
      await navigator.clipboard.writeText(await text);
    }
  } catch (e) {
    console.warn("copy md", e);
    said = say("Copy failed, try again ▾", "Kopiointi epäonnistui, yritä uudelleen ▾");
  } finally {
    vExport.removeAttribute("aria-busy");
    sayOnExport(said, said.endsWith("✓") ? 2000 : 6000);
  }
}

async function download(format) {
  openMenu(false);
  if (!link?.id || vExport.getAttribute("aria-busy") === "true") return;
  vExport.setAttribute("aria-busy", "true");
  vExport.textContent = say("Exporting…", "Viedään…");
  let failed = "";
  try {
    const blob = await fetchExport(format);
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = exportName(deck?.name, format);
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 30000);
  } catch (e) {
    console.warn("export " + format, e);
    failed = say("Download failed, try again ▾", "Lataus epäonnistui, yritä uudelleen ▾");
  } finally {
    vExport.removeAttribute("aria-busy");
    if (failed) sayOnExport(failed, 6000);
    else vExport.textContent = EXPORT_LABEL;
  }
}

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
  if (book) {
    const units = book.spreads.length;
    const s = spreadNow();
    const k = slideForKey(ev.key, s, units);
    if (k < 0) return;
    ev.preventDefault();
    if (k === s + 1 || k === s - 1) step(k - s);
    else if (k !== s) go(firstPage(book.spreads, k));
    return;
  }
  const to = slideForKey(ev.key, at, lists.length);
  if (to < 0) return;
  ev.preventDefault();
  go(to);
});

// a click or a tap: on the left third back, elsewhere on; a swipe sideways.
// Two fingers are a pinch: the slide is seen closer (or further) and moved
// with them, and lifting them goes nowhere; while it is closer one finger
// drags it about. The page itself does not zoom (touch-action: none), as
// the slide is painted for the window and a page zoom would blur it.
let down = null;
const fingers = new Map();
let pinch = null;
function fingerSpan() {
  const [a, b] = [...fingers.values()];
  return { d: Math.hypot(a.x - b.x, a.y - b.y) || 1, mx: (a.x + b.x) / 2, my: (a.y + b.y) / 2 };
}
// the page of a realistic book under a pointer, in page units from the spine
const onPage = (ev) => ({ x: (ev.clientX - place.spineX) / place.scale, y: (ev.clientY - place.top) / place.scale });
canvas.addEventListener("pointerdown", (ev) => {
  if (!realistic() || turn || !place) return;
  const p = onPage(ev);
  turn = grabTurn(book.spreads, spreadNow(), p.x, p.y, deck.width, deck.height);
  if (!turn) return;
  turn.held = { id: ev.pointerId, t: performance.now(), x: p.x, vx: 0 };
  canvas.setPointerCapture(ev.pointerId);
  // the slide's own press (a tap, a swipe, a pinch) does not see it
  ev.stopImmediatePropagation();
  repaint();
});
canvas.addEventListener("pointermove", (ev) => {
  // a page's corner can be taken: the hand says so
  if (!turn && realistic() && place && ev.pointerType === "mouse") {
    const p = onPage(ev);
    const s = spreadNow();
    canvas.style.cursor = grabAt(p.x, p.y, deck.width, deck.height, s + 1 < book.spreads.length, s > 0) ? "grab" : "";
  }
  if (!turn || !turn.held || turn.held.id !== ev.pointerId) return;
  canvas.style.cursor = "grabbing";
  ev.stopImmediatePropagation();
  const p = onPage(ev);
  dragTurn(turn, p.x, p.y, performance.now());
  repaint();
});
const letHold = (ev) => {
  if (!turn || !turn.held || turn.held.id !== ev.pointerId) return;
  ev.stopImmediatePropagation();
  canvas.style.cursor = "";
  releaseTurn(turn, deck.width, performance.now());
  repaint();
};
canvas.addEventListener("pointerup", letHold);
canvas.addEventListener("pointercancel", letHold);
canvas.addEventListener("pointerdown", (ev) => {
  down = { x: ev.clientX, y: ev.clientY, lx: ev.clientX, ly: ev.clientY };
  if (ev.pointerType === "mouse") return;
  fingers.set(ev.pointerId, { x: ev.clientX, y: ev.clientY });
  if (fingers.size === 2 && lists.length) {
    pinch = fingerSpan();
    down = null;
  }
});
canvas.addEventListener("pointermove", (ev) => {
  if (fingers.has(ev.pointerId)) fingers.set(ev.pointerId, { x: ev.clientX, y: ev.clientY });
  const w = canvas.clientWidth;
  const h = canvas.clientHeight;
  if (pinch) {
    if (fingers.size < 2) return;
    const p = fingerSpan();
    zoom = pinchView(zoom || fitSlide(w, h, viewW(), deck.height), w, h, viewW(), deck.height, p.d / pinch.d, p.mx, p.my, p.mx - pinch.mx, p.my - pinch.my);
    pinch = p;
    repaint();
  } else if (down && zoom) {
    zoom = panView(zoom, w, h, viewW(), deck.height, ev.clientX - down.lx, ev.clientY - down.ly);
    down.lx = ev.clientX;
    down.ly = ev.clientY;
    repaint();
  }
});
function lift(ev) {
  fingers.delete(ev.pointerId);
  if (pinch && fingers.size < 2) {
    pinch = null;
    // pinched back out to the fitted slide: fitted again
    if (!isZoomed(zoom, canvas.clientWidth, canvas.clientHeight, viewW(), deck.height)) zoom = null;
  }
}
canvas.addEventListener("pointercancel", (ev) => {
  lift(ev);
  down = null;
});
canvas.addEventListener("pointerup", (ev) => {
  lift(ev);
  if (!down || !lists.length) return;
  const dx = ev.clientX - down.x;
  const dy = ev.clientY - down.y;
  down = null;
  if (Math.abs(dx) > 40 && Math.abs(dx) > Math.abs(dy) && !zoom) step(dx < 0 ? 1 : -1);
  else if (Math.abs(dx) < 10 && Math.abs(dy) < 10) step(ev.clientX < canvas.clientWidth / 3 ? -1 : 1);
});
// iOS Safari zooms the page on a pinch despite touch-action: not over the slide
canvas.addEventListener("touchmove", (ev) => ev.preventDefault(), { passive: false });
for (const g of ["gesturestart", "gesturechange", "gestureend"]) canvas.addEventListener(g, (ev) => ev.preventDefault());

// --- start ---------------------------------------------------------------------
// A file of the shared deck as text (a world's .gltf), null when it has none.
async function deckFileText(d, path) {
  const f = (d.files || []).find((x) => x.path === String(path).replace(/^\/+/, ""));
  if (!f || !f.url) return null;
  const res = await fetch(f.url);
  if (!res.ok) throw new Error("HTTP " + res.status);
  return res.text();
}

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

// who is signed in on the page, when a private presentation needed it
let user = null;

function notFound() {
  if (given) {
    showNote(say("This shared presentation was not found.", "Jaettua esitystä ei löytynyt."));
    return;
  }
  const esc = (t) => String(t).replace(/[&<>"]/g, (c) => "&#" + c.charCodeAt(0) + ";");
  const who = user ? esc(user.email || user.displayName || "") : "";
  showNote(say("This presentation was not found, or it is private.", "Esitystä ei löytynyt, tai se on yksityinen.") + "<br>" +
    (user
      ? say(`Signed in as ${who}, which does not own it.`, `Kirjautuneena ${who}, joka ei omista sitä.`)
      : say("If it is yours, sign in with the Google account that owns it.", "Jos se on sinun, kirjaudu sen omistavalla Google-tilillä.")) +
    `<br><button id="vSignIn" type="button">${user ? say("Use another Google account", "Käytä toista Google-tiliä") : say("Sign in with Google", "Kirjaudu Googlella")}</button>`);
  document.getElementById("vSignIn").addEventListener("click", async (ev) => {
    ev.stopPropagation();
    try {
      await signIn(!!user);
      location.reload();
    } catch (e) {
      console.warn("sign-in", e);
      if (e?.code !== "auth/popup-closed-by-user") note.insertAdjacentText("beforeend", " " + say("Sign-in did not go through.", "Kirjautuminen ei onnistunut."));
    }
  });
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
    let res = await fetch(viewUrl(link));
    // not found, or private: shown to its owner signed in here (viewauth.js)
    if (res.status === 404 && !given) {
      user = await currentUser();
      if (user) res = await fetch(viewUrl(link), { headers: await authHeaders(user) });
    }
    if (res.status === 404) {
      await shown;
      notFound();
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
  book = bookOf(deck);
  if (book && book.render === "realistic") {
    try {
      canvas.after(sheet);
      bookGl = new BookGL(sheet);
    } catch (e) {
      // no second WebGL context: the book is shown flat
      console.warn("realistic book", e);
      bookGl = null;
    }
  }
  if (deck.name) document.title = deck.name + " · Sliqtly";
  const looks = lookFacesOf(got.lists).map((name) => [name, LOOK_FACES[name]]);
  await Promise.all([fonts.then(() => looks.length && loadFaces(looks)), ...picturesOf(deck).map(pictureOf)]);
  lists = got.lists;
  // the deck's own effects, compiled on the server from its ```fx blocks
  if (got.effects) registerDeckEffects(JSON.stringify(got.effects));
  // a program (or a slide's script) runs in the page; its engine and the
  // little it is painted with are loaded only for a deck that has one
  if (((deck.plays || []).length || (got.scripts || []).length) && !realistic()) {
    try {
      const { startPlays } = await import("./viewplay.js");
      plays = await startPlays({
        plays: deck.plays || [], scripts: got.scripts || [], lists, slideW: deck.width, slideH: deck.height, canvas, current: () => at, count: () => lists.length,
        shownPages: () => (book ? spreadPages(book.spreads, spreadNow()).map((p) => p.page) : [at]),
        go: (i) => go(i), repaint,
        pictures, gl: () => gl, dpr: () => Math.min(window.devicePixelRatio || 1, 3), imageChanged, slidePicture: pagePicture,
        readFile: (path) => deckFileText(deck, path),
      });
    } catch (e) {
      console.warn("programs on slides", e);
    }
  }
  await shown;
  bar.hidden = false;
  shownAt = performance.now();
  at = -1;
  go(Math.min(link.slide, lists.length - 1));
  wake();
  started();
}
start();
