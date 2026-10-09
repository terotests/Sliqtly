// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Programs on slides (```app): each runs in CErXes in a Worker of its own
// (cerxes-worker.js), only while its slide is on the stage; its view() comes
// back as an element tree that the app lays out with EVG and paints in the
// fence's box (src/PresPlay.rgr). This file is the page's half: which
// programs run, their frames, the keyboard and the pointer.
//
//   frame in:  { w, h, dt, time, keys, pointer, events, deck: app.playState }
//   frame out: the tree (app.setPlayFrame) and what the program asked of the
//              deck (app.playAsks, granted by the fence's allow:)
//
// One frame of a program is in flight at a time. A program that does not
// answer in LIMIT_MS (an endless loop) has its worker ended; the box keeps
// its last picture and the page says why.
import { RUNTIME } from "./cerxes-runtime.js";
import { DECK_RUNTIME } from "./apps-runtime.js";

const LIMIT_MS = 3000;
// the first load fetches and compiles the engine
const FIRST_LIMIT_MS = 15000;
// deck.set lays the deck out again (its headers and footers): at most this often
const SET_EVERY_MS = 400;

/** A frame's reply: the tree's JSON and the asks' JSON, split at the last line break. */
export function splitFrame(out) {
  const nl = out.lastIndexOf("\n");
  if (nl < 0) return { tree: out, asks: "[]" };
  return { tree: out.slice(0, nl), asks: out.slice(nl + 1) };
}

/**
 * Asks sorted for the deck: deck.set held back (the last value per key wins)
 * until SET_EVERY_MS has passed, everything else at once.
 */
export function sortAsks(asks, held) {
  const now = [];
  for (const a of asks) {
    if (a && a.k === "deck.set") held.set(String(a.key).toLowerCase(), a);
    else now.push(a);
  }
  return now;
}

