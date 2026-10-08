// node --test: a deck's files against its share's (web/sharefiles.js)
import test from "node:test";
import assert from "node:assert/strict";
import { planFiles, seenAfterSave, versionOf } from "../sharefiles.js";

const ink = (sha, size = 10) => ({ path: "drawings/a.ink", size, sha, url: "/files/a.ink" });
const paths = (list) => list.map((f) => f.path || f);

test("a file written again there, unchanged here, is taken", () => {
  const p = planFiles({
    remote: [ink("v2")],
    local: new Map([["drawings/a.ink", "10:1"]]),
    stamps: new Map([["drawings/a.ink", "10:1"]]),
    seen: new Map([["drawings/a.ink", "v1"]]),
  });
  assert.deepEqual(paths(p.update), ["drawings/a.ink"]);
  assert.deepEqual(p.add, []);
  assert.equal(p.seen.get("drawings/a.ink"), "v2");
});

test("the version this copy already is: nothing to take", () => {
  const p = planFiles({
    remote: [ink("v1")],
    local: new Map([["drawings/a.ink", "10:1"]]),
    stamps: new Map([["drawings/a.ink", "10:1"]]),
    seen: new Map([["drawings/a.ink", "v1"]]),
  });
  assert.deepEqual(p.update, []);
  assert.equal(p.seen.get("drawings/a.ink"), "v1");
});

test("changed here too: this copy stays, to go up next", () => {
  const p = planFiles({
    remote: [ink("v2")],
    local: new Map([["drawings/a.ink", "12:5"]]),
    stamps: new Map([["drawings/a.ink", "10:1"]]),
    seen: new Map([["drawings/a.ink", "v1"]]),
  });
  assert.deepEqual(p.update, []);
  assert.equal(p.seen.get("drawings/a.ink"), "v1");
});

test("not known which version this copy is: the one there, nothing fetched", () => {
  const p = planFiles({
    remote: [ink("v3")],
    local: new Map([["drawings/a.ink", "10:1"]]),
    stamps: new Map([["drawings/a.ink", "10:1"]]),
  });
  assert.deepEqual(p.update, []);
  assert.equal(p.seen.get("drawings/a.ink"), "v3");
});

test("a share that names no versions: never taken in place", () => {
  const p = planFiles({
    remote: [{ path: "drawings/a.ink", size: 12 }],
    local: new Map([["drawings/a.ink", "10:1"]]),
    stamps: new Map([["drawings/a.ink", "10:1"]]),
    seen: new Map([["drawings/a.ink", "v1"]]),
  });
  assert.deepEqual(p.update, []);
  assert.equal(p.seen.has("drawings/a.ink"), false);
});

test("new there: added; gone there and unchanged here: removed", () => {
  const p = planFiles({
    remote: [ink("v1"), { path: "media/b.png", size: 3, sha: "b1" }],
    local: new Map([["drawings/a.ink", "10:1"], ["media/c.png", "3:1"], ["media/d.png", "4:2"]]),
    stamps: new Map([["drawings/a.ink", "10:1"], ["media/c.png", "3:1"], ["media/d.png", "4:1"]]),
    seen: new Map([["drawings/a.ink", "v1"]]),
  });
  assert.deepEqual(paths(p.add), ["media/b.png"]);
  assert.deepEqual(p.remove, ["media/c.png"]);
  assert.equal(p.seen.get("media/b.png"), "b1");
});

test("removed here and not yet sent: not added back", () => {
  const p = planFiles({
    remote: [ink("v1")],
    local: new Map(),
    stamps: new Map([["drawings/a.ink", "10:1"]]),
  });
  assert.deepEqual(p.add, []);
});

test("a save names the versions it wrote there", () => {
  const s = seenAfterSave([ink("v4"), { path: "media/x.png", size: 1 }]);
  assert.equal(s.get("drawings/a.ink"), "v4");
  assert.equal(s.has("media/x.png"), false);
  assert.equal(versionOf(null), "");
});

test("a file a save left as it was keeps the version seen: a newer one there is still taken", () => {
  const seen = seenAfterSave(
    [ink("v2"), { path: "media/b.png", size: 3, sha: "b2" }],
    new Map([["drawings/a.ink", "v1"], ["media/gone.png", "g1"]]),
    new Set(["media/b.png"]),
  );
  assert.equal(seen.get("drawings/a.ink"), "v1");
  assert.equal(seen.get("media/b.png"), "b2");
  assert.equal(seen.has("media/gone.png"), false);
  const p = planFiles({
    remote: [ink("v2")],
    local: new Map([["drawings/a.ink", "10:1"]]),
    stamps: new Map([["drawings/a.ink", "10:1"]]),
    seen,
  });
  assert.deepEqual(paths(p.update), ["drawings/a.ink"]);
});
