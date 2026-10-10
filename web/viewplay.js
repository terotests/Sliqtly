// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Programs on slides (```app) in the public viewer. The server leaves each
// program's box empty and sends its source, stylesheet and box
// (mcp-go/rgr/View.rgr plays); this runs them with the editor's own page
// half (web/apps.js: a CErXes Worker per program, frames, keys, the
// watchdog) and paints each one's picture, or its plate with why it is not
// running, with PresPlayWeb (src/PresPlayWeb.rgr, web/dist-view/pres_play.js).
//
// A program with allow: 3d draws its <scene3d> worlds as the editor does
// (web/three3d.js, pres_3d.js loaded the first time one is shown).
//
// What a program may ask of the deck: moving between slides (allow:
// slide.nav). deck.set and el() change the deck's layout, which the viewer
// does not have: they work in the editor and are said once in the console.
import { createApps } from "./apps.js";

function loadScript(url) {
  return new Promise((resolve, reject) => {
    const s = document.createElement("script");
    s.src = url;
    s.onload = resolve;
    s.onerror = () => reject(new Error("could not load " + url));
    document.head.appendChild(s);
  });
}

// The point a window position is on page `page`, given where the viewer
// painted it ({x, y, scale}, CSS px of the canvas): slide units.
export function slidePoint(placed, rect, clientX, clientY) {
  if (!placed) return null;
  return { x: (clientX - rect.left - placed.x) / placed.scale, y: (clientY - rect.top - placed.y) / placed.scale };
}

// What the viewer does with a program's asks: moves when its fence allows
// slide.nav, a line for the rest. { moves: [{k, n}], notes: [line] }
export function viewerAsks(play, asks) {
  const moves = [];
  const notes = [];
  const nav = (play.allow || []).includes("slide.nav");
  for (const a of asks) {
    if (!a || typeof a.k !== "string") continue;
    if (a.k.startsWith("slide.")) {
      if (nav) moves.push(a);
      else notes.push(play.src + ": " + a.k + " asked, but the app block has no allow: slide.nav");
    } else {
      notes.push(play.src + ": " + a.k + " changes the slides, which the viewer shows as saved (it works in the editor)");
    }
  }
  return { moves, notes };
}

/**
 * plays: the server's list; canvas: the viewer's; current() the slide shown,
 * count() how many; shownPages() the pages on screen; go(i) to a slide;
 * repaint() when a picture changed; frame() once a frame after the
 * programs' frames came in.
 */
export async function startPlays({ plays, canvas, current, count, shownPages, go, repaint, frame = () => {} }) {
  const stamp = new URL(import.meta.url).search;
  await loadScript(new URL("./pres_play.js" + stamp, import.meta.url).href);
  const web = new globalThis.PresPlayWeb();
  const byKey = new Map();
  for (const p of plays) {
    const [x, y, w, h] = p.box;
    web.add(p.key, p.src, p.w, p.h, x, y, w, h);
    web.setCss(p.key, p.cssText || "");
    web.setAllow3d(p.key, (p.allow || []).includes("3d"));
    byKey.set(p.key, p);
  }
  // where each page was painted last, for the pointer
  const placed = new Map();
  let focus = "";
  const notes = [];
  const said = new Set();
  const note = (line) => {
    if (said.has(line)) return;
    said.add(line);
    notes.push(line);
  };

  const app = {
    playsJson: () => JSON.stringify(plays.map((p) => ({ key: p.key, slide: p.slide, src: p.src, css: p.css, w: p.w, h: p.h, allow: p.allow || [], loaded: true }))),
    playSource: (key) => byKey.get(key)?.text || "",
    playsShown: () => {
      const pages = shownPages();
      return plays.filter((p) => pages.includes(p.slide)).map((p) => p.key).join("\n");
    },
    setPlayFrame: (key, tree) => web.setTree(key, tree),
    playState: (key) => {
      const p = byKey.get(key);
      return JSON.stringify({ home: p ? p.slide + 1 : 1, slide: current() + 1, slides: count(), step: 0, mode: "present", focused: focus === key, data: {} });
    },
    playAsks: (key, json) => {
      const p = byKey.get(key);
      let asks = [];
      try { asks = JSON.parse(json); } catch (_) { /* nothing asked */ }
      if (!p || !asks.length) return false;
      const { moves, notes: lines } = viewerAsks(p, asks);
      for (const line of lines) note(line);
      for (const a of moves) {
        if (a.k === "slide.next" || a.k === "slide.step") go(current() + 1);
        else if (a.k === "slide.prev") go(current() - 1);
        else if (a.k === "slide.go") go((a.n | 0) - 1);
      }
      return moves.length > 0;
    },
    playNotes: () => notes.splice(0).join("\n"),
    playAt: (cx, cy) => {
      const rect = canvas.getBoundingClientRect();
      for (const p of plays) {
        const at = slidePoint(placed.get(p.slide), rect, cx, cy);
        if (at && web.point(p.key, at.x, at.y)) return p.key;
      }
      return "";
    },
    playPoint: (key, cx, cy) => {
      const p = byKey.get(key);
      const at = p && slidePoint(placed.get(p.slide), canvas.getBoundingClientRect(), cx, cy);
      return at ? web.point(key, at.x, at.y) : "";
    },
    setPlayFocus: (key) => { focus = key; },
    playFocus: () => focus,
    setPlayStopped: (key, why) => web.setStopped(key, why),
  };

  // the viewer has no toasts: what went wrong is on the plate and here
  const apps = createApps({ app, repaint, toast: (line) => console.warn(line) });
  const loop = () => {
    apps.tick(1);
    frame();
    requestAnimationFrame(loop);
  };
  requestAnimationFrame(loop);

  // a press on a program's picture is the program's (and gives it the
  // keyboard), not the viewer's next slide
  window.addEventListener("pointerdown", (ev) => {
    if (ev.target !== canvas) return;
    if (apps.pointerDown(ev.clientX, ev.clientY, true)) {
      ev.preventDefault();
      ev.stopImmediatePropagation();
    }
  }, true);
  window.addEventListener("pointermove", (ev) => apps.pointerMove(ev.clientX, ev.clientY));
  window.addEventListener("pointerup", () => apps.pointerUp());

  return {
    // the worlds the programs on the pages shown drew in their last frames:
    // [{src, scene, x, y, w, h}] in their slide's units
    scenesJson() {
      const pages = shownPages();
      const out = [];
      for (const p of plays) if (pages.includes(p.slide)) out.push(...JSON.parse(web.scenesJson(p.key)));
      return JSON.stringify(out);
    },
    // page painted at {x, y, scale} (CSS px of the canvas): its programs'
    // pictures as display lists in the slide's units
    listsFor(page, view) {
      placed.set(page, view);
      const out = [];
      for (const p of plays) if (p.slide === page) out.push(JSON.parse(web.paintJson(p.key)));
      return out;
    },
  };
}
