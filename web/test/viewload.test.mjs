// node --test: what the public viewer fetches before a slide, and the order of the rest (web/viewload.js)
import test from "node:test";
import assert from "node:assert/strict";
import { slidesOfPictures, slideDistance, facesFor, PictureQueue } from "../viewload.js";

const lists = [
  { cmds: [{ src: "/media/logo.svg" }, { src: "/media/a.png" }, { text: "Hi", font: "Noto Sans" }] },
  { cmds: [{ src: "/media/logo.svg" }, { src: "/media/b.png" }] },
  { cmds: [{ src: "/media/logo.svg" }, { src: "/media/c.png" }, { text: "Bold", font: "Noto Sans-Bold" }] },
  { cmds: [{ src: "/media/logo.svg" }, { src: "/media/d.png" }] },
];
const pics = ["a.png", "b.png", "c.png", "d.png", "logo.svg", "script.png"].map((n) => ({ src: "/media/" + n }));

// a load the test lets finish when it wants
function loader() {
  const started = [];
  const pending = new Map();
  return {
    started,
    load: (p) => new Promise((resolve, reject) => {
      started.push(p.src);
      pending.set(p.src, { resolve, reject });
    }),
    finish: async (src, ok = true) => {
      const p = pending.get(src);
      pending.delete(src);
      ok ? p.resolve() : p.reject(new Error("HTTP 404"));
      for (let i = 0; i < 5; i++) await Promise.resolve();
    },
  };
}

test("the slides each picture is drawn on", () => {
  const on = slidesOfPictures(lists);
  assert.deepEqual(on.get("/media/logo.svg"), [0, 1, 2, 3]);
  assert.deepEqual(on.get("/media/c.png"), [2]);
  assert.equal(on.get("/media/script.png"), undefined);
});

test("nearest slides first, a slide behind counting double", () => {
  assert.deepEqual([3, 4, 5, 2, 1, 6].map((i) => slideDistance(i, 3)), [0, 1, 2, 2, 4, 3]);
});

test("the faces the slides are set in, and the rest after", () => {
  const faces = [["Open Sans", "o.ttf"], ["Open Sans-Bold", "ob.ttf"], ["Noto Sans", "n.ttf"], ["Noto Sans-Bold", "nb.ttf"]];
  const { need, rest } = facesFor(lists, faces);
  assert.deepEqual(need.map((f) => f[0]), ["Noto Sans", "Noto Sans-Bold"]);
  assert.deepEqual(rest.map((f) => f[0]), ["Open Sans", "Open Sans-Bold"]);
  // text in no named face is set in the first
  assert.deepEqual(facesFor([{ cmds: [{ text: "x" }] }], faces).need.map((f) => f[0]), ["Open Sans"]);
});

test("a slide waits for its own pictures only, the rest come nearest first", async () => {
  const l = loader();
  const loaded = [];
  const q = new PictureQueue(pics, lists, { load: l.load, loaded: (s) => loaded.push(s), at: 0, parallel: 2 });
  // what no list draws first (a script may want it at once), then slide 0's
  assert.deepEqual(l.started, ["/media/script.png", "/media/a.png"]);
  let shown = false;
  q.whenReady(0).then(() => (shown = true));
  await l.finish("/media/script.png");
  assert.deepEqual(l.started.slice(2), ["/media/logo.svg"]);
  // a picture that does not come is done all the same: the slide shows without it
  await l.finish("/media/a.png", false);
  assert.equal(shown, false);
  await l.finish("/media/logo.svg");
  assert.equal(shown, true);
  assert.equal(q.ready(0), true);
  assert.equal(q.ready(1), false);
  assert.deepEqual(l.started.slice(3), ["/media/b.png", "/media/c.png"]);
  assert.deepEqual(q.progress(), { done: 3, of: 6 });
  assert.deepEqual(loaded, ["/media/script.png", "/media/a.png", "/media/logo.svg"]);
});

test("moving to a slide puts its pictures first", async () => {
  const l = loader();
  const q = new PictureQueue(pics, lists, { load: l.load, at: 0, parallel: 1 });
  assert.deepEqual(l.started, ["/media/script.png"]);
  q.focus(3);
  await l.finish("/media/script.png");
  let ready3 = false;
  q.whenReady(3).then(() => (ready3 = true));
  assert.deepEqual(l.started.slice(1), ["/media/logo.svg"]);
  await l.finish("/media/logo.svg");
  assert.equal(ready3, false);
  assert.deepEqual(l.started.slice(2), ["/media/d.png"]);
  await l.finish("/media/d.png");
  assert.equal(ready3, true);
  // behind: slide 2 (one back, counts 2) before slide 1 and slide 0
  await l.finish(l.started[3]);
  await l.finish(l.started[4]);
  assert.deepEqual(l.started.slice(3), ["/media/c.png", "/media/b.png", "/media/a.png"]);
});
