// node --test: what the browser keeps is kept per account on the editor
// (web/account.js), and the store of old goes only to the account that says
// it is theirs.
import test from "node:test";
import assert from "node:assert/strict";
import { storeName, accountStorage, legacyChoice, declineLegacy, moveKeys, copyStore, ACCOUNT_KEYS, LEGACY_KEY } from "../account.js";

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

test("each account has its own database; no account keeps the old one", () => {
  assert.equal(storeName("evg-presentation", ""), "evg-presentation");
  assert.equal(storeName("evg-presentation", "u1"), "evg-presentation@u1");
  assert.notEqual(storeName("evg-presentation", "u1"), storeName("evg-presentation", "u2"));
});

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

test("the old store is asked about once per account, never after it was taken", () => {
  const s = storage();
  assert.equal(legacyChoice(s, "u1", 0), "no");
  assert.equal(legacyChoice(s, "", 5), "no");
  assert.equal(legacyChoice(s, "u1", 5), "ask");
  declineLegacy(s, "u1");
  declineLegacy(s, "u1");
  assert.equal(legacyChoice(s, "u1", 5), "no");
  assert.equal(legacyChoice(s, "u2", 5), "ask");
  s.setItem(LEGACY_KEY, "taken");
  assert.equal(legacyChoice(s, "u2", 5), "no");
});

test("moving the old keys in: rooms with their parts, nothing of the account's written over", () => {
  const s = storage({ "sliqtly.rooms/general": "g", "sliqtly.rooms/r1": "r", "evgp.doc": "old", "sliqtly.lang": "fi", "sliqtly@u1/evgp.doc": "mine" });
  moveKeys(s, "u1", ACCOUNT_KEYS);
  const a = accountStorage(s, "u1");
  assert.equal(a.getItem("sliqtly.rooms/general"), "g");
  assert.equal(a.getItem("sliqtly.rooms/r1"), "r");
  assert.equal(a.getItem("evgp.doc"), "mine");
  assert.equal(s.getItem("evgp.doc"), null);
  assert.equal(s.getItem("sliqtly.rooms/general"), null);
  // the browser's own settings stay shared
  assert.equal(s.getItem("sliqtly.lang"), "fi");
});

test("copying the old store: decks, files and history", async () => {
  const mem = () => {
    const docs = new Map(), files = [], objects = [];
    return {
      docs, files, objects,
      listDocs: async () => [...docs.values()],
      putDoc: async (d) => { docs.set(d.id, d); },
      listFiles: async (id) => files.filter((f) => f.doc === id),
      putFile: async (f) => { files.push(f); },
      listObjects: async (id) => objects.filter((o) => o.doc === id),
      putObject: async (doc, id, data) => { objects.push({ doc, id, data }); },
    };
  };
  const from = mem();
  await from.putDoc({ id: "a", text: "# A" });
  await from.putFile({ doc: "a", path: "media/x.png" });
  await from.putObject("a", "o1", new Uint8Array([1]));
  const to = mem();
  assert.equal(await copyStore(from, to), 1);
  assert.deepEqual([...to.docs.keys()], ["a"]);
  assert.equal(to.files.length, 1);
  assert.equal(to.objects[0].id, "o1");
});
