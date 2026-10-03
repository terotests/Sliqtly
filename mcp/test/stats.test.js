// Visitor counts (src/stats.js) and the /api/hit route, with Firestore
// replaced by a fake that applies increments and merges.

import test from "node:test";
import assert from "node:assert/strict";
import { createApp } from "../src/http.js";
import { createStats, visitOf } from "../src/stats.js";

const NOW = Date.UTC(2026, 9, 3, 12);
const DAY = 24 * 60 * 60 * 1000;
const UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 Safari/605.1.15";
const PHONE = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Mobile/15E148 Safari/604.1";

const INC = Symbol("inc");
const FieldValue = { increment: (n) => ({ [INC]: n }) };

function merge(into, add) {
  const out = { ...into };
  for (const [k, v] of Object.entries(add)) {
    if (v && v[INC] !== undefined) out[k] = (out[k] || 0) + v[INC];
    else if (v && typeof v === "object" && !(v instanceof Date)) out[k] = merge(out[k] || {}, v);
    else out[k] = v;
  }
  return out;
}

function fakeDb() {
  const data = new Map();
  const ref = (c, id) => ({ key: `${c}/${id}` });
  const db = {
    data,
    collection: (c) => ({ doc: (id) => ref(c, id) }),
    runTransaction: async (fn) => fn({
      get: async (r) => ({ exists: data.has(r.key), data: () => data.get(r.key) }),
      set: (r, v, opts) => data.set(r.key, opts?.merge ? merge(data.get(r.key) || {}, v) : merge({}, v)),
    }),
  };
  return db;
}

function setup(t = NOW) {
  const db = fakeDb();
  let clock = t;
  let n = 0;
  const hit = createStats({ db, FieldValue, now: () => clock, random: () => `salt${n++}` });
  return { db, hit, day: (d) => db.data.get(`stats/${d}`), tick: (ms) => { clock += ms; } };
}

test("visitOf: pages, devices, referrers, bots", () => {
  assert.deepEqual(visitOf({ body: { p: "editor", r: "www.Google.com" }, ua: UA }), { page: "editor", device: "desktop", ref: "google.com" });
  assert.deepEqual(visitOf({ body: { p: "view" }, ua: PHONE }), { page: "view", device: "mobile", ref: "" });
  assert.equal(visitOf({ body: { p: "view", r: "sliqtly.com" }, ua: UA }).ref, "");
  assert.equal(visitOf({ body: { p: "view", r: "a b/c" }, ua: UA }).ref, "");
  assert.equal(visitOf({ body: { p: "admin" }, ua: UA }), null);
  assert.equal(visitOf({ body: {}, ua: UA }), null);
  assert.equal(visitOf({ body: { p: "editor" }, ua: "Googlebot/2.1" }), null);
  assert.equal(visitOf({ body: { p: "editor" }, ua: "" }), null);
});

test("a visitor is counted once a day, every load is a view", async () => {
  const { hit, day, db } = setup();
  assert.equal(await hit({ ip: "1.1.1.1", ua: UA, body: { p: "editor", r: "news.ycombinator.com" } }), true);
  await hit({ ip: "1.1.1.1", ua: UA, body: { p: "view", r: "news.ycombinator.com" } });
  await hit({ ip: "2.2.2.2", ua: PHONE, body: { p: "view" } });
  assert.equal(await hit({ ip: "3.3.3.3", ua: "curl/8", body: { p: "view" } }), false);
  assert.deepEqual(day("2026-10-03"), {
    views: 3, visitors: 2,
    pages: { editor: 1, view: 2 },
    devices: { desktop: 1, mobile: 1 },
    refs: { "news.ycombinator.com": 1 },
  });
  // nothing that names a visitor: the seen marks are hashes with an expiry
  const seen = [...db.data.keys()].filter((k) => k.startsWith("stats_seen/"));
  assert.equal(seen.length, 2);
  for (const k of seen) {
    assert.match(k, /^stats_seen\/[0-9a-f]{64}$/);
    assert.deepEqual(Object.keys(db.data.get(k)), ["expires"]);
  }
  assert.equal(db.data.get("stats_salt/2026-10-03").expires.getTime(), NOW + 2 * DAY);
});

test("a new day has a new salt, so the same visitor counts again", async () => {
  const { hit, day, tick, db } = setup();
  await hit({ ip: "1.1.1.1", ua: UA, body: { p: "editor" } });
  tick(DAY);
  await hit({ ip: "1.1.1.1", ua: UA, body: { p: "editor" } });
  assert.equal(day("2026-10-03").visitors, 1);
  assert.equal(day("2026-10-04").visitors, 1);
  assert.notEqual(db.data.get("stats_salt/2026-10-03").salt, db.data.get("stats_salt/2026-10-04").salt);
});

test("/api/hit counts beacons from the site only and always answers 204", async () => {
  const calls = [];
  const app = createApp({ store: {}, baseUrl: "https://sliqtly.com", stats: async (h) => { calls.push(h); return true; } });
  const server = app.listen(0);
  const url = `http://127.0.0.1:${server.address().port}/api/hit`;
  const post = (origin, body = { p: "editor" }) => fetch(url, {
    method: "POST",
    headers: { "content-type": "application/json", "user-agent": UA, ...(origin ? { origin } : {}), "x-forwarded-for": "9.9.9.9, 10.0.0.1" },
    body: JSON.stringify(body),
  });
  try {
    assert.equal((await post("https://sliqtly.com")).status, 204);
    assert.equal((await post("https://sliqtly.web.app", { p: "view" })).status, 204);
    assert.equal((await post("https://evil.example")).status, 204);
    assert.equal((await post(null)).status, 204);
  } finally {
    server.close();
  }
  assert.deepEqual(calls.map((c) => [c.ip, c.body.p]), [["9.9.9.9", "editor"], ["9.9.9.9", "view"]]);
  assert.equal(calls[0].ua, UA);
});
