// node --test: editing together (web/collab.js), against RangerDiff's RdOt as
// the build copies it (web/dist/rangerdiff.mjs) and RdOtHub as the server
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { editsOf, loadMe, saveMe, cleanName, rgbOf, chatTime, CollabDoc, CollabSession, ANIMALS } from "../collab.js";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const DIST = path.join(HERE, "..", "dist", "rangerdiff.mjs");
const have = fs.existsSync(DIST);
const ot = have ? await import(DIST) : null;
const t2 = have ? test : test.skip;

function rng(seed) {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 4294967296;
  };
}
const PIECES = ["a", "b", "xy", " ", "\n", "## ", "ä", "😀", "👨‍👩‍👧"];
const whole = (s, at) => (at > 0 && at < s.length && /[\udc00-\udfff]/.test(s[at]) ? at - 1 : at);
function randomEdit(r, s) {
  const at = whole(s, Math.floor(r() * (s.length + 1)));
  let del = Math.min(Math.floor(r() * 4), s.length - at);
  del = Math.max(whole(s, at + del) - at, 0);
  let ins = "";
  if (r() < 0.7) for (let i = 0; i < 1 + Math.floor(r() * 3); i += 1) ins += PIECES[Math.floor(r() * PIECES.length)];
  return { next: s.slice(0, at) + ins + s.slice(at + del), caret: at + ins.length };
}

