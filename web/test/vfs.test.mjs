// node --test: the browser's store (web/vfs.js) never leaves a call waiting
// for good. A transaction that does not finish is aborted and tried again
// over a new connection; a connection the browser closed is replaced.
import test from "node:test";
import assert from "node:assert/strict";
import { idbStore, StallError } from "../vfs.js";

// A connection whose transactions finish (`hang` false) or never do. Each
// request answers `value`; `aborted` counts the transactions given up on.
function fakeDb({ hang = false, closing = false, value = "v" } = {}) {
  const db = {
    aborted: 0,
    closed: false,
    close() { db.closed = true; },
    transaction() {
      if (closing) {
        const e = new Error("The database connection is closing.");
        e.name = "InvalidStateError";
        throw e;
      }
      const tx = {
        abort() { db.aborted++; setTimeout(() => tx.onabort?.(), 0); },
        objectStore() {
          const req = () => {
            const r = {};
            setTimeout(() => {
              r.result = value;
              r.onsuccess?.();
              if (!hang) setTimeout(() => tx.oncomplete?.(), 0);
            }, 0);
            return r;
          };
          return { get: req, put: req, delete: req, getAll: req, index: () => ({ getAll: req, getAllKeys: req }) };
        },
      };
      return tx;
    },
  };
  return db;
}

test("a call is answered over the connection it opened", async () => {
  const s = idbStore(async () => fakeDb({ value: { id: "a" } }));
  assert.deepEqual(await s.getDoc("a"), { id: "a" });
});

test("a stuck transaction is aborted, said, and tried again over a new connection", async () => {
  const dbs = [fakeDb({ hang: true }), fakeDb({ value: "second" })];
  let opened = 0;
  const said = [];
  const s = idbStore(async () => dbs[opened++], { stalled: (store, mode) => said.push(store + " " + mode) }, { stallMs: 30 });
  assert.equal(await s.listFiles("d"), "second");
  assert.equal(opened, 2);
  assert.equal(dbs[0].aborted, 1);
  assert.equal(dbs[0].closed, true);
  assert.deepEqual(said, ["files readonly"]);
});

test("stuck twice: the call fails instead of waiting for good", async () => {
  const s = idbStore(async () => fakeDb({ hang: true }), {}, { stallMs: 20 });
  await assert.rejects(s.getDoc("a"), StallError);
});

test("a connection the browser is closing is replaced", async () => {
  const dbs = [fakeDb({ closing: true }), fakeDb({ value: "new" })];
  let opened = 0;
  const s = idbStore(async () => dbs[opened++]);
  assert.equal(await s.getDoc("a"), "new");
  assert.equal(opened, 2);
});

test("let go (a frozen page) aborts what is open; the call is made again once back, without a word", async () => {
  const dbs = [fakeDb({ hang: true }), fakeDb({ value: "after" })];
  let opened = 0;
  const said = [];
  const s = idbStore(async () => dbs[opened++], { stalled: () => said.push(1) }, { stallMs: 10000 });
  const call = s.getDoc("a");
  await new Promise((r) => setTimeout(r, 5));
  s.letGo();
  assert.equal(await call, "after");
  assert.equal(dbs[0].aborted, 1);
  assert.equal(dbs[0].closed, true);
  assert.deepEqual(said, []);
});

test("let go (a frozen page): the next call opens a new connection", async () => {
  const dbs = [fakeDb({ value: 1 }), fakeDb({ value: 2 })];
  let opened = 0;
  const s = idbStore(async () => dbs[opened++]);
  assert.equal(await s.getDoc("a"), 1);
  s.letGo();
  assert.equal(dbs[0].closed, true);
  assert.equal(await s.getDoc("a"), 2);
});
