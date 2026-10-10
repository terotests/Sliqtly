// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Selecting a slide's text with the mouse and copying it, as text on a web
// page is: the rules, which need no browser (web/test/slidetext.test.mjs).
// The public viewer (web/view.js) and the editor's presenting (web/main.js)
// wire them to the pointer and the clipboard.
//
// A slide reaches the page as a display list, and its text as one TEXT
// command (k 3) per line of each run, at the line box's top left. Read in
// paint order, which is the order the Markdown wrote them in, the runs make
// ONE string, the slide's text as a copy gives it: runs on the same line
// joined as they stand (with a space where there is a gap), a line that a
// paragraph wrapped onto joined by a space, any other line by a newline. A
// selection is a range of that string, [anchor, caret) either way round,
// so it can start in a title and end in a list three runs further on.
//
// Where a character sits is measured, not guessed: `measure(text, cmd)` is
// how wide `text` is in the run's own face, which the page asks the same
// 2D context the painter rasterizes runs with (fontSpec, letter-spacing).

const seg = typeof Intl !== "undefined" && Intl.Segmenter ? new Intl.Segmenter(undefined, { granularity: "grapheme" }) : null;

/** The offsets in `s` a caret may stop at: each grapheme cluster's start, and the end. */
export function stopsOf(s) {
  const out = [];
  if (seg) for (const g of seg.segment(s)) out.push(g.index);
  else for (let i = 0; i < s.length; ) {
    out.push(i);
    i += s.codePointAt(i) > 0xffff ? 2 : 1;
  }
  out.push(s.length);
  return out;
}

// A run the selection can use: drawn text, upright and not invisible.
function readable(c) {
  return c && c.k === 3 && typeof c.text === "string" && c.text.length > 0 && !c.rot && !(c.c && c.c[3] === 0) && c.h > 0;
}

// Whether b is level with a: on the same line, whatever the order.
const level = (a, b) => Math.abs(a.y - b.y) < Math.min(a.h, b.h) * 0.5;
const right = (r) => r.xs[r.xs.length - 1];
const sizeOf = (r) => r.size || r.h;

/**
 * The text of a display list's commands, as the page lays a selection over
 * it: { text, runs }, each run { cmd (its index in cmds), x, y, h, start,
 * end (its part of text), stops (offsets in its own text), xs (the x of
 * each stop) }, in reading order.
 *
 * Reading order is paint order, but for a run painted after the text it
 * stands to the left of on the same line: a list's number ("1.") is drawn
 * after its item, and read before it.
 */
export function slideText(cmds, measure) {
  const runs = [];
  for (let i = 0; i < (cmds || []).length; i++) {
    const c = cmds[i];
    if (!readable(c)) continue;
    const stops = stopsOf(c.text);
    const ls = c.ls || 0;
    const xs = stops.map((s, k) => (s === 0 ? c.x : c.x + measure(c.text.slice(0, s), c) + ls * k));
    const run = { cmd: i, x: c.x, y: c.y, h: c.h, size: c.size || c.h, text: c.text, start: 0, end: 0, stops, xs };
    // the first run of this line, painted before, that it stands left of
    let at = runs.length;
    for (let j = runs.length - 1; j >= 0 && j >= runs.length - 24; j--) {
      const r = runs[j];
      if (!level(r, run)) continue;
      if (right(run) <= r.x + 1) at = j;
      else break;
    }
    runs.splice(at, 0, run);
  }
  // the separators: runs on one line joined as they stand (a space where
  // there is a gap); a line a paragraph wrapped onto (it starts where a run
  // of the line above starts, a line's step below it) by a space; any other
  // line by a newline
  let text = "";
  let line = [];
  let above = [];
  for (const run of runs) {
    const prev = line[line.length - 1];
    const tol = prev ? Math.max(1, Math.min(prev.h, run.h) * 0.25) : 1;
    if (prev && level(prev, run) && run.x >= right(prev) - tol) {
      const gap = run.x - right(prev);
      text += gap > sizeOf(run) * 0.15 && !/\s$/.test(text) && !/^\s/.test(run.text) ? " " : "";
      line.push(run);
    } else {
      if (prev) {
        above = line;
        const dy = run.y - above[0].y;
        const step = Math.max(above[0].h * 1.05, sizeOf(above[0]) * 1.6);
        const wraps = dy > above[0].h * 0.5 && dy <= step && Math.abs(sizeOf(run) - sizeOf(above[0])) < 0.5 && above.some((r) => Math.abs(r.x - run.x) < tol);
        text += wraps ? (/\s$/.test(text) ? "" : " ") : "\n";
      }
      line = [run];
    }
    run.start = text.length;
    text += run.text;
    run.end = text.length;
  }
  for (const run of runs) delete run.text;
  return { text, runs };
}

