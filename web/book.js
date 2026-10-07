// SPDX-License-Identifier: AGPL-3.0-or-later
//
// A deck with `mode: book` in a player: spreads, and a page turned by its
// corner. No DOM and no GL here, so all of it runs under node --test
// (web/test/book.test.mjs); web/bookgl.js draws what this works out.
//
// Which pages face each other is the deck's (RangerMarkdown MdBook): the
// server sends the spreads as [left, right] page pairs, -1 for none
// (mcp-go/rgr/View.rgr), and this only reads them.
//
// THE TURN. A spread has its spine at x = 0, the left page on [-W, 0] and
// the right on [0, W], y down from 0 to H, all in page units. A page held at
// a point C on its outer edge and dragged to Q curls round a cylinder: the
// part of the leaf past the fold line wraps a cylinder of radius R and lies
// flat again, face down, beyond it. The fold is perpendicular to C→Q and
// placed so the held point lands under the pointer. Nothing tears at the
// spine: Q is held where both ends of the spine stay on the flat side, and R
// shrinks where the cylinder would reach them.

/** The book of a viewer's deck, or null for a deck of slides. */
export function bookOf(deck) {
  const b = deck && deck.book;
  if (!b || !Array.isArray(b.spreads) || !b.spreads.length) return null;
  return { render: b.render === "realistic" ? "realistic" : "flat", spreads: b.spreads };
}

/** The spread page `page` is on (0 when it is on none). */
export function spreadOfPage(spreads, page) {
  const i = spreads.findIndex(([l, r]) => l === page || r === page);
  return i < 0 ? 0 : i;
}

/** The page going to spread `s` lands on: its left page, else its right. */
export function firstPage(spreads, s) {
  const [l, r] = spreads[Math.max(0, Math.min(spreads.length - 1, s))];
  return l >= 0 ? l : r;
}

/** "2–3 / 12", or "1 / 12" for a page alone. */
export function spreadLabel(spreads, s, n) {
  const [l, r] = spreads[s] || [-1, -1];
  if (l >= 0 && r >= 0) return `${l + 1}–${r + 1} / ${n}`;
  return `${(l >= 0 ? l : r) + 1} / ${n}`;
}

/** The spread's pages and where each starts, in page widths from the spread's left edge. */
export function spreadPages(spreads, s) {
  const [l, r] = spreads[s] || [-1, -1];
  const out = [];
  if (l >= 0) out.push({ page: l, x: 0 });
  if (r >= 0) out.push({ page: r, x: 1 });
  return out;
}

// --- the turn -----------------------------------------------------------------

const len = (x, y) => Math.hypot(x, y);

/**
 * The point a press at (x, y) holds, or null: on the outer third of a page
 * that has another spread to turn to. `side` 1 turns forward (the right
 * page), -1 back (the left page). The held point is on the page's outer
 * edge, at the height of the press.
 */
export function grabAt(x, y, W, H, canNext, canPrev) {
  if (y < 0 || y > H) return null;
  if (canNext && x > W * (2 / 3) && x <= W) return { side: 1, cx: W, cy: y };
  if (canPrev && x < -W * (2 / 3) && x >= -W) return { side: -1, cx: -W, cy: y };
  return null;
}

/**
 * Q held where the leaf can reach: each end of the spine nearer Q than the
 * held point C (or equally near), so neither is past the fold.
 */
export function clampPointer(cx, cy, qx, qy, H) {
  let x = qx;
  let y = qy;
  for (const ey of [0, H]) {
    const reach = len(cx, cy - ey);
    const d = len(x, y - ey);
    if (d > reach) {
      x = (x / d) * reach;
      y = ey + ((y - ey) / d) * reach;
    }
  }
  return { x, y };
}

/**
 * The curl for C held and dragged to Q: { dx, dy } the unit direction from the
 * fold towards C, (px, py) a point on the fold line, R the cylinder's radius,
 * and progress 0 (flat) … 1 (turned over).
 */
export function curl(cx, cy, qx, qy, W, H) {
  const q = clampPointer(cx, cy, qx, qy, H);
  const D = len(cx - q.x, cy - q.y);
  if (D < 1e-6) {
    const dx = Math.sign(cx) || 1;
    return { dx, dy: 0, px: cx, py: cy, R: 0, qx: q.x, qy: q.y, progress: 0 };
  }
  const dx = (cx - q.x) / D;
  const dy = (cy - q.y) / D;
  // a tighter roll at first, opening as the page comes over, never more than
  // a tenth of the page
  let R = Math.min(W * 0.1, D * 0.18);
  // neither end of the spine may reach the cylinder
  for (const ey of [0, H]) {
    const along = (0 - cx) * dx + (ey - cy) * dy;
    R = Math.min(R, Math.max(0, (-2 * along - D) / Math.PI));
  }
  const s = (D + Math.PI * R) / 2;
  return { dx, dy, px: cx - dx * s, py: cy - dy * s, R, qx: q.x, qy: q.y, progress: Math.min(1, D / (2 * W)) };
}

/**
 * Where a point (x, y) of the turning leaf goes under the curl `c`:
 * [x, y, z, face], z towards the reader and face 1 on the front, -1 once it
 * is over (face down).
 */
export function bend(x, y, c) {
  const s = (x - c.px) * c.dx + (y - c.py) * c.dy;
  if (s <= 0) return [x, y, 0, 1];
  const bx = x - c.dx * s;
  const by = y - c.dy * s;
  const arc = Math.PI * c.R;
  if (s < arc && c.R > 0) {
    const a = s / c.R;
    const t = c.R * Math.sin(a);
    return [bx + c.dx * t, by + c.dy * t, c.R * (1 - Math.cos(a)), a < Math.PI / 2 ? 1 : -1];
  }
  const back = s - arc;
  return [bx - c.dx * back, by - c.dy * back, 2 * c.R, -1];
}

/**
 * Letting go: the turn goes over when the pointer is past the spine or the
 * page was flicked towards it fast enough; otherwise it falls back.
 */
export function letGo(side, qx, vx, W) {
  const past = side > 0 ? qx < 0 : qx > 0;
  const flick = side > 0 ? vx < -0.6 * W : vx > 0.6 * W;
  return past || flick;
}

/**
 * Where the held point travels on a turn by itself (a key, a click) or after
 * letting go: from (fx, fy) to the other side, (-cx, cy), in time u 0…1,
 * eased, lifted a little in the middle so the page rolls rather than slides.
 */
export function turnPath(cx, cy, fx, fy, u, over, H) {
  const e = u < 0.5 ? 2 * u * u : 1 - Math.pow(-2 * u + 2, 2) / 2;
  const tx = over ? -cx : cx;
  const ty = cy;
  const lift = over ? Math.sin(Math.PI * e) * H * 0.12 * (cy > H / 2 ? -1 : 1) : 0;
  return { x: fx + (tx - fx) * e, y: fy + (ty - fy) * e + lift };
}
