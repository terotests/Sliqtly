// node --test: the owner's dashboard's bars (web/admin.js), without a page.
import test from "node:test";
import assert from "node:assert/strict";
import { niceMax, columns, money } from "../admin.js";

test("the axis tops out at a round number", () => {
  assert.deepEqual([0, 0.3, 1, 7, 12, 26, 140].map(niceMax), [1, 0.5, 1, 10, 20, 50, 200]);
});

test("stacked bars share the column, side by side ones split it", () => {
  const rows = [{ day: "a", s: 2, t: 2 }, { day: "b", s: 0, t: 1 }];
  const st = columns(rows, ["s", "t"], { width: 100, height: 40, gap: 2 });
  assert.equal(st.max, 5);
  const [s, t] = st.cols[0].bars;
  assert.equal(s.y, 40 - 16);
  assert.equal(t.y, 40 - 32);
  assert.equal(t.h, 14, "a 2px gap under the upper segment");
  assert.equal(st.cols[1].bars[0].h, 0);
  const side = columns(rows, ["s", "t"], { width: 100, height: 40, stack: false, gap: 2 });
  assert.equal(side.max, 2);
  const [a, b] = side.cols[0].bars;
  assert.equal(a.h, 40);
  assert.equal(b.x, a.x + a.w + 2);
});

test("money in the bill's currency", () => {
  assert.equal(money(1.5, "EUR"), "€1.50");
  assert.equal(money(0, "USD"), "$0.00");
});
