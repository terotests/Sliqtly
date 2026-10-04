// node --test: Copy and Paste between presentations, and the Files tab's order (web/fileclip.js)
import test from "node:test";
import assert from "node:assert/strict";
import { sortFiles, pasteTarget, pastePlan, fileClipboard, readClipNote, CLIP_DOC, CLIP_KEY } from "../fileclip.js";
import { openVfs } from "../vfs.js";

function fakeStorage() {
  const m = new Map();
  return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)) };
}

test("a folder's files come together, so its heading shows once", () => {
  const rows = ["data/movies.json", "data/live/abc.csv", "top.css", "data/cars.json", "media/a.png", "data/airports.csv"].map((path) => ({ path }));
  const order = sortFiles(rows).map((r) => r.path);
  assert.deepEqual(order, ["top.css", "data/airports.csv", "data/cars.json", "data/movies.json", "data/live/abc.csv", "media/a.png"]);
  // each folder once, in one run
  const dirs = order.map((p) => p.slice(0, p.lastIndexOf("/") + 1));
  const runs = dirs.filter((d, i) => d !== dirs[i - 1]);
  assert.equal(new Set(runs).size, runs.length);
});

test("a pasted file whose name is taken gets -2, -3 before its extension", () => {
  const taken = new Set(["data/cars.json", "data/cars-2.json", "media/logo"]);
  assert.equal(pasteTarget("data/new.json", taken), "data/new.json");
  assert.equal(pasteTarget("data/cars.json", taken), "data/cars-3.json");
  assert.equal(pasteTarget("media/logo", taken), "media/logo-2");
  // a linked source's copy is named after the source: it replaces the copy
  assert.equal(pasteTarget("data/live/x1.csv", new Set(["data/live/x1.csv"])), "data/live/x1.csv");
});

test("a paste never lands two files on one path and leaves identical ones be", async () => {
  const existing = [
    { path: "data/a.csv", data: "1,2" },
    { path: "data/b.csv", data: "old" },
    { path: "media/p.png", data: new Blob([new Uint8Array([1, 2, 3])], { type: "image/png" }) },
  ];
  const plan = await pastePlan([
    { path: "data/a.csv", data: "1,2" },
    { path: "data/b.csv", data: "new" },
    { path: "media/p.png", data: new Blob([new Uint8Array([1, 2, 3])], { type: "image/png" }) },
    { path: "media/q.png", data: new Blob([new Uint8Array([9])]) },
  ], existing);
  assert.deepEqual(plan, [
    { from: "data/a.csv", to: "data/a.csv", same: true },
    { from: "data/b.csv", to: "data/b-2.csv", same: false },
    { from: "media/p.png", to: "media/p.png", same: true },
    { from: "media/q.png", to: "media/q.png", same: false },
  ]);
  // pasted again after the first paste renamed it: found under its new name
  const again = await pastePlan([{ path: "data/b.csv", data: "new" }], [...existing, { path: "data/b-2.csv", data: "new" }]);
  assert.deepEqual(again, [{ from: "data/b.csv", to: "data/b-2.csv", same: true }]);
  const other = await pastePlan([{ path: "data/b.csv", data: "newer" }], [...existing, { path: "data/b-2.csv", data: "new" }, { path: "data/b-x.csv", data: "newer" }]);
  assert.deepEqual(other.map((p) => p.to), ["data/b-3.csv"]);
  const twice = await pastePlan([{ path: "x.txt", data: "1" }, { path: "x.txt", data: "2" }], [{ path: "x.txt", data: "0" }]);
  assert.deepEqual(twice.map((p) => p.to), ["x-2.txt", "x-3.txt"]);
});

test("the clipboard keeps the copies apart from every deck, and the next Copy replaces them", async () => {
  const vfs = await openVfs();
  const storage = fakeStorage();
  const clip = fileClipboard(vfs, storage);
  assert.equal(clip.note(), null);
  await vfs.putFile({ doc: "deckA", path: "data/a.csv", data: "1,2", type: "text/csv", size: 3 });
  const src = await vfs.listFiles("deckA");
  await clip.copy(src, "Deck A");
  assert.deepEqual((await clip.files()).map((f) => [f.doc, f.path, f.data]), [[CLIP_DOC, "data/a.csv", "1,2"]]);
  assert.equal(clip.note().count, 1);
  assert.equal(clip.note().from, "Deck A");
  // another tab reads the same note
  assert.equal(readClipNote(storage).count, 1);
  assert.ok(storage.getItem(CLIP_KEY));
  // the deck it came from goes; the copies stay
  await vfs.deleteDoc("deckA");
  assert.equal((await clip.files()).length, 1);
  await clip.copy([{ doc: "deckB", path: "media/x.png", data: "img" }], "Deck B");
  assert.deepEqual((await clip.files()).map((f) => f.path), ["media/x.png"]);
  // the deck's own files are untouched by copying out of it
  assert.equal((await vfs.listDocs()).length, 0);
});

test("without storage the clipboard lasts as long as the page", async () => {
  const vfs = await openVfs();
  const clip = fileClipboard(vfs, null);
  await clip.copy([{ doc: "d", path: "a.txt", data: "x" }], "D");
  assert.equal(clip.note().count, 1);
  assert.equal(readClipNote({ getItem: () => "{bad" }), null);
});
