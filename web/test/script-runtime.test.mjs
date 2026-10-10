// node --test: a slide's script ({script=…} on its heading). The runtime the
// script runs with (web/script-runtime.js) finds the same entities as the
// deck's own selectors (PresSel, src/PresSel.rgr, compiled into both) on a
// real slide, and its frames carry what the hooks set.
//
// Runs the built app and runtime (web/dist, `npm run build`). The runtime is
// plain JavaScript, so it runs here in a vm context as it runs in CErXes.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";
import { DECK_RUNTIME } from "../apps-runtime.js";
import { SCRIPT_RUNTIME } from "../dist/script-runtime.js";

const dist = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "dist");
const appJs = path.join(dist, "pres_app.js");
if (!fs.existsSync(appJs)) throw new Error("web/dist/pres_app.js is missing: run `npm run build` first");
(0, eval)(fs.readFileSync(appJs, "utf8"));
const css = fs.readFileSync(path.join(dist, "themes", "aurora.css"), "utf8");

const MD = "---\nslide-split-level: 2\n---\n\n" +
  "## Sales grow {script=apps/fx.tsx}\n\n- Saturday record\n- Buns sell\n\nCoffee pays\n{#kahvi .key}\n\n" +
  '```vega-lite\n{"data":{"values":[{"k":"Mon","n":4},{"k":"Tue","n":7},{"k":"Wed","n":5}]},"mark":"bar","encoding":{"x":{"field":"k","type":"nominal"},"y":{"field":"n","type":"quantitative"}}}\n```\n\n' +
  "## Flow {script=apps/flow.tsx}\n\n```mermaid\nflowchart LR\n  A[Order] --> B[Bake]\n  B --> C[Sell]\n```\n";

function openDeck() {
  const app = new globalThis.PresApp();
  app.setPageSize(1440, 900);
  app.setStyleSheet(css);
  app.setSource(MD);
  app.settleAll(); // the charts drawn, as on a shown slide
  return app;
}

function runtime(source = "") {
  const ctx = vm.createContext({ console, JSON, Math });
  vm.runInContext(DECK_RUNTIME + "\n" + SCRIPT_RUNTIME + "\n" + source, ctx);
  return ctx;
}

const SELECTORS = [
  "h2", "li", "li:2", "#kahvi", "p.key", ".key", "h2 word", "h2 word:2", "h2 char", "li word", "chart:1 bar",
  "chart bar:3", "chart:2 bar", "chart label", "li marker", "li:2 marker", "*", "slide", "li:9",
];
const FLOW_SELECTORS = ["diagram:1 node#B", "diagram node", "edge A->B", "edge B->C", "node#C", "edge"];

test("the runtime's find() names what PresSel names", () => {
  const app = openDeck();
  for (const [slide, list] of [[0, SELECTORS], [1, FLOW_SELECTORS]]) {
    const key = app.deck.scriptKeyOf(slide);
    const tree = JSON.parse(app.scriptTree(key));
    const rt = runtime();
    rt.__setTree(tree);
    for (const sel of list) {
      const mine = Array.from(rt.find(sel)).map((e) => e.id);
      const deck = Array.from(app.deck.findOnSlide(slide, sel));
      assert.deepEqual(mine, deck, `find("${sel}") on slide ${slide + 1}`);
    }
  }
  // and they name something
  assert.equal(app.deck.findOnSlide(0, "chart:1 bar").length, 3);
  assert.equal(app.deck.findOnSlide(1, "edge A->B").length, 1);
  assert.equal(app.deck.findOnSlide(0, "li marker").length, 2, "an item's bullet is its own part");
  const tree = JSON.parse(app.scriptTree(app.deck.scriptKeyOf(0)));
  assert.equal(tree.find((e) => e.id === "li-1").t, "Saturday record", "and not the item's text");
});

