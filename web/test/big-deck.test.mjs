// node --test: a deck of 200 slides opens, presents and edits without
// stalling. Only the slides in view have their charts drawn (PresApp.settle,
// MdEmbedCache.lazy); the rest are drawn as they come into view, and all of
// them before a file is written (PresApp.settleAll).
//
// Runs the built app (web/dist/pres_app.js, `npm run build`), the same code a
// tab runs. The time limits are several times what a laptop takes, so a slow
// CI machine passes; the counts are what must not regress.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const dist = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "dist");
const appJs = path.join(dist, "pres_app.js");
if (!fs.existsSync(appJs)) throw new Error("web/dist/pres_app.js is missing: run `npm run build` first");
(0, eval)(fs.readFileSync(appJs, "utf8"));
const css = fs.readFileSync(path.join(dist, "themes", "aurora.css"), "utf8");

const SLIDES = 200;
const now = () => performance.now();

// 200 slides: text, lists, tables, code, and a chart or diagram on every
// third slide or so (40 Vega-Lite charts, 20 Mermaid flowcharts), each
// different: two fences with the same text share one drawing.
function bigDeck(n) {
  let md = "---\ntitle: Iso pakka\n---\n\n";
  let charts = 0;
  for (let i = 1; i <= n; i++) {
    md += `## Dia ${i}\n\n`;
    const k = i % 10;
    if (k === 0) {
      charts++;
      md += "```vega-lite\n" + JSON.stringify({ data: { values: [{ m: "a", n: i }, { m: "b", n: i * 2 }, { m: "c", n: 3 }] }, mark: "bar", encoding: { x: { field: "m", type: "nominal" }, y: { field: "n", type: "quantitative" } } }) + "\n```\n\n";
    } else if (k === 5) {
      charts++;
      md += "```vega-lite\n" + JSON.stringify({ data: { values: Array.from({ length: 20 }, (_, j) => ({ x: j, y: Math.round(Math.sin(j + i) * 100) / 100 })) }, mark: "line", encoding: { x: { field: "x", type: "quantitative" }, y: { field: "y", type: "quantitative" } } }) + "\n```\n\n";
    } else if (k === 3) {
      charts++;
      md += `\`\`\`mermaid\nflowchart LR\n  A[Alku ${i}] --> B{Valinta}\n  B --> C[Yksi]\n  B --> D[Kaksi]\n\`\`\`\n\n`;
    } else if (k === 7) {
      md += "| A | B | C |\n|---|---|---|\n| 1 | 2 | 3 |\n| 4 | 5 | 6 |\n\n";
    } else if (k === 8) {
      md += "```js\nfunction f(x) {\n  return x * 2;\n}\n```\n\n";
    } else {
      md += `Kappale ${i}, jossa on **lihavoitua** ja *kursiivia* tekstiä ja hieman pidempi lause rivitystä varten.\n\n- yksi\n- kaksi\n- kolme\n\n`;
    }
  }
  return { md, charts };
}

const { md, charts } = bigDeck(SLIDES);

function openApp() {
  const app = new globalThis.PresApp();
  app.setPageSize(1440, 900);
  app.setStyleSheet(css);
  const t0 = now();
  app.setSource(md);
  return { app, openMs: now() - t0 };
}

// A presentation's slide plays to rest: its transition and build steps.
function playToRest(app) {
  for (let k = 0; k < 40 && app.settle() === false; k++) {
    const t = app.currentTime();
    if (app.setTime(t + 0.25) <= t) break;
  }
}

test("a 200-slide deck opens without drawing its charts", () => {
  const { app, openMs } = openApp();
  assert.ok(app.deck.slideCount() >= SLIDES, `slides: ${app.deck.slideCount()}`);
  assert.equal(app.deck.undrawn(), charts, "every chart waits until its slide is in view");
  assert.ok(openMs < 2000, `opened in ${openMs.toFixed(0)} ms`);
});

test("the editor draws the charts on the slides it shows", () => {
  const { app } = openApp();
  app.layoutJson();
  const t0 = now();
  let rebuilds = 0;
  while (app.settle()) rebuilds++;
  const ms = now() - t0;
  const thumbs = JSON.parse(app.layoutJson()).thumbs;
  assert.ok(thumbs.length > 0 && thumbs.length <= 20, `thumbnails painted: ${thumbs.length}`);
  const last = Math.max(...thumbs.map((t) => t[0]));
  // slides 1..last are in view; a chart is on slides 3, 5, 10, 13, …
  const inView = bigDeck(last + 2).charts;
  assert.ok(app.deck.undrawn() >= charts - inView, `undrawn ${app.deck.undrawn()}, ${inView} in view`);
  assert.ok(app.deck.undrawn() < charts, "the ones in view are drawn");
  assert.ok(rebuilds <= 3, `settled in ${rebuilds} layouts`);
  assert.ok(ms < 3000, `settled in ${ms.toFixed(0)} ms`);
});

test("the viewer shows 200 slides one after another without a stall", () => {
  const { app } = openApp();
  app.present(true);
  playToRest(app);
  let worst = 0;
  let worstAt = -1;
  let shown = 0;
  let idleWorst = 0;
  const t0 = now();
  while (shown < app.deck.slideCount() - 1) {
    const before = JSON.parse(app.layoutJson()).slide;
    // a click: the next build step or slide, drawn at once
    const a = now();
    app.next();
    app.settle();
    app.stageJson();
    const step = now() - a;
    if (step > worst) {
      worst = step;
      worstAt = before;
    }
    // at rest, the slide after it is drawn ahead (PresApp.settle)
    const b = now();
    playToRest(app);
    idleWorst = Math.max(idleWorst, now() - b);
    shown = JSON.parse(app.layoutJson()).slide;
    assert.ok(shown >= before, "never goes back");
  }
  const total = now() - t0;
  assert.equal(shown, app.deck.slideCount() - 1, "reaches the last slide");
  assert.equal(app.deck.undrawn(), 0, "every chart drawn by the time its slide was shown");
  assert.ok(worst < 250, `slowest click ${worst.toFixed(0)} ms (from slide ${worstAt + 1})`);
  assert.ok(idleWorst < 1000, `slowest drawing ahead ${idleWorst.toFixed(0)} ms`);
  assert.ok(total < 20000, `whole show ${total.toFixed(0)} ms`);
});

test("every chart is drawn before the deck is written to a file", () => {
  const { app } = openApp();
  const t0 = now();
  app.settleAll();
  const ms = now() - t0;
  assert.equal(app.deck.undrawn(), 0);
  assert.ok(ms < 5000, `all charts in ${ms.toFixed(0)} ms`);
  // the last slide's chart is in its frame
  const last = JSON.parse(app.slideJson(app.deck.slideCount() - 1));
  assert.ok(last.list.cmds.length > 20, `last slide drawn: ${last.list.cmds.length} commands`);
});
