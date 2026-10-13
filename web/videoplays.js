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
