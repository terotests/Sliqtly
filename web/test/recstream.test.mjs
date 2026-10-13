// node --test: a presentation recorded as display lists and played (web/recstream.js)
import test from "node:test";
import assert from "node:assert/strict";
import { RecEncoder, Recording, RecNav, slidesOf, packStream, unpackStream, streamHash, streamPath } from "../recstream.js";

// two slides: the first 2 s with a build step at 1 s, the second 1 s; 10 fps
const timeline = {
  width: 1920, height: 1080, fps: 10, frames: 30, duration: 3,
  events: [
    { kind: "slide", start: 0, end: 2, slide: 0 },
    { kind: "transition", start: 0, end: 0.5, slide: 0 },
    { kind: "build", start: 1, end: 1.4, slide: 0, step: 1 },
    { kind: "slide", start: 2, end: 3, slide: 1 },
  ],
};
const box = (x, a = 1) => ({ k: 0, x, y: 0, w: 10, h: 10, c: [255, 0, 0, a] });
function record() {
  const enc = new RecEncoder(timeline);
  for (let n = 0; n < 30; n++) {
    // slide 1: a box fades in over 0.5 s, a second comes at the step; slide 2 still
    const cmds = n < 20 ? [box(0, Math.min(1, n / 5))] : [box(50)];
    if (n >= 10 && n < 20) cmds.push(box(20));
    enc.add({ cmds, effects: n >= 20 ? [{ kind: "rain", time: n / 10 }] : [] }, 960, 540);
  }
  return enc.finish();
}

test("the slides' master times and their steps come from the timeline", () => {
  assert.deepEqual(slidesOf(timeline), [{ start: 0, end: 2, steps: [1], enter: 0.5 }, { start: 2, end: 3, steps: [] }]);
});

test("a slide's way in ends where its transition does (its programs run from there)", () => {
  const tl = { events: [{ kind: "slide", start: 0, end: 2, slide: 0 }, { kind: "slide", start: 2, end: 4, slide: 1 }, { kind: "transition", start: 2, end: 2.6, slide: 1 }] };
  assert.deepEqual(slidesOf(tl), [{ start: 0, end: 2, steps: [] }, { start: 2, end: 4, steps: [], enter: 2.6 }]);
});

test("the recording carries its programs for the player", () => {
  const plays = [{ key: "apps/g.tsx#1", slide: 0, src: "apps/g.tsx", box: [0, 0, 10, 10], text: "x" }];
  assert.deepEqual(new RecEncoder(timeline).finish({ plays }).plays, plays);
  assert.deepEqual(record().plays, []);
});

test("a shape is kept once, wherever it is drawn; a frame like the one before is no key", () => {
  const s = record();
  assert.equal(s.sliqrec, 2);
  assert.equal(s.frames, 30);
  assert.equal(s.width, 960);
  // the fading box's 6 looks; the step's box and the second slide's are
  // the opaque one at other places
  assert.equal(s.cmds.length, 6);
  // frames 0..5 fade, 10 adds the step, 20 the next slide
  assert.deepEqual(s.keys.map(([n]) => n), [0, 1, 2, 3, 4, 5, 10, 20]);
  // the effect's clock is not part of it: one change, at the next slide
  assert.deepEqual(s.effects, [[0, []], [20, [{ kind: "rain" }]]]);
  assert.deepEqual(s.scripts, []);
});

test("frame n draws its key's list; a slide's end is its own last frame", () => {
  const rec = new Recording(record());
  assert.equal(rec.list(7).cmds.length, 1);
  assert.equal(rec.list(7).cmds[0].c[3], 1);
  assert.equal(rec.list(12).cmds.length, 2);
  assert.equal(rec.list(25).cmds[0].x, 50);
  assert.equal(rec.list(25).effects[0].kind, "rain");
  assert.equal(rec.frameAt(2, 0), 19);
  assert.equal(rec.frameAt(2, 1), 20);
  assert.equal(rec.frameAt(99), 29);
  assert.equal(rec.slideAt(2.5), 1);
});