test("a person: kept, named, coloured", () => {
  const store = new Map();
  const storage = { getItem: (k) => store.get(k) ?? null, setItem: (k, v) => store.set(k, v) };
  const a = loadMe(storage, rng(1));
  assert.match(a.name, /^Anonymous[A-Z]/);
  assert.ok(ANIMALS.includes(a.name.slice(9)));
  assert.match(a.color, /^#[0-9a-f]{6}$/);
  const b = loadMe(storage, rng(2));
  assert.equal(b.who, a.who, "the same person in the next page");
  assert.notEqual(b.client, a.client, "a page of its own");
  saveMe(storage, { ...b, name: "Ada" });
  assert.equal(loadMe(storage).name, "Ada");
  assert.equal(loadMe(null, rng(3)).name.startsWith("Anonymous"), true, "no storage: still someone");
  assert.equal(cleanName("  Ada\u0007 Lovelace  "), "Ada Lovelace");
  assert.equal([...cleanName("x".repeat(60))].length, 40);
  assert.deepEqual(rgbOf("#ea580c"), [234, 88, 12]);
  const at = new Date(2026, 9, 4, 9, 5).getTime();
  assert.equal(chatTime(at, at + 1000), "09:05");
  assert.equal(chatTime(at, at + 86400000 * 2, "fi"), "4.10. 09:05");
});

t2("an edit as the editor's replaces", () => {
  const r = rng(7);
  for (let i = 0; i < 2000; i += 1) {
    let s = "";
    for (let k = Math.floor(r() * 20); k > 0; k -= 1) s += PIECES[Math.floor(r() * PIECES.length)];
    let next = s;
    for (let k = 0; k < 3; k += 1) next = randomEdit(r, next).next;
    const d = ot.RdOtDelta.diff(s, next, -1);
    let t = s;
    for (const e of editsOf(d)) t = t.slice(0, e.offset) + e.text + t.slice(e.offset + e.removed);
    assert.equal(t, next);
    // a delta of several pieces (an edit transformed over another)
    const other = ot.RdOtDelta.diff(s, randomEdit(r, s).next, -1);
    const p = ot.RdOtDelta.transform(d, other);
    let u = other.apply(s);
    for (const e of editsOf(p.a)) u = u.slice(0, e.offset) + e.text + u.slice(e.offset + e.removed);
    assert.equal(u, p.a.apply(other.apply(s)));
  }
});

t2("carets: another's moves with the edits here and there", () => {
  const d = new CollabDoc(ot, { text: "Hello world", rev: 0, client: "me" });
  d.setPeers([{ client: "me", caret: 0, anchor: 0 }, { client: "x", name: "X", color: "#000000", caret: 6, anchor: 11 }]);
  assert.equal(d.peers.size, 1, "not one's own");
  d.local("Hi, Hello world", 3);
  assert.deepEqual([d.peers.get("x").caret, d.peers.get("x").anchor], [10, 15]);
  // while that waits, the server says where X is at revision 0
  d.cursor({ client: "x", caret: 0, anchor: 0 });
  assert.equal(d.peers.get("x").caret, 0, "an insert at it: before it");
  assert.equal(d.peerRows(), "X\t0\t0\t0\t0\t0");
});

// a room as the folder server keeps it, in memory: RdOtHub, every edit to
// every stream in order, delivered when the test says
function fakeRoom(start) {
  const hub = new ot.RdOtHub(start);
  const streams = new Map();
  const seqs = new Map();
  const peers = new Map();
  const send = (ev) => { for (const s of streams.values()) s.queue.push(ev); };
  const transport = (client) => ({
    snapshot: async () => ({ rev: hub.rev, md: hub.text, peers: [...peers.values()], chat: [] }),
    send: async (_, body) => {
      if (body.seq <= (seqs.get(body.client) || 0)) return { rev: hub.rev };
      if (!hub.submit(body.rev, ot.RdOtDelta.fromJson(JSON.stringify(body.ops)), body.client)) throw Object.assign(new Error("refused"), { code: "does-not-apply" });
      seqs.set(body.client, body.seq);
      const e = hub.log[hub.log.length - 1];
      send({ t: "op", rev: e.rev, client: e.client, ops: JSON.parse(e.delta.toJson()) });
      return { rev: hub.rev };
    },
    presence: async () => ({}),
    chat: async (_, body) => { const m = { id: String(Math.random()), who: client, name: client, text: body.text, at: 0 }; send({ t: "chat", msg: m }); return m; },
    stream: (_, q, onEvent, onOpen) => {
      peers.set(q.client, { client: q.client, who: q.who, name: q.name, color: q.color, caret: 0, anchor: 0 });
      streams.set(q.client, { queue: [], onEvent });
      send({ t: "peers", rev: hub.rev, peers: [...peers.values()] });
      onOpen?.();
      return () => streams.delete(q.client);
    },
  });
  return { hub, streams, transport };
}

function fakeEditor(text) {
  return {
    s: text, v: 0, c: 0, rows: "",
    version() { return this.v; }, text() { return this.s; }, caret() { return this.c; }, anchor() { return this.c; },
    apply(o, n, t) {
      this.s = this.s.slice(0, o) + t + this.s.slice(o + n);
      if (this.c > o) this.c = Math.max(o, this.c - n) + (this.c >= o + n ? t.length : 0);
      this.v += 1;
    },
    synced() {}, setPeers(r) { this.rows = r; },
    type(next, caret) { this.s = next; this.c = caret; this.v += 1; },
  };
}

const flush = () => new Promise((ok) => setTimeout(ok, 0));

t2("many pages typing at once end with one text", async () => {
  const start = "# Title\n\nSome text 😀 here.\n";
  const room = fakeRoom(start);
  const r = rng(42);
  const pages = [];
  for (let i = 0; i < 6; i += 1) {
    const ed = fakeEditor(start);
    const me = { who: "w" + i, name: "P" + i, color: "#123456", client: "c" + i };
    const s = new CollabSession(ot, room.transport(me.client), ed, me);
    await s.start("deck01");
    pages.push({ ed, s, me });
  }
  for (let step = 0; step < 3000; step += 1) {
    const p = pages[Math.floor(r() * pages.length)];
    const what = r();
    if (what < 0.3 && step < 2400) {
      // a keystroke: the page looks at the editor after each (main.js afterInput)
      const e = randomEdit(r, p.ed.s);
      p.ed.type(e.next, e.caret);
      p.s.takeLocal();
    } else if (what < 0.8) {
      // some of what its stream has arrives
      const q = room.streams.get(p.me.client).queue;
      for (let k = Math.floor(r() * 3); k > 0 && q.length; k -= 1) p.s.event(q.shift());
    } else {
      p.s.tick(step * 1000);
    }
    if (step % 50 === 0) await flush();
  }
  // everything delivered, everything sent
  for (let round = 0; round < 50; round += 1) {
    for (const p of pages) {
      p.s.tick();
      const q = room.streams.get(p.me.client).queue;
      while (q.length) p.s.event(q.shift());
    }
    await flush();
  }
  for (const p of pages) {
    assert.equal(p.ed.s, room.hub.text, p.me.client + " has the room's text");
    assert.equal(p.s.doc.text, room.hub.text);
    assert.equal(p.s.doc.rev, room.hub.rev);
  }
  assert.ok(room.hub.rev > 100, "edits went through: " + room.hub.rev);
  assert.ok(room.hub.text.length < 20000, "no text came back from the dead: " + room.hub.text.length);
  // and each sees the others' carets
  assert.equal(pages[0].ed.rows.split("\n").length, 5);
});

t2("a page joining late takes the room's text, its caret kept", async () => {
  const room = fakeRoom("abc\nnew line from the room\n");
  const ed = fakeEditor("abc\n");
  ed.c = 2;
  const s = new CollabSession(ot, room.transport("late"), ed, { who: "w", name: "L", color: "#000000", client: "late" });
  let shown = null;
  s.on.state = (md) => { shown = md; };
  await s.start("deck01");
  assert.equal(ed.s, room.hub.text);
  assert.equal(ed.c, 2);
  assert.equal(shown, room.hub.text);
});

t2("a page goes by the name and colour the room gives it", async () => {
  const room = fakeRoom("x");
  const ed = fakeEditor("x");
  const me = { who: "w1", name: "AnonymousPanda", color: "#ea580c", client: "c1" };
  const s = new CollabSession(ot, room.transport("c1"), ed, me);
  const told = [];
  s.on.me = (name, color) => told.push(name + " " + color);
  await s.start("deck01");
  s.event({ t: "peers", rev: 0, peers: [{ client: "c0", who: "w0", name: "AnonymousPanda", color: "#ea580c" }, { client: "c1", who: "w1", name: "AnonymousOtter", color: "#0d9488" }] });
  assert.deepEqual(told, ["AnonymousOtter #0d9488"]);
  assert.equal(s.me.name, "AnonymousOtter", "sent again as this on a reconnect");
  s.event({ t: "peers", rev: 0, peers: [{ client: "c1", who: "w1", name: "AnonymousOtter", color: "#0d9488" }] });
  assert.equal(told.length, 1, "told once");
});
