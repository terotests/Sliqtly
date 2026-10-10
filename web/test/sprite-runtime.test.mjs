// node --test: sprites in a slide's script (web/sprite-runtime.js). A sprite
// stands on the boxes of a real slide, walks and jumps between them as its
// plan says, is drawn as its sheet's frame, and ends where its plan ends.
//
// Runs the built app (web/dist/pres_app.js, `npm run build`), like
// script-runtime.test.mjs.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";
import { DECK_RUNTIME } from "../apps-runtime.js";
import { SCRIPT_RUNTIME } from "../dist/script-runtime.js";
import { SPRITE_RUNTIME } from "../dist/sprite-runtime.js";

const dist = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "dist");
const appJs = path.join(dist, "pres_app.js");
if (!fs.existsSync(appJs)) throw new Error("web/dist/pres_app.js is missing: run `npm run build` first");
(0, eval)(fs.readFileSync(appJs, "utf8"));
const css = fs.readFileSync(path.join(dist, "themes", "aurora.css"), "utf8");

const MD = "---\nslide-split-level: 2\n---\n\n" +
  "## Flow {script=apps/flow.tsx}\n\n```mermaid\nflowchart LR\n  A[Order] --> B[Bake]\n  B --> C[Sell]\n```\n\n" +
  "## List {script=apps/list.tsx}\n\n- Saturday record\n- Buns sell\n";

const SHEET = `sprites.sheet("robot", { src: "sprites/robot.png", grid: [8, 1], frame: [20, 20], feet: 1,
  anims: { idle: { from: 0, frames: 2, fps: 2 }, walk: { from: 2, frames: 4, fps: 8 }, jump: { from: 6, frames: 2, loop: false } } });\n`;

function openDeck() {
  const app = new globalThis.PresApp();
  app.setPageSize(1440, 900);
  app.setStyleSheet(css);
  app.setSource(MD);
  app.settleAll();
  return app;
}

// the runtime with slide `slide`'s entities, and `source` run after it
function runtime(app, slide, source = "") {
  const logged = [];
  const ctx = vm.createContext({ console: { log: (s) => logged.push(String(s)) }, JSON, Math });
  vm.runInContext(DECK_RUNTIME + "\n" + SCRIPT_RUNTIME + "\n" + SPRITE_RUNTIME, ctx);
  const tree = app.scriptTree(app.deck.scriptKeyOf(slide));
  ctx.__setTree(JSON.parse(tree));
  vm.runInContext(SHEET + source, ctx);
  ctx.logged = logged;
  return { ctx, tree };
}

const frame = (ctx, dt, tree) => JSON.parse(ctx.__scriptFrame(JSON.stringify({ dt, tree })));
const imageOf = (out, id = "sprite-1") => out.a.find((a) => a.id === id);
const near = (a, b, d = 0.01) => Math.abs(a - b) <= d;

test("a sprite stands on its box: feet on the top edge, its first frame cut from the sheet", () => {
  const app = openDeck();
  const { ctx } = runtime(app, 0, 'function start() { sprites.add("robot", { on: find("node#A"), size: 100 }); }');
  const out = frame(ctx, 0);
  const a = Array.from(ctx.find("node#A"))[0];
  const img = imageOf(out);
  assert.equal(img.k, "image");
  assert.equal(img.src, "/sprites/robot.png", "a deck file, as the slides name their pictures");
  assert.ok(near(img.x + img.w / 2, a.box.x + a.box.w / 2), "centred on the box");
  assert.ok(near(img.y + img.h - img.h / 20, a.box.y), "its feet (a pixel above the frame's foot) on the box's top");
  assert.deepEqual([img.cropX, img.cropY, img.cropW, img.cropH], [0, 0, 1 / 8, 1]);
  assert.equal(img.flipH, false);
});

test("walkTo on one level hops the gap between two boxes and ends on the target", () => {
  const app = openDeck();
  const { ctx } = runtime(app, 0, 'var r; function start() { r = sprites.add("robot", { on: find("node#A"), size: 100 }); r.walkTo(find("node#B")); }');
  frame(ctx, 0);
  const plan = ctx.r.plan();
  const kinds = plan.legs.map((l) => l.k);
  assert.ok(kinds.includes("jump"), "a hop over the gap: " + kinds.join(" "));
  assert.equal(kinds[0], "walk", "walks to the edge first");
  const b = Array.from(ctx.find("node#B"))[0];
  assert.ok(near(plan.x, b.box.x + b.box.w / 2) && near(plan.y, b.box.y), "ends on B");
  // half way through the hop it is in the air, and the frame is a jump's
  const hop = plan.legs.find((l) => l.k === "jump");
  ctx.__spriteClock = hop.t0 + hop.dur / 2;
  const st = ctx.r.state();
  assert.ok(st.y < b.box.y - 10, "in the air");
  assert.equal(st.anim, "jump");
  const out = frame(ctx, 0);
  assert.ok(imageOf(out).cropX >= 6 / 8, "a jump frame");
});

