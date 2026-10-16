// SPDX-License-Identifier: AGPL-3.0-or-later
//
// web/videoplays.js — the programs on slides (```app) run for a video.
//
// A video's frame n is time n / fps, never the clock on the wall
// (web/videoexport.js), so a program in it is not run by the page's own
// loop (web/apps.js) but stepped here: before frame n is drawn, each
// program on that frame's slide is handed one frame of 1 / fps at the
// slide's own time, in a CErXes worker of its own (cerxes-worker.js), and
// what it gives is its picture in that frame (app.setPlayFrame). A program
// starts afresh when its slide comes, and ends when the slide goes. It gets
// no keys and no pointer; what it asks of the deck is left out.
import { RUNTIME } from "./cerxes-runtime.js";
import { DECK_RUNTIME } from "./apps-runtime.js";
import { workerUrl } from "./sitescript.js";
import { splitFrame } from "./apps.js";

// the first load fetches and compiles the engine; a frame after that
const FIRST_LIMIT_MS = 15000;
const LIMIT_MS = 3000;

/** The frame a program is handed at its slide's time `time`, `dt` after the last. */
export function videoFrameArg(p, state, time, dt) {
  return {
    w: p.w, h: p.h, dt, time,
    keys: {},
    pointer: { x: 0, y: 0, down: false, inside: false },
    events: [],
    deck: { ...state, slide: state.home, mode: "present", focused: false },
  };
}

/** The programs of slide `slide` among app.playsJson()'s (no slide scripts). */
export function playsOn(plays, slide) {
  return plays.filter((p) => !p.script && p.slide === slide);
}

/** The seconds a frame's asks (the JSON list after its tree) give with presentation.video, 0 for none. */
export function videoSecondsOf(asksText) {
  let asks = [];
  try {
    asks = JSON.parse(asksText || "[]");
  } catch {
    return 0;
  }
  const v = Array.isArray(asks) ? asks.filter((a) => a && a.k === "video").pop() : null;
  const s = v ? Number(v.seconds) : 0;
  return isFinite(s) && s > 0 ? s : 0;
}

/**
 * Before a video or a recording is timed: each program on a slide is
 * started and handed one frame, and how long it asked its slide to stay
 * (presentation.video, said from that frame on) goes to app.setPlayVideo;
 * one that asks nothing, or does not start, is as its slide is. Returns
 * whether the deck's timing changed.
 */
export async function probeVideo({ app, makeWorker = () => new Worker(workerUrl("cerxes-worker.js"), { type: "module" }) }) {
  let changed = false;
  for (const p of JSON.parse(app.playsJson()).filter((q) => !q.script)) {
    let seconds = 0;
    const source = app.playSource(p.key);
    if (source) {
      let worker = null;
      try {
        worker = makeWorker();
        const ask = (msg, limit) => new Promise((resolve) => {
          const timer = setTimeout(() => resolve({ ok: false }), limit);
          worker.onmessage = (ev) => { clearTimeout(timer); resolve(ev.data); };
          worker.onerror = () => { clearTimeout(timer); resolve({ ok: false }); };
          worker.postMessage(msg);
        });
        const m = await ask({ type: "load", runtime: RUNTIME + "\n" + DECK_RUNTIME, source }, FIRST_LIMIT_MS);
        if (m.ok) {
          const arg = JSON.stringify(videoFrameArg(p, JSON.parse(app.playState(p.key)), 0, 0));
          const f = await ask({ type: "frame", arg }, LIMIT_MS);
          if (f.ok) seconds = videoSecondsOf(splitFrame(f.out).asks);
        }
      } catch {
        seconds = 0;
      } finally {
        if (worker) worker.terminate();
      }
    }
    if (app.setPlayVideo(p.key, seconds)) changed = true;
  }
  return changed;
}

/**
 * app: the PresApp (playsJson, playSource, playState, setPlayFrame,
 * setPlayStopped). `step(slide, time, dt)` before each frame; `end()` after
 * the last.
 */
