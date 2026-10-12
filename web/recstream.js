// SPDX-License-Identifier: AGPL-3.0-or-later
//
// web/recstream.js — a presentation recorded as display lists, and played.
//
// The editor plays the deck on its own, frame by frame at n / fps (the video
// export's timeline, src/PresVideo.rgr: PresApp.videoBegin/videoFrameJson,
// no clock on the wall), and keeps what each frame draws. A page then shows
// the recording with only the painter (gl/evg-webgl.js): no layout, no
// engine. The light player plays it (web/play.js, /r/{id}/{hash}).
//
// The stream (JSON, gzipped in the file):
//
//   sliqrec   1, the format
//   width, height, fps, frames, duration
//   slides    [{ start, end, steps: [t] }]: each slide's master time, and
//             the build steps on it (the sub times a presenter steps
//             through); seconds of the recording
//   cmds      every distinct shape once: a drawing command without its
//             place (x, y), its points (pts) as whole twentieths of a unit
//             from that place, each from the one before
//   keys      [[n, [shape, x, y, shape, x, y, …]]]: the frames whose drawing
//             differs from the one before, each command as its shape and
//             its place (in twentieths); a frame between two keys draws the
//             earlier one. A slide moving in is its shapes at other places,
//             so its frames add places, not shapes
//   effects   [[n, effects]]: the slide's surface effects from frame n on,
//             without their clock (the page runs it, as the viewer does)
//   faces     [[name, file]]: the faces the text is set in (fonts/{file})
//   pictures  [{ src, type, b64 }]: each picture a command draws (src), as
//             the export encoded it
//   deckEffects  the deck's own surface effects (FxLang JSON, web/fxdeck.js)
//   scripts   [] for now. A slide's script (TSX) is recorded as the poses
//             the timeline draws; running it live over the recording comes
//             later, and goes here.
//
// The light player (web/play.js, /r/{id}/{hash}) needs nothing else: the
// faces come from the site's fonts/, the pictures are in the stream.
//
// Played (RecNav): ← → go from slide to slide (each plays its way in up to
// its first step), Space goes on to the next step and from a slide's end to
// the next slide, and autoplay plays the whole recording through.

export const FORMAT = 2;

// places and points are kept in twentieths of a slide unit (a 960 wide
// slide drawn 3840 wide is a fifth of a pixel off at most)
const Q = 20;

// A command as its shape (no place, its points from the place, in
// twentieths, each from the one before) and its place.
function shapeOf(c) {
  const ox = c.x || 0;
  const oy = c.y || 0;
  const sh = { ...c };
  delete sh.x;
  delete sh.y;
  if (Array.isArray(c.pts)) {
    const out = new Array(c.pts.length);
    let px = 0;
    let py = 0;
    for (let i = 0; i + 1 < c.pts.length; i += 2) {
      const x = Math.round((c.pts[i] - ox) * Q);
      const y = Math.round((c.pts[i + 1] - oy) * Q);
      out[i] = x - px;
      out[i + 1] = y - py;
      px = x;
      py = y;
    }
    sh.pts = out;
  }
  return { sh, x: Math.round(ox * Q), y: Math.round(oy * Q) };
}

// The command a shape draws at place (x, y) (twentieths).
function placed(sh, x, y) {
  const ox = x / Q;
  const oy = y / Q;
  const c = { ...sh, x: ox, y: oy };
  if (Array.isArray(sh.pts)) {
    const out = new Array(sh.pts.length);
    let px = 0;
    let py = 0;
    for (let i = 0; i + 1 < sh.pts.length; i += 2) {
      px += sh.pts[i];
      py += sh.pts[i + 1];
      out[i] = ox + px / Q;
      out[i + 1] = oy + py / Q;
    }
    c.pts = out;
  }
  return c;
}

// A list's effects without their clock, as text: the same effects at
// another moment are the same effects.
function effectsKey(effects) {
  return JSON.stringify((effects || []).map((e) => {
    const o = { ...e };
    delete o.time;
    return o;
  }));
}

/**
 * Builds a stream from the frames in order. timeline: PresApp.videoBegin's
 * JSON (width, height, fps, frames, duration, events).
 */
export class RecEncoder {
  constructor(timeline) {
    this.timeline = timeline;
    this.cmdIds = new Map();
    this.cmds = [];
    this.keys = [];
    this.effects = [];
    this.n = 0;
    this.lastIds = null;
    this.lastFx = null;
    this.width = timeline.width;
    this.height = timeline.height;
  }

