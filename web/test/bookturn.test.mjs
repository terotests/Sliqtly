// node --test: a realistic book's page turned (web/bookturn.js): by itself
// from its corner, held and let go over the spine or back, and what a frame
// draws meanwhile.
import test from "node:test";
import assert from "node:assert/strict";
import { autoTurn, grabTurn, dragTurn, releaseTurn, stepTurn, turnScene, turnPages } from "../bookturn.js";

const spreads = [[-1, 0], [1, 2], [3, 4], [5, -1]];
const W = 400;
const H = 600;

test("a turn by itself starts on its first frame and lands over", () => {
  const t = autoTurn(spreads, 0, 1, W, H, null);
  assert.equal(t.front, 0);
  assert.equal(t.back, 1);
  assert.equal(t.under, 2);
  assert.equal(stepTurn(t, 5000, H), "turning", "the clock starts at the first frame");
  assert.equal(stepTurn(t, 5300, H), "turning");
  assert.ok(t.qx < W && t.qx > -W, "the corner on its way");
  assert.equal(stepTurn(t, 5800, H), "over");
  assert.ok(Math.abs(t.qx + W) < 1e-6, "the corner on the other side");
});

test("no turn past either end", () => {
  assert.equal(autoTurn(spreads, 0, -1, W, H, 0), null);
  assert.equal(autoTurn(spreads, 3, 1, W, H, 0), null);
});

test("a corner taken, dragged past the spine and let go turns over", () => {
  const t = grabTurn(spreads, 1, W * 0.9, H * 0.8, W, H);
  assert.equal(t.side, 1);
  assert.equal(t.front, 2);
  t.held = { t: 0, x: t.cx, vx: 0 };
  dragTurn(t, W * 0.5, H * 0.7, 100);
  dragTurn(t, -W * 0.2, H * 0.6, 400);
  releaseTurn(t, W, 400);
  assert.equal(t.held, null);
  assert.equal(t.anim.over, true);
  assert.equal(stepTurn(t, 1000, H), "over");
});

test("let go short of the spine, slowly: it falls back", () => {
  const t = grabTurn(spreads, 1, W * 0.95, H * 0.5, W, H);
  t.held = { t: 0, x: t.cx, vx: 0 };
  dragTurn(t, W * 0.6, H * 0.5, 1000);
  dragTurn(t, W * 0.55, H * 0.5, 2000);
  releaseTurn(t, W, 2000);
  assert.equal(t.anim.over, false);
  assert.equal(stepTurn(t, 3000, H), "back");
});

test("a left page is taken back; the middle of a page is not a corner", () => {
  const t = grabTurn(spreads, 2, -W * 0.9, H * 0.5, W, H);
  assert.equal(t.side, -1);
  assert.equal(t.front, 3);
  assert.equal(t.under, 1);
  assert.equal(grabTurn(spreads, 2, W * 0.3, H * 0.5, W, H), null);
  assert.equal(grabTurn(spreads, 0, -W * 0.9, H * 0.5, W, H), null, "nothing before the cover");
});

test("a frame: the side being turned is the leaf, the other lies still", () => {
  assert.deepEqual(turnScene(spreads, 1, null, W, H), { left: 1, right: 2, turn: null });
  const t = autoTurn(spreads, 1, 1, W, H, null);
  stepTurn(t, 0, H);
  const s = turnScene(spreads, 1, t, W, H);
  assert.equal(s.left, 1);
  assert.equal(s.right, -1);
  assert.equal(s.turn.front, 2);
  assert.ok(s.turn.curl.R >= 0);
  assert.deepEqual(turnPages(spreads, 1, t).sort(), [1, 2, 3, 4]);
  assert.deepEqual(turnPages(spreads, 0, null), [0]);
});
