// node --test: a deck's text kept until its place has it (web/drafts.js)
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { openPlan, draftStore, editKeeper } from "../drafts.js";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const DIST = path.join(HERE, "..", "dist", "rangerdiff.mjs");
const ot = fs.existsSync(DIST) ? await import(DIST) : null;
const t2 = ot ? test : test.skip;

function memStorage() {
  const m = new Map();
  return {
    get length() { return m.size; },
    key: (i) => [...m.keys()][i] ?? null,
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => { m.set(k, String(v)); },
    removeItem: (k) => { m.delete(k); },
    m,
  };
}

t2("a deck opens with this page's changes, the share's kept too", () => {
  const was = "# Deck\n\none\n";
  assert.deepEqual(openPlan(ot, null, was), { md: was, from: "shared" });
  assert.deepEqual(openPlan(ot, { md: was, cloudMd: was }, was + "x"), { md: was + "x", from: "shared" }, "nothing waited here");
  assert.deepEqual(openPlan(ot, { md: was + "mine\n", cloudMd: null }, was), { md: was, from: "shared" }, "the share's text never known: as before");
  assert.deepEqual(openPlan(ot, { md: was + "mine\n", cloudMd: was }, was), { md: was + "mine\n", from: "kept" }, "the share has not moved");
  assert.deepEqual(openPlan(ot, { md: was + "mine\n", cloudMd: was }, "# Deck!\n\none\n"), { md: "# Deck!\n\none\nmine\n", from: "merged" });
  assert.deepEqual(openPlan(ot, { md: was + "mine\n", cloudMd: was }, was + "mine\n"), { md: was + "mine\n", from: "shared" }, "the share got it");
});

test("drafts: kept, dropped once the share has them, oldest go past the limit", () => {
  const st = memStorage();
  let clock = 1;
  const d = draftStore(st, { maxBytes: 200, now: () => clock++ });
  d.keep("a", "mine", "theirs");
  assert.deepEqual(d.get("a"), { md: "mine", cloudMd: "theirs", at: 1 });
  d.keep("a", "same", "same");
  assert.equal(d.get("a"), null, "the share has it all");
  d.keep("a", "x".repeat(60), "");
  d.keep("b", "y".repeat(60), "");
  d.keep("c", "z".repeat(60), "");
  assert.equal(d.get("a"), null, "the oldest went");
  assert.ok(d.get("b") && d.get("c"));
  d.keep("huge", "q".repeat(500), "");
  assert.equal(d.get("huge"), null, "past the limit alone: not kept");
  assert.ok(d.get("c"), "nor did it push others out");
  // a storage that refuses: nothing kept, nothing thrown
  const bad = { length: 0, key: () => null, getItem: () => { throw new Error("no"); }, setItem: () => { throw new Error("no"); }, removeItem: () => { throw new Error("no"); } };
  const b = draftStore(bad);
  b.keep("a", "x", "y");
  assert.equal(b.get("a"), null);
  assert.equal(draftStore(null).get("a"), null);
  st.setItem("sliqtly.draft.bad", "{not json");
  assert.equal(d.get("bad"), null);
});

test("decks in Edit in this tab", () => {
  const st = memStorage();
  const e = editKeeper(st, 3);
  assert.equal(e.has("a"), false);
  e.set("a", true);
  e.set("b", true);
  assert.equal(editKeeper(st).has("a"), true, "read again after a reload");
  e.set("a", false);
  assert.equal(e.has("a"), false);
  for (const k of ["c", "d", "e"]) e.set(k, true);
  assert.equal(e.has("b"), false, "only the latest few");
  assert.equal(e.has(""), false);
  st.setItem("sliqtly.editing", "nonsense");
  assert.equal(e.has("c"), false);
});
