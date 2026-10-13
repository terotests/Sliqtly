// The programs on slides stepped for a video (web/videoplays.js): each
// frame's time handed to the program on that frame's slide, its tree set as
// its picture, a new start on its slide's arrival and an end when it goes.
import test from "node:test";
import assert from "node:assert/strict";
import { createVideoPlays, playsOn, videoFrameArg } from "../dist/videoplays.js";

test("a program on the slide, not a slide's script nor another slide's", () => {
  const plays = [{ key: "a#1", slide: 1 }, { key: "s", slide: 1, script: true }, { key: "b#1", slide: 2 }];
  assert.deepEqual(playsOn(plays, 1).map((p) => p.key), ["a#1"]);
});

test("the frame a program gets: the video's time, its own slide, no keys", () => {
  const f = videoFrameArg({ w: 640, h: 480 }, { home: 3, slide: 1, slides: 5, mode: "edit", focused: true }, 1.5, 0.04);
  assert.equal(f.time, 1.5);
  assert.equal(f.dt, 0.04);
  assert.deepEqual(f.keys, {});
  assert.equal(f.deck.slide, 3);
  assert.equal(f.deck.mode, "present");
  assert.equal(f.deck.focused, false);
});

// a worker that answers like cerxes-worker.js: the tree names the time
function fakeWorker(made) {
  const w = {
    loads: 0, frames: [], ended: false,
    postMessage(m) {
      if (m.type === "load") w.loads++;
      else w.frames.push(JSON.parse(m.arg));
      const reply = m.type === "load" ? { type: "loaded", ok: true } : { type: "frame", ok: true, out: JSON.stringify({ t: "div", id: "t" + JSON.parse(m.arg).time }) + "\n[]" };
      queueMicrotask(() => w.onmessage({ data: reply }));
    },
    terminate() { w.ended = true; },
  };
  made.push(w);
  return w;
}

test("each frame steps the slide's program; it starts again when its slide comes back", async () => {
  const set = [];
  const app = {
    playsJson: () => JSON.stringify([{ key: "a#1", slide: 1, w: 100, h: 50 }]),
    playSource: () => "function view() { return null }",
    playState: () => JSON.stringify({ home: 2, slide: 1 }),
    setPlayFrame: (key, tree) => { set.push([key, tree]); return true; },
    setPlayStopped: () => {},
  };
  const made = [];
  const vp = createVideoPlays({ app, makeWorker: () => fakeWorker(made) });
  assert.ok(vp.has());
  await vp.step(0, 0, 0.1);
  assert.equal(made.length, 0);
  await vp.step(1, 0.1, 0.1);
  await vp.step(1, 0.2, 0.1);
  assert.equal(made.length, 1);
  assert.equal(made[0].loads, 1);
  assert.deepEqual(made[0].frames.map((f) => f.time), [0.1, 0.2]);
  assert.deepEqual(set.map(([k, t]) => [k, JSON.parse(t).id]), [["a#1", "t0.1"], ["a#1", "t0.2"]]);
  await vp.step(2, 0, 0.1);
  assert.ok(made[0].ended);
  await vp.step(1, 0, 0.1);
  assert.equal(made.length, 2);
  vp.end();
  assert.ok(made[1].ended);
});

test("a program that does not load is said once and not stepped", async () => {
  const stopped = [];
  const app = {
    playsJson: () => JSON.stringify([{ key: "a#1", slide: 0, w: 1, h: 1 }]),
    playSource: () => "x",
    playState: () => "{}",
    setPlayFrame: () => true,
    setPlayStopped: (k, why) => stopped.push(why),
  };
  const w = { postMessage() { queueMicrotask(() => w.onmessage({ data: { ok: false, error: "SyntaxError" } })); }, terminate() {} };
  const vp = createVideoPlays({ app, makeWorker: () => w });
  await vp.step(0, 0, 0.1);
  await vp.step(0, 0.1, 0.1);
  assert.deepEqual(stopped, ["SyntaxError"]);
});

// presentation.video: a program says how long its slide stays when the deck
// plays on its own, and the page asks every program before a video is timed.
import vm from "node:vm";
import { DECK_RUNTIME } from "../apps-runtime.js";
import { probeVideo, videoSecondsOf } from "../dist/videoplays.js";

test("presentation.video is said once, with the frame after the call", () => {
  const mods = {};
  const ctx = vm.createContext({ JSON, Math, Number, isFinite, defineModule: (name, ex) => { mods[name] = ex; return ex; }, __frame: () => '{"t":"div"}' });
  vm.runInContext(DECK_RUNTIME, ctx);
  mods.Sliqtly.presentation.video({ seconds: 24.5 });
  const asks = (out) => out.slice(out.lastIndexOf("\n") + 1);
  assert.equal(videoSecondsOf(asks(ctx.__deckFrame("{}"))), 24.5);
  assert.equal(asks(ctx.__deckFrame("{}")), "[]");
  mods.Sliqtly.presentation.video(8);
  assert.equal(videoSecondsOf(asks(ctx.__deckFrame("{}"))), 8);
  mods.Sliqtly.presentation.video("x");
  assert.deepEqual(JSON.parse(asks(ctx.__deckFrame("{}"))), [{ k: "video", seconds: 0 }]);
});

test("asks without video, or not JSON, give 0", () => {
  assert.equal(videoSecondsOf('[{"k":"deck.set","key":"a","value":"1"}]'), 0);
  assert.equal(videoSecondsOf("nope"), 0);
  assert.equal(videoSecondsOf('[{"k":"video","seconds":3},{"k":"video","seconds":6}]'), 6);
});

test("probeVideo: each program's first frame, what it asked to the app, the worker ended", async () => {
  const told = [];
  const app = {
    playsJson: () => JSON.stringify([{ key: "a#1", slide: 1, w: 10, h: 10 }, { key: "b#1", slide: 2, w: 10, h: 10 }, { key: "s", slide: 3, script: true }, { key: "c#1", slide: 4 }]),
    playSource: (key) => (key === "c#1" ? "" : "src"),
    playState: () => "{}",
    setPlayVideo: (key, s) => { told.push([key, s]); return key === "a#1"; },
  };
  const made = [];
  const asks = { "a#1": '[{"k":"video","seconds":25}]', "b#1": "[]" };
  let n = 0;
  const keys = ["a#1", "b#1"];
  const makeWorker = () => {
    const key = keys[n++];
    const w = {
      ended: false,
      postMessage(m) {
        const reply = m.type === "load" ? { ok: true } : { ok: true, out: '{"t":"div"}\n' + asks[key] };
        queueMicrotask(() => w.onmessage({ data: reply }));
      },
      terminate() { w.ended = true; },
    };
    made.push(w);
    return w;
  };
  assert.equal(await probeVideo({ app, makeWorker }), true);
  assert.deepEqual(told, [["a#1", 25], ["b#1", 0], ["c#1", 0]]);
  assert.ok(made.length === 2 && made.every((w) => w.ended));
});
