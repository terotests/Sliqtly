// node --test: what the editor hands the version-view frame (web/version-view.js)
import test from "node:test";
import assert from "node:assert/strict";
import { viewPacket, readPacket, isViewFrame, isReady, readyMessage, viewSrc, VIEW_PARAM } from "../version-view.js";

const snap = {
  name: "Q3",
  theme: "aurora",
  md: "# One\n\n---\n\n# Two\n",
  css: "h1 { color: red; }",
  files: [
    { path: "pics/a.png", blob: "abc", recipe: "", type: "image/png", data: new Blob([new Uint8Array([1, 2, 3])], { type: "image/png" }) },
    { path: "data/sales.csv", blob: "def", recipe: "", type: "text/csv", data: "a,b\n1,2\n" },
  ],
};

test("a version crosses whole: text, theme, CSS, files", () => {
  const got = readPacket(viewPacket(snap, { time: "2026-10-04T10:00:00Z" }));
  assert.equal(got.md, snap.md);
  assert.equal(got.css, snap.css);
  assert.equal(got.theme, "aurora");
  assert.equal(got.name, "Q3");
  assert.equal(got.time, "2026-10-04T10:00:00Z");
  assert.deepEqual(got.files.map((f) => [f.path, f.type]), [["pics/a.png", "image/png"], ["data/sales.csv", "text/csv"]]);
  assert.ok(got.files[0].data instanceof Blob);
  assert.equal(got.files[1].data, "a,b\n1,2\n");
});

test("only what the frame needs is sent (no blob ids or recipes)", () => {
  const p = viewPacket(snap);
  assert.deepEqual(Object.keys(p.files[0]).sort(), ["data", "path", "type"]);
});

test("a theme's own CSS stays null when the version has none", () => {
  const got = readPacket(viewPacket({ ...snap, css: null }));
  assert.equal(got.css, null);
});

test("a version without text is an empty deck, not a missing one", () => {
  const got = readPacket(viewPacket({ files: [] }));
  assert.equal(got.md, "");
  assert.equal(got.theme, "");
  assert.deepEqual(got.files, []);
});

test("anything else is not taken", () => {
  assert.equal(readPacket(null), null);
  assert.equal(readPacket({}), null);
  assert.equal(readPacket({ kind: "other", md: "# x" }), null);
  assert.equal(readPacket(readyMessage()), null);
});

test("files that climb out of the deck or carry no data are left out", () => {
  const p = viewPacket(snap);
  p.files.push({ path: "../x.png", type: "", data: "x" }, { path: "/etc", type: "", data: "x" },
    { path: "a//b", type: "", data: "x" }, { path: "ok.txt", type: "", data: { evil: 1 } });
  assert.deepEqual(readPacket(p).files.map((f) => f.path), ["pics/a.png", "data/sales.csv"]);
});

test("the frame is the page asked for with ?version-view inside another page", () => {
  assert.equal(isViewFrame("?" + VIEW_PARAM, true), true);
  assert.equal(isViewFrame("?" + VIEW_PARAM, false), false);
  assert.equal(isViewFrame("?sample=welcome", true), false);
  assert.equal(isViewFrame("", true), false);
  assert.ok(viewSrc().includes(VIEW_PARAM));
});

test("ready is its own message", () => {
  assert.equal(isReady(readyMessage()), true);
  assert.equal(isReady(viewPacket(snap)), false);
  assert.equal(isReady(null), false);
});
