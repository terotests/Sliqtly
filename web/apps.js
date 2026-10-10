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
//
// A slide's script ({script=apps/fx.tsx} on its heading, PresScript) runs the
// same way, keyed "script:<file>#n": it is handed the slide's entities
// (app.scriptTree, again whenever an edit changes them), its frames come back
// as properties for them (app.setScriptFrame), and it stops when its slide
// leaves the stage. Where it ends (app.setScriptFinal: thumbnails, the PDF)
// is worked out by a worker of its own (finals below), and so is how it
// opens (app.setScriptOpen: start(), onEnter, its first build step): its
// slide arrives in that pose, and while presenting the script ticks once
// the slide's transition is over (app.scriptArriving). A frame over
// BUDGET_MS three times running stops it, and the slide shows where it ends.
import { RUNTIME } from "./cerxes-runtime.js";
import { DECK_RUNTIME } from "./apps-runtime.js";
import { workerUrl } from "./sitescript.js";
import { SCRIPT_RUNTIME } from "./script-runtime.js";
import { SPRITE_RUNTIME } from "./sprite-runtime.js";

const LIMIT_MS = 3000;
// the first load fetches and compiles the engine
const FIRST_LIMIT_MS = 15000;
// deck.set lays the deck out again (its headers and footers): at most this often
const SET_EVERY_MS = 400;
// a slide's script: its frame's time in the engine, and how many in a row
// may take longer
export const BUDGET_MS = 4;
const OVER_IN_A_ROW = 3;

// __calibrate's time on a fast machine: a slower engine gets a budget as
// much larger, up to four times
export const CALIB_REF_MS = 0.4;
/** A script's time budget a frame, from how long __calibrate took here. */
export function scriptBudget(calib) {
  if (!(calib > 0) || !isFinite(calib)) return BUDGET_MS;
  return BUDGET_MS * Math.min(4, Math.max(1, calib / CALIB_REF_MS));
}

/** Whether a frame's time ends a script: `over` frames over budget in a row so far. */
export function overBudget(over, ms, budget = BUDGET_MS) {
  const next = ms > budget ? over + 1 : 0;
  return { over: next, stop: next >= OVER_IN_A_ROW };
}

/**
 * The slide's entities handed over before the script itself runs, so a
 * find() at its top level (const bars = find("chart:1 bar")) finds them.
 */
export function treeFirst(tree) {
  return tree ? "\n__setTree(" + tree + ");" : "";
}

/**
 * __scriptOpen's argument: the deck as the script's slide will have it when
 * it arrives from the slide before it, at build step `step` (the editor's
 * first; the viewer shows every step). state: the deck's playState (JSON
 * text), tree: the slide's entities (JSON text).
 */
export function openArg(state, tree, reduced = false, step = 0) {
  let deck = {};
  try { deck = JSON.parse(state); } catch (_) { /* defaults */ }
  const home = deck.home || 1;
  deck = { ...deck, slide: home, from: home - 1, step };
  return JSON.stringify({ tree: tree ? JSON.parse(tree) : null, deck, env: { reducedMotion: reduced, export: false } });
}

/** A script's frame: what it set (passed on whole) and what it asked. */
export function splitScriptFrame(out) {
  let asks = [];
  let leave = false;
  let take = [];
  try {
    const f = JSON.parse(out);
    if (f && Array.isArray(f.k)) asks = f.k;
    leave = !!(f && f.leave);
    if (f && Array.isArray(f.take)) take = f.take.map(String);
  } catch (_) { /* the deck says it was no frame */ }
  return { frame: out, asks, leave, take };
}

