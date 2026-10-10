// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Programs on slides (```app) in the public viewer. The server leaves each
// program's box empty and sends its source, stylesheet and box
// (mcp-go/rgr/View.rgr plays); this runs them with the editor's own page
// half (web/apps.js: a CErXes Worker per program, frames, keys, the
// watchdog) and paints each one's picture, or its plate with why it is not
// running, with PresPlayWeb (src/PresPlayWeb.rgr, web/dist-view/pres_play.js).
//
// A slide's script ({script=…}) runs the same way while its slide is shown:
// the server sends the slide as the Markdown has it with its entities
// (View.scripts), and each frame is laid over it by PresScriptWeb
// (src/PresScriptWeb.rgr), the editor's own PresScriptApply. The list the
// server sent shows where the script ends, before it runs and after.
//
// What a program may ask of the deck: moving between slides (allow:
// slide.nav). deck.set and el() change the deck's layout, which the viewer
// does not have: they work in the editor and are said once in the console.
// A program with `allow: 3d` draws its <scene3d> worlds with the editor's
// web/three3d.js, into pictures the viewer paints with the slide.
import { createApps } from "./apps.js";
import { createThree3d } from "./three3d.js";
import { loadScript } from "./sitescript.js";

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
 * plays: the server's programs; scripts: its slides' scripts; lists: the
 * slides' lists at rest; canvas: the viewer's; current() the slide shown,
 * count() how many; shownPages() the pages on screen; go(i) to a slide;
 * repaint() when a picture changed. For 3-D worlds: pictures, the viewer's
 * src → picture map; gl() its WebGL context; dpr() its device pixel ratio;
 * imageChanged(gl, src) the painter's; slidePicture(page, w, h) a page as
 * an ImageData without its programs.
 */
