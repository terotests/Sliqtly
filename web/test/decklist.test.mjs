// node --test: the presentations window's list (web/decklist.js)
import test from "node:test";
import assert from "node:assert/strict";
import { deckRows, sortRows, whenText, deckListJson, nextSort, roomShareRows } from "../decklist.js";

const local = [
  { id: "a", name: "Old deck", created: 100, updated: 200 },
  { id: "b", name: "Shared deck", created: 300, updated: 400, cloud: "S1" },
];
const cloud = [
  { id: "S1", name: "Shared deck", created: 300, updated: 900 },
  { id: "M1", name: "Made by Claude", created: 1000 },
];

test("a deck kept here and in the cloud is one row with the later time", () => {
  const rows = deckRows(local, cloud, "a");
  assert.equal(rows.length, 3);
  const b = rows.find((r) => r.id === "b");
  assert.deepEqual(b, { id: "b", name: "Shared deck", created: 300, updated: 900, where: "both", current: false });
  assert.equal(rows.find((r) => r.id === "a").current, true);
});

test("a cloud share not kept here (made by an assistant) is listed, opened from the cloud", () => {
  const m = deckRows(local, cloud, "a").find((r) => r.id === "cloud:M1");
  assert.deepEqual(m, { id: "cloud:M1", name: "Made by Claude", created: 1000, updated: 1000, where: "cloud", current: false });
});

test("signed out: only this browser's decks", () => {
  const rows = deckRows(local, [], "x");
  assert.deepEqual(rows.map((r) => r.where), ["browser", "browser"]);
});

test("last changed first by default", () => {
  const ids = sortRows(deckRows(local, cloud, "a")).map((r) => r.id);
  assert.deepEqual(ids, ["cloud:M1", "b", "a"]);
});

test("by added, newest first; by name, A–Z with ties last changed first", () => {
  const rows = deckRows(local, cloud, "a");
  assert.deepEqual(sortRows(rows, "created").map((r) => r.id), ["cloud:M1", "b", "a"]);
  const named = [
    { id: "1", name: "beta", created: 1, updated: 1 },
    { id: "2", name: "Alpha", created: 1, updated: 5 },
    { id: "3", name: "alpha", created: 1, updated: 9 },
  ];
  assert.deepEqual(sortRows(named, "name").map((r) => r.id), ["3", "2", "1"]);
});

test("sorting leaves the given rows as they were", () => {
  const rows = [{ id: "1", updated: 1, created: 1 }, { id: "2", updated: 2, created: 2 }];
  sortRows(rows);
  assert.deepEqual(rows.map((r) => r.id), ["1", "2"]);
});

test("times read as d.m.yyyy hh:mm, none as empty", () => {
  const ms = new Date(2026, 9, 4, 11, 9).getTime();
  assert.equal(whenText(ms), "4.10.2026 11:09");
  assert.equal(whenText(0), "");
  assert.equal(whenText(undefined), "");
});

test("the window's JSON: sorted rows, a known sort, where in words", () => {
  const t = (s) => s;
  const j = JSON.parse(deckListJson(deckRows(local, cloud, "a"), "bogus", t, "n"));
  assert.equal(j.sort, "updated");
  assert.equal(j.note, "n");
  assert.deepEqual(j.rows.map((r) => r.where), ["In the cloud", "This browser and the cloud", "This browser"]);
  assert.equal(j.rows[0].cloudOnly, true);
  assert.equal(j.rows[2].current, true);
  assert.equal(j.rows[0].added, whenText(1000));
  const unnamed = JSON.parse(deckListJson([{ id: "z", name: "", created: 1, updated: 1, where: "browser" }], "name", t));
  assert.equal(unnamed.rows[0].name, "presentation");
  assert.equal(unnamed.sort, "name");
});

test("a column's head pressed again turns its order round", () => {
  assert.deepEqual(nextSort({ by: "updated", dir: "desc" }, "created"), { by: "created", dir: "desc" });
  assert.deepEqual(nextSort({ by: "created", dir: "desc" }, "created"), { by: "created", dir: "asc" });
  assert.deepEqual(nextSort({ by: "created", dir: "asc" }, "created"), { by: "created", dir: "desc" });
  assert.deepEqual(nextSort({ by: "created", dir: "desc" }, "name"), { by: "name", dir: "asc" });
  assert.deepEqual(nextSort({ by: "name", dir: "asc" }, "name"), { by: "name", dir: "desc" });
  assert.deepEqual(nextSort({ by: "name", dir: "asc" }, "bogus"), { by: "name", dir: "asc" });
});

test("oldest first and Z–A when turned round", () => {
  const rows = deckRows(local, cloud, "a");
  assert.deepEqual(sortRows(rows, "updated", "asc").map((r) => r.id), ["a", "b", "cloud:M1"]);
  assert.deepEqual(sortRows(rows, "created", "asc").map((r) => r.id), ["a", "b", "cloud:M1"]);
  assert.deepEqual(sortRows(rows, "name", "desc").map((r) => r.id), ["b", "a", "cloud:M1"]);
  const j = JSON.parse(deckListJson(rows, "created", (s) => s, "", "asc"));
  assert.equal(j.dir, "asc");
  assert.deepEqual(j.rows.map((r) => r.id), ["a", "b", "cloud:M1"]);
  assert.equal(JSON.parse(deckListJson(rows, "name", (s) => s)).dir, "asc");
  assert.equal(JSON.parse(deckListJson(rows, "updated", (s) => s)).dir, "desc");
});

test("a room's presentations on the server: a deck kept here is its own row, the rest the server's", () => {
  const rows = roomShareRows([{ deck_id: "S1", name: "Shared deck", updated: 900 }, { deck_id: "M1", name: "Made by Claude", updated: 50 }], local, "b");
  assert.deepEqual(rows, [
    { id: "b", name: "Shared deck", created: 300, updated: 900, where: "both", current: true },
    { id: "cloud:M1", name: "Made by Claude", created: 50, updated: 50, where: "cloud", current: false },
  ]);
  assert.deepEqual(roomShareRows(undefined, local, "b"), []);
});

test("byCloud: a deck in the cloud goes by its share, still the open one", () => {
  const local = [{ id: "d1", name: "A", created: 1, updated: 2, cloud: "S1" }, { id: "d2", name: "B", created: 1, updated: 3 }];
  const rows = deckRows(local, [{ id: "S1", name: "A", created: 1, updated: 5 }], "d1", { byCloud: true });
  assert.deepEqual(rows.map((r) => [r.id, r.where, r.current]), [["cloud:S1", "both", true], ["d2", "browser", false]]);
  assert.equal(deckRows(local, [], "d1")[0].id, "d1");
});