  /** The next frame's list ({ cmds, effects }) and the slide's size. */
  add(list, width, height) {
    if (width) this.width = width;
    if (height) this.height = height;
    const ids = [];
    for (const c of (list && list.cmds) || []) {
      const { sh, x, y } = shapeOf(c);
      const k = JSON.stringify(sh);
      let id = this.cmdIds.get(k);
      if (id === undefined) {
        id = this.cmds.length;
        this.cmdIds.set(k, id);
        this.cmds.push(sh);
      }
      ids.push(id, x, y);
    }
    const same = this.lastIds && ids.length === this.lastIds.length && ids.every((v, i) => v === this.lastIds[i]);
    if (!same) {
      this.keys.push([this.n, ids]);
      this.lastIds = ids;
    }
    const fx = effectsKey(list && list.effects);
    if (fx !== this.lastFx) {
      this.effects.push([this.n, JSON.parse(fx)]);
      this.lastFx = fx;
    }
    this.n++;
  }

  /** The stream; `with`: what the page needs besides the frames (faces,
   * pictures, deckEffects). */
  finish(withAssets = {}) {
    const tl = this.timeline;
    return {
      sliqrec: FORMAT,
      width: this.width,
      height: this.height,
      fps: tl.fps,
      frames: this.n,
      duration: tl.duration,
      slides: slidesOf(tl),
      cmds: this.cmds,
      keys: this.keys,
      effects: this.effects,
      faces: withAssets.faces || [],
      pictures: withAssets.pictures || [],
      deckEffects: withAssets.deckEffects || "",
      scripts: [],
      // the 3-D flight through the slides (web/play.js), when exported with it
      flight: withAssets.flight || null,
    };
  }
}

/** The slides and their build steps from the timeline's events. */
export function slidesOf(timeline) {
  const slides = [];
  for (const e of timeline.events || []) {
    if (e.kind === "slide") slides[e.slide] = { start: e.start, end: e.end, steps: [] };
  }
  for (const e of timeline.events || []) {
    if (e.kind === "build" && slides[e.slide]) slides[e.slide].steps.push(e.start);
  }
  const out = [];
  for (let i = 0; i < slides.length; i++) {
    const s = slides[i] || { start: out.length ? out[out.length - 1].end : 0, end: out.length ? out[out.length - 1].end : 0, steps: [] };
    s.steps = [...new Set(s.steps)].filter((t) => t > s.start && t < s.end).sort((a, b) => a - b);
    out.push(s);
  }
  return out;
}

/** A stream read back: what frame n (or time t) draws. */
export class Recording {
  constructor(stream) {
    if (!stream || stream.sliqrec !== FORMAT) throw new Error("not a recording of this format (export it again)");
    this.s = stream;
    this.width = stream.width;
    this.height = stream.height;
    this.fps = stream.fps;
    this.frames = stream.frames;
    this.duration = stream.duration;
    this.slides = stream.slides;
    this.built = new Map();
  }

  /** The frame shown at time t on slide i: a slide's last frame at its end
   * (not the next slide's first). */
  frameAt(t, i = -1) {
    let n = Math.floor(t * this.fps + 1e-6);
    const s = this.slides[i];
    if (s) {
      const first = Math.round(s.start * this.fps);
      const last = Math.max(first, Math.ceil(s.end * this.fps - 1e-6) - 1);
      n = Math.max(first, Math.min(last, n));
    }
    return Math.max(0, Math.min(this.frames - 1, n));
  }

  /** The list frame n draws: { cmds, effects } (shared: not to be changed
   * but for the effects' clock). */
  list(n) {
    const k = lastAtOrBefore(this.s.keys, n);
    const fx = lastAtOrBefore(this.s.effects, n);
    const key = k + ":" + fx;
    let l = this.built.get(key);
    if (!l) {
      const refs = k >= 0 ? this.s.keys[k][1] : [];
      const effects = fx >= 0 ? this.s.effects[fx][1].map((e) => ({ ...e })) : [];
      const cmds = [];
      for (let i = 0; i + 2 < refs.length; i += 3) cmds.push(placed(this.s.cmds[refs[i]], refs[i + 1], refs[i + 2]));
      l = { cmds, effects };
      if (this.built.size > 64) this.built.clear();
      this.built.set(key, l);
    }
    return l;
  }

  /** The slide shown at time t. */
  slideAt(t) {
    let i = 0;
    for (let k = 0; k < this.slides.length; k++) if (this.slides[k].start <= t + 1e-9) i = k;
    return i;
  }
}

