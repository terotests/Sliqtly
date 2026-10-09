// node --test: web/book.js — a book's spreads in the viewer, and the page
// turned by its corner (no GL: where every point of the leaf goes).
import test from "node:test";
import assert from "node:assert/strict";
import { bookOf, spreadOfPage, firstPage, spreadLabel, spreadPages, grabAt, clampPointer, curl, bend, letGo, turnPath, leafMesh } from "../book.js";

const W = 400;
const H = 560;
const spreads = [[-1, 0], [1, 2], [3, 4]];
const near = (a, b, eps = 1e-6) => Math.abs(a - b) < eps;

test("a deck without a book is slides; a book's render is flat unless realistic", () => {
  assert.equal(bookOf({ width: 1 }), null);
  assert.equal(bookOf({ book: { spreads: [] } }), null);
  assert.equal(bookOf({ book: { spreads, render: "shiny" } }).render, "flat");
  assert.equal(bookOf({ book: { spreads, render: "realistic" } }).render, "realistic");
});

test("spreads: where a page is, where a spread starts, what it is called", () => {
  assert.equal(spreadOfPage(spreads, 0), 0);
  assert.equal(spreadOfPage(spreads, 2), 1);
  assert.equal(spreadOfPage(spreads, 3), 2);
  assert.equal(firstPage(spreads, 0), 0);
  assert.equal(firstPage(spreads, 1), 1);
  assert.equal(firstPage(spreads, 9), 3, "held inside the book");
  assert.equal(spreadLabel(spreads, 0, 5), "1 / 5");
  assert.equal(spreadLabel(spreads, 1, 5), "2–3 / 5");
  assert.deepEqual(spreadPages(spreads, 0), [{ page: 0, x: 1 }]);
  assert.deepEqual(spreadPages(spreads, 1), [{ page: 1, x: 0 }, { page: 2, x: 1 }]);
  assert.deepEqual(spreadPages([[4, -1]], 0), [{ page: 4, x: 0 }]);
});

test("a press on a page's outer third holds its edge; nowhere to turn, nothing", () => {
  assert.deepEqual(grabAt(390, 500, W, H, true, true), { side: 1, cx: W, cy: 500 });
  assert.deepEqual(grabAt(-390, 20, W, H, true, true), { side: -1, cx: -W, cy: 20 });
  assert.equal(grabAt(100, 500, W, H, true, true), null, "the middle of the page is no corner");
  assert.equal(grabAt(390, 500, W, H, false, true), null, "the last spread turns no further");
  assert.equal(grabAt(390, H + 5, W, H, true, true), null);
});

test("nothing moves until it is dragged, and the leaf is not torn from the spine", () => {
  const c0 = curl(W, H, W, H, W, H);
  assert.equal(c0.progress, 0);
  for (const [x, y] of [[0, 0], [W, H], [W / 2, H / 2]]) {
    const [bx, by, bz] = bend(x, y, c0);
    assert.ok(near(bx, x) && near(by, y) && bz === 0);
  }
  // dragged far up and left: held where the spine's ends stay put
  const q = clampPointer(W, H, -5000, -5000, H);
  assert.ok(Math.hypot(q.x, q.y - H) <= Math.hypot(W, 0) + 1e-6);
  assert.ok(Math.hypot(q.x, q.y) <= Math.hypot(W, H) + 1e-6);
  for (const [qx, qy] of [[200, 300], [-50, 100], [-380, 540], [0, 0], [-5000, -5000], [300, H]]) {
    const c = curl(W, H, qx, qy, W, H);
    for (const ey of [0, H / 2, H]) {
      const [bx, by, bz] = bend(0, ey, c);
      assert.ok(near(bx, 0, 1e-6) && near(by, ey, 1e-6) && bz === 0, `spine at ${ey} stays for Q ${qx},${qy}`);
    }
  }
});

test("the held point lands under the pointer, face down", () => {
  for (const [qx, qy] of [[250, 400], [100, 520], [-200, 500], [-390, H]]) {
    const c = curl(W, H, qx, qy, W, H);
    const [bx, by, , face] = bend(W, H, c);
    assert.ok(near(bx, c.qx, 1e-6) && near(by, c.qy, 1e-6), `corner at ${bx},${by} for ${c.qx},${c.qy}`);
    assert.equal(face, -1);
  }
});

