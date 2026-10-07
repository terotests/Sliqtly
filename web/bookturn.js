// SPDX-License-Identifier: AGPL-3.0-or-later
//
// A page of a realistic book being turned: held by its corner, let go, or
// turning by itself (a key, a click, the next spread chosen elsewhere). The
// geometry is web/book.js's; this is the state between frames, without a
// screen, shared by the shared link's viewer (web/view.js) and the editor's
// presenting (web/main.js), which draw it with web/bookgl.js.
//
// `spreads` is the book's [[left, right], …] (-1 none), `s` the spread lying
// open, W and H a page. Points are in page units from the spine, y down.

import { curl, letGo, turnPath, grabAt } from "./book.js";

/**
 * A turn of the page at spread `s` towards `side` (1 on, -1 back), held at
 * (cx, cy) on its outer edge; null when there is no spread that way.
 */
export function startTurn(spreads, s, side, cx, cy) {
  const to = s + side;
  if (to < 0 || to >= spreads.length) return null;
  const [l, r] = spreads[s];
  const [l2, r2] = spreads[to];
  return side > 0
    ? { side, cx, cy, qx: cx, qy: cy, from: s, to, front: r, back: l2, under: r2, held: null, anim: null }
    : { side, cx, cy, qx: cx, qy: cy, from: s, to, front: l, back: r2, under: l2, held: null, anim: null };
}

/**
 * A turn nobody holds, from the page's bottom corner, over; `now` null:
 * from its first frame (stepTurn).
 */
export function autoTurn(spreads, s, side, W, H, now) {
  const t = startTurn(spreads, s, side, side * W, H);
  if (t) t.anim = { start: now, fx: t.cx, fy: t.cy, over: true, ms: 750 };
  return t;
}

/** The corner a press at (x, y) takes on spread `s`, or null. */
export function grabTurn(spreads, s, x, y, W, H) {
  const g = grabAt(x, y, W, H, s + 1 < spreads.length, s > 0);
  return g ? startTurn(spreads, s, g.side, g.cx, g.cy) : null;
}

/** The held point dragged to (x, y) at time `now` (ms). */
export function dragTurn(t, x, y, now) {
  const h = t.held;
  if (h) {
    const dt = Math.max(1, now - h.t) / 1000;
    h.vx = h.vx * 0.6 + ((x - h.x) / dt) * 0.4;
    h.t = now;
    h.x = x;
  }
  t.qx = x;
  t.qy = y;
}

/**
 * Letting go of a held page: over when past the spine or flicked, else back
 * where it lay; a tap that hardly moved turns it like a click.
 */
export function releaseTurn(t, W, now) {
  const vx = t.held ? t.held.vx : 0;
  const moved = Math.abs(t.qx - t.cx) + Math.abs(t.qy - t.cy);
  t.held = null;
  if (moved < 4) {
    t.anim = { start: now, fx: t.cx, fy: t.cy, over: true, ms: 750 };
    return;
  }
  const over = letGo(t.side, t.qx, vx, W);
  t.anim = { start: now, fx: t.qx, fy: t.qy, over, ms: over ? 420 : 300 };
}

/**
 * The turn moved on to time `now`: "held", "turning", or, once it has
 * landed, "over" (the next spread lies open) or "back" (the same one).
 */
export function stepTurn(t, now, H) {
  if (!t.anim) return "held";
  const a = t.anim;
  // a turn started without a time starts on its first frame
  if (a.start == null) a.start = now;
  const u = Math.min(1, (now - a.start) / a.ms);
  const q = turnPath(t.cx, t.cy, a.fx, a.fy, u, a.over, H);
  t.qx = q.x;
  t.qy = q.y;
  if (u < 1) return "turning";
  return a.over ? "over" : "back";
}

/**
 * What bookgl.js draws for spread `s` with turn `t` (or null): the pages
 * lying still and the leaf.
 */
export function turnScene(spreads, s, t, W, H) {
  const [left, right] = spreads[s];
  if (!t) return { left, right, turn: null };
  return {
    left: t.side < 0 ? -1 : left,
    right: t.side > 0 ? -1 : right,
    turn: { side: t.side, front: t.front, back: t.back, under: t.under, curl: curl(t.cx, t.cy, t.qx, t.qy, W, H) },
  };
}

/** The pages a frame of spread `s` with turn `t` shows. */
export function turnPages(spreads, s, t) {
  const [l, r] = spreads[s];
  const out = [l, r];
  if (t) out.push(t.front, t.back, t.under);
  return [...new Set(out.filter((p) => p >= 0))];
}