function reducedMotion() {
  try {
    return !!(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  } catch (_) {
    return false;
  }
}

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

  function newRun(key, script = false) {
    return { key, script, worker: null, source: "", running: false, waiting: false, deadline: 0, last: 0, events: [], pointer: { x: 0, y: 0, down: false, inside: false }, held: new Map(), setAt: 0, stopped: "", tree: "", over: 0, leaves: false, leaving: false, budget: BUDGET_MS, take: [], framed: false, opened: false };
  }

  // Where each script ends (__scriptFinal), and how it opens
  // (__scriptOpen), worked out one at a time in a worker of their own, so a
  // thumbnail or the PDF shows it before the slide was ever shown, and the
  // slide arrives as the script starts it.
  const finals = { worker: null, queue: [], busy: null, deadline: 0, done: new Map() };
  // key -> how the script opens: { source, out } (__scriptOpen's frame)
  const opens = new Map();

  // the stage shows script r's slide as the script opens it (until its
  // first frame), when that was worked out for this source
  function applyOpen(r, source) {
    const o = opens.get(r.key);
    if (!r.script || !o || o.source !== source || !app.setScriptOpen) return;
    r.opened = true;
    if (app.setScriptOpen(r.key, o.out)) repaint();
  }

  function finalArg(key, tree) {
    let f = {};
    try { f = JSON.parse(app.scriptFinalArgs(key)); } catch (_) { /* defaults */ }
    return JSON.stringify({ tree: JSON.parse(tree), deck: JSON.parse(app.playState(key)), steps: f.steps || 0, seconds: f.seconds || 0, env: { reducedMotion: reducedMotion(), export: true } });
  }

  // fn: "__scriptFinal" or "__scriptOpen"; how it opens goes first, as the
  // stage needs it before a thumbnail does
  function wantFinal(key, source, tree, fn = "__scriptFinal") {
    const want = source + "\u0000" + tree;
    const job = fn + " " + key;
    if (finals.done.get(job) === want) return;
    if (finals.busy && finals.busy.job === job && finals.busy.want === want) return;
    finals.queue = finals.queue.filter((q) => q.job !== job);
    const q = { key, source, tree, want, fn, job };
    if (fn === "__scriptOpen") finals.queue.unshift(q);
    else finals.queue.push(q);
  }

  function pumpFinals(now) {
    if (finals.busy) {
      if (now <= finals.deadline) return;
      // an endless final(): the slide keeps its Markdown, and says why
      const b = finals.busy;
      finals.busy = null;
      if (finals.worker) finals.worker.terminate();
      finals.worker = null;
      finals.done.set(b.job, b.want);
      say(name({ key: b.key }) + ": " + t("did not reach its end within 3 s (an endless loop?)"));
    }
    const next = finals.queue.shift();
    if (!next) return;
    if (!finals.worker) {
      let w = null;
      try {
        w = new Worker(workerUrl("cerxes-worker.js"), { type: "module" });
      } catch (e) {
        // no engine here: the slide keeps its Markdown, and says why
        finals.done.set(next.job, next.want);
        say(name({ key: next.key }) + ": " + t("the program's engine did not start") + " (" + ((e && e.message) || "worker") + ")");
        return;
      }
      w.onmessage = (ev) => finalReply(w, ev.data);
      w.onerror = () => { if (finals.worker === w) { finals.worker = null; finals.busy = null; } };
      finals.worker = w;
      finals.deadline = now + FIRST_LIMIT_MS;
    } else {
      finals.deadline = now + LIMIT_MS;
    }
    finals.busy = next;
    const arg = next.fn === "__scriptOpen" ? openArg(app.playState(next.key), next.tree, reducedMotion(), app.scriptOpenStep ? app.scriptOpenStep(next.key) : 0) : finalArg(next.key, next.tree);
    finals.worker.postMessage({ type: "final", fn: next.fn, runtime: RUNTIME + "\n" + DECK_RUNTIME + "\n" + SCRIPT_RUNTIME + "\n" + SPRITE_RUNTIME + treeFirst(next.tree), source: next.source, arg });
  }

  function finalReply(w, m) {
    if (finals.worker !== w || !finals.busy) return;
    const b = finals.busy;
    finals.busy = null;
    finals.done.set(b.job, b.want);
    const opening = b.fn === "__scriptOpen";
    if (m.output && !opening) for (const line of m.output.split("\n")) if (line) console.log(name({ key: b.key }) + " (final): " + line);
    if (!m.ok) {
      // the run on the stage says it too; said once
      say(name({ key: b.key }) + ": " + m.error);
      return;
    }
    if (opening) {
      opens.set(b.key, { source: b.source, out: m.out });
      // arrived late: the slide is on the stage with no frame yet
      const r = runs.get(b.key);
      if (r && r.worker && !r.framed) applyOpen(r, r.source);
      return;
    }
    if (app.setScriptFinal(b.key, m.out)) repaint();
  }

  // why: what went wrong, said in a toast and on the program's plate
  function stopRun(r, why) {
    if (r.worker) r.worker.terminate();
    r.worker = null;
    r.running = false;
    r.waiting = false;
    r.over = 0;
    r.tree = "";
    r.leaves = false;
    r.leaving = false;
    r.take = [];
    r.framed = false;
    r.opened = false;
    if (r.script && app.endScriptLive) {
      app.endScriptLive(r.key, why || "");
      repaint();
    }
    if (why) {
      r.stopped = why;
      say(name(r) + ": " + why);
      if (app.setPlayStopped) {
        app.setPlayStopped(r.key, why);
        repaint();
      }
    }
  }

  function say(line) {
    if (told.has(line)) return;
    told.add(line);
    toast(line);
  }

  function load(r, source) {
    stopRun(r, "");
    // (stopping cleared the stage's frames) the slide as the script opens it
    applyOpen(r, source);
    r.source = source;
    r.stopped = "";
    r.events = [];
    // with the build's stamp (?v=…), as every import of ours has
    let w = null;
    try {
      w = new Worker(workerUrl("cerxes-worker.js"), { type: "module" });
    } catch (e) {
      stopRun(r, t("the program's engine did not start") + " (" + ((e && e.message) || "worker") + ")");
      return;
    }
    w.onmessage = (ev) => reply(r, w, ev.data);
    w.onerror = (e) => {
      if (r.worker === w) stopRun(r, t("the program's engine did not start") + " (" + (e.message || "worker") + ")");
    };
    r.worker = w;
    r.waiting = true;
    r.deadline = performance.now() + FIRST_LIMIT_MS;
    w.postMessage({ type: "load", runtime: RUNTIME + "\n" + DECK_RUNTIME + (r.script ? "\n" + SCRIPT_RUNTIME + "\n" + SPRITE_RUNTIME + treeFirst(app.scriptTree(r.key)) : ""), source, calibrate: r.script });
  }

  function name(r) {
    return r.key.replace(/^script:/, "").replace(/#\d+$/, "");
  }

  function reply(r, w, m) {
    if (r.worker !== w) return;
    r.waiting = false;
    if (m.output) for (const line of m.output.split("\n")) if (line) console.log(name(r) + ": " + line);
    if (!m.ok) {
      stopRun(r, m.error);
      return;
    }
    if (m.type === "loaded") {
      r.running = true;
      if (r.script) r.budget = scriptBudget(m.calib);
      if (app.setPlayStopped) app.setPlayStopped(r.key, "");
      r.last = performance.now();
      return;
    }
    let tree, asks;
    // onLeave's frame: how the slide looks while the next one arrives
    if (r.script && r.leaving) {
      if (app.setScriptLeave) app.setScriptLeave(r.key, m.out);
      stopRun(r, "");
      repaint();
      return;
    }
    if (r.script) {
      const b = overBudget(r.over, m.ms || 0, r.budget);
      r.over = b.over;
      if (b.stop) {
        stopRun(r, t("took over its time a frame three times in a row, so it was stopped and the slide shows where it ends") + " (" + r.budget.toFixed(1) + " ms)");
        return;
      }
      const f = splitScriptFrame(m.out);
      r.leaves = f.leave;
      r.take = f.take;
      if (!app.setScriptFrame(r.key, f.frame)) {
        stopRun(r, t("its frame was not one a script gives"));
        return;
      }
      r.framed = true;
      asks = JSON.stringify(f.asks);
    } else {
      ({ tree, asks } = splitFrame(m.out));
      if (!app.setPlayFrame(r.key, tree)) {
        stopRun(r, t("view() did not give an element tree"));
        return;
      }
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
    pumpFinals(now);
    const shown = new Set(shownKeys());
    const focus = app.playFocus();
    if (focus && !shown.has(focus)) release();
    if (skipKey && !shown.has(skipKey)) skipKey = "";
    for (const p of plays) {
      if (!p.loaded) continue;
      let r = runs.get(p.key);
      const source = app.playSource(p.key);
      if (!r) {
        r = newRun(p.key, !!p.script);
        runs.set(p.key, r);
      }
      if (r.script) {
        // where it ends, for the slide while it is not running
        const tree = app.scriptTree(p.key);
        // (a page without setScriptFinal had it worked out for it: the viewer)
        if (tree && app.setScriptFinal) wantFinal(p.key, source, tree);
        if (tree && app.setScriptOpen) wantFinal(p.key, source, tree, "__scriptOpen");
        // a script runs only while its slide is on the stage, from the start
        // each time it comes back
        if (!shown.has(p.key)) {
          // onLeave first, then it stops (reply)
          if (r.leaving) {
            if (now > r.deadline) stopRun(r, "");
            continue;
          }
          if (r.worker && r.running && r.leaves && !r.waiting && app.setScriptLeave) {
            const state = JSON.parse(app.playState(p.key));
            r.leaving = true;
            r.waiting = true;
            r.deadline = now + LIMIT_MS;
            r.worker.postMessage({ type: "frame", fn: "__scriptLeave", arg: JSON.stringify({ deck: state, to: state.slide || 0, env: { reducedMotion: reducedMotion() } }) });
            continue;
          }
          if (r.worker && !r.waiting) stopRun(r, "");
          else if (r.worker && now > r.deadline) stopRun(r, "");
          if (r.stopped && source !== r.source) r.stopped = "";
          continue;
        }
      }
      if (r.stopped && source === r.source) continue;
      // the slide arrives as the script opens it, before its engine has
      // given a frame (it may still be loading)
      if (shown.has(p.key) && !r.framed && !r.opened) applyOpen(r, source);
      // a program starts the first time its slide is shown, and again
      // whenever its file changes
      if (source !== r.source) {
        if (shown.has(p.key) || r.source) load(r, source);
        continue;
      }
      if (r.waiting) {
        if (now > r.deadline) stopRun(r, t("did not answer within 3 s (an endless loop?), so it was stopped"));
        continue;
      }
      if (!r.worker) {
        if (shown.has(p.key)) load(r, source);
        continue;
      }
      if (!r.running || !shown.has(p.key) || document.hidden) continue;
      // its slide still coming in: it holds its opening pose until then
      if (r.script && app.scriptArriving && app.scriptArriving(p.key)) {
        r.last = now;
        continue;
      }
      flushSets(r);
      const dt = Math.min(0.1, (now - r.last) / 1000);
      r.last = now;
      const mine = focus === p.key;
      const frame = {
        w: p.w, h: p.h, dt, time: now / 1000,
        keys: r.script ? scriptKeys : mine ? keys : {},
        pointer: r.pointer,
        events: r.events,
        deck: JSON.parse(app.playState(p.key)),
      };
      if (r.script) {
        // the entities again only when an edit changed them
        const tree = app.scriptTree(p.key);
        if (tree !== r.tree) {
          frame.tree = JSON.parse(tree);
          r.tree = tree;
        }
        frame.env = { reducedMotion: reducedMotion() };
      }
      const arg = JSON.stringify(frame);
      r.events = [];
      r.waiting = true;
      r.deadline = now + LIMIT_MS;
      r.worker.postMessage(r.script ? { type: "frame", arg, fn: "__scriptFrame" } : { type: "frame", arg });
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
    for (const r of runs.values()) {
      if (!r.script || !r.running) continue;
      const at = app.scriptHit(r.key, x, y);
      if (!at) continue;
      const p = JSON.parse(at);
      Object.assign(r.pointer, { x: p.x, y: p.y, down: true, inside: true });
      // onClick takes the press while presenting; otherwise it is the deck's
      if (presenting && app.scriptOwnsClick(r.key)) {
        r.events.push({ type: "click", id: p.id, x: p.x, y: p.y });
        return true;
      }
    }
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
    for (const r of runs.values()) {
      if (!r.script || !r.running) continue;
      const at = app.scriptHit(r.key, x, y);
      if (at) {
        const p = JSON.parse(at);
        Object.assign(r.pointer, { x: p.x, y: p.y, inside: true });
      } else r.pointer.inside = false;
    }
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
  // A running script hears the keys (onKeyDown) but does not take them: the
  // arrows still move the presentation, unless it took that key
  // (input.take). True when a running script took it.
  const scriptKeys = {};
  function scriptKey(ev, type) {
    if (ev.ctrlKey || ev.metaKey || ev.altKey) return false;
    if (type === "keydown") scriptKeys[ev.key] = true;
    else delete scriptKeys[ev.key];
    let taken = false;
    for (const r of runs.values()) {
      if (!r.script || !r.running) continue;
      if (r.take.includes(ev.key)) taken = true;
      if (type === "keydown" && ev.repeat) continue;
      r.events.push({ type, key: ev.key });
    }
    return taken;
  }

  function keyDown(ev) {
    if (scriptKey(ev, "keydown")) return true;
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
    if (scriptKey(ev, "keyup")) return true;
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
    for (const k in scriptKeys) delete scriptKeys[k];
  });

  // Another deck: no program of the last one goes on running.
  function reset() {
    for (const r of runs.values()) stopRun(r, "");
    runs.clear();
    if (finals.worker) finals.worker.terminate();
    finals.worker = null;
    finals.queue = [];
    finals.busy = null;
    finals.done.clear();
    plays = [];
    playsRev = -1;
    told.clear();
    skipKey = "";
    for (const k in keys) delete keys[k];
  }

  return { tick, pointerDown, pointerMove, pointerUp, reset, running: () => [...runs.values()].filter((r) => r.running).map((r) => r.key) };
}
