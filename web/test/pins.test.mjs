// node --test: presentations pinned to the top of their room (web/rooms.js)
import test from "node:test";
import assert from "node:assert/strict";
import { emptyRooms, readKept, changeKept, parseRooms, roomDecks, deckLines, editedOutOfPlace, setPinned, isPinned, pinnedFirst, forgetStory, moveDeck, createRoom, GENERAL, SHOWN } from "../rooms.js";
import { sortStories, groupStories } from "../stories.js";

function store(box = new Map()) {
  return {
    get length() { return box.size; },
    key: (i) => [...box.keys()][i] ?? null,
    getItem: (k) => box.get(k) ?? null,
    setItem: (k, v) => box.set(k, String(v)),
    removeItem: (k) => box.delete(k),
  };
}

test("a pin is set, cleared and kept as one fact of its own", () => {
  const box = new Map();
  const st = store(box);
  const heard = [];
  changeKept(st, "k", (s) => setPinned(s, "a", true), (k, v) => heard.push([k, v]));
  assert.equal(box.get("k/pinned/a"), "1");
  assert.deepEqual(heard, [["pinned/a", "1"]]);
  assert.ok(isPinned(readKept(st, "k"), "a"));
  // pinned again: nothing to write
  heard.length = 0;
  changeKept(st, "k", (s) => setPinned(s, "a", true), (k, v) => heard.push([k, v]));
  assert.deepEqual(heard, []);
  changeKept(st, "k", (s) => setPinned(s, "a", false), (k, v) => heard.push([k, v]));
  assert.deepEqual(heard, [["pinned/a", null]]);
  assert.ok(!box.has("k/pinned/a"));
  assert.ok(!isPinned(readKept(st, "k"), "a"));
});

test("samples are not pinned; the old one-value state reads its pins", () => {
  assert.equal(setPinned(emptyRooms(), "sample:welcome", true).pinned["sample:welcome"], undefined);
  assert.ok(isPinned(parseRooms(JSON.stringify({ pinned: { a: true, b: false } })), "a"));
  assert.ok(!isPinned(parseRooms(JSON.stringify({ pinned: { a: true, b: false } })), "b"));
});

test("the pin goes with the deck to another room, and goes when the deck is deleted", () => {
  let s = setPinned(emptyRooms(), "a", true);
  const r = createRoom(s, "Review", () => "r1");
  s = moveDeck(r.state, "a", r.id);
  const rows = roomDecks(s, r.id, [{ id: "a", name: "Master" }], []);
  assert.equal(rows[0].pinned, true);
  assert.equal(roomDecks(s, GENERAL, [{ id: "b", name: "B" }], [])[0].pinned, false);
  assert.ok(!isPinned(forgetStory(s, "a"), "a"));
});

test("pinned presentations come first in the panel, above the folders, all of them", () => {
  const folders = [{ id: "f-t", name: "Testing" }];
  const rows = [
    { id: "a", name: "A", folder: "f-t" },
    { id: "b", name: "B", current: true },
    { id: "m", name: "Master", folder: "f-t", pinned: true },
    { id: "n", name: "Notes", pinned: true },
  ];
  const lines = deckLines(rows, { folders, open: ["f-t"] }).split("\n").map((l) => l.split("\t"));
  assert.deepEqual(lines.map((l) => [l[0], l[3]]), [["m", "p"], ["n", "p"], ["f:f-t", "f"], ["a", "i"], ["b", ""]]);
  // the folder counts what is listed in it
  assert.equal(lines[2][4], "1");
  // pinned ones do not count against SHOWN
  const many = Array.from({ length: SHOWN }, (_, i) => ({ id: "x" + i, name: "X" }));
  const out = deckLines([{ id: "p", name: "P", pinned: true }, ...many], { showAll: "all" }).split("\n");
  assert.equal(out.length, SHOWN + 1);
  assert.ok(!out.includes("all\tall\t\ta"));
});

test("a save of a pinned deck does not ask the list drawn again; pinned ones keep their order", () => {
  assert.equal(editedOutOfPlace([{ id: "p", pinned: true }, { id: "b", current: true }], "edited"), false);
  assert.equal(editedOutOfPlace([{ id: "p", pinned: true }, { id: "a" }, { id: "b", current: true }], "edited"), true);
  assert.equal(editedOutOfPlace([{ id: "a" }, { id: "p", pinned: true, current: true }], "edited"), false);
  assert.deepEqual(pinnedFirst([{ id: "a" }, { id: "p", pinned: true }, { id: "b" }, { id: "q", pinned: true }]).map((r) => r.id), ["p", "q", "a", "b"]);
});

test("Stories: pinned first, in every group", () => {
  const rows = [
    { id: "a", name: "A", updated: 300, type: "review" },
    { id: "m", name: "M", updated: 100, type: "review", pinned: true },
    { id: "t", name: "T", updated: 200, type: "task", pinned: true },
  ];
  assert.deepEqual(sortStories(rows).map((r) => r.id), ["t", "m", "a"]);
  const g = groupStories(rows, { by: "type" });
  assert.deepEqual(g.find((x) => x.key === "review").rows.map((r) => r.id), ["m", "a"]);
  assert.equal(g.find((x) => x.key === "review").rows[0].pinned, true);
});