export async function startPlays({ plays = [], scripts = [], lists = [], canvas, current, count, shownPages, go, repaint, pictures, gl, dpr, imageChanged, slidePicture, readFile }) {
  await loadScript("pres_play.js");
  const web = new globalThis.PresPlayWeb();
  const scriptWeb = new globalThis.PresScriptWeb();
  const byKey = new Map();
  // a script's slide while it runs: its list with the last frame laid over
  const live = new Map();
  for (const p of plays) {
    const [x, y, w, h] = p.box;
    web.add(p.key, p.src, p.w, p.h, x, y, w, h);
    web.setCss(p.key, p.cssText || "");
    web.setAllow3d(p.key, (p.allow || []).includes("3d"));
    byKey.set(p.key, p);
  }
  for (const s of scripts) {
    const b = s.base || {};
    scriptWeb.add(s.key, JSON.stringify(b.list || {}), JSON.stringify(b.scene || {}), b.ink || "", b.accent || "");
    byKey.set(s.key, { ...s, script: true, w: 0, h: 0, css: "" });
    // how it opens, worked out on the server (View.scriptOpen): the slide's
    // first paint already shows it, not where the script ends
    if (s.open && scriptWeb.setFrame(s.key, JSON.stringify(s.open))) live.set(s.slide, s.key);
  }
  // where each page was painted last, for the pointer
  const placed = new Map();
  // the slide shown before this one (1-based, 0 for none): onEnter(from)
  let seenPage = -1;
  let cameFrom = 0;
  let focus = "";
  const notes = [];
  const said = new Set();
  const note = (line) => {
    if (said.has(line)) return;
    said.add(line);
    notes.push(line);
  };

  const app = {
    playsJson: () => JSON.stringify([
      ...plays.map((p) => ({ key: p.key, slide: p.slide, src: p.src, css: p.css, w: p.w, h: p.h, allow: p.allow || [], loaded: true })),
      ...scripts.map((s) => ({ key: s.key, slide: s.slide, src: s.src, css: "", w: 0, h: 0, allow: s.allow || [], loaded: true, script: true })),
    ]),
    playSource: (key) => byKey.get(key)?.text || "",
    playsShown: () => {
      const pages = shownPages();
      return [...plays, ...scripts].filter((p) => pages.includes(p.slide)).map((p) => p.key).join("\n");
    },
    setPlayFrame: (key, tree) => web.setTree(key, tree),
    playState: (key) => {
      const p = byKey.get(key);
      const step = p && p.script ? p.steps || 0 : 0;
      return JSON.stringify({ home: p ? p.slide + 1 : 1, slide: current() + 1, slides: count(), step, from: cameFrom, mode: "present", focused: focus === key, data: {} });
    },
    // a slide's script (web/apps.js): the tree it is handed, its frames
    // laid over its slide, and back to the list at rest when it stops. Where
    // it ends came with the lists (no setScriptFinal: nothing to work out).
    scriptTree: (key) => {
      const p = byKey.get(key);
      return p && p.tree ? JSON.stringify(p.tree) : "";
    },
    setScriptFrame: (key, json) => {
      const p = byKey.get(key);
      if (!p || !scriptWeb.setFrame(key, json)) return false;
      live.set(p.slide, key);
      repaint();
      return true;
    },
    // the run stopped (its slide left): the slide comes back as it opens
    endScriptLive: (key) => {
      const p = byKey.get(key);
      if (!p || live.get(p.slide) !== key) return;
      if (!p.open || !scriptWeb.setFrame(key, JSON.stringify(p.open))) live.delete(p.slide);
    },
    // how the script opens (web/apps.js works it out): the slide as it
    // arrives, until the script's first frame
    setScriptOpen: (key, json) => {
      const p = byKey.get(key);
      if (!p || !scriptWeb.setFrame(key, json)) return false;
      live.set(p.slide, key);
      repaint();
      return true;
    },
    // the viewer shows every build step
    scriptOpenStep: (key) => byKey.get(key)?.steps || 0,
    scriptOwnsClick: (key) => scriptWeb.ownsClick(key),
    scriptHit: (key, cx, cy) => {
      const p = byKey.get(key);
      if (!p || !shownPages().includes(p.slide)) return "";
      const at = slidePoint(placed.get(p.slide), canvas.getBoundingClientRect(), cx, cy);
      if (!at) return "";
      return JSON.stringify({ x: at.x, y: at.y, id: scriptWeb.entityAt(key, at.x, at.y) });
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
  // the worlds on the slide shown, placed in the slide's units
  const worlds = plays.some((p) => (p.allow || []).includes("3d")) ? createThree3d({
    app: {
      playScenesJson: () => {
        const page = current();
        const out = [];
        for (const p of plays) if (p.slide === page) out.push(...JSON.parse(web.scenesJson(p.key)));
        return JSON.stringify(out);
      },
      selectedSlide: current,
    },
    pictures,
    imageChanged,
    slidePicture: (w, h) => slidePicture(current(), w, h),
    scale: () => placed.get(current())?.scale || 1,
    repaint,
    toast: (line) => console.warn(line),
    readFile,
  }) : null;
  const loop = () => {
    apps.tick(1);
    if (worlds && worlds.tick(0, gl(), dpr())) repaint();
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
    // page's list while its script runs (the list at rest with the frame
    // laid over its commands), else null
    listOf(page) {
      const key = live.get(page);
      if (!key || !lists[page]) return null;
      const { cmds } = JSON.parse(scriptWeb.listJson(key));
      return { ...lists[page], cmds };
    },
    // page painted at {x, y, scale} (CSS px of the canvas): its programs'
    // pictures as display lists in the slide's units
    listsFor(page, view) {
      placed.set(page, view);
      if (page === current() && page !== seenPage) {
        cameFrom = seenPage + 1;
        seenPage = page;
      }
      const out = [];
      for (const p of plays) if (p.slide === page) out.push(JSON.parse(web.paintJson(p.key)));
      return out;
    },
  };
}