// the index of the last [n, …] entry at or before n, -1 for none
function lastAtOrBefore(entries, n) {
  let lo = 0;
  let hi = entries.length - 1;
  let found = -1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (entries[mid][0] <= n) {
      found = mid;
      lo = mid + 1;
    } else hi = mid - 1;
  }
  return found;
}

/**
 * Where a recording is played, and the way round it. t is the recording's
 * time; `slide` the slide it is on. Presenting (auto false) the clock stops
 * at each build step and at the slide's end; autoplay plays to the end.
 */
export class RecNav {
  constructor(rec, { auto = false } = {}) {
    this.rec = rec;
    this.auto = auto;
    this.slide = 0;
    this.t = rec.slides.length ? rec.slides[0].start : 0;
    this.playing = true;
  }

  get count() {
    return this.rec.slides.length;
  }

  // where the clock stops next on the shown slide: its next step, or its end
  stopAfter(t) {
    const s = this.rec.slides[this.slide];
    if (!s) return this.rec.duration;
    for (const step of s.steps) if (step > t + 1e-6) return step;
    return s.end;
  }

  /** dt seconds of playing; true when the frame changed hands (still moving). */
  tick(dt) {
    if (!this.playing) return false;
    const want = this.t + Math.max(0, dt);
    if (this.auto) {
      this.t = Math.min(want, this.rec.duration);
      this.slide = this.rec.slideAt(this.t);
      if (this.t >= this.rec.duration - 1e-9) this.playing = false;
      return true;
    }
    const stop = this.stopAfter(this.t);
    if (want >= stop) {
      this.t = stop;
      this.playing = false;
    } else this.t = want;
    return true;
  }

  /** The frame to draw now. */
  frame() {
    return this.rec.frameAt(this.t, this.slide);
  }

  /** Slide i from its master time, playing its way in. */
  go(i) {
    const n = this.count;
    if (!n) return;
    this.slide = Math.max(0, Math.min(n - 1, i));
    this.t = this.rec.slides[this.slide].start;
    this.playing = true;
  }

  /** → : the next slide; at the last one, its end. */
  nextSlide() {
    if (this.slide < this.count - 1) this.go(this.slide + 1);
    else this.finish();
  }

  /** ← : the previous slide (or this one again from its start). */
  prevSlide() {
    this.go(this.slide - 1);
  }

  /** Space: what is moving lands where it stops; at a step the next one
   * plays; at the slide's end the next slide. */
  next() {
    if (this.playing && !this.auto) {
      this.t = this.stopAfter(this.t);
      this.playing = false;
      return;
    }
    const s = this.rec.slides[this.slide];
    if (s && this.t < s.end - 1e-6) {
      this.auto = false;
      this.playing = true;
      return;
    }
    this.auto = false;
    this.nextSlide();
  }

  /** The shown slide at its end, everything on it shown. */
  finish() {
    const s = this.rec.slides[this.slide];
    if (!s) return;
    this.t = s.end;
    this.playing = false;
  }

  /** Autoplay on (plays on from here to the recording's end) or off. */
  setAuto(on) {
    this.auto = !!on;
    if (this.auto) {
      if (this.t >= this.rec.duration - 1e-6) this.go(0);
      this.auto = true;
      this.playing = true;
    } else this.playing = false;
  }
}

/** The stream as the file keeps it: gzipped JSON. */
export async function packStream(stream) {
  const bytes = new TextEncoder().encode(JSON.stringify(stream));
  const gz = new Blob([bytes]).stream().pipeThrough(new CompressionStream("gzip"));
  return new Uint8Array(await new Response(gz).arrayBuffer());
}

/** The file's bytes read back as a Recording. */
export async function unpackStream(bytes) {
  const plain = new Blob([bytes]).stream().pipeThrough(new DecompressionStream("gzip"));
  return new Recording(JSON.parse(await new Response(plain).text()));
}

/** The name a recording's bytes go under: 24 hex of their SHA-256, so its
 * address changes when the recording does. */
export async function streamHash(bytes) {
  const d = new Uint8Array(await crypto.subtle.digest("SHA-256", bytes));
  return [...d.slice(0, 12)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

/** The deck file a recording is kept in. */
export function streamPath(hash, ext = ".sliqrec") {
  return "recordings/web-" + hash + ext;
}

export const STREAM_TYPE = "application/vnd.sliqtly.rec+gzip";
