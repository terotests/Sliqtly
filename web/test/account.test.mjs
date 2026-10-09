// node --test: what the browser keeps is kept per account on the editor
// (web/account.js).
import test from "node:test";
import assert from "node:assert/strict";
import { accountStorage } from "../account.js";

function storage(init = {}) {
  const m = new Map(Object.entries(init));
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => m.set(k, String(v)),
    removeItem: (k) => m.delete(k),
    key: (i) => [...m.keys()][i] ?? null,
    get length() { return m.size; },
    m,
  };
}

test("one account's keys are not another's", () => {
  const s = storage({ "sliqtly.rooms/general": "old" });
  const a = accountStorage(s, "u1");
  const b = accountStorage(s, "u2");
  a.setItem("evgp.doc", "deckA");
  a.setItem("sliqtly.rooms/r1", "x");
  assert.equal(a.getItem("evgp.doc"), "deckA");
  assert.equal(b.getItem("evgp.doc"), null);
  assert.equal(b.getItem("sliqtly.rooms/general"), null);
  assert.deepEqual([...Array(a.length).keys()].map((i) => a.key(i)), ["evgp.doc", "sliqtly.rooms/r1"]);
  assert.equal(b.length, 0);
  a.removeItem("evgp.doc");
  assert.equal(a.getItem("evgp.doc"), null);
  // a storage event's key: its own, or not
  assert.equal(a.own("sliqtly@u1/sliqtly.fileClip"), "sliqtly.fileClip");
  assert.equal(b.own("sliqtly@u1/sliqtly.fileClip"), null);
  assert.equal(a.own("sliqtly.fileClip"), null);
});

test("no account: the storage as it was", () => {
  const s = storage({ "evgp.doc": "d" });
  const o = accountStorage(s, "");
  assert.equal(o.getItem("evgp.doc"), "d");
  assert.equal(o.length, 1);
  assert.equal(o.own("evgp.doc"), "evgp.doc");
});
