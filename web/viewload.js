// SPDX-License-Identifier: AGPL-3.0-or-later
//
// What the public viewer (web/view.js) fetches before it shows a slide, and
// in which order the rest follows.
//
// A deck's pictures used to be fetched all at once, and the first slide
// waited for the last of them: on a phone a deck of a dozen large pictures
// stayed on the intro for many seconds. Now a slide waits only for its own
// pictures; the others come after it, nearest slides first (ahead before
// behind), a few at a time, and moving to another slide puts its pictures
// first. The faces wait the same way: those the slides are set in first,
// the other base faces (fallbacks for glyphs a face lacks) after them.

/** The slides each picture is drawn on: Map src → [slide index]. */
export function slidesOfPictures(lists) {
  const on = new Map();
  (lists || []).forEach((l, i) => {
    for (const c of l.cmds || []) {
      if (!c.src) continue;
      const at = on.get(c.src) || [];
      if (!at.includes(i)) at.push(i);
      on.set(c.src, at);
    }
  });
  return on;
}

/** How far slide i is from the one shown, as the order to fetch in: the
 * slide itself, the next ones, a slide behind counting double. */
export function slideDistance(i, from) {
  const d = i - from;
  return d >= 0 ? d : -2 * d;
}

/** faces: [[name, file]]. The ones the lists' text is set in (a list with
 * text in no named face is set in the first), and the rest. */
export function facesFor(lists, faces) {
  const used = new Set();
  for (const l of lists || []) {
    for (const c of l.cmds || []) {
      if (c.font) used.add(c.font);
      else if (typeof c.text === "string" && faces.length) used.add(faces[0][0]);
    }
  }
  const need = faces.filter(([name]) => used.has(name));
  return { need, rest: faces.filter((f) => !need.includes(f)) };
}

/**
 * The deck's pictures, fetched a few at a time, nearest the shown slide
 * first. pics: [{ src, … }] (viewlink.js picturesOf); load(pic) → Promise
 * (its failure counts as done: the slide is shown without it);
 * loaded(src) is called as each one is in.
 */
export class PictureQueue {
  constructor(pics, lists, { load, loaded = () => {}, at = 0, parallel = 3 }) {
    this.slidesOf = slidesOfPictures(lists);
    this.load = load;
    this.loaded = loaded;
    this.parallel = parallel;
    this.at = at;
    this.waiting = [...pics];
    this.inFlight = [];
    this.done = new Set();
    this.waiters = [];
    this.order();
    this.pump();
  }

  // a picture no list draws (a slide's script or program names it): first,
  // since what runs it may want it at once
  rank(p) {
    const on = this.slidesOf.get(p.src);
    if (!on) return -1;
    return Math.min(...on.map((i) => slideDistance(i, this.at)));
  }

  order() {
    this.waiting.sort((a, b) => this.rank(a) - this.rank(b));
  }

  /** Slide i is shown: its pictures go first. */
  focus(i) {
    if (i === this.at) return;
    this.at = i;
    this.order();
  }

  /** Whether every picture slide i draws (and those no list draws) is in. */
  ready(i) {
    return this.waiting.concat(this.inFlight).every((p) => !this.needs(p, i));
  }

  needs(p, i) {
    const on = this.slidesOf.get(p.src);
    return !on || on.includes(i);
  }

  /** Resolves once slide i's pictures are in (or failed). */
  whenReady(i) {
    if (this.ready(i)) return Promise.resolve();
    return new Promise((resolve) => this.waiters.push({ i, resolve }));
  }

  /** How many of the deck's pictures are in, of how many. */
  progress() {
    return { done: this.done.size, of: this.done.size + this.waiting.length + this.inFlight.length };
  }

  pump() {
    while (this.inFlight.length < this.parallel && this.waiting.length) {
      const p = this.waiting.shift();
      this.inFlight.push(p);
      new Promise((resolve) => resolve(this.load(p)))
        .catch(() => {})
        .then(() => {
          this.inFlight = this.inFlight.filter((x) => x !== p);
          this.done.add(p.src);
          this.loaded(p.src);
          this.waiters = this.waiters.filter((w) => {
            if (!this.ready(w.i)) return true;
            w.resolve();
            return false;
          });
          this.pump();
        });
    }
  }
}
