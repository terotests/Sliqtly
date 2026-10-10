// node --test: the presenter in a slide's script (web/presenter-runtime.js).
// A script makes it, fades it in, has it say a paragraph and lines, and its
// lines are held exactly as long as a `::: story` slide holds them (the same
// PresStory code, compiled into script-sel.js).
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
import { PRESENTER_RUNTIME } from "../dist/presenter-runtime.js";

const dist = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "dist");
const appJs = path.join(dist, "pres_app.js");
if (!fs.existsSync(appJs)) throw new Error("web/dist/pres_app.js is missing: run `npm run build` first");
(0, eval)(fs.readFileSync(appJs, "utf8"));
const css = fs.readFileSync(path.join(dist, "themes", "aurora.css"), "utf8");

const MD = "---\nslide-split-level: 2\n---\n\n" +
  "## Finding {script=apps/talk.tsx}\n\nconformanceIssues() says whether the PDF is fit for print.\n\n- a point\n";

function openDeck() {
  const app = new globalThis.PresApp();
  app.setPageSize(1440, 900);
  app.setStyleSheet(css);
  app.setSource(MD);
  app.settleAll();
  return app;
}

function runtime(app, source) {
  const logged = [];
  const ctx = vm.createContext({ console: { log: (s) => logged.push(String(s)) }, JSON, Math });
  vm.runInContext(DECK_RUNTIME + "\n" + SCRIPT_RUNTIME + "\n" + SPRITE_RUNTIME + "\n" + PRESENTER_RUNTIME, ctx);
  const tree = JSON.parse(app.scriptTree(app.deck.scriptKeyOf(0)));
  ctx.__setTree(tree);
  vm.runInContext(source, ctx);
  ctx.logged = logged;
  return { ctx, tree };
}

const frame = (ctx, dt) => JSON.parse(ctx.__scriptFrame(JSON.stringify({ dt })));
const el = (out) => out.a.find((a) => a.k === "presenter");
const beatSecs = (ctx, text) => ctx.__Story.PresStory.beatSeconds(ctx.__Story.PresStory.parse(text)[0]);

test("hidden until show(), then faded in from the right", () => {
  const { ctx } = runtime(openDeck(), "var p; function start() { p = presenter.create().show({ fade: 1, from: 'right' }); }");
  let out = frame(ctx, 0);
  assert.equal(el(out).opacity, 0);
  assert.equal(el(out).visible, false);
  out = frame(ctx, 0.5);
  assert.ok(el(out).opacity > 0.3 && el(out).opacity < 0.7, "half way: " + el(out).opacity);
  assert.ok(el(out).x > 0, "still coming in from the right");
  out = frame(ctx, 0.6);
  assert.equal(el(out).opacity, 1);
  assert.equal(el(out).x, 0, "where it stands");
  assert.equal(el(out).w, 960, "the slide it stands on");
});

test("say(paragraph) takes its words and hides it; each line is held as long as a story line", () => {
  const { ctx } = runtime(openDeck(),
    "var p; function start() { p = presenter.create({ visible: true }).say(presentation.activeSlide.find('p').first()).say('But in RGB mode it is zero.', { pose: 'chin' }); }");
  let out = frame(ctx, 0);
  const para = Array.from(ctx.__find("p", null))[0];
  assert.equal(out.p[para.id].opacity, 0, "the paragraph is hidden: the bubble says it");
  const first = "conformanceIssues() says whether the PDF is fit for print.";
  assert.equal(el(out).story, first + "\nBut in RGB mode it is zero. {pose=chin}");
  assert.equal(el(out).shown, 1);
  const hold = beatSecs(ctx, first);
  out = frame(ctx, hold - 0.05);
  assert.equal(el(out).shown, 1, "still the first line");
  out = frame(ctx, 0.1);
  assert.equal(el(out).shown, 2, "the second after the first was read");
  assert.ok(el(out).since < 0.1);
  assert.equal(ctx.p.plan().end, hold + beatSecs(ctx, "But in RGB mode it is zero. {pose=chin}"));
});

test("a call made later starts then, not at the end of what was before", () => {
  const { ctx } = runtime(openDeck(), "var p; function start() { p = presenter.create({ visible: true }).say('One.'); }");
  frame(ctx, 0);
  frame(ctx, 10);
  ctx.p.say("Two.");
  const plan = ctx.p.plan();
  assert.equal(plan.lines[1].t0, 10, "said when it was asked");
});

test("hide fades it out; the final frame shows where its queue ends", () => {
  const { ctx, tree } = runtime(openDeck(),
    "var hit = 0; function start() { presenter.create().show().say('Hello there.').call(function () { hit++; }).hide({ fade: 0.5 }); }");
  let out = JSON.parse(ctx.__scriptFinal(JSON.stringify({ tree, steps: 0 })));
  assert.equal(ctx.hit, 1);
  assert.equal(el(out).opacity, 0, "hidden at the end");
  const { ctx: c2, tree: t2 } = runtime(openDeck(), "function start() { presenter.create().show().say('One.').say('So two.'); }");
  out = JSON.parse(c2.__scriptFinal(JSON.stringify({ tree: t2, steps: 0 })));
  assert.equal(el(out).shown, 2, "the last line out");
  assert.equal(el(out).opacity, 1);
});

test("a pose that does not exist is said once and left out", () => {
  const { ctx } = runtime(openDeck(), "function start() { presenter.create().say('Hi.', { pose: 'dance' }); }");
  const out = frame(ctx, 0);
  assert.equal(el(out).story, "Hi.");
  assert.ok(ctx.logged.some((l) => /no pose "dance"/.test(l)));
});

test("the host draws it: the character and its bubble in the frame's commands", () => {
  const app = openDeck();
  const key = app.deck.scriptKeyOf(0);
  const { ctx } = runtime(app, "function start() { presenter.create({ visible: true }).say('Niinpä koodi bugaa tässä.'); }");
  const out = frame(ctx, 5);
  assert.ok(app.deck.setScriptFrame(key, JSON.stringify(out)), "a frame the deck takes");
  assert.ok(app.deck.wantFaces.includes("Gloria Hallelujah"), "the bubble's face is asked for");
});