/** The run `i` (an offset of the slide's text) is in or just after; -1 when none. */
function runOf(st, i) {
  let best = -1;
  for (let r = 0; r < st.runs.length; r++) if (st.runs[r].start <= i) best = r;
  return best;
}

/** The x of offset `i` of the slide's text, on its run (the end of the run a separator follows). */
export function xAt(st, i) {
  const r = runOf(st, i);
  if (r < 0) return 0;
  const run = st.runs[r];
  const k = i - run.start;
  for (let s = run.stops.length - 1; s >= 0; s--) if (run.stops[s] <= k) return run.xs[s];
  return run.xs[0];
}

/** The run under (px, py) in the slide's units, grown by `pad`; -1 when the point is on no text. */
export function runAt(st, px, py, pad = 0) {
  let best = -1;
  let bestD = Infinity;
  for (let r = 0; r < st.runs.length; r++) {
    const run = st.runs[r];
    const x1 = run.xs[run.xs.length - 1];
    if (py < run.y - pad || py >= run.y + run.h + pad || px < run.x - pad || px > x1 + pad) continue;
    const d = px < run.x ? run.x - px : px > x1 ? px - x1 : 0;
    if (d < bestD) {
      best = r;
      bestD = d;
    }
  }
  return best;
}

/**
 * The offset a pointer at (px, py) lands on, for a drag that may leave the
 * text: the run level with it (nearest by x), else the nearest line above
 * or below; then the nearer edge of the character under it.
 */
export function indexAt(st, px, py) {
  if (!st.runs.length) return 0;
  let best = 0;
  let bestKey = [Infinity, Infinity];
  for (let r = 0; r < st.runs.length; r++) {
    const run = st.runs[r];
    const x1 = run.xs[run.xs.length - 1];
    const dy = py < run.y ? run.y - py : py >= run.y + run.h ? py - (run.y + run.h) + 1e-6 : 0;
    const dx = px < run.x ? run.x - px : px > x1 ? px - x1 : 0;
    if (dy < bestKey[0] || (dy === bestKey[0] && dx < bestKey[1])) {
      best = r;
      bestKey = [dy, dx];
    }
  }
  const run = st.runs[best];
  // above a run's line: its start; below it: its end
  if (py < run.y && bestKey[0] > 0) return run.start;
  if (py >= run.y + run.h && bestKey[0] > 0) return run.end;
  let k = 0;
  for (let s = 0; s < run.xs.length; s++) {
    if (Math.abs(run.xs[s] - px) < Math.abs(run.xs[k] - px)) k = s;
  }
  return run.start + run.stops[k];
}

/** The band over [a, b) of the slide's text: one rectangle per run it covers, { cmd, x, y, w, h }. */
export function bandRects(st, a, b) {
  const out = [];
  if (b <= a) return out;
  let prev = null;
  for (const run of st.runs) {
    const before = prev;
    prev = run;
    if (run.end <= a || run.start >= b) continue;
    // the gap from a selected run just before it on the same line (a list's
    // number) is covered too
    const gap = before && level(before, run) ? run.x - right(before) : -1;
    const x0 = a < run.start && gap > 0 && gap < sizeOf(run) * 3 ? right(before) : xAt(st, Math.max(a, run.start));
    const x1 = b >= run.end ? run.xs[run.xs.length - 1] : xAt(st, b);
    if (x1 > x0) out.push({ cmd: run.cmd, x: x0, y: run.y, w: x1 - x0, h: run.h });
  }
  return out;
}

