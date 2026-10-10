// node --test: a slide script drives the source code viewer
// (presentation.code, allow: code; PresApp.playCode). Asks go through the
// deck's grants as the other asks do; the viewer only opens presenting.
//
// Runs the built app (web/dist, `npm run build`).
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";
import { DECK_RUNTIME } from "../apps-runtime.js";

const dist = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "dist");
const appJs = path.join(dist, "pres_app.js");
if (!fs.existsSync(appJs)) throw new Error("web/dist/pres_app.js is missing: run `npm run build` first");
(0, eval)(fs.readFileSync(appJs, "utf8"));
const css = fs.readFileSync(path.join(dist, "themes", "aurora.css"), "utf8");

const CPP = Array.from({ length: 80 }, (_, i) => "int line" + (i + 1) + " = " + (i + 1) + ";").join("\n") + "\n";

function deck(allow) {
  const app = new globalThis.PresApp();
  app.setPageSize(1440, 900);
  app.setStyleSheet(css);
  app.setSource("---\nslide-split-level: 2\n---\n\n## Flow {script=apps/c.tsx" + (allow ? ' allow="' + allow + '"' : "") + "}\n\n" +
    "```mermaid\nflowchart LR\n  A[Client] --> B[API submit]\n```\n\n::: code\nB api.cpp#L40-44\n:::\n");
  app.settleAll();
  app.setCodeFile("code/api.cpp", CPP);
  return { app, key: app.deck.scriptKeyOf(0) };
}

// a frame later: the viewer grown open, laid out and drawn
const frame = (app) => { app.deck.uiNow += 0.5; app.codeJson(); };
const code = (app, key) => JSON.parse(app.playState(key)).code;
const ask = (app, key, list) => app.playAsks(key, JSON.stringify(list));

test("presentation.code asks what the viewer does and reads what it shows", () => {
  const ctx = vm.createContext({ JSON, Math });
  vm.runInContext(DECK_RUNTIME, ctx);
  const c = ctx.presentation.code;
  ctx.__deckState({ code: { isOpen: true, path: "api.cpp", tab: 1, tabs: 2, mode: "split", zoom: 2, line: 40 } });
  assert.deepEqual([c.isOpen, c.path, c.tab, c.tabs, c.mode, c.zoom, c.line], [true, "api.cpp", 1, 2, "split", 2, 40]);
  ctx.__asks = [];
  c.open("api.cpp#L4", { mode: "diff" });
  c.openNode("B");
  c.setMode("now");
  c.goToLine("12");
  c.scroll(-3.4);
  c.setZoom(2);
  c.showTab(2);
  c.nextChange();
  c.prevChange();
  c.close();
  assert.deepEqual(JSON.parse(JSON.stringify(ctx.__asks)), [
    { k: "code.open", value: "api.cpp#L4", key: "diff" }, { k: "code.node", value: "B" }, { k: "code.mode", value: "now" },
    { k: "code.line", n: 12 }, { k: "code.scroll", n: -3 }, { k: "code.zoom", n: 2 }, { k: "code.tab", n: 2 },
    { k: "code.change", n: 1 }, { k: "code.change", n: -1 }, { k: "code.close" },
  ]);
  ctx.__deckState({});
  assert.equal(c.isOpen, false, "closed when the page says nothing");
});

test("a script with allow: code opens, scrolls, zooms and closes the viewer while presenting", () => {
  const { app, key } = deck("code");
  app.present(true);
  ask(app, key, [{ k: "code.open", value: "code/api.cpp#L10", key: "now" }]);
  assert.equal(app.codeView.isOpen(), true);
  frame(app);
  let s = code(app, key);
  assert.equal(s.path, "api.cpp");
  assert.equal(s.mode, "now");
  assert.ok(s.line >= 1 && s.line <= 10, "line 10 shown, top " + s.line);
  ask(app, key, [{ k: "code.line", n: 60 }]);
  frame(app);
  s = code(app, key);
  assert.ok(s.line > 40 && s.line <= 60, "line 60 shown, top " + s.line);
  ask(app, key, [{ k: "code.scroll", n: 5 }, { k: "code.zoom", n: 3 }]);
  frame(app);
  s = code(app, key);
  assert.equal(s.zoom, 3);
  ask(app, key, [{ k: "code.close" }]);
  assert.equal(code(app, key).isOpen, false);
  // a box's own links, as a double click on it opens them
  ask(app, key, [{ k: "code.node", value: "API submit" }]);
  frame(app);
  s = code(app, key);
  assert.equal(s.isOpen, true);
  assert.equal(s.path, "api.cpp");
  assert.ok(s.line > 10 && s.line <= 40, "the link's lines, top " + s.line);
  ask(app, key, [{ k: "code.close" }, { k: "code.node", value: "Nobody" }]);
  assert.equal(code(app, key).isOpen, false, "a box with no links opens nothing");
});

test("without allow: code the asks are refused and said; in the editor nothing opens", () => {
  const { app, key } = deck("");
  app.present(true);
  ask(app, key, [{ k: "code.open", value: "api.cpp" }]);
  assert.equal(app.codeView.isOpen(), false);
  assert.match(app.playNotes(), /code\.open needs "allow: code"/);
  const ed = deck("code");
  ask(ed.app, ed.key, [{ k: "code.open", value: "api.cpp" }]);
  assert.equal(ed.app.codeView.isOpen(), false, "presenting only");
  ask(ed.app, ed.key, [{ k: "code.open", value: "" }]);
  assert.match(ed.app.playNotes(), /code\.open without a file/);
});
