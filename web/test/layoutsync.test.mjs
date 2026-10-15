// node --test: the rooms, folders and stories synced with sliqtly.com (web/layoutsync.js)
import test from "node:test";
import assert from "node:assert/strict";
import { applyFacts, layoutSync, LAYOUT_SEQ, LAYOUT_PENDING } from "../layoutsync.js";
import { changeKept, readKept, createRoom, moveDeck, roomOf, emptyRooms } from "../rooms.js";

const KEY = "sliqtly.rooms";

function memStore() {
  const m = new Map();
  return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: (k) => m.delete(k), key: (i) => [...m.keys()][i] ?? null, get length() { return m.size; }, m };
}

// mcp-go/layout.go's sync, in a few lines: numbered changes, fill, full
function fakeServer() {
  const facts = new Map();
  let seq = 0;
  return {
    facts,
    put(k, v) {
      const cur = facts.get(k);
      if (cur && cur.v === v) return;
      if (!cur && v === null) return;
      facts.set(k, { v, s: ++seq });
    },
    call(a) {
      for (const p of a.put || []) {
        if (p.fill && facts.has(p.k)) continue;
        this.put(p.k, p.v ?? null);
      }
      const full = !a.since || a.since > seq;
      const out = [];
      for (const [k, f] of facts) if (full ? f.v !== null : f.s > a.since) out.push({ k, v: f.v });
      return Promise.resolve({ seq, full, facts: out });
    },
  };
}

test("applyFacts writes the server's facts, leaves the pending ones, and a full set removes the rest", () => {
  const s = memStore();
  s.setItem(KEY + "/deck/a", "r-1");
  s.setItem(KEY + "/deck/b", "r-1");
  s.setItem(KEY + "/deck/c", "r-2");
  assert.equal(applyFacts(s, KEY, [{ k: "deck/a", v: "r-9" }, { k: "deck/b", v: "x" }], { full: true, pending: { "deck/b": "r-1" } }), true);
  assert.equal(s.getItem(KEY + "/deck/a"), "r-9");
  assert.equal(s.getItem(KEY + "/deck/b"), "r-1", "pending kept");
  assert.equal(s.getItem(KEY + "/deck/c"), null, "not the server's");
  assert.equal(applyFacts(s, KEY, [{ k: "deck/a", v: "r-9" }]), false, "nothing new");
  applyFacts(s, KEY, [{ k: "deck/a", v: null }]);
  assert.equal(s.getItem(KEY + "/deck/a"), null, "removed");
});

test("the first browser's rooms become the account's; a second browser joins without overwriting", async () => {
  const server = fakeServer();
  const one = memStore();
  // kept before there was a server, as the one value of old
  one.setItem(KEY, JSON.stringify({ rooms: [{ id: "r-a", title: "Alpha", created: 1 }], placed: { "cloud:d1": "r-a" } }));
  const ls1 = layoutSync({ store: one, key: KEY, call: (a) => server.call(a) });
  await ls1.sync();
  assert.equal(server.facts.get("deck/cloud:d1").v, "r-a");
  assert.equal(one.getItem(KEY), null, "the old value went as facts");
  assert.equal(roomOf(readKept(one, KEY), "cloud:d1"), "r-a");

  const two = memStore();
  changeKept(two, KEY, (s) => moveDeck(createRoom(s, "Beta", () => "b").state, "cloud:d1", "r-b"));
  changeKept(two, KEY, (s) => moveDeck(s, "cloud:d2", "r-b"));
  let heard = 0;
  const ls2 = layoutSync({ store: two, key: KEY, call: (a) => server.call(a), onChange: () => heard++ });
  await ls2.sync();
  const kept = readKept(two, KEY);
  assert.equal(roomOf(kept, "cloud:d1"), "r-a", "the server's placement stays");
  assert.equal(roomOf(kept, "cloud:d2"), "r-b", "its own new one added");
  assert.deepEqual(kept.rooms.map((r) => r.id).sort(), ["r-a", "r-b"]);
  assert.equal(heard, 1);

  // the first hears of it, and of a move made by the assistant
  server.put("deck/cloud:d1", "playground");
  await ls1.sync();
  assert.equal(roomOf(readKept(one, KEY), "cloud:d2"), "r-b");
  assert.equal(roomOf(readKept(one, KEY), "cloud:d1"), "playground");
});

test("a change made here is pending until it is sent, and survives a failed sync", async () => {
  const server = fakeServer();
  const s = memStore();
  const ls = layoutSync({ store: s, key: KEY, call: (a) => server.call(a) });
  await ls.sync();
  changeKept(s, KEY, (st) => createRoom(st, "Gamma", () => "g").state, ls.mark);
  assert.ok(JSON.parse(s.getItem(LAYOUT_PENDING))["room/r-g"]);
  const failing = layoutSync({ store: s, key: KEY, call: () => Promise.reject(new Error("offline")) });
  await assert.rejects(failing.sync());
  assert.ok(JSON.parse(s.getItem(LAYOUT_PENDING))["room/r-g"], "still pending");
  await ls.sync();
  assert.deepEqual(JSON.parse(s.getItem(LAYOUT_PENDING)), {});
  assert.ok(server.facts.get("room/r-g").v.includes("Gamma"));
  assert.ok(Number(s.getItem(LAYOUT_SEQ)) > 0);
  // removed here: removed there
  changeKept(s, KEY, () => emptyRooms(), ls.mark);
  await ls.sync();
  assert.equal(server.facts.get("room/r-g").v, null);
});

test("soon waits for the changes made together, then syncs once", async () => {
  const server = fakeServer();
  const s = memStore();
  let calls = 0;
  let fire = null;
  const ls = layoutSync({ store: s, key: KEY, call: (a) => (calls++, server.call(a)), setTimer: (fn) => (fire = fn, 1), clearTimer: () => {} });
  ls.soon();
  ls.soon();
  assert.equal(calls, 0);
  fire();
  await new Promise((ok) => setTimeout(ok, 0));
  assert.equal(calls, 1);
});