export function createApps({ app, repaint, toast, t = (s) => s }) {
  const runs = new Map(); // key -> run
  let plays = [];
  let playsRev = -1;
  const keys = {};
  const told = new Set();
  // the program Esc took the keyboard from while editing: its box is picked
  // like any block (a double click edits it) until a press lands elsewhere
  let skipKey = "";

  function newRun(key) {
    return { key, worker: null, source: "", running: false, waiting: false, deadline: 0, last: 0, events: [], pointer: { x: 0, y: 0, down: false, inside: false }, held: new Map(), setAt: 0, stopped: "" };
  }

  function stopRun(r, why) {
    if (r.worker) r.worker.terminate();
    r.worker = null;
    r.running = false;
    r.waiting = false;
    if (why) {
      r.stopped = why;
      say(why);
    }
  }

  function say(line) {
    if (told.has(line)) return;
    told.add(line);
    toast(line);
  }

  function load(r, source) {
    stopRun(r, "");
    r.source = source;
    r.stopped = "";
    r.events = [];
    // with this module's build stamp (?v=…), as every import of ours has
    const w = new Worker(new URL("./cerxes-worker.js" + new URL(import.meta.url).search, import.meta.url), { type: "module" });
    w.onmessage = (ev) => reply(r, w, ev.data);
    w.onerror = (e) => {
      if (r.worker === w) stopRun(r, r.key.split("#")[0] + ": " + t("the program's engine did not start") + " (" + (e.message || "worker") + ")");
    };
    r.worker = w;
    r.waiting = true;
    r.deadline = performance.now() + FIRST_LIMIT_MS;
    w.postMessage({ type: "load", runtime: RUNTIME + "\n" + DECK_RUNTIME, source });
  }

  function name(r) {
    return r.key.replace(/#\d+$/, "");
  }

  function reply(r, w, m) {
    if (r.worker !== w) return;
    r.waiting = false;
    if (m.output) for (const line of m.output.split("\n")) if (line) console.log(name(r) + ": " + line);
    if (!m.ok) {
      stopRun(r, name(r) + ": " + m.error);
      return;
    }
    if (m.type === "loaded") {
      r.running = true;
      r.last = performance.now();
      return;
    }
    const { tree, asks } = splitFrame(m.out);
    let changed = app.setPlayFrame(r.key, tree);
    if (!changed) {
      stopRun(r, name(r) + ": " + t("view() did not give an element tree"));
      return;
    }
    if (asks !== "[]") {
      let list = [];
      try { list = JSON.parse(asks); } catch (_) { /* nothing asked */ }
      const now = sortAsks(list, r.held);
      if (now.length) app.playAsks(r.key, JSON.stringify(now));
    }
    flushSets(r);
    noteRefusals();
    repaint();
  }

  function flushSets(r) {
    if (!r.held.size) return;
    const now = performance.now();
    if (now - r.setAt < SET_EVERY_MS) return;
    r.setAt = now;
    const sets = [...r.held.values()];
    r.held.clear();
    app.playAsks(r.key, JSON.stringify(sets));
  }

  function noteRefusals() {
    const notes = app.playNotes();
    if (!notes) return;
    for (const line of notes.split("\n")) if (line) say(line);
  }

  function shownKeys() {
    return app.playsShown().split("\n").filter(Boolean);
  }

  // Once a frame, from the page's frame(): the programs the deck has now,
  // and a frame for each one on the stage.
  function tick(rev) {
    if (rev !== playsRev) {
      playsRev = rev;
      try { plays = JSON.parse(app.playsJson()); } catch (_) { plays = []; }
      const have = new Set(plays.map((p) => p.key));
      for (const [key, r] of runs) if (!have.has(key)) {
        stopRun(r, "");
        runs.delete(key);
      }
      if (app.playFocus() && !have.has(app.playFocus())) release();
    }
    if (!plays.length) return;
    const now = performance.now();
    const shown = new Set(shownKeys());
    const focus = app.playFocus();
    if (focus && !shown.has(focus)) release();
    if (skipKey && !shown.has(skipKey)) skipKey = "";
    for (const p of plays) {
      if (!p.loaded) continue;
      let r = runs.get(p.key);
      const source = app.playSource(p.key);
      if (!r) {
        r = newRun(p.key);
        runs.set(p.key, r);
      }
      if (r.stopped && source === r.source) continue;
      // a program starts the first time its slide is shown, and again
      // whenever its file changes
      if (source !== r.source) {
        if (shown.has(p.key) || r.source) load(r, source);
        continue;
      }
      if (r.waiting) {
        if (now > r.deadline) stopRun(r, name(r) + ": " + t("did not answer within 3 s (an endless loop?), so it was stopped"));
        continue;
      }
      if (!r.worker) {
        if (shown.has(p.key)) load(r, source);
        continue;
      }
      if (!r.running || !shown.has(p.key) || document.hidden) continue;
      flushSets(r);
      const dt = Math.min(0.1, (now - r.last) / 1000);
      r.last = now;
      const mine = focus === p.key;
      const arg = JSON.stringify({
        w: p.w, h: p.h, dt, time: now / 1000,
        keys: mine ? keys : {},
        pointer: r.pointer,
        events: r.events,
        deck: JSON.parse(app.playState(p.key)),
      });
      r.events = [];
      r.waiting = true;
      r.deadline = now + LIMIT_MS;
      r.worker.postMessage({ type: "frame", arg });
    }
  }

  function release() {
    for (const k in keys) delete keys[k];
    if (app.playFocus()) {
      app.setPlayFocus("");
      repaint();
    }
  }

  // A press on the stage: a program's box takes it, and the keyboard with
  // it (every press, however quick: a game is clicked fast). While editing,
  // after Esc the box is a block like any other until a press elsewhere.
  // True when the program took it.
  function pointerDown(x, y, presenting) {
    if (!plays.length) return false;
    const key = app.playAt(x, y);
    if (!key) {
      skipKey = "";
      release();
      return false;
    }
    if (!presenting && key === skipKey) return false;
    skipKey = "";
    const r = runs.get(key);
    const at = app.playPoint(key, x, y);
    if (!r || !at) return false;
    if (app.playFocus() !== key) {
      for (const k in keys) delete keys[k];
      app.setPlayFocus(key);
    }
    const p = JSON.parse(at);
    Object.assign(r.pointer, { x: p.x, y: p.y, down: true, inside: true });
    r.events.push({ type: "pointerdown", id: p.id, x: p.x, y: p.y });
    r.events.push({ type: "click", id: p.id, x: p.x, y: p.y });
    repaint();
    return true;
  }

  function pointerMove(x, y) {
    const key = app.playFocus();
    const r = key && runs.get(key);
    if (!r) return;
    const at = app.playPoint(key, x, y);
    if (!at) {
      r.pointer.inside = false;
      return;
    }
    const p = JSON.parse(at);
    Object.assign(r.pointer, { x: p.x, y: p.y, inside: true });
  }

  function pointerUp() {
    for (const r of runs.values()) r.pointer.down = false;
  }

  // The keyboard, while a program has it: everything but chords with
  // Control / Command (the page's own) and Esc, which gives it back.
  function keyDown(ev) {
    const key = app.playFocus();
    const r = key && runs.get(key);
    if (!r || ev.ctrlKey || ev.metaKey || ev.altKey) return false;
    if (ev.key === "Escape") {
      skipKey = key;
      release();
      return true;
    }
    if (!ev.repeat) r.events.push({ type: "keydown", key: ev.key });
    keys[ev.key] = true;
    return true;
  }

  function keyUp(ev) {
    const key = app.playFocus();
    const r = key && runs.get(key);
    if (!r || !(ev.key in keys)) return false;
    delete keys[ev.key];
    r.events.push({ type: "keyup", key: ev.key });
    return true;
  }

  window.addEventListener("keydown", (ev) => {
    if (keyDown(ev)) {
      ev.preventDefault();
      ev.stopImmediatePropagation();
    }
  }, true);
  window.addEventListener("keyup", (ev) => {
    if (keyUp(ev)) {
      ev.preventDefault();
      ev.stopImmediatePropagation();
    }
  }, true);
  window.addEventListener("blur", () => {
    for (const k in keys) delete keys[k];
  });

  // Another deck: no program of the last one goes on running.
  function reset() {
    for (const r of runs.values()) stopRun(r, "");
    runs.clear();
    plays = [];
    playsRev = -1;
    told.clear();
    skipKey = "";
    for (const k in keys) delete keys[k];
  }

  return { tick, pointerDown, pointerMove, pointerUp, reset, running: () => [...runs.values()].filter((r) => r.running).map((r) => r.key) };
}
