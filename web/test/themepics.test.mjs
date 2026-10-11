// node --test: the theme picker's pictures (web/themepics.js), without a page.
import test from "node:test";
import assert from "node:assert/strict";
import { themePicture, picturesToDraw, fitPage, lookTiles, drawLookTile } from "../themepics.js";

test("a theme's picture is named as the Ranger side names it", () => {
  assert.equal(themePicture("pearl"), "/__theme/pearl");
  assert.equal(themePicture(""), "/__theme/-", "the document's own CSS");
});

test("only the pictures not drawn yet, or drawn in another language, are drawn", () => {
  const rows = "aurora\taurora\tDark\npearl\tpearl\tLight\n\tDocument's own\n\t\n";
  const drawn = new Map([["/__theme/aurora", "en"], ["/__theme/pearl", "fi"]]);
  assert.deepEqual(picturesToDraw(rows, drawn, "en"), ["pearl", ""]);
  assert.deepEqual(picturesToDraw(rows, new Map(), "en"), ["aurora", "pearl", ""]);
});

test("a landscape slide fills the tile, a portrait one sits in its middle", () => {
  assert.deepEqual(fitPage(960, 540, 308, 174), { s: 308 / 960, x: 0, y: (174 - 540 * (308 / 960)) / 2 });
  const p = fitPage(595, 842, 308, 174);
  assert.equal(p.y, 0);
  assert.ok(p.x > 80 && Math.abs(p.x * 2 + 595 * p.s - 308) < 1e-9, "bands at the sides, as wide each");
  assert.deepEqual(fitPage(0, 0, 10, 10), { s: 1, x: 0, y: 0 });
});

test("the 3-D looks' tiles carry their colours; theme rows and broken cells are skipped", () => {
  const rows = "aurora\taurora\tDark\nlook3d:gold\tGold HUD\t3-D\t255,189,56;5,3,0;242,158,31\nlook3d:bad\tBad\t3-D\t1,2;3\n";
  assert.deepEqual(lookTiles(rows), [{ key: "look3d:gold", glow: [255, 189, 56], air: [5, 3, 0], grid: [242, 158, 31] }]);
});

test("a look's tile is drawn in its own colours", () => {
  const calls = [];
  const g = new Proxy({}, { get: (o, k) => (k in o ? o[k] : () => calls.push(k)), set: (o, k, v) => { o[k] = v; if (k === "strokeStyle") calls.push(v); return true; } });
  drawLookTile(g, 220, 124, { glow: [255, 189, 56], air: [5, 3, 0], grid: [242, 158, 31] });
  assert.ok(calls.includes("rgba(242,158,31,0.55)"), "its grid");
  assert.ok(calls.includes("rgba(255,189,56,0.95)"), "its screen");
  assert.ok(calls.includes("strokeRect"));
});
