// node --test: selecting a slide's text and what a copy gives (web/slidetext.js)
import test from "node:test";
import assert from "node:assert/strict";
import { slideText, stopsOf, runAt, indexAt, bandRects, withBand, sideBySide, xAt, TextSelection } from "../slidetext.js";

// every character 10 wide: where a stop lands is easy to say
const mono = (s) => [...s].length * 10;
const run = (text, x, y, more = {}) => ({ k: 3, x, y, w: 600, h: 20, text, font: "Open Sans", size: 16, c: [0, 0, 0, 1], ...more });

test("a paragraph's wrapped lines join with a space, a new paragraph with a newline", () => {
  const cmds = [
    { k: 0, x: 0, y: 0, w: 800, h: 450, c: [255, 255, 255, 1] },
    run("Title", 40, 20, { h: 40, size: 32 }),
    run("Klikkaa peliä ja aja", 40, 100),
    run("nuolilla tai WASD:lla.", 40, 120),
    run("Kartalla: Näsinneula", 40, 160),
  ];
  const st = slideText(cmds, mono);
  assert.equal(st.text, "Title\nKlikkaa peliä ja aja nuolilla tai WASD:lla.\nKartalla: Näsinneula");
  assert.equal(st.runs.length, 4);
  assert.equal(st.runs[0].cmd, 1);
  assert.equal(st.runs[2].start, "Title\nKlikkaa peliä ja aja ".length);
});

test("runs on one line: abutting ones join as they stand, a gap is a space", () => {
  // "plain " + bold "word" + "." — and a bullet a gap before its item
  const st = slideText([
    run("plain ", 40, 100),
    run("word", 100, 100, { weight: "bold" }),
    run(".", 140, 100),
    run("•", 40, 140),
    run("item", 70, 140),
  ], mono);
  assert.equal(st.text, "plain word.\n• item");
});

test("rotated, invisible and empty runs are not text one can select", () => {
  const st = slideText([run("up", 0, 0, { rot: 90 }), run("ghost", 0, 30, { c: [0, 0, 0, 0] }), run("", 0, 60), { k: 0, x: 0, y: 0, w: 1, h: 1 }, run("seen", 0, 90)], mono);
  assert.equal(st.text, "seen");
});

test("stops are grapheme clusters: a caret never lands inside an emoji or a combined letter", () => {
  assert.deepEqual(stopsOf("a👍🏽b"), [0, 1, 5, 6]);
  assert.deepEqual(stopsOf("éx"), [0, 2, 3]);
});

test("letter-spacing is in each stop's x", () => {
  const st = slideText([run("abc", 0, 0, { ls: 2 })], mono);
  assert.deepEqual(st.runs[0].xs, [0, 12, 24, 36]);
});

test("a pointer on text: the run under it, and the nearer edge of the letter", () => {
  const st = slideText([run("Hello", 40, 100), run("world", 40, 120)], mono);
  assert.equal(runAt(st, 45, 105), 0);
  assert.equal(runAt(st, 45, 125), 1);
  assert.equal(runAt(st, 200, 105), -1, "past the end of the line is not on text");
  assert.equal(runAt(st, 95, 105, 6), 0, "…unless within the pad");
  assert.equal(indexAt(st, 54, 105), 1);
  assert.equal(indexAt(st, 56, 105), 2);
  // off the text while dragging: level with a line is that line; above all
  // of it the first run's start, below it the last run's end
  assert.equal(indexAt(st, 400, 105), 5);
  assert.equal(indexAt(st, 0, 125), 6);
  assert.equal(indexAt(st, 45, 0), 0);
  assert.equal(indexAt(st, 45, 900), st.text.length);
});

test("the band: one rectangle per run, from the first offset to the last", () => {
  const st = slideText([run("Hello", 40, 100), run("world", 40, 120)], mono);
  assert.equal(xAt(st, 2), 60);
  assert.deepEqual(bandRects(st, 2, 8), [
    { cmd: 0, x: 60, y: 100, w: 30, h: 20 },
    { cmd: 1, x: 40, y: 120, w: 20, h: 20 },
  ]);
  assert.deepEqual(bandRects(st, 3, 3), []);
  // the gap between a list's number and its item is covered as well
  const li = slideText([run("item", 70, 140), run("1.", 40, 140)], mono);
  assert.equal(li.text, "1. item");
  assert.deepEqual(bandRects(li, 0, 7).map((r) => [r.x, r.w]), [[40, 20], [60, 50]]);
  assert.deepEqual(bandRects(li, 3, 7).map((r) => [r.x, r.w]), [[70, 40]], "not when the selection starts in the item");
  // drawn under the text it covers, over what is behind it
  const cmds = [{ k: 0, x: 0, y: 0, w: 9, h: 9, c: [1, 1, 1, 1] }, run("Hello", 40, 100)];
  const st2 = slideText(cmds, mono);
  const out = withBand(cmds, st2, 0, 5);
  assert.deepEqual(out.map((c) => c.k), [0, 0, 3]);
  assert.equal(out[1].w, 50);
  assert.equal(cmds.length, 2, "the list itself is not changed");
});