test("a frame carries what tick, build and onClick set", () => {
  const app = openDeck();
  const tree = JSON.parse(app.scriptTree(app.deck.scriptKeyOf(0)));
  const rt = runtime(`
    var t = 0, clicked = "";
    function tick(dt) { t += dt; find("li").each(function (e, i) { e.set({ y: e.box.y + 10 * i, opacity: Math.min(1, t) }); }); }
    function build(n) { find("chart:1 bar").set({ scale: n }); }
    function onClick(e) { clicked = e ? e.id : "none"; add("text", { id: "said", text: clicked, x: 10, y: 10 }); }
  `);
  const out = JSON.parse(rt.__scriptFrame(JSON.stringify({ tree, dt: 0.5, deck: { step: 1 }, events: [{ type: "click", id: "li-2" }] })));
  assert.equal(out.build, true);
  assert.equal(out.click, true);
  assert.equal(out.p["li-2"].opacity, 0.5);
  assert.equal(out.p["li-2"].y, tree.find((e) => e.id === "li-2").b[1] + 10);
  assert.equal(out.p["chart-1/bar-1"].scale, 1);
  assert.deepEqual(out.a, [{ id: "said", k: "text", text: "li-2", x: 10, y: 10 }]);
  // reset: the entity is as the Markdown has it again
  rt.find("li:2")[0].reset();
  const next = JSON.parse(rt.__scriptFrame(JSON.stringify({ dt: 0, deck: { step: 1 }, events: [] })));
  assert.equal(next.p["li-2"].opacity, 0.5, "tick sets it again");
});

test("the final frame: final(), else the ticks run out", () => {
  const app = openDeck();
  const tree = JSON.parse(app.scriptTree(app.deck.scriptKeyOf(0)));
  const ticking = runtime(`var t = 0; function tick(dt) { t += dt; find("h2").set({ opacity: Math.min(1, t / 2) }); }`);
  const a = JSON.parse(ticking.__scriptFinal(JSON.stringify({ tree, seconds: 3, steps: 0 })));
  assert.equal(a.p["h2-1"].opacity, 1);
  // build steps taken one after another, as on the stage: what step 1 set
  // is still there after step 2
  const building = runtime(`var seen = []; function build(n) { seen.push(n); if (n === 1) find("li:1").set({ opacity: 0.3 }); if (n === 2) find("li:2").set({ opacity: 0.6 }); }`);
  const s = JSON.parse(building.__scriptFinal(JSON.stringify({ tree, seconds: 0, steps: 2 })));
  assert.deepEqual(Array.from(building.seen), [0, 1, 2]);
  assert.equal(s.p["li-1"].opacity, 0.3);
  assert.equal(s.p["li-2"].opacity, 0.6);
  const ending = runtime(`function tick() { find("h2").set({ opacity: 0.2 }); } function final() { find("h2").set({ color: "#ff0000" }); }`);
  const b = JSON.parse(ending.__scriptFinal(JSON.stringify({ tree, seconds: 3 })));
  assert.deepEqual(b.p["h2-1"], { color: "#ff0000" });
});

test("a find() at the script's top level finds the slide's entities", async () => {
  const { treeFirst } = await import("../dist/apps.js");
  const app = openDeck();
  const tree = app.scriptTree(app.deck.scriptKeyOf(0));
  const ctx = vm.createContext({ console, JSON, Math });
  vm.runInContext(DECK_RUNTIME + "\n" + SCRIPT_RUNTIME + treeFirst(tree) + `
    const items = find("li");
    function tick() { items.set({ opacity: 0.25 }); }
  `, ctx);
  assert.equal(vm.runInContext("items.length", ctx), 2);
  const out = JSON.parse(ctx.__scriptFrame(JSON.stringify({ tree: JSON.parse(tree), dt: 0.1, deck: {}, events: [] })));
  assert.equal(out.p["li-2"].opacity, 0.25);
});

test("onEnter hears where the slide came from, onLeave where it goes", () => {
  const app = openDeck();
  const key = app.deck.scriptKeyOf(0);
  const tree = JSON.parse(app.scriptTree(key));
  const rt = runtime(`
    var came = -1;
    function onEnter(from) { came = from; find("h2").set({ opacity: 0.5 }); }
    function onLeave(to) { find("h2").set({ x: to * 100 }); }
  `);
  const first = JSON.parse(rt.__scriptFrame(JSON.stringify({ tree, dt: 0, deck: { slide: 1, from: 3 }, events: [] })));
  assert.equal(rt.came, 3);
  assert.equal(first.p["h2-1"].opacity, 0.5);
  assert.equal(first.leave, true, "the page asks for onLeave's frame");
  const left = JSON.parse(rt.__scriptLeave(JSON.stringify({ deck: { slide: 2 }, to: 2 })));
  assert.equal(left.p["h2-1"].x, 200);
  // the editor draws it while the next slide arrives, until the script runs again
  assert.ok(app.setScriptLeave(key, JSON.stringify(left)));
  assert.equal(app.deck.scriptRuns[0].hasLeave, true);
  assert.ok(app.setScriptFrame(key, JSON.stringify(first)));
  assert.equal(app.deck.scriptRuns[0].hasLeave, false);
});

