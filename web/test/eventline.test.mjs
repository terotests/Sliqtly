// node --test: the page's one stream to a server of one's own
// (web/eventline.js), against fake WebSocket/EventSource and timers
import test from "node:test";
import assert from "node:assert/strict";
import { EventLine, RETRY_MS, LEAVE_MS } from "../eventline.js";

function world({ socketsWork = true } = {}) {
  const w = { sockets: [], sources: [], timers: [], now: 0, log: [] };
  class FakeSocket {
    constructor(url) {
      this.url = url;
      this.closed = false;
      w.sockets.push(this);
    }
    close() { this.closed = true; }
    // the server's side
    up() { this.onopen?.(); }
    send(m) { this.onmessage?.({ data: JSON.stringify(m) }); }
    drop() { this.closed = true; this.onclose?.(); }
  }
  class FakeSource {
    constructor(url) {
      this.url = url;
      this.closed = false;
      this.named = {};
      w.sources.push(this);
    }
    addEventListener(k, f) { this.named[k] = f; }
    close() { this.closed = true; }
  }
  w.env = {
    WebSocket: FakeSocket,
    EventSource: FakeSource,
    location: { protocol: "http:", host: "box:8080" },
    setTimeout: (f, ms) => { const t = { f, at: w.now + ms, done: false }; w.timers.push(t); return t; },
    clearTimeout: (t) => { if (t) t.done = true; },
  };
  w.tick = (ms) => {
    w.now += ms;
    for (const t of w.timers) if (!t.done && t.at <= w.now) { t.done = true; t.f(); }
  };
  w.on = {
    status: (st) => w.log.push("status " + st.state),
    changed: (id) => w.log.push("changed " + id),
    open: () => w.log.push("open"),
    lost: () => w.log.push("lost"),
  };
  w.socketsWork = socketsWork;
  w.open = () => w.sockets.filter((s) => !s.closed);
  return w;
}

const Q = { client: "c1", who: "w1", name: "Ada", color: "#ea580c", rev: 3 };

test("one WebSocket per page carries status, changes and the room", () => {
  const w = world();
  const line = new EventLine(w.env, w.on);
  line.start();
  assert.equal(w.sockets.length, 1);
  assert.equal(w.sockets[0].url, "ws://box:8080/api/socket");
  w.sockets[0].up();
  w.sockets[0].send({ k: "status", v: { state: "ready" } });
  w.sockets[0].send({ k: "changed", id: "deck1" });
  assert.deepEqual(w.log, ["open", "status ready", "changed deck1"]);

  const seen = [];
  let opens = 0;
  line.join("deckA", Q, (m) => seen.push(m.t), () => opens++);
  // the stream opens again, with the room; the one before is closed
  assert.equal(w.open().length, 1);
  const s = w.open()[0];
  const u = new URL(s.url);
  assert.equal(u.pathname, "/api/socket");
  assert.deepEqual(Object.fromEntries(u.searchParams), { room: "deckA", client: "c1", who: "w1", name: "Ada", color: "#ea580c", rev: "3" });
  s.up();
  s.send({ k: "room", id: 4, v: { t: "op", rev: 4 } });
  s.send({ k: "room", v: { t: "peers", rev: 4 } });
  s.send({ k: "changed", id: "deckA" });
  assert.deepEqual(seen, ["op", "peers"]);
  assert.equal(opens, 1);
  assert.equal(w.log.at(-1), "changed deckA");
});

test("leaving one deck and joining the next opens the stream once", () => {
  const w = world();
  const line = new EventLine(w.env, w.on);
  line.start();
  w.sockets[0].up();
  const leave = line.join("deckA", Q, () => {}, () => {});
  assert.equal(w.sockets.length, 2);
  leave();
  line.join("deckB", { ...Q, rev: 0 }, () => {}, () => {});
  assert.equal(w.sockets.length, 3);
  w.tick(LEAVE_MS + 10);
  assert.equal(w.sockets.length, 3, "no stream without a room in between");
  assert.equal(w.open().length, 1);
  assert.match(w.open()[0].url, /room=deckB/);
});

test("leaving for good: the stream without a room, a moment later", () => {
  const w = world();
  const line = new EventLine(w.env, w.on);
  line.start();
  const leave = line.join("deckA", Q, () => {}, () => {});
  leave();
  assert.match(w.open()[0].url, /room=deckA/);
  w.tick(LEAVE_MS);
  assert.equal(w.open().length, 1);
  assert.equal(w.open()[0].url, "ws://box:8080/api/socket");
});

test("a leave after another join does not take the new room away", () => {
  const w = world();
  const line = new EventLine(w.env, w.on);
  line.start();
  const leaveA = line.join("deckA", Q, () => {}, () => {});
  line.join("deckB", Q, () => {}, () => {});
  leaveA();
  w.tick(LEAVE_MS + 10);
  assert.match(w.open()[0].url, /room=deckB/);
});

test("a dropped stream comes back with the last edit it had", () => {
  const w = world();
  const line = new EventLine(w.env, w.on);
  line.start();
  let opens = 0;
  line.join("deckA", Q, () => {}, () => opens++);
  let s = w.open()[0];
  s.up();
  s.send({ k: "room", id: 9, v: { t: "op", rev: 9 } });
  s.drop();
  assert.equal(w.log.at(-1), "lost");
  w.tick(RETRY_MS[0]);
  s = w.open()[0];
  assert.match(s.url, /rev=9/);
  s.up();
  assert.equal(opens, 2, "a reconnect is told as one");
  // tries wait longer each time, up to the last step, and start over once through
  s.drop();
  w.tick(RETRY_MS[0]);
  w.open()[0].drop();
  const before = w.sockets.length;
  w.tick(RETRY_MS[0]);
  assert.equal(w.sockets.length, before, "the second try waits longer");
  w.tick(RETRY_MS[1]);
  assert.equal(w.sockets.length, before + 1);
});

test("where WebSockets never get through, Server-Sent Events are used", () => {
  const w = world();
  const line = new EventLine(w.env, w.on);
  line.start();
  w.sockets[0].drop();
  w.tick(RETRY_MS[0]);
  w.sockets[1].drop();
  assert.equal(w.sources.length, 1);
  assert.equal(w.sources[0].url, "/api/events");
  const es = w.sources[0];
  es.onopen();
  es.named.status({ data: JSON.stringify({ state: "ready" }) });
  es.onmessage({ data: JSON.stringify({ id: "deck1" }) });
  const seen = [];
  line.join("deckA", Q, (m) => seen.push(m.t), () => {});
  assert.ok(es.closed);
  const es2 = w.sources[1];
  assert.match(es2.url, /^\/api\/events\?room=deckA&/);
  es2.onmessage({ data: JSON.stringify({ t: "op", rev: 5 }) });
  assert.deepEqual(seen, ["op"]);
  assert.deepEqual(w.log.slice(-3), ["open", "status ready", "changed deck1"]);
});

test("a WebSocket that worked once is not given up for a restart", () => {
  const w = world();
  const line = new EventLine(w.env, w.on);
  line.start();
  w.sockets[0].up();
  w.sockets[0].drop();
  for (let i = 0; i < 5; i++) {
    w.tick(5000);
    w.open()[0].drop();
  }
  assert.equal(w.sources.length, 0);
});

test("no WebSocket at all: events from the start", () => {
  const w = world();
  w.env.WebSocket = undefined;
  new EventLine(w.env, w.on).start();
  assert.equal(w.sources.length, 1);
});
