// node --test: the canvas's pixels per CSS pixel (web/pixels.js). Browser
// zoom raises devicePixelRatio and shrinks the page; the canvas follows the
// screen's pixels while it stays within a large screen's worth of them.
import test from "node:test";
import assert from "node:assert/strict";
import { canvasDpr, BUDGET } from "../pixels.js";

test("an ordinary window keeps the ratio it always had", () => {
  assert.equal(canvasDpr(1, 1440, 900), 1);
  assert.equal(canvasDpr(2, 1440, 900), 2);
  assert.equal(canvasDpr(1.25, 1536, 864), 1.25);
});

test("a big window at a high ratio is held at 2, as before", () => {
  assert.equal(canvasDpr(3, 2560, 1440), 2);
});

test("browser zoom draws at the screen's own pixels, not scaled up", () => {
  // a 1440×900 Retina window at 250% and 500%
  assert.equal(canvasDpr(5, 576, 360), 5);
  assert.equal(canvasDpr(10, 288, 180), 10);
  // a page laid out at its smallest (320×240) keeps the budget
  const k = canvasDpr(10, 320, 240);
  assert.ok(k <= 10 && 320 * 240 * k * k <= BUDGET + 1);
});

test("no side longer than the GPU's texture", () => {
  assert.equal(canvasDpr(10, 900, 300, 4096), 4096 / 900);
  assert.equal(canvasDpr(2, 5000, 300, 8192), 8192 / 5000);
});

test("a missing ratio is 1", () => {
  assert.equal(canvasDpr(0, 800, 600), 1);
  assert.equal(canvasDpr(undefined, 800, 600), 1);
});