test("find() walks the entities once per selector, set() says an unknown key, input.take keeps keys", () => {
  const app = openDeck();
  const tree = JSON.parse(app.scriptTree(app.deck.scriptKeyOf(0)));
  const logged = [];
  const ctx = vm.createContext({ console: { log: (l) => logged.push(String(l)) }, JSON, Math });
  vm.runInContext(DECK_RUNTIME + "\n" + SCRIPT_RUNTIME + `
    input.take("ArrowRight", "Escape");
    function tick() { find("li").set({ opacity: 0.5, wobble: 2 }); }
  `, ctx);
  ctx.__setTree(tree);
  let walks = 0;
  const all = ctx.__findAll;
  ctx.__findAll = (sel, scope) => { walks++; return all(sel, scope); };
  vm.runInContext("__findAll = this.__findAll", ctx);
  const a = ctx.find("li");
  a.pop();
  const b = ctx.find("li");
  assert.equal(walks, 1, "the second find() is the first one's");
  assert.equal(b.length, a.length + 1, "and a list of its own");
  const out = JSON.parse(ctx.__scriptFrame(JSON.stringify({ dt: 0.1, deck: {}, events: [] })));
  assert.deepEqual(out.take, ["ArrowRight"], "Escape is never taken");
  assert.equal(out.p["li-1"].opacity, 0.5);
  assert.equal(out.p["li-1"].wobble, undefined);
  assert.equal(logged.filter((l) => /"wobble" is no property/.test(l)).length, 1, "said once");
  ctx.__setTree(tree);
  ctx.find("li");
  assert.equal(walks, 2, "new entities, a new walk");
});

test("the frame laid over the slide moves what it names", () => {
  const app = openDeck();
  const key = app.deck.scriptKeyOf(0);
  assert.ok(app.setScriptFinal(key, JSON.stringify({ p: { "li-2": { y: 0 } } })));
  assert.equal(app.deck.scriptRuns[0].hasFinal, true);
});

test("a selector that is no selector is said, and finds nothing", () => {
  const rt = runtime();
  rt.__setTree([{ id: "slide", k: "slide", b: [0, 0, 960, 540], i: 0, p: -1 }]);
  assert.equal(rt.find("li:x").length, 0);
  assert.ok(rt.__Sel.PresSel.parse("li:x").error);
  assert.ok(rt.__Sel.PresSel.parse("A->B").error);
});

test("a script over its budget three frames in a row is stopped", async () => {
  const { overBudget } = await import("../dist/apps.js");
  let s = { over: 0, stop: false };
  s = overBudget(s.over, 5);
  s = overBudget(s.over, 6);
  assert.equal(s.stop, false);
  s = overBudget(s.over, 1);
  assert.equal(s.over, 0, "a quick frame starts the count again");
  for (const ms of [5, 5, 5]) s = overBudget(s.over, ms);
  assert.equal(s.stop, true);
  // a slower engine (its __calibrate took longer) gets more time, up to 4×
  const { scriptBudget, BUDGET_MS, CALIB_REF_MS } = await import("../dist/apps.js");
  assert.equal(scriptBudget(0), BUDGET_MS);
  assert.equal(scriptBudget(CALIB_REF_MS / 2), BUDGET_MS);
  assert.equal(scriptBudget(CALIB_REF_MS * 2), BUDGET_MS * 2);
  assert.equal(scriptBudget(CALIB_REF_MS * 10), BUDGET_MS * 4);
  assert.equal(overBudget(0, 6, scriptBudget(CALIB_REF_MS * 2)).over, 0, "6 ms is within 8");
});

// the runtime with defineModule as CErXes has it: the modules it defined
function moduleRuntime(source = "") {
  const modules = {};
  const ctx = vm.createContext({ console: { log() {} }, JSON, Math, defineModule: (name, m) => { modules[name] = m; return m; } });
  vm.runInContext(DECK_RUNTIME + "\n" + SCRIPT_RUNTIME + "\n" + source, ctx);
  return { ctx, modules };
}

