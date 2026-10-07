// node --test: `mode: book` on the stage. Two pages side by side, the page
// being edited where a slide would be and the page facing it beside it;
// presenting goes a spread at a time and counts "2–3 / 5".
//
// Runs the built app (web/dist/pres_app.js, `npm run build`).
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const dist = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "dist");
const appJs = path.join(dist, "pres_app.js");
if (!fs.existsSync(appJs)) throw new Error("web/dist/pres_app.js is missing: run `npm run build` first");
(0, eval)(fs.readFileSync(appJs, "utf8"));

const pages = ["Kansi", "Tukholma", "Uppsala", "Gotlanti", "Kotiin"].map((t) => `# ${t}\n\nSivu ${t}.\n`).join("\n");
const front = (extra = "") => `---\ntitle: "Ruotsin matka 2026"\nmode: book\npage: A5\n${extra}---\n\n`;

function app(src) {
  const a = new globalThis.PresApp();
  a.setPageSize(1400, 900);
  a.setSource(src);
  return a;
}

const stage = (a) => JSON.parse(a.stageJson());
const layout = (a) => JSON.parse(a.layoutJson());
// the left edges of the stage's clips, in the page's units
const clipsX = (doc) => doc.list.cmds.filter((c) => c.k === 4).map((c) => Math.round(c.x));
const texts = (doc) => doc.list.cmds.filter((c) => c.k === 3).map((c) => c.text).join(" ");

test("the book's pages face each other: the cover alone, then 2|3, 4|5", () => {
  const a = app(front() + pages);
  const d = a.deck;
  assert.equal(d.slideCount(), 5);
  const b = d.book();
  assert.ok(b.isBook());
  assert.equal(b.spreadCount(5), 3);
  assert.equal(b.leftOf(1, 5), 1);
  assert.equal(b.rightOf(1, 5), 2);
});

test("editing page 2 shows page 3 beside it, page 2 keeping a slide's place", () => {
  const a = app(front() + pages);
  a.selectSlide(1);
  const L = layout(a);
  const doc = stage(a);
  const w = a.deck.pageW;
  // page 2's own frame clipped at 0, page 3's one page to the right
  assert.deepEqual(clipsX(doc), [0, Math.round(w)]);
  assert.match(texts(doc), /Tukholma/);
  assert.match(texts(doc), /Uppsala/);
  // two pages fit the stage side by side
  const sc = L.stage[2];
  assert.ok(w * 2 * sc <= 1400, "the spread fits the width");
  // page 3 (a right-hand page) has its spread starting a page to its left
  a.selectSlide(2);
  assert.deepEqual(clipsX(stage(a)), [0, -Math.round(w)]);
  const L3 = layout(a);
  assert.ok(Math.abs(L3.stage[0] - (L.stage[0] + w * sc)) < 1, "page 3 sits right of where page 2 sat");
});

test("the cover and a deck of slides show one page", () => {
  const a = app(front() + pages);
  a.selectSlide(0);
  assert.deepEqual(clipsX(stage(a)), [0]);
  const s = app(pages);
  s.selectSlide(1);
  assert.deepEqual(clipsX(stage(s)), [0]);
});

test("presenting goes a spread at a time and names its pages", () => {
  const a = app(front() + pages);
  a.present(true);
  assert.equal(a.slideLabel(), "1 / 5");
  a.next();
  assert.equal(a.selected, 1);
  assert.equal(a.slideLabel(), "2–3 / 5");
  // both pages, one frame, inside one clip two pages wide
  const doc = JSON.parse(a.stageJson());
  const clip = doc.list.cmds.find((c) => c.k === 4);
  assert.ok(Math.abs(clip.w - a.deck.pageW * 2) < 0.5);
  a.next();
  assert.equal(a.selected, 3);
  assert.equal(a.slideLabel(), "4–5 / 5");
  a.next();
  assert.ok(a.atEnd());
  a.prev();
  a.prev();
  assert.equal(a.selected, 1);
  a.pagePrev();
  assert.equal(a.selected, 0);
});

test("book-start: left pairs 1|2", () => {
  const a = app(front("book-start: left\n") + pages);
  a.present(true);
  assert.equal(a.slideLabel(), "1–2 / 5");
  a.next();
  assert.equal(a.slideLabel(), "3–4 / 5");
  a.next();
  assert.equal(a.slideLabel(), "5 / 5");
});

test("the inside margin is at the binding on both pages", () => {
  const a = app(front("margin-inside: 30mm\nmargin-outside: 10mm\n") + pages);
  const d = a.deck;
  const left = (i) => {
    const doc = JSON.parse(a.slideJson(i));
    return Math.min(...doc.list.cmds.filter((c) => c.k === 3).map((c) => c.x));
  };
  const mm = 72 / 25.4;
  // page 1 is a right-hand page (inside on the left), page 2 a left-hand one
  assert.ok(Math.abs(left(0) - 30 * mm) < 1.5, `page 1 at ${left(0)}`);
  assert.ok(Math.abs(left(1) - 10 * mm) < 1.5, `page 2 at ${left(1)}`);
  assert.ok(d.pageH > d.pageW, "A5 is upright");
});

test("presenting a realistic book hands the page its spreads and spine", () => {
  const a = app(front("render: realistic\n") + pages);
  assert.equal(layout(a).book, null, "editing: the stage draws the pages");
  a.present(true);
  let b = layout(a).book;
  assert.deepEqual(b.spreads, [[-1, 0], [1, 2], [3, 4]]);
  assert.equal(b.spread, 0);
  // the cover is a right-hand page: the spine is its left edge
  assert.equal(b.spine, 0);
  assert.equal(Math.round(b.w), Math.round(a.deck.pageW));
  a.next();
  b = layout(a).book;
  assert.equal(b.spread, 1);
  // page 2 (left) is shown: the spine is its right edge
  assert.equal(Math.round(b.spine), Math.round(a.deck.pageW));
  // a flat book is the stage's to draw
  const f = app(front() + pages);
  f.present(true);
  assert.equal(layout(f).book, null);
});
