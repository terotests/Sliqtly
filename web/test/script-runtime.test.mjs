// node --test: a slide's script ({script=…} on its heading). The runtime the
// script runs with (web/script-runtime.js) finds the same entities as the
// deck's own selectors (PresSel, src/PresScript.rgr) on a real slide, and its
// frames carry what the hooks set.
//
// Runs the built app (web/dist/pres_app.js, `npm run build`). The runtime is
// plain JavaScript, so it runs here in a vm context as it runs in CErXes.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";
import { DECK_RUNTIME } from "../apps-runtime.js";
import { SCRIPT_RUNTIME } from "../script-runtime.js";

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
  const ending = runtime(`function tick() { find("h2").set({ opacity: 0.2 }); } function final() { find("h2").set({ color: "#ff0000" }); }`);
  const b = JSON.parse(ending.__scriptFinal(JSON.stringify({ tree, seconds: 3 })));
  assert.deepEqual(b.p["h2-1"], { color: "#ff0000" });
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
  assert.ok(rt.__parseSel("li:x").error);
  assert.ok(rt.__parseSel("A->B").error);
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
});
