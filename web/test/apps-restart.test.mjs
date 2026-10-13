// node --test: when a program on a slide (```app) starts again from its
// beginning (web/apps.js startsAfresh): while presenting, as the
// presentation begins and each time its slide comes back; never while
// editing, never for a slide's script. Runs on web/dist (npm run build).
import test from "node:test";
import assert from "node:assert/strict";

const { startsAfresh } = await import("../dist/apps.js");
const run = (o = {}) => ({ script: false, worker: {}, source: "a", away: false, presenting: false, ...o });

test("presenting begins on a slide whose game is running: it starts afresh", () => {
  assert.equal(startsAfresh(run({ presenting: false }), true, true, "a"), true);
});

test("its slide comes back while presenting: it starts afresh, and only then", () => {
  assert.equal(startsAfresh(run({ presenting: true, away: true }), true, true, "a"), true);
  assert.equal(startsAfresh(run({ presenting: true, away: false }), true, true, "a"), false);
  assert.equal(startsAfresh(run({ presenting: true, away: true }), false, true, "a"), false);
});

test("while editing a program keeps running, also when its slide comes back", () => {
  assert.equal(startsAfresh(run({ away: true }), true, false, "a"), false);
  assert.equal(startsAfresh(run({ presenting: true }), true, false, "a"), false);
});

test("a slide's script, a program not started yet, or a changed file: not this rule", () => {
  assert.equal(startsAfresh(run({ script: true }), true, true, "a"), false);
  assert.equal(startsAfresh(run({ worker: null }), true, true, "a"), false);
  assert.equal(startsAfresh(run(), true, true, "b"), false);
});