test("presenting: the clock stops at the step and at the slide's end", () => {
  const nav = new RecNav(new Recording(record()));
  nav.tick(0.4);
  assert.equal(nav.playing, true);
  nav.tick(5);
  assert.equal(nav.t, 1);
  assert.equal(nav.playing, false);
  // Space: on to the slide's end, not into the next slide
  nav.next();
  nav.tick(5);
  assert.equal(nav.t, 2);
  assert.equal(nav.slide, 0);
  assert.equal(nav.frame(), 19);
  // Space at the end: the next slide from its master time
  nav.next();
  assert.equal(nav.slide, 1);
  assert.equal(nav.t, 2);
  assert.equal(nav.playing, true);
});

test("Space while it moves lands it where it stops", () => {
  const nav = new RecNav(new Recording(record()));
  nav.tick(0.2);
  nav.next();
  assert.equal(nav.t, 1);
  assert.equal(nav.playing, false);
});

test("← → go from slide to slide; past the last one is its end", () => {
  const nav = new RecNav(new Recording(record()));
  nav.nextSlide();
  assert.deepEqual([nav.slide, nav.t], [1, 2]);
  nav.nextSlide();
  assert.deepEqual([nav.slide, nav.t, nav.playing], [1, 3, false]);
  nav.prevSlide();
  assert.deepEqual([nav.slide, nav.t], [0, 0]);
  nav.prevSlide();
  assert.deepEqual([nav.slide, nav.t], [0, 0]);
});

test("autoplay plays through the steps and the slides to the end", () => {
  const nav = new RecNav(new Recording(record()));
  nav.setAuto(true);
  nav.tick(2.5);
  assert.deepEqual([nav.slide, nav.t, nav.playing], [1, 2.5, true]);
  nav.tick(9);
  assert.deepEqual([nav.t, nav.playing], [3, false]);
  // on again at the end: from the start
  nav.setAuto(true);
  assert.deepEqual([nav.slide, nav.t, nav.playing], [0, 0, true]);
});

test("packed as gzip and read back; the name follows the bytes", async () => {
  const s = record();
  const bytes = await packStream(s);
  assert.equal(bytes[0], 0x1f);
  const rec = await unpackStream(bytes);
  assert.equal(rec.frames, 30);
  assert.deepEqual(rec.list(12).cmds, new Recording(s).list(12).cmds);
  const h = await streamHash(bytes);
  assert.match(h, /^[0-9a-f]{24}$/);
  assert.equal(h, await streamHash(bytes));
  assert.equal(streamPath(h), "recordings/web-" + h + ".sliqrec");
  assert.equal(streamPath(h, ".webm"), "recordings/web-" + h + ".webm");
});

test("another format is refused", () => {
  assert.throws(() => new Recording({ sliqrec: 1 }));
});

test("what the light player needs goes with the frames", () => {
  const enc = new RecEncoder(timeline);
  enc.add({ cmds: [{ k: 2, text: "Hi", font: "Lato" }] }, 960, 540);
  const s = enc.finish({ faces: [["Lato", "Lato-Regular.ttf"]], pictures: [{ src: "/media/a.png", type: "image/webp", b64: "AA==" }], deckEffects: "[]" });
  assert.deepEqual(s.faces, [["Lato", "Lato-Regular.ttf"]]);
  assert.equal(s.pictures[0].src, "/media/a.png");
  assert.equal(s.deckEffects, "[]");
  // without them: none
  assert.deepEqual(new RecEncoder(timeline).finish().faces, []);
});

test("points are kept from the command's place, in twentieths of a unit", () => {
  const enc = new RecEncoder(timeline);
  const line = (dx) => ({ k: 6, x: 10 + dx, y: 20, w: 5, h: 5, pts: [10 + dx, 20, 15.03 + dx, 25.5, 12 + dx, 21] });
  enc.add({ cmds: [line(0)] }, 960, 540);
  enc.add({ cmds: [line(100.25)] }, 960, 540);
  const s = enc.finish();
  // one shape at two places
  assert.equal(s.cmds.length, 1);
  assert.deepEqual(s.cmds[0].pts, [0, 0, 101, 110, -61, -90]);
  const rec = new Recording(s);
  const c = rec.list(1).cmds[0];
  assert.equal(c.x, 110.25);
  assert.deepEqual(c.pts.map((v) => Math.round(v * 100) / 100), [110.25, 20, 115.3, 25.5, 112.25, 21]);
});