test('the "Sliqtly" module: presentation.activeSlide finds what find() finds', () => {
  const app = openDeck();
  const tree = JSON.parse(app.scriptTree(app.deck.scriptKeyOf(0)));
  const { ctx, modules } = moduleRuntime();
  const S = modules.Sliqtly;
  assert.ok(S, "the runtime defines it");
  ctx.__setTree(tree);
  ctx.__deckState({ slide: 3, slides: 7, step: 1, from: 2, mode: "present" });
  const slide = S.presentation.activeSlide;
  assert.deepEqual(Array.from(slide.find("li")).map((e) => e.id), Array.from(ctx.__find("li", null)).map((e) => e.id));
  assert.equal(slide.find("li").length, 2);
  assert.equal(slide.index, 3);
  assert.equal(slide.step, 1);
  assert.equal(slide.from, 2);
  assert.equal(S.presentation.slides, 7);
  assert.equal(slide.tree().id, "slide");
  assert.equal(typeof S.input.take, "function");
  assert.equal(S.env.export, false);
});

test("the old globals still work and say once that they are the old form", () => {
  const app = openDeck();
  const tree = JSON.parse(app.scriptTree(app.deck.scriptKeyOf(0)));
  const logged = [];
  const ctx = vm.createContext({ console: { log: (l) => logged.push(String(l)) }, JSON, Math });
  vm.runInContext(DECK_RUNTIME + "\n" + SCRIPT_RUNTIME, ctx);
  ctx.__setTree(tree);
  assert.equal(ctx.find("li").length, 2);
  ctx.find("h2");
  assert.equal(logged.filter((l) => /find\(\) is the old form/.test(l)).length, 1);
});

// The declarations sliqtly_guide(topic=script-api) gives: every member of
// Presentation and ActiveSlide is on the runtime's objects.
test("the script-api declarations name what the runtime has", () => {
  const guide = fs.readFileSync(path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..", "mcp-go", "assets", "guide.md"), "utf8");
  const members = (name) => {
    const m = guide.match(new RegExp("interface " + name + " \\{([\\s\\S]*?)\\n\\}"));
    assert.ok(m, "interface " + name);
    return [...m[1].matchAll(/^\s+(?:readonly )?([a-zA-Z]+)\??[(:<]/gm)].map((x) => x[1]);
  };
  const { ctx, modules } = moduleRuntime();
  ctx.__setTree([{ id: "slide", k: "slide", b: [0, 0, 960, 540], i: 0, p: -1 }]);
  const S = modules.Sliqtly;
  for (const k of members("Presentation")) assert.ok(k in S.presentation, "presentation." + k);
  for (const k of members("ActiveSlide")) assert.ok(k in S.presentation.activeSlide, "activeSlide." + k);
  for (const k of members("Input")) assert.ok(k in S.input, "input." + k);
  for (const k of members("Env")) assert.ok(k in S.env, "env." + k);
  const exported = [...guide.matchAll(/^\s+export const (\w+)/gm)].map((x) => x[1]);
  assert.deepEqual(exported, ["presentation", "input", "env"]);
  for (const k of exported) assert.ok(S[k], k);
});

test("how a script opens: start, onEnter and build, no tick; the stage shows it until the first frame", async () => {
  const { openArg } = await import("../dist/apps.js");
  const app = openDeck();
  const key = app.deck.scriptKeyOf(0);
  const treeText = app.scriptTree(key);
  const rt = runtime(`
    var came = -1, built = [];
    function start() { find("li").set({ opacity: 0 }); }
    function onEnter(from) { came = from; }
    function build(n) { built.push(n); }
    function tick() { find("li").set({ opacity: 1 }); }
  `);
  const arg = JSON.parse(openArg(JSON.stringify({ home: 3, slide: 1, from: 0, step: 4 }), treeText));
  assert.deepEqual([arg.deck.slide, arg.deck.from, arg.deck.step], [3, 2, 0], "as it arrives from the slide before");
  const out = JSON.parse(rt.__scriptOpen(JSON.stringify(arg)));
  assert.equal(out.p["li-1"].opacity, 0, "no tick ran");
  assert.equal(rt.came, 2);
  assert.deepEqual(Array.from(rt.built), [0]);
  // the editor draws it on the stage until the script's first frame
  assert.ok(app.setScriptOpen(key, JSON.stringify(out)));
  const run = app.deck.scriptRuns[0];
  assert.equal(run.hasOpen, true);
  app.deck.showControls = true;
  assert.equal(app.deck.scriptFrameFor(0), run.open);
  assert.ok(app.setScriptFrame(key, JSON.stringify({ p: { "li-1": { opacity: 1 } } })));
  assert.equal(run.hasOpen, false);
  assert.ok(app.setScriptOpen(key, JSON.stringify(out)));
  app.endScriptLive(key, "");
  assert.equal(run.hasOpen, false, "and not after it left the stage");
});