test("a drag selects; a plain click is still the slide's click", () => {
  const s = new TextSelection();
  s.setText("Hello world");
  assert.equal(s.press(2, false, true), "select");
  assert.equal(s.drag(2), false, "a wobble on the same letter is not a drag");
  assert.equal(s.release(), "click");
  assert.equal(s.has(), false);

  assert.equal(s.press(6, false, true), "select");
  assert.equal(s.drag(9), true);
  assert.equal(s.drag(11), true);
  assert.equal(s.release(), "");
  assert.equal(s.selected(), "world");
  // dragged backwards: the same text
  s.press(5, false, true);
  s.drag(0);
  s.release();
  assert.equal(s.selected(), "Hello");
});

test("with text selected, the next click lets go of it and does nothing else", () => {
  const s = new TextSelection();
  s.setText("Hello world");
  s.press(0, false, true);
  s.drag(5);
  s.release();
  assert.equal(s.press(3, false, true), "select");
  assert.equal(s.release(), "", "not the next slide");
  assert.equal(s.has(), false);
  // off the text too
  s.press(0, false, true);
  s.drag(5);
  s.release();
  assert.equal(s.press(0, false, false), "clear");
  assert.equal(s.has(), false);
  // and with nothing selected a press off the text is not ours
  assert.equal(s.press(0, false, false), "");
});

test("Shift+click extends; select all; new text drops the selection", () => {
  const s = new TextSelection();
  s.setText("Hello world");
  s.press(0, false, true);
  s.drag(2);
  s.release();
  assert.equal(s.press(8, true, false), "select");
  assert.equal(s.release(), "");
  assert.equal(s.selected(), "Hello wo");
  s.selectAll();
  assert.equal(s.selected(), "Hello world");
  s.setText("Hello world");
  assert.equal(s.selected(), "Hello world", "the same text keeps it");
  s.setText("Other slide");
  assert.equal(s.selected(), "");
});


test("a book's two pages side by side: one text, each page gets its own part of the band", () => {
  const left = [{ k: 0, x: 0, y: 0, w: 9, h: 9, c: [1, 1, 1, 1] }, run("Left", 40, 100)];
  const right = [run("Right", 40, 100)];
  const { cmds, from } = sideBySide([{ cmds: left, dx: 0 }, { cmds: right, dx: 800 }]);
  assert.deepEqual(from, [0, 2]);
  assert.equal(right[0].x, 40, "the page's own list is not moved");
  const st = slideText(cmds, mono);
  assert.equal(st.text, "Left Right");
  const l = withBand(left, st, 0, st.text.length, { from: from[0], dx: 0 });
  const r = withBand(right, st, 0, st.text.length, { from: from[1], dx: 800 });
  assert.deepEqual(l.map((c) => c.k), [0, 0, 3]);
  assert.deepEqual(r.map((c) => c.k), [0, 3]);
  assert.equal(r[0].x, 40, "back in the page's own units");
});

test("the Finance sample as the server lays it out: wrapped lines, and a list's numbers before their items", async () => {
  const fs = await import("node:fs");
  const { lists } = JSON.parse(fs.readFileSync(new URL("../../scripts/fixtures/view-deck.json", import.meta.url), "utf8"));
  const width = (s, c) => [...s].length * c.size * 0.5;
  assert.equal(slideText(lists[0].cmds, width).text, "Take charge of your money\nA budget, a buffer and compound interest – three things that go a long way.");
  assert.equal(slideText(lists[2].cmds, width).text, [
    "Three rules",
    "1. Pay yourself first – savings move on payday",
    "2. A buffer before investing: three months of expenses",
    "3. Keep spending visible: no surprises at the end of the month",
  ].join("\n"));
});