export const BAND = [56, 132, 255, 0.32];

/**
 * `cmds` with the band over [a, b) drawn under the text it covers (a new
 * array; the list itself is kept). When the slide's text was read from
 * several lists laid side by side (a book's two pages), `from` is where
 * this list's commands start in what was read and `dx` how far right it was
 * moved.
 */
export function withBand(cmds, st, a, b, { color = BAND, from = 0, dx = 0 } = {}) {
  const at = new Map();
  for (const r of bandRects(st, a, b)) {
    const i = r.cmd - from;
    if (i < 0 || i >= cmds.length) continue;
    if (!at.has(i)) at.set(i, []);
    at.get(i).push({ k: 0, x: r.x - dx, y: r.y, w: r.w, h: r.h, c: color.slice() });
  }
  if (!at.size) return cmds;
  const out = [];
  for (let i = 0; i < cmds.length; i++) {
    const under = at.get(i);
    if (under) out.push(...under);
    out.push(cmds[i]);
  }
  return out;
}

/**
 * The commands of lists laid side by side ([{ cmds, dx }], a book's
 * spread), as one list to read the text of: each text command moved right
 * by its list's dx. `from` gives where each list's commands start.
 */
export function sideBySide(parts) {
  const cmds = [];
  const from = [];
  for (const { cmds: cs, dx } of parts) {
    from.push(cmds.length);
    for (const c of cs || []) cmds.push(dx && c.k === 3 ? { ...c, x: c.x + dx } : c);
  }
  return { cmds, from };
}

/**
 * What a press, a drag and a release do with the mouse over a slide:
 *   press(i, shift, onText)  "select" (the press is the selection's), "clear"
 *                    (a press that only lets go of a selection) or "" (not
 *                    ours: the slide's own click, a link, the next slide)
 *   drag(i)          true when the drag selects (it has left the press)
 *   release()        "click" when the press was a plain click on text, which
 *                    the slide gets as it would have; else ""
 * A plain click on text is still the slide's click, so clicking on to the
 * next slide works wherever one clicks; only a drag selects. While text is
 * selected, the next click lets go of it and does nothing else.
 */
export class TextSelection {
  constructor() {
    this.text = "";
    this.anchor = 0;
    this.caret = 0;
    this.pressed = false;
    this.moved = false;
    this.had = false;
  }

  /** New text drops the selection; the same text keeps it. */
  setText(t) {
    if (t === this.text) return;
    this.text = t;
    this.clear();
  }

  has() {
    return this.anchor !== this.caret;
  }

  start() {
    return Math.min(this.anchor, this.caret);
  }

  end() {
    return Math.max(this.anchor, this.caret);
  }

  /** What a copy gives: the selected characters, line breaks included. */
  selected() {
    return this.has() ? this.text.slice(this.start(), this.end()) : "";
  }

  clear() {
    this.anchor = this.caret = 0;
    this.pressed = false;
    this.moved = false;
  }

  selectAll() {
    this.anchor = 0;
    this.caret = this.text.length;
  }

  press(i, shift, onText) {
    this.had = this.has();
    if (shift && this.had) {
      this.caret = i;
      this.pressed = true;
      this.moved = true;
      return "select";
    }
    if (!onText) {
      if (!this.had) return "";
      this.clear();
      return "clear";
    }
    this.pressed = true;
    this.moved = false;
    this.pressAt = i;
    return "select";
  }

  drag(i) {
    if (!this.pressed) return false;
    if (!this.moved) {
      if (i === this.pressAt) return false;
      this.moved = true;
      this.anchor = this.pressAt;
    }
    this.caret = i;
    return true;
  }

  release() {
    if (!this.pressed) return "";
    this.pressed = false;
    if (this.moved) return "";
    // a click: it lets go of a selection, or it is the slide's
    if (this.had) {
      this.clear();
      return "";
    }
    return "click";
  }
}