test("a target on another level is jumped to, and the sprite turns to where it goes", () => {
  const app = openDeck();
  const { ctx } = runtime(app, 1, 'var r; function start() { r = sprites.add("robot", { on: find("li:2"), along: 0.9, size: 80 }); r.jump(find("h2"), { at: 0.1 }); }');
  frame(ctx, 0);
  const plan = ctx.r.plan();
  assert.equal(plan.legs.length, 1);
  assert.equal(plan.legs[0].k, "jump");
  const h2 = Array.from(ctx.find("h2"))[0];
  assert.ok(near(plan.y, h2.box.y), "lands on the heading");
  assert.equal(plan.face, -1, "went left");
  const out = frame(ctx, plan.end + 0.1);
  assert.equal(imageOf(out).flipH, true, "the sheet faces right, so it is drawn mirrored");
});

test("the clock moves sprites with tick or without it, and calls on the way", () => {
  const app = openDeck();
  const { ctx } = runtime(app, 0,
    'var hit = 0; var r; function start() { r = sprites.add("robot", { on: find("node#A") }); r.wait(0.5).call(function () { hit++; find("node#A").set({ scale: 1.2 }); }).wait(0.5); }');
  frame(ctx, 0);
  let out = frame(ctx, 0.3);
  assert.equal(ctx.hit, 0);
  out = frame(ctx, 0.3);
  assert.equal(ctx.hit, 1, "called once its time came");
  const a = Array.from(ctx.find("node#A"))[0];
  assert.equal(out.p[a.id].scale, 1.2, "what it set rides on the frame");
  frame(ctx, 1);
  assert.equal(ctx.hit, 1, "and only once");
  assert.equal(ctx.r.done(), true);
});

test("say shows its words over the sprite for its seconds, then they go", () => {
  const app = openDeck();
  const { ctx } = runtime(app, 0, 'function start() { sprites.add("robot", { on: find("node#A") }).say("Hello", { secs: 1 }); }');
  let out = frame(ctx, 0.1);
  const words = out.a.find((a) => a.id === "sprite-1-words");
  assert.equal(words.text, "Hello");
  assert.equal(words.visible, true);
  assert.ok(words.y < imageOf(out).y, "above the head");
  out = frame(ctx, 1);
  assert.equal(out.a.find((a) => a.id === "sprite-1-words").visible, false);
});

test("the final frame has every sprite where its plan ends, standing still", () => {
  const app = openDeck();
  const key = app.deck.scriptKeyOf(0);
  const tree = app.scriptTree(key);
  const ctx = vm.createContext({ console, JSON, Math });
  vm.runInContext(DECK_RUNTIME + "\n" + SCRIPT_RUNTIME + "\n" + SPRITE_RUNTIME + "\n__setTree(" + tree + ");\n" + SHEET +
    'var r; function start() { r = sprites.add("robot", { on: find("node#A") }); r.walkTo(find("node#C")).call(function () { find("node#C").set({ opacity: 0.5 }); }); }\nfunction final() {}', ctx);
  const out = JSON.parse(ctx.__scriptFinal(JSON.stringify({ tree: JSON.parse(tree), steps: 0 })));
  const c = Array.from(ctx.find("node#C"))[0];
  const img = imageOf(out);
  assert.ok(near(img.x + img.w / 2, c.box.x + c.box.w / 2), "on C");
  assert.equal(img.cropX, 0, "the idle frame");
  assert.equal(out.p[c.id].opacity, 0.5, "what it called on the way was called");
  // the deck draws that frame: the sheet's window and the mirroring reach the command
  assert.ok(app.setScriptFinal(key, JSON.stringify(out)));
  const ad = Array.from(app.deck.scriptRuns[0].final.adds).find((a) => a.id === "sprite-1");
  assert.equal(ad.hasCrop, true);
  assert.ok(near(ad.cropW, 1 / 8));
});

test("the plan follows the boxes when an edit moves them", () => {
  const app = openDeck();
  const { ctx, tree } = runtime(app, 0, 'var r; function start() { r = sprites.add("robot", { on: find("node#A") }); r.walkTo(find("node#B")); }');
  frame(ctx, 0);
  const before = ctx.r.plan().x;
  const moved = JSON.parse(tree).map((e) => (e.u === "B" && e.k === "node" ? { ...e, b: [e.b[0] + 50, e.b[1], e.b[2], e.b[3]] } : e));
  frame(ctx, 0, moved);
  assert.ok(near(ctx.r.plan().x, before + 50), "made again against the moved box");
});

test("a sheet nobody gave is said, and the sprite is not drawn", () => {
  const app = openDeck();
  const { ctx } = runtime(app, 0, 'function start() { sprites.add("ghost", { on: find("node#A") }); }');
  const out = frame(ctx, 0);
  assert.ok(ctx.logged.some((l) => /no sheet "ghost"/.test(l)));
  assert.equal(imageOf(out).visible, false);
});