test("dragged straight across, the page is a mirror about the fold and lifts round it", () => {
  const c = curl(W, H / 2, 100, H / 2, W, H);
  assert.ok(near(c.dy, 0) && near(c.dx, 1), "the fold is upright");
  assert.ok(c.R > 0, "a roll, not a crease");
  // the part before the fold lies flat; past the roll it lies flat again,
  // two radii up
  const [, , zFlat] = bend(c.px - 10, 100, c);
  assert.equal(zFlat, 0);
  const [, , zOver] = bend(W, 100, c);
  assert.ok(near(zOver, 2 * c.R));
  // half way round the roll the leaf stands up a radius
  const [, , zMid] = bend(c.px + (Math.PI * c.R) / 2, 100, c);
  assert.ok(near(zMid, c.R));
  // every point keeps its distance along the page: the roll is the arc
  assert.ok(c.progress > 0 && c.progress < 1);
});

test("letting go: over past the spine or on a flick, else back", () => {
  assert.equal(letGo(1, -10, 0, W), true);
  assert.equal(letGo(1, 150, 0, W), false);
  assert.equal(letGo(1, 150, -0.8 * W, W), true);
  assert.equal(letGo(-1, 10, 0, W), true);
  assert.equal(letGo(-1, -150, 0, W), false);
});

test("a turn by itself starts at the corner and ends on the other side", () => {
  const a = turnPath(W, H, W, H, 0, true, H);
  assert.ok(near(a.x, W) && near(a.y, H));
  const b = turnPath(W, H, W, H, 1, true, H);
  assert.ok(near(b.x, -W) && near(b.y, H, 1e-9));
  const mid = turnPath(W, H, W, H, 0.5, true, H);
  assert.ok(mid.y < H, "lifted on the way");
  const back = turnPath(W, H, 120, 400, 1, false, H);
  assert.ok(near(back.x, W) && near(back.y, H));
});

const area = (t) => Math.abs((t[1][3] - t[0][3]) * (t[2][4] - t[0][4]) - (t[2][3] - t[0][3]) * (t[1][4] - t[0][4])) / 2;
const triangles = (m) => { const out = []; for (let i = 0; i < m.length; i += 3) out.push([m[i], m[i + 1], m[i + 2]]); return out; };
// where a triangle's corners are on the page (u, v back to page units)
const sOfVertex = (c, xa, v) => (xa + v[3] * W - c.px) * c.dx + (v[4] * H - c.py) * c.dy;

test("the leaf: the whole page, cut along the fold, in the order it lies", () => {
  for (const u of [0.2, 0.5, 0.8, 0.9, 0.95, 0.98, 0.995]) {
    const q = turnPath(W, H, W, H, u, true, H);
    const c = curl(W, H, q.x, q.y, W, H);
    const tris = triangles(leafMesh(c, 0, W, H));
    // the page, all of it once
    const sum = tris.reduce((a, t) => a + area(t), 0);
    assert.ok(near(sum, 1, 1e-6), `u=${u}: the page's area ${sum}`);
    // drawn in the order of distance past the fold: what lies over comes later
    let last = -Infinity;
    for (const t of tris) {
      const s = Math.min(...t.map((v) => sOfVertex(c, 0, v)));
      assert.ok(s >= last - 1e-6, `u=${u}: a strip before one it lies on`);
      last = s;
    }
    // the roll is cut finely however narrow it is: no triangle spans more
    // of it than one step
    const arc = Math.PI * c.R;
    for (const t of tris) {
      const s = t.map((v) => sOfVertex(c, 0, v));
      const a = Math.max(0, Math.min(...s));
      const b = Math.min(arc, Math.max(...s));
      if (b > a) assert.ok(b - a <= arc / 24 + 1e-6, `u=${u}: a facet over ${b - a} of a roll of ${arc}`);
    }
  }
});

test("the leaf ending its turn: the rolled part rises with its distance past the fold", () => {
  const q = turnPath(W, H, W, H, 0.97, true, H);
  const c = curl(W, H, q.x, q.y, W, H);
  assert.ok(c.R < W / 48, "by the end the roll is narrower than a cell of the old grid");
  const vs = leafMesh(c, 0, W, H);
  let s0 = -Infinity;
  let z0 = -Infinity;
  for (const v of [...vs].sort((a, b) => sOfVertex(c, 0, a) - sOfVertex(c, 0, b))) {
    const s = sOfVertex(c, 0, v);
    if (s > s0 + 1e-9) assert.ok(v[2] >= z0 - 1e-9, "higher further on");
    s0 = s;
    z0 = v[2];
  }
  // a left-hand page turned back is the mirror of it
  const cb = curl(-W, H, -q.x, q.y, W, H);
  const back = triangles(leafMesh(cb, -W, W, H));
  assert.ok(near(back.reduce((a, t) => a + area(t), 0), 1, 1e-6));
});