export function createVideoPlays({ app, makeWorker = () => new Worker(workerUrl("cerxes-worker.js"), { type: "module" }) }) {
  const plays = JSON.parse(app.playsJson());
  const runs = new Map(); // key -> { worker, ok, failed }
  let shown = -1;

  function ask(r, msg, limit) {
    return new Promise((resolve) => {
      const timer = setTimeout(() => resolve({ ok: false, error: "did not answer within " + Math.round(limit / 1000) + " s" }), limit);
      r.worker.onmessage = (ev) => {
        clearTimeout(timer);
        resolve(ev.data);
      };
      r.worker.onerror = (e) => {
        clearTimeout(timer);
        resolve({ ok: false, error: (e && e.message) || "the program's engine did not start" });
      };
      r.worker.postMessage(msg);
    });
  }

  function stop(key) {
    const r = runs.get(key);
    if (r && r.worker) r.worker.terminate();
    runs.delete(key);
  }

  async function start(p) {
    const r = { worker: null, failed: false };
    runs.set(p.key, r);
    const source = app.playSource(p.key);
    if (!source) {
      r.failed = true;
      return r;
    }
    try {
      r.worker = makeWorker();
    } catch (e) {
      r.failed = true;
      app.setPlayStopped(p.key, (e && e.message) || "worker");
      return r;
    }
    const m = await ask(r, { type: "load", runtime: RUNTIME + "\n" + DECK_RUNTIME, source }, FIRST_LIMIT_MS);
    if (!m.ok) {
      r.failed = true;
      app.setPlayStopped(p.key, m.error || "");
    }
    return r;
  }

  async function step(slide, time, dt) {
    if (slide !== shown) {
      for (const key of [...runs.keys()]) stop(key);
      shown = slide;
    }
    for (const p of playsOn(plays, slide)) {
      const r = runs.get(p.key) || (await start(p));
      if (r.failed) continue;
      const arg = JSON.stringify(videoFrameArg(p, JSON.parse(app.playState(p.key)), time, dt));
      const m = await ask(r, { type: "frame", arg }, LIMIT_MS);
      if (!m.ok) {
        r.failed = true;
        app.setPlayStopped(p.key, m.error || "");
        continue;
      }
      const { tree } = splitFrame(m.out);
      if (!app.setPlayFrame(p.key, tree)) r.failed = true;
    }
  }

  function end() {
    for (const key of [...runs.keys()]) stop(key);
    shown = -1;
  }

  return { step, end, has: () => plays.some((p) => !p.script) };
}

// The frames a program is run for its picture in an export: as many as the
// server runs (mcp-go/cerxescheck.go cerxesCheckFrames), so the editor's
// export and the MCP's show the same picture.
export const STILL_FRAMES = 3;
export const STILL_DT = 0.016;

/**
 * Before an export: each program that would show as its fence's plate
 * (app.playsStill(picked): its slide was never on the stage) is started and
 * run for its first frames, as on its slide, and its last tree is set as
 * its picture (app.setPlayFrame). One that does not run says why on its
 * plate (app.setPlayStopped). Returns how many got a picture. `tried`: a
 * Set of keys run before, skipped and added to (the Document view runs each
 * program once, not again while one gives no picture).
 */
export async function stillPlays({ app, picked = false, tried = null, makeWorker = () => new Worker(workerUrl("cerxes-worker.js"), { type: "module" }) }) {
  let list = [];
  try { list = JSON.parse(app.playsStill(picked)); } catch { list = []; }
  let drawn = 0;
  for (const p of list) {
    if (tried && tried.has(p.key)) continue;
    const source = app.playSource(p.key);
    if (!source) continue;
    if (tried) tried.add(p.key);
    let worker = null;
    try {
      worker = makeWorker();
    } catch (e) {
      app.setPlayStopped(p.key, (e && e.message) || "worker");
      continue;
    }
    const ask = (msg, limit) => new Promise((resolve) => {
      const timer = setTimeout(() => resolve({ ok: false, error: "did not answer within " + Math.round(limit / 1000) + " s" }), limit);
      worker.onmessage = (ev) => { clearTimeout(timer); resolve(ev.data); };
      worker.onerror = (e) => { clearTimeout(timer); resolve({ ok: false, error: (e && e.message) || "the program's engine did not start" }); };
      worker.postMessage(msg);
    });
    try {
      const m = await ask({ type: "load", runtime: RUNTIME + "\n" + DECK_RUNTIME, source }, FIRST_LIMIT_MS);
      if (!m.ok) {
        app.setPlayStopped(p.key, m.error || "");
        continue;
      }
      let tree = "";
      let why = "";
      for (let i = 0; i < STILL_FRAMES; i++) {
        const arg = JSON.stringify(videoFrameArg(p, JSON.parse(app.playState(p.key)), i * STILL_DT, STILL_DT));
        const f = await ask({ type: "frame", arg }, LIMIT_MS);
        if (!f.ok) {
          why = f.error || "";
          break;
        }
        tree = splitFrame(f.out).tree;
      }
      if (why) app.setPlayStopped(p.key, why);
      else if (tree && app.setPlayFrame(p.key, tree)) drawn++;
    } finally {
      worker.terminate();
    }
  }
  return drawn;
}
