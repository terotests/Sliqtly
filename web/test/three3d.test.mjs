// node --test: a 3-D world's picture keeps its box's shape (web/three3d.js),
// so the camera's aspect matches the box and pixels stay square.
import test from "node:test";
import assert from "node:assert/strict";
import { worldPictureSize } from "../three3d.js";

test("a small box is drawn at its own size", () => {
  assert.deepEqual(worldPictureSize(400, 225, 2, 1024), [800, 450]);
});

test("a wide box past the limit shrinks as a whole, not one side", () => {
  const [w, h] = worldPictureSize(1600, 900, 2, 1024);
  assert.equal(w, 1024);
  assert.equal(h, 576);
  assert.ok(Math.abs(w / h - 16 / 9) < 0.01);
});

test("a tall box past the limit keeps its shape", () => {
  const [w, h] = worldPictureSize(300, 1200, 2, 1024);
  assert.equal(h, 1024);
  assert.equal(w, 256);
});

test("never less than a pixel a side", () => {
  assert.deepEqual(worldPictureSize(5000, 1, 1, 1024), [1024, 1]);
});
