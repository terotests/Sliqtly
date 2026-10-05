// node --test: what an open presentation's tab names and how it opens again (web/decktabs.js)
import test from "node:test";
import assert from "node:assert/strict";
import { deckKey, canReturn, reopenPlan, tabLabel, readDeckTabs, keepDeckTabs, DECK_TABS_KEY } from "../decktabs.js";

test("a kept deck is its id; one not kept yet is where it came from", () => {
  assert.equal(deckKey({ persisted: true, id: "d1", src: "sample:welcome" }), "d1");
  assert.equal(deckKey({ persisted: false, id: "d2", src: "sample:welcome" }), "sample:welcome");
  assert.equal(deckKey({ persisted: false, id: "d3", src: null }), "d3");
  assert.equal(canReturn({ persisted: false, src: "cloud:abc123" }), true);
  assert.equal(canReturn({ persisted: false, src: null }), false);
});

test("a tab's key says how its deck is opened", () => {
  assert.deepEqual(reopenPlan("sample:uutta"), { kind: "sample", arg: "uutta" });
  assert.deepEqual(reopenPlan("cloud:Ab12Cd34"), { kind: "cloud", arg: "Ab12Cd34" });
  assert.deepEqual(reopenPlan("muv9-x1"), { kind: "doc", arg: "muv9-x1" });
});

test("a label is one line without tabs", () => {
  assert.equal(tabLabel("Budget\t2027\nQ1"), "Budget 2027 Q1");
  assert.equal(tabLabel("  "), "presentation");
});

test("the row is kept for the tab of the browser, and an empty one is not", () => {
  const m = new Map();
  const storage = { getItem: (k) => m.get(k) ?? null, setItem: (k, v) => m.set(k, v), removeItem: (k) => m.delete(k) };
  keepDeckTabs(storage, "d1\nd1\tSales");
  assert.equal(readDeckTabs(storage), "d1\nd1\tSales");
  keepDeckTabs(storage, "");
  assert.equal(m.has(DECK_TABS_KEY), false);
  assert.equal(readDeckTabs(null), "");
  assert.equal(readDeckTabs({ getItem: () => { throw new Error("blocked"); } }), "");
});
